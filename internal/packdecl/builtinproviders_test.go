package packdecl

import (
	"reflect"
	"strings"
	"testing"
)

// `built_in_providers` is a program's fact (docs/design/pi-codex-provider-shadowing.md OQ-3): the
// names decode as written, a plan names the program's own provider and the key it reads, a null
// plan is the program having none for that plan, and a program that says nothing decodes to none.
// BuiltInProvidersFor reads it by bin.
func TestBuiltInProvidersIsAProgramsFact(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"program","bin":"acme","via":"npm","package":"@acme/acme",
	   "built_in_providers":{"names":["zai","zai-coding-plan","foo"],
	     "plans":{"zai":{"provider":"zai-coding-plan","api_key_env_name":"ZHIPU_API_KEY"},"foo":null}}},
	  {"kind":"program","bin":"other","via":"npm","package":"@acme/other"}]}`))
	if len(probs) != 0 {
		t.Fatalf("fixture: %v", probs)
	}
	got := m.BuiltInProvidersFor("acme")
	if got == nil {
		t.Fatal("acme declares built_in_providers, BuiltInProvidersFor returned nil")
	}
	if want := []string{"zai", "zai-coding-plan", "foo"}; !reflect.DeepEqual(got.Names, want) {
		t.Errorf("names = %v, want %v", got.Names, want)
	}
	if p := got.Plans["zai"]; p == nil || p.Provider != "zai-coding-plan" || p.APIKeyEnvName != "ZHIPU_API_KEY" {
		t.Errorf("plans.zai = %+v, want zai-coding-plan reading ZHIPU_API_KEY", p)
	}
	if p, ok := got.Plans["foo"]; !ok || p != nil {
		t.Errorf("plans.foo = %+v (present %v), want a null plan: the program has none for it", p, ok)
	}
	if m.BuiltInProvidersFor("other") != nil {
		t.Error("a program that declares nothing must decode to no built_in_providers")
	}
	if m.BuiltInProvidersFor("") != nil || m.BuiltInProvidersFor("absent") != nil {
		t.Error("a bin the manifest does not install has no built_in_providers")
	}
}

// What no reader could use as written is refused at load: the field on another kind, no names, an
// empty, padded or repeated name, a plan with no provider, a plan naming a provider that is not one
// of the names, a bad key name, an unknown key, and the field on a fork, whose base keeps it (FP-D6).
func TestBuiltInProvidersIsRefusedWhereNothingCouldReadIt(t *testing.T) {
	const prog = `{"kind":"program","bin":"a","via":"npm","package":"a","built_in_providers":`
	for _, tc := range []struct{ name, contribution, want string }{
		{"another kind", `{"kind":"env","env":{"A":"b"},"built_in_providers":{"names":["x"]}}`,
			`does not take "built_in_providers"`},
		{"no names", prog + `{"names":[]}}`, `"built_in_providers.names" is empty`},
		{"names absent", prog + `{}}`, `"built_in_providers.names" is empty`},
		{"an empty name", prog + `{"names":[""]}}`, "empty provider name"},
		{"a padded name", prog + `{"names":[" zai"]}}`, "holds whitespace"},
		{"a name twice", prog + `{"names":["p","p"]}}`, `names "p" twice`},
		{"a plan naming no provider", prog + `{"names":["p"],"plans":{"q":{}}}}`, "names no provider"},
		{"a plan off the names", prog + `{"names":["p"],"plans":{"q":{"provider":"r"}}}}`,
			`names "r", which is not in "built_in_providers.names"`},
		{"a bad key name", prog + `{"names":["p"],"plans":{"q":{"provider":"p","api_key_env_name":"1X"}}}}`,
			"is not an environment variable name"},
		{"an empty plan key", prog + `{"names":["p"],"plans":{"":{"provider":"p"}}}}`, "keyed by an empty"},
		{"an unknown key", prog + `{"name":["p"]}}`, `unknown field "name"`},
		{"a fork", `{"kind":"program","bin":"a","via":"source","fork_of":"base",
		  "source":"git+https://example.test/a.git#0123456789abcdef0123456789abcdef01234567",
		  "build":"make","produces":[".local/bin/a"],"built_in_providers":{"names":["p"]}}`,
			`does not take "built_in_providers"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, probs := Decode([]byte(`{"name":"acme","contributes":[` + tc.contribution + `]}`))
			if !strings.Contains(strings.Join(probs, "\n"), tc.want) {
				t.Errorf("problems %v, want one containing %q", probs, tc.want)
			}
		})
	}
}
