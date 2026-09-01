package agentenv

import (
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// cfgFrom builds a merged-config shape from JSON, the way LoadConfig would hand it over.
func cfgFrom(t *testing.T, text string) *jsonx.OrderedMap {
	t.Helper()
	v, err := jsonx.Decode([]byte(text))
	if err != nil {
		t.Fatalf("decoding test config: %v", err)
	}
	m, ok := v.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("test config is not an object: %T", v)
	}
	return m
}

// bedrockTable is the composed providers entry the bedrock shape composes from: the
// delivery shape packs/claude ships, the region and model ids the user's own
// `providers.bedrock` carries. This is the whole bedrock env — the shape says which
// variable takes its value from where, and the entry says what the value IS.
func bedrockTable(t *testing.T) *jsonx.OrderedMap {
	t.Helper()
	return cfgFrom(t, `{"bedrock": {
		"region": "us-east-1",
		"models": {"default": "opus-x", "haiku": "haiku-x", "sonnet": "sonnet-x"},
		"env_shape": {"anthropic": {
			"ANTHROPIC_DEFAULT_HAIKU_MODEL":  "{model:haiku}",
			"ANTHROPIC_DEFAULT_OPUS_MODEL":   "{model:default}",
			"ANTHROPIC_DEFAULT_SONNET_MODEL": "{model:sonnet}",
			"AWS_REGION":                     "{region}"
		}}
	}}`)
}

// TestResolveBedrockFullBlock pins the bedrock delivery the shipped packs/claude shape
// composes. The order here is the composer's own: the shape's variable names sorted — no
// agent or provider is named in the code that produces it, which is the point of moving
// the shape out of Go and into the pack.
func TestResolveBedrockFullBlock(t *testing.T) {
	got := Resolve(bedrockTable(t), "claude", "bedrock", "bedrock", nil)
	want := []Var{
		{Key: "ANTHROPIC_DEFAULT_HAIKU_MODEL", Value: "haiku-x"},
		{Key: "ANTHROPIC_DEFAULT_OPUS_MODEL", Value: "opus-x"},
		{Key: "ANTHROPIC_DEFAULT_SONNET_MODEL", Value: "sonnet-x"},
		{Key: "AWS_REGION", Value: "us-east-1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Resolve = %+v\nwant %+v", got, want)
	}
}

// TestResolveBedrockWithNothingToFillIn: the profile says bedrock but no provider block
// configures it. The shape delivers nothing it cannot fill — an empty AWS_REGION or an
// empty model id would be a request to the wrong place — and there is no hardcoded
// fallback var to fall back to: what the variant itself contributes (claude's
// CLAUDE_CODE_USE_BEDROCK) is a profile `env` literal, delivered by the pack env channel,
// not by this composition.
func TestResolveBedrockWithNothingToFillIn(t *testing.T) {
	for name, table := range map[string]*jsonx.OrderedMap{
		"no providers at all": nil,
		"entry without the facts": cfgFrom(t,
			`{"bedrock": {"env_shape": {"anthropic": {"AWS_REGION": "{region}"}}}}`),
	} {
		t.Run(name, func(t *testing.T) {
			if got := Resolve(table, "claude", "bedrock", "bedrock", nil); len(got) != 0 {
				t.Errorf("Resolve = %+v, want nothing", got)
			}
		})
	}
}

// TestResolveQuietCases: a shared config routinely names providers a machine cannot use.
// None of these may error or emit anything.
func TestResolveQuietCases(t *testing.T) {
	providers := bedrockTable(t)
	cases := []struct {
		name     string
		agent    string
		profile  string
		provider string
	}{
		{"no profile for this agent", "claude", "", "bedrock"},
		{"unknown provider name", "claude", "not-a-provider", "not-a-provider"},
		{"an agent that speaks another protocol gets no anthropic shape", "codex", "bedrock", "bedrock"},
		{"empty agent name", "", "bedrock", "bedrock"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Resolve(providers, tc.agent, tc.profile, tc.provider, nil)
			if len(got) != 0 {
				t.Errorf("Resolve = %+v, want nothing", got)
			}
		})
	}
}

// TestResolveSkipsNonStringAndEmpty: a wrong-typed or empty model must be dropped rather
// than emitted as an empty assignment, which would override the agent's own default.
func TestResolveSkipsNonStringAndEmpty(t *testing.T) {
	providers := cfgFrom(t, `{"bedrock": {
		"region": "",
		"models": {"default": 42, "haiku": "", "sonnet": "sonnet-x"},
		"env_shape": {"anthropic": {
			"ANTHROPIC_DEFAULT_HAIKU_MODEL":  "{model:haiku}",
			"ANTHROPIC_DEFAULT_OPUS_MODEL":   "{model:default}",
			"ANTHROPIC_DEFAULT_SONNET_MODEL": "{model:sonnet}",
			"AWS_REGION":                     "{region}"
		}}
	}}`)
	got := Resolve(providers, "claude", "bedrock", "bedrock", nil)
	want := []Var{{Key: "ANTHROPIC_DEFAULT_SONNET_MODEL", Value: "sonnet-x"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Resolve = %+v\nwant %+v", got, want)
	}
}

// --- the provider's own delivery shape (OQ-14, zai-plumbing.md §4.1 Route B) ---

// zaiTable is the composed providers table a zai-style pack produces: endpoints by
// protocol, the credential as a variable NAME, and one env shape per protocol
// (zai-plumbing.md §4.1's env_shape, verbatim).
func zaiTable(t *testing.T) *jsonx.OrderedMap {
	t.Helper()
	return cfgFrom(t, `{"zai": {
		"api_key_env": "ZAI_API_KEY",
		"endpoints": {
			"anthropic": {"base_url": "https://api.z.ai/api/anthropic"},
			"openai":    {"base_url": "https://api.z.ai/api/paas/v4", "wire_api": "openai-chat"}
		},
		"env_shape": {
			"anthropic": {"ANTHROPIC_BASE_URL": "{endpoint}", "ANTHROPIC_AUTH_TOKEN": "{key}"},
			"openai":    {"OPENAI_BASE_URL": "{endpoint}"}
		}
	}}`)
}

// lookupOf returns a Lookup over fixed values — the shape both notches build from their
// hydrated env_sources plus the environment they already carry.
func lookupOf(values map[string]string) Lookup {
	return func(name string) (string, bool) {
		v, ok := values[name]
		return v, ok
	}
}

// TestResolveComposesTheProviderEnvShapeForTheAgentsProtocol is OQ-14's contract. One
// provider, one key, two protocols: claude is delivered the anthropic shape (the endpoint
// of THAT protocol, and the key relayed from the hydrated variable), an OpenAI-shaped
// agent the openai one. No agent is named anywhere in the provider.
func TestResolveComposesTheProviderEnvShapeForTheAgentsProtocol(t *testing.T) {
	lookup := lookupOf(map[string]string{"ZAI_API_KEY": "tok-9"})

	got := Resolve(zaiTable(t), "claude", "zai", "zai", lookup)
	want := []Var{
		{Key: "ANTHROPIC_AUTH_TOKEN", Value: "tok-9"},
		{Key: "ANTHROPIC_BASE_URL", Value: "https://api.z.ai/api/anthropic"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("claude env = %+v\nwant %+v", got, want)
	}

	// The same table, an agent that speaks the other protocol: the shape follows the
	// agent's protocol, and the openai shape names no key, so no credential is invented
	// for an agent that reads its key through its own config file.
	openai := Resolve(zaiTable(t), "pi", "zai", "zai", lookup)
	wantOpenai := []Var{{Key: "OPENAI_BASE_URL", Value: "https://api.z.ai/api/paas/v4"}}
	if !reflect.DeepEqual(openai, wantOpenai) {
		t.Errorf("pi env = %+v\nwant %+v", openai, wantOpenai)
	}
}

// TestResolveEnvShapeFollowsRequiresProviderNotTheProfileName: a variant may name a
// provider other than itself, and the shape must come from the provider NAMED.
func TestResolveEnvShapeFollowsRequiresProviderNotTheProfileName(t *testing.T) {
	providers := cfgFrom(t, `{
	  "glm": {"endpoints": {"anthropic": {"base_url": "https://wrong.example/anthropic"}}},
	  "zai": {"api_key_env": "ZAI_API_KEY",
	          "endpoints": {"anthropic": {"base_url": "https://api.z.ai/api/anthropic"}},
	          "env_shape": {"anthropic": {"ANTHROPIC_BASE_URL": "{endpoint}"}}}
	}`)
	got := Resolve(providers, "claude", "glm", "zai", nil)
	want := []Var{{Key: "ANTHROPIC_BASE_URL", Value: "https://api.z.ai/api/anthropic"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("env = %+v\nwant %+v (from the REQUIRED provider, not the profile name)", got, want)
	}
}

// TestResolveEnvShapeQuietWhenAnInputIsMissing: every half-composed shape must drop the
// var it cannot fill rather than emit an empty one — an empty base URL is a request to the
// wrong host, and an empty token is a credential that gets SENT.
func TestResolveEnvShapeQuietWhenAnInputIsMissing(t *testing.T) {
	cases := []struct {
		name      string
		table     string
		agent     string
		provider  string
		lookup    Lookup
		wantSuite []Var
	}{
		{
			name:  "provider absent from the composed table",
			table: `{"other": {"env_shape": {"anthropic": {"A": "{endpoint}"}}}}`,
			agent: "claude", provider: "zai",
		},
		{
			name:  "no env shape declared",
			table: `{"zai": {"endpoints": {"anthropic": {"base_url": "https://x.example"}}}}`,
			agent: "claude", provider: "zai",
		},
		{
			name: "no shape for the protocol this agent speaks",
			table: `{"zai": {"endpoints": {"openai": {"base_url": "https://x.example/v4"}},
			        "env_shape": {"openai": {"OPENAI_BASE_URL": "{endpoint}"}}}}`,
			agent: "claude", provider: "zai",
		},
		{
			name: "agent speaks nothing yolo knows",
			table: `{"zai": {"endpoints": {"anthropic": {"base_url": "https://x.example"}},
			        "env_shape": {"anthropic": {"ANTHROPIC_BASE_URL": "{endpoint}"}}}}`,
			agent: "copilot", provider: "zai",
		},
		{
			name: "endpoint missing for the protocol the shape names",
			table: `{"zai": {"endpoints": {"openai": {"base_url": "https://x.example/v4"}},
			        "env_shape": {"anthropic": {"ANTHROPIC_BASE_URL": "{endpoint}"}}}}`,
			agent: "claude", provider: "zai",
		},
		{
			name: "credential pointer names no variable",
			table: `{"zai": {"endpoints": {"anthropic": {"base_url": "https://x.example"}},
			        "env_shape": {"anthropic": {"ANTHROPIC_AUTH_TOKEN": "{key}"}}}}`,
			agent: "claude", provider: "zai", lookup: lookupOf(map[string]string{"ANY": "v"}),
		},
		{
			name: "the named variable was never hydrated",
			table: `{"zai": {"api_key_env": "ZAI_API_KEY",
			        "env_shape": {"anthropic": {"ANTHROPIC_AUTH_TOKEN": "{key}"}}}}`,
			agent: "claude", provider: "zai", lookup: lookupOf(nil),
		},
		{
			name:  "a value that is neither placeholder renders nothing",
			table: `{"zai": {"env_shape": {"anthropic": {"ANTHROPIC_BASE_URL": "https://x.example"}}}}`,
			agent: "claude", provider: "zai",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Resolve(cfgFrom(t, tc.table), tc.agent, "zai", tc.provider, tc.lookup)
			if len(got) != 0 {
				t.Errorf("Resolve = %+v, want nothing", got)
			}
		})
	}
}

// TestResolveEnvShapeEndpointSurvivesAMissingKey: the two placeholders are independent.
// A provider whose key is not hydrated yet still delivers its endpoint — the shape is not
// all-or-nothing, and the §6.2 preflight is what escalates the missing half.
func TestResolveEnvShapeEndpointSurvivesAMissingKey(t *testing.T) {
	providers := cfgFrom(t, `{"zai": {"api_key_env": "ZAI_API_KEY",
	  "endpoints": {"anthropic": {"base_url": "https://api.z.ai/api/anthropic"}},
	  "env_shape": {"anthropic": {"ANTHROPIC_BASE_URL": "{endpoint}", "ANTHROPIC_AUTH_TOKEN": "{key}"}}}}`)
	got := Resolve(providers, "claude", "zai", "zai", nil)
	want := []Var{{Key: "ANTHROPIC_BASE_URL", Value: "https://api.z.ai/api/anthropic"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("env = %+v\nwant %+v", got, want)
	}
}

// TestProtocolFor pins the resolution table itself: the §5 table is the one place the
// agent→protocol mapping lives in Go, so a wrong entry here is a wrong env for every
// launch, and an agent that gains a protocol gains it HERE.
func TestProtocolFor(t *testing.T) {
	want := map[string]string{
		"claude":   "anthropic",
		"pi":       "openai",
		"codex":    "openai",
		"opencode": "openai",
	}
	for agent, proto := range want {
		if got := ProtocolFor(agent); got != proto {
			t.Errorf("ProtocolFor(%q) = %q, want %q", agent, got, proto)
		}
	}
	for _, agent := range []string{"", "copilot", "agy", "not-an-agent"} {
		if got := ProtocolFor(agent); got != "" {
			t.Errorf("ProtocolFor(%q) = %q, want none", agent, got)
		}
	}
}

func TestApplyOverlaysInPlaceAndAppends(t *testing.T) {
	environ := []string{"PATH=/bin", "AWS_REGION=old", "HOME=/home/a"}
	got := Apply(environ, []Var{
		{Key: "AWS_REGION", Value: "us-east-1"},
		{Key: "CLAUDE_CODE_USE_BEDROCK", Value: "1"},
	})
	want := []string{"PATH=/bin", "AWS_REGION=us-east-1", "HOME=/home/a", "CLAUDE_CODE_USE_BEDROCK=1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Apply = %q\nwant %q", got, want)
	}
	// The input must not be mutated — callers pass os.Environ() and may reuse it.
	if environ[1] != "AWS_REGION=old" {
		t.Errorf("Apply mutated its input: %q", environ)
	}
}

// TestApplyUnsetRemovesRatherThanEmpties is the §2.2 `unset AWS_PROFILE` case, and the
// distinction is the whole reason Var has an Unset field: AWS_PROFILE= is not the same
// as no AWS_PROFILE, and no config surface can express the latter at all.
func TestApplyUnsetRemovesRatherThanEmpties(t *testing.T) {
	got := Apply([]string{"PATH=/bin", "AWS_PROFILE=work", "HOME=/home/a"},
		[]Var{{Key: "AWS_PROFILE", Unset: true}})
	want := []string{"PATH=/bin", "HOME=/home/a"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Apply unset = %q\nwant %q", got, want)
	}
	for _, kv := range got {
		if envKey(kv) == "AWS_PROFILE" {
			t.Fatalf("AWS_PROFILE survived as %q", kv)
		}
	}
}

func TestApplyUnsetOfAbsentKeyIsQuiet(t *testing.T) {
	got := Apply([]string{"PATH=/bin"}, []Var{{Key: "AWS_PROFILE", Unset: true}})
	if !reflect.DeepEqual(got, []string{"PATH=/bin"}) {
		t.Errorf("Apply = %q", got)
	}
}

// TestApplyUnsetThenSetEndsSet: order within vars is honored, so a removal followed by an
// assignment of the same key leaves it SET. Without this, "unset then set" would silently
// lose the assignment.
func TestApplyUnsetThenSetEndsSet(t *testing.T) {
	got := Apply([]string{"AWS_PROFILE=work"}, []Var{
		{Key: "AWS_PROFILE", Unset: true},
		{Key: "AWS_PROFILE", Value: "fresh"},
	})
	if !reflect.DeepEqual(got, []string{"AWS_PROFILE=fresh"}) {
		t.Errorf("Apply = %q, want [AWS_PROFILE=fresh]", got)
	}
}

// TestApplyDuplicateInheritedKey: execve semantics are last-wins, so a duplicated key in
// the inherited environ must collapse to one slot before the overlay targets it.
func TestApplyDuplicateInheritedKey(t *testing.T) {
	got := Apply([]string{"A=1", "A=2", "B=3"}, []Var{{Key: "A", Value: "3"}})
	want := []string{"A=3", "B=3"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Apply = %q\nwant %q", got, want)
	}
}

func TestApplyHandlesBareKeyEntry(t *testing.T) {
	got := Apply([]string{"WEIRD"}, []Var{{Key: "WEIRD", Value: "now-set"}})
	if !reflect.DeepEqual(got, []string{"WEIRD=now-set"}) {
		t.Errorf("Apply = %q", got)
	}
}
