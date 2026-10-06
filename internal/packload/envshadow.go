package packload

// envshadow.go is OQ-NC12's DISCLOSURE (docs/plans/notch-convergence.md, ruled 2026-10-05: "can
// we do A and then disclose at every launch if something was shadowed in that way? that's
// probably best"): a launch names each variable for which one of yolo's own sources (a profile, the
// env_sources keys file, a pack's `env`) beat another that set it to something else, naming the
// winning source and every losing one. Never a value: the line is about which source decided, and
// these variables are typically credentials.
//
// Worded once here and printed at every vehicle the one ordered composition (envcompose.go) serves:
// a jail launch and an attach on podman or Apple Container and a macos-user launch
// (internal/cli/run's noteCredentialScope, over every process the launch composes for), and
// `yolo host -- <cmd>` and `yolo host env` (internal/cli, over the one process it composes). A
// disclosure, so no flag hides it (docs/reference/report-tiers.md, OQ-RO3), and one line per
// shadowed name, so a launch where nothing is shadowed prints nothing.
//
// WHAT IS NOT A SHADOW HERE (notch-convergence NC-D74, NC-D75):
//
//   - an entry beaten by one of its own rank: two packs' `env` of one name, and a pack's gated
//     value over its own static one, keep EnvFold's per-pack order (providers.md pv-oq-8),
//     which this ruling does not touch;
//   - a loser that gives the process what the winner gives it (the same value, or two ways of
//     setting nothing), since nothing the user configured was lost;
//   - the user's own shell at the host: OQ-NC13 ruled that it has no say over a name yolo
//     composes, as a host's environment has none in a jail, so it is no losing source;
//   - the names a vehicle writes over the composition (the three wire tables, a pointer the host
//     hands only its launch-owned service), which the caller passes as except.

import (
	"sort"
	"strings"
)

// shadowProcess is one process the disclosure speaks for: its composition, and the delivery whose
// profiles name a shape var's source (nil for the shared composition, which has no shape vars).
type shadowProcess struct {
	agent string
	comp  EnvComposition
	d     *AgentDelivery
}

// ShadowLines is the disclosure for a JAIL launch, which composes for every process at once: the
// shared composition (a shell, an agent no profile selects) and each agent with a delivery of its
// own (EnvFor). One line per shadowed name, sorted, nil when nothing is shadowed. A clause that
// holds for one agent's process alone says which ("for claude"); one that holds for every
// process says nothing more, unless an agent's process differs, when it says "for every other
// process". except is the names the vehicle writes over the composition. Nil on a nil receiver.
func (s *CredentialScope) ShadowLines(except []string) []string {
	if s == nil {
		return nil
	}
	var agents []shadowProcess
	for _, agent := range s.Agents() {
		agents = append(agents, shadowProcess{agent: agent, comp: s.EnvFor(agent), d: s.agents[agent]})
	}
	return shadowLines(shadowProcess{comp: s.SharedEnv()}, agents, except)
}

// ShadowLinesFor is the disclosure for ONE process, agent's (EnvFor): the host exec, which composes
// for the program it runs and nothing else, so no clause names a process. Same lines, same order
// and same silence as ShadowLines. Nil on a nil receiver.
func (s *CredentialScope) ShadowLinesFor(agent string, except []string) []string {
	if s == nil {
		return nil
	}
	return shadowLines(shadowProcess{agent: agent, comp: s.EnvFor(agent), d: s.agents[agent]}, nil, except)
}

// shadowLines renders base's shadows and each agent's that differ from base's, one line per name.
func shadowLines(base shadowProcess, agents []shadowProcess, except []string) []string {
	skip := map[string]bool{}
	for _, k := range except {
		skip[k] = true
	}
	keys := map[string]bool{}
	for _, p := range append([]shadowProcess{base}, agents...) {
		for k := range p.comp.shadowed {
			if !skip[k] {
				keys[k] = true
			}
		}
	}
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	var lines []string
	for _, key := range names {
		baseClause := shadowClause(base, key)
		type group struct {
			clause string
			agents []string
		}
		var groups []*group
		for _, p := range agents {
			c := shadowClause(p, key)
			if c == "" || c == baseClause {
				continue
			}
			found := false
			for _, g := range groups {
				if g.clause == c {
					g.agents, found = append(g.agents, p.agent), true
					break
				}
			}
			if !found {
				groups = append(groups, &group{clause: c, agents: []string{p.agent}})
			}
		}
		var parts []string
		for _, g := range groups {
			parts = append(parts, g.clause+", for "+andList(g.agents))
		}
		switch {
		case baseClause != "" && len(groups) > 0:
			parts = append(parts, baseClause+", for every other process")
		case baseClause != "":
			parts = append(parts, baseClause)
		}
		if len(parts) == 0 {
			continue
		}
		lines = append(lines, "Shadowed "+key+": "+strings.Join(parts, "; "))
	}
	return lines
}

// shadowClause is "<winner> wins over <losers>" for key in p's composition, "" when nothing it
// shadowed gave the process anything other than what the winner gives it.
func shadowClause(p shadowProcess, key string) string {
	winner, ok := p.comp.Lookup(key)
	if !ok {
		return ""
	}
	var losers []string
	for _, l := range p.comp.shadowed[key] {
		if sameEffect(l, winner) {
			continue
		}
		losers = append(losers, shadowSource(p, l))
	}
	if len(losers) == 0 {
		return ""
	}
	return shadowSource(p, winner) + " wins over " + andList(losers)
}

// sameEffect reports whether two entries give a process the same thing: one value, or nothing
// (a removal and an empty value both leave the name unset at every reader).
func sameEffect(a, b EnvEntry) bool {
	av, aok := effectiveValue(a)
	bv, bok := effectiveValue(b)
	return aok == bok && av == bv
}

// effectiveValue is what e leaves the process holding under its name; false for nothing.
func effectiveValue(e EnvEntry) (string, bool) {
	if e.Unset || e.Value == "" {
		return "", false
	}
	return e.Value, true
}

// shadowSource names the source of e in the line, never its value: "the zai profile's value",
// "your env_sources removal", "the aws-auth pack's value".
func shadowSource(p shadowProcess, e EnvEntry) string {
	noun := "value"
	if e.Unset {
		noun = "removal"
	}
	switch e.Origin {
	case FromPackEnv:
		if e.Pack == "" {
			return "a selected pack's " + noun
		}
		return "the " + e.Pack + " pack's " + noun
	case FromEnvSources:
		return "your env_sources " + noun
	case FromProfileEnv:
		d := p.d
		// THE REGION FILL (regionfill.go) is a shape var the region file gave, not the profile's
		// derive, and the Region line names that file too.
		if d != nil && d.RegionFile != nil && d.RegionFile.Region != "" && d.RegionFile.Var == e.Key &&
			!e.Unset && e.Value == d.RegionFile.Region {
			return "the region yolo read from " + d.RegionFile.fileLabel()
		}
		set := []string(nil)
		if d != nil {
			set = d.Set
			if len(set) == 0 && d.Profile != "" {
				set = []string{d.Profile}
			}
		}
		switch len(set) {
		case 0:
			return "the active profile's " + noun
		case 1:
			return "the " + set[0] + " profile's " + noun
		default:
			return "the " + andList(set) + " profiles' " + noun
		}
	}
	return "a launch source's " + noun
}
