package packload

// agentfiles_test.go pins the composition half of AGENT FILES (docs/design/model-lists-and-pickers.md
// MM-D33) through the credential gate, ScopeCredentials, whose AgentDelivery.Files is what the
// jail's per-agent env writer reads: a declared variable never becomes a shape var, the derive is
// told whether the notch writes files, and a table is written as JSON.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// filePack is an agent pack installing "acme", declaring ACME_DOC → doc.json as an agent file, with
// derive as its derive.lua.
func filePack(t *testing.T, derive string) *Pack {
	t.Helper()
	return scopePack(t, "acme", `{"name":"acme","contributes":[
	  {"kind":"program","bin":"acme","via":"npm","package":"@acme/acme","protocols":["openai"],
	   "agent_files":{"ACME_DOC":"doc.json"}}]}`, derive)
}

// fileScope composes a gate for acme on a provider of one endpoint, writing agent files when
// files is set.
func fileScope(t *testing.T, packs []*Pack, files bool) (*CredentialScope, error) {
	t.Helper()
	return ScopeCredentials(ScopeInput{
		Packs:      packs,
		Providers:  userProviders(t, `{"p":{"endpoints":{"openai":{"base_url":"https://p.example/v1","wire_api":"openai-chat-completions"}}}}`),
		Profiles:   map[string]string{"acme": "p-profile"},
		Resolved:   map[string]ResolvedProfile{"p-profile": {Provider: "p"}},
		AgentFiles: files,
	})
}

// The derive composes the file only where it is told the notch writes one, and the gate takes it
// out of the environment either way: at a notch that writes agent files it is the delivery's one
// file, its table written as JSON; at one that writes none (the host) it is withheld, and the
// variables the derive composes for that case are the shape.
const fileDerive = `yolo.env("acme", function(ctx)
  local out = { ACME_URL = "https://p.example/v1" }
  if ctx.agent_files then
    out.ACME_DOC = { providers = { { name = "p", apiKey = "k" } }, models = { { id = "m-1" }, { id = "m-2" } } }
    out.ACME_MODEL = "p/m-1"
  else
    out.ACME_MODEL = "m-1"
  end
  return out
end)`

func TestAgentFilesLeaveTheEnvironmentAndReachTheDelivery(t *testing.T) {
	packs := []*Pack{filePack(t, fileDerive)}
	scope, err := fileScope(t, packs, true)
	if err != nil {
		t.Fatal(err)
	}
	d := scope.Agent("acme")
	for _, v := range d.Shape {
		if v.Key == "ACME_DOC" {
			t.Errorf("the agent file's content became a shape var: %+v", v)
		}
		if v.Key == "ACME_MODEL" && v.Value != "p/m-1" {
			t.Errorf("ACME_MODEL = %q, want the file's spelling: the derive was not told the notch writes files", v.Value)
		}
	}
	files := d.AgentFiles()
	if len(files) != 1 || files[0].Var != "ACME_DOC" || files[0].Name != "doc.json" {
		t.Fatalf("agent files = %+v, want ACME_DOC → doc.json", files)
	}
	var doc map[string]any
	if err := json.Unmarshal(files[0].Content, &doc); err != nil {
		t.Fatalf("the table was not written as JSON: %v\n%s", err, files[0].Content)
	}
	want := map[string]any{"providers": []any{map[string]any{"name": "p", "apiKey": "k"}},
		"models": []any{map[string]any{"id": "m-1"}, map[string]any{"id": "m-2"}}}
	if !reflect.DeepEqual(doc, want) {
		t.Errorf("doc.json = %v, want %v", doc, want)
	}
	if !strings.HasSuffix(string(files[0].Content), "\n") {
		t.Errorf("doc.json ends with no newline: %q", files[0].Content)
	}

	host, err := fileScope(t, packs, false)
	if err != nil {
		t.Fatal(err)
	}
	if files := host.Agent("acme").AgentFiles(); len(files) != 0 {
		t.Errorf("a notch that writes no agent file collected %+v", files)
	}
	for _, v := range host.Agent("acme").Shape {
		if v.Key == "ACME_DOC" || (v.Key == "ACME_MODEL" && v.Value != "m-1") {
			t.Errorf("the host's shape carries %s = %q; want no file variable and the environment's own model", v.Key, v.Value)
		}
	}
}

// A derive that composes a declared variable at a notch that writes none has it withheld rather than
// handed to the program as a value, and one that sets it to neither a string nor a table is a
// broken producer, refused like any other.
func TestAnAgentFileIsWithheldWhereNoneIsWrittenAndRefusedWhenMalformed(t *testing.T) {
	always := `yolo.env("acme", function(ctx) return { ACME_DOC = "raw text", ACME_URL = "u" } end)`
	host, err := fileScope(t, []*Pack{filePack(t, always)}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range host.Agent("acme").Shape {
		if v.Key == "ACME_DOC" {
			t.Errorf("ACME_DOC = %q reached the host's environment as a value", v.Value)
		}
	}
	jail, err := fileScope(t, []*Pack{filePack(t, always)}, true)
	if err != nil {
		t.Fatal(err)
	}
	if files := jail.Agent("acme").AgentFiles(); len(files) != 1 || string(files[0].Content) != "raw text" {
		t.Errorf("a string agent file = %+v, want its text as it is", files)
	}
	broken := `yolo.env("acme", function(ctx) return { ACME_DOC = true } end)`
	if _, err := fileScope(t, []*Pack{filePack(t, broken)}, true); err == nil ||
		!strings.Contains(err.Error(), "agent file ACME_DOC") {
		t.Errorf("a boolean agent file composed: %v", err)
	}
}
