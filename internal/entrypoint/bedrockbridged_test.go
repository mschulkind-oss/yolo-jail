package entrypoint

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// The region-composed Bedrock upstream as the agents' own derives meet it
// (docs/design/wire-bridge-gateway.md §8 step 1, WG-I39). The shipped `bedrock` names a region and
// no address; the wire bridge's chat-completions adapter declares it fronts the aws-bedrock
// platform (packs/wire-bridge, `from_platforms`), so composition gives `bedrock` the bridge's
// anthropic address, marked for a via profile. copilot and claude, which reach the bridge through
// that address, are routed at it under `bedrock-bridge` and at nothing new on `-p bedrock`.

// bridgedBedrockEnv composes the shipped packs for agent and runs agent's env producer under
// profile, returning its variables by name, with the composed `bedrock` entry.
func bridgedBedrockEnv(t *testing.T, agent, pack, profile string, extra ...string) (map[string]string, *jsonx.OrderedMap) {
	t.Helper()
	packs := testPacksForAgent(t, pack, append([]string{"bedrock", "wire-bridge"}, extra...)...)
	table, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles(packs, nil, table)
	if err != nil {
		t.Fatal(err)
	}
	vars, err := packload.AgentEnv(packs, table, map[string]string{agent: profile}, agent, profile,
		func(string) (string, bool) { return "", false }, packload.WithResolvedProfiles(resolved))
	if err != nil {
		t.Fatalf("%s on %s: %v", agent, profile, err)
	}
	got := map[string]string{}
	for _, v := range vars {
		got[v.Key] = v.Value
	}
	v, _ := table.Get("bedrock")
	entry, _ := v.(*jsonx.OrderedMap)
	return got, entry
}

// forViaOf is the service entry's anthropic endpoint is marked for, "" for none.
func forViaOf(entry *jsonx.OrderedMap) string {
	v, _ := entry.Get("endpoints")
	eps, _ := v.(*jsonx.OrderedMap)
	if eps == nil {
		return ""
	}
	ev, _ := eps.Get("anthropic")
	ep, _ := ev.(*jsonx.OrderedMap)
	if ep == nil {
		return ""
	}
	s, _ := ep.Get(packload.ForViaKey)
	str, _ := s.(string)
	return str
}

// TestTheBridgeFrontsTheRegionNamedBedrockForAViaProfile pins the composition itself: `bedrock`'s
// only endpoint is the bridge's anthropic adapter address, carrying the bridge's caller token and
// marked for a profile that routes through wire-bridge. It fails if packs/wire-bridge stops
// declaring the platform or packload.adaptEndpoints stops composing from it.
func TestTheBridgeFrontsTheRegionNamedBedrockForAViaProfile(t *testing.T) {
	_, entry := bridgedBedrockEnv(t, "copilot", "copilot", "bedrock-bridge")
	v, _ := entry.Get("endpoints")
	eps, _ := v.(*jsonx.OrderedMap)
	if eps == nil || len(eps.Keys()) != 1 {
		t.Fatalf("bedrock's composed endpoints = %v, want the bridge's anthropic address alone", v)
	}
	ev, _ := eps.Get("anthropic")
	ep, _ := ev.(*jsonx.OrderedMap)
	for key, want := range map[string]string{"base_url": "http://127.0.0.1:8214",
		"api_key_env_name": "YOLO_SERVICE_WIRE_BRIDGE_TOKEN", packload.ForViaKey: "wire-bridge"} {
		if got, _ := ep.Get(key); got != want {
			t.Errorf("bedrock's anthropic endpoint %s = %v, want %q", key, got, want)
		}
	}
}

// TestCopilotReachesBedrockThroughTheBridgeOnlyUnderAVia: copilot has no Bedrock client of its own,
// so the bridge is its only way in. Under `bedrock-bridge` it is pointed at the bridge's adapter
// with the bridge's caller token and the list's first model, since the list names no `default`
// and copilot's BYOK needs one (OQ-ML2). On `-p bedrock` it composes nothing, as before: the
// address is the via profile's.
func TestCopilotReachesBedrockThroughTheBridgeOnlyUnderAVia(t *testing.T) {
	got, _ := bridgedBedrockEnv(t, "copilot", "copilot", "bedrock-bridge")
	for key, want := range map[string]string{
		"COPILOT_PROVIDER_BASE_URL": "http://127.0.0.1:8214",
		"COPILOT_PROVIDER_TYPE":     "anthropic",
		"COPILOT_MODEL":             "global.anthropic.claude-opus-5-5",
	} {
		if got[key] != want {
			t.Errorf("copilot on bedrock-bridge: %s = %q, want %q (env %v)", key, got[key], want, got)
		}
	}
	if got["COPILOT_PROVIDER_API_KEY"] == "" {
		t.Errorf("copilot on bedrock-bridge sends no credential to the bridge: %v", got)
	}
	native, _ := bridgedBedrockEnv(t, "copilot", "copilot", "bedrock")
	if len(native) != 0 {
		t.Errorf("copilot on -p bedrock, which routes through no via, composed %v; want nothing", native)
	}
}

// TestClaudeRidesTheAdapterRouteOnlyUnderAVia: claude's everything profile is routed at the
// bridge's adapter address, and `-p bedrock` keeps claude's own Bedrock client, the address
// notwithstanding.
func TestClaudeRidesTheAdapterRouteOnlyUnderAVia(t *testing.T) {
	got, _ := bridgedBedrockEnv(t, "claude", "claude", "bedrock-bridge")
	if got["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:8214" || got["CLAUDE_CODE_USE_BEDROCK"] != "" {
		t.Errorf("claude on bedrock-bridge: %v, want ANTHROPIC_BASE_URL at the adapter and no Bedrock switch", got)
	}
	native, _ := bridgedBedrockEnv(t, "claude", "claude", "bedrock")
	if native["ANTHROPIC_BASE_URL"] != "" || native["CLAUDE_CODE_USE_BEDROCK"] != "1" {
		t.Errorf("claude on -p bedrock: %v, want its own Bedrock client and no bridge address", native)
	}
}

// TestNoNativeBedrockAgentIsRefusedOverTheBridgesAddress: the anthropic address composed for a via
// profile is not an endpoint the provider names, so codex, pi and opencode, which speak no
// anthropic, are not refused on `-p bedrock` as a provider they cannot speak (ResolveProtocol), and
// run their own Bedrock clients as before. claude is in each launch, since the address is composed
// only where some agent speaks anthropic.
func TestNoNativeBedrockAgentIsRefusedOverTheBridgesAddress(t *testing.T) {
	for _, agent := range []string{"codex", "pi", "opencode"} {
		_, entry := bridgedBedrockEnv(t, agent, agent, "bedrock", "claude") // fails on the refusal
		if forViaOf(entry) != "wire-bridge" {
			t.Fatalf("%s: the launch composed no bridge address onto bedrock, so this checks nothing", agent)
		}
	}
}
