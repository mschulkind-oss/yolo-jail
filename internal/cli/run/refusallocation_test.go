package run

// refusallocation_test.go pins the launch's config gate naming WHERE a refused key was written
// (userguide/reference/configuration.md#the-config-files). The maintainer's
// case: a refusal such as the retired `use_profiles` named the key and nothing else, while the
// config it came from is composed from many files — includes, a --user-layer, the workspace
// config and its local override. So each case writes the key into ONE of those files, at a known
// line, and asserts the refusal the launch prints names that file, line and column.
//
// Driven through Run(), not through config's own helpers: the location is attached at the call
// site (loadAndValidateConfig), and a test of the helper alone passes with that call deleted.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// refusalHome is a scratch HOME, resolved where it is minted (AGENTS.md's darwin rule), with
// the user config dir made.
func refusalHome(t *testing.T) (home, cfgDir string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv(config.UserLayerEnv, "")
	cfgDir = filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return home, cfgDir
}

func writeAt(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// launchRefusal runs a launch in ws and returns what the config gate printed.
func launchRefusal(t *testing.T, ws string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if rc := Run(*capabilityGateOptions(t, ws, nil, &stdout, &stderr)); rc == 0 {
		t.Fatalf("the launch accepted a config carrying use_profiles:\n%s%s", stdout.String(), stderr.String())
	}
	return stdout.String() + stderr.String()
}

// The retired key is written in a file the user config INCLUDES. The refusal names the
// included file, as the user would type it (~ for the home), and the line and column the
// key's value starts at.
func TestLaunchRefusalNamesTheIncludedFileAndLine(t *testing.T) {
	_, cfgDir := refusalHome(t)
	writeAt(t, filepath.Join(cfgDir, "config.jsonc"), `{
  // the machine-local half lives in its own file
  "include_if_found": ["profiles.jsonc"],
}`)
	writeAt(t, filepath.Join(cfgDir, "profiles.jsonc"), `{
  "providers": {},
  "use_profiles": {"claude": "zai"},
}`)
	out := launchRefusal(t, t.TempDir())
	want := "~/.config/yolo-jail/profiles.jsonc:3:19: config.use_profiles: RENAMED"
	if !strings.Contains(out, want) {
		t.Errorf("the refusal does not say where use_profiles was written (want %q):\n%s", want, out)
	}
}

// The same key written in a --user-layer file: the refusal names the layer.
func TestLaunchRefusalNamesTheUserLayerFileAndLine(t *testing.T) {
	home, _ := refusalHome(t)
	layer := filepath.Join(home, "layers", "dev.jsonc")
	if err := os.MkdirAll(filepath.Dir(layer), 0o755); err != nil {
		t.Fatal(err)
	}
	writeAt(t, layer, "{\n\n    \"use_profiles\": \"zai\"\n}\n")
	t.Setenv(config.UserLayerEnv, layer)
	out := launchRefusal(t, t.TempDir())
	want := "~/layers/dev.jsonc:3:21: config.use_profiles: RENAMED"
	if !strings.Contains(out, want) {
		t.Errorf("the refusal does not name the --user-layer file (want %q):\n%s", want, out)
	}
}

// The key in the workspace's own config draws two refusals, the rename and the scope, and each
// names the workspace file and line.
func TestLaunchRefusalNamesTheWorkspaceFileAndLine(t *testing.T) {
	refusalHome(t)
	ws := t.TempDir()
	writeAt(t, filepath.Join(ws, "yolo-jail.jsonc"), "{\n  \"packages\": [],\n  \"use_profiles\": \"zai\"\n}\n")
	out := launchRefusal(t, ws)
	for _, want := range []string{
		"yolo-jail.jsonc:3:19: config.use_profiles: RENAMED",
		"yolo-jail.jsonc:3:19: config.use_profiles: user-scope only",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not name the workspace file (want %q):\n%s", want, out)
		}
	}
}

// A key written in two files names both: the one whose value the merge kept first (the
// include, which wins over the file including it), then the other.
func TestLaunchRefusalNamesEveryFileTheKeyIsWrittenIn(t *testing.T) {
	_, cfgDir := refusalHome(t)
	writeAt(t, filepath.Join(cfgDir, "config.jsonc"), `{
  "use_profiles": "bedrock",
  "include_if_found": ["local.jsonc"],
}`)
	writeAt(t, filepath.Join(cfgDir, "local.jsonc"), "{\"use_profiles\": \"zai\"}")
	out := launchRefusal(t, t.TempDir())
	line := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "config.use_profiles: RENAMED") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no use_profiles refusal:\n%s", out)
	}
	if !strings.Contains(line, "~/.config/yolo-jail/local.jsonc:1:18: config.use_profiles") {
		t.Errorf("the refusal does not lead with the include, whose value won:\n%s", line)
	}
	if !strings.Contains(line, "also written at ~/.config/yolo-jail/config.jsonc:2:19") {
		t.Errorf("the refusal does not name the other file the key is written in:\n%s", line)
	}
}

// The capability gate prints its refusal in a sentence of its own, so the location is a line
// of its own under it: where `required_capabilities` is written.
func TestCapabilityRefusalSaysWhereTheRequirementIsWritten(t *testing.T) {
	ws := capabilityWorkspace(t, "{\n  \"required_capabilities\": [\"web_search\"]\n}")
	var stdout, stderr bytes.Buffer
	if rc := Run(*capabilityGateOptions(t, ws, nil, &stdout, &stderr)); rc != 1 {
		t.Fatalf("Run() = %d, want 1\n%s%s", rc, stdout.String(), stderr.String())
	}
	want := "config.required_capabilities is written at " + filepath.Join(ws, "yolo-jail.jsonc") + ":2:28."
	if !strings.Contains(stderr.String(), want) {
		t.Errorf("the capability refusal does not say where the requirement is written (want %q):\n%s",
			want, stderr.String())
	}
}

// A WARNING the config gate prints is located as its refusals are: the retired `repo_path` is
// ignored with a warning, written here in an include beside a refusal that stops the launch.
func TestLaunchWarningNamesTheFileAndLine(t *testing.T) {
	_, cfgDir := refusalHome(t)
	writeAt(t, filepath.Join(cfgDir, "config.jsonc"), `{"include_if_found": ["old.jsonc"], "use_profiles": "zai"}`)
	writeAt(t, filepath.Join(cfgDir, "old.jsonc"), "{\n  \"repo_path\": \"/src/yolo\"\n}\n")
	out := launchRefusal(t, t.TempDir())
	want := "~/.config/yolo-jail/old.jsonc:2:16: config.repo_path: ignored"
	if !strings.Contains(out, want) {
		t.Errorf("the warning does not say where repo_path was written (want %q):\n%s", want, out)
	}
}

// The same-file preset/null contradiction, which the gate checks per file rather than over the
// merged config, leads with where the null entry is written.
func TestLaunchPresetNullRefusalNamesTheNullsLine(t *testing.T) {
	refusalHome(t)
	ws := t.TempDir()
	writeAt(t, filepath.Join(ws, "yolo-jail.jsonc"),
		"{\n  \"mcp_presets\": [\"chrome-devtools\"],\n  \"mcp_servers\": {\"chrome-devtools\": null}\n}\n")
	var stdout, stderr bytes.Buffer
	if rc := Run(*capabilityGateOptions(t, ws, nil, &stdout, &stderr)); rc == 0 {
		t.Fatalf("the launch accepted a preset the same file null-removes:\n%s%s", stdout.String(), stderr.String())
	}
	want := filepath.Join(ws, "yolo-jail.jsonc") + ":3:38: preset 'chrome-devtools' is enabled"
	if out := stdout.String() + stderr.String(); !strings.Contains(out, want) {
		t.Errorf("the refusal does not lead with where the null is written (want %q):\n%s", want, out)
	}
}
