package paths

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenRootCreateRetriesALostCreateRace(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	calls := 0
	real := rootOpenFile
	rootOpenFile = func(r *os.Root, name string, flag int, perm fs.FileMode) (*os.File, error) {
		calls++
		f, err := real(r, name, flag, perm)
		if calls == 1 && err == nil {
			f.Close()
			return nil, &fs.PathError{Op: "openat", Path: name, Err: fs.ErrNotExist}
		}
		return f, err
	}
	t.Cleanup(func() { rootOpenFile = real })
	f, err := OpenRootCreate(root, "x.lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

// A removed root stays open and stats as a directory; only its name tells it from a live one,
// and it must not be retried.
func TestOpenRootCreateDoesNotRetryARemovedDirectory(t *testing.T) {
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
	real := rootOpenFile
	rootOpenFile = func(r *os.Root, name string, flag int, perm fs.FileMode) (*os.File, error) {
		calls++
		return real(r, name, flag, perm)
	}
	t.Cleanup(func() { rootOpenFile = real })
	if _, err := OpenRootCreate(root, "x.lock", os.O_RDWR|os.O_CREATE, 0o600); !errors.Is(err, fs.ErrNotExist) || calls != 1 {
		t.Fatalf("err=%v calls=%d, want ErrNotExist after one call", err, calls)
	}
}

func TestOpenRootCreateIsBoundedAndSparesNonCreatingOpens(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	calls := 0
	real := rootOpenFile
	rootOpenFile = func(*os.Root, string, int, fs.FileMode) (*os.File, error) {
		calls++
		return nil, &fs.PathError{Op: "openat", Path: "x", Err: fs.ErrNotExist}
	}
	t.Cleanup(func() { rootOpenFile = real })
	if _, err := OpenRootCreate(root, "x", os.O_RDWR, 0); !errors.Is(err, fs.ErrNotExist) || calls != 1 {
		t.Fatalf("non-creating: err=%v calls=%d, want one call", err, calls)
	}
	calls = 0
	if _, err := OpenRootCreate(root, "x", os.O_RDWR|os.O_CREATE, 0o600); !errors.Is(err, fs.ErrNotExist) || calls != 1+createRetries {
		t.Fatalf("creating: err=%v calls=%d, want %d", err, calls, 1+createRetries)
	}
}
