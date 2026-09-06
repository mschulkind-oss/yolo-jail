package integration

// liveoverlayguard_test.go is the end-to-end pin on the live-overlay refusal:
// run from INSIDE a yolo jail, a launch whose workspace is /workspace would jail
// the running session's own live tree over its own home. The unit tier carries
// the argument (internal/cli/run/liveoverlayguard_test.go: the truth table, a
// real Run that refuses before any side effect, and an AST call-site pin); what
// only a real in-jail invocation adds is that the AMBIENT YOLO_VERSION reaches
// the guard through the CLI's own environment handling.
//
// Which is why this file gates on the jail itself and not on requireJail alone.
// requireJail skips under -short; it does not prove a jail. A GitHub runner has
// no ambient YOLO_VERSION (so the guard cannot fire) and no /workspace (so
// cmd.Dir cannot even be entered), and this test failed there on both arches,
// every run, with `chdir /workspace: no such file or directory` — a red badge
// for a condition CI cannot represent. Skipping it off-jail costs no coverage
// of the guard: the unit tier above runs in `check-ci` on every push.

import (
	"os"
	"strings"
	"testing"
)

// liveWorkspaceDir is where every container backend binds the workspace, and the
// literal the guard compares against (internal/cli/run/liveoverlayguard.go).
const liveWorkspaceDir = "/workspace"

// requireLiveJailWorkspace skips unless BOTH conditions the guard needs are
// really present: this process is inside a jail, and that jail's live bind
// exists. Off-jail the test can only fail, and a pass would say nothing.
func requireLiveJailWorkspace(t *testing.T) {
	t.Helper()
	if os.Getenv("YOLO_VERSION") == "" {
		t.Skip("not inside a yolo jail (no ambient YOLO_VERSION): the guard cannot fire here")
	}
	if fi, err := os.Stat(liveWorkspaceDir); err != nil || !fi.IsDir() {
		t.Skipf("%s is not a directory here: no live workspace to launch on", liveWorkspaceDir)
	}
}

func TestLiveWorkspaceLaunchRefusesInJail(t *testing.T) {
	requireJail(t)
	requireLiveJailWorkspace(t)

	// cmd.Dir IS the workspace a launch resolves, so pointing it at /workspace
	// is the accident itself — cwd drift in a nested-launch command. The guard
	// must refuse it before anything else runs.
	r := runCommand(t, liveWorkspaceDir, append(jailRunArgs(), "--", "true"))
	if r.rc == 0 {
		t.Fatalf("a launch on /workspace from inside a jail must refuse, not run:\n%s",
			r.combined())
	}
	for _, want := range []string{"Refusing to launch", "/tmp/yolo-nested", "YOLO_ALLOW_LIVE_WORKSPACE"} {
		if !strings.Contains(r.combined(), want) {
			t.Errorf("the refusal should name %q:\n%s", want, r.combined())
		}
	}
}
