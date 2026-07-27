package agentcfg

// staterender.go is the stateful boot render harness: the §5 capture-diff
// overlay loop plus the §3.2 first-migration bootstrap from
// docs/design/config-migration-to-prism.md. Compose (compose.go) renders ONE
// surface as a pure function of its layers; ComposeStateful wraps it with the
// per-boot state machine that decides the overlay layer from the sidecar files
// and reports what the caller must persist.
//
// It stays PURE — no file I/O, no container. The caller (internal/entrypoint,
// wired in a later commit) reads the two sidecars and the current surface file,
// hands their bytes here, and writes back what StatefulOutput says to write.
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
	// Its ABSENCE is the first-migration signal (§3.2): the harness seeds a
	// truthful baseline with an empty overlay and skips capture this boot.
	LastRenderPresent bool
	// LastRenderBytes is the last_render sidecar content (the surface-codec bytes
	// yolo wrote last boot). Ignored when LastRenderPresent is false. A present
	// but empty or undecodable value is treated as a first migration (§3.3): it
	// cannot be trusted as a diff baseline, so re-seed rather than capture the
	// whole file.
	LastRenderBytes []byte

	// OverlayJSON is the overlay sidecar content (JSON), or nil when absent. On a
	// first migration it is reset to {} regardless (§3.3 dangling-overlay case).
	OverlayJSON []byte
}

// StatefulOutput is the render plus the two sidecar values the caller must
// persist. The caller writes Result.Encoded to the surface path,
// LastRenderBytes to the last_render sidecar, and OverlayJSON to the overlay
// sidecar — three writes, unconditionally, every boot.
type StatefulOutput struct {
	// Result is the composed surface (compose.go Result): Config, Encoded bytes,
	// Excluded stage globs, Provenance.
	Result *Result

	// LastRenderBytes is what to write to the last_render sidecar: exactly
	// Result.Encoded (the surface-codec bytes just rendered). Provided as a
	// named field so the caller's intent reads clearly at the write site.
	LastRenderBytes []byte

	// OverlayJSON is what to write to the overlay sidecar (JSON): {} on a first
	// migration, else the accumulated overlay after this boot's capture.
	OverlayJSON []byte

	// FirstMigration reports that this boot took the §3.2 seed path (absent or
	// untrusted last_render): the render used an empty overlay and capture was
	// skipped. The caller uses this to gate the one-time §4.7 orphan-file cleanup.
	FirstMigration bool
}

// ComposeStateful runs the per-boot state machine for one surface and returns
// the render plus the sidecar values to persist. It never returns an error for
// a recoverable on-disk condition (corrupt/empty sidecar, corrupt or absent
// current file) — those self-heal by re-seeding or skipping capture, so a
// mangled home can never break the boot. It DOES return an error for a genuine
// programmer error (unknown codec, or a Compose failure such as a Lua error),
// matching Compose's fail-closed contract (§3.4).
//
// The two paths (docs/design/config-migration-to-prism.md §3.2):
//
//	first migration (last_render absent/untrusted):
//	    render  = Compose(overlay=∅)
//	    write surface_path, last_render := render; overlay := {}   # skip capture
//	steady state (last_render trusted):
//	    delta   = mergeDiff(last_render_decoded, current_decoded)
//	    overlay = mergeAccumulate(overlay, delta)                  # §3.4 tombstones
//	    render  = Compose(overlay)
//	    write surface_path, last_render := render
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
	// migration (§3.2 / §3.3): we cannot diff against it, so seeding from the
	// fresh render with an empty overlay is the only correct move — capturing the
	// on-disk file would pin stale bespoke output (§3.1).
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
				residue = dropYoloOwnedSubtrees(residue, pm)
				// Also drop anything the surface MANAGES. Managed is re-asserted after
				// the fold, so an adopted managed key can never affect the output — it
				// would only sit in the sidecar as permanent noise, and `yolo config
				// diff` would report a phantom "edit" the user cannot act on. (A stale
				// managed VALUE on disk, e.g. codex's approval_policy=on-request from
				// an old boot, is exactly this case.)
				if mg, ok := in.Base.Surface.Managed.(map[string]any); ok {
					residue = dropKeys(residue, mg)
				}
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

	// Render with the decided overlay. Compose owns decode/merge/transform/
	// enforce/encode and is the exact engine `yolo config render` uses (§6).
	base := in.Base
	base.Overlay = overlay
	res, err := Compose(base)
	if err != nil {
		return nil, err
	}

	// The overlay sidecar to persist. Marshal deterministically; on a first
	// migration this is the empty object so steady-state capture starts clean.
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

// emptyOverlay is the "no captured edits" overlay for a surface kind.
//
// For an object surface that is `{}` — an empty merge patch, which changes
// nothing. For a keyless surface it is nil, NOT the zero value: Compose treats a
// non-nil keyless layer as a real assertion that wins the fold, so an empty
// string overlay would blank the file instead of deferring to the layers below.
// dropYoloOwnedSubtrees removes from an adopted residue every key that collides
// with a CONTAINER yolo itself produces, keyed against the pure render.
//
// This is the line between "state the agent owns" and "yolo's own output from a
// previous boot", and on a first migration it is the only signal available —
// last_render, which normally disambiguates them, is exactly what is missing.
//
// Without it, adoption resurrects dropped yolo-owned entries and breaks §2
// principle 1 ("regenerate, don't reconcile"): an MCP server removed from config
// would come back, because the stale entry sits under mcp_servers, a table yolo
// computes wholesale. Verified against the real surfaces — codex's
// mcp_servers.staleServer, opencode's mcp.staleServer and mise's stale baked
// [tools] runtimes all resurrected before this.
//
// The rule: if the pure render has the key as an OBJECT, yolo owns that subtree and
// nothing inside it is adopted. If the key is absent from the pure render entirely,
// yolo knows nothing about it, so it is agent state and IS adopted — that is the
// copilot_tokens case this whole change exists for.
func dropYoloOwnedSubtrees(residue, pure map[string]any) map[string]any {
	out := make(map[string]any, len(residue))
	for k, v := range residue {
		if _, yoloOwns := pure[k].(map[string]any); yoloOwns {
			continue
		}
		out[k] = v
	}
	return out
}

// dropKeys returns m without any key present in drop.
func dropKeys(m, drop map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if _, skip := drop[k]; skip {
			continue
		}
		out[k] = v
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
