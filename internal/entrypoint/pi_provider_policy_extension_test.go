package entrypoint

// pi_provider_policy_extension_test.go pins the active set's provider policy
// (docs/design/simultaneous-auth-and-pack-isolation.md §3) from the shipped declaration to what
// pi does: the pi pack delivers yolo-provider-policy.js into pi's extension discovery directory;
// pi's env derive, run through the same credential scope a launch runs, hands it the policy; and
// the extension, given that environment, puts a blocking provider in place of every provider
// outside the set. The stand-in harness runs the extension under node against a fake pi, so it
// runs wherever node does; the native one loads it into the INSTALLED pi's own runtime and skips
// where pi is not installed. Neither starts a pi process or reaches a network.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestShippedPiPackDeliversTheProviderPolicyExtension(t *testing.T) {
	p := shippedPiPack(t)
	var found bool
	for _, c := range p.Decl.Contributions() {
		if c.Kind == packdecl.KindFiles && c.From == "extensions/yolo-provider-policy.js" &&
			c.Into == ".pi/agent/extensions/yolo-provider-policy.js" {
			found = true
		}
	}
	if !found {
		t.Fatal("the pi pack does not deliver yolo-provider-policy.js to pi's extension directory")
	}
	home := t.TempDir()
	if _, err := RenderHostFiles(p, home, filesReq(t), false); err != nil {
		t.Fatalf("rendering the shipped pi extensions: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".pi", "agent", "extensions", "yolo-provider-policy.js")); err != nil {
		t.Fatalf("pi cannot discover the rendered extension: %v", err)
	}
}

// piPolicyEnv is every variable pi's env derive hands pi for profile (and set), through the
// credential scope a launch composes: the shipped pi pack and its needs, a provider table with zai,
// a provider pi has no built-in for (myproxy) and openai-codex. nil when the derive hands nothing.
func piPolicyEnv(t *testing.T, profile string, set []string) []string {
	t.Helper()
	var packs []*packload.Pack
	for _, name := range []string{"pi", "bedrock", "openai-auth"} {
		p, err := embeddedPack(name)
		if err != nil {
			t.Fatal(err)
		}
		packs = append(packs, p)
	}
	raw, err := jsonx.Decode([]byte(`{
	  "zai":{"endpoints":{"openai":{"base_url":"https://api.z.ai/v4"}}},
	  "myproxy":{"endpoints":{"openai":{"base_url":"http://127.0.0.1:9/v1"}}},
	  "openai-codex":{"endpoints":{"openai-responses":{"base_url":"https://chatgpt.example/codex"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	providers := raw.(*jsonx.OrderedMap)
	resolved := map[string]packload.ResolvedProfile{
		"zai": {Provider: "zai"}, "myproxy": {Provider: "myproxy"}, "codex": {Provider: "openai-codex"},
	}
	in := packload.ScopeInput{Packs: packs, Providers: providers, Resolved: resolved,
		Profiles: map[string]string{}}
	if profile != "" {
		in.Profiles["pi"] = profile
	}
	if len(set) > 0 {
		in.Sets = map[string][]string{"pi": set}
	}
	scope, err := packload.ScopeCredentials(in)
	if err != nil {
		t.Fatal(err)
	}
	d := scope.Agent("pi")
	if d == nil {
		return nil
	}
	var env []string
	for _, v := range d.Shape {
		env = append(env, v.Key+"="+v.Value)
	}
	return env
}

// piPolicyAllStub stands in for pi-ai's providers/all: two built-in providers whose own auth,
// streams and catalog refresh say so when they run, so a harness can tell whether a block left
// any of pi's own code for a provider reachable.
const piPolicyAllStub = `
const builtin = (id, name) => ({
	id, name,
	auth: { apiKey: { name: name + " API key", resolve: async () => { throw new Error("BUILTIN AUTH RAN " + id); } } },
	getModels: () => [{ id: id + "-model", provider: id, api: "openai-completions" }],
	refreshModels: async () => { throw new Error("BUILTIN REFRESH RAN " + id); },
	stream: () => { throw new Error("BUILTIN STREAM RAN " + id); },
	streamSimple: () => { throw new Error("BUILTIN STREAM RAN " + id); },
});
export function builtinProviders() { return [builtin("anthropic", "Anthropic"), builtin("zai", "ZAI")]; }
`

// piPolicyHarness runs the shipped extension against a fake pi and reports, for every provider it
// registered, what each of pi's entry points into that provider does; then fires session_start
// and model_select with a live registry in which another extension replaced one block and a
// provider only the registry knows appeared.
const piPolicyHarness = `
const registrations = [];
const handlers = {};
const warnings = [];
console.warn = (m) => warnings.push(String(m));
const pi = {
	registerProvider: (p, config) => registrations.push(config === undefined ? p : { id: p, config }),
	on: (event, fn) => { (handlers[event] ??= []).push(fn); },
};
const mod = await import("./extension.mjs");
await mod.default(pi);
const settle = async (f) => { try { const v = await f(); return "OK " + JSON.stringify(v ?? null); } catch (e) { return "ERR " + e.message; } };
const describe = async (p) => ({
	id: p.id, name: p.name, native: p.config === undefined,
	models: (p.getModels?.() ?? []).map((m) => m.id),
	available: (p.filterModels?.(p.getModels?.() ?? [], undefined) ?? null),
	refreshModels: typeof p.refreshModels,
	check: await settle(() => p.auth?.apiKey?.check?.({ ctx: {}, signal: AbortSignal.timeout(1000) })),
	resolve: await settle(() => p.auth?.apiKey?.resolve?.({ ctx: {}, credential: { type: "api_key", key: "stored" }, signal: AbortSignal.timeout(1000) })),
	apiKeyLogin: await settle(() => p.auth?.apiKey?.login?.({})),
	oauthLogin: await settle(() => p.auth?.oauth?.login?.({})),
	refresh: await settle(() => p.auth?.oauth?.refresh?.({ type: "oauth", access: "a", refresh: "r", expires: 0 })),
	toAuth: await settle(() => p.auth?.oauth?.toAuth?.({ type: "oauth", access: "a", refresh: "r", expires: 0 })),
	streamSimple: await settle(() => p.streamSimple?.({ id: "m", provider: p.id }, { messages: [] }, {})),
});
const out = { registrations: [], handlers: Object.keys(handlers).sort() };
for (const p of registrations) out.registrations.push(await describe(p));
const reregistered = [];
const current = {
	anthropic: { id: "anthropic", auth: { apiKey: { name: "another extension's" } } },
	late: { id: "late", name: "Late", auth: { apiKey: { name: "late key" } }, getModels: () => [{ id: "late-model" }] },
};
for (const p of registrations) if (p.id === "myproxy") current.myproxy = p;
const ctx = { hasUI: false, modelRegistry: {
	getAll: () => [{ provider: "anthropic" }, { provider: "zai" }, { provider: "myproxy" }, { provider: "router", api: "pi-virtual" }],
	getRegisteredProviderIds: () => ["late"],
	getProvider: (id) => current[id],
	registerProvider: (p) => { reregistered.push(p.id); current[p.id] = p; },
} };
for (const fn of handlers.session_start ?? []) await fn({ type: "session_start" }, ctx);
out.reregistered = [...reregistered].sort();
out.rechecked = {};
for (const event of ["input", "before_agent_start", "turn_start"]) {
	current.anthropic = { id: "anthropic", auth: { apiKey: { name: "another extension's" } } };
	reregistered.length = 0;
	for (const fn of handlers[event] ?? []) await fn({ type: event }, ctx);
	out.rechecked[event] = [...reregistered];
}
out.lateResolve = current.late ? await settle(() => current.late.auth.apiKey.resolve({})) : "none";
for (const fn of handlers.model_select ?? []) await fn({ type: "model_select", model: { provider: "late", api: "openai-completions" } }, ctx);
for (const fn of handlers.model_select ?? []) await fn({ type: "model_select", model: { provider: "late", api: "pi-virtual" } }, ctx);
for (const fn of handlers.model_select ?? []) await fn({ type: "model_select", model: { provider: "zai", api: "openai-completions" } }, ctx);
out.warnings = warnings;
console.log(JSON.stringify(out));
`

type piPolicyProvider struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Native        bool     `json:"native"`
	Models        []string `json:"models"`
	Available     []string `json:"available"`
	RefreshModels string   `json:"refreshModels"`
	Check         string   `json:"check"`
	Resolve       string   `json:"resolve"`
	APIKeyLogin   string   `json:"apiKeyLogin"`
	OAuthLogin    string   `json:"oauthLogin"`
	Refresh       string   `json:"refresh"`
	ToAuth        string   `json:"toAuth"`
	StreamSimple  string   `json:"streamSimple"`
}

type piPolicyRun struct {
	Registrations []piPolicyProvider  `json:"registrations"`
	Handlers      []string            `json:"handlers"`
	Reregistered  []string            `json:"reregistered"`
	Rechecked     map[string][]string `json:"rechecked"`
	LateResolve   string              `json:"lateResolve"`
	Warnings      []string            `json:"warnings"`
}

// runPiPolicyExtension runs the SHIPPED yolo-provider-policy.js under node with env added to a
// clean environment, a models.json naming myproxy, and pi-ai's providers/all stubbed.
func runPiPolicyExtension(t *testing.T, env []string, extra ...map[string]string) piPolicyRun {
	t.Helper()
	p := shippedPiPack(t)
	source, err := os.ReadFile(filepath.Join(p.Root, "extensions", "yolo-provider-policy.js"))
	if err != nil {
		t.Fatal(err)
	}
	dir, home := t.TempDir(), t.TempDir()
	pkg := filepath.Join(dir, "node_modules", "@earendil-works", "pi-ai")
	agent := filepath.Join(home, ".pi", "agent")
	for _, d := range []string{pkg, agent} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for path, body := range map[string]string{
		filepath.Join(dir, "extension.mjs"): string(source),
		filepath.Join(dir, "harness.mjs"):   piPolicyHarness,
		filepath.Join(pkg, "package.json"): `{"name":"@earendil-works/pi-ai","type":"module",` +
			`"exports":{"./providers/all":"./all.js"}}`,
		filepath.Join(pkg, "all.js"): piPolicyAllStub,
		filepath.Join(agent, "models.json"): `{"providers":{"myproxy":{"baseUrl":"http://127.0.0.1:9/v1",` +
			`"api":"openai-completions","apiKey":"${MYPROXY_KEY}","models":[{"id":"m1"}]}}}`,
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, files := range extra {
		for rel, body := range files {
			path := filepath.Join(dir, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	node := requireNode(t, "the pi provider-policy extension")
	cmd := exec.Command(node, "harness.mjs")
	cmd.Dir = dir
	cmd.Env = append([]string{"HOME=" + home, "PATH=" + filepath.Dir(node)}, env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("running the shipped extension: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	var run piPolicyRun
	if err := json.Unmarshal(stdout.Bytes(), &run); err != nil {
		t.Fatalf("decoding %s: %v", stdout.String(), err)
	}
	return run
}

// WITH NO PROFILE THE EXTENSION DOES NOTHING: the derive hands no policy, and the extension
// registers nothing and listens to nothing, so pi keeps its native behavior.
func TestPiProviderPolicyWithNoProfileLeavesPiAlone(t *testing.T) {
	if env := piPolicyEnv(t, "", nil); len(env) != 0 {
		t.Fatalf("pi with no profile got %v from its env derive; the policy must be absent", env)
	}
	run := runPiPolicyExtension(t, nil)
	if len(run.Registrations) != 0 || len(run.Handlers) != 0 {
		t.Errorf("with no policy the extension registered %v and listened to %v, want nothing",
			run.Registrations, run.Handlers)
	}
}

// WITH AN ACTIVE SET, EVERY PROVIDER OUTSIDE IT IS A BLOCK, and nothing of pi's own code for it
// runs: under the policy the derive hands for the set [zai, codex], the built-in anthropic and the
// models.json-only myproxy are each registered as a native provider whose auth, logins and streams
// all refuse with yolo's denial, which counts as configured (so pi reaches the denial rather than
// its own "No API key found"), lists no model as available (so /model hides it), keeps the
// catalog, and fetches none. The set's own providers are never touched. A provider another
// extension put in a block's place, or one only the live registry knows, gets its block at
// session_start, and picking one of its models warns; a virtual model's selection does not.
func TestPiProviderPolicyBlocksEveryProviderOutsideTheSet(t *testing.T) {
	env := piPolicyEnv(t, "zai", []string{"zai", "codex"})
	if len(env) == 0 {
		t.Fatal("the derive handed pi no policy for an active set")
	}
	run := runPiPolicyExtension(t, env)
	var ids []string
	for _, p := range run.Registrations {
		ids = append(ids, p.ID)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"anthropic", "myproxy"}) {
		t.Fatalf("blocked %v, want exactly the providers outside the set [anthropic myproxy]", ids)
	}
	for _, p := range run.Registrations {
		denial := `yolo: provider "` + p.ID + `" is outside this launch's profile set (zai, codex), which allows only openai-codex, zai.`
		if !p.Native {
			t.Errorf("%s: registered as a ProviderConfig, which keeps pi's own auth; want a native provider", p.ID)
		}
		for what, got := range map[string]string{"resolve": p.Resolve, "api-key login": p.APIKeyLogin,
			"oauth login": p.OAuthLogin, "oauth refresh": p.Refresh, "oauth toAuth": p.ToAuth, "streamSimple": p.StreamSimple} {
			if !strings.HasPrefix(got, "ERR "+denial) {
				t.Errorf("%s: %s = %q, want yolo's denial %q", p.ID, what, got, denial)
			}
			if strings.Contains(got, "BUILTIN") {
				t.Errorf("%s: %s ran pi's own code for the provider: %q", p.ID, what, got)
			}
		}
		if !strings.Contains(p.Check, `"type":"api_key"`) {
			t.Errorf("%s: check = %q, want configured, so pi reaches the denial", p.ID, p.Check)
		}
		if p.Available == nil || len(p.Available) != 0 {
			t.Errorf("%s: available models %v, want none, so /model hides the provider", p.ID, p.Available)
		}
		if p.RefreshModels != "undefined" {
			t.Errorf("%s: refreshModels is %s, want none, so no catalog is fetched with its credential", p.ID, p.RefreshModels)
		}
	}
	for _, p := range run.Registrations {
		if p.ID == "anthropic" && (p.Name != "Anthropic" || !slices.Equal(p.Models, []string{"anthropic-model"})) {
			t.Errorf("anthropic's block is %q with %v, want pi's own name and catalog", p.Name, p.Models)
		}
	}
	if !slices.Equal(run.Reregistered, []string{"anthropic", "late"}) {
		t.Errorf("session_start re-blocked %v, want the replaced anthropic and the registry-only late, "+
			"and never the virtual-only router", run.Reregistered)
	}
	for _, event := range []string{"input", "before_agent_start", "turn_start"} {
		if !slices.Equal(run.Rechecked[event], []string{"anthropic"}) {
			t.Errorf("%s re-blocked %v, want the replaced anthropic", event, run.Rechecked[event])
		}
	}
	if !strings.HasPrefix(run.LateResolve, `ERR yolo: provider "late" is outside`) {
		t.Errorf("late's re-registered block resolves %q, want the denial", run.LateResolve)
	}
	if len(run.Warnings) != 1 || !strings.Contains(run.Warnings[0], `provider "late" is outside`) {
		t.Errorf("model_select warned %q, want one warning, for the physical out-of-set model alone", run.Warnings)
	}
}

// AN UNREADABLE POLICY BLOCKS EVERY PROVIDER, the set's included, and the first session says so:
// the launch asked for a restriction, and one the extension cannot read must not run unrestricted.
func TestPiProviderPolicyThatCannotBeReadBlocksEverything(t *testing.T) {
	run := runPiPolicyExtension(t, []string{"YOLO_PI_PROVIDER_POLICY={\"schemaVersion\":1}"})
	if len(run.Warnings) == 0 || !strings.Contains(run.Warnings[0], "is unreadable, so pi may call no provider") {
		t.Errorf("the first session warned %q, want the unreadable-policy denial", run.Warnings)
	}
	var ids []string
	for _, p := range run.Registrations {
		ids = append(ids, p.ID)
		if !strings.Contains(p.Resolve, "is unreadable, so pi may call no provider") {
			t.Errorf("%s resolves %q, want the unreadable-policy denial", p.ID, p.Resolve)
		}
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"anthropic", "myproxy", "zai"}) {
		t.Errorf("blocked %v, want every provider", ids)
	}
}

// A PI TOO OLD TO TAKE A PROVIDER OBJECT IS TOLD, not trusted: its ModelRuntime has no
// registerNativeProvider, so the block would be dropped, and the first session says so and how to
// fix it. A pi that has one says nothing.
func TestPiProviderPolicyWarnsWhereThisPiCannotTakeTheBlock(t *testing.T) {
	env := piPolicyEnv(t, "zai", nil)
	root := func(method string) map[string]string {
		return map[string]string{
			"node_modules/@earendil-works/pi-coding-agent/package.json": `{"name":"@earendil-works/pi-coding-agent",` +
				`"type":"module","exports":{".":"./index.js"}}`,
			"node_modules/@earendil-works/pi-coding-agent/index.js": `export class ModelRuntime { ` + method + ` }`,
		}
	}
	old := runPiPolicyExtension(t, env, root("refresh() {}"))
	if len(old.Warnings) == 0 || !strings.Contains(old.Warnings[0], "too old to take yolo's profile-set provider block") {
		t.Errorf("an old pi's session started with warnings %q, want the unenforceable warning first", old.Warnings)
	}
	current := runPiPolicyExtension(t, env, root("registerNativeProvider() {}"))
	for _, w := range current.Warnings {
		if strings.Contains(w, "too old") {
			t.Errorf("a pi with registerNativeProvider was warned %q", w)
		}
	}
}

// A SET PI CAN CALL NONE OF blocks every provider and says why, not that the policy is
// unreadable: here a set whose one entry has no address pi can reach.
func TestPiProviderPolicyForASetPiCannotCallSaysSo(t *testing.T) {
	run := runPiPolicyExtension(t, []string{`YOLO_PI_PROVIDER_POLICY={"schemaVersion":1,"mode":"allowlist",` +
		`"allowedProviderIds":[],"profiles":["nowhere"]}`})
	if len(run.Registrations) != 3 {
		t.Errorf("blocked %d providers, want every one of the 3", len(run.Registrations))
	}
	for _, p := range run.Registrations {
		if !strings.Contains(p.Resolve, "pi can call none of that set's providers") {
			t.Errorf("%s resolves %q, want the empty-set denial", p.ID, p.Resolve)
		}
	}
}

// piPolicyNativeHarness loads the shipped extension into the installed pi's own runtime
// (createAgentSessionServices, then a session) over a home holding saved logins for providers
// outside the set, and reports what each kind of call ends with. Every network call is refused
// and recorded.
const piPolicyNativeHarness = `
import { readFileSync } from "node:fs";
import { join } from "node:path";
const fetched = [];
globalThis.fetch = async (url) => { fetched.push(String(url)); throw new Error("the test's network is off"); };
const pi = await import(process.env.PI_INDEX);
const agentDir = join(process.env.HOME, ".pi", "agent");
const authPath = join(agentDir, "auth.json");
const before = readFileSync(authPath);
const out = { version: pi.VERSION };
const services = await pi.createAgentSessionServices({ cwd: process.env.WORKDIR, agentDir });
out.diagnostics = (services.diagnostics ?? []).map((d) => d.message);
const rt = services.modelRuntime;
await rt.refresh({ allowNetwork: false });
out.available = [...new Set(rt.getAvailableSnapshot().map((m) => m.provider))].sort();
const first = (p) => rt.getModels().find((m) => m.provider === p);
out.calls = {};
for (const p of ["anthropic", "openai", "openai-codex", "github-copilot", "myproxy"]) {
	const m = first(p);
	out.calls[p] = m ? ((await rt.completeSimple(m, { messages: [{ role: "user", content: "hi", timestamp: Date.now() }] })).errorMessage ?? "NO ERROR") : "NO MODEL";
}
out.zai = await rt.getAuth(first("zai")).then((a) => a?.auth?.apiKey ?? "none", (e) => "ERR " + e.message);
out.login = await rt.login("anthropic", "api_key", { prompt: async () => "sk-new", notify() {} }).then(() => "LOGGED IN", (e) => "ERR " + e.message);
const { session } = await pi.createAgentSession({ cwd: process.env.WORKDIR, agentDir, model: first("anthropic") });
out.session = [];
session.subscribe((e) => {
	if (e.type === "auto_retry_start") out.session.push("RETRY");
	if (e.type === "message_end" && e.message?.role === "assistant") out.session.push(e.message.errorMessage ?? "NO ERROR");
});
await session.prompt("hello").catch((e) => out.session.push("THROW " + e.message));
out.authUnchanged = Buffer.compare(before, readFileSync(authPath)) === 0;
out.fetched = fetched;
console.log("RESULT " + JSON.stringify(out));
`

// UNDER PI'S OWN RUNTIME the block holds for every saved login and every call: with the set [zai]
// and saved logins for anthropic (a key), openai-codex (an OAuth login past expiry) and
// github-copilot (a live OAuth login), an OPENAI_API_KEY in the environment and a models.json row
// for myproxy, each call outside the set ends with yolo's denial and nothing reaches a network,
// the expired login is not refreshed, /login stores nothing, auth.json is byte for byte what it
// was, and a session's prompt ends with the denial once, unretried. zai's own key still resolves.
func TestPiProviderPolicyUnderPisOwnRuntime(t *testing.T) {
	pkg := installedPiPackage(t)
	p := shippedPiPack(t)
	source, err := os.ReadFile(filepath.Join(p.Root, "extensions", "yolo-provider-policy.js"))
	if err != nil {
		t.Fatal(err)
	}
	dir, home := t.TempDir(), t.TempDir()
	agent := filepath.Join(home, ".pi", "agent")
	for _, d := range []string{filepath.Join(agent, "extensions"), filepath.Join(dir, "work")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for path, body := range map[string]string{
		filepath.Join(agent, "extensions", "yolo-provider-policy.js"): string(source),
		filepath.Join(agent, "auth.json"): `{"anthropic":{"type":"api_key","key":"sk-ant-stored"},` +
			`"openai-codex":{"type":"oauth","access":"a","refresh":"r","expires":1000},` +
			`"github-copilot":{"type":"oauth","access":"ghu","refresh":"ghr","expires":4102444800000}}`,
		filepath.Join(agent, "models.json"): `{"providers":{"myproxy":{"baseUrl":"http://127.0.0.1:9/v1",` +
			`"api":"openai-completions","apiKey":"${MYPROXY_KEY}","models":[{"id":"m1"}]}}}`,
		filepath.Join(dir, "harness.mjs"): piPolicyNativeHarness,
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	node := requireNode(t, "the provider-policy extension under pi's own runtime")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "harness.mjs")
	cmd.Dir = dir
	cmd.Env = append([]string{
		"HOME=" + home, "WORKDIR=" + filepath.Join(dir, "work"),
		"PATH=" + filepath.Dir(node) + string(os.PathListSeparator) + "/usr/bin:/bin",
		"PI_INDEX=" + filepath.Join(pkg, "dist", "index.js"), "PI_OFFLINE=1", "PI_TELEMETRY=0",
		"ZAI_API_KEY=zai-key", "OPENAI_API_KEY=openai-key", "MYPROXY_KEY=proxy-key",
	}, piPolicyEnv(t, "zai", nil)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("loading the shipped extension into pi's runtime: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	var run struct {
		Version       string            `json:"version"`
		Diagnostics   []string          `json:"diagnostics"`
		Available     []string          `json:"available"`
		Calls         map[string]string `json:"calls"`
		Zai           string            `json:"zai"`
		Login         string            `json:"login"`
		Session       []string          `json:"session"`
		AuthUnchanged bool              `json:"authUnchanged"`
		Fetched       []string          `json:"fetched"`
	}
	for _, line := range strings.Split(stdout.String(), "\n") {
		if raw, ok := strings.CutPrefix(line, "RESULT "); ok {
			if err := json.Unmarshal([]byte(raw), &run); err != nil {
				t.Fatalf("decoding %q: %v", raw, err)
			}
		}
	}
	denial := func(provider string) string {
		return `yolo: provider "` + provider + `" is outside this launch's profile set (zai), which allows only zai.`
	}
	t.Logf("measured under pi %s at %s", run.Version, pkg)
	if len(run.Diagnostics) != 0 {
		t.Errorf("pi %s reported %v loading the extension", run.Version, run.Diagnostics)
	}
	if !slices.Equal(run.Available, []string{"zai"}) {
		t.Errorf("pi %s lists models of %v, want only the set's zai", run.Version, run.Available)
	}
	for provider, got := range run.Calls {
		if !strings.Contains(got, denial(provider)) {
			t.Errorf("pi %s: a %s call ended with %q, want yolo's denial", run.Version, provider, got)
		}
	}
	if len(run.Calls) != 5 {
		t.Errorf("measured %d providers' calls (%v), want 5", len(run.Calls), run.Calls)
	}
	if run.Zai != "zai-key" {
		t.Errorf("the set's zai resolved %q, want its own key", run.Zai)
	}
	if !strings.Contains(run.Login, denial("anthropic")) {
		t.Errorf("/login for anthropic ended %q, want the denial and nothing stored", run.Login)
	}
	if len(run.Session) != 1 || !strings.Contains(run.Session[0], denial("anthropic")) {
		t.Errorf("a session prompt on an anthropic model ended %q, want the denial once, unretried", run.Session)
	}
	if !run.AuthUnchanged {
		t.Error("auth.json changed: a block must neither refresh nor store a login")
	}
	if len(run.Fetched) != 0 {
		t.Errorf("pi fetched %v under the policy", run.Fetched)
	}
}
