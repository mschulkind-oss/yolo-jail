package prune

import (
	"os"
	"path/filepath"
	"time"
)

// PruneOrphanImageRoots reaps durable per-image GC roots (BUILD_DIR/roots/<sha16>,
// created host-side by image.RegisterImageRoot — storage-lifecycle §1) that no
// longer pin a needed image closure, so a subsequent nix GC can reclaim the
// store paths behind them. It NEVER deletes a store path itself — only the
// gcroot symlink — so the reclaim is deferred to `nix store gc` (§3) or the
// daemon's own auto-GC (§2).
//
// This is the one destructive gcroot operation in prune, so it is triple-guarded
// against the incident it exists to prevent (unrooting a live image):
//
//  1. FAIL-SAFE liveness gate — liveKnown==false (the runtime couldn't be
//     enumerated) reaps NOTHING, the same tri-state polarity as
//     PruneOrphanBuildRoots / ReapRelayOrphans (unknown ≠ "nothing live").
//  2. PROTECTED set — a root whose symlink target is any store path currently in
//     a runtime's load sentinel (image.ReadLoadedPaths, LRU of the last 10 loads
//     per runtime) is kept. The image a live container runs is always the most
//     recent sentinel entry, so this never unroots a running closure; at most it
//     over-retains ~10 roots per runtime.
//  3. AGE floor — a root younger than olderThan is kept, covering a root just
//     created by an in-flight `yolo run` before its path reached the sentinel.
//
// A dangling root (target already gone) pins nothing and is reaped when the
// liveness gate passes. Returns (rootsRemoved, that WOULD be removed in dry-run).
// Removing a symlink frees ~0 bytes directly (the closure bytes come back only on
// a later nix GC), so this reports a COUNT, deliberately not a byte total folded
// into the reclaimed-bytes summary.
func PruneOrphanImageRoots(rootsDir string, protected map[string]struct{}, liveKnown bool, olderThan time.Duration, apply bool, now time.Time) []string {
	reaped := []string{}
	if !liveKnown {
		// Liveness unknown — decline to touch any root (fail safe).
		return reaped
	}
	entries, err := os.ReadDir(rootsDir)
	if err != nil {
		return reaped // no roots dir yet (nothing ever rooted) → nothing to reap
	}
	for _, e := range entries {
		link := filepath.Join(rootsDir, e.Name())
		st, err := os.Lstat(link)
		if err != nil {
			continue
		}
		// Only ever touch symlinks under roots/ — a stray regular file/dir here is
		// not ours to remove.
		if st.Mode()&os.ModeSymlink == 0 {
			continue
		}
		// (2) protected — the target store path is a currently/recently loaded
		// image. A dangling link (Readlink ok but target may be gone) is compared
		// by its recorded target string, which still matches a sentinel entry if
		// that path was the loaded one; otherwise it falls through to reaping.
		target, rerr := os.Readlink(link)
		if rerr == nil {
			if _, keep := protected[target]; keep {
				continue
			}
		}
		// (3) age floor — skip a root created within the startup grace window.
		if now.Sub(st.ModTime()) < olderThan {
			continue
		}
		reaped = append(reaped, link)
		if apply {
			_ = os.Remove(link)
		}
	}
	return reaped
}
