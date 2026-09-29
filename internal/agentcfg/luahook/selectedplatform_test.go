package luahook

// selectedplatform_test.go pins ctx.selected_platform (OQ-BR2,
// docs/design/providers-and-profiles-redesign.md): the platform of the selected provider's row,
// read off the table the derive is handed, so a derive recognizes Bedrock by what the provider
// says it is rather than by its name.

import "testing"

func TestDeriveSeesTheSelectedProvidersPlatform(t *testing.T) {
	script := `yolo.env("claude", function(ctx) return { P = ctx.selected_platform } end)`
	for _, tc := range []struct {
		name     string
		selected string
		tables   map[string]map[string]any
		want     string
	}{
		{"the row's platform", "mine", map[string]map[string]any{
			"providers": {"mine": map[string]any{"platform": "aws-bedrock"}}}, "aws-bedrock"},
		{"a row with none", "mine", map[string]map[string]any{
			"providers": {"mine": map[string]any{"region": "x"}}}, ""},
		{"no row for the selection", "gone", map[string]map[string]any{
			"providers": {"mine": map[string]any{"platform": "aws-bedrock"}}}, ""},
		{"a non-string value", "mine", map[string]map[string]any{
			"providers": {"mine": map[string]any{"platform": []any{"aws-bedrock"}}}}, ""},
		{"no selection", "", map[string]map[string]any{
			"providers": {"mine": map[string]any{"platform": "aws-bedrock"}}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := GopherLuaVM{}.Derive(script, &DeriveCtx{
				Agent: "claude", Env: true, SelectedProvider: tc.selected, Tables: tc.tables})
			if err != nil {
				t.Fatal(err)
			}
			if p, _ := got["P"].(string); p != tc.want {
				t.Errorf("ctx.selected_platform = %q, want %q", p, tc.want)
			}
		})
	}
}
