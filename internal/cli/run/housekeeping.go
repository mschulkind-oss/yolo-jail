package run

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/flakebundle"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// housekeeping.go is the POST-LAUNCH SLOT and the lock that serialises it
// (disk-levers-and-backfill.md §5.1, OQ-BF5).
//
// HOUSEKEEPING SLOT — the window in the host `yolo` process AFTER the container
// is running and attached, while the launcher is otherwise waiting on its child.
// It exists structurally: the launcher spawns the runtime and waits, it does not
// exec into it. Work done there never delays the jail, needs no daemon (it dies
// with the launch), and runs on a host process with the host's view of every
// store.
//
// THREE PROPERTIES OF THE SLOT THAT ARE NOT OBVIOUS, and that anything added
// here has to respect:
//
//  1. THE GOROUTINE DIES ON os.Exit. The terminate arm exits the process, so a
//     class can be cut mid-delete. Every pass must therefore be restartable and
//     must stamp its debounce ONLY on completion — a stamp written first turns
//     one interrupted pass into a day of not running.
//  2. STDOUT IS THE CONTAINER'S BY THEN. The pty is attached, so anything the
//     slot prints on stdout lands inside the user's session output. Slot work
//     writes to stderr or not at all.
//  3. IT IS NOT A BACKGROUND JOB. Same process, same lifetime, same YOLO_*
//     environment the launch had.

// housekeepingNote records what the slot did. IT NEVER WRITES TO THE TERMINAL,
// and that is the whole point of the function existing.
//
// THE BUG THIS FIXES. The slot's notices started on stdout, moved to stderr
// when OQ-BF5 put them after the container attaches — and stderr is the SAME
// TERMINAL. A dim line from the launcher's goroutine lands on top of whatever
// the agent's TUI is drawing, so a reclaim overlaid a running session. §5.1
// already said it: "Nothing that needs a TTY runs there — by then the TTY is
// the container's." Both streams are the container's, not just stdout.
//
// The file is <workspace>/.yolo/housekeeping.log, beside boot.log and
// host-perf.log, so the three things a launch does behind the user's back are
// greppable in one place. `yolo stores` and `yolo prune` are the human-facing
// surfaces for this information; a launch is not.
//
// Best-effort: a launch must never fail because housekeeping could not write a
// note about itself.
func (o *Options) housekeepingNote(format string, args ...any) {
	writeHousekeepingNote(paths.OpenWorkspaceStateFile, o.Workspace, o.Now(), fmt.Sprintf(format, args...))
}

// housekeepingLogName is the note file directly under <workspace>/.yolo.
const housekeepingLogName = "housekeeping.log"

// writeHousekeepingNote appends one timestamped line to workspace's housekeeping.log,
// opened through open: paths.OpenWorkspaceStateFile for a writer that belongs to a live
// launch, paths.OpenExistingWorkspaceStateFile for one that may outlive the workspace (the
// scratch remover). Beneath a root on `.yolo` either way, never by path: the directory is
// jail-writable, and a link left at the name would take the note to the file it names.
func writeHousekeepingNote(open func(workspace, name string, flag int, perm fs.FileMode) (*os.File, error),
	workspace string, now time.Time, line string) {
	f, err := open(workspace, housekeepingLogName, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "%s  %s\n", now.UTC().Format(time.RFC3339), line)
}

// housekeepingLockName is the machine-wide lock every housekeeping DELETION and
// every load-and-record step takes.
//
// MACHINE-WIDE, not per workspace: the stores it protects (podman's image store,
// build/roots, the caches) are machine-wide, so two launches in different
// workspaces are exactly the collision it exists to prevent.
//
// HELD ONE DELETION AT A TIME (OQ-PR2 of docs/design/podman-reboot-readiness.md,
// ruled 2026-09-29). It used to be held for the whole pass, and every other
// launch's image re-inspect waited for that pass: 16.1 s at the 2026-09-29 reboot,
// on a host already slow because podman was finishing its post-boot refresh. A
// pass now takes it around each deletion and lets go between deletions, so a
// re-inspect waits at most one deletion.
const housekeepingLockName = "housekeeping.lock"

// HousekeepingLockPath is that lock's path, beside the per-workspace locks.
func HousekeepingLockPath() string {
	return filepath.Join(paths.GlobalStorage(), "locks", housekeepingLockName)
}

// housekeepingPassLockName is the lock a housekeeping PASS holds for its whole
// length, non-blocking, beside the per-deletion one. It is what keeps two passes
// from interleaving now that the shared lock is let go between deletions: a second
// launch's slot finds it held and skips its pass, as it skipped when the shared lock
// was held for the pass. Nothing but a pass takes it, so nothing ever waits on it.
const housekeepingPassLockName = "housekeeping-pass.lock"

// HousekeepingPassLockPath is the pass lock's path, beside the shared one.
func HousekeepingPassLockPath() string {
	return filepath.Join(paths.GlobalStorage(), "locks", housekeepingPassLockName)
}

// withHousekeepingPass runs fn as one housekeeping pass, and SKIPS fn entirely if
// another pass is running.
//
// SKIP RATHER THAN WAIT, deliberately, and for passes this is unchanged. Every
// caller is best-effort housekeeping on a debounce; another launch's pass running
// means the work is already being done, so waiting would buy a duplicate pass at the
// price of a slot. The one thing that must not happen is two passes interleaving,
// and the pass lock, taken non-blocking for the whole pass, prevents it between
// passes of this rule.
//
// fn is handed the per-deletion prune.Guard every class brackets each deletion with:
// it takes the SHARED lock (HousekeepingLockPath), blocking, runs the class's
// recheck, deletes only if the item is still unused, and lets go. Blocking is right
// there: the other holders are a launch's re-inspect-and-record, which is short, and
// a pass from a yolo older than this rule, which knows no pass lock and holds the
// shared lock for its whole pass. Waiting that out keeps the two passes' DELETIONS
// from overlapping, not the passes from interleaving: an old pass that finds the
// shared lock free between this pass's deletions runs whole there, and this pass's
// next deletion rechecks after it (PR-D14). Only an upgrade in progress mixes them.
//
// THE RACE THE SHARED LOCK CLOSES is narrower than "two reapers at once", because
// `rmi` is no longer forced (feddc5e0) and fails on an image with a container. What
// is left is the window between another launch's image inspect and its
// AddLoadedPath: B decides its image is present, A's reap sees no container on it
// yet and no sentinel entry, and removes it — then B's `podman run` fails on an
// image that existed a moment ago. So the LOAD side takes the same lock around its
// re-inspect-and-record, and only that; and since a pass no longer keeps that
// re-inspect out for its whole length, the image class's recheck reads the load
// sentinel B records under the lock (prune.AutoReapOldImagesGuarded).
func (o *Options) withHousekeepingPass(fn func(guard prune.Guard)) {
	pass, ok := openLockFile(HousekeepingPassLockPath())
	if !ok {
		return
	}
	defer pass.Close()
	if err := syscall.Flock(int(pass.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return // another pass is running: it is doing this work
	}
	defer func() { _ = syscall.Flock(int(pass.Fd()), syscall.LOCK_UN) }()
	shared, ok := openLockFile(HousekeepingLockPath())
	if !ok {
		return
	}
	defer shared.Close()
	fn(deletionGuard(shared))
}

// deletionGuard is the per-deletion bracket over the shared housekeeping lock: take it,
// recheck, delete, let go. A lock that cannot be taken skips the deletion — the safe
// direction for a reaper, and the next pass retries.
func deletionGuard(shared *os.File) prune.Guard {
	return func(recheck func() bool, del func()) bool {
		fd := int(shared.Fd())
		if err := syscall.Flock(fd, syscall.LOCK_EX); err != nil {
			return false
		}
		// Released with LOCK_UN, the file staying open for the next deletion (flock.go's
		// rule): a child a deletion forks carries the descriptor until its exec, and
		// LOCK_UN on any duplicate releases the lock for all of them.
		defer func() { _ = syscall.Flock(fd, syscall.LOCK_UN) }()
		if recheck != nil && !recheck() {
			return false
		}
		del()
		return true
	}
}

// openLockFile opens (creating) a lock file under locks/, false when it cannot.
func openLockFile(path string) (*os.File, bool) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, false
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, false
	}
	return f, true
}

// housekeepingSlots is every housekeeping slot this process has running. A launch never waits on
// it: the slot dies at the launch's exit, restartable by design. A test that drives a launch past the
// jail's start waits on it before its cleanup ends (dispatchOptions), because the slot reads HOME as
// it goes and, outliving its test, writes its stamps under the next test's HOME.
var housekeepingSlots sync.WaitGroup

// runHousekeeping is the slot's body: every automatic class, in one place, as one
// pass, after the container is up, each deletion under the shared lock.
//
// Ordering inside it is not load-bearing today — each class is independent and
// debounced on its own stamp. What IS load-bearing is that this runs AFTER the
// container is visible (so a reap can never race this launch's own image) and
// that nothing here can fail the launch.
func (o *Options) runHousekeeping(rt string, reclaimConsent reclaimConsent, cname string) {
	sp := o.Perf.Span("housekeeping.slot")
	defer sp.End()
	o.withHousekeepingPass(func(guard prune.Guard) {
		o.autoReapOldImages(rt, guard)
		o.reapSupersededStoreOutputs(rt, guard)
		o.measureAndPurgeCache(reclaimConsent, guard)
		o.measureAndPurgeMiseVersions(rt, reclaimConsent, guard)
		o.reapSmallAutomaticClasses(rt, cname, guard)
		o.reapImageTars(rt, guard)
		o.reapFlakeBundleGenerations(rt, guard)
		// Deletes nothing in the slot: it starts the detached remover, which removes each
		// volume only once podman says no container references it (scratchremoval.go).
		o.reapScratchVolumes(rt)
	})
}

// measureAndPurgeCache is the offered tier's SLOT half (§5.3, "measure late,
// offer early"): it walks the host cache, stamps what it found for the NEXT
// launch to offer on, and purges only if this launch already has consent.
//
// The walk lives here and never in front of a launch — §2.1 measured 369k files
// in this class. It is bounded by cacheWalkBudget, and a class that exceeds it
// reports what it summed so far as partial, which the offer then says out loud
// rather than presenting a short count as the whole truth.
func (o *Options) measureAndPurgeCache(consent reclaimConsent, guard prune.Guard) {
	if o.inJail() {
		return // the host cache is the host's to sweep
	}
	// The walk is the expensive thing in this whole design, so it is the one
	// most in need of the per-class debounce. A STANDING yes is not exempt: there
	// is nothing new to reclaim an hour after the last one. A yes given at THIS
	// launch's prompt is — it was given against a measurement the debounce is
	// about to hide (reclaimConsent).
	consented := consent.has(cachePurgeClass)
	due, done := o.classDebounce("cache")
	if !due && !consent.freshFor(cachePurgeClass) {
		return
	}
	gs := paths.GlobalStorage()
	cacheRoot := filepath.Join(gs, "cache")
	subdirs := prune.CachePurgeDefaultSubdirs

	// DRY-RUN FIRST, always: the measurement is what the next launch offers on,
	// and it must exist whether or not this launch may delete anything.
	start := o.Now()
	bytes, files := prune.PurgeCacheByAge(cacheRoot, subdirs, nil, cacheAgeDays, false, o.Now())
	RecordOfferMeasurement(cachePurgeClass, offerMeasurement{
		Bytes:   bytes,
		Detail:  fmtCachePurgeDetail(files),
		When:    o.Now(),
		Partial: o.Now().Sub(start) >= cacheWalkBudget,
	})
	done()
	if !consented || bytes == 0 {
		return
	}
	removed, _ := prune.PurgeCacheByAgeGuarded(cacheRoot, subdirs, nil, cacheAgeDays, true, o.Now(), guard)
	if removed > 0 {
		o.housekeepingNote("cache: reclaimed %s older than %d days, as agreed",
			prune.FmtBytes(removed), int(cacheAgeDays))
	}
}

// cacheAgeDays is §5.3's unchanged 30-day rule.
const cacheAgeDays = 30

// cacheWalkBudget is §5.3's 60 s: past it, the figure is reported as partial
// rather than as a total that happens to be short.
const cacheWalkBudget = 60 * time.Second

func fmtCachePurgeDetail(files int) string {
	if files == 1 {
		return "1 file"
	}
	return strconv.Itoa(files) + " files"
}

// measureAndPurgeMiseVersions is the second offered class's slot half
// (minimal-disk-footprint.md OQ-DF4, ruled 2026-10-05): it judges the shared mise
// tool store against every jail's use record (internal/miseuse), stamps what it
// found for the next launch to offer on, and removes the versions no jail has used
// for 30 days only if this launch has consent — the cache class's shape, reused.
//
// HOST-ONLY, AND LINUX-ONLY: a jail cannot see the host's running jails, so it can
// never tell a running jail that has not recorded its use from no jail at all, and
// a Mac's jails do not use the state dir's store. And it honors
// the automatic reapers' opt-out, which the integration suite sets: its launches
// share this machine's build dir, where this class's measurement and stamp live,
// with a tool store of their own.
//
// A PASS THAT CANNOT JUDGE records nothing to offer: a decline (the records
// cannot answer) leaves the debounce unstamped so the next launch asks again, and
// a pass that is still waiting for the record to cover a whole window is a
// complete pass with nothing in it. Either way, on consent, it still FINISHES what
// an interrupted removal left (DF-D8): that needs no judgement, and a pass with no
// new candidate finishes it too, so a failed delete never waits for one.
func (o *Options) measureAndPurgeMiseVersions(rt string, consent reclaimConsent, guard prune.Guard) {
	if o.inJail() {
		return
	}
	// A Mac's jails keep the store in a volume inside the container VM (miseStoreVolume), or in
	// the sandbox account on macos-user: the state dir's mise/ here is not the store they use.
	if o.IsMacOS {
		return
	}
	if o.Getenv(autoReapOptOutEnv) != "" {
		return
	}
	due, done := o.classDebounce("mise-versions")
	if !due && !consent.freshFor(miseVersionsClass) {
		return
	}
	var live runtime.LiveSet
	if rt != "" {
		live = prune.LiveYoloContainers(rt, o.pruneRunFunc())
	}
	sweep := prune.FindUnusedMiseVersions(paths.GlobalMise(), live, o.Now(), cacheWalkBudget)
	nothing := offerMeasurement{When: o.Now()}
	switch {
	case sweep.Declined != "":
		nothing.Detail = "declined: " + sweep.Declined
		RecordOfferMeasurement(miseVersionsClass, nothing)
		o.housekeepingNote("tool versions: declined — %s", sweep.Declined)
		// not stamped: the next launch retries
	case sweep.Waiting != "":
		nothing.Detail = sweep.Waiting
		RecordOfferMeasurement(miseVersionsClass, nothing)
		done()
	default:
		RecordOfferMeasurement(miseVersionsClass, offerMeasurement{
			Bytes:   sweep.Bytes + sweep.LeftoverBytes,
			Detail:  fmtMiseVersionsDetail(sweep.Candidates, sweep.Leftovers()),
			When:    o.Now(),
			Partial: sweep.Partial,
		})
		done()
	}
	if !consent.has(miseVersionsClass) || (len(sweep.Candidates) == 0 && sweep.Leftovers() == 0) {
		return
	}
	sweep = prune.PruneUnusedMiseVersionsGuarded(sweep, o.Now(), guard)
	if len(sweep.Removed) > 0 {
		o.housekeepingNote("tool versions: reclaimed %s in %d version(s) no jail used for 30 days, as agreed",
			prune.FmtBytes(sweep.RemovedBytes), len(sweep.Removed))
	}
	for _, v := range sweep.Failed {
		o.housekeepingNote("tool versions: could not remove %s: %s", v.Display(), v.Err)
	}
}

// fmtMiseVersionsDetail names the largest few versions an offer covers, so a user
// deciding sees which tools a yes would make them download again, and says when it
// also covers what interrupted removals left.
func fmtMiseVersionsDetail(cands []prune.MiseVersion, leftovers int) string {
	const named = 3
	parts := make([]string, 0, named+1)
	for i, v := range cands {
		if i == named {
			parts = append(parts, fmt.Sprintf("+%d more", len(cands)-named))
			break
		}
		parts = append(parts, v.Display())
	}
	noun := "versions"
	if len(cands) == 1 {
		noun = "version"
	}
	detail := fmt.Sprintf("%d %s", len(cands), noun)
	if len(parts) > 0 {
		detail += ": " + strings.Join(parts, ", ")
	}
	if leftovers > 0 {
		detail += fmt.Sprintf(", and %d interrupted removal(s) to finish", leftovers)
	}
	return detail
}

// lockHousekeepingFn is the load path's half of OQ-BF5's lock: it hands
// image.AutoLoadImage a way to take the SAME machine-wide lock the slot takes,
// so an inspect-and-record can never interleave with a reap.
//
// It BLOCKS where the slot skips, and the asymmetry is the point. The slot is
// best-effort work on a debounce — if someone else holds the lock, the work is
// already happening and skipping costs nothing. A launch cannot skip: it needs
// the window closed or its `podman run` may fail on an image removed underneath
// it. The wait is bounded by ONE DELETION of another launch's pass, which holds the
// lock around each deletion and lets go between them (OQ-PR2 of
// docs/design/podman-reboot-readiness.md). It used to be the whole pass —
// housekeeping.slot measured 62 s and 116 s on the maintainer's host, and 16.1 s at
// the 2026-09-29 reboot — and a pass from a yolo older than that rule still holds
// it that long. So a launch that has to wait SAYS so, and shows for how long, rather
// than sitting silent between the nix build and "Image load needed".
//
// nil in-jail: nothing inside a jail reaps the host's images, so there is
// nothing to serialise against and the lock file is not even on a shared
// filesystem there.
func (o *Options) lockHousekeepingFn() func() func() {
	if o.inJail() {
		return nil
	}
	return func() func() {
		path := HousekeepingLockPath()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return func() {}
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return func() {}
		}
		fd := int(f.Fd())
		if ferr := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); ferr != nil {
			if !errors.Is(ferr, syscall.EWOULDBLOCK) && !errors.Is(ferr, syscall.EAGAIN) {
				_ = f.Close()
				return func() {}
			}
			o.pr(o.Stderr).printf("[dim]Waiting for another launch's housekeeping pass "+
				"to finish (lock %s)...[/dim]", filepath.Base(path))
			waited := o.withStderrProgress("Waiting for the housekeeping lock", func() bool {
				return syscall.Flock(fd, syscall.LOCK_EX) == nil
			})
			if !waited {
				_ = f.Close()
				return func() {}
			}
		}
		return func() {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			_ = f.Close()
		}
	}
}

// classDebounce is §5.3's "at most once per 24 h PER STORE CLASS", generalised
// from the image reap's single `last-image-reap` stamp exactly as the design
// says it should be: one stamp per class under BuildDir().
//
// WITHOUT THIS THE SLOT IS A PER-LAUNCH WALK. The cache class alone was measured
// at 369k files; running it on every launch is the cost §5.3's budget and
// debounce exist to bound, and it would show up as a launch that got slower for
// no visible reason.
//
// STAMP ON COMPLETION, never before: the slot's goroutine dies when the
// terminate arm calls os.Exit, so a pass can be cut mid-flight. A stamp written
// first turns one interrupted pass into a day of not running.
func (o *Options) classDebounce(class string) (due bool, done func()) {
	stamp := filepath.Join(paths.BuildDir(), "last-"+class+"-reap")
	if !prune.DueForAutoImageReap(stamp, prune.AutoReapInterval, o.Now()) {
		return false, func() {}
	}
	return true, func() {
		// NOR WHILE A SIGNAL ENDS THE PROCESS. The arm stops every nix and refuses any started
		// after, so a pass finishing in its teardown had each `nix store delete` refused and did
		// nothing; it ran to its end, but it did not complete
		// (TestAPassASignalCutsShortLeavesItsClassDue).
		if _, ending := signalEndingTheProcess(); ending {
			return
		}
		// MkdirAll first: prune.RecordAutoImageReap discards its write error on
		// purpose (a failed stamp only means the next launch retries), which is
		// the safe direction but silently NEVER debounces on a machine whose
		// build dir does not exist yet — caught by
		// TestEachClassDebouncesSeparately on a fresh HOME.
		_ = os.MkdirAll(filepath.Dir(stamp), 0o755)
		prune.RecordAutoImageReap(stamp, o.Now())
	}
}

// reapSupersededStoreOutputs is OQ-BF3 on the launch path: delete yolo's own
// unrooted install-prefix and Go-build outputs by name.
//
// AUTOMATIC ONLY BECAUSE OQ-BF4 LANDED. The ruling made this conditional on
// every running jail's prefix having a durable root, and the reason is exact:
// before that, "unrooted" did not mean "unused", it meant "we have not been
// recording" — and deleting on that basis takes pid1's binary out from under a
// live jail. It reads the roots BF4 writes.
//
// Host-only. In-jail /nix/store is a read-only bind of the host's and the
// gcroots dir is unmounted, so a jail cannot tell rooted from unrooted and must
// not guess; the same refusal RunNixStoreGC already has.
func (o *Options) reapSupersededStoreOutputs(rt string, guard prune.Guard) {
	if o.inJail() {
		return
	}
	if o.Getenv(autoReapOptOutEnv) != "" {
		return
	}
	due, done := o.classDebounce("store-outputs")
	if !due {
		return
	}
	buildDir := paths.BuildDir()
	rootDirs := []string{
		filepath.Join(buildDir, "roots"),
		filepath.Join(buildDir, "prefix-roots"),
	}
	run := o.pruneRunFunc()
	// ASK THE RUNTIME FIRST, and decline entirely if it cannot answer. A prefix a
	// live jail is executing from is not superseded, whether or not anything
	// rooted it — see SupersededStoreOutputs' inUse guard for the upgrade window
	// this closes. "No running jails" and "I cannot tell" must not be the same
	// answer when the action is deleting a store path.
	live := prune.LiveYoloContainers(rt, run)
	inUseSources, srcKnown, _ := prune.LivePrefixSources(rt, live, prune.PrefixBinMountDest, run)
	if !srcKnown {
		return // not stamped: the next launch retries
	}
	inUse := map[string]bool{}
	for src := range inUseSources {
		if sp := prune.PrefixStorePathOf(src); sp != "" {
			inUse[sp] = true
		}
	}
	candidates := prune.SupersededStoreOutputs("/nix/store", rootDirs, inUse, prune.StoreOutputGrace, o.Now())
	if len(candidates) == 0 {
		done()
		return
	}
	removed := prune.DeleteSupersededStoreOutputsGuarded(candidates, true, run, guard, rootDirs)
	done()
	if len(removed) > 0 {
		o.housekeepingNote("store outputs: reclaimed %d superseded path(s) (OQ-BF3)", len(removed))
	}
}

// reapSmallAutomaticClasses is §5.2's fourth automatic row — agent staging
// orphans and retired loophole state. Kilobytes to hundreds of MB, tri-state
// gated already, and listed in the mapping as "in the slot" for both the first
// pass and the steady state.
//
// SMALL IS WHY THEY ARE HERE, not why they are optional. 443 dirs / 36.5 MiB and
// 6 generations / 1.9 MiB were measured; the reason to reclaim them
// automatically is that they have complete evidence and a trivial regeneration,
// which is P3, and the reason they were never reclaimed is the same one that let
// 404 GiB accrue — nothing ran the reaper.
//
// The captures class is deliberately NOT here: capture.PruneSupersededCaptures
// needs the CaptureRecords reader `yolo prune` constructs, and wiring a second
// copy of that into the launch path would be a second definition of what a
// superseded capture is. It stays a `yolo prune` class until that reader has one
// home.
func (o *Options) reapSmallAutomaticClasses(rt, launchingCname string, guard prune.Guard) {
	if o.inJail() {
		return
	}
	if o.Getenv(autoReapOptOutEnv) != "" {
		return
	}
	due, done := o.classDebounce("small-classes")
	if !due {
		return
	}
	run := o.pruneRunFunc()
	live := prune.LiveYoloContainers(rt, run)
	if !live.Known {
		// Tri-state, unchanged: a staging dir belonging to a jail we cannot see
		// is not an orphan. Not stamped — the next launch retries.
		return
	}
	// THE KNOWN SET IS live ∪ tracked ∪ THIS LAUNCH, and each of the three earned
	// its place by something breaking without it.
	//
	// TRACKED was in PruneOrphanAgentStaging's contract from the start — "a name is
	// an orphan only when it is neither live nor tracked" — and `yolo prune`'s call
	// site honored it while THIS one did not, so the automatic sweep was strictly
	// more destructive than the manual one. A stopped-but-tracked jail lost the
	// briefing its next start would have reused.
	//
	// ⚠ THIS LAUNCH'S OWN NAME, because the sweep ran inside the launch that had
	// just staged into that directory. stagePacks writes its pack tree under
	// AGENTS_DIR/<cname> early (run.go), the container is not created until the very end, and this slot
	// sits between them — so the jail was neither live nor tracked at exactly the
	// moment its own staging was judged an orphan. Measured 2026-09-09 on a real
	// host: `Error: statfs …/agents/yolo-yolo-jail-887995ca/packs: no such file or
	// directory`, after auto-capture held the slot open long enough for the window
	// to matter.
	//
	// ⚠ THE LAUNCHING NAME IS NOT THE WHOLE PROTECTION, and must not be read as it.
	// It only ever covers the sweep that runs INSIDE the launch it belongs to, and the
	// launch that failed was not that: AUTO-CAPTURE runs a full Run of its own per
	// installer program (internal/cli.runCaptureJail), so the sweep that judged
	// 887995ca an orphan was passed the CAPTURE jail's cname, from a launch whose slot
	// legitimately knew nothing about the outer one. The cross-launch half is
	// stagePacks stamping AGENTS_DIR/<cname>'s own mtime (touchAgentStagingDir), which
	// arms the age floor below for every sweeper in every process.
	//
	// The age floor did not save it and could not, BEFORE that stamp existed: it reads
	// AGENTS_DIR/<cname>'s mtime, and staging creates the `packs` CHILD, which on a
	// relaunch leaves the parent's mtime at whatever a previous session left.
	// Directories on that host carried mtimes weeks old with a freshly staged `packs`
	// inside.
	//
	// ⚠ The slot's own header comment claims it "runs late enough that this launch's
	// own container is visible". That is true of the IMAGE and false of the
	// container, which is created after every housekeeping class has run. Do not
	// restore a version of this that relies on it.
	known := prune.TrackedContainerNames(paths.ContainerDir())
	for name := range live.Names {
		known[name] = struct{}{}
	}
	if launchingCname != "" {
		known[launchingCname] = struct{}{}
	}
	// Each removal under the shared lock (guard), rechecking that the dir is still past the
	// age floor and still untracked (prune.PruneOrphanAgentStagingGuarded).
	_, dirs, _ := prune.PruneOrphanAgentStagingGuarded(paths.AgentsDir(), known, live.Known,
		time.Hour, true, o.Now(), guard, paths.ContainerDir())
	_, gens, _ := prune.PruneRetiredLoopholeStateGuarded(filepath.Join(paths.GlobalStorage(), "state"),
		hostArchiveKeepInSlot, true, guard)
	done()
	if dirs+gens > 0 {
		o.housekeepingNote("small classes: reclaimed %d agent staging dir(s), %d loophole state generation(s)",
			dirs, gens)
	}
}

// reapFlakeBundleGenerations collects the staged flake-bundle generations no
// running jail is mounting.
//
// It is the collector the generations design owes: `just install` no longer
// rewrites the bundle in place (which deleted a running jail's binaries — see
// internal/flakebundle), it stages a new generation and swaps a symlink, so the
// old ones accumulate instead. One generation is ~200 MB of binaries.
//
// SAME TRI-STATE AS EVERY OTHER REAP HERE, and for the sharpest version of the
// reason: the thing being deleted is the directory some jail's pid1 is executing
// out of. LivePrefixSources asks the runtime which directory each live container
// mounts at /opt/yolo-jail/bin; if it cannot answer, this declines and does not
// stamp, so the next launch retries.
//
// Host-only. In-jail, the state dir is the jail's own and holds no generations a
// host install staged.
func (o *Options) reapFlakeBundleGenerations(rt string, guard prune.Guard) {
	if o.inJail() {
		return
	}
	if o.Getenv(autoReapOptOutEnv) != "" {
		return
	}
	due, done := o.classDebounce("bundle-generations")
	if !due {
		return
	}
	run := o.pruneRunFunc()
	live := prune.LiveYoloContainers(rt, run)
	sources, known, _ := prune.LivePrefixSources(rt, live, prune.PrefixBinMountDest, run)
	if !known {
		return // not stamped: the next launch retries
	}
	removed := flakebundle.ReapGuarded(paths.FlakeBundleDir(), sources, known, true, o.Now(), guard)
	done()
	if len(removed) > 0 {
		o.housekeepingNote("flake bundle: reclaimed %d superseded generation(s)", len(removed))
	}
}

// hostArchiveKeepInSlot mirrors `yolo prune`'s hostArchiveKeep. Spelled here
// rather than exported from prune because it is that command's flag default, and
// the slot must not silently change what a manual prune keeps.
const hostArchiveKeepInSlot = 3

// reapImageTars is §5.2's second automatic row, and the one I first left out of
// the slot: image tars on a streaming runtime.
//
// It is automatic on the same footing as the rest — evidence complete (the
// runtime streams, so a tar is one-shot), regeneration is a build — and the
// number comes from the runtime, not from here: prune.ResolveImageCacheKeep is
// 0 on podman since OQ-BF6 and 3 on Apple Container, which cannot stream.
//
// ZERO RETAINED IS NOT ZERO READABLE. This bounds what is KEPT; the offline
// fallback still loads whatever tar exists (image.newestTars). A change that
// "finished" this by removing the reader would break the safety net OQ-DF1
// deliberately kept.
func (o *Options) reapImageTars(rt string, guard prune.Guard) {
	if o.Getenv(autoReapOptOutEnv) != "" {
		return
	}
	due, done := o.classDebounce("image-tars")
	if !due {
		return
	}
	keep := prune.ResolveImageCacheKeep(prune.ImageCacheKeepUnset, rt)
	bytes, files := prune.PruneImageCacheGuarded(filepath.Join(paths.GlobalStorage(), "cache", "images"), keep, true, guard)
	// An interrupted archive delivery's directory (image-delivery/) is the same
	// kind of bytes, reclaimed on the same debounce.
	db, dn := prune.PruneImageDeliveryGuarded(paths.ImageDeliveryDir(), true, guard)
	bytes, files = bytes+db, files+dn
	done()
	if files > 0 {
		o.housekeepingNote("image tars: reclaimed %s (this runtime streams, so it keeps %d)",
			prune.FmtBytes(bytes), keep)
	}
}
