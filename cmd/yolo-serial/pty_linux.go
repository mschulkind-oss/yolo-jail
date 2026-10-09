//go:build linux

package main

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

func openPty() (*os.File, string, error) {
	mFd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, "", fmt.Errorf("open /dev/ptmx: %w", err)
	}

	var unlock int
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(mFd), unix.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		unix.Close(mFd)
		return nil, "", fmt.Errorf("unlockpt: %w", e)
	}

	var ptn uint32
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(mFd), unix.TIOCGPTN, uintptr(unsafe.Pointer(&ptn))); e != 0 {
		unix.Close(mFd)
		return nil, "", fmt.Errorf("ptsname: %w", e)
	}

	slavePath := fmt.Sprintf("/dev/pts/%d", ptn)
	// NON-BLOCKING, so the returned file is pollable and Close cancels a Read or Write parked on
	// it. runPty writes every bridge frame to this master, and that write cannot complete while
	// no serial tool has opened the slave (darwin blocks the first write until the slave opens;
	// Linux once the line's buffer is full). On a blocking descriptor Go cannot cancel it, the
	// SIGTERM handler's Close returned with the write still parked, SA_RESTART resumed it, and
	// `yolo-serial pty` outlived its kill (ptysignal_test.go).
	if err := unix.SetNonblock(mFd, true); err != nil {
		unix.Close(mFd)
		return nil, "", fmt.Errorf("set the master non-blocking: %w", err)
	}
	return os.NewFile(uintptr(mFd), "/dev/ptmx"), slavePath, nil
}
