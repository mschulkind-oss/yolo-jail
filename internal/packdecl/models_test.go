package packdecl

// models_test.go pins the `models` kind's DECLARATION (docs/design/model-lists-and-pickers.md
// §7, OQ-BR12): the shape it decodes into and travels in, and every refusal its validation
// makes. What a composition does with it is packload's and is tested there
// (modellists_test.go).

import (
	"strings"
	"testing"
)

func decodeModelsEntry(t *testing.T, entry string) (*Manifest, []string) {
	t.Helper()
	return Decode([]byte(`{"name": "company", "contributes": [` + entry + `]}`))
}

// Both verbs decode clean and reach the projection whole, in declaration order, with every
// field of an entry: a field that decodes but never leaves the Contribution is a fact no
// derive can render.
func TestModelsDecodesAndTravels(t *testing.T) {
	m, problems := Decode([]byte(`{"name": "company", "contributes": [
		{"kind": "models", "provider": "bedrock", "add": [
			{"id": "global.moonshot.kimi-k3", "vendor": "moonshot", "alias": "kimi",
			 "name": "Kimi K3", "description": "Long context", "context_window": 256000,
			 "max_tokens": 32000, "reasoning": true, "input": ["text"],
			 "cost": {"input": 0.6, "output": 2.5, "cache_read": 0, "cache_write": 0}},
			{"id": "global.anthropic.claude-opus-5-5", "vendor": "anthropic"}]},
		{"kind": "models", "provider": "bedrock", "only": ["global.moonshot.kimi-k3"]}
	]}`))
	if len(problems) != 0 {
		t.Fatalf("valid models contributions were refused: %v", problems)
	}
	got := m.ModelsContributions()
	if len(got) != 2 {
		t.Fatalf("ModelsContributions() = %d entries, want 2", len(got))
	}
	if got[0].Provider != "bedrock" || len(got[0].Add) != 2 || len(got[0].Only) != 0 {
		t.Fatalf("entry 0 = %+v, want bedrock with two added models", got[0])
	}
	kimi := got[0].Add[0]
	if kimi.ID != "global.moonshot.kimi-k3" || kimi.Vendor != "moonshot" || kimi.Alias != "kimi" ||
		kimi.Name != "Kimi K3" || kimi.Description != "Long context" || kimi.ContextWindow != 256000 ||
		kimi.MaxTokens != 32000 || kimi.Reasoning == nil || !*kimi.Reasoning ||
		len(kimi.Input) != 1 || kimi.Cost == nil || kimi.Cost.Output == nil || *kimi.Cost.Output != 2.5 {
		t.Errorf("the first added entry lost a field on the way out: %+v", kimi)
	}
	if got[1].Provider != "bedrock" || len(got[1].Only) != 1 || got[1].Only[0] != "global.moonshot.kimi-k3" {
		t.Errorf("entry 1 = %+v, want bedrock keeping only Kimi", got[1])
	}
	if fp, ok := FootprintOf(KindModels); !ok || fp.Combine != CombineOverlay || fp.MayBeReviewWorthy {
		t.Errorf("the models footprint = %+v, want an overlay that is never review-worthy", fp)
	}
}

func TestModelsRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, entry, want string
	}{
		{"no provider", `{"kind": "models", "only": ["a"]}`, `needs "provider"`},
		{"no verb", `{"kind": "models", "provider": "p"}`, `needs "add"`},
		{"both verbs", `{"kind": "models", "provider": "p", "only": ["a"],
			"add": [{"id": "b", "vendor": "v"}]}`, `one verb`},
		{"an entry with no id", `{"kind": "models", "provider": "p", "add": [{"vendor": "v"}]}`, `needs "id"`},
		{"an entry with no vendor", `{"kind": "models", "provider": "p", "add": [{"id": "m"}]}`, `needs "vendor"`},
		{"an uppercase vendor", `{"kind": "models", "provider": "p", "add": [{"id": "m", "vendor": "Moonshot"}]}`,
			`one lowercase token`},
		{"an unknown entry field", `{"kind": "models", "provider": "p", "add": [{"id": "m", "vendor": "v", "size": 3}]}`,
			`unknown field`},
		{"an id added twice", `{"kind": "models", "provider": "p", "add": [{"id": "m", "vendor": "v"},
			{"id": "m", "vendor": "v"}]}`, `added twice`},
		{"an alias naming two entries", `{"kind": "models", "provider": "p", "add": [
			{"id": "m", "vendor": "v", "alias": "default"}, {"id": "n", "vendor": "v", "alias": "default"}]}`,
			`names two entries`},
		{"a modality outside the set", `{"kind": "models", "provider": "p", "add": [
			{"id": "m", "vendor": "v", "input": ["audio"]}]}`, `not "text" or "image"`},
		{"a partial cost", `{"kind": "models", "provider": "p", "add": [
			{"id": "m", "vendor": "v", "cost": {"input": 1}}]}`, `cost.output is required`},
		{"an empty add", `{"kind": "models", "provider": "p", "add": []}`, `adds nothing`},
		{"an id kept twice", `{"kind": "models", "provider": "p", "only": ["m", "m"]}`, `listed twice`},
		{"only on another kind", `{"kind": "env", "vars": {"A": "1"}, "only": ["m"]}`, `does not take "only"`},
		{"add on another kind", `{"kind": "env", "vars": {"A": "1"}, "add": [{"id": "m", "vendor": "v"}]}`,
			`does not take "add"`},
		{"enforce_models off a profile", `{"kind": "models", "provider": "p", "only": ["m"], "enforce_models": false}`,
			`does not take "enforce_models"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, problems := decodeModelsEntry(t, tc.entry)
			if !strings.Contains(strings.Join(problems, "\n"), tc.want) {
				t.Errorf("problems = %v, want one containing %q", problems, tc.want)
			}
		})
	}
}
