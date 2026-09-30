package entrypoint

// pi_openai_auth_refusal_test.go pins pi's refusal on openai-codex
// (docs/design/model-lists-and-pickers.md MM-D6, MM-D23), the one provider yolo-model-lists.js's
// wrapper does not reach, from the boot render to what the shipped yolo-openai-auth.js hands pi.
// The pi/codex-models file a real boot render writes carries the switch of the profile that
// governs openai-codex as `enforce`, and while it is on the extension's registration, which also
// carries the subscription login, adds a `streamSimple` that refuses a model outside the list and
// hands a listed one, with the options pi resolved (the login's bearer token among them), to pi's
// own openai-codex stream. That the login reaches a listed model this way was MEASURED on pi
// 0.99.1's shipped bundle against a mock endpoint ([§14.4] of the design); these tests run the
// extension under node against a stand-in for pi-ai, so no pi process starts.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// renderedCodexModels is the pi/codex-models file a real boot render of the pi pack writes over
// the provider table its needs closure composes, with useProfiles as the launch's selection and
// user as the user's own profiles.
func renderedCodexModels(t *testing.T, user map[string]packload.UserProfile, useProfiles string) []byte {
	t.Helper()
	packs := testPacksForAgent(t, "pi")
	providers, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles(packs, user, providers)
	if err != nil {
		t.Fatal(err)
	}
	r := newPioencodeRender(t, mustCompactJSON(t, providers))
	r.wireProfiles(mustCompactJSON(t, packload.ProfilesWireTable(resolved)))
	r.render(t, useProfiles)
	raw, err := os.ReadFile(filepath.Join(r.e.Home, filepath.FromSlash(piCodexModelsRel(t))))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// piCodexStub stands in for pi-ai's providers/all module: a catalog that describes every
// openai-codex id (so no degraded-catalog warning is in play), and, unless noBuiltin, a built-in
// openai-codex provider on openai-codex-responses whose stream records what it is handed.
func piCodexStub(noBuiltin bool) string {
	builtins := `[{ id: "openai-codex", getModels: () => [getBuiltinModel("openai-codex", "gpt-6.1-sol")],
		streamSimple: (model, context, options) => { calls.push({ via: "builtin", model: model.id, options }); return "builtin-stream"; } }]`
	if noBuiltin {
		builtins = "[]"
	}
	return `
export const calls = [];
export function getBuiltinModel(provider, id) {
	if (provider !== "openai-codex") return undefined;
	return { id, name: id, api: "openai-codex-responses", provider, baseUrl: "https://catalog.example",
		reasoning: true, input: ["text"], maxTokens: 128000, contextWindow: 272000,
		cost: { input: 1, output: 1, cacheRead: 0, cacheWrite: 0 } };
}
export function builtinProviders() { return ` + builtins + `; }
`
}

// piCodexRun is what one run of the shipped openai-auth extension did: its one registration's
// keys, api, address and model ids; whether the login survived; what its streamSimple, when it
// has one, did with each probe id; every call a delegate stream received; and every warning.
type piCodexRun struct {
	Name     string            `json:"name"`
	Keys     []string          `json:"keys"`
	API      string            `json:"api"`
	BaseURL  string            `json:"baseUrl"`
	OAuth    bool              `json:"oauth"`
	Models   []string          `json:"models"`
	Attempts []piStreamAttempt `json:"attempts"`
	Calls    []struct {
		Via          string `json:"via"`
		Model        string `json:"model"`
		SameOptions  bool   `json:"sameOptions"`
		OptionAPIKey string `json:"optionApiKey"`
	} `json:"calls"`
	Warnings []string `json:"warnings"`
}

// piCodexRefusalHarness loads the extension with a stand-in for pi's extension API, fires
// session_start so a deferred warning shows, and hands the registration's streamSimple, when
// there is one, each id in PROBE_IDS with an options object standing for the subscription login
// pi resolved before calling it.
const piCodexRefusalHarness = `
import extension from "./extension.mjs";
const all = await import("@earendil-works/pi-ai/providers/all").catch(() => ({}));
const registrations = [], handlers = [], warnings = [];
const resolved = { apiKey: "the-subscription-access-token", headers: { "x-pi": "1" } };
await extension({
	registerProvider(name, config) { registrations.push({ name, config }); },
	on(event, handler) { if (event === "session_start") handlers.push(handler); },
});
for (const h of handlers) h({}, { hasUI: true, ui: { notify: (message) => warnings.push(message) } });
if (registrations.length !== 1) throw new Error("registrations: " + registrations.length);
const { name, config } = registrations[0];
const attempts = [];
if (typeof config.streamSimple === "function") {
	for (const id of JSON.parse(process.env.PROBE_IDS)) {
		try {
			attempts.push({ returned: String(config.streamSimple({ id, api: config.api }, { messages: [] }, resolved)) });
		} catch (e) {
			attempts.push({ error: e.message });
		}
	}
}
console.log(JSON.stringify({
	name, keys: Object.keys(config).sort(), api: config.api ?? "", baseUrl: config.baseUrl ?? "",
	oauth: typeof config.oauth?.login === "function" && typeof config.oauth?.refreshToken === "function" &&
		config.oauth?.getApiKey?.({ access: "a" }) === "a",
	models: (config.models ?? []).map((m) => m.id), attempts,
	calls: (all.calls ?? []).map((c) => ({ via: c.via, model: c.model, sameOptions: c.options === resolved,
		optionApiKey: c.options?.apiKey ?? "" })),
	warnings,
}));
`

// runPiCodexRefusal runs the SHIPPED yolo-openai-auth.js under node with HOME holding list at the
// pi/codex-models surface's path. allJS stubs pi-ai's providers/all, and compatJS its compat
// module; an empty string leaves that module out, so its import fails as it would in a pi that
// dropped it.
func runPiCodexRefusal(t *testing.T, list []byte, allJS, compatJS string, probe ...string) piCodexRun {
	t.Helper()
	p := shippedPiPack(t)
	source, err := os.ReadFile(filepath.Join(p.Root, "extensions", "yolo-openai-auth.js"))
	if err != nil {
		t.Fatal(err)
	}
	dir, home := t.TempDir(), t.TempDir()
	dest := filepath.Join(home, filepath.FromSlash(piCodexModelsRel(t)))
	pkg := filepath.Join(dir, "node_modules", "@earendil-works", "pi-ai")
	for _, d := range []string{filepath.Dir(dest), pkg} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	exports := map[string]string{}
	files := map[string]string{
		filepath.Join(dir, "extension.mjs"): string(source),
		filepath.Join(dir, "harness.mjs"):   piCodexRefusalHarness,
		dest:                                string(list),
	}
	if allJS != "" {
		exports["./providers/all"] = "./all.js"
		files[filepath.Join(pkg, "all.js")] = allJS
	}
	if compatJS != "" {
		exports["./compat"] = "./compat.js"
		files[filepath.Join(pkg, "compat.js")] = compatJS
	}
	manifest, err := json.Marshal(map[string]any{"name": "@earendil-works/pi-ai", "type": "module", "exports": exports})
	if err != nil {
		t.Fatal(err)
	}
	files[filepath.Join(pkg, "package.json")] = string(manifest)
	for path, body := range files {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	probeJSON, err := json.Marshal(probe)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", "harness.mjs")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+home, "PROBE_IDS="+string(probeJSON))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("running the shipped extension: %v\n%s", err, out)
	}
	var run piCodexRun
	if err := json.Unmarshal(out, &run); err != nil {
		t.Fatalf("decoding %s: %v", out, err)
	}
	return run
}

// requireTheLoginAndAddressStay fails when a registration lost what makes it the subscription
// provider: its address, its api and its login.
func requireTheLoginAndAddressStay(t *testing.T, run piCodexRun) {
	t.Helper()
	if run.Name != "openai-codex" || run.BaseURL != "https://chatgpt.com/backend-api" ||
		run.API != "openai-codex-responses" || !run.OAuth {
		t.Errorf("registration = %+v, want openai-codex at the subscription's address on "+
			"openai-codex-responses, with yolo's login", run)
	}
}

// ON THE SHIPPED codex PROFILE, WHOSE SWITCH IS ON BY DEFAULT, THE REGISTRATION REFUSES: its
// streamSimple ends an unlisted model's turn with yolo's refusal before any stream starts, and
// hands a listed one, a 1M variant included, to pi's own openai-codex provider with the very
// options pi resolved, the login's token among them. Deleting the wrapper, or the derive's
// `enforce`, fails this.
func TestPiOpenAIAuthExtensionRefusesAModelOutsideTheCodexList(t *testing.T) {
	list := renderedCodexModels(t, nil, `{"pi":"codex"}`)
	run := runPiCodexRefusal(t, list, piCodexStub(false), "", "gpt-6.1-sol", "gpt-6-astra[1m]", "gpt-5.5")
	requireTheLoginAndAddressStay(t, run)
	if !strings.Contains(strings.Join(run.Keys, ","), "streamSimple") {
		t.Fatalf("the registration carries %v with the codex profile's switch on: no streamSimple, so "+
			"`pi --model openai-codex/<unlisted id>` still runs", run.Keys)
	}
	if len(run.Models) == 0 || run.Models[0] != "gpt-6.1-sol" {
		t.Fatalf("registered models = %v, want the declared list, gpt-6.1-sol first", run.Models)
	}
	if len(run.Attempts) != 3 {
		t.Fatalf("attempts = %+v, want three", run.Attempts)
	}
	for i, id := range []string{"gpt-6.1-sol", "gpt-6-astra[1m]"} {
		if a := run.Attempts[i]; a.Error != "" || a.Returned != "builtin-stream" {
			t.Errorf("listed %s got %+v, want pi's own openai-codex stream", id, a)
		}
	}
	refused := run.Attempts[2].Error
	for _, want := range []string{`"openai-codex/gpt-5.5"`, "not on yolo's model list", strings.Join(run.Models, ", "),
		`"enforce_models": false`} {
		if !strings.Contains(refused, want) {
			t.Errorf("the unlisted model's refusal = %q, want it to say %s", refused, want)
		}
	}
	if len(run.Calls) != 2 {
		t.Fatalf("delegate calls = %+v, want the two listed models alone", run.Calls)
	}
	for _, c := range run.Calls {
		if c.Via != "builtin" || !c.SameOptions || c.OptionAPIKey != "the-subscription-access-token" {
			t.Errorf("the delegate got %+v, want pi's built-in stream with the options pi resolved, "+
				"untouched: that is how the subscription login reaches the request", c)
		}
	}
	if len(run.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", run.Warnings)
	}
}

// NO PROFILE SELECTED STILL REFUSES: the list is registered on every launch, since pi can switch
// to openai-codex after a /login, and a launch with no profile reads the switch's default, on
// (MM-D5), as pi's other lists do (MM-D21's piEnforceFor).
func TestPiOpenAIAuthExtensionRefusesWithNoProfileSelected(t *testing.T) {
	list := renderedCodexModels(t, nil, `{}`)
	var file map[string]any
	if err := json.Unmarshal(list, &file); err != nil {
		t.Fatal(err)
	}
	if file["enforce"] != true {
		t.Fatalf("pi/codex-models with no profile = %s, want enforce true, the switch's default", list)
	}
	run := runPiCodexRefusal(t, list, piCodexStub(false), "", "gpt-5.5")
	if len(run.Attempts) != 1 || run.Attempts[0].Error == "" {
		t.Errorf("attempts = %+v, want gpt-5.5 refused", run.Attempts)
	}
}

// WITH THE SWITCH OFF the registration is today's: the list is still pi's exact menu, the login
// and the address are unchanged, and nothing refuses.
func TestPiOpenAIAuthExtensionRefusesNothingWithTheSwitchOff(t *testing.T) {
	off := false
	user := map[string]packload.UserProfile{"codex-open": {Provider: "openai-codex", EnforceModels: &off}}
	list := renderedCodexModels(t, user, `{"pi":"codex-open"}`)
	var file map[string]any
	if err := json.Unmarshal(list, &file); err != nil {
		t.Fatal(err)
	}
	if file["enforce"] != false {
		t.Fatalf("pi/codex-models under a profile turning enforce_models off = %s, want enforce false", list)
	}
	run := runPiCodexRefusal(t, list, piCodexStub(false), "")
	requireTheLoginAndAddressStay(t, run)
	if strings.Contains(strings.Join(run.Keys, ","), "streamSimple") {
		t.Errorf("the registration carries %v with enforce_models off, want no streamSimple", run.Keys)
	}
	if len(run.Models) == 0 {
		t.Error("the registration lost the list with enforce_models off; the menu stays exact")
	}
}

// NO LIST, NOTHING TO REFUSE AGAINST: a missing or empty file registers no models, which leaves
// pi's own openai-codex catalog in place, and a refusal there would turn away pi's own models.
func TestPiOpenAIAuthExtensionRefusesNothingWithoutAList(t *testing.T) {
	for name, raw := range map[string]string{"empty": `{"enforce":true}`, "no models": `{"models":[],"enforce":true}`} {
		t.Run(name, func(t *testing.T) {
			run := runPiCodexRefusal(t, []byte(raw), piCodexStub(false), "")
			requireTheLoginAndAddressStay(t, run)
			if strings.Contains(strings.Join(run.Keys, ","), "streamSimple") || len(run.Models) != 0 {
				t.Errorf("a list-less registration carries %v and models %v, want neither", run.Keys, run.Models)
			}
		})
	}
}

// WITHOUT pi's BUILT-IN openai-codex PROVIDER the wrapper hands a listed model to pi's api
// registry for openai-codex-responses, where pi itself sends a registration's models when no
// built-in serves them.
func TestPiOpenAIAuthExtensionDelegatesToPisAPIRegistryWithoutABuiltIn(t *testing.T) {
	list := renderedCodexModels(t, nil, `{"pi":"codex"}`)
	compat := `
import { calls } from "./all.js";
export function getApiProvider(api) {
	return { api, streamSimple: (model, context, options) => { calls.push({ via: "registry:" + api, model: model.id, options }); return "registry-stream"; } };
}
`
	run := runPiCodexRefusal(t, list, piCodexStub(true), compat, "gpt-6.1-sol", "gpt-5.5")
	if len(run.Attempts) != 2 || run.Attempts[0].Returned != "registry-stream" || run.Attempts[1].Error == "" {
		t.Fatalf("attempts = %+v, want the listed model to pi's registry and gpt-5.5 refused", run.Attempts)
	}
	if len(run.Calls) != 1 || run.Calls[0].Via != "registry:openai-codex-responses" || !run.Calls[0].SameOptions {
		t.Errorf("delegate calls = %+v, want gpt-6.1-sol to pi's openai-codex-responses stream with pi's options", run.Calls)
	}
}

// A STREAM THAT CANNOT BE FOUND LEAVES THE EXACT MENU ALONE, and pi says once that it cannot
// refuse there, rather than registering a wrapper with nothing to hand a listed model to, which
// would break every request, or refusing in silence nowhere.
func TestPiOpenAIAuthExtensionSaysWhenItCannotRefuse(t *testing.T) {
	list := renderedCodexModels(t, nil, `{"pi":"codex"}`)
	run := runPiCodexRefusal(t, list, piCodexStub(true), "")
	requireTheLoginAndAddressStay(t, run)
	if strings.Contains(strings.Join(run.Keys, ","), "streamSimple") || len(run.Models) == 0 {
		t.Errorf("registration keys %v, models %v: want the list alone, with no streamSimple", run.Keys, run.Models)
	}
	var said bool
	for _, w := range run.Warnings {
		if strings.Contains(w, "openai-codex") && strings.Contains(w, "cannot refuse") {
			said = true
		}
	}
	if !said {
		t.Errorf("warnings = %v, want one saying pi cannot refuse outside openai-codex's list", run.Warnings)
	}
}

// ONE REFUSAL, TWO FILES. pi loads every .js file in its extensions directory as an extension,
// so each of the two carries a copy of the refusal's wording rather than a shared module; a copy
// edited alone would word one agent's refusal two ways (MM-D23).
func TestPisTwoRefusalsAreWordedAlike(t *testing.T) {
	p := shippedPiPack(t)
	fn := regexp.MustCompile(`(?s)\nfunction refusal\(provider, id, listed\) \{\n.*?\n\}\n`)
	var first string
	for _, file := range []string{"yolo-model-lists.js", "yolo-openai-auth.js"} {
		raw, err := os.ReadFile(filepath.Join(p.Root, "extensions", file))
		if err != nil {
			t.Fatal(err)
		}
		found := fn.FindAllString(string(raw), -1)
		if len(found) != 1 {
			t.Fatalf("%s carries %d refusal functions, want 1", file, len(found))
		}
		if first == "" {
			first = found[0]
		} else if found[0] != first {
			t.Errorf("%s words the refusal differently from yolo-model-lists.js:\n%s\nvs\n%s", file, found[0], first)
		}
	}
}
