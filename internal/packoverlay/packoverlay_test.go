package packoverlay

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	_ "github.com/mschulkind-oss/yolo-jail/internal/packreg" // so shippedOwnerOf can see the shipped packs
)

// ownerPack is a pack that DECLARES claude/settings — the Layout C "owner" role.
func ownerPack(name string) *packload.Pack {
	surface, _ := json.Marshal([]map[string]any{{
		"agent": "claude", "name": "settings", "codec": "json",
		"path": "~/.claude/settings.json",
		"managed": map[string]any{
			"preferences": map[string]any{"autoUpdaterStatus": "disabled"},
		},
	}})
	return &packload.Pack{Name: name, Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{{Kind: packdecl.KindConfig, Raw: surface}},
	}}
}

// overlayPack is a pack that CONTRIBUTES to a surface it does not own — the fzf role.
func overlayPack(name, target string, managed map[string]any) *packload.Pack {
	body, _ := json.Marshal(map[string]any{"managed": managed})
	return &packload.Pack{Name: name, Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindConfigOverlay, Surface: target, Raw: body},
		},
	}}
}

// rawOverlayPack is overlayPack with a hand-written body, for the malformed cases.
func rawOverlayPack(name, target, body string) *packload.Pack {
	return &packload.Pack{Name: name, Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindConfigOverlay, Surface: target, Raw: json.RawMessage(body)},
		},
	}}
}

// THE case the kind exists for: an overlay declared by pack B resolves onto a surface
// pack A owns. Cross-pack, so a per-pack collection would find nothing.
func TestOverlayFromOtherPackResolvesOntoOwnersSurface(t *testing.T) {
	set := Collect([]*packload.Pack{
		ownerPack("claude"),
		overlayPack("claude-fzf", "claude/settings", map[string]any{"fileSuggestion": "cmd"}),
	}, true, nil)

	if len(set.Problems) != 0 {
		t.Fatalf("well-formed overlay reported problems: %v", set.Problems)
	}
	if len(set.Orphans) != 0 {
		t.Fatalf("the owner IS selected, so nothing should be orphaned: %+v", set.Orphans)
	}
	got := set.For("claude", "settings")
	if len(got) != 1 {
		t.Fatalf("want 1 overlay on claude/settings, got %d", len(got))
	}
	if got[0].Pack != "claude-fzf" {
		t.Errorf("overlay attributed to %q, want claude-fzf (provenance names the contributor)", got[0].Pack)
	}
	layer, _ := got[0].Data.(map[string]any)
	if layer["fileSuggestion"] != "cmd" {
		t.Errorf("overlay layer = %#v, want the contributed key", got[0].Data)
	}
}

// Pack ORDER, not declaration site, decides which overlay wins a shared key — the same
// "later pack wins" rule every other multi-pack kind uses.
func TestOverlayFoldOrderFollowsPackOrder(t *testing.T) {
	set := Collect([]*packload.Pack{
		ownerPack("claude"),
		overlayPack("team", "claude/settings", map[string]any{"theme": "dark"}),
		overlayPack("personal", "claude/settings", map[string]any{"theme": "gruvbox"}),
	}, true, nil)
	got := set.For("claude", "settings")
	if len(got) != 2 {
		t.Fatalf("want both overlays, got %d", len(got))
	}
	if got[0].Pack != "team" || got[1].Pack != "personal" {
		t.Errorf("fold order = [%s %s], want [team personal] (later pack wins)", got[0].Pack, got[1].Pack)
	}
}

// An overlay whose target has no owner IN THIS SET is inert and named, and the message
// says which pack to select — ruling R2. Ordering matters: the overlay must not appear in
// byTarget at all, or the render would create a file the overlay does not own.
func TestOrphanOverlayIsInertAndNamesTheMissingOwner(t *testing.T) {
	set := Collect([]*packload.Pack{
		overlayPack("claude-fzf", "claude/settings", map[string]any{"fileSuggestion": "cmd"}),
	}, true, nil)

	if got := set.For("claude", "settings"); len(got) != 0 {
		t.Errorf("an ownerless overlay must contribute NOTHING (it would otherwise own the "+
			"surface by accident), got %+v", got)
	}
	if len(set.Problems) != 0 {
		t.Errorf("an unselected owner is not an ERROR (R2: it must not fail the launch): %v", set.Problems)
	}
	if len(set.Orphans) != 1 {
		t.Fatalf("want 1 reported orphan, got %+v", set.Orphans)
	}
	reason := set.Orphans[0].Reason()
	for _, want := range []string{"no effect", "claude/settings", "`claude` pack is not selected"} {
		if !strings.Contains(reason, want) {
			t.Errorf("orphan reason %q missing %q — R2 requires it be reported BY NAME "+
				"with the pack to select", reason, want)
		}
	}
}

// An overlay naming a surface NO pack declares is still inert and reported, but the
// message must not claim a pack is unselected — it is most likely a typo.
func TestOrphanOverlayWithNoOwnerAnywhereSaysSo(t *testing.T) {
	set := Collect([]*packload.Pack{
		overlayPack("typo", "clod/settings", map[string]any{"k": 1}),
	}, true, nil)
	if len(set.Orphans) != 1 {
		t.Fatalf("want 1 orphan, got %+v", set.Orphans)
	}
	reason := set.Orphans[0].Reason()
	if !strings.Contains(reason, "check the identity") {
		t.Errorf("an unknown identity should point at the identity, got %q", reason)
	}
	if strings.Contains(reason, "not selected") {
		t.Errorf("no pack owns clod/settings, so nothing is 'not selected': %q", reason)
	}
}

// A CORE surface (mise/config) is not a pack's, so an overlay onto it is inert with its
// own reason rather than the typo message.
func TestOverlayOntoCoreSurfaceIsInertAndSaysWhy(t *testing.T) {
	set := Collect([]*packload.Pack{
		overlayPack("meddler", "mise/config", map[string]any{"k": 1}),
	}, true, nil)
	if len(set.Orphans) != 1 {
		t.Fatalf("want 1 orphan, got %+v", set.Orphans)
	}
	if reason := set.Orphans[0].Reason(); !strings.Contains(reason, "yolo's OWN surfaces") {
		t.Errorf("a core surface should be named as core-owned, got %q", reason)
	}
}

// Malformed overlays are PROBLEMS, not orphans: the author asserted something the
// mechanism will never honor, which is the same class as a malformed surface.
func TestMalformedOverlaysAreProblems(t *testing.T) {
	cases := []struct {
		name   string
		pack   *packload.Pack
		expect string
	}{
		{"target is not agent/name",
			rawOverlayPack("p", "claudesettings", `{"managed":{"k":1}}`),
			"not an \"agent/name\" identity"},
		{"body redeclares the mode",
			rawOverlayPack("p", "claude/settings", `{"mode":"rmw","managed":{"k":1}}`),
			"may not set \"mode\""},
		{"body redeclares the path",
			rawOverlayPack("p", "claude/settings", `{"path":"~/x.json","managed":{"k":1}}`),
			"may not set \"path\""},
		{"body contributes no keys",
			rawOverlayPack("p", "claude/settings", `{}`),
			"contributes no keys"},
		{"body misspells managed",
			rawOverlayPack("p", "claude/settings", `{"manged":{"k":1}}`),
			"unknown field"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			set := Collect([]*packload.Pack{ownerPack("claude"), tc.pack}, true, nil)
			if len(set.For("claude", "settings")) != 0 {
				t.Error("a malformed overlay must contribute nothing")
			}
			joined := strings.Join(set.Problems, "\n")
			if !strings.Contains(joined, tc.expect) {
				t.Errorf("problems %q missing %q", joined, tc.expect)
			}
		})
	}
}

// The mode-flip hazard R1 names is what an overlay must be UNABLE to do. A `config`
// contribution can silently replace the owner's mode; an overlay body naming `mode` is
// refused, so the correct expression cannot carry the incorrect effect.
func TestOverlayCannotFlipTheOwnersMode(t *testing.T) {
	owner := ownerPack("claude")
	set := Collect([]*packload.Pack{owner,
		rawOverlayPack("claude-fzf", "claude/settings",
			`{"mode":"rmw","managed":{"fileSuggestion":"cmd"}}`)}, true, nil)
	if len(set.Problems) == 0 {
		t.Fatal("an overlay naming `mode` must be refused — silently flipping the owner's " +
			"mode is the general hazard (R1) this kind exists to remove")
	}
	// And the owner's own surface is untouched by the attempt.
	surfaces, _ := owner.SurfacesFor(true)
	if len(surfaces) != 1 || surfaces[0].ResolvedMode() != "stateful" {
		t.Errorf("owner's mode changed: %+v", surfaces)
	}
}

// Applied() is the R3 report source: which surfaces carry overlays, and from which packs,
// in fold order.
func TestAppliedReportsContributorsInFoldOrder(t *testing.T) {
	set := Collect([]*packload.Pack{
		ownerPack("claude"),
		overlayPack("team", "claude/settings", map[string]any{"a": 1}),
		overlayPack("personal", "claude/settings", map[string]any{"b": 2}),
	}, true, nil)
	applied := set.Applied()
	if len(applied) != 1 {
		t.Fatalf("want 1 overlaid surface, got %+v", applied)
	}
	if applied[0].Target != "claude/settings" || applied[0].Agent != "claude" {
		t.Errorf("applied identity wrong: %+v", applied[0])
	}
	if strings.Join(applied[0].Packs, ",") != "team,personal" {
		t.Errorf("contributing packs = %v, want [team personal] in fold order", applied[0].Packs)
	}
}

// A pack set with NO overlays yields an empty set and a nil lookup — the invariant that
// makes wiring this a no-op for every pack yolo ships.
func TestNoOverlaysIsEmpty(t *testing.T) {
	set := Collect([]*packload.Pack{ownerPack("claude")}, true, nil)
	if len(set.Problems) != 0 || len(set.Orphans) != 0 || len(set.Applied()) != 0 {
		t.Errorf("a pack set with no overlays must resolve to nothing: %+v", set)
	}
	if set.For("claude", "settings") != nil {
		t.Error("For() must return nil for a surface nobody overlays")
	}
}

// A nil set is safe to query, so a caller with no other packs in view (RenderHostPack's
// documented nil) needs no guard.
func TestNilSetIsSafe(t *testing.T) {
	var set *OverlaySet
	if set.For("claude", "settings") != nil {
		t.Error("nil OverlaySet.For should be nil")
	}
	if set.Applied() != nil {
		t.Error("nil OverlaySet.Applied should be nil")
	}
}

// A pack MAY overlay a surface it owns itself. Odd but legal, and it must resolve rather
// than be reported ownerless — the owner check is about the surface having an owner, not
// about the contributor being a different pack.
func TestOverlayOntoOwnSurfaceResolves(t *testing.T) {
	surface, _ := json.Marshal([]map[string]any{{
		"agent": "solo", "name": "settings", "codec": "json", "path": "~/.solo/settings.json",
	}})
	body, _ := json.Marshal(map[string]any{"managed": map[string]any{"k": 1}})
	p := &packload.Pack{Name: "solo", Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindConfig, Raw: surface},
		{Kind: packdecl.KindConfigOverlay, Surface: "solo/settings", Raw: body},
	}}}
	set := Collect([]*packload.Pack{p}, true, nil)
	if len(set.For("solo", "settings")) != 1 {
		t.Errorf("a self-overlay should resolve; orphans=%+v problems=%v", set.Orphans, set.Problems)
	}
}

// --- the `profile` modifier (profiles-as-pack-variants.md §7) ---------------------------------
//
// One optional field gating a cross-pack contribution on a profile being ACTIVE for the
// surface's OWNING agent — the target identity's agent segment, which is a CLI name, the
// namespace the profile table keys on. These tests pin the gate at the collection: what
// reaches the render decides everything downstream (provenance, footprint notices, the
// fold), so the property to hold is "the overlay resolves onto its target or is absent
// from the set entirely".

// gatedOverlayPack is overlayPack carrying the profile gate.
func gatedOverlayPack(name, target, profile string, managed map[string]any) *packload.Pack {
	body, _ := json.Marshal(map[string]any{"managed": managed})
	return &packload.Pack{Name: name, Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindConfigOverlay, Surface: target, Profile: profile, Raw: body},
		},
	}}
}

// THE GATE'S POSITIVE HALF: with the named profile active at the target's agent, the
// overlay resolves exactly as an ungated one does — same fold slot, same provenance
// attribution — because the gate only decides PRESENCE.
func TestGatedOverlayResolvesWhenProfileIsActiveForTheOwner(t *testing.T) {
	set := Collect([]*packload.Pack{
		ownerPack("claude"),
		gatedOverlayPack("zai", "claude/settings", "zai", map[string]any{"env": map[string]any{"A": "b"}}),
	}, true, map[string]string{"claude": "zai"})

	if len(set.Problems) != 0 || len(set.Orphans) != 0 {
		t.Fatalf("an active, well-formed overlay must be clean: problems=%v orphans=%+v",
			set.Problems, set.Orphans)
	}
	got := set.For("claude", "settings")
	if len(got) != 1 {
		t.Fatalf("want the gated overlay on claude/settings, got %d", len(got))
	}
	if got[0].Pack != "zai" {
		t.Errorf("attribution = %q, want zai — the gate must not disturb provenance", got[0].Pack)
	}
	if layer, _ := got[0].Data.(map[string]any); layer["env"] == nil {
		t.Errorf("overlay layer = %#v, want the contributed keys", got[0].Data)
	}
}

// THE GATE'S NEGATIVE HALF, three ways to be inactive: another name active at that agent,
// NO name active at that agent, and the name active at a DIFFERENT agent (the table keys
// on the surface's owner, not on the contributor). All three are a clean skip — no keys,
// no error, no orphan, no applied notice — because selection is the optionality (§7.1)
// and profile values are free-form (parent OQ-3), so an unmatched name is inert.
func TestGatedOverlaySkipsCleanlyWhenProfileIsNotActive(t *testing.T) {
	cases := []struct {
		name     string
		profiles map[string]string
	}{
		{"another name active", map[string]string{"claude": "bedrock"}},
		{"nothing active", nil},
		{"active at another agent", map[string]string{"pi": "zai"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			owner := ownerPack("claude")
			set := Collect([]*packload.Pack{
				owner,
				gatedOverlayPack("zai", "claude/settings", "zai", map[string]any{"env": map[string]any{"A": "b"}}),
			}, true, tc.profiles)

			if got := set.For("claude", "settings"); got != nil {
				t.Errorf("an inactive profile must contribute NOTHING (a skip, not an error): %+v", got)
			}
			if len(set.Problems) != 0 {
				t.Errorf("an inactive profile is not an ERROR: %v", set.Problems)
			}
			if len(set.Orphans) != 0 {
				t.Errorf("an inactive profile must not be reported as an orphan — the reason it "+
					"contributed nothing is the selection, not a missing owner: %+v", set.Orphans)
			}
			if len(set.Applied()) != 0 {
				t.Errorf("an inactive profile must not be announced as applied: %+v", set.Applied())
			}
			// And the owner's own surface is untouched by the skip: it still declares
			// exactly what it declared.
			surfaces, _ := owner.SurfacesFor(true)
			if len(surfaces) != 1 {
				t.Errorf("the owner's surface set changed under an inactive overlay: %+v", surfaces)
			}
		})
	}
}

// BACK-COMPAT: an overlay with NO profile field is unconditional, whatever the table
// holds. The field must not become an implicit gate on "some profile is active" — every
// existing overlay depends on the absence meaning pre-field behavior.
func TestUngatedOverlayIgnoresTheProfileTable(t *testing.T) {
	set := Collect([]*packload.Pack{
		ownerPack("claude"),
		overlayPack("claude-fzf", "claude/settings", map[string]any{"fileSuggestion": "cmd"}),
	}, true, map[string]string{"claude": "bedrock"})
	if got := set.For("claude", "settings"); len(got) != 1 {
		t.Errorf("an ungated overlay must render regardless of the active profile, got %d", len(got))
	}
}

// The gate does not swallow R2: with the profile ACTIVE and the owner unselected, the
// overlay is still reported as an orphan — the reason it contributed nothing is now a
// missing owner, and that report's remedy ("select that pack") is actionable.
func TestGatedOverlayStillReportsOrphanWhenProfileIsActive(t *testing.T) {
	set := Collect([]*packload.Pack{
		gatedOverlayPack("zai", "claude/settings", "zai", map[string]any{"k": 1}),
	}, true, map[string]string{"claude": "zai"})
	if len(set.Orphans) != 1 {
		t.Fatalf("want the active-but-ownerless overlay reported as an orphan, got %+v", set.Orphans)
	}
	if reason := set.Orphans[0].Reason(); !strings.Contains(reason, "`claude` pack is not selected") {
		t.Errorf("orphan reason should name the pack to select, got %q", reason)
	}
}
