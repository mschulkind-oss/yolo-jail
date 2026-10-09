package entrypoint

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func modelListFixtureTable(t *testing.T, id, vendor string) ([]*packload.Pack, *jsonx.OrderedMap, *jsonx.OrderedMap,
	map[string]bool, map[string]packload.ResolvedProfile) {
	t.Helper()
	packs := testPacksForAgent(t, "claude", "codex")
	packDir := t.TempDir()
	var user *jsonx.OrderedMap
	var manifest string
	switch {
	case id == "":
		manifest = `{"contributes":[{"kind":"models","provider":"bedrock","only":["not-present"]}]}`
	case vendor == "":
		// A vendorless alias can only enter through user configuration: pack manifests require
		// each declared model's maker. This also pins the final user alias after an empty `only`.
		manifest = `{"contributes":[{"kind":"models","provider":"bedrock","only":["not-present"]}]}`
		models := jsonx.NewOrderedMap()
		models.Set(id, id)
		provider := jsonx.NewOrderedMap()
		provider.Set("models", models)
		user = jsonx.NewOrderedMap()
		user.Set("bedrock", provider)
	default:
		aliasVendor := "openai"
		if vendor == "openai" {
			aliasVendor = "anthropic"
		}
		manifest = `{"contributes":[{"kind":"models","provider":"bedrock","add":[{"id":"` + id + `","vendor":"` + vendor + `"}]},` +
			`{"kind":"models","provider":"bedrock","only":["` + id + `"]}]}`
		facts := jsonx.NewOrderedMap()
		facts.Set("id", id)
		facts.Set("vendor", aliasVendor)
		models := jsonx.NewOrderedMap()
		models.Set("fixture-alias", facts)
		provider := jsonx.NewOrderedMap()
		provider.Set("models", models)
		user = jsonx.NewOrderedMap()
		user.Set("bedrock", provider)
	}
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture, problems := packload.LoadDir(packDir, "fixture-model-list")
	if len(problems) != 0 {
		t.Fatalf("load model-list fixture: %v", problems)
	}
	packs = append(packs, fixture)
	presence := map[string]bool{}
	table, err := packload.ComposeProviders(user, packs, packload.WithModelListPresence(func(name string, supplied bool) {
		presence[name] = supplied
	}))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles(packs, nil, table)
	if err != nil {
		t.Fatal(err)
	}
	return packs, user, table, presence, resolved
}

func modelListWarningFor(packs []*packload.Pack, table *jsonx.OrderedMap, presence map[string]bool,
	resolved map[string]packload.ResolvedProfile, agent, profile string) bool {
	got := packload.EmptyModelLists(packload.EmptyModelListInput{
		Packs: packs, Providers: table, Presence: presence, Resolved: resolved,
		Profiles: map[string]string{agent: profile}, Sets: map[string][]string{agent: {profile}},
	})
	for _, row := range got {
		if row.Program == agent && row.Provider == "bedrock" {
			return true
		}
	}
	return false
}

func renderedModelID(t *testing.T, packs []*packload.Pack, user, table *jsonx.OrderedMap,
	resolved map[string]packload.ResolvedProfile, agent, profile, id string) bool {
	t.Helper()
	switch agent {
	case "claude":
		rendered := renderClaudeModels(t, packs, user, nil, profile)
		return strings.Contains(mustCompactJSON(t, rendered.settings), id) || strings.Contains(fmt.Sprint(rendered.env), id)
	case "codex":
		cfg := renderCodexConfig(t, mustCompactJSON(t, table), `{"codex":"`+profile+`"}`,
			mustCompactJSON(t, packload.ProfilesWireTable(resolved)))
		return strings.Contains(mustCompactJSON(t, cfg), id)
	default:
		t.Fatalf("unhandled derive fixture %q", agent)
		return false
	}
}

// The evaluator's metadata must agree with the unchanged shipped derives on the same final list:
// Claude narrows only its native Bedrock client, while Codex keeps its OpenAI-maker restriction
// on both native and via routes. A vendorless row remains callable for both.
func TestEmptyModelListMakerRulesMatchUnchangedClaudeAndCodexDerives(t *testing.T) {
	for _, tc := range []struct {
		name, id, vendor, agent, profile string
		warn, rendered                   bool
	}{
		{"Claude native rejects another maker", "fixture-openai-model", "openai", "claude", "bedrock", true, false},
		{"Claude via accepts another maker", "fixture-openai-via", "openai", "claude", "bedrock-bridge", false, true},
		{"Claude native accepts Anthropic", "fixture-anthropic-model", "anthropic", "claude", "bedrock", false, true},
		{"Claude native rejects an unknown maker", "fixture-unknown-claude", "fixture-maker", "claude", "bedrock", true, false},
		{"Claude via accepts an unknown maker", "fixture-unknown-claude-via", "fixture-maker", "claude", "bedrock-bridge", false, true},
		{"Claude native accepts vendorless", "fixture-vendorless-claude", "", "claude", "bedrock", false, true},
		{"Claude via accepts vendorless", "fixture-vendorless-claude-via", "", "claude", "bedrock-bridge", false, true},
		{"Codex native rejects Anthropic", "fixture-anthropic-codex", "anthropic", "codex", "bedrock", true, false},
		{"Codex via rejects Anthropic", "fixture-anthropic-via", "anthropic", "codex", "bedrock-bridge", true, false},
		{"Codex native rejects an unknown maker", "fixture-unknown-codex", "fixture-maker", "codex", "bedrock", true, false},
		{"Codex via rejects an unknown maker", "fixture-unknown-codex-via", "fixture-maker", "codex", "bedrock-bridge", true, false},
		{"Codex native accepts OpenAI", "fixture-openai-codex", "openai", "codex", "bedrock", false, true},
		{"Codex native accepts vendorless", "fixture-vendorless", "", "codex", "bedrock", false, true},
		{"Codex via accepts vendorless", "fixture-vendorless-via", "", "codex", "bedrock-bridge", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packs, user, table, presence, resolved := modelListFixtureTable(t, tc.id, tc.vendor)
			if got := modelListWarningFor(packs, table, presence, resolved, tc.agent, tc.profile); got != tc.warn {
				t.Errorf("empty-model evaluator warning = %v, want %v", got, tc.warn)
			}
			if got := renderedModelID(t, packs, user, table, resolved, tc.agent, tc.profile, tc.id); got != tc.rendered {
				t.Errorf("unchanged %s derive rendered model id = %v, want %v; provider=%s", tc.agent, got, tc.rendered, mustCompactJSON(t, table))
			}
		})
	}
}

// The empty alias is a real second-pass alias in the unchanged derives. It contributes a
// vendorless row and must not make the diagnostic report a list that Codex can render.
func TestEmptyModelListEmptyAliasMatchesCodexDerive(t *testing.T) {
	const id = "fixture-empty-alias"
	packs, user, _, _, _ := modelListFixtureTable(t, id, "")
	providerValue, ok := user.Get("bedrock")
	if !ok {
		t.Fatal("fixture user providers lack bedrock")
	}
	provider, ok := providerValue.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("bedrock user provider = %T, want object", providerValue)
	}
	modelsValue, ok := provider.Get("models")
	if !ok {
		t.Fatal("fixture bedrock provider lacks models")
	}
	models, ok := modelsValue.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("bedrock models = %T, want object", modelsValue)
	}
	models.Set(id, nil)
	models.Set("", id)

	presence := map[string]bool{}
	table, err := packload.ComposeProviders(user, packs, packload.WithModelListPresence(func(name string, supplied bool) {
		presence[name] = supplied
	}))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles(packs, nil, table)
	if err != nil {
		t.Fatal(err)
	}
	if !presence["bedrock"] {
		t.Fatal("the empty-key user alias was not recorded as a supplied list")
	}
	if modelListWarningFor(packs, table, presence, resolved, "codex", "bedrock") {
		t.Error("Codex derives retain the empty-key alias as a vendorless callable row; the evaluator must not warn")
	}
	if !renderedModelID(t, packs, user, table, resolved, "codex", "bedrock", id) {
		t.Errorf("unchanged Codex derive did not render the empty-key alias id %q; providers=%s", id, mustCompactJSON(t, table))
	}
}

// A source-empty native Claude list is the declared exception; it does not hide a non-empty
// filtered list, and the unchanged derive still receives no fabricated row.
func TestEmptyModelListNativeExceptionMatchesClaudeDerive(t *testing.T) {
	packs, user, table, presence, resolved := modelListFixtureTable(t, "", "")
	if modelListWarningFor(packs, table, presence, resolved, "claude", "bedrock") {
		t.Fatal("Claude's supplied native empty list should use its declared defaulting exception")
	}
	if !modelListWarningFor(packs, table, presence, resolved, "codex", "bedrock") {
		t.Fatal("Codex's same supplied source-empty list must not inherit Claude's exception")
	}
	claude := renderClaudeModels(t, packs, user, nil, "bedrock")
	if claude.env["CLAUDE_CODE_USE_BEDROCK"] != "1" {
		t.Errorf("source-empty native profile lost its Bedrock default provider: env=%v", claude.env)
	}
	for _, key := range []string{"ANTHROPIC_MODEL", "CLAUDE_CODE_SUBAGENT_MODEL"} {
		if value, set := claude.env[key]; set {
			t.Errorf("source-empty list must not pin %s=%q over Claude's native default", key, value)
		}
	}
	codex := renderCodexConfig(t, mustCompactJSON(t, table), `{"codex":"bedrock"}`,
		mustCompactJSON(t, packload.ProfilesWireTable(resolved)))
	if codex["model_provider"] != "amazon-bedrock-runtime" || codex["model"] != nil {
		t.Errorf("source-empty list must leave Codex on its native default provider/model state: %v", codex)
	}
}
