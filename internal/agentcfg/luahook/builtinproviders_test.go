package luahook

// builtinproviders_test.go pins ctx.built_in_providers (DeriveCtx.BuiltInProviders;
// docs/design/pi-codex-provider-shadowing.md OQ-3) through the shipped derives that read it, run
// in the real VM: a provider the agent has a provider of its own for gets no row and is selected
// under the agent's own id, and one the agent has built in for another plan with none for this
// one (`false` in Lua, a BuiltInProvider with no ID here) gets no row and no selection, the case
// the launch's profile line reports. No shipped pack declares a plan of that kind, so only this
// test reaches the derives' `false` branch. It reads the packs off disk, as shippedpacks_test.go
// does, because packload imports this package.

import (
	"os"
	"testing"
)

// zaiCtx is a derive ctx with zai selected and reachable, as packs/zai composes it, and the
// agent's own providers as builtIn says.
func zaiCtx(agent, surface string, builtIn map[string]BuiltInProvider) *DeriveCtx {
	return &DeriveCtx{
		Agent:            agent,
		Surface:          surface,
		SelectedProvider: "zai",
		ProfileName:      "zai",
		Profile:          map[string]string{"model": "glm-5.3"},
		ActiveSet:        []SetEntry{{ProfileName: "zai", Provider: "zai", Profile: map[string]string{"model": "glm-5.3"}}},
		BuiltInProviders: builtIn,
		Tables: map[string]map[string]any{
			"providers": {"zai": map[string]any{
				"api_key_env_name": "ZAI_API_KEY",
				"endpoints": map[string]any{"openai": map[string]any{
					"base_url": "https://api.z.ai/api/coding/paas/v4", "wire_api": "openai-chat-completions"}},
				"models":  map[string]any{"glm-4.6": "glm-4.6", "glm-5.3": "glm-5.3"},
				"options": map[string]any{"model": "glm-5.3"},
			}},
		},
	}
}

func TestShippedDerivesHonorTheAgentsOwnProviders(t *testing.T) {
	for _, tc := range []struct {
		pack, agent, catalog, rowsKey, settings string
	}{
		{"pi", "pi", "models", "providers", "settings"},
		{"omp", "oh-omp", "models", "providers", "settings"},
		{"opencode", "opencode", "config", "provider", "config"},
	} {
		t.Run(tc.agent, func(t *testing.T) {
			src, err := os.ReadFile("../../../packs/" + tc.pack + "/derive.lua")
			if err != nil {
				t.Fatal(err)
			}
			script := string(src)
			rows := func(builtIn map[string]BuiltInProvider) map[string]any {
				out, err := (GopherLuaVM{}).Derive(script, zaiCtx(tc.agent, tc.catalog, builtIn))
				if err != nil {
					t.Fatalf("Derive(%s/%s): %v", tc.agent, tc.catalog, err)
				}
				m, _ := out[tc.rowsKey].(map[string]any)
				return m
			}
			selection := func(builtIn map[string]BuiltInProvider) map[string]any {
				out, err := (GopherLuaVM{}).Derive(script, zaiCtx(tc.agent, tc.settings, builtIn))
				if err != nil {
					t.Fatalf("Derive(%s/%s): %v", tc.agent, tc.settings, err)
				}
				m, _ := out["selection"].(map[string]any)
				return m
			}

			// THE WORLD BEFORE THE RULING, an entrypoint handing no table: zai is catalogued, so
			// the cases below measure the check and not an empty fixture.
			if rows(nil)["zai"] == nil {
				t.Fatalf("with no built-in table the derive wrote no zai row: %v", rows(nil))
			}

			own := rows(map[string]BuiltInProvider{"zai": {ID: "zai"}})
			if own["zai"] != nil {
				t.Errorf("zai is the agent's own and still got a row: %v", own["zai"])
			}

			none := map[string]BuiltInProvider{"zai": {}}
			if r := rows(none); r["zai"] != nil {
				t.Errorf("zai is built in for another plan and still got a row: %v", r["zai"])
			}
			if sel := selection(none); len(sel) != 0 {
				t.Errorf("zai is built in for another plan, with none of the agent's own for it, and "+
					"the derive still selected it: %v", sel)
			}
		})
	}
}

// THE AGENT'S OWN ID NAMES THE SELECTION where a plan maps yolo's provider to another of the
// agent's providers: opencode's model and filter name zai-coding-plan for yolo's zai.
func TestOpencodesSelectionNamesItsOwnProviderForThePlan(t *testing.T) {
	src, err := os.ReadFile("../../../packs/opencode/derive.lua")
	if err != nil {
		t.Fatal(err)
	}
	out, err := (GopherLuaVM{}).Derive(string(src), zaiCtx("opencode", "config",
		map[string]BuiltInProvider{"zai": {ID: "zai-coding-plan", APIKeyEnvName: "ZHIPU_API_KEY"}}))
	if err != nil {
		t.Fatal(err)
	}
	sel, _ := out["selection"].(map[string]any)
	if sel["model"] != "zai-coding-plan/glm-5.3" {
		t.Errorf("model = %v, want zai-coding-plan/glm-5.3", sel["model"])
	}
	if got, _ := sel["enabled_providers"].([]any); len(got) != 1 || got[0] != "zai-coding-plan" {
		t.Errorf("enabled_providers = %v, want [zai-coding-plan]", sel["enabled_providers"])
	}
}

func TestCodexDeriveUsesNativeMembershipAndKeepsOnlyLegacySubscriptionFallback(t *testing.T) {
	src, err := os.ReadFile("../../../packs/codex/derive.lua")
	if err != nil {
		t.Fatal(err)
	}
	script := string(src)
	ctx := &DeriveCtx{
		Agent: "codex", Surface: "config", SelectedProvider: "openai",
		Profile:          map[string]string{"model": "fast"},
		BuiltInProviders: map[string]BuiltInProvider{"openai": {ID: "openai"}},
		Tables: map[string]map[string]any{"providers": {"openai": map[string]any{
			"base_url": "https://poison.example/v1",
			"models":   map[string]any{"default": "expanded-default", "fast": "expanded-fast"},
		}}},
	}
	out, err := (GopherLuaVM{}).Derive(script, ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := out["model_providers"].(map[string]any)
	if rows["openai"] != nil {
		t.Errorf("native Codex key got a generic row: %v", rows["openai"])
	}
	sel, _ := out["selection"].(map[string]any)
	if sel["model_provider"] != "openai" || sel["model"] != "fast" {
		t.Errorf("native selection = %v, want openai/fast literally", sel)
	}

	// A false entry is membership too, but means this plan has no native provider ID. It may
	// neither leak a catalog row nor fall through to endpoint-gated generic selection.
	ctx.BuiltInProviders = map[string]BuiltInProvider{"openai": {}}
	out, err = (GopherLuaVM{}).Derive(script, ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows, _ = out["model_providers"].(map[string]any)
	sel, _ = out["selection"].(map[string]any)
	if rows["openai"] != nil || len(sel) != 0 {
		t.Errorf("false native plan fell through: rows=%v selection=%v", rows, sel)
	}

	// An explicit false native Bedrock plan with no via URL must bypass the earlier native
	// Bedrock region/selection branch too, even when the provider declares a region.
	ctx.SelectedProvider = "amazon-bedrock-runtime"
	ctx.ViaURL = ""
	ctx.BuiltInProviders = map[string]BuiltInProvider{"amazon-bedrock-runtime": {}}
	ctx.Tables["providers"] = map[string]any{"amazon-bedrock-runtime": map[string]any{
		"platform": "aws-bedrock", "region": "us-west-2",
	}}
	out, err = (GopherLuaVM{}).Derive(script, ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows, _ = out["model_providers"].(map[string]any)
	sel, _ = out["selection"].(map[string]any)
	if rows["amazon-bedrock-runtime"] != nil || len(sel) != 0 {
		t.Errorf("false native Bedrock plan without via fell through: rows=%v selection=%v", rows, sel)
	}

	// The early Codex Bedrock-via branch must also respect false membership: no fake bridge
	// row/selection may be synthesized when the provider has no native plan ID.
	ctx.ViaURL = "http://127.0.0.1:8216/agent/codex"
	ctx.Tables["providers"] = map[string]any{"amazon-bedrock-runtime": map[string]any{
		"platform": "aws-bedrock", "base_url": "https://poison-bedrock.example/v1",
	}}
	out, err = (GopherLuaVM{}).Derive(script, ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows, _ = out["model_providers"].(map[string]any)
	sel, _ = out["selection"].(map[string]any)
	if rows["amazon-bedrock-runtime"] != nil || len(sel) != 0 {
		t.Errorf("false native Bedrock-via plan fell through: rows=%v selection=%v", rows, sel)
	}

	// Old entrypoints without the metadata table still keep the historical subscription rule,
	// but the new five-name native set has no hard-coded fallback that could hide dropped metadata.
	legacy := &DeriveCtx{Agent: "codex", Surface: "config", SelectedProvider: "openai-codex",
		Profile: map[string]string{}, Tables: map[string]map[string]any{"providers": {"openai-codex": map[string]any{
			"base_url": "https://subscription-poison.example/v1",
			"models":   map[string]any{"default": "gpt-6.1-sol"},
		}}}}
	out, err = (GopherLuaVM{}).Derive(script, legacy)
	if err != nil {
		t.Fatal(err)
	}
	rows, _ = out["model_providers"].(map[string]any)
	sel, _ = out["selection"].(map[string]any)
	if rows["openai-codex"] != nil || sel["model_provider"] != nil || sel["model"] != "gpt-6.1-sol" {
		t.Errorf("legacy subscription fallback changed: rows=%v selection=%v", rows, sel)
	}
}

// (BuiltInProvider.YoloList), and no such field for one on the agent's own list, so a derive can
// tell the two apart from the table alone.
func TestTheCtxSaysWhichBuiltInProviderRunsYolosList(t *testing.T) {
	script := `yolo.derive("acme", "s", function(ctx)
	  local b = ctx.built_in_providers
	  return { codex = tostring(b["openai-codex"].yolo_list), zai = tostring(b["zai"].yolo_list) }
	end)`
	out, err := (GopherLuaVM{}).Derive(script, zaiCtx("acme", "s", map[string]BuiltInProvider{
		"openai-codex": {ID: "openai", YoloList: true}, "zai": {ID: "zai"}}))
	if err != nil {
		t.Fatal(err)
	}
	if out["codex"] != "true" || out["zai"] != "nil" {
		t.Errorf("yolo_list = %v, want true for openai-codex and absent for zai", out)
	}
}
