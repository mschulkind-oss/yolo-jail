package run

import (
	"bufio"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// singletonFixture stands up the state a HOST-WIDE daemon that is ALREADY RUNNING
// leaves on the machine: a bound socket at the framework-derived path, and a pid
// file naming a live process (this one). broker.BrokerIsAlive reads exactly those
// four facts, so a startHostSingleton driven against this fixture takes the "no-op,
// it is already up" branch — which is the branch every jail after the first takes.
//
// The loophole NAME is per-test and unique, because these paths are fixed by
// derivation (that is their whole point) and a test must not collide with the real
// /tmp/yolo-claude-oauth-broker.* of the machine it runs on.
func singletonFixture(t *testing.T, name string) net.Listener {
	t.Helper()
	sock := paths.HostSingletonSocket(name)
	assertSockPathFits(t, sock)
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("binding the fixture singleton socket %s: %v", sock, err)
	}
	t.Cleanup(func() { _ = ln.Close(); _ = os.Remove(sock) })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				// THE PREAMBLE FIRST, exactly as a real fronted daemon does. It is
				// consumed here rather than ignored because ignoring it would make
				// this fixture pass whether or not yolo sent one — and the preamble
				// is what carries the host-asserted jail identity that replaced the
				// deleted relay's payload stamp (invariant I1).
				var hdr [4]byte
				if _, err := io.ReadFull(br, hdr[:]); err != nil {
					return
				}
				body := make([]byte, binary.BigEndian.Uint32(hdr[:]))
				if _, err := io.ReadFull(br, body); err != nil {
					return
				}
				if !strings.Contains(string(body), `"v":1`) {
					return
				}
				line, err := br.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimSpace(line) == "ping" {
					_, _ = c.Write([]byte("pong\n"))
				}
			}(conn)
		}
	}()
	pidFile := paths.HostSingletonPIDFile(name)
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Remove(pidFile)
		_ = os.Remove(paths.HostSingletonLock(name))
	})
	return ln
}

// hostScopedSpec is a spec whose command MUST NOT RUN: it touches markerPath, so
// any test that ends up spawning leaves evidence. Every assertion below about "the
// daemon was ensured, not spawned" reads that marker.
func hostScopedSpec(markerPath string) *jsonx.OrderedMap {
	spec := jsonx.NewOrderedMap()
	spec.Set("command", []any{"/bin/touch", markerPath})
	return spec
}

func hostScopedDaemon() *loopholes.HostDaemon {
	return &loopholes.HostDaemon{
		Publishes:  loopholes.PublishesSocket,
		RequestEnd: loopholes.RequestEndFramed,
		Preamble:   true,
		Scope:      loopholes.ScopeHost,
	}
}

// dialFrontLine sends one newline-terminated request through a published endpoint
// and returns the response line. The endpoint is the jail-facing half: pinned
// certificate, bearer token, then bytes.
func dialFrontLine(t *testing.T, endpointPath, req string) string {
	t.Helper()
	conn, err := svcendpoint.DialLocal(endpointPath, 3*time.Second)
	if err != nil {
		t.Fatalf("dialing %s: %v", endpointPath, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte(req + "\n")); err != nil {
		t.Fatalf("writing to %s: %v", endpointPath, err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("reading from %s: %v", endpointPath, err)
	}
	return strings.TrimSpace(line)
}

// TestHostScopedDaemonIsEnsuredNotSpawned is THE property `host_daemon.scope`
// exists for, and the one whose absence is a data-loss bug rather than a
// performance one.
//
// The broker holds a flock that serializes Claude OAuth refreshes; Anthropic mints
// SINGLE-USE refresh tokens, so two brokers are not two daemons, they are two
// processes racing to burn the same token (docs/design/agent-credentials.md §2.5).
// The run pipeline used to protect that by testing the loophole's NAME. This asserts
// the declaration does the same job: a `scope: "host"` record whose daemon is
// already up gets FRONTED, and its command is never executed.
//
// The marker file is what makes the negative real. Asserting "the front came up"
// would pass just as well if yolo had spawned a second daemon beside the first.
func TestHostScopedDaemonIsEnsuredNotSpawned(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("binds host-wide /tmp singleton paths")
	}
	name := "yjtest-singleton-a"
	singletonFixture(t, name)

	socketsDir := t.TempDir()
	if err := os.Chmod(socketsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "spawned")
	var buf strings.Builder
	o := &Options{}
	fillDefaults(o)
	o.Stdout = &buf

	h, ok := o.startHostSingleton(name, hostScopedSpec(marker), socketsDir, "127.0.0.1", hostScopedDaemon())
	if !ok {
		t.Fatalf("the host-scoped service did not come up; output: %q", buf.String())
	}
	defer h.stop()

	if fileExists(marker) {
		t.Error("the host-scoped daemon's command RAN. A singleton that is already serving " +
			"another jail must be ensured, never spawned — a second broker holds a second " +
			"flock and the two burn the same single-use refresh token")
	}
	// It is fronted at the ordinary endpoint path, so the jail sees exactly what any
	// other loopback-TLS loophole gives it.
	wantEndpoint := filepath.Join(socketsDir, name+paths.ServiceEndpointExt)
	if h.hostPath != wantEndpoint {
		t.Errorf("hostPath = %q, want %q", h.hostPath, wantEndpoint)
	}
	if h.jailPath != hostServiceEndpointPath(name) {
		t.Errorf("jailPath = %q, want %q", h.jailPath, hostServiceEndpointPath(name))
	}
	if !svcendpoint.Probe(wantEndpoint) {
		t.Fatalf("no endpoint published at %s; output: %q", wantEndpoint, buf.String())
	}
	// And the bytes actually reach the host-wide daemon through it.
	if got := dialFrontLine(t, wantEndpoint, "ping"); got != "pong" {
		t.Errorf("round trip through the front = %q, want \"pong\"", got)
	}
}

// TestHostScopedHandleEmitsNoEnvVar: the handle carries an EMPTY envVarName, so
// run.go's insert loop skips it.
//
// A host-scoped loophole's jail-facing variable is emitted at ARGV-ASSEMBLY time by
// hostServicesMountArgs — optimistically, before the front has run at all — and that
// is deliberate: a launch whose front never publishes must be REFUSED by the in-jail
// reachability witness rather than quietly become a jail that was never told the
// service exists (loopback-tls-reachability.md §7.3, and the tests beside this one
// pin the optimism). Emitting it from the handle as well would put the same `-e`
// in the container argv twice.
func TestHostScopedHandleEmitsNoEnvVar(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("binds host-wide /tmp singleton paths")
	}
	name := "yjtest-singleton-b"
	singletonFixture(t, name)
	socketsDir := t.TempDir()
	if err := os.Chmod(socketsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	o := &Options{}
	fillDefaults(o)
	o.Stdout = &buf
	h, ok := o.startHostSingleton(name, hostScopedSpec(filepath.Join(t.TempDir(), "x")),
		socketsDir, "127.0.0.1", hostScopedDaemon())
	if !ok {
		t.Fatalf("did not come up; output: %q", buf.String())
	}
	defer h.stop()
	if h.envVarName != "" {
		t.Errorf("envVarName = %q; a host-scoped loophole's variable is emitted by "+
			"hostServicesMountArgs at argv-assembly time, so a handle carrying one puts "+
			"the same -e in the container argv twice", h.envVarName)
	}
}

// TestOneSingletonManyJails is the design property stated as a test: ONE daemon, N
// fronts, and a jail ending takes only its own.
//
// This is what the per-jail relay used to provide by having N relays, and it is the
// thing most easily broken by copying startExternalService's teardown — which
// SIGKILLs the daemon's process group and unlinks its socket. Doing that here would
// cut off every other live jail's credential path the moment any one jail exits, and
// nothing in a single-jail test would notice.
func TestOneSingletonManyJails(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("binds host-wide /tmp singleton paths")
	}
	name := "yjtest-singleton-c"
	singletonFixture(t, name)

	var buf strings.Builder
	o := &Options{}
	fillDefaults(o)
	o.Stdout = &buf

	start := func() (loopholeDaemon, string) {
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		h, ok := o.startHostSingleton(name, hostScopedSpec(filepath.Join(t.TempDir(), "x")),
			dir, "127.0.0.1", hostScopedDaemon())
		if !ok {
			t.Fatalf("a front did not come up; output: %q", buf.String())
		}
		return h, filepath.Join(dir, name+paths.ServiceEndpointExt)
	}

	jailA, endpointA := start()
	jailB, endpointB := start()
	defer jailB.stop()

	// Two SEPARATE credentials over one daemon. Same-file endpoints would mean two
	// jails sharing a bearer token.
	if endpointA == endpointB {
		t.Fatal("both jails published to the same endpoint file")
	}
	epA, err := svcendpoint.Read(endpointA)
	if err != nil {
		t.Fatal(err)
	}
	epB, err := svcendpoint.Read(endpointB)
	if err != nil {
		t.Fatal(err)
	}
	if epA.Token == epB.Token {
		t.Error("both jails were published the SAME bearer token; each front must mint its own")
	}
	if got := dialFrontLine(t, endpointA, "ping"); got != "pong" {
		t.Errorf("jail A round trip = %q", got)
	}
	if got := dialFrontLine(t, endpointB, "ping"); got != "pong" {
		t.Errorf("jail B round trip = %q", got)
	}

	// Jail A ends. Its endpoint is retired; the DAEMON and jail B are untouched.
	jailA.stop()
	if svcendpoint.Probe(endpointA) {
		t.Error("jail A's endpoint survived its stop(); the credential must die with the front")
	}
	if !fileExists(paths.HostSingletonSocket(name)) {
		t.Fatal("a jail ending unlinked the HOST-WIDE socket — every other jail on this " +
			"machine just lost its credential path, and the daemon cannot be reached again " +
			"until something rebinds it")
	}
	if got := dialFrontLine(t, endpointB, "ping"); got != "pong" {
		t.Errorf("jail B round trip after jail A ended = %q, want \"pong\" — one jail's "+
			"teardown must not reach the shared daemon", got)
	}
}

// TestStartLoopholesRoutesByScope pins the DISPATCH, not just the two functions it
// dispatches to.
//
// The branch it guards is a one-line predicate in startLoopholes, and the shape this
// repo has shipped five times is a callee that is correct and a caller that never
// reaches it. Deleting the scope test there would leave every test above green while
// the broker got SPAWNED per jail — the exact defect `scope` exists to prevent — so
// this drives the loop itself and asserts the daemon's command never ran.
func TestStartLoopholesRoutesByScope(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("binds host-wide /tmp singleton paths")
	}
	name := "yjtest-singleton-d"
	singletonFixture(t, name)
	marker := filepath.Join(t.TempDir(), "spawned")

	dir := t.TempDir()
	lp := filepath.Join(dir, name)
	if err := os.MkdirAll(lp, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name": "` + name + `", "description": "fixture", "version": 1,
	  "default_enabled": true, "transport": "` + loopholes.TransportLoopbackTLS + `",
	  "lifecycle": "spawned",
	  "host_daemon": {"cmd": ["/bin/touch", "` + marker + `"],
	                  "publishes": "socket", "scope": "host"}}`
	if err := os.WriteFile(filepath.Join(lp, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := loopholes.BundledLoopholesDir
	loopholes.BundledLoopholesDir = func() string { return dir }
	t.Cleanup(func() { loopholes.BundledLoopholesDir = orig })

	var buf strings.Builder
	o := &Options{}
	fillDefaults(o)
	o.Stdout = &buf
	o.IsMacOS = false

	handles := o.startLoopholes("yolo-scope-test", "podman", jsonx.NewOrderedMap())
	socketsDir := hostServiceSocketsDir("yolo-scope-test", false)
	t.Cleanup(func() {
		o.stopLoopholes(handles, socketsDir, "", "")
		_ = os.RemoveAll(socketsDir)
	})

	if fileExists(marker) {
		t.Fatal("startLoopholes SPAWNED a scope:\"host\" daemon. The dispatch no longer " +
			"consults hd.Scope, so the broker would run once per jail and the refresh " +
			"flock would be held by N processes against each other")
	}
	var found *loopholeDaemon
	for i := range handles {
		if handles[i].name == name {
			found = &handles[i]
		}
	}
	if found == nil {
		t.Fatalf("startLoopholes returned no handle for the host-scoped loophole, so its "+
			"front is never closed at teardown; output: %q", buf.String())
	}
	if !svcendpoint.Probe(found.hostPath) {
		t.Errorf("no endpoint published at %s; output: %q", found.hostPath, buf.String())
	}
}

// TestHostScopedBrokerDaemonAnswersThroughTheFront is the END-TO-END check for the
// half of the conversion that no manifest assertion and no path assertion can see:
// the REAL broker daemon, ENSURED by the spawn path from a `scope: "host"` record,
// answering a real request through the front yolo published for a jail.
//
// # Why the real daemon
//
// The conversion moved internal/oauthbroker from hostservice.ServeUnix to
// ServeFrontedUnix. Get that wrong in the "forgot to change it" direction and the
// preamble the front now prepends is consumed AS the client's request: the daemon
// answers the wrong question, the terminator's real request is never read, and
// every Claude OAuth refresh in every jail fails — on a connection both ends believe
// is healthy, with a published endpoint, a live daemon and a passing readiness
// probe. Nothing else in the tree can see that. The manifest tests see a manifest;
// the path tests see paths; the fixture-daemon tests above see a daemon that was
// written to agree with whatever yolo does.
//
// # What it deliberately does NOT use
//
// The loophole is named `yjtest-broker`, not `claude-oauth-broker`. The singleton's
// socket, pid and lock are derived from the NAME (that is the design), so running
// this under the real name would ensure a daemon at the real
// /tmp/yolo-claude-oauth-broker.sock — killing or replacing the developer's own
// broker, with a state dir full of this test's dummy certificates. The argv is the
// SHIPPED one apart from the socket, and TestMain dispatches it to the real
// oauthbroker.Main.
func TestHostScopedBrokerDaemonAnswersThroughTheFront(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process and binds /tmp singleton paths")
	}
	name := "yjtest-broker"
	// Dummy CA/leaf so EnsureCAAndLeaf short-circuits and the test needs no openssl.
	// The daemon never reads them on the ping path.
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

	sock := paths.HostSingletonSocket(name)
	assertSockPathFits(t, sock)
	_ = os.Remove(sock)
	t.Cleanup(func() {
		// The daemon is detached (Setsid), so nothing reaps it for us. BrokerSpawn
		// wrote its pid where the derivation says, which is also what makes a second
		// launch on this host find it rather than start another.
		if raw, err := os.ReadFile(paths.HostSingletonPIDFile(name)); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && pid > 0 {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
		for _, p := range []string{sock, paths.HostSingletonPIDFile(name), paths.HostSingletonLock(name)} {
			_ = os.Remove(p)
		}
	})

	socketsDir := t.TempDir()
	if err := os.Chmod(socketsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	spec := jsonx.NewOrderedMap()
	spec.Set("command", []any{
		"yolo", "internal", "daemon", "claude-oauth-broker",
		"--socket", "{socket}", "--no-background-refresh",
	})
	var buf strings.Builder
	o := &Options{}
	fillDefaults(o)
	o.Stdout = &buf

	h, ok := o.startHostSingleton(name, spec, socketsDir, "127.0.0.1", hostScopedDaemon())
	if !ok {
		t.Fatalf("the broker daemon did not come up behind the front; output: %q", buf.String())
	}
	defer h.stop()

	endpoint := filepath.Join(socketsDir, name+paths.ServiceEndpointExt)
	if !svcendpoint.Probe(endpoint) {
		t.Fatalf("no endpoint published at %s; output: %q", endpoint, buf.String())
	}

	// THE ASSERTION: a framed {"action":"ping"} sent as a jail sends it — through
	// the endpoint, so the front writes its preamble ahead of these bytes — comes
	// back as a pong. Under ServeUnix the daemon would read the PREAMBLE as this
	// request and answer something else entirely.
	conn, err := svcendpoint.DialLocal(endpoint, 3*time.Second)
	if err != nil {
		t.Fatalf("dialing %s: %v", endpoint, err)
	}
	defer conn.Close()
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
	if !strings.Contains(string(payload), "pong") {
		t.Errorf("the broker did not pong through the front: %q\nThat is what a daemon "+
			"still on hostservice.ServeUnix looks like: it consumed yolo's connection "+
			"preamble AS this request, so every jail's OAuth refresh would fail on a "+
			"connection both ends believe is healthy.", string(payload))
	}
}
