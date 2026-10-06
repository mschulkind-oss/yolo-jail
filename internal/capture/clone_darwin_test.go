//go:build darwin

package capture

// clone_darwin_test.go measures the macOS reflink arm, APFS's clonefile(2), for real rather than
// through the forceChain seam: a Mac's temp dir is on its own APFS disk, so on check-macos the
// clone either happens or these tests fail. A temp dir on another filesystem skips, naming it,
// everywhere but CI, where it fails: a skip there would leave the arm unmeasured on the one
// machine that runs it.
//
// The "not here" answers come from layouts that disk does not have (another volume, HFS+ or SMB,
// a Seatbelt denial), so the fallback tests stub the system call (clonefile) and nothing else:
// they still go through reflinkFile's own call and the real chain.
//
// TestReflinkGivesTheDestinationItsOwnInode (materialize_test.go) runs here too, against
// reflinkOne, which on darwin is this arm.

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// apfsTempDir is a fresh temp dir that clonefile(2) can clone within, symlinks resolved
// (resolvedTempDir, confine_test.go).
func apfsTempDir(t *testing.T) string {
	t.Helper()
	dir := resolvedTempDir(t)
	if kind := fsName(dir); kind != "apfs" {
		if os.Getenv("CI") != "" {
			t.Fatalf("the temp dir %s is on %s, not apfs. A CI Mac's disk is APFS, so either the runner "+
				"changed or fsName no longer reads the filesystem's name; skipping here would leave the "+
				"clone arm unmeasured on the one machine that runs it", dir, orUnknown(kind))
		}
		t.Skipf("the temp dir %s is on %s, not apfs: clonefile(2) cannot clone there, and materialize "+
			"takes the hardlink or copy arm", dir, orUnknown(kind))
	}
	return dir
}

// On APFS every file of a materialize is a CLONE: its own inode, with the manifest's mode rather
// than the store's frozen one, and a write to it leaves the store's bytes alone.
func TestMaterializeClonesEveryFileOnAPFS(t *testing.T) {
	home := apfsTempDir(t)
	_, entry := entryFixture(t, home, Platform(), nil)

	var errw bytes.Buffer
	res, err := Materialize(MaterializeOptions{Entry: entry, Home: home, Stderr: &errw})
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if res.Files != 2 || res.Reflinked != res.Files || res.Mechanism() != "reflink" {
		t.Fatalf("on APFS every file must be cloned: %d files, %d reflinked / %d linked / %d copied, "+
			"mechanism %q, reflink retired by %q", res.Files, res.Reflinked, res.Linked, res.Copied,
			res.Mechanism(), res.ReflinkRetired)
	}
	if errw.Len() != 0 {
		t.Errorf("nothing was copied, so nothing should have been reported: %s", errw.String())
	}

	rel := filepath.Join(".local", "share", "vendor", "1.0", "vendor")
	src, dst := filepath.Join(entry.Tree, rel), filepath.Join(home, rel)
	si, di := statT(t, src), statT(t, dst)
	if si.Ino == di.Ino {
		t.Fatalf("the clone shares the store's inode (%d): a write through it would reach the store", si.Ino)
	}
	if di.Nlink != 1 {
		t.Errorf("the clone has nlink %d, want 1", di.Nlink)
	}
	for path, want := range map[string]fs.FileMode{
		dst: 0o755,
		filepath.Join(home, ".local", "share", "vendor", "1.0", "data.txt"): 0o644,
	} {
		fi, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != want {
			t.Errorf("%s has mode %v, want the manifest's %v: a clone copies the store's frozen mode, "+
				"which reflinkFile must replace", path, fi.Mode().Perm(), want)
		}
	}
	// The chmod reached the clone alone.
	if fi, err := os.Lstat(src); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm()&0o222 != 0 {
		t.Errorf("the store's file is writable (%v) after a materialize: the chmod reached it", fi.Mode().Perm())
	}

	before, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("rewritten by a self-updater\n"), 0o755); err != nil {
		t.Fatalf("the clone cannot be written, so a self-updater would fail on it: %v", err)
	}
	after, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("a write to the clone changed the store's bytes to %q", after)
	}
}

// CopyTree, the darwin caller the chain has today (a patched extension's per-launch copy), clones
// too: no byte copy and no copy report.
func TestCopyTreeClonesOnAPFS(t *testing.T) {
	home := apfsTempDir(t)
	_, entry := entryFixture(t, home, Platform(), nil)
	dest := filepath.Join(apfsTempDir(t), "vendor")

	var errw bytes.Buffer
	res, err := CopyTree(CopyTreeOptions{Entry: entry, Prefix: ".local/share/vendor", Dest: dest, Stderr: &errw})
	if err != nil {
		t.Fatalf("CopyTree: %v", err)
	}
	if res.Reflinked != 2 || res.Copied != 0 || res.Linked != 0 {
		t.Fatalf("on APFS the tree's 2 files must be cloned: %d reflinked / %d linked / %d copied, "+
			"reflink retired by %q", res.Reflinked, res.Linked, res.Copied, res.ReflinkRetired)
	}
	if errw.Len() != 0 {
		t.Errorf("nothing was copied, so nothing should have been reported: %s", errw.String())
	}
	src := statT(t, filepath.Join(entry.Tree, ".local", "share", "vendor", "1.0", "vendor"))
	if statT(t, filepath.Join(dest, "1.0", "vendor")).Ino == src.Ino {
		t.Error("the copy shares the store's inode")
	}
}

// A destination that already exists is refused as a fact about this file, not as "not here": the
// error keeps its errno and both paths, reflink is not retired for it, and what was there stays.
func TestClonefileLeavesAnExistingDestinationAlone(t *testing.T) {
	dir := apfsTempDir(t)
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("store bytes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("already here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := reflinkFile(src, dst, 0o755)
	if err == nil {
		t.Fatal("clonefile over an existing destination succeeded")
	}
	if errors.Is(err, errCloneUnsupported) {
		t.Errorf("an existing destination retired reflink for the run: %v", err)
	}
	if !errors.Is(err, fs.ErrExist) || !strings.Contains(err.Error(), src) || !strings.Contains(err.Error(), dst) {
		t.Errorf("the error = %v, want EEXIST naming both paths", err)
	}
	if body, rerr := os.ReadFile(dst); rerr != nil || string(body) != "already here\n" {
		t.Errorf("the existing destination was touched: %q, %v", body, rerr)
	}
}

// The errnos that mean "not here" retire the arm; every other one is about one file and fails it.
func TestClonefileErrorSortsNotHereFromOneFile(t *testing.T) {
	// The reason both spellings are listed: on darwin they are two errnos.
	if unix.ENOTSUP == unix.EOPNOTSUPP {
		t.Fatalf("ENOTSUP and EOPNOTSUPP are one errno (%d) here", unix.ENOTSUP)
	}
	for _, errno := range []syscall.Errno{unix.ENOTSUP, unix.EOPNOTSUPP, unix.EXDEV, unix.ENOSYS, unix.EPERM} {
		if err := clonefileError("/s", "/d", errno); !errors.Is(err, errCloneUnsupported) {
			t.Errorf("%v must retire reflink for the run, got %v", errno, err)
		}
	}
	for _, errno := range []syscall.Errno{unix.EEXIST, unix.EACCES, unix.ENOENT, unix.EIO, unix.ENOSPC} {
		err := clonefileError("/s", "/d", errno)
		if errors.Is(err, errCloneUnsupported) {
			t.Errorf("%v is about one file and must not retire reflink: %v", errno, err)
		}
		if !errors.Is(err, errno) {
			t.Errorf("%v lost its errno: %v", errno, err)
		}
	}
}

// stubClonefile makes reflinkFile's clonefile(2) call answer errno, as a layout check-macos's one
// APFS disk does not have would. It counts the calls and keeps the flags of the last one.
func stubClonefile(t *testing.T, errno syscall.Errno) (calls, flags *int) {
	t.Helper()
	old := clonefile
	n, f := 0, 0
	clonefile = func(src, dst string, fl int) error { n++; f = fl; return errno }
	t.Cleanup(func() { clonefile = old })
	return &n, &f
}

// When clonefile(2) answers "not here", reflinkFile's own call retires the arm after one try and
// the chain places every file by its next arm instead of failing: a store or copy root on another
// APFS volume (EXDEV), on HFS+ or SMB (ENOTSUP, EOPNOTSUPP), a kernel without the call (ENOSYS),
// a Seatbelt denial (EPERM). Through the real call rather than clonefileError alone, so a
// reflinkFile that stops sorting its errno fails every case.
func TestClonefileNotHereFallsThroughToTheNextArm(t *testing.T) {
	for _, errno := range []syscall.Errno{unix.EXDEV, unix.ENOTSUP, unix.EOPNOTSUPP, unix.ENOSYS, unix.EPERM} {
		t.Run(unix.ErrnoName(errno), func(t *testing.T) {
			home := resolvedTempDir(t)
			_, entry := entryFixture(t, home, Platform(), nil)
			calls, flags := stubClonefile(t, errno)

			res, err := Materialize(MaterializeOptions{Entry: entry, Home: home, Stderr: io.Discard})
			if err != nil {
				t.Fatalf("clonefile answering %v failed the materialize instead of falling back: %v", errno, err)
			}
			if res.Files != 2 || res.Reflinked != 0 || res.Linked+res.Copied != res.Files {
				t.Errorf("want the 2 files placed by hardlink or copy: %d files, %d reflinked / %d linked / "+
					"%d copied", res.Files, res.Reflinked, res.Linked, res.Copied)
			}
			if !strings.Contains(res.ReflinkRetired, "clonefile") {
				t.Errorf("reflink retired by %q, want clonefile's answer", res.ReflinkRetired)
			}
			if *calls != 1 {
				t.Errorf("clonefile was called %d times for 2 files, want 1: a \"not here\" answer retires the "+
					"arm for the run", *calls)
			}
			if want := unix.CLONE_NOFOLLOW | unix.CLONE_NOOWNERCOPY; *flags&want != want {
				t.Errorf("clonefile flags %#x, want CLONE_NOFOLLOW|CLONE_NOOWNERCOPY (%#x) among them", *flags, want)
			}

			// CopyTree has no hardlink arm, so the same answer copies.
			*calls = 0
			dest := filepath.Join(resolvedTempDir(t), "vendor")
			cres, err := CopyTree(CopyTreeOptions{Entry: entry, Prefix: ".local/share/vendor", Dest: dest,
				Stderr: io.Discard})
			if err != nil {
				t.Fatalf("clonefile answering %v failed CopyTree instead of falling back: %v", errno, err)
			}
			if cres.Files != 2 || cres.Copied != cres.Files {
				t.Errorf("want CopyTree's 2 files copied: %d files, %d reflinked / %d linked / %d copied",
					cres.Files, cres.Reflinked, cres.Linked, cres.Copied)
			}
			if !strings.Contains(cres.ReflinkRetired, "clonefile") || *calls != 1 {
				t.Errorf("CopyTree: reflink retired by %q after %d calls, want clonefile's answer after 1",
					cres.ReflinkRetired, *calls)
			}
		})
	}
}

// An errno about one file goes through reflinkFile's call as that file's failure, not as "not
// here": the materialize fails with the errno, after one try, rather than quietly hardlinking.
func TestClonefileOneFileErrnoFailsTheMaterialize(t *testing.T) {
	home := resolvedTempDir(t)
	_, entry := entryFixture(t, home, Platform(), nil)
	calls, _ := stubClonefile(t, unix.EACCES)

	_, err := Materialize(MaterializeOptions{Entry: entry, Home: home, Stderr: io.Discard})
	if err == nil {
		t.Fatal("clonefile answering EACCES fell back instead of failing the materialize")
	}
	if !errors.Is(err, unix.EACCES) || errors.Is(err, errCloneUnsupported) {
		t.Errorf("the error = %v, want EACCES and not \"not here\"", err)
	}
	if *calls != 1 {
		t.Errorf("clonefile was called %d times, want 1: the first file's failure ends the materialize", *calls)
	}
}
