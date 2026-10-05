package wirebridged

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// The wire bridge on plain `-p bedrock` for the agents with no Bedrock client of their own
// (docs/design/bedrock-plumbing.md OQ-BR1, ruled 2026-09-29: "through its own Bedrock client
// where it has one, and through the wire bridge where it has none"; the carrier,
// docs/design/wire-bridge-gateway.md WG-I44). claude, codex, opencode and pi keep their own
// clients there, and copilot and oh-omp, which have none, are carried by the bridge exactly as
// `bedrock-bridge` carries them: copilot on the adapter route, oh-omp on its via route. The
// tables cross as the launcher writes them (ProfilesWireTable's YOLO_PROFILES, decoded by the
// entrypoint's LoadProfiles), so the daemon's boot is measured against what the host emits.

// plainBedrockEnv is the boot environment of a launch of every shipped pack with use as its
// profile table: the composed providers, the launcher's YOLO_PROFILES and YOLO_USE_PROFILES.
func plainBedrockEnv(t *testing.T, use map[string]string) (*jsonx.OrderedMap, map[string]packload.ResolvedProfile, map[string]string) {
	t.Helper()
	return plainBedrockEnvWith(t, "", use)
}

// plainBedrockEnvWith is plainBedrockEnv over a user `providers` layer.
func plainBedrockEnvWith(t *testing.T, user string, use map[string]string) (*jsonx.OrderedMap, map[string]packload.ResolvedProfile, map[string]string) {
	t.Helper()
	providers, resolved := shippedBridgeTables(t, user)
	provJSON, err := jsonx.DumpsCompact(providers)
	if err != nil {
		t.Fatal(err)
	}
	profJSON, err := jsonx.DumpsCompact(packload.ProfilesWireTable(resolved))
	if err != nil {
		t.Fatal(err)
	}
	useMap := jsonx.NewOrderedMap()
	for agent, profile := range use {
		useMap.Set(agent, profile)
	}
	useJSON, err := jsonx.DumpsCompact(useMap)
	if err != nil {
		t.Fatal(err)
	}
	e := routeEnv(provJSON, profJSON, useJSON)
	return e.LoadProviders(), e.LoadProfiles(), bootUseProfiles(e)
}

// everyAgentOnPlainBedrock is every shipped agent the bridge or its own client can put on Bedrock.
var everyAgentOnPlainBedrock = map[string]string{"claude": "bedrock", "codex": "bedrock", "opencode": "bedrock",
	"pi": "bedrock", "copilot": "bedrock", "oh-omp": "bedrock"}

// TestPlainBedrockCarriesOnlyTheAgentsWithNoClientOfTheirOwn is the serve decision over the
// crossed tables: the adapter route serves copilot (claude, first in agent order, keeps its own
// client), the via listener serves oh-omp alone, both on runtime's URL composed from the served
// agent's region, the launcher's WillServe agrees, and the launch's via gate has nothing to say.
// It fails if the profile table stops carrying which agents the bridge carries, or the daemon
// stops reading it.
func TestPlainBedrockCarriesOnlyTheAgentsWithNoClientOfTheirOwn(t *testing.T) {
	providers, resolved, use := plainBedrockEnv(t, everyAgentOnPlainBedrock)
	p := planFor(providers, use, resolved)
	if p.adapter == nil || p.adapter.Agent != "copilot" || p.adapter.ProviderName != "bedrock" ||
		!p.adapter.RegionalUpstream || !p.adapter.RegionFromEnv {
		t.Fatalf("adapter route %+v (idle: %s), want copilot's, on bedrock's region-composed upstream",
			p.adapter, p.adapterWhy)
	}
	var via []string
	for _, r := range p.via.Routes {
		via = append(via, r.Agent)
		if !r.Chat.Regional || r.Chat != r.Responses {
			t.Errorf("%s's via route %+v, want one region-composed upstream for both wires", r.Agent, r)
		}
	}
	// copilot's via route is planned as under bedrock-bridge, where every agent of a via profile
	// gets one; it rides the adapter route, and no request of its goes there.
	if strings.Join(via, ",") != "copilot,oh-omp" {
		t.Errorf("via routes for %v, want copilot and oh-omp alone: claude, codex, opencode and pi use "+
			"their own Bedrock clients on -p bedrock (skipped: %v)", via, p.via.Skipped)
	}
	if !WillServe(providers, selecting(use), resolved) {
		t.Error("the launcher's WillServe says the bridge idles, so the launch would register no witness for it")
	}
	refusals, notices := ViaRouteGate(packload.Embedded(), providers, use, resolved)
	if len(refusals) != 0 || len(notices) != 0 {
		t.Errorf("refusals %v notices %v, want neither: the bridge carries both agents", refusals, notices)
	}
	// The agents with their own clients alone: nothing is carried, and the bridge idles as before.
	native := map[string]string{"claude": "bedrock", "codex": "bedrock", "opencode": "bedrock", "pi": "bedrock"}
	if providers, resolved, use := plainBedrockEnv(t, native); WillServe(providers, selecting(use), resolved) {
		t.Error("the bridge serves -p bedrock for agents that all have their own Bedrock clients")
	}
}

// TestPlainBedrockReachesRuntimeForCopilotAndOmp sends one request of each carried agent through
// the booted plan: copilot's Messages request goes to runtime's chat-completions in copilot's
// region, its starting model too (copilot's open-weight default, MM-D34), its Claude model to
// runtime's Messages route, untranslated, as under bedrock-bridge, where a list names that model
// Anthropic's (anthropicOnTheList), and oh-omp's chat-completions to runtime in its own, each
// signed with that agent's own pair.
func TestPlainBedrockReachesRuntimeForCopilotAndOmp(t *testing.T) {
	clearAWS(t)
	logs := captureDiag(t)
	up := withUpstream(t)
	providers, resolved, use := plainBedrockEnvWith(t, anthropicOnTheList, everyAgentOnPlainBedrock)
	p := planFor(providers, use, resolved)
	if p.adapter == nil {
		t.Fatalf("no adapter route on -p bedrock: %s", p.adapterWhy)
	}
	home := t.TempDir()
	writeAgentKey(t, home, "copilot", awsPair("AKIDCOPILOT", "eu-west-1"))
	writeAgentKey(t, home, "oh-omp", awsPair("AKIDOMP", "ap-southeast-2"))
	adapter, via := freeLoopback(t), freeLoopback(t)
	p.adapter.ListenAddr, p.via.ListenAddr = adapter, via
	startPlan(t, p, home)
	for _, tc := range []struct{ url, body, wantURL, wantAKID string }{
		{"http://" + adapter + "/v1/messages",
			`{"model":"us.openai.gpt-6.1-sol","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`,
			"https://bedrock-runtime.eu-west-1.amazonaws.com/openai/v1/chat/completions", "AKIDCOPILOT"},
		{"http://" + adapter + "/v1/messages",
			`{"model":"openai.gpt-oss-120b-1:0","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`,
			"https://bedrock-runtime.eu-west-1.amazonaws.com/openai/v1/chat/completions", "AKIDCOPILOT"},
		{"http://" + adapter + "/v1/messages",
			`{"model":"global.anthropic.claude-opus-5-5","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`,
			"https://bedrock-runtime.eu-west-1.amazonaws.com/anthropic/v1/messages", "AKIDCOPILOT"},
		{"http://" + via + "/agent/oh-omp/chat/completions", `{"model":"us.openai.gpt-6.1-sol","messages":[]}`,
			"https://bedrock-runtime.ap-southeast-2.amazonaws.com/openai/v1/chat/completions", "AKIDOMP"},
	} {
		before := up.calls()
		resp, body := postTo(t, tc.url, tc.body, nil)
		if resp.StatusCode != 200 || up.calls() != before+1 {
			t.Errorf("%s: status %d, upstream calls %d: %s", tc.url, resp.StatusCode, up.calls()-before, body)
			continue
		}
		sent := up.requests[before]
		if sent.URL.String() != tc.wantURL {
			t.Errorf("%s went to %s, want %s", tc.url, sent.URL, tc.wantURL)
		}
		if auth := sent.Header.Get("Authorization"); !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential="+tc.wantAKID+"/") {
			t.Errorf("%s: Authorization %q, want %s's own pair", tc.url, auth, tc.wantAKID)
		}
	}
	wantLines(t, logs(), "SigV4 for bedrock in eu-west-1 ($AWS_REGION from ")
}

// TestANarrowedBedrockListGovernsTheCarriedAgents: the allowlist (WG-I40) follows each carried
// agent as it would a via profile's. oh-omp's via route refuses a model a company `only` dropped
// from the Bedrock list, and the adapter route copilot alone shares counts copilot among its
// sharers, which, sending models off any list, has an off-list model logged rather than refused.
func TestANarrowedBedrockListGovernsTheCarriedAgents(t *testing.T) {
	b := bootNarrowed(t, []string{"copilot", "omp", "bedrock", "aws-auth", "wire-bridge"},
		`{"kind":"models","provider":"bedrock","only":["us.openai.gpt-6.1-sol"]}`, nil,
		map[string]string{"copilot": "bedrock", "oh-omp": "bedrock"},
		map[string]string{"copilot": awsPair("AKIDCOPILOT", "us-east-1"), "oh-omp": awsPair("AKIDOMP", "us-east-1")}, true)
	if b.adapter == "" || b.via == "" {
		t.Fatalf("the boot served adapter %q and via %q, want both on -p bedrock", b.adapter, b.via)
	}
	resp, body := postTo(t, "http://"+b.via+"/agent/oh-omp/chat/completions",
		`{"model":"global.openai.gpt-6-astra","messages":[]}`, nil)
	if resp.StatusCode != 400 || b.up.calls() != 0 {
		t.Errorf("oh-omp's off-list model: %d, upstream calls %d, want a 400 and nothing sent: %s",
			resp.StatusCode, b.up.calls(), body)
	}
	if resp, body := postTo(t, "http://"+b.adapter+"/v1/messages",
		`{"model":"global.openai.gpt-6-astra","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`,
		nil); resp.StatusCode != 200 {
		t.Errorf("copilot's off-list model on the adapter route: %d %s, want it passed and logged", resp.StatusCode, body)
	}
	adapterLine := ""
	for _, line := range strings.Split(b.logs(), "\n") {
		if strings.Contains(line, `provider "bedrock": anthropic on `) {
			adapterLine = line
		}
	}
	if !strings.Contains(adapterLine, "copilot sends models off it, so an off-list model is logged, not refused") {
		t.Errorf("the adapter route's serve line %q does not name the list copilot shares. Full log:\n%s",
			adapterLine, b.logs())
	}
}

// TestACarriedAgentWhoseAdapterRouteAnotherProviderHoldsIsRefused: the daemon serves one adapter
// route, the first candidate in agent order (routeFor), and claude sorts before copilot. So beside
// claude on a provider the adapter fronts at the same address (cerebras), copilot carried on
// `-p bedrock` would send its requests to cerebras with claude's key, and beside claude on the
// Codex subscription, whose route listens on another port, to nothing at all. Before the carrier
// copilot reached nothing there and kept its own login; the launch now refuses, naming both
// agents and both providers (adapterTakenRefusal). The same holds for copilot under the
// bridge-forcing profile, and nothing is refused where the route claude takes is copilot's own
// provider.
func TestACarriedAgentWhoseAdapterRouteAnotherProviderHoldsIsRefused(t *testing.T) {
	for _, tc := range []struct {
		use  map[string]string
		want []string
	}{
		{map[string]string{"claude": "cerebras", "copilot": "bedrock"}, []string{
			`profile "bedrock" (active for copilot)`,
			`it has no client of provider "bedrock"'s platform, so wire-bridge carries it`,
			"the bridge's adapter address for provider bedrock, 127.0.0.1:8214",
			`this launch gives it to claude on provider cerebras (profile "cerebras")`,
			"would reach provider cerebras, with the credential that reaches claude, and not provider bedrock",
			"another profile for copilot (`-p copilot=<name>`)"}},
		{map[string]string{"claude": "codex", "copilot": "bedrock"}, []string{
			`this launch gives it to claude on provider openai-codex (profile "codex")`,
			"which listens at 127.0.0.1:8215, so nothing listens at 127.0.0.1:8214"}},
		{map[string]string{"claude": "cerebras", "copilot": "bedrock-bridge"}, []string{
			`profile "bedrock-bridge" (active for copilot) routes copilot through wire-bridge (via: "wire-bridge")`,
			"would reach provider cerebras"}},
	} {
		providers, resolved, use := plainBedrockEnv(t, tc.use)
		refusals, _ := ViaRouteGate(packload.Embedded(), providers, use, resolved)
		if len(refusals) != 1 {
			t.Errorf("%v: refusals %v, want one, for copilot", tc.use, refusals)
			continue
		}
		for _, want := range tc.want {
			if !strings.Contains(refusals[0].Error(), want) {
				t.Errorf("%v: the refusal must say %q:\n%v", tc.use, want, refusals[0])
			}
		}
	}
	for _, use := range []map[string]string{
		{"claude": "bedrock", "copilot": "bedrock"},
		{"claude": "bedrock-bridge", "copilot": "bedrock"},
		{"claude": "cerebras", "copilot": "cerebras"},
	} {
		providers, resolved, use := plainBedrockEnv(t, use)
		if refusals, _ := ViaRouteGate(packload.Embedded(), providers, use, resolved); len(refusals) != 0 {
			t.Errorf("%v: refused %v, though the adapter route is copilot's own provider's", use, refusals)
		}
	}
}

// TestACarriedAgentTheAdapterCannotServeIsToldToSelectAnotherProfile: copilot carried on a Bedrock
// provider whose region the bridge composes no host from is served no adapter route, so its
// requests go nowhere, and the notice says so with a carried agent's remedy, another profile:
// the profile names no via to remove (unroutedViaNotice's carried branch).
func TestACarriedAgentTheAdapterCannotServeIsToldToSelectAnotherProfile(t *testing.T) {
	providers, resolved := shippedBridgeTables(t, `{"bedrock": {"region": "us-central1"}}`)
	use := map[string]string{"copilot": "bedrock"}
	refusals, notices := ViaRouteGate(packload.Embedded(), providers, use, resolved)
	if len(refusals) != 0 || len(notices) != 1 {
		t.Fatalf("refusals %v notices %v, want one notice for copilot", refusals, notices)
	}
	for _, want := range []string{`profile "bedrock" (active for copilot)`,
		`it has no client of provider "bedrock"'s platform, so wire-bridge carries it`,
		"Select another profile for copilot (`-p copilot=<name>`)"} {
		if !strings.Contains(notices[0], want) {
			t.Errorf("the notice must say %q:\n%s", want, notices[0])
		}
	}
	if strings.Contains(notices[0], `Remove "via"`) {
		t.Errorf("the notice offers to remove a via the profile does not name:\n%s", notices[0])
	}
}
