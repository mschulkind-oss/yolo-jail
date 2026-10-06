package entrypoint

// pi_subagents_scope_test.go pins the maintainer's ruling of 2026-09-28 on pi-subagents'
// `subagents` block (docs/research/extension-model-defaults.md OQ-XM3, which also answers
// docs/research/pi-model-selection-ux.md OQ-PM1): pi's settings derive writes it for EVERY
// provider a pi profile selects, not only openai-codex.
//
//   - subagents.defaultModel is the active profile's default model, as `provider/id`, so a
//     child agent that names no model starts there;
//   - subagents.modelScope is {enforce, strict, allow} over the selected provider's
//     configured models, so a child may name another model of that provider and never one
//     of another provider.
//
// Every case renders through ConfigurePackSurfaces, the boot's own loop, over the SHIPPED
// pi pack and providers composed from the shipped packs, so deleting the block from the
// derive, or the yolo.model_for call its default goes through, fails a test here. The
// ctx root is an empty directory (newPioencodeRender), so this jail's /ctx/host-pi
// settings cannot leak in.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// shippedScopeRender is a render harness over the providers the shipped pi, zai, openrouter,
// kilo and llamacpp packs compose (pi's closure adds openai-auth, which declares openai-codex),
// with the profiles those packs ship resolved the way a launch resolves them. kilo's profile
// states a model, because kilo declares no model list and names none of its own.
func shippedScopeRender(t *testing.T) *pioencodeRender {
	t.Helper()
	packs := testPacksForAgent(t, "pi", "zai", "openrouter", "kilo", "llamacpp")
	providers, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles(packs, map[string]packload.UserProfile{
		"kilo": {Provider: "kilo", Options: map[string]string{"model": "deepseek-v4.1-flash"}},
	}, providers)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"codex", "zai", "openrouter", "kilo", "llamacpp"} {
		if _, ok := resolved[name]; !ok {
			t.Fatalf("the shipped packs resolve no %q profile, so its case measures nothing", name)
		}
	}
	r := newPioencodeRender(t, mustCompactJSON(t, providers))
	r.wireProfiles(mustCompactJSON(t, packload.ProfilesWireTable(resolved)))
	return r
}

// wantPiSubagents is the block each shipped profile renders. The codex one is unchanged by
// the ruling (the exact declared ids, ML-D5). zai and openrouter are pi's own providers, so pi
// uses its own list and their scope is the whole provider, the default being the profile's
// `model` as pi's own id (docs/design/pi-codex-provider-shadowing.md OQ-3); kilo declares no
// model list, so its scope is the whole provider and nothing else.
func wantPiSubagents(profile string) map[string]any {
	scope := func(allow ...any) map[string]any {
		return map[string]any{"enforce": true, "strict": true, "allow": allow}
	}
	switch profile {
	case "codex":
		return map[string]any{
			"defaultProvider": "openai-codex",
			"defaultModel":    "openai-codex/gpt-6.1-sol",
			"modelScope":      scope(piCodexExactAllow...),
		}
	case "zai":
		return map[string]any{
			"defaultProvider": "zai",
			"defaultModel":    "zai/glm-5.3",
			"modelScope":      scope("zai/*"),
		}
	case "openrouter":
		return map[string]any{
			"defaultProvider": "openrouter",
			"modelScope":      scope("openrouter/*"),
		}
	case "kilo":
		return map[string]any{
			"defaultProvider": "kilo",
			"defaultModel":    "kilo/deepseek/deepseek-v4.1-flash",
			"modelScope":      scope("kilo/*"),
		}
	case "llamacpp":
		return map[string]any{
			"defaultProvider": "llamacpp",
			"defaultModel":    "llamacpp/llama",
			"modelScope":      scope("llamacpp/llama"),
		}
	}
	panic("no expectation for profile " + profile)
}

func requirePiSubagents(t *testing.T, r *pioencodeRender, profile string) {
	t.Helper()
	got := r.piSettings(t)["subagents"]
	if want := wantPiSubagents(profile); !reflect.DeepEqual(got, want) {
		t.Errorf("after a -p %s render, pi subagents =\n  %#v\nwant\n  %#v", profile, got, want)
	}
}

// One provider class per case: the subscription (codex), providers pi has built in (zai, with a
// profile model, and openrouter, with none), a provider with configured models (llamacpp), and
// one with none whose profile names a model (kilo).
func TestPiSubagentsBlockFollowsEveryProvidersProfile(t *testing.T) {
	for _, profile := range []string{"codex", "zai", "openrouter", "kilo", "llamacpp"} {
		t.Run(profile, func(t *testing.T) {
			r := shippedScopeRender(t)
			r.render(t, `{"pi":"`+profile+`"}`)
			requirePiSubagents(t, r, profile)
		})
	}
}

// The child's default is the SAME model the chat selection starts on: one resolution of the
// profile's default, not two that can drift.
func TestPiSubagentsDefaultIsTheChatSelectionsModel(t *testing.T) {
	for _, profile := range []string{"codex", "zai", "kilo", "llamacpp"} {
		t.Run(profile, func(t *testing.T) {
			r := shippedScopeRender(t)
			r.render(t, `{"pi":"`+profile+`"}`)
			settings := r.piSettings(t)
			sub, _ := settings["subagents"].(map[string]any)
			want := absentOr(settings["defaultProvider"]) + "/" + absentOr(settings["defaultModel"])
			if sub["defaultModel"] != want {
				t.Errorf("subagents.defaultModel = %v, want the chat selection %q", sub["defaultModel"], want)
			}
		})
	}
}

// A profile switch between codex and a non-codex provider, both directions and on one home,
// leaves exactly the new provider's block: no codex id in another provider's scope, no
// codex default under a provider whose default yolo cannot name, and no stale block after
// a deselect.
func TestPiSubagentsBlockFollowsAProfileSwitch(t *testing.T) {
	for _, other := range []string{"zai", "openrouter"} {
		t.Run("codex to "+other+" and back", func(t *testing.T) {
			r := shippedScopeRender(t)
			for _, profile := range []string{"codex", other, "codex", other} {
				r.render(t, `{"pi":"`+profile+`"}`)
				requirePiSubagents(t, r, profile)
			}
			// A deselect: no profile, no policy. The block was yolo's computed output, so it
			// goes with the selection and pi-subagents' own default applies again.
			r.render(t, `{}`)
			if sub, present := r.piSettings(t)["subagents"]; present {
				t.Errorf("after an unprofiled render pi subagents = %#v, want it gone", sub)
			}
			r.render(t, `{"pi":"codex"}`)
			requirePiSubagents(t, r, "codex")
		})
	}
}

// For a provider whose default yolo cannot name, the block DELETES any subagents.defaultModel
// a lower layer holds, so the child inherits the parent session's model, which is on the
// selected provider. A host settings.json carrying a codex policy — this jail's maintainer
// keeps one — would otherwise hand every openrouter child a codex model, which
// pi-subagents' scope only WARNS about for an inherited model (model-scope.ts,
// checkModelScope: "inherited" is severity warn). The host's own keys under the block stay.
func TestPiSubagentsUnknownDefaultClearsAHostLayersCrossProviderModel(t *testing.T) {
	r := shippedScopeRender(t)
	writePiHostSettings(t, `{"subagents":{"defaultModel":"openai-codex/gpt-6-sol",`+
		`"modelScope":{"enforce":true,"allow":["openai-codex/gpt-6-sol"]},"disableBuiltins":true}}`)
	r.render(t, `{"pi":"openrouter"}`)
	sub, _ := r.piSettings(t)["subagents"].(map[string]any)
	if model, present := sub["defaultModel"]; present {
		t.Errorf("an openrouter launch kept the host layer's subagents.defaultModel %v — a "+
			"child with no model of its own would start on another provider", model)
	}
	want := wantPiSubagents("openrouter")["modelScope"]
	if !reflect.DeepEqual(sub["modelScope"], want) {
		t.Errorf("subagents.modelScope = %#v, want %#v over the host layer's", sub["modelScope"], want)
	}
	if sub["disableBuiltins"] != true {
		t.Errorf("the host layer's own subagents.disableBuiltins was lost: %#v", sub)
	}
}

// The default goes through yolo.model_for, whose missing-alias note is a WARNING at boot:
// a provider with a model list and no `default` alias, under a profile that names no model,
// resolves no default, says so on the launch's stderr, and still renders its scope. This
// pins the boot's Warn wiring (deriveComputedLayer) as well as pi's call.
func TestPiSubagentsMissingDefaultAliasWarnsAndStillScopes(t *testing.T) {
	r := newPioencodeRender(t, noDefaultJSON)
	r.render(t, `{"pi":"solo"}`)
	want := map[string]any{
		"defaultProvider": "solo",
		"modelScope":      map[string]any{"enforce": true, "strict": true, "allow": []any{"solo/qwen"}},
	}
	if got := r.piSettings(t)["subagents"]; !reflect.DeepEqual(got, want) {
		t.Errorf("pi subagents = %#v, want %#v", got, want)
	}
	note := luahook.MissingTierAliasNote("solo", "default")
	if !strings.Contains(r.errw.String(), note) {
		t.Errorf("the boot did not warn about solo's missing default alias; want %q in:\n%s",
			note, r.errw.String())
	}
}

// The codex branch takes the same rule when its declared list is empty (a user who nulled
// the models): the whole provider, never an empty allow, which pi-subagents refuses as a
// settings error. Before the ruling this case wrote no scope at all, so a child could name
// any provider's model.
func TestPiSubagentsCodexWithNoDeclaredModelsScopesTheWholeProvider(t *testing.T) {
	r := newPioencodeRender(t, `{"openai-codex":{"endpoints":{"openai-responses":`+
		`{"base_url":"https://chatgpt.com/backend-api/codex","wire_api":"openai-responses"}}}}`)
	r.render(t, `{"pi":"openai-codex"}`)
	want := map[string]any{
		"defaultProvider": "openai-codex",
		"modelScope": map[string]any{
			"enforce": true, "strict": true, "allow": []any{"openai-codex/*"},
		},
	}
	if got := r.piSettings(t)["subagents"]; !reflect.DeepEqual(got, want) {
		t.Errorf("pi subagents = %#v, want %#v", got, want)
	}
}
