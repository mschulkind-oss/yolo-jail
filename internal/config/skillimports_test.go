package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestSkillImportsWorkspaceConfig(t *testing.T) {
	_, _, workspace := workspaceFileHome(t, "skill-imports")
	body := "[skill_imports.acme]\n" +
		"git = 'https://github.com/acme/agent-skills'\n" +
		"rev = '3f9c2b7e1d4a5c6b8e9f0a1b2c3d4e5f60718293'\n" +
		"ref = 'main'\n" +
		"path = 'skills'\n" +
		"only = ['review', 'triage']\n"
	write(t, filepath.Join(workspace, paths.WorkspaceConfigTOMLName), body)

	cfg, err := LoadConfig(workspace, true, nil)
	if err != nil {
		t.Fatalf("LoadConfig valid workspace skill imports: %v", err)
	}
	if errs, _ := ValidateConfig(cfg, workspace, nil); len(errs) != 0 {
		t.Fatalf("ValidateConfig valid workspace skill imports: %v", errs)
	}

	// A pin is immutable; without rev, a valid ref selects the moving form.
	write(t, filepath.Join(workspace, paths.WorkspaceConfigTOMLName), "[skill_imports.acme]\n"+
		"git = 'https://github.com/acme/agent-skills'\nref = 'main'\n")
	cfg, err = LoadConfig(workspace, true, nil)
	if err != nil {
		t.Fatalf("LoadConfig valid moving-ref import: %v", err)
	}
	if errs, _ := ValidateConfig(cfg, workspace, nil); len(errs) != 0 {
		t.Fatalf("ValidateConfig valid moving-ref import: %v", errs)
	}
}

func TestSkillImportsValidation(t *testing.T) {
	_, _, workspace := workspaceFileHome(t, "skill-imports-invalid")
	wrongVeto, _ := ValidateConfig(decode(t, `{"workspace_skill_imports":"no"}`), workspace, nil)
	if !strings.Contains(strings.Join(wrongVeto, "\n"), "workspace_skill_imports: expected a boolean") {
		t.Fatalf("wrong veto type errors = %v", wrongVeto)
	}
	cases := []struct {
		name  string
		entry string
		want  string
	}{
		{"missing revision and ref", "git = 'https://github.com/acme/skills'", "rev or ref"},
		{"URL user info", "git = 'https://user:secret@example.test/acme/skills'\nref = 'main'", "credentials"},
		{"unsupported transport", "git = 'http://example.test/acme/skills'\nref = 'main'", "transport"},
		{"abbreviated revision", "git = 'https://example.test/acme/skills'\nrev = 'abcd1234'", "full commit"},
		{"invalid ref", "git = 'https://example.test/acme/skills'\nref = '../main'", "ref"},
		{"parent path", "git = 'https://example.test/acme/skills'\nref = 'main'\npath = '../skills'", "path"},
		{"absolute path", "git = 'https://example.test/acme/skills'\nref = 'main'\npath = '/skills'", "path"},
		{"git metadata path", "git = 'https://example.test/acme/skills'\nref = 'main'\npath = 'a/.git/b'", "path"},
		{"slash in name filter", "git = 'https://example.test/acme/skills'\nref = 'main'\nonly = ['nested/name']", "only"},
		{"duplicate name filter", "git = 'https://example.test/acme/skills'\nref = 'main'\nonly = ['review', 'review']", "only"},
		{"backslash in name filter", "git = 'https://example.test/acme/skills'\nref = 'main'\nonly = ['nested\\\\name']", "only"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			write(t, filepath.Join(workspace, paths.WorkspaceConfigTOMLName), "[skill_imports.acme]\n"+tc.entry+"\n")
			cfg, err := LoadConfig(workspace, true, nil)
			if err != nil {
				t.Fatalf("LoadConfig rejected syntax before semantic validation: %v", err)
			}
			errs, _ := ValidateConfig(cfg, workspace, nil)
			joined := strings.Join(errs, "\n")
			if !strings.Contains(joined, tc.want) {
				t.Fatalf("ValidateConfig errors = %v, want an error containing %q", errs, tc.want)
			}
		})
	}
}

func TestWorkspaceSkillImportsVetoScope(t *testing.T) {
	home, _, workspace := workspaceFileHome(t, "skill-imports-scope")
	workspaceConfig := filepath.Join(workspace, paths.WorkspaceConfigTOMLName)
	write(t, workspaceConfig, "workspace_skill_imports = false\n")
	if _, err := LoadConfig(workspace, true, nil); err == nil || !strings.Contains(err.Error(), "user-scope only") {
		t.Fatalf("workspace-scope veto error = %v, want user-scope refusal", err)
	}

	userConfig := filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName)
	write(t, userConfig, "workspace_skill_imports = false\n")
	write(t, workspaceConfig, "[skill_imports.acme]\ngit = 'https://example.test/acme/skills'\nref = 'main'\n")
	if _, err := LoadConfig(workspace, true, nil); err != nil {
		t.Fatalf("user veto with workspace imports: %v", err)
	}

	write(t, userConfig, "[skill_imports.acme]\ngit = 'https://example.test/acme/skills'\nref = 'main'\n")
	write(t, workspaceConfig, "")
	if _, err := LoadConfig(workspace, true, nil); err == nil || !strings.Contains(err.Error(), "workspace-only key") {
		t.Fatalf("user-scope skill_imports error = %v, want workspace-only refusal", err)
	}
}

func TestSkillImportsConsumerAndVetoDefault(t *testing.T) {
	cfg := decode(t, `{"skill_imports":{"zeta":{"git":"https://example.test/zeta/skills","ref":"main","only":[]},"alpha":{"git":"ssh://example.test/alpha/skills","rev":"3f9c2b7e1d4a5c6b8e9f0a1b2c3d4e5f60718293"}}}`)
	imports := SkillImports(cfg)
	if len(imports) != 2 || imports[0].Handle != "alpha" || imports[1].Handle != "zeta" {
		t.Fatalf("SkillImports = %#v, want sorted alpha/zeta entries", imports)
	}
	if imports[0].Path != "skills" || imports[0].Rev == "" || imports[1].Ref != "main" || !imports[1].OnlySet || len(imports[1].Only) != 0 {
		t.Errorf("SkillImports lost defaults, mode or explicit empty filter: %#v", imports)
	}
	if !WorkspaceSkillImportsEnabled(cfg) {
		t.Error("absent veto must default enabled")
	}
	veto := decode(t, `{"workspace_skill_imports":false}`)
	if WorkspaceSkillImportsEnabled(veto) {
		t.Error("explicit false veto must disable workspace imports")
	}
}

func TestSkillImportsConfigCheckIsOffline(t *testing.T) {
	_, _, workspace := workspaceFileHome(t, "skill-imports-offline")
	write(t, filepath.Join(workspace, paths.WorkspaceConfigTOMLName), "[skill_imports.acme]\n"+
		"git = 'ssh://example.test/acme/skills'\nref = 'main'\n")
	cfg, err := LoadConfig(workspace, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if errs, _ := ValidateConfig(cfg, workspace, nil); len(errs) != 0 {
		t.Fatalf("offline validation errors = %v", errs)
	}
	if _, err := os.Stat(filepath.Join(workspace, ".git")); err == nil {
		t.Fatal("config validation unexpectedly created workspace Git state")
	}
}
