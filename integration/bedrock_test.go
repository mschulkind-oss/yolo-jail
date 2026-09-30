package integration

import (
	"os"
	"path/filepath"
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
	// GPT-6.1 Sol in eu-west-1 too: no Region detection, and a user outside the US names another
	// model in a profile (docs/design/bedrock-plumbing.md BR-D19, superseding BR-D17).
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

// THE SHIPPED bedrock-bridge PROFILE, at a real launch: pi on it starts, and the wire bridge
// carries pi's requests to bedrock-runtime's own URL, composed from the provider's region, since
// the provider names no address (docs/design/wire-bridge-gateway.md §8 step 1, WG-I39). Before
// that build this launch was refused. No request leaves the jail: no AWS credential is delivered,
// so pi's route answers with a 503 that names the upstream it composed and the credential
// sources it needs, which shows the composed URL without calling AWS.
func TestBedrockBridgeCarriesPiToRuntimeInItsRegion(t *testing.T) {
	requireJail(t)

	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["pi"], "providers": {"bedrock": {"region": "us-east-1"}}}`)

	// The via listener is the only one this launch binds, so the endpoint file names it: at the
	// declared :8216 on a private network namespace, and at a port the launcher picked on a shared
	// one (a nested jail's), which is why the address is read rather than written down.
	script := `set -u
addr=$(cat "$YOLO_SERVICE_WIRE_BRIDGE_ENDPOINT")
code=$(curl -sS -o /workspace/bedrock-bridge-pi.json -w '%{http_code}' \
  "http://$addr/agent/pi/chat/completions" -H 'content-type: application/json' \
  -H "authorization: Bearer $YOLO_SERVICE_WIRE_BRIDGE_TOKEN" -d '{"model":"m","messages":[]}')
echo "CODE=$code"`
	r := runCommand(t, dir, append(jailRunArgs(), "-p", "bedrock-bridge", "--", "bash", "-lc", script))
	if r.rc != 0 {
		t.Fatalf("-p bedrock-bridge -- pi must start now that the bridge composes Bedrock's URL:\n%s", r.combined())
	}
	if strings.Contains(r.combined(), `remove "via" from profile "bedrock-bridge"`) {
		t.Errorf("the launch still names the via as a problem:\n%s", r.combined())
	}
	body, err := os.ReadFile(filepath.Join(dir, "bedrock-bridge-pi.json"))
	if err != nil {
		t.Fatalf("pi's request to its via route wrote nothing (%v):\n%s", err, r.combined())
	}
	if !strings.Contains(r.stdout, "CODE=503") ||
		!strings.Contains(string(body), "goes to Bedrock (https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1)") {
		t.Errorf("pi's via route must answer 503 naming runtime's URL composed from us-east-1: %s\n%s", body, r.combined())
	}
}

// OPENCODE AND AWS_DEFAULT_REGION, at a real launch (docs/design/bedrock-plumbing.md BR-D18):
// opencode's Bedrock loader never reads AWS_DEFAULT_REGION and falls back to us-east-1, so a
// `-p bedrock` launch whose only region is an env_sources AWS_DEFAULT_REGION is refused before
// the jail starts, naming opencode and the variable it would have ignored.
func TestBedrockRefusesOpencodeARegionItDoesNotRead(t *testing.T) {
	requireJail(t)

	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["opencode"], "env_sources": [{"AWS_DEFAULT_REGION": "eu-west-1"}]}`)

	r := runCommand(t, dir, append(jailRunArgs(), "-p", "bedrock", "--", "true"))
	if r.rc == 0 {
		t.Fatalf("-p bedrock -- opencode with only AWS_DEFAULT_REGION must be refused:\n%s", r.combined())
	}
	for _, want := range []string{"Refusing to launch: a selected provider is reached through a region",
		`selected for opencode: its composed entry sets no "region", and AWS_REGION is not set`,
		"AWS_DEFAULT_REGION reaches opencode, which does not read it"} {
		if !strings.Contains(r.combined(), want) {
			t.Errorf("the refusal must say %q:\n%s", want, r.combined())
		}
	}
}

// THE HOST'S AWS CONFIG GIVES THE REGION, at a real launch (docs/design/bedrock-plumbing.md
// BR-DIR1): opencode on `-p bedrock` with no region on the provider and none in env_sources is
// given its default profile's region from the launcher's ~/.aws/config (an invented one, in the
// isolated home), in opencode's own env file and nowhere else, and the launch says where it came
// from. The same launch without the file is TestBedrockRefusesOpencodeARegionItDoesNotRead's
// refusal, which the unit tier pins too.
func TestBedrockTakesTheRegionOfTheHostsAWSConfig(t *testing.T) {
	requireJail(t)

	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["opencode"]}`)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".aws"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".aws", "config"),
		[]byte("[default]\nregion = eu-north-1\n\n[profile team]\nregion = ap-northeast-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	script := `echo "SHELL_REGION=${AWS_REGION-unset}"; . ~/.config/yolo-agent-env/opencode.sh; echo "REGION=${AWS_REGION-unset}"`
	r := runCommand(t, dir, append(jailRunArgs(), "-p", "bedrock", "--", "bash", "-lc", script))
	if r.rc != 0 {
		t.Fatalf("a region in the host's ~/.aws/config must let -p bedrock -- opencode launch: rc %d\n%s", r.rc, r.combined())
	}
	if got := kvLine(r.stdout, "REGION"); got != "eu-north-1" {
		t.Errorf("opencode's env file carries AWS_REGION=%q, want the default profile's eu-north-1:\n%s", got, r.combined())
	}
	if got := kvLine(r.stdout, "SHELL_REGION"); got != "unset" {
		t.Errorf("the region is opencode's, yet a bare shell in the jail has AWS_REGION=%q", got)
	}
	if want := `Region: AWS_REGION=eu-north-1 for opencode on provider "bedrock", read from ~/.aws/config [default]`; !strings.Contains(r.stderr, want) {
		t.Errorf("the launch must say where the region came from, %q:\n%s", want, r.stderr)
	}
}
