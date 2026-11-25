package svcendpoint

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"log"
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

// crossingRecorder collects the crossings of ONE publication directory, and
// restores the previous sink at cleanup. Every test here installs one: the sink
// is package state, so leaving one behind would leak into the next test.
type crossingRecorder struct {
	mu sync.Mutex
	// mine is the publication-directory name this recorder accepts. It is EMPTY
	// until scopeTo, and an empty scope keeps NOTHING — see captureCrossings.
	mine string
	seen []Crossing
}

// captureCrossings installs a recorder for THIS test's crossings only.
//
// The sink is PROCESS-WIDE, and a crossing is emitted when a connection closes —
// which can happen after the test that opened it has returned and restored the
// sink, by which point the next test has installed its own. That is not
// hypothetical: it turned CI red twice, on two different tests (runs 32037731872
// and 34738535819), each time asserting one test's connection against another
// test's expectations.
//
// So the recorder filters on the JAIL name, which crossingIdentity derives from the
// endpoint file's parent directory — unique per test because BOTH directory
// helpers now MkdirTemp their leaf (privateDir, privateSocketDir).
//
// AN UNSCOPED RECORDER KEEPS NOTHING, and that is the fix for the second outage
// rather than a detail. It used to accept EVERYTHING until scopeTo narrowed it,
// which made "call scopeTo" a rule a test could silently forget — and all but two
// of the tests holding a recorder had forgotten it, each one asserting on whichever
// crossing happened to land first. Keeping nothing turns that omission into a
// deterministic, immediate failure (await says so by name) instead of an
// intermittent red CI run against somebody else's connection.
func captureCrossings(t *testing.T) *crossingRecorder {
	t.Helper()
	r := &crossingRecorder{}
	prev := CrossingSink()
	SetCrossingSink(func(c Crossing) {
		r.mu.Lock()
		defer r.mu.Unlock()
		// "" is unreachable for a real crossing — crossingName maps the degenerate
		// paths to "unknown" — so an unscoped recorder matches nothing at all.
		if c.Jail != r.mine {
			return // another test's connection closing late, or we are not scoped yet
		}
		r.seen = append(r.seen, c)
	})
	t.Cleanup(func() { SetCrossingSink(prev) })
	return r
}

// scopeTo names the publication directory this recorder accepts. Call it as soon
// as the listener under test is up: nothing is recorded before it.
func (r *crossingRecorder) scopeTo(dir string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mine = filepath.Base(dir)
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
	r.mu.Lock()
	scoped := r.mine != ""
	r.mu.Unlock()
	if !scoped {
		t.Fatalf("this recorder was never scoped, so it kept nothing: call " +
			"rec.scopeTo(<the publication directory>) once the listener is up")
	}
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

// upstreamLog records, per upstream connection, the RAW connection-preamble bytes
// the daemon behind the front was handed. Raw rather than decoded on purpose: the
// "one implementation, both server shapes" claim is a claim about BYTES, and a
// decode-then-compare would hide an encoding difference between the two shapes.
type upstreamLog struct {
	mu   sync.Mutex
	pre  [][]byte
	conn int
}

func (u *upstreamLog) add(raw []byte) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.pre = append(u.pre, raw)
}

func (u *upstreamLog) connected() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.conn++
}

func (u *upstreamLog) conns() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.conn
}

// await polls until at least n preambles have been recorded, and returns them.
func (u *upstreamLog) await(t *testing.T, n int) [][]byte {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		u.mu.Lock()
		got := append([][]byte(nil), u.pre...)
		u.mu.Unlock()
		if len(got) >= n {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("upstream saw %d preambles, want at least %d", len(u.pre), n)
	return nil
}

// readRawPreamble reads one preamble frame off c and returns it VERBATIM, header
// included. io.ReadFull twice, never one big Read: the preamble is a PREFIX on
// the read stream, not a concatenation, so a single Read is not guaranteed to
// span into (or stop before) the client's own bytes.
func readRawPreamble(c net.Conn) ([]byte, error) {
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(c, hdr); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr)
	if n == 0 || n > preambleMax {
		return nil, errors.New("not a preamble frame")
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(c, body); err != nil {
		return nil, err
	}
	return append(hdr, body...), nil
}

// startEchoFront stands up a real front: an upstream AF_UNIX daemon that echoes
// one message, and a ServeFront in front of it. Returns the endpoint path and the
// log of what the daemon behind the front actually received.
//
// The daemon reads the connection preamble FIRST, which is what every daemon
// behind this transport must now do — and it is the reason the echo below can
// still be one 64-byte Read: ReadPreamble/readRawPreamble consume exactly their
// frame, so the next Read starts on the client's first byte.
func startEchoFront(t *testing.T) (string, *upstreamLog) {
	t.Helper()
	return startEchoFrontWith(t, FrontOptions{})
}

// startEchoFrontWith is startEchoFront with the front's options.
func startEchoFrontWith(t *testing.T, opts FrontOptions) (string, *upstreamLog) {
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
	seen := &upstreamLog{}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			seen.connected()
			go func() {
				defer func() { _ = conn.Close() }()
				raw, err := readRawPreamble(conn)
				if err != nil {
					return
				}
				seen.add(raw)
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
	go func() { _ = ServeFrontWithOptions(endpoint, "127.0.0.1", upstream, stop, opts) }()
	waitProbe(t, endpoint)
	return endpoint, seen
}

// ---------------------------------------------------------------------------
// R1 — connection-level facts, at the front, for every fronted daemon
// ---------------------------------------------------------------------------

// TestFrontRecordsAcceptedCrossing is the base case: one authenticated
// connection through the front produces exactly one connection-level record
// carrying which service, which jail, both byte counts and a duration.
func TestFrontRecordsAcceptedCrossing(t *testing.T) {
	rec := captureCrossings(t)
	endpoint, _ := startEchoFront(t)
	rec.scopeTo(filepath.Dir(endpoint))

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
	// EXACT, not `>=`. The bounds these replaced passed whether or not the byte
	// accounting was corrupted, which made them blind to the single
	// highest-consequence bug in this feature — see
	// TestPreambleIsNotCountedAsJailTraffic.
	if got.BytesIn != 4 {
		t.Errorf("BytesIn = %d, want exactly the 4 request bytes the jail sent", got.BytesIn)
	}
	if got.BytesOut != 8 {
		t.Errorf("BytesOut = %d, want exactly the 8 response bytes", got.BytesOut)
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
	// The LISTENER's directory, not the bogus endpoint file dialled below: the
	// record is attributed host-side, which is the property this test asserts.
	rec.scopeTo(filepath.Dir(s.path))

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
	rec.scopeTo(filepath.Dir(s.path))

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
	rec.scopeTo(dir)

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
// The connection preamble (docs/reference/loophole-transport.md, "The connection preamble")
// ---------------------------------------------------------------------------

// TestPreambleIsNotCountedAsJailTraffic is THE test for the highest-consequence
// silent failure in this feature.
//
// BytesIn is defined as "how many PLAINTEXT bytes the JAIL sent host-ward"
// (crossing.go) and surfaces verbatim as bytes_in= in internal/crossaudit. The
// natural-looking implementation of a read-stream prefix —
// io.MultiReader(bytes.NewReader(pre), c.Conn) with the counter left on the
// composite read — inflates EVERY tier-1 record by the preamble's length,
// silently, on the one field an auditor reads as volume. Nothing else in this
// package can see that: the assertion it broke used to be `BytesIn >= 4`.
//
// So this test is exact on both sides, and both halves are load-bearing: the
// preamble the daemon actually received is measured (not assumed), which is what
// keeps the count assertion from passing vacuously in a build where no preamble
// is sent at all.
func TestPreambleIsNotCountedAsJailTraffic(t *testing.T) {
	rec := captureCrossings(t)
	endpoint, seen := startEchoFront(t)
	rec.scopeTo(filepath.Dir(endpoint))

	const payload = "ping"
	conn, err := Dial(endpoint, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil || string(buf[:n]) != "got:"+payload {
		t.Fatalf("response = %q, %v", buf[:n], err)
	}
	_ = conn.Close()

	// ANTI-VACUITY: the daemon really was handed a preamble, and a non-trivial
	// one. Without this the count assertion below would also pass on a build that
	// sends nothing.
	pre := seen.await(t, 1)[0]
	if len(pre) < 5 {
		t.Fatalf("upstream received a %d-byte preamble; nothing to exclude from the count", len(pre))
	}

	got := rec.await(t, 1)[0]
	if got.BytesIn != int64(len(payload)) {
		t.Errorf("BytesIn = %d, want exactly %d.\nThe preamble is %d bytes and %d is what the "+
			"count would be if it were included — the tier-1 record must describe what the "+
			"JAIL sent, not what yolo prepended for the daemon.",
			got.BytesIn, len(payload), len(pre), len(payload)+len(pre))
	}
}

// TestPreambleNeverAppearsInTheResponseDirection: the preamble is host→daemon
// only, exactly once, at connection open. §5.5's "the jail-side client never sees
// it, so a client cannot forge, suppress, or even observe it" is a property of
// countingConn.Write being untouched, and this is what says so out loud.
func TestPreambleNeverAppearsInTheResponseDirection(t *testing.T) {
	rec := captureCrossings(t)
	endpoint, seen := startEchoFront(t)
	rec.scopeTo(filepath.Dir(endpoint))

	conn, err := Dial(endpoint, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read the whole response: %v", err)
	}
	_ = conn.Close()

	// Byte-for-byte the daemon's answer: nothing before it, nothing after it.
	if string(resp) != "got:ping" {
		t.Errorf("client saw %q, want %q — something was prepended to the RESPONSE", resp, "got:ping")
	}
	pre := seen.await(t, 1)[0]
	if bytes.Contains(resp, pre) {
		t.Error("the connection preamble came back to the client in the response direction")
	}
	if bytes.Contains(resp, []byte(`"jail_id"`)) {
		t.Errorf("the response carries a jail_id envelope: %q", resp)
	}
	// And the byte count agrees: 8 out, not 8 plus a preamble.
	if got := rec.await(t, 1)[0]; got.BytesOut != int64(len("got:ping")) {
		t.Errorf("BytesOut = %d, want exactly 8", got.BytesOut)
	}
}

// TestBothServerShapesSeeTheSamePreambleBytes is §5.5's "one implementation
// covers both server shapes" claim, measured rather than argued: a daemon behind
// the FRONT and a daemon served DIRECTLY by Listen must receive byte-identical
// preambles for the same identity, or the Go server library and a third-party
// daemon are already two implementations.
//
// The two listeners share a directory and a service basename — crossingIdentity
// strips the extension, so "echo.endpoint" and "echo.ep" are the same service in
// the same jail — which is what makes a byte comparison meaningful instead of a
// field-by-field one.
func TestBothServerShapesSeeTheSamePreambleBytes(t *testing.T) {
	dir := privateSocketDir(t)
	upstream := filepath.Join(dir, "up.sock")
	assertSockPathFits(t, upstream)

	// Shape 1: fronted. A dumb upstream that records its preamble and hangs up.
	fronted := make(chan []byte, 1)
	uln, err := net.Listen("unix", upstream)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = uln.Close() })
	go func() {
		for {
			c, err := uln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				if raw, err := readRawPreamble(c); err == nil {
					select {
					case fronted <- raw:
					default:
					}
				}
			}()
		}
	}()
	frontPath := filepath.Join(dir, "echo.endpoint")
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() { _ = ServeFront(frontPath, "127.0.0.1", upstream, stop) }()
	waitProbe(t, frontPath)

	// Shape 2: endpoint-published. The daemon's own accept loop, no front.
	direct := make(chan []byte, 1)
	directPath := filepath.Join(dir, "echo.ep")
	dln, err := Listen(directPath, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dln.Close() })
	go func() {
		for {
			c, err := dln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				if raw, err := readRawPreamble(c); err == nil {
					select {
					case direct <- raw:
					default:
					}
				}
			}()
		}
	}()

	for _, p := range []string{frontPath, directPath} {
		conn, err := Dial(p, 5*time.Second)
		if err != nil {
			t.Fatalf("dial %s: %v", filepath.Base(p), err)
		}
		// DELIVERY IS LAZY ON THE READ PATH, so the two shapes are reached
		// differently and both must be exercised: the front dials upstream eagerly
		// and copies unconditionally, while an endpoint-published daemon sees the
		// preamble only when it reads. Neither is given a client byte here, which
		// is the point — a preamble that needed one would not arrive at all.
		t.Cleanup(func() { _ = conn.Close() })
	}

	var got [2][]byte
	for i, ch := range []chan []byte{fronted, direct} {
		select {
		case got[i] = <-ch:
		case <-time.After(5 * time.Second):
			t.Fatalf("shape %d never received a preamble", i)
		}
	}
	want := encodePreamble(Preamble{
		JailID:  filepath.Base(dir),
		Service: "echo",
		V:       PreambleVersion,
	})
	if !bytes.Equal(got[0], got[1]) {
		t.Errorf("the two server shapes received different preambles:\n front %q\ndirect %q",
			got[0], got[1])
	}
	if !bytes.Equal(got[0], want) {
		t.Errorf("fronted preamble = %q, want %q", got[0], want)
	}
	if !bytes.Equal(got[1], want) {
		t.Errorf("endpoint preamble = %q, want %q", got[1], want)
	}
}

// TestRejectedConnectionNeverReachesADaemon: authentication precedes the wrapper
// that carries the preamble (listen.go), so a connection that failed the token
// check produces no preamble because it produces no connection — the daemon never
// hears about it at all.
//
// This is what keeps internal/hostservice/transport_test.go's case-2 reasoning
// intact: a rejected client cannot get yolo to assert an identity for it, and
// cannot get a daemon to read anything it wrote.
func TestRejectedConnectionNeverReachesADaemon(t *testing.T) {
	rec := captureCrossings(t)
	endpoint, seen := startEchoFront(t)
	// The FRONT's directory. The rejected connection is attributed to the listener
	// that refused it, never to the bogus endpoint file the client read — which is
	// the same host-side attribution TestRejectedCrossingIsRecorded pins.
	rec.scopeTo(filepath.Dir(endpoint))

	ep, err := Read(endpoint)
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
	// THE WHOLE RECORDED SET, not got[0]. Exactly one connection has ever been made
	// to this front and the line above just proved it was refused, so "the first
	// record that happened to arrive" was never the assertion anyone meant — and in
	// CI run 34738535819 the record at index 0 said "accepted" while that same
	// ErrAuthRejected check passed, i.e. index 0 was not this connection at all.
	// Asserting the set is also STRICTLY MORE teeth than the index was: it fails a
	// build that records a rejection AND an acceptance for one refused connection,
	// which got[0] could not see.
	got := rec.await(t, 1)
	if len(got) != 1 || got[0].Outcome != CrossingRejected {
		t.Fatalf("crossings = %+v, want exactly one %q", got, CrossingRejected)
	}
	if n := seen.conns(); n != 0 {
		t.Errorf("the upstream daemon was reached %d times by a REJECTED connection", n)
	}

	// Anti-vacuity on the same fixture: the correct token does reach the daemon,
	// so the zero above is the rejection and not a broken front.
	conn, err := Dial(endpoint, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	seen.await(t, 1)
	if n := seen.conns(); n != 1 {
		t.Errorf("upstream connections = %d, want exactly the one authenticated crossing", n)
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
	logs := &syncBuf{}
	warned := make(chan struct{})
	prevLogger := Logger
	Logger = log.New(&notifyWriter{w: logs, needle: "crossing audit", hit: warned}, "", 0)
	t.Cleanup(func() { Logger = prevLogger })
	prev := CrossingSink()
	t.Cleanup(func() { SetCrossingSink(prev) })
	SetCrossingSink(func(Crossing) { panic("sink exploded") })

	endpoint, _ := startEchoFront(t)

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

	// A crossing is recorded when the server side of the connection finishes, which
	// can be after the client has read its reply, so wait for the warning itself
	// (written before the sink is uninstalled) rather than checking at once.
	select {
	case <-warned:
	case <-time.After(time.Minute):
		t.Fatalf("no warning logged for the panicking sink; logs:\n%s", logs.String())
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

	endpoint, _ := startEchoFront(t)
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
	endpoint, _ := startEchoFront(t)
	rec.scopeTo(filepath.Dir(endpoint))
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

// TestRecorderIgnoresAnotherTestsCrossing pins the isolation fix deterministically,
// because the bug it prevents is not: the sink is process-wide, and a connection
// opened by one test can close — and emit — while the NEXT test holds the sink. That
// is how CI run 32037731872 went red.
//
// Synthesising the foreign record is the point. Reproducing the real race needs a
// loaded machine and still does not fail reliably; this fails immediately if the
// filter is removed.
func TestRecorderIgnoresAnotherTestsCrossing(t *testing.T) {
	rec := captureCrossings(t)
	rec.scopeTo("/tmp/yj-mine-12345")

	sink := CrossingSink()
	if sink == nil {
		t.Fatal("captureCrossings did not install a sink")
	}
	sink(Crossing{Service: "theirs", Jail: "yj-theirs-99999"}) // another test's, closing late
	sink(Crossing{Service: "mine", Jail: "yj-mine-12345"})     // this test's

	got := rec.all()
	if len(got) != 1 {
		t.Fatalf("recorded %d crossings, want exactly 1 — the foreign one must be dropped", len(got))
	}
	if got[0].Service != "mine" {
		t.Errorf("kept Service = %q, want %q", got[0].Service, "mine")
	}
}

// TestAnUnscopedRecorderKeepsNothing pins the second half of the same isolation
// fix, and it is the half that turned CI red a second time (run 34738535819).
//
// A recorder used to accept EVERY crossing until scopeTo narrowed it, so a test
// that forgot to scope asserted on whichever record the process happened to emit
// first — its own on a quiet machine, a neighbour's under load. Keeping nothing
// makes the omission deterministic: await fails by name instead of the assertion
// failing once in a hundred runs against someone else's connection.
func TestAnUnscopedRecorderKeepsNothing(t *testing.T) {
	rec := captureCrossings(t) // deliberately NOT scoped

	sink := CrossingSink()
	if sink == nil {
		t.Fatal("captureCrossings did not install a sink")
	}
	sink(Crossing{Service: "theirs", Jail: "yj-theirs-99999", Outcome: CrossingAccepted})
	sink(Crossing{Service: "also-theirs", Jail: "yj-other-11111", Outcome: CrossingRejected})

	if got := rec.all(); len(got) != 0 {
		t.Fatalf("an unscoped recorder kept %d crossings (%+v), want none — scoping "+
			"must be what OPENS the recorder, not what narrows an open one", len(got), got)
	}
}

// notifyWriter passes writes to w and closes hit the first time a write contains needle.
type notifyWriter struct {
	w      io.Writer
	needle string
	hit    chan struct{}
	once   sync.Once
}

func (n *notifyWriter) Write(p []byte) (int, error) {
	written, err := n.w.Write(p)
	if strings.Contains(string(p), n.needle) {
		n.once.Do(func() { close(n.hit) })
	}
	return written, err
}
