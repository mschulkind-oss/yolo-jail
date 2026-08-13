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
	ln, err := Listen(publishPath, advertiseHost)
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
		go splice(conn, upstreamUnixPath)
	}
}

// splice joins one authenticated client connection to the upstream Unix socket.
//
// WAIT ONLY ON THE RESPONSE DIRECTION, AND NEVER PROPAGATE THE CLIENT'S EOF
// UPSTREAM. This is the single most likely thing in this package to get subtly
// wrong, so: the upstream daemon's own pipe semantics tear down BOTH of its sockets
// on either EOF (frozen parity behaviour), so if this function returned on
// whichever direction ended first — and closed both — it would cut short a response
// still in flight the moment the request direction ended. A framed client that
// writes one request and then waits does exactly that. So the request direction
// runs UNWAITED in its own goroutine, and this function returns only when the
// response direction ends, which happens when upstream closes after its final
// frame — by which point io.Copy has already written every byte to the client.
func splice(client net.Conn, upstreamUnixPath string) {
	defer func() { _ = client.Close() }()
	up, err := net.Dial("unix", upstreamUnixPath)
	if err != nil {
		Logger.Printf("front: dial upstream %s failed: %v", upstreamUnixPath, err)
		return
	}
	defer func() { _ = up.Close() }()
	go func() { _, _ = io.Copy(up, client) }() // request direction, UNWAITED
	_, _ = io.Copy(client, up)                 // wait ONLY on the response
}
