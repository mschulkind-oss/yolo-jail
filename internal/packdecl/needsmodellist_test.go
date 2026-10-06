package packdecl

import (
	"strings"
	"testing"
)

// `needs_model_list` is a program's fact (docs/design/model-lists-and-pickers.md OQ-MM6): the
// platforms it starts on only from a list. Manifest.NeedsModelList answers it per bin and
// platform, and a program that declares nothing needs no list anywhere.
func TestNeedsModelListIsAProgramsFact(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"program","bin":"acme","via":"npm","package":"@acme/acme","needs_model_list":["aws-bedrock"]},
	  {"kind":"program","bin":"other","via":"npm","package":"@acme/other"}]}`))
	if len(probs) != 0 {
		t.Fatalf("fixture: %v", probs)
	}
	if !m.NeedsModelList("acme", "aws-bedrock") {
		t.Error("acme declares it needs a list on aws-bedrock")
	}
	if m.NeedsModelList("acme", "other-platform") || m.NeedsModelList("acme", "") {
		t.Error("acme needs a list on aws-bedrock alone")
	}
	if m.NeedsModelList("other", "aws-bedrock") || m.NeedsModelList("absent", "aws-bedrock") {
		t.Error("a program that declares nothing, or none at all, needs no list")
	}
}

// What no consumer could read as written is refused at load: the field on another kind, an empty
// list, an empty platform, a platform twice, and the field on a fork, whose base keeps it (FP-D6).
func TestNeedsModelListIsRefusedWhereNothingCouldReadIt(t *testing.T) {
	for _, tc := range []struct{ name, contribution, want string }{
		{"another kind", `{"kind":"env","env":{"A":"b"},"needs_model_list":["aws-bedrock"]}`,
			`does not take "needs_model_list"`},
		{"an empty list", `{"kind":"program","bin":"a","via":"npm","package":"a","needs_model_list":[]}`,
			"is an empty list"},
		{"an empty platform", `{"kind":"program","bin":"a","via":"npm","package":"a","needs_model_list":[""]}`,
			"empty platform"},
		{"a platform twice", `{"kind":"program","bin":"a","via":"npm","package":"a",
		  "needs_model_list":["p","p"]}`, `names "p" twice`},
		{"a fork", `{"kind":"program","bin":"a","via":"source","fork_of":"base",
		  "source":"git+https://example.test/a.git#0123456789abcdef0123456789abcdef01234567",
		  "build":"make","produces":[".local/bin/a"],"needs_model_list":["aws-bedrock"]}`,
			`does not take "needs_model_list"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, probs := Decode([]byte(`{"name":"acme","contributes":[` + tc.contribution + `]}`))
			if !strings.Contains(strings.Join(probs, "\n"), tc.want) {
				t.Errorf("problems %v, want one containing %q", probs, tc.want)
			}
		})
	}
}
