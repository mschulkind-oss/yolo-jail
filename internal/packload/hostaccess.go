package packload

// hostaccess.go is THE host-access claim set for a pack: the union of every producer,
// computed once, so the install gate and the launch gate cannot disagree about what a
// pack asks of the host.
//
// # Why one helper, and why the old invariant was already false
//
// packdecl's HostAccessClaims doc claimed the boundary was "collected in one predicate so
// a caller cannot check two of the three and believe it covered the boundary". That was
// true when there was one producer and FALSE the moment there were two: as of the plugin
// kind, both gates appended by hand —
//
//	want := append(p.Decl.HostAccessClaims(), p.PluginHostAccessClaims()...)
//
// once in internal/cli/pack.go's resolveHostApproval (which PROMPTS and records the
// lockfile) and once in internal/cli/run/packs.go's packMayAccessHost (which CHECKS the
// lockfile at launch), with a comment at the second warning what happens when one is
// updated and the other is not. Two hand-built unions of the same set is a
// drift-by-construction shape, and the drift is not cosmetic in either direction:
//
//   - a producer added to the PROMPT only → the user approves a claim the launch gate
//     never asks about, i.e. an unapproved crossing is honored;
//   - a producer added to the LAUNCH gate only → the launch demands approval for a claim
//     `pack install` never showed, so the pack is refused with no route to approving it.
//
// The `loophole` kind is a THIRD producer, and it is the first whose claim is host code
// EXECUTION, so the cost of that drift stops being a config read. Hence: one function,
// both gates call it, and a source-level test (hostaccessgates_test.go) fails if either
// gate reaches for a producer directly.

import "sort"

// HostAccessClaims is the SPECIFIC, sorted, deduplicated set of host-access claims this
// pack makes — the union of every producer:
//
//	p.Decl.HostAccessClaims()      pack.json's own: reads-host, mount, installer, briefing
//	p.PluginHostAccessClaims()     a wrapped agent plugin's code-running components
//	p.LoopholeHostAccessClaims()   a shipped loophole's daemon, intercepts, binds, devices
//
// It is what a user approves at `yolo pack install` and what the launch gate checks
// against the lockfile, so the two ends compare the same strings by construction rather
// than by two call sites happening to list the same producers.
//
// EVERY producer's strings are lockfile comparison keys: specific (which file, which dir,
// which argv), stable across machines, and never elided. A new producer belongs in this
// function and nowhere else.
//
// Deduplicated because a union is a set — the same claim reached by two producers is one
// thing to approve, and a duplicate would make the approved set's length depend on how
// the claim was derived.
func (p *Pack) HostAccessClaims() []string {
	var out []string
	out = append(out, p.Decl.HostAccessClaims()...)
	out = append(out, p.PluginHostAccessClaims()...)
	out = append(out, p.LoopholeHostAccessClaims()...)
	sort.Strings(out)
	return dedupeSorted(out)
}

// dedupeSorted collapses adjacent duplicates in a sorted slice, returning nil for empty
// input — nil rather than an empty slice because `len(want) == 0` is the gates' own
// "nothing to approve" test and both spellings must read the same there.
func dedupeSorted(in []string) []string {
	var out []string
	for i, s := range in {
		if i > 0 && s == in[i-1] {
			continue
		}
		out = append(out, s)
	}
	return out
}
