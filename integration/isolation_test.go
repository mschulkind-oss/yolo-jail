package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Isolation tests: AGENTS.md placement, per-side venv/mise shadowing, VS Code
// MCP shadowing, and Overmind
// socket isolation. Each asserts that host-side workspace state does not leak
// through the /workspace bind mount into the jail (and vice versa).

// TestWorkspaceIsolation confirms five independent host↔jail isolation
// properties in ONE jail launch. All five use the tempProject fixture and their
// host-side setups write to DISJOINT workspace paths (AGENTS.md, .venv/*,
// .vscode/mcp.json, .overmind.sock), so co-locating them causes no interference;
// merged to pay the ~12-13s container cold-start once instead of five times.
// Each property keeps its own fenced probe + independent assertion, so leak/
// shadow coverage is fully preserved:
//
//  1. AGENTS.md — workspace file untouched while generated AGENTS context is
//     mounted into ~/.copilot and ~/.gemini.
//  2. host .venv — a host-created .venv (interpreter symlink into the host mise
//     store) is shadowed by the per-side .yolo/home dir, not leaked through the
//     /workspace bind.
//  3. mise store — MISE_DATA_DIR is the neutral /mise and the store is writable.
//  4. .vscode/mcp.json — shadowed with /dev/null so a host VS Code MCP config
//     can't reach the jail agents.
//  5. Overmind — OVERMIND_SOCKET points outside /workspace, and a host
//     .overmind.sock is not visible in the jail.
//
// TestMiseVenvActivation stays SEPARATE: it needs its own mise.toml, a 600s
// timeout, and skipIfInContainer, none of which fit this shared-fixture launch.
func TestWorkspaceIsolation(t *testing.T) {
	requireJail(t)
	dir := tempProject(t)

	// --- host-side setup (disjoint paths) ---
	// (1) workspace AGENTS.md — must survive untouched.
	workspaceAgents := filepath.Join(dir, "AGENTS.md")
	const agentsOriginal = "project-owned agents file\n"
	if err := os.WriteFile(workspaceAgents, []byte(agentsOriginal), 0o644); err != nil {
		t.Fatalf("writing workspace AGENTS.md: %v", err)
	}
	// (2) a host .venv whose interpreter symlink points into the host mise store.
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
	// (4) a host .vscode/mcp.json to be shadowed.
	vscodeDir := filepath.Join(dir, ".vscode")
	if err := os.MkdirAll(vscodeDir, 0o755); err != nil {
		t.Fatalf("creating .vscode: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vscodeDir, "mcp.json"),
		[]byte(`{"servers": {"bad": {"command": "false"}}}`), 0o644); err != nil {
		t.Fatalf("writing .vscode/mcp.json: %v", err)
	}
	// (5) a host .overmind.sock to be shadowed.
	if err := os.WriteFile(filepath.Join(dir, ".overmind.sock"),
		[]byte("fake-host-socket"), 0o644); err != nil {
		t.Fatalf("writing .overmind.sock: %v", err)
	}

	// --- one launch, one fenced probe per property ---
	r := runYolo(t, dir, strings.Join([]string{
		`echo "=== AGENTS ==="; ls /home/agent/.copilot/AGENTS.md && ls /home/agent/.gemini/AGENTS.md; echo "AGENTS rc=$?"`,
		`echo "=== VENV ==="; ls -a /workspace/.venv/ && cat /workspace/.venv/pyvenv.cfg 2>/dev/null; true`,
		`echo "=== MISE ==="; echo "MISE_DATA_DIR=$MISE_DATA_DIR" && touch /mise/.yolo-write-probe && rm /mise/.yolo-write-probe && echo MISE_STORE_WRITABLE`,
		`echo "=== VSCODE ==="; cat /workspace/.vscode/mcp.json`,
		`echo "=== OVERMIND ==="; echo "OVERMIND_SOCKET=$OVERMIND_SOCKET"; cat /workspace/.overmind.sock 2>&1; echo "OVSOCK EXIT=$?"`,
	}, "\n"))

	agents := section(r.stdout, "=== AGENTS ===", "=== VENV ===")
	venv := section(r.stdout, "=== VENV ===", "=== MISE ===")
	mise := section(r.stdout, "=== MISE ===", "=== VSCODE ===")
	vscode := section(r.stdout, "=== VSCODE ===", "=== OVERMIND ===")
	overmind := section(r.stdout, "=== OVERMIND ===", "")

	// (1) home AGENTS.md present, and the workspace file was NOT modified.
	if !strings.Contains(agents, "AGENTS rc=0") {
		t.Fatalf("home ~/.copilot + ~/.gemini AGENTS.md not both present:\n%s", agents)
	}
	if got, err := os.ReadFile(workspaceAgents); err != nil {
		t.Fatalf("re-reading workspace AGENTS.md: %v", err)
	} else if string(got) != agentsOriginal {
		t.Fatalf("workspace AGENTS.md was modified: got %q, want %q", got, agentsOriginal)
	}
	// (2) host venv contents must not show through the shadow.
	if strings.Contains(venv, "host-marker") || strings.Contains(venv, "hostuser") {
		t.Fatalf("host venv leaked into jail:\n%s", venv)
	}
	// (3) mise store is the neutral path and writable.
	if !strings.Contains(mise, "MISE_DATA_DIR=/mise") || !strings.Contains(mise, "MISE_STORE_WRITABLE") {
		t.Fatalf("mise store not neutral+writable:\n%s", mise)
	}
	// (4) .vscode/mcp.json shadowed to empty (/dev/null).
	if strings.Contains(vscode, "bad") || strings.Contains(vscode, "servers") {
		t.Fatalf("workspace .vscode/mcp.json leaked into jail:\n%s", vscode)
	}
	// (5) OVERMIND_SOCKET is set + outside /workspace; host .overmind.sock hidden.
	sock := ""
	for _, ln := range strings.Split(overmind, "\n") {
		ln = strings.TrimSpace(ln)
		if rest, ok := strings.CutPrefix(ln, "OVERMIND_SOCKET="); ok {
			sock = strings.TrimSpace(rest)
		}
	}
	if sock == "" {
		t.Fatalf("OVERMIND_SOCKET should be set, got:\n%s", overmind)
	}
	if strings.HasPrefix(sock, "/workspace") {
		t.Fatalf("OVERMIND_SOCKET must not be inside /workspace (got %s)", sock)
	}
	if strings.Contains(overmind, "fake-host-socket") {
		t.Fatalf("host .overmind.sock leaked into jail:\n%s", overmind)
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
