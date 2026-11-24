package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE CALL SITE: `yolo host apply` itself writes the replacement into the briefing, and writes no
// per-workspace setting into the real home (IF-D13). Delete the hostBriefingExtra call and the
// replacement never reaches ~/.claude/CLAUDE.md.
func TestApplyHostWritesTheUserScopesReplacementIntoTheBriefingAndNoSettings(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "rules.md"), []byte("REPLACEMENT RULE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	selectPacksWith(t, home, `"claude"`, `,"repo_instructions":{"mode":"replace","replace_with":"~/rules.md"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("apply rc=%d\n%s", rc, report)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".claude", "CLAUDE.md"))
	if err != nil {
		t.Fatalf("the briefing was not generated: %v", err)
	}
	if !strings.Contains(string(raw), "REPLACEMENT RULE") {
		t.Errorf("the replacement is not in the host briefing:\n%s", raw)
	}
	if settings, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json")); err == nil &&
		strings.Contains(string(settings), "claudeMdExcludes") {
		t.Errorf("apply wrote a per-workspace exclusion into the shared real home:\n%s", settings)
	}
}
