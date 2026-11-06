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
	return darwinProcExited(rows[0]), nil
}

func darwinProcExited(row unix.KinfoProc) bool {
	return row.Proc.P_stat == 5 || row.Proc.P_stat == 6
}

// ownedGroupHasNoLiveMemberOS lists process group pgid and reports whether
// every member has exited. kill(-pgid) answers EPERM on darwin when the group
// holds only zombies, so this listing is what tells that apart from a live
// member the signal could not reach.
func ownedGroupHasNoLiveMemberOS(pgid int) (bool, error) {
	rows, err := unix.SysctlKinfoProcSlice("kern.proc.pgrp", pgid)
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if int(row.Eproc.Pgid) == pgid && !darwinProcExited(row) {
			return false, nil
		}
	}
	return true, nil
}

func ownedLeaderIdentity(pid int) (string, error) {
	rows, err := ownedLeaderInfo(pid)
	if err != nil {
		return "", err
	}
	start := rows[0].Proc.P_starttime
	return fmt.Sprintf("%d.%06d Darwin start time", start.Sec, start.Usec), nil
}
