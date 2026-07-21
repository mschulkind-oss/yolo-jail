package entrypoint

import (
	"os"
	"path/filepath"
	"testing"
)

// TestConfigurePiCopiesNonSettingsHostFiles is the regression test for the
// host_pi_files asymmetry: claude's ConfigureClaude copies every
// host_claude_files entry except settings.json into ~/.claude/, but ConfigurePi
// historically only read settings.json — so a host_pi_files entry like
// models.json was mounted at /ctx/host-pi/ yet never installed into
// ~/.pi/agent/, where pi actually reads it. This asserts parity with claude.
func TestConfigurePiCopiesNonSettingsHostFiles(t *testing.T) {
	home := t.TempDir()
	ctx := t.TempDir()

	// Point the (normally hardcoded) /ctx/host-pi at a temp dir.
	orig := hostPiDir
	hostPiDir = ctx
	t.Cleanup(func() { hostPiDir = orig })

	// Host-mounted files under /ctx/host-pi.
	if err := os.WriteFile(filepath.Join(ctx, "settings.json"), []byte(`{"theme":"dark"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	modelsBody := `{"providers":{"bedrock-mantle":{}}}`
	if err := os.WriteFile(filepath.Join(ctx, "models.json"), []byte(modelsBody), 0o644); err != nil {
		t.Fatal(err)
	}

	e := &Env{
		Home: home,
		Vars: map[string]string{
			"YOLO_HOST_PI_FILES": `["settings.json", "models.json"]`,
		},
	}
	if err := ConfigurePi(e); err != nil {
		t.Fatal(err)
	}

	// models.json must be installed where pi reads it, with host content intact.
	got, err := os.ReadFile(filepath.Join(home, ".pi", "agent", "models.json"))
	if err != nil {
		t.Fatalf("models.json not installed into ~/.pi/agent/: %v", err)
	}
	if string(got) != modelsBody {
		t.Errorf("models.json content = %q, want %q", got, modelsBody)
	}

	// settings.json still merged from the host (theme carried over) with the
	// yolo-managed key forced.
	settings := loadObject(filepath.Join(home, ".pi", "agent", "settings.json"))
	if v, _ := settings.Get("theme"); v != "dark" {
		t.Errorf("settings.json theme = %v, want dark (host merge)", v)
	}
	if v, _ := settings.Get("defaultProjectTrust"); v != "always" {
		t.Errorf("defaultProjectTrust = %v, want always (yolo-managed)", v)
	}
}

// TestConfigurePiSkipsAbsentHostFiles confirms a listed-but-missing file is a
// best-effort no-op (mirrors claude's syncHostClaudeFiles).
func TestConfigurePiSkipsAbsentHostFiles(t *testing.T) {
	home := t.TempDir()
	ctx := t.TempDir()
	orig := hostPiDir
	hostPiDir = ctx
	t.Cleanup(func() { hostPiDir = orig })

	e := &Env{
		Home: home,
		Vars: map[string]string{"YOLO_HOST_PI_FILES": `["settings.json", "absent.json"]`},
	}
	if err := ConfigurePi(e); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".pi", "agent", "absent.json")); !os.IsNotExist(err) {
		t.Errorf("absent.json should not be created, stat err = %v", err)
	}
}
