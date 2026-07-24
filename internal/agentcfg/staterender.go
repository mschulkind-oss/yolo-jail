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
// The two sidecars (§5), which the caller stores in `<workspace>/.yolo/`:
//
//   - last_render: the exact surface-codec bytes yolo wrote last boot. Stored
//     in the surface's own codec (not JSON) so it byte-matches what was written
//     and diffs cleanly against the on-disk file — "the bytes yolo wrote last
//     boot" per §5.
//   - overlay: the accumulated in-jail edits, ALWAYS JSON. The overlay must be
//     able to carry `null` tombstones (a captured deletion), which TOML/lines
//     codecs cannot express; JSON is the one codec that round-trips the generic
//     value model including nulls. It is an engine-internal sidecar the agent
//     never sees (§5), so its on-disk format is yolo's choice, not the surface's.

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
		// §3.2 seed / §3.3 dangling-overlay reset: overlay starts genuinely empty.
		// Any OverlayJSON on disk is discarded (it may be an aborted-migration
		// leftover), so nothing pre-existing leaks into the render.
		//
		// "Empty" is nil for a keyless surface, NOT the zero value: an empty
		// string is a real assertion that the file is empty and would win the
		// fold, blanking the render. nil means "this layer says nothing".
		overlay = emptyOverlay(kind)
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
