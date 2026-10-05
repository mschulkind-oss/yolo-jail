package entrypoint

import (
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// bedrock_codex_test.go pins codex's native Bedrock binding (docs/design/bedrock-plumbing.md
// §6.2 and §12 step 4; OQ-BR1, ruled 2026-09-29): `-p codex=bedrock` selects codex's own
// built-in `amazon-bedrock-runtime` client and writes the built-in's override only for a region
// the provider declares. packs/bedrock ships no model list (docs/design/model-lists-and-pickers.md
// MM-D32), so with none supplied codex starts on its own Bedrock default and yolo writes no
// `model`; a list a pack or the user supplies starts it on that list's first OpenAI entry.
// Driven through the boot render (renderCodexConfig) over the tables codex's real needs closure
// composes, so it fails if the derive stops binding, if codex stops needing packs/bedrock, or if
// a model is picked where none was supplied.

func TestCodexOnBedrockUsesItsOwnRuntimeClient(t *testing.T) {
	const sol, astra, opus = "us.openai.gpt-6.1-sol", "global.openai.gpt-6-astra", "global.anthropic.claude-opus-5-5"
	for _, tc := range []struct {
		name      string
		list      bool // a company pack supplies bedrockListAdd
		providers string
		profiles  map[string]packload.UserProfile
		use       string
		model     any // nil when yolo writes none and codex starts on its own default
		row       any // model_providers["amazon-bedrock-runtime"]; nil when none is written
	}{
		// NO LIST, NO PICK (MM-D32): codex starts on its own Bedrock default.
		{"a region in the environment writes no override and no model", false, "", nil,
			`{"codex":"bedrock"}`, nil, nil},
		{"the provider's region is the built-in's aws.region", false,
			`{"bedrock":{"region":"eu-west-1"}}`, nil, `{"codex":"bedrock"}`, nil,
			map[string]any{"aws": map[string]any{"region": "eu-west-1"}}},
		// A profile's model is the user's choice, passed through as written with no list.
		{"a profile's model with no list", false, "",
			map[string]packload.UserProfile{"astra": {Provider: "bedrock", Options: map[string]string{"model": astra}}},
			`{"codex":"astra"}`, astra, nil},
		// A supplied list starts codex on its first OpenAI entry, in every Region (BR-D19's rule
		// over whatever list a pack supplies).
		{"a supplied list starts codex on its first OpenAI entry", true,
			`{"bedrock":{"region":"eu-west-1"}}`, nil, `{"codex":"bedrock"}`, sol,
			map[string]any{"aws": map[string]any{"region": "eu-west-1"}}},
		{"a profile naming another OpenAI entry", true, "",
			map[string]packload.UserProfile{"astra": {Provider: "bedrock", Options: map[string]string{"model": astra}}},
			`{"codex":"astra"}`, astra, nil},
		// Anthropic's models are not served by the Responses API codex drives, so the profile's
		// pick is skipped and codex starts on the first entry it can call.
		{"a profile naming the Anthropic entry", true, "",
			map[string]packload.UserProfile{"opus": {Provider: "bedrock", Options: map[string]string{"model": opus}}},
			`{"codex":"opus"}`, sol, nil},
		// A user default alias codex can call steers it; the OQ-ML2 opt-in.
		{"a user default alias", false, `{"bedrock":{"models":{"default":"global.openai.gpt-6-astra"}}}`, nil,
			`{"codex":"bedrock"}`, astra, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tables := bedrockTables
			if tc.list {
				tables = bedrockListTables
			}
			providersJSON, wire := tables(t, "codex", tc.providers, tc.profiles)
			cfg := renderCodexConfig(t, providersJSON, tc.use, wire)
			if cfg["model_provider"] != "amazon-bedrock-runtime" {
				t.Errorf("model_provider = %v, want codex's built-in amazon-bedrock-runtime", cfg["model_provider"])
			}
			if cfg["model"] != tc.model {
				t.Errorf("model = %v, want %v", cfg["model"], tc.model)
			}
			rows, _ := cfg["model_providers"].(map[string]any)
			if _, generic := rows["bedrock"]; generic {
				t.Errorf("a generic row was written for bedrock: %v", rows["bedrock"])
			}
			if got := rows["amazon-bedrock-runtime"]; !reflect.DeepEqual(got, tc.row) {
				t.Errorf("model_providers.amazon-bedrock-runtime = %#v, want %#v — an override "+
					"carrying any field beyond the seven codex permits is refused (trap D3)", got, tc.row)
			}
		})
	}
}

// A PROFILE FORCING THE WIRE BRIDGE puts codex on its via route, never on its own client: the row
// speaks Responses at codex's via URL with the bridge's caller token, although the provider names
// no endpoint, because the bridge is what reaches Bedrock there. Running codex natively instead
// would ignore the profile. (The bridge composes that route's upstream from the region, and serves
// it: wirebridged's TestTheShippedBedrockBridgeProfileMeetsEachAgentAsItCan.)
func TestCodexOnABridgedBedrockProfileRidesItsViaRoute(t *testing.T) {
	providersJSON, wire := bedrockTables(t, "codex", `{"bedrock":{"region":"us-east-1"}}`, bedrockViaProfile, "wire-bridge")
	cfg := renderCodexConfig(t, providersJSON, `{"codex":"over-bridge"}`, wire)
	// No list is supplied, so yolo picks no model (MM-D32): codex sends its own default.
	if cfg["model_provider"] != "bedrock" || cfg["model"] != nil {
		t.Errorf("selection = %v/%v, want the via row bedrock and no model", cfg["model_provider"], cfg["model"])
	}
	listed, listedWire := bedrockListTables(t, "codex", `{"bedrock":{"region":"us-east-1"}}`, bedrockViaProfile, "wire-bridge")
	if got := renderCodexConfig(t, listed, `{"codex":"over-bridge"}`, listedWire)["model"]; got != "us.openai.gpt-6.1-sol" {
		t.Errorf("model = %v on a supplied list, want its first OpenAI entry", got)
	}
	rows, _ := cfg["model_providers"].(map[string]any)
	if rows["amazon-bedrock-runtime"] != nil {
		t.Errorf("a bridged profile wrote the native override: %v", rows["amazon-bedrock-runtime"])
	}
	row, _ := rows["bedrock"].(map[string]any)
	// The row's env_key is the bridge's caller token, which this render (codex's pack alone, no
	// staged wire-bridge pack) cannot name; a launch's is pinned by the via tests.
	delete(row, "env_key")
	want := map[string]any{"name": "bedrock", "base_url": "http://127.0.0.1:8216/agent/codex", "wire_api": "responses"}
	if !reflect.DeepEqual(row, want) {
		t.Errorf("model_providers.bedrock = %#v, want the via row %#v", row, want)
	}
}
