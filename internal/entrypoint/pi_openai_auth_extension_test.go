package entrypoint

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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

// Pi's own lock is scoped to one workspace auth.json. The adapter must therefore ask the
// machine broker on every login and refresh, and must never put its canonical refresh token
// in that workspace file. A broker that is already authenticated must not start another
// browser flow. This executes the shipped extension with a fake yolo client.
func TestPiOpenAIAuthExtensionReusesBrokerLoginAndRefreshes(t *testing.T) {
	p := shippedPiPack(t)
	source, err := os.ReadFile(filepath.Join(p.Root, "extensions", "yolo-openai-auth.js"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	extension := filepath.Join(dir, "extension.mjs")
	if err := os.WriteFile(extension, source, 0o644); err != nil {
		t.Fatal(err)
	}
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
	yolo := filepath.Join(dir, "yolo")
	if err := os.WriteFile(yolo, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$CALLS"
case "$3" in
  status) printf '{"logged_in":true,"login_required":false}\n' ;;
  login) printf 'unexpected browser login\n' >&2; exit 9 ;;
  token) printf '{"access_token":"access-%s","refresh_token":"must-not-escape","expires_at":4102444800000,"account_id":"acct-1","generation":4}\n' "$(wc -l < "$CALLS" | tr -d ' ')" ;;
esac
`), 0o755); err != nil {
		t.Fatal(err)
	}
	harness := filepath.Join(dir, "harness.mjs")
	if err := os.WriteFile(harness, []byte(`
import extension from "./extension.mjs";
let registration;
await extension({ registerProvider(name, config) { registration = { name, config }; } });
if (registration.name !== "openai-codex") throw new Error("wrong provider: " + registration.name);
const oauth = registration.config.oauth;
const first = await oauth.login({});
const second = await oauth.refreshToken(first, new AbortController().signal);
if (first.refresh !== "yolo-broker:4" || second.refresh !== "yolo-broker:4") throw new Error("refresh secret escaped");
if (first.access !== "access-2" || second.access !== "access-3") throw new Error("broker calls were not sequenced");
if (first.expires !== 4102444800000 || second.accountId !== "acct-1") throw new Error("view shape lost");
if (oauth.getApiKey(second) !== "access-3") throw new Error("access token not resolved");
`), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(dir, "calls")
	cmd := exec.Command(requireNode(t, "the pi openai-auth extension"), harness)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "CALLS="+calls,
		// A temp HOME: the extension reads its model list from HOME, and a developer's real
		// ~/.pi must never feed a test.
		"HOME="+t.TempDir())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("executing Pi OpenAI extension: %v\n%s", err, output)
	}
	if strings.Contains(string(output), "unexpected browser login") {
		t.Fatalf("an existing broker login started a browser flow: %q", output)
	}
	got, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	want := "internal openai-auth-client status\ninternal openai-auth-client token\ninternal openai-auth-client token\n"
	if string(got) != want {
		t.Fatalf("broker calls = %q, want %q", strings.TrimSpace(string(got)), strings.TrimSpace(want))
	}
}

func TestPiOpenAIAuthExtensionStartsBrowserOnlyWhenStatusRequiresLogin(t *testing.T) {
	p := shippedPiPack(t)
	source, err := os.ReadFile(filepath.Join(p.Root, "extensions", "yolo-openai-auth.js"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "extension.mjs"), source, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "yolo"), []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$CALLS"
case "$3" in
  status) printf '{"logged_in":false}\n' ;;
  login) printf 'Open this URL: https://example.test/login\n' >&2; printf '{"ok":true}\n' ;;
  token) printf '{"access_token":"access","expires_at":4102444800000,"generation":1}\n' ;;
esac
`), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "harness.mjs"), []byte(`
import extension from "./extension.mjs";
let oauth;
await extension({ registerProvider(_name, config) { oauth = config.oauth; } });
const result = await oauth.login({});
if (result.access !== "access" || result.refresh !== "yolo-broker:1") throw new Error("bad credentials");
`), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(dir, "calls")
	cmd := exec.Command(requireNode(t, "the pi openai-auth extension"), filepath.Join(dir, "harness.mjs"))
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "CALLS="+calls,
		// A temp HOME: the extension reads its model list from HOME, and a developer's real
		// ~/.pi must never feed a test.
		"HOME="+t.TempDir())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("executing Pi OpenAI extension: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "https://example.test/login") {
		t.Fatalf("login URL was not forwarded: %q", output)
	}
	got, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	want := "internal openai-auth-client status\ninternal openai-auth-client login\ninternal openai-auth-client token\n"
	if string(got) != want {
		t.Fatalf("broker calls = %q, want %q", strings.TrimSpace(string(got)), strings.TrimSpace(want))
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

// catalogStub installs a stand-in for pi's own catalog module, resolved the way node
// resolves the extension's bare import from its directory. Known ids carry a marker the
// extension can only have copied from the catalog.
func (f piExtensionFixture) catalogStub(t *testing.T) {
	t.Helper()
	pkg := filepath.Join(f.dir, "node_modules", "@earendil-works", "pi-ai")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "package.json"), []byte(
		`{"name":"@earendil-works/pi-ai","type":"module","exports":{"./providers/all":"./all.js"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "all.js"), []byte(`
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
`), 0o644); err != nil {
		t.Fatal(err)
	}
}

// run executes a harness beside the extension and fails on a non-zero exit.
func (f piExtensionFixture) run(t *testing.T, harness string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, "harness.mjs"), []byte(harness), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(requireNode(t, "the pi openai-auth extension"), "harness.mjs")
	cmd.Dir = f.dir
	cmd.Env = append(os.Environ(), "HOME="+f.home)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("executing the Pi OpenAI extension harness: %v\n%s", err, output)
	}
}

// piRegisterHarness is the harness prefix every case shares: load the extension the way pi
// does (awaiting the factory) and capture the one provider it registers.
const piRegisterHarness = `
import extension from "./extension.mjs";
let registration;
let beforeProviderHandler;
await extension({
	registerProvider(name, config) { registration = { name, config }; },
	on(event, handler) { if (event === "before_provider_request") beforeProviderHandler = handler; },
});
if (!registration || registration.name !== "openai-codex") throw new Error("provider not registered as openai-codex");
const cfg = registration.config;
if (cfg.baseUrl !== "https://chatgpt.com/backend-api" || cfg.api !== "openai-codex-responses") throw new Error("address lost: " + JSON.stringify(cfg));
if (typeof cfg.oauth?.login !== "function" || typeof cfg.oauth?.refreshToken !== "function") throw new Error("oauth lost");
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
	cmd.Env = append(os.Environ(), "HOME="+f.home, "PI_HAS_UI="+ui)
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
	pkg := filepath.Join(f.dir, "node_modules", "@earendil-works", "pi-coding-agent")
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
// With and without a model list, because the name must not depend on the list.
func TestPiOpenAIAuthExtensionNamesTheProviderItRegisters(t *testing.T) {
	for _, tc := range []struct {
		name, file string
	}{{"with the rendered list", piCodexModelsFixture}, {"with no list", ""}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPiExtensionFixture(t)
			if tc.file != "" {
				f.modelsFile(t, tc.file)
			}
			f.run(t, piRegisterHarness+`
if (typeof cfg.name !== "string" || cfg.name.length === 0) throw new Error("the registration sets no name, so pi falls back to its built-in's label: " + JSON.stringify(cfg.name));
if (/legacy/i.test(cfg.name)) throw new Error("the registration names the provider " + JSON.stringify(cfg.name));
`)
		})
	}
}
