package jailcontent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const scopedPackTOML = `{ pack = "local/corpus", skills = { dir = "skills/core-tools", only = ["skill-a"] } }`
const scopedPackAddDryRun = "yolo pack add local/corpus --skills-dir skills/core-tools --only skill-a --dry-run"

func assertScopedPackAdvice(t *testing.T, body string) {
	t.Helper()
	for _, want := range []string{
		"Direct scoped pack selection is user-only",
		scopedPackTOML,
		scopedPackAddDryRun,
		"omit `--dry-run` to write",
		"source of the user's config",
		"edit that source",
		"source is absent",
		"ask the human",
		"not the installed host config",
		"across workspaces",
		"this project's config",
		"--subdir",
		"whole pack",
		"Scoped entries activate no",
		"legacy file filters",
		"guesswork",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered configuring skill misses %q", want)
		}
	}
	if !strings.Contains(strings.ToLower(body), "typed candidate") || !strings.Contains(strings.ToLower(body), "raw") {
		t.Error("rendered configuring skill must preserve the raw/typed selector distinction")
	}
}

func TestRenderBuiltinHostSkillKeepsScopedPackEditingAdvice(t *testing.T) {
	files, err := RenderBuiltinSkills(FactSheet{Host: true, HostWorkspace: "/owned", UserConfigPath: "/owned/config.toml"})
	if err != nil {
		t.Fatal(err)
	}
	body := string(files["configuring-the-jail/SKILL.md"])
	assertScopedPackAdvice(t, body)
	for _, want := range []string{"yolo pack explain", "user TOML", "/owned/config.toml"} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered host configuring skill misses %q", want)
		}
	}
	if strings.Contains(body, "Edit the user configuration at `/owned/config.toml`") {
		t.Error("host configuring skill must not direct hand-editing the applied config")
	}
}

func TestRenderBuiltinSkillsKeepsScopedPackAuthorityAndInspectionAdvice(t *testing.T) {
	cases := []struct {
		name  string
		facts FactSheet
	}{
		{name: "container", facts: FactSheet{WorkspacePath: "/workspace", ConfigPath: "/workspace/yolo-jail.toml"}},
		{name: "native-session", facts: FactSheet{WorkspacePath: "/Users/test/project", ConfigPath: "/Users/test/project/yolo-jail.toml", Briefing: BriefingInput{Workspace: "/Users/test/project", Home: "/Users/test/project/.yolo/setups/seatbelt-self/home", Mechanism: "macos-user"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files, err := RenderBuiltinSkills(tc.facts)
			if err != nil {
				t.Fatal(err)
			}
			body := string(files["configuring-the-jail/SKILL.md"])
			assertScopedPackAdvice(t, body)
			for _, want := range []string{"on the host", "whole-pack readiness", "substitute workspace\n`skill_imports`"} {
				if !strings.Contains(body, want) {
					t.Errorf("rendered configuring skill misses %q", want)
				}
			}
		})
	}
}

func TestWriteBuiltinSkillsWithoutFactsKeepsScopedPackAdvice(t *testing.T) {
	dst := t.TempDir()
	if _, err := writeBuiltinSkills(dst, nil, nil, false); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dst, "configuring-the-jail", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	assertScopedPackAdvice(t, string(body))
}
