package luahook

// activeset_test.go pins the derive side of the ACTIVE SET (docs/design/active-provider-sets.md
// §4.3): ctx.active_set exposes every entry of an agent's set in order, and yolo.model_for's
// optional provider answers for an entry of the set and for nothing outside it. Each case runs
// through the production VM, so removing the ctx field or the argument fails it.

import (
	"reflect"
	"testing"
)

func activeSetCtx() *DeriveCtx {
	c := modelForCtx("tiered", "default", nil)
	c.ActiveSet = []SetEntry{
		{ProfileName: "t", Provider: "tiered", Profile: map[string]string{"alias": "default"}},
		{ProfileName: "p", Provider: "partial", Profile: map[string]string{"model": "x"}},
	}
	c.Tables["providers"]["partial"].(map[string]any)["platform"] = "acme"
	return c
}

// ctx.active_set is a list of one table per entry, in set order, each carrying the fields ctx
// carries for the primary: the profile name, the provider, its row's platform and the options.
func TestTheActiveSetReachesTheDeriveInOrder(t *testing.T) {
	got, err := GopherLuaVM{}.Derive(`
yolo.derive("probe", "settings", function(ctx)
  local out = {}
  for i, e in ipairs(ctx.active_set) do
    out["e" .. i] = e.profile_name .. "|" .. e.provider .. "|" .. e.platform .. "|" ..
      (e.profile.model or e.profile.alias or "")
  end
  out.n = tostring(#ctx.active_set)
  return out
end)`, activeSetCtx())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"n": "2", "e1": "t|tiered||default", "e2": "p|partial|acme|x"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ctx.active_set read back as %#v, want %#v", got, want)
	}
	// No selection is an empty list, so a derive can iterate it unguarded.
	got, err = GopherLuaVM{}.Derive(`
yolo.derive("probe", "settings", function(ctx) return { n = tostring(#ctx.active_set) } end)`,
		modelForCtx("", "default", nil))
	if err != nil {
		t.Fatal(err)
	}
	if got["n"] != "0" {
		t.Errorf("no selection must expose an empty ctx.active_set, got %v", got)
	}
}

// Each entry carries ITS OWN profile's model-list switch as `enforce_models` (MM-D5 read for a
// set, AP-P1): on by default, off for an entry whose profile says `enforce_models: false`, whatever
// the primary's says. A derive that renders a refusal for a narrowed list on every entry's
// provider (opencode's whitelist) reads it here.
func TestEachSetEntryCarriesItsOwnModelSwitch(t *testing.T) {
	ctx := activeSetCtx()
	ctx.ActiveSet[1].ModelsNotEnforced = true
	got, err := GopherLuaVM{}.Derive(`
yolo.derive("probe", "settings", function(ctx)
  return { primary = tostring(ctx.enforce_models), e1 = tostring(ctx.active_set[1].enforce_models),
    e2 = tostring(ctx.active_set[2].enforce_models) }
end)`, ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"primary": "true", "e1": "true", "e2": "false"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the set's switches read back as %#v, want %#v", got, want)
	}
}

// yolo.model_for(alias, provider) answers for an entry of the agent's set, and nil for a provider
// outside it even when that provider declares the alias (XM-D1, read for a set).
func TestModelForAnswersForAnEntryOfTheSetOnly(t *testing.T) {
	script := `
yolo.derive("probe", "settings", function(ctx)
  local q1 = yolo.model_for("default", "partial")
  local q2 = yolo.model_for("default", "bare")
  local q3 = yolo.model_for("default", "tiered")
  local q4 = yolo.model_for("default")
  return { entry = q1 or "<nil>", outside = q2 or "<nil>", primary = q3 or "<nil>", bare = q4 or "<nil>" }
end)`
	ctx := activeSetCtx()
	ctx.Tables["providers"]["bare"] = map[string]any{"models": map[string]any{"default": "bare-default"}}
	got, err := GopherLuaVM{}.Derive(script, ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"entry": "partial/partial-default", "outside": "<nil>",
		"primary": "tiered/tier-default", "bare": "tiered/tier-default"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("model_for over a set = %#v, want %#v", got, want)
	}
}
