package entrypoint

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// codexviaderive_test.go pins codex's half of the via route's Responses wire
// (docs/design/wire-bridge-gateway.md §4.1, WG-I20/WG-I22): the real packs/codex/derive.lua
// writes ctx.via_url as the SELECTED provider's base_url, speaking responses, with no
// env_key, and leaves every other row and every non-via launch as it was.

func codexViaProvidersTable() map[string]any {
	return map[string]any{
		"router": map[string]any{
			"api_key_env_name": "ROUTER_API_KEY",
			"endpoints": map[string]any{"openai": map[string]any{
				"base_url": "https://router.example/api/v1", "wire_api": "openai-responses"}},
			"models": map[string]any{"default": "openai/gpt-oss-120b"},
		},
		"other": map[string]any{
			"api_key_env_name": "OTHER_KEY",
			"endpoints": map[string]any{"openai": map[string]any{
				"base_url": "https://other.example/v1", "wire_api": "openai-responses"}},
		},
		"zai": map[string]any{
			"api_key_env_name": "ZAI_API_KEY",
			"endpoints": map[string]any{"openai": map[string]any{
				"base_url": "https://api.z.ai/api/paas/v4", "wire_api": "openai-chat-completions"}},
		},
		"openai-codex": map[string]any{
			"endpoints": map[string]any{"openai-responses": map[string]any{
				"base_url": "https://chatgpt.com/backend-api/codex", "wire_api": "openai-responses"}},
		},
	}
}

func codexConfigFor(t *testing.T, sel surfaceSelection) map[string]any {
	t.Helper()
	script, s := deriveSurface(t, "codex", "codex/config")
	got, _, err := deriveComputedLayer(&Env{Vars: map[string]string{}}, s, script, sel,
		map[string]map[string]any{manifest.SourceProviders: codexViaProvidersTable()})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func codexRow(t *testing.T, got map[string]any, name string) map[string]any {
	t.Helper()
	provs, _ := got["model_providers"].(map[string]any)
	row, _ := provs[name].(map[string]any)
	return row
}

func TestCodexDeriveWritesTheViaURLForTheSelectedProviderOnly(t *testing.T) {
	via := viaBase + "/agent/codex"
	got := codexConfigFor(t, surfaceSelection{Profile: "pr", Provider: "router", ViaURL: via})

	router := codexRow(t, got, "router")
	if router["base_url"] != via || router["wire_api"] != "responses" {
		t.Errorf("router row = %v, want the via URL speaking responses", router)
	}
	if _, has := router["env_key"]; has {
		t.Errorf("router row = %v: a via row names no env_key (the service holds the key)", router)
	}
	if other := codexRow(t, got, "other"); other["base_url"] != "https://other.example/v1" || other["env_key"] != "OTHER_KEY" {
		t.Errorf("a provider the profile does not select must keep its own URL and key: %v", other)
	}
	sel, _ := got["selection"].(map[string]any)
	if sel["model_provider"] != "router" || sel["model"] != "openai/gpt-oss-120b" {
		t.Errorf("selection = %v, want router and its default model", sel)
	}

	native := codexConfigFor(t, surfaceSelection{Profile: "pr", Provider: "router"})
	if r := codexRow(t, native, "router"); r["base_url"] != "https://router.example/api/v1" || r["env_key"] != "ROUTER_API_KEY" {
		t.Errorf("with no via the selected provider must keep its own URL and key: %v", r)
	}
}

// TestCodexDeriveGivesNoViaRowToAProviderItCannotReach: the via row rides the catalog's
// own gate. A chat-only provider gets no row and no selection with or without via, and the
// ChatGPT subscription — which codex implements natively — never gets a row at all.
func TestCodexDeriveGivesNoViaRowToAProviderItCannotReach(t *testing.T) {
	via := viaBase + "/agent/codex"
	got := codexConfigFor(t, surfaceSelection{Profile: "pz", Provider: "zai", ViaURL: via})
	if row := codexRow(t, got, "zai"); row != nil {
		t.Errorf("zai row = %v, want none: codex cannot speak chat-completions", row)
	}
	if sel, _ := got["selection"].(map[string]any); sel != nil {
		t.Errorf("selection = %v, want none for an unreachable provider", sel)
	}

	sub := codexConfigFor(t, surfaceSelection{Profile: "codex", Provider: "openai-codex", ViaURL: via})
	if row := codexRow(t, sub, "openai-codex"); row != nil {
		t.Errorf("openai-codex row = %v, want none: codex implements the subscription natively", row)
	}
	if sel, _ := sub["selection"].(map[string]any); sel["model_provider"] != nil || sel["model"] == nil {
		t.Errorf("selection = %v, want the model alone (codex's own subscription client)", sel)
	}
}

// TestAViaProfileReachesCodexThroughTheWire is the jail-side chain for codex: the host's
// writer produces YOLO_PROFILES, the boot reader decodes it, the per-surface selection
// keys ctx.via_url by agent, and the real codex derive writes it — only for the agent
// whose active profile is the via profile.
func TestAViaProfileReachesCodexThroughTheWire(t *testing.T) {
	wire, err := jsonx.DumpsCompact(packload.ProfilesWireTable(map[string]packload.ResolvedProfile{
		"pr":    {Provider: "router", Via: "wire-bridge", ViaBase: viaBase},
		"plain": {Provider: "router"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	e := &Env{Vars: map[string]string{"YOLO_PROFILES": wire}}
	surface := manifest.Surface{Agent: "codex", Name: "config"}

	sel := surfaceSelectionFor(nil, e.LoadProfiles(), map[string]string{"codex": "pr", "pi": "plain"}, surface)
	if r := codexRow(t, codexConfigFor(t, sel), "router"); r["base_url"] != viaBase+"/agent/codex" {
		t.Errorf("codex's router base_url = %v, want %s/agent/codex", r["base_url"], viaBase)
	}

	// The via profile active for ANOTHER agent gives codex nothing.
	sel = surfaceSelectionFor(nil, e.LoadProfiles(), map[string]string{"codex": "plain", "pi": "pr"}, surface)
	if sel.ViaURL != "" {
		t.Errorf("codex ViaURL = %q, want none (the via profile is pi's)", sel.ViaURL)
	}
	if r := codexRow(t, codexConfigFor(t, sel), "router"); r["base_url"] != "https://router.example/api/v1" {
		t.Errorf("codex's router base_url = %v, want its own URL", r["base_url"])
	}
}
