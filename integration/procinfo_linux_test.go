//go:build linux

package integration

// procinfo_linux_test.go is the Linux half of the harness's view of host processes
// (procinfo_test.go): every answer is read from /proc.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// processState is pid's state letter ("S", "R", "Z", …) from /proc/<pid>/stat. found is false
// when /proc has no such process; err is any other failure to read it.
func processState(pid int) (state string, found bool, err error) {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	// The state is the first field after the parenthesised command name, which may itself
	// contain spaces or parentheses — hence the LAST ')'.
	s := string(stat)
	i := strings.LastIndexByte(s, ')')
	if i < 0 || i+2 >= len(s) {
		return "", false, fmt.Errorf("unparsable /proc/%d/stat: %q", pid, s)
	}
	return s[i+2 : i+3], true, nil
}

// processArgs is pid's argv, from /proc/<pid>/cmdline, whose arguments are NUL-separated.
func processArgs(pid int) ([]string, error) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimRight(string(raw), "\x00")
	if trimmed == "" {
		return nil, nil
	}
	return strings.Split(trimmed, "\x00"), nil
}

// processChildren is every pid whose parent is ppid, from the fourth field of each
// /proc/<pid>/stat.
func processChildren(ppid int) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var kids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil {
			continue
		}
		s := string(stat)
		i := strings.LastIndexByte(s, ')')
		if i < 0 {
			continue
		}
		fields := strings.Fields(s[i+1:])
		if len(fields) >= 2 && fields[1] == strconv.Itoa(ppid) {
			kids = append(kids, pid)
		}
	}
	return kids
}
