package entrypoint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// This follows the production handoff on both sides: the shipped Pi pack declares and
// resolves the profile on the host, then ConfigurePackSurfaces consumes those exact wire
// tables and writes the settings Pi reads. Pi owns openai-codex in its built-in catalog,
// so yolo selects it without shadowing it in models.json.
func TestPiCodexProfileSelectsBuiltInProviderAndExplicitModel(t *testing.T) {
	pi := shippedPiPack(t)
	providers, err := packload.ComposeProviders(nil, []*packload.Pack{pi})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles([]*packload.Pack{pi}, nil, providers)
	if err != nil {
		t.Fatal(err)
	}
	profile, ok := resolved["codex"]
	if !ok || profile.Provider != "openai-codex" {
		t.Fatalf("resolved codex profile = %#v, want openai-codex", profile)
	}

	providersJSON := `{}`
	if providers != nil {
		providersJSON = mustCompactJSON(t, providers)
	}
	r := newPioencodeRender(t, providersJSON)
	r.wireProfiles(mustCompactJSON(t, packload.ProfilesWireTable(resolved)))
	r.render(t, `{"pi":"codex"}`)
	settings := r.piSettings(t)
	if settings["defaultProvider"] != "openai-codex" || settings["defaultModel"] != "gpt-6-sol" {
		t.Fatalf("Pi selection = provider %#v model %#v, want openai-codex/gpt-6-sol",
			settings["defaultProvider"], settings["defaultModel"])
	}
	enabled, ok := settings["enabledModels"].([]any)
	wantEnabled := []any{
		"openai-codex/gpt-6-luna",
		"openai-codex/gpt-6-sol",
		"openai-codex/gpt-6-astra",
	}
	if !ok || !reflect.DeepEqual(enabled, wantEnabled) {
		t.Fatalf("Pi enabledModels = %#v, want only GPT-6 models %#v", settings["enabledModels"], wantEnabled)
	}
	wantSubagents := map[string]any{
		"defaultProvider": "openai-codex",
		"defaultModel":    "openai-codex/gpt-6-sol",
		"modelScope": map[string]any{
			"enforce": true,
			"strict":  true,
			"allow": []any{
				"openai-codex/gpt-6-*",
			},
		},
	}
	if got := settings["subagents"]; !reflect.DeepEqual(got, wantSubagents) {
		t.Fatalf("Pi subagents = %#v, want a strict GPT-6 Codex policy %#v", got, wantSubagents)
	}
	models := r.piModels(t)
	if catalog, _ := models["providers"].(map[string]any); catalog != nil {
		if _, shadowed := catalog["openai-codex"]; shadowed {
			t.Fatalf("models.json shadows Pi's built-in openai-codex provider: %#v", catalog)
		}
	}
}

// THE PRODUCTION PACK SET for a pi launch is pi AND openai-auth: pi's `needs` joins it
// unconditionally, and openai-auth is the pack that DECLARES the openai-codex provider.
// The test above composes from the pi pack ALONE, so openai-codex never reaches the
// catalog derive there and the row it renders in production went unmeasured — the shape
// that shipped a models.json pi refuses to load.
//
// The failure is one type, and it is fatal to the WHOLE FILE. pi's ProviderConfigSchema
// declares `models` as an optional ARRAY, and a schema failure returns an EMPTY provider
// map with an error — so one bad row deletes every OTHER provider's row with it, and a
// launch that selected a second provider reports "No models match pattern" for a model the
// same file names. Measured against the installed pi 0.85.1 (core/model-config.js,
// ModelConfig.load → validateModelsConfig): `{"models":{}}` yields
// `providers.openai-codex.models: must be array` and zero providers; with the key OMITTED
// the same file loads both rows. An empty Lua table cannot be spelled as an array
// (luahook/marshal.go's documented ambiguity — `{}` is an object on the way back), so a
// derive that has no models for a provider must not write the key at all.
//
// The assertion is the schema invariant rather than "openai-codex has no row", because the
// class is every provider that declares an address and no model list: kilo does so
// whenever no profile names a model, and so does a user's own `endpoints.openai`.
func TestPiCatalogNeverWritesAModelsMapWhereAnArrayBelongs(t *testing.T) {
	packs := make([]*packload.Pack, 0, 3)
	for _, name := range []string{"pi", "openai-auth", "kilo"} {
		p, err := embeddedPack(name)
		if err != nil {
			t.Fatalf("embedded %s: %v", name, err)
		}
		packs = append(packs, p)
	}
	providers, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles(packs, nil, providers)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := providers.Get("openai-codex"); !ok {
		t.Fatalf("the composed table has no openai-codex provider, so this case measures "+
			"nothing: %v", providers.Keys())
	}
	r := newPioencodeRender(t, mustCompactJSON(t, providers))
	r.wireProfiles(mustCompactJSON(t, packload.ProfilesWireTable(resolved)))
	r.render(t, `{"pi":"codex"}`)

	models := r.piModels(t)
	catalog, ok := models["providers"].(map[string]any)
	if !ok {
		t.Fatalf("models.json has no providers table: %#v", models)
	}
	for name, row := range catalog {
		entry, ok := row.(map[string]any)
		if !ok {
			t.Fatalf("models.json row %s is not an object: %#v", name, row)
		}
		list, present := entry["models"]
		if !present {
			continue
		}
		if _, isArray := list.([]any); !isArray {
			t.Errorf("models.json provider %s has models = %#v (%T), and pi's schema "+
				"declares an ARRAY — this row makes pi discard the entire file, every "+
				"other provider included", name, list, list)
		}
	}
}

func TestPiExplicitProfileScopesModelsAndNoProfilePreservesUserScope(t *testing.T) {
	r := newPioencodeRender(t, zaiReachableJSON)
	r.render(t, `{"pi":"zai"}`)
	settings := r.piSettings(t)
	enabled, ok := settings["enabledModels"].([]any)
	if !ok {
		t.Fatalf("zai profile enabledModels missing: %#v", settings)
	}
	wantEnabled := []any{"zai/glm-4.6", "zai/glm-5.3", "zai/glm-5.3-flash"}
	if !reflect.DeepEqual(enabled, wantEnabled) {
		t.Fatalf("zai profile enabledModels = %#v, want %#v", enabled, wantEnabled)
	}

	models := r.piModels(t)
	provs, ok := models["providers"].(map[string]any)
	if !ok {
		t.Fatalf("pi models providers missing: %#v", models)
	}
	zaiProv, ok := provs["zai"].(map[string]any)
	if !ok {
		t.Fatalf("pi models zai provider missing: %#v", provs)
	}
	zaiModels, ok := zaiProv["models"].([]any)
	if !ok || len(zaiModels) != 3 {
		t.Fatalf("pi models zai models = %#v, want 3 models", zaiProv["models"])
	}
	for _, m := range zaiModels {
		entry := m.(map[string]any)
		if mt, ok := entry["maxTokens"]; ok {
			t.Errorf("model %v has maxTokens = %v, want omitted", entry["id"], mt)
		}
	}

	settings["enabledModels"] = []any{"cerebras/*", "zai/*"}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(r.e.Home, ".pi", "agent", "settings.json")
	if err := os.WriteFile(settingsPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	r.render(t, `{}`)
	preserved, ok := r.piSettings(t)["enabledModels"].([]any)
	if !ok || len(preserved) != 2 || preserved[0] != "cerebras/*" || preserved[1] != "zai/*" {
		t.Fatalf("unprofiled Pi changed existing enabledModels: %#v", preserved)
	}
}

func mustCompactJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := jsonx.DumpsCompact(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
