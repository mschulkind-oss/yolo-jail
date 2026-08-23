package run

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// workspaceLock is a held exclusive flock on a lock file (the per-workspace race
// guard). Close releases the lock + closes the fd (idempotent).
type workspaceLock struct {
	f      *os.File
	closed bool
}

// lockNotices are the two console seams acquireWorkspaceLock speaks through.
//
// TWO SEAMS, NOT ONE, and the distinction is the point of the type rather than
// an accident of the signature: a WAIT IS NOT A WARNING. `warn` says the race
// guard is off and this launch may collide with another; `waiting` says the guard
// is doing exactly its job and the caller should expect a pause. Folding the
// second onto the first would have printed "Warning: Waiting for concurrent jail
// launch..." — the call site wraps warn's text in a "Warning:" prefix — telling a
// user that normal, correct serialisation is a problem. They also differ in
// arity over time (a wait has a natural end, a warning does not), so keeping
// them apart leaves room for a "done waiting" note without re-typing warn.
//
// A struct rather than two positional func params because the two have identical
// types: at the call site `warn, waiting` and `waiting, warn` both compile, and
// the failure would be silent and exactly backwards. Either field may be nil.
type lockNotices struct {
	// warn reports that the flock could not be taken at all (race protection
	// disabled) — a degraded launch that still proceeds.
	warn func(string)
	// waiting reports that another process holds the lock and this one is about
	// to block until it is released.
	waiting func(string)
}

// acquireWorkspaceLock opens lockPath and takes an exclusive flock, NON-BLOCKING
// FIRST so that contention is observable.
//
// The blocking-only version of this function was silent: a second `yolo` in the
// same workspace parked on syscall.Flock while the first built the image and
// provisioned the overlay, with no terminal output whatsoever, which reads as a
// hang rather than as the race guard working. So the acquire is now two steps —
// LOCK_NB to learn whether anyone else holds it, then the blocking acquire — and
// the only thing that changes between them is that the user is told why the wait
// is happening (workspace named, so a user with several jails knows WHICH one is
// ahead of them; lock file named, so the wait is traceable to a real path).
//
// A flock ERROR (anything that is not contention) keeps the old behavior
// deliberately: warn, and return the open file anyway so the caller proceeds
// unguarded. A workspace lock is a courtesy against a self-inflicted race, not a
// safety property worth refusing a launch over.
//
// warn/waiting are the two seams (see lockNotices); either may be nil.
func acquireWorkspaceLock(lockPath, workspace string, notices lockNotices) (*workspaceLock, error) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	fd := int(f.Fd())

	err = flockSyscall(fd, syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return &workspaceLock{f: f}, nil
	}
	// EAGAIN and EWOULDBLOCK are the same errno on every platform this builds
	// for; both are named because that equality is a platform fact, not an API
	// promise, and a port that split them must not lose the notice.
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		if notices.waiting != nil {
			notices.waiting(fmt.Sprintf(
				"Waiting for concurrent jail launch in workspace %s (lock %s)...",
				workspace, filepath.Base(lockPath)))
		}
	}
	// Fall through to the blocking acquire on a non-contention error too: the NB
	// attempt is an INFORMATIONAL probe, and treating an unexpected errno from it
	// as fatal-to-locking would drop a guard the old code still took. Only a
	// failure of the blocking call itself means the lock was not obtained.
	if err := flockSyscall(fd, syscall.LOCK_EX); err != nil {
		if notices.warn != nil {
			notices.warn("could not acquire workspace lock (" + err.Error() +
				"); race protection disabled")
		}
	}
	return &workspaceLock{f: f}, nil
}

// flockSyscall is syscall.Flock behind a package var so the error path above —
// the one that degrades a launch instead of failing it — is reachable from a
// test. Nothing but a test ever reassigns it.
var flockSyscall = syscall.Flock

// Close releases the flock and closes the fd. Idempotent (guarded here for the
// multiple teardown paths).
func (l *workspaceLock) Close() {
	if l == nil || l.closed {
		return
	}
	l.closed = true
	_ = l.f.Close() // closing the fd releases the flock
}
