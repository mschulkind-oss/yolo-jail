package macosuser

// wiretables_test.go pins row D2 of docs/plans/notch-convergence.md: every wire table the
// launch composes reaches the macos-user BOOTSTRAP env, because that env is what renders the
// pack surfaces there. The relay and its invariant both used to spell two of the three and
// drop YOLO_PROFILES, and agreed with each other while codex on this backend rendered no
// model_provider. Both now range over entrypoint.WireTables, and these tests fail if either
// goes back to a list of its own.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// codexLocalTables is a launch that selects the profile "local" at codex's CLI name, where
// "local" is a profile NAME distinct from the provider it resolves to. That distinction is
// the point: only YOLO_PROFILES says that "local" means llamacpp, so a bootstrap missing it
// cannot resolve the selection by any fallback.
func codexLocalTables() map[string]string {
	return map[string]string{
		entrypoint.ProvidersWireEnv:   `{"llamacpp":{"base_url":"http://127.0.0.1:8080/v1","models":{"default":"llama"}}}`,
		entrypoint.ProfilesWireEnv:    `{"local":{"provider":"llamacpp"}}`,
		entrypoint.UseProfilesWireEnv: `{"codex":"local"}`,
	}
}

func optsWithTables(tables map[string]string) Options {
	opts := newOpts("/Users/Shared/proj")
	opts.PackEnv = jsonx.NewOrderedMap()
	for _, k := range entrypoint.WireTables() {
		opts.PackEnv.Set(k, tables[k])
	}
	return opts
}

// bootstrapVars is the environment the bootstrap argv hands `yolo internal darwin-bootstrap`:
// every K=V between `env -i` and the staged binary.
func bootstrapVars(t *testing.T, argv []string) map[string]string {
	t.Helper()
	vars := map[string]string{}
	in := false
	for _, a := range argv {
		if a == "-i" {
			in = true
			continue
		}
		if !in {
			continue
		}
		k, v, ok := strings.Cut(a, "=")
		if !ok {
			break
		}
		vars[k] = v
	}
	if len(vars) == 0 {
		t.Fatalf("no env -i pairs on the bootstrap argv %v", argv)
	}
	return vars
}

// Every table, by the one list, reaches the bootstrap argv verbatim.
func TestEveryWireTableReachesTheBootstrapEnv(t *testing.T) {
	tables := codexLocalTables()
	plan := buildPlan(mockDeps(nil), optsWithTables(tables), nil)
	vars := bootstrapVars(t, plan.BootstrapArgv)
	for _, k := range entrypoint.WireTables() {
		if got, ok := vars[k]; !ok || got != tables[k] {
			t.Errorf("%s reached the bootstrap env as %q (present %v), want %q — the native "+
				"bootstrap renders the pack surfaces from it", k, got, ok, tables[k])
		}
	}
	if problems := PlanInvariants(plan); len(problems) > 0 {
		t.Errorf("a plan carrying every table must satisfy every invariant: %v", problems)
	}
}

// The invariant sees each table's relay by itself: strip one table from the bootstrap argv
// and PlanInvariants names it. YOLO_PROFILES is the one the old hand-spelled list missed.
func TestPlanInvariantsNamesEachDroppedWireTable(t *testing.T) {
	for _, dropped := range entrypoint.WireTables() {
		t.Run(dropped, func(t *testing.T) {
			plan := buildPlan(mockDeps(nil), optsWithTables(codexLocalTables()), nil)
			var kept []string
			for _, a := range plan.BootstrapArgv {
				if !strings.HasPrefix(a, dropped+"=") {
					kept = append(kept, a)
				}
			}
			plan.BootstrapArgv = kept
			var named bool
			for _, p := range PlanInvariants(plan) {
				if strings.HasPrefix(p, dropped+" is in the launch env but not baked into the bootstrap env") {
					named = true
				}
			}
			if !named {
				t.Errorf("PlanInvariants did not name %s missing from the bootstrap env: %v",
					dropped, PlanInvariants(plan))
			}
		})
	}
}

// THE DONE-WHEN of plan item 19: codex on macos-user renders its model_provider. Drives the
// real embedded codex pack through the boot render (entrypoint.ConfigurePackSurfaces, the
// step RunDarwinBootstrap runs) over exactly the environment BuildRunPlan hands the
// bootstrap, read back from the argv. Without the YOLO_PROFILES relay the selection "local"
// names a profile the bootstrap has no table for, and config.toml carries no model_provider.
func TestCodexOnMacosUserRendersItsModelProvider(t *testing.T) {
	var codex *packload.Pack
	for _, p := range packload.Embedded() {
		if p.Name == "codex" {
			codex = p
		}
	}
	if codex == nil {
		t.Fatal("no embedded codex pack")
	}
	ctx := t.TempDir()
	t.Setenv("YOLO_CTX_ROOT", ctx)
	if err := os.MkdirAll(filepath.Join(ctx, "host-codex"), 0o755); err != nil {
		t.Fatal(err)
	}

	plan := buildPlan(mockDeps(nil), optsWithTables(codexLocalTables()), nil)
	home := t.TempDir()
	e := entrypoint.DarwinEnvFrom(bootstrapVars(t, plan.BootstrapArgv), home)
	e.Workspace = t.TempDir()
	var errw bytes.Buffer
	e.Stderr = &errw

	entrypoint.ConfigurePackSurfaces(e, []*packload.Pack{codex})
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("boot render failed: %v\n%s", fails, errw.String())
	}
	body, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("codex config.toml not rendered: %v\n%s", err, errw.String())
	}
	if !strings.Contains(string(body), `model_provider = "llamacpp"`) {
		t.Errorf("codex on macos-user rendered no model_provider for the selected profile:\n%s", body)
	}
}
