package serialdaemon

import (
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
)

const (
	DefaultBaudRate  = 115200
	DefaultTimeoutMs = 2000
	DefaultMaxBytes  = 65536
)

// Settings is what the daemon runs on: the settings yolo resolved at launch, frozen, and the
// path of the grant file whose `grants` block every request re-reads (grants.go).
//
// There is no device allowlist here. What yolo-serial may list and open is the `devices` grant's
// values, and only the grant file carries them: the retired `allowed_devices` setting and its five
// default globs are gone (docs/design/loophole-access-approval-plan.md F4), so a grant file with no
// block, no file, or no --settings at all grants nothing and every request fails closed.
type Settings struct {
	// DefaultBaud is the `default_baud` setting, read once at startup: a change needs a restart.
	DefaultBaud int
	// grantPath is the grant file, re-read at every request for the `devices` grant. Empty
	// grants nothing.
	grantPath string
}

// LoadSettings reads the grant file yolo wrote for this loophole at launch: the settings it
// resolved, read once here, plus the reserved `grants` block, which BuildHandler's handler
// re-reads per request.
func LoadSettings(settingsPath string) Settings {
	cfg, _ := loadSettings(settingsPath)
	return cfg
}

// loadSettings is LoadSettings plus whether the file read cleanly, for the self-check. A missing
// file is not a failure (no jail has launched this loophole yet); a file that does not read is,
// and the daemon refuses every request with it.
func loadSettings(settingsPath string) (cfg Settings, ok bool) {
	cfg = Settings{DefaultBaud: DefaultBaudRate, grantPath: settingsPath}
	if settingsPath == "" {
		return cfg, true
	}
	if _, err := os.Lstat(settingsPath); err != nil {
		return cfg, true
	}
	settings, _, err := loopholegrant.Read(settingsPath)
	if err != nil {
		return cfg, false
	}
	if n, isInt := positiveInt(settings["default_baud"]); isInt {
		cfg.DefaultBaud = n
	}
	return cfg, true
}

// positiveInt is a JSON number that is a positive integer.
func positiveInt(v any) (int, bool) {
	f, ok := v.(float64)
	if !ok || f <= 0 || f != float64(int(f)) {
		return 0, false
	}
	return int(f), true
}
