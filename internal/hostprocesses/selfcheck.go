package hostprocesses

import (
	"fmt"
	"os"
	"strconv"
)

// SelfCheck is the `yolo doctor` health check, run through the loophole's
// `doctor_cmd`.
//
// It reports on the SETTINGS FILE the daemon would actually read — the same file,
// resolved from the same {settings} token, that the daemon's argv names. It used to
// hunt for a yolo-jail.jsonc via $YOLO_HOST_PROCESSES_CONFIG or the cwd, which meant
// the doctor's answer and the daemon's behaviour came from two different searches and
// could disagree about which file was in force. There is one file now, and the
// caller names it.
//
// # A MISSING file is not a failure, and getting that wrong would be loud
//
// yolo writes the settings file when a jail LAUNCHES this loophole, so on a machine
// that has not launched one yet the file is simply absent — the normal state of a
// fresh install, and `yolo check` runs there. Reporting it as FAIL would put a red
// line under every fresh machine for a condition the user cannot act on and that the
// very next `yolo` invocation fixes.
//
// What IS a failure is a file that exists and does not parse: the daemon collapses
// that to an empty allowlist and keeps running, so `yolo-ps` would show nothing while
// everything looked healthy. That is the one thing this check can see that nothing
// else reports.
//
// Exit codes: 0 for every knowable state, 1 for a settings file that is present and
// unreadable as JSON.
func SelfCheck(settingsPath string) int {
	if settingsPath == "" {
		// No path means the daemon was run by hand rather than through the manifest.
		// Not a fault — there is simply nothing to report on.
		fmt.Println("OK: daemon present; no settings file in scope " +
			"(pass --settings to check one)")
		return 0
	}
	if !isFile(settingsPath) {
		fmt.Println("OK: daemon present; no settings resolved yet at " + settingsPath +
			" — yolo writes it when a jail launches this loophole")
		return 0
	}
	cfg, ok := loadSettings(settingsPath)
	if !ok {
		fmt.Println("FAIL: settings file at " + settingsPath + " is not readable JSON — the " +
			"daemon would start with an EMPTY allowlist and look healthy while yolo-ps " +
			"showed nothing")
		return 1
	}
	if len(cfg.Visible) == 0 {
		fmt.Println("OK: settings at " + settingsPath +
			" allowlist no process names (loopholes.host-processes.settings.visible is empty)")
		return 0
	}
	fmt.Println("OK: " + strconv.Itoa(len(cfg.Visible)) + " comms allowlisted at " + settingsPath)
	return 0
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}
