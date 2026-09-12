package agentcfg

// staterender.go is the stateful boot render harness: the §5 capture-diff
// overlay loop plus the §3.2 first-migration path from
// docs/reference/config-migration-to-prism.md. Compose (compose.go) renders ONE
// surface as a pure function of its layers; ComposeStateful wraps it with the
// per-boot state machine that decides the overlay layer from the sidecar files
// and reports what the caller must persist.
//
// §3.2 originally called that path a BOOTSTRAP that seeds an empty overlay and
// skips capture. B1 inverted it — it now ADOPTS the on-disk file — and the
// docstrings below describe the body as it stands, not as §3.2 wrote it.
//
// It stays PURE — no file I/O, no container. Each caller reads the two sidecars
// and the current surface file, hands their bytes here, and writes back the part
// of StatefulOutput its job needs:
//
//   - internal/entrypoint's boot path (prism.go) is the full one — surface file,
//     last_render and overlay, three writes every boot.
//   - internal/cli's CAPTURE path (captureSurfaceAt, configdiff.go — behind
//     `yolo config capture` and the capture-on-terminate fold in
//     configcapture.go) writes ONLY the overlay sidecar. It is here to reuse the
//     engine's capture diff rather than grow a second one that could disagree
//     with the boot render; the surface file and last_render stay the boot's to
//     write.
//
// `yolo config diff` is NOT a caller: it reports the overlay sidecar's own content
// and deliberately does not re-compose (configDiff's docstring says why).
//
// Keeping the state machine here means the hard parts — first-migration
// detection, the §3.3 defensive handling of dangling/corrupt sidecars, and the
// diff/accumulate/render loop — are unit-tested with zero filesystem, and it
// can use the unexported mergeDiff/mergeAccumulate directly.
//
// The two sidecars (§5), which the caller stores in `<workspace>/.yolo/prism/`.
// B3: they are DIFFERENT KINDS of thing, and conflating them has caused real
// mistakes — reason about each by its kind, not by "the sidecars":
//
//   - overlay — DURABLE STATE. The accumulated in-jail edits, and the only record
//     that they ever happened. Nothing else can reconstruct it. Losing it loses
//     every captured edit permanently; that is why `yolo config reset` deleting it
//     IS the discard operation, and why it lives in the workspace rather than a
//     cache dir. ALWAYS JSON: the overlay must carry `null` tombstones (a captured
//     deletion), which the TOML/lines codecs cannot express, and JSON is the one
//     codec that round-trips the generic value model including nulls. Engine-
//     internal — the agent never sees it — so its format is yolo's choice, not the
//     surface's.
//
//   - last_render — a ONE-BOOT PENDING-EDIT BASELINE. Not a cache and not durable
//     state. It is the exact surface-codec bytes yolo wrote last boot, stored in the
//     surface's own codec so it byte-matches the file and diffs cleanly.
//
//     It is tempting to call it a cache, since it is derivable in principle — and
//     that is exactly the mistake. Deleting it does NOT cause a harmless
//     recompute: it destroys the ability to tell an in-jail EDIT from yolo's own
//     previous output, so every edit made since the last boot and not yet captured
//     is silently lost. Before B1 it was worse — a missing baseline discarded the
//     whole file's agent-owned state (the copilot OAuth wipe). It is "state" only
//     for the span of one boot cycle, after which it is rewritten.
//
// Practical consequence: the overlay must be preserved and backed up like data;
// last_render must be preserved across a single restart but is meaningless
// afterwards. Neither is a cache, and nothing here may be pruned as one.
//
// VOCABULARY (E/3.7). Three terms are in use and they are NOT synonyms, which is why a
// blanket rename would lose information rather than add clarity:
//
//	in-jail edit       the ACT: an agent or user writing to a composed file
//	captured edits     the user-facing name for the STATE that survives regeneration.
//	                   This is the umbrella term — what `yolo config diff` shows and
//	                   `yolo config reset` discards
//	captured overlay   the specific LAYER in the fold, between workspace and computed
//
// Deliberately NOT "managed": that already means keys YOLO re-asserts and wins, which
// is the opposite relationship. Using one word for both would make the fold order
// unreadable.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
)

// StatefulInputs is everything the boot harness needs to render one surface
// with overlay capture. The Base carries the stateless layers (surface,
// host bytes, workspace, script, VM); the harness computes Base.Overlay itself
// from the sidecars, so any Overlay set on Base is ignored.
type StatefulInputs struct {
	// Base is the stateless composition input (compose.go). Its Overlay field is
	// overwritten by the harness — set the surface, host, workspace, and script
	// here, not the overlay.
	Base Inputs

	// CurrentBytes is the current on-disk content of the surface file (the bytes
	// the agent may have edited in-jail), or nil/empty when the file is absent.
	// Decoded with the surface codec for the §5 capture diff.
	CurrentBytes []byte

	// LastRenderPresent reports whether the last_render sidecar exists on disk.
	// Its ABSENCE is the first-migration signal (§3.2). With no baseline there is
	// nothing to diff against, so capture is impossible — and since B1 the harness
	// ADOPTS the on-disk file instead of discarding it, seeding the overlay from
	// what the file holds beyond the pure render. It seeded an EMPTY overlay here
	// before B1, which is what wiped copilot's OAuth tokens.
	LastRenderPresent bool
	// LastRenderBytes is the last_render sidecar content (the surface-codec bytes
	// yolo wrote last boot). Ignored when LastRenderPresent is false. A present
	// but empty or undecodable value is treated as a first migration (§3.3): it
	// cannot be trusted as a diff baseline, so this boot adopts rather than
	// capturing a diff against a baseline that is not there.
	LastRenderBytes []byte

	// OverlayJSON is the overlay sidecar content (JSON), or nil when absent. On a
	// first migration it is IGNORED regardless (§3.3 dangling-overlay case): that
	// boot's overlay is rebuilt from the on-disk file by adoption, so a dangling
	// sidecar cannot leak into it. (Before B1 the rebuilt value was always {},
	// which is why this said "reset to {}".)
	OverlayJSON []byte
}

// StatefulOutput is the render plus the two sidecar values the caller must
// persist. The BOOT caller writes Result.Encoded to the surface path,
// LastRenderBytes to the last_render sidecar, and OverlayJSON to the overlay
// sidecar — three writes, unconditionally, every boot. The capture path writes
// OverlayJSON alone (see the file header).
type StatefulOutput struct {
	// Result is the composed surface (compose.go Result): Config, Encoded bytes,
	// Provenance.
	Result *Result

	// LastRenderBytes is what to write to the last_render sidecar: exactly
	// Result.Encoded (the surface-codec bytes just rendered). Provided as a
	// named field so the caller's intent reads clearly at the write site.
	LastRenderBytes []byte

	// OverlayJSON is what to write to the overlay sidecar (JSON): on a first
	// migration the ADOPTED residue of the on-disk file — {} when there is no file,
	// or nothing in it beyond what yolo already asserts — else the accumulated
	// overlay after this boot's capture. Narrowed against computed and managed
	// either way.
	OverlayJSON []byte

	// FirstMigration reports that this boot took the §3.2 path (absent or untrusted
	// last_render): the overlay came from ADOPTING the on-disk file rather than
	// from a capture diff. The caller uses this to gate the one-time §4.7
	// orphan-file cleanup.
	FirstMigration bool
}

// ComposeStateful runs the per-boot state machine for one surface and returns
// the render plus the sidecar values to persist. It never returns an error for
// a recoverable on-disk condition (corrupt/empty sidecar, corrupt or absent
// current file) — those self-heal by re-seeding or skipping capture, so a
// mangled home can never break the boot. It DOES return an error for a genuine
// programmer error (unknown codec, or a Compose failure such as a shape mismatch),
// matching Compose's fail-closed contract (§3.4).
//
// The two paths (docs/reference/config-migration-to-prism.md §3.2):
//
//	first migration (last_render absent/untrusted):   # B1: ADOPT, don't discard
//	    pure    = Compose(overlay=∅)
//	    overlay = dropNullLeaves(mergeDiff(pure, current_decoded))  # the residue
//	    overlay = dropComputedTables(overlay)                       # wholesale
//	steady state (last_render trusted):
//	    delta   = mergeDiff(last_render_decoded, current_decoded)
//	    overlay = mergeAccumulate(overlay, delta)                   # §3.4 tombstones
//	then BOTH paths:
//	    overlay = narrowOverlay(overlay, computed, managed)         # leaf-level
//	    render  = Compose(overlay)
//	    write surface_path, last_render := render, overlay := overlay
func ComposeStateful(in StatefulInputs) (*StatefulOutput, error) {
	c, ok := codec.LookupCodec(in.Base.Surface.Codec)
	if !ok {
		return nil, fmt.Errorf("agentcfg: surface %s/%s: unknown codec %q",
			in.Base.Surface.Agent, in.Base.Surface.Name, in.Base.Surface.Codec)
	}

	kind := in.Base.Surface.Kind()

	// Decide the effective overlay and whether this is a first migration.
	//
	// A last_render sidecar is TRUSTED only when it is present AND decodes to the
	// surface's own shape. Absent, empty, or undecodable last_render => first
	// migration (§3.2 / §3.3): there is no baseline, so capture is impossible and
	// the branch below ADOPTS the on-disk file instead (B1). It adopts a NARROWED
	// residue rather than the file itself, because taking the whole file would pin
	// stale bespoke output (§3.1) — that narrowing is what the two passes do.
	lastRender, lastOK := decodeKind(c, kind, in.LastRenderBytes)
	firstMigration := !in.LastRenderPresent || !lastOK

	var overlay any
	if firstMigration {
		// B1 (⚠ DATA LOSS FIX): ADOPT the on-disk file instead of discarding it.
		//
		// This branch used to seed an EMPTY overlay, which silently destroyed every
		// agent-owned key in the file. copilot/config is the sharp case: it renders
		// with Defaults {"yolo": true} and no host layer, so a boot with an
		// absent/corrupt last_render — a fresh workspace, a deleted sidecar, an
		// interrupted migration — collapsed a file holding copilot_tokens /
		// logged_in_users / last_logged_in_user to {"yolo": true} and logged the user
		// out. Steady state recovered, which is why it went unnoticed.
		//
		// Adopting = seed the overlay with mergeDiff(pureRender, current): the
		// residue of the on-disk file after subtracting what yolo itself would
		// produce. That keeps agent-owned state and lets yolo's own layers win for
		// the keys yolo asserts, with no markers and no recursion.
		//
		// This is safe against `yolo config reset` ONLY because reset also truncates
		// the surface to the pure render (ruling 1, configReset). Without that,
		// reset → no baseline → adopt would resurrect the very edits the user asked
		// to discard, making reset a no-op. The two halves are one change.
		//
		// "Empty" is nil for a keyless surface, NOT the zero value: an empty
		// string is a real assertion that the file is empty and would win the
		// fold, blanking the render. nil means "this layer says nothing".
		overlay = emptyOverlay(kind)
		if current, curOK := decodeKind(c, kind, in.CurrentBytes); curOK {
			if kind == codec.KindObject {
				// Subtract what a pure render would produce; keep the rest.
				pure, perr := Compose(in.Base)
				if perr != nil {
					return nil, perr
				}
				pureMap, _ := decodeKind(c, kind, pure.Encoded)
				pm, _ := pureMap.(map[string]any)
				curMap, _ := current.(map[string]any)
				residue := dropNullLeaves(mergeDiff(pm, curMap))
				// Narrowing the residue takes TWO passes at DIFFERENT GRANULARITIES,
				// and neither subsumes the other.
				//
				// (a) WHOLESALE, here: a top-level key the COMPUTED layer holds as an
				// object is a table yolo regenerates in full, so what sits under it on
				// disk is taken to be yolo's own previous output rather than a captured
				// edit (dropComputedTables — read its docstring for where that reading is
				// too coarse, and what it costs).
				//
				// (b) LEAF-LEVEL, after this branch: the SHARED narrowing both branches
				// run (narrowOverlay), which is the exact dual of the merge each owning
				// layer gets — it keeps a captured SIBLING inside an object-valued
				// owner, because such a sibling really does reach the file.
				//
				// (b) CANNOT SUBSUME (a), which is why both exist: dropOverriddenKeys
				// recurses into an object-valued owner and keeps every key the owner
				// lacks — and a stale entry under a computed table is exactly such a
				// key, so the leaf pass alone resurrects an MCP server dropped from
				// config (§2 principle 1, regenerate-don't-reconcile).
				//
				// Neither may be the OLD blanket drop of every top-level key the pure
				// render holds as an object. That took managed `permissions` with it,
				// so `permissions.ask` — a leaf managed does not hold, which Enforce
				// merges around — was silently lost on every adopting boot, while the
				// very next boot captured it happily. dropOverriddenKeys says why in as
				// many words: a blanket top-level key drop is "simpler and wrong".
				residue = dropComputedTables(residue, in.Base.Computed)
				if len(residue) > 0 {
					overlay = residue
				}
			}
			// KEYLESS surfaces are deliberately NOT adopted. A raw/lines surface has
			// one "key" — the whole file — so adoption would mean "the existing file
			// wins outright", which defeats the host layer entirely: a readonly
			// host-mirrored file would freeze at whatever stale content was on disk
			// and never pick up host-side changes again. There is also no partial
			// residue to take, so nothing here is recoverable the way an object's
			// unasserted keys are. Object surfaces are where the data-loss risk lives
			// (copilot's tokens), and they get exact key-level adoption instead.
		}
	} else {
		// Steady state. Start from the persisted overlay ({} if absent — §3.3
		// case 3), then accumulate this boot's captured delta.
		overlay = parseOverlayKind(kind, in.OverlayJSON)
		if current, curOK := decodeKind(c, kind, in.CurrentBytes); curOK {
			// §5: diff the on-disk file against the trusted baseline and fold the
			// delta into the durable overlay.
			//
			// For an OBJECT surface that is the RFC-7386 diff: mergeAccumulate
			// preserves null tombstones so a captured deletion persists (§3.4).
			//
			// For a KEYLESS surface (raw/lines) the same loop holds with a
			// degenerate diff: the file has one "key" — itself — so "did it
			// change" is value inequality, and the captured delta is the whole
			// edited value. Accumulate is replacement (the newest edit is the
			// overlay). Nothing else about capture differs, which is why raw
			// files get edit-survives-regeneration for free rather than needing a
			// parallel mechanism.
			if kind == codec.KindObject {
				lastMap, _ := lastRender.(map[string]any)
				curMap, _ := current.(map[string]any)
				overlayMap, _ := overlay.(map[string]any)
				delta := mergeDiff(lastMap, curMap)
				overlay = mergeAccumulate(overlayMap, delta)
			} else if !reflect.DeepEqual(lastRender, current) {
				overlay = current
			}
		}
		// A corrupt/absent current file (curOK false) skips capture: we bias
		// toward under-capture rather than freezing a spurious delta into the
		// never-aging overlay.
	}

	// ONE RULE, BOTH BRANCHES: strip from the decided overlay everything a
	// higher-ranking layer — computed, then managed — will override anyway.
	// Adoption has narrowed its residue against managed since B1; steady-state
	// capture did NOT, and neither branch narrowed against computed at all, so
	// every boot that saw such a key edited on disk folded it into the sidecar as
	// permanent, un-actionable noise.
	//
	// It runs on the ACCUMULATED overlay rather than on the incoming delta, and
	// that is the whole point: narrowing the delta would only stop NEW
	// contamination and leave every sidecar already carrying dead keys dirty
	// forever. Narrowing after the accumulate makes the store SELF-HEALING — a
	// sidecar an older yolo wrote is canonicalized on the next boot.
	overlay = narrowOverlay(kind, overlay, in.Base.Computed, in.Base.Surface.Managed)

	// Render with the decided overlay. Compose owns decode/merge/enforce/encode
	// and is the exact engine `yolo config render` uses (§6).
	base := in.Base
	base.Overlay = overlay
	res, err := Compose(base)
	if err != nil {
		return nil, err
	}

	// The overlay sidecar to persist. Marshal deterministically. On a first
	// migration this is the ADOPTED residue, not `{}` — that was the pre-B1 rule,
	// and the whole point of B1 is that the residue is the only surviving record of
	// the agent-owned keys the file held (StatefulOutput.OverlayJSON). It is `{}`
	// only when the file held nothing beyond what yolo already asserts.
	overlayJSON, err := marshalOverlay(overlay)
	if err != nil {
		return nil, fmt.Errorf("agentcfg: surface %s/%s: marshal overlay: %w",
			in.Base.Surface.Agent, in.Base.Surface.Name, err)
	}

	return &StatefulOutput{
		Result:          res,
		LastRenderBytes: res.Encoded,
		OverlayJSON:     overlayJSON,
		FirstMigration:  firstMigration,
	}, nil
}

// decodeKind decodes bytes with the surface codec and reports success only when
// the result is non-empty input that decodes to the surface's own shape. Empty
// input, a decode error, or a shape mismatch all report ok=false — the callers
// treat every one as "cannot trust / cannot capture", which is the conservative
// choice for both the last_render baseline and the current file.
//
// The kind check is what makes this usable for raw/lines surfaces: an object-only
// check reported ok=false for every raw file, which silently disabled capture for
// them (an edit to a raw surface was quietly discarded every boot).
func decodeKind(c codec.Codec, kind codec.Kind, data []byte) (any, bool) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, false
	}
	decoded, err := c.Decode(data)
	if err != nil {
		return nil, false
	}
	if !kind.Matches(decoded) {
		return nil, false
	}
	return decoded, true
}

// dropComputedTables removes from an adopted first-migration residue every
// top-level key the COMPUTED layer holds as an OBJECT — a table yolo regenerates
// in full every boot. The bet it makes is that such a key is yolo's own output
// from a previous boot rather than agent state, and on a first migration the
// computed layer's SHAPE is the only signal available to make it: last_render —
// which normally disambiguates the two — is exactly what is missing.
//
// THE BET IS KEY-LEVEL AND THE COMPUTED LAYER IS NOT, so it over-drops whenever a
// derive returns an object it only partly fills in. ⚠ claude/settings is the live
// case, measured against the shipped pack: packs/claude/derive.lua returns `env`
// and `enabledPlugins` as objects while asserting only its own leaves inside them
// (ENABLE_LSP_TOOL, the three LSP plugin ids), so an adopting boot drops the
// agent's OTHER env vars and the user's OTHER enabled plugins wholesale — the same
// two-branches-disagree loss the `permissions` fix below closed, one key over on
// the same file. It is PRE-EXISTING, not a regression: the blanket pure-render drop
// this replaced took both keys too. Closing it needs a leaf-level signal this
// function does not have (which leaves under a computed table the derive actually
// asserted), so the boundary is stated here rather than papered over.
//
// Without it, adoption resurrects dropped yolo-owned entries and breaks §2
// principle 1 ("regenerate, don't reconcile"): an MCP server removed from config
// would come back, because the stale entry sits under mcp_servers, a table yolo
// computes wholesale. Verified against the real surfaces — codex's
// mcp_servers.staleServer, opencode's mcp.staleServer and mise's stale baked
// [tools] runtimes all resurrected before this.
//
// THE GRANULARITY IS THE WHOLE POINT, in both directions:
//
//   - WHOLESALE against COMPUTED, because the leaf-level narrowing cannot express
//     this. dropOverriddenKeys is the dual of a merge-patch and therefore KEEPS a
//     key the owner lacks — which is precisely the stale entry. Deleting this pass
//     leaves the resurrection class wide open with every other test still green.
//   - NOT wholesale against the PURE RENDER, which is what this replaced. The pure
//     render holds managed's `permissions` as an object too, so the blanket drop
//     took Claude's own `permissions.ask` with it: an adopting boot silently
//     discarded the agent's permission list, while a steady-state boot kept it.
//     Everything below computed — managed, host, workspace, defaults — merges
//     key-by-key, so it gets the leaf-level pass (narrowOverlay) instead.
//
// A key the computed layer does not hold as an object is untouched here: either
// yolo knows nothing about it, in which case it is agent state and IS adopted
// (the copilot_tokens case this whole branch exists for), or an owning layer
// asserts it as a leaf and the leaf-level pass will drop it.
func dropComputedTables(residue map[string]any, computed any) map[string]any {
	computedMap, ok := computed.(map[string]any)
	if !ok {
		return residue
	}
	out := make(map[string]any, len(residue))
	for k, v := range residue {
		if _, wholesale := computedMap[k].(map[string]any); wholesale {
			continue
		}
		out[k] = v
	}
	return out
}

// narrowOverlay removes from a decided overlay every entry a HIGHER-RANKING
// layer will override anyway, so the sidecar holds only captured edits that can
// actually reach the written file.
//
// TWO layers outrank the capture overlay, and both disqualify a capture for the
// same reason:
//
//   - COMPUTED folds directly above the overlay (compose.go). It is yolo's
//     per-boot regenerated data — the reconciled MCP-server table, claude's
//     LSP-driven enabledPlugins toggles and env.ENABLE_LSP_TOOL, mise's injected
//     [tools] pins — and the §4 slot exists precisely "so it wins over a stale
//     in-jail edit to the same key (§2 principle 1, regenerate-don't-reconcile)".
//   - MANAGED is re-asserted AFTER the fold (enforceManaged), so it wins the
//     file unconditionally.
//
// A capture either layer overrides changes nothing. It only sits in the sidecar
// as permanent noise, and `yolo config diff` reports a phantom "edit" the user
// cannot act on. (A stale managed VALUE on disk, e.g. codex's
// approval_policy=on-request from an old boot, is exactly this case.) Measured on
// a live jail, this was most of what the store held: mise/config's entire `tools`
// capture, codex/config's entire `mcp_servers`, opencode/config's entire `mcp`.
//
// THE COMPETING SEMANTIC, deliberately abandoned: retaining the captured edit
// would mean that if the owning layer later STOPS supplying the key, the old edit
// silently activates. That was rejected because the pending edit is invisible —
// nothing tells you it is queued — and it can sit for months. Both live examples
// are hazards rather than conveniences: for claude/settings the managed key is
// `permissions`, so activation would restore a stale permission grant nobody
// remembers making; and for computed, deleting an MCP server from your config
// would silently resurrect it from a capture taken while it still existed.
//
// KEYLESS surfaces (raw/lines) are covered by the same rule rather than being
// exempt. They have one "key" — the whole file — so a non-nil computed layer
// replaces the rendered value and enforceManaged replaces it again for managed;
// either way a captured whole-file edit is dead. No pack yolo ships declares
// managed or computed on a keyless surface today, but Surface.Managed and
// Inputs.Computed are both `any` precisely so a surface CAN pin a whole file, so
// the case is representable and is handled here rather than argued away.
func narrowOverlay(kind codec.Kind, overlay, computed, managed any) any {
	// Applied in fold order. Order does not change the result — each pass only
	// removes entries — but it reads the way the layers stack.
	overlay = narrowAgainst(kind, overlay, computed)
	return narrowAgainst(kind, overlay, managed)
}

// narrowAgainst is one pass of narrowOverlay against a single owning layer.
func narrowAgainst(kind codec.Kind, overlay, owner any) any {
	if owner == nil {
		return overlay
	}
	ownerMap, ownerIsObj := owner.(map[string]any)
	overlayMap, overlayIsObj := overlay.(map[string]any)
	if ownerIsObj && overlayIsObj {
		return dropOverriddenKeys(overlayMap, ownerMap)
	}
	// Whole-value override: the owner replaces the entire rendered value, so
	// nothing the overlay holds can survive it.
	return emptyOverlay(kind)
}

// dropOverriddenKeys returns m without the entries an OWNING layer will override
// anyway. It is the exact dual of the merge that layer gets — and has to be,
// because an OBJECT-valued owner merges key-by-key, so a captured SIBLING inside
// it really does reach the file and is a real edit. claude/settings is the live
// case: managed asserts `permissions.defaultMode` and friends, while Claude's own
// `permissions.ask` is untouched by it and must survive. A blanket top-level key
// drop would be simpler and wrong — it would discard the agent's permission list
// on every boot.
//
// THE LINE THIS DRAWS: drop only what is PROVABLY dead from the (overlay, owner)
// pair ALONE. The lower layers — host, workspace, another pack's config-overlay —
// are not visible here, so anything whose deadness depends on them is KEPT. That
// is why a null tombstone under an object-valued owner survives (see below):
// keeping a redundant tombstone is noise, dropping a live one is data loss, and
// this repo has already paid for that class once (B1, the copilot OAuth wipe).
//
// A nested object emptied by the recursion is itself dropped, so a fully-owned
// subtree does not survive as a meaningless {}.
func dropOverriddenKeys(m, owner map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		ov, owned := owner[k]
		if !owned {
			out[k] = v
			continue
		}
		oSub, oIsObj := ov.(map[string]any)
		if !oIsObj {
			// The owner replaces the whole value at k (or, for a null, deletes it),
			// so nothing the overlay holds at k — a value or a tombstone — survives.
			continue
		}
		if v == nil {
			// A TOMBSTONE under an object-valued owner is still LIVE: it erases what
			// the lower layers put at k, leaving only the owner's own keys. Whether
			// any lower layer contributes is not knowable here, so it is kept.
			out[k] = v
			continue
		}
		vSub, vIsObj := v.(map[string]any)
		if !vIsObj {
			// A non-object under an object owner is discarded by the merge (RFC 7386:
			// a non-object target under an object patch is treated as empty).
			continue
		}
		if inner := dropOverriddenKeys(vSub, oSub); len(inner) > 0 {
			out[k] = inner
		}
	}
	return out
}

// dropNullLeaves returns m without any null-valued entry, recursively. Used to
// strip RFC-7386 tombstones out of an adopted first-migration overlay: the overlay
// must add the agent's own keys without deleting the ones yolo asserts. A nested
// object that becomes empty after stripping is itself dropped, so an
// all-tombstones subtree does not survive as a meaningless {}.
func dropNullLeaves(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		switch t := v.(type) {
		case nil:
			continue
		case map[string]any:
			if inner := dropNullLeaves(t); len(inner) > 0 {
				out[k] = inner
			}
		default:
			out[k] = v
		}
	}
	return out
}

// emptyOverlay is the "no captured edits" overlay for a surface kind.
//
// For an object surface that is `{}` — an empty merge patch, which changes
// nothing. For a keyless surface it is nil, NOT the zero value: Compose treats a
// non-nil keyless layer as a real assertion that wins the fold, so an empty
// string overlay would blank the file instead of deferring to the layers below.
func emptyOverlay(kind codec.Kind) any {
	if kind == codec.KindObject {
		return map[string]any{}
	}
	return nil
}

// parseOverlayKind decodes the overlay sidecar JSON for a surface of kind,
// defaulting to the empty overlay for absent or undecodable content (§3.3: a
// dangling overlay is not trusted). The overlay is always JSON regardless of the
// surface codec (see file header), so a raw surface's captured text is stored as
// a JSON string and a lines surface's as a JSON array.
func parseOverlayKind(kind codec.Kind, data []byte) any {
	if len(bytes.TrimSpace(data)) == 0 {
		return emptyOverlay(kind)
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil || v == nil {
		return emptyOverlay(kind)
	}
	// A sidecar whose shape doesn't match the surface is as untrustworthy as one
	// that won't parse (e.g. the surface's codec changed between boots).
	if !kind.Matches(v) {
		return emptyOverlay(kind)
	}
	return v
}

// marshalOverlay serializes the overlay to stable, indented JSON (sorted keys
// via encoding/json), with a nil object overlay rendering as `{}`. Null
// tombstones survive the round-trip — that is the whole reason the overlay is
// JSON and not the surface codec. A nil keyless overlay marshals to `null`,
// which parseOverlayKind reads back as "no captured edits".
func marshalOverlay(overlay any) ([]byte, error) {
	if m, ok := overlay.(map[string]any); ok && m == nil {
		overlay = map[string]any{}
	}
	return json.MarshalIndent(overlay, "", "  ")
}
