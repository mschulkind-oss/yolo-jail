package entrypoint

// opencodeactiveset_test.go pins opencode's ACTIVE SET render (docs/design/active-provider-sets.md
// §4.4, §8 step 3 and AP-D15; the active set, a term that doc coins, is the ordered list of
// profiles one agent runs on for one launch): with YOLO_USE_PROFILES carrying
// `{"opencode": ["zai", "router"]}`, the boot renders a provider row for each entry,
// `enabled_providers` naming every entry in set order, and the start `model` and `small_model` of
// the first entry alone. A Bedrock entry after the first is bound to opencode's own amazon-bedrock
// client with its provider's region, and each entry keeps its own profile's model-list switch.
// Through ConfigurePackSurfaces, the entry the boot loop uses, over the real embedded opencode
// pack: deleting the surface loop's set lowering, ctx.active_set, or the derive's
// opencodeSetProviders fails these cases.

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func renderOpencodeSet(t *testing.T, use string) *pioencodeRender {
	t.Helper()
	r := newPioencodeRender(t, setProvidersJSON)
	r.wireProfiles(setProfilesWire)
	r.render(t, use)
	return r
}

func ocRows(t *testing.T, cfg map[string]any) map[string]any {
	t.Helper()
	rows, _ := cfg["provider"].(map[string]any)
	if rows == nil {
		t.Fatalf("opencode.json has no provider table: %v", cfg)
	}
	return rows
}

// A SET REACHES opencode WHOLE: both entries are live in one session (AP-P1). Each has its row,
// keyed to its own credential, and opencode's own provider filter names both, the primary first;
// a fresh session starts on the PRIMARY's default (AP-D1), its small model too.
func TestOpencodeRendersItsWholeActiveSet(t *testing.T) {
	cfg := renderOpencodeSet(t, `{"opencode":["zai","router"]}`).ocConfig(t)

	if got := strs(cfg["enabled_providers"]); !reflect.DeepEqual(got, []string{"zai", "router"}) {
		t.Errorf("enabled_providers = %v, want every entry of the set in order", got)
	}
	if cfg["model"] != "zai/glm-5.3" || cfg["small_model"] != "zai/glm-5.3" {
		t.Errorf("model = %v, small_model = %v, want the first entry's zai/glm-5.3", cfg["model"], cfg["small_model"])
	}
	rows := ocRows(t, cfg)
	for name, key := range map[string]string{"zai": "{env:ZAI_API_KEY}", "router": "{env:ROUTER_API_KEY}"} {
		row, _ := rows[name].(map[string]any)
		opts, _ := row["options"].(map[string]any)
		if opts == nil || opts["apiKey"] != key {
			t.Errorf("opencode.json %s row = %#v, want apiKey %s", name, row, key)
		}
	}
	router, _ := rows["router"].(map[string]any)
	models, _ := router["models"].(map[string]any)
	for _, id := range []string{"vendor/a", "vendor/b"} {
		if _, ok := models[id]; !ok {
			t.Errorf("router's row lacks model %s: %v", id, models)
		}
	}
	if _, leaked := cfg[selectionKey]; leaked {
		t.Errorf("opencode.json carries a literal %q table", selectionKey)
	}
}

// THE FIRST ENTRY DECIDES `model`: the same two providers in the other order start opencode on
// router's default, and the filter leads with router. Nothing else about the set moves.
func TestOpencodesFirstEntryDecidesItsStartModel(t *testing.T) {
	cfg := renderOpencodeSet(t, `{"opencode":["router","zai"]}`).ocConfig(t)
	if cfg["model"] != "router/vendor/b" || cfg["small_model"] != "router/vendor/b" {
		t.Errorf("model = %v, small_model = %v, want router's declared default, router/vendor/b",
			cfg["model"], cfg["small_model"])
	}
	if got := strs(cfg["enabled_providers"]); !reflect.DeepEqual(got, []string{"router", "zai"}) {
		t.Errorf("enabled_providers = %v, want [router zai]", got)
	}
}

// A set of one renders byte for byte what the single profile renders (AP-P1): the list spelling
// and the string spelling are one selection for opencode, as they are for pi.
func TestAnOpencodeSetOfOneRendersExactlyTheSingleProfile(t *testing.T) {
	read := func(r *pioencodeRender) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(r.e.Home, ".config", "opencode", "opencode.json"))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	single, listed := renderOpencodeSet(t, `{"opencode":"zai"}`), renderOpencodeSet(t, `{"opencode":["zai"]}`)
	if a, b := read(single), read(listed); a != b {
		t.Errorf("opencode.json differs between \"zai\" and [\"zai\"]:\n--- string\n%s\n--- list\n%s", a, b)
	}
}

// REMOVING AN ENTRY REWRITES THE FILTER WHOLE (§4.10): enabled_providers rides the selection,
// so the next boot on [zai] names zai alone rather than keeping the router it wrote before, and
// `model` stays the primary's.
func TestAnEntryLeavingOpencodesSetLeavesTheFilter(t *testing.T) {
	r := renderOpencodeSet(t, `{"opencode":["zai","router"]}`)
	r.render(t, `{"opencode":"zai"}`)
	cfg := r.ocConfig(t)
	if got := strs(cfg["enabled_providers"]); !reflect.DeepEqual(got, []string{"zai"}) {
		t.Errorf("after router left the set, enabled_providers = %v, want [zai]", got)
	}
	if cfg["model"] != "zai/glm-5.3" {
		t.Errorf("model = %v, want zai/glm-5.3", cfg["model"])
	}
}

// A BEDROCK ENTRY AFTER THE FIRST is bound to opencode's own amazon-bedrock client with ITS
// provider's region as options.region (AP-D12's "anywhere in the set", AP-D14's region read for
// an entry after the first): opencode on [zai, bedrock] starts on zai, enables both zai and
// amazon-bedrock, and carries the native row, so a switch to a Bedrock model reaches one opencode
// can call. Before the set learned the native row (opencodeNativeBedrockEntry), the Bedrock
// entry had no row at all: the generic one is never written for a Bedrock provider.
func TestOpencodeOnASetWithBedrockSecondBindsItNatively(t *testing.T) {
	const opus = "global.anthropic.claude-opus-5-5"
	providersJSON, wire := bedrockTables(t, "opencode", `{"bedrock":{"region":"eu-west-1"}}`, nil, "zai")
	r := newPioencodeRender(t, providersJSON)
	r.wireProfiles(wire)
	r.render(t, `{"opencode":["zai","bedrock"]}`)
	cfg := r.ocConfig(t)

	rows := ocRows(t, cfg)
	if rows["zai"] == nil {
		t.Errorf("the primary's row is missing: %v", rows)
	}
	if _, generic := rows["bedrock"]; generic {
		t.Errorf("a generic row was written for the Bedrock entry: %v", rows["bedrock"])
	}
	native, _ := rows["amazon-bedrock"].(map[string]any)
	if native == nil {
		t.Fatalf("no amazon-bedrock row for opencode's second entry: %v", rows)
	}
	if got := native["options"]; !reflect.DeepEqual(got, map[string]any{"region": "eu-west-1"}) {
		t.Errorf("the native row's options = %#v, want the entry's own region", got)
	}
	models, _ := native["models"].(map[string]any)
	if _, ok := models[opus]; !ok {
		t.Errorf("the native row lacks the Bedrock list's %s: %v", opus, models)
	}
	if got := strs(cfg["enabled_providers"]); !reflect.DeepEqual(got, []string{"zai", "amazon-bedrock"}) {
		t.Errorf("enabled_providers = %v, want [zai amazon-bedrock]", got)
	}
	if m, _ := cfg["model"].(string); m == "" || m[:4] != "zai/" {
		t.Errorf("a fresh session starts on the primary: model = %v", cfg["model"])
	}

	// With Bedrock FIRST the start model is the native one, and zai follows it in the filter.
	r = newPioencodeRender(t, providersJSON)
	r.wireProfiles(wire)
	r.render(t, `{"opencode":["bedrock","zai"]}`)
	cfg = r.ocConfig(t)
	if cfg["model"] != "amazon-bedrock/"+opus {
		t.Errorf("model = %v, want amazon-bedrock/%s", cfg["model"], opus)
	}
	if got := strs(cfg["enabled_providers"]); !reflect.DeepEqual(got, []string{"amazon-bedrock", "zai"}) {
		t.Errorf("enabled_providers = %v, want [amazon-bedrock zai]", got)
	}
}

// EACH ENTRY KEEPS ITS OWN MODEL-LIST SWITCH (MM-D5 read for a set, AP-P1): under an `only`, a row
// renders opencode's refusing `whitelist` while the switch of the entry on its provider is on, so
// an entry whose own profile says `enforce_models: false` gets none although the primary's switch
// is on, and the primary's row keeps its whitelist. Reading the primary's switch for every row, as
// the derive did before sets, puts the whitelist back on router.
func TestEachOpencodeSetEntryKeepsItsOwnModelSwitch(t *testing.T) {
	const narrowed = `{
  "zai":{"api_key_env_name":"ZAI_API_KEY","models_only":true,
    "models":{"default":"glm-5.3","glm-5.3":"glm-5.3"},
    "endpoints":{"openai":{"base_url":"https://api.z.ai/api/coding/paas/v4"}}},
  "router":{"api_key_env_name":"ROUTER_API_KEY","models_only":true,
    "models":{"default":"vendor/b","vendor/b":"vendor/b"},
    "endpoints":{"openai":{"base_url":"https://router.example/v1"}}}}`
	r := newPioencodeRender(t, narrowed)
	r.wireProfiles(`{"zai":{"provider":"zai"},"router":{"provider":"router","_enforce_models":"false"}}`)
	r.render(t, `{"opencode":["zai","router"]}`)
	rows := ocRows(t, r.ocConfig(t))
	zai, _ := rows["zai"].(map[string]any)
	if got := strs(zai["whitelist"]); !reflect.DeepEqual(got, []string{"glm-5.3"}) {
		t.Errorf("the primary's row whitelist = %v, want [glm-5.3] (its switch is on)", got)
	}
	router, _ := rows["router"].(map[string]any)
	if w, ok := router["whitelist"]; ok {
		t.Errorf("router's own profile turned enforcement off, but its row carries whitelist %v", w)
	}
}
