package stores

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// WalkResult is one store's measurement, INCLUDING how complete it is. The
// completeness fields are not diagnostics: a total that silently dropped an
// unreadable subtree, or stopped at a deadline, is a wrong number wearing a
// right number's clothes, and this command's whole discipline is that no figure
// is printed without saying how it was obtained.
type WalkResult struct {
	// Bytes is the APPARENT size — the sum of file sizes, via lstat, symlinks
	// never followed. Hardlinked content is counted once per path that holds it,
	// which is the same rule the design's own /nix/store measurements used.
	Bytes int64
	Files int
	// DeadBytes/DeadFiles count the files older than the age cutoff, and are
	// filled only when one was given (--age).
	DeadBytes int64
	DeadFiles int
	// Partial is set when the walk hit its budget. Bytes/Files are then a LOWER
	// BOUND, and every renderer must say so.
	Partial bool
	// Unreadable counts subtrees skipped because the walk could not read them.
	// The walk keeps going — one unreadable directory is not a reason to lose the
	// whole store's size — but a non-zero count is reported, because a total that
	// quietly omits a subtree is exactly the wrong number this type exists to
	// prevent.
	Unreadable int
	Elapsed    time.Duration
}

// WalkFunc sizes one directory tree. cutoff is the --age boundary (zero => no
// age accounting); budget bounds the walk (zero => unbounded).
//
// The error contract is the caller's whole tri-state: os.ErrNotExist means the
// store is ABSENT (0 bytes, and that is not an error — a fresh machine has
// almost none of these), any other error means UNKNOWN (it exists and could not
// be read), and nil means the WalkResult stands.
type WalkFunc func(root string, cutoff time.Time, budget time.Duration, now func() time.Time) (WalkResult, error)

// walkTree is the real WalkFunc: an lstat-based recursive size walk with a
// deadline.
//
// The deadline is checked ONCE PER ENTRY rather than every N entries. time.Now
// is a vDSO read (~20ns), so even a 400k-file cache pays under 10ms for it,
// while a batched check would make the budget untestable without a synthetic
// tree of thousands of files — and a budget nothing can test is a budget that
// works until the day it matters.
func walkTree(root string, cutoff time.Time, budget time.Duration, now func() time.Time) (WalkResult, error) {
	if now == nil {
		now = time.Now
	}
	st, err := os.Lstat(root)
	if err != nil {
		return WalkResult{}, err // ErrNotExist => absent; anything else => unknown
	}
	start := now()
	if !st.IsDir() {
		// A store path is not always a tree. A nix output can be a single file
		// (every *-stream-yolo-jail path is a script), and treating that as an
		// error reported a whole class as "0 B, partial" — a wrong number with a
		// plausible label, which is the exact failure this package's Sizing field
		// exists to prevent. A regular file is its own size; anything else
		// (symlink, socket, device) is UNKNOWN, never 0.
		if !st.Mode().IsRegular() {
			return WalkResult{}, fmt.Errorf("not a regular file or directory")
		}
		res := WalkResult{Bytes: st.Size(), Files: 1, Elapsed: now().Sub(start)}
		if !cutoff.IsZero() && st.ModTime().Before(cutoff) {
			res.DeadBytes, res.DeadFiles = st.Size(), 1
		}
		return res, nil
	}
	deadline := start.Add(budget)

	var res WalkResult
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			res.Unreadable++
			return nil // skip this subtree, keep the rest of the store's size
		}
		if budget > 0 && !now().Before(deadline) {
			res.Partial = true
			return filepath.SkipAll
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			res.Unreadable++
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil
		}
		size := info.Size()
		res.Bytes += size
		res.Files++
		if !cutoff.IsZero() && info.ModTime().Before(cutoff) {
			// The same mtime rule prune.PurgeCacheByAge applies (mtime >= cutoff is
			// kept), so the "older than" column is legible against the reclaimer
			// that would act on it. It is NOT a claim that these bytes are
			// reclaimable: coverage is the reclaimer column's job, and some of
			// these subdirs have no reclaimer at all.
			res.DeadBytes += size
			res.DeadFiles++
		}
		return nil
	})
	res.Elapsed = now().Sub(start)
	return res, nil
}

// sizeStore runs a walk and folds its outcome into a store row, which is where
// the absent/unknown/partial tri-state becomes the Sizing a renderer prints.
func sizeStore(s *Store, root string, o Options) {
	var cutoff time.Time
	if o.Age {
		cutoff = o.Now().Add(-time.Duration(o.AgeDays * 24 * float64(time.Hour)))
	}
	res, err := o.Walk(root, cutoff, o.Budget, o.Now)
	switch {
	case errors.Is(err, os.ErrNotExist):
		s.Sizing = SizingAbsent
		return
	case err != nil:
		s.Sizing = SizingUnknown
		s.Reason = err.Error()
		return
	}
	s.Bytes = res.Bytes
	s.Files = res.Files
	s.Elapsed = res.Elapsed.Round(time.Millisecond).String()
	s.Unreadable = res.Unreadable
	s.Sizing = SizingMeasured
	if res.Partial {
		s.Sizing = SizingPartial
		s.Reason = fmt.Sprintf("walk budget %s exhausted", o.Budget)
	} else if res.Unreadable > 0 {
		// A total missing an unreadable subtree is a lower bound, and the honest
		// name for a lower bound here is the same one the deadline produces.
		s.Sizing = SizingPartial
		s.Reason = fmt.Sprintf("%d unreadable subtree(s)", res.Unreadable)
	}
	if o.Age {
		s.Dead = &Dead{Bytes: res.DeadBytes, Files: res.DeadFiles, OlderThanDays: o.AgeDays}
	}
}
