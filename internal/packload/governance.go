package packload

// governance.go is THE ONE ANSWER to "which of this pack's files does it deliver, and which of its
// contributions decides where each one goes?" (docs/reference/pack-system.md#briefing-governance, #briefing-r5).
//
// Three readers ask that question and all three used to answer it themselves, each behind its own
// `declared` gate: the jail briefing composer (run.packBriefingProses' `if !declared`), the jail
// skills reader (SkillsSources' `if !declared`), and the host notch's zero-ceremony borrower
// (borrowingSources' `!p.declares(kind)`). The three agreed, which is how one trap reached every
// notch: declaring ONE narrow delivery switched off the pack's whole implicit broadcast, so a pack
// that added `{from: "files/pi-rules.md", agents: ["pi"]}` beside its house rules stopped shipping
// the house rules to anyone (pack-system.md#one-governance-reader). Now all three derive from GovernedSources and none keeps a gate
// of its own — a fourth reader added later asks this function or it asks nothing.
//
// THE RULE, per kind:
//
//   - `briefing`: an explicit `from` names exactly that one file. A content contribution that omits
//     `from` names every `briefing/*.md` (one level deep, `*.md` only, exact case) that no other
//     contribution names. A file NOBODY names is Implicit — the P2 broadcast — which therefore
//     exists only when no content contribution omits `from`.
//   - `skills`: an omitted `from` means `skills`, and that tree is ONE unit: Implicit only when no
//     content contribution names it.
//   - `files` governs nothing here: it has no convention to broadcast (pack-system.md#briefing-non-goals).
//
// A DESTINATION (`agent` set) GOVERNS NOTHING AND SOURCES NOTHING (P5). Counting one as an
// omitted-`from` content contribution would switch off every agent pack's own implicit broadcast,
// because every shipped agent pack declares `{agent, into}` with no `from`.
//
// IT READS THE ORIGINAL DECLARATION, never a ResolveDestinations clone's Decl. The clone appends a
// synthesized `{into, from}` copy of each borrower, so reading it would give every explicit `from`
// two governors and turn the implicit borrower into an omitted-`from` declaration — and the
// implicit broadcast would vanish at the host notch only. Pack.origDecl is the pointer the clone
// keeps for exactly this.

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// GovernedSource is one source a pack DELIVERS for a kind, and the one contribution that decides
// where it goes.
type GovernedSource struct {
	// Rel is the cleaned, slash-separated, pack-relative source: "briefing/a.md", "files/pi.md",
	// "skills".
	Rel string
	// By is the ONE governing content contribution, verbatim from the pack's original declaration.
	// When Implicit it is Contribution{Kind: kind}: no `from`, no `into`, no `agents` — the
	// broadcast.
	By packdecl.Contribution
	// Implicit reports that no declaration names this source: the P2 broadcast.
	Implicit bool
	// Abs is the absolute path of the source in the pack's tree — the file for `briefing`, the
	// directory for `skills`.
	Abs string
	// Text is the source's prose, right-trimmed, for `briefing`; empty for `skills`. Never empty
	// for a returned briefing source: a blank file delivers nothing and is not returned.
	Text string

	// order is By's index in the declaration (len(decl) for the implicit governor), so a reader
	// that wants its governors in declaration order can recover it from the briefing list, which
	// is sorted by Rel instead.
	order int
}

// governanceProblem is one pack-system.md#briefing-p4 report, keyed by the governor's SourceKey so a per-contribution
// reader (GovernedBriefingFor, which the host render calls) can say which declaration it is about.
type governanceProblem struct {
	key string
	msg string
}

// GovernedSources is every source this pack delivers for `kind`, each with its one governor, plus
// one problem per declared source that could not be honored.
//
// Only `briefing` and `skills` return sources; `files` (and every other kind) returns nil.
//
// ORDER: briefing sources sort byte-wise by Rel across the whole pack — the pack-system.md#briefing-directory "within a pack,
// its files are ordered by filename" — so the order is a property of the tree and not of the
// `contributes` list (pack-system.md#briefing-governance). Skills sources keep declaration order, with the implicit source last.
//
// PROBLEMS are pack-system.md#briefing-p4's only: a declared `from` that is absent, not a file (a directory, for
// skills), blank, or escaping the pack tree delivers nothing and is reported — at today's warning
// severity, which the caller chooses. It NEVER quietly delivers another file (P4). An absent
// convention is silent: most packs carry no prose. A `briefing/AGENTS.md` is not reported here —
// LoadDir refuses it fatally (OQ-PB2) — but it is never read either.
func (p *Pack) GovernedSources(kind packdecl.Kind) (sources []GovernedSource, problems []string) {
	srcs, probs := p.governed(kind)
	for _, pr := range probs {
		problems = append(problems, pr.msg)
	}
	return srcs, problems
}

// governed is GovernedSources with each problem keyed by its governor.
func (p *Pack) governed(kind packdecl.Kind) ([]GovernedSource, []governanceProblem) {
	switch kind {
	case packdecl.KindBriefing:
		return p.governedBriefing()
	case packdecl.KindSkills:
		return p.governedSkills()
	}
	return nil, nil
}

// declaration is the pack's ORIGINAL contribution list — the one governance is computed from. A
// ResolveDestinations clone answers with the declaration it was cloned from (see the file header).
func (p *Pack) declaration() []packdecl.Contribution {
	d := p.Decl
	if p.origDecl != nil {
		d = p.origDecl
	}
	if d == nil {
		return nil
	}
	return d.Contributions()
}

// governors is the pack's CONTENT contributions of `kind`, one per SourceKey, in declaration order.
//
// A repeated SourceKey is refused on the strict path (packdecl's validateDuplicateContentSources,
// OQ-PB5), so on any launch the map below never collides. The tolerant in-jail decode runs no
// sibling checks, though, and a host caller may hold a pack whose Decode problems it discarded —
// so a repeat is folded rather than trusted: the FIRST governs, and when neither names a path its
// audience is widened by the second's (a broadcast absorbing every audience). That is the dedup the
// readers used to do by text, kept as the fallback so an invalid manifest still delivers each file
// once.
func (p *Pack) governors(kind packdecl.Kind) ([]packdecl.Contribution, []int) {
	var out []packdecl.Contribution
	var order []int
	index := map[string]int{}
	for i, c := range p.declaration() {
		if c.Kind != kind || c.Agent != "" {
			continue
		}
		key := c.SourceKey()
		if j, seen := index[key]; seen {
			first := &out[j]
			if first.Into == "" && c.Into == "" {
				if len(first.Agents) == 0 || len(c.Agents) == 0 {
					first.Agents = nil
				} else {
					first.Agents = append(append([]string(nil), first.Agents...), c.Agents...)
				}
			}
			continue
		}
		index[key] = len(out)
		out = append(out, c)
		order = append(order, i)
	}
	return out, order
}

// inPack is the lexical containment check every source read makes, kept verbatim from the resolvers
// it replaced: `from` is manifest data, packdecl.Validate rejects ".." at the authoring boundary,
// but a caller may hold a pack whose Decode problems it discarded (`yolo host apply` reads a local
// pack through packForCheckDeps, which does exactly that). A "../../.ssh/id_rsa" that slipped
// through would otherwise be copied into a file the user reads as INSTRUCTIONS. Lexical, so it
// bounds a declared path and not a symlink inside the tree; on the jail path packstage has already
// refused escaping symlinks.
func (p *Pack) inPack(rel string) (string, bool) {
	root := filepath.Clean(p.Root)
	full := filepath.Clean(filepath.Join(root, filepath.FromSlash(rel)))
	if root != "" && root != "." && !strings.HasPrefix(full, root+string(filepath.Separator)) {
		return full, false
	}
	return full, true
}

// readProse reads one briefing file, right-trimmed. ok is false when it is absent or not a regular
// file (a directory, say) — which the caller distinguishes from blank.
func readProse(full string) (text string, isDir bool, ok bool) {
	fi, err := os.Stat(full) // Stat, not Lstat: a symlinked prose file is legitimate
	if err != nil {
		return "", false, false
	}
	if !fi.Mode().IsRegular() {
		return "", fi.IsDir(), false
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", false, false
	}
	return strings.TrimRight(string(data), " \t\r\n"), false, true
}

// readDir is os.ReadDir, a seam so a test can fake a CASE-INSENSITIVE filesystem (default APFS,
// which the macos-user backend and a Mac's `yolo host apply` read packs from) on Linux.
var readDir = os.ReadDir

// conventionalBriefingDir is the pack's briefing/ directory, returned only when the pack root holds
// an entry spelled EXACTLY packdecl.DefaultBriefingDir (pack-system.md#briefing-directory: "Names match case-sensitively.
// `Briefing/` is not the convention."). Opening "<root>/briefing" by path is not that check: on a
// case-insensitive filesystem it opens `Briefing/`, and every entry would then be labelled
// `briefing/<name>` and broadcast. The root is listed and the name compared instead, so both
// filesystems answer alike. Shared with LoadDir's reserved-basename refusal, which must not
// refuse a file inside a directory that is not the convention.
func conventionalBriefingDir(root string) (string, bool) {
	if root == "" {
		root = "."
	}
	entries, err := readDir(root)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if e.Name() != packdecl.DefaultBriefingDir {
			continue
		}
		dir := filepath.Join(root, e.Name())
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() { // Stat: a symlinked dir is legitimate
			return dir, true
		}
		return "", false
	}
	return "", false
}

// sameFileAsAny reports whether full is one of the files already governed by an explicit `from`.
//
// Governance keys on cleaned strings (R4), but a declared `from` is READ through the filesystem —
// and two strings can name one file: a symlinked directory on any filesystem, a case variant
// (`Briefing/a.md`, `briefing/A.md`) on a case-insensitive one. Without this, the convention loop
// would find that file unnamed and broadcast it beside its narrower declaration — the audience
// narrowing silently failing, and the file delivered twice.
func sameFileAsAny(full string, named []os.FileInfo) bool {
	if len(named) == 0 {
		return false
	}
	fi, err := os.Stat(full)
	if err != nil {
		return false
	}
	for _, n := range named {
		if os.SameFile(fi, n) {
			return true
		}
	}
	return false
}

func (p *Pack) governedBriefing() ([]GovernedSource, []governanceProblem) {
	govs, order := p.governors(packdecl.KindBriefing)
	var out []GovernedSource
	var problems []governanceProblem
	named := map[string]bool{}
	var namedFiles []os.FileInfo // the files explicit `from`s resolved to, for sameFileAsAny
	remainder := GovernedSource{
		By: packdecl.Contribution{Kind: packdecl.KindBriefing}, Implicit: true, order: len(p.declaration()),
	}
	for i, c := range govs {
		key := c.SourceKey()
		if key == "" {
			remainder = GovernedSource{By: c, order: order[i]}
			continue
		}
		named[key] = true
		report := func(msg string) {
			problems = append(problems, governanceProblem{key: key, msg: msg})
		}
		if packdecl.RepositoryInstructionFile(key) {
			// Refused by the validator on both decode paths; this is for a caller that discarded
			// that refusal. P1 is "never read", not "refused and then read anyway".
			report(fmt.Sprintf("pack %s: briefing `from` %q is a repository's own agent "+
				"instructions — never shipped as pack prose (move it under %s/)",
				p.Name, c.From, packdecl.DefaultBriefingDir))
			continue
		}
		full, ok := p.inPack(key)
		if !ok {
			report(fmt.Sprintf("pack %s: briefing `from` %q escapes the pack tree — refused",
				p.Name, c.From))
			continue
		}
		if fi, err := os.Stat(full); err == nil && fi.Mode().IsRegular() {
			namedFiles = append(namedFiles, fi)
		}
		text, isDir, ok := readProse(full)
		switch {
		case !ok && isDir:
			report(fmt.Sprintf("pack %s declares `briefing` from %q, which is a directory, not "+
				"a file — no prose delivered from it (a briefing `from` names ONE file; omit "+
				"`from` to carry every %s/*.md)", p.Name, c.From, packdecl.DefaultBriefingDir))
		case !ok:
			report(fmt.Sprintf("pack %s declares `briefing` from %q, which is not in its "+
				"content — no prose delivered from it (check the `from` path, and any "+
				"only/exclude filters)", p.Name, c.From))
		case text == "":
			report(fmt.Sprintf("pack %s declares `briefing` from %q, which is empty — no "+
				"prose delivered from it", p.Name, c.From))
		default:
			out = append(out, GovernedSource{Rel: key, By: c, Abs: full, Text: text, order: order[i]})
		}
	}
	// The convention: every regular *.md DIRECTLY inside briefing/ that no declaration named.
	// Absent is silent, a subdirectory is not read, and a blank file contributes nothing.
	// The directory must be spelled exactly `briefing` (conventionalBriefingDir), and a file an
	// explicit `from` already reached under another spelling is not unnamed (sameFileAsAny).
	dir, ok := conventionalBriefingDir(filepath.Clean(p.Root))
	if entries, err := readDir(dir); ok && err == nil {
		for _, e := range entries {
			rel := packdecl.DefaultBriefingDir + "/" + e.Name()
			if named[rel] || !packdecl.ConventionalBriefingFile(rel) ||
				packdecl.RepositoryInstructionFile(rel) {
				continue
			}
			full := filepath.Join(dir, e.Name())
			if sameFileAsAny(full, namedFiles) {
				continue
			}
			text, _, ok := readProse(full)
			if !ok || text == "" {
				continue
			}
			src := remainder
			src.Rel, src.Abs, src.Text = rel, full, text
			out = append(out, src)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Rel < out[j].Rel })
	return out, problems
}

func (p *Pack) governedSkills() ([]GovernedSource, []governanceProblem) {
	govs, order := p.governors(packdecl.KindSkills)
	var out []GovernedSource
	var problems []governanceProblem
	add := func(c packdecl.Contribution, implicit bool, ord int) {
		key := c.SourceKey()
		dir, prob := p.skillsDir(key)
		if prob != "" {
			problems = append(problems, governanceProblem{key: key, msg: prob})
		}
		if dir != "" {
			out = append(out, GovernedSource{Rel: key, By: c, Implicit: implicit, Abs: dir, order: ord})
		}
	}
	named := false
	for i, c := range govs {
		if c.SourceKey() == packdecl.DefaultSkillsDir {
			named = true
		}
		add(c, false, order[i])
	}
	if !named {
		add(packdecl.Contribution{Kind: packdecl.KindSkills}, true, len(p.declaration()))
	}
	return out, problems
}

// skillsDir resolves a skills source key to an absolute directory, with SkillsSourceDir's three
// outcomes (its doc comment is the authority): a readable directory; a NON-conventional source
// that is absent, a file, or escaping → "" plus a problem; the conventional dir absent → "", no
// problem.
func (p *Pack) skillsDir(rel string) (string, string) {
	dir, ok := p.inPack(rel)
	if !ok {
		return "", fmt.Sprintf("pack %s: skills `from` %q escapes the pack tree — refused", p.Name, rel)
	}
	conventional := path.Clean(rel) == packdecl.DefaultSkillsDir
	fi, err := os.Stat(dir) // Stat, not Lstat: a symlinked skills dir is legitimate
	switch {
	case err == nil && fi.IsDir():
		return dir, ""
	case conventional:
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

// governedBy is the subset of `sources` whose governor has SourceKey `key` — how a synthesized
// host-side contribution (`{into, from}`, ResolveDestinations) is matched back to the declaration
// that governs it. OQ-PB5 makes the key unique per kind within a pack, and "" is the remainder:
// the omitted-`from` governor's files, or the implicit broadcast's.
func governedBy(sources []GovernedSource, key string) []GovernedSource {
	var out []GovernedSource
	for _, s := range sources {
		if s.By.SourceKey() == key {
			out = append(out, s)
		}
	}
	return out
}

// GovernedBriefingFor is the briefing sources ONE contribution carries — its governed files, in
// Rel order — plus the problems about its declared source.
//
// The contribution may be an original declaration or a synthesized ResolveDestinations copy; it is
// matched to its governor by SourceKey. A DESTINATION (`agent` set) carries nothing (P5).
func (p *Pack) GovernedBriefingFor(c packdecl.Contribution) ([]GovernedSource, []string) {
	if c.Kind != packdecl.KindBriefing || c.Agent != "" {
		return nil, nil
	}
	sources, problems := p.governedBriefing()
	key := c.SourceKey()
	var probs []string
	for _, pr := range problems {
		if pr.key == key {
			probs = append(probs, pr.msg)
		}
	}
	return governedBy(sources, key), probs
}

// JoinBriefingSources joins one pack's briefing sources into its SECTION: one blank line between
// files, exactly the spacing between packs (pack-system.md#briefing-directory "Joining"), so a pack's files read the same
// whether a destination composes them one entry at a time (the jail) or as one section (the host).
func JoinBriefingSources(sources []GovernedSource) string {
	texts := make([]string, 0, len(sources))
	for _, s := range sources {
		if s.Text != "" {
			texts = append(texts, s.Text)
		}
	}
	return strings.Join(texts, "\n\n")
}
