package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestLoadConfigWithReadInputOverridesThePhysicalWorkspaceFileInTheRealGraph(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws := t.TempDir()
	userDir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(userDir, paths.UserConfigTOMLName), "include_if_found = ['parts/user.toml']\n[install]\nnix = ['user']\n")
	write(t, filepath.Join(userDir, "parts", "user.toml"), "[machine.resources]\nmemory = '8g'\n")
	write(t, filepath.Join(userDir, "config.d", "10-extra.toml"), "[install]\nmise = { python = '3.12' }\n")
	write(t, filepath.Join(ws, WorkspaceTOMLConfigName), "include_if_found = ['parts/project.toml']\n[machine.resources]\ncpus = 4\n")
	write(t, filepath.Join(ws, "parts", "project.toml"), "[machine.resources]\nmemory = '16g'\n")

	physical := filepath.Join(ws, "physical-local.toml")
	write(t, physical, "[machine.resources]\ncpus = 2\n")
	alias := filepath.Join(ws, WorkspaceLocalTOMLConfigName)
	if err := os.Symlink(physical, alias); err != nil {
		t.Fatal(err)
	}
	planned := []byte("[machine.resources]\ncpus = 12\n")
	input, err := NewConfigReadInput(physical, planned)
	if err != nil {
		t.Fatal(err)
	}
	planned[0] = 'x' // The invocation owns a defensive copy.

	_, used, err := LoadConfigWithReadInputResult(ws, true, nil, input)
	if err != nil || !used {
		t.Fatalf("explicit physical target consumption = %v, err=%v; want consumed", used, err)
	}
	got, src, err := LoadConfigWithReadInputAndSources(ws, true, nil, input)
	if err != nil {
		t.Fatal(err)
	}
	resources := nestedValue(t, got, "machine", "resources")
	if value, _ := resources.Get("cpus"); !hasInt(value, 12) {
		t.Fatalf("substituted cpus = %#v, want 12", value)
	}
	if _, ok := resources.Get("memory"); !ok {
		t.Errorf("effective graph lost unmodified include memory: %#v", resources)
	}
	install := nestedValue(t, got, "install")
	for _, key := range []string{"nix", "mise"} {
		if _, ok := install.Get(key); !ok {
			t.Errorf("effective graph lost install.%s from user/config.d readers: %#v", key, install)
		}
	}
	locations := src.Locations("config.machine.resources.cpus")
	if len(locations) == 0 || !strings.Contains(strings.Join(locations, "\n"), filepath.Base(alias)) {
		t.Fatalf("source path did not preserve the authored symlink path %q: %q", alias, locations)
	}

	ordinary, err := LoadConfig(ws, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	ordinaryResources := nestedValue(t, ordinary, "machine", "resources")
	if value, _ := ordinaryResources.Get("cpus"); !hasInt(value, 2) {
		t.Fatalf("unrelated LoadConfig invocation observed substitution: cpus=%#v", value)
	}
}

func TestLoadConfigWithReadInputReachesUserFragmentsAndNestedIncludes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws := t.TempDir()
	userDir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(userDir, paths.UserConfigTOMLName), "include_if_found = ['parts/user.toml']\n[env.set]\nREADER_INPUT_TEST = 'base'\n")
	write(t, filepath.Join(userDir, "parts", "user.toml"), "[install]\nnix = ['user']\n")
	fragment := filepath.Join(userDir, "config.d", "10-extra.toml")
	write(t, fragment, "[install.mise]\npython = '3.12'\n")
	write(t, filepath.Join(ws, WorkspaceTOMLConfigName), "include_if_found = ['parts/project.toml']\n[machine.resources]\ncpus = 2\n")
	projectInclude := filepath.Join(ws, "parts", "project.toml")
	write(t, projectInclude, "[machine.resources]\nmemory = '16g'\n")

	for _, tc := range []struct {
		name, target, original, planned, path string
		want                                  any
	}{
		{name: "user base", target: filepath.Join(userDir, paths.UserConfigTOMLName), original: "base", planned: "changed", path: "env.set.READER_INPUT_TEST", want: "changed"},
		{name: "user config.d", target: fragment, original: "3.12", planned: "3.13", path: "install.mise.python", want: "3.13"},
		{name: "nested project include", target: projectInclude, original: "16g", planned: "24g", path: "machine.resources.memory", want: "24g"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := os.ReadFile(tc.target)
			if err != nil {
				t.Fatal(err)
			}
			input, err := NewConfigReadInput(tc.target, []byte(strings.Replace(string(body), tc.original, tc.planned, 1)))
			if err != nil {
				t.Fatal(err)
			}
			got, used, err := LoadConfigWithReadInputResult(ws, true, nil, input)
			if err != nil || !used {
				t.Fatalf("%s target consumption = %v, err=%v; want consumed", tc.name, used, err)
			}
			value := configPathValue(t, got, tc.path)
			if value != tc.want {
				t.Fatalf("effective %s = %#v, want %#v", tc.path, value, tc.want)
			}
		})
	}
}

func configPathValue(t *testing.T, cfg *jsonx.OrderedMap, path string) any {
	t.Helper()
	var current any = cfg
	for _, key := range strings.Split(path, ".") {
		m, ok := current.(*jsonx.OrderedMap)
		if !ok {
			t.Fatalf("%s is not a map: %#v", key, current)
		}
		var found bool
		current, found = m.Get(key)
		if !found {
			t.Fatalf("missing %s in %#v", key, cfg)
		}
	}
	return current
}

func TestLoadConfigWithReadInputUsesTheLegacyJSONCReader(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws := t.TempDir()
	include := filepath.Join(ws, "part.jsonc")
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"include_if_found":["part.jsonc"]}`)
	write(t, include, `{"env":{"set":{"READER_INPUT_JSONC":"before"}}}`)
	input, err := NewConfigReadInput(include, []byte(`{"env":{"set":{"READER_INPUT_JSONC":"after"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	got, used, err := LoadConfigWithReadInputResult(ws, true, nil, input)
	if err != nil || !used {
		t.Fatalf("legacy JSONC target consumption = %v, err=%v; want consumed", used, err)
	}
	if value := configPathValue(t, got, "env.set.READER_INPUT_JSONC"); value != "after" {
		t.Fatalf("legacy JSONC included value = %#v, want after", value)
	}
}

func TestLoadConfigWithReadInputDoesNotIntroduceAnUnreadTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws := t.TempDir()
	write(t, filepath.Join(ws, WorkspaceTOMLConfigName), "[machine.resources]\ncpus = 3\n")
	unread := filepath.Join(ws, "not-in-config-graph.toml")
	write(t, unread, "[install]\nnix = ['original']\n")
	input, err := NewConfigReadInput(unread, []byte("[install]\nnix = ['must-not-appear']\n"))
	if err != nil {
		t.Fatal(err)
	}
	got, used, err := LoadConfigWithReadInputResult(ws, true, nil, input)
	if err != nil || used {
		t.Fatalf("unreferenced target consumption = %v, err=%v; want not consumed", used, err)
	}
	if _, exists := got.Get("install"); exists {
		t.Fatalf("explicit target bypassed the normal reader graph: %#v", got)
	}
}

func TestNewConfigReadInputRequiresAnAbsolutePhysicalPath(t *testing.T) {
	if _, err := NewConfigReadInput("relative.toml", []byte("[install]\nnix=[]\n")); err == nil {
		t.Fatal("relative input target was accepted")
	}
}

func TestLoadConfigWithReadInputRetainsWorkspaceScopeChecks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws := t.TempDir()
	path := filepath.Join(ws, WorkspaceTOMLConfigName)
	write(t, path, "[install]\nnix = ['ordinary']\n")
	input, err := NewConfigReadInput(path, []byte("packs = ['ordinary']\n[install]\nnix = ['ordinary']\n"))
	if err != nil {
		t.Fatal(err)
	}
	_, used, err := LoadConfigWithReadInputResult(ws, true, nil, input)
	if err == nil || !strings.Contains(err.Error(), "user-scope only") || !used {
		t.Fatalf("substitution/scope result = (consumed=%v, err=%v); want consumed bytes then normal scope refusal", used, err)
	}
}

func TestLoadConfigWithReadInputBypassesOnlyTheOwnWorkspaceSnapshotShortcut(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "reader-input-test")
	t.Setenv("YOLO_USER_LAYER", "")
	ws := t.TempDir()
	t.Setenv("YOLO_WORKSPACE", ws)
	t.Setenv("YOLO_SETUP", "podman-self")
	path := filepath.Join(ws, WorkspaceTOMLConfigName)
	write(t, path, "[install]\nnix = ['live']\n")
	write(t, WorkspaceAssembledConfigPath(ws, "podman-self"), `{"install":{"nix":["assembled"]}}`)

	ordinary, err := LoadConfig(ws, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	ordinaryInstall := nestedValue(t, ordinary, "install")
	if value, _ := ordinaryInstall.Get("nix"); !hasString(value, "assembled") {
		t.Fatalf("control did not exercise own-workspace assembled shortcut: %#v", value)
	}

	input, err := NewConfigReadInput(path, []byte("[install]\nnix = ['edited']\n"))
	if err != nil {
		t.Fatal(err)
	}
	edited, err := LoadConfigWithReadInput(ws, true, nil, input)
	if err != nil {
		t.Fatal(err)
	}
	install := nestedValue(t, edited, "install")
	if value, _ := install.Get("nix"); !hasString(value, "edited") {
		t.Fatalf("explicit read-input invocation did not recompose the file: %#v", value)
	}
	unsupported, err := NewConfigReadInput(WorkspaceAssembledConfigPath(ws, "podman-self"), []byte(`{"install":{"nix":["not-a-reader-input"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, used, err := LoadConfigWithReadInputResult(ws, true, nil, unsupported); err != nil || used {
		t.Fatalf("assembled-artifact input consumption = %v, err=%v; want unsupported/not consumed", used, err)
	}
}

func nestedValue(t *testing.T, config *jsonx.OrderedMap, keys ...string) *jsonx.OrderedMap {
	t.Helper()
	var current any = config
	for _, key := range keys {
		m, ok := current.(*jsonx.OrderedMap)
		if !ok {
			t.Fatalf("%s is not a map: %#v", key, current)
		}
		var exists bool
		current, exists = m.Get(key)
		if !exists {
			t.Fatalf("missing %s in config %#v", key, config)
		}
	}
	result, ok := current.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("nested value is not a map: %#v", current)
	}
	return result
}

func hasInt(value any, want int64) bool {
	got, ok := jsonx.AsInt(value)
	return ok && got == want
}

func hasString(values any, want string) bool {
	items, ok := values.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
