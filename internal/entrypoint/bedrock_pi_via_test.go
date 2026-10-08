package entrypoint

import (
	"bytes"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// bedrock_pi_via_test.go pins pi's via row on a Bedrock provider (docs/design/wire-bridge-gateway.md
// WG-I36, WG-I49): under `-p bedrock-bridge` pi's models.json row for the selected provider points
// at its via route and speaks Bedrock's own Converse there (`bedrock-converse-stream`), with the
// bridge's caller token as its key, so pi's own AWS SDK sends unsigned Converse and the bridge
// signs it (WG-I48). Plain `-p bedrock` keeps pi's built-in amazon-bedrock client. The files go
// through the boot render a launch runs (ConfigurePackSurfaces) over the packs pi's needs closure
// selects with the bridge, and the environment through the host composition (AgentEnv).

// piBridgedRender boots pi's surfaces over pi's closure with the wire bridge and bedrock, the
// tables composed from userProviders (may be "") and, when withList, a company pack's Bedrock list.
func piBridgedRender(t *testing.T, userProviders string, withList bool, extra string, use string) *pioencodeRender {
	t.Helper()
	packs := testPacksForAgent(t, "pi", "bedrock", "wire-bridge")
	if withList {
		packs = append(packs, companyModelsPack(t, bedrockListAdd+extra))
	}
	providersJSON, wire := bedrockTablesFor(t, packs, userProviders, nil)
	r := &pioencodeRender{errw: &bytes.Buffer{}}
	r.e = &Env{Home: t.TempDir(), Workspace: t.TempDir(),
		Vars:   map[string]string{"YOLO_PROVIDERS": providersJSON, "YOLO_PROFILES": wire, "YOLO_USE_PROFILES": use},
		Stderr: r.errw}
	withCtxRoot(t, t.TempDir(), "pi")
	ConfigurePackSurfaces(r.e, packs)
	if fails := r.e.GenFailures(); len(fails) != 0 {
		t.Fatalf("boot render failed: %v\n%s", fails, r.errw.String())
	}
	return r
}

// TestPiOnBedrockBridgeSpeaksConverseToItsViaRoute: on the shipped bedrock-bridge, pi's row for
// `bedrock` is its via route speaking bedrock-converse-stream with the bridge's caller token, no
// amazon-bedrock row is written, and pi starts on `bedrock`. It fails if the models derive stops
// choosing Converse for a Bedrock via row (the row then speaks openai-completions).
func TestPiOnBedrockBridgeSpeaksConverseToItsViaRoute(t *testing.T) {
	r := piBridgedRender(t, `{"bedrock":{"region":"us-east-1"}}`, true, "", `{"pi":"bedrock-bridge"}`)
	rows, _ := r.piModels(t)["providers"].(map[string]any)
	row, _ := rows["bedrock"].(map[string]any)
	if row == nil {
		t.Fatalf("no bedrock via row in models.json: %v", rows)
	}
	for key, want := range map[string]string{
		"baseUrl": "http://127.0.0.1:8216/agent/pi",
		"api":     "bedrock-converse-stream",
		"apiKey":  "${YOLO_SERVICE_WIRE_BRIDGE_TOKEN}",
	} {
		if row[key] != want {
			t.Errorf("bedrock row %s = %v, want %q (row %v)", key, row[key], want, row)
		}
	}
	if models, _ := row["models"].([]any); len(models) != 3 {
		t.Errorf("the row's models = %v, want the supplied list's three", row["models"])
	}
	if rows["amazon-bedrock"] != nil {
		t.Errorf("a bridged profile wrote pi's built-in amazon-bedrock row: %v", rows["amazon-bedrock"])
	}
	if s := r.piSettings(t); s["defaultProvider"] != "bedrock" {
		t.Errorf("defaultProvider = %v, want bedrock, the via row", s["defaultProvider"])
	}
}

// TestPiOnPlainBedrockKeepsItsOwnConverseClient: with no via, pi's Bedrock models stay on its own
// amazon-bedrock provider, and nothing is written under `bedrock`.
func TestPiOnPlainBedrockKeepsItsOwnConverseClient(t *testing.T) {
	r := piBridgedRender(t, `{"bedrock":{"region":"us-east-1"}}`, true, "", `{"pi":"bedrock"}`)
	rows, _ := r.piModels(t)["providers"].(map[string]any)
	if rows["bedrock"] != nil {
		t.Errorf("plain bedrock wrote a via-shaped bedrock row: %v", rows["bedrock"])
	}
	native, _ := rows["amazon-bedrock"].(map[string]any)
	if native == nil {
		t.Fatalf("no amazon-bedrock row: %v", rows)
	}
	for _, key := range []string{"baseUrl", "api", "apiKey"} {
		if v, ok := native[key]; ok {
			t.Errorf("the native row carries %s = %v", key, v)
		}
	}
	if s := r.piSettings(t); s["defaultProvider"] != "amazon-bedrock" {
		t.Errorf("defaultProvider = %v, want amazon-bedrock", s["defaultProvider"])
	}
}

// TestANarrowedBedrockListRegistersOnConverseUnderTheVia: a list a company `only` narrowed is
// registered for the via row under `bedrock`, on bedrock-converse-stream, so the extension's
// registration speaks the row's API. It fails if the model-lists derive keeps chat-completions for
// a Bedrock via row.
func TestANarrowedBedrockListRegistersOnConverseUnderTheVia(t *testing.T) {
	r := piBridgedRender(t, `{"bedrock":{"region":"us-east-1"}}`, true,
		`,{"kind":"models","provider":"bedrock","only":["global.anthropic.claude-opus-5-5"]}`, `{"pi":"bedrock-bridge"}`)
	lists, _ := r.surface(t, ".pi", "agent", "yolo-model-lists.json")["providers"].(map[string]any)
	reg, _ := lists["bedrock"].(map[string]any)
	if reg == nil {
		t.Fatalf("no registration under bedrock: %v", lists)
	}
	if reg["api"] != "bedrock-converse-stream" {
		t.Errorf("registration api = %v, want bedrock-converse-stream", reg["api"])
	}
	if models, _ := reg["models"].([]any); len(models) != 1 {
		t.Errorf("registered models = %v, want the narrowed one", reg["models"])
	}
	if lists["amazon-bedrock"] != nil {
		t.Errorf("a bridged profile registered pi's built-in amazon-bedrock: %v", lists["amazon-bedrock"])
	}
}

// TestPiOnBedrockBridgeIsHandedTheProvidersRegion: pi's AWS SDK will not build a Converse client
// without a region, even when the bridge picks the host, so a region the provider declares reaches
// pi as AWS_REGION on the via route too, through the env composition a launch runs. It fails if
// the env derive relays the region only for pi's native client.
func TestPiOnBedrockBridgeIsHandedTheProvidersRegion(t *testing.T) {
	packs := testPacksForAgent(t, "pi", "bedrock", "wire-bridge")
	user := jsonx.NewOrderedMap()
	bedrock := jsonx.NewOrderedMap()
	bedrock.Set("region", "eu-west-1")
	user.Set("bedrock", bedrock)
	providers, err := packload.ComposeProviders(user, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles(packs, nil, providers)
	if err != nil {
		t.Fatal(err)
	}
	vars, err := packload.AgentEnv(packs, providers, map[string]string{"pi": "bedrock-bridge"}, "pi", "bedrock-bridge",
		func(string) (string, bool) { return "", false }, packload.WithResolvedProfiles(resolved))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, v := range vars {
		got[v.Key] = v.Value
	}
	if got["AWS_REGION"] != "eu-west-1" {
		t.Errorf("pi's environment on bedrock-bridge = %v, want AWS_REGION=eu-west-1", got)
	}
}
