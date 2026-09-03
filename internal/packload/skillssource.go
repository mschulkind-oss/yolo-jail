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
// the same bug. The precedence matches `briefing`'s, which already honored `from`
// (entrypoint/hostbriefing.go's hostBriefingProse): the declared value first, the convention
// as the fallback.

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

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
// reads a local pack through packForCheckDeps, which does exactly that. It is lexical, so it
// bounds a declared path and not a symlink inside the tree; on the jail path packstage has
// already refused escaping symlinks, and on the host path an unstaged tree is only ever a
// pack the user pointed at themselves.
func (p *Pack) SkillsSourceDir(c packdecl.Contribution) (string, string) {
	rel := c.SkillsSource()
	root := filepath.Clean(p.Root)
	dir := filepath.Clean(filepath.Join(root, filepath.FromSlash(rel)))
	conventional := path.Clean(rel) == packdecl.DefaultSkillsDir

	if root != "" && root != "." && !strings.HasPrefix(dir, root+string(filepath.Separator)) {
		return "", fmt.Sprintf("pack %s: skills `from` %q escapes the pack tree — refused", p.Name, rel)
	}
	fi, err := os.Stat(dir) // Stat, not Lstat: a symlinked skills dir is legitimate
	switch {
	case err == nil && fi.IsDir():
		return dir, ""
	case conventional:
		// The convention, absent. Normal — see the doc comment.
		return "", ""
	case err != nil:
		return "", fmt.Sprintf("pack %s declares `skills` from %q, which is not in its "+
			"content — no skills delivered from it (check the `from` path, and any "+
			"only/exclude filters)", p.Name, rel)
	default:
		return "", fmt.Sprintf("pack %s declares `skills` from %q, which is a file, not a "+
			"directory — a skills source holds one subdirectory per skill", p.Name, rel)
	}
}

// SkillsSource is one resolved skills source: the absolute directory, and the AUDIENCE the
// contribution that named it declared.
//
// The audience travels WITH the source because that is the only place it can travel: the jail
// merges every selected pack's skills into every declared destination through one global list
// (jailcontent's packSkillDirs), so a source that arrived as a bare path had no way to say who
// it was for — which is `skills`' half of the defect briefing-audiences.md closes, and it is
// the same shape jailcontent.PackBriefing needed for `briefing`.
type SkillsSource struct {
	// Dir is the absolute source directory.
	Dir string
	// Agents is the audience the declaring contribution named. EMPTY MEANS BROADCAST — the
	// pre-field behavior, and the only behavior a zero-ceremony pack can ask for (P2).
	Agents []string
}

// SkillsSources is the resolved sources, in declaration order and deduplicated by DIR, plus
// one problem per declaration that could not be honored.
//
// DEDUPLICATED BY DIR, WITH THE AUDIENCES UNIONED and a broadcast absorbing every audience —
// the same rule run.packBriefingProses applies to identical prose, and needed for the same
// reason: two contributions may name the same source (or omit `from` twice and get the
// conventional dir twice), and copying that tree once per contribution would be one delivery
// reported as several. A broadcast wins because it already reaches everywhere an audience
// could.
//
// A pack declaring NO skills contribution falls back to the conventional dir, which is the
// zero-ceremony merge the jail path depends on: a pack that is just a `skills/` tree and an
// AGENTS.md needs no manifest at all, and its skills still reach whichever agent pack owns
// the destination. That fallback lives here rather than at the call site because both jail
// call sites need it identically, and the one that forgot it would silently drop every
// manifest-less pack's skills.
func (p *Pack) SkillsSources() (sources []SkillsSource, problems []string) {
	index := map[string]int{}
	add := func(c packdecl.Contribution) {
		dir, prob := p.SkillsSourceDir(c)
		if prob != "" {
			problems = append(problems, prob)
		}
		if dir == "" {
			return
		}
		if i, seen := index[dir]; seen {
			if len(sources[i].Agents) == 0 || len(c.Agents) == 0 {
				sources[i].Agents = nil
				return
			}
			sources[i].Agents = append(sources[i].Agents, c.Agents...)
			return
		}
		index[dir] = len(sources)
		sources = append(sources, SkillsSource{Dir: dir, Agents: c.Agents})
	}
	declared := false
	for _, c := range p.Decl.Contributions() {
		if c.Kind != packdecl.KindSkills {
			continue
		}
		declared = true
		add(c)
	}
	if !declared {
		// Zero-ceremony: no manifest (or none mentioning skills) still merges skills/, and it
		// has no manifest to name an audience in, so it broadcasts.
		add(packdecl.Contribution{Kind: packdecl.KindSkills})
	}
	return sources, problems
}
