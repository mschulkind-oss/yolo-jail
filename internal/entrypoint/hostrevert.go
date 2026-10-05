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
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonptr"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// HostRevertedKey is one key a revert removed, or would remove.
type HostRevertedKey struct {
	// Surface is the identity the key lives in, "agent/name".
	Surface string
	// Path is the resolved real-home file the key is in.
	Path string
	// Key is the TOP-LEVEL key the record attributes, or — for a value withdrawn from INSIDE a
	// table (CO-D12) — the RFC 6901 pointer of that value ("/env/CLAUDE_CODE_USE_BEDROCK").
	Key string
	// Layer is the attribution the removal rests on, verbatim from the record
	// (`managed`, `computed`, `defaults`, `config-overlay:<pack>`, or a `retired:` form).
	// REPORTED, not just used: the user is owed the authority for each removal, and a
	// `defaults` line means something different to them than a `managed` one.
	Layer string
	// Action is "removed" or "would remove" (observe posture), or "kept" on a Kept entry.
	Action string
	// Why is a Kept entry's reason, in the user's terms: why the revert leaves a key the record
	// calls yolo's (CO-D12).
	Why string
}

// HostRevert is one revert's whole result: the keys, and the provenance records that end the
// relationship. Records is carried separately because it is not derivable from Keys — a
// surface yolo rendered but whose keys the user has since deleted has a record and no keys,
// and the record still has to go or the home keeps claiming a render that owns nothing.
type HostRevert struct {
	Keys []HostRevertedKey
	// Kept are the keys the record calls yolo's that the revert LEAVES, each with Action "kept"
	// and its Why: an empty default still at its declared value (keptWhyShape), and — since
	// CO-D12 — a value that no longer holds what yolo wrote (a default the user changed, a
	// selection key the user picked, a recorded leaf the user edited), which is the user's now.
	// Reported so the dry run names every key of yolo's the file will still hold, not only the
	// ones it takes out.
	Kept    []HostRevertedKey
	Records []string
	// Forgotten are the capture-store files and the pre-retirement selection record the revert
	// removes beside each withdrawn surface's records (CO-D13, CO-D14), so the next owned apply is
	// a first apply again rather than replaying the removals as the user's deletions.
	Forgotten []string
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
	// and the provenance record's location does not depend on the contract (`own` keeps it
	// under host-provenance/, §6.2, where the retired `assert` kept it). That independence is
	// what lets the CLI run a revert under `none` on a home `assert` wrote into (OQ-CO14). A
	// revert is an rmw-shaped operation — it exists because rmw cannot express removal. Under
	// `own` a composed surface withdraws a dropped key by regenerating without it, and the CLI
	// refuses this verb there; ⚠ an `rmw`-declared surface at an owned host keeps a key yolo
	// stopped writing as `retired:…` until a revert under `none` takes it out (a dropped PACK's
	// keys are pruned by PruneHostOverlayKeys at the apply).
	e := &Env{Home: homeDir, Vars: map[string]string{}, hostTarget: true}

	var out HostRevert
	// THE USER'S host_files SURFACES TOO (OQ-NC8): a source-less entry's render keeps the same
	// record under the same directory, so the revert withdraws it by the same walk. They come
	// from the records themselves rather than the config, since a withdrawing user may have
	// emptied `host_files` already, exactly as they may have emptied `packs`.
	surfaces := append(hostOverlaySurfaces(candidates, e.renderTarget().Profile()),
		hostUserFileRecords(e)...)
	for _, s := range surfaces {
		if err := withdrawHostSurface(e, s, observe, &out); err != nil {
			return out, err
		}
	}
	return out, nil
}

// withdrawHostSurface is one surface of the revert walk: remove from its real file every key the
// provenance record attributes to a layer yolo wrote, and delete the record (and the config-list
// record beside it). It appends what it did, or would do under observe, to out. It is the ONE
// withdrawal, run by `--revert` over every recorded surface and by a host apply over a dropped
// host_files entry's (RetireHostUserFiles), so the two remove by one rule.
func withdrawHostSurface(e *Env, s manifest.Surface, observe bool, out *HostRevert) error {
	recPath := prismProvenancePath(e, s.Agent, s.Name)
	if recPath == "" {
		return nil
	}
	data, err := os.ReadFile(recPath)
	if err != nil {
		// No record: yolo has never asserted this surface in this home, so there is
		// nothing of its here to withdraw. Absent-means-never-rendered is the same
		// reading hostProvenanceExists depends on.
		return nil
	}
	path := expandHomePath(e, s.Path)
	orig, obj, before, derr := readRMWSource(s, path)
	if derr != nil {
		// A file yolo cannot decode is left untouched AND keeps its record: deleting
		// the record would strand keys yolo wrote with nothing left that remembers
		// whose they are, which is the laundering RetiredLayer exists to prevent.
		return nil
	}
	id := s.Agent + "/" + s.Name
	action := "removed"
	if observe {
		action = "would remove"
	}
	record := agentcfg.ParseProvenanceRecord(data)
	// THE CONFIG-LIST ENTRIES YOLO INSERTED, first and per entry: a list path is never a
	// whole key yolo owns (its `config-list` label is not asserted), so the key pass
	// below cannot reach it, and removing the whole array would take the user's own
	// entries with it. The insert record is the authority, exactly as the provenance
	// record is for keys — only an entry yolo recorded inserting is removed.
	listRec := e.renderTarget().ListRecordPath(s.Agent, s.Name)
	listKeys, listChanged := revertListEntries(obj, readListRecord(e, s.Agent, s.Name), record)
	for _, k := range listKeys {
		out.Keys = append(out.Keys, HostRevertedKey{Surface: id, Path: path, Key: k,
			Layer: agentcfg.LayerConfigList, Action: action})
	}
	if listRec != "" {
		if _, err := os.Stat(listRec); err == nil {
			out.Records = append(out.Records, listRec)
		}
	}
	// THE VALUES yolo WROTE, never more (CO-D12). A record entry is per TOP-LEVEL key, and a key
	// can hold the user's content beside yolo's: `env` holds one switch a derive asserted and the
	// user's own variables; `permissions` holds the managed `defaultMode` and an `allow` the user
	// added; a default or a selected model the user has since changed holds the user's value. So a
	// key is withdrawn by what yolo recorded writing inside it — the computed-leaf record, the
	// selection record, the pack's declared managed and default values — and only a key with no
	// such record goes whole, as before.
	ev := revertEvidence{
		leaves:    readHostLeafRecord(e, s.Agent, s.Name),
		selection: readRevertSelection(e, s.Agent, s.Name),
	}
	plain, _ := jsonx.Plain(obj).(map[string]any)
	var removed, removedLeaves, emptiedAfter []string
	for _, k := range revertableKeys(record) {
		v, present := obj.Get(k.key)
		if !present {
			continue
		}
		w := ev.withdrawal(s, k, jsonx.Plain(v), plain)
		for _, kept := range w.kept {
			out.Kept = append(out.Kept, HostRevertedKey{Surface: id, Path: path, Key: kept.key,
				Layer: k.layer, Action: "kept", Why: kept.why})
		}
		for _, leaf := range w.leaves {
			removedLeaves = append(removedLeaves, leaf)
			out.Keys = append(out.Keys, HostRevertedKey{Surface: id, Path: path, Key: leaf,
				Layer: k.layer, Action: action})
		}
		if w.whole {
			removed = append(removed, k.key)
			out.Keys = append(out.Keys, HostRevertedKey{Surface: id, Path: path, Key: k.key,
				Layer: k.layer, Action: action})
		}
		if w.dropIfEmptied {
			emptiedAfter = append(emptiedAfter, k.key)
		}
	}
	out.Records = append(out.Records, recPath)
	forget := revertForgottenFiles(e, s)
	out.Forgotten = append(out.Forgotten, forget...)
	if observe {
		return nil
	}
	if len(removed) > 0 || len(removedLeaves) > 0 || listChanged {
		for _, k := range removed {
			obj.Delete(k)
		}
		for _, leaf := range removedLeaves {
			deleteLeaf(obj, leaf)
		}
		// A table whose every value was yolo's goes with them: what is left is a key yolo
		// created, now holding nothing.
		for _, k := range emptiedAfter {
			if v, ok := obj.Get(k); ok {
				if m, isMap := v.(*jsonx.OrderedMap); isMap && m.Len() == 0 {
					obj.Delete(k)
				}
			}
		}
		// orig/before carry the file's comments into the re-emit: a revert removes named
		// keys and must leave the rest — including the prose around it — as it found it.
		text, eerr := encodeSurfaceObject(s, obj, orig, before)
		if eerr != nil {
			return fmt.Errorf("%s: %w", id, eerr)
		}
		if werr := writeInPlaceString(path, text); werr != nil {
			return fmt.Errorf("%s: %w", id, werr)
		}
	}
	// THE FILE IS NOT DELETED even when every key in it was yolo's and it is now empty.
	// Deletion is the host notch's one legitimate asymmetry (§6.3), and an empty object
	// is recoverable by hand while a deleted file is not — so a revert that emptied a
	// surface leaves `{}` rather than guessing that yolo created the file. That is why an
	// empty declared default stays (keptWhyShape): the file left behind has to be one
	// its agent still reads.
	if rerr := os.Remove(recPath); rerr != nil && !os.IsNotExist(rerr) {
		return fmt.Errorf("%s: removing the provenance record %s: %w", id, recPath, rerr)
	}
	if listRec != "" {
		if rerr := os.Remove(listRec); rerr != nil && !os.IsNotExist(rerr) {
			return fmt.Errorf("%s: removing the config-list record %s: %w", id, listRec, rerr)
		}
	}
	// The computed-leaf record ends with the relationship too (HC-D25): the leaves it names were
	// removed above, or are the user's now.
	if leafRec := e.renderTarget().LeafRecordPath(s.Agent, s.Name); leafRec != "" {
		if rerr := os.Remove(leafRec); rerr != nil && !os.IsNotExist(rerr) {
			return fmt.Errorf("%s: removing the computed-leaf record %s: %w", id, leafRec, rerr)
		}
	}
	// AND THE CAPTURE STORE'S FILES FOR THE SURFACE (CO-D13), with the selection record the
	// retired `assert` kept beside the provenance record (CO-D14). Left behind, an owned apply
	// after the revert read the old last_render as its baseline and the file the revert left as
	// edits against it — every key the revert took out replayed as the user's deletion, written
	// as `"theme": null` — and "a later owned apply is a FIRST apply again" was false.
	for _, f := range forget {
		if rerr := os.Remove(f); rerr != nil && !os.IsNotExist(rerr) {
			return fmt.Errorf("%s: removing %s: %w", id, f, rerr)
		}
	}
	return nil
}

// revertForgottenFiles is every file beside a surface's records that the revert removes with
// them, and that exists: the owned capture store's sidecars for the surface (the baseline, the
// captured edits, the selection record and the list capture) and the selection record the retired
// `assert` kept under the provenance directory (legacySelectionRecordPath).
func revertForgottenFiles(e *Env, s manifest.Surface) []string {
	owned := render.Host(e.Home, nil, render.OwnershipOwn)
	var out []string
	for _, f := range []string{
		owned.LastRenderPath(s.Agent, s.Name),
		owned.OverlayPath(s.Agent, s.Name),
		owned.SelectionPath(s.Agent, s.Name),
		owned.ListCapturePath(s.Agent, s.Name),
		legacySelectionRecordPath(e, s.Agent, s.Name),
	} {
		if f == "" {
			continue
		}
		if _, err := os.Stat(f); err == nil {
			out = append(out, f)
		}
	}
	return out
}

// readRevertSelection is the selection record a revert judges a selection key by: the owned
// capture store's, or else the one the retired `assert` kept beside the provenance record — the
// record that says which value yolo's selection last wrote, so a value differing from it is the
// user's own pick.
func readRevertSelection(e *Env, agent, name string) map[string]any {
	for _, f := range []string{
		render.Host(e.Home, nil, render.OwnershipOwn).SelectionPath(agent, name),
		legacySelectionRecordPath(e, agent, name),
	} {
		if f == "" {
			continue
		}
		if data, err := os.ReadFile(f); err == nil {
			return agentcfg.ParseSelectionRecord(data)
		}
	}
	return nil
}

// revertEvidence is what a revert knows yolo wrote INSIDE a surface's keys, beside the per-key
// provenance record: the computed-leaf record (pointer → the value yolo wrote) and the selection
// record (top-level key → the value yolo's selection wrote).
type revertEvidence struct {
	leaves    map[string]any
	selection map[string]any
}

// revertWithdrawal is what a revert does with one recorded key: take it out whole, take out the
// listed values inside it (pointers), and keep the listed values, each with why. dropIfEmptied
// asks for the key itself to go once its withdrawn values leave it an empty object.
type revertWithdrawal struct {
	whole         bool
	leaves        []string
	kept          []revertKept
	dropIfEmptied bool
}

type revertKept struct{ key, why string }

// The reasons a revert keeps a value the record calls yolo's, in the user's terms.
//
// keptWhyShape is the one that is not the user's: a `defaults` key whose declared default is an
// empty object or array, still holding exactly that, holds nothing to withdraw — it is the SHAPE
// the pack declares its file needs. pi/models is why (HC-D1): pi 0.87.1 rejects a models.json
// without `providers`, so the pack declares `"providers": {}`, and a revert that removed it left
// `{}` in a file the apply had created — a revert empties a file rather than deleting it — and pi
// printed `models.json error` at every start. Keeping the shape needs no guess about who created
// the file. An empty default the user has since filled is theirs (keptWhyDefault).
const (
	keptWhyShape     = "an empty default, the shape the pack declares this file needs"
	keptWhySelection = "it no longer holds the value yolo's selection wrote, so it is a pick of yours"
	keptWhyDefault   = "it no longer holds the default the pack declares, so it is a value of yours"
	keptWhyManaged   = "it no longer holds the value your packs declare, so it may be a value of yours"
	keptWhyLeaf      = "it no longer holds the value yolo wrote there, so it is a value of yours"
)

// withdrawal decides one recorded key (CO-D12), v being its current value and file the whole
// document, both plain. In order, the first that applies:
//
//   - A SELECTION KEY (the selection record names it): out whole when it still holds the value the
//     selection wrote, else kept — a model the user picked after yolo selected one is theirs.
//   - A `defaults` KEY: out whole when it still holds the declared default (kept as the file's
//     shape when that default is an empty container, keptWhyShape), withdrawn value by value
//     when both are objects, else kept — under `none` no apply ever relabels an edited default as
//     the user's, so the revert has to look.
//   - AN OBJECT: withdrawn value by value — each leaf the computed-leaf record names under it, and
//     each value the surface's managed layer declares under it, that still holds what yolo wrote;
//     every other value in it is the user's and stays. The key goes too if that leaves it empty.
//     With nothing recorded or declared inside it (a wholesale table, a key a dropped overlay
//     wrote), it goes whole, as before.
//   - A SCALAR the leaf record or the managed layer names: out when it still holds that value,
//     else kept.
//   - Anything else goes whole, as before: the record is the only evidence there is.
func (ev revertEvidence) withdrawal(s manifest.Surface, k revertedKey, v any, file map[string]any) revertWithdrawal {
	layer := k.layer
	if last, retired := agentcfg.RetiredOf(layer); retired {
		layer = last
	}
	if wrote, ok := ev.selection[k.key]; ok {
		if sameJSON(v, wrote) {
			return revertWithdrawal{whole: true}
		}
		return revertWithdrawal{kept: []revertKept{{k.key, keptWhySelection}}}
	}
	if layer == agentcfg.LayerDefaults {
		declared, ok := s.DefaultsMap()[k.key]
		switch {
		case !ok:
			return revertWithdrawal{whole: true}
		case sameJSON(v, declared) && emptyContainer(jsonx.Plain(declared)):
			return revertWithdrawal{kept: []revertKept{{k.key, keptWhyShape}}}
		case sameJSON(v, declared):
			return revertWithdrawal{whole: true}
		}
		if dm, isMap := jsonx.Plain(declared).(map[string]any); isMap {
			if _, vIsMap := v.(map[string]any); vIsMap {
				if leaves := flattenLeaves(map[string]any{k.key: dm}); len(leaves) > 0 {
					return ev.byValue(file, leaves, keptWhyDefault)
				}
			}
		}
		return revertWithdrawal{kept: []revertKept{{k.key, keptWhyDefault}}}
	}
	declared, hasDecl := s.ManagedMap()[k.key]
	if _, isMap := v.(map[string]any); isMap {
		wrote := map[string]any{}
		for p, w := range ev.leaves {
			if steps, err := jsonptr.Parse(p); err == nil && len(steps) > 1 && steps[0] == k.key {
				wrote[p] = w
			}
		}
		if dm, isMap := jsonx.Plain(declared).(map[string]any); hasDecl && isMap {
			for p, w := range flattenLeaves(map[string]any{k.key: dm}) {
				if _, recorded := wrote[p]; !recorded {
					wrote[p] = w
				}
			}
		}
		if len(wrote) == 0 {
			return revertWithdrawal{whole: true}
		}
		return ev.byValue(file, wrote, keptWhyLeaf)
	}
	if w, ok := ev.leaves[jsonptr.Format([]string{k.key})]; ok {
		if sameJSON(v, w) {
			return revertWithdrawal{whole: true}
		}
		return revertWithdrawal{kept: []revertKept{{k.key, keptWhyLeaf}}}
	}
	if layer == agentcfg.LayerManaged && hasDecl {
		if _, isMap := jsonx.Plain(declared).(map[string]any); !isMap {
			if sameJSON(v, declared) {
				return revertWithdrawal{whole: true}
			}
			return revertWithdrawal{kept: []revertKept{{k.key, keptWhyManaged}}}
		}
	}
	return revertWithdrawal{whole: true}
}

// byValue withdraws each pointer in wrote whose value the file still holds, keeping (with why)
// each one the file holds differently; a pointer the file no longer reaches is not reported.
func (revertEvidence) byValue(file, wrote map[string]any, why string) revertWithdrawal {
	ptrs := make([]string, 0, len(wrote))
	for p := range wrote {
		ptrs = append(ptrs, p)
	}
	sort.Strings(ptrs)
	w := revertWithdrawal{dropIfEmptied: true}
	for _, p := range ptrs {
		cur, in := leafAt(file, p)
		if !in {
			continue
		}
		if sameJSON(cur, wrote[p]) {
			w.leaves = append(w.leaves, p)
			continue
		}
		w.kept = append(w.kept, revertKept{p, why})
	}
	return w
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

// emptyContainer is an empty object or an empty array, in jsonx.Plain form.
func emptyContainer(v any) bool {
	switch c := v.(type) {
	case map[string]any:
		return len(c) == 0
	case []any:
		return len(c) == 0
	}
	return false
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

// revertListEntries removes from obj every config-list entry the insert record says yolo
// INSERTED, returning one report line per removed entry ("<pointer> <entry>") and whether
// the document changed. A key whose provenance reads `config-list` — one ONLY the list step
// created — is deleted outright once it holds nothing, since yolo created it; any other key
// is left, emptied or not, because an empty array the user's file already had is theirs.
//
// Conservative in the same ways as the key walk: an unreadable record claims nothing, a path
// the file no longer holds as an array is skipped, and an entry the file no longer holds is
// not reported.
func revertListEntries(obj *jsonx.OrderedMap, recs map[string]agentcfg.ListInsertRecord,
	provenance map[string]string) ([]string, bool) {
	paths := make([]string, 0, len(recs))
	for p := range recs {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var lines []string
	changed := false
	for _, p := range paths {
		tokens, err := jsonptr.Parse(p)
		if err != nil || len(tokens) == 0 {
			continue
		}
		v, st, _ := rmwLookup(obj, tokens)
		arr, isArr := v.([]any)
		if st != pathFoundRMW || !isArr {
			continue
		}
		next := arr
		for _, e := range recs[p].Inserted {
			kept := next[:0:0]
			hit := false
			for _, x := range next {
				if agentcfg.EntriesEqual(x, e) {
					hit = true
					continue
				}
				kept = append(kept, x)
			}
			if hit {
				lines = append(lines, p+" "+oneLineEntry(e))
				next = kept
			}
		}
		if len(next) == len(arr) {
			continue
		}
		changed = true
		if len(next) == 0 && len(tokens) == 1 && provenance[tokens[0]] == agentcfg.LayerConfigList {
			obj.Delete(tokens[0])
			continue
		}
		rmwSetPath(obj, tokens, next)
	}
	return lines, changed
}

// oneLineEntry renders a list entry for a report line.
func oneLineEntry(v any) string {
	b, err := json.Marshal(jsonx.Plain(v))
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}
