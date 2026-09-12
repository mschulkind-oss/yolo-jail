package agentcfg

// literalnull.go carries the ONE value a merge patch cannot express: a key whose
// value is a literal `null`.
//
// THE COLLISION, stated once. Every layer in this engine folds through RFC 7386,
// where a null under a key DELETES that key (mergeValue), and the capture overlay
// depends on exactly that: a key the user deleted in-jail is stored as a null
// tombstone so the next boot does not resurrect it
// (docs/reference/config-migration-to-prism.md §3.4). The same null therefore
// cannot also mean "the value here is null" — inside a patch the two meanings are
// one token. mergeDiff's docstring has said so since it was written: *"a patch
// cannot express 'set a key to a literal null'"*.
//
// WHAT THAT COST. A user's `~/.acme/settings.json` holding `"apiKeyHelper": null`
// composes to a file WITHOUT that key, so switching a home to `own` deleted it and
// no loss field named the deletion — §11's second measured bug
// (docs/design/config-ownership-and-promotion.md §11, OQ-CO12). It is not the same
// bug as the `{}` one beside it in that table: an empty object reaches the overlay
// perfectly well once dropNullLeaves stops eating it, and is then carried by the
// fold unharmed. Only the null needs a channel of its own.
//
// THE CHANNEL. A DECODED FILE HOLDS NO TOMBSTONES — every null in it is a null the
// user wrote — so the ambiguity that exists inside a patch does not exist at the
// file. ComposeStateful reads the marked keypaths straight off the current file on
// every branch and hands them to Compose as Inputs.LiteralNulls, which reinstates
// them after the fold. Three consequences worth knowing:
//
//   - The overlay sidecar is UNTOUCHED and stays a clean merge patch. dropNullLeaves
//     goes on stripping every null from an adopted residue, which is still right:
//     the residue is a patch, and a patch's nulls are tombstones.
//   - It is SELF-SUSTAINING rather than durable. The render writes the null back, so
//     the next boot reads the same mark off the same file. Delete the key and the
//     mark goes with it, which is the capture behaviour a user expects;
//     `yolo config reset` truncates to the pure render and takes it too, which is
//     what reset means.
//   - A literal null cannot be PROMOTED. A pack's `config-overlay` is a merge patch
//     as well, so no destination could hold it. `yolo config promote` refuses it as
//     not in the capture, and that refusal is correct rather than a gap.

// literalNullSkeleton returns the marked-keypath skeleton of a DECODED surface
// file: a map mirroring m in which a `nil` leaf marks a key the file holds with a
// literal null, and an object value means something below that key is marked.
// Unmarked keys and empty branches are pruned, so len(result) == 0 means the file
// holds no nulls at any depth — which is every TOML, raw and lines surface by
// construction, and most JSON ones.
//
// It reads VALUES, never the patch a diff produces, which is what makes it
// unambiguous: see the file comment.
func literalNullSkeleton(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		switch t := v.(type) {
		case nil:
			out[k] = nil
		case map[string]any:
			if inner := literalNullSkeleton(t); len(inner) > 0 {
				out[k] = inner
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// reinstateLiteralNulls sets each marked keypath of skeleton to a literal nil in
// cfg, and returns the TOP-LEVEL keys it claimed (for provenance). It mutates cfg.
//
// layers is the evidence for "did anything that OUTRANKS THE FILE speak for this
// key?" — every object layer above the capture overlay in the fold, plus managed.
// Compose builds it and says why the line falls there; the short form is that a
// literal null is a CAPTURED VALUE carried outside the sidecar, so it folds at the
// overlay's precedence: `computed` and `managed` beat it, `defaults`, `host`,
// `workspace` and every `config-overlay` lose to it, exactly as they lose to a
// non-null value the capture overlay does carry.
//
// ⚠ THE CAPTURE OVERLAY IS NOT IN THAT LIST and must stay out — it is a record OF
// the file, so it cannot be evidence AGAINST it. Compose says why in full.
func reinstateLiteralNulls(cfg, skeleton map[string]any, layers []map[string]any) []string {
	var created []string
	for k, marked := range skeleton {
		if reinstateAt(cfg, k, marked, layers) {
			created = append(created, k)
		}
	}
	return created
}

// reinstateAt reinstates one key of one level and reports whether it CREATED that
// key in cfg (as opposed to descending into one that was already there, or
// declining).
func reinstateAt(cfg map[string]any, k string, marked any, layers []map[string]any) bool {
	spoken, allObjects := layersSpeakFor(layers, k)
	cur, present := cfg[k]

	sub, markedIsObject := marked.(map[string]any)
	if !markedIsObject {
		// A marked LEAF: the file holds `k: null` right here.
		//
		// ⚠ `present` IS NOT A VETO, and it was until 2026-09-12. cfg[k] is present
		// precisely when some layer supplied a value there, and `spoken` has already
		// asked whether any layer that OUTRANKS THE FILE did. So a value still
		// standing here came from BELOW the overlay — a `defaults`, `host`,
		// `workspace` or `config-overlay` value — and those lose to a captured value
		// (Compose's overlayIdx comment measures the case: `"theme": null` against a
		// `defaults` of `"system"` kept the null under `assert` and took the default
		// under `own`, which is §11's criterion broken). Declining on `present` made
		// the null the ONLY captured value in the engine that a lower layer could
		// overwrite.
		if spoken {
			return false
		}
		cfg[k] = nil
		return true
	}

	// A marked SUBTREE: some descendant of k is null in the file. Descend only
	// while both sides agree k is an object — a layer holding a scalar, an array
	// or a tombstone at k has replaced the whole subtree, and so has the fold.
	if spoken && !allObjects {
		return false
	}
	curMap, curIsObject := cur.(map[string]any)
	if present && !curIsObject {
		return false
	}
	if !present {
		curMap = map[string]any{}
	}
	child := childLayers(layers, k)
	changed := false
	for ck, cm := range sub {
		if reinstateAt(curMap, ck, cm, child) {
			changed = true
		}
	}
	if present {
		// Descended into a key the fold already produced; nothing was created at
		// THIS level even if something was below it.
		return false
	}
	if !changed {
		// Every descendant declined, so attaching an empty object here would
		// invent a key the file's nulls do not justify.
		return false
	}
	cfg[k] = curMap
	return true
}

// layersSpeakFor reports whether any layer mentions k, and whether EVERY layer
// that does holds it as an object.
//
// "Every", not "any", is the fail-safe direction: a key one layer supplies as an
// object and a later one tombstones is gone from the fold, and treating it as
// object-shaped would rebuild it from the file's nulls.
func layersSpeakFor(layers []map[string]any, k string) (spoken, allObjects bool) {
	allObjects = true
	for _, l := range layers {
		v, ok := l[k]
		if !ok {
			continue
		}
		spoken = true
		if _, isObject := v.(map[string]any); !isObject {
			allObjects = false
		}
	}
	return spoken, spoken && allObjects
}

// childLayers returns the sub-maps every layer holds at k, so a recursion keeps
// asking the same question one level down. A layer that does not hold k as an
// object contributes nothing — layersSpeakFor has already refused to descend past
// one of those.
func childLayers(layers []map[string]any, k string) []map[string]any {
	var out []map[string]any
	for _, l := range layers {
		if sub, ok := l[k].(map[string]any); ok {
			out = append(out, sub)
		}
	}
	return out
}
