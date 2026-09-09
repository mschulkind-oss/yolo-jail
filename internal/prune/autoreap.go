package prune

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultKeepImages is `--keep-images`'s default (NewDefaultOptions in
// prunecmd.go references this constant rather than a second literal, so the
// manual command's number and the automatic launch-path reap's number
// (AutoReapOldImages below) can never silently diverge).
//
// minimal-disk-footprint.md OQ-DF3, ruled 2026-09-06: this stays a SMALL
// undo-buffer margin ON TOP OF the sentinel-derived liveness veto
// (ProtectedImageTags), not the sole retention mechanism. The veto already
// keeps every recently-USED image (the load sentinel's LRU-10, across all
// runtimes) regardless of this count — that is the "keep-by-use" rule the
// PruneOldImages doc comment already anticipated. `keep` only decides how
// many otherwise-unprotected, already-superseded images additionally survive
// as a look-back/undo buffer, for the same "I applied, noticed, applied
// again, then looked" reason hostArchiveKeep and PruneRetiredLoopholeState's
// keep exist. The measured evidence (docs/reference/image-staging-vs-baking.md,
// "Cost model": ~24 images / 38.68 GB, ~2.7 GB unique per Go-only rebuild)
// shows this number was never the defect — `keep=2` applied today already
// reduces that to roughly 6 GB. What was missing was a trigger; see
// AutoReapOldImages.
const DefaultKeepImages = 2

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
// TRIGGER half. The mechanism it drives (PruneOldImages: the CreatedAt-sorted
// keep window, deduped by image ID, vetoed by ProtectedImageTags) is
// UNCHANGED, so this call is exactly as safe as a human typing
// `yolo prune --apply` — this function only decides WHETHER AND WHEN to make
// that same call on its own, never how.
//
// Debounced by DueForAutoImageReap so a busy machine does not re-probe podman
// on every launch (P7). Fails CLOSED exactly like the manual section: an
// unreadable load-sentinel ledger (liveKnown==false) declines the whole
// pass — and, deliberately, does NOT stamp the debounce sentinel in that
// case, so the very next launch (which may be the one whose own
// image.AddLoadedPath call finally makes the ledger readable) retries
// immediately rather than waiting out a full interval on a machine that was
// never actually protected in the first place.
//
// keep is the retention count (DefaultKeepImages in production); callers pass
// it rather than this function inventing its own, so the manual command's
// number and the automatic one can never silently diverge. ran reports
// whether the pass actually executed (true even when removed is empty — the
// debounce, not "nothing to remove", is what ran distinguishes).
func AutoReapOldImages(rt, buildDir string, keep int, now time.Time, run RunFunc) (removed []string, ran bool, declined ImageReapDecline) {
	sentinel := filepath.Join(buildDir, autoReapSentinelName)
	if !DueForAutoImageReap(sentinel, AutoReapInterval, now) {
		// DEBOUNCED, not declined. Nothing is wrong and nothing is said: this is
		// the overwhelmingly common case, once per day per machine at most, and a
		// line here would be noise in front of every launch (OQ-LS2).
		return nil, false, ""
	}
	protectedTags, liveKnown := ProtectedImageTags(buildDir)
	removed, declined = PruneOldImages(rt, keep, protectedTags, liveKnown, true, run)
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
