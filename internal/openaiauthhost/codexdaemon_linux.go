package openaiauthhost

import (
	"bytes"
	"os"
	"strconv"
	"strings"
)

// processIdentity reads pid's native identity the way Codex records it on Linux
// (codex-rs/app-server-daemon/src/backend/pid_identity.rs, rust-v0.159.0): the boot id from
// /proc/sys/kernel/random/boot_id, trimmed, and field 22 of /proc/<pid>/stat, the start time in
// clock ticks since boot, with the state field beside it so a zombie can be told apart. The comm
// field can hold spaces and ')', so the fields are counted after the LAST ')', as Codex does.
// ok is false when either read fails.
func processIdentity(pid int) (bootID string, startTicks uint64, zombie, ok bool) {
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil || strings.TrimSpace(string(boot)) == "" {
		return "", 0, false, false
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", 0, false, false
	}
	end := bytes.LastIndexByte(stat, ')')
	if end < 0 {
		return "", 0, false, false
	}
	fields := strings.Fields(string(stat[end+1:]))
	if len(fields) < 20 {
		return "", 0, false, false
	}
	ticks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return "", 0, false, false
	}
	return strings.TrimSpace(string(boot)), ticks, fields[0] == "Z", true
}
