package svcendpoint

import (
	"io"
	"net"
)

// ServeFront runs an authenticated loopback-TLS front that SPLICES each
// authenticated connection to a host-only Unix socket, and returns when stop is
// closed (or the listener fails). It publishes to publishPath exactly as Listen
// does, and Close unlinks it.
//
// This is the second of two server shapes, and it exists for ONE reason: a daemon
// whose core is typed on *net.UnixConn and whose teardown semantics are frozen
// cannot simply swap its listener. A tls.Conn has CloseWrite but no CloseRead, and
// re-typing such a core risks a contract that is parity-frozen. Splicing does not
// touch it.
//
// Every daemon that CAN take Listen directly should: its handler is already
// transport-generic, and a splice there would mean two listeners and a host-only
// socket per daemon for no benefit.
//
// The front is deliberately independent of the upstream: it publishes as soon as it
// binds, and a connection that cannot reach upstreamUnixPath is logged and dropped.
func ServeFront(publishPath, advertiseHost, upstreamUnixPath string, stop <-chan struct{}) error {
	return ServeFrontWithOptions(publishPath, advertiseHost, upstreamUnixPath, stop, FrontOptions{})
}

// FrontOptions tunes ServeFrontWithOptions. The zero value is ServeFront's
// behaviour exactly, and every field is named so that the zero value is the
// FRAMEWORK DEFAULT rather than the absence of one — which is why the preamble
// knob below is spelled NoPreamble.
type FrontOptions struct {
	// HalfCloseUpstream makes the front CloseWrite the upstream Unix socket when
	// the client's request direction ends, so a daemon that reads its request TO
	// EOF sees one. It is per-loophole (`request_end: "eof"`), never the
	// default: the relay's core tears down BOTH its sockets on either EOF
	// (frozen parity), so half-closing there would cut short a response still in
	// flight — which is exactly why splice runs the request direction unwaited.
	HalfCloseUpstream bool

	// NoPreamble suppresses the connection preamble (preamble.go,
	// docs/design/broker-as-a-pack.md §5.5) for daemons behind this front.
	//
	// THE ZERO VALUE IS ON — do not invert this field. §5.5's default is
	// `preamble: true`, so that no manifest has to declare anything to keep
	// working, and a knob whose zero value meant "off" would put a silent-off
	// default back in the one place the whole design exists to remove it from.
	//
	// It exists for a genuinely dumb pipe: a daemon yolo did not write, whose
	// protocol has no room for a frame it never asked for. Setting it costs that
	// daemon its identity — no jail_id today, and nothing the preamble grows to
	// carry later — which is meant to be the reason to think twice. It costs
	// yolo NOTHING in audit: tier 1's jail= is derived from the publication path
	// either way (crossing.go), so this is not a privacy switch and cannot be
	// used as one.
	NoPreamble bool
}

// ServeFrontWithOptions is ServeFront with per-daemon knobs; see FrontOptions.
func ServeFrontWithOptions(publishPath, advertiseHost, upstreamUnixPath string, stop <-chan struct{}, opts FrontOptions) error {
	// listenWith, not Listen: the audit label these connections carry
	// (crossing.go) plus the preamble switch. CrossingViaFront is also the marker
	// that NO per-request tier exists for them — splice does not parse the
	// stream, and the preamble is the one thing yolo ADDS to it, never something
	// it reads back.
	ln, err := listenWith(publishPath, advertiseHost, CrossingViaFront, !opts.NoPreamble)
	if err != nil {
		return err
	}
	defer func() { _ = ln.Close() }()
	go func() {
		<-stop
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return nil // listener closed on stop, or the accept loop ended
		}
		go splice(conn, upstreamUnixPath, opts.HalfCloseUpstream)
	}
}

// splice joins one authenticated client connection to the upstream Unix socket.
//
// WAIT ONLY ON THE RESPONSE DIRECTION, AND (BY DEFAULT) NEVER PROPAGATE THE
// CLIENT'S EOF UPSTREAM. This is the single most likely thing in this package to
// get subtly wrong, so: the upstream daemon's own pipe semantics tear down BOTH of
// its sockets on either EOF (frozen parity behaviour), so if this function returned
// on whichever direction ended first — and closed both — it would cut short a
// response still in flight the moment the request direction ended. A framed client
// that writes one request and then waits does exactly that. So the request
// direction runs UNWAITED in its own goroutine, and this function returns only when
// the response direction ends, which happens when upstream closes after its final
// frame — by which point io.Copy has already written every byte to the client.
//
// halfCloseUpstream serves the OPPOSITE daemon shape — one that reads its request
// to EOF: when the request direction ends, the upstream Unix socket is
// CloseWrite'd (the dial below is net.Dial("unix", …), so the assertion holds),
// while the response direction stays open for the reply.
//
// THE CONNECTION PREAMBLE NEEDS NOTHING FROM THIS FUNCTION, deliberately. It is
// prefixed onto the client's READ stream inside countingConn (crossing.go), so the
// request-direction io.Copy below carries it upstream as its first write —
// which is also why the copy is started UNCONDITIONALLY and the upstream is
// dialled EAGERLY: a fronted daemon receives the preamble at connection open,
// without waiting for a byte from the jail. Neither io.Copy can shortcut past
// countingConn.Read either: *net.UnixConn implements neither io.ReaderFrom nor
// io.WriterTo (its ReadFrom/WriteTo are the PacketConn signatures), and
// countingConn embeds the net.Conn INTERFACE so no WriteTo is promoted from the
// *tls.Conn. Both copies therefore run the generic buffer loop.
func splice(client net.Conn, upstreamUnixPath string, halfCloseUpstream bool) {
	defer func() { _ = client.Close() }()
	up, err := net.Dial("unix", upstreamUnixPath)
	if err != nil {
		Logger.Printf("front: dial upstream %s failed: %v", upstreamUnixPath, err)
		// AUDIT ONLY, and additive by construction: this changes what the tier-1
		// record SAYS about a connection that was already being dropped, and
		// nothing about when or how it is dropped. The deferred Close above still
		// runs and still emits the record — with CrossingUnreachable instead of
		// CrossingAccepted, because "authenticated, then the daemon was gone" is
		// its own fact.
		if cc, ok := client.(*countingConn); ok {
			cc.mark(CrossingUnreachable, CrossingReasonUpstreamDial)
		}
		return
	}
	defer func() { _ = up.Close() }()
	go func() { // request direction, UNWAITED
		_, _ = io.Copy(up, client)
		if halfCloseUpstream {
			if uc, ok := up.(*net.UnixConn); ok {
				_ = uc.CloseWrite()
			}
		}
	}()
	_, _ = io.Copy(client, up) // wait ONLY on the response
}
