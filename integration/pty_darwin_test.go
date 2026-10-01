//go:build darwin

package integration

// pty_darwin_test.go is openTestPty's darwin half; the Linux half is in
// attachskewrestart_linux_test.go, where it was first needed. It exists for the Apple Container
// job's detach-sequence and client-death measures (applecontainerkeeper_test.go), which type at
// a real terminal on the Mac. The repo vendors no pty library, so this is posix_openpt(3) by
// hand: grantpt, unlockpt and ptsname are the TIOCPTYGRANT, TIOCPTYUNLK and TIOCPTYGNAME ioctls
// on the master. TestOpenTestPtyCarriesBytesBothWays runs it in ci.yml's check-macos job on every
// push to main.

import (
	"bytes"
	"os"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openTestPty returns a pty pair, skipping when /dev/ptmx cannot be opened — a sandbox without
// it is not evidence either way. Once the master is open a failing ioctl is this function's own
// fault, so that fails the test rather than skipping it.
func openTestPty(t *testing.T) (master, slave *os.File) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	fd := m.Fd()
	if err := unix.IoctlSetInt(int(fd), unix.TIOCPTYGRANT, 0); err != nil {
		m.Close()
		t.Fatalf("grantpt on a machine that has /dev/ptmx: %v", err)
	}
	if err := unix.IoctlSetInt(int(fd), unix.TIOCPTYUNLK, 0); err != nil {
		m.Close()
		t.Fatalf("unlockpt on a machine that has /dev/ptmx: %v", err)
	}
	// TIOCPTYGNAME writes the slave's path, NUL-terminated, into a 128-byte buffer.
	var name [128]byte
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(unix.TIOCPTYGNAME),
		uintptr(unsafe.Pointer(&name[0]))); e != 0 {
		m.Close()
		t.Fatalf("ptsname on a machine that has /dev/ptmx: %v", e)
	}
	path := string(name[:])
	if i := bytes.IndexByte(name[:], 0); i >= 0 {
		path = string(name[:i])
	}
	s, err := os.OpenFile(path, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		m.Close()
		t.Fatalf("opening the slave %s: %v", path, err)
	}
	t.Cleanup(func() { s.Close(); m.Close() })
	return m, s
}
