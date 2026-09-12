package agentcfg

// enforce.go is the MANAGED FLOOR: the step that re-asserts the surface's
// `managed` layer over everything the fold produced, so yolo's non-negotiable
// keys win the rendered file regardless of what any layer below them said.
//
// It lived as a method on luahook.Ctx until docs/design/lua-transform-removal.md
// §4.2 lifted it here unchanged. Nothing about it is Lua: it is the last step of
// the §3.1 pipeline (… → enforce(managed) → encode), so it belongs to the
// pipeline's own package. The move preserved three semantics that a tidier
// rewrite would each silently change — every one of them is pinned by a test in
// enforce_test.go, and each pin names which:
//
//  1. THIS IS NOT RFC 7386. engine.go's mergeValue deletes a key whose patch
//     value is null; enforceValue ASSIGNS it. The two are one `if` apart and
//     look interchangeable. They are not: mergeValue is the fold's merge, this
//     is the floor's, and a managed null means "this key renders as null", not
//     "drop whatever the host set".
//  2. THE INPUT IS THE ORIGINAL managed layer, not a defensive copy of it. When
//     a transform still ran between the fold and the floor, the Lua-visible
//     ctx.managed was a deep copy precisely so a script could scribble on it
//     without moving the floor; the floor read the untouched original. Compose
//     passes in.Surface.Managed directly for the same reason.
//  3. THE CONFIG MAP IS MUTATED IN PLACE when both sides are objects, and the
//     same map is returned. A pure copying rewrite would change aliasing for any
//     caller still holding the merged map.

// enforceManaged re-applies the managed layer over config, managed keys winning,
// and returns the enforced value. It is the pipeline's enforce step (§3.1), run
// AFTER every merged layer.
//
// The merge is DEEP: a managed OBJECT merges key-by-key into the existing config
// object rather than replacing it wholesale, so siblings under the same
// top-level key survive (e.g. a host `permissions.ask` is kept while yolo forces
// `permissions.allow`). A managed scalar/array still replaces. This closes the
// "shallow-Enforce subtree clobber" fidelity gap the Phase B surfaces documented
// (claude/copilot managed nested objects). Managed values are deep-copied in, so
// the result never shares mutable structure with the managed layer.
//
// For a KEYLESS surface (raw/lines) there is nothing to merge key-by-key: a
// non-nil managed layer replaces the whole rendered value. `managed` on a raw
// surface therefore means "this file is exactly these bytes", which is coarse
// but is the only thing "enforce" can mean without keys. A nil managed layer
// leaves config alone, so a surface with no managed value is untouched.
//
// When both sides are objects the config map is mutated IN PLACE and returned
// (item 3 above).
func enforceManaged(config, managed any) any {
	cfgMap, cfgIsObj := config.(map[string]any)
	encMap, encIsObj := managed.(map[string]any)
	if !cfgIsObj || !encIsObj {
		if managed != nil {
			return deepCopyValue(managed)
		}
		return config
	}
	for k, v := range encMap {
		cfgMap[k] = enforceValue(cfgMap[k], v)
	}
	return cfgMap
}

// enforceValue merges a managed value over the current one, managed winning.
// Two objects merge recursively (so siblings survive); anything else — a scalar,
// an array, a type mismatch, or nil — is replaced by a deep copy of the managed
// value.
//
// The nil case is the one that is NOT mergeValue: see item 1 of the file
// comment. Do not "unify" the two.
func enforceValue(cur, managed any) any {
	mMap, mIsObj := managed.(map[string]any)
	cMap, cIsObj := cur.(map[string]any)
	if !mIsObj || !cIsObj {
		return deepCopyValue(managed)
	}
	out := make(map[string]any, len(cMap)+len(mMap))
	for k, v := range cMap {
		out[k] = v
	}
	for k, v := range mMap {
		out[k] = enforceValue(cMap[k], v)
	}
	return out
}

// deepCopyMap returns a deep copy of m (maps/slices cloned, scalars copied), so
// the returned value shares no mutable structure with m. nil in -> nil out.
func deepCopyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = deepCopyValue(v)
	}
	return out
}

// deepCopyValue deep-copies the decoded-config value shapes: map[string]any,
// []any, and scalars. Unknown types are returned as-is (treated as immutable).
func deepCopyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return deepCopyMap(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = deepCopyValue(e)
		}
		return out
	default:
		return v
	}
}
