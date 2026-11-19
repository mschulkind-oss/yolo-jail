package entrypoint

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/mcpsignin"
)

func TestPiManagedMCPFrozenEligibilityAndSuppliedInvalidFacts(t *testing.T) {
	for _, tc := range []struct {
		name, wire string
		helper     bool
	}{
		{"legacy absence", "", true},
		{"eligible without bundle or source", `{"version":1,"decisions":[{"server":"configured-alias","agent":"pi","capturable":false,"header_form":"bare"}]}`, true},
		{"authoritative empty", `{"version":1}`, false},
		{"wrong agent", `{"version":1,"decisions":[{"server":"configured-alias","agent":"claude","capturable":false}]}`, false},
		{"wrong server", `{"version":1,"decisions":[{"server":"other","agent":"pi","capturable":false}]}`, false},
		{"invalid future", `{"version":2}`, false},
		{"invalid shape", `{"version":1,"unknown":true}`, false},
		{"invalid trailing", `{"version":1} {}`, false},
		{"malformed supplied", `unreadable-private-fixture`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := siblingEnv(t, map[string]string{mcpsignin.WireEnv: tc.wire, mcpAuthBrokerEndpointEnv: "synthetic.endpoint", "YOLO_MCP_SERVERS": `{"configured-alias":{"url":"https://mcp.fixture.invalid/mcp","sign_in":"once","oauth":{"client_id":"own-client"}}}`})
			if err := ConfigurePackByName(e, "pi"); err != nil {
				t.Fatal(err)
			}
			remote := readRenderedJSON(t, e.Home, ".pi/agent/mcp.json")["mcpServers"].(map[string]any)["configured-alias"].(map[string]any)
			headers, _ := remote["headers"].(map[string]any)
			if (headers["Authorization"] == "!yolo mcp-auth header configured-alias") != tc.helper {
				t.Fatalf("supplied facts changed nonsource eligibility for %s", tc.name)
			}
			if !tc.helper && remote["oauth"].(map[string]any)["clientId"] != "own-client" {
				t.Fatal("plain route borrowed a client")
			}
		})
	}
}
