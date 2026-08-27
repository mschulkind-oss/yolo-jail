//go:build linux || darwin

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
	return os.NewFile(uintptr(mFd), "/dev/ptmx"), slavePath, nil
}
