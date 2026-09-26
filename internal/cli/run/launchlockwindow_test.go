package run

// launchlockwindow_test.go pins WHERE the per-workspace launch lock's window closes on each arm
// — the call sites holdLaunchLock's doc comment lists. Staging opens the window
// (concurrentstaging_test.go has the race that made it open there); each arm must close it at
// the point it stops reading the workspace's shared staging, and no later, or a second
// terminal in the workspace waits for the first one's whole session.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// launchLockHeld reports whether the lock file at path is held, by taking it non-blocking on a
// descriptor of its own and dropping it again. A flock held on another open file description —
// in this process or another — makes the take fail.
func launchLockHeld(t *testing.T, path string) bool {
	t.Helper()
	// A lock dir nobody has created yet is a lock nobody holds.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return true
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

// TestTheMacosUserBackendIsHandedTheLaunchLockStagingTook: the macos-user arm is dispatched with
// the lock staging took STILL HELD (the backend's own host-side reads of the staging — the
// daemons' module dirs, the copies it stages for the sandbox — happen inside it), and the
// backend's own acquisition, through the seam the front door wires (AcquireWorkspaceLockFor), is
// HANDED that hold. Taking the file a second time in one process would wait on itself for ever,
// and releasing what it was handed must release the launch's hold, since that release — before
// the agent — is where this backend's window ends.
func TestTheMacosUserBackendIsHandedTheLaunchLockStagingTook(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	ws := t.TempDir()
	cname := yoloruntime.FromWorkspace(ws)
	lockPath := launchLockPath(cname)

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	// A plan render: it stages and dispatches exactly as a launch does, and starts no host
	// daemon, which this test is not about.
	o.DryRun = true

	reached := false
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _, _ string,
		_ macosuser.HostContext, _ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool) int {
		reached = true
		if !launchLockHeld(t, lockPath) {
			t.Error("the macos-user backend was dispatched without the workspace launch lock " +
				"held, so a second launch of this workspace could restage while this one's " +
				"backend still reads the staging")
		}
		got := make(chan func(), 1)
		go func() { got <- AcquireWorkspaceLockFor(ws, cname, nil, nil) }()
		select {
		case release := <-got:
			release()
			if launchLockHeld(t, lockPath) {
				t.Error("releasing the lock the backend was handed left the launch's hold in " +
					"place, so a second launch would wait for this whole session")
			}
		case <-time.After(5 * time.Second):
			t.Error("the backend's AcquireWorkspaceLockFor waited on the lock this very launch " +
				"holds: a self-deadlock, and every macos-user launch would hang")
		}
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	if !reached {
		t.Fatalf("the macos-user arm was never dispatched\nstdout:\n%s\nstderr:\n%s",
			stdout.String(), stderr.String())
	}
	if launchLockHeld(t, lockPath) {
		t.Error("the launch lock is still held after Run returned")
	}
}

// TestAnAttachReleasesTheLaunchLockBeforeItsSession: on podman, a launch that finds its jail
// running restages and refreshes under the lock and then attaches — and the attach session must
// not hold it, or every other terminal in the workspace would queue behind this one's shell.
// The attach arm's first runtime question (the version inspect, attachskew.go) is where it is
// read: by then the window is over.
func TestAnAttachReleasesTheLaunchLockBeforeItsSession(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	ws := t.TempDir()
	cname := yoloruntime.FromWorkspace(ws)
	lockPath := launchLockPath(cname)
	// No runtime binary: the attach's exec fails as "not found on PATH" after every host-side
	// step it has, which is all this test needs.
	t.Setenv("PATH", t.TempDir())

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	inspected := false
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		switch {
		case len(argv) >= 2 && argv[1] == "info":
			return ExecResult{Ran: true, RC: 0, Stdout: "host: {}"}
		case len(argv) >= 2 && argv[1] == "ps" && strings.Contains(strings.Join(argv, " "), "name=^/"+cname+"$"):
			return ExecResult{Ran: true, RC: 0, Stdout: "abc123\n"}
		case len(argv) >= 2 && argv[1] == "inspect":
			inspected = true
			if launchLockHeld(t, lockPath) {
				t.Error("the attach arm still holds the workspace launch lock: its session would " +
					"make every other launch of this workspace wait until it exits")
			}
			return ExecResult{Ran: true, RC: 0, Stdout: "YOLO_VERSION=9.9.9-test\n"}
		}
		return ExecResult{Ran: true, RC: 0}
	}

	_ = Run(*o)
	if !strings.Contains(stdout.String(), "Attaching to existing jail") {
		t.Fatalf("the launch did not take the attach arm, so this test says nothing about it\n"+
			"stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if !inspected {
		t.Fatalf("the attach arm asked the runtime nothing, so the lock was never read at it\n"+
			"stdout:\n%s", stdout.String())
	}
}
