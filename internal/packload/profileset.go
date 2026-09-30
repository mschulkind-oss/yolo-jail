package packload

// profileset.go is the ACTIVE SET's core half (docs/design/active-provider-sets.md, OQ-AP1 to
// OQ-AP3 ruled 2026-09-29): an agent's selection is an ordered list of profiles, not one.
//
// The terms are that doc's, and it coins all three:
//
//   - an ACTIVE SET is the ordered list of profiles one agent (one CLI name) runs on for one
//     launch — the value of its entry in the config `profile` key or its `-p` pair;
//   - its PRIMARY is the first entry, where a fresh session starts when yolo has to pick, and
//     what every fold written before sets reads as "the selected profile" (ProfileTable);
//   - a SET-CAPABLE agent is one whose pack declares `provider_sets` on the program that installs
//     it (packdecl.Contribution.ProviderSets), and so may be handed a set of more than one.
//
// A set of one is today's selection exactly (AP-P1): it crosses as the plain string it always
// did (ProfileSetWire), so nothing an existing config renders moves.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// ProfileSetValue lowers ONE value of a profile table to its set: a non-empty string is a set of one,
// a non-empty array of non-empty strings is the set in written order. ok is false for anything
// else — a null (the key removed), an empty array, or an element that is not a name — so a
// malformed value selects nothing, the answer ProfileTable always gave a non-string. The config
// validator refuses each of those shapes before a launch reaches here.
func ProfileSetValue(v any) ([]string, bool) {
	switch t := v.(type) {
	case string:
		if t == "" {
			return nil, false
		}
		return []string{t}, true
	case []any:
		if len(t) == 0 {
			return nil, false
		}
		out := make([]string, 0, len(t))
		for _, e := range t {
			s, ok := e.(string)
			if !ok || s == "" {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	case []string:
		if len(t) == 0 {
			return nil, false
		}
		for _, s := range t {
			if s == "" {
				return nil, false
			}
		}
		return append([]string(nil), t...), true
	}
	return nil, false
}

// ProfileSets lowers a decoded profile table — YOLO_USE_PROFILES in the jail, the config
// `profile` key folded at the host (config.FoldProfiles) — to CLI name → active set, in order. ProfileTable's twin, with the
// same null and malformed-value rule, so the two agree about which agents select anything.
func ProfileSets(m *jsonx.OrderedMap) map[string][]string {
	if m == nil {
		return nil
	}
	var out map[string][]string
	for _, k := range m.Keys() {
		v, _ := m.Get(k)
		set, ok := ProfileSetValue(v)
		if !ok {
			continue
		}
		if out == nil {
			out = map[string][]string{}
		}
		out[k] = set
	}
	return out
}

// ProfileSetWire is the value one set crosses the launcher↔jail contract as: the plain string
// for a set of one, so a launch whose every set has one entry writes the table it always wrote
// and needs no new contract tag (AP-D8), and an array for more.
func ProfileSetWire(set []string) any {
	switch len(set) {
	case 0:
		return "" // `-p <cli>=`, the selection of nothing it always was
	case 1:
		return set[0]
	}
	out := make([]any, len(set))
	for i, s := range set {
		out[i] = s
	}
	return out
}

// SplitProfileList splits a comma-separated profile list — the `-p` spelling of a set, carried
// verbatim in run.Options — into its entries. A profile name may not contain a comma (both
// schemas refuse one), so the split is the list's one reading. Empty elements are kept, for the
// parser to refuse by position; every other reader receives a list the parser already checked.
func SplitProfileList(v string) []string {
	if v == "" {
		return nil
	}
	return strings.Split(v, ",")
}

// HoldsProviderSets reports whether the pack among packs that installs agent's CLI declares
// `provider_sets` on that program: whether agent may hold a set of more than one (AP-D2). False
// for an agent no pack in packs installs, which has no derive to hand a set to.
func HoldsProviderSets(packs []*Pack, agent string) bool {
	owner := binOwner(packs, agent)
	return owner != nil && owner.Decl != nil && owner.Decl.HoldsProviderSets(agent)
}

// BareListNote is the one launch line OQ-AP3 rules for a bare list: which agents took it whole
// and which, taking one profile because their packs do not declare provider_sets, start on its
// first entry and ignore the rest. It says what yolo can hand an agent, never what the agent can
// hold: oh-omp can hold several providers (docs/design/active-provider-sets.md §3) and still takes
// one profile until its pack declares provider_sets.
// "" when the list has one entry or no agent was narrowed, so a bare list every receiver holds
// says nothing it has not already said in the profile lines. keyed says where the list was
// written — the `profile` key's string, list or "*" form (true) or a bare `-p` (false) — so the
// line names its source and the per-agent spelling of the same list there.
func BareListNote(list, whole, narrowed []string, keyed bool) string {
	if len(list) <= 1 || len(narrowed) == 0 {
		return ""
	}
	whole = sortedCopy(whole)
	narrowed = sortedCopy(narrowed)
	source, remedy := "a bare -p, naming no agent", "-p <agent>="+strings.Join(list, ",")
	if keyed {
		quoted := make([]string, len(list))
		for i, name := range list {
			quoted[i] = fmt.Sprintf("%q", name)
		}
		source = "the profile key's list, naming no agent"
		remedy = `"profile": {"<agent>": [` + strings.Join(quoted, ", ") + `]}`
	}
	line := fmt.Sprintf("Profile list %s (%s): %s %s one profile (%s "+
		"not declare provider_sets), so %s on %s alone and %s %s", strings.Join(list, ", "), source,
		joinAnd(narrowed), plural(len(narrowed), "takes", "take"),
		plural(len(narrowed), "its pack does", "their packs do"),
		plural(len(narrowed), "it starts", "each starts"), list[0],
		plural(len(narrowed), "ignores", "ignore"), joinAnd(list[1:]))
	if len(whole) > 0 {
		line += fmt.Sprintf("; %s %s the whole list", joinAnd(whole), plural(len(whole), "takes", "take"))
	}
	return line + ". Name an agent to give it a list of its own: " + remedy + "."
}

// SingleProviderSetRefusal is OQ-AP2's refusal (ruled 2026-09-29, option A), the one wording at
// every notch and in config validation: a list NAMED at an agent whose pack does not declare
// provider_sets, naming the agent, why, and the one-entry spellings that work. The why is the
// declaration yolo reads, never a claim about the agent: claude, codex and copilot run one
// provider per session, but oh-omp can hold several and is refused only because its pack does
// not declare it yet (docs/design/active-provider-sets.md §3).
func SingleProviderSetRefusal(agent string, set []string) string {
	return fmt.Sprintf("profiles %s are selected for %s, whose pack does not declare "+
		"provider_sets, so yolo cannot hand it a list: it would start %s on %s and drop %s in "+
		"silence — select one profile for it: `-p %s=%s` for one launch, or "+
		"`\"profile\": {%q: %q}` in your config", strings.Join(set, ", "), agent, agent,
		set[0], joinAnd(set[1:]), agent, set[0], agent, set[0])
}

// ProfileSetProblems is every refusal an effective set table earns once its names resolve
// (AP-D3, AP-D7 and OQ-AP2), one line per problem, agents in name order. sets is the CLI-keyed
// table (ProfileSets); resolved is the launch's resolved profile table, which carries each
// entry's provider and via. An agent no pack in packs installs is skipped: validation and the
// typed-pair check refuse its key on their own terms, and it has no derive to hand a set to.
//
// Undeclared names are NOT here: the declaration check refuses each one before resolution,
// naming the entry, so this reads only names that resolve.
//
//   - A name listed twice is refused, naming it.
//   - A set of more than one at an agent that does not declare provider_sets is refused
//     (SingleProviderSetRefusal). A bare list never reaches here in that shape: the caller
//     narrowed it (config.FoldProfiles), so a list left at such an agent was named at it.
//   - Two entries resolving to ONE provider are refused, naming both: one provider has one
//     catalog row and one key, so two option sets for it mean nothing once a session switches.
//   - Two entries on ONE REGIONAL PLATFORM are refused, naming both (AP-D12): a platform some
//     pack says is reached through a region (a provider declaring `platform` and
//     `region_env_name`, regionRequirements) is read from variables of the agent's process, and
//     a process has one AWS_REGION, so two Bedrock providers in one set would share one region
//     and one credential chain. providers is the composed table the entries' platforms are read
//     off; nil asks nothing of platforms.
//   - A via entry anywhere but first is refused (AP-D9, this build's narrowing of AP-D7's "at
//     most one per set"): an agent has ONE via route, whose upstream is the provider its
//     primary resolves to, so a via entry must be the primary to be routed at all. Being first
//     also makes it the only one, which is AP-D7's limit.
func ProfileSetProblems(packs []*Pack, providers *jsonx.OrderedMap, sets map[string][]string,
	resolved map[string]ResolvedProfile) []string {
	regional := regionRequirements(packs)
	agents := make([]string, 0, len(sets))
	for agent := range sets {
		agents = append(agents, agent)
	}
	sort.Strings(agents)
	var problems []string
	for _, agent := range agents {
		set := sets[agent]
		if binOwner(packs, agent) == nil || len(set) == 0 {
			continue
		}
		seen := map[string]bool{}
		dup := false
		for _, name := range set {
			if seen[name] {
				problems = append(problems, fmt.Sprintf("profile %q is listed twice in %s's "+
					"profiles (%s) — a set names each profile once", name, agent, strings.Join(set, ", ")))
				dup = true
				break
			}
			seen[name] = true
		}
		if dup || len(set) == 1 {
			continue
		}
		if !HoldsProviderSets(packs, agent) {
			problems = append(problems, SingleProviderSetRefusal(agent, set))
			continue
		}
		byProvider := map[string]string{}
		for _, name := range set {
			provider := ProviderFor(resolved, name)
			if provider == "" {
				continue
			}
			if first, twice := byProvider[provider]; twice {
				problems = append(problems, fmt.Sprintf("profiles %q and %q in %s's profiles both "+
					"resolve to provider %q — one provider has one catalog row and one key, so a "+
					"set names each provider once: keep one of them", first, name, agent, provider))
				continue
			}
			byProvider[provider] = name
		}
		byPlatform := map[string]string{}
		for _, name := range set {
			provider := ProviderFor(resolved, name)
			platform := entryString(providerEntry(providers, provider), "platform")
			req, isRegional := regional[platform]
			if provider == "" || !isRegional {
				continue
			}
			first, twice := byPlatform[platform]
			if twice && ProviderFor(resolved, first) != provider {
				problems = append(problems, fmt.Sprintf("profiles %q and %q in %s's profiles are "+
					"both on platform %q, which pack %s says is reached through a region: %s's "+
					"process carries one %s and one credential chain for it, so a set names that "+
					"platform once: keep one of them", first, name, agent, platform, req.pack, agent,
					orList(req.vars)))
				continue
			}
			if !twice {
				byPlatform[platform] = name
			}
		}
		for i, name := range set {
			if i == 0 {
				continue
			}
			if via := resolved[name].Via; via != "" {
				problems = append(problems, fmt.Sprintf("profile %q (entry %d of %s's profiles: %s) "+
					"routes through %q, and a via profile can sit in a set only as its first entry — "+
					"%s has one via route, whose upstream is the provider the first entry resolves "+
					"to: list it first (-p %s=%s) or leave it out", name, i+1, agent,
					strings.Join(set, ", "), via, agent, agent, strings.Join(moveFirst(set, i), ",")))
			}
		}
	}
	return problems
}

// ActiveSetFor is the derive input for one agent's set (ctx.active_set,
// docs/design/active-provider-sets.md §4.3): each entry's profile name, the provider it resolves
// to, its resolved option map and its own model-list switch (ModelsEnforced), in set order. Nil
// for no set. The option maps are the resolved table's own, never copied by a caller that edits
// them.
func ActiveSetFor(set []string, resolved map[string]ResolvedProfile) []luahook.SetEntry {
	if len(set) == 0 {
		return nil
	}
	out := make([]luahook.SetEntry, 0, len(set))
	for _, name := range set {
		e := luahook.SetEntry{ProfileName: name, Provider: ProviderFor(resolved, name),
			ModelsNotEnforced: !ModelsEnforced(resolved[name])}
		if r, ok := resolved[name]; ok && r.Options != nil {
			e.Profile = r.Options
		}
		out = append(out, e)
	}
	return out
}

// SetPosition names where provider sits in the active sets of more than one entry the gate
// composed, for a refusal about that one provider (§4.5: "naming the entry, its position and the
// agent"): "profile openrouter is entry 2 of pi's profiles (zai, openrouter)", one clause per
// agent in name order. "" when no such set holds it, which is every launch whose sets all have
// one entry, so their refusals read as they always did.
func (s *CredentialScope) SetPosition(provider string) string {
	sets := s.Sets()
	agents := make([]string, 0, len(sets))
	for agent := range sets {
		agents = append(agents, agent)
	}
	sort.Strings(agents)
	var parts []string
	for _, agent := range agents {
		d := s.agents[agent]
		for i, p := range d.setProviders() {
			if p == provider && i < len(d.Set) {
				parts = append(parts, fmt.Sprintf("profile %s is entry %d of %s's profiles (%s)",
					d.Set[i], i+1, agent, strings.Join(d.Set, ", ")))
			}
		}
	}
	return strings.Join(parts, "; ")
}

// moveFirst is set with entry i moved to the front, the rest in order.
func moveFirst(set []string, i int) []string {
	out := []string{set[i]}
	for j, name := range set {
		if j != i {
			out = append(out, name)
		}
	}
	return out
}

// sortedCopy is names sorted, leaving the caller's slice alone.
func sortedCopy(names []string) []string {
	out := append([]string(nil), names...)
	sort.Strings(out)
	return out
}

// joinAnd renders names as prose: "a", "a and b", "a, b and c".
func joinAnd(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// plural picks the singular or plural spelling for n.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
