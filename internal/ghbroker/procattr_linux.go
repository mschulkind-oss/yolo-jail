package ghbroker

import "syscall"

// childProcAttr puts each gh in a process group of its own, so a timeout or Shutdown ends
// what it spawned too, and has the kernel SIGKILL it if the broker dies first (SIGKILL,
// OOM), so no gh outlives the broker that was bounding it.
func childProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}
