package hostservice

import (
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// syncBuf is a Logger destination safe to read while a connection goroutine
// writes to it.
type syncBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// TestFramedDaemonProducesBothTiers pins the relationship between the two audit
// tiers, which is the thing most likely to be papered over later.
//
// They are not a fallback and a better version of one thing. A framed daemon
// behind ServeEndpoint produces BOTH:
//
//   - tier 1, from the transport (internal/svcendpoint): one CONNECTION record —
//     which service, which jail, bytes each way, duration, whether it
//     authenticated. Uniform across every daemon, fronted or not.
//   - tier 2, from this package: one REQUEST line — the request's top-level key
//     names and the handler's exit code. Available ONLY here, because only here
//     is there a parsed request to describe.
//
// A FRONTED daemon (`publishes: "socket"`) still gets tier 1 and no tier 2: the
// front splices a byte stream it does not parse, so there is no request to
// describe. That ceiling has not moved. What DID move is the sentence this
// comment used to carry — that tier 2's `jail=` is the client's own claim
// "forever". It is not: on a preamble-bearing connection yolo asserts the jail
// host-side, from the same derivation tier 1 uses, so the two tiers now AGREE by
// construction and a spoofed jail_id is overridden in both
// (docs/design/broker-as-a-pack.md §5.5).
func TestFramedDaemonProducesBothTiers(t *testing.T) {
	advertiseLoopback(t)

	prevLogger := Logger
	logs := &syncBuf{}
	Logger = log.New(logs, "", 0)
	t.Cleanup(func() { Logger = prevLogger })

	prevSink := svcendpoint.CrossingSink()
	var mu sync.Mutex
	var seen []svcendpoint.Crossing
	svcendpoint.SetCrossingSink(func(c svcendpoint.Crossing) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, c)
	})
	t.Cleanup(func() { svcendpoint.SetCrossingSink(prevSink) })

	dir, err := os.MkdirTemp("/tmp", "yj-tiers-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	ep := filepath.Join(dir, "twotier.endpoint")

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = ServeEndpoint(func(s *Session) { s.Stdout("pong\n") }, ep, stop)
		close(done)
	}()
	waitForEndpoint(t, ep)
	defer func() { close(stop); <-done }()

	conn := dialEndpoint(t, ep)
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	// THE SPOOF IS THE NEGATIVE CASE, kept on the wire deliberately. A client
	// naming itself is exactly what the preamble exists to override, so the
	// assertions below are only meaningful with a lie in the request.
	if err := frameproto.WriteRequest(conn, []byte(`{"jail_id":"i-said-so","mode":"ping"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := frameproto.ReadFrame(conn); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()

	// Tier 1: one connection record, labelled as directly served.
	var crossing svcendpoint.Crossing
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(seen)
		if n > 0 {
			crossing = seen[0]
		}
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if crossing.Outcome != svcendpoint.CrossingAccepted {
		t.Fatalf("tier 1 recorded no accepted crossing for a framed daemon (got %+v)", crossing)
	}
	if crossing.Via != svcendpoint.CrossingViaEndpoint {
		t.Errorf("tier 1 Via = %q, want %q", crossing.Via, svcendpoint.CrossingViaEndpoint)
	}
	if crossing.Service != "twotier" {
		t.Errorf("tier 1 Service = %q, want %q", crossing.Service, "twotier")
	}

	// Tier 2: the per-request access line. jail= is now the HOST's assertion, and
	// keys= still reports the spoofed field's NAME — the line describes what was
	// asked without adopting what it claimed.
	line := logs.String()
	wantJail := filepath.Base(dir)
	for _, want := range []string{"jail=" + wantJail, "keys=jail_id,mode", "rc=0", "elapsed_ms=", "bytes_out="} {
		if !strings.Contains(line, want) {
			t.Errorf("tier 2 access log missing %q; got:\n%s", want, line)
		}
	}
	if strings.Contains(line, "jail=i-said-so") {
		t.Errorf("tier 2 took the CLIENT's word for the jail's identity; the connection "+
			"preamble's host-asserted jail_id must win. Line:\n%s", line)
	}

	// Neither tier takes the jail's own word for it, and — the property §12 asks
	// for — they now AGREE, because both are the same derivation from the path
	// yolo published at rather than two derivations kept in step by hand.
	if crossing.Jail == "i-said-so" {
		t.Error("tier 1 took the jail's own word for its identity; it must derive it " +
			"host-side from the publication path")
	}
	if crossing.Jail != wantJail {
		t.Errorf("tier 1 Jail = %q, want %q (the publication directory)", crossing.Jail, wantJail)
	}
	if !strings.Contains(line, "jail="+crossing.Jail+" ") {
		t.Errorf("the two tiers disagree on jail=: tier 1 says %q, tier 2 line is:\n%s",
			crossing.Jail, line)
	}
}

// TestServeUnixKeepsTheClientSuppliedJailID is the other half, and it is why
// handleOne keeps a fallback at all rather than hard-wiring the preamble.
//
// ServeUnix is host-to-host: nothing crossed a boundary, so yolo has no jail to
// assert and sends no preamble. The broker singleton is reached this way, through
// a relay that stamps jail_id INTO the payload and opts out of the preamble until
// it is deleted — so removing the fallback would blank every broker access line.
func TestServeUnixKeepsTheClientSuppliedJailID(t *testing.T) {
	prevLogger := Logger
	logs := &syncBuf{}
	Logger = log.New(logs, "", 0)
	t.Cleanup(func() { Logger = prevLogger })

	dir, err := os.MkdirTemp("/tmp", "yj-unixjail-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s.sock")

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = ServeUnix(func(s *Session) { s.Stdout("pong\n") }, sock, stop)
		close(done)
	}()
	defer func() { close(stop); <-done }()

	var conn net.Conn
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err = net.Dial("unix", sock)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ServeUnix never bound %s: %v", sock, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	// NO PREAMBLE IS WRITTEN by this client, and none is expected: a ServeUnix
	// daemon that read one would hang here forever on bytes nobody sends.
	if err := frameproto.WriteRequest(conn, []byte(`{"jail_id":"relay-stamped","mode":"ping"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := frameproto.ReadFrame(conn); err != nil {
		t.Fatalf("no response on a bare AF_UNIX connection: %v", err)
	}
	_ = conn.Close()

	waitFor(t, func() bool { return strings.Contains(logs.String(), "jail=") })
	if line := logs.String(); !strings.Contains(line, "jail=relay-stamped") {
		t.Errorf("a host-to-host request lost its in-payload jail_id; got:\n%s", line)
	}
}

// waitFor polls until cond holds, so an assertion on a line written by a
// connection goroutine is not a race against the client's own return.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition never held within 5s")
}
