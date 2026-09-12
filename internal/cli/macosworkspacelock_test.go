package cli

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// The macos-user backend takes its per-workspace launch lock through a Deps seam, and the
// front door is what wires it. This test invokes the wired seam and proves the lock is
// REAL — the file exists and an independent non-blocking flock on it is refused — rather
// than asserting the field is non-nil, which a refusal closure would also satisfy.
//
// It runs on Linux, and the thing it covers is not macOS-specific: flock(2) and the lock
// file live on the host, on whichever OS the launcher runs. What cannot be checked here is
// the backend actually calling the seam — that is macosuser's own test, which drives
// RunMacosUser and fails if the call is deleted.
func TestWorkspaceLockSeamReallyLocks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	release := workspaceLockSeam("/Users/Shared/yolo/proj", "yolo-proj-abc123")
	if release == nil {
		t.Fatal("the seam returned no release; the backend would never unlock")
	}

	lockPath := filepath.Join(paths.GlobalStorage(), "locks", "yolo-proj-abc123.lock")
	f, err := os.OpenFile(lockPath, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("no lock file was created at %s: %v\nTwo launches in one workspace would "+
			"run two provisioning stages against one npm prefix and one mise store.",
			lockPath, err)
	}
	defer func() { _ = f.Close() }()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		t.Error("the lock file exists but is not held: a second launch would not wait")
	}

	// And releasing has to actually release, or the second terminal in a workspace blocks
	// until the first process exits rather than until its provisioning is done.
	release()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Errorf("the lock is still held after release: %v", err)
	}
}

// THE WIRING, which the test above cannot see. A launch assembles its Deps here; a launch
// with LockWorkspace left nil runs unlocked, and nothing about that is visible until two
// terminals provision one workspace at once. Measured before this test existed: deleting
// the assignment broke nothing.
//
// Weak on its own (non-nil), and deliberately not on its own — the only value it can hold
// is workspaceLockSeam, which the test above proves takes a real flock.
func TestMacosLaunchDepsWiresTheWorkspaceLock(t *testing.T) {
	deps := macosLaunchDeps(nil, nil, false)
	if deps.LockWorkspace == nil {
		t.Fatal("a macos-user LAUNCH assembles Deps with no workspace lock: two launches in " +
			"one workspace would run two provisioning stages against one npm prefix and " +
			"one mise store, with nothing to serialise them")
	}
	// And the four `yolo macos-*` setup commands must NOT get it: none of them provisions
	// anything, and a setup command blocking on a running jail's launch is a new way to
	// look hung.
	if macosuser.RealDeps(nil, nil, false).LockWorkspace != nil {
		t.Error("RealDeps itself now carries the workspace lock, so `yolo macos-setup` and " +
			"friends would block on a concurrent launch")
	}
}
