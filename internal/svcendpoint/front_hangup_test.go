package svcendpoint

// A client that SENT a request and then hung up must release the daemon behind
// the front.
//
// The regression pin for a defect a macOS CI triage found on 2026-10-09: the
// serial bridge's `pty` and `monitor` modes stream a device until their client
// goes away, and they learn that only from EOF on their own socket. The front
// signalled EOF upstream only when the client wrote NOTHING (the probe case,
// front_probe_test.go) or under request_end "eof", so after a jail's Ctrl+C on a
// quiet device nothing ever closed the upstream socket: the bridge kept the
// host device open, and the splice kept two goroutines and two fds, until the
// device next printed a byte.

import (
	"crypto/tls"
	"net"
	"path/filepath"
	"testing"
	"time"
)

// startStreamingFront stands a daemon with the serial monitor's shape: it
// consumes the preamble and ONE request frame, then reads the connection until
// it ends (the inbound pump) and never writes (a device that prints nothing).
// released receives once that read ends — the moment a real bridge closes its
// device.
func startStreamingFront(t *testing.T) (endpoint string, released <-chan error) {
	t.Helper()
	dir := privateSocketDir(t)
	upstream := filepath.Join(dir, "stream.sock")
	assertSockPathFits(t, upstream)
	endpoint = filepath.Join(dir, "stream.endpoint")

	ln, err := net.Listen("unix", upstream)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	ch := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			ch <- err
			return
		}
		defer func() { _ = conn.Close() }()
		if _, err := readRawPreamble(conn); err != nil {
			ch <- err
			return
		}
		if _, err := readRawPreamble(conn); err != nil { // the request
			ch <- err
			return
		}
		buf := make([]byte, 64)
		for {
			if _, err := conn.Read(buf); err != nil {
				ch <- err
				return
			}
		}
	}()

	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() { _ = ServeFront(endpoint, "127.0.0.1", upstream, stop) }()
	waitProbe(t, endpoint)
	return endpoint, ch
}

func TestClientHangupReleasesTheUpstream(t *testing.T) {
	for _, tc := range []struct {
		name   string
		hangUp func(net.Conn) error
	}{
		// yolo-serial's own Ctrl+C path: tls.Conn.Close sends close_notify, then
		// closes the socket.
		{"graceful close", func(c net.Conn) error { return c.Close() }},
		// A client killed outright: the socket closes with no close_notify.
		{"transport dropped", func(c net.Conn) error { return c.(*tls.Conn).NetConn().Close() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			endpoint, released := startStreamingFront(t)

			conn, err := DialLocal(endpoint, 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := conn.Write(frame(`{"mode":"monitor","device":"/dev/ttyUSB0"}`)); err != nil {
				t.Fatal(err)
			}
			if err := tc.hangUp(conn); err != nil {
				t.Fatal(err)
			}

			select {
			case <-released:
			case <-time.After(3 * time.Second):
				t.Fatal("the client hung up and the daemon behind the front never saw its " +
					"connection end: the front kept the upstream socket open, so a serial " +
					"bridge would hold the host device until the device next printed a byte")
			}
		})
	}
}
