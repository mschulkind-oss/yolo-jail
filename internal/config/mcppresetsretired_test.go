package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mcppresetsretired_test.go pins the RETIREMENT of the top-level `mcp_presets` key
// (2026-10-23, docs/design/mcp-presets-removal.md §13 step 3, ruled at
// docs/reference/pack-system.md#oq-pk1). The browser is the `chrome-devtools` pack, selected in
// the user config; `sequential-thinking` is deleted, and its replacement is one `mcp_servers`
// entry. The key is a refusal naming both, as `journal` and `macos_log` are.

// wantMCPPresetsRefusal is what every spelling of the refusal must carry: the removal, the
// pack and the file it is selected in, the sequential-thinking entry, and the check to run.
var wantMCPPresetsRefusal = []string{
	"REMOVED", "`yolo pack add chrome-devtools`",
	"`yolo config set mcp_servers.sequential-thinking.command '[\"npx\", \"-y\", \"@modelcontextprotocol/server-sequential-thinking\"]'`",
	"Delete the key", "yolo check",
}

func assertMCPPresetsRefusal(t *testing.T, label string, errs, warns []string) {
	t.Helper()
	hits := containing(errs, "config.mcp_presets")
	if len(hits) != 1 {
		t.Errorf("%s: errors = %v, want ONE refusal (and no generic unknown-key error beside it)",
			label, errs)
		return
	}
	for _, want := range wantMCPPresetsRefusal {
		if !strings.Contains(hits[0], want) {
			t.Errorf("%s: refusal %q does not name %q", label, hits[0], want)
		}
	}
	// Never a whole `packs` value: copied literally it would replace the user's agent list.
	if strings.Contains(hits[0], `"packs": [`) {
		t.Errorf("%s: refusal %q spells a whole packs list", label, hits[0])
	}
	if len(containing(warns, "mcp_presets")) != 0 {
		t.Errorf("%s: the refusal is also a warning: %v", label, warns)
	}
}

// Every value the key ever took is refused on the host, the empty list and null included
// (§9.1: an empty array is still the key being present).
func TestRetiredMCPPresetsKeyIsRefusedAndNamesItsReplacement(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	useProfileKeysHome(t)
	for _, body := range []string{
		`["chrome-devtools"]`, `["sequential-thinking"]`, `["chrome-devtools", "sequential-thinking"]`,
		`[]`, `null`, `"chrome-devtools"`,
	} {
		errs, warns := ValidateConfig(decode(t, `{"mcp_presets": `+body+`}`), t.TempDir(), nil)
		assertMCPPresetsRefusal(t, "mcp_presets "+body, errs, warns)
	}
	if errs, _ := ValidateConfig(decode(t, `{}`), t.TempDir(), nil); len(containing(errs, "mcp_presets")) != 0 {
		t.Errorf("a config without the key is refused: %v", errs)
	}
}

// The key is refused from EITHER scope: written in the user config or in the workspace
// yolo-jail.jsonc, it reaches the merged config both callers validate. Driven through
// LoadConfig and ValidateConfig — the pair the launch preflight and `yolo check` run — so the
// test fails if the refusal is unwired from validation.
func TestRetiredMCPPresetsKeyIsRefusedAtBothScopes(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	for _, scope := range []string{"user", "workspace"} {
		t.Run(scope, func(t *testing.T) {
			home := useProfileKeysHome(t)
			workspace := t.TempDir()
			path := filepath.Join(workspace, WorkspaceConfigName)
			if scope == "user" {
				dir := filepath.Join(home, ".config", "yolo-jail")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(dir, "config.jsonc")
			}
			if err := os.WriteFile(path, []byte(`{"mcp_presets": ["chrome-devtools"]}`), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadConfig(workspace, false, nil)
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			errs, warns := ValidateConfig(cfg, workspace, nil)
			assertMCPPresetsRefusal(t, scope+" scope", errs, warns)
		})
	}
}

// In a jail the config is the host-generated snapshot, so the refusal is a warning there.
func TestRetiredMCPPresetsKeyOnlyWarnsInsideAJail(t *testing.T) {
	t.Setenv("YOLO_VERSION", "0.12.2")
	useProfileKeysHome(t)
	errs, warns := ValidateConfig(decode(t, `{"mcp_presets": ["chrome-devtools"]}`), t.TempDir(), nil)
	if len(containing(errs, "mcp_presets")) != 0 || len(containing(warns, "config.mcp_presets")) != 1 {
		t.Errorf("in-jail: errs %v warns %v, want one warning", errs, warns)
	}
}

// The key is listed as retired, so config-ref's coverage check does not demand it be documented.
func TestMCPPresetsIsARetiredKey(t *testing.T) {
	for _, k := range TopLevelConfigKeys() {
		if k == "mcp_presets" {
			t.Error("mcp_presets is still a live top-level key")
		}
	}
	found := false
	for _, k := range RetiredConfigKeys() {
		found = found || k == "mcp_presets"
	}
	if !found {
		t.Error("mcp_presets is not in RetiredConfigKeys")
	}
}
