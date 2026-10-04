// Package nixchildren keeps the set of nix processes this yolo process has running, so that a yolo
// ended by a signal stops them instead of leaving them behind.
//
// A Ctrl-C at a terminal reaches nix by itself: the terminal signals its whole foreground group,
// nix included. A signal sent to yolo's PID alone does not — `kill -INT`, a supervisor's SIGTERM, a
// test harness — and the launch guard then ended yolo and left its nix running with no parent
// (measured 2026-10-03: an interrupted launch's `nix eval --impure --raw .#imageIdentity`, parent
// pid 1, still running after its test had passed). Stop gives each one the interrupt the terminal
// would have, so it ends the way a Ctrl-C ends it.
//
// A nix is in the set when it was started through Start or Run, and only then. The package is its
// own so that every package running a nix can use the one set: internal/image and
// internal/darwinpkg are independent leaves, and the no-image backend does not import the image
// package (internal/image's NixFlakeFlags says why).
package nixchildren

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"time"
)

// stopGrace is how long Stop waits for an interrupted nix to exit before it kills it. nix answers
// an interrupt within a second or two; the bound is for one that does not.
const stopGrace = 10 * time.Second

// Set is the nix processes started through it and not yet waited for. This process has one
// (current); Isolate gives a test its own.
type Set struct {
	mu sync.Mutex
	// stopping says Stop has run: no nix starts after it. The launch's main goroutine goes on while
	// the signal's teardown runs, and an identity eval the stop cut short falls through to a build,
	// which would otherwise start just as the process exits and outlive it.
	stopping bool
	// awaitExit is the stop's hand-off, run by the goroutine whose nix the stop cut short once that
	// nix has been waited for: the teardown that stopped it owns the process's exit, and nothing
	// that goroutine would go on to do is the launch's any more. Left to go on, it reported the
	// build the signal interrupted as a failed one ("Cannot start jail: could not build yolo's own
	// binaries") just as the teardown exited, three interrupted launches of three (2026-10-03),
	// where before the stop it never got past the nix. nil hands nothing off.
	awaitExit func()
	running   map[*os.Process]chan struct{}
}

func newSet() *Set {
	return &Set{running: map[*os.Process]chan struct{}{}}
}

// current is this process's set.
var current = newSet()

// ErrStopped is a nix not started because a signal is ending this process.
var ErrStopped = errors.New("nix not started: a signal is ending this launch")

// start starts cmd and tracks it until release, which the caller calls once cmd's Wait returned,
// or refuses with ErrStopped once a stop has run. The start happens under the set's lock, so a
// stop either finds the child registered or keeps it from starting: there is no moment between.
//
// A release after a stop hands off (awaitExit) before it returns, so a caller does not go on to
// report the end of a nix the stop caused. A refused start does not: the stop is the teardown's
// first act, so its own goroutine has no nix to release, but it is the one goroutine that could
// still ask to start one, and waiting there for its own exit would never end.
func (s *Set) start(cmd *exec.Cmd) (release func(), err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return func() {}, ErrStopped
	}
	if err := cmd.Start(); err != nil {
		return func() {}, err
	}
	p, done := cmd.Process, make(chan struct{})
	s.running[p] = done
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.running, p)
			s.mu.Unlock()
			close(done)
		})
		s.handOff()
	}, nil
}

// handOff runs the stop's awaitExit once a stop has run, and returns at once before one.
func (s *Set) handOff() {
	s.mu.Lock()
	await := s.awaitExit
	if !s.stopping {
		await = nil
	}
	s.mu.Unlock()
	if await != nil {
		await()
	}
}

// stop interrupts every running nix of the set, waits up to grace for each to be waited for, and
// kills any still running then. awaitExit is the hand-off each one's caller runs (handOff).
func (s *Set) stop(grace time.Duration, awaitExit func()) {
	s.mu.Lock()
	s.stopping = true
	s.awaitExit = awaitExit
	running := make(map[*os.Process]chan struct{}, len(s.running))
	for p, done := range s.running {
		running[p] = done
	}
	s.mu.Unlock()
	for p := range running {
		_ = p.Signal(os.Interrupt)
	}
	deadline := time.NewTimer(grace)
	defer deadline.Stop()
	expired := false
	for p, done := range running {
		if !expired {
			select {
			case <-done:
				continue
			case <-deadline.C:
				expired = true
			}
		}
		select {
		case <-done:
		default:
			_ = p.Kill()
		}
	}
}

// Start starts cmd, a nix this process runs, and tracks it until release, which the caller calls
// once cmd's Wait has returned.
func Start(cmd *exec.Cmd) (release func(), err error) { return current.start(cmd) }

// Run is cmd.Run for a nix this process runs, tracked while it runs.
func Run(cmd *exec.Cmd) error {
	release, err := Start(cmd)
	if err != nil {
		return err
	}
	defer release()
	return cmd.Wait()
}

// Stop ends every nix this process still has running — an interrupt first, as a terminal's Ctrl-C
// would deliver, then, past stopGrace, a kill — and starts no more. A signal teardown calls it
// before the process exits, with awaitExit waiting for that exit: the goroutine each stopped nix
// belonged to runs it before going on.
func Stop(awaitExit func()) { current.stop(stopGrace, awaitExit) }

// Isolate gives the calling test a set of tracked nix children of its own until it ends: an empty
// set now, stopped when the test ends so no stand-in nix it started outlives it, and then the
// previous set back. For tests only; nothing in yolo calls it.
//
// A stop is permanent for the set it ran on, as it must be for a process a signal is ending, which
// exits next. A test binary fakes that exit and goes on to other tests, so a test that drove a
// launch's signal teardown through the real Stop left every later test in its binary that started
// a tracked nix refused, and whether that test passed depended on the order the tests ran in
// (internal/cli/run's TestAGuardTestsNixStopEndsWithTheTest).
//
// t is a *testing.T; Cleanup is all this needs of it, so the yolo binary does not link the testing
// package.
func Isolate(t interface{ Cleanup(func()) }) *Set {
	saved, s := current, newSet()
	current = s
	t.Cleanup(func() {
		s.stop(time.Second, nil)
		current = saved
	})
	return s
}

// Running is how many nix processes s has started and not yet released: a test's way to know its
// stand-in nix is running before it sends the signal that should stop it.
func (s *Set) Running() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.running)
}
