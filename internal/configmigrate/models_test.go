package configmigrate

import (
	"path/filepath"
	"strings"
	"testing"
)

// models_test.go pins the model rows' conversion and folds (docs/design/config-migration.md §4
// item 4, docs/design/readable-config.md OQ-RC18, RC-D27, RC-D29): a legacy row keyed by its own
// id becomes a readable row, a `vendor` the id implies is dropped, and a fact every row of a
// provider repeats moves into its `every_model`, each only while the provider table a launch
// resolves stays the same.

// legacyBedrockRows is the shape of the maintainer's own Bedrock rows: each keyed by its id, each
// writing its maker, three facts the same on every row and two that differ.
const legacyBedrockRows = `{
  "providers": {"bedrock": {"models": {
    // the big one
    "global.anthropic.claude-haiku-5-5": {"id": "global.anthropic.claude-haiku-5-5", "name": "Haiku 5.5",
      "vendor": "anthropic", "context_window": 200000, "max_tokens": 64000, "reasoning": true, "input": ["text", "image"]},
    "global.anthropic.claude-opus-5-5": {"id": "global.anthropic.claude-opus-5-5", "name": "Opus 5.5",
      "vendor": "anthropic", "context_window": 1000000, "max_tokens": 64000, "reasoning": true, "input": ["text", "image"],
      "cost": {"input": 5, "output": 25, "cache_read": 0.5, "cache_write": 6.25}},
    "global.openai.gpt-6-luna": {"id": "global.openai.gpt-6-luna", "name": "Luna",
      "vendor": "openai", "context_window": 400000, "max_tokens": 64000, "reasoning": true, "input": ["text", "image"],
      "cost": {"input": 1, "output": 8, "cache_read": 0.1, "cache_write": 0}}
  }}}
}
`

func TestPlanConvertsLegacyModelRowsAndFoldsRepeatedFacts(t *testing.T) {
	home := env(t)
	user := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	write(t, user, legacyBedrockRows)
	p := plan(t, Request{UserConfigDir: filepath.Dir(user)})
	if !p.Equal {
		t.Fatalf("plan unequal: %+v\n%+v", p.Differences, p.NotConverted)
	}
	tg := target(t, p, user)
	got := string(tg.Bytes)
	for _, want := range []string{
		"[providers.bedrock.every_model]",
		"max_tokens = 64000",
		"reasoning = true",
		`accepts = ["text", "image"]`,
		`[providers.bedrock.models."global.anthropic.claude-opus-5-5"]`,
		`name = "Opus 5.5"`,
		"context = 1000000",
		"cache_write = 6.25",
		"# the big one",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("converted file lacks %q:\n%s", want, got)
		}
	}
	for _, gone := range []string{"vendor", "id =", "context_window", "input = [", "cost"} {
		if strings.Contains(got, gone) {
			t.Errorf("converted file still says %q:\n%s", gone, got)
		}
	}
	if strings.Count(got, "max_tokens") != 1 {
		t.Errorf("the repeated max_tokens was not folded into every_model once:\n%s", got)
	}
	folded := map[string]bool{}
	for _, f := range tg.Folded {
		folded[f.Path] = true
	}
	for _, want := range []string{"providers.bedrock.every_model.max_tokens", "providers.bedrock.every_model.reasoning", "providers.bedrock.every_model.accepts"} {
		if !folded[want] {
			t.Errorf("Folded lacks %s: %+v", want, tg.Folded)
		}
	}
	if transcript := p.Transcript(); !strings.Contains(transcript, `"providers.bedrock.every_model.max_tokens": written on each of 3 models, folded into one`) {
		t.Errorf("the transcript does not name the folds:\n%s", transcript)
	}
	// Apply reads the written file back through the same proof, the provider table included.
	if _, err := Apply(p, ApplyOptions{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
}

// A FOLD THAT WOULD REACH A PACK'S ALIAS is not made: zai's pack ships `glm-5.3-flash` with no
// window, so an `every_model.context` would give it one. The rows still convert, each keeping its
// own facts.
func TestPlanKeepsAFactOnEachRowWhenEveryModelWouldReachAPacksAlias(t *testing.T) {
	home := env(t)
	user := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	write(t, user, `{
  "providers": {"zai": {"models": {
    "glm-4.6": {"id": "glm-4.6", "context_window": 200000},
    "glm-5.3": {"id": "glm-5.3", "context_window": 200000}
  }}}
}
`)
	p := plan(t, Request{UserConfigDir: filepath.Dir(user)})
	if !p.Equal {
		t.Fatalf("plan unequal: %+v\n%+v", p.Differences, p.NotConverted)
	}
	tg := target(t, p, user)
	got := string(tg.Bytes)
	if strings.Contains(got, "every_model") || strings.Count(got, "context = 200000") != 2 || len(tg.Folded) != 0 {
		t.Errorf("the fold reached the pack's own alias:\n%s", got)
	}
}

// A CONVERSION THAT WOULD REORDER A MODEL LIST refuses: a readable row is listed in its place
// among the rows, ahead of a pack's alias, where the legacy row was listed by name. zai's pack
// ships `glm-4.6`, which the old list put first.
func TestPlanRefusesAConversionThatWouldReorderAModelList(t *testing.T) {
	home := env(t)
	user := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	write(t, user, `{"providers": {"zai": {"models": {"glm-5.3": {"id": "glm-5.3", "context_window": 200000}}}}}
`)
	g, err := Discover(Request{UserConfigDir: filepath.Dir(user)})
	if err != nil {
		t.Fatal(err)
	}
	p, err := Plan(g)
	if err == nil || p.Equal {
		t.Fatalf("a reordered model list must not prove equal: %v", err)
	}
	if !strings.Contains(err.Error(), "the provider table the launch resolves: zai.(model order)") {
		t.Errorf("the refusal does not name the order:\n%v", err)
	}
}
