// Package heldchildren lets a test process stop the detached daemons it started itself,
// through the process handles it holds, and nothing else.
//
// A host singleton (broker.EnsureSingleton) is spawned detached and is meant to outlive
// the launch that started it, so production never stops one. A test binary that spawned
// one must, or the daemon outlives the test run. Finding it again by a PID read from a
// file is unsafe on a machine many test runs share: the file can be another run's, and
// the kernel may have handed the PID to an unrelated process since. So the spawner
// hands its *os.Process here, and a test stops only what is held. Signalling through the
// handle cannot reach a reused PID on Linux, where Go signals through a pidfd; elsewhere
// it narrows the risk to Go's own window between reaping a child and marking it done.
//
// Two limits, accepted. A child stopped by StopAll is any child held since Enable, which
// is sticky, so a nested isolation's release stops its enclosing scope's children too.
// And a test binary that is killed runs no cleanup, so what it held is stopped by nothing
// here; a singleton daemon exits once its state directory, under the test's temporary
// HOME, is removed (hostservice.WatchStateDir).
//
// Nothing is held until Enable is called, which only tests do, so in production Hold is
// a no-op and the package holds no references.
package heldchildren

import (
	"os"
	"sync"
	"syscall"
	"time"
)

type child struct {
	p      *os.Process
	exited <-chan struct{}
}

var (
	mu      sync.Mutex
	enabled bool
	held    []child
)

// Enable starts recording every child Hold is given. Tests call it (testsupport's
// IsolateHostSingletons does); production never does.
func Enable() {
	mu.Lock()
	enabled = true
	mu.Unlock()
}

// Hold records p, a child this process started and is reaping, whose exit closes exited.
// It is a no-op until Enable is called.
func Hold(p *os.Process, exited <-chan struct{}) {
	if p == nil || exited == nil {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	if !enabled {
		return
	}
	held = append(held, child{p: p, exited: exited})
}

// Stop stops the live held child whose PID is pid: SIGTERM, then SIGKILL once grace has
// passed, then a wait for its exit. It reports whether such a child was held; a PID this
// process does not hold is never signalled.
func Stop(pid int, grace time.Duration) bool {
	c, ok := take(func(c child) bool { return c.p.Pid == pid })
	if !ok {
		return false
	}
	stop(c, grace)
	return true
}

// StopAll stops every live held child, each as Stop does.
func StopAll(grace time.Duration) {
	for {
		c, ok := take(func(child) bool { return true })
		if !ok {
			return
		}
		stop(c, grace)
	}
}

// take removes and returns the most recently held live child match accepts, dropping
// every child that has already exited.
func take(match func(child) bool) (child, bool) {
	mu.Lock()
	defer mu.Unlock()
	live := held[:0]
	for _, c := range held {
		if !exited(c) {
			live = append(live, c)
		}
	}
	held = live
	for i := len(held) - 1; i >= 0; i-- {
		if match(held[i]) {
			c := held[i]
			held = append(held[:i], held[i+1:]...)
			return c, true
		}
	}
	return child{}, false
}

func exited(c child) bool {
	select {
	case <-c.exited:
		return true
	default:
		return false
	}
}

func stop(c child, grace time.Duration) {
	_ = c.p.Signal(syscall.SIGTERM)
	select {
	case <-c.exited:
		return
	case <-time.After(grace):
	}
	_ = c.p.Kill()
	select {
	case <-c.exited:
	case <-time.After(5 * time.Second):
	}
}
