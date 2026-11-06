package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestHostImportExclusions(t *testing.T) {
	requireJail(t)

	const hostSettings = `{"hooks":{"PreToolUse":[{"matcher":"BASH","command":"host-only-exclusion-sentinel"}]},"env":{"YOLO_HOST_IMPORT_KEEP":"host-sibling-sentinel"},"permissions":{"defaultMode":"host-only-exclusion-sentinel"}}`

	writeHostSettings := func(t *testing.T) []byte {
		t.Helper()
		path := filepath.Join(os.Getenv("HOME"), ".claude", "settings.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(hostSettings), 0o600); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}

	readSettings := func(t *testing.T, dir string) map[string]any {
		t.Helper()
		var settings map[string]any
		if err := json.Unmarshal(renderedSurface(t, dir, "claude", "settings.json"), &settings); err != nil {
			t.Fatalf("decode rendered Claude settings: %v", err)
		}
		return settings
	}

	t.Run("excludes only requested host members and keeps pack contribution", func(t *testing.T) {
		packHome(t, `{"packs":["claude"],"host_import_exclusions":{"claude/settings":["/hooks","/permissions/defaultMode"],"pi/settings":["/dormant"]}}`)
		hostBefore := writeHostSettings(t)
		dir := writeProject(t, `{}`)
		result := runYoloDirect(t, dir, "bash", "-lc", "true")
		if result.rc != 0 {
			t.Fatalf("launch with host import exclusions failed (rc %d):\n%s\n%s", result.rc, result.stdout, result.stderr)
		}

		settings := readSettings(t, dir)
		if _, present := settings["hooks"]; present {
			t.Errorf("excluded host-only hooks were imported: %#v", settings["hooks"])
		}
		env, ok := settings["env"].(map[string]any)
		if !ok || env["YOLO_HOST_IMPORT_KEEP"] != "host-sibling-sentinel" {
			t.Errorf("unexcluded host sibling = %#v, want host-sibling-sentinel", settings["env"])
		}
		permissions, ok := settings["permissions"].(map[string]any)
		if !ok || permissions["defaultMode"] != "acceptEdits" {
			t.Errorf("the matching non-host pack contribution was filtered: permissions=%#v, want managed defaultMode acceptEdits", settings["permissions"])
		}
		if _, err := os.Stat(filepath.Join(jailHome(dir), "pi", "agent", "settings.json")); !os.IsNotExist(err) {
			t.Errorf("known unselected pi target was not dormant: stat err=%v", err)
		}
		hostAfter, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".claude", "settings.json"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(hostAfter, hostBefore) {
			t.Errorf("host source changed across launch: before %q, after %q", hostBefore, hostAfter)
		}
	})

	t.Run("empty policy preserves imported host values", func(t *testing.T) {
		packHome(t, `{"packs":["claude"],"host_import_exclusions":{}}`)
		hostBefore := writeHostSettings(t)
		dir := writeProject(t, `{}`)
		result := runYoloDirect(t, dir, "bash", "-lc", "true")
		if result.rc != 0 {
			t.Fatalf("launch with empty exclusions failed (rc %d):\n%s\n%s", result.rc, result.stdout, result.stderr)
		}

		settings := readSettings(t, dir)
		if _, present := settings["hooks"]; !present {
			t.Errorf("empty policy removed imported host hooks: %#v", settings)
		}
		env, ok := settings["env"].(map[string]any)
		if !ok || env["YOLO_HOST_IMPORT_KEEP"] != "host-sibling-sentinel" {
			t.Errorf("empty policy changed imported sibling: env=%#v", settings["env"])
		}
		hostAfter, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".claude", "settings.json"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(hostAfter, hostBefore) {
			t.Errorf("empty policy changed host source: before %q, after %q", hostBefore, hostAfter)
		}
	})
}
