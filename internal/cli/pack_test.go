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
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// `pack init` must scaffold a pack that `pack lint` accepts. If the scaffold did not
// lint clean, every author's first action would produce an error — and the two would
// be free to drift apart.
func TestPackInitScaffoldLintsClean(t *testing.T) {
	dir := t.TempDir()
	var out, errw bytes.Buffer
	if rc := packMain([]string{"init", dir}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("init rc = %d: %s", rc, errw.String())
	}
	for _, want := range []string{"AGENTS.md", "SKILL.md", "README.md"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("init did not create %s:\n%s", want, out.String())
		}
	}
	out.Reset()
	errw.Reset()
	if rc := packMain([]string{"lint", dir}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("scaffolded pack does not lint clean: rc %d\n%s%s", rc, out.String(), errw.String())
	}
}

// init must be safe to re-run: it reports skips rather than clobbering an author's
// edited files.
func TestPackInitDoesNotClobber(t *testing.T) {
	dir := t.TempDir()
	var out, errw bytes.Buffer
	packMain([]string{"init", dir}, &out, &errw, false, nil)
	edited := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(edited, []byte("MY OWN PROSE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if rc := packMain([]string{"init", dir}, &out, &errw, false, nil); rc != 0 {
		t.Fatal(errw.String())
	}
	if !strings.Contains(out.String(), "skip AGENTS.md") {
		t.Errorf("re-run should report a skip:\n%s", out.String())
	}
	if data, _ := os.ReadFile(edited); !strings.Contains(string(data), "MY OWN PROSE") {
		t.Error("init clobbered an edited file")
	}
}

// lint runs the REAL staging rules, so an author hits the exec-bit refusal before a
// consumer's jail does. A linter that disagreed with the stager would be worse than
// none.
//
// The refusal is reported on STDOUT with the other lint problems, not stderr: a staging
// failure is now collected as a problem rather than returned on, so that it prints
// alongside the manifest validation (see TestPackLintReportsExecBitAndManifestTogether).
// One problem list in one stream beats a refusal on stderr and the explanation on stdout.
func TestPackLintReportsStagingRefusals(t *testing.T) {
	dir := t.TempDir()
	var out, errw bytes.Buffer
	packMain([]string{"init", dir}, &out, &errw, false, nil)
	if err := os.WriteFile(filepath.Join(dir, "hook.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errw.Reset()
	if rc := packMain([]string{"lint", dir}, &out, &errw, false, nil); rc == 0 {
		t.Fatal("expected lint to fail on an executable file")
	}
	report := out.String() + errw.String()
	if !strings.Contains(report, "allow_exec") {
		t.Errorf("lint error should name the opt-in: %s", report)
	}
	// It must name the CONSUMER's config, not pack.json — pointing an author at the
	// manifest sends them to the one file that cannot grant the exec bit.
	if !strings.Contains(report, "config.jsonc") {
		t.Errorf("lint error must name ~/.config/yolo-jail/config.jsonc as where "+
			"allow_exec goes, not pack.json: %s", report)
	}
}

// The exec-bit refusal and the manifest error must print TOGETHER.
//
// This is the regression test for the masking bug: `packLint` used to return as soon as
// staging failed, so an author who followed the old message's advice — put
// `"allow_exec": true` in pack.json — saw ONLY the unchanged refusal. The manifest
// validation that would have said "unknown field allow_exec" never ran. Either line alone
// is misleading; the pair is self-explanatory.
func TestPackLintReportsExecBitAndManifestTogether(t *testing.T) {
	dir := t.TempDir()
	var out, errw bytes.Buffer
	packMain([]string{"init", dir}, &out, &errw, false, nil)
	if err := os.WriteFile(filepath.Join(dir, "hook.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The mistake the old message invited: allow_exec in the MANIFEST, where it is an
	// unknown field.
	manifest := `{"name":"t","allow_exec":true,` +
		`"contributes":[{"kind":"skills","from":"skills","into":".claude/skills"}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errw.Reset()
	if rc := packMain([]string{"lint", dir}, &out, &errw, false, nil); rc == 0 {
		t.Fatal("expected lint to fail")
	}
	report := out.String() + errw.String()
	for _, want := range []string{"is executable", "unknown field \"allow_exec\""} {
		if !strings.Contains(report, want) {
			t.Errorf("lint must report %q alongside the other problem, not mask it:\n%s",
				want, report)
		}
	}
}

// --allow-exec lints the way a consenting consumer would stage, which is the only way an
// author can see past the refusal to the rest of the report: allow_exec lives in the
// user's config, so there is nothing an author can put in their own tree to get there.
func TestPackLintAllowExecFlag(t *testing.T) {
	for _, args := range [][]string{
		{"lint", "--allow-exec", "DIR"},
		{"lint", "DIR", "--allow-exec"}, // flag order must not matter
	} {
		dir := t.TempDir()
		var out, errw bytes.Buffer
		packMain([]string{"init", dir}, &out, &errw, false, nil)
		if err := os.WriteFile(filepath.Join(dir, "hook.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		withDir := make([]string, len(args))
		for i, a := range args {
			if a == "DIR" {
				a = dir
			}
			withDir[i] = a
		}
		out.Reset()
		errw.Reset()
		if rc := packMain(withDir, &out, &errw, false, nil); rc != 0 {
			t.Errorf("lint %v should accept an exec-bit file: rc %d\n%s%s",
				args, rc, out.String(), errw.String())
		}
	}
}

// A skill dir with no SKILL.md is invisible to every agent and produces no error
// anywhere else — the single most likely authoring mistake.
func TestPackLintCatchesSkillDirWithoutManifest(t *testing.T) {
	dir := t.TempDir()
	var out, errw bytes.Buffer
	packMain([]string{"init", dir}, &out, &errw, false, nil)
	broken := filepath.Join(dir, "skills", "broken")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "notes.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if rc := packMain([]string{"lint", dir}, &out, &errw, false, nil); rc == 0 {
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
	if rc := packMain([]string{"lint", dir}, &out, &errw, false, nil); rc == 0 {
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
	if rc := packMain([]string{"lint", dir}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("lint rejected a config pack carrying a README/LICENSE (rc=%d):\n%s",
			rc, out.String())
	}
}

// THE DEFECT THIS FIX IS ABOUT (docs/plans/outstanding-work.md §7). A pack that does
// absolutely nothing and a working config-only pack used to get the IDENTICAL message
// ("pack has neither a skills/ dir nor an AGENTS.md"), which is what made the old rule
// useless in the one case it existed for. They must now be told different things: one is a
// failure, the other is fine.
func TestPackLintDistinguishesDoingNothingFromDoingConfigOnly(t *testing.T) {
	// A pack that contributes nothing and ships nothing any reader picks up.
	zero := t.TempDir()
	writeFile(t, filepath.Join(zero, "pack.json"), `{"name":"zero","contributes":[]}`)

	var out, errw bytes.Buffer
	if rc := packMain([]string{"lint", zero}, &out, &errw, false, nil); rc == 0 {
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
	if rc := packMain([]string{"lint", cfg}, &out, &errw, false, nil); rc != 0 {
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
	if rc := packMain([]string{"lint", dir}, &out, &errw, false, nil); rc != 0 {
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
		if rc := packMain([]string{"lint", p.Root}, &out, &errw, false, nil); rc != 0 {
			t.Errorf("shipped pack %s does not lint clean (rc=%d):\n%s%s",
				p.Name, rc, out.String(), errw.String())
		}
	}
}

// A pack delivering skills and prose by CONVENTION, with no manifest at all, does work and
// must lint clean — the zero-ceremony shape the jail reads through
// packload.SkillsSourceDirs' undeclared fallback and run.readPackBriefing. Guards the
// obvious wrong implementation of the zero-contributions check: keying it on the manifest
// alone would fail-lint the pack `pack init` scaffolds.
func TestPackLintAcceptsZeroCeremonyPack(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "prose\n")
	writeFile(t, filepath.Join(dir, "skills", "example", "SKILL.md"),
		"---\nname: example\ndescription: d\n---\nbody\n")

	var out, errw bytes.Buffer
	if rc := packMain([]string{"lint", dir}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("lint rejected a zero-ceremony pack (rc=%d):\n%s", rc, out.String())
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
	if rc := packMain([]string{"lint", dir}, &out, &errw, false, nil); rc == 0 {
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
	packMain([]string{"init", dir}, &out, &errw, false, nil) // valid skeleton (skills + AGENTS.md)

	// A manifest with an unknown kind AND a missing required field.
	manifest := `{"contributes":[{"kind":"nonsense"},{"kind":"program","bin":"x"}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errw.Reset()
	if rc := packMain([]string{"lint", dir}, &out, &errw, false, nil); rc == 0 {
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
	packMain([]string{"init", dir}, &out, &errw, false, nil)
	manifest := `{"contributes":[{"kind":"env","vars":{"ACME_MODE":"fast"}}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errw.Reset()
	if rc := packMain([]string{"lint", dir}, &out, &errw, false, nil); rc != 0 {
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
	packMain([]string{"init", dir}, &out, &errw, false, nil)
	manifest := `{"contributes":[{"kind":"mount","host":"datasets/acme","into":"acme-data"}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errw.Reset()
	if rc := packMain([]string{"footprint", dir}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("footprint on a local path failed: rc %d\n%s%s", rc, out.String(), errw.String())
	}
	// The mount claim (host read → /ctx) must appear and be flagged for review.
	if !strings.Contains(out.String(), "mount") || !strings.Contains(out.String(), "review") {
		t.Errorf("footprint did not show the review-worthy mount claim:\n%s", out.String())
	}
}

// F6b: the two single-pack inspection commands must agree on the exec bit. `lint
// --allow-exec` accepted a pack shipping an executable while `footprint` on that same pack
// exited 1 on the refusal — so a pack you COULD lint you could NOT inspect, and footprint's
// own help advertises it as the way to inspect a pack you are authoring.
//
// The refusal WITHOUT the flag is asserted in the same test on purpose: the fix is the two
// commands agreeing, not footprint quietly dropping a trust gate lint still enforces.
func TestPackFootprintAllowExecMatchesLint(t *testing.T) {
	for _, args := range [][]string{
		{"footprint", "--allow-exec", "DIR"},
		{"footprint", "DIR", "--allow-exec"}, // flag order must not matter, as in lint
	} {
		dir := t.TempDir()
		var out, errw bytes.Buffer
		packMain([]string{"init", dir}, &out, &errw, false, nil)
		if err := os.WriteFile(filepath.Join(dir, "hook.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}

		// Same pack, same flag: lint accepts it, so footprint must too.
		out.Reset()
		errw.Reset()
		if rc := packMain([]string{"lint", "--allow-exec", dir}, &out, &errw, false, nil); rc != 0 {
			t.Fatalf("precondition: lint --allow-exec should accept this pack, rc=%d\n%s%s",
				rc, out.String(), errw.String())
		}
		withDir := make([]string, len(args))
		for i, a := range args {
			if a == "DIR" {
				a = dir
			}
			withDir[i] = a
		}
		out.Reset()
		errw.Reset()
		if rc := packMain(withDir, &out, &errw, false, nil); rc != 0 {
			t.Errorf("footprint %v refused a pack `lint --allow-exec` accepts: rc %d\n%s%s",
				args, rc, out.String(), errw.String())
		}

		// Without the flag the exec-bit refusal stands: the flag supplies the consumer's
		// half of the decision, it does not remove the gate.
		out.Reset()
		errw.Reset()
		if rc := packMain([]string{"footprint", dir}, &out, &errw, false, nil); rc == 0 {
			t.Errorf("footprint without --allow-exec must still refuse the exec bit:\n%s",
				out.String())
		}
	}
}

func TestPackUnknownVerbIsAnError(t *testing.T) {
	var out, errw bytes.Buffer
	if rc := packMain([]string{"frobnicate"}, &out, &errw, false, nil); rc == 0 {
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
	packMain([]string{"init", pack}, &out, &errw, false, nil)

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
	if rc := packMain([]string{"explain", "p"}, &out, &errw, false, nil); rc != 0 {
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
	if rc := packMain([]string{"explain", "nope"}, &out, &errw, false, nil); rc == 0 {
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
	if rc := packMain([]string{"ls"}, &out, &errw, false, nil); rc != 0 {
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
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
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
	if rc := packMain([]string{"install"}, &out, &errw, false, nil); rc != 0 {
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
	if rc := packMain([]string{"install"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("second install rc = %d: %s", rc, errw.String())
	}
	if !strings.Contains(out.String(), "unchanged") {
		t.Errorf("second install should report unchanged:\n%s", out.String())
	}

	// status reports the locked commit.
	out.Reset()
	if rc := packMain([]string{"status"}, &out, &errw, false, nil); rc != 0 {
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
	if rc := packMain([]string{"install"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("install rc = %d: %s", rc, errw.String())
	}

	// Edit the ref without re-installing.
	write("some-other-ref")
	out.Reset()
	rc := packMain([]string{"status"}, &out, &errw, false, nil)
	if rc == 0 {
		t.Error("status should fail when config and lock disagree")
	}
	if !strings.Contains(out.String(), "config changed since install") {
		t.Errorf("status did not flag drift:\n%s", out.String())
	}
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
	packMain([]string{"install"}, &out, &errw, false, nil)

	if err := os.WriteFile(cfg, []byte(`{"packs": []}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	packMain([]string{"install"}, &out, &errw, false, nil)

	lock, err := packsrc.LoadLock(packsrc.LockPath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if _, still := lock.Get("gone"); still {
		t.Error("a pack removed from config should be pruned from the lockfile")
	}
}

// resolveHostApproval is the install-time consent gate. These cases pin: a pack
// reading nothing needs no prompt; a "yes" records the current claim set; a "no" (or
// no stdin) records nothing new; and an unchanged pin carries the prior approval
// forward without prompting.
func TestResolveHostApproval(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pack.json"),
		[]byte(`{"contributes":[{"kind":"mount","host":"refs","into":"refs"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	claim := "mount refs -> /ctx/refs"
	pr := richtext.Printer{W: &bytes.Buffer{}}

	// Fresh install, user answers yes → the current claim is approved.
	approved, denied := resolveHostApproval("acme", dir, packsrc.LockEntry{}, false, pr,
		strings.NewReader("y\n"), &bytes.Buffer{})
	if denied || len(approved) != 1 || approved[0] != claim {
		t.Errorf("yes should approve the claim: approved=%v denied=%v", approved, denied)
	}

	// Fresh install, user answers no → nothing approved, denied.
	approved, denied = resolveHostApproval("acme", dir, packsrc.LockEntry{}, false, pr,
		strings.NewReader("n\n"), &bytes.Buffer{})
	if !denied || len(approved) != 0 {
		t.Errorf("no should approve nothing and deny: approved=%v denied=%v", approved, denied)
	}

	// No stdin (non-interactive) → fail closed, same as no.
	approved, denied = resolveHostApproval("acme", dir, packsrc.LockEntry{}, false, pr, nil, &bytes.Buffer{})
	if !denied || len(approved) != 0 {
		t.Errorf("nil stdin must fail closed: approved=%v denied=%v", approved, denied)
	}

	// Unchanged pin: the claim is already approved → carried forward, NO prompt (an
	// empty stdin would fail the prompt, so reaching approved proves it never asked).
	prev := packsrc.LockEntry{Name: "acme", ApprovedHostAccess: []string{claim}}
	approved, denied = resolveHostApproval("acme", dir, prev, true, pr, strings.NewReader(""), &bytes.Buffer{})
	if denied || len(approved) != 1 || approved[0] != claim {
		t.Errorf("an already-approved claim must carry forward without prompting: approved=%v denied=%v", approved, denied)
	}
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
	if rc := packMain([]string{"lint", dir}, &out, &errw, false, nil); rc == 0 {
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
	if rc := packMain([]string{"lint", dir}, &out, &errw, false, nil); rc == 0 {
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
	if rc := packMain([]string{"lint", dir}, &out, &errw, false, nil); rc != 0 {
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
		packMain([]string{"lint", p.Root}, &out, &errw, false, nil)
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
	if rc := packMain([]string{"lint", dir}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("lint rc=%d:\n%s", rc, out.String())
	}
	if !strings.Contains(out.String(), "from my-skills/") {
		t.Errorf("footprint does not name the resolved skills source:\n%s", out.String())
	}
}
