package jsonx

import (
	"reflect"
	"testing"
)

// THE WHOLE POINT IS DEPTH. A one-level clone is the shape that looks correct and is not:
// the copy's `endpoints` is still the original's, so a write under it lands in the caller's
// tree (docs/design/wire-bridge-port-collision.md §2.2, step 2).
func TestDeepCopyIsDeepEnoughToSurviveANestedWrite(t *testing.T) {
	v, err := Decode([]byte(`{"p": {"endpoints": {"openai": {"base_url": "http://up.example/v1"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	original := v.(*OrderedMap)
	before, err := DumpsSnapshot(original)
	if err != nil {
		t.Fatal(err)
	}

	clone := DeepCopyMap(original)
	entry, _ := clone.Get("p")
	eps, _ := entry.(*OrderedMap).Get("endpoints")
	ep := NewOrderedMap()
	ep.Set("base_url", "http://127.0.0.1:8214")
	eps.(*OrderedMap).Set("anthropic", ep)

	after, err := DumpsSnapshot(original)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Errorf("a write three levels into the copy reached the original:\nbefore: %s\nafter:  %s",
			before, after)
	}
}

// KEY ORDER AND THE VALUE MODEL SURVIVE, which is what separates this from Plain: an
// integer literal must stay an integer and the order must stay the insertion order, or a
// copied config no longer re-encodes byte-identically and every snapshot comparison drifts.
func TestDeepCopyPreservesOrderAndTheValueModel(t *testing.T) {
	src := []byte(`{"z": 1, "a": {"n": 2.5, "big": 9007199254740993, "s": "x", "b": true, "nil": null}, "m": [1, {"k": "v"}, []]}`)
	v, err := Decode(src)
	if err != nil {
		t.Fatal(err)
	}
	original := v.(*OrderedMap)
	clone := DeepCopyMap(original)

	if got, want := clone.Keys(), original.Keys(); !reflect.DeepEqual(got, want) {
		t.Errorf("key order = %v, want %v", got, want)
	}
	oneWay, err := DumpsIndent(original, 2)
	if err != nil {
		t.Fatal(err)
	}
	other, err := DumpsIndent(clone, 2)
	if err != nil {
		t.Fatal(err)
	}
	if oneWay != other {
		t.Errorf("the copy does not re-encode identically:\n%s\n---\n%s", oneWay, other)
	}
	inner, _ := clone.Get("a")
	if big, _ := inner.(*OrderedMap).Get("big"); !IsInt(big) {
		t.Errorf("an integer literal survived the copy as %T; Plain lowers the model and "+
			"DeepCopy must not", big)
	}
}

// A NIL MAP COPIES TO NIL — "absent" is a distinction the decoder draws and the copy may
// not collapse it into an empty object.
func TestDeepCopyMapKeepsNilNil(t *testing.T) {
	if got := DeepCopyMap(nil); got != nil {
		t.Errorf("DeepCopyMap(nil) = %#v, want nil", got)
	}
}

// SLICES ARE MUTABLE TOO. A shared backing array is the same defect one type over: a
// provider's `capabilities` list written through would edit the caller's config.
func TestDeepCopyClonesSliceBackingArrays(t *testing.T) {
	original := []any{"web_search", NewOrderedMap()}
	clone := DeepCopy(original).([]any)
	clone[0] = "tampered"
	if original[0] != "web_search" {
		t.Error("writing an element of the copy reached the original slice")
	}
	if clone[1] == original[1] {
		t.Error("a map inside a copied slice is still the original's")
	}

	strs := []string{"a", "b"}
	strClone := DeepCopy(strs).([]string)
	strClone[0] = "tampered"
	if strs[0] != "a" {
		t.Error("[]string was returned by reference")
	}
}

// map[string]any is not what Decode produces, but the encoder accepts it and a caller can
// inject one, so the copier has to reach into it rather than treat it as a scalar.
func TestDeepCopyClonesPlainMaps(t *testing.T) {
	original := map[string]any{"a": map[string]any{"b": "c"}}
	clone := DeepCopy(original).(map[string]any)
	clone["a"].(map[string]any)["b"] = "tampered"
	if original["a"].(map[string]any)["b"] != "c" {
		t.Error("a nested plain map was shared with the original")
	}
}
