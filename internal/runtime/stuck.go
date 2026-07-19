package runtime

import "strings"

// provisioningCommands are the leaf process names that, if they are the ONLY
// non-infrastructure processes running, mean the jail is stuck mid-provision.
var provisioningCommands = map[string]struct{}{
	"uv": {}, "mise": {}, "pip": {}, "npm": {},
}

// infraCommands are never counted as "user" processes — the container's own
// scaffolding.
var infraCommands = map[string]struct{}{
	"bash": {}, "sh": {}, "podman-init": {}, "yolo-entrypo": {},
	"sleep": {}, "sed": {},
}

// StuckReasonFromTop analyzes `podman top <name> -eo comm` stdout and returns a
// reason string if the container is stuck, or "" if healthy.
// logic of _check_container_stuck: drop the header row, strip blanks; no
// processes => "no processes"; if every remaining process is provisioning or
// infra (no genuine user command) => "stuck in provisioning".
//
// This is Apple-Container-agnostic: the caller skips it for the container
// runtime (which has no `top`), matching the Python early-return.
func StuckReasonFromTop(stdout string) string {
	var procs []string
	for _, line := range tableRows(stdout) {
		if p := strings.TrimSpace(line); p != "" {
			procs = append(procs, p)
		}
	}
	if len(procs) == 0 {
		return "no processes"
	}
	for _, p := range procs {
		if _, ok := provisioningCommands[p]; ok {
			continue
		}
		if _, ok := infraCommands[p]; ok {
			continue
		}
		// A genuine user command is running — healthy.
		return ""
	}
	return "stuck in provisioning"
}
