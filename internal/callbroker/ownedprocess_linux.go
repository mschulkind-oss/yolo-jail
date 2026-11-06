//go:build linux

package callbroker

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"syscall"

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

// ownedGroupHasNoLiveMemberOS lists /proc and reports whether every process
// in group pgid has exited (state Z or X). A process that vanishes while the
// listing runs has exited too.
func ownedGroupHasNoLiveMemberOS(pgid int) (bool, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + entry.Name() + "/stat")
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
				continue
			}
			return false, err
		}
		end := strings.LastIndexByte(string(b), ')')
		if end < 0 {
			return false, fmt.Errorf("malformed process stat for PID %s", entry.Name())
		}
		fields := strings.Fields(string(b[end+1:]))
		if len(fields) < 3 {
			return false, fmt.Errorf("short process stat for PID %s", entry.Name())
		}
		if fields[2] == strconv.Itoa(pgid) && fields[0] != "Z" && fields[0] != "X" {
			return false, nil
		}
	}
	return true, nil
}
