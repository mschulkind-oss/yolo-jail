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
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// zaiReachableJSON is packs/zai's provider fact, spelled the way the composed table
// carries it. Both agents under test reach it through the openai endpoint — pi
// translating its openai-chat-completions wire_api into its own openai-completions,
// opencode consuming no wire_api at all — and it carries the `default` alias whose id is
// the model half of both selections.
const zaiReachableJSON = `{"zai":{
  "api_key_env_name":"ZAI_API_KEY",
  "models":{"default":"glm-4.7","fast":"glm-4.7-air"},
  "endpoints":{
    "anthropic":{"base_url":"https://api.z.ai/api/anthropic"},
    "openai":{"base_url":"https://api.z.ai/api/paas/v4","wire_api":"openai-chat-completions"}
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
},"llamacpp":{"base_url":"http://127.0.0.1:8080/v1"}}`

// noDefaultJSON is a reachable provider that declares models but no `default` alias, and
// one that declares a single model only. They are the two shapes the model half of a
// selection has to resolve without guessing: an unknown alias would be a selection the
// agent refuses at resolution time (pi matches model ids exactly against the provider's
// list; opencode raises ModelNotFoundError), so the honest degradation is to write less.
const noDefaultJSON = `{"solo":{
  "base_url":"http://127.0.0.1:8080/v1","models":{"fast":"qwen"}},
 "split":{"base_url":"http://127.0.0.1:8081/v1","models":{"fast":"qwen","big":"qwen-max"}}}`

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
	// zai rides along because it is the pack that DECLARES the `zai` variant — the shipped
	// shape where the profile lives on a pack that installs no CLI, so the resolution is
	// cross-pack by construction. Without it, `{"pi": "zai"}` would resolve to the bare
	// name and this harness would be testing a different rule than the one that ships.
	zai, err := embeddedPack("zai")
	if err != nil {
		t.Fatalf("embedded zai: %v", err)
	}
	r.e.Vars["YOLO_USE_PROFILES"] = profilesJSON
	ConfigurePackSurfaces(r.e, []*packload.Pack{pi, ocode, zai})
	if fails := r.e.GenFailures(); len(fails) != 0 {
		t.Fatalf("boot render failed: %v\n%s", fails, r.errw.String())
	}
}

// wireProfiles installs the RESOLVED profile table a real launch composes on the host and
// lowers into the jail as YOLO_PROFILES — the table activeProfileOptions reads to fill
// ctx.profile, the input the model half of a selection derives from. It stays in force
// across renders on purpose: a launch delivers the table every boot, so a multi-boot
// sequence that keeps the selection selected sees the same options each time. A test that
// never sets it exercises the no-option world, which is also the world of a launch that
// composed no profiles.
func (r *pioencodeRender) wireProfiles(json string) { r.e.Vars["YOLO_PROFILES"] = json }

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
			"keys, provider-catalog-and-selection.md §3 pi row)", settings["defaultProvider"], provider)
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
			profiles:     `{"pi":"zai"}`,
			wantProvider: "zai",
			wantModel:    "glm-4.7",
		},
		{
			// OQ-CS4's arrival at pi's own key: the profile's `model` option names an alias
			// of the SAME provider table, and the id under it is what defaultModel gets —
			// not the declared default. The option crosses the resolved table, so the wire
			// table here is the shape a real launch lowers in, not a second spelling.
			name:         "the profile's model option names the alias",
			providers:    zaiReachableJSON,
			profiles:     `{"pi":"zai"}`,
			wire:         `{"zai": {"provider": "zai", "model": "fast"}}`,
			wantProvider: "zai",
			wantModel:    "glm-4.7-air",
		},
		{
			// An option naming an alias the provider does not declare is not a licence to
			// guess: the id under a wrong alias is not on the list pi matches against
			// exactly, and neither is any other id. defaultProvider stands, defaultModel
			// stays absent — the same degradation as a provider with no default alias.
			name:         "an option naming an unknown alias writes defaultProvider alone",
			providers:    zaiReachableJSON,
			profiles:     `{"pi":"zai"}`,
			wire:         `{"zai": {"provider": "zai", "model": "turbo"}}`,
			wantProvider: "zai",
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
			guard:     "zai",
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
			guard:     "zai",
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
			providers:    `{"bare":{"base_url":"http://127.0.0.1:8080/v1"}}`,
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
			if settings["theme"] != "system" {
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
	}{
		{
			name:      "an opencode-reachable provider is selected with its default alias",
			providers: zaiReachableJSON,
			profiles:  `{"opencode":"zai"}`,
			wantModel: "zai/glm-4.7",
		},
		{
			// OQ-CS4 at opencode's key: the option names the alias, the id under it joins
			// the provider with the one slash opencode splits on, and the catalog row the
			// prefix names is the same one this derive wrote.
			name:      "the profile's model option names the alias",
			providers: zaiReachableJSON,
			profiles:  `{"opencode":"zai"}`,
			wire:      `{"zai": {"provider": "zai", "model": "fast"}}`,
			wantModel: "zai/glm-4.7-air",
		},
		{
			// An option naming an alias the provider does not declare asks a question the
			// table cannot answer, and the one-model fallback is deliberately NOT the
			// answer — that fallback belongs to the default ask, where "which model" has
			// only one possible reply. Here it would be a silent override of an explicit
			// one, so the key stays absent and opencode's own choice stands.
			name:      "an option naming an unknown alias writes nothing",
			providers: noDefaultJSON,
			profiles:  `{"opencode":"solo"}`,
			wire:      `{"solo": {"provider": "solo", "model": "turbo"}}`,
			guard:     "solo",
		},
		{
			// OQ-CS2 again, with a guard: with `model` unset opencode falls back to its own
			// persisted interactive choice (~/.local/state/opencode/model.json), which is
			// exactly the choice a default written here would revert on the next launch.
			name:      "no active profile writes nothing selection-shaped",
			providers: zaiReachableJSON,
			profiles:  ``,
			guard:     "zai",
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
			guard:     "zai",
		},
		{
			// One model declared and no alias for it: "which model" has a single possible
			// answer, so the derive claims it rather than writing a provider half.
			name:      "a provider with a single model selects that model",
			providers: noDefaultJSON,
			profiles:  `{"opencode":"solo"}`,
			wantModel: "solo/qwen",
		},
		{
			// Two models and no default: any pick would be a guess, `model` is one key so
			// there is no partial write, and the honest degradation is no selection at all —
			// opencode's own choice stands.
			name:      "a provider with two models and no default writes nothing",
			providers: noDefaultJSON,
			profiles:  `{"opencode":"split"}`,
			guard:     "split",
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

// TestPiAndOpencodeSelectionDeactivatesAcrossRenders is the end-to-end OQ-CS2 pair for
// both agents. The fresh half is the "no active profile" case in each table above; this is
// the OTHER half, on homes a selecting launch already wrote: a launch with no profile keeps
// the keys the selection left. yolo can turn a selection on and cannot turn it off (§5.1) —
// which is why the harness here is the multi-boot one and not the per-case fresh render,
// which can only ever test the first boot.
func TestPiAndOpencodeSelectionDeactivatesAcrossRenders(t *testing.T) {
	r := newPioencodeRender(t, zaiReachableJSON)

	r.render(t, `{"pi":"zai","opencode":"zai"}`)
	requirePiSelection(t, r.piSettings(t), r.piModels(t), "zai", "glm-4.7")
	requireOpencodeSelection(t, r.ocConfig(t), "zai/glm-4.7")

	r.render(t, ``)
	requirePiSelection(t, r.piSettings(t), r.piModels(t), "zai", "glm-4.7")
	requireOpencodeSelection(t, r.ocConfig(t), "zai/glm-4.7")
}

// TestPiSelectionSurvivesAUserEdit is the hazard OQ-CS2 exists for, on pi's surface: pi
// lets a user change the default model interactively mid-session, the next boot of the SAME
// selection must not revert them, and the mechanism that buys that is the reserved
// namespace plus the edge-triggered apply — which is why the pin is a boot sequence and not
// a derive call.
func TestPiSelectionSurvivesAUserEdit(t *testing.T) {
	r := newPioencodeRender(t, zaiReachableJSON)
	piSettings := []string{".pi", "agent", "settings.json"}

	r.render(t, `{"pi":"zai"}`)
	requirePiSelection(t, r.piSettings(t), r.piModels(t), "zai", "glm-4.7")

	r.edit(t, piSettings, "defaultModel", "glm-4.7-air")

	requirePiSelection(t, r.piSettings(t), r.piModels(t), "zai", "glm-4.7-air")
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
