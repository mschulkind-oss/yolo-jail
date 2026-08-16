package svcendpoint

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// readPreambleFrom feeds raw to ReadPreamble over a net.Pipe and returns what the
// reader made of it. The write runs in a goroutine because net.Pipe is
// synchronous and unbuffered: a Write blocks until the reader has taken every
// byte, which is also what makes "the reader stopped early" observable here
// rather than silently buffered away.
func readPreambleFrom(t *testing.T, raw []byte) (Preamble, error) {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	go func() {
		_, _ = client.Write(raw)
		// EOF after the frame, so a reader waiting for more fails instead of
		// hanging the test out to the package timeout.
		_ = client.Close()
	}()
	return ReadPreamble(server)
}

// frame wraps body the way both the preamble and a frameproto request are framed
// — 4-byte big-endian length, then the body. Hand-rolled rather than borrowed
// from internal/frameproto because this package has ZERO internal imports by
// design (doc.go), and the identical shape of the two frames is the whole point
// of TestFrameprotoRequestIsRejectedAsAPreamble below.
func frame(body string) []byte {
	out := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(out[:4], uint32(len(body)))
	copy(out[4:], body)
	return out
}

func TestPreambleRoundTrip(t *testing.T) {
	want := Preamble{
		// The production shape: the publication DIRECTORY base name, not the
		// container name (crossingIdentity).
		JailID:  "yolo-host-services-7f3a1b2c",
		Service: "host-processes",
		V:       PreambleVersion,
	}
	got, err := readPreambleFrom(t, encodePreamble(want))
	if err != nil {
		t.Fatalf("ReadPreamble: %v", err)
	}
	if got != want {
		t.Errorf("round trip changed the preamble:\n got %+v\nwant %+v", got, want)
	}
}

// TestPreambleOversizeLengthIsRefused pins "CAP BEFORE ALLOCATING". The length is
// 4 GiB, so an implementation that allocated first and validated second would not
// return a tidy error here — it would exhaust the test runner.
func TestPreambleOversizeLengthIsRefused(t *testing.T) {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], math.MaxUint32)
	if _, err := readPreambleFrom(t, hdr[:]); !errors.Is(err, ErrBadPreambleFrame) {
		t.Errorf("ReadPreamble(4 GiB length) error = %v, want ErrBadPreambleFrame", err)
	}
	// Zero is refused for the same reason it is in the token frame: an empty
	// body cannot carry a version, so accepting it would mean accepting a
	// preamble that fails the check below for a reason nobody can act on.
	var zero [4]byte
	if _, err := readPreambleFrom(t, zero[:]); !errors.Is(err, ErrBadPreambleFrame) {
		t.Errorf("ReadPreamble(0 length) error = %v, want ErrBadPreambleFrame", err)
	}
}

// TestPreambleCapPrecedesTheAllocationTextually is the other half of the test
// above, and it is a SOURCE assertion on purpose: the runtime half only proves
// that today's max-uint32 case is refused, while the property that matters is
// ordering — the cap has to sit between the length and the make, forever. A
// refactor that moved the allocation up would keep every behavioural test green
// on a 4 GiB length only by luck of the allocator.
func TestPreambleCapPrecedesTheAllocationTextually(t *testing.T) {
	src, err := os.ReadFile("preamble.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	capAt := strings.Index(body, "n > preambleMax")
	allocAt := strings.Index(body, "body := make([]byte, n)")
	if capAt < 0 || allocAt < 0 {
		t.Fatalf("preamble.go no longer contains the cap (%d) or the allocation (%d) in the expected form", capAt, allocAt)
	}
	if capAt > allocAt {
		t.Error("the preambleMax cap is checked AFTER the buffer is allocated — a garbage length prefix now allocates whatever it names")
	}
}

// TestFrameprotoRequestIsRejectedAsAPreamble is the highest-value test in this
// file. A preamble and a frameproto request are byte-identical in shape, and
// encoding/json ignores unknown fields — so without the version check a daemon
// that read a real request as its preamble would decode it "successfully" with
// every field zero and then block forever on the next ReadRequestBytes. This is
// what makes reading the wrong frame LOUD instead of a hang.
func TestFrameprotoRequestIsRejectedAsAPreamble(t *testing.T) {
	got, err := readPreambleFrom(t, frame(`{"mode":"list"}`))
	if !errors.Is(err, ErrPreambleVersion) {
		t.Fatalf("ReadPreamble(frameproto request) error = %v, want ErrPreambleVersion", err)
	}
	if got != (Preamble{}) {
		t.Errorf("a rejected preamble returned fields: %+v", got)
	}
	// A future version is refused by the same gate, which is the property §9
	// asks for: an unrecognized version fails loudly rather than guessing at a
	// key set this build has never seen.
	if _, err := readPreambleFrom(t, frame(`{"jail_id":"j","service":"s","v":2}`)); !errors.Is(err, ErrPreambleVersion) {
		t.Errorf("ReadPreamble(v=2) error = %v, want ErrPreambleVersion", err)
	}
	// Not-JSON-at-all is a frame problem, not a version problem.
	if _, err := readPreambleFrom(t, frame(`not json`)); !errors.Is(err, ErrBadPreambleFrame) {
		t.Errorf("ReadPreamble(garbage body) error = %v, want ErrBadPreambleFrame", err)
	}
}

// deadlineRecorder records every read deadline ReadPreamble sets and, when
// compress is non-zero, SUBSTITUTES a much shorter one for any non-zero request.
// The substitution is what lets the behavioural test below observe a forgotten
// clear in milliseconds instead of the five real seconds handshakeTimeout would
// cost — the contract under test is "the deadline is cleared", not "it is five
// seconds", and the constant is asserted separately by the recorded values.
type deadlineRecorder struct {
	net.Conn
	compress time.Duration
	set      []time.Time
}

func (d *deadlineRecorder) SetReadDeadline(t time.Time) error {
	d.set = append(d.set, t)
	if !t.IsZero() && d.compress > 0 {
		t = time.Now().Add(d.compress)
	}
	return d.Conn.SetReadDeadline(t)
}

// TestPreambleClearsTheReadDeadlineOnEveryPath pins the clear that nothing else
// pins on the server side (svcendpoint_test.go covers only the CLIENT half of the
// no-session-deadline contract). A preamble read that leaves a deadline behind
// kills every long-lived stream at exactly five seconds, intermittently, with no
// error text and a green suite — so every return path is checked, not just the
// happy one.
func TestPreambleClearsTheReadDeadlineOnEveryPath(t *testing.T) {
	good := encodePreamble(Preamble{JailID: "j", Service: "s", V: PreambleVersion})
	var badLen [4]byte
	binary.BigEndian.PutUint32(badLen[:], math.MaxUint32)

	cases := []struct {
		name string
		raw  []byte
	}{
		{"success", good},
		{"short header", []byte{0x00}},
		{"oversize length", badLen[:]},
		{"truncated body", good[:len(good)-1]},
		{"wrong version", frame(`{"mode":"list"}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, server := net.Pipe()
			defer func() { _ = client.Close(); _ = server.Close() }()
			rec := &deadlineRecorder{Conn: server}
			go func() {
				_, _ = client.Write(tc.raw)
				_ = client.Close()
			}()
			_, _ = ReadPreamble(rec)

			if len(rec.set) != 2 {
				t.Fatalf("SetReadDeadline called %d times, want 2 (arm, then clear): %v", len(rec.set), rec.set)
			}
			if rec.set[0].IsZero() {
				t.Error("the preamble read was never armed with a deadline")
			}
			if d := time.Until(rec.set[0]); d > handshakeTimeout || d < handshakeTimeout/2 {
				t.Errorf("armed deadline is %v out, want ~%v", d, handshakeTimeout)
			}
			if !rec.set[1].IsZero() {
				t.Errorf("the read deadline was left at %v instead of being cleared", rec.set[1])
			}
		})
	}
}

// TestPreambleLeavesTheStreamWithoutADeadline is the behavioural half of the test
// above: after a successful ReadPreamble the connection carries no read deadline,
// so a byte that arrives well after the handshake window still reads cleanly.
// Compressed to milliseconds by deadlineRecorder rather than sleeping past the
// real five seconds.
func TestPreambleLeavesTheStreamWithoutADeadline(t *testing.T) {
	const window = 20 * time.Millisecond

	client, server := net.Pipe()
	defer func() { _ = client.Close(); _ = server.Close() }()
	rec := &deadlineRecorder{Conn: server, compress: window}

	go func() {
		_, _ = client.Write(encodePreamble(Preamble{JailID: "j", Service: "s", V: PreambleVersion}))
		time.Sleep(3 * window) // past the (compressed) handshake window
		_, _ = client.Write([]byte("late"))
	}()

	if _, err := ReadPreamble(rec); err != nil {
		t.Fatalf("ReadPreamble: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(rec, buf); err != nil {
		t.Fatalf("read after the preamble: %v — the handshake deadline outlived the preamble", err)
	}
	if string(buf) != "late" {
		t.Errorf("read %q after the preamble, want %q", buf, "late")
	}
}

// TestPreambleFrameIsOneWrite pins the single-Write framing. Two writes are a
// partial read for whoever is waiting, and the preamble's reader is the first
// thing a third-party daemon implements.
func TestPreambleFrameIsOneWrite(t *testing.T) {
	got := encodePreamble(Preamble{JailID: "yolo-host-services-7f3a1b2c", Service: "host-processes", V: PreambleVersion})
	if n := binary.BigEndian.Uint32(got[:4]); int(n) != len(got)-4 {
		t.Errorf("length prefix says %d, body is %d bytes", n, len(got)-4)
	}
	if body := string(got[4:]); !strings.Contains(body, `"jail_id":"yolo-host-services-7f3a1b2c"`) ||
		!strings.Contains(body, `"service":"host-processes"`) ||
		!strings.Contains(body, `"v":1`) {
		t.Errorf("preamble body = %s, want all three JSON keys", body)
	}
}
