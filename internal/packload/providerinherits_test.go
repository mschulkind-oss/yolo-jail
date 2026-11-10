package packload

// providerinherits_test.go pins `inherits` (docs/design/provider-inherits.md) through the
// compositions every consumer runs, ComposeProviders and ComposeProviderDeclarations, so each test
// fails when the resolver's call site in composeProviderTable is deleted, not only the helper.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// inheritsPack is a parent p and a child c that inherits it, with the credential and the variant
// each owns, on one regional platform.
func inheritsPack(t *testing.T, extra string) *Pack {
	t.Helper()
	return readablePack(t, `
[providers.p]
api_key_env_name = "P_KEY"
platform = "aws-bedrock"
region_env_name = ["AWS_REGION"]
model = "parent-default"

[providers.p.variants.bridge]
name = "p-bridge"
via = "wire-bridge"

[providers.c]
inherits = "p"
api_key_env_name = "C_KEY"

[providers.c.variants.bridge]
name = "c-bridge"
via = "wire-bridge"
`+extra)
}

func inheritedEntry(t *testing.T, table *jsonx.OrderedMap, name string) *jsonx.OrderedMap {
	t.Helper()
	e := providerEntry(table, name)
	if e == nil {
		t.Fatalf("no %s in the composed table: %s", name, dump(t, table))
	}
	return e
}

func entryIDs(t *testing.T, table *jsonx.OrderedMap, name string) []string {
	t.Helper()
	models := subOrderedOrNil(inheritedEntry(t, table, name), "models")
	if models == nil {
		return nil
	}
	return append([]string(nil), models.Keys()...)
}

func TestAChildTakesItsParentsUserSettings(t *testing.T) {
	user := userProviders(t, `{"p":{"region":"eu-west-1","model":"mine","models":{"m-1":{"name":"One","context":200000}},
	  "every_model":{"reasoning":true}}}`)
	got := compose(t, user, []*Pack{inheritsPack(t, "")})
	c := inheritedEntry(t, got, "c")
	if region, _ := stringAt(c, "region"); region != "eu-west-1" {
		t.Errorf("c.region = %q, want the parent's user region", region)
	}
	if m, _ := stringAt(c, "model"); m != "mine" {
		t.Errorf("c.model = %q, want the parent's user default model", m)
	}
	if platform, _ := stringAt(c, "platform"); platform != "aws-bedrock" {
		t.Errorf("c.platform = %q, want the inherited platform", platform)
	}
	if ids := entryIDs(t, got, "c"); !reflect.DeepEqual(ids, []string{"m-1"}) {
		t.Errorf("c models = %v, want the parent's [m-1]", ids)
	}
	if _, ok := inheritedEntry(t, got, "c").Get(everyModelKey); ok {
		t.Errorf("every_model was left in the table the derives read: %s", dump(t, c))
	}
	wantSubstrings(t, dump(t, c), `"reasoning": "true"`)
}

func TestAChildsOwnUserModelsDoNotLeakBackToTheParent(t *testing.T) {
	user := userProviders(t, `{"p":{"models":{"m-1":{"name":"One"}}},"c":{"models":{"m-2":{"name":"Two"},"m-1":{"name":"One for c"}}}}`)
	got := compose(t, user, []*Pack{inheritsPack(t, "")})
	if ids := entryIDs(t, got, "p"); !reflect.DeepEqual(ids, []string{"m-1"}) {
		t.Errorf("p models = %v, want only its own [m-1]", ids)
	}
	if ids := entryIDs(t, got, "c"); !reflect.DeepEqual(ids, []string{"m-1", "m-2"}) {
		t.Errorf("c models = %v, want the inherited m-1 and its own m-2", ids)
	}
	wantSubstrings(t, dump(t, inheritedEntry(t, got, "c")), `"name": "One for c"`)
	if strings.Contains(dump(t, inheritedEntry(t, got, "p")), "One for c") {
		t.Error("the child's override reached the parent")
	}
}

func TestTheCredentialTheNamesAndTheVariantsNeverCarryOver(t *testing.T) {
	user := userProviders(t, `{"p":{"api_key_env_name":"USER_P_KEY","alias":"pp","name":"pp",
	  "variants":{"extra":{"name":"p-extra","via":"wire-bridge"}}}}`)
	packs := []*Pack{inheritsPack(t, "")}
	got := compose(t, user, packs)
	c := inheritedEntry(t, got, "c")
	if key, _ := stringAt(c, "api_key_env_name"); key != "C_KEY" {
		t.Errorf("c.api_key_env_name = %q, want its own C_KEY", key)
	}
	for _, k := range []string{"alias", "name", "variants"} {
		if _, ok := c.Get(k); ok {
			t.Errorf("c carries the parent's %q: %s", k, dump(t, c))
		}
	}
	decls, err := ComposeProviderDeclarations(user, packs)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range decls {
		if d.Name != "c" {
			continue
		}
		if !reflect.DeepEqual([]string(d.APIKeyEnvName), []string{"C_KEY"}) {
			t.Errorf("c declaration claims %v, want [C_KEY]", d.APIKeyEnvName)
		}
		if len(d.Aliases) != 0 {
			t.Errorf("c took the parent's aliases %v", d.Aliases)
		}
	}
}

func TestAChildsOwnPackKeysLayerOverTheParent(t *testing.T) {
	pack := readablePack(t, `
[providers.p]
platform = "aws-bedrock"
region = "us-east-1"
model = "parent-default"

[providers.c]
inherits = "p"
region = "ap-south-1"
`)
	c := inheritedEntry(t, compose(t, nil, []*Pack{pack}), "c")
	if region, _ := stringAt(c, "region"); region != "ap-south-1" {
		t.Errorf("c.region = %q, want its own declaration over the parent's", region)
	}
	if platform, _ := stringAt(c, "platform"); platform != "aws-bedrock" {
		t.Errorf("c.platform = %q, want the inherited platform", platform)
	}
}

func TestAChildOnlyContributionNarrowsTheInheritedList(t *testing.T) {
	user := userProviders(t, `{"p":{"models":{"a":"a","b":"b","c":"c"}}}`)
	narrow := modelsPack(t, "policy", `{"kind":"models","provider":"c","only":["a","new-1"]},
	  {"kind":"models","provider":"c","add":[{"id":"new-1","vendor":"v"},{"id":"a","vendor":"v"}]}`)
	table, notes := composeWithNotes(t, user, []*Pack{inheritsPack(t, ""), narrow})
	if got := composedList(t, table, "c"); !reflect.DeepEqual(got, []string{"a", "new-1"}) {
		t.Errorf("c list = %v, want the inherited list narrowed to [a new-1]", got)
	}
	if got := composedList(t, table, "p"); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("p list = %v, want the parent untouched", got)
	}
	if v, _ := inheritedEntry(t, table, "c").Get(ModelsOnlyKey); v != true {
		t.Errorf("c %s = %v, want true", ModelsOnlyKey, v)
	}
	dedup := false
	for _, n := range notes {
		dedup = dedup || (strings.Contains(n, `"a"`) && strings.Contains(n, "already holds"))
	}
	if !dedup {
		t.Errorf("notes = %v, want the add of the inherited id \"a\" deduplicated against it", notes)
	}
}

func TestAChildRowMayTwinAnInheritedRow(t *testing.T) {
	user := userProviders(t, `{"p":{"models":{"base":{"model":"vendor/base","name":"Base","context":1000000}}},
	  "c":{"models":{"base-200k":{"twin_of":"base","compact_at":200000}}}}`)
	got := compose(t, user, []*Pack{inheritsPack(t, "")})
	wantSubstrings(t, dump(t, inheritedEntry(t, got, "c")),
		`"base-200k": "base-200k"`, `"wire_id": "vendor/base"`, `"context_window": "200000"`)
	decls, err := ComposeProviderDeclarations(user, []*Pack{inheritsPack(t, "")})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range decls {
		if d.Name != "c" {
			continue
		}
		if d.ModelRows["base-200k"].TwinOf != "base" {
			t.Fatalf("c rows = %+v, want base-200k twinning the inherited base", d.ModelRows)
		}
		if err := ValidateProviderModelGraph(d); err != nil {
			t.Errorf("the twin of an inherited row failed the graph check: %v", err)
		}
	}
}

func TestANulledParentStillLendsAndIsGone(t *testing.T) {
	pack := inheritsPack(t, "")
	user := userProviders(t, `{"p":null}`)
	got := compose(t, user, []*Pack{pack})
	if providerEntry(got, "p") != nil {
		t.Errorf("the nulled parent is still in the table: %s", dump(t, got))
	}
	c := inheritedEntry(t, got, "c")
	if platform, _ := stringAt(c, "platform"); platform != "aws-bedrock" {
		t.Errorf("c.platform = %q, want the nulled parent's pack-layer platform still lent", platform)
	}
}

func TestAnEmptyInheritsInTheUserLayerOptsOut(t *testing.T) {
	user := userProviders(t, `{"p":{"region":"eu-west-1"},"c":{"inherits":""}}`)
	got := compose(t, user, []*Pack{inheritsPack(t, "")})
	c := inheritedEntry(t, got, "c")
	for _, k := range []string{"platform", "region", "model"} {
		if _, ok := c.Get(k); ok {
			t.Errorf("an opted-out child still holds %q: %s", k, dump(t, c))
		}
	}
}

func TestAUserMayDeclareAChildAndNameItsParent(t *testing.T) {
	user := userProviders(t, `{"mine":{"inherits":"p","api_key_env_name":"MINE_KEY"}}`)
	got := compose(t, user, []*Pack{inheritsPack(t, "")})
	mine := inheritedEntry(t, got, "mine")
	if platform, _ := stringAt(mine, "platform"); platform != "aws-bedrock" {
		t.Errorf("mine.platform = %q, want the inherited platform", platform)
	}
	if key, _ := stringAt(mine, "api_key_env_name"); key != "MINE_KEY" {
		t.Errorf("mine.api_key_env_name = %q, want its own", key)
	}
}

func TestTheComposedTableNeverHoldsInherits(t *testing.T) {
	for _, user := range []string{`{}`, `{"c":{"inherits":""}}`, `{"x":{"inherits":"p"}}`} {
		packs := []*Pack{inheritsPack(t, "")}
		got := compose(t, userProviders(t, user), packs)
		if strings.Contains(dump(t, got), `"inherits"`) {
			t.Errorf("user %s: the key reached the table: %s", user, dump(t, got))
		}
		decls, err := ComposeProviderDeclarations(userProviders(t, user), packs)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range decls {
			if d.Inherits != "" {
				t.Errorf("user %s: declaration %s still inherits %q", user, d.Name, d.Inherits)
			}
		}
	}
}

func TestInheritsRefusalsNameTheNextStep(t *testing.T) {
	for _, tc := range []struct {
		name, pack, user string
		wants            []string
	}{
		{"missing parent", `[providers.c]
inherits = "ghost"
`, `{}`, []string{"providers.c.inherits", `"ghost"`, "yolo pack add", "declare providers.ghost",
			`yolo config set providers.c.inherits '""' --user`}},
		{"self", ``, `{"p":{"inherits":"p"}}`, []string{"providers.p.inherits", "names itself",
			`yolo config set providers.p.inherits '""' --user`}},
		{"chain", `[providers.a]
inherits = "b"
[providers.b]
inherits = "p"
[providers.p]
platform = "x"
`, `{}`, []string{"providers.a.inherits", "one level only", `yolo config set providers.b.inherits '""' --user`}},
		{"cycle", `[providers.a]
inherits = "b"
[providers.b]
inherits = "a"
`, `{}`, []string{"inherit from each other", `yolo config set providers.b.inherits '""' --user`}},
		{"nulled parent that inherits", `[providers.c]
inherits = "p"
[providers.p]
inherits = "q"
[providers.q]
platform = "x"
`, `{"p":null}`, []string{"providers.c.inherits", "one level only",
			`yolo config set providers.c.inherits '"<root>"' --user`}},
		{"nulled parent no pack", `[providers.c]
inherits = "gone"
`, `{"gone":null}`, []string{`"gone"`, "yolo pack add"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pack := readablePack(t, tc.pack)
			if tc.pack == `` {
				pack = readablePack(t, "[providers.p]\nplatform = \"x\"\n")
			}
			_, err := ComposeProviders(userProviders(t, tc.user), []*Pack{pack})
			if err == nil {
				t.Fatal("composed, want a refusal")
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q lacks %q", err, want)
				}
			}
			if _, derr := ComposeProviderDeclarations(userProviders(t, tc.user), []*Pack{pack}); derr == nil {
				t.Error("ComposeProviderDeclarations composed where ComposeProviders refused")
			}
		})
	}
}

func TestAChildInheritsThePackLayerDefaultModelInTheDeclarations(t *testing.T) {
	decls, err := ComposeProviderDeclarations(nil, []*Pack{inheritsPack(t, "")})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range decls {
		if d.Name == "c" {
			if d.Model != "parent-default" || d.Platform != "aws-bedrock" {
				t.Errorf("c = model %q platform %q, want the parent's", d.Model, d.Platform)
			}
			return
		}
	}
	t.Fatal("no c declaration")
}

// The shipped bedrock-key inherits bedrock (packs/bedrock/pack.toml): what the user writes under
// providers.bedrock reaches it, and its own credential stays its own.
func TestTheShippedBedrockKeyInheritsTheUsersBedrockSettings(t *testing.T) {
	packs := embeddedNamed(t, "claude", "aws-auth", "bedrock")
	user := userProviders(t, `{"bedrock":{"region":"eu-west-1","model":"opus","every_model":{"reasoning":true},
	  "models":{"global.anthropic.claude-opus-5-5":{"name":"Opus 5.5","context":1000000}}},
	  "bedrock-key":{"models":{"mine":{"name":"Mine"}}}}`)
	got := compose(t, user, packs)
	key := inheritedEntry(t, got, "bedrock-key")
	if region, _ := stringAt(key, "region"); region != "eu-west-1" {
		t.Errorf("bedrock-key.region = %q, want the bedrock region", region)
	}
	if platform, _ := stringAt(key, "platform"); platform != "aws-bedrock" {
		t.Errorf("bedrock-key.platform = %q, want inherited aws-bedrock", platform)
	}
	if m, _ := stringAt(key, "model"); m != "opus" {
		t.Errorf("bedrock-key.model = %q, want the bedrock default model", m)
	}
	if ids := entryIDs(t, got, "bedrock-key"); !reflect.DeepEqual(ids, []string{"global.anthropic.claude-opus-5-5", "mine"}) {
		t.Errorf("bedrock-key models = %v, want bedrock's list then its own", ids)
	}
	if ids := entryIDs(t, got, "bedrock"); !reflect.DeepEqual(ids, []string{"global.anthropic.claude-opus-5-5"}) {
		t.Errorf("bedrock models = %v: bedrock-key's own model leaked back", ids)
	}
	wantSubstrings(t, dump(t, key), `"reasoning": "true"`)
	names := stringsAt(key, "api_key_env_name")
	if want := []string{"AWS_BEARER_TOKEN_BEDROCK", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"}; !reflect.DeepEqual(names, want) {
		t.Errorf("bedrock-key claims %v, want its own %v", names, want)
	}
	if strings.Contains(dump(t, got), `"inherits"`) {
		t.Error("inherits reached the composed table")
	}
}

func stringsAt(entry *jsonx.OrderedMap, key string) []string {
	v, _ := entry.Get(key)
	list, _ := v.([]any)
	var out []string
	for _, x := range list {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}
