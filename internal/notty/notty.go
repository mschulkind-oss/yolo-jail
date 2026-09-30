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
package notty

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
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

// forwarded is the set of signals Run passes on to the child's process group.
var forwarded = []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP}

// Run prepares c (Prepare), starts it, forwards the signals above to its process group until it
// exits, and returns c.Wait's error: nil on a zero exit, an *exec.ExitError otherwise.
func Run(c *exec.Cmd) error {
	Prepare(c)
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, forwarded...)
	defer signal.Stop(sigs)
	if err := c.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-sigs:
				if sig, ok := s.(syscall.Signal); ok {
					// The child leads its own session, so its pid is its process group.
					_ = syscall.Kill(-c.Process.Pid, sig)
				}
			case <-done:
				return
			}
		}
	}()
	err := c.Wait()
	close(done)
	return err
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
