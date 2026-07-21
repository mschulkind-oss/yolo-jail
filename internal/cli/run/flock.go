package run

import (
	"os"
	"syscall"
)

// workspaceLock is a held exclusive flock on a lock file (the per-workspace race
// guard). Close releases the lock + closes the fd (idempotent).
type workspaceLock struct {
	f      *os.File
	closed bool
}

// acquireWorkspaceLock opens lockPath and takes a BLOCKING exclusive flock.
// A flock error is non-fatal (race protection disabled) — the file handle is
// still returned so the fd is held until on_started releases it.
// warn receives the warning text on flock failure.
func acquireWorkspaceLock(lockPath string, warn func(string)) (*workspaceLock, error) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		if warn != nil {
			warn("could not acquire workspace lock (" + err.Error() + "); race protection disabled")
		}
	}
	return &workspaceLock{f: f}, nil
}

// Close releases the flock and closes the fd. Idempotent (guarded here for the
// multiple teardown paths).
func (l *workspaceLock) Close() {
	if l == nil || l.closed {
		return
	}
	l.closed = true
	_ = l.f.Close() // closing the fd releases the flock
}
