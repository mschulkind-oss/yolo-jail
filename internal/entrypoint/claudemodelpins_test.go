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
