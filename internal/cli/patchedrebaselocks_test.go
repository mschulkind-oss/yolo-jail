package cli

// patchedrebaselocks_test.go pins where `yolo pack rebase` takes its rebase directory lock
// (patchedrebaselocks.go; docs/design/patched-forks.md PF-D73): the keyed verb and the scratch
// rebase in one place, so either refuses a second run of either on one clone directory; and that
// place is this user's alone, so a directory another user made first in a shared /tmp, open to
// others or a link, is refused, naming TMPDIR, rather than used.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// mustRebaseDirLocks is the store a rebase takes its directory lock in, as a running rebase holds it.
func mustRebaseDirLocks(t *testing.T) *packsrc.Store {
	t.Helper()
	s, err := rebaseDirLocks()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// ONE LOCK FOR BOTH FORMS: while a rebase holds a clone directory, a keyed run and a scratch run
// into it are each refused at once, and neither clones.
func TestAKeyedAndAScratchRebaseShareTheDirectoryLock(t *testing.T) {
	f := newPatchedFixture(t, "")
	f.commit(t, "v1.2.0", map[int]string{11: "eleven"})
	t.Setenv("TMPDIR", t.TempDir())
	dir := filepath.Join(t.TempDir(), "clone")
	unlock, held, err := mustRebaseDirLocks(t).TryLockRebaseDir(resolveExistingPrefix(dir))
	if err != nil || held {
		t.Fatalf("taking the lock: held=%v %v", held, err)
	}
	defer unlock()
	for _, args := range [][]string{
		{"forkpack/tool", "--into", dir},
		{"forkpack/tool", "--pack", f.forkDir, "--into", dir},
	} {
		rc, out, errw := rebaseVerb(t, args...)
		if rc != 1 || !strings.Contains(errw, "another `yolo pack rebase` is working in "+dir) ||
			strings.Contains(out, "cloning") {
			t.Errorf("rebase %q while the directory is held: rc=%d\n%s\n%s", args, rc, out, errw)
		}
	}
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused rebase made its clone (%v)", err)
	}
}

// A LOCK DIRECTORY THAT IS NOT THIS USER'S ALONE is refused, naming TMPDIR: one open to others, as
// another user would leave it in a shared /tmp, and a link. Nothing is cloned, and the directory is
// left as it was.
func TestARebaseRefusesALockDirectoryOthersCanReach(t *testing.T) {
	f := newPatchedFixture(t, "")
	f.commit(t, "v1.2.0", map[int]string{11: "eleven"})
	t.Setenv("XDG_RUNTIME_DIR", "")
	for _, c := range []struct {
		name string
		make func(path string)
		want string
	}{
		{"open to others", func(path string) {
			if err := os.Mkdir(path, 0o777); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0o777); err != nil {
				t.Fatal(err)
			}
		}, "has mode 0777, open to other users"},
		{"a link", func(path string) {
			if err := os.Symlink(t.TempDir(), path); err != nil {
				t.Fatal(err)
			}
		}, "is a symbolic link"},
	} {
		tmp := t.TempDir()
		t.Setenv("TMPDIR", tmp)
		lockDir := filepath.Join(tmp, fmt.Sprintf("yolo-rebase-locks-%d", os.Getuid()))
		c.make(lockDir)
		dir := filepath.Join(t.TempDir(), "clone")
		rc, out, errw := rebaseVerb(t, "forkpack/tool", "--pack", f.forkDir, "--into", dir)
		if rc != 1 || !strings.Contains(errw, lockDir+" "+c.want) || !strings.Contains(errw, rebaseLockStep) ||
			strings.Contains(out, "cloning") {
			t.Errorf("%s: rc=%d\n%s\n%s", c.name, rc, out, errw)
		}
		if _, err := os.Lstat(filepath.Join(lockDir, "locks")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s: a lock was taken in %s (%v)", c.name, lockDir, err)
		}
	}
}

// A TMPDIR THAT DOES NOT EXIST is not made: the rebase is refused, naming TMPDIR.
func TestARebaseWithAMissingTMPDIRNamesIt(t *testing.T) {
	f := newPatchedFixture(t, "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	missing := filepath.Join(t.TempDir(), "missing", "tmp")
	t.Setenv("TMPDIR", missing)
	rc, out, errw := rebaseVerb(t, "forkpack/tool", "--pack", f.forkDir, "--into", filepath.Join(t.TempDir(), "clone"))
	if rc != 1 || !strings.Contains(errw, rebaseLockStep) {
		t.Errorf("rc=%d\n%s\n%s", rc, out, errw)
	}
	if _, err := os.Lstat(filepath.Dir(missing)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the rebase made the missing TMPDIR's parent (%v)", err)
	}
}

// THE LOCK DIRECTORY'S PLACE: under $XDG_RUNTIME_DIR when it names a directory, else under the
// temporary directory; made with no permission for anyone else.
func TestTheRebaseLockDirectoryIsThisUsersAlone(t *testing.T) {
	runtimeDir, tmp := t.TempDir(), t.TempDir()
	uid := os.Getuid()
	dir, err := privateRebaseLockDir(runtimeDir, tmp, uid)
	if err != nil || filepath.Dir(dir) != runtimeDir {
		t.Fatalf("with a runtime directory: %q, %v", dir, err)
	}
	if fi, err := os.Lstat(dir); err != nil || fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("the lock directory's mode: %v, %v", fi.Mode(), err)
	}
	if dir, err := privateRebaseLockDir(filepath.Join(runtimeDir, "gone"), tmp, uid); err != nil || filepath.Dir(dir) != tmp {
		t.Errorf("with a runtime directory that is gone: %q, %v; want it under %s", dir, err, tmp)
	}
	if dir, err := privateRebaseLockDir("relative", tmp, uid); err != nil || filepath.Dir(dir) != tmp {
		t.Errorf("with a relative runtime directory: %q, %v; want it under %s", dir, err, tmp)
	}
	if why := notPrivateDir(fakeDirInfo{uid: uint32(uid) + 1}, uid); !strings.Contains(why, "belongs to user") {
		t.Errorf("another user's directory: %q", why)
	}
	if why := notPrivateDir(fakeDirInfo{uid: uint32(uid)}, uid); why != "" {
		t.Errorf("this user's private directory: %q", why)
	}
}

// fakeDirInfo is a mode-0700 directory owned by uid, as Lstat would report one.
type fakeDirInfo struct{ uid uint32 }

func (f fakeDirInfo) Name() string       { return "d" }
func (f fakeDirInfo) Size() int64        { return 0 }
func (f fakeDirInfo) Mode() fs.FileMode  { return fs.ModeDir | 0o700 }
func (f fakeDirInfo) ModTime() time.Time { return time.Time{} }
func (f fakeDirInfo) IsDir() bool        { return true }
func (f fakeDirInfo) Sys() any           { return &syscall.Stat_t{Uid: f.uid} }
