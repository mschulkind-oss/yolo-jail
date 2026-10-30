package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// modelfacts_toml_test.go pins the TOML spellings of docs/design/model-facts-migration.md: a top-level
// `model_facts` table in the row fact spellings (MF-D2), a row keyed by a name that says which
// model it is with `model` (MF-D1), and a row's `wire_id` (MF-D3).

func loadUserTOML(t *testing.T, document string) error {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	write(t, filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName), document)
	_, err := LoadReadableUserScopeConfig(true, nil)
	return err
}

func TestModelFactsIsAReadableTopLevelTable(t *testing.T) {
	if err := loadUserTOML(t, `[model_facts.floor-1017]
base = "deepseek/deepseek-v4.1-flash"
name = "DeepSeek Floor"
reasoning = true
accepts = ["text", "image"]
price = { input = 0.098, output = 0.294, cache_read = 0.00294, cache_write = 0 }
context = 1_048_576
max_tokens = 131_072
openrouter_routing = { order = ["morph", "streamlake"], allow_fallbacks = false }

[providers.openrouter.models]
floor = { model = "deepseek/deepseek-v4.1-flash-1017:floor", extends = "floor-1017" }
`); err != nil {
		t.Fatalf("a readable model_facts table was refused: %v", err)
	}
	keys := schemaKeyMap(ReadableSchemaKeys())
	for _, path := range []string{"model_facts", "model_facts.*", "model_facts.*.accepts", "model_facts.*.price.input", "model_facts.*.context", "model_facts.*.openrouter_routing"} {
		key, ok := keys[path]
		if !ok {
			t.Errorf("readable schema omits %q", path)
		} else if key.Scope != SchemaScopeUserOnly {
			t.Errorf("readable schema scope of %q = %v, want user only", path, key.Scope)
		}
	}
	// The launch's validation reads the readable spellings, and a row's `extends` finds the entry.
	if errs := modelFactsErrors(t, `{"model_facts": {"floor-1017": {"accepts": ["text"], "price": {"input": 1, "output": 2, "cache_read": 0, "cache_write": 0}, "context": 1000}},
	  "providers": {"openrouter": {"models": {"floor": {"model": "acme/floor", "extends": "floor-1017"}}}}}`); len(errs) != 0 {
		t.Errorf("readable model_facts refused by validation: %v", errs)
	}
}

func TestAModelFactsEntryRefusesWhatOnlyARouteCanSay(t *testing.T) {
	for _, key := range []string{"model", "wire_id", "twin_of", "compact_at", "extends"} {
		value := `"x"`
		if key == "compact_at" {
			value = "1000"
		}
		t.Run(key, func(t *testing.T) {
			if err := loadUserTOML(t, "[model_facts.shared]\ncontext = 1000\n"+key+" = "+value+"\n"); err == nil ||
				!strings.Contains(err.Error(), "model_facts.shared."+key) {
				t.Errorf("TOML %s in a model_facts entry: err = %v", key, err)
			}
			errs := strings.Join(modelFactsErrors(t, `{"model_facts": {"shared": {"context": 1000, "`+key+`": `+value+`}}}`), "\n")
			if !strings.Contains(errs, "config.model_facts.shared."+key) || !strings.Contains(errs, "only a route") {
				t.Errorf("validation of %s in a model_facts entry: %s", key, errs)
			}
		})
	}
}

func TestAReadableRowAcceptsModelAndWireID(t *testing.T) {
	if err := loadUserTOML(t, `[providers.openrouter.models]
floor = { model = "acme/floor", name = "Floor", context = 1_000_000 }
floor-300k = { model = "acme/floor@300k", wire_id = "acme/floor", context = 307_200 }
`); err != nil {
		t.Fatalf("a row with model and wire_id was refused: %v", err)
	}
	if errs := providerErrors(t, `{"openrouter": {"models": {"floor": {"model": "acme/floor"}, "floor-300k": {"model": "acme/floor@300k", "wire_id": "acme/floor"}}}}`); len(errs) != 0 {
		t.Errorf("validation refused model and wire_id: %v", errs)
	}
	for _, tc := range []struct{ body, want string }{
		{`{"openrouter": {"models": {"floor": {"model": ""}}}}`, "model"},
		{`{"openrouter": {"every_model": {"model": "acme/floor"}}}`, "every_model.model"},
	} {
		if errs := strings.Join(providerErrors(t, tc.body), "\n"); !strings.Contains(errs, tc.want) {
			t.Errorf("%s: want a refusal naming %q, got %s", tc.body, tc.want, errs)
		}
	}
}

func TestTheIDKeyIsRefusedNamingModel(t *testing.T) {
	err := loadUserTOML(t, `[providers.openrouter.models]
floor = { id = "acme/floor" }
`)
	if err == nil || !strings.Contains(err.Error(), "providers.*.models.*.model") {
		t.Fatalf("TOML `id` on a row: err = %v, want the refusal to name model", err)
	}
}
