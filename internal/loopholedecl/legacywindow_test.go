package loopholedecl_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// legacywindow_test.go pins the manifest window (docs/design/legacy-config-compat.md LC-D10): a
// LEGACY manifest (manifest.jsonc, manifest.json) that declares `default_enabled` or a
// workspace-scope setting is read as what it meant in v0.15.1, never refused. A loophole.toml is
// refused as before (TestDefaultEnabledIsRefusedOnEveryManifest).

// decodeBoth runs the strict and the tolerant decoder over a legacy fragment.
func decodeBoth(t *testing.T, fragment string) map[string]*loopholedecl.Manifest {
	t.Helper()
	out := map[string]*loopholedecl.Manifest{}
	strict, err := loopholedecl.Decode(bodyWith(fragment), probeDir)
	if err != nil {
		t.Fatalf("strict decode of %s: %v", fragment, err)
	}
	out["strict"] = strict
	tolerant, _, err := loopholedecl.DecodeTolerant(bodyWith(fragment), probeDir)
	if err != nil {
		t.Fatalf("tolerant decode of %s: %v", fragment, err)
	}
	out["tolerant"] = tolerant
	return out
}

// TestALegacyManifestsDefaultEnabledTrueReadsAsNothing: `default_enabled: true` meant the loophole
// ran when its pack was selected, which is what every loophole now does, so the manifest decodes
// exactly as one without the key.
func TestALegacyManifestsDefaultEnabledTrueReadsAsNothing(t *testing.T) {
	without := decodeBoth(t, "")
	for name, m := range decodeBoth(t, `"default_enabled": true`) {
		if !reflect.DeepEqual(m, without[name]) {
			t.Errorf("%s: default_enabled: true decoded to %+v, want the manifest without the key %+v", name, m, without[name])
		}
	}
}

// TestALegacyManifestsDefaultEnabledFalseIsOffUntilSwitchedOn: `default_enabled: false` meant the
// loophole ran only where a config switched it on. It reads as a loophole off until this launch
// holds a grant, with the one grant kind the legacy projection makes of a `loopholes.<name>.enabled:
// true` (LegacyEnable), so the switch, its TOML conversion and `yolo request` all have a spelling.
func TestALegacyManifestsDefaultEnabledFalseIsOffUntilSwitchedOn(t *testing.T) {
	for name, m := range decodeBoth(t, `"default_enabled": false`) {
		if !m.LegacyDefaultOff {
			t.Errorf("%s: LegacyDefaultOff is false; the loophole would run with no switch", name)
		}
		if len(m.Grants) != 1 {
			t.Fatalf("%s: grants %+v, want the one legacy run kind", name, m.Grants)
		}
		g := m.Grants[0]
		if g.Kind != loopholedecl.LegacyRunGrantKind || !g.LegacyEnable || g.Valued || g.RequestableByDefault {
			t.Errorf("%s: grant %+v, want kind %q, legacy_enable, not valued, not requestable by default",
				name, g, loopholedecl.LegacyRunGrantKind)
		}
		if strings.TrimSpace(g.Text) == "" || strings.TrimSpace(m.Access) == "" {
			t.Errorf("%s: the grant's text %q or the access line %q is empty; the gate prints both", name, g.Text, m.Access)
		}
	}
	// A manifest that declares its own grant kinds keeps them: the window adds none.
	own := `"default_enabled": false, "access": "read notes", "grants": [{"kind": "read", "text": "read notes", "legacy_enable": true}]`
	for name, m := range decodeBoth(t, own) {
		if !m.LegacyDefaultOff || len(m.Grants) != 1 || m.Grants[0].Kind != "read" {
			t.Errorf("%s: LegacyDefaultOff %v, grants %+v; want the author's one kind, off until granted", name, m.LegacyDefaultOff, m.Grants)
		}
	}
	// v0.15.1 type-checked the key and never coerced it.
	for _, tolerant := range []bool{false, true} {
		err := decodeErr(t, `"default_enabled": "yes"`, tolerant)
		if err == nil || !strings.Contains(err.Error(), "default_enabled") || !strings.Contains(err.Error(), "true or false") {
			t.Errorf("tolerant=%v: a non-boolean default_enabled: %v", tolerant, err)
		}
	}
}

// TestALegacyManifestsWorkspaceScopeSettingIsARequestScopeSetting: `scope: "workspace"` let either
// config supply the value, the workspace's winning. It reads as a request-scope setting, the user's
// value every project's default and a project's replacing it there, which a TOML project can spell
// under [requests.<pack>] (LC-D17's reading of serial's `default_baud`).
func TestALegacyManifestsWorkspaceScopeSettingIsARequestScopeSetting(t *testing.T) {
	for name, m := range decodeBoth(t, `"settings": {"greeting": {"type": "string", "scope": "workspace"}}`) {
		if len(m.Settings) != 1 || m.Settings[0].Scope != loopholedecl.SettingScopeRequest {
			t.Errorf("%s: settings %+v, want greeting at request scope", name, m.Settings)
		}
	}
}

// TestTheLegacyWindowIsNotOpenToALoopholeTOML: the window is for a manifest written before
// loophole.toml existed; a loophole.toml declaring either spelling is refused, as LG-D3 and LG-D5
// rule.
func TestTheLegacyWindowIsNotOpenToALoopholeTOML(t *testing.T) {
	const dir = "packs/probe/loopholes/probe"
	for _, body := range []string{
		"name = \"probe\"\ntransport = \"none\"\ndefault_enabled = false\n",
		"name = \"probe\"\ntransport = \"none\"\n[settings.visible]\ntype = \"string_list\"\nscope = \"workspace\"\n",
	} {
		if _, err := loopholedecl.Decode([]byte(body), dir); err == nil {
			t.Errorf("a loophole.toml decoded:\n%s", body)
		}
		if _, _, err := loopholedecl.DecodeTolerant([]byte(body), dir); err == nil {
			t.Errorf("a loophole.toml decoded tolerantly:\n%s", body)
		}
	}
}

// TestLegacyNotesNameWhatTheWindowRead: `yolo pack lint` passes a legacy manifest the window reads,
// with a note per key saying what it reads as and what `yolo pack migrate` will ask of the author.
func TestLegacyNotesNameWhatTheWindowRead(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "probe")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, loopholedecl.ManifestName), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(string(bodyWith(`"default_enabled": false, "settings": {"greeting": {"type": "string", "scope": "workspace"}}`)))
	notes := strings.Join(loopholedecl.LegacyNotesDir(dir), "\n")
	for _, want := range []string{"default_enabled: false", "settings.greeting.scope", "request", "[[grants]]"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes do not name %q:\n%s", want, notes)
		}
	}
	write(string(bodyWith("")))
	if notes := loopholedecl.LegacyNotesDir(dir); len(notes) != 0 {
		t.Errorf("a manifest the window reads nothing of has notes: %q", notes)
	}
}
