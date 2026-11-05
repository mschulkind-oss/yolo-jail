package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func resourceGroupsConfig(t *testing.T, raw string) *jsonx.OrderedMap {
	t.Helper()
	value, err := jsonx.Decode([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return value.(*jsonx.OrderedMap)
}

func TestResourceGroupsOnlyUserScopeAndResolution(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	workspace := filepath.Join(project, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := resourceGroupsConfig(t, `{"resource_groups":{"default":"default.slice","maintenance":"maintenance.slice","agent_percent":5,"projects":{"`+root+`":"root.slice","`+project+`":"project.slice"},"workspaces":{"`+workspace+`":"exact.slice"}}}`)
	selected, err := SelectResourceGroup(cfg, workspace)
	if err != nil || selected.Unit != "exact.slice" || selected.Source != "workspace" {
		t.Fatalf("exact workspace selection = %#v, %v", selected, err)
	}
	workspaceAlias := filepath.Join(root, "workspace-alias")
	if err := os.Symlink(workspace, workspaceAlias); err == nil {
		selected, err = SelectResourceGroup(cfg, workspaceAlias)
		if err != nil || selected.Unit != "exact.slice" || selected.Source != "workspace" {
			t.Fatalf("canonicalized workspace alias selection = %#v, %v", selected, err)
		}
	}
	deleteExact := resourceGroupsConfig(t, `{"resource_groups":{"default":"default.slice","projects":{"`+root+`":"root.slice","`+project+`":"project.slice"}}}`)
	selected, err = SelectResourceGroup(deleteExact, workspace)
	if err != nil || selected.Unit != "project.slice" || selected.Source != "project "+project {
		t.Fatalf("nearest project selection = %#v, %v", selected, err)
	}
	fallback := resourceGroupsConfig(t, `{"resource_groups":{"default":"default.slice"}}`)
	selected, err = SelectResourceGroup(fallback, workspace)
	if err != nil || selected.Unit != "default.slice" || selected.Source != "default" {
		t.Fatalf("default selection = %#v, %v", selected, err)
	}
	off := resourceGroupsConfig(t, `{}`)
	selected, err = SelectResourceGroup(off, workspace)
	if err != nil || selected.Unit != "" {
		t.Fatalf("empty config selection = %#v, %v", selected, err)
	}

	workspaceTable := jsonx.NewOrderedMap()
	workspaceTable.Set("/tmp", "work.slice")
	owner := jsonx.NewOrderedMap()
	owner.Set("workspaces", workspaceTable)
	badScope := jsonx.NewOrderedMap()
	badScope.Set("resource_groups", owner)
	if violation := firstReadableTOMLViolation(badScope, SchemaScopeWorkspaceOnly); violation == nil {
		t.Fatal("project-scope resource_groups was not refused")
	}
}

func TestResourceGroupsRejectUnknownJSONCFieldsAndValidateWorkspaceMappingKeys(t *testing.T) {
	workspace := t.TempDir()
	for _, raw := range []string{
		`{"resource_groups":{"defualt":"work.slice"}}`,
		`{"resource_groups":{"maintenancee":"maint.slice"}}`,
		`{"resource_groups":{"agent_percnet":5}}`,
	} {
		cfg := resourceGroupsConfig(t, raw)
		problems, _ := ValidateConfig(cfg, workspace, nil)
		if !strings.Contains(strings.Join(problems, "\n"), "resource_groups.") || !strings.Contains(strings.Join(problems, "\n"), "unknown key") {
			t.Errorf("ValidateConfig(%s) = %v, want unknown resource_groups field", raw, problems)
		}
	}

	pathTable := jsonx.NewOrderedMap()
	pathTable.Set(workspace, "work.slice")
	group := jsonx.NewOrderedMap()
	group.Set("workspaces", pathTable)
	cfg := jsonx.NewOrderedMap()
	cfg.Set("resource_groups", group)
	if problems := ValidateResourceGroups(cfg); len(problems) != 0 {
		t.Fatalf("valid arbitrary workspace mapping key refused: %v", problems)
	}
}

func TestResourceGroupsJSONCDefaultTypoFailsFullConfigValidation(t *testing.T) {
	workspace := t.TempDir()
	cfg := resourceGroupsConfig(t, `{"resource_groups":{"defualt":"work.slice"}}`)
	problems, _ := ValidateConfig(cfg, workspace, nil)
	if got := strings.Join(problems, "\n"); !strings.Contains(got, "resource_groups.defualt: unknown key") {
		t.Fatalf("full JSONC validation = %v, want resource_groups typo refusal", problems)
	}
}

func TestResourceGroupsValidatePercentAndCanonicalPath(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	for _, test := range []struct {
		value any
		valid bool
	}{{1, true}, {99, true}, {0, false}, {100, false}, {true, false}, {5.5, false}} {
		cfg := jsonx.NewOrderedMap()
		table := jsonx.NewOrderedMap()
		table.Set("agent_percent", test.value)
		cfg.Set("resource_groups", table)
		got := ValidateResourceGroups(cfg)
		if (len(got) == 0) != test.valid {
			t.Errorf("agent_percent=%v errors=%v, valid=%v", test.value, got, test.valid)
		}
	}
	cfg := resourceGroupsConfig(t, `{"resource_groups":{"projects":{"`+link+`":"alias.slice"}}}`)
	if got := ValidateResourceGroups(cfg); len(got) == 0 || !strings.Contains(strings.Join(got, " "), "canonical path") {
		t.Fatalf("symlink alias errors = %v, want canonical-path refusal", got)
	}
}
