//go:build linux

package entrypoint

import (
	"os"
	"strconv"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// AT A TERMINAL a failed token call starts a browser login, so this is the case where treating
// a held pi lock as a missing login does the visible harm. With stdin on a pty the launcher must
// still make no login call. Linux-only for the pty, opened from /dev/ptmx as the other launcher
// tty tests do, since the repo vendors no pty library.
func TestPiLauncherDoesNotLogInAtATerminalWhenARunningPiHoldsItsAuthLock(t *testing.T) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	defer m.Close()
	var unlock int32
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, m.Fd(), unix.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		t.Skipf("unlockpt: %v", e)
	}
	n, err := unix.IoctlGetInt(int(m.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Skipf("ptsname: %v", err)
	}
	slave, err := os.OpenFile("/dev/pts/"+strconv.Itoa(n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("open slave: %v", err)
	}
	defer slave.Close()
	runPiLauncherAgainstBusyAuthLock(t, slave)
}
