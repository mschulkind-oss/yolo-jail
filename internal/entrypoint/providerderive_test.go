package entrypoint

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// TestGatewayProviderPacksDeclareOnlyStableFacts pins the two gateway packs' boundary:
// yolo owns the endpoint and credential-variable facts, while the user owns every model
// id. Reading the real embedded manifests makes a missing embed entry, a renamed pack, or
// a tempting shipped "recommended model" fail here rather than looking like a working
// provider catalog in one agent only.
func TestGatewayProviderPacksDeclareOnlyStableFacts(t *testing.T) {
	tests := []struct {
		pack, key, openAIURL, openAIWire, anthropicURL string
		needsBridge                                    bool
	}{
		{"openrouter", "OPENROUTER_API_KEY", "https://openrouter.ai/api/v1", "openai-responses", "https://openrouter.ai/api", false},
		{"kilo", "KILO_API_KEY", "https://api.kilo.ai/api/gateway", "openai-chat-completions", "http://127.0.0.1:8216", true},
	}
	for _, tt := range tests {
		t.Run(tt.pack, func(t *testing.T) {
			p, err := embeddedPack(tt.pack)
			if err != nil {
				t.Fatalf("embedded %s: %v", tt.pack, err)
			}
			providers := p.Decl.Providers()
			if len(providers) != 1 {
				t.Fatalf("%s contributes %d providers, want one", tt.pack, len(providers))
			}
			provider := providers[0]
			if provider.Name != tt.pack || provider.APIKeyEnvName != tt.key {
				t.Fatalf("provider = %+v, want %q with %s", provider, tt.pack, tt.key)
			}
			if len(provider.Models) != 0 {
				t.Errorf("%s ships models %#v; gateway model ids are user-curated", tt.pack, provider.Models)
			}
			if provider.Options["model"].Defaulted {
				t.Errorf("%s defaults a model; selecting its profile must not silently choose one", tt.pack)
			}
			openai := provider.Endpoints["openai"]
			if openai.BaseURL != tt.openAIURL || openai.WireAPI != tt.openAIWire {
				t.Errorf("openai endpoint = %+v, want %s (%s)", openai, tt.openAIURL, tt.openAIWire)
			}
			if got := provider.Endpoints["anthropic"].BaseURL; got != tt.anthropicURL {
				t.Errorf("anthropic endpoint = %q, want %q", got, tt.anthropicURL)
			}
			profiles := p.Decl.Profiles()
			if len(profiles) != 1 || profiles[0].Name != tt.pack || profiles[0].Provider != tt.pack {
				t.Errorf("profiles = %+v, want one same-named provider profile", profiles)
			}
			bridge := false
			for _, need := range p.Decl.DeclaredNeeds() {
				if need.Pack == "wire-bridge" && len(need.WhenBins) == 2 && need.WhenBins[0] == "claude" && need.WhenBins[1] == "copilot" {
					bridge = true
				}
			}
			if bridge != tt.needsBridge {
				t.Errorf("wire-bridge need = %v, want %v", bridge, tt.needsBridge)
			}
		})
	}
}

// TestCodexDeriveUsesCodexCredentialField is a regression test for custom-provider
// credentials. Codex reads `env_key`; `api_key_env` looks plausible but is ignored, so
// every routed provider becomes an anonymous first request.
func TestCodexDeriveUsesCodexCredentialField(t *testing.T) {
	script, s := deriveSurface(t, "codex", "codex/config")
	got, err := deriveComputedLayer(&Env{Vars: map[string]string{}}, s, script, surfaceSelection{}, map[string]map[string]any{
		manifest.SourceProviders: {
			"router": map[string]any{
				"api_key_env_name": "ROUTER_API_KEY",
				"endpoints": map[string]any{
					"openai": map[string]any{"base_url": "https://router.example/v1", "wire_api": "openai-responses"},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry := got["model_providers"].(map[string]any)["router"].(map[string]any)
	if entry["env_key"] != "ROUTER_API_KEY" {
		t.Errorf("env_key = %v, want ROUTER_API_KEY", entry["env_key"])
	}
	if _, present := entry["api_key_env"]; present {
		t.Errorf("api_key_env = %v, but Codex ignores that field", entry["api_key_env"])
	}
}

// TestCodexDeriveSetsModelProviderName pins that every model_providers.<id> table
// carries a non-empty `name` field (defaulting to the provider key if no display
// name is set). Codex CLI strictly refuses config.toml with
// "provider name must not be empty" otherwise.
func TestCodexDeriveSetsModelProviderName(t *testing.T) {
	script, s := deriveSurface(t, "codex", "codex/config")
	got, err := deriveComputedLayer(&Env{Vars: map[string]string{}}, s, script, surfaceSelection{}, map[string]map[string]any{
		manifest.SourceProviders: {
			"local": map[string]any{
				"endpoints": map[string]any{
					"openai": map[string]any{"base_url": "http://localhost:11434/v1", "wire_api": "openai-responses"},
				},
			},
			"custom": map[string]any{
				"name": "Custom Provider Name",
				"endpoints": map[string]any{
					"openai": map[string]any{"base_url": "http://localhost:8080/v1", "wire_api": "openai-responses"},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	provs, ok := got["model_providers"].(map[string]any)
	if !ok {
		t.Fatalf("model_providers missing: %#v", got)
	}
	localEntry := provs["local"].(map[string]any)
	if localEntry["name"] != "local" {
		t.Errorf("local.name = %v, want %q (defaulting to provider key so codex does not reject empty name)", localEntry["name"], "local")
	}
	customEntry := provs["custom"].(map[string]any)
	if customEntry["name"] != "Custom Provider Name" {
		t.Errorf("custom.name = %v, want %q", customEntry["name"], "Custom Provider Name")
	}
}

// TestPiDeriveHandlesLocalProviderContextAndKey pins that an unkeyed local provider
// receives apiKey: "local" (so pi can resolve its saved default), maps context_window
// into contextWindow without setting maxTokens unless explicitly configured (avoiding 400
// errors on endpoints with output token limits), and maps max_tokens when specified.
func TestPiDeriveHandlesLocalProviderContextAndKey(t *testing.T) {
	script, s := deriveSurface(t, "pi", "pi/models")
	got, err := deriveComputedLayer(&Env{Vars: map[string]string{}}, s, script, surfaceSelection{}, map[string]map[string]any{
		manifest.SourceProviders: {
			"local": map[string]any{
				"base_url": "http://host.containers.internal:8080/v1",
				"models":   map[string]any{"default": "qwen3.8-27b"},
				"options":  map[string]any{"context_window": "180224"},
			},
			"local_with_max": map[string]any{
				"base_url": "http://host.containers.internal:8080/v1",
				"models":   map[string]any{"default": "qwen3.8-27b"},
				"options":  map[string]any{"context_window": "180224", "max_tokens": "8192"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	provs := got["providers"].(map[string]any)
	local := provs["local"].(map[string]any)
	if local["apiKey"] != "local" {
		t.Errorf("apiKey = %v, want 'local' for unkeyed local provider", local["apiKey"])
	}
	models := local["models"].([]any)
	if len(models) == 0 {
		t.Fatal("no models in local provider")
	}
	model := models[0].(map[string]any)
	if model["contextWindow"] != float64(180224) && model["contextWindow"] != int64(180224) && model["contextWindow"] != 180224 {
		t.Errorf("contextWindow = %v, want 180224", model["contextWindow"])
	}
	if mt, ok := model["maxTokens"]; ok {
		t.Errorf("maxTokens should be omitted when not explicitly set, got %v", mt)
	}

	localWithMax := provs["local_with_max"].(map[string]any)
	modelsWithMax := localWithMax["models"].([]any)
	if len(modelsWithMax) == 0 {
		t.Fatal("no models in local_with_max provider")
	}
	modelWithMax := modelsWithMax[0].(map[string]any)
	if modelWithMax["maxTokens"] != float64(8192) && modelWithMax["maxTokens"] != int64(8192) && modelWithMax["maxTokens"] != 8192 {
		t.Errorf("maxTokens = %v, want 8192", modelWithMax["maxTokens"])
	}
}

// TestPiDeriveSettingsScopesDeclaredModels verifies that pi settings derive scopes enabledModels
// to the declared curated models of the active provider instead of wildcarding, hiding uncurated models.
func TestPiDeriveSettingsScopesDeclaredModels(t *testing.T) {
	script, s := deriveSurface(t, "pi", "pi/settings")

	// 1. Provider with declared models (like zai)
	got, err := deriveComputedLayer(&Env{Vars: map[string]string{}}, s, script, surfaceSelection{
		Profile:  "zai",
		Provider: "zai",
	}, map[string]map[string]any{
		manifest.SourceProviders: {
			"zai": map[string]any{
				"base_url": "https://api.z.ai/api/coding/paas/v4",
				"models": map[string]any{
					"glm-4.6":       "glm-4.6",
					"glm-5.3":       "glm-5.3",
					"glm-5.3-flash": "glm-5.3-flash",
				},
				"options": map[string]any{
					"model": "glm-5.3",
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	enabled, ok := got["enabledModels"].([]any)
	if !ok {
		t.Fatalf("enabledModels missing or not a slice: %#v", got)
	}
	want := []string{"zai/glm-4.6", "zai/glm-5.3", "zai/glm-5.3-flash"}
	if len(enabled) != len(want) {
		t.Fatalf("enabledModels = %v, want %v", enabled, want)
	}
	for i, w := range want {
		if enabled[i] != w {
			t.Errorf("enabledModels[%d] = %v, want %s", i, enabled[i], w)
		}
	}
	sel, ok := got["selection"].(map[string]any)
	if !ok {
		t.Fatalf("selection missing or not a map: %#v", got)
	}
	if sel["defaultProvider"] != "zai" {
		t.Errorf("selection.defaultProvider = %v, want 'zai'", sel["defaultProvider"])
	}
	if sel["defaultModel"] != "glm-5.3" {
		t.Errorf("selection.defaultModel = %v, want 'glm-5.3'", sel["defaultModel"])
	}

	// 2. Provider without declared models (like kilo) falls back to wildcard
	gotKilo, err := deriveComputedLayer(&Env{Vars: map[string]string{}}, s, script, surfaceSelection{
		Profile:  "kilo",
		Provider: "kilo",
	}, map[string]map[string]any{
		manifest.SourceProviders: {
			"kilo": map[string]any{
				"base_url": "https://api.kilo.ai/api/gateway",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	kiloEnabled, ok := gotKilo["enabledModels"].([]any)
	if !ok || len(kiloEnabled) != 1 || kiloEnabled[0] != "kilo/*" {
		t.Errorf("kilo enabledModels = %v, want ['kilo/*']", gotKilo["enabledModels"])
	}
}

// TestOpenCodeDeriveHandlesLocalProviderLimitAndSmallModel pins that an unkeyed local
// endpoint receives apiKey: "local", context_window maps to limit.context on model entries,
// and selection sets small_model to prevent opencode from phoning home to gpt-5-nano.
func TestOpenCodeDeriveHandlesLocalProviderLimitAndSmallModel(t *testing.T) {
	script, s := deriveSurface(t, "opencode", "opencode/config")
	got, err := deriveComputedLayer(&Env{Vars: map[string]string{}}, s, script, surfaceSelection{
		Profile:  "local",
		Provider: "local",
	}, map[string]map[string]any{
		manifest.SourceProviders: {
			"local": map[string]any{
				"base_url": "http://host.containers.internal:8080/v1",
				"models":   map[string]any{"default": "qwen3.8-27b"},
				"options":  map[string]any{"context_window": "180224"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	provs, ok := got["provider"].(map[string]any)
	if !ok {
		t.Fatalf("opencode/config produced no provider table: %#v", got)
	}
	localProv, ok := provs["local"].(map[string]any)
	if !ok {
		t.Fatalf("provider.local missing: %#v", provs)
	}
	opts, ok := localProv["options"].(map[string]any)
	if !ok {
		t.Fatalf("provider.local has no options table: %#v", localProv)
	}
	if opts["apiKey"] != "local" {
		t.Errorf("options.apiKey = %v, want 'local' for unkeyed local endpoint", opts["apiKey"])
	}
	models, ok := localProv["models"].(map[string]any)
	if !ok {
		t.Fatalf("provider.local has no models table: %#v", localProv)
	}
	m, ok := models["qwen3.8-27b"].(map[string]any)
	if !ok {
		t.Fatalf("model qwen3.8-27b missing: %#v", models)
	}
	limit, ok := m["limit"].(map[string]any)
	if !ok {
		t.Fatalf("model qwen3.8-27b has no limit table: %#v", m)
	}
	if limit["context"] != float64(180224) && limit["context"] != int64(180224) && limit["context"] != 180224 {
		t.Errorf("limit.context = %v, want 180224", limit["context"])
	}
	sel, ok := got["selection"].(map[string]any)
	if !ok {
		t.Fatalf("opencode selection missing: %#v", got)
	}
	if sel["model"] != "local/qwen3.8-27b" {
		t.Errorf("selection.model = %v, want local/qwen3.8-27b", sel["model"])
	}
	if sel["small_model"] != "local/qwen3.8-27b" {
		t.Errorf("selection.small_model = %v, want local/qwen3.8-27b", sel["small_model"])
	}
}

// TestZaiCodingPlanPackCuratesItsThreeModels pins the Coding Plan contract rather
// than Z.ai's much broader PAYG catalog. The default must be GLM-5.3, whose 1M
// context window is the provider-level value Claude's derive uses for a selected
// default profile.
func TestZaiCodingPlanPackCuratesItsThreeModels(t *testing.T) {
	p, err := embeddedPack("zai")
	if err != nil {
		t.Fatalf("embedded zai: %v", err)
	}
	providers := p.Decl.Providers()
	if len(providers) != 1 {
		t.Fatalf("zai contributes %d providers, want one", len(providers))
	}
	provider := providers[0]
	if got := provider.Endpoints["openai"].BaseURL; got != "https://api.z.ai/api/coding/paas/v4" {
		t.Errorf("openai endpoint = %q, want Coding Plan endpoint", got)
	}
	wantModels := map[string]string{
		"glm-4.6":       "glm-4.6",
		"glm-5.3":       "glm-5.3",
		"glm-5.3-flash": "glm-5.3-flash",
	}
	if len(provider.Models) != len(wantModels) {
		t.Fatalf("models = %#v, want exactly %#v", provider.Models, wantModels)
	}
	for alias, model := range wantModels {
		if got := provider.Models[alias]; got != model {
			t.Errorf("models[%q] = %q, want %q", alias, got, model)
		}
	}
	if got := provider.Options["model"]; !got.Defaulted || got.Value != "glm-5.3" {
		t.Errorf("model option = %+v, want default glm-5.3", got)
	}
	if got := provider.Options["context_window"]; !got.Defaulted || got.Value != "1000000" {
		t.Errorf("context_window option = %+v, want 1000000 for GLM-5.3", got)
	}
}

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
	script := packload.DeriveScript(p)
	if script == "" {
		t.Fatalf("pack %s ships no derive.lua", pack)
	}
	surfaces, _ := p.SurfacesFor(false)
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
				"models":           map[string]any{"default": "glm-5.3[1m]", "fast": "glm-5.3-flash[1m]"},
				"endpoints": map[string]any{
					"anthropic": map[string]any{"base_url": "https://api.z.ai/api/anthropic"},
					"openai": map[string]any{
						"base_url": "https://api.z.ai/api/paas/v4",
						"wire_api": "openai-chat-completions",
					},
				},
			},
		},
	}
}

// TestProviderDerivesResolveAnEndpointsOnlyProvider pins the resolution half of the
// endpoints schema (zai-plumbing.md §5, closure rule 2) against the canonical vocabulary
// (docs/reference/providers.md): the composed wire_api is YOLO's canonical protocol
// name, so what reaches an agent is that agent's own spelling of the protocol its endpoint
// resolves to — or, when the agent cannot speak it, NO entry at all.
func TestProviderDerivesResolveAnEndpointsOnlyProvider(t *testing.T) {
	tables := zaiEndpointsTable()
	const openaiURL = "https://api.z.ai/api/paas/v4"

	t.Run("pi", func(t *testing.T) {
		script, s := deriveSurface(t, "pi", "pi/models")
		got, err := deriveComputedLayer(&Env{Vars: map[string]string{}}, s, script, surfaceSelection{}, tables)
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
		// pi resolves `openai`, so it takes the openai endpoint — not the one for the
		// protocol it is not here to speak — and the endpoint's CANONICAL
		// openai-chat-completions reaches pi translated into pi's own spelling, not
		// verbatim (OQ-PT1).
		if zai["baseUrl"] != openaiURL {
			t.Errorf("baseUrl = %v, want the openai endpoint %s", zai["baseUrl"], openaiURL)
		}
		if zai["api"] != "openai-completions" {
			t.Errorf("api = %v, want pi's spelling of chat completions, openai-completions — "+
				"the canonical openai-chat-completions translated, never passed through "+
				"(nothing consumes yolo's spelling)", zai["api"])
		}
		// pi has no apiKeyEnv field (docs/reference/providers.md D11): its env
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
		got, err := deriveComputedLayer(&Env{Vars: map[string]string{}}, s, script, surfaceSelection{}, tables)
		if err != nil {
			t.Fatal(err)
		}
		// z.ai speaks chat completions only and codex speaks responses only, so the openai
		// endpoint's canonical openai-chat-completions has NO codex spelling: the derive
		// emits no zai entry at all rather than one that 404s at first request (design
		// §3.3 — a fact about the world, not a bug). Dropping the entry is the fix; a
		// wire_api value that made it "work" would be the defect reintroduced.
		provs, present := got["model_providers"].(map[string]any)
		if !present {
			return // no provider survived: nothing for codex, which is the assertion
		}
		if zai, still := provs["zai"]; still {
			t.Fatalf("codex got a zai entry (%#v), and none is reachable: z.ai's openai "+
				"route speaks chat completions, codex accepts responses only — the derive "+
				"must emit nothing for a protocol it cannot speak (§3.4)", zai)
		}
	})

	t.Run("opencode", func(t *testing.T) {
		script, s := deriveSurface(t, "opencode", "opencode/config")
		got, err := deriveComputedLayer(&Env{Vars: map[string]string{}}, s, script, surfaceSelection{}, tables)
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
		// opencode reads baseURL/apiKey only inside `options` (docs/reference/providers.md
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

// TestClaudeDeriveReadsTheUseProfilesTable pins the one thing the claude/config derive
// needs from core: that the selection table reaches it under the name its Lua reads.
// The derive reads `ctx.use_profiles.claude` and falls back to "default" when that index
// is nil — a silent skip, not an error — so a rename on either side of the seam (the
// Source* constant that keys the table, the knownDeriveSources entry that exposes it,
// or the field name in packs/claude/derive.lua) would flip every jail back to
// subscription mode without anything anywhere failing. All three are in this one
// assertion's path, which is why it runs the REAL derive.lua rather than a copy.
func TestClaudeDeriveReadsTheUseProfilesTable(t *testing.T) {
	script, s := deriveSurface(t, "claude", "claude/config")
	search := map[string]any{"command": "mcp-search", "provides": "web_search"}

	mcpServers := func() map[string]map[string]any {
		return map[string]map[string]any{
			manifest.SourceMCPServers: {"search": search},
		}
	}
	render := func(tables map[string]map[string]any) map[string]any {
		t.Helper()
		got, err := deriveComputedLayer(&Env{Vars: map[string]string{}}, s, script, surfaceSelection{}, tables)
		if err != nil {
			t.Fatal(err)
		}
		servers, ok := got["mcpServers"].(map[string]any)
		if !ok {
			t.Fatalf("claude/config produced no mcpServers table: %#v", got)
		}
		return servers
	}

	selected := func(profile any) map[string]map[string]any {
		tables := mcpServers()
		tables[manifest.SourceUseProfiles] = map[string]any{"claude": profile}
		return tables
	}

	// Bedrock mode passes the search MCPs through: native web search is a first-party
	// subscription feature, so the suppression that runs otherwise must not.
	if _, present := render(selected("bedrock"))["search"]; !present {
		t.Error("a bedrock selection must keep the web_search MCP server")
	}
	if _, present := render(selected("glm"))["search"]; present {
		t.Error("a non-bedrock selection must suppress the web_search MCP server")
	}
	// No selection at all is the same as a non-bedrock one (OQ-CS2: the no-profile case
	// is the agent's own default), and it is reached through the same nil fallback this
	// test exists to observe.
	if _, present := render(mcpServers())["search"]; present {
		t.Error("no use_profiles table must behave as the agent's own default, not bedrock")
	}
}

// TestProviderDerivesKeepTheBaseURLShorthand: the single-protocol form still works and
// still carries the provider's own wire_api — translated, like every other path — which is
// what every provider written before `endpoints` existed relies on.
func TestProviderDerivesKeepTheBaseURLShorthand(t *testing.T) {
	tables := map[string]map[string]any{
		manifest.SourceProviders: {
			"glm": map[string]any{
				"base_url":         "https://open.bigmodel.cn/api/paas/v4",
				"wire_api":         "openai-chat-completions",
				"api_key_env_name": "GLM_API_KEY",
			},
		},
	}
	script, s := deriveSurface(t, "pi", "pi/models")
	got, err := deriveComputedLayer(&Env{Vars: map[string]string{}}, s, script, surfaceSelection{}, tables)
	if err != nil {
		t.Fatal(err)
	}
	providers := got["providers"].(map[string]any)
	zai := providers["glm"].(map[string]any)
	if zai["baseUrl"] != "https://open.bigmodel.cn/api/paas/v4" {
		t.Errorf("baseUrl = %v, want the shorthand base_url", zai["baseUrl"])
	}
	if zai["api"] != "openai-completions" {
		t.Errorf("api = %v, want the shorthand's canonical openai-chat-completions translated "+
			"into pi's openai-completions", zai["api"])
	}
	// The credential rides the same config-value syntax on the shorthand path too — the
	// shorthand and the endpoints form share one body, and this is what keeps them shared.
	if zai["apiKey"] != "${GLM_API_KEY}" {
		t.Errorf("apiKey = %v, want the ${GLM_API_KEY} reference on the shorthand path too", zai["apiKey"])
	}
}

// TestProviderDerivesTranslateTheCanonicalVocabulary is the dialect map, asserted row by
// row (docs/reference/providers.md, OQ-PT1): yolo's canonical protocol name goes IN,
// each agent's own spelling comes OUT, and a canonical value the agent cannot speak
// produces NO entry rather than a half-configured one. Every row runs both derives over
// the same provider, so a translation added for one agent and forgotten for the other
// cannot land, and neither can a pass-through — no agent reads yolo's spelling, so a
// verbatim value shows up here as a wrong `api`/`wire_api` or as a dropped entry.
//
// The undeclared row is the only default in the table, and it is deliberately an agent
// fact rather than an endpoint one: codex has exactly one value it accepts, so that is
// what an omitted wire_api means for it; pi has NO default (an absent api is a
// composition error that deletes the provider, pi 0.84.4 provider-composer.js:48-52), so
// the derive must choose, and it chooses pi's chat-completions spelling — the protocol
// the `openai` endpoint key names (zai-plumbing.md §5). The last row is not a default at
// all but the version-skew case: a canonical name these derives predate, which both maps
// must treat as unspeakable rather than pass through.
func TestProviderDerivesTranslateTheCanonicalVocabulary(t *testing.T) {
	const url = "https://provider.example/v1"
	cases := []struct {
		name      string
		wireAPI   string // "" declares nothing
		wantPi    string // pi's `api`, or "" for NO pi entry
		wantCodex string // codex's `wire_api`, or "" for NO codex entry
	}{
		{name: "openai-chat-completions", wireAPI: "openai-chat-completions",
			wantPi: "openai-completions", wantCodex: ""},
		{name: "openai-responses", wireAPI: "openai-responses",
			wantPi: "openai-responses", wantCodex: "responses"},
		{name: "anthropic", wireAPI: "anthropic",
			wantPi: "anthropic-messages", wantCodex: ""},
		{name: "undeclared", wireAPI: "",
			wantPi: "openai-completions", wantCodex: "responses"},
		// A canonical name this build's derives have never heard of. Reachable without any
		// authoring mistake: packdecl refuses an unknown wire_api when the MANIFEST is read
		// and internal/config refuses one in user config, but the composed table crosses the
		// host→jail boundary as data, so a newer host staging a fourth protocol lands in THIS
		// jail's older derives unvalidated (packdecl's unknownWireAPISkip is the same skew,
		// seen from the other side). Both dialect maps miss, so BOTH agents must get no
		// entry — this row exists because pi's drop path otherwise has no input at all: the
		// three real names all have pi spellings, so only codex's was exercised.
		// "openai-realtime" is a stand-in for any such future name.
		{name: "canonical-name-this-build-does-not-know", wireAPI: "openai-realtime",
			wantPi: "", wantCodex: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prov := map[string]any{"base_url": url}
			if tc.wireAPI != "" {
				prov["wire_api"] = tc.wireAPI
			}
			tables := map[string]map[string]any{
				manifest.SourceProviders: {"acme": prov},
			}

			t.Run("pi", func(t *testing.T) {
				script, s := deriveSurface(t, "pi", "pi/models")
				got, err := deriveComputedLayer(&Env{Vars: map[string]string{}}, s, script, surfaceSelection{}, tables)
				if err != nil {
					t.Fatal(err)
				}
				providers, _ := got["providers"].(map[string]any)
				entry, present := providers["acme"].(map[string]any)
				if tc.wantPi == "" {
					if present {
						t.Fatalf("pi got an entry (%#v) for a protocol it cannot speak — the "+
							"derive must emit nothing rather than a provider that fails at "+
							"first request", entry)
					}
					return
				}
				if !present {
					t.Fatalf("pi/models has no acme entry: %#v", got)
				}
				if entry["api"] != tc.wantPi {
					t.Errorf("api = %v, want pi's %q (canonical %q translated, never passed "+
						"through — no agent reads yolo's spelling)", entry["api"], tc.wantPi, tc.wireAPI)
				}
				if entry["baseUrl"] != url {
					t.Errorf("baseUrl = %v, want the provider's %s", entry["baseUrl"], url)
				}
			})

			t.Run("codex", func(t *testing.T) {
				script, s := deriveSurface(t, "codex", "codex/config")
				got, err := deriveComputedLayer(&Env{Vars: map[string]string{}}, s, script, surfaceSelection{}, tables)
				if err != nil {
					t.Fatal(err)
				}
				provs, _ := got["model_providers"].(map[string]any)
				entry, present := provs["acme"].(map[string]any)
				if tc.wantCodex == "" {
					if present {
						t.Fatalf("codex got an entry (%#v) for a protocol it cannot speak — the "+
							"derive must emit nothing rather than a wire_api codex refuses "+
							"(chat was removed from the product)", entry)
					}
					return
				}
				if !present {
					t.Fatalf("codex/config has no acme entry: %#v", got)
				}
				if entry["wire_api"] != tc.wantCodex {
					t.Errorf("wire_api = %v, want codex's %q (canonical %q translated)", entry["wire_api"], tc.wantCodex, tc.wireAPI)
				}
				if entry["base_url"] != url {
					t.Errorf("base_url = %v, want the provider's %s", entry["base_url"], url)
				}
			})
		})
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
			// Anthropic-endpoint-only. pi CAN speak Anthropic Messages
			// (anthropic-messages is in its registry) — the reason this is not a pi row
			// is the resolution table (zai-plumbing.md §5): pi resolves `openai`, and an
			// endpoints-only provider with no openai endpoint names no URL for the
			// protocol pi resolves to. Inventing an entry here would point pi's
			// chat-completions api at an anthropic URL.
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
	got, err := deriveComputedLayer(&Env{Vars: map[string]string{}}, s, script, surfaceSelection{}, tables)
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
			t.Errorf("%s names no openai endpoint — no URL for the protocol pi resolves to — "+
				"and must not become a pi catalog row: %#v", name, providers[name])
		}
	}
}

// TestPiDeriveHandlesKiloGatewayNormalizationAndContextWindow pins that Kilo gateway model IDs
// (e.g. deepseek-v4.1-flash) are normalized to deepseek/deepseek-v4.1-flash, the context window
// defaults to 1,048,576 for DeepSeek models, and settings.json selection matches the normalized ID.
func TestPiDeriveHandlesKiloGatewayNormalizationAndContextWindow(t *testing.T) {
	scriptModels, sModels := deriveSurface(t, "pi", "pi/models")
	gotModels, err := deriveComputedLayer(&Env{Vars: map[string]string{}}, sModels, scriptModels, surfaceSelection{
		Profile:  "kilo",
		Provider: "kilo",
	}, map[string]map[string]any{
		manifest.SourceProviders: {
			"kilo": map[string]any{
				"base_url": "https://api.kilo.ai/api/gateway",
				"models":   map[string]any{"default": "deepseek-v4.1-flash"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	provs := gotModels["providers"].(map[string]any)
	kilo := provs["kilo"].(map[string]any)
	models := kilo["models"].([]any)
	if len(models) == 0 {
		t.Fatal("no models in kilo provider")
	}
	model := models[0].(map[string]any)
	if model["id"] != "deepseek/deepseek-v4.1-flash" {
		t.Errorf("model id = %v, want deepseek/deepseek-v4.1-flash", model["id"])
	}
	if model["contextWindow"] != float64(1048576) && model["contextWindow"] != int64(1048576) && model["contextWindow"] != 1048576 {
		t.Errorf("contextWindow = %v, want 1048576", model["contextWindow"])
	}

	scriptSettings, sSettings := deriveSurface(t, "pi", "pi/settings")
	gotSettings, err := deriveComputedLayer(&Env{Vars: map[string]string{}}, sSettings, scriptSettings, surfaceSelection{
		Profile:  "kilo",
		Provider: "kilo",
	}, map[string]map[string]any{
		manifest.SourceProviders: {
			"kilo": map[string]any{
				"base_url": "https://api.kilo.ai/api/gateway",
				"models":   map[string]any{"default": "deepseek-v4.1-flash"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	sel, ok := gotSettings["selection"].(map[string]any)
	if !ok {
		t.Fatalf("selection missing or not a map: %#v", gotSettings)
	}
	if sel["defaultProvider"] != "kilo" {
		t.Errorf("defaultProvider = %v, want 'kilo'", sel["defaultProvider"])
	}
	if sel["defaultModel"] != "deepseek/deepseek-v4.1-flash" {
		t.Errorf("defaultModel = %v, want 'deepseek/deepseek-v4.1-flash'", sel["defaultModel"])
	}
	enabled, ok := gotSettings["enabledModels"].([]any)
	if !ok {
		t.Fatalf("enabledModels missing: %#v", gotSettings)
	}
	found := false
	for _, em := range enabled {
		if em == "kilo/deepseek/deepseek-v4.1-flash" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("enabledModels = %v, want it to contain kilo/deepseek/deepseek-v4.1-flash", enabled)
	}
}
