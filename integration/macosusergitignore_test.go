package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE HOST'S GLOBAL GITIGNORE APPLIES IN THE macos-user SANDBOX (docs/reference/git-identity.md):
// the host CLI copies the file git's `core.excludesFile` names into the staged context tree, the
// plan names the copy to the bootstrap, and the bootstrap points the sandbox's own
// core.excludesFile at it — as the container launch points the jail's at its read-only bind.
//
// The host's global git config is a temp file named by GIT_CONFIG_GLOBAL, so the launch's own
// `git config --global --get core.excludesFile` reads exactly what this test wrote; the sandbox
// starts under `env -i`, so the variable does not leak in, and what the sandbox's git obeys is
// what the bootstrap wrote there. The sandbox starts in the workspace (macosuseridentity_test.go
// pins that), which the probe makes a repository; `git check-ignore` exits 0 for a path a
// pattern ignores.
func TestMacosUserGlobalGitignoreAppliesInTheSandbox(t *testing.T) {
	requireMacosUser(t)
	packHome(t, `{}`)
	dir := t.TempDir()
	ignore := filepath.Join(dir, "global-ignore")
	if err := os.WriteFile(ignore, []byte("*.yolo-probe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitconfig := filepath.Join(dir, "gitconfig")
	if err := os.WriteFile(gitconfig, []byte("[core]\n\texcludesFile = "+ignore+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := macosUserWorkspace(t, `{}`)

	r := runMacosUser(t, ws, strings.Join([]string{
		`echo "=== GIT ==="`,
		`git init -q . 2>/dev/null`,
		`echo "excludes|$(git config --global --get core.excludesFile 2>/dev/null || echo NONE)"`,
		`if git check-ignore -q x.yolo-probe; then echo "ignored|YES"; else echo "ignored|NO"; fi`,
		`if git check-ignore -q x.kept; then echo "control|YES"; else echo "control|NO"; fi`,
		`echo "=== END GIT ==="`,
	}, "\n"), withEnv("GIT_CONFIG_GLOBAL="+gitconfig))
	if r.rc != 0 || !strings.Contains(r.stdout, "=== END GIT ===") {
		t.Fatalf("the launch did not run its probe (rc %d).\nstdout:\n%s\nstderr:\n%s", r.rc, r.stdout, r.stderr)
	}
	got := macosUserHomeProbeFields(t, r.stdout, "GIT")
	if got["ignored"] != "YES" {
		t.Errorf("the sandbox's git does not ignore x.yolo-probe, which the host's global gitignore "+
			"lists (core.excludesFile there: %q).\nfull output:\n%s", got["excludes"], r.combined())
	}
	if got["control"] != "NO" {
		t.Errorf("the CONTROL failed: x.kept is ignored too, so the probe above says nothing about "+
			"the gitignore.\n%s", r.stdout)
	}
	if !strings.HasPrefix(got["excludes"], "/var/yolo-jail/ctx/") {
		t.Errorf("the sandbox's core.excludesFile is %q, want the staged copy under /var/yolo-jail/ctx", got["excludes"])
	}
}
