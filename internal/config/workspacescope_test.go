package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

func TestProviderScopeValidatorUsesRuleIdentityForArbitraryMapKeys(t *testing.T) {
	providers := map[string]any{}
	for _, name := range []string{"project.provider", "bracket[name]", "punctuation-!"} {
		providers[name] = map[string]any{
			"platform":         nil,
			"api_key_env_name": nil,
			"endpoints":        map[string]any{"custom.proto[!]": map[string]any{"base_url": "https://example.invalid"}},
		}
	}
	providers["null.provider[entry]"] = nil
	providers["legal.provider[entry]"] = map[string]any{"models": []string{"model"}}
	body, err := json.Marshal(map[string]any{"providers": providers})
	if err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, WorkspaceConfigName), body, 0o644); err != nil {
		t.Fatal(err)
	}
	var errs []string
	validateProviderCredentialScope(ws, &errs)
	if len(errs) != 10 {
		t.Fatalf("provider scope errors = %d, want 10 (all endpoint, platform, api-key, and null refusals): %v", len(errs), errs)
	}
	joined := strings.Join(errs, "\n")
	for _, name := range []string{"project.provider", "bracket[name]", "punctuation-!", "null.provider[entry]"} {
		if !strings.Contains(joined, name) {
			t.Errorf("scope refusal omitted provider key %q: %v", name, errs)
		}
	}
	if strings.Contains(joined, "legal.provider[entry]") {
		t.Fatalf("legal provider key was refused: %v", errs)
	}
}

func TestNullProvidersScopeValidatorRefusesAtWorkspaceScope(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, WorkspaceConfigName), []byte(`{"providers":null}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var errs []string
	validateProviderCredentialScope(ws, &errs)
	if len(errs) != 1 || !strings.Contains(errs[0], "config.providers") {
		t.Fatalf("null providers errors = %v, want one existing credential-scope refusal", errs)
	}
}

func TestLoopholeSettingsScopeValidatorUsesRuleIdentityForDynamicKeys(t *testing.T) {
	name, setting := "dotted.pack[name]!", "setting.with[punctuation]"
	spec := jsonx.NewOrderedMap()
	settings := jsonx.NewOrderedMap()
	settings.Set(setting, true)
	spec.Set("settings", settings)
	info := &LoopholeInfo{Name: name, Settings: []loopholedecl.Setting{{Key: setting, Scope: loopholedecl.SettingScopeUser}}}
	got := loopholeSettingsScopeViolations(name, spec, "workspace config", info)
	if len(got) != 1 || !strings.Contains(got[0], "config.loopholes."+name+".settings."+setting) {
		t.Fatalf("loophole setting violations = %v, want one refusal naming dynamic keys", got)
	}
}

func TestWorkspaceScopeFactsDescribeCurrentSchemaAndConditionalDeciders(t *testing.T) {
	wantWhole := make(map[string]bool)
	for _, key := range SchemaKeys() {
		if key.Scope == SchemaScopeUserOnly && !strings.ContainsAny(key.Path, ".*") && WorkspaceUserScopeOnlyKey(key.Path) {
			wantWhole[key.Path] = true
		}
	}
	gotWhole := make(map[string]string)
	gotConditional := make(map[WorkspaceScopeRestriction]bool)
	for _, rule := range WorkspaceScopeRestrictions() {
		if rule.Path == "" {
			t.Fatal("scope fact has an empty path")
		}
		if strings.HasPrefix(rule.Path, "config.") {
			key := strings.TrimPrefix(rule.Path, "config.")
			if wantWhole[key] {
				gotWhole[key] = rule.When
			}
		}
		gotConditional[rule] = true
	}
	if len(gotWhole) != len(wantWhole) {
		t.Fatalf("whole-key facts = %v, want current schema/predicate intersection %v", gotWhole, wantWhole)
	}
	for key := range wantWhole {
		if _, ok := gotWhole[key]; !ok {
			t.Errorf("whole-key fact config.%s is missing", key)
		}
	}
	wantNonNull := map[string]bool{
		"agent_updates": true, "host_apply_on_launch": true, "host_floor": true,
		"host_management": true, "host_path": true, "host_wrappers": true,
		"local_clones": true, "macos_user_mode": true, "macos_user_shared_tmp": true,
		"perf_logging": true, "promotion_target": true, "share_host_fonts": true,
		"shipped_skill_prefix": true, "update_check": true,
	}
	for key := range wantWhole {
		wantWhen := ""
		if wantNonNull[key] {
			wantWhen = "value is non-null"
		}
		if gotWhole[key] != wantWhen {
			t.Errorf("whole-key fact config.%s When = %q, want %q", key, gotWhole[key], wantWhen)
		}
	}
	for _, retiredOrUnsupported := range []string{"config.provisioners", "config.use_profiles"} {
		if _, exists := gotWhole[strings.TrimPrefix(retiredOrUnsupported, "config.")]; exists {
			t.Errorf("unsupported whole-key fact %q was retained", retiredOrUnsupported)
		}
	}

	for _, rule := range []WorkspaceScopeRestriction{
		{Path: "config.brokered.*.sets"},
		{Path: "config.brokered.*.grant_kinds"},
		{Path: "config.providers", When: "providers value is null"},
		{Path: "config.providers.*", When: "provider value is null"},
		{Path: "config.env_sources[*]", When: "file entry resolves outside the workspace"},
		{Path: "config.providers.*.endpoints"},
		{Path: "config.providers.*.endpoints.*.base_url"},
		{Path: "config.providers.*.platform"},
		{Path: "config.providers.*.api_key_env_name"},
		{Path: "config.mounts[*]", When: "mount is read-write"},
		{Path: "config.host_files[*]", When: "entry names a host source (including string shorthand)"},
		{Path: "config.loopholes.*", When: "entry declares a command (install)"},
		{Path: "config.loopholes.*.doctor_cmd", When: "entry is not backed by a manifest"},
		{Path: "config.loopholes.*.env"},
		{Path: "config.loopholes.*.settings.*", When: "manifest declares the setting user-scope"},
	} {
		if !gotConditional[rule] {
			t.Errorf("scope facts omit current conditional decider %q when %q", rule.Path, rule.When)
		}
	}
	for _, mixed := range []string{"providers", "mounts", "host_files", "loopholes"} {
		if _, whole := gotWhole[mixed]; whole || WorkspaceUserScopeOnlyKey(mixed) {
			t.Errorf("mixed key %q was reported as wholly user-scope-only", mixed)
		}
	}
}

func TestWorkspaceScopeFactsMatchPublicValidationAndCannotControlIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	validate := func(body string) []string {
		t.Helper()
		ws := t.TempDir()
		if err := os.WriteFile(filepath.Join(ws, WorkspaceConfigName), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		errs, _ := ValidateConfig(decode(t, body), ws, nil)
		return errs
	}

	whole := validate(`{"packs":null}`)
	if !strings.Contains(strings.Join(whole, "\n"), "config.packs: user-scope only") {
		t.Fatalf("public validation did not refuse a null whole-key workspace value: %v", whole)
	}
	for _, tc := range []struct {
		name, body string
		wants      []string
	}{
		{name: "non-null-only whole key accepts null", body: `{"host_path":null}`},
		{name: "top-level provider null", body: `{"providers":null}`, wants: []string{"config.providers: user-scope only"}},
		{name: "provider entry null", body: `{"providers":{"odd.provider[entry]":null}}`, wants: []string{"config.providers.odd.provider[entry]: user-scope only"}},
		{name: "routing field nulls refuse", body: `{"providers":{"p":{"platform":null,"api_key_env_name":null,"endpoints":null}}}`, wants: []string{
			"config.providers.p.platform: user-scope only",
			"config.providers.p.api_key_env_name: user-scope only",
			"config.providers.p.endpoints: user-scope only",
		}},
		{name: "provider region null is allowed", body: `{"providers":{"p":{"region":null}}}`},
		{name: "mixed nulls are accepted", body: `{"mounts":null,"host_files":null,"loopholes":null,"brokered":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := validate(tc.body)
			joined := strings.Join(errs, "\n")
			for _, want := range tc.wants {
				if !strings.Contains(joined, want) {
					t.Errorf("public validation errors = %v, want refusal containing %q", errs, want)
				}
			}
			if len(tc.wants) == 0 && strings.Contains(joined, "user-scope only") {
				t.Fatalf("null-accepted workspace value received a scope refusal: %v", errs)
			}
		})
	}

	for _, tc := range []struct {
		name, body string
	}{
		{name: "source-less host file", body: `{"host_files":[{"path":"~/.config/tool/conf.json","content":"{}"}]}`},
		{name: "provider entry", body: `{"providers":{"legal.provider[entry]":{}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := validate(tc.body)
			if strings.Contains(strings.Join(errs, "\n"), "user-scope only") {
				t.Fatalf("allowed mixed workspace value received a scope refusal: %v", errs)
			}
		})
	}

	before := validate(`{"packs":null}`)
	rules := WorkspaceScopeRestrictions()
	if len(rules) == 0 {
		t.Fatal("scope facts unexpectedly empty")
	}
	for i := range rules {
		rules[i].Path = "config.packs"
		rules[i].When = "allow everything"
	}
	rules[0] = WorkspaceScopeRestriction{}
	fresh := WorkspaceScopeRestrictions()
	if fresh[0].Path == "corrupted" || fresh[0].Path == "" {
		t.Fatalf("mutating returned metadata changed a later copy: first rule = %+v", fresh[0])
	}
	after := validate(`{"packs":null}`)
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("mutating returned descriptive facts changed public enforcement:\nbefore %v\nafter %v", before, after)
	}
}

func TestWorkspaceScopeFactsLeaveMixedKeysUnclassified(t *testing.T) {
	for _, key := range []string{"providers", "mounts", "host_files", "loopholes"} {
		if WorkspaceUserScopeOnlyKey(key) {
			t.Errorf("mixed/value-specific key %q was classified as wholly user-scope-only", key)
		}
	}
}
