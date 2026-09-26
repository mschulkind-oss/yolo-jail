package entrypoint

import (
	"encoding/json"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// openai-codex is one of oh-omp's BUILT-IN providers (its ChatGPT subscription client, with
// its own OAuth login), so ~/.oh-omp/agent/models.yml must never carry a row for it
// (docs/design/pi-codex-provider-shadowing.md OQ-1, OQ-2: an agent never derives catalog
// entries from a provider it natively implements). omp applies a models.yml row's baseUrl
// and headers to the built-in provider's models of the same name, so a row redirects the
// subscription client — pi's failure, in the fork that inherited pi's catalog.
//
// Shipped packs do not reach the row today only by accident of spelling: packs/openai-auth's
// own endpoint is keyed `openai-responses`, which omp's derive does not read, and the
// bridge address composed under `anthropic` carries no wire_api. Any endpoint omp CAN read
// (a user's providers.openai-codex.endpoints.openai) or a via profile selecting it writes
// the row, so the exclusion is by NAME, as packs/codex/derive.lua and packs/pi/derive.lua do.
func ompModelsFor(t *testing.T, sel surfaceSelection, providers map[string]any) map[string]any {
	t.Helper()
	script, s := deriveSurface(t, "omp", "oh-omp/models")
	got, _, err := deriveComputedLayer(&Env{Vars: map[string]string{}}, s, script, sel,
		map[string]map[string]any{manifest.SourceProviders: providers})
	if err != nil {
		t.Fatal(err)
	}
	provs, _ := got["providers"].(map[string]any)
	return provs
}

func ompCodexTable() map[string]any {
	return map[string]any{
		"openai-codex": map[string]any{
			"endpoints": map[string]any{
				"openai-responses": map[string]any{
					"base_url": "https://chatgpt.com/backend-api/codex",
					"wire_api": "openai-responses"},
				"openai": map[string]any{
					"base_url": "https://codex-proxy.example/v1",
					"wire_api": "openai-responses"},
			},
		},
		"local": map[string]any{
			"endpoints": map[string]any{"openai": map[string]any{
				"base_url": "http://host.containers.internal:8080/v1",
				"wire_api": "openai-chat-completions"}},
			"models": map[string]any{"default": "qwen3.8-27b"},
		},
	}
}

func TestOmpCatalogNeverCataloguesOpenAICodex(t *testing.T) {
	provs := ompModelsFor(t, surfaceSelection{}, ompCodexTable())
	if row, shadowed := provs["openai-codex"]; shadowed {
		t.Fatalf("omp's models.yml catalogues openai-codex, shadowing omp's built-in "+
			"subscription client: %#v", row)
	}
	if _, ok := provs["local"]; !ok {
		t.Fatalf("the exclusion dropped another provider too; catalog = %#v", provs)
	}
}

// A via profile routes only the provider it selects, and a native provider is not one yolo
// catalogues at all — so selecting openai-codex through a via profile writes no row either.
func TestOmpViaProfileNeverCataloguesOpenAICodex(t *testing.T) {
	via := viaBase + "/agent/oh-omp"
	provs := ompModelsFor(t, surfaceSelection{Profile: "pc", Provider: "openai-codex", ViaURL: via},
		ompCodexTable())
	if row, shadowed := provs["openai-codex"]; shadowed {
		t.Fatalf("a via profile selecting openai-codex wrote an omp models.yml row for it, "+
			"shadowing omp's built-in subscription client: %#v", row)
	}
	if local, _ := provs["local"].(map[string]any); local["baseUrl"] != "http://host.containers.internal:8080/v1" {
		t.Fatalf("a provider the profile does not select must keep its own row: %#v", provs["local"])
	}
}

// The same property through a PRODUCTION pack set (design doc P3): omp beside claude, whose
// `needs` joins openai-auth (the pack declaring openai-codex) and wire-bridge (whose adapter
// composes an anthropic address onto it, because omp and claude both speak anthropic).
func TestOmpCatalogFromShippedPacksOmitsOpenAICodex(t *testing.T) {
	packs := make([]*packload.Pack, 0, 5)
	for _, name := range []string{"omp", "claude", "openai-auth", "wire-bridge", "kilo"} {
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
	if _, ok := providers.Get("openai-codex"); !ok {
		t.Fatalf("the composed table has no openai-codex provider, so this case measures "+
			"nothing: %v", providers.Keys())
	}
	var table map[string]any
	if err := json.Unmarshal([]byte(mustCompactJSON(t, providers)), &table); err != nil {
		t.Fatal(err)
	}
	provs := ompModelsFor(t, surfaceSelection{}, table)
	if row, shadowed := provs["openai-codex"]; shadowed {
		t.Fatalf("models.yml shadows omp's built-in openai-codex provider: %#v", row)
	}
	if _, ok := provs["kilo"]; !ok {
		t.Fatalf("kilo, a reachable provider, is missing from the catalog: %#v", provs)
	}
}
