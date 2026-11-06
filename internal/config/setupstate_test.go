package config

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// EACH SETUP'S JAIL HAS ITS OWN LAUNCH RECORDS (named-setups-plan.md slice 6a-iii, SUP-D37): the
// boot baseline and the assembled copy with its origins are under the setup's state directory,
// and "" reads this session's setup's (YOLO_SETUP). Two setups' launches never overwrite each
// other's, and the file an older yolo wrote at .yolo/config-boot.json is not read as either's.
func TestLaunchRecordsAreTheSetups(t *testing.T) {
	ws := t.TempDir()
	for leaf, path := range map[string]string{
		"config-boot.json":              WorkspaceConfigBootPath(ws, "podman-self"),
		"config-assembled.json":         WorkspaceAssembledConfigPath(ws, "podman-self"),
		"config-assembled-origins.json": WorkspaceAssembledOriginsPath(ws, "podman-self"),
	} {
		if want := filepath.Join(paths.SetupStateDir(ws, "podman-self"), leaf); path != want {
			t.Errorf("%s is at %s, want %s", leaf, path, want)
		}
	}
	t.Setenv(setup.EnvVar, "seatbelt-self")
	if got, want := WorkspaceConfigBootPath(ws, ""), filepath.Join(paths.SetupStateDir(ws, "seatbelt-self"), "config-boot.json"); got != want {
		t.Errorf(`WorkspaceConfigBootPath(ws, "") = %s, want this session's setup's %s`, got, want)
	}

	a, b := jsonx.NewOrderedMap(), jsonx.NewOrderedMap()
	a.Set("packs", []any{"claude"})
	b.Set("packs", []any{"codex"})
	if _, err := WriteWorkspaceBootBaseline(ws, "podman-self", a); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteWorkspaceBootBaseline(ws, "seatbelt-self", b); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(LegacyConfigBootPath(ws), []byte(`{"packs":["legacy"]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"podman-self": "claude", "seatbelt-self": "codex", "": "codex"} {
		got, ok, err := ReadWorkspaceBootBaseline(ws, name)
		if err != nil || !ok {
			t.Fatalf("ReadWorkspaceBootBaseline(%q) = %v, %v", name, ok, err)
		}
		if v, _ := got.Get("packs"); len(v.([]any)) != 1 || v.([]any)[0] != want {
			t.Errorf("setup %q read packs %v, want [%s]", name, v, want)
		}
	}
	if got, want := BootBaselineSetups(ws), []string{"podman-self", "seatbelt-self"}; !slices.Equal(got, want) {
		t.Errorf("BootBaselineSetups = %v, want %v", got, want)
	}
}

// With YOLO_SETUP unset — a session an older yolo launched, or no session at all — a read of
// "this session's" record is a NAMED error, never a silent read of .yolo/config-boot.json or of
// some setup's file.
func TestASessionThatNamesNoSetupHasNoRecords(t *testing.T) {
	ws := t.TempDir()
	t.Setenv(setup.EnvVar, "")
	if err := os.MkdirAll(paths.WorkspaceStateDir(ws), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(LegacyConfigBootPath(ws), []byte(`{"packs":["legacy"]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadWorkspaceBootBaseline(ws, ""); !errors.Is(err, ErrNoSessionSetup) {
		t.Errorf("ReadWorkspaceBootBaseline: %v, want ErrNoSessionSetup", err)
	}
	if _, _, ok, err := WorkspaceConfigDrift(ws, ""); ok || !errors.Is(err, ErrNoSessionSetup) {
		t.Errorf("WorkspaceConfigDrift: ok=%v err=%v, want ErrNoSessionSetup", ok, err)
	}
	if _, err := WriteWorkspaceBootBaseline(ws, "", jsonx.NewOrderedMap()); !errors.Is(err, ErrNoSessionSetup) {
		t.Errorf("WriteWorkspaceBootBaseline: %v, want ErrNoSessionSetup", err)
	}
	t.Setenv(setup.EnvVar, "Not A Setup")
	if _, _, err := ReadWorkspaceBootBaseline(ws, ""); err == nil {
		t.Error("a YOLO_SETUP that is not a setup name was read")
	}
}

// The workspace marker reads a setup's baseline, and the legacy file an older launch left.
func TestBootBaselineMarkedReadsEitherSpelling(t *testing.T) {
	ws := t.TempDir()
	if BootBaselineMarked(ws) {
		t.Fatal("an empty workspace is marked")
	}
	if _, err := WriteWorkspaceBootBaseline(ws, "podman-self", jsonx.NewOrderedMap()); err != nil {
		t.Fatal(err)
	}
	if !BootBaselineMarked(ws) {
		t.Error("a setup's baseline does not mark the workspace")
	}
	legacy := t.TempDir()
	if err := os.MkdirAll(paths.WorkspaceStateDir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(LegacyConfigBootPath(legacy), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !BootBaselineMarked(legacy) {
		t.Error("the legacy baseline does not mark the workspace")
	}
}
