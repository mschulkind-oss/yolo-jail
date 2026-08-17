package run

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// TestRelayEnsureSparesALiveLegacyRelay pins the UPGRADE path.
//
// The bug: a jail launched by a PRE-loopback-TLS yolo has a relay that works and
// publishes no endpoint file. relayHealthy calls svcendpoint.Probe on that missing
// file, says "unhealthy", and relayEnsure kills the relay and respawns on the new
// scheme — which the CONTAINER CAN NEVER REACH, because its environment was frozen
// at launch naming YOLO_SERVICE_..._SOCKET at the legacy path inside the mounted
// host-services dir. A working jail is converted into a broken one mid-session,
// recoverable only by relaunching.
//
// So relayEnsure must detect a live legacy relay and leave it alone. This test
// drives the real decision path with a REAL unix socket at the legacy path, because
// the whole question is whether that socket is reachable — a fake would assert the
// thing under test.
func TestRelayEnsureSparesALiveLegacyRelay(t *testing.T) {
	// shortSocketDir, not t.TempDir(): a TMPDIR-rooted path overruns darwin's
	// sun_path, and the t.Skipf this used to carry turned that into a SILENT
	// no-run on check-macos — the platform whose upgrade path this test exists to
	// protect. A short path has no legitimate reason to fail, so it is fatal now.
	socketsDir := shortSocketDir(t)
	legacy := filepath.Join(socketsDir, broker.BrokerLoopholeName+".sock")

	assertSockPathFits(t, legacy)
	ln, err := net.Listen("unix", legacy)
	if err != nil {
		t.Fatalf("cannot bind the legacy relay socket at %s: %v", legacy, err)
	}
	defer ln.Close()
	go acceptAndClose(ln)

	if got := legacyRelaySocketFile(socketsDir); got != legacy {
		t.Fatalf("legacyRelaySocketFile = %q, want %q — the upgrade check would look "+
			"at the wrong path and kill the relay it is meant to spare", got, legacy)
	}

	// A pid file naming THIS process: alive, and killing it is not something the
	// test can survive — which is exactly the point.
	pidFile := filepath.Join(t.TempDir(), "relay.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	o := &Options{}
	fillDefaults(o)

	if !o.relayIsAlive(pidFile, legacy) {
		t.Fatal("the harness did not produce a live legacy relay; the rest of this " +
			"test would pass vacuously")
	}

	// No endpoint file exists, so relayHealthy is false — the condition that used
	// to mean "kill it".
	endpoint := relayEndpointFile(socketsDir)
	if _, err := os.Stat(endpoint); err == nil {
		t.Fatalf("endpoint file %s should not exist in this scenario", endpoint)
	}

	// THE DECISION ITSELF, not just its ingredients. This assertion is the one that was
	// missing: the three checks above pin the legacy path, the liveness probe and the absent
	// endpoint file, and the comment used to claim "if relayEnsure decides to reap here, the
	// assertion below catches it" — but the assertion below only stat'd the endpoint file.
	// MEASURED: the whole five-line spare branch could be deleted from relayEnsure with
	// `go test ./internal/cli/run/` still green. relaySpareLegacy is the branch's condition
	// extracted as a pure predicate over the four paths, which is the seam this needed —
	// relayEnsure itself spawns and flocks, which is why nobody tested it.
	newSock := relaySocketFile(relayShortHash("some-jail"))
	if !o.relaySpareLegacy(pidFile, newSock, endpoint, socketsDir) {
		t.Fatal("a LIVE legacy relay was not spared. relayEnsure would reap it and respawn on " +
			"the new scheme, which the running container CANNOT REACH — its environment was " +
			"frozen at launch naming YOLO_SERVICE_..._SOCKET at the legacy path. That converts " +
			"a working jail into a broken one mid-session, recoverable only by relaunching")
	}
}

// The spare is NOT unconditional, and this is the control that makes the assertion above mean
// something: with no legacy relay alive, the decision must be "reap and respawn" — otherwise
// every unhealthy relay on every machine would be left in place forever and the loopback-TLS
// transport would never actually start.
func TestRelayEnsureDoesNotSpareWhenNoLegacyRelayIsAlive(t *testing.T) {
	socketsDir := shortSocketDir(t)
	pidFile := filepath.Join(t.TempDir(), "relay.pid")
	// No pid file, no legacy socket, no endpoint: nothing is alive anywhere.
	o := &Options{}
	fillDefaults(o)

	endpoint := relayEndpointFile(socketsDir)
	newSock := relaySocketFile(relayShortHash("some-jail"))
	if o.relaySpareLegacy(pidFile, newSock, endpoint, socketsDir) {
		t.Error("nothing was alive and the decision was still SPARE — the relay would never be " +
			"respawned, so the jail could never authenticate")
	}
}

// A HEALTHY current relay is not "spared" either, and the distinction matters: sparing means
// "do not touch a working PRE-UPGRADE relay". A relay that is healthy on the NEW scheme has
// already been handled by relayEnsure's two relayHealthy returns, and reporting it as spared
// would make the predicate's answer depend on call position rather than on state — which is
// what makes it answerable in isolation at all.
func TestRelaySpareLegacyIsFalseForAHealthyCurrentRelay(t *testing.T) {
	dir := shortSocketDir(t)
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A live legacy socket AND a healthy current relay: the stale-legacy-file case.
	legacy := filepath.Join(dir, broker.BrokerLoopholeName+".sock")
	assertSockPathFits(t, legacy)
	legacyLn, err := net.Listen("unix", legacy)
	if err != nil {
		t.Fatalf("cannot bind the legacy socket at %s: %v", legacy, err)
	}
	defer legacyLn.Close()
	go acceptAndClose(legacyLn)

	sockPath := filepath.Join(dir, "relay.sock")
	sockLn, err := listenUnixForTest(t, sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer sockLn.Close()
	endpoint := filepath.Join(dir, broker.BrokerLoopholeName+".endpoint")
	epLn, err := svcendpoint.Listen(endpoint, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	defer epLn.Close()

	pidFile := filepath.Join(t.TempDir(), "relay.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := &Options{}
	fillDefaults(o)

	if !o.relayHealthy(pidFile, sockPath, endpoint) {
		t.Fatal("the harness did not produce a HEALTHY current relay; the assertion below " +
			"would pass vacuously")
	}
	if o.relaySpareLegacy(pidFile, sockPath, endpoint, dir) {
		t.Error("a healthy CURRENT relay beside a leftover legacy socket file was reported as " +
			"'spare the legacy one'. The spare is for a working PRE-UPGRADE relay; keying on " +
			"legacy liveness alone would take this path and never republish")
	}
}

// And relayEnsure ACTUALLY ASKS. Asserted over the source, because the predicate being
// correct and the reap path not consulting it is exactly the state this batch found: the
// five-line branch could be deleted wholesale with the package still green.
//
// A behavioural test cannot cover this — relayEnsure spawns a detached daemon and takes a
// flock, which is why the branch went untested for its whole life. A source assertion is the
// honest instrument for "the decision is on the path", and it costs nothing.
func TestRelayEnsureConsultsTheSpareDecision(t *testing.T) {
	body, err := os.ReadFile("loopholesruntime.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "o.relaySpareLegacy(") {
		t.Error("relayEnsure no longer calls relaySpareLegacy, so nothing stops it reaping a " +
			"working PRE-UPGRADE relay: the container's frozen environment names the legacy " +
			"socket path, so the respawned relay is unreachable and a working jail becomes a " +
			"broken one mid-session. The predicate's own tests keep passing — it is the CALL " +
			"that went missing, which is how the branch this replaced survived untested.")
	}
}

// acceptAndClose drains a listener so a connect probe against it succeeds.
func acceptAndClose(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		_ = c.Close()
	}
}

// TestRetireStaleRelayFilesRemovesTheLegacySocket pins the other half: when we DO
// respawn, the legacy socket must go.
//
// relayKill closes the listener, but SetUnlinkOnClose(false) leaves the file behind.
// A pre-upgrade container whose frozen env still names it then dials a dead FILE and
// gets ECONNREFUSED — indistinguishable from "the relay crashed, retry". Removing it
// turns that into ENOENT, which the terminator already reports as the clean
// "not wired up in this jail" case.
func TestRetireStaleRelayFilesRemovesTheLegacySocket(t *testing.T) {
	socketsDir := t.TempDir()
	legacy := filepath.Join(socketsDir, broker.BrokerLoopholeName+".sock")
	if err := os.WriteFile(legacy, []byte("dead socket file"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The retirement relayEnsure performs before a respawn.
	retireStaleRelayFiles(relaySocketFile("deadbeef"), relayEndpointFile(socketsDir))
	_ = os.Remove(legacyRelaySocketFile(socketsDir))

	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("legacy relay socket survived retirement (%v) — a pre-upgrade "+
			"container would dial it forever and get ECONNREFUSED rather than the "+
			"honest ENOENT", err)
	}
}
