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

// TestCtrlCTerminatesProxy drives the real pump with a real pty. It pins the
// raw-mode Ctrl-C call site: the byte is consumed by the proxy and triggers a
// targeted self-interrupt rather than reaching the runtime's pty.
// TestCtrlCReachesTheJail pins the 2026-09-19 ruling: ^C is forwarded to the pty like
// any other byte, so the JAIL's line discipline decides what it means.
//
// It used to be intercepted and turned into a targeted SIGINT at the proxy, which made
// Ctrl-C mean "quit the launcher" — measured on a real host, pressing it at a jail's bash
// prompt to clear the line tore the whole session down. Forwarding restores the ordinary
// `podman exec -it` contract.
//
// The assertion is that the byte ARRIVES, which is the whole behaviour change: the child
// here is a `cat` on a raw pty, so a delivered 0x03 comes back out rather than raising a
// signal, and reading it back proves the proxy passed it through instead of eating it.
func TestCtrlCReachesTheJail(t *testing.T) {
	// Same plumbing as TestPtyPassthrough, which is the proven shape: a pty whose
	// slave stands in for os.Stdin, and the real RunWithProxy on top of it. Driving
	// proxyLoop by hand would pin the loop while leaving RunWithProxy free to
	// reintroduce an interception above it.
	master, slave, err := openPty()
	if err != nil {
		t.Skipf("cannot open pty: %v", err)
	}
	defer unix.Close(master)

	origIn, origOut := os.Stdin, os.Stdout
	os.Stdin = os.NewFile(uintptr(slave), "pty-slave-stdin")
	os.Stdout = os.NewFile(uintptr(slave), "pty-slave-stdout")
	defer func() { os.Stdin, os.Stdout = origIn, origOut; unix.Close(slave) }()

	// WHAT THIS CAN AND CANNOT ASSERT, stated because two earlier drafts asserted the
	// wrong thing.
	//
	// In production the chain is: proxy forwards 0x03 → the child (`podman exec -it`)
	// reads it as stdin data → podman relays it over the exec stream → the CONTAINER's
	// pty line discipline raises SIGINT at bash. The signal is raised inside the jail,
	// by a pty this package never sees. A unit test has no container, and the proxy
	// deliberately does NO Setsid (it broke `podman -it`), so the proxy's own pty has no
	// session and no foreground process group — nothing here will ever raise a SIGINT.
	//
	// So what is pinned is DELIVERY, which is the entire behaviour change: the byte
	// reaches the child instead of being eaten. The child puts its own pty in raw mode
	// first, because a cooked pty returns nothing to a reader until a newline — a draft
	// without `stty raw` failed on a PLAIN byte too, which is what proved the plumbing
	// wrong rather than the fix. If this test ever fails, send an ordinary byte first.
	done := make(chan int, 1)
	go func() {
		rc, _ := RunWithProxy([]string{"sh", "-c",
			"stty raw -echo; dd bs=1 count=1 of=/dev/null 2>/dev/null; exit 42"}, nil, nil)
		done <- rc
	}()

	time.Sleep(300 * time.Millisecond)
	if _, err := unix.Write(master, []byte{interruptByte}); err != nil {
		t.Fatal(err)
	}

	select {
	case rc := <-done:
		if rc != 42 {
			t.Errorf("child rc = %d, want 42 — it never read the ^C byte", rc)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("^C never reached the child: the proxy ate it. That is the pre-2026-09-19 " +
			"behaviour, where Ctrl-C quit the launcher instead of reaching the jail — at a " +
			"bash prompt it tore the session down rather than clearing the line.")
	}
}

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

// TestSelfSuspendResetsTerminal verifies that selfSuspend writes terminal reset
// escape sequences (including cursor show \x1b[?25h) to the terminal so the
// host shell prompt does not lose its cursor.
func TestSelfSuspendResetsTerminal(t *testing.T) {
	signal.Ignore(syscall.SIGTSTP)
	defer signal.Reset(syscall.SIGTSTP)

	master, slave, err := openPty()
	if err != nil {
		t.Skipf("cannot open pty: %v", err)
	}
	defer unix.Close(master)
	defer unix.Close(slave)

	cooked, err := unix.IoctlGetTermios(slave, unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	setRaw(slave, cooked)

	selfSuspend(slave, cooked)

	buf := make([]byte, 256)
	// Set read deadline so test does not hang if nothing was written.
	if err := unix.SetNonblock(master, true); err != nil {
		t.Fatal(err)
	}
	n, err := unix.Read(master, buf)
	if err != nil {
		t.Fatalf("reading reset sequence from master: %v", err)
	}
	got := string(buf[:n])
	if !strings.Contains(got, "\x1b[?25h") {
		t.Errorf("selfSuspend did not emit cursor show sequence, got %q", got)
	}
	if !strings.Contains(got, termReset) {
		t.Errorf("selfSuspend output = %q, want containing %q", got, termReset)
	}
}

// TestRestoreTerminalResetsTerminalModes asserts that restoreTerminal restores
// cooked termios and emits termReset (cursor visible, mouse off, normal modes).
func TestRestoreTerminalResetsTerminalModes(t *testing.T) {
	master, slave, err := openPty()
	if err != nil {
		t.Skipf("cannot open pty: %v", err)
	}
	defer unix.Close(master)
	defer unix.Close(slave)

	cooked, err := unix.IoctlGetTermios(slave, unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	setRaw(slave, cooked)

	restoreTerminal(slave, cooked)

	// Verify termios restored to cooked.
	current, err := unix.IoctlGetTermios(slave, unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	if current.Lflag&unix.ICANON == 0 {
		t.Error("restoreTerminal did not restore canonical mode (ICANON)")
	}

	// Verify termReset was written to the terminal.
	buf := make([]byte, 256)
	if err := unix.SetNonblock(master, true); err != nil {
		t.Fatal(err)
	}
	n, err := unix.Read(master, buf)
	if err != nil {
		t.Fatalf("reading reset sequence from master: %v", err)
	}
	got := string(buf[:n])
	if !strings.Contains(got, termReset) {
		t.Errorf("restoreTerminal output = %q, want %q", got, termReset)
	}
}

// TestResetTerminalFdSelection asserts that resetTerminal prioritizes stdout,
// falls back to stderr, then falls back to inFd, and writes nothing if none is a tty.
func TestResetTerminalFdSelection(t *testing.T) {
	origStdout, origStderr := os.Stdout, os.Stderr
	defer func() { os.Stdout, os.Stderr = origStdout, origStderr }()

	t.Run("writes to stdout when stdout is tty", func(t *testing.T) {
		m, s, err := openPty()
		if err != nil {
			t.Skip(err)
		}
		defer unix.Close(m)
		defer unix.Close(s)

		os.Stdout = os.NewFile(uintptr(s), "stdout")
		os.Stderr = origStderr
		_ = unix.SetNonblock(m, true)

		resetTerminal(-1)

		buf := make([]byte, 256)
		n, err := unix.Read(m, buf)
		if err != nil {
			t.Fatalf("read from stdout master: %v", err)
		}
		if string(buf[:n]) != termReset {
			t.Errorf("got %q, want %q", string(buf[:n]), termReset)
		}
	})

	t.Run("writes to stderr when stdout is not tty but stderr is", func(t *testing.T) {
		m, s, err := openPty()
		if err != nil {
			t.Skip(err)
		}
		defer unix.Close(m)
		defer unix.Close(s)

		os.Stdout = origStdout // non-tty under go test
		os.Stderr = os.NewFile(uintptr(s), "stderr")
		_ = unix.SetNonblock(m, true)

		resetTerminal(-1)

		buf := make([]byte, 256)
		n, err := unix.Read(m, buf)
		if err != nil {
			t.Fatalf("read from stderr master: %v", err)
		}
		if string(buf[:n]) != termReset {
			t.Errorf("got %q, want %q", string(buf[:n]), termReset)
		}
	})

	t.Run("writes to inFd when neither stdout nor stderr is tty", func(t *testing.T) {
		m, s, err := openPty()
		if err != nil {
			t.Skip(err)
		}
		defer unix.Close(m)
		defer unix.Close(s)

		os.Stdout = origStdout
		os.Stderr = origStderr
		_ = unix.SetNonblock(m, true)

		resetTerminal(s)

		buf := make([]byte, 256)
		n, err := unix.Read(m, buf)
		if err != nil {
			t.Fatalf("read from inFd master: %v", err)
		}
		if string(buf[:n]) != termReset {
			t.Errorf("got %q, want %q", string(buf[:n]), termReset)
		}
	})

	t.Run("writes nothing when no fd is a tty", func(t *testing.T) {
		os.Stdout = origStdout
		os.Stderr = origStderr
		resetTerminal(-1)
	})
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
