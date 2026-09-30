package hostfloor

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// lock.go is the floor's one-install-per-program rule (host-tool-provisioning.md §4,
// "Concurrency"): a per-program flock in the prefix. Installs of DIFFERENT programs never wait on
// each other; two launches of one program produce one install.
//
// A flock rather than a pid file, because the kernel releases it when its holder dies — a killed
// install leaves no lock anyone has to judge stale. The holder's pid is written INTO the file all
// the same, for the one line a waiting launch prints ("waiting for pid N"), and for `yolo check`.

// fileLock is one held lock.
type fileLock struct{ f *os.File }

// flock is syscall.Flock behind a var so a test can stand in for contention without a second
// process. Nothing but a test reassigns it.
var flock = syscall.Flock

// errLockHeld reports a non-blocking acquire that found the lock taken.
var errLockHeld = errors.New("lock held")

// acquire takes the lock at path. With wait false it fails fast with errLockHeld; with wait true
// it first tries without blocking, and calls onWait with the holder's pid (0 when unreadable)
// before blocking, so the caller can say who it is waiting for.
func acquire(path string, wait bool, onWait func(pid int)) (*fileLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			f.Close()
			return nil, err
		}
		if !wait {
			f.Close()
			return nil, errLockHeld
		}
		if onWait != nil {
			onWait(lockHolder(path))
		}
		if err := flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
			f.Close()
			return nil, err
		}
	}
	// The holder's pid, for the next waiter's line. Truncate first: a shorter pid must not
	// leave the tail of a longer one behind.
	if err := f.Truncate(0); err == nil {
		_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	}
	return &fileLock{f: f}, nil
}

// release drops the lock. The file stays: removing it would race a waiter that has it open.
func (l *fileLock) release() {
	if l == nil || l.f == nil {
		return
	}
	_ = flock(int(l.f.Fd()), syscall.LOCK_UN)
	_ = l.f.Close()
	l.f = nil
}

// lockHolder reads the pid the last holder wrote, 0 when there is none.
func lockHolder(path string) int {
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

// LockState is what `yolo check` says about one program's lock.
type LockState struct {
	// Held is true when an install of this program is running right now.
	Held bool
	// PID is the pid the lock file names: the running installer's when Held, otherwise the
	// last holder's.
	PID int
}

// Lock reports bin's install lock without disturbing it: a non-blocking acquire that, when it
// succeeds, is released at once. It never waits.
func (f *Floor) Lock(bin string) LockState {
	path := f.lockPath(bin)
	st := LockState{PID: lockHolder(path)}
	fh, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return st
	}
	defer fh.Close()
	if err := flock(int(fh.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		st.Held = errors.Is(err, syscall.EWOULDBLOCK)
		return st
	}
	_ = flock(int(fh.Fd()), syscall.LOCK_UN)
	return st
}
