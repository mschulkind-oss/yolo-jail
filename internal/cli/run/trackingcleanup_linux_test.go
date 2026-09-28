//go:build linux

package run

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestAForkDuringTheTeardownsLockHoldDoesNotKeepTheSkeleton reproduces CI run 36486674316
// (TestPodmanHomeIsAPerJailReadOnlySkeleton/writable_home_dirs, ubuntu-24.04-arm): the
// skeleton a jail was bound from survived its exit although the container was gone.
//
// THE RACE. The normal-exit chain takes the workspace lock twice in a row, non-blocking:
// stopLoopholes holds it across a `podman ps -a`, closes it, and forgetGoneContainer takes it
// again microseconds later. Meanwhile the housekeeping slot's goroutine is forking podman
// calls. A child forked while stopLoopholes holds the lock carries a copy of that descriptor
// until its exec closes it (O_CLOEXEC acts only at exec), and a flock belongs to the open
// file DESCRIPTION, so closing the parent's descriptor released nothing: the child's copy
// still held it, forgetGoneContainer's take failed, and the tracking file, the skeleton and
// the pack tree were all left behind. Measured with a fork loop against a 200 µs hold: 12%
// to 24% of immediate re-takes failed with close-only release, none with LOCK_UN first.
//
// The child is simulated deterministically: the fake runtime, answering stopLoopholes' own
// probe while the lock is held, duplicates the lock descriptor exactly as fork would, and
// keeps the duplicate open for the rest of the test, as a child that has not reached exec
// yet does.
func TestAForkDuringTheTeardownsLockHoldDoesNotKeepTheSkeleton(t *testing.T) {
	const cname = "yolo-tracking-forked-child"
	o, tracking, probes := trackingFixture(t, cname, ExecResult{Ran: true, RC: 0})
	skeleton := buildSkeletonForTest(t, cname, nil, nil, nil)
	lockPath := filepath.Join(paths.GlobalStorage(), "locks", cname+".lock")

	answer := o.Exec
	inherited := -1
	o.Exec = func(argv []string, dir string, env []string, timeout time.Duration) ExecResult {
		if inherited < 0 && isExistingProbe(argv, cname) {
			inherited = dupOpenDescriptorOf(t, lockPath)
		}
		return answer(argv, dir, env, timeout)
	}
	t.Cleanup(func() {
		if inherited >= 0 {
			_ = syscall.Close(inherited)
		}
	})

	socketsDir := t.TempDir()
	o.teardownAfterExit(nil, "", nil, socketsDir, cname, "podman", skeleton, 0)

	if inherited < 0 {
		t.Fatal("stopLoopholes never probed while holding the lock, so the fork was not simulated")
	}
	if *probes < 2 {
		t.Fatalf("the runtime was asked %d time(s): forgetGoneContainer never got past the "+
			"workspace lock, which a forked child's inherited descriptor still held", *probes)
	}
	if _, err := os.Lstat(tracking); !os.IsNotExist(err) {
		t.Errorf("the tracking file %s stayed (err %v)", tracking, err)
	}
	if _, err := os.Lstat(skeleton); !os.IsNotExist(err) {
		t.Errorf("the skeleton %s stayed after its container was known gone (err %v) — "+
			"a launch removes its own skeleton then (OQ-BH16)", skeleton, err)
	}
}

// dupOpenDescriptorOf finds this process's open descriptor for path and duplicates it,
// which is what fork does to every descriptor a child inherits.
func dupOpenDescriptorOf(t *testing.T, path string) int {
	t.Helper()
	want, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the lock file: %v", err)
	}
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		fd := 0
		for _, c := range e.Name() {
			fd = fd*10 + int(c-'0')
		}
		var st syscall.Stat_t
		if syscall.Fstat(fd, &st) != nil {
			continue
		}
		if ws, ok := want.Sys().(*syscall.Stat_t); ok && ws.Dev == st.Dev && ws.Ino == st.Ino {
			d, err := syscall.Dup(fd)
			if err != nil {
				t.Fatal(err)
			}
			return d
		}
	}
	t.Fatalf("no open descriptor for %s while stopLoopholes was probing", path)
	return -1
}
