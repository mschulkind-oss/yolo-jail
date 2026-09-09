package run

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
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
	dir := paths.WorkspaceStateDir(o.Workspace)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "housekeeping.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "%s  %s\n", o.Now().UTC().Format(time.RFC3339), fmt.Sprintf(format, args...))
}

// housekeepingLockName is the machine-wide lock every housekeeping pass and
// every load-and-record step takes.
//
// MACHINE-WIDE, not per workspace: the stores it protects (podman's image store,
// build/roots, the caches) are machine-wide, so two launches in different
// workspaces are exactly the collision it exists to prevent.
const housekeepingLockName = "housekeeping.lock"

// HousekeepingLockPath is that lock's path, beside the per-workspace locks.
func HousekeepingLockPath() string {
	return filepath.Join(paths.GlobalStorage(), "locks", housekeepingLockName)
}

// withHousekeepingLock runs fn while holding the machine-wide housekeeping lock,
// and SKIPS fn entirely if the lock is already held.
//
// SKIP RATHER THAN WAIT, deliberately. Every caller is best-effort housekeeping
// on a debounce; another launch holding the lock means the work is already being
// done, so waiting would buy a duplicate pass at the price of blocking a launch.
// The one thing that must not happen is two passes interleaving, and skipping
// prevents that as completely as waiting does.
//
// THE RACE THIS CLOSES is narrower than "two reapers at once", because `rmi` is
// no longer forced (feddc5e0) and fails on an image with a container. What is
// left is the window between another launch's image inspect and its
// AddLoadedPath: B decides its image is present, A's reap sees no container on
// it yet and no sentinel entry, and removes it — then B's `podman run` fails on
// an image that existed a moment ago. So the LOAD side takes the same lock
// around its re-inspect-and-record, and only that: holding it across the stream
// itself would serialise every launch's image load on the machine.
func (o *Options) withHousekeepingLock(fn func()) {
	path := HousekeepingLockPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	if ferr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); ferr != nil {
		_ = f.Close()
		return // held elsewhere: the other holder is doing this work
	}
	defer func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}()
	fn()
}

// runHousekeeping is the slot's body: every automatic class, in one place, under
// one lock, after the container is up.
//
// Ordering inside it is not load-bearing today — each class is independent and
// debounced on its own stamp. What IS load-bearing is that this runs AFTER the
// container is visible (so a reap can never race this launch's own image) and
// that nothing here can fail the launch.
func (o *Options) runHousekeeping(rt string, reclaimConsent bool) {
	sp := o.Perf.Span("housekeeping.slot")
	defer sp.End()
	o.withHousekeepingLock(func() {
		o.autoReapOldImages(rt)
		o.reapSupersededStoreOutputs(rt)
		o.measureAndPurgeCache(reclaimConsent)
		o.reapSmallAutomaticClasses(rt)
		o.reapImageTars(rt)
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
func (o *Options) measureAndPurgeCache(consented bool) {
	if o.inJail() {
		return // the host cache is the host's to sweep
	}
	// The walk is the expensive thing in this whole design, so it is the one
	// most in need of the per-class debounce. A consented purge is NOT exempt:
	// there is nothing new to reclaim an hour after the last one.
	due, done := o.classDebounce("cache")
	if !due {
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
	removed, _ := prune.PurgeCacheByAge(cacheRoot, subdirs, nil, cacheAgeDays, true, o.Now())
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

// lockHousekeepingFn is the load path's half of OQ-BF5's lock: it hands
// image.AutoLoadImage a way to take the SAME machine-wide lock the slot takes,
// so an inspect-and-record can never interleave with a reap.
//
// It BLOCKS where the slot skips, and the asymmetry is the point. The slot is
// best-effort work on a debounce — if someone else holds the lock, the work is
// already happening and skipping costs nothing. A launch cannot skip: it needs
// the window closed or its `podman run` may fail on an image removed underneath
// it. The wait is bounded by the other holder's pass, which is housekeeping and
// short.
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
		if ferr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); ferr != nil {
			_ = f.Close()
			return func() {}
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
func (o *Options) reapSupersededStoreOutputs(rt string) {
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
	run := func(argv []string, timeout time.Duration) prune.ProbeResult {
		res := o.Exec(argv, "", nil, timeout)
		return prune.ProbeResult{Stdout: res.Stdout, RC: res.RC, Ran: res.Ran && !res.Timeout}
	}
	// ASK THE RUNTIME FIRST, and decline entirely if it cannot answer. A prefix a
	// live jail is executing from is not superseded, whether or not anything
	// rooted it — see SupersededStoreOutputs' inUse guard for the upgrade window
	// this closes. "No running jails" and "I cannot tell" must not be the same
	// answer when the action is deleting a store path.
	live := prune.LiveYoloContainers(rt, run)
	inUseSources, srcKnown := prune.LivePrefixSources(rt, live, prune.PrefixBinMountDest, run)
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
	removed := prune.DeleteSupersededStoreOutputs(candidates, true, run)
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
func (o *Options) reapSmallAutomaticClasses(rt string) {
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
	run := func(argv []string, timeout time.Duration) prune.ProbeResult {
		res := o.Exec(argv, "", nil, timeout)
		return prune.ProbeResult{Stdout: res.Stdout, RC: res.RC, Ran: res.Ran && !res.Timeout}
	}
	live := prune.LiveYoloContainers(rt, run)
	if !live.Known {
		// Tri-state, unchanged: a staging dir belonging to a jail we cannot see
		// is not an orphan. Not stamped — the next launch retries.
		return
	}
	known := map[string]struct{}{}
	for name := range live.Names {
		known[name] = struct{}{}
	}
	_, dirs, _ := prune.PruneOrphanAgentStaging(paths.AgentsDir(), known, live.Known,
		time.Hour, true, o.Now())
	_, gens, _ := prune.PruneRetiredLoopholeState(filepath.Join(paths.GlobalStorage(), "state"),
		hostArchiveKeepInSlot, true)
	done()
	if dirs+gens > 0 {
		o.housekeepingNote("small classes: reclaimed %d agent staging dir(s), %d loophole state generation(s)",
			dirs, gens)
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
func (o *Options) reapImageTars(rt string) {
	if o.Getenv(autoReapOptOutEnv) != "" {
		return
	}
	due, done := o.classDebounce("image-tars")
	if !due {
		return
	}
	keep := prune.ResolveImageCacheKeep(prune.ImageCacheKeepUnset, rt)
	bytes, files := prune.PruneImageCache(filepath.Join(paths.GlobalStorage(), "cache", "images"), keep, true)
	done()
	if files > 0 {
		o.housekeepingNote("image tars: reclaimed %s (this runtime streams, so it keeps %d)",
			prune.FmtBytes(bytes), keep)
	}
}
