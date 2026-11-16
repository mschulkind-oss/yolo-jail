package jailcontent

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestImportedSkillUsesConfinedAllOrNothingStaging(t *testing.T) {
	f := newWSFixture(t)
	badRoot := realTempDir(t)
	fifoRoot := realTempDir(t)
	goodRoot := realTempDir(t)
	must(t, os.WriteFile(filepath.Join(badRoot, "SKILL.md"), []byte("partial secret skill"), 0o644))
	must(t, os.WriteFile(filepath.Join(badRoot, "safe.md"), []byte("must not stage"), 0o644))
	must(t, os.Symlink(f.secret, filepath.Join(badRoot, "escape")))
	must(t, os.WriteFile(filepath.Join(fifoRoot, "SKILL.md"), []byte("special file skill"), 0o644))
	must(t, syscall.Mkfifo(filepath.Join(fifoRoot, "pipe"), 0o644))
	must(t, os.WriteFile(filepath.Join(goodRoot, "SKILL.md"), []byte("good sibling"), 0o644))

	layer, err := stageWorkspaceLayer(&WorkspaceSkills{Root: f.ws, Imported: []ImportedWorkspaceSkill{
		{Handle: "acme", Name: "review", Root: badRoot},
		{Handle: "acme", Name: "pipe-skill", Root: fifoRoot},
		{Handle: "acme", Name: "other", Root: goodRoot},
	}})
	must(t, err)
	defer layer.close()
	if len(layer.skills) != 1 || layer.skills[0].name != "other" {
		t.Fatalf("staged imported skills = %+v, want only the safe sibling", layer.skills)
	}
	if !strings.Contains(layer.refused[0].Path, "skill_imports.acme/review/escape") {
		t.Fatalf("escaping link refusal did not name its imported entry: %+v", layer.refused)
	}
	fifoRefused := false
	for _, refused := range layer.refused {
		if strings.Contains(refused.Path, "skill_imports.acme/pipe-skill/pipe") {
			fifoRefused = true
		}
	}
	if !fifoRefused {
		t.Fatalf("special file refusal did not name its imported entry: %+v", layer.refused)
	}
	for _, partial := range []string{"1", "2"} {
		if _, err := os.Stat(filepath.Join(layer.dir, partial)); !os.IsNotExist(err) {
			t.Fatalf("partial files from a refused imported skill remain in scratch at %s: %v", partial, err)
		}
	}
	if got, err := os.ReadFile(filepath.Join(layer.dir, "3", "SKILL.md")); err != nil || string(got) != "good sibling" {
		t.Fatalf("safe sibling content = %q, %v", got, err)
	}
	assertNoSecretAnywhere(t, layer.dir)
}

func TestImportedSkillsAreBelowWorkspaceBuiltinAndPackSkills(t *testing.T) {
	f := newWSFixture(t)
	targets(t, map[string][]string{"claude": {".claude/skills"}})
	f.file(t, "native/review/SKILL.md", "workspace winner")
	importRoot := realTempDir(t)
	for name, body := range map[string]string{
		"review":               "import loses to workspace",
		"configuring-the-jail": "import loses to builtin",
		"pack-wins":            "import loses to pack",
		"import-only":          "import is delivered",
	} {
		root := filepath.Join(importRoot, name)
		must(t, os.MkdirAll(root, 0o755))
		must(t, os.WriteFile(filepath.Join(root, "SKILL.md"), []byte(body), 0o644))
	}
	packRoot := realTempDir(t)
	must(t, os.MkdirAll(filepath.Join(packRoot, "pack-wins"), 0o755))
	must(t, os.WriteFile(filepath.Join(packRoot, "pack-wins", "SKILL.md"), []byte("pack wins"), 0o644))
	SetPackSkillDirs([]PackSkillSource{{Dir: packRoot, Pack: "fixture"}})
	staging, report, err := PrepareSkillsWith("ws-import-precedence", &WorkspaceSkills{Root: f.ws, Dirs: []string{"native"}, Imported: []ImportedWorkspaceSkill{
		{Handle: "acme", Name: "review", Root: filepath.Join(importRoot, "review")},
		{Handle: "acme", Name: "configuring-the-jail", Root: filepath.Join(importRoot, "configuring-the-jail")},
		{Handle: "acme", Name: "pack-wins", Root: filepath.Join(importRoot, "pack-wins")},
		{Handle: "acme", Name: "import-only", Root: filepath.Join(importRoot, "import-only")},
	}})
	must(t, err)
	root := filepath.Join(staging, SkillStagingName("claude"))
	read := func(skill string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, skill, "SKILL.md"))
		must(t, err)
		return string(data)
	}
	if got := read("review"); got != "workspace winner" {
		t.Fatalf("workspace collision winner = %q", got)
	}
	if got := read("pack-wins"); got != "pack wins" {
		t.Fatalf("pack collision winner = %q", got)
	}
	if got := read("configuring-the-jail"); strings.Contains(got, "import loses") {
		t.Fatalf("built-in collision lost to imported skill: %q", got)
	}
	if got := read("import-only"); got != "import is delivered" {
		t.Fatalf("uncontested import = %q", got)
	}
	if len(report.Shadowed) == 0 {
		t.Fatal("composition did not disclose imported lower-precedence skills")
	}
}
