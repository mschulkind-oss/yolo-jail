//go:build darwin

package journald

import (
	"time"

	"golang.org/x/sys/unix"
)

// processOwner reports the effective uid and start time of a live process, read from the
// kernel (kern.proc.pid), or false when the process is gone or unreadable.
func processOwner(pid int) (uint32, time.Time, bool) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp == nil || int(kp.Proc.P_pid) != pid {
		return 0, time.Time{}, false
	}
	st := kp.Proc.P_starttime
	return kp.Eproc.Ucred.Uid, time.Unix(int64(st.Sec), int64(st.Usec)*1000), true
}
