package svcendpoint

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

const (
	// tokenBytes is a token's entropy: 32 crypto/rand bytes, rendered as 64
	// lowercase hex characters (whitespace-free by construction, which is what
	// lets the endpoint file be whitespace-separated with no escaping).
	tokenBytes = 32

	// tokenFrameMax caps the leading token frame. The cap is checked BEFORE the
	// buffer is allocated: without it a garbage 4-byte length prefix from an
	// unauthenticated caller allocates gigabytes pre-auth.
	tokenFrameMax = 4096

	// authAck is the single byte the server writes after a token verifies. Its
	// only job is ATTRIBUTION: a token mismatch is a post-accept drop, so without
	// an ack the client sees EOF-before-response and reports it as a failure of
	// whatever is behind the transport — the most misleading message in the system
	// for the most likely misconfiguration. Because the ack arrives BEFORE the
	// request is ever sent, EOF at that point is unambiguously auth failure.
	// It leaks nothing: the server sends NOTHING on failure, so a port scanner
	// learns only that it was hung up on.
	authAck = 0x01

	// handshakeTimeout bounds the pre-request exchange (token frame in, ack out)
	// at both ends. It is deliberately NOT a session deadline — see Dial.
	handshakeTimeout = 5 * time.Second
)

// errBadTokenFrame / errBadToken are internal: a rejected connection is closed by
// the accept loop and never reaches a caller, so these exist to be logged and
// counted, not returned. ErrAuthRejected is the CLIENT-side name for the same
// event, which is where attribution is needed.
var (
	errBadTokenFrame = errors.New("svcendpoint: token frame out of range")
	errBadToken      = errors.New("svcendpoint: token mismatch")
)

// NewToken mints a fresh bearer token: 32 crypto/rand bytes as 64 lowercase hex.
//
// The LISTENER mints its own token in process, and the run pipeline never sees it.
// That is what makes this file the single writer of a single artifact — no
// cross-process agreement, so no host-side token file to persist, hand over on
// argv, harden against planting, or forget to reap (a `yolo prune --apply` that
// reaped a pid file but not a token file would leave a live credential on disk
// forever). Token retirement becomes identical to endpoint retirement, and
// rotation comes free.
//
// Scope is per-(jail, service), not per-jail: at one listener per service each
// Listen mints its own, so compromising one service's published file does not
// grant the others. Under in-process minting that costs literally nothing.
func NewToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// IsToken reports whether s is well-formed: exactly 64 lowercase hex characters.
// This is a FORMAT check, never an authorization check — see verifyTokenFrame.
func IsToken(s string) bool {
	if len(s) != tokenBytes*2 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// writeTokenFrame writes the leading token frame — 4-byte big-endian length then
// the raw token bytes — as ONE Write, before any protocol bytes.
func writeTokenFrame(w io.Writer, token string) error {
	frame := make([]byte, 4+len(token))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(token)))
	copy(frame[4:], token)
	_, err := w.Write(frame)
	return err
}

// verifyTokenFrame reads and verifies one connection's leading token frame, then
// writes the ack. On success the read deadline is CLEARED, which is what preserves
// the no-session-deadline contract for the long-lived stream that follows (see
// Dial). Every failure returns an error and logs PAYLOAD-FREE: a length, never a
// value, and never the token, the cert, or the endpoint line.
func verifyTokenFrame(conn net.Conn, token string) error {
	_ = conn.SetReadDeadline(time.Now().Add(handshakeTimeout))
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(hdr)
	// CAP BEFORE ALLOCATING.
	if n == 0 || n > tokenFrameMax {
		Logger.Printf("auth: token frame length %d out of range — dropping connection", n)
		return errBadTokenFrame
	}
	got := make([]byte, n)
	if _, err := io.ReadFull(conn, got); err != nil {
		return err
	}
	// subtle.ConstantTimeCompare, never == or bytes.Equal.
	if subtle.ConstantTimeCompare(got, []byte(token)) != 1 {
		Logger.Printf("auth: token mismatch (%d bytes) — dropping connection", len(got))
		return errBadToken
	}
	if _, err := conn.Write([]byte{authAck}); err != nil {
		return err
	}
	_ = conn.SetReadDeadline(time.Time{}) // clear before the long-lived stream
	return nil
}

// readAck reads the server's one-byte accept ack under a handshake deadline and
// then clears BOTH deadlines, so the returned conn carries none.
//
// ONLY A CLEAN EOF IS A REJECTION SIGNAL, and that distinction is load-bearing. A
// rejecting listener returns errBadToken and drops the connection WITHOUT writing
// (see verifyToken above), so the client reads zero bytes and gets io.EOF — and
// because the ack precedes the request, nothing else can have closed it yet.
//
// A TIMEOUT or a RESET is not a verdict, it is the connection dying: the listener
// accepted TCP and TLS and then went away mid-handshake. That is exactly what a
// relay RESTART looks like, and relayEnsure opens that window on every attach — so
// mapping it to ErrAuthRejected hands the most common transient the message
// reserved for the one fault that needs a human ("your token does not match").
// An earlier version wrapped every read error, which moved that misattribution
// rather than removing it.
func readAck(conn net.Conn) error {
	_ = conn.SetReadDeadline(time.Now().Add(handshakeTimeout))
	var ack [1]byte
	if _, err := io.ReadFull(conn, ack[:]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return fmt.Errorf("%w: the listener closed without an accept ack", ErrAuthRejected)
		}
		return fmt.Errorf("svcendpoint: no accept ack from the listener: %w", err)
	}
	if ack[0] != authAck {
		return fmt.Errorf("%w: unexpected accept byte %#02x", ErrAuthRejected, ack[0])
	}
	_ = conn.SetDeadline(time.Time{})
	return nil
}
