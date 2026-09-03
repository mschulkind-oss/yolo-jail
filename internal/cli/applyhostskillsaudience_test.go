package cli

// applyhostskillsaudience_test.go is the HOST notch's `skills` audience (OQ-BA4) plus the
// step-7 reporting (§9): `yolo pack footprint` and `yolo pack lint` state a contribution's
// targeting.
//
// The host half of `skills` needed no new routing code — packload's borrowedDestinations is
// generic over the kind, so the same filter that routes an addressed briefing routes an
// addressed skills tree — which is exactly why it needs a test at THIS level: the assertion
// that the generic path really covers the second kind is a file in a real home, not a unit.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// skillsAudienceHome selects two agent packs — each naming its own skills destination and
// declaring the identity it answers to — plus one content pack whose skills are addressed to
// the first.
func skillsAudienceHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	base := t.TempDir()
	var entries []string
	for _, a := range []string{"alphacli", "betacli"} {
		dir := filepath.Join(base, a)
		writeFile(t, filepath.Join(dir, "pack.json"),
			`{"name":"`+a+`","description":"a","contributes":[`+
				`{"kind":"program","bin":"`+a+`","via":"npm","package":"`+a+`"},`+
				`{"kind":"skills","into":".`+a+`/skills","agent":"`+a+`"}]}`)
		entries = append(entries, `{"source":"file://`+dir+`","name":"`+a+`"}`)
	}
	house := filepath.Join(base, "house")
	writeFile(t, filepath.Join(house, "pack.json"),
		`{"name":"house","description":"h","contributes":[`+
			`{"kind":"skills","from":"skills","agents":["alphacli"]}]}`)
	writeFile(t, filepath.Join(house, "skills", "alpha-only", "SKILL.md"),
		"---\nname: alpha-only\n---\n")
	entries = append(entries, `{"source":"file://`+house+`","name":"house"}`)

	selectPacks(t, home, strings.Join(entries, ","))
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

// THE FILE ON DISK: the addressed skill lands in the home of the agent it named and nowhere
// else.
func TestApplyHostDeliversAnAddressedSkillOnlyToItsAudience(t *testing.T) {
	home := skillsAudienceHome(t)

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("host apply --assert rc=%d\n%s", rc, report)
	}
	if _, err := os.Stat(filepath.Join(home, ".alphacli", "skills", "alpha-only")); err != nil {
		t.Errorf("the addressed skill never reached alphacli's home: %v\n%s", err, report)
	}
	if _, err := os.Stat(filepath.Join(home, ".betacli", "skills", "alpha-only")); err == nil {
		t.Errorf("an alphacli-addressed skill was delivered into betacli's home — the defect "+
			"mergedest.go:74-76 was cited for in the design:\n%s", report)
	}
}

// STEP 7's REPORTING: `pack footprint` states a contribution's targeting, in both directions.
//
// It matters most for an ADDRESSED contribution, whose target column was BLANK: `into` is the
// target for this kind and an addressed contribution has none by design (P4), so the line read
// as a kind, a gap, and a detail. The single-pack views are exactly where an author checks
// what their manifest does before configuring it.
func TestPackFootprintStatesAContributionsTargeting(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pack.json"),
		`{"name":"house","description":"h","contributes":[`+
			`{"kind":"briefing","from":"prose/a.md","agents":["alphacli","betacli"]},`+
			`{"kind":"skills","from":"skills","agents":["alphacli"]}]}`)
	writeFile(t, filepath.Join(dir, "prose", "a.md"), "House rules.\n")
	writeFile(t, filepath.Join(dir, "skills", "demo", "SKILL.md"), "---\nname: demo\n---\n")

	var out, errw bytes.Buffer
	if rc := packMain([]string{"footprint", dir}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("footprint rc=%d: %s", rc, errw.String())
	}
	report := out.String() + errw.String()
	for _, want := range []string{
		"→ alphacli, betacli",       // the TARGET column, where a path would be
		"addressed to alphacli",     // the detail, on the skills line
		"destination inferred from", // and why it names no path
	} {
		if !strings.Contains(report, want) {
			t.Errorf("footprint missing %q — an addressed contribution's targeting is the one "+
				"thing about it that is not visible from a path:\n%s", want, report)
		}
	}
}

// The OTHER direction, and R4's own complaint: a destination that declares NO identity is
// silently unaddressable, and its users cannot tell why a scoped pack skipped it. So the
// footprint says which destinations are addressable and which are not.
func TestPackFootprintStatesWhetherADestinationIsAddressable(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pack.json"),
		`{"name":"twocli","description":"t","contributes":[`+
			`{"kind":"briefing","into":".two/AGENTS.md","agent":"twocli"},`+
			`{"kind":"skills","into":".two/skills"}]}`)

	var out, errw bytes.Buffer
	if rc := packMain([]string{"footprint", dir}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("footprint rc=%d: %s", rc, errw.String())
	}
	report := out.String() + errw.String()
	if !strings.Contains(report, "this destination IS twocli's") {
		t.Errorf("the footprint must say a destination is ADDRESSABLE and under what name:\n%s",
			report)
	}
	if !strings.Contains(report, "declares no `agent`") {
		t.Errorf("the footprint must say a destination is UNADDRESSABLE — that is R4's whole "+
			"complaint, and it is invisible from the path:\n%s", report)
	}
}
