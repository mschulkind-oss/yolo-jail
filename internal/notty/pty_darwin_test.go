//go:build darwin

package notty

import (
	"bytes"
	"os"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openPty opens a pty pair, or skips when this machine has none to give. The repo vendors no pty
// library, so this is posix_openpt(3) by hand: grantpt, unlockpt and ptsname are the
// TIOCPTYGRANT, TIOCPTYUNLK and TIOCPTYGNAME ioctls on the master, as in the integration
// package's darwin half (integration/pty_darwin_test.go), which check-macos runs on every push.
func openPty(t *testing.T) (master, slave *os.File) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no /dev/ptmx: %v", err)
	}
	fd := int(m.Fd())
	for _, req := range []struct {
		name string
		op   uint
	}{{"grantpt", unix.TIOCPTYGRANT}, {"unlockpt", unix.TIOCPTYUNLK}} {
		if err := unix.IoctlSetInt(fd, req.op, 0); err != nil {
			m.Close()
			t.Skipf("%s: %v", req.name, err)
		}
	}
	// TIOCPTYGNAME writes the slave's path, NUL-terminated, into a 128-byte buffer.
	var name [128]byte
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(unix.TIOCPTYGNAME),
		uintptr(unsafe.Pointer(&name[0]))); e != 0 {
		m.Close()
		t.Skipf("ptsname: %v", e)
	}
	path := string(name[:])
	if i := bytes.IndexByte(name[:], 0); i >= 0 {
		path = string(name[:i])
	}
	s, err := os.OpenFile(path, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		m.Close()
		t.Skipf("open %s: %v", path, err)
	}
	t.Cleanup(func() { s.Close(); m.Close() })
	return m, s
}
