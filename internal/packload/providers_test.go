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
	   "models":{"default":"glm-5.3[1m]","fast":"glm-5.3-flash"}}]}`)}
}

// shippedBedrockPack returns a pack declaring a REGIONAL provider — one whose address is
// a region rather than a base URL, which is the shape packs/claude ships for bedrock.
func shippedBedrockPack(t *testing.T) *Pack {
	return &Pack{Name: "bedrock", Decl: declFrom(t, `{"contributes":[
	  {"kind":"provider","name":"bedrock","region":"us-east-1"}]}`)}
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
		`"models": {"default": "glm-5.3[1m]", "fast": "glm-5.3-flash"}, ` +
		`"endpoints": {"anthropic": {"base_url": "https://api.z.ai/api/anthropic"}, ` +
		`"openai": {"base_url": "https://api.z.ai/api/paas/v4", "wire_api": "openai-chat-completions"}}}}`
	if s := dump(t, got); s != want {
		t.Errorf("pack-only composition:\n got %s\nwant %s", s, want)
	}

	// User overrides: one alias re-pointed, one added, one endpoint's URL re-pointed —
	// everything else survives from the pack.
	user := userProviders(t, `{"zai":{
	  "models":{"fast":"glm-5","coder":"glm-5.3[1m]-coder"},
	  "endpoints":{"openai":{"base_url":"https://proxy.example.internal/v4"}}}}`)
	got = compose(t, user, []*Pack{pack})
	if s := dump(t, got); !strings.Contains(s, `"fast": "glm-5"`) ||
		!strings.Contains(s, `"coder": "glm-5.3[1m]-coder"`) || !strings.Contains(s, `"default": "glm-5.3[1m]"`) {
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

// TestComposeProvidersHonorTheNullDropBelowTheTopLevel pins the merge's own convention one
// level under the entry (provider-catalog-and-selection.md §4's note, provider-table-
// fidelity-plan.md step 4): a null in the user's override is a DELETE wherever it appears,
// not a value. At the top level ComposeProviders already dropped the whole entry; below it
// the per-field fold set the key to a literal null instead — `models.fast: null` composed
// an alias whose value is nothing, which no reader of the table has a meaning for.
func TestComposeProvidersHonorTheNullDropBelowTheTopLevel(t *testing.T) {
	pack := shippedZaiPack(t)

	// One level under the entry: the alias goes, the one beside it stays.
	user := userProviders(t, `{"zai":{"models":{"fast":null}}}`)
	got := compose(t, user, []*Pack{pack})
	if s := dump(t, got); strings.Contains(s, "fast") {
		t.Errorf("a null model alias must delete the alias, got %s", s)
	}
	if s := dump(t, got); !strings.Contains(s, `"default": "glm-5.3[1m]"`) {
		t.Errorf("the alias beside the null must survive, got %s", s)
	}

	// Two levels under it, and over an OBJECT: the null takes the whole subtree with it.
	user = userProviders(t, `{"zai":{"endpoints":{"anthropic":{"base_url":null,
	  "wire_api":"replaced"}}}}`)
	got = compose(t, user, []*Pack{pack})
	if s := dump(t, got); strings.Contains(s, "api.z.ai/api/anthropic") {
		t.Errorf("a null endpoint field must delete the field, got %s", s)
	}
	if s := dump(t, got); !strings.Contains(s, `"wire_api": "replaced"`) {
		t.Errorf("the field beside the null must survive, got %s", s)
	}
	if s := dump(t, got); !strings.Contains(s, `"zai"`) {
		t.Errorf("the entry itself must survive a null inside it, got %s", s)
	}

	user = userProviders(t, `{"zai":{"models":null}}`)
	if s := dump(t, compose(t, user, []*Pack{pack})); strings.Contains(s, "glm-5.3[1m]") {
		t.Errorf("a null over a whole subtree must delete the subtree, got %s", s)
	}

	// Non-null still replaces — the convention this sits beside, already pinned by
	// TestComposeProvidersShipsUnderUserConfig and pinned here once more beside the delete
	// it shares a fold with.
	user = userProviders(t, `{"zai":{"models":{"fast":"glm-5"}}}`)
	if s := dump(t, compose(t, user, []*Pack{pack})); !strings.Contains(s, `"fast": "glm-5"`) {
		t.Errorf("a non-null override still replaces, got %s", s)
	}
}

// TestComposeProvidersRendersTheDeclaredOptionsBlock pins the third fact the composed
// table carries: the provider's declared option surface. A pack's manifest can declare it
// and the table can omit it, and nothing complains — the derives read named fields and
// ignore the rest — so the omission was invisible until the profile resolution started
// reading its census off the table and came up empty against a provider that declares.
func TestComposeProvidersRendersTheDeclaredOptionsBlock(t *testing.T) {
	pack := optionProviderPack(t)
	got := compose(t, nil, []*Pack{pack})
	// The null survives INTO the table: it is what tells the reader the option is
	// declared with no default (OQ-CS7), and collapsing it to an absent key would turn a
	// declared surface into no surface at all.
	want := `{"zai": {"endpoints": {"anthropic": {"base_url": "https://api.z.ai/api/anthropic"}}, ` +
		`"options": {"model": "default", "thinking": null}}}`
	if s := dump(t, got); s != want {
		t.Errorf("pack-only composition:\n got %s\nwant %s", s, want)
	}

	// The user overrides a default per option, and the option they do not touch keeps the
	// pack's spelling — declared-no-default included.
	user := userProviders(t, `{"zai":{"options":{"model":"fast"}}}`)
	if s := dump(t, compose(t, user, []*Pack{pack})); !strings.Contains(s, `"options": {"model": "fast", "thinking": null}`) {
		t.Errorf("an option default should compose per field, got %s", s)
	}

	// THE ONE NULL THAT IS NOT A DELETE (OQ-CS7): under `options`, a null lowers the
	// default and keeps the option declared. Everywhere else in this entry the same
	// syntax removes the key — pinned by the test above this one — and applying that rule
	// here would silently UNDECLARE an option the user only asked to un-default, so a
	// profile naming it would then be refused as undeclared.
	user = userProviders(t, `{"zai":{"options":{"model":null}}}`)
	if s := dump(t, compose(t, user, []*Pack{pack})); !strings.Contains(s, `"options": {"model": null, "thinking": null}`) {
		t.Errorf("a null option default must keep the option declared, got %s", s)
	}

	// The map itself is still an ordinary field: null deletes the whole block.
	user = userProviders(t, `{"zai":{"options":null}}`)
	if s := dump(t, compose(t, user, []*Pack{pack})); strings.Contains(s, "options") {
		t.Errorf("a null over the whole options block must delete it, got %s", s)
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
// user override of one field replaces it and the agent's env derive reads the same value
// the config derives would see.
func TestComposeProvidersCarriesARegionalProviderFacts(t *testing.T) {
	pack := shippedBedrockPack(t)
	want := `{"bedrock": {"region": "us-east-1"}}`
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
	// requirement left with it. The shape D4 measured refusing before the rule moved —
	// packs: ["claude"] with providers: {"bedrock": null}, which came out as "pack claude
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
