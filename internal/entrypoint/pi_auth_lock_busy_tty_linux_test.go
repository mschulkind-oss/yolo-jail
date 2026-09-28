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
	runPiLauncherAgainstBusyAuthLock(t, openTestPty(t), false)
}

// AT A TERMINAL, when no login exists the launcher logs in and asks for the token again. If a
// running pi holds the lock by then, that second call exits 75 too, and the launcher must
// answer it the same way: say so, leave auth.json in place and start pi, rather than abort
// under set -e.
func TestPiLauncherStartsPiWhenTheLockIsHeldAfterALoginAtATerminal(t *testing.T) {
	runPiLauncherAgainstBusyAuthLock(t, openTestPty(t), true)
}

// openTestPty returns the slave side of a fresh pty, skipping the test when none is available.
func openTestPty(t *testing.T) *os.File {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	t.Cleanup(func() { m.Close() })
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
	t.Cleanup(func() { slave.Close() })
	return slave
}
