package packoverlay

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// C3, as rewritten for posture lists (docs/design/notch-scoped-config-contributions.md §4.1).
// Collect's `autonomy` parameter has TWO effects and this file pins both halves, so neither
// can drift while looking like the other.
//
// THE FIRST IS THAT IT NEVER CHANGES SURFACE IDENTITIES OR OWNERSHIP. Collect's owner pass
// reads only surface IDENTITIES, and the posture fold — packload.foldPostureManaged — merges
// keys into the `Managed` layer of surfaces ALREADY present, leaving any patch that names no
// base surface to the overlay pass as a posture overlay (OQ-3), which never owns a surface. So
// both postures yield the same identity set by construction, and which
// overlays and plain config-lists find an owner cannot depend on the bit. If a future change
// makes the posture able to ADD or REMOVE a surface identity,
// TestCollectAutonomyDoesNotChangeTheResolution fails — the correct alarm, because at that
// moment every caller's autonomy argument starts deciding which contributions find an owner.
//
// THE SECOND IS THAT IT SELECTS POSTURE LISTS, and that one used to be the opposite claim.
// Until posture lists existed this file said the bit had NO observable effect on the output —
// inverting it at every caller left the suite green, and that survival was a property rather
// than missing coverage. It is not any more: `guarded.lists` are placed while the bit is off
// and `autonomous.lists` while it is on (TestCollectAutonomySelectsPostureLists), so an
// inverted argument at a caller now moves a host-only entry into a jail. Where the bit is
// consequential for the surfaces themselves — p.SurfacesFor at the render — it is pinned by
// internal/entrypoint/bootautonomy_test.go in BOTH directions.
func TestCollectAutonomyDoesNotChangeTheResolution(t *testing.T) {
	// A pack whose autonomy postures patch the surface it owns AND name one it does not.
	// The second half is the case that would break the identity half if the fold ever created
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
	// phantom (must be an ORPHAN at both — a posture patch is not a declaration). The same
	// pair for a plain config-list, which carries no posture either.
	contributor := overlayPack("helper", "acme/settings", map[string]any{"k": 1})
	phantomOverlay := overlayPack("hopeful", "acme/phantom", map[string]any{"k": 2})
	lister := listPack("lister", listContribution("acme/settings", "/tags", `["a"]`),
		listContribution("acme/phantom", "/tags", `["b"]`))

	packs := []*packload.Pack{owner, contributor, phantomOverlay, lister}
	on := Collect(packs, true, nil)
	off := Collect(packs, false, nil)

	if len(on.For("acme", "settings")) != 1 || len(off.For("acme", "settings")) != 1 {
		t.Errorf("the owned surface must carry its overlay at BOTH postures: on=%d off=%d",
			len(on.For("acme", "settings")), len(off.For("acme", "settings")))
	}
	if len(on.ListsFor("acme", "settings")) != 1 || len(off.ListsFor("acme", "settings")) != 1 {
		t.Errorf("the owned surface must carry the plain config-list at BOTH postures: on=%d off=%d",
			len(on.ListsFor("acme", "settings")), len(off.ListsFor("acme", "settings")))
	}
	// The phantom identity exists only inside the GUARDED posture's patch. If the fold ever
	// promoted a patch to a declaration, `off` would own it and these would stop being
	// orphans — the exact asymmetry that would make the bit decide ownership.
	for _, c := range []struct {
		name string
		set  *OverlaySet
	}{{"autonomy ON", on}, {"autonomy OFF", off}} {
		if got := len(c.set.For("acme", "phantom")) + len(c.set.ListsFor("acme", "phantom")); got != 0 {
			t.Errorf("%s: a posture patch must not create a surface a contribution can own (got %d)",
				c.name, got)
		}
		var targets []string
		for _, o := range c.set.Orphans {
			targets = append(targets, o.KindName()+" "+o.Target)
		}
		// Sorted by target, then pack: hopeful's overlay before lister's list. At the guarded
		// posture the phantom patch itself is one more orphan, acme's own, sorting first: since
		// OQ-3 a posture patch on a surface its pack does not declare is a POSTURE OVERLAY, and
		// an ownerless one is reported where its posture is selected (postureoverlay_test.go).
		// It is an orphan, not an owner, which is the identity half this test is for.
		want := []string{"config-overlay acme/phantom", "config-list acme/phantom"}
		if c.set == off {
			want = append([]string{"autonomy acme/phantom"}, want...)
		}
		if !reflect.DeepEqual(targets, want) {
			t.Errorf("%s: orphans = %v, want exactly %v", c.name, targets, want)
		}
	}
	// And the whole reported picture is identical, not merely equivalent where we looked.
	if len(on.Problems) != 0 || len(off.Problems) != 0 {
		t.Errorf("well-formed input reported problems: on=%v off=%v", on.Problems, off.Problems)
	}
	if a, b := on.Applied(), off.Applied(); !reflect.DeepEqual(a, b) {
		t.Errorf("Applied() differs by posture: %+v vs %+v", a, b)
	}
	if a, b := on.AppliedLists(), off.AppliedLists(); !reflect.DeepEqual(a, b) {
		t.Errorf("AppliedLists() differs by posture with no posture list in the set: %+v vs %+v", a, b)
	}
}

// THE SECOND HALF: the bit selects posture lists, in both directions, and an unselected
// posture's list is a CLEAN SKIP — no problem, no orphan, no applied row. Delete the posture
// gate in Collect and both postures' entries land at every notch, which is the leak the gate
// exists to prevent (the design's motivating case: a permission gate for host pi that costs
// tokens and prompts in every jail).
func TestCollectAutonomySelectsPostureLists(t *testing.T) {
	packs := []*packload.Pack{
		ownerPack("claude"),
		postureListPack("matt",
			[]packdecl.PostureList{{Surface: "claude/settings", Path: "/extra", Add: json.RawMessage(`["jail-only"]`)}},
			[]packdecl.PostureList{{Surface: "claude/settings", Path: "/extra", Add: json.RawMessage(`["host-only"]`)}}),
	}
	for _, c := range []struct {
		autonomy bool
		want     string
	}{{true, "jail-only"}, {false, "host-only"}} {
		set := Collect(packs, c.autonomy, nil)
		if len(set.Problems) != 0 || len(set.Orphans) != 0 {
			t.Fatalf("autonomy=%v: an unselected posture's list must be a clean skip: problems=%v "+
				"orphans=%+v", c.autonomy, set.Problems, set.Orphans)
		}
		// Exactly one list: the selected posture's, which is what excludes the other.
		got := set.ListsFor("claude", "settings")
		if len(got) != 1 || !reflect.DeepEqual(got[0].Add, []any{c.want}) || got[0].Pack != "matt" {
			t.Fatalf("autonomy=%v: lists = %+v, want only matt's %q", c.autonomy, got, c.want)
		}
		if applied := set.AppliedLists(); len(applied) != 1 ||
			!reflect.DeepEqual(applied[0].Packs, []string{"matt"}) {
			t.Errorf("autonomy=%v: AppliedLists = %+v, want the one surface naming matt",
				c.autonomy, applied)
		}
	}
}

// postureListPack is a pack whose ONLY contribution is an autonomy declaration carrying
// posture lists — the shape a personal pack takes when all it adds is an entry for one
// posture. A nil slice leaves that posture undeclared.
func postureListPack(name string, autonomous, guarded []packdecl.PostureList) *packload.Pack {
	c := packdecl.Contribution{Kind: packdecl.KindAutonomy}
	if autonomous != nil {
		c.Autonomous = &packdecl.AutonomyPosture{Lists: autonomous}
	}
	if guarded != nil {
		c.Guarded = &packdecl.AutonomyPosture{Lists: guarded}
	}
	return &packload.Pack{Name: name, Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{c}}}
}
