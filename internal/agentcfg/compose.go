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
	Workspace map[string]any

	// Overlay is the capture-diff overlay layer (§5) that carries in-jail edits
	// across regeneration, already decoded. Merged above workspace, below the Lua
	// transform + managed. nil = absent.
	Overlay map[string]any

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
	Computed map[string]any

	// Script is the concatenated config.lua source (user-then-workspace, §3.4),
	// or "" for the identity transform. VM is required iff Script is non-empty.
	Script string
	VM     luahook.LuaVM
}

// Result is the outcome of composing one surface.
type Result struct {
	// Config is the fully-composed decoded config (post-transform, post-enforce).
	Config map[string]any
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

// Layer names used in Provenance and Explain output.
const (
	layerDefaults  = "defaults"
	layerHost      = "host"
	layerWorkspace = "workspace"
	layerOverlay   = "overlay"
	layerComputed  = "computed"
	layerTransform = "transform"
	layerManaged   = "managed"
)

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

	// Decode the host layer (the one layer that arrives as raw bytes). An
	// empty/absent host file is an empty layer, not an error.
	var host map[string]any
	if len(in.HostBytes) > 0 {
		decoded, derr := c.Decode(in.HostBytes)
		if derr != nil {
			return nil, fmt.Errorf("agentcfg: surface %s/%s: decode host bytes: %w", in.Surface.Agent, in.Surface.Name, derr)
		}
		m, ok := decoded.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("agentcfg: surface %s/%s: host config is not an object (got %T)", in.Surface.Agent, in.Surface.Name, decoded)
		}
		host = m
	}

	// Track provenance by folding the same ascending-precedence layer list the
	// engine folds, recording which named layer last touched each top-level key.
	prov := map[string]string{}
	preLayers := []struct {
		name string
		data map[string]any
	}{
		{layerDefaults, in.Surface.Defaults},
		{layerHost, host},
		{layerWorkspace, in.Workspace},
		{layerOverlay, in.Overlay},
		{layerComputed, in.Computed},
	}
	orderedLayers := make([]map[string]any, 0, len(preLayers))
	for _, l := range preLayers {
		if l.data == nil {
			continue
		}
		orderedLayers = append(orderedLayers, l.data)
		for k := range l.data {
			// A null tombstone in a layer deletes the key; reflect that in
			// provenance so --explain doesn't claim a deleted key is present.
			if l.data[k] == nil {
				delete(prov, k)
			} else {
				prov[k] = l.name
			}
		}
	}

	// Fold defaults<host<workspace<overlay via the pure engine (§3.1 deepMerge).
	merged := render(orderedLayers...)

	// Snapshot the pre-transform values so we can attribute transform edits —
	// not just added/dropped keys but also keys whose value the transform
	// changed (e.g. §6.5's extensions array, present before and after).
	preValues := make(map[string]any, len(merged))
	for k, v := range merged {
		preValues[k] = v
	}

	// Transform step (§3.1): run the Lua hook (or identity when Script == "").
	ctx := luahook.NewCtx(in.Surface.Agent, in.Surface.Name, merged, in.Surface.Managed)
	transformed, terr := luahook.Apply(luahook.Transform{VM: in.VM, Script: in.Script}, ctx)
	if terr != nil {
		return nil, terr // already wrapped fail-closed by Apply
	}

	// Attribute transform edits: any key the transform added, changed, or
	// dropped is recorded against the transform layer.
	if in.Script != "" {
		for k, nv := range transformed {
			ov, existed := preValues[k]
			if !existed || !reflect.DeepEqual(ov, nv) {
				prov[k] = layerTransform
			}
		}
		for k := range preValues {
			if _, still := transformed[k]; !still {
				prov[k] = layerTransform + " (dropped)"
			}
		}
	}

	// Enforce step (§3.1): re-apply the managed layer AFTER the hook, so managed
	// keys win regardless of what the transform did.
	ctx.Config = transformed
	ctx.Enforce()
	for k := range in.Surface.Managed {
		if in.Surface.Managed[k] == nil {
			continue
		}
		prov[k] = layerManaged
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
