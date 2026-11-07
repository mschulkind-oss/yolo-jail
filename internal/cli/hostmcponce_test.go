package cli

import (
	"strings"
	"testing"
)

// OQ-MA10, status quo: `yolo host -- claude` does not keep a sign_in = "once" server, and says
// so, naming the server and the jail launch that does. Deleting hostOnceSignInNote's call in the
// host launch fails here.
func TestHostLaunchSaysTheOneSignInIsNotKeptHere(t *testing.T) {
	cfg := `{"packs": ["claude"], "mcp_servers": {"slack": {"url": "https://mcp.slack.com/mcp", "sign_in": "once"}}}`
	rc, _, errs := hostGateRunIn(t, cfg, nil, nil, "claude", nil)
	if rc != 0 {
		t.Fatalf("rc = %d:\n%s", rc, errs)
	}
	for _, want := range []string{"MCP one sign-in is not kept at yolo host: slack", "OQ-MA10", "`yolo -- claude`"} {
		if !strings.Contains(errs, want) {
			t.Errorf("the host launch does not say %q:\n%s", want, errs)
		}
	}
}

// Without a one-sign-in server, or for a program whose pack keeps no MCP sign-ins, nothing is said.
func TestHostLaunchIsSilentWithoutAOneSignInServer(t *testing.T) {
	for name, cfg := range map[string]string{
		"no once server": `{"packs": ["claude"], "mcp_servers": {"slack": {"url": "https://mcp.slack.com/mcp"}}}`,
		"pi":             `{"packs": ["claude", "pi"], "mcp_servers": {"slack": {"url": "https://mcp.slack.com/mcp", "sign_in": "once"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			agent := "claude"
			if name == "pi" {
				agent = "pi"
			}
			_, _, errs := hostGateRunIn(t, cfg, nil, nil, agent, nil)
			if strings.Contains(errs, "MCP one sign-in") {
				t.Errorf("the line was printed:\n%s", errs)
			}
		})
	}
}
