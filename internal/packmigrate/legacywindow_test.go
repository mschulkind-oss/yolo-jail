package packmigrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// legacywindow_test.go pins `yolo pack migrate`'s half of the legacy manifest window
// (docs/design/legacy-config-compat.md LC-D10): `default_enabled: true` converts to nothing, and
// `default_enabled: false` and a workspace-scope setting are refused naming what the author must
// restate, since a manifest is the author's and loophole.toml cannot say either.

func writeProbeManifest(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "loopholes", "probe", "manifest.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateDropsALegacyDefaultEnabledTrue(t *testing.T) {
	dir := loopholePackFixture(t, "pack.toml", loopholePackTOML)
	writeProbeManifest(t, dir, `{
  "name": "probe",
  "transport": "none",
  "default_enabled": true,
  "settings": {"greeting": {"type": "string", "scope": "user", "default": "hi"}}
}`)
	m, err := Plan(dir, Options{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(m.Loopholes) != 1 {
		t.Fatalf("plan = %+v, want one loophole", m)
	}
	toml := string(m.Loopholes[0].TOML)
	if strings.Contains(toml, "default_enabled") {
		t.Errorf("the converted manifest still says default_enabled:\n%s", toml)
	}
	if !strings.Contains(toml, "greeting") {
		t.Errorf("the converted manifest lost the rest of the declaration:\n%s", toml)
	}
	// What it wrote is a manifest this yolo reads, as the one it replaces read.
	if _, err := loopholedecl.Decode([]byte(toml), filepath.Join(dir, "loopholes", "probe")); err != nil {
		t.Errorf("the converted manifest does not decode: %v\n%s", err, toml)
	}
}

func TestMigrateRefusesWhatOnlyTheAuthorCanRestate(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       []string
	}{
		{"default_enabled false", `{"name": "probe", "transport": "none", "default_enabled": false}`,
			[]string{"default_enabled: false", "[[grants]]", "yolo pack migrate"}},
		{"a workspace-scope setting", `{"name": "probe", "transport": "none",
  "settings": {"greeting": {"type": "string", "scope": "workspace", "default": "hi"}}}`,
			[]string{"settings.greeting.scope", `"request"`, "yolo pack migrate"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := loopholePackFixture(t, "pack.toml", loopholePackTOML)
			writeProbeManifest(t, dir, tc.body)
			before := treeSnapshot(t, dir)
			_, err := Plan(dir, Options{})
			if err == nil {
				t.Fatal("Plan converted a manifest whose meaning loophole.toml cannot say")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("refusal does not name %q:\n%v", w, err)
				}
			}
			if !equalSnapshots(before, treeSnapshot(t, dir)) {
				t.Error("a refused migration changed the tree")
			}
		})
	}
}
