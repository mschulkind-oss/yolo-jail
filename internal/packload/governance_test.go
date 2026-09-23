package packload

// governance_test.go pins GovernedSources — the ONE predicate the jail briefing composer, the jail
// skills reader and the host notch's borrower all derive from (pack-briefing-defaults.md §3.3, R5).
// The notch-level call-site tests live beside each notch: internal/cli/run
// (TestJailBriefingMattShape, TestJailSkillsBroadcastSurvivesANarrowerTree), internal/cli
// (TestApplyHostBriefingMattShape) and internal/cli/run's cross-notch parity test.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// rels projects sources to their Rel list.
func rels(sources []GovernedSource) []string {
	out := make([]string, 0, len(sources))
	for _, s := range sources {
		out = append(out, s.Rel)
	}
	return out
}

// briefing/ is read ONE LEVEL DEEP, `*.md` ONLY, exact case, and ordered BYTE-WISE by name — not by
// directory order and not case-folded (§3.1).
func TestGovernedBriefingReadsTheDirectoryShallowMarkdownOnlyBytewise(t *testing.T) {
	p := addressedPack(t, "p", map[string]string{
		"briefing/b.md":        "b\n",
		"briefing/B.md":        "upper\n", // 'B' < 'a' < 'b' byte-wise
		"briefing/a.md":        "a\n",
		"briefing/notes.txt":   "not markdown\n",
		"briefing/x.MD":        "wrong case\n",
		"briefing/sub/deep.md": "not read\n",
		"Briefing/cap.md":      "not the convention\n",
		"briefing/empty.md":    "\n \t\n", // blank: silent, delivers nothing
	})
	got, problems := p.GovernedSources(packdecl.KindBriefing)
	if len(problems) != 0 {
		t.Errorf("problems = %v, want none — nothing here was DECLARED", problems)
	}
	if want := []string{"briefing/B.md", "briefing/a.md", "briefing/b.md"}; !reflect.DeepEqual(rels(got), want) {
		t.Fatalf("sources = %v, want %v", rels(got), want)
	}
	for _, s := range got {
		if !s.Implicit || s.By.Kind != packdecl.KindBriefing || s.By.From != "" || s.By.Into != "" ||
			len(s.By.Agents) != 0 {
			t.Errorf("%s governed by %+v (implicit=%v), want the implicit broadcast", s.Rel, s.By, s.Implicit)
		}
	}
	if got[1].Text != "a" {
		t.Errorf("Text = %q, want the right-trimmed prose", got[1].Text)
	}
}

// THE MATT SHAPE (§2.2, §3.3): house rules under briefing/ and ONE addressed file beside them.
// Declaring the narrow delivery must ADD it, never switch the broad one off (P3).
func TestGovernedBriefingAnAddressedFileAddsToTheBroadcast(t *testing.T) {
	pi := packdecl.Contribution{Kind: packdecl.KindBriefing, From: "files/pi-rules.md", Agents: []string{"pi"}}
	p := addressedPack(t, "matt", map[string]string{
		"briefing/house-rules.md": "house\n", "files/pi-rules.md": "pi only\n",
	}, pi)
	got, _ := p.GovernedSources(packdecl.KindBriefing)
	if want := []string{"briefing/house-rules.md", "files/pi-rules.md"}; !reflect.DeepEqual(rels(got), want) {
		t.Fatalf("sources = %v, want %v — both files deliver", rels(got), want)
	}
	if !got[0].Implicit {
		t.Errorf("house-rules governed by %+v, want the implicit broadcast", got[0].By)
	}
	if got[1].Implicit || !reflect.DeepEqual(got[1].By.Agents, []string{"pi"}) {
		t.Errorf("pi-rules governed by %+v, want the addressed contribution", got[1].By)
	}
}

// Naming ONE briefing/ file narrows THAT file; the rest keep broadcasting.
func TestGovernedBriefingNamingOneFileNarrowsOnlyIt(t *testing.T) {
	pi := packdecl.Contribution{Kind: packdecl.KindBriefing, From: "./briefing/pi.md", Agents: []string{"pi"}}
	p := addressedPack(t, "p", map[string]string{
		"briefing/pi.md": "pi\n", "briefing/all.md": "all\n",
	}, pi)
	got, _ := p.GovernedSources(packdecl.KindBriefing)
	if want := []string{"briefing/all.md", "briefing/pi.md"}; !reflect.DeepEqual(rels(got), want) {
		t.Fatalf("sources = %v, want %v — briefing/pi.md once, under its CLEANED path (R4)", rels(got), want)
	}
	if !got[0].Implicit {
		t.Errorf("all.md governed by %+v, want the implicit broadcast", got[0].By)
	}
	if got[1].Implicit || got[1].By.From != "./briefing/pi.md" {
		t.Errorf("pi.md governed by %+v, want the contribution naming it", got[1].By)
	}
}

// An omitted-`from` content contribution names the WHOLE unclaimed convention — so there is no
// implicit broadcast left — and a named file stays its own governor's.
func TestGovernedBriefingAnOmittedFromTakesTheRemainder(t *testing.T) {
	rest := packdecl.Contribution{Kind: packdecl.KindBriefing, Agents: []string{"claude"}}
	pi := packdecl.Contribution{Kind: packdecl.KindBriefing, From: "briefing/pi.md", Agents: []string{"pi"}}
	p := addressedPack(t, "p", map[string]string{
		"briefing/pi.md": "pi\n", "briefing/a.md": "a\n", "briefing/b.md": "b\n",
	}, rest, pi)
	got, _ := p.GovernedSources(packdecl.KindBriefing)
	if want := []string{"briefing/a.md", "briefing/b.md", "briefing/pi.md"}; !reflect.DeepEqual(rels(got), want) {
		t.Fatalf("sources = %v, want %v", rels(got), want)
	}
	for _, s := range got {
		if s.Implicit {
			t.Errorf("%s is implicit — an omitted-`from` contribution leaves no broadcast", s.Rel)
		}
		want := "claude"
		if s.Rel == "briefing/pi.md" {
			want = "pi"
		}
		if len(s.By.Agents) != 1 || s.By.Agents[0] != want {
			t.Errorf("%s governed by %+v, want the %s contribution", s.Rel, s.By, want)
		}
	}
}

// ORDER-INDEPENDENT (§3.3): reversing `contributes` changes neither membership nor routing.
func TestGovernedBriefingIgnoresContributesOrder(t *testing.T) {
	files := map[string]string{
		"briefing/pi.md": "pi\n", "briefing/a.md": "a\n", "files/extra.md": "x\n",
	}
	cs := []packdecl.Contribution{
		{Kind: packdecl.KindBriefing, Agents: []string{"claude"}},
		{Kind: packdecl.KindBriefing, From: "briefing/pi.md", Agents: []string{"pi"}},
		{Kind: packdecl.KindBriefing, From: "files/extra.md", Into: ".x/X.md"},
	}
	fwd, _ := addressedPack(t, "p", files, cs...).GovernedSources(packdecl.KindBriefing)
	rev, _ := addressedPack(t, "p", files, cs[2], cs[1], cs[0]).GovernedSources(packdecl.KindBriefing)
	project := func(ss []GovernedSource) []string {
		var out []string
		for _, s := range ss {
			out = append(out, s.Rel+"→"+s.By.SourceKey()+"/"+strings.Join(s.By.Agents, ",")+"/"+s.By.Into)
		}
		return out
	}
	if !reflect.DeepEqual(project(fwd), project(rev)) {
		t.Errorf("governance depends on contributes order:\n fwd %v\n rev %v", project(fwd), project(rev))
	}
}

// A DESTINATION GOVERNS NOTHING (P5). Every shipped agent pack declares `{agent, into}` with no
// `from`; counted as an omitted-`from` content contribution, it would switch that pack's own
// implicit broadcast off.
func TestGovernedSourcesADestinationDoesNotSuppressTheBroadcast(t *testing.T) {
	p := addressedPack(t, "claude", map[string]string{
		"briefing/own.md": "own\n", "skills/s/SKILL.md": "s\n",
	},
		packdecl.Contribution{Kind: packdecl.KindBriefing, Agent: "claude", Into: ".claude/CLAUDE.md"},
		packdecl.Contribution{Kind: packdecl.KindSkills, Agent: "claude", Into: ".claude/skills"})
	for _, kind := range []packdecl.Kind{packdecl.KindBriefing, packdecl.KindSkills} {
		got, _ := p.GovernedSources(kind)
		if len(got) != 1 || !got[0].Implicit {
			t.Errorf("%s: sources = %+v, want the one implicit source — a destination names nothing",
				kind, got)
		}
	}
}

// A declared `from` that cannot be honored is REPORTED and delivers nothing; an absent convention
// is silent; a blank conventional file is silent. `files` governs nothing here at all.
func TestGovernedSourcesProblemsAreDeclaredSourcesOnly(t *testing.T) {
	p := addressedPack(t, "p", map[string]string{"briefing/blank.md": " \n"},
		packdecl.Contribution{Kind: packdecl.KindBriefing, From: "gone.md", Agents: []string{"pi"}},
		packdecl.Contribution{Kind: packdecl.KindSkills, From: "gone-skills", Agents: []string{"pi"}},
		packdecl.Contribution{Kind: packdecl.KindFiles, From: "tree", Agents: []string{"pi"}})
	for _, tc := range []struct {
		kind packdecl.Kind
		name string
	}{{packdecl.KindBriefing, "gone.md"}, {packdecl.KindSkills, "gone-skills"}} {
		got, problems := p.GovernedSources(tc.kind)
		if len(got) != 0 {
			t.Errorf("%s: sources = %v, want none", tc.kind, rels(got))
		}
		if len(problems) != 1 || !strings.Contains(problems[0], tc.name) {
			t.Errorf("%s: problems = %v, want exactly one naming %s", tc.kind, problems, tc.name)
		}
	}
	if got, problems := p.GovernedSources(packdecl.KindFiles); got != nil || problems != nil {
		t.Errorf("files: %v, %v — want nil: `files` has no convention to govern", got, problems)
	}
}

// Skills: an omitted `from` IS `skills`, and the tree is ONE unit — named by any content
// contribution, it is not implicit; otherwise it broadcasts, AFTER the declared sources.
func TestGovernedSkillsTheTreeIsOneUnit(t *testing.T) {
	files := map[string]string{"skills/a/SKILL.md": "a\n", "extra/b/SKILL.md": "b\n"}
	extra := packdecl.Contribution{Kind: packdecl.KindSkills, From: "extra", Agents: []string{"pi"}}
	got, _ := addressedPack(t, "p", files, extra).GovernedSources(packdecl.KindSkills)
	if want := []string{"extra", "skills"}; !reflect.DeepEqual(rels(got), want) || !got[1].Implicit {
		t.Fatalf("sources = %+v, want extra then the implicit skills/ — a narrower tree adds", got)
	}
	named := packdecl.Contribution{Kind: packdecl.KindSkills, Into: ".mine/skills"}
	got, _ = addressedPack(t, "p", files, named).GovernedSources(packdecl.KindSkills)
	if len(got) != 1 || got[0].Rel != "skills" || got[0].Implicit || got[0].By.Into != ".mine/skills" {
		t.Errorf("sources = %+v, want skills/ governed by the omitted-`from` contribution", got)
	}
}

// OQ-PB2: a reserved basename INSIDE briefing/ is a FATAL LoadDir problem naming the move — with a
// manifest or without — and is never read even by a caller that discards the problem.
func TestLoadDirRefusesAReservedNameInsideBriefing(t *testing.T) {
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md"} {
		root := proseTree(t, map[string]string{"briefing/" + name: "dual use\n", "briefing/ok.md": "ok\n"})
		p, probs := LoadDir(root, "p")
		if len(probs) != 1 || !strings.Contains(probs[0], "briefing/"+name) ||
			!strings.Contains(probs[0], "git mv") {
			t.Errorf("%s: LoadDir problems = %v, want one naming the file and the move", name, probs)
		}
		got, _ := p.GovernedSources(packdecl.KindBriefing)
		if want := []string{"briefing/ok.md"}; !reflect.DeepEqual(rels(got), want) {
			t.Errorf("%s: sources = %v, want %v — the reserved file is never read", name, rels(got), want)
		}
	}
	// A root AGENTS.md is NOT refused: it is the repository's, and it simply is not read (OQ-PB3).
	root := proseTree(t, map[string]string{"AGENTS.md": "repo guide\n"})
	if _, probs := LoadDir(root, "p"); len(probs) != 0 {
		t.Errorf("LoadDir refused a ROOT AGENTS.md: %v — that file is the repository's own", probs)
	}
}

// THE CLONE TRAP: governance is computed from the ORIGINAL declaration. A ResolveDestinations clone's
// Decl holds a synthesized `{into, from}` copy of every borrower; read from it, the implicit
// broadcast reads as an omitted-`from` declaration and vanishes at the host notch only.
func TestGovernedSourcesOfAResolvedCloneAreTheOriginals(t *testing.T) {
	claude := agentPack(t, "claude", packdecl.Contribution{Kind: packdecl.KindBriefing,
		Agent: "claude", Into: ".claude/CLAUDE.md"})
	pi := agentPack(t, "pi", packdecl.Contribution{Kind: packdecl.KindBriefing,
		Agent: "pi", Into: ".pi/agent/AGENTS.md"})
	matt := addressedPack(t, "matt", map[string]string{
		"briefing/house-rules.md": "house\n", "files/pi-rules.md": "pi only\n",
	}, packdecl.Contribution{Kind: packdecl.KindBriefing, From: "files/pi-rules.md", Agents: []string{"pi"}})
	want, _ := matt.GovernedSources(packdecl.KindBriefing)

	d := matt.ResolveDestinations([]*Pack{claude, pi, matt})
	if d.Pack == matt {
		t.Fatal("nothing was inferred — the fixture must exercise the clone")
	}
	got, _ := d.Pack.GovernedSources(packdecl.KindBriefing)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("clone governance = %+v\nwant the original's %+v", got, want)
	}
	// And resolving the clone again must not launder synthesized entries into "the declaration".
	again := d.Pack.ResolveDestinations([]*Pack{claude, pi, d.Pack})
	if got2, _ := again.Pack.GovernedSources(packdecl.KindBriefing); !reflect.DeepEqual(got2, want) {
		t.Errorf("twice-resolved governance = %+v, want the original's", got2)
	}
}

// THE HOST HALF OF THE MATT SHAPE, at the inference: house rules reach EVERY destination (the
// implicit borrower), pi-rules reach pi's only (the addressed one). Mutation: restore the
// `declares`-style suppression in borrowingSources and the implicit borrower disappears here.
func TestResolveDestinationsMattShapeBorrowsForBothGovernors(t *testing.T) {
	claude := agentPack(t, "claude", packdecl.Contribution{Kind: packdecl.KindBriefing,
		Agent: "claude", Into: ".claude/CLAUDE.md"})
	pi := agentPack(t, "pi", packdecl.Contribution{Kind: packdecl.KindBriefing,
		Agent: "pi", Into: ".pi/agent/AGENTS.md"})
	matt := addressedPack(t, "matt", map[string]string{
		"briefing/house-rules.md": "house\n", "files/pi-rules.md": "pi only\n",
	}, packdecl.Contribution{Kind: packdecl.KindBriefing, From: "files/pi-rules.md", Agents: []string{"pi"}})

	d := matt.ResolveDestinations([]*Pack{claude, pi, matt})
	var pairs []string
	for _, c := range d.Inferred {
		text, _ := d.Pack.BriefingProseFor(c)
		pairs = append(pairs, c.Into+"="+text)
	}
	want := []string{".pi/agent/AGENTS.md=pi only", ".claude/CLAUDE.md=house", ".pi/agent/AGENTS.md=house"}
	if !reflect.DeepEqual(pairs, want) {
		t.Errorf("inferred = %v, want %v", pairs, want)
	}
}

// `{into: ".claude/CLAUDE.md"}` names the unclaimed convention BY OMISSION, so briefing/ goes to
// that one path and nowhere else — the no-widening promise kept by the files being named (§3.3).
func TestResolveDestinationsAnIntoGovernsTheConventionWithoutWidening(t *testing.T) {
	claude := agentPack(t, "claude", packdecl.Contribution{Kind: packdecl.KindBriefing,
		Agent: "claude", Into: ".claude/CLAUDE.md"})
	pi := agentPack(t, "pi", packdecl.Contribution{Kind: packdecl.KindBriefing,
		Agent: "pi", Into: ".pi/agent/AGENTS.md"})
	own := addressedPack(t, "own", map[string]string{"briefing/a.md": "a\n", "briefing/b.md": "b\n"},
		packdecl.Contribution{Kind: packdecl.KindBriefing, Into: ".claude/CLAUDE.md"})
	d := own.ResolveDestinations([]*Pack{claude, pi, own})
	if len(d.Inferred) != 0 || d.Pack != own {
		t.Errorf("Inferred = %v — a pack naming its own `into` must not be widened", d.Inferred)
	}
	if text, _ := own.BriefingProseFor(own.Decl.Contributions()[0]); text != "a\n\nb" {
		t.Errorf("the `into` contribution carries %q, want every briefing/ file", text)
	}
}

// P2 INCLUDES THE BROADCASTING PACK'S OWN DESTINATIONS (§3.5): an agent pack shipping prose
// reaches its own agent. The self-skip in borrowedDestinations made the host notch skip it while
// the jail's nil audience reached it.
func TestResolveDestinationsABroadcastReachesItsOwnPacksDestination(t *testing.T) {
	claude := addressedPack(t, "claude", map[string]string{"briefing/own.md": "own\n"},
		packdecl.Contribution{Kind: packdecl.KindBriefing, Agent: "claude", Into: ".claude/CLAUDE.md"})
	pi := agentPack(t, "pi", packdecl.Contribution{Kind: packdecl.KindBriefing,
		Agent: "pi", Into: ".pi/agent/AGENTS.md"})
	d := claude.ResolveDestinations([]*Pack{claude, pi})
	if want := []string{".claude/CLAUDE.md", ".pi/agent/AGENTS.md"}; !sameStrings(
		intos(d.Inferred, packdecl.KindBriefing), want) {
		t.Errorf("inferred = %v, want %v — including the pack's own destination",
			intos(d.Inferred, packdecl.KindBriefing), want)
	}
}

// The tolerant-decode FALLBACK: two omitted-`from` contributions (refused on the strict path) still
// give every file exactly ONE governor, with the audiences unioned — never a file delivered twice.
func TestGovernedBriefingFoldsADuplicateGovernor(t *testing.T) {
	p := addressedPack(t, "p", map[string]string{"briefing/a.md": "a\n"},
		packdecl.Contribution{Kind: packdecl.KindBriefing, Agents: []string{"claude"}},
		packdecl.Contribution{Kind: packdecl.KindBriefing, Agents: []string{"pi"}})
	got, _ := p.GovernedSources(packdecl.KindBriefing)
	if len(got) != 1 || !reflect.DeepEqual(got[0].By.Agents, []string{"claude", "pi"}) {
		t.Errorf("sources = %+v, want one file governed once, audiences unioned", got)
	}
	// And the fold never writes into the declaration it read.
	if a := p.Decl.Contributions()[0].Agents; !reflect.DeepEqual(a, []string{"claude"}) {
		t.Errorf("the declaration was mutated: %v", a)
	}
}

// SkillsSourceDir is the P5 half for skills: a destination sources nothing, even with skills/ on disk.
func TestSkillsSourceDirADestinationSourcesNothing(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "skills", "s"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := &Pack{Name: "claude", Root: root, Decl: &packdecl.Manifest{}}
	dir, prob := p.SkillsSourceDir(packdecl.Contribution{Kind: packdecl.KindSkills,
		Agent: "claude", Into: ".claude/skills"})
	if dir != "" || prob != "" {
		t.Errorf("SkillsSourceDir(destination) = %q, %q; want nothing", dir, prob)
	}
}

// foldCase fakes a case-insensitive filesystem (default APFS) for one directory of a pack: every
// PATH open of `<root>/<folded>` reaches `<root>/<onDisk>` (a symlink stands in for the case
// folding), while a LISTING of the root — through the readDir seam — shows only the on-disk name,
// exactly as APFS does. That is the pair of facts the bug lives between: opening "briefing" by path
// succeeds, and no entry is spelled "briefing".
func foldCase(t *testing.T, root, onDisk, folded string) {
	t.Helper()
	if err := os.Symlink(onDisk, filepath.Join(root, folded)); err != nil {
		t.Fatal(err)
	}
	orig := readDir
	t.Cleanup(func() { readDir = orig })
	readDir = func(name string) ([]os.DirEntry, error) {
		entries, err := os.ReadDir(name)
		if err != nil || filepath.Clean(name) != filepath.Clean(root) {
			return entries, err
		}
		out := entries[:0]
		for _, e := range entries {
			if e.Name() != folded {
				out = append(out, e)
			}
		}
		return out, nil
	}
}

// `Briefing/` IS NOT THE CONVENTION ON A CASE-INSENSITIVE FILESYSTEM EITHER (§3.1). Opening
// "<root>/briefing" by path there opens `Briefing/`; the root must be listed and the name compared,
// or a Mac broadcasts what Linux ignores. LoadDir's reserved-basename refusal shares the probe, so
// a `Briefing/AGENTS.md` is not refused as a source it is not.
func TestGovernedBriefingIgnoresACaseVariantDirectoryOnAFoldingFilesystem(t *testing.T) {
	root := proseTree(t, map[string]string{"Briefing/cap.md": "not the convention\n",
		"Briefing/AGENTS.md": "a repository file\n"})
	foldCase(t, root, "Briefing", "briefing")
	p, probs := LoadDir(root, "p")
	if len(probs) != 0 {
		t.Errorf("LoadDir refused a file inside `Briefing/`, which is not the convention: %v", probs)
	}
	if got, _ := p.GovernedSources(packdecl.KindBriefing); len(got) != 0 {
		t.Errorf("sources = %v, want none — `Briefing/` read as the convention", rels(got))
	}
	// And the exact spelling is still read through the same seam.
	readDir = os.ReadDir
	root = proseTree(t, map[string]string{"briefing/a.md": "a\n"})
	p, _ = LoadDir(root, "p")
	if got, _ := p.GovernedSources(packdecl.KindBriefing); !reflect.DeepEqual(rels(got), []string{"briefing/a.md"}) {
		t.Errorf("sources = %v, want the exactly-spelled convention", rels(got))
	}
}

// TWO SPELLINGS OF ONE FILE ARE ONE FILE (R4), on disk and not only as strings. A `from` that
// reaches briefing/a.md through another spelling — a symlinked directory here; a case variant on a
// case-insensitive filesystem — governs it, so the convention loop must not broadcast it as well:
// the narrowing would silently fail and the addressed agent would get the file twice.
func TestGovernedBriefingAnotherSpellingOfANamedFileIsNamed(t *testing.T) {
	p := addressedPack(t, "p", map[string]string{"briefing/a.md": "a\n", "briefing/b.md": "b\n"},
		packdecl.Contribution{Kind: packdecl.KindBriefing, From: "alias/a.md", Agents: []string{"pi"}})
	if err := os.Symlink("briefing", filepath.Join(p.Root, "alias")); err != nil {
		t.Fatal(err)
	}
	got, problems := p.GovernedSources(packdecl.KindBriefing)
	if len(problems) != 0 {
		t.Errorf("problems = %v", problems)
	}
	if want := []string{"alias/a.md", "briefing/b.md"}; !reflect.DeepEqual(rels(got), want) {
		t.Fatalf("sources = %v, want %v — briefing/a.md is alias/a.md, already governed", rels(got), want)
	}
	if got[0].Implicit || !reflect.DeepEqual(got[0].By.Agents, []string{"pi"}) || !got[1].Implicit {
		t.Errorf("governors = %+v", got)
	}
}

// carriesFor ASKS ABOUT ONE CONTRIBUTION'S OWN TREE. A borrower whose tree holds no skill must not
// "carry" because a SIBLING source of the same pack does: it would synthesize a destination and an
// Addressed delivery for a tree with nothing in it. The pack's skills/ still reaches everyone.
func TestResolveDestinationsAnEmptyAddressedSkillsTreeCarriesNothing(t *testing.T) {
	claude := agentPack(t, "claude", packdecl.Contribution{Kind: packdecl.KindSkills,
		Agent: "claude", Into: ".claude/skills"})
	pi := agentPack(t, "pi", packdecl.Contribution{Kind: packdecl.KindSkills,
		Agent: "pi", Into: ".pi/agent/skills"})
	s := addressedPack(t, "s", map[string]string{
		"skills/one/SKILL.md": "---\nname: one\n---\n",
		"empty-tree/loose.md": "a loose file is not a skill\n",
	}, packdecl.Contribution{Kind: packdecl.KindSkills, From: "empty-tree", Agents: []string{"pi"}})

	_, dests := ResolveDestinations([]*Pack{claude, pi, s})
	var d *Destinations
	for i := range dests {
		if dests[i].Pack.Name == "s" {
			d = &dests[i]
		}
	}
	if d == nil {
		t.Fatal("no resolution for pack s")
	}
	for _, c := range d.Inferred {
		if c.From == "empty-tree" {
			t.Errorf("inferred %+v for a tree holding no skill", c)
		}
	}
	for _, a := range d.Addressed {
		if a.From == "empty-tree" && len(a.Into) > 0 {
			t.Errorf("Addressed %+v reports a delivery from a tree holding no skill", a)
		}
	}
	if got := intos(d.Inferred, packdecl.KindSkills); !reflect.DeepEqual(got, []string{".claude/skills", ".pi/agent/skills"}) {
		t.Errorf("skills/ inferred into %v, want both agents' destinations", got)
	}
}
