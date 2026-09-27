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

// Left out at a notch that runs no pack service, kept everywhere else, and a service-less
// adapter kept at both.
func TestWithoutServiceAdaptationsComposesNoServedAddress(t *testing.T) {
	agent := protocolAgentPack(t, `,"protocols":["anthropic"]`)
	vendor := providerPack(t, `,"endpoints":{"openai":{"base_url":"https://vendor.example/v1"}}`)
	for _, tc := range []struct {
		adapter *Pack
		opts    []ComposeOption
		want    string
	}{
		{servicedAdapterPack(t), nil, "http://127.0.0.1:8214"},
		{servicedAdapterPack(t), []ComposeOption{WithoutServiceAdaptations()}, ""},
		{adapterPack(t, "openai", "anthropic", "https://gw.example/a"), []ComposeOption{WithoutServiceAdaptations()},
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
	providers, err := ComposeProviders(nil, packs, WithoutServiceAdaptations())
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
	for _, want := range []string{`"bridge-daemon" service`, "http://127.0.0.1:8214", "inside a jail"} {
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
