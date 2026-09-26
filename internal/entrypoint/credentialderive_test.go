package entrypoint

// credentialderive_test.go pins the IN-JAIL half of a provider's credential variable LIST
// (docs/design/provider-credential-scope.md, OQ-CN1), through the boot render of the real
// pi, opencode and zai packs (pioencodeRender).
//
// The derives read the composed table through packload.ProvidersForDerive: a provider that
// names ONE credential variable — a string, or a list of one — is pointed at it exactly as
// before, and one that lists several points an agent at none of them. Without that view a
// list reaches the derives' `"${" .. prov.api_key_env_name .. "}"` as a Lua table and the
// boot refuses (A12), which is what these cells would then report.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// surfaceText is a rendered surface's raw bytes, for an assertion about text anywhere in it.
func (r *pioencodeRender) surfaceText(t *testing.T, rel ...string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(append([]string{r.e.Home}, rel...)...))
	if err != nil {
		t.Fatalf("read the rendered surface %s: %v", filepath.Join(rel...), err)
	}
	return string(raw)
}

const credentialListJSON = `{
  "bedrock":{"api_key_env_name":["AWS_BEARER_TOKEN_BEDROCK","AWS_ACCESS_KEY_ID","AWS_SECRET_ACCESS_KEY"],
    "region":"us-east-1"},
  "multi":{"api_key_env_name":["MULTI_A","MULTI_B"],"models":{"default":"m1"},
    "endpoints":{"openai":{"base_url":"https://multi.example/v1","wire_api":"openai-chat-completions"}}},
  "one":{"api_key_env_name":["ONE_KEY"],"models":{"default":"o1"},
    "endpoints":{"openai":{"base_url":"https://one.example/v1","wire_api":"openai-chat-completions"}}},
  "zai":{"api_key_env_name":"ZAI_API_KEY","models":{"glm-5.3":"glm-5.3"},
    "endpoints":{"openai":{"base_url":"https://api.z.ai/api/coding/paas/v4","wire_api":"openai-chat-completions"}}}
}`

// Done condition 1's models.json half: `-p zai` for pi renders a catalog that names no AWS
// variable, and each provider's key reference is its ONE variable or nothing.
func TestPiModelsJSONCarriesNoMultiRouteCredential(t *testing.T) {
	r := newPioencodeRender(t, credentialListJSON)
	r.wireProfiles(`{"zai": {"provider": "zai", "model": "glm-5.3"}}`)
	r.render(t, `{"pi":"zai","opencode":"zai"}`)

	models := r.piModels(t)
	raw := r.surfaceText(t, ".pi", "agent", "models.json")
	if strings.Contains(raw, "AWS_") {
		t.Errorf("pi's models.json names an AWS variable although pi selected zai:\n%s", raw)
	}
	provs, _ := models["providers"].(map[string]any)
	for name, want := range map[string]any{
		"zai":   "${ZAI_API_KEY}",
		"one":   "${ONE_KEY}", // a list of one points at its one variable
		"multi": nil,          // several point at none
	} {
		entry, ok := provs[name].(map[string]any)
		if !ok {
			t.Fatalf("models.json has no %s entry: %#v", name, provs)
		}
		if got := entry["apiKey"]; got != want {
			t.Errorf("models.json providers.%s.apiKey = %#v, want %#v", name, got, want)
		}
	}

	oc := r.ocConfig(t)
	ocProvs, _ := oc["provider"].(map[string]any)
	one, _ := ocProvs["one"].(map[string]any)
	oneOpts, _ := one["options"].(map[string]any)
	if oneOpts["apiKey"] != "{env:ONE_KEY}" {
		t.Errorf("opencode provider.one.options.apiKey = %#v, want {env:ONE_KEY}", oneOpts["apiKey"])
	}
	multi, _ := ocProvs["multi"].(map[string]any)
	if multiOpts, _ := multi["options"].(map[string]any); multiOpts["apiKey"] != nil {
		t.Errorf("opencode provider.multi.options.apiKey = %#v, want none", multiOpts["apiKey"])
	}
}
