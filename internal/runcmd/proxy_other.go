//go:build !linux

package runcmd

import (
	"os"
	"os/exec"
	"syscall"
)

// runWithProxy is the non-Linux fallback: a plain foreground exec (no pty
// proxy). The Linux path uses internal/ttyproxy; on darwin the container run
// path (podman machine / Apple Container) is not exercised by the nested-jail
// gate, and macos-user delegates to Python before reaching here. onStarted runs
// after spawn; onTerminate is not wired (no signal proxy in the fallback).
func runWithProxy(cmd []string, onStarted func(*os.Process), onTerminate func()) (int, error) {
	_ = onTerminate
	c := exec.Command(cmd[0], cmd[1:]...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Start(); err != nil {
		return 0, err
	}
	if onStarted != nil {
		go onStarted(c.Process)
	}
	err := c.Wait()
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
