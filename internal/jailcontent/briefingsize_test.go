package jailcontent

import (
	"strings"
	"testing"
)

// docs/design/briefing-diet.md BD-D9: the launch's size line, its wording and when it is absent.
func TestBriefingSizeWarningNamesTheFileTheLimitAndTheNextStep(t *testing.T) {
	big := strings.Repeat("m", 41000)
	packs := []PackBriefing{
		{Name: "claude", Text: "pointer", Agents: []string{"claude"}},
		{Name: "matt", Text: big},
		{Name: "pi-only", Text: strings.Repeat("p", 90000), Agents: []string{"pi"}},
	}
	content := ComposePackBriefings("yolo body\n", packs, "claude", false)
	got := BriefingSizeWarning(BriefingSize{
		Into: ".claude/CLAUDE.md", Agent: "claude", Content: content, Limit: 40000, Packs: packs,
	})
	rest := BriefingChars(content) - 41000 - len("pointer")
	want := "~/.claude/CLAUDE.md is " + commaInt(BriefingChars(content)) + " characters, over the 40,000 " +
		"at which claude warns about a long file. Its largest parts: pack matt 41,000; the rest " +
		"(yolo's text and any host file) " + commaInt(rest) + "; pack claude 7. Move a section of pack " +
		"matt's briefing into a skill in that pack (skills/<name>/SKILL.md), which the agent reads " +
		"only when it needs it."
	if got != want {
		t.Errorf("the line is\n%q\nwant\n%q", got, want)
	}
	if strings.Contains(got, "pi-only") {
		t.Error("prose addressed to another agent is not part of this file and must not be named")
	}
}

func TestBriefingSizeWarningIsAbsentUnderTheLimitAndWithoutOne(t *testing.T) {
	content := strings.Repeat("a", 40000)
	if got := BriefingSizeWarning(BriefingSize{Into: "f", Content: content, Limit: 40000}); got != "" {
		t.Errorf("a file exactly at the limit is not over it, got %q", got)
	}
	if got := BriefingSizeWarning(BriefingSize{Into: "f", Content: content + "a", Limit: 0}); got != "" {
		t.Errorf("a destination with no declared limit gets no line, got %q", got)
	}
	if got := BriefingSizeWarning(BriefingSize{Into: "f", Content: content + "a", Limit: 40000}); got == "" {
		t.Error("one character over the limit must be disclosed")
	}
}

// With no pack prose in the file, the next step still names something to do.
func TestBriefingSizeWarningWithNoPackProseStillNamesANextStep(t *testing.T) {
	got := BriefingSizeWarning(BriefingSize{Into: ".a/AGENTS.md", Content: strings.Repeat("x", 11), Limit: 10})
	if !strings.Contains(got, "its agent warns") || !strings.Contains(got, "Move a section of the file into a skill") {
		t.Errorf("got %q", got)
	}
}

// Characters are UTF-16 code units, as a JavaScript string's length counts them.
func TestBriefingCharsCountsUTF16CodeUnits(t *testing.T) {
	for in, want := range map[string]int{"abc": 3, "é": 1, "😀": 2, "": 0} {
		if got := BriefingChars(in); got != want {
			t.Errorf("BriefingChars(%q) = %d, want %d", in, got, want)
		}
	}
}
