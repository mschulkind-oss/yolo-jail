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
// gate, and macos-user takes its own native path before reaching here. onStarted
// runs after spawn; onTerminate is not wired (no signal proxy in the fallback).
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
