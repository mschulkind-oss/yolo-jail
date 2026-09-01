package packdecl

import (
	"strings"
	"testing"
)

// A profile contribution decodes through strict Decode, Profiles() returns them in
// declaration order, and ProfileFor selects by the name the user chose (§3.1) — the
// open-selector twin of PostureFor, whose selector is the confinement notch.
func TestProfileDecodesAndSelects(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"claude","contributes":[
	  {"kind":"profile","name":"bedrock",
	   "config":[{"agent":"claude","name":"settings","managed":{"env":{"CLAUDE_CODE_USE_BEDROCK":"1"}}}],
	   "launch":[{"bin":"claude","flags":["--profile","bedrock"]}],
	   "env":{"AWS_REGION":"us-east-1","AWS_PROFILE":null},
	   "requires_provider":"bedrock"},
	  {"kind":"profile","name":"glm","env":{"ZAI_API_KEY":"x"}}]}`))
	if len(probs) != 0 {
		t.Fatalf("profiles should decode cleanly, got: %v", probs)
	}
	ps := m.Profiles()
	if len(ps) != 2 || ps[0].Name != "bedrock" || ps[1].Name != "glm" {
		t.Fatalf("declaration order lost (it IS the later-wins fold order): %+v", ps)
	}
	if len(ps[0].Launch) != 1 || ps[0].Launch[0].Bin != "claude" {
		t.Errorf("the profile's launch flags did not decode: %+v", ps[0].Launch)
	}
	if ps[0].RequiresProvider != "bedrock" {
		t.Errorf("requires_provider did not decode: %+v", ps[0])
	}
	if len(ps[0].Config) == 0 {
		t.Errorf("the profile's config patch did not decode: %+v", ps[0])
	}
	// The selector is the name, and an unselected name is nil rather than an empty
	// posture — the same shape PostureFor's callers key off.
	if got := m.ProfileFor("bedrock"); got == nil || got.RequiresProvider != "bedrock" {
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

// `env` is the one map in the manifest whose values may be an explicit JSON null: null
// means UNSET (OQ-7), and map[string]string cannot carry that distinction — a null
// decodes to "" there, indistinguishable from a deliberate empty value. Hence EnvValue.
func TestProfileEnvNullMeansUnset(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"claude","contributes":[
	  {"kind":"profile","name":"bedrock","env":{"AWS_REGION":"us-east-1","AWS_PROFILE":null,"AWS_EMPTY":""}}]}`))
	if len(probs) != 0 {
		t.Fatalf("a null env value is legal (it means unset), got: %v", probs)
	}
	env := m.ProfileFor("bedrock").Env
	if got, ok := env["AWS_REGION"]; !ok || !got.Set || got.Value != "us-east-1" {
		t.Errorf(`AWS_REGION should decode set to "us-east-1", got %+v`, got)
	}
	if got, ok := env["AWS_PROFILE"]; !ok || got.Set {
		t.Errorf("AWS_PROFILE should decode as an explicit UNSET, got %+v", got)
	}
	// An empty STRING is a real value, not an unset: the two are different declarations.
	if got, ok := env["AWS_EMPTY"]; !ok || !got.Set || got.Value != "" {
		t.Errorf(`AWS_EMPTY should decode set to "", got %+v`, got)
	}
}

// The kind's required fields, and the shape it refuses: `name` is the whole identity
// (§3.4 — CombineExclusive keyed (pack, name)), and a launch entry is a bin + flags.
func TestProfileValidation(t *testing.T) {
	_, probs := Decode([]byte(`{"name":"acme","contributes":[{"kind":"profile","env":{"A":"1"}}]}`))
	if len(probs) != 1 || !strings.Contains(probs[0], `needs "name"`) {
		t.Errorf("a profile without a name must be refused, got %v", probs)
	}

	_, probs = Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"profile","name":"dev","launch":[{"flags":["--x"]}]},
	  {"kind":"profile","name":"other","launch":[{"bin":"/usr/bin/env"}]}]}`))
	joined := strings.Join(probs, "; ")
	if len(probs) != 2 || !strings.Contains(joined, `launch[0]: needs a "bin"`) ||
		!strings.Contains(joined, "must be a bare program name") {
		t.Errorf("each launch entry needs a bare bin name, got %v", probs)
	}

	// requires_provider is an optional plain string; a profile with none of the optional
	// bodies is a legal (if pointless) declaration.
	if _, probs = Decode([]byte(`{"name":"acme","contributes":[{"kind":"profile","name":"dev"}]}`)); len(probs) != 0 {
		t.Errorf("a bare named profile should validate, got %v", probs)
	}
}

// Two profiles with the same name in ONE pack is a load error (§3.4): the name is the
// whole selector, so the second declaration would silently replace the first in
// ProfileFor — which picks the first match and has no way to report the ambiguity.
//
// AUTHORING-TIME only (validateContributions runs on the strict path); DecodeTolerant
// validates entries one at a time and cannot see siblings, the same authoring/jail line
// validateProviderNames draws.
func TestProfileNameDeclaredTwiceByOnePackIsRefused(t *testing.T) {
	_, probs := Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"profile","name":"dev","env":{"A":"1"}},
	  {"kind":"profile","name":"dev","env":{"B":"2"}}]}`))
	joined := strings.Join(probs, "; ")
	if !contains(joined, `profile "dev" is declared again`) {
		t.Errorf("a duplicate profile name must be refused with the first index named, got %q", joined)
	}
	// Across packs there is nothing to combine — this file sees one pack at a time, and
	// two packs shipping "dev" is exactly the unrelated-coincidence case §3.4 rules legal.
	// Two DIFFERENT names in one pack are the ordinary multi-variant pack.
	if _, probs = Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"profile","name":"dev","env":{"A":"1"}},
	  {"kind":"profile","name":"bedrock","env":{"B":"2"}}]}`)); len(probs) != 0 {
		t.Errorf("two distinct profile names should validate, got %v", probs)
	}
}

// A profile patches the pack's OWN surfaces and names a provider by reference — it reads
// nothing from the host, so it makes no host-access claim and needs no approval at
// `pack install` (the same absence autonomy has).
func TestProfileMakesNoHostAccessClaim(t *testing.T) {
	m := &Manifest{Contributes: []Contribution{{Kind: KindProfile, Name: "bedrock",
		Env:              map[string]EnvValue{"AWS_REGION": {Set: true, Value: "us-east-1"}},
		RequiresProvider: "bedrock"}}}
	if r := m.NeedsHostAccess(); len(r) != 0 {
		t.Errorf("a profile reads nothing from the host, got %v", r)
	}
	if c := m.HostAccessClaims(); len(c) != 0 {
		t.Errorf("a profile makes no host-access claim, got %v", c)
	}
}
