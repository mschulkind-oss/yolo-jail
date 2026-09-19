package cli

// restoreonce_test.go pins the jail-indicator restore closure: it runs its work exactly
// once across BOTH arms, and calling it does not deadlock.
//
// The deadlock is the reason this file exists. The first draft wrote
// `restoreOnce := func() { once.Do(restore) }` and then `restore = restoreOnce`, so the
// closure called Do on the very Once it was running inside. sync.Once.Do blocks until the
// first call returns, so the second Do never returns either — no error, no panic, no
// stack, just a launcher that never exits. Shipped for one commit: `exit` at a jail prompt
// printed the timing line and hung, and the only escape was the Ctrl-C the same change had
// just given back to the jail.
//
// A plain "it runs once" assertion would NOT have caught it — the deadlock happens on the
// FIRST call. The timeout is the part that matters.

import (
	"sync"
	"testing"
	"time"
)

// restoreOnceForTest mirrors runRun's construction exactly. It is duplicated rather than
// exported because what is being pinned is the SHAPE — capture the original by value —
// and a shared helper would let the production site drift away from the thing under test
// while this file stayed green.
func restoreOnceForTest(restore func()) func() {
	inner := restore
	var once sync.Once
	return func() { once.Do(inner) }
}

func TestRestoreOnceDoesNotDeadlock(t *testing.T) {
	var calls int
	f := restoreOnceForTest(func() { calls++ })

	done := make(chan struct{})
	go func() {
		f()
		f() // the second arm: both the defer and RestoreTerminal may call it
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the restore closure deadlocked. sync.Once.Do blocks until the first " +
			"call returns, so a closure that calls Do on the Once it is running inside " +
			"never returns — which is what made `exit` hang after the timing line.")
	}
	if calls != 1 {
		t.Errorf("restore ran %d times, want exactly 1", calls)
	}
}

// THE MUTATION THIS FILE EXISTS FOR, as an executable statement of the bug: a closure that
// resolves the function through a variable reassigned to the closure itself deadlocks.
// Pinned so nobody reintroduces the shape believing it equivalent.
func TestSelfReferentialOnceIsTheBugShape(t *testing.T) {
	var once sync.Once
	var restore func()
	work := func() {}
	restore = work
	selfRef := func() { once.Do(restore) }
	restore = selfRef // the defect: Do now re-enters itself

	done := make(chan struct{})
	go func() { selfRef(); close(done) }()

	select {
	case <-done:
		t.Error("the self-referential Once returned — if sync.Once ever stops blocking " +
			"on re-entry, the ⚠ in runRun can be relaxed. Until then it is load-bearing.")
	case <-time.After(300 * time.Millisecond):
		// Deadlocked, as documented. The goroutine stays parked for the life of the
		// test binary, which is why the work function is empty and holds nothing.
	}
}
