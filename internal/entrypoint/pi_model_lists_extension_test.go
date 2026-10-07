package entrypoint

// pi_model_lists_extension_test.go pins the pi half of an `only`
// (docs/design/model-lists-and-pickers.md MM-D6) from the shipped declaration to what the
// extension hands pi: the pi pack delivers yolo-model-lists.js into pi's extension discovery
// directory, and the extension, reading the file a real boot render wrote at the path the
// manifest declares, registers exactly the narrowed list. With the profile's enforce_models off
// it passes `models` alone, the form that keeps pi's own address, wire and credential for the
// provider; with it on (the default) it adds `api` and a `streamSimple` wrapper that refuses a
// model outside the list and hands a listed one, with pi's resolved options untouched, to the
// stream pi would have used (MM-D21). The extension runs under node against a stand-in for
// pi-ai, so no pi process starts.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestShippedPiPackDeliversTheModelListsExtension(t *testing.T) {
	p := shippedPiPack(t)
	var found bool
	for _, c := range p.Decl.Contributions() {
		if c.Kind == packdecl.KindFiles && c.From == "extensions/yolo-model-lists.js" &&
			c.Into == ".pi/agent/extensions/yolo-model-lists.js" {
			found = true
		}
	}
	if !found {
		t.Fatal("the pi pack does not deliver yolo-model-lists.js to pi's extension directory")
	}
	home := t.TempDir()
	if _, err := RenderHostFiles(p, home, filesReq(t), false); err != nil {
		t.Fatalf("rendering the shipped pi extensions: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".pi", "agent", "extensions", "yolo-model-lists.js")); err != nil {
		t.Fatalf("pi cannot discover the rendered extension: %v", err)
	}
}

// piModelListsRel is the pi/model-lists surface's path relative to HOME, read off the shipped
// manifest, so the extension's path is pinned to the manifest's.
func piModelListsRel(t *testing.T) string {
	t.Helper()
	pi, err := embeddedPack("pi")
	if err != nil {
		t.Fatal(err)
	}
	surfaces, _ := pi.SurfacesFor(false)
	for _, s := range surfaces {
		if s.Agent == "pi" && s.Name == "model-lists" {
			rel, ok := strings.CutPrefix(s.Path, "~/")
			if !ok {
				t.Fatalf("pi/model-lists path %q is not home-relative", s.Path)
			}
			return rel
		}
	}
	t.Fatal("packs/pi declares no pi/model-lists surface")
	return ""
}

// piZaiCatalogStub stands in for pi-ai's providers/all module. Its catalog knows glm-5.3 on zai
// and nothing else, and its built-in zai provider serves openai-completions and records every
// stream it is handed, so a harness can tell which delegate a listed model reached and with what
// options.
const piZaiCatalogStub = `
export const calls = [];
export function getBuiltinModel(provider, id) {
	if (provider !== "zai" || id !== "glm-5.3") return undefined;
	return { id, name: "Catalog GLM-5.3", api: "openai-completions", provider, baseUrl: "https://catalog.example",
		reasoning: true, input: ["text"], maxTokens: 131072, contextWindow: 200000,
		cost: { input: 1, output: 3, cacheRead: 0, cacheWrite: 0 } };
}
export function getBuiltinModels(provider) {
	return provider === "zai" ? [getBuiltinModel("zai", "glm-5.3")] : [];
}
export function builtinProviders() {
	return [{ id: "zai", getModels: () => getBuiltinModels("zai"),
		streamSimple: (model, context, options) => { calls.push({ via: "builtin", model: model.id, options }); return "builtin-stream"; } }];
}
`

// piCompatStub stands in for pi-ai's compat module: an api registry whose every stream records
// what it is handed, into the providers/all stub's log.
const piCompatStub = `
import { calls } from "./all.js";
export function getApiProvider(api) {
	return { api, streamSimple: (model, context, options) => { calls.push({ via: "registry:" + api, model: model.id, options }); return "registry-stream"; } };
}
`

// piStreamAttempt is what a registration's streamSimple did with one model: what it returned, or
// the message it threw.
type piStreamAttempt struct {
	Returned string `json:"returned"`
	Error    string `json:"error"`
}

// piModelListsRun is what one run of the shipped extension did: each registration, with its
// config's keys, its api and its models and, when it carries a streamSimple, what that did with a
// listed and an unlisted model; every call a delegate stream received; and every warning shown.
type piModelListsRun struct {
	Registrations []struct {
		Name     string           `json:"name"`
		Keys     []string         `json:"keys"`
		API      string           `json:"api"`
		Models   []map[string]any `json:"models"`
		Listed   piStreamAttempt  `json:"listed"`
		Unlisted piStreamAttempt  `json:"unlisted"`
	} `json:"registrations"`
	Calls []struct {
		Via          string `json:"via"`
		Model        string `json:"model"`
		SameOptions  bool   `json:"sameOptions"`
		OptionAPIKey string `json:"optionApiKey"`
	} `json:"calls"`
	Warnings []string `json:"warnings"`
}

// piModelListsHarness loads the extension with a stand-in for pi's extension API, fires
// session_start so a deferred warning shows, and, for each registration carrying a streamSimple,
// calls it once with the list's first model and once with an id the list does not hold, each
// with an options object standing for the credential pi resolved before calling it.
const piModelListsHarness = `
import extension from "./extension.mjs";
import { calls } from "@earendil-works/pi-ai/providers/all";
const registrations = [], handlers = [], warnings = [];
const resolved = { apiKey: "pi-resolved-key", headers: { "x-pi": "1" } };
await extension({
	registerProvider(name, config) { registrations.push({ name, config }); },
	on(event, handler) { if (event === "session_start") handlers.push(handler); },
});
for (const h of handlers) h({}, { hasUI: true, ui: { notify: (message) => warnings.push(message) } });
const out = [];
for (const { name, config } of registrations) {
	const r = { name, keys: Object.keys(config).sort(), api: config.api ?? "", models: config.models ?? [] };
	if (typeof config.streamSimple === "function") {
		const attempt = (id) => {
			try {
				return { returned: String(config.streamSimple({ id, api: config.api }, { messages: [] }, resolved)) };
			} catch (e) {
				return { error: e.message };
			}
		};
		r.listed = attempt(r.models[0].id);
		r.unlisted = attempt("not-on-the-list");
	}
	out.push(r);
}
console.log(JSON.stringify({
	registrations: out,
	calls: (calls ?? []).map((c) => ({ via: c.via, model: c.model, sameOptions: c.options === resolved,
		optionApiKey: c.options?.apiKey ?? "" })),
	warnings,
}));
`

// runPiModelListsExtension runs the SHIPPED yolo-model-lists.js under node, with HOME holding
// lists at the pi/model-lists surface's path and pi-ai's two modules stubbed by allJS and
// compatJS.
func runPiModelListsExtension(t *testing.T, lists []byte, allJS, compatJS string) piModelListsRun {
	t.Helper()
	p := shippedPiPack(t)
	source, err := os.ReadFile(filepath.Join(p.Root, "extensions", "yolo-model-lists.js"))
	if err != nil {
		t.Fatal(err)
	}
	dir, home := t.TempDir(), t.TempDir()
	dest := filepath.Join(home, filepath.FromSlash(piModelListsRel(t)))
	pkg := filepath.Join(dir, "node_modules", "@earendil-works", "pi-ai")
	for _, d := range []string{filepath.Dir(dest), pkg} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for path, body := range map[string]string{
		filepath.Join(dir, "extension.mjs"): string(source),
		filepath.Join(dir, "harness.mjs"):   piModelListsHarness,
		dest:                                string(lists),
		filepath.Join(pkg, "package.json"): `{"name":"@earendil-works/pi-ai","type":"module",` +
			`"exports":{"./providers/all":"./all.js","./compat":"./compat.js"}}`,
		filepath.Join(pkg, "all.js"):    allJS,
		filepath.Join(pkg, "compat.js"): compatJS,
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(requireNode(t, "the pi model-lists extension"), "harness.mjs")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("running the shipped extension: %v\n%s", err, out)
	}
	var run piModelListsRun
	if err := json.Unmarshal(out, &run); err != nil {
		t.Fatalf("decoding %s: %v", out, err)
	}
	return run
}

// renderedZaiLists is the pi/model-lists file a real boot render writes for zai narrowed by a
// company pack's `only`: under the zai profile, or, with open set, under a user profile over zai
// that turns enforce_models off.
func renderedZaiLists(t *testing.T, open bool) []byte {
	t.Helper()
	var user map[string]packload.UserProfile
	profile := "zai"
	if open {
		off := false
		user = map[string]packload.UserProfile{"zai-open": {Provider: "zai", EnforceModels: &off}}
		profile = "zai-open"
	}
	providers, profiles := zaiNarrowed(t, "pi", user)
	r := newPioencodeRender(t, providers)
	r.wireProfiles(profiles)
	r.render(t, `{"pi":"`+profile+`"}`)
	raw, err := os.ReadFile(filepath.Join(r.e.Home, filepath.FromSlash(piModelListsRel(t))))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// WITH THE SWITCH OFF the registration is `models` alone: the menu is exact, nothing refuses, and
// pi keeps its own address, wire and credential for the provider.
func TestPiModelListsExtensionRegistersTheRenderedListAlone(t *testing.T) {
	run := runPiModelListsExtension(t, renderedZaiLists(t, true), piZaiCatalogStub, piCompatStub)
	got := run.Registrations
	if len(got) != 1 || got[0].Name != "zai" {
		t.Fatalf("registrations = %+v, want one, for zai", got)
	}
	if strings.Join(got[0].Keys, ",") != "models" {
		t.Errorf("the registration carries %v with enforce_models off: only `models` keeps pi's own "+
			"address, wire and credential for the provider", got[0].Keys)
	}
	models := got[0].Models
	if len(models) != 2 || models[0]["id"] != "glm-5.3" || models[1]["id"] != "glm-5.3-flash" {
		t.Fatalf("registered models = %v, want glm-5.3 then glm-5.3-flash", models)
	}
	known, unknown := models[0], models[1]
	if known["name"] != "Catalog GLM-5.3" || known["maxTokens"] != float64(131072) {
		t.Errorf("glm-5.3 = %v, want pi's catalog facts where yolo declares none", known)
	}
	if known["contextWindow"] != float64(1000000) {
		t.Errorf("glm-5.3 contextWindow = %v, want zai's declared 1,000,000 over the catalog's", known["contextWindow"])
	}
	for _, k := range []string{"api", "baseUrl", "provider"} {
		if _, has := known[k]; has {
			t.Errorf("glm-5.3 carries the catalog's %s: the registration must never repoint the provider", k)
		}
	}
	if unknown["name"] != "glm-5.3-flash" || unknown["maxTokens"] != float64(16384) {
		t.Errorf("glm-5.3-flash = %v, want pi's models.json defaults for an id its catalog lacks", unknown)
	}
	if in, _ := unknown["input"].([]any); len(in) != 2 {
		t.Errorf("glm-5.3-flash input = %v, want zai's declared text and image", unknown["input"])
	}
	if len(run.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", run.Warnings)
	}
}

// WITH THE SWITCH ON, THE DEFAULT, THE REGISTRATION REFUSES (MM-D6, MM-D21): it names the api the
// derive stated for zai's models.json row and carries a streamSimple that ends an unlisted model's
// turn with yolo's refusal before any stream starts, and hands a listed one to pi's own built-in
// zai provider with the very options pi resolved, credential included. Deleting the wrapper, or
// the derive's `enforce`, fails this.
func TestPiModelListsExtensionRefusesAModelOutsideTheList(t *testing.T) {
	run := runPiModelListsExtension(t, renderedZaiLists(t, false), piZaiCatalogStub, piCompatStub)
	got := run.Registrations
	if len(got) != 1 || got[0].Name != "zai" {
		t.Fatalf("registrations = %+v, want one, for zai", got)
	}
	reg := got[0]
	if strings.Join(reg.Keys, ",") != "api,models,streamSimple" {
		t.Fatalf("the registration carries %v, want api, models and streamSimple: pi runs an "+
			"extension's streamSimple only for models of its api, and refuses one without an api", reg.Keys)
	}
	if reg.API != "openai-completions" {
		t.Errorf("api = %q, want openai-completions, the api of zai's models.json row", reg.API)
	}
	if len(reg.Models) != 2 {
		t.Errorf("registered models = %v, want the narrowed two", reg.Models)
	}
	if reg.Listed.Error != "" || reg.Listed.Returned != "builtin-stream" {
		t.Errorf("a listed model got %+v, want pi's own built-in zai stream", reg.Listed)
	}
	for _, want := range []string{`"zai/not-on-the-list"`, "not on yolo's model list", "glm-5.3, glm-5.3-flash",
		`"enforce_models": false`} {
		if !strings.Contains(reg.Unlisted.Error, want) {
			t.Errorf("an unlisted model's refusal = %q, want it to say %s", reg.Unlisted.Error, want)
		}
	}
	if len(run.Calls) != 1 || run.Calls[0].Model != "glm-5.3" || run.Calls[0].Via != "builtin" {
		t.Fatalf("delegate calls = %+v, want the listed model alone, to the built-in provider", run.Calls)
	}
	if !run.Calls[0].SameOptions || run.Calls[0].OptionAPIKey != "pi-resolved-key" {
		t.Errorf("the delegate got %+v, want the options pi resolved, untouched: that is how pi's own "+
			"credential reaches the stream", run.Calls[0])
	}
}

// A PROVIDER PI DOES NOT SHIP (a key yolo defines, such as kilo) has no built-in provider, so the
// wrapper hands a listed model to pi's api registry for the row's api, which is where pi itself
// sends a registration's models when no built-in serves them.
func TestPiModelListsExtensionDelegatesAProviderPiDoesNotShipToItsAPIRegistry(t *testing.T) {
	lists := []byte(`{"providers":{"kilo":{"models":[{"id":"vendor/a"}],"enforce":true,"api":"openai-completions"}}}`)
	run := runPiModelListsExtension(t, lists, piZaiCatalogStub, piCompatStub)
	if len(run.Registrations) != 1 || run.Registrations[0].API != "openai-completions" {
		t.Fatalf("registrations = %+v, want kilo's, refusing on openai-completions", run.Registrations)
	}
	if run.Registrations[0].Unlisted.Error == "" {
		t.Error("an unlisted kilo model was not refused")
	}
	if len(run.Calls) != 1 || run.Calls[0].Via != "registry:openai-completions" || !run.Calls[0].SameOptions {
		t.Errorf("delegate calls = %+v, want vendor/a to pi's openai-completions stream with pi's options", run.Calls)
	}
}

// A LIST WHOSE MODELS RUN ON TWO pi APIS CANNOT BE REFUSED WHOLE: a registration names one api,
// pi hands its wrapper only that api's models, and pi's --model fallback copies a listed model of
// either api. So it is registered as the exact menu alone, and pi says once that it cannot refuse
// there, rather than refusing half of what --model can reach.
func TestPiModelListsExtensionSaysWhenAListSpanningTwoAPIsCannotBeRefused(t *testing.T) {
	const twoAPIs = `
export const calls = [];
const catalog = { "vendor/a": "openai-completions", "vendor/b": "anthropic-messages" };
export function getBuiltinModel(provider, id) {
	return provider === "openrouter" && catalog[id] ? { id, name: id, api: catalog[id], provider } : undefined;
}
export function getBuiltinModels(provider) {
	return provider === "openrouter" ? Object.keys(catalog).map((id) => getBuiltinModel(provider, id)) : [];
}
export function builtinProviders() { return []; }
`
	lists := []byte(`{"providers":{"openrouter":{"models":[{"id":"vendor/a"},{"id":"vendor/b"}],"enforce":true}}}`)
	run := runPiModelListsExtension(t, lists, twoAPIs, piCompatStub)
	if len(run.Registrations) != 1 || strings.Join(run.Registrations[0].Keys, ",") != "models" {
		t.Fatalf("registrations = %+v, want openrouter's list as models alone", run.Registrations)
	}
	if len(run.Warnings) != 1 || !strings.Contains(run.Warnings[0], "openrouter") ||
		!strings.Contains(run.Warnings[0], "cannot refuse") {
		t.Errorf("warnings = %v, want one saying pi cannot refuse outside openrouter's list", run.Warnings)
	}
}

// PI'S OWN BEDROCK CLIENT HAS NO models.json ROW, so the derive states no api and the extension
// takes it from pi's catalog: the listed ids' own api, and for an id the catalog lacks the one api
// pi's catalog gives the whole provider.
func TestPiModelListsExtensionReadsTheBedrockAPIFromPisCatalog(t *testing.T) {
	const bedrock = `
export const calls = [];
export function getBuiltinModel(provider, id) {
	return provider === "amazon-bedrock" && id === "us.anthropic.claude-opus-4-6-v1"
		? { id, name: "Claude Opus 4.6", api: "bedrock-converse-stream", provider } : undefined;
}
export function getBuiltinModels(provider) {
	return provider === "amazon-bedrock" ? [getBuiltinModel(provider, "us.anthropic.claude-opus-4-6-v1")] : [];
}
export function builtinProviders() {
	return [{ id: "amazon-bedrock", getModels: () => getBuiltinModels("amazon-bedrock"),
		streamSimple: (model, context, options) => { calls.push({ via: "builtin", model: model.id, options }); return "builtin-stream"; } }];
}
`
	lists := []byte(`{"providers":{"amazon-bedrock":{"models":[{"id":"global.openai.gpt-6-astra"},` +
		`{"id":"us.anthropic.claude-opus-4-6-v1"}],"enforce":true}}}`)
	run := runPiModelListsExtension(t, lists, bedrock, piCompatStub)
	if len(run.Registrations) != 1 || run.Registrations[0].API != "bedrock-converse-stream" {
		t.Fatalf("registrations = %+v, want amazon-bedrock's, refusing on bedrock-converse-stream", run.Registrations)
	}
	if len(run.Calls) != 1 || run.Calls[0].Via != "builtin" || run.Calls[0].Model != "global.openai.gpt-6-astra" {
		t.Errorf("delegate calls = %+v, want the listed id to pi's own Bedrock provider", run.Calls)
	}
}

// piOpenRouterCatalogStub stands in for pi-ai's catalog on openrouter: it knows the DeepSeek
// V4.1 Flash BASE the two routing variants inherit from, with the thinkingLevelMap a real
// catalog row carries for a model whose minimal and medium levels are unsupported (the nulls
// acceptance 3 is about), and an openai-completions stream, so a list whose entries base to it
// resolves to one api and registers with the refusing wrapper.
const piOpenRouterCatalogStub = `
export const calls = [];
export function getBuiltinModel(provider, id) {
	if (provider !== "openrouter" || id !== "deepseek/deepseek-v4.1-flash") return undefined;
	return { id, name: "DeepSeek V4.1 Flash", api: "openai-completions", provider,
		baseUrl: "https://openrouter.ai/api/v1", reasoning: true, input: ["text", "image"],
		maxTokens: 131072, contextWindow: 163840,
		thinkingLevelMap: { off: null, minimal: null, low: "low", medium: null, high: "high", max: "max" },
		cost: { input: 0.1, output: 0.4, cacheRead: 0.01, cacheWrite: 0 } };
}
export function getBuiltinModels(provider) {
	return provider === "openrouter" ? [getBuiltinModel("openrouter", "deepseek/deepseek-v4.1-flash")] : [];
}
export function builtinProviders() {
	return [{ id: "openrouter", getModels: () => getBuiltinModels("openrouter"),
		streamSimple: (model, context, options) => { calls.push({ via: "builtin", model: model.id, options }); return "builtin-stream"; } }];
}
`

// renderedOpenRouterLists is the pi/model-lists file a real boot render writes for a USER
// config that declares two OpenRouter routing variants on the built-in openrouter provider and
// selects a profile on it. The rows are the shipped ids; the routing objects are the two the
// task names, and both variants base to the one catalog row.
func renderedOpenRouterLists(t *testing.T) []byte {
	t.Helper()
	decoded, err := jsonx.Decode([]byte(`{"openrouter":{"models":{
	  "deepseek-floor":{"id":"deepseek/deepseek-v4.1-flash:floor","base":"deepseek/deepseek-v4.1-flash",
	    "openrouter_routing":{"order":["streamlake","morph","deepinfra"],"allow_fallbacks":false}},
	  "deepseek-nitro":{"id":"deepseek/deepseek-v4.1-flash:nitro","base":"deepseek/deepseek-v4.1-flash",
	    "openrouter_routing":{"order":["together","streamlake","morph"],"allow_fallbacks":false}}
	}}}`))
	if err != nil {
		t.Fatalf("fixture providers: %v", err)
	}
	user, _ := decoded.(*jsonx.OrderedMap)
	providers, err := packload.ComposeProviders(user, testPacksForAgent(t, "pi"))
	if err != nil {
		t.Fatal(err)
	}
	r := newPioencodeRender(t, mustCompactJSON(t, providers))
	// The shipped openrouter profile selects the built-in openrouter provider; enforce_models
	// defaults on, so the registration carries the wrapper too.
	r.wireProfiles(`{"openrouter":{"provider":"openrouter"}}`)
	r.render(t, `{"pi":"openrouter"}`)
	raw, err := os.ReadFile(filepath.Join(r.e.Home, filepath.FromSlash(piModelListsRel(t))))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// piModelListRouting is one rendered entry's routing facts, decoded from the JSON the boot
// render wrote, so the test asserts on the file's bytes and not on a Go value the render never
// produced.
type piModelListRouting struct {
	Models []struct {
		ID      string         `json:"id"`
		Base    string         `json:"base"`
		Routing map[string]any `json:"openrouter_routing"`
	} `json:"models"`
}

// A USER CONFIG DECLARES OPENROUTER ROUTING; THE BUILT-IN PROVIDER'S MENU IS THE DECLARED LIST
// (acceptance 1-4). The boot render writes a model-lists entry for openrouter even without an
// `only`, each row carries its `base` and its `openrouter_routing`, and the shipped extension
// lowers the routing into pi's `compat.openRouterRouting` (the request's `provider` field)
// while the catalog row's thinkingLevelMap survives — nulls included. WITHOUT routing the
// surface stays empty (acceptance 4), which the last assertion pins.
func TestPiRendersOpenRouterRoutingFromAUserConfig(t *testing.T) {
	raw := renderedOpenRouterLists(t)
	var file struct {
		Providers map[string]piModelListRouting `json:"providers"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("the rendered model-lists file is not JSON: %v\n%s", err, raw)
	}
	or, ok := file.Providers["openrouter"]
	if !ok {
		t.Fatalf("the boot render wrote no openrouter list:\n%s", raw)
	}
	want := []struct {
		id      string
		base    string
		routing map[string]any
	}{
		{"deepseek/deepseek-v4.1-flash:floor", "deepseek/deepseek-v4.1-flash",
			map[string]any{"order": []any{"streamlake", "morph", "deepinfra"}, "allow_fallbacks": false}},
		{"deepseek/deepseek-v4.1-flash:nitro", "deepseek/deepseek-v4.1-flash",
			map[string]any{"order": []any{"together", "streamlake", "morph"}, "allow_fallbacks": false}},
	}
	if len(or.Models) != len(want) {
		t.Fatalf("rendered openrouter models = %+v, want the two declared routes", or.Models)
	}
	for i, w := range want {
		m := or.Models[i]
		if m.ID != w.id || m.Base != w.base {
			t.Errorf("rendered model %d = %+v, want id %s base %s", i, m, w.id, w.base)
		}
		if !reflect.DeepEqual(m.Routing, w.routing) {
			t.Errorf("rendered %s routing = %v, want %v", m.ID, m.Routing, w.routing)
		}
	}

	// The shipped extension, under node, reading the file the boot render wrote: the routing is
	// pi's compat.openRouterRouting and the catalog's thinkingLevelMap survives whole (nulls
	// included), so a request built from the registered model carries `provider: {order, …,
	// allow_fallbacks: false}` — pi's buildParams does exactly this for a model whose compat
	// carries openRouterRouting — while the row still declares no address of its own.
	run := runPiModelListsExtension(t, raw, piOpenRouterCatalogStub, piCompatStub)
	if len(run.Registrations) != 1 || run.Registrations[0].Name != "openrouter" {
		t.Fatalf("registrations = %+v, want one, for openrouter", run.Registrations)
	}
	reg := run.Registrations[0]
	if len(reg.Models) != 2 {
		t.Fatalf("registered models = %+v, want the two declared routes", reg.Models)
	}
	for i, w := range want {
		m := reg.Models[i]
		if m["id"] != w.id {
			t.Errorf("registered model %d id = %v, want %s", i, m["id"], w.id)
		}
		if _, stray := m["openrouter_routing"]; stray {
			t.Errorf("registered %s carries a top-level openrouter_routing; definition() must lower it into compat", m["id"])
		}
		if _, stray := m["base"]; stray {
			t.Errorf("registered %s carries `base`; it is a lookup key, not a pi model field", m["id"])
		}
		compat, _ := m["compat"].(map[string]any)
		if !reflect.DeepEqual(compat["openRouterRouting"], w.routing) {
			t.Errorf("registered %s compat.openRouterRouting = %v, want %v", m["id"], compat["openRouterRouting"], w.routing)
		}
		// The catalog facts the variant inherits: the nulls must be PRESENT as nulls, not absent.
		levels, _ := m["thinkingLevelMap"].(map[string]any)
		if levels == nil {
			t.Fatalf("registered %s lost the catalog's thinkingLevelMap: %v", m["id"], m)
		}
		for _, level := range []string{"off", "minimal", "medium"} {
			got, present := levels[level]
			if !present || got != nil {
				t.Errorf("registered %s thinkingLevelMap.%s = %v (present %v), want an explicit null", m["id"], level, got, present)
			}
		}
		if levels["high"] != "high" || levels["max"] != "max" {
			t.Errorf("registered %s thinkingLevelMap = %v, want the catalog's supported levels kept", m["id"], levels)
		}
		for _, k := range []string{"api", "baseUrl", "provider"} {
			if _, has := m[k]; has {
				t.Errorf("registered %s carries the catalog's %s: the registration must never repoint the provider", m["id"], k)
			}
		}
	}

	// NO ROUTING DECLARED, NO CHANGE (acceptance 4): the same openrouter provider with no
	// routing fact renders no model-lists entry at all, so nothing is registered over pi's own
	// catalog. The user's list here is an ordinary models map, which the catalog derive owns.
	decoded, err := jsonx.Decode([]byte(`{"openrouter":{"models":{"deepseek-floor":"deepseek/deepseek-v4.1-flash"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := decoded.(*jsonx.OrderedMap)
	providers, err := packload.ComposeProviders(plain, testPacksForAgent(t, "pi"))
	if err != nil {
		t.Fatal(err)
	}
	r := newPioencodeRender(t, mustCompactJSON(t, providers))
	r.wireProfiles(`{"openrouter":{"provider":"openrouter"}}`)
	r.render(t, `{"pi":"openrouter"}`)
	unchanged, err := os.ReadFile(filepath.Join(r.e.Home, filepath.FromSlash(piModelListsRel(t))))
	if err != nil {
		t.Fatal(err)
	}
	var noRouting struct {
		Providers map[string]json.RawMessage `json:"providers"`
	}
	if err := json.Unmarshal(unchanged, &noRouting); err != nil {
		t.Fatalf("the no-routing model-lists file is not JSON: %v\n%s", err, unchanged)
	}
	if _, present := noRouting.Providers["openrouter"]; present {
		t.Errorf("a provider with no routing declared registered a list anyway:\n%s", unchanged)
	}
}
