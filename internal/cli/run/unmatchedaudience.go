package run

// unmatchedaudience.go is the JAIL launch's half of risk R1
// (docs/reference/agent-briefings.md#ba-r1): an ADDRESSED contribution — content that names an
// audience with `agents` instead of a destination with `into` — whose audience matches no
// destination of its own kind in this jail. Such content is composed into nothing, and its
// author believes it was delivered.
//
// A REPORT, NEVER A REFUSAL, and that is the ruling rather than a softness. By the time this
// runs the eighth pre-flight (packload.AgentAudienceProblems) has already refused every name
// that is not in this jail's vocabulary, so every name reaching here IS owned by a selected
// pack. The addressing pack's `agents` is therefore correct, and what is missing is an `agent`
// on the OWNING pack's destination of that kind: refusing would punish the wrong author
// (agent-briefings.md, "Two severities"). `yolo host apply` prints the same fact as a `no effect`
// warning at exit status 0 (reportInferredDestinations in internal/cli); this is the line the
// jail launch used to lack.
//
// IT READS THE JAIL'S OWN DESTINATION ENUMERATIONS, NOT packload.ResolveDestinations. The jail
// never calls ResolveDestinations — it composes each destination out of the whole pack set,
// filtering by the destination's declared identity (jailbriefingaudience_test.go's header has
// why the two notches' mechanisms differ) — so an answer computed the host's way could say
// "delivered" about content the jail composes into nothing, or the reverse. Each kind is asked
// of the enumeration the jail actually delivers through:
//
//   - `briefing`: briefingDestinations — the SAME deduplicated list refreshJailBriefings composes
//     and assembleRunCmd mounts, so a second pack's identity at an already-claimed path is not a
//     destination here either, exactly as it is not one at composition.
//   - `skills`: packSkillTargets — the targets jailcontent.PrepareSkills stages into, pinned
//     against the stager itself (its per-target audience filter included) by
//     TestUnmatchedSkillsAudienceAgreesWithSkillStaging.
//   - `files`: the slot rule packFilesTargets applies (an `agent` + `into` `files` contribution),
//     pinned against that function by TestUnmatchedFilesAudienceAgreesWithPackFilesTargets.
//
// The sources are packload.GovernedSources for the two kinds that have a convention, the ONE
// governance reader (pack-system.md#one-governance-reader), so a file this report names is a
// file the jail would have delivered had a destination matched.
import (
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// reportUnmatchedAudiences prints one warning per addressed contribution in the selected set
// whose audience reaches no destination of its kind (unmatchedAudiences). Called once per
// launch, from stagePacks, after the vocabulary pre-flight and before anything is delivered.
func (o *Options) reportUnmatchedAudiences(packs []*packload.Pack) {
	for _, line := range unmatchedAudiences(packs) {
		o.pr(o.Stdout).print("[yellow]Warning: " + line + "[/yellow]")
	}
}

// unmatchedAudiences returns one report line per addressed content contribution in `packs`
// whose `agents` names no destination of its kind this jail declares, in pack order and then
// kind order (briefing, skills, files). A contribution that reaches at least one destination
// is not reported, however many of its names matched nothing: that is `yolo host apply`'s
// granularity too (an AddressedDelivery with a non-empty Into prints no warning).
//
// Problems GovernedSources reports (a declared `from` that cannot be read) are deliberately
// dropped here: packBriefingProses and packSkillSourceDirs have already printed each of them
// on this launch, and this reader asks a different question about the sources that DO exist.
func unmatchedAudiences(packs []*packload.Pack) []string {
	dests := map[packdecl.Kind]map[string]bool{
		packdecl.KindBriefing: {},
		packdecl.KindSkills:   {},
		packdecl.KindFiles:    {},
	}
	for _, d := range briefingDestinations(packs) {
		if d.Agent != "" {
			dests[packdecl.KindBriefing][d.Agent] = true
		}
	}
	for _, t := range packSkillTargets(packs) {
		if t.Agent != "" {
			dests[packdecl.KindSkills][t.Agent] = true
		}
	}
	for agent := range filesSlotAgents(packs) {
		dests[packdecl.KindFiles][agent] = true
	}

	var lines []string
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, kind := range []packdecl.Kind{packdecl.KindBriefing, packdecl.KindSkills} {
			sources, _ := p.GovernedSources(kind)
			// One line per GOVERNING contribution, naming every source it governs: a
			// `{"kind":"briefing","agents":["x"]}` with no `from` governs every unnamed
			// briefing/*.md, and one mistake is one line.
			type group struct {
				agents []string
				rels   []string
			}
			var order []string
			groups := map[string]*group{}
			for _, src := range sources {
				if len(src.By.Agents) == 0 || reachesAny(src.By.Agents, dests[kind]) {
					continue
				}
				key := src.By.SourceKey() + "\x00" + strings.Join(src.By.Agents, "\x00")
				g, seen := groups[key]
				if !seen {
					g = &group{agents: src.By.Agents}
					groups[key] = g
					order = append(order, key)
				}
				g.rels = append(g.rels, src.Rel)
			}
			for _, key := range order {
				g := groups[key]
				sort.Strings(g.rels)
				lines = append(lines, unmatchedAudienceLine(p.Name, kind, g.rels, g.agents))
			}
		}
		for _, c := range p.Decl.Contributions() {
			if c.Kind != packdecl.KindFiles || c.Agent != "" || c.Into != "" || len(c.Agents) == 0 {
				continue
			}
			if reachesAny(c.Agents, dests[packdecl.KindFiles]) {
				continue
			}
			lines = append(lines, unmatchedAudienceLine(p.Name, c.Kind, []string{c.From}, c.Agents))
		}
	}
	return lines
}

// filesSlotAgents is the set of agents that declare a `files` DESTINATION — the slot an
// addressed `files` contribution lands under — by the rule packFilesTargets' alias table
// applies: a `files` contribution carrying both `agent` and `into`.
func filesSlotAgents(packs []*packload.Pack) map[string]bool {
	out := map[string]bool{}
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, c := range p.Decl.Contributions() {
			if c.Kind == packdecl.KindFiles && c.Agent != "" && c.Into != "" {
				out[c.Agent] = true
			}
		}
	}
	return out
}

// reachesAny reports whether any name in the audience owns a destination in dests.
func reachesAny(agents []string, dests map[string]bool) bool {
	for _, a := range agents {
		if dests[a] {
			return true
		}
	}
	return false
}

// unmatchedAudienceLine is the warning text. It names the pack, the kind, the pack-relative
// sources and the audience, and states the remedy the way `yolo host apply` does — with the one
// difference a launch allows: the pre-flight has already proved every name here is owned by a
// selected pack, so "select the pack" is not a remedy at this point and is not offered.
func unmatchedAudienceLine(pack string, kind packdecl.Kind, sources, agents []string) string {
	quoted := make([]string, 0, len(sources))
	for _, s := range sources {
		quoted = append(quoted, "`"+s+"`")
	}
	return fmt.Sprintf("pack %s: %s from %s is addressed to %s (its `agents` selector), and no %s "+
		"destination in this jail declares a matching `agent`, so this launch delivers it to no "+
		"agent. The pack that owns %s has to declare one (an `agent` beside its own `into`), or "+
		"correct `agents` in %s's pack.json; declaring `into` is not the remedy: a contribution "+
		"names an audience or a destination, never both",
		pack, kind, strings.Join(quoted, ", "), quotedAgents(agents), kind, quotedAgents(agents), pack)
}

// quotedAgents renders an audience: the names quoted, joined with "or", because the selector
// is an allowlist and any ONE of them matching would have routed the content. The same
// rendering `yolo host apply`'s report uses (internal/cli's quotedAgents), so the two notches
// print one audience one way.
func quotedAgents(agents []string) string {
	quoted := make([]string, 0, len(agents))
	for _, a := range agents {
		quoted = append(quoted, fmt.Sprintf("%q", a))
	}
	if len(quoted) < 2 {
		return strings.Join(quoted, "")
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " or " + quoted[len(quoted)-1]
}
