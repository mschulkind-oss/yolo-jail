//go:build linux

package capture

// clone_linux_test.go pins the Linux reflink arm's CALL SITE on the one filesystem where FICLONE
// has no reason to refuse. TestReflinkGivesTheDestinationItsOwnInode (materialize_test.go) skips
// whenever reflinkOne refuses, and the chain tests and the capturematerialize integration cell
// accept any mechanism, so with the arm switched off every one of them stayed green, on btrfs
// too. Within one btrfs filesystem FICLONE always clones, so there a refusal FAILS.
//
// Only btrfs is held to it: XFS clones only when made with reflink=1 and ZFS only with block
// cloning on, so either may refuse for a reason that is no defect. Anything else skips, naming
// the filesystem. A GitHub runner's ext4 is among those, so CI cannot measure this arm; a jail on
// btrfs can.

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// btrfsTempDir is a fresh temp dir with its symlinks resolved (resolvedTempDir, confine_test.go),
// on btrfs, or the test skips.
func btrfsTempDir(t *testing.T) string {
	t.Helper()
	dir := resolvedTempDir(t)
	if kind := fsName(dir); kind != "btrfs" {
		t.Skipf("the temp dir %s is on %s, not btrfs: FICLONE is not certain to clone there", dir,
			orUnknown(kind))
	}
	return dir
}

// On btrfs every file of a materialize is a reflink: its own inode, with the manifest's mode, and
// no copy report.
func TestMaterializeReflinksEveryFileOnBtrfs(t *testing.T) {
	home := btrfsTempDir(t)
	_, entry := entryFixture(t, home, Platform(), nil)
	if kind := fsName(entry.Tree); kind != "btrfs" {
		t.Skipf("the store %s is on %s, not btrfs", entry.Tree, orUnknown(kind))
	}

	var errw bytes.Buffer
	res, err := Materialize(MaterializeOptions{Entry: entry, Home: home, Stderr: &errw})
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if res.Files != 2 || res.Reflinked != res.Files || res.Mechanism() != "reflink" {
		t.Fatalf("on one btrfs every file must be reflinked: %d files, %d reflinked / %d linked / "+
			"%d copied, mechanism %q, reflink retired by %q", res.Files, res.Reflinked, res.Linked,
			res.Copied, res.Mechanism(), res.ReflinkRetired)
	}
	if errw.Len() != 0 {
		t.Errorf("nothing was copied, so nothing should have been reported: %s", errw.String())
	}

	rel := filepath.Join(".local", "share", "vendor", "1.0", "vendor")
	si, di := statT(t, filepath.Join(entry.Tree, rel)), statT(t, filepath.Join(home, rel))
	if si.Ino == di.Ino {
		t.Fatalf("the reflink shares the store's inode (%d): a write through it would reach the store", si.Ino)
	}
	if di.Nlink != 1 {
		t.Errorf("the reflink has nlink %d, want 1", di.Nlink)
	}
	if fi, err := os.Lstat(filepath.Join(home, rel)); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm() != 0o755 {
		t.Errorf("the reflink has mode %v, want the manifest's 0755 rather than the store's frozen one",
			fi.Mode().Perm())
	}
}

// CopyTree, whose chain is reflink then copy, reflinks every file on btrfs too.
func TestCopyTreeReflinksEveryFileOnBtrfs(t *testing.T) {
	home := btrfsTempDir(t)
	_, entry := entryFixture(t, home, Platform(), nil)
	if kind := fsName(entry.Tree); kind != "btrfs" {
		t.Skipf("the store %s is on %s, not btrfs", entry.Tree, orUnknown(kind))
	}
	dest := filepath.Join(btrfsTempDir(t), "vendor")

	var errw bytes.Buffer
	res, err := CopyTree(CopyTreeOptions{Entry: entry, Prefix: ".local/share/vendor", Dest: dest, Stderr: &errw})
	if err != nil {
		t.Fatalf("CopyTree: %v", err)
	}
	if res.Files != 2 || res.Reflinked != res.Files {
		t.Fatalf("on one btrfs the tree's 2 files must be reflinked: %d files, %d reflinked / %d copied, "+
			"reflink retired by %q", res.Files, res.Reflinked, res.Copied, res.ReflinkRetired)
	}
	if errw.Len() != 0 {
		t.Errorf("nothing was copied, so nothing should have been reported: %s", errw.String())
	}
	if fi, err := os.Lstat(filepath.Join(dest, "1.0", "vendor")); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm() != fs.FileMode(0o755) {
		t.Errorf("the reflink has mode %v, want the manifest's 0755", fi.Mode().Perm())
	}
}
