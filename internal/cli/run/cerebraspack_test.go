package run

// cerebraspack_test.go pins the SECOND purely-declarative provider pack
// (docs/design/cerebras-pack-and-copilot-delivery.md) against the pack the binary
// actually embeds, the way zaipack_test.go pins the first. The pack's contract is the
// same shape — select it, drop in a key — over a service that speaks ONE wire protocol:
// every assertion here is something the design doc's audit table claims about the
// delivery, pinned where it composes.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// cerebrasKey is the env_sources channel with the one credential the pack names.
func cerebrasKey() *jsonx.OrderedMap {
	m := jsonx.NewOrderedMap()
	m.Set("CEREBRAS_API_KEY", "csk-test")
	return m
}

// cerebrasSelected is an agent pack beside the provider pack: the minimal launch the
// README's setup section promises.
func cerebrasSelected(t *testing.T) []*packload.Pack {
	return []*packload.Pack{officialPack(t, "pi"), officialPack(t, "cerebras")}
}

// TestCerebrasPackShipsTheCatalogTheDerivesRead: the pack's whole openai-side content
// crosses as the composed table — base URL, the measured wire protocol, the key NAME,
// and the one wire-true model alias. pi's and opencode's catalogs (and copilot's BYOK
// block) are all derived from exactly this entry.
func TestCerebrasPackShipsTheCatalogTheDerivesRead(t *testing.T) {
	argv := zaiLaunch(t, cerebrasSelected(t), bareConfig(), cerebrasKey(), nil)

	vals := envArgValues(argv, "YOLO_PROVIDERS")
	if len(vals) != 1 {
		t.Fatalf("argv carries %d YOLO_PROVIDERS args, want 1", len(vals))
	}
	v, err := jsonx.Decode([]byte(strings.TrimPrefix(vals[0], "YOLO_PROVIDERS=")))
	if err != nil {
		t.Fatalf("YOLO_PROVIDERS is not JSON: %v", err)
	}
	providers, ok := v.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("YOLO_PROVIDERS is not an object: %T", v)
	}
	cerebras, ok := mapGet(providers, "cerebras").(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("YOLO_PROVIDERS has no cerebras entry: %v", providers.Keys())
	}
	if got := mapStr(cerebras, "api_key_env_name"); got != "CEREBRAS_API_KEY" {
		t.Errorf("cerebras api_key_env_name = %q, want the conventional NAME", got)
	}
	endpoints, ok := mapGet(cerebras, "endpoints").(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("cerebras ships no endpoints table: %v", cerebras.Keys())
	}
	// The anthropic endpoint is the WIRE BRIDGE's loopback URL (wire-bridge.md
	// §3.3) — the declaration the bridge pack, joined through the needs entry
	// below, is what makes true. It is a loopback URL and nothing more: no host,
	// no credential in the URL, and no wire_api (claude's derive reads base_url
	// directly).
	anthropic, _ := mapGet(endpoints, "anthropic").(*jsonx.OrderedMap)
	if mapStr(anthropic, "base_url") != "http://127.0.0.1:8214" {
		t.Errorf("anthropic endpoint = %v, want the bridge's manifest-borne loopback "+
			"URL — the single source of the port (WB-D2/D13)", anthropic)
	}
	openai, _ := mapGet(endpoints, "openai").(*jsonx.OrderedMap)
	if mapStr(openai, "base_url") != "https://api.cerebras.ai/v1" ||
		mapStr(openai, "wire_api") != "openai-chat-completions" {
		t.Errorf("openai endpoint = %v, want the measured base URL and the canonical name "+
			"for a chat-completions-only service (no /v1/responses exists — which is also "+
			"why codex cannot ride it)", openai)
	}
	// The need (WB-D3): the bridge pack joins the launch by itself when a
	// consumer of the bridged URL is among the launch's agents — the declaration
	// that makes the anthropic URL above true. Neither half ships without the
	// other (plan build order step 4). The bins are the consumers of an
	// anthropic endpoint, and there are exactly two: claude reads it directly,
	// and copilot's derive PREFERS it when present (D-3) — drop a bin here and
	// that agent's launch composes a dead URL.
	needs := officialPack(t, "cerebras").Decl.DeclaredNeeds()
	if len(needs) != 1 || needs[0].Pack != "wire-bridge" || len(needs[0].WhenBins) != 2 ||
		needs[0].WhenBins[0] != "claude" || needs[0].WhenBins[1] != "copilot" {
		t.Errorf("cerebras needs = %v, want exactly [{wire-bridge, when_bins: [claude, copilot]}] — "+
			"the anthropic endpoint and the need that stages its bridge cannot ship apart, "+
			"and every derive that reads an anthropic endpoint needs the bridge staged",
			needs)
	}
	models, ok := mapGet(cerebras, "models").(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("cerebras ships no models map: %v", cerebras.Keys())
	}
	if got := mapStr(models, "default"); got != "qwen-3.8-27b" {
		t.Errorf("cerebras default model = %q, want the one agentic-capable public model "+
			"the design doc ruled on (D-1)", got)
	}
	if models.Len() != 1 {
		t.Errorf("cerebras ships %d aliases, want exactly the one — gpt-oss-120b is "+
			"deliberately absent (hallucinated tool calls have no tier), and extra aliases "+
			"are the user's to merge: %v", models.Len(), models.Keys())
	}
	// The options surface the profile may tune, with defaults (OQ-CS4): model
	// resolves through the alias map, and context_window is the free-tier figure
	// claude's auto-compact triggers at — live only since the bridge made claude
	// able to ride this provider at all (wire-bridge.md §6, WB-D8).
	options, ok := mapGet(cerebras, "options").(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("cerebras ships no options map: %v", cerebras.Keys())
	}
	if got := mapStr(options, "model"); got != "default" {
		t.Errorf("options.model = %q, want the alias name it resolves through", got)
	}
	if got := mapStr(options, "context_window"); got != "65536" {
		t.Errorf("options.context_window = %q, want the free-tier 64K bound — the "+
			"conservative window claude's auto-compact should trigger at (WB-D8)", got)
	}
	if options.Len() != 2 {
		t.Errorf("cerebras declares %d options, want exactly model and context_window — "+
			"api_timeout_ms stays absent with no evidence of 50-minute turns: %v",
			options.Len(), options.Keys())
	}
}

// TestCerebrasPackComposesTheBridgedClaudeRoute: claude selected beside cerebras
// composes the bridge's loopback URL — the anthropic endpoint the pack declares,
// which the wire-bridge pack (joined through the needs entry) is what makes true.
// The derive itself is UNCHANGED (wire-bridge.md §3.3): it reads the endpoint
// like any other, and cannot even see whether a bridge exists. (The token alone
// also rides, a recorded pre-existing behavior the design doc flags as OQ-2;
// this test does not pin that half because it is not this pack's claim.)
//
// This is the flip of the pre-bridge assertion (claude got nothing from this
// pack): the endpoint and the need that stages its bridge shipped together, and
// this test is where the endpoint half is pinned at the argv.
func TestCerebrasPackComposesTheBridgedClaudeRoute(t *testing.T) {
	packs := []*packload.Pack{officialPack(t, "claude"), officialPack(t, "cerebras")}
	argv := zaiLaunch(t, packs, bareConfig(), cerebrasKey(),
		func(o *Options) { o.ProfileName = "cerebras" })

	got := envArgValues(argv, "ANTHROPIC_BASE_URL")
	if len(got) != 1 || got[0] != "ANTHROPIC_BASE_URL=http://127.0.0.1:8214" {
		t.Errorf("claude must be routed at the bridge's loopback URL the manifest "+
			"declares: %q", got)
	}
}

// TestCerebrasPackRefusesALaunchWithNoKey: the pack's README contract, same as zai's —
// catalog membership demands the credential (OQ-PT4), naming what it wants; quiet once
// the key arrives.
func TestCerebrasPackRefusesALaunchWithNoKey(t *testing.T) {
	packs := []*packload.Pack{officialPack(t, "cerebras")}

	o := retireOptions(t, discardBuf())
	lines, refuse := o.checkProviderCredentials(bareConfig(), packs,
		channelFor(t, o, bareConfig(), packs, emptyEnv()), nil)
	if !refuse {
		t.Fatal("a selected cerebras pack with no key hydrated must refuse the launch")
	}
	got := strings.Join(lines, "\n")
	for _, want := range []string{"CEREBRAS_API_KEY", `provider "cerebras"`, "pack cerebras"} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal must name %q:\n%s", want, got)
		}
	}

	o = retireOptions(t, discardBuf())
	if lines, refuse := o.checkProviderCredentials(bareConfig(), packs,
		channelFor(t, o, bareConfig(), packs, cerebrasKey()), nil); len(lines) != 0 || refuse {
		t.Errorf("the key the README says to drop in must satisfy the check:\n%s",
			strings.Join(lines, "\n"))
	}
}
