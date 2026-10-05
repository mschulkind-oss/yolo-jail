package run

// copilotbyok_test.go pins copilot's provider delivery — a yolo.env producer, because
// copilot's BYOK is env-var-only (its own help topic: no provider keys exist in any
// copilot config file). The tests run the REAL copilot derive.lua against the REAL
// shipped provider packs, the same tier zaipack_test.go pins claude's half at: what
// lands is what the reach table in
// docs/reference/cerebras-pack-and-copilot-delivery.md#which-agents-a-provider-can-reach
// claims, composed through composePackChannel and argv assembly.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// copilotProvidersPath is where copilot's providers.json sits in a container jail: beside its env
// file, under the jail home (docs/design/model-lists-and-pickers.md MM-D33).
const copilotProvidersPath = "/home/agent/.config/yolo-agent-env/copilot.providers.json"

// copilotProvidersFile delivers the launch's channel the way deliverChannel does on podman and
// returns copilot's providers.json as copilot reads it, and its mode; nil when none was written.
func copilotProvidersFile(t *testing.T, la assembled) (map[string]any, os.FileMode) {
	t.Helper()
	ws := t.TempDir()
	deliverChannel(ws, "podman", la.in.envChannel(la.o))
	path := filepath.Join(ws, agentEnvStateDir, "copilot.providers.json")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, 0
	}
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("copilot's providers.json is not JSON: %v\n%s", err, raw)
	}
	return doc, fi.Mode().Perm()
}

// providersFileModels is the ids of a providers.json's model rows, in file order, each checked to
// name provider as its provider and its id as its wire model.
func providersFileModels(t *testing.T, doc map[string]any, provider string) []string {
	t.Helper()
	rows, _ := doc["models"].([]any)
	var ids []string
	for _, r := range rows {
		row, _ := r.(map[string]any)
		id, _ := row["id"].(string)
		if row["provider"] != provider || row["wireModel"] != id {
			t.Errorf("model row %v: want provider %q and its id as wireModel", row, provider)
		}
		ids = append(ids, id)
	}
	return ids
}

// copilotLaunch is zaiLaunch with the copilot agent pack beside a provider pack, the
// latter's key hydrated.
func copilotLaunch(t *testing.T, provider string, tune func(*Options)) []string {
	t.Helper()
	// The bridge, listed the way ResolveNeeds joins it: cerebras's `needs` names the
	// copilot bin as well as claude's (D-3 — copilot's derive PREFERS an anthropic
	// endpoint), and since the bridged address moved into the adapter's own manifest the
	// pack that declares it has to be in the set for the pairing to resolve. Harmless for
	// a provider that speaks anthropic natively: an adapter never overwrites a real
	// endpoint.
	packs := []*packload.Pack{
		officialPack(t, "copilot"), officialPack(t, provider), officialPack(t, "wire-bridge"),
	}
	var env *jsonx.OrderedMap
	if provider == "cerebras" {
		env = cerebrasKey()
	} else {
		env = hydratedKey()
	}
	return zaiLaunch(t, packs, bareConfig(), env, tune)
}

// copilotLaunchAssembled is copilotLaunch for the tests that assert the channel
// file beside the argv.
func copilotLaunchAssembled(t *testing.T, provider string, tune func(*Options)) assembled {
	t.Helper()
	// The bridge, listed the way ResolveNeeds joins it: cerebras's `needs` names the
	// copilot bin as well as claude's (D-3 — copilot's derive PREFERS an anthropic
	// endpoint), and since the bridged address moved into the adapter's own manifest the
	// pack that declares it has to be in the set for the pairing to resolve. Harmless for
	// a provider that speaks anthropic natively: an adapter never overwrites a real
	// endpoint.
	packs := []*packload.Pack{
		officialPack(t, "copilot"), officialPack(t, provider), officialPack(t, "wire-bridge"),
	}
	var env *jsonx.OrderedMap
	if provider == "cerebras" {
		env = cerebrasKey()
	} else {
		env = hydratedKey()
	}
	return zaiLaunchAssembled(t, packs, bareConfig(), env, tune)
}

// TestCopilotByokComposesCerebrasThroughTheBridge: `-p cerebras` on a copilot
// launch arms the full BYOK block — and since cerebras now declares an
// anthropic endpoint (the wire bridge's loopback URL, wire-bridge.md §3.3),
// D-3 routes copilot there: the anthropic type, no WIRE_API, the one model
// alias, and as the key the bridge's per-launch caller token, never the
// cerebras key, which the bridge adds upstream itself (WB-D18). copilot's
// derive reads every endpoint the composed table carries and prefers the
// anthropic one, so the bridge is as much copilot's route as claude's — which
// is why cerebras's `needs` entry names the copilot bin too.
//
// CEREBRAS SHIPS A LIST, so in a jail copilot also gets the whole of it as its providers.json
// (docs/design/model-lists-and-pickers.md MM-D31), beside its env file, the variable pointing
// there and the start model spelled as the file's selection id, `<provider>/<id>`.
func TestCopilotByokComposesCerebrasThroughTheBridge(t *testing.T) {
	la := copilotLaunchAssembled(t, "cerebras", func(o *Options) { o.ProfileName = "cerebras" })

	got := la.channelEnv(t,
		"COPILOT_PROVIDER_BASE_URL", "COPILOT_PROVIDER_TYPE", "COPILOT_PROVIDER_WIRE_API",
		"COPILOT_MODEL", "COPILOT_PROVIDER_API_KEY", "COPILOT_PROVIDERS_CONFIG")
	// The key copilot sends to the bridge is the bridge's per-launch caller token, never
	// the cerebras key: the bridge adds that upstream itself (WB-D18), and a loopback port
	// is no place to send a provider's credential.
	token := la.o.callerTokens["YOLO_SERVICE_WIRE_BRIDGE_TOKEN"]
	if len(token) != 64 {
		t.Fatalf("the launch minted no wire-bridge caller token: %q", la.o.callerTokens)
	}
	want := []string{
		"COPILOT_MODEL=cerebras/qwen-3.8-27b",
		"COPILOT_PROVIDER_API_KEY=" + token,
		"COPILOT_PROVIDER_BASE_URL=http://127.0.0.1:8214",
		"COPILOT_PROVIDER_TYPE=anthropic",
		"COPILOT_PROVIDERS_CONFIG=" + copilotProvidersPath,
	}
	if len(got) != len(want) {
		t.Fatalf("copilot BYOK env = %q, want %q (no WIRE_API on the anthropic type)",
			got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("copilot BYOK env %d = %q, want %q", i, got[i], want[i])
		}
	}
	doc, mode := copilotProvidersFile(t, la)
	if doc == nil {
		t.Fatal("no providers.json was written beside copilot's env file")
	}
	if mode != agentEnvFileMode {
		t.Errorf("providers.json mode = %o, want the env file's %o: it carries a credential", mode, agentEnvFileMode)
	}
	wantProvider := []any{map[string]any{"name": "cerebras", "type": "anthropic",
		"baseUrl": "http://127.0.0.1:8214", "apiKey": token}}
	if !reflect.DeepEqual(doc["providers"], wantProvider) {
		t.Errorf("providers.json providers = %v, want %v", doc["providers"], wantProvider)
	}
	if ids := providersFileModels(t, doc, "cerebras"); !slices.Equal(ids, []string{"qwen-3.8-27b"}) {
		t.Errorf("providers.json models = %v, want cerebras's whole list, its one entry", ids)
	}
}

// TestCopilotByokPrefersTheAnthropicRoute: a provider speaking both protocols (zai) gets
// copilot's anthropic spelling — no WIRE_API at all, because copilot's wire_api enum
// speaks only to the openai type (D-3: the anthropic route is the richer surface).
func TestCopilotByokPrefersTheAnthropicRoute(t *testing.T) {
	la := copilotLaunchAssembled(t, "zai", func(o *Options) { o.ProfileName = "zai" })

	got := la.channelEnv(t,
		"COPILOT_PROVIDER_BASE_URL", "COPILOT_PROVIDER_TYPE", "COPILOT_PROVIDER_WIRE_API",
		"COPILOT_MODEL", "COPILOT_PROVIDER_API_KEY")
	want := []string{
		"COPILOT_MODEL=zai/glm-5.3",
		"COPILOT_PROVIDER_API_KEY=tok-9",
		"COPILOT_PROVIDER_BASE_URL=https://api.z.ai/api/anthropic",
		"COPILOT_PROVIDER_TYPE=anthropic",
	}
	if len(got) != len(want) {
		t.Fatalf("copilot BYOK env = %q, want %q (no WIRE_API on the anthropic type)",
			got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("copilot BYOK env %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestCopilotByokComposesNothingWithoutTheProfile: presence is not selection — the pack
// selected and the key hydrated still composes no COPILOT_* var, because arming BYOK
// unbidden would switch a GitHub-auth copilot to a custom provider under nobody's feet.
func TestCopilotByokComposesNothingWithoutTheProfile(t *testing.T) {
	argv := copilotLaunch(t, "cerebras", nil)
	if got := envArgValues(argv,
		"COPILOT_PROVIDER_BASE_URL", "COPILOT_PROVIDER_TYPE", "COPILOT_PROVIDER_WIRE_API",
		"COPILOT_MODEL", "COPILOT_PROVIDER_API_KEY"); len(got) != 0 {
		t.Errorf("unprofiled launch carried copilot BYOK env: %q", got)
	}
}

// TestCopilotByokComposesNothingForAnEndpointlessProvider: bedrock names no endpoint at
// all (region facts only), and copilot's `azure` type is Azure OpenAI's deployment URL
// shape, not a bedrock address — so the derive must leave BYOK un-armed rather than
// guess. Runs against the real bedrock provider packs/bedrock ships, in a launch with no wire
// bridge (the set is not closed over claude's needs): with the bridge, which carries copilot on
// `-p bedrock` (wire-bridge-gateway.md WG-I44), copilot is armed at the bridge's adapter instead,
// as TestPlainBedrockCarriesTheClientlessAgentsThroughTheBridge pins.
func TestCopilotByokComposesNothingForAnEndpointlessProvider(t *testing.T) {
	packs := []*packload.Pack{officialPack(t, "copilot"), officialPack(t, "claude"), officialPack(t, "bedrock")}
	la := zaiLaunchAssembled(t, packs, bareConfig(), emptyEnv(),
		func(o *Options) { o.ProfileName = "bedrock" })

	if got := la.channelEnv(t,
		"COPILOT_PROVIDER_BASE_URL", "COPILOT_PROVIDER_TYPE", "COPILOT_PROVIDER_WIRE_API",
		"COPILOT_MODEL", "COPILOT_PROVIDER_API_KEY"); len(got) != 0 {
		t.Errorf("an endpointless provider armed copilot BYOK: %q", got)
	}
}

// TestCopilotByokHandlesLocalProviderAndContext pins that copilot BYOK accepts
// shorthand base_url, defaults API key to local for local endpoints, and passes
// context_window to COPILOT_PROVIDER_MAX_PROMPT_TOKENS.
func TestCopilotByokHandlesLocalProviderAndContext(t *testing.T) {
	provs := jsonx.NewOrderedMap()
	localProv := jsonx.NewOrderedMap()
	localProv.Set("base_url", "http://host.containers.internal:8080/v1")
	localProv.Set("models", map[string]any{"default": "qwen3.8-27b"})
	opts := jsonx.NewOrderedMap()
	opts.Set("context_window", "180224")
	localProv.Set("options", opts)
	provs.Set("local", localProv)

	profiles := jsonx.NewOrderedMap()
	profiles.Set("copilot", "local")

	cfg := newConfig(
		"profile", profiles,
		"providers", provs,
	)

	packs := []*packload.Pack{officialPack(t, "copilot")}
	la := zaiLaunchAssembled(t, packs, cfg, emptyEnv(), func(o *Options) {
		o.ProfileName = "local"
		writeProfilesAtHome(t, `{"local": {"provider": "local"}}`)
	})

	got := la.channelEnv(t,
		"COPILOT_PROVIDER_BASE_URL", "COPILOT_PROVIDER_TYPE", "COPILOT_PROVIDER_WIRE_API",
		"COPILOT_MODEL", "COPILOT_PROVIDER_API_KEY", "COPILOT_PROVIDER_MAX_PROMPT_TOKENS")
	want := []string{
		"COPILOT_MODEL=local/qwen3.8-27b",
		"COPILOT_PROVIDER_API_KEY=local",
		"COPILOT_PROVIDER_BASE_URL=http://host.containers.internal:8080/v1",
		"COPILOT_PROVIDER_MAX_PROMPT_TOKENS=180224",
		"COPILOT_PROVIDER_TYPE=openai",
		"COPILOT_PROVIDER_WIRE_API=completions",
	}
	if len(got) != len(want) {
		t.Fatalf("copilot local BYOK env = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("copilot local BYOK env %d = %q, want %q", i, got[i], want[i])
		}
	}
	// The user's own one-entry list is a list too, so the file carries it, with the openai
	// type's wire API, the local dummy key and the context window on its row.
	doc, _ := copilotProvidersFile(t, la)
	wantDoc := map[string]any{
		"providers": []any{map[string]any{"name": "local", "type": "openai", "wireApi": "completions",
			"baseUrl": "http://host.containers.internal:8080/v1", "apiKey": "local"}},
		"models": []any{map[string]any{"id": "qwen3.8-27b", "provider": "local", "wireModel": "qwen3.8-27b",
			"maxPromptTokens": float64(180224)}},
	}
	if !reflect.DeepEqual(doc, wantDoc) {
		t.Errorf("providers.json = %v, want %v", doc, wantDoc)
	}
}
