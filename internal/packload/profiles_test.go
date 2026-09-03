package packload

// profiles_test.go pins ResolveProfiles' merge order and its two refusals. The merge
// order is the one a reader of docs/reference/providers.md will assume —
// declared defaults UNDER the profile's own values — so each test asserts the ORDER,
// not merely membership: the value that wins a colliding key is the assertion.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentenv"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// optionProviderPack returns a pack whose provider DECLARES a surface: `model` with a
// default, `thinking` declared with none — the two shapes OptionDefault exists to tell
// apart (OQ-CS7's wrinkle), and the pair every census test below measures against.
func optionProviderPack(t *testing.T) *Pack {
	return &Pack{Name: "zai", Decl: declFrom(t, `{"contributes":[
	  {"kind":"provider","name":"zai",
	   "endpoints":{"anthropic":{"base_url":"https://api.z.ai/api/anthropic"}},
	   "options":{"model":"default","thinking":null}}]}`)}
}

// composePacks is the providers table a launch with no user `providers` entries would
// carry, built through the package's own compose helper. ResolveProfiles reads its
// declared-options census off the COMPOSED table rather than off the manifests, so a
// test that resolves a profile needs the table a real launch would have composed first —
// handing it one built by hand would be testing a caller that does not exist.
func composePacks(t *testing.T, packs []*Pack) *jsonx.OrderedMap {
	return compose(t, nil, packs)
}

// resolve is ResolveProfiles with the refusal channel turned into a failure; the tests
// that ARE about a refusal call ResolveProfiles directly and read the error.
func resolve(t *testing.T, packs []*Pack, user map[string]UserProfile) map[string]ResolvedProfile {
	t.Helper()
	got, err := ResolveProfiles(packs, user, composePacks(t, packs))
	if err != nil {
		t.Fatalf("resolving profiles: %v", err)
	}
	return got
}

// TestResolveProfilesPutsDeclaredDefaultsUnderUserValues pins the merge ORDER: the
// provider's `model` default is what a profile that says nothing about `model` gets, and
// a profile that does say something REPLACES it — the direction the definition states
// ("a profile states only what it changes", §5.2). Asserted with a colliding key, not
// with two disjoint ones, because disjoint keys pass under either order.
func TestResolveProfilesPutsDeclaredDefaultsUnderUserValues(t *testing.T) {
	pack := optionProviderPack(t)
	got := resolve(t, []*Pack{pack}, map[string]UserProfile{
		"zai-slow": {Provider: "zai"},
		"zai-fast": {Provider: "zai", Options: map[string]string{"model": "fast"}},
	})

	if p := got["zai-slow"]; p.Provider != "zai" || p.Options["model"] != "default" {
		t.Errorf("a profile stating nothing takes the declared default, got %+v", p)
	}
	if p := got["zai-fast"]; p.Provider != "zai" || p.Options["model"] != "fast" {
		t.Errorf("a profile's own value wins over the declared default, got %+v", p)
	}
	// `thinking` is DECLARED with no default, so the profile that says nothing about it
	// reaches the derive as nothing — that is the promise the null spelling makes
	// (OQ-CS7), and the reason a defaultless option is not the empty string.
	slow := got["zai-slow"]
	if _, set := slow.Options["thinking"]; set {
		t.Errorf("a declared option with no default composes nothing, got %+v", slow.Options)
	}
}

// TestResolveProfilesDefaultlessOptionIsSettable is the other half of the null reading:
// the profile CAN set it, and it arrives as an ordinary value.
func TestResolveProfilesDefaultlessOptionIsSettable(t *testing.T) {
	pack := optionProviderPack(t)
	got := resolve(t, []*Pack{pack}, map[string]UserProfile{
		"thinker": {Provider: "zai", Options: map[string]string{"thinking": "low"}},
	})
	if v := got["thinker"].Options["thinking"]; v != "low" {
		t.Errorf(`a profile-set defaultless option should read "low", got %q`, v)
	}
}

// TestResolveProfilesUserEntryCustomizesAPackProfile pins the §5.2 worked example: the
// user declares the PACK's own profile name with no `provider` of its own, and keeps the
// pack's — field merge, pack under user, exactly the convention `providers` uses.
func TestResolveProfilesUserEntryCustomizesAPackProfile(t *testing.T) {
	pack := optionProviderPack(t)
	pack.Decl = declFrom(t, `{"contributes":[
	  {"kind":"provider","name":"zai","options":{"model":"default","thinking":null}},
	  {"kind":"profile","name":"zai","provider":"zai"}]}`)
	got := resolve(t, []*Pack{pack}, map[string]UserProfile{
		"zai": {Options: map[string]string{"model": "fast"}},
	})
	if p := got["zai"]; p.Provider != "zai" {
		t.Errorf("a user entry without a provider keeps the pack's, got %+v", p)
	}
	if p := got["zai"]; p.Options["model"] != "fast" {
		t.Errorf("the user's value wins over the declared default, got %+v", p.Options)
	}
}

// TestResolveProfilesPackProfileAloneStillResolves is the shipped shape, unchanged: a
// pack declares a profile and no user entry touches it, and the name still resolves to
// its provider plus that provider's defaults — a shipped profile is a default a user
// overrides, not a second class of thing the table omits.
func TestResolveProfilesPackProfileAloneStillResolves(t *testing.T) {
	pack := optionProviderPack(t)
	pack.Decl = declFrom(t, `{"contributes":[
	  {"kind":"provider","name":"zai","options":{"model":"default"}},
	  {"kind":"profile","name":"zai","provider":"zai"}]}`)
	got := resolve(t, []*Pack{pack}, nil)
	p, ok := got["zai"]
	if !ok || p.Provider != "zai" || p.Options["model"] != "default" {
		t.Errorf("a pack-only profile should resolve to its provider and defaults, got %+v", p)
	}
}

// TestResolveProfilesUndeclaredOptionNamesWhatTheProviderAccepts is the census (OQ-CS7):
// the provider owns the schema, so the refusal names the option, the provider that does
// not have it, and what it DOES accept. One message — the same one every caller shows.
func TestResolveProfilesUndeclaredOptionNamesWhatTheProviderAccepts(t *testing.T) {
	pack := optionProviderPack(t)
	_, err := ResolveProfiles([]*Pack{pack}, map[string]UserProfile{
		"zai-fast": {Provider: "zai", Options: map[string]string{"model": "fast", "temperature": "0.2"}},
	}, composePacks(t, []*Pack{pack}))
	if err == nil {
		t.Fatal("an option the provider does not declare should refuse the launch")
	}
	for _, want := range []string{
		`profile "zai-fast"`, `option "temperature"`, `provider "zai"`, `declared: model, thinking`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal should name %s, got: %v", want, err)
		}
	}
}

// TestResolveProfilesNoDeclaredOptionsImposesNoCensus is the rule that keeps an
// un-declaring provider launching: a provider whose entry carries no `options` imposes no
// schema and cannot refuse anything. The brief's own integration case —
// `{"zai-fast": {"provider": "zai", "model": "fast"}}` over the shipped zai — depends on
// this, and a census keyed to "the provider exists" instead of "the provider declared"
// would refuse it.
func TestResolveProfilesNoDeclaredOptionsImposesNoCensus(t *testing.T) {
	pack := &Pack{Name: "zai", Decl: declFrom(t, `{"contributes":[
	  {"kind":"provider","name":"zai",
	   "endpoints":{"anthropic":{"base_url":"https://api.z.ai/api/anthropic"}}}]}`)}
	got := resolve(t, []*Pack{pack}, map[string]UserProfile{
		"zai-fast": {Provider: "zai", Options: map[string]string{"model": "fast"}},
	})
	if p := got["zai-fast"]; p.Provider != "zai" || p.Options["model"] != "fast" {
		t.Errorf("free keys pass verbatim when the provider declares no surface, got %+v", p)
	}
}

// TestResolveProfilesComposesAUserProviderOptionsUnderAProfileValue pins the OTHER way a
// provider comes to declare a surface: the user's own `providers` entry. A user-declared
// provider is an ordinary entry of the composed table, so its `options` must reach the
// resolution the way a pack's do — the declared default UNDER the profile's own value,
// per §5.2's merge order. (The config layer's half of this — that such an entry validates
// clean at all — is TestValidateProvidersOptionsIsADeclaredSurface.)
func TestResolveProfilesComposesAUserProviderOptionsUnderAProfileValue(t *testing.T) {
	pack := &Pack{Name: "mine", Decl: declFrom(t, `{"contributes":[
	  {"kind":"provider","name":"mine",
	   "endpoints":{"openai":{"base_url":"https://mine.example/v4"}}}]}`)}
	user := userProviders(t, `{"mine":{"options":{"model":"default","thinking":null}}}`)
	got, err := ResolveProfiles([]*Pack{pack}, map[string]UserProfile{
		"mine-slow": {Provider: "mine"},
		"mine-fast": {Provider: "mine", Options: map[string]string{"model": "fast"}},
	}, compose(t, user, []*Pack{pack}))
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if p := got["mine-slow"]; p.Provider != "mine" || p.Options["model"] != "default" {
		t.Errorf("a user-declared default should reach a profile that says nothing, got %+v", p)
	}
	if p := got["mine-fast"]; p.Options["model"] != "fast" {
		t.Errorf("the profile's own value should win over the user-declared default, got %+v", p)
	}
	if _, set := got["mine-slow"].Options["thinking"]; set {
		t.Errorf("a user-declared option with no default composes nothing, got %+v", got["mine-slow"].Options)
	}
}

// TestResolveProfilesCensusReadsTheComposedTable pins WHY the census is taken off the
// composed table rather than off the manifests: the user's `providers` entry changes what
// a provider declares, and the census has to change with it. Adding an option to the entry
// makes it settable by a profile — under a manifest-only census this same profile is
// REFUSED for naming an option the pack never declared — and nulling a declared default
// keeps the option settable while taking its default away.
func TestResolveProfilesCensusReadsTheComposedTable(t *testing.T) {
	pack := &Pack{Name: "zai", Decl: declFrom(t, `{"contributes":[
	  {"kind":"provider","name":"zai",
	   "endpoints":{"anthropic":{"base_url":"https://api.z.ai/api/anthropic"}},
	   "options":{"model":"default"}}]}`)}

	// The user's entry ADDS an option: the census widens, so the profile naming it
	// resolves instead of refusing.
	added := userProviders(t, `{"zai":{"options":{"thinking":null}}}`)
	got, err := ResolveProfiles([]*Pack{pack}, map[string]UserProfile{
		"thinker": {Provider: "zai", Options: map[string]string{"thinking": "low"}},
	}, compose(t, added, []*Pack{pack}))
	if err != nil {
		t.Fatalf("an option the user's entry declares should be settable: %v", err)
	}
	if v := got["thinker"].Options["thinking"]; v != "low" {
		t.Errorf("a user-added option should resolve, got %+v", got["thinker"].Options)
	}

	// And the user LOWERS a default: `model` stays declared, so it stays settable, but the
	// profile that says nothing about it reaches the derive as nothing.
	lowered := userProviders(t, `{"zai":{"options":{"model":null}}}`)
	got, err = ResolveProfiles([]*Pack{pack}, map[string]UserProfile{
		"silent":   {Provider: "zai"},
		"zai-fast": {Provider: "zai", Options: map[string]string{"model": "fast"}},
	}, compose(t, lowered, []*Pack{pack}))
	if err != nil {
		t.Fatalf("a lowered default keeps the option declared, so a profile may still set it: %v", err)
	}
	if _, set := got["silent"].Options["model"]; set {
		t.Errorf("a lowered default composes nothing, got %+v", got["silent"].Options)
	}
	if v := got["zai-fast"].Options["model"]; v != "fast" {
		t.Errorf("the lowered option is still settable, got %+v", got["zai-fast"].Options)
	}
}

// TestResolveProfilesRefusesAProfileThatSelectsNothing pins the boundary the shrink
// moved: a kind:profile that names no provider is no longer a legal manifest shape (the
// schema refuses it, and so does the config layer for a user entry), so the one way this
// branch is reachable is a CALLER that built its own table. That is refused rather than
// skipped — a selection of nothing is exactly what an entry in the resolved table must
// never quietly be, and the pre-shrink version of this function skipped it because the
// schema still allowed the shape.
func TestResolveProfilesRefusesAProfileThatSelectsNothing(t *testing.T) {
	pack := &Pack{Name: "ghost", Decl: declFrom(t, `{"contributes":[
	  {"kind":"profile","name":"real","provider":"zai"}]}`)}
	_, err := ResolveProfiles([]*Pack{pack}, map[string]UserProfile{
		"ghost": {Options: map[string]string{"model": "fast"}},
	}, composePacks(t, []*Pack{pack}))
	if err == nil {
		t.Fatal("a profile resolving to no provider must refuse, not resolve to an empty selection")
	}
	if !strings.Contains(err.Error(), `profile "ghost" names no provider`) {
		t.Errorf("the refusal should name the profile and why, got: %v", err)
	}
}

// TestResolveProfilesUnknownProviderResolvesWithoutACensus pins the boundary between the
// two refusals: a profile naming a provider NO selected pack declares is not the census
// refusal — there is no schema to be measured against — so it resolves, and the provider
// being absent from the composed table is the §6.2 credential pre-flight's business.
func TestResolveProfilesUnknownProviderResolvesWithoutACensus(t *testing.T) {
	pack := optionProviderPack(t)
	got := resolve(t, []*Pack{pack}, map[string]UserProfile{
		"offroad": {Provider: "nowhere", Options: map[string]string{"model": "x"}},
	})
	if p := got["offroad"]; p.Provider != "nowhere" || p.Options["model"] != "x" {
		t.Errorf("a provider nothing declares imposes no census, got %+v", p)
	}
}

// TestDeclaredProfileNamesIsTheUnionOfBothSides pins the OQ-CS6 check's source of truth:
// a name either side declares is declared, and a name NEITHER does is the reportable
// error the message spells out with the list of what IS declared.
func TestDeclaredProfileNamesIsTheUnionOfBothSides(t *testing.T) {
	pack := &Pack{Name: "zai", Decl: declFrom(t, `{"contributes":[
	  {"kind":"profile","name":"zai","provider":"zai"},
	  {"kind":"profile","name":"bedrock","provider":"bedrock"}]}`)}
	user := map[string]UserProfile{"zai-fast": {Provider: "zai"}}
	declared := DeclaredProfileNames([]*Pack{pack}, user)
	if strings.Join(declared, ",") != "bedrock,zai,zai-fast" {
		t.Errorf("declared names should be the sorted union of both sides, got %v", declared)
	}
	msg := UndeclaredProfileMessage("zai-fst", declared)
	if !strings.Contains(msg, `no profile named "zai-fst" is declared`) ||
		!strings.Contains(msg, "declared: bedrock, zai, zai-fast") {
		t.Errorf("the undeclared-name refusal should name the typo and the real list, got %q", msg)
	}
	if none := UndeclaredProfileMessage("x", nil); !strings.Contains(none, "(declared: none)") {
		t.Errorf("nothing declared should say so rather than print an empty list, got %q", none)
	}
}

// TestProfilesWireTableIsDeterministic pins the wire shape AND its key order: this table
// is serialized into YOLO_PROFILES and parsed in the jail, so a run that reshuffles it
// turns every unchanged launch into a diff. `provider` leads each entry; option keys are
// sorted after it.
func TestProfilesWireTableIsDeterministic(t *testing.T) {
	pack := optionProviderPack(t)
	resolved, err := ResolveProfiles([]*Pack{pack}, map[string]UserProfile{
		"zai-fast": {Provider: "zai", Options: map[string]string{"thinking": "low"}},
	}, composePacks(t, []*Pack{pack}))
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	got := dump(t, ProfilesWireTable(resolved))
	want := `{"zai-fast": {"provider": "zai", "model": "default", "thinking": "low"}}`
	if got != want {
		t.Errorf("wire table should be %s, got %s", want, got)
	}
}

// THE ENV PATH of ctx.profile: AgentEnv is the runner the host notch composes through,
// and the resolved table reaches its derive only through WithResolvedProfiles — an option,
// not a parameter, which is exactly the kind of input a caller forgets to hand over. So
// the test drives the runner the way the host does and asserts the option's effect on what
// the derive saw; deleting the option from the caller's argument list (or the field from
// the ctx) leaves these vars empty.

// profileEnvClaudePack is the agent pack whose env producer reports what its ctx.profile
// held: the model option's value and how many keys the map carried.
func profileEnvClaudePack(t *testing.T) *Pack {
	return envClaudePack(t, `
yolo.env("claude", function(ctx)
  local n = 0
  for _ in pairs(ctx.profile) do n = n + 1 end
  return { PROFILE_MODEL = ctx.profile.model or "", PROFILE_KEYS = tostring(n) }
end)`)
}

func TestAgentEnvHandsTheActiveProfileOptionsToTheDerive(t *testing.T) {
	claude := profileEnvClaudePack(t)
	zai := optionProviderPack(t)
	providers, err := ComposeProviders(nil, []*Pack{claude, zai})
	if err != nil {
		t.Fatalf("composing providers: %v", err)
	}
	resolved, err := ResolveProfiles([]*Pack{claude, zai}, map[string]UserProfile{
		"glm": {Provider: "zai", Options: map[string]string{"model": "fast"}},
	}, composePacks(t, []*Pack{claude, zai}))
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	vars, err := AgentEnv([]*Pack{claude, zai}, providers,
		map[string]string{"claude": "glm"}, "claude", "glm", envLookup,
		WithResolvedProfiles(resolved))
	if err != nil {
		t.Fatal(err)
	}
	// One key: `model` is the profile's own value, and `thinking` — declared with no
	// default and set by nobody — composes nothing (OQ-CS7's null reading).
	want := []agentenv.Var{
		{Key: "PROFILE_KEYS", Value: "1"},
		{Key: "PROFILE_MODEL", Value: "fast"},
	}
	if len(vars) != len(want) {
		t.Fatalf("vars = %#v, want %#v (the resolved table reached the derive)", vars, want)
	}
	for i := range want {
		if vars[i] != want[i] {
			t.Errorf("var %d = %#v, want %#v", i, vars[i], want[i])
		}
	}
}

// The declared defaults arrive with the profile, not just its own values: `glm` says
// nothing about `model`, and the derive still sees the default — a producer should not
// have to re-read the provider's declaration to know what a profile resolves to.
func TestAgentEnvSeesTheDeclaredDefaultsUnderTheProfile(t *testing.T) {
	claude := profileEnvClaudePack(t)
	zai := optionProviderPack(t)
	providers, err := ComposeProviders(nil, []*Pack{claude, zai})
	if err != nil {
		t.Fatalf("composing providers: %v", err)
	}
	vars, err := AgentEnv([]*Pack{claude, zai}, providers,
		map[string]string{"claude": "glm"}, "claude", "glm", envLookup,
		WithResolvedProfiles(map[string]ResolvedProfile{
			"glm": {Provider: "zai", Options: map[string]string{"model": "default"}},
		}))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vars {
		if v.Key == "PROFILE_MODEL" && v.Value != "default" {
			t.Errorf("PROFILE_MODEL = %q, want the declared default", v.Value)
		}
	}
}

// A caller that never hands the table over (an older notch, or one that does not compose
// it) gets an EMPTY ctx.profile — the same world as a profile with no options, which is
// the honest answer for "this composition does not know", and never a second, worse one.
func TestAgentEnvWithoutTheResolvedTableSeesAnEmptyProfile(t *testing.T) {
	claude := profileEnvClaudePack(t)
	zai := optionProviderPack(t)
	providers, err := ComposeProviders(nil, []*Pack{claude, zai})
	if err != nil {
		t.Fatalf("composing providers: %v", err)
	}
	vars, err := AgentEnv([]*Pack{claude, zai}, providers,
		map[string]string{"claude": "glm"}, "claude", "glm", envLookup)
	if err != nil {
		t.Fatal(err)
	}
	if len(vars) != 1 || vars[0] != (agentenv.Var{Key: "PROFILE_KEYS", Value: "0"}) {
		t.Errorf("vars = %#v, want exactly PROFILE_KEYS=0 — empty, not somebody else's options", vars)
	}
}
