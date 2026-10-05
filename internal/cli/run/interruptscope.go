package run

// interruptscope.go is an INTERRUPT SCOPE, a term coined here: a stretch of a launch during which a
// Ctrl-C ends one piece of work rather than the launch. Its one user is a patched fork's ADVANCE at a
// fresh launch (docs/design/patched-forks.md §7, PF-D25): the launch waits for the build of a newer
// upstream, and a Ctrl-C ends that build and starts the jail on the good build the machine already
// has. An act that runs several advances shares one ActInterrupt across their scopes, so that one
// Ctrl-C ends the act's whole wait (PF-D57).
//
// It is an arm of this process's launch-arm stack (armstack.go), installed innermost for the scope's
// span, so a signal reaches it and not the launch guard under it. Its arm runs no teardown and
// never exits: it cancels the scope's context, which the work inside reads (the check's and the
// walk's git, the build lock's wait, and the build jail, which runs as a child process of its own
// for that reason: internal/cli's forkbuildchild.go). A second signal changes nothing.
//
// ONLY A SIGINT IS THE SCOPE'S. A SIGHUP (the terminal went away) or a SIGTERM ends the launch as it
// would have without the scope: the scope ends its work, and then the signal is raised again once
// the scope's arm is gone, so the arm under it — the launch guard — ends the launch with what the
// launch made, as at any other point before its keeper.

import (
	"context"
	"os"
	"sync"
	"syscall"
)

// InterruptScope runs fn under an interrupt scope: a signal while fn runs cancels fn's context
// instead of ending the process. It returns once fn has, with the SIGINT that ended fn's work, or
// nil when none did. A SIGHUP or SIGTERM that arrived is raised again once the scope's arm is gone,
// and this never returns then: the arm under it ends the process.
func InterruptScope(fn func(ctx context.Context)) (interrupted os.Signal) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var got os.Signal
	a := armInterruptScope(func(s os.Signal) {
		mu.Lock()
		if got == nil || s != syscall.SIGINT {
			got = s
		}
		mu.Unlock()
		cancel()
	})
	fn(ctx)
	a.disarm()
	mu.Lock()
	sig := got
	mu.Unlock()
	if sig != nil && sig != syscall.SIGINT {
		reraiseSignal(sig)
	}
	return sig
}

// ActInterrupt is AN ACT'S INTERRUPT, a term coined here: one Ctrl-C's reach over a whole act that
// runs several interrupt scopes in turn — a fresh jail launch's fork-build slot (each patched fork's
// advance, then each patched extension's), a `yolo host -- <bin>` (its extensions' advances, then its
// program's), a `yolo host apply --assert` (every extension's, then every patched program's). A
// Ctrl-C ends the scope it lands in, as InterruptScope does, and the act remembers it, so the act's
// later work reads Interrupted and starts no wait: the user asked once to stop waiting, for the act,
// and a launch that waited out every remaining build anyway would need one Ctrl-C per patched item.
//
// The zero value is an act no Ctrl-C has reached. A nil act is no act: its Scope is a plain
// InterruptScope and it never reads as interrupted. Safe for concurrent use.
type ActInterrupt struct {
	mu  sync.Mutex
	sig os.Signal
}

// Scope runs fn under an interrupt scope (InterruptScope) and returns what that returns, recording a
// SIGINT that ended fn's work for the act's later work to read.
func (a *ActInterrupt) Scope(fn func(ctx context.Context)) os.Signal {
	sig := InterruptScope(fn)
	if a != nil && sig != nil {
		a.mu.Lock()
		a.sig = sig
		a.mu.Unlock()
	}
	return sig
}

// Interrupted reports whether a Ctrl-C ended one of the act's interrupt scopes; false for a nil act.
func (a *ActInterrupt) Interrupted() bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sig != nil
}

// armInterruptScope installs an interrupt scope's arm as the innermost: every signal routed to it
// is handed to interrupt, and nothing else happens — no teardown, no exit — until the scope
// disarms it. It never begins a teardown, so its disarm always succeeds.
func armInterruptScope(interrupt func(os.Signal)) *launchSignalArm {
	a := &launchSignalArm{signals: make(chan os.Signal, 4), done: make(chan struct{}), exited: make(chan struct{})}
	go func() {
		for {
			select {
			case s := <-a.signals:
				interrupt(s)
			case <-a.done:
				return
			}
		}
	}()
	pushLaunchArm(a)
	return a
}

// reraiseSignal raises sig at this process again, for the arm now innermost, and waits for that
// arm to end the process. A var so a test can see the raise without ending the test binary.
var reraiseSignal = func(sig os.Signal) {
	if s, ok := sig.(syscall.Signal); ok {
		_ = syscall.Kill(os.Getpid(), s)
	}
	select {}
}
