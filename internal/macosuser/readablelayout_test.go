package macosuser

// readablelayout_test.go pins the macos-user readers of the settings the readable config layout
// moved (docs/reference/config/rulings.md): `resources` → `machine.resources` and `mise_tools` →
// `install.mise`. Since that cutover the LOADED config carries only the new spelling, whichever
// format the file was written in, and every macos-user reader that still asked for the old
// top-level key found nothing: no cooperative cpus, no I/O priority, no memory guard, and no
// provisioning stage, so declared mise tools never arrived. The other tests in this package hand
// buildPlan a hand-made legacy map, which is why they stayed green; these go through the loader a
// launch uses (config.LoadConfigWithSources, run's loadAndValidateConfig) for both formats.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// readableLayoutFixtures are the same declarations in both formats: the legacy JSONC workspace
// file with top-level `resources` and `mise_tools`, and the TOML one with `machine.resources`
// and `install.mise`.
var readableLayoutFixtures = []struct {
	name, file, body string
}{
	{"legacy jsonc", "yolo-jail.jsonc", `{
  "resources": {"cpus": 3, "memory": "256m", "io": "idle"},
  "mise_tools": {"neovim": "nightly"}
}`},
	{"toml", "yolo-jail.toml", `[machine.resources]
cpus = 3
memory = "256m"
io = "idle"

[install.mise]
neovim = "nightly"
`},
}

// loadWorkspaceConfig writes body to a fresh workspace and loads it exactly as a launch does,
// with an empty user scope so nothing of the host's leaks in.
func loadWorkspaceConfig(t *testing.T, file, body string) *jsonx.OrderedMap {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_USER_LAYER", "")
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, file), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.LoadConfigWithSources(ws, true, func(string) {})
	if err != nil {
		t.Fatalf("loading %s: %v", file, err)
	}
	if errs, _ := config.ValidateConfig(cfg, ws, nil); len(errs) != 0 {
		t.Fatalf("%s does not validate: %v", file, errs)
	}
	return cfg
}

// TestTheLoadedConfigsResourcesReachEveryMacosUserReader: cpus (orchestrator's cooperative
// parallelism defaults), the I/O priority and the memory guard (buildPlan) all see the declared
// `machine.resources`, from either format. Reverting either reader to the top-level `resources`
// key fails this.
func TestTheLoadedConfigsResourcesReachEveryMacosUserReader(t *testing.T) {
	for _, fx := range readableLayoutFixtures {
		t.Run(fx.name, func(t *testing.T) {
			cfg := loadWorkspaceConfig(t, fx.file, fx.body)
			if _, legacy := cfg.Get("resources"); legacy {
				t.Fatalf("the loaded config still carries a top-level resources, so this test "+
					"cannot tell the readers apart: %s", jsonx.MustDumpsCompact(cfg))
			}

			var buf bytes.Buffer
			deps := mockDeps(nil)
			deps.Out = &buf
			deps.HostCPUs = func() int { return 8 }
			opts := newOpts("/Users/Shared/proj")
			opts.Config = cfg
			plan := buildPlan(deps, opts, nil)

			for _, k := range CooperativeCPUVars {
				if v, _ := sandboxEnvFileValue(plan.EnvFileContent, k); v != "3" {
					t.Errorf("cpus 3: %s = %q, want 3\n%s", k, v, buf.String())
				}
			}
			if !strings.Contains(buf.String(), "resources.cpus (3) is honored cooperatively") {
				t.Errorf("no cooperative-cpus disclosure:\n%s", buf.String())
			}
			if plan.IOPriority != ioprio.Idle {
				t.Errorf("IOPriority = %q, want %q", plan.IOPriority, ioprio.Idle)
			}
			if plan.SessionGuard.MemoryBytes != 256<<20 {
				t.Errorf("SessionGuard = %+v, want 256m", plan.SessionGuard)
			}
		})
	}
}

// TestTheLoadedConfigsMiseToolsStartTheProvisioningStage: ProvisionNeeded sees the declared
// `install.mise`, so the plan carries a provisioning stage, and the bootstrap is handed the tool.
// Reverting config.MergeMiseTools (or JailMiseTools) to the top-level `mise_tools` fails this.
func TestTheLoadedConfigsMiseToolsStartTheProvisioningStage(t *testing.T) {
	for _, fx := range readableLayoutFixtures {
		t.Run(fx.name, func(t *testing.T) {
			cfg := loadWorkspaceConfig(t, fx.file, fx.body)
			if _, legacy := cfg.Get("mise_tools"); legacy {
				t.Fatalf("the loaded config still carries a top-level mise_tools: %s",
					jsonx.MustDumpsCompact(cfg))
			}
			if !ProvisionNeeded(cfg, FloorStage{}) {
				t.Fatal("ProvisionNeeded = false for a config declaring install.mise")
			}
			opts := newOpts("/Users/Shared/proj")
			opts.Config = cfg
			plan := buildPlan(mockDeps(nil), opts, nil)
			if len(plan.ProvisionArgv) == 0 {
				t.Error("the plan has no provisioning stage although install.mise declares a tool")
			}
			if !anyHasPrefix(plan.BootstrapArgv, "YOLO_MISE_TOOLS={") || !anyContains(plan.BootstrapArgv, `"neovim"`) {
				t.Errorf("the bootstrap is not handed the declared tool: %v", plan.BootstrapArgv)
			}
		})
	}
}
