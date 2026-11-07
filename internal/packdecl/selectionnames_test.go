package packdecl

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func decodeOK(t *testing.T, src string) *Manifest {
	t.Helper()
	m, problems := DecodeTOML([]byte(src))
	if len(problems) != 0 || m == nil {
		t.Fatalf("DecodeTOML: %v", problems)
	}
	return m
}

func decodeRefused(t *testing.T, src, want string) {
	t.Helper()
	_, problems := DecodeTOML([]byte(src))
	joined := strings.Join(problems, "\n")
	if !strings.Contains(joined, want) {
		t.Fatalf("problems %q do not contain %q", joined, want)
	}
}

func TestNameListReadsAStringOrAList(t *testing.T) {
	var one, many NameList
	if err := json.Unmarshal([]byte(`"or"`), &one); err != nil || !reflect.DeepEqual(one, NameList{"or"}) {
		t.Fatalf("string: %v %v", one, err)
	}
	if err := json.Unmarshal([]byte(`["a","b"]`), &many); err != nil || !reflect.DeepEqual(many, NameList{"a", "b"}) {
		t.Fatalf("list: %v %v", many, err)
	}
	out, _ := json.Marshal(one)
	if string(out) != `"or"` {
		t.Fatalf("one name marshals as %s, want a string", out)
	}
}

func TestAPackProviderTakesAnAliasStringOrListAndVariantNames(t *testing.T) {
	m := decodeOK(t, `name = "p"

[providers.bedrock]
alias = ["br", "aws"]
platform = "aws-bedrock"

[providers.bedrock.variants.bridge]
name = "bedrock-bridge"
alias = "bb"
via = "wire-bridge"
`)
	providers := m.Providers()
	if len(providers) != 1 {
		t.Fatalf("providers = %+v", providers)
	}
	got := providers[0].SelectionNames()
	want := []string{"bedrock", "br", "aws", "bedrock-bridge", "bb"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SelectionNames = %v, want %v", got, want)
	}
	for _, name := range want {
		if !m.DeclaresSelectionName(name) {
			t.Errorf("DeclaresSelectionName(%q) = false", name)
		}
	}
	if m.DeclaresSelectionName("bedrock/bridge") {
		t.Error("a variant with a name still answers to its default spelling")
	}
	if len(m.LegacySpellings) != 0 {
		t.Errorf("the keyed form is not a legacy spelling: %v", m.LegacySpellings)
	}
}

func TestAVariantWithoutANameAnswersToItsDefaultSpelling(t *testing.T) {
	m := decodeOK(t, `name = "p"

[providers.zai]
platform = "zai"

[providers.zai.variants.fast]
model = "glm-fast"
`)
	if got := m.Providers()[0].SelectionNames(); !reflect.DeepEqual(got, []string{"zai", "zai/fast"}) {
		t.Fatalf("SelectionNames = %v", got)
	}
}

func TestAPackMayNotNameItsProvider(t *testing.T) {
	decodeRefused(t, `name = "p"

[providers.openrouter]
name = "or"
platform = "openrouter"
`, "a pack may not name its provider")
}

func TestAWrittenNameMayNotHoldASlash(t *testing.T) {
	decodeRefused(t, `name = "p"

[providers.zai]
alias = "z/ai"
platform = "zai"
`, "/")
}

func TestADuplicateAliasIsRefused(t *testing.T) {
	decodeRefused(t, `name = "p"

[providers.zai]
alias = ["z", "z"]
platform = "zai"
`, "z")
}

func TestLegacyProviderSpellingsAreListed(t *testing.T) {
	m := decodeOK(t, `name = "p"

[[provider]]
name = "bedrock"
platform = "aws-bedrock"

[[profile]]
name = "bedrock-bridge"
provider = "bedrock"
via = "wire-bridge"
`)
	want := []string{`[[provider]] name = "bedrock"`, `[[profile]] name = "bedrock-bridge"`}
	if !reflect.DeepEqual(m.LegacySpellings, want) {
		t.Fatalf("LegacySpellings = %v, want %v", m.LegacySpellings, want)
	}
	if got := LegacyProviderSpellings([]byte("name = \"p\"\n")); got != nil {
		t.Fatalf("a manifest with no provider lists %v", got)
	}
}

func TestAProviderDeclaredBothWaysIsRefusedNamingPackMigrate(t *testing.T) {
	decodeRefused(t, `name = "p"

[providers.zai]
platform = "zai"

[[provider]]
name = "zai"
pinned_endpoint = true
`, "yolo pack migrate <path>")
}

func TestFormerlyProvidersMustNameANameThePackAnswersTo(t *testing.T) {
	decodeOK(t, `name = "copilot-auth"
formerly = { providers = { github-copilot = "copilot" } }

[providers.github-copilot]
alias = "copilot"
platform = "github-copilot"
`)
	decodeRefused(t, `name = "p"
formerly = { providers = { old = "gone" } }

[providers.zai]
platform = "zai"
`, `no provider of this pack answers to "gone"`)
	decodeRefused(t, `name = "p"
formerly = { providers = { z = "zai" } }

[providers.zai]
alias = "z"
platform = "zai"
`, `still answers to "z"`)
}

func TestFormerSelectionNamesMergesProfilesAndProviders(t *testing.T) {
	f := &Formerly{Profiles: map[string]string{"a": "b"}, Providers: map[string]string{"c": "d"}}
	if got := f.FormerSelectionNames(); !reflect.DeepEqual(got, map[string]string{"a": "b", "c": "d"}) {
		t.Fatalf("FormerSelectionNames = %v", got)
	}
	var none *Formerly
	if got := none.FormerSelectionNames(); len(got) != 0 {
		t.Fatalf("nil Formerly = %v", got)
	}
}

func TestFoldProfilesMakesAliasesAndVariants(t *testing.T) {
	m := decodeOK(t, `name = "p"
formerly = { profiles = { github-copilot = "copilot", older = "elsewhere" } }

[[provider]]
name = "github-copilot"
platform = "github-copilot"

[[provider]]
name = "bedrock"
platform = "aws-bedrock"

[[profile]]
name = "copilot"
provider = "github-copilot"

[[profile]]
name = "bedrock-bridge"
provider = "bedrock"
via = "wire-bridge"

[[profile]]
name = "elsewhere"
provider = "someone-elses"
`)
	folds, kept := m.FoldProfiles()
	if len(folds) != 2 || folds[0].Variant != "" || folds[0].Provider != "github-copilot" ||
		folds[1].Variant != "bridge" || folds[1].Value.Name != "bedrock-bridge" || folds[1].Value.Via != "wire-bridge" {
		t.Fatalf("folds = %+v", folds)
	}
	if len(kept) != 1 || kept[0].Profile != "elsewhere" || !strings.Contains(kept[0].Reason, "not declared in this pack") {
		t.Fatalf("kept = %+v", kept)
	}
	out := m.FoldedProfiles(folds)
	if len(out.Profiles()) != 1 {
		t.Fatalf("profiles left = %+v", out.Profiles())
	}
	byName := map[string]ProviderContribution{}
	for _, p := range out.Providers() {
		byName[p.Name] = p
	}
	if !reflect.DeepEqual(byName["github-copilot"].Aliases, []string{"copilot"}) {
		t.Errorf("aliases = %v", byName["github-copilot"].Aliases)
	}
	if v := byName["bedrock"].Variants["bridge"]; v.Name != "bedrock-bridge" || v.Via != "wire-bridge" {
		t.Errorf("variant = %+v", v)
	}
	if !reflect.DeepEqual(out.Formerly.Providers, map[string]string{"github-copilot": "copilot"}) ||
		!reflect.DeepEqual(out.Formerly.Profiles, map[string]string{"older": "elsewhere"}) {
		t.Errorf("formerly = %+v", out.Formerly)
	}
	if len(m.Profiles()) != 3 {
		t.Error("FoldedProfiles changed the manifest it read")
	}
}
