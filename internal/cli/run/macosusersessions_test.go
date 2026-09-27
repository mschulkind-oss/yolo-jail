package run

// macosusersessions_test.go pins the macos-user backend's HOST-SERVICES SESSIONS: two sandboxed
// sessions of ONE workspace each get a host-services dir of their own, so one session's exit
// never removes or invalidates an endpoint the other is using
// (docs/design/host-daemon-ownership.md#OQ-HD10, "The second run, MEASURED").
//
// THE DEFECT, measured on a Mac (scheduled macos-user run 36319436117 at f937d0fd): both sessions
// published into the one per-workspace dir the workspace's container name selects, the second
// session's front REPLACED the first one's endpoint file, and when the shorter session exited its
// teardown removed the whole dir. The longer session's jail was left with no endpoint file, and
// even had the file stayed, it named the dead session's front.
//
// THE TESTS DRIVE Run(), the real macos-user arm, never startLoopholesMatching: the arm's own
// start and deferred teardown are what shared the dir, so a test of the callee alone would pass
// with the arm still sharing it. The second session is a second Run() made from inside the
// first one's backend stub, which is the only point at which the first session is fully up: its
// services started, its sandbox (here, the stub) running, and its teardown not yet begun.

import (
	"bufio"
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// writeLocalLoopholeModules puts several loophole modules in the conventional local pack
// (~/.config/yolo-jail/local), the way writeLocalLoopholePack puts one.
func writeLocalLoopholeModules(t *testing.T, home string, manifests map[string]string) {
	t.Helper()
	root := filepath.Join(home, ".config", "yolo-jail", "local")
	var contributions []string
	for name, manifest := range manifests {
		mod := filepath.Join(root, "loopholes", name)
		if err := os.MkdirAll(mod, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(mod, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		contributions = append(contributions, `{"kind":"loophole","from":"loopholes/`+name+`"}`)
	}
	body := `{"contributes":[` + strings.Join(contributions, ",") + `]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// frontReply sends one line through a published endpoint and returns the reply line. Unlike
// dialFrontLine it reports rather than failing the test, because it is called from inside a
// launch's backend stub, where a t.Fatalf would unwind through Run's teardown mid-assertion.
func frontReply(endpointPath, req string) (string, error) {
	conn, err := svcendpoint.DialLocal(endpointPath, 3*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte(req + "\n")); err != nil {
		return "", err
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	return strings.TrimSpace(line), err
}

// sessionEndpoints is what one session's sandbox is told: the endpoint file of each service.
type sessionEndpoints struct {
	broker, proxy string
}

func endpointsIn(env *jsonx.OrderedMap, broker, proxy string) sessionEndpoints {
	get := func(name string) string {
		if env == nil {
			return ""
		}
		v, _ := env.Get(hostServiceEnvVar(name))
		s, _ := v.(string)
		return s
	}
	return sessionEndpoints{broker: get(broker), proxy: get(proxy)}
}

// TestAMacosUserSessionsExitLeavesAConcurrentSessionsEndpointsWorking is OQ-HD10's measured
// defect as a unit test: session A of a workspace is up, session B of the same workspace starts
// and exits, and A's jail must still reach both of its services through the endpoints it was
// handed. Two services, because the defect had two halves:
//
//   - a HOST-SCOPED service (the claude-oauth-broker's shape): one daemon on the machine, one
//     front per session. The daemon is a fixture that is already running, so neither launch
//     spawns it and the test is about the fronts and their endpoint files alone.
//   - a PER-JAIL fronted service: one daemon per session behind its own upstream socket, which
//     the shared dir's hash also keyed, so B's spawn unlinked A's upstream socket and B's
//     teardown retired it.
func TestAMacosUserSessionsExitLeavesAConcurrentSessionsEndpointsWorking(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("binds host-wide singleton paths and spawns a host daemon")
	}
	home := packHome(t)
	ws := t.TempDir()
	cname := yoloruntime.FromWorkspace(ws)

	const broker, proxy = "yjtest-hd10-broker", "yjtest-hd10-proxy"
	singletonFixture(t, broker)
	stamp := paths.HostSingletonPIDFile(broker) + ".capability"
	if err := os.WriteFile(stamp, []byte("fronted-preamble-v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(stamp) })
	spawned := filepath.Join(t.TempDir(), "spawned")
	writeLocalLoopholeModules(t, home, map[string]string{
		broker: `{"name": "` + broker + `", "description": "host-wide fixture",
			"default_enabled": true, "transport": "loopback-tls",
			"host_daemon": {"publishes": "socket", "scope": "host",
				"cmd": ["/bin/touch", "` + spawned + `"]}}`,
		proxy: `{"name": "` + proxy + `", "description": "per-jail fixture",
			"default_enabled": true, "transport": "loopback-tls",
			"host_daemon": {"publishes": "socket", "preamble": false,
				"cmd": ` + testHostDaemonCmdJSON("hd10-proxy") + `}}`,
	})
	writeUserConfigJSON(t, home, `{"packs": []}`)

	var outA, errA, outB, errB bytes.Buffer
	var a, b sessionEndpoints
	var bDuring, aAfter [2]string
	var bDuringErr, aAfterErr [2]error
	rcB := -1

	oB := dispatchOptions(t, ws, "macos-user", &outB, &errB, nil)
	oB.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _, _ string,
		_ macosuser.HostContext, _ bool, env *jsonx.OrderedMap, _ []packload.BlockedTool) int {
		b = endpointsIn(env, broker, proxy)
		bDuring[0], bDuringErr[0] = frontReply(b.broker, "ping")
		bDuring[1], bDuringErr[1] = frontReply(b.proxy, "ping")
		return 0
	}
	oA := dispatchOptions(t, ws, "macos-user", &outA, &errA, nil)
	oA.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _, _ string,
		_ macosuser.HostContext, _ bool, env *jsonx.OrderedMap, _ []packload.BlockedTool) int {
		a = endpointsIn(env, broker, proxy)
		// The real backend releases the launch lock it is handed before it starts the agent
		// (AcquireWorkspaceLockFor returns this launch's own hold), so a second terminal's
		// launch runs while this session is up. Do the same, then run that second launch.
		AcquireWorkspaceLockFor(ws, cname, nil, nil)()
		rcB = Run(*oB)
		// B has returned, teardown and all. A is still up, so its endpoints must still answer.
		aAfter[0], aAfterErr[0] = frontReply(a.broker, "ping")
		aAfter[1], aAfterErr[1] = frontReply(a.proxy, "ping")
		return 0
	}
	rcA := Run(*oA)

	logs := "A:\n" + outA.String() + errA.String() + "\nB:\n" + outB.String() + errB.String()
	if rcA != 0 || rcB != 0 {
		t.Fatalf("Run() = %d (A), %d (B), want 0 and 0\n%s", rcA, rcB, logs)
	}
	if a.broker == "" || a.proxy == "" || b.broker == "" || b.proxy == "" {
		t.Fatalf("a session was not handed both endpoints (A %+v, B %+v), so this test says "+
			"nothing about their lifetimes:\n%s", a, b, logs)
	}
	for i, name := range []string{broker, proxy} {
		if bDuringErr[i] != nil || bDuring[i] != "pong" {
			t.Errorf("session B could not reach %s while both sessions were up: %q, %v\n%s",
				name, bDuring[i], bDuringErr[i], logs)
		}
	}
	for i, name := range []string{broker, proxy} {
		if aAfterErr[i] != nil || aAfter[i] != "pong" {
			t.Errorf("after session B exited, session A could not reach %s through the endpoint "+
				"it was handed (%s): reply %q, error %v. One session's exit removed or replaced "+
				"an endpoint another live session of the same workspace uses (OQ-HD10's measured "+
				"defect)\n%s", name, []string{a.broker, a.proxy}[i], aAfter[i], aAfterErr[i], logs)
		}
	}
	if fileExists(spawned) {
		t.Error("a launch SPAWNED the host-scoped daemon although one was already running")
	}
	// A's own teardown still removes A's own dir: per-session state must not become litter.
	if fileExists(filepath.Dir(a.broker)) {
		t.Errorf("session A's host-services dir %s outlived session A", filepath.Dir(a.broker))
	}
	// And no teardown touched the host-wide daemon both sessions were fronting.
	if conn, err := net.DialTimeout("unix", paths.HostSingletonSocket(broker), time.Second); err != nil {
		t.Errorf("the host-wide daemon stopped answering after the sessions ended: %v", err)
	} else {
		_ = conn.Close()
	}
}

// servicesSessionDirs lists the host-services dirs of cname's macos-user sessions that exist now.
func servicesSessionDirs(t *testing.T, cname string) []string {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join(paths.HostServicesBase(false),
		paths.HostServicesSessionPrefix(cname)+"*"))
	if err != nil {
		t.Fatal(err)
	}
	return dirs
}

// plantServicesSession makes a session dir the way openServicesSession does, with an endpoint
// file in it, and says how its liveness question should come out: "live" holds its lock on a
// descriptor of the test's own (another process, as far as flock is concerned), "gone" leaves the
// lock file unheld, and "nolock" creates no lock file at all.
func plantServicesSession(t *testing.T, cname, state string) string {
	t.Helper()
	dir, err := os.MkdirTemp(paths.HostServicesBase(false), paths.HostServicesSessionPrefix(cname))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.WriteFile(filepath.Join(dir, "svc"+paths.ServiceEndpointExt), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if state == "nolock" {
		return dir
	}
	f, err := os.OpenFile(filepath.Join(dir, paths.HostServicesSessionLockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if state == "gone" {
		_ = f.Close()
		return dir
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return dir
}

// TestAMacosUserLaunchCollectsOnlySessionsKnownToBeGone: a session killed without its teardown
// (SIGKILL, OOM) leaves its dir behind, and the next macos-user launch on the machine collects
// it, because nobody holds its lock. Every session dir it cannot prove gone stays: a live
// session's, and one with no lock file yet (a session between creating its dir and its lock).
// The container-shaped dir of the same workspace name is not a session's at all and is never
// the sweep's to touch.
//
// Through Run(), the real macos-user arm: the sweep is part of opening the session, which is the
// spawn's first act, so deleting that call leaves the dead session's dir here and this fails.
func TestAMacosUserLaunchCollectsOnlySessionsKnownToBeGone(t *testing.T) {
	home := packHome(t)
	writeUserConfigJSON(t, home, `{"packs": []}`)
	ws := t.TempDir()
	cname := yoloruntime.FromWorkspace(ws)
	// Another workspace's dead session, too: liveness is each session's own lock, so the sweep
	// is machine-wide, and a workspace nobody launches again would otherwise keep it.
	elsewhere := yoloruntime.FromWorkspace(t.TempDir())

	gone := plantServicesSession(t, cname, "gone")
	goneElsewhere := plantServicesSession(t, elsewhere, "gone")
	live := plantServicesSession(t, cname, "live")
	noLock := plantServicesSession(t, cname, "nolock")
	upstream := frontSocketFile(frontShortHash(gone), "svc")
	if err := os.WriteFile(upstream, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(upstream) })
	jailDir := hostServiceSocketsDir(cname, false)
	if err := os.MkdirAll(jailDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(jailDir) })

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	var during []string
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _, _ string,
		_ macosuser.HostContext, _ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool) int {
		during = servicesSessionDirs(t, cname)
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	for _, d := range []string{gone, goneElsewhere} {
		if fileExists(d) {
			t.Errorf("the launch left %s, a session dir whose lock nobody holds, so its session is "+
				"known to be gone", d)
		}
	}
	if fileExists(upstream) {
		t.Errorf("the dead session's fronted-daemon upstream socket %s outlived its dir", upstream)
	}
	for d, why := range map[string]string{
		live:    "its lock is held, so its session is alive",
		noLock:  "it has no lock file, which is a session starting up as much as a dead one",
		jailDir: "it is a container jail's dir, not a session's",
	} {
		if !fileExists(d) {
			t.Errorf("the launch removed %s, but %s", d, why)
		}
	}
	// The launch's own session existed beside the two survivors while its backend ran.
	if len(during) != 3 {
		t.Errorf("while the backend ran, this workspace had session dirs %v; want the live one, "+
			"the lockless one and this launch's own", during)
	}
}

// TestASessionsUpstreamSocketsAreOutOfAContainerTeardownsReach: a macos-user session keys its
// fronted daemons' upstream sockets by its own dir (frontShortHash), and that key does not begin
// with the workspace's container hash. It must not: a container jail of the same name retires its
// upstream sockets by the glob /tmp/yolo-front-<8hex>-*.sock, so a session keyed
// "<8hex>-<random>" would lose its daemons' sockets to a container teardown it has nothing to do
// with.
func TestASessionsUpstreamSocketsAreOutOfAContainerTeardownsReach(t *testing.T) {
	const cname = "yolo-ws-abcd1234"
	session := filepath.Join(paths.HostServicesBase(false), paths.HostServicesSessionPrefix(cname)+"123456")
	sock := frontSocketFile(frontShortHash(session), "svc")
	if matched, _ := filepath.Match(frontSocketFile(paths.JailShortHash(cname), "*"), sock); matched {
		t.Errorf("session upstream socket %s matches the container teardown's glob for %s", sock, cname)
	}
	// And the container dir keeps its own key, which its sockets were always bound under.
	if got := frontShortHash(hostServiceSocketsDir(cname, false)); got != paths.JailShortHash(cname) {
		t.Errorf("frontShortHash(container dir) = %q, want the jail hash %q", got, paths.JailShortHash(cname))
	}
}

// TestADryRunNamesTheSessionDirsShapeAndItsGrant: a --dry-run still names the credential
// service's endpoint, because the plan is what a human reads to check the sandbox's grant on it
// (run.go says why only that one). A plan render creates no session dir, so the path it names
// has a placeholder where the session's random suffix goes, and it must still be a plan the
// backend accepts: an endpoint with its grant staged (macosuser.PlanInvariants), not the
// per-workspace dir no session publishes into any more.
func TestADryRunNamesTheSessionDirsShapeAndItsGrant(t *testing.T) {
	home := packHome(t)
	ws := t.TempDir()
	cname := yoloruntime.FromWorkspace(ws)
	writeLocalLoopholePack(t, home, openAIAuthBrokerName, `{"name": "`+openAIAuthBrokerName+`",
		"description": "credential service fixture", "default_enabled": true,
		"transport": "loopback-tls",
		"host_daemon": {"publishes": "socket", "scope": "host", "cmd": ["/bin/true"]}}`)
	writeUserConfigJSON(t, home, `{"packs": []}`)

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.DryRun = true
	var env *jsonx.OrderedMap
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _, _ string,
		_ macosuser.HostContext, _ bool, launchEnv *jsonx.OrderedMap, _ []packload.BlockedTool) int {
		env = launchEnv
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run(--dry-run) = %d\n%s%s", rc, stdout.String(), stderr.String())
	}
	key := hostServiceEnvVar(openAIAuthBrokerName)
	v, _ := env.Get(key)
	endpoint, _ := v.(string)
	want := filepath.Join(servicesSessionPlanDir(cname, false), openAIAuthBrokerName+paths.ServiceEndpointExt)
	if endpoint != want {
		t.Fatalf("the dry run names %s=%q, want %q: the shape of this session's own dir", key, endpoint, want)
	}
	if !strings.HasPrefix(filepath.Base(filepath.Dir(endpoint)), paths.HostServicesSessionPrefix(cname)) {
		t.Errorf("the dry run's endpoint dir %s is not a session dir of this workspace", filepath.Dir(endpoint))
	}
	plan := macosuser.BuildRunPlan(ws, jsonx.NewOrderedMap(), []string{"pi"}, []string{"pi"},
		"/opt/yolo", "", "", macosuser.HostContext{}, env, nil, nil)
	if !strings.Contains(plan.EnvFileContent, key+"=") {
		t.Fatalf("the plan's env file does not carry %s, so the invariant below would check "+
			"nothing:\n%s", key, plan.EnvFileContent)
	}
	for _, p := range macosuser.PlanInvariants(plan) {
		if strings.Contains(p, key) {
			t.Errorf("the backend would refuse the dry run's plan over the placeholder endpoint: %s", p)
		}
	}
	if dirs := servicesSessionDirs(t, cname); len(dirs) != 0 {
		t.Errorf("the dry run created session dirs %v", dirs)
	}
}

// TestOnlyTheSpawnOpensAServicesSession: the macos-user session dir has one creator, the spawn,
// for TestOnlyTheSpawnCreatesTheHostServicesDir's reason — every path out of that call reaches the
// arm's teardown. And the arm's teardown must be endServicesSession, the one that removes this
// session's own dir and releases its lock: the teardown it replaced passed the per-workspace dir.
func TestOnlyTheSpawnOpensAServicesSession(t *testing.T) {
	callers := funcsCalling(t, "openServicesSession")
	if !callers["startLoopholesMatching"] || len(callers) != 1 {
		t.Errorf("openServicesSession is called from %v; only startLoopholesMatching may call it", callers)
	}
	if enders := funcsCalling(t, "endServicesSession"); !enders["Run"] {
		t.Errorf("the macos-user arm (in Run) no longer ends its services session (callers: %v), "+
			"so its dir outlives it and its lock is never released", enders)
	}
}

// funcsCalling names every non-test function in this package whose body calls name.
func funcsCalling(t *testing.T, name string) map[string]bool {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && callsIn(fn)[name] {
				out[fn.Name.Name] = true
			}
		}
	}
	return out
}
