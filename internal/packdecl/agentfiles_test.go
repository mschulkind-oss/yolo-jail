package packdecl

import (
	"reflect"
	"strings"
	"testing"
)

// `agent_files` is a program's fact (docs/design/model-lists-and-pickers.md MM-D33), read by bin:
// packload.AgentEnv asks the manifest of the pack installing an agent which of its env derive's
// variables are agent files.
func TestAgentFilesIsAProgramsFactReadByBin(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"program","bin":"acme","via":"npm","package":"@acme/acme",
	   "agent_files":{"ACME_PROVIDERS":"providers.json"}},
	  {"kind":"program","bin":"other","via":"npm","package":"@acme/other"}]}`))
	if len(probs) != 0 {
		t.Fatalf("fixture: %v", probs)
	}
	if got, want := m.AgentFiles("acme"), map[string]string{"ACME_PROVIDERS": "providers.json"}; !reflect.DeepEqual(got, want) {
		t.Errorf("AgentFiles(acme) = %v, want %v", got, want)
	}
	if got := m.AgentFiles("other"); got != nil {
		t.Errorf("a program that declares nothing has agent files %v", got)
	}
	if got := m.AgentFiles("absent"); got != nil {
		t.Errorf("a bin the manifest does not install has agent files %v", got)
	}
}

// What no consumer could write as declared is refused at load: the field on another kind, an
// empty object, a key that is no variable name, a file name that leaves the directory, names a
// parent or ends like an env file, one name for two variables, and the field on a fork, whose
// base keeps it (FP-D6).
func TestAgentFilesIsRefusedWhereNothingCouldWriteIt(t *testing.T) {
	prog := func(files string) string {
		return `{"kind":"program","bin":"a","via":"npm","package":"a","agent_files":` + files + `}`
	}
	for _, tc := range []struct{ name, contribution, want string }{
		{"another kind", `{"kind":"env","env":{"A":"b"},"agent_files":{"A":"a.json"}}`,
			`does not take "agent_files"`},
		{"an empty object", prog(`{}`), "is an empty object"},
		{"a key that is no variable", prog(`{"not-a-var":"a.json"}`), "is not an environment variable name"},
		{"a path", prog(`{"A":"sub/a.json"}`), "want one plain file name"},
		{"a parent", prog(`{"A":"a..json"}`), "want one plain file name"},
		{"a hidden name", prog(`{"A":".a.json"}`), "want one plain file name"},
		{"an env file's suffix", prog(`{"A":"a.sh"}`), `ending in ".sh"`},
		{"one name twice", prog(`{"A":"a.json","B":"a.json"}`), `names the file "a.json" for both A and B`},
		{"a fork", `{"kind":"program","bin":"a","via":"source","fork_of":"base",
		  "source":"git+https://example.test/a.git#0123456789abcdef0123456789abcdef01234567",
		  "build":"make","produces":[".local/bin/a"],"agent_files":{"A":"a.json"}}`,
			`does not take "agent_files"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, probs := Decode([]byte(`{"name":"acme","contributes":[` + tc.contribution + `]}`))
			if !strings.Contains(strings.Join(probs, "\n"), tc.want) {
				t.Errorf("problems %v, want one containing %q", probs, tc.want)
			}
		})
	}
}
