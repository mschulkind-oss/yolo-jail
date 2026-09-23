package cli

// packlintdeliveries_test.go pins pack-briefing-defaults.md §3.6 at the one place an author
// looks before a launch: `yolo pack lint` lists EVERY delivery, the implicit broadcast included,
// and names every conventional-looking file it will not ship (P6).
//
// Every test here goes through packMain, the verb's own entry point, so deleting the listing or
// the notes call in packLint fails them — a test on printPackDeliveries alone would stay green
// with the call site gone.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// lintReport runs `yolo pack lint dir` and returns rc and the whole report.
func lintReport(t *testing.T, dir string) (int, string) {
	t.Helper()
	var out, errw bytes.Buffer
	rc := packMain([]string{"lint", dir}, &out, &errw, false)
	return rc, out.String() + errw.String()
}

// hasLine reports whether some line of report contains every one of parts.
func hasLine(report string, parts ...string) bool {
	for _, line := range strings.Split(report, "\n") {
		all := true
		for _, p := range parts {
			if !strings.Contains(line, p) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

// THE matt SHAPE (§2.2, §3.7): house rules in briefing/ plus one addressed file outside it.
// Before per-file governance the addressed line switched the house rules off and lint said
// nothing; now lint must show BOTH deliveries, and say which one no manifest line names.
func TestPackLintListsTheImplicitBroadcastBesideAnAddressedFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "briefing", "house-rules.md"), "House rules.\n")
	writeFile(t, filepath.Join(dir, "files", "pi-rules.md"), "Pi rules.\n")
	writeFile(t, filepath.Join(dir, "pack.json"), `{"name":"matt","contributes":[`+
		`{"kind":"briefing","agents":["pi"],"from":"files/pi-rules.md"}]}`)

	rc, report := lintReport(t, dir)
	if rc != 0 {
		t.Fatalf("lint rc=%d:\n%s", rc, report)
	}
	if !hasLine(report, "briefing/house-rules.md", "every agent (implicit broadcast)") {
		t.Errorf("lint does not list the implicit broadcast of briefing/house-rules.md:\n%s", report)
	}
	if !hasLine(report, "files/pi-rules.md", "→ pi", "contributes[0]") {
		t.Errorf("lint does not list the addressed delivery with the line that routes it:\n%s", report)
	}
	// The addressed file is DECLARED, so it must not be labelled implicit.
	if hasLine(report, "files/pi-rules.md", "implicit") {
		t.Errorf("a declared delivery is labelled implicit:\n%s", report)
	}
}

// A declared broadcast (`{"kind":"briefing"}`, P2) is listed as one, distinct from the implicit
// one: both reach every agent, but only one is written down.
func TestPackLintListsADeclaredBroadcastAsDeclared(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "briefing", "a.md"), "A.\n")
	writeFile(t, filepath.Join(dir, "briefing", "b.md"), "B.\n")
	writeFile(t, filepath.Join(dir, "pack.json"), `{"name":"p","contributes":[{"kind":"briefing"}]}`)

	rc, report := lintReport(t, dir)
	if rc != 0 {
		t.Fatalf(`{"kind":"briefing"} must lint clean (P2):%d\n%s`, rc, report)
	}
	for _, rel := range []string{"briefing/a.md", "briefing/b.md"} {
		if !hasLine(report, rel, "every agent (declared broadcast)", "contributes[0]") {
			t.Errorf("%s is not listed as a declared broadcast:\n%s", rel, report)
		}
	}
	// And in filename order, which is the order a destination composes them in (R6).
	if strings.Index(report, "briefing/a.md") > strings.Index(report, "briefing/b.md") {
		t.Errorf("the listing is not in byte-wise filename order:\n%s", report)
	}
	if hasLine(report, "briefing/", "implicit") {
		t.Errorf("a governed briefing/ file is labelled implicit:\n%s", report)
	}
}

// §3.6's not-shipped line, for each root repository instruction file: INFO, not a warning or a
// failure — for a repository pack, not shipping it is correct — and never listed as a delivery.
func TestPackLintNamesRootInstructionFilesAsNotShipped(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "briefing", "rules.md"), "Rules.\n")
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md"} {
		writeFile(t, filepath.Join(dir, name), "Work in this repo like so.\n")
	}

	rc, report := lintReport(t, dir)
	if rc != 0 {
		t.Fatalf("a root AGENTS.md is correct for a repository pack and must not fail lint "+
			"(rc=%d):\n%s", rc, report)
	}
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md"} {
		want := name + ": not shipped: this is the repository's own agent instructions — " +
			"ship prose under briefing/"
		if !hasLine(report, "ℹ", want) {
			t.Errorf("lint does not name %s as not shipped, at info:\n%s", name, report)
		}
		if hasLine(report, "→", name) {
			t.Errorf("%s is listed as a delivery:\n%s", name, report)
		}
		if hasLine(report, "✗", name) || hasLine(report, "⚠", name) {
			t.Errorf("%s drew a failure or warning — it is info (§3.6):\n%s", name, report)
		}
	}
	if strings.Contains(report, "nothing reads") {
		t.Errorf("a root instruction file was reported as unread content:\n%s", report)
	}
}

// §3.6's not-read line: a subdirectory of briefing/ (named ONCE, however many files it holds)
// and a non-*.md file in it. A file a declared `from` names IS delivered, even from a
// subdirectory, so it is never named — the note is about the convention, not about `from`.
func TestPackLintNamesWhatBriefingDoesNotRead(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "briefing", "ok.md"), "Read.\n")
	writeFile(t, filepath.Join(dir, "briefing", "notes.txt"), "Not read.\n")
	writeFile(t, filepath.Join(dir, "briefing", "deep", "a.md"), "Not read.\n")
	writeFile(t, filepath.Join(dir, "briefing", "deep", "b.md"), "Not read.\n")
	writeFile(t, filepath.Join(dir, "briefing", "named", "x.md"), "Read, by name.\n")
	writeFile(t, filepath.Join(dir, "pack.json"), `{"name":"p","contributes":[`+
		`{"kind":"briefing","from":"briefing/named/x.md","agents":["claude"]}]}`)

	rc, report := lintReport(t, dir)
	if rc != 0 {
		t.Fatalf("lint rc=%d:\n%s", rc, report)
	}
	const why = "not read: briefing/ is read one level deep, *.md only"
	for _, rel := range []string{"briefing/notes.txt", "briefing/deep/"} {
		if !hasLine(report, "ℹ", rel+": "+why) {
			t.Errorf("lint does not name %s as not read:\n%s", rel, report)
		}
	}
	if n := strings.Count(report, "briefing/deep/: "); n != 1 {
		t.Errorf("a subdirectory must be named once, got %d:\n%s", n, report)
	}
	if strings.Contains(report, "briefing/named/: ") || strings.Contains(report, "named/x.md: not read") {
		t.Errorf("a file a declared `from` delivers was named as not read:\n%s", report)
	}
	if !hasLine(report, "briefing/named/x.md", "→ claude") ||
		!hasLine(report, "briefing/ok.md", "implicit broadcast") {
		t.Errorf("the delivered files are not listed:\n%s", report)
	}
}

// §10: "`yolo pack init`, followed by the scaffold's own advice, lints as TWO deliveries." The
// advice is followed literally — the manifest is the bytes the README shows — so a scaffold whose
// advice switches the broadcast off (the old AGENTS.md scaffold, §2.2) fails here.
func TestPackInitAdviceAddsToTheBroadcast(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "scaf")
	var out, errw bytes.Buffer
	if rc := packMain([]string{"init", dir}, &out, &errw, false); rc != 0 {
		t.Fatalf("init rc=%d: %s", rc, errw.String())
	}
	readme, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := scaffoldAddressedExample("scaf")
	if !strings.Contains(string(readme), manifest) {
		t.Fatalf("the README does not show the addressed manifest this test follows:\n%s", readme)
	}
	for _, want := range []string{
		// narrowing is a contribution NAMING a briefing/ file (§3.6)
		`"from": "briefing/scaf.md", "agents": ["claude"]`,
		"still reaches every agent",
	} {
		if !strings.Contains(string(readme), want) {
			t.Errorf("the README advice is missing %q:\n%s", want, readme)
		}
	}
	writeFile(t, filepath.Join(dir, "pack.json"), manifest)
	writeFile(t, filepath.Join(dir, "prose", "claude.md"), "Claude only.\n")

	rc, report := lintReport(t, dir)
	if rc != 0 {
		t.Fatalf("the scaffold, after its own advice, does not lint clean (rc=%d):\n%s", rc, report)
	}
	if !hasLine(report, "briefing/scaf.md", "every agent (implicit broadcast)") ||
		!hasLine(report, "prose/claude.md", "→ claude") {
		t.Errorf("following the scaffold's advice must yield TWO briefing deliveries — the "+
			"broadcast kept, the addressed file added:\n%s", report)
	}
}

// The scaffold never names its prose file after a repository instruction file, even for a pack
// directory called that: briefing/CLAUDE.md is refused (OQ-PB2), so the scaffold would fail its
// own lint.
func TestPackInitNeverScaffoldsAReservedBriefingName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "CLAUDE")
	var out, errw bytes.Buffer
	if rc := packMain([]string{"init", dir}, &out, &errw, false); rc != 0 {
		t.Fatalf("init rc=%d: %s", rc, errw.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "briefing", "CLAUDE.md")); err == nil {
		t.Error("init scaffolded briefing/CLAUDE.md, which LoadDir refuses")
	}
	if rc, report := lintReport(t, dir); rc != 0 {
		t.Errorf("the scaffold does not lint clean (rc=%d):\n%s", rc, report)
	}
}

// §3.4 at lint: a declared briefing `from` that does not exist delivers nothing and FAILS lint —
// the treatment a missing `skills` source already gets — and no other file is listed in its
// place. The pack's own briefing/ files still broadcast (P3), and the unclaimed-content advice
// does not pile on.
func TestPackLintFailsAMissingDeclaredBriefingFrom(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "briefing", "house.md"), "House.\n")
	writeFile(t, filepath.Join(dir, "prose", "claude.md"), "Close, but not named.\n")
	writeFile(t, filepath.Join(dir, "pack.json"), `{"name":"p","contributes":[`+
		`{"kind":"briefing","from":"prose/claud.md","agents":["claude"]}]}`)

	rc, report := lintReport(t, dir)
	if rc == 0 {
		t.Fatalf("a declared briefing `from` that does not exist must fail lint:\n%s", report)
	}
	if !hasLine(report, "✗", "prose/claud.md", "no prose delivered") {
		t.Errorf("lint does not name the missing declared source:\n%s", report)
	}
	if strings.Contains(report, "nothing reads") {
		t.Errorf("the precise diagnosis must suppress the unclaimed-content advice:\n%s", report)
	}
}

// §4's three new refusals reach lint, each naming its fix: a reserved `from`, a reserved name
// inside briefing/, and two contributions naming one source (OQ-PB5, naming both).
func TestPackLintRefusesTheBriefingDefaultsExceptions(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{"reserved from", map[string]string{
			"AGENTS.md": "prose\n",
			"pack.json": `{"name":"p","contributes":[{"kind":"briefing","from":"AGENTS.md"}]}`,
		}, []string{"AGENTS.md", "git mv"}},
		{"reserved name inside briefing/", map[string]string{
			"briefing/AGENTS.md": "prose\n",
		}, []string{"briefing/AGENTS.md", "git mv"}},
		{"one source, two contributions", map[string]string{
			"briefing/a.md": "prose\n",
			"pack.json": `{"name":"p","contributes":[` +
				`{"kind":"briefing","agents":["claude"]},{"kind":"briefing","agents":["pi"]}]}`,
		}, []string{"contributes[0]", "contributes[1]"}},
		{"from on a destination", map[string]string{
			"x.md": "prose\n",
			"pack.json": `{"name":"p","contributes":[` +
				`{"kind":"briefing","agent":"acme","into":".acme/A.md","from":"x.md"}]}`,
		}, []string{`"agents":["acme"]`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for rel, body := range tc.files {
				writeFile(t, filepath.Join(dir, filepath.FromSlash(rel)), body)
			}
			rc, report := lintReport(t, dir)
			if rc == 0 {
				t.Fatalf("lint accepted it:\n%s", report)
			}
			for _, w := range tc.want {
				if !hasLine(report, "✗", w) {
					t.Errorf("no failure line naming %q:\n%s", w, report)
				}
			}
		})
	}
}

// The advisory on a content `into` an agent pack owns, for briefing: it names what the line
// governs and that dropping it WIDENS — the correction of the old "adds nothing (drop it…)"
// (§2.6). The skills half is TestPackLintNamesTheOwningPack.
func TestPackLintAdvisorySaysDroppingABriefingIntoWidens(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "briefing", "rules.md"), "Rules.\n")
	writeFile(t, filepath.Join(dir, "pack.json"), `{"name":"p","contributes":[`+
		`{"kind":"briefing","into":".claude/CLAUDE.md"}]}`)

	rc, report := lintReport(t, dir)
	if rc != 0 {
		t.Fatalf("lint rc=%d:\n%s", rc, report)
	}
	if !hasLine(report, "ℹ", "contributes[0]", "already declared by the claude pack",
		"every briefing/*.md no other contribution names", "WIDENS", `agents: ["claude"]`) {
		t.Errorf("the advisory does not state what the line routes and what dropping it does:\n%s",
			report)
	}
	if strings.Contains(report, "adds nothing") {
		t.Errorf("the advisory still says the line adds nothing:\n%s", report)
	}
	if !hasLine(report, "briefing/rules.md", "→ .claude/CLAUDE.md", "contributes[0]") {
		t.Errorf("the delivery listing does not show the routed file:\n%s", report)
	}
}

// `yolo pack --help` states the new convention (§3.6): prose under briefing/, a root AGENTS.md
// never shipped, and silence as broadcast in a manifest too.
func TestPackUsageStatesTheBriefingConvention(t *testing.T) {
	var out, errw bytes.Buffer
	if rc := packMain([]string{"--help"}, &out, &errw, false); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	got := out.String()
	for _, want := range []string{"briefing/", "NEVER shipped", "broadcast"} {
		if !strings.Contains(got, want) {
			t.Errorf("pack --help does not state %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "an AGENTS.md at the pack") || strings.Contains(got, "briefing prose (AGENTS.md)") {
		t.Errorf("pack --help still teaches a root AGENTS.md as the prose source:\n%s", got)
	}
}

// Per-file governance for skills (§3.3): a declared, non-conventional skills source does not
// switch off the pack's skills/ tree, which keeps broadcasting — so lint must list it AND hold
// it to the SKILL.md rule. Reading the declared sources alone (the old SkillsSources gate) would
// pass a broken skill in a tree every agent receives.
func TestPackLintChecksTheImplicitSkillsTreeBesideADeclaredOne(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "my-skills", "good", "SKILL.md"),
		"---\nname: good\ndescription: d\n---\nbody\n")
	writeFile(t, filepath.Join(dir, "skills", "fine", "SKILL.md"),
		"---\nname: fine\ndescription: d\n---\nbody\n")
	writeFile(t, filepath.Join(dir, "pack.json"), `{"name":"p","contributes":[`+
		`{"kind":"skills","from":"my-skills","agents":["claude"]}]}`)

	rc, report := lintReport(t, dir)
	if rc != 0 {
		t.Fatalf("lint rc=%d:\n%s", rc, report)
	}
	if !hasLine(report, "skills/ → every agent (implicit broadcast)") ||
		!hasLine(report, "my-skills/ → claude", "contributes[0]") {
		t.Errorf("lint does not list both skills deliveries:\n%s", report)
	}

	writeFile(t, filepath.Join(dir, "skills", "broken", "notes.md"), "x")
	rc, report = lintReport(t, dir)
	if rc == 0 || !hasLine(report, "✗", "skills/broken has no SKILL.md") {
		t.Errorf("a broken skill in the implicitly-broadcast skills/ tree must fail lint "+
			"(rc=%d):\n%s", rc, report)
	}
}

// A root AGENTS.md is the repository's, not unread CONTENT: a config-only pack that lives in a
// repository (and so carries one) must lint clean, with the info line and without "stages files
// nothing reads" — which is the rule for a pack whose content lands nowhere.
func TestPackLintDoesNotCountARootAgentsMdAsUnreadContent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pack.json"), `{"name":"cfg","contributes":[{"kind":"config",`+
		`"config":[{"agent":"acme","name":"settings","codec":"json","path":"~/.acme/s.json",`+
		`"managed":{"a":1}}]}]}`)
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "How to work in this repository.\n")

	rc, report := lintReport(t, dir)
	if rc != 0 {
		t.Fatalf("a config pack carrying its repository's AGENTS.md must lint clean (rc=%d):\n%s",
			rc, report)
	}
	if strings.Contains(report, "nothing reads") {
		t.Errorf("the repository's AGENTS.md was reported as unread content:\n%s", report)
	}
	if !hasLine(report, "ℹ", "AGENTS.md: not shipped") {
		t.Errorf("the info line is missing:\n%s", report)
	}
}
