package entrypoint

import (
	"reflect"
	"testing"
)

// opencoderowlimits_test.go pins the SHAPE of every model `limit` opencode's config receives.
// opencode's config schema (ConfigProviderV1.Model in the installed 1.18.34 binary, read and never
// run) declares it as `{context: number, input?: number, output: number}`: a limit naming one of
// the two required fields does not satisfy it, and opencode reads an invalid config as a config
// error rather than as a row without a limit. So a row whose provider declares only a context
// window carries `output` 0, opencode's own "not stated" (its maxOutputTokens reads 0 as its
// 32,000 cap, the cap it applies to a larger stated output too), and a row with only an output
// limit carries no `limit`: a context of 0 would replace opencode's catalog window and turn its
// compaction off. Driven through the boot render.

// A GENERIC ROW: a provider's `context_window` or `max_tokens` option, as zai and cerebras ship
// a window alone.
func TestOpencodeGenericRowLimitsCarryBothFieldsItsSchemaRequires(t *testing.T) {
	const tables = `{
  "ctx":{"api_key_env_name":"CTX_KEY","models":{"default":"m1","m1":"m1"},"options":{"context_window":"65536"},
    "endpoints":{"openai":{"base_url":"https://ctx.example/v1"}}},
  "out":{"api_key_env_name":"OUT_KEY","models":{"default":"m2","m2":"m2"},"options":{"max_tokens":"8192"},
    "endpoints":{"openai":{"base_url":"https://out.example/v1"}}},
  "both":{"api_key_env_name":"BOTH_KEY","models":{"default":"m3","m3":"m3"},"options":{"context_window":"32768","max_tokens":"4096"},
    "endpoints":{"openai":{"base_url":"https://both.example/v1"}}}}`
	r := newPioencodeRender(t, tables)
	r.wireProfiles(`{"ctx":{"provider":"ctx"}}`)
	r.render(t, `{"opencode":"ctx"}`)
	rows := ocRows(t, r.ocConfig(t))
	for _, tc := range []struct {
		provider, model string
		limit           any
	}{
		{"ctx", "m1", map[string]any{"context": float64(65536), "output": float64(0)}},
		{"out", "m2", nil},
		{"both", "m3", map[string]any{"context": float64(32768), "output": float64(4096)}},
	} {
		row, _ := rows[tc.provider].(map[string]any)
		models, _ := row["models"].(map[string]any)
		m, _ := models[tc.model].(map[string]any)
		if m == nil {
			t.Errorf("%s has no %s row: %v", tc.provider, tc.model, row)
			continue
		}
		if got := m["limit"]; !reflect.DeepEqual(got, tc.limit) {
			t.Errorf("%s/%s limit = %#v, want %#v", tc.provider, tc.model, got, tc.limit)
		}
	}
}

// opencode's NATIVE Bedrock row: a model a user adds to the Bedrock list with a window and no
// output limit, beside the shipped entries, which declare both.
func TestOpencodeBedrockRowLimitsCarryBothFieldsItsSchemaRequires(t *testing.T) {
	providersJSON, wire := bedrockTables(t, "opencode", `{"bedrock":{
    "models":{"x.only-window":"x.only-window","x.only-output":"x.only-output"},
    "model_options":{"x.only-window":{"context_window":"200000"},"x.only-output":{"max_tokens":"9000"}}}}`, nil)
	r := newPioencodeRender(t, providersJSON)
	r.wireProfiles(wire)
	r.render(t, `{"opencode":"bedrock"}`)
	native, _ := ocRows(t, r.ocConfig(t))["amazon-bedrock"].(map[string]any)
	models, _ := native["models"].(map[string]any)
	for id, want := range map[string]any{
		"x.only-window":                    map[string]any{"context": float64(200000), "output": float64(0)},
		"x.only-output":                    nil,
		"global.anthropic.claude-opus-5-5": map[string]any{"context": float64(1000000), "output": float64(128000)},
	} {
		m, _ := models[id].(map[string]any)
		if m == nil {
			t.Errorf("the native row has no %s: %v", id, models)
			continue
		}
		if got := m["limit"]; !reflect.DeepEqual(got, want) {
			t.Errorf("amazon-bedrock/%s limit = %#v, want %#v", id, got, want)
		}
	}
}
