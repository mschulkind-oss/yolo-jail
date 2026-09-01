package entrypoint

// hostoverlayprune.go retires the `config-overlay` KEYS of a pack that has been DROPPED from
// config — ruling R3's first sentence in docs/plans/host-pack-drop-cleanup.md.
//
// It is a separate entry from RenderHostPack for the same structural reason
// PruneHostBriefings is separate from RenderHostBriefing: `yolo host apply`'s render loop is
// `for _, p := range loaded`, and a pack removed from `packs` is not in `loaded`, so nothing
// ever asks what it left behind. The overlay keys it asserted therefore stayed in the user's
// config file forever.
//
// THE PROVENANCE RECORD IS THE AUTHORITY, and it has to be. An overlay folds in BELOW the
// owning surface's managed layer, so it leaves no trace in the resulting file: no marker, no
// delimited block, nothing in the bytes that says "a pack put this here". The only place the
// fact was ever written down is the per-key record
// (<home>/.local/share/yolo-jail/host-provenance/<agent>-<name>.provenance), where a
// contributed key reads `config-overlay:<pack>` — or `retired:config-overlay:<pack>` once the
// anti-laundering pass has carried the attribution forward past the drop (see
// agentcfg.RetiredLayer). Both labels are accepted here, and both are needed: the pre-render
// record on an observe run still says the first, and the post-render record on a run after
// the drop says the second.
//
// Four deliberate conservatisms, each of which leaves a key rather than removing one:
//
//   - A NIL active SET IS REFUSED. Reading it as "nothing is active" would strip every
//     pack-contributed key in the user's home on a caller bug. PruneHostBriefings takes the
//     same posture for the same reason; an empty non-nil map is the honest "no packs".
//   - `host` IS NEVER TOUCHED. A key the user set themselves — including one whose NAME a
//     pack also happens to use — is recorded `host`, and only a config-overlay attribution is
//     eligible. That asymmetry is the whole safety property: this pass can only remove what
//     yolo has a record of having written for a pack.
//   - A KEY A LIVE LAYER STILL CLAIMS IS NOT AN ORPHAN, whatever the record says. The record
//     holds one winner per key, so two packs contributing the same key leave only the last
//     one named — and if that one was dropped while the other stayed, the record alone would
//     call a live key an orphan. The live layers are consulted directly (see liveClaims), so
//     the record's coarseness cannot cost the user a key that is still being asserted.
//   - AN UNPARSEABLE FILE YIELDS NOTHING. The decode is best-effort (the
//     existingSurfaceObject posture): a file yolo cannot read has no keys to find, so nothing
//     is removed and nothing is written. The render loop refuses that same file loudly, so
//     the user gets one message about it rather than two.
//
// What this pass does NOT reach, on purpose:
//
//   - A DYNAMIC MANAGED TABLE (`mcpServers`). An overlay contributing table entries is folded
//     into the wholesale table layer, so its key is attributed `computed` rather than to the
//     pack — and it needs no prune anyway: dropping the contributor leaves hostTableLayer
//     empty and regenerateManagedTables CLEARS the block on the very next apply. The existing
//     wholesale mechanism already fixes that case, and a second remover would be racing it.
//   - `retired:managed` / `retired:computed`. Those are the OWNER pack's own keys, which is
//     the other axis (a dropped owner, or an owner that stopped declaring a key) and a
//     different ruling. R3's first sentence is about overlay keys.
//
// Not archived, unlike a retired skill or file (R2). There is nothing to archive: a key is a
// value in a file, not content, and the value is a pack's own assertion — putting the pack
// back in `packs` and re-applying restores it exactly. What the caller owes the user is the
// CONFIRMATION (R3: "it is IN a file the user owns, so it rides the same gate"), which is why
// this function reports in observe posture and the CLI gates the write on that report.

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// HostOverlayOrphan is one config-overlay key in a rendered host surface whose contributing
// pack is no longer configured — enough to report it, gate on it, and act on it.
type HostOverlayOrphan struct {
	// Surface is the identity the key lives in, "agent/name".
	Surface string
	// Path is the resolved real-home file the key is in.
	Path string
	// Key is the TOP-LEVEL key, matching the provenance record's granularity (see
	// rmwProvenance: a layer that sets a nested key claims the whole top-level key).
	Key string
	// Pack is the pack that asserted it and is no longer in `packs` — the actionable half of
	// the report, and the thing nothing else in the user's home remembers.
	Pack string
	// Action is "removed" or "would remove" (observe posture).
	Action string
}

// PruneHostOverlayKeys removes the config-overlay keys of packs that have left `active`, from
// every surface the candidate packs declare, and returns one entry per key.
//
// candidates supplies the surfaces to look at — the union of the packs this apply resolved and
// the packs yolo SHIPS, exactly as PruneHostBriefings takes them. Both halves matter: the
// overlay's key lives in a surface the OWNER declares, and the owner may itself have been
// dropped, in which case only the embedded set still knows where the file is.
//
// active names the packs whose keys are legitimate. Pass the CONFIGURED set, not the
// resolvable one: a pack that is still in `packs` but could not be loaded this launch (a git
// remote that is offline) must keep its keys, the same threshold pruneDroppedPackOutput uses.
//
// overlays is this apply's live cross-pack resolution, consulted so a key some OTHER selected
// pack still contributes is never treated as an orphan (see liveClaims).
//
// When observe is true it reports what it WOULD remove and writes nothing — so the caller can
// put the report in front of a confirmation prompt, and a dry-run `yolo host apply` is an honest
// preview. Removing a key also drops it from the provenance record, so the record stops naming
// a key that is no longer in the file.
func PruneHostOverlayKeys(candidates []*packload.Pack, active map[string]bool,
	overlays *packoverlay.OverlaySet, homeDir string, observe bool) ([]HostOverlayOrphan, error) {
	if active == nil {
		return nil, fmt.Errorf("host config-overlay prune: refusing to prune with an " +
			"unknown active pack set")
	}
	// hostTarget: the same projection RenderHostPack uses, so the provenance path and the
	// surface path resolve against THIS home rather than a jail's tree. See Env.hostTarget.
	e := &Env{Home: homeDir, Vars: map[string]string{}, hostTarget: true}

	var out []HostOverlayOrphan
	for _, s := range hostOverlaySurfaces(candidates, e.renderTarget().Profile()) {
		recPath := prismProvenancePath(e, s.Agent, s.Name)
		if recPath == "" {
			continue
		}
		data, err := os.ReadFile(recPath)
		if err != nil {
			// No record: yolo has never asserted this surface in this home, so it has nothing
			// here to retire. Absent-means-never-rendered is the same reading
			// hostProvenanceExists depends on.
			continue
		}
		surfaceOverlays := overlays.For(s.Agent, s.Name)
		orphaned := orphanedOverlayKeys(agentcfg.ParseProvenanceRecord(data), active,
			liveClaims(s, surfaceOverlays))
		if len(orphaned) == 0 {
			continue
		}
		path := expandHomePath(e, s.Path)
		orig, obj, before, derr := readRMWSource(s, path)
		if derr != nil {
			// Best-effort by design — see the file comment. A file yolo cannot read yields no
			// orphans and no write, and the render loop reports the refusal.
			continue
		}
		id := s.Agent + "/" + s.Name
		action := "removed"
		if observe {
			action = "would remove"
		}
		var removed []string
		for _, k := range orphaned {
			if _, present := obj.Get(k.key); !present {
				// The record names a key the file no longer has (the user deleted it, a
				// tombstone dropped it). Nothing to remove, so nothing to report: a phantom
				// removal line is the mistake removeHostBriefingBlockAt avoids by returning
				// nil for a block that was not there.
				continue
			}
			removed = append(removed, k.key)
			out = append(out, HostOverlayOrphan{Surface: id, Path: path, Key: k.key,
				Pack: k.pack, Action: action})
		}
		if observe || len(removed) == 0 {
			continue
		}
		for _, k := range removed {
			obj.Delete(k)
		}
		// orig/before carry the file's comments into the re-emit: a prune removes named keys
		// and must leave the rest — including the prose around it — exactly as it found it.
		text, eerr := encodeSurfaceObject(s, obj, orig, before)
		if eerr != nil {
			return out, fmt.Errorf("%s: %w", id, eerr)
		}
		if werr := writeInPlaceString(path, text); werr != nil {
			return out, fmt.Errorf("%s: %w", id, werr)
		}
		// Keep the record honest about the file it describes: a key that is gone must stop
		// being attributed, or the NEXT prune reads an orphan that no longer exists. Written
		// even when it empties the record — "rendered here, nothing attributed" is a
		// measurement, and collapsing it to absent would read as "never rendered".
		record := agentcfg.ParseProvenanceRecord(data)
		for _, k := range removed {
			delete(record, k)
		}
		writeProvenanceRecord(e, s.Agent, s.Name, record)
	}
	return out, nil
}

// orphanedKey is one provenance entry eligible for retirement: the key and the pack that
// asserted it.
type orphanedKey struct {
	key  string
	pack string
}

// orphanedOverlayKeys selects the record's config-overlay entries whose pack is not in active
// and which no live layer claims, sorted by key so a prune is deterministic.
func orphanedOverlayKeys(record map[string]string, active, live map[string]bool) []orphanedKey {
	var out []orphanedKey
	for key, layer := range record {
		pack, isOverlay := overlayPackOf(layer)
		if !isOverlay || pack == "" || active[pack] || live[key] {
			continue
		}
		out = append(out, orphanedKey{key: key, pack: pack})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out
}

// overlayPackOf reports the pack named by a config-overlay provenance label, accepting both
// the live form (`config-overlay:<pack>`) and the retired one
// (`retired:config-overlay:<pack>`, agentcfg.RetiredLayer).
//
// BOTH are required, and which one is on disk depends only on when the record was last
// written: a run that has not re-rendered yet (observe) still sees the live label, and a run
// after a rendering apply sees the retired one, because the anti-laundering pass carries the
// attribution forward rather than letting it decay to `host`. Treating only one as eligible
// would make the prune work in exactly one of the two postures.
//
// Built from agentcfg's own constructions (OverlayLayer, RetiredOf) rather than string
// literals, so the writer and this reader cannot drift on the vocabulary. Every other layer
// name returns false, which is what keeps `host` — the user's own keys — permanently out of
// reach of this pass, along with `retired:managed`/`retired:computed`, which belong to the
// surface's owner rather than to any overlay.
func overlayPackOf(layer string) (string, bool) {
	if last, retired := agentcfg.RetiredOf(layer); retired {
		layer = last
	}
	prefix := agentcfg.OverlayLayer("")
	if !strings.HasPrefix(layer, prefix) {
		return "", false
	}
	return layer[len(prefix):], true
}

// liveClaims is the set of TOP-LEVEL keys some layer in THIS apply still asserts on a surface:
// the owner's managed layer plus every overlay a selected pack contributes.
//
// It is the cross-check that keeps the record's coarseness from costing a live key. The record
// holds ONE winner per key, so when two packs contribute the same key only the last is named —
// drop that one and the record says `config-overlay:<dropped>` for a key the surviving pack is
// still setting. Reading the record alone would remove it, and the next apply would put it
// straight back: churn at best, and at worst a removal the user sees and cannot explain.
//
// Defaults are deliberately NOT included. A default is fill-if-absent, so it does not assert a
// key that is already there; a key whose only live claim is a default is still the user's (the
// same reason agentcfg.LayerAsserted excludes `defaults`).
func liveClaims(s manifest.Surface, overlays []agentcfg.Overlay) map[string]bool {
	out := map[string]bool{}
	if managed, isMap := s.Managed.(map[string]any); isMap {
		for k, v := range managed {
			if v == nil {
				continue // an RFC-7386 tombstone asserts no value
			}
			out[k] = true
		}
	}
	for _, ov := range overlays {
		layer, isMap := ov.Data.(map[string]any)
		if !isMap {
			continue
		}
		for k := range layer {
			out[k] = true
		}
	}
	return out
}

// hostOverlaySurfaces is the deduplicated set of host-renderable surfaces the candidate packs
// declare — the files a prune pass must look at, and the only ones it can act on.
//
// Deduped by identity with the LAST declaration winning, matching manifest.Merge, so the
// surface inspected is the one a render would have used.
//
// prof is the target's confinement profile, and its AgentAutonomy bit has to be the CALLER's
// rather than a literal here — not merely for tidiness but because this pass reads a record
// the render wrote: the surface it inspects must be the posture that is actually on disk. At
// the host notch that is the guarded one (§4.2). A mismatch between the two would look for
// keys in a surface no render ever produced.
//
// Two exclusions, both meaning "no key-level RMW is possible here", i.e. there is nothing this
// pass could remove even in principle:
//
//   - ModeUnrendered: yolo does not write the file at all, so it holds no asserted key.
//   - a non-object codec (`lines`, `raw`): the whole file is the one "key", so removing a key
//     would mean replacing the file — see rmwCodecRefusal.
//
// A ${workspace}-bearing PATH is excluded too. A host render has no workspace referent, so
// such a surface never rendered here; joining the literal placeholder into a real path would
// point this pass at a file nobody wrote.
func hostOverlaySurfaces(candidates []*packload.Pack, prof render.Profile) []manifest.Surface {
	byKey := map[manifest.SurfaceKey]manifest.Surface{}
	var order []manifest.SurfaceKey
	for _, p := range candidates {
		if p == nil {
			continue
		}
		// No profile table, matching the render this pass retires keys FOR: RenderHostPack
		// selects no variant, so a variant's keys were never written here and must not be
		// looked for in a surface no render ever produced.
		surfaces, _ := p.SurfacesFor(prof.AgentAutonomy, nil)
		for _, s := range surfaces {
			if s.ResolvedMode() == manifest.ModeUnrendered {
				continue
			}
			if rmwCodecRefusal(s) != nil {
				continue
			}
			if strings.Contains(s.Path, agentcfg.WorkspacePlaceholder) {
				continue
			}
			if _, seen := byKey[s.Key()]; !seen {
				order = append(order, s.Key())
			}
			byKey[s.Key()] = s
		}
	}
	out := make([]manifest.Surface, 0, len(order))
	for _, k := range order {
		out = append(out, byKey[k])
	}
	return out
}
