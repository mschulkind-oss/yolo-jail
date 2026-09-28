package packload

// profiledisclosure.go is the launch's profile disclosure, worded once for every notch
// (docs/reference/providers.md#what-the-launch-checks-and-prints; docs/plans/notch-convergence.md
// item 13, row A7): one line per DISTINCT profile name the launch selects, naming the packs that
// DECLARE a variant of that name and the packs that RECEIVED it. A jail launch printed it and
// `yolo host` printed nothing, so a host launch never said where its profile landed.

import (
	"sort"
	"strings"
)

// ProfileDisclosure is one selected profile name, with the selected packs that declare a
// variant of it and the ones that received it.
//
// RECEIVED is every selected pack, not the pack the name keys to: the table reaches every
// pack's derive whole, so "who got it" has no narrower honest answer. DECLARED is the packs
// shipping a `kind: "profile"` with that name, the half that says whether the name means
// anything to any selected pack. It is NOT the packs that will act on it: a pack may declare the
// name and then do its variant work inside a derive nothing here can see. So the verb is never
// "honored" (providers.md#pv-oq-10).
type ProfileDisclosure struct {
	Name               string
	Declared, Received []string
}

// Head is the line's label, "Profile <name>:".
func (d ProfileDisclosure) Head() string { return "Profile " + d.Name + ":" }

// Detail is the rest of the line: "declared: <packs>; received: <packs>".
func (d ProfileDisclosure) Detail() string {
	who := "none"
	if len(d.Declared) > 0 {
		who = strings.Join(d.Declared, ", ")
	}
	return "declared: " + who + "; received: " + strings.Join(d.Received, ", ")
}

// Line is the whole line, unstyled.
func (d ProfileDisclosure) Line() string { return d.Head() + " " + d.Detail() }

// ProfileDisclosures is the disclosure for a launch's CLI-keyed profile table over its selected
// packs, one entry per distinct name, sorted by name. Nil when nothing is selected: a launch
// with no profile is the common case, and restating its absence would be noise.
//
// Two packs declaring one name are both listed. The name is owned only within a pack, so they
// are unrelated declarations of one selector value, and saying so beats hiding the coincidence.
func ProfileDisclosures(table map[string]string, packs []*Pack) []ProfileDisclosure {
	seen := map[string]bool{}
	var names []string
	for _, name := range table {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	received := make([]string, 0, len(packs))
	for _, p := range packs {
		received = append(received, p.Name)
	}
	sort.Strings(received)
	out := make([]ProfileDisclosure, 0, len(names))
	for _, name := range names {
		var declared []string
		for _, p := range packs {
			if p.Decl != nil && p.Decl.ProfileFor(name) != nil {
				declared = append(declared, p.Name)
			}
		}
		sort.Strings(declared)
		out = append(out, ProfileDisclosure{Name: name, Declared: declared, Received: received})
	}
	return out
}
