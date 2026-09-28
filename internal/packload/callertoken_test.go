package packload

// callertoken_test.go pins the composition half of a pack service's caller token
// (docs/reference/wire-bridge.md WB-D18): an address a pack's own service serves names that
// service's caller token as its credential, the derive's table resolves it into the endpoint's
// own api_key, the credential gate answers it before any env_sources entry of the same name, a
// via route's derive input names it, and the real claude derive sends it — never the provider's
// key and never nothing, which would let claude's saved login's OAuth bearer go to the bridge.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// bridgeTokenVar is the caller token variable servicedAdapterPack's service gets.
var bridgeTokenVar = paths.ServiceCallerTokenEnv("bridge-daemon")

const aCallerToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func endpointCredentialEnv(t *testing.T, providers *jsonx.OrderedMap, provider, protocol string) (string, bool) {
	t.Helper()
	entry, _ := providers.Get(provider)
	eps, _ := asOrdered(t, entry).Get("endpoints")
	m, ok := eps.(*jsonx.OrderedMap)
	if !ok {
		return "", false
	}
	ep, _ := m.Get(protocol)
	em, ok := ep.(*jsonx.OrderedMap)
	if !ok {
		return "", false
	}
	v, ok := em.Get("api_key_env_name")
	s, _ := v.(string)
	return s, ok
}

// A service-served address names its service's caller token; a service-less adapter (a remote
// gateway) names none, so an agent sent there keeps the provider's own credential.
func TestAServiceServedAddressNamesItsServicesCallerToken(t *testing.T) {
	if bridgeTokenVar != "YOLO_SERVICE_BRIDGE_DAEMON_TOKEN" {
		t.Fatalf("caller token variable = %q", bridgeTokenVar)
	}
	agent := protocolAgentPack(t, `,"protocols":["anthropic"]`)
	vendor := providerPack(t, `,"endpoints":{"openai":{"base_url":"https://vendor.example/v1"}}`)

	providers, err := ComposeProviders(nil, []*Pack{agent, vendor, servicedAdapterPack(t)})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := endpointCredentialEnv(t, providers, "p", "anthropic"); got != bridgeTokenVar {
		t.Errorf("the bridge-served anthropic endpoint names credential %q, want %q", got, bridgeTokenVar)
	}
	if _, has := endpointCredentialEnv(t, providers, "p", "openai"); has {
		t.Error("the provider's own endpoint must not name the service's token")
	}

	providers, err = ComposeProviders(nil, []*Pack{agent, vendor,
		adapterPack(t, "openai", "anthropic", "https://gw.example/a")})
	if err != nil {
		t.Fatal(err)
	}
	if got, has := endpointCredentialEnv(t, providers, "p", "anthropic"); has {
		t.Errorf("a service-less adapter's address names credential %q; it has no service to demand one", got)
	}
}

// The real claude derive, over a bridged provider: ANTHROPIC_AUTH_TOKEN is the bridge's caller
// token, never the provider's key, and the "local" dummy — never absent — when the launch
// hydrated no token, since an absent ANTHROPIC_AUTH_TOKEN sends claude's saved login's OAuth
// bearer to the base URL (docs/design/agent-auth-modes.md §8.1).
func TestClaudeSendsTheBridgesCallerTokenNeverItsLoginOrTheProvidersKey(t *testing.T) {
	claude, vendor := realClaudePack(t),
		providerPack(t, `,"endpoints":{"openai":{"base_url":"https://vendor.example/v1"}}`)
	packs := []*Pack{claude, vendor, servicedAdapterPack(t)}
	providers, err := ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveProfiles(packs, nil, providers)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		lookup map[string]string
		want   string
	}{
		{"hydrated", map[string]string{"P_KEY": "tok-9", bridgeTokenVar: aCallerToken}, aCallerToken},
		{"no token hydrated", map[string]string{"P_KEY": "tok-9"}, "local"},
	} {
		vars, err := AgentEnv(packs, providers, map[string]string{"claude": "sel"}, "claude", "sel",
			func(n string) (string, bool) { v, ok := tc.lookup[n]; return v, ok },
			WithResolvedProfiles(resolved))
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, v := range vars {
			got[v.Key] = v.Value
		}
		if got["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:8214" {
			t.Errorf("%s: ANTHROPIC_BASE_URL = %q, want the bridge", tc.name, got["ANTHROPIC_BASE_URL"])
		}
		if tok, set := got["ANTHROPIC_AUTH_TOKEN"]; !set || tok != tc.want {
			t.Errorf("%s: ANTHROPIC_AUTH_TOKEN = %q (set %v), want %q — never the provider's key "+
				"tok-9, and never absent", tc.name, tok, set, tc.want)
		}
	}
}

// The credential gate answers a caller token before an env_sources entry or the launch
// environment spelling the same name, so no user value can stand in for what the service
// demands.
func TestTheCredentialGateAnswersACallerTokenFirst(t *testing.T) {
	probe := scopePack(t, "probe", `{"name":"probe","contributes":[
	  {"kind":"program","bin":"probe","via":"npm","package":"@acme/probe","protocols":["openai"]}]}`,
		`yolo.env("probe", function(ctx)
  local out = {}
  for name, p in pairs(ctx.providers) do
    local ep = p.endpoints and p.endpoints.openai
    if ep and ep.api_key then out["SAW_" .. string.upper(name)] = ep.api_key end
  end
  return out
end)`)
	providers := userProviders(t, `{"zai":{"api_key_env_name":"ZAI_API_KEY",
	  "endpoints":{"openai":{"base_url":"http://127.0.0.1:8216","api_key_env_name":"`+bridgeTokenVar+`"}}}}`)
	scope, err := ScopeCredentials(ScopeInput{
		Packs:        []*Pack{probe},
		Providers:    providers,
		Profiles:     map[string]string{"probe": "zp"},
		Resolved:     map[string]ResolvedProfile{"zp": {Provider: "zai"}},
		EnvSources:   hydrated("ZAI_API_KEY", "z", bridgeTokenVar, "from-env-sources"),
		Fallback:     func(string) (string, bool) { return "from-shell", true },
		CallerTokens: map[string]string{bridgeTokenVar: aCallerToken},
	})
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	for _, v := range scope.Agent("probe").Shape {
		seen = append(seen, v.Key+"="+v.Value)
	}
	if strings.Join(seen, ",") != "SAW_ZAI="+aCallerToken {
		t.Errorf("the derive saw %v, want the endpoint's own credential to be the caller token", seen)
	}
}

// A via route's derive input names the via service's caller token, and only while the route
// is live: no via, a via whose service is not in the launch, or a service that serves no via
// address all give "".
func TestViaAPIKeyEnvNameForIsTheViaServicesCallerToken(t *testing.T) {
	packs := []*Pack{viaServicePack(t, "wire-bridge", "http://127.0.0.1:8216")}
	live := ResolvedProfile{Provider: "zai", Via: "wire-bridge", ViaBase: "http://127.0.0.1:8216"}
	if got := ViaAPIKeyEnvNameFor(packs, live, "pi"); got != "YOLO_SERVICE_WIRE_BRIDGE_TOKEN" {
		t.Errorf("live via = %q, want YOLO_SERVICE_WIRE_BRIDGE_TOKEN", got)
	}
	for name, tc := range map[string]struct {
		packs []*Pack
		r     ResolvedProfile
	}{
		"no via":                 {packs, ResolvedProfile{Provider: "zai"}},
		"service not in launch":  {packs, ResolvedProfile{Provider: "zai", Via: "wire-bridge"}},
		"service serves no via":  {[]*Pack{viaServicePack(t, "wire-bridge", "")}, live},
		"another pack's service": {[]*Pack{viaServicePack(t, "other", "http://127.0.0.1:8216")}, live},
	} {
		if got := ViaAPIKeyEnvNameFor(tc.packs, tc.r, "pi"); got != "" {
			t.Errorf("%s: %q, want none", name, got)
		}
	}
}
