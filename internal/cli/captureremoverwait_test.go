package cli

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
)

// A BUILD JAIL'S SCRATCH REMOVER IS A WRITER INTO ITS STAGING WORKSPACE, so the cleanup that
// deletes that workspace waits for it first. The build jail's own launch (a separate
// `yolo internal fork-build-jail` process) starts `yolo internal scratch-rm` detached at its
// teardown and exits without waiting, by design; the capture's cleanup then deleted the staging
// dir while the remover was still running podman and about to log its line there. CI run
// 38804113167 failed two integration tests' t.TempDir cleanup with "directory not empty"
// that way: the remover of an npm-resolve jail outlived the launch that asked for it.
//
// The "remover" is this test holding the shared in-flight lock the real one inherits, writing
// its housekeeping line and releasing a moment later. Delete the wait from
// cleanupCaptureWorkspace and the cleanup returns before the release.
func TestCaptureCleanupWaitsForTheBuildJailsScratchRemover(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(filepath.Join(ws, ".yolo"), 0o755); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(filepath.Join(ws, ".yolo", run.ScratchRemoverLockName), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	// The descriptor is read here, once: the remover goroutine must not touch the *os.File the
	// deferred Close tears down, since a flock is no happens-before edge for the race detector.
	fd := int(lock.Fd())
	if err := syscall.Flock(fd, syscall.LOCK_SH); err != nil {
		t.Fatal(err)
	}

	var released atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Long enough that a cleanup which does not wait (a directory removal) is long done.
		time.Sleep(200 * time.Millisecond)
		_ = os.WriteFile(filepath.Join(ws, ".yolo", "housekeeping.log"), []byte("scratch: removed\n"), 0o644)
		released.Store(true)
		_ = syscall.Flock(fd, syscall.LOCK_UN)
	}()

	cleanupCaptureWorkspace(ws, "yolo-capture-remover-test")
	defer func() { <-done }()
	if !released.Load() {
		t.Fatal("the capture cleanup returned while the build jail's scratch remover still held " +
			"the staging workspace's in-flight lock: its last line lands after the delete")
	}
	if _, err := os.Lstat(ws); !os.IsNotExist(err) {
		t.Errorf("the staging workspace survived the cleanup: %v", err)
	}
}
