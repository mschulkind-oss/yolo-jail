package mcpauth

import (
	"errors"
	"os"
	"time"

	"syscall"
)

// fileLock is an advisory flock held until Close.
type fileLock struct{ file *os.File }

// ErrStoreBusy is the closed existing-state lock classification shared by
// issuance and status; callers must not diagnose arbitrary error text.
var ErrStoreBusy = errors.New("mcp-auth: timed out waiting for store lock")

var storeLockNow = time.Now
var storeLockSleep = time.Sleep

func lockFile(file *os.File, wait time.Duration) (*fileLock, error) {
	deadline := storeLockNow().Add(wait)
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &fileLock{file: file}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EINTR) {
			return nil, err
		}
		if !storeLockNow().Before(deadline) {
			return nil, ErrStoreBusy
		}
		storeLockSleep(5 * time.Millisecond)
	}
}

func (l *fileLock) Close() error {
	unlockErr := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
