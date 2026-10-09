//go:build darwin

package journald

import "golang.org/x/sys/unix"

// processOwner reports the effective uid of a live process, read from the kernel
// (kern.proc.pid), or false when the process is gone or unreadable.
func processOwner(pid int) (uint32, bool) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp == nil || int(kp.Proc.P_pid) != pid {
		return 0, false
	}
	return kp.Eproc.Ucred.Uid, true
}
