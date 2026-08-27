package serialdaemon

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDefaultSettings(t *testing.T) {
	s := DefaultSettings()
	if len(s.AllowedDevices) == 0 {
		t.Fatal("DefaultSettings returned empty AllowedDevices")
	}
	if s.DefaultBaud != 115200 {
		t.Errorf("DefaultBaud = %d, want 115200", s.DefaultBaud)
	}
}

func TestLoadSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	content := []byte(`{
		"allowed_devices": ["/dev/ttyUSB0", "/dev/ttyACM*"],
		"default_baud": 9600
	}`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := LoadSettings(path)
	wantDevices := []string{"/dev/ttyUSB0", "/dev/ttyACM*"}
	if !reflect.DeepEqual(cfg.AllowedDevices, wantDevices) {
		t.Errorf("AllowedDevices = %v, want %v", cfg.AllowedDevices, wantDevices)
	}
	if cfg.DefaultBaud != 9600 {
		t.Errorf("DefaultBaud = %d, want 9600", cfg.DefaultBaud)
	}
}

func TestLoadSettingsMissingOrEmpty(t *testing.T) {
	cfg := LoadSettings("")
	if len(cfg.AllowedDevices) == 0 {
		t.Errorf("LoadSettings(\"\") gave empty AllowedDevices")
	}

	cfg = LoadSettings("/nonexistent/file.json")
	if len(cfg.AllowedDevices) == 0 {
		t.Errorf("LoadSettings(nonexistent) gave empty AllowedDevices")
	}
}
