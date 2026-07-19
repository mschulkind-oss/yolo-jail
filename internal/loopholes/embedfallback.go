package loopholes

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"

	bundledloopholes "github.com/mschulkind-oss/yolo-jail/bundled_loopholes"
)

// embedContentHash returns a short content hash over every embedded file
// (path + bytes, sorted), so materialized copies from different binary
// versions never collide and upgrades materialize fresh instead of serving a
// stale extraction.
var embedContentHash = sync.OnceValue(func() string {
	h := sha256.New()
	var paths []string
	_ = fs.WalkDir(bundledloopholes.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			paths = append(paths, p)
		}
		return nil
	})
	sort.Strings(paths)
	for _, p := range paths {
		b, err := fs.ReadFile(bundledloopholes.FS, p)
		if err != nil {
			continue
		}
		fmt.Fprintf(h, "%s\x00%d\x00", p, len(b))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
})

// materializeEmbedded extracts the embedded bundled loopholes to a
// content-addressed cache directory and returns it. Idempotent: an existing
// extraction is returned as-is (the final rename is atomic, so existence
// implies completeness). Used only when no checkout / in-jail copy exists —
// see BundledLoopholesDir.
func materializeEmbedded() (string, error) {
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	target := filepath.Join(cacheRoot, "yolo-jail", "bundled-loopholes", embedContentHash())
	if fileExists(target) {
		return target, nil
	}

	tmp := fmt.Sprintf("%s.tmp-%d", target, os.Getpid())
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	if err := os.RemoveAll(tmp); err != nil {
		return "", err
	}
	err = fs.WalkDir(bundledloopholes.FS, ".", func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		dst := filepath.Join(tmp, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		b, rerr := fs.ReadFile(bundledloopholes.FS, p)
		if rerr != nil {
			return rerr
		}
		return os.WriteFile(dst, b, 0o644)
	})
	if err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	if err := os.Rename(tmp, target); err != nil {
		// Lost a race with a concurrent extraction of the same content —
		// the winner's copy is byte-identical, use it.
		_ = os.RemoveAll(tmp)
		if fileExists(target) {
			return target, nil
		}
		return "", err
	}
	return target, nil
}
