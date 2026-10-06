package entrypoint

// opencodecodex_test.go pins opencode on the ChatGPT subscription (yolo's `openai-codex`
// provider) through its OWN client: opencode's built-in `openai` provider, which its built-in
// ChatGPT login puts on the subscription (opencode 1.18.34, read from the installed binary and
// upstream source, never run). Three halves, each through its production call site:
//
//   - the boot render (ConfigurePackSurfaces over the real embedded packs): `provider.openai`
//     carries the one list (docs/design/model-lists-and-pickers.md ML-D1) and no `npm`, `baseURL`
//     or yolo row for openai-codex (docs/design/pi-codex-provider-shadowing.md OQ-2), and the
//     selection names `openai/<id>`;
//   - the launch env (packload.ScopeCredentials, the gate a launch composes): the OpenAI login
//     prelaunch, keyed on the provider;
//   - the shipped plugin under node, with a fake `yolo` on PATH: it serves each request with the
//     broker's access token and never touches a login that is not yolo's.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// renderOpencodeCodex renders opencode over the table its own launch composes, opencode's needs
// closure plus extra, with the profiles that table declares resolved, for the given
// YOLO_USE_PROFILES. wire, when not "", replaces the resolved profile table.
func renderOpencodeCodex(t *testing.T, use, wire string, extra ...string) map[string]any {
	t.Helper()
	packs := testPacksForAgent(t, "opencode", extra...)
	providers, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles(packs, nil, providers)
	if err != nil {
		t.Fatal(err)
	}
	if wire == "" {
		wire = mustCompactJSON(t, packload.ProfilesWireTable(resolved))
	}
	r := newPioencodeRender(t, mustCompactJSON(t, providers))
	r.wireProfiles(wire)
	r.render(t, use)
	return r.ocConfig(t)
}

// ON THE codex PROFILE opencode RUNS ITS OWN CLIENT: the row is opencode's built-in `openai` with
// no `npm` and no `baseURL` (both would replace that client and its ChatGPT handling), the list
// rides it as rows, the start model is the list's default, and the filter names `openai` alone.
// No yolo row exists for openai-codex at all: that row was the shadow OQ-2 forbids.
func TestOpencodeOnCodexRendersItsOwnOpenAIProvider(t *testing.T) {
	cfg := renderOpencodeCodex(t, `{"opencode":"codex"}`, "")
	if cfg["model"] != "openai/gpt-6.1-sol" || cfg["small_model"] != "openai/gpt-6.1-sol" {
		t.Errorf("model = %v, small_model = %v, want openai/gpt-6.1-sol", cfg["model"], cfg["small_model"])
	}
	if got := strs(cfg["enabled_providers"]); !reflect.DeepEqual(got, []string{"openai"}) {
		t.Errorf("enabled_providers = %v, want [openai]", got)
	}
	rows := ocRows(t, cfg)
	if row, present := rows["openai-codex"]; present {
		t.Errorf("opencode got a yolo row for openai-codex, shadowing its own client: %v", row)
	}
	row, _ := rows["openai"].(map[string]any)
	if row == nil {
		t.Fatalf("no openai row: %v", rows)
	}
	if npm, set := row["npm"]; set {
		t.Errorf("the openai row names npm %v, which replaces opencode's own OpenAI client", npm)
	}
	opts, _ := row["options"].(map[string]any)
	if url, set := opts["baseURL"]; set {
		t.Errorf("the openai row re-points opencode at %v", url)
	}
	// The non-key: an ambient OPENAI_API_KEY must never carry a subscription request to the
	// platform API when no login is stored.
	if key, _ := opts["apiKey"].(string); key == "" || strings.HasPrefix(key, "{env:") || strings.HasPrefix(key, "sk-") {
		t.Errorf("the openai row's apiKey = %q, want yolo's literal non-key", key)
	}
	models, _ := row["models"].(map[string]any)
	sol, _ := models["gpt-6.1-sol"].(map[string]any)
	long, _ := models["gpt-6.1-sol[1m]"].(map[string]any)
	if sol["name"] != "GPT-6.1 Sol" || !reflect.DeepEqual(sol["limit"],
		map[string]any{"context": float64(272000), "input": float64(272000), "output": float64(0)}) {
		t.Errorf("gpt-6.1-sol row = %v, want its name and the declared 272,000-token window", sol)
	}
	if long["id"] != "gpt-6.1-sol" || long["name"] != "GPT-6.1 Sol (1M context)" ||
		!reflect.DeepEqual(long["limit"], map[string]any{"context": float64(1000000), "input": float64(1000000), "output": float64(0)}) {
		t.Errorf("gpt-6.1-sol[1m] row = %v, want the base id on the wire and the 1M window", long)
	}
	want := []string{"gpt-6-astra", "gpt-6-astra[1m]", "gpt-6-luna", "gpt-6-luna[1m]", "gpt-6.1-sol", "gpt-6.1-sol[1m]"}
	if got := strs(row["whitelist"]); !reflect.DeepEqual(got, want) {
		t.Errorf("whitelist = %v, want exactly the list %v", got, want)
	}
}

// THE SWITCH OFF RENDERS NO WHITELIST (MM-D5): the rows stay, so the list's names and windows
// still reach opencode's menu, but nothing narrows it or refuses another model.
func TestOpencodeOnCodexWithEnforcementOffWritesNoWhitelist(t *testing.T) {
	cfg := renderOpencodeCodex(t, `{"opencode":"codex"}`,
		`{"codex":{"provider":"openai-codex","_enforce_models":"false"}}`)
	row, _ := ocRows(t, cfg)["openai"].(map[string]any)
	if w, set := row["whitelist"]; set {
		t.Errorf("with enforce_models false the openai row still whitelists %v", w)
	}
	if models, _ := row["models"].(map[string]any); len(models) != 6 {
		t.Errorf("the list's rows = %v, want all six", models)
	}
}

// A LATER ENTRY ON THE SUBSCRIPTION (AP-P1): opencode on [zai, codex] starts on zai, enables both
// zai's provider and its own `openai`, and carries the subscription's row, so a switch mid-session
// reaches the subscription through opencode's own client. zai's plan is opencode's own
// zai-coding-plan, which gets no row (docs/design/pi-codex-provider-shadowing.md OQ-3).
func TestOpencodeOnASetWithCodexSecondKeepsTheSubscription(t *testing.T) {
	cfg := renderOpencodeCodex(t, `{"opencode":["zai","codex"]}`, "", "zai")
	if got := strs(cfg["enabled_providers"]); !reflect.DeepEqual(got, []string{"zai-coding-plan", "openai"}) {
		t.Errorf("enabled_providers = %v, want [zai-coding-plan openai]", got)
	}
	if m, _ := cfg["model"].(string); !strings.HasPrefix(m, "zai-coding-plan/") {
		t.Errorf("model = %v, want the primary's, on opencode's own zai-coding-plan", cfg["model"])
	}
	rows := ocRows(t, cfg)
	if rows["zai"] != nil || rows["zai-coding-plan"] != nil || rows["openai"] == nil {
		t.Errorf("rows = %v, want the subscription's alone", rows)
	}
}

// THE LOGIN PRELAUNCH IS KEYED ON THE PROVIDER (OQ-BR8, PP-D2): opencode is launched with the
// view flag and the file opencode's Auth store reads whenever openai-codex is selected for it,
// and with neither on another provider.
func TestOpencodeIsLaunchedWithTheOpenAILoginOnlyOnTheSubscription(t *testing.T) {
	packs := testPacksForAgent(t, "opencode", "zai")
	env, err := launchEnvOf(packs, map[string]string{"opencode": "codex"}, "opencode")
	if err != nil {
		t.Fatalf("opencode on codex: %v", err)
	}
	if env["YOLO_AUTH_PRELAUNCH_OPENCODE_FLAG"] != "--opencode-auth" ||
		env["YOLO_AUTH_PRELAUNCH_OPENCODE_PATH"] != ".local/share/opencode/auth.json" {
		t.Errorf("opencode on codex is launched with %v, want the --opencode-auth prelaunch", env)
	}
	env, err = launchEnvOf(packs, map[string]string{"opencode": "zai"}, "opencode")
	if err != nil {
		t.Fatalf("opencode on zai: %v", err)
	}
	for k, v := range env {
		if strings.HasPrefix(k, "YOLO_AUTH_PRELAUNCH_") {
			t.Errorf("opencode on zai is launched with %s=%q", k, v)
		}
	}
}

// A RESPONSES-ONLY PROVIDER RIDES opencode's RESPONSES SDK: packs/opencode declares
// `openai-responses`, so the protocol gate admits such a provider, and the row must not put it on
// the chat-completions SDK, which has no Responses model. A provider offering both keeps chat.
func TestOpencodeCatalogsAResponsesOnlyProviderOnItsResponsesSDK(t *testing.T) {
	const tables = `{
  "resp":{"api_key_env_name":"RESP_KEY","models":{"default":"m1","m1":"m1"},
    "endpoints":{"openai-responses":{"base_url":"https://resp.example/v1"}}},
  "both":{"api_key_env_name":"BOTH_KEY","models":{"default":"m2","m2":"m2"},
    "endpoints":{"openai":{"base_url":"https://chat.both.example/v1"},
                 "openai-responses":{"base_url":"https://resp.both.example/v1"}}}}`
	r := newPioencodeRender(t, tables)
	r.wireProfiles(`{"resp":{"provider":"resp"},"both":{"provider":"both"}}`)
	r.render(t, `{"opencode":"resp"}`)
	cfg := r.ocConfig(t)
	rows := ocRows(t, cfg)
	resp, _ := rows["resp"].(map[string]any)
	opts, _ := resp["options"].(map[string]any)
	if resp["npm"] != "@ai-sdk/openai" || opts["baseURL"] != "https://resp.example/v1" || opts["apiKey"] != "{env:RESP_KEY}" {
		t.Errorf("resp row = %v, want @ai-sdk/openai at its Responses endpoint with its key", resp)
	}
	if cfg["model"] != "resp/m1" {
		t.Errorf("model = %v, want resp/m1", cfg["model"])
	}
	both, _ := rows["both"].(map[string]any)
	bothOpts, _ := both["options"].(map[string]any)
	if both["npm"] != "@ai-sdk/openai-compatible" || bothOpts["baseURL"] != "https://chat.both.example/v1" {
		t.Errorf("both row = %v, want its chat-completions endpoint on the compatible SDK", both)
	}
}

// shippedOpencodePack is the embedded opencode pack, materialized.
func shippedOpencodePack(t *testing.T) *packload.Pack {
	t.Helper()
	return mustEmbeddedPack(t, "opencode")
}

// THE PLUGIN REACHES opencode's SERVER PLUGIN DIRECTORY, and the pack needs openai-auth, the pack
// declaring the provider and its one list, unconditionally (ML-D1's consumer rule).
func TestShippedOpencodePackDeliversItsOpenAIAuthPlugin(t *testing.T) {
	p := shippedOpencodePack(t)
	needs := 0
	for _, n := range p.Decl.DeclaredNeeds() {
		if n.Pack == "openai-auth" && len(n.WhenBins) == 0 {
			needs++
		}
	}
	if needs != 1 {
		t.Errorf("opencode needs = %v, want one unconditional openai-auth need", p.Decl.DeclaredNeeds())
	}
	found := false
	for _, c := range p.Decl.Contributions() {
		if c.Kind == packdecl.KindFiles && c.From == "plugins/yolo-openai-auth.js" &&
			c.Into == ".config/opencode/plugins/yolo-openai-auth.js" {
			found = true
		}
	}
	if !found {
		t.Fatal("the opencode pack does not deliver yolo's OpenAI plugin to opencode's plugin directory")
	}
	home := t.TempDir()
	if _, err := RenderHostFiles(p, home, filesReq(t), false); err != nil {
		t.Fatalf("rendering the shipped opencode plugin: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "plugins", "yolo-openai-auth.js")); err != nil {
		t.Fatalf("opencode cannot discover the rendered plugin: %v", err)
	}
}

// opencodePluginRun runs the shipped plugin under node with harness as the script body, a fake
// `yolo` answering status and token from fakeYolo, HOME as home, and env added. It returns the
// combined output and the broker calls the fake recorded.
func opencodePluginRun(t *testing.T, home, fakeYolo, harness string, env ...string) (string, string) {
	t.Helper()
	source, err := os.ReadFile(filepath.Join(shippedOpencodePack(t).Root, "plugins", "yolo-openai-auth.js"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plugin.mjs"), source, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "yolo"), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$CALLS\"\n"+fakeYolo), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "harness.mjs"), []byte(`import plugin from "./plugin.mjs";
`+harness), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(dir, "calls")
	cmd := exec.Command("node", "harness.mjs")
	cmd.Dir = dir
	// The environment opencode would give the plugin, minus anything of this jail's that names a
	// route or an auth store: a test plugin must read only the fixture's.
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "PATH", "HOME", "XDG_DATA_HOME", "OPENCODE_AUTH_CONTENT", "YOLO_VERSION",
			"YOLO_SERVICE_OPENAI_AUTH_BROKER_ENDPOINT", "YOLO_OPENAI_AUTH_HOST_SOCKET":
			continue
		}
		cmd.Env = append(cmd.Env, kv)
	}
	cmd.Env = append(cmd.Env, "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "HOME="+home, "CALLS="+calls)
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running the shipped opencode plugin: %v\n%s", err, out)
	}
	got, _ := os.ReadFile(calls)
	return string(out), string(got)
}

// writeOpencodeAuth writes an auth.json for opencode's Auth store under home.
func writeOpencodeAuth(t *testing.T, home string, entries map[string]any) {
	t.Helper()
	path := filepath.Join(home, ".local", "share", "opencode", "auth.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

const fakeLoggedInYolo = `case "$3" in
  status) printf '{"logged_in":true,"login_required":false}\n' ;;
  login) printf 'unexpected browser login\n' >&2; exit 9 ;;
  token) printf '{"access_token":"access-%s","refresh_token":"must-not-escape","expires_at":4102444800000,"account_id":"acct-1","generation":4}\n' "$(wc -l < "$CALLS" | tr -d ' ')" ;;
esac
`

// ON YOLO'S CREDENTIAL THE PLUGIN SERVES EVERY REQUEST FROM THE BROKER: its fetch replaces
// opencode's own one, drops the request's Authorization (an ambient key included) and opencode's
// internal title header, sends the broker's access token and the account to the subscription's
// Responses endpoint, and asks the broker again only near the token's expiry. It never asks for
// a refresh and never sees the refresh token.
func TestOpencodeOpenAIAuthPluginServesTheBrokersToken(t *testing.T) {
	home := t.TempDir()
	writeOpencodeAuth(t, home, map[string]any{
		"openai":    map[string]any{"type": "oauth", "refresh": "yolo-broker:4", "access": "stale", "expires": 1},
		"anthropic": map[string]any{"type": "api", "key": "keep"},
	})
	out, calls := opencodePluginRun(t, home, fakeLoggedInYolo, `
const hooks = await plugin.server({});
if (plugin.id !== "yolo-openai-auth") throw new Error("id: " + plugin.id);
if (hooks.auth?.provider !== "openai") throw new Error("no openai auth hook: " + JSON.stringify(hooks));
const labels = hooks.auth.methods.map((m) => m.type + ":" + m.label);
if (labels.join("|") !== "oauth:ChatGPT Plus/Pro (yolo shared login)|api:Manually enter API Key") throw new Error("methods: " + labels);
const options = await hooks.auth.loader(async () => ({ type: "oauth", refresh: "yolo-broker:4", access: "stale", expires: 1 }));
if (options.apiKey !== "opencode-oauth-dummy-key" || typeof options.fetch !== "function") throw new Error("loader: " + JSON.stringify(options));
const sent = [];
globalThis.fetch = async (url, init) => { sent.push({ url: String(url), headers: Object.fromEntries(init.headers.entries()), body: init.body }); return new Response("{}"); };
for (let i = 0; i < 2; i++) {
	await options.fetch("https://api.openai.com/v1/responses", { method: "POST", body: "{}",
		headers: { authorization: "Bearer sk-ambient", "x-opencode-title": "true", originator: "opencode" } });
}
console.log(JSON.stringify(sent));
`)
	var sent []struct {
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
		Body    string            `json:"body"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &sent); err != nil {
		t.Fatalf("harness output %q: %v", out, err)
	}
	if len(sent) != 2 {
		t.Fatalf("requests = %v, want 2", sent)
	}
	for _, r := range sent {
		if r.URL != "https://chatgpt.com/backend-api/codex/responses" {
			t.Errorf("request went to %s, want the subscription's Responses endpoint", r.URL)
		}
		if r.Headers["authorization"] != "Bearer access-1" || r.Headers["chatgpt-account-id"] != "acct-1" {
			t.Errorf("request headers = %v, want the broker's token and account", r.Headers)
		}
		if _, leaked := r.Headers["x-opencode-title"]; leaked {
			t.Errorf("opencode's internal title header left with the request: %v", r.Headers)
		}
		if r.Headers["originator"] != "opencode" || r.Body != "{}" {
			t.Errorf("the request lost opencode's own header or body: %v", r)
		}
	}
	if strings.Contains(out, "must-not-escape") {
		t.Errorf("the refresh token reached the plugin's output: %s", out)
	}
	if calls != "internal openai-auth-client token\n" {
		t.Errorf("broker calls = %q, want one token call for two requests", calls)
	}
}

// THE PLUGIN NEVER TAKES OVER A LOGIN THAT IS NOT YOLO'S: with no credential, or the user's own
// ChatGPT login (a real refresh token), and no `yolo host` socket, it registers nothing, so
// opencode's own OpenAI login, methods and fetch are exactly opencode's. Handed the host socket
// (`yolo host -p codex -- opencode`), it offers the shared login, and its loader still leaves the
// user's own credential to opencode.
func TestOpencodeOpenAIAuthPluginLeavesOpencodesOwnLoginAlone(t *testing.T) {
	for _, tc := range []struct {
		name  string
		entry map[string]any
	}{
		{"no credential", nil},
		{"the user's own ChatGPT login", map[string]any{"type": "oauth", "refresh": "rt-users-own", "access": "a", "expires": 1}},
		{"an API key", map[string]any{"type": "api", "key": "sk-users-own"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.entry != nil {
				writeOpencodeAuth(t, home, map[string]any{"openai": tc.entry})
			}
			out, calls := opencodePluginRun(t, home, fakeLoggedInYolo, `
const hooks = await plugin.server({});
console.log(JSON.stringify(Object.keys(hooks)));
`)
			if strings.TrimSpace(out) != "[]" || calls != "" {
				t.Errorf("hooks = %s, broker calls = %q, want none at all", out, calls)
			}
		})
	}
	home := t.TempDir()
	out, calls := opencodePluginRun(t, home, fakeLoggedInYolo, `
const hooks = await plugin.server({});
if (hooks.auth?.provider !== "openai") throw new Error("the host route registered no shared login");
const own = await hooks.auth.loader(async () => ({ type: "oauth", refresh: "rt-users-own", access: "a", expires: 1 }));
console.log(JSON.stringify(own));
`, "YOLO_OPENAI_AUTH_HOST_SOCKET=/tmp/broker.host")
	if strings.TrimSpace(out) != "{}" || calls != "" {
		t.Errorf("the loader on the user's own login returned %s with broker calls %q, want {} and none", out, calls)
	}
}

// THE SHARED LOGIN IN /connect stores yolo's view: the access token and the generation marker,
// never the refresh token. Already signed in, it opens no browser; signed out, it runs the broker's
// own login and shows the URL the broker prints.
func TestOpencodeOpenAIAuthPluginLoginStoresTheBrokersView(t *testing.T) {
	const loginHarness = `
const hooks = await plugin.server({});
const method = hooks.auth.methods[0];
const authorization = await method.authorize();
const result = await authorization.callback();
console.log(JSON.stringify({ url: authorization.url, method: authorization.method, result }));
`
	for _, tc := range []struct {
		name, yolo, url, calls string
	}{
		{"signed in", fakeLoggedInYolo, "",
			"internal openai-auth-client status\ninternal openai-auth-client token\n"},
		{"signed out", `case "$3" in
  status) printf '{"logged_in":false}\n' ;;
  login) printf 'Open this URL to authenticate:\nhttps://auth.example.test/authorize?x=1\n' >&2; printf '{"ok":true}\n' ;;
  token) printf '{"access_token":"access-new","expires_at":4102444800000,"generation":5}\n' ;;
esac
`, "https://auth.example.test/authorize?x=1",
			"internal openai-auth-client status\ninternal openai-auth-client login\ninternal openai-auth-client token\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, calls := opencodePluginRun(t, t.TempDir(), tc.yolo, loginHarness,
				"YOLO_OPENAI_AUTH_HOST_SOCKET=/tmp/broker.host")
			var got struct {
				URL    string         `json:"url"`
				Method string         `json:"method"`
				Result map[string]any `json:"result"`
			}
			if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
				t.Fatalf("harness output %q: %v", out, err)
			}
			if got.URL != tc.url || got.Method != "auto" {
				t.Errorf("authorization url = %q method = %q, want %q and auto", got.URL, got.Method, tc.url)
			}
			refresh, _ := got.Result["refresh"].(string)
			if got.Result["type"] != "success" || !strings.HasPrefix(refresh, "yolo-broker:") ||
				strings.Contains(out, "must-not-escape") {
				t.Errorf("stored view = %v, want success with the generation marker as its refresh", got.Result)
			}
			if calls != tc.calls {
				t.Errorf("broker calls = %q, want %q", calls, tc.calls)
			}
		})
	}
}

// THE LOGIN PRELAUNCH FOLLOWS A LATER ENTRY OF THE SET (AP-P1, pi's rule): opencode on [zai, codex]
// starts on zai and can switch to the subscription mid-session, so its launcher stores the shared
// login's view as it does for codex alone. Without it the switch would reach opencode's `openai`
// row with no stored login, on the derive's non-key.
func TestOpencodeOnASetWithCodexSecondIsLaunchedWithTheOpenAILogin(t *testing.T) {
	packs := testPacksForAgent(t, "opencode", "zai")
	providers, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles(packs, nil, providers)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := packload.ScopeCredentials(packload.ScopeInput{Packs: packs, Providers: providers,
		Profiles: map[string]string{"opencode": "zai"}, Sets: map[string][]string{"opencode": {"zai", "codex"}},
		Resolved: resolved})
	if err != nil {
		t.Fatalf("opencode on [zai, codex]: %v", err)
	}
	if got, _ := scope.DeliveredTo("opencode", "YOLO_AUTH_PRELAUNCH_OPENCODE_FLAG"); got != "--opencode-auth" {
		t.Errorf("opencode on [zai, codex] carries prelaunch flag %q, want --opencode-auth", got)
	}
}
