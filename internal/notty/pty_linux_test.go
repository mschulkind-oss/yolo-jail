//go:build linux

package notty

import (
	"os"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openPty opens a pty pair, or skips when this machine has none to give.
func openPty(t *testing.T) (master, slave *os.File) {
	t.Helper()
	m, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no /dev/ptmx: %v", err)
	}
	var unlock int
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(m), unix.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		unix.Close(m)
		t.Skipf("unlockpt: %v", e)
	}
	n, err := unix.IoctlGetUint32(m, unix.TIOCGPTN)
	if err != nil {
		unix.Close(m)
		t.Skipf("ptsname: %v", err)
	}
	s, err := os.OpenFile("/dev/pts/"+itoa(n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		unix.Close(m)
		t.Skipf("open pts: %v", err)
	}
	master = os.NewFile(uintptr(m), "ptmx")
	t.Cleanup(func() { master.Close(); s.Close() })
	return master, s
}

func itoa(n uint32) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}
