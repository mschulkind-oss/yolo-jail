package agentcfg

import (
	"reflect"
	"testing"
)

// enforce_test.go pins the managed floor DIRECTLY, at the function
// docs/design/lua-transform-removal.md §4.2 lifted out of luahook.Ctx.
//
// The compose-level pins stay the ones that prove the floor is WIRED — delete
// the enforceManaged call in compose.go and TestComposeManagedNilValueIsAssigned‑
// NotDeleted, TestComposeRawManagedReplacesWholeFile and the whole
// *EnforcesManaged suite go red. What they cannot state as plainly is WHICH
// semantics the function has, and those are what a later "unify the two merges"
// refactor would take. Each test below names the rewrite it is there to fail.

// TestEnforceManagedNilIsAssignedNotDeleted is the one that matters most: the
// floor is NOT RFC 7386.
//
// engine.go's mergeValue deletes a key whose patch value is null; enforceValue
// assigns it. The two functions are one `if` apart and read as though either
// would do. This test runs the same inputs through BOTH and asserts they
// disagree, so the day someone replaces enforceValue with mergeValue — the
// single most likely "simplification" here — this fails by name instead of
// quietly dropping every host key a managed null sits over.
func TestEnforceManagedNilIsAssignedNotDeleted(t *testing.T) {
	config := map[string]any{"nulled": "host set this", "theme": "dark"}
	managed := map[string]any{"nulled": nil}

	got, ok := enforceManaged(config, managed).(map[string]any)
	if !ok {
		t.Fatalf("enforceManaged returned %T, want an object", got)
	}
	v, present := got["nulled"]
	if !present {
		t.Fatal("the floor DELETED a nil-valued managed key; it must ASSIGN it " +
			"(enforceValue is not RFC 7386 — §4.2 item 1)")
	}
	if v != nil {
		t.Errorf("nulled = %#v, want an explicit nil assigned over the host value", v)
	}
	if got["theme"] != "dark" {
		t.Errorf("untouched sibling clobbered: %#v", got)
	}

	// The fold's merge, same inputs, opposite answer. If this stops being true
	// the two have been unified and the assertion above is no longer load-bearing.
	merged := mergeValue(map[string]any{"nulled": "host set this", "theme": "dark"}, managed).(map[string]any)
	if _, present := merged["nulled"]; present {
		t.Fatal("mergeValue kept a null-patched key — the fold is supposed to " +
			"DELETE it; the two merges have been unified, which is the bug this " +
			"test exists to catch")
	}
}

// TestEnforceManagedKeylessReplacesWholeValue pins the branch a keyless surface
// (raw/lines) takes: there are no keys to enforce one at a time, so a non-nil
// managed layer replaces the rendered value outright, and a nil one leaves it
// exactly alone.
//
// The rewrite this fails: making the non-object branch merge, or making it
// replace unconditionally (which would blank every surface that declares no
// managed layer — i.e. nearly all of them).
func TestEnforceManagedKeylessReplacesWholeValue(t *testing.T) {
	cases := []struct {
		name                  string
		config, managed, want any
	}{
		{"raw string replaced", "host content\n", "yolo owns this file\n", "yolo owns this file\n"},
		{"nil managed leaves raw alone", "host content\n", nil, "host content\n"},
		{"nil managed leaves an object alone", map[string]any{"a": 1}, nil, map[string]any{"a": 1}},
		{"array replaced wholesale", []any{"a", "b"}, []any{"only"}, []any{"only"}},
		{"a scalar managed beats an object config", map[string]any{"a": 1}, "bytes", "bytes"},
		{"an object managed over a scalar config replaces it", "scalar", map[string]any{"a": 1}, map[string]any{"a": 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := enforceManaged(tc.config, tc.managed); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("enforceManaged(%#v, %#v) = %#v, want %#v", tc.config, tc.managed, got, tc.want)
			}
		})
	}
}

// TestEnforceManagedDeepMergesObjects pins the DEEP half: a managed object
// merges key-by-key into the config object rather than replacing it, so a
// sibling the host set under the same top-level key survives. This is the
// "shallow-Enforce subtree clobber" fix the Phase B surfaces needed; the rewrite
// it fails is a shallow assignment loop.
func TestEnforceManagedDeepMergesObjects(t *testing.T) {
	config := map[string]any{
		"permissions": map[string]any{"ask": []any{"Bash(rm:*)"}, "allow": []any{"host"}},
	}
	managed := map[string]any{
		"permissions": map[string]any{"allow": []any{}},
	}
	got := enforceManaged(config, managed).(map[string]any)
	perms := got["permissions"].(map[string]any)
	if !reflect.DeepEqual(perms["ask"], []any{"Bash(rm:*)"}) {
		t.Errorf("host sibling under a managed key was clobbered: %#v", perms)
	}
	if !reflect.DeepEqual(perms["allow"], []any{}) {
		t.Errorf("managed leaf did not win: %#v", perms)
	}
}

// TestEnforceManagedSharesNoStructureWithManaged pins the deep-copy-in: the
// result must not alias the managed layer, at any depth, in either the object
// or the whole-value branch. Without it a later render mutating the composed
// config would rewrite the manifest's own managed layer in memory, and the next
// surface composed in the same process would enforce the corrupted value.
func TestEnforceManagedSharesNoStructureWithManaged(t *testing.T) {
	managed := map[string]any{"limits": map[string]any{"cpu": int64(4)}}

	got := enforceManaged(map[string]any{}, managed).(map[string]any)
	got["limits"].(map[string]any)["cpu"] = int64(999)
	if managed["limits"].(map[string]any)["cpu"] != int64(4) {
		t.Errorf("object branch aliases the managed layer: %#v", managed)
	}

	// The whole-value branch copies too (a keyless surface whose managed layer
	// is a table, and the reason newCtx copies through deepCopyValue).
	wholeManaged := map[string]any{"nested": []any{"a"}}
	whole := enforceManaged("host bytes", wholeManaged).(map[string]any)
	whole["nested"].([]any)[0] = "rewritten"
	if !reflect.DeepEqual(wholeManaged["nested"], []any{"a"}) {
		t.Errorf("whole-value branch aliases the managed layer: %#v", wholeManaged)
	}
}

// TestEnforceManagedMutatesConfigInPlace pins the ALIASING, which is the third
// thing the move had to preserve (§4.2 / enforce.go item 3): when both sides are
// objects the config map handed in is the map written to and the map returned.
//
// This is not an aesthetic claim about the code — it is what any caller still
// holding the merged map observes. Compose hands its folded map straight in and
// keeps using the returned one, and a caller that kept a reference to the map it
// passed sees the enforced keys through it. A pure copying rewrite is not wrong
// in itself, but it changes that, so it has to be made deliberately and
// re-measured against every holder — not arrived at while tidying. If you are
// here because this test failed, that is the conversation it is asking for.
func TestEnforceManagedMutatesConfigInPlace(t *testing.T) {
	config := map[string]any{"theme": "dark"}
	got := enforceManaged(config, map[string]any{"defaultProjectTrust": "always"})

	gotMap, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("enforceManaged returned %T, want an object", got)
	}
	if !sameMap(gotMap, config) {
		t.Error("the returned object is a different map from the config passed in; " +
			"the floor enforces IN PLACE (§4.2 item 3)")
	}
	if config["defaultProjectTrust"] != "always" {
		t.Errorf("caller's map was not written through: %#v", config)
	}
}

// sameMap reports whether two maps are the same underlying map. Go has no `==`
// for maps; reflect.Value.Pointer on a map is the map header address, which is
// exactly the identity question asked here.
func sameMap(a, b map[string]any) bool {
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}
