package packload

// treefallback.go is an UNMODIFIED EXTENSION's FALLBACK (docs/design/pi-extension-store-builds.md
// §4.3, XB-D7): where a launch hands no tree for an extension that declares one — a notch that
// builds none, a build that failed with nothing serving, a wire that is absent — the author's raw
// entry takes the place of the tree's list entry in the contributing pack's own list
// contributions, before anything folds, so the agent installs the extension itself, exactly as it
// does with no yolo in between.
//
// THIS IS NOT THE POST-MERGE REWRITE pack-system.md's OQ-LT2 forbids: it chooses between two
// strings one author wrote, in that author's own contributions, before the fold, and touches no
// other pack's entry and no entry the user wrote. Equality is exact, so core reads none of the
// agent's grammar.

import (
	"bytes"
	"encoding/json"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// ApplyTreeFallbacks returns packs with every taken fallback substituted, and the trees whose
// fallback was taken, in PatchedTrees' order. A tree's fallback is taken when it declares one and
// handed reports that this launch hands it no tree. Every pack it changes is a copy (copyPackDecl,
// its posture lists copied too); the input is never modified, since an embedded pack is one shared
// value per process. With nothing taken, packs is returned as it came.
func ApplyTreeFallbacks(packs []*Pack, handed func(Fork) bool) ([]*Pack, []Fork) {
	var taken []Fork
	byPack := map[string]map[string]string{} // pack → the tree's list entry → its fallback
	for _, f := range PatchedTrees(packs) {
		if f.Fallback == "" || handed(f) {
			continue
		}
		taken = append(taken, f)
		if byPack[f.Pack] == nil {
			byPack[f.Pack] = map[string]string{}
		}
		byPack[f.Pack][f.ListEntry()] = f.Fallback
	}
	if len(taken) == 0 {
		return packs, nil
	}
	out := append([]*Pack(nil), packs...)
	for i, p := range out {
		if p == nil || p.Decl == nil || byPack[p.Name] == nil {
			continue
		}
		swap := byPack[p.Name]
		cp := copyPackDecl(p)
		for j, c := range cp.Decl.Contributes {
			switch c.Kind {
			case packdecl.KindConfigList:
				cp.Decl.Contributes[j].Add = substituteEntries(c.Add, swap)
			case packdecl.KindAutonomy:
				cp.Decl.Contributes[j].Autonomous = postureWithFallbacks(c.Autonomous, swap)
				cp.Decl.Contributes[j].Guarded = postureWithFallbacks(c.Guarded, swap)
			}
		}
		out[i] = cp
	}
	return out, taken
}

// postureWithFallbacks is a copy of posture with swap applied to its lists, or posture itself when
// it has none.
func postureWithFallbacks(posture *packdecl.AutonomyPosture, swap map[string]string) *packdecl.AutonomyPosture {
	if posture == nil || len(posture.Lists) == 0 {
		return posture
	}
	cp := *posture
	cp.Lists = append([]packdecl.PostureList(nil), posture.Lists...)
	for i, l := range cp.Lists {
		cp.Lists[i].Add = substituteEntries(l.Add, swap)
	}
	return &cp
}

// substituteEntries is a list body's `add` array with every string entry swap names replaced by its
// value, in place, so the list keeps its order. A body that does not decode, or names none of them,
// is returned as it came: the collector reports a malformed one.
func substituteEntries(add json.RawMessage, swap map[string]string) json.RawMessage {
	var entries []any
	dec := json.NewDecoder(bytes.NewReader(add))
	dec.UseNumber() // a number entry beside the swapped one is written back as it was written
	if err := dec.Decode(&entries); err != nil {
		return add
	}
	changed := false
	for i, e := range entries {
		if s, ok := e.(string); ok {
			if to, hit := swap[s]; hit {
				entries[i], changed = to, true
			}
		}
	}
	if !changed {
		return add
	}
	b, err := json.Marshal(entries)
	if err != nil {
		return add
	}
	return b
}
