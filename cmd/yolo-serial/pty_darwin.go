//go:build darwin

package main

import (
	"bytes"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openPty is posix_openpt + grantpt + unlockpt + ptsname, spelled as the darwin ioctls libc's
// own versions issue: open the /dev/ptmx clone device, TIOCPTYGRANT to chown and chmod the
// slave to the caller, TIOCPTYUNLK to unlock it, and TIOCPTYGNAME for its /dev/ttysNNN name.
//
// It exists because a macos-user guest runs this client inside its Seatbelt sandbox
// (macosuser.GuestClients), where `yolo-serial pty` allocates the virtual PTY a serial tool
// opens. pty_linux.go is the Linux spelling of the same four steps.
//
// The name comes from the kernel rather than from the master's device number: TIOCPTYGNAME
// writes the slave's path into a 128-byte buffer (sys/ttycom.h, 128 being the size the ioctl
// number encodes), which is the one answer that cannot disagree with the device it names.
func openPty() (*os.File, string, error) {
	mFd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, "", fmt.Errorf("open /dev/ptmx: %w", err)
	}
	if err := unix.IoctlSetInt(mFd, unix.TIOCPTYGRANT, 0); err != nil {
		unix.Close(mFd)
		return nil, "", fmt.Errorf("grantpt: %w", err)
	}
	if err := unix.IoctlSetInt(mFd, unix.TIOCPTYUNLK, 0); err != nil {
		unix.Close(mFd)
		return nil, "", fmt.Errorf("unlockpt: %w", err)
	}
	var name [128]byte
	//lint:ignore SA1019 TIOCPTYGNAME fills a 128-byte out buffer, and x/sys/unix exports no darwin wrapper for an out-buffer ioctl, so the raw SYS_IOCTL is the only spelling
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(mFd), uintptr(unix.TIOCPTYGNAME), uintptr(unsafe.Pointer(&name[0]))); e != 0 {
		unix.Close(mFd)
		return nil, "", fmt.Errorf("ptsname: %w", e)
	}
	n := bytes.IndexByte(name[:], 0)
	if n < 0 {
		n = len(name)
	}
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
	return os.NewFile(uintptr(mFd), "/dev/ptmx"), string(name[:n]), nil
}
