package prune

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// THERE IS NO DefaultKeepImages ANY MORE, and this is where it was.
//
// It was `--keep-images`'s default (2), and minimal-disk-footprint.md OQ-DF3
// ruled the NUMBER unchanged on 2026-09-06 — as a small undo buffer sitting on
// top of the sentinel-derived veto rather than the safety mechanism itself.
// OQ-LS3 (docs/design/the-load-sentinel-is-not-a-liveness-oracle.md §6.2, ruled
// 2026-09-08) then ruled the MECHANISM out rather than the number: the unit is
// the CONFIGURATION and the superseded-per-config count is zero, so a global
// count has no depth left to bound and there is no undo buffer to size. What
// replaced it is CurrentImageTags — one pointer per workspace, union'd with the
// `podman ps` veto (currentimages.go).
//
// Do not reintroduce a count here to "soften" a reap. The floor is one image per
// configuration and it is a FLOOR: everything in use is protected regardless,
// and a kept image also holds its base layer in place.

// AutoReapInterval bounds how often the LAUNCH PATH's automatic old-image
// reap actually does anything beyond a cheap timestamp read.
// minimal-disk-footprint.md P7 warns that automating a sweep moves its
// trigger to "a schedule set by other people's jails" — this is the
// mitigation for Ledger C: every launch checks in, but the reap itself fires
// at most once per interval, so a busy multi-workspace machine never re-probes
// podman (or narrows another launch's grace window before its image ages out
// of the load-sentinel LRU-10) on every single launch. A day is short enough
// that the measured growth (~7-8 images every 3 days on the audited machine)
// can never again go unreaped for the weeks the manual command was
// (minimal-disk-footprint.md §1.1: 24+ days, 404+ GiB, on a hint nobody
// acted on) — the defect this replaces was "however long a human forgets",
// which was unbounded by construction; a day is not.
const AutoReapInterval = 24 * time.Hour

// autoReapSentinelName is BuildDir()'s file recording the last time the
// launch-path automatic reap actually RAN (as opposed to skipping via the
// debounce below). Deliberately a different file from the last-load-<runtime>
// sentinels image.AddLoadedPath writes: this one is prune's own clock, not a
// liveness ledger, and keeping the two apart means a bug in reading one can
// never widen what the other protects.
const autoReapSentinelName = "last-image-reap"

// DueForAutoImageReap reports whether at least `interval` has elapsed since
// the timestamp recorded at `sentinel`. A missing or unparseable sentinel
// reports true — a fresh machine, or one upgrading onto this feature for the
// first time, is always due. This only gates whether the pass RUNS AT ALL;
// it grants no license on its own — PruneOldImages' own liveness veto is what
// stays fail-safe once the pass does run, and that polarity is untouched
// here (see AutoReapOldImages).
func DueForAutoImageReap(sentinel string, interval time.Duration, now time.Time) bool {
	data, err := os.ReadFile(sentinel)
	if err != nil {
		return true
	}
	sec, perr := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if perr != nil {
		return true
	}
	return now.Sub(time.Unix(sec, 0)) >= interval
}

// RecordAutoImageReap stamps `sentinel` with now's Unix time, marking "an
// automatic reap pass just ran" so DueForAutoImageReap declines until the
// interval elapses again. The write error is discarded, on purpose and in the
// safe direction: a failed stamp only means the NEXT launch tries again
// immediately (more frequent checks), never a debounce stuck open forever.
func RecordAutoImageReap(sentinel string, now time.Time) {
	_ = os.WriteFile(sentinel, []byte(strconv.FormatInt(now.Unix(), 10)+"\n"), 0o644)
}

// AutoReapOldImages is the launch path's counterpart to `yolo prune`'s manual
// "Old yolo-jail images" section — minimal-disk-footprint.md OQ-DF3's
// TRIGGER half. The mechanism it drives (PruneOldImages: rows deduped by image
// ID, retained by the per-workspace current pointers, vetoed by `podman ps`) is
// exactly the one the manual command drives, so this call is as safe as a human
// typing `yolo prune --apply` — this function only decides WHETHER AND WHEN to
// make that same call on its own, never how. It reads the evidence from the same
// function (CurrentImageTags) rather than assembling its own, so the two
// entrances cannot come to different verdicts about one image.
//
// Debounced by DueForAutoImageReap so a busy machine does not re-probe podman
// on every launch (P7). Fails CLOSED exactly like the manual section: no
// honourable current-image pointer (known==false) declines the whole pass — and,
// deliberately, does NOT stamp the debounce sentinel in that case, so the very
// next launch (which may be the one whose own RecordCurrentImage call finally
// gives the pass its evidence) retries immediately rather than waiting out a
// full interval on a machine that was never actually protected in the first
// place.
//
// ran reports whether the pass actually executed (true even when removed is
// empty — the debounce, not "nothing to remove", is what ran distinguishes).
func AutoReapOldImages(rt, buildDir string, now time.Time, run RunFunc) (removed []string, ran bool, declined ImageReapDecline) {
	sentinel := filepath.Join(buildDir, autoReapSentinelName)
	if !DueForAutoImageReap(sentinel, AutoReapInterval, now) {
		// DEBOUNCED, not declined. Nothing is wrong and nothing is said: this is
		// the overwhelmingly common case, once per day per machine at most, and a
		// line here would be noise in front of every launch (OQ-LS2).
		return nil, false, ""
	}
	protectedTags, known := CurrentImageTags(buildDir)
	removed, declined = PruneOldImages(rt, protectedTags, known, true, run)
	if declined != "" {
		// DECLINED. Deliberately NOT stamped: the debounce records that a pass
		// ran, and a pass that could not establish its evidence did not run. The
		// old code returned (nil, false) for an unreadable ledger, which read as
		// "debounced" to the caller — indistinguishable from the quiet case.
		return nil, false, declined
	}
	RecordAutoImageReap(sentinel, now)
	return removed, true, ""
}
