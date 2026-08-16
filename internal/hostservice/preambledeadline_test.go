package hostservice

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
)

// The SERVER half of the no-session-deadline contract, which nothing else pins.
//
// svcendpoint.ReadPreamble arms a five-second read deadline and clears it on
// every return path, and preamble_test.go over there proves the clear happens.
// What that cannot say is whether the clear SURVIVES the daemon's own read path:
// a deadline left behind on this side kills the next read on the connection —
// intermittently, at exactly five seconds, with no error text and a green
// suite — and the only symptom is a jail request that stops answering.
//
// Driven at serveListener rather than through ServeEndpoint on purpose: an
// endpoint-published daemon is handed its preamble from memory (svcendpoint's
// countingConn), so no deadline can ever expire against it and the test would
// pass on a build that never cleared anything. Over a plain listener the frame
// really crosses a socket, which is the ServeFrontedUnix shape and the only one
// where the deadline is load-bearing.

// deadlineCompressConn shrinks every non-zero read deadline to window, so a
// forgotten clear shows up in milliseconds instead of the five real seconds
// handshakeTimeout would cost. The contract under test is "the deadline is
// cleared", never "it is five seconds" — svcendpoint asserts the constant.
type deadlineCompressConn struct {
	net.Conn
	window time.Duration

	mu    sync.Mutex
	armed int
}

func (c *deadlineCompressConn) SetReadDeadline(t time.Time) error {
	if !t.IsZero() {
		c.mu.Lock()
		c.armed++
		c.mu.Unlock()
		t = time.Now().Add(c.window)
	}
	return c.Conn.SetReadDeadline(t)
}

func (c *deadlineCompressConn) armCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.armed
}

// deadlineCompressListener hands out deadlineCompressConns and remembers them, so
// a test can assert the compression it depends on actually fired.
type deadlineCompressListener struct {
	net.Listener
	window time.Duration

	mu    sync.Mutex
	conns []*deadlineCompressConn
}

func (l *deadlineCompressListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	wrapped := &deadlineCompressConn{Conn: c, window: l.window}
	l.mu.Lock()
	l.conns = append(l.conns, wrapped)
	l.mu.Unlock()
	return wrapped, nil
}

func (l *deadlineCompressListener) armsTotal() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, c := range l.conns {
		n += c.armCount()
	}
	return n
}

// TestTheHandshakeDeadlineDoesNotOutliveThePreamble: a client that sends its
// preamble, goes quiet for longer than the handshake window, and only then sends
// its request must still be served. Under a forgotten clear the request read
// times out and the handler never runs.
func TestTheHandshakeDeadlineDoesNotOutliveThePreamble(t *testing.T) {
	const window = 50 * time.Millisecond

	// captureLog, never `Logger = …`: handleOne writes its access line from a
	// deferred func on a connection goroutine that serveListener does not join, so
	// reassigning the Logger in a cleanup races it (see logRouter).
	logs := captureLog(t)

	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := &deadlineCompressListener{Listener: raw, window: window}
	calls := &atomic.Int64{}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = serveListener(func(s *Session) {
			calls.Add(1)
			s.Stdout("pong\n")
		}, ln, stop, true)
		close(done)
	}()
	t.Cleanup(func() { close(stop); <-done })

	conn, err := net.Dial("tcp", raw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(clientPreamble(t, "yolo-host-services-7f3a1b2c", 1)); err != nil {
		t.Fatal(err)
	}

	// The gap is the test. It is comfortably past the (compressed) handshake
	// window and well inside the client's own deadline.
	time.Sleep(4 * window)

	// BOTH halves report the same diagnosis, because a leaked deadline can surface
	// on either: the daemon times out, closes, and whichever of the write or the
	// read the client is on at that moment is the one that fails.
	const leaked = "The handshake read deadline outlived the preamble read, so every " +
		"long-lived stream on this transport dies at handshakeTimeout."
	if err := frameproto.WriteRequest(conn, []byte(`{"mode":"list"}`)); err != nil {
		t.Fatalf("the daemon hung up on a request sent %v after the preamble: %v\n%s\nDaemon log:\n%s",
			4*window, err, leaked, logs.String())
	}
	if _, err := frameproto.ReadFrame(conn); err != nil {
		t.Fatalf("no response to a request sent %v after the preamble: %v\n%s\nDaemon log:\n%s",
			4*window, err, leaked, logs.String())
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("handler calls = %d, want 1", n)
	}

	// ANTI-VACUITY: the compression only means something if a deadline was armed
	// at all. Without this the test would also pass on a build whose preamble read
	// set no deadline in the first place — which would be a different bug, not a
	// fix (an unbounded preamble read parks a goroutine per stalled connection).
	if n := ln.armsTotal(); n == 0 {
		t.Error("no read deadline was ever armed, so the clear proved nothing; " +
			"the preamble read must bound itself")
	}
}

// TestAStalledPreambleDoesNotBlockOtherConnections: the preamble read runs on the
// connection's OWN goroutine, never in the accept loop.
//
// One client that connects and sends half a length prefix would otherwise hold
// every other client for the full handshake timeout — and a loop of them is a
// denial of service from exactly the adversary this transport is built against,
// which is why svcendpoint's own accept loop authenticates off-loop and why
// journald's ServeEndpoint reads its preamble inside the per-connection
// goroutine. Nothing pinned the same property here.
func TestAStalledPreambleDoesNotBlockOtherConnections(t *testing.T) {
	addr, calls, logs := startPreambleListener(t, true)

	// Two of the four header bytes, then silence — no close, so the daemon's
	// preamble read cannot finish until its deadline expires.
	stalled, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stalled.Close() }()
	if _, err := stalled.Write([]byte{0x00, 0x00}); err != nil {
		t.Fatal(err)
	}

	// A second client must be served IMMEDIATELY — well inside the stalled
	// connection's handshake window, which is what makes the assertion mean
	// "concurrent" rather than "eventually".
	good, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("the accept loop stopped accepting while a preamble was stalled: %v", err)
	}
	defer func() { _ = good.Close() }()
	_ = good.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := good.Write(clientPreamble(t, "yolo-host-services-7f3a1b2c", 1)); err != nil {
		t.Fatal(err)
	}
	if err := frameproto.WriteRequest(good, []byte(`{"mode":"list"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := frameproto.ReadFrame(good); err != nil {
		t.Fatalf("a second client got no response while one stalled mid-preamble: %v\n"+
			"the preamble must be read on the connection's own goroutine, never in the "+
			"accept loop.\nDaemon log:\n%s", err, logs.String())
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("handler calls = %d, want 1 (the stalled connection must not reach the handler)", n)
	}
}
