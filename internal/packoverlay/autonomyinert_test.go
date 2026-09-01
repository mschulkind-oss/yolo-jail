package packoverlay

import (
	"encoding/json"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// C3. Collect's `autonomy` parameter is INERT — it cannot change any field of the returned
// OverlaySet — and this file pins that as a property rather than leaving it as a comment.
//
// It matters because it is the answer to a MUTATION SURVIVING. Inverting the argument at
// every one of Collect's callers leaves the whole suite green, which normally means the
// call is untested; here it means the parameter genuinely has no observable effect, and the
// two claims are indistinguishable without a test that says which. The reason is
// structural: Collect reads only surface IDENTITIES (to build its owner set), and the
// posture fold — packload.foldPostureManaged — merges keys into the `Managed` layer of
// surfaces ALREADY present, ignoring any patch that names no base surface. So both postures
// yield the same identity set by construction.
//
// This is what makes it safe for the callers to derive the bit from a Target instead of
// passing a literal: the derivation cannot regress behavior here, and where the bit IS
// consequential — p.SurfacesFor at the render itself — it is pinned by
// internal/entrypoint/bootautonomy_test.go in BOTH directions. Keeping the parameter is
// still right (it makes this package unable to disagree with the notch that computed it),
// but a reader deserves to know its effect is zero rather than untested.
//
// If a future change makes the posture able to ADD or REMOVE a surface identity, this test
// fails — which is the correct alarm, because at that moment every caller's autonomy
// argument starts deciding which overlays find an owner.
func TestCollectAutonomyDoesNotChangeTheResolution(t *testing.T) {
	// A pack whose autonomy postures patch the surface it owns AND name one it does not.
	// The second half is the case that would break inertness if the fold ever created
	// surfaces from a patch: only the guarded posture mentions `acme/phantom`.
	base, _ := json.Marshal([]map[string]any{{
		"agent": "acme", "name": "settings", "codec": "json",
		"path": "~/.acme/settings.json", "managed": map[string]any{"benign": true},
	}})
	posture := func(mode string, phantom bool) json.RawMessage {
		surfaces := []map[string]any{{
			"agent": "acme", "name": "settings", "codec": "json",
			"path": "~/.acme/settings.json", "managed": map[string]any{"permissionMode": mode},
		}}
		if phantom {
			surfaces = append(surfaces, map[string]any{
				"agent": "acme", "name": "phantom", "codec": "json",
				"path": "~/.acme/phantom.json", "managed": map[string]any{"x": 1},
			})
		}
		raw, _ := json.Marshal(surfaces)
		return raw
	}
	owner := &packload.Pack{Name: "acme", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindConfig, Raw: base},
			{Kind: packdecl.KindAutonomy,
				Autonomous: &packdecl.AutonomyPosture{Config: posture("bypass", false)},
				Guarded:    &packdecl.AutonomyPosture{Config: posture("prompt", true)}},
		},
	}}
	// One overlay onto the owned surface (must resolve at both postures) and one onto the
	// phantom (must be an ORPHAN at both — a posture patch is not a declaration).
	contributor := overlayPack("helper", "acme/settings", map[string]any{"k": 1})
	phantomOverlay := overlayPack("hopeful", "acme/phantom", map[string]any{"k": 2})

	packs := []*packload.Pack{owner, contributor, phantomOverlay}
	on := Collect(packs, true, nil)
	off := Collect(packs, false, nil)

	if len(on.For("acme", "settings")) != 1 || len(off.For("acme", "settings")) != 1 {
		t.Errorf("the owned surface must carry its overlay at BOTH postures: on=%d off=%d",
			len(on.For("acme", "settings")), len(off.For("acme", "settings")))
	}
	// The phantom identity exists only inside the GUARDED posture's patch. If the fold ever
	// promoted a patch to a declaration, `off` would own it and this overlay would stop
	// being an orphan — the exact asymmetry that would make the parameter consequential.
	for _, c := range []struct {
		name string
		set  *OverlaySet
	}{{"autonomy ON", on}, {"autonomy OFF", off}} {
		if got := len(c.set.For("acme", "phantom")); got != 0 {
			t.Errorf("%s: a posture patch must not create a surface an overlay can own (got %d)",
				c.name, got)
		}
		if len(c.set.Orphans) != 1 || c.set.Orphans[0].Target != "acme/phantom" {
			t.Errorf("%s: want exactly the acme/phantom orphan, got %+v", c.name, c.set.Orphans)
		}
	}
	// And the whole reported picture is identical, not merely equivalent where we looked.
	if len(on.Problems) != 0 || len(off.Problems) != 0 {
		t.Errorf("well-formed input reported problems: on=%v off=%v", on.Problems, off.Problems)
	}
	if a, b := on.Applied(), off.Applied(); len(a) != len(b) {
		t.Errorf("Applied() differs by posture: %+v vs %+v", a, b)
	} else {
		for i := range a {
			if a[i].Target != b[i].Target || len(a[i].Packs) != len(b[i].Packs) {
				t.Errorf("Applied()[%d] differs by posture: %+v vs %+v", i, a[i], b[i])
			}
		}
	}
}
