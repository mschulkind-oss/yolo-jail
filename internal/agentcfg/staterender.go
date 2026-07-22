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

	// Decide the effective overlay and whether this is a first migration.
	//
	// A last_render sidecar is TRUSTED only when it is present AND decodes to an
	// object. Absent, empty, or undecodable last_render => first migration (§3.2
	// / §3.3): we cannot diff against it, so seeding from the fresh render with an
	// empty overlay is the only correct move — capturing the on-disk file would
	// pin stale bespoke output (§3.1).
	lastRender, lastOK := decodeObject(c, in.LastRenderBytes)
	firstMigration := !in.LastRenderPresent || !lastOK

	var overlay map[string]any
	if firstMigration {
		// §3.2 seed / §3.3 dangling-overlay reset: overlay starts genuinely empty.
		// Any OverlayJSON on disk is discarded (it may be an aborted-migration
		// leftover), so nothing pre-existing leaks into the render.
		overlay = map[string]any{}
	} else {
		// Steady state. Start from the persisted overlay ({} if absent — §3.3
		// case 3), then accumulate this boot's captured delta.
		overlay = parseOverlay(in.OverlayJSON)
		if current, curOK := decodeObject(c, in.CurrentBytes); curOK {
			// §5: diff the on-disk file against the trusted baseline and fold the
			// delta into the durable overlay. mergeAccumulate preserves null
			// tombstones so a captured deletion persists (§3.4).
			delta := mergeDiff(lastRender, current)
			overlay = mergeAccumulate(overlay, delta)
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

// decodeObject decodes bytes with the surface codec and reports success only
// when the result is a non-empty-input object. Empty input, a decode error, or
// a non-object shape all report ok=false — the callers treat every one as
// "cannot trust / cannot capture", which is the conservative choice for both
// the last_render baseline and the current file.
func decodeObject(c codec.Codec, data []byte) (map[string]any, bool) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, false
	}
	decoded, err := c.Decode(data)
	if err != nil {
		return nil, false
	}
	m, ok := decoded.(map[string]any)
	if !ok {
		return nil, false
	}
	return m, true
}

// parseOverlay decodes the overlay sidecar JSON, defaulting to an empty overlay
// for absent or undecodable content (§3.3: a dangling overlay is not trusted).
// The overlay is always JSON regardless of the surface codec (see file header).
func parseOverlay(data []byte) map[string]any {
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

// marshalOverlay serializes the overlay to stable, indented JSON (sorted keys
// via encoding/json), with a nil/empty overlay rendering as `{}`. Null
// tombstones survive the round-trip — that is the whole reason the overlay is
// JSON and not the surface codec.
func marshalOverlay(overlay map[string]any) ([]byte, error) {
	if overlay == nil {
		overlay = map[string]any{}
	}
	return json.MarshalIndent(overlay, "", "  ")
}
