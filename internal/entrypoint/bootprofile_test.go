package entrypoint

// bootprofile_test.go pins that the BOOT LOOP's profile variant comes from the jail's
// YOLO_PACK_PROFILES table, the way bootautonomy_test.go pins the posture from the
// confinement profile.
//
// Same reason, same trap: the profile fold lives INSIDE packload.SurfacesFor, so the
// shipped-pack prism tests (which drive ConfigurePackByName) and packload's own tests both
// stay green if the boot loop simply stops handing the table down — and that would make a
// selected variant write nothing in a real jail, silently, while every unit test stayed
// green. So the assertion is on the loop the entrypoint actually runs.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// profileBootPack declares an autonomy posture and one named variant over ONE surface it
// owns, both touching the same key — so the file can only come out right when the fold
// order (posture, then variant) ran in that order.
func profileBootPack(t *testing.T) *packload.Pack {
	t.Helper()
	patch := func(mode string) json.RawMessage {
		raw, err := json.Marshal([]any{map[string]any{
			"agent": "acme", "name": "settings", "codec": "json",
			"path":    "~/.acme/settings.json",
			"managed": map[string]any{"mode": mode},
		}})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	base, err := json.Marshal([]any{map[string]any{
		"agent": "acme", "name": "settings", "codec": "json",
		"path":    "~/.acme/settings.json",
		"managed": map[string]any{"benign": true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return &packload.Pack{Name: "acme", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindProgram, Bin: "acme", Via: "npm", Package: "acme"},
			{Kind: packdecl.KindConfig, Raw: base},
			{Kind: packdecl.KindAutonomy,
				Autonomous: &packdecl.AutonomyPosture{Config: patch("bypass")},
				Guarded:    &packdecl.AutonomyPosture{Config: patch("prompt")}},
			{Kind: packdecl.KindProfile, Name: "bedrock", Raw: patch("bedrock")},
		},
	}}
}

// renderAcmeSettings drives the boot loop over profileBootPack and returns the rendered
// settings file parsed.
func renderAcmeSettings(t *testing.T, profiles string) map[string]any {
	t.Helper()
	var errw bytes.Buffer
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{
		"YOLO_PACK_PROFILES": profiles,
	}, Stderr: &errw}
	withCtxRoot(t, t.TempDir(), "acme")

	ConfigurePackSurfaces(e, []*packload.Pack{profileBootPack(t)})
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("boot render failed: %v", fails)
	}
	data, err := os.ReadFile(filepath.Join(e.Home, ".acme", "settings.json"))
	if err != nil {
		t.Fatalf("read rendered surface: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse rendered surface: %v\n%s", err, data)
	}
	return got
}

// A variant named in the jail's profile table folds AFTER the posture, so a key both touch
// reads the variant's value and the pack's own managed key survives.
func TestBootRenderFoldsTheSelectedProfile(t *testing.T) {
	got := renderAcmeSettings(t, `{"acme":"bedrock"}`)
	if got["mode"] != "bedrock" {
		t.Errorf("the selected variant must fold AFTER the autonomy posture (later-wins), "+
			"got mode=%v:\n%v", got["mode"], got)
	}
	if got["benign"] != true {
		t.Errorf("the pack's own managed key must survive the variant fold:\n%v", got)
	}
}

// No selection (or a name this pack does not declare): the posture is the last word, and
// the variant contributes nothing — a launch that selected no profile must not render one.
func TestBootRenderWithoutAProfileSelectionRendersThePostureOnly(t *testing.T) {
	for _, table := range []string{``, `{"acme":"nobody"}`, `{"claude":"bedrock"}`} {
		got := renderAcmeSettings(t, table)
		if got["mode"] != "bypass" {
			t.Errorf("table %q: the autonomous posture must be the last word, got mode=%v",
				table, got["mode"])
		}
	}
}
