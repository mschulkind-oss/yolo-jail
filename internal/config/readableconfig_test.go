package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

func TestLoadReadableWorkspaceConfigReadsReadableInstall(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, paths.WorkspaceConfigTOMLName), "[install]\nnix = ['just']\nmise = { python = '3.12' }\n")

	got, err := LoadReadableWorkspaceConfig(dir, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, exists := got.Get("install")
	install, valid := raw.(*jsonx.OrderedMap)
	if !exists || !valid {
		t.Fatalf("install missing: %#v", got)
	}
	nix, _ := install.Get("nix")
	if !reflect.DeepEqual(nix, []any{"just"}) {
		t.Fatalf("install.nix = %#v, want [\"just\"]", nix)
	}
	mise, _ := install.Get("mise")
	python, _ := mise.(*jsonx.OrderedMap).Get("python")
	if python != "3.12" {
		t.Fatalf("install.mise.python = %#v, want string \"3.12\"", python)
	}
}

func TestLoadConfigRejectsWorkspaceResourceGroupsBeforeMerge(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, tc := range []struct {
		name      string
		files     map[string]string
		wantFile  string
		wantScope string
	}{
		{name: "project JSONC", files: map[string]string{paths.WorkspaceConfigLegacyJSONCName: `{"resource_groups":{"default":"work.slice"}}`}, wantFile: paths.WorkspaceConfigLegacyJSONCName, wantScope: "this project file"},
		{name: "local JSONC", files: map[string]string{paths.WorkspaceLocalConfigLegacyJSONCName: `{"resource_groups":{"default":"work.slice"}}`}, wantFile: paths.WorkspaceLocalConfigLegacyJSONCName, wantScope: "this local file"},
		{name: "included JSONC", files: map[string]string{
			paths.WorkspaceConfigLegacyJSONCName: `{"include_if_found":["parts/owner.jsonc"]}`,
			"parts/owner.jsonc":                  `{"resource_groups":{"default":"work.slice"}}`,
		}, wantScope: "this included file"},
		{name: "project TOML", files: map[string]string{paths.WorkspaceConfigTOMLName: "[resource_groups]\ndefault = 'work.slice'\n"}, wantFile: paths.WorkspaceConfigTOMLName, wantScope: "this project file"},
		{name: "local TOML", files: map[string]string{paths.WorkspaceLocalConfigTOMLName: "[resource_groups]\ndefault = 'work.slice'\n"}, wantFile: paths.WorkspaceLocalConfigTOMLName, wantScope: "this local file"},
		{name: "included TOML", files: map[string]string{
			paths.WorkspaceConfigTOMLName: "include_if_found = ['parts/owner.toml']\n",
			"parts/owner.toml":            "[resource_groups]\ndefault = 'work.slice'\n",
		}, wantFile: filepath.Join("parts", "owner.toml"), wantScope: "this included file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			for name, body := range tc.files {
				write(t, filepath.Join(workspace, name), body)
			}
			_, _, err := LoadConfigWithSources(workspace, true, func(string) {})
			if err == nil || !strings.Contains(err.Error(), "config.resource_groups") || !strings.Contains(err.Error(), "user-scope only") {
				t.Fatalf("LoadConfigWithSources error = %v, want file-backed owner-scope refusal", err)
			}
			if tc.wantScope != "" {
				wantFile := tc.wantFile
				if strings.Contains(tc.name, "included") {
					name := "owner.jsonc"
					if strings.Contains(tc.name, "TOML") {
						name = "owner.toml"
					}
					wantFile = filepath.Join(workspace, "parts", name)
				}
				for _, want := range []string{wantFile, "first remove the `resource_groups` table from " + tc.wantScope, "then retry the command", "yolo config set resource_groups.default '\"example.slice\"' --user", "example only; replace it with your independently chosen unit; do not copy a project value", "After the workspace config graph is repaired", "on the host", "independently choose an owner-level mapping"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("LoadConfigWithSources error %q does not contain corrective advice %q", err, want)
					}
				}
				if strings.Contains(err.Error(), "yolo config unset resource_groups") {
					t.Errorf("LoadConfigWithSources error contains an unsupported repair command: %q", err)
				}
				removeAt := strings.Index(err.Error(), "first remove the `resource_groups` table")
				retryAt := strings.Index(err.Error(), "then retry the command")
				ownerAt := strings.Index(err.Error(), "yolo config set resource_groups.default")
				if removeAt < 0 || retryAt <= removeAt || ownerAt <= retryAt {
					t.Errorf("owner suggestion must follow file removal and retry guidance: %q", err)
				}
			}
		})
	}
}

func TestLoadConfigKeepsOwnerJSONCSupportAndRejectsIntegralFloatPercent(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	userPath := filepath.Join(home, ".config", "yolo-jail", paths.UserConfigLegacyJSONCName)
	write(t, userPath, `{"resource_groups":{"default":"owner.slice","agent_percent":5}}`)
	cfg, _, err := LoadConfigWithSources(workspace, true, func(string) {})
	if err != nil || len(ValidateResourceGroups(cfg)) != 0 {
		t.Fatalf("owner JSONC config load/validation errors = %v, %v", err, ValidateResourceGroups(cfg))
	}
	selected, selectErr := SelectResourceGroup(cfg, workspace)
	if selectErr != nil || selected.Unit != "owner.slice" {
		t.Fatalf("owner JSONC selection = %#v, %v", selected, selectErr)
	}

	for _, tc := range []struct {
		name  string
		file  string
		body  string
		valid bool
	}{
		{name: "JSONC integer 5", file: paths.UserConfigLegacyJSONCName, body: `{"resource_groups":{"agent_percent":5}}`, valid: true},
		{name: "TOML integer 5", file: paths.UserConfigTOMLName, body: "[resource_groups]\nagent_percent = 5\n", valid: true},
		{name: "JSONC 5.0", file: paths.UserConfigLegacyJSONCName, body: `{"resource_groups":{"agent_percent":5.0}}`},
		{name: "TOML 5.0", file: paths.UserConfigTOMLName, body: "[resource_groups]\nagent_percent = 5.0\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			userPath := filepath.Join(home, ".config", "yolo-jail", tc.file)
			write(t, userPath, tc.body)
			cfg, _, err := LoadConfigWithSources(workspace, true, func(string) {})
			if err != nil {
				t.Fatal(err)
			}
			errs := ValidateResourceGroups(cfg)
			if tc.valid && len(errs) != 0 {
				t.Fatalf("integer agent_percent validation errors = %v", errs)
			}
			if !tc.valid && (len(errs) == 0 || !strings.Contains(strings.Join(errs, " "), "integer from 1 through 99")) {
				t.Fatalf("agent_percent validation errors = %v, want integer-only refusal", errs)
			}
		})
	}
}

func TestLoadReadableWorkspaceConfigFormatAndIncludes(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, paths.WorkspaceConfigTOMLName), "format = 2\ninclude_if_found = ['parts/base.toml']\n\n[machine.resources]\ncpus = 8\n")
	write(t, filepath.Join(dir, "parts", "base.toml"), "include_if_found = ['nested.toml']\n[install]\nnix = ['just']\n")
	write(t, filepath.Join(dir, "parts", "nested.toml"), "[machine.resources]\nmemory = '16g'\n")
	write(t, filepath.Join(dir, paths.WorkspaceLocalConfigTOMLName), "[machine.resources]\ncpus = 10\n")

	got, src, err := LoadReadableWorkspaceConfigWithSources(dir, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := got.Get("format"); exists {
		t.Fatal("format is reader metadata and must not appear in effective config")
	}
	install, _ := got.Get("install")
	nix, _ := install.(*jsonx.OrderedMap).Get("nix")
	if !reflect.DeepEqual(nix, []any{"just"}) {
		t.Fatalf("included install.nix = %#v", nix)
	}
	resources, _ := got.Get("machine")
	resources, _ = resources.(*jsonx.OrderedMap).Get("resources")
	cpus, _ := resources.(*jsonx.OrderedMap).Get("cpus")
	memory, _ := resources.(*jsonx.OrderedMap).Get("memory")
	cpusN, cpusIsInt := jsonx.AsInt(cpus)
	if !cpusIsInt || cpusN != 10 || memory != "16g" {
		t.Fatalf("machine.resources = %#v", resources)
	}
	if locs := src.Locations("config.install.nix[0]"); len(locs) != 1 || !strings.Contains(locs[0], "parts/base.toml:") {
		t.Fatalf("included TOML source location = %q", locs)
	}
}

func TestLoadReadableWorkspaceConfigIncludesOptionalAndCycle(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, paths.WorkspaceConfigTOMLName), "include_if_found = ['parts/one.toml', 'missing.toml']\n[install]\nnix = ['base']\n")
	write(t, filepath.Join(dir, "parts", "one.toml"), "include_if_found = ['two.toml']\n[install]\nnix = ['one']\n")
	write(t, filepath.Join(dir, "parts", "two.toml"), "include_if_found = ['one.toml']\n[install]\nnix = ['two']\n")

	got, err := LoadReadableWorkspaceConfig(dir, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	install, _ := got.Get("install")
	nix, _ := install.(*jsonx.OrderedMap).Get("nix")
	if !reflect.DeepEqual(nix, []any{"base", "one", "two"}) {
		t.Fatalf("cycle/optional include result = %#v", nix)
	}
}

func TestLoadReadableUserScopeConfigKeepsBaseWhenConfigDReadFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	userDir := filepath.Join(home, ".config", "yolo-jail")
	basePath := filepath.Join(userDir, paths.UserConfigTOMLName)
	write(t, basePath, "[install]\nnix = ['base']\n[env.set]\nRECOVERY_TEST = 'literal'\n")
	fragmentDir := filepath.Join(userDir, "config.d")
	write(t, fragmentDir, "not a directory")

	var warnings []string
	warn := func(msg string) { warnings = append(warnings, msg) }
	got, err := LoadReadableUserScopeConfig(false, warn)
	if err != nil {
		t.Fatal(err)
	}
	if got, exists := got.Get("install"); !exists {
		t.Fatalf("non-strict reader discarded valid base config: %#v", got)
	} else {
		install, ok := got.(*jsonx.OrderedMap)
		if !ok || install == nil {
			t.Fatalf("non-strict reader returned invalid map: %#v", got)
		}
	}
	install, _ := got.Get("install")
	actual, _ := install.(*jsonx.OrderedMap).Get("nix")
	if !reflect.DeepEqual(actual, []any{"base"}) {
		t.Errorf("install.nix = %#v, want preserved base value", actual)
	}
	env, _ := got.Get("env")
	if env == nil {
		t.Errorf("non-strict reader discarded valid env config: %#v", got)
	} else if set, _ := env.(*jsonx.OrderedMap).Get("set"); set == nil {
		t.Errorf("non-strict reader discarded valid env.set config: %#v", got)
	} else if value, _ := set.(*jsonx.OrderedMap).Get("RECOVERY_TEST"); value != "literal" {
		t.Errorf("env.set.RECOVERY_TEST = %#v, want preserved literal", value)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "Failed to parse") || !strings.Contains(warnings[0], fragmentDir) {
		t.Errorf("warnings = %q, want one existing parse-failure warning naming config.d", warnings)
	}

	got, sources, err := LoadReadableUserScopeConfigWithSources(false, warn)
	if err != nil {
		t.Fatal(err)
	}
	install, _ = got.Get("install")
	actual, _ = install.(*jsonx.OrderedMap).Get("nix")
	if !reflect.DeepEqual(actual, []any{"base"}) {
		t.Errorf("source reader install.nix = %#v, want preserved base value", actual)
	}
	env, _ = got.Get("env")
	set, _ := env.(*jsonx.OrderedMap).Get("set")
	if value, _ := set.(*jsonx.OrderedMap).Get("RECOVERY_TEST"); value != "literal" {
		t.Errorf("source reader env.set.RECOVERY_TEST = %#v, want preserved literal", value)
	}
	for _, check := range []struct {
		path string
		want string
	}{
		{path: "config.install.nix[0]", want: "~/.config/yolo-jail/" + paths.UserConfigTOMLName + ":2:"},
		{path: "config.env.set.RECOVERY_TEST", want: "~/.config/yolo-jail/" + paths.UserConfigTOMLName + ":4:"},
	} {
		locations := sources.Locations(check.path)
		if len(locations) != 1 || !strings.Contains(locations[0], check.want) {
			t.Errorf("%s source locations = %q, want base source %q", check.path, locations, check.want)
		}
	}
	if len(warnings) != 2 || !strings.Contains(warnings[1], "Failed to parse") || !strings.Contains(warnings[1], fragmentDir) {
		t.Errorf("source-reader warnings = %q, want one additional existing parse-failure warning", warnings)
	}

	strict, strictSources, err := LoadReadableUserScopeConfigWithSources(true, nil)
	if err == nil || strict != nil || strictSources != nil || !strings.Contains(err.Error(), "Failed to parse") || !strings.Contains(err.Error(), fragmentDir) {
		t.Errorf("strict source reader = (%#v, %#v, %v), want refusal naming config.d and no hydrated config", strict, strictSources, err)
	}
}

func TestLoadReadableUserScopeConfigDFragmentsApplyResetSequentiallyWithSources(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	userDir := filepath.Join(home, ".config", "yolo-jail")
	basePath := filepath.Join(userDir, paths.UserConfigTOMLName)
	fragments := filepath.Join(userDir, "config.d")
	resetPath := filepath.Join(fragments, "00-reset.toml")
	lastPath := filepath.Join(fragments, "01-last.toml")
	write(t, basePath, "[install]\nnix = ['a']\n")
	write(t, resetPath, "[install]\nnix = []\n")
	write(t, lastPath, "[install]\nnix = ['b']\n")

	got, sources, err := LoadReadableUserScopeConfigWithSources(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	install, _ := got.Get("install")
	nix, _ := install.(*jsonx.OrderedMap).Get("nix")
	if !reflect.DeepEqual(nix, []any{"b"}) {
		t.Fatalf("user config.d install.nix = %#v, want [\"b\"] after the fragment reset", nix)
	}
	locations := sources.Locations("config.install.nix")
	for _, path := range []string{basePath, resetPath, lastPath} {
		found := false
		for _, location := range locations {
			if strings.Contains(location, filepath.Base(path)+":") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("install.nix provenance %q omits writer %q", locations, path)
		}
	}
	itemLocations := sources.Locations("config.install.nix[0]")
	if len(itemLocations) != 1 || !strings.Contains(itemLocations[0], filepath.Base(lastPath)+":") {
		t.Errorf("surviving install.nix item provenance = %q, want only final fragment %q", itemLocations, lastPath)
	}
}

func TestLoadReadableConfigComposesEmptyListResetAcrossThreeLayersWithSources(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	userPath := filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName)
	write(t, userPath, "[install]\nnix = ['a']\n")
	workspace := t.TempDir()
	projectPath := filepath.Join(workspace, paths.WorkspaceConfigTOMLName)
	localPath := filepath.Join(workspace, paths.WorkspaceLocalConfigTOMLName)
	write(t, projectPath, "[install]\nnix = []\n")
	write(t, localPath, "[install]\nnix = ['b']\n")

	got, sources, err := LoadReadableConfigWithSources(workspace, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	install, _ := got.Get("install")
	nix, _ := install.(*jsonx.OrderedMap).Get("nix")
	if !reflect.DeepEqual(nix, []any{"b"}) {
		t.Fatalf("three-layer install.nix = %#v, want [\"b\"] after the middle empty list clears the base", nix)
	}
	locations := sources.Locations("config.install.nix")
	for _, path := range []string{userPath, projectPath, localPath} {
		found := false
		for _, location := range locations {
			if strings.Contains(location, filepath.Base(path)+":") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("install.nix provenance %q omits writer %q", locations, path)
		}
	}
	itemLocations := sources.Locations("config.install.nix[0]")
	if len(itemLocations) != 1 || !strings.Contains(itemLocations[0], filepath.Base(localPath)+":") {
		t.Errorf("surviving install.nix item provenance = %q, want only local layer %q", itemLocations, localPath)
	}
}

func TestLoadReadableUserScopeReadsSortedConfigDFragments(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	userDir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(userDir, paths.UserConfigTOMLName), "packs = ['base']\n")
	withoutFragments, err := LoadReadableUserScopeConfig(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := withoutFragments.Get("packs"); !reflect.DeepEqual(got, []any{"base"}) {
		t.Fatalf("config without config.d = %#v", got)
	}
	fragments := filepath.Join(userDir, "config.d")
	if err := os.MkdirAll(fragments, 0o700); err != nil {
		t.Fatal(err)
	}
	fragment := filepath.Join(home, "outside.toml")
	write(t, fragment, "packs = ['linked']\n")
	if err := os.Symlink(fragment, filepath.Join(fragments, "20-extra.toml")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(fragments, "10-extra.toml"), "packs = ['first']\n")
	write(t, filepath.Join(fragments, "readme.txt"), "not a config")

	got, err := LoadReadableUserScopeConfig(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	packs, _ := got.Get("packs")
	if !reflect.DeepEqual(packs, []any{"base", "first", "linked"}) {
		t.Fatalf("config.d merge order = %#v", packs)
	}
}

func TestLoadReadableUserScopeRejectsLegacyCollision(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	userDir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(userDir, paths.UserConfigTOMLName), "packs = ['new']\n")
	write(t, filepath.Join(userDir, paths.UserConfigLegacyJSONCName), `{"packs":["old"]}`)
	if _, err := LoadReadableUserScopeConfig(true, nil); err == nil ||
		!strings.Contains(err.Error(), paths.UserConfigTOMLName) || !strings.Contains(err.Error(), paths.UserConfigLegacyJSONCName) {
		t.Fatalf("user format collision error = %v, want both sibling names", err)
	}
}

func TestLoadReadableUserScopeAcceptsUserSideNewVocabulary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName)
	write(t, path, `packs = ['claude', { git = 'https://example.invalid/matt-craft', ref = 'main' }]
[pack_sources.acme]
git = 'https://example.invalid/yolo-packs'
ref = 'main'
[active_provider]
default = 'bedrock'
[providers.zai]
api_key_env_name = 'ZAI_KEY'
[allow.aws-auth]
profile = 'my-sso-profile'
unnarrowed = true
`)
	if _, err := LoadReadableUserScopeConfig(true, nil); err != nil {
		t.Fatalf("valid user-side readable config refused: %v", err)
	}
}

func TestReadableTOMLSourcePositionsHandleQuotedKeysAndArrayItems(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, paths.WorkspaceConfigTOMLName), "mounts = [{ host = '~/repo', at = '/ctx/repo' }]\n\n[providers.\"org/name.with.dot\"]\nregion = 'east'\n")
	_, src, err := LoadReadableWorkspaceConfigWithSources(dir, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	mountLocs := src.Locations("config.mounts[0].host")
	providerLocs := src.Locations("config.providers.org/name.with.dot.region")
	if len(mountLocs) != 1 || !strings.HasSuffix(mountLocs[0], ":1:20") {
		t.Fatalf("array item source location = %q", mountLocs)
	}
	if len(providerLocs) != 1 || !strings.HasSuffix(providerLocs[0], ":4:10") {
		t.Fatalf("quoted dotted/slash key source location = %q", providerLocs)
	}
}

func TestReadableTOMLRejectsLegacyVocabularyWithoutAliases(t *testing.T) {
	cases := []struct {
		name, document, want string
	}{
		{"packages", `packages = ['just']
`, "install.nix"},
		{"brokered", `[brokered.git]
always = ['fetch']
`, "allow.*"},
		{"agents", `agents = ['claude']
`, "packs"},
		{"agent_profiles", `[agent_profiles]
pi = 'bedrock'
`, "active_provider"},
		{"use_profiles", `use_profiles = 'bedrock'
`, "active_provider"},
		{"repo_path", `repo_path = '/src/yolo'
`, "retired"},
		{"macos_log", `macos_log = 'user'
`, "macos-log pack"},
		{"mcp_presets", `mcp_presets = ['chrome-devtools']
`, "mcp_servers"},
		// The keys named setups replace are not here: v0.16.1 wrote them to TOML, so a TOML file
		// holding one is read and refused by its value (TestARemovedSpellingInATOMLFile…).
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, paths.WorkspaceConfigTOMLName), tc.document)
			if _, err := LoadReadableWorkspaceConfig(dir, true, nil); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("legacy TOML key error = %v, want guidance containing %q", err, tc.want)
			}
		})
	}
}

func TestReadableTOMLWorkspaceScopeRefusesUserLeversButAllowsModelFacts(t *testing.T) {
	cases := []struct {
		name, document, want string
	}{
		{"packs", `packs = ['claude']
`, "user-scope only"},
		{"provider routing", `[providers.zai]
api_key_env_name = 'ZAI_KEY'
`, "user-scope only"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, paths.WorkspaceConfigTOMLName), tc.document)
			if _, err := LoadReadableWorkspaceConfig(dir, true, nil); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("scope refusal = %v, want %q", err, tc.want)
			}
		})
	}
	// A mount's `write` is a project REQUEST (RC-D24): the reader takes it, and validation
	// judges it against the user's allow.mounts (mountsrequest_test.go).
	request := t.TempDir()
	write(t, filepath.Join(request, paths.WorkspaceConfigTOMLName), "mounts = [{ host = '~/data', at = '/ctx/data', write = true }]\n")
	if _, err := LoadReadableWorkspaceConfig(request, true, nil); err != nil {
		t.Fatalf("the reader refused a project's writable-mount request: %v", err)
	}

	dir := t.TempDir()
	write(t, filepath.Join(dir, paths.WorkspaceConfigTOMLName), `[providers.zai.models."model.with.dot"]
name = 'Project model label'
[requests.github]
repos = ['acme/shared']
`)
	if _, err := LoadReadableWorkspaceConfig(dir, true, nil); err != nil {
		t.Fatalf("existing shared model fact/request was refused: %v", err)
	}
}

func TestReadableTOMLEnvFilesAnchorToDeclaringLayer(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, paths.WorkspaceConfigTOMLName), "include_if_found = ['parts/env.toml']\n[env]\nfiles = ['root.env']\n")
	write(t, filepath.Join(dir, "parts", "env.toml"), "[env]\nfiles = ['nested.env']\n")
	got, err := LoadReadableWorkspaceConfig(dir, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	env, _ := got.Get("env")
	files, _ := env.(*jsonx.OrderedMap).Get("files")
	want := []any{filepath.Join(dir, "root.env"), filepath.Join(dir, "parts", "nested.env")}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("anchored env.files = %#v, want %#v", files, want)
	}
}

func TestReadableTOMLEmptyListClearsPolicyKeyAndRetainsLayerSources(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, paths.WorkspaceConfigTOMLName), "[machine]\nports = [{ host = 8080, jail = 80 }]\n")
	write(t, filepath.Join(dir, paths.WorkspaceLocalConfigTOMLName), "[machine]\nports = []\n")
	got, sources, err := LoadReadableWorkspaceConfigWithSources(dir, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	machine, _ := got.Get("machine")
	ports, _ := machine.(*jsonx.OrderedMap).Get("ports")
	if gotPorts, ok := ports.([]any); !ok || len(gotPorts) != 0 {
		t.Fatalf("empty-list removal result = %#v", ports)
	}
	locations := sources.Locations("config.machine.ports")
	if len(locations) != 2 || !strings.Contains(strings.Join(locations, "\n"), paths.WorkspaceConfigTOMLName) || !strings.Contains(strings.Join(locations, "\n"), paths.WorkspaceLocalConfigTOMLName) {
		t.Fatalf("empty-list removal source writers = %q", locations)
	}
}

func TestReadableTOMLValidationUsesActualArrayItemPosition(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, paths.WorkspaceConfigTOMLName), `mounts = [
  { host = '~/one', at = '/ctx/one' },
  { host = 12, at = '/ctx/two' },
]
`)
	_, err := LoadReadableWorkspaceConfig(dir, true, nil)
	if err == nil || !strings.Contains(err.Error(), ":3:") || !strings.Contains(err.Error(), "config.mounts[1].host") {
		t.Fatalf("second array item type error = %v, want line 3 and indexed config path", err)
	}
}

func TestReadableTOMLMCPServerTableAndOffRemoval(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, paths.WorkspaceConfigTOMLName), `[mcp_servers.local]
command = ['node']
args = ['server.js']
env = { API_KEY = 'value' }
requires_env = ['API_KEY']
provides = 'web_search'
`)
	if _, err := LoadReadableWorkspaceConfig(dir, true, nil); err != nil {
		t.Fatalf("MCP server table refused: %v", err)
	}
	write(t, filepath.Join(dir, paths.WorkspaceConfigTOMLName), `mcp_servers.local = 'off'
`)
	if _, err := LoadReadableWorkspaceConfig(dir, true, nil); err != nil {
		t.Fatalf("MCP server removal value refused: %v", err)
	}
	write(t, filepath.Join(dir, paths.WorkspaceConfigTOMLName), `mcp_servers.local = 'disabled'
`)
	if _, err := LoadReadableWorkspaceConfig(dir, true, nil); err == nil || !strings.Contains(err.Error(), "expected a table") {
		t.Fatalf("non-removal MCP string error = %v", err)
	}
}

func TestReadableListSchemaDeclaresElementShape(t *testing.T) {
	for _, rule := range ReadableSchemaKeys() {
		if rule.Type != SchemaTypeList {
			continue
		}
		itemPath := strings.Split(rule.Path, ".")
		itemPath = append(itemPath, "*")
		if _, ok := readableSchemaRule(itemPath); !ok {
			t.Errorf("readable list %q has no element schema", rule.Path)
		}
	}
}

func TestLoadReadableConfigComposesNewUserAndWorkspaceLayers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	write(t, filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName), "packs = ['claude']\n")
	workspace := t.TempDir()
	write(t, filepath.Join(workspace, paths.WorkspaceConfigTOMLName), "[install]\nnix = ['just']\n")
	got, err := LoadReadableConfig(workspace, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if packs, _ := got.Get("packs"); !reflect.DeepEqual(packs, []any{"claude"}) {
		t.Fatalf("composed packs = %#v", packs)
	}
	if _, exists := got.Get("install"); !exists {
		t.Fatalf("composed readable workspace layer missing: %#v", got)
	}
}

func TestLoadJSONCFileStillUsesLegacyVocabulary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, paths.WorkspaceConfigLegacyJSONCName)
	write(t, path, `{"packages": ["just"]}`)
	got, err := LoadJSONCFile(path, path, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := got.Get("packages"); !present {
		t.Fatal("low-level legacy JSONC decoder stopped returning the legacy package key")
	}
	if _, present := got.Get("install"); present {
		t.Fatal("low-level legacy JSONC decoder projected into readable vocabulary")
	}
}

func TestStandardConfigReadersActivateReadableConfigBeforeRuntimeHandoff(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	write(t, filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName), "packs = ['claude']\n")
	dir := t.TempDir()
	write(t, filepath.Join(dir, paths.WorkspaceConfigTOMLName), "[install]\nnix = ['just']\n")

	workspace, err := LoadWorkspaceConfig(dir, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if install, ok := workspace.Get("install"); !ok || install == nil {
		t.Fatalf("standard workspace reader did not load readable install: %#v", workspace)
	}
	user, err := UserScopeConfig(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if packs, _ := user.Get("packs"); !reflect.DeepEqual(packs, []any{"claude"}) {
		t.Fatalf("standard user reader packs = %#v", packs)
	}
	composed, err := LoadConfig(dir, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := composed.Get("install"); !exists {
		t.Fatalf("standard composed reader did not load readable workspace config: %#v", composed)
	}
}

func TestStandardReadersComposeIncludesFragmentsExplicitLayerAndProjectLocalOrder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "test")
	userDir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(userDir, paths.UserConfigTOMLName), "include_if_found = ['includes/base.toml']\n[install]\nnix = ['main']\n[machine.resources]\ncpus = 1\n[env.set]\nPAGER = 'main'\n")
	write(t, filepath.Join(userDir, "includes", "base.toml"), "[install]\nnix = ['included']\n")
	write(t, filepath.Join(userDir, "config.d", "10-first.toml"), "[install]\nnix = ['first']\n[env.set]\nPAGER = 'first'\n")
	write(t, filepath.Join(userDir, "config.d", "20-second.toml"), "[install]\nnix = ['second']\n[env.set]\nPAGER = 'second'\n")
	write(t, filepath.Join(userDir, "inherited-launch.jsonc"), `{"install":{"nix":["inherited"]},"env":{"set":{"PAGER":"inherited"}}}`)
	write(t, filepath.Join(userDir, "config.d", "ignored.jsonc"), `{"install":{"nix":["not-loaded"]}}`)
	layer := filepath.Join(t.TempDir(), "explicit.toml")
	write(t, layer, "include_if_found = ['nested/layer.toml']\n[install]\nnix = ['explicit']\n[env.set]\nPAGER = 'explicit'\n")
	write(t, filepath.Join(filepath.Dir(layer), "nested", "layer.toml"), "[install]\nnix = ['explicit-include']\n")
	if problem := ValidateUserLayer(layer); problem != "" {
		t.Fatalf("explicit TOML --user-layer validation: %s", problem)
	}
	t.Setenv(UserLayerEnv, layer)

	user, err := UserScopeConfig(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	install, _ := asMap(mustValue(t, user, "install"))
	if nix, _ := install.Get("nix"); !reflect.DeepEqual(nix, []any{"inherited", "main", "included", "first", "second", "explicit", "explicit-include"}) {
		t.Fatalf("user install.nix layer order = %#v", nix)
	}
	env, _ := asMap(mustValue(t, user, "env"))
	set, _ := asMap(mustValue(t, env, "set"))
	if pager, _ := set.Get("PAGER"); pager != "explicit" {
		t.Fatalf("explicit --user-layer did not win: PAGER=%#v", pager)
	}

	workspace := t.TempDir()
	write(t, filepath.Join(workspace, paths.WorkspaceConfigTOMLName), "[install]\nnix = ['project']\n[machine.resources]\ncpus = 2\n")
	write(t, filepath.Join(workspace, paths.WorkspaceLocalConfigTOMLName), "[install]\nnix = []\n[machine.resources]\ncpus = 3\n")
	composed, err := LoadConfig(workspace, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	install, _ = asMap(mustValue(t, composed, "install"))
	if nix, _ := install.Get("nix"); !reflect.DeepEqual(nix, []any{}) {
		t.Fatalf("local empty list did not clear composed install.nix: %#v", nix)
	}
	machine, _ := asMap(mustValue(t, composed, "machine"))
	resources, _ := asMap(mustValue(t, machine, "resources"))
	rawCPUs, _ := resources.Get("cpus")
	if cpus, ok := jsonx.AsInt(rawCPUs); !ok || cpus != 3 {
		t.Fatalf("project/local precedence resources.cpus=%#v", rawCPUs)
	}
}

func TestStandardReaderPreservesLegacyEnvSourcesAndLiteralOff(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	userPath := filepath.Join(home, ".config", "yolo-jail", paths.UserConfigLegacyJSONCName)
	write(t, userPath, `{"env_sources":["secrets.env",{"AWS_PROFILE":"off","PAGER":null}]}`)

	got, sources, err := UserScopeConfigWithSources(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Read in its readable spelling, env.sources (RC-D50): the same list, an inline entry as
	// { set, unset }.
	entries, ok := asList(mustValue(t, mustMap(t, got, "env"), "sources"))
	if !ok || len(entries) != 2 || entries[0] != filepath.Join(filepath.Dir(userPath), "secrets.env") {
		t.Fatalf("legacy ordered env_sources = %#v", entries)
	}
	inline, _ := asMap(entries[1])
	assignments, _ := asMap(mustValue(t, inline, "set"))
	if value, _ := assignments.Get("AWS_PROFILE"); value != "off" {
		t.Fatalf("literal environment value off changed: %#v", value)
	}
	if unset, _ := asList(mustValue(t, inline, "unset")); !reflect.DeepEqual(unset, []any{"PAGER"}) {
		t.Fatalf("legacy environment unset changed: %#v", unset)
	}
	resolved, removals := ResolveEnvSourcesFull(t.TempDir(), got, nil)
	if value, _ := resolved.Get("AWS_PROFILE"); value != "off" || !reflect.DeepEqual(removals, []string{"PAGER"}) {
		t.Fatalf("legacy environment operation changed: values=%#v removals=%#v", resolved, removals)
	}
	if sources == nil || len(sources.Locations("config.env.sources[1].set.AWS_PROFILE")) == 0 || len(sources.Locations("config.env.sources[1]")) == 0 {
		t.Fatalf("legacy env source origins were lost: %#v", sources)
	}
}

func TestStandardReaderPreservesOrderedLegacyEnvSourcesAcrossLayers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "test")
	userDir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(userDir, "inherited-launch.jsonc"), `{"env_sources":["inherited.env",{"DROP":null}]}`)
	write(t, filepath.Join(userDir, "inherited.env"), "DROP=inherited\nINHERITED=outer\n")
	write(t, filepath.Join(userDir, paths.UserConfigLegacyJSONCName), `{"include_if_found":["includes/later.jsonc"],"env_sources":[{"VALUE":"inline-before-include"}]}`)
	write(t, filepath.Join(userDir, "includes", "later.jsonc"), `{"env_sources":["later.env"]}`)
	write(t, filepath.Join(userDir, "includes", "later.env"), "VALUE=included-file\n")
	layer := filepath.Join(t.TempDir(), "explicit.jsonc")
	write(t, layer, `{"env_sources":[{"VALUE":"explicit-inline","CLEAR":"restored"}]}`)
	t.Setenv(UserLayerEnv, layer)

	got, sources, err := UserScopeConfigWithSources(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	entries, ok := asList(mustValue(t, mustMap(t, got, "env"), "sources"))
	if !ok || len(entries) != 5 {
		t.Fatalf("legacy ordered env_sources compatibility projection = %#v, want five entries", entries)
	}
	for i, entry := range entries {
		if path, ok := entry.(string); ok && !filepath.IsAbs(path) {
			t.Errorf("env_sources[%d] was not anchored to its declaring file: %q", i, path)
		}
	}
	resolved, removals := ResolveEnvSourcesFull(t.TempDir(), got, nil)
	if value, _ := resolved.Get("VALUE"); value != "explicit-inline" {
		t.Errorf("later explicit inline source did not win after layer dotenv: VALUE=%#v", value)
	}
	if value, _ := resolved.Get("INHERITED"); value != "outer" {
		t.Errorf("inherited file value missing: %#v", value)
	}
	if value, _ := resolved.Get("CLEAR"); value != "restored" {
		t.Errorf("later inline assignment did not restore cleared value: CLEAR=%#v", value)
	}
	if !reflect.DeepEqual(removals, []string{"DROP"}) {
		t.Errorf("legacy null unset was not preserved: %#v", removals)
	}
	for _, sourcePath := range []string{"config.env.sources[0]", "config.env.sources[2]", "config.env.sources[4].set.VALUE"} {
		if locations := sources.Locations(sourcePath); len(locations) == 0 {
			t.Errorf("ordered source provenance missing %s: %#v", sourcePath, sources)
		}
	}
}

func TestStandardReaderAnchorsLegacyInheritedEnvSources(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "test")
	userPath := filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName)
	write(t, userPath, "[env.set]\nPAGER = 'local'\n")
	inheritedPath := filepath.Join(filepath.Dir(userPath), "inherited-launch.jsonc")
	write(t, inheritedPath, `{"env_sources":["secrets.env"],"env":{"set":{"VISUAL":"host"}}}`)

	got, err := UserScopeConfig(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	env, _ := asMap(mustValue(t, got, "env"))
	set, _ := asMap(mustValue(t, env, "set"))
	if pager, _ := set.Get("PAGER"); pager != "local" {
		t.Fatalf("jail's own config did not win over inherited layer: PAGER=%#v", pager)
	}
	sources, _ := asList(mustValue(t, env, "files"))
	if len(sources) != 1 || sources[0] != filepath.Join(filepath.Dir(userPath), "secrets.env") {
		t.Fatalf("inherited legacy env_sources were not anchored beside their file: %#v", sources)
	}
}

func TestStandardReaderCommonValidatorAcceptsB2EffectiveFields(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, WorkspaceTOMLConfigName)
	write(t, path, `[install]
nix = ['jq']
[machine]
gpu = false
[[machine.ports]]
host = 8080
jail = 80
[[machine.forward]]
jail = 3000
host = 3001
[machine.resources]
cpus = 2
[env.set]
PAGER = 'off'
`)

	config, err := LoadConfig(workspace, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if errs, warns := ValidateConfig(config, workspace, nil); len(errs) > 0 || len(warns) > 0 {
		t.Fatalf("common validator rejected valid B2 config: errors=%v warnings=%v", errs, warns)
	}
}

func TestStandardReaderCommonValidatorUsesLegacyPackageSourcePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	userDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(userDir, "extra.jsonc")
	write(t, filepath.Join(userDir, paths.UserConfigLegacyJSONCName), `{"packages": ["strace"], "include_if_found": ["extra.jsonc"]}`)
	write(t, path, `{
  "packages": [
    "bad name!"
  ]
}
`)
	workspace := t.TempDir()
	write(t, filepath.Join(workspace, paths.WorkspaceConfigLegacyJSONCName), `{}`)
	config, sources, err := LoadConfigWithSources(workspace, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	errs, _ := ValidateConfig(config, workspace, nil)
	located := sources.Annotate(errs)
	if len(located) != 1 || !strings.HasPrefix(located[0], "~/.config/yolo-jail/extra.jsonc:3:5:") || !strings.Contains(located[0], "config.packages[1]: invalid package name 'bad name!'") {
		t.Fatalf("legacy diagnostic did not preserve the included file's authored package path: %v", located)
	}
}

func TestStandardReaderCommonValidatorPreservesCanonicalSourcePaths(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, WorkspaceTOMLConfigName)
	write(t, path, "[install]\nnix = ['config.packages bad']\n")
	config, sources, err := LoadConfigWithSources(workspace, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	errs, _ := ValidateConfig(config, workspace, nil)
	located := sources.Annotate(errs)
	if len(located) != 1 || !strings.HasPrefix(located[0], path+":2:") || !strings.Contains(located[0], "config.install.nix[0]") || !strings.Contains(located[0], "invalid package name 'config.packages bad'") {
		t.Fatalf("B2 validator error lost canonical path/provenance: %v", located)
	}
}

func TestStandardReaderCommonValidatorRejectsMalformedReadableValuesWithoutMutation(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{name: "install type", body: `{"install":{"nix":"not-a-list"}}`, want: "config.install.nix: expected a list"},
		{name: "machine nested unknown", body: `{"machine":{"resources":{"unknown":true}}}`, want: "config.machine.resources.unknown: unknown key"},
		{name: "env files wrong type", body: `{"env":{"files":"not-a-list"}}`, want: "config.env: removed"},
		{name: "env set wrong type", body: `{"env":{"set":"not-a-table"}}`, want: "config.env: removed"},
		{name: "env unset wrong type", body: `{"env":{"unset":"not-a-list"}}`, want: "config.env: removed"},
		{name: "env files nonstring", body: `{"env":{"files":[1]}}`, want: "config.env: removed"},
		{name: "env set nonstring", body: `{"env":{"set":{"PAGER":1}}}`, want: "config.env: removed"},
		{name: "env unset nonstring", body: `{"env":{"unset":[1]}}`, want: "config.env: removed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("YOLO_VERSION", "")
			t.Setenv(UserLayerEnv, "")
			write(t, filepath.Join(home, ".config", "yolo-jail", paths.UserConfigLegacyJSONCName), tc.body)
			workspace := t.TempDir()
			config, err := LoadConfig(workspace, true, nil)
			if err != nil {
				t.Fatal(err)
			}
			before := cloneValidationMap(config)
			errs, _ := ValidateConfig(config, workspace, nil)
			if !strings.Contains(strings.Join(errs, "\n"), tc.want) {
				t.Fatalf("common validator missed malformed readable input %s: %v", tc.name, errs)
			}
			if !reflect.DeepEqual(config, before) {
				t.Fatalf("validation projection mutated the effective map: before=%#v after=%#v", before, config)
			}
		})
	}
}

func TestStandardReaderFormatRefusalLabelEscapesIncludedFilenameControls(t *testing.T) {
	workspace := t.TempDir()
	filename := "bad\n\x1b.toml"
	write(t, filepath.Join(workspace, WorkspaceConfigName), `{"include_if_found": ["bad\n\u001b.toml"]}`)
	write(t, filepath.Join(workspace, filename), "format = 'bad'\n")

	_, err := LoadConfig(workspace, true, nil)
	if err == nil {
		t.Fatal("LoadConfig accepted a non-integer TOML format")
	}
	message := err.Error()
	if strings.ContainsAny(message, "\n\r\x1b") {
		t.Fatalf("format refusal includes literal filename controls: %q", message)
	}
	if !strings.Contains(message, `bad\n\x1b.toml`) {
		t.Fatalf("format refusal does not visibly identify the included filename: %q", message)
	}
}

func TestStandardReaderMCPCommonValidatorKeepsReadableValuesUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name        string
		body        string
		wantOff     bool
		wantCommand []any
	}{
		{name: "server off removal", body: "mcp_servers.local = 'off'\n", wantOff: true},
		{name: "literal off argv element", body: "[mcp_servers.local]\ncommand = ['node', 'off']\n", wantCommand: []any{"node", "off"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("YOLO_VERSION", "")
			t.Setenv(UserLayerEnv, "")
			workspace := t.TempDir()
			write(t, filepath.Join(workspace, WorkspaceTOMLConfigName), tc.body)

			config, err := LoadConfig(workspace, true, nil)
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			servers, ok := asMap(mustValue(t, config, "mcp_servers"))
			if !ok {
				t.Fatalf("mcp_servers is not a table: %#v", config)
			}
			server := mustValue(t, servers, "local")
			if tc.wantOff {
				if server != "off" {
					t.Fatalf("LoadConfig mcp_servers.local = %#v, want literal removal marker %q", server, "off")
				}
			} else {
				entry, ok := asMap(server)
				if !ok || !reflect.DeepEqual(mustValue(t, entry, "command"), tc.wantCommand) || len(entry.Keys()) != 1 {
					t.Fatalf("LoadConfig mcp_servers.local = %#v, want canonical argv command %#v", server, tc.wantCommand)
				}
			}

			before := cloneValidationMap(config)
			errs, _ := ValidateConfig(config, workspace, nil)
			if len(errs) != 0 {
				t.Fatalf("ValidateConfig rejected readable MCP %s: %v", tc.name, errs)
			}
			if !reflect.DeepEqual(config, before) {
				t.Fatalf("validation projection changed the effective config: before=%#v after=%#v", before, config)
			}
		})
	}
}

func TestStandardReaderMCPCommonValidatorRejectsMalformedLegacyArgs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv(UserLayerEnv, "")
	workspace := t.TempDir()
	write(t, filepath.Join(workspace, WorkspaceConfigName), `{"mcp_servers":{"local":{"command":"node","args":["server.js",7]}}}`)

	config, err := LoadConfig(workspace, true, nil)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	before := cloneValidationMap(config)
	errs, _ := ValidateConfig(config, workspace, nil)
	if !strings.Contains(strings.Join(errs, "\n"), "config.mcp_servers.local.args") {
		t.Fatalf("ValidateConfig accepted malformed MCP args: %v", errs)
	}
	if !reflect.DeepEqual(config, before) {
		t.Fatalf("validation projection changed the effective config: before=%#v after=%#v", before, config)
	}
}

func TestStandardReaderRejectsMalformedTypedMCPArgs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv(UserLayerEnv, "")
	workspace := t.TempDir()
	write(t, filepath.Join(workspace, WorkspaceTOMLConfigName), "[mcp_servers.local]\ncommand = ['node']\nargs = [7]\n")

	if _, err := LoadConfig(workspace, true, nil); err == nil || !strings.Contains(err.Error(), "mcp_servers.local.args") {
		t.Fatalf("LoadConfig accepted malformed typed MCP args: %v", err)
	}
}

func TestStandardReaderMCPCommonValidatorKeepsLegacyProgramArgvOpaque(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv(UserLayerEnv, "")
	workspace := t.TempDir()
	write(t, filepath.Join(workspace, WorkspaceConfigName), `{"mcp_servers":{"local":{"command":"off","args":["server.js","literal off"]}}}`)

	config, err := LoadConfig(workspace, true, nil)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	before := cloneValidationMap(config)
	errs, _ := ValidateConfig(config, workspace, nil)
	if len(errs) != 0 {
		t.Fatalf("ValidateConfig rejected legacy MCP program argv: %v", errs)
	}
	servers, _ := asMap(mustValue(t, config, "mcp_servers"))
	server, _ := asMap(mustValue(t, servers, "local"))
	if command := mustValue(t, server, "command"); command != "off" {
		t.Fatalf("legacy MCP command = %#v, want opaque literal command %q", command, "off")
	}
	if args := mustValue(t, server, "args"); !reflect.DeepEqual(args, []any{"server.js", "literal off"}) {
		t.Fatalf("legacy MCP args = %#v, want original argv", args)
	}
	if !reflect.DeepEqual(config, before) {
		t.Fatalf("validation projection changed the effective config: before=%#v after=%#v", before, config)
	}
}

func TestStandardReaderMCPTableOffResetSurvivesIncludeComposition(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	write(t, filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName), `[mcp_servers.local]
command = ['old']
args = ['--old']
[mcp_servers.local.env]
TOKEN = 'old'
`)
	workspace := t.TempDir()
	write(t, filepath.Join(workspace, WorkspaceTOMLConfigName), `include_if_found = ['replacement.toml']
[mcp_servers]
local = 'off'
`)
	write(t, filepath.Join(workspace, "replacement.toml"), `[mcp_servers.local]
command = ['new']
`)

	withoutSources, err := LoadConfig(workspace, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	withSources, _, err := LoadConfigWithSources(workspace, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(withoutSources, withSources) {
		t.Fatalf("source recording changed effective map: without=%#v with=%#v", withoutSources, withSources)
	}
	withServers, _ := asMap(mustValue(t, withSources, "mcp_servers"))
	server, _ := asMap(mustValue(t, withServers, "local"))
	if command, _ := server.Get("command"); !reflect.DeepEqual(command, []any{"new"}) {
		t.Fatalf("replacement command = %#v, want only ['new']", command)
	}
	for _, key := range []string{"args", "env"} {
		if _, present := server.Get(key); present {
			t.Errorf("table-off reset resurrected inherited %s: %#v", key, server)
		}
	}
	if errs, _ := ValidateConfig(withSources, workspace, nil); len(errs) != 0 {
		t.Fatalf("common validator rejected readable MCP command: %v", errs)
	}
}

func TestStandardReaderCommonValidatorRejectsMalformedMachinePortShapes(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{name: "ports scalar", body: `{"machine":{"ports":"8080:80"}}`, want: "config.machine.ports: expected a list"},
		{name: "ports legacy string entry", body: `{"machine":{"ports":["8080:80"]}}`, want: "config.machine.ports[0]: expected a table"},
		{name: "forward scalar", body: `{"machine":{"forward":"8080:80"}}`, want: "config.machine.forward: expected a list"},
		{name: "forward legacy string entry", body: `{"machine":{"forward":["8080:80"]}}`, want: "config.machine.forward[0]: expected a table"},
		{name: "port unknown member", body: `{"machine":{"ports":[{"host":8080,"jail":80,"unexpected":true}]}}`, want: "config.machine.ports[0].unexpected: unknown key"},
		{name: "forward unknown member", body: `{"machine":{"forward":[{"jail":8080,"host":80,"unexpected":true}]}}`, want: "config.machine.forward[0].unexpected: unknown key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("YOLO_VERSION", "")
			write(t, filepath.Join(home, ".config", "yolo-jail", paths.UserConfigLegacyJSONCName), tc.body)
			workspace := t.TempDir()
			config, err := LoadConfig(workspace, true, nil)
			if err != nil {
				t.Fatal(err)
			}
			before := cloneValidationMap(config)
			errs, _ := ValidateConfig(config, workspace, nil)
			if !strings.Contains(strings.Join(errs, "\n"), tc.want) {
				t.Fatalf("common validator missed malformed port shape %s: %v", tc.name, errs)
			}
			if !reflect.DeepEqual(config, before) {
				t.Fatalf("validation projection mutated the effective map: before=%#v after=%#v", before, config)
			}
		})
	}
}

func TestStandardReaderCommonValidatorPreservesReadableDiagnosticPaths(t *testing.T) {
	t.Run("environment key", func(t *testing.T) {
		workspace := t.TempDir()
		path := filepath.Join(workspace, WorkspaceTOMLConfigName)
		write(t, path, "[env.set]\n'BAD-NAME' = 'off'\n")
		config, sources, err := LoadConfigWithSources(workspace, true, nil)
		if err != nil {
			t.Fatal(err)
		}
		errs, _ := ValidateConfig(config, workspace, nil)
		located := sources.Annotate(errs)
		if len(located) != 1 || !strings.HasPrefix(located[0], path+":2:") || !strings.Contains(located[0], "config.env.set.BAD-NAME") {
			t.Fatalf("readable env diagnostic lost authored source path: %v", located)
		}
	})

	t.Run("unprojected legacy address-bound port", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("YOLO_VERSION", "")
		workspace := t.TempDir()
		path := filepath.Join(workspace, WorkspaceConfigName)
		write(t, path, `{"packages":["not a package"],"network":{"ports":["127.0.0.1:8000:bad"]}}`)
		config, sources, err := LoadConfigWithSources(workspace, true, nil)
		if err != nil {
			t.Fatal(err)
		}
		errs, _ := ValidateConfig(config, workspace, nil)
		located := sources.Annotate(errs)
		joined := strings.Join(located, "\n")
		if !strings.Contains(joined, "config.packages[0]") || !strings.Contains(joined, "config.network.ports[0].container") {
			t.Fatalf("diagnostics did not preserve the projected and unprojected source paths: %v", located)
		}
		for _, message := range located {
			if strings.Contains(message, "config.network.ports[0].container") && !strings.HasPrefix(message, path+":") {
				t.Errorf("legacy address-bound port lost its authored location: %s", message)
			}
		}
	})
}

func TestStandardReaderEscapesControlCharactersInTOMLIncludeLabels(t *testing.T) {
	workspace := t.TempDir()
	name := "bad\n\x1b.toml"
	write(t, filepath.Join(workspace, WorkspaceTOMLConfigName), `include_if_found = ["bad\n\u001b.toml"]`)
	write(t, filepath.Join(workspace, name), "unrecognized = true\n")

	_, err := LoadWorkspaceConfig(workspace, true, nil)
	if err == nil {
		t.Fatal("invalid included TOML unexpectedly loaded")
	}
	if strings.ContainsAny(err.Error(), "\r\n\x1b") {
		t.Fatalf("TOML include label exposed terminal control characters: %q", err)
	}

	write(t, filepath.Join(workspace, name), "format = 3\n")
	_, err = LoadWorkspaceConfig(workspace, true, nil)
	if err == nil || !strings.Contains(err.Error(), "format 3") {
		t.Fatalf("included future format was not refused: %v", err)
	}
	if strings.ContainsAny(err.Error(), "\r\n\x1b") {
		t.Fatalf("future-format refusal exposed terminal control characters: %q", err)
	}
}

func TestStandardReaderKeepsTOMLEmptyResetAcrossNonemptyInclude(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	write(t, filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName), "[install]\nnix = ['user']\n")
	workspace := t.TempDir()
	write(t, filepath.Join(workspace, WorkspaceTOMLConfigName), "include_if_found = ['add.toml']\n[install]\nnix = []\n")
	write(t, filepath.Join(workspace, "add.toml"), "[install]\nnix = ['new']\n")

	withoutSources, err := LoadConfig(workspace, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	withSources, sources, err := LoadConfigWithSources(workspace, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(withoutSources, withSources) {
		t.Fatalf("source recording changed effective map: without=%#v with=%#v", withoutSources, withSources)
	}
	install, _ := asMap(mustValue(t, withSources, "install"))
	if nix, _ := install.Get("nix"); !reflect.DeepEqual(nix, []any{"new"}) {
		t.Fatalf("TOML empty reset was lost across a nonempty include: %#v", nix)
	}
	if sources == nil || len(sources.Locations("config.install.nix[0]")) == 0 {
		t.Fatalf("included value lost provenance: %#v", sources)
	}
}

func TestStandardReadersKeepLegacyEmptyListsAdditiveAcrossIncludesAndLayers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	write(t, filepath.Join(home, ".config", "yolo-jail", paths.UserConfigLegacyJSONCName), `{"packages":["user"]}`)
	layer := filepath.Join(t.TempDir(), "layer.jsonc")
	write(t, layer, `{"packages":[]}`)
	t.Setenv(UserLayerEnv, layer)
	workspace := t.TempDir()
	write(t, filepath.Join(workspace, paths.WorkspaceConfigLegacyJSONCName), `{"packages":["base"],"include_if_found":["empty.jsonc"]}`)
	write(t, filepath.Join(workspace, "empty.jsonc"), `{"packages":[]}`)
	write(t, filepath.Join(workspace, WorkspaceLocalConfigName), `{"packages":[]}`)

	got, err := LoadConfig(workspace, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	install, _ := asMap(mustValue(t, got, "install"))
	if nix, _ := install.Get("nix"); !reflect.DeepEqual(nix, []any{"user", "base"}) {
		t.Fatalf("legacy empty list changed list-union behavior across reader layers: %#v", nix)
	}
}

func TestStandardWorkspaceReaderProjectsRepresentableLegacyPorts(t *testing.T) {
	workspace := t.TempDir()
	write(t, filepath.Join(workspace, paths.WorkspaceConfigLegacyJSONCName), `{"network":{"mode":"bridge","ports":["8000:80"],"forward_host_ports":["5432:15432"]}}`)
	got, sources, err := LoadWorkspaceConfigWithSources(workspace, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	machine, _ := asMap(mustValue(t, got, "machine"))
	ports, _ := asList(mustValue(t, machine, "ports"))
	port, _ := asMap(ports[0])
	if host, ok := jsonx.AsInt(mustValue(t, port, "host")); !ok || host != 8000 {
		t.Fatalf("machine.ports host = %#v", mustValue(t, port, "host"))
	}
	if jail, ok := jsonx.AsInt(mustValue(t, port, "jail")); !ok || jail != 80 {
		t.Fatalf("machine.ports jail = %#v", mustValue(t, port, "jail"))
	}
	forward, _ := asList(mustValue(t, machine, "forward"))
	mapping, _ := asMap(forward[0])
	if jail, ok := jsonx.AsInt(mustValue(t, mapping, "jail")); !ok || jail != 5432 {
		t.Fatalf("machine.forward jail = %#v", mustValue(t, mapping, "jail"))
	}
	if host, ok := jsonx.AsInt(mustValue(t, mapping, "host")); !ok || host != 15432 {
		t.Fatalf("machine.forward host = %#v", mustValue(t, mapping, "host"))
	}
	network, _ := asMap(mustValue(t, got, "network"))
	if mode, _ := network.Get("mode"); mode != "bridge" {
		t.Fatalf("network.mode = %#v", mode)
	}
	if _, exists := network.Get("ports"); exists {
		t.Fatal("representable legacy network.ports remained in the effective vocabulary")
	}
	if len(sources.Locations("config.machine.ports[0].host")) == 0 || len(sources.Locations("config.machine.forward[0].jail")) == 0 {
		t.Fatalf("normalized legacy port source locations missing: %#v", sources)
	}

	// An address-bound or protocol entry is representable too (RC-D53): it reads as a table
	// that renders the same -p text.
	write(t, filepath.Join(workspace, paths.WorkspaceConfigLegacyJSONCName), `{"network":{"ports":["127.0.0.1:8000:80", "9000:9000/udp", "0.0.0.0:53:53/udp"]}}`)
	legacyAddressBound, err := LoadWorkspaceConfig(workspace, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := legacyAddressBound.Get("network"); exists {
		t.Fatalf("an address-bound legacy port stayed in the legacy vocabulary: %#v", legacyAddressBound)
	}
	boundMachine, _ := asMap(mustValue(t, legacyAddressBound, "machine"))
	boundPorts, _ := asList(mustValue(t, boundMachine, "ports"))
	var specs []string
	for _, entry := range boundPorts {
		table, _ := asMap(entry)
		spec, ok := PublishPortSpec(table)
		if !ok {
			t.Fatalf("a lowered port does not render: %#v", entry)
		}
		specs = append(specs, spec)
	}
	if want := []string{"127.0.0.1:8000:80", "9000:9000/udp", "0.0.0.0:53:53/udp"}; !reflect.DeepEqual(specs, want) {
		t.Fatalf("the lowered ports render %q, want the legacy entries %q", specs, want)
	}
}

func TestStandardConfigReadersNormalizeB2LegacyKeysAndPreserveOtherSemantics(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	userPath := filepath.Join(home, ".config", "yolo-jail", paths.UserConfigLegacyJSONCName)
	write(t, userPath, `{"packages":["base"],"profiles":{"claude":{"provider":"bedrock","options":{"temperature":0.2}}},"brokered":{"git":{"grants":[{"kind":"branch","repo":"example.com/acme","branch":"main"}]}},"packs":["local/tools"]}`)
	workspace := t.TempDir()
	write(t, filepath.Join(workspace, paths.WorkspaceConfigTOMLName), "[install]\nnix = ['project']\n[machine]\ngpu = true\n")

	got, sources, err := LoadConfigWithSources(workspace, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	installValue, ok := got.Get("install")
	if !ok {
		t.Fatalf("effective install missing: %#v", got)
	}
	install, ok := asMap(installValue)
	if !ok {
		t.Fatalf("effective install has type %T", installValue)
	}
	if nix, _ := install.Get("nix"); !reflect.DeepEqual(nix, []any{"base", "project"}) {
		t.Fatalf("merged install.nix = %#v", nix)
	}
	if _, ok := got.Get("gpu"); ok {
		t.Fatal("legacy B2 key gpu leaked into the effective map")
	}
	machine, _ := asMap(mustValue(t, got, "machine"))
	if gpu, _ := machine.Get("gpu"); gpu != true {
		t.Fatalf("machine.gpu = %#v", gpu)
	}
	profiles, _ := asMap(mustValue(t, got, "profiles"))
	wantProfiles := decode(t, `{"claude":{"provider":"bedrock","options":{"temperature":0.2}}}`)
	wantProfile := mustValue(t, wantProfiles, "claude")
	if !reflect.DeepEqual(mustValue(t, profiles, "claude"), wantProfile) {
		t.Fatalf("legacy profile/options semantics changed: got %#v want %#v", mustValue(t, profiles, "claude"), wantProfile)
	}
	brokered, _ := asMap(mustValue(t, got, "brokered"))
	wantBrokered := decode(t, `{"git":{"grants":[{"kind":"branch","repo":"example.com/acme","branch":"main"}]}}`)
	wantGit := mustValue(t, wantBrokered, "git")
	if !reflect.DeepEqual(mustValue(t, brokered, "git"), wantGit) {
		t.Fatalf("legacy scoped broker grant changed: got %#v want %#v", mustValue(t, brokered, "git"), wantGit)
	}
	if packs, _ := got.Get("packs"); !reflect.DeepEqual(packs, []any{"local/tools"}) {
		t.Fatalf("pack identity = %#v", packs)
	}
	if sources == nil {
		t.Fatal("legacy source provenance is missing")
	}
	profileLocations := sources.Locations("config.profiles.claude.options.temperature")
	grantLocations := sources.Locations("config.brokered.git.grants[0].branch")
	if len(sources.Locations("config.install.nix")) == 0 || len(profileLocations) == 0 || len(grantLocations) == 0 ||
		!strings.Contains(strings.Join(profileLocations, "\n"), paths.UserConfigLegacyJSONCName) ||
		!strings.Contains(strings.Join(grantLocations, "\n"), paths.UserConfigLegacyJSONCName) {
		t.Fatalf("legacy source provenance or source format did not follow effective paths: profiles=%q grants=%q sources=%#v", profileLocations, grantLocations, sources)
	}
}

func mustValue(t *testing.T, values *jsonx.OrderedMap, key string) any {
	t.Helper()
	value, ok := values.Get(key)
	if !ok {
		t.Fatalf("missing %q in %#v", key, values)
	}
	return value
}

func TestReadableWorkspaceConfigCollisionOffersSafeManualResolution(t *testing.T) {
	dir := t.TempDir()
	toml := filepath.Join(dir, paths.WorkspaceConfigTOMLName)
	legacy := filepath.Join(dir, paths.WorkspaceConfigLegacyJSONCName)
	write(t, toml, "[install]\nnix = ['new']\n")
	write(t, legacy, `{"packages":["legacy"]}`)
	before := snapshotReadableConfigDir(t, dir)

	if _, err := LoadReadableWorkspaceConfig(dir, true, nil); err == nil {
		t.Fatal("readable workspace loader accepted colliding files")
	} else {
		for _, want := range []string{paths.WorkspaceConfigTOMLName, paths.WorkspaceConfigLegacyJSONCName, "never merged", "backup path does not already exist", "then rerun the command", "mv ", paths.WorkspaceConfigLegacyJSONCName + ".backup", paths.WorkspaceConfigTOMLName + ".backup", "neither file is converted"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("collision error %q does not contain safe-resolution guidance %q", err, want)
			}
		}
		if strings.Contains(err.Error(), "yolo config migrate") {
			t.Errorf("collision error names nonexistent migration command: %v", err)
		}
	}
	if after := snapshotReadableConfigDir(t, dir); !reflect.DeepEqual(after, before) {
		t.Fatalf("collision refusal wrote config files: before=%v after=%v", before, after)
	}

	backup := toml + ".backup"
	if err := os.Rename(toml, backup); err != nil {
		t.Fatal(err)
	}
	legacyConfig, err := LoadWorkspaceConfig(dir, true, nil)
	if err != nil {
		t.Fatalf("legacy workspace reader stopped working after moving TOML aside: %v", err)
	}
	install, ok := asMap(mustValue(t, legacyConfig, "install"))
	if nix, exists := install.Get("nix"); !ok || !exists || !reflect.DeepEqual(nix, []any{"legacy"}) {
		t.Fatalf("standard legacy workspace config = %#v, want normalized install.nix", legacyConfig)
	}
	if got, err := os.ReadFile(backup); err != nil || string(got) != "[install]\nnix = ['new']\n" {
		t.Fatalf("preserved TOML backup = %q, %v", got, err)
	}
}

func TestReadableUserScopeCollisionOffersSafeManualResolution(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	userDir := filepath.Join(home, ".config", "yolo-jail")
	toml := filepath.Join(userDir, paths.UserConfigTOMLName)
	legacy := filepath.Join(userDir, paths.UserConfigLegacyJSONCName)
	write(t, toml, "packs = ['new']\n")
	write(t, legacy, `{"packs":["legacy"]}`)
	before := snapshotReadableConfigDir(t, userDir)

	if _, err := LoadReadableUserScopeConfig(true, nil); err == nil {
		t.Fatal("readable user loader accepted colliding files")
	} else {
		for _, want := range []string{paths.UserConfigTOMLName, paths.UserConfigLegacyJSONCName, "never merged", "backup path does not already exist", "then rerun the command", "mv ", paths.UserConfigLegacyJSONCName + ".backup", paths.UserConfigTOMLName + ".backup", "neither file is converted"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("collision error %q does not contain safe-resolution guidance %q", err, want)
			}
		}
		if strings.Contains(err.Error(), "yolo config migrate") {
			t.Errorf("collision error names nonexistent migration command: %v", err)
		}
	}
	if after := snapshotReadableConfigDir(t, userDir); !reflect.DeepEqual(after, before) {
		t.Fatalf("collision refusal wrote config files: before=%v after=%v", before, after)
	}

	backup := toml + ".backup"
	if err := os.Rename(toml, backup); err != nil {
		t.Fatal(err)
	}
	legacyConfig, err := UserScopeConfig(true, nil)
	if err != nil {
		t.Fatalf("legacy user reader stopped working after moving TOML aside: %v", err)
	}
	if packs, ok := legacyConfig.Get("packs"); !ok || !reflect.DeepEqual(packs, []any{"legacy"}) {
		t.Fatalf("legacy user config = %#v, want original packs", legacyConfig)
	}
	if got, err := os.ReadFile(backup); err != nil || string(got) != "packs = ['new']\n" {
		t.Fatalf("preserved TOML backup = %q, %v", got, err)
	}
}

func TestReadableWorkspaceConfigThreeSiblingCollisionRecovery(t *testing.T) {
	testReadableThreeSiblingCollisionRecovery(t, false)
}

func TestReadableUserScopeThreeSiblingCollisionRecovery(t *testing.T) {
	testReadableThreeSiblingCollisionRecovery(t, true)
}

func testReadableThreeSiblingCollisionRecovery(t *testing.T, userScope bool) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "reader path with spaces and 'quotes")
	dir := root
	var names []string
	var read func() (*jsonx.OrderedMap, error)
	var toml string
	var expectedKey string
	var expectedValue any
	if userScope {
		dir = filepath.Join(root, ".config", "yolo-jail")
		t.Setenv("HOME", root)
		names = []string{paths.UserConfigTOMLName, paths.UserConfigLegacyJSONCName, paths.UserConfigLegacyJSONName}
		toml = filepath.Join(dir, names[0])
		expectedKey, expectedValue = "packs", []any{"new"}
		read = func() (*jsonx.OrderedMap, error) { return LoadReadableUserScopeConfig(true, nil) }
	} else {
		names = []string{paths.WorkspaceConfigTOMLName, paths.WorkspaceConfigLegacyJSONCName, paths.WorkspaceConfigLegacyJSONName}
		toml = filepath.Join(dir, names[0])
		expectedKey, expectedValue = "install", nil
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		workspace, err := filepath.Rel(cwd, dir)
		if err != nil {
			t.Fatal(err)
		}
		read = func() (*jsonx.OrderedMap, error) { return LoadReadableWorkspaceConfig(workspace, true, nil) }
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	contents := []string{"packs = ['new']\n", `{"packs":["old-jsonc"]}`, `{"packs":["old-json"]}`}
	if !userScope {
		contents = []string{"[install]\nnix = ['new']\n", `{"packages":["old-jsonc"]}`, `{"packages":["old-json"]}`}
	}
	files := make([]string, len(names))
	for i, name := range names {
		files[i] = filepath.Join(dir, name)
		write(t, files[i], contents[i])
	}
	before := snapshotReadableConfigDir(t, dir)
	if _, err := read(); err == nil {
		t.Fatal("readable reader accepted TOML, JSONC, and JSON siblings")
	} else {
		for _, file := range files {
			if !strings.Contains(err.Error(), file) {
				t.Errorf("collision error %q omits supplied full path %q", err, file)
			}
		}
		if strings.Contains(err.Error(), "yolo config migrate") {
			t.Errorf("collision error recommends nonexistent migration: %v", err)
		}
		if after := snapshotReadableConfigDir(t, dir); !reflect.DeepEqual(after, before) {
			t.Fatalf("collision refusal wrote config files: before=%v after=%v", before, after)
		}
		commands := readableCollisionRecoveryCommands(t, err, files)
		keepTOML, ok := commands[toml]
		if !ok {
			t.Fatalf("collision error has no keep-TOML recovery for %q: %v", toml, err)
		}
		for _, keep := range files {
			command, ok := commands[keep]
			if !ok {
				t.Errorf("collision error has no recovery for keeping %q active", keep)
				continue
			}
			for _, source := range files {
				if source == keep {
					continue
				}
				backup := source + ".backup"
				if !strings.Contains(command, shquote.Quote(source)) || !strings.Contains(command, shquote.Quote(backup)) {
					t.Errorf("recovery for keeping %q active omits full source/destination paths %q and %q: %s", keep, source, backup, command)
				}
			}
		}
		otherCWD := t.TempDir()
		jsoncBackup := files[1] + ".backup"
		write(t, jsoncBackup, "do not overwrite\n")
		runReadableRecoveryCommand(t, otherCWD, keepTOML, false)
		if got, err := os.ReadFile(jsoncBackup); err != nil || string(got) != "do not overwrite\n" {
			t.Fatalf("existing backup was changed: %q, %v", got, err)
		}
		if got, err := os.ReadFile(files[1]); err != nil || string(got) != contents[1] {
			t.Fatalf("source with existing backup was changed: %q, %v", got, err)
		}
		if err := os.Remove(jsoncBackup); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(dir, "missing backup target"), jsoncBackup); err != nil {
			t.Fatal(err)
		}
		runReadableRecoveryCommand(t, otherCWD, keepTOML, false)
		if target, err := os.Readlink(jsoncBackup); err != nil || target != filepath.Join(dir, "missing backup target") {
			t.Fatalf("dangling-symlink backup was changed: %q, %v", target, err)
		}
		if got, err := os.ReadFile(files[1]); err != nil || string(got) != contents[1] {
			t.Fatalf("source with dangling-symlink backup was changed: %q, %v", got, err)
		}
		if err := os.Remove(jsoncBackup); err != nil {
			t.Fatal(err)
		}
		runReadableRecoveryCommand(t, otherCWD, keepTOML, true)
		for i, source := range files[1:] {
			backup := source + ".backup"
			if got, err := os.ReadFile(backup); err != nil || string(got) != contents[i+1] {
				t.Fatalf("recovery did not preserve %q: got %q, %v", source, got, err)
			}
			if _, err := os.Lstat(source); !os.IsNotExist(err) {
				t.Errorf("recovery left competing sibling %q active (Lstat error %v)", source, err)
			}
		}
	}

	got, err := read()
	if err != nil {
		t.Fatalf("reader still reports a sibling collision after moving both legacy files aside: %v", err)
	}
	if userScope {
		if value, ok := got.Get(expectedKey); !ok || !reflect.DeepEqual(value, expectedValue) {
			t.Fatalf("readable user config %s = %#v, want %#v", expectedKey, value, expectedValue)
		}
	} else {
		install, ok := got.Get("install")
		if !ok || install == nil {
			t.Fatalf("readable workspace config lacks install after recovery: %#v", got)
		}
	}
}

func readableCollisionRecoveryCommands(t *testing.T, err error, files []string) map[string]string {
	t.Helper()
	commands := make(map[string]string, len(files))
	for _, keep := range files {
		marker := "To keep " + keep + " active: `"
		start := strings.Index(err.Error(), marker)
		if start < 0 {
			continue
		}
		start += len(marker)
		end := strings.Index(err.Error()[start:], "`")
		if end < 0 {
			t.Fatalf("unterminated recovery command for %q in %v", keep, err)
		}
		commands[keep] = err.Error()[start : start+end]
	}
	return commands
}

func runReadableRecoveryCommand(t *testing.T, cwd, command string, wantSuccess bool) {
	t.Helper()
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = cwd
	output, err := cmd.CombinedOutput()
	if wantSuccess && err != nil {
		t.Fatalf("recovery command failed from different CWD: %v\n%s\ncommand: %s", err, output, command)
	}
	if !wantSuccess && err == nil {
		t.Fatalf("recovery command unexpectedly succeeded with occupied backup: %s", command)
	}
}

func TestReadableTOMLUnsupportedFormatOffersSafeManualRewrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "supplied-workspace")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(dir) == filepath.Clean(cwd) {
		t.Fatalf("test workspace unexpectedly equals CWD: %s", dir)
	}
	path := filepath.Join(dir, paths.WorkspaceConfigTOMLName)
	document := "format = 1\npackages = ['legacy']\n"
	write(t, path, document)
	before := snapshotReadableConfigDir(t, dir)

	_, err = LoadReadableWorkspaceConfig(dir, true, nil)
	if err == nil {
		t.Fatal("format-1 TOML was accepted")
	}
	backupCommand := shquote.Join([]string{"cp", "-p", path, path + ".backup"})
	moveAsideCommand := shquote.Join([]string{"mv", path, path + ".disabled"})
	for _, want := range []string{path, "format 1", "format 2", "backup path does not already exist", backupCommand, "manually rewrite", "TOML", "schema", "before using it", "changing or removing the format marker alone is not a conversion", "original legacy JSONC/JSON filename", moveAsideCommand, "never rename legacy bytes to `.toml`"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("unsupported-format error %q does not contain safe recovery guidance %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "yolo config migrate") || strings.Contains(err.Error(), "set format = 2") {
		t.Errorf("unsupported-format error recommends a nonexistent or marker-only conversion: %v", err)
	}
	if after := snapshotReadableConfigDir(t, dir); !reflect.DeepEqual(after, before) {
		t.Fatalf("unsupported-format refusal wrote config files: before=%v after=%v", before, after)
	}

	write(t, path, "[install]\nnix = ['just']\n")
	if _, err := LoadReadableWorkspaceConfig(dir, true, nil); err != nil {
		t.Fatalf("format-2 TOML without a marker was refused: %v", err)
	}
	write(t, path, "format = 2\n[install]\nnix = ['just']\n")
	if _, err := LoadReadableWorkspaceConfig(dir, true, nil); err != nil {
		t.Fatalf("explicit format-2 TOML was refused: %v", err)
	}
	for _, format := range []int{0, 1, 3} {
		t.Run(fmt.Sprintf("format-%d-refused", format), func(t *testing.T) {
			write(t, path, fmt.Sprintf("format = %d\n", format))
			_, err := LoadReadableWorkspaceConfig(dir, true, nil)
			if err == nil {
				t.Fatalf("format %d TOML was accepted", format)
			}
			if format > 2 && (!strings.Contains(err.Error(), "Upgrade with `yolo update`") || !strings.Contains(err.Error(), "just install")) {
				t.Fatalf("future format error = %v, want existing upgrade remedy", err)
			}
		})
	}
}

func snapshotReadableConfigDir(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := make(map[string][]byte, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			t.Fatalf("unexpected subdirectory %s in config snapshot", entry.Name())
		}
		contents, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		snapshot[entry.Name()] = contents
	}
	return snapshot
}

func TestLoadWorkspaceConfigReadableRejectsSiblingCollision(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, paths.WorkspaceConfigTOMLName), "[install]\nnix = ['just']\n")
	write(t, filepath.Join(dir, paths.WorkspaceConfigLegacyJSONCName), `{"packages":["just"]}`)
	if _, err := LoadReadableWorkspaceConfig(dir, true, nil); err == nil ||
		!strings.Contains(err.Error(), paths.WorkspaceConfigTOMLName) || !strings.Contains(err.Error(), paths.WorkspaceConfigLegacyJSONCName) {
		t.Fatalf("collision error = %v, want both sibling names", err)
	}
}

func TestLoadReadableWorkspaceConfigFormatVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, paths.WorkspaceConfigTOMLName)
	write(t, path, "format = 3\n")
	if _, err := LoadReadableWorkspaceConfig(dir, true, nil); err == nil || !strings.Contains(err.Error(), "format 3") {
		t.Fatalf("future format error = %v", err)
	}
	write(t, path, "format = 2\n[install]\nnix = ['just']\n")
	if _, err := LoadReadableWorkspaceConfig(dir, true, nil); err != nil {
		t.Fatalf("format 2 refused: %v", err)
	}
	write(t, path, "format = 1\n")
	if _, err := LoadReadableWorkspaceConfig(dir, true, nil); err == nil {
		t.Fatal("format 1 was accepted in the TOML filename")
	}
}

func mustMap(t *testing.T, m *jsonx.OrderedMap, key string) *jsonx.OrderedMap {
	t.Helper()
	out, ok := asMap(mustValue(t, m, key))
	if !ok {
		t.Fatalf("%s is not a table in %#v", key, m)
	}
	return out
}

// config.d/*.toml as `yolo config-ref` states it: read with no user config file at all (the
// absent user config is the TOML one a first write creates), never beside a legacy config.jsonc,
// each fragment's own include_if_found followed (relative to config.d/), and a later fragment's
// scalar winning over an earlier one's.
func TestConfigDFragmentsReadAsConfigRefStates(t *testing.T) {
	for _, tc := range []struct {
		name, top, body string
		want            any
	}{
		{"no user config file", "", "", "second"},
		{"a TOML user config", paths.UserConfigTOMLName, "agents_md_extra = 'top'\n", "second"},
		{"a legacy user config", paths.UserConfigLegacyJSONCName, `{"agents_md_extra": "top"}`, "top"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("YOLO_VERSION", "")
			t.Setenv(UserLayerEnv, "")
			userDir := filepath.Join(home, ".config", "yolo-jail")
			if tc.top != "" {
				write(t, filepath.Join(userDir, tc.top), tc.body)
			}
			write(t, filepath.Join(userDir, "config.d", "10-a.toml"), "include_if_found = ['parts/nix.toml']\nagents_md_extra = 'first'\n")
			write(t, filepath.Join(userDir, "config.d", "parts", "nix.toml"), "[install]\nnix = ['from-include']\n")
			write(t, filepath.Join(userDir, "config.d", "20-b.toml"), "agents_md_extra = 'second'\n")
			got, err := LoadReadableUserScopeConfig(true, nil)
			if err != nil {
				t.Fatal(err)
			}
			extra, _ := got.Get("agents_md_extra")
			if extra != tc.want {
				t.Fatalf("agents_md_extra = %#v, want %#v", extra, tc.want)
			}
			install, _ := got.Get("install")
			if tc.want == "second" {
				nix, _ := install.(*jsonx.OrderedMap).Get("nix")
				if !reflect.DeepEqual(nix, []any{"from-include"}) {
					t.Fatalf("the fragment's include was not followed: install = %#v", install)
				}
			} else if install != nil {
				t.Fatalf("a legacy user config read config.d: install = %#v", install)
			}
		})
	}
}

// RC-D54: allow.<source>.sets is judged by the sets validator brokered.<source>.sets is, at a
// launch's validation: a malformed set is refused by name, and a well-formed one launches.
func TestAllowSetsIsValidatedLikeTheLegacySets(t *testing.T) {
	for _, tc := range []struct {
		sets string
		want string
	}{
		{"[{ name = 'review', mode = 'standing' }]", ""},
		{"[{ name = 'review', mode = 'sometimes' }]", "allow.github.sets[0]"},
	} {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("YOLO_VERSION", "")
		t.Setenv(UserLayerEnv, "")
		write(t, filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName), "packs = ['github']\n[allow.github]\nsets = "+tc.sets+"\n")
		ws := t.TempDir()
		merged, err := LoadConfig(ws, true, func(string) {})
		if err != nil {
			t.Fatal(err)
		}
		errs, _ := ValidateConfig(merged, ws, nil)
		got := strings.Join(errs, "\n")
		if tc.want == "" && got != "" {
			t.Errorf("%s: a well-formed set was refused: %s", tc.sets, got)
		}
		if tc.want != "" && !strings.Contains(got, tc.want) {
			t.Errorf("%s: a malformed set was not refused by name: %q", tc.sets, got)
		}
	}
}
