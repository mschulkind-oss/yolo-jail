package config

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// RC-D68: the user's `name` on a provider replaces its id in the selection namespace, its
// variants follow as <name>/<variant>, and selecting the replaced name is refused naming the new
// one, in the owner's words, through the refusal every launch reader runs.
func TestAProvidersNameReplacesItsIdAndTheOldNameIsRefusedNamingIt(t *testing.T) {
	packs := activeProviderPacks(t)
	cfg := decode(t, `{"providers": {"bedrock": {"name": "aws", "model": "sonnet",
	    "variants": {"opus": {"model": "opus"}}}}}`)
	profiles, err := SelectableProfiles(map[string]packload.UserProfile{}, cfg, packs)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"aws", "aws/opus"} {
		if _, ok := profiles[name]; !ok {
			t.Errorf("%q is not selectable: %v", name, keysOf(profiles))
		}
	}
	for _, name := range []string{"bedrock", "bedrock/opus"} {
		if _, ok := profiles[name]; ok {
			t.Errorf("the replaced name %q is still selectable", name)
		}
	}
	replaced := ReplacedNameRefusals(cfg, packs, profiles)
	want := `bedrock is named "aws" in your config (providers.bedrock.name): -p aws`
	if replaced["bedrock"] != want {
		t.Fatalf("refusal = %q, want %q", replaced["bedrock"], want)
	}
	fold := FoldProfiles(ReceiversOf(packs), ConfigActiveSelection(decode(t, `{"active_provider": {"default": "bedrock"}}`)))
	problems := UndeclaredProfileProblems(fold, 0, profiles, replaced, packs, nil)
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), want) {
		t.Fatalf("selecting the replaced name was not refused naming the new one: %v", problems)
	}
	ok := FoldProfiles(ReceiversOf(packs), ConfigActiveSelection(decode(t, `{"active_provider": {"default": "aws/opus"}}`)))
	if problems := UndeclaredProfileProblems(ok, 0, profiles, replaced, packs, nil); len(problems) != 0 {
		t.Fatalf("the new name was refused: %v", problems)
	}
}

func TestAVariantsNameReplacesItsDefaultSpelling(t *testing.T) {
	packs := activeProviderPacks(t)
	cfg := decode(t, `{"providers": {"bedrock": {"model": "sonnet",
	    "variants": {"opus": {"name": "big", "alias": ["huge"], "model": "opus"}}}}}`)
	profiles, err := SelectableProfiles(map[string]packload.UserProfile{}, cfg, packs)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"big", "huge"} {
		if _, ok := profiles[name]; !ok {
			t.Errorf("%q is not selectable: %v", name, keysOf(profiles))
		}
	}
	refusal := ReplacedNameRefusals(cfg, packs, profiles)["bedrock/opus"]
	if !strings.HasPrefix(refusal, `bedrock/opus is named "big" (`) || !strings.HasSuffix(refusal, "-p big") {
		t.Fatalf("refusal = %q", refusal)
	}
}

func TestEveryProviderNamespaceCollisionIsFatal(t *testing.T) {
	packs := activeProviderPacks(t)
	for label, body := range map[string]string{
		"an alias another provider's id holds": `{"providers": {"bedrock": {"alias": "zai"}}}`,
		"a variant name an alias holds":        `{"providers": {"bedrock": {"alias": "x", "model": "m", "variants": {"v": {"name": "x", "model": "n"}}}}}`,
		"a name another provider's id holds":   `{"providers": {"bedrock": {"name": "zai"}}}`,
	} {
		_, err := SelectableProfiles(map[string]packload.UserProfile{}, decode(t, body), packs)
		if err == nil || !strings.Contains(err.Error(), "collision") {
			t.Errorf("%s: err = %v, want a collision", label, err)
		}
	}
}

func TestAnAliasEqualToItsOwnNameIsNoCollision(t *testing.T) {
	packs := activeProviderPacks(t)
	cfg := decode(t, `{"providers": {"bedrock": {"name": "aws", "alias": ["aws", "amazon"]}}}`)
	profiles, err := SelectableProfiles(map[string]packload.UserProfile{}, cfg, packs)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := profiles["amazon"]; !ok {
		t.Fatalf("the alias is not selectable: %v", keysOf(profiles))
	}
}

func TestAWrittenNameWithASlashIsRefused(t *testing.T) {
	packs := activeProviderPacks(t)
	_, err := SelectableProfiles(map[string]packload.UserProfile{}, decode(t, `{"providers": {"bedrock": {"name": "a/b"}}, "active_provider": {"default": "native"}}`), packs)
	if err == nil || !strings.Contains(err.Error(), "/") {
		t.Fatalf("err = %v", err)
	}
}

// A pack's legacy [[profile]] keeps a name only over its own provider; one taking a name another
// provider answers to is a collision like any other (an implementation decision under RC-D68).
func TestALegacyPackProfileOverAnotherProvidersNameIsFatal(t *testing.T) {
	m, problems := packdecl.DecodeTOML([]byte(`name = "p"

[providers.bedrock]
alias = "x"
platform = "aws-bedrock"

[providers.zai]
platform = "zai"

[[profile]]
name = "x"
provider = "zai"
`))
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	packs := append(activeProviderPacks(t)[:2], &packload.Pack{Name: "p", Decl: m})
	_, err := SelectableProfiles(map[string]packload.UserProfile{}, decode(t, `{}`), packs)
	if err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("err = %v, want a collision", err)
	}
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
