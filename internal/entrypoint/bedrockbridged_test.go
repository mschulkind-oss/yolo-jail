package entrypoint

import (
	"encoding/json"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// The region-composed Bedrock upstream as the agents' own derives meet it
// (docs/design/wire-bridge-gateway.md §8 step 1, WG-I39). The shipped `bedrock` names a region and
// no address; the wire bridge's chat-completions adapter declares it fronts the aws-bedrock
// platform (packs/wire-bridge, `from_platforms`), so composition gives `bedrock` the bridge's
// anthropic address, marked for a via profile. claude, which reaches the bridge through that
// address, is routed at it under `bedrock-bridge` and keeps its own Bedrock client on `-p bedrock`;
// copilot and oh-omp, which have no Bedrock client, are carried by the bridge under both (WG-I44).

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

// TestCopilotReachesBedrockThroughTheBridgeOnEitherProfile: copilot has no Bedrock client of its
// own, so the bridge is its only way in, and it takes it on plain `-p bedrock` as under
// `bedrock-bridge` (docs/design/bedrock-plumbing.md OQ-BR1: "through the wire bridge where it has
// none"; the carrier, docs/design/wire-bridge-gateway.md WG-I44). On either it is pointed at the
// bridge's adapter with the bridge's caller token and the list's first model, since the list names
// no `default` and copilot's BYOK needs one (OQ-ML2). In a launch with no bridge it composes
// nothing, as before: nothing would carry it.
func TestCopilotReachesBedrockThroughTheBridgeOnEitherProfile(t *testing.T) {
	for _, profile := range []string{"bedrock-bridge", "bedrock"} {
		got, _ := bridgedBedrockEnv(t, "copilot", "copilot", profile)
		for key, want := range map[string]string{
			"COPILOT_PROVIDER_BASE_URL": "http://127.0.0.1:8214",
			"COPILOT_PROVIDER_TYPE":     "anthropic",
			"COPILOT_MODEL":             "global.anthropic.claude-opus-5-5",
		} {
			if got[key] != want {
				t.Errorf("copilot on %s: %s = %q, want %q (env %v)", profile, key, got[key], want, got)
			}
		}
		if got["COPILOT_PROVIDER_API_KEY"] == "" {
			t.Errorf("copilot on %s sends no credential to the bridge: %v", profile, got)
		}
	}
	packs := testPacksForAgent(t, "copilot", "bedrock")
	for _, p := range packs {
		if p.Name == "wire-bridge" {
			t.Fatalf("copilot and bedrock alone close over the wire bridge, so this checks nothing: %v", packNames(packs))
		}
	}
	table, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles(packs, nil, table)
	if err != nil {
		t.Fatal(err)
	}
	vars, err := packload.AgentEnv(packs, table, map[string]string{"copilot": "bedrock"}, "copilot", "bedrock",
		func(string) (string, bool) { return "", false }, packload.WithResolvedProfiles(resolved))
	if err != nil {
		t.Fatal(err)
	}
	if len(vars) != 0 {
		t.Errorf("copilot on -p bedrock with no bridge in the launch composed %v; want nothing", vars)
	}
}

// TestOmpReachesBedrockThroughTheBridgeOnPlainBedrock is oh-omp's half, on the jail side: the
// host's resolution of the shipped packs, the YOLO_PROFILES its writer emits, the boot's reader,
// the per-surface selection and the real omp derive. oh-omp has no Bedrock client yolo drives
// (its own speaks mantle, which yolo does not ship), so on `-p bedrock` its models.yml row for
// `bedrock` is its via route on the bridge, chat-completions with the bridge's caller token, as
// under `bedrock-bridge`. claude, beside it, gets no via URL: it keeps its own Bedrock client.
func TestOmpReachesBedrockThroughTheBridgeOnPlainBedrock(t *testing.T) {
	packs := testPacksForAgent(t, "omp", "claude", "bedrock")
	table, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles(packs, nil, table)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := jsonx.DumpsCompact(packload.ProfilesWireTable(resolved))
	if err != nil {
		t.Fatal(err)
	}
	crossed := (&Env{Vars: map[string]string{"YOLO_PROFILES": wire}}).LoadProfiles()
	use := map[string]string{"oh-omp": "bedrock", "claude": "bedrock"}
	sel := surfaceSelectionFor(packs, crossed, use, nil, manifest.Surface{Agent: "oh-omp", Name: "models"})
	if sel.ViaURL != viaBase+"/agent/oh-omp" || sel.ViaAPIKeyEnvName != "YOLO_SERVICE_WIRE_BRIDGE_TOKEN" {
		t.Fatalf("oh-omp's selection on -p bedrock: via %q key %q, want its route on the bridge", sel.ViaURL,
			sel.ViaAPIKeyEnvName)
	}
	if claude := surfaceSelectionFor(packs, crossed, use, nil, manifest.Surface{Agent: "claude"}); claude.ViaURL != "" {
		t.Errorf("claude on -p bedrock was given via URL %q; it has its own Bedrock client", claude.ViaURL)
	}
	var providers map[string]any
	if err := json.Unmarshal([]byte(mustCompactJSON(t, table)), &providers); err != nil {
		t.Fatal(err)
	}
	row, _ := ompModelsFor(t, sel, providers)["bedrock"].(map[string]any)
	if row["baseUrl"] != sel.ViaURL || row["api"] != "openai-completions" || row["apiKey"] != sel.ViaAPIKeyEnvName {
		t.Errorf("oh-omp's bedrock row = %v, want its via URL speaking chat-completions with the bridge's token", row)
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
