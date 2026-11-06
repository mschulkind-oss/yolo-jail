//go:build linux

package gitbroker

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

func processIdentity(pid int) (string, bool, error) {
	return processIdentityRead(pid, os.ReadFile)
}

func processIdentityRead(pid int, readFile func(string) ([]byte, error)) (string, bool, error) {
	b, err := readFile(fmt.Sprintf("/proc/%d/stat", pid))
	if os.IsNotExist(err) || errors.Is(err, syscall.ESRCH) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	// comm is parenthesized and may contain spaces or closing parentheses.
	end := strings.LastIndexByte(string(b), ')')
	if end < 0 {
		return "", false, fmt.Errorf("malformed /proc/%d/stat", pid)
	}
	fields := strings.Fields(string(b[end+1:]))
	if len(fields) < 20 {
		return "", false, fmt.Errorf("short /proc/%d/stat", pid)
	}
	if _, err := strconv.ParseUint(fields[19], 10, 64); err != nil {
		return "", false, fmt.Errorf("invalid /proc/%d starttime: %w", pid, err)
	}
	return fields[19], fields[0] != "Z" && fields[0] != "X", nil
}
