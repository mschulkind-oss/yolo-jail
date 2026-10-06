package run

// macosuserkeeper_test.go pins the KEEPER AT macos-user (docs/design/jail-lifetime-last-session-wins.md
// §9.9; JL-D37 to JL-D45): one keeper per workspace's macos-user key holds what every session of the
// workspace uses outside the sandbox, a joining launch composes against its roster and starts
// nothing, the last session streams its teardown, a key whose keeper died refuses a new arrival and
// is reaped by its last session or `yolo stop`, and a roster of a contract this build does not know
// is refused.
//
// DRIVEN THROUGH Run(), the real macos-user arm, with the sandbox stubbed (MacosUserRun) and the
// keeper run in-process by the package's spawner (inProcessKeeper, or keeperWithSignals below, which
// also hands a test the keeper's signals). The host service is a per-jail fronted fixture daemon in
// the conventional local pack, so an endpoint the sandbox is told really answers; a test about tokens
// and per-agent env files adds claude on cerebras, whose wire bridge is a launch-owned service the
// keeper starts through the stubbed start.
//
// ⚠ NONE OF THIS HAS RUN ON A MAC. What only a Mac settles is in the macos-user job:
// TestMacosUserTwoConcurrentLaunchesOfOneWorkspace (integration/macosuserspawnlock_test.go).

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/nixchildren"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// keeperProxy is the fixture loophole every test here starts: a per-jail daemon behind a front,
// answering "ping" with "pong" (frontUpstreamChildMain).
const keeperProxy = "yjtest-keeper-proxy"

// writeKeeperProxy puts keeperProxy in the conventional local pack under home.
func writeKeeperProxy(t *testing.T, home string) {
	t.Helper()
	writeLocalLoopholeModules(t, home, map[string]string{
		keeperProxy: `{"name": "` + keeperProxy + `", "description": "per-jail fixture",
			"default_enabled": true, "transport": "loopback-tls",
			"host_daemon": {"publishes": "socket", "preamble": false,
				"cmd": ` + testHostDaemonCmdJSON("keeper-proxy") + `}}`,
	})
}

// countKeeperSpawns counts every keeper the package's spawner starts during the test.
func countKeeperSpawns(t *testing.T) *atomic.Int32 {
	t.Helper()
	var n atomic.Int32
	orig := defaultKeeperSpawner
	defaultKeeperSpawner = func(launch *Options, planPath string, progress, lifeline, lock *os.File,
		reserved []*os.File) (func() int, error) {
		n.Add(1)
		return orig(launch, planPath, progress, lifeline, lock, reserved)
	}
	t.Cleanup(func() { defaultKeeperSpawner = orig })
	return &n
}

// keeperLaunch is a macos-user launch of ws, its sandbox stub, and its output.
func keeperLaunch(t *testing.T, ws string, stub func(env *jsonx.OrderedMap) int) (*Options, *lockedBuffer) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	out := &lockedBuffer{}
	o.Stdout, o.Stderr = out, out
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, _ macosuser.HomeOverlay,
		_ macosuser.HostContext, _ bool, env *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
		// The real backend releases the workspace launch lock before its agent (run.AcquireWorkspaceLockFor
		// hands it this launch's own hold), so another terminal's launch can run while this one is up.
		AcquireWorkspaceLockFor(ws, yoloruntime.FromWorkspace(ws), nil, nil)()
		return stub(env)
	}
	return o, out
}

// envString is a launch env's string value of name.
func envString(env *jsonx.OrderedMap, name string) string {
	if env == nil {
		return ""
	}
	v, _ := env.Get(name)
	s, _ := v.(string)
	return s
}

// macosUserKeyOf is the macos-user key of ws's workspace.
func macosUserKeyOf(ws string) string {
	return keeperKey(yoloruntime.FromWorkspace(ws), keeperNotchMacosUser)
}

// assertKeyEnded fails unless nothing of key's keeper is left: no keeper, no roster, no session
// record, and no host-services dir dir.
func assertKeyEnded(t *testing.T, key, dir string) {
	t.Helper()
	if probeKeeper(key) != keeperGone {
		t.Errorf("a keeper still holds %s", key)
	}
	if rec, ok := readKeeperRecord(key); ok {
		t.Errorf("the roster of %s outlived its keeper: %+v", key, rec)
	}
	if s := liveKeyedSessions(key); len(s) > 0 {
		t.Errorf("session records of %s outlived their sessions: %+v", key, s)
	}
	if dir != "" && fileExists(dir) {
		t.Errorf("the host-services dir %s outlived its keeper", dir)
	}
}

// A LAUNCH THAT PLANS A HOST SERVICE SPAWNS ONE KEEPER (JL-D37, JL-D38, JL-D41), which holds the
// front while the sandbox runs, names it in a roster, and ends with the session: the session's quit
// streams the keeper's teardown, and nothing of it is left. Deleting startMacosUserKeeper's call from
// Run's arm, or the endpoint read from the roster, fails this.
func TestAPlannedHostServiceSpawnsOneMacosUserKeeper(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("spawns a host daemon")
	}
	home := packHome(t)
	writeKeeperProxy(t, home)
	writeUserConfigJSON(t, home, `{"packs": []}`)
	ws := t.TempDir()
	key := macosUserKeyOf(ws)
	spawns := countKeeperSpawns(t)

	var endpoint, reply string
	var rec keeperRecord
	var recorded bool
	var alive keeperLiveness
	var sessions []keyedSession
	o, out := keeperLaunch(t, ws, func(env *jsonx.OrderedMap) int {
		endpoint = envString(env, hostServiceEnvVar(keeperProxy))
		reply, _ = frontReply(endpoint, "ping")
		rec, recorded = readKeeperRecord(key)
		alive = probeKeeper(key)
		sessions = liveKeyedSessions(key)
		return 0
	})
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\n%s", rc, out.String())
	}
	if n := spawns.Load(); n != 1 {
		t.Fatalf("the launch spawned %d keepers, want 1\n%s", n, out.String())
	}
	if endpoint == "" || reply != "pong" {
		t.Fatalf("the sandbox was told %q for %s, which answered %q; want the keeper's front answering pong\n%s",
			endpoint, keeperProxy, reply, out.String())
	}
	if alive != keeperAlive || !recorded || !rec.Ready || rec.Contract != keeperRosterContract ||
		rec.Notch != keeperNotchMacosUser || rec.Endpoints[hostServiceEnvVar(keeperProxy)] != endpoint {
		t.Errorf("while the session ran, the keeper (%v) and its roster (%v, %+v) did not name the endpoint the "+
			"sandbox was told", alive, recorded, rec)
	}
	if len(sessions) != 1 || sessions[0].PID != o.Getpid() || !sessions[0].Kept {
		t.Errorf("the session's record during the session = %+v, want this launch's, kept", sessions)
	}
	for _, want := range []string{
		"keeper: yolo internal daemon jail-keeper will hold this workspace's macos-user host services (the " +
			keeperProxy + " service) until its last macos-user session leaves; log: " + keeperLogPath(key),
		"keeper: started, pid ",
		"keeper: done",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the launch must say %q:\n%s", want, out.String())
		}
	}
	assertKeyEnded(t, key, filepath.Dir(endpoint))
}

// A LAUNCH THAT PLANS NOTHING A KEEPER HOLDS SPAWNS NONE (JL-D42): no keeper, no count, and the
// arrival lock gone before its session, so a second terminal never waits on it. It still records
// itself, as a session no keeper holds, so `yolo stop` can end it (JL-D44), and its quit removes the
// record. Deleting the arm's releaseArrivalLock or recordKeeperlessSession on that path fails this.
func TestAMacosUserLaunchThatPlansNothingHasNoKeeper(t *testing.T) {
	home := packHome(t)
	writeUserConfigJSON(t, home, `{"packs": []}`)
	ws := t.TempDir()
	key := macosUserKeyOf(ws)
	spawns := countKeeperSpawns(t)
	var counted, arrivalHeld bool
	var records []keyedSession
	o, out := keeperLaunch(t, ws, func(*jsonx.OrderedMap) int {
		if f, err := openSessionLock(key); err == nil {
			counted = readSessionLockHolder(f) != heldByNobody
			_ = f.Close()
		}
		records = liveKeyedSessions(key)
		if f, err := os.OpenFile(arrivalLockPath(key), os.O_RDWR|os.O_CREATE, 0o644); err == nil {
			arrivalHeld = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil
			_ = f.Close()
		}
		return 0
	})
	o.Getpid = func() int { return 4254 }
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\n%s", rc, out.String())
	}
	if n := spawns.Load(); n != 0 {
		t.Errorf("a launch that plans nothing outside the sandbox spawned %d keepers", n)
	}
	if counted {
		t.Error("a launch with no keeper counted itself in the session lock")
	}
	if len(records) != 1 || records[0].PID != 4254 || records[0].Kept {
		t.Errorf("the session's record during the session = %+v, want this launch's, not kept", records)
	}
	if s := liveKeyedSessions(key); len(s) != 0 {
		t.Errorf("the session's record outlived its quit: %+v", s)
	}
	if arrivalHeld {
		t.Error("a launch with no keeper held its key's arrival lock through its session")
	}
	if strings.Contains(out.String(), "keeper:") {
		t.Errorf("a launch with no keeper named one:\n%s", out.String())
	}
}

// keeperBridgeConfig is a user config selecting claude on cerebras, whose wire bridge a macos-user
// launch runs as a launch-owned service, beside the local pack's fixture front.
const keeperBridgeConfig = `{"packs": ["claude", "cerebras"], "env_sources": [{"CEREBRAS_API_KEY": "csk-test"}]}`

// fakeHeld is a started launch-owned service whose end a test controls (Done), as a Running's.
type fakeHeld struct {
	done    chan struct{}
	stopped *atomic.Int32
}

func (f *fakeHeld) Stop()                               { f.stopped.Add(1) }
func (f *fakeHeld) PID() int                            { return 4246 }
func (f *fakeHeld) Supervise(string, io.Writer, string) {}
func (f *fakeHeld) Done() <-chan struct{}               { return f.done }

// observeHeldServices stubs the launch-owned service start, returning the started plans' count and
// the one fakeHeld every start returns.
func observeHeldServices(t *testing.T) (*atomic.Int32, *fakeHeld) {
	t.Helper()
	var starts atomic.Int32
	held := &fakeHeld{done: make(chan struct{}), stopped: &atomic.Int32{}}
	orig := startMacosUserService
	startMacosUserService = func(p *launchservice.Plan, _ map[string]string) (launchedService, string, error) {
		starts.Add(1)
		return held, "/log/launch-service-" + p.Service + ".log", nil
	}
	t.Cleanup(func() { startMacosUserService = orig })
	return &starts, held
}

// agentEnvFile is the per-agent env file of agent the macos-user arm writes for ws.
func agentEnvFile(ws, agent string) string {
	return filepath.Join(paths.WorkspaceHomeState(ws), macosUserAgentEnvDir, agent+".sh")
}

// TWO ARRIVALS SHARE ONE KEEPER (JL-D39, JL-D41; §8 item 14 without an agent turn): the second
// launch joins it and says so, starts nothing, and its sandbox is told the same endpoint, the same
// bridge address and caller token, and gets byte-identical per-agent env files. The FIRST to quit
// leaves the second's endpoint answering, and the LAST streams the teardown and leaves nothing.
// Deleting arriveMacosUser's join, seedFromRoster's adoption or joinMacosUserKeeper's line fails this.
func TestTwoMacosUserSessionsOfOneWorkspaceShareOneKeeper(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("spawns a host daemon")
	}
	home := packHome(t)
	writeKeeperProxy(t, home)
	writeUserConfigJSON(t, home, keeperBridgeConfig)
	ws := t.TempDir()
	key := macosUserKeyOf(ws)
	spawns := countKeeperSpawns(t)
	starts, _ := observeHeldServices(t)

	type seen struct {
		endpoint, url, token string
		envFile              []byte
	}
	look := func(env *jsonx.OrderedMap) seen {
		b, _ := os.ReadFile(agentEnvFile(ws, "claude"))
		return seen{endpoint: envString(env, hostServiceEnvVar(keeperProxy)),
			url: envString(env, "ANTHROPIC_BASE_URL"), token: envString(env, "ANTHROPIC_AUTH_TOKEN"), envFile: b}
	}
	var a, b seen
	var afterA, packTree string
	var treeAfterA bool
	bIn, releaseB, bDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	oB, outB := keeperLaunch(t, ws, func(env *jsonx.OrderedMap) int {
		b = look(env)
		close(bIn)
		<-releaseB
		afterA, _ = frontReply(b.endpoint, "ping")
		// THE PACK TREE CHANGED HANDS (JL-D86): the keeper runs from the first launch's tree, so that
		// launch's return left it for the keeper's end.
		if rec, ok := readKeeperRecord(key); ok {
			packTree = rec.PackTree
			treeAfterA = packTree != "" && fileExists(packTree)
		}
		return 0
	})
	oB.Args, oB.ProfileName = []string{"claude"}, "cerebras"
	rcB := -1
	oA, outA := keeperLaunch(t, ws, func(env *jsonx.OrderedMap) int {
		a = look(env)
		go func() {
			defer close(bDone)
			rcB = Run(*oB)
		}()
		select {
		case <-bIn:
		case <-bDone:
		case <-time.After(30 * time.Second):
		}
		return 0
	})
	oA.Args, oA.ProfileName = []string{"claude"}, "cerebras"
	if rc := Run(*oA); rc != 0 {
		close(releaseB)
		<-bDone
		t.Fatalf("Run() = %d (the first launch)\n%s\nB:\n%s", rc, outA.String(), outB.String())
	}
	select {
	case <-bIn:
	default:
		close(releaseB)
		<-bDone
		t.Fatalf("the second launch never reached its session (rc %d):\nA:\n%s\nB:\n%s", rcB, outA.String(), outB.String())
	}
	// The first has quit, teardown and all: the second's endpoint must still answer.
	stillAlive := probeKeeper(key)
	close(releaseB)
	<-bDone
	logs := "A:\n" + outA.String() + "\nB:\n" + outB.String()
	if rcB != 0 {
		t.Fatalf("Run() = %d (the second launch)\n%s", rcB, logs)
	}
	if n := spawns.Load(); n != 1 {
		t.Errorf("two launches of one workspace spawned %d keepers, want 1\n%s", n, logs)
	}
	if n := starts.Load(); n != 1 {
		t.Errorf("the wire bridge's host half was started %d times, want once, by the keeper\n%s", n, logs)
	}
	if a.endpoint == "" || a.endpoint != b.endpoint || a.url == "" || a.url != b.url ||
		a.token == "" || a.token != b.token {
		t.Errorf("the two sandboxes were told different host services:\nA %+v\nB %+v\n%s", a, b, logs)
	}
	if len(a.envFile) == 0 || !bytes.Equal(a.envFile, b.envFile) {
		t.Errorf("claude's per-agent env file differs between the two sessions:\nA:\n%s\nB:\n%s", a.envFile, b.envFile)
	}
	if stillAlive != keeperAlive || afterA != "pong" {
		t.Errorf("after the first session quit, the keeper was %v and the second's endpoint answered %q; "+
			"want it alive and pong\n%s", stillAlive, afterA, logs)
	}
	if !treeAfterA {
		t.Errorf("after the first session quit, the pack tree the keeper runs from (%q) was gone\n%s", packTree, logs)
	}
	if packTree != "" && fileExists(packTree) {
		t.Errorf("the keeper's pack tree %s outlived the key's last session", packTree)
	}
	if !strings.Contains(outB.String(), "keeper: joined yolo internal daemon jail-keeper (pid ") ||
		strings.Contains(outB.String(), "keeper: yolo internal daemon jail-keeper will hold") {
		t.Errorf("the second launch must say it joined the keeper, and not start one:\n%s", outB.String())
	}
	if !strings.Contains(outA.String(), "This workspace's macos-user host services stay up (keeper pid ") ||
		!strings.Contains(outA.String(), "for 1 other session") {
		t.Errorf("the first to quit must say the host services stay up for the other session:\n%s", outA.String())
	}
	if !strings.Contains(outB.String(), "keeper: done") {
		t.Errorf("the last to quit must stream the keeper's teardown:\n%s", outB.String())
	}
	assertKeyEnded(t, key, filepath.Dir(a.endpoint))
}

// plantDeadKeeper leaves key in the state a SIGKILLed keeper does: its roster, naming a host-services
// dir it made, a free liveness lock, and one session still running, which this test holds (its count
// and its record). It returns the dir and the session's two holds.
//
// The dir is made under paths.HostServicesBase(isMacOS), where a keeper of options with that IsMacOS
// makes its own and where their reap (reapKeyRecords) looks for it, so isMacOS is the IsMacOS of the
// options the test drives: dispatchOptions' false for a Run, paths.IsMacOS for StopMacosUser, whose
// options are NewDefaultOptions'. On darwin the two bases differ, HostServicesBase(true) resolving
// /tmp's symlink to /private/tmp, and a dir planted under the other one is one the reap never matches.
func plantDeadKeeper(t *testing.T, ws, key string, isMacOS bool) (dir string, count *sessionLock, record *os.File) {
	t.Helper()
	cname := yoloruntime.FromWorkspace(ws)
	base := paths.HostServicesBase(isMacOS)
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(base, paths.HostServicesSessionPrefix(cname))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := writeKeeperRecord(key, keeperRecord{PID: 4247, Started: time.Now(), Workspace: ws,
		Runtime: "macos-user", SocketsDir: dir, Contract: keeperRosterContract, Notch: keeperNotchMacosUser,
		Build: keeperBuildStamp(), Ready: true}); err != nil {
		t.Fatal(err)
	}
	count, _, err = takeSessionLock(key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(count.release)
	record, err = openKeyedSessionRecord(key, 4248, time.Now(), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeKeyedSessionRecord(record) })
	return dir, count, record
}

// AN ARRIVAL AT AN UNKEPT KEY IS REFUSED (JL-D44, as OQ-JL7 ruled for every notch): the keeper is
// gone and a session runs on, so the launch names that session and `yolo stop`, and spawns no
// replacement. Then that session's quit, the last, reaps what the dead keeper left, removing each
// record only while the roster still names that keeper. Deleting arriveMacosUser's refusal, or
// endMacosUserSession's reap, fails this.
func TestAnArrivalAtAMacosUserKeyWhoseKeeperDiedIsRefused(t *testing.T) {
	home := packHome(t)
	writeUserConfigJSON(t, home, `{"packs": []}`)
	ws := t.TempDir()
	key := macosUserKeyOf(ws)
	dir, count, record := plantDeadKeeper(t, ws, key, false)
	spawns := countKeeperSpawns(t)
	reached := false
	o, out := keeperLaunch(t, ws, func(*jsonx.OrderedMap) int { reached = true; return 0 })
	if rc := Run(*o); rc != 1 || reached {
		t.Fatalf("Run() = %d (reached %v), want the unkept key's refusal\n%s", rc, reached, out.String())
	}
	for _, want := range []string{"Refusing to launch: this workspace's macos-user host services are gone, " +
		"because its keeper (pid 4247) is", ") still runs without them.", "1 session (pid 4248, since ",
		"'yolo stop' from this workspace ends them"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the refusal must say %q:\n%s", want, out.String())
		}
	}
	if n := spawns.Load(); n != 0 {
		t.Errorf("an arrival at an unkept key spawned %d keepers, want none", n)
	}

	// THE GUARD: a reap whose roster names another keeper by now removes nothing.
	q := &Options{Workspace: ws}
	fillDefaults(q)
	q.Stdout, q.Stderr = out, out
	q.reapKeyRecords(key, yoloruntime.FromWorkspace(ws), keeperRecord{PID: 9999, SocketsDir: dir})
	if _, ok := readKeeperRecord(key); !ok || !fileExists(dir) {
		t.Fatalf("a reap for a keeper the roster does not name removed the roster or its dir")
	}

	// The surviving session quits: the last, so it reaps.
	q.macosUserKey = &macosUserKeying{key: key, record: record}
	q.sessionLock = count
	q.endMacosUserSession("macos-user", 0)
	if !strings.Contains(out.String(), "This workspace's macos-user keeper (pid 4247) is gone, and this was its last session") {
		t.Errorf("the last session of an unkept key must say it removes what the keeper left:\n%s", out.String())
	}
	assertKeyEnded(t, key, dir)
}

// A ROSTER OF A CONTRACT THIS BUILD DOES NOT KNOW IS REFUSED (JL-D45): another build's keeper holds
// the key, and its roster's format and token variables are not this build's to read, so the arrival
// names the keeper's pid and build, the sessions and `yolo stop`, and counts nothing. Deleting
// arriveMacosUser's contract check fails this.
func TestAJoinerRefusesARosterOfAContractItDoesNotKnow(t *testing.T) {
	home := packHome(t)
	writeUserConfigJSON(t, home, `{"packs": []}`)
	ws := t.TempDir()
	key := macosUserKeyOf(ws)
	live, err := holdLivenessLock(key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { releaseLock(live) })
	if err := writeKeeperRecord(key, keeperRecord{PID: 4249, Started: time.Now(), Workspace: ws,
		Contract: keeperRosterContract + 98, Notch: keeperNotchMacosUser, Build: "99.0.0@future", Ready: true}); err != nil {
		t.Fatal(err)
	}
	reached := false
	o, out := keeperLaunch(t, ws, func(*jsonx.OrderedMap) int { reached = true; return 0 })
	if rc := Run(*o); rc != 1 || reached {
		t.Fatalf("Run() = %d (reached %v), want the contract refusal\n%s", rc, reached, out.String())
	}
	for _, want := range []string{"held by a keeper (pid 4249) of yolo 99.0.0@future, whose roster (contract 99)",
		"'yolo stop' from this workspace ends them and their keeper"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the refusal must say %q:\n%s", want, out.String())
		}
	}
	if s, ok := tryExclusiveSessionLock(key); !ok {
		t.Error("a refused arrival kept a count of the key")
	} else {
		s.release()
	}
	if len(liveKeyedSessions(key)) != 0 {
		t.Error("a refused arrival left a session record")
	}
}

// A HELD SERVICE THAT DIES AFTER READY IS RECORDED (JL-D71 at macos-user): the keeper rewrites its
// roster with the death and logs it, an arrival afterwards is told, and the last session's streamed
// teardown carries the line. Deleting holdKey's watchServices, or joinMacosUserKeeper's
// noteServicesDown, fails this.
func TestAMacosUserServiceThatDiesAfterReadyIsRecordedForTheNextArrivalAndQuit(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("spawns a host daemon")
	}
	home := packHome(t)
	writeKeeperProxy(t, home)
	writeUserConfigJSON(t, home, keeperBridgeConfig)
	ws := t.TempDir()
	key := macosUserKeyOf(ws)
	_, held := observeHeldServices(t)
	oB, outB := keeperLaunch(t, ws, func(*jsonx.OrderedMap) int { return 0 })
	oB.Args, oB.ProfileName = []string{"claude"}, "cerebras"
	var down []keeperServiceDown
	rcB := -1
	oA, outA := keeperLaunch(t, ws, func(*jsonx.OrderedMap) int {
		close(held.done)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if rec, ok := readKeeperRecord(key); ok && len(rec.Down) > 0 {
				down = rec.Down
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		rcB = Run(*oB)
		return 0
	})
	oA.Args, oA.ProfileName = []string{"claude"}, "cerebras"
	if rc := Run(*oA); rc != 0 || rcB != 0 {
		t.Fatalf("Run() = %d, %d\nA:\n%s\nB:\n%s", rc, rcB, outA.String(), outB.String())
	}
	if len(down) != 1 || down[0].What != `the "wire-bridge" service's host half` {
		t.Fatalf("the keeper's roster recorded %+v, want the bridge's host half down\nA:\n%s", down, outA.String())
	}
	if !strings.Contains(outB.String(), `The "wire-bridge" service's host half of this workspace's macos-user `+
		`host services has been down since `) {
		t.Errorf("an arrival after the death must be told of it:\n%s", outB.String())
	}
	if !strings.Contains(outA.String(), `keeper: the "wire-bridge" service's host half went down at `) {
		t.Errorf("the last session's streamed teardown must carry the death's line:\n%s", outA.String())
	}
	assertKeyEnded(t, key, "")
}

// keeperWithSignals makes the package's spawner run each keeper in-process with sigs as its signals
// and pid as its own pid, as inProcessKeeper does otherwise, so a test can send the keeper what
// `yolo stop` sends it.
func keeperWithSignals(t *testing.T, sigs chan os.Signal, pid int) {
	t.Helper()
	keeperInProcess(t, inProcessSpec{sigs: sigs, pid: pid})
}

// inProcessSpec is what keeperInProcess changes about a keeper inProcessKeeper would run.
type inProcessSpec struct {
	// sigs are the keeper's signals; nil is a channel nothing sends on.
	sigs chan os.Signal
	// pid is the keeper's own pid; 0 leaves the process's.
	pid int
	// seams are the keeper's (KeeperSeams).
	seams KeeperSeams
	// onFrame, when set, runs as each frame the keeper writes to its progress pipe crosses to the
	// launch, before it does.
	onFrame func(tag byte)
}

// keeperInProcess makes the package's spawner run each keeper in-process, as inProcessKeeper does,
// with what spec changes.
func keeperInProcess(t *testing.T, spec inProcessSpec) {
	t.Helper()
	orig := defaultKeeperSpawner
	defaultKeeperSpawner = func(launch *Options, planPath string, progress, lifeline, lock *os.File,
		reserved []*os.File) (func() int, error) {
		dup := func(f *os.File) *os.File {
			if f == nil {
				return nil
			}
			syscall.ForkLock.RLock()
			fd, err := syscall.Dup(int(f.Fd()))
			if err == nil {
				syscall.CloseOnExec(fd)
			}
			syscall.ForkLock.RUnlock()
			if err != nil {
				t.Errorf("dup: %v", err)
				return nil
			}
			return os.NewFile(uintptr(fd), f.Name())
		}
		p, l, k := dup(progress), dup(lifeline), dup(lock)
		var held []*os.File
		for _, f := range reserved {
			held = append(held, dup(f))
		}
		plan, err := readKeeperPlan(planPath)
		if err != nil {
			return nil, err
		}
		// A PROXIED PROGRESS PIPE, when the test watches the frames: the keeper writes to the proxy,
		// which hands each frame on to the launch's pipe after onFrame.
		keeperProgress, proxied := p, make(chan struct{})
		if spec.onFrame != nil && p != nil {
			r, w, err := os.Pipe()
			if err != nil {
				return nil, err
			}
			keeperProgress = w
			go func() {
				defer close(proxied)
				defer p.Close()
				defer r.Close()
				br := bufio.NewReader(r)
				for {
					tag, payload, err := readFrame(br)
					if err != nil {
						return
					}
					spec.onFrame(tag)
					if writeFrame(p, tag, payload) != nil {
						return
					}
				}
			}()
		} else {
			close(proxied)
		}
		sigs := spec.sigs
		if sigs == nil {
			sigs = make(chan os.Signal)
		}
		done := make(chan int, 1)
		go func() {
			rc := runKeeper(plan, spec.seams, keeperProgress, l, k, held, sigs, func(ko *Options) {
				adoptLaunchSeams(ko, launch)
				if spec.pid != 0 {
					ko.Getpid = func() int { return spec.pid }
				}
			})
			_ = keeperProgress.Close()
			<-proxied
			done <- rc
		}()
		return func() int { return <-done }, nil
	}
	t.Cleanup(func() { defaultKeeperSpawner = orig })
}

// signalsSent stands in for the kernel under `yolo stop`, sending each signal to whatever the test
// mapped its pid to.
func signalsSent(t *testing.T, to map[int]func(syscall.Signal)) *[]int {
	t.Helper()
	var mu sync.Mutex
	var sent []int
	orig := signalKeyProcess
	signalKeyProcess = func(pid int, sig syscall.Signal) error {
		mu.Lock()
		sent = append(sent, pid)
		mu.Unlock()
		if f := to[pid]; f != nil {
			f(sig)
			return nil
		}
		return errors.New("no such process")
	}
	t.Cleanup(func() { signalKeyProcess = orig })
	return &sent
}

// `yolo stop` AT macos-user ENDS THE KEY (JL-D44): it signals the keeper and each recorded session,
// streams the keeper's teardown, and returns once it is done. Deleting either signal, or the wait,
// fails this.
func TestYoloStopEndsAMacosUserKeeperAndItsSessions(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("spawns a host daemon")
	}
	home := packHome(t)
	writeKeeperProxy(t, home)
	writeUserConfigJSON(t, home, `{"packs": []}`)
	ws := t.TempDir()
	key := macosUserKeyOf(ws)
	const keeperPID, sessionPID = 4251, 4252
	sigs := make(chan os.Signal, 2)
	keeperWithSignals(t, sigs, keeperPID)
	sessionEnds := make(chan struct{})
	var once sync.Once
	sent := signalsSent(t, map[int]func(syscall.Signal){
		keeperPID:  func(s syscall.Signal) { sigs <- s },
		sessionPID: func(syscall.Signal) { once.Do(func() { close(sessionEnds) }) },
	})
	stopOut := &lockedBuffer{}
	stopRC := -1
	stopped := make(chan struct{})
	var endpoint string
	o, out := keeperLaunch(t, ws, func(env *jsonx.OrderedMap) int {
		endpoint = envString(env, hostServiceEnvVar(keeperProxy))
		go func() {
			defer close(stopped)
			stopRC = StopMacosUser(stopOut, stopOut, ws)
		}()
		select {
		case <-sessionEnds:
		case <-time.After(30 * time.Second):
		}
		return 143
	})
	o.Getpid = func() int { return sessionPID }
	rc := Run(*o)
	select {
	case <-stopped:
	case <-time.After(60 * time.Second):
		t.Fatalf("`yolo stop` did not return\nstop:\n%s\nlaunch:\n%s", stopOut.String(), out.String())
	}
	if rc != 143 || stopRC != 0 {
		t.Fatalf("Run() = %d, stop = %d, want the session's 143 and 0\nstop:\n%s\nlaunch:\n%s", rc, stopRC,
			stopOut.String(), out.String())
	}
	if len(*sent) != 2 || (*sent)[0] != keeperPID || (*sent)[1] != sessionPID {
		t.Errorf("the stop signalled %v, want the keeper (%d) and then the session (%d)", *sent, keeperPID, sessionPID)
	}
	for _, want := range []string{"sent SIGTERM to the keeper (pid 4251), a session (pid 4252)",
		"keeper: done", "Stopped this workspace's macos-user sessions and their keeper."} {
		if !strings.Contains(stopOut.String(), want) {
			t.Errorf("the stop must say %q:\n%s", want, stopOut.String())
		}
	}
	assertKeyEnded(t, key, filepath.Dir(endpoint))
}

// `yolo stop` AT AN UNKEPT macos-user KEY REAPS IT (JL-D44, JL-D30): no keeper to signal, so it
// signals the recorded session, waits for it to let the count go, and removes what the dead keeper
// left. Deleting the reap fails this.
func TestYoloStopReapsAnUnkeptMacosUserKey(t *testing.T) {
	home := packHome(t)
	writeUserConfigJSON(t, home, `{"packs": []}`)
	ws := t.TempDir()
	key := macosUserKeyOf(ws)
	dir, count, record := plantDeadKeeper(t, ws, key, paths.IsMacOS)
	sent := signalsSent(t, map[int]func(syscall.Signal){
		4248: func(syscall.Signal) {
			closeKeyedSessionRecord(record)
			count.release()
		},
	})
	var out bytes.Buffer
	if rc := StopMacosUser(&out, &out, ws); rc != 0 {
		t.Fatalf("StopMacosUser() = %d\n%s", rc, out.String())
	}
	if len(*sent) != 1 || (*sent)[0] != 4248 {
		t.Errorf("the stop signalled %v, want the session alone (4248): its keeper is gone", *sent)
	}
	if !strings.Contains(out.String(), "This workspace's macos-user keeper (pid 4247) was gone; removing what it left.") {
		t.Errorf("the stop must say it removes what the dead keeper left:\n%s", out.String())
	}
	assertKeyEnded(t, key, dir)
}

// A STOP WITH NOTHING RUNNING SAYS SO AND SUCCEEDS, so the stop-then-launch series keeps its first
// half idempotent at macos-user too.
func TestYoloStopAtAnIdleMacosUserWorkspaceSaysSo(t *testing.T) {
	packHome(t)
	ws := t.TempDir()
	var out bytes.Buffer
	if rc := StopMacosUser(&out, &out, ws); rc != 0 {
		t.Fatalf("StopMacosUser() = %d\n%s", rc, out.String())
	}
	if !strings.Contains(out.String(), "Nothing to stop: no macos-user session of this workspace") {
		t.Errorf("an idle stop must say there is nothing to stop:\n%s", out.String())
	}
}

// A SESSION'S RECORD IS LIVE WHILE ITS SESSION HOLDS IT: a reader names it, and once it is let go
// without its removal (a SIGKILLed session) the next reader removes it and names nothing.
func TestAKeyedSessionRecordIsLiveOnlyWhileHeld(t *testing.T) {
	packHome(t)
	key := keeperKey("yolo-records-0000aaaa", keeperNotchMacosUser)
	f, err := openKeyedSessionRecord(key, 4253, time.Unix(1700000000, 0), true)
	if err != nil {
		t.Fatal(err)
	}
	if s := liveKeyedSessions(key); len(s) != 1 || s[0].PID != 4253 {
		t.Fatalf("a held record reads %+v, want pid 4253", s)
	}
	// Let it go without removing it, as a killed session does.
	path := strings.TrimSuffix(f.Name(), keyedSessionRecordPending) + ".json"
	_ = f.Close()
	if s := liveKeyedSessions(key); len(s) != 0 {
		t.Errorf("an unheld record still reads live: %+v", s)
	}
	if fileExists(path) {
		t.Errorf("an unheld record %s was not removed by its reader", path)
	}
}

// A JOINER GRANTS ONLY AN ENDPOINT FILE NO SESSION GRANTED (§9.9.5): Run hands the backend, through
// the arm, the keeper's grant record, which the first session's stage writes, and a second session's
// stage skips the files it names. Only paths in the keeper's host-services dir are recorded or
// skipped. Deleting Run's setGrants call, or macosUserGrants' record, fails this.
func TestAJoiningMacosUserSessionSkipsTheGrantsTheFirstMade(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("spawns a host daemon")
	}
	home := packHome(t)
	writeKeeperProxy(t, home)
	writeUserConfigJSON(t, home, `{"packs": []}`)
	ws := t.TempDir()
	var firstSkips, joinerSkips, joinerSkipsElsewhere bool
	var endpoint string
	oB, outB := keeperLaunch(t, ws, func(env *jsonx.OrderedMap) int { return 0 })
	oB.MacosUserArm = NewMacosUserArm()
	armB := oB.MacosUserArm
	oB.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, _ macosuser.HomeOverlay,
		_ macosuser.HostContext, _ bool, env *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
		AcquireWorkspaceLockFor(ws, yoloruntime.FromWorkspace(ws), nil, nil)()
		joinerSkips = armB.SkipGrant(envString(env, hostServiceEnvVar(keeperProxy)))
		joinerSkipsElsewhere = armB.SkipGrant("/elsewhere/x.endpoint")
		return 0
	}
	oA, outA := keeperLaunch(t, ws, func(*jsonx.OrderedMap) int { return 0 })
	oA.MacosUserArm = NewMacosUserArm()
	armA := oA.MacosUserArm
	rcB := -1
	oA.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, _ macosuser.HomeOverlay,
		_ macosuser.HostContext, _ bool, env *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
		AcquireWorkspaceLockFor(ws, yoloruntime.FromWorkspace(ws), nil, nil)()
		endpoint = envString(env, hostServiceEnvVar(keeperProxy))
		firstSkips = armA.SkipGrant(endpoint)
		// What the first session's stage would report it granted: the file, its dir, and a path of
		// its own outside the keeper's dir.
		armA.Staged([]string{endpoint, filepath.Dir(endpoint), "/elsewhere/x.endpoint"})
		rcB = Run(*oB)
		return 0
	}
	if rc := Run(*oA); rc != 0 || rcB != 0 {
		t.Fatalf("Run() = %d, %d\nA:\n%s\nB:\n%s", rc, rcB, outA.String(), outB.String())
	}
	if endpoint == "" || firstSkips {
		t.Errorf("the first session skipped the grant of %q, which no session had made", endpoint)
	}
	if !joinerSkips {
		t.Error("the joining session's stage would grant the keeper's endpoint file again")
	}
	if joinerSkipsElsewhere {
		t.Error("the joining session would skip a grant outside the keeper's host-services dir")
	}
	if fileExists(keeperGrantedPath(macosUserKeyOf(ws))) {
		t.Error("the grant record outlived the keeper")
	}
}

// A SIGNAL BEFORE THE KEEPER IS READY ENDS ITS START ON ITS LIFELINE (JL-D40, the arm's setup phase):
// the arm closes the keeper's lifeline, the keeper sees it before ready, stops what it started and
// removes its records, never saying ready, and the launch returns the signal's status without
// reaching its session. The signal is taken by the arm while the keeper starts the wire bridge, as a
// Ctrl-C there is, and the start waits until the keeper has seen its lifeline end (the seam), so the
// keeper's check before ready reads it. Deleting startMacosUserKeeper's onEnding, or holdKey's
// endedBeforeReady, fails this: without the first the lifeline stays open until the keeper is ready,
// and the keeper ends only later, through the last session's teardown.
func TestASignalBeforeTheMacosUserKeeperIsReadyEndsItsStart(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("spawns a host daemon")
	}
	nixchildren.Isolate(t)
	home := packHome(t)
	writeUserConfigJSON(t, home, keeperBridgeConfig)
	ws := t.TempDir()
	key := macosUserKeyOf(ws)
	lifelineSeen := make(chan struct{})
	var once sync.Once
	var ready atomic.Bool
	keeperInProcess(t, inProcessSpec{
		seams: KeeperSeams{lifelineGone: func() { once.Do(func() { close(lifelineSeen) }) }},
		onFrame: func(tag byte) {
			if tag == frameReady {
				ready.Store(true)
			}
		},
	})
	reached := false
	o, out := keeperLaunch(t, ws, func(*jsonx.OrderedMap) int { reached = true; return 0 })
	o.Args, o.ProfileName = []string{"claude"}, "cerebras"
	o.MacosUserArm = NewMacosUserArm()
	arm := o.MacosUserArm
	held := &fakeHeld{done: make(chan struct{}), stopped: &atomic.Int32{}}
	sawLifelineEnd := false
	orig := startMacosUserService
	startMacosUserService = func(p *launchservice.Plan, _ map[string]string) (launchedService, string, error) {
		arm.take(syscall.SIGTERM)
		select {
		case <-lifelineSeen:
			sawLifelineEnd = true
		case <-time.After(10 * time.Second):
		}
		return held, "/log/launch-service-" + p.Service + ".log", nil
	}
	t.Cleanup(func() { startMacosUserService = orig })
	if rc := Run(*o); rc != 128+int(syscall.SIGTERM) || reached {
		t.Fatalf("Run() = %d (reached %v), want the signal's 143 before the session\n%s", rc, reached, out.String())
	}
	if !sawLifelineEnd {
		t.Fatalf("the signal did not end the keeper's lifeline while it was starting\n%s", out.String())
	}
	if n := held.stopped.Load(); n != 1 {
		t.Errorf("the keeper stopped the service it started %d times on its unwind, want once", n)
	}
	if !strings.Contains(out.String(), "is gone before they were ready; ending them") {
		t.Errorf("the keeper must say it ends what it started, its launch gone before ready:\n%s", out.String())
	}
	if ready.Load() || strings.Contains(out.String(), "keeper: done") {
		t.Errorf("the keeper said ready, or tore down as a ready keeper does, after its lifeline ended:\n%s", out.String())
	}
	assertKeyEnded(t, key, "")
}

// A JOINER THAT NEEDS A HOST SERVICE THE KEEPER DOES NOT RUN IS REFUSED, NEVER RESTARTED (JL-D39): the
// config changed since the keeper started, so the arrival names what the keeper lacks, the sessions
// and `yolo stop`, and the hatch the container attach takes, AllowAttachSkewEnv, joins without it.
// No second keeper starts either way. Deleting joinMacosUserKeeper's comparison fails this.
func TestAMacosUserJoinerNeedingAServiceTheKeeperLacksIsRefused(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("spawns a host daemon")
	}
	home := packHome(t)
	writeKeeperProxy(t, home)
	writeUserConfigJSON(t, home, `{"packs": []}`)
	ws := t.TempDir()
	spawns := countKeeperSpawns(t)
	observeHeldServices(t)
	joiner := func(hatch bool) (*Options, *lockedBuffer, *bool) {
		reached := false
		o, out := keeperLaunch(t, ws, func(*jsonx.OrderedMap) int { reached = true; return 0 })
		o.Args, o.ProfileName = []string{"claude"}, "cerebras"
		getenv := o.Getenv
		o.Getenv = func(k string) string {
			if hatch && k == AllowAttachSkewEnv {
				return "1"
			}
			return getenv(k)
		}
		return o, out, &reached
	}
	oB, outB, reachedB := joiner(false)
	oC, outC, reachedC := joiner(true)
	rcB, rcC := -1, -1
	oA, outA := keeperLaunch(t, ws, func(*jsonx.OrderedMap) int {
		// The config the keeper started from had no agent; the next launches pair claude through
		// the wire bridge, which the keeper does not run.
		writeUserConfigJSON(t, home, keeperBridgeConfig)
		rcB = Run(*oB)
		rcC = Run(*oC)
		return 0
	})
	if rc := Run(*oA); rc != 0 {
		t.Fatalf("Run() = %d\n%s", rc, outA.String())
	}
	if rcB != 1 || *reachedB {
		t.Fatalf("the joiner returned %d (reached %v), want the refusal\n%s", rcB, *reachedB, outB.String())
	}
	for _, want := range []string{"Refusing to launch: this launch needs ", `the wire-bridge service's host half`,
		"which this workspace's macos-user keeper (pid ", "and 1 session (pid 1, since ", ") uses it.",
		"'yolo stop' from this workspace ends them and their keeper", AllowAttachSkewEnv + "=1 joins without it"} {
		if !strings.Contains(outB.String(), want) {
			t.Errorf("the refusal must say %q:\n%s", want, outB.String())
		}
	}
	if rcC != 0 || !*reachedC || !strings.Contains(outC.String(), "Joining this workspace's macos-user keeper (pid ") {
		t.Errorf("under %s=1 the joiner returned %d (reached %v); want it joined, saying without what\n%s",
			AllowAttachSkewEnv, rcC, *reachedC, outC.String())
	}
	if n := spawns.Load(); n != 1 {
		t.Errorf("%d keepers were spawned; a joiner is never handed a new one", n)
	}
	assertKeyEnded(t, macosUserKeyOf(ws), "")
}
