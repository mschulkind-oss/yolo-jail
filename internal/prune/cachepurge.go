package prune

import (
	"os"
	"path/filepath"
	"strings"
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

// CachePurgeBudget bounds one cache purge pass, the walk and the deletions alike (§5.3's 60 s;
// CI-D7 in docs/design/cache-isolation.md). Past it the pass stops and reports itself partial.
const CachePurgeBudget = 60 * time.Second

// purgeBatch is how many directory entries the walk reads at a time (CI-D7), so that the deadline
// is checked between batches even inside one very large directory.
const purgeBatch = 256

// purgeClock is the clock the deadline is read from; a test seam.
var purgeClock = time.Now

// PurgeCacheByAge removes regular files older than olderThanDays under each named
// subdir of cacheRoot. Returns (bytesRemoved, filesRemoved):
//   - only the caller-named subdirs are scanned (no glob, no recursion into the
//     allowlist);
//   - a subdir named in relocations is purged at its real host target instead
//     of under cacheRoot (see below);
//   - forbidden browser-profile subdirs are hard-excluded even if named;
//   - credentials and lock files are always held, whatever their age (cacheHeld);
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
// now is the clock seam; the cutoff is now - olderThanDays*86400. It has no deadline:
// PurgeCacheByAgeWithin is the bounded form every production caller uses.
func PurgeCacheByAge(cacheRoot string, subdirs []string, relocations map[string]string, olderThanDays float64, apply bool, now time.Time) (bytesRemoved int64, filesRemoved int) {
	return PurgeCacheByAgeGuarded(cacheRoot, subdirs, relocations, olderThanDays, apply, now, nil)
}

// PurgeCacheByAgeGuarded is PurgeCacheByAge with each file's removal bracketed by guard
// (guard.go). Its recheck re-reads the file right before the removal: a file some jail
// rewrote since the walk saw it is no longer older than the cutoff, and is kept.
func PurgeCacheByAgeGuarded(cacheRoot string, subdirs []string, relocations map[string]string, olderThanDays float64, apply bool, now time.Time, guard Guard) (bytesRemoved int64, filesRemoved int) {
	b, f, _ := PurgeCacheByAgeWithin(cacheRoot, subdirs, relocations, olderThanDays, apply, now, guard, time.Time{})
	return b, f
}

// PurgeCacheByAgeWithin is PurgeCacheByAgeGuarded stopped at deadline (zero: none). The deadline
// is checked before each batch of directory entries and before each removal, and partial reports
// that it stopped the pass: the counts are then what it reached, a lower bound, and the caller
// must not record the pass as complete (CI-D7).
func PurgeCacheByAgeWithin(cacheRoot string, subdirs []string, relocations map[string]string, olderThanDays float64, apply bool, now time.Time, guard Guard, deadline time.Time) (bytesRemoved int64, filesRemoved int, partial bool) {
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
		w := purgeWalk{cutoff: cutoff, apply: apply, guard: guard, deadline: deadline,
			hold: func(rel string) bool { return cacheHeld(sub, rel) }}
		if target := relocations[sub]; target != "" {
			if r, err := os.OpenRoot(target); err == nil {
				w.run(r, ".")
				r.Close()
			}
		} else if cache != nil {
			w.run(cache, sub)
		}
		bytesRemoved += w.bytes
		filesRemoved += w.files
		if w.partial {
			return bytesRemoved, filesRemoved, true
		}
	}
	return bytesRemoved, filesRemoved, false
}

// cacheHeld reports whether the file at rel (slash-separated, relative to the bucket's root) is
// one the age purge must keep whatever its age: the plan's "always hold" rows
// (docs/design/cache-isolation-plan.md, V6). A credential is not cache — Hugging Face keeps its
// login token at huggingface/token and its named tokens in huggingface/stored_tokens, both caught
// by the "token" rule — and a lock or control file's age says nothing about whether a process
// holds it (uv/.lock is uv's live cache lock).
func cacheHeld(bucket, rel string) bool {
	parts := strings.Split(rel, "/")
	for _, part := range parts {
		l := strings.ToLower(part)
		if strings.Contains(l, "token") || strings.Contains(l, "auth") {
			return true
		}
	}
	base := strings.ToLower(parts[len(parts)-1])
	switch {
	case base == "lock", base == ".lock", strings.HasSuffix(base, ".lock"), strings.HasSuffix(base, ".lck"):
		return true
	case base == "cachedir.tag":
		return true
	case bucket == "uv" && rel == ".gitignore":
		return true
	}
	return false
}

// purgeBeforeRemove, when set, is called with each file's path (relative to the purge's root)
// just before the purge removes it. It is a test seam, nil in production: the window it opens is
// the one in which a jail can swap a directory on the way for a link, between the walk's check
// and the removal.
var purgeBeforeRemove func(rel string)

// purgeOldFilesUnder removes regular files under rel below r whose mtime is before cutoff,
// returning (bytesRemoved, filesRemoved), with no deadline and nothing held. Shared by
// PurgeAgentLogs; the cache purge runs the same walk (purgeWalk) with its holds and deadline.
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
func purgeOldFilesUnder(r *os.Root, rel string, cutoff time.Time, apply bool, guard Guard) (bytesRemoved int64, filesRemoved int) {
	w := purgeWalk{cutoff: cutoff, apply: apply, guard: guard}
	w.run(r, rel)
	return w.bytes, w.files
}

// purgeWalk is one bounded walk of a tree beneath an os.Root (CI-D7): directories are read
// purgeBatch entries at a time, and the deadline is checked before each batch and each removal.
type purgeWalk struct {
	cutoff   time.Time
	apply    bool
	guard    Guard
	hold     func(rel string) bool // rel is slash-separated, relative to the walk's start; nil holds nothing
	deadline time.Time             // zero: none

	bytes   int64
	files   int
	partial bool
}

func (w *purgeWalk) expired() bool {
	if !w.partial && !w.deadline.IsZero() && !purgeClock().Before(w.deadline) {
		w.partial = true
	}
	return w.partial
}

func (w *purgeWalk) run(r *os.Root, rel string) {
	info, err := r.Lstat(rel)
	if err != nil || !info.IsDir() {
		return
	}
	w.dir(r, rel, "")
}

// dir walks the directory at path (relative to r); under is its path relative to the walk's
// start, slash-separated, for the holds.
func (w *purgeWalk) dir(r *os.Root, path, under string) {
	f, err := r.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	for {
		if w.expired() {
			return
		}
		ents, err := f.ReadDir(purgeBatch)
		for _, e := range ents {
			p := filepath.Join(path, e.Name())
			u := e.Name()
			if under != "" {
				u = under + "/" + e.Name()
			}
			if e.IsDir() {
				w.dir(r, p, u)
			} else {
				w.file(r, p, u)
			}
			if w.partial {
				return
			}
		}
		if err != nil { // io.EOF, or a directory that stopped reading
			return
		}
	}
}

func (w *purgeWalk) file(r *os.Root, path, under string) {
	if w.hold != nil && w.hold(under) {
		return
	}
	st, err := r.Lstat(path)
	if err != nil || st.Mode()&os.ModeSymlink != 0 || !st.Mode().IsRegular() {
		return
	}
	// Kept when mtime >= cutoff.
	if !st.ModTime().Before(w.cutoff) {
		return
	}
	size := st.Size()
	if w.apply {
		if w.expired() {
			return
		}
		removed := false
		w.guard.Do(func() bool {
			// Under the guard's lock, the walk's own test, asked again.
			again, err := r.Lstat(path)
			return err == nil && again.Mode().IsRegular() && again.ModTime().Before(w.cutoff)
		}, func() {
			if purgeBeforeRemove != nil {
				purgeBeforeRemove(path)
			}
			removed = r.Remove(path) == nil
		})
		if !removed {
			return
		}
	}
	w.bytes += size
	w.files++
}
