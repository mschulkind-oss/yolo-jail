package packload

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// shippedZaiPack returns a pack declaring the zai provider with both protocols, the
// canonical shape zai-plumbing.md §4 ships.
func shippedZaiPack(t *testing.T) *Pack {
	return &Pack{Name: "zai", Decl: declFrom(t, `{"contributes":[
	  {"kind":"provider","name":"zai",
	   "endpoints":{"anthropic":{"base_url":"https://api.z.ai/api/anthropic"},
	                "openai":{"base_url":"https://api.z.ai/api/paas/v4","wire_api":"openai-chat-completions"}},
	   "api_key_env_name":"ZAI_API_KEY",
	   "models":{"default":"glm-4.7","fast":"glm-4.7-air"},
	   "env_shape":{"anthropic":{"ANTHROPIC_BASE_URL":"{endpoint}",
	                             "ANTHROPIC_AUTH_TOKEN":"{key}"}}}]}`)}
}

// shippedBedrockPack returns a pack declaring a REGIONAL provider — one whose address is
// a region rather than a base URL, which is the shape packs/claude ships for bedrock.
func shippedBedrockPack(t *testing.T) *Pack {
	return &Pack{Name: "bedrock", Decl: declFrom(t, `{"contributes":[
	  {"kind":"provider","name":"bedrock",
	   "region":"us-east-1",
	   "env_shape":{"anthropic":{"AWS_REGION":"{region}"}}}]}`)}
}

// userProviders decodes a `providers` config block into the map the composer takes.
func userProviders(t *testing.T, body string) *jsonx.OrderedMap {
	t.Helper()
	v, err := jsonx.Decode([]byte(body))
	if err != nil {
		t.Fatalf("fixture providers block: %v", err)
	}
	m, ok := v.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("fixture providers block: expected an object, got %T", v)
	}
	return m
}

func dump(t *testing.T, v any) string {
	t.Helper()
	s, err := jsonx.DumpsCompact(v)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	return s
}

// TestComposeProvidersShipsUnderUserConfig pins the composition and its direction: the
// pack's SERVICE facts arrive whole, and the user's config wins PER FIELD — an override
// of one model alias or one endpoint's URL must not force restating the rest, which is
// the entire reason the facts are shipped rather than authored (zai-plumbing.md §7).
func TestComposeProvidersShipsUnderUserConfig(t *testing.T) {
	pack := shippedZaiPack(t)

	// Pack alone: the facts verbatim, the credential pointer spelled the way the
	// config key is.
	got := ComposeProviders(nil, []*Pack{pack})
	// Key ORDER is pinned too: the entry is serialized into an env var the derives read,
	// so a run that reshuffles it would turn every jail's provider table into a diff.
	want := `{"zai": {"api_key_env_name": "ZAI_API_KEY", ` +
		`"models": {"default": "glm-4.7", "fast": "glm-4.7-air"}, ` +
		`"endpoints": {"anthropic": {"base_url": "https://api.z.ai/api/anthropic"}, ` +
		`"openai": {"base_url": "https://api.z.ai/api/paas/v4", "wire_api": "openai-chat-completions"}}, ` +
		`"env_shape": {"anthropic": {"ANTHROPIC_AUTH_TOKEN": "{key}", ` +
		`"ANTHROPIC_BASE_URL": "{endpoint}"}}}}`
	if s := dump(t, got); s != want {
		t.Errorf("pack-only composition:\n got %s\nwant %s", s, want)
	}

	// User overrides: one alias re-pointed, one added, one endpoint's URL re-pointed —
	// everything else survives from the pack.
	user := userProviders(t, `{"zai":{
	  "models":{"fast":"glm-5","coder":"glm-4.7-coder"},
	  "endpoints":{"openai":{"base_url":"https://proxy.example.internal/v4"}}}}`)
	got = ComposeProviders(user, []*Pack{pack})
	if s := dump(t, got); !strings.Contains(s, `"fast": "glm-5"`) ||
		!strings.Contains(s, `"coder": "glm-4.7-coder"`) || !strings.Contains(s, `"default": "glm-4.7"`) {
		t.Errorf("models should merge per alias, got %s", s)
	}
	if s := dump(t, got); !strings.Contains(s, `"openai": {"base_url": "https://proxy.example.internal/v4", "wire_api": "openai-chat-completions"}`) {
		t.Errorf("an overridden endpoint should keep the pack's wire_api, got %s", s)
	}
	if s := dump(t, got); !strings.Contains(s, `"anthropic": {"base_url": "https://api.z.ai/api/anthropic"}`) {
		t.Errorf("the untouched endpoint must survive the override, got %s", s)
	}

	// A provider only the user declares passes through whole.
	user = userProviders(t, `{"mine":{"base_url":"https://mine.example/v4","api_key_env_name":"MINE_KEY"}}`)
	if s := dump(t, ComposeProviders(user, []*Pack{pack})); !strings.Contains(s, `"mine": {"base_url": "https://mine.example/v4", "api_key_env_name": "MINE_KEY"}`) {
		t.Errorf("a user-only provider should pass through, got %s", s)
	}

	// A null user entry DROPS the provider — the same disable convention the config key
	// already has, applied to the shipped layer the user is overriding.
	user = userProviders(t, `{"zai":null}`)
	if got := ComposeProviders(user, []*Pack{pack}); got != nil {
		t.Errorf("a null user entry should drop the shipped provider, got %s", dump(t, got))
	}
}

// TestComposeProvidersCarriesARegionalProviderFacts pins the region: a provider whose
// address is a region composes it into the entry under the key the config key uses, so a
// user override of one field replaces it and the env shape's {region} placeholder reads
// the same value the derives would see.
func TestComposeProvidersCarriesARegionalProviderFacts(t *testing.T) {
	pack := shippedBedrockPack(t)
	want := `{"bedrock": {"region": "us-east-1", ` +
		`"env_shape": {"anthropic": {"AWS_REGION": "{region}"}}}}`
	if s := dump(t, ComposeProviders(nil, []*Pack{pack})); s != want {
		t.Errorf("regional provider composition:\n got %s\nwant %s", s, want)
	}
	// The user's own region is the override, per field like any other.
	user := userProviders(t, `{"bedrock":{"region":"eu-central-1"}}`)
	if s := dump(t, ComposeProviders(user, []*Pack{pack})); !strings.Contains(s, `"region": "eu-central-1"`) {
		t.Errorf("a user region should override the shipped one, got %s", s)
	}
}

// TestComposeProvidersNilWhenNothingShipped: a launch with no provider from either side
// must encode exactly as it did before the kind existed — the golden argv pins "{}".
func TestComposeProvidersNilWhenNothingShipped(t *testing.T) {
	pack := &Pack{Name: "claude", Decl: declFrom(t, `{"contributes":[
	  {"kind":"env","vars":{"A":"1"}}]}`)}
	if got := ComposeProviders(nil, []*Pack{pack}); got != nil {
		t.Errorf("no providers anywhere should compose to nil, got %s", dump(t, got))
	}
	empty := jsonx.NewOrderedMap()
	if got := ComposeProviders(empty, []*Pack{pack}); got != nil {
		t.Errorf("an empty user table should compose to nil, got %s", dump(t, got))
	}
}

// TestComposeProvidersKeepsFirstOnANameClash: the pre-flight refuses a name two packs
// ship, so the compose never has to decide — but if a caller ever skips it, the result
// must be a STABLE table (first wins) rather than whichever pack sorted last.
func TestComposeProvidersKeepsFirstOnANameClash(t *testing.T) {
	a := &Pack{Name: "a", Decl: declFrom(t, `{"contributes":[
	  {"kind":"provider","name":"zai","endpoints":{"openai":{"base_url":"https://a.example/v4"}}}]}`)}
	b := &Pack{Name: "b", Decl: declFrom(t, `{"contributes":[
	  {"kind":"provider","name":"zai","endpoints":{"openai":{"base_url":"https://b.example/v4"}}}]}`)}
	if s := dump(t, ComposeProviders(nil, []*Pack{a, b})); !strings.Contains(s, "https://a.example/v4") {
		t.Errorf("first shipper should win an unrefused clash, got %s", s)
	}
}

// TestProviderClaimIsSoleOwnedByName pins the footprint half: one claim per provider,
// keyed by the BARE NAME (no discriminator), so the generic exclusive loop is the whole
// cross-pack check — two packs shipping zai collide, and one pack shipping two providers
// does not.
func TestProviderClaimIsSoleOwnedByName(t *testing.T) {

	cs := claimSet(FootprintOf(shippedZaiPack(t)))
	c, ok := cs["provider zai"]
	if !ok {
		t.Fatalf("shipped provider produced no claim: %+v", cs)
	}
	if c.ReviewWorthy {
		t.Errorf("service facts are not review-worthy: %+v", c)
	}
	if !strings.Contains(c.Detail, "$ZAI_API_KEY") {
		t.Errorf("the claim should say where the key comes from: %+v", c)
	}

	// Two packs, one name: the collision the exclusivity exists for.
	other := &Pack{Name: "zai-other", Decl: declFrom(t, `{"contributes":[
	  {"kind":"provider","name":"zai","endpoints":{"openai":{"base_url":"https://other.example/v4"}}}]}`)}
	cols := Collisions([]*Pack{shippedZaiPack(t), other})
	if len(cols) != 1 || cols[0].Kind != packdecl.KindProvider || cols[0].Target != "zai" {
		t.Fatalf("two packs shipping one provider should collide on the name, got %+v", cols)
	}
	// One pack, two names: the ordinary multi-provider pack.
	two := &Pack{Name: "multi", Decl: declFrom(t, `{"contributes":[
	  {"kind":"provider","name":"zai","endpoints":{"openai":{"base_url":"https://a.example/v4"}}},
	  {"kind":"provider","name":"other","endpoints":{"openai":{"base_url":"https://b.example/v4"}}}]}`)}
	if cols := Collisions([]*Pack{two}); len(cols) != 0 {
		t.Errorf("two distinct provider names in one pack are not a collision, got %+v", cols)
	}
}
