package entrypoint

// claudemodelpins_test.go pins what claude's two derives write about MODELS on a provider yolo
// routes it to (docs/design/model-lists-and-pickers.md MM-D1 to MM-D3):
//
//   - the start model (ANTHROPIC_MODEL, CLAUDE_CODE_SUBAGENT_MODEL) is pinned only on the
//     profile's `pin_model` opt-in wherever claude's allowlist makes the session valid instead,
//     because claude returns to ANTHROPIC_MODEL at every launch and so overrode a valid `/model`
//     choice every time (MM-D3);
//   - every tier (opus, sonnet, haiku, fable) is pinned to a list id, so claude's background,
//     hook and classifier requests, which pick by tier, stay on the list (MM-D2).
//
// Both derives run through their production call sites: the settings surface through the boot
// render (ConfigurePackByName), the env through the host composition a launch runs
// (packload.AgentEnv), over a provider table packload.ComposeProviders composes from the real
// needs closure and a profile table packload.ResolveProfiles resolves, user profiles included.

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// claudeRendered is what claude's derives rendered for one launch.
type claudeRendered struct {
	settings map[string]any
	env      map[string]string
}

// renderClaudeModels composes the providers (the packs' own, then user over them), resolves
// the profiles (the packs' own, then the user's), and renders claude with `profile` active.
func renderClaudeModels(t *testing.T, packs []*packload.Pack, user *jsonx.OrderedMap,
	profiles map[string]packload.UserProfile, profile string) claudeRendered {
	t.Helper()
	providers, err := packload.ComposeProviders(user, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles(packs, profiles, providers)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := newClaudePrismEnv(t, map[string]string{
		"YOLO_PROVIDERS":    mustCompactJSON(t, providers),
		"YOLO_USE_PROFILES": `{"claude":"` + profile + `"}`,
		"YOLO_PROFILES":     mustCompactJSON(t, packload.ProfilesWireTable(resolved)),
	})
	if err := ConfigurePackByName(e, "claude"); err != nil {
		t.Fatal(err)
	}
	var got claudeRendered
	got.settings = decodeJSONFile(t, filepath.Join(e.ClaudeDir(), "settings.json"))
	vars, err := packload.AgentEnv(packs, providers, map[string]string{"claude": profile},
		"claude", profile, func(string) (string, bool) { return "", false },
		packload.WithResolvedProfiles(resolved))
	if err != nil {
		t.Fatal(err)
	}
	got.env = map[string]string{}
	for _, v := range vars {
		got.env[v.Key] = v.Value
	}
	return got
}

var claudeTierVars = []string{
	"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL",
	"ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL",
}

// A PROFILE NAMING ANOTHER ENTRY MAKES THAT ENTRY THE DEFAULT, and only `pin_model` pins it as
// the start. `model` keeps its job of naming the list's default entry (MM-D3): the tier pins and
// availableModels' order lead with it. A profile over openai-codex declares no option census
// (the provider declares no options, ML-D1), so the opt-in needs no declaration there.
func TestClaudeOnTheCodexListPinsTheStartOnlyOnTheOptIn(t *testing.T) {
	packs := testPacksForAgent(t, "claude")
	for _, tc := range []struct {
		name    string
		options map[string]string
		pinned  bool
	}{
		{"a profile naming Astra", map[string]string{"model": "gpt-6-astra"}, false},
		{"a profile naming Astra and opting in", map[string]string{"model": "gpt-6-astra", "pin_model": "true"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := renderClaudeModels(t, packs, nil, map[string]packload.UserProfile{
				"astra": {Provider: "openai-codex", Options: tc.options},
			}, "astra")
			for _, k := range claudeTierVars {
				if got.env[k] != "gpt-6-astra" {
					t.Errorf("%s = %q, want gpt-6-astra, the entry the profile names", k, got.env[k])
				}
			}
			if got.env["ANTHROPIC_DEFAULT_OPUS_MODEL_NAME"] != "GPT-6 Astra" {
				t.Errorf("ANTHROPIC_DEFAULT_OPUS_MODEL_NAME = %q, want the default entry's name",
					got.env["ANTHROPIC_DEFAULT_OPUS_MODEL_NAME"])
			}
			for _, k := range []string{"ANTHROPIC_MODEL", "CLAUDE_CODE_SUBAGENT_MODEL"} {
				v, set := got.env[k]
				switch {
				case tc.pinned && v != "gpt-6-astra":
					t.Errorf("%s = %q, want gpt-6-astra: the profile opted in with pin_model", k, v)
				case !tc.pinned && set:
					t.Errorf("%s = %q, want it unset without pin_model — claude returns to it at "+
						"every launch, over a valid /model choice", k, v)
				}
			}
			available, _ := got.settings["availableModels"].([]any)
			if len(available) == 0 || available[0] != "gpt-6-astra" {
				t.Errorf("availableModels = %v, want the default entry first", available)
			}
			// The picker keeps the list's own order: the default leads the allowlist, not the menu.
			picker, _ := got.settings["modelPicker"].(map[string]any)
			options, _ := picker["options"].([]any)
			var ids []any
			for _, o := range options {
				ids = append(ids, o.(map[string]any)["model"])
			}
			if want := []any{"gpt-6.1-sol", "gpt-6.1-sol[1m]", "gpt-6-astra", "gpt-6-astra[1m]", "gpt-6-luna", "gpt-6-luna[1m]"}; !reflect.DeepEqual(ids, want) {
				t.Errorf("modelPicker ids = %v, want the declared order %v", ids, want)
			}
		})
	}
}

// A ROUTED PROVIDER'S LIST IS CLAUDE'S WHOLE MENU (MM-D1). claude reaches z.ai at the gateway's
// own Anthropic endpoint, where it has no catalog: its built-in rows are only its tier names,
// meaning whatever the tier pins say. So the picker's built-ins are replaced by the list's own
// rows — spelled as claude sends them, with the [1m] the provider's 1M context window earns — and
// every tier, fable included, is pinned to a list id with the entry's name (MM-D2).
//
// What stays as today, and why: no availableModels on a list that no `only` narrowed, since z.ai
// serves more than yolo lists and whether a list refuses there is OQ-MM3's; and the start pin,
// ANTHROPIC_MODEL, since it is the only check on claude's start where the allowlist does not
// render, until OQ-MM3 says what replaces it (§14.4 step 1).
func TestClaudeOnARoutedProviderShowsTheListAndPinsEveryTier(t *testing.T) {
	packs := testPacksForAgent(t, "claude", "zai")
	user := jsonx.NewOrderedMap()
	zai := jsonx.NewOrderedMap()
	models := jsonx.NewOrderedMap()
	named := jsonx.NewOrderedMap()
	named.Set("id", "glm-5.3")
	named.Set("name", "GLM-5.3")
	models.Set("glm-5.3", named)
	zai.Set("models", models)
	user.Set("zai", zai)
	got := renderClaudeModels(t, packs, user, nil, "zai")

	picker, _ := got.settings["modelPicker"].(map[string]any)
	if picker["replaceBuiltInOptions"] != true {
		t.Errorf("modelPicker = %v, want the built-ins replaced", got.settings["modelPicker"])
	}
	options, _ := picker["options"].([]any)
	var ids, labels []any
	for _, o := range options {
		ids = append(ids, o.(map[string]any)["model"])
		labels = append(labels, o.(map[string]any)["label"])
	}
	if want := []any{"glm-4.6[1m]", "glm-5.3[1m]", "glm-5.3-flash[1m]"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("modelPicker ids = %v, want %v", ids, want)
	}
	if want := []any{"glm-4.6", "GLM-5.3", "glm-5.3-flash"}; !reflect.DeepEqual(labels, want) {
		t.Errorf("modelPicker labels = %v, want %v", labels, want)
	}
	for _, k := range []string{"availableModels", "enforceAvailableModels"} {
		if v, present := got.settings[k]; present {
			t.Errorf("settings %s = %v, want it absent: no `only` narrowed z.ai's list (OQ-MM3)", k, v)
		}
	}

	for _, k := range claudeTierVars {
		if got.env[k] != "glm-5.3[1m]" {
			t.Errorf("%s = %q, want glm-5.3[1m], the provider's declared default", k, got.env[k])
		}
		if got.env[k+"_NAME"] != "GLM-5.3" {
			t.Errorf("%s_NAME = %q, want the entry's name", k, got.env[k+"_NAME"])
		}
	}
	// Every tier pin is a row of the picker, so Default and every background request resolve
	// to a model the menu offers.
	for _, k := range claudeTierVars {
		found := false
		for _, id := range ids {
			found = found || id == got.env[k]
		}
		if !found {
			t.Errorf("%s = %q is not a modelPicker row %v", k, got.env[k], ids)
		}
	}
	if got.env["ANTHROPIC_MODEL"] != "glm-5.3[1m]" {
		t.Errorf("ANTHROPIC_MODEL = %q, want today's start pin kept until OQ-MM3 is ruled", got.env["ANTHROPIC_MODEL"])
	}
}

// A BRIDGED PROVIDER IS ROUTED TOO. Cerebras serves no Anthropic endpoint of its own; core
// composes the wire bridge's address as its `anthropic` endpoint (packload.adaptEndpoints), and
// claude reaches it there, where it has no catalog either. Its one declared model is the menu,
// named by its id rather than by the `default` alias it sits under.
func TestClaudeOnABridgedProviderShowsTheList(t *testing.T) {
	got := renderClaudeModels(t, testPacksForAgent(t, "claude", "cerebras"), nil, nil, "cerebras")
	picker, _ := got.settings["modelPicker"].(map[string]any)
	options, _ := picker["options"].([]any)
	want := []any{map[string]any{"model": "qwen-3.8-27b", "label": "qwen-3.8-27b"}}
	if picker["replaceBuiltInOptions"] != true || !reflect.DeepEqual(options, want) {
		t.Errorf("modelPicker = %v, want %v with the built-ins replaced", got.settings["modelPicker"], want)
	}
	for _, k := range claudeTierVars {
		if got.env[k] != "qwen-3.8-27b" {
			t.Errorf("%s = %q, want qwen-3.8-27b", k, got.env[k])
		}
	}
}

// companyModelsPack is a company pack (a pack carrying policy rather than a program,
// docs/design/model-lists-and-pickers.md §1) whose contributions shape another pack's provider.
func companyModelsPack(t *testing.T, contributes string) *packload.Pack {
	t.Helper()
	dir := t.TempDir()
	writeHostFile(t, filepath.Join(dir, "pack.json"), `{"name":"company","description":"d","contributes":[`+contributes+`]}`)
	p, problems := packload.LoadDir(dir, "company")
	if p == nil || len(problems) != 0 {
		t.Fatalf("the company fixture did not load: %v", problems)
	}
	return p
}

func pickerIDs(t *testing.T, settings map[string]any) []any {
	t.Helper()
	picker, _ := settings["modelPicker"].(map[string]any)
	if picker["replaceBuiltInOptions"] != true {
		t.Errorf("modelPicker = %v, want the built-ins replaced", settings["modelPicker"])
	}
	options, _ := picker["options"].([]any)
	var ids []any
	for _, o := range options {
		ids = append(ids, o.(map[string]any)["model"])
	}
	return ids
}

// UNDER AN `only` ON A ROUTED PROVIDER (§14.1, claude routed): the narrowed list is the menu,
// claude's allowlist refuses what is off it while the profile's switch is on, and — the allowlist
// now keeping the session valid — the start is pinned only on pin_model (MM-D1, MM-D3, MM-D5).
// With the switch off the list only shapes the menu, and the start pin stays until OQ-MM3 says
// what replaces it.
func TestClaudeUnderAnOnlyOnARoutedProvider(t *testing.T) {
	company := companyModelsPack(t, `{"kind":"models","provider":"zai","only":["glm-5.3","glm-5.3-flash"]}`)
	packs := append(testPacksForAgent(t, "claude", "zai"), company)
	off := false
	for _, tc := range []struct {
		name            string
		profiles        map[string]packload.UserProfile
		profile         string
		enforced        bool
		wantStartPinned bool
	}{
		{"the shipped profile", nil, "zai", true, false},
		{"a profile opting in with pin_model", map[string]packload.UserProfile{
			"zai-pin": {Provider: "zai", Options: map[string]string{"pin_model": "true"}}}, "zai-pin", true, true},
		{"a profile with enforcement off", map[string]packload.UserProfile{
			"zai-open": {Provider: "zai", EnforceModels: &off}}, "zai-open", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := renderClaudeModels(t, packs, nil, tc.profiles, tc.profile)
			if ids := pickerIDs(t, got.settings); !reflect.DeepEqual(ids, []any{"glm-5.3[1m]", "glm-5.3-flash[1m]"}) {
				t.Errorf("modelPicker ids = %v, want exactly the narrowed list", ids)
			}
			available, hasAllowlist := got.settings["availableModels"]
			switch {
			case tc.enforced && !reflect.DeepEqual(available, []any{"glm-5.3[1m]", "glm-5.3-flash[1m]"}):
				t.Errorf("availableModels = %v, want the narrowed list, the default (zai's glm-5.3) first", available)
			case tc.enforced && got.settings["enforceAvailableModels"] != true:
				t.Errorf("enforceAvailableModels = %v, want true while the switch is on", got.settings["enforceAvailableModels"])
			case !tc.enforced && hasAllowlist:
				t.Errorf("availableModels = %v with enforce_models off, want no refusal", available)
			}
			for _, k := range claudeTierVars {
				if got.env[k] != "glm-5.3[1m]" {
					t.Errorf("%s = %q, want glm-5.3[1m]", k, got.env[k])
				}
			}
			start, pinned := got.env["ANTHROPIC_MODEL"]
			if pinned != tc.wantStartPinned || (pinned && start != "glm-5.3[1m]") {
				t.Errorf("ANTHROPIC_MODEL = %q (set %v), want set %v", start, pinned, tc.wantStartPinned)
			}
			if _, inSettings := got.settings["env"].(map[string]any)["ANTHROPIC_DEFAULT_OPUS_MODEL"]; inSettings {
				t.Error("a routed tier pin was written into settings.json, where a bare claude would send it to Anthropic")
			}
		})
	}
}

// UNDER AN `only` ON CLAUDE'S OWN BEDROCK CLIENT (§14.1, claude native): the menu is the list's
// Anthropic entries, since Bedrock's Messages API serves Claude alone; the allowlist refuses the
// rest; and every tier is pinned in settings.json's `env` block, beside CLAUDE_CODE_USE_BEDROCK,
// where a bare `claude` keeps it (MM-D2). A company pack adds the models and narrows to them,
// which is the shape a company ships (OQ-BR12).
func TestClaudeUnderAnOnlyOnItsOwnBedrockClient(t *testing.T) {
	company := companyModelsPack(t, `{"kind":"models","provider":"bedrock","add":[
	    {"id":"global.anthropic.claude-opus-5-5","vendor":"anthropic","name":"Claude Opus 5.5"},
	    {"id":"global.moonshot.kimi-k3","vendor":"moonshot","name":"Kimi K3"},
	    {"id":"us.anthropic.claude-sonnet-5","vendor":"anthropic","alias":"sonnet","name":"Claude Sonnet 5"}]},
	  {"kind":"models","provider":"bedrock","only":["global.anthropic.claude-opus-5-5","global.moonshot.kimi-k3","us.anthropic.claude-sonnet-5"]}`)
	packs := append(testPacksForAgent(t, "claude"), company)
	got := renderClaudeModels(t, packs, nil, nil, "bedrock")

	want := []any{"global.anthropic.claude-opus-5-5", "us.anthropic.claude-sonnet-5"}
	if ids := pickerIDs(t, got.settings); !reflect.DeepEqual(ids, want) {
		t.Errorf("modelPicker ids = %v, want the list's Anthropic entries %v", ids, want)
	}
	if available := got.settings["availableModels"]; !reflect.DeepEqual(available, want) ||
		got.settings["enforceAvailableModels"] != true {
		t.Errorf("availableModels = %v (enforce %v), want %v enforced", available, got.settings["enforceAvailableModels"], want)
	}
	settingsEnv, _ := got.settings["env"].(map[string]any)
	if settingsEnv["CLAUDE_CODE_USE_BEDROCK"] != "1" {
		t.Fatalf("settings env = %v, want claude's own Bedrock client switched on", settingsEnv)
	}
	wantTiers := map[string]string{
		"ANTHROPIC_DEFAULT_OPUS_MODEL":   "global.anthropic.claude-opus-5-5",
		"ANTHROPIC_DEFAULT_SONNET_MODEL": "us.anthropic.claude-sonnet-5",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":  "global.anthropic.claude-opus-5-5",
		"ANTHROPIC_DEFAULT_FABLE_MODEL":  "global.anthropic.claude-opus-5-5",
	}
	for k, v := range wantTiers {
		if settingsEnv[k] != v {
			t.Errorf("settings env %s = %v, want %s", k, settingsEnv[k], v)
		}
		if got.env[k] != v {
			t.Errorf("process env %s = %q, want %s, the value the settings file carries", k, got.env[k], v)
		}
	}
	if settingsEnv["ANTHROPIC_DEFAULT_SONNET_MODEL_NAME"] != "Claude Sonnet 5" {
		t.Errorf("settings env sonnet name = %v, want the entry's name", settingsEnv["ANTHROPIC_DEFAULT_SONNET_MODEL_NAME"])
	}
	if v, set := got.env["ANTHROPIC_MODEL"]; set {
		t.Errorf("ANTHROPIC_MODEL = %q, want no start pin: the allowlist keeps the session valid (MM-D3)", v)
	}
}

// A LIST THAT ONLY ADDS KEEPS TODAY'S RENDERING on claude's own Bedrock client: what an `add`
// does to the agent's own catalog is OQ-MM1's, unruled, so no picker, no allowlist and no pins
// come of it.
func TestClaudeKeepsItsOwnMenuForAListThatOnlyAdds(t *testing.T) {
	company := companyModelsPack(t, `{"kind":"models","provider":"bedrock","add":[
	    {"id":"global.anthropic.claude-opus-5-5","vendor":"anthropic"}]}`)
	got := renderClaudeModels(t, append(testPacksForAgent(t, "claude"), company), nil, nil, "bedrock")
	for _, k := range []string{"modelPicker", "availableModels", "enforceAvailableModels"} {
		if v, present := got.settings[k]; present {
			t.Errorf("settings %s = %v, want it absent for a list no `only` narrowed (OQ-MM1)", k, v)
		}
	}
}
