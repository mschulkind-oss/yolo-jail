package svcendpoint

import (
	"encoding/json"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"
)

// DELIVERY IS LAZY ON THE READ PATH, and these are the tests for the half of that
// claim nothing else exercises: a daemon that WRITES BEFORE IT READS.
//
// The preamble lives in countingConn.pre and is handed over only when somebody
// calls Read (crossing.go), so a daemon's first Write happens with the frame
// still undelivered. That ordering is FINE — but it is fine by construction, not
// by test, and the two server shapes reach it by different routes: the front
// dials upstream eagerly and copies unconditionally (front.go), while an
// endpoint-published daemon sees nothing until its own accept loop reads. So
// front-shape testing cannot surface an endpoint-shape ordering bug, and both are
// driven below.
//
// What each one pins, beyond "it does not deadlock":
//
//   - the greeting reaches the CLIENT unchanged, with no preamble bytes ahead of
//     it — the write direction is untouched (countingConn.Write), so a client
//     still cannot observe the frame even on a connection where the daemon spoke
//     first;
//   - the preamble is still intact and still FIRST on the daemon's read stream
//     afterwards, followed by the client's own bytes — a prefix, not a
//     concatenation;
//   - the tier-1 byte counts are unchanged by the reordering.

// greeterResult is what a write-first daemon saw, in order.
type greeterResult struct {
	pre  []byte // the raw preamble frame, header included
	body []byte // the client's own bytes, read after it
	err  error
}

// decodePreamble parses a raw frame the way a third-party daemon would, without
// reaching for the unexported encoder. Test-local on purpose: ReadPreamble is the
// supported reader and is covered in preamble_test.go; here the frame arrives
// through readRawPreamble so the assertions can be about ORDER on the wire.
func decodePreamble(t *testing.T, raw []byte) Preamble {
	t.Helper()
	if len(raw) < 5 {
		t.Fatalf("preamble frame is %d bytes; nothing to decode", len(raw))
	}
	var p Preamble
	if err := json.Unmarshal(raw[4:], &p); err != nil {
		t.Fatalf("decode preamble body: %v", err)
	}
	return p
}

// TestEndpointDaemonMayWriteBeforeItReads is the ENDPOINT shape: the daemon's own
// accept loop, no front. It writes a greeting the instant it accepts — before it
// has read a byte, and therefore before yolo's preamble has been delivered at all
// — and only then reads.
func TestEndpointDaemonMayWriteBeforeItReads(t *testing.T) {
	rec := captureCrossings(t)
	dir := privateDir(t)
	path := filepath.Join(dir, "greeter.endpoint")
	ln, err := Listen(path, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	const greeting = "HELLO\n"
	const payload = "ping"
	seen := make(chan greeterResult, 1)
	go func() {
		conn, aerr := ln.Accept()
		if aerr != nil {
			seen <- greeterResult{err: aerr}
			return
		}
		defer func() { _ = conn.Close() }()
		// THE WHOLE POINT: a Write with the preamble still sitting undelivered.
		if _, werr := conn.Write([]byte(greeting)); werr != nil {
			seen <- greeterResult{err: werr}
			return
		}
		raw, perr := readRawPreamble(conn)
		if perr != nil {
			seen <- greeterResult{err: perr}
			return
		}
		body := make([]byte, len(payload))
		if _, rerr := io.ReadFull(conn, body); rerr != nil {
			seen <- greeterResult{err: rerr}
			return
		}
		seen <- greeterResult{pre: raw, body: body}
	}()

	conn, err := Dial(path, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	// The client reads FIRST and writes second, which is what makes the daemon's
	// write genuinely the first byte on the connection in either direction.
	got := make([]byte, len(greeting))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("the client never got the daemon's greeting: %v", err)
	}
	if string(got) != greeting {
		t.Errorf("client saw %q, want %q — something was prepended to the RESPONSE "+
			"direction on a connection whose daemon spoke first", got, greeting)
	}
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}

	r := <-seen
	if r.err != nil {
		t.Fatalf("a daemon that wrote before it read never completed its read: %v", r.err)
	}
	p := decodePreamble(t, r.pre)
	want := Preamble{JailID: filepath.Base(dir), Service: "greeter", V: PreambleVersion}
	if p != want {
		t.Errorf("preamble = %+v, want %+v", p, want)
	}
	if string(r.body) != payload {
		t.Errorf("the daemon read %q after the preamble, want %q — the preamble is a "+
			"PREFIX on the read stream, so the client's own bytes must follow it intact",
			r.body, payload)
	}

	_ = conn.Close()
	c := rec.await(t, 1)[0]
	if c.BytesIn != int64(len(payload)) {
		t.Errorf("BytesIn = %d, want exactly %d — writing first must not move the "+
			"preamble onto the jail's byte count", c.BytesIn, len(payload))
	}
	if c.BytesOut != int64(len(greeting)) {
		t.Errorf("BytesOut = %d, want exactly %d", c.BytesOut, len(greeting))
	}
}

// TestFrontedDaemonMayWriteBeforeItReads is the FRONT shape of the same thing,
// and it is not redundant: here the preamble is already in flight from splice's
// request-direction copy while the daemon writes, so the greeting and yolo's
// frame genuinely cross on the wire. Both must arrive whole, in their own
// directions.
func TestFrontedDaemonMayWriteBeforeItReads(t *testing.T) {
	dir := privateSocketDir(t)
	upstream := filepath.Join(dir, "up.sock")
	assertSockPathFits(t, upstream)
	endpoint := filepath.Join(dir, "greeter.endpoint")

	const greeting = "HELLO\n"
	const payload = "ping"
	uln, err := net.Listen("unix", upstream)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = uln.Close() })
	seen := make(chan greeterResult, 1)
	go func() {
		for {
			c, aerr := uln.Accept()
			if aerr != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				if _, werr := c.Write([]byte(greeting)); werr != nil {
					seen <- greeterResult{err: werr}
					return
				}
				raw, perr := readRawPreamble(c)
				if perr != nil {
					seen <- greeterResult{err: perr}
					return
				}
				body := make([]byte, len(payload))
				if _, rerr := io.ReadFull(c, body); rerr != nil {
					seen <- greeterResult{err: rerr}
					return
				}
				seen <- greeterResult{pre: raw, body: body}
			}(c)
		}
	}()

	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() { _ = ServeFront(endpoint, "127.0.0.1", upstream, stop) }()
	waitProbe(t, endpoint)

	conn, err := Dial(endpoint, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	got := make([]byte, len(greeting))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("the client never got the daemon's greeting through the front: %v", err)
	}
	if string(got) != greeting {
		t.Errorf("client saw %q, want %q", got, greeting)
	}
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}

	select {
	case r := <-seen:
		if r.err != nil {
			t.Fatalf("the fronted write-first daemon never completed its read: %v", r.err)
		}
		p := decodePreamble(t, r.pre)
		want := Preamble{JailID: filepath.Base(dir), Service: "greeter", V: PreambleVersion}
		if p != want {
			t.Errorf("preamble = %+v, want %+v", p, want)
		}
		if string(r.body) != payload {
			t.Errorf("the daemon read %q after the preamble, want %q", r.body, payload)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the fronted daemon never reported; a write before the first read wedged the splice")
	}
}
