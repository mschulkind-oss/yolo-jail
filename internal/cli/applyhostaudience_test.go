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
