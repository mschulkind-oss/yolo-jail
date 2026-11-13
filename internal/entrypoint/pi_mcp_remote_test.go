package entrypoint

import (
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// pi's remote MCP form (packs/pi: mcp_remote_transports = ["http"], derive.lua's
// pi_mcp_servers). Stock pi 1.0.1 takes a streamable-HTTP server as `url`, `headers` and a
// camelCase `oauth` table, and rejects SSE (dist/core/mcp-servers.js validateMcpServerConfig),
// so through the SHIPPED pi pack an http server lands in ~/.pi/agent/mcp.json in pi's spelling
// and an sse server is withheld with the boot notice naming pi. Deleting the pack's
// declaration withholds the http server too; deleting the mapping writes yolo's snake_case.
func TestPiMcpRendersARemoteServerInPisOwnForm(t *testing.T) {
	e, home := piMCPEnv(t)
	var notices strings.Builder
	e.Stderr = &notices
	e.Vars["YOLO_MCP_SERVERS"] = `{
		"probe-mcp": {"command": "/bin/probe-mcp", "args": ["--stdio"]},
		"linear": {"url": "https://mcp.linear.app/mcp", "headers": {"X-Org": "${ORG}"},
		           "oauth": {"client_id": "c1", "callback_port": 3118, "scopes": ["read", "write"]}},
		"plain": {"url": "https://mcp.example.test/mcp", "transport": "http"},
		"legacy": {"url": "https://legacy.example.test/sse", "transport": "sse"}}`
	bootJail(t, e, shippedPi(t))

	got := readRenderedJSON(t, home, piMCPRel)["mcpServers"]
	want := map[string]any{
		"probe-mcp": probeMCPTable()["probe-mcp"],
		"linear": map[string]any{"url": "https://mcp.linear.app/mcp", "headers": map[string]any{"X-Org": "${ORG}"},
			"oauth": map[string]any{"clientId": "c1", "callbackPort": float64(3118), "scope": "read write"}},
		"plain": map[string]any{"url": "https://mcp.example.test/mcp"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("~/.pi/agent/mcp.json mcpServers:\n got %#v\nwant %#v", got, want)
	}
	if line := `legacy: pi has no remote MCP form in yolo yet, so no agent in this jail is configured for it`; !strings.Contains(notices.String(), line) {
		t.Fatalf("boot notices = %q, want %q", notices.String(), line)
	}
}

// The header route (docs/design/mcp-auth-sharing.md §9.6; the plan's ID-23): in a jail whose
// mcp-auth broker runs, a server the user made a one-sign-in server renders with pi's `!command`
// Authorization header running `yolo mcp-auth header <name>` and no oauth table, so pi starts no
// sign-in of its own; the user's other headers stay, and `sign_in` never reaches pi's file.
// Without the broker the same server renders in the plain form, oauth and all. Deleting the
// MCPHeaderRoute wiring in deriveCtx, or the derive's branch, fails the first half.
func TestPiMcpTakesTheHeaderRouteForAOneSignInServerWhereTheBrokerRuns(t *testing.T) {
	servers := `{
		"slack": {"url": "https://mcp.slack.com/mcp", "sign_in": "once", "headers": {"X-Team": "t1", "authorization": "stale"},
		          "oauth": {"client_id": "c1", "callback_port": 3118}},
		"linear": {"url": "https://mcp.linear.app/mcp"}}`
	for _, tc := range []struct {
		name     string
		endpoint string
		slack    map[string]any
	}{
		{"broker runs", "/run/yolo/services/mcp-auth-broker.endpoint", map[string]any{"url": "https://mcp.slack.com/mcp",
			"headers": map[string]any{"X-Team": "t1", "Authorization": "!yolo mcp-auth header slack"}}},
		{"no broker", "", map[string]any{"url": "https://mcp.slack.com/mcp",
			"headers": map[string]any{"X-Team": "t1", "authorization": "stale"},
			"oauth":   map[string]any{"clientId": "c1", "callbackPort": float64(3118)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, home := piMCPEnv(t)
			e.Vars["YOLO_MCP_SERVERS"] = servers
			if tc.endpoint != "" {
				e.Vars[mcpAuthBrokerEndpointEnv] = tc.endpoint
			}
			bootJail(t, e, shippedPi(t))
			got := readRenderedJSON(t, home, piMCPRel)["mcpServers"]
			want := map[string]any{"slack": tc.slack, "linear": map[string]any{"url": "https://mcp.linear.app/mcp"}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("mcpServers:\n got %#v\nwant %#v", got, want)
			}
		})
	}
}

// At the host the verb reads the broker's store itself, on Linux; on a Mac nothing at that notch
// takes a sign-in over, so the route is off there.
func TestMCPHeaderRouteAtEachNotch(t *testing.T) {
	jail := &Env{Vars: map[string]string{}}
	if mcpHeaderRoute(jail) {
		t.Fatal("a jail with no broker endpoint took the header route")
	}
	jail.Vars[mcpAuthBrokerEndpointEnv] = "/run/x.endpoint"
	if !mcpHeaderRoute(jail) {
		t.Fatal("a jail whose broker runs did not take the header route")
	}
	host := &Env{Vars: map[string]string{}, hostTarget: true}
	if got, want := mcpHeaderRoute(host), runtime.GOOS == "linux"; got != want {
		t.Fatalf("host route = %v, want %v on %s", got, want, runtime.GOOS)
	}
}
