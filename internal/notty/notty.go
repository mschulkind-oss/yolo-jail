// Package notty runs a command with NO CONTROLLING TERMINAL and /dev/null on its stdin, which is
// how yolo runs every vendor installer (docs/design/provisioner-sets.md PS-D1).
//
// # Why both halves
//
// A vendor installer that has to survive `curl | sh` reads its questions from /dev/tty rather
// than from stdin, so a /dev/null stdin alone does not stop it asking: codex's installer asked
// `Start Codex now? [y/N]` on /dev/tty mid-probe, measured on a Mac
// (docs/plans/runbooks/mac-provisioner-measurements.md). /dev/tty is the process's controlling
// terminal, so the child is started in a session of its own (setsid), which has none: opening
// /dev/tty then fails, and a prompt either takes its default or fails. The accepted cost is
// PS-D1's: an installer that needs an answer fails instead of asking.
//
// Its stdout and stderr are the caller's, so the installer's progress still reaches whoever
// is watching; writing to a terminal does not need it to be the controlling one.
//
// # Signals
//
// A child in its own session is out of the terminal's foreground process group, so a Ctrl-C at
// the terminal no longer reaches it. Run forwards the interrupt, terminate and hangup signals
// this process receives to the child's process group while it waits, so stopping yolo still
// stops the installer rather than leaving it running unattended.
//
// AND A STOP IS STILL A STOP. This process catches those signals to forward them, so it no longer
// dies of them, and a caller that reads only an exit status sees "the installer failed" where it
// used to see "I was stopped". Run therefore reports a child that died of a signal it forwarded as
// Stopped. A caller that runs more work afterwards stops there (the host's dependency gate), and a
// process whose only job is the child (`yolo internal no-terminal`) ends with WrapperExit, which
// dies of the same signal, because the shell that started it tells the two apart by exactly that:
// bash waiting on a command when a Ctrl-C arrives abandons its script only when the command died of
// SIGINT, and goes on to the next line when the command exited, even with status 130.
package notty

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

// Prepare sets c to start with no controlling terminal and a /dev/null stdin. It keeps whatever
// else c's SysProcAttr says. Run calls it; a caller that must Start c itself calls it first.
func Prepare(c *exec.Cmd) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.Setsid = true
	// Setctty would give the new session a terminal back; a caller's leftover must not.
	c.SysProcAttr.Setctty = false
	// A nil Stdin is /dev/null (os/exec), and a caller's stdin must not survive either.
	c.Stdin = nil
}

// forwarded is the set of signals Run passes on to the child's process group. Each one's Go
// default is to end the process, which WrapperExit relies on to die of it.
var forwarded = []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP}

// Stopped is Run's error for a child that died of a signal Run received and forwarded to it: what
// stopped this process stopped the child too. It wraps the child's *exec.ExitError, so ExitCode and
// errors.As read through it.
type Stopped struct {
	// Signal is the signal the child died of, which this process received.
	Signal syscall.Signal
	// Err is the child's exit error.
	Err error
}

func (s *Stopped) Error() string { return s.Err.Error() }
func (s *Stopped) Unwrap() error { return s.Err }

// Run prepares c (Prepare), starts it, forwards the signals above to its process group until it
// exits, and returns c.Wait's error: nil on a zero exit, a *Stopped when the child died of a
// signal Run forwarded, and otherwise the *exec.ExitError.
func Run(c *exec.Cmd) error {
	Prepare(c)
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, forwarded...)
	defer signal.Stop(sigs)
	if err := c.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	sent := make(chan map[syscall.Signal]bool, 1)
	go func() {
		seen := map[syscall.Signal]bool{}
		for {
			select {
			case s := <-sigs:
				if sig, ok := s.(syscall.Signal); ok {
					// Recorded BEFORE it is sent, so a child that dies of it is always found here.
					seen[sig] = true
					// The child leads its own session, so its pid is its process group.
					_ = syscall.Kill(-c.Process.Pid, sig)
				}
			case <-done:
				sent <- seen
				return
			}
		}
	}()
	err := c.Wait()
	close(done)
	seen := <-sent
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() && seen[ws.Signal()] {
			return &Stopped{Signal: ws.Signal(), Err: err}
		}
	}
	return err
}

// WrapperExit is how a process whose whole job is running the child ends after Run returned err
// (`yolo internal no-terminal`, the jail launcher's installer run). For a Stopped child it does not
// return: it dies of the same signal, so the shell that started it sees what it would have seen
// running the child itself (the package comment says why that is the difference between a Ctrl-C
// and "carry on"). Otherwise, or if the signal cannot end this process (one it was started with
// ignored), it returns ExitCode(err) for the caller to exit with.
func WrapperExit(err error) int {
	var st *Stopped
	if errors.As(err, &st) {
		// Every Notify for it is undone, so the runtime's default, ending the process by the
		// signal, is what the kill meets.
		signal.Reset(st.Signal)
		if kerr := syscall.Kill(os.Getpid(), st.Signal); kerr == nil {
			// Delivery to a multi-threaded process is asynchronous; it lands within this wait.
			time.Sleep(2 * time.Second)
		}
	}
	return ExitCode(err)
}

// ExitCode is the status a wrapper should exit with after Run returned err: 0 for nil, the
// child's own status for an exit, 128+N for a death by signal N (the shell's convention), and
// 127 when the command could not be started at all.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return ee.ExitCode()
	}
	return 127
}
