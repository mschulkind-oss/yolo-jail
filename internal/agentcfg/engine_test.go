package agentcfg

import (
	"reflect"
	"testing"
)

// --- deepMerge ---------------------------------------------------------------

func TestDeepMerge(t *testing.T) {
	tests := []struct {
		name string
		base map[string]any
		over map[string]any
		want map[string]any
	}{
		{
			name: "disjoint keys union",
			base: map[string]any{"a": 1},
			over: map[string]any{"b": 2},
			want: map[string]any{"a": 1, "b": 2},
		},
		{
			name: "scalar over scalar replaces",
			base: map[string]any{"a": 1},
			over: map[string]any{"a": 2},
			want: map[string]any{"a": 2},
		},
		{
			name: "objects merge one level",
			base: map[string]any{"o": map[string]any{"a": 1, "b": 2}},
			over: map[string]any{"o": map[string]any{"b": 3, "c": 4}},
			want: map[string]any{"o": map[string]any{"a": 1, "b": 3, "c": 4}},
		},
		{
			name: "objects merge at two-plus levels",
			base: map[string]any{
				"l1": map[string]any{
					"l2": map[string]any{"keep": 1, "change": "old"},
				},
			},
			over: map[string]any{
				"l1": map[string]any{
					"l2":  map[string]any{"change": "new", "add": true},
					"sib": 9,
				},
			},
			want: map[string]any{
				"l1": map[string]any{
					"l2":  map[string]any{"keep": 1, "change": "new", "add": true},
					"sib": 9,
				},
			},
		},
		{
			name: "null deletes existing key",
			base: map[string]any{"a": 1, "b": 2},
			over: map[string]any{"b": nil},
			want: map[string]any{"a": 1},
		},
		{
			name: "null deletes nested key",
			base: map[string]any{"o": map[string]any{"a": 1, "b": 2}},
			over: map[string]any{"o": map[string]any{"a": nil}},
			want: map[string]any{"o": map[string]any{"b": 2}},
		},
		{
			name: "null on absent key is a no-op",
			base: map[string]any{"a": 1},
			over: map[string]any{"gone": nil},
			want: map[string]any{"a": 1},
		},
		{
			name: "array replaces, never element-merges",
			base: map[string]any{"list": []any{1, 2, 3}},
			over: map[string]any{"list": []any{9}},
			want: map[string]any{"list": []any{9}},
		},
		{
			name: "object replaces array wholesale",
			base: map[string]any{"x": []any{1, 2}},
			over: map[string]any{"x": map[string]any{"k": "v"}},
			want: map[string]any{"x": map[string]any{"k": "v"}},
		},
		{
			name: "scalar replaces object wholesale (type change edge, RFC 7386)",
			base: map[string]any{"x": map[string]any{"deep": 1}},
			over: map[string]any{"x": "scalar"},
			want: map[string]any{"x": "scalar"},
		},
		{
			name: "object over scalar merges into empty (type change edge, RFC 7386)",
			base: map[string]any{"x": "scalar"},
			over: map[string]any{"x": map[string]any{"k": "v"}},
			want: map[string]any{"x": map[string]any{"k": "v"}},
		},
		{
			name: "empty over is identity",
			base: map[string]any{"a": 1},
			over: map[string]any{},
			want: map[string]any{"a": 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deepMerge(tt.base, tt.over)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("deepMerge()\n got = %#v\nwant = %#v", got, tt.want)
			}
		})
	}
}

func TestDeepMergeDoesNotMutateInputs(t *testing.T) {
	base := map[string]any{"o": map[string]any{"a": 1}}
	over := map[string]any{"o": map[string]any{"b": 2}}
	_ = deepMerge(base, over)

	wantBase := map[string]any{"o": map[string]any{"a": 1}}
	wantOver := map[string]any{"o": map[string]any{"b": 2}}
	if !reflect.DeepEqual(base, wantBase) {
		t.Errorf("base mutated: %#v", base)
	}
	if !reflect.DeepEqual(over, wantOver) {
		t.Errorf("over mutated: %#v", over)
	}
}

// --- render ------------------------------------------------------------------

func TestRenderFoldPrecedence(t *testing.T) {
	defaults := map[string]any{
		"theme":     "light",
		"model":     "default-model",
		"telemetry": true,
		"nested":    map[string]any{"a": "d", "keep": 1},
	}
	host := map[string]any{
		"theme":  "dark",                   // host overrides default
		"nested": map[string]any{"a": "h"}, // deep merge, keep stays
	}
	workspace := map[string]any{
		"model": "ws-model", // workspace overrides host/default
	}
	overlay := map[string]any{
		"model": "overlay-model", // overlay outranks workspace
		"extra": "from-overlay",
	}
	managed := map[string]any{
		"model":     "MANAGED", // managed wins everything
		"telemetry": false,     // managed asserts its key over default
	}

	got := render(defaults, host, workspace, overlay, managed)
	want := map[string]any{
		"theme":     "dark",
		"model":     "MANAGED",
		"telemetry": false,
		"extra":     "from-overlay",
		"nested":    map[string]any{"a": "h", "keep": 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("render()\n got = %#v\nwant = %#v", got, want)
	}
}

func TestRenderNoLayers(t *testing.T) {
	got := render()
	if !reflect.DeepEqual(got, map[string]any{}) {
		t.Fatalf("render() with no layers = %#v, want empty map", got)
	}
}

func TestRenderNullTombstoneAcrossLayers(t *testing.T) {
	// A higher layer can delete a lower layer's key via a null (overlay
	// carrying a tombstone from mergeDiff, §5).
	defaults := map[string]any{"a": 1, "b": 2}
	overlay := map[string]any{"b": nil}
	got := render(defaults, overlay)
	want := map[string]any{"a": 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("render() with tombstone\n got = %#v\nwant = %#v", got, want)
	}
}

// --- mergeDiff ---------------------------------------------------------------

func TestMergeDiff(t *testing.T) {
	tests := []struct {
		name string
		old  map[string]any
		new  map[string]any
		want map[string]any
	}{
		{
			name: "no change yields empty patch",
			old:  map[string]any{"a": 1, "o": map[string]any{"b": 2}},
			new:  map[string]any{"a": 1, "o": map[string]any{"b": 2}},
			want: map[string]any{},
		},
		{
			name: "added key",
			old:  map[string]any{"a": 1},
			new:  map[string]any{"a": 1, "b": 2},
			want: map[string]any{"b": 2},
		},
		{
			name: "changed leaf",
			old:  map[string]any{"a": 1},
			new:  map[string]any{"a": 2},
			want: map[string]any{"a": 2},
		},
		{
			name: "deleted key becomes null tombstone",
			old:  map[string]any{"a": 1, "b": 2},
			new:  map[string]any{"a": 1},
			want: map[string]any{"b": nil},
		},
		{
			name: "nested change only emits the changed leaf",
			old:  map[string]any{"o": map[string]any{"keep": 1, "x": "old"}},
			new:  map[string]any{"o": map[string]any{"keep": 1, "x": "new"}},
			want: map[string]any{"o": map[string]any{"x": "new"}},
		},
		{
			name: "nested add and delete together",
			old:  map[string]any{"o": map[string]any{"gone": 1, "keep": 2}},
			new:  map[string]any{"o": map[string]any{"keep": 2, "fresh": 3}},
			want: map[string]any{"o": map[string]any{"gone": nil, "fresh": 3}},
		},
		{
			name: "array change emits whole new array",
			old:  map[string]any{"list": []any{1, 2, 3}},
			new:  map[string]any{"list": []any{1, 2}},
			want: map[string]any{"list": []any{1, 2}},
		},
		{
			name: "equal arrays emit nothing",
			old:  map[string]any{"list": []any{1, 2, 3}},
			new:  map[string]any{"list": []any{1, 2, 3}},
			want: map[string]any{},
		},
		{
			name: "type change scalar to object emits whole new value",
			old:  map[string]any{"x": "scalar"},
			new:  map[string]any{"x": map[string]any{"k": "v"}},
			want: map[string]any{"x": map[string]any{"k": "v"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeDiff(tt.old, tt.new)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("mergeDiff()\n got = %#v\nwant = %#v", got, tt.want)
			}
		})
	}
}

// TestMergeDiffRoundTrip is the §5 contract: applying the captured diff back
// over the old render reconstructs the new render exactly, including deletions
// (via null tombstones) and nested changes.
func TestMergeDiffRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		old  map[string]any
		new  map[string]any
	}{
		{
			name: "added, changed, deleted, and nested together",
			old: map[string]any{
				"keep":   "same",
				"change": "old",
				"delete": "bye",
				"nest":   map[string]any{"a": 1, "drop": 2, "sub": map[string]any{"x": 1}},
				"arr":    []any{1, 2, 3},
			},
			new: map[string]any{
				"keep":   "same",
				"change": "new",
				"add":    "hello",
				"nest":   map[string]any{"a": 1, "sub": map[string]any{"x": 2, "y": 3}},
				"arr":    []any{9},
			},
		},
		{
			name: "pure deletion",
			old:  map[string]any{"a": 1, "b": 2, "c": 3},
			new:  map[string]any{"a": 1},
		},
		{
			name: "no change",
			old:  map[string]any{"a": 1, "o": map[string]any{"b": 2}},
			new:  map[string]any{"a": 1, "o": map[string]any{"b": 2}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			patch := mergeDiff(tc.old, tc.new)
			got := deepMerge(tc.old, patch)
			if !reflect.DeepEqual(got, tc.new) {
				t.Fatalf("round-trip deepMerge(old, mergeDiff(old,new)) != new\n patch = %#v\n got   = %#v\n want  = %#v",
					patch, got, tc.new)
			}
		})
	}
}

// TestCapturedOverlaySurvivesRegeneration is the §5 single-boot contract that
// the three pure functions here fully cover: an in-jail edit (including a
// deletion) is captured by mergeDiff into the overlay layer, and feeding that
// overlay through render reproduces the edit against a fresh defaults/host
// render — the deletion is NOT resurrected because the overlay carries a null
// tombstone that render applies.
//
// NOTE (out of scope, for the capture-diff sidecar piece): the doc's
// multi-boot accumulation `overlay = deepMerge(overlay, delta)` cannot use this
// RFC-7386 deepMerge as-is — a tombstone merged onto an overlay that lacks the
// key is applied (a no-op delete) instead of stored, dropping the deletion.
// Accumulation needs a tombstone-PRESERVING merge variant, which is a separate
// primitive from these three. See the report.
func TestCapturedOverlaySurvivesRegeneration(t *testing.T) {
	// What yolo wrote last boot.
	lastRender := map[string]any{"a": 1, "b": 2, "theme": "dark"}
	// Agent edits in-jail: deletes "b", changes theme.
	currentFile := map[string]any{"a": 1, "theme": "light"}

	// Capture the edit into the overlay (first capture from empty == the delta).
	overlay := mergeDiff(lastRender, currentFile)
	wantOverlay := map[string]any{"b": nil, "theme": "light"}
	if !reflect.DeepEqual(overlay, wantOverlay) {
		t.Fatalf("captured overlay\n got = %#v\nwant = %#v", overlay, wantOverlay)
	}

	// Next boot: render fresh defaults/host with the overlay layer on top.
	defaults := map[string]any{"a": 1, "b": 2, "theme": "dark"}
	got := render(defaults, overlay)
	want := map[string]any{"a": 1, "theme": "light"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("regenerated render\n got = %#v\nwant = %#v (b stays deleted, theme=light)", got, want)
	}
}

// TestMergeAccumulatePreservesTombstoneOnAbsentKey is the §3.4 correctness fix:
// mergeAccumulate must STORE a null tombstone even when the key is absent in the
// accumulator, whereas deepMerge (rightly, for the render fold) drops it.
func TestMergeAccumulatePreservesTombstoneOnAbsentKey(t *testing.T) {
	overlay := map[string]any{"theme": "light"} // no "b" yet
	delta := map[string]any{"b": nil}           // a captured deletion of a key not in overlay

	// deepMerge would DROP the tombstone (no-op delete on an absent key)...
	if _, present := deepMerge(overlay, delta)["b"]; present {
		t.Fatal("precondition: deepMerge unexpectedly kept the tombstone")
	}
	// ...mergeAccumulate must KEEP it.
	acc := mergeAccumulate(overlay, delta)
	v, present := acc["b"]
	if !present || v != nil {
		t.Errorf("mergeAccumulate dropped the tombstone: got present=%v value=%#v, want present=true nil", present, v)
	}
	if acc["theme"] != "light" {
		t.Errorf("mergeAccumulate lost unrelated key: %#v", acc)
	}
}

// TestMergeAccumulateMultiBootDeletion is the steady-state bug the fix prevents:
// a deletion captured on boot N must survive to boot N+2 through the accumulator.
func TestMergeAccumulateMultiBootDeletion(t *testing.T) {
	// Boot 1: agent deletes "gate"; overlay starts empty.
	overlay := map[string]any{}
	overlay = mergeAccumulate(overlay, map[string]any{"gate": nil})
	// Boot 2: agent changes theme; the "gate" tombstone must persist.
	overlay = mergeAccumulate(overlay, map[string]any{"theme": "solarized"})

	if v, ok := overlay["gate"]; !ok || v != nil {
		t.Errorf("tombstone lost across boots: overlay=%#v", overlay)
	}
	// Applying the accumulated overlay keeps gate deleted and theme set.
	defaults := map[string]any{"gate": true, "theme": "dark", "keep": 1}
	got := render(defaults, overlay)
	want := map[string]any{"theme": "solarized", "keep": 1}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("multi-boot render\n got = %#v\nwant = %#v (gate stays deleted)", got, want)
	}
}

// TestMergeAccumulateDoesNotMutateInputs guards the non-mutation contract.
func TestMergeAccumulateDoesNotMutateInputs(t *testing.T) {
	base := map[string]any{"a": map[string]any{"x": 1}}
	over := map[string]any{"a": map[string]any{"y": 2}, "b": nil}
	_ = mergeAccumulate(base, over)
	if len(base["a"].(map[string]any)) != 1 {
		t.Errorf("base mutated: %#v", base)
	}
	if _, ok := over["a"].(map[string]any)["x"]; ok {
		t.Errorf("over mutated: %#v", over)
	}
}
