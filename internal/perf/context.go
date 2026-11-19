package perf

// context.go is a trace's identity: the ids, and the one string form that carries "the span I am a
// child of" from one process to another. The design is docs/design/startup-tracing.md, §5.1 and
// §5.2 (TR-D2, TR-D3).
//
// The formats are the W3C trace-context ones (https://www.w3.org/TR/trace-context/#traceparent-header):
// a 16-byte trace id and an 8-byte span id, both random and written as lowercase hex, and the pair
// carried as `00-<32 hex trace id>-<16 hex span id>-01`. They are OpenTelemetry's own, so an export
// never translates an id.

import (
	"crypto/rand"
	"encoding/hex"
)

// Which variable carries a Context into a child's environment is the host wiring's to name, beside
// its entry in the launcher's list of known launch variables (internal/cli/run/unknownlaunchenv.go).
// ⚠ Whatever reads it, a LAUNCHER never adopts a context from its own environment (TR-D3): a
// launcher mints its trace (MintTrace), and the keeper adopts one from its plan.

// TraceID names one trace: every span one launch caused, in whichever process it ran. The zero
// value is "no trace".
type TraceID [16]byte

// SpanID names one span within a trace. The zero value is "no span", which is also how a line
// says it was given no parent.
type SpanID [8]byte

// String is the id as 32 lowercase hex digits.
func (id TraceID) String() string { return hex.EncodeToString(id[:]) }

// IsZero reports whether id is the zero ("no trace") value.
func (id TraceID) IsZero() bool { return id == TraceID{} }

// String is the id as 16 lowercase hex digits.
func (id SpanID) String() string { return hex.EncodeToString(id[:]) }

// IsZero reports whether id is the zero ("no span") value.
func (id SpanID) IsZero() bool { return id == SpanID{} }

// newTraceID returns a random, non-zero trace id. crypto/rand.Read does not fail on any
// platform yolo ships for (it panics instead, which is not a path a timing logger guards).
func newTraceID() TraceID {
	var id TraceID
	for id.IsZero() {
		_, _ = rand.Read(id[:])
	}
	return id
}

// newSpanID returns a random, non-zero span id.
func newSpanID() SpanID {
	var id SpanID
	for id.IsZero() {
		_, _ = rand.Read(id[:])
	}
	return id
}

// Trace is the identity one process records under: the trace it belongs to and, when another
// process handed it the trace, the span there that this process is a child of. A process's first
// line in the trace (its lane line) carries both.
type Trace struct {
	ID TraceID
	// Parent is the span in another process this lane is a child of: zero for the process that
	// minted the trace.
	Parent SpanID
}

// MintTrace returns a fresh trace with no parent: what a launcher does when it constructs its
// collector (§5.1), and what the scheduler does per act (§5.5).
func MintTrace() Trace { return Trace{ID: newTraceID()} }

// Context is the reference one process hands another: "the span I am a child of", in the trace
// both belong to.
type Context struct {
	Trace TraceID
	Span  SpanID
}

// Valid reports whether c names both a trace and a span. Only a valid Context has a string form.
func (c Context) Valid() bool { return !c.Trace.IsZero() && !c.Span.IsZero() }

// String is the W3C traceparent form, `00-<trace id>-<span id>-01`, or "" for an invalid Context.
func (c Context) String() string {
	if !c.Valid() {
		return ""
	}
	return "00-" + c.Trace.String() + "-" + c.Span.String() + "-01"
}

// Join is the Trace a child process records under when it was handed c: c's trace, with c's span
// as its lane's parent.
func (c Context) Join() Trace { return Trace{ID: c.Trace, Parent: c.Span} }

// traceparentLen is len("00-") + 32 + len("-") + 16 + len("-") + 2.
const traceparentLen = 55

// ParseContext reads a traceparent string. Anything but exactly the form String writes, give or
// take the flags byte, is "no context" (false), never a panic and never a partial value: the
// wrong length, a version other than 00, upper-case or other non-hex digits, misplaced dashes, or
// an all-zero trace or span id. The input may come from a jail, so nothing about it is trusted.
func ParseContext(s string) (Context, bool) {
	if len(s) != traceparentLen || s[2] != '-' || s[35] != '-' || s[52] != '-' {
		return Context{}, false
	}
	if s[:2] != "00" || !lowerHex(s[53:55]) {
		return Context{}, false
	}
	var c Context
	if !decodeLowerHex(c.Trace[:], s[3:35]) || !decodeLowerHex(c.Span[:], s[36:52]) || !c.Valid() {
		return Context{}, false
	}
	return c, true
}

func lowerHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func decodeLowerHex(dst []byte, s string) bool {
	if len(s) != 2*len(dst) || !lowerHex(s) {
		return false
	}
	_, err := hex.Decode(dst, []byte(s))
	return err == nil
}
