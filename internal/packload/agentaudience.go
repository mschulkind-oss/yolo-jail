package packload

// agentaudience.go is the P3 refusal: an `agents` selector may name only an agent THIS JAIL
// HAS, and anything else is fatal (briefing-audiences.md §4.3, OQ-BA3).
//
// # The one question this file answers, and the one it does not
//
// The design contains two sentences that look like they disagree, and roadmap 💬 20 records
// them as a live question: P3 makes an unenabled NAME fatal, while risk R1 describes an
// addressed contribution that matched no destination as REPORTED. Both are kept, because they
// are answers to two different questions and only one of them is this file's:
//
//   - IS THE NAME IN THE VOCABULARY? "Does any enabled pack claim an agent called `claude`?"
//     Decidable from the claim set alone, and FATAL when the answer is no — this file. It is
//     the question P3 is about, and it is why `cloude` and `codex`-in-a-jail-without-codex
//     fail the same way with the same remedy: fix the name, or select the pack.
//   - DID THE NAME REACH A DESTINATION OF THIS KIND? "The pack that owns `claude` is here, but
//     does it declare a `skills` destination carrying `agent: claude`?" That can be no for a
//     perfectly good name, and it is R1's question — REPORTED, through
//     Destinations.Addressed/Orphaned, where the remedy is a line in the owning pack rather
//     than a line in the addressing one.
//
// Read together they are one rule with two severities, split where the user's remedy splits:
// a name nobody provides is the addressing author's mistake; a destination the owner never
// declared an identity for is the owning pack's gap (R4), and refusing the launch over it
// would punish the wrong author.

import (
	"fmt"
	"sort"
	"strings"
)

// AgentAudienceProblems reports every `agents` selector in `packs` that names an agent this
// pack set does not have. One message per (pack, kind, offending name), in a deterministic
// order, each naming the four things R3 asks a diagnostic for: the offending string, the
// declaring pack, the candidate list, and a did-you-mean.
//
// EMPTY MEANS THE LAUNCH MAY PROCEED. The caller decides the severity — every caller makes it
// fatal today, and §4.3 says why the gate lands at the launch pre-flight and `yolo host apply`
// rather than at `yolo pack lint`: lint takes a single pack root with NO config, so it cannot
// know the enabled set and must not pretend to (R5 — move the gate, do not lower the severity).
func AgentAudienceProblems(packs []*Pack) []string {
	have := AgentNames(packs)
	known := map[string]bool{}
	for _, n := range have {
		known[n] = true
	}
	type problem struct{ pack, kind, name string }
	var found []problem
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		// Over EVERY contribution rather than a kind list, which is what keeps this total:
		// validateContribution refuses `agents` on anything but `briefing` and `skills`
		// (ahead of its kind switch, so a kind added tomorrow inherits the refusal), so a
		// contribution of any other kind cannot carry one — and if one somehow did, checking
		// it is the safe direction.
		for _, c := range p.Decl.Contributions() {
			for _, a := range c.Agents {
				if a == "" || known[a] {
					// Empty is the validator's problem, not this gate's
					// (validateContribution: "empty agent name"), and reporting it twice
					// would send the author looking for a pack to select.
					continue
				}
				found = append(found, problem{p.Name, string(c.Kind), a})
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
		return found[i].name < found[j].name
	})
	var out []string
	seen := map[problem]bool{}
	for _, f := range found {
		if seen[f] {
			continue // one message per mistake, even if the manifest repeats it
		}
		seen[f] = true
		out = append(out, agentAudienceMessage(f.pack, f.kind, f.name, have))
	}
	return out
}

// agentAudienceMessage is the refusal one bad name earns. It has to work for a user who has
// never read the design doc, so it names the string, the pack, the whole candidate list and a
// guess — and it gives BOTH remedies, because "fix the typo" and "select the pack" are the two
// things that could have gone wrong and the message cannot tell which (P3: they are the same
// mistake from the jail's point of view).
//
// NOTCH-NEUTRAL WORDING, deliberately. Both gates print this string, and one of them is
// `yolo host apply`, where there is no jail — an earlier draft said "this jail's agents" and
// was measurably wrong at the host notch. `packs` is the right subject either way: it is the
// one list the user edits, and it is the same key at both notches.
func agentAudienceMessage(pack, kind, name string, have []string) string {
	msg := fmt.Sprintf("pack %s: %s `agents` names %q, which no pack in `packs` provides",
		pack, kind, name)
	if guess := nearestAgentName(name, have); guess != "" {
		msg += fmt.Sprintf(" — did you mean %q?", guess)
	}
	if len(have) == 0 {
		return msg + ". Your `packs` provide NO agents at all, so no audience can be named: " +
			"add the pack that provides " + fmt.Sprintf("%q", name) + ", or drop the " +
			"`agents` selector to reach every destination"
	}
	return msg + fmt.Sprintf(". Agents your `packs` provide: %s. Fix the name, or add the "+
		"pack that provides %q", strings.Join(have, ", "), name)
}

// nearestAgentName is the did-you-mean §4.3 asks for: the closest candidate within an
// edit-distance budget that scales with the name's length, or "" when nothing is close enough
// to be worth guessing at.
//
// THIS IS THE SECOND LEVENSHTEIN IN THE TREE, and the duplication is stated rather than
// hidden: internal/loopholes/supersede.go has the first, feeding the same shape of message
// ("supersedes capability X, which no loophole serves — did you mean Y?"). They are not shared
// because packload cannot import loopholes (loopholes → config → packload is a cycle), and the
// right fix is a leaf package both import, which is a change of its own rather than a rider on
// this one. Budget and tie-breaking are deliberately identical, so the two messages guess the
// same way.
func nearestAgentName(want string, candidates []string) string {
	budget := len(want) / 3
	if budget > 4 {
		budget = 4
	}
	if budget < 1 {
		return ""
	}
	best, bestDist := "", budget+1
	for _, c := range candidates {
		if d := agentNameEditDistance(want, c); d < bestDist {
			best, bestDist = c, d
		}
	}
	if bestDist > budget {
		return ""
	}
	return best
}

// agentNameEditDistance is plain Levenshtein over runes, two rows at a time. Agent names are
// short, so the straightforward version is the right one.
func agentNameEditDistance(a, b string) int {
	ar, br := []rune(a), []rune(b)
	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = minOf3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(br)]
}

func minOf3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}
