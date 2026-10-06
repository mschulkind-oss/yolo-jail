package entrypoint

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// bedrock_pi_test.go pins pi's native Bedrock binding (docs/design/bedrock-plumbing.md §6.2, the
// native half of OQ-BR5; §12 step 5; OQ-BR1, ruled 2026-09-29): `-p pi=bedrock` selects pi's own
// built-in `amazon-bedrock` provider and hands pi a provider-declared region as AWS_REGION.
// packs/bedrock ships no model list (docs/design/model-lists-and-pickers.md MM-D32), so with none
// supplied pi keeps its own catalog and its own default; a list a pack or the user supplies is
// catalogued under `amazon-bedrock` (models only, so each stays on pi's Converse client), selected
// with its first entry or the profile's, and scopes pi and pi-subagents. The files go through the
// boot render and the environment through the host composition a launch runs, each over the
// tables pi's real needs closure composes.

func TestPiOnBedrockUsesItsOwnConverseClient(t *testing.T) {
	const opus, sol, astra = "global.anthropic.claude-opus-5-5", "us.openai.gpt-6.1-sol",
		"global.openai.gpt-6-astra"
	wantModels := []any{
		map[string]any{"id": opus, "name": "Claude Opus 5.5 (Global)", "contextWindow": float64(1000000),
			"maxTokens": float64(128000), "reasoning": true, "input": []any{"text", "image"}},
		map[string]any{"id": sol, "name": "GPT-6.1 Sol (US)", "contextWindow": float64(1000000),
			"maxTokens": float64(131072), "input": []any{"text", "image"}},
		map[string]any{"id": astra, "name": "GPT-6 Astra (Global)", "contextWindow": float64(1050000),
			"maxTokens": float64(128000), "input": []any{"text", "image"}},
	}
	for _, tc := range []struct {
		name     string
		profiles map[string]packload.UserProfile
		use      string
		model    string
		enabled  []any
	}{
		{"the shipped profile starts on a supplied list's first", nil, `{"pi":"bedrock"}`, opus,
			[]any{"amazon-bedrock/" + opus, "amazon-bedrock/" + sol, "amazon-bedrock/" + astra}},
		{"a profile's model leads the scope",
			map[string]packload.UserProfile{"astra": {Provider: "bedrock", Options: map[string]string{"model": astra}}},
			`{"pi":"astra"}`, astra,
			[]any{"amazon-bedrock/" + astra, "amazon-bedrock/" + opus, "amazon-bedrock/" + sol}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			providersJSON, wire := bedrockListTables(t, "pi", `{"bedrock":{"region":"eu-west-1"}}`, tc.profiles)
			r := newPioencodeRender(t, providersJSON)
			r.wireProfiles(wire)
			r.render(t, tc.use)

			rows, _ := r.piModels(t)["providers"].(map[string]any)
			if _, generic := rows["bedrock"]; generic {
				t.Errorf("a generic row was written for bedrock: %v", rows["bedrock"])
			}
			native, _ := rows["amazon-bedrock"].(map[string]any)
			if native == nil {
				t.Fatalf("no providers[\"amazon-bedrock\"] row in models.json: %v", rows)
			}
			for _, key := range []string{"baseUrl", "api", "apiKey"} {
				if v, ok := native[key]; ok {
					t.Errorf("the native row carries %s = %v; it must leave pi's built-in Converse client in place", key, v)
				}
			}
			if !reflect.DeepEqual(native["models"], wantModels) {
				t.Errorf("models = %#v\nwant %#v", native["models"], wantModels)
			}

			s := r.piSettings(t)
			if s["defaultProvider"] != "amazon-bedrock" || s["defaultModel"] != tc.model {
				t.Errorf("selection = %v/%v, want amazon-bedrock/%s", s["defaultProvider"], s["defaultModel"], tc.model)
			}
			if !reflect.DeepEqual(s["enabledModels"], tc.enabled) {
				t.Errorf("enabledModels = %v, want %v", s["enabledModels"], tc.enabled)
			}
			sub, _ := s["subagents"].(map[string]any)
			if sub["defaultModel"] != "amazon-bedrock/"+tc.model {
				t.Errorf("subagents.defaultModel = %v, want amazon-bedrock/%s", sub["defaultModel"], tc.model)
			}
			scope, _ := sub["modelScope"].(map[string]any)
			if !reflect.DeepEqual(scope["allow"], tc.enabled) {
				t.Errorf("subagents.modelScope.allow = %v, want %v", scope["allow"], tc.enabled)
			}
		})
	}
}

// NO LIST, NO PICK (MM-D32): with no list supplied pi writes no native row, so pi's own catalog
// stays whole, selects its built-in provider and names no model, so pi starts on its own default,
// and scopes all of that provider. pi's own catalog spells some ids runtime refuses (§4), an
// upstream fault the ruling leaves to pi.
func TestPiOnBedrockWithNoListKeepsItsOwnCatalogAndDefault(t *testing.T) {
	providersJSON, wire := bedrockTables(t, "pi", `{"bedrock":{"region":"eu-west-1"}}`, nil)
	r := newPioencodeRender(t, providersJSON)
	r.wireProfiles(wire)
	r.render(t, `{"pi":"bedrock"}`)
	if rows, _ := r.piModels(t)["providers"].(map[string]any); rows["amazon-bedrock"] != nil {
		t.Errorf("no list was supplied, yet models.json replaces pi's catalog: %v", rows["amazon-bedrock"])
	}
	s := r.piSettings(t)
	if s["defaultProvider"] != "amazon-bedrock" {
		t.Errorf("defaultProvider = %v, want pi's own amazon-bedrock", s["defaultProvider"])
	}
	if m, set := s["defaultModel"]; set && m != nil {
		t.Errorf("defaultModel = %v, want none: yolo picks no Bedrock model (MM-D32)", m)
	}
	if got, want := s["enabledModels"], []any{"amazon-bedrock/*"}; !reflect.DeepEqual(got, want) {
		t.Errorf("enabledModels = %v, want %v, the whole provider", got, want)
	}
}

// THE REGION REACHES pi's PROCESS: pi's Converse client reads it from the environment, so a region
// the provider declares must arrive as AWS_REGION, through the env composition a launch runs.
func TestPiOnBedrockReceivesTheProvidersRegion(t *testing.T) {
	packs := testPacksForAgent(t, "pi")
	user := jsonx.NewOrderedMap()
	bedrock := jsonx.NewOrderedMap()
	bedrock.Set("region", "eu-west-1")
	user.Set("bedrock", bedrock)
	providers, err := packload.ComposeProviders(user, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles(packs, nil, providers)
	if err != nil {
		t.Fatal(err)
	}
	vars, err := packload.AgentEnv(packs, providers, map[string]string{"pi": "bedrock"}, "pi", "bedrock",
		func(string) (string, bool) { return "", false }, packload.WithResolvedProfiles(resolved))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, v := range vars {
		got[v.Key] = v.Value
	}
	if got["AWS_REGION"] != "eu-west-1" {
		t.Errorf("pi's environment on bedrock = %v, want AWS_REGION=eu-west-1", got)
	}
	if _, ok := got["YOLO_AUTH_PRELAUNCH_PI_FLAG"]; ok {
		t.Errorf("the OpenAI login prelaunch rode a Bedrock launch: %v", got)
	}
}

// A PROFILE FORCING THE WIRE BRIDGE gets no native row and no native selection.
func TestPiOnABridgedBedrockProfileGetsNoNativeRow(t *testing.T) {
	providersJSON, wire := bedrockTables(t, "pi", `{"bedrock":{"region":"us-east-1"}}`, bedrockViaProfile, "wire-bridge")
	r := newPioencodeRender(t, providersJSON)
	r.wireProfiles(wire)
	r.render(t, `{"pi":"over-bridge"}`)
	rows, _ := r.piModels(t)["providers"].(map[string]any)
	if rows["amazon-bedrock"] != nil {
		t.Errorf("a bridged profile wrote the native row: %v", rows["amazon-bedrock"])
	}
	if s := r.piSettings(t); s["defaultProvider"] == "amazon-bedrock" {
		t.Errorf("a bridged profile selected the native provider: %v", s)
	}
}

// A BEDROCK ENTRY ANYWHERE IN pi's ACTIVE SET is bound to pi's own client (docs/design/active-
// provider-sets.md AP-P1: every entry is live, not only the primary). pi on [zai, bedrock] starts
// on zai, and its models.json carries the native amazon-bedrock row too, so a switch to a Bedrock
// model reaches one pi can call; its scoped list runs zai's models then Bedrock's. Before the set
// learned the native row (piNativeBedrockEntry), a Bedrock entry after the first named
// amazon-bedrock ids in enabledModels with no row behind them.
func TestPiOnASetWithBedrockSecondCatalogsItNatively(t *testing.T) {
	const opus = "global.anthropic.claude-opus-5-5"
	providersJSON, wire := bedrockListTables(t, "pi", `{"bedrock":{"region":"eu-west-1"}}`, nil, "zai")
	r := newPioencodeRender(t, providersJSON)
	r.wireProfiles(wire)
	r.render(t, `{"pi":["zai","bedrock"]}`)

	rows, _ := r.piModels(t)["providers"].(map[string]any)
	if rows["zai"] == nil {
		t.Errorf("the primary's row is missing: %v", rows)
	}
	native, _ := rows["amazon-bedrock"].(map[string]any)
	if native == nil {
		t.Fatalf("no amazon-bedrock row in models.json for pi's second entry: %v", rows)
	}
	for _, key := range []string{"baseUrl", "api", "apiKey"} {
		if v, ok := native[key]; ok {
			t.Errorf("the native row carries %s = %v; it must leave pi's built-in Converse client in place", key, v)
		}
	}
	s := r.piSettings(t)
	if s["defaultProvider"] != "zai" {
		t.Errorf("a fresh session starts on the primary: defaultProvider = %v", s["defaultProvider"])
	}
	enabled := strs(s["enabledModels"])
	if len(enabled) == 0 || !strings.HasPrefix(enabled[0], "zai/") {
		t.Errorf("the scoped list must lead with the primary's run: %v", enabled)
	}
	if !slices.Contains(enabled, "amazon-bedrock/"+opus) {
		t.Errorf("the scoped list must carry the Bedrock entry's models under amazon-bedrock: %v", enabled)
	}
}
