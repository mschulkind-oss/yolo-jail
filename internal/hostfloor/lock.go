package hostfloor

import (
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
)

// lock.go is the floor's one-install-per-program rule (host-tool-provisioning.md §4,
// "Concurrency"): a per-program flock in the prefix. Installs of DIFFERENT programs never wait on
// each other; two launches of one program produce one install.
//
// THE LOCK IS internal/pidlock, which this file's own implementation was lifted into so the fork
// build's lock is the same one (docs/design/forked-programs-as-packs.md FP-D1): a flock, released
// by the kernel when its holder dies, with the holder's pid written into the file for the one line
// a waiting launch prints ("waiting for pid N") and for `yolo check`.

// fileLock is one held lock.
type fileLock struct{ l *pidlock.Lock }

// errLockHeld reports a non-blocking acquire that found the lock taken.
var errLockHeld = pidlock.ErrHeld

// acquire takes the lock at path. With wait false it fails fast with errLockHeld; with wait true
// it first tries without blocking, and calls onWait with the holder's pid (0 when unreadable)
// before blocking, so the caller can say who it is waiting for.
func acquire(path string, wait bool, onWait func(pid int)) (*fileLock, error) {
	mode := pidlock.NoWait
	if wait {
		mode = pidlock.Wait
	}
	l, err := pidlock.Acquire(path, mode, onWait)
	if err != nil {
		return nil, err
	}
	return &fileLock{l: l}, nil
}

// release drops the lock. The file stays: removing it would race a waiter that has it open.
func (l *fileLock) release() {
	if l == nil {
		return
	}
	l.l.Release()
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
	return LockState{PID: pidlock.Holder(path), Held: pidlock.Held(path)}
}
