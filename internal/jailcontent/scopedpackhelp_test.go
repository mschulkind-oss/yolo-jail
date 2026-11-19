package jailcontent

import (
	"strings"
	"testing"
)

func TestRenderBuiltinHostSkillKeepsScopedPackEditingAdvice(t *testing.T) {
	files, err := RenderBuiltinSkills(FactSheet{Host: true, HostWorkspace: "/owned", UserConfigPath: "/owned/config.toml"})
	if err != nil {
		t.Fatal(err)
	}
	body := string(files["configuring-the-jail/SKILL.md"])
	for _, want := range []string{"--skills-dir", "--only skill-a --dry-run", "yolo pack explain", "user TOML", "legacy file filters"} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered host configuring skill misses %q", want)
		}
	}
}

func TestRenderBuiltinSkillsKeepsScopedPackAuthorityAndInspectionAdvice(t *testing.T) {
	files, err := RenderBuiltinSkills(FactSheet{WorkspacePath: "/workspace", ConfigPath: "/workspace/yolo-jail.toml"})
	if err != nil {
		t.Fatal(err)
	}
	body := string(files["configuring-the-jail/SKILL.md"])
	for _, want := range []string{"Direct scoped pack selection is user-only", "--skills-dir", "--only skill-a --dry-run", "on the host", "whole-pack readiness", "substitute workspace\n`skill_imports`"} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered configuring skill misses %q", want)
		}
	}
}
