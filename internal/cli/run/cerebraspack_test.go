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
	if anthropic := mapGet(endpoints, "anthropic"); anthropic != nil {
		t.Errorf("cerebras declares an anthropic endpoint (%v) — the service has none, "+
			"and the design doc's audit says claude gets nothing from this pack", anthropic)
	}
	openai, _ := mapGet(endpoints, "openai").(*jsonx.OrderedMap)
	if mapStr(openai, "base_url") != "https://api.cerebras.ai/v1" ||
		mapStr(openai, "wire_api") != "openai-chat-completions" {
		t.Errorf("openai endpoint = %v, want the measured base URL and the canonical name "+
			"for a chat-completions-only service (no /v1/responses exists — which is also "+
			"why codex cannot ride it)", openai)
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
}

// TestCerebrasPackComposesNoClaudeRoute: claude selected beside an openai-only provider
// composes no base URL — there is no anthropic endpoint to point at, and a fabricated
// one would send claude's traffic at a host that speaks the wrong protocol. (The token
// alone still rides, a recorded pre-existing behavior the design doc flags as OQ-2;
// this test does not pin that half because it is not this pack's claim.)
func TestCerebrasPackComposesNoClaudeRoute(t *testing.T) {
	packs := []*packload.Pack{officialPack(t, "claude"), officialPack(t, "cerebras")}
	argv := zaiLaunch(t, packs, bareConfig(), cerebrasKey(),
		func(o *Options) { o.ProfileName = "cerebras" })

	if got := envArgValues(argv, "ANTHROPIC_BASE_URL"); len(got) != 0 {
		t.Errorf("claude must not be routed by an openai-only provider: %q", got)
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
