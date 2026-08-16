package svcendpoint

import (
	"crypto/tls"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// eofSocketDir returns a per-test directory short enough to hold an AF_UNIX
// socket. NOT t.TempDir(): that is rooted at TMPDIR, which on macOS overruns
// darwin's 104-byte sun_path — the daemon then fails to bind and the test
// reports a timeout that reads as a broken front rather than a too-long path.
func eofSocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "yj-fe-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	// Already /tmp-rooted, so this passes today. Asserted anyway: the guard is what turns a
	// future overrun into a message naming the limit and the fix, instead of a bare
	// "bind: invalid argument" from net.Listen forty lines below.
	assertSockPathFits(t, filepath.Join(dir, "up.sock"))
	return dir
}

// TestFrontEOFModeHalfClosesUpstream: a daemon that reads its request TO EOF
// works on a bare socket and hangs forever behind the default front, because
// splice never propagates the client's EOF upstream (relay-parity, frozen).
// FrontOptions.HalfCloseUpstream serves exactly that daemon shape: when the
// client's request direction ends, the front CloseWrites the upstream Unix
// socket, the daemon's read returns, and its response still flows back
// (loophole-packaging.md §2.1b hazard 2).
func TestFrontEOFModeHalfClosesUpstream(t *testing.T) {
	dir := eofSocketDir(t)
	upstream := filepath.Join(dir, "up.sock")
	endpoint := filepath.Join(dir, "svc.endpoint")

	// The upstream daemon: reads the whole request TO EOF, then responds.
	ln, err := net.Listen("unix", upstream)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	daemonErr := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			daemonErr <- err
			return
		}
		defer conn.Close()
		// The connection preamble first, as every daemon behind this transport
		// now must. It matters MORE for a read-to-EOF daemon than for a framed
		// one: io.ReadAll below would otherwise fold yolo's frame into the
		// request body and the echo would come back with a JSON object glued to
		// the front of it.
		if _, err := ReadPreamble(conn); err != nil {
			daemonErr <- err
			return
		}
		// BOUNDED: this read returns only when the front half-closes upstream, so a
		// regression here used to block both ends until go test's package-wide
		// 10-minute timeout panic — a failure mode that names no cause and truncates
		// the diagnosis. With a deadline the same regression fails in ~2s, here, with
		// the half-close named.
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		req, err := io.ReadAll(conn)
		if err != nil {
			daemonErr <- err
			return
		}
		_, err = conn.Write(append([]byte("got:"), req...))
		daemonErr <- err
	}()

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		_ = ServeFrontWithOptions(endpoint, "127.0.0.1", upstream, stop,
			FrontOptions{HalfCloseUpstream: true})
	}()
	waitProbe(t, endpoint)

	conn, err := Dial(endpoint, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	// End the request direction. The client is a *tls.Conn, whose CloseWrite
	// sends close_notify — the front's request-side copy then sees EOF.
	if err := conn.(*tls.Conn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	// Bounded for the same reason as the daemon's read above.
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	resp, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("reading the response timed out or failed — the front did not "+
			"half-close the upstream socket, so the daemon's read-to-EOF never "+
			"returned: %v", err)
	}
	if got := string(resp); got != "got:ping" {
		t.Errorf("response = %q, want %q", got, "got:ping")
	}
	if err := <-daemonErr; err != nil {
		t.Errorf("upstream daemon never saw its request end (missing upstream "+
			"half-close): %v", err)
	}
}

// TestServeFrontDefaultHasNoHalfClose pins that the plain ServeFront keeps the
// relay's frozen semantics: the zero FrontOptions means NO upstream half-close,
// and ServeFront is exactly ServeFrontWithOptions with the zero value. The
// relay's teardown parity depends on the client's EOF never reaching it.
func TestServeFrontDefaultHasNoHalfClose(t *testing.T) {
	dir := eofSocketDir(t)
	upstream := filepath.Join(dir, "up.sock")
	endpoint := filepath.Join(dir, "svc.endpoint")

	// The upstream daemon: responds after ONE read, then verifies its next read
	// BLOCKS past the client's CloseWrite (no EOF propagated) until the front
	// tears the splice down on the response direction's end.
	ln, err := net.Listen("unix", upstream)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	sawEOF := make(chan bool, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if _, err := ReadPreamble(conn); err != nil {
			return
		}
		buf := make([]byte, 64)
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		if _, err := conn.Write(append([]byte("got:"), buf[:n]...)); err != nil {
			return
		}
		// With no half-close, this read must NOT return EOF promptly after the
		// client's CloseWrite; it returns only when the front closes both sides.
		_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		_, err = conn.Read(buf)
		sawEOF <- err == io.EOF
	}()

	stop := make(chan struct{})
	defer close(stop)
	go func() { _ = ServeFront(endpoint, "127.0.0.1", upstream, stop) }()
	waitProbe(t, endpoint)

	conn, err := Dial(endpoint, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	if err := conn.(*tls.Conn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil || string(buf[:n]) != "got:ping" {
		t.Fatalf("response = %q, %v", buf[:n], err)
	}
	if <-sawEOF {
		t.Error("the default front propagated the client's EOF upstream; " +
			"that breaks the relay's frozen teardown parity")
	}
	_ = conn.Close()
}

// waitProbe polls until the front has published a complete endpoint file.
func waitProbe(t *testing.T, endpoint string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if Probe(endpoint) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("front never published a complete endpoint at %s", endpoint)
}
