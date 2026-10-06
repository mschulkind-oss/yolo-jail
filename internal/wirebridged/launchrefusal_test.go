package wirebridged

// launchrefusal_test.go pins ViaRouteGate (docs/design/wire-bridge-gateway.md WG-I13,
// WG-I14, WG-I15): the launcher asks each via agent's derives whether its config points it
// at its via URL, discloses a via that re-points nothing, refuses exactly the re-pointed
// agents the daemon, booted from the same tables, serves no route for, and discloses a route
// that lacks the wire the agent prefers. The fixture agents are named for their derive's
// rule rather than after a shipped agent; the shipped derives are pinned at the end.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

const gateProviders = `{
  "zai": {"api_key_env_name": "ZAI_API_KEY", "endpoints": {
    "openai": {"base_url": "https://api.z.ai/api/paas/v4", "wire_api": "openai-chat-completions"}}},
  "resp": {"endpoints": {
    "openai": {"base_url": "https://resp.example/v1", "wire_api": "openai-responses"}}},
  "both": {"endpoints": {
    "openai": {"base_url": "https://both.example/v1"}}},
  "anthro": {"endpoints": {
    "anthropic": {"base_url": "https://anthro.example"}}},
  "openai-codex": {"endpoints": {
    "openai-responses": {"base_url": "https://chatgpt.com/backend-api/codex", "wire_api": "openai-responses"}}}
}`

// gateManifest installs five agents: chatty prefers `openai` (chat-completions), resper
// prefers `openai-responses`, anth prefers `anthropic`, bare declares no protocols, and
// broken prefers `openai` but its derive fails. Each has one rendered config surface its
// derive writes.
const gateManifest = `{"contributes": [
  {"kind": "program", "bin": "chatty", "via": "npm", "package": "@example/chatty", "protocols": ["openai", "openai-responses"]},
  {"kind": "program", "bin": "resper", "via": "npm", "package": "@example/resper", "protocols": ["openai-responses", "openai"]},
  {"kind": "program", "bin": "anth", "via": "npm", "package": "@example/anth", "protocols": ["anthropic"]},
  {"kind": "program", "bin": "bare", "via": "npm", "package": "@example/bare"},
  {"kind": "program", "bin": "broken", "via": "npm", "package": "@example/broken", "protocols": ["openai"]},
  {"kind": "config", "config": [
    {"agent": "chatty", "name": "config", "codec": "json", "mode": "computed", "path": "~/.chatty/config.json"},
    {"agent": "resper", "name": "config", "codec": "json", "mode": "computed", "path": "~/.resper/config.json"},
    {"agent": "anth", "name": "config", "codec": "json", "mode": "computed", "path": "~/.anth/config.json"},
    {"agent": "bare", "name": "config", "codec": "json", "mode": "computed", "path": "~/.bare/config.json"},
    {"agent": "broken", "name": "config", "codec": "json", "mode": "computed", "path": "~/.broken/config.json"}
  ]}
]}`

// gateDerive gives the fixture agents the two rules the shipped derives follow. chatty, anth
// and bare re-point whatever provider is selected (opencode's rule). resper re-points only a
// provider with a Responses endpoint, and never openai-codex, its built-in subscription
// client (codex's rule).
const gateDerive = `
local function repointAll(ctx)
  if not ctx.via_url or ctx.via_url == "" then return {} end
  return { providers = { [ctx.selected_provider] = { baseUrl = ctx.via_url } } }
end
yolo.derive("chatty", "config", repointAll)
yolo.derive("anth", "config", repointAll)
yolo.derive("bare", "config", repointAll)
yolo.derive("broken", "config", function(ctx) error("broken derive") end)
yolo.derive("resper", "config", function(ctx)
  if not ctx.via_url or ctx.via_url == "" or ctx.selected_provider == "openai-codex" then return {} end
  local p = ctx.providers[ctx.selected_provider]
  local eps = (type(p) == "table" and p.endpoints) or {}
  local ep = eps["openai-responses"] or eps.openai
  if type(ep) ~= "table" or (ep.wire_api and ep.wire_api ~= "openai-responses") then return {} end
  return { model_providers = { [ctx.selected_provider] = { base_url = ctx.via_url } } }
end)
`

func gateAgents(t *testing.T) []*packload.Pack {
	t.Helper()
	m, problems := packdecl.Decode([]byte(gateManifest))
	if len(problems) != 0 {
		t.Fatalf("fixture manifest invalid: %v", problems)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "derive.lua"), []byte(gateDerive), 0o644); err != nil {
		t.Fatal(err)
	}
	return []*packload.Pack{{Name: "agents", Root: root, Decl: m}}
}

const gateBase = "http://127.0.0.1:8216"

// gateResolved names each via profile after its provider.
func gateResolved() map[string]packload.ResolvedProfile {
	out := map[string]packload.ResolvedProfile{
		"nobase": {Provider: "anthro", Via: ServiceName},
		"native": {Provider: "anthro"},
		"other":  {Provider: "zai", Via: "other-service", ViaBase: gateBase},
	}
	for _, p := range []string{"zai", "resp", "both", "anthro", "openai-codex"} {
		out["v-"+p] = packload.ResolvedProfile{Provider: p, Via: ServiceName, ViaBase: gateBase}
	}
	return out
}

func gate(t *testing.T, use map[string]string) ([]error, []string) {
	t.Helper()
	return ViaRouteGate(gateAgents(t), mustProviders(t, gateProviders), use, gateResolved())
}

func wantAll(t *testing.T, what, msg string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(msg, w) {
			t.Errorf("%s lacks %q:\n%s", what, w, msg)
		}
	}
}

func wantNone(t *testing.T, what, msg string, unwanted ...string) {
	t.Helper()
	for _, w := range unwanted {
		if strings.Contains(msg, w) {
			t.Errorf("%s claims %q:\n%s", what, w, msg)
		}
	}
}

// TestViaRouteGateRefusesARepointedAgentWithNoRoute: a re-pointed agent whose provider offers
// neither wire, or is the subscription a via route never carries, leaves its prefix unserved,
// so the launch refuses, naming the profile, the agent, the daemon's reason, the URL and the
// remedy. With no via route served at all the daemon binds no via listener, so the refusal
// says the connection is refused, not that a 404 comes back.
func TestViaRouteGateRefusesARepointedAgentWithNoRoute(t *testing.T) {
	refusals, notices := gate(t, map[string]string{"chatty": "v-anthro"})
	if len(notices) != 0 || len(refusals) != 1 {
		t.Fatalf("refusals %v notices %v, want one refusal", refusals, notices)
	}
	msg := refusals[0].Error()
	wantAll(t, "refusal", msg, `profile "v-anthro" (active for chatty)`,
		"chatty's config points it at its via URL, http://127.0.0.1:8216/agent/chatty",
		"declares no chat-completions or Responses endpoint",
		"binds no via listener", "refused a connection", `remove "via" from profile "v-anthro"`)
	wantNone(t, "refusal with no via listener", msg, "404")

	refusals, notices = gate(t, map[string]string{"chatty": "v-openai-codex"})
	if len(notices) != 0 || len(refusals) != 1 {
		t.Fatalf("refusals %v notices %v, want one refusal for a re-pointed subscription", refusals, notices)
	}
	wantAll(t, "subscription refusal", refusals[0].Error(), "ChatGPT subscription",
		"http://127.0.0.1:8216/agent/chatty")
}

// TestViaRouteGateSaysA404WhenTheListenerIsUp: when another agent's via route is served, the
// unserved agent's URL is on a live listener, and the refusal names the 404 it would get.
func TestViaRouteGateSaysA404WhenTheListenerIsUp(t *testing.T) {
	refusals, _ := gate(t, map[string]string{"chatty": "v-anthro", "resper": "v-resp"})
	if len(refusals) != 1 {
		t.Fatalf("refusals %v, want one for chatty", refusals)
	}
	wantAll(t, "refusal beside a served route", refusals[0].Error(), "(active for chatty)",
		"via listener", "with a 404")
	wantNone(t, "refusal beside a served route", refusals[0].Error(), "refused a connection")
}

// TestViaRouteGateDisclosesAViaThatRepointsNothing (WG-I15): resper's derive keeps
// openai-codex on its own client and writes no row for a provider it cannot reach, so a via
// on either changes nothing it sends. The launch discloses that and refuses nothing — and
// claims neither a 404 nor a missing endpoint for a URL resper never uses.
func TestViaRouteGateDisclosesAViaThatRepointsNothing(t *testing.T) {
	refusals, notices := gate(t, map[string]string{"resper": "v-openai-codex"})
	if len(refusals) != 0 || len(notices) != 1 {
		t.Fatalf("refusals %v notices %v, want one no-effect notice", refusals, notices)
	}
	wantAll(t, "subscription notice", notices[0], `profile "v-openai-codex" (active for resper)`,
		"does not point it at its via URL, http://127.0.0.1:8216/agent/resper",
		"has no effect on resper", "ChatGPT subscription")
	wantNone(t, "subscription notice", notices[0], "404")

	refusals, notices = gate(t, map[string]string{"resper": "v-zai"})
	if len(refusals) != 0 || len(notices) != 1 {
		t.Fatalf("refusals %v notices %v, want one no-effect notice", refusals, notices)
	}
	wantAll(t, "chat-only notice", notices[0], "has no effect on resper")
	wantNone(t, "chat-only notice", notices[0], "404", "declares no Responses endpoint")
}

// TestViaRouteGateDisclosesADeriveItCouldNotRun: when the agent's derive fails, whether the
// via re-points it is unknown, so the gate discloses that and refuses nothing — the boot runs
// the same derive and refuses the launch over the failure itself.
func TestViaRouteGateDisclosesADeriveItCouldNotRun(t *testing.T) {
	refusals, notices := gate(t, map[string]string{"broken": "v-anthro"})
	if len(refusals) != 0 || len(notices) != 1 {
		t.Fatalf("refusals %v notices %v, want one could-not-check notice", refusals, notices)
	}
	wantAll(t, "derive-failure notice", notices[0], `profile "v-anthro" (active for broken)`,
		"could not be checked", "broken derive")
}

// TestViaRouteGateDisclosesARouteWithoutTheAgentsWire: chatty is re-pointed at a route that
// serves only Responses, so the launch warns, naming the endpoint the provider lacks and the
// preference it read.
func TestViaRouteGateDisclosesARouteWithoutTheAgentsWire(t *testing.T) {
	refusals, notices := gate(t, map[string]string{"chatty": "v-resp"})
	if len(refusals) != 0 || len(notices) != 1 {
		t.Fatalf("refusals %v notices %v, want one wire notice", refusals, notices)
	}
	wantAll(t, "wire notice", notices[0], `profile "v-resp" (active for chatty)`,
		"provider resp declares no chat-completions endpoint", "http://127.0.0.1:8216/agent/chatty",
		`first declared protocol is "openai"`)
}

// TestViaRouteGateSaysNothingWhereNothingIsWrong: a route with the agent's wire, a profile
// with no via, an agent that is not a via agent (anth prefers anthropic, bare declares no
// protocols) even though both derives re-point, an agent no pack installs (WG-I10), a via
// naming a service other than this one, whose routes are not this daemon's to judge, and an
// agent whose via URL is empty — its service is absent, or the table is the host notch's
// inert one (WG-I12).
func TestViaRouteGateSaysNothingWhereNothingIsWrong(t *testing.T) {
	for name, use := range map[string]map[string]string{
		"chatty on chat":       {"chatty": "v-zai"},
		"resper on Responses":  {"resper": "v-resp"},
		"both wires":           {"chatty": "v-both", "resper": "v-both"},
		"no via":               {"chatty": "native"},
		"prefers anthropic":    {"anth": "v-anthro"},
		"declares no protocol": {"bare": "v-anthro"},
		"not installed":        {"omp": "v-anthro"},
		"another service":      {"resper": "other"},
		"no address":           {"chatty": "nobase"},
	} {
		if refusals, notices := gate(t, use); len(refusals)+len(notices) != 0 {
			t.Errorf("%s: refusals %v notices %v", name, refusals, notices)
		}
	}
	inert, _ := packload.ViaServedAt(gateResolved(), nil, packload.NothingServed())
	refusals, notices := ViaRouteGate(gateAgents(t), mustProviders(t, gateProviders),
		map[string]string{"chatty": "v-anthro"}, inert)
	if len(refusals)+len(notices) != 0 {
		t.Errorf("an inert table refused %v / disclosed %v", refusals, notices)
	}
}

// TestViaRouteGateAgreesWithTheDaemonsPlan: a re-pointed agent is refused exactly when the
// daemon's own plan, over the whole table, serves no route for it — the property that makes
// the refusal the daemon's decision rather than a copy of it.
func TestViaRouteGateAgreesWithTheDaemonsPlan(t *testing.T) {
	providers := mustProviders(t, gateProviders)
	use := map[string]string{"chatty": "v-anthro", "resper": "v-resp"}
	served := map[string]bool{}
	for _, r := range viaRoutesFor(providers, use, gateResolved()).Routes {
		served[r.Agent] = true
	}
	refusals, _ := ViaRouteGate(gateAgents(t), providers, use, gateResolved())
	refused := map[string]bool{}
	for _, err := range refusals {
		for agent := range use {
			if strings.Contains(err.Error(), "(active for "+agent+")") {
				refused[agent] = true
			}
		}
	}
	for agent := range use {
		if served[agent] == refused[agent] {
			t.Errorf("%s: served %v, refused %v — exactly one must hold", agent, served[agent], refused[agent])
		}
	}
}

// shippedGate runs the gate over the shipped packs and the providers they compose, with one
// via profile "v" over provider, active for each agent in agents.
func shippedGate(t *testing.T, provider string, agents ...string) ([]error, []string) {
	t.Helper()
	packs := packload.Embedded()
	providers, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatalf("composing the shipped providers: %v", err)
	}
	use := map[string]string{}
	for _, a := range agents {
		use[a] = "v"
	}
	resolved := map[string]packload.ResolvedProfile{
		"v": {Provider: provider, Via: ServiceName, ViaBase: gateBase},
	}
	return ViaRouteGate(packs, providers, use, resolved)
}

// TestShippedSubscriptionViaRepointsNoAgent: a via over openai-codex. pi, oh-omp, codex and
// opencode each implement the subscription natively (opencode through its own `openai` client
// and ChatGPT login), so no derive re-points it: the via has no effect on any of them, every
// launch starts, and each is told so. opencode was the one refused until its derive stopped
// cataloguing openai-codex (docs/design/pi-codex-provider-shadowing.md OQ-2), because a via row
// sent it to a prefix nothing serves.
func TestShippedSubscriptionViaRepointsNoAgent(t *testing.T) {
	refusals, notices := shippedGate(t, "openai-codex", "pi", "oh-omp", "codex", "opencode")
	if len(refusals) != 0 {
		t.Fatalf("refusals %v, want none: every agent here keeps its own subscription client", refusals)
	}
	joined := strings.Join(notices, "\n")
	for _, agent := range []string{"pi", "oh-omp", "codex", "opencode"} {
		wantAll(t, agent+" notice", joined, "(active for "+agent+")", "has no effect on "+agent)
	}
}

// TestShippedClaudeOnABridgedBedrockProfileRidesTheAdapterRoute: claude prefers `anthropic`, which
// no via route carries, so a profile over `bedrock` with a via (the everything profile's shape,
// OQ-BR11) reaches claude through the adapter route instead: the shipped `bedrock` names a region
// and no address, the wire bridge's chat-completions adapter fronts its platform (from_platforms),
// and the anthropic address it composes is claude's under the via (WG-I39). The daemon, booted from
// the same tables, serves that route, so nothing is disclosed. Before the region-composed upstream
// no route carried claude and the launch disclosed it (PP-D4), which the next test keeps for a
// provider the bridge still cannot carry.
func TestShippedClaudeOnABridgedBedrockProfileRidesTheAdapterRoute(t *testing.T) {
	refusals, notices := shippedGate(t, "bedrock", "claude")
	if len(refusals) != 0 || len(notices) != 0 {
		t.Fatalf("refusals %v notices %v, want neither: the adapter route carries claude", refusals, notices)
	}
}

// TestShippedClaudeOnAViaNoRouteCarriesIsDisclosed: a via over a provider whose anthropic address is
// its own carries none of claude's requests, and the launch says so (PP-D4), naming claude's own
// Bedrock switch when the provider is Bedrock, and never refusing.
func TestShippedClaudeOnAViaNoRouteCarriesIsDisclosed(t *testing.T) {
	packs := packload.Embedded()
	providers, err := packload.ComposeProviders(mustProviders(t, `{"corp-bedrock":{"platform":"aws-bedrock",
		"endpoints":{"anthropic":{"base_url":"https://bedrock-gw.corp.example/anthropic"}}}}`), packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved := map[string]packload.ResolvedProfile{"v": {Provider: "corp-bedrock", Via: ServiceName, ViaBase: gateBase}}
	refusals, notices := ViaRouteGate(packs, providers, map[string]string{"claude": "v"}, resolved)
	if len(refusals) != 0 || len(notices) != 1 {
		t.Fatalf("refusals %v notices %v, want one notice", refusals, notices)
	}
	wantAll(t, "claude notice", notices[0], `profile "v" (active for claude)`,
		"no via route carries claude", `it speaks "anthropic"`,
		"does not point it at its via URL, http://127.0.0.1:8216/agent/claude",
		"the via sends none of claude's requests through wire-bridge",
		`claude reaches a provider of platform "aws-bedrock", such as "corp-bedrock", only through its own client`,
		"CLAUDE_CODE_USE_BEDROCK in claude/settings", `Remove "via" from profile "v"`)
	// On a provider of no platform the notice names no switch.
	_, notices = shippedGate(t, "zai", "claude")
	if len(notices) != 1 || strings.Contains(notices[0], "switches on") {
		t.Errorf("claude on a via over zai: one notice naming no switch, got %v", notices)
	}
}

// TestShippedAgentsOutsideTheViaCheck: agy declares no protocols and nothing in its pack reads
// ctx.via_url, so a via on its profile is neither refused nor disclosed.
func TestShippedAgentsOutsideTheViaCheck(t *testing.T) {
	if refusals, notices := shippedGate(t, "zai", "agy"); len(refusals)+len(notices) != 0 {
		t.Errorf("agy: refusals %v notices %v, want neither", refusals, notices)
	}
}

// TestShippedCodexOnAChatOnlyProviderIsNotRepointed: codex's derive writes no row for zai,
// which offers only chat-completions, with or without via, so the gate discloses that the
// via has no effect and claims no 404 at a URL codex never uses.
func TestShippedCodexOnAChatOnlyProviderIsNotRepointed(t *testing.T) {
	refusals, notices := shippedGate(t, "zai", "codex")
	if len(refusals) != 0 || len(notices) != 1 {
		t.Fatalf("refusals %v notices %v, want one no-effect notice", refusals, notices)
	}
	wantAll(t, "codex notice", notices[0], "has no effect on codex")
	wantNone(t, "codex notice", notices[0], "404", "declares no Responses endpoint")
}

// TestShippedViaOverABuiltInProviderRepointsNothing: a via over a provider the agent has built in
// (docs/design/pi-codex-provider-shadowing.md OQ-3) gets no via row, so the via has no effect, and
// the notice says which rule that is and the one way to route it anyway. It was a wire notice for
// pi on openrouter until the ruling, when pi's derive still re-pointed its openrouter row. opencode
// names its own provider for yolo's zai, the coding plan's.
func TestShippedViaOverABuiltInProviderRepointsNothing(t *testing.T) {
	for _, tc := range []struct{ provider, agent, own string }{
		{"openrouter", "pi", `"openrouter" is one of pi's own providers`},
		{"cerebras", "oh-omp", `"cerebras" is one of oh-omp's own providers`},
		{"zai", "opencode", `"zai" is opencode's own "zai-coding-plan" provider`},
	} {
		t.Run(tc.agent+"/"+tc.provider, func(t *testing.T) {
			refusals, notices := shippedGate(t, tc.provider, tc.agent)
			if len(refusals) != 0 || len(notices) != 1 {
				t.Fatalf("refusals %v notices %v, want one no-effect notice", refusals, notices)
			}
			wantAll(t, tc.agent+" notice", notices[0], "has no effect on "+tc.agent, tc.own,
				"yolo writes no model entry over a provider an agent has built in",
				"declare the provider under `providers` with a name "+tc.agent+" has no provider of")
			wantNone(t, tc.agent+" notice", notices[0], "declares no chat-completions endpoint", "404")
		})
	}
}

// TestShippedViaRowsSpeakTheWireTheAgentPrefers ties WG-I14's preference to the derives it
// stands in for. Each wired agent's real derive runs with ctx.via_url set, over a provider
// offering both wires; the row it points at the via URL names a wire in the agent's own
// vocabulary (pi's and oh-omp's `api`, codex's `wire_api`, the `npm` SDK opencode loads), and
// that wire must be the one preferredViaWire reads off the manifest. claude and copilot,
// whose derives read no via URL, are not via agents. A reordered `protocols` list, or a
// derive that changes the wire its via row speaks, fails here.
func TestShippedViaRowsSpeakTheWireTheAgentPrefers(t *testing.T) {
	packs := packload.Embedded()
	providers := mustProviders(t, gateProviders)
	resolved := map[string]packload.ResolvedProfile{
		"v": {Provider: "both", Via: ServiceName, ViaBase: gateBase},
	}
	vocab := map[string]string{
		"openai-completions":        wireAPIChatCompletions, // pi, oh-omp: api
		"responses":                 wireAPIResponses,       // codex: wire_api
		"@ai-sdk/openai-compatible": wireAPIChatCompletions, // opencode: npm
	}
	for _, agent := range []string{"pi", "oh-omp", "opencode", "codex"} {
		ptrs, err := packload.DerivedViaPointers(packs, providers, map[string]string{agent: "v"}, resolved, agent)
		if err != nil {
			t.Fatalf("%s: %v", agent, err)
		}
		if len(ptrs) == 0 {
			t.Errorf("%s's derive points nothing at its via URL over a provider offering both wires", agent)
			continue
		}
		written := ""
		for _, ptr := range ptrs {
			for depth := len(ptr.Path) - 1; depth >= 0 && written == ""; depth-- {
				row := valueAt(ptr.Layer, ptr.Path[:depth])
				for _, key := range []string{"api", "wire_api", "npm"} {
					if s, ok := row[key].(string); ok && vocab[s] != "" {
						written = vocab[s]
					}
				}
			}
		}
		wire, protocol, via := preferredViaWire(packs, agent)
		if !via || written == "" || wire != written {
			t.Errorf("%s: its via row speaks %q, its first declared protocol %q names %q (via agent %v)",
				agent, written, protocol, wire, via)
		}
	}
	for _, agent := range []string{"claude", "copilot"} {
		if _, protocol, via := preferredViaWire(packs, agent); via {
			t.Errorf("%s prefers %q, which makes it a via agent — its derive reads no via URL", agent, protocol)
		}
	}
}

// valueAt returns the object at path inside layer, or nil.
func valueAt(layer map[string]any, path []string) map[string]any {
	cur := layer
	for _, k := range path {
		next, ok := cur[k].(map[string]any)
		if !ok {
			return nil
		}
		cur = next
	}
	return cur
}

// TestTheShippedBedrockBridgeProfileMeetsEachAgentAsItCan pins the shipped `bedrock-bridge`
// profile (docs/design/bedrock-plumbing.md OQ-BR1, ruled 2026-09-29: one profile forces the wire
// bridge) through the profile resolution a launch runs, then the launch's via gate and the
// daemon's own plan over the same tables. The profile ships from packs/bedrock as
// {provider: bedrock, via: wire-bridge}, and the provider names a region and no address, so
// the bridge composes runtime's URL from the region (docs/design/wire-bridge-gateway.md §8 step
// 1, WG-I39). Every agent is carried: codex, pi, oh-omp and opencode on their via routes, whose
// one upstream is runtime's own OpenAI-compatible base, and claude on the adapter route, whose
// anthropic address the adapter composed for the via. None is refused, none is disclosed, and
// none quietly runs its own Bedrock client instead: the profile asked for the bridge. Before the
// region-composed upstream, the four were refused and claude was disclosed.
func TestTheShippedBedrockBridgeProfileMeetsEachAgentAsItCan(t *testing.T) {
	packs := packload.Embedded()
	providers, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatalf("composing the shipped providers: %v", err)
	}
	resolved, err := packload.ResolveProfiles(packs, nil, providers)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := resolved["bedrock-bridge"]
	if !ok || r.Provider != "bedrock" || r.Via != ServiceName || r.ViaBase == "" {
		t.Fatalf("the shipped bedrock-bridge profile resolves to %+v, want provider bedrock via %s", r, ServiceName)
	}
	use := map[string]string{}
	for _, a := range []string{"claude", "codex", "pi", "oh-omp", "opencode"} {
		use[a] = "bedrock-bridge"
	}
	refusals, notices := ViaRouteGate(packs, providers, use, resolved)
	if len(refusals) != 0 || len(notices) != 0 {
		t.Errorf("refusals %v notices %v, want neither: the bridge carries every agent", refusals, notices)
	}
	plan := planFor(providers, use, resolved)
	if plan.adapter == nil || plan.adapter.ProviderName != "bedrock" || !plan.adapter.RegionalUpstream ||
		!plan.adapter.RegionFromEnv || plan.adapter.UpstreamBaseURL != "" {
		t.Errorf("adapter route %+v (idle: %s), want bedrock's, region-composed at boot from the served agent's region",
			plan.adapter, plan.adapterWhy)
	}
	served := map[string]bool{}
	for _, r := range plan.via.Routes {
		served[r.Agent] = true
		if !r.Chat.Regional || r.Chat != r.Responses || !r.Chat.RegionFromEnv {
			t.Errorf("%s's via route %+v, want one region-composed upstream for both wires", r.Agent, r)
		}
	}
	for _, agent := range []string{"codex", "pi", "oh-omp", "opencode"} {
		if !served[agent] {
			t.Errorf("no via route for %s (skipped: %v)", agent, plan.via.Skipped)
		}
	}
}
