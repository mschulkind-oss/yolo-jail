package packsrc

// rebaselock_test.go pins that the rebase directory lock (TryLockRebaseDir) is never taken through
// a link planted where its lock file goes (docs/design/patched-forks.md PF-D73): the open fails, and
// nothing is made where the link points.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTheRebaseDirLockIsNotTakenThroughALink(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	dir := filepath.Join(t.TempDir(), "clone")
	path := filepath.Join(s.Dir, "locks", "rebase-"+mirrorSlug(filepath.Clean(dir))+".lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "made-by-the-lock")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if unlock, held, err := s.TryLockRebaseDir(dir); err == nil {
		if unlock != nil {
			unlock()
		}
		t.Errorf("the lock was taken through a link (held=%v)", held)
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the lock's open made %s (%v)", target, err)
	}
	// With the link gone, the lock is taken, and a second taker is told it is held.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	unlock, held, err := s.TryLockRebaseDir(dir)
	if err != nil || held {
		t.Fatalf("taking the lock: held=%v %v", held, err)
	}
	defer unlock()
	if _, held, err := s.TryLockRebaseDir(dir); err != nil || !held {
		t.Errorf("a second taker: held=%v %v, want held", held, err)
	}
}
