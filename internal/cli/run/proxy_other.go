//go:build !linux

package run

import (
	"os"
	"os/exec"
	"syscall"
)

// runWithProxy is the non-Linux fallback: a plain foreground exec (no pty
// proxy). The Linux path uses internal/ttyproxy; on darwin the container run
// path (podman machine / Apple Container) is not exercised by the nested-jail
// gate. onStarted runs after spawn; onTerminate is not wired (no signal proxy in
// the fallback). The macos-user session no longer runs here: it was this
// function's last caller (through RunWithProxy), and with no signal arm a
// SIGTERM or a closed window ended the launcher past its teardown. It runs under
// its launch's own arm now (MacosUserArm.RunSession, macosuserarm.go).
//
// The Options param mirrors the Linux half's stage-hook seam; this fallback
// has only spawn and child-exit to mark, and a nil collector's Mark is a
// no-op, so the marks are unconditional here too.
func runWithProxy(cmd []string, onStarted func(*os.Process), onTerminate func(), o *Options) (int, error) {
	_ = onTerminate
	c := exec.Command(cmd[0], cmd[1:]...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Start(); err != nil {
		return 0, err
	}
	o.Perf.Mark("child.spawned")
	if onStarted != nil {
		go onStarted(c.Process)
	}
	err := c.Wait()
	o.Perf.Mark("child.exited")
	if err == nil {
		return 0, nil
	}
	if ee, ok := err.(*exec.ExitError); ok {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok {
			return ws.ExitStatus(), nil
		}
	}
	return 1, nil
}

// runArmedSession is the fallback's session run, a fresh launch's first session or an attach's:
// the same plain foreground exec, with the caller's arm (launchSignalArm) handed the client so its
// teardown ends it at its pid.
// There is no terminal to put back here: the runtime's own client sets its tty modes.
func runArmedSession(cmd []string, arm *launchSignalArm, o *Options) (int, error) {
	c := exec.Command(cmd[0], cmd[1:]...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if len(o.runtimeClientEnv) > 0 {
		c.Env = append(os.Environ(), o.runtimeClientEnv...)
	}
	if err := c.Start(); err != nil {
		return 0, err
	}
	o.Perf.Mark("child.spawned")
	arm.attach(plainSessionHandle{c})
	err := c.Wait()
	o.Perf.Mark("child.exited")
	return exitCodeOf(err), nil
}

// plainSessionHandle is the fallback's sessionHandle: nothing to restore, and the client to
// kill.
type plainSessionHandle struct{ c *exec.Cmd }

func (plainSessionHandle) Terminate() bool { return true }

func (h plainSessionHandle) Kill() { _ = h.c.Process.Kill() }
