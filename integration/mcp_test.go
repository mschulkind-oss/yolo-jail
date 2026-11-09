package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// MCP-config tests. The in-jail `python - <<'PY' ... PY` probes parse the jail
// image's
// generated agent config files (copilot mcp-config.json, codex config.toml)
// with the image's python3, unaffected by host-side Python ejection.

// mcpConfigWithAgents is the standard fixture (copilot + codex + claude, curl +
// grep blocks, bridge net) with an extra top-level key merged in — the Go
// equivalent of the Python tests reading temp_project's config, adding a key, and
// writing it back.
// The `packs` selection is NOT here: it is user-scope only (see writeProjectWithPacks), so
// the workspace config carries everything else and the caller selects packs separately.
func mcpConfigWithAgents(extra string) string {
	return `{
  "security": {
    "blocked_tools": [
      "curl",
      {"name": "grep", "message": "NO GREP ALLOWED", "suggestion": "use rg"}
    ]
  },
  "network": {"mode": "bridge"},
  ` + extra + `
}`
}

// TestCustomMcpServerConfigPropagates confirms custom MCP servers from
// yolo-jail.jsonc reach each agent's config: copilot's mcp-config.json, codex's
// config.toml, and pi's own ~/.pi/agent/mcp.json, the file pi's built-in MCP
// client reads (pi 0.99.0 and later), with nothing written for pi-mcp-adapter.
func TestCustomMcpServerConfigPropagates(t *testing.T) {
	requireJail(t)
	dir := writeProjectWithPacks(t, mcpConfigWithAgents(
		`"mcp_servers": {"probe-mcp": {"command": "/workspace/probe-mcp.py", "args": ["--stdio"]}}`),
		"copilot", "codex", "claude", "pi")
	if err := os.WriteFile(filepath.Join(dir, "probe-mcp.py"), []byte("#!/usr/bin/env python3\n"), 0o644); err != nil {
		t.Fatalf("writing probe-mcp.py: %v", err)
	}

	r := runYolo(t, dir, `python - <<'PY'
import json
from pathlib import Path
copilot = json.loads(Path('/home/agent/.copilot/mcp-config.json').read_text())
codex = Path('/home/agent/.codex/config.toml').read_text()
pi = json.loads(Path('/home/agent/.pi/agent/mcp.json').read_text())
print(copilot['mcpServers']['probe-mcp']['command'])
# codex is TOML; assert the command lands rather than parsing it.
print([l for l in codex.splitlines() if 'probe-mcp.py' in l][0].split('"')[1])
print(pi['mcpServers']['probe-mcp']['command'])
print('PI_ADAPTER_FILE=' + str(Path('/home/agent/.pi/agent/mcp-adapter.json').exists()))
PY`)
	if r.rc != 0 {
		t.Fatalf("expected rc 0, got %d\n%s", r.rc, r.stderr)
	}
	if n := strings.Count(r.stdout, "/workspace/probe-mcp.py"); n != 3 {
		t.Fatalf("expected probe-mcp command in all three agent configs (count 3), got %d\nstdout=%q", n, r.stdout)
	}
	if !strings.Contains(r.stdout, "PI_ADAPTER_FILE=False") {
		t.Fatalf("the jail wrote pi-mcp-adapter's mcp-adapter.json; pi's servers belong in "+
			"mcp.json alone\nstdout=%q", r.stdout)
	}
}

func TestRemoteMcpServerIsWithheldFromAnAgentWithNoRemoteForm(t *testing.T) {
	requireJail(t)
	dir := writeProjectWithPacks(t, mcpConfigWithAgents(
		`"mcp_servers": {"linear": {"url": "https://mcp.linear.app/mcp"}}`), "copilot")
	r := runYolo(t, dir, `grep -c mcp.linear.app /home/agent/.copilot/mcp-config.json || true`)
	if r.rc != 0 {
		t.Fatalf("rc %d\n%s", r.rc, r.stderr)
	}
	if strings.TrimSpace(r.stdout) != "0" {
		t.Fatalf("Copilot config holds the remote server: %q", r.stdout)
	}
	want := "linear: copilot has no remote MCP form in yolo yet, so no agent in this jail is configured for it"
	if !strings.Contains(r.stderr+r.stdout, want) {
		t.Fatalf("missing remote-form notice %q\n%s", want, r.stderr+r.stdout)
	}
}

func TestRemoteMcpServerOffDisablesTheRenderedEntry(t *testing.T) {
	requireJail(t)
	dir := writeProjectWithPacks(t, mcpConfigWithAgents(
		`"mcp_servers": {"linear": "off"}`), "codex")
	r := runYolo(t, dir, `python - <<'PY'
import tomllib
from pathlib import Path
servers = tomllib.loads(Path('/home/agent/.codex/config.toml').read_text()).get('mcp_servers', {})
print('LINEAR_PRESENT=' + str('linear' in servers))
PY`)
	if r.rc != 0 {
		t.Fatalf("rc %d\n%s", r.rc, r.stderr)
	}
	if !strings.Contains(r.stdout, "LINEAR_PRESENT=False") {
		t.Fatalf("off entry reached Codex:\n%s", r.stdout)
	}
}

func TestRemoteMcpServerReachesCodexClaudeAndOpencode(t *testing.T) {
	requireJail(t)
	dir := writeProjectWithPacks(t, mcpConfigWithAgents(
		`"mcp_servers": {"linear": {"url": "https://mcp.linear.app/mcp", "headers": {"X-Org": "acme"}, "oauth": {"client_id": "client-1", "scopes": ["read"], "callback_port": 3118}}}`), "codex", "claude", "opencode")
	r := runYolo(t, dir, `python - <<'PY'
import json, tomllib
from pathlib import Path
codex = tomllib.loads(Path('/home/agent/.codex/config.toml').read_text())['mcp_servers']['linear']
claude = json.loads(Path('/home/agent/.claude.json').read_text())['mcpServers']['linear']
opencode = json.loads(Path('/home/agent/.config/opencode/opencode.json').read_text())['mcp']['linear']
print('CODEX=' + str(codex['url'] == 'https://mcp.linear.app/mcp' and codex['http_headers']['X-Org'] == 'acme' and codex['oauth']['client_id'] == 'client-1' and codex['scopes'] == ['read']))
print('CLAUDE=' + str(claude['type'] == 'http' and claude['url'] == 'https://mcp.linear.app/mcp' and claude['headers']['X-Org'] == 'acme' and claude['oauth']['clientId'] == 'client-1' and 'scopes' not in claude))
print('OPENCODE=' + str(opencode['type'] == 'remote' and opencode['url'] == 'https://mcp.linear.app/mcp' and opencode['headers']['X-Org'] == 'acme' and opencode['oauth']['clientId'] == 'client-1'))
PY`)
	if r.rc != 0 {
		t.Fatalf("rc %d\n%s", r.rc, r.stderr)
	}
	for _, want := range []string{"CODEX=True", "CLAUDE=True", "OPENCODE=True"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("remote MCP render missing %q\n%s", want, r.stdout)
		}
	}
}

// TestRetiredMcpPresetsKeyIsRefused confirms the retired `mcp_presets` key refuses the launch
// before any container starts, naming both next steps (docs/design/mcp-presets-removal.md §13
// step 3): the chrome-devtools pack and the user's own sequential-thinking entry.
func TestRetiredMcpPresetsKeyIsRefused(t *testing.T) {
	requireJail(t)
	// No pack selection needed: the validator refuses this before a container starts.
	dir := writeProject(t, mcpConfigWithAgents(`"mcp_presets": ["chrome-devtools", "sequential-thinking"]`))

	// Host semantics: inside a jail the config is the host's snapshot, and the key only warns there.
	r := runCommand(t, dir, append(jailRunArgs(), "--", "bash", "-lc", "true"), withHostSemantics())
	out := r.combined()
	if r.rc != 1 {
		t.Fatalf("expected rc 1, got %d\n%s", r.rc, out)
	}
	for _, want := range []string{
		"Invalid jail config",
		"config.mcp_presets: REMOVED",
		"run `yolo pack add chrome-devtools` on the host",
		"@modelcontextprotocol/server-sequential-thinking",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in output, got:\n%s", want, out)
		}
	}
}

func TestRemoteMcpEffectiveTransportBindsRenderedHeaders(t *testing.T) {
	requireJail(t)
	const endpoint = "https://mcp.linear.app/mcp"
	for _, tc := range []struct {
		name, userEntry, workspaceEntry, wantHeader, notWantHeader string
	}{
		{
			name:           "transport-only override clears inherited headers",
			userEntry:      `{"url":"` + endpoint + `","headers":{"Authorization":"Bearer inherited-secret"}}`,
			workspaceEntry: `{"transport":"sse"}`,
			notWantHeader:  "Bearer inherited-secret",
		},
		{
			name:           "same URL with omitted transport inherits SSE and headers",
			userEntry:      `{"url":"` + endpoint + `","transport":"sse","headers":{"Authorization":"Bearer inherited-secret"}}`,
			workspaceEntry: `{"url":"` + endpoint + `"}`,
			wantHeader:     "Bearer inherited-secret",
		},
		{
			name:           "explicit replacement headers are rendered",
			userEntry:      `{"url":"` + endpoint + `","headers":{"Authorization":"Bearer inherited-secret"}}`,
			workspaceEntry: `{"transport":"sse","headers":{"Authorization":"Bearer replacement-secret"}}`,
			wantHeader:     "Bearer replacement-secret",
			notWantHeader:  "Bearer inherited-secret",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packHome(t, `{"packs":["claude"],"mcp_servers":{"linear":`+tc.userEntry+`}}`)
			dir := writeProject(t, mcpConfigWithAgents(`"mcp_servers":{"linear":`+tc.workspaceEntry+`}`))
			r := runYolo(t, dir, `python - <<'PY'
import json
from pathlib import Path
server = json.loads(Path('/home/agent/.claude.json').read_text())['mcpServers']['linear']
print('TRANSPORT=' + str(server.get('type')))
print('URL=' + str(server.get('url')))
print('AUTH=' + str(server.get('headers', {}).get('Authorization')))
PY`)
			if r.rc != 0 {
				t.Fatalf("rc %d\n%s", r.rc, r.stderr)
			}
			for _, want := range []string{"TRANSPORT=sse", "URL=" + endpoint} {
				if !strings.Contains(r.stdout, want) {
					t.Errorf("rendered Claude MCP config lacks %q\n%s", want, r.stdout)
				}
			}
			if tc.wantHeader != "" && !strings.Contains(r.stdout, tc.wantHeader) {
				t.Errorf("rendered Claude MCP config lost expected header %q\n%s", tc.wantHeader, r.stdout)
			}
			if tc.notWantHeader != "" && strings.Contains(r.stdout, tc.notWantHeader) {
				t.Errorf("rendered Claude MCP config retained unsafe header %q\n%s", tc.notWantHeader, r.stdout)
			}
		})
	}
}

// TestWorkspaceMcpConfigsAreIsolated confirms each workspace keeps its own
// generated MCP config files (host-side per-agent overlays at
// <ws>/.yolo/setups/<setup>/home/{copilot/mcp-config.json,codex/config.toml}), so one
// workspace's servers never leak into another's.
func TestWorkspaceMcpConfigsAreIsolated(t *testing.T) {
	requireJail(t)
	base := `{
  "security": {"blocked_tools": ["curl"]},
  "network": {"mode": "bridge"},
  `
	// One user config for both workspaces: `packs` is user-scope, so the two projects
	// necessarily share a selection. That is fine for what this test isolates — MCP servers
	// come from the WORKSPACE config, which is exactly the split being verified.
	packHome(t, `{"packs": ["copilot", "codex"]}`)
	projectA := writeProject(t, base+`"mcp_servers": {"probe-mcp": {"command": "/workspace/probe-mcp.py"}}
}`)
	projectB := writeProject(t, base+`"mcp_servers": {}
}`)

	if r := runYolo(t, projectA, "true"); r.rc != 0 {
		t.Fatalf("project_a run failed (rc %d): %s", r.rc, r.stderr)
	}
	if r := runYolo(t, projectB, "true"); r.rc != 0 {
		t.Fatalf("project_b run failed (rc %d): %s", r.rc, r.stderr)
	}

	// Codec-aware: copilot's surface is JSON, codex's is TOML. A JSON parse of the
	// TOML file fails on its leading comment, so the check is per-codec rather than
	// one shape for both.
	hasProbe := func(dir, agent, file string) bool {
		t.Helper()
		p := filepath.Join(jailHome(dir), agent, file)
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("reading %s: %v", p, err)
		}
		if strings.HasSuffix(file, ".toml") {
			// The TOML surface spells it [mcp_servers.probe-mcp]; a substring
			// check is enough here and avoids a TOML dependency in the test.
			return strings.Contains(string(data), "probe-mcp")
		}
		var cfg struct {
			McpServers map[string]json.RawMessage `json:"mcpServers"`
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			t.Fatalf("parsing %s: %v", p, err)
		}
		_, ok := cfg.McpServers["probe-mcp"]
		return ok
	}

	if !hasProbe(projectA, "copilot", "mcp-config.json") {
		t.Fatalf("project_a copilot config missing probe-mcp")
	}
	if !hasProbe(projectA, "codex", "config.toml") {
		t.Fatalf("project_a codex config missing probe-mcp")
	}
	if hasProbe(projectB, "copilot", "mcp-config.json") {
		t.Fatalf("project_b copilot config should not have probe-mcp (project_a leaked in)")
	}
	if hasProbe(projectB, "codex", "config.toml") {
		t.Fatalf("project_b codex config should not have probe-mcp")
	}
}

// TestMcpChromeDevtoolsPackReachesEveryAgent confirms the chrome-devtools PACK — a `kind: "mcp"`
// entry composed on the host into YOLO_MCP_SERVERS (docs/design/mcp-presets-removal.md §14) —
// puts a chrome-devtools server in copilot's mcp-config.json, codex's config.toml and claude's
// ~/.claude.json, each running the pack's wrapper under the jail's home through /bin/sh, with no
// core branch naming any of the three; that the wrapper is where the entry says and reports the
// server and the image's chromium; and that `mcp_servers: {"chrome-devtools": null}` removes it
// from all three. `--check` starts nothing and installs nothing. It also reads the launchers the
// boot generated (mcp-presets-removal.md MP-D9): an agent's carries the server's program in the
// refresh it runs before exec, and the program's own carries no update of its own; with the null,
// the agent's carries nothing for it.
func TestMcpChromeDevtoolsPackReachesEveryAgent(t *testing.T) {
	requireJail(t)
	probe := `python - <<'PY'
import json
from pathlib import Path
home = Path('/home/agent')
wrapper = '/home/agent/.local/share/yolo-chrome-devtools/chrome-devtools-mcp-wrapper'
copilot = json.loads((home / '.copilot/mcp-config.json').read_text()).get('mcpServers', {})
claude = json.loads((home / '.claude.json').read_text()).get('mcpServers', {})
codex = (home / '.codex/config.toml').read_text()
print('COPILOT=' + str(copilot.get('chrome-devtools', {}).get('args') == [wrapper]))
print('CLAUDE=' + str(claude.get('chrome-devtools', {}).get('command') == '/bin/sh'))
print('CODEX=' + str('chrome-devtools' in codex and wrapper in codex))
def baked(name, var):
    for line in (home / '.yolo/bin/launch' / name).read_text().splitlines():
        if line.startswith(var + '='):
            return line.split('=', 1)[1]
    return 'none'
print('AGENT_REFRESHES=' + str('chrome-devtools-mcp' in baked('claude', 'SERVERS_NPM')))
print('SERVER_UPDATES=' + baked('chrome-devtools-mcp', 'UPDATES_ENABLED'))
PY
/bin/sh /home/agent/.local/share/yolo-chrome-devtools/chrome-devtools-mcp-wrapper --check || true
/bin/sh /home/agent/.local/share/yolo-chrome-devtools/chrome-devtools-mcp-wrapper --boot-check; echo BOOT_CHECK_RC=$?`

	dir := writeProjectWithPacks(t, mcpConfigWithAgents(`"mcp_servers": {}`),
		"copilot", "codex", "claude", "chrome-devtools")
	r := runYolo(t, dir, probe)
	if r.rc != 0 {
		t.Fatalf("expected rc 0, got %d\n%s", r.rc, r.stderr)
	}
	for _, want := range []string{"COPILOT=True", "CLAUDE=True", "CODEX=True",
		"AGENT_REFRESHES=True", "SERVER_UPDATES=0",
		"chrome-devtools-mcp: /home/agent/.yolo/bin/launch/chrome-devtools-mcp", "browser: /",
		"BOOT_CHECK_RC=0"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("selected: no %q\nstdout=%q\nstderr=%q", want, r.stdout, r.stderr)
		}
	}
	// The pack's boot_check (mcp-presets-removal.md OQ-MP9) ran at boot and, with the image's
	// chromium there, said nothing.
	if strings.Contains(r.stderr, "chrome-devtools: browser: none found") || strings.Contains(r.stderr, "its boot check") {
		t.Errorf("the boot warned about the browser with the image's chromium present:\n%s", r.stderr)
	}
	if bootLog, err := os.ReadFile(filepath.Join(dir, ".yolo", "logs", "boot.log")); err != nil {
		t.Errorf("reading the boot log: %v", err)
	} else if !strings.Contains(string(bootLog), "boot check chrome-devtools") {
		t.Errorf("the boot log does not record the pack's boot check running:\n%s", bootLog)
	}

	dir = writeProjectWithPacks(t, mcpConfigWithAgents(`"mcp_servers": {"chrome-devtools": null}`),
		"copilot", "codex", "claude", "chrome-devtools")
	r = runYolo(t, dir, probe)
	if r.rc != 0 {
		t.Fatalf("expected rc 0, got %d\n%s", r.rc, r.stderr)
	}
	for _, want := range []string{"COPILOT=False", "CLAUDE=False", "CODEX=False", "AGENT_REFRESHES=False"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("nulled: no %q\nstdout=%q", want, r.stdout)
		}
	}
}

// THE chrome-devtools PACK ON macos-user, asked of a real sandbox (docs/design/mcp-presets-removal.md
// §7, §14: "selecting the pack produces a named missing-binary report rather than a blanket 'not
// delivered on this backend'"). The plan composes the pack's entry for the sandbox account's home
// and the bootstrap renders it into claude's ~/.claude.json; the wrapper the entry runs is where it
// says, and its --check names the server's launcher and a browser — the GitHub macOS runners ship
// Google Chrome in /Applications. --check starts nothing and installs nothing. The unit tests pin
// the composition on Linux (internal/macosuser/mcpbootstrap_test.go).
func TestMacosUserChromeDevtoolsPackReachesClaude(t *testing.T) {
	requireMacosUser(t)
	packHome(t, `{"packs": ["claude", "chrome-devtools"]}`)
	ws := macosUserWorkspace(t, `{}`)
	r := macosUserRunProbe(t, "chrome-devtools pack", ws, strings.Join([]string{
		`echo "=== CLAUDE ==="`,
		`cat ~/.claude.json 2>&1`,
		`echo "=== CHECK ==="`,
		`/bin/sh ~/.local/share/yolo-chrome-devtools/chrome-devtools-mcp-wrapper --check 2>&1 || true`,
		`echo "=== END ==="`,
	}, "\n"))
	claude := section(r.stdout, "=== CLAUDE ===", "=== CHECK ===")
	if !strings.Contains(claude, `"chrome-devtools"`) ||
		!strings.Contains(claude, "/.local/share/yolo-chrome-devtools/chrome-devtools-mcp-wrapper") ||
		strings.Contains(claude, "/home/agent") {
		t.Errorf("claude's config does not run the pack's wrapper under the sandbox home:\n%s\nlaunch output:\n%s",
			claude, r.stderr)
	}
	check := section(r.stdout, "=== CHECK ===", "=== END ===")
	if !strings.Contains(check, "chrome-devtools-mcp: ") || !strings.Contains(check, "/.yolo/bin/launch/chrome-devtools-mcp") {
		t.Errorf("the wrapper did not find the sandbox's launcher:\n%s", check)
	}
	if !strings.Contains(check, "browser: /") {
		t.Errorf("the wrapper named no browser on a runner that ships Chrome:\n%s", check)
	}
}
