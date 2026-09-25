//go:build linux

package provision

// provision_tty_linux_test.go runs the stage script with a REAL terminal on stdin, because
// the one branch a non-interactive test cannot reach is the one the refusal has to beat: at
// a tty, an ordinary failure asks "continue anyway?", and a refusal must not.
//
// Linux-only for the pty: it opens /dev/ptmx directly (the lingerprobe tests' shape), since
// the repo vendors no pty library and adds no dependency for a test.

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openPty returns a pty pair, skipping when the machine cannot allocate one — a sandbox
// without /dev/ptmx is not evidence either way.
func openPty(t *testing.T) (master, slave *os.File) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	var unlock int32
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, m.Fd(), unix.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		m.Close()
		t.Skipf("unlockpt: %v", e)
	}
	n, err := unix.IoctlGetInt(int(m.Fd()), unix.TIOCGPTN)
	if err != nil {
		m.Close()
		t.Skipf("ptsname: %v", err)
	}
	s, err := os.OpenFile("/dev/pts/"+strconv.Itoa(n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		m.Close()
		t.Skipf("open slave: %v", err)
	}
	t.Cleanup(func() { s.Close(); m.Close() })
	return m, s
}

// AT A TERMINAL, a refusal exits with RefusedStatus and never prompts — and the control
// case proves the pty really made `[ -t 0 ]` true, so "no prompt" is not vacuous.
func TestARefusalAtATerminalDoesNotOfferToContinue(t *testing.T) {
	// Control: an ordinary failure at a tty prompts, reads the answer, and on `y` continues.
	master, slave := openPty(t)
	if _, err := master.Write([]byte("y\n")); err != nil {
		t.Fatal(err)
	}
	rc, stdout, stderr, _ := runStage(t, Setup("exit 1"), slave)
	if !strings.Contains(stderr, "continue anyway?") {
		t.Fatalf("the control case never prompted, so the pty did not make stdin a terminal and "+
			"the refusal case below would prove nothing (rc %d):\n%s", rc, stderr)
	}
	if rc != 0 || !strings.Contains(stdout, "TARGET-REACHED") {
		t.Errorf("control: answering y must continue to the target: rc %d, stdout %q", rc, stdout)
	}

	// The refusal, at the same kind of terminal: no prompt, status passed through, no target.
	// The same `y` is waiting on this pty too, so a regressed script that DID prompt reads it,
	// continues, and fails the assertions below at once — rather than blocking on `read` until
	// go test's global timeout, which is how this case failed before the answer was written.
	master2, slave2 := openPty(t)
	if _, err := master2.Write([]byte("y\n")); err != nil {
		t.Fatal(err)
	}
	rc, stdout, stderr, _ = runStage(t, Setup("exit "+strconv.Itoa(RefusedStatus)), slave2)
	if strings.Contains(stderr, "continue anyway?") {
		t.Errorf("a refusal at a terminal offered to continue — OQ-AR3 rules out any escape hatch:\n%s", stderr)
	}
	if rc != RefusedStatus {
		t.Errorf("rc = %d, want RefusedStatus (%d)", rc, RefusedStatus)
	}
	if strings.Contains(stdout, "TARGET-REACHED") {
		t.Errorf("the target ran after a refusal at a terminal:\n%s", stdout)
	}
}
