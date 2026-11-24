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
		set.byTarget[key] = append(set.byTarget[key], agentcfg.Overlay{Pack: p.Name, Data: data})
	}
}

func instructionsOrphan(p *packload.Pack, surface string, key manifest.SurfaceKey, autonomy bool) OrphanOverlay {
	_, coreOwned := agentcfg.BuiltinManifest().Lookup(key.Agent, key.Name)
	return OrphanOverlay{Kind: packdecl.KindInstructions, Pack: p.Name, Target: surface,
		Owner: shippedOwnerOf(key, autonomy), CoreOwned: coreOwned}
}
