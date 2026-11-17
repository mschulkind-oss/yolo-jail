package nofollow

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWalkRootPermissionRestorationUsesOpenedRootAfterNameReplacement(t *testing.T) {
	parent := t.TempDir()
	owned := filepath.Join(parent, "owned")
	moved := filepath.Join(parent, "moved")
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(owned, "dir"), 0555); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(outside, "dir"), 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(filepath.Join(moved, "dir"), 0755)
		_ = os.Chmod(filepath.Join(outside, "dir"), 0755)
	})
	root, err := os.OpenRoot(owned)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.Rename(owned, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, owned); err != nil {
		t.Fatal(err)
	}
	if err := WalkRoot(root, "dir", nil, func(e Entry) error {
		if e.Kind == Dir {
			return e.File.Chmod(e.Perm | 0700)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	a, _ := os.Stat(filepath.Join(moved, "dir"))
	b, _ := os.Stat(filepath.Join(outside, "dir"))
	if a.Mode().Perm() != 0755 || b.Mode().Perm() != 0555 {
		t.Fatalf("permission restoration redirected: owned=%o outside=%o", a.Mode().Perm(), b.Mode().Perm())
	}
}
