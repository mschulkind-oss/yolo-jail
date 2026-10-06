package jailcontent

// workspaceskills_hostcheck_test.go pins CheckWorkspaceSkillSource, the single-source check the
// host notch's in-workspace link runs (docs/design/workspace-skills.md WS-D21): the jail's own
// reader over one directory, which names every skill it would deliver and every entry it refuses,
// and leaves nothing behind.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCheckWorkspaceSkillSourceNamesTheSkillsItWouldDeliver(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"review", "lint"} {
		p := filepath.Join(root, ".claude", "skills", name, "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("body\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	scratch := t.TempDir()
	t.Setenv("TMPDIR", scratch)
	check, err := CheckWorkspaceSkillSource(root, ".claude/skills")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(check.Skills, []string{"lint", "review"}) || len(check.Refused) != 0 {
		t.Errorf("check = %+v, want lint and review and no refusal", check)
	}
	if left, _ := os.ReadDir(scratch); len(left) != 0 {
		t.Errorf("the check left its scratch copy behind: %v", left)
	}

	// An absent directory is a check with nothing in it, not an error.
	if c, err := CheckWorkspaceSkillSource(root, ".agents/skills"); err != nil || len(c.Skills)+len(c.Refused) != 0 {
		t.Errorf("an absent source: %+v, %v", c, err)
	}
}

// An escaping link is refused by name, never by target, and the source is not clean: the host
// writes no link to it (P5).
func TestCheckWorkspaceSkillSourceRefusesAnEscapingLink(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(root, ".claude", "skills")
	if err := os.MkdirAll(filepath.Join(src, "ok"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "ok", "SKILL.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside-target")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(src, "evil")); err != nil {
		t.Fatal(err)
	}
	check, err := CheckWorkspaceSkillSource(root, ".claude/skills")
	if err != nil {
		t.Fatal(err)
	}
	if len(check.Refused) != 1 || check.Refused[0].Path != ".claude/skills/evil" {
		t.Fatalf("refusals = %+v, want the one escaping entry", check.Refused)
	}
	if strings.Contains(check.Refused[0].Reason, outside) {
		t.Errorf("the refusal names the link's target: %q", check.Refused[0].Reason)
	}
	// The source dir itself a link out of the tree: refused as a source.
	if err := os.RemoveAll(filepath.Join(root, ".agents")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".agents")); err != nil {
		t.Fatal(err)
	}
	check, err = CheckWorkspaceSkillSource(root, ".agents/skills")
	if err != nil {
		t.Fatal(err)
	}
	if len(check.Refused) != 1 || check.Refused[0].Path != ".agents/skills" {
		t.Errorf("a source behind an escaping parent: %+v, want it refused", check.Refused)
	}
}
