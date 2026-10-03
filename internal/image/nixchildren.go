package image

// nixchildren.go keeps the set of nix processes this process has running for a launch — the
// install-prefix and image builds, the image-identity eval, the store-delivered extras build — so
// that a launch ended by a signal stops them instead of leaving them behind.
//
// A Ctrl-C at a terminal reaches nix by itself: the terminal signals its whole foreground group,
// nix included. A signal sent to yolo's PID alone does not — `kill -INT`, a supervisor's SIGTERM, a
// test harness — and the launch guard then ended yolo and left its nix running with no parent
// (measured 2026-10-03: an interrupted launch's `nix eval --impure --raw .#imageIdentity`, parent
// pid 1, still running after its test had passed). StopNixChildren gives each one the interrupt the
// terminal would have, so it ends the way a Ctrl-C ends it.

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"time"
)

// nixStopGrace is how long StopNixChildren waits for an interrupted nix to exit before it kills
// it. nix answers an interrupt within a second or two; the bound is for one that does not.
const nixStopGrace = 10 * time.Second

// nixChildSet is the nix processes started through it and not yet waited for.
type nixChildSet struct {
	mu sync.Mutex
	// stopping says StopNixChildren has run: no nix starts after it. The launch's main goroutine
	// goes on while the signal's teardown runs, and an identity eval the stop cut short falls
	// through to a build, which would otherwise start just as the process exits and outlive it.
	stopping bool
	running  map[*os.Process]chan struct{}
}

func newNixChildSet() *nixChildSet {
	return &nixChildSet{running: map[*os.Process]chan struct{}{}}
}

// nixChildren is this process's set.
var nixChildren = newNixChildSet()

// errNixStopped is a nix not started because a signal is ending the launch.
var errNixStopped = errors.New("nix not started: a signal is ending this launch")

// start starts cmd and tracks it until release, which the caller calls once cmd's Wait returned,
// or refuses with errNixStopped once a stop has run. The start happens under the set's lock, so a
// stop either finds the child registered or keeps it from starting: there is no moment between.
func (s *nixChildSet) start(cmd *exec.Cmd) (release func(), err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return func() {}, errNixStopped
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
	}, nil
}

// stop interrupts every running nix of the set, waits up to grace for each to be waited for, and
// kills any still running then.
func (s *nixChildSet) stop(grace time.Duration) {
	s.mu.Lock()
	s.stopping = true
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

// StartNix starts cmd, a nix this launch runs, and tracks it until release, which the caller
// calls once cmd's Wait has returned.
func StartNix(cmd *exec.Cmd) (release func(), err error) { return nixChildren.start(cmd) }

// RunNix is cmd.Run for a nix this launch runs, tracked while it runs.
func RunNix(cmd *exec.Cmd) error {
	release, err := StartNix(cmd)
	if err != nil {
		return err
	}
	defer release()
	return cmd.Wait()
}

// StopNixChildren ends every nix this process still has running for a launch — an interrupt
// first, as a terminal's Ctrl-C would deliver, then, past nixStopGrace, a kill — and starts no
// more. A launch's signal teardown calls it before the process exits.
func StopNixChildren() { nixChildren.stop(nixStopGrace) }
