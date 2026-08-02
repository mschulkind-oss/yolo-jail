package entrypoint

// hostrender.go is the host-target render entry (env-manager plan Phase 4): render a
// pack's config surfaces into the invoking user's REAL home, at the `host` confinement
// notch, reusing the same boot writers via an Env pointed at that home. It is the
// implementation `yolo apply --host` calls.
//
// The resolved decisions this encodes (env-manager plan OQ-1..4, host-render-target.md
// §6.3, §6.6):
//   - PURE RMW. Every surface is read-modify-written: yolo regenerates only the keys it
//     declares (managed + dynamic tables) and leaves every key the agent wrote. No
//     whole-file compose, so no capture overlay, so no --revert (OQ-4).
//   - PROVENANCE IS STILL RECORDED. "No sidecars" covers the two CAPTURE sidecars
//     (last_render, overlay), which pure RMW genuinely has no use for. It does not cover
//     the per-key winning-layer record: a host render knows which layer won each key, and
//     for a while it wrote that down nowhere, so `yolo config diff` at the host inferred
//     the winner from declarations and could state the opposite of what happened (an
//     overlay key with no competing `managed` value reported as "managed won"). The record
//     goes under the rendered home's STATE dir, not a workspace and not the user's config
//     dir — see render.Target.ProvenanceDir. Assert only: recording a winner in observe
//     posture would document a write that never happened.
//   - NO computed layer. The live MCP/LSP tables embed jail-absolute paths, so a host
//     render passes an empty computed map — a ${workspace}-derived value has no referent
//     off-container (OQ-2/§6.6) and such a surface is refused, not bound.
//   - Config kinds only. The FieldSet census: only config surfaces are target-
//     independent; mount/reads-host/state/files are refused by name upstream (the caller
//     reports them). This entry renders the surfaces; the confinement gate is the
//     caller's.
//   - User-scoped. What is rendered is a function of the pack + user config, never of a
//     workspace — so Workspace is empty and a ${workspace} surface is skipped.

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
)

// HostRenderResult reports, per surface, what a host render did (or would do, in
// observe/dry-run mode) — enough for `apply --host` to print an honest summary.
type HostRenderResult struct {
	Surface string // "agent/name"
	Path    string // resolved real-home path
	Action  string // "rendered" | "would render" | "refused: <reason>"
	// Overwrites lists the dotted managed keys whose EXISTING value in the real file
	// differs from what this render writes — the reviewer's "always warn on overwrite"
	// for the host notch (§4.2 / env-manager plan Phase 9). Empty when the render only
	// adds keys or re-asserts identical values. Populated in both observe and assert, so
	// the dry-run preview shows the collision BEFORE anything is written (finding D2).
	// A key coming from a config-overlay is labelled with the contributing pack, since
	// the remedy there is a different pack than the surface's owner.
	Overwrites []string
	// Overlays names the packs contributing config-overlay keys to this surface, in fold
	// order (later wins). It is the host-side half of ruling R3: an override folds in
	// below the owner's managed layer, so it is invisible in the resulting file, and
	// provenance nobody can read does not make it legible. Empty for the common case.
	Overlays []string
}

// RenderHostPack renders one pack's config surfaces into homeDir (the real $HOME), pure
// RMW, no computed layer. When observe is true it computes what WOULD change and writes
// nothing (the `apply --host --dry-run` / default `observe` posture). It returns one
// result per surface and does not run hooks or touch any non-config kind.
//
// homeDir is the real home; the Env's Workspace is left empty so a ${workspace} surface
// is refused rather than bound to some arbitrary dir.
//
// overlays is the CROSS-PACK config-overlay resolution (packoverlay.Collect over every
// pack this apply is asserting). It has to be a parameter rather than derived here:
// this function sees ONE pack, and an overlay in pack B targets a surface pack A owns, so
// a per-pack derivation would find none of the overlays the kind exists to carry. Pass nil
// for a caller that has no other packs in view.
func RenderHostPack(p *packload.Pack, homeDir string, observe bool, overlays *packoverlay.OverlaySet) ([]HostRenderResult, error) {
	// hostTarget: this Env drives render.Host, not render.Jail. Load-bearing for every
	// Target-keyed path the writers resolve — without it an empty Workspace reads as the
	// container default "/workspace" (WorkspaceDir()), so a host apply would write its
	// provenance into some jail's .yolo/prism tree and read a workspace config.lua that
	// has nothing to do with this render. See Env.hostTarget.
	e := &Env{Home: homeDir, Vars: map[string]string{}, hostTarget: true}
	// Host is the autonomy-OFF notch (§4.2): render the GUARDED posture, so a pack's
	// jail-bypass permission keys do NOT reach the real home. This is the fix for the
	// apply --host bypass leak.
	surfaces, problems := p.SurfacesFor(false)
	if len(problems) > 0 {
		return nil, fmt.Errorf("pack %s: %s", p.Name, problems[0])
	}

	var out []HostRenderResult
	for _, s := range surfaces {
		id := s.Agent + "/" + s.Name
		path := expandHomePath(e, s.Path)

		if s.ResolvedMode() == manifest.ModeUnrendered {
			continue // declared but never written, at any target
		}
		if usesWorkspacePlaceholder(s) {
			out = append(out, HostRenderResult{Surface: id, Path: path,
				Action: "refused: uses ${workspace}, which has no referent on the host"})
			continue
		}
		surfaceOverlays := overlays.For(s.Agent, s.Name)
		// Compute which managed keys would OVERWRITE a differing existing value, from the
		// file as it stands now — before any write, so observe reports the same collisions
		// assert would cause.
		overwrites := managedOverwrites(e, s, path)
		// An overlay ASSERTS its keys on the host too, so a key it would clobber is the
		// same always-warn case a managed key is (§4.2). Attributed to the contributing
		// pack, because "which pack is about to overwrite my value" is the question R3
		// exists to keep answerable.
		overwrites = append(overwrites, overlayOverwrites(e, s, path, surfaceOverlays)...)
		if observe {
			out = append(out, HostRenderResult{Surface: id, Path: path, Action: "would render",
				Overwrites: overwrites, Overlays: overlayPackNames(surfaceOverlays)})
			continue
		}
		// Pure RMW into the real home, NO computed layer (host gets none — OQ-4).
		if err := renderSurfaceRMWSurface(e, s, nil, surfaceOverlays); err != nil {
			return out, fmt.Errorf("%s: %w", id, err)
		}
		out = append(out, HostRenderResult{Surface: id, Path: path, Action: "rendered",
			Overwrites: overwrites, Overlays: overlayPackNames(surfaceOverlays)})
	}
	return out, nil
}

// overlayPackNames lists the packs contributing overlays to a surface, in fold order —
// the provenance the caller prints so an override is legible in `apply --host` output
// (ruling R3) and not only in the jail's sidecar.
func overlayPackNames(overlays []agentcfg.Overlay) []string {
	if len(overlays) == 0 {
		return nil
	}
	out := make([]string, 0, len(overlays))
	for _, ov := range overlays {
		out = append(out, ov.Pack)
	}
	return out
}

// overlayOverwrites returns the dotted keys an overlay would write over a DIFFERING
// existing value, each labelled with the contributing pack. Same shape and same
// best-effort JSON basis as managedOverwrites; the label is what makes the warning
// actionable, since the remedy is dropping a different pack than the surface's owner.
func overlayOverwrites(e *Env, s manifest.Surface, path string, overlays []agentcfg.Overlay) []string {
	if len(overlays) == 0 {
		return nil
	}
	existing := loadObject(path)
	var out []string
	for _, ov := range overlays {
		layer, isMap := ov.Data.(map[string]any)
		if !isMap || len(layer) == 0 {
			continue
		}
		var keys []string
		collectOverwrites(existing, layer, "", &keys)
		sort.Strings(keys)
		for _, k := range keys {
			out = append(out, k+" (config-overlay from "+ov.Pack+")")
		}
	}
	return out
}

// managedOverwrites returns the dotted managed keys whose value in the EXISTING file at
// path differs from what this surface's managed layer will write — the host-notch
// "warn before you clobber a user value" (§4.2 / env-manager plan Phase 9, the reviewer's
// always-warn). It reads the file as it stands (loadObject, matching the RMW writer's own
// JSON round-trip) and walks the managed map; a key absent from the file is an ADD, not an
// overwrite, so it is not reported. Deterministic (sorted). Best-effort: it mirrors the
// RMW writer, which is JSON-based, so a non-JSON surface simply yields no findings.
func managedOverwrites(e *Env, s manifest.Surface, path string) []string {
	s = agentcfg.SubstituteWorkspace(s, e.WorkspaceDir())
	managed, ok := s.Managed.(map[string]any)
	if !ok || len(managed) == 0 {
		return nil
	}
	existing := loadObject(path)
	var out []string
	collectOverwrites(existing, managed, "", &out)
	sort.Strings(out)
	return out
}

// collectOverwrites walks the managed layer against the existing OrderedMap, appending a
// dotted key path for each leaf whose existing value differs from the managed value. An
// object managed value recurses (so a sibling the user owns under the same parent is not
// reported); a missing existing key is an add, not an overwrite.
func collectOverwrites(existing *jsonx.OrderedMap, managed map[string]any, prefix string, out *[]string) {
	for _, k := range sortedKeys(managed) {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		mv := managed[k]
		cur, present := existing.Get(k)
		if !present {
			continue // an ADD, not an overwrite
		}
		if sub, isMap := mv.(map[string]any); isMap {
			if curMap, ok := cur.(*jsonx.OrderedMap); ok {
				collectOverwrites(curMap, sub, key, out)
				continue
			}
			// managed wants an object where the user has a scalar/array — a real overwrite.
			*out = append(*out, key)
			continue
		}
		if !sameJSON(cur, mv) {
			*out = append(*out, key)
		}
	}
}

// sameJSON reports whether two decoded values are equal by their JSON serialization —
// codec-agnostic value equality that tolerates the OrderedMap vs map/[]any shape
// differences between a loaded file and a managed literal. Both are normalized to plain
// Go values first (jsonx.Plain), so encoding/json sorts object keys deterministically on
// both sides and the comparison is order-independent.
func sameJSON(a, b any) bool {
	ab, errA := json.Marshal(jsonx.Plain(a))
	bb, errB := json.Marshal(jsonx.Plain(b))
	if errA != nil || errB != nil {
		return false
	}
	return string(ab) == string(bb)
}

// usesWorkspacePlaceholder reports whether a surface's data references ${workspace}.
// Detected structurally: substituting two different workspace values yields different
// surfaces iff the placeholder is present. Such a surface is per-jail and refused on a
// host target (no workspace referent).
func usesWorkspacePlaceholder(s manifest.Surface) bool {
	a := agentcfg.SubstituteWorkspace(s, "\x00A")
	b := agentcfg.SubstituteWorkspace(s, "\x00B")
	return !reflect.DeepEqual(a.Managed, b.Managed) || !reflect.DeepEqual(a.Defaults, b.Defaults)
}
