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
	if !strings.Contains(string(runSrc), "o.runHousekeeping(rt, reclaimConsent)") {
		t.Fatal("run.go no longer runs the housekeeping slot from onStarted — every automatic " +
			"class then silently stops running")
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
	if !strings.Contains(string(hkSrc), "o.reapSupersededStoreOutputs()") {
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
	o.reapSupersededStoreOutputs()
	if called {
		t.Fatal("the store-output reap ran inside a jail — it cannot distinguish rooted from " +
			"unrooted there, so it must refuse rather than guess (the same refusal RunNixStoreGC has)")
	}
}
