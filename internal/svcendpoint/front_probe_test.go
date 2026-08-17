package svcendpoint

// A connect-and-close probe against a FRAMED fronted daemon must not leak the
// splice, and must still produce a tier-1 record.
//
// This is the regression pin for a MEASURED defect, not a hypothetical: putting
// host-processes behind the front made every readiness probe — and every
// `yolo check` run — wedge two goroutines and two fds forever, and emit no
// crossing record at all, because the daemon blocked in its request read and so
// nothing ever closed either side. See splice's comment in front.go.

import (
	"io"
	"net"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// startFramedFront stands a daemon with the shape that hangs: it consumes the
// connection preamble, then blocks reading a request frame that a probe never
// sends. That second read is the whole point — a daemon that treats the FIRST
// frame as its request cannot reproduce this, because the preamble satisfies it.
func startFramedFront(t *testing.T) (string, string) {
	t.Helper()
	dir := privateSocketDir(t)
	upstream := filepath.Join(dir, "framed.sock")
	assertSockPathFits(t, upstream)
	endpoint := filepath.Join(dir, "framed.endpoint")

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
				if _, err := readRawPreamble(conn); err != nil {
					return
				}
				if _, err := readRawPreamble(conn); err != nil { // the request
					return
				}
				_, _ = conn.Write([]byte("ok"))
			}()
		}
	}()

	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() { _ = ServeFront(endpoint, "127.0.0.1", upstream, stop) }()
	waitProbe(t, endpoint)
	return endpoint, dir
}

// TestProbeDoesNotLeakTheSplice pins the fix. Without it this leaks exactly two
// goroutines per probe; the threshold is deliberately loose (a few stragglers
// from the runtime are fine) because the defect is unbounded growth, not a
// precise count.
func TestProbeDoesNotLeakTheSplice(t *testing.T) {
	endpoint, _ := startFramedFront(t)

	settle := func() int {
		for i := 0; i < 20; i++ {
			runtime.GC()
			time.Sleep(50 * time.Millisecond)
		}
		return runtime.NumGoroutine()
	}
	before := settle()

	const probes = 5
	for i := 0; i < probes; i++ {
		conn, err := DialLocal(endpoint, 5*time.Second)
		if err != nil {
			t.Fatalf("probe %d: %v", i, err)
		}
		_ = conn.Close() // connect-and-close: socketConnectable's exact shape
	}

	if leaked := settle() - before; leaked >= probes {
		t.Errorf("leaked %d goroutines across %d connect-and-close probes; "+
			"splice must signal EOF upstream when the client wrote nothing", leaked, probes)
	}
}

// TestProbeStillProducesACrossingRecord is the audit half of the same defect,
// and it is the half that would have gone unnoticed: a wedged splice never runs
// its deferred Close, so countingConn never emits. A fix that stopped the leak
// without restoring the record would leave every probe invisible to the audit.
func TestProbeStillProducesACrossingRecord(t *testing.T) {
	rec := captureCrossings(t)
	endpoint, dir := startFramedFront(t)
	// The sink is process-wide; assert only on this test's own crossings, or a
	// connection closing late in another test fails the exact count below.
	rec.scopeTo(dir)

	conn, err := DialLocal(endpoint, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()

	got := rec.await(t, 1)
	if len(got) != 1 {
		t.Fatalf("crossings = %d, want exactly 1 for one probe", len(got))
	}
	if got[0].BytesIn != 0 {
		t.Errorf("BytesIn = %d, want 0: a probe sends no payload, and the "+
			"preamble is yolo's own bytes rather than the jail's", got[0].BytesIn)
	}
}

// TestFramedRequestStillRoundTripsThroughTheFront is the anti-vacuity control:
// the EOF signal must fire only when the client wrote NOTHING, so a real request
// must still reach the daemon and its reply must still reach the client. Without
// this, "stop the leak by always closing upstream" passes the two tests above and
// cuts every response short.
func TestFramedRequestStillRoundTripsThroughTheFront(t *testing.T) {
	endpoint, _ := startFramedFront(t)

	conn, err := DialLocal(endpoint, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.Write(frame(`{"mode":"list"}`)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("reading the reply: %v", err)
	}
	if string(buf) != "ok" {
		t.Errorf("reply = %q, want %q", buf, "ok")
	}
}
