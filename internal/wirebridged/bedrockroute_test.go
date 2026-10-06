package wirebridged

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/sigv4"
)

// The region-composed Bedrock upstream and the platform-keyed signer
// (docs/design/wire-bridge-gateway.md §8 step 1, WG-I37 to WG-I39), through the PRODUCTION boot:
// the shipped packs composed as a launch composes them, the daemon's own plan over that table,
// and servePlan reading each served agent's key channel. No request leaves the process
// (captureUpstream answers every one).

// shippedBridgeTables composes every shipped pack, as TestTheShippedBedrockBridgeProfile… does,
// with an optional user `providers` layer, and resolves the shipped profiles over it.
func shippedBridgeTables(t *testing.T, user string) (*jsonx.OrderedMap, map[string]packload.ResolvedProfile) {
	t.Helper()
	packs := packload.Embedded()
	var layer *jsonx.OrderedMap
	if user != "" {
		layer = mustProviders(t, user)
	}
	providers, err := packload.ComposeProviders(layer, packs)
	if err != nil {
		t.Fatalf("composing the shipped providers: %v", err)
	}
	resolved, err := packload.ResolveProfiles(packs, nil, providers)
	if err != nil {
		t.Fatal(err)
	}
	return providers, resolved
}

// awsPair is a static key pair in the launcher's frozen write format, for one agent's env file,
// with the region line when region is set.
func awsPair(akid, region string) string {
	lines := "export AWS_ACCESS_KEY_ID=${AWS_ACCESS_KEY_ID:-'" + akid + "'}\n" +
		"export AWS_SECRET_ACCESS_KEY=${AWS_SECRET_ACCESS_KEY:-'secret-" + akid + "'}"
	if region != "" {
		lines += "\nexport AWS_REGION=${AWS_REGION:-'" + region + "'}"
	}
	return lines
}

// clearAWS empties every variable the chain and the region read in this process, so only the
// key channel answers.
func clearAWS(t *testing.T) {
	t.Helper()
	for _, v := range append(append([]string{}, sigv4.EnvVars...), sigv4.RegionVars...) {
		t.Setenv(v, "")
	}
}

// anthropicOnTheList is a user layer giving the shipped `bedrock` a one-entry list naming Claude
// Opus 5.5's maker. packs/bedrock ships no list (docs/design/model-lists-and-pickers.md MM-D32), and
// the bridge passes a model untranslated only when the provider's list names it Anthropic's, so a
// test of that route supplies the list, as a user or a company pack would.
const anthropicOnTheList = `{"bedrock": {"models": {"global.anthropic.claude-opus-5-5":
  {"id": "global.anthropic.claude-opus-5-5", "vendor": "anthropic"}}}}`

// servedShippedBedrockBridge boots the daemon's plan for claude, pi and codex on the shipped
// `bedrock-bridge`, each agent's env file holding its own pair and region, on free loopback ports,
// with Claude Opus 5.5 on the provider's list as Anthropic's (anthropicOnTheList).
func servedShippedBedrockBridge(t *testing.T, claudeRegion, piRegion string) (up *captureUpstream, adapter, via string, logs func() string) {
	t.Helper()
	return servedShippedBedrockBridgeOver(t, anthropicOnTheList, claudeRegion, piRegion)
}

// servedShippedBedrockBridgeOver is servedShippedBedrockBridge over the user `providers` layer user
// ("" for none, so the provider's list is packs/bedrock's own, which is empty).
func servedShippedBedrockBridgeOver(t *testing.T, user, claudeRegion, piRegion string) (up *captureUpstream, adapter, via string, logs func() string) {
	t.Helper()
	clearAWS(t)
	logs = captureDiag(t)
	up = withUpstream(t)
	providers, resolved := shippedBridgeTables(t, user)
	use := map[string]string{"claude": "bedrock-bridge", "pi": "bedrock-bridge", "codex": "bedrock-bridge"}
	p := planFor(providers, use, resolved)
	if p.adapter == nil {
		t.Fatalf("no adapter route for claude on bedrock-bridge: %s", p.adapterWhy)
	}
	home := t.TempDir()
	writeAgentKey(t, home, "claude", awsPair("AKIDCLAUDE", claudeRegion))
	writeAgentKey(t, home, "pi", awsPair("AKIDPI", piRegion))
	writeAgentKey(t, home, "codex", awsPair("AKIDCODEX", piRegion))
	adapter, via = freeLoopback(t), freeLoopback(t)
	p.adapter.ListenAddr, p.via.ListenAddr = adapter, via
	startPlan(t, p, home)
	return up, adapter, via, logs
}

// TestTheShippedBedrockBridgeReachesRuntimeInTheServedAgentsRegion is the build's headline: with
// no address anywhere in the provider, every request of the everything profile and of the via
// routes goes to bedrock-runtime in the region the served agent was handed, signed for that region
// with that agent's own pair. Claude's GPT model is translated to runtime's chat-completions, its
// Claude model goes untranslated to runtime's Messages route on the same host (Part 2), pi's
// chat-completions and codex's Responses reach runtime's OpenAI-compatible base, and the serve log
// names each composed upstream and where its region came from. It fails if the boot stops reading
// the region (the route would idle), stops composing runtime's URL, or signs for another region.
func TestTheShippedBedrockBridgeReachesRuntimeInTheServedAgentsRegion(t *testing.T) {
	up, adapter, via, logs := servedShippedBedrockBridge(t, "eu-west-1", "ap-southeast-2")
	for _, tc := range []struct {
		url, body, wantURL, wantScope, wantAKID string
	}{
		{"http://" + adapter + "/v1/messages",
			`{"model":"us.openai.gpt-6.1-sol","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`,
			"https://bedrock-runtime.eu-west-1.amazonaws.com/openai/v1/chat/completions", "/eu-west-1/bedrock/", "AKIDCLAUDE"},
		{"http://" + adapter + "/v1/messages",
			`{"model":"global.anthropic.claude-opus-5-5","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`,
			"https://bedrock-runtime.eu-west-1.amazonaws.com/anthropic/v1/messages", "/eu-west-1/bedrock/", "AKIDCLAUDE"},
		{"http://" + via + "/agent/pi/chat/completions", `{"model":"us.openai.gpt-6.1-sol","messages":[]}`,
			"https://bedrock-runtime.ap-southeast-2.amazonaws.com/openai/v1/chat/completions", "/ap-southeast-2/bedrock/", "AKIDPI"},
		{"http://" + via + "/agent/codex/responses", `{"model":"us.openai.gpt-6.1-sol","input":"hi"}`,
			"https://bedrock-runtime.ap-southeast-2.amazonaws.com/openai/v1/responses", "/ap-southeast-2/bedrock/", "AKIDCODEX"},
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
		auth := sent.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential="+tc.wantAKID+"/") || !strings.Contains(auth, tc.wantScope) {
			t.Errorf("%s: Authorization %q, want %s's pair signed for %s", tc.url, auth, tc.wantAKID, tc.wantScope)
		}
	}
	wantLines(t, logs(),
		"→ openai https://bedrock-runtime.eu-west-1.amazonaws.com/openai/v1",
		"SigV4 for bedrock in eu-west-1 ($AWS_REGION from ",
		"untranslated to https://bedrock-runtime.eu-west-1.amazonaws.com/anthropic/v1/messages",
		"/agent/pi/ chat-completions and Responses → https://bedrock-runtime.ap-southeast-2.amazonaws.com/openai/v1")
}

// WITH NO LIST, A CLAUDE MODEL IS TRANSLATED TOO (docs/design/model-lists-and-pickers.md MM-D32):
// the bridge passes a model to runtime's Messages route untranslated only when the provider's list
// names it Anthropic's, and core never reads a maker out of an id, so on the shipped `bedrock`,
// which ships no list, Claude Opus 5.5 reaches runtime's chat-completions like every other model.
// A list naming its maker (anthropicOnTheList) puts it back on Messages, as the test above pins.
func TestWithNoListTheBridgeTranslatesAClaudeModelToo(t *testing.T) {
	up, adapter, _, _ := servedShippedBedrockBridgeOver(t, "", "eu-west-1", "eu-west-1")
	before := up.calls()
	resp, body := postTo(t, "http://"+adapter+"/v1/messages",
		`{"model":"global.anthropic.claude-opus-5-5","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`, nil)
	if resp.StatusCode != 200 || up.calls() != before+1 {
		t.Fatalf("status %d, upstream calls %d: %s", resp.StatusCode, up.calls()-before, body)
	}
	if got, want := up.requests[before].URL.String(), "https://bedrock-runtime.eu-west-1.amazonaws.com/openai/v1/chat/completions"; got != want {
		t.Errorf("a Claude model with no list went to %s, want %s", got, want)
	}
}

// TestARegionNamedBedrockRouteIdlesWithoutARegion: the bridge never signs for a region nobody
// chose. With no region in the provider or the served agent's key channel, the adapter route
// idles naming the variables it read, and a via route answers 503 saying the same while the
// listener stays up; a variable holding something that is not a region is named, never used.
func TestARegionNamedBedrockRouteIdlesWithoutARegion(t *testing.T) {
	_, adapter, via, logs := servedShippedBedrockBridge(t, "", "")
	wantLines(t, logs(), "the adapter route for provider \"bedrock\" does not serve",
		"it has no region: the provider declares no `region`, and neither $AWS_REGION nor $AWS_DEFAULT_REGION is set")
	_ = adapter
	resp, body := postTo(t, "http://"+via+"/agent/pi/chat/completions", `{"model":"m","messages":[]}`, nil)
	if resp.StatusCode != 503 || !strings.Contains(body, "$AWS_REGION") {
		t.Errorf("a via route with no region: %d %s, want a 503 naming $AWS_REGION", resp.StatusCode, body)
	}

	region, _, why := envRegion(func(name string) (string, string) {
		if name == "AWS_REGION" {
			return "evil.example/x", "the test's channel"
		}
		return "us-east-1", "the test's channel"
	})
	if region != "" || !strings.Contains(why, `"evil.example/x", which is not an AWS region`) {
		t.Errorf("envRegion over a non-region = %q, %q; want it refused by name, never skipped to the next", region, why)
	}
}

// TestADeclaredRegionComposesTheUpstreamAtBoot: a provider's own `region` is the upstream's, in
// the tables alone, so the launcher's plan already names runtime's URL and the agent's key
// channel is not asked; it also wins over a region the agent was handed, as every derive's does.
func TestADeclaredRegionComposesTheUpstreamAtBoot(t *testing.T) {
	providers, resolved := shippedBridgeTables(t, `{"bedrock": {"region": "us-west-2"}}`)
	p := planFor(providers, map[string]string{"claude": "bedrock-bridge", "pi": "bedrock-bridge"}, resolved)
	if p.adapter == nil || p.adapter.UpstreamBaseURL != "https://bedrock-runtime.us-west-2.amazonaws.com/openai/v1" ||
		p.adapter.SignRegion != "us-west-2" || p.adapter.RegionFromEnv {
		t.Fatalf("adapter route %+v (idle %s), want runtime in us-west-2 from the provider's region", p.adapter, p.adapterWhy)
	}
	for _, r := range p.via.Routes {
		if r.Chat.BaseURL != "https://bedrock-runtime.us-west-2.amazonaws.com/openai/v1" || r.Chat.SignRegion != "us-west-2" {
			t.Errorf("%s's via upstream %+v, want runtime in us-west-2", r.Agent, r.Chat)
		}
	}
	bad, resolvedBad := shippedBridgeTables(t, `{"bedrock": {"region": "not a region"}}`)
	if _, why := routeFor(bad, map[string]string{"claude": "bedrock-bridge"}, resolvedBad); !strings.Contains(why,
		`its region "not a region" is not an AWS region`) {
		t.Errorf("a provider region that is not one: idle %q, want it named", why)
	}
}

// TestTheAdapterAddressServesOnlyAViaProfile: on `-p bedrock`, claude runs its own Bedrock client,
// so the address the adapter composed for the via is nobody's there, and the daemon serves no route
// for it: WillServe answers false, and the launch registers no witness for an idle bridge.
func TestTheAdapterAddressServesOnlyAViaProfile(t *testing.T) {
	providers, resolved := shippedBridgeTables(t, "")
	if WillServe(providers, selecting(map[string]string{"claude": "bedrock"}), resolved) {
		t.Error("the bridge would serve claude on -p bedrock, which uses claude's own Bedrock client")
	}
	if _, why := routeFor(providers, map[string]string{"claude": "bedrock"}, resolved); !strings.Contains(why,
		"claude uses its own client") {
		t.Errorf("idle reason %q, want it to say claude uses its own client", why)
	}
	if !WillServe(providers, selecting(map[string]string{"claude": "bedrock-bridge"}), resolved) {
		t.Error("the bridge would not serve claude's everything profile")
	}
}

// TestAJailBridgeOnCodexPlansOnlyClaudesRoute: the jail's half of HS-D24's rule
// (docs/design/host-notch-services.md). The shipped composed table names the Bedrock adapter's
// address under bedrock (WG-I39) beside the Codex route's under openai-codex, and the jail's
// agents carry that table (FT-D2's names), yet a boot whose only selection is claude on `codex`
// plans claude's route alone: no via route and nothing at 8214, the address no agent was pointed at.
func TestAJailBridgeOnCodexPlansOnlyClaudesRoute(t *testing.T) {
	providers, resolved := shippedBridgeTables(t, "")
	if v, _ := providers.Get("bedrock"); endpointBaseURL(v.(*jsonx.OrderedMap), "anthropic") != "http://127.0.0.1:8214" {
		t.Fatalf("the shipped table no longer composes the Bedrock adapter's address onto bedrock: %v", v)
	}
	p := planFor(providers, map[string]string{"claude": "codex"}, resolved)
	if p.adapter == nil || p.adapter.ProviderName != "openai-codex" || p.adapter.ListenAddr != CodexResponsesListenAddr {
		t.Fatalf("the adapter route = %+v (%s), want claude's Codex route on %s", p.adapter, p.adapterWhy, CodexResponsesListenAddr)
	}
	if len(p.via.Routes) != 0 || p.via.ListenAddr != "" {
		t.Errorf("the boot plans via routes %+v on %q, and no agent is on a via profile", p.via.Routes, p.via.ListenAddr)
	}
}

// TestTheSignerKeysOnTheProvidersPlatform pins OQ-WG1's follow-up (WG-I37): a provider that says it
// is Bedrock is signed at the address it names, a FIPS endpoint or a proxy the host rule never
// matched, for the host's region, else its own, else the served agent's; over plain http it is not
// served; and a provider that declares no Bedrock platform keeps the host rule, a runtime host
// signed and any other address never.
func TestTheSignerKeysOnTheProvidersPlatform(t *testing.T) {
	const fips = "https://bedrock-runtime-fips.us-east-1.amazonaws.com/openai/v1"
	const proxy = "https://bedrock.corp.example/openai/v1"
	for _, tc := range []struct {
		name, entry, upstream string
		want                  signing
		refused               string
	}{
		{"a Bedrock platform at a FIPS host signs for its own region", `{"platform":"aws-bedrock","region":"us-east-1"}`,
			fips, signing{Region: "us-east-1"}, ""},
		{"a Bedrock platform at a proxy with no region signs for the agent's", `{"platform":"aws-bedrock"}`,
			proxy, signing{FromEnv: true}, ""},
		{"runtime's host decides the region over the provider's", `{"platform":"aws-bedrock","region":"eu-west-1"}`,
			bedrockBase, signing{Region: "us-east-1"}, ""},
		{"a Bedrock platform over plain http is refused", `{"platform":"aws-bedrock","region":"us-east-1"}`,
			"http://bedrock.corp.example/openai/v1", signing{}, "only over https"},
		{"no platform keeps the host rule at runtime's host", `{}`, bedrockBase, signing{Region: "us-east-1"}, ""},
		{"no platform at a FIPS host is never signed", `{}`, fips, signing{}, ""},
		{"an unknown platform is inert", `{"platform":"gcp-vertex","region":"us-east-1"}`, proxy, signing{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, _ := mustProviders(t, `{"x":`+tc.entry+`}`).Get("x")
			got := bedrockSigning(v.(*jsonx.OrderedMap), tc.upstream)
			if tc.refused != "" {
				if !strings.Contains(got.Refusal, tc.refused) || got.signs() {
					t.Errorf("bedrockSigning = %+v, want refused for %q", got, tc.refused)
				}
				return
			}
			if got != tc.want {
				t.Errorf("bedrockSigning = %+v, want %+v", got, tc.want)
			}
		})
	}

	// THROUGH THE DAEMON: a user's Bedrock provider at a proxy, its region only in the served agent's
	// key channel, is served signed on both the adapter and the via route.
	clearAWS(t)
	up := withUpstream(t)
	providers, resolved := shippedBridgeTables(t, `{"corp": {"platform": "aws-bedrock",
		"endpoints": {"openai": {"base_url": "`+proxy+`", "wire_api": "openai-chat-completions"}}}}`)
	resolved["corp-via"] = packload.ResolvedProfile{Provider: "corp", Via: ServiceName, ViaBase: "http://127.0.0.1:8216"}
	p := planFor(providers, map[string]string{"claude": "corp-via", "pi": "corp-via"}, resolved)
	if p.adapter == nil || !p.adapter.RegionFromEnv || p.adapter.UpstreamBaseURL != proxy {
		t.Fatalf("adapter route %+v (idle %s), want the proxy, signed for the agent's region", p.adapter, p.adapterWhy)
	}
	home := t.TempDir()
	writeAgentKey(t, home, "claude", awsPair("AKIDCLAUDE", "ca-central-1"))
	writeAgentKey(t, home, "pi", awsPair("AKIDPI", "ca-central-1"))
	adapter, via := freeLoopback(t), freeLoopback(t)
	p.adapter.ListenAddr, p.via.ListenAddr = adapter, via
	startPlan(t, p, home)
	for _, url := range []string{"http://" + adapter + "/v1/messages", "http://" + via + "/agent/pi/chat/completions"} {
		before := up.calls()
		resp, body := postTo(t, url, `{"model":"m","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`, nil)
		if resp.StatusCode != 200 || up.calls() != before+1 {
			t.Fatalf("%s: %d %s", url, resp.StatusCode, body)
		}
		sent := up.requests[before]
		if sent.URL.Host != "bedrock.corp.example" ||
			!strings.Contains(sent.Header.Get("Authorization"), "/ca-central-1/bedrock/aws4_request") {
			t.Errorf("%s: sent to %s with %q, want the proxy signed for ca-central-1", url, sent.URL,
				sent.Header.Get("Authorization"))
		}
	}
}

// TestTheAdapterFrontsExactlyThePlatformsTheDaemonReaches keeps the declaration that composes the
// bridge's address onto a provider with none (packs/wire-bridge's `from_platforms`) and the
// daemon's implementation of reaching one (FrontedPlatforms) one list: a platform declared and not
// implemented would compose an address the daemon idles on, and one implemented and not declared
// would never be reached.
func TestTheAdapterFrontsExactlyThePlatformsTheDaemonReaches(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "packs", "wire-bridge", "pack.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Contributes []struct {
			Kind   string `json:"kind"`
			Adapts *struct {
				From, To      string
				FromPlatforms []string `json:"from_platforms"`
			} `json:"adapts"`
		} `json:"contributes"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	declared := map[string][]string{}
	for _, c := range m.Contributes {
		if c.Kind == "adapter" && c.Adapts != nil && len(c.Adapts.FromPlatforms) > 0 {
			declared[c.Adapts.From+"->"+c.Adapts.To] = c.Adapts.FromPlatforms
		}
	}
	got, ok := declared["openai->anthropic"]
	if len(declared) != 1 || !ok || strings.Join(got, ",") != strings.Join(FrontedPlatforms, ",") {
		t.Errorf("packs/wire-bridge fronts %v, want the chat-completions adapter alone to front %v", declared, FrontedPlatforms)
	}
}
