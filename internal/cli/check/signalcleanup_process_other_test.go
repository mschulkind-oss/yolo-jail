//go:build !darwin && !linux

package check

import (
	"errors"
	"os/exec"
	"time"
)

func configureSignalCleanupOwnedGroup(*exec.Cmd) {}

func verifySignalCleanupOwnedGroup(*exec.Cmd) error { return nil }

func killSignalCleanupOwnedGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return errors.New("signal cleanup child was not started")
	}
	return cmd.Process.Kill()
}

func waitSignalCleanupOwnedGroupExit(cmd *exec.Cmd, _ time.Duration) bool {
	return cmd.ProcessState != nil
}
