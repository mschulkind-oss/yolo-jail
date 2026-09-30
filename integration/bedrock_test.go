package integration

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// bedrock_test.go is the launch tier of the one Bedrock provider (docs/design/bedrock-plumbing.md
// OQ-BR9 and OQ-BR1, ruled 2026-09-29): a real launch of codex, pi and opencode alone, with
// `-p bedrock` and a region, renders each agent's OWN Bedrock client into its own file. The unit
// tests drive each derive through the boot render; this is the tier where the staged pack tree,
// the needs closure a launch runs (none of the three packs lists `bedrock`, so it must join
// through their `needs`), the composed table crossing into the jail and the stateful render all
// have to agree. No agent runs, and nothing reaches AWS.

// codexBedrockRegion is the built-in override's region line, under its own table.
var codexBedrockRegion = regexp.MustCompile(`(?m)^\[model_providers\.amazon-bedrock-runtime\.aws\]\s*\nregion\s*=\s*"([^"]*)"`)

func TestBedrockRendersEachAgentsOwnClient(t *testing.T) {
	requireJail(t)

	const region = "eu-west-1"
	const opus, sol = "global.anthropic.claude-opus-5-5", "us.openai.gpt-6.1-sol"
	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["codex", "pi", "opencode"], "providers": {"bedrock": {"region": "`+region+`"}}}`)

	r := runCommand(t, dir, append(jailRunArgs(), "-p", "bedrock", "--", "true"))
	if r.rc != 0 {
		t.Fatalf("a -p bedrock launch of codex, pi and opencode failed: rc %d\n%s", r.rc, r.combined())
	}
	for _, want := range []string{"+ bedrock (needed by ", "+ aws-auth (needed by bedrock)"} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("the launch must say the closure joined the Bedrock provider, %q:\n%s", want, r.stderr)
		}
	}

	t.Run("codex selects its built-in amazon-bedrock-runtime client", func(t *testing.T) {
		config := string(renderedSurface(t, dir, "codex", "config.toml"))
		if m := codexModelProviderAssign.FindStringSubmatch(config); m == nil || m[1] != "amazon-bedrock-runtime" {
			t.Errorf("codex model_provider = %v, want amazon-bedrock-runtime:\n%s", m, config)
		}
		if m := codexModelAssign.FindStringSubmatch(config); m == nil || m[1] != sol {
			t.Errorf("codex model = %v, want the first OpenAI entry %s:\n%s", m, sol, config)
		}
		if m := codexBedrockRegion.FindStringSubmatch(config); m == nil || m[1] != region {
			t.Errorf("codex's built-in override carries region %v, want %s:\n%s", m, region, config)
		}
		for _, row := range codexProviderRow.FindAllStringSubmatch(config, -1) {
			if row[1] == "bedrock" {
				t.Errorf("codex config.toml carries a generic model_providers.bedrock row:\n%s", config)
			}
		}
	})

	t.Run("pi catalogs the list under its built-in amazon-bedrock and selects it", func(t *testing.T) {
		settings := readPioencodeSurface(t, dir, "pi", "agent", "settings.json")
		if settings.provider != "amazon-bedrock" || settings.model != opus {
			t.Errorf("pi selection = %q/%q, want amazon-bedrock/%s", settings.provider, settings.model, opus)
		}
		models := readPioencodeSurface(t, dir, "pi", "agent", "models.json")
		requireCataloged(t, models.raw, "providers", "amazon-bedrock", "pi models.json")
		provs, _ := models.raw["providers"].(map[string]any)
		row, _ := provs["amazon-bedrock"].(map[string]any)
		for _, key := range []string{"baseUrl", "api", "apiKey"} {
			if v, ok := row[key]; ok {
				t.Errorf("pi's amazon-bedrock row carries %s = %v, which would move it off its built-in client", key, v)
			}
		}
		var ids []any
		list, _ := row["models"].([]any)
		for _, m := range list {
			entry, _ := m.(map[string]any)
			ids = append(ids, entry["id"])
		}
		if want := []any{opus, sol, "global.openai.gpt-6-astra"}; !reflect.DeepEqual(ids, want) {
			t.Errorf("pi's amazon-bedrock models = %v, want %v", ids, want)
		}
	})

	t.Run("opencode binds its built-in amazon-bedrock provider", func(t *testing.T) {
		config := readPioencodeSurface(t, dir, "config", "opencode", "opencode.json")
		if config.slashJoin != "amazon-bedrock/"+opus {
			t.Errorf("opencode model = %q, want amazon-bedrock/%s", config.slashJoin, opus)
		}
		requireCataloged(t, config.raw, "provider", "amazon-bedrock", "opencode.json")
		provs, _ := config.raw["provider"].(map[string]any)
		row, _ := provs["amazon-bedrock"].(map[string]any)
		if opts, _ := row["options"].(map[string]any); opts["region"] != region {
			t.Errorf("opencode's amazon-bedrock options = %v, want region %s", row["options"], region)
		}
		if v, ok := row["npm"]; ok {
			t.Errorf("opencode's amazon-bedrock row carries npm = %v, which overrides its own per-model routing", v)
		}
		if _, generic := provs["bedrock"]; generic {
			t.Errorf("opencode.json carries a generic provider.bedrock row: %v", provs["bedrock"])
		}
	})
}

// THE SHIPPED bedrock-bridge PROFILE, at a real launch: pi on it is refused before the jail starts,
// because the wire bridge has no upstream for a provider named by region alone
// (docs/design/bedrock-plumbing.md BR-D16), and the refusal names the profile and its way out.
func TestBedrockBridgeRefusesAnAgentTheBridgeCannotCarry(t *testing.T) {
	requireJail(t)

	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["pi"], "providers": {"bedrock": {"region": "us-east-1"}}}`)

	r := runCommand(t, dir, append(jailRunArgs(), "-p", "bedrock-bridge", "--", "true"))
	if r.rc == 0 {
		t.Fatalf("-p bedrock-bridge -- pi must be refused while the bridge cannot reach Bedrock:\n%s", r.combined())
	}
	for _, want := range []string{`profile "bedrock-bridge" (active for pi)`,
		"provider bedrock (via for pi) declares no chat-completions or Responses endpoint",
		`remove "via" from profile "bedrock-bridge"`} {
		if !strings.Contains(r.combined(), want) {
			t.Errorf("the refusal must say %q:\n%s", want, r.combined())
		}
	}
}
