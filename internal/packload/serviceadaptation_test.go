package packload

// serviceadaptation_test.go pins the composition and the gate for a notch that runs NO pack
// service (docs/design/credential-sources-separation.md ES-D18): an adaptation whose own pack
// serves it with a `service` (packs/wire-bridge's shape) composes no address there, while a
// service-less adapter (a remote gateway, a proxy the user runs) composes as it always has;
// and a pairing only the left-out adaptation would resolve refuses as *UnservedAdapterError.
// The host's call sites are pinned in internal/cli (hostbridgeadapter_test.go).

import (
	"errors"
	"strings"
	"testing"
)

// servicedAdapterPack is packs/wire-bridge's shape: the adapter plus the service whose daemon
// serves its address.
func servicedAdapterPack(t *testing.T) *Pack {
	t.Helper()
	return &Pack{Name: "bridge", Decl: declFrom(t, `{"contributes":[
	  {"kind":"adapter","adapts":{"from":"openai","to":"anthropic"},"address":"http://127.0.0.1:8214"},
	  {"kind":"service","name":"bridge-daemon","jail_daemon":{"cmd":["yolo-jaild","bridge"]}}]}`)}
}

func TestServiceAdaptationsAreTheOnesAPacksOwnServiceServes(t *testing.T) {
	packs := []*Pack{servicedAdapterPack(t), adapterPack(t, "openai-responses", "anthropic", "https://gw.example/a")}
	got := ServiceAdaptations(packs, map[string]string{"openai->anthropic": "http://127.0.0.1:9214"})
	if len(got) != 1 || got[0].Pack != "bridge" || got[0].Service != "bridge-daemon" ||
		got[0].Address != "http://127.0.0.1:9214" {
		t.Errorf("ServiceAdaptations = %+v, want bridge's one, served by bridge-daemon, at the "+
			"user's override", got)
	}
	for _, a := range Adaptations(packs) {
		if a.Pack == "gateway" && a.Service != "" {
			t.Errorf("a service-less adapter names no service: %+v", a)
		}
	}
}

// Left out at a notch that does not serve the adapter's service (the host, macos-user, or a
// container launch whose payload lacks it), kept where it is served and when composing as
// declared, and a service-less adapter kept at every notch.
func TestWithServedComposesOnlyAServedAddress(t *testing.T) {
	agent := protocolAgentPack(t, `,"protocols":["anthropic"]`)
	vendor := providerPack(t, `,"endpoints":{"openai":{"base_url":"https://vendor.example/v1"}}`)
	for _, tc := range []struct {
		adapter *Pack
		opts    []ComposeOption
		want    string
	}{
		{servicedAdapterPack(t), nil, "http://127.0.0.1:8214"},
		{servicedAdapterPack(t), []ComposeOption{WithServed(NothingServed())}, ""},
		{servicedAdapterPack(t), []ComposeOption{WithServed(ServedInJail([]string{"bridge-daemon"}))},
			"http://127.0.0.1:8214"},
		{servicedAdapterPack(t), []ComposeOption{WithServed(ServedInJail(nil))}, ""},
		{adapterPack(t, "openai", "anthropic", "https://gw.example/a"), []ComposeOption{WithServed(NothingServed())},
			"https://gw.example/a"},
	} {
		providers, err := ComposeProviders(nil, []*Pack{agent, vendor, tc.adapter}, tc.opts...)
		if err != nil {
			t.Fatal(err)
		}
		entry, _ := providers.Get("p")
		if got := endpointURL(t, asOrdered(t, entry), "anthropic"); got != tc.want {
			t.Errorf("adapter %s, %d options: anthropic endpoint = %q, want %q", tc.adapter.Name, len(tc.opts), got, tc.want)
		}
	}
}

// The gate refuses the pairing only the left-out adaptation would resolve, and says why when
// told what was left out; without that it is the ordinary refusal.
func TestTheGateNamesAnUnservedAdapter(t *testing.T) {
	agent := protocolAgentPack(t, `,"protocols":["anthropic"]`)
	vendor := providerPack(t, `,"endpoints":{"openai":{"base_url":"https://vendor.example/v1"}}`)
	bridge := servicedAdapterPack(t)
	packs := []*Pack{agent, vendor, bridge}
	providers, err := ComposeProviders(nil, packs, WithServed(NothingServed()))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveProfiles(packs, nil, providers)
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(string) (string, bool) { return "", false }
	_, err = AgentEnv(packs, providers, map[string]string{"claude": "sel"}, "claude", "sel", lookup,
		WithResolvedProfiles(resolved), WithUnservedAdaptations(ServiceAdaptations(packs, nil)))
	var unserved *UnservedAdapterError
	if !errors.As(err, &unserved) {
		t.Fatalf("err = %v, want *UnservedAdapterError", err)
	}
	if unserved.Agent != "claude" || unserved.Provider != "p" || unserved.Adaptation.Pack != "bridge" {
		t.Errorf("the refusal names the pairing and the adapter: %+v", unserved)
	}
	for _, want := range []string{`"bridge-daemon" service`, "http://127.0.0.1:8214", "a daemon this launch does not run"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Error() must say %q: %v", want, err)
		}
	}
	_, err = AgentEnv(packs, providers, map[string]string{"claude": "sel"}, "claude", "sel", lookup,
		WithResolvedProfiles(resolved))
	if err == nil || errors.As(err, &unserved) {
		t.Errorf("without the unserved list the pairing still refuses, as the ordinary refusal: %v", err)
	}
	// ScopeCredentials hands its input's list to every derive's gate.
	_, err = ScopeCredentials(ScopeInput{Packs: packs, Providers: providers,
		Profiles: map[string]string{"claude": "sel"}, Resolved: resolved,
		UnservedAdaptations: ServiceAdaptations(packs, nil)})
	if !errors.As(err, &unserved) {
		t.Errorf("ScopeCredentials must pass UnservedAdaptations to the gate: %v", err)
	}
}

// At a notch that runs no pack service, an UNSELECTED pack's service adaptation is no remedy
// either: selecting the pack there composes no address (WithoutServiceAdaptations), so outcome
// 3's "Add it to `packs` and this pairing resolves" would send the user straight into the
// refusal above. Handed UnservedAdaptationsAt, the gate refuses the pairing once, as
// *UnservedAdapterError naming the unselected pack and the user's address override. The jail
// notch hands the gate nothing, so there outcome 3 still names the pack to add.
func TestAnUnselectedServiceAdaptationIsNoRemedyWhereNoServiceRuns(t *testing.T) {
	agent := protocolAgentPack(t, `,"protocols":["anthropic"]`)
	vendor := providerPack(t, `,"endpoints":{"openai":{"base_url":"https://vendor.example/v1"}}`)
	packs := []*Pack{agent, vendor}
	providers, err := ComposeProviders(nil, packs, WithServed(NothingServed()))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveProfiles(packs, nil, providers)
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(string) (string, bool) { return "", false }
	unservable := UnservedAdaptationsAt(packs, map[string]string{"openai->anthropic": "http://127.0.0.1:9214"}, NothingServed())
	_, err = AgentEnv(packs, providers, map[string]string{"claude": "sel"}, "claude", "sel", lookup,
		WithResolvedProfiles(resolved), WithUnservedAdaptations(unservable))
	var unserved *UnservedAdapterError
	if !errors.As(err, &unserved) {
		t.Fatalf("err = %v, want *UnservedAdapterError for the unselected wire-bridge", err)
	}
	if unserved.Adaptation.Pack != "wire-bridge" || unserved.Selected ||
		unserved.Adaptation.Address != "http://127.0.0.1:9214" {
		t.Errorf("the refusal names the unselected shipped pack at the user's override: %+v", unserved)
	}
	if strings.Contains(err.Error(), "Add it to `packs`") {
		t.Errorf("selecting the pack resolves nothing at this notch, so the refusal must not say to: %v", err)
	}

	_, err = AgentEnv(packs, providers, map[string]string{"claude": "sel"}, "claude", "sel", lookup,
		WithResolvedProfiles(resolved))
	if err == nil || errors.As(err, &unserved) ||
		!strings.Contains(err.Error(), "Pack \"wire-bridge\" adapts \"openai\" → \"anthropic\". Add it to `packs`") {
		t.Errorf("a notch that runs its packs' services still names the pack to add (outcome 3): %v", err)
	}
}

// UnservedAdaptationsAt, at a notch serving nothing, is the selected packs' service adaptations, then the unselected shipped
// packs', each at the user's override; a service-less adapter is in neither half.
func TestUnservedAdaptationsAtANotchServingNothingSpansSelectedAndUnselected(t *testing.T) {
	gateway := adapterPack(t, "openai-responses", "anthropic", "https://gw.example/a")
	got := UnservedAdaptationsAt([]*Pack{servicedAdapterPack(t), gateway},
		map[string]string{"openai->anthropic": "http://127.0.0.1:9214"}, NothingServed())
	byPack := map[string][]Adaptation{}
	for _, a := range got {
		byPack[a.Pack] = append(byPack[a.Pack], a)
	}
	if b := byPack["bridge"]; len(b) != 1 || b[0].Address != "http://127.0.0.1:9214" {
		t.Errorf("the selected service pack's adaptation, at the override: %+v", b)
	}
	if len(byPack["wire-bridge"]) == 0 {
		t.Errorf("the unselected shipped wire-bridge's adaptations are unservable too: %+v", got)
	}
	for _, a := range byPack["wire-bridge"] {
		if a.Service == "" {
			t.Errorf("only service adaptations are unservable: %+v", a)
		}
		if a.From == "openai" && a.Address != "http://127.0.0.1:9214" {
			t.Errorf("the override reaches an unselected pack's adaptation too: %+v", a)
		}
	}
	if len(byPack["gateway"]) != 0 {
		t.Errorf("a service-less adapter is servable wherever it is declared: %+v", byPack["gateway"])
	}
}
