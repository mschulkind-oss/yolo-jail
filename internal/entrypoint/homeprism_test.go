package entrypoint

// homeprism_test.go pins the capture store per home (named-setups-plan.md SUP-D35): a jail's boot
// keeps its prism baseline beside the home its launch bound (paths.HomeStateEnv), so two setups of
// one project, each with its own home, never diff one home against the other's render. With one
// store per project (the 6a-ii review's M2 reproduction), setup A's boot after an edit captured in
// setup B read A's stale file as an edit back to the old value, and the overlay lost B's edit.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// bootHome runs the boot loop over f's packs into home, as a launch that bound homeRel tells it.
func (f *providerKeyFixture) bootHome(home, homeRel string) map[string]any {
	t := f.t
	t.Helper()
	providers, err := packload.ComposeProviders(nil, f.packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveShippedProfiles(f.packs, nil, providers)
	if err != nil {
		t.Fatal(err)
	}
	errw := &bytes.Buffer{}
	e := &Env{Home: home, Workspace: f.ws, Stderr: errw, Vars: map[string]string{
		"YOLO_PROVIDERS":    mustCompactJSON(t, providers),
		"YOLO_PROFILES":     mustCompactJSON(t, packload.ProfilesWireTable(resolved)),
		"YOLO_USE_PROFILES": mustCompactJSON(t, jsonx.NewOrderedMap()),
		paths.HomeStateEnv:  homeRel,
	}}
	ConfigurePackSurfaces(e, f.packs)
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("boot failed: %v\n%s", fails, errw)
	}
	return readSettingsAt(t, home)
}

func readSettingsAt(t *testing.T, home string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("settings are not JSON: %v\n%s", err, data)
	}
	return m
}

// TestEachHomesBootKeepsItsOwnBaseline: the user sets a key in A, then a different value in B;
// each boot captures its own home's edit. A's next boot must not read A's file, which still holds
// A's value, as an edit back against B's baseline, and B's file must keep B's value after it; nor
// may B ever receive A's value. Each store sits beside its home. Red if the boot's render target
// stops taking the home its launch bound (Env.renderTarget's WithHomeState): with one store for
// the project, A's boot captured "system" back over B's "solarized", and B's next boot lost it.
func TestEachHomesBootKeepsItsOwnBaseline(t *testing.T) {
	f := newProviderKeyFixture(t, nil)
	homeA, homeB := t.TempDir(), t.TempDir()
	const relA, relB = "setups/a/home", "setups/b/home"
	editTheme := func(home, theme string) {
		t.Helper()
		m := readSettingsAt(t, home)
		m["theme"] = theme
		data, _ := json.MarshalIndent(m, "", "  ")
		if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	f.bootHome(homeA, relA)
	editTheme(homeA, "system")
	if got := f.bootHome(homeA, relA); got["theme"] != "system" {
		t.Fatalf("A's own boot lost A's edit, so the premise is wrong: %v", got)
	}
	if got := f.bootHome(homeB, relB); got["theme"] != nil {
		t.Errorf("B's first boot received A's edit (theme %v): one setup's capture reached another's home", got["theme"])
	}
	editTheme(homeB, "solarized")
	if got := f.bootHome(homeB, relB); got["theme"] != "solarized" {
		t.Fatalf("B's own boot lost B's edit, so the premise is wrong: %v", got)
	}

	if got := f.bootHome(homeA, relA); got["theme"] != "system" {
		t.Errorf("A's boot lost A's own edit (theme %v)", got["theme"])
	}
	if got := f.bootHome(homeB, relB); got["theme"] != "solarized" {
		t.Errorf("after A booted, B's edit is gone from B's file (theme %v): A's boot diffed its own "+
			"home against B's baseline and captured A's value back", got["theme"])
	}

	overlayB := filepath.Join(paths.SetupStateDir(f.ws, "b"), "prism", "claude-settings.overlay.json")
	ov, err := os.ReadFile(overlayB)
	if err != nil {
		t.Fatalf("B's capture is not in the store beside B's home: %v", err)
	}
	var overlay map[string]any
	if err := json.Unmarshal(ov, &overlay); err != nil || overlay["theme"] != "solarized" {
		t.Errorf("B's store does not hold B's edit: %s (%v)", ov, err)
	}
	if _, err := os.Stat(filepath.Join(paths.SetupStateDir(f.ws, "a"), "prism", "claude-settings.last_render")); err != nil {
		t.Errorf("A's baseline is not beside A's home: %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths.WorkspaceStateDir(f.ws), "prism")); !os.IsNotExist(err) {
		t.Errorf("a setup's boot wrote the legacy home's store .yolo/prism (err %v)", err)
	}
}

// TestABootWithNoBoundHomeKeepsTheLegacyStore: a session whose launch named no home (a yolo from
// before SUP-D35), or the legacy home, keeps `.yolo/prism`, where every earlier boot left it.
func TestABootWithNoBoundHomeKeepsTheLegacyStore(t *testing.T) {
	for _, rel := range []string{"", "home", "../escape"} {
		f := newProviderKeyFixture(t, nil)
		f.bootHome(t.TempDir(), rel)
		if _, err := os.Stat(filepath.Join(paths.WorkspaceStateDir(f.ws), "prism", "claude-settings.last_render")); err != nil {
			t.Errorf("home %q: the baseline is not in the legacy store: %v", rel, err)
		}
	}
}
