package luahook

import "testing"

func TestModelForUsesTheSameProjectedTablesAsLua(t *testing.T) {
	for _, tc := range []struct {
		name  string
		entry map[string]any
	}{
		{"scoped-view-for-pi", map[string]any{
			"models":       map[string]any{"default": "big-id", "small": "small-id"},
			"models_views": map[string]any{"pi": map[string]any{"only": []any{"small-id"}}},
		}},
		{"equivalent-unscoped-only", map[string]any{
			"models":      map[string]any{"small": "small-id"},
			"models_only": true,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := `yolo.derive("pi", "config", function(ctx)
			  local _, id = yolo.model_for("default")
			  return { table_default = tostring(ctx.providers.gw.models.default), model_for_default = tostring(id) }
			end)`
			out, err := (GopherLuaVM{}).Derive(script, &DeriveCtx{Agent: "pi", Surface: "config",
				SelectedProvider: "gw", Tables: map[string]map[string]any{"providers": {"gw": tc.entry}}})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%v", out)
			if out["table_default"] != "nil" || out["model_for_default"] != "nil" {
				t.Errorf("derive saw alias outside its view: %v", out)
			}
		})
	}
}
