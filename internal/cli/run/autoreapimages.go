package run

import (
	"time"

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
// yolo-jail images" section has always been SAFE (C2's dedup + the
// ProtectedImageTags liveness veto, hardened fail-safe in `4064f720`); what
// was missing was ever running it (minimal-disk-footprint.md §1: the same
// hint sat true for 24+ days and 404+ GiB accrued regardless). This calls the
// exact same prune.PruneOldImages the manual command does, through
// prune.AutoReapOldImages's day-long debounce (P7: a launch checks in, the
// reap itself fires at most once per interval) — so this method only decides
// WHETHER to fire it, never how, and the veto/dedup are untouched.
//
// Call it AFTER autoLoadImage succeeds (runContainer does): this launch's own
// image is then already recorded in the load sentinel
// image.AddLoadedPath just wrote, and therefore already protected before the
// reap's own liveness read runs. Best-effort and silent on the common
// (debounced, opted-out, or nothing-to-remove) path; only prints when it
// actually freed something, so a launch that never triggers a reap looks
// exactly as it did before this existed. A failure or a skip here must never
// cost the launch — nothing here is checked by the caller.
func (o *Options) autoReapOldImages(rt string) {
	if o.Getenv(autoReapOptOutEnv) != "" {
		return
	}
	buildDir := paths.BuildDir()
	run := func(argv []string, timeout time.Duration) prune.ProbeResult {
		res := o.Exec(argv, "", nil, timeout)
		// A timed-out probe still reports Ran=true with a zero-value RC
		// (runcmd.go's realExec) — treat it as "did not run" so prune's
		// `res.Ran && res.RC == 0` checks can't misread a killed process as a
		// clean, empty success.
		return prune.ProbeResult{Stdout: res.Stdout, RC: res.RC, Ran: res.Ran && !res.Timeout}
	}
	removed, ran, declined := prune.AutoReapOldImages(rt, buildDir, prune.DefaultKeepImages, o.Now(), run)
	switch {
	case declined != "":
		// OQ-LS2, the automatic path. This is LOUD and on STDERR, and both are
		// deliberate. Loud because a decline here should be impossible: the
		// reap runs on a launch that is about to `podman run`, so a runtime
		// that cannot answer `ps` cannot start a container either — reaching
		// this branch means something is wrong, not that housekeeping was
		// quietly skipped. Stderr because stdout belongs to the jailed command.
		//
		// NOT a dim line, which was the first proposal: dim is the volume for
		// something routine, and the only routine case (debounced, or nothing
		// to remove) says nothing at all.
		o.pr(o.Stderr).printf("[yellow]Warning: the automatic image reap declined — %s. "+
			"Nothing was reclaimed, and the launch continues.[/yellow]\n"+
			"[dim]  This should not happen on a launch that is starting a container: the same "+
			"runtime answered the image load a moment ago. `yolo prune` will report the same "+
			"cause with a non-zero exit.[/dim]", declined)
	case ran && len(removed) > 0:
		// STDERR, not stdout, since OQ-BF5 moved this into the post-launch
		// housekeeping slot: by then the pty is attached and stdout belongs to
		// the jailed command, so a line there lands inside the user's session
		// output — and would corrupt anything they piped.
		o.pr(o.Stderr).printf("[dim]Reclaimed %d stale yolo-jail image(s) automatically "+
			"(minimal-disk-footprint.md OQ-DF3; `yolo prune` shows the full picture).[/dim]",
			len(removed))
	}
}
