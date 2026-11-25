package entrypoint

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// A ROW CAN DECLARE THAT ITS ROUTE REFUSES STRICT TOOLS (docs/design/model-lists-and-pickers.md
// ML-D16). `strict_tools = false` is lowered to pi's own `compat.supportsStrictMode: false`, so the
// first request of a fresh session carries no `strict` keys instead of being refused once and
// retried. A row that says nothing carries no compat, and `true` is written as stated.
func piStrictToolsModels(t *testing.T, providersJSON, factsJSON string) map[string]any {
	t.Helper()
	decoded, err := jsonx.Decode([]byte(providersJSON))
	if err != nil {
		t.Fatal(err)
	}
	user, _ := decoded.(*jsonx.OrderedMap)
	factsDecoded, err := jsonx.Decode([]byte(factsJSON))
	if err != nil {
		t.Fatal(err)
	}
	facts, _ := factsDecoded.(*jsonx.OrderedMap)
	providers, err := packload.ComposeProviders(user, testPacksForAgent(t, "pi"), packload.WithModelFacts(facts))
	if err != nil {
		t.Fatal(err)
	}
	r := newPioencodeRender(t, mustCompactJSON(t, providers))
	r.wireProfiles(`{"ion":{"provider":"ion"}}`)
	r.render(t, `{"pi":"ion"}`)
	return r.piModels(t)
}

const ionstreamRows = `{"ion":{
  "endpoints":{"openai":{"base_url":"https://ion.example/v1","wire_api":"openai-chat-completions"}},
  "api_key_env_name":"ION_API_KEY",
  "models":{
    "no-strict":{"id":"ion/model-a","strict_tools":false},
    "strict":{"id":"ion/model-b","strict_tools":true},
    "plain":{"id":"ion/model-c"},
    "shared":{"id":"ion/model-d","extends":"ion-facts"}}}}`

const ionstreamFacts = `{"ion-facts":{"strict_tools":false}}`

func TestPiWritesAModelRowsStrictToolsRefusalAsCompat(t *testing.T) {
	m := piStrictToolsModels(t, ionstreamRows, ionstreamFacts)
	compat := func(id string) map[string]any {
		c, _ := piRow(t, m, "ion", id)["compat"].(map[string]any)
		return c
	}
	if c := compat("ion/model-a"); c["supportsStrictMode"] != false {
		t.Errorf("strict_tools=false row compat = %v, want supportsStrictMode false", c)
	}
	if c := compat("ion/model-b"); c["supportsStrictMode"] != true {
		t.Errorf("strict_tools=true row compat = %v, want supportsStrictMode true", c)
	}
	if c := compat("ion/model-c"); c != nil {
		t.Errorf("a row stating nothing carries compat %v, want none", c)
	}
	if c := compat("ion/model-d"); c["supportsStrictMode"] != false {
		t.Errorf("a row extending model_facts strict_tools=false has compat %v, want supportsStrictMode false", c)
	}
	if _, has := piRow(t, m, "ion", "ion/model-a")["strict_tools"]; has {
		t.Errorf("the row carries the yolo spelling, which pi reads nothing from")
	}
}

// A ROW ON A PROVIDER PI SHIPS reaches pi through the model-lists registration, and the shipped
// extension lowers `strict_tools` into `compat.supportsStrictMode` merged over the catalog row's own
// compat and any OpenRouter pin, with no stray `strict_tools` key left on the registered model.
func TestPiModelListsExtensionLowersStrictToolsIntoCompat(t *testing.T) {
	decoded, err := jsonx.Decode([]byte(`{"openrouter":{"models":{
	  "flat":{"id":"deepseek/deepseek-v4.1-flash:floor","base":"deepseek/deepseek-v4.1-flash","strict_tools":false},
	  "pinned":{"id":"deepseek/deepseek-v4.1-flash:nitro","base":"deepseek/deepseek-v4.1-flash","strict_tools":false,
	    "openrouter_routing":{"order":["together"],"allow_fallbacks":false}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	user, _ := decoded.(*jsonx.OrderedMap)
	providers, err := packload.ComposeProviders(user, testPacksForAgent(t, "pi"))
	if err != nil {
		t.Fatal(err)
	}
	r := newPioencodeRender(t, mustCompactJSON(t, providers))
	r.wireProfiles(`{"openrouter":{"provider":"openrouter"}}`)
	r.render(t, `{"pi":"openrouter"}`)
	raw, err := os.ReadFile(filepath.Join(r.e.Home, filepath.FromSlash(piModelListsRel(t))))
	if err != nil {
		t.Fatal(err)
	}
	run := runPiModelListsExtension(t, raw, piOpenRouterCatalogStub, piCompatStub)
	if len(run.Registrations) != 1 || len(run.Registrations[0].Models) != 2 {
		t.Fatalf("registrations = %+v, want one openrouter list of two rows", run.Registrations)
	}
	for _, m := range run.Registrations[0].Models {
		compat, _ := m["compat"].(map[string]any)
		if compat["supportsStrictMode"] != false {
			t.Errorf("registered %v compat = %v, want supportsStrictMode false", m["id"], compat)
		}
		if _, stray := m["strict_tools"]; stray {
			t.Errorf("registered %v carries a top-level strict_tools; pi reads only compat", m["id"])
		}
	}
	pinned, _ := run.Registrations[0].Models[1]["compat"].(map[string]any)
	if !reflect.DeepEqual(pinned["openRouterRouting"], map[string]any{"order": []any{"together"}, "allow_fallbacks": false}) {
		t.Errorf("the pinned row lost its routing beside the strict fact: %v", pinned)
	}
}

// The readable spelling (a row keyed by a name, saying its id with `model`) reaches pi the same way.
func TestPiWritesAReadableRowsStrictToolsRefusalAsCompat(t *testing.T) {
	m := piStrictToolsModels(t, `{"ion":{
	  "endpoints":{"openai":{"base_url":"https://ion.example/v1","wire_api":"openai-chat-completions"}},
	  "api_key_env_name":"ION_API_KEY",
	  "models":{"a":{"model":"ion/model-a","strict_tools":false},"b":{"model":"ion/model-b"}}}}`, `{}`)
	if c, _ := piRow(t, m, "ion", "ion/model-a")["compat"].(map[string]any); c["supportsStrictMode"] != false {
		t.Errorf("readable strict_tools=false row compat = %v, want supportsStrictMode false", c)
	}
	if c := piRow(t, m, "ion", "ion/model-b")["compat"]; c != nil {
		t.Errorf("readable row stating nothing has compat %v", c)
	}
}
