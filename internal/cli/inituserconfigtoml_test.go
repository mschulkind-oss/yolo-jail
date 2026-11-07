package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitUserConfigLeavesAnExistingTOMLConfigAlone: with a config.toml already in place,
// `yolo init-user-config` says it exists and writes no config.jsonc beside it, a pair the
// config reader refuses.
func TestInitUserConfigLeavesAnExistingTOMLConfigAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	toml := filepath.Join(dir, "config.toml")
	const body = "packs = [\"claude\"]\n"
	if err := os.WriteFile(toml, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if rc := InitUserConfig(&out, false); rc != 0 {
		t.Fatalf("rc %d\n%s", rc, out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "config.jsonc")); err == nil {
		t.Fatalf("init-user-config wrote config.jsonc beside config.toml:\n%s", out.String())
	}
	if !strings.Contains(out.String(), toml+" already exists") {
		t.Errorf("output does not name the existing config.toml:\n%s", out.String())
	}
	if got, _ := os.ReadFile(toml); string(got) != body {
		t.Errorf("config.toml changed: %q", got)
	}
}
