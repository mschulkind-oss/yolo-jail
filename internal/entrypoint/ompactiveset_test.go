package entrypoint

// ompactiveset_test.go pins oh-omp's ACTIVE SET render (docs/design/active-provider-sets.md §4.4
// and AP-D18; the active set, a term that doc coins, is the ordered list of profiles one agent
// runs on for one launch): with YOLO_USE_PROFILES carrying `{"oh-omp": ["zai", "router"]}`, the
// boot renders a models.yml row for each entry, keyed to its own credential, and, when an `only`
// narrowed any entry's list, an `enabledModels` scope in config.yml that holds every entry in set
// order: the narrowed run with its default first, and `<provider>/*` for an entry with no narrowed
// list, since omp's selector shows nothing outside a scope. Through ConfigurePackSurfaces, the
// entry the boot loop uses, over the real embedded omp pack: deleting the surface loop's set
// lowering, ctx.active_set, or the derive's set branch fails these cases.

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// ompSetProviders is zai beside a second provider, router, each with its own key and list. zai's
// list is narrowed by an `only` when narrowed is set, the marker a `models` contribution leaves.
func ompSetProviders(narrowed bool) string {
	only := ""
	if narrowed {
		only = `"models_only":true,`
	}
	return `{
  "zai":{"api_key_env_name":"ZAI_API_KEY",` + only + `
    "models":{"default":"glm-5.3","glm-5.3":"glm-5.3","glm-5.3-flash":"glm-5.3-flash"},
    "endpoints":{"openai":{"base_url":"https://api.z.ai/api/coding/paas/v4","wire_api":"openai-chat-completions"}}},
  "router":{"api_key_env_name":"ROUTER_API_KEY",
    "models":{"default":"vendor/b","vendor/a":"vendor/a","vendor/b":"vendor/b"},
    "endpoints":{"openai":{"base_url":"https://router.example/v1","wire_api":"openai-chat-completions"}}}}`
}

const ompSetProfiles = `{"zai":{"provider":"zai"},"router":{"provider":"router"}}`

// renderOmpSet boots the embedded omp pack once with the given providers table and active-profile
// table, and returns the home it rendered into.
func renderOmpSet(t *testing.T, providers, use string) string {
	t.Helper()
	omp, err := embeddedPack("omp")
	if err != nil {
		t.Fatal(err)
	}
	var errw bytes.Buffer
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Stderr: &errw, Vars: map[string]string{
		"YOLO_PROVIDERS":    providers,
		"YOLO_USE_PROFILES": use,
		"YOLO_PROFILES":     ompSetProfiles,
	}}
	withCtxRoot(t, t.TempDir(), "omp")
	ConfigurePackSurfaces(e, []*packload.Pack{omp})
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("boot render failed: %v\n%s", fails, errw.String())
	}
	return e.Home
}

// ompYAML decodes one of omp's rendered files, or returns nil when the boot wrote none.
func ompYAML(t *testing.T, home, file string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, ".oh-omp", "agent", file))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := (codec.YAML{}).Decode(raw)
	if err != nil {
		t.Fatalf("%s is not YAML: %v\n%s", file, err, raw)
	}
	m, _ := decoded.(map[string]any)
	return m
}

// A SET REACHES oh-omp WHOLE: each entry is a models.yml row keyed to its own credential, as every
// reachable provider's row was before sets, and with no entry's list narrowed no scope is written,
// exactly as for one profile (AP-P1), so omp's own picker offers every entry.
func TestOmpRendersItsWholeActiveSet(t *testing.T) {
	home := renderOmpSet(t, ompSetProviders(false), `{"oh-omp":["zai","router"]}`)
	models := ompYAML(t, home, "models.yml")
	rows, _ := models["providers"].(map[string]any)
	for name, key := range map[string]string{"zai": "ZAI_API_KEY", "router": "ROUTER_API_KEY"} {
		row, _ := rows[name].(map[string]any)
		if row == nil || row["apiKey"] != key {
			t.Errorf("models.yml %s row = %#v, want apiKey %s", name, row, key)
		}
	}
	if cfg := ompYAML(t, home, "config.yml"); cfg != nil && cfg["enabledModels"] != nil {
		t.Errorf("no entry is narrowed, so no scope may be written: enabledModels = %v", cfg["enabledModels"])
	}
}

// ONCE A SCOPE IS WRITTEN IT HOLDS THE WHOLE SET (§4.4, AP-P2): omp's selector shows only the scope,
// so a scope of the narrowed primary alone would hide router in a session meant to switch to it.
// The narrowed run leads with its default; router, with no narrowed list, is every model omp has
// for it. In the other order router leads, so omp's start rule (the scope's first entry when no
// saved model is in it) lands on the primary there too.
func TestOmpScopesTheWholeSetWhenAnEntryIsNarrowed(t *testing.T) {
	for _, tc := range []struct {
		use  string
		want []any
	}{
		{`{"oh-omp":["zai","router"]}`, []any{"zai/glm-5.3", "zai/glm-5.3-flash", "router/*"}},
		{`{"oh-omp":["router","zai"]}`, []any{"router/*", "zai/glm-5.3", "zai/glm-5.3-flash"}},
	} {
		home := renderOmpSet(t, ompSetProviders(true), tc.use)
		cfg := ompYAML(t, home, "config.yml")
		if got := cfg["enabledModels"]; !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: enabledModels = %v, want %v", tc.use, got, tc.want)
		}
		if _, leaked := cfg[selectionKey]; leaked {
			t.Errorf("%s: config.yml carries a literal %q table", tc.use, selectionKey)
		}
	}
}

// A set of one renders byte for byte what the single profile renders (AP-P1), narrowed or not:
// the list spelling and the string spelling are one selection for omp.
func TestAnOmpSetOfOneRendersExactlyTheSingleProfile(t *testing.T) {
	for _, narrowed := range []bool{false, true} {
		single := renderOmpSet(t, ompSetProviders(narrowed), `{"oh-omp":"zai"}`)
		listed := renderOmpSet(t, ompSetProviders(narrowed), `{"oh-omp":["zai"]}`)
		for _, file := range []string{"models.yml", "config.yml"} {
			a, errA := os.ReadFile(filepath.Join(single, ".oh-omp", "agent", file))
			b, errB := os.ReadFile(filepath.Join(listed, ".oh-omp", "agent", file))
			if (errA == nil) != (errB == nil) || !bytes.Equal(a, b) {
				t.Errorf("narrowed=%v: %s differs between \"zai\" and [\"zai\"]:\n--- string (%v)\n%s\n--- list (%v)\n%s",
					narrowed, file, errA, a, errB, b)
			}
		}
	}
}

// REMOVING AN ENTRY REWRITES THE SCOPE WHOLE (§4.10): enabledModels rides the selection, so the next
// boot on [zai] scopes zai alone rather than keeping the router run it wrote before.
func TestAnEntryLeavingOmpsSetLeavesTheScope(t *testing.T) {
	omp, err := embeddedPack("omp")
	if err != nil {
		t.Fatal(err)
	}
	var errw bytes.Buffer
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Stderr: &errw, Vars: map[string]string{
		"YOLO_PROVIDERS":    ompSetProviders(true),
		"YOLO_USE_PROFILES": `{"oh-omp":["zai","router"]}`,
		"YOLO_PROFILES":     ompSetProfiles,
	}}
	withCtxRoot(t, t.TempDir(), "omp")
	ConfigurePackSurfaces(e, []*packload.Pack{omp})
	e.Vars["YOLO_USE_PROFILES"] = `{"oh-omp":"zai"}`
	ConfigurePackSurfaces(e, []*packload.Pack{omp})
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("boot render failed: %v\n%s", fails, errw.String())
	}
	cfg := ompYAML(t, e.Home, "config.yml")
	if got, want := cfg["enabledModels"], []any{"zai/glm-5.3", "zai/glm-5.3-flash"}; !reflect.DeepEqual(got, want) {
		t.Errorf("after router left the set, enabledModels = %v, want %v", got, want)
	}
}
