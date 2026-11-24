package integration

import (
	"os/exec"
	"syscall"
)

// inOwnGroup starts cmd as the leader of a process group of its own, so killGroup can end it
// and every process it started. A stand-in shell's `sleep` is the shell's child, not this test's:
// killing the shell alone leaves that sleep running past the test, and the TMPDIR leak gate fails
// the whole `just test-fast` run naming it (two `sleep 600.0`s and a `sleep 600` from this
// package, 2026-11-24).
func inOwnGroup(cmd *exec.Cmd) *exec.Cmd {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

// killGroup sends SIGKILL to the process group inOwnGroup gave cmd, the leader included.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
