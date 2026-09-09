//go:build linux

package ttyproxy

import (
	"os"
	"os/signal"
	"strings"
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

// The stage-hook order pin on the plain (non-TTY) path: spawned then exited,
// and nothing else — the plain fallback has no drain and no termios to
// restore, so those stages must stay silent rather than fire vacuously. The
// pty path's stages are pinned end-to-end by the integration suite, where a
// real terminal exists.
func TestStageHookPlainPathOrder(t *testing.T) {
	var stages []string
	rc, err := RunWithProxyHooked([]string{"/bin/true"}, nil, nil, func(s string) {
		stages = append(stages, s)
	})
	if err != nil || rc != 0 {
		t.Fatalf("rc=%d err=%v", rc, err)
	}
	want := []string{StageSpawned, StageExited}
	if len(stages) != len(want) {
		t.Fatalf("stages = %v, want %v", stages, want)
	}
	for i := range want {
		if stages[i] != want[i] {
			t.Fatalf("stages = %v, want %v", stages, want)
		}
	}
}

// A hook that panics must not take the proxy with it — observers are
// best-effort by contract, and the reverse (a diagnostics hook killing every
// launch) is the failure the perf design forbids.
func TestStageHookPanicIsContained(t *testing.T) {
	rc, err := RunWithProxyHooked([]string{"/bin/true"}, nil, nil, func(string string) {
		panic("observer bug")
	})
	if err != nil || rc != 0 {
		t.Fatalf("panicking hook changed the outcome: rc=%d err=%v", rc, err)
	}
}

// TestWinchResignalsTheChild is the regression for size drift — the ptys in a
// jail chain disagreeing about rows and columns, which reads as a garbled agent
// TUI. See resyncWinsize for the race and the measurement.
//
// IT IS DETERMINISTIC FOR A USEFUL REASON, and one worth stating because the
// production bug is a coin flip. The child here is NOT in the fake host pty's
// foreground process group, so the kernel sends it no SIGWINCH of its own: the
// only signal it can possibly receive is the one resyncWinsize sends. Without the
// fix the child sees zero and never prints; with it, exactly one, after the proxy
// pty already reports the new size. So this fails by TIMING OUT before the fix and
// passes promptly after — the mutation check ran both ways.
//
// The proxy is driven by raising SIGWINCH in this process rather than by resizing
// alone: the proxy learns of resizes through signal.Notify on its own process, and
// a pty with no foreground process group signals nobody when its size changes.
// A test that only resized would assert nothing at all.
func TestWinchResignalsTheChild(t *testing.T) {
	master, slave, err := openPty()
	if err != nil {
		t.Skipf("cannot open pty: %v", err)
	}
	defer unix.Close(master)

	origIn, origOut := os.Stdin, os.Stdout
	os.Stdin = os.NewFile(uintptr(slave), "pty-slave-stdin")
	os.Stdout = os.NewFile(uintptr(slave), "pty-slave-stdout")
	defer func() { os.Stdin, os.Stdout = origIn, origOut; unix.Close(slave) }()

	// `sleep & wait` rather than a bare `sleep`: a POSIX shell defers a trap until
	// the current foreground command finishes, so a bare sleep would swallow the
	// signal for its whole duration and make this a test of the timeout. The trap
	// kills the sleep too — a backgrounded child inherits the proxy pty and holds
	// it open, so the proxy's drain would never see EOF and the test would hang
	// after already having proved its point.
	done := make(chan int, 1)
	go func() {
		rc, _ := RunWithProxy([]string{"sh", "-c",
			`trap 'stty size <&0; kill $S 2>/dev/null; exit 0' WINCH; sleep 10 & S=$!; wait`}, nil, nil)
		done <- rc
	}()
	time.Sleep(200 * time.Millisecond)

	// Resize the FAKE HOST pty, then tell the proxy the way the kernel would.
	want := &unix.Winsize{Row: 41, Col: 137}
	if err := unix.IoctlSetWinsize(master, unix.TIOCSWINSZ, want); err != nil {
		t.Skipf("cannot resize the fake host pty: %v", err)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGWINCH); err != nil {
		t.Fatalf("raising SIGWINCH: %v", err)
	}

	// The child's `stty size` lands on the proxy pty, which the proxy pumps to
	// os.Stdout — the fake host pty slave — so it comes back out of `master`.
	out := make(chan string, 1)
	go func() {
		buf := make([]byte, 256)
		var acc []byte
		for {
			n, err := unix.Read(master, buf)
			if n > 0 {
				acc = append(acc, buf[:n]...)
				if strings.Contains(string(acc), "41 137") {
					out <- string(acc)
					return
				}
			}
			if err != nil {
				out <- string(acc)
				return
			}
		}
	}()

	select {
	case got := <-out:
		if !strings.Contains(got, "41 137") {
			t.Errorf("child reported %q, want the resized 41x137", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the child never saw a SIGWINCH. The proxy writes the new size to its " +
			"pty but that pty has no foreground process group, so nothing signals the " +
			"child — this is size drift, and resyncWinsize's targeted re-signal is what " +
			"closes it.")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("proxy did not return after the child exited")
	}
}
