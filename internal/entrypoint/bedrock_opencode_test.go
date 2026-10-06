package entrypoint

import (
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// bedrock_opencode_test.go pins opencode's native Bedrock binding (docs/design/bedrock-plumbing.md
// §6.2 and §12 step 5; OQ-BR1, ruled 2026-09-29): `-p opencode=bedrock` binds opencode's own
// built-in `amazon-bedrock` provider — no npm, no endpoint, `options.region` only for a region
// the provider declares. packs/bedrock ships no model list (docs/design/model-lists-and-pickers.md
// MM-D32), so with none supplied opencode lists its own catalog and starts on its own default,
// and yolo names no model; a list a pack or the user supplies is listed whole, and opencode starts
// on its first entry. Driven through the boot render over the tables opencode's real needs closure
// composes.

func TestOpencodeOnBedrockUsesItsOwnClient(t *testing.T) {
	const opus, sol = "global.anthropic.claude-opus-5-5", "us.openai.gpt-6.1-sol"
	allModels := map[string]any{
		"global.anthropic.claude-opus-5-5": map[string]any{"name": "Claude Opus 5.5 (Global)",
			"limit": map[string]any{"context": float64(1000000), "output": float64(128000)}},
		"us.openai.gpt-6.1-sol": map[string]any{"name": "GPT-6.1 Sol (US)",
			"limit": map[string]any{"context": float64(1000000), "output": float64(131072)}},
		"global.openai.gpt-6-astra": map[string]any{"name": "GPT-6 Astra (Global)",
			"limit": map[string]any{"context": float64(1050000), "output": float64(128000)}},
	}
	for _, tc := range []struct {
		name      string
		list      bool // a company pack supplies bedrockListAdd
		providers string
		profiles  map[string]packload.UserProfile
		use       string
		model     string // "" when yolo names none and opencode starts on its own default
		options   any    // provider["amazon-bedrock"].options; nil when none is written
	}{
		{"no list names no model", false, "", nil, `{"opencode":"bedrock"}`, "", nil},
		{"the provider's region is options.region", false, `{"bedrock":{"region":"eu-west-1"}}`, nil,
			`{"opencode":"bedrock"}`, "", map[string]any{"region": "eu-west-1"}},
		{"a supplied list starts on its first entry", true, "", nil, `{"opencode":"bedrock"}`, opus, nil},
		{"a supplied list with the provider's region", true, `{"bedrock":{"region":"eu-west-1"}}`, nil,
			`{"opencode":"bedrock"}`, opus, map[string]any{"region": "eu-west-1"}},
		{"a profile naming another entry", true, "",
			map[string]packload.UserProfile{"sol": {Provider: "bedrock", Options: map[string]string{"model": sol}}},
			`{"opencode":"sol"}`, sol, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tables := bedrockTables
			if tc.list {
				tables = bedrockListTables
			}
			providersJSON, wire := tables(t, "opencode", tc.providers, tc.profiles)
			r := newPioencodeRender(t, providersJSON)
			r.wireProfiles(wire)
			r.render(t, tc.use)
			cfg := r.ocConfig(t)
			rows, _ := cfg["provider"].(map[string]any)
			if _, generic := rows["bedrock"]; generic {
				t.Errorf("a generic row was written for bedrock: %v", rows["bedrock"])
			}
			native, _ := rows["amazon-bedrock"].(map[string]any)
			if native == nil {
				t.Fatalf("no provider[\"amazon-bedrock\"] row: %v", rows)
			}
			for _, key := range []string{"npm", "endpoint", "baseURL"} {
				if v, ok := native[key]; ok {
					t.Errorf("the native row carries %s = %v, which drags opencode's bare ids off their routing", key, v)
				}
			}
			if !reflect.DeepEqual(native["options"], tc.options) {
				t.Errorf("options = %#v, want %#v", native["options"], tc.options)
			}
			wantModels := allModels
			if !tc.list {
				wantModels = map[string]any{}
			}
			if got, _ := native["models"].(map[string]any); !reflect.DeepEqual(got, wantModels) && (len(got) != 0 || len(wantModels) != 0) {
				t.Errorf("models = %#v, want %#v", native["models"], wantModels)
			}
			if tc.model == "" {
				if cfg["model"] != nil || cfg["small_model"] != nil {
					t.Errorf("model = %v, small_model = %v, want neither: no list names one (MM-D32)", cfg["model"], cfg["small_model"])
				}
			} else if want := "amazon-bedrock/" + tc.model; cfg["model"] != want || cfg["small_model"] != want {
				t.Errorf("model = %v, small_model = %v, want both %s", cfg["model"], cfg["small_model"], want)
			}
			if got := cfg["enabled_providers"]; !reflect.DeepEqual(got, []any{"amazon-bedrock"}) {
				t.Errorf("enabled_providers = %v, want the one provider the selection names", got)
			}
		})
	}
}

// A PROFILE FORCING THE WIRE BRIDGE gets no native row and no native selection: the profile asked
// for the bridge, and opencode on its own client would ignore that.
func TestOpencodeOnABridgedBedrockProfileGetsNoNativeRow(t *testing.T) {
	providersJSON, wire := bedrockTables(t, "opencode", `{"bedrock":{"region":"us-east-1"}}`, bedrockViaProfile, "wire-bridge")
	r := newPioencodeRender(t, providersJSON)
	r.wireProfiles(wire)
	r.render(t, `{"opencode":"over-bridge"}`)
	cfg := r.ocConfig(t)
	rows, _ := cfg["provider"].(map[string]any)
	if rows["amazon-bedrock"] != nil {
		t.Errorf("a bridged profile wrote the native row: %v", rows["amazon-bedrock"])
	}
	if m, _ := cfg["model"].(string); len(m) > len("amazon-bedrock/") && m[:len("amazon-bedrock/")] == "amazon-bedrock/" {
		t.Errorf("a bridged profile selected the native provider: model = %s", m)
	}
}
