package run

import (
	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
)

// recordCurrentImage records the image this launch made ready as THIS
// WORKSPACE'S CURRENT IMAGE — the retention evidence OQ-LS3 replaced
// `--keep-images` with (docs/design/the-load-sentinel-is-not-a-liveness-oracle.md
// §6.2, ruled 2026-09-08).
//
// It is one line of wiring and the whole feature depends on it. The reaper's
// retention set is the union of these pointers (prune.CurrentImageTags): with
// nothing writing them the set is empty, the sweep declines forever
// (fail-safe), and disk stops being reclaimed — the shape of defect that sat
// true for 24 days and 404 GiB before OQ-DF3's trigger. That is why
// currentimagecallsite_test.go pins the CALL rather than only this function.
//
// # WHERE IT SITS, AND THE WINDOW THAT IS LEFT
//
// Immediately after autoLoadImage returns OK and BEFORE the container starts,
// under the machine-wide housekeeping lock — the same lock OQ-BF5 gave the load
// path so an inspect-and-record can never interleave with another launch's reap.
// Taking it here is what keeps this write from landing in the middle of a sweep
// that has already read the pointer directory.
//
// A NARROW WINDOW SURVIVES, and it is smaller than the one OQ-BF5 closed rather
// than a reopening of it. AutoLoadImage brackets its own inspect + sentinel
// append; this write happens just after that bracket releases, so between the
// two another launch's sweep can see an image with no container on it. What
// covers that gap is THIS WORKSPACE'S PREVIOUS POINTER, which persists across
// launches and names the same store path on every relaunch of an unchanged
// config — precisely the case where AutoLoadImage finds the image already
// present and records nothing new. The residual is a launch whose store path
// CHANGED, into an image that is already loaded and that no workspace points at;
// the cost is a failed `podman run` on the next few milliseconds' timing, never
// a killed jail, because `rmi` is never forced (feddc5e0). Closing it outright
// means moving this write inside AutoLoadImage's existing bracket — the right
// fix, deliberately not taken here while internal/image is being reworked
// elsewhere.
//
// # WHAT A FAILURE COSTS, so it is never worth failing a launch over
//
// A pointer that cannot be written leaves this workspace unrepresented in the
// retention set. While the jail is up, guard #0 (`podman ps`) protects its image
// regardless; once it stops, a later sweep may remove that image, and the next
// launch re-streams it from a store closure OQ-LS1's GC-root policy still holds
// for a week. So the whole blast radius of a failed write is one image load, and
// the note lands in the housekeeping log rather than on a terminal that by then
// may belong to the jail.
//
// A DEGRADED LAUNCH RECORDS NOTHING, on purpose: AutoLoadImage's SkipBuild /
// build-failure fallback has no store path to name (LoadResult.StorePath is
// empty) and runs the image the flake's legacy `latest` tag points at, which
// prune.CurrentImageTags protects unconditionally for exactly this branch.
func (o *Options) recordCurrentImage(loaded image.LoadResult, cname string) {
	if loaded.StorePath == "" {
		return
	}
	unlock := func() {}
	if lockFn := o.lockHousekeepingFn(); lockFn != nil {
		unlock = lockFn()
	}
	defer unlock()
	if err := prune.RecordCurrentImage(paths.BuildDir(), cname, o.Workspace, loaded.StorePath); err != nil {
		o.housekeepingNote("images: could not record this workspace's current image (%v). "+
			"Its image is unprotected once this jail stops, so a later reap may remove it "+
			"and the next launch will re-stream it (OQ-LS3).", err)
	}
}
