package entrypoint

// claudetieraliases_test.go pins MM-D17 (docs/design/model-lists-and-pickers.md, for OQ-PSW1):
// claude's derive reads yolo's conventional aliases for its tiers, `frontier` for opus, `balanced`
// for sonnet and `fast` for haiku, beside claude's own `opus`, `sonnet` and `haiku`, which win where
// a provider declares both. Each cell drives one of the derive's tier reads through its production
// call site (renderClaudeModels: the boot render and packload.AgentEnv): the provider branch's
// sonnet and haiku reads, on a routed provider and on claude's own Bedrock client, and the tier
// pins under an `only`. The openai-codex branch's read is pinned by
// TestAnAliasForADeclaredCodexIDChangesNoConsumer. Dropping a conventional name from the derive's
// tier table fails every cell.

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// zaiAliases is a user `providers.zai.models` map of string-form aliases over z.ai's own list.
func zaiAliases(aliases map[string]string) *jsonx.OrderedMap {
	models := jsonx.NewOrderedMap()
	for _, k := range []string{"frontier", "opus", "balanced", "sonnet", "fast", "haiku"} {
		if v, ok := aliases[k]; ok {
			models.Set(k, v)
		}
	}
	zai := jsonx.NewOrderedMap()
	zai.Set("models", models)
	user := jsonx.NewOrderedMap()
	user.Set("zai", zai)
	return user
}

// ON A ROUTED PROVIDER a provider that declares only the conventional names pins claude's Sonnet
// and Haiku tiers to them, where it used to leave both on the default. The Opus tier stays the
// selected model, as the routed branch has always pinned it, `frontier` or not.
func TestClaudeRoutedTiersReadTheConventionalAliases(t *testing.T) {
	packs := testPacksForAgent(t, "claude", "zai")
	got := renderClaudeModels(t, packs, zaiAliases(map[string]string{
		"frontier": "glm-4.6", "balanced": "glm-5.3-flash", "fast": "glm-4.6"}), nil, "zai")
	want := map[string]string{
		"ANTHROPIC_DEFAULT_OPUS_MODEL":   "glm-5.3[1m]",
		"ANTHROPIC_DEFAULT_SONNET_MODEL": "glm-5.3-flash[1m]",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":  "glm-4.6[1m]",
		"ANTHROPIC_SMALL_FAST_MODEL":     "glm-4.6[1m]",
	}
	for k, v := range want {
		if got.env[k] != v {
			t.Errorf("%s = %q, want %q", k, got.env[k], v)
		}
	}
}

// CLAUDE'S OWN NAME WINS where a provider declares both: whoever wrote `sonnet` wrote it for claude.
func TestClaudeTierVendorNameWinsOverTheConventionalAlias(t *testing.T) {
	packs := testPacksForAgent(t, "claude", "zai")
	got := renderClaudeModels(t, packs, zaiAliases(map[string]string{
		"balanced": "glm-4.6", "sonnet": "glm-5.3-flash", "fast": "glm-5.3-flash", "haiku": "glm-4.6"}), nil, "zai")
	if got.env["ANTHROPIC_DEFAULT_SONNET_MODEL"] != "glm-5.3-flash[1m]" {
		t.Errorf("ANTHROPIC_DEFAULT_SONNET_MODEL = %q, want sonnet's glm-5.3-flash[1m] over balanced's",
			got.env["ANTHROPIC_DEFAULT_SONNET_MODEL"])
	}
	if got.env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] != "glm-4.6[1m]" {
		t.Errorf("ANTHROPIC_DEFAULT_HAIKU_MODEL = %q, want haiku's glm-4.6[1m] over fast's",
			got.env["ANTHROPIC_DEFAULT_HAIKU_MODEL"])
	}
}

// ON CLAUDE'S OWN BEDROCK CLIENT the conventional names are read too, held to the makers that
// client can call: a `sonnet` naming another maker's model gives way to the tier's next name,
// `balanced` (MM-D18), rather than leaving the tier on the default.
func TestClaudeNativeBedrockTiersReadTheConventionalAliases(t *testing.T) {
	company := companyModelsPack(t, `{"kind":"models","provider":"bedrock","add":[
	    {"id":"us.anthropic.claude-opus-5-6","vendor":"anthropic","alias":"default"},
	    {"id":"global.moonshot.kimi-k3","vendor":"moonshot","alias":"sonnet"},
	    {"id":"us.anthropic.claude-sonnet-5","vendor":"anthropic","alias":"balanced"},
	    {"id":"us.anthropic.claude-haiku-5","vendor":"anthropic","alias":"fast"}]}`)
	got := renderClaudeModels(t, append(testPacksForAgent(t, "claude"), company), nil, nil, "bedrock")
	want := map[string]string{
		"ANTHROPIC_DEFAULT_OPUS_MODEL":   "us.anthropic.claude-opus-5-6",
		"ANTHROPIC_DEFAULT_SONNET_MODEL": "us.anthropic.claude-sonnet-5",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":  "us.anthropic.claude-haiku-5",
		"ANTHROPIC_SMALL_FAST_MODEL":     "us.anthropic.claude-haiku-5",
	}
	for k, v := range want {
		if got.env[k] != v {
			t.Errorf("%s = %q, want %q", k, got.env[k], v)
		}
	}
}

// UNDER AN `only` every tier reads its names, the Opus tier included, since the narrowed list's
// pins read an alias for every tier (MM-D2): `frontier`, `balanced` and `fast` each pin their tier,
// and the fable tier, which has no conventional alias, takes the default entry.
func TestClaudeUnderAnOnlyReadsTheConventionalAliases(t *testing.T) {
	company := companyModelsPack(t, `{"kind":"models","provider":"bedrock","add":[
	    {"id":"us.anthropic.claude-sonnet-5","vendor":"anthropic","alias":"default"},
	    {"id":"us.anthropic.claude-opus-5-6","vendor":"anthropic","alias":"frontier"},
	    {"id":"us.anthropic.claude-sonnet-5-1","vendor":"anthropic","alias":"balanced"},
	    {"id":"us.anthropic.claude-haiku-5","vendor":"anthropic","alias":"fast"}]},
	  {"kind":"models","provider":"bedrock","only":["us.anthropic.claude-sonnet-5","us.anthropic.claude-opus-5-6",
	    "us.anthropic.claude-sonnet-5-1","us.anthropic.claude-haiku-5"]}`)
	got := renderClaudeModels(t, append(testPacksForAgent(t, "claude"), company), nil, nil, "bedrock")
	settingsEnv, _ := got.settings["env"].(map[string]any)
	want := map[string]string{
		"ANTHROPIC_DEFAULT_OPUS_MODEL":   "us.anthropic.claude-opus-5-6",
		"ANTHROPIC_DEFAULT_SONNET_MODEL": "us.anthropic.claude-sonnet-5-1",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":  "us.anthropic.claude-haiku-5",
		"ANTHROPIC_DEFAULT_FABLE_MODEL":  "us.anthropic.claude-sonnet-5",
	}
	for k, v := range want {
		if got.env[k] != v {
			t.Errorf("process env %s = %q, want %q", k, got.env[k], v)
		}
		if settingsEnv[k] != v {
			t.Errorf("settings env %s = %v, want %q, the value the process env carries", k, settingsEnv[k], v)
		}
	}
}
