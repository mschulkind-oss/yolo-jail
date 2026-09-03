package cli

// hostapplysurvey_test.go pins THE CHANGE PREDICATE end to end, through the same call site
// `yolo host apply --dry-run` uses (docs/design/host-apply-staleness.md §3.4, §10 step 1).
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

	// claude/settings' managed layer asserts preferences.autoUpdaterStatus at the host notch.
	settings := filepath.Join(home, ".claude", "settings.json")
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("fixture bug: %v", err)
	}
	edited := strings.Replace(string(data), `"autoUpdaterStatus": "disabled"`,
		`"autoUpdaterStatus": "enabled"`, 1)
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
