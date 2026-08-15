package svcendpoint

import (
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

// crossingRecorder collects everything the process-wide sink is handed, and
// restores the previous sink at cleanup. Every test here installs one: the sink
// is package state, so leaving one behind would leak into the next test.
type crossingRecorder struct {
	mu   sync.Mutex
	seen []Crossing
}

func captureCrossings(t *testing.T) *crossingRecorder {
	t.Helper()
	r := &crossingRecorder{}
	prev := CrossingSink()
	SetCrossingSink(func(c Crossing) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.seen = append(r.seen, c)
	})
	t.Cleanup(func() { SetCrossingSink(prev) })
	return r
}

func (r *crossingRecorder) all() []Crossing {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Crossing(nil), r.seen...)
}

// await polls until at least n records have arrived. A crossing is recorded when
// the connection CLOSES, which is concurrent with the client's own return, so
// every assertion here needs a bounded wait rather than a bare read.
func (r *crossingRecorder) await(t *testing.T, n int) []Crossing {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := r.all(); len(got) >= n {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("only %d crossings recorded, want at least %d", len(r.all()), n)
	return nil
}

// startEchoFront stands up a real front: an upstream AF_UNIX daemon that echoes
// one message, and a ServeFront in front of it. Returns the endpoint path.
func startEchoFront(t *testing.T) string {
	t.Helper()
	dir := privateSocketDir(t)
	upstream := filepath.Join(dir, "up.sock")
	assertSockPathFits(t, upstream)
	endpoint := filepath.Join(dir, "echo.endpoint")

	ln, err := net.Listen("unix", upstream)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				buf := make([]byte, 64)
				n, err := conn.Read(buf)
				if err != nil {
					return
				}
				_, _ = conn.Write(append([]byte("got:"), buf[:n]...))
			}()
		}
	}()

	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() { _ = ServeFront(endpoint, "127.0.0.1", upstream, stop) }()
	waitProbe(t, endpoint)
	return endpoint
}

// ---------------------------------------------------------------------------
// R1 — connection-level facts, at the front, for every fronted daemon
// ---------------------------------------------------------------------------

// TestFrontRecordsAcceptedCrossing is the base case: one authenticated
// connection through the front produces exactly one connection-level record
// carrying which service, which jail, both byte counts and a duration.
func TestFrontRecordsAcceptedCrossing(t *testing.T) {
	rec := captureCrossings(t)
	endpoint := startEchoFront(t)

	conn, err := Dial(endpoint, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil || string(buf[:n]) != "got:ping" {
		t.Fatalf("response = %q, %v", buf[:n], err)
	}
	_ = conn.Close()

	got := rec.await(t, 1)[0]
	if got.Outcome != CrossingAccepted {
		t.Errorf("Outcome = %q, want %q", got.Outcome, CrossingAccepted)
	}
	if got.Via != CrossingViaFront {
		t.Errorf("Via = %q, want %q", got.Via, CrossingViaFront)
	}
	if got.Service != "echo" {
		t.Errorf("Service = %q, want %q — the service name comes from the endpoint "+
			"file yolo published, not from anything the client said", got.Service, "echo")
	}
	if want := filepath.Base(filepath.Dir(endpoint)); got.Jail != want {
		t.Errorf("Jail = %q, want %q", got.Jail, want)
	}
	if got.BytesIn < 4 {
		t.Errorf("BytesIn = %d, want at least the 4 request bytes", got.BytesIn)
	}
	if got.BytesOut < 8 {
		t.Errorf("BytesOut = %d, want at least the 8 response bytes", got.BytesOut)
	}
	if got.At.IsZero() {
		t.Error("At is the zero time; a crossing with no timestamp audits nothing")
	}
	if got.Duration < 0 {
		t.Errorf("Duration = %v, want a non-negative span", got.Duration)
	}
}

// TestRejectedCrossingIsRecorded: a REJECTED crossing is at least as interesting
// as an accepted one, so authentication failure is recorded rather than being
// visible only as a dropped connection.
func TestRejectedCrossingIsRecorded(t *testing.T) {
	rec := captureCrossings(t)
	s := startServer(t)

	ep, err := Read(s.path)
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(privateDir(t), "wrongtoken.endpoint")
	if err := Publish(bad, Endpoint{HostPort: ep.HostPort, CertDER: ep.CertDER, Token: wrong}); err != nil {
		t.Fatal(err)
	}
	if _, err := Dial(bad, 5*time.Second); !errors.Is(err, ErrAuthRejected) {
		t.Fatalf("Dial with the wrong token: %v, want ErrAuthRejected", err)
	}

	got := rec.await(t, 1)[0]
	if got.Outcome != CrossingRejected {
		t.Errorf("Outcome = %q, want %q", got.Outcome, CrossingRejected)
	}
	if got.Reason != CrossingReasonTokenMismatch {
		t.Errorf("Reason = %q, want %q", got.Reason, CrossingReasonTokenMismatch)
	}
	// The record is attributed to the LISTENER's published identity, not to the
	// bogus endpoint file the client dialed through.
	if got.Service != "svc" {
		t.Errorf("Service = %q, want %q", got.Service, "svc")
	}
	if got.Via != CrossingViaEndpoint {
		t.Errorf("Via = %q, want %q — this listener is served directly, not fronted",
			got.Via, CrossingViaEndpoint)
	}
}

// TestOversizedTokenFrameRecordedAsBadFrame pins the second rejection reason, so
// "the token was wrong" and "that was not a token frame at all" stay
// distinguishable in the audit log.
func TestOversizedTokenFrameRecordedAsBadFrame(t *testing.T) {
	rec := captureCrossings(t)
	s := startServer(t)

	conn := dialPinnedRaw(t, s.path)
	writeRawTokenFrame(t, conn, tokenFrameMax+1, nil)
	expectNoAck(t, conn)

	got := rec.await(t, 1)[0]
	if got.Outcome != CrossingRejected || got.Reason != CrossingReasonBadTokenFrame {
		t.Errorf("outcome/reason = %q/%q, want %q/%q",
			got.Outcome, got.Reason, CrossingRejected, CrossingReasonBadTokenFrame)
	}
}

// TestUnreachableUpstreamRecorded: an AUTHENTICATED connection the front could
// not deliver is neither an accepted crossing nor a rejected one. It gets its own
// outcome, because "the jail got through the boundary and the daemon was gone" is
// exactly the state an audit reader must not have to infer from byte counts.
func TestUnreachableUpstreamRecorded(t *testing.T) {
	rec := captureCrossings(t)
	dir := privateSocketDir(t)
	upstream := filepath.Join(dir, "absent.sock") // deliberately never bound
	endpoint := filepath.Join(dir, "orphan.endpoint")

	stop := make(chan struct{})
	defer close(stop)
	go func() { _ = ServeFront(endpoint, "127.0.0.1", upstream, stop) }()
	waitProbe(t, endpoint)

	conn, err := Dial(endpoint, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadAll(conn); err != nil {
		t.Fatalf("the front did not hang up on an undeliverable connection: %v", err)
	}
	_ = conn.Close()

	got := rec.await(t, 1)[0]
	if got.Outcome != CrossingUnreachable {
		t.Errorf("Outcome = %q, want %q", got.Outcome, CrossingUnreachable)
	}
	if got.Reason != CrossingReasonUpstreamDial {
		t.Errorf("Reason = %q, want %q", got.Reason, CrossingReasonUpstreamDial)
	}
}

// ---------------------------------------------------------------------------
// R4 — AUDIT-ONLY: the audit path cannot break the data path
// ---------------------------------------------------------------------------

// TestSinkPanicCannotBreakTheDataPath is THE test for this feature.
//
// A sink runs inside a connection goroutine, and an unrecovered panic in ANY
// goroutine takes down the whole process — so a sink that panics would turn an
// audit-only log into the loudest failure mode in the system. It must instead be
// swallowed, warned about once, and disabled, with every byte of the crossing
// delivered and the NEXT crossing still working.
func TestSinkPanicCannotBreakTheDataPath(t *testing.T) {
	logs := captureLogger(t)
	prev := CrossingSink()
	t.Cleanup(func() { SetCrossingSink(prev) })
	SetCrossingSink(func(Crossing) { panic("sink exploded") })

	endpoint := startEchoFront(t)

	for i, want := range []string{"got:one", "got:two"} {
		conn, err := Dial(endpoint, 5*time.Second)
		if err != nil {
			t.Fatalf("crossing %d: dial: %v", i, err)
		}
		if _, err := conn.Write([]byte(want[4:])); err != nil {
			t.Fatalf("crossing %d: write: %v", i, err)
		}
		buf := make([]byte, 64)
		n, err := conn.Read(buf)
		if err != nil || string(buf[:n]) != want {
			t.Fatalf("crossing %d: response = %q, %v — a panicking AUDIT sink broke "+
				"the DATA path", i, buf[:n], err)
		}
		_ = conn.Close()
	}

	// One warning, and the sink is off afterwards.
	if !strings.Contains(logs.String(), "crossing audit") {
		t.Errorf("no warning logged for the panicking sink; logs:\n%s", logs.String())
	}
	if CrossingSink() != nil {
		t.Error("a sink that panicked is still installed; it must be disabled after one warning")
	}
}

// TestNoSinkIsTheDefault: with nothing installed the transport behaves exactly as
// it did before this feature, and recording is a no-op rather than a nil deref.
func TestNoSinkIsTheDefault(t *testing.T) {
	prev := CrossingSink()
	SetCrossingSink(nil)
	t.Cleanup(func() { SetCrossingSink(prev) })

	endpoint := startEchoFront(t)
	conn, err := Dial(endpoint, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil || string(buf[:n]) != "got:ping" {
		t.Fatalf("response = %q, %v", buf[:n], err)
	}
	_ = conn.Close()
}

// ---------------------------------------------------------------------------
// R5 — no secrets, ever
// ---------------------------------------------------------------------------

// TestCrossingCarriesNoSecret: a Crossing has counts and names, never content.
// svcendpoint's logging rule is "a length, never a value", and the audit record
// is held to it too — the endpoint file's token and certificate must not appear
// in any field of any record.
func TestCrossingCarriesNoSecret(t *testing.T) {
	rec := captureCrossings(t)
	endpoint := startEchoFront(t)
	ep, err := Read(endpoint)
	if err != nil {
		t.Fatal(err)
	}

	conn, err := Dial(endpoint, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("secret-payload")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	_, _ = conn.Read(buf)
	_ = conn.Close()

	for _, c := range rec.await(t, 1) {
		blob := c.Service + "\x00" + c.Jail + "\x00" + c.Via + "\x00" + c.Outcome + "\x00" + c.Reason
		for name, secret := range map[string]string{
			"the bearer token": ep.Token,
			"the payload":      "secret-payload",
			"the host:port":    ep.HostPort,
		} {
			if strings.Contains(blob, secret) {
				t.Errorf("a Crossing field carried %s; the record is counts and names only", name)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Identity derivation
// ---------------------------------------------------------------------------

// TestCrossingIdentityFromPublishPath pins host-side attribution: both names come
// from the path YOLO chose to publish at, so a jail cannot rename itself in the
// audit log by sending a different jail_id.
func TestCrossingIdentityFromPublishPath(t *testing.T) {
	for _, tc := range []struct {
		path, service, jail string
	}{
		{"/tmp/yolo-host-services-a1b2c3d4/host-processes.endpoint", "host-processes", "yolo-host-services-a1b2c3d4"},
		{"/tmp/yolo-host-services-a1b2c3d4/claude-oauth-broker.endpoint", "claude-oauth-broker", "yolo-host-services-a1b2c3d4"},
		{"/tmp/d/acme.proxy.endpoint", "acme.proxy", "d"},
		{"svc.endpoint", "svc", "unknown"},
		{"", "unknown", "unknown"},
	} {
		service, jail := crossingIdentity(tc.path)
		if service != tc.service || jail != tc.jail {
			t.Errorf("crossingIdentity(%q) = %q/%q, want %q/%q",
				tc.path, service, jail, tc.service, tc.jail)
		}
	}
}
