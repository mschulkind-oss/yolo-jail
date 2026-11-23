package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `yolo internal tool-skills <workspace> [lock]` is the verb the provisioning step calls. Deleting
// its case from runInternal makes the verb an unknown command and fails this test.
func TestInternalToolSkillsRefreshesTheSnapshotFromTheEnvironment(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	ws := filepath.Join(root, "ws")
	skillDir := filepath.Join(root, "mise", "installs", "tool-a", "1", "s")
	for _, d := range []string{ws, skillDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	listing := `[{"name":"s","tool":"tool-a","version":"1","path":"` + skillDir + `"}]`
	if err := os.WriteFile(filepath.Join(bin, "mise"), []byte("#!/bin/sh\ncat <<'EOF'\n"+listing+"\nEOF\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("MISE_DATA_DIR", filepath.Join(root, "mise"))
	t.Setenv("YOLO_MISE_SKILL_TOOLS", `["tool-a"]`)
	t.Setenv("YOLO_TOOL_SKILLS_TOKEN", "tok")
	lock := filepath.Join(root, "main", "tool-skills.lock")
	if rc := runInternal([]string{"tool-skills", ws, lock}); rc != 0 {
		t.Fatalf("rc %d", rc)
	}
	cur, err := os.ReadFile(filepath.Join(ws, ".yolo", "tool-skills", "current"))
	if err != nil {
		t.Fatal(err)
	}
	idx, err := os.ReadFile(filepath.Join(ws, ".yolo", "tool-skills", strings.TrimSpace(string(cur)), "index.json"))
	if err != nil || !strings.Contains(string(idx), `"tok"`) {
		t.Fatalf("%s %v", idx, err)
	}
	if _, err := os.Stat(lock); err != nil {
		t.Fatalf("the named lock was not used: %v", err)
	}
}
