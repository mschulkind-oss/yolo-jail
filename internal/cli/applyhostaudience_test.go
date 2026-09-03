package cli

// applyhostaudience_test.go is the `yolo host apply` half of briefing-audiences.md: the
// command-level pin that an ADDRESSED contribution reaches only its audience in a REAL home,
// and that the report says so.
//
// The composition itself is pinned at the entrypoint level
// (internal/entrypoint/hostbriefingaudience_test.go). These exist because the two things only
// this level can measure are the FILE ON DISK and the OBSERVE REPORT — §9 step 2's own
// "immediately observable via `yolo host apply --observe`".

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// audienceFixture selects two agent packs — each declaring where its agent reads AND the
// identity that destination answers to — plus one content pack whose prose is addressed to
// just one of them. TWO agent packs, because "reached only its audience" is only a real
// measurement when there is somewhere else it could have gone.
func audienceFixture(t *testing.T, agents string) (home string) {
	t.Helper()
	home = t.TempDir()
	base := t.TempDir()
	var entries []string
	for _, a := range []struct{ name, into string }{
		{"alphacli", ".alpha/AGENTS.md"},
		{"betacli", ".beta/AGENTS.md"},
	} {
		dir := filepath.Join(base, a.name)
		writeFile(t, filepath.Join(dir, "pack.json"),
			`{"name":"`+a.name+`","description":"a","contributes":[`+
				`{"kind":"program","bin":"`+a.name+`","via":"npm","package":"`+a.name+`"},`+
				`{"kind":"briefing","into":"`+a.into+`","agent":"`+a.name+`"}]}`)
		entries = append(entries, `{"source":"file://`+dir+`","name":"`+a.name+`"}`)
	}
	house := filepath.Join(base, "house")
	writeFile(t, filepath.Join(house, "pack.json"),
		`{"name":"house","description":"h","contributes":[`+
			`{"kind":"briefing","from":"prose/alpha.md","agents":[`+agents+`]}]}`)
	writeFile(t, filepath.Join(house, "prose", "alpha.md"), "Alpha-only rule.\n")
	entries = append(entries, `{"source":"file://`+house+`","name":"house"}`)

	selectPacks(t, home, strings.Join(entries, ","))
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

// THE FILE ON DISK. Addressed prose lands in the home of the agent it named and nowhere else —
// the assertion no unit test can make, because the render's write half is a different function
// from its compose half.
func TestApplyHostDeliversAddressedProseOnlyToItsAudience(t *testing.T) {
	home := audienceFixture(t, `"alphacli"`)

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("host apply --assert rc=%d\n%s", rc, report)
	}
	alpha, err := os.ReadFile(filepath.Join(home, ".alpha", "AGENTS.md"))
	if err != nil {
		t.Fatalf("the addressed prose never reached alphacli's home: %v\n%s", err, report)
	}
	if !strings.Contains(string(alpha), "Alpha-only rule.") {
		t.Errorf(".alpha/AGENTS.md is missing the prose addressed to it:\n%s", alpha)
	}
	beta, err := os.ReadFile(filepath.Join(home, ".beta", "AGENTS.md"))
	if err == nil && strings.Contains(string(beta), "Alpha-only rule.") {
		t.Errorf("alphacli-addressed prose was broadcast into betacli's home — the whole "+
			"defect this design closes:\n%s", beta)
	}
}

// THE REPORT. An addressed delivery must read as ADDRESSED, naming the audience and the file:
// "declares no destination" is false of a pack that declared precisely who its prose was for,
// and it makes a working selector indistinguishable from a typo.
func TestApplyHostReportNamesAnAddressedContributionsAudience(t *testing.T) {
	audienceFixture(t, `"alphacli"`)

	rc, report := applyWith(t, false, strings.NewReader(""))
	if rc != 0 {
		t.Fatalf("host apply --observe rc=%d\n%s", rc, report)
	}
	if n := countLines(report, "house", "addresses alphacli", "prose/alpha.md",
		".alpha/AGENTS.md"); n != 1 {
		t.Fatalf("want exactly one line naming the audience, the source and the destination "+
			"it reached, got %d:\n%s", n, report)
	}
	if n := countLines(report, "house", "declares no destination"); n != 0 {
		t.Errorf("an addressed pack was reported as SILENT — it declared an audience on "+
			"purpose and deliberately no path (P4), so this line describes the opposite of "+
			"what the author did:\n%s", report)
	}
}

// P3 AT THE HOST NOTCH: an `agents` selector naming an agent this machine's packs do not
// provide REFUSES the apply, and writes nothing. The second of the two gates §4.3 selects.
func TestApplyHostRefusesAnAudienceNoPackProvides(t *testing.T) {
	home := audienceFixture(t, `"alphaclu"`) // a typo for alphacli
	before := treeHashes(t, home)

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc == 0 {
		t.Fatalf("host apply --assert accepted prose addressed to nobody:\n%s", report)
	}
	for _, want := range []string{
		"alphaclu",
		"pack house",
		`did you mean "alphacli"`,
		"Agents your `packs` provide",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report missing %q; got:\n%s", want, report)
		}
	}
	if after := treeHashes(t, home); len(after) != len(before) {
		t.Errorf("the refused apply changed the home: %d files before, %d after",
			len(before), len(after))
	}
	if _, err := os.Stat(filepath.Join(home, ".alpha", "AGENTS.md")); err == nil {
		t.Error("the refused apply generated a briefing anyway")
	}
}

// R1, AND THE HALF `Orphaned []Kind` CANNOT SAY. The name is fine — the pre-flight above would
// have refused it otherwise — but the owning pack declares no destination of that KIND with
// that identity. So the apply must not refuse (the remedy belongs to the OWNING pack, R4), and
// must not print the kind-level "declare `into` in house's pack.json" advice either, since
// naming a path is the one thing an addressed pack must never do (P4).
func TestApplyHostReportsAnAudienceThatReachedNoDestinationOfThatKind(t *testing.T) {
	home := t.TempDir()
	base := t.TempDir()
	// alphacli owns the name and declares an identity on its BRIEFING only.
	agent := filepath.Join(base, "alphacli")
	writeFile(t, filepath.Join(agent, "pack.json"),
		`{"name":"alphacli","description":"a","contributes":[`+
			`{"kind":"program","bin":"alphacli","via":"npm","package":"alphacli"},`+
			`{"kind":"briefing","into":".alpha/AGENTS.md","agent":"alphacli"},`+
			`{"kind":"skills","into":".alpha/skills"}]}`)
	// house addresses its SKILLS to alphacli, which no destination claims.
	house := filepath.Join(base, "house")
	writeFile(t, filepath.Join(house, "pack.json"),
		`{"name":"house","description":"h","contributes":[`+
			`{"kind":"skills","from":"skills","agents":["alphacli"]}]}`)
	writeFile(t, filepath.Join(house, "skills", "demo", "SKILL.md"), "---\nname: demo\n---\n")
	selectPacks(t, home,
		`{"source":"file://`+agent+`","name":"alphacli"},`+
			`{"source":"file://`+house+`","name":"house"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	rc, report := applyWith(t, false, strings.NewReader(""))
	if rc != 0 {
		t.Fatalf("a GOOD name that reached no destination must be reported, not refused — the "+
			"remedy is an `agent` on the owning pack's skills contribution (R4); rc=%d\n%s",
			rc, report)
	}
	if n := countLines(report, "house", "addresses alphacli", "declares that identity"); n != 1 {
		t.Fatalf("want exactly one line naming the audience that reached nothing, got %d:\n%s",
			n, report)
	}
	if n := countLines(report, "house", "declare `into`"); n != 0 {
		t.Errorf("the kind-level orphan advice reached an ADDRESSED contribution — telling this "+
			"author to name a path is the one thing P4 forbids:\n%s", report)
	}
}
