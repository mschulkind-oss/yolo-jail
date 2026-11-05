//go:build darwin

package gitremote

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func gitLeaderIdentity(pid int) (string, error) {
	rows, err := unix.SysctlKinfoProcSlice("kern.proc.pid", pid)
	if err != nil {
		return "", err
	}
	if len(rows) != 1 || int(rows[0].Proc.P_pid) != pid {
		return "", fmt.Errorf("cannot observe Git leader PID %d", pid)
	}
	start := rows[0].Proc.P_starttime
	return fmt.Sprintf("%d.%06d Darwin start time", start.Sec, start.Usec), nil
}
