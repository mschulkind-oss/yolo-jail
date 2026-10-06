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
