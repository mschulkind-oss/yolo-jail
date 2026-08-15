package hostservice

import (
	"log"
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
// A FRONTED daemon (`publishes: "socket"`) gets tier 1 and nothing else, forever:
// the front splices a byte stream it does not parse. That is the ceiling, and it
// is why this test asserts the two tiers exist side by side rather than asserting
// one covers the other.
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
	// jail_id is what the CLIENT says it is. Tier 2 records it verbatim, as the
	// protocol says to ("daemons must not trust it"); tier 1 ignores it entirely.
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

	// Tier 2: the per-request access line, still exactly as documented.
	line := logs.String()
	for _, want := range []string{"jail=i-said-so", "keys=jail_id,mode", "rc=0", "elapsed_ms=", "bytes_out="} {
		if !strings.Contains(line, want) {
			t.Errorf("tier 2 access log missing %q; got:\n%s", want, line)
		}
	}

	// And the difference between them, which is the reason to keep both: tier 2's
	// jail is what the CLIENT claimed, tier 1's is derived host-side from the path
	// yolo published at. A jail can rename itself in tier 2 and cannot in tier 1.
	if crossing.Jail == "i-said-so" {
		t.Error("tier 1 took the jail's own word for its identity; it must derive it " +
			"host-side from the publication path")
	}
	if want := filepath.Base(dir); crossing.Jail != want {
		t.Errorf("tier 1 Jail = %q, want %q (the publication directory)", crossing.Jail, want)
	}
}
