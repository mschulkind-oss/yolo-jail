package entrypoint

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// openai-codex is pi's BUILT-IN subscription provider, so pi's models.json must never carry a
// row for it (docs/design/pi-codex-provider-shadowing.md OQ-1, OQ-2). packs/openai-auth declares
// an openai-responses endpoint on openai-codex, and a row written from it shadows pi's own
// client, which then authenticates with the workspace's ambient OPENAI_API_KEY and gets 401s.
// The exclusion is by NAME, as packs/codex/derive.lua does; every other reachable provider is
// still catalogued.
func TestPiCatalogNeverCataloguesOpenAICodex(t *testing.T) {
	script, s := deriveSurface(t, "pi", "pi/models")
	got, _, err := deriveComputedLayer(&Env{Vars: map[string]string{}}, s, script,
		surfaceSelection{}, map[string]map[string]any{
			manifest.SourceProviders: {
				"openai-codex": map[string]any{
					"endpoints": map[string]any{"openai-responses": map[string]any{
						"base_url": "https://chatgpt.com/backend-api/codex"}},
				},
				"local": map[string]any{
					"endpoints": map[string]any{"openai": map[string]any{
						"base_url": "http://host.containers.internal:8080/v1"}},
					"models": map[string]any{"default": "qwen3.8-27b"},
				},
			},
		})
	if err != nil {
		t.Fatal(err)
	}
	provs, _ := got["providers"].(map[string]any)
	if _, shadowed := provs["openai-codex"]; shadowed {
		t.Fatalf("pi's models.json catalogues openai-codex, shadowing pi's built-in "+
			"subscription client: %#v", provs["openai-codex"])
	}
	if _, ok := provs["local"]; !ok {
		t.Fatalf("the exclusion dropped another provider too; catalog = %#v", provs)
	}
}

// The same property through the PRODUCTION pack set, where the openai-codex row comes from
// packs/openai-auth's own declaration rather than a hand-built table, and with no profile
// selected — the case the codex-profile test above does not reach.
func TestPiCatalogFromShippedPacksOmitsOpenAICodex(t *testing.T) {
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
	if _, ok := providers.Get("openai-codex"); !ok {
		t.Fatalf("the composed table has no openai-codex provider, so this case measures "+
			"nothing: %v", providers.Keys())
	}
	r := newPioencodeRender(t, mustCompactJSON(t, providers))
	r.render(t, `{}`)
	catalog, _ := r.piModels(t)["providers"].(map[string]any)
	if _, shadowed := catalog["openai-codex"]; shadowed {
		t.Fatalf("models.json shadows pi's built-in openai-codex provider: %#v", catalog)
	}
	if _, ok := catalog["kilo"]; !ok {
		t.Fatalf("kilo, a reachable provider, is missing from the catalog: %#v", catalog)
	}
}
