// Package nixchildren keeps the set of nix processes this yolo process has running, so that a yolo
// ended by a signal stops them instead of leaving them behind.
//
// A Ctrl-C at a terminal reaches nix by itself: the terminal signals its whole foreground group,
// nix included. A signal sent to yolo's PID alone does not — `kill -INT`, a supervisor's SIGTERM, a
// test harness — and yolo then ended and left its nix running with no parent (measured
// 2026-10-03: an interrupted launch's `nix eval --impure --raw .#imageIdentity`, parent pid 1,
// still running after its test had passed). Stop gives each one the interrupt the terminal would
// have, so it ends the way a Ctrl-C ends it.
//
// A nix is in the set when it was started through Start, Run or StartIfNix, and only then. A
// signal teardown calls Stop: each of a container launch's signal arms calls it first
// (internal/cli/run's launchSignalArm.terminate), and StopOnSignal is the teardown of a command
// with no signal arm of its own around the nix it runs.
//
// The package is its own so that every package running a nix can use the one set: internal/image
// and internal/darwinpkg are independent leaves, and the no-image backend does not import the
// image package (internal/image's NixFlakeFlags says why).
package nixchildren

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
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
	// exits is where a test's set (Isolate) hears the process exits StopOnSignal's teardown ends
	// with. nil for the process's own set, whose exit is os.Exit.
	exits chan int
}

func newSet() *Set {
	return &Set{running: map[*os.Process]chan struct{}{}}
}

// current is this process's set.
var current = newSet()

// ErrStopped is a nix not started because a signal is ending this process.
var ErrStopped = errors.New("nix not started: a signal is ending yolo")

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

// StartIfNix is Start for a cmd that runs a nix — `nix` itself or one of its `nix-*` tools, by
// the base name of cmd.Args[0] — and cmd.Start for any other program: for an exec seam that runs
// nix among other programs, such as `yolo check`'s probes, `yolo prune`'s and a launch's. Its
// release is the caller's once cmd's Wait has returned, and does nothing for a program that is no
// nix.
func StartIfNix(cmd *exec.Cmd) (release func(), err error) {
	if len(cmd.Args) > 0 && isNix(cmd.Args[0]) {
		return Start(cmd)
	}
	return func() {}, cmd.Start()
}

// isNix reports whether name, a program to run, is a nix: `nix` or a `nix-*` tool such as
// nix-store, by its base name.
func isNix(name string) bool {
	base := filepath.Base(name)
	return base == "nix" || strings.HasPrefix(base, "nix-")
}

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

// exit is how StopOnSignal's teardown ends the process. Isolate fakes it.
var exit = os.Exit

// StopOnSignal arms SIGINT, SIGHUP and SIGTERM, until the disarm it returns, for a command with no
// signal teardown of its own around the nix it runs, such as `yolo check`. A signal stops every nix
// this process has running (Stop) and then ends the process with status 128+N, the status a
// launch's own arm ends with. Without it, the signal's default action ended yolo at once and its
// nix ran on with no parent.
//
// A SIGHUP or SIGINT this process started with ignored is not armed, and stays ignored: `nohup`
// ignores SIGHUP, and a shell without job control starts a background job with SIGINT ignored.
// Notify would turn either back on, so `nohup yolo check` ended 129 on a hangup and stopped its
// image build, which before the arm went on. (Those two are the signals the Go runtime leaves
// ignored when it inherits them so; signal.Ignored says which.)
//
// A container launch does not use it: each of its own signal arms calls Stop itself
// (internal/cli/run's launchSignalArm.terminate), and two arms acting on one signal would race to
// exit.
//
// disarm is idempotent. Called once a signal's teardown has begun, it waits for that teardown's
// exit instead of returning, so the caller never races it to the end of the process with a status
// of its own. A signal already delivered to the arm as it is disarmed is raised again, to take
// whatever action it has without the arm.
func StopOnSignal() (disarm func()) {
	s, end := current, exit
	var armed []os.Signal
	for _, sig := range []os.Signal{syscall.SIGINT, syscall.SIGHUP, syscall.SIGTERM} {
		if !signal.Ignored(sig) {
			armed = append(armed, sig)
		}
	}
	if len(armed) == 0 {
		return func() {}
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, armed...)
	var (
		mu       sync.Mutex
		ending   bool
		disarmed bool
	)
	removed, exited := make(chan struct{}), make(chan struct{})
	go func() {
		var sig os.Signal
		select {
		case sig = <-sigs:
		case <-removed:
			select {
			case sig = <-sigs:
			default:
				return
			}
		}
		mu.Lock()
		if disarmed {
			mu.Unlock()
			raise(sig)
			return
		}
		ending = true
		mu.Unlock()
		s.stop(stopGrace, func() { <-exited })
		end(signalStatus(sig))
		// Reached only where the exit is faked (Isolate): the process has ended otherwise.
		close(exited)
	}()
	return func() {
		mu.Lock()
		if ending {
			mu.Unlock()
			<-exited
			return
		}
		if !disarmed {
			disarmed = true
			signal.Stop(sigs)
			close(removed)
		}
		mu.Unlock()
	}
}

// signalStatus is the exit status for a process a signal ended: 128+N.
func signalStatus(sig os.Signal) int {
	if n, ok := sig.(syscall.Signal); ok {
		return 128 + int(n)
	}
	return 1
}

// raise sends sig to this process again.
func raise(sig os.Signal) {
	if p, err := os.FindProcess(os.Getpid()); err == nil {
		_ = p.Signal(sig)
	}
}

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
// It fakes the process exit StopOnSignal's teardown ends with too, for the same reason: the
// teardown's status arrives on the set's Exited instead, and the test goes on.
//
// t is a *testing.T; Cleanup is all this needs of it, so the yolo binary does not link the testing
// package.
func Isolate(t interface{ Cleanup(func()) }) *Set {
	savedSet, savedExit, s := current, exit, newSet()
	s.exits = make(chan int, 4)
	current = s
	exit = func(code int) {
		select {
		case s.exits <- code:
		default:
		}
	}
	t.Cleanup(func() {
		s.stop(time.Second, nil)
		current, exit = savedSet, savedExit
	})
	return s
}

// Stopped reports whether a stop has run on the set this process uses now — its own, once every
// test's Isolate has been undone. It is for a test binary's TestMain: a test that drove a real stop
// without Isolate left the process's own set stopped for good, every later test in the binary that
// started a tracked nix was refused, and whether that test passed depended on the order the tests
// ran in. For tests only; nothing in yolo calls it.
func Stopped() bool {
	current.mu.Lock()
	defer current.mu.Unlock()
	return current.stopping
}

// Exited is the statuses StopOnSignal's teardown ended the process with, for a set Isolate made.
func (s *Set) Exited() <-chan int { return s.exits }

// Running is how many nix processes s has started and not yet released: a test's way to know its
// stand-in nix is running before it sends the signal that should stop it.
func (s *Set) Running() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.running)
}
