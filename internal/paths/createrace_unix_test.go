//go:build linux || darwin

package paths

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// lostCreateRace replaces openat so that the first creating call really creates the file
// (as the winning opener of the macOS race does) and then reports ENOENT; later calls are real.
func lostCreateRace(t *testing.T) *int {
	t.Helper()
	calls := 0
	real := openat
	openat = func(dirfd int, path string, flags int, mode uint32) (int, error) {
		calls++
		fd, err := real(dirfd, path, flags, mode)
		if calls == 1 && flags&unix.O_CREAT != 0 && err == nil {
			unix.Close(fd)
			return -1, unix.ENOENT
		}
		return fd, err
	}
	t.Cleanup(func() { openat = real })
	return &calls
}

func TestLostCreateRaceIsRetriedInALiveDirectory(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	calls := lostCreateRace(t)
	f, err := openNoFollowLeaf(root, "records.lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatalf("open failed after a lost create race: %v", err)
	}
	f.Close()
	if *calls != 2 {
		t.Fatalf("openat calls = %d, want 2", *calls)
	}
}

// The call site: OpenLockFileBeneath is what mcpauth's brokers use.
func TestOpenLockFileBeneathSurvivesALostCreateRace(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	lostCreateRace(t)
	f, err := OpenLockFileBeneath(root, "records.lock")
	if err != nil {
		t.Fatalf("OpenLockFileBeneath: %v", err)
	}
	f.Close()
}

func TestRemovedDirectoryStillReportsENOENT(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gone")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	calls := 0
	real := openat
	openat = func(dirfd int, path string, flags int, mode uint32) (int, error) {
		calls++
		return real(dirfd, path, flags, mode)
	}
	t.Cleanup(func() { openat = real })
	_, err = openNoFollowLeaf(root, "records.lock", os.O_RDWR|os.O_CREATE, 0o600)
	if !errors.Is(err, syscall.ENOENT) {
		t.Fatalf("err = %v, want ENOENT", err)
	}
	if calls != 1 {
		t.Fatalf("openat calls = %d, want 1 (a removed directory is not retried)", calls)
	}
}

func TestEndlessENOENTInALiveDirectoryIsBounded(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	calls := 0
	real := openat
	openat = func(int, string, int, uint32) (int, error) { calls++; return -1, unix.ENOENT }
	t.Cleanup(func() { openat = real })
	if _, err := openNoFollowLeaf(root, "x", os.O_RDWR|os.O_CREATE, 0o600); !errors.Is(err, syscall.ENOENT) {
		t.Fatalf("err = %v, want ENOENT", err)
	}
	if calls != 1+createRetries {
		t.Fatalf("openat calls = %d, want %d", calls, 1+createRetries)
	}
}

func TestOpenWithoutCreateIsNotRetried(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	calls := 0
	real := openat
	openat = func(dirfd int, path string, flags int, mode uint32) (int, error) {
		calls++
		return real(dirfd, path, flags, mode)
	}
	t.Cleanup(func() { openat = real })
	if _, err := openNoFollowLeaf(root, "absent", os.O_RDWR, 0); !errors.Is(err, syscall.ENOENT) {
		t.Fatalf("err = %v, want ENOENT", err)
	}
	if calls != 1 {
		t.Fatalf("openat calls = %d, want 1", calls)
	}
}

func TestRetryAfterLostCreateRaceStillRefusesASymlinkLeaf(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Symlink("target", filepath.Join(dir, "leaf")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	calls := 0
	real := openat
	openat = func(dirfd int, path string, flags int, mode uint32) (int, error) {
		calls++
		if calls == 1 {
			return -1, unix.ENOENT
		}
		return real(dirfd, path, flags, mode)
	}
	t.Cleanup(func() { openat = real })
	f, err := openNoFollowLeaf(root, "leaf", os.O_RDWR|os.O_CREATE, 0o600)
	if err == nil {
		f.Close()
		t.Fatal("a symlink leaf was opened after the retry")
	}
	if calls != 2 {
		t.Fatalf("openat calls = %d, want 2 (the retry ran and was refused)", calls)
	}
	if _, serr := os.Lstat(target); !os.IsNotExist(serr) {
		t.Fatalf("the link's target was created: %v", serr)
	}
}

// Many openers of one new name: on macOS this is the race itself; on Linux it must simply pass.
func TestConcurrentFirstOpensOfOneLockNameAllSucceed(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	const n = 32
	start := make(chan struct{})
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			f, err := OpenLockFileBeneath(root, "records.lock")
			if err == nil {
				f.Close()
			}
			errs[i] = err
		}()
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("opener %d: %v", i, err)
		}
	}
}
