package run

import (
	"bytes"
	"testing"

	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// macosusercaptureterminate_test.go pins E3 on the macos-user arm (macosusercapture.go): the fold
// of a session's edits to capture-mode surfaces into their overlay sidecars, which a container's
// teardown runs and this arm did not, so `yolo config diff` after a macos-user session reported
// the session before it.

// macosUserCaptureRun drives Run down the macos-user arm with a fake backend and a recording
// capture seam. during runs inside the fake backend, as the session would. It returns how many
// times the capture ran, with what, and whether it ran after the backend returned.
func macosUserCaptureRun(t *testing.T, dryRun bool, during func(ws string)) (calls int, gotWS, gotRT string, afterSession bool) {
	t.Helper()
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.DryRun = dryRun
	returned := false
	o.MacosUserRun = fakeMacosUserRun(func(macosUserCall) int {
		if during != nil {
			during(ws)
		}
		returned = true
		return 0
	})
	o.CaptureOnTerminate = func(w, r string) {
		calls++
		gotWS, gotRT, afterSession = w, r, returned
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr.String())
	}
	return calls, gotWS, gotRT, afterSession
}

// Once the session returns, its edits are folded from the host side, once, for this workspace and
// this runtime (whose name decides the dot-stripped sidecar layout the fold reads). Fails if the
// arm's captureMacosUserConfig call is deleted.
func TestMacosUserCapturesConfigOnTerminate(t *testing.T) {
	calls, gotWS, gotRT, after := macosUserCaptureRun(t, false, nil)
	if calls != 1 {
		t.Fatalf("the capture ran %d times after a macos-user session, want 1", calls)
	}
	if gotRT != "macos-user" {
		t.Errorf("the capture got runtime %q, want macos-user", gotRT)
	}
	if gotWS == "" {
		t.Error("the capture got no workspace")
	}
	if !after {
		t.Error("the capture ran before the session returned; it must fold that session's edits")
	}
}

// A dry run starts no session, so there is nothing to fold.
func TestMacosUserDryRunCapturesNothing(t *testing.T) {
	if calls, _, _, _ := macosUserCaptureRun(t, true, nil); calls != 0 {
		t.Fatalf("a macos-user --dry-run ran the capture %d times, want 0", calls)
	}
}

// Another launch of the workspace in its setup holds the launch lock while its bootstrap renders
// the same surfaces, so the capture yields to it rather than read a surface against the baseline
// that render is replacing; that render folds the same edits. Fails if the try-lock goes.
func TestMacosUserCaptureYieldsToALaunchMidBoot(t *testing.T) {
	calls, _, _, _ := macosUserCaptureRun(t, false, func(ws string) {
		cname := yoloruntime.FromWorkspace(ws)
		// What the real backend does before its agent: release the hold the launch handed it.
		AcquireWorkspaceLockFor(ws, cname, nil, nil)()
		// Then another launch of the workspace takes the lock for its bootstrap.
		other, ok := tryWorkspaceLock(cname)
		if !ok {
			t.Fatal("could not take the workspace lock as a concurrent launch would")
		}
		t.Cleanup(other.Close)
	})
	if calls != 0 {
		t.Fatalf("the capture ran %d times while another launch held the workspace lock, want 0", calls)
	}
}
