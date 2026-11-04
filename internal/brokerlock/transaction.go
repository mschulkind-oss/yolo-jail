// Package brokerlock serializes scope authority and request/grant state per source.
package brokerlock

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// WithInspectionContext holds the existing transaction lock without creating or changing
// the store directory or lock file. Inspection fails closed when the lock is unavailable.
func WithInspectionContext(ctx context.Context, dir string, fn func() error) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return errors.New("broker directory is not real")
	}
	path := filepath.Join(dir, ".lock")
	f, err := os.OpenFile(path, os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err = f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return errors.New("store lock is not a regular file")
	}
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn()
}

// WithTransaction holds dir/.lock exclusively. Callbacks must not call another
// lock-taking API for this directory; reads and current-policy gates do not lock.
// All cooperating authority writers must use this same source directory.
func WithTransaction(dir string, fn func() error) error {
	return WithTransactionContext(context.Background(), dir, fn)
}

// WithTransactionContext bounds lock acquisition; it never interrupts a callback
// halfway through a durable state change. Gates check cancellation before spending.
func WithTransactionContext(ctx context.Context, dir string, fn func() error) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return errors.New("broker directory is not real")
	}
	if err = os.Chmod(dir, 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, statErr := f.Stat()
	if statErr != nil || !fi.Mode().IsRegular() {
		return errors.New("store lock is not a regular file")
	}
	if err = f.Chmod(0600); err != nil {
		return err
	}
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err = ctx.Err(); err != nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}
