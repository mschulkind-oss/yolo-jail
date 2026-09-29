package run

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestALaunchLeavesTheStateDirUncommittable is THE CALL-SITE PIN for
// paths.EnsureWorkspaceStateDir.
//
// It drives a REAL Run rather than attachLaunchLog, because the callee is already covered
// by internal/paths and a callee-only test is the shape AGENTS.md records this repo as
// having shipped five times: delete the ensure from the launch path and every one of those
// tests stays green while a user's next `git add .` picks up a launch.log full of the argv
// the launcher printed. This one fails — the ensure is gone, or attachLaunchLog is gone
// from Run, or prepareWsState created .yolo first without it. What it asserts is the
// property, not the bytes: the file is there, after a launch, in a workspace nothing else
// touched.
func TestALaunchLeavesTheStateDirUncommittable(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.MacosUserRun = func(*jsonx.OrderedMap, string, []string, []string, string, string, macosuser.HomeOverlay, macosuser.HostContext, bool, *jsonx.OrderedMap, []packload.BlockedTool, macosuser.JailDaemons) int {
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr.String())
	}

	// The launch really did write the dangerous thing — otherwise this test would pass on
	// a launch that created no state dir at all, and prove nothing.
	if _, err := os.Stat(filepath.Join(ws, ".yolo", LaunchLogName)); err != nil {
		t.Fatalf("this launch wrote no %s, so the test cannot tell an ignored state dir "+
			"from an absent one: %v", LaunchLogName, err)
	}
	got, err := os.ReadFile(filepath.Join(ws, ".yolo", paths.WorkspaceStateIgnoreName))
	if err != nil {
		t.Fatalf("a launched workspace's .yolo is committable: %v", err)
	}
	if string(got) != paths.WorkspaceStateIgnore {
		t.Errorf("the launch wrote a .gitignore that is not the one paths defines:\n%s", got)
	}
}
