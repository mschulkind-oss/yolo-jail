package packload

// agentaudience.go answers FS-D6 (docs/design/pack-files-for-agent-state.md#FS-D6, §4.2): an
// `agents` selector naming an agent no SELECTED pack provides is SKIPPED — the contribution
// delivers nothing for that name and the launch runs. It used to be the P3 refusal; the
// maintainer ruled it out on 2026-10-31: "There's no way to know what packs could be selected.
// The premise is false. We just need to skip by default. There's no way to warn about the typo.
// You can't have both."
//
// # The one question this file answers, and the one it does not
//
//   - IS THE NAME IN THE SELECTED VOCABULARY? "Does any selected pack claim an agent called
//     `agy`?" Decidable from the claim set alone. When the answer is no, the content addressed to
//     that name is SKIPPED and the skip is disclosed — this file. A misspelling and a real agent
//     the user did not select read the same way, because yolo cannot tell them apart: a name
//     outside the selection may belong to a pack yolo has never seen.
//   - DID THE NAME REACH A DESTINATION OF THIS KIND? "The pack that owns `claude` is here, but
//     does it declare a `skills` destination carrying `agent: claude`?" That is R1's question —
//     REPORTED, through Destinations.Addressed/Orphaned, where the remedy is a line in the owning
//     pack. It is asked only of names in the vocabulary; a skipped name is not reported twice.

import (
	"fmt"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// AudienceSkip is one (pack, kind, agent) whose content is not delivered because no selected
// pack provides the agent (FS-D6).
type AudienceSkip struct {
	Pack  string        // the addressing pack
	Kind  packdecl.Kind // the contribution kind (briefing, skills, files)
	Agent string        // the agent as the manifest wrote it
	// Provider is the pack yolo SHIPS that provides Agent, or "" when none does. Read for
	// advice only: it reserves nothing and refuses nothing (pack-files-for-agent-state.md §6.2).
	Provider string
}

// Line is the disclosure: what was skipped and why. `yolo check` prints it as a [SKIP] row and
// the launch and `yolo host apply` print it beside their pack lines.
func (s AudienceSkip) Line() string {
	return fmt.Sprintf("pack %s: %s for %s not delivered — no selected pack provides %s",
		s.Pack, s.Kind, s.Agent, s.Agent)
}

// NextStep is the step that makes the content apply, or "" when yolo cannot name one: only a
// pack yolo ships is known to provide a name, and any other name — a misspelling included —
// stands alone, as written.
func (s AudienceSkip) NextStep() string {
	if s.Provider == "" {
		return ""
	}
	return "If you want it here:  yolo pack add " + s.Provider
}

// AudienceKnown reports whether any name in `agents` is in `vocab` (AgentNames of the selected
// set, as a set). A contribution none of whose names is known was skipped whole, so the R1
// reports ask it before calling an addressed contribution unmatched.
func AudienceKnown(agents []string, vocab map[string]bool) bool {
	for _, a := range agents {
		if vocab[a] {
			return true
		}
	}
	return false
}

// AgentVocabulary is AgentNames as a set.
func AgentVocabulary(packs []*Pack) map[string]bool {
	out := map[string]bool{}
	for _, n := range AgentNames(packs) {
		out[n] = true
	}
	return out
}

// AgentAudienceSkips returns every `agents` name in `selected` that no selected pack provides,
// one per (pack, kind, name), in a deterministic order. `shipped` is the set yolo ships
// (packload.Embedded() at every caller), read only to name a provider in the next step.
//
// EMPTY MEANS EVERY ADDRESSED NAME IS HERE. Every caller DISCLOSES each skip — the launch in its
// pack lines, `yolo host apply` in its report, `yolo check` as a [SKIP] row — and none refuses.
func AgentAudienceSkips(selected, shipped []*Pack) []AudienceSkip {
	known := AgentVocabulary(selected)
	providers, _ := agentNameClaims(shipped)
	type key struct {
		pack  string
		kind  packdecl.Kind
		agent string
	}
	var found []key
	for _, p := range selected {
		if p == nil || p.Decl == nil {
			continue
		}
		// Over EVERY contribution rather than a kind list: validateContribution refuses `agents`
		// on anything but `briefing`, `skills` and `files`, so a kind added tomorrow inherits
		// the refusal there, and this reader stays total.
		for _, c := range p.Decl.Contributions() {
			for _, a := range c.Agents {
				if a == "" || known[a] {
					// Empty is the validator's problem (validateContribution: "empty agent name").
					continue
				}
				found = append(found, key{p.Name, c.Kind, a})
			}
		}
	}
	sort.SliceStable(found, func(i, j int) bool {
		if found[i].pack != found[j].pack {
			return found[i].pack < found[j].pack
		}
		if found[i].kind != found[j].kind {
			return found[i].kind < found[j].kind
		}
		return found[i].agent < found[j].agent
	})
	var out []AudienceSkip
	seen := map[key]bool{}
	for _, f := range found {
		if seen[f] {
			continue // one line per skip, even if the manifest repeats it
		}
		seen[f] = true
		s := AudienceSkip{Pack: f.pack, Kind: f.kind, Agent: f.agent}
		if claims := providers[f.agent]; len(claims) > 0 {
			s.Provider = claims[0].pack
		}
		out = append(out, s)
	}
	return out
}
