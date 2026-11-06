package run

import (
	"io"
	"testing"

	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// THE PROJECT LOCK RIDES ON THE LAUNCH LOCK (named-setups-plan.md SUP-D36): a launch that decides
// about the legacy home holds the launch lock file of the legacy jail name, which every launch of
// the project takes whatever its setup, and which an older yolo took as its own launch lock. It is
// released with the launch lock (workspaceLock.ride), and a launch lock handed to a keeper leaves it
// held until the launch lets it go (releaseHomeLock).
func TestTheProjectLockGoesWithTheLaunchLock(t *testing.T) {
	for _, handOff := range []bool{false, true} {
		name := "released"
		if handOff {
			name = "handed off"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			ws := t.TempDir()
			o := &Options{Workspace: ws, Setup: setup.MustBuiltIn("podman-self"), Stderr: io.Discard}
			legacy := yoloruntime.FromWorkspace(ws)
			projectHeld := func() bool {
				l, ok := tryWorkspaceLock(legacy)
				if ok {
					l.Close()
				}
				return !ok
			}
			o.holdHomeLock()
			if !projectHeld() {
				t.Fatal("holdHomeLock did not take the project lock")
			}
			o.holdLaunchLock(o.jailName())
			if o.launchLock == nil || o.launchLock.path != launchLockPath(yoloruntime.JailName(ws, "podman-self")) {
				t.Fatalf("the launch lock is not the setup's jail's: %+v", o.launchLock)
			}
			if handOff {
				o.launchLock.handOff()
				if !projectHeld() {
					t.Fatal("handing the launch lock to a keeper released the project lock")
				}
				o.releaseHomeLock()
			} else {
				o.releaseLaunchLock()
			}
			if projectHeld() {
				t.Error("the project lock outlived its release")
			}
		})
	}
}
