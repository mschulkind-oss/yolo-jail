package entrypoint

// pi_model_lists_cooperation_test.go pins ML-D15 (docs/design/model-lists-and-pickers.md): when
// another pi extension registered a provider before yolo's model-lists extension, yolo's
// registration keeps that extension's models and its other keys and adds only yolo's declared
// rows whose ids it lacks; a list the profile enforces still replaces the models and says so; and
// a second registration of yolo's merges with the other extension's models, never with yolo's
// own earlier ones. pi's model runtime is a stand-in that lays each registration's defined keys
// over the previous one, as pi 1.0.1's ModelRuntime.registerProvider does, and applies the queued
// registrations in load order, as pi's bind does, so no pi process starts.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// piRuntimeStub stands in for @earendil-works/pi-coding-agent's ModelRuntime: its
// registerProvider merges defined keys over the previous registration, and
// getRegisteredProviderConfig returns the registration it holds.
const piRuntimeStub = `
export class ModelRuntime {
	constructor() { this.providers = new Map(); }
	registerProvider(id, config) {
		const effective = { ...this.providers.get(id) };
		for (const [key, value] of Object.entries(config)) if (value !== undefined) effective[key] = value;
		this.providers.set(id, effective);
	}
	getRegisteredProviderConfig(id) { return this.providers.get(id); }
}
`

// piCooperationHarness queues $PRIOR's registrations (a pack extension loaded before yolo's, each
// given a streamSimple of its own), loads yolo's extension $YOLO_LOADS times, applies every queued
// registration to one runtime in that order (yolo's first when $YOLO_FIRST is set), fires session_start, and prints each provider's
// registration as the runtime holds it.
const piCooperationHarness = `
import extension from "./extension.mjs";
import { ModelRuntime } from "@earendil-works/pi-coding-agent";
const runtime = new ModelRuntime();
const theirs = [], ours = [];
for (const [name, config] of JSON.parse(process.env.PRIOR || "[]")) {
	theirs.push([name, { ...config, streamSimple: () => "pack-stream" }]);
}
const handlers = [], warnings = [];
for (let i = 0; i < Number(process.env.YOLO_LOADS || "1"); i++) {
	await extension({
		registerProvider(name, config) { ours.push([name, config]); },
		on(event, handler) { if (event === "session_start") handlers.push(handler); },
	});
}
const queued = process.env.YOLO_FIRST ? [...ours, ...theirs] : [...theirs, ...ours];
for (const [name, config] of queued) runtime.registerProvider(name, config);
for (const h of handlers) h({}, { hasUI: true, ui: { notify: (message) => warnings.push(message) } });
const providers = {};
for (const [name, config] of runtime.providers) {
	providers[name] = {
		keys: Object.keys(config).sort(),
		models: config.models ?? [],
		stream: typeof config.streamSimple === "function" ? (() => {
			try { return String(config.streamSimple({ id: "not-listed", api: config.api }, { messages: [] }, {})); }
			catch (e) { return "refused"; }
		})() : "",
	};
}
console.log(JSON.stringify({ providers, warnings }));
`

type piCooperationRun struct {
	Providers map[string]struct {
		Keys   []string         `json:"keys"`
		Models []map[string]any `json:"models"`
		Stream string           `json:"stream"`
	} `json:"providers"`
	Warnings []string `json:"warnings"`
}

// runPiCooperation runs the SHIPPED yolo-model-lists.js through piCooperationHarness, with lists
// at the pi/model-lists surface's path, prior registered first, and yolo's extension loaded loads
// times.
func runPiCooperation(t *testing.T, lists, prior string, loads string, allJS string) piCooperationRun {
	t.Helper()
	return runPiCooperationOrdered(t, lists, prior, loads, allJS, false)
}

// runPiCooperationOrdered is runPiCooperation with yolo's registrations applied before prior's
// when yoloFirst is set.
func runPiCooperationOrdered(t *testing.T, lists, prior string, loads string, allJS string, yoloFirst bool) piCooperationRun {
	t.Helper()
	p := shippedPiPack(t)
	source, err := os.ReadFile(movedShippedFile(t, p, ".pi/agent/extensions/yolo-model-lists.js"))
	if err != nil {
		t.Fatal(err)
	}
	dir, home := t.TempDir(), t.TempDir()
	dest := filepath.Join(home, filepath.FromSlash(piModelListsRel(t)))
	ai := filepath.Join(dir, "node_modules", "@earendil-works", "pi-ai")
	agent := filepath.Join(dir, "node_modules", "@earendil-works", "pi-coding-agent")
	for _, d := range []string{filepath.Dir(dest), ai, agent} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for path, body := range map[string]string{
		filepath.Join(dir, "extension.mjs"): string(source),
		filepath.Join(dir, "harness.mjs"):   piCooperationHarness,
		dest:                                lists,
		filepath.Join(ai, "package.json"): `{"name":"@earendil-works/pi-ai","type":"module",` +
			`"exports":{"./providers/all":"./all.js","./compat":"./compat.js"}}`,
		filepath.Join(ai, "all.js"):          allJS,
		filepath.Join(ai, "compat.js"):       piCompatStub,
		filepath.Join(agent, "package.json"): `{"name":"@earendil-works/pi-coding-agent","type":"module","exports":{".":"./index.js"}}`,
		filepath.Join(agent, "index.js"):     piRuntimeStub,
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(requireNode(t, "the pi model-lists extension"), "harness.mjs")
	cmd.Dir = dir
	cmd.Env = append(environWithout("PI_CODING_AGENT_DIR"), "HOME="+home, "PRIOR="+prior, "YOLO_LOADS="+loads)
	if yoloFirst {
		cmd.Env = append(cmd.Env, "YOLO_FIRST=1")
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("running the shipped extension: %v\n%s", err, out)
	}
	var run piCooperationRun
	if err := json.Unmarshal(out, &run); err != nil {
		t.Fatalf("decoding %s: %v", out, err)
	}
	return run
}

func modelIDs(models []map[string]any) []string {
	ids := make([]string, 0, len(models))
	for _, m := range models {
		id, _ := m["id"].(string)
		ids = append(ids, id)
	}
	return ids
}

// bedrockCompletionLists is a keep_catalog list for pi's own Bedrock client: Opus, which pi's
// catalog knows, and a row the catalog lacks.
const bedrockCompletionLists = `{"providers":{"amazon-bedrock":{"models":[
	{"id":"global.anthropic.claude-opus-5-5","contextWindow":1000000},
	{"id":"global.anthropic.claude-sonnet-5","name":"Sonnet 5","contextWindow":200000}],
	"enforce":false,"keep_catalog":true}}}`

// packBedrockPrior is a pack extension's own amazon-bedrock registration: a long-context variant
// row and its own Opus row, with its own request hook (the harness adds a streamSimple).
const packBedrockPrior = `[["amazon-bedrock",{"api":"bedrock-converse-stream","models":[
	{"id":"global.anthropic.claude-opus-5-5[1m]","name":"Pack Opus 1M","contextWindow":1000000},
	{"id":"global.anthropic.claude-opus-5-5","name":"Pack Opus","contextWindow":272000}]}]]`

// ML-D15: AN EXTENSION'S EARLIER REGISTRATION KEEPS ITS ROWS AND ITS HOOK, AND YOLO ADDS ONLY
// ITS DECLARED ROWS THE EXTENSION LACKS. pi's catalog filler (nova-pro) is not added: the
// extension's list replaced pi's catalog by its own choice, and yolo only completes what it
// declares. A second load of yolo's extension merges with the extension's rows again, not with
// yolo's first merge, so nothing doubles.
func TestPiModelListsKeepAnEarlierExtensionsRegistration(t *testing.T) {
	for _, loads := range []string{"1", "2"} {
		run := runPiCooperation(t, bedrockCompletionLists, packBedrockPrior, loads, piBedrockCatalogStub)
		got := run.Providers["amazon-bedrock"]
		want := []string{"global.anthropic.claude-opus-5-5[1m]", "global.anthropic.claude-opus-5-5", "global.anthropic.claude-sonnet-5"}
		if ids := modelIDs(got.Models); !reflect.DeepEqual(ids, want) {
			t.Fatalf("loads=%s: registered ids = %v, want the extension's two rows then yolo's sonnet", loads, ids)
		}
		if got.Models[1]["name"] != "Pack Opus" || got.Models[1]["contextWindow"] != float64(272000) {
			t.Errorf("loads=%s: opus = %v, want the extension's own row, untouched", loads, got.Models[1])
		}
		if got.Models[2]["name"] != "Sonnet 5" {
			t.Errorf("loads=%s: sonnet = %v, want yolo's declared row", loads, got.Models[2])
		}
		if got.Stream != "pack-stream" || got.Keys == nil || !strings.Contains(strings.Join(got.Keys, ","), "api") {
			t.Errorf("loads=%s: registration keys %v, stream %q: want the extension's api and request hook kept",
				loads, got.Keys, got.Stream)
		}
		if len(run.Warnings) != 0 {
			t.Errorf("loads=%s: warnings = %v, want none for a list nothing enforces", loads, run.Warnings)
		}
	}
}

// With no earlier registration the merge changes nothing: yolo's list is pi's catalog with each
// row merged in, as ML-D12 built it.
func TestPiModelListsWithNoEarlierRegistrationAreUnchanged(t *testing.T) {
	run := runPiCooperation(t, bedrockCompletionLists, "[]", "1", piBedrockCatalogStub)
	got := run.Providers["amazon-bedrock"]
	want := []string{"global.anthropic.claude-opus-5-5", "amazon.nova-pro", "global.anthropic.claude-sonnet-5"}
	if ids := modelIDs(got.Models); !reflect.DeepEqual(ids, want) {
		t.Fatalf("registered ids = %v, want %v", ids, want)
	}
	if levels, _ := got.Models[0]["thinkingLevelMap"].(map[string]any); levels["xhigh"] != "xhigh" {
		t.Errorf("opus = %v, want the catalog's facts under the row", got.Models[0])
	}
}

// AN ENFORCED LIST STILL REPLACES, AND SAYS SO: the profile asked pi to refuse outside yolo's
// list (MM-D21), so the extension's models and hook give way to yolo's list and its refusal, and
// the user is told how to keep the extension's models.
func TestPiEnforcedModelListReplacesAnEarlierRegistrationAndSaysSo(t *testing.T) {
	lists := `{"providers":{"zai":{"models":[{"id":"glm-5.3"}],"enforce":true}}}`
	prior := `[["zai",{"api":"openai-completions","models":[{"id":"zai-extra","name":"Extra"}]}]]`
	run := runPiCooperation(t, lists, prior, "1", piZaiCatalogStub)
	got := run.Providers["zai"]
	if ids := modelIDs(got.Models); !reflect.DeepEqual(ids, []string{"glm-5.3"}) {
		t.Errorf("registered ids = %v, want yolo's enforced list alone", ids)
	}
	if got.Stream != "refused" {
		t.Errorf("an unlisted model's stream = %q, want yolo's refusal", got.Stream)
	}
	if len(run.Warnings) != 1 || !strings.Contains(run.Warnings[0], "zai") ||
		!strings.Contains(run.Warnings[0], `"enforce_models": false`) {
		t.Errorf("warnings = %v, want one naming zai and the switch that keeps the extension's models", run.Warnings)
	}

	// With nothing registered before, the enforced list says nothing, and yolo's own earlier
	// registration (a reload) is not another extension's: THEIRS marks it as yolo's.
	for _, loads := range []string{"1", "2"} {
		if run := runPiCooperation(t, lists, "[]", loads, piZaiCatalogStub); len(run.Warnings) != 0 {
			t.Errorf("loads=%s: warnings with no earlier registration = %v, want none", loads, run.Warnings)
		}
	}
}

// piCopilotCatalogStub stands in for pi-ai's catalog on github-copilot, whose models span more
// than one pi api: claude-sol runs on anthropic-messages and gpt-6.1-sol on openai-responses.
const piCopilotCatalogStub = `
const rows = [
	{ id: "claude-sol", name: "Catalog Claude", api: "anthropic-messages", provider: "github-copilot",
		baseUrl: "https://copilot.example", headers: { "Editor-Version": "vscode/1" }, input: ["text"],
		contextWindow: 200000, maxTokens: 32000, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } },
	{ id: "gpt-6.1-sol", name: "Catalog GPT", api: "openai-responses", provider: "github-copilot",
		baseUrl: "https://copilot.example", headers: { "Editor-Version": "vscode/1" }, input: ["text"],
		contextWindow: 400000, maxTokens: 64000, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } },
];
export const calls = [];
export function getBuiltinModel(provider, id) {
	return provider === "github-copilot" ? rows.find((m) => m.id === id) : undefined;
}
export function getBuiltinModels(provider) { return provider === "github-copilot" ? rows : []; }
export function builtinProviders() { return []; }
`

// AN ENFORCED LIST YOLO CANNOT ENFORCE MERGES (ML-D15): a narrowed github-copilot list spanning
// two pi apis gets no refusing stream, so taking another extension's rows away would cost the
// user its long-context copies and buy no refusal. yolo keeps that extension's rows in either
// load order, adds its declared rows the extension lacks, and says only that it cannot refuse.
func TestPiUnenforceableModelListKeepsAnotherExtensionsRows(t *testing.T) {
	lists := `{"providers":{"github-copilot":{"models":[{"id":"claude-sol"},{"id":"gpt-6.1-sol"}],"enforce":true}}}`
	prior := `[["github-copilot",{"models":[
		{"id":"gpt-6.1-sol[1m]","name":"GPT 1M","api":"openai-responses","wireId":"gpt-6.1-sol","contextWindow":1000000},
		{"id":"gpt-6.1-sol","name":"Pack GPT","api":"openai-responses","contextWindow":400000}]}]]`
	for _, yoloFirst := range []bool{false, true} {
		run := runPiCooperationOrdered(t, lists, prior, "1", piCopilotCatalogStub, yoloFirst)
		ids := modelIDs(run.Providers["github-copilot"].Models)
		for _, want := range []string{"gpt-6.1-sol[1m]", "gpt-6.1-sol"} {
			if !slices.Contains(ids, want) {
				t.Errorf("yoloFirst=%v: registered ids = %v, want the extension's %s kept", yoloFirst, ids, want)
			}
		}
		if !yoloFirst {
			if want := []string{"gpt-6.1-sol[1m]", "gpt-6.1-sol", "claude-sol"}; !reflect.DeepEqual(ids, want) {
				t.Errorf("registered ids = %v, want the extension's rows then yolo's claude-sol", ids)
			}
		}
		for _, w := range run.Warnings {
			if strings.Contains(w, "replaced") {
				t.Errorf("yoloFirst=%v: warning %q: nothing was replaced", yoloFirst, w)
			}
		}
		if len(run.Warnings) != 1 || !strings.Contains(run.Warnings[0], "cannot refuse") {
			t.Errorf("yoloFirst=%v: warnings = %v, want the one saying pi cannot refuse outside the list", yoloFirst, run.Warnings)
		}
	}
}
