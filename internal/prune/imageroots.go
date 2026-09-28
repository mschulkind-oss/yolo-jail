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
// TWO RULES, and the second amends the first only where it was wrong.
//
// AGE, for every root no container is running on (OQ-LS1, ruled 2026-09-08). The
// question for such a root — "will I want this closure again?" — is a
// PREDICTION, and liveness is a wrong predictor of it in both directions: a jail
// stopped five seconds ago is not live, and a jail that ran for three weeks last
// month pins a closure nobody will build again. An unrooted-for-a-week closure
// goes, and when the guess is wrong the cost is one rebuild.
//
// LIVENESS, for the root of an image a container IS running on (OQ-LS4, ruled
// 2026-09-28): never reaped, however old. OQ-LS1's premise — losing a root costs
// a rebuild, never a running jail — is false for exactly that root on
// podman/Linux, where the host /nix/store is bind-mounted over the jail's own and
// every /bin/* resolves through it: a jail up longer than the horizon lost its
// root to age, and the next GC broke it (storage-lifecycle.md, 2026-07-22). With
// this rule a plain `nix-collect-garbage` on any schedule is safe for every jail
// yolo can map to a root. It is the rule the install-prefix roots already had
// (prefixroots.go, OQ-BF4), for the same reason: a running jail executes from it.
//
// liveKeys is LiveImageRootKeys' answer — the roots/<sha16> basenames of every
// running image — and liveKnown its tri-state. liveKnown=false reaps NOTHING:
// reaping is the dangerous direction here, so P3 ("unknown is not permission")
// applies to this pass again, which it stopped doing when the pass was age alone.
//
// olderThan is a RETENTION HORIZON, not a startup grace window. Callers pass
// ImageRootRetention; a much shorter value silently turns this back into the
// race guard it used to be, which is not a policy.
//
// A dangling root (target already gone) pins nothing and is reaped once old, even
// under a running image — there is nothing left for it to hold. Returns the roots
// removed (or that WOULD be removed in dry-run). Removing a symlink frees ~0
// bytes directly (the closure bytes come back only on a later nix GC), so this
// reports a COUNT, deliberately not a byte total folded into the reclaimed-bytes
// summary.
func PruneOrphanImageRoots(rootsDir string, liveKeys map[string]bool, liveKnown bool, olderThan time.Duration, apply bool, now time.Time) []string {
	reaped := []string{}
	if !liveKnown {
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
		// AGE. nix-store --add-root refreshes the symlink's own mtime even when
		// the link already points at the same store path, so the mtime is the
		// last time a launch ran this image FRESH — a build, or a stock-tag match
		// with a valid record (internal/image/stockimage.go). An attach, or a jail
		// simply staying up, refreshes nothing; that is what the liveness rule
		// below is for.
		if now.Sub(st.ModTime()) < olderThan {
			continue
		}
		// LIVENESS (OQ-LS4): a container is running on this image. A root whose
		// target is gone holds nothing for it, so that one still goes.
		if liveKeys[e.Name()] {
			if _, err := os.Stat(link); err == nil {
				continue
			}
		}
		reaped = append(reaped, link)
		if apply {
			_ = os.Remove(link)
		}
	}
	return reaped
}
