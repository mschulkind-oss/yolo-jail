//go:build linux

package run

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// keeperProcessBirth binds private start metadata to this boot and this exact
// process incarnation; a PID and a timestamp written by userspace are not enough.
func keeperProcessBirth(pid int) string {
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil || strings.TrimSpace(string(boot)) == "" || pid <= 0 {
		return ""
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return ""
	}
	end := strings.LastIndexByte(string(stat), ')')
	if end < 0 {
		return ""
	}
	fields := strings.Fields(string(stat[end+1:]))
	if len(fields) <= 19 || fields[0] == "Z" || fields[0] == "X" {
		return ""
	}
	ticks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || ticks == 0 {
		return ""
	}
	return strings.TrimSpace(string(boot)) + ":" + strconv.FormatUint(ticks, 10)
}
