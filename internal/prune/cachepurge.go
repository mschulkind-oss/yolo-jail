package prune

import (
	"os"
	"path/filepath"
	"time"
)

// CachePurgeDefaultSubdirs are the ~/.cache subdirs safe to purge by age —
// content is pure CAS with a fast re-download/recompute path.
var CachePurgeDefaultSubdirs = []string{
	"uv", "pip", "npm", "go-build", "mise", "pex", "pants", "node-gyp", "gopls",
}

// CachePurgeHeavySubdirs are opt-in age-purge subdirs with a meaningful re-fetch
// cost (playwright browsers ~400 MiB each; HF models GiBs).
var CachePurgeHeavySubdirs = []string{"ms-playwright", "huggingface"}

// cachePurgeForbidden are subdirs PurgeCacheByAge refuses to touch even when
// explicitly named — they carry live user profile state (cookies, IndexedDB,
// extensions) or the installed binaries of a tool, not regenerable cache.
var cachePurgeForbidden = map[string]struct{}{
	"chromium": {}, "google-chrome": {}, "chrome": {}, "mozilla": {},
	"firefox": {}, "thunderbird": {}, "copilot": {},
}

// PurgeCacheByAge removes regular files older than olderThanDays under each named
// subdir of cacheRoot. Returns (bytesRemoved, filesRemoved):
//   - only the caller-named subdirs are scanned (no glob, no recursion into the
//     allowlist);
//   - a subdir named in relocations is purged at its real host target instead
//     of under cacheRoot (see below);
//   - forbidden browser-profile subdirs are hard-excluded even if named;
//   - symlinks are never followed or deleted;
//   - staleness is keyed off mtime (>= cutoff is kept), not atime;
//   - apply=false returns accurate counts without mutating.
//
// relocations maps a cache subdir name to the absolute host directory that
// actually holds its bytes (nil when nothing is relocated). Without it the
// heavy purge would join cacheRoot/huggingface — the empty stub the relocation
// mount lands on — and report a successful purge of 0 B while the GiB it was
// aimed at sit untouched on the other filesystem. A relocated subdir is still
// subject to the forbidden-subdir check above: relocating something does not
// make it purgeable.
//
// now is the clock seam; the cutoff is now - olderThanDays*86400.
func PurgeCacheByAge(cacheRoot string, subdirs []string, relocations map[string]string, olderThanDays float64, apply bool, now time.Time) (bytesRemoved int64, filesRemoved int) {
	cutoff := now.Add(-time.Duration(olderThanDays * 86400 * float64(time.Second)))

	for _, sub := range subdirs {
		if _, forbidden := cachePurgeForbidden[sub]; forbidden {
			continue
		}
		root := filepath.Join(cacheRoot, sub)
		if target := relocations[sub]; target != "" {
			root = target
		}
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			continue
		}
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			st, err := os.Lstat(path)
			if err != nil {
				return nil
			}
			if st.Mode()&os.ModeSymlink != 0 {
				return nil
			}
			if !st.Mode().IsRegular() {
				return nil
			}
			// Kept when mtime >= cutoff.
			if !st.ModTime().Before(cutoff) {
				return nil
			}
			size := st.Size()
			if apply {
				if err := os.Remove(path); err != nil {
					return nil
				}
			}
			bytesRemoved += size
			filesRemoved++
			return nil
		})
	}
	return bytesRemoved, filesRemoved
}
