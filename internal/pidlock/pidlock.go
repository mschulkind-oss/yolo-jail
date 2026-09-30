// Package pidlock is an exclusive file lock whose holder writes its pid into the file: the
// shape the host floor's one-install-per-program rule has (docs/design/host-tool-provisioning.md
// §4, "Concurrency"), lifted out so the fork build's lock (docs/design/forked-programs-as-packs.md
// FP-D1) is the same lock rather than a second one.
//
// A flock rather than a pid file, because the kernel releases it when its holder dies — a killed
// holder leaves no lock anyone has to judge stale. The pid written INTO the file is for the one line
// a waiter prints ("waiting for pid N") and for a status report; nothing decides by it.
package pidlock

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Lock is one held lock.
type Lock struct{ f *os.File }

// Flock is syscall.Flock behind a var so a test can stand in for contention without a second
// process. Nothing but a test reassigns it.
var Flock = syscall.Flock

// ErrHeld reports a non-blocking acquire that found the lock taken.
var ErrHeld = errors.New("lock held")

// ErrTimedOut reports a bounded wait that ran out with the lock still taken.
var ErrTimedOut = errors.New("timed out waiting for the lock")

// Mode is how an acquire behaves when the lock is taken.
type Mode struct {
	// Wait blocks until the lock is free. Without it the acquire fails at once with ErrHeld.
	Wait bool
	// Bound, with Wait, gives up after this long with ErrTimedOut. Zero waits without limit.
	Bound time.Duration
}

// NoWait fails at once when the lock is taken.
var NoWait = Mode{}

// Wait blocks until the lock is free, without limit.
var Wait = Mode{Wait: true}

// pollInterval is how often a bounded wait asks again: flock has no timeout of its own.
const pollInterval = 50 * time.Millisecond

// Acquire takes the lock at path. When it is taken, NoWait fails with ErrHeld; a waiting mode first
// calls onWait with the holder's pid (0 when unreadable), so the caller can say who it waits for,
// and then waits — without limit, or until mode.Bound runs out (ErrTimedOut).
func Acquire(path string, mode Mode, onWait func(pid int)) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			f.Close()
			return nil, err
		}
		if !mode.Wait {
			f.Close()
			return nil, ErrHeld
		}
		if onWait != nil {
			onWait(Holder(path))
		}
		if err := waitFor(f, mode.Bound); err != nil {
			f.Close()
			return nil, err
		}
	}
	// The holder's pid, for the next waiter's line. Truncate first: a shorter pid must not leave
	// the tail of a longer one behind.
	if err := f.Truncate(0); err == nil {
		_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	}
	return &Lock{f: f}, nil
}

// waitFor blocks on f's lock, without limit when bound is zero, else polling until bound.
func waitFor(f *os.File, bound time.Duration) error {
	if bound <= 0 {
		return Flock(int(f.Fd()), syscall.LOCK_EX)
	}
	deadline := time.Now().Add(bound)
	for {
		err := Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return err
		}
		if !time.Now().Before(deadline) {
			return ErrTimedOut
		}
		time.Sleep(pollInterval)
	}
}

// Release drops the lock. The file stays: removing it would race a waiter that has it open.
func (l *Lock) Release() {
	if l == nil || l.f == nil {
		return
	}
	_ = Flock(int(l.f.Fd()), syscall.LOCK_UN)
	_ = l.f.Close()
	l.f = nil
}

// Holder reads the pid the last holder wrote, 0 when there is none.
func Holder(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0
	}
	return pid
}

// Held reports whether the lock at path is held right now, without disturbing it: a non-blocking
// acquire that, when it succeeds, is released at once. It never waits.
func Held(path string) bool {
	fh, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return false
	}
	defer fh.Close()
	if err := Flock(int(fh.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.Is(err, syscall.EWOULDBLOCK)
	}
	_ = Flock(int(fh.Fd()), syscall.LOCK_UN)
	return false
}
