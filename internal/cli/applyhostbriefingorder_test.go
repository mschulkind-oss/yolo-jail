package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE HOST NOTCH COMPOSES THE SAME TWO BLOCKS IN THE SAME ORDER (docs/reference/agent-briefings.md#what-a-composed-briefing-contains,
// BD-D8 and the owner's ruling BD-D11): the user's agents_md_extra and non-shipped packs' prose,
// then a `---` rule, then yolo's host header and the shipped packs' pointers. Driven through
// `yolo host apply` itself, so the CLI's request (which hands the user-scope agents_md_extra to the
// composer) and the composer are pinned together.
func TestApplyHostPutsTheUsersProseBeforeYolosBlock(t *testing.T) {
	home := t.TempDir()
	prose := func(name, text string) string {
		dir := filepath.Join(t.TempDir(), name)
		writeFile(t, filepath.Join(dir, "pack.json"),
			`{"name":"`+name+`","description":"p","contributes":[`+
				`{"kind":"briefing","from":"briefing/prose.md","into":".claude/CLAUDE.md"}]}`)
		writeFile(t, filepath.Join(dir, "briefing", "prose.md"), text+"\n")
		return dir
	}
	mine := prose("mine", "LOCAL PACK RULE")
	// A shipped pack forked into a local copy is no longer shipped: its prose is the user's.
	fork := prose("github", "FORKED GITHUB RULE")
	selectPacksWith(t, home, `"claude",{"source":"file://`+mine+`","name":"mine"},`+
		`{"source":"file://`+fork+`","name":"github"}`, `,"agents_md_extra":"EXTRA RULE"`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("apply rc=%d\n%s", rc, report)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".claude", "CLAUDE.md"))
	if err != nil {
		t.Fatalf("the destination was not generated: %v", err)
	}
	body := string(raw)
	at := -1
	markers := []string{"EXTRA RULE", "LOCAL PACK RULE", "FORKED GITHUB RULE", "\n---\n",
		"# YOLO", "**yolo-claude**"}
	for _, m := range markers {
		i := strings.Index(body, m)
		if i <= at {
			t.Fatalf("the host briefing must read %q in that order; %q is at %d:\n%s", markers, m, i, body)
		}
		at = i
	}
	if n := strings.Count(body, "\n---\n"); n != 1 {
		t.Errorf("the two blocks are separated by exactly one rule, found %d:\n%s", n, body)
	}
}

// With no user prose the host file is yolo's block alone: no rule.
func TestApplyHostWithNoUserProseHasNoRule(t *testing.T) {
	home := t.TempDir()
	selectPacks(t, home, `"claude"`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("apply rc=%d\n%s", rc, report)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".claude", "CLAUDE.md"))
	if err != nil {
		t.Fatalf("the destination was not generated: %v", err)
	}
	if body := string(raw); !strings.HasPrefix(body, "# YOLO") || strings.Contains(body, "\n---\n") {
		t.Errorf("with no user prose the file is yolo's block alone, with no rule:\n%s", body)
	}
}
