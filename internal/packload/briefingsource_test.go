package packload

// briefingsource_test.go pins BriefingProseFor — what ONE briefing contribution carries — over the
// governance predicate (governance_test.go pins the predicate itself). The notch-level gates live
// in internal/cli/run/packbriefingfrom_test.go and internal/entrypoint/hostbriefing_test.go.
//
// These replaced the fallback-chain tests (`[from, AGENTS.md]`), which pinned the opposite of
// docs/reference/pack-system.md#briefing-p1, #briefing-p4: that AGENTS.md is the convention, and that a declared
// `from` that is missing or blank delivers AGENTS.md instead. Both are now false, by ruling.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// proseTree writes {relative name → body} into a fresh dir and returns it.
func proseTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func briefingContribution(from string) packdecl.Contribution {
	return packdecl.Contribution{Kind: packdecl.KindBriefing, From: from, Into: ".claude/CLAUDE.md"}
}

// declaringPack is a pack over `files` whose manifest is exactly `c` — so BriefingProseFor(c) asks
// about a contribution the pack really declares, which is what governance is computed from.
func declaringPack(t *testing.T, files map[string]string, c ...packdecl.Contribution) *Pack {
	t.Helper()
	return &Pack{Name: "p", Root: proseTree(t, files), Decl: &packdecl.Manifest{Contributes: c}}
}

// The declared `from` is read, and ONLY it: the briefing/ files beside it are the implicit
// broadcast's, not this contribution's.
func TestBriefingProseForReadsTheDeclaredFrom(t *testing.T) {
	c := briefingContribution("prose/house-rules.md")
	p := declaringPack(t, map[string]string{
		"prose/house-rules.md": "house\n", "briefing/other.md": "other\n",
	}, c)
	got, prob := p.BriefingProseFor(c)
	if got != "house" || prob != "" {
		t.Errorf("BriefingProseFor = %q, %q; want the DECLARED source and nothing else", got, prob)
	}
}

// A ROOT AGENTS.md (or CLAUDE.md, or GEMINI.md) IS NEVER READ (P1): not by an omitted `from`, and
// not by an implicit borrower. The pack has prose on disk and delivers none of it until it moves
// under briefing/ — the ruled cost of the cut (OQ-PB3), meant to be visible here.
func TestBriefingProseForNeverReadsARootInstructionFile(t *testing.T) {
	files := map[string]string{"AGENTS.md": "agents\n", "CLAUDE.md": "claude\n", "GEMINI.md": "gemini\n"}
	omitted := briefingContribution("")
	p := declaringPack(t, files, omitted)
	if got, prob := p.BriefingProseFor(omitted); got != "" || prob != "" {
		t.Errorf("BriefingProseFor(omitted from) = %q, %q; want nothing — a root AGENTS.md is the "+
			"repository's own instructions", got, prob)
	}
	zc := declaringPack(t, files)
	if got, _ := zc.BriefingProseFor(briefingContribution("")); got != "" {
		t.Errorf("a manifest-less pack's implicit borrower read %q — P1 holds with or without a "+
			"manifest", got)
	}
}

// …and naming one explicitly is refused by the validator; a caller that discarded that refusal
// still gets NOTHING read, and a problem saying why.
func TestBriefingProseForRefusesAReservedFromEvenWhenTheRefusalWasDiscarded(t *testing.T) {
	for _, from := range []string{"AGENTS.md", "docs/CLAUDE.md", "./GEMINI.md"} {
		c := briefingContribution(from)
		p := declaringPack(t, map[string]string{
			"AGENTS.md": "a\n", "docs/CLAUDE.md": "c\n", "GEMINI.md": "g\n",
		}, c)
		got, prob := p.BriefingProseFor(c)
		if got != "" {
			t.Errorf("from=%q read %q — a repository instruction file is never pack prose", from, got)
		}
		if !strings.Contains(prob, "repository") {
			t.Errorf("from=%q: problem = %q, want one naming why it was not read", from, prob)
		}
	}
}

// A declared `from` that is missing, blank or a directory DELIVERS NOTHING AND IS REPORTED (pack-system.md#briefing-p4,
// P4) — and no other file arrives in its place, however conventional. The fallback chain is gone.
func TestBriefingProseForDeclaredSourceIsTheOnlySource(t *testing.T) {
	cases := map[string]struct {
		files map[string]string
		want  string // substring of the problem
	}{
		"missing":   {map[string]string{"briefing/x.md": "x\n", "AGENTS.md": "a\n"}, "not in its content"},
		"blank":     {map[string]string{"prose/h.md": "\n \t\n", "briefing/x.md": "x\n"}, "empty"},
		"directory": {map[string]string{"prose/h.md/inner.md": "i\n", "briefing/x.md": "x\n"}, "directory"},
	}
	for name, tc := range cases {
		c := briefingContribution("prose/h.md")
		p := declaringPack(t, tc.files, c)
		got, prob := p.BriefingProseFor(c)
		if got != "" {
			t.Errorf("%s: BriefingProseFor = %q, want nothing — a named source is the ONLY source", name, got)
		}
		if !strings.Contains(prob, "prose/h.md") || !strings.Contains(prob, tc.want) {
			t.Errorf("%s: problem = %q, want one naming prose/h.md and %q", name, prob, tc.want)
		}
	}
}

// An ABSENT CONVENTION is silent: most packs carry no prose, and every shipped agent pack's
// destination has none.
func TestBriefingProseForAbsentConventionIsSilent(t *testing.T) {
	omitted := briefingContribution("")
	p := declaringPack(t, nil, omitted)
	if got, prob := p.BriefingProseFor(omitted); got != "" || prob != "" {
		t.Errorf("BriefingProseFor = %q, %q; the convention being absent is the NORMAL case", got, prob)
	}
}

// An ESCAPING `from` is REFUSED. `from` is manifest data a caller may hold with its Decode problems
// discarded, and the file it names is read as INSTRUCTIONS.
func TestBriefingProseForRefusesAnEscapingFrom(t *testing.T) {
	c := briefingContribution("../../.ssh/id_rsa")
	p := declaringPack(t, map[string]string{"briefing/x.md": "x\n"}, c)
	got, prob := p.BriefingProseFor(c)
	if got != "" {
		t.Errorf("an escaping `from` returned content: %q", got)
	}
	if !strings.Contains(prob, "escapes the pack tree") {
		t.Errorf("the refusal must name the cause; got %q", prob)
	}
}

// A DESTINATION SHIPS NOTHING (P5): an agent pack's `{agent, into}` names where content lands.
// Before, it read the pack's own prose into its own agent through the fallback chain.
func TestBriefingProseForADestinationCarriesNothing(t *testing.T) {
	dest := packdecl.Contribution{Kind: packdecl.KindBriefing, Agent: "claude", Into: ".claude/CLAUDE.md"}
	p := declaringPack(t, map[string]string{"briefing/x.md": "x\n"}, dest)
	if got, prob := p.BriefingProseFor(dest); got != "" || prob != "" {
		t.Errorf("BriefingProseFor(destination) = %q, %q; a destination sources nothing", got, prob)
	}
}

// An omitted `from` carries EVERY unclaimed briefing/*.md, joined as one section in filename order
// with one blank line between files — the spacing between packs.
func TestBriefingProseForOmittedFromJoinsTheConvention(t *testing.T) {
	omitted := briefingContribution("")
	p := declaringPack(t, map[string]string{
		"briefing/b.md": "bee\n", "briefing/a.md": "ay\n\n", "briefing/c.md": "cee",
	}, omitted)
	got, prob := p.BriefingProseFor(omitted)
	if want := "ay\n\nbee\n\ncee"; got != want || prob != "" {
		t.Errorf("BriefingProseFor = %q, %q; want %q", got, prob, want)
	}
}
