package run

// macosuserloopholes_test.go pins the macos-user arm's HOST-SERVICE LIFECYCLE: it starts
// every loophole this machine supports, discloses the execution before it happens, and hands
// the sandbox the endpoint of each service that published.
//
// THE TESTS DRIVE Run(), never startLoopholesDisclosed, and that is the whole point of the
// file. The defect this change closes survived because the only assertions about this arm
// called the callee directly — AGENTS.md's "a test that pins the CALLEE while the CALL SITE is
// unpinned is not a test", found once already in loopholeinert_test.go's own comment. Delete
// the startLoopholesDisclosed call from the macos-user arm and every test below fails.
//
// ⚠ NONE OF THIS HAS EXECUTED ON HARDWARE. The arm runs only on macOS; these tests exercise
// the host half of it on any platform, which is what a unit test can reach. What they cannot
// see is anything the Seatbelt sandbox or the separate uid decides — above all whether the
// sandboxed process can actually OPEN the 0600 endpoint file after macosuser's two `chmod +a`
// ACEs. That needs the self-hosted arm64 runner (docs/plans/runbooks/mac-actions-runner.md).

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// testHostDaemonCmdJSON is a host daemon a unit test may really run: the test binary's own
// `-front-upstream-child` mode (hostservices_test.go), which BINDS A REAL AF_UNIX SOCKET at
// the path yolo substitutes into {socket} and then serves it.
//
// A real socket rather than a `touch`, because the lifecycle waits for a `publishes: "socket"`
// daemon BY CONNECT — bare existence is what a stale file satisfies instantly, which is
// precisely the wait this repo replaced. A fixture that could not be connected to would fail
// the wait and the test would measure the warn-and-continue path instead of the one a real
// service takes. stopLoopholes kills the process group when Run returns, on every exit path.
//
// TestMain dispatches this argv before m.Run(), so the spawned test binary serves the socket
// rather than re-running the suite.
func testHostDaemonCmdJSON(tag string) string {
	return `["` + os.Args[0] + `", "-front-upstream-child", "line", "{socket}", "` + tag + `"]`
}

// writeUserConfigJSON writes an arbitrary user config. packHome has already pointed HOME at a
// temp dir, so the developer's own ~/.config/yolo-jail is never read or written.
func writeUserConfigJSON(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeLocalLoopholePack puts one loophole module in the CONVENTIONAL local pack
// (~/.config/yolo-jail/local), which is how a launch-level test gets a pack of its own:
// config.LoadPacks appends it with no `packs` entry naming it, so Run() stages, loads and
// discloses it exactly as it does a configured pack.
func writeLocalLoopholePack(t *testing.T, home, loopholeName, manifest string) {
	t.Helper()
	mod := filepath.Join(home, ".config", "yolo-jail", "local", "loopholes", loopholeName)
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	body := `{"contributes":[{"kind":"loophole","from":"loopholes/` + loopholeName + `"}]}`
	if err := os.WriteFile(filepath.Join(home, ".config", "yolo-jail", "local", "pack.json"),
		[]byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// macosUserLaunchResult is what one launch is observable as from outside: the env the backend
// handler was given, what the per-jail services dir held AT THAT MOMENT, everything printed,
// and the exit code.
type macosUserLaunchResult struct {
	env *jsonx.OrderedMap
	// published is read INSIDE the handler, which is the only window in which it exists: the
	// launch's deferred stopLoopholes closes each front, and a front's listener Close unlinks
	// the endpoint file — retiring the jail's credential — before Run returns. A test that
	// looked afterwards would measure the teardown and conclude nothing ever started.
	published []string
	out       string
	rc        int
}

// macosUserLaunch runs a real macos-user launch against a stub backend handler.
func macosUserLaunch(t *testing.T, ws string) macosUserLaunchResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	socketsDir := hostServiceSocketsDir(runtime.FromWorkspace(ws), false)
	got := macosUserLaunchResult{}
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _, _ string,
		_ macosuser.HostContext, _ bool, launchEnv *jsonx.OrderedMap, _ []packload.BlockedTool) int {
		got.env = launchEnv
		entries, _ := os.ReadDir(socketsDir)
		for _, e := range entries {
			got.published = append(got.published, e.Name())
		}
		return 0
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketsDir) })
	got.rc = Run(*o)
	got.out = stdout.String() + stderr.String()
	return got
}

// A CONFIG-DECLARED host service starts on this backend and its endpoint reaches the sandbox.
//
// The config route rather than a pack, deliberately: it is the half the inert report used to
// name separately (configInertLines), it carries no origin gate and no staging, so a failure
// here is about the ARM's lifecycle and nothing else.
//
// The assertion is the VALUE, not merely the variable: the sandbox is a process on this
// machine's own filesystem, so what it must be handed is the host path the daemon published —
// hand it the container path (insertHostServiceEnv's jailPath) and it opens nothing.
func TestMacosUserLaunchStartsAConfigDeclaredServiceAndCarriesItsEndpoint(t *testing.T) {
	home := packHome(t)
	ws := t.TempDir()
	writeUserConfigJSON(t, home, `{
	  "packs": [],
	  "loopholes": {
	    "acme-proxy": {"enabled": true, "description": "acme proxy",
	      "command": `+testHostDaemonCmdJSON("acme-config-daemon")+`}
	  }
	}`)

	got := macosUserLaunch(t, ws)
	out := got.out
	if got.rc != 0 {
		t.Fatalf("Run() = %d, want 0\n%s", got.rc, out)
	}
	if got.env == nil {
		t.Fatalf("Run() never reached the macos-user handler:\n%s", out)
	}
	name := hostServiceEnvVar("acme-proxy")
	value, ok := got.env.Get(name)
	if !ok {
		t.Fatalf("the launch env does not carry %s, so the sandbox is never told where its "+
			"service is. Every pack loophole was INERT on this backend until the arm was routed "+
			"through startLoopholesDisclosed; carrying the endpoint is the other half of that.\n%s",
			name, out)
	}
	want := filepath.Join(hostServiceSocketsDir(runtime.FromWorkspace(ws), false),
		"acme-proxy"+paths.ServiceEndpointExt)
	if got, _ := value.(string); got != want {
		t.Errorf("%s = %q, want the HOST path the daemon published (%q). The sandbox reads this "+
			"machine's own filesystem — a jail path names nothing here", name, got, want)
	}
	// And the service really came up, so the assertion above is not about a variable naming a
	// path nothing ever created.
	if leaf := filepath.Base(want); !containsString(got.published, leaf) {
		t.Errorf("no host service published %s while the backend ran (dir held %v); the launch "+
			"carried an endpoint for a daemon that never started:\n%s", leaf, got.published, out)
	}
}

// A PACK-SHIPPED loophole's daemon starts here too, and the launch DISCLOSES it first.
//
// Both halves in one launch because they are one guarantee: AGENTS.md makes the read/exec
// banners the entire trust boundary, so a daemon that starts without its line is not a weaker
// boundary but none at all — and a line printed for a daemon that never starts is the
// overclaim OQ-10 rules out. The spawn is asserted as well as the line, or the ordering claim
// would pass vacuously on a backend that printed and did nothing.
func TestMacosUserLaunchDisclosesAndStartsAPackHostDaemon(t *testing.T) {
	home := packHome(t)
	ws := t.TempDir()
	writeLocalLoopholePack(t, home, "acme-proxy", `{"name": "acme-proxy",
		"description": "acme proxy", "default_enabled": true, "transport": "loopback-tls",
		"host_daemon": {"publishes": "socket", "cmd": `+testHostDaemonCmdJSON("acme-pack-daemon")+`}}`)
	writeUserConfigJSON(t, home, `{"packs": []}`)

	got := macosUserLaunch(t, ws)
	out := got.out
	if got.rc != 0 {
		t.Fatalf("Run() = %d, want 0\n%s", got.rc, out)
	}
	if !strings.Contains(out, "runs pack code on your machine") {
		t.Errorf("a macos-user launch spawned a pack's host daemon without the heading that "+
			"says so. The exec banner IS the trust boundary on the one backend with no "+
			"container around it:\n%s", out)
	}
	if !strings.Contains(out, "acme-pack-daemon") {
		t.Errorf("the disclosure does not name the argv, so the user cannot tell WHAT ran:\n%s", out)
	}
	if !containsString(got.published, "acme-proxy"+paths.ServiceEndpointExt) {
		t.Fatalf("the pack's host daemon never published (dir held %v), so the disclosure above "+
			"is the only thing this test proves — and the loophole is still inert here:\n%s",
			got.published, out)
	}
	if got.env == nil {
		t.Fatalf("Run() never reached the macos-user handler:\n%s", out)
	}
	if _, ok := got.env.Get(hostServiceEnvVar("acme-proxy")); !ok {
		t.Errorf("the pack's service started and the sandbox was not told its endpoint:\n%s", out)
	}
}

// A --dry-run starts NOTHING and says nothing about host execution.
//
// The plan render is the one macos-user invocation that must not cross the spawn boundary: it
// describes a launch rather than performing one, so an exec line here would name daemons this
// invocation never runs — the same overclaim as a disclosure without a spawn, printed by the
// instrument people use to inspect what a launch WOULD do.
func TestMacosUserDryRunStartsNoHostServices(t *testing.T) {
	home := packHome(t)
	ws := t.TempDir()
	writeLocalLoopholePack(t, home, "acme-proxy", `{"name": "acme-proxy",
		"description": "acme proxy", "default_enabled": true, "transport": "loopback-tls",
		"host_daemon": {"publishes": "socket", "cmd": `+testHostDaemonCmdJSON("acme-dryrun-daemon")+`}}`)
	writeUserConfigJSON(t, home, `{"packs": []}`)

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.DryRun = true
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _, _ string,
		_ macosuser.HostContext, _ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool) int {
		return 0
	}
	cname := runtime.FromWorkspace(ws)
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(cname, false)) })

	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run(--dry-run) = %d, want 0\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	out := stdout.String() + stderr.String()
	if strings.Contains(out, "runs pack code on your machine") || strings.Contains(out, "acme-dryrun-daemon") {
		t.Errorf("a dry run announced host execution it never performed:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(hostServiceSocketsDir(cname, false),
		"acme-proxy"+paths.ServiceEndpointExt)); err == nil {
		t.Error("a dry run started a host daemon; the plan render must describe the launch, " +
			"not perform it")
	}
}
