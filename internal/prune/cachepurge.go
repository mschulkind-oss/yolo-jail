package prune

import (
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// CachePurgeDefaultSubdirs are the ~/.cache subdirs safe to purge by age —
// content is pure CAS with a fast re-download/recompute path.
//
// `nce` was ADDED by OQ-BF2 (measured: 1.86 GiB already dead here, covered by
// nothing). `staticcheck` is deliberately still ABSENT, and the reason is a
// measurement rather than caution: it SELF-TRIMS. Both it and `go-build` use
// Go's own cache, which evicts entries unused for five days and sweeps at most
// once a day (trimLimit/trimInterval, cmd/go/internal/cache), leaving a
// `trim.txt` marker — verified present in both on 2026-09-08. A 30-day rule
// over a self-trimming cache can only ever be a no-op that costs a walk, and
// §2.1's own table shows it: of every subdir measured for files older than 30
// days, go-build is the only ZERO.
//
// go-build stays on the list because removing it is a behaviour change with no
// bytes behind it, but it must never be cited as evidence that this purge works.
var CachePurgeDefaultSubdirs = []string{
	"uv", "pip", "npm", "go-build", "mise", "pex", "pants", "node-gyp", "gopls", "nce",
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

	// The cache root is opened FOLLOWING a link at it, and so is a relocation target: neither
	// is a path the jail can replace (each is a jail's mountpoint, and the components above are
	// the host's own), and a user may well have put the machine store or a relocated cache on
	// another disk through one. Everything BELOW them is the jail's to write, so it is walked
	// and removed beneath the root (purgeOldFilesUnder).
	cache, _ := os.OpenRoot(cacheRoot)
	if cache != nil {
		defer cache.Close()
	}
	for _, sub := range subdirs {
		if _, forbidden := cachePurgeForbidden[sub]; forbidden {
			continue
		}
		var b int64
		var f int
		if target := relocations[sub]; target != "" {
			if r, err := os.OpenRoot(target); err == nil {
				b, f = purgeOldFilesUnder(r, ".", cutoff, apply)
				r.Close()
			}
		} else if cache != nil {
			b, f = purgeOldFilesUnder(cache, sub, cutoff, apply)
		}
		bytesRemoved += b
		filesRemoved += f
	}
	return bytesRemoved, filesRemoved
}

// purgeBeforeRemove, when set, is called with each file's path (relative to the purge's root)
// just before the purge removes it. It is a test seam, nil in production: the window it opens is
// the one in which a jail can swap a directory on the way for a link, between the walk's check
// and the removal.
var purgeBeforeRemove func(rel string)

// purgeOldFilesUnder removes regular files under rel below r whose mtime is before cutoff,
// returning (bytesRemoved, filesRemoved). Shared by PurgeCacheByAge and PurgeAgentLogs.
// Discipline (identical to the cache-purge contract):
//   - a missing/non-dir rel is a no-op (returns 0,0), and so is a symbolic link at rel;
//   - symlinks are never followed or deleted;
//   - only regular files are counted/removed (dirs are left as mount anchors);
//   - mtime >= cutoff is kept;
//   - apply=false computes accurate counts without mutating.
//
// BENEATH r, never by plain path (docs/reference/jail-home.md, "Host code in jail-writable
// state"): every tree this purges is one a jail can write, and the purge runs as the host user.
// A directory the jail swaps for a link, before the walk or between the walk's check and the
// removal, leaves the root, and r refuses the removal rather than deleting the host file of the
// same name behind it.
func purgeOldFilesUnder(r *os.Root, rel string, cutoff time.Time, apply bool) (bytesRemoved int64, filesRemoved int) {
	info, err := r.Lstat(rel)
	if err != nil || !info.IsDir() {
		return 0, 0
	}
	_ = fs.WalkDir(r.FS(), filepath.ToSlash(rel), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		path = filepath.FromSlash(path)
		st, err := r.Lstat(path)
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
			if purgeBeforeRemove != nil {
				purgeBeforeRemove(path)
			}
			if err := r.Remove(path); err != nil {
				return nil
			}
		}
		bytesRemoved += size
		filesRemoved++
		return nil
	})
	return bytesRemoved, filesRemoved
}
