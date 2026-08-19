package hostprocesses

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestLoadSettingsMissingFile: a missing file -> empty visible + DEFAULT fields.
//
// The three unreadable cases (absent, unparseable, not an object) share this answer
// deliberately — see disabled(). An empty allowlist is the fail-closed state, and
// it is the SAME state the feature has always had before anyone configured it.
func TestLoadSettingsMissingFile(t *testing.T) {
	cfg := LoadSettings(filepath.Join(t.TempDir(), "nope.json"))
	if len(cfg.Visible) != 0 {
		t.Errorf("missing-file visible = %v, want empty", cfg.Visible)
	}
	if !reflect.DeepEqual(cfg.Fields, DefaultFields) {
		t.Errorf("missing-file fields = %v, want DEFAULT", cfg.Fields)
	}
}

// TestLoadSettingsEmptyPath covers the daemon started with no --settings at all:
// same fail-closed answer, and reached without touching the filesystem.
func TestLoadSettingsEmptyPath(t *testing.T) {
	if cfg := LoadSettings(""); len(cfg.Visible) != 0 {
		t.Errorf("empty-path visible = %v, want empty", cfg.Visible)
	}
}

// TestLoadSettingsReadsTheFlatFile pins the SHAPE of what yolo writes. The daemon
// reads a flat map of values, NOT a yolo-jail.jsonc — so a file still spelling
// `host_processes.visible` must resolve to the fail-closed empty allowlist rather
// than to whatever it nests. That is the check that makes the retired --config flag's
// refusal necessary rather than merely tidy: a silent flag alias would land exactly
// this input here and report an empty allowlist as a working daemon.
func TestLoadSettingsReadsTheFlatFile(t *testing.T) {
	dir := t.TempDir()
	flat := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(flat,
		[]byte(`{"visible":["sway","waykeeper"],"fields":["pid","comm"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := LoadSettings(flat)
	if !reflect.DeepEqual(cfg.Visible, []string{"sway", "waykeeper"}) {
		t.Errorf("visible = %v", cfg.Visible)
	}
	if !reflect.DeepEqual(cfg.Fields, []string{"pid", "comm"}) {
		t.Errorf("fields = %v", cfg.Fields)
	}

	nested := filepath.Join(dir, "old.json")
	if err := os.WriteFile(nested,
		[]byte(`{"host_processes":{"visible":["sway"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if old := LoadSettings(nested); len(old.Visible) != 0 {
		t.Errorf("a config-shaped file resolved to %v — the daemon reads a FLAT settings "+
			"file, so nesting must yield the fail-closed empty allowlist, never a "+
			"partially-honored config", old.Visible)
	}
}

// TestLoadSettingsEmptyFieldsFallsBackToDefaults pins the one place an empty list is
// not taken literally: an empty `ps -o` column list is a broken invocation, not a
// narrower view.
// `visible` has the opposite rule and empty means OFF, which is why the two keys do
// not share a helper.
func TestLoadSettingsEmptyFieldsFallsBackToDefaults(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(p, []byte(`{"visible":["sway"],"fields":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := LoadSettings(p)
	if !reflect.DeepEqual(cfg.Fields, DefaultFields) {
		t.Errorf("empty fields = %v, want DEFAULT", cfg.Fields)
	}
	if len(cfg.Visible) != 1 {
		t.Errorf("visible = %v, want the one declared name", cfg.Visible)
	}
}

// TestSelfCheckMissingFileIsNotAFailure guards the one thing that would make `yolo
// check` red on every fresh machine. The settings file is written when a jail LAUNCHES
// this loophole, so before the first launch it is absent — a normal state, not a fault,
// and not one the user can act on.
func TestSelfCheckMissingFileIsNotAFailure(t *testing.T) {
	if rc := SelfCheck(filepath.Join(t.TempDir(), "settings.json")); rc != 0 {
		t.Errorf("SelfCheck on an unwritten settings file = %d, want 0 — yolo writes it at "+
			"launch, so its absence is the state of every machine that has not launched "+
			"a jail yet", rc)
	}
	if rc := SelfCheck(""); rc != 0 {
		t.Errorf("SelfCheck with no path = %d, want 0", rc)
	}
}

// TestSelfCheckUnparseableFileFails is the case that IS a fault, and the only one this
// check can see that nothing else reports: the daemon collapses an unreadable settings
// file to an empty allowlist and keeps running, so yolo-ps shows nothing while
// everything looks healthy.
func TestSelfCheckUnparseableFileFails(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if rc := SelfCheck(p); rc != 1 {
		t.Errorf("SelfCheck on a corrupt settings file = %d, want 1", rc)
	}
	// A well-formed file with an empty allowlist is NOT a failure: an empty allowlist
	// is a configuration, and it is what the feature defaults to.
	if err := os.WriteFile(p, []byte(`{"visible":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if rc := SelfCheck(p); rc != 0 {
		t.Errorf("SelfCheck on an empty allowlist = %d, want 0", rc)
	}
}
