package packload

// builtinproviders_test.go pins core's three readings of `built_in_providers`
// (docs/design/pi-codex-provider-shadowing.md OQ-3, ruled 2026-10-05: yolo writes no model entry
// over any provider an agent has built in, and the agent uses its own list): the per-agent view
// every derive's ctx carries (BuiltInProvidersFor), the launch's profile line (profileReach), and
// the agent environment (AgentEnv, through the credential gate every vehicle delivers from).

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// THE VIEW IS THE AGENT'S OWN PACK'S, read by bin ownership: a declared name maps to itself, a
// plan maps a yolo provider to the agent's own provider for it, with the key that provider reads,
// and an agent whose pack declares nothing (claude) has no view, which every derive reads as the
// world before the ruling.
func TestBuiltInProvidersForReadsTheAgentsOwnPack(t *testing.T) {
	packs := embeddedNamed(t, "pi", "opencode", "omp", "claude")
	for _, tc := range []struct {
		agent, provider string
		want            luahook.BuiltInProvider
	}{
		{"pi", "zai", luahook.BuiltInProvider{ID: "zai"}},
		{"pi", "openai-codex", luahook.BuiltInProvider{ID: "openai-codex"}},
		{"oh-omp", "kilo", luahook.BuiltInProvider{ID: "kilo"}},
		{"opencode", "zai", luahook.BuiltInProvider{ID: "zai-coding-plan", APIKeyEnvName: "ZHIPU_API_KEY"}},
		{"opencode", "openai-codex", luahook.BuiltInProvider{ID: "openai"}},
	} {
		got, ok := BuiltInProviderFor(packs, tc.agent, tc.provider)
		if !ok || got != tc.want {
			t.Errorf("%s's own provider for %s = %+v (built in %v), want %+v", tc.agent, tc.provider, got, ok, tc.want)
		}
	}
	for _, tc := range []struct{ agent, provider string }{
		{"pi", "kilo"}, {"pi", "llamacpp"}, {"opencode", "llamacpp"}, {"claude", "zai"}, {"nobody", "zai"},
	} {
		if got, ok := BuiltInProviderFor(packs, tc.agent, tc.provider); ok {
			t.Errorf("%s has %s built in (%+v), want not", tc.agent, tc.provider, got)
		}
	}
	if BuiltInProvidersFor(packs, "claude") != nil {
		t.Error("claude's pack declares no built_in_providers, so it has no view")
	}
}

// THE PROFILE LINE SAYS THE AGENT REACHES IT THROUGH ITS OWN CLIENT, naming the agent's own id for
// the plan, where it used to name the provider's endpoint, which the agent no longer uses. claude
// declares no built-in providers, so it still reaches zai on its anthropic endpoint.
func TestTheProfileLineSaysAnAgentReachesItsOwnProvider(t *testing.T) {
	d := disclose(t, []string{"pi", "opencode", "omp", "claude", "zai"},
		map[string]string{"pi": "zai", "opencode": "zai", "oh-omp": "zai", "claude": "zai"}, nil)
	for agent, own := range map[string]string{"pi": "zai", "opencode": "zai-coding-plan", "oh-omp": "zai"} {
		r := reachOf(t, d, agent)
		if want := `through its own "` + own + `" client, with its own model list`; r.Route != want || len(r.Warnings) != 0 {
			t.Errorf("%s on zai: %+v, want route %q and no warning", agent, r, want)
		}
	}
	if r := reachOf(t, d, "claude"); r.Route != `on its "anthropic" endpoint` {
		t.Errorf("claude on zai: %+v, want its anthropic endpoint", r)
	}
	if !strings.Contains(d.Line(), `opencode → provider "zai", through its own "zai-coding-plan" client`) {
		t.Errorf("the line must say opencode reaches zai through its own coding-plan client: %s", d.Line())
	}
}

// A PROFILE ON A PLAN THE AGENT HAS NO PROVIDER OF ITS OWN FOR SAYS SO, with the next step: the
// agent has the name built in for another plan (a null plan), so yolo writes it no model entry and
// nothing the profile configures reaches the agent's own client. No shipped pack declares one, so
// the agent is a fixture's.
func TestAProfileOnAPlanTheAgentHasNoProviderOfItsOwnForSaysSo(t *testing.T) {
	acme := writePack(t, "acme", "acme", `{"name": "acme", "contributes": [
	  {"kind": "program", "bin": "acme", "via": "npm", "package": "@acme/acme", "protocols": ["openai"],
	   "built_in_providers": {"names": ["zai"], "plans": {"zai": null}}}]}`)
	packs := append([]*Pack{acme}, embeddedNamed(t, "zai")...)
	table := map[string]string{"acme": "zai"}
	providers, resolved, _ := launchSelection(t, packs, nil, nil, table)
	d := ProfileDisclosures(ProfileDisclosureInput{Table: table, Packs: packs,
		Resolved: resolved, Providers: providers})
	r := reachOf(t, d[0], "acme")
	if r.Route != "" || len(r.Warnings) != 1 {
		t.Fatalf("acme on zai: %+v, want no route and one warning", r)
	}
	for _, want := range []string{
		`profile "zai" reaches nothing for acme`,
		`acme has a provider of its own named "zai" for another plan and none for this profile's`,
		"yolo writes no model entry over a provider an agent has built in",
		"Select a profile whose provider acme reaches (`-p acme=<name>`), or none for acme",
	} {
		if !strings.Contains(r.Warnings[0], want) {
			t.Errorf("the warning lacks %q:\n%s", want, r.Warnings[0])
		}
	}
	if !strings.Contains(d[0].Line(), `acme → provider "zai", which it cannot use here (below)`) {
		t.Errorf("the line must say acme cannot use zai: %s", d[0].Line())
	}
}

// THE AGENT ENVIRONMENT ON A BUILT-IN PROVIDER, through the credential gate every vehicle delivers
// from: opencode's own zai-coding-plan reads ZHIPU_API_KEY, so zai's key reaches opencode under
// that name too; and no tier variable names a model from yolo's list for an agent on its own
// provider, so pi on zai composes none and removes the ones another provider of the table names,
// while claude, which declares no built-in providers, keeps zai's tier.
func TestTheGateRelaysThePlansKeyAndNoTierOnABuiltInProvider(t *testing.T) {
	packs := embeddedNamed(t, "pi", "opencode", "claude", "zai", "llamacpp")
	providers := compose(t, userProviders(t, `{"zai": {"models": {"fast": "glm-5.3-flash"}}}`), packs)
	resolved, err := ResolveProfiles(packs, nil, providers)
	if err != nil {
		t.Fatal(err)
	}
	sources := jsonx.NewOrderedMap()
	sources.Set("ZAI_API_KEY", "tok-zai")
	scope, err := ScopeCredentials(ScopeInput{Packs: packs, Providers: providers, Resolved: resolved,
		EnvSources: sources, Profiles: map[string]string{"opencode": "zai", "pi": "zai", "claude": "zai"}})
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := scope.DeliveredTo("opencode", "ZHIPU_API_KEY"); !ok || v != "tok-zai" {
		t.Errorf("opencode on zai carries ZHIPU_API_KEY = %q (%v), want zai's key under the name its "+
			"own zai-coding-plan reads", v, ok)
	}
	if v, ok := scope.DeliveredTo("pi", "ZHIPU_API_KEY"); ok {
		t.Errorf("pi, whose own zai reads ZAI_API_KEY, was handed ZHIPU_API_KEY = %q", v)
	}
	for _, agent := range []string{"pi", "opencode"} {
		roles := roleVars(scope.Agent(agent).Shape)
		if strings.Contains(roles, "=") {
			t.Errorf("%s on its own zai carries a tier naming yolo's list: %q", agent, roles)
		}
		if !strings.Contains(roles, "-YOLO_MODEL_FAST") {
			t.Errorf("%s on its own zai keeps another agent's fast tier: %q, want it removed", agent, roles)
		}
	}
	if roles := roleVars(scope.Agent("claude").Shape); !strings.Contains(roles, "YOLO_MODEL_FAST=zai/glm-5.3-flash") {
		t.Errorf("claude on zai, which declares no built-in providers, lost zai's tier: %q", roles)
	}
}
