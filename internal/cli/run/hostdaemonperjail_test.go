package run

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/openaiauth"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// lingerAfterSIGTERM is frontUpstreamChildMain's "lingers" mode: the child records its pid, and a
// SIGTERM makes it say so and then finish, exiting only once the test releases it — a credential
// daemon completing the refresh in flight when its jail ended.
func lingerAfterSIGTERM() {
	if p := os.Getenv("YJ_FRONT_CHILD_PID"); p != "" {
		_ = os.WriteFile(p, []byte(strconv.Itoa(os.Getpid())), 0o600)
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM)
	go func() {
		<-sigs
		if p := os.Getenv("YJ_FRONT_CHILD_TERMED"); p != "" {
			_ = os.WriteFile(p, nil, 0o600)
		}
		for release := os.Getenv("YJ_FRONT_CHILD_RELEASE"); release != ""; time.Sleep(20 * time.Millisecond) {
			if _, err := os.Stat(release); err == nil {
				break
			}
		}
		os.Exit(0)
	}()
}

// spliceChildMain binds socketPath and relays every connection, byte for byte and the
// connection preamble with it, to upstream: a per-jail daemon whose handler is one the test
// serves in its own process. It exits 0 on SIGTERM, as a daemon with nothing in flight does.
func spliceChildMain(socketPath, upstream string) int {
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "splice-child:", err)
		return 1
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM)
	go func() { <-sigs; _ = ln.Close() }()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return 0
		}
		go func(c net.Conn) {
			defer c.Close()
			up, err := net.Dial("unix", upstream)
			if err != nil {
				return
			}
			defer up.Close()
			done := make(chan struct{}, 2)
			go func() { _, _ = io.Copy(up, c); done <- struct{}{} }()
			go func() { _, _ = io.Copy(c, up); done <- struct{}{} }()
			<-done
		}(conn)
	}
}

// lingeringDaemon starts the "lingers" child behind a front through startExternalService, with hd's
// teardown, and returns its handle, its pid, and the TERMED and RELEASE marker paths.
func lingeringDaemon(t *testing.T, o *Options, hd *loopholes.HostDaemon) (loopholeDaemon, int, string, string) {
	t.Helper()
	socketsDir := t.TempDir()
	if err := os.Chmod(socketsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	termed := filepath.Join(dir, "termed")
	release := filepath.Join(dir, "release")
	spec := jsonx.NewOrderedMap()
	spec.Set("command", []any{os.Args[0], "-front-upstream-child", "lingers", "{socket}"})
	env := jsonx.NewOrderedMap()
	env.Set("YJ_FRONT_CHILD_PID", pidFile)
	env.Set("YJ_FRONT_CHILD_TERMED", termed)
	env.Set("YJ_FRONT_CHILD_RELEASE", release)
	spec.Set("env", env)
	h, ok := o.startExternalService("lingering", spec, socketsDir, loopholes.TransportLoopbackTLS,
		"127.0.0.1", hd)
	if !ok {
		t.Fatal("the lingering daemon never came up")
	}
	var pid int
	for deadline := time.Now().Add(5 * time.Second); pid == 0; time.Sleep(10 * time.Millisecond) {
		if raw, err := os.ReadFile(pidFile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
		}
		if time.Now().After(deadline) {
			t.Fatal("the lingering daemon never recorded its pid")
		}
	}
	t.Cleanup(func() {
		_ = os.WriteFile(release, nil, 0o600)
		_ = syscall.Kill(pid, syscall.SIGKILL)
	})
	return h, pid, termed, release
}

// processLive reports whether pid is a running process, a zombie counting as gone.
func processLive(pid int) bool {
	state, alive := procState(pid)
	return alive && state != "Z"
}

// THE DETACH DISPOSITION AT THE JAIL'S END (docs/design/host-daemon-ownership.md §1.3, "detach,
// do not drain, do not reap"): a daemon whose manifest declares `detach` gets its front closed
// and one SIGTERM, and the teardown neither waits for it nor kills it. The grace is set to thirty
// seconds so a teardown that waited would be seen to; the daemon then finishes on its own.
// Route stopService to killServiceGroup and the stop waits out the grace and SIGKILLs it.
func TestADetachedDaemonIsSignalledAndNotWaitedFor(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process and reads /proc")
	}
	var buf strings.Builder
	o := &Options{}
	fillDefaults(o)
	o.Stderr = &buf
	o.ServiceTermGrace = 30 * time.Second
	h, pid, termed, release := lingeringDaemon(t, o, &loopholes.HostDaemon{
		Publishes: loopholes.PublishesSocket, RequestEnd: loopholes.RequestEndFramed, Detach: true})
	upstream := frontSocketFile(frontShortHash(filepath.Dir(h.hostPath)), "lingering")

	start := time.Now()
	h.stop()
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("stopping a detached daemon took %s: the jail's end must not wait for it", took)
	}
	if !waitFor(5*time.Second, func() bool { return fileExists(termed) }) {
		t.Fatal("the detached daemon was never sent its SIGTERM")
	}
	if !processLive(pid) {
		t.Fatalf("the detached daemon (pid %d) is gone right after its jail's end: it was killed "+
			"instead of being left to finish its work in flight\n%s", pid, buf.String())
	}
	if fileExists(h.hostPath) {
		t.Error("the endpoint file outlived the jail's end: the front must close before the daemon is left")
	}
	if fileExists(upstream) {
		t.Errorf("the upstream socket %s outlived the jail's end", upstream)
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !waitFor(10*time.Second, func() bool { return !processLive(pid) }) {
		t.Errorf("the detached daemon (pid %d) did not exit once its work was done", pid)
	}
	if strings.Contains(buf.String(), "ignored SIGTERM") {
		t.Errorf("the teardown reported a SIGKILL escalation for a detached daemon:\n%s", buf.String())
	}
}

// AND EVERY OTHER DAEMON KEEPS ITS TEARDOWN: SIGTERM to the group, the grace, then SIGKILL. The
// same lingering daemon without `detach` is gone when stop returns. Route stopService to
// detachService for every daemon and it survives the stop.
func TestADaemonWithoutDetachIsStillKilledAfterItsGrace(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process and reads /proc")
	}
	var buf strings.Builder
	o := &Options{}
	fillDefaults(o)
	o.Stderr = &buf
	o.ServiceTermGrace = 200 * time.Millisecond
	h, pid, termed, _ := lingeringDaemon(t, o, &loopholes.HostDaemon{
		Publishes: loopholes.PublishesSocket, RequestEnd: loopholes.RequestEndFramed})
	h.stop()
	if !fileExists(termed) {
		t.Error("the daemon was not sent SIGTERM before the SIGKILL")
	}
	if !waitFor(2*time.Second, func() bool { return !processLive(pid) }) {
		t.Errorf("a daemon without `detach` (pid %d) outlived its jail's end past the grace", pid)
	}
	if !strings.Contains(buf.String(), "ignored SIGTERM") {
		t.Errorf("the SIGKILL escalation was not reported:\n%s", buf.String())
	}
}

// perJailFixture records one pack module, named name, declaring a detaching daemon (what the
// three credential daemons declare since they lost `scope: "host"`) that runs the "line" child, with the preamble off because that child reads its request first.
func perJailFixture(t *testing.T, name string) {
	t.Helper()
	dir := t.TempDir()
	lp := filepath.Join(dir, name)
	if err := os.MkdirAll(lp, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd, err := json.Marshal([]string{os.Args[0], "-front-upstream-child", "line", "{socket}"})
	if err != nil {
		t.Fatal(err)
	}
	manifest := `{"name": "` + name + `", "description": "fixture", "version": 1,
	  "transport": "` + loopholes.TransportLoopbackTLS + `",
	  "lifecycle": "spawned",
	  "host_daemon": {"cmd": ` + string(cmd) + `, "publishes": "socket",
	                  "preamble": false, "detach": true}}`
	if err := os.WriteFile(filepath.Join(lp, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	loopholes.SetPackModuleResolver(nil)
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: lp, HostExecApproved: true}})
	t.Cleanup(func() {
		loopholes.ResetPackModules()
		loopholes.SetPackModuleResolver(resolvePackLoopholeModules)
	})
}

// HD-R1 ON PODMAN, THE DISPATCH (docs/design/host-daemon-ownership.md): a credential daemon's
// record gets a daemon of this jail's own, keyed by the jail, and nothing at the machine-wide
// rendezvous the old singleton used. Its endpoint variable stays the assembler's
// (endpointPromisedAtAssembly), so the handle carries none. Ensure a machine-wide daemon for it
// again (startLoopholesMatching) and the daemon is ensured at /tmp/yolo-<name>.sock instead.
func TestOnPodmanACredentialDaemonRunsAsTheJailsOwn(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	name := "yjtest-perjail"
	perJailFixture(t, name)
	var buf strings.Builder
	o := &Options{}
	fillDefaults(o)
	o.Stderr = &buf
	o.IsMacOS = false

	cname := "yolo-perjail-test"
	handles := o.startLoopholes(cname, "podman", jsonx.NewOrderedMap())
	socketsDir := hostServiceSocketsDir(cname, false)
	t.Cleanup(func() { _ = os.RemoveAll(socketsDir) })
	var h *loopholeDaemon
	for i := range handles {
		if handles[i].name == name {
			h = &handles[i]
		}
	}
	if h == nil {
		o.stopLoopholes(handles, socketsDir, "", "")
		t.Fatalf("no handle for the credential daemon's record on podman; output:\n%s", buf.String())
	}
	upstream := frontSocketFile(frontShortHash(socketsDir), name)
	if !fileExists(upstream) {
		t.Errorf("the daemon's socket is not this jail's own %s", upstream)
	}
	for _, p := range []string{paths.HostSingletonSocket(name), paths.HostSingletonPIDFile(name),
		paths.HostSingletonLock(name)} {
		if fileExists(p) {
			t.Errorf("the launch touched the machine-wide rendezvous %s, which only an older yolo's "+
				"singleton uses now", p)
		}
	}
	if h.envVarName != "" {
		t.Errorf("the handle carries %s, which the assembler already emitted: the argv would carry "+
			"it twice", h.envVarName)
	}
	if got := dialFrontLine(t, h.hostPath, "ping"); got != "pong" {
		t.Errorf("round trip through this jail's front = %q, want pong", got)
	}
	o.stopLoopholes(handles, socketsDir, "", "")
	if fileExists(upstream) {
		t.Errorf("the jail's end left its daemon's socket %s", upstream)
	}
	if svcendpoint.Probe(h.hostPath) {
		t.Error("the jail's end left its endpoint file")
	}
}

// writeOpenAIState writes a valid OpenAI credential state at path.
func writeOpenAIState(t *testing.T, path string) {
	t.Helper()
	raw, err := json.Marshal(openaiauth.State{Version: 1, AccessToken: "access", IDToken: "id",
		RefreshToken: "refresh", ExpiresAtMS: time.Now().Add(time.Hour).UnixMilli(), Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// MIGRATE, THEN SPAWN (docs/design/host-daemon-ownership.md §5.2): on a backend where each jail
// runs its own OpenAI daemon, the one-time legacy-state migration still runs before the spawn, so
// the jail's daemon reads the relocated login. Delete the migrateLegacyOpenAIAuthState call from
// startLoopholesMatching and the legacy file stays in the workspace.
func TestThePerJailOpenAIDaemonRunsTheLegacyMigrationFirst(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	retireHome(t)
	perJailFixture(t, openaiauth.LoopholeName)
	workspace := t.TempDir()
	legacy := filepath.Join(workspace, "{state}", openaiauth.StateFileName)
	writeOpenAIState(t, legacy)
	var buf strings.Builder
	o := &Options{Workspace: workspace}
	fillDefaults(o)
	o.Stderr = &buf
	o.IsMacOS = false

	cname := "yolo-perjail-migrate"
	handles := o.startLoopholes(cname, "podman", jsonx.NewOrderedMap())
	socketsDir := hostServiceSocketsDir(cname, false)
	t.Cleanup(func() {
		o.stopLoopholes(handles, socketsDir, "", "")
		_ = os.RemoveAll(socketsDir)
	})
	if !startedLoophole(handles, openaiauth.LoopholeName) {
		t.Fatalf("the OpenAI daemon did not start; output:\n%s", buf.String())
	}
	canonical := filepath.Join(loopholes.StateDirFor(openaiauth.LoopholeName), openaiauth.StateFileName)
	if _, err := openaiauth.ReadState(canonical); err != nil {
		t.Errorf("the legacy login was not migrated to %s before the spawn: %v", canonical, err)
	}
	if fileExists(legacy) {
		t.Errorf("the legacy login is still in the workspace at %s", legacy)
	}
}

// A MIGRATION THAT CANNOT COMPLETE STARTS NO DAEMON, as it did under the singleton, and says what
// to do: with both a legacy and a canonical login there is no right one to guess, and a daemon
// serving the canonical beside an unread legacy file would hide the conflict.
func TestAMigrationThatCannotCompleteStartsNoOpenAIDaemonAndSaysWhy(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	retireHome(t)
	perJailFixture(t, openaiauth.LoopholeName)
	workspace := t.TempDir()
	writeOpenAIState(t, filepath.Join(workspace, "{state}", openaiauth.StateFileName))
	writeOpenAIState(t, filepath.Join(loopholes.StateDirFor(openaiauth.LoopholeName), openaiauth.StateFileName))
	var buf strings.Builder
	o := &Options{Workspace: workspace}
	fillDefaults(o)
	o.Stderr = &buf
	o.IsMacOS = false

	cname := "yolo-perjail-conflict"
	handles := o.startLoopholes(cname, "podman", jsonx.NewOrderedMap())
	socketsDir := hostServiceSocketsDir(cname, false)
	t.Cleanup(func() {
		o.stopLoopholes(handles, socketsDir, "", "")
		_ = os.RemoveAll(socketsDir)
	})
	if startedLoophole(handles, openaiauth.LoopholeName) {
		t.Error("the OpenAI daemon started although its legacy-state migration could not complete")
	}
	out := buf.String()
	for _, want := range []string{"legacy OpenAI credential", "starts no OpenAI credential service",
		"launch again"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, out)
		}
	}
}

// HD-R1 ON APPLE CONTAINER: the one credential service its allow list admits is the jail's own
// daemon too, keyed by the jail, with nothing at the machine-wide rendezvous. Take
// the per-jail spawn from Apple Container and it is ensured at /tmp/yolo-<name>.sock.
func TestOnAppleContainerTheOpenAIServiceRunsAsTheJailsOwnDaemon(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	retireHome(t)
	name := openaiauth.LoopholeName
	perJailFixture(t, name)
	var buf strings.Builder
	o := &Options{Workspace: t.TempDir()}
	fillDefaults(o)
	o.Stderr = &buf
	o.IsMacOS = false

	cname := "yolo-perjail-ac"
	handles := o.startLoopholes(cname, "container", jsonx.NewOrderedMap())
	socketsDir := hostServiceSocketsDir(cname, false)
	t.Cleanup(func() {
		o.stopLoopholes(handles, socketsDir, "", "")
		_ = os.RemoveAll(socketsDir)
	})
	if !startedLoophole(handles, name) {
		t.Fatalf("Apple Container started no OpenAI credential service; output:\n%s", buf.String())
	}
	if upstream := frontSocketFile(frontShortHash(socketsDir), name); !fileExists(upstream) {
		t.Errorf("the OpenAI daemon's socket is not this jail's own %s", upstream)
	}
	for _, p := range []string{paths.HostSingletonSocket(name), paths.HostSingletonPIDFile(name)} {
		if fileExists(p) {
			t.Errorf("the Apple Container launch touched the machine-wide rendezvous %s", p)
		}
	}
}

// HD-D4'S PER-WORKSPACE SPAWN GUARD, THROUGH THE ARM: a macos-user launch whose workspace's guard
// is held, as a `yolo host` launch of the workspace holds it while its daemons start, waits for
// it, says so, and its keeper starts the workspace's host services only once it is free. Delete
// the guard's take from the macos-user keeper (keeper.go) and the launch runs straight through.
func TestTheMacosUserArmWaitsForTheWorkspacesSpawnGuard(t *testing.T) {
	o, _, seen := codexNativeLaunch(t)
	observeDoorways(t)
	var stderr syncBuffer
	o.Stderr = &stderr
	cname := yoloruntime.FromWorkspace(o.Workspace)
	held, err := o.takeHostDaemonSpawnGuard(cname)
	if err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			held.Close()
		}
	}()
	rc := make(chan int, 1)
	go func() { rc <- Run(*o) }()
	if !waitFor(30*time.Second, func() bool {
		return strings.Contains(stderr.String(), "Waiting for another launch of this workspace to start its host services")
	}) {
		held.Close()
		released = true
		<-rc
		t.Fatalf("the launch never waited for the held spawn guard:\n%s", stderr.String())
	}
	select {
	case code := <-rc:
		t.Fatalf("the launch returned %d while the workspace's spawn guard was held:\n%s", code, stderr.String())
	case <-time.After(300 * time.Millisecond):
	}
	held.Close()
	released = true
	select {
	case code := <-rc:
		if code != 0 || !seen.reached {
			t.Fatalf("Run() = %d (reached the command: %v) once the guard was free:\n%s",
				code, seen.reached, stderr.String())
		}
	case <-time.After(60 * time.Second):
		t.Fatal("the launch did not finish within 60s of the guard's release")
	}
}

// A GUARD ITS KEEPER CANNOT TAKE REFUSES THE LAUNCH, before any host service starts, naming the
// lock and what to do: the guard is a real one, unlike the courtesy launch lock, which warns and
// goes on.
// A directory where the lock file goes makes the open fail.
func TestTheMacosUserArmRefusesWhenItCannotTakeTheSpawnGuard(t *testing.T) {
	o, stderr, seen := codexNativeLaunch(t)
	doors := observeDoorways(t)
	path := hostDaemonSpawnGuardPath(yoloruntime.FromWorkspace(o.Workspace))
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if rc := Run(*o); rc != 1 || seen.reached {
		t.Fatalf("Run() = %d (reached the command: %v), want the launch refused\n%s", rc, seen.reached, stderr.String())
	}
	for _, want := range []string{"Refusing the macos-user launch", path, "then launch again"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("the refusal lacks %q:\n%s", want, stderr.String())
		}
	}
	if len(doors.plans) != 0 {
		t.Errorf("the refused launch opened %d doorways", len(doors.plans))
	}
}

// THE REAL BROKER, AS A JAIL'S OWN DAEMON, ANSWERS THROUGH ITS FRONT: the shipped argv spawned per
// jail (startExternalService, under the manifest's `publishes: "socket"` with the preamble on and
// `detach`), and a framed ping sent as a jail sends it — through the endpoint, so the front writes
// its connection preamble ahead of these bytes — comes back as a pong. A daemon that read the
// preamble AS the request (hostservice.ServeUnix) would answer something else, and every Claude
// OAuth refresh would fail on a connection both ends believe is healthy. Its end is the detach:
// the daemon is gone shortly after the stop, its socket with it.
//
// The loophole is named `yjtest-broker`, not `claude-oauth-broker`, so nothing this test runs
// is ever mistaken for a developer's own broker; the argv is the shipped one, and TestMain
// dispatches it to the real oauthbroker.Main.
func TestAJailsOwnBrokerDaemonAnswersThroughItsFront(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	name := "yjtest-broker"
	// Dummy CA/leaf so EnsureCAAndLeaf short-circuits; the daemon never reads them on the ping path.
	state := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"ca.crt", "ca.key", "server.crt", "server.key"} {
		if err := os.WriteFile(filepath.Join(state, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("YOLO_BROKER_STATE_DIR", state)
	socketsDir := t.TempDir()
	if err := os.Chmod(socketsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	spec := jsonx.NewOrderedMap()
	spec.Set("command", []any{"yolo", "internal", "daemon", "claude-oauth-broker",
		"--socket", "{socket}", "--no-background-refresh"})
	var buf strings.Builder
	o := &Options{}
	fillDefaults(o)
	o.Stderr = &buf
	h, ok := o.startExternalService(name, spec, socketsDir, loopholes.TransportLoopbackTLS, "127.0.0.1",
		&loopholes.HostDaemon{Publishes: loopholes.PublishesSocket, RequestEnd: loopholes.RequestEndFramed,
			Preamble: true, Detach: true})
	if !ok {
		t.Fatalf("the broker daemon did not come up behind its front; output: %q", buf.String())
	}
	stopped := false
	defer func() {
		if !stopped {
			h.stop()
		}
	}()
	conn, err := svcendpoint.DialLocal(h.hostPath, 3*time.Second)
	if err != nil {
		t.Fatalf("dialing %s: %v", h.hostPath, err)
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	req := []byte(`{"action":"ping"}`)
	frame := make([]byte, 4+len(req))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(req)))
	copy(frame[4:], req)
	if _, err := conn.Write(frame); err != nil {
		t.Fatalf("writing the ping: %v", err)
	}
	// Response framing is <1-byte stream_id><4-byte BE length><payload>.
	hdr := make([]byte, 5)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		t.Fatalf("no response frame through the front: %v; output: %q", err, buf.String())
	}
	n := binary.BigEndian.Uint32(hdr[1:5])
	if n > 1<<20 {
		t.Fatalf("implausible response frame length %d", n)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(conn, payload); err != nil {
		t.Fatalf("short response frame: %v", err)
	}
	_ = conn.Close()
	if !strings.Contains(string(payload), "pong") {
		t.Errorf("the broker did not pong through its front: %q — it read yolo's connection "+
			"preamble as the request", string(payload))
	}
	h.stop()
	stopped = true
	if !waitFor(10*time.Second, func() bool { return !fileExists(h.upstream) }) {
		t.Errorf("the broker's socket %s outlived its jail's end", h.upstream)
	}
}
