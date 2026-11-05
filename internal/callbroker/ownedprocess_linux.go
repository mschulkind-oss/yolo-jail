//go:build linux

package callbroker

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

func ownedLeaderExited(pid int) (bool, error) {
	var info unix.Siginfo
	err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOHANG|unix.WNOWAIT, nil)
	return info.Signo != 0, err
}

func ownedLeaderIdentity(pid int) (string, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", err
	}
	end := strings.LastIndexByte(string(b), ')')
	if end < 0 {
		return "", fmt.Errorf("malformed process stat")
	}
	fields := strings.Fields(string(b[end+1:]))
	if len(fields) < 20 {
		return "", fmt.Errorf("short process stat")
	}
	return fields[19] + " Linux clock ticks", nil
}
