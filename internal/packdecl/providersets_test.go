package packdecl

// providersets_test.go pins the manifest half of docs/design/active-provider-sets.md: the
// `provider_sets` declaration a program makes for the agent it installs (AP-D2), refused on
// every other kind, read by bin; and a profile name without the comma the -p grammar
// separates a list with (OQ-AP1). Through Decode and the real validator.

import (
	"testing"
)

func TestProviderSetsIsAProgramDeclarationReadByBin(t *testing.T) {
	m, problems := Decode([]byte(`{"contributes":[
	  {"kind":"program","bin":"pi","via":"npm","package":"p","provider_sets":true},
	  {"kind":"program","bin":"other","via":"npm","package":"q"}]}`))
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	if !m.HoldsProviderSets("pi") {
		t.Error("pi's program declares provider_sets, so HoldsProviderSets(pi) must be true")
	}
	if m.HoldsProviderSets("other") || m.HoldsProviderSets("") || m.HoldsProviderSets("absent") {
		t.Error("a program that declares nothing is single-provider, and so is a bin the pack does not install")
	}
	probs := validateContribution("c[0]", Contribution{Kind: KindSkills, From: "skills",
		Into: ".x/skills", ProviderSets: true})
	if !containsSubstr(probs, `does not take "provider_sets"`) {
		t.Errorf("provider_sets on a non-program kind must be refused; got %v", probs)
	}
}

func TestAProfileNameRefusesTheListSeparator(t *testing.T) {
	probs := validateContribution("test", Contribution{Kind: KindProfile, Name: "zai,fast", Provider: "zai"})
	if !containsSubstr(probs, "must not contain ','") {
		t.Fatalf("problems = %v, want the comma refusal", probs)
	}
}
