package packload

// profiles_test.go pins ResolveProfiles' merge order and its two refusals. The merge
// order is the one a reader of provider-catalog-and-selection.md §5.2 will assume —
// declared defaults UNDER the profile's own values — so each test asserts the ORDER,
// not merely membership: the value that wins a colliding key is the assertion.

import (
	"strings"
	"testing"
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

// resolve is ResolveProfiles with the refusal channel turned into a failure; the tests
// that ARE about a refusal call ResolveProfiles directly and read the error.
func resolve(t *testing.T, packs []*Pack, user map[string]UserProfile) map[string]ResolvedProfile {
	t.Helper()
	got, err := ResolveProfiles(packs, user)
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
// pack's — field merge, pack under user, exactly the convention `providers` uses. This
// is also where the requires_provider compat shim is the code under test.
func TestResolveProfilesUserEntryCustomizesAPackProfile(t *testing.T) {
	pack := optionProviderPack(t)
	pack.Decl = declFrom(t, `{"contributes":[
	  {"kind":"provider","name":"zai","options":{"model":"default","thinking":null}},
	  {"kind":"profile","name":"zai","requires_provider":"zai"}]}`)
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
	  {"kind":"profile","name":"zai","requires_provider":"zai"}]}`)
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
	})
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

// TestResolveProfilesNoDeclaredOptionsImposesNoCensus is the rule that keeps today's
// tree launching: no pack SHIPS options yet, so a provider with no `options` declares no
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

// TestResolveProfilesRefusesAProfileThatSelectsNothing is property 3's fatal arm: the
// manifest schema requires only a profile NAME today, so a kind:profile with no provider
// is a legal declaration that still declares nothing — and a name that selects no
// provider is not a profile.
func TestResolveProfilesRefusesAProfileThatSelectsNothing(t *testing.T) {
	pack := &Pack{Name: "ghost", Decl: declFrom(t, `{"contributes":[
	  {"kind":"profile","name":"ghost"}]}`)}
	_, err := ResolveProfiles([]*Pack{pack}, nil)
	if err == nil {
		t.Fatal("a profile that names no provider should refuse the launch")
	}
	if !strings.Contains(err.Error(), `profile "ghost" names no provider`) {
		t.Errorf("refusal should say what the name failed to declare, got: %v", err)
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
	  {"kind":"profile","name":"zai","requires_provider":"zai"}]}`)}
	user := map[string]UserProfile{"zai-fast": {Provider: "zai"}}
	declared := DeclaredProfileNames([]*Pack{pack}, user)
	if strings.Join(declared, ",") != "zai,zai-fast" {
		t.Errorf("declared names should be the sorted union of both sides, got %v", declared)
	}
	msg := UndeclaredProfileMessage("zai-fst", declared)
	if !strings.Contains(msg, `no profile named "zai-fst" is declared`) ||
		!strings.Contains(msg, "declared: zai, zai-fast") {
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
	})
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	got := dump(t, ProfilesWireTable(resolved))
	want := `{"zai-fast": {"provider": "zai", "model": "default", "thinking": "low"}}`
	if got != want {
		t.Errorf("wire table should be %s, got %s", want, got)
	}
}
