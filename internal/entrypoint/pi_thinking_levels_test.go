package entrypoint

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// A DECLARED ROW'S `thinking` REACHES PI AS ITS thinkingLevelMap (docs/design/model-lists-and-pickers.md
// ML-D17; the downstream handoff's patch series 9). A row carrying a `base` or a `wire_id` stays a
// full `models.json` row, which replaces pi's catalog entry and so loses the catalog's levels, and
// the derive cannot read pi's catalog to copy them back. So the row says its own: every level it
// lists maps to itself, and every other level, `off` included, is null (pi's "unsupported"), which
// is what leaves `xhigh` and `max` offered only when listed (pi-ai getSupportedThinkingLevels).
const thinkingBedrockRows = `{"bedrock":{"region":"us-east-1","models":{
  "custom":{"model":"global.openai.gpt-6-luna@custom","vendor":"openai","base":"global.openai.gpt-6-luna",
    "reasoning":true,"thinking":["minimal","low","medium","high","xhigh"]},
  "luna":{"model":"global.openai.gpt-6-luna","vendor":"openai","thinking":["low","high","xhigh","max"]},
  "plain":{"model":"global.openai.gpt-6-astra","vendor":"openai","base":"global.openai.gpt-6-astra"}}}}`

func TestPiWritesADeclaredRowsThinkingLevelsAsItsLevelMap(t *testing.T) {
	providersJSON, wire := bedrockTables(t, "pi", thinkingBedrockRows, nil)
	r := newPioencodeRender(t, providersJSON)
	r.wireProfiles(wire)
	r.render(t, `{"pi":"bedrock"}`)
	rows, _ := r.piModels(t)["providers"].(map[string]any)
	native, _ := rows["amazon-bedrock"].(map[string]any)
	if native == nil {
		t.Fatalf("no amazon-bedrock row: %v", rows)
	}
	models := map[string]map[string]any{}
	list, _ := native["models"].([]any)
	for _, m := range list {
		row, _ := m.(map[string]any)
		id, _ := row["id"].(string)
		models[id] = row
	}
	want := map[string]any{"off": nil, "minimal": "minimal", "low": "low", "medium": "medium",
		"high": "high", "xhigh": "xhigh", "max": nil}
	if got := models["global.openai.gpt-6-luna@custom"]["thinkingLevelMap"]; !reflect.DeepEqual(got, want) {
		t.Errorf("a base-bearing row's thinkingLevelMap = %v, want %v", got, want)
	}
	if row := models["global.openai.gpt-6-astra"]; row == nil || row["thinkingLevelMap"] != nil {
		t.Errorf("a base-bearing row stating no thinking = %v, want no thinkingLevelMap", row)
	}
	// A catalog-id row that states levels is a full row too: a modelOverrides entry is merged
	// object-wise, and the nulls that mark a level unsupported do not survive that layer.
	overrides, _ := native["modelOverrides"].(map[string]any)
	if _, overridden := overrides["global.openai.gpt-6-luna"]; overridden {
		t.Errorf("luna states its levels, which an override cannot carry: %v", overrides)
	}
	wantLuna := map[string]any{"off": nil, "minimal": nil, "low": "low", "medium": nil,
		"high": "high", "xhigh": "xhigh", "max": "max"}
	if got := models["global.openai.gpt-6-luna"]["thinkingLevelMap"]; !reflect.DeepEqual(got, wantLuna) {
		t.Errorf("luna's thinkingLevelMap = %v, want %v", got, wantLuna)
	}
	if _, stray := models["global.openai.gpt-6-luna@custom"]["thinking_levels"]; stray {
		t.Errorf("the row carries the yolo spelling, which pi reads nothing from")
	}

	// The menu file carries the same map, nulls included, for the extension to register.
	menu, _ := piModelListsFile(t, r)["amazon-bedrock"]["models"].([]any)
	var menuMap any
	for _, row := range menu {
		if m, _ := row.(map[string]any); m["id"] == "global.openai.gpt-6-luna@custom" {
			menuMap = m["thinkingLevelMap"]
		}
	}
	if !reflect.DeepEqual(menuMap, want) {
		t.Errorf("menu row thinkingLevelMap = %v, want %v", menuMap, want)
	}

	// AND STOCK pi, where it is installed: it loads the file, composes its own catalog with it,
	// and offers the custom row's `xhigh` and not its unlisted `max`.
	pkg := installedPiPackage(t)
	dir := t.TempDir()
	modelsPath := filepath.Join(dir, "models.json")
	raw, err := json.Marshal(r.piModels(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modelsPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	harness := filepath.Join(dir, "harness.mjs")
	if err := os.WriteFile(harness, []byte(piComposeHarness), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(requireNode(t, "pi's own provider composition"), harness, pkg, modelsPath,
		"global.openai.gpt-6-luna@custom").CombinedOutput()
	if err != nil {
		t.Fatalf("harness: %v\n%s", err, out)
	}
	var composed map[string]struct {
		Error            string         `json:"error"`
		ThinkingLevelMap map[string]any `json:"thinkingLevelMap"`
	}
	if err := json.Unmarshal(out, &composed); err != nil {
		t.Fatalf("harness output %s: %v", out, err)
	}
	custom := composed["global.openai.gpt-6-luna@custom"]
	if custom.Error != "" {
		t.Fatalf("pi refused the rendered models.json: %s", custom.Error)
	}
	if custom.ThinkingLevelMap["xhigh"] != "xhigh" || custom.ThinkingLevelMap["max"] != nil {
		t.Errorf("pi's composed custom row = %+v, want xhigh offered and max not", custom)
	}
}
