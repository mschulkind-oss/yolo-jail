package entrypoint

// builtinproviders_test.go pins docs/design/pi-codex-provider-shadowing.md OQ-3, ruled 2026-10-05
// (the broad reading): yolo writes no model entry over any provider an agent has built in, and
// the agent uses its own list. A BUILT-IN PROVIDER is that design's term: one an agent ships its
// own client and model list for, under its own key. Each agent pack names its own in
// `built_in_providers` (packdecl.BuiltInProviders), core hands the answer to every derive as
// ctx.built_in_providers (surfaceSelectionFor), and pi's, oh-omp's and opencode's derives write no
// row under one.
//
// Every case renders through ConfigurePackSurfaces, the boot's own loop, over the shipped agent
// pack and the providers its shipped neighbours compose, so deleting the ctx's call site, a
// derive's check, or a name from a pack's list fails a case here. The names asserted are the
// design's table (§6.4, "What yolo writes today"), not the packs' lists read back, so a list that
// lost a shipped provider's name is caught rather than agreed with. Codex's bounded source list
// and real-boot output are covered below independently of that older cross-agent table.

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

// builtInRender boots the shipped pack named agentPack, over the providers its closure and every
// shipped provider pack compose and the profiles they ship, with use as YOLO_USE_PROFILES. It
// returns the home rendered into.
func builtInRender(t *testing.T, agentPack, use string) string {
	t.Helper()
	packs := testPacksForAgent(t, agentPack, "zai", "cerebras", "openrouter", "kilo", "llamacpp")
	providers, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles(packs, nil, providers)
	if err != nil {
		t.Fatal(err)
	}
	var errw bytes.Buffer
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Stderr: &errw, Vars: map[string]string{
		"YOLO_PROVIDERS":    mustCompactJSON(t, providers),
		"YOLO_USE_PROFILES": use,
		"YOLO_PROFILES":     mustCompactJSON(t, packload.ProfilesWireTable(resolved)),
	}}
	withCtxRoot(t, t.TempDir(), agentPack)
	ConfigurePackSurfaces(e, []*packload.Pack{packs[0]})
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("boot render failed: %v\n%s", fails, errw.String())
	}
	return e.Home
}

func codexNativeFixture(t *testing.T, userProvidersJSON string, profiles map[string]packload.UserProfile) ([]*packload.Pack, *jsonx.OrderedMap, map[string]packload.ResolvedProfile) {
	t.Helper()
	packs := testPacksForAgent(t, "codex", "zai", "wire-bridge")
	userProviders, err := jsonx.Decode([]byte(userProvidersJSON))
	if err != nil {
		t.Fatal(err)
	}
	table, err := packload.ComposeProviders(userProviders.(*jsonx.OrderedMap), packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles(packs, profiles, table)
	if err != nil {
		t.Fatal(err)
	}
	return packs, table, resolved
}

func renderCodexNativeFixture(t *testing.T, packs []*packload.Pack, providers *jsonx.OrderedMap,
	resolved map[string]packload.ResolvedProfile, use string) map[string]any {
	t.Helper()
	var codex *packload.Pack
	for _, p := range packs {
		if p.Name == "codex" {
			codex = p
			break
		}
	}
	if codex == nil {
		t.Fatal("selected packs lack codex")
	}
	var errw bytes.Buffer
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Stderr: &errw, Vars: map[string]string{
		"YOLO_PROVIDERS":    mustCompactJSON(t, providers),
		"YOLO_USE_PROFILES": use,
		"YOLO_PROFILES":     mustCompactJSON(t, packload.ProfilesWireTable(resolved)),
	}}
	withCtxRoot(t, t.TempDir(), "codex")
	ConfigurePackSurfaces(e, []*packload.Pack{codex})
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("Codex boot render failed: %v\n%s", fails, errw.String())
	}
	return renderedFile(t, e.Home, ".codex/config.toml", codec.TOML{})
}

const codexNativePoisonProviders = `{
  "openai":{"base_url":"https://poison-openai.example/v1","models":{"default":"yolo-default","fast":"expanded-id"}},
  "amazon-bedrock":{"base_url":"https://poison-bedrock.example/v1","models":{"default":"yolo-default","fast":"expanded-id"}},
  "amazon-bedrock-runtime":{"base_url":"https://poison-runtime.example/v1","models":{"default":"yolo-default","fast":"expanded-id"}},
  "ollama":{"base_url":"https://poison-ollama.example/v1","models":{"default":"yolo-default","fast":"expanded-id"}},
  "lmstudio":{"base_url":"https://poison-lmstudio.example/v1","models":{"default":"yolo-default","fast":"expanded-id"}},
  "responses":{"endpoints":{"openai-responses":{"base_url":"https://custom-responses.example/v1"}},"models":{"default":"responses-default"}},
  "acme":{"endpoints":{"openai":{"base_url":"https://custom-acme.example/v1"}},"api_key_env_name":"ACME_KEY","models":{"default":"acme-default"}},
  "llamacpp":{"base_url":"https://custom-llamacpp.example/v1","models":{"default":"llama-default"}}
}`

// CODEX'S BUILT-IN CATALOG IS OWNED BY ITS DECLARATION: the real selected Codex pack reaches
// ctx.built_in_providers through ConfigurePackSurfaces, so poison endpoints under all five
// Rust-native keys do not become model_providers rows. Native selection needs no reachable yolo
// endpoint and passes a non-default profile model literally, not through yolo's alias table.
func TestCodexNativeProvidersAreNotCataloguedAndSelectTheirOwnIDs(t *testing.T) {
	profiles := map[string]packload.UserProfile{}
	for _, name := range []string{"openai", "amazon-bedrock", "amazon-bedrock-runtime", "ollama", "lmstudio"} {
		profiles[name] = packload.UserProfile{Provider: name, Options: map[string]string{"model": "fast"}}
	}
	profiles["codex-subscription"] = packload.UserProfile{Provider: "openai-codex"}
	packs, table, resolved := codexNativeFixture(t, codexNativePoisonProviders, profiles)
	declared := packload.BuiltInProvidersFor(packs, "codex")
	wantNative := []string{"openai", "amazon-bedrock", "amazon-bedrock-runtime", "ollama", "lmstudio"}
	if len(declared) != len(wantNative)+1 {
		t.Fatalf("Codex built-in metadata has %d entries, want five native IDs plus its subscription plan: %v", len(declared), declared)
	}
	for _, name := range wantNative {
		if got, ok := declared[name]; !ok || got.ID != name {
			t.Errorf("Codex built-in %q = %+v (present %t), want that literal native ID", name, got, ok)
		}
	}
	if _, ok := declared["responses"]; ok {
		t.Errorf("responses is a wire name, not a Codex native provider key: %v", declared["responses"])
	}
	if sub, ok := declared["openai-codex"]; !ok || sub.ID != "openai" || !sub.YoloList {
		t.Errorf("Codex subscription plan = %+v (present %t), want openai with yolo-owned list", sub, ok)
	}
	for _, name := range []string{"openai", "amazon-bedrock", "amazon-bedrock-runtime", "ollama", "lmstudio"} {
		t.Run(name, func(t *testing.T) {
			got := renderCodexNativeFixture(t, packs, table, resolved, `{"codex":"`+name+`"}`)
			if got["model_provider"] != name || got["model"] != "fast" {
				t.Errorf("native selection = %v/%v, want literal %s/fast", got["model_provider"], got["model"], name)
			}
			rows, _ := got["model_providers"].(map[string]any)
			if rows[name] != nil {
				t.Errorf("native provider %q was emitted as a configured row: %v", name, rows[name])
			}
		})
	}

	// The model alias is not expanded, and absent/default do not create a native model value.
	for _, option := range []string{"", "default"} {
		name := "openai"
		profile := "openai"
		if option == "default" {
			profile = "openai-default"
			profiles[profile] = packload.UserProfile{Provider: name, Options: map[string]string{"model": option}}
			packs, table, resolved = codexNativeFixture(t, codexNativePoisonProviders, profiles)
		}
		if option == "" {
			profiles["openai-no-model"] = packload.UserProfile{Provider: name}
			packs, table, resolved = codexNativeFixture(t, codexNativePoisonProviders, profiles)
			profile = "openai-no-model"
		}
		got := renderCodexNativeFixture(t, packs, table, resolved, `{"codex":"`+profile+`"}`)
		if got["model_provider"] != name {
			t.Errorf("native provider = %v, want %s", got["model_provider"], name)
		}
		if _, ok := got["model"]; ok {
			t.Errorf("native model = %v for absent/default option %q, want no model", got["model"], option)
		}
	}

	// A via profile cannot manufacture a bridge selection for any native key. In particular,
	// Codex's early Bedrock-via branch must not select the bridge after its native row is dropped.
	viaProviders := strings.Replace(codexNativePoisonProviders,
		`"amazon-bedrock-runtime":{"base_url":`, `"amazon-bedrock-runtime":{"platform":"aws-bedrock","base_url":`, 1)
	viaProfiles := map[string]packload.UserProfile{}
	for _, name := range []string{"openai", "amazon-bedrock", "amazon-bedrock-runtime", "ollama", "lmstudio"} {
		viaProfiles["via-"+name] = packload.UserProfile{Provider: name, Via: "wire-bridge",
			Options: map[string]string{"model": "fast"}}
	}
	viaPacks, viaTable, viaResolved := codexNativeFixture(t, viaProviders, viaProfiles)
	for _, name := range []string{"openai", "amazon-bedrock", "amazon-bedrock-runtime", "ollama", "lmstudio"} {
		got := renderCodexNativeFixture(t, viaPacks, viaTable, viaResolved, `{"codex":"via-`+name+`"}`)
		if got["model_provider"] != name || got["model"] != "fast" {
			t.Errorf("native-key via selection for %s = %v/%v, want native ID/literal model", name, got["model_provider"], got["model"])
		}
		rows, _ := got["model_providers"].(map[string]any)
		if rows[name] != nil || strings.Contains(mustCompactJSON(t, rows), "127.0.0.1:8216") {
			t.Errorf("native-key via emitted a bridge row for %s: %v", name, rows)
		}
	}

	// The legacy subscription policy still owns its yolo list and writes no provider key;
	// a missing metadata table remains safe for this one old rule.
	sub := renderCodexNativeFixture(t, packs, table, resolved, `{"codex":"codex-subscription"}`)
	if sub["model_provider"] != nil || sub["model"] != "gpt-6.1-sol" {
		t.Errorf("subscription selection = %v/%v, want no provider and yolo's first model", sub["model_provider"], sub["model"])
	}
	rows, _ := sub["model_providers"].(map[string]any)
	if rows["openai-codex"] != nil {
		t.Errorf("subscription metadata/list policy wrote a generic row: %v", rows["openai-codex"])
	}
}

// A natively implemented provider is selectable even when the yolo declaration has no endpoint
// Codex can use; the native provider map, not codexReachable, supplies that client.
func TestCodexNativeSelectionDoesNotRequireAYoloEndpoint(t *testing.T) {
	packs, table, resolved := codexNativeFixture(t,
		`{"openai":{"models":{"default":"yolo-default","fast":"expanded-id"}}}`,
		map[string]packload.UserProfile{"openai-fast": {Provider: "openai", Options: map[string]string{"model": "fast"}}})
	got := renderCodexNativeFixture(t, packs, table, resolved, `{"codex":"openai-fast"}`)
	if got["model_provider"] != "openai" || got["model"] != "fast" {
		t.Errorf("endpoint-less native selection = %v/%v, want openai/fast", got["model_provider"], got["model"])
	}
	rows, _ := got["model_providers"].(map[string]any)
	if rows["openai"] != nil {
		t.Errorf("endpoint-less native selection emitted a custom row: %v", rows["openai"])
	}
}

func TestCodexKeepsCustomProviderCatalogRowsAndNoProfileSelection(t *testing.T) {
	profiles := map[string]packload.UserProfile{
		"responses": {Provider: "responses"}, "acme": {Provider: "acme"}, "llamacpp": {Provider: "llamacpp"},
	}
	packs, table, resolved := codexNativeFixture(t, codexNativePoisonProviders, profiles)
	got := renderCodexNativeFixture(t, packs, table, resolved, `{}`)
	rows, _ := got["model_providers"].(map[string]any)
	for _, name := range []string{"responses", "acme", "llamacpp"} {
		if rows[name] == nil {
			t.Errorf("custom provider %q lost its catalog row: %v", name, rows)
		}
	}
	for _, name := range []string{"openai", "amazon-bedrock", "amazon-bedrock-runtime", "ollama", "lmstudio", "openai-codex"} {
		if rows[name] != nil {
			t.Errorf("no-profile render emitted built-in/subscription row %q: %v", name, rows[name])
		}
	}
	for _, key := range []string{"model_provider", "model"} {
		if _, ok := got[key]; ok {
			t.Errorf("no profile wrote %s=%v", key, got[key])
		}
	}
}

func renderedFile(t *testing.T, home, rel string, c interface {
	Decode([]byte) (any, error)
}) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, rel))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := c.Decode(raw)
	if err != nil {
		t.Fatalf("%s does not decode: %v\n%s", rel, err, raw)
	}
	m, _ := decoded.(map[string]any)
	return m
}

// catalogRows is the provider table of agent's model file in home: pi's models.json `providers`,
// oh-omp's models.yml `providers`, opencode's opencode.json `provider`.
func catalogRows(t *testing.T, agent, home string) map[string]any {
	t.Helper()
	var doc map[string]any
	key := "providers"
	switch agent {
	case "pi":
		doc = renderedFile(t, home, ".pi/agent/models.json", codec.JSON{})
	case "oh-omp":
		doc = renderedFile(t, home, ".oh-omp/agent/models.yml", codec.YAML{})
	case "opencode":
		doc, key = renderedFile(t, home, ".config/opencode/opencode.json", codec.JSON{}), "provider"
	default:
		t.Fatalf("no model file known for %s", agent)
	}
	rows, _ := doc[key].(map[string]any)
	return rows
}

// THE DESIGN'S TABLE (§6.4): which shipped providers each agent has a provider of its own for, by
// the same key, read from each agent's installed code; openai-codex is the original rule (OQ-1).
// llamacpp is no agent's own under that key (pi's and oh-omp's are keyed `llama.cpp`), and kilo is
// not pi's, so those two still get rows and prove the table reached the derive.
var builtInByAgent = []struct {
	agent, pack string
	own         []string
	catalogued  []string
}{
	{"pi", "pi", []string{"zai", "cerebras", "openrouter", "openai-codex"}, []string{"llamacpp", "kilo"}},
	{"oh-omp", "omp", []string{"zai", "cerebras", "openrouter", "kilo", "openai-codex"}, []string{"llamacpp"}},
	{"opencode", "opencode", []string{"zai", "cerebras", "openrouter", "kilo", "openai-codex"}, []string{"llamacpp"}},
}

// NO AGENT CATALOGUES A PROVIDER IT HAS BUILT IN, with no profile active and with each of those
// providers' own profile selected: no row under the provider's name, nor under opencode's own id
// for zai's plan (zai-coding-plan), while a provider the agent does not implement still gets its
// row in the same render.
func TestNoAgentCataloguesAProviderItHasBuiltIn(t *testing.T) {
	for _, tc := range builtInByAgent {
		for _, profile := range append([]string{""}, tc.own...) {
			if profile == "openai-codex" {
				profile = "codex"
			}
			name := tc.agent + "/no profile"
			use := `{}`
			if profile != "" {
				name, use = tc.agent+"/"+profile, `{"`+tc.agent+`":"`+profile+`"}`
			}
			t.Run(name, func(t *testing.T) {
				if profile == "codex" && tc.agent == "oh-omp" {
					t.Skip("oh-omp's pack ships no codex profile")
				}
				rows := catalogRows(t, tc.agent, builtInRender(t, tc.pack, use))
				for _, own := range append([]string{"zai-coding-plan"}, tc.own...) {
					if row, ok := rows[own]; ok {
						t.Errorf("%s's model file has a row under %q, one of its own providers: %v", tc.agent, own, row)
					}
				}
				for _, cat := range tc.catalogued {
					if rows[cat] == nil {
						t.Errorf("%s's model file lost the row for %q, which it has no provider of its own "+
							"for, so this case measures nothing: %v", tc.agent, cat, rows)
					}
				}
			})
		}
	}
}

// PI TAKES ITS OWN ZAI, WITH NOTHING FROM YOLO'S LIST: pi 1.0.1's own zai already calls the coding
// plan and has no glm-4.6, which packs/zai lists and pi's settings derive used to name in
// enabledModels. The selection names pi's own provider, the profile's model as pi's own id, and a
// scope of the whole provider; nothing pi writes for zai names glm-4.6, and pi's extension is
// handed no list to register for it.
func TestPiTakesItsOwnZaiWithNothingFromYolosList(t *testing.T) {
	home := builtInRender(t, "pi", `{"pi":"zai"}`)
	if rows := catalogRows(t, "pi", home); rows["zai"] != nil {
		t.Errorf("models.json catalogues pi's own zai: %v", rows["zai"])
	}
	settings := renderedFile(t, home, ".pi/agent/settings.json", codec.JSON{})
	if settings["defaultProvider"] != "zai" || settings["defaultModel"] != "glm-5.3" {
		t.Errorf("pi selection = %v/%v, want its own zai on the profile's glm-5.3",
			settings["defaultProvider"], settings["defaultModel"])
	}
	if got := settings["enabledModels"]; !reflect.DeepEqual(got, []any{"zai/*"}) {
		t.Errorf("enabledModels = %v, want [zai/*]: pi's own list, whole", got)
	}
	sub, _ := settings["subagents"].(map[string]any)
	scope, _ := sub["modelScope"].(map[string]any)
	if got := scope["allow"]; !reflect.DeepEqual(got, []any{"zai/*"}) {
		t.Errorf("subagents.modelScope.allow = %v, want [zai/*]", got)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".pi/agent/settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "glm-4.6") {
		t.Errorf("pi's settings.json names glm-4.6, a model of yolo's zai list pi's own zai lacks:\n%s", raw)
	}
	if lists := renderedFile(t, home, ".pi/agent/yolo-model-lists.json", codec.JSON{}); len(lists) != 0 {
		t.Errorf("pi's extension is handed a list for its own zai: %v", lists)
	}
}

// OPENCODE TAKES ZAI'S PLAN THROUGH ITS OWN zai-coding-plan: opencode's own `zai` is the metered
// API, so the selection names its coding-plan provider, the one its menu enables, with no row for
// either and no small model of yolo's choosing.
func TestOpencodeTakesZaisPlanThroughItsOwnCodingPlanProvider(t *testing.T) {
	home := builtInRender(t, "opencode", `{"opencode":"zai"}`)
	cfg := renderedFile(t, home, ".config/opencode/opencode.json", codec.JSON{})
	if cfg["model"] != "zai-coding-plan/glm-5.3" {
		t.Errorf("model = %v, want zai-coding-plan/glm-5.3: opencode's own provider for the plan", cfg["model"])
	}
	if got := cfg["enabled_providers"]; !reflect.DeepEqual(got, []any{"zai-coding-plan"}) {
		t.Errorf("enabled_providers = %v, want [zai-coding-plan]", got)
	}
	if small, present := cfg["small_model"]; present {
		t.Errorf("small_model = %v, want none: no profile names one, and yolo's aliases are not opencode's", small)
	}
	rows := catalogRows(t, "opencode", home)
	for _, name := range []string{"zai", "zai-coding-plan"} {
		if rows[name] != nil {
			t.Errorf("opencode.json has a row under %q: %v", name, rows[name])
		}
	}
}

// OH-OMP ON ZAI RUNS ITS OWN: omp's own zai speaks Anthropic Messages at the coding plan's
// Anthropic route and reads ZAI_API_KEY, so it gets no models.yml row and no scope.
func TestOmpOnZaiRunsItsOwnZai(t *testing.T) {
	home := builtInRender(t, "omp", `{"oh-omp":"zai"}`)
	if rows := catalogRows(t, "oh-omp", home); rows["zai"] != nil {
		t.Errorf("models.yml catalogues omp's own zai: %v", rows["zai"])
	}
	if cfg := renderedFile(t, home, ".oh-omp/agent/config.yml", codec.YAML{}); cfg["enabledModels"] != nil {
		t.Errorf("config.yml scopes omp's own zai to %v", cfg["enabledModels"])
	}
}
