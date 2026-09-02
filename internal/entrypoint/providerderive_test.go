package entrypoint

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
)

// The provider half of the three derives that read ctx.providers. pi/models, codex/config
// and opencode/config each project the one provider table into their own dialect, and the
// tests here run each pack's REAL derive.lua through deriveComputedLayer — the same entry
// the boot loop and the host notch use — so a change to a gate or a default cannot land
// without one of these failing.
//
// Nothing here renders a file: the assertions are on the derive's output map, because the
// codec walk below the derive is covered by the prism tests and would only duplicate them.

// deriveSurface looks up one embedded pack's surface by "agent/name" and returns it with
// the pack's derive script. A surface that is not there is a test failure, not a nil
// dereference — a renamed surface should read as a moved target, not as a mystery.
func deriveSurface(t *testing.T, pack, id string) (string, manifest.Surface) {
	t.Helper()
	p, err := embeddedPack(pack)
	if err != nil {
		t.Fatalf("embedded %s: %v", pack, err)
	}
	script := loadPackDeriveScript(p)
	if script == "" {
		t.Fatalf("pack %s ships no derive.lua", pack)
	}
	surfaces, _ := p.SurfacesFor(false, nil)
	for _, s := range surfaces {
		if s.Agent+"/"+s.Name == id {
			return script, s
		}
	}
	t.Fatalf("pack %s declares no surface %s", pack, id)
	return "", manifest.Surface{}
}

// zaiEndpointsTable is the §5 shape: one provider whose URLs live under `endpoints`,
// keyed by protocol, with no top-level base_url at all. Before the endpoints closure
// rules this provider was silently absent from all three catalogs — every derive gated
// on prov.base_url.
func zaiEndpointsTable() map[string]map[string]any {
	return map[string]map[string]any{
		manifest.SourceProviders: {
			"zai": map[string]any{
				"api_key_env_name": "ZAI_API_KEY",
				"models":           map[string]any{"default": "glm-4.7", "fast": "glm-4.7-air"},
				"endpoints": map[string]any{
					"anthropic": map[string]any{"base_url": "https://api.z.ai/api/anthropic"},
					"openai": map[string]any{
						"base_url": "https://api.z.ai/api/paas/v4",
						"wire_api": "openai-chat",
					},
				},
			},
		},
	}
}

// TestProviderDerivesResolveAnEndpointsOnlyProvider pins the resolution half of the
// endpoints schema (zai-plumbing.md §5, closure rule 2): an agent that speaks `openai`
// emits the openai endpoint's URL and wire protocol, and an endpoints-only provider
// reaches every catalog instead of vanishing from it.
func TestProviderDerivesResolveAnEndpointsOnlyProvider(t *testing.T) {
	tables := zaiEndpointsTable()
	const (
		openaiURL = "https://api.z.ai/api/paas/v4"
		anthroURL = "https://api.z.ai/api/anthropic"
	)

	t.Run("pi", func(t *testing.T) {
		script, s := deriveSurface(t, "pi", "pi/models")
		got, err := deriveComputedLayer(&Env{Vars: map[string]string{}}, s, script, tables)
		if err != nil {
			t.Fatal(err)
		}
		providers, ok := got["providers"].(map[string]any)
		if !ok {
			t.Fatalf("pi/models produced no providers table: %#v", got)
		}
		zai, ok := providers["zai"].(map[string]any)
		if !ok {
			t.Fatalf("providers.zai missing: %#v", providers)
		}
		// pi speaks openai, so it takes the openai endpoint — not the one for the
		// protocol it cannot speak.
		if zai["baseUrl"] != openaiURL {
			t.Errorf("baseUrl = %v, want the openai endpoint %s", zai["baseUrl"], openaiURL)
		}
		if zai["api"] != "openai-chat" {
			t.Errorf("api = %v, want the endpoint's wire_api openai-chat", zai["api"])
		}
		// pi has no apiKeyEnv field (provider-table-fidelity.md §3.5 D11): its env
		// indirection is the config-value syntax ON apiKey, so the credential reaches pi
		// as "${ZAI_API_KEY}" — a reference pi expands at read time — and never as a
		// separate field pi's schema has no entry for.
		if zai["apiKey"] != "${ZAI_API_KEY}" {
			t.Errorf("apiKey = %v, want the ${ZAI_API_KEY} config-value reference pi expands", zai["apiKey"])
		}
		if _, present := zai["apiKeyEnv"]; present {
			t.Errorf("apiKeyEnv = %v, and pi has no such field — dead configuration (D11)", zai["apiKeyEnv"])
		}
		if _, present := zai["models"]; !present {
			t.Error("models alias map missing from the pi entry")
		}
	})

	t.Run("codex", func(t *testing.T) {
		script, s := deriveSurface(t, "codex", "codex/config")
		got, err := deriveComputedLayer(&Env{Vars: map[string]string{}}, s, script, tables)
		if err != nil {
			t.Fatal(err)
		}
		provs, ok := got["model_providers"].(map[string]any)
		if !ok {
			t.Fatalf("codex/config produced no model_providers table: %#v", got)
		}
		zai, ok := provs["zai"].(map[string]any)
		if !ok {
			t.Fatalf("model_providers.zai missing: %#v", provs)
		}
		if zai["base_url"] != openaiURL {
			t.Errorf("base_url = %v, want the openai endpoint %s", zai["base_url"], openaiURL)
		}
		if zai["wire_api"] != "openai-chat" {
			t.Errorf("wire_api = %v, want the endpoint's openai-chat", zai["wire_api"])
		}
		if zai["api_key_env"] != "ZAI_API_KEY" {
			t.Errorf("api_key_env = %v, want ZAI_API_KEY (codex's own field name, from our api_key_env_name)", zai["api_key_env"])
		}
	})

	t.Run("opencode", func(t *testing.T) {
		script, s := deriveSurface(t, "opencode", "opencode/config")
		got, err := deriveComputedLayer(&Env{Vars: map[string]string{}}, s, script, tables)
		if err != nil {
			t.Fatal(err)
		}
		provs, ok := got["provider"].(map[string]any)
		if !ok {
			t.Fatalf("opencode/config produced no provider table: %#v", got)
		}
		zai, ok := provs["zai"].(map[string]any)
		if !ok {
			t.Fatalf("provider.zai missing: %#v", provs)
		}
		// opencode reads baseURL/apiKey only inside `options` (provider-table-fidelity.md
		// §3.5 D10): the loader merges only provider.options and resolveSDK reads
		// { ...provider.options }, so a top-level value lists in /models and never dials.
		opts, ok := zai["options"].(map[string]any)
		if !ok {
			t.Fatalf("provider.zai has no options table: %#v", zai)
		}
		if opts["baseURL"] != openaiURL {
			t.Errorf("options.baseURL = %v, want the openai endpoint %s", opts["baseURL"], openaiURL)
		}
		if opts["apiKey"] != "{env:ZAI_API_KEY}" {
			t.Errorf("options.apiKey = %v, want the {env:ZAI_API_KEY} interpolation", opts["apiKey"])
		}
		// The negative half: the top-level spelling is the part opencode ignores, so both
		// halves must move together or the entry stays visible-but-unusable.
		if _, present := zai["baseURL"]; present {
			t.Errorf("top-level baseURL = %v, and opencode reads it only under options (D10)", zai["baseURL"])
		}
		if _, present := zai["apiKey"]; present {
			t.Errorf("top-level apiKey = %v, and opencode reads it only under options (D10)", zai["apiKey"])
		}
		if zai["npm"] != "@ai-sdk/openai-compatible" {
			t.Errorf("npm = %v, want the SDK package kept top-level (the one place upstream reads it)", zai["npm"])
		}
	})
}

// TestProviderDerivesKeepTheBaseURLShorthand: the single-protocol form still works and
// still carries the provider's own wire_api, which is what every provider written before
// `endpoints` existed relies on.
func TestProviderDerivesKeepTheBaseURLShorthand(t *testing.T) {
	tables := map[string]map[string]any{
		manifest.SourceProviders: {
			"glm": map[string]any{
				"base_url":         "https://open.bigmodel.cn/api/paas/v4",
				"wire_api":         "openai-chat",
				"api_key_env_name": "GLM_API_KEY",
			},
		},
	}
	script, s := deriveSurface(t, "pi", "pi/models")
	got, err := deriveComputedLayer(&Env{Vars: map[string]string{}}, s, script, tables)
	if err != nil {
		t.Fatal(err)
	}
	providers := got["providers"].(map[string]any)
	zai := providers["glm"].(map[string]any)
	if zai["baseUrl"] != "https://open.bigmodel.cn/api/paas/v4" {
		t.Errorf("baseUrl = %v, want the shorthand base_url", zai["baseUrl"])
	}
	if zai["api"] != "openai-chat" {
		t.Errorf("api = %v, want the provider's own wire_api", zai["api"])
	}
	// The credential rides the same config-value syntax on the shorthand path too — the
	// shorthand and the endpoints form share one body, and this is what keeps them shared.
	if zai["apiKey"] != "${GLM_API_KEY}" {
		t.Errorf("apiKey = %v, want the ${GLM_API_KEY} reference on the shorthand path too", zai["apiKey"])
	}
}

// TestCodexDeriveDefaultsToOpenaiChat pins the measured default (zai OQ-Z1: an
// authenticated probe 2026-09-01 put POST /v4/responses at 404 on both z.ai routes while
// /v4/chat/completions returned a real completion). A provider that omits wire_api is the
// ordinary case — it is optional — so the default decides which endpoint codex actually
// POSTs to, and "responses" wired every chat-only provider to a 404 that surfaced as a
// codex error rather than a config one.
func TestCodexDeriveDefaultsToOpenaiChat(t *testing.T) {
	tables := map[string]map[string]any{
		manifest.SourceProviders: {
			"zai": map[string]any{
				"base_url":         "https://api.z.ai/api/paas/v4",
				"api_key_env_name": "ZAI_API_KEY",
			},
		},
	}
	script, s := deriveSurface(t, "codex", "codex/config")
	got, err := deriveComputedLayer(&Env{Vars: map[string]string{}}, s, script, tables)
	if err != nil {
		t.Fatal(err)
	}
	provs, ok := got["model_providers"].(map[string]any)
	if !ok {
		t.Fatalf("codex/config produced no model_providers table: %#v", got)
	}
	zai, ok := provs["zai"].(map[string]any)
	if !ok {
		t.Fatalf("model_providers.zai missing: %#v", provs)
	}
	if zai["wire_api"] != "openai-chat" {
		t.Errorf("wire_api = %v, want the measured default openai-chat", zai["wire_api"])
	}
}

// TestProviderDerivesSkipAProviderWithNoURLForThem pins BOTH halves of the gate: a
// provider that names no URL at all is not a catalog row (the sentinel a host render
// probes with relies on this), and neither is one whose only endpoint speaks a protocol
// this agent cannot — emitting a URL-less entry would hand the agent a provider it
// cannot reach. Dropping the gate to `type(prov) == "table"` would put both in.
func TestProviderDerivesSkipAProviderWithNoURLForThem(t *testing.T) {
	tables := map[string]map[string]any{
		manifest.SourceProviders: {
			// The host-render sentinel entry: a table with neither key.
			"__yolo_table_probe__": map[string]any{"command": "probe"},
			// Anthropic-only: claude's endpoint, not one pi can speak.
			"claude_only": map[string]any{
				"api_key_env_name": "ANTHROPIC_API_KEY",
				"endpoints": map[string]any{
					"anthropic": map[string]any{"base_url": "https://api.anthropic.com"},
				},
			},
			// One provider pi CAN speak, so the assertion below proves selectivity
			// rather than an empty catalog: the derive omits the table wholesale when
			// nothing survives.
			"glm": map[string]any{
				"base_url": "https://open.bigmodel.cn/api/paas/v4",
			},
		},
	}
	script, s := deriveSurface(t, "pi", "pi/models")
	got, err := deriveComputedLayer(&Env{Vars: map[string]string{}}, s, script, tables)
	if err != nil {
		t.Fatal(err)
	}
	providers, ok := got["providers"].(map[string]any)
	if !ok {
		t.Fatalf("pi/models produced no providers table: %#v", got)
	}
	if _, present := providers["glm"]; !present {
		t.Errorf("the one openai-speaking provider must still be emitted: %#v", providers)
	}
	// glm names no api_key_env_name, so it must get NO apiKey key at all — an empty or
	// literal "$" value would read as a credential pi would try to expand.
	if got := providers["glm"].(map[string]any)["apiKey"]; got != nil {
		t.Errorf("a provider with no api_key_env_name must carry no apiKey, got %v", got)
	}
	for _, name := range []string{"__yolo_table_probe__", "claude_only"} {
		if _, present := providers[name]; present {
			t.Errorf("%s has no openai endpoint and must not become a pi catalog row: %#v", name, providers[name])
		}
	}
}
