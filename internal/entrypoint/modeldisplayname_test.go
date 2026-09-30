package entrypoint

// modeldisplayname_test.go pins MM-D7's display-name rule for every derive that writes a
// model row into its agent's own catalog (docs/design/model-lists-and-pickers.md MM-D7): a
// model's display `name` is the entry's own `name` fact, or absent so the agent's catalog name
// (or the id) shows, and NEVER the yolo alias it sits under. The aliases are yolo's pointers —
// `default`, `fast` — and a row named after one showed a model as "default" in opencode's
// menu, and in pi's and oh-omp's, which built their rows the same way (§14.2's WARNING,
// "Display name").
//
// Each agent is driven through the boot render (ConfigurePackSurfaces / ConfigurePackByName)
// over the real embedded pack, so the pin is on what the agent reads, not on a helper.

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// THE HELPER IS ONE TEXT IN THREE FILES. A derive cannot load another file, so
// modelDisplayName is copied into each catalog-writing derive; a copy edited alone would put
// the rule MM-D7 states once back into three answers.
func TestModelDisplayNameHelperIsIdenticalInEveryDerive(t *testing.T) {
	helper := regexp.MustCompile(`(?s)-- THE DISPLAY NAME OF A CATALOG ROW.*?\nlocal function modelDisplayName\(prov, id\)\n.*?\nend\n`)
	var first, firstPack string
	for _, pack := range []string{"opencode", "pi", "omp"} {
		p, err := embeddedPack(pack)
		if err != nil {
			t.Fatal(err)
		}
		matches := helper.FindAllString(packload.DeriveScript(p), -1)
		if len(matches) != 1 {
			t.Fatalf("packs/%s/derive.lua carries %d copies of modelDisplayName, want 1", pack, len(matches))
		}
		if first == "" {
			first, firstPack = matches[0], pack
			continue
		}
		if matches[0] != first {
			t.Errorf("packs/%s/derive.lua's modelDisplayName differs from packs/%s/derive.lua's", pack, firstPack)
		}
	}
}

// aliasNamedJSON is a reachable provider whose one model sits under the `default` alias only
// (packs/cerebras's shape), beside a model that declares a name of its own and one whose only
// aliases are yolo's pointers and itself.
const aliasNamedJSON = `{"gateway":{
  "endpoints":{"openai":{"base_url":"http://127.0.0.1:8090/v1","wire_api":"openai-chat-completions"}},
  "models":{"default":"qwen-3.8-27b","fast":"glm-5.3-flash","glm-5.3":"glm-5.3"},
  "model_options":{"glm-5.3":{"name":"GLM-5.3"}}
}}`

func TestNoCatalogRowIsNamedAfterItsAlias(t *testing.T) {
	r := newPioencodeRender(t, aliasNamedJSON)
	r.render(t, `{}`)

	// opencode: provider.<id>.models is keyed by the wire id.
	ocProv, _ := r.ocConfig(t)["provider"].(map[string]any)
	gw, _ := ocProv["gateway"].(map[string]any)
	ocModels, _ := gw["models"].(map[string]any)
	if len(ocModels) != 3 {
		t.Fatalf("opencode gateway models = %v, want the three ids", ocModels)
	}
	for id, raw := range ocModels {
		m, _ := raw.(map[string]any)
		name, named := m["name"]
		switch {
		case id == "glm-5.3":
			if name != "GLM-5.3" {
				t.Errorf("opencode %s name = %v, want the entry's own name fact", id, name)
			}
		case named:
			t.Errorf("opencode %s name = %v, want none, so opencode shows its catalog's name or the id", id, name)
		}
	}

	// pi: one row per alias today; no row may carry an alias as its name.
	piProv, _ := r.piModels(t)["providers"].(map[string]any)
	piGw, _ := piProv["gateway"].(map[string]any)
	piRows, _ := piGw["models"].([]any)
	if len(piRows) == 0 {
		t.Fatalf("pi gateway row = %v, want models", piGw)
	}
	for _, raw := range piRows {
		m, _ := raw.(map[string]any)
		switch name, named := m["name"]; {
		case m["id"] == "glm-5.3" && name == "GLM-5.3":
		case m["id"] == "glm-5.3" && named:
			t.Errorf("pi glm-5.3 name = %v, want GLM-5.3", name)
		case named && m["id"] != "glm-5.3":
			t.Errorf("pi row %v carries name %v, want none (pi shows the id)", m["id"], name)
		}
	}

	// oh-omp: models.yml rows.
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{"YOLO_PROVIDERS": aliasNamedJSON}}
	if err := ConfigurePackByName(e, "omp"); err != nil {
		t.Fatalf("ConfigurePackByName(omp): %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(e.Home, ".oh-omp", "agent", "models.yml"))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := (codec.YAML{}).Decode(raw)
	if err != nil {
		t.Fatalf("models.yml is not valid YAML: %v\n%s", err, raw)
	}
	ompProv, _ := decoded.(map[string]any)["providers"].(map[string]any)
	ompGw, _ := ompProv["gateway"].(map[string]any)
	ompRows, _ := ompGw["models"].([]any)
	if len(ompRows) == 0 {
		t.Fatalf("omp gateway row = %v, want models", ompGw)
	}
	for _, raw := range ompRows {
		m, _ := raw.(map[string]any)
		name, named := m["name"]
		switch {
		case m["id"] == "glm-5.3":
			if name != "GLM-5.3" {
				t.Errorf("omp glm-5.3 name = %v, want GLM-5.3", name)
			}
		case named:
			t.Errorf("omp row %v carries name %v, want none", m["id"], name)
		}
	}
}
