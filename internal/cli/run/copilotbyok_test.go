package run

// copilotbyok_test.go pins copilot's provider delivery — a yolo.env producer, because
// copilot's BYOK is env-var-only (its own help topic: no provider keys exist in any
// copilot config file). The tests run the REAL copilot derive.lua against the REAL
// shipped provider packs, the same tier zaipack_test.go pins claude's half at: what
// lands is what the audit table in docs/design/cerebras-pack-and-copilot-delivery.md
// claims, composed through composePackChannel and argv assembly.

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// copilotLaunch is zaiLaunch with the copilot agent pack beside a provider pack, the
// latter's key hydrated.
func copilotLaunch(t *testing.T, provider string, tune func(*Options)) []string {
	t.Helper()
	packs := []*packload.Pack{officialPack(t, "copilot"), officialPack(t, provider)}
	var env *jsonx.OrderedMap
	if provider == "cerebras" {
		env = cerebrasKey()
	} else {
		env = hydratedKey()
	}
	return zaiLaunch(t, packs, bareConfig(), env, tune)
}

// TestCopilotByokComposesCerebrasThroughTheBridge: `-p cerebras` on a copilot
// launch arms the full BYOK block — and since cerebras now declares an
// anthropic endpoint (the wire bridge's loopback URL, wire-bridge.md §3.3),
// D-3 routes copilot there: the anthropic type, no WIRE_API, the one model
// alias, the hydrated key. This is the flip the bridge shipped: copilot's
// derive reads every endpoint the composed table carries and prefers the
// anthropic one, so the bridge is as much copilot's route as claude's — which
// is why cerebras's `needs` entry names the copilot bin too. The derive is
// UNCHANGED; only the table grew an endpoint.
func TestCopilotByokComposesCerebrasThroughTheBridge(t *testing.T) {
	argv := copilotLaunch(t, "cerebras", func(o *Options) { o.ProfileName = "cerebras" })

	got := envArgValues(argv,
		"COPILOT_PROVIDER_BASE_URL", "COPILOT_PROVIDER_TYPE", "COPILOT_PROVIDER_WIRE_API",
		"COPILOT_MODEL", "COPILOT_PROVIDER_API_KEY")
	want := []string{
		"COPILOT_MODEL=qwen-3.8-27b",
		"COPILOT_PROVIDER_API_KEY=csk-test",
		"COPILOT_PROVIDER_BASE_URL=http://127.0.0.1:8214",
		"COPILOT_PROVIDER_TYPE=anthropic",
	}
	if len(got) != len(want) {
		t.Fatalf("copilot BYOK env = %q, want %q (no WIRE_API on the anthropic type)",
			got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("copilot BYOK env %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestCopilotByokPrefersTheAnthropicRoute: a provider speaking both protocols (zai) gets
// copilot's anthropic spelling — no WIRE_API at all, because copilot's wire_api enum
// speaks only to the openai type (D-3: the anthropic route is the richer surface).
func TestCopilotByokPrefersTheAnthropicRoute(t *testing.T) {
	argv := copilotLaunch(t, "zai", func(o *Options) { o.ProfileName = "zai" })

	got := envArgValues(argv,
		"COPILOT_PROVIDER_BASE_URL", "COPILOT_PROVIDER_TYPE", "COPILOT_PROVIDER_WIRE_API",
		"COPILOT_MODEL", "COPILOT_PROVIDER_API_KEY")
	want := []string{
		"COPILOT_MODEL=glm-5.3",
		"COPILOT_PROVIDER_API_KEY=tok-9",
		"COPILOT_PROVIDER_BASE_URL=https://api.z.ai/api/anthropic",
		"COPILOT_PROVIDER_TYPE=anthropic",
	}
	if len(got) != len(want) {
		t.Fatalf("copilot BYOK env = %q, want %q (no WIRE_API on the anthropic type)",
			got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("copilot BYOK env %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestCopilotByokComposesNothingWithoutTheProfile: presence is not selection — the pack
// selected and the key hydrated still composes no COPILOT_* var, because arming BYOK
// unbidden would switch a GitHub-auth copilot to a custom provider under nobody's feet.
func TestCopilotByokComposesNothingWithoutTheProfile(t *testing.T) {
	argv := copilotLaunch(t, "cerebras", nil)
	if got := envArgValues(argv,
		"COPILOT_PROVIDER_BASE_URL", "COPILOT_PROVIDER_TYPE", "COPILOT_PROVIDER_WIRE_API",
		"COPILOT_MODEL", "COPILOT_PROVIDER_API_KEY"); len(got) != 0 {
		t.Errorf("unprofiled launch carried copilot BYOK env: %q", got)
	}
}

// TestCopilotByokComposesNothingForAnEndpointlessProvider: bedrock names no endpoint at
// all (region facts only), and copilot's `azure` type is Azure OpenAI's deployment URL
// shape, not a bedrock address — so the derive must leave BYOK un-armed rather than
// guess. Runs against the real bedrock provider packs/claude ships.
func TestCopilotByokComposesNothingForAnEndpointlessProvider(t *testing.T) {
	packs := []*packload.Pack{officialPack(t, "copilot"), officialPack(t, "claude")}
	argv := zaiLaunch(t, packs, bareConfig(), emptyEnv(),
		func(o *Options) { o.ProfileName = "bedrock" })

	if got := envArgValues(argv,
		"COPILOT_PROVIDER_BASE_URL", "COPILOT_PROVIDER_TYPE", "COPILOT_PROVIDER_WIRE_API",
		"COPILOT_MODEL", "COPILOT_PROVIDER_API_KEY"); len(got) != 0 {
		t.Errorf("an endpointless provider armed copilot BYOK: %q", got)
	}
}
