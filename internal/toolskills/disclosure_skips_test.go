package toolskills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The skips the step names (docs/reference/mise-tool-skills.md): a skill the jail's copier or the
// selection drops is named in the host read's words, one line each, with the next step. All of
// them run through the real RunStep, so deleting its call site fails every one.

func TestRunStepNamesASkillOutsideTheInstallsDirectory(t *testing.T) {
	f := newFixture(t)
	outside := filepath.Join(f.root, "elsewhere", "s")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(outside, "SKILL.md"), []byte("x"), 0o644)
	m, _ := miseListing(t, Skill{Name: "out", Tool: "tool-a", Version: "1", Path: outside}, f.skill(t, "tool-a", "1", "ok", "x"))
	assertSkipLine(t, runStepOut(t, f, stepEnv(f, "", "tool-a"), m), "out", "tool-a")
}

func TestRunStepNamesASkillWhoseLinkEscapesIt(t *testing.T) {
	f := newFixture(t)
	secret := filepath.Join(f.root, "secret")
	_ = os.WriteFile(secret, []byte("SECRET"), 0o600)
	bad := f.skill(t, "tool-a", "1", "bad", "x")
	if err := os.Symlink(secret, filepath.Join(bad.Path, "abs")); err != nil {
		t.Fatal(err)
	}
	m, _ := miseListing(t, bad, f.skill(t, "tool-a", "1", "ok", "y"))
	assertSkipLine(t, runStepOut(t, f, stepEnv(f, "", "tool-a"), m), "bad", "tool-a")
}

func TestRunStepNamesASkillOverTheSizeCap(t *testing.T) {
	f := newFixture(t)
	big := f.skill(t, "tool-a", "1", "big", "x")
	for i := 0; i <= DefaultMaxEntries; i++ {
		if err := os.WriteFile(filepath.Join(big.Path, fmt.Sprintf("f%d", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m, _ := miseListing(t, big)
	assertSkipLine(t, runStepOut(t, f, stepEnv(f, "", "tool-a"), m), "big", "tool-a")
}

func TestRunStepNamesASkillWithAnInvalidName(t *testing.T) {
	f := newFixture(t)
	bad := f.skill(t, "tool-a", "1", "good", "x")
	bad.Name = ".hidden"
	m, _ := miseListing(t, bad)
	assertSkipLine(t, runStepOut(t, f, stepEnv(f, "", "tool-a"), m), ".hidden", "tool-a")
}

func TestRunStepNamesTheLoserOfASameNameConflictBetweenTwoTools(t *testing.T) {
	f := newFixture(t)
	m, _ := miseListing(t, f.skill(t, "tool-a", "1", "dup", "a"), f.skill(t, "tool-b", "1", "dup", "b"))
	got := runStepOut(t, f, stepEnv(f, "", "tool-a", "tool-b"), m)
	for _, want := range []string{`yolo: mise skill "dup" from tool-b not delivered: tool-a comes first in your install.mise and wins. Reorder install.mise (`, "to change that."} {
		if !strings.Contains(got, want) {
			t.Errorf("%q\nmissing %q", got, want)
		}
	}
}

// The wording is the host read's: one shared helper, so the two cannot drift.
func TestSkipLinesAreTheOneSharedWording(t *testing.T) {
	esc := func(s string) string { return "<" + s + ">" }
	got := SkipLine("n", "t", "why", esc)
	want := `yolo: mise skill "<n>" (<t>) not delivered: <why>. Fix that skill in the tool, or remove the tool from your install.mise, then relaunch yolo.`
	if got != want {
		t.Fatalf("got %q", got)
	}
	if got := ShadowLine("n", "loser", "winner", esc); !strings.HasPrefix(got, `yolo: mise skill "<n>" from <loser> not delivered: <winner> comes first in your install.mise and wins.`) {
		t.Fatalf("got %q", got)
	}
}

func assertSkipLine(t *testing.T, got, name, tool string) {
	t.Helper()
	want := `yolo: mise skill "` + name + `" (` + tool + `) not delivered: `
	if !strings.Contains(got, want) || !strings.Contains(got, "Fix that skill in the tool, or remove the tool from your install.mise, then relaunch yolo.") {
		t.Fatalf("no named skip line %q in %q", want, got)
	}
}
