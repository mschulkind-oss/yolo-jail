package cli

// applyconvertedwarn_test.go pins the converted-config warning at `yolo host apply` (pack-naming
// second review, minor 2): a user config the old migrator converted (a .pre-migrate copy beside it,
// an entry with no `bare`) has its renamed skills named by the apply, with the command that keeps
// the old names. It fails if apply.go stops printing packload.DefaultNamespaceRenames.

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestHostApplyNamesTheSkillsAConvertedConfigRenames(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	cfg := filepath.Join(home, ".config", "yolo-jail")
	writeFile(t, filepath.Join(cfg, "packs", "tools", "pack.json"),
		`{"contributes":[{"kind":"skills","from":"skills","into":".claude/skills"}]}`)
	writeFile(t, filepath.Join(cfg, "packs", "tools", "skills", "review", "SKILL.md"), "---\nname: review\n---\nR.\n")
	writeFile(t, filepath.Join(cfg, "config.toml"), "packs = [\"claude\", \"local/tools\"]\n")
	writeFile(t, filepath.Join(cfg, "config.jsonc.pre-migrate"), "{}\n")
	stubDeclaredBins(t)

	_, report := applyWith(t, false, nil)
	for _, want := range []string{"packs      renamed", "skill review → tools-review",
		"yolo config set 'packs[1]' '{ pack = \"local/tools\", bare = true }' --user"} {
		if !strings.Contains(report, want) {
			t.Errorf("host apply does not say %q:\n%s", want, report)
		}
	}
}
