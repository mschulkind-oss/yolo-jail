package loopholedecl_test

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

func settingsCheckManifest(argv any, topLevel bool) map[string]any {
	manifest := launchCheckManifest(nil)
	manifest["host_daemon"].(map[string]any)["cmd"] = []any{"daemon", "--settings", "{settings}"}
	if argv != nil {
		manifest["host_daemon"].(map[string]any)["settings_check"] = argv
	}
	manifest["settings"] = map[string]any{
		"region": map[string]any{"type": "string", "default": "us-east-1"},
	}
	if topLevel {
		manifest["settings_check"] = []any{"validator", "{settings}"}
	}
	return manifest
}

func TestSettingsCheckDecodesArgvAndPreservesLegacyManifest(t *testing.T) {
	legacy, err := decodeMap(t, "checked", launchCheckManifest(nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(legacy.HostDaemon.SettingsCheck) != 0 {
		t.Fatalf("legacy settings_check = %v", legacy.HostDaemon.SettingsCheck)
	}
	manifest := settingsCheckManifest([]any{"validator", "--settings", "{settings}", "--module", "{loophole_dir}"}, false)
	decoded, err := decodeMap(t, "checked", manifest)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"validator", "--settings", "{settings}", "--module", "{loophole_dir}"}
	if len(decoded.HostDaemon.SettingsCheck) != len(want) {
		t.Fatalf("decoded settings_check = %v", decoded.HostDaemon.SettingsCheck)
	}
	for i := range want {
		if decoded.HostDaemon.SettingsCheck[i] != want[i] {
			t.Fatalf("decoded settings_check = %v, want %v", decoded.HostDaemon.SettingsCheck, want)
		}
	}
}

func TestSettingsCheckRejectsInvalidArgvPlacementAndMissingSettings(t *testing.T) {
	for _, tc := range []struct {
		name          string
		argv          any
		validSettings bool
		want          string
	}{
		{"not-list", "validator", true, "settings_check"},
		{"empty", []any{}, true, "settings_check"},
		{"non-string", []any{"validator", 1, "{settings}"}, true, "settings_check"},
		{"no-snapshot-token", []any{"validator"}, true, "private snapshot"},
		{"control", []any{"validator", "{settings}\n"}, true, "control character"},
		{"jail-token", []any{"validator", "{jail_loophole_dir}", "{settings}"}, true, "the HOST"},
		{"missing-settings", []any{"validator", "{settings}"}, false, "requires at least one declared 'settings'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := launchCheckManifest(nil)
			manifest["host_daemon"].(map[string]any)["cmd"] = []any{"daemon", "--settings", "{settings}"}
			if tc.argv != nil {
				manifest["host_daemon"].(map[string]any)["settings_check"] = tc.argv
			}
			if tc.validSettings {
				manifest["settings"] = map[string]any{"region": map[string]any{"type": "string", "default": "x"}}
			}
			manifest["name"] = "bad"
			_, err := decodeMap(t, "bad", manifest)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
	badDaemon := settingsCheckManifest([]any{"validator", "{settings}"}, false)
	badDaemon["name"] = "bad-daemon"
	badDaemon["host_daemon"].(map[string]any)["cmd"] = []any{"daemon"}
	if _, err := decodeMap(t, "bad-daemon", badDaemon); err == nil || !strings.Contains(err.Error(), "host_daemon.cmd") {
		t.Fatalf("settings_check accepted a daemon that does not consume its settings: %v", err)
	}
	manifest := settingsCheckManifest([]any{"validator", "{settings}"}, true)
	manifest["name"] = "misplaced"
	_, err := decodeMap(t, "misplaced", manifest)
	if err == nil || !strings.Contains(err.Error(), "settings_check") {
		t.Fatalf("top-level settings_check was accepted: %v", err)
	}
	_, _, err = loopholedecl.DecodeTolerant(manifestBytes(t, manifest), "/loopholes/misplaced")
	if err != nil {
		t.Fatalf("tolerant decoder should preserve its existing future-key behavior: %v", err)
	}
}
