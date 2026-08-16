package svcendpoint

// The connection preamble — docs/design/broker-as-a-pack.md §5.5.
//
// NOTHING CALLS ANY OF THIS YET. The producer is listenWith's accepted-connection
// wrapper and the consumers are yolo's own daemons; both arrive in the next
// commit. This file is the codec alone, so that the frame has exactly one
// encoder and exactly one reader before anything depends on either.
//
// The imports below are stdlib ONLY, and that is a package invariant, not an
// accident of this file: `go list -deps ./internal/svcendpoint | rg yolo-jail`
// must keep printing just this package, or the leanest baked clients (cmd/yolo-ps
// is "a pure frameproto client — no config, no json5") start dragging the CLI in.
// The frame is encoding/json, NOT internal/jsonx, for that reason — and it can
// be, because yolo is the only producer and no byte-identical re-encode is
// required of anyone.

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

const (
	// PreambleVersion is the envelope version this build writes and the only one
	// it accepts. It is MANDATORY rather than optional (§9): the preamble is
	// meant to grow, so the discipline is not "keep it empty" but "keep it a
	// versioned envelope" — a daemon that does not recognize a version must fail
	// loudly rather than guess at a key set it has never seen.
	PreambleVersion = 1

	// preambleMax caps the preamble frame, and the cap is checked BEFORE the
	// buffer is allocated — the same rule tokenFrameMax states, for the same
	// reason. It is a lower bar here (the writer is yolo itself), but a reader
	// that trusts a 4-byte length it did not write allocates whatever it is
	// handed, and ReadPreamble is exported for third-party daemons.
	preambleMax = 4096
)

// ErrBadPreambleFrame / ErrPreambleVersion are EXPORTED, unlike token.go's
// errBadTokenFrame, because their reader is a daemon rather than an accept loop:
// a rejected token drops a connection nobody was going to hear about, whereas a
// preamble failure has to be distinguishable by the daemon that has to decide
// what to do with the connection.
var (
	ErrBadPreambleFrame = errors.New("svcendpoint: preamble frame out of range")
	ErrPreambleVersion  = errors.New("svcendpoint: unrecognized preamble version")
)

// Preamble is what yolo tells a daemon ABOUT THE CONNECTION before the
// connection's own bytes start. It is host→daemon only, exactly once, at
// connection open; it never appears in the response direction and the jail-side
// client never sees it, so a client cannot forge, suppress, or even observe it.
//
// It is deliberately not called an identity frame: identity is what it carries
// first, not what it is. Naming it for today's single field would make the
// second field look like a violation instead of an addition — V is what keeps
// that honest.
type Preamble struct {
	// JailID is HOST-ASSERTED. It is the publication directory's base name, the
	// same value crossingIdentity derives for the tier-1 connection record, so
	// the two agree by construction rather than by two independent derivations.
	JailID string `json:"jail_id"`
	// Service is which loophole this listener is.
	Service string `json:"service"`
	// V is the envelope version — see PreambleVersion.
	V int `json:"v"`
}

// encodePreamble frames one preamble: 4-byte big-endian length then the JSON
// object, in ONE allocation so it goes out as one Write (writeTokenFrame's
// idiom, for the same reason — a split frame is a partial read for whoever is
// waiting on it).
//
// Unexported: listenWith is the only producer, and keeping it that way is what
// makes "yolo is the only thing that ever writes a preamble" a property of the
// type system rather than a convention.
//
// json.Marshal cannot fail for this closed struct — two strings and an int,
// where invalid UTF-8 in a string is replaced rather than refused — so there is
// no error to return to a listener that would have nothing to do with one.
func encodePreamble(p Preamble) []byte {
	body, _ := json.Marshal(p)
	frame := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(body)))
	copy(frame[4:], body)
	return frame
}

// ReadPreamble reads one connection preamble off c.
//
// ONE READER FOR EVERY DAEMON, so the deadline clear below is written once. A
// preamble read that sets a deadline and forgets to clear it kills every
// long-lived stream — a host-processes `tree` query, a journalctl follow — at
// exactly five seconds, intermittently, with no error text and a green suite.
// token.go:125 clears it on the success path only, because every failure there
// drops the connection; here a failure may be survivable (see below), so the
// clear is deferred and covers EVERY return path.
//
// THE VERSION CHECK IS THE POINT, not a formality. The preamble and a frameproto
// request are byte-identical in shape — 4-byte BE length then a JSON object —
// and encoding/json ignores unknown fields, so a daemon that reads a real
// request `{"mode":"list"}` as a preamble would decode it "successfully" with
// every field zero and then block forever on the next ReadRequestBytes.
// Rejecting V != PreambleVersion turns that hang into a named error.
//
// A caller must NEVER fall back to treating these bytes as a request. It must
// also not treat a short or absent preamble as fatal to the daemon: yolo's own
// readiness probe is a bare connect-and-close on a fronted daemon's socket that
// bypasses the front and therefore sends nothing, so "closed before a preamble"
// has to degrade exactly as "closed before a request" already does.
//
// Nothing here logs: the preamble carries no secret today, but a debug line
// printing its bytes would establish the wrong pattern on a stream that V exists
// to let grow (listen.go's Logger is payload-free by construction).
func ReadPreamble(c net.Conn) (Preamble, error) {
	_ = c.SetReadDeadline(time.Now().Add(handshakeTimeout))
	defer func() { _ = c.SetReadDeadline(time.Time{}) }() // clear on EVERY path

	hdr := make([]byte, 4)
	if _, err := io.ReadFull(c, hdr); err != nil {
		return Preamble{}, err
	}
	n := binary.BigEndian.Uint32(hdr)
	// CAP BEFORE ALLOCATING.
	if n == 0 || n > preambleMax {
		return Preamble{}, fmt.Errorf("%w: length %d", ErrBadPreambleFrame, n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(c, body); err != nil {
		return Preamble{}, err
	}
	var p Preamble
	if err := json.Unmarshal(body, &p); err != nil {
		return Preamble{}, fmt.Errorf("%w: %s", ErrBadPreambleFrame, err)
	}
	if p.V != PreambleVersion {
		return Preamble{}, fmt.Errorf("%w: got v=%d, want v=%d", ErrPreambleVersion, p.V, PreambleVersion)
	}
	return p, nil
}
