package packload

// credentialscope_test.go pins THE CREDENTIAL GATE's own rule (ScopeCredentials,
// docs/design/provider-credential-scope.md OQ-CN1/CN2): which hydrated values are whose, the
// env derive's hydration narrowed to the agent's own provider, the derive view of a
// credential list, and the disclosure's wording. The vehicles that deliver this answer are
// pinned where they write: internal/cli/run (container, macos-user), internal/cli (host).

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// scopePack loads a manifest (and an optional derive.lua) from a temp tree, the way a
// launch loads a staged pack, so its derive runs.
func scopePack(t *testing.T, name, manifest, derive string) *Pack {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, root, manifest)
	if derive != "" {
		if err := os.WriteFile(filepath.Join(root, "derive.lua"), []byte(derive), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, problems := LoadDir(root, name)
	if len(problems) != 0 {
		t.Fatalf("loading %s: %v", name, problems)
	}
	return p
}

// twoProviders is a composed table with a single-key provider and a multi-route one, as a
// launch composes them: zai names one variable, bedrock-like `routes` lists two.
func twoProviders(t *testing.T) *jsonx.OrderedMap {
	return userProviders(t, `{
	  "zai":{"api_key_env_name":"ZAI_API_KEY",
	    "endpoints":{"openai":{"base_url":"https://api.z.ai/v4","wire_api":"openai-chat-completions"}}},
	  "cerebras":{"api_key_env_name":["CEREBRAS_API_KEY"],
	    "endpoints":{"openai":{"base_url":"https://api.cerebras.ai/v1","wire_api":"openai-chat-completions"}}},
	  "routes":{"api_key_env_name":["ROUTE_BEARER","ROUTE_PAIR_ID"]}}`)
}

func hydrated(pairs ...string) *jsonx.OrderedMap {
	m := jsonx.NewOrderedMap()
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i], pairs[i+1])
	}
	return m
}

// The split: a claimed value goes to the agents whose provider claims it, an unclaimed one
// to everyone, and a claimed one nobody's provider claims to no process at all.
func TestScopeCredentialsSplitsClaimedFromShared(t *testing.T) {
	scope, err := ScopeCredentials(ScopeInput{
		Providers: twoProviders(t),
		Profiles:  map[string]string{"pi": "zai-profile", "codex": "routes-profile"},
		Resolved: map[string]ResolvedProfile{
			"zai-profile": {Provider: "zai"}, "routes-profile": {Provider: "routes"},
		},
		EnvSources: hydrated("ZAI_API_KEY", "z", "CEREBRAS_API_KEY", "c",
			"ROUTE_BEARER", "b", "GH_TOKEN", "g"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := scope.SharedEnvSources().Keys(); strings.Join(got, ",") != "GH_TOKEN" {
		t.Errorf("shared env_sources = %v, want only the unclaimed GH_TOKEN", got)
	}
	if got := scope.Agent("pi").EnvSources.Keys(); strings.Join(got, ",") != "ZAI_API_KEY" {
		t.Errorf("pi's own env_sources = %v, want its provider's key only", got)
	}
	if got := scope.Agent("codex").EnvSources.Keys(); strings.Join(got, ",") != "ROUTE_BEARER" {
		t.Errorf("codex's own env_sources = %v, want the listed route its provider claims", got)
	}
	if scope.DeliversEnvSource("CEREBRAS_API_KEY") {
		t.Error("a key claimed by a provider NO agent selected must reach no process")
	}
	if !scope.DeliversEnvSource("GH_TOKEN") || !scope.DeliversEnvSource("ZAI_API_KEY") {
		t.Error("an unclaimed value, and a selected provider's key, are delivered")
	}
	if got := strings.Join(scope.SelectedProviders(), ","); got != "routes,zai" {
		t.Errorf("SelectedProviders = %s, want the two providers some agent selected", got)
	}
	if got := strings.Join(scope.EnvSourcesFor("claude").Keys(), ","); got != "GH_TOKEN" {
		t.Errorf("an agent with no profile receives the shared set only, got %s", got)
	}
}

// OQ-CN2's rendered-config half: the env derive's copy of the table carries the api_key of
// the agent's OWN provider and no other. The probe derive reports every api_key it can see,
// so a gate that handed AgentEnv the ungated lookup would put cerebras's key in pi's
// environment and fail here.
func TestTheEnvDeriveSeesOnlyTheAgentsOwnProvidersKey(t *testing.T) {
	probe := scopePack(t, "probe", `{"name":"probe","contributes":[
	  {"kind":"program","bin":"probe","via":"npm","package":"@acme/probe","protocols":["openai"]}]}`,
		`yolo.env("probe", function(ctx)
  local out = {}
  for name, p in pairs(ctx.providers) do
    if p.api_key then out["SAW_" .. string.upper(name)] = p.api_key end
  end
  return out
end)`)
	scope, err := ScopeCredentials(ScopeInput{
		Packs:      []*Pack{probe},
		Providers:  twoProviders(t),
		Profiles:   map[string]string{"probe": "zai-profile"},
		Resolved:   map[string]ResolvedProfile{"zai-profile": {Provider: "zai"}},
		EnvSources: hydrated("ZAI_API_KEY", "z", "CEREBRAS_API_KEY", "c"),
		Fallback: func(name string) (string, bool) {
			if name == "CEREBRAS_API_KEY" {
				return "c-from-shell", true // the launch environment is gated too
			}
			return "", false
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	for _, v := range scope.Agent("probe").Shape {
		seen = append(seen, v.Key+"="+v.Value)
	}
	if strings.Join(seen, ",") != "SAW_ZAI=z" {
		t.Errorf("probe's env derive saw %v, want only its own provider's key (SAW_ZAI=z)", seen)
	}

	// A provider naming its one variable as a LIST OF ONE is hydrated like the string form:
	// the derive's view (ProvidersForDerive) is what hydration reads the pointer off.
	scope, err = ScopeCredentials(ScopeInput{
		Packs:      []*Pack{probe},
		Providers:  twoProviders(t),
		Profiles:   map[string]string{"probe": "cerebras-profile"},
		Resolved:   map[string]ResolvedProfile{"cerebras-profile": {Provider: "cerebras"}},
		EnvSources: hydrated("ZAI_API_KEY", "z", "CEREBRAS_API_KEY", "c"),
	})
	if err != nil {
		t.Fatal(err)
	}
	seen = nil
	for _, v := range scope.Agent("probe").Shape {
		seen = append(seen, v.Key+"="+v.Value)
	}
	if strings.Join(seen, ",") != "SAW_CEREBRAS=c" {
		t.Errorf("probe on cerebras (a one-element key list) saw %v, want SAW_CEREBRAS=c", seen)
	}
}

// The derive view of a credential list: one name — a string or a list of one — is the
// variable a derive points the agent at, several are none, and the relayed table itself is
// never rewritten.
func TestProvidersForDeriveProjectsTheOneKeyVariable(t *testing.T) {
	table := twoProviders(t)
	view := ProvidersForDerive(table)
	for name, want := range map[string]string{"zai": "ZAI_API_KEY", "cerebras": "CEREBRAS_API_KEY", "routes": ""} {
		entry := providerEntry(view, name)
		got, present := entry.Get("api_key_env_name")
		if want == "" {
			if present {
				t.Errorf("%s lists several variables, so a derive must see none, got %#v", name, got)
			}
			continue
		}
		if got != want {
			t.Errorf("%s: derive view api_key_env_name = %#v, want %q", name, got, want)
		}
	}
	if names := CredentialEnvNames(providerEntry(table, "routes")); len(names) != 2 {
		t.Errorf("the relayed table must keep the whole list, got %v", names)
	}
}

// The shipped bedrock declaration lists its ruled credential routes (OQ-SSO7: a bearer, a
// static key pair, an SSO pointer), so the gate can scope each to the agent that selected
// bedrock; a one-key pack keeps its single string.
func TestTheShippedBedrockProviderClaimsItsCredentialRoutes(t *testing.T) {
	loaded, problems := MaterializeEmbedded(officialpacks.FS, t.TempDir())
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	table, err := ComposeProviders(nil, loaded)
	if err != nil {
		t.Fatal(err)
	}
	// The EXACT CN-D2 list, as a set: OQ-SSO7's three routes as the clients spell them plus
	// the other two of pi's four Bedrock spellings. Dropping AWS_PROFILE, say, would leave it
	// shared to every process, and pi's amazon-bedrock authenticates from it — the §2.1 leak
	// reopened through that one name.
	got := append([]string(nil), CredentialEnvNames(providerEntry(table, "bedrock"))...)
	sort.Strings(got)
	want := []string{"AWS_ACCESS_KEY_ID", "AWS_BEARER_TOKEN_BEDROCK",
		"AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_PROFILE", "AWS_SECRET_ACCESS_KEY",
		"AWS_SESSION_TOKEN"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("bedrock claims %v, want exactly %v (CN-D2)", got, want)
	}
	if KeyEnvName(providerEntry(table, "bedrock")) != "" {
		t.Error("bedrock lists several routes, so it must point no agent at any one of them")
	}
	if got, _ := providerEntry(table, "zai").Get("api_key_env_name"); got != "ZAI_API_KEY" {
		t.Errorf("zai's one variable must compose as the string it always was, got %#v", got)
	}
}

// The disclosure names variables, providers and recipients — never a value — and says
// "withheld" for a credential no agent selected.
func TestCredentialScopeDisclosureNamesOnly(t *testing.T) {
	scope, err := ScopeCredentials(ScopeInput{
		Providers:  twoProviders(t),
		Profiles:   map[string]string{"pi": "zai-profile"},
		Resolved:   map[string]ResolvedProfile{"zai-profile": {Provider: "zai"}},
		EnvSources: hydrated("ZAI_API_KEY", "secret-z", "ROUTE_BEARER", "secret-b", "GH_TOKEN", "g"),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(scope.Disclosure(), "\n")
	for _, want := range []string{"ZAI_API_KEY (provider zai): pi only",
		"ROUTE_BEARER (provider routes): withheld"} {
		if !strings.Contains(got, want) {
			t.Errorf("disclosure missing %q:\n%s", want, got)
		}
	}
	for _, leak := range []string{"secret-z", "secret-b", "GH_TOKEN"} {
		if strings.Contains(got, leak) {
			t.Errorf("disclosure carries %q:\n%s", leak, got)
		}
	}
	quiet, _ := ScopeCredentials(ScopeInput{EnvSources: hydrated("GH_TOKEN", "g")})
	if lines := quiet.Disclosure(); lines != nil {
		t.Errorf("a launch that hydrated no claimed credential discloses nothing, got %v", lines)
	}
}
