package wirebridged

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The output cap the translating route writes (docs/reference/wire-bridge.md, "The output cap";
// docs/design/wire-bridge-gateway.md §2.4): every Anthropic Messages request carries
// `max_tokens`, and the chat-completions request the route builds from it carries the cap as
// `max_completion_tokens`, OpenAI's current field, which every chat-completions upstream a
// shipped pack routes through the bridge accepts. GPT-6.1 Sol on Bedrock refused the older
// `max_tokens` with a 400 on 2026-10-01, so claude's everything profile and copilot failed every
// turn on it. Measured through the PRODUCTION boot: the shipped packs composed as a launch
// composes them, the daemon's own plan, servePlan, and captureUpstream standing in for the
// network.

// capFields decodes the two cap fields of one upstream chat-completions body.
func capFields(t *testing.T, body []byte) (completion, legacy json.RawMessage) {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("the upstream body is not JSON: %v\n%s", err, body)
	}
	return doc["max_completion_tokens"], doc["max_tokens"]
}

// TestTheShippedBedrockBridgeSendsGPTModelsTheirCapAsMaxCompletionTokens is the defect's
// reproduction: claude on the shipped `bedrock-bridge`, asking for each OpenAI model on the
// shipped Bedrock list, reaches runtime's chat-completions with the cap it sent as
// max_completion_tokens and no max_tokens at all. It fails if the translation writes the cap
// under the field Sol refuses.
func TestTheShippedBedrockBridgeSendsGPTModelsTheirCapAsMaxCompletionTokens(t *testing.T) {
	up, adapter, _, _ := servedShippedBedrockBridge(t, "us-east-1", "us-east-1")
	for _, model := range []string{"us.openai.gpt-6.1-sol", "global.openai.gpt-6-astra"} {
		before := up.calls()
		resp, body := postTo(t, "http://"+adapter+"/v1/messages",
			`{"model":"`+model+`","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`, nil)
		if resp.StatusCode != 200 || up.calls() != before+1 {
			t.Errorf("%s: status %d, upstream calls %d: %s", model, resp.StatusCode, up.calls()-before, body)
			continue
		}
		if got := up.requests[before].URL.Path; got != "/openai/v1/chat/completions" {
			t.Errorf("%s went to %s, want runtime's chat-completions", model, got)
		}
		completion, legacy := capFields(t, up.bodies[before])
		if string(completion) != "16" || legacy != nil {
			t.Errorf("%s: max_completion_tokens %s, max_tokens %s; want the cap as max_completion_tokens 16 "+
				"and no max_tokens, which GPT-6.1 Sol refuses with a 400:\n%s", model, completion, legacy, up.bodies[before])
		}
	}
}

// capThroughShippedRoute boots claude's adapter route on the shipped profile named profile, over
// the shipped packs with an optional user `providers` layer, sends one request with a cap of 16,
// and returns the body the upstream received.
func capThroughShippedRoute(t *testing.T, user, profile, keyLine string) []byte {
	t.Helper()
	_, body := capRequestThroughShippedRoute(t, user, profile, keyLine, "m")
	return body
}

// capRequestThroughShippedRoute is capThroughShippedRoute for a named model, returning the
// upstream request as well as its body, so a caller can tell which handler served it.
func capRequestThroughShippedRoute(t *testing.T, user, profile, keyLine, model string) (*http.Request, []byte) {
	t.Helper()
	clearAWS(t)
	for _, v := range []string{"CEREBRAS_API_KEY", "KILO_API_KEY"} {
		t.Setenv(v, "")
	}
	_ = captureDiag(t)
	up := withUpstream(t)
	providers, resolved := shippedBridgeTables(t, user)
	p := planFor(providers, map[string]string{"claude": profile}, resolved)
	if p.adapter == nil {
		t.Fatalf("no adapter route for claude on %s: %s", profile, p.adapterWhy)
	}
	home := t.TempDir()
	writeAgentKey(t, home, "claude", keyLine)
	p.adapter.ListenAddr = freeLoopback(t)
	startPlan(t, p, home)
	resp, body := postTo(t, "http://"+p.adapter.ListenAddr+"/v1/messages",
		`{"model":"`+model+`","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`, nil)
	if resp.StatusCode != 200 || up.calls() != 1 {
		t.Fatalf("claude on %s: status %d, upstream calls %d: %s", profile, resp.StatusCode, up.calls(), body)
	}
	return up.requests[0], up.bodies[0]
}

// TestTheShippedChatCompletionsUpstreamsGetMaxCompletionTokens: the other two providers a shipped
// pack routes through the translating route take the default too. Cerebras documents
// max_completion_tokens with max_tokens as its alias, and Kilo's own pi provider sends
// max_completion_tokens to the same gateway (docs/reference/wire-bridge.md, "The output cap").
func TestTheShippedChatCompletionsUpstreamsGetMaxCompletionTokens(t *testing.T) {
	for _, tc := range []struct{ profile, key string }{
		{"cerebras", "export CEREBRAS_API_KEY=${CEREBRAS_API_KEY:-'cerebras-key'}"},
		{"kilo", "export KILO_API_KEY=${KILO_API_KEY:-'kilo-key'}"},
	} {
		t.Run(tc.profile, func(t *testing.T) {
			sent := capThroughShippedRoute(t, "", tc.profile, tc.key)
			if completion, legacy := capFields(t, sent); string(completion) != "16" || legacy != nil {
				t.Errorf("max_completion_tokens %s, max_tokens %s; want the cap as max_completion_tokens 16:\n%s",
					completion, legacy, sent)
			}
		})
	}
}

// TestAProviderDeclaringMaxTokensGetsItsCapAsMaxTokens is the declared fact end to end: the
// option in the user's providers layer reaches the provider's resolved profile, the boot's route
// and the translation, and the upstream receives the cap as max_tokens alone. It fails if the
// boot stops reading the option or stops handing it to the handler.
func TestAProviderDeclaringMaxTokensGetsItsCapAsMaxTokens(t *testing.T) {
	sent := capThroughShippedRoute(t, `{"cerebras": {"options": {"max_tokens_field": "max_tokens"}}}`,
		"cerebras", "export CEREBRAS_API_KEY=${CEREBRAS_API_KEY:-'cerebras-key'}")
	if completion, legacy := capFields(t, sent); completion != nil || string(legacy) != "16" {
		t.Errorf("max_completion_tokens %s, max_tokens %s; want the cap as max_tokens 16 alone:\n%s",
			completion, legacy, sent)
	}
}

// TestASignedBedrockRouteHonorsTheDeclaredMaxTokensField: the declared fact reaches the SIGNED
// chat-completions handler too, the one a Bedrock upstream is served by, and not only the keyed
// one TestAProviderDeclaringMaxTokensGetsItsCapAsMaxTokens reaches. No shipped provider declares
// the option on Bedrock, where GPT-6.1 Sol refuses `max_tokens`; this declares it anyway, on the
// shipped bedrock-bridge, because the default cap field cannot show which options the signed
// handler was handed. It fails if adapterHandler's signed call site stops passing the route's
// options (route.chatOptions) and hands that handler the defaults.
func TestASignedBedrockRouteHonorsTheDeclaredMaxTokensField(t *testing.T) {
	sent, body := capRequestThroughShippedRoute(t,
		`{"bedrock": {"options": {"max_tokens_field": "max_tokens"}}}`,
		"bedrock-bridge", awsPair("AKIDCLAUDE", "us-east-1"), "us.openai.gpt-6.1-sol")
	if got := sent.URL.String(); got != "https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1/chat/completions" {
		t.Fatalf("the request went to %s, want runtime's chat-completions", got)
	}
	if auth := sent.Header.Get("Authorization"); !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AKIDCLAUDE/") {
		t.Fatalf("Authorization %q: the request was not served by the signed handler", auth)
	}
	if completion, legacy := capFields(t, body); completion != nil || string(legacy) != "16" {
		t.Errorf("max_completion_tokens %s, max_tokens %s; want the cap as max_tokens 16 alone, as the "+
			"provider declares:\n%s", completion, legacy, body)
	}
}

// TestResolveRouteReadsTheMaxTokensFieldServiceFact: only the exact spelling "max_tokens" moves
// the cap off the default, by the rule supports_usage_in_streaming is read with, so a typo keeps
// OpenAI's current field rather than switching to the deprecated one.
func TestResolveRouteReadsTheMaxTokensFieldServiceFact(t *testing.T) {
	for _, tc := range []struct {
		name, profile string
		want          bool
	}{
		{"undeclared keeps the default", `{"p":{"provider":"cerebras"}}`, false},
		{"max_completion_tokens keeps the default", `{"p":{"provider":"cerebras","max_tokens_field":"max_completion_tokens"}}`, false},
		{"max_tokens writes max_tokens", `{"p":{"provider":"cerebras","max_tokens_field":"max_tokens"}}`, true},
		{"an unrecognized value keeps the default", `{"p":{"provider":"cerebras","max_tokens_field":"maxTokens"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			route, idle := resolveRoute(routeEnv(bridgedProviders, tc.profile, `{"claude":"p"}`))
			if idle != "" {
				t.Fatalf("a bridged route must serve, got idle: %s", idle)
			}
			if route.CapAsMaxTokens != tc.want {
				t.Errorf("CapAsMaxTokens = %v, want %v", route.CapAsMaxTokens, tc.want)
			}
			if got := route.chatOptions().CapAsMaxTokens; got != tc.want {
				t.Errorf("the handler's ChatOptions.CapAsMaxTokens = %v, want %v", got, tc.want)
			}
		})
	}
}
