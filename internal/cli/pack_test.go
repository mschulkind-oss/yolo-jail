package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// `pack init` must scaffold a pack that `pack lint` accepts. If the scaffold did not
// lint clean, every author's first action would produce an error — and the two would
// be free to drift apart.
func TestPackInitScaffoldLintsClean(t *testing.T) {
	dir := t.TempDir()
	var out, errw bytes.Buffer
	if rc := packMain([]string{"init", dir}, &out, &errw, false); rc != 0 {
		t.Fatalf("init rc = %d: %s", rc, errw.String())
	}
	for _, want := range []string{"AGENTS.md", "SKILL.md", "README.md"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("init did not create %s:\n%s", want, out.String())
		}
	}
	out.Reset()
	errw.Reset()
	if rc := packMain([]string{"lint", dir}, &out, &errw, false); rc != 0 {
		t.Fatalf("scaffolded pack does not lint clean: rc %d\n%s%s", rc, out.String(), errw.String())
	}
}

// init must be safe to re-run: it reports skips rather than clobbering an author's
// edited files.
func TestPackInitDoesNotClobber(t *testing.T) {
	dir := t.TempDir()
	var out, errw bytes.Buffer
	packMain([]string{"init", dir}, &out, &errw, false)
	edited := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(edited, []byte("MY OWN PROSE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if rc := packMain([]string{"init", dir}, &out, &errw, false); rc != 0 {
		t.Fatal(errw.String())
	}
	if !strings.Contains(out.String(), "skip AGENTS.md") {
		t.Errorf("re-run should report a skip:\n%s", out.String())
	}
	if data, _ := os.ReadFile(edited); !strings.Contains(string(data), "MY OWN PROSE") {
		t.Error("init clobbered an edited file")
	}
}

// lint runs the REAL staging rules, so an author hits a refusal before a consumer's jail
// does. A linter that disagreed with the stager would be worse than none.
//
// The rule exercised here is the ESCAPING SYMLINK, which is what remains of the two. It
// used to be the exec bit, and that is gone with the gate (packstage's package doc says
// why); the property under test was never the exec bit itself but that a staging refusal
// reaches the report at all.
//
// The refusal is reported on STDOUT with the other lint problems, not stderr: a staging
// failure is collected as a problem rather than returned on, so that it prints alongside
// the manifest validation (see TestPackLintReportsStagingRefusalAndManifestTogether).
// One problem list in one stream beats a refusal on stderr and the explanation on stdout.
func TestPackLintReportsStagingRefusals(t *testing.T) {
	dir := t.TempDir()
	var out, errw bytes.Buffer
	packMain([]string{"init", dir}, &out, &errw, false)
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	out.Reset()
	errw.Reset()
	if rc := packMain([]string{"lint", dir}, &out, &errw, false); rc == 0 {
		t.Fatal("expected lint to fail on a symlink escaping the pack root")
	}
	report := out.String() + errw.String()
	if !strings.Contains(report, "escape.md") {
		t.Errorf("lint error should name the offending file: %s", report)
	}
}

// A staging refusal and a manifest error must print TOGETHER.
//
// This is the regression test for the masking bug: `packLint` used to return as soon as
// staging failed, so an author saw one problem, fixed it, ran again, and met the next.
// The case that forced the fix was an exec-bit refusal masking a manifest error; the
// refusal is gone and the masking is what was actually wrong, so the test moves to the
// staging rule that remains rather than retiring with the one that prompted it.
func TestPackLintReportsStagingRefusalAndManifestTogether(t *testing.T) {
	dir := t.TempDir()
	var out, errw bytes.Buffer
	packMain([]string{"init", dir}, &out, &errw, false)
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	manifest := `{"name":"t","bogus_field":true,` +
		`"contributes":[{"kind":"skills","from":"skills","into":".claude/skills"}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errw.Reset()
	if rc := packMain([]string{"lint", dir}, &out, &errw, false); rc == 0 {
		t.Fatal("expected lint to fail")
	}
	report := out.String() + errw.String()
	for _, want := range []string{"escape.md", "bogus_field"} {
		if !strings.Contains(report, want) {
			t.Errorf("lint must report %q alongside the other problem, not mask it:\n%s",
				want, report)
		}
	}
}

// A PACK MAY SHIP ITS TOOLS, and both single-pack inspection commands must say so.
//
// This replaces TestPackLintAllowExecFlag and TestPackFootprintAllowExecMatchesLint, which
// pinned the --allow-exec flag and the requirement that lint and footprint AGREE about it.
// The agreement is still the property worth testing — footprint's help advertises it as
// the way to inspect a pack you are authoring, so a pack you can lint you must be able to
// footprint — but the answer both give is now "yes" with no flag to pass.
func TestPackLintAndFootprintAcceptAnExecutable(t *testing.T) {
	dir := t.TempDir()
	var out, errw bytes.Buffer
	packMain([]string{"init", dir}, &out, &errw, false)
	tool := filepath.Join(dir, "skills", "s", "references", "check.sh")
	if err := os.MkdirAll(filepath.Dir(tool), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tool, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skills", "s", "SKILL.md"),
		[]byte("---\nname: s\ndescription: d\n---\nrun references/check.sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, verb := range []string{"lint", "footprint"} {
		out.Reset()
		errw.Reset()
		if rc := packMain([]string{verb, dir}, &out, &errw, false); rc != 0 {
			t.Errorf("%s refused a pack shipping a skill's own tool: rc %d\n%s%s",
				verb, rc, out.String(), errw.String())
		}
	}
}

// The "already declared by the X pack" hint is DERIVED from the embedded packs, and both
// halves matter. It was a hardcoded list of paths until 2026-08-31, by which point 5 of
// its 11 entries were wrong — and a stale entry does not merely fail to advise, it advises
// WRONGLY: it told an author to delete `.pi/agent/skills`, which in a config with no `pi`
// pack was the only thing delivering their skills.
func TestPackLintNamesTheOwningPack(t *testing.T) {
	dir := t.TempDir()
	var out, errw bytes.Buffer
	packMain([]string{"init", dir}, &out, &errw, false)
	// One destination an agent pack owns, one nobody owns (agy moved off this path).
	manifest := `{"name":"t","contributes":[` +
		`{"kind":"skills","from":"skills","into":".claude/skills"},` +
		`{"kind":"skills","from":"skills","into":".gemini/antigravity-cli/skills"}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errw.Reset()
	if rc := packMain([]string{"lint", dir}, &out, &errw, false); rc != 0 {
		t.Fatalf("lint failed: rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	report := out.String()
	if !strings.Contains(report, "already declared by the claude pack") {
		t.Errorf("lint did not name the owning pack for .claude/skills:\n%s", report)
	}
	// The other one must stay SILENT — no pack declares it, so it is the author's own
	// destination, and this is the case the literal list got backwards. Checked against the
	// HINT lines only: the footprint listing below them prints every claim by design, so
	// matching the whole report would assert the opposite of what it means to.
	for _, line := range strings.Split(report, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "ℹ") &&
			strings.Contains(line, "antigravity-cli") {
			t.Errorf("lint flagged a destination no pack declares — that is the advice "+
				"that deletes working contributions:\n%s", line)
		}
	}
}

// A skill dir with no SKILL.md is invisible to every agent and produces no error
// anywhere else — the single most likely authoring mistake.
func TestPackLintCatchesSkillDirWithoutManifest(t *testing.T) {
	dir := t.TempDir()
	var out, errw bytes.Buffer
	packMain([]string{"init", dir}, &out, &errw, false)
	broken := filepath.Join(dir, "skills", "broken")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "notes.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if rc := packMain([]string{"lint", dir}, &out, &errw, false); rc == 0 {
		t.Fatal("expected lint to flag a skill dir with no SKILL.md")
	}
	if !strings.Contains(out.String(), "skills/broken") {
		t.Errorf("lint did not name the offending dir:\n%s", out.String())
	}
}

// A pack that stages content NO contribution claims and that sits in no
// conventionally-read location really would be read by nothing — the one case the old
// "neither a skills/ dir nor an AGENTS.md" rule was actually about, kept after that rule's
// premise expired.
//
// The pack here DOES do real work (it renders a config surface), so the zero-contributions
// check below is silent and this is the only line: the two checks partition the mistakes
// rather than both firing.
func TestPackLintFlagsStagedContentNothingReads(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pack.json"), `{"name":"cs","contributes":[{"kind":"config",`+
		`"config":[{"agent":"acme","name":"settings","codec":"json","path":"~/.acme/s.json",`+
		`"managed":{"a":1}}]}]}`)
	writeFile(t, filepath.Join(dir, "stray.txt"), "x")

	var out, errw bytes.Buffer
	if rc := packMain([]string{"lint", dir}, &out, &errw, false); rc == 0 {
		t.Fatalf("expected lint to flag staged content nothing reads:\n%s", out.String())
	}
	got := out.String()
	if !strings.Contains(got, "nothing reads") || !strings.Contains(got, "stray.txt") {
		t.Errorf("lint must say what is unread and NAME it:\n%s", got)
	}
	// It must not ALSO claim the pack does nothing — it renders a surface.
	if strings.Contains(got, "ZERO contributions") {
		t.Errorf("a config-rendering pack was told it declares zero contributions:\n%s", got)
	}
}

// Repo-hygiene files at the pack root are not content, so a working pack with a README does
// not get told its README is unread. Without this, the replacement rule would be noise for
// exactly the packs the rule it replaced wrongly rejected.
func TestPackLintIgnoresNonContentRootFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pack.json"), `{"name":"cr","contributes":[{"kind":"config",`+
		`"config":[{"agent":"acme","name":"settings","codec":"json","path":"~/.acme/s.json",`+
		`"managed":{"a":1}}]}]}`)
	writeFile(t, filepath.Join(dir, "README.md"), "# cr\n")
	writeFile(t, filepath.Join(dir, "LICENSE"), "MIT\n")

	var out, errw bytes.Buffer
	if rc := packMain([]string{"lint", dir}, &out, &errw, false); rc != 0 {
		t.Fatalf("lint rejected a config pack carrying a README/LICENSE (rc=%d):\n%s",
			rc, out.String())
	}
}

// THE DEFECT THIS FIX IS ABOUT (docs/plans/roadmap.md §7). A pack that does
// absolutely nothing and a working config-only pack used to get the IDENTICAL message
// ("pack has neither a skills/ dir nor an AGENTS.md"), which is what made the old rule
// useless in the one case it existed for. They must now be told different things: one is a
// failure, the other is fine.
func TestPackLintDistinguishesDoingNothingFromDoingConfigOnly(t *testing.T) {
	// A pack that contributes nothing and ships nothing any reader picks up.
	zero := t.TempDir()
	writeFile(t, filepath.Join(zero, "pack.json"), `{"name":"zero","contributes":[]}`)

	var out, errw bytes.Buffer
	if rc := packMain([]string{"lint", zero}, &out, &errw, false); rc == 0 {
		t.Fatalf("a pack that does nothing must be flagged:\n%s", out.String())
	}
	got := out.String()
	// In THOSE WORDS: the maintainer's requirement is that the message name the actual
	// problem, not a missing skills/ dir.
	if !strings.Contains(got, "ZERO contributions") {
		t.Errorf("the do-nothing message must say the pack declares zero contributions:\n%s", got)
	}
	if strings.Contains(got, "neither a") {
		t.Errorf("the retired skills/-or-AGENTS.md wording is back:\n%s", got)
	}

	// A config-only pack renders a real surface. It does work, so it must lint CLEAN — this
	// is the shape the old rule rejected, and F5's pure-`files` case is the same argument.
	cfg := t.TempDir()
	writeFile(t, filepath.Join(cfg, "pack.json"), `{"name":"cfg","contributes":[{"kind":"config",`+
		`"config":[{"agent":"acme","name":"settings","codec":"json","path":"~/.acme/settings.json",`+
		`"managed":{"theme":"dark"}}]}]}`)
	out.Reset()
	errw.Reset()
	if rc := packMain([]string{"lint", cfg}, &out, &errw, false); rc != 0 {
		t.Fatalf("a config-only pack does real work and must lint clean (rc=%d):\n%s",
			rc, out.String())
	}
}

// F5, from a real migration: a pack whose whole purpose is a `files` tree plus a
// `config-overlay` has no AGENTS.md and no skills/, and the old rule refused it with
// "it would stage files nothing reads" — which is simply false, since the agent reads
// exactly those files.
func TestPackLintAcceptsPureFilesPack(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pack.json"), `{"name":"local","contributes":[`+
		`{"kind":"files","from":"tree","into":".acme/data"},`+
		`{"kind":"config-overlay","surface":"claude/settings","config":{"managed":{"x":1}}}]}`)
	writeFile(t, filepath.Join(dir, "tree", "models.json"), "{}\n")

	var out, errw bytes.Buffer
	if rc := packMain([]string{"lint", dir}, &out, &errw, false); rc != 0 {
		t.Fatalf("lint rejected a files+config-overlay pack (F5) with rc=%d:\n%s",
			rc, out.String())
	}
}

// EVERY pack yolo ships must lint clean. The old rule rejected all six — they are
// pack.json + derive.lua with no skills/ and no AGENTS.md — which is the strongest possible
// evidence that a lint rule is asking the wrong question: the reference implementations of
// the thing it validates cannot pass it.
func TestShippedPacksLintClean(t *testing.T) {
	packs := packload.Embedded()
	if len(packs) == 0 {
		t.Fatal("no embedded packs to lint")
	}
	for _, p := range packs {
		var out, errw bytes.Buffer
		if rc := packMain([]string{"lint", p.Root}, &out, &errw, false); rc != 0 {
			t.Errorf("shipped pack %s does not lint clean (rc=%d):\n%s%s",
				p.Name, rc, out.String(), errw.String())
		}
	}
}

// A pack delivering skills and prose by CONVENTION, with no manifest at all, does work and
// must lint clean — the zero-ceremony shape the jail reads through
// packload.SkillsSourceDirs' undeclared fallback and packload.BriefingProse. Guards the
// obvious wrong implementation of the zero-contributions check: keying it on the manifest
// alone would fail-lint the pack `pack init` scaffolds.
func TestPackLintAcceptsZeroCeremonyPack(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "prose\n")
	writeFile(t, filepath.Join(dir, "skills", "example", "SKILL.md"),
		"---\nname: example\ndescription: d\n---\nbody\n")

	var out, errw bytes.Buffer
	if rc := packMain([]string{"lint", dir}, &out, &errw, false); rc != 0 {
		t.Fatalf("lint rejected a zero-ceremony pack (rc=%d):\n%s", rc, out.String())
	}
}

// lint's claimed-paths set must track packdecl.DefaultBriefingFiles, not a private copy of
// it. CLAUDE.md left that list on 2026-08-17 (pack-code-separation.md §3.3), and lint kept
// its own hardcoded `{"AGENTS.md", "CLAUDE.md"}` — so a pack whose only content is a root
// CLAUDE.md was counted as CLAIMED ("some reader picks this up") by the one check whose job
// is to say when nothing does. It linted clean and briefed nothing, which is precisely the
// accepted-and-ignored shape both checks were rewritten to stop producing.
//
// Pinned as behavior rather than by asserting the literal, because the defect is the
// DUPLICATION: a test on the list's contents would have stayed green while the two copies
// drifted, which is how this survived the rename in the first place.
func TestPackLintTracksTheBriefingConvention(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "CLAUDE.md"), "prose\n")

	var out, errw bytes.Buffer
	if rc := packMain([]string{"lint", dir}, &out, &errw, false); rc == 0 {
		t.Fatalf("lint accepted a pack whose only content is CLAUDE.md; nothing reads that "+
			"name any more, so the pack does nothing:\n%s", out.String())
	}
	if got := out.String(); !strings.Contains(got, "CLAUDE.md") {
		t.Errorf("lint did not name the unread file:\n%s", got)
	}

	// The other half, so the fix cannot be "call everything unclaimed": AGENTS.md IS still
	// the convention, and a pack carrying only that one still lints clean.
	agents := t.TempDir()
	writeFile(t, filepath.Join(agents, "AGENTS.md"), "prose\n")
	out.Reset()
	errw.Reset()
	if rc := packMain([]string{"lint", agents}, &out, &errw, false); rc != 0 {
		t.Fatalf("lint rejected a pack carrying the conventional AGENTS.md (rc=%d):\n%s",
			rc, out.String())
	}
}

// A typo'd `from` gets the SPECIFIC diagnosis and only that one. Its real skills tree is
// unclaimed, so the unread-content rule would otherwise fire too — advising the author to
// "move them under skills/", where they already are. A fixed rule that adds a contradictory
// second line is new noise, not a fix.
func TestPackLintTypoedFromDrawsOneDiagnosis(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pack.json"),
		`{"name":"t","contributes":[{"kind":"skills","from":"my-skils","into":".claude/skills"}]}`)
	writeFile(t, filepath.Join(dir, "skills", "example", "SKILL.md"),
		"---\nname: example\ndescription: d\n---\nbody\n")

	var out, errw bytes.Buffer
	if rc := packMain([]string{"lint", dir}, &out, &errw, false); rc == 0 {
		t.Fatalf("a typo'd `from` must still fail lint:\n%s", out.String())
	}
	got := out.String()
	if !strings.Contains(got, "my-skils") {
		t.Errorf("lint did not name the missing declared source:\n%s", got)
	}
	if strings.Contains(got, "nothing reads") {
		t.Errorf("lint added a contradictory second complaint about unread content:\n%s", got)
	}
}

// lint must validate the MANIFEST, not just the file tree: an unknown kind, a
// missing required field, or an unknown top-level key has to be caught here rather
// than at jail boot (where only the first surfaces, one per launch).
func TestPackLintValidatesManifest(t *testing.T) {
	dir := t.TempDir()
	var out, errw bytes.Buffer
	packMain([]string{"init", dir}, &out, &errw, false) // valid skeleton (skills + AGENTS.md)

	// A manifest with an unknown kind AND a missing required field.
	manifest := `{"contributes":[{"kind":"nonsense"},{"kind":"program","bin":"x"}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errw.Reset()
	if rc := packMain([]string{"lint", dir}, &out, &errw, false); rc == 0 {
		t.Fatalf("expected lint to fail on a malformed manifest:\n%s%s", out.String(), errw.String())
	}
	got := out.String() + errw.String()
	if !strings.Contains(got, "nonsense") {
		t.Errorf("lint did not report the unknown kind:\n%s", got)
	}
	// It must report EVERY problem, not stop at the first — the whole reason to lint.
	if !strings.Contains(got, "via") {
		t.Errorf("lint did not also report the missing program field (should report all):\n%s", got)
	}
}

// A lint-clean pack with a valid manifest shows its footprint, so an author who
// never launches a jail still sees what the pack claims.
func TestPackLintPrintsFootprint(t *testing.T) {
	dir := t.TempDir()
	var out, errw bytes.Buffer
	packMain([]string{"init", dir}, &out, &errw, false)
	manifest := `{"contributes":[{"kind":"env","vars":{"ACME_MODE":"fast"}}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errw.Reset()
	if rc := packMain([]string{"lint", dir}, &out, &errw, false); rc != 0 {
		t.Fatalf("valid manifest should lint clean: rc %d\n%s%s", rc, out.String(), errw.String())
	}
	if !strings.Contains(out.String(), "ACME_MODE") {
		t.Errorf("lint did not print the env claim in the footprint:\n%s", out.String())
	}
}

// footprint must accept a local pack directory, not only the embedded packs, so an
// author can inspect the pack they are writing before configuring it.
func TestPackFootprintAcceptsLocalPath(t *testing.T) {
	dir := t.TempDir()
	var out, errw bytes.Buffer
	packMain([]string{"init", dir}, &out, &errw, false)
	manifest := `{"contributes":[{"kind":"mount","host":"datasets/acme","into":"acme-data"}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errw.Reset()
	if rc := packMain([]string{"footprint", dir}, &out, &errw, false); rc != 0 {
		t.Fatalf("footprint on a local path failed: rc %d\n%s%s", rc, out.String(), errw.String())
	}
	// The mount claim (host read → /ctx) must appear and be flagged for review.
	if !strings.Contains(out.String(), "mount") || !strings.Contains(out.String(), "review") {
		t.Errorf("footprint did not show the review-worthy mount claim:\n%s", out.String())
	}
}

func TestPackUnknownVerbIsAnError(t *testing.T) {
	var out, errw bytes.Buffer
	if rc := packMain([]string{"frobnicate"}, &out, &errw, false); rc == 0 {
		t.Error("unknown verb should fail")
	}
	if !strings.Contains(errw.String(), "unknown verb") {
		t.Errorf("stderr = %s", errw.String())
	}
}

// `explain` is the answer to "why isn't my skill showing up?", so it must report
// the FILTERED files, not just the staged ones.
func TestPackExplainReportsFilteredFiles(t *testing.T) {
	home := t.TempDir()
	pack := t.TempDir()
	t.Setenv("HOME", home)

	var out, errw bytes.Buffer
	packMain([]string{"init", pack}, &out, &errw, false)

	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc"),
		[]byte(`{"packs": [{"source": "file://`+pack+`", "name": "p", "only": ["skills/*"]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	errw.Reset()
	if rc := packMain([]string{"explain", "p"}, &out, &errw, false); rc != 0 {
		t.Fatalf("explain rc = %d: %s", rc, errw.String())
	}
	got := out.String()
	if !strings.Contains(got, "skills/example/SKILL.md") {
		t.Errorf("explain did not list the staged skill:\n%s", got)
	}
	// The whole point: the excluded files are named.
	if !strings.Contains(got, "AGENTS.md") || !strings.Contains(got, "filtered out") {
		t.Errorf("explain must report what the filters dropped:\n%s", got)
	}
}

func TestPackExplainUnknownNameIsAnError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if rc := packMain([]string{"explain", "nope"}, &out, &errw, false); rc == 0 {
		t.Error("explain of an unconfigured pack should fail")
	}
	if !strings.Contains(errw.String(), "pack ls") {
		t.Errorf("error should point at `yolo pack ls`: %s", errw.String())
	}
}

// `pack ls` with nothing configured must explain where packs go rather than
// printing an empty table.
func TestPackLsEmptyExplainsWhereToConfigure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if rc := packMain([]string{"ls"}, &out, &errw, false); rc != 0 {
		t.Fatal(errw.String())
	}
	if !strings.Contains(out.String(), "user scope only") {
		t.Errorf("empty ls should say where packs are configured:\n%s", out.String())
	}
}

// gitPackRepo builds a real git repo containing a pack in a subdirectory, so the
// install path exercises actual git rather than a mock.
func gitPackRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "tools", "agent-pack", "skills", "gitskill")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "SKILL.md"),
		[]byte("---\nname: gitskill\ndescription: from git\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		// CleanGitEnv first: hook-exported git state is ABSOLUTE from a linked
		// worktree and would redirect this helper onto the committer's index
		// (this closure's `add -A` is the one that staged 1441 bogus entries).
		cmd.Env = append(packsrc.CleanGitEnv(os.Environ()), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("add", "-A")
	run("commit", "-qm", "pack")
	return dir
}

// C5 end to end: install FETCHES and records a commit; status reports it; a second
// install is a no-op that says "unchanged".
func TestPackInstallFetchesAndLocks(t *testing.T) {
	repo := gitPackRepo(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := "git+file://" + repo + "//tools/agent-pack?ref=main"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc"),
		[]byte(`{"packs": [{"source": "`+src+`", "name": "gp"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errw bytes.Buffer
	if rc := packMain([]string{"install"}, &out, &errw, false); rc != 0 {
		t.Fatalf("install rc = %d\n%s%s", rc, out.String(), errw.String())
	}
	if !strings.Contains(out.String(), "gp") {
		t.Errorf("install did not report the pack:\n%s", out.String())
	}

	// The lockfile records the COMMIT, not just the ref: "what you asked for" vs
	// "what you got" is the whole reason it exists.
	lock, err := packsrc.LoadLock(packsrc.LockPath(filepath.Join(cfgDir, "config.jsonc")))
	if err != nil {
		t.Fatal(err)
	}
	e, ok := lock.Get("gp")
	if !ok {
		t.Fatalf("pack not locked: %+v", lock.Packs)
	}
	if len(e.Commit) != 40 || e.Ref != "main" {
		t.Errorf("lock entry = %+v, want a full SHA and ref=main", e)
	}

	// Re-install is idempotent and says so, rather than implying it re-fetched.
	out.Reset()
	if rc := packMain([]string{"install"}, &out, &errw, false); rc != 0 {
		t.Fatalf("second install rc = %d: %s", rc, errw.String())
	}
	if !strings.Contains(out.String(), "unchanged") {
		t.Errorf("second install should report unchanged:\n%s", out.String())
	}

	// status reports the locked commit.
	out.Reset()
	if rc := packMain([]string{"status"}, &out, &errw, false); rc != 0 {
		t.Fatalf("status rc = %d: %s", rc, errw.String())
	}
	if !strings.Contains(out.String(), e.Commit[:8]) {
		t.Errorf("status did not show the locked commit:\n%s", out.String())
	}
}

// DRIFT: editing the config address without re-installing must be REPORTED. Launch
// resolves from the store, so a silently-stale lock is the most confusing possible
// behavior — the user's edit appears to do nothing.
func TestPackStatusFlagsConfigDrift(t *testing.T) {
	repo := gitPackRepo(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(cfgDir, "config.jsonc")
	write := func(ref string) {
		src := "git+file://" + repo + "//tools/agent-pack?ref=" + ref
		if err := os.WriteFile(cfg,
			[]byte(`{"packs": [{"source": "`+src+`", "name": "gp"}]}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var out, errw bytes.Buffer
	write("main")
	if rc := packMain([]string{"install"}, &out, &errw, false); rc != 0 {
		t.Fatalf("install rc = %d: %s", rc, errw.String())
	}

	// Edit the ref without re-installing.
	write("some-other-ref")
	out.Reset()
	rc := packMain([]string{"status"}, &out, &errw, false)
	if rc == 0 {
		t.Error("status should fail when config and lock disagree")
	}
	if !strings.Contains(out.String(), "config changed since install") {
		t.Errorf("status did not flag drift:\n%s", out.String())
	}
}

// The conventional local pack has no lock entry — nothing FETCHED it — so `pack status` must
// not send the user to `yolo pack install`, a command that cannot help. Same rule as the
// builtin case, for the opposite reason. The second half is what keeps the fix from being
// over-broad: a genuinely uninstalled FETCHED pack must still be told to install.
func TestPackStatusDoesNotTellYouToInstallTheLocalPack(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(filepath.Join(cfgDir, "local", "skills", "mine"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "local", "skills", "mine", "SKILL.md"),
		[]byte("---\nname: mine\ndescription: d\n---\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc"),
		[]byte(`{"packs": ["claude", {"name": "remote", "source": "git+https://example.invalid/p.git"}]}`),
		0o644); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	packMain([]string{"status"}, &out, &errw, false)
	report := out.String()

	local := statusLineFor(t, report, "local")
	if strings.Contains(local, "not installed") {
		t.Errorf("the local pack is found by CONVENTION and cannot be installed; status says:\n  %s", local)
	}
	// The negative control: an actually-uninstalled fetched pack still needs the remedy.
	if remote := statusLineFor(t, report, "remote"); !strings.Contains(remote, "not installed") {
		t.Errorf("a fetched pack with no lock entry must still say so; got:\n  %s", remote)
	}
}

// statusLineFor returns the single `pack status` line for name, failing if there is not exactly
// one. Scoped rather than a report-wide Contains: this report names several packs, so a
// whole-output substring check passes on another pack's line (a trap that made a sibling test in
// this suite pass under mutation).
func statusLineFor(t *testing.T, report, name string) string {
	t.Helper()
	var found []string
	for _, line := range strings.Split(report, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), name) {
			found = append(found, strings.TrimSpace(line))
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one status line for %q, got %d:\n%s", name, len(found), report)
	}
	return found[0]
}

// A pack removed from config must be pruned from the lockfile, and the removal
// REPORTED: it means content is about to stop being delivered.
func TestPackInstallPrunesRemovedPacks(t *testing.T) {
	home := t.TempDir()
	pack := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(cfgDir, "config.jsonc")
	if err := os.WriteFile(cfg,
		[]byte(`{"packs": [{"source": "file://`+pack+`", "name": "gone"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	packMain([]string{"install"}, &out, &errw, false)

	if err := os.WriteFile(cfg, []byte(`{"packs": []}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	packMain([]string{"install"}, &out, &errw, false)

	lock, err := packsrc.LoadLock(packsrc.LockPath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if _, still := lock.Get("gone"); still {
		t.Error("a pack removed from config should be pruned from the lockfile")
	}
}

// `yolo pack install` ASKS NOTHING, and records no approval — end to end, through the
// production path, with a real git pack that declares a host mount.
//
// FIVE TESTS USED TO LIVE HERE: TestResolveHostApproval (yes/no/no-stdin/carry-forward),
// TestResolveHostApprovalRefusesNonTerminalStdin, TestPackInstallRefusesPipedApproval and
// TestPackInstallRefusesAnOSPipeWithBytesWaiting (the `yes | yolo pack install` shape, in
// both branches of the deleted approvalStdinFrom). Every one pinned the gate OQ-TP9 deleted
// as theatre (docs/design/trust-paths.md, 2026-09-04): to reach this command with this pack
// configured you edited `packs` in ~/.config/yolo-jail/config.jsonc as the host user, which
// is strictly more authority than the prompt withheld.
//
// THE TWO PIPE TESTS ARE THE ONES WORTH REMEMBERING, because their criticism was correct and
// too small — `yes | yolo pack install` answered the prompt, so the gate could not tell a
// human from a pipe. That is a true objection that made the gate look EXAMINED, which is
// exactly what stopped anyone from running the authority test on it for months
// (gate-placement-principle.md says so about itself). This replacement asserts the outcome
// they were reaching for by a different route: there is no question for a pipe to answer.
//
// Driven through packMain rather than a helper, because the assertion is about the COMMAND: a
// prompt reintroduced anywhere inside install would either block this test (no stdin is
// wired any more) or leave its telltale in the output.
func TestPackInstallNeverPromptsForHostAccess(t *testing.T) {
	repo := gitHostAccessPackRepo(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(cfgDir, "config.jsonc")
	src := "git+file://" + repo + "//hostpack?ref=main"
	if err := os.WriteFile(cfg,
		[]byte(`{"packs": [{"source": "`+src+`", "name": "hp"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errw bytes.Buffer
	rc := packMain([]string{"install"}, &out, &errw, false)
	if rc != 0 {
		t.Errorf("installing a fetched pack that declares a host mount exited %d:\n%s%s\n"+
			"OQ-TP9 deleted the approval that made this non-zero; a failure here is that gate, "+
			"back", rc, out.String(), errw.String())
	}
	report := out.String() + errw.String()
	for _, telltale := range []string{"[y/N]", "interactive terminal", "Approve host access"} {
		if strings.Contains(report, telltale) {
			t.Errorf("`pack install` prompted for host access (%q):\n%s\n\nThere is nothing "+
				"to ask: the person who put this git URL in their own user config already "+
				"granted more than the prompt withheld", telltale, report)
		}
	}

	// AND THE LOCKFILE RECORDS THE PIN AND NOTHING ELSE. Its approval field is deleted, so
	// what is checked is the raw JSON — a re-added field would be invisible to a typed read.
	lock, err := packsrc.LoadLock(packsrc.LockPath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	e, ok := lock.Get("hp")
	if !ok {
		t.Fatalf("the pack was not locked: %+v", lock.Packs)
	}
	if e.Commit == "" {
		t.Error("the lockfile recorded no commit — the pin is the whole of what it is for now")
	}
	raw, err := os.ReadFile(packsrc.LockPath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "approved") {
		t.Errorf("the lockfile carries an approval field:\n%s", raw)
	}
}

// gitHostAccessPackRepo builds a real git repo containing a pack that CLAIMS host
// access (a mount), so install's approval gate actually fires.
func gitHostAccessPackRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "hostpack")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "pack.json"),
		[]byte(`{"contributes":[{"kind":"mount","host":"refs","into":"refs"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		// CleanGitEnv first: hook-exported git state is ABSOLUTE from a linked
		// worktree and would redirect this helper onto the committer's index
		// (this closure's `add -A` is the one that staged 1441 bogus entries).
		cmd.Env = append(packsrc.CleanGitEnv(os.Environ()), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("add", "-A")
	run("commit", "-qm", "pack")
	return dir
}

// lint must read the skills source the manifest DECLARES, not a hardcoded skills/. The
// bug being guarded: `from` was validated for shape and then ignored everywhere, so a pack
// delivering from `my-skills/` linted clean while the linter checked a tree nothing read —
// and a missing SKILL.md in the tree that WAS read passed.
func TestPackLintReadsDeclaredSkillsFrom(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pack.json"),
		`{"name":"sf","contributes":[{"kind":"skills","from":"my-skills","into":".claude/skills"}]}`)
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "prose\n")
	// A skill dir under the DECLARED source with no SKILL.md: invisible to every agent.
	writeFile(t, filepath.Join(dir, "my-skills", "broken", "notes.md"), "x")

	var out, errw bytes.Buffer
	if rc := packMain([]string{"lint", dir}, &out, &errw, false); rc == 0 {
		t.Fatalf("lint passed a skill dir with no SKILL.md under the declared source:\n%s",
			out.String())
	}
	if !strings.Contains(out.String(), "my-skills/broken") {
		t.Errorf("lint named the wrong dir (it should follow `from`):\n%s", out.String())
	}
}

// A NON-CONVENTIONAL `from` naming nothing is a lint failure: the author asked for a
// specific path and would get no skills from it at either notch.
func TestPackLintFlagsMissingDeclaredSkillsFrom(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pack.json"),
		`{"name":"sf","contributes":[{"kind":"skills","from":"my-skills","into":".claude/skills"}]}`)
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "prose\n")
	// The CONVENTIONAL dir exists; the declared one does not. The old code read this one.
	writeFile(t, filepath.Join(dir, "skills", "example", "SKILL.md"),
		"---\nname: example\ndescription: d\n---\nbody\n")

	var out, errw bytes.Buffer
	if rc := packMain([]string{"lint", dir}, &out, &errw, false); rc == 0 {
		t.Fatalf("lint passed a pack whose declared skills source is absent:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "my-skills") {
		t.Errorf("lint did not name the missing declared source:\n%s", out.String())
	}
}

// The CONVENTIONAL source being absent is NOT a lint failure on its own, because a
// contribution that only NAMES a destination other packs merge into is legitimate — that is
// what all six shipped packs do. Guards against the noise regression: keying the check on
// `from != ""` would fail-lint every shipped pack.
func TestPackLintAcceptsConventionalFromWithNoSkills(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pack.json"),
		`{"name":"dest","contributes":[{"kind":"skills","from":"skills","into":".claude/skills"}]}`)
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "prose\n")

	var out, errw bytes.Buffer
	if rc := packMain([]string{"lint", dir}, &out, &errw, false); rc != 0 {
		t.Fatalf("lint rejected a destination-naming pack (rc=%d):\n%s", rc, out.String())
	}
}

// No SHIPPED pack draws the new `from`-is-missing complaint. Every one declares
// `from: "skills"` and carries no skills of its own — their contribution names the destination
// other packs merge into — so a rule keyed on `from != ""` rather than on the CONVENTION
// would fail-lint all six.
//
// Scoped to THIS rule rather than asserting rc==0, deliberately kept narrow now that the
// broader assertion exists (TestShippedPacksLintClean): a failure here says the `from`
// convention exemption broke specifically, which is a different diagnosis from "a shipped
// pack fails some lint rule".
func TestShippedPacksDrawNoMissingSkillsFromComplaint(t *testing.T) {
	for _, p := range packload.Embedded() {
		var out, errw bytes.Buffer
		packMain([]string{"lint", p.Root}, &out, &errw, false)
		if strings.Contains(out.String(), "nothing stages under") {
			t.Errorf("shipped pack %s draws the missing-skills-source complaint:\n%s",
				p.Name, out.String())
		}
	}
}

// The footprint names the resolved SOURCE, so an author can see at lint time which dir their
// skills come from. A claim line reading only "merged" is identical for a working pack and one
// whose skills nothing reads.
func TestPackLintFootprintNamesSkillsSource(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pack.json"),
		`{"name":"sf","contributes":[{"kind":"skills","from":"my-skills","into":".claude/skills"}]}`)
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "prose\n")
	writeFile(t, filepath.Join(dir, "my-skills", "example", "SKILL.md"),
		"---\nname: example\ndescription: d\n---\nbody\n")

	var out, errw bytes.Buffer
	if rc := packMain([]string{"lint", dir}, &out, &errw, false); rc != 0 {
		t.Fatalf("lint rc=%d:\n%s", rc, out.String())
	}
	if !strings.Contains(out.String(), "from my-skills/") {
		t.Errorf("footprint does not name the resolved skills source:\n%s", out.String())
	}
}
