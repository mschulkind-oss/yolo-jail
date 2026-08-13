package run

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/oauthterminator"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// TestBrokerEndpointEnvVarDoesNotDrift ties the run pipeline's PRODUCER to the
// in-jail terminator's CONSUMER, across two packages that never reference each
// other at runtime.
//
// Nothing else catches a divergence: the producer writes an env var into a
// container's argv and the consumer reads it inside that container, so a mismatch
// compiles, links, launches, and then exits 2 with "no host broker endpoint
// available" — which reads as a missing loophole, not a typo. That is precisely
// how a drifted name once silently disabled the cgroup delegate in every jail.
func TestBrokerEndpointEnvVarDoesNotDrift(t *testing.T) {
	if got, want := hostServiceEnvVar(broker.BrokerLoopholeName), oauthterminator.BrokerEndpointEnv; got != want {
		t.Errorf("run emits %q; the terminator reads %q", got, want)
	}
	if want := "YOLO_SERVICE_CLAUDE_OAUTH_BROKER_ENDPOINT"; oauthterminator.BrokerEndpointEnv != want {
		t.Errorf("BrokerEndpointEnv = %q, want %q", oauthterminator.BrokerEndpointEnv, want)
	}
	// The retired spelling must not be what either side settled on.
	if strings.HasSuffix(oauthterminator.BrokerEndpointEnv, "_SOCKET") {
		t.Error("the broker env var still says _SOCKET; its value is an endpoint file")
	}
}

// TestRelayHealthyRequiresPublishedEndpoint: a relay that is ALIVE but has
// published nothing usable is UNHEALTHY.
//
// The relay's own socket is host-only now, so the endpoint file is the jail's only
// route in. Health-by-liveness would call an unpublished relay fine forever: never
// respawned, and the jail permanently unable to authenticate. A pre-upgrade relay
// is in exactly that state after a yolo upgrade, so this is the upgrade path, not a
// hypothetical — which is why the check is unconditional rather than gated on a
// platform.
func TestRelayHealthyRequiresPublishedEndpoint(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(dir, "relay.pid")
	endpointPath := filepath.Join(dir, "claude-oauth-broker.endpoint")

	// A real listener stands in for the relay's own socket probe: relayIsAlive
	// answers true when no pid file exists and the socket connects. Here we drive
	// the pid path instead, with a PID the harness reports as alive.
	o := &Options{}
	fillDefaults(o)
	o.PIDAlive = func(int) bool { return true }
	if err := os.WriteFile(pidFile, []byte("4242\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 1. Alive, nothing published.
	if o.relayHealthy(pidFile, filepath.Join(dir, "nope.sock"), endpointPath) {
		t.Error("a relay that published no endpoint was reported healthy")
	}
	// 2. Alive, a 2-field endpoint (an older publisher, or a truncated write).
	if err := os.WriteFile(endpointPath, []byte("127.0.0.1:1 Y29zdA==\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if o.relayHealthy(pidFile, filepath.Join(dir, "nope.sock"), endpointPath) {
		t.Error("a 2-field endpoint file was reported healthy; existence is not health")
	}
	// 3. Alive AND a complete publication: healthy. Without this control the two
	//    negatives above would also pass if relayHealthy simply always said false.
	//    relayIsAlive additionally needs the relay's own socket to connect, so both
	//    halves are stood up and only the endpoint half varies between the cases.
	sockPath := filepath.Join(dir, "relay.sock")
	sockLn, err := listenUnixForTest(t, sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer sockLn.Close()
	ln, err := svcendpoint.Listen(endpointPath, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if !o.relayHealthy(pidFile, sockPath, endpointPath) {
		t.Error("a live relay with a complete published endpoint was reported unhealthy")
	}
	// 4. And the endpoint half alone flips it back: retire the publication, keep
	//    everything else, and health must go false.
	if err := os.Remove(endpointPath); err != nil {
		t.Fatal(err)
	}
	if o.relayHealthy(pidFile, sockPath, endpointPath) {
		t.Error("removing the published endpoint did not make the relay unhealthy")
	}
}

// TestRelaySpawnArgvCarriesEndpointAndNoToken: the spawn argv names the endpoint
// FILE and carries nothing secret.
//
// #32 passed a --token-file path specifically so the secret would not show up in
// `ps`; with the token minted in-process and written inside the 0600 file there is
// no second artifact at all, so the property to hold is the stronger one. YOLO_DEBUG
// prints this argv verbatim into logs and transcripts.
func TestRelaySpawnArgvCarriesEndpointAndNoToken(t *testing.T) {
	o := &Options{}
	fillDefaults(o)
	argv := o.relaySpawnArgv("/tmp/yolo-broker-relay-deadbeef.sock", "/tmp/yolo-claude-oauth-broker.sock",
		"yolo-ws-abcd1234", "/tmp/yolo-host-services-deadbeef/claude-oauth-broker.endpoint")
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "--endpoint /tmp/yolo-host-services-deadbeef/claude-oauth-broker.endpoint") {
		t.Errorf("argv does not pass the endpoint file: %q", joined)
	}
	// The relay's own socket must be the host-only one, not a path inside the
	// jail's mounted dir — leaving it there keeps the retired transport reachable
	// from inside the jail, and lets the jail unlink the relay's socket.
	if strings.Contains(joined, "--socket /tmp/yolo-host-services-") {
		t.Errorf("the relay's socket is inside the jail's mounted dir: %q", joined)
	}
	if regexp.MustCompile(`\b[0-9a-f]{64}\b`).MatchString(joined) {
		t.Errorf("a 64-hex run (a token) is on the relay argv: %q", joined)
	}
	for _, a := range argv {
		if strings.Contains(a, "--token") {
			t.Errorf("the relay argv carries a token flag: %q", a)
		}
	}
}

// TestRelaySocketIsHostOnly: the relay's socket lives beside its pid and lock
// files, never in the per-jail dir that is bind-mounted :rw into the jail.
func TestRelaySocketIsHostOnly(t *testing.T) {
	const hash = "deadbeef"
	sock := relaySocketFile(hash)
	if !strings.HasPrefix(sock, "/tmp/yolo-broker-relay-") || !strings.HasSuffix(sock, ".sock") {
		t.Errorf("relaySocketFile = %q, want /tmp/yolo-broker-relay-<hash>.sock", sock)
	}
	if strings.Contains(sock, "yolo-host-services-") {
		t.Errorf("the relay socket is inside the mounted host-services dir: %q", sock)
	}
	// It sits with the pid and lock files it is reaped alongside.
	if filepath.Dir(sock) != filepath.Dir(relayPIDFile(hash)) {
		t.Errorf("socket dir %q != pid file dir %q", filepath.Dir(sock), filepath.Dir(relayPIDFile(hash)))
	}
	// And the endpoint file — the ONE thing the jail needs — is in the mounted dir.
	ep := relayEndpointFile("/tmp/yolo-host-services-deadbeef")
	if ep != "/tmp/yolo-host-services-deadbeef/claude-oauth-broker.endpoint" {
		t.Errorf("relayEndpointFile = %q", ep)
	}
}

// brokerFixtureDirs points loophole discovery at a bundled dir holding ONLY a
// broker manifest, so the env-emission gate is exercised without depending on the
// real bundled set or on `claude` being installed.
func brokerFixtureDirs(t *testing.T, enabled bool) {
	t.Helper()
	dir := t.TempDir()
	lp := filepath.Join(dir, broker.BrokerLoopholeName)
	if err := os.MkdirAll(lp, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name": "` + broker.BrokerLoopholeName + `", "description": "fixture",
	  "version": 1, "enabled": ` + map[bool]string{true: "true", false: "false"}[enabled] + `,
	  "transport": "` + loopholes.TransportTLSIntercept + `", "lifecycle": "spawned",
	  "intercepts": [{"host": "platform.claude.com"}], "broker_ip": "127.0.0.1",
	  "host_daemon": {"cmd": ["yolo", "internal", "daemon", "` + broker.BrokerLoopholeName + `", "--socket", "{socket}"]},
	  "jail_daemon": {"cmd": ["yolo-jaild", "oauth-terminator"], "restart": "on-failure"}}`
	if err := os.WriteFile(filepath.Join(lp, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	empty := t.TempDir()
	origB, origU := loopholes.BundledLoopholesDir, loopholes.UserLoopholesDir
	loopholes.BundledLoopholesDir = func() string { return dir }
	loopholes.UserLoopholesDir = func() string { return empty }
	t.Cleanup(func() {
		loopholes.BundledLoopholesDir = origB
		loopholes.UserLoopholesDir = origU
	})
}

// TestBrokerEnvEmittedWhenLoopholeActive: the broker's endpoint env is emitted
// because the LOOPHOLE is active, not because a socket happened to exist.
//
// The old gate stat'd the singleton's socket at assembly time. The container's
// environment is frozen at `podman run`, so a jail launched during the second the
// singleton was restarting got NO broker address for its entire life — the
// terminator exits 2 and Claude Code never starts, and nothing later repairs it.
// PathExists is forced FALSE here: under the old gate that alone suppressed the
// variable.
func TestBrokerEnvEmittedWhenLoopholeActive(t *testing.T) {
	brokerFixtureDirs(t, true)
	o := goldenOptions("/ws", t.TempDir())
	o.PathExists = func(string) bool { return false } // the singleton's socket is absent

	args := o.hostServicesMountArgs("podman", "yolo-ws-abcd1234", jsonx.NewOrderedMap())
	want := hostServiceEnvVar(broker.BrokerLoopholeName) + "=" +
		paths.JailHostServicesDir + "/" + broker.BrokerLoopholeName + paths.ServiceEndpointExt
	if !containsStr(args, want) {
		t.Errorf("broker env not emitted while the loophole is active: %v", args)
	}
	// The value is a PATH, never an address: the port is kernel-assigned and can
	// change under a running container.
	if strings.Contains(want, ":") && !strings.Contains(want, "=/") {
		t.Errorf("emitted value is not an absolute path: %q", want)
	}
}

// TestBrokerEnvSuppressedWhenLoopholeDisabled is the control: without it the test
// above would also pass if the variable were emitted unconditionally.
func TestBrokerEnvSuppressedWhenLoopholeDisabled(t *testing.T) {
	brokerFixtureDirs(t, false)
	o := goldenOptions("/ws", t.TempDir())
	o.PathExists = func(string) bool { return true } // the singleton IS up

	args := o.hostServicesMountArgs("podman", "yolo-ws-abcd1234", jsonx.NewOrderedMap())
	for _, a := range args {
		if strings.Contains(a, "CLAUDE_OAUTH_BROKER") {
			t.Errorf("broker env emitted for a disabled loophole: %q", a)
		}
	}
	// The directory mount is unconditional and still there — the loophole gate must
	// govern only the env var.
	if len(args) != 2 || args[0] != "-v" || !strings.HasSuffix(args[1], paths.JailHostServicesDir+":rw") {
		t.Errorf("want exactly the host-services mount, got %v", args)
	}
}

// TestNoBrokerTokenEnvEmitted: no token environment variable exists, at all.
//
// The deletion is pinned rather than assumed. A fallback would keep the
// child-process inheritance problem alive for whatever read it first, which is the
// entire reason the token moved into the endpoint file.
func TestNoBrokerTokenEnvEmitted(t *testing.T) {
	brokerFixtureDirs(t, true)
	o := goldenOptions("/ws", t.TempDir())
	o.PathExists = func(string) bool { return true }
	args := o.hostServicesMountArgs("podman", "yolo-ws-abcd1234", jsonx.NewOrderedMap())
	sawBroker := false
	for _, a := range args {
		if strings.Contains(a, "CLAUDE_OAUTH_BROKER") {
			sawBroker = true
		}
		if strings.Contains(a, "_TOKEN=") {
			t.Errorf("a token env var is emitted: %q", a)
		}
		if regexp.MustCompile(`\b[0-9a-f]{64}\b`).MatchString(a) {
			t.Errorf("a 64-hex run (a token) is emitted: %q", a)
		}
	}
	if !sawBroker {
		t.Fatal("no broker env at all; the assertion above proved nothing")
	}
}

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// listenUnixForTest stands up the relay's own socket so the health control can
// isolate the endpoint half of relayHealthy from the liveness half.
func listenUnixForTest(t *testing.T, path string) (io.Closer, error) {
	t.Helper()
	return net.Listen("unix", path)
}
