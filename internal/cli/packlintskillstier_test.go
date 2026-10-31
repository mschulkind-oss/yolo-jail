package cli

// packlintskillstier_test.go pins `yolo pack lint`'s warning for the retired
// `skills_tier = "namespaced"` (docs/design/pack-naming.md PN-D9): the key no longer changes any
// name, so a pack declaring it is told so, and a pack that does not is not.

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPackLintWarnsThatSkillsTierIsIgnored(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "skills", "review", "SKILL.md"), "---\nname: review\n---\n")
	writeFile(t, filepath.Join(dir, "pack.toml"), "name = \"tiered\"\nskills_tier = \"namespaced\"\n\n"+
		"[[skills]]\nfrom = \"skills\"\ninto = \".claude/skills\"\n")
	rc, report := lintReport(t, dir)
	if rc != 0 {
		t.Fatalf("lint rc=%d (the retired key is a warning, never a failure):\n%s", rc, report)
	}
	if !strings.Contains(report, `skills_tier = "namespaced" is ignored`) {
		t.Errorf("lint does not warn that skills_tier is ignored:\n%s", report)
	}

	plain := t.TempDir()
	writeFile(t, filepath.Join(plain, "skills", "review", "SKILL.md"), "---\nname: review\n---\n")
	writeFile(t, filepath.Join(plain, "pack.toml"), "name = \"plain\"\n\n"+
		"[[skills]]\nfrom = \"skills\"\ninto = \".claude/skills\"\n")
	if _, report := lintReport(t, plain); strings.Contains(report, "skills_tier") {
		t.Errorf("lint warns about skills_tier on a pack that does not declare it:\n%s", report)
	}
}
