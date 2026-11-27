package testsupport

import (
	"os"
	"path/filepath"
	"testing"
)

// UnsealOnCleanup registers a t.Cleanup that makes every file and directory under root
// writable again. The skill payload store publishes read-only trees (0555 directories), and
// t.TempDir's own cleanup cannot remove them as a non-root user (root ignores the mode, so a
// root jail passes and CI fails). Call it right after creating the store: cleanups run last
// in, first out, so this runs before TempDir's removal. Symlinks are never followed.
func UnsealOnCleanup(t testing.TB, root string) {
	t.Helper()
	t.Cleanup(func() {
		_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
			if err == nil && fi.Mode()&os.ModeSymlink == 0 {
				_ = os.Chmod(p, fi.Mode().Perm()|0700)
			}
			return nil
		})
	})
}
