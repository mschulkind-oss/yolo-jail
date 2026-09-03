package cli

// hostapplylock_test.go pins §4.6: one writer per host home, and a launch that cannot take the
// lock EXECS rather than waiting or refusing.
//
// The interesting assertion is the second one. Idempotence makes two concurrent applies
// converge on the same CONTENT and says nothing about the writes: two processes
// read-modify-writing one settings.json interleave at the file, and the later write discards
// what the earlier one added. So the lock is the mechanism, and "what happens when you cannot
// have it" is the behaviour a test has to hold — because the wrong answer there (wait, or
// refuse) breaks starting two agents at once, which is the ordinary case the lock exists for.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestHostApplyLockIsKeyedByTheHome is §4.6's "keyed by the resolved home", asserted where it
// can actually go wrong: the lock lives in the home's own state dir, so two homes cannot name
// one file and the same home always names the same one.
func TestHostApplyLockIsKeyedByTheHome(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	if hostApplyLockPath(a) == hostApplyLockPath(b) {
		t.Errorf("two homes share one lock file (%s) — an apply into one would serialise "+
			"against an apply into the other", hostApplyLockPath(a))
	}
	// Deterministic for one home: nothing in the path may come from time, a PID or a random
	// suffix, or two launches into the same home would take two different locks and neither
	// would exclude the other. Compared through a variable rather than by calling twice inline,
	// which staticcheck reads as a tautology (SA4000) and which would not survive a rewrite.
	same := hostApplyLockPath(a)
	if hostApplyLockPath(a) != same {
		t.Error("the same home resolved to two lock files")
	}
	// Under the state dir, in locks/, beside the per-workspace jail locks — the convention
	// internal/cli/run/run.go establishes so everything serialising on a file is in one place.
	if !strings.HasPrefix(hostApplyLockPath(a), filepath.Join(a, ".local", "share", "yolo-jail")) {
		t.Errorf("the lock is not under the home's state dir: %s", hostApplyLockPath(a))
	}
	if filepath.Base(filepath.Dir(hostApplyLockPath(a))) != "locks" {
		t.Errorf("the lock is not in locks/: %s", hostApplyLockPath(a))
	}
}

// TestHostApplyLockExcludesASecondHolder is the mechanism: the second acquire of the same
// home's lock fails while the first is held, and succeeds once it is released.
//
// Two acquires in ONE process are a real test of this, not a shortcut: flock is per open file
// DESCRIPTION, so two independent os.OpenFile handles contend exactly as two processes do.
func TestHostApplyLockExcludesASecondHolder(t *testing.T) {
	home := t.TempDir()
	first := tryHostApplyLock(home)
	if first == nil {
		t.Fatal("could not take an uncontended lock")
	}
	if second := tryHostApplyLock(home); second != nil {
		second.Close()
		first.Close()
		t.Fatal("two holders of one home's apply lock — idempotence does not make two " +
			"read-modify-writers of one file safe (R4)")
	}
	// A DIFFERENT home is not blocked by it.
	if other := tryHostApplyLock(t.TempDir()); other == nil {
		t.Error("a lock on one home blocked another home")
	} else {
		other.Close()
	}
	first.Close()
	again := tryHostApplyLock(home)
	if again == nil {
		t.Fatal("the lock was not released by Close")
	}
	again.Close()
	// Close is idempotent — the gate defers it on paths where it may already have run.
	again.Close()
}

// TestHostApplyGateExecsWhenTheLockIsHeld is the behaviour §4.6 rules on, and the one a wrong
// answer breaks: a launch that cannot take the lock treats it as cannot-determine and EXECS.
//
// Not waiting: the holder may be sitting at a [y/N] prompt, so waiting is an unbounded pause
// on someone else's terminal. Not refusing: starting two agents at once is the ordinary case
// this lock exists for, and failing both of them would be a worse outcome than the race.
//
// The home is deliberately left UNAPPLIED and the environment has no TTY and no approval, so
// every other row of §4.3's table would refuse here. Only the lock makes it exec.
func TestHostApplyGateExecsWhenTheLockIsHeld(t *testing.T) {
	gateFixture(t, true)
	// Stand in for the other process. Contention is reported by the syscall, so intercepting
	// it is how a single-process test reaches the path — and it holds whatever real contention
	// would produce, since tryHostApplyLock treats every failure as the same answer.
	prev := hostApplyFlock
	hostApplyFlock = func(fd, how int) error { return syscall.EWOULDBLOCK }
	t.Cleanup(func() { hostApplyFlock = prev })

	var errw bytes.Buffer
	if !hostApplyGate(&errw, nil, "claude") {
		t.Fatalf("a launch that cannot take the lock must EXEC, not refuse and not wait:\n%s",
			errw.String())
	}
	report := errw.String()
	if !strings.Contains(report, "another `yolo host apply` is running") {
		t.Errorf("the launch must say why it did not check:\n%s", report)
	}
	if n := strings.Count(strings.TrimRight(report, "\n"), "\n"); n != 0 {
		t.Errorf("cannot-determine is at most one line to stderr (§4.4); got %d:\n%s", n+1, report)
	}
}

// TestHostApplyGateHoldsTheLockAcrossTheWholeSequence pins that the lock covers the OBSERVE
// pass as well as the write.
//
// Locking only the apply would leave the window that matters open: the survey could read a
// home another process is halfway through applying, conclude "out of date", and prompt about
// drift that is gone by the time it asks. So the assertion is made where the gate is doing its
// reading — inside the terminal probe, which runs after the survey and before any write.
func TestHostApplyGateHoldsTheLockAcrossTheWholeSequence(t *testing.T) {
	home := gateFixture(t, true)
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("assert apply rc=%d\n%s", rc, report)
	}
	driftTheHome(t, home)

	var heldDuringTheSurvey bool
	prev := hostGateCanPrompt
	hostGateCanPrompt = func() bool {
		// By now the survey has run. A second acquire must fail, i.e. this process is holding
		// the lock it took before reading.
		if l := tryHostApplyLock(home); l != nil {
			l.Close()
		} else {
			heldDuringTheSurvey = true
		}
		return false // fall through to the non-TTY refusal; the launch outcome is not the point
	}
	t.Cleanup(func() { hostGateCanPrompt = prev })

	var errw bytes.Buffer
	hostApplyGate(&errw, nil, "claude")
	if !heldDuringTheSurvey {
		t.Errorf("the gate read the home without holding its apply lock — the survey can then "+
			"describe a home another writer is halfway through:\n%s", errw.String())
	}
	// And it is RELEASED on the way out, or the next launch's check would be a permanent
	// no-op and the agent would inherit a lock it has no business holding.
	if l := tryHostApplyLock(home); l == nil {
		t.Error("the gate did not release the lock before returning")
	} else {
		l.Close()
	}
}

// TestHostApplyLockSurvivesAnUnwritableStateDir is the third failure the acquire folds into one
// answer: it must not panic and must not stop a launch. A home whose state dir cannot be
// created is exactly as unresolvable as a contended lock, and the caller does the same thing.
func TestHostApplyLockSurvivesAnUnwritableStateDir(t *testing.T) {
	home := t.TempDir()
	// A FILE where the state dir has to be a directory: MkdirAll fails, with no way around it.
	if err := os.WriteFile(filepath.Join(home, ".local"), []byte("not a dir\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if l := tryHostApplyLock(home); l != nil {
		l.Close()
		t.Error("took a lock under a state dir that cannot exist")
	}
}
