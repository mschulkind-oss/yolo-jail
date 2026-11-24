package toolskills

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func openDirFD(t *testing.T, path string) int {
	t.Helper()
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unix.Close(fd) })
	return fd
}

// A removed snapshot directory stays open; on macOS APFS it even keeps nlink=2. Its lock
// create must report ENOENT after one attempt, whatever fstat says of the descriptor.
func TestLockOfARemovedSnapshotDirectoryIsNotRetried(t *testing.T) {
	yolo := t.TempDir()
	snap := filepath.Join(yolo, SnapshotDirName)
	if err := os.Mkdir(snap, 0o755); err != nil {
		t.Fatal(err)
	}
	yoloFD, snapFD := openDirFD(t, yolo), openDirFD(t, snap)
	if !snapDirIsLive(yoloFD, snapFD) {
		t.Fatal("a live snapshot directory reads as removed")
	}
	if err := os.Remove(snap); err != nil {
		t.Fatal(err)
	}
	calls := 0
	real := openat
	openat = func(fd int, p string, flags int, mode uint32) (int, error) {
		calls++
		return real(fd, p, flags, mode)
	}
	t.Cleanup(func() { openat = real })
	if _, err := lockFile(yoloFD, snapFD, ""); err != unix.ENOENT {
		t.Fatalf("err = %v, want ENOENT", err)
	}
	if calls != 1 {
		t.Fatalf("openat calls = %d, want 1", calls)
	}
}
