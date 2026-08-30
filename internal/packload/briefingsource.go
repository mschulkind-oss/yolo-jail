package packload

// briefingsource.go resolves a `briefing` contribution's SOURCE — the pack-relative file its
// prose is read from. It is skillssource.go's sibling, and it exists for the same reason: a
// `from` that one notch honored and the other ignored.
//
// The divergence was verified rather than inferred (roadmap.md §6a-4): the host
// render built `[from, "AGENTS.md", "CLAUDE.md"]`, while `run.readPackBriefing` took a
// DIRECTORY and scanned the conventional pair unconditionally — so a pack declaring
// `from: "house-rules.md"` briefed at the host and stayed silent in a jail. That is the
// accepted-and-ignored defect `skills` had, in the sibling kind, and the fix is the same one:
// ONE resolver both readers go through, so a third reader cannot inherit a fourth spelling.
//
// packdecl.Contribution.BriefingCandidates() owns the PRECEDENCE (declared `from` first, then
// the convention); this owns reading the pack's tree through it — the same split
// SkillsSource()/SkillsSourceDir() already uses.

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// BriefingProseFor resolves ONE briefing contribution to its prose, right-trimmed, plus a
// problem string when the declaration could not be honored.
//
// THE PRECEDENCE IS A FALLBACK CHAIN, not a single choice, and that is `briefing`'s documented
// difference from `skills`: BriefingCandidates returns `[from, AGENTS.md]` and its
// contract is "the caller reads the first one that exists and is non-empty". So a declared `from`
// that is absent still resolves to the convention — narrowing that here would change what the
// HOST notch has always done, which is the opposite of the convergence this file is for.
//
// What DOES change is that the fallback stops being silent. A non-conventional `from` yolo could
// not read yields a problem NAMING it even when the convention then supplied prose, because that
// is the accepted-and-ignored shape the whole §6a-4 fix is about: the author named a file, got
// somebody else's content, and nothing said so. `skills` resolves this by refusing (SkillsSourceDir
// returns no dir); `briefing` reports and carries on, since its fallback is contractual.
//
// A CONVENTIONAL `from` that is absent is NOT a problem, and the noise/signal line is drawn there
// for SkillsSourceDir's reason: a briefing contribution whose job is to name the DESTINATION other
// packs merge into carries no prose file of its own — all six shipped packs are exactly that shape
// — so warning would fire on every launch and every apply of a stock config, which is how a
// warning stops being read. (They reached that shape by dropping the redundant `from: "AGENTS.md"`
// literals this sentence used to cite; the resolver behaves identically either way, which is the
// point of routing both spellings through BriefingCandidates.)
//
// The containment check is hostBriefingProse's, kept verbatim in intent: `from` is manifest data,
// packdecl.Validate rejects ".." at the authoring boundary, but a caller may hold a pack whose
// Decode problems it discarded (`yolo host apply` reads a local pack through packForCheckDeps, which
// does exactly that). A "../../.ssh/id_rsa" that slipped through would otherwise be copied into a
// file the user reads as INSTRUCTIONS.
func (p *Pack) BriefingProseFor(c packdecl.Contribution) (string, string) {
	root := filepath.Clean(p.Root)
	declaredMissed := c.From != "" && !isConventionalBriefingFile(c.From)
	for _, rel := range c.BriefingCandidates() {
		full := filepath.Clean(filepath.Join(root, filepath.FromSlash(rel)))
		if root != "" && root != "." && !strings.HasPrefix(full, root+string(filepath.Separator)) {
			// Refused outright rather than falling through to the convention, matching
			// SkillsSourceDir: an escaping `from` is not a path that happens to be missing, it is
			// a declaration that must never be resolved at all.
			return "", fmt.Sprintf("pack %s: briefing `from` %q escapes the pack tree — refused",
				p.Name, rel)
		}
		data, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		text := strings.TrimRight(string(data), " \t\r\n")
		if text == "" {
			continue
		}
		if rel == c.From {
			declaredMissed = false
		}
		return text, missingBriefingFromProblem(p.Name, c.From, declaredMissed, text != "")
	}
	return "", missingBriefingFromProblem(p.Name, c.From, declaredMissed, false)
}

// missingBriefingFromProblem is the "your `from` was not read" message, or "" when there is
// nothing to say. `fellBack` distinguishes the two consequences, because they need different
// remedies: prose from the wrong file is a silent substitution, and no prose at all is a pack
// that briefs nothing.
func missingBriefingFromProblem(pack, from string, missed, fellBack bool) string {
	if !missed {
		return ""
	}
	if fellBack {
		return fmt.Sprintf("pack %s declares `briefing` from %q, which is not in its content — "+
			"its conventional AGENTS.md was used instead (check the `from` path, and "+
			"any only/exclude filters)", pack, from)
	}
	return fmt.Sprintf("pack %s declares `briefing` from %q, which is not in its content — no "+
		"prose delivered from it (check the `from` path, and any only/exclude filters)",
		pack, from)
}

// BriefingProse resolves the pack's prose for the JAIL, where one composed briefing is written
// per destination and every pack contributes the same text to all of them.
//
// FIRST NON-EMPTY contribution wins, and that is a real limit rather than a resolution rule:
// the jail's composition takes one (pack, text) pair per pack (jailcontent.ComposePackBriefings), so
// a pack declaring two briefing contributions with two different `from` files cannot deliver
// both there. The host render is per-DESTINATION and does honor both (BriefingProseFor); making
// the jail match would mean composing per destination, which is a larger change than the `from`
// fix this file is.
//
// A pack declaring NO briefing contribution falls back to the convention — the zero-ceremony
// merge both notches depend on (packload.ResolveDestinations infers the destination; this reads
// the content). That fallback lives here for the reason SkillsSourceDirs' does: the call site
// that forgot it would silently drop every manifest-less pack's prose.
func (p *Pack) BriefingProse() (string, []string) {
	var problems []string
	declared := false
	for _, c := range p.Decl.Contributions() {
		if c.Kind != packdecl.KindBriefing {
			continue
		}
		declared = true
		text, prob := p.BriefingProseFor(c)
		if prob != "" {
			problems = append(problems, prob)
		}
		if text != "" {
			return text, problems
		}
	}
	if !declared {
		text, prob := p.BriefingProseFor(packdecl.Contribution{Kind: packdecl.KindBriefing})
		if prob != "" {
			problems = append(problems, prob)
		}
		return text, problems
	}
	return "", problems
}

// isConventionalBriefingFile reports whether rel names one of the conventional briefing files,
// so an explicit `from: "AGENTS.md"` is indistinguishable in intent from an omitted one.
func isConventionalBriefingFile(rel string) bool {
	clean := path.Clean(rel)
	for _, name := range packdecl.DefaultBriefingFiles() {
		if clean == name {
			return true
		}
	}
	return false
}
