package run

import (
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
	// value is a socket path must not advertise an endpoint file. It disappears with
	// the last unix-socket service; until then the two names must not be one.
	if got := hostServiceSocketEnvVar("journal"); got != "YOLO_SERVICE_JOURNAL_SOCKET" {
		t.Errorf("hostServiceSocketEnvVar(journal) = %q", got)
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
	return o.startExternalService("fake-svc", spec, socketsDir, transport, "127.0.0.1")
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
		loopholes.TransportLoopbackTLS, "127.0.0.1")
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
		loopholes.TransportLoopbackTLS, "127.0.0.1")
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
