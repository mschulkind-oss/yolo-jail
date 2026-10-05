package capture

// treecopy_test.go pins CopyTree (docs/design/patched-extensions.md §8.1, PPX-D7): one subtree of an
// admitted entry copied into a directory of its own, with the prefix cut and the manifest's modes,
// by reflink or copy and NEVER by hardlink — the arm Materialize tries second, and the one that
// would hand a jail that can write its mount the store's own inode.

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestCopyTreeCopiesOneSubtreeAndNeverHardlinks(t *testing.T) {
	home := t.TempDir()
	_, entry := entryFixture(t, home, Platform(), nil)
	links := 0
	restore := forceChain(t,
		func(src, dst string, perm fs.FileMode) error { return errCloneUnsupported },
		func(src, dst string) error { links++; return os.Link(src, dst) })
	defer restore()

	dest := filepath.Join(t.TempDir(), "copies", "vendor")
	var errw bytes.Buffer
	res, err := CopyTree(CopyTreeOptions{Entry: entry, Prefix: ".local/share/vendor", Dest: dest, Stderr: &errw})
	if err != nil {
		t.Fatalf("CopyTree: %v", err)
	}
	if links != 0 || res.Linked != 0 {
		t.Fatalf("CopyTree hardlinked %d files — a tree is never hardlinked out of the store", links)
	}
	if res.Copied != 2 {
		t.Errorf("copied %d files, want the subtree's 2", res.Copied)
	}
	src := statT(t, filepath.Join(entry.Tree, ".local", "share", "vendor", "1.0", "vendor"))
	dst := statT(t, filepath.Join(dest, "1.0", "vendor"))
	if src.Ino == dst.Ino {
		t.Error("the copy shares the store's inode")
	}
	fi, err := os.Stat(filepath.Join(dest, "1.0", "vendor"))
	if err != nil || fi.Mode().Perm() != 0o755 {
		t.Errorf("the copied program's mode is %v (%v), want the manifest's 0755, not the store's frozen one", fi.Mode(), err)
	}
	if _, err := os.Lstat(filepath.Join(dest, "bin")); err == nil {
		t.Error("CopyTree placed an entry from outside its prefix")
	}
	if _, err := os.Lstat(filepath.Join(dest, ".local")); err == nil {
		t.Error("CopyTree kept the prefix instead of cutting it")
	}
	// A copy is made whole into a directory of its own, never over another.
	if _, err := CopyTree(CopyTreeOptions{Entry: entry, Prefix: ".local/share/vendor", Dest: dest}); err == nil {
		t.Error("CopyTree copied over an existing directory")
	}
	// A prefix the entry does not hold is an error, not an empty copy.
	if _, err := CopyTree(CopyTreeOptions{Entry: entry, Prefix: ".local/share/other",
		Dest: filepath.Join(t.TempDir(), "x")}); err == nil {
		t.Error("CopyTree of a prefix the entry lacks succeeded")
	}
}

// With reflink available CopyTree uses it, and the copy is still its own inode.
func TestCopyTreeReflinksWhenItCan(t *testing.T) {
	home := t.TempDir()
	_, entry := entryFixture(t, home, Platform(), nil)
	dest := filepath.Join(t.TempDir(), "vendor")
	res, err := CopyTree(CopyTreeOptions{Entry: entry, Prefix: ".local/share/vendor", Dest: dest})
	if err != nil {
		t.Fatalf("CopyTree: %v", err)
	}
	if res.Reflinked+res.Copied != 2 || res.Linked != 0 {
		t.Errorf("counters %+v", res)
	}
	var st syscall.Stat_t
	if err := syscall.Stat(filepath.Join(dest, "1.0", "data.txt"), &st); err != nil {
		t.Fatal(err)
	}
	if src := statT(t, filepath.Join(entry.Tree, ".local", "share", "vendor", "1.0", "data.txt")); src.Ino == st.Ino {
		t.Error("the copy shares the store's inode")
	}
}
