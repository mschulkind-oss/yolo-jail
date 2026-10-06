package integration

import (
	"strings"
	"testing"
)

// AN MCP SERVER GATED ON AN env_sources VARIABLE REACHES THE AGENT'S CONFIG ON macos-user.
//
// requires_env asks the environment the agent will have. On this backend that environment is
// the root-owned session env file, and the bootstrap that renders every agent config reads it
// into its generator Env (entrypoint's hydrate_session_env step, named on the bootstrap argv by
// path). Before that it asked its own closed contract, which carries no env_sources value, and
// the server was dropped while the agent's own environment held the variable (measured with a
// gh server gated on GITHUB_TOKEN). The unit tests pin both halves on Linux
// (internal/macosuser/envfile_test.go, internal/entrypoint/darwinsessionenv_test.go).
func TestMacosUserKeepsAnEnvGatedMCPServer(t *testing.T) {
	requireMacosUser(t)
	packHome(t, `{"packs": ["claude"], "env_sources": [{"PROBE_MCP_TOKEN": "integration-probe-not-a-real-token"}]}`)
	ws := macosUserWorkspace(t, `{"mcp_servers": {"probe-gated": {"command": "/usr/bin/true", "requires_env": ["PROBE_MCP_TOKEN"]}}}`)
	r := macosUserRunProbe(t, "env-gated MCP", ws, strings.Join([]string{
		`echo "=== ENV ==="`,
		`echo "TOKEN=${PROBE_MCP_TOKEN:-UNSET}"`,
		`echo "=== CLAUDE ==="`,
		`cat ~/.claude.json 2>&1`,
		`echo "=== END ==="`,
	}, "\n"))
	if env := section(r.stdout, "=== ENV ===", "=== CLAUDE ==="); !strings.Contains(env, "TOKEN=integration-probe-not-a-real-token") {
		t.Fatalf("the agent's own environment lacks PROBE_MCP_TOKEN, so the gate below would "+
			"be asked about a variable nothing delivered:\n%s", env)
	}
	claude := section(r.stdout, "=== CLAUDE ===", "=== END ===")
	if !strings.Contains(claude, `"probe-gated"`) {
		t.Errorf("claude's config lacks the server gated on PROBE_MCP_TOKEN, which the agent's "+
			"environment carries:\n%s\nlaunch output:\n%s", claude, r.stderr)
	}
	if strings.Contains(r.combined(), "MCP server 'probe-gated' skipped") {
		t.Errorf("the bootstrap's gate reported the variable missing:\n%s", r.combined())
	}
}
