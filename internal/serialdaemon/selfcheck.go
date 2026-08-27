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

	devices := findDevices(cfg.AllowedDevices)
	accessible := 0
	for _, d := range devices {
		if d.Accessible {
			accessible++
		}
	}

	fmt.Printf("serial: %d device(s) found matching allowlist (%d accessible)\n", len(devices), accessible)
	for _, d := range devices {
		status := "ok"
		if !d.Accessible {
			status = "inaccessible: " + d.Error
		}
		fmt.Printf("  - %s: %s\n", d.Path, status)
	}
	return 0
}
