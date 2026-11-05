//go:build darwin

package run

import (
	"strconv"

	"golang.org/x/sys/unix"
)

// Ask for this one PID, never scan other processes or signal it. An unreadable
// process birth is unknown, not proof that an old start record is current.
func keeperProcessBirth(pid int) string {
	if pid <= 0 {
		return ""
	}
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp == nil || int(kp.Proc.P_pid) != pid || kp.Proc.P_stat == 5 { // SZOMB
		return ""
	}
	sec, nsec := kp.Proc.P_starttime.Unix()
	if sec <= 0 {
		return ""
	}
	return strconv.FormatInt(sec, 10) + ":" + strconv.FormatInt(nsec, 10)
}
