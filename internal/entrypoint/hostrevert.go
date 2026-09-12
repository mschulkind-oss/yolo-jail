package entrypoint

// hostrevert.go is `yolo host apply --revert`: remove from the user's real home every key
// yolo ASSERTED there, on the authority of the provenance record, and delete the record
// (docs/design/config-ownership-and-promotion.md §10 step 3).
//
// It reverses env-manager plan OQ-1 ("is there a `--revert` verb on the host target? →
// RESOLVED: NO", 2026-08-01), deliberately and on grounds that ruling did not have: it named
// the MISSING MEMORY as the blocker, and the memory now exists. The reversal row is in that
// plan's own ledger, under "Blocks Phase 4 (host render)".
//
// # It is PruneHostOverlayKeys' walk, with a wider eligible-layer set
//
// That pass already removes keys from a real file on this record's authority — *"THE
// PROVENANCE RECORD IS THE AUTHORITY, and it has to be"* (hostoverlayprune.go). Everything
// structural is shared with it: the surfaces come from the same hostOverlaySurfaces, the
// record from the same prismProvenancePath, the read/encode/write from the same RMW codec.
// Two things differ, and they are the whole of this file:
//
//   - WHICH ATTRIBUTIONS ARE ELIGIBLE. The prune takes `config-overlay:<pack>` only, and only
//     for a pack that has left `packs`. A revert takes every layer yolo WROTE, whatever pack
//     is still configured, because the user is not dropping a pack — they are withdrawing
//     yolo from the file.
//   - THE RECORD IS DELETED, not edited. A prune keeps the record honest about a file it
//     still describes; a revert ends the relationship, and an absent record is how this tree
//     spells "yolo has never rendered here" (hostProvenanceExists → FirstApply). So after a
//     revert the next apply treats the home as untouched and every guard keyed on FirstApply
//     is armed again, which is exactly right: it IS a first apply again.
//
// # `host` IS NEVER TOUCHED, and that asymmetry is the safety property
//
// A key the user set themselves — including one whose NAME a pack also uses — is recorded
// `host`, and no `host` attribution is eligible here any more than in the prune. This pass
// can only remove what yolo has a record of having written.
//
// # WHAT IT DOES NOT DO: restore
//
// Revert REMOVES yolo's keys; it does not bring back what a key held before yolo first wrote
// it, because nothing snapshots that (the migration guide has said so since before this verb
// existed). The consequence to state plainly, because it is the one way a revert can cost
// something: a key the user EDITED since the last apply still carries that apply's
// attribution, so a value they changed by hand into a slot yolo owns is deleted rather than
// preserved. The containment is the posture — a revert is a DRY RUN by default and lists
// every key with the attribution it would act on, so the user sees each one before anything
// is written.

import (
	"fmt"
	"os"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// HostRevertedKey is one key a revert removed, or would remove.
type HostRevertedKey struct {
	// Surface is the identity the key lives in, "agent/name".
	Surface string
	// Path is the resolved real-home file the key is in.
	Path string
	// Key is the TOP-LEVEL key, matching the record's granularity (a layer that sets a
	// nested key claims the whole top-level key — see rmwProvenance).
	Key string
	// Layer is the attribution the removal rests on, verbatim from the record
	// (`managed`, `computed`, `defaults`, `config-overlay:<pack>`, or a `retired:` form).
	// REPORTED, not just used: the user is owed the authority for each removal, and a
	// `defaults` line means something different to them than a `managed` one.
	Layer string
	// Action is "removed" or "would remove" (observe posture).
	Action string
}

// HostRevert is one revert's whole result: the keys, and the provenance records that end the
// relationship. Records is carried separately because it is not derivable from Keys — a
// surface yolo rendered but whose keys the user has since deleted has a record and no keys,
// and the record still has to go or the home keeps claiming a render that owns nothing.
type HostRevert struct {
	Keys    []HostRevertedKey
	Records []string
}

// RevertHostRender withdraws yolo from every host surface it has a provenance record for.
//
// candidates supplies the surfaces to look at — the union of the packs this invocation
// resolved and the packs yolo SHIPS, exactly as PruneHostOverlayKeys takes them, and for the
// same reason: the record lives beside a surface the OWNER declares, and the owner may have
// been dropped from `packs` since, in which case only the embedded set still knows where the
// file is. A revert is the case where that matters most — a user withdrawing yolo has every
// reason to have already emptied `packs`.
//
// When observe is true it reports what it WOULD do and writes nothing, which is the default
// posture of the verb.
//
// Four conservatisms, inherited from the prune because they are the same walk, each of which
// leaves a key rather than removing one: a record that cannot be read yields nothing (the
// surface was never rendered here); a file that cannot be decoded is left entirely alone,
// record included; a key the record names but the file no longer has is not reported (a
// phantom removal line is worse than silence); and only attributions yolo can prove it wrote
// are eligible.
func RevertHostRender(candidates []*packload.Pack, homeDir string, observe bool) (HostRevert, error) {
	// hostTarget: the same projection RenderHostPack and PruneHostOverlayKeys use, so the
	// record this reads is the one the render WROTE. prismProvenancePath resolves through
	// render.Host(...).ProvenancePath — one definition, shared by the writer and this reader,
	// rather than a second path derivation free to drift.
	//
	// NO `host_management` CONTRACT, deliberately: nothing on this walk asks the mode census,
	// and the provenance record's location does not depend on the contract (both `assert` and
	// `own` keep it under host-provenance/, §6.2). A revert is an rmw-shaped operation — it
	// exists because rmw cannot express removal — so under `own` the render withdraws a
	// dropped pack's keys by regenerating without them, and this verb has nothing to add.
	e := &Env{Home: homeDir, Vars: map[string]string{}, hostTarget: true}

	var out HostRevert
	for _, s := range hostOverlaySurfaces(candidates, e.renderTarget().Profile()) {
		recPath := prismProvenancePath(e, s.Agent, s.Name)
		if recPath == "" {
			continue
		}
		data, err := os.ReadFile(recPath)
		if err != nil {
			// No record: yolo has never asserted this surface in this home, so there is
			// nothing of its here to withdraw. Absent-means-never-rendered is the same
			// reading hostProvenanceExists depends on.
			continue
		}
		path := expandHomePath(e, s.Path)
		orig, obj, before, derr := readRMWSource(s, path)
		if derr != nil {
			// A file yolo cannot decode is left untouched AND keeps its record: deleting
			// the record would strand keys yolo wrote with nothing left that remembers
			// whose they are, which is the laundering RetiredLayer exists to prevent.
			continue
		}
		id := s.Agent + "/" + s.Name
		action := "removed"
		if observe {
			action = "would remove"
		}
		var removed []string
		for _, k := range revertableKeys(agentcfg.ParseProvenanceRecord(data)) {
			if _, present := obj.Get(k.key); !present {
				continue
			}
			removed = append(removed, k.key)
			out.Keys = append(out.Keys, HostRevertedKey{Surface: id, Path: path, Key: k.key,
				Layer: k.layer, Action: action})
		}
		out.Records = append(out.Records, recPath)
		if observe {
			continue
		}
		if len(removed) > 0 {
			for _, k := range removed {
				obj.Delete(k)
			}
			// orig/before carry the file's comments into the re-emit: a revert removes named
			// keys and must leave the rest — including the prose around it — as it found it.
			text, eerr := encodeSurfaceObject(s, obj, orig, before)
			if eerr != nil {
				return out, fmt.Errorf("%s: %w", id, eerr)
			}
			if werr := writeInPlaceString(path, text); werr != nil {
				return out, fmt.Errorf("%s: %w", id, werr)
			}
		}
		// THE FILE IS NOT DELETED even when every key in it was yolo's and it is now empty.
		// Deletion is the host notch's one legitimate asymmetry (§6.3), and an empty object
		// is recoverable by hand while a deleted file is not — so a revert that emptied a
		// surface leaves `{}` rather than guessing that yolo created the file.
		if rerr := os.Remove(recPath); rerr != nil && !os.IsNotExist(rerr) {
			return out, fmt.Errorf("%s: removing the provenance record %s: %w", id, recPath, rerr)
		}
	}
	return out, nil
}

// revertedKey is one eligible provenance entry: the key and the attribution that makes it
// yolo's to remove.
type revertedKey struct {
	key   string
	layer string
}

// revertableKeys selects the record entries yolo can prove it WROTE, sorted by key so a
// revert is deterministic.
//
// THE ELIGIBLE SET, and why each member is in it:
//
//	managed / computed / config-overlay:<pack>   agentcfg.LayerAsserted — force-written on
//	                                             every render, regardless of what the file
//	                                             said. Uncontroversially yolo's output.
//	retired:<any of those>                       the same key, after the layer that claimed
//	                                             it stopped (agentcfg.RetiredLayer). The
//	                                             attribution was KEPT precisely so a pass
//	                                             like this can still see whose it was.
//	defaults                                     yolo FILLED a key the file did not have.
//
// `defaults` is the interesting one, and it is eligible here while LayerAsserted excludes it
// — which is not a contradiction but the difference between the two questions. LayerAsserted
// answers "may this attribution be RETIRED and then pruned as an orphan?", where a default's
// value is the user's to change and must not be swept. This asks "did yolo put this key in
// the file?", and for `defaults` the record only says so under keepFilledDefaults' two
// conditions: yolo filled the key (it was absent before), and the value is STILL the declared
// default (intactDefaults) — so a `defaults` line is a key that holds nothing the user wrote.
// Leaving it behind would make a revert incomplete in a way no user could see or fix: the
// keys yolo introduced would stay, with no verb left that removes them.
//
// EVERYTHING ELSE IS INELIGIBLE, which is `host` and any label this vocabulary does not
// know. Fail-safe by construction, the same posture LayerAsserted takes: a corrupt,
// truncated or hand-edited record can only reach a key by spelling one of the tokens above,
// and a bare "retired:" or "config-overlay:" with nothing behind it proves nothing and so
// protects the key.
func revertableKeys(record map[string]string) []revertedKey {
	var out []revertedKey
	for key, layer := range record {
		if !revertableLayer(layer) {
			continue
		}
		out = append(out, revertedKey{key: key, layer: layer})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out
}

// revertableLayer is the predicate, built from agentcfg's own constructions rather than
// string literals so the writer and this reader cannot drift on the vocabulary.
func revertableLayer(layer string) bool {
	if last, retired := agentcfg.RetiredOf(layer); retired {
		// A retired DEFAULTS label is not written today (retireUnclaimed only retires
		// asserted layers), but reading it as eligible costs nothing and stating the rule
		// once beats a second rule for a label that may exist tomorrow.
		layer = last
	}
	return agentcfg.LayerAsserted(layer) || layer == agentcfg.LayerDefaults
}
