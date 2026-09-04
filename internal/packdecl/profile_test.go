package packdecl

import (
	"strings"
	"testing"
)

// A profile contribution decodes through strict Decode, Profiles() returns them in
// declaration order, and ProfileFor selects by the name the user chose (§5.2) — the
// open-selector twin of PostureFor, whose selector is the confinement notch.
//
// The kind is NAME + PROVIDER and nothing else (OQ-PT8's shrink): every field that used
// to live in the body is a tombstone or a refusal, and the bodies it carried are
// `profile:`-modified contributions of the kinds that own them.
func TestProfileDecodesAndSelects(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"claude","contributes":[
	  {"kind":"profile","name":"bedrock","provider":"bedrock"},
	  {"kind":"profile","name":"glm","provider":"zai"}]}`))
	if len(probs) != 0 {
		t.Fatalf("profiles should decode cleanly, got: %v", probs)
	}
	ps := m.Profiles()
	if len(ps) != 2 || ps[0].Name != "bedrock" || ps[1].Name != "glm" {
		t.Fatalf("declaration order lost (it IS the later-wins fold order): %+v", ps)
	}
	if ps[0].Provider != "bedrock" || ps[1].Provider != "zai" {
		t.Errorf("the selection did not decode: %+v", ps)
	}
	// The selector is the name, and an unselected name is nil rather than an empty
	// posture — the same shape PostureFor's callers key off.
	if got := m.ProfileFor("bedrock"); got == nil || got.Provider != "bedrock" {
		t.Errorf("ProfileFor(bedrock) should be the first profile: %+v", got)
	}
	if got := m.ProfileFor("nobody"); got != nil {
		t.Errorf("ProfileFor(nobody) should be nil, got %+v", got)
	}
	// A pack with no profile gets none — the nil-ness the fold keys off.
	none := &Manifest{Contributes: []Contribution{{Kind: KindEnv, Vars: map[string]string{"X": "1"}}}}
	if got := none.Profiles(); len(got) != 0 {
		t.Errorf("a pack declaring no profile should have none, got %+v", got)
	}
	if got := none.ProfileFor("bedrock"); got != nil {
		t.Errorf("a pack with no profile should select nothing, got %+v", got)
	}
}

// The kind's required fields: BOTH name and provider, on every decode path. Property 3
// (§5.2) is an inversion of the first draft — the provider used to be optional because
// the body was the point; now the selection IS the point, and a name resolving to no
// provider would be a gate that silently gates nothing.
//
// The old BODY fields are refused by name, with the migration named, so an un-migrated
// manifest is told what to write rather than told its JSON is unknown.
func TestProfileValidation(t *testing.T) {
	_, probs := Decode([]byte(`{"name":"acme","contributes":[{"kind":"profile","provider":"p"}]}`))
	if len(probs) != 1 || !strings.Contains(probs[0], `needs "name"`) {
		t.Errorf("a profile without a name must be refused, got %v", probs)
	}

	_, probs = Decode([]byte(`{"name":"acme","contributes":[{"kind":"profile","name":"dev"}]}`))
	if len(probs) != 1 || !strings.Contains(probs[0], `needs "provider"`) {
		t.Errorf("a profile without a provider must be refused (property 3), got %v", probs)
	}

	// The tombstones. `requires_provider` is decodable, then refused by name — the `tier`
	// precedent — so a stale manifest gets the migration sentence instead of
	// "unknown field", and the refusal is the only thing that field can produce.
	_, probs = Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"profile","name":"dev","requires_provider":"p"}]}`))
	joined := strings.Join(probs, "; ")
	if len(probs) != 2 || !strings.Contains(joined, `requires_provider`) ||
		!strings.Contains(joined, `"provider": "p"`) {
		t.Errorf("the old field name must be refused with the migration, got %v", probs)
	}

	// The other two body halves refuse the same way, each naming the contribution that
	// owns the body now.
	_, probs = Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"profile","name":"dev","provider":"p","launch":[{"bin":"claude","flags":["--x"]}]}]}`))
	if len(probs) != 1 || !strings.Contains(probs[0], `kind "launch"`) {
		t.Errorf("a profile launch body must be refused with its new kind, got %v", probs)
	}
	_, probs = Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"profile","name":"dev","provider":"p","env":{"A":"1"}}]}`))
	if len(probs) != 1 || !strings.Contains(probs[0], `"vars"`) {
		t.Errorf("a profile env body must be refused with its new kind and field, got %v", probs)
	}

	// `config` is a live field on two other kinds, so the tombstone cannot see it: the
	// kind's own validation refuses it, naming the config-overlay migration.
	_, probs = Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"profile","name":"dev","provider":"p",
	   "config":[{"agent":"claude","name":"settings","managed":{"env":{"A":"1"}}}]}]}`))
	if len(probs) != 1 || !strings.Contains(probs[0], `config-overlay`) {
		t.Errorf("a profile config patch must be refused with its new kind, got %v", probs)
	}

	// A minimal well-formed declaration: name + provider, nothing else, decodes clean.
	if _, probs = Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"profile","name":"dev","provider":"p"}]}`)); len(probs) != 0 {
		t.Errorf("a name+provider profile should validate, got %v", probs)
	}
}

// The `profile` MODIFIER, on the kinds that have a consumer for it: config-overlay gates
// on the target surface's owning agent, env gates on the launch's profile table. Every
// other kind refuses it, because no consumer reads it there — and a refusal is what keeps
// an author from shipping a gate that gates nothing. (The schema halves are pinned in
// overlayprofile_test.go; this is the kind-list half of the same rule.)
func TestProfileModifierTakesConfigOverlayAndEnvOnly(t *testing.T) {
	ok := []string{
		`{"kind":"config-overlay","profile":"dev","surface":"claude/settings",
		  "config":{"managed":{"env":{"A":"1"}}}}`,
		`{"kind":"env","profile":"dev","vars":{"A":"1"}}`,
	}
	for _, raw := range ok {
		if _, probs := Decode([]byte(`{"name":"acme","contributes":[` + raw + `]}`)); len(probs) != 0 {
			t.Errorf("a gated contribution should validate, got %v", probs)
		}
	}

	refused := []string{
		`{"kind":"program","profile":"dev","bin":"claude","via":"npm","package":"claude"}`,
		`{"kind":"launch","profile":"dev","bin":"claude","flags":["--x"]}`,
		`{"kind":"provider","profile":"dev","name":"zai"}`,
		`{"kind":"config","profile":"dev","config":[{"agent":"claude","name":"settings"}]}`,
	}
	for _, raw := range refused {
		_, probs := Decode([]byte(`{"name":"acme","contributes":[` + raw + `]}`))
		joined := strings.Join(probs, "; ")
		if len(probs) != 1 || !strings.Contains(joined, `does not take "profile"`) {
			t.Errorf("a gated %q must be refused, got %v", raw, probs)
		}
	}
}

// Two profiles with the same name in ONE pack is a load error (§5.2): the name is the
// whole selector, so the second declaration would silently replace the first in
// ProfileFor — which picks the first match and has no way to report the ambiguity.
//
// AUTHORING-TIME only (validateContributions runs on the strict path); DecodeTolerant
// validates entries one at a time and cannot see siblings, the same authoring/jail line
// validateProviderNames draws.
func TestProfileNameDeclaredTwiceByOnePackIsRefused(t *testing.T) {
	_, probs := Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"profile","name":"dev","provider":"p"},
	  {"kind":"profile","name":"dev","provider":"q"}]}`))
	joined := strings.Join(probs, "; ")
	if !contains(joined, `profile "dev" is declared again`) {
		t.Errorf("a duplicate profile name must be refused with the first index named, got %q", joined)
	}
	// Across packs there is nothing to combine — this file sees one pack at a time, and
	// two packs shipping "dev" is exactly the unrelated-coincidence case the kind's
	// ownership rules leave legal. Two DIFFERENT names in one pack are the ordinary
	// multi-selection pack.
	if _, probs = Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"profile","name":"dev","provider":"p"},
	  {"kind":"profile","name":"bedrock","provider":"q"}]}`)); len(probs) != 0 {
		t.Errorf("two distinct profile names should validate, got %v", probs)
	}
}

// A profile names a provider this pack declares and reads nothing from the host, so it makes
// no host crossing and nothing about it reaches the launch disclosure (the same absence
// autonomy has). It used to be phrased as "needs no approval at `pack install`", which OQ-TP9
// made moot without changing the fact.
func TestProfileMakesNoHostCrossing(t *testing.T) {
	m := &Manifest{Contributes: []Contribution{{Kind: KindProfile, Name: "bedrock",
		Provider: "bedrock"}}}
	if c := hostCrossings(m); len(c) != 0 {
		t.Errorf("a profile reads nothing from the host, got %v", c)
	}
}

// The env fold's two accessors partition the kind: the unconditional map holds only
// ungated contributions, and the gated slice carries its own gate — so a consumer that
// reads only the map cannot fold a gated pair as decoration, and one that wants the gate
// gets it spelled out per entry.
func TestEnvContributionsSplitOnTheProfileGate(t *testing.T) {
	m := &Manifest{Contributes: []Contribution{
		{Kind: KindEnv, Vars: map[string]string{"STATIC": "1"}},
		{Kind: KindEnv, Profile: "bedrock", Vars: map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1"}},
		{Kind: KindEnv, Profile: "bedrock", Vars: map[string]string{"AWS_REGION": "us-east-1"}},
		{Kind: KindEnv, Vars: map[string]string{"LATER": "2"}},
	}}

	if got := m.EnvContributions(); len(got) != 2 || got["STATIC"] != "1" || got["LATER"] != "2" {
		t.Errorf("the unconditional map must hold the ungated entries only, got %+v", got)
	}
	gated := m.ProfiledEnvContributions()
	if len(gated) != 2 || gated[0].Profile != "bedrock" || gated[1].Profile != "bedrock" {
		t.Fatalf("the gated slice must keep both gated contributions in declaration order, got %+v", gated)
	}
	if gated[0].Vars["CLAUDE_CODE_USE_BEDROCK"] != "1" || gated[1].Vars["AWS_REGION"] != "us-east-1" {
		t.Errorf("the gated vars did not survive, got %+v", gated)
	}

	// A manifest with no env at all: nil map, nil slice — the shape the fold keys off.
	empty := &Manifest{}
	if got := empty.EnvContributions(); got != nil {
		t.Errorf("no env declarations should give no map, got %+v", got)
	}
	if got := empty.ProfiledEnvContributions(); got != nil {
		t.Errorf("no env declarations should give no slice, got %+v", got)
	}
}
