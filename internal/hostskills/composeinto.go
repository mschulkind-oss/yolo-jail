package hostskills

// composeinto.go is the JAIL's entry into this package's one layer writer
// (docs/plans/notch-convergence.md#OQ-NC11, item 25).
//
// The jail used to copy every pack's skills flat with a copier of its own
// (jailcontent.copySkillSubdirs, deleted), so a namespaced pack's skill was `/<skill>` in a jail and
// `/<pack>:<skill>` at the host, a wrapped plugin arrived as a bare directory, and two packs
// shipping one name resolved silently to whichever came last. The ruling makes the host's
// behavior the jail's: the same layer plan (Destination and its Layers), written by the same
// writeLayer, refused by the same Collisions.
//
// WHAT DIFFERS IS ONLY THE DESTINATION, and it is why this is a second entry rather than a flag on
// RenderHostSkills. A jail's destination is a directory yolo owns outright: a scratch tree the
// jail's staging syncs into a :ro bind. There is nothing in it to adopt, nothing to archive and no
// record to keep, so every ownership input the host render carries is empty here, and every rule
// that reads one is inert: an entry already in dir can only be an earlier layer's, which the
// run's claim set proves is yolo's.
//
// WHAT THE JAIL ADDS is the reserved-child fence at WRITE time. The host fences a reserved child
// at adoption (Adoptions), because there the damage is taking the user's tree over. In a jail the
// damage is the other direction — the §7 home, where an earlier apply adopted a sync root into the
// local pack, would compose the user's claude.ai identity buckets into every jail — so a reserved
// name is withheld from the composed tree whichever layer ships it, and said.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ComposeResult is what ComposeInto wrote at one destination.
type ComposeResult struct {
	// Taken maps every top-level entry name the layers wrote to the pack that wrote it: the input
	// a lower layer (yolo's built-ins, the workspace) needs to fill only what is left, and to say
	// who took a name it wanted.
	Taken map[string]string
	// Results are every per-entry outcome, in the writer's order. A caller surfaces the refusals
	// (a namespaced delivery downgraded, a wrapped plugin's components a flat tier cannot carry)
	// and the reserved withholdings; the rest are detail.
	Results []Result
}

// ComposeInto composes one destination's layers into dir, which the caller owns wholesale and
// which holds nothing a layer did not write: packs in layer order, each at its own tier, wrapped
// plugins included.
//
// A COLLISION IS FATAL BEFORE ANYTHING IS WRITTEN, for RenderHostSkills' reason, and with its
// error: the refusal a user reads at a jail launch is the host's, word for word, so its remedy
// (rename one, or namespace one) means the same thing at both notches. The launch refuses it
// earlier, host-side, as a pre-flight over every destination; this check is the writer's own, so
// no caller can compose past it.
func ComposeInto(d Destination, dir string) (ComposeResult, error) {
	if cols := Collisions([]Destination{d}); len(cols) > 0 {
		return ComposeResult{}, CollisionError(cols)
	}
	claimed := map[string]string{}
	var out []Result
	for _, l := range d.Layers {
		// No record, no legacy record, nothing pre-owned, no archive: see the file comment. The
		// claim set is the one ownership input left, shared across the layers as the host render
		// shares it.
		res, err := writeLayer(dir, l, ComposeRequest{}, map[string]bool{}, claimed, false)
		out = append(out, res...)
		if err != nil {
			return ComposeResult{Results: out}, err
		}
	}
	taken := map[string]string{}
	paths := make([]string, 0, len(claimed))
	for p := range claimed {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		rel, err := filepath.Rel(dir, p)
		if err != nil || rel == "." || strings.ContainsRune(rel, filepath.Separator) || strings.HasPrefix(rel, "..") {
			continue // not a top-level entry: nothing a lower layer could contend for
		}
		pack := claimed[p]
		if d.IsReserved(rel) {
			r := Result{Name: rel, Path: p, Action: ActionReserved}
			if err := os.RemoveAll(p); err != nil {
				r.Action, r.Detail = ActionRefused, "could not withhold the reserved name: "+err.Error()
				out = append(out, r)
				return ComposeResult{Results: out}, err
			}
			what := d.ReservedNotes[rel]
			if what == "" {
				what = "another tool's sync root"
			}
			r.Detail = "pack " + pack + " ships an entry of this name, and a pack here reserves it for " +
				what + ", so it was withheld"
			out = append(out, r)
			continue
		}
		taken[rel] = pack
	}
	return ComposeResult{Taken: taken, Results: out}, nil
}
