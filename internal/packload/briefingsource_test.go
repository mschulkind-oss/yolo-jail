package packload

// briefingsource_test.go pins the ONE resolver both notches read a `briefing` contribution's
// prose through (roadmap.md §6a-4). The notch-level gates live in
// internal/cli/run/packbriefingfrom_test.go and internal/entrypoint/hostbriefing_test.go; these
// pin the precedence itself, which is what the divergence was about.

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
		path := filepath.Join(root, name)
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

// The declared `from` WINS over both conventional names.
func TestBriefingProseForPrefersTheDeclaredFrom(t *testing.T) {
	p := &Pack{Name: "p", Root: proseTree(t, map[string]string{
		"house-rules.md": "house\n", "AGENTS.md": "agents\n", "CLAUDE.md": "claude\n",
	})}
	got, prob := p.BriefingProseFor(briefingContribution("house-rules.md"))
	if got != "house" || prob != "" {
		t.Errorf("BriefingProseFor = %q, %q; want the DECLARED source", got, prob)
	}
}

// AGENTS.md is THE convention and the ONLY one: an omitted `from` reads AGENTS.md, and a
// pack whose prose lives in CLAUDE.md briefs NOTHING until it says so with `from`
// (pack-code-separation.md §3.3 — CLAUDE.md left DefaultBriefingFiles on 2026-08-17).
func TestBriefingProseForConventionIsAgentsMdAlone(t *testing.T) {
	p := &Pack{Name: "p", Root: proseTree(t, map[string]string{
		"AGENTS.md": "agents\n", "CLAUDE.md": "claude\n",
	})}
	if got, _ := p.BriefingProseFor(briefingContribution("")); got != "agents" {
		t.Errorf("BriefingProseFor = %q, want AGENTS.md to be the convention", got)
	}
	// CLAUDE.md is NOT reached when AGENTS.md is absent — that fallback is gone. The pack
	// has prose on disk and delivers none of it, which is the whole cost of the ruling and
	// is meant to be visible here rather than discovered in a jail.
	q := &Pack{Name: "q", Root: proseTree(t, map[string]string{"CLAUDE.md": "claude\n"})}
	if got, _ := q.BriefingProseFor(briefingContribution("")); got != "" {
		t.Errorf("BriefingProseFor = %q, want no prose: CLAUDE.md is no longer conventional "+
			"and a pack keeping its prose there must declare `from`", got)
	}
	// …and declaring it is all it takes.
	if got, prob := q.BriefingProseFor(briefingContribution("CLAUDE.md")); got != "claude" || prob != "" {
		t.Errorf("BriefingProseFor(from=CLAUDE.md) = %q, %q; an explicit `from` must still "+
			"read it", got, prob)
	}
}

// An EMPTY candidate is skipped rather than winning — "the first one that exists and is
// NON-EMPTY". With the convention down to one name the chain that demonstrates it is
// [from, AGENTS.md]: a declared source that is a whitespace stub still falls back.
func TestBriefingProseForSkipsAnEmptyCandidate(t *testing.T) {
	p := &Pack{Name: "p", Root: proseTree(t, map[string]string{
		"house-rules.md": "\n \n", "AGENTS.md": "agents\n",
	})}
	got, prob := p.BriefingProseFor(briefingContribution("house-rules.md"))
	if got != "agents" {
		t.Errorf("BriefingProseFor = %q, want an empty declared source to be skipped", got)
	}
	if !strings.Contains(prob, "house-rules.md") {
		t.Errorf("the substitution must still be reported; got %q", prob)
	}
}

// A CONVENTIONAL `from` that is absent is SILENT. All six shipped packs declare
// `from: "AGENTS.md"` and carry no such file, so a warning here would fire on every launch and
// every apply of a stock config.
//
// "Conventional" now means AGENTS.md ALONE, so `from: "CLAUDE.md"` is an ordinary declared
// source and its absence REPORTS like any other. That is the second half of the same ruling
// and it is deliberate: once yolo stops reading a name for free, naming it is a claim about
// the pack's content, and an unmet claim is exactly what missingBriefingFromProblem is for.
func TestBriefingProseForConventionalAbsenceIsSilent(t *testing.T) {
	p := &Pack{Name: "p", Root: t.TempDir()}
	for _, from := range []string{"", "AGENTS.md"} {
		if got, prob := p.BriefingProseFor(briefingContribution(from)); got != "" || prob != "" {
			t.Errorf("from=%q gave %q, %q; the convention being absent is the NORMAL case",
				from, got, prob)
		}
	}
	if got, prob := p.BriefingProseFor(briefingContribution("CLAUDE.md")); got != "" ||
		!strings.Contains(prob, "CLAUDE.md") {
		t.Errorf("from=CLAUDE.md gave %q, %q; it is a declared source now, so its absence "+
			"must be reported", got, prob)
	}
}

// A NON-CONVENTIONAL `from` that is absent falls back — BriefingCandidates' contract — but NOT
// silently. Both halves are the point: narrowing the fallback would change what the host notch
// has always done, and staying silent is the accepted-and-ignored defect §6a-4 is about.
func TestBriefingProseForReportsAnIgnoredDeclaredFrom(t *testing.T) {
	p := &Pack{Name: "p", Root: proseTree(t, map[string]string{"AGENTS.md": "agents\n"})}
	got, prob := p.BriefingProseFor(briefingContribution("house-rules.md"))
	if got != "agents" {
		t.Errorf("BriefingProseFor = %q, want the conventional fallback", got)
	}
	if !strings.Contains(prob, "house-rules.md") || !strings.Contains(prob, "instead") {
		t.Errorf("the problem must name the ignored declaration and the substitution; got %q", prob)
	}

	// With nothing conventional either, the message is the sharper one: this pack briefs nothing.
	q := &Pack{Name: "q", Root: t.TempDir()}
	got, prob = q.BriefingProseFor(briefingContribution("house-rules.md"))
	if got != "" || !strings.Contains(prob, "no prose") {
		t.Errorf("BriefingProseFor = %q, %q; want the briefs-nothing message", got, prob)
	}
}

// An ESCAPING `from` is REFUSED outright, not fallen back from. `from` is manifest data a caller
// may hold with its Decode problems discarded, and the file it names is read as INSTRUCTIONS.
func TestBriefingProseForRefusesAnEscapingFrom(t *testing.T) {
	p := &Pack{Name: "p", Root: proseTree(t, map[string]string{"AGENTS.md": "agents\n"})}
	got, prob := p.BriefingProseFor(briefingContribution("../../.ssh/id_rsa"))
	if got != "" {
		t.Errorf("an escaping `from` returned content: %q", got)
	}
	if !strings.Contains(prob, "escapes the pack tree") {
		t.Errorf("the refusal must name the cause; got %q", prob)
	}
}

// BriefingProse (the JAIL entry) falls back to the convention for a pack that declares NO
// briefing contribution — the zero-ceremony case both notches depend on.
func TestBriefingProseZeroCeremonyPack(t *testing.T) {
	p := &Pack{Name: "p", Decl: &packdecl.Manifest{},
		Root: proseTree(t, map[string]string{"AGENTS.md": "bare\n"})}
	got, problems := p.BriefingProse()
	if got != "bare" || len(problems) != 0 {
		t.Errorf("BriefingProse = %q, %v; a manifest-less pack must still contribute its "+
			"AGENTS.md", got, problems)
	}
}

// BriefingProse takes the FIRST NON-EMPTY contribution, which is a real limit of the jail's
// one-text-per-pack composition rather than a resolution rule — stated here so a reader does not
// mistake it for the host's per-destination behavior.
func TestBriefingProseTakesTheFirstNonEmptyContribution(t *testing.T) {
	p := &Pack{Name: "p",
		Root: proseTree(t, map[string]string{"a.md": "first\n", "b.md": "second\n"}),
		Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindBriefing, From: "a.md", Into: ".claude/CLAUDE.md"},
			{Kind: packdecl.KindBriefing, From: "b.md", Into: ".codex/AGENTS.md"},
		}}}
	if got, _ := p.BriefingProse(); got != "first" {
		t.Errorf("BriefingProse = %q, want the first contribution's prose", got)
	}
}
