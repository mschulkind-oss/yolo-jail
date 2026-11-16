package jailcontent

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
)

func TestInspectImportedSkillShadowsUsesPerDestinationOrderReadOnly(t *testing.T) {
	root, packDir, localDir := t.TempDir(), t.TempDir(), t.TempDir()
	for path, name := range map[string]string{
		filepath.Join(root, ".agents", "skills", "workspace-collision", "SKILL.md"): "workspace",
		filepath.Join(packDir, "pack-collision", "SKILL.md"):                        "pack",
		filepath.Join(packDir, "configuring-the-jail", "SKILL.md"):                  "pack built-in override",
		filepath.Join(localDir, "local-collision", "SKILL.md"):                      "local",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	before := map[string]map[string]string{
		"workspace": inventoryTree(t, root),
		"pack":      inventoryTree(t, packDir),
		"local":     inventoryTree(t, localDir),
	}
	sources := []PackSkillSource{
		{Pack: "copilot-pack", Dir: packDir, Agents: []string{"copilot"}, Tier: hostskills.TierFlat},
		{Pack: "local", Dir: localDir, Tier: hostskills.TierFlat},
	}
	targets := []SkillTarget{
		{Dest: ".copilot/skills", Agent: "copilot", ProjectDirs: []string{".agents/skills"}},
		{Dest: ".codex/skills", Agent: "codex", ProjectDirs: []string{".agents/skills"}},
	}
	imported := []string{"pack-collision", "local-collision", "configuring-the-jail", "diagnosing-the-jail", "workspace-collision", "yolo-lsp", "unique"}
	got, err := InspectImportedSkillShadows(&WorkspaceSkills{Root: root, Dirs: []string{".agents/skills"}}, sources, targets, imported)
	if err != nil {
		t.Fatal(err)
	}
	byDestination := map[string]map[string]string{}
	for _, shadow := range got {
		if byDestination[shadow.Destination] == nil {
			byDestination[shadow.Destination] = map[string]string{}
		}
		byDestination[shadow.Destination][shadow.Name] = shadow.By
	}
	want := map[string]map[string]string{
		"~/.copilot/skills": {
			"pack-collision":       "pack copilot-pack",
			"local-collision":      "pack local",
			"configuring-the-jail": "pack copilot-pack",
			"diagnosing-the-jail":  "yolo's built-in skills",
			"workspace-collision":  "workspace skill source .agents/skills",
			"yolo-lsp":             "yolo's LSP plugin",
		},
		"~/.codex/skills": {
			"local-collision":      "pack local",
			"configuring-the-jail": "yolo's built-in skills",
			"diagnosing-the-jail":  "yolo's built-in skills",
			"workspace-collision":  "workspace skill source .agents/skills",
			"yolo-lsp":             "yolo's LSP plugin",
		},
	}
	if !reflect.DeepEqual(byDestination, want) {
		t.Fatalf("shadow inventory = %#v, want %#v", byDestination, want)
	}
	if got := inventoryTree(t, root); !reflect.DeepEqual(got, before["workspace"]) {
		t.Fatalf("workspace changed during read-only inventory: %v", got)
	}
	if got := inventoryTree(t, packDir); !reflect.DeepEqual(got, before["pack"]) {
		t.Fatalf("pack tree changed during read-only inventory: %v", got)
	}
	if got := inventoryTree(t, localDir); !reflect.DeepEqual(got, before["local"]) {
		t.Fatalf("local pack changed during read-only inventory: %v", got)
	}
}

func inventoryTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			out[rel] = "directory"
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
