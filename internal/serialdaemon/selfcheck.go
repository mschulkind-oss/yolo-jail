package serialdaemon

import (
	"fmt"
	"os"
)

// SelfCheck runs diagnostic checks for `yolo doctor` or `yolo check`.
func SelfCheck(settingsPath string) int {
	cfg, ok := loadSettings(settingsPath)
	if !ok {
		fmt.Fprintf(os.Stderr, "serial: cannot parse settings file %s\n", settingsPath)
		return 1
	}

	a := readAccess(cfg.grantPath)
	if r, refuse := a.refusal(""); refuse {
		fmt.Printf("serial: devices %s for this project; nothing is listed or opened until a grant covers it\n", r.Standing)
		return 0
	}
	devices := findDevices(a.devices())
	accessible := 0
	for _, d := range devices {
		if d.Accessible {
			accessible++
		}
	}

	fmt.Printf("serial: %d device(s) found matching the granted device globs (%d accessible)\n", len(devices), accessible)
	for _, d := range devices {
		status := "ok"
		if !d.Accessible {
			status = "inaccessible: " + d.Error
		}
		fmt.Printf("  - %s: %s\n", d.Path, status)
	}
	return 0
}
