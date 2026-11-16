package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

func TestPackLintChecksScopedModelsAudience(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"scoped", "only = [\"alpha\"]\nagents = [\"claude\"]", true},
		{"empty", "only = [\"alpha\"]\nagents = []", false},
		{"duplicate", "only = [\"alpha\"]\nagents = [\"claude\",\"claude\"]", false},
		{"add", "add = [{ id = \"alpha\", vendor = \"acme\" }]\nagents = [\"claude\"]", false},
		{"singular", "only = [\"alpha\"]\nagent = \"claude\"", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "pack.toml"), "name = \"policy\"\ndescription = \"fixture\"\n[[models]]\nprovider = \"gw\"\n"+tc.body+"\n")
			var out, errw bytes.Buffer
			rc := packMain([]string{"lint", dir}, &out, &errw, false)
			if (rc == 0) != tc.valid {
				t.Fatalf("lint rc=%d valid=%v: %s%s", rc, tc.valid, out.String(), errw.String())
			}
		})
	}
}

func TestHostLaunchSelectionUsesPiScopedModelsWithExcludedDefault(t *testing.T) {
	t.Setenv(entrypoint.NoLaunchFlagsEnv, "")
	var results []map[string]any
	for _, audience := range []string{"agents = [\"pi\"]", ""} {
		home := hostGateHome(t, `packs = ["pi"]
[active_provider]
pi = "gw"
`, nil)
		writeFile(t, filepath.Join(home, ".config", "yolo-jail", "local", "pack.toml"), `name = "local"
description = "synthetic selection"
[[provider]]
name = "gw"
models = { default = "beta-2", alpha = "alpha-1", beta = "beta-2" }
endpoints.openai = { base_url = "https://fixture.invalid/v1", wire_api = "openai-chat-completions" }
[[models]]
provider = "gw"
only = ["alpha-1"]
`+audience+"\n")
		hostSelectionRunAtExec(t, []string{"-p", "pi=gw"}, "pi", func(env map[string]string) {
			dir := env["PI_CODING_AGENT_DIR"]
			if dir == "" {
				t.Fatal("host launch did not hand Pi its selected session home")
			}
			data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
			if err != nil {
				t.Fatal(err)
			}
			var settings map[string]any
			if err := json.Unmarshal(data, &settings); err != nil {
				t.Fatal(err)
			}
			if settings["defaultProvider"] != "gw" || settings["defaultModel"] != "alpha-1" {
				t.Errorf("host selection escaped view: %v", settings)
			}
			if env["YOLO_MODEL_DEFAULT"] != "" {
				t.Errorf("host role escaped view: %q", env["YOLO_MODEL_DEFAULT"])
			}
			results = append(results, settings)
		})
	}
	if !reflect.DeepEqual(results[0], results[1]) {
		t.Errorf("host scoped/unscoped selections differ: %v", results)
	}
}

func TestConfigEditorAcceptsLegacyProfilesWithScopedModels(t *testing.T) {
	project := testconfig.Workspace(t, "machine = {}\n")
	user := testconfig.UserFormat(t, configdoc.JSONC, `{"packs":["pi","openrouter"],"profiles":{"mine":{"provider":"openrouter"}}}`)
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	writeFile(t, filepath.Join(user.Root, ".config", "yolo-jail", "local", "pack.toml"), `name = "local"
description = "fixture"
[[models]]
provider = "openrouter"
only = ["synthetic/model"]
agents = ["pi"]
`)
	for _, args := range [][]string{
		{"set", "machine.resources.cpus", "2"},
		{"add", "env.unset", `"SYNTHETIC"`},
		{"unset", "env"},
	} {
		rc, out, errw := runConfigEditCapture(t, project.Root, args)
		if rc != 0 {
			t.Fatalf("config %v rc=%d: %s%s", args, rc, out, errw)
		}
	}
}
