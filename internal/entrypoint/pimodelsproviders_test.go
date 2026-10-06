package entrypoint

// pimodelsproviders_test.go pins HC-D1 (docs/design/host-computed-layer.md §7): every
// ~/.pi/agent/models.json yolo writes carries an object-valued `providers`.
//
// pi 0.87.1's ModelsConfigSchema REQUIRES `providers` (dist/core/model-config.js), so a
// models.json of `{}` fails its schema check, pi prints "models.json error: …" at every start
// and runs with no custom providers. Two renders wrote exactly that before the default: a
// jail whose only agent pack is pi (its catalog never writes the built-in openai-codex, so a
// pi-only jail has no row, and the derive returns {}), and `yolo host apply --assert` into a
// home with no models.json (the host runs no derive for content, and the surface declared no
// layer). The fix is one manifest default, `"defaults": {"providers": {}}` on pi/models, and
// these tests reach it only through the two production entries that render the surface —
// ConfigurePackSurfaces (the boot loop) and RenderHostPack (`yolo host apply`) — so deleting
// the default, or either entry's use of the surface's layers, fails them.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// piModelsPath is where pi reads its model config, under the given home.
func piModelsPath(home string) string {
	return filepath.Join(home, ".pi", "agent", "models.json")
}

// requireObjectProviders fails unless the file at path decodes to a JSON object whose
// `providers` is itself an object — the shape pi's schema requires. It returns that object
// so a caller can say more about what it holds.
func requireObjectProviders(t *testing.T, path, what string) map[string]any {
	t.Helper()
	raw, _ := os.ReadFile(path)
	doc := decodeJSONFile(t, path)
	v, present := doc["providers"]
	if !present {
		t.Fatalf("%s: models.json has no `providers`, which pi 0.87.1's ModelsConfigSchema "+
			"requires (it prints \"models.json error\" at every start):\n%s", what, raw)
	}
	providers, isObj := v.(map[string]any)
	if !isObj {
		t.Fatalf("%s: models.json `providers` is %T, want an object:\n%s", what, v, raw)
	}
	return providers
}

// bootPiOnlyJail runs the boot render of a jail whose only agent pack is pi — pi plus the
// packs its `needs` close over — with the provider table that pack set composes, into home.
func bootPiOnlyJail(t *testing.T, home string) {
	t.Helper()
	packs := testPacksForAgent(t, "pi")
	providers, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	e := &Env{Home: home, Workspace: t.TempDir(),
		Vars: map[string]string{"YOLO_PROVIDERS": mustCompactJSON(t, providers)}}
	withCtxRoot(t, t.TempDir(), "pi")
	ConfigurePackSurfaces(e, packs)
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("pi-only boot render failed: %v", fails)
	}
}

// A PI-ONLY JAIL writes `providers`. The composed table is not empty — packs/openai-auth ships
// openai-codex — which is what makes the case sharp: the table arrives and the catalog still
// writes no row, because openai-codex is pi's built-in provider and is never catalogued.
func TestAPiOnlyJailWritesAModelsFileWithProviders(t *testing.T) {
	home := t.TempDir()
	bootPiOnlyJail(t, home)
	if providers := requireObjectProviders(t, piModelsPath(home), "pi-only jail"); len(providers) != 0 {
		t.Errorf("a pi-only jail catalogued %v — openai-codex is pi's built-in provider and "+
			"must never get a models.json row (pi-codex-provider-shadowing OQ-1/OQ-2)", providers)
	}
}

// A models.json of `{}` LEFT BY AN EARLIER BOOT is repaired by the next one. The surface is
// computed, so every boot recomposes it from its layers and never reads the file back; the
// default therefore reaches a home a pre-fix boot wrote, with nothing to migrate.
func TestTheNextJailBootRepairsAnEmptyModelsFile(t *testing.T) {
	home := t.TempDir()
	path := piModelsPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bootPiOnlyJail(t, home)
	requireObjectProviders(t, path, "the boot after a {} models.json")
}

// hostRenderPi runs `yolo host apply` for the shipped pi pack into home under
// `host_management: "own"` — the one writing contract, which renders pi/models (declared
// `computed`) through `stateful` — and returns the pi/models result. observe is the dry run.
func hostRenderPi(t *testing.T, home string, observe bool) HostRenderResult {
	t.Helper()
	pi, err := embeddedPack("pi")
	if err != nil {
		t.Fatal(err)
	}
	results, err := RenderHostPack(pi, home, render.OwnershipOwn, observe, packoverlay.Collect([]*packload.Pack{pi}, false, nil), nil)

	if err != nil {
		t.Fatalf("RenderHostPack(pi): %v", err)
	}
	return resultFor(t, results, "pi/models")
}

// A HOST APPLY INTO A HOME WITH NO models.json creates it with `providers` — the measured
// case, where `yolo host apply --assert` created `{}` and host pi then printed the error.
func TestAHostApplyIntoAFreshHomeWritesAModelsFileWithProviders(t *testing.T) {
	home := t.TempDir()
	if r := hostRenderPi(t, home, false); r.Action != "rendered" {
		t.Fatalf("pi/models at the host: %q, want rendered", r.Action)
	}
	requireObjectProviders(t, piModelsPath(home), "host apply into a fresh home")
}

// A HOST FILE HOLDING `{}` — written by `yolo host apply` before this default — is repaired by
// the next apply, and one holding the user's own providers keeps them: the first owned render
// adopts the file as the user's captured edit, which outranks a default, and a default fills
// only an absent key.
func TestTheNextHostApplyRepairsAnEmptyModelsFileAndKeepsYourProviders(t *testing.T) {
	t.Run("{} is repaired", func(t *testing.T) {
		home := t.TempDir()
		path := piModelsPath(home)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if r := hostRenderPi(t, home, true); !r.WouldChange {
			t.Errorf("the dry run over a {} models.json reported %q with WouldChange=false; "+
				"the --assert adds `providers`, so it must say so", r.Action)
		}
		hostRenderPi(t, home, false)
		requireObjectProviders(t, path, "host apply over a {} models.json")
	})
	t.Run("your providers are kept", func(t *testing.T) {
		home := t.TempDir()
		path := piModelsPath(home)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		mine := `{"providers":{"mine":{"baseUrl":"http://127.0.0.1:9/v1","api":"openai-completions"}}}`
		if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
			t.Fatal(err)
		}
		hostRenderPi(t, home, false)
		if providers := requireObjectProviders(t, path, "host apply over your providers"); providers["mine"] == nil {
			t.Errorf("the host apply dropped your own provider: %v", providers)
		}
	})
}
