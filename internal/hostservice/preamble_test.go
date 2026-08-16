package hostservice

import (
	"encoding/binary"
	"encoding/json"
	"log"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
)

// These drive serveListener DIRECTLY over a plain TCP listener, which is the only
// way to reach the preamble FAILURE paths at all.
//
// Through ServeEndpoint they are unreachable by construction: svcendpoint injects
// the preamble into the accepted connection's read stream from memory, so the read
// cannot fail no matter what the client does or does not send. The failure branch
// is the contract for the entry point that binds a socket BEHIND yolo's front —
// where a connection can arrive without one, because yolo's own readiness probe
// dials that socket directly and sends nothing at all. Writing the test against
// serveListener pins the behaviour now instead of when that entry point lands.
func startPreambleListener(t *testing.T, readPreamble bool) (addr string, calls *atomic.Int64, logs *syncBuf) {
	t.Helper()
	prevLogger := Logger
	logs = &syncBuf{}
	Logger = log.New(logs, "", 0)
	t.Cleanup(func() { Logger = prevLogger })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	calls = &atomic.Int64{}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = serveListener(func(s *Session) {
			calls.Add(1)
			s.Stdout("pong\n")
		}, ln, stop, readPreamble)
		close(done)
	}()
	t.Cleanup(func() { close(stop); <-done })
	return ln.Addr().String(), calls, logs
}

// clientPreamble hand-rolls the frame a real listener would have prefixed. Hand
// rolled rather than borrowed: encodePreamble is unexported in svcendpoint
// precisely so that yolo stays the only producer, and a test that reached for it
// would be asserting against the encoder instead of against the wire.
func clientPreamble(t *testing.T, jailID string, v int) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{"jail_id": jailID, "service": "svc", "v": v})
	if err != nil {
		t.Fatal(err)
	}
	out := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(out[:4], uint32(len(body)))
	copy(out[4:], body)
	return out
}

// TestPreambleFailureNeverBecomesARequest is the anti-fallback test.
//
// A preamble and a frameproto request are byte-identical in shape, so a reader
// that "recovered" by retrying the bytes as a request would reinstate exactly the
// framing coincidence transport_test.go's case 2 documents — this time on the
// auditing path, where guessing right means attributing a request to whichever
// jail the bytes happened to name. Every case here must therefore reach the
// HANDLER zero times and leave the accept loop alive.
func TestPreambleFailureNeverBecomesARequest(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload []byte
		wantLog string
	}{
		{
			// yolo's own readiness probe for a fronted daemon: connect, send
			// nothing, close. It must be as quiet as a connection that closed
			// before its request, or a daemon dies on its own health check.
			name:    "connect and close",
			payload: nil,
			wantLog: "conn closed without a request",
		},
		{
			// THE ONE THAT MATTERS: a perfectly well-formed request arriving where
			// a preamble was expected. Under a fallback the handler would run.
			name:    "a request where a preamble belongs",
			payload: frameBytes(`{"jail_id":"attacker","mode":"list"}`),
			wantLog: "unrecognized version",
		},
		{
			name:    "a future preamble version",
			payload: clientPreamble(t, "j", 999),
			wantLog: "unrecognized version",
		},
		{
			name:    "not JSON at all",
			payload: frameBytes(`not json`),
			wantLog: "malformed frame",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addr, calls, logs := startPreambleListener(t, true)
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				t.Fatal(err)
			}
			if len(tc.payload) > 0 {
				if _, err := conn.Write(tc.payload); err != nil {
					t.Fatal(err)
				}
			}
			_ = conn.Close()

			waitFor(t, func() bool { return strings.Contains(logs.String(), tc.wantLog) })
			if n := calls.Load(); n != 0 {
				t.Errorf("the handler ran %d times on a connection with no valid preamble; "+
					"the bytes must never be retried as a request", n)
			}
			// Payload-free: the log names the fault, never the frame. The preamble
			// is a versioned envelope meant to grow, so a line that quoted it today
			// would be quoting whatever it carries tomorrow.
			if strings.Contains(logs.String(), "attacker") || strings.Contains(logs.String(), "not json") {
				t.Errorf("the diagnostic quoted the frame's contents:\n%s", logs.String())
			}

			// AND THE DAEMON IS STILL SERVING. A dropped connection is not a fatal
			// one; this is the half that keeps a health probe from killing a daemon.
			good, err := net.Dial("tcp", addr)
			if err != nil {
				t.Fatalf("the accept loop died on a bad preamble: %v", err)
			}
			defer func() { _ = good.Close() }()
			_ = good.SetDeadline(time.Now().Add(5 * time.Second))
			if _, err := good.Write(clientPreamble(t, "yolo-host-services-7f3a1b2c", 1)); err != nil {
				t.Fatal(err)
			}
			if err := frameproto.WriteRequest(good, []byte(`{"mode":"list"}`)); err != nil {
				t.Fatal(err)
			}
			if _, err := frameproto.ReadFrame(good); err != nil {
				t.Fatalf("the next well-formed connection got no response: %v", err)
			}
			if n := calls.Load(); n != 1 {
				t.Errorf("handler calls = %d after one good request, want 1", n)
			}
			// The access line is written in handleOne's defer, after the handler
			// returns — concurrent with the client's own read, so this waits.
			waitFor(t, func() bool {
				return strings.Contains(logs.String(), "jail=yolo-host-services-7f3a1b2c")
			})
		})
	}
}

// TestPreambleFreeListenerReadsTheRequestFirst is ServeUnix's half of the same
// switch, driven through the same helper so the two cannot drift: with
// readPreamble false the FIRST frame on the wire is the request, and a daemon that
// read a preamble anyway would hang here on bytes nobody sends.
func TestPreambleFreeListenerReadsTheRequestFirst(t *testing.T) {
	addr, calls, logs := startPreambleListener(t, false)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := frameproto.WriteRequest(conn, []byte(`{"jail_id":"relay-stamped","mode":"list"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := frameproto.ReadFrame(conn); err != nil {
		t.Fatalf("a preamble-free listener did not answer a bare request: %v", err)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("handler calls = %d, want 1", n)
	}
	waitFor(t, func() bool { return strings.Contains(logs.String(), "jail=relay-stamped") })
}

// frameBytes wraps body as one 4-byte-BE length-prefixed frame — the shape a
// frameproto request and a connection preamble SHARE, which is the whole reason
// the version check has to exist.
func frameBytes(body string) []byte {
	out := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(out[:4], uint32(len(body)))
	copy(out[4:], body)
	return out
}
