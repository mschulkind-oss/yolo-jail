//go:build linux

package ioprio

import "syscall"

// whoProcess is IOPRIO_WHO_PROCESS. With who 0 it names the CALLING THREAD, and with a
// tid it names that thread: an I/O priority belongs to one thread, not to a process.
const whoProcess = 1

// SetCurrentThread sets the calling OS thread's I/O priority to the raw value v.
//
// The caller must hold the thread (runtime.LockOSThread) for the set to mean anything: an
// unpinned goroutine sets whichever thread it happened to be scheduled on, and a later
// child started from another goroutine does not inherit it (docs/design/io-priority.md
// §3.1, measured).
func SetCurrentThread(v int) error {
	_, _, errno := syscall.RawSyscall(syscall.SYS_IOPRIO_SET, whoProcess, 0, uintptr(v))
	if errno != 0 {
		return errno
	}
	return nil
}

// GetThread reads thread tid's raw I/O priority; tid 0 is the calling thread.
func GetThread(tid int) (int, error) {
	r, _, errno := syscall.RawSyscall(syscall.SYS_IOPRIO_GET, whoProcess, uintptr(tid), 0)
	if errno != 0 {
		return 0, errno
	}
	return int(r), nil
}
