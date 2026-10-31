package check

// pluginagent_test.go drives the host's skills composition through `yolo check` with the real
// claude and pi packs (pack-naming second review, major 3). A whole wrapped plugin's inner skill
// `review` meets a bare pack's `review`: pi lists the inner skill by its own name, so the clash is
// refused at pi's skills dir; Claude Code shows it as /<plugin>:review, so Claude's dir is clean.
// The agent comes from the shipped packs' own `skills` contributions (compose.go, `d.Agent =
// c.Agent`), which this test fails without.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFixtureFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCheckRefusesAPluginsInnerSkillAtPiButNotAtClaude(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	cfg := filepath.Join(home, ".config", "yolo-jail")
	both := `{"kind":"skills","from":"skills","agents":["claude","pi"]}`
	writeFixtureFiles(t, filepath.Join(cfg, "packs"), map[string]string{
		"kit/pack.json": `{"skills_tier":"namespaced","contributes":[` + both + `]}`,
		"kit/skills/kit/.claude-plugin/plugin.json": `{"name":"kit"}`,
		"kit/skills/kit/skills/rev/SKILL.md":        "---\nname: review\n---\nR.\n",
		"other/pack.json":                           `{"contributes":[` + both + `]}`,
		"other/skills/review/SKILL.md":              "---\nname: review\n---\nO.\n",
	})
	writeFixtureFiles(t, cfg, map[string]string{"config.toml": `packs = ["claude", "pi", "local/kit", ` +
		`{ pack = "local/other", bare = true }]` + "\n"})

	_, out := runSectionPacks()
	var clashes []string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, `deliver the name "review"`) {
			clashes = append(clashes, line)
		}
	}
	if len(clashes) != 1 || !strings.Contains(clashes[0], filepath.Join(".pi", "agent", "skills")) {
		t.Fatalf("want exactly one clash on review, at pi's skills dir; got %q in:\n%s", clashes, out)
	}
}
