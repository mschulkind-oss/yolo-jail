//go:build linux

package ttyproxy

import (
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// TestSuspendStopIsDirectedAtTheCallingThread pins every ^Z stop site to a THREAD-directed
// SIGTSTP aimed at the thread making the call.
//
// A process-directed kill(getpid(), SIGTSTP) is taken by the thread-group leader whenever
// the leader does not block it, and the Go thread that raised it returns from kill() and
// keeps running until the leader's group stop reaches it. That thread then runs the
// "SIGTSTP was ignored" branch of suspendAfterReset and puts the host terminal back in raw
// mode, so the proxy ends up stopped with the user's shell left raw (CI run 41138698250,
// TestSuspendResumePreservesOutputQueueOrder: stopped, but no ICANON/ECHO/OPOST). A signal
// aimed at the calling thread is acted on before that thread returns to user code, so the
// code after the raise runs only once the process was continued, or the signal ignored.
//
// The witness is the kernel's per-thread pending set: with SIGTSTP blocked on this thread
// (and ignored, so nothing stops), a thread-directed raise is held in this thread's SigPnd,
// while a process-directed one is either dropped (the leader ignores it) or held in the
// shared ShdPnd. No timing is involved.
func TestSuspendStopIsDirectedAtTheCallingThread(t *testing.T) {
	sites := []struct {
		name  string
		raise func(t *testing.T, slave int, cooked *unix.Termios)
	}{
		{"selfSuspend", func(t *testing.T, slave int, cooked *unix.Termios) {
			selfSuspend(slave, cooked)
		}},
		{"suspendAfterReset-without-session", func(t *testing.T, slave int, cooked *unix.Termios) {
			if _, err := suspendAfterReset(slave, cooked, nil, nil); err != nil {
				t.Fatalf("suspendAfterReset: %v", err)
			}
		}},
		{"suspendAfterReset-with-session", func(t *testing.T, slave int, cooked *unix.Termios) {
			sess := newSession(slave, cooked, nil, nil)
			sess.ownsTTY = func(int) bool { return true }
			suspended, err := suspendAfterReset(slave, cooked, sess, nil)
			if err != nil || !suspended {
				t.Fatalf("suspendAfterReset = %v, %v; want the stop attempted", suspended, err)
			}
		}},
	}
	for _, site := range sites {
		t.Run(site.name, func(t *testing.T) {
			master, slave, err := openPty()
			if err != nil {
				t.Skipf("cannot open pty: %v", err)
			}
			defer unix.Close(master)
			defer unix.Close(slave)
			cooked := mustGetTermios(t, slave)
			oldStdout, oldStderr := terminalStdoutFD, terminalStderrFD
			terminalStdoutFD, terminalStderrFD = -1, -1
			defer func() { terminalStdoutFD, terminalStderrFD = oldStdout, oldStderr }()

			signal.Ignore(syscall.SIGTSTP)
			defer signal.Reset(syscall.SIGTSTP)
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			var tstp, previous unix.Sigset_t
			tstp.Val[0] = 1 << (uint(syscall.SIGTSTP) - 1)
			if err := unix.PthreadSigmask(unix.SIG_BLOCK, &tstp, &previous); err != nil {
				t.Fatal(err)
			}
			// Unblocking delivers the held signal to the SIG_IGN disposition, which drops it.
			defer func() { _ = unix.PthreadSigmask(unix.SIG_SETMASK, &previous, nil) }()

			site.raise(t, slave, cooked)

			threadPending, sharedPending := pendingSignalMasks(t)
			bit := uint64(1) << (uint(syscall.SIGTSTP) - 1)
			if threadPending&bit == 0 {
				t.Fatalf("SIGTSTP is not pending on the calling thread (SigPnd=%#x ShdPnd=%#x): the stop was not aimed at the thread that raised it", threadPending, sharedPending)
			}
			if sharedPending&bit != 0 {
				t.Fatalf("SIGTSTP is pending process-wide (ShdPnd=%#x): any thread may take the stop while the raiser runs on", sharedPending)
			}
		})
	}
}

func pendingSignalMasks(t *testing.T) (thread, shared uint64) {
	t.Helper()
	status, err := os.ReadFile("/proc/thread-self/status")
	if err != nil {
		t.Skipf("cannot read this thread's status: %v", err)
	}
	var sawThread, sawShared bool
	for _, line := range strings.Split(string(status), "\n") {
		field, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch field {
		case "SigPnd":
			thread, err = strconv.ParseUint(strings.TrimSpace(value), 16, 64)
			sawThread = err == nil
		case "ShdPnd":
			shared, err = strconv.ParseUint(strings.TrimSpace(value), 16, 64)
			sawShared = err == nil
		}
	}
	if !sawThread || !sawShared {
		t.Fatalf("this thread's status has no SigPnd/ShdPnd: %q", status)
	}
	return thread, shared
}
