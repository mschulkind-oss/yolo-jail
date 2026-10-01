package packdecl

import (
	"reflect"
	"strings"
	"testing"
)

// `exact_menu_refuses` is a program's fact (docs/design/model-lists-and-pickers.md MM-D29): the
// accessor answers it by bin, with the providers it names, and nil for a program that declares
// nothing or a bin the manifest does not install.
func TestExactMenuRefusesIsAProgramsFact(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"program","bin":"acme","via":"npm","package":"@acme/acme",
	   "exact_menu_refuses":{"providers":["sub"]}},
	  {"kind":"program","bin":"bare","via":"npm","package":"@acme/bare","exact_menu_refuses":{}},
	  {"kind":"program","bin":"other","via":"npm","package":"@acme/other"}]}`))
	if len(probs) != 0 {
		t.Fatalf("fixture: %v", probs)
	}
	if got := m.ExactMenuRefusal("acme"); got == nil || !reflect.DeepEqual(got.Providers, []string{"sub"}) {
		t.Errorf("ExactMenuRefusal(acme) = %+v, want providers [sub]", got)
	}
	if got := m.ExactMenuRefusal("bare"); got == nil || got.Providers != nil {
		t.Errorf("ExactMenuRefusal(bare) = %+v, want the fact with no providers", got)
	}
	if m.ExactMenuRefusal("other") != nil || m.ExactMenuRefusal("absent") != nil {
		t.Errorf("a program that declares nothing, and an absent bin, must answer nil")
	}
}

// What no consumer could read as written is refused at load: the field on another kind, an empty
// providers list, an empty provider name, a name twice, and the field on a fork, whose base keeps
// it (FP-D6).
func TestExactMenuRefusesIsRefusedWhereNothingCouldReadIt(t *testing.T) {
	for _, tc := range []struct{ name, contribution, want string }{
		{"another kind", `{"kind":"env","env":{"A":"b"},"exact_menu_refuses":{}}`,
			`does not take "exact_menu_refuses"`},
		{"an empty providers list", `{"kind":"program","bin":"a","via":"npm","package":"a",
		  "exact_menu_refuses":{"providers":[]}}`, "is an empty list"},
		{"an empty provider name", `{"kind":"program","bin":"a","via":"npm","package":"a",
		  "exact_menu_refuses":{"providers":[""]}}`, "empty provider name"},
		{"a provider twice", `{"kind":"program","bin":"a","via":"npm","package":"a",
		  "exact_menu_refuses":{"providers":["p","p"]}}`, `names "p" twice`},
		{"an unknown key", `{"kind":"program","bin":"a","via":"npm","package":"a",
		  "exact_menu_refuses":{"provider":["p"]}}`, `unknown field "provider"`},
		{"a fork", `{"kind":"program","bin":"a","via":"source","fork_of":"base",
		  "source":"git+https://example.test/a.git#0123456789abcdef0123456789abcdef01234567",
		  "build":"make","produces":[".local/bin/a"],"exact_menu_refuses":{}}`,
			`does not take "exact_menu_refuses"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, probs := Decode([]byte(`{"name":"acme","contributes":[` + tc.contribution + `]}`))
			if !strings.Contains(strings.Join(probs, "\n"), tc.want) {
				t.Errorf("problems %v, want one containing %q", probs, tc.want)
			}
		})
	}
}
