package entrypoint

import "testing"

// bedrock_generic_row_test.go pins BR-D10's "a Bedrock provider never gets a generic row"
// (docs/design/bedrock-plumbing.md) in the one case where the rule does work: a Bedrock provider
// that NAMES an endpoint. The shipped provider names none, so an agent's generic-row gate drops it
// before the Bedrock skip is ever asked, and a test over it alone passes with the skip deleted.
// A user who gives `bedrock` an `openai` endpoint, or declares a second provider of the same
// platform with one, would otherwise get a generic row carrying one key, while Bedrock's
// credential is the AWS chain only the agent's own client signs with. Driven through each
// agent's boot render over the tables its real needs closure composes, under the native profile.

// bedrockWithEndpoints is the user providers JSON: the shipped `bedrock` with a region and an
// endpoint of the dialect the agent speaks, and a second, unselected Bedrock provider with one.
func bedrockWithEndpoints(protocol, wireAPI string) string {
	ep := `{"` + protocol + `":{"base_url":"https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1",` +
		`"wire_api":"` + wireAPI + `"}}`
	return `{"bedrock":{"region":"us-east-1","endpoints":` + ep + `},` +
		`"bedrock-gw":{"platform":"aws-bedrock","endpoints":` + ep + `}}`
}

func TestABedrockProviderWithAnEndpointGetsNoGenericRow(t *testing.T) {
	generic := func(t *testing.T, agent string, rows map[string]any) {
		t.Helper()
		for _, name := range []string{"bedrock", "bedrock-gw"} {
			if row, ok := rows[name]; ok {
				t.Errorf("%s wrote a generic row for the Bedrock provider %s: %v", agent, name, row)
			}
		}
	}

	t.Run("codex", func(t *testing.T) {
		providersJSON, wire := bedrockTables(t, "codex", bedrockWithEndpoints("openai", "openai-responses"), nil)
		cfg := renderCodexConfig(t, providersJSON, `{"codex":"bedrock"}`, wire)
		rows, _ := cfg["model_providers"].(map[string]any)
		generic(t, "codex", rows)
		if cfg["model_provider"] != "amazon-bedrock-runtime" {
			t.Errorf("model_provider = %v, want codex's own amazon-bedrock-runtime", cfg["model_provider"])
		}
	})

	t.Run("opencode", func(t *testing.T) {
		providersJSON, wire := bedrockTables(t, "opencode", bedrockWithEndpoints("openai", "openai-chat-completions"), nil)
		r := newPioencodeRender(t, providersJSON)
		r.wireProfiles(wire)
		r.render(t, `{"opencode":"bedrock"}`)
		cfg := r.ocConfig(t)
		rows, _ := cfg["provider"].(map[string]any)
		generic(t, "opencode", rows)
		if rows["amazon-bedrock"] == nil {
			t.Errorf("no native amazon-bedrock row: %v", rows)
		}
	})

	// pi writes its native row only to register a list (packs/bedrock ships none since MM-D32), so
	// a company pack supplies one: the native row is then the proof the Bedrock path was taken.
	t.Run("pi", func(t *testing.T) {
		providersJSON, wire := bedrockListTables(t, "pi", bedrockWithEndpoints("openai", "openai-chat-completions"), nil)
		r := newPioencodeRender(t, providersJSON)
		r.wireProfiles(wire)
		r.render(t, `{"pi":"bedrock"}`)
		rows, _ := r.piModels(t)["providers"].(map[string]any)
		generic(t, "pi", rows)
		if rows["amazon-bedrock"] == nil {
			t.Errorf("no native amazon-bedrock row: %v", rows)
		}
	})
}
