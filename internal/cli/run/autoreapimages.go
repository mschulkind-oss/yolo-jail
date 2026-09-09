package run

import (
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
)

// autoReapOptOutEnv is the escape hatch, in the same style as
// YOLO_ALLOW_STALE_IMAGE / YOLO_NO_HOST_LOOPBACK: any non-empty value drops
// back to today's behaviour (nothing is reaped automatically; `yolo prune
// --apply` remains available by hand). Named because an automatic `rmi -f`
// against a real, possibly-shared podman store is exactly the kind of
// aggressive default this codebase always ships a loud opt-out for, and
// because it is also the fast, honest way to verify the launch path (a
// nested-jail run, or any launch against a store with images worth keeping)
// without risking a real deletion.
const autoReapOptOutEnv = "YOLO_NO_AUTO_IMAGE_REAP"

// autoReapOldImages is the launch path's trigger for reclaiming superseded
// `localhost/yolo-jail` images (Ledger C, minimal-disk-footprint.md §3.3) —
// the OQ-DF3 ruling's TRIGGER half wired in. `yolo prune --apply`'s "Old
// yolo-jail images" section has always been SAFE (C2's dedup, the `podman ps`
// veto, and since OQ-LS3 a retention set that is one pointer per workspace);
// what was missing was ever running it (minimal-disk-footprint.md §1: the same
// hint sat true for 24+ days and 404+ GiB accrued regardless). This calls the
// exact same prune.PruneOldImages the manual command does, through
// prune.AutoReapOldImages's day-long debounce (P7: a launch checks in, the
// reap itself fires at most once per interval) — so this method only decides
// WHETHER to fire it, never how, and the veto/dedup are untouched.
//
// Call it AFTER autoLoadImage succeeds AND after recordCurrentImage
// (runContainer does both, in that order): this launch's own image is then this
// workspace's recorded current image, and therefore protected before the reap
// reads the retention set. Best-effort and silent on the common
// (debounced, opted-out, or nothing-to-remove) path; only prints when it
// actually freed something, so a launch that never triggers a reap looks
// exactly as it did before this existed. A failure or a skip here must never
// cost the launch — nothing here is checked by the caller.
func (o *Options) autoReapOldImages(rt string) {
	if o.Getenv(autoReapOptOutEnv) != "" {
		return
	}
	buildDir := paths.BuildDir()
	// Upstream factored this into pruneRunFunc (brokenprefix.go), which keeps the
	// same Timeout→Ran=false correction: a timed-out probe reports Ran=true with a
	// zero RC, and prune's `res.Ran && res.RC == 0` checks would misread a killed
	// process as a clean, empty success.
	run := o.pruneRunFunc()
	removed, ran, declined := prune.AutoReapOldImages(rt, buildDir, o.Now(), run)
	switch {
	case declined != "":
		// TO THE LOG, NOT THE TERMINAL. This runs in the post-launch slot, where
		// both streams belong to the jailed command — a warning here lands on top
		// of whatever the agent's TUI is drawing. It is still recorded in full,
		// and `yolo prune` reports the same cause with a non-zero exit where a
		// human actually asked (OQ-LS2).
		o.housekeepingNote("images: reap DECLINED — %s. Nothing was reclaimed. "+
			"This should not happen on a launch that is starting a container: the same "+
			"runtime answered the image load a moment ago.", declined)
	case ran && len(removed) > 0:
		o.housekeepingNote("images: reclaimed %d stale yolo-jail image(s) (OQ-DF3)", len(removed))
	}
}
