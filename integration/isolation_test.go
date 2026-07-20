package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This file ports the isolation tests from tests/test_jail.py: AGENTS.md
// placement, per-side venv/mise shadowing, VS Code MCP shadowing, and Overmind
// socket isolation. Each asserts that host-side workspace state does not leak
// through the /workspace bind mount into the jail (and vice versa).

// TestWorkspaceAgentsUntouchedAndHomeAgentsPresent confirms the workspace
// AGENTS.md stays untouched while the generated AGENTS context is mounted into
// the app home dirs (~/.copilot, ~/.gemini).
func TestWorkspaceAgentsUntouchedAndHomeAgentsPresent(t *testing.T) {
	requireJail(t)
	dir := tempProject(t)
	workspaceAgents := filepath.Join(dir, "AGENTS.md")
	const original = "project-owned agents file\n"
	if err := os.WriteFile(workspaceAgents, []byte(original), 0o644); err != nil {
		t.Fatalf("writing workspace AGENTS.md: %v", err)
	}

	r := runYolo(t, dir,
		"ls /home/agent/.copilot/AGENTS.md && ls /home/agent/.gemini/AGENTS.md")
	if r.rc != 0 {
		t.Fatalf("expected home AGENTS.md files present (rc %d)\nstderr=%q", r.rc, r.stderr)
	}
	got, err := os.ReadFile(workspaceAgents)
	if err != nil {
		t.Fatalf("re-reading workspace AGENTS.md: %v", err)
	}
	if string(got) != original {
		t.Fatalf("workspace AGENTS.md was modified: got %q, want %q", got, original)
	}
}

// TestHostVenvShadowed confirms a host-created .venv is invisible inside the
// jail. /workspace/.venv is a shadow mount backed by a per-side dir under
// .yolo/home, so a host venv — whose interpreter symlink points into the host's
// mise store and can never resolve in-jail — must not leak through the workspace
// bind.
func TestHostVenvShadowed(t *testing.T) {
	requireJail(t)
	dir := tempProject(t)
	venvBin := filepath.Join(dir, ".venv", "bin")
	if err := os.MkdirAll(venvBin, 0o755); err != nil {
		t.Fatalf("creating .venv/bin: %v", err)
	}
	const hostPython = "/home/hostuser/.local/share/mise/installs/python/3.13.0/bin"
	if err := os.WriteFile(filepath.Join(dir, ".venv", "pyvenv.cfg"),
		[]byte("home = "+hostPython+"\n"), 0o644); err != nil {
		t.Fatalf("writing pyvenv.cfg: %v", err)
	}
	if err := os.Symlink(hostPython+"/python", filepath.Join(venvBin, "python")); err != nil {
		t.Fatalf("creating python symlink: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".venv", "host-marker"),
		[]byte("host venv contents\n"), 0o644); err != nil {
		t.Fatalf("writing host-marker: %v", err)
	}

	r := runYolo(t, dir,
		"ls -a /workspace/.venv/ && cat /workspace/.venv/pyvenv.cfg 2>/dev/null; true")
	if r.rc != 0 {
		t.Fatalf("expected rc 0, got %d\nstderr=%q", r.rc, r.stderr)
	}
	// The jail-side view is a separate per-side dir: none of the host venv's
	// contents may show through.
	if strings.Contains(r.stdout, "host-marker") {
		t.Fatalf("host venv host-marker leaked into jail:\n%s", r.stdout)
	}
	if strings.Contains(r.stdout, "hostuser") {
		t.Fatalf("host venv pyvenv.cfg leaked into jail:\n%s", r.stdout)
	}
}

// TestMiseStoreNeutralPathWritable confirms MISE_DATA_DIR is the fixed neutral
// path /mise and that the store is writable.
func TestMiseStoreNeutralPathWritable(t *testing.T) {
	requireJail(t)
	dir := tempProject(t)
	r := runYolo(t, dir,
		`echo "MISE_DATA_DIR=$MISE_DATA_DIR"`+
			" && touch /mise/.yolo-write-probe"+
			" && rm /mise/.yolo-write-probe"+
			" && echo MISE_STORE_WRITABLE")
	if r.rc != 0 {
		t.Fatalf("expected rc 0, got %d\nstderr=%q", r.rc, r.stderr)
	}
	if !strings.Contains(r.stdout, "MISE_DATA_DIR=/mise") {
		t.Fatalf("expected MISE_DATA_DIR=/mise in stdout, got:\n%s", r.stdout)
	}
	if !strings.Contains(r.stdout, "MISE_STORE_WRITABLE") {
		t.Fatalf("expected MISE_STORE_WRITABLE in stdout, got:\n%s", r.stdout)
	}
}

// TestMiseVenvActivation confirms a mise.toml with _.python.venv activates the
// venv automatically (sets $VIRTUAL_ENV). Skipped in a nested container: mise has
// a re-entrant shim deadlock under podman-in-podman. Uses a longer timeout —
// nested container startup plus venv creation can be slow when mise resolves or
// installs Python versions.
func TestMiseVenvActivation(t *testing.T) {
	requireJail(t)
	skipIfInContainer(t)
	dir := t.TempDir()
	t.Cleanup(func() { forceRemoveContainer(dir) })
	// Pin to 3.13 (already installed in the jail) to avoid a slow download.
	const miseToml = "[tools]\npython = \"3.13\"\n\n" +
		"[env]\n_.python.venv = { path = \".venv\", create = true }\n"
	if err := os.WriteFile(filepath.Join(dir, "mise.toml"), []byte(miseToml), 0o644); err != nil {
		t.Fatalf("writing mise.toml: %v", err)
	}

	r := runYolo(t, dir, "echo $VIRTUAL_ENV", withTimeout(600*time.Second))
	if r.rc != 0 {
		t.Fatalf("expected rc 0, got %d\nstderr=%q", r.rc, r.stderr)
	}
	if !strings.Contains(r.stdout, ".venv") {
		t.Fatalf("expected .venv in $VIRTUAL_ENV, got:\n%s", r.stdout)
	}
}

// TestVscodeMcpShadowed confirms a workspace .vscode/mcp.json is shadowed with
// /dev/null inside the jail, so a host VS Code MCP config cannot reach the agents.
func TestVscodeMcpShadowed(t *testing.T) {
	requireJail(t)
	dir := tempProject(t)
	vscodeDir := filepath.Join(dir, ".vscode")
	if err := os.MkdirAll(vscodeDir, 0o755); err != nil {
		t.Fatalf("creating .vscode: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vscodeDir, "mcp.json"),
		[]byte(`{"servers": {"bad": {"command": "false"}}}`), 0o644); err != nil {
		t.Fatalf("writing .vscode/mcp.json: %v", err)
	}

	r := runYolo(t, dir, "cat /workspace/.vscode/mcp.json")
	// /dev/null is empty, so cat should not output the original mcp.json content.
	if strings.Contains(r.stdout, "bad") {
		t.Fatalf("workspace .vscode/mcp.json leaked into jail:\n%s", r.stdout)
	}
	if strings.Contains(r.stdout, "servers") {
		t.Fatalf("workspace .vscode/mcp.json leaked into jail:\n%s", r.stdout)
	}
}

// TestOvermindSocketIsolated confirms OVERMIND_SOCKET points outside /workspace
// so host and jail Overmind instances don't conflict.
func TestOvermindSocketIsolated(t *testing.T) {
	requireJail(t)
	dir := tempProject(t)
	r := runYolo(t, dir, "echo $OVERMIND_SOCKET")
	socketPath := strings.TrimSpace(r.stdout)
	if socketPath == "" {
		t.Fatalf("OVERMIND_SOCKET should be set, got empty\nstderr=%q", r.stderr)
	}
	if strings.HasPrefix(socketPath, "/workspace") {
		t.Fatalf("OVERMIND_SOCKET must not be inside /workspace (got %s)", socketPath)
	}
}

// TestOvermindHostSockNotVisible confirms a host-side .overmind.sock is not
// visible inside the jail.
func TestOvermindHostSockNotVisible(t *testing.T) {
	requireJail(t)
	dir := tempProject(t)
	// Simulate a host overmind socket file in the workspace.
	if err := os.WriteFile(filepath.Join(dir, ".overmind.sock"),
		[]byte("fake-host-socket"), 0o644); err != nil {
		t.Fatalf("writing .overmind.sock: %v", err)
	}

	r := runYolo(t, dir, "cat /workspace/.overmind.sock 2>&1; echo EXIT=$?")
	// The file should either not exist or be empty (shadowed).
	if strings.Contains(r.stdout, "fake-host-socket") {
		t.Fatalf("host .overmind.sock leaked into jail:\n%s", strings.TrimSpace(r.stdout))
	}
}
