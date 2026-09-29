//go:build !linux

package ghbroker

import "syscall"

// childProcAttr puts each gh in a process group of its own, so a timeout or Shutdown ends
// what it spawned too. There is no parent-death signal here: a gh whose broker was killed
// outright runs until its next write fails (the broker's end of its pipes is gone) or it
// finishes.
func childProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
