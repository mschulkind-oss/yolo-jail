package integration

import (
	"strings"
	"testing"
)

// claudecredentialstore_test.go asks each real backend what CL-D22's bridge promises
// (docs/design/claude-login-without-interception.md): a launch that selects the claude pack
// points Claude's credential store (CLAUDE_SECURESTORAGE_CONFIG_DIR) at the machine-scope
// `.claude-shared-credentials` directory, the variable reaches a process the launch starts,
// and the directory is one that process can create Claude's lock directories and rename a
// temp file in, which is how Claude takes its refresh and write locks and saves its
// credential. The unit suite pins each launch arm's argv or launch env; only a started jail
// shows the variable surviving the backend's own environment handling and the directory
// really being writable there (a rootless podman's ID mapping, the macos-user sandbox
// account and its Seatbelt profile, Apple Container's VM share).
//
// NO CLAUDE RUNS: the probe is shell only.

// claudeStoreProbe prints the variable, then makes and removes a lock-shaped directory and
// renames a temp file into place inside the directory it names. Unique names per process, so
// two runs sharing a machine store cannot collide.
const claudeStoreProbe = `printf 'STORE|%s\n' "${CLAUDE_SECURESTORAGE_CONFIG_DIR-UNSET}"
d="${CLAUDE_SECURESTORAGE_CONFIG_DIR:-/nonexistent-yolo-it}"
l="$d/.yolo-it-lock-probe-$$"
if mkdir "$l" 2>/dev/null && rmdir "$l"; then echo 'LOCK|ok'; else echo 'LOCK|failed'; fi
f="$d/.yolo-it-rename-probe-$$"
if printf x > "$f.tmp" 2>/dev/null && mv "$f.tmp" "$f" && rm -f "$f"; then echo 'RENAME|ok'; else rm -f "$f.tmp"; echo 'RENAME|failed'; fi
`

// assertClaudeStore checks one backend's probe output against the directory it should name.
func assertClaudeStore(t *testing.T, backend string, res result, want string) {
	t.Helper()
	if res.rc != 0 {
		t.Fatalf("%s: the probe launch failed (rc=%d)\nstdout:\n%s\nstderr:\n%s",
			backend, res.rc, res.stdout, res.stderr)
	}
	fields := map[string]string{}
	for _, line := range strings.Split(res.stdout, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "|"); ok {
			fields[k] = v
		}
	}
	if got := fields["STORE"]; got != want {
		t.Fatalf("%s: CLAUDE_SECURESTORAGE_CONFIG_DIR = %q in the jail, want %q. Claude would "+
			"keep reading ~/.claude/.credentials.json through the link its first refresh "+
			"replaces with a private file (CL-D22)\nstdout:\n%s", backend, got, want, res.stdout)
	}
	for _, k := range []string{"LOCK", "RENAME"} {
		if fields[k] != "ok" {
			t.Errorf("%s: %s in %s = %q: Claude takes its locks by mkdir and saves its "+
				"credential by renaming a temp file there, so a directory it cannot write "+
				"is a login it cannot keep\nstdout:\n%s", backend, k, want, fields[k], res.stdout)
		}
	}
}

func TestPodmanPointsClaudesStoreAtTheWritableSharedDir(t *testing.T) {
	requireJail(t)
	if rt := detectRuntime(); rt != "podman" {
		t.Skipf("podman's argv is what this reads; runtime here is %q", rt)
	}
	dir := writeProjectWithPacks(t, `{"network": {"mode": "bridge"}}`, "claude")
	assertClaudeStore(t, "podman", runYolo(t, dir, claudeStoreProbe),
		"/home/agent/.claude-shared-credentials")
}

func TestAppleContainerPointsClaudesStoreAtTheWritableSharedDir(t *testing.T) {
	dir := appleContainerWorkspace(t)
	assertClaudeStore(t, "Apple Container", runYolo(t, dir, claudeStoreProbe, appleContainerEnv()),
		"/home/agent/.claude-shared-credentials")
}

func TestMacosUserPointsClaudesStoreAtTheWritableSharedDir(t *testing.T) {
	requireMacosUser(t)
	packHome(t, `{"packs": ["claude"]}`)
	ws := macosUserWorkspace(t, `{}`)
	assertClaudeStore(t, "macos-user", runMacosUser(t, ws, claudeStoreProbe),
		macosUserSandboxHome+"/.claude-shared-credentials")
}
