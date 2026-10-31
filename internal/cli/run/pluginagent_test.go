package run

// pluginagent_test.go drives the jail's skills plan through the launch pre-flight with the real
// claude and pi packs (pack-naming second review, major 3). A whole wrapped plugin's inner skill
// `review` meets a bare pack's `review`: pi lists the inner skill by its own name, so the launch
// refuses the clash at pi's skills dir, while Claude Code shows it as /<plugin>:review and its dir
// is clean. The agent comes from each target (jailcontent.SkillPlan, `Agent: t.Agent`), which this
// test fails without.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTree(t *testing.T, root string, files map[string]string) {
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

// The converted-config warning at the launch (pack-naming second review, minor 2): a user config
// the old migrator converted has its renamed skills named on stderr, with the command that keeps
// the old names. It fails if packs.go stops printing packload.DefaultNamespaceRenames.
func TestTheLaunchNamesTheSkillsAConvertedConfigRenames(t *testing.T) {
	home := packHome(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	cfg := filepath.Join(home, ".config", "yolo-jail")
	writeTree(t, cfg, map[string]string{
		"packs/tools/pack.json":              `{"contributes":[{"kind":"skills","from":"skills"}]}`,
		"packs/tools/skills/review/SKILL.md": "---\nname: review\n---\nR.\n",
		"config.toml":                        "packs = [\"claude\", \"local/tools\"]\n",
		"config.jsonc.pre-migrate":           "{}\n",
	})
	var stderr strings.Builder
	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: &stderr}
	if _, _, _, err := o.stagePacks("converted-warning"); err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	for _, want := range []string{"skill review → tools-review",
		"yolo config set 'packs[1]' '{ pack = \"local/tools\", bare = true }' --user"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("the launch does not say %q:\n%s", want, stderr.String())
		}
	}
}

func TestTheLaunchRefusesAPluginsInnerSkillAtPiButNotAtClaude(t *testing.T) {
	parent := t.TempDir()
	writeTree(t, parent, map[string]string{
		"kit/pack.json": `{"name":"kit","skills_tier":"namespaced","contributes":[{"kind":"skills","from":"skills"}]}`,
		"kit/skills/kit/.claude-plugin/plugin.json": `{"name":"kit"}`,
		"kit/skills/kit/skills/rev/SKILL.md":        "---\nname: review\n---\nR.\n",
		"other/pack.json":                           `{"name":"other","contributes":[{"kind":"skills","from":"skills"}]}`,
		"other/skills/review/SKILL.md":              "---\nname: review\n---\nO.\n",
	})
	entries := `{"source":"file://` + filepath.Join(parent, "kit") + `","name":"kit"},` +
		`{"source":"file://` + filepath.Join(parent, "other") + `","name":"other"}`

	for _, tc := range []struct {
		name, agents string
		refused      bool
	}{
		{"claude alone", `"claude"`, false},
		{"claude and pi", `"claude","pi"`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := packHome(t)
			writeUserPacks(t, home, `[`+tc.agents+`,`+entries+`]`)
			o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: discardBuf()}
			_, _, _, err := o.stagePacks("plugin-agent-" + strings.ReplaceAll(tc.name, " ", "-"))
			if !tc.refused {
				if err != nil {
					t.Fatalf("Claude Code namespaces a plugin's skills; the launch must not refuse: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("the inner review must clash with the bare review at pi's skills dir")
			}
			msg := err.Error()
			if !strings.Contains(msg, `~/.pi/agent/skills: 2 packs deliver the name "review"`) ||
				strings.Contains(msg, "~/.claude/skills:") {
				t.Errorf("want the clash at pi's dir and none at Claude's:\n%s", msg)
			}
		})
	}
}
