package packload

// adapter_test.go pins OUTCOME 2 (docs/design/protocol-resolution.md §3): a pairing that
// has no common protocol resolves anyway when some selected pack declares the conversion,
// and the address it resolves to is the ADAPTER's own declaration.
//
// The measurement that matters is the SHIPPED one at the bottom: `packs/cerebras` used to
// hand-write yolo's internal loopback port into a provider manifest, and the composed table
// has to come out byte-identical now that the adapter writes it instead. Everything above
// is the rule that makes that true for a pack nobody shipped.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentenv"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// adapterPack declares one conversion and nothing else — the shape §3's table calls "a
// remote service yolo does not run": an adapter with no daemon, no service contribution and
// no `needs`. It is the case the rejected design (an adapter as a field on `service`) could
// not express at all, so it is the one the fixture uses.
func adapterPack(t *testing.T, from, to, address string) *Pack {
	t.Helper()
	return &Pack{Name: "gateway", Decl: declFrom(t, `{"contributes":[
	  {"kind":"adapter","adapts":{"from":"`+from+`","to":"`+to+`"},
	   "address":"`+address+`"}]}`)}
}

// OUTCOME 2 — the pairing resolves at the adapter's address, with no further configuration
// and nothing in the provider naming the adapter.
func TestADeclaredAdapterResolvesAnOtherwiseUnusablePairing(t *testing.T) {
	agent := protocolAgentPack(t, `,"protocols":["anthropic"]`)
	vendor := providerPack(t, `,"endpoints":{"openai":{"base_url":"https://vendor.example/v1"}}`)
	gateway := adapterPack(t, "openai", "anthropic", "https://gw.example/anthropic")
	packs := []*Pack{agent, vendor, gateway}
	providers, err := ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveProfiles(packs, nil, providers)
	if err != nil {
		t.Fatal(err)
	}
	vars, err := AgentEnv(packs, providers, map[string]string{"claude": "sel"}, "claude", "sel",
		func(string) (string, bool) { return "", false }, WithResolvedProfiles(resolved))
	if err != nil {
		t.Fatalf("a declared adapter must make the pairing resolve: %v", err)
	}
	if len(vars) != 1 || vars[0].Value != "https://gw.example/anthropic" {
		t.Errorf("vars = %#v, want the ADAPTER's address — the provider declares no "+
			"anthropic endpoint and must not have to", vars)
	}
}

// WITHOUT THE ADAPTER'S PACK, the very same declarations refuse. The two halves are the
// whole user story (OQ-PR3) and the resolver never closes the gap on the user's behalf:
// selecting a provider must not decide what runs in the jail.
func TestTheSamePairingRefusesWithoutTheAdaptersPack(t *testing.T) {
	agent := protocolAgentPack(t, `,"protocols":["anthropic"]`)
	vendor := providerPack(t, `,"endpoints":{"openai":{"base_url":"https://vendor.example/v1"}}`)
	if _, err := runAgentEnv(t, agent, vendor); err == nil {
		t.Fatal("with no adapter selected the pairing must refuse — an adapter that is not " +
			"in `packs` is not in the launch")
	}
}

// AN ADAPTER IS NEVER PREFERRED OVER A NATIVE ENDPOINT (§4.1). A provider that speaks the
// agent's wire itself keeps its own address even in a jail where the adapter is selected —
// the injection FILLS A HOLE and never overwrites.
func TestAnAdapterNeverOverwritesANativeEndpoint(t *testing.T) {
	agent := protocolAgentPack(t, `,"protocols":["anthropic"]`)
	vendor := providerPack(t, `,"endpoints":{"anthropic":{"base_url":"https://vendor.example/a"},`+
		`"openai":{"base_url":"https://vendor.example/v1"}}`)
	gateway := adapterPack(t, "openai", "anthropic", "https://gw.example/anthropic")
	providers, err := ComposeProviders(nil, []*Pack{agent, vendor, gateway})
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := providers.Get("p")
	if got := endpointURL(t, asOrdered(t, entry), "anthropic"); got != "https://vendor.example/a" {
		t.Errorf("anthropic endpoint = %q, want the provider's OWN address", got)
	}
}

// A USER'S EXPLICIT ADDRESS OUTRANKS THE ADAPTER, because the injection runs below the
// user layer and only fills a protocol nobody named. A user who wrote an address did not
// leave a hole.
func TestAUserAddressOutranksTheAdapter(t *testing.T) {
	agent := protocolAgentPack(t, `,"protocols":["anthropic"]`)
	vendor := providerPack(t, `,"endpoints":{"openai":{"base_url":"https://vendor.example/v1"}}`)
	gateway := adapterPack(t, "openai", "anthropic", "https://gw.example/anthropic")
	user := userProviders(t, `{"p":{"endpoints":{"anthropic":{"base_url":"http://127.0.0.1:9999"}}}}`)
	providers, err := ComposeProviders(user, []*Pack{agent, vendor, gateway})
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := providers.Get("p")
	if got := endpointURL(t, asOrdered(t, entry), "anthropic"); got != "http://127.0.0.1:9999" {
		t.Errorf("anthropic endpoint = %q, want the user's own address", got)
	}
}

// NOTHING IS COMPOSED FOR A WIRE NO SELECTED AGENT SPEAKS. The providers table is one table
// for every agent, so the injection is gated on the union of what this launch's agents
// declare — otherwise a jail of openai-speaking agents would carry an address pointing at a
// listener nobody started.
func TestNoAddressIsComposedForAWireNobodySpeaks(t *testing.T) {
	agent := protocolAgentPack(t, `,"protocols":["openai"]`)
	vendor := providerPack(t, `,"endpoints":{"openai":{"base_url":"https://vendor.example/v1"}}`)
	gateway := adapterPack(t, "openai", "anthropic", "https://gw.example/anthropic")
	providers, err := ComposeProviders(nil, []*Pack{agent, vendor, gateway})
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := providers.Get("p")
	if _, present := asOrdered(t, entry).Get("endpoints"); !present {
		t.Fatal("the provider lost its own endpoints")
	}
	if got := endpointURL(t, asOrdered(t, entry), "anthropic"); got != "" {
		t.Errorf("anthropic endpoint = %q, want none — no selected agent speaks that wire", got)
	}
}

// THE PACK THAT DECLARES IT IS NOT NAMED ANYWHERE IN THE RULE (P6). A third-party pack
// declaring the same pair resolves identically to the one yolo ships, which is the
// requirement rather than a nicety: an adapter mechanism that works better for the pack we
// ship is a special case wearing a vocabulary's clothes.
func TestAThirdPartyAdapterResolvesLikeTheShippedOne(t *testing.T) {
	agent := protocolAgentPack(t, `,"protocols":["anthropic"]`)
	vendor := providerPack(t, `,"endpoints":{"openai":{"base_url":"https://vendor.example/v1"}}`)
	mine := &Pack{Name: "my-own-proxy", Decl: declFrom(t, `{"contributes":[
	  {"kind":"adapter","adapts":{"from":"openai","to":"anthropic"},
	   "address":"http://127.0.0.1:7777"}]}`)}
	providers, err := ComposeProviders(nil, []*Pack{agent, vendor, mine})
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := providers.Get("p")
	if got := endpointURL(t, asOrdered(t, entry), "anthropic"); got != "http://127.0.0.1:7777" {
		t.Errorf("anthropic endpoint = %q, want the third-party pack's address — core may "+
			"not prefer, name or fall back to any particular adapter", got)
	}
}

// THE SHIPPED MEASUREMENT: claude beside cerebras and the bridge composes the exact entry
// cerebras used to hand-write. `grep -rn 8214 packs/` is step 4's done-condition and the
// number now lives in ONE manifest — the adapter's — so this is where the equality it
// replaced is checked.
func TestTheShippedBridgeComposesWhatCerebrasUsedToDeclare(t *testing.T) {
	var packs []*Pack
	for _, p := range Embedded() {
		switch p.Name {
		case "claude", "cerebras", "wire-bridge":
			packs = append(packs, p)
		}
	}
	if len(packs) != 3 {
		t.Fatalf("selected %d of the three embedded packs", len(packs))
	}
	providers, err := ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := providers.Get("cerebras")
	e := asOrdered(t, entry)
	if got := endpointURL(t, e, "anthropic"); got != "http://127.0.0.1:8214" {
		t.Errorf("cerebras anthropic endpoint = %q, want the bridge's declared address — "+
			"byte-identical to the URL the provider manifest used to carry", got)
	}
	if got := endpointURL(t, e, "openai"); got != "https://api.cerebras.ai/v1" {
		t.Errorf("cerebras openai endpoint = %q, want its own upstream untouched", got)
	}
}

// endpointURL reads endpoints.<protocol>.base_url off a composed entry, "" when absent.
func endpointURL(t *testing.T, entry *jsonx.OrderedMap, protocol string) string {
	t.Helper()
	v, ok := entry.Get("endpoints")
	if !ok {
		return ""
	}
	endpoints, ok := v.(*jsonx.OrderedMap)
	if !ok {
		return ""
	}
	ep, ok := endpoints.Get(protocol)
	if !ok {
		return ""
	}
	m, ok := ep.(*jsonx.OrderedMap)
	if !ok {
		return ""
	}
	u, _ := m.Get("base_url")
	s, _ := u.(string)
	return s
}

// OUTCOME 3 — the refusal NAMES THE PACK TO ADD. The two halves are the whole user story
// (OQ-PR3): with the adapter's pack absent the pairing refuses and says which pack to add;
// with it present it just works, with no flag, no endpoint to write and nothing naming the
// bridge. The resolver never closes the gap itself, because choosing a provider must not
// change what runs in your jail.
//
// THE SHIPPED CASE, deliberately: what makes the message discoverable is that it names a
// pack the user can actually select, and only the packs yolo ships can be named from a
// launch that did not select them.
func TestOutcomeThreeNamesThePackToAdd(t *testing.T) {
	var claude, cerebras *Pack
	for _, p := range Embedded() {
		switch p.Name {
		case "claude":
			claude = p
		case "cerebras":
			cerebras = p
		}
	}
	if claude == nil || cerebras == nil {
		t.Fatal("the embedded claude and cerebras packs are what this test measures")
	}
	_, err := runAgentEnvWithProfile(t, []*Pack{claude, cerebras}, "claude", "cerebras")
	if err == nil {
		t.Fatal("claude beside cerebras with no adapter selected must refuse")
	}
	for _, want := range []string{
		`provider "cerebras" speaks "openai"`,
		`agent "claude" speaks "anthropic"`,
		`Pack "wire-bridge" adapts "openai" → "anthropic"`,
		"Add it to `packs`",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must contain %q:\n%v", want, err)
		}
	}
}

// OUTCOME 4 KEEPS ITS OWN MESSAGE: when nothing yolo can see declares the conversion, the
// refusal must not pretend a remedy exists. The agent speaks a wire no shipped adapter
// produces, so there is no pack to name.
func TestOutcomeFourSaysThereIsNoRemedy(t *testing.T) {
	agent := protocolAgentPack(t, `,"protocols":["unobtainium"]`)
	vendor := providerPack(t, `,"endpoints":{"openai":{"base_url":"https://vendor.example/v1"}}`)
	_, err := runAgentEnv(t, agent, vendor)
	if err == nil {
		t.Fatal("a pairing nothing adapts must refuse")
	}
	if strings.Contains(err.Error(), "Add it to `packs`") {
		t.Errorf("outcome 4 must not offer a remedy it cannot name:\n%v", err)
	}
	if !strings.Contains(err.Error(), "Nothing this launch can see declares an adapter") {
		t.Errorf("outcome 4 must say the pairing has no adapter anywhere:\n%v", err)
	}
}

// A SELECTED ADAPTER IS NEVER REPORTED AS MISSING: the remedy list is the UNSELECTED packs
// alone, so a launch that already has the pack and still cannot resolve (a different pair)
// is never told to add what it has.
func TestASelectedPackIsNotNamedAsMissing(t *testing.T) {
	var claude, cerebras, bridge *Pack
	for _, p := range Embedded() {
		switch p.Name {
		case "claude":
			claude = p
		case "cerebras":
			cerebras = p
		case "wire-bridge":
			bridge = p
		}
	}
	got := UnselectedAdaptations([]*Pack{claude, cerebras, bridge})
	for _, a := range got {
		if a.Pack == "wire-bridge" {
			t.Errorf("a SELECTED pack appears in the unselected remedy list: %#v", a)
		}
	}
}

// runAgentEnvWithProfile is runAgentEnv over an explicit pack set and profile name.
func runAgentEnvWithProfile(t *testing.T, packs []*Pack, agent, profile string) ([]agentenv.Var, error) {
	t.Helper()
	providers, err := ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveProfiles(packs, nil, providers)
	if err != nil {
		t.Fatal(err)
	}
	return AgentEnv(packs, providers, map[string]string{agent: profile}, agent, profile,
		func(string) (string, bool) { return "", false }, WithResolvedProfiles(resolved))
}

// THE ADDRESS IS CONFIGURABLE WITH A DEFAULT (§6, build step 7). The pair stays the
// declaring pack's claim; what a user may move is the PORT, and the reason is
// backend-shaped: harmless on a container's private loopback, real on macos-user, where
// there is no network namespace and an adapter's ports are HOST ports.
func TestAUserOverridesTheAdaptersAddress(t *testing.T) {
	agent := protocolAgentPack(t, `,"protocols":["anthropic"]`)
	vendor := providerPack(t, `,"endpoints":{"openai":{"base_url":"https://vendor.example/v1"}}`)
	gateway := adapterPack(t, "openai", "anthropic", "https://gw.example/anthropic")
	providers, err := ComposeProviders(nil, []*Pack{agent, vendor, gateway},
		WithAdapterAddresses(map[string]string{
			AdapterKey("openai", "anthropic"): "http://127.0.0.1:9214",
		}))
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := providers.Get("p")
	if got := endpointURL(t, asOrdered(t, entry), "anthropic"); got != "http://127.0.0.1:9214" {
		t.Errorf("anthropic endpoint = %q, want the user's override", got)
	}
}

// AN OVERRIDE FOR A PAIR NOTHING DECLARES CHANGES NOTHING. The user table is a set of
// addresses for conversions, not a way to declare one: which pairs exist is a pack's claim,
// and an override that could conjure one would be a config key declaring an adapter.
func TestAnOverrideCannotDeclareAnAdaptation(t *testing.T) {
	agent := protocolAgentPack(t, `,"protocols":["anthropic"]`)
	vendor := providerPack(t, `,"endpoints":{"openai":{"base_url":"https://vendor.example/v1"}}`)
	providers, err := ComposeProviders(nil, []*Pack{agent, vendor},
		WithAdapterAddresses(map[string]string{
			AdapterKey("openai", "anthropic"): "http://127.0.0.1:9214",
		}))
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := providers.Get("p")
	if got := endpointURL(t, asOrdered(t, entry), "anthropic"); got != "" {
		t.Errorf("anthropic endpoint = %q, want none — no pack declares this conversion, and a "+
			"config entry is an address for one, never a declaration of one", got)
	}
}

// THE DEFAULT SURVIVES AN UNRELATED OVERRIDE: a table naming a different pair leaves this
// one at the address its pack declared.
func TestAnUnrelatedOverrideLeavesTheDefault(t *testing.T) {
	agent := protocolAgentPack(t, `,"protocols":["anthropic"]`)
	vendor := providerPack(t, `,"endpoints":{"openai":{"base_url":"https://vendor.example/v1"}}`)
	gateway := adapterPack(t, "openai", "anthropic", "https://gw.example/anthropic")
	providers, err := ComposeProviders(nil, []*Pack{agent, vendor, gateway},
		WithAdapterAddresses(map[string]string{
			AdapterKey("openai-responses", "anthropic"): "http://127.0.0.1:9215",
		}))
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := providers.Get("p")
	if got := endpointURL(t, asOrdered(t, entry), "anthropic"); got != "https://gw.example/anthropic" {
		t.Errorf("anthropic endpoint = %q, want the declared default", got)
	}
}
