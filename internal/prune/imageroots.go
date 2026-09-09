package prune

import (
	"os"
	"path/filepath"
	"time"
)

// ImageRootRetention is how long an image GC root survives without a launch
// using it (OQ-LS1: "if you haven't built an image in a week you probably don't
// need the cache, you can wait again next build").
//
// A WEEK, and the number is the ruling rather than a tuning: it is the horizon
// over which "will I want this closure again" is actually decided. The value it
// replaced was 3600 s, which was never a policy — it was a race guard for a
// root a launch had just created, and an hour is far too short to predict want
// and far too long to guard a startup.
const ImageRootRetention = 7 * 24 * time.Hour

// PruneOrphanImageRoots reaps durable per-image GC roots (BUILD_DIR/roots/<sha16>,
// created host-side by image.RegisterImageRoot — storage-lifecycle §1) that no
// longer pin a needed image closure, so a subsequent nix GC can reclaim the
// store paths behind them. It NEVER deletes a store path itself — only the
// gcroot symlink — so the reclaim is deferred to `nix store gc` (§3) or the
// daemon's own auto-GC (§2).
//
// AGE IS THE WHOLE POLICY (OQ-LS1, ruled 2026-09-08). This function used to
// take a `protected` set read from the load sentinel and a `liveKnown` gate,
// and both are gone. The question it answers — "will I want this closure
// again?" — is a PREDICTION, and liveness is a wrong predictor of it in both
// directions: a jail stopped five seconds ago is not live, so a liveness veto
// would permit unrooting the closure it needs thirty seconds from now; a jail
// up for three weeks would pin a closure nobody will ever build again. The
// ruling: an unrooted-for-a-week closure goes, and when the guess is wrong the
// cost is one rebuild, which is the price a cache is allowed to charge.
//
// Two consequences worth stating because they look like weakenings:
//
//   - P3 ("unknown is not permission") no longer applies HERE, and is not
//     weakened anywhere else. An age policy has no authority it could fail to
//     reach — the mtime is on the link itself — so the fail-safe gate was
//     removed together with the question it guarded. Every other reaper in this
//     package keeps its tri-state.
//   - Losing a root costs a REBUILD, never a running jail. That is what makes
//     age admissible here, and it is exactly what does NOT hold for the install
//     prefix: a running jail EXECUTES from its prefix, so those roots keep a
//     liveness guarantee instead (disk-levers-and-backfill.md OQ-BF4). The test
//     for which policy a root gets is that one question.
//
// olderThan is now a RETENTION HORIZON, not a startup grace window. Callers pass
// ImageRootRetention; a much shorter value silently turns this back into the
// race guard it used to be, which is not a policy.
//
// A dangling root (target already gone) pins nothing and is reaped. Returns the
// roots removed (or that WOULD be removed in dry-run). Removing a symlink frees
// ~0 bytes directly (the closure bytes come back only on a later nix GC), so
// this reports a COUNT, deliberately not a byte total folded into the
// reclaimed-bytes summary.
func PruneOrphanImageRoots(rootsDir string, olderThan time.Duration, apply bool, now time.Time) []string {
	reaped := []string{}
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
		// AGE — the only test. The mtime is the last time a launch USED this
		// image: nix-store --add-root refreshes the symlink's own mtime even when
		// the link already points at the same store path, and AutoLoadImage
		// re-registers the root on every success rather than only on a load. So
		// "older than the horizon" already means "no launch has wanted this for
		// that long", with no bookkeeping of its own.
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
