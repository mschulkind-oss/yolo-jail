package packload

// modelroles_test.go pins the role environment (OQ-XM4, docs/research/extension-model-defaults.md)
// through the credential gate, ScopeCredentials, whose AgentDelivery.Shape is what every vehicle
// delivers: the per-agent env file, the macos-user session and the host exec. Deleting the
// ModelRoleVars call from AgentEnv fails every test here.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentenv"
)

// roleScope composes a gate over a table where tiered names three of the four aliases, plain
// names only `default` and bare names none, nobody naming `frontier`; alpha, beta and gamma
// select one each.
func roleScope(t *testing.T, packs []*Pack) *CredentialScope {
	t.Helper()
	scope, err := ScopeCredentials(ScopeInput{
		Packs: packs,
		Providers: userProviders(t, `{
		  "tiered":{"models":{"default":"t-main","fast":"t-mini","balanced":"t-mid","t-main":"t-main"}},
		  "plain":{"models":{"default":"p-main","p-main":"p-main"}},
		  "bare":{"endpoints":{"openai":{"base_url":"https://bare.example/v1","wire_api":"openai-chat-completions"}}}}`),
		Profiles: map[string]string{"alpha": "tiered-profile", "beta": "plain-profile", "gamma": "bare-profile"},
		Resolved: map[string]ResolvedProfile{
			"tiered-profile": {Provider: "tiered"}, "plain-profile": {Provider: "plain"},
			"bare-profile": {Provider: "bare"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

// rolePack is an agent pack installing bin, with derive as its derive.lua ("" for none).
func rolePack(t *testing.T, bin, derive string) *Pack {
	t.Helper()
	return scopePack(t, bin, `{"name":"`+bin+`","contributes":[
	  {"kind":"program","bin":"`+bin+`","via":"npm","package":"@acme/`+bin+`","protocols":["openai"]}]}`, derive)
}

// roleVars renders a delivery's YOLO_MODEL_* vars as KEY=VALUE, a removal as -KEY, in order.
func roleVars(vars []agentenv.Var) string {
	var out []string
	for _, v := range vars {
		if !strings.HasPrefix(v.Key, ModelRoleEnvPrefix) {
			continue
		}
		if v.Unset {
			out = append(out, "-"+v.Key)
			continue
		}
		out = append(out, v.Key+"="+v.Value)
	}
	return strings.Join(out, " ")
}

// Each agent carries ITS provider's tiers, qualified, and a tier only another provider names is
// removed, so a child an agent on `plain` starts cannot inherit `tiered`'s fast model. A tier no
// provider names (`frontier`) is not touched at all. None of the three packs ships a derive.lua:
// the variables are core's.
func TestEachAgentCarriesItsOwnProvidersTiers(t *testing.T) {
	scope := roleScope(t, []*Pack{rolePack(t, "alpha", ""), rolePack(t, "beta", ""), rolePack(t, "gamma", "")})
	for agent, want := range map[string]string{
		"alpha": "YOLO_MODEL_BALANCED=tiered/t-mid YOLO_MODEL_DEFAULT=tiered/t-main YOLO_MODEL_FAST=tiered/t-mini",
		"beta":  "-YOLO_MODEL_BALANCED YOLO_MODEL_DEFAULT=plain/p-main -YOLO_MODEL_FAST",
		"gamma": "-YOLO_MODEL_BALANCED -YOLO_MODEL_DEFAULT -YOLO_MODEL_FAST",
	} {
		if got := roleVars(scope.Agent(agent).Shape); got != want {
			t.Errorf("%s's role environment = %q, want %q", agent, got, want)
		}
	}
}

// A launch whose providers name no tier alias composes no role variable for anyone, so an agent
// pack with no derive.lua composes exactly what it did before (nothing).
func TestNoTierAliasAnywhereComposesNothing(t *testing.T) {
	scope, err := ScopeCredentials(ScopeInput{
		Packs:     []*Pack{rolePack(t, "alpha", "")},
		Providers: twoProviders(t),
		Profiles:  map[string]string{"alpha": "zai-profile"},
		Resolved:  map[string]ResolvedProfile{"zai-profile": {Provider: "zai"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if d := scope.Agent("alpha"); !d.Empty() {
		t.Errorf("no provider names a tier alias, and alpha's delivery is %+v", d.Shape)
	}
}

// The agent's own pack wins a name it sets itself, a removal included: the pack is the more
// specific statement about its own agent's process. The roles it does not name stay core's.
func TestThePacksOwnDeriveOverridesARoleVariable(t *testing.T) {
	alpha := rolePack(t, "alpha", `yolo.env("alpha", function(ctx)
  return { YOLO_MODEL_FAST = "tiered/pinned", YOLO_MODEL_BALANCED = ctx.tombstone, OTHER = "x" }
end)`)
	scope := roleScope(t, []*Pack{alpha, rolePack(t, "beta", ""), rolePack(t, "gamma", "")})
	want := "-YOLO_MODEL_BALANCED YOLO_MODEL_DEFAULT=tiered/t-main YOLO_MODEL_FAST=tiered/pinned"
	if got := roleVars(scope.Agent("alpha").Shape); got != want {
		t.Errorf("alpha's role environment = %q, want %q", got, want)
	}
	if v, ok := scope.DeliveredTo("alpha", "OTHER"); !ok || v != "x" {
		t.Errorf("the derive's own variable was lost beside the roles: %q, %v", v, ok)
	}
}

// The role is the PRIMARY's: an active set's later entry is never a source of a tier, as
// yolo.model_for answers with no provider named.
func TestARoleIsThePrimarysProvidersOnly(t *testing.T) {
	scope, err := ScopeCredentials(ScopeInput{
		Packs: []*Pack{rolePack(t, "alpha", "")},
		Providers: userProviders(t, `{
		  "tiered":{"models":{"default":"t-main","fast":"t-mini"}},
		  "plain":{"models":{"default":"p-main"}}}`),
		Profiles: map[string]string{"alpha": "plain-profile"},
		Sets:     map[string][]string{"alpha": {"plain-profile", "tiered-profile"}},
		Resolved: map[string]ResolvedProfile{
			"tiered-profile": {Provider: "tiered"}, "plain-profile": {Provider: "plain"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := roleVars(scope.Agent("alpha").Shape), "YOLO_MODEL_DEFAULT=plain/p-main -YOLO_MODEL_FAST"; got != want {
		t.Errorf("alpha on [plain, tiered] = %q, want %q", got, want)
	}
}

// The names are the convention's, upper-cased, so the four the research doc publishes.
func TestModelRoleEnvSpellsTheFourNames(t *testing.T) {
	var got []string
	for _, a := range []string{"default", "fast", "balanced", "frontier"} {
		got = append(got, ModelRoleEnv(a))
	}
	if strings.Join(got, ",") != "YOLO_MODEL_DEFAULT,YOLO_MODEL_FAST,YOLO_MODEL_BALANCED,YOLO_MODEL_FRONTIER" {
		t.Errorf("ModelRoleEnv = %v", got)
	}
}
