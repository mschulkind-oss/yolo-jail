//go:build unix

package supervisor

import (
	"os/exec"
	"syscall"
	"time"
)

// confine puts the child in its own process group and kills the whole group when its context
// ends, so a grandchild cannot outlive the queue's shutdown.
func confine(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	platformAttr(cmd.SysProcAttr)
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 2 * time.Second
}
