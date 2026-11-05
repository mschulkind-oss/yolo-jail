//go:build darwin

package callbroker

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func ownedLeaderInfo(pid int) ([]unix.KinfoProc, error) {
	rows, err := unix.SysctlKinfoProcSlice("kern.proc.pid", pid)
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 || int(rows[0].Proc.P_pid) != pid {
		return nil, fmt.Errorf("cannot observe owned call broker leader PID %d", pid)
	}
	return rows, nil
}

func ownedLeaderExited(pid int) (bool, error) {
	rows, err := ownedLeaderInfo(pid)
	if err != nil {
		return false, err
	}
	return rows[0].Proc.P_stat == 5 || rows[0].Proc.P_stat == 6, nil
}

func ownedLeaderIdentity(pid int) (string, error) {
	rows, err := ownedLeaderInfo(pid)
	if err != nil {
		return "", err
	}
	start := rows[0].Proc.P_starttime
	return fmt.Sprintf("%d.%06d Darwin start time", start.Sec, start.Usec), nil
}
