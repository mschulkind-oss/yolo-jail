package run

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/mcpsignin"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func mcpCallbackStateRoot() string {
	return filepath.Join(paths.GlobalStorage(), "state", "mcp-callback")
}

func (o *Options) callbackRoot() string {
	if o.mcpCallbackRoot != "" {
		return o.mcpCallbackRoot
	}
	return mcpCallbackStateRoot()
}

// withCallbackLease serializes only one user's one port. An unsafe existing
// file/directory never grants ownership and is never repaired.
func (o *Options) withCallbackLease(port int, acquire bool, action func() error) error {
	root := o.callbackRoot()
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return os.ErrPermission
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) {
		return os.ErrPermission
	}
	path := filepath.Join(root, "port-"+strconv.Itoa(port)+".lock")
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return os.ErrPermission
	}
	stat, ok = info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) {
		return os.ErrPermission
	}
	if err := o.waitCallbackLease(fd, acquire); err != nil {
		return err
	}
	defer flockSyscall(fd, syscall.LOCK_UN)
	return action()
}

func writeCallbackJSON(path string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".callback-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(body); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// acquireCallbackLease holds the port and previous owner's lifecycle exclusion
// through absence proof and replacement. A pending launch with no runtime still
// owns its lease; unknown liveness or lifecycle contention never reclaims it.
func (o *Options) acquireCallbackLease(lease mcpsignin.CallbackLease) (string, string) {
	if o.mcpCallbackLeaseEvent != nil {
		o.mcpCallbackLeaseEvent("acquire", lease)
	}
	if !lease.Valid() {
		return "unavailable", ""
	}
	disposition, owner := "unavailable", ""
	err := o.withCallbackLease(lease.Port, true, func() error {
		previous, err := mcpsignin.ReadCallbackLease(o.callbackRoot(), lease.Port)
		if err == nil {
			release, acquired := o.callbackLifecycleExclusion(previous.Launch, lease.Launch)
			if !acquired {
				disposition, owner = "held", previous.Launch
				return nil
			}
			defer release()
			if previous == lease {
				disposition, owner = "publication pending", lease.Launch
				return nil
			}
			id, known := o.probeExistingContainer(previous.Launch, previous.Runtime, trackingProbeTimeout)
			if !known || id != "" {
				disposition, owner = "held", previous.Launch
				return nil
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := writeCallbackJSON(mcpsignin.CallbackLeasePath(o.callbackRoot(), lease.Port), lease); err != nil {
			return err
		}
		disposition, owner = "publication pending", lease.Launch
		return nil
	})
	if err != nil {
		return "unavailable", ""
	}
	return disposition, owner
}

// callbackLifecycleExclusion never waits for another owner's lifecycle while
// holding a port lock. For our own cname it retains the already-held descriptor
// and mutex instead of opening a second descriptor that would contend with it.
func (o *Options) callbackLifecycleExclusion(previous, next string) (func(), bool) {
	if previous == next && o.launchLock != nil {
		lock := o.launchLock
		lock.mu.Lock()
		if lock.closed || lock.path != launchLockPath(previous) || !callbackLifecycleFileSafe(lock.f, previous) || syscall.Flock(int(lock.f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil || !callbackLifecycleFileSafe(lock.f, previous) {
			lock.mu.Unlock()
			return nil, false
		}
		return lock.mu.Unlock, true
	}
	path := launchLockPath(previous)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, false
	}
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0644)
	if err != nil {
		return nil, false
	}
	file := os.NewFile(uintptr(fd), path)
	if !callbackLifecycleFileSafe(file, previous) || syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB) != nil || !callbackLifecycleFileSafe(file, previous) {
		_ = syscall.Flock(fd, syscall.LOCK_UN)
		file.Close()
		return nil, false
	}
	lock := &workspaceLock{f: file}
	return lock.Close, true
}

// callbackLifecycleFileSafe accepts the launch lock only when it is the regular file we own
// at its named path and no one but its owner can write it, so another account cannot hold or
// swap the gate. It does not demand one exact mode: acquireWorkspaceLock creates the file
// subject to the umask, so 0600 and 0640 are as safe as 0644.
func callbackLifecycleFileSafe(file *os.File, cname string) bool {
	if file == nil || file.Name() != launchLockPath(cname) {
		return false
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) {
		return false
	}
	named, err := os.Lstat(file.Name())
	return err == nil && named.Mode().IsRegular() && os.SameFile(info, named)
}

// releaseCallbackLease is compare-and-delete. Its caller proves runtime absence;
// a stale teardown cannot remove a different launch generation's lease.
func (o *Options) releaseCallbackLease(lease mcpsignin.CallbackLease) {
	if o.mcpCallbackLeaseEvent != nil {
		o.mcpCallbackLeaseEvent("release", lease)
	}
	if !lease.Valid() {
		return
	}
	_ = o.withCallbackLease(lease.Port, false, func() error {
		current, err := mcpsignin.ReadCallbackLease(o.callbackRoot(), lease.Port)
		if err != nil || current != lease {
			return err
		}
		return os.Remove(mcpsignin.CallbackLeasePath(o.callbackRoot(), lease.Port))
	})
}

func (o *Options) writeCallbackReceipt(generation string, rows []mcpsignin.Callback) error {
	path := mcpsignin.CallbackReceiptPath(o.callbackRoot(), generation)
	if path == "" {
		return os.ErrInvalid
	}
	if err := os.MkdirAll(o.callbackRoot(), 0700); err != nil {
		return err
	}
	info, err := os.Lstat(o.callbackRoot())
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return os.ErrPermission
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) {
		return os.ErrPermission
	}
	return writeCallbackJSON(path, mcpsignin.CallbackReceipt{Version: 1, Generation: generation, Callbacks: rows})
}

// This bound limits optional operation waiting, never lease lifetime or liveness.
const mcpCallbackLeaseWait = 2 * time.Second

func (o *Options) waitCallbackLease(fd int, acquire bool) error {
	now := time.Now
	if o.mcpCallbackLockNow != nil {
		now = o.mcpCallbackLockNow
	}
	wait := callbackLeaseWait
	if o.mcpCallbackLockWait != nil {
		wait = o.mcpCallbackLockWait
	}
	ctx := context.Background()
	if acquire && o.mcpCallbackLockContext != nil {
		ctx = o.mcpCallbackLockContext
	}
	deadline := now().Add(mcpCallbackLeaseWait)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if acquire && o.launchGuard != nil {
			o.launchGuard.mu.Lock()
			ended := o.launchGuard.ended
			o.launchGuard.mu.Unlock()
			if ended {
				return context.Canceled
			}
		}
		if !now().Before(deadline) {
			return context.DeadlineExceeded
		}
		err := flockSyscall(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			return err
		}
		delay := 20 * time.Millisecond
		if remaining := deadline.Sub(now()); remaining < delay {
			delay = remaining
		}
		if err := wait(ctx, delay); err != nil {
			return err
		}
	}
}

func callbackLeaseWait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
