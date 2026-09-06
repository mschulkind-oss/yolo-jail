package integration

// liveoverlayguard_test.go is the end-to-end pin on the live-overlay refusal:
// this suite runs INSIDE a yolo jail (YOLO_VERSION is in its ambient
// environment), which is exactly the condition the guard exists for — a launch
// whose workspace is /workspace would jail the running session's own live tree
// over its own home. The unit tier covers the truth table
// (internal/cli/run liveoverlayguard_test.go); only a real in-jail invocation
// proves the ambient YOLO_VERSION reaches the guard through the CLI's own
// environment handling.

import (
	"strings"
	"testing"
)

func TestLiveWorkspaceLaunchRefusesInJail(t *testing.T) {
	requireJail(t)

	// cmd.Dir IS the workspace a launch resolves, so pointing it at /workspace
	// is the accident itself — cwd drift in a nested-launch command. The guard
	// must refuse it before anything else runs.
	r := runCommand(t, "/workspace", append(jailRunArgs(), "--", "true"))
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
