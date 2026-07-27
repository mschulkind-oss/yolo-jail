package manifest

import (
	"strings"
	"testing"
)

// D3: a surface must be definable as DATA, going through the SAME validation as a Go
// literal — that is what lets an agent's surfaces move into an official pack, and what
// lets a third-party pack declare one, without either being less checked.
func TestDecodeSurfacesRoundTripsThroughValidation(t *testing.T) {
	surfaces, problems := DecodeSurfaces([]byte(`[
	  {"agent":"acme","name":"settings","path":"~/.acme/settings.json","codec":"json",
	   "defaults":{"theme":"dark"},"managed":{"telemetry":false}},
	  {"agent":"acme","name":"mcp","path":"~/.acme/mcp.json","codec":"json",
	   "mode":"computed","defaults":{"mcpServers":{}}}
	]`))
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	if len(surfaces) != 2 {
		t.Fatalf("surfaces = %v", surfaces)
	}
	m, err := New(surfaces...)
	if err != nil {
		t.Fatalf("data-defined surfaces failed the shared validation: %v", err)
	}
	s, ok := m.Lookup("acme", "mcp")
	if !ok {
		t.Fatal("acme/mcp missing")
	}
	if s.ResolvedMode() != ModeComputed {
		t.Errorf("mode = %q, want computed", s.ResolvedMode())
	}
	// The default mode is stateful, matching the Go-literal surfaces.
	if base, _ := m.Lookup("acme", "settings"); base.ResolvedMode() != ModeStateful {
		t.Errorf("absent mode should default to stateful, got %q", base.ResolvedMode())
	}
}

// An unknown FIELD is an error. A pack author who writes "manged" instead of "managed"
// would otherwise get a surface that silently asserts nothing, with no signal at all.
func TestDecodeSurfacesRejectsUnknownField(t *testing.T) {
	_, problems := DecodeSurfaces([]byte(`[{"agent":"a","name":"n","path":"~/x","codec":"json","manged":{}}]`))
	if len(problems) == 0 {
		t.Fatal("expected a problem for a misspelled field")
	}
	if !strings.Contains(problems[0], "manged") {
		t.Errorf("problem should name the bad field: %v", problems)
	}
}

// An unknown MODE is an error rather than a silent fallback to stateful: silently
// capturing edits on a surface whose author asked for overwrite semantics is a
// data-loss bug, not a formatting nit.
func TestDecodeSurfacesRejectsUnknownMode(t *testing.T) {
	_, problems := DecodeSurfaces([]byte(`[{"agent":"a","name":"n","path":"~/x","codec":"json","mode":"magic"}]`))
	if len(problems) == 0 {
		t.Fatal("expected a problem for an unknown mode")
	}
	if !strings.Contains(problems[0], "magic") || !strings.Contains(problems[0], "stateful") {
		t.Errorf("problem should name the bad mode and the valid set: %v", problems)
	}
}

// Every missing required field is reported, not just the first: an author fixing a
// surface wants the whole list rather than one edit-check cycle per field.
func TestDecodeSurfacesReportsAllMissingFields(t *testing.T) {
	_, problems := DecodeSurfaces([]byte(`[{}]`))
	if len(problems) < 4 {
		t.Errorf("expected a problem per missing field, got %d: %v", len(problems), problems)
	}
}

// An absent layer must stay ABSENT, not become an empty map. On a keyless surface an
// empty-map layer hard-errors, and on an object surface it claims "yolo asserts
// nothing here" rather than "yolo has no opinion".
func TestDecodeSurfacesKeepsAbsentLayersNil(t *testing.T) {
	surfaces, problems := DecodeSurfaces([]byte(`[{"agent":"a","name":"n","path":"~/x.raw","codec":"raw"}]`))
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	if surfaces[0].Defaults != nil || surfaces[0].Managed != nil {
		t.Errorf("absent layers must stay nil: defaults=%#v managed=%#v",
			surfaces[0].Defaults, surfaces[0].Managed)
	}
}

// Merge lets a later surface REPLACE an earlier one with the same key — the mechanism
// by which a pack overrides an official definition, matching the "later wins" rule
// packs already use for skills. The override is validated exactly as strictly.
func TestMergeLetsLaterSurfaceOverride(t *testing.T) {
	base := []Surface{
		{Agent: "a", Name: "n", Path: "~/base.json", Codec: "json",
			Defaults: map[string]any{"from": "base"}},
		{Agent: "b", Name: "n", Path: "~/b.json", Codec: "json"},
	}
	override := Surface{Agent: "a", Name: "n", Path: "~/override.json", Codec: "json",
		Defaults: map[string]any{"from": "pack"}}

	m, err := Merge(base, override)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := m.Lookup("a", "n")
	if got.Path != "~/override.json" {
		t.Errorf("later surface did not win: %q", got.Path)
	}
	// The untouched surface survives, and there is no duplicate.
	if len(m.Surfaces()) != 2 {
		t.Errorf("expected 2 surfaces after override, got %d", len(m.Surfaces()))
	}
}

// A malformed override must FAIL the merge rather than being accepted because it came
// from data: data-defined surfaces are not a validation bypass.
func TestMergeValidatesOverrides(t *testing.T) {
	base := []Surface{{Agent: "a", Name: "n", Path: "~/x.json", Codec: "json"}}
	if _, err := Merge(base, Surface{Agent: "a", Name: "n", Path: "~/x.json", Codec: "bogus"}); err == nil {
		t.Error("an override with a bad codec must fail validation")
	}
}
