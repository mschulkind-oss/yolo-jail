package loopholes

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func redirectSettingsCheckState(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	original := StateDirFor
	StateDirFor = func(name string) string { return filepath.Join(root, name) }
	t.Cleanup(func() { StateDirFor = original })
	return root
}

func TestRunSettingsCheckUsesPrivateInputAndPreservesFrozenBytes(t *testing.T) {
	redirectSettingsCheckState(t)
	lp := &Loophole{
		Name:     "fixture",
		Settings: []Setting{{Key: "profile", Type: SettingTypeString, Default: "old"}},
		HostDaemon: &HostDaemon{SettingsCheck: []string{"/bin/sh", "-c",
			"printf 'mutated' > \"$1\"; printf '%s' '{\"reason\":\"missing narrowing\",\"remedy\":\"edit user settings\"}'; exit 9", "validator", "{settings}"}},
	}
	before := jsonx.NewOrderedMap()
	before.Set("profile", "candidate-secret")
	frozen, _, err := FrozenSettingsBytes(lp, before)
	if err != nil {
		t.Fatal(err)
	}
	stable, _, err := WriteSettings(lp, before)
	if err != nil {
		t.Fatal(err)
	}
	stableBefore, err := os.ReadFile(stable)
	if err != nil {
		t.Fatal(err)
	}
	result := RunSettingsCheck(lp, frozen)
	if result.Outcome != hostservice.CommandRefused || result.Reason != "missing narrowing" || result.Remedy != "edit user settings" {
		t.Fatalf("settings check result = %+v", result)
	}
	entries, err := os.ReadDir(filepath.Dir(stable))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(stable) {
		t.Fatalf("private validator snapshot was not cleaned up: %v", entries)
	}
	stableAfter, err := os.ReadFile(stable)
	if err != nil || !reflect.DeepEqual(stableBefore, stableAfter) {
		t.Fatalf("validator mutated stable settings: before=%q after=%q err=%v", stableBefore, stableAfter, err)
	}
	if !reflect.DeepEqual(frozen, []byte("{\"profile\": \"candidate-secret\"}\n")) {
		t.Fatalf("frozen daemon bytes changed: %q", frozen)
	}
}

func TestRunSettingsCheckClassifiesTimeoutAndEmptyRefusal(t *testing.T) {
	redirectSettingsCheckState(t)
	lp := &Loophole{Name: "fixture", Settings: []Setting{{Key: "value", Type: SettingTypeString}},
		HostDaemon: &HostDaemon{SettingsCheck: []string{"/bin/sh", "-c", "sleep 5", "validator", "{settings}"}}}
	frozen, _, err := FrozenSettingsBytes(lp, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := RunSettingsCheck(lp, frozen)
	if result.Outcome != hostservice.CommandTimedOut || result.Reason == "" || result.Remedy == "" {
		t.Fatalf("timeout result = %+v", result)
	}
	// A timeout says nothing about the settings, so its next step is a retry and the command that
	// switches the loophole off, never "correct the settings".
	for _, want := range []string{"Retry the launch", "`yolo loopholes disable fixture`"} {
		if !strings.Contains(result.Remedy, want) {
			t.Errorf("timeout remedy %q lacks the next step %q", result.Remedy, want)
		}
	}
	if strings.Contains(result.Remedy, "Correct the host service settings") {
		t.Errorf("timeout remedy blames the settings: %q", result.Remedy)
	}
	if entries, err := os.ReadDir(StateDirFor(lp.Name)); err != nil || len(entries) != 0 {
		t.Fatalf("timeout left a validator snapshot behind: entries=%v err=%v", entries, err)
	}
	lp.HostDaemon.SettingsCheck = []string{"/bin/sh", "-c", "exit 9", "validator", "{settings}"}
	result = RunSettingsCheck(lp, frozen)
	if result.Outcome != hostservice.CommandRefused || result.Reason == "" || result.Remedy == "" {
		t.Fatalf("empty refusal result = %+v", result)
	}
	if entries, err := os.ReadDir(StateDirFor(lp.Name)); err != nil || len(entries) != 0 {
		t.Fatalf("refusal left a validator snapshot behind: entries=%v err=%v", entries, err)
	}
}
