package treesync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSyncOwnedLinksPreservesOnlyPlannedLinksAndDirectoryInode(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	external := filepath.Join(t.TempDir(), "external")
	if err := os.WriteFile(external, []byte("untouched"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(src, "chosen"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "chosen/SKILL.md"), []byte("private"), 0644); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(t.TempDir(), "jail-path")
	if err := os.Symlink(dangling, filepath.Join(src, "chosen/scripts")); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(src, dst); err == nil {
		t.Fatal("ordinary Sync accepted a link")
	}
	before, _ := os.Stat(dst)
	if err := os.Symlink(external, filepath.Join(dst, "stale")); err != nil {
		t.Fatal(err)
	}
	plan := map[string]string{"chosen/scripts": dangling}
	if changed, err := SyncOwnedLinks(src, dst, plan); err != nil || !changed {
		t.Fatalf("sync: %v %v", changed, err)
	}
	after, _ := os.Stat(dst)
	if !os.SameFile(before, after) {
		t.Fatal("bound destination inode replaced")
	}
	if target, err := os.Readlink(filepath.Join(dst, "chosen/scripts")); err != nil || target != dangling {
		t.Fatalf("link: %q %v", target, err)
	}
	if data, err := os.ReadFile(external); err != nil || string(data) != "untouched" {
		t.Fatal("stale link target touched")
	}
	if changed, err := SyncOwnedLinks(src, dst, plan); err != nil || changed {
		t.Fatalf("unchanged sync: %v %v", changed, err)
	}
	if _, err := SyncOwnedLinks(src, dst, map[string]string{"chosen/scripts": "/wrong"}); err == nil {
		t.Fatal("unplanned target allowed")
	}
}
