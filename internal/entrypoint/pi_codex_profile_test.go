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

// piCodexExactAllow is pi-subagents' modelScope.allow for the shipped codex declaration:
// every declared id, EXACTLY, where a hand-written `openai-codex/gpt-6-*` glob used to
// state the family a third time (docs/design/model-lists-and-pickers.md ML-D5). The literal
// is the expectation; codex_model_list_test.go pins that it follows the declaration.
var piCodexExactAllow = []any{
	"openai-codex/gpt-6.1-sol",
	"openai-codex/gpt-6.1-sol[1m]",
	"openai-codex/gpt-6-astra",
	"openai-codex/gpt-6-astra[1m]",
	"openai-codex/gpt-6-luna",
	"openai-codex/gpt-6-luna[1m]",
}

// This follows the production handoff on both sides: the pack set a pi launch carries
// declares and resolves the profile on the host, then ConfigurePackSurfaces consumes those
// exact wire tables and writes the settings Pi reads. Pi owns openai-codex in its built-in
// catalog, so yolo selects it without shadowing it in models.json.
//
// The set is pi's selection CLOSURE (testPacksForAgent), not the pi pack alone: pi's
// `needs` joins openai-auth, the pack that declares openai-codex with a Responses address.
// Composed from pi alone, openai-codex never reached the catalog derive and the shadow
// assertion below passed vacuously (docs/design/pi-codex-provider-shadowing.md §5, P3).
func TestPiCodexProfileSelectsBuiltInProviderAndExplicitModel(t *testing.T) {
	packs := testPacksForAgent(t, "pi")
	providers, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := providers.Get("openai-codex"); !ok {
		t.Fatalf("the composed table has no openai-codex provider, so the shadow assertion "+
			"measures nothing: %v", providers.Keys())
	}
	resolved, err := packload.ResolveProfiles(packs, nil, providers)
	if err != nil {
		t.Fatal(err)
	}
	profile, ok := resolved["codex"]
	if !ok || profile.Provider != "openai-codex" {
		t.Fatalf("resolved codex profile = %#v, want openai-codex", profile)
	}

	r := newPioencodeRender(t, mustCompactJSON(t, providers))
	r.wireProfiles(mustCompactJSON(t, packload.ProfilesWireTable(resolved)))
	r.render(t, `{"pi":"codex"}`)
	settings := r.piSettings(t)
	if settings["defaultProvider"] != "openai-codex" || settings["defaultModel"] != "gpt-6.1-sol" {
		t.Fatalf("Pi selection = provider %#v model %#v, want openai-codex/gpt-6.1-sol",
			settings["defaultProvider"], settings["defaultModel"])
	}
	// NO SCOPE (docs/design/model-lists-and-pickers.md ML-D2): pi's "all" view for
	// openai-codex is the list its extension registers, so pi shows no Scope toggle and
	// starts on the pair above.
	if scoped, present := settings["enabledModels"]; present {
		t.Fatalf("Pi enabledModels = %#v, want it absent for openai-codex", scoped)
	}
	wantSubagents := map[string]any{
		"defaultProvider": "openai-codex",
		"defaultModel":    "openai-codex/gpt-6.1-sol",
		"modelScope": map[string]any{
			"enforce": true,
			"strict":  true,
			"allow":   piCodexExactAllow,
		},
	}
	if got := settings["subagents"]; !reflect.DeepEqual(got, wantSubagents) {
		t.Fatalf("Pi subagents = %#v, want a strict GPT-6 Codex policy %#v", got, wantSubagents)
	}
	catalog, _ := r.piModels(t)["providers"].(map[string]any)
	if _, shadowed := catalog["openai-codex"]; shadowed {
		t.Fatalf("models.json shadows Pi's built-in openai-codex provider: %#v", catalog)
	}
}

func TestPiCodexProfileSelects1MContextModel(t *testing.T) {
	packs := testPacksForAgent(t, "pi")
	providers, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	userProfiles := map[string]packload.UserProfile{
		"codex": {
			Provider: "openai-codex",
			Options:  map[string]string{"model": "gpt-6-astra[1m]"},
		},
	}
	resolved, err := packload.ResolveProfiles(packs, userProfiles, providers)
	if err != nil {
		t.Fatal(err)
	}

	r := newPioencodeRender(t, mustCompactJSON(t, providers))
	r.wireProfiles(mustCompactJSON(t, packload.ProfilesWireTable(resolved)))
	r.render(t, `{"pi":"codex"}`)
	settings := r.piSettings(t)
	if settings["defaultProvider"] != "openai-codex" || settings["defaultModel"] != "gpt-6-astra[1m]" {
		t.Fatalf("Pi selection = provider %#v model %#v, want openai-codex/gpt-6-astra[1m]",
			settings["defaultProvider"], settings["defaultModel"])
	}
	// A 1M variant is a model of its own in pi's list, so it is selected by the pair alone;
	// there is no scope to lead it with.
	if scoped, present := settings["enabledModels"]; present {
		t.Fatalf("Pi enabledModels = %#v, want it absent for openai-codex", scoped)
	}
	wantSubagents := map[string]any{
		"defaultProvider": "openai-codex",
		"defaultModel":    "openai-codex/gpt-6-astra[1m]",
		"modelScope": map[string]any{
			"enforce": true,
			"strict":  true,
			"allow":   piCodexExactAllow,
		},
	}
	if got := settings["subagents"]; !reflect.DeepEqual(got, wantSubagents) {
		t.Fatalf("Pi subagents = %#v, want %#v", got, wantSubagents)
	}
}

// THE PRODUCTION PACK SET for a pi launch is pi AND openai-auth: pi's `needs` joins it
// unconditionally, and openai-auth is the pack that DECLARES the openai-codex provider.
// The tests above used to compose from the pi pack ALONE, so openai-codex never reached the
// catalog derive there and the row it rendered in production went unmeasured — the shape
// that shipped a models.json pi refuses to load. Every pi fixture here now composes pi's
// selection closure (testPacksForAgent) plus the providers the case selects.
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
	packs := testPacksForAgent(t, "pi", "kilo")
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
	// zaiReachableJSON's provider is named `zhipu`, which pi has none of its own for, so it is
	// catalogued (zai itself is pi's own: docs/design/pi-codex-provider-shadowing.md OQ-3).
	r := newPioencodeRender(t, zaiReachableJSON)
	r.render(t, `{"pi":"zhipu"}`)
	settings := r.piSettings(t)
	enabled, ok := settings["enabledModels"].([]any)
	if !ok {
		t.Fatalf("zhipu profile enabledModels missing: %#v", settings)
	}
	// This fixture carries no default (no profile model, no options.model, no
	// `default` alias), so the list stays purely sorted — the no-default neighbor of
	// the default-first order TestPiDeriveSettingsScopesDeclaredModels pins.
	wantEnabled := []any{"zhipu/glm-4.6", "zhipu/glm-5.3", "zhipu/glm-5.3-flash"}
	if !reflect.DeepEqual(enabled, wantEnabled) {
		t.Fatalf("zhipu profile enabledModels = %#v, want %#v", enabled, wantEnabled)
	}

	models := r.piModels(t)
	provs, ok := models["providers"].(map[string]any)
	if !ok {
		t.Fatalf("pi models providers missing: %#v", models)
	}
	zaiProv, ok := provs["zhipu"].(map[string]any)
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
