package entrypoint

// pioencodeselection_test.go pins the pi and opencode halves of OQ-CS1: a profile active
// at each agent's CLI name writes that agent's OWN selection keys — pi's
// `defaultProvider`/`defaultModel` pair in ~/.pi/agent/settings.json (pi 0.84.4
// dist/core/settings-manager.d.ts:71-72, the pair pi's own interactive writer persists) and
// opencode's top-level `model = "<provider>/<model>"` in opencode.json (v1.18.18
// config.ts:74-76, split at model.ts:33-39) — and the cases that must write NOTHING write
// nothing there: no active profile (OQ-CS2) and a selected provider the agent cannot reach
// (the catalog's own gate, so a selection can never name a row the catalog dropped).
//
// The derives run through ConfigurePackSurfaces — the entry the BOOT loop uses — over the
// REAL embedded pi, opencode and zai packs, so the pin covers the whole chain and not a
// copy of it: surfaceSelectionFor's resolution (surfacederiveselection_test.go owns that
// pin; this builds on it, because a selection key no derive consumes is dead either way),
// the shipped derive.lua files, and the stateful JSON render. A test that ran a derive
// directly would stay green if the boot stopped handing these derives their selection,
// which is exactly the "test pins the callee while the call site is unpinned" shape
// AGENTS.md warns about.
//
// Nothing here asserts a file the agent did not read: both surfaces are JSON, so the
// render is decoded back as JSON, the way each agent reads it.

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// zaiReachableJSON is packs/zai's provider fact, spelled the way the composed table
// carries it. Both agents under test reach it through the openai endpoint — pi
// translating its openai-chat-completions wire_api into its own openai-completions,
// opencode consuming no wire_api at all — and it carries GLM-5.3, the profile default,
// as the model half of both selections.
//
// It is NAMED `zhipu`, a name neither agent has a provider of, because `zai` is one of
// pi's and opencode's own providers, which their derives write no row for
// (docs/design/pi-codex-provider-shadowing.md OQ-3; builtinproviders_test.go pins that).
// These cases pin the selection a catalogued provider gets, so the provider must be one
// the derives catalogue. The same rename holds for the set and menu fixtures beside it.
const zaiReachableJSON = `{"zhipu":{
  "api_key_env_name":"ZAI_API_KEY",
  "models":{"glm-4.6":"glm-4.6","glm-5.3":"glm-5.3","glm-5.3-flash":"glm-5.3-flash"},
  "endpoints":{
    "anthropic":{"base_url":"https://api.z.ai/api/anthropic"},
    "openai":{"base_url":"https://api.z.ai/api/coding/paas/v4","wire_api":"openai-chat-completions"}
  }
}}`

// anthropicOnlyJSON is a provider whose only URL is an anthropic endpoint — no openai
// endpoint and no shorthand — beside a neighbour both agents CAN reach. The neighbour is
// what makes the case say something about claude_only rather than about an empty table:
// with it, the same render proves the table arrived, that the neighbour is cataloged, and
// that the selected provider is not.
const anthropicOnlyJSON = `{"claude_only":{
  "api_key_env_name":"ANTHROPIC_API_KEY",
  "endpoints":{"anthropic":{"base_url":"https://api.anthropic.com"}}
},"llamacpp":{"endpoints":{"openai":{"base_url":"http://127.0.0.1:8080/v1"}}}}`

// noDefaultJSON is a reachable provider that declares models but no `default` alias, and
// one that declares a single model only. They are the two shapes the model half of a
// selection has to resolve without guessing: an unknown alias would be a selection the
// agent refuses at resolution time (pi matches model ids exactly against the provider's
// list; opencode raises ModelNotFoundError), so the honest degradation is to write less.
const noDefaultJSON = `{"solo":{
  "endpoints":{"openai":{"base_url":"http://127.0.0.1:8080/v1"}},"models":{"fast":"qwen"}},
 "split":{"endpoints":{"openai":{"base_url":"http://127.0.0.1:8081/v1"}},"models":{"fast":"qwen","big":"qwen-max"}}}`

// pioencodeRender drives the boot render of the real pi, opencode and zai packs repeatedly
// over ONE home and workspace, so each render reads the sidecars the previous one wrote.
// The selection mechanism is a state machine across boots; a harness that started fresh
// every time could only ever test its first step.
type pioencodeRender struct {
	e    *Env
	errw *bytes.Buffer
}

func newPioencodeRender(t *testing.T, providersJSON string) *pioencodeRender {
	t.Helper()
	r := &pioencodeRender{errw: &bytes.Buffer{}}
	r.e = &Env{Home: t.TempDir(), Workspace: t.TempDir(),
		Vars: map[string]string{"YOLO_PROVIDERS": providersJSON}, Stderr: r.errw}
	// Both packs read host surface bytes off the /ctx mount; neither fixture ships one,
	// so one root with a dir per pack is all the mount needs to look real.
	root := t.TempDir()
	withCtxRoot(t, root, "pi")
	withCtxRoot(t, root, "opencode")
	return r
}

// render runs one boot with the given profile table. A boot failure is fatal: every later
// step of a sequence would be measuring a render that never happened.
//
// Unless the case installed a resolved table of its own (wireProfiles), one is lowered for
// the names this boot ACTIVATES, each declaring its own name as its provider — the
// OQ-CS6-declared spelling, since a launch refuses a name nothing declares before any of
// this renders. A case that needs the table to carry more than that (an option, or a
// provider other than the name) sets its own and is not second-guessed here.
func (r *pioencodeRender) render(t *testing.T, profilesJSON string) {
	t.Helper()
	pi, err := embeddedPack("pi")
	if err != nil {
		t.Fatalf("embedded pi: %v", err)
	}
	ocode, err := embeddedPack("opencode")
	if err != nil {
		t.Fatalf("embedded opencode: %v", err)
	}
	// zai rides along because it is the pack that SHIPS the zai provider facts — the
	// shipped pairing these derives translate (its provider reaches the derives through
	// YOLO_PROVIDERS, which the fixture sets directly).
	zai, err := embeddedPack("zai")
	if err != nil {
		t.Fatalf("embedded zai: %v", err)
	}
	r.e.Vars["YOLO_USE_PROFILES"] = profilesJSON
	if _, lowered := r.e.Vars["YOLO_PROFILES"]; !lowered {
		r.wireProfiles(nameAsProviderTable(t, profilesJSON))
	}
	ConfigurePackSurfaces(r.e, []*packload.Pack{pi, ocode, zai})
	if fails := r.e.GenFailures(); len(fails) != 0 {
		t.Fatalf("boot render failed: %v\n%s", fails, r.errw.String())
	}
}

// wireProfiles installs the RESOLVED profile table a real launch composes on the host and
// lowers into the jail as YOLO_PROFILES — the table BOTH halves of the ctx read from: the
// provider through packload.ProviderFor and the options through activeProfileOptions. It
// stays in force across renders on purpose: a launch delivers the table every boot, so a
// multi-boot sequence that keeps the selection selected sees the same options each time. A
// test that never sets it gets the names-as-providers default the render lowers.
func (r *pioencodeRender) wireProfiles(json string) { r.e.Vars["YOLO_PROFILES"] = json }

// nameAsProviderTable is the resolved wire table for an active-profile table whose every
// selected name is the user's own declaration of a provider of the same name:
// name → {provider: name}. It is what a launch resolves to for such a config, and it keeps
// a case whose point is the PROVIDER table (a name the table does not hold, a protocol the
// agent cannot speak) honest about the selection half.
func nameAsProviderTable(t *testing.T, profilesJSON string) string {
	t.Helper()
	out := jsonx.NewOrderedMap()
	if profilesJSON != "" {
		decoded, err := jsonx.Decode([]byte(profilesJSON))
		if err != nil {
			t.Fatalf("decoding the profile table %s: %v", profilesJSON, err)
		}
		m, ok := decoded.(*jsonx.OrderedMap)
		if !ok {
			t.Fatalf("profile table %s is not an object", profilesJSON)
		}
		for _, agent := range m.Keys() {
			v, _ := m.Get(agent)
			name, _ := v.(string)
			if name == "" {
				continue
			}
			entry := jsonx.NewOrderedMap()
			entry.Set("provider", name)
			out.Set(name, entry)
		}
	}
	data, err := jsonx.DumpsCompact(out)
	if err != nil {
		t.Fatalf("encoding the resolved table: %v", err)
	}
	return data
}

// surface reads one rendered surface back and decodes it. A missing file is fatal rather
// than an empty map: "the key is absent" is only an assertion if the file it is absent
// FROM is the file the agent reads.
func (r *pioencodeRender) surface(t *testing.T, rel ...string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(append([]string{r.e.Home}, rel...)...))
	if err != nil {
		t.Fatalf("read the rendered surface %s: %v", filepath.Join(rel...), err)
	}
	decoded, err := (codec.JSON{}).Decode(raw)
	if err != nil {
		t.Fatalf("decode %s: %v\n---\n%s", filepath.Join(rel...), err, raw)
	}
	m, ok := decoded.(map[string]any)
	if !ok {
		t.Fatalf("%s is not a JSON object: %T", filepath.Join(rel...), decoded)
	}
	return m
}

// piSettings / piModels / ocConfig are the three files these agents read, at the paths
// their pack manifests declare.
func (r *pioencodeRender) piSettings(t *testing.T) map[string]any {
	return r.surface(t, ".pi", "agent", "settings.json")
}
func (r *pioencodeRender) piModels(t *testing.T) map[string]any {
	return r.surface(t, ".pi", "agent", "models.json")
}
func (r *pioencodeRender) ocConfig(t *testing.T) map[string]any {
	return r.surface(t, ".config", "opencode", "opencode.json")
}

// edit rewrites one surface file with one top-level string key set — the shape of an
// interactive change, which leaves the rest of the file alone. It goes through the codec
// so the hand edit is a file the agent could have written, not a corruption.
func (r *pioencodeRender) edit(t *testing.T, rel []string, key, value string) {
	t.Helper()
	m := r.surface(t, rel...)
	m[key] = value
	out, err := (codec.JSON{}).Encode(m)
	if err != nil {
		t.Fatalf("encode the hand edit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(append([]string{r.e.Home}, rel...)...), out, 0o644); err != nil {
		t.Fatalf("write the hand edit: %v", err)
	}
}

// requirePiSelection asserts pi's pair at the surface root — where its settings manager
// reads them — that the namespace itself never leaked into the file, and (when model is
// not "") that the catalog row the pair names is the one the same gate wrote. provider
// and model being "" asserts the key is ABSENT, which is the assertion half the cases
// below exist to make.
func requirePiSelection(t *testing.T, settings, models map[string]any, provider, model string) {
	t.Helper()
	if absentOr(settings["defaultProvider"]) != provider {
		t.Errorf("settings.json defaultProvider = %v, want %q (pi 0.84.4 settings-manager "+
			"keys, docs/reference/providers.md pi row)", settings["defaultProvider"], provider)
	}
	if absentOr(settings["defaultModel"]) != model {
		t.Errorf("settings.json defaultModel = %v, want %q — the model id must match the "+
			"provider's list exactly, so the pair is the pair pi itself would write",
			settings["defaultModel"], model)
	}
	if _, leaked := settings[selectionKey]; leaked {
		t.Errorf("settings.json carries a literal %q table — the reserved namespace reached "+
			"the file, which is an implementation detail of the layer, never of the file", selectionKey)
	}
	// The catalog must answer the same gate the selection does: a defaultProvider naming a
	// provider models.json dropped is the half-selection the shared gate exists to make
	// unrepresentable.
	if provider != "" {
		provs, _ := models["providers"].(map[string]any)
		if _, present := provs[provider]; !present {
			t.Errorf("models.json has no %s row for the defaultProvider yolo just wrote — "+
				"pi would hold a selection naming a provider it has no entry for: %#v",
				provider, provs)
		}
	}
}

// requireOpencodeSelection is requirePiSelection for opencode's single key.
func requireOpencodeSelection(t *testing.T, config map[string]any, model string) {
	t.Helper()
	if absentOr(config["model"]) != model {
		t.Errorf("opencode.json model = %v, want %q (\"<provider>/<model>\", split on the "+
			"first slash — v1.18.18 config.ts:74-76, model.ts:33-39)", config["model"], model)
	}
	if _, leaked := config[selectionKey]; leaked {
		t.Errorf("opencode.json carries a literal %q table — the reserved namespace reached "+
			"the file, which is an implementation detail of the layer, never of the file", selectionKey)
	}
	if model != "" {
		provs, _ := config["provider"].(map[string]any)
		id := model[:strings.IndexByte(model, '/')]
		if _, present := provs[id]; !present {
			t.Errorf("opencode.json has no provider.%s row for the model yolo just wrote — "+
				"an unknown prefix is a ModelNotFoundError, not a preference opencode "+
				"ignores: %#v", id, provs)
		}
	}
}

func TestPiDeriveWritesTheSelectionPair(t *testing.T) {
	cases := []struct {
		name         string
		providers    string
		profiles     string
		wantProvider string // "" asserts the key is ABSENT
		wantModel    string // "" asserts the key is ABSENT
		// wire is the resolved YOLO_PROFILES table this case launches with; "" carries
		// none, which is the no-option world every profile without a `model` value lives in.
		wire string
		// guard names a provider that MUST reach the catalog even though it is not
		// selected — a case whose provider table holds nothing pi can speak would make the
		// absence of a selection key vacuous, so those cases carry a speakable neighbour
		// and name it here. "" for the cases that select the one provider they carry.
		guard string
	}{
		{
			name:         "a pi-reachable provider is selected with its default alias",
			providers:    zaiReachableJSON,
			profiles:     `{"pi":"zhipu"}`,
			wire:         `{"zhipu": {"provider": "zhipu", "model": "glm-5.3"}}`,
			wantProvider: "zhipu",
			wantModel:    "glm-5.3",
		},
		{
			// OQ-CS4's arrival at pi's own key: the profile's `model` option names an alias
			// of the SAME provider table, and the id under it is what defaultModel gets —
			// not the declared default. The option crosses the resolved table, so the wire
			// table here is the shape a real launch lowers in, not a second spelling.
			name:         "the profile's model option names the alias",
			providers:    zaiReachableJSON,
			profiles:     `{"pi":"zhipu"}`,
			wire:         `{"zhipu": {"provider": "zhipu", "model": "glm-5.3-flash"}}`,
			wantProvider: "zhipu",
			wantModel:    "glm-5.3-flash",
		},
		{
			// An option naming an alias the provider does not declare is not a licence to
			// guess: the id under a wrong alias is not on the list pi matches against
			// exactly, and neither is any other id. defaultProvider stands, defaultModel
			// stays absent — the same degradation as a provider with no default alias.
			name:         "an option naming an unknown alias writes defaultProvider alone",
			providers:    zaiReachableJSON,
			profiles:     `{"pi":"zhipu"}`,
			wire:         `{"zhipu": {"provider": "zhipu", "model": "turbo"}}`,
			wantProvider: "zhipu",
		},
		{
			// OQ-CS2: the no-profile case is the agent's own — pi's own persisted
			// interactive choice stands, and yolo writing a default here would revert it on
			// the next launch. The catalog half is NOT gated on the selection (OQ-CS1
			// option D), so this case asserts absence AND presence, which is what keeps the
			// absence from being vacuous.
			name:      "no active profile writes nothing selection-shaped",
			providers: zaiReachableJSON,
			profiles:  ``,
			guard:     "zhipu",
		},
		{
			// The gate is the catalog's, not "any endpoint pi's registry can name": pi
			// speaks anthropic-messages, but an endpoints-only provider with no openai
			// endpoint names no URL for the protocol pi resolves to (zai-plumbing.md §5,
			// pinned by providerderive_test.go), so it is no catalog row — and a
			// defaultProvider naming a dropped row is the half-selection the shared gate
			// makes unrepresentable.
			name:      "an anthropic-endpoint-only provider is never selected",
			providers: anthropicOnlyJSON,
			profiles:  `{"pi":"claude_only"}`,
			guard:     "llamacpp",
		},
		{
			// A profile whose provider the composed table does not hold delivers
			// nothing to any agent, so it selects nothing either.
			name:      "a selected name the table does not hold selects nothing",
			providers: zaiReachableJSON,
			profiles:  `{"pi":"mystery"}`,
			guard:     "zhipu",
		},
		{
			// The model fallback is the derive's business (OQ-CS3) and today it is the
			// provider's declared `default` alias. No declared default means no
			// defaultModel — pi resolves its own model within the named provider, and a
			// guessed id would be one the provider's list does not hold, which pi matches
			// against exactly. defaultProvider alone is therefore not a half-selection; the
			// killing kind is a defaultProvider with no catalog row, which the shared gate
			// prevents.
			name:         "a provider with models but no default alias writes defaultProvider alone",
			providers:    noDefaultJSON,
			profiles:     `{"pi":"solo"}`,
			wantProvider: "solo",
		},
		{
			name:         "a provider with no models at all writes defaultProvider alone",
			providers:    `{"bare":{"endpoints":{"openai":{"base_url":"http://127.0.0.1:8080/v1"}}}}`,
			profiles:     `{"pi":"bare"}`,
			wantProvider: "bare",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newPioencodeRender(t, tc.providers)
			if tc.wire != "" {
				r.wireProfiles(tc.wire)
			}
			r.render(t, tc.profiles)
			settings, models := r.piSettings(t), r.piModels(t)

			requirePiSelection(t, settings, models, tc.wantProvider, tc.wantModel)

			// A case whose provider table holds a speakable provider that is NOT selected
			// proves the table reached the derive at all — and, with it, that the catalog
			// is not gated on the selection (OQ-CS1 option D).
			if tc.guard != "" {
				provs, ok := models["providers"].(map[string]any)
				if !ok {
					t.Fatalf("models.json has no providers table — the composed table did "+
						"not reach the derive: %#v", models)
				}
				if _, present := provs[tc.guard]; !present {
					t.Errorf("models.json has no %s entry, and a provider nobody selected "+
						"still belongs in the catalog (OQ-CS1 option D): %#v", tc.guard, provs)
				}
			}
			// The settings surface is a real stateful render with layers of its own, not a
			// file the selection mechanism created: its declared defaults must still be
			// there, which is what keeps "the key is absent" from meaning "the file never
			// rendered".
			if settings["theme"] != "light/dark" {
				t.Errorf("settings.json theme = %v, want the surface's declared default — the "+
					"selection must ride a real render, not replace it", settings["theme"])
			}
		})
	}
}

func TestOpencodeDeriveWritesTheSelectionKey(t *testing.T) {
	cases := []struct {
		name      string
		providers string
		profiles  string
		wantModel string // "" asserts the key is ABSENT
		// wire is the resolved YOLO_PROFILES table this case launches with; "" carries
		// none, which is the no-option world every profile without a `model` value lives in.
		wire  string
		guard string // a provider that must still be cataloged; "" selects the only one
		// wantProviders is enabled_providers, nil asserting it is ABSENT: the menu follows the
		// selected provider whether or not a model resolves (OQ-CN4; active-provider-sets.md
		// AP-D17), and nothing is written where no provider is selected.
		wantProviders []string
	}{
		{
			name:          "an opencode-reachable provider is selected with its default alias",
			providers:     zaiReachableJSON,
			profiles:      `{"opencode":"zhipu"}`,
			wire:          `{"zhipu": {"provider": "zhipu", "model": "glm-5.3"}}`,
			wantModel:     "zhipu/glm-5.3",
			wantProviders: []string{"zhipu"},
		},
		{
			// OQ-CS4 at opencode's key: the option names the alias, the id under it joins
			// the provider with the one slash opencode splits on, and the catalog row the
			// prefix names is the same one this derive wrote.
			name:          "the profile's model option names the alias",
			providers:     zaiReachableJSON,
			profiles:      `{"opencode":"zhipu"}`,
			wire:          `{"zhipu": {"provider": "zhipu", "model": "glm-5.3-flash"}}`,
			wantModel:     "zhipu/glm-5.3-flash",
			wantProviders: []string{"zhipu"},
		},
		{
			// An option naming an alias the provider does not declare asks a question the
			// table cannot answer, and the one-model fallback is deliberately NOT the
			// answer — that fallback belongs to the default ask, where "which model" has
			// only one possible reply. Here it would be a silent override of an explicit
			// one, so the key stays absent and opencode chooses, within the provider selected.
			name:          "an option naming an unknown alias writes no model",
			providers:     noDefaultJSON,
			profiles:      `{"opencode":"solo"}`,
			wire:          `{"solo": {"provider": "solo", "model": "turbo"}}`,
			guard:         "solo",
			wantProviders: []string{"solo"},
		},
		{
			// OQ-CS2 again, with a guard: with `model` unset opencode falls back to its own
			// persisted interactive choice (~/.local/state/opencode/model.json), which is
			// exactly the choice a default written here would revert on the next launch.
			name:      "no active profile writes nothing selection-shaped",
			providers: zaiReachableJSON,
			profiles:  ``,
			guard:     "zhipu",
		},
		{
			// opencode's gate is the catalog's too, and here the stakes are higher than a
			// dangling preference: an unknown prefix in `model` is a ModelNotFoundError
			// with no silent fallback, so a selection whose provider the catalog dropped
			// would be a config that fails at first request.
			name:      "an anthropic-endpoint-only provider is never selected",
			providers: anthropicOnlyJSON,
			profiles:  `{"opencode":"claude_only"}`,
			guard:     "llamacpp",
		},
		{
			name:      "a selected name the table does not hold selects nothing",
			providers: zaiReachableJSON,
			profiles:  `{"opencode":"mystery"}`,
			guard:     "zhipu",
		},
		{
			// One model declared and no alias for it: "which model" has a single possible
			// answer, so the derive claims it rather than writing a provider half.
			name:          "a provider with a single model selects that model",
			providers:     noDefaultJSON,
			profiles:      `{"opencode":"solo"}`,
			wantModel:     "solo/qwen",
			wantProviders: []string{"solo"},
		},
		{
			// Two models and no default: any pick would be a guess, `model` is one key so
			// there is no partial write, and the honest degradation is no `model` at all —
			// opencode chooses, held to the provider selected.
			name:          "a provider with two models and no default writes no model",
			providers:     noDefaultJSON,
			profiles:      `{"opencode":"split"}`,
			guard:         "split",
			wantProviders: []string{"split"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newPioencodeRender(t, tc.providers)
			if tc.wire != "" {
				r.wireProfiles(tc.wire)
			}
			r.render(t, tc.profiles)
			config := r.ocConfig(t)

			requireOpencodeSelection(t, config, tc.wantModel)
			got, present := config["enabled_providers"]
			switch {
			case tc.wantProviders == nil && present:
				t.Errorf("opencode.json enabled_providers = %#v, want none: no provider is selected", got)
			case tc.wantProviders != nil && !reflect.DeepEqual(strs(got), tc.wantProviders):
				t.Errorf("opencode.json enabled_providers = %#v, want %#v", got, tc.wantProviders)
			}

			if tc.guard != "" {
				provs, ok := config["provider"].(map[string]any)
				if !ok {
					t.Fatalf("opencode.json has no provider table — the composed table did "+
						"not reach the derive: %#v", config)
				}
				if _, present := provs[tc.guard]; !present {
					t.Errorf("opencode.json has no provider.%s entry, and a provider nobody "+
						"selected still belongs in the catalog (OQ-CS1 option D): %#v",
						tc.guard, provs)
				}
			}
		})
	}
}

// TestPiAndOpencodeSelectionDeactivatesAcrossRenders pins OQ-PSW2: on deactivation, yolo
// clears the keys it wrote (falling back to native defaults or host layer), and drops
// the selection record.
func TestPiAndOpencodeSelectionDeactivatesAcrossRenders(t *testing.T) {
	r := newPioencodeRender(t, zaiReachableJSON)

	r.wireProfiles(`{"zhipu": {"provider": "zhipu", "model": "glm-5.3"}}`)
	r.render(t, `{"pi":"zhipu","opencode":"zhipu"}`)
	requirePiSelection(t, r.piSettings(t), r.piModels(t), "zhipu", "glm-5.3")
	requireOpencodeSelection(t, r.ocConfig(t), "zhipu/glm-5.3")

	r.render(t, ``)
	if got := r.piSettings(t)["defaultProvider"]; got != nil {
		t.Errorf("after deactivation pi defaultProvider = %v, want nil (cleared)", got)
	}
	if got := r.piSettings(t)["defaultModel"]; got != nil {
		t.Errorf("after deactivation pi defaultModel = %v, want nil (cleared)", got)
	}
	if got := r.ocConfig(t)["model"]; got != nil {
		t.Errorf("after deactivation opencode model = %v, want nil (cleared)", got)
	}
	for _, surface := range [][2]string{{"pi", "settings"}, {"opencode", "config"}} {
		path := prismSelectionRecordPath(r.e, surface[0], surface[1])
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("after clearing, %s.%s selection record still exists at %s", surface[0], surface[1], path)
		}
	}
}

// TestPiSelectionSurvivesAUserEdit is the hazard OQ-CS2 exists for, on pi's surface: pi
// lets a user change the default model interactively mid-session, the next boot of the SAME
// selection must not revert them, and the mechanism that buys that is the reserved
// namespace plus the edge-triggered apply — which is why the pin is a boot sequence and not
// a derive call.
func TestPiSelectionSurvivesAUserEdit(t *testing.T) {
	r := newPioencodeRender(t, zaiReachableJSON)
	piSettings := []string{".pi", "agent", "settings.json"}

	r.wireProfiles(`{"zhipu": {"provider": "zhipu", "model": "glm-5.3"}}`)
	r.render(t, `{"pi":"zhipu"}`)
	requirePiSelection(t, r.piSettings(t), r.piModels(t), "zhipu", "glm-5.3")

	r.edit(t, piSettings, "defaultModel", "glm-5.3-flash")

	requirePiSelection(t, r.piSettings(t), r.piModels(t), "zhipu", "glm-5.3-flash")
}

// TestPiAndOpencodeWriteNoRecordWhenNothingIsSelected pins the quiet half for these two
// surfaces: a derive that emits no selection leaves no selection record, so the sidecar
// tree grows only where the mechanism is used — and the fresh no-profile launch above is
// only provable beside the record it must also not create.
func TestPiAndOpencodeWriteNoRecordWhenNothingIsSelected(t *testing.T) {
	r := newPioencodeRender(t, zaiReachableJSON)
	r.render(t, ``)

	for _, surface := range [][2]string{{"pi", "settings"}, {"opencode", "config"}} {
		path := prismSelectionRecordPath(r.e, surface[0], surface[1])
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("a surface with no selection wrote a selection record at %s: %v", path, err)
		}
	}
	requirePiSelection(t, r.piSettings(t), r.piModels(t), "", "")
	requireOpencodeSelection(t, r.ocConfig(t), "")
}

// editValue is edit for a value of any JSON shape — an interactive change to pi's scoped
// models writes an ARRAY, which edit's string-only signature cannot express.
func (r *pioencodeRender) editValue(t *testing.T, rel []string, key string, value any) {
	t.Helper()
	m := r.surface(t, rel...)
	m[key] = value
	out, err := (codec.JSON{}).Encode(m)
	if err != nil {
		t.Fatalf("encode the hand edit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(append([]string{r.e.Home}, rel...)...), out, 0o644); err != nil {
		t.Fatalf("write the hand edit: %v", err)
	}
}

// piEnabledModels is pi's enabledModels as the rendered file holds it, as strings, or nil
// when the key is absent.
func piEnabledModels(t *testing.T, settings map[string]any) []string {
	t.Helper()
	raw, ok := settings["enabledModels"]
	if !ok {
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		t.Fatalf("enabledModels is %T, want an array", raw)
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		s, _ := v.(string)
		out = append(out, s)
	}
	return out
}

// TestPiEnabledModelsUserEditSurvivesARerender is the array half of the hazard OQ-CS2 exists
// for: pi's /model scoping writes enabledModels, and a same-selection launch must not revert
// the user's list to the derive's.
func TestPiEnabledModelsUserEditSurvivesARerender(t *testing.T) {
	r := newPioencodeRender(t, zaiReachableJSON)
	piSettings := []string{".pi", "agent", "settings.json"}
	r.wireProfiles(`{"zhipu": {"provider": "zhipu", "model": "glm-5.3"}}`)

	r.render(t, `{"pi":"zhipu"}`)
	if got := piEnabledModels(t, r.piSettings(t)); strings.Join(got, ",") != "zhipu/glm-5.3,zhipu/glm-4.6,zhipu/glm-5.3-flash" {
		t.Fatalf("activation enabledModels = %v, want the default-led list", got)
	}

	mine := []any{"zhipu/glm-5.3-flash"}
	r.editValue(t, piSettings, "enabledModels", mine)
	r.render(t, `{"pi":"zhipu"}`)
	if got := piEnabledModels(t, r.piSettings(t)); strings.Join(got, ",") != "zhipu/glm-5.3-flash" {
		t.Errorf("after a same-selection re-render enabledModels = %v, want the user's [zhipu/glm-5.3-flash] kept", got)
	}
}

// TestPiEnabledModelsFollowTheSelectionRules walks enabledModels through the rest of the
// selection state machine over real boots: a changed selection moves yolo's own list,
// deactivation clears it (OQ-PSW2), and deactivation keeps a list the user wrote.
func TestPiEnabledModelsFollowTheSelectionRules(t *testing.T) {
	piSettings := []string{".pi", "agent", "settings.json"}
	const zaiList = "zhipu/glm-5.3,zhipu/glm-4.6,zhipu/glm-5.3-flash"

	t.Run("a changed selection moves yolo's list", func(t *testing.T) {
		r := newPioencodeRender(t, zaiReachableJSON)
		r.wireProfiles(`{"zhipu": {"provider": "zhipu", "model": "glm-5.3"}}`)
		r.render(t, `{"pi":"zhipu"}`)
		r.wireProfiles(`{"zhipu": {"provider": "zhipu", "model": "glm-5.3-flash"}}`)
		r.render(t, `{"pi":"zhipu"}`)
		if got := strings.Join(piEnabledModels(t, r.piSettings(t)), ","); got != "zhipu/glm-5.3-flash,zhipu/glm-4.6,zhipu/glm-5.3" {
			t.Errorf("enabledModels = %s, want the new default leading", got)
		}
	})
	t.Run("deactivation clears yolo's list", func(t *testing.T) {
		r := newPioencodeRender(t, zaiReachableJSON)
		r.wireProfiles(`{"zhipu": {"provider": "zhipu", "model": "glm-5.3"}}`)
		r.render(t, `{"pi":"zhipu"}`)
		if got := strings.Join(piEnabledModels(t, r.piSettings(t)), ","); got != zaiList {
			t.Fatalf("activation enabledModels = %s, want %s", got, zaiList)
		}
		r.render(t, ``)
		if got := piEnabledModels(t, r.piSettings(t)); got != nil {
			t.Errorf("after deactivation enabledModels = %v, want it cleared", got)
		}
	})
	t.Run("deactivation keeps the user's list", func(t *testing.T) {
		r := newPioencodeRender(t, zaiReachableJSON)
		r.wireProfiles(`{"zhipu": {"provider": "zhipu", "model": "glm-5.3"}}`)
		r.render(t, `{"pi":"zhipu"}`)
		r.editValue(t, piSettings, "enabledModels", []any{"zhipu/glm-5.3-flash"})
		r.render(t, ``)
		if got := strings.Join(piEnabledModels(t, r.piSettings(t)), ","); got != "zhipu/glm-5.3-flash" {
			t.Errorf("after deactivation enabledModels = %s, want the user's list kept", got)
		}
	})

	// THE UPGRADE PATH FOR openai-codex (docs/design/model-lists-and-pickers.md ML-D2): the
	// codex arm stopped naming enabledModels while its profile stays ACTIVE. A jail an older
	// yolo booted holds the scope that yolo wrote, in the file and in the selection record;
	// the same deselect rule that runs when a profile leaves clears it, per key, and notes
	// it in the boot log — and keeps a list the user (or pi's save-as-default) edited.
	oldCodexScope := []any{
		"openai-codex/gpt-6-sol", "openai-codex/gpt-6-sol[1m]",
		"openai-codex/gpt-6-astra", "openai-codex/gpt-6-astra[1m]",
		"openai-codex/gpt-6-luna", "openai-codex/gpt-6-luna[1m]",
	}
	t.Run("an active codex profile clears the scope an older yolo wrote", func(t *testing.T) {
		r := newPiCodexRender(t)
		r.render(t, `{"pi":"codex"}`)
		r.simulatePreScopeRemovalBoot(t, oldCodexScope, true)
		if got := piEnabledModels(t, r.piSettings(t)); len(got) != len(oldCodexScope) {
			t.Fatalf("the simulated older boot's file holds enabledModels %v, want the old scope", got)
		}
		log := withBootLog(r)
		r.render(t, `{"pi":"codex"}`)
		settings := r.piSettings(t)
		if got := piEnabledModels(t, settings); got != nil {
			t.Errorf("after the upgrade boot enabledModels = %v, want yolo's old scope cleared", got)
		}
		if settings["defaultProvider"] != "openai-codex" || settings["defaultModel"] != "gpt-6.1-sol" {
			t.Errorf("the clear moved the pair: %v/%v, want openai-codex/gpt-6.1-sol, the declaration's default",
				settings["defaultProvider"], settings["defaultModel"])
		}
		// The whole line: its reason must hold with the profile still active, which the
		// deselect-only wording ("the profile that set it is no longer selected") did not.
		if want := `selection: cleared pi/settings enabledModels (was ["openai-codex/gpt-6-sol",` +
			`"openai-codex/gpt-6-sol[1m]","openai-codex/gpt-6-astra","openai-codex/gpt-6-astra[1m]",` +
			`"openai-codex/gpt-6-luna","openai-codex/gpt-6-luna[1m]"]): yolo's selection no longer sets it`; !strings.Contains(log.String(), want) {
			t.Errorf("boot log lacks %q:\n%s", want, log.String())
		}
		if strings.Contains(log.String(), "pi/settings defaultModel") {
			t.Errorf("the pair was recorded as cleared, and the profile is still active:\n%s", log.String())
		}
	})
	t.Run("an active codex profile keeps a scope the user edited", func(t *testing.T) {
		r := newPiCodexRender(t)
		r.render(t, `{"pi":"codex"}`)
		r.simulatePreScopeRemovalBoot(t, oldCodexScope, true)
		mine := []any{"openai-codex/gpt-6-luna", "openai-codex/gpt-6-sol"}
		r.editValue(t, piSettings, "enabledModels", mine)
		r.render(t, `{"pi":"codex"}`)
		if got := strings.Join(piEnabledModels(t, r.piSettings(t)), ","); got != "openai-codex/gpt-6-luna,openai-codex/gpt-6-sol" {
			t.Errorf("after the upgrade boot enabledModels = %s, want the user's edited scope kept", got)
		}
	})
	// THE UPGRADE FROM A RELEASE. v0.10.0 wrote codex's scope as a PLAIN computed key
	// (packs/pi/derive.lua at that tag returns `enabledModels` beside `selection`), so no
	// selection record names it and the deselect rule above never sees it. The upgrade boot
	// recomposes settings.json from its layers, the file matching the last render (nothing
	// was edited in the jail), and no layer asserts the key any more: it is gone. This is
	// the path every user coming from a published version takes.
	t.Run("an active codex profile drops the scope a release wrote as a computed key", func(t *testing.T) {
		r := newPiCodexRender(t)
		r.render(t, `{"pi":"codex"}`)
		r.simulatePreScopeRemovalBoot(t, oldCodexScope, false)
		if got := piEnabledModels(t, r.piSettings(t)); len(got) != len(oldCodexScope) {
			t.Fatalf("the simulated release boot's file holds enabledModels %v, want the old scope", got)
		}
		r.render(t, `{"pi":"codex"}`)
		settings := r.piSettings(t)
		if got := piEnabledModels(t, settings); got != nil {
			t.Errorf("after the upgrade boot enabledModels = %v, want the release's computed scope gone", got)
		}
		if settings["defaultProvider"] != "openai-codex" || settings["defaultModel"] != "gpt-6.1-sol" {
			t.Errorf("the upgrade moved the pair: %v/%v, want openai-codex/gpt-6.1-sol, the declaration's default",
				settings["defaultProvider"], settings["defaultModel"])
		}
	})
}

// newPiCodexRender is the multi-boot harness over the table a pi launch composes (pi's needs
// closure, which brings packs/openai-auth and its openai-codex declaration), with the codex
// profile it resolves installed.
func newPiCodexRender(t *testing.T) *pioencodeRender {
	t.Helper()
	packs := testPacksForAgent(t, "pi")
	providers, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles(packs, nil, providers)
	if err != nil {
		t.Fatal(err)
	}
	r := newPioencodeRender(t, mustCompactJSON(t, providers))
	r.wireProfiles(mustCompactJSON(t, packload.ProfilesWireTable(resolved)))
	return r
}

// simulatePreScopeRemovalBoot rewrites the state one boot left behind into what a yolo that
// still wrote codex's enabledModels would have left: the list in settings.json and the same
// bytes as the last_render sidecar (so nothing reads as an in-jail edit). recorded says
// which older yolo: true puts the list in the selection record as yolo's own write, the
// shape between the key moving under the selection and its removal; false leaves the record
// alone, the shape v0.10.0 left, which wrote the key as a plain computed one.
func (r *pioencodeRender) simulatePreScopeRemovalBoot(t *testing.T, scope []any, recorded bool) {
	t.Helper()
	settings := r.piSettings(t)
	settings["enabledModels"] = scope
	out, err := (codec.JSON{}).Encode(settings)
	if err != nil {
		t.Fatalf("encode the simulated render: %v", err)
	}
	for _, path := range []string{
		filepath.Join(r.e.Home, ".pi", "agent", "settings.json"),
		prismLastRenderPath(r.e, "pi", "settings"),
	} {
		if err := os.WriteFile(path, out, 0o644); err != nil {
			t.Fatalf("write the simulated render to %s: %v", path, err)
		}
	}
	if !recorded {
		return
	}
	recPath := prismSelectionRecordPath(r.e, "pi", "settings")
	raw, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatalf("read the selection record: %v", err)
	}
	rec := agentcfgParseRecordForTest(t, raw)
	rec["enabledModels"] = scope
	writeRecordForTest(t, recPath, rec)
}

// TestPiSelectionArrayIsNotAHostTable pins the host notch's half of letting an array ride
// the selection: hostTableKeys claims object-valued keys alone as wholesale yolo tables, so
// neither the namespace nor the enabledModels array under it may appear among pi/settings'
// host tables, where it would be written by replacement into the user's real settings.json.
func TestPiSelectionArrayIsNotAHostTable(t *testing.T) {
	pi, err := embeddedPack("pi")
	if err != nil {
		t.Fatalf("embedded pi: %v", err)
	}
	surfaces, _ := pi.SurfacesFor(false)
	visited := false
	for _, s := range surfaces {
		if s.Agent != "pi" || s.Name != "settings" {
			continue
		}
		visited = true
		for _, k := range hostTableKeys(pi, s) {
			if k == "enabledModels" || k == "selection" {
				t.Errorf("pi/settings host tables include %q; an array or the selection namespace must never be a wholesale host table", k)
			}
		}
	}
	if !visited {
		t.Fatal("pi/settings surface was never visited — did the pack drop it?")
	}
}

// TestPiEnabledModelsWrittenBeforeTheSelectionAreAdopted is the upgrade path: a jail
// rendered before enabledModels rode the selection holds yolo's list with NO record entry
// for it. A file value equal to what the selection names is indistinguishable from yolo's
// own write, so it is adopted; otherwise the list would read as the user's forever, and a
// changed selection would never move it.
func TestPiEnabledModelsWrittenBeforeTheSelectionAreAdopted(t *testing.T) {
	r := newPioencodeRender(t, zaiReachableJSON)
	r.wireProfiles(`{"zhipu": {"provider": "zhipu", "model": "glm-5.3"}}`)
	r.render(t, `{"pi":"zhipu"}`)

	// Rewrite the record as the pre-change mechanism left it: the pair only.
	path := prismSelectionRecordPath(r.e, "pi", "settings")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the selection record: %v", err)
	}
	rec := agentcfgParseRecordForTest(t, raw)
	delete(rec, "enabledModels")
	writeRecordForTest(t, path, rec)

	r.render(t, `{"pi":"zhipu"}`) // same selection: the equal list is adopted
	r.wireProfiles(`{"zhipu": {"provider": "zhipu", "model": "glm-5.3-flash"}}`)
	r.render(t, `{"pi":"zhipu"}`) // a changed selection must move the adopted list
	if got := strings.Join(piEnabledModels(t, r.piSettings(t)), ","); got != "zhipu/glm-5.3-flash,zhipu/glm-4.6,zhipu/glm-5.3" {
		t.Errorf("after a changed selection enabledModels = %s, want the new default leading — "+
			"the pre-selection list was never adopted and is being held as the user's", got)
	}
}

func agentcfgParseRecordForTest(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	decoded, err := (codec.JSON{}).Decode(raw)
	if err != nil {
		t.Fatalf("decode the selection record: %v", err)
	}
	m, ok := decoded.(map[string]any)
	if !ok {
		t.Fatalf("selection record is %T, want an object", decoded)
	}
	return m
}

func writeRecordForTest(t *testing.T, path string, rec map[string]any) {
	t.Helper()
	out, err := (codec.JSON{}).Encode(rec)
	if err != nil {
		t.Fatalf("encode the selection record: %v", err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatalf("write the selection record: %v", err)
	}
}
