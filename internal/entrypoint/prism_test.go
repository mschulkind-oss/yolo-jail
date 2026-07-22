package entrypoint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// prismTestEnv builds an Env with a fake jail home, a fake /ctx/host-pi mount,
// and a writable workspace (for the .yolo/prism sidecars), plus the given
// host_pi_files list. It returns the env and the host-mount dir so a test can
// seed host files.
func prismTestEnv(t *testing.T, hostPiFilesJSON string) (*Env, string) {
	t.Helper()
	home := t.TempDir()
	ctx := t.TempDir()
	ws := t.TempDir()

	orig := hostPiDir
	hostPiDir = ctx
	t.Cleanup(func() { hostPiDir = orig })

	e := &Env{
		Home:      home,
		Workspace: ws,
		Vars:      map[string]string{"YOLO_HOST_PI_FILES": hostPiFilesJSON},
	}
	return e, ctx
}

// decodeJSONFile reads and JSON-decodes a file into a generic object.
func decodeJSONFile(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode %s: %v\n---\n%s", path, err, raw)
	}
	return m
}

// TestConfigurePiPrismFirstMigration is the pi proof-of-concept for the
// config-composition cutover: on the FIRST prism boot (no last_render sidecar)
// the settings file converges to the fresh engine render — host keys merged,
// the yolo-managed defaultProjectTrust forced — the last_render sidecar is
// seeded to those exact bytes, the overlay starts empty, and the obsolete
// yolo-host-synced-settings.json snapshot (§4.7 orphan) is deleted.
func TestConfigurePiPrismFirstMigration(t *testing.T) {
	e, ctx := prismTestEnv(t, `["settings.json"]`)

	// Host settings the prism must merge in.
	hostJSON := `{"theme":"dark","defaultModel":"claude-fable-5"}`
	if err := os.WriteFile(filepath.Join(ctx, "settings.json"), []byte(hostJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	// A pre-existing bespoke settings.json carrying a STALE key the fresh render
	// no longer emits, plus the obsolete snapshot the bespoke path wrote.
	piDir := e.PiDir()
	if err := os.MkdirAll(piDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(piDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"theme":"dark","staleBespokeKey":"gone","defaultProjectTrust":"always"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshotPath := e.PiHostSettingsSnapshotPath()
	if err := os.WriteFile(snapshotPath, []byte(`{"theme":"dark"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ConfigurePiPrism(e); err != nil {
		t.Fatalf("ConfigurePiPrism: %v", err)
	}

	// The rendered settings: host theme wins over the default, host defaultModel
	// present, managed key forced, and the stale bespoke key GONE (dropped, not
	// captured — the one-time-migration-snapshot posture).
	got := decodeJSONFile(t, settingsPath)
	if got["theme"] != "dark" {
		t.Errorf("theme = %v, want dark (host merge)", got["theme"])
	}
	if got["defaultModel"] != "claude-fable-5" {
		t.Errorf("defaultModel = %v, want claude-fable-5 (host)", got["defaultModel"])
	}
	if got["defaultProjectTrust"] != "always" {
		t.Errorf("defaultProjectTrust = %v, want always (managed)", got["defaultProjectTrust"])
	}
	if _, present := got["staleBespokeKey"]; present {
		t.Errorf("staleBespokeKey survived (%v); first migration must drop it", got["staleBespokeKey"])
	}

	// The last_render sidecar equals exactly the bytes just written to the surface.
	surfaceBytes, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	lastRenderBytes, err := os.ReadFile(prismLastRenderPath(e, "pi", "settings"))
	if err != nil {
		t.Fatalf("last_render sidecar missing: %v", err)
	}
	if string(lastRenderBytes) != string(surfaceBytes) {
		t.Errorf("last_render != surface bytes:\n last_render: %s\n surface:     %s", lastRenderBytes, surfaceBytes)
	}

	// The overlay sidecar is a genuinely empty object.
	overlay := decodeJSONFile(t, prismOverlayPath(e, "pi", "settings"))
	if len(overlay) != 0 {
		t.Errorf("overlay = %v, want {} on first migration", overlay)
	}

	// The obsolete snapshot orphan is deleted (§4.7).
	if _, err := os.Stat(snapshotPath); !os.IsNotExist(err) {
		t.Errorf("yolo-host-synced-settings.json should be deleted on migration, stat err = %v", err)
	}
}

// TestConfigurePiPrismSteadyStateEditSurvives proves the §5 overlay loop across
// two boots: after the first-migration seed, an in-jail edit to settings.json
// (adding a key) is captured and SURVIVES the second boot's regeneration.
func TestConfigurePiPrismSteadyStateEditSurvives(t *testing.T) {
	e, ctx := prismTestEnv(t, `["settings.json"]`)
	if err := os.WriteFile(filepath.Join(ctx, "settings.json"), []byte(`{"theme":"dark"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Boot 1: first migration seeds the baseline.
	if err := ConfigurePiPrism(e); err != nil {
		t.Fatalf("boot 1: %v", err)
	}
	settingsPath := filepath.Join(e.PiDir(), "settings.json")

	// The agent edits settings.json in-jail: change theme + add a personal key.
	edited := decodeJSONFile(t, settingsPath)
	edited["theme"] = "solarized"
	edited["myPersonalKey"] = "keepme"
	editedBytes, _ := json.Marshal(edited)
	if err := os.WriteFile(settingsPath, editedBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	// Boot 2: steady state. The engine must capture the edit into the overlay and
	// re-emit it, so both survive the regen even though the host says theme=dark.
	if err := ConfigurePiPrism(e); err != nil {
		t.Fatalf("boot 2: %v", err)
	}
	got := decodeJSONFile(t, settingsPath)
	if got["theme"] != "solarized" {
		t.Errorf("theme = %v, want solarized (in-jail edit must survive regen)", got["theme"])
	}
	if got["myPersonalKey"] != "keepme" {
		t.Errorf("myPersonalKey = %v, want keepme (in-jail edit must survive regen)", got["myPersonalKey"])
	}
	// The managed key is still enforced regardless of edits.
	if got["defaultProjectTrust"] != "always" {
		t.Errorf("defaultProjectTrust = %v, want always (managed always wins)", got["defaultProjectTrust"])
	}
	// The overlay sidecar recorded the edit.
	overlay := decodeJSONFile(t, prismOverlayPath(e, "pi", "settings"))
	if overlay["theme"] != "solarized" || overlay["myPersonalKey"] != "keepme" {
		t.Errorf("overlay = %v, want theme+myPersonalKey captured", overlay)
	}
}

// TestConfigurePiPrismNoHostSettings proves host-source gating: when
// settings.json is NOT in YOLO_HOST_PI_FILES, the host layer is empty and the
// render is defaults<managed only (theme defaults to "system").
func TestConfigurePiPrismNoHostSettings(t *testing.T) {
	e, ctx := prismTestEnv(t, `["models.json"]`) // settings.json intentionally absent from the list
	// Even if a host settings.json exists on the mount, it must NOT be read
	// because it is not declared in host_pi_files (fail-closed staging).
	if err := os.WriteFile(filepath.Join(ctx, "settings.json"), []byte(`{"theme":"dark"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ConfigurePiPrism(e); err != nil {
		t.Fatalf("ConfigurePiPrism: %v", err)
	}
	got := decodeJSONFile(t, filepath.Join(e.PiDir(), "settings.json"))
	if got["theme"] != "system" {
		t.Errorf("theme = %v, want system (undeclared host settings must not be read)", got["theme"])
	}
	if got["defaultProjectTrust"] != "always" {
		t.Errorf("defaultProjectTrust = %v, want always (managed)", got["defaultProjectTrust"])
	}
}

// TestConfigurePiPrismStillStagesNonSettingsFiles proves the port preserves the
// existing host_pi_files tree-staging behavior (models.json et al. still land in
// ~/.pi/agent/) — the prism owns settings.json, not the sibling files.
func TestConfigurePiPrismStillStagesNonSettingsFiles(t *testing.T) {
	e, ctx := prismTestEnv(t, `["settings.json", "models.json"]`)
	if err := os.WriteFile(filepath.Join(ctx, "settings.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	modelsBody := `{"providers":{"bedrock-mantle":{}}}`
	if err := os.WriteFile(filepath.Join(ctx, "models.json"), []byte(modelsBody), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ConfigurePiPrism(e); err != nil {
		t.Fatalf("ConfigurePiPrism: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(e.PiDir(), "models.json"))
	if err != nil {
		t.Fatalf("models.json not staged: %v", err)
	}
	if string(got) != modelsBody {
		t.Errorf("models.json = %q, want %q", got, modelsBody)
	}
}
