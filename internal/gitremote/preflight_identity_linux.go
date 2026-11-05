//go:build linux

package gitremote

import (
	"fmt"
	"os"
	"strings"
)

func gitLeaderIdentity(pid int) (string, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", err
	}
	end := strings.LastIndexByte(string(b), ')')
	if end < 0 {
		return "", fmt.Errorf("malformed /proc/%d/stat", pid)
	}
	fields := strings.Fields(string(b[end+1:]))
	if len(fields) < 20 {
		return "", fmt.Errorf("short /proc/%d/stat", pid)
	}
	return fields[19] + " Linux clock ticks", nil
}
