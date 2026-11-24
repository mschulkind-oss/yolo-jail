package packoverlay

// instructions.go places the `instructions` kind's two config mechanisms (packdecl.Instructions):
// a LIST appended to an array of a surface, and a SET folded into a surface's managed layer. Its
// third mechanism, launch flags, is not a config fold and is read by packload.LaunchFlagsFor.
//
// Both go through the folds that already exist (a config-list and a config-overlay), so the user's
// own entries survive (IF-D9), the per-entry capture and the host record work as they do for any
// list, and no engine code learns what an instruction file is.

import (
	"encoding/json"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// placeInstructions places one pack's `instructions` mechanism for the selection the pack carries.
// An ownerless target is inert and reported like every other ownerless contribution (R2); a
// malformed body is a Problem, loud as a malformed overlay is.
func placeInstructions(set *OverlaySet, p *packload.Pack, decl packdecl.Instructions,
	owners map[manifest.SurfaceKey]string, autonomy bool) {
	if surface, path, add, ok := decl.ListFor(p.Instructions); ok {
		key, err := manifest.ParseSurfaceID(surface)
		if err != nil {
			set.Problems = append(set.Problems, "pack "+p.Name+": instructions: "+err.Error())
			return
		}
		body, err := json.Marshal(add)
		if err != nil {
			set.Problems = append(set.Problems, "pack "+p.Name+": instructions: "+err.Error())
			return
		}
		list, err := agentcfg.NewListContribution(p.Name, path, body)
		if err != nil {
			set.Problems = append(set.Problems, "pack "+p.Name+": instructions on "+surface+": "+err.Error())
			return
		}
		if _, owned := owners[key]; !owned {
			set.Orphans = append(set.Orphans, instructionsOrphan(p, surface, key, autonomy))
			return
		}
		set.listsByTarget[key] = append(set.listsByTarget[key], list)
	}
	if surface, body, ok := decl.SetFor(p.Instructions); ok {
		key, err := manifest.ParseSurfaceID(surface)
		if err != nil {
			set.Problems = append(set.Problems, "pack "+p.Name+": instructions: "+err.Error())
			return
		}
		data, probs := manifest.DecodeOverlay(surface, body)
		if len(probs) > 0 {
			for _, prob := range probs {
				set.Problems = append(set.Problems, "pack "+p.Name+": instructions: "+prob)
			}
			return
		}
		if _, owned := owners[key]; !owned {
			set.Orphans = append(set.Orphans, instructionsOrphan(p, surface, key, autonomy))
			return
		}
		set.byTarget[key] = append(set.byTarget[key], agentcfg.Overlay{Pack: p.Name, Data: integralAsInt(data)})
	}
}

// integralAsInt turns a whole number a JSON round trip made a float64 back into an int64. The
// manifest's body travels as JSON, which has one number type, and the TOML codec writes a float64
// as `0.0`, which an agent that reads an integer setting (codex's `project_doc_max_bytes`) refuses.
// A TOML file decodes a whole number as int64, so this is the value the surface already holds.
func integralAsInt(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = integralAsInt(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = integralAsInt(e)
		}
		return out
	case float64:
		if x == float64(int64(x)) {
			return int64(x)
		}
	}
	return v
}

func instructionsOrphan(p *packload.Pack, surface string, key manifest.SurfaceKey, autonomy bool) OrphanOverlay {
	_, coreOwned := agentcfg.BuiltinManifest().Lookup(key.Agent, key.Name)
	return OrphanOverlay{Kind: packdecl.KindInstructions, Pack: p.Name, Target: surface,
		Owner: shippedOwnerOf(key, autonomy), CoreOwned: coreOwned}
}
