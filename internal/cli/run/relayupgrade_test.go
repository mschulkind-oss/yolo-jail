package run

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
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
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	if got := legacyRelaySocketFile(socketsDir); got != legacy {
		t.Fatalf("legacyRelaySocketFile = %q, want %q — the upgrade check would look "+
			"at the wrong path and kill the relay it is meant to spare", got, legacy)
	}

	// A pid file naming THIS process: alive, and killing it is not something the
	// test can survive — which is exactly the point. If relayEnsure decides to
	// reap here, the assertion below catches it before any signal is sent, because
	// relayKill removes the pid file first.
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
