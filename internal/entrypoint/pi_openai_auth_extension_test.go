package entrypoint

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/openauthclient"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

func shippedPiPack(t *testing.T) *packload.Pack {
	t.Helper()
	loaded, problems := packload.MaterializeEmbedded(officialpacks.FS, t.TempDir())
	if len(problems) > 0 {
		t.Fatalf("materializing embedded packs: %v", problems)
	}
	for _, p := range loaded {
		if p.Name == "pi" {
			return p
		}
	}
	t.Fatal("the pi pack is not embedded")
	return nil
}

// The adapter has to reach Pi through its real extension discovery directory. A source
// file that exists in the pack but has no files contribution is dead documentation, so
// this assertion starts from the shipped declaration that the renderer consumes.
func TestShippedPiPackDeliversOpenAIAuthExtension(t *testing.T) {
	p := shippedPiPack(t)
	needs := p.Decl.DeclaredNeeds()
	openaiAuth := 0
	for _, n := range needs {
		if n.Pack == "openai-auth" && len(n.WhenBins) == 0 {
			openaiAuth++
		}
	}
	if openaiAuth != 1 {
		t.Fatalf("pi needs = %v, want one unconditional openai-auth need", needs)
	}
	var found bool
	for _, c := range p.Decl.Contributions() {
		if c.Kind == packdecl.KindFiles && c.From == "extensions/yolo-openai-auth.js" &&
			c.Into == ".pi/agent/extensions/yolo-openai-auth.js" {
			found = true
		}
	}
	if !found {
		t.Fatal("pi pack does not deliver the yolo OpenAI adapter to Pi's extension directory")
	}
	if _, err := os.Stat(filepath.Join(p.Root, "extensions", "yolo-openai-auth.js")); err != nil {
		t.Fatalf("declared Pi OpenAI extension is absent: %v", err)
	}
	home := t.TempDir()
	if _, err := RenderHostFiles(p, home, filesReq(t), false); err != nil {
		t.Fatalf("rendering the shipped Pi extension: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".pi", "agent", "extensions", "yolo-openai-auth.js")); err != nil {
		t.Fatalf("Pi cannot discover the rendered extension: %v", err)
	}
}

// piRoute is which registration the shipped extension makes: the jail route's ProviderConfig,
// `registerProvider("openai-codex", config)`, or the host route's native provider,
// `registerProvider(provider)`, which it makes only where `yolo host` set the broker's socket
// (docs/design/pi-host-openai-auth.md OQ-1, D2).
type piRoute struct {
	name   string
	socket bool
}

var (
	piJailRoute = piRoute{"jail route", false}
	piHostRoute = piRoute{"host route", true}
	piRoutes    = []piRoute{piJailRoute, piHostRoute}
)

// env is the route's half of a harness's environment. The socket variable is set to a path no
// broker serves, or set EMPTY, never inherited: a developer running the suite under `yolo host`
// carries a live one, which would decide the case. PI_WANT_NATIVE is what requireRoute in
// piRegistrationViewJS expects; a later entry overrides it for a case that must fall back.
func (r piRoute) env() []string {
	if r.socket {
		return []string{openauthclient.HostSocketEnv + "=/nonexistent/yolo-openai-auth.sock", "PI_WANT_NATIVE=1"}
	}
	return []string{openauthclient.HostSocketEnv + "=", "PI_WANT_NATIVE=0"}
}

// piRegistrationViewJS reads either registration into one view, so a harness asserts the login,
// the list and the refusal in one spelling on both routes. requireRoute fails a run whose route
// is not the one the Go side asked for.
const piRegistrationViewJS = `
function registrationView(args) {
	const [first, config] = args;
	if (typeof first === "string") {
		const oauth = config?.oauth ?? {};
		return {
			native: false, id: first, name: config?.name, baseUrl: config?.baseUrl, apis: [config?.api],
			models: config?.models, streamSimple: config?.streamSimple, config,
			hasLogin: typeof oauth.login === "function" && typeof oauth.refreshToken === "function" &&
				typeof oauth.getApiKey === "function",
			login: (interaction) => oauth.login(interaction ?? {}),
			refresh: (credential, signal) => oauth.refreshToken(credential, signal),
			apiKeyOf: async (credential) => oauth.getApiKey(credential),
		};
	}
	const oauth = first?.auth?.oauth ?? {};
	const models = typeof first?.getModels === "function" ? first.getModels() : undefined;
	return {
		native: true, id: first?.id, name: first?.name, baseUrl: first?.baseUrl,
		apis: [...new Set((models ?? []).map((m) => m.api))], models, streamSimple: first?.streamSimple, provider: first,
		hasLogin: typeof oauth.login === "function" && typeof oauth.refresh === "function" &&
			typeof oauth.toAuth === "function",
		login: (interaction) => oauth.login(interaction ?? {}),
		refresh: (credential, signal) => oauth.refresh(credential, signal),
		apiKeyOf: async (credential) => (await oauth.toAuth(credential)).apiKey,
	};
}
function requireRoute(view) {
	const want = process.env.PI_WANT_NATIVE === "1";
	if (view.native !== want) {
		throw new Error("the extension registered " + (view.native ? "a native provider" : "a ProviderConfig") +
			" with " + (process.env.YOLO_OPENAI_AUTH_HOST_SOCKET ? "the host socket set" : "no host socket"));
	}
}
`

// Pi's own lock is scoped to one workspace auth.json. The adapter must therefore ask the
// machine broker on every login and refresh, and must never put its canonical refresh token
// in that workspace file. A broker that is already authenticated must not start another
// browser flow. This executes the shipped extension with a fake yolo client, on both routes:
// the host route's native provider carries the same login.
func TestPiOpenAIAuthExtensionReusesBrokerLoginAndRefreshes(t *testing.T) {
	for _, route := range piRoutes {
		t.Run(route.name, func(t *testing.T) {
			f := newPiExtensionFixture(t)
			f.builtinStub(t)
			// ⚠ `wc -l | tr -d ' '`, AND THE `tr` IS THE WHOLE REASON THIS TEST PASSES ON A MAC.
			// BSD `wc` right-pads its count to 8 columns; GNU `wc` does not. So the call counter
			// below interpolated as `access-       2` on darwin and `access-2` on Linux, and the
			// harness's `first.access !== "access-2"` failed with "broker calls were not sequenced"
			// — a message that points at sequencing when the fault is whitespace. Measured on macOS
			// 26.5; it reddened `check-macos` while `check-go` stayed green, which is what made it
			// read as a macOS behaviour difference in the adapter rather than in the fixture.
			//
			// The sibling site in openaiauth_prelaunch_test.go does NOT need this: it feeds the
			// count to `[ … -gt 1 ]`, and POSIX integer comparison tolerates leading blanks. The
			// workflows that count test lines already carry the same `tr` for the same reason.
			f.fakeClient(t, `
case "$3" in
  status) printf '{"logged_in":true,"login_required":false}\n' ;;
  login) printf 'unexpected browser login\n' >&2; exit 9 ;;
  token) printf '{"access_token":"access-%s","refresh_token":"must-not-escape","expires_at":4102444800000,"account_id":"acct-1","generation":4}\n' "$(wc -l < "$CALLS" | tr -d ' ')" ;;
esac
`)
			output := f.output(t, route, piRegistrationViewJS+`
import extension from "./extension.mjs";
const registrations = [];
await extension({ registerProvider(...args) { registrations.push(args); } });
if (registrations.length !== 1) throw new Error("registrations: " + registrations.length);
const view = registrationView(registrations[0]);
requireRoute(view);
if (view.id !== "openai-codex") throw new Error("wrong provider: " + view.id);
const first = await view.login({});
const second = await view.refresh(first, new AbortController().signal);
if (first.refresh !== "yolo-broker:4" || second.refresh !== "yolo-broker:4") throw new Error("refresh secret escaped");
if (first.access !== "access-2" || second.access !== "access-3") throw new Error("broker calls were not sequenced");
if (first.expires !== 4102444800000 || second.accountId !== "acct-1") throw new Error("view shape lost");
if (await view.apiKeyOf(second) !== "access-3") throw new Error("access token not resolved");
`)
			if strings.Contains(string(output), "unexpected browser login") {
				t.Fatalf("an existing broker login started a browser flow: %q", output)
			}
			want := "internal openai-auth-client status\ninternal openai-auth-client token\ninternal openai-auth-client token\n"
			if got := f.calls(t); got != want {
				t.Fatalf("broker calls = %q, want %q", strings.TrimSpace(got), strings.TrimSpace(want))
			}
		})
	}
}

func TestPiOpenAIAuthExtensionStartsBrowserOnlyWhenStatusRequiresLogin(t *testing.T) {
	for _, route := range piRoutes {
		t.Run(route.name, func(t *testing.T) {
			f := newPiExtensionFixture(t)
			f.builtinStub(t)
			f.fakeClient(t, `
case "$3" in
  status) printf '{"logged_in":false}\n' ;;
  login) printf 'Open this URL: https://example.test/login\n' >&2; printf '{"ok":true}\n' ;;
  token) printf '{"access_token":"access","expires_at":4102444800000,"generation":1}\n' ;;
esac
`)
			output := f.output(t, route, piRegistrationViewJS+`
import extension from "./extension.mjs";
const registrations = [];
await extension({ registerProvider(...args) { registrations.push(args); } });
const view = registrationView(registrations[0]);
requireRoute(view);
const result = await view.login({});
if (result.access !== "access" || result.refresh !== "yolo-broker:1") throw new Error("bad credentials");
`)
			if !strings.Contains(string(output), "https://example.test/login") {
				t.Fatalf("login URL was not forwarded: %q", output)
			}
			want := "internal openai-auth-client status\ninternal openai-auth-client login\ninternal openai-auth-client token\n"
			if got := f.calls(t); got != want {
				t.Fatalf("broker calls = %q, want %q", strings.TrimSpace(got), strings.TrimSpace(want))
			}
		})
	}
}

// piExtensionFixture is one directory the shipped extension runs from, with a HOME of its
// own, so the model-list file it reads is the fixture's and never the developer's.
type piExtensionFixture struct {
	dir, home string
}

func newPiExtensionFixture(t *testing.T) piExtensionFixture {
	t.Helper()
	p := shippedPiPack(t)
	source, err := os.ReadFile(filepath.Join(p.Root, "extensions", "yolo-openai-auth.js"))
	if err != nil {
		t.Fatal(err)
	}
	f := piExtensionFixture{dir: t.TempDir(), home: t.TempDir()}
	if err := os.WriteFile(filepath.Join(f.dir, "extension.mjs"), source, 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

// modelsFile writes the pi/codex-models data file at the path the shipped manifest declares
// for it; raw is written verbatim, so a malformed file is spellable.
func (f piExtensionFixture) modelsFile(t *testing.T, raw string) {
	t.Helper()
	dest := filepath.Join(f.home, filepath.FromSlash(piCodexModelsRel(t)))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

// piCatalogStubJS stands in for pi's own catalog lookup. Known ids carry a marker the extension
// can only have copied from the catalog.
const piCatalogStubJS = `
const known = {
	"gpt-6-sol": { name: "Catalog Sol", contextWindow: 272000 },
	"gpt-6-astra": { name: "Catalog Astra", contextWindow: 272000 },
};
export function getBuiltinModel(provider, id) {
	if (provider !== "openai-codex" || !known[id]) return undefined;
	return {
		id, api: "openai-codex-responses", provider, baseUrl: "https://catalog.example",
		reasoning: true, input: ["text", "image"], maxTokens: 128000,
		cost: { input: 2, output: 10, cacheRead: 0.2, cacheWrite: 2.5 },
		thinkingLevelMap: { marker: "from-catalog-" + id },
		...known[id],
	};
}
`

// piBuiltinProvidersStubJS adds pi's built-in openai-codex provider to the catalog stub: pi's own
// name, address and login, each a marker the host route must replace, its catalog's models, and
// two streams that record what they are handed.
const piBuiltinProvidersStubJS = `
export const streamed = [];
export function builtinProviders() {
	const models = () => Object.keys(known).map((id) => getBuiltinModel("openai-codex", id));
	return [{
		id: "openai-codex", name: "OpenAI Codex (legacy)", baseUrl: "https://catalog.example",
		auth: { oauth: { name: "pi's own login", isSubscription: true,
			login: async () => { throw new Error("pi's own login ran"); },
			refresh: async () => { throw new Error("pi's own refresh ran"); },
			toAuth: async () => ({ apiKey: "pi's own token" }) } },
		getModels: models, getAllModels: models, refreshModels: async () => {},
		stream: (model, context, options) => { streamed.push({ via: "stream", model: model.id, options }); return "builtin-stream"; },
		streamSimple: (model, context, options) => { streamed.push({ via: "streamSimple", model: model.id, options }); return "builtin-stream"; },
	}];
}
`

// stubPiAI installs a stand-in for pi-ai's providers/all module, resolved the way node resolves
// the extension's bare import from its directory.
func (f piExtensionFixture) stubPiAI(t *testing.T, allJS string) { writePiAIStub(t, f.dir, allJS) }

// writePiAIStub writes the stand-in for pi-ai's providers/all module into root's node_modules,
// where node resolves a bare import from any file below root.
func writePiAIStub(t *testing.T, root, allJS string) {
	t.Helper()
	pkg := filepath.Join(root, "node_modules", "@earendil-works", "pi-ai")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "package.json"), []byte(
		`{"name":"@earendil-works/pi-ai","type":"module","exports":{"./providers/all":"./all.js"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "all.js"), []byte(allJS), 0o644); err != nil {
		t.Fatal(err)
	}
}

// piNativeCoreStubJS stands in for the root of a pi whose loader takes a provider object: the
// root exports ModelRuntime, and its prototype has the registerNativeProvider that loader hands
// the object to (pi 0.81.0 on; MEASURED 2026-10-04, docs/design/pi-host-openai-auth.md PH-D4).
const piNativeCoreStubJS = "export class ModelRuntime { registerNativeProvider() {} }\n"

// piPreNativeCoreStubJS is the root of pi 0.80.10, the shape MEASURED there on 2026-10-04: a
// ModelRuntime with no registerNativeProvider, so its loader queues a provider object as a name
// and fails applying it, without throwing at the call.
const piPreNativeCoreStubJS = "export const VERSION = \"0.80.10\";\nexport class ModelRuntime {}\n"

// writePiCoreStub writes body as the stand-in for pi's own package root,
// @earendil-works/pi-coding-agent, into root's node_modules, where node resolves a bare import
// from any file below root.
func writePiCoreStub(t *testing.T, root, body string) {
	t.Helper()
	pkg := filepath.Join(root, "node_modules", "@earendil-works", "pi-coding-agent")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "package.json"), []byte(
		`{"name":"@earendil-works/pi-coding-agent","type":"module","exports":{".":"./index.js"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "index.js"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// catalogStub installs pi's catalog lookup alone, the module of a pi that exports no built-in
// provider object.
func (f piExtensionFixture) catalogStub(t *testing.T) { f.stubPiAI(t, piCatalogStubJS) }

// builtinStub installs the catalog lookup and pi's built-in openai-codex provider, under the root
// of a pi whose loader takes a provider object: the pi the host route registers natively on.
func (f piExtensionFixture) builtinStub(t *testing.T) {
	f.stubPiAI(t, piCatalogStubJS+piBuiltinProvidersStubJS)
	writePiCoreStub(t, f.dir, piNativeCoreStubJS)
}

// fakeClient installs a stand-in `yolo` whose `internal openai-auth-client <action>` runs the
// shell body with the action in $3, after logging the call to the fixture's calls file.
func (f piExtensionFixture) fakeClient(t *testing.T, body string) {
	t.Helper()
	bin := filepath.Join(f.dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "yolo"), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$CALLS\"\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// calls is what the fake client was asked, one line per call, or "" when it was never run.
func (f piExtensionFixture) calls(t *testing.T) string {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(f.dir, "calls"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(got)
}

// output executes a harness beside the extension on the route, with any env after the route's,
// fails on a non-zero exit, and returns what it printed. The fake client, when installed, is
// first on PATH.
func (f piExtensionFixture) output(t *testing.T, route piRoute, harness string, env ...string) []byte {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, "harness.mjs"), []byte(harness), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(requireNode(t, "the pi openai-auth extension"), "harness.mjs")
	cmd.Dir = f.dir
	cmd.Env = append(os.Environ(), "HOME="+f.home, "CALLS="+filepath.Join(f.dir, "calls"),
		"PATH="+filepath.Join(f.dir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Env = append(append(cmd.Env, route.env()...), env...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("executing the Pi OpenAI extension harness on the %s: %v\n%s", route.name, err, output)
	}
	return output
}

// run executes a harness beside the extension on the jail route and fails on a non-zero exit.
func (f piExtensionFixture) run(t *testing.T, harness string) {
	t.Helper()
	f.output(t, piJailRoute, harness)
}

// piRegisterHarness is the harness prefix every case shares: load the extension the way pi
// does (awaiting the factory) and capture the one provider it registers, as `view` on either
// route and as `cfg`, the ProviderConfig, on the jail route.
const piRegisterHarness = piRegistrationViewJS + `
import extension from "./extension.mjs";
const registrations = [];
let beforeProviderHandler;
await extension({
	registerProvider(...args) { registrations.push(args); },
	on(event, handler) { if (event === "before_provider_request") beforeProviderHandler = handler; },
});
if (registrations.length !== 1) throw new Error("registrations: " + registrations.length);
const view = registrationView(registrations[0]);
requireRoute(view);
const cfg = view.config;
if (view.id !== "openai-codex") throw new Error("provider not registered as openai-codex");
if (view.baseUrl !== "https://chatgpt.com/backend-api") throw new Error("address lost: " + view.baseUrl);
if (!view.native && JSON.stringify(view.apis) !== JSON.stringify(["openai-codex-responses"])) throw new Error("api lost: " + view.apis);
if (!view.hasLogin) throw new Error("oauth lost");
function eq(got, want, what) {
	if (JSON.stringify(got) !== JSON.stringify(want)) throw new Error(what + " = " + JSON.stringify(got) + ", want " + JSON.stringify(want));
}
`

// The rendered data file (the pi/codex-models surface) is the model list. Three entries
// cover the three definition paths: a base id pi's catalog knows, the [1m] variant of one
// (looked up by its `base`), and an id the catalog lacks.
const piCodexModelsFixture = `{"models":[
	{"id":"gpt-6-sol","name":"GPT-6 Sol","contextWindow":272000},
	{"id":"gpt-6-sol[1m]","base":"gpt-6-sol","name":"GPT-6 Sol (1M context)","contextWindow":1000000},
	{"id":"gpt-6-nova"}
]}`

func TestPiOpenAIAuthExtensionRegistersTheRenderedListOverPisCatalog(t *testing.T) {
	f := newPiExtensionFixture(t)
	f.modelsFile(t, piCodexModelsFixture)
	f.catalogStub(t)
	f.run(t, piRegisterHarness+`
const models = cfg.models;
eq(models.map((m) => m.id), ["gpt-6-sol", "gpt-6-sol[1m]", "gpt-6-nova"], "registered ids, in the file's order");
const [sol, sol1m, nova] = models;
eq(sol.thinkingLevelMap, { marker: "from-catalog-gpt-6-sol" }, "base id's catalog facts");
eq(sol.name, "GPT-6 Sol", "the declared name over the catalog's");
eq(sol.cost.output, 10, "base id's catalog cost");
eq(sol1m.thinkingLevelMap, { marker: "from-catalog-gpt-6-sol" }, "the [1m] variant's facts come from its BASE");
eq(sol1m.contextWindow, 1000000, "the [1m] variant's declared context window");
eq(sol1m.maxTokens, 128000, "the [1m] variant's catalog maxTokens");
for (const m of models) {
	for (const k of ["api", "provider", "baseUrl"]) {
		if (k in m) throw new Error(m.id + " carries the catalog's " + k + ": the registration must be the only address");
	}
}
eq([nova.name, nova.reasoning, nova.input, nova.contextWindow, nova.maxTokens],
	["gpt-6-nova", false, ["text"], 128000, 16384], "an id the catalog lacks gets pi's modelFromJson defaults");
eq(nova.cost, { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, "the defaults' cost");
`)
}

// With no catalog module to import (a pi without the alias, or a node run outside pi) the
// list still registers, every entry on the defaults path, and loading does not throw.
func TestPiOpenAIAuthExtensionRegistersTheListWithoutPisCatalog(t *testing.T) {
	f := newPiExtensionFixture(t)
	f.modelsFile(t, piCodexModelsFixture)
	f.run(t, piRegisterHarness+`
eq(cfg.models.map((m) => [m.id, m.maxTokens, m.reasoning]),
	[["gpt-6-sol", 16384, false], ["gpt-6-sol[1m]", 16384, false], ["gpt-6-nova", 16384, false]],
	"every entry on the defaults path");
eq(cfg.models.map((m) => m.contextWindow), [272000, 1000000, 128000], "declared windows kept");
`)
}

// A missing, malformed or empty file registers NO models of our own — the key is absent,
// so pi's applyExtension passes its built-in openai-codex catalog through — and never costs
// the login: the address and the oauth block are registered regardless.
func TestPiOpenAIAuthExtensionWithoutAListKeepsPisCatalogAndTheLogin(t *testing.T) {
	cases := map[string]string{
		"missing":   "",
		"malformed": "{not json",
		"empty":     "{}",
		"not-array": `{"models":{"gpt-6-sol":{}}}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			f := newPiExtensionFixture(t)
			f.catalogStub(t)
			if name != "missing" {
				f.modelsFile(t, raw)
			}
			f.run(t, piRegisterHarness+`
if ("models" in cfg) throw new Error("models registered from no list: " + JSON.stringify(cfg.models));
`)
		})
	}
}

// The [1m] suffix is a CLIENT spelling: the wire takes the base id, so the request hook
// strips it and leaves every other payload alone.
func TestPiOpenAIAuthExtensionStripsThe1MSuffix(t *testing.T) {
	f := newPiExtensionFixture(t)
	f.modelsFile(t, piCodexModelsFixture)
	f.run(t, piRegisterHarness+`
if (typeof beforeProviderHandler !== "function") throw new Error("before_provider_request handler not registered");
const res1 = beforeProviderHandler({ payload: { model: "gpt-6-sol[1m]", input: ["test"] } });
eq(res1, { model: "gpt-6-sol", input: ["test"] }, "stripped payload");
eq(beforeProviderHandler({ payload: { model: "gpt-6-astra", input: ["x"] } }), undefined, "a model with no suffix");
eq(beforeProviderHandler({ payload: { model: "gpt-6-luna[1m]" } }), { model: "gpt-6-luna" }, "a second base");
`)
}

// piWarningHarness loads the extension with a session_start listener captured, fires it twice
// the way pi does on /new or a resume, with a UI and without one, and prints what reached the
// user: every ctx.ui.notify call and every console.warn line.
const piWarningHarness = `
import extension from "./extension.mjs";
const handlers = [];
const seen = { notified: [], warned: [] };
console.warn = (...args) => { seen.warned.push(args.join(" ")); };
await extension({
	registerProvider() {},
	on(event, handler) { if (event === "session_start") handlers.push(handler); },
});
const withUI = { hasUI: true, ui: { notify(message, type) { seen.notified.push([type, message]); } } };
const withoutUI = { hasUI: false, ui: { notify() { throw new Error("notify called without a UI"); } } };
const ctx = process.env.PI_HAS_UI === "1" ? withUI : withoutUI;
for (let i = 0; i < 2; i++) for (const h of handlers) await h({ type: "session_start" }, ctx);
console.log(JSON.stringify(seen));
`

// runWarningHarness runs piWarningHarness in the fixture and decodes what it printed.
func (f piExtensionFixture) runWarningHarness(t *testing.T, hasUI bool) (notified [][2]string, warned []string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, "harness.mjs"), []byte(piWarningHarness), 0o644); err != nil {
		t.Fatal(err)
	}
	ui := "0"
	if hasUI {
		ui = "1"
	}
	cmd := exec.Command(requireNode(t, "the pi openai-auth extension"), "harness.mjs")
	cmd.Dir = f.dir
	cmd.Env = append(append(os.Environ(), "HOME="+f.home, "PI_HAS_UI="+ui), piJailRoute.env()...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("executing the Pi OpenAI extension warning harness: %v\n%s", err, out)
	}
	var seen struct {
		Notified [][2]string `json:"notified"`
		Warned   []string    `json:"warned"`
	}
	if err := json.Unmarshal(out, &seen); err != nil {
		t.Fatalf("decoding the harness output %q: %v", out, err)
	}
	return seen.Notified, seen.Warned
}

// A REGISTRATION THAT FELL BACK TO DEFAULTS SAYS SO. When pi's catalog cannot be imported (a pi
// that renamed the alias or the function) or lacks an id, the model registers as a text-only
// model with no thinking levels and a 16384-token output cap. That is a real loss, and pi is
// not version-pinned, so the extension tells the user once per process: through pi's own
// notification when there is a UI, on stderr when there is none, as pi's own runner does.
func TestPiOpenAIAuthExtensionWarnsOnceWhenPisCatalogCannotDescribeAModel(t *testing.T) {
	t.Run("the catalog does not import", func(t *testing.T) {
		f := newPiExtensionFixture(t)
		f.modelsFile(t, piCodexModelsFixture)
		notified, warned := f.runWarningHarness(t, true)
		if len(warned) != 0 {
			t.Errorf("stderr warnings with a UI present: %q", warned)
		}
		if len(notified) != 1 {
			t.Fatalf("notifications = %q, want exactly one across two session starts", notified)
		}
		if notified[0][0] != "warning" || !strings.Contains(notified[0][1], "catalog") ||
			!strings.Contains(notified[0][1], "16384") {
			t.Errorf("notification = %q, want a warning that pi's catalog did not load and what the models lost", notified[0])
		}
	})
	t.Run("the catalog module no longer exports the lookup", func(t *testing.T) {
		f := newPiExtensionFixture(t)
		f.modelsFile(t, piCodexModelsFixture)
		f.catalogStub(t)
		all := filepath.Join(f.dir, "node_modules", "@earendil-works", "pi-ai", "all.js")
		if err := os.WriteFile(all, []byte("export function getModel() { return undefined; }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		notified, _ := f.runWarningHarness(t, true)
		if len(notified) != 1 || !strings.Contains(notified[0][1], "getBuiltinModel") {
			t.Errorf("notifications = %q, want one naming the missing getBuiltinModel", notified)
		}
	})
	t.Run("the catalog lacks an id", func(t *testing.T) {
		f := newPiExtensionFixture(t)
		f.modelsFile(t, piCodexModelsFixture)
		f.catalogStub(t)
		notified, _ := f.runWarningHarness(t, true)
		if len(notified) != 1 {
			t.Fatalf("notifications = %q, want exactly one", notified)
		}
		msg := notified[0][1]
		if !strings.Contains(msg, "gpt-6-nova") || strings.Contains(msg, "gpt-6-sol") {
			t.Errorf("notification = %q, want it to name gpt-6-nova, the one id the catalog lacks, and no other", msg)
		}
	})
	t.Run("without a UI it goes to stderr", func(t *testing.T) {
		f := newPiExtensionFixture(t)
		f.modelsFile(t, piCodexModelsFixture)
		_, warned := f.runWarningHarness(t, false)
		if len(warned) != 1 || !strings.Contains(warned[0], "catalog") {
			t.Errorf("stderr warnings = %q, want exactly one naming pi's catalog", warned)
		}
	})
	t.Run("every id is in the catalog", func(t *testing.T) {
		f := newPiExtensionFixture(t)
		f.modelsFile(t, `{"models":[{"id":"gpt-6-sol"},{"id":"gpt-6-sol[1m]","base":"gpt-6-sol"},{"id":"gpt-6-astra"}]}`)
		f.catalogStub(t)
		if notified, warned := f.runWarningHarness(t, true); len(notified) != 0 || len(warned) != 0 {
			t.Errorf("a list pi's catalog fully describes warned: notified %q, stderr %q", notified, warned)
		}
	})
	t.Run("no list registers nothing and says nothing", func(t *testing.T) {
		f := newPiExtensionFixture(t)
		f.modelsFile(t, "{}")
		if notified, warned := f.runWarningHarness(t, true); len(notified) != 0 || len(warned) != 0 {
			t.Errorf("an empty list, which leaves pi's own catalog in place, warned: notified %q, stderr %q", notified, warned)
		}
	})
}

// piPackageStub installs a stand-in for pi's own package root, the specifier the extension
// reads VERSION from, resolved the way node resolves the extension's bare import from its
// directory. body is the module's source, so a root exporting no version is spellable.
func (f piExtensionFixture) piPackageStub(t *testing.T, body string) {
	t.Helper()
	writePiCoreStub(t, f.dir, body)
}

// Two ids the catalog stub lacks, the shape of a pi older than the GPT-6 models.
const piTwoMissingModelsFixture = `{"models":[{"id":"gpt-6-sol"},{"id":"gpt-6-luna"},{"id":"gpt-6-nova"}]}`

// A MISSING MODEL NAMES ITS LIKELY CAUSE AND THE FIX. A catalog that loaded but lacks an id is,
// in the common case, a pi older than the model, so the one warning names the running pi's
// version and `pi update`; with no version to read it still gives the remedy, hedged. A catalog
// that did not load, or one that describes every id, makes no claim about pi's age.
func TestPiOpenAIAuthExtensionNamesPisVersionAndTheRemedyWhenAModelIsMissing(t *testing.T) {
	t.Run("a readable version is named", func(t *testing.T) {
		f := newPiExtensionFixture(t)
		f.modelsFile(t, piTwoMissingModelsFixture)
		f.catalogStub(t)
		f.piPackageStub(t, `export const VERSION = "0.85.1";`+"\n")
		notified, warned := f.runWarningHarness(t, true)
		if len(notified) != 1 || len(warned) != 0 {
			t.Fatalf("notified %q, stderr %q, want exactly one notification", notified, warned)
		}
		msg := notified[0][1]
		for _, want := range []string{"gpt-6-luna, gpt-6-nova", "16384", "Your pi (0.85.1) predates these models", "`pi update` fixes it"} {
			if !strings.Contains(msg, want) {
				t.Errorf("notification = %q, want it to contain %q", msg, want)
			}
		}
	})
	t.Run("one missing model reads in the singular", func(t *testing.T) {
		f := newPiExtensionFixture(t)
		f.modelsFile(t, piCodexModelsFixture)
		f.catalogStub(t)
		f.piPackageStub(t, `export const VERSION = "0.85.1";`+"\n")
		notified, warned := f.runWarningHarness(t, false)
		if len(notified) != 0 {
			t.Fatalf("notified %q without a UI", notified)
		}
		if len(warned) != 1 || !strings.Contains(warned[0], "Your pi (0.85.1) predates this model; `pi update` fixes it.") {
			t.Errorf("stderr warnings = %q, want one naming pi 0.85.1 and the remedy in the singular", warned)
		}
	})
	unreadable := map[string]string{
		"no package root":          "",
		"a root exporting none":    "export const OTHER = 1;\n",
		"pi's unread-package mark": `export const VERSION = "0.0.0";` + "\n",
		"a non-string version":     "export const VERSION = 85;\n",
	}
	for name, body := range unreadable {
		t.Run("unreadable version: "+name, func(t *testing.T) {
			f := newPiExtensionFixture(t)
			f.modelsFile(t, piTwoMissingModelsFixture)
			f.catalogStub(t)
			if body != "" {
				f.piPackageStub(t, body)
			}
			notified, warned := f.runWarningHarness(t, true)
			if len(notified) != 1 || len(warned) != 0 {
				t.Fatalf("notified %q, stderr %q, want exactly one notification", notified, warned)
			}
			msg := notified[0][1]
			if !strings.Contains(msg, "Your pi may predate these models; `pi update` fixes it.") {
				t.Errorf("notification = %q, want the hedged remedy", msg)
			}
			if strings.Contains(msg, "Your pi (") || strings.Contains(msg, "0.0.0") {
				t.Errorf("notification = %q names a version it could not read", msg)
			}
		})
	}
	t.Run("no missing model, no warning", func(t *testing.T) {
		f := newPiExtensionFixture(t)
		f.modelsFile(t, `{"models":[{"id":"gpt-6-sol"},{"id":"gpt-6-astra"}]}`)
		f.catalogStub(t)
		f.piPackageStub(t, `export const VERSION = "0.85.1";`+"\n")
		if notified, warned := f.runWarningHarness(t, true); len(notified) != 0 || len(warned) != 0 {
			t.Errorf("a catalog describing every id warned: notified %q, stderr %q", notified, warned)
		}
	})
	t.Run("a catalog that did not load makes no age claim", func(t *testing.T) {
		f := newPiExtensionFixture(t)
		f.modelsFile(t, piTwoMissingModelsFixture)
		f.piPackageStub(t, `export const VERSION = "0.85.1";`+"\n")
		notified, _ := f.runWarningHarness(t, true)
		if len(notified) != 1 || !strings.Contains(notified[0][1], "did not load") {
			t.Fatalf("notifications = %q, want one saying the catalog did not load", notified)
		}
		if msg := notified[0][1]; strings.Contains(msg, "predate") || strings.Contains(msg, "0.85.1") {
			t.Errorf("notification = %q blames pi's age for a catalog that did not load", msg)
		}
	})
}

// THE REGISTRATION NAMES ITS OWN PROVIDER. pi 0.99.0 renamed its built-in openai-codex
// provider "OpenAI Codex (legacy)" (pi-ai dist/providers/openai-codex.js), and pi composes a
// provider's display name as the extension's `name`, else models.json's, else the built-in's
// (0.99.1 dist/core/provider-composer.js, composeModelProvider). A registration with no `name`
// therefore showed yolo's subscription provider as "(legacy)" in pi's menus
// (docs/design/model-lists-and-pickers.md §14.2; MM-D6: "Either form sets its own name").
// With and without a model list, because the name must not depend on the list, and on both
// routes, because the host route's provider starts as pi's built-in, legacy name and all.
func TestPiOpenAIAuthExtensionNamesTheProviderItRegisters(t *testing.T) {
	for _, route := range piRoutes {
		for _, tc := range []struct {
			name, file string
		}{{"with the rendered list", piCodexModelsFixture}, {"with no list", ""}} {
			t.Run(route.name+"/"+tc.name, func(t *testing.T) {
				f := newPiExtensionFixture(t)
				f.builtinStub(t)
				if tc.file != "" {
					f.modelsFile(t, tc.file)
				}
				f.output(t, route, piRegisterHarness+`
if (typeof view.name !== "string" || view.name.length === 0) throw new Error("the registration sets no name, so pi falls back to its built-in's label: " + JSON.stringify(view.name));
if (/legacy/i.test(view.name)) throw new Error("the registration names the provider " + JSON.stringify(view.name));
`)
			})
		}
	}
}

// piHostRouteClient is the fake client the host-route cases run: an authenticated broker whose
// token view counts its calls and expires at $EXPIRES_AT, far in the future unless a case says.
const piHostRouteClient = `
case "$3" in
  status) printf '{"logged_in":true,"login_required":false}\n' ;;
  login) printf 'unexpected browser login\n' >&2; exit 9 ;;
  token) printf '{"access_token":"access-%s","expires_at":%s,"generation":4}\n' "$(wc -l < "$CALLS" | tr -d ' ')" "${EXPIRES_AT:-4102444800000}" ;;
esac
`

// THE HOST ROUTE IS A NATIVE PROVIDER CONFIGURED BY THE HOST SOCKET (docs/design/pi-host-openai-auth.md
// OQ-1, D2). Where `yolo host` set the broker's socket and pi exports its built-in openai-codex
// provider, the extension registers that provider through pi's one-argument registerProvider,
// with yolo's login in place of pi's own and a key method pi counts as configured while the
// socket is set, with nothing stored in the user's auth.json (NC-D37). That method resolves to
// the broker's access token, reusing it inside pi's five-minute window, and offers no /login
// setup. Deleting the native registration, or ungating the check, fails this.
func TestPiOpenAIAuthHostRouteRegistersPisBuiltInAsANativeProvider(t *testing.T) {
	f := newPiExtensionFixture(t)
	f.builtinStub(t)
	f.fakeClient(t, piHostRouteClient)
	f.output(t, piHostRoute, `
import extension from "./extension.mjs";
import { readFileSync, existsSync } from "node:fs";
function eq(got, want, what) {
	if (JSON.stringify(got) !== JSON.stringify(want)) throw new Error(what + " = " + JSON.stringify(got) + ", want " + JSON.stringify(want));
}
const calls = () => existsSync(process.env.CALLS) ? readFileSync(process.env.CALLS, "utf8").trim().split("\n") : [];
const registrations = [];
await extension({ registerProvider(...args) { registrations.push(args); }, on() {} });
eq(registrations.map((args) => args.length), [1], "registerProvider calls, by argument count");
const p = registrations[0][0];
eq([p.id, p.name, p.baseUrl], ["openai-codex", "OpenAI Codex", "https://chatgpt.com/backend-api"], "id, name and address");
eq(p.getModels().map((m) => m.id), ["gpt-6-sol", "gpt-6-astra"], "with no list, pi's own catalog");
const { oauth, apiKey } = p.auth;
eq([oauth.name, oauth.isSubscription], ["OpenAI Codex (yolo shared login)", true], "the login is yolo's, not pi's own");
if (typeof apiKey?.check !== "function" || typeof apiKey?.resolve !== "function") throw new Error("no key method");
if ("login" in apiKey) throw new Error("the key method offers a /login setup, and there is no key to enter");
const signal = new AbortController().signal;
eq(await apiKey.check({ ctx: {}, signal }), { type: "oauth", source: "yolo shared login" }, "check with the socket set");
const socket = process.env.YOLO_OPENAI_AUTH_HOST_SOCKET;
delete process.env.YOLO_OPENAI_AUTH_HOST_SOCKET;
eq(await apiKey.check({ ctx: {}, signal }), undefined, "check with no socket");
eq(await apiKey.resolve({ ctx: {}, signal }), undefined, "resolve with no socket");
process.env.YOLO_OPENAI_AUTH_HOST_SOCKET = socket;
eq(calls(), [], "client calls from a check");
eq(await apiKey.resolve({ ctx: {}, signal }), { auth: { apiKey: "access-1" }, source: "yolo shared login" }, "resolve");
eq((await apiKey.resolve({ ctx: {}, signal })).auth.apiKey, "access-1", "a second resolve inside the token's life");
eq(calls(), ["internal openai-auth-client token"], "client calls from two resolves");
const login = await oauth.login({ signal });
eq([login.type, login.access, login.refresh], ["oauth", "access-3", "yolo-broker:4"], "the login's credential");
const refreshed = await oauth.refresh(login, signal);
eq([refreshed.type, refreshed.access, refreshed.refresh], ["oauth", "access-4", "yolo-broker:4"], "the refresh's credential");
eq(await oauth.toAuth(refreshed), { apiKey: "access-4" }, "the request auth of a stored login");
`)
}

// A TOKEN NEAR ITS END IS ASKED FOR AGAIN: the reuse ends where pi would refresh a stored login,
// five minutes before expiry, so a request never runs on a token about to lapse. Both sides of the
// window are pinned (docs/design/pi-host-openai-auth.md PH-D3): a token with four minutes left is
// fetched again, and one with six is reused, so a window much longer than five minutes, which
// would quietly turn the reuse off and run the client on every request, fails as surely as a
// shorter one.
func TestPiOpenAIAuthHostRouteAsksAgainForATokenNearItsEnd(t *testing.T) {
	for _, tc := range []struct {
		left  time.Duration
		keys  string
		calls int
	}{
		{left: 4 * time.Minute, keys: `["access-1","access-2"]`, calls: 2},
		{left: 6 * time.Minute, keys: `["access-1","access-1"]`, calls: 1},
	} {
		t.Run(tc.left.String()+" left", func(t *testing.T) {
			f := newPiExtensionFixture(t)
			f.builtinStub(t)
			f.fakeClient(t, piHostRouteClient)
			expires := time.Now().Add(tc.left).UnixMilli()
			f.output(t, piHostRoute, `
import extension from "./extension.mjs";
let provider;
await extension({ registerProvider(p) { provider = p; }, on() {} });
const signal = new AbortController().signal;
const keys = [];
for (let i = 0; i < 2; i++) keys.push((await provider.auth.apiKey.resolve({ ctx: {}, signal })).auth.apiKey);
if (JSON.stringify(keys) !== process.env.WANT_KEYS) throw new Error("keys = " + JSON.stringify(keys) + ", want " + process.env.WANT_KEYS);
`, fmt.Sprintf("EXPIRES_AT=%d", expires), "WANT_KEYS="+tc.keys)
			if got := strings.Count(f.calls(t), "internal openai-auth-client token\n"); got != tc.calls {
				t.Errorf("token calls from two resolves = %d, want %d; calls:\n%s", got, tc.calls, f.calls(t))
			}
		})
	}
}

// THE LIST AND THE REFUSAL RIDE ON THE NATIVE PROVIDER. A native registration replaces the
// extension layer pi would compose a ProviderConfig's models over, so the rendered list is the
// provider's own getModels: each definition completed as a Model of this provider, at the
// subscription's address, on its api, with pi's catalog facts for its base id; and no catalog
// refresh replaces it. With the list's switch on, the provider's two streams refuse a model
// outside it before pi's own stream runs, and hand a listed one on with pi's resolved options.
func TestPiOpenAIAuthHostRouteCarriesTheListAndTheRefusalOnTheProvider(t *testing.T) {
	f := newPiExtensionFixture(t)
	f.builtinStub(t)
	f.modelsFile(t, strings.Replace(piCodexModelsFixture, `{"models":[`, `{"enforce":true,"models":[`, 1))
	f.output(t, piHostRoute, piRegisterHarness+`
const { streamed } = await import("@earendil-works/pi-ai/providers/all");
const p = view.provider;
eq(view.models.map((m) => m.id), ["gpt-6-sol", "gpt-6-sol[1m]", "gpt-6-nova"], "the provider's models, in the file's order");
eq(p.getAllModels().map((m) => m.id), view.models.map((m) => m.id), "getAllModels");
for (const m of view.models) {
	eq([m.provider, m.api, m.baseUrl], ["openai-codex", "openai-codex-responses", "https://chatgpt.com/backend-api"], m.id + "'s address");
}
const [sol, sol1m, nova] = view.models;
eq(sol.thinkingLevelMap, { marker: "from-catalog-gpt-6-sol" }, "base id's catalog facts");
eq(sol1m.thinkingLevelMap, { marker: "from-catalog-gpt-6-sol" }, "the [1m] variant's facts from its BASE");
eq([sol1m.contextWindow, nova.maxTokens], [1000000, 16384], "declared window and the defaults path");
if (p.refreshModels !== undefined) throw new Error("a catalog refresh can replace the exact menu");
const options = { apiKey: "the-subscription-access-token" };
eq(p.streamSimple(sol1m, { messages: [] }, options), "builtin-stream", "a listed model's streamSimple");
eq(p.stream(sol, { messages: [] }, options), "builtin-stream", "a listed model's stream");
for (const call of ["stream", "streamSimple"]) {
	let refused = "";
	try { p[call]({ ...sol, id: "gpt-5.5" }, { messages: [] }, options); } catch (e) { refused = e.message; }
	if (!refused.includes('"openai-codex/gpt-5.5" is not on yolo') || !refused.includes("gpt-6-sol, gpt-6-sol[1m], gpt-6-nova")) {
		throw new Error(call + " of an unlisted model: " + JSON.stringify(refused));
	}
}
eq(streamed.map((c) => [c.via, c.model, c.options === options]),
	[["streamSimple", "gpt-6-sol[1m]", true], ["stream", "gpt-6-sol", true]], "what pi's own streams were handed");
`)
}

// THE PROVIDERCONFIG IS THE FALLBACK. With the socket set but nothing to register natively, the
// extension registers today's ProviderConfig, so an older pi keeps the login it had, yolo's entry
// in /login included (docs/design/pi-host-openai-auth.md PH-D4): a pi that exports no built-in
// providers, none for openai-codex, or one on another api, which nativeModel would point every
// listed model at with nothing to serve it; no catalog module at all; and a pi whose loader cannot
// take a provider object. That last is pi 0.80.10, MEASURED 2026-10-04: it exports the built-in,
// and its registerProvider queues the object as a name without throwing, then fails applying it,
// so the registration is lost and the extension reported as failed. A try/catch around the call
// cannot see that, so the extension asks pi's root for registerNativeProvider, and each case's
// stand-in registerProvider only records the call, as 0.80.10's throws nothing.
func TestPiOpenAIAuthHostRouteFallsBackToTheProviderConfig(t *testing.T) {
	builtins := piCatalogStubJS + piBuiltinProvidersStubJS
	for name, tc := range map[string]struct {
		allJS, coreJS string
	}{
		"pi exports no built-in providers": {allJS: piCatalogStubJS, coreJS: piNativeCoreStubJS},
		"pi's built-ins lack openai-codex": {allJS: piCatalogStubJS + "export function builtinProviders() { return []; }\n",
			coreJS: piNativeCoreStubJS},
		"pi's built-in serves another api": {allJS: piCatalogStubJS + `
export function builtinProviders() {
	const models = () => Object.keys(known).map((id) => ({ ...getBuiltinModel("openai-codex", id), api: "openai-responses-v9" }));
	return [{ id: "openai-codex", name: "OpenAI Codex (legacy)", auth: { oauth: {} }, getModels: models, getAllModels: models,
		streamSimple: () => { throw new Error("the built-in ran a model on an api it does not serve"); } }];
}
`, coreJS: piNativeCoreStubJS},
		"no catalog module at all":                          {coreJS: piNativeCoreStubJS},
		"pi 0.80.10, whose loader takes no provider object": {allJS: builtins, coreJS: piPreNativeCoreStubJS},
		"no pi package root to ask":                         {allJS: builtins},
	} {
		t.Run(name, func(t *testing.T) {
			f := newPiExtensionFixture(t)
			if tc.allJS != "" {
				f.stubPiAI(t, tc.allJS)
			}
			if tc.coreJS != "" {
				writePiCoreStub(t, f.dir, tc.coreJS)
			}
			f.modelsFile(t, piCodexModelsFixture)
			f.output(t, piHostRoute, piRegistrationViewJS+`
import extension from "./extension.mjs";
const registrations = [];
await extension({ registerProvider(...args) { registrations.push(args); }, on() {} });
if (registrations.length !== 1) throw new Error("registrations: " + registrations.length);
const view = registrationView(registrations[0]);
requireRoute(view);
if (!view.hasLogin || view.config.oauth?.name !== "OpenAI Codex (yolo shared login)") throw new Error("the fallback lost yolo's login");
if (JSON.stringify(view.config.models?.map((m) => m.id)) !== JSON.stringify(["gpt-6-sol", "gpt-6-sol[1m]", "gpt-6-nova"])) {
	throw new Error("the fallback lost the list: " + JSON.stringify(view.config.models));
}
`, "PI_WANT_NATIVE=0")
		})
	}
}

// THE JAIL ROUTE NEVER REGISTERS NATIVELY, even where pi exports its built-in: with no host
// socket the registration is the ProviderConfig a jail has always had, which counts as configured
// only for the login the jail stores before pi starts, and adds no key method to pi's /login.
// (The implementation decision recorded in docs/design/pi-host-openai-auth.md: native only where
// the socket is set.)
func TestPiOpenAIAuthJailRouteNeverRegistersNatively(t *testing.T) {
	f := newPiExtensionFixture(t)
	f.builtinStub(t)
	f.modelsFile(t, piCodexModelsFixture)
	f.output(t, piJailRoute, piRegisterHarness+`
if ("apiKey" in cfg) throw new Error("the jail's registration carries a key method");
`)
}
