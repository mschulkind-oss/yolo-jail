package cli

// applyhostzeroceremony_test.go is the END-TO-END gate for finding F1: a pack with `skills/` +
// `AGENTS.md` and NO pack.json must render to a real home, not silently render nothing.
//
// The shipped behavior these pin against: `pack lint` said `✓ pack ok — 2 file(s) stage`,
// `apply --host --assert` printed not one line about the pack, and `~/.claude/skills` did not
// exist afterwards. The cause was a notch asymmetry — packload.SkillsSourceDirs infers the
// conventional dir for a manifest-less pack (so the JAIL merges it), while the host render
// iterated Decl.Contributions() and found none.
//
// Every test uses a t.TempDir() home with XDG_CONFIG_HOME inside it. The real $HOME is never
// read or written.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// zeroCeremonyFixture writes a pack with NO pack.json — a skills tree plus an AGENTS.md, the
// layout `yolo pack --help` promotes — and selects it in a user-scope config alongside
// `alongside` (a raw `packs` list fragment, so a test can select agent packs or none).
func zeroCeremonyFixture(t *testing.T, alongside string) (home, packDir string) {
	t.Helper()
	home = t.TempDir()
	packDir = filepath.Join(t.TempDir(), "zc")
	writeFile(t, filepath.Join(packDir, "skills", "zcskill", "SKILL.md"),
		"---\nname: zcskill\ndescription: d\n---\nZero-ceremony body.\n")
	writeFile(t, filepath.Join(packDir, "AGENTS.md"), "Zero-ceremony prose.\n")

	entry := `{"source":"file://` + packDir + `","name":"zc"}`
	if alongside != "" {
		entry = alongside + "," + entry
	}
	selectPacks(t, home, entry)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home, packDir
}

// THE F1 ASSERTION. Selected alongside `claude`, a manifest-less pack's skill and prose reach
// the destinations `claude` declares. Before the fix this home came out empty.
func TestApplyHostZeroCeremonyPackDelivers(t *testing.T) {
	home, _ := zeroCeremonyFixture(t, `"claude"`)

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("apply --host --assert rc=%d\n%s", rc, report)
	}
	// UNNAMESPACED, even though `claude` — the pack that NAMES this destination — is namespaced.
	// The inference used to inherit the destination's tier, which is what S2 removed: a borrowed
	// destination is a destination, not a naming policy, and a pack that declared nothing cannot
	// have made the positive choice `skills_tier` now requires.
	skill := filepath.Join(home, ".claude", "skills", "zcskill", "SKILL.md")
	if _, err := os.Stat(skill); err != nil {
		t.Fatalf("a zero-ceremony pack's skill did not reach %s: %v\n"+
			"F1: the jail infers this destination and the host must too\nreport:\n%s",
			skill, err, report)
	}
	brief, err := os.ReadFile(filepath.Join(home, ".claude", "CLAUDE.md"))
	if err != nil {
		t.Fatalf("a zero-ceremony pack's prose did not reach ~/.claude/CLAUDE.md: %v\n%s",
			err, report)
	}
	if !strings.Contains(string(brief), "Zero-ceremony prose.") {
		t.Errorf("the briefing does not carry the pack's prose:\n%s", brief)
	}
}

// The inference is NAMED, not silent. yolo is writing into a directory the pack never mentioned,
// so the report has to say which one and why — otherwise "where did this skill come from?" has
// no answer in the output that produced it.
func TestApplyHostZeroCeremonyNamesTheInferredDestination(t *testing.T) {
	zeroCeremonyFixture(t, `"claude"`)

	rc, report := applyWith(t, false, nil) // observe: the preview must say it too
	if rc != 0 {
		t.Fatalf("observe rc=%d\n%s", rc, report)
	}
	for _, want := range []string{"declares no destination", ".claude/skills", ".claude/CLAUDE.md"} {
		if !strings.Contains(report, want) {
			t.Errorf("the report does not mention %q — an inferred destination must be named:\n%s",
				want, report)
		}
	}
}

// Selected alongside SEVERAL agent packs, one zero-ceremony pack reaches all of them. This is
// the headline win the migration was after: five personal skills going from one agent to all of
// them, without a manifest.
func TestApplyHostZeroCeremonyReachesEveryAgentPack(t *testing.T) {
	home, _ := zeroCeremonyFixture(t, `"claude","pi","codex"`)

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("apply --host --assert rc=%d\n%s", rc, report)
	}
	// ONE NAME AT ALL THREE, which is S2's outcome and the reason this list is now parallel. It
	// used to read `.claude/skills/zc/skills/zcskill` against two bare paths, because the tier was
	// inherited per destination — so this one pack's one skill was `/zc:zcskill` in Claude and
	// `/zcskill` in pi and codex. The pack never chose either, and could not have: it has no
	// manifest.
	for _, rel := range []string{
		filepath.Join(".claude", "skills", "zcskill", "SKILL.md"),
		filepath.Join(".pi", "agent", "skills", "zcskill", "SKILL.md"),
		filepath.Join(".codex", "skills", "zcskill", "SKILL.md"),
	} {
		if _, err := os.Stat(filepath.Join(home, rel)); err != nil {
			t.Errorf("the zero-ceremony skill did not reach ~/%s: %v", rel, err)
		}
	}
	if !strings.Contains(report, "zcskill") {
		t.Errorf("report never names the delivered skill:\n%s", report)
	}
	// The invocation name is identical everywhere — the split-brain S2 removed.
	if n := countLines(report, "invoke as /zc:zcskill"); n != 0 {
		t.Errorf("a pack with NO manifest was namespaced at some destination, got %d such "+
			"lines:\n%s", n, report)
	}
}

// A pack that reaches NOTHING is refused by name and non-zero. This is F1's other route: a
// content pack selected with no agent pack has no destination to borrow, so it renders nothing —
// and a pack that contributes nothing anywhere is always a mistake, never a silent success.
func TestApplyHostZeroCeremonyWithNoAgentPackIsRefused(t *testing.T) {
	zeroCeremonyFixture(t, "")

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc == 0 {
		t.Errorf("a pack that renders NOTHING exited 0 — it must be refused by name:\n%s", report)
	}
	if !strings.Contains(report, "zc") || !strings.Contains(report, "no pack in `packs` names a") {
		t.Errorf("the refusal does not name the pack and the cause:\n%s", report)
	}
}

// NO REGRESSION for a pack WITH a manifest: it renders to exactly the destination it declared,
// and gains none of the others in the set. The inference must not widen what a declaration
// means — that would write into homes the author never named.
func TestApplyHostDeclaringPackIsUnaffectedByTheInference(t *testing.T) {
	home := t.TempDir()
	packDir := filepath.Join(t.TempDir(), "declaring")
	writeFile(t, filepath.Join(packDir, "pack.json"),
		`{"name":"declaring","description":"d","contributes":[`+
			`{"kind":"skills","from":"skills","into":".claude/skills"}]}`)
	writeFile(t, filepath.Join(packDir, "skills", "decskill", "SKILL.md"),
		"---\nname: decskill\ndescription: d\n---\nbody\n")
	writeFile(t, filepath.Join(packDir, "AGENTS.md"), "Declaring prose.\n")
	// `pi` is selected too, so an inference that ignored the declaration would land there.
	selectPacks(t, home, `"claude","pi",{"source":"file://`+packDir+`","name":"declaring"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("apply --host --assert rc=%d\n%s", rc, report)
	}
	// The declared destination, honored — and flat, as declared, not claude's namespaced tier.
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "decskill", "SKILL.md")); err != nil {
		t.Fatalf("the declared destination did not receive the skill: %v\n%s", err, report)
	}
	// And NOT pi's, which the pack never named.
	if _, err := os.Stat(filepath.Join(home, ".pi", "agent", "skills", "decskill")); err == nil {
		t.Error("a DECLARED skills destination was widened to another agent's dir — a " +
			"declaration must be honored exactly")
	}
}

// A pack that declares `skills` and carries an incidental AGENTS.md is the ordinary shape of a
// content pack. Its unrouted prose is named as inert (ruling R2's posture for an orphaned
// config-overlay) but must NOT fail the apply: the pack delivered what it declared.
func TestApplyHostPartlyDeclaringPackReportsInertProseWithoutFailing(t *testing.T) {
	home := t.TempDir()
	packDir := filepath.Join(t.TempDir(), "partly")
	// No agent pack is selected, so nothing anywhere names a briefing destination.
	writeFile(t, filepath.Join(packDir, "pack.json"),
		`{"name":"partly","description":"d","contributes":[`+
			`{"kind":"skills","from":"skills","into":".partly/skills"}]}`)
	writeFile(t, filepath.Join(packDir, "skills", "pskill", "SKILL.md"),
		"---\nname: pskill\ndescription: d\n---\nbody\n")
	writeFile(t, filepath.Join(packDir, "AGENTS.md"), "Partly prose.\n")
	selectPacks(t, home, `{"source":"file://`+packDir+`","name":"partly"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("a pack that delivered its declared skills must not fail over an unrouted "+
			"AGENTS.md (ruling R2: inert is named, not an error): rc=%d\n%s", rc, report)
	}
	if _, err := os.Stat(filepath.Join(home, ".partly", "skills", "pskill", "SKILL.md")); err != nil {
		t.Fatalf("the declared skill did not land: %v\n%s", err, report)
	}
	if !strings.Contains(report, "no effect") {
		t.Errorf("the unrouted briefing was not reported as inert — never silent:\n%s", report)
	}
}

// IDEMPOTENT: a second --assert re-reports the inference and changes nothing about the briefing.
// The inference must be a property of the config, not of whether a previous apply happened.
func TestApplyHostZeroCeremonyIsIdempotent(t *testing.T) {
	home, _ := zeroCeremonyFixture(t, `"claude"`)

	if rc, report := applyWith(t, true, strings.NewReader("y\n")); rc != 0 {
		t.Fatalf("first apply rc=%d\n%s", rc, report)
	}
	briefPath := filepath.Join(home, ".claude", "CLAUDE.md")
	first, err := os.ReadFile(briefPath)
	if err != nil {
		t.Fatal(err)
	}
	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("second apply rc=%d\n%s", rc, report)
	}
	second, err := os.ReadFile(briefPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("the briefing changed on re-apply:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if strings.Count(string(second), "Zero-ceremony prose.") != 1 {
		t.Errorf("the pack's prose appears more than once — the managed block must be rewritten "+
			"in place:\n%s", second)
	}
}
