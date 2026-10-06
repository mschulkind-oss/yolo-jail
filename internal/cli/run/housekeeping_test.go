package run

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
)

// holdLock takes path's flock on a descriptor of its own, as another process would, and
// returns the release.
func holdLock(t *testing.T, path string) func() {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("could not take %s for the test: %v", path, err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}
}

// lockFree reports whether path's flock can be taken right now, taking and dropping it.
func lockFree(t *testing.T, path string) bool {
	t.Helper()
	return !launchLockHeld(t, path)
}

// TestAPassSkipsWhileAnotherPassRuns pins the choice that makes the slot safe to
// call from every launch: a pass that finds another pass running SKIPS rather than
// waits. Waiting would buy a duplicate pass at the price of a slot, and the thing
// that must not happen — two passes interleaving over machine-wide stores — is
// prevented either way. Since OQ-PR2 the pass has its own lock for this, held for the
// whole pass, so the SHARED lock a launch's re-inspect takes is no longer what a pass
// skips on: a pass that finds only that one held runs, and its deletions wait.
func TestAPassSkipsWhileAnotherPassRuns(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	o := &Options{}
	fillDefaults(o)

	release := holdLock(t, HousekeepingPassLockPath())
	ran := false
	o.withHousekeepingPass(func(prune.Guard) { ran = true })
	if ran {
		t.Fatal("a pass ran while another pass held the pass lock — two passes over podman's " +
			"image store and build/roots is the interleaving OQ-BF5's lock exists to stop")
	}
	release()
	o.withHousekeepingPass(func(prune.Guard) { ran = true })
	if !ran {
		t.Fatal("the slot skipped with the pass lock free — skipping must be about contention, not a default")
	}

	// A launch's re-inspect holding the SHARED lock does not make a pass skip.
	releaseShared := holdLock(t, HousekeepingLockPath())
	defer releaseShared()
	ran = false
	o.withHousekeepingPass(func(prune.Guard) { ran = true })
	if !ran {
		t.Fatal("a pass skipped because a launch held the shared lock: the pass would then never " +
			"run on a busy machine, and its deletions are what wait for that lock now")
	}
}

// TestTwoPassesNeverInterleave is OQ-PR2's proof obligation: with the shared lock let go
// between deletions, a second pass started while the first is between deletions must still
// not run at all.
func TestTwoPassesNeverInterleave(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	first, second := &Options{}, &Options{}
	fillDefaults(first)
	fillDefaults(second)

	between := make(chan struct{})
	finish := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		first.withHousekeepingPass(func(guard prune.Guard) {
			guard.Do(nil, func() {})
			close(between) // one deletion done, the shared lock let go, the pass not over
			<-finish
			guard.Do(nil, func() {})
		})
	}()
	<-between
	if !lockFree(t, HousekeepingLockPath()) {
		t.Fatal("the first pass holds the shared lock between deletions")
	}
	secondRan := false
	second.withHousekeepingPass(func(prune.Guard) { secondRan = true })
	close(finish)
	<-done
	if secondRan {
		t.Fatal("a second pass ran between the first pass's deletions: the two interleaved")
	}
}

// TestAPassHoldsTheSharedLockOnlyAroundEachDeletion: the shared lock — the one a launch's
// image re-inspect blocks on — is held during a deletion and free before, between and
// after, so the re-inspect waits at most one deletion (OQ-PR2).
func TestAPassHoldsTheSharedLockOnlyAroundEachDeletion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	o := &Options{}
	fillDefaults(o)
	shared := HousekeepingLockPath()
	var heldDuring, freeBefore, freeBetween []bool
	o.withHousekeepingPass(func(guard prune.Guard) {
		for i := 0; i < 3; i++ {
			freeBefore = append(freeBefore, lockFree(t, shared))
			guard.Do(func() bool { return true }, func() {
				heldDuring = append(heldDuring, !lockFree(t, shared))
			})
			freeBetween = append(freeBetween, lockFree(t, shared))
		}
	})
	for i := range heldDuring {
		if !heldDuring[i] || !freeBefore[i] || !freeBetween[i] {
			t.Errorf("deletion %d: held during=%v, free before=%v, free after=%v", i+1,
				heldDuring[i], freeBefore[i], freeBetween[i])
		}
	}
	if len(heldDuring) != 3 {
		t.Fatalf("ran %d deletions, want 3", len(heldDuring))
	}
}

// TestADeletionWaitsForALaunchsReinspectAndRechecksAfterIt: a deletion that finds a launch
// holding the shared lock waits for it, then rechecks — so what the launch recorded while it
// held the lock (the load sentinel, in the image class) is what the recheck reads.
func TestADeletionWaitsForALaunchsReinspectAndRechecksAfterIt(t *testing.T) {
	for _, launchRecords := range []bool{true, false} {
		t.Setenv("HOME", t.TempDir())
		o := &Options{}
		fillDefaults(o)
		release := holdLock(t, HousekeepingLockPath())
		var recorded atomicBool
		deleted := make(chan bool, 1)
		go o.withHousekeepingPass(func(guard prune.Guard) {
			ran := false
			guard.Do(func() bool { return !recorded.load() }, func() { ran = true })
			deleted <- ran
		})
		select {
		case <-deleted:
			t.Fatal("the deletion ran while a launch held the shared lock")
		case <-time.After(150 * time.Millisecond):
		}
		if launchRecords {
			recorded.store(true) // the launch records the item under its lock, then lets go
		}
		release()
		select {
		case ran := <-deleted:
			if ran == launchRecords {
				t.Errorf("launch recorded the item=%v, yet the deletion ran=%v: the recheck must "+
					"read what the launch recorded while it held the lock", launchRecords, ran)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the deletion never ran after the launch let go")
		}
	}
}

// atomicBool is a flag two goroutines share.
type atomicBool struct {
	mu sync.Mutex
	v  bool
}

func (b *atomicBool) load() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.v
}

func (b *atomicBool) store(v bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.v = v
}

// TestHousekeepingLockIsMachineWide: the stores it protects are machine-wide, so
// two launches in DIFFERENT workspaces are exactly the collision it exists to
// prevent. A per-workspace path would look correct and prevent nothing.
func TestHousekeepingLockIsMachineWide(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got := HousekeepingLockPath()
	if strings.Contains(got, "workspace") || !strings.HasSuffix(got, housekeepingLockName) {
		t.Fatalf("HousekeepingLockPath() = %q — it must be one path per MACHINE, beside the "+
			"per-workspace locks, not derived from a workspace", got)
	}
}

// TestReapRunsInTheSlotNotBeforeTheContainer is the call-site pin for OQ-BF5's
// move. Both facts matter and neither is visible to a unit test of either half:
// the reap must be reachable from onStarted (or it never runs at all), and it
// must NOT still be called on the pre-container path (or the move bought
// nothing and a backlog still holds the launch).
func TestReapRunsInTheSlotNotBeforeTheContainer(t *testing.T) {
	runSrc, err := os.ReadFile("run.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(runSrc), "\n\to.autoReapOldImages(rt, guard)\n") {
		t.Error("run.go calls autoReapOldImages on the pre-container path again — OQ-BF5 moved it " +
			"into the housekeeping slot so a first pass over a backlog stops holding the launch")
	}
	if !strings.Contains(string(runSrc), "o.runHousekeeping(rt, reclaimConsent, cname)") {
		t.Fatal("run.go no longer runs the housekeeping slot from onStarted with THIS LAUNCH'S " +
			"cname — every automatic class then silently stops running, and without the cname " +
			"the agent-staging sweep reaps the directory this launch just staged into (it is " +
			"neither live nor tracked yet at that point). See " +
			"TestALaunchNeverReapsItsOwnStagingDir.")
	}
	hkSrc, err := os.ReadFile("housekeeping.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(hkSrc), "o.autoReapOldImages(rt, guard)") ||
		!strings.Contains(string(hkSrc), "withHousekeepingPass") {
		t.Fatal("the slot no longer runs the image reap under the machine-wide lock")
	}
	if !strings.Contains(string(hkSrc), "o.measureAndPurgeCache(reclaimConsent, guard)") {
		t.Fatal("the slot no longer measures the cache class — the offered tier reads what the " +
			"LAST slot measured (§5.3 measure late, offer early), so without this the offer " +
			"never has a size and silently never fires")
	}
	if !strings.Contains(string(runSrc), "o.maybeOfferReclaim()") {
		t.Fatal("run.go no longer makes the offer before the container attaches — the offered " +
			"tier stops existing and OQ-BF1's whole disposition is inert")
	}
	if !strings.Contains(string(hkSrc), "o.reapSupersededStoreOutputs(rt, guard)") {
		t.Fatal("the slot no longer reclaims yolo's own superseded store outputs (OQ-BF3) — " +
			"that class has no other collector at all, and was measured accruing 0.43 GB/day")
	}
}

// TestStoreOutputReapIsHostOnly: in-jail /nix/store is a read-only bind of the
// host's and the gcroots dir is unmounted, so a jail cannot tell rooted from
// unrooted. Guessing there deletes a path some host launch is rooting.
func TestStoreOutputReapIsHostOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YOLO_VERSION", "0.0.0-test") // inJail() reads this
	o := &Options{}
	fillDefaults(o)
	called := false
	o.Exec = func([]string, string, []string, time.Duration) ExecResult {
		called = true
		return ExecResult{Ran: true}
	}
	o.reapSupersededStoreOutputs("podman", nil)
	if called {
		t.Fatal("the store-output reap ran inside a jail — it cannot distinguish rooted from " +
			"unrooted there, so it must refuse rather than guess (the same refusal RunNixStoreGC has)")
	}
}

// TestEachClassDebouncesSeparately is §5.3's "at most once per 24 h PER STORE
// CLASS". Two facts, and the second is the one a shared stamp would break: each
// class has its own stamp, so one class running does not silence another.
func TestEachClassDebouncesSeparately(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	o := &Options{}
	fillDefaults(o)
	o.Now = time.Now

	dueA, doneA := o.classDebounce("cache")
	if !dueA {
		t.Fatal("a class with no stamp must be due")
	}
	dueB, _ := o.classDebounce("store-outputs")
	if !dueB {
		t.Fatal("a second class is due independently — a shared stamp would let one class " +
			"running silence every other for a day")
	}
	doneA()
	if again, _ := o.classDebounce("cache"); again {
		t.Error("a completed class must debounce until the interval elapses")
	}
	if other, _ := o.classDebounce("store-outputs"); !other {
		t.Error("completing one class must not debounce another")
	}
}

// TestDebounceStampsOnCompletionNotEntry: the slot's goroutine dies when the
// terminate arm calls os.Exit, so a pass can be cut mid-flight. A stamp written
// on ENTRY turns one interrupted pass into a day of not running — which is the
// original 404 GiB defect's exact shape, at a smaller scale.
func TestDebounceStampsOnCompletionNotEntry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	o := &Options{}
	fillDefaults(o)
	o.Now = time.Now

	due, _ := o.classDebounce("cache") // take the decision, never call done
	if !due {
		t.Fatal("expected due")
	}
	if stillDue, _ := o.classDebounce("cache"); !stillDue {
		t.Fatal("an interrupted pass stamped the debounce — the next launch must retry, not " +
			"wait out a day on work that never happened")
	}
}

// TestSmallClassesKeepTheirTriState: agent staging is liveness-gated, and a
// staging dir belonging to a jail the runtime cannot enumerate is not an orphan.
// Unlike the image ROOTS (OQ-LS1), this class has an authority, so unknown must
// still decline — and must NOT stamp, or one unreachable moment costs a day.
func TestSmallClassesKeepTheirTriState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	o := &Options{}
	fillDefaults(o)
	o.Now = time.Now
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		return ExecResult{Ran: false} // the runtime cannot be enumerated
	}
	o.reapSmallAutomaticClasses("podman", "yolo-test-launching", nil)

	if due, _ := o.classDebounce("small-classes"); !due {
		t.Fatal("a declined pass stamped its debounce — one unreachable runtime would then cost " +
			"a full day of not reclaiming, which is the defect this whole design started from")
	}
}

// TestSlotRunsEveryAutomaticClass pins §5.2's mapping against the slot's body.
// Each row there is a class nothing else reclaims automatically; one dropped
// call is a store that silently stops being swept, with every unit test green.
func TestSlotRunsEveryAutomaticClass(t *testing.T) {
	src, err := os.ReadFile("housekeeping.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range []string{
		"o.autoReapOldImages(rt, guard)",
		"o.reapSupersededStoreOutputs(rt, guard)",
		"o.measureAndPurgeCache(reclaimConsent, guard)",
		"o.measureAndPurgeMiseVersions(rt, reclaimConsent, guard)",
		"o.reapSmallAutomaticClasses(rt, cname, guard)",
		"o.reapImageTars(rt, guard)",
		"o.reapFlakeBundleGenerations(rt, guard)",
	} {
		if !strings.Contains(string(src), call) {
			t.Errorf("the housekeeping slot no longer calls %s — that is a §5.2 automatic-tier "+
				"row with no other collector", call)
		}
	}
}

// TestEveryClassDeletesUnderTheGuard is OQ-PR2's call-site pin: each class hands the slot's
// per-deletion guard to the reaper that deletes, so every deletion in the pass happens under
// the shared lock with its recheck. A class that went back to the unguarded reaper would
// delete with no lock at all now that the pass no longer holds one — every guard unit test
// green. A text match cannot see the arguments a call passes (a nil guard, an empty tracking
// directory), so the classes whose calls span lines are pinned by behavior as well:
// TestTheSmallClassesDeleteThroughTheGuardAndRecheckTracking and
// TestTheImageTarClassDeletesThroughTheGuardAndRechecks.
func TestEveryClassDeletesUnderTheGuard(t *testing.T) {
	src := ""
	for _, f := range []string{"housekeeping.go", "autoreapimages.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src += string(b)
	}
	for _, call := range []string{
		"prune.AutoReapOldImagesGuarded(rt, buildDir, o.Now(), run, guard)",
		"prune.DeleteSupersededStoreOutputsGuarded(candidates, true, run, guard, rootDirs)",
		"prune.PurgeCacheByAgeGuarded(cacheRoot, subdirs, nil, cacheAgeDays, true, o.Now(), guard)",
		"prune.PruneUnusedMiseVersionsGuarded(sweep, o.Now(), guard)",
		"prune.PruneOrphanAgentStagingGuarded(",
		"prune.PruneRetiredLoopholeStateGuarded(",
		"prune.PruneImageCacheGuarded(",
		"prune.PruneImageDeliveryGuarded(paths.ImageDeliveryDir(), true, guard)",
		"flakebundle.ReapGuarded(paths.FlakeBundleDir(), sources, known, true, o.Now(), guard)",
		"o.withHousekeepingPass(func(guard prune.Guard) {",
	} {
		if !strings.Contains(src, call) {
			t.Errorf("the slot no longer calls %s — a class deleting outside the per-deletion "+
				"lock, or a slot that no longer runs as one pass", call)
		}
	}
	for _, unguarded := range []string{"prune.AutoReapOldImages(", "prune.DeleteSupersededStoreOutputs(",
		"prune.PruneOrphanAgentStaging(", "prune.PruneRetiredLoopholeState(", "prune.PruneImageCache(",
		"prune.PruneImageDelivery(", "flakebundle.Reap(", "prune.PruneUnusedMiseVersions("} {
		if strings.Contains(src, unguarded) {
			t.Errorf("the slot calls the unguarded %s", unguarded)
		}
	}
}

// TestBundleGenerationReapAsksWhatIsRunning is the same call-site pin for the
// bundle generations. internal/flakebundle proves Reap declines when liveness is
// unknown; nothing there notices the launch path deciding it knows anyway, and
// the regression deletes the directory some jail's pid1 is executing out of.
func TestBundleGenerationReapAsksWhatIsRunning(t *testing.T) {
	src, err := os.ReadFile("housekeeping.go")
	if err != nil {
		t.Fatal(err)
	}
	fn := afterFunc(string(src), "func (o *Options) reapFlakeBundleGenerations(")
	for _, want := range []string{"LivePrefixSources(", "if !known {", "flakebundle.ReapGuarded("} {
		if !strings.Contains(fn, want) {
			t.Fatalf("the bundle-generation pass no longer calls %s. Generations exist so a "+
				"`just install` cannot delete a running jail's binaries; a reap that does not "+
				"ask the runtime what is mounted puts that failure straight back.", want)
		}
	}
}

// afterFunc returns the source from a function's declaration to the next
// top-level declaration, so a pin reads the function it names rather than the
// whole file (where another pass's LivePrefixSources call would satisfy it).
func afterFunc(src, decl string) string {
	i := strings.Index(src, decl)
	if i < 0 {
		return ""
	}
	rest := src[i+len(decl):]
	if j := strings.Index(rest, "\nfunc "); j >= 0 {
		return rest[:j]
	}
	return rest
}

// TestStoreOutputReapAsksWhatIsRunning is the call-site pin for the upgrade
// window. The unit test in internal/prune proves the guard works; nothing there
// would notice the launch path stopping passing it, and the regression deletes
// a running jail's binaries on the first pass after an upgrade.
func TestStoreOutputReapAsksWhatIsRunning(t *testing.T) {
	src, err := os.ReadFile("housekeeping.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"LivePrefixSources(", "PrefixStorePathOf(", "if !srcKnown {"} {
		if !strings.Contains(string(src), want) {
			t.Fatalf("the store-output pass no longer calls %s — it would then select a prefix a "+
				"live jail is executing from, whenever that jail launched before OQ-BF4 shipped. "+
				"That is every already-running jail on the first pass after an upgrade.", want)
		}
	}
}

// TestTheImageTarSlotAlsoReapsInterruptedDeliveries pins the launch-path call
// site of prune.PruneImageDelivery: an archive delivery killed mid-copy leaves a
// whole image layout under the state dir's image-delivery/, out of the cache the
// tar sweep reads, and the automatic slot is the only collector that runs without
// a human typing `yolo prune`.
func TestTheImageTarSlotAlsoReapsInterruptedDeliveries(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	o := &Options{}
	fillDefaults(o)
	o.Now = time.Now
	o.Workspace = t.TempDir()
	o.Getenv = func(string) string { return "" }
	stale := filepath.Join(paths.ImageDeliveryDir(), "k-1"+image.DeliveryWorkSuffix)
	if err := os.MkdirAll(filepath.Join(stale, "layout"), 0o700); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-48 * time.Hour)
	for _, p := range []string{filepath.Join(stale, "layout"), stale} {
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatal(err)
		}
	}
	o.reapImageTars("podman", nil)
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("the housekeeping slot left an interrupted image delivery in place")
	}
}

// recordingGuard is a prune.Guard that brackets each deletion with no lock at all and counts
// it, running before(n) ahead of deletion n's recheck: a stand-in for what a launch changes
// while that deletion waits for the shared lock.
type recordingGuard struct {
	calls  int
	before func(n int)
}

func (g *recordingGuard) guard() prune.Guard {
	return func(recheck func() bool, del func()) bool {
		g.calls++
		if g.before != nil {
			g.before(g.calls)
		}
		if recheck != nil && !recheck() {
			return false
		}
		del()
		return true
	}
}

// ageDirs sets each path's mtime to at.
func ageDirs(t *testing.T, at time.Time, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.Chtimes(d, at, at); err != nil {
			t.Fatal(err)
		}
	}
}

// OQ-PR2's wiring, by BEHAVIOR rather than by source text: the small classes hand the slot's
// guard to the reapers that delete, so every agent-staging and retired-loophole deletion goes
// through it, and the staging class passes the tracking directory its recheck reads. A launch
// that starts tracking an orphan while its deletion waits for the lock keeps it. A nil guard
// (every deletion unbracketed) or an empty tracking directory (the recheck's tracking half
// off) fails here.
func TestTheSmallClassesDeleteThroughTheGuardAndRecheckTracking(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	o := &Options{}
	fillDefaults(o)
	o.Now = time.Now
	o.Workspace = t.TempDir()
	o.Getenv = func(string) string { return "" }
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 2 && argv[1] == "ps" && argv[2] == "-a" {
			return ExecResult{Ran: true, RC: 0} // podman answers: no containers at all
		}
		return ExecResult{Ran: false}
	}
	old := time.Now().Add(-2 * time.Hour)
	orphanA := filepath.Join(paths.AgentsDir(), "yolo-orphan-a")
	orphanB := filepath.Join(paths.AgentsDir(), "yolo-orphan-b")
	archive := filepath.Join(paths.GlobalStorage(), "state", prune.RetiredLoopholeStateDir)
	var gens []string
	for _, stamp := range []string{"20260901-000000", "20260902-000000", "20260903-000000",
		"20260904-000000", "20260905-000000"} {
		gens = append(gens, filepath.Join(archive, stamp))
	}
	for _, d := range append([]string{orphanA, orphanB}, gens...) {
		if err := os.MkdirAll(filepath.Join(d, "x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ageDirs(t, old, orphanA, orphanB)
	g := &recordingGuard{before: func(n int) {
		if n == 1 { // the first deletion, yolo-orphan-a's (the listing is sorted): a launch tracks it meanwhile
			if err := os.MkdirAll(paths.ContainerDir(), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(paths.ContainerDir(), "yolo-orphan-a"), []byte("{}"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}}
	o.reapSmallAutomaticClasses("podman", "yolo-launching", g.guard())

	if g.calls != 4 {
		t.Errorf("%d deletions went through the guard, want 4 (two staging dirs, two generations)", g.calls)
	}
	if _, err := os.Stat(orphanA); err != nil {
		t.Errorf("a staging dir a launch started tracking while its deletion waited was removed: the "+
			"recheck did not read the tracking directory (%v)", err)
	}
	if _, err := os.Stat(orphanB); !os.IsNotExist(err) {
		t.Errorf("the untracked orphan was kept (%v)", err)
	}
	for i, gen := range gens {
		_, err := os.Stat(gen)
		if gone := os.IsNotExist(err); gone != (i < 2) {
			t.Errorf("generation %s: removed=%v, want the two oldest removed and the newest %d kept",
				filepath.Base(gen), gone, hostArchiveKeepInSlot)
		}
	}
}

// The image-tar class, the same way: each tar and each interrupted delivery is deleted
// through the guard, and a tar a launch rewrote while its deletion waited is kept.
func TestTheImageTarClassDeletesThroughTheGuardAndRechecks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	o := &Options{}
	fillDefaults(o)
	o.Now = time.Now
	o.Workspace = t.TempDir()
	o.Getenv = func(string) string { return "" }
	images := filepath.Join(paths.GlobalStorage(), "cache", "images")
	if err := os.MkdirAll(images, 0o755); err != nil {
		t.Fatal(err)
	}
	older, newer := filepath.Join(images, "older.tar"), filepath.Join(images, "newer.tar")
	for _, p := range []string{older, newer} {
		if err := os.WriteFile(p, []byte("tar"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ageDirs(t, time.Now().Add(-3*time.Hour), older)
	ageDirs(t, time.Now().Add(-2*time.Hour), newer)
	delivery := filepath.Join(paths.ImageDeliveryDir(), "k-1"+image.DeliveryWorkSuffix)
	if err := os.MkdirAll(filepath.Join(delivery, "layout"), 0o700); err != nil {
		t.Fatal(err)
	}
	ageDirs(t, time.Now().Add(-48*time.Hour), filepath.Join(delivery, "layout"), delivery)
	g := &recordingGuard{before: func(n int) {
		if n == 1 { // newest first: a launch rewrites newer.tar while its deletion waits
			now := time.Now()
			_ = os.Chtimes(newer, now, now)
		}
	}}
	o.reapImageTars("podman", g.guard())

	if g.calls != 3 {
		t.Errorf("%d deletions went through the guard, want 3 (two tars, one delivery)", g.calls)
	}
	if _, err := os.Stat(newer); err != nil {
		t.Errorf("a tar rewritten while its deletion waited was removed (%v)", err)
	}
	for _, p := range []string{older, delivery} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s was kept (%v)", filepath.Base(p), err)
		}
	}
}
