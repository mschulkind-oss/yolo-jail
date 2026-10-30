package configmigrate

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// modelfacts_test.go pins the conversion and proof of docs/design/model-facts-migration.md: a
// legacy row keyed by a name becomes a row saying `model` (MF-D1), `model_facts` and `extends`
// pass through in the row fact spellings (MF-D2), a `wire_id` twin becomes `twin_of` its
// sibling's key while the proof holds and a row with `wire_id` otherwise (MF-D3), and the
// provider-table layer compares a vendor the id names as derived (MF-D5).

// userPlan writes body as the old user config and plans its migration.
func userPlan(t *testing.T, body string) (*MigrationPlan, string) {
	t.Helper()
	home := env(t)
	user := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	write(t, user, body)
	p := plan(t, Request{UserConfigDir: filepath.Dir(user)})
	if !p.Equal {
		t.Fatalf("plan unequal: %+v\n%+v", p.Differences, p.NotConverted)
	}
	if len(p.NotConverted) != 0 {
		t.Fatalf("not converted: %+v", p.NotConverted)
	}
	return p, string(target(t, p, user).Bytes)
}

func wantLines(t *testing.T, got string, want []string, gone []string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("converted file lacks %q:\n%s", w, got)
		}
	}
	for _, g := range gone {
		if strings.Contains(got, g) {
			t.Errorf("converted file still says %q:\n%s", g, got)
		}
	}
}

// A ROW KEYED BY A NAME converts to a row with the same key saying `model` (MF-D1); it used to be
// left not converted, since a readable row was keyed by its id alone. A `vendor` the id names is
// left out as for an id-keyed row.
func TestPlanConvertsANamedRowToARowWithModel(t *testing.T) {
	p, got := userPlan(t, `{"providers": {"openrouter": {"models": {
  "opus": {"id": "acme/opus-5", "vendor": "acme", "name": "Opus", "context_window": 1000000}
}}}}
`)
	wantLines(t, got, []string{"[providers.openrouter.models.opus]", `model = "acme/opus-5"`, `name = "Opus"`, "context = 1000000"},
		[]string{"id =", "vendor", "context_window"})
	if _, err := Apply(p, ApplyOptions{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
}

// twinFixture is a tier and its 300k twin, the twin sending the tier's id and stating only its name
// and window (the maintainer's shape).
const twinFixture = `{"providers": {"openrouter": {"models": {
  "floor": {"id": "acme/m:floor", "vendor": "acme", "name": "Floor", "context_window": 1048576%s},
  "floor-300k": {"id": "acme/m:floor@300k", "wire_id": "acme/m:floor", "vendor": "acme", "name": "Floor (300k)", "context_window": 307200}
}}}}
`

func TestPlanConvertsAWireIDTwinToTwinOfTheSiblingsKey(t *testing.T) {
	p, got := userPlan(t, strings.Replace(twinFixture, "%s", "", 1))
	wantLines(t, got, []string{`twin_of = "floor"`, `model = "acme/m:floor@300k"`, `name = "Floor (300k)"`, "context = 307200"},
		[]string{"wire_id"})
	if transcript := p.Transcript(); !strings.Contains(transcript, `"providers.openrouter.models.floor-300k": written as twin_of "floor"`) {
		t.Errorf("the transcript does not name the twin:\n%s", transcript)
	}
	if _, err := Apply(p, ApplyOptions{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
}

// A twin whose sibling states a fact the twin does not would take it under `twin_of`, so the twin
// keeps `wire_id`; so does one whose wire id is no row of its provider.
func TestPlanKeepsWireIDWhenTwinOfWouldChangeTheTwin(t *testing.T) {
	_, got := userPlan(t, strings.Replace(twinFixture, "%s", `, "max_tokens": 131072`, 1))
	wantLines(t, got, []string{`wire_id = "acme/m:floor"`, `model = "acme/m:floor@300k"`}, []string{"twin_of"})

	_, got = userPlan(t, `{"providers": {"openrouter": {"models": {
  "lone-300k": {"id": "acme/m:lone@300k", "wire_id": "acme/m:lone", "vendor": "acme", "context_window": 307200}
}}}}
`)
	wantLines(t, got, []string{`wire_id = "acme/m:lone"`}, []string{"twin_of"})
}

// The plan tries `twin_of` first, and on a difference in the provider table plans again with
// `wire_id` (MF-D3).
func TestPlanFallsBackToWireIDWhenTwinOfChangesTheTable(t *testing.T) {
	home := env(t)
	user := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	write(t, user, strings.Replace(twinFixture, "%s", `, "max_tokens": 131072`, 1))
	g, err := Discover(Request{UserConfigDir: filepath.Dir(user)})
	if err != nil {
		t.Fatal(err)
	}
	first, err := planWith(g, planOptions{fold: true, inlineEnv: true, twinOf: true})
	var ineq *InequalityError
	if !errors.As(err, &ineq) || !first.differsIn(providerTableLayer) || !first.twinned() {
		t.Fatalf("twin_of over a sibling with its own max_tokens should change the table: %v", err)
	}
	p, err := Plan(g)
	if err != nil || !p.Equal || p.twinned() {
		t.Fatalf("the retry with wire_id did not prove: %v", err)
	}
}

// sharedFactsFixture is a `model_facts` entry in the legacy spellings and a row that extends it.
const sharedFactsFixture = `{
  "model_facts": {"floor-1017": {"base": "acme/m", "name": "Floor", "reasoning": true, "input": ["text", "image"],
    "cost": {"input": 0.098, "output": 0.294, "cache_read": 0.00294, "cache_write": 0},
    "context_window": 1048576, "max_tokens": 131072,
    "openrouter_routing": {"order": ["morph", "cloudflare"], "allow_fallbacks": false}}},
  "providers": {"openrouter": {"models": {
    "floor": {"id": "acme/m:floor", "vendor": "acme", "extends": "floor-1017"}
  }}}
}
`

func TestPlanConvertsModelFactsAndExtendsUnchanged(t *testing.T) {
	p, got := userPlan(t, sharedFactsFixture)
	wantLines(t, got, []string{
		"[model_facts.floor-1017]", `base = "acme/m"`, `accepts = ["text", "image"]`, "context = 1048576",
		"cache_read = 0.00294", `extends = "floor-1017"`, `model = "acme/m:floor"`, "allow_fallbacks = false",
	}, []string{"context_window", "input = [", "cost", "every_model"})
	if _, err := Apply(p, ApplyOptions{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
}

// A legacy row with no `vendor` composes with none, and its readable row infers the maker its id
// names: the proof compares that vendor as derived and the transcript names the row (MF-D5).
func TestPlanComparesAVendorTheIDNamesAsDerived(t *testing.T) {
	p, got := userPlan(t, `{"providers": {"openrouter": {"models": {
  "flash": {"id": "deepseek/flash", "context_window": 1048576}
}}}}
`)
	wantLines(t, got, []string{`model = "deepseek/flash"`}, []string{"vendor"})
	if len(p.DerivedVendors) != 1 || p.DerivedVendors[0] != (DerivedVendor{Provider: "openrouter", Model: "flash", Vendor: "deepseek"}) {
		t.Errorf("DerivedVendors = %+v", p.DerivedVendors)
	}
}

// The transcript names both what MF-D3 and MF-D5 add to a plan: each twin written as `twin_of`,
// and each vendor the launch now carries because the id names it.
func TestPlanTranscriptNamesTwinsAndDerivedVendors(t *testing.T) {
	p, _ := userPlan(t, `{"providers": {"openrouter": {"models": {
  "floor": {"id": "deepseek/m:floor", "name": "Floor"},
  "floor-300k": {"id": "deepseek/m:floor@300k", "wire_id": "deepseek/m:floor", "name": "Floor (300k)", "context_window": 307200}
}}}}
`)
	transcript := p.Transcript()
	for _, want := range []string{
		`"providers.openrouter.models.floor-300k": written as twin_of "floor"`,
		`vendor "deepseek" follows from the id of "providers.openrouter.models.floor"`,
		`vendor "deepseek" follows from the id of "providers.openrouter.models.floor-300k"`,
		"identical",
	} {
		if !strings.Contains(transcript, want) {
			t.Errorf("the transcript lacks %q:\n%s", want, transcript)
		}
	}
}

// maintainersModels is shaped like the maintainer's model sections (model-facts-migration.md
// appendix A.1's source), with made-up values: two shared entries, two named rows that extend
// them, two `wire_id` twins, a full named row, a pack-aliased `default` with facts, a no-pack
// provider's `default`, an id-keyed row, and `options.model` naming a name.
const maintainersModels = `{
  // shared facts, one entry per tier
  "model_facts": {
    "floor-1017": {"base": "deepseek/v4", "name": "Floor", "reasoning": true, "input": ["text", "image"],
      "cost": {"input": 0.1, "output": 0.3, "cache_read": 0.003, "cache_write": 0},
      "context_window": 1048576, "max_tokens": 131072,
      "openrouter_routing": {"order": ["morph", "streamlake"], "allow_fallbacks": false}},
    "nitro-1017": {"base": "deepseek/v4", "name": "Nitro", "reasoning": true, "input": ["text", "image"],
      "cost": {"input": 0.1, "output": 0.3, "cache_read": 0.003, "cache_write": 0},
      "context_window": 1048576, "max_tokens": 131072,
      "openrouter_routing": {"order": ["streamlake", "morph"], "allow_fallbacks": false}}
  },
  "providers": {
    "deepseek": {
      "api_key_env_name": "DEEPSEEK_API_KEY",
      "options": {"model": "default"},
      "models": {"default": {"id": "deepseek-flash", "name": "Flash", "reasoning": true, "input": ["text", "image"],
        "cost": {"input": 0.3, "output": 1.2, "cache_read": 0.006, "cache_write": 0}, "context_window": 1048576, "max_tokens": 384000}}
    },
    "cerebras": {"models": {"default": {"id": "qwen-3.8-27b", "name": "Qwen 27B", "reasoning": true, "input": ["text", "image"],
      "cost": {"input": 0.99, "output": 1.49, "cache_read": 0, "cache_write": 0}, "context_window": 65536, "max_tokens": 32768}}},
    "openrouter": {
      "options": {"model": "floor"},
      "models": {
        // a twin states only its name and window
        "floor": {"id": "deepseek/v4:floor", "extends": "floor-1017"},
        "floor-300k": {"id": "deepseek/v4:floor@300k", "wire_id": "deepseek/v4:floor", "extends": "floor-1017", "name": "Floor (300k)", "context_window": 307200},
        "nitro": {"id": "deepseek/v4:nitro", "extends": "nitro-1017"},
        "nitro-300k": {"id": "deepseek/v4:nitro@300k", "wire_id": "deepseek/v4:nitro", "extends": "nitro-1017", "name": "Nitro (300k)", "context_window": 307200},
        "mimo": {"id": "xiaomi/mimo", "name": "MiMo", "reasoning": true, "input": ["text", "image"],
          "cost": {"input": 0.14, "output": 0.28, "cache_read": 0.0028, "cache_write": 0}, "context_window": 1048576, "max_tokens": 131072}
      }
    },
    "openai-codex": {"models": {"gpt-6.1-sol": {"id": "gpt-6.1-sol", "name": "Sol 6.1"}}}
  }
}
`

func TestPlanConvertsTheMaintainersModelSectionsWithNothingLeft(t *testing.T) {
	p, got := userPlan(t, maintainersModels)
	wantLines(t, got, []string{
		"[model_facts.floor-1017]", "[model_facts.nitro-1017]", `accepts = ["text", "image"]`,
		`model = "deepseek-flash"`, `model = "qwen-3.8-27b"`,
		`model = "deepseek/v4:floor"`, `extends = "floor-1017"`,
		`model = "deepseek/v4:floor@300k"`, `twin_of = "floor"`, `name = "Floor (300k)"`, "context = 307200",
		`model = "deepseek/v4:nitro@300k"`, `twin_of = "nitro"`,
		`model = "xiaomi/mimo"`, `name = "Sol 6.1"`,
		`model = "floor"`, "# a twin states only its name and window", "# shared facts, one entry per tier",
	}, []string{"id =", "wire_id", "context_window", "cost", "vendor", "every_model"})
	// The rows are written in the old list's order, the names sorted.
	order := []string{"[providers.openrouter.models.floor]", "[providers.openrouter.models.floor-300k]",
		"[providers.openrouter.models.mimo]", "[providers.openrouter.models.nitro]", "[providers.openrouter.models.nitro-300k]"}
	last := -1
	for _, row := range order {
		at := strings.Index(got, row)
		if at < last {
			t.Errorf("%s is out of the old order:\n%s", row, got)
		}
		last = at
	}
	if _, err := Apply(p, ApplyOptions{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
}

// maintainersModelsTOML is the TOML a person would write by hand for maintainersModels in the
// spelling of model-facts-migration.md appendix A.1: inline rows, `model`, `twin_of`, the readable
// `model_facts`.
const maintainersModelsTOML = `[model_facts.floor-1017]
base = "deepseek/v4"
name = "Floor"
reasoning = true
accepts = ["text", "image"]
price = { input = 0.1, output = 0.3, cache_read = 0.003, cache_write = 0 }
context = 1_048_576
max_tokens = 131_072
openrouter_routing = { order = ["morph", "streamlake"], allow_fallbacks = false }

[model_facts.nitro-1017]
base = "deepseek/v4"
name = "Nitro"
reasoning = true
accepts = ["text", "image"]
price = { input = 0.1, output = 0.3, cache_read = 0.003, cache_write = 0 }
context = 1_048_576
max_tokens = 131_072
openrouter_routing = { order = ["streamlake", "morph"], allow_fallbacks = false }

[providers.deepseek]
api_key_env_name = "DEEPSEEK_API_KEY"
options = { model = "default" }

[providers.deepseek.models]
default = { model = "deepseek-flash", name = "Flash", reasoning = true, accepts = ["text", "image"], price = { input = 0.3, output = 1.2, cache_read = 0.006, cache_write = 0 }, context = 1_048_576, max_tokens = 384_000 }

[providers.cerebras.models]
default = { model = "qwen-3.8-27b", name = "Qwen 27B", reasoning = true, accepts = ["text", "image"], price = { input = 0.99, output = 1.49, cache_read = 0, cache_write = 0 }, context = 65_536, max_tokens = 32_768 }

[providers.openrouter]
options = { model = "floor" }

[providers.openrouter.models]
floor      = { model = "deepseek/v4:floor", extends = "floor-1017" }
floor-300k = { model = "deepseek/v4:floor@300k", twin_of = "floor", name = "Floor (300k)", context = 307_200 }
mimo       = { model = "xiaomi/mimo", name = "MiMo", reasoning = true, accepts = ["text", "image"], price = { input = 0.14, output = 0.28, cache_read = 0.0028, cache_write = 0 }, context = 1_048_576, max_tokens = 131_072 }
nitro      = { model = "deepseek/v4:nitro", extends = "nitro-1017" }
nitro-300k = { model = "deepseek/v4:nitro@300k", twin_of = "nitro", name = "Nitro (300k)", context = 307_200 }

[providers.openai-codex.models]
"gpt-6.1-sol" = { name = "Sol 6.1" }
`

// A HAND-WRITTEN TOML of appendix A.1's shape launch-composes to the provider table the legacy
// file it says composes to, a vendor the id names compared as derived (MF-D5): the readable
// spellings are what a launch reads, not only what the converter writes.
func TestAHandWrittenTOMLOfTheConvertedShapeComposesTheSameProviderTable(t *testing.T) {
	p, _ := userPlan(t, maintainersModels)
	legacy := p.canonical.providers
	handwritten := filepath.Join(realDir(t), "config.toml")
	write(t, handwritten, maintainersModelsTOML)
	s, err := readCanonical(nil, handwritten, "", nil, nil, "", false, planOptions{}, packload.Embedded())
	if err != nil {
		t.Fatalf("the hand-written TOML does not read: %v", err)
	}
	for _, want := range []string{`"floor-300k":"deepseek/v4:floor@300k"`, `"wire_id":"deepseek/v4:floor"`, `"model_shared"`, `"default":"qwen-3.8-27b"`} {
		if !strings.Contains(strings.ReplaceAll(legacy, " ", ""), want) {
			t.Fatalf("the legacy table lacks %s, so the comparison proves nothing: %s", want, legacy)
		}
	}
	if s.set.providers != legacy {
		t.Fatalf("the hand-written TOML composes a different provider table:\n got %s\nwant %s", s.set.providers, legacy)
	}
}

// A twin whose sibling lives in the other file keeps `wire_id` in the file it is written to, and
// the merged read twins only what a file's own conversion did, so the proof is equal (MF-D8).
func TestATwinWhoseSiblingIsInTheOtherFileKeepsWireIDAndProves(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "config.jsonc"), `{"providers": {"openrouter": {"models": {
  "floor": {"id": "acme/m:floor", "name": "Floor"}
}}}}
`)
	ws := realDir(t)
	project := filepath.Join(ws, "yolo-jail.jsonc")
	write(t, project, `{"providers": {"openrouter": {"models": {
  "floor-300k": {"id": "acme/m:floor@300k", "wire_id": "acme/m:floor", "name": "Floor"}
}}}}
`)
	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	if !p.Equal || len(p.NotConverted) != 0 {
		t.Fatalf("plan unequal: %+v\n%+v", p.Differences, p.NotConverted)
	}
	wantLines(t, string(target(t, p, project).Bytes), []string{`wire_id = "acme/m:floor"`}, []string{"twin_of"})
}

// A malformed list `extends` on a twin and its sibling is not comparable with ==; the plan
// must not panic on it.
func TestATwinWithAListExtendsDoesNotPanic(t *testing.T) {
	home := env(t)
	user := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	write(t, user, `{"providers": {"openrouter": {"models": {
  "floor": {"id": "acme/m:floor", "extends": ["m"]},
  "floor-300k": {"id": "acme/m:floor@300k", "wire_id": "acme/m:floor", "extends": ["m"]}
}}}}
`)
	g, err := Discover(Request{UserConfigDir: filepath.Dir(user)})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = Plan(g)
}
