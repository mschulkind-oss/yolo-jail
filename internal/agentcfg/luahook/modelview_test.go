package luahook

import (
	"testing"
)

func TestDeriveContextProjectsProviderModelsAndNeverExposesViews(t *testing.T) {
	providers := map[string]any{"bedrock": map[string]any{
		"models":        map[string]any{"fable": "fable-id", "opus": "opus-id", "gpt": "gpt-id"},
		"model_options": map[string]any{"fable": map[string]any{"name": "Fable"}, "gpt": map[string]any{"name": "GPT"}},
		"models_views":  map[string]any{"claude": map[string]any{"only": []any{"fable-id", "opus-id"}}},
	}}
	for _, agent := range []string{"claude", "pi", "copilot"} {
		t.Run(agent, func(t *testing.T) {
			script := `yolo.derive("` + agent + `", "config", function(ctx)
			  local p = ctx.providers.bedrock
			  local ids = {}
			  for alias, id in pairs(p.models) do ids[alias] = id end
			  return { views = tostring(p.models_views == nil), narrowed = tostring(p.models_only == true), ids = ids }
			end)`
			out, err := (GopherLuaVM{}).Derive(script, &DeriveCtx{Agent: agent, Surface: "config", Tables: map[string]map[string]any{
				"providers": providers,
			}})
			if err != nil {
				t.Fatal(err)
			}
			if out["views"] != "true" {
				t.Errorf("derive exposed models_views: %v", out)
			}
			ids, ok := out["ids"].(map[string]any)
			if !ok {
				t.Fatalf("derive ids have type %T: %v", out["ids"], out["ids"])
			}
			if agent == "claude" {
				if out["narrowed"] != "true" || len(ids) != 2 || ids["fable"] != "fable-id" || ids["opus"] != "opus-id" {
					t.Errorf("Claude derive view = %v, want exact fable/opus list marked narrowed", out)
				}
			} else if out["narrowed"] != "false" || len(ids) != 3 {
				t.Errorf("unmatched %s derive view = %v, want unchanged models and no narrowed marker", agent, out)
			}
		})
	}
	shared := providers["bedrock"].(map[string]any)
	if _, exists := shared["models_only"]; exists {
		t.Error("derive projection mutated the shared providers table")
	}
	if _, exists := shared["models_views"]; !exists || len(shared["models"].(map[string]any)) != 3 {
		t.Error("derive projection removed the shared view key or changed shared model rows")
	}
}
