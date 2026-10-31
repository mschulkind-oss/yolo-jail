package cli

// packlintskillstier_test.go pins `yolo pack lint`'s warning for `skills_tier = "namespaced"`
// (docs/design/pack-naming.md PN-D9): the key now only delivers a wrapped plugin whole, so a pack
// that wraps none is told it changes nothing, and a pack that wraps one, or omits the key, is not.

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPackLintWarnsWhenSkillsTierChangesNothing(t *testing.T) {
	const warning = `skills_tier = "namespaced" changes nothing here`
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "skills", "review", "SKILL.md"), "---\nname: review\n---\n")
	writeFile(t, filepath.Join(dir, "pack.toml"), "name = \"tiered\"\nskills_tier = \"namespaced\"\n\n"+
		"[[skills]]\nfrom = \"skills\"\ninto = \".claude/skills\"\n")
	rc, report := lintReport(t, dir)
	if rc != 0 {
		t.Fatalf("lint rc=%d (the key is a warning, never a failure):\n%s", rc, report)
	}
	if !strings.Contains(report, warning) {
		t.Errorf("lint does not warn that skills_tier changes nothing on a pack wrapping no plugin:\n%s", report)
	}

	wrapper := t.TempDir()
	writeFile(t, filepath.Join(wrapper, "skills", "kit", ".claude-plugin", "plugin.json"), `{"name":"kit"}`)
	writeFile(t, filepath.Join(wrapper, "skills", "kit", "skills", "go", "SKILL.md"), "---\nname: go\n---\n")
	writeFile(t, filepath.Join(wrapper, "pack.toml"), "name = \"wrapper\"\nskills_tier = \"namespaced\"\n\n"+
		"[[skills]]\nfrom = \"skills\"\ninto = \".claude/skills\"\n")
	if _, report := lintReport(t, wrapper); strings.Contains(report, warning) {
		t.Errorf("lint warns about skills_tier on a pack whose wrapped plugin it delivers whole:\n%s", report)
	}

	plain := t.TempDir()
	writeFile(t, filepath.Join(plain, "skills", "review", "SKILL.md"), "---\nname: review\n---\n")
	writeFile(t, filepath.Join(plain, "pack.toml"), "name = \"plain\"\n\n"+
		"[[skills]]\nfrom = \"skills\"\ninto = \".claude/skills\"\n")
	if _, report := lintReport(t, plain); strings.Contains(report, "skills_tier") {
		t.Errorf("lint warns about skills_tier on a pack that does not declare it:\n%s", report)
	}
}
