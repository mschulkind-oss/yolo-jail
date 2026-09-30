package integration

// codex_model_list_test.go is the integration tier of the ONE openai-codex model list
// (docs/design/model-lists-and-pickers.md ML-D1): a real `-p codex` launch of the three
// agent packs that consume it renders the same list into every file those agents read,
// after config resolution, pack staging, provider composition and the jail's boot render
// have all had a chance to drop or mutate it. The unit pin
// (internal/entrypoint/codex_model_list_test.go) drives the same derives through the boot
// loop and the shipped pi extension; only a launch proves the table the launch composed
// reaches them. Selecting a pack renders its surfaces and installs no CLI, so no vendor
// install and no agent run happens here.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestCodexProfileRendersOneModelListForEveryAgent(t *testing.T) {
	requireJail(t)

	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["pi", "claude", "codex"]}`)
	// runCommand rather than runYolo: the flag goes BEFORE the `--` that starts the
	// container command. A bare name selects the profile for every agent.
	r := runCommand(t, dir, append(jailRunArgs(), "-p", "codex", "--", "true"))
	if r.rc != 0 {
		t.Fatalf("-p codex launch failed: rc %d\n%s", r.rc, r.combined())
	}

	decode := func(what string, raw []byte) map[string]any {
		t.Helper()
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("parsing %s: %v\n%s", what, err, raw)
		}
		return m
	}

	// pi's extension registers exactly the entries of this file (the pi/codex-models
	// surface), so its ids ARE pi's openai-codex models.
	file := decode("the pi codex-models file", renderedSurface(t, dir, "pi", "agent", "yolo-openai-codex-models.json"))
	entries, _ := file["models"].([]any)
	var ids []any
	for i, raw := range entries {
		e, _ := raw.(map[string]any)
		id, _ := e["id"].(string)
		ids = append(ids, id)
		// A 1M variant is listed right after its base, naming it.
		if base, isVariant := e["base"].(string); isVariant {
			if id != base+"[1m]" || i == 0 || ids[i-1] != base {
				t.Errorf("entry %d = %v: a [1m] variant must follow its base and be named for it", i, e)
			}
		}
	}
	if len(ids) == 0 {
		t.Fatalf("the pi codex-models file lists no models, so nothing below measures anything: %v", file)
	}
	// The codex profile's model-list switch, on by default, travels beside the list, and with it
	// pi's extension refuses a model outside the list on the subscription, as claude's allowlist
	// below does (docs/design/model-lists-and-pickers.md MM-D23).
	if file["enforce"] != true {
		t.Errorf("the pi codex-models file's enforce = %v, want true: the codex profile does not "+
			"turn enforce_models off, so pi must refuse a model outside the list", file["enforce"])
	}
	first, _ := ids[0].(string)

	claude := decode("claude settings.json", renderedSurface(t, dir, "claude", "settings.json"))
	if !reflect.DeepEqual(claude["availableModels"], ids) {
		t.Errorf("claude availableModels = %v, and pi registers %v — two lists for one subscription",
			claude["availableModels"], ids)
	}

	pi := decode("pi settings.json", renderedSurface(t, dir, "pi", "agent", "settings.json"))
	if pi["defaultProvider"] != "openai-codex" || pi["defaultModel"] != first {
		t.Errorf("pi selection = %v/%v, want openai-codex/%s", pi["defaultProvider"], pi["defaultModel"], first)
	}
	if scoped, present := pi["enabledModels"]; present {
		t.Errorf("pi settings.json carries enabledModels %v; for openai-codex pi's \"all\" view is "+
			"the registered list and yolo writes no scope (ML-D2)", scoped)
	}
	sub, _ := pi["subagents"].(map[string]any)
	scope, _ := sub["modelScope"].(map[string]any)
	var wantAllow []any
	for _, id := range ids {
		wantAllow = append(wantAllow, "openai-codex/"+id.(string))
	}
	if !reflect.DeepEqual(scope["allow"], wantAllow) {
		t.Errorf("pi subagents.modelScope.allow = %v, want %v", scope["allow"], wantAllow)
	}

	config := string(renderedSurface(t, dir, "codex", "config.toml"))
	if m := codexModelAssign.FindStringSubmatch(config); m == nil || m[1] != first {
		t.Errorf("codex config.toml model = %v, want the first listed id %s:\n%s", m, first, config)
	}
	if strings.Contains(config, "[model_providers.openai-codex]") {
		t.Errorf("codex config.toml catalogs openai-codex, which codex implements natively:\n%s", config)
	}

	// codex's model menu is written from this list by its launcher before codex starts
	// (docs/design/model-lists-and-pickers.md MM-D9, MM-D22), so the launch must render it: the
	// same ids, bases only, since codex's own catalog has no [1m] spelling.
	list := decode("the codex model-list file", renderedSurface(t, dir, "codex", "yolo-model-list.json"))
	listed, _ := list["models"].([]any)
	var codexIDs, bases []any
	for _, raw := range listed {
		e, _ := raw.(map[string]any)
		codexIDs = append(codexIDs, e["id"])
	}
	for _, raw := range entries {
		if e, _ := raw.(map[string]any); e["base"] == nil {
			bases = append(bases, e["id"])
		}
	}
	if !reflect.DeepEqual(codexIDs, bases) {
		t.Errorf("codex's model list = %v, want the subscription's ids %v, bases only", codexIDs, bases)
	}
}
