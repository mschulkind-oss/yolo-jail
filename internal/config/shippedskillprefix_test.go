package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShippedSkillPrefixReadsOnlyUserScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")

	userCfg := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(userCfg), 0o755); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	t.Chdir(ws)
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"shipped_skill_prefix":"ws"}`)
	if got := ShippedSkillPrefix(); got != "" {
		t.Fatalf("ShippedSkillPrefix() with only a workspace value = %q, want no prefix", got)
	}
	write(t, userCfg, `{"shipped_skill_prefix":"yolo"}`)
	if got := ShippedSkillPrefix(); got != "yolo" {
		t.Errorf("ShippedSkillPrefix() = %q, want yolo", got)
	}
	write(t, userCfg, `{"shipped_skill_prefix":"Not A Word"}`)
	if got := ShippedSkillPrefix(); got != "" {
		t.Errorf("ShippedSkillPrefix() over an invalid value = %q, want no prefix", got)
	}
}

func TestValidateShippedSkillPrefix(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("YOLO_VERSION", "")
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"shipped_skill_prefix":"ws"}`)

	var errs []string
	validateShippedSkillPrefix(decode(t, `{"shipped_skill_prefix":"ws"}`), ws, &errs)
	if joined := strings.Join(errs, "\n"); !strings.Contains(joined, "user-scope only") ||
		!strings.Contains(joined, "yolo config set shipped_skill_prefix") {
		t.Fatalf("workspace shipped_skill_prefix errors = %v, want the user-scope refusal naming the verb", errs)
	}
	for _, body := range []string{`{"shipped_skill_prefix":"yolo"}`, `{"shipped_skill_prefix":null}`, `{}`} {
		errs = nil
		validateShippedSkillPrefix(decode(t, body), t.TempDir(), &errs)
		if len(errs) != 0 {
			t.Errorf("validateShippedSkillPrefix(%s) errors = %v", body, errs)
		}
	}
	for _, body := range []string{`{"shipped_skill_prefix":"Yolo"}`, `{"shipped_skill_prefix":"a--b"}`,
		`{"shipped_skill_prefix":""}`, `{"shipped_skill_prefix":true}`} {
		errs = nil
		validateShippedSkillPrefix(decode(t, body), t.TempDir(), &errs)
		if len(errs) == 0 {
			t.Errorf("validateShippedSkillPrefix(%s) accepted an invalid prefix", body)
		}
	}
}
