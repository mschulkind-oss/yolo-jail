//go:build darwin || linux

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const stateLockName = "state.lock"

func openStateLock(dir string) (*os.File, error) {
	path := filepath.Join(dir, stateLockName)
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, fmt.Errorf("open fixture state lock: %w", err)
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = errors.New("fixture state lock must be a regular file")
	}
	if err == nil {
		err = f.Chmod(0600)
	}
	if err == nil {
		info, err = f.Stat()
	}
	if err == nil && info.Mode().Perm() != 0600 {
		err = errors.New("fixture state lock must be private")
	}
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func lockStateFile(f *os.File) error {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return errors.New("fixture state directory is already in use; stop its fixture or choose a new private directory")
		}
		return fmt.Errorf("lock fixture state directory: %w", err)
	}
	return nil
}

func validateInheritedStateLock(dir string, f *os.File) error {
	if f == nil {
		return errors.New("missing inherited fixture state lock")
	}
	fdInfo, err := f.Stat()
	if err != nil {
		return err
	}
	pathInfo, err := os.Lstat(filepath.Join(dir, stateLockName))
	if err != nil {
		return err
	}
	if !fdInfo.Mode().IsRegular() || !os.SameFile(fdInfo, pathInfo) || fdInfo.Mode().Perm() != 0600 {
		return errors.New("inherited fixture state lock is not the private lock for this directory")
	}
	if err := lockStateFile(f); err != nil {
		return err
	}
	return nil
}
