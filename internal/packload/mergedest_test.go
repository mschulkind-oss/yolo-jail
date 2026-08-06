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
	for _, k := range d.Orphaned {
		if !want[k] {
			t.Errorf("Orphaned names %q, which the pack does not carry", k)
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
	// Identity-carrying fields must survive the copy: every downstream pass keys on Name, and
	// MayAccessHost gates the origin checks.
	if d.Pack.Name != zc.Name || d.Pack.Root != zc.Root || d.Pack.MayAccessHost != zc.MayAccessHost {
		t.Errorf("the copy lost pack identity: %+v vs %+v", d.Pack, zc)
	}
}
