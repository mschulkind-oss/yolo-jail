package luahook

// modelfor_test.go pins yolo.model_for, the derive helper that resolves a tier alias for the
// SELECTED provider to `provider/id` (docs/research/extension-model-defaults.md OQ-XM1), and
// the fourth conventional alias, `frontier` (OQ-XM2).
//
// Each case runs a script through the production VM, so deleting the helper's registration
// in newDeriveSession fails every one of them: the strict VM refuses an unknown yolo.<name>.
// The call site in the shipped pi derive is pinned separately, through the boot render, in
// internal/entrypoint/pi_subagents_scope_test.go.

import (
	"reflect"
	"strings"
	"testing"
)

// modelForScript returns what yolo.model_for answers for one alias, both returns, so a case
// can tell "resolved" from "nil" without the Lua side inventing a spelling for nil.
const modelForScript = `
yolo.derive("probe", "settings", function(ctx)
  local qualified, id = yolo.model_for(ctx.profile.alias)
  return { qualified = qualified or "<nil>", id = id or "<nil>" }
end)`

// tieredProvider declares every conventional alias, plus one of its own, and a neighbour
// that declares aliases the selected provider lacks — the provider-mismatch fixture.
func modelForCtx(selected, alias string, warn func(string)) *DeriveCtx {
	return &DeriveCtx{
		Agent:            "probe",
		Surface:          "settings",
		SelectedProvider: selected,
		Profile:          map[string]string{"alias": alias},
		Warn:             warn,
		Tables: map[string]map[string]any{
			"providers": {
				"tiered": map[string]any{"models": map[string]any{
					"default":  "tier-default",
					"fast":     "tier-fast",
					"balanced": "tier-balanced",
					"frontier": "tier-frontier",
					"mine":     "vendor/tier-mine",
				}},
				"partial": map[string]any{"models": map[string]any{"default": "partial-default"}},
				"bare":    map[string]any{"endpoints": map[string]any{}},
			},
		},
	}
}

func runModelFor(t *testing.T, selected, alias string) (map[string]any, []string) {
	t.Helper()
	var warnings []string
	got, err := GopherLuaVM{}.Derive(modelForScript, modelForCtx(selected, alias,
		func(msg string) { warnings = append(warnings, msg) }))
	if err != nil {
		t.Fatalf("model_for(%q) under %q: %v", alias, selected, err)
	}
	return got, warnings
}

// Every conventional alias, and an open-vocabulary one, resolves to the selected provider's
// id, qualified as pi-subagents and opencode spell a model. The id keeps its own slashes
// (kilo's ids are `vendor/model`): the provider prefix is added, never parsed.
func TestModelForResolvesEachAliasForTheSelectedProvider(t *testing.T) {
	for alias, id := range map[string]string{
		"default":  "tier-default",
		"fast":     "tier-fast",
		"balanced": "tier-balanced",
		"frontier": "tier-frontier",
		"mine":     "vendor/tier-mine",
	} {
		got, warnings := runModelFor(t, "tiered", alias)
		want := map[string]any{"qualified": "tiered/" + id, "id": id}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("model_for(%q) = %#v, want %#v", alias, got, want)
		}
		if len(warnings) != 0 {
			t.Errorf("model_for(%q) resolved and still warned: %v", alias, warnings)
		}
	}
}

// A conventional alias the selected provider does not declare resolves to nil and WARNS,
// naming the provider and the alias. It never refuses: the derive runs to completion.
// `frontier` is one of the four (OQ-XM2), so it warns like the other three.
func TestModelForWarnsWithoutRefusingForAMissingConventionalAlias(t *testing.T) {
	for _, alias := range []string{"fast", "balanced", "frontier"} {
		got, warnings := runModelFor(t, "partial", alias)
		if want := map[string]any{"qualified": "<nil>", "id": "<nil>"}; !reflect.DeepEqual(got, want) {
			t.Errorf("model_for(%q) on a provider without it = %#v, want nil", alias, got)
		}
		if len(warnings) != 1 {
			t.Fatalf("model_for(%q) on a provider without it warned %d times, want once: %v",
				alias, len(warnings), warnings)
		}
		for _, want := range []string{`"partial"`, `"` + alias + `"`, "frontier"} {
			if !strings.Contains(warnings[0], want) {
				t.Errorf("the warning for a missing %q must name %s: %q", alias, want, warnings[0])
			}
		}
	}
	// A provider with no models map at all is missing every alias the same way.
	if _, warnings := runModelFor(t, "bare", "default"); len(warnings) != 1 {
		t.Errorf("model_for(\"default\") on a provider with no models warned %v, want once", warnings)
	}
}

// An alias outside the convention is open vocabulary: absent, it is nil and SILENT, because a
// derive may probe a name (an exact model id a profile states) that no provider is expected
// to declare.
func TestModelForIsSilentForAMissingOpenVocabularyAlias(t *testing.T) {
	got, warnings := runModelFor(t, "partial", "glm-5.3")
	if want := map[string]any{"qualified": "<nil>", "id": "<nil>"}; !reflect.DeepEqual(got, want) {
		t.Errorf("model_for(\"glm-5.3\") = %#v, want nil", got)
	}
	if len(warnings) != 0 {
		t.Errorf("an open-vocabulary alias warned: %v", warnings)
	}
}

// PROVIDER MISMATCH: the helper answers for the SELECTED provider only. Another provider in
// the table declaring the alias is never borrowed from, since a child agent handed that id
// would cross providers — the one thing OQ-XM3's ruling forbids. With no provider selected
// there is nothing to resolve against, and nothing to warn about.
func TestModelForNeverResolvesAgainstAnotherProvider(t *testing.T) {
	got, warnings := runModelFor(t, "partial", "frontier")
	if got["qualified"] != "<nil>" {
		t.Errorf("model_for(\"frontier\") under partial = %v; tiered declares it, and the "+
			"helper must not borrow another provider's model", got["qualified"])
	}
	if len(warnings) != 1 {
		t.Errorf("want one missing-alias warning, got %v", warnings)
	}

	got, warnings = runModelFor(t, "", "default")
	if got["qualified"] != "<nil>" || len(warnings) != 0 {
		t.Errorf("with no provider selected, model_for = %v (warnings %v), want nil and silent",
			got, warnings)
	}
	got, warnings = runModelFor(t, "ghost", "default")
	if got["qualified"] != "<nil>" || len(warnings) != 0 {
		t.Errorf("a selected provider the table does not hold: model_for = %v (warnings %v), "+
			"want nil and silent — the launch's own gate reports that provider", got, warnings)
	}
}

// A nil Warn drops the note rather than failing the derive, for the readers that render no
// warnings of their own (the via-pointer scan and the env composition, whose boot twin reports).
func TestModelForToleratesANilWarn(t *testing.T) {
	got, err := GopherLuaVM{}.Derive(modelForScript, modelForCtx("partial", "fast", nil))
	if err != nil {
		t.Fatal(err)
	}
	if got["qualified"] != "<nil>" {
		t.Errorf("model_for(\"fast\") under partial = %v, want nil", got["qualified"])
	}
}

// The four conventional aliases are one list, in the order the docs publish them.
func TestConventionalModelAliasesIncludeFrontier(t *testing.T) {
	want := []string{"default", "fast", "balanced", "frontier"}
	if !reflect.DeepEqual(ConventionalModelAliases, want) {
		t.Errorf("ConventionalModelAliases = %v, want %v", ConventionalModelAliases, want)
	}
}
