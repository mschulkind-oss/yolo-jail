package packload

// briefingsource_test.go pins the ONE resolver both notches read a `briefing` contribution's
// prose through (outstanding-work.md §6a-4). The notch-level gates live in
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

// AGENTS.md before CLAUDE.md when `from` is omitted — the DefaultBriefingFiles order.
func TestBriefingProseForConventionOrder(t *testing.T) {
	p := &Pack{Name: "p", Root: proseTree(t, map[string]string{
		"AGENTS.md": "agents\n", "CLAUDE.md": "claude\n",
	})}
	if got, _ := p.BriefingProseFor(briefingContribution("")); got != "agents" {
		t.Errorf("BriefingProseFor = %q, want AGENTS.md to win the convention", got)
	}
	// And CLAUDE.md is reached when AGENTS.md is absent.
	q := &Pack{Name: "q", Root: proseTree(t, map[string]string{"CLAUDE.md": "claude\n"})}
	if got, _ := q.BriefingProseFor(briefingContribution("")); got != "claude" {
		t.Errorf("BriefingProseFor = %q, want the CLAUDE.md fallback", got)
	}
}

// An EMPTY candidate is skipped rather than winning: a pack whose AGENTS.md is a stub still
// briefs from CLAUDE.md, which is what "the first one that exists and is NON-EMPTY" means.
func TestBriefingProseForSkipsAnEmptyCandidate(t *testing.T) {
	p := &Pack{Name: "p", Root: proseTree(t, map[string]string{
		"AGENTS.md": "\n \n", "CLAUDE.md": "claude\n",
	})}
	if got, _ := p.BriefingProseFor(briefingContribution("")); got != "claude" {
		t.Errorf("BriefingProseFor = %q, want an empty AGENTS.md to be skipped", got)
	}
}

// A CONVENTIONAL `from` that is absent is SILENT. All six shipped packs declare
// `from: "AGENTS.md"` and carry no such file, so a warning here would fire on every launch and
// every apply of a stock config.
func TestBriefingProseForConventionalAbsenceIsSilent(t *testing.T) {
	p := &Pack{Name: "p", Root: t.TempDir()}
	for _, from := range []string{"", "AGENTS.md", "CLAUDE.md"} {
		if got, prob := p.BriefingProseFor(briefingContribution(from)); got != "" || prob != "" {
			t.Errorf("from=%q gave %q, %q; the convention being absent is the NORMAL case",
				from, got, prob)
		}
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
