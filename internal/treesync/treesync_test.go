package treesync

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/treedigest"
)

func write(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func inode(t *testing.T, path string) uint64 {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("no syscall.Stat_t on this platform")
	}
	return st.Ino
}

func digest(t *testing.T, dir string) string {
	t.Helper()
	d, err := treedigest.Of(dir)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// A first sync into an absent destination is a copy: same bytes, same exec bits.
func TestSyncIntoAnAbsentDestinationCopies(t *testing.T) {
	src, dst := t.TempDir(), filepath.Join(t.TempDir(), "dst")
	write(t, filepath.Join(src, "pack.json"), `{"name":"x"}`, 0o644)
	write(t, filepath.Join(src, "bin", "tool"), "#!/bin/sh\n", 0o755)
	write(t, filepath.Join(src, "deep", "a", "b.txt"), "b", 0o600)

	changed, err := Sync(src, dst)
	if err != nil || !changed {
		t.Fatalf("Sync = (%v, %v), want (true, nil)", changed, err)
	}
	if digest(t, src) != digest(t, dst) {
		t.Error("the synced tree's content differs from its source")
	}
	for rel, want := range map[string]os.FileMode{"pack.json": 0o644, "bin/tool": 0o755, "deep/a/b.txt": 0o644, "deep/a": 0o755} {
		fi, err := os.Stat(filepath.Join(dst, rel))
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != want {
			t.Errorf("%s mode = %o, want %o (the staging copiers' rule)", rel, got, want)
		}
	}
}

// THE PROPERTY A LIVE BIND NEEDS: a re-sync of unchanged content touches nothing. Every file and
// directory keeps its inode, which is what a bind mount of it captured, and nothing is reported
// changed.
func TestSyncOfUnchangedContentKeepsEveryInode(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	write(t, filepath.Join(src, "loopholes", "broker", "manifest.jsonc"), "{}", 0o644)
	write(t, filepath.Join(src, "files", "footer.js"), "js", 0o644)
	if _, err := Sync(src, dst); err != nil {
		t.Fatal(err)
	}
	paths := []string{dst, filepath.Join(dst, "loopholes"), filepath.Join(dst, "loopholes", "broker"),
		filepath.Join(dst, "loopholes", "broker", "manifest.jsonc"), filepath.Join(dst, "files", "footer.js")}
	before := map[string]uint64{}
	for _, p := range paths {
		before[p] = inode(t, p)
	}

	changed, err := Sync(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("re-syncing identical content reported a change")
	}
	for _, p := range paths {
		if got := inode(t, p); got != before[p] {
			t.Errorf("%s was replaced (inode %d -> %d) although nothing about it changed; a live "+
				"bind of it now shows the removed copy", p, before[p], got)
		}
	}
}

// A changed file is REPLACED by rename, never rewritten in place (a reader sees old bytes or new
// ones), an added entry appears, a dropped one goes, and a directory present on both sides keeps
// its inode while its entries change.
func TestSyncOfChangedContentReplacesOnlyWhatChanged(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	write(t, filepath.Join(src, "keep.txt"), "same", 0o644)
	write(t, filepath.Join(src, "tree", "edit.txt"), "old", 0o644)
	write(t, filepath.Join(src, "tree", "drop.txt"), "gone soon", 0o644)
	if _, err := Sync(src, dst); err != nil {
		t.Fatal(err)
	}
	keepIno, treeIno := inode(t, filepath.Join(dst, "keep.txt")), inode(t, filepath.Join(dst, "tree"))
	editIno := inode(t, filepath.Join(dst, "tree", "edit.txt"))

	write(t, filepath.Join(src, "tree", "edit.txt"), "new", 0o644)
	if err := os.Remove(filepath.Join(src, "tree", "drop.txt")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(src, "tree", "added.txt"), "added", 0o755)

	changed, err := Sync(src, dst)
	if err != nil || !changed {
		t.Fatalf("Sync = (%v, %v), want (true, nil)", changed, err)
	}
	if digest(t, src) != digest(t, dst) {
		t.Error("the re-synced tree's content differs from its source")
	}
	if inode(t, filepath.Join(dst, "keep.txt")) != keepIno {
		t.Error("an unchanged file was replaced")
	}
	if inode(t, filepath.Join(dst, "tree")) != treeIno {
		t.Error("a directory present on both sides was replaced rather than recursed into")
	}
	if inode(t, filepath.Join(dst, "tree", "edit.txt")) == editIno {
		t.Error("a changed file kept its inode, so it was rewritten in place and a reader could see it truncated")
	}
	if _, err := os.Stat(filepath.Join(dst, "tree", "drop.txt")); !os.IsNotExist(err) {
		t.Errorf("an entry the source no longer has survived: %v", err)
	}
	// No temporary sibling is left behind.
	entries, _ := os.ReadDir(filepath.Join(dst, "tree"))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".yolo-sync-") {
			t.Errorf("a temporary file was left in the destination: %s", e.Name())
		}
	}
}

// An exec bit flipping is a change even when the bytes are identical.
func TestSyncTreatsAModeChangeAsAChange(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	write(t, filepath.Join(src, "tool"), "x", 0o644)
	if _, err := Sync(src, dst); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(src, "tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	if changed, err := Sync(src, dst); err != nil || !changed {
		t.Fatalf("Sync = (%v, %v), want (true, nil)", changed, err)
	}
	fi, _ := os.Stat(filepath.Join(dst, "tool"))
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("mode = %o, want 0755", fi.Mode().Perm())
	}
}

// A type change on either side is resolved in the source's favour, and the destination root is
// never replaced — not even to fix it.
func TestSyncReplacesAnEntryOfTheWrongTypeButNeverTheRoot(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	write(t, filepath.Join(src, "was-file"), "", 0o644)
	if err := os.Remove(filepath.Join(src, "was-file")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(src, "was-file", "inner"), "now a dir", 0o644)
	write(t, filepath.Join(src, "was-dir"), "now a file", 0o644)
	write(t, filepath.Join(dst, "was-file"), "a file", 0o644)
	write(t, filepath.Join(dst, "was-dir", "inner"), "a dir", 0o644)
	if err := os.Symlink("/etc/passwd", filepath.Join(dst, "a-link")); err != nil {
		t.Fatal(err)
	}
	rootIno := inode(t, dst)

	if _, err := Sync(src, dst); err != nil {
		t.Fatal(err)
	}
	if digest(t, src) != digest(t, dst) {
		t.Error("the synced tree's content differs from its source")
	}
	if inode(t, dst) != rootIno {
		t.Error("the destination root was replaced")
	}

	file := filepath.Join(t.TempDir(), "file")
	write(t, file, "x", 0o644)
	if _, err := Sync(src, file); err == nil {
		t.Error("a destination root that is a file was accepted; rule 1 says it is never replaced, so it must be refused")
	}
}
