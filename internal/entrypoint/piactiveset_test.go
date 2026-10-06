package entrypoint

// piactiveset_test.go pins pi's ACTIVE SET render (docs/design/active-provider-sets.md §4.4 and
// §4.6; the active set, a term that doc coins, is the ordered list of profiles one agent runs on
// for one launch): with YOLO_USE_PROFILES carrying `{"pi": ["zai", "router"]}`, the boot renders
// both providers' catalog rows, a scoped picker list that is the union with the primary's default
// first, a pi-subagents scope over every entry and nothing else, and the start pair of the
// primary. Through ConfigurePackSurfaces, the entry the boot loop uses, over the real embedded
// pi pack: deleting the surface loop's set lowering (surfaceloop.go), the ctx field, or the pi
// derive's piSetSettings wrapper fails these cases.

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// setProvidersJSON is zai (with a declared default) beside a second provider declaring its own
// list and default, and openai-codex's declared list with a 1M variant.
const setProvidersJSON = `{
  "zhipu":{"api_key_env_name":"ZAI_API_KEY",
    "models":{"default":"glm-5.3","glm-4.6":"glm-4.6","glm-5.3":"glm-5.3"},
    "endpoints":{"openai":{"base_url":"https://api.z.ai/api/coding/paas/v4","wire_api":"openai-chat-completions"}}},
  "router":{"api_key_env_name":"ROUTER_API_KEY",
    "models":{"default":"vendor/b","vendor/a":"vendor/a","vendor/b":"vendor/b"},
    "endpoints":{"openai":{"base_url":"https://router.example/v1"}}},
  "openai-codex":{
    "models":{"gpt-a":"gpt-a","gpt-b":"gpt-b"},
    "model_options":{"gpt-a":{"order":"1","long_context_window":"1000000"},"gpt-b":{"order":"2"}},
    "endpoints":{"openai-responses":{"base_url":"https://chatgpt.example/codex"}}}}`

const setProfilesWire = `{"zhipu":{"provider":"zhipu"},"router":{"provider":"router","context_window":"65536"},
  "codex":{"provider":"openai-codex"}}`

func renderPiSet(t *testing.T, use string) *pioencodeRender {
	t.Helper()
	r := newPioencodeRender(t, setProvidersJSON)
	r.wireProfiles(setProfilesWire)
	r.render(t, use)
	return r
}

func strs(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, e := range list {
		s, _ := e.(string)
		out = append(out, s)
	}
	return out
}

func TestPiRendersItsWholeActiveSet(t *testing.T) {
	r := renderPiSet(t, `{"pi":["zhipu","router"]}`)
	settings, models := r.piSettings(t), r.piModels(t)

	// The start pair is the PRIMARY's (AP-D1): a fresh session starts on zai's default.
	requirePiSelection(t, settings, models, "zhipu", "glm-5.3")

	// The scoped picker is the union, the primary's default first, then the primary's other
	// models, then the second entry's run with ITS default leading (§4.4).
	wantEnabled := []string{"zhipu/glm-5.3", "zhipu/glm-4.6", "router/vendor/b", "router/vendor/a"}
	if got := strs(settings["enabledModels"]); !reflect.DeepEqual(got, wantEnabled) {
		t.Errorf("enabledModels = %v, want %v", got, wantEnabled)
	}

	// Both providers are catalog rows, each keyed to its own credential, and the second entry's
	// own profile option reaches its own row (piProfileFor): router's context_window.
	provs, _ := models["providers"].(map[string]any)
	for name, key := range map[string]string{"zhipu": "${ZAI_API_KEY}", "router": "${ROUTER_API_KEY}"} {
		row, _ := provs[name].(map[string]any)
		if row == nil || row["apiKey"] != key {
			t.Errorf("models.json %s row = %#v, want apiKey %s", name, row, key)
		}
	}
	router, _ := provs["router"].(map[string]any)
	for _, m := range router["models"].([]any) {
		if cw := m.(map[string]any)["contextWindow"]; cw != float64(65536) {
			t.Errorf("router's model %v has contextWindow %v, want its own profile's 65536", m, cw)
		}
	}

	// A child agent may run on any provider in the set and on nothing outside it (§4.6).
	sub, _ := settings["subagents"].(map[string]any)
	if sub["defaultModel"] != "zhipu/glm-5.3" || sub["defaultProvider"] != "zhipu" {
		t.Errorf("subagents start = %v/%v, want the primary's zhipu/glm-5.3", sub["defaultProvider"], sub["defaultModel"])
	}
	scope, _ := sub["modelScope"].(map[string]any)
	wantAllow := []string{"zhipu/glm-5.3", "zhipu/glm-4.6", "router/vendor/b", "router/vendor/a"}
	if got := strs(scope["allow"]); !reflect.DeepEqual(got, wantAllow) || scope["strict"] != true || scope["enforce"] != true {
		t.Errorf("subagents.modelScope = %#v, want an enforced strict allow of %v", scope, wantAllow)
	}
}

// AP-D6: an openai-codex entry in a set of more than one adds its declared BASE ids to the
// scoped list, the `[1m]` variant left out (a minimatch pattern would read it as a character
// class), while the pi-subagents scope keeps every declared id.
func TestACodexEntryAddsItsBaseIdsToTheScopedList(t *testing.T) {
	r := renderPiSet(t, `{"pi":["zhipu","codex"]}`)
	settings := r.piSettings(t)
	wantEnabled := []string{"zhipu/glm-5.3", "zhipu/glm-4.6", "openai-codex/gpt-a", "openai-codex/gpt-b"}
	if got := strs(settings["enabledModels"]); !reflect.DeepEqual(got, wantEnabled) {
		t.Errorf("enabledModels = %v, want %v", got, wantEnabled)
	}
	sub, _ := settings["subagents"].(map[string]any)
	scope, _ := sub["modelScope"].(map[string]any)
	allow := strs(scope["allow"])
	for _, want := range []string{"openai-codex/gpt-a", "openai-codex/gpt-a[1m]", "openai-codex/gpt-b"} {
		found := false
		for _, a := range allow {
			found = found || a == want
		}
		if !found {
			t.Errorf("subagents allow %v lacks %s", allow, want)
		}
	}

	// With codex FIRST, the session starts on the subscription's declared default, and the scope
	// is written although codex alone writes none (ML-D2 holds only when codex is the whole set).
	r = renderPiSet(t, `{"pi":["codex","zhipu"]}`)
	settings = r.piSettings(t)
	if settings["defaultProvider"] != "openai-codex" || settings["defaultModel"] != "gpt-a" {
		t.Errorf("start pair = %v/%v, want openai-codex/gpt-a", settings["defaultProvider"], settings["defaultModel"])
	}
	wantEnabled = []string{"openai-codex/gpt-a", "openai-codex/gpt-b", "zhipu/glm-5.3", "zhipu/glm-4.6"}
	if got := strs(settings["enabledModels"]); !reflect.DeepEqual(got, wantEnabled) {
		t.Errorf("enabledModels with codex first = %v, want %v", got, wantEnabled)
	}
}

// A set of one renders byte for byte what the single profile renders (AP-P1): the list spelling
// and the string spelling are one selection.
func TestASetOfOneRendersExactlyTheSingleProfile(t *testing.T) {
	read := func(r *pioencodeRender, rel ...string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(append([]string{r.e.Home}, rel...)...))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	single, listed := renderPiSet(t, `{"pi":"zhipu"}`), renderPiSet(t, `{"pi":["zhipu"]}`)
	for _, rel := range [][]string{{".pi", "agent", "settings.json"}, {".pi", "agent", "models.json"}} {
		if a, b := read(single, rel...), read(listed, rel...); a != b {
			t.Errorf("%s differs between \"zhipu\" and [\"zhipu\"]:\n--- string\n%s\n--- list\n%s",
				filepath.Join(rel...), a, b)
		}
	}
}
