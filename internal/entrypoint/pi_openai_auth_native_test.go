package entrypoint

// pi_openai_auth_native_test.go loads the SHIPPED yolo-openai-auth.js into pi's OWN runtime: the
// installed @earendil-works/pi-coding-agent's createAgentSessionServices, the call pi makes before
// it creates a session, which discovers the extension in the agent directory through pi's own
// loader, applies its registration and runs the availability pass that decides which providers
// /model lists (docs/design/pi-host-openai-auth.md §1.1 step 2, §5 step 3). The node harnesses
// beside it run the extension against stand-ins for pi; only this one proves pi itself counts the
// host route's provider as configured with nothing stored.
//
// Offline: a scratch HOME, a fake `yolo` first on PATH, PI_OFFLINE, and a fetch that refuses and
// records. No session is created and no model is called: the one stream it opens is for a model
// the refusal turns away before pi's own stream runs. It skips where pi's package is not
// installed, which is CI's case, and where the installed pi is too old for what it reads, after
// checking that the extension loads there without an error.

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

	"github.com/mschulkind-oss/yolo-jail/internal/openauthclient"
)

// installedPiPackage returns the directory of the installed @earendil-works/pi-coding-agent, or
// skips. YOLO_TEST_PI_PACKAGE names one directly (a checkout's packages/coding-agent, say); else
// npm's global root under NPM_CONFIG_PREFIX, under the prefix of the node on PATH (npm's default),
// and under ~/.npm-global. npm itself is not run.
func installedPiPackage(t *testing.T) string {
	t.Helper()
	var candidates []string
	if dir := os.Getenv("YOLO_TEST_PI_PACKAGE"); dir != "" {
		candidates = append(candidates, dir)
	}
	var roots []string
	for _, env := range []string{"NPM_CONFIG_PREFIX", "npm_config_prefix"} {
		if prefix := os.Getenv(env); prefix != "" {
			roots = append(roots, filepath.Join(prefix, "lib", "node_modules"))
		}
	}
	if node, err := exec.LookPath("node"); err == nil {
		if real, err := filepath.EvalSymlinks(node); err == nil {
			roots = append(roots, filepath.Join(filepath.Dir(filepath.Dir(real)), "lib", "node_modules"))
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, ".npm-global", "lib", "node_modules"))
	}
	for _, root := range roots {
		candidates = append(candidates, filepath.Join(root, "@earendil-works", "pi-coding-agent"))
	}
	for _, dir := range candidates {
		if _, err := os.Stat(filepath.Join(dir, "dist", "index.js")); err == nil {
			return dir
		}
	}
	t.Skipf("pi's package (@earendil-works/pi-coding-agent) is not installed where this test looks (%v), "+
		"so the shipped extension was not loaded into pi's own runtime; `npm install -g "+
		"@earendil-works/pi-coding-agent`, or YOLO_TEST_PI_PACKAGE=<its directory>, runs it", candidates)
	return ""
}

// piNativeHarness builds pi's session services over HOME's agent directory and reports what pi
// decided about openai-codex: configured, how, which models /model would list, the request auth,
// what an unlisted model's turn ends with, and whether auth.json is the bytes it was.
//
// ⚠ IT READS PI'S DECISION AFTER ONE MORE REFRESH, not the moment the services return. Every
// registration starts a refresh pi does not await, and the one it does await can finish while one
// of those is still running and has superseded it, so under load the snapshot pi returns can
// predate the availability pass that counts this provider (MEASURED 2026-10-04 on pi 1.0.1:
// docs/design/pi-host-openai-auth.md §5.1). The extra awaited refresh settles that; atStart keeps
// what pi showed first, for the log.
//
// ⚠ AN OLDER pi LACKS SOME OF WHAT IT READS, and that is not a fault of the extension: pi 0.81.0
// has no isUsingSubscription, and pi 0.80.10 no registerNativeProvider either, the method pi's
// loader hands a provider object to (MEASURED 2026-10-04). So the harness asks before it reads:
// a pi missing a runtime method listed in piNativeRuntimeMethods reports them in `missing`, with
// its VERSION, after the measurements every pi owes (the extension loads without an error, nothing
// fetched, auth.json untouched), and reads nothing else; runPiNative skips on it.
const piNativeHarness = `
import { readFileSync } from "node:fs";
import { join } from "node:path";
const fetched = [];
globalThis.fetch = async (url) => { fetched.push(String(url)); throw new Error("the test's network is off"); };
const pi = await import(process.env.PI_INDEX);
const version = typeof pi.VERSION === "string" && pi.VERSION.length > 0 ? pi.VERSION : "version unknown";
const absent = (object, names) => names.filter((name) => typeof object?.[name] !== "function");
const agentDir = join(process.env.HOME, ".pi", "agent");
const authPath = join(agentDir, "auth.json");
const before = readFileSync(authPath);
async function measure() {
	if (typeof pi.createAgentSessionServices !== "function") return { missing: ["createAgentSessionServices"] };
	const services = await pi.createAgentSessionServices({ cwd: process.env.WORKDIR, agentDir });
	const diagnostics = (services.diagnostics ?? []).map((d) => d.message);
	const rt = services.modelRuntime;
	const missing = absent(rt, JSON.parse(process.env.PI_RUNTIME_METHODS));
	if (missing.length > 0) return { missing, diagnostics };
	const codexListed = () => rt.getAvailableSnapshot().filter((m) => m.provider === "openai-codex").length;
	const atStart = { configured: rt.hasConfiguredAuth("openai-codex"), listed: codexListed() };
	await rt.refresh({ allowNetwork: false });
	const listed = rt.getAvailableSnapshot().filter((m) => m.provider === "openai-codex").map((m) => m.id);
	const apiKey = await rt.getAuth("openai-codex").then((a) => a?.auth?.apiKey ?? "", (e) => "ERROR " + e.message);
	let refusal = "";
	const model = listed.length > 0 ? rt.getModel("openai-codex", listed[0]) : undefined;
	if (model) refusal = (await rt.completeSimple({ ...model, id: "gpt-5.5" }, { messages: [] })).errorMessage ?? "";
	return {
		atStart, diagnostics,
		configured: rt.hasConfiguredAuth("openai-codex"), oauth: rt.isUsingOAuth("openai-codex"),
		subscription: rt.isUsingSubscription("openai-codex"), name: rt.getProvider("openai-codex")?.name ?? "",
		listed, apiKey, refusal,
	};
}
const run = await measure();
console.log("RESULT " + JSON.stringify({
	...run, version, authUnchanged: Buffer.compare(before, readFileSync(authPath)) === 0, fetched,
}));
`

// piNativeRuntimeMethods is every method piNativeHarness calls on pi's model runtime, and
// registerNativeProvider, which it does not call but without which pi's loader cannot take the
// host route's provider object, so the extension falls back to the ProviderConfig there
// (docs/design/pi-host-openai-auth.md PH-D4) and nothing this file asserts of the host route holds.
var piNativeRuntimeMethods = []string{
	"hasConfiguredAuth", "isUsingOAuth", "isUsingSubscription", "getAvailableSnapshot", "refresh",
	"getAuth", "getModel", "getProvider", "completeSimple", "registerNativeProvider",
}

type piNativeRun struct {
	AtStart struct {
		Configured bool `json:"configured"`
		Listed     int  `json:"listed"`
	} `json:"atStart"`
	Version       string   `json:"version"`
	Missing       []string `json:"missing"`
	Diagnostics   []string `json:"diagnostics"`
	Configured    bool     `json:"configured"`
	OAuth         bool     `json:"oauth"`
	Subscription  bool     `json:"subscription"`
	Name          string   `json:"name"`
	Listed        []string `json:"listed"`
	APIKey        string   `json:"apiKey"`
	Refusal       string   `json:"refusal"`
	AuthUnchanged bool     `json:"authUnchanged"`
	Fetched       []string `json:"fetched"`
	Calls         []string `json:"-"`
}

// runPiNative runs piNativeHarness under the installed pi, with the shipped extension and the
// boot render's codex list in a scratch home whose auth.json holds auth, and the host socket
// variable set or not. The fake client answers as an authenticated broker.
func runPiNative(t *testing.T, pkg string, socket bool, auth string) piNativeRun {
	t.Helper()
	p := shippedPiPack(t)
	source, err := os.ReadFile(filepath.Join(p.Root, "extensions", "yolo-openai-auth.js"))
	if err != nil {
		t.Fatal(err)
	}
	dir, home := t.TempDir(), t.TempDir()
	agent := filepath.Join(home, ".pi", "agent")
	bin := filepath.Join(dir, "bin")
	calls := filepath.Join(dir, "calls")
	for _, d := range []string{filepath.Join(agent, "extensions"), bin, filepath.Join(dir, "work")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string][]byte{
		filepath.Join(agent, "extensions", "yolo-openai-auth.js"):    source,
		filepath.Join(home, filepath.FromSlash(piCodexModelsRel(t))): renderedCodexModels(t, nil, `{"pi":"codex"}`),
		filepath.Join(agent, "auth.json"):                            []byte(auth),
		filepath.Join(dir, "harness.mjs"):                            []byte(piNativeHarness),
	}
	for path, body := range files {
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bin, "yolo"), []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$CALLS"
case "$3" in
  status) printf '{"logged_in":true,"login_required":false}\n' ;;
  token) printf '{"access_token":"broker-access","expires_at":4102444800000,"generation":7}\n' ;;
  *) printf 'unexpected %s\n' "$3" >&2; exit 9 ;;
esac
`), 0o755); err != nil {
		t.Fatal(err)
	}
	node := requireNode(t, "the shipped extension under pi's own runtime")
	methods, err := json.Marshal(piNativeRuntimeMethods)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "harness.mjs")
	cmd.Dir = dir
	// A CLEAN environment: a developer's jail or `yolo host` session carries the socket, an
	// endpoint and YOLO_VERSION, each of which would decide the case.
	cmd.Env = []string{
		"HOME=" + home, "WORKDIR=" + filepath.Join(dir, "work"), "CALLS=" + calls,
		"PATH=" + bin + string(os.PathListSeparator) + filepath.Dir(node) + string(os.PathListSeparator) + "/usr/bin:/bin",
		"PI_INDEX=" + filepath.Join(pkg, "dist", "index.js"), "PI_OFFLINE=1", "PI_TELEMETRY=0",
		"PI_RUNTIME_METHODS=" + string(methods),
	}
	if socket {
		cmd.Env = append(cmd.Env, openauthclient.HostSocketEnv+"="+filepath.Join(dir, "absent.sock"))
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("loading the shipped extension into pi's runtime: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	var run piNativeRun
	for _, line := range strings.Split(stdout.String(), "\n") {
		if raw, ok := strings.CutPrefix(line, "RESULT "); ok {
			if err := json.Unmarshal([]byte(raw), &run); err != nil {
				t.Fatalf("decoding %q: %v", raw, err)
			}
		}
	}
	if raw, err := os.ReadFile(calls); err == nil {
		run.Calls = strings.Split(strings.TrimSpace(string(raw)), "\n")
	}
	if len(run.Diagnostics) != 0 {
		t.Errorf("pi %s reported %v loading the extension", run.Version, run.Diagnostics)
	}
	if len(run.Fetched) != 0 {
		t.Errorf("pi or the extension fetched %v with PI_OFFLINE set", run.Fetched)
	}
	if !run.AuthUnchanged {
		t.Errorf("auth.json changed: yolo writes nothing into the user's pi login file (NC-D37)")
	}
	if len(run.Missing) != 0 {
		t.Skipf("the pi at %s (%s) has no %s, which this test reads, so it checked only that the extension "+
			"loads there without an error and writes nothing; update that pi (`pi update`), or set "+
			"YOLO_TEST_PI_PACKAGE to the directory of a newer @earendil-works/pi-coding-agent",
			pkg, run.Version, strings.Join(run.Missing, ", "))
	}
	if run.AtStart.Configured != run.Configured || run.AtStart.Listed != len(run.Listed) {
		t.Logf("pi's first snapshot (configured %v, %d openai-codex models) predates its settled one: "+
			"the refresh race docs/design/pi-host-openai-auth.md §5.1 records", run.AtStart.Configured, run.AtStart.Listed)
	}
	return run
}

// UNDER `yolo host -- pi` PI ITSELF COUNTS openai-codex CONFIGURED WITH NOTHING STORED, and with
// no host socket it does not. With the socket set and an empty auth.json, pi lists the codex
// profile's models, counts the provider an OAuth subscription (the footer's mark), resolves the
// request to the broker's token, ends an unlisted model's turn with yolo's refusal, and leaves
// auth.json byte for byte. A plain pi over the same home lists none of them and asks nothing.
func TestPiOpenAIAuthUnderPisOwnRuntimeIsConfiguredByTheHostSocketAlone(t *testing.T) {
	pkg := installedPiPackage(t)
	want := codexListEntries(t, renderedCodexModels(t, nil, `{"pi":"codex"}`))

	host := runPiNative(t, pkg, true, "{}")
	if !host.Configured || !host.OAuth || !host.Subscription {
		t.Errorf("with the host socket: configured %v, oauth %v, subscription %v, want all three",
			host.Configured, host.OAuth, host.Subscription)
	}
	if !slices.Equal(host.Listed, want) {
		t.Errorf("with the host socket pi lists %v for openai-codex, want the codex profile's list %v", host.Listed, want)
	}
	if host.Name != "OpenAI Codex" || host.APIKey != "broker-access" {
		t.Errorf("with the host socket: provider %q resolving %q, want \"OpenAI Codex\" on the broker's token",
			host.Name, host.APIKey)
	}
	if !strings.Contains(host.Refusal, `"openai-codex/gpt-5.5" is not on yolo's model list`) {
		t.Errorf("an unlisted model's turn ended with %q, want yolo's refusal", host.Refusal)
	}
	if !slices.Equal(host.Calls, []string{"internal openai-auth-client token"}) {
		t.Errorf("client calls = %q, want one token request, reused for the second", host.Calls)
	}

	plain := runPiNative(t, pkg, false, "{}")
	if plain.Configured || len(plain.Listed) != 0 || len(plain.Calls) != 0 {
		t.Errorf("with no host socket: configured %v, listed %v, client calls %q; want none of them",
			plain.Configured, plain.Listed, plain.Calls)
	}
}

// A LOGIN STORED IN auth.json STILL WINS on the host route: pi resolves a stored credential before
// any key method (pi-ai auth/resolve.js), so a login the user made is the one a request uses, and
// the broker is not asked (docs/design/pi-host-openai-auth.md Appendix A, P8).
func TestPiOpenAIAuthUnderPisOwnRuntimeAStoredLoginWinsOverTheHostRoute(t *testing.T) {
	pkg := installedPiPackage(t)
	stored := `{"openai-codex":{"type":"oauth","access":"own-access","refresh":"own-refresh","expires":4102444800000}}`
	run := runPiNative(t, pkg, true, stored)
	if !run.Configured || run.APIKey != "own-access" || len(run.Calls) != 0 {
		t.Errorf("configured %v resolving %q with client calls %q, want the stored login's token and no call",
			run.Configured, run.APIKey, run.Calls)
	}
}

// codexListEntries is the ids of a pi/codex-models file, in its order.
func codexListEntries(t *testing.T, raw []byte) []string {
	t.Helper()
	var file struct {
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range file.Models {
		ids = append(ids, m.ID)
	}
	if len(ids) == 0 {
		t.Fatalf("the rendered codex list is empty, so nothing here measures anything: %s", raw)
	}
	return ids
}
