package packload

// namecollisions.go refuses two selected packs that would be given ONE agent-facing name that is
// not a skill's: two entries whose namespaces are one word, and two packs whose `files` landing and
// `/ctx/host-…` directory are one name (docs/design/pack-naming.md §3.5). A skill name two packs
// deliver alike is hostskills.Collisions' refusal; this is the half that needs no skills read.
//
// Every clash is refused, never resolved by renaming: no name may depend on what else is
// selected (PN-D3).

import (
	"fmt"
	"strings"
)

// NameCollisions is one refusal per clash among packs, in selection order, each naming both
// entries and the step that resolves it.
func NameCollisions(packs []*Pack) []string {
	var out []string
	type first struct {
		pack  *Pack
		owner string
	}
	namespaces := map[string]first{}
	landings := map[string]*Pack{}
	for _, p := range packs {
		if p == nil {
			continue
		}
		if ns := p.Namespace; ns != "" {
			prev, seen := namespaces[ns]
			switch {
			case !seen:
				namespaces[ns] = first{pack: p, owner: p.NamespaceOwner}
			case prev.owner != p.NamespaceOwner:
				out = append(out, fmt.Sprintf("packs %s and %s are both in the namespace %q, so their "+
					"skills would be one set of names: %s", prev.pack.Name, p.Name, ns,
					namespaceStep(prev.pack, p)))
			}
		}
		landing := p.LandingName()
		if prev, seen := landings[landing]; seen {
			out = append(out, fmt.Sprintf("packs %s and %s would both land their files under %q "+
				"(and mount host files under /ctx/host-%s): set `namespace = \"…\"` on either entry "+
				"listed on its own, or drop `bare` from one", prev.Name, p.Name, landing, landing))
			continue
		}
		landings[landing] = p
	}
	return out
}

// namespaceStep is the step for two entries whose namespaces are one word: a pack listed on its
// own takes `namespace = "…"`, and a source is renamed by hand: its key and every handle using it.
func namespaceStep(a, b *Pack) string {
	var steps []string
	for _, p := range []*Pack{a, b} {
		if src, ok := strings.CutPrefix(p.NamespaceOwner, "source:"); ok {
			steps = append(steps, fmt.Sprintf("renaming the [pack_sources] key %s and the handles that use it (then `yolo check`)", src))
			continue
		}
		steps = append(steps, fmt.Sprintf("`namespace = \"…\"` on %s's entry", p.Name))
	}
	return "resolve it with " + strings.Join(steps, " or ")
}
