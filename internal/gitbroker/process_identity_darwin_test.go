//go:build darwin

package gitbroker

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func processIdentity(pid int) (string, bool, error) {
	rows, err := unix.SysctlKinfoProcSlice("kern.proc.pid", pid)
	if err != nil {
		return "", false, err
	}
	if len(rows) == 0 {
		return "", false, nil
	}
	if len(rows) != 1 || int(rows[0].Proc.P_pid) != pid {
		return "", false, fmt.Errorf("unexpected kern.proc.pid row for %d", pid)
	}
	p := rows[0].Proc
	// XNU's SZOMB (5) and SDEAD (6) cannot run a delayed child action.
	return fmt.Sprintf("%d.%06d", p.P_starttime.Sec, p.P_starttime.Usec), p.P_stat != 5 && p.P_stat != 6, nil
}
