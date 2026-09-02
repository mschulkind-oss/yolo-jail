package entrypoint

// bootprofile_test.go pins that the BOOT LOOP's gated overlay comes from the jail's
// YOLO_USE_PROFILES table, the way bootautonomy_test.go pins the posture from the
// confinement profile.
//
// Same reason, same trap: the gating lives INSIDE ConfigurePackSurfaces (it hands
// packoverlay.Collect the table it read), so packoverlay's own tests and packload's both
// stay green if the boot loop simply stops handing the table down — and that would make a
// selected profile contribute nothing in a real jail, silently, while every unit test
// stayed green. So the assertion is on the loop the entrypoint actually runs.
//
// The precedence is the one thing the shrink CHANGED here: the old variant patch merged
// into the surface's managed layer, above the posture; the config-overlay slot composes
// BELOW it, so the owner (posture included) keeps the last word on a key both name. That
// ordering is asserted on purpose — a revert to the old fold would fail the third case
// below rather than pass silently.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// profileBootPack declares an autonomy posture and one profile selection over ONE surface
// it owns, with the profile's config half in its new home: a gated config-overlay. All
// three touch the same file, and two touch the same key, so the file can only come out
// right when the gate fired AND the precedence (managed above overlay) held.
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
	gated, err := json.Marshal(map[string]any{
		"managed": map[string]any{"gated": "yes", "mode": "from-overlay"},
	})
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
			{Kind: packdecl.KindProfile, Name: "bedrock", Provider: "bedrock"},
			{Kind: packdecl.KindConfigOverlay, Surface: "acme/settings", Profile: "bedrock", Raw: gated},
		},
	}}
}

// renderAcmeSettings drives the boot loop over profileBootPack and returns the rendered
// settings file parsed.
func renderAcmeSettings(t *testing.T, profiles string) map[string]any {
	t.Helper()
	var errw bytes.Buffer
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{
		"YOLO_USE_PROFILES": profiles,
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

// A profile named in the jail's table delivers its overlay's keys to the rendered file.
func TestBootRenderDeliversTheSelectedProfileOverlay(t *testing.T) {
	got := renderAcmeSettings(t, `{"acme":"bedrock"}`)
	if got["gated"] != "yes" {
		t.Errorf("the selected profile's overlay keys must reach the render, got:\n%v", got)
	}
	if got["benign"] != true {
		t.Errorf("the pack's own managed key must survive the overlay:\n%v", got)
	}
}

// No selection (or a name this pack does not declare): the overlay is a clean skip, and a
// launch that selected no profile must not render one.
func TestBootRenderWithoutAProfileSelectionRendersNoOverlay(t *testing.T) {
	for _, table := range []string{``, `{"acme":"nobody"}`, `{"claude":"bedrock"}`} {
		got := renderAcmeSettings(t, table)
		if got["gated"] != nil {
			t.Errorf("table %q: an unselected profile's overlay must render nothing, got:\n%v",
				table, got)
		}
		if got["mode"] != "bypass" {
			t.Errorf("table %q: the autonomous posture must be the last word, got mode=%v",
				table, got["mode"])
		}
	}
}

// The one precedence the shrink changed: the overlay folds BELOW the owner's managed
// layer, so a key the posture also writes reads the POSTURE's value even with the profile
// selected. The old fold had the variant winning; pinning the new answer is what makes the
// change a recorded consequence rather than a silent demotion.
func TestBootRenderKeepsTheOwnersManagedAboveTheOverlay(t *testing.T) {
	got := renderAcmeSettings(t, `{"acme":"bedrock"}`)
	if got["mode"] != "bypass" {
		t.Errorf("the owner's managed key must win over the gated overlay's, got mode=%v:\n%v",
			got["mode"], got)
	}
}
