package check

import (
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// brokerprobe_test.go covers this package's own copy of two facts about the
// host-wide singleton — where it lives, and how it may be probed. Both copies were
// unpinned here while their twins in internal/broker were pinned, which is the
// asymmetry that makes a duplicated constant dangerous rather than merely
// duplicated: the pinned copy moves, the unpinned one does not, and `yolo check` —
// the command whose entire job is telling the user whether the other two agree —
// becomes the one that disagrees.

// brokerSockPathMax is the longest AF_UNIX path that binds on the tighter of the
// two platforms: darwin's sun_path is 104 bytes INCLUDING the NUL, Linux's is 108.
const brokerSockPathMax = 103

// shortBrokerSocketDir returns a per-test dir short enough to hold a socket.
// t.TempDir() is not: it is rooted at TMPDIR, which on darwin is
// /var/folders/<2>/<26>/T/ before the test name is appended.
func shortBrokerSocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "yj-chk-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// TestCheckSingletonPathsMatchTheDerivedOnes closes the third route named in
// broker.SingletonDeps' own doc and pinned by neither test that mentions it.
//
// Four things reach the singleton: the run pipeline's front, via
// paths.HostSingletonSocket(name) built from the loophole RECORD; `yolo broker
// {status,stop,restart}`, via the broker.BrokerSingleton* constants; a
// not-yet-upgraded yolo on the same host, via those same constants; and THIS
// PACKAGE, via a hand-copied literal of its own. TestSingletonPathsMatchTheBrokerConstants
// pins the first pair against the second. Nothing pinned the third copy, so
// `/tmp/yolo-claude-oauth-broker.sock` could drift here alone — and the symptom
// would be `yolo check` reporting "daemon not running" about a broker serving every
// jail on the machine, with the remedy it prints (`yolo broker restart`) cycling a
// singleton that was fine.
//
// Measured 2026-08-19: changing both literals in broker.go passes `go test -short
// ./...` in full.
func TestCheckSingletonPathsMatchTheDerivedOnes(t *testing.T) {
	for _, tc := range []struct{ what, here, derived, constant string }{
		{"socket", brokerSingletonSocket,
			paths.HostSingletonSocket(brokerLoopholeName), broker.BrokerSingletonSocket},
		{"pid file", brokerSingletonPIDFil,
			paths.HostSingletonPIDFile(brokerLoopholeName), broker.BrokerSingletonPIDFile},
	} {
		if tc.here != tc.derived {
			t.Errorf("check's %s literal is %q but paths.HostSingleton* derives %q — the run "+
				"pipeline fronts one file while this command inspects another, so a healthy "+
				"broker reports as absent", tc.what, tc.here, tc.derived)
		}
		if tc.here != tc.constant {
			t.Errorf("check's %s literal is %q but internal/broker's constant is %q — "+
				"`yolo broker status` and `yolo check` would disagree about the same daemon",
				tc.what, tc.here, tc.constant)
		}
	}
	// And the NAME this package keys on is the one the rest of the tree does, since
	// every derivation above is a function of it and nothing else.
	if brokerLoopholeName != broker.BrokerLoopholeName {
		t.Errorf("check's loophole name is %q, internal/broker's is %q",
			brokerLoopholeName, broker.BrokerLoopholeName)
	}
}

// TestBrokerSocketAcceptsWritesNothing is this package's copy of the regression
// broker.TestSingletonReachableWritesNothing pins, and it is on the credential path
// for the same reason.
//
// The probe this replaced wrote a framed {"action":"ping"} and waited for a pong.
// The singleton sits behind yolo's front now (`publishes: "socket"` + `scope:
// "host"`), so its first read on every connection is the CONNECTION PREAMBLE, and a
// bare ping is rejected as a preamble carrying no version. Reinstate the round trip
// here and reportBrokerDaemon falls to its default arm: r.FAIL, "daemon unreachable
// (pid=N, socket present, not accepting)", advising `yolo broker restart` — about a
// daemon serving every jail on the host perfectly well, with a remedy that would cut
// all of them off while it cycles.
//
// So the property is not "the probe still answers true" but "the probe SENDS
// NOTHING": the preamble is yolo asserting which jail is on the other end of a
// connection, and a host-side liveness check belongs to no jail. Asserting on the
// BYTES rather than on the return value is what makes a reinstated protocol write
// fail here even if it happens to keep answering true.
//
// Measured 2026-08-19: both the byte write alone and the full revert to
// brokerPingConn passed `go test -short ./...` before this test existed.
func TestBrokerSocketAcceptsWritesNothing(t *testing.T) {
	sock := filepath.Join(shortBrokerSocketDir(t), "b.sock")
	if len(sock) > brokerSockPathMax {
		t.Fatalf("socket path is %d bytes, over the %d-byte darwin sun_path limit: %s",
			len(sock), brokerSockPathMax, sock)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	// A daemon that reads to EOF and answers nothing — which is what a fronted
	// daemon looks like to anyone who has not sent it a preamble.
	got := make(chan []byte, 1)
	go func() {
		conn, aerr := ln.Accept()
		if aerr != nil {
			got <- nil
			return
		}
		defer conn.Close()
		b, rerr := io.ReadAll(conn)
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			got <- nil
			return
		}
		got <- b
	}()

	if !brokerSocketAccepts(sock, 2*time.Second) {
		t.Fatal("a listening socket read as not accepting; every `yolo check` on a healthy " +
			"host would then fail the broker row")
	}
	select {
	case b := <-got:
		if len(b) != 0 {
			t.Errorf("the liveness probe wrote %d bytes (%q) — it must write NONE. A daemon "+
				"behind yolo's front reads a connection preamble first, so anything this side "+
				"could send is either rejected (and a healthy broker reports as unreachable) "+
				"or a forged jail identity in a connection that belongs to no jail",
				len(b), b)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the probe never closed its connection: the listener's ReadAll is still " +
			"blocked, so a wedged daemon would hold up every `yolo check`")
	}
}

// TestBrokerSocketAcceptsRejectsAStaleFile is the control the test above needs:
// without it, a probe rewritten to `return true` unconditionally would satisfy both
// the "answers true" and the "writes nothing" halves.
//
// A leftover REGULAR FILE at the socket path is what a SIGKILLed daemon leaves
// behind, and existence is not liveness — brokerStatus already gates the probe on
// PathExists, so this is the only thing standing between a stale file and a green
// "daemon live" row.
func TestBrokerSocketAcceptsRejectsAStaleFile(t *testing.T) {
	stale := filepath.Join(shortBrokerSocketDir(t), "b.sock")
	if err := os.WriteFile(stale, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	if brokerSocketAccepts(stale, 200*time.Millisecond) {
		t.Error("a stale regular file at the socket path read as accepting")
	}
	if brokerSocketAccepts(filepath.Join(t.TempDir(), "absent.sock"), 200*time.Millisecond) {
		t.Error("a path with nothing at it read as accepting")
	}
}
