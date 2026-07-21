// Package agentcfg is the pure, codec-agnostic composition engine behind
// yolo's generated-config pipeline (docs/plans/agent-settings-composition.md,
// §3.1 pipeline, §4 layers, §5 overlay).
//
// yolo regenerates every config file it owns (Claude/Codex/pi settings, MCP,
// LSP, mise, identity — the "surfaces" of §1.1) on each boot from an ordered
// stack of layers: defaults < host < workspace < runtime-overlay < managed
// (§4). This package holds ONLY the format-independent core of that pipeline —
// deep-merge composition, the left-to-right layer fold, and the capture-diff
// that lets in-jail edits survive regeneration (§5). It operates purely over
// already-decoded generic values:
//
//   - objects -> map[string]any
//   - arrays  -> []any
//   - scalars -> string / bool / numeric / nil (nil == a decoded JSON null)
//
// Everything with a dependency — the per-surface codecs (decode/encode), the
// Lua transform VM, the manifest loader, file I/O, and CLI wiring — is a
// separate, later Phase-A piece and deliberately lives OUTSIDE this leaf. That
// keeps this the one component that is trivially unit-testable in isolation and
// shared byte-for-byte by both the entrypoint boot render and `yolo config
// render` (§6).
//
// Merge semantics follow RFC 7386 (JSON Merge Patch): objects merge at every
// depth, a null value deletes its key, and any non-object value (array or
// scalar) replaces wholesale. The design (§4) also anticipates a per-keypath
// "append" (with dedupe) strategy that a surface manifest may pin for a keypath
// (e.g. an allow-list) — that is intentionally NOT built here (see the TODO on
// deepMerge); it needs manifest/keypath context this leaf does not carry.
package agentcfg

import "reflect"

// deepMerge composes over onto base and returns a NEW map. It never mutates
// either argument: object nodes are rebuilt fresh, so a caller may keep merging
// earlier layers without corruption. (Untouched leaf arrays/scalars are shared
// by reference — they are treated as immutable values.)
//
// RFC 7386 (JSON Merge Patch) semantics, applied at every depth:
//
//   - object over object      -> recursive per-key merge
//   - null over anything       -> the key is deleted from the result
//   - array over anything      -> the array replaces (no element merge; a future
//     manifest-pinned "append" strategy is the noted exception — not built here)
//   - scalar over anything     -> the scalar replaces
//   - object over non-object   -> the non-object is discarded and the object
//     merges into an empty table (RFC 7386: a patch object treats a non-object
//     target as {}). This is the doc-silent type-change edge (object<->scalar);
//     we take RFC-7386 behavior deliberately.
//
// TODO(append-strategy, §4): a surface manifest may pin `append` (with dedupe)
// for a specific keypath so an allow-list accumulates instead of replacing.
// That requires keypath/manifest context this pure leaf does not have; wire it
// in when the manifest layer lands rather than smuggling strategy flags here.
func deepMerge(base, over map[string]any) map[string]any {
	// over is always an object, so mergeValue always yields an object here.
	return mergeValue(base, over).(map[string]any)
}

// mergeValue is the RFC 7386 MergePatch(target, patch) recursion over generic
// decoded values. A non-object patch replaces the target outright; an object
// patch merges key-by-key, with a null patch value deleting the key.
func mergeValue(target, patch any) any {
	patchMap, patchIsObject := patch.(map[string]any)
	if !patchIsObject {
		// Arrays and scalars replace wholesale. A top-level null reaching here
		// (patch is nil) also "replaces" — but under a key the object branch
		// below intercepts null first and deletes instead, which is where the
		// RFC delete semantics actually live.
		return patch
	}

	// Patch is an object. Per RFC 7386, if the target is not an object it is
	// treated as an empty object (the previous non-object value is discarded).
	targetMap, targetIsObject := target.(map[string]any)
	result := make(map[string]any, len(targetMap))
	if targetIsObject {
		for k, v := range targetMap {
			result[k] = v
		}
	}
	for k, v := range patchMap {
		if v == nil {
			delete(result, k) // null deletes the key (no-op if absent)
			continue
		}
		result[k] = mergeValue(result[k], v)
	}
	return result
}

// mergeAccumulate composes over onto base like deepMerge, but PRESERVES null
// tombstones even when the key is absent in base. It is the §5 overlay
// accumulation primitive (docs/design/config-migration-to-prism.md §3.4):
//
//	overlay = mergeAccumulate(overlay, delta)   # deletions persist across boots
//
// deepMerge (RFC 7386) is correct for the render FOLD — there a null means
// "delete this key from the composed output", so a null over an absent key is
// rightly a no-op. But the OVERLAY is not the output: it is a durable patch that
// is re-applied every boot, so a captured deletion (delta carries `key: null`)
// must be STORED even if the accumulator does not yet hold key — otherwise the
// next boot resurrects the key. mergeAccumulate stores the null instead of
// dropping it. Everything else matches deepMerge (objects merge recursively,
// arrays/scalars replace, non-mutating). It never mutates its arguments.
func mergeAccumulate(base, over map[string]any) map[string]any {
	result := make(map[string]any, len(base))
	for k, v := range base {
		result[k] = v
	}
	for k, v := range over {
		if v == nil {
			result[k] = nil // preserve the tombstone even when k is absent in base
			continue
		}
		if ov, ok := result[k]; ok {
			// Recurse only when BOTH sides are objects; otherwise over replaces.
			if om, oOK := ov.(map[string]any); oOK {
				if nm, nOK := v.(map[string]any); nOK {
					result[k] = mergeAccumulate(om, nm)
					continue
				}
			}
		}
		result[k] = v
	}
	return result
}

// render folds the layers left-to-right with deepMerge, so the LAST argument
// wins any leaf conflict. Callers pass layers in ascending precedence —
// defaults, host, workspace, overlay, managed (§4) — so managed keys win and
// are, in effect, applied last. Zero layers yields an empty map.
func render(layers ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, layer := range layers {
		out = deepMerge(out, layer)
	}
	return out
}

// mergeDiff is the §5 capture-diff: given the previous render (oldR) and the
// current on-disk config (newR), it returns the minimal RFC-7386 patch that
// turns oldR into newR. This patch is what yolo accumulates into the overlay
// sidecar so an in-jail edit survives the next regeneration.
//
//   - a leaf that differs        -> recorded as its new value
//   - a key present only in newR  -> recorded as its (whole) new subtree
//   - a key present only in oldR  -> recorded as an explicit null tombstone, so
//     a deletion is not resurrected by the next render (the exact bug §5/§7 call
//     out in the old three-way merge)
//   - a leaf that is unchanged    -> omitted (keeps the overlay minimal)
//
// The result round-trips: deepMerge(oldR, mergeDiff(oldR, newR)) reconstructs
// newR. It never mutates its arguments (new subtrees are carried by reference,
// treated as immutable).
//
// Limitation (inherited from RFC 7386, and matching render's output which never
// carries nulls): a patch cannot express "set a key to a literal null" — a null
// always means delete. Render outputs strip nulls during composition, so oldR
// and a rendered newR never contain literal nulls; a raw on-disk newR carrying
// a literal null value is the one shape this cannot faithfully capture.
func mergeDiff(oldR, newR map[string]any) map[string]any {
	// oldR and newR are both objects, so diffValue returns an object.
	patch, _ := diffValue(oldR, newR)
	return patch.(map[string]any)
}

// diffValue returns the RFC-7386 patch turning old into new, plus whether
// anything changed. Two objects diff recursively (added keys carry their whole
// subtree, removed keys become null tombstones); any other shape mismatch or
// unequal value is emitted as new wholesale (arrays replace, per §4).
func diffValue(old, new any) (any, bool) {
	oldMap, oldIsObject := old.(map[string]any)
	newMap, newIsObject := new.(map[string]any)

	if oldIsObject && newIsObject {
		patch := map[string]any{}
		for k, nv := range newMap {
			ov, existed := oldMap[k]
			if !existed {
				patch[k] = nv // added key: carry the whole new subtree
				continue
			}
			if sub, changed := diffValue(ov, nv); changed {
				patch[k] = sub
			}
		}
		for k := range oldMap {
			if _, present := newMap[k]; !present {
				patch[k] = nil // tombstone: key removed in newR
			}
		}
		return patch, len(patch) > 0
	}

	// At least one side is a non-object (array/scalar), or the types differ.
	// Arrays and scalars are compared as whole values and replace on any
	// difference (§4: arrays replace by default).
	if reflect.DeepEqual(old, new) {
		return nil, false
	}
	return new, true
}
