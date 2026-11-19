package entrypoint

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/mcpsignin"
)

func managedMCPSurfaceEnv(t *testing.T, native, capturable, usable, reachable bool) *Env {
	t.Helper()
	facts := mcpsignin.Launch{Version: mcpsignin.LaunchVersion}
	decision := mcpsignin.Decision{Server: "configured-alias", Agent: "claude", Capturable: capturable, UsableRecord: usable, HeaderForm: "json"}
	if native {
		decision.Point = &mcpsignin.Point{Pack: "fixture-bundle", NativeName: "plugin:namespace-original:fixture-key"}
	}
	facts.Decisions = []mcpsignin.Decision{decision}
	body, err := json.Marshal(facts)
	if err != nil {
		t.Fatal(err)
	}
	e := siblingEnv(t, map[string]string{mcpsignin.WireEnv: string(body)})
	if reachable {
		e.Vars[mcpAuthBrokerEndpointEnv] = "/run/yolo/services/synthetic.endpoint"
	}
	return e
}

func TestClaudeManagedMCPRouteThroughConfigurePack(t *testing.T) {
	for _, tc := range []struct {
		name                                          string
		native, capturable, usable, reachable, header bool
	}{
		{"managed capturable", true, true, false, true, true},
		{"plain bootstrap", false, false, false, true, false},
		{"record without bundle", false, false, true, true, true},
		{"credential view", true, false, false, true, false},
		{"unreachable", true, true, true, false, false},
		{"record overrides noncapturable view", true, false, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := managedMCPSurfaceEnv(t, tc.native, tc.capturable, tc.usable, tc.reachable)
			e.Vars["YOLO_MCP_SERVERS"] = `{"configured-alias":{"url":"https://mcp.fixture.invalid/mcp","sign_in":"once","headers":{"aUtHoRiZaTiOn":"stale","AUTHORIZATION":"also-stale","X-Team":"t1"},"oauth":{"client_id":"own-client","callback_port":31111}}}`
			if err := ConfigurePackByName(e, "claude"); err != nil {
				t.Fatal(err)
			}
			config := readRenderedJSON(t, e.Home, ".claude.json")
			server := config["mcpServers"].(map[string]any)["configured-alias"].(map[string]any)
			_, helper := server["headersHelper"]
			if helper != tc.header {
				t.Fatalf("wrong route: %#v", server)
			}
			if tc.header {
				if server["headersHelper"] != "yolo mcp-auth header configured-alias --format json" || server["oauth"] != nil {
					t.Fatal("wrong helper form or own OAuth on helper route")
				}
				headers := server["headers"].(map[string]any)
				if headers["X-Team"] != "t1" {
					t.Fatal("lost nonauth header")
				}
				for key := range headers {
					if strings.EqualFold(key, "Authorization") {
						t.Fatal("static Authorization survived")
					}
				}
			} else if server["oauth"].(map[string]any)["clientId"] != "own-client" {
				t.Fatal("plain route borrowed a client")
			}
		})
	}
}

func TestClaudeManagedMCPInvalidWireKeepsPlainBootstrap(t *testing.T) {
	for _, wire := range []string{"", `{"version":2}`, `{"version":1,"secret":"fixture-do-not-print"}`, `{"version":1} {"version":1}`, `fixture-do-not-print`} {
		t.Run(wire, func(t *testing.T) {
			e := siblingEnv(t, map[string]string{mcpsignin.WireEnv: wire, mcpAuthBrokerEndpointEnv: "synthetic.endpoint", "YOLO_MCP_SERVERS": `{"configured-alias":{"url":"https://mcp.fixture.invalid/mcp","sign_in":"once"}}`})
			var warnings bytes.Buffer
			e.Stderr = &warnings
			if err := ConfigurePackByName(e, "claude"); err != nil {
				t.Fatal(err)
			}
			server := readRenderedJSON(t, e.Home, ".claude.json")["mcpServers"].(map[string]any)["configured-alias"].(map[string]any)
			if server["headersHelper"] != nil || strings.Contains(warnings.String(), "fixture-do-not-print") || !strings.Contains(warnings.String(), "native plain bootstrap") {
				t.Fatalf("invalid wire enabled a source or leaked input: %#v; %s", server, warnings.String())
			}
		})
	}
}

func TestPiManagedMCPNonsourceNeedsNoNativePoint(t *testing.T) {
	e := siblingEnv(t, map[string]string{mcpAuthBrokerEndpointEnv: "synthetic.endpoint", "YOLO_MCP_SERVERS": `{"configured-alias":{"url":"https://mcp.fixture.invalid/mcp","sign_in":"once","headers":{"AUTHORIZATION":"stale","X-Team":"t1"},"oauth":{"client_id":"must-not-borrow"}}}`})
	if err := ConfigurePackByName(e, "pi"); err != nil {
		t.Fatal(err)
	}
	remote := readRenderedJSON(t, e.Home, ".pi/agent/mcp.json")["mcpServers"].(map[string]any)["configured-alias"].(map[string]any)
	headers := remote["headers"].(map[string]any)
	if headers["Authorization"] != "!yolo mcp-auth header configured-alias" || headers["AUTHORIZATION"] != nil || headers["X-Team"] != "t1" || remote["oauth"] != nil {
		t.Fatalf("nonsource bare-byte compatibility changed: %#v", remote)
	}
}
