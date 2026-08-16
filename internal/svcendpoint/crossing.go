package svcendpoint

import (
	"net"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// This file is TIER 1 of the boundary audit: one record per jail↔host CONNECTION,
// emitted by the transport, uniform across every daemon that rides it.
//
// # Tier 1 cannot be made to cover tier 2, and this is the honest ceiling
//
// TIER 2 is internal/hostservice's per-REQUEST access line
// (`jail=… keys=… rc=… elapsed_ms=… bytes_out=…`, docs/design/loophole-protocol.md
// §Access logging). It exists only for daemons that speak yolo's FRAMED protocol
// through that helper, because only there is there a parsed request to describe.
//
// There is NO generic per-request equivalent here and there cannot be one. A
// fronted daemon (`publishes: "socket"`) is served by ServeFront, whose splice
// JOINS A BYTE STREAM IT DOES NOT PARSE — and nothing constrains a loophole's
// protocol to be request-shaped at all: it may be framed, a raw stream, audio,
// video. An earlier design draft promised the front as "the natural home for the
// crossing audit log" in the per-request sense; that claim is WITHDRAWN and this
// file must not be read as reinstating it. What the front can honestly report is
// exactly what Crossing carries: which service, which jail, when, how long, how
// many bytes each way, and whether the connection authenticated.
//
// So the two tiers are not a fallback and a better version of one thing. They are
// two different observations, and a framed daemon produces BOTH: one connection
// record from here, and one request line per request from hostservice.
//
// # AUDIT-ONLY (and this is load-bearing, not a slogan)
//
// Nothing in this file may block, fail, or delay a crossing. recordCrossing
// tolerates a nil sink, RECOVERS a panicking one (an unrecovered panic in a
// connection goroutine takes down the whole daemon), and disables it after one
// warning. Records are emitted when a connection CLOSES — after its last byte —
// so the sink is never on the data path's critical section.
//
// # A LENGTH, NEVER A VALUE
//
// Every field below is a count or a name yolo itself chose. No payload byte, no
// token, no certificate, no endpoint line, ever — the same rule the package
// Logger is held to (see its doc comment), for the same reason: these records
// land in a file under ~/.local/share/yolo-jail/logs/ and in any transcript that
// quotes it, and CI's secret scan runs --only-verified, so a leak here would not
// be caught.
//
// That rule covers the CONNECTION PREAMBLE too (preamble.go), which this file's
// countingConn is the delivery point for: NO Crossing field ever carries preamble
// content, and none ever will. The preamble is a versioned envelope meant to
// grow, so a record that quoted it today would be quoting whatever it carries
// tomorrow. Jail is derived from the publication path by crossingIdentity — the
// SAME derivation the preamble's jail_id is built from — so tier 1 already says
// everything the preamble could tell it, without reading a byte of the stream.

// Crossing outcomes. A fixed vocabulary, because an audit log people grep is a
// schema whether or not anyone writes it down.
const (
	// CrossingAccepted: the connection authenticated and was served.
	CrossingAccepted = "accepted"
	// CrossingRejected: the connection did not authenticate and was dropped
	// before any daemon saw it.
	CrossingRejected = "rejected"
	// CrossingUnreachable: the connection AUTHENTICATED but the front could not
	// reach the daemon behind it. Its own outcome on purpose — "the jail got
	// through the boundary and the daemon was gone" is a distinct fact from both
	// of the above, and an audit reader must not have to infer it from a zero
	// byte count.
	CrossingUnreachable = "unreachable"
)

// Reasons. "" means there is nothing to add beyond the outcome.
const (
	// CrossingReasonTokenMismatch: a well-formed token frame carrying the wrong
	// token — the one fault that usually needs a human.
	CrossingReasonTokenMismatch = "token-mismatch"
	// CrossingReasonBadTokenFrame: a zero-length or oversized length prefix, i.e.
	// not a token frame at all.
	CrossingReasonBadTokenFrame = "bad-token-frame"
	// CrossingReasonHandshake: the connection died during the pre-auth exchange —
	// a TLS failure, a plaintext dial, a scanner that connected and left. NOT a
	// verdict about a credential, and kept separate for exactly that reason (see
	// readAck's note on the same distinction client-side).
	CrossingReasonHandshake = "handshake-incomplete"
	// CrossingReasonUpstreamDial: the front could not dial the daemon's socket.
	CrossingReasonUpstreamDial = "upstream-dial-failed"
)

// How the connection was served. Not a transport — there is one transport — but
// which server shape sat behind it, which is what decides whether tier 2 exists
// for this crossing at all.
const (
	// CrossingViaFront: spliced to a daemon's AF_UNIX socket by ServeFront. NO
	// per-request tier exists for these.
	CrossingViaFront = "front"
	// CrossingViaEndpoint: handed to a caller's own accept loop by Listen —
	// hostservice.ServeEndpoint in practice, which adds the per-request tier.
	CrossingViaEndpoint = "endpoint"
)

// Crossing is ONE jail↔host boundary crossing, described at connection level.
//
// Everything here is a count or a host-chosen name. See the file comment: a
// length, never a value.
type Crossing struct {
	// Service is the host service / loophole name, derived from the endpoint file
	// YOLO published — never self-reported by the client. Same property that
	// makes the relay's host-side jail_id stamp trustworthy.
	Service string
	// Jail is the per-jail publication directory's name (in production
	// "yolo-host-services-<8hex>"), likewise derived host-side.
	Jail string
	// Via is CrossingViaFront or CrossingViaEndpoint.
	Via string
	// Outcome is one of the Crossing* outcome constants.
	Outcome string
	// Reason is one of the CrossingReason* constants, or "" when the outcome says
	// everything.
	Reason string
	// At is when the connection was accepted.
	At time.Time
	// Duration is accept→close for an accepted crossing, and the length of the
	// failed pre-auth exchange for a rejected one.
	Duration time.Duration
	// BytesIn is how many PLAINTEXT bytes the jail sent host-ward on this
	// connection; BytesOut is how many went the other way. Counted at the TLS
	// boundary, so these are payload volume rather than wire volume — the number
	// an auditor means by "how much crossed". COUNTS ONLY: no content is
	// retained anywhere. Zero for a rejected crossing, whose only traffic was the
	// pre-auth handshake.
	BytesIn  int64
	BytesOut int64
}

// crossingSinkFn receives one Crossing per boundary crossing. It must not block
// for long: it runs on the connection's own goroutine, after the connection's
// last byte.
type crossingSinkFn func(Crossing)

var (
	crossingMu sync.RWMutex
	crossing   crossingSinkFn
)

// SetCrossingSink installs the process-wide connection-level audit sink, or
// removes it with nil. A host process installs one at startup
// (internal/crossaudit); everything else — every test binary, cmd/yolo-ps, any
// third-party importer — leaves it nil and this package behaves exactly as it did
// before the audit existed.
//
// Guarded by a mutex rather than being a bare package var like Logger: unlike
// Logger, this is read from every connection goroutine while a test's cleanup
// restores it, which is a data race a bare var cannot avoid.
func SetCrossingSink(sink func(Crossing)) {
	crossingMu.Lock()
	defer crossingMu.Unlock()
	crossing = sink
}

// CrossingSink returns the installed sink, or nil. For tests and diagnostics —
// use recordCrossing to emit, never this.
func CrossingSink() func(Crossing) {
	crossingMu.RLock()
	defer crossingMu.RUnlock()
	return crossing
}

// recordCrossing hands one record to the sink, and CANNOT FAIL A CROSSING.
//
// Two defenses, both deliberate:
//
//   - a nil sink is the default and is a no-op, so the audit is opt-in and its
//     absence is not an error path;
//   - a PANICKING sink is recovered, warned about ONCE, and then uninstalled. Go
//     kills the process on an unrecovered panic in any goroutine, so without this
//     a bug in an audit sink would take down a daemon mid-crossing — the exact
//     failure mode an audit-only feature is forbidden from introducing.
func recordCrossing(c Crossing) {
	sink := CrossingSink()
	if sink == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			Logger.Printf("crossing audit: sink panicked (%v) — audit disabled; "+
				"crossings continue unaffected", r)
			SetCrossingSink(nil)
		}
	}()
	sink(c)
}

// crossingIdentity derives the service and jail names from the path the listener
// PUBLISHES at: <per-jail dir>/<service>.endpoint.
//
// Host-side attribution by construction. The alternative — reading a name out of
// the request — is both unavailable at the front (nothing parses the stream) and
// unsound anywhere (the jail would be naming itself in its own audit log).
func crossingIdentity(publishPath string) (service, jail string) {
	base := filepath.Base(publishPath)
	service = strings.TrimSuffix(base, filepath.Ext(base))
	jail = filepath.Base(filepath.Dir(publishPath))
	return crossingName(service), crossingName(jail)
}

// crossingName normalizes filepath.Base's answers for degenerate paths ("", ".",
// "/") to one spelling, so an audit reader never has to wonder whether "." is a
// directory name or a missing value.
func crossingName(s string) string {
	switch s {
	case "", ".", string(filepath.Separator):
		return "unknown"
	}
	return s
}

// countingConn is an authenticated connection that counts its own traffic and
// emits its Crossing when it closes.
//
// It wraps EVERY accepted connection, sink or no sink, so there is exactly one
// connection shape in production and in test — a conditional wrapper would mean
// the audited path and the unaudited path were different code, which is how a
// "cannot break the data path" claim quietly stops being true.
//
// Embedding net.Conn rather than re-implementing it keeps every deadline and
// address method intact.
//
// IT IS ALSO THE ONLY PLACE THE CONNECTION PREAMBLE CAN LIVE, and the reason is
// front.go:92: splice type-asserts the accepted connection back to *countingConn
// to downgrade an undeliverable crossing to CrossingUnreachable. Wrapping this
// type in an OUTER prefixing reader compiles, passes every test, and silently
// deletes that audit outcome, because the comma-ok assertion just stops matching.
// So the prefix goes INSIDE, on Read. (The claim that used to stand here — "the
// only server-side type assertion in this package is on the front's UPSTREAM Unix
// socket" — was already false when it was written; front.go asserts on the
// CLIENT too. Do not restore it.)
type countingConn struct {
	net.Conn
	service, jail, via string
	start              time.Time

	// pre is the connection preamble still owed to whoever reads this connection
	// (preamble.go, docs/design/broker-as-a-pack.md §5.5), or nil for a listener
	// that does not send one. It is a window onto the Listener's ONE shared,
	// never-mutated frame: only this slice header moves, so a preamble costs a
	// reslice per connection rather than a marshal.
	//
	// NOT an atomic and NOT guarded, unlike in/out below, and the asymmetry is
	// deliberate: in/out are written from two goroutines because splice reads and
	// writes concurrently, but Read itself runs on exactly ONE goroutine in both
	// server shapes — splice's request-direction copy, or the daemon's own
	// per-connection handler. A mutex here would suggest a race that cannot
	// happen and would sit on the data path forever.
	pre []byte

	// atomics: splice reads and writes on two different goroutines.
	in, out atomic.Int64

	mu              sync.Mutex
	outcome, reason string

	closeOnce sync.Once
}

func newCountingConn(conn net.Conn, service, jail, via string, start time.Time, pre []byte) *countingConn {
	return &countingConn{
		Conn: conn, service: service, jail: jail, via: via,
		start: start, pre: pre, outcome: CrossingAccepted,
	}
}

func (c *countingConn) Read(p []byte) (int, error) {
	// THE PREAMBLE IS YOLO'S OWN BYTES, SO IT RETURNS BEFORE THE COUNTER.
	//
	// BytesIn is defined as "how many PLAINTEXT bytes the JAIL sent host-ward"
	// and surfaces verbatim as bytes_in= in internal/crossaudit. Serving the
	// prefix through the counter — which is exactly what an io.MultiReader-shaped
	// implementation would do — inflates every tier-1 record by the preamble's
	// length, silently, on the one field an auditor reads as volume. So this
	// branch returns before c.in.Add and before c.Conn.Read.
	//
	// A PREFIX IS NOT A CONCATENATION: one Read here yields ONLY preamble bytes,
	// never the first of the client's. A daemon that treats one Read as one
	// message must read the preamble with ReadPreamble (io.ReadFull-based) first.
	if len(c.pre) > 0 {
		n := copy(p, c.pre)
		c.pre = c.pre[n:]
		return n, nil
	}
	n, err := c.Conn.Read(p)
	c.in.Add(int64(n))
	return n, err
}

func (c *countingConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.out.Add(int64(n))
	return n, err
}

// mark downgrades an accepted crossing's outcome before it is recorded — the
// front's undeliverable-connection case. Additive: it changes what the record
// SAYS, never what the connection DOES.
func (c *countingConn) mark(outcome, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.outcome, c.reason = outcome, reason
}

// Close closes the connection FIRST and records afterwards, exactly once. The
// record is therefore never on the path of a byte, and a caller that closes twice
// does not double-count.
func (c *countingConn) Close() error {
	err := c.Conn.Close()
	c.closeOnce.Do(func() {
		c.mu.Lock()
		outcome, reason := c.outcome, c.reason
		c.mu.Unlock()
		recordCrossing(Crossing{
			Service:  c.service,
			Jail:     c.jail,
			Via:      c.via,
			Outcome:  outcome,
			Reason:   reason,
			At:       c.start,
			Duration: time.Since(c.start),
			BytesIn:  c.in.Load(),
			BytesOut: c.out.Load(),
		})
	})
	return err
}

// crossingRejectReason maps an authentication failure to the fixed vocabulary.
// A read error is NOT a credential verdict — see CrossingReasonHandshake.
func crossingRejectReason(err error) string {
	switch err {
	case errBadToken:
		return CrossingReasonTokenMismatch
	case errBadTokenFrame:
		return CrossingReasonBadTokenFrame
	}
	return CrossingReasonHandshake
}
