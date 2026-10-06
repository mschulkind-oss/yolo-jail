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
// lost a shipped provider's name is caught rather than agreed with.

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
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

// renderedFile decodes home/rel with codec (nil when the boot wrote no such file).
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
