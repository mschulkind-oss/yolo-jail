package entrypoint

import (
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// bedrock_codex_test.go pins codex's native Bedrock binding (docs/design/bedrock-plumbing.md
// §6.2 and §12 step 4; OQ-BR1, ruled 2026-09-29): `-p codex=bedrock` selects codex's own
// built-in `amazon-bedrock-runtime` client, starts it on the first OpenAI entry of the one
// Bedrock list — GPT-6.1 Sol in every Region, by the maintainer's ruling (BR-D19) — and writes
// the built-in's override only for a region the provider declares.
// Driven through the boot render (renderCodexConfig) over the tables codex's real needs closure
// composes, so it fails if the derive stops binding, if codex stops needing packs/bedrock, or if
// the list's first OpenAI entry moves.

func TestCodexOnBedrockUsesItsOwnRuntimeClient(t *testing.T) {
	const sol, astra, opus = "us.openai.gpt-6.1-sol", "global.openai.gpt-6-astra", "global.anthropic.claude-opus-5-5"
	for _, tc := range []struct {
		name      string
		providers string
		profiles  map[string]packload.UserProfile
		use       string
		model     string
		row       any // model_providers["amazon-bedrock-runtime"]; nil when none is written
	}{
		{"a region in the environment writes no override", "", nil,
			`{"codex":"bedrock"}`, sol, nil},
		// NO REGION DETECTION (BR-D19): a Region outside the US still starts codex on GPT-6.1 Sol,
		// whose `us.` id AWS offers only there; a user there names another model in a profile.
		{"the provider's region is the built-in's aws.region, and picks no other model",
			`{"bedrock":{"region":"eu-west-1"}}`, nil, `{"codex":"bedrock"}`, sol,
			map[string]any{"aws": map[string]any{"region": "eu-west-1"}}},
		{"a profile naming another OpenAI entry", "",
			map[string]packload.UserProfile{"astra": {Provider: "bedrock", Options: map[string]string{"model": astra}}},
			`{"codex":"astra"}`, astra, nil},
		// Anthropic's models are not served by the Responses API codex drives, so the profile's
		// pick is skipped and codex starts on the first entry it can call.
		{"a profile naming the Anthropic entry", "",
			map[string]packload.UserProfile{"opus": {Provider: "bedrock", Options: map[string]string{"model": opus}}},
			`{"codex":"opus"}`, sol, nil},
		// A user default alias codex can call steers it; the OQ-ML2 opt-in.
		{"a user default alias", `{"bedrock":{"models":{"default":"global.openai.gpt-6-astra"}}}`, nil,
			`{"codex":"bedrock"}`, astra, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			providersJSON, wire := bedrockTables(t, "codex", tc.providers, tc.profiles)
			cfg := renderCodexConfig(t, providersJSON, tc.use, wire)
			if cfg["model_provider"] != "amazon-bedrock-runtime" {
				t.Errorf("model_provider = %v, want codex's built-in amazon-bedrock-runtime", cfg["model_provider"])
			}
			if cfg["model"] != tc.model {
				t.Errorf("model = %v, want %s", cfg["model"], tc.model)
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
	if cfg["model_provider"] != "bedrock" || cfg["model"] != "us.openai.gpt-6.1-sol" {
		t.Errorf("selection = %v/%v, want the via row bedrock on the first OpenAI entry", cfg["model_provider"], cfg["model"])
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
