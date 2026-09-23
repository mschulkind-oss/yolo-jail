package packload

// skillssource.go resolves a `skills` contribution's SOURCE — the pack-relative dir its
// skills are read from.
//
// It exists because `from` was accepted and silently ignored on `skills`. All three readers
// hardcoded the conventional dir (`internal/cli/run/packs.go` twice, on the embedded and the
// configured staging path, and `internal/cli/applyhostskills.go` at the host notch), so a
// pack declaring `{"kind":"skills","from":"my-skills","into":".claude/skills"}` had `skills/`
// read instead — no warning, no line in any report, and `pack lint` said the manifest was
// fine. A declaration yolo accepts and ignores is the class of defect the pack system refuses
// everywhere else (internal/render's FieldSet exists so an inapplicable kind is REFUSED by
// name rather than skipped in silence).
//
// ONE resolver for all three, deliberately: three copies of "read <root>/skills" is how the
// field came to be ignored in the first place, and a fourth reader added later would inherit
// the same bug.
//
// THE PRECEDENCE DOES NOT MATCH `briefing`'s, and this header used to say it did while it did
// not (docs/reference/pack-system.md#briefing-p4): briefing's `from` was a fallback chain to AGENTS.md, and
// skills' has always been the only source. Since that design they do agree — a declared source
// is the ONLY source, for both kinds (P4) — and WHICH sources a pack delivers, and who governs
// each, is governance.go's answer for both kinds rather than a gate each reader keeps.

import (
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// SkillsSourceDir resolves ONE skills contribution to an absolute source directory in this
// pack's tree, plus a problem string when the declaration cannot be honored.
//
// Three outcomes, and there is deliberately no fourth:
//
//   - a readable directory → returned, no problem;
//   - a NON-CONVENTIONAL source that is absent, not a directory, or escapes the pack tree →
//     "", with a problem naming it. The author named a specific path and got nothing, so
//     saying nothing would just move the silent-ignore bug one level down;
//   - the CONVENTIONAL dir absent → "", NO problem, whether `from` spelled it out or not.
//
// That last line is where the noise/signal boundary sits, and it is drawn at the CONVENTION
// rather than at "was `from` written down". All six packs yolo ships declare
// `from: "skills"` and carry no skills of their own — their contribution exists to name the
// destination other packs merge into (hostskills.Deliver says exactly this) — so keying the
// warning on `from != ""` would fire it on every launch and every apply of a stock config,
// which is how a warning stops being read. And an explicit `from: "skills"` is
// indistinguishable in intent from an omitted one: both say "the usual place". A source the
// author had to invent is the case where absence is evidence of a mistake.
//
// The containment check is the same one hostBriefingProse makes, for the same reason and with
// the same reach: `from` is manifest data, packdecl.Validate rejects ".." at the authoring
// boundary, but a caller may hold a pack whose Decode problems it discarded — `yolo host apply`
// reads a pack through resolveConfiguredPack (internal/cli), which does exactly that. It is
// lexical, so it bounds a declared path and not a symlink inside the tree; on the jail path
// packstage has already refused escaping symlinks, and on the host path an unstaged tree is
// either a local pack the user pointed at themselves or a fetched one that resolver has run
// through packstage's own refusal before reading it in place.
//
// A DESTINATION (`agent` set) SOURCES NOTHING and returns "", "" (docs/reference/pack-system.md#briefing-p5):
// an agent pack's `{agent, into}` names where content lands, and reading the pack's own skills/
// through it made the destination line double as a delivery. An agent pack's skills/ still ships
// — as the implicit broadcast (GovernedSources), which reaches its own destination like any other.
func (p *Pack) SkillsSourceDir(c packdecl.Contribution) (string, string) {
	if c.Agent != "" {
		return "", ""
	}
	return p.skillsDir(c.SkillsSource())
}

// SkillsSource is one resolved skills source: the absolute directory, and the AUDIENCE the
// contribution that named it declared.
//
// The audience travels WITH the source because that is the only place it can travel: the jail
// merges every selected pack's skills into every declared destination through one global list
// (jailcontent's packSkillDirs), so a source that arrived as a bare path had no way to say who
// it was for — which is `skills`' half of the defect the audience selector (docs/reference/agent-briefings.md#audiences-what-varies-per-destination) closes, and it is
// the same shape jailcontent.PackBriefing needed for `briefing`.
type SkillsSource struct {
	// Dir is the absolute source directory.
	Dir string
	// Agents is the audience the declaring contribution named. EMPTY MEANS BROADCAST — the
	// pre-field behavior, and the only behavior a zero-ceremony pack can ask for (P2).
	Agents []string
}

// SkillsSources is the resolved sources, in declaration order with the implicit one last, plus
// one problem per declaration that could not be honored — GovernedSources(KindSkills), carrying
// each governor's audience.
//
// THERE IS NO `declared` GATE any more (docs/reference/pack-system.md#briefing-governance). The conventional skills/
// tree is ONE unit: it broadcasts implicitly unless some content contribution names it, and a
// contribution naming a DIFFERENT tree (`{from: "extra-skills", agents: ["pi"]}`) no longer
// switches it off. A destination (`agent` set) names nothing. So a pack that is just a `skills/`
// tree needs no manifest at all, and one that adds a narrower tree beside it keeps the broad one.
//
// Deduplicated by DIR, with the audiences UNIONED and a broadcast absorbing every audience, as a
// fallback only: two content contributions naming one source are refused on the strict path
// (OQ-PB5), but two distinct keys can still resolve to one directory (a symlink), and copying
// that tree twice would be one delivery reported as two.
func (p *Pack) SkillsSources() (sources []SkillsSource, problems []string) {
	governed, problems := p.GovernedSources(packdecl.KindSkills)
	index := map[string]int{}
	for _, g := range governed {
		if i, seen := index[g.Abs]; seen {
			if len(sources[i].Agents) == 0 || len(g.By.Agents) == 0 {
				sources[i].Agents = nil
				continue
			}
			sources[i].Agents = append(sources[i].Agents, g.By.Agents...)
			continue
		}
		index[g.Abs] = len(sources)
		sources = append(sources, SkillsSource{Dir: g.Abs, Agents: append([]string(nil), g.By.Agents...)})
	}
	return sources, problems
}
