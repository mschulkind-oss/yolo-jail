package run

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// TestHostServicesDirIsPrivate: the per-jail host-services dir must be created
// 0700, because it holds endpoint files and an endpoint file carries that
// service's bearer token. svcendpoint refuses to publish a credential into a
// group/world-accessible directory, so a 0755 dir does not merely look untidy —
// every host service dies at spawn.
func TestHostServicesDirIsPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "yolo-host-services-deadbeef")
	mkdirHostServicesDir(dir)
	st, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o700 {
		t.Errorf("fresh host-services dir mode = %#o, want 0700", perm)
	}
}

// TestHostServicesDirTightensExisting: a host upgrading from a yolo that created
// the dir 0755 must not be left with an unusable one. MkdirAll leaves an existing
// directory's mode alone, so the Chmod is the only thing carrying that host over.
func TestHostServicesDirTightensExisting(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "yolo-host-services-deadbeef")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil { // MkdirAll applies umask
		t.Fatal(err)
	}
	mkdirHostServicesDir(dir)
	st, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o700 {
		t.Errorf("pre-existing 0755 dir left at %#o, want tightened to 0700", perm)
	}
}

// TestHostServiceEnvVarIsEndpoint pins the CANONICAL spelling. The producer here
// and the consumers (yolo-ps, the OAuth terminator) must never drift — a drifted
// name once silently disabled the cgroup delegate in every jail.
func TestHostServiceEnvVarIsEndpoint(t *testing.T) {
	cases := map[string]string{
		"host-processes":      "YOLO_SERVICE_HOST_PROCESSES_ENDPOINT",
		"cgroup-delegate":     "YOLO_SERVICE_CGROUP_DELEGATE_ENDPOINT",
		"claude-oauth-broker": "YOLO_SERVICE_CLAUDE_OAUTH_BROKER_ENDPOINT",
		"journal":             "YOLO_SERVICE_JOURNAL_ENDPOINT",
		"weird.name-here":     "YOLO_SERVICE_WEIRD_NAME_HERE_ENDPOINT",
		"_leading":            "YOLO_SERVICE_LEADING_ENDPOINT",
	}
	for name, want := range cases {
		if got := hostServiceEnvVar(name); got != want {
			t.Errorf("hostServiceEnvVar(%q) = %q, want %q", name, got, want)
		}
		if strings.HasSuffix(hostServiceEnvVar(name), "_SOCKET") {
			t.Errorf("hostServiceEnvVar(%q) produced a _SOCKET name", name)
		}
	}
	// The retiring spelling still exists, and still says SOCKET — a service whose
	// value is a socket path must not advertise an endpoint file. Exactly ONE
	// service still uses it: the cgroup delegate, and NOT because its client is
	// unported (cmd/yolo-cglimit is a baked Go binary). SO_PEERCRED is what does
	// not survive the hop; see startCgroupDelegate.
	if got := hostServiceSocketEnvVar(paths.BuiltinCgroupLoopholeName); got != "YOLO_SERVICE_CGROUP_DELEGATE_SOCKET" {
		t.Errorf("hostServiceSocketEnvVar(cgroup-delegate) = %q", got)
	}
}

// TestOnlyTheCgroupDelegateStillPublishesASocket is the guard on the ONE
// remaining AF_UNIX service.
//
// It is a naming test on purpose: the delegate is an in-process goroutine that
// needs cgroup v2 to start, so a behavioural test would skip on most machines —
// and a skip here would turn "the flip half-applied" into invisible
// non-coverage. What it pins is the pair of decisions that make the delegate the
// exception: its jail-side value is a SOCKET path, and the variable naming it
// says so.
//
// If a future change makes the delegate reachable over loopback-tls, this test
// must be DELETED rather than adjusted, and deleting it should force reading
// the SO_PEERCRED argument above startCgroupDelegate first: a TCP hop carries no
// peer credential, and a front would attest yolo's own pid, moving the yolo run
// process into the jail's job cgroup.
func TestOnlyTheCgroupDelegateStillPublishesASocket(t *testing.T) {
	if !strings.HasSuffix(paths.CgdSocketName, ".sock") {
		t.Errorf("CgdSocketName = %q, want a .sock name", paths.CgdSocketName)
	}
	// Every OTHER built-in service name is on loopback-tls and must therefore
	// advertise an endpoint file.
	for _, name := range paths.BuiltinLoopholeNames {
		if name == paths.BuiltinCgroupLoopholeName {
			continue
		}
		if got := hostServiceEnvVar(name); !strings.HasSuffix(got, "_ENDPOINT") {
			t.Errorf("built-in %q advertises %q — every service but the cgroup "+
				"delegate is on loopback-tls now", name, got)
		}
	}
}

// TestEmittedAddressIsAStablePath: what the run pipeline injects into the container
// is ALWAYS a path, never a port and never an address.
//
// That is the bootstrap-ordering invariant. The container's environment is frozen at
// `podman run` time, so anything that can change later — a restarted daemon's
// kernel-assigned port, a rotated token — has to live behind a stable path the
// client re-reads on every dial.
func TestEmittedAddressIsAStablePath(t *testing.T) {
	got := hostServiceEndpointPath("host-processes")
	want := paths.JailHostServicesDir + "/host-processes" + paths.ServiceEndpointExt
	if got != want {
		t.Errorf("hostServiceEndpointPath = %q, want %q", got, want)
	}
	if !strings.HasPrefix(got, "/") {
		t.Errorf("emitted value %q is not an absolute path", got)
	}
	if _, _, err := net.SplitHostPort(got); err == nil {
		t.Errorf("emitted value %q parses as host:port; it must be a path", got)
	}
}

// startExternalServiceHarness spawns a fake daemon whose whole job is to write
// whatever the script says into the path the framework substituted, so the WAIT
// PREDICATE is what is under test.
//
// The script receives that path as $1 via the {endpoint} placeholder — which also
// exercises the substitution itself: a template the framework failed to fill would
// leave the script with an empty $1 and every one of these tests would pass
// vacuously. TestExternalServiceAcceptsCompleteEndpoint is the control that catches
// exactly that.
func startExternalServiceHarness(t *testing.T, socketsDir, script, transport string) (loopholeDaemon, bool) {
	t.Helper()
	spec := jsonx.NewOrderedMap()
	spec.Set("command", []any{"sh", "-c", script, "fake-svc", "{endpoint}"})
	o := &Options{}
	fillDefaults(o)
	o.Stdout = io.Discard
	return o.startExternalService("fake-svc", spec, socketsDir, transport, "127.0.0.1", nil)
}

// TestExternalServiceWaitsForCompleteEndpoint: a daemon that publishes an INCOMPLETE
// endpoint file must not be accepted.
//
// Existence is not health. The old predicate was a path-existence poll; keeping it
// would let a truncated or older-format file read as healthy forever, so a dead
// daemon is never respawned and the jail can never reach it.
func TestExternalServiceWaitsForCompleteEndpoint(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	socketsDir := t.TempDir()
	// Two fields: the format an older publisher wrote, and also what a torn write
	// looks like. It must NOT satisfy the wait.
	h, ok := startExternalServiceHarness(t, socketsDir,
		`test -n "$1" || exit 9; printf '127.0.0.1:1 Y29zdA==\n' > "$1"; sleep 30`,
		loopholes.TransportLoopbackTLS)
	if ok {
		if h.stop != nil {
			h.stop()
		}
		t.Fatal("a 2-field endpoint file satisfied the wait; want the daemon killed and no handle")
	}
}

// TestExternalServiceAcceptsCompleteEndpoint is the control for the test above: the
// same harness with a REAL published endpoint must succeed, so the negative above is
// not just "the harness never works".
func TestExternalServiceAcceptsCompleteEndpoint(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	socketsDir := t.TempDir()
	real := filepath.Join(t.TempDir(), "seed")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	seed := filepath.Join(real, "seed.endpoint")
	ln, err := svcendpoint.Listen(seed, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	h, ok := startExternalServiceHarness(t, socketsDir,
		`test -n "$1" || exit 9; cp `+seed+` "$1"; sleep 30`, loopholes.TransportLoopbackTLS)
	if !ok {
		t.Fatal("a complete endpoint file did not satisfy the wait")
	}
	defer h.stop()
	if h.envVarName != "YOLO_SERVICE_FAKE_SVC_ENDPOINT" {
		t.Errorf("envVarName = %q, want YOLO_SERVICE_FAKE_SVC_ENDPOINT", h.envVarName)
	}
	if h.jailPath != paths.JailHostServicesDir+"/fake-svc"+paths.ServiceEndpointExt {
		t.Errorf("jailPath = %q", h.jailPath)
	}
	if filepath.Base(h.hostPath) != "fake-svc"+paths.ServiceEndpointExt {
		t.Errorf("hostPath = %q, want a .endpoint leaf", h.hostPath)
	}
}

// TestExternalServiceRemovesStaleEndpoint: a dead predecessor's endpoint file must be
// gone before the spawn.
//
// Otherwise the wait succeeds instantly against a file naming a port nobody is on,
// and every client in the jail dials a dead address for the container's whole life.
func TestExternalServiceRemovesStaleEndpoint(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	socketsDir := t.TempDir()
	if err := os.Chmod(socketsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(socketsDir, "fake-svc"+paths.ServiceEndpointExt)
	// A COMPLETE-looking predecessor: a real listener's publication, then the
	// listener is closed so the port is dead.
	dead, err := svcendpoint.Listen(stale, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatal(err)
	}
	// Close() unlinks, so re-publish the same bytes to simulate a crashed daemon
	// that left its file behind.
	ep, err := svcendpoint.Read(stale)
	if err != nil {
		t.Fatal(err)
	}
	_ = dead.Close()
	if err := svcendpoint.Publish(stale, ep); err != nil {
		t.Fatal(err)
	}
	// The fake daemon publishes NOTHING. If the stale file survived the spawn, the
	// wait would be satisfied by it and we would get a handle.
	h, ok := startExternalServiceHarness(t, socketsDir, `sleep 30`, loopholes.TransportLoopbackTLS)
	if ok {
		if h.stop != nil {
			h.stop()
		}
		t.Fatal("a stale predecessor endpoint satisfied the wait; it must be removed before the spawn")
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("the stale endpoint file still exists after the spawn attempt")
	}
}

// TestExternalServiceReportsCrashImmediately: a daemon that exits non-zero at
// startup is reported the moment it dies, with its exit status — not after the
// full readiness deadline.
//
// The old early-exit check read cmd.ProcessState, which only cmd.Wait() populates
// and nothing called, so it was dead code: every crashed daemon silently burned
// the whole 5s deadline, serially, one per daemon.
func TestExternalServiceReportsCrashImmediately(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	socketsDir := t.TempDir()
	var buf strings.Builder
	o := &Options{}
	fillDefaults(o)
	o.Stdout = &buf
	spec := jsonx.NewOrderedMap()
	spec.Set("command", []any{"sh", "-c", "exit 3"})
	start := time.Now()
	_, ok := o.startExternalService("fake-svc", spec, socketsDir,
		loopholes.TransportLoopbackTLS, "127.0.0.1", nil)
	elapsed := time.Since(start)
	if ok {
		t.Fatal("an instantly-exiting daemon produced a handle")
	}
	if elapsed >= 4*time.Second {
		t.Errorf("failure took %v; a crashed daemon must be reported immediately, "+
			"not after the readiness deadline", elapsed)
	}
	out := buf.String()
	for _, want := range []string{"fake-svc", "host-service-fake-svc.log", "exit status 3"} {
		if !strings.Contains(out, want) {
			t.Errorf("crash warning missing %q; got %q", want, out)
		}
	}
}

// TestExternalServiceWarnsOnReadinessTimeout: a daemon that starts and never
// publishes must produce a WARNING naming the loophole, the awaited path, and its
// log file. Before this existed the timeout branch returned false with no output
// at all — the printer was plumbed to the call site and discarded — so the
// failure was completely silent until the agent hit it in-jail.
func TestExternalServiceWarnsOnReadinessTimeout(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	socketsDir := t.TempDir()
	var buf strings.Builder
	o := &Options{}
	fillDefaults(o)
	o.Stdout = &buf
	o.ServiceReadyTimeout = 300 * time.Millisecond
	spec := jsonx.NewOrderedMap()
	spec.Set("command", []any{"sh", "-c", "sleep 30"})
	h, ok := o.startExternalService("fake-svc", spec, socketsDir,
		loopholes.TransportLoopbackTLS, "127.0.0.1", nil)
	if ok {
		if h.stop != nil {
			h.stop()
		}
		t.Fatal("a daemon that never published satisfied the wait")
	}
	out := buf.String()
	wantPath := filepath.Join(socketsDir, "fake-svc"+paths.ServiceEndpointExt)
	for _, want := range []string{"Warning", "fake-svc", wantPath, "host-service-fake-svc.log"} {
		if !strings.Contains(out, want) {
			t.Errorf("timeout warning missing %q; got %q", want, out)
		}
	}
}

// frontUpstreamChildMain is the publishes:"socket" daemon child (dispatched from
// TestMain): it binds a REAL AF_UNIX socket at socketPath and serves a trivial
// protocol. It deliberately does NOT unlink a pre-existing file first — the
// framework owns stale-socket retirement (the pre-spawn unlink), so binding
// fresh here also asserts that retirement happened.
//
// mode "line": read one newline-terminated request, answer "pong\n" to "ping".
// mode "eof": read the request TO EOF (the request_end:"eof" daemon shape),
// answer "got:<request>".
func frontUpstreamChildMain(mode, socketPath string) int {
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "front-upstream-child:", err)
		return 1
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			return 0
		}
		go func(c net.Conn) {
			defer c.Close()
			if mode == "eof" {
				req, err := io.ReadAll(c)
				if err != nil {
					return
				}
				_, _ = c.Write(append([]byte("got:"), req...))
				return
			}
			line, err := bufio.NewReader(c).ReadString('\n')
			if err != nil {
				return
			}
			if strings.TrimSpace(line) == "ping" {
				_, _ = c.Write([]byte("pong\n"))
			}
		}(conn)
	}
}

// TestFrontedServiceComesUpBehindFront: a daemon declaring publishes:"socket"
// binds a plain unix socket, and yolo waits for it BY CONNECT, then runs the
// svcendpoint front and publishes the endpoint file itself — so the jail sees
// exactly what a self-publishing daemon gives it: the same env var name, the
// same in-jail endpoint path, dialable with the same client
// (loophole-packaging.md §2.1).
func TestFrontedServiceComesUpBehindFront(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	socketsDir := t.TempDir()
	if err := os.Chmod(socketsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	o := &Options{}
	fillDefaults(o)
	o.Stdout = &buf
	spec := jsonx.NewOrderedMap()
	spec.Set("command", []any{os.Args[0], "-front-upstream-child", "line", "{socket}"})
	hd := &loopholes.HostDaemon{
		Publishes:  loopholes.PublishesSocket,
		RequestEnd: loopholes.RequestEndFramed,
	}
	h, ok := o.startExternalService("fronted", spec, socketsDir,
		loopholes.TransportLoopbackTLS, "127.0.0.1", hd)
	if !ok {
		t.Fatalf("fronted service failed to come up; output: %q", buf.String())
	}
	stopped := false
	defer func() {
		if !stopped {
			h.stop()
		}
	}()

	// The jail-facing shape is IDENTICAL to a self-publishing loopback-TLS
	// daemon's: endpoint env var, endpoint jail path, endpoint file in the
	// mounted services dir.
	if h.envVarName != "YOLO_SERVICE_FRONTED_ENDPOINT" {
		t.Errorf("envVarName = %q, want YOLO_SERVICE_FRONTED_ENDPOINT", h.envVarName)
	}
	if h.jailPath != hostServiceEndpointPath("fronted") {
		t.Errorf("jailPath = %q, want %q", h.jailPath, hostServiceEndpointPath("fronted"))
	}
	wantEndpoint := filepath.Join(socketsDir, "fronted"+paths.ServiceEndpointExt)
	if h.hostPath != wantEndpoint {
		t.Errorf("hostPath = %q, want %q", h.hostPath, wantEndpoint)
	}
	// The upstream socket lives OUTSIDE the mounted services dir: the dir holds
	// endpoint files and nothing else, or the retired socket transport would
	// stay reachable from inside the jail.
	upstream := frontSocketFile(frontShortHash(socketsDir), "fronted")
	if !fileExists(upstream) {
		t.Errorf("upstream socket %q missing while the service is up", upstream)
	}
	entries, err := os.ReadDir(socketsDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sock") {
			t.Errorf("a socket %q leaked into the mounted services dir", e.Name())
		}
	}

	// A request round-trips through the front: pinned TLS + token auth from the
	// endpoint file, spliced to the daemon's plain socket.
	conn, err := svcendpoint.DialLocal(h.hostPath, 5*time.Second)
	if err != nil {
		t.Fatalf("dial through the front: %v", err)
	}
	boundConn(t, conn)
	if _, err := conn.Write([]byte("ping\n")); err != nil {
		t.Fatal(err)
	}
	reply, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read through the front: %v", err)
	}
	_ = conn.Close()
	if strings.TrimSpace(reply) != "pong" {
		t.Errorf("reply = %q, want pong", reply)
	}

	// Teardown retires all three artifacts: the endpoint file (the credential),
	// the daemon group, and the upstream socket a SIGKILLed daemon cannot unlink.
	h.stop()
	stopped = true
	if fileExists(h.hostPath) {
		t.Error("endpoint file survived teardown")
	}
	if fileExists(upstream) {
		t.Error("upstream socket survived teardown")
	}
}

// TestConfigLoopholeComesUpBehindFront is the end-to-end proof of the
// discover.go flip (loophole-packaging.md §2.2): a yolo-jail.jsonc `loopholes:`
// entry whose daemon binds a plain unix socket, driven through the REAL
// pipeline — Discover synthesis, startLoopholes' spec/transport/daemon plumbing,
// the fronted spawn — comes up with a published endpoint file, dialable via
// svcendpoint, request round-tripping. And it gets its env var + endpoint file
// exactly like a manifest loophole: the generic handle emission is what mounts
// and advertises it, so the handle's fields ARE the contract (R11).
func TestConfigLoopholeComesUpBehindFront(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	emptyLoopholeDirs(t)

	loopCfg := jsonx.NewOrderedMap()
	entry := jsonx.NewOrderedMap()
	entry.Set("description", "echo daemon on a plain unix socket")
	entry.Set("command", []any{os.Args[0], "-front-upstream-child", "line", "{socket}"})
	loopCfg.Set("echoer", entry)
	cfg := newConfig("loopholes", loopCfg)

	o := &Options{}
	fillDefaults(o)
	var buf strings.Builder
	o.Stdout = &buf
	// No cgroup delegate (its gate is a PathExists probe), no containers to
	// enumerate (Ran:false), no journal (absent key).
	o.PathExists = func(string) bool { return false }
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{} }

	cname := "yolo-e2e-" + sha1Hex8(home)
	socketsDir := hostServiceSocketsDir(cname, false)
	handles := o.startLoopholes(cname, "podman", cfg)
	stopped := false
	stopAll := func() {
		if !stopped {
			stopped = true
			o.stopLoopholes(handles, socketsDir, cname, "podman")
		}
	}
	defer stopAll()

	var h *loopholeDaemon
	for i := range handles {
		if handles[i].name == "echoer" {
			h = &handles[i]
		}
	}
	if h == nil {
		t.Fatalf("no handle for the config loophole; handles=%v output=%q", handles, buf.String())
	}

	// R11: the jail-facing contract is IDENTICAL to a manifest loophole's — the
	// endpoint env var (run.go inserts `-e envVarName=jailPath` generically for
	// every handle), the in-jail endpoint path, and the endpoint file inside the
	// mounted services dir.
	if h.envVarName != "YOLO_SERVICE_ECHOER_ENDPOINT" {
		t.Errorf("envVarName = %q, want YOLO_SERVICE_ECHOER_ENDPOINT", h.envVarName)
	}
	if h.jailPath != hostServiceEndpointPath("echoer") {
		t.Errorf("jailPath = %q, want %q", h.jailPath, hostServiceEndpointPath("echoer"))
	}
	wantEndpoint := filepath.Join(socketsDir, "echoer"+paths.ServiceEndpointExt)
	if h.hostPath != wantEndpoint {
		t.Errorf("hostPath = %q, want %q (inside the mounted services dir)", h.hostPath, wantEndpoint)
	}
	if !svcendpoint.Probe(h.hostPath) {
		t.Fatalf("endpoint file %q not published/complete", h.hostPath)
	}

	// The request round-trips through the front to the daemon's plain socket.
	conn, err := svcendpoint.DialLocal(h.hostPath, 5*time.Second)
	if err != nil {
		t.Fatalf("dial through the front: %v", err)
	}
	if _, err := conn.Write([]byte("ping\n")); err != nil {
		t.Fatal(err)
	}
	reply, err := bufio.NewReader(conn).ReadString('\n')
	_ = conn.Close()
	if err != nil {
		t.Fatalf("read through the front: %v", err)
	}
	if strings.TrimSpace(reply) != "pong" {
		t.Errorf("reply = %q, want pong", reply)
	}

	upstream := frontSocketFile(frontShortHash(socketsDir), "echoer")
	if !fileExists(upstream) {
		t.Errorf("upstream socket %q missing while the service is up", upstream)
	}
	stopAll()
	if fileExists(h.hostPath) {
		t.Error("endpoint file survived teardown")
	}
	if fileExists(upstream) {
		t.Error("upstream socket survived teardown")
	}
	if fileExists(socketsDir) {
		t.Error("sockets dir survived teardown")
	}
}

// TestManifestEOFDaemonRoundTripsBehindFront: a MANIFEST loophole declaring
// publishes:"socket" + request_end:"eof" — loaded through the real loader, so
// the enum parsing, the {socket} survival, and the FrontOptions wiring are all
// on the path — serves a daemon that reads its request TO EOF. Without the
// half-close mapping this daemon works on a bare socket and hangs forever
// behind the front (loophole-packaging.md §2.1b hazard 2).
func TestManifestEOFDaemonRoundTripsBehindFront(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	userDir := t.TempDir()
	mod := filepath.Join(userDir, "eofd")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "name": "eofd",
  "description": "reads its request to EOF",
  "host_daemon": {
    "cmd": [` + fmt.Sprintf("%q", os.Args[0]) + `, "-front-upstream-child", "eof", "{socket}"],
    "publishes": "socket",
    "request_end": "eof"
  }
}`
	if err := os.WriteFile(filepath.Join(mod, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	discovered := loopholes.Discover(loopholes.DiscoverOptions{
		Root: userDir, RootSet: true, IncludeBundled: false,
	})
	if len(discovered) != 1 || discovered[0].HostDaemon == nil {
		t.Fatalf("discovered %+v, want the one eofd loophole", discovered)
	}
	lp := discovered[0]

	socketsDir := t.TempDir()
	if err := os.Chmod(socketsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	spec := jsonx.NewOrderedMap()
	spec.Set("command", toAnyList(lp.HostDaemon.Cmd))
	o := &Options{}
	fillDefaults(o)
	var buf strings.Builder
	o.Stdout = &buf
	h, ok := o.startExternalService("eofd", spec, socketsDir,
		lp.Transport, "127.0.0.1", lp.HostDaemon)
	if !ok {
		t.Fatalf("eof daemon failed to come up; output: %q", buf.String())
	}
	defer h.stop()

	conn, err := svcendpoint.DialLocal(h.hostPath, 5*time.Second)
	if err != nil {
		t.Fatalf("dial through the front: %v", err)
	}
	boundConn(t, conn)
	if _, err := conn.Write([]byte("payload")); err != nil {
		t.Fatal(err)
	}
	// End the request direction; the eof front must half-close upstream so the
	// daemon's read-to-EOF returns and the response flows back.
	type closeWriter interface{ CloseWrite() error }
	if err := conn.(closeWriter).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	resp, err := io.ReadAll(conn)
	_ = conn.Close()
	if err != nil {
		t.Fatalf("reading the response failed or timed out — the request_end:\"eof\" "+
			"front did not half-close the upstream socket, so the daemon's "+
			"read-to-EOF never returned: %v", err)
	}
	if got := string(resp); got != "got:payload" {
		t.Errorf("response = %q, want %q", got, "got:payload")
	}
}

// boundConn bounds every read on a conn dialed through svcendpoint: DialLocal's
// handshake clears the dial deadlines it set, so without this a lost EOF anywhere
// in client → front → upstream half-close → daemon blocks io.ReadAll forever and
// the whole PACKAGE dies on go test's 10-minute timeout panic. That failure names
// no cause and truncates the goroutine dump; a deadline turns the same regression
// into a one-line assertion. (Observed once as a nondeterministic 9m49s hang in
// TestManifestEOFDaemonRoundTripsBehindFront.)
func boundConn(t *testing.T, conn net.Conn) {
	t.Helper()
	// 10s: the round trip is milliseconds once readiness has been waited for, and a
	// bound that is generous by three orders of magnitude still cannot flake under
	// load — but it does have to be a bound.
	d := 10 * time.Second
	if td, ok := t.Deadline(); ok {
		if left := time.Until(td) - time.Second; left > 0 && left < d {
			d = left
		}
	}
	if err := conn.SetDeadline(time.Now().Add(d)); err != nil {
		t.Fatal(err)
	}
}

// toAnyList converts a string slice into the []any shape a decoded config
// carries.
func toAnyList(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// TestFrontedServiceStaleUpstreamNeitherSatisfiesNorBlocks: the upstream wait is
// a CONNECT with the stale file unlinked pre-spawn — a leftover from a SIGKILLed
// predecessor must neither satisfy the wait instantly (the §2.1b hazard: front
// publishes, Probe succeeds, jail authenticates, every request dropped at the
// dial) nor fail the fresh daemon's bind with EADDRINUSE.
func TestFrontedServiceStaleUpstreamNeitherSatisfiesNorBlocks(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	socketsDir := t.TempDir()
	var buf strings.Builder
	o := &Options{}
	fillDefaults(o)
	o.Stdout = &buf
	o.ServiceReadyTimeout = 300 * time.Millisecond
	upstream := frontSocketFile(frontShortHash(socketsDir), "fake-svc")
	if err := os.WriteFile(upstream, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(upstream)
	// The daemon never binds anything. If the wait were existence-based, the
	// stale file would satisfy it instantly and we would get a handle.
	spec := jsonx.NewOrderedMap()
	spec.Set("command", []any{"sh", "-c", "sleep 30"})
	hd := &loopholes.HostDaemon{Publishes: loopholes.PublishesSocket}
	h, ok := o.startExternalService("fake-svc", spec, socketsDir,
		loopholes.TransportLoopbackTLS, "127.0.0.1", hd)
	if ok {
		h.stop()
		t.Fatal("a stale upstream file satisfied the readiness wait; it must be a CONNECT")
	}
	if fileExists(upstream) {
		t.Error("the stale upstream file survived the spawn; it must be unlinked " +
			"BEFORE the spawn or the fresh daemon's bind fails with EADDRINUSE")
	}
	out := buf.String()
	for _, want := range []string{"Warning", "fake-svc", upstream, "host-service-fake-svc.log"} {
		if !strings.Contains(out, want) {
			t.Errorf("upstream-wait warning missing %q; got %q", want, out)
		}
	}
}

// TestStopLoopholesRetiresFrontSockets: a jail that simply ENDS (no relaunch)
// must not leave fronted daemons' upstream sockets in /tmp — stopLoopholes
// covers them beside the relay's socket, which the sockets-dir rmtree no longer
// reaches since both went host-only.
func TestStopLoopholesRetiresFrontSockets(t *testing.T) {
	shortHash := sha1Hex8(t.TempDir())
	socketsDir := filepath.Join(t.TempDir(), hostServicesDirPrefix+shortHash)
	if err := os.MkdirAll(socketsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sock := frontSocketFile(shortHash, "fake-svc")
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(sock)
	o := &Options{}
	fillDefaults(o)
	o.Stdout = io.Discard
	o.PIDAlive = func(int) bool { return false }
	o.stopLoopholes(nil, socketsDir, "", "podman")
	if fileExists(sock) {
		t.Errorf("front socket %q survived stopLoopholes", sock)
	}
	if fileExists(socketsDir) {
		t.Errorf("sockets dir %q survived stopLoopholes", socketsDir)
	}
}

// TestExternalServiceTeardownKillsProcessGroup: stop() must signal the daemon's
// whole PROCESS GROUP, not just the direct child.
//
// The spawn sets Setsid, so the daemon leads its own group — and the old
// teardown signalled cmd.Process alone, so anything the daemon forked survived
// deselection, the lockfile entry, and `yolo loopholes list` knowing the name
// (loophole-packaging.md §4.5).
func TestExternalServiceTeardownKillsProcessGroup(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	socketsDir := t.TempDir()
	real := filepath.Join(t.TempDir(), "seed")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	seed := filepath.Join(real, "seed.endpoint")
	ln, err := svcendpoint.Listen(seed, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	pidFile := filepath.Join(t.TempDir(), "forked.pid")
	// The daemon FORKS a sleeper, records its pid, publishes, and keeps running.
	h, ok := startExternalServiceHarness(t, socketsDir,
		`sleep 300 & echo $! > `+pidFile+`; cp `+seed+` "$1"; exec sleep 300`,
		loopholes.TransportLoopbackTLS)
	if !ok {
		t.Fatal("the harness daemon never became ready")
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the daemon never recorded its forked child: %v", err)
	}
	forked, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	h.stop()
	// The forked child must be dead (or a zombie awaiting its reaper — its
	// parent died with the group, so /proc state Z is as dead as it gets here).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, alive := procState(forked)
		if !alive || state == "Z" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	state, _ := procState(forked)
	t.Fatalf("forked child %d still alive (state %q) after teardown; "+
		"stop() must kill the process GROUP", forked, state)
}

// procState reads /proc/<pid>/stat's state field; alive=false when the process
// is gone entirely.
func procState(pid int) (state string, alive bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", false
	}
	// The state is the first field after the parenthesised comm.
	s := string(data)
	if i := strings.LastIndexByte(s, ')'); i >= 0 && i+2 < len(s) {
		return string(s[i+2]), true
	}
	return "?", true
}

// TestNoTokenInLaunchArgv: no bearer token ever crosses the container launch argv
// or the container's environment.
//
// This is the POSITIVE form of a property #32 tried to protect with an argv-redacting
// debug helper. With the token delivered only inside the 0600 endpoint file, there is
// nothing to redact — so the invariant is asserted directly instead, and it cannot
// creep back: YOLO_DEBUG prints the whole argv verbatim, and a token there would land
// in logs and transcripts that no secret scan of ours would catch.
func TestNoTokenInLaunchArgv(t *testing.T) {
	ws := "/ws"
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions(ws, home)
	// PathExists true so the broker/host-services env is emitted at all — the
	// assertion is about what that env CONTAINS, so it must not be vacuous.
	o.PathExists = func(string) bool { return true }

	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	cfg := newConfig("agents", []any{"claude"}, "security", sec)
	in := &assembleInput{
		cfg: cfg, rt: "podman", cname: "yolo-ws-abcd1234",
		packs: claudePackFixture(t), agentsPath: "/agents/yolo-ws-abcd1234",
		wsState: "/ws/.yolo/home", miseStore: "/mise-store", yoloVersion: "9.9.9-test",
		mountTargets: map[string]struct{}{}, lspNPMInstall: "", lspGoInstall: "",
	}
	argv := o.assembleRunCmd(in)

	hex64 := regexp.MustCompile(`\b[0-9a-f]{64}\b`)
	sawHostServices := false
	for i, a := range argv {
		if strings.Contains(a, "yolo-host-services-") || strings.Contains(a, "YOLO_SERVICE_") {
			sawHostServices = true
		}
		if hex64.MatchString(a) {
			t.Errorf("argv[%d] contains a 64-hex run: %q", i, a)
		}
		if strings.Contains(a, "_TOKEN=") {
			t.Errorf("argv[%d] carries a token env var: %q", i, a)
		}
	}
	if !sawHostServices {
		t.Fatal("the assembled argv mentions no host services at all; the assertion above proved nothing")
	}
}

// TestAdvertiseHostFollowsTheNetworkNamespace: what a loopback-TLS daemon publishes
// must match whether the jail will SHARE the launcher's network namespace.
//
// This is not cosmetic. A daemon binds the launcher's 127.0.0.1. When the jail shares
// that namespace — `--net=host`, forced for podman-in-podman and selectable as
// network.mode: "host" — the gateway name resolves to the launcher's own upstream
// host, where nothing is listening, and every dial is refused. Measured that way in a
// nested jail before this existed. The two decisions live in different files
// (assemble.go writes the flag, this picks the address) so they must be pinned
// together.
func TestAdvertiseHostFollowsTheNetworkNamespace(t *testing.T) {
	bridge := jsonx.NewOrderedMap()
	hostNet := jsonx.NewOrderedMap()
	netSec := jsonx.NewOrderedMap()
	netSec.Set("mode", "host")
	hostNet.Set("network", netSec)

	cases := []struct {
		name        string
		rt          string
		cfg         *jsonx.OrderedMap
		inContainer bool
		want        string
	}{
		// Bridge on a real host: the gateway name is right, so leave the default.
		{"podman bridge on a host", "podman", bridge, false, ""},
		// Nested: the assembler forces --net=host, so the jail's loopback IS ours.
		{"podman nested (net=host forced)", "podman", bridge, true, "127.0.0.1"},
		// Explicit host networking: same shared namespace, same answer.
		{"network.mode host", "podman", hostNet, false, "127.0.0.1"},
		// Apple Container gets no host services at all.
		{"apple container", "container", bridge, false, ""},
	}
	for _, tc := range cases {
		o := &Options{}
		fillDefaults(o)
		o.IsMacOS = false
		o.Network = "bridge"
		o.PathExists = func(string) bool { return tc.inContainer }
		if got := o.advertiseHostFor(tc.rt, tc.cfg); got != tc.want {
			t.Errorf("%s: advertiseHostFor = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The §4.3a placement rule at the SPAWN: a daemon program inside the workspace
// this launch mounts :rw is one the agent rewrites, so it is refused instead of
// started. This is the face config validation cannot cover — a manifest's
// host_daemon.cmd never passes through the config validator at all.
func TestExternalServiceRefusesADaemonInsideTheWorkspace(t *testing.T) {
	ws := t.TempDir()
	daemon := filepath.Join(ws, "tool.py")
	if err := os.WriteFile(daemon, []byte("#!/bin/sh\nsleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	o := &Options{Workspace: ws}
	fillDefaults(o)
	o.Stdout = &buf
	spec := jsonx.NewOrderedMap()
	spec.Set("command", []any{daemon, "--socket", "{socket}"})

	if _, ok := o.startExternalService("wsdaemon", spec, t.TempDir(),
		loopholes.TransportLoopbackTLS, "127.0.0.1", nil); ok {
		t.Fatal("a daemon inside the mounted workspace must not be spawned")
	}
	for _, want := range []string{"wsdaemon", daemon, "§4.3a"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("refusal %q does not name %q", buf.String(), want)
		}
	}
}

// Control: the same spec outside both agent-writable trees still starts, and the
// bundled ["yolo", …] shape is untouched by the check — it runs BEFORE SelfExecArgv
// precisely so yolo's own path (which lives in the workspace during nested-jail
// verification) cannot trip it.
func TestExternalServiceStartsADaemonOutsideTheWorkspace(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a host process")
	}
	socketsDir := t.TempDir()
	if err := os.Chmod(socketsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	o := &Options{Workspace: t.TempDir()}
	fillDefaults(o)
	o.Stdout = &buf
	spec := jsonx.NewOrderedMap()
	spec.Set("command", []any{os.Args[0], "-front-upstream-child", "line", "{socket}"})
	hd := &loopholes.HostDaemon{
		Publishes:  loopholes.PublishesSocket,
		RequestEnd: loopholes.RequestEndFramed,
	}
	h, ok := o.startExternalService("outside", spec, socketsDir,
		loopholes.TransportLoopbackTLS, "127.0.0.1", hd)
	if !ok {
		t.Fatalf("a daemon outside the workspace must still start; output: %q", buf.String())
	}
	h.stop()
}

// TestExternalServiceAcceptsADaemonizingWrapper: the R1 refinement. A daemon whose
// launcher FORKS the real server and exits 0 is a legitimate shape (every
// daemonizing wrapper), so a clean pre-readiness exit must keep polling to the
// deadline rather than be judged a failure — the crash report is for a NON-zero
// exit. Without the status check, this whole class of daemon was refused the
// moment the crash-report fix landed.
func TestExternalServiceAcceptsADaemonizingWrapper(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	socketsDir := t.TempDir()
	real := filepath.Join(t.TempDir(), "seed")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	seed := filepath.Join(real, "seed.endpoint")
	ln, err := svcendpoint.Listen(seed, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	// The wrapper publishes from a BACKGROUND child, then exits 0 immediately —
	// so the exited channel closes well before the endpoint appears.
	h, ok := startExternalServiceHarness(t, socketsDir,
		`( sleep 0.4; cp `+seed+` "$1" ) & exit 0`, loopholes.TransportLoopbackTLS)
	if !ok {
		t.Fatal("a wrapper that exits 0 after backgrounding its publisher must still " +
			"reach readiness; the clean exit is not a failure")
	}
	h.stop()
}

// TestExternalServiceReportsCleanExitWithNoService: the other side of that
// refinement. A daemon that exits 0 and publishes NOTHING must still be reported,
// with its own message — "exited (status 0)" rather than the crash text or the bare
// timeout text, because the two have different causes and different fixes.
func TestExternalServiceReportsCleanExitWithNoService(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	socketsDir := t.TempDir()
	var buf strings.Builder
	o := &Options{}
	fillDefaults(o)
	o.Stdout = &buf
	o.ServiceReadyTimeout = 300 * time.Millisecond
	spec := jsonx.NewOrderedMap()
	spec.Set("command", []any{"sh", "-c", "exit 0"})
	if _, ok := o.startExternalService("quiet-svc", spec, socketsDir,
		loopholes.TransportLoopbackTLS, "127.0.0.1", nil); ok {
		t.Fatal("a daemon that published nothing produced a handle")
	}
	for _, want := range []string{"quiet-svc", "exited (status 0)", "never became reachable"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("warning %q does not carry %q", buf.String(), want)
		}
	}
}
