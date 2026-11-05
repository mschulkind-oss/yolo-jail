//go:build darwin

package gitremote

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func gitExitedUnreaped(pid int) (bool, error) {
	rows, err := unix.SysctlKinfoProcSlice("kern.proc.pid", pid)
	if err != nil {
		return false, err
	}
	if len(rows) != 1 || int(rows[0].Proc.P_pid) != pid {
		return false, fmt.Errorf("cannot observe unreaped git process %d", pid)
	}
	// XNU reports unreaped exited children as SZOMB (5) or SDEAD (6).
	return rows[0].Proc.P_stat == 5 || rows[0].Proc.P_stat == 6, nil
}
