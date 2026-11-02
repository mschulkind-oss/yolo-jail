package serialdaemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
)

// TestLoadSettings: `default_baud` comes from the grant file's settings, read once at startup.
func TestLoadSettings(t *testing.T) {
	cfg := LoadSettings(grantFile(t, `{"default_baud": 9600}`, loopholegrant.Granted, "/dev/ttyUSB0"))
	if cfg.DefaultBaud != 9600 {
		t.Errorf("DefaultBaud = %d, want 9600", cfg.DefaultBaud)
	}
}

// TestLoadSettingsMissingOrEmpty: no --settings, a missing file, and a file without the setting
// run on the default baud. None of them grants a device (grants_test.go).
func TestLoadSettingsMissingOrEmpty(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(empty, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", "/nonexistent/file.json", empty} {
		cfg, ok := loadSettings(path)
		if !ok || cfg.DefaultBaud != DefaultBaudRate {
			t.Errorf("loadSettings(%q) = %+v, ok=%v; want the default baud, ok", path, cfg, ok)
		}
		if devices := readAccess(cfg.grantPath).devices(); len(devices) != 0 {
			t.Errorf("loadSettings(%q) grants %v, want nothing", path, devices)
		}
	}
}
