package configedit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// DirectoryLock is an opaque, callback-lifetime exclusive directory lock. Its descriptor
// belongs to WithDirectoryLock; config edits may borrow it but cannot release its ownership.
type DirectoryLock struct {
	parent *os.File
	path   string
}

// WithDirectoryLock holds a read-only directory descriptor through fn and releases it on
// every return. Nonblocking callers receive false when another descriptor owns the lock.
// The token is valid only during fn and must not be used concurrently or retained afterward.
func WithDirectoryLock(path string, nonblocking bool, fn func(*DirectoryLock) error) (bool, error) {
	if fn == nil {
		return false, errors.New("directory lock callback is required")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	parent, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer parent.Close()
	info, err := parent.Stat()
	if err != nil || !info.IsDir() {
		return false, fmt.Errorf("lock parent %s is not a directory", path)
	}
	mode := unix.LOCK_EX
	if nonblocking {
		mode |= unix.LOCK_NB
	}
	if err := unix.Flock(int(parent.Fd()), mode); err != nil {
		if nonblocking && (errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN)) {
			return false, nil
		}
		return false, err
	}
	defer unlockParentDirectory(parent)
	if err := verifyParentDirectory(parent, path); err != nil {
		return false, err
	}
	held := &DirectoryLock{parent: parent, path: path}
	defer func() { held.parent = nil }()
	return true, fn(held)
}

func openLockedParentDirectory(path string, held *DirectoryLock) (*os.File, func(), error) {
	parent, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open config target parent %s: %w", path, err)
	}
	if held != nil {
		if held.parent == nil {
			parent.Close()
			return nil, nil, errors.New("caller-held config parent lock is no longer live; retry within its owning transaction")
		}
		if err := verifyParentDirectory(held.parent, held.path); err != nil {
			parent.Close()
			return nil, nil, err
		}
		targetInfo, err := parent.Stat()
		if err != nil {
			parent.Close()
			return nil, nil, err
		}
		heldInfo, err := held.parent.Stat()
		if err != nil {
			parent.Close()
			return nil, nil, err
		}
		if os.SameFile(targetInfo, heldInfo) {
			parent.Close()
			return held.parent, func() {}, nil
		}
	}
	if err := lockParentDirectory(parent); err != nil {
		parent.Close()
		return nil, nil, fmt.Errorf("lock config target parent %s: %w", path, err)
	}
	return parent, func() { unlockParentDirectory(parent); parent.Close() }, nil
}
