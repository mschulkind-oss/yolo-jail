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

// compose is ComposeProviders with the refusal channel turned into a test failure: the
// assertions below are about the SHAPE of a table that composes, and each way one can
// fail to compose has a test of its own that reads the error.
func compose(t *testing.T, user *jsonx.OrderedMap, packs []*Pack) *jsonx.OrderedMap {
	t.Helper()
	got, err := ComposeProviders(user, packs)
	if err != nil {
		t.Fatalf("composing: %v", err)
	}
	return got
}

// TestComposeProvidersShipsUnderUserConfig pins the composition and its direction: the
// pack's SERVICE facts arrive whole, and the user's config wins PER FIELD — an override
// of one model alias or one endpoint's URL must not force restating the rest, which is
// the entire reason the facts are shipped rather than authored (zai-plumbing.md §7).
func TestComposeProvidersShipsUnderUserConfig(t *testing.T) {
	pack := shippedZaiPack(t)

	// Pack alone: the facts verbatim, the credential pointer spelled the way the
	// config key is.
	got := compose(t, nil, []*Pack{pack})
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
	got = compose(t, user, []*Pack{pack})
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
	if s := dump(t, compose(t, user, []*Pack{pack})); !strings.Contains(s, `"mine": {"base_url": "https://mine.example/v4", "api_key_env_name": "MINE_KEY"}`) {
		t.Errorf("a user-only provider should pass through, got %s", s)
	}

	// A null user entry DROPS the provider — the same disable convention the config key
	// already has, applied to the shipped layer the user is overriding.
	user = userProviders(t, `{"zai":null}`)
	if got := compose(t, user, []*Pack{pack}); got != nil {
		t.Errorf("a null user entry should drop the shipped provider, got %s", dump(t, got))
	}
}

// TestComposeProvidersRefusesAManufacturedAddressPair pins D2 (provider-table-fidelity.md
// §4.1, OQ-PT2): the shorthand and the endpoint map are each legal alone, and the config
// validator refuses them together in an entry a user wrote — but this merge is PER FIELD,
// so a user `base_url` over a pack that ships `endpoints` used to compose exactly the
// refused pair and hand it to consumers that resolve it differently (the derives prefer
// the shorthand and fall back to endpoints; agentenv reads endpoints only). Refusing here
// is the rule the validator enforces on the inputs holding on the merge's output too.
//
// The refusal names BOTH sources, because "your config conflicts with the pack" is not
// actionable until it says which pack and which key. The override itself stays spellable:
// the passing assertion above this one pins `endpoints.<protocol>.base_url` as the way to
// re-point one protocol and keep the rest of the pack's facts.
func TestComposeProvidersRefusesAManufacturedAddressPair(t *testing.T) {
	pack := shippedZaiPack(t)
	user := userProviders(t, `{"zai":{"base_url":"https://my.proxy.example/v1"}}`)

	got, err := ComposeProviders(user, []*Pack{pack})
	if err == nil {
		t.Fatalf("a user base_url over a pack that ships endpoints must refuse, got %s", dump(t, got))
	}
	for _, want := range []string{
		`"zai"`,
		packdecl.ProviderAddressConflictMessage, // the SAME words the config validator refuses with
		"pack zai",                              // where the endpoints came from
		"providers.zai.base_url",                // where the shorthand came from
		"endpoints.<protocol>.base_url",         // the spelling that still works
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name %q:\n%s", want, err.Error())
		}
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
	if s := dump(t, compose(t, nil, []*Pack{pack})); s != want {
		t.Errorf("regional provider composition:\n got %s\nwant %s", s, want)
	}
	// The user's own region is the override, per field like any other.
	user := userProviders(t, `{"bedrock":{"region":"eu-central-1"}}`)
	if s := dump(t, compose(t, user, []*Pack{pack})); !strings.Contains(s, `"region": "eu-central-1"`) {
		t.Errorf("a user region should override the shipped one, got %s", s)
	}
}

// TestComposeProvidersNilWhenNothingShipped: a launch with no provider from either side
// must encode exactly as it did before the kind existed — the golden argv pins "{}".
func TestComposeProvidersNilWhenNothingShipped(t *testing.T) {
	pack := &Pack{Name: "claude", Decl: declFrom(t, `{"contributes":[
	  {"kind":"env","vars":{"A":"1"}}]}`)}
	if got := compose(t, nil, []*Pack{pack}); got != nil {
		t.Errorf("no providers anywhere should compose to nil, got %s", dump(t, got))
	}
	empty := jsonx.NewOrderedMap()
	if got := compose(t, empty, []*Pack{pack}); got != nil {
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
	if s := dump(t, compose(t, nil, []*Pack{a, b})); !strings.Contains(s, "https://a.example/v4") {
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

// TestProviderCredentialGapsFollowCatalogMembership pins the requirement rule (OQ-PT4,
// provider-catalog-and-selection.md §4): what owes a credential is an entry of the COMPOSED
// table carrying an endpoint — not a provider a selected pack declares. In a dictionary
// means you need the key; not in one means you do not. Nothing here looks at a declaration
// directly, so deleting the walk over the composed table (or the endpoint predicate that
// gates it) leaves the first and third assertions with no facts to read.
func TestProviderCredentialGapsFollowCatalogMembership(t *testing.T) {
	zai := shippedZaiPack(t)
	bedrock := shippedBedrockPack(t)
	lookup := func(string) (string, bool) { return "", false } // nothing hydrated anywhere

	// Cataloged, and the credential never arrived: the refusal, naming the provider, the
	// variable and the pack whose entry demands it. (Two lines: the fact, then the
	// consulted-channels line the function always appends.)
	facts := ProviderCredentialGaps([]*Pack{zai}, compose(t, nil, []*Pack{zai}), lookup, nil)
	if len(facts) != 2 {
		t.Fatalf("a cataloged provider with an unset credential must refuse, got %+v", facts)
	}
	for _, want := range []string{"pack zai", `provider "zai"`, "ZAI_API_KEY"} {
		if !strings.Contains(facts[0], want) {
			t.Errorf("the fact must name %q: %s", want, facts[0])
		}
	}

	// The SAME table with the entry null-dropped: the provider left the catalog, and the
	// requirement left with it. This is the launch D4 measured refusing before the rule
	// moved — packs: ["claude"] with providers: {"bedrock": null} came out as "pack claude
	// requires provider "bedrock", and the composed providers table has no entry by that
	// name", the user's own "no" read back as a fault.
	user := userProviders(t, `{"zai":null}`)
	if facts := ProviderCredentialGaps([]*Pack{zai}, compose(t, user, []*Pack{zai}), lookup, nil); facts != nil {
		t.Errorf("a null-dropped provider is not required:\n%s", strings.Join(facts, "\n"))
	}

	// A provider with no endpoint composes whole and still demands nothing, with nothing
	// hydrated at all: it reaches no agent's catalog, so there is no key to demand.
	if facts := ProviderCredentialGaps([]*Pack{bedrock}, compose(t, nil, []*Pack{bedrock}), lookup, nil); facts != nil {
		t.Errorf("a provider with no endpoint must demand no credential:\n%s", strings.Join(facts, "\n"))
	}

	// The same question with the credential pointer PRESENT, which is the case the endpoint
	// predicate actually decides: a provider that names its key but has no endpoint is in
	// no agent's dictionary, so the variable is nobody's to set. Demanding it is the
	// pack-declaration walk again — the shape that refused launches for a provider no
	// agent could reach.
	keyed := &Pack{Name: "keyed", Decl: declFrom(t, `{"contributes":[
	  {"kind":"provider","name":"keyed","api_key_env_name":"KEYED_KEY",
	   "region":"us-east-1"}]}`)}
	if facts := ProviderCredentialGaps([]*Pack{keyed}, compose(t, nil, []*Pack{keyed}), lookup, nil); facts != nil {
		t.Errorf("a provider with a credential pointer but no endpoint reaches no catalog "+
			"and must demand nothing:\n%s", strings.Join(facts, "\n"))
	}

	// An entry only the user's config put in the table is required the same way, and the
	// attribution says so — naming a pack that does not exist would send the reader
	// looking for one.
	user = userProviders(t, `{"mine":{"api_key_env_name":"MINE_KEY",
	  "endpoints":{"openai":{"base_url":"https://mine.example/v4"}}}}`)
	facts = ProviderCredentialGaps(nil, compose(t, user, nil), lookup, nil)
	if len(facts) != 2 {
		t.Fatalf("a user-declared cataloged provider must refuse too, got %+v", facts)
	}
	for _, want := range []string{"your config declares", `provider "mine"`, "MINE_KEY"} {
		if !strings.Contains(facts[0], want) {
			t.Errorf("the fact must name %q: %s", want, facts[0])
		}
	}
}
