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
	if settings["defaultProvider"] != "openai-codex" || settings["defaultModel"] != "gpt-5.6-terra" {
		t.Fatalf("Pi selection = provider %#v model %#v, want openai-codex/gpt-5.6-terra",
			settings["defaultProvider"], settings["defaultModel"])
	}
	enabled, ok := settings["enabledModels"].([]any)
	wantEnabled := []any{
		"openai-codex/gpt-5.6-luna",
		"openai-codex/gpt-5.6-terra",
		"openai-codex/gpt-5.6-sol",
		"openai-codex/gpt-6-astra",
	}
	if !ok || !reflect.DeepEqual(enabled, wantEnabled) {
		t.Fatalf("Pi enabledModels = %#v, want only 5.6-or-newer models %#v", settings["enabledModels"], wantEnabled)
	}
	wantSubagents := map[string]any{
		"defaultProvider": "openai-codex",
		"defaultModel":    "openai-codex/gpt-5.6-terra",
		"modelScope": map[string]any{
			"enforce": true,
			"strict":  true,
			"allow": []any{
				"openai-codex/gpt-5.6-*",
				"openai-codex/gpt-6-*",
			},
		},
	}
	if got := settings["subagents"]; !reflect.DeepEqual(got, wantSubagents) {
		t.Fatalf("Pi subagents = %#v, want a strict 5.6-or-newer Codex policy %#v", got, wantSubagents)
	}
	models := r.piModels(t)
	if catalog, _ := models["providers"].(map[string]any); catalog != nil {
		if _, shadowed := catalog["openai-codex"]; shadowed {
			t.Fatalf("models.json shadows Pi's built-in openai-codex provider: %#v", catalog)
		}
	}
}

func TestPiExplicitProfileScopesModelsAndNoProfilePreservesUserScope(t *testing.T) {
	r := newPioencodeRender(t, zaiReachableJSON)
	r.render(t, `{"pi":"zai"}`)
	settings := r.piSettings(t)
	enabled, ok := settings["enabledModels"].([]any)
	if !ok || len(enabled) != 1 || enabled[0] != "zai/*" {
		t.Fatalf("zai profile enabledModels = %#v, want only zai/*", settings["enabledModels"])
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
