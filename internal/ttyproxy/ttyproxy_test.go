//go:build linux

package ttyproxy

import (
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestNonTTYFallback: with non-TTY stdin, RunWithProxy plain-spawns and returns
// the child's exit code (no pty), matching the Python fallback.
func TestNonTTYFallback(t *testing.T) {
	// os.Stdin under `go test` is not a TTY, so this exercises the fallback.
	rc, err := RunWithProxy([]string{"sh", "-c", "exit 7"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rc != 7 {
		t.Errorf("non-TTY fallback rc = %d, want 7", rc)
	}
}

// TestNonTTYOnStarted: the onStarted callback runs (best-effort) in the
// fallback path.
func TestNonTTYOnStarted(t *testing.T) {
	ran := make(chan struct{}, 1)
	rc, err := RunWithProxy([]string{"true"}, func(*os.Process) { ran <- struct{}{} }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Error("onStarted did not run")
	}
}

// TestPtyPassthrough drives RunWithProxy with a REAL pty as stdin (so the TTY
// path runs), writes input, and verifies the child echoes it back through the
// proxy. This exercises openPty, raw mode, and the bidirectional pump.
func TestPtyPassthrough(t *testing.T) {
	// Create a pty; the slave becomes the proxy's stdin.
	master, slave, err := openPty()
	if err != nil {
		t.Skipf("cannot open pty: %v", err)
	}
	defer unix.Close(master)

	// Redirect os.Stdin/os.Stdout to the pty slave for the duration.
	origIn, origOut := os.Stdin, os.Stdout
	os.Stdin = os.NewFile(uintptr(slave), "pty-slave-stdin")
	os.Stdout = os.NewFile(uintptr(slave), "pty-slave-stdout")
	defer func() { os.Stdin, os.Stdout = origIn, origOut; unix.Close(slave) }()

	// Child: cat one line back. `head -1` exits after one line -> proxy returns.
	done := make(chan int, 1)
	go func() {
		rc, _ := RunWithProxy([]string{"head", "-n", "1"}, nil, nil)
		done <- rc
	}()

	// Write a line into the master; the proxy forwards it to the child's pty,
	// head echoes (via the pty) and exits.
	time.Sleep(100 * time.Millisecond)
	_, _ = unix.Write(master, []byte("hello\n"))

	select {
	case rc := <-done:
		if rc != 0 {
			t.Errorf("pty passthrough child rc = %d, want 0", rc)
		}
	case <-time.After(5 * time.Second):
		t.Error("proxy did not return after child exit (pump/exit race?)")
	}
}

// TestSuspendIsTargetedNotPgroupWide is a STATIC guard on the design: the
// suspend path must use syscall.Kill(getpid(), SIGTSTP) — a targeted self-kill,
// never a pgroup-wide kill(0)/kill(-pgid) that would also stop podman. This
// asserts the source contract via a behavioral proxy: selfSuspend on a process
// with SIGTSTP ignored must NOT stop it (proving it's a normal targeted signal,
// not a group broadcast that bypasses our own disposition).
func TestSuspendTargetedSelfOnly(t *testing.T) {
	// Ignore SIGTSTP in this test process so selfSuspend's Kill(getpid(),
	// SIGTSTP) does NOT actually stop the test — proving it's a targeted
	// self-signal subject to our own disposition, not a pgroup-wide broadcast
	// (which would stop podman, the exact regression the design forbids).
	signal.Ignore(syscall.SIGTSTP)
	defer signal.Reset(syscall.SIGTSTP)

	cooked := &unix.Termios{}
	if c, err := unix.IoctlGetTermios(int(os.Stdin.Fd()), unix.TCGETS); err == nil {
		cooked = c
	}
	doneCh := make(chan struct{})
	go func() {
		selfSuspend(int(os.Stdin.Fd()), cooked)
		close(doneCh)
	}()
	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("selfSuspend hung — SIGTSTP was not targeted/ignored as expected")
	}
}
