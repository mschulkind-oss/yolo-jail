package run

// workspaceconfigname_test.go pins that the launch's own readers of the workspace config file
// read the file the loader reads. config.LoadWorkspaceConfig takes `yolo-jail.jsonc`, or
// `yolo-jail.json` where only that exists (config.ResolveWorkspaceConfigPath); two launch
// readers joined the `.jsonc` name themselves, so a workspace configured in `yolo-jail.json`
// got no `workspace_readonly` lock on its config and no same-file preset/null check, while its
// keys were honored everywhere else.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// assembleReadonlyWorkspace assembles a podman launch in ws whose config sets
// `workspace_readonly: ["vendored"]`, and returns the argv.
func assembleReadonlyWorkspace(t *testing.T, ws string) []string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions(ws, home)
	var buf bytes.Buffer
	o.Stdout = &buf
	o.Stderr = &buf
	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	return o.assembleRunCmd(&assembleInput{
		cfg: newConfig("agents", []any{"claude"}, "security", sec,
			"workspace_readonly", []any{"vendored"}),
		rt:           "podman",
		cname:        "yolo-ws-abcd1234",
		packs:        claudePackFixture(t),
		agentsPath:   "/agents/yolo-ws-abcd1234",
		wsState:      "/ws/.yolo/home",
		miseStore:    "/mise-store",
		yoloVersion:  "9.9.9-test",
		mountTargets: map[string]struct{}{},
	})
}

// workspaceROBinds is every `-v <src>:/workspace/<rel>:ro` value in argv.
func workspaceROBinds(argv []string) []string {
	var out []string
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "-v" && strings.Contains(argv[i+1], ":/workspace/") &&
			strings.HasSuffix(argv[i+1], ":ro") {
			out = append(out, argv[i+1])
		}
	}
	return out
}

// `workspace_readonly` locks the workspace config the launch READ, under the name it was read
// under — `yolo-jail.json` where that is the file — so the agent cannot rewrite the config the
// next launch reads. Driven through assembleRunCmd, the lock's call site.
func TestWorkspaceReadonlyLocksTheConfigFileTheLoaderReads(t *testing.T) {
	for _, name := range []string{"yolo-jail.jsonc", "yolo-jail.json"} {
		t.Run(name, func(t *testing.T) {
			ws, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(ws, name), []byte(`{}`), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(ws, "vendored"), 0o755); err != nil {
				t.Fatal(err)
			}
			binds := workspaceROBinds(assembleReadonlyWorkspace(t, ws))
			want := filepath.Join(ws, name) + ":/workspace/" + name + ":ro"
			found := false
			for _, b := range binds {
				if b == want {
					found = true
				}
			}
			if !found {
				t.Errorf("workspace_readonly did not lock the config the launch reads (want %q); "+
					"read-only binds: %q", want, binds)
			}
		})
	}
}

// The same-file preset/null refusal reads the workspace config the loader read: a
// `yolo-jail.json` that enables a preset and null-removes it is refused, at its own line.
func TestLaunchPresetNullRefusalReadsTheJSONWorkspaceConfig(t *testing.T) {
	refusalHome(t)
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeAt(t, filepath.Join(ws, "yolo-jail.json"),
		"{\n  \"mcp_presets\": [\"chrome-devtools\"],\n  \"mcp_servers\": {\"chrome-devtools\": null}\n}\n")
	var stdout, stderr bytes.Buffer
	Run(*capabilityGateOptions(t, ws, nil, &stdout, &stderr))
	want := filepath.Join(ws, "yolo-jail.json") + ":3:38: preset 'chrome-devtools' is enabled"
	if out := stdout.String() + stderr.String(); !strings.Contains(out, want) {
		t.Errorf("a preset yolo-jail.json null-removes was not refused at its line (want %q):\n%s",
			want, out)
	}
}
