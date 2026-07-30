package agentcfg

// compose.go is the exported orchestrator that stitches the pure engine
// (engine.go) to the codec, manifest, and luahook subpackages into the runnable
// pipeline of docs/plans/agent-settings-composition.md §3.1:
//
//	decode(host) ─┐
//	defaults ─────┤ deepMerge → merged → transform(Lua) → enforce(managed) → encode
//	overlay ──────┘
//
// It is the single entrypoint shared byte-for-byte by the entrypoint boot
// render and `yolo config render` (§6): "what render prints" is "what the jail
// gets". The engine stays a pure leaf; everything with a dependency (codecs,
// the Lua VM, the manifest) is injected through Inputs so this file — and its
// callers — can be tested without a container.

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
)

// Inputs is everything Compose needs to render ONE surface. The layers that
// come from outside the manifest (the host file, the workspace layer, and the
// capture-diff overlay from §5) are passed here as already-read bytes / decoded
// maps; Compose owns the decode/merge/transform/enforce/encode sequence.
type Inputs struct {
	// Surface is the manifest entry being rendered (path, codec name, defaults,
	// managed, transform-script presence). Required.
	Surface manifest.Surface

	// HostBytes is the raw content of the host file the surface mirrors (§6.5 ①),
	// or nil/empty when the host has no such file. Decoded with the surface codec.
	HostBytes []byte

	// Workspace is the optional workspace-scope layer (already decoded). Merged
	// above host, below overlay (§4). nil = absent.
	//
	// Typed `any` (like every layer here) so a non-object surface can carry its
	// whole-file value: see the "non-object surfaces" note on Compose.
	Workspace any

	// Overlays are config-overlay contributions from OTHER packs onto a surface a
	// different pack owns (docs/design/pack-system.md §3, §5). Each is a decoded object layer
	// plus the name of the contributing pack, for per-key provenance. They fold in
	// after Workspace and BELOW the capture Overlay and Computed — an overlay
	// overrides the owner's defaults (later-wins), but a user's in-jail edit
	// (capture) and yolo's freshly-regenerated computed data and the managed floor
	// all still win over it. Ordered: later entries win over earlier ones, matching
	// the "later pack wins" rule skills/bins already use. Empty = none.
	//
	// Only meaningful for object surfaces (a keyless surface has no keys to
	// overlay); a keyless surface with overlays is a caller error the boot path
	// prevents by only ever attaching overlays to object config kinds.
	Overlays []Overlay

	// Overlay is the capture-diff overlay layer (§5) that carries in-jail edits
	// across regeneration, already decoded. Merged above workspace + config
	// overlays, below the Lua transform + managed. nil = absent.
	Overlay any

	// Computed is the runtime-computed layer: yolo's per-boot DYNAMIC content that
	// is derived from live config rather than declared statically in the manifest
	// — e.g. the reconciled MCP-server table, or the LSP-plugin enable toggles and
	// ENABLE_LSP_TOOL env that depend on which LSP servers are configured. The
	// boot caller computes it and hands it in already decoded. It merges ABOVE
	// overlay and BELOW the transform + managed (§4 slot: it is yolo's freshly
	// regenerated data, so it wins over a stale in-jail edit to the same key —
	// §2 principle 1 "regenerate, don't reconcile" — but a config.lua transform
	// may still reshape it and managed still wins the floor). A null value is an
	// RFC-7386 tombstone (deletes the key), so a dynamic entry that is gone this
	// boot simply is not emitted — no sidecar memory needed. nil = absent.
	Computed any

	// Script is the concatenated config.lua source (user-then-workspace, §3.4),
	// or "" for the identity transform. VM is required iff Script is non-empty.
	Script string
	VM     luahook.LuaVM
}

// WholeFileKey is the Provenance key used for a KEYLESS surface (raw/lines):
// such a file has no top-level keys to attribute, so its single entry records
// which layer produced the whole file. Bracketed so it cannot collide with a
// real config key in an object surface's provenance map.
const WholeFileKey = "<file>"

// Result is the outcome of composing one surface.
type Result struct {
	// Config is the fully-composed decoded config (post-transform, post-enforce).
	//
	// Typed `any`: its shape is the surface codec's (map[string]any for
	// json/toml, []any for lines, string for raw). Use ConfigMap for the object
	// case.
	Config any
	// Encoded is Config serialized with the surface codec — the exact bytes yolo
	// would write to Surface.Path.
	Encoded []byte
	// Excluded is the ordered list of stage globs the transform asked to drop
	// (§3.2 ctx.stage.exclude), deduped.
	Excluded []string
	// Provenance records, per top-level config key, which layer last set it —
	// the data behind `yolo config render --explain` (§6). Keys deleted by the
	// transform (e.g. §6.5's dropped permission-gate) do not appear in Config but
	// are recorded here with layer "transform (dropped)".
	Provenance map[string]string
}

// ConfigMap returns Config as an object, or nil for a keyless surface. A
// convenience for the object-surface callers that dominate.
func (r *Result) ConfigMap() map[string]any {
	m, _ := r.Config.(map[string]any)
	return m
}

// Overlay is one config-overlay contribution: a decoded object layer and the
// name of the pack that contributed it. The Pack name is what makes an override
// legible in provenance ("key X: overlay:house-rules") rather than an anonymous
// "overlay". See Inputs.Overlays.
type Overlay struct {
	// Pack is the contributing pack's name, used only for provenance labelling.
	Pack string
	// Data is the decoded object layer (map[string]any). A nil/non-object entry
	// is skipped as absent.
	Data any
}

// Layer names used in Provenance and Explain output.
const (
	layerDefaults  = "defaults"
	layerHost      = "host"
	layerWorkspace = "workspace"
	layerOverlay   = "overlay"
	layerComputed  = "computed"
	layerTransform = "transform"
	layerManaged   = "managed"
	// layerConfigOverlay is the provenance-label PREFIX for a config-overlay
	// contribution; the full label is "config-overlay:<pack>" so a reader sees
	// which pack's overlay won a key (OQ2).
	layerConfigOverlay = "config-overlay"
)

// layerAbsent reports whether a layer says nothing and must be skipped — as
// opposed to an explicitly empty value ("" / []any{} / {}) which is a real
// assertion the layers fold honors.
//
// A plain `data == nil` is not enough: a caller passing a nil map[string]any or
// []any as the Computed/Workspace layer (common — a "no dynamic content" boot)
// boxes a TYPED nil into the `any`, which is != nil. The object fold tolerated
// that (ranging a nil map is a no-op), but the keyless fold then tried to match
// a nil map against a scalar/array codec and failed with a spurious "layer is
// not string" — the exact break host_files' raw/lines surfaces hit as the first
// keyless callers with a nil computed layer. reflect.Value.IsNil catches the
// typed-nil map/slice/pointer case that == nil misses.
func layerAbsent(data any) bool {
	if data == nil {
		return true
	}
	switch v := reflect.ValueOf(data); v.Kind() {
	case reflect.Map, reflect.Slice, reflect.Ptr, reflect.Interface:
		return v.IsNil()
	default:
		return false
	}
}

// Compose runs the full §3.1 pipeline for one surface and returns the rendered
// config, its encoded bytes, the stage excludes, and per-key provenance. It is
// pure: no file I/O, no container — the caller supplies bytes and decoded
// layers via in, and Compose returns bytes. Errors are loud and fail-closed
// (§3.4): a decode failure, a missing VM for a non-empty script, or a Lua error
// aborts the render rather than shipping a partial file.
func Compose(in Inputs) (*Result, error) {
	c, err := codec.LookupCodec(in.Surface.Codec)
	if !err {
		return nil, fmt.Errorf("agentcfg: surface %s/%s: unknown codec %q", in.Surface.Agent, in.Surface.Name, in.Surface.Codec)
	}

	kind := in.Surface.Kind()

	// Decode the host layer (the one layer that arrives as raw bytes). An
	// empty/absent host file is an empty layer, not an error.
	var host any
	if len(in.HostBytes) > 0 {
		decoded, derr := c.Decode(in.HostBytes)
		if derr != nil {
			return nil, fmt.Errorf("agentcfg: surface %s/%s: decode host bytes: %w", in.Surface.Agent, in.Surface.Name, derr)
		}
		// The decoded shape must match what the codec promises (codec.Kind), not
		// merely "be an object". A json surface whose host file holds a top-level
		// ARRAY is still an error — deep-merging it would silently discard it —
		// but a raw surface's string is now perfectly ordinary input.
		if !kind.Matches(decoded) {
			return nil, fmt.Errorf("agentcfg: surface %s/%s: host config is not %s (got %T)",
				in.Surface.Agent, in.Surface.Name, kind, decoded)
		}
		host = decoded
	}

	// Track provenance by folding the same ascending-precedence layer list the
	// engine folds, recording which named layer last touched each top-level key.
	prov := map[string]string{}
	preLayers := []struct {
		name string
		data any
	}{
		{layerDefaults, in.Surface.Defaults},
		{layerHost, host},
		{layerWorkspace, in.Workspace},
	}
	// config-overlay contributions fold in after workspace, in order (later pack
	// wins), each labelled with its pack so an override is attributable (OQ2).
	for _, ov := range in.Overlays {
		preLayers = append(preLayers, struct {
			name string
			data any
		}{layerConfigOverlay + ":" + ov.Pack, ov.Data})
	}
	preLayers = append(preLayers,
		struct {
			name string
			data any
		}{layerOverlay, in.Overlay},
		struct {
			name string
			data any
		}{layerComputed, in.Computed},
	)

	var merged any
	if kind == codec.KindObject {
		// Object surfaces: the §3.1 deep-merge fold, with per-key provenance.
		orderedLayers := make([]map[string]any, 0, len(preLayers))
		for _, l := range preLayers {
			if layerAbsent(l.data) {
				continue
			}
			m, ok := l.data.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("agentcfg: surface %s/%s: %s layer is not an object (got %T)",
					in.Surface.Agent, in.Surface.Name, l.name, l.data)
			}
			orderedLayers = append(orderedLayers, m)
			for k := range m {
				// A null tombstone in a layer deletes the key; reflect that in
				// provenance so --explain doesn't claim a deleted key is present.
				if m[k] == nil {
					delete(prov, k)
				} else {
					prov[k] = l.name
				}
			}
		}
		merged = render(orderedLayers...)
	} else {
		// KEYLESS surfaces (raw -> string, lines -> []any): whole-value
		// replacement in the same ascending order. There is no deep-merge to do
		// and no per-key attribution to make — a file with no keys has exactly
		// one "key", itself — so the highest present layer simply wins, and
		// provenance records that single winner under WholeFileKey.
		//
		// A layer is "present" iff non-nil. That is what makes an absent layer
		// skip rather than blank the file: `nil` means "this layer says nothing",
		// while an explicitly EMPTY value ("" or []any{}) is a real assertion
		// that the file is empty and does win. Conflating the two would let a
		// surface with no workspace layer erase its own host content.
		for _, l := range preLayers {
			if layerAbsent(l.data) {
				continue
			}
			if !kind.Matches(l.data) {
				return nil, fmt.Errorf("agentcfg: surface %s/%s: %s layer is not %s (got %T)",
					in.Surface.Agent, in.Surface.Name, l.name, kind, l.data)
			}
			merged = l.data
			prov[WholeFileKey] = l.name
		}
		if merged == nil {
			merged = kind.ZeroValue()
		}
	}

	// Snapshot the pre-transform values so we can attribute transform edits —
	// not just added/dropped keys but also keys whose value the transform
	// changed (e.g. §6.5's extensions array, present before and after).
	preValues := map[string]any{}
	if mm, ok := merged.(map[string]any); ok {
		for k, v := range mm {
			preValues[k] = v
		}
	}
	preWhole := merged

	// Transform step (§3.1): run the Lua hook (or identity when Script == "").
	// The hook sees the surface's own shape and must return that same shape;
	// luahook enforces it against Kind (a raw transform returns a string).
	ctx := luahook.NewCtxKind(in.Surface.Agent, in.Surface.Name, kind, merged, in.Surface.Managed)
	transformed, terr := luahook.Apply(luahook.Transform{VM: in.VM, Script: in.Script}, ctx)
	if terr != nil {
		return nil, terr // already wrapped fail-closed by Apply
	}

	// Attribute transform edits: any key the transform added, changed, or
	// dropped is recorded against the transform layer. For a keyless surface the
	// only attribution possible is "the transform changed the file".
	if in.Script != "" {
		if tm, ok := transformed.(map[string]any); ok {
			for k, nv := range tm {
				ov, existed := preValues[k]
				if !existed || !reflect.DeepEqual(ov, nv) {
					prov[k] = layerTransform
				}
			}
			for k := range preValues {
				if _, still := tm[k]; !still {
					prov[k] = layerTransform + " (dropped)"
				}
			}
		} else if !reflect.DeepEqual(preWhole, transformed) {
			prov[WholeFileKey] = layerTransform
		}
	}

	// Enforce step (§3.1): re-apply the managed layer AFTER the hook, so managed
	// keys win regardless of what the transform did. On a keyless surface a
	// non-nil managed layer replaces the whole value (see Ctx.Enforce).
	ctx.Config = transformed
	ctx.Enforce()
	if mm := in.Surface.ManagedMap(); mm != nil {
		for k := range mm {
			if mm[k] == nil {
				continue
			}
			prov[k] = layerManaged
		}
	} else if in.Surface.Managed != nil {
		prov[WholeFileKey] = layerManaged
	}

	encoded, eerr := c.Encode(ctx.Config)
	if eerr != nil {
		return nil, fmt.Errorf("agentcfg: surface %s/%s: encode: %w", in.Surface.Agent, in.Surface.Name, eerr)
	}

	return &Result{
		Config:     ctx.Config,
		Encoded:    encoded,
		Excluded:   dedupeStable(ctx.Stage.Excluded()),
		Provenance: prov,
	}, nil
}

// dedupeStable returns globs with duplicates removed, preserving first-seen
// order (the engine's job per luahook.Stage.Exclude's contract).
func dedupeStable(globs []string) []string {
	if len(globs) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(globs))
	out := make([]string, 0, len(globs))
	for _, g := range globs {
		if seen[g] {
			continue
		}
		seen[g] = true
		out = append(out, g)
	}
	return out
}

// ProvenanceLines renders Provenance as sorted "key\tlayer" lines for the
// --explain output (§6). Sorted so the output is deterministic and diffable.
func (r *Result) ProvenanceLines() []string {
	keys := make([]string, 0, len(r.Provenance))
	for k := range r.Provenance {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("%s\t%s", k, r.Provenance[k]))
	}
	return lines
}
