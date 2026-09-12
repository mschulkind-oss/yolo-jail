package agentcfg

// literalnull_test.go pins the channel a literal `null` travels on (literalnull.go), and
// specifically the rules the end-to-end criterion test in internal/entrypoint CANNOT reach:
// what happens when a LAYER also speaks for the marked key.
//
// ⚠ THE CALL SITE IS PINNED HERE TOO, in TestComposeStatefulReadsLiteralNullsOffTheFile.
// Every other test in this file drives Compose directly with a hand-built
// Inputs.LiteralNulls, so all of them stay green if ComposeStateful stops filling the field —
// which is the whole feature. That one test is what fails.

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
)

func nullSurface() manifest.Surface {
	return manifest.Surface{Agent: "acme", Name: "settings", Codec: "json"}
}

// composeWith renders one object surface and returns the composed map.
func composeWith(t *testing.T, in Inputs) (map[string]any, *Result) {
	t.Helper()
	res, err := Compose(in)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	return res.ConfigMap(), res
}

// A MARKED KEY NO LAYER SPEAKS FOR IS REINSTATED — the plain case, at both depths, including
// a subtree whose entire content is a null (nothing below it can come from the overlay, so if
// the skeleton did not rebuild the parent the key would be gone).
func TestLiteralNullsReinstateWhereNoLayerSpeaks(t *testing.T) {
	cfg, res := composeWith(t, Inputs{
		Surface: nullSurface(),
		Overlay: map[string]any{"nested": map[string]any{"kept": 1.0}},
		LiteralNulls: map[string]any{
			"top":       nil,
			"nested":    map[string]any{"inner": nil},
			"onlyNulls": map[string]any{"gone": nil},
		},
	})
	want := map[string]any{
		"top":       nil,
		"nested":    map[string]any{"kept": 1.0, "inner": nil},
		"onlyNulls": map[string]any{"gone": nil},
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("got %#v, want %#v", cfg, want)
	}
	// A key this pass CREATED at top level is attributed to the layer name for
	// "a key of yours that survived regeneration".
	for _, k := range []string{"top", "onlyNulls"} {
		if got := res.Provenance[k]; got != layerOverlay {
			t.Errorf("provenance[%q] = %q, want %q", k, got, layerOverlay)
		}
	}
}

// A LAYER ALWAYS WINS, and the case that matters is the one where what it says is "DELETED".
// A computed tombstone removes a key on purpose (regenerate-don't-reconcile); putting it back
// as null would undo a decision yolo made this boot, and the file would then hold a key the
// live config no longer has.
func TestLiteralNullsNeverOverrideALayer(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   Inputs
		want map[string]any
	}{
		{
			name: "a computed tombstone stays deleted",
			in: Inputs{
				Surface:      nullSurface(),
				Overlay:      map[string]any{"server": "stale"},
				Computed:     map[string]any{"server": nil},
				LiteralNulls: map[string]any{"server": nil},
			},
			want: map[string]any{},
		},
		{
			name: "an ordinary layer value wins",
			in: Inputs{
				Surface:      manifest.Surface{Agent: "acme", Name: "settings", Codec: "json", Defaults: map[string]any{"theme": "system"}},
				LiteralNulls: map[string]any{"theme": nil},
			},
			want: map[string]any{"theme": "system"},
		},
		{
			name: "the managed floor wins",
			in: Inputs{
				Surface:      manifest.Surface{Agent: "acme", Name: "settings", Codec: "json", Managed: map[string]any{"mode": "locked"}},
				LiteralNulls: map[string]any{"mode": nil},
			},
			want: map[string]any{"mode": "locked"},
		},
		{
			name: "a layer's scalar blocks the whole marked subtree",
			in: Inputs{
				Surface:      nullSurface(),
				Overlay:      map[string]any{"opts": "a string, not a table"},
				LiteralNulls: map[string]any{"opts": map[string]any{"inner": nil}},
			},
			want: map[string]any{"opts": "a string, not a table"},
		},
		{
			name: "but a SIBLING under an object-valued layer is still reinstated",
			in: Inputs{
				Surface: manifest.Surface{Agent: "acme", Name: "settings", Codec: "json",
					Managed: map[string]any{"permissions": map[string]any{"defaultMode": "default"}}},
				LiteralNulls: map[string]any{"permissions": map[string]any{"ask": nil}},
			},
			want: map[string]any{"permissions": map[string]any{"defaultMode": "default", "ask": nil}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, _ := composeWith(t, tc.in)
			if !reflect.DeepEqual(cfg, tc.want) {
				t.Errorf("got %#v, want %#v", cfg, tc.want)
			}
		})
	}
}

// AND A LAYER THAT SUPPLIES A KEY AND A LATER ONE THAT TOMBSTONES IT COUNTS AS DELETED.
// layersSpeakFor asks whether EVERY mentioning layer holds the key as an object, not whether
// any does — the fail-safe direction. With "any", the object-valued layer below would make
// this look descendable and the file's nulls would rebuild a key the fold removed.
func TestLiteralNullsDoNotRebuildATombstonedSubtree(t *testing.T) {
	cfg, _ := composeWith(t, Inputs{
		Surface:      nullSurface(),
		Workspace:    map[string]any{"table": map[string]any{"a": 1.0}},
		Computed:     map[string]any{"table": nil},
		LiteralNulls: map[string]any{"table": map[string]any{"b": nil}},
	})
	if len(cfg) != 0 {
		t.Errorf("the tombstoned subtree came back: %#v", cfg)
	}
}

// THE SKELETON IS READ OFF VALUES, and prunes every branch that marks nothing — so a file
// with no nulls costs the render nothing and the pass never runs.
func TestLiteralNullSkeletonPrunes(t *testing.T) {
	got := literalNullSkeleton(map[string]any{
		"scalar": 1.0,
		"obj":    map[string]any{"deep": map[string]any{"here": nil}, "other": "x"},
		"none":   map[string]any{"a": "b"},
		"null":   nil,
	})
	want := map[string]any{
		"null": nil,
		"obj":  map[string]any{"deep": map[string]any{"here": nil}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
	if literalNullSkeleton(map[string]any{"a": 1.0, "b": map[string]any{"c": "d"}}) != nil {
		t.Error("a file with no nulls must skeleton to nil, so the pass is skipped entirely")
	}
}

// ── THE CALL SITE ────────────────────────────────────────────────────────────────────────
//
// ComposeStateful reads the marks off the CURRENT FILE, on BOTH branches. Delete either read
// and this is the test that goes red; every test above keeps passing, because they all hand
// Compose a skeleton by hand.
func TestComposeStatefulReadsLiteralNullsOffTheFile(t *testing.T) {
	surface := manifest.Surface{Agent: "acme", Name: "settings", Codec: "json",
		Defaults: map[string]any{"theme": "system"}}
	const file = `{"theme":"system","userNull":null}`

	// FIRST MIGRATION: no last_render, so the branch that adopts.
	first, err := ComposeStateful(StatefulInputs{
		Base:         Inputs{Surface: surface},
		CurrentBytes: []byte(file),
	})
	if err != nil {
		t.Fatalf("first migration: %v", err)
	}
	if !first.FirstMigration {
		t.Fatal("fixture did not take the adoption branch")
	}
	if v, ok := first.Result.ConfigMap()["userNull"]; !ok || v != nil {
		t.Errorf("adoption dropped the file's literal null: %s", first.Result.Encoded)
	}
	// The OVERLAY stays a clean merge patch: the null is NOT in it, and must not be —
	// a null there means "delete" (§3.4), which is the collision this channel exists to
	// avoid rather than to relocate.
	if got := string(first.OverlayJSON); got != "{}" {
		t.Errorf("the overlay sidecar holds %s; the literal null must not be stored as a "+
			"tombstone — it would delete the key on the next render", got)
	}

	// STEADY STATE: a trusted last_render, so the branch that captures a diff. This is the
	// half a fix applied only to the adoption residue would miss, and the render is a fixed
	// point only because both branches read the file.
	second, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: surface},
		CurrentBytes:      first.Result.Encoded,
		LastRenderPresent: true,
		LastRenderBytes:   first.LastRenderBytes,
		OverlayJSON:       first.OverlayJSON,
	})
	if err != nil {
		t.Fatalf("steady state: %v", err)
	}
	if second.FirstMigration {
		t.Fatal("fixture did not take the steady-state branch")
	}
	if string(second.Result.Encoded) != string(first.Result.Encoded) {
		t.Errorf("the steady-state render dropped what adoption put back:\nfirst:\n%s\n"+
			"second:\n%s", first.Result.Encoded, second.Result.Encoded)
	}
}

// THE CASE ONLY A REAL BOOT FOUND: a null ADDED in steady state, whose capture lands in the
// overlay as a TOMBSTONE.
//
// mergeDiff cannot tell "the user added `k: null`" from "the user deleted k" — both are
// `k: null` in the patch — so the first edit records a tombstone, and the tombstone then
// deletes the very key it was meant to record. The overlay is a record OF the file and
// therefore cannot be evidence AGAINST it, which is why Compose excludes it from the
// literal-null evidence. Nothing in the host-notch tests could see this: they only ever
// exercise the ADOPTION branch, where the overlay is built from the file in the same breath.
//
// Measured on a nested jail boot 2026-09-12 against claude/settings, before the exclusion:
// `oqco12Null` gone, `nested_probe.deepNull` gone, and both sitting in the sidecar as nulls.
func TestLiteralNullAddedInSteadyStateSurvivesItsOwnTombstone(t *testing.T) {
	surface := manifest.Surface{Agent: "acme", Name: "settings", Codec: "json"}

	// Boot 1: a plain file, so the overlay is seeded and last_render is trusted after.
	first, err := ComposeStateful(StatefulInputs{
		Base: Inputs{Surface: surface}, CurrentBytes: []byte(`{"keep":"me"}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	// The in-jail EDIT: two nulls added to the rendered file, one nested.
	edited := `{"keep":"me","added":null,"sub":{"deep":null}}`

	// Boot 2: steady state. The delta records both as tombstones — which is the shape
	// this test exists for, so assert it rather than hoping.
	second, err := ComposeStateful(StatefulInputs{
		Base: Inputs{Surface: surface}, CurrentBytes: []byte(edited),
		LastRenderPresent: true, LastRenderBytes: first.LastRenderBytes,
		OverlayJSON: first.OverlayJSON,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.FirstMigration {
		t.Fatal("fixture did not take the steady-state branch")
	}
	var overlay map[string]any
	if err := json.Unmarshal(second.OverlayJSON, &overlay); err != nil {
		t.Fatal(err)
	}
	if v, ok := overlay["added"]; !ok || v != nil {
		t.Fatalf("the premise moved: the capture no longer records the added null as a "+
			"tombstone, so this test is measuring something else now. Overlay: %s",
			second.OverlayJSON)
	}

	cfg := second.Result.ConfigMap()
	if v, ok := cfg["added"]; !ok || v != nil {
		t.Errorf("the overlay's own tombstone deleted the key it was recording: %s",
			second.Result.Encoded)
	}
	sub, _ := cfg["sub"].(map[string]any)
	if sub == nil {
		t.Fatalf("the nested key is gone entirely: %s", second.Result.Encoded)
	}
	if v, ok := sub["deep"]; !ok || v != nil {
		t.Errorf("the nested null did not survive its tombstone: %s", second.Result.Encoded)
	}

	// Boot 3: and it is a fixed point, with the stale tombstone still in the sidecar —
	// the exclusion has to keep working against an overlay it did not clean.
	third, err := ComposeStateful(StatefulInputs{
		Base: Inputs{Surface: surface}, CurrentBytes: second.Result.Encoded,
		LastRenderPresent: true, LastRenderBytes: second.LastRenderBytes,
		OverlayJSON: second.OverlayJSON,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(third.Result.Encoded) != string(second.Result.Encoded) {
		t.Errorf("not a fixed point:\nsecond:\n%s\nthird:\n%s",
			second.Result.Encoded, third.Result.Encoded)
	}
}

// AND THE PURE RENDER STAYS LAYERS-ALONE. StatefulOutput.PureBytes is what
// entrypoint.archiveAdoption compares the file against to ask "is this anything other than
// yolo's own output?", so a literal null of the user's must NOT appear in it — or the archive
// gate would read a file carrying the user's null as yolo's own and take no copy.
func TestLiteralNullsStayOutOfThePureRender(t *testing.T) {
	out, err := ComposeStateful(StatefulInputs{
		Base: Inputs{Surface: manifest.Surface{Agent: "acme", Name: "settings", Codec: "json",
			Defaults: map[string]any{"theme": "system"}}},
		CurrentBytes: []byte(`{"theme":"system","userNull":null}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var pure map[string]any
	if err := json.Unmarshal(out.PureBytes, &pure); err != nil {
		t.Fatalf("decode pure bytes %q: %v", out.PureBytes, err)
	}
	if _, found := pure["userNull"]; found {
		t.Errorf("the layers-alone render carries the file's null: %s", out.PureBytes)
	}
}

// ── dropNullLeaves: the OTHER bug in §11's table, and a different mechanism ───────────────
//
// An object the user wrote EMPTY reaches the overlay and folds through unharmed once this
// stops eating it. An object this function's own recursion empties is residue and still goes.
func TestDropNullLeavesKeepsAnAlreadyEmptyObject(t *testing.T) {
	got := dropNullLeaves(map[string]any{
		"userEmpty":   map[string]any{},
		"allTomb":     map[string]any{"a": nil},
		"mixed":       map[string]any{"a": nil, "b": 1.0, "c": map[string]any{}},
		"topTombston": nil,
	})
	want := map[string]any{
		"userEmpty": map[string]any{},
		"mixed":     map[string]any{"b": 1.0, "c": map[string]any{}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}
