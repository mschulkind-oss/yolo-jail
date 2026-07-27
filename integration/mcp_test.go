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
// yolo-jail.jsonc reach both agent configs (copilot mcp-config.json and codex
// settings.json).
func TestCustomMcpServerConfigPropagates(t *testing.T) {
	requireJail(t)
	dir := writeProjectWithPacks(t, mcpConfigWithAgents(
		`"mcp_servers": {"probe-mcp": {"command": "/workspace/probe-mcp.py", "args": ["--stdio"]}}`),
		"copilot", "codex", "claude")
	if err := os.WriteFile(filepath.Join(dir, "probe-mcp.py"), []byte("#!/usr/bin/env python3\n"), 0o644); err != nil {
		t.Fatalf("writing probe-mcp.py: %v", err)
	}

	r := runYolo(t, dir, `python - <<'PY'
import json
from pathlib import Path
copilot = json.loads(Path('/home/agent/.copilot/mcp-config.json').read_text())
codex = Path('/home/agent/.codex/config.toml').read_text()
print(copilot['mcpServers']['probe-mcp']['command'])
# codex is TOML; assert the command lands rather than parsing it.
print([l for l in codex.splitlines() if 'probe-mcp.py' in l][0].split('"')[1])
PY`)
	if r.rc != 0 {
		t.Fatalf("expected rc 0, got %d\n%s", r.rc, r.stderr)
	}
	if n := strings.Count(r.stdout, "/workspace/probe-mcp.py"); n != 2 {
		t.Fatalf("expected probe-mcp command in both agent configs (count 2), got %d\nstdout=%q", n, r.stdout)
	}
}

// TestMcpPresetCanBeEnabled confirms MCP presets from yolo-jail.jsonc enable the
// built-in servers in both agent configs.
func TestMcpPresetCanBeEnabled(t *testing.T) {
	requireJail(t)
	dir := writeProjectWithPacks(t, mcpConfigWithAgents(
		`"mcp_presets": ["chrome-devtools", "sequential-thinking"]`),
		"copilot", "codex", "claude")

	r := runYolo(t, dir, `python - <<'PY'
import json
from pathlib import Path
copilot = json.loads(Path('/home/agent/.copilot/mcp-config.json').read_text())
codex = Path('/home/agent/.codex/config.toml').read_text()
print('chrome-devtools' in copilot['mcpServers'])
print('chrome-devtools' in codex)
print('sequential-thinking' in copilot['mcpServers'])
print('sequential-thinking' in codex)
PY`)
	if r.rc != 0 {
		t.Fatalf("expected rc 0, got %d\n%s", r.rc, r.stderr)
	}
	// The four probe lines are the payload; ignore any leading CLI notices (e.g.
	// an env_sources "file not found" warning from the host user config) that
	// print to stdout ahead of the script output.
	var payload []string
	for _, ln := range strings.Split(r.stdout, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "True" || ln == "False" {
			payload = append(payload, ln)
		}
	}
	want := []string{"True", "True", "True", "True"}
	if strings.Join(payload, ",") != strings.Join(want, ",") {
		t.Fatalf("expected preset servers in both agent configs, got payload %v\nstdout=%q", payload, r.stdout)
	}
}

// TestSameFilePresetAndNullOverrideIsRejected confirms one config file cannot
// both enable a preset (mcp_presets) and null-remove it (mcp_servers) — the
// preflight validator rejects it before any container starts.
func TestSameFilePresetAndNullOverrideIsRejected(t *testing.T) {
	requireJail(t)
	// No pack selection needed: the validator rejects this before a container starts, so
	// what the jail would have contained never matters.
	dir := writeProject(t, mcpConfigWithAgents(
		`"mcp_presets": ["chrome-devtools", "sequential-thinking"],
  "mcp_servers": {"chrome-devtools": null}`))

	r := runYoloCLI(t, dir, "run", "--", "bash", "-lc", "true")
	out := r.combined()
	if r.rc != 1 {
		t.Fatalf("expected rc 1, got %d\n%s", r.rc, out)
	}
	for _, want := range []string{
		"Invalid jail config",
		"preset 'chrome-devtools' is enabled in mcp_presets",
		"within the same config file",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in output, got:\n%s", want, out)
		}
	}
}

// TestWorkspaceMcpConfigsAreIsolated confirms each workspace keeps its own
// generated MCP config files (host-side per-agent overlays at
// <ws>/.yolo/home/{copilot/mcp-config.json,codex/config.toml}), so one
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
	projectA := writeProject(t, base+`"mcp_presets": ["chrome-devtools", "sequential-thinking"]
}`)
	projectB := writeProject(t, base+`"mcp_servers": {"chrome-devtools": null}
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
	hasChromeDevtools := func(dir, agent, file string) bool {
		t.Helper()
		p := filepath.Join(dir, ".yolo", "home", agent, file)
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("reading %s: %v", p, err)
		}
		if strings.HasSuffix(file, ".toml") {
			// The TOML surface spells it [mcp_servers.chrome-devtools]; a substring
			// check is enough here and avoids a TOML dependency in the test.
			return strings.Contains(string(data), "chrome-devtools")
		}
		var cfg struct {
			McpServers map[string]json.RawMessage `json:"mcpServers"`
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			t.Fatalf("parsing %s: %v", p, err)
		}
		_, ok := cfg.McpServers["chrome-devtools"]
		return ok
	}

	if !hasChromeDevtools(projectA, "copilot", "mcp-config.json") {
		t.Fatalf("project_a copilot config missing chrome-devtools")
	}
	if !hasChromeDevtools(projectA, "codex", "config.toml") {
		t.Fatalf("project_a codex config missing chrome-devtools")
	}
	if hasChromeDevtools(projectB, "copilot", "mcp-config.json") {
		t.Fatalf("project_b copilot config should not have chrome-devtools (project_a leaked in)")
	}
	if hasChromeDevtools(projectB, "codex", "config.toml") {
		t.Fatalf("project_b codex config should not have chrome-devtools")
	}
}
