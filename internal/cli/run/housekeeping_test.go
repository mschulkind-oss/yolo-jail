package run

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestHousekeepingLockSkipsWhenHeld pins the choice that makes the slot safe to
// call from every launch: a pass whose lock is already held SKIPS rather than
// waits. Waiting would buy a duplicate pass at the price of blocking a launch,
// and the thing that must not happen — two passes interleaving over machine-wide
// stores — is prevented either way.
func TestHousekeepingLockSkipsWhenHeld(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := HousekeepingLockPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	held, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("could not take the lock for the test: %v", err)
	}

	o := &Options{}
	fillDefaults(o)
	ran := false
	o.withHousekeepingLock(func() { ran = true })
	if ran {
		t.Fatal("the slot ran while another holder had the machine-wide lock — two passes over " +
			"podman's image store and build/roots is the interleaving OQ-BF5's lock exists to stop")
	}

	// Released: the very next call runs.
	_ = syscall.Flock(int(held.Fd()), syscall.LOCK_UN)
	o.withHousekeepingLock(func() { ran = true })
	if !ran {
		t.Fatal("the slot skipped with the lock free — skipping must be about contention, not a default")
	}
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
	if strings.Contains(string(runSrc), "\n\to.autoReapOldImages(rt)\n") {
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
	if !strings.Contains(string(hkSrc), "o.autoReapOldImages(rt)") ||
		!strings.Contains(string(hkSrc), "withHousekeepingLock") {
		t.Fatal("the slot no longer runs the image reap under the machine-wide lock")
	}
	if !strings.Contains(string(hkSrc), "o.measureAndPurgeCache(reclaimConsent)") {
		t.Fatal("the slot no longer measures the cache class — the offered tier reads what the " +
			"LAST slot measured (§5.3 measure late, offer early), so without this the offer " +
			"never has a size and silently never fires")
	}
	if !strings.Contains(string(runSrc), "o.maybeOfferReclaim()") {
		t.Fatal("run.go no longer makes the offer before the container attaches — the offered " +
			"tier stops existing and OQ-BF1's whole disposition is inert")
	}
	if !strings.Contains(string(hkSrc), "o.reapSupersededStoreOutputs(rt)") {
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
	o.reapSupersededStoreOutputs("podman")
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
	o.reapSmallAutomaticClasses("podman", "yolo-test-launching")

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
		"o.autoReapOldImages(rt)",
		"o.reapSupersededStoreOutputs(rt)",
		"o.measureAndPurgeCache(reclaimConsent)",
		"o.reapSmallAutomaticClasses(rt, cname)",
		"o.reapImageTars(rt)",
		"o.reapFlakeBundleGenerations(rt)",
	} {
		if !strings.Contains(string(src), call) {
			t.Errorf("the housekeeping slot no longer calls %s — that is a §5.2 automatic-tier "+
				"row with no other collector", call)
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
	for _, want := range []string{"LivePrefixSources(", "if !known {", "flakebundle.Reap("} {
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
