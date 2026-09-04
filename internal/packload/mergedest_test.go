package packload

// mergedest_test.go pins the ZERO-CEREMONY destination inference (finding F1).
//
// The defect it guards: a pack with `skills/` + `AGENTS.md` and NO pack.json declared no
// destination, so the host render — which iterates declarations — did nothing and said nothing,
// while the jail merged it fine. These tests assert the inference at the unit level; the
// end-to-end assertion that a skill LANDS in a real (temp) home is in internal/cli.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// agentPack is a stand-in for a shipped agent pack: it declares destinations and ships no
// content, which is exactly the shape all six real ones have.
func agentPack(t *testing.T, name string, contributes ...packdecl.Contribution) *Pack {
	t.Helper()
	return &Pack{Name: name, Root: t.TempDir(),
		Decl: &packdecl.Manifest{Contributes: contributes}}
}

// zeroCeremonyPack writes a pack tree with NO pack.json: a skills dir holding one skill and an
// AGENTS.md. skills=false omits the skills tree, prose=false omits the AGENTS.md.
func zeroCeremonyPack(t *testing.T, name string, skills, prose bool) *Pack {
	t.Helper()
	root := t.TempDir()
	if skills {
		dir := filepath.Join(root, "skills", "zcskill")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("body\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if prose {
		if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# prose\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The empty manifest LoadDir hands a pack with no pack.json.
	return &Pack{Name: name, Root: root, Decl: &packdecl.Manifest{}}
}

// addressedPack writes a pack tree from a path→body map and takes an IN-MEMORY manifest: the
// shape briefing-audiences.md §4.1 gives a content pack — source files at paths of its own
// choosing, and contributions that name an AUDIENCE (`agents`) instead of a path.
//
// Deliberately NOT the conventional layout zeroCeremonyPack writes, and deliberately not routed
// through LoadDir. The point of every fixture below is a pack whose content is somewhere only its
// own `from` can find, which is precisely what the conventional-source probe cannot see.
func addressedPack(t *testing.T, name string, files map[string]string,
	contributes ...packdecl.Contribution) *Pack {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &Pack{Name: name, Root: root, Decl: &packdecl.Manifest{Contributes: contributes}}
}

// froms is the `from` of every contribution of one kind, positionally matching intos().
func froms(cs []packdecl.Contribution, kind packdecl.Kind) []string {
	var out []string
	for _, c := range cs {
		if c.Kind == kind {
			out = append(out, c.From)
		}
	}
	return out
}

// intos is the `into` of every contribution of one kind, for comparing against a want list.
func intos(cs []packdecl.Contribution, kind packdecl.Kind) []string {
	var out []string
	for _, c := range cs {
		if c.Kind == kind {
			out = append(out, c.Into)
		}
	}
	return out
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// THE F1 CASE: a zero-ceremony pack selected alongside two agent packs gets one skills and one
// briefing destination per agent — the same destinations the jail merges into.
func TestResolveDestinationsInfersFromTheSelectedSet(t *testing.T) {
	claude := agentPack(t, "claude",
		packdecl.Contribution{Kind: packdecl.KindSkills, From: "skills",
			Into: ".claude/skills", Tier: "namespaced"},
		packdecl.Contribution{Kind: packdecl.KindBriefing, From: "AGENTS.md",
			Into: ".claude/CLAUDE.md", After: "host:.claude/CLAUDE.md"})
	pi := agentPack(t, "pi",
		packdecl.Contribution{Kind: packdecl.KindSkills, From: "skills", Into: ".pi/agent/skills"},
		packdecl.Contribution{Kind: packdecl.KindBriefing, From: "AGENTS.md", Into: ".pi/agent/AGENTS.md"})
	zc := zeroCeremonyPack(t, "zc", true, true)

	set := []*Pack{claude, pi, zc}
	packs, outcomes := ResolveDestinations(set)

	d := outcomes[2]
	if len(d.Orphaned) != 0 {
		t.Fatalf("zero-ceremony pack reported orphaned kinds %v with two agent packs selected — "+
			"the destinations are right there in their manifests", d.Orphaned)
	}
	got := packs[2].Decl.Contributions()
	if want := []string{".claude/skills", ".pi/agent/skills"}; !sameStrings(
		intos(got, packdecl.KindSkills), want) {
		t.Errorf("skills destinations = %v, want %v — a zero-ceremony pack must reach every "+
			"agent's skills dir at the host, as it already does in a jail (F1)",
			intos(got, packdecl.KindSkills), want)
	}
	if want := []string{".claude/CLAUDE.md", ".pi/agent/AGENTS.md"}; !sameStrings(
		intos(got, packdecl.KindBriefing), want) {
		t.Errorf("briefing destinations = %v, want %v", intos(got, packdecl.KindBriefing), want)
	}
}

// S2: A TIER IS NOT INHERITED. A BORROWED DESTINATION IS A DESTINATION, NOT A NAMING POLICY.
//
// This test replaces TestResolveDestinationsInheritsTier, which asserted the opposite on the
// argument that a tier is a fact about the destination TOOL and the pack naming the directory is
// the authority on it. The consequence was the defect: a zero-ceremony pack borrowing BOTH
// `.claude/skills` (namespaced) and `.codex/skills` (flat) inherited both, so one skill in one pack
// was `/zc:mine` in Claude and `/mine` in codex — two names for one skill, neither of them chosen
// by the pack that owns the content, which has no manifest to choose with. A tier is now the pack's
// OWN positive opt-in (packdecl's SkillsTier), so there is nothing here to inherit.
func TestResolveDestinationsDoesNotInheritTier(t *testing.T) {
	// The declaring pack carries BOTH spellings, and that is what gives the assertions below their
	// teeth. Found by mutation: with the fixture declaring neither, restoring
	// `Tier: c.Tier` in borrowedDestinations copied an empty string and the suite stayed green —
	// the test proved only that nothing invents a tier, not that nothing PROPAGATES one.
	//
	// `Tier` on the contribution is the RETIRED field (packdecl refuses it at the authoring
	// boundary), reachable here only through the tolerant decoder at a version boundary. It is set
	// deliberately: the inference must not launder a field the validator rejects into a
	// synthesized contribution, where no validator ever looks again.
	claude := agentPack(t, "claude", packdecl.Contribution{
		Kind: packdecl.KindSkills, From: "skills", Into: ".claude/skills", Tier: "namespaced"})
	claude.Decl.SkillsTier = "namespaced" // the DECLARING pack's own choice, for itself
	zc := zeroCeremonyPack(t, "zc", true, false)

	d := zc.ResolveDestinations([]*Pack{claude, zc})
	if len(d.Inferred) != 1 {
		t.Fatalf("Inferred = %v, want one skills destination", d.Inferred)
	}
	if d.Inferred[0].Tier != "" {
		t.Errorf("inferred tier = %q, want empty — the retired per-contribution field must not be "+
			"synthesized OR propagated: nothing reads it, and a synthesized contribution is past "+
			"every validator that would refuse it", d.Inferred[0].Tier)
	}
	// And the BORROWER's own tier is untouched by the pack it borrowed from: `zc` has no manifest,
	// so it never opted in, and yolo must not opt in on its behalf.
	if d.Pack.Decl.WantsNamespacedSkills() {
		t.Error("a zero-ceremony pack inherited the destination pack's namespacing — `skills_tier` " +
			"is a POSITIVE choice, and a pack with no pack.json cannot have made it")
	}
	// `from` must stay EMPTY so it resolves to the borrower's OWN conventional dir. Inheriting
	// it would read the agent pack's source, which is the one thing the inference must not do.
	if d.Inferred[0].From != "" {
		t.Errorf("inferred from = %q, want empty — the destination is borrowed, the content is not",
			d.Inferred[0].From)
	}
	// `after` must NOT be inherited: on a briefing it means "prepend the user's own file", which
	// is the agent pack's job at that destination.
	briefer := agentPack(t, "claude", packdecl.Contribution{Kind: packdecl.KindBriefing,
		From: "AGENTS.md", Into: ".claude/CLAUDE.md", After: "host:.claude/CLAUDE.md"})
	zc2 := zeroCeremonyPack(t, "zc2", false, true)
	d2 := zc2.ResolveDestinations([]*Pack{briefer, zc2})
	if len(d2.Inferred) != 1 || d2.Inferred[0].After != "" {
		t.Errorf("inferred briefing = %+v, want After empty — two packs both prepending the "+
			"same host file into one briefing is not a merge", d2.Inferred)
	}
}

// AN ADDRESSED CONTRIBUTION IS ROUTED, AND ONLY TO ITS AUDIENCE (briefing-audiences.md §4.1).
//
// A content pack says WHO its prose is for and never WHERE it goes: `{kind: briefing, agents:
// ["claude"]}` with no `into`. Two things have to hold at once, and each is a separate defect
// if it does not:
//
//   - `declares` must test for a DESTINATION, not for the kind. An `agents`-only contribution
//     names no destination, so a pack carrying one is still silent about where its prose goes —
//     and a `Kind == kind` test would call it a declaring pack and skip inference entirely,
//     which is prose delivered NOWHERE, silently.
//   - `borrowedDestinations` must narrow to the destinations whose owner declared a matching
//     `agent`. Without the filter the selector is decoration: the prose reaches `.pi/agent/
//     AGENTS.md` too, which is the broadcast the field exists to stop.
//
// Built in memory rather than through LoadDir on purpose — an `into`-less briefing has to be
// constructible here before the validator learns to accept one.
func TestResolveDestinationsRoutesAnAddressedBriefing(t *testing.T) {
	claude := agentPack(t, "claude", packdecl.Contribution{Kind: packdecl.KindBriefing,
		Into: ".claude/CLAUDE.md", Agent: "claude"})
	pi := agentPack(t, "pi", packdecl.Contribution{Kind: packdecl.KindBriefing,
		Into: ".pi/agent/AGENTS.md", Agent: "pi"})
	// A content pack: an AGENTS.md at its root, and a manifest that names an AUDIENCE only.
	house := zeroCeremonyPack(t, "house", false, true)
	house.Decl = &packdecl.Manifest{Contributes: []packdecl.Contribution{{
		Kind: packdecl.KindBriefing, Agents: []string{"claude"}}}}

	d := house.ResolveDestinations([]*Pack{claude, pi, house})
	if len(d.Orphaned) != 0 {
		t.Fatalf("Orphaned = %v — claude declared `agent: claude` and a destination, so the "+
			"selector matched something", d.Orphaned)
	}
	got := intos(d.Inferred, packdecl.KindBriefing)
	if want := []string{".claude/CLAUDE.md"}; !sameStrings(got, want) {
		t.Fatalf("inferred briefing = %v, want %v — an `agents`-only contribution names no "+
			"destination (so it must be INFERRED, not treated as a declaration) and names an "+
			"audience (so pi's destination must not be borrowed)", got, want)
	}
}

// A SELECTOR THAT MATCHES NOTHING IS ORPHANED, NOT SILENT (risk R1). A content pack addressing
// `codex` in a jail that selected only claude is the whole pack going inert, and the one thing
// it must not do is go inert quietly — the filter has to route into the report the inference
// already has, not into an empty slice nobody looks at.
//
// (What the two GATES then do with that — refuse the launch or report and skip — is §4.3's
// question, not this function's. Here it is data.)
func TestResolveDestinationsReportsAnUnmatchedAudience(t *testing.T) {
	claude := agentPack(t, "claude", packdecl.Contribution{Kind: packdecl.KindBriefing,
		Into: ".claude/CLAUDE.md", Agent: "claude"})
	house := zeroCeremonyPack(t, "house", false, true)
	house.Decl = &packdecl.Manifest{Contributes: []packdecl.Contribution{{
		Kind: packdecl.KindBriefing, Agents: []string{"codex"}}}}

	d := house.ResolveDestinations([]*Pack{claude, house})
	if len(d.Inferred) != 0 {
		t.Errorf("Inferred = %v for prose addressed to an agent this set does not have",
			d.Inferred)
	}
	if len(d.Orphaned) != 1 || d.Orphaned[0].Kind != packdecl.KindBriefing {
		t.Errorf("Orphaned = %v, want one %q — a pack whose whole content reaches nothing must "+
			"never be silent about it", d.Orphaned, packdecl.KindBriefing)
	}
}

// AN ADDRESSED CONTRIBUTION NAMES ITS OWN SOURCE, AND THAT IS THE WHOLE POINT OF `from` +
// `agents` (briefing-audiences.md §4.1 line 194: `{from: "prose/claude.md", agents: ["claude"]}`).
//
// Two production chokepoints have to give way for this shape to route at all, and each was
// written for the ZERO-CEREMONY pack, which by construction has no `from`:
//
//   - the content probe asked only whether the pack holds content at the CONVENTIONAL location, so
//     a pack whose only prose is `prose/claude.md` looked empty and `ResolveDestinations` skipped
//     it BEFORE the orphan branch — nothing delivered and nothing reported;
//   - the synthesized contribution left `from` EMPTY on purpose, which is right when the
//     destination is borrowed and the content is conventional, and wrong here: the addressed
//     contribution named its source, so blanking it delivers somebody else's file.
//
// The assertion that pins the second half is the resolved prose, not the field: `From` is only
// evidence, and BriefingProseFor is what the host render actually calls per contribution
// (ComposeHostBriefings) to decide what lands.
func TestResolveDestinationsRoutesAnAddressedBriefingFrom(t *testing.T) {
	claude := agentPack(t, "claude", packdecl.Contribution{Kind: packdecl.KindBriefing,
		Into: ".claude/CLAUDE.md", Agent: "claude"})
	pi := agentPack(t, "pi", packdecl.Contribution{Kind: packdecl.KindBriefing,
		Into: ".pi/agent/AGENTS.md", Agent: "pi"})
	// No AGENTS.md at the root: the ONLY prose this pack has is the addressed one.
	house := addressedPack(t, "house",
		map[string]string{"prose/claude.md": "claude house rules\n"},
		packdecl.Contribution{Kind: packdecl.KindBriefing, From: "prose/claude.md",
			Agents: []string{"claude"}})

	d := house.ResolveDestinations([]*Pack{claude, pi, house})
	if len(d.Orphaned) != 0 {
		t.Fatalf("Orphaned = %v — claude declared `agent: claude` and a destination, so this "+
			"prose has somewhere to go", d.Orphaned)
	}
	if want := []string{".claude/CLAUDE.md"}; !sameStrings(
		intos(d.Inferred, packdecl.KindBriefing), want) {
		t.Fatalf("inferred briefing = %v, want %v — a contribution that names a source and an "+
			"audience carries no conventional file, and must still route",
			intos(d.Inferred, packdecl.KindBriefing), want)
	}
	text, prob := d.Pack.BriefingProseFor(d.Inferred[0])
	if text != "claude house rules" {
		t.Errorf("resolved prose = %q, want %q — the ADDRESSED source is what the destination "+
			"receives; a synthesized `from` of \"\" delivers the convention instead", text,
			"claude house rules")
	}
	if prob != "" {
		t.Errorf("resolved prose problem = %q, want none", prob)
	}
}

// The WRONG-CONTENT half on its own: the pack also happens to carry a conventional AGENTS.md, so
// the inference succeeds either way and only the CONTENT tells the two apart. This is the case
// that stays green when the routing is fixed and the synthesis is not.
func TestResolveDestinationsAddressedBriefingBeatsTheConventionalFile(t *testing.T) {
	claude := agentPack(t, "claude", packdecl.Contribution{Kind: packdecl.KindBriefing,
		Into: ".claude/CLAUDE.md", Agent: "claude"})
	house := addressedPack(t, "house", map[string]string{
		"AGENTS.md":       "everyone\n",
		"prose/claude.md": "claude only\n",
	}, packdecl.Contribution{Kind: packdecl.KindBriefing, From: "prose/claude.md",
		Agents: []string{"claude"}})

	d := house.ResolveDestinations([]*Pack{claude, house})
	if len(d.Inferred) != 1 {
		t.Fatalf("Inferred = %v, want one briefing destination", d.Inferred)
	}
	if got := d.Inferred[0].From; got != "prose/claude.md" {
		t.Errorf("inferred from = %q, want %q — the destination is borrowed, but the SOURCE was "+
			"declared and must survive the synthesis", got, "prose/claude.md")
	}
	text, _ := d.Pack.BriefingProseFor(d.Inferred[0])
	if text != "claude only" {
		t.Errorf("resolved prose = %q, want %q — a pack that happens to own an AGENTS.md must "+
			"not have it substituted for the file its contribution named", text, "claude only")
	}
}

// `skills` TAKES THE SAME FIELD AND THE PARALLEL IS EXACT (§4.1 line 195, OQ-BA4). Same two
// chokepoints, same fix: the source probe must ask the contribution's `from` (SkillsSourceDir
// already honors it) and the synthesis must carry it.
func TestResolveDestinationsRoutesAnAddressedSkillsFrom(t *testing.T) {
	claude := agentPack(t, "claude", packdecl.Contribution{Kind: packdecl.KindSkills,
		From: "skills", Into: ".claude/skills", Agent: "claude"})
	pi := agentPack(t, "pi", packdecl.Contribution{Kind: packdecl.KindSkills,
		From: "skills", Into: ".pi/agent/skills", Agent: "pi"})
	// No conventional `skills/` dir — only the addressed one.
	house := addressedPack(t, "house",
		map[string]string{"skills-claude/review/SKILL.md": "body\n"},
		packdecl.Contribution{Kind: packdecl.KindSkills, From: "skills-claude",
			Agents: []string{"claude"}})

	d := house.ResolveDestinations([]*Pack{claude, pi, house})
	if len(d.Orphaned) != 0 {
		t.Fatalf("Orphaned = %v — claude declared a skills destination and `agent: claude`",
			d.Orphaned)
	}
	if want := []string{".claude/skills"}; !sameStrings(
		intos(d.Inferred, packdecl.KindSkills), want) {
		t.Fatalf("inferred skills = %v, want %v", intos(d.Inferred, packdecl.KindSkills), want)
	}
	dir, prob := d.Pack.SkillsSourceDir(d.Inferred[0])
	if want := filepath.Join(house.Root, "skills-claude"); dir != want {
		t.Errorf("resolved skills source = %q, want %q — the declared `from` is what "+
			"hostskills.ComposeHostSkills reads per contribution", dir, want)
	}
	if prob != "" {
		t.Errorf("resolved skills problem = %q, want none", prob)
	}
}

// AN ADDRESSED CONTRIBUTION THAT MATCHES NOTHING IS REPORTED, NOT SILENT (risk R1) — even when
// the pack has no conventional file at all.
//
// This is the silent-skip defect at its sharpest: the content probe ran BEFORE the orphan branch,
// so a pack whose only content is addressed died at the probe and never reached the report. Both
// kinds, because both took the same skip.
func TestResolveDestinationsReportsAnAddressedSourceNothingCanDeliver(t *testing.T) {
	claude := agentPack(t, "claude",
		packdecl.Contribution{Kind: packdecl.KindBriefing, Into: ".claude/CLAUDE.md",
			Agent: "claude"},
		packdecl.Contribution{Kind: packdecl.KindSkills, From: "skills",
			Into: ".claude/skills", Agent: "claude"})
	house := addressedPack(t, "house", map[string]string{
		"prose/codex.md":               "codex only\n",
		"skills-codex/review/SKILL.md": "body\n",
	},
		packdecl.Contribution{Kind: packdecl.KindBriefing, From: "prose/codex.md",
			Agents: []string{"codex"}},
		packdecl.Contribution{Kind: packdecl.KindSkills, From: "skills-codex",
			Agents: []string{"codex"}})

	d := house.ResolveDestinations([]*Pack{claude, house})
	if len(d.Inferred) != 0 {
		t.Errorf("Inferred = %v for content addressed to an agent this set does not have",
			d.Inferred)
	}
	got := map[packdecl.Kind]int{}
	for _, o := range d.Orphaned {
		got[o.Kind]++
	}
	for _, want := range []packdecl.Kind{packdecl.KindBriefing, packdecl.KindSkills} {
		if got[want] != 1 {
			t.Errorf("Orphaned = %v, want %q exactly once — a pack whose whole content reaches "+
				"nothing must never go inert quietly, and must not be reported twice for one kind",
				d.Orphaned, want)
		}
	}
}

// AN ORPHAN CARRIES WHY IT IS ORPHANED, because since the audience selector there are two whys
// and their remedies are opposites:
//
//   - NO DESTINATION EXISTS for the kind, anywhere in the set. Fixed by selecting an agent pack
//     or by writing an `into`.
//   - A DESTINATION EXISTS and no destination's declared `agent` matches the contribution's
//     `agents`. Fixed by selecting the pack that OWNS that name, or by correcting the selector —
//     and NOT by writing an `into`, which validateContribution refuses beside `agents`.
//
// The audience is what tells them apart, so the report cannot say which one it is unless the
// resolver hands it over. One set holds both at once here: claude declares a briefing
// destination and owns the name `claude`, and declares no skills destination at all.
func TestResolveDestinationsOrphanCarriesItsAudience(t *testing.T) {
	claude := agentPack(t, "claude", packdecl.Contribution{Kind: packdecl.KindBriefing,
		Into: ".claude/CLAUDE.md", Agent: "claude"})
	house := addressedPack(t, "house", map[string]string{
		"prose/codex.md":         "codex only\n",
		"skills/review/SKILL.md": "body\n",
	}, packdecl.Contribution{Kind: packdecl.KindBriefing, From: "prose/codex.md",
		Agents: []string{"codex"}})

	d := house.ResolveDestinations([]*Pack{claude, house})
	if len(d.Orphaned) != 2 {
		t.Fatalf("Orphaned = %v, want two — one briefing (unmatched audience) and one skills "+
			"(no destination at all)", d.Orphaned)
	}
	byKind := map[packdecl.Kind]Orphan{}
	for _, o := range d.Orphaned {
		byKind[o.Kind] = o
	}
	if got := byKind[packdecl.KindBriefing].Agents; !sameStrings(got, []string{"codex"}) {
		t.Errorf("briefing orphan Agents = %v, want [codex] — a briefing destination exists and "+
			"is being written to; the audience it addressed is what nothing owns, and a report "+
			"that cannot see the audience tells this author to declare `into` instead", got)
	}
	if got := byKind[packdecl.KindSkills].Agents; len(got) != 0 {
		t.Errorf("skills orphan Agents = %v, want none — the conventional skills tree named no "+
			"audience, so it was eligible for every destination of its kind and there were none",
			got)
	}
}

// TWO UNMATCHED AUDIENCES ARE TWO FACTS, so the orphan dedup key is the kind AND the audience.
// Per kind alone (what it was while there was one reason to be orphaned) reports the first and
// drops the second, and the two can differ in exactly the thing the report is now keyed on.
func TestResolveDestinationsOrphansEachUnmatchedAudienceSeparately(t *testing.T) {
	claude := agentPack(t, "claude", packdecl.Contribution{Kind: packdecl.KindBriefing,
		Into: ".claude/CLAUDE.md", Agent: "claude"})
	house := addressedPack(t, "house", map[string]string{
		"prose/codex.md": "codex\n",
		"prose/agy.md":   "agy\n",
	},
		packdecl.Contribution{Kind: packdecl.KindBriefing, From: "prose/codex.md",
			Agents: []string{"codex"}},
		packdecl.Contribution{Kind: packdecl.KindBriefing, From: "prose/agy.md",
			Agents: []string{"agy"}})

	d := house.ResolveDestinations([]*Pack{claude, house})
	var got []string
	for _, o := range d.Orphaned {
		if o.Kind == packdecl.KindBriefing && len(o.Agents) == 1 {
			got = append(got, o.Agents[0])
		}
	}
	if want := []string{"codex", "agy"}; !sameStrings(got, want) {
		t.Errorf("orphaned briefing audiences = %v, want %v — each addressed contribution that "+
			"reached nothing is its own line in the report", got, want)
	}
}

// EACH ADDRESSED CONTRIBUTION IS RESOLVED ON ITS OWN — the multi-entry shape §4.1 shows, where one
// pack briefs claude from one file and pi from another. A union over the kind's audiences cannot
// express it: it yields both destinations with one source, so whichever file the union picked
// would reach both agents. The pairing is the assertion.
func TestResolveDestinationsPairsEachAddressedSourceWithItsOwnAudience(t *testing.T) {
	claude := agentPack(t, "claude", packdecl.Contribution{Kind: packdecl.KindBriefing,
		Into: ".claude/CLAUDE.md", Agent: "claude"})
	pi := agentPack(t, "pi", packdecl.Contribution{Kind: packdecl.KindBriefing,
		Into: ".pi/agent/AGENTS.md", Agent: "pi"})
	house := addressedPack(t, "house", map[string]string{
		"prose/claude.md": "for claude\n",
		"prose/pi.md":     "for pi\n",
	},
		packdecl.Contribution{Kind: packdecl.KindBriefing, From: "prose/claude.md",
			Agents: []string{"claude"}},
		packdecl.Contribution{Kind: packdecl.KindBriefing, From: "prose/pi.md",
			Agents: []string{"pi"}})

	d := house.ResolveDestinations([]*Pack{claude, pi, house})
	if want := []string{".claude/CLAUDE.md", ".pi/agent/AGENTS.md"}; !sameStrings(
		intos(d.Inferred, packdecl.KindBriefing), want) {
		t.Fatalf("inferred briefing = %v, want %v", intos(d.Inferred, packdecl.KindBriefing), want)
	}
	if want := []string{"prose/claude.md", "prose/pi.md"}; !sameStrings(
		froms(d.Inferred, packdecl.KindBriefing), want) {
		t.Fatalf("inferred from = %v, want %v — each destination gets the source addressed to IT",
			froms(d.Inferred, packdecl.KindBriefing), want)
	}
	for i, want := range []string{"for claude", "for pi"} {
		if text, _ := d.Pack.BriefingProseFor(d.Inferred[i]); text != want {
			t.Errorf("prose at %s = %q, want %q", d.Inferred[i].Into, text, want)
		}
	}
}

// SILENCE IS PER CONTRIBUTION: an addressed contribution is routed even when the pack ALSO
// declares an `into` for the same kind. A whole-kind "did this pack name a destination?" gate
// answers yes here and drops the addressed entry without a word — the same delivery-to-nowhere
// the `Into != ""` tightening exists to prevent, reached from the other side. The declared
// contribution is still honored EXACTLY: not rerouted, not duplicated, not widened.
func TestResolveDestinationsRoutesAnAddressedContributionBesideADeclaredOne(t *testing.T) {
	claude := agentPack(t, "claude", packdecl.Contribution{Kind: packdecl.KindBriefing,
		Into: ".claude/CLAUDE.md", Agent: "claude"})
	pi := agentPack(t, "pi", packdecl.Contribution{Kind: packdecl.KindBriefing,
		Into: ".pi/agent/AGENTS.md", Agent: "pi"})
	house := addressedPack(t, "house", map[string]string{
		"prose/house.md":  "house wide\n",
		"prose/claude.md": "claude only\n",
	},
		packdecl.Contribution{Kind: packdecl.KindBriefing, From: "prose/house.md",
			Into: ".house/AGENTS.md"},
		packdecl.Contribution{Kind: packdecl.KindBriefing, From: "prose/claude.md",
			Agents: []string{"claude"}})

	d := house.ResolveDestinations([]*Pack{claude, pi, house})
	if want := []string{".claude/CLAUDE.md"}; !sameStrings(
		intos(d.Inferred, packdecl.KindBriefing), want) {
		t.Fatalf("inferred briefing = %v, want %v — a declared `into` elsewhere in the manifest "+
			"must not silence the addressed entry beside it",
			intos(d.Inferred, packdecl.KindBriefing), want)
	}
	if got := intos(d.Pack.Decl.Contributions(), packdecl.KindBriefing); !sameStrings(got,
		[]string{".house/AGENTS.md", "", ".claude/CLAUDE.md"}) {
		t.Errorf("resolved contributions = %v, want the two declared ones untouched plus one "+
			"inferred destination", got)
	}
}

// THE ZERO-CEREMONY PATH IS UNCHANGED, and it is the case the empty `from` was right about: a pack
// with no manifest borrows the DESTINATION and never the content. The declaring pack here names a
// `from` of its own on BOTH kinds — the mutation that would copy it is invisible against the
// shipped `from: "skills"`, which is the conventional value the borrower resolves to anyway.
func TestResolveDestinationsZeroCeremonyBorrowsTheDestinationAndNotTheSource(t *testing.T) {
	claude := agentPack(t, "claude",
		packdecl.Contribution{Kind: packdecl.KindSkills, From: "vendor/skills",
			Into: ".claude/skills", Agent: "claude"},
		packdecl.Contribution{Kind: packdecl.KindBriefing, From: "vendor/prose.md",
			Into: ".claude/CLAUDE.md", Agent: "claude"})
	zc := zeroCeremonyPack(t, "zc", true, true)

	d := zc.ResolveDestinations([]*Pack{claude, zc})
	if len(d.Inferred) != 2 {
		t.Fatalf("Inferred = %v, want one skills and one briefing destination", d.Inferred)
	}
	for _, c := range d.Inferred {
		if c.From != "" {
			t.Errorf("inferred %s from = %q, want empty — a pack with no manifest named no source, "+
				"and the declaring pack's `from` is about ITS tree", string(c.Kind), c.From)
		}
	}
	// And the empty `from` still resolves to this pack's OWN conventional content, which is the
	// property the emptiness exists for.
	for _, c := range d.Inferred {
		switch c.Kind {
		case packdecl.KindSkills:
			dir, prob := d.Pack.SkillsSourceDir(c)
			if want := filepath.Join(zc.Root, "skills"); dir != want || prob != "" {
				t.Errorf("zero-ceremony skills source = %q (problem %q), want %q", dir, prob, want)
			}
		case packdecl.KindBriefing:
			text, prob := d.Pack.BriefingProseFor(c)
			if text != "# prose" || prob != "" {
				t.Errorf("zero-ceremony prose = %q (problem %q), want %q", text, prob, "# prose")
			}
		}
	}
}

// A DESTINATION THAT DECLARES NO IDENTITY IS NEVER SELECTED — and is still BROADCAST to (R4).
//
// The two halves are one rule read from both ends. A third-party agent pack that has not added
// `agent` cannot be named by any selector, because the design deleted the derivation that would
// have guessed a name for it (OQ-BA2); and a pack that names no audience must still reach it,
// because silence means broadcast (P2) and that is what makes this field safe to land before any
// pack adopts it.
func TestResolveDestinationsSkipsUnidentifiedDestinationsOnlyForASelector(t *testing.T) {
	claude := agentPack(t, "claude", packdecl.Contribution{Kind: packdecl.KindBriefing,
		Into: ".claude/CLAUDE.md", Agent: "claude"})
	// Declares a destination and no identity — the third-party agent pack of R4.
	anon := agentPack(t, "anon", packdecl.Contribution{Kind: packdecl.KindBriefing,
		Into: ".anon/AGENTS.md"})

	addressed := zeroCeremonyPack(t, "house", false, true)
	addressed.Decl = &packdecl.Manifest{Contributes: []packdecl.Contribution{{
		Kind: packdecl.KindBriefing, Agents: []string{"claude"}}}}
	d := addressed.ResolveDestinations([]*Pack{claude, anon, addressed})
	if want := []string{".claude/CLAUDE.md"}; !sameStrings(
		intos(d.Inferred, packdecl.KindBriefing), want) {
		t.Errorf("addressed inference = %v, want %v — an empty `agent` must not match a "+
			"selector, or every unidentified destination would receive every audience",
			intos(d.Inferred, packdecl.KindBriefing), want)
	}

	// The same set, an UNAUDIENCED pack: both destinations, including the unidentified one.
	zc := zeroCeremonyPack(t, "zc", false, true)
	d2 := zc.ResolveDestinations([]*Pack{claude, anon, zc})
	if want := []string{".claude/CLAUDE.md", ".anon/AGENTS.md"}; !sameStrings(
		intos(d2.Inferred, packdecl.KindBriefing), want) {
		t.Errorf("unaudienced inference = %v, want %v — silence means BROADCAST, and a pack "+
			"with no manifest has no way to opt in to anything else",
			intos(d2.Inferred, packdecl.KindBriefing), want)
	}
}

// A pack that DECLARES a destination is honored exactly: the inference must not widen what an
// existing manifest means by adding every other agent's dir to it.
func TestResolveDestinationsLeavesADeclaringPackAlone(t *testing.T) {
	claude := agentPack(t, "claude", packdecl.Contribution{
		Kind: packdecl.KindSkills, From: "skills", Into: ".claude/skills"})
	pi := agentPack(t, "pi", packdecl.Contribution{
		Kind: packdecl.KindSkills, From: "skills", Into: ".pi/agent/skills"})
	// Declares ONE destination and carries skills — the pre-F1 shape a real user's pack has.
	declaring := zeroCeremonyPack(t, "mine", true, false)
	declaring.Decl = &packdecl.Manifest{Contributes: []packdecl.Contribution{{
		Kind: packdecl.KindSkills, From: "skills", Into: ".claude/skills"}}}

	d := declaring.ResolveDestinations([]*Pack{claude, pi, declaring})
	if len(d.Inferred) != 0 {
		t.Errorf("Inferred = %v for a pack that declared its destination — a declaration is "+
			"honored EXACTLY; inferring on top of it would write into homes its author never named",
			d.Inferred)
	}
	if d.Pack != declaring {
		t.Error("a pack with nothing inferred must be returned as-is, not copied")
	}
}

// Per KIND, not per pack: declaring `skills` must not suppress the `briefing` inference. This
// is the case a pack-level "did it declare anything?" check gets wrong.
func TestResolveDestinationsInfersPerKind(t *testing.T) {
	claude := agentPack(t, "claude",
		packdecl.Contribution{Kind: packdecl.KindSkills, From: "skills", Into: ".claude/skills"},
		packdecl.Contribution{Kind: packdecl.KindBriefing, From: "AGENTS.md", Into: ".claude/CLAUDE.md"})
	half := zeroCeremonyPack(t, "half", true, true)
	half.Decl = &packdecl.Manifest{Contributes: []packdecl.Contribution{{
		Kind: packdecl.KindSkills, From: "skills", Into: ".mine/skills"}}}

	d := half.ResolveDestinations([]*Pack{claude, half})
	if want := []string{".claude/CLAUDE.md"}; !sameStrings(
		intos(d.Inferred, packdecl.KindBriefing), want) {
		t.Errorf("inferred briefing = %v, want %v — a pack that declared `skills` and no "+
			"`briefing` still needs its prose routed", intos(d.Inferred, packdecl.KindBriefing), want)
	}
	if got := intos(d.Inferred, packdecl.KindSkills); len(got) != 0 {
		t.Errorf("inferred skills = %v for a pack that declared its own — must be untouched", got)
	}
}

// A pack carrying content with NO destination anywhere in the set is ORPHANED, not silently
// empty. That is F1 reached by the other route: a content pack selected with no agent pack.
func TestResolveDestinationsReportsAnOrphanedKind(t *testing.T) {
	zc := zeroCeremonyPack(t, "zc", true, true)
	d := zc.ResolveDestinations([]*Pack{zc})
	if len(d.Inferred) != 0 {
		t.Fatalf("Inferred = %v with no other pack in the set", d.Inferred)
	}
	want := map[packdecl.Kind]bool{packdecl.KindSkills: true, packdecl.KindBriefing: true}
	if len(d.Orphaned) != len(want) {
		t.Fatalf("Orphaned = %v, want both carried kinds — a pack that reaches nothing must "+
			"never be silent", d.Orphaned)
	}
	for _, o := range d.Orphaned {
		if !want[o.Kind] {
			t.Errorf("Orphaned names %q, which the pack does not carry", o.Kind)
		}
	}
}

// An EMPTY pack — no skills, no prose — is orphaned for nothing. There is no content to route,
// so a per-destination line for it would be noise in a report whose whole value is being read.
func TestResolveDestinationsIgnoresAPackWithNoContent(t *testing.T) {
	claude := agentPack(t, "claude",
		packdecl.Contribution{Kind: packdecl.KindSkills, From: "skills", Into: ".claude/skills"},
		packdecl.Contribution{Kind: packdecl.KindBriefing, From: "AGENTS.md", Into: ".claude/CLAUDE.md"})
	empty := zeroCeremonyPack(t, "empty", false, false)

	d := empty.ResolveDestinations([]*Pack{claude, empty})
	if len(d.Inferred) != 0 || len(d.Orphaned) != 0 {
		t.Errorf("Inferred=%v Orphaned=%v for a pack carrying nothing — both must be empty",
			d.Inferred, d.Orphaned)
	}
}

// A skills dir holding only LOOSE FILES carries nothing: no tool reads a bare .md as a skill
// (hostskills.collectSkills counts directories only), so inferring a destination for it would
// promise a delivery that then reports zero entries.
func TestResolveDestinationsIgnoresALooseFileSkillsDir(t *testing.T) {
	claude := agentPack(t, "claude", packdecl.Contribution{
		Kind: packdecl.KindSkills, From: "skills", Into: ".claude/skills"})
	loose := zeroCeremonyPack(t, "loose", false, false)
	if err := os.MkdirAll(filepath.Join(loose.Root, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(loose.Root, "skills", "notes.md"),
		[]byte("notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	d := loose.ResolveDestinations([]*Pack{claude, loose})
	if len(d.Inferred) != 0 || len(d.Orphaned) != 0 {
		t.Errorf("Inferred=%v Orphaned=%v — a skills dir of loose files holds no skill",
			d.Inferred, d.Orphaned)
	}
}

// A WHITESPACE-ONLY AGENTS.md is not prose. The briefing render yields no block for it, so
// counting it as content would infer a destination and then report "ships no briefing prose".
func TestResolveDestinationsIgnoresBlankProse(t *testing.T) {
	claude := agentPack(t, "claude", packdecl.Contribution{
		Kind: packdecl.KindBriefing, From: "AGENTS.md", Into: ".claude/CLAUDE.md"})
	blank := zeroCeremonyPack(t, "blank", false, false)
	if err := os.WriteFile(filepath.Join(blank.Root, "AGENTS.md"),
		[]byte("\n \n\t\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	d := blank.ResolveDestinations([]*Pack{claude, blank})
	if len(d.Inferred) != 0 || len(d.Orphaned) != 0 {
		t.Errorf("Inferred=%v Orphaned=%v — a whitespace-only briefing file is not prose",
			d.Inferred, d.Orphaned)
	}
}

// The set the inference reads is the ORIGINAL one, so the result cannot depend on iteration
// order: a zero-ceremony pack must never become a destination SOURCE for the next one. Two
// content packs and no agent pack must BOTH report orphaned.
func TestResolveDestinationsDoesNotChainInferredDestinations(t *testing.T) {
	claude := agentPack(t, "claude", packdecl.Contribution{
		Kind: packdecl.KindSkills, From: "skills", Into: ".claude/skills"})
	a := zeroCeremonyPack(t, "a", true, false)
	b := zeroCeremonyPack(t, "b", true, false)

	// With claude present, both borrow from claude — and only from claude.
	_, outcomes := ResolveDestinations([]*Pack{claude, a, b})
	for i, name := range []string{"a", "b"} {
		got := intos(outcomes[i+1].Inferred, packdecl.KindSkills)
		if want := []string{".claude/skills"}; !sameStrings(got, want) {
			t.Errorf("%s inferred %v, want %v — a destination one content pack only INHERITED "+
				"must not become a source for the next", name, got, want)
		}
	}
	// With no agent pack, neither can borrow from the other.
	_, none := ResolveDestinations([]*Pack{a, b})
	for i, name := range []string{"a", "b"} {
		if len(none[i].Inferred) != 0 {
			t.Errorf("%s inferred %v with no agent pack selected", name, none[i].Inferred)
		}
		if len(none[i].Orphaned) == 0 {
			t.Errorf("%s reported no orphaned kind while carrying skills nothing can deliver", name)
		}
	}
}

// The ORIGINAL pack's declaration is never mutated. Embedded() caches its packs process-wide
// and the same *Pack reaches the render loop, the prune candidates and the overlay collector —
// so appending in place would make one pack's inference visible to every later reader, including
// passes whose job is to compare against what the pack actually declares.
func TestResolveDestinationsDoesNotMutateTheOriginal(t *testing.T) {
	claude := agentPack(t, "claude", packdecl.Contribution{
		Kind: packdecl.KindSkills, From: "skills", Into: ".claude/skills"})
	zc := zeroCeremonyPack(t, "zc", true, false)

	d := zc.ResolveDestinations([]*Pack{claude, zc})
	if n := len(zc.Decl.Contributions()); n != 0 {
		t.Errorf("the original pack's declaration grew to %d contribution(s) — the inference "+
			"must resolve into a COPY", n)
	}
	if d.Pack == zc {
		t.Error("the resolved pack is the original — an inferring resolution must return a copy")
	}
	if n := len(d.Pack.Decl.Contributions()); n != 1 {
		t.Errorf("the resolved copy declares %d contribution(s), want 1", n)
	}
	// Identity-carrying fields must survive the copy: every downstream pass keys on Name,
	// and Root is where its content is read from.
	if d.Pack.Name != zc.Name || d.Pack.Root != zc.Root {
		t.Errorf("the copy lost pack identity: %+v vs %+v", d.Pack, zc)
	}
}
