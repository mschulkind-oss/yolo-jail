package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This file ports the MCP-config tests from tests/test_jail.py. The in-jail
// `python - <<'PY' ... PY` probes are kept verbatim: they parse the jail image's
// generated agent config files (copilot mcp-config.json, gemini settings.json)
// with the image's python3, unaffected by host-side Python ejection.

// mcpConfigWithAgents is the standard fixture (copilot + gemini + claude, curl +
// grep blocks, bridge net) with an extra top-level key merged in — the Go
// equivalent of the Python tests reading temp_project's config, adding a key, and
// writing it back.
func mcpConfigWithAgents(extra string) string {
	return `{
  "agents": ["copilot", "gemini", "claude"],
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
// yolo-jail.jsonc reach both agent configs (copilot mcp-config.json and gemini
// settings.json).
func TestCustomMcpServerConfigPropagates(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, mcpConfigWithAgents(
		`"mcp_servers": {"probe-mcp": {"command": "/workspace/probe-mcp.py", "args": ["--stdio"]}}`))
	if err := os.WriteFile(filepath.Join(dir, "probe-mcp.py"), []byte("#!/usr/bin/env python3\n"), 0o644); err != nil {
		t.Fatalf("writing probe-mcp.py: %v", err)
	}

	r := runYolo(t, dir, `python - <<'PY'
import json
from pathlib import Path
copilot = json.loads(Path('/home/agent/.copilot/mcp-config.json').read_text())
gemini = json.loads(Path('/home/agent/.gemini/settings.json').read_text())
print(copilot['mcpServers']['probe-mcp']['command'])
print(gemini['mcpServers']['probe-mcp']['command'])
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
	dir := writeProject(t, mcpConfigWithAgents(
		`"mcp_presets": ["chrome-devtools", "sequential-thinking"]`))

	r := runYolo(t, dir, `python - <<'PY'
import json
from pathlib import Path
copilot = json.loads(Path('/home/agent/.copilot/mcp-config.json').read_text())
gemini = json.loads(Path('/home/agent/.gemini/settings.json').read_text())
print('chrome-devtools' in copilot['mcpServers'])
print('chrome-devtools' in gemini['mcpServers'])
print('sequential-thinking' in copilot['mcpServers'])
print('sequential-thinking' in gemini['mcpServers'])
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
// <ws>/.yolo/home/{copilot/mcp-config.json,gemini/settings.json}), so one
// workspace's servers never leak into another's.
func TestWorkspaceMcpConfigsAreIsolated(t *testing.T) {
	requireJail(t)
	base := `{
  "agents": ["copilot", "gemini"],
  "security": {"blocked_tools": ["curl"]},
  "network": {"mode": "bridge"},
  `
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

	hasChromeDevtools := func(dir, agent, file string) bool {
		t.Helper()
		p := filepath.Join(dir, ".yolo", "home", agent, file)
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("reading %s: %v", p, err)
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
	if !hasChromeDevtools(projectA, "gemini", "settings.json") {
		t.Fatalf("project_a gemini config missing chrome-devtools")
	}
	if hasChromeDevtools(projectB, "copilot", "mcp-config.json") {
		t.Fatalf("project_b copilot config should not have chrome-devtools (project_a leaked in)")
	}
	if hasChromeDevtools(projectB, "gemini", "settings.json") {
		t.Fatalf("project_b gemini config should not have chrome-devtools")
	}
}
