package packdecl

import (
	"strings"
	"testing"
)

const inheritsFixture = `name = "fixture"

[providers.parent]
platform = "aws-bedrock"

[providers.child]
inherits = "parent"
`

func TestAProviderInheritsReachesTheDeclarationAndRoundTrips(t *testing.T) {
	m, problems := DecodeTOML([]byte(inheritsFixture))
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	got := map[string]string{}
	for _, p := range m.Providers() {
		got[p.Name] = p.Inherits
	}
	if got["child"] != "parent" || got["parent"] != "" {
		t.Fatalf("Providers() inherits = %v, want child -> parent", got)
	}
	encoded, err := EncodeTOML(m)
	if err != nil {
		t.Fatal(err)
	}
	again, problems := DecodeTOML(encoded)
	if len(problems) != 0 {
		t.Fatalf("re-decode: %v\n%s", problems, encoded)
	}
	for _, p := range again.Providers() {
		if p.Name == "child" && p.Inherits != "parent" {
			t.Errorf("the encoder dropped inherits:\n%s", encoded)
		}
	}
}

func TestAProviderMayNotInheritItself(t *testing.T) {
	_, problems := DecodeTOML([]byte("name = \"fixture\"\n\n[providers.loop]\ninherits = \"loop\"\n"))
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "names the provider itself") {
		t.Fatalf("want a self-inherit problem, got %v", problems)
	}
}

func TestInheritsIsLegalInLegacyJSONProviders(t *testing.T) {
	m := mustDecodeLegacy(t, `{"name":"fixture","contributes":[
		{"kind":"provider","name":"parent","platform":"aws-bedrock"},
		{"kind":"provider","name":"child","inherits":"parent"}]}`)
	for _, p := range m.Providers() {
		if p.Name == "child" && p.Inherits != "parent" {
			t.Fatalf("legacy spelling lost inherits: %+v", p)
		}
	}
}
