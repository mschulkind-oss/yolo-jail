package cli

// hostapplysurvey_test.go pins THE CHANGE PREDICATE end to end, through the same call site
// `yolo host apply --dry-run` uses (docs/reference/host-apply-staleness.md §3.4, §10 step 1).
//
// THE FIRST PROPERTY IS R3, the design's highest-consequence failure: *"a freshly-applied home
// prompts not at all, ever, until something actually changes."* Everything §4.3 builds on top of
// this reads the predicate, so a predicate that always says "changed" turns the launch gate into
// a prompt on every launch — worse than the silent drift it replaces.
//
// EVERY TEST HERE GOES THROUGH applyHostSurveyed, never through a kind's own render, and each
// asserts InSync > 0 as well as the changed set. That second assertion is the CALL-SITE half:
// without it, deleting every `survey.note(...)` call would leave an empty changed set, and the
// R3 test would pass against a feature that had been switched off wholesale (AGENTS.md's
// callee-pinned-call-site-unpinned rule, which this repo has shipped five times).

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// surveyApply runs an observe pass and returns the roll-up plus the report.
func surveyApply(t *testing.T) (*hostApplySurvey, string) {
	t.Helper()
	var out, errw bytes.Buffer
	survey := &hostApplySurvey{}
	if rc := applyHostSurveyed(&out, &errw, false, false, nil, survey); rc != 0 {
		t.Fatalf("observe apply rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	return survey, out.String() + errw.String()
}

// TestHostApplySurveySeesNothingToChangeAfterAnAssert is R3.
//
// It is the whole reason the predicate is a content comparison rather than a look at which
// report fields are populated: before it, every surface not skipped or refused reported
// `would render` unconditionally, so this assertion could not be made at all.
func TestHostApplySurveySeesNothingToChangeAfterAnAssert(t *testing.T) {
	shippedPacksFixture(t)

	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("assert apply rc=%d\n%s", rc, report)
	}

	survey, report := surveyApply(t)
	if survey.InSync == 0 {
		t.Fatalf("the survey saw NO destinations at all — the predicate's call sites are not "+
			"wired, so \"nothing would change\" below would be vacuous\n%s", report)
	}
	if survey.Changes() {
		t.Errorf("a freshly-applied home reports %d destination(s) that would change; want none "+
			"(R3 — every one of these is a prompt on every launch once §4.3 lands): %+v\n%s",
			len(survey.Changed), survey.Changed, report)
	}
	if !strings.Contains(report, survey.Summary()) {
		t.Errorf("the dry run must END in the roll-up %q — that is the honest way to verify the "+
			"predicate before anything depends on it (§10 step 1)\n%s", survey.Summary(), report)
	}
	if !strings.Contains(report, "0 would change") {
		t.Errorf("want `0 would change` in the report\n%s", report)
	}
}

// TestHostApplySurveySeesAHandEditedConfigSurface is the negative control, and it is the case
// OQ-HS9 rules the whole design on: the CONFIG never moved, so an approval-snapshot comparison
// would see nothing. Only measuring the render catches it.
func TestHostApplySurveySeesAHandEditedConfigSurface(t *testing.T) {
	home := shippedPacksFixture(t)

	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("assert apply rc=%d\n%s", rc, report)
	}
	if survey, report := surveyApply(t); survey.Changes() {
		t.Fatalf("fixture bug: the home is not settled after one assert: %+v\n%s",
			survey.Changed, report)
	}

	// claude/settings' managed layer asserts permissions.defaultMode at the host notch (the
	// GUARDED value, "default" — the autonomous posture never reaches a real host).
	settings := filepath.Join(home, ".claude", "settings.json")
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("fixture bug: %v", err)
	}
	edited := strings.Replace(string(data), `"defaultMode": "default"`,
		`"defaultMode": "plan"`, 1)
	if edited == string(data) {
		t.Fatalf("fixture bug: no managed key to edit in %s:\n%s", settings, data)
	}
	if err := os.WriteFile(settings, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	survey, report := surveyApply(t)
	if !survey.Changes() {
		t.Fatalf("a hand-edited managed key must read as a pending change — the config did not "+
			"move, so this is the one thing a config-approval snapshot cannot see (OQ-HS9)\n%s",
			report)
	}
	var found bool
	for _, c := range survey.Changed {
		if c.Path == settings {
			found = true
		}
	}
	if !found {
		t.Errorf("the changed set must name %s; got %+v\n%s", settings, survey.Changed, report)
	}
	if !strings.Contains(report, "would change") {
		t.Errorf("the report must name the changed destination\n%s", report)
	}
}

// TestHostApplySurveyIgnoresPureReformatting is the `Formatting` carve-out from §3.4, at the
// level a user meets it: re-indenting a rendered JSON surface changes its BYTES and changes
// nothing they configured.
//
// A predicate comparing the render against the file's raw bytes would report this as a change
// forever — and since yolo's canonical JSON is 2-space, so would every 4-space or tab-indented
// ~/.claude/settings.json anyone has ever hand-written. That is R3 arriving by the other route.
func TestHostApplySurveyIgnoresPureReformatting(t *testing.T) {
	home := shippedPacksFixture(t)

	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("assert apply rc=%d\n%s", rc, report)
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("fixture bug: %v", err)
	}
	// Double every leading indent unit. Same values, same key order, different bytes.
	reindented := strings.ReplaceAll(string(data), "\n  ", "\n    ")
	if reindented == string(data) {
		t.Fatalf("fixture bug: nothing to re-indent in %s:\n%s", settings, data)
	}
	if err := os.WriteFile(settings, []byte(reindented), 0o644); err != nil {
		t.Fatal(err)
	}

	survey, report := surveyApply(t)
	for _, c := range survey.Changed {
		if c.Path == settings {
			t.Errorf("re-indenting %s must NOT read as a pending change: nothing the user "+
				"configured moved, and a gate that fires on layout prompts forever (§3.4's "+
				"Formatting carve-out)\n%s", settings, report)
		}
	}
	if survey.InSync == 0 {
		t.Errorf("the survey saw no destinations — the assertion above is vacuous\n%s", report)
	}
}

// TestHostApplySurveyCoversBriefingAndFiles is the OTHER TWO written kinds, and it needs the
// `dropme` fixture rather than the shipped packs: no shipped pack contributes briefing prose or
// a `files` tree, so against `shippedPacksFixture` every briefing line reads
// `skipped: no pack contributes …` and there is no files line at all — a survey that hard-coded
// `WouldChange: true` for both would pass every other test in this file.
//
// §3.4's ruling is that ALL FOUR kinds are covered, with no two tiers of "up to date"
// (OQ-HS4), so each of the four needs a test that fails when its own predicate is wrong.
func TestHostApplySurveyCoversBriefingAndFiles(t *testing.T) {
	home, _ := dropFixture(t, dropPackJSON)

	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("assert apply rc=%d\n%s", rc, report)
	}
	briefing := filepath.Join(home, ".claude", "CLAUDE.md")
	delivered := filepath.Join(home, ".claude", "bin", "pick.sh")
	for _, p := range []string{briefing, delivered} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("fixture bug: %s was not written: %v", p, err)
		}
	}

	survey, report := surveyApply(t)
	if survey.Changes() {
		t.Errorf("a settled home must report no pending change for the briefing or files kinds "+
			"either; got %+v\n%s", survey.Changed, report)
	}
	// Both destinations must be IN the survey, or the assertion above is silent about them.
	inSurvey := map[string]bool{}
	for _, c := range survey.Changed {
		inSurvey[c.Path] = true
	}
	if !strings.Contains(report, briefing) || !strings.Contains(report, delivered) {
		t.Fatalf("fixture bug: the report does not mention both destinations\n%s", report)
	}

	// NEGATIVE CONTROL, one per kind: an edited destination must read as a pending change, or
	// "no pending change" above is satisfied by a predicate that is simply always false.
	if err := os.WriteFile(briefing, []byte("hand-edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(delivered, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(delivered, []byte("#!/bin/sh\necho edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	survey, report = surveyApply(t)
	for _, c := range survey.Changed {
		inSurvey[c.Path] = true
	}
	for _, want := range []string{briefing, delivered} {
		if !inSurvey[want] {
			t.Errorf("a hand-edited %s must read as a pending change; got %+v\n%s",
				want, survey.Changed, report)
		}
	}
}

// TestHostApplySurveySeesADeletedSkill covers the skills kind specifically, which had no
// content comparison at all before this: every entry reported `rendered` on every apply, so a
// roll-up built from the actions alone would have said "everything would change" forever.
func TestHostApplySurveySeesADeletedSkill(t *testing.T) {
	home := shippedPacksFixture(t)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "local", "skills", "mine", "SKILL.md"),
		"---\nname: mine\n---\nbody\n")

	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("assert apply rc=%d\n%s", rc, report)
	}
	delivered := filepath.Join(home, ".claude", "skills", "mine")
	if _, err := os.Stat(delivered); err != nil {
		t.Fatalf("fixture bug: the local pack's skill did not reach %s: %v", delivered, err)
	}
	if survey, report := surveyApply(t); survey.Changes() {
		t.Fatalf("fixture bug: the home is not settled after one assert: %+v\n%s",
			survey.Changed, report)
	}

	if err := os.RemoveAll(delivered); err != nil {
		t.Fatal(err)
	}
	survey, report := surveyApply(t)
	var found bool
	for _, c := range survey.Changed {
		if c.Path == delivered {
			found = true
		}
	}
	if !found {
		t.Errorf("a deleted composed skill must read as a pending change; got %+v\n%s",
			survey.Changed, report)
	}
}

// ---------------------------------------------------------------------------
// docs/design/report-tiers.md §4.3 — the verdict block, and the counts under it.
//
// THE COUNTS ARE THE POINT OF THESE TESTS, not the sentences. The measured report said
// "8 in sync, 76 would change" about a home where six files and fourteen skills would change
// (§3.4): seventy of the seventy-six were fourteen skills counted once per agent directory,
// and nine "damaged entry" lines were three MCP servers counted once per agent. Every test
// below is written so that deleting the CALL SITE that feeds the survey — not the counting
// method — turns it red, which is the shape AGENTS.md requires and this repo has shipped
// wrong five times.

// applyAt runs one apply in the given posture and returns rc plus the report, without the
// t.Fatal the other helpers apply: a verdict has to be assertable on a run that FAILED too.
func applyAt(t *testing.T, write bool) (int, string) {
	t.Helper()
	rc, report := applyWith(t, write, nil)
	return rc, report
}

// TestHostApplyVerdictPrintsOnAllFourPaths is §4.3's first rule — *it prints on every path* —
// against the four the command actually has: two postures times two branches.
//
// BOTH HALVES WERE BROKEN, in different ways, and neither is reachable from the other's fix.
// The roll-up sat inside `if !write`, so an --assert — the posture that writes into a real
// home — ended with no summary at all; and the zero-packs branch returns before the tail
// entirely, so an empty `packs` printed one dim line and stopped. A test that checked only the
// dry run with packs would pass against both defects.
func TestHostApplyVerdictPrintsOnAllFourPaths(t *testing.T) {
	for _, tc := range []struct {
		name  string
		zero  bool
		write bool
		want  string
	}{
		{"observe, packs configured", false, false, "An --assert would complete."},
		{"assert, packs configured", false, true, "Applied:"},
		{"observe, zero packs", true, false, "No packs configured"},
		{"assert, zero packs", true, true, "No packs configured"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := shippedPacksFixture(t)
			if tc.zero {
				selectPacks(t, home, "")
			}
			rc, report := applyAt(t, tc.write)
			if rc != 0 {
				t.Fatalf("rc=%d\n%s", rc, report)
			}
			if !strings.Contains(report, tc.want) {
				t.Errorf("the run must END in a verdict stating its RESULT; want a line "+
					"containing %q (§4.3, P7 — the reader never computes the outcome)\n%s",
					tc.want, report)
			}
			// The POSTURE FOOTER, on every path too: §4.2's last line, and the only thing
			// that tells a reader of a dry run that nothing happened.
			footer := "dry run — nothing was written"
			if tc.write {
				footer = "assert — this posture writes into"
			}
			if !strings.Contains(report, footer) {
				t.Errorf("want the posture footer %q\n%s", footer, report)
			}
		})
	}
}

// TestHostApplyVerdictSaysNothingToDoOnASettledHome pins the degenerate row of §4.3's table.
// It is the row an operator meets most often — a home already applied — and the one where the
// old tail was least useful: "8 in sync, 0 would change" is arithmetic, not an answer.
func TestHostApplyVerdictSaysNothingToDoOnASettledHome(t *testing.T) {
	shippedPacksFixture(t)
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("assert apply rc=%d\n%s", rc, report)
	}
	_, report := applyAt(t, false)
	if !strings.Contains(report, "Nothing to do — this home is up to date.") {
		t.Errorf("a settled home's dry run must say so in one sentence\n%s", report)
	}
}

// multiAgentSkillFixture puts ONE skill of the user's own into several agent skill
// directories — the shape §3.3's worst repetition axis measures, at fixture scale.
//
// Five agent packs, so the composed destinations are five real directories; the skill is
// written into three of them, which is enough for "counted per destination" and "counted per
// name" to give different answers.
func multiAgentSkillFixture(t *testing.T) (home string, dirs []string) {
	t.Helper()
	home = shippedPacksFixture(t)
	dirs = []string{
		filepath.Join(home, ".claude", "skills"),
		filepath.Join(home, ".codex", "skills"),
		filepath.Join(home, ".pi", "agent", "skills"),
	}
	for _, d := range dirs {
		writeFile(t, filepath.Join(d, "mine", "SKILL.md"),
			"---\nname: mine\ndescription: mine\n---\nMy own skill.\n")
	}
	return home, dirs
}

// TestHostApplySurveyCountsSkillsByNameNotDestination is §4.3's skills count: *skills, not
// destinations, deduplicated by name*.
//
// THE MEASUREMENT BEHIND IT: fourteen skills in five agent directories produced seventy
// changed destinations, seventy per-entry lines and seventy paths — 210 of the report's 277
// lines stating one fact three times (§3.3). The destination count is still collected, and is
// still right for the launch gate, which asks a yes/no question; it is simply not a count a
// human asked for.
func TestHostApplySurveyCountsSkillsByNameNotDestination(t *testing.T) {
	_, dirs := multiAgentSkillFixture(t)

	survey, report := surveyApply(t)

	// The destinations are genuinely several — otherwise the dedup below is vacuous.
	destinations := 0
	for _, c := range survey.Changed {
		if c.Kind == "skills" && c.Surface == "mine" {
			destinations++
		}
	}
	if destinations < len(dirs) {
		t.Fatalf("fixture bug: `mine` reached %d changed destination(s), want at least %d\n%s",
			destinations, len(dirs), report)
	}

	adopted := survey.SkillNames(skillAdopted)
	if len(adopted) != 1 || adopted[0] != "mine" {
		t.Fatalf("one skill in %d agent dirs is ONE skill: want [mine], got %v (the survey "+
			"counts %d destinations for it)\n%s", len(dirs), adopted, destinations, report)
	}
	if !strings.Contains(report, "1 skill would move into your local pack") {
		t.Errorf("the counts must state the SKILL count, not the destination count "+
			"(§4.3: fourteen, not seventy)\n%s", report)
	}
	// And the destination roll-up must NOT be the number the reader is handed as the answer.
	if strings.Contains(report, fmt.Sprintf("%d skills would move", destinations)) {
		t.Errorf("the report counts destinations as skills\n%s", report)
	}
}

// TestHostApplySurveyTiersASkillAdoptionAsALoss pins §4.1's tier at the skills call site: an
// adoption takes something of the user's and is tier 3, a plain composition is tier 2.
//
// It fails if printSkillResult passes a constant tier — which is the whole failure mode a
// "tier" is meant to prevent, since a constant is exactly what a log level degenerates into.
func TestHostApplySurveyTiersASkillAdoptionAsALoss(t *testing.T) {
	multiAgentSkillFixture(t)
	survey, report := surveyApply(t)
	var seenLoss, seenRun bool
	for _, c := range survey.Changed {
		switch {
		case c.Kind == "skills" && c.Surface == "mine":
			if c.Tier != tierLoss {
				t.Errorf("an ADOPTED skill is a §4.1 tier-3 loss — it moves content of the "+
					"user's out of %s; got tier %d\n%s", c.Path, c.Tier, report)
			}
			seenLoss = true
		case c.Tier == tierRun:
			seenRun = true
		}
	}
	if !seenLoss {
		t.Fatalf("fixture bug: no adopted skill in the changed set\n%s", report)
	}
	if !seenRun {
		t.Errorf("every changed destination came back tier 3 — the tier is not being "+
			"decided per fact, which makes the assertion above vacuous\n%s", report)
	}
}

// multiAgentMCPFixture settles a home and then hand-adds ONE server, by the same name, to the
// three agent surfaces that own an MCP table — and each spells the table differently
// (`mcp_servers` in codex's TOML, `mcp` in opencode's JSON, `mcpServers` in agy's).
//
// That spelling is the whole reason §4.3 counts entry losses by NAME: the raw loss strings are
// table-qualified, so three agents holding one server the user added produce three strings,
// and the measured home produced NINE for three servers (§3.3's third row — *"three lines
// read as three problems with three fixes, when they are one problem with one fix"*).
func multiAgentMCPFixture(t *testing.T) (home string, surfaces int) {
	t.Helper()
	home = shippedPacksFixture(t)
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("settling apply rc=%d\n%s", rc, report)
	}
	edits := []struct{ path, old, new string }{
		{filepath.Join(home, ".codex", "config.toml"), "[mcp_servers]",
			"[mcp_servers]\n[mcp_servers.handmade]\ncommand = \"echo\""},
		{filepath.Join(home, ".config", "opencode", "opencode.json"), `"mcp": {}`,
			`"mcp": {"handmade": {"type": "local", "command": ["echo"]}}`},
		{filepath.Join(home, ".gemini", "antigravity-cli", "mcp_config.json"),
			`"mcpServers": {}`, `"mcpServers": {"handmade": {"command": "echo"}}`},
	}
	for _, e := range edits {
		data, err := os.ReadFile(e.path)
		if err != nil {
			t.Fatalf("fixture bug: %v", err)
		}
		edited := strings.Replace(string(data), e.old, e.new, 1)
		if edited == string(data) {
			t.Fatalf("fixture bug: no %q table in %s:\n%s", e.old, e.path, data)
		}
		if err := os.WriteFile(e.path, []byte(edited), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home, len(edits)
}

// TestHostApplySurveyCountsOneMCPEntryOnceAcrossAgents is §4.3's entry-loss count: *servers ×
// agents, said as N servers from M agents*, against the multi-agent fixture §9 step 1 names.
func TestHostApplySurveyCountsOneMCPEntryOnceAcrossAgents(t *testing.T) {
	_, surfaces := multiAgentMCPFixture(t)

	survey, report := surveyApply(t)

	// The report really does state the loss once per surface — the repetition being counted.
	if got := strings.Count(report, "would damage your existing entry"); got != surfaces {
		t.Fatalf("fixture bug: %d per-surface loss lines, want %d\n%s", got, surfaces, report)
	}
	entries, from := survey.DroppedEntries()
	if entries != 1 || from != surfaces {
		t.Errorf("one server the user added by hand, in %d agents, is ONE entry from %d "+
			"surfaces; got %d from %d (the table is spelled differently in each file, which "+
			"is why the raw strings cannot be the unit)\n%s",
			surfaces, surfaces, entries, from, report)
	}
	want := fmt.Sprintf("1 of your entries would be dropped from %d surfaces", surfaces)
	if !strings.Contains(report, want) {
		t.Errorf("want the counts to state %q\n%s", want, report)
	}
}

// TestHostApplySurveyTiersASurfaceWithLossesAsATier3 is the config half of the tier, and the
// negative control is the point: a changed surface that takes nothing of the user's stays
// tier 2, so a call site hard-coding tier 3 fails here.
func TestHostApplySurveyTiersASurfaceWithLossesAsATier3(t *testing.T) {
	home, _ := multiAgentMCPFixture(t)
	lossy := filepath.Join(home, ".gemini", "antigravity-cli", "mcp_config.json")
	// THE CONTROL: a surface that would change and takes NOTHING of the user's. Deleting a
	// rendered file is the cleanest one — the render re-creates it, and with no existing file
	// there is no existing value to overwrite. Without this the fixture's every changed
	// surface carries a loss, and "tier 3" below would be satisfied by a constant.
	clean := filepath.Join(home, ".claude", "settings.json")
	if err := os.Remove(clean); err != nil {
		t.Fatalf("fixture bug: %v", err)
	}

	survey, report := surveyApply(t)
	var found, others bool
	for _, c := range survey.Changed {
		if c.Kind != "config" {
			continue
		}
		if c.Path == lossy {
			found = true
			if c.Tier != tierLoss {
				t.Errorf("%s would DROP an entry of the user's — §4.1 tier 3; got tier %d\n%s",
					lossy, c.Tier, report)
			}
			continue
		}
		if c.Path == clean && c.Tier == tierRun {
			others = true
		}
	}
	if !found {
		t.Fatalf("fixture bug: %s is not in the changed set\n%s", lossy, report)
	}
	if !others {
		t.Errorf("%s would change and loses nothing, so it is a §4.1 tier-2 RUN FACT; it did "+
			"not come back as one, which makes the tier-3 assertion above vacuous\n%s",
			clean, report)
	}
}

// TestHostApplySurveyCountsReplacedValuesWithTheirFileCount is §4.3's *values of yours
// replaced — keys, with the file count*. Two keys in two files, so a count that collapsed
// either dimension would be visible.
func TestHostApplySurveyCountsReplacedValuesWithTheirFileCount(t *testing.T) {
	home := shippedPacksFixture(t)
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("settling apply rc=%d\n%s", rc, report)
	}
	edits := []struct{ path, old, new string }{
		{filepath.Join(home, ".claude", "settings.json"),
			`"defaultMode": "default"`, `"defaultMode": "plan"`},
		{filepath.Join(home, ".pi", "agent", "settings.json"),
			`"defaultProjectTrust": "ask"`, `"defaultProjectTrust": "always"`},
	}
	for _, e := range edits {
		data, err := os.ReadFile(e.path)
		if err != nil {
			t.Fatalf("fixture bug: %v", err)
		}
		edited := strings.Replace(string(data), e.old, e.new, 1)
		if edited == string(data) {
			t.Fatalf("fixture bug: %s does not contain %s:\n%s", e.path, e.old, data)
		}
		if err := os.WriteFile(e.path, []byte(edited), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	survey, report := surveyApply(t)
	keys, files := survey.ReplacedValues()
	if keys != len(edits) || files != len(edits) {
		t.Errorf("%d hand-edited managed values in %d files must count as %d keys in %d "+
			"files; got %d in %d\n%s", len(edits), len(edits), len(edits), len(edits),
			keys, files, report)
	}
	want := fmt.Sprintf("%d of your values would be replaced in %d files", len(edits), len(edits))
	if !strings.Contains(report, want) {
		t.Errorf("want the counts to state %q\n%s", want, report)
	}
}

// TestHostApplyVerdictNamesAMissingDependency is §4.9 in the dry run: a missing declared
// dependency is a tier-3 BLOCKER that decides the verdict.
//
// Before it, a missing host dep printed correctly — which binary, and the command that would
// install it — and then changed nothing: not the exit code, and not the roll-up, which counted
// destinations and never asked about dependencies. It was the one finding that makes the rest
// of the apply pointless, rendered as another line in the middle of 277.
//
// The exit code stays 0 and nothing is written: the dry run's whole outcome IS the prediction
// (OQ-RO5). The prompt and the fatal decline are the --assert half, and are build step 5.
func TestHostApplyVerdictNamesAMissingDependency(t *testing.T) {
	home := t.TempDir()
	packDir := filepath.Join(t.TempDir(), "needy")
	writeFile(t, filepath.Join(packDir, "pack.json"),
		`{"name":"needy","description":"d","contributes":[`+
			`{"kind":"requires","bin":"yolo-absent-probe-bin"}]}`)
	selectPacks(t, home, `"claude",{"source":"file://`+packDir+`","name":"needy"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	survey, report := surveyApply(t)
	if got := survey.MissingDeps(); len(got) != 1 || got[0] != "yolo-absent-probe-bin" {
		t.Fatalf("the survey must name the missing binary; got %v\n%s", got, report)
	}
	if _, missing, _ := survey.Deps(); missing != 1 {
		t.Errorf("want 1 missing dependency counted; got %d\n%s", missing, report)
	}
	for _, want := range []string{
		"An --assert would NOT complete", "yolo-absent-probe-bin",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the verdict must state the blocker and name it; want %q\n%s", want, report)
		}
	}
	// It must NOT claim the apply would complete — the assertion above passes on a report
	// that says both.
	if strings.Contains(report, "An --assert would complete.") {
		t.Errorf("a missing dependency cannot coexist with a would-complete verdict\n%s", report)
	}
}

// TestHostApplySurveyCountsPresentDependencies is the other side of the same probe, and the
// control for the test above: a present dependency is a tier-2 run fact that is COUNTED, never
// itemized. Without it, deleting the probe's call site would be caught only by a fixture that
// happens to declare a missing binary.
func TestHostApplySurveyCountsPresentDependencies(t *testing.T) {
	shippedPacksFixture(t)
	survey, report := surveyApply(t)
	present, missing, _ := survey.Deps()
	if present == 0 {
		t.Fatalf("the shipped packs declare host programs and this jail has them — the survey "+
			"saw none, so the dep probe's call site is not wired\n%s", report)
	}
	if missing != 0 {
		t.Skipf("this host is missing %d declared dependency(ies); the count below is about "+
			"the present ones", missing)
	}
	want := fmt.Sprintf("%d declared dependencies present", present)
	if !strings.Contains(report, want) {
		t.Errorf("want the counts to state %q\n%s", want, report)
	}
}

// TestHostApplyVerdictNamesAPackThatFailedToRender is §4.3's render-failure row, and the
// reason it outranks every other verdict: the pack's surfaces are absent from every count
// below the sentence, so a verdict drawn from those counts alone would report a completed
// apply out of a traversal that lost a pack. The error itself stays on stderr, where it is,
// and the exit code stays 1.
func TestHostApplyVerdictNamesAPackThatFailedToRender(t *testing.T) {
	home := t.TempDir()
	packDir := filepath.Join(t.TempDir(), "bust")
	// An unknown surface `mode` is a PACK-LEVEL problem — RenderHostPack refuses the whole
	// pack rather than one surface — which is exactly the class this row exists for.
	writeFile(t, filepath.Join(packDir, "pack.json"),
		`{"name":"bust","description":"d","contributes":[{"kind":"config","config":[`+
			`{"agent":"bust","name":"s","path":"~/.bust/x.json","codec":"json",`+
			`"mode":"nonsense","managed":{"a":1}}]}]}`)
	selectPacks(t, home, `{"source":"file://`+packDir+`","name":"bust"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	rc, report := applyAt(t, false)
	if rc != 1 {
		t.Fatalf("a pack that fails to render exits 1; got rc=%d\n%s", rc, report)
	}
	for _, want := range []string{"would be incomplete", "bust"} {
		if !strings.Contains(report, want) {
			t.Errorf("the verdict must name the failure and the pack; want %q\n%s", want, report)
		}
	}
	if strings.Contains(report, "Nothing to do") || strings.Contains(report, "would complete.") {
		t.Errorf("a failed render cannot report a clean outcome\n%s", report)
	}
}
