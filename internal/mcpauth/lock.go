package mcpauth

import (
	"errors"
	"os"
	"time"

	"syscall"
)

// fileLock is an advisory flock held until Close.
type fileLock struct{ file *os.File }

func lockFile(file *os.File, wait time.Duration) (*fileLock, error) {
	deadline := time.Now().Add(wait)
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &fileLock{file: file}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EINTR) {
			return nil, err
		}
		if !time.Now().Before(deadline) {
			return nil, errors.New("mcp-auth: timed out waiting for store lock")
		}
		time.Sleep(5 * time.Millisecond)
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
