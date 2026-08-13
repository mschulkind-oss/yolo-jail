package hostservice

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// pinnedTLSClient dials the published listener over TLS pinning its exact
// certificate — everything a legitimate client does EXCEPT present a valid token.
func pinnedTLSClient(t *testing.T, ep svcendpoint.Endpoint) net.Conn {
	t.Helper()
	cert, err := x509.ParseCertificate(ep.CertDER)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	_, port, err := net.SplitHostPort(ep.HostPort)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp",
		net.JoinHostPort("127.0.0.1", port),
		&tls.Config{RootCAs: pool, ServerName: svcendpoint.ServerName, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

// writeTokenFrameRaw writes the wire form of the leading token frame: 4-byte
// big-endian length, then the bytes.
func writeTokenFrameRaw(t *testing.T, w net.Conn, token string) {
	t.Helper()
	frame := make([]byte, 4+len(token))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(token)))
	copy(frame[4:], token)
	if _, err := w.Write(frame); err != nil {
		t.Fatal(err)
	}
}

// TestServeRejectsUnauthenticatedBeforeHandler: a connection presenting the wrong
// token must never reach handleOne.
//
// MEASURED, NOT INFERRED — the handler increments a counter, and the assertion is on
// that counter. Inferring from "no response arrived" would pass even if the handler
// ran and its reply were merely lost, which is the whole failure mode that matters:
// a daemon that acts on an unauthenticated request has already leaked whatever the
// loophole grants, regardless of what the caller gets back.
//
// It also pins the access log: an unauthenticated connection leaves NO access-log
// line, because there was no session.
func TestServeRejectsUnauthenticatedBeforeHandler(t *testing.T) {
	advertiseLoopback(t)
	dir, err := os.MkdirTemp("/tmp", "yj-auth-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	ep := filepath.Join(dir, "auth.endpoint")

	var calls atomic.Int64
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = Serve(func(s *Session) {
			calls.Add(1)
			s.Stdout("reached the handler\n")
		}, ep, stop)
		close(done)
	}()
	waitForEndpoint(t, ep)
	defer func() { close(stop); <-done }()

	published, err := svcendpoint.Read(ep)
	if err != nil {
		t.Fatal(err)
	}

	// A HAND-ROLLED client, not svcendpoint.Dial, and this matters. The adversary
	// holds the address and the public certificate (both are in the file, and the
	// cert is public by design) but not the token — and does NOT politely stop when
	// no ack arrives. It sends a perfectly valid request frame anyway. Driving this
	// through Dial instead would stop at the missing ack and prove only that the
	// client gives up, never that the SERVER refuses to act.
	wrong := "00000000000000000000000000000000000000000000000000000000deadbeef"
	if len(wrong) != len(published.Token) {
		t.Fatalf("test bug: wrong token is %d chars, published is %d", len(wrong), len(published.Token))
	}
	if wrong == published.Token {
		t.Fatal("test bug: the wrong token is the real one")
	}
	// Case 1: a wrong token, then a valid request anyway.
	conn := pinnedTLSClient(t, published)
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	writeTokenFrameRaw(t, conn, wrong)
	_ = frameproto.WriteRequest(conn, []byte(`{"jail_id":"attacker","mode":"list"}`))
	buf := make([]byte, 1)
	if n, rerr := conn.Read(buf); rerr == nil && n > 0 {
		t.Errorf("wrong-token client got a response byte %#02x; the server must send nothing", buf[0])
	}
	_ = conn.Close()

	// Case 2: NO TOKEN FRAME AT ALL — straight to a valid request. This is the case
	// the handler counter actually catches, and it is why the counter is here.
	// Case 1 is shielded by a framing coincidence: a server that stopped
	// authenticating would consume the 64-byte token frame AS a request, fail to
	// decode it as JSON, and skip the handler anyway — so case 1 alone would let a
	// missing token check survive. Case 2 removes the coincidence: the first frame on
	// the wire IS a well-formed request, so an unauthenticating server runs the
	// handler and the counter goes to 1.
	bare := pinnedTLSClient(t, published)
	_ = bare.SetDeadline(time.Now().Add(5 * time.Second))
	_ = frameproto.WriteRequest(bare, []byte(`{"jail_id":"attacker","mode":"list"}`))
	if n, rerr := bare.Read(buf); rerr == nil && n > 0 {
		t.Errorf("token-less client got a response byte %#02x; the server must send nothing", buf[0])
	}
	_ = bare.Close()

	// Give a handler that should never run every chance to run.
	time.Sleep(200 * time.Millisecond)
	if n := calls.Load(); n != 0 {
		t.Errorf("handler invoked %d times on an unauthenticated connection, want 0", n)
	}

	// And the same through the real client, which must attribute it as auth failure
	// rather than as "the thing behind the transport broke".
	tampered := published
	tampered.Token = wrong
	badEP := filepath.Join(dir, "tampered.endpoint")
	if err := svcendpoint.Publish(badEP, tampered); err != nil {
		t.Fatal(err)
	}
	bad, err := svcendpoint.DialLocal(badEP, 5*time.Second)
	if err == nil {
		bad.Close()
		t.Fatal("dial with a wrong token succeeded; want rejection")
	}
	if !errors.Is(err, svcendpoint.ErrAuthRejected) {
		t.Errorf("wrong-token dial error = %v, want ErrAuthRejected", err)
	}

	// The listener must still serve a correctly authenticated client afterwards: a
	// rejected connection is dropped, not fatal to the accept loop.
	good := dialEndpoint(t, ep)
	defer good.Close()
	good.SetDeadline(time.Now().Add(5 * time.Second))
	if err := frameproto.WriteRequest(good, []byte(`{"jail_id":"j"}`)); err != nil {
		t.Fatal(err)
	}
	f, err := frameproto.ReadFrame(good)
	if err != nil {
		t.Fatal(err)
	}
	if f.StreamID != frameproto.StreamStdout || string(f.Payload) != "reached the handler\n" {
		t.Errorf("after a rejection, good client got stream %d %q", f.StreamID, f.Payload)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("handler calls = %d after one authenticated request, want 1", n)
	}
}

// TestServeRejectsPlaintextBeforeHandler: raw TCP to the bound port — no TLS at all,
// just a token frame — must not reach the handler either. This is the sibling-jail
// port-scan case: reachability is not authorization.
func TestServeRejectsPlaintextBeforeHandler(t *testing.T) {
	advertiseLoopback(t)
	dir, err := os.MkdirTemp("/tmp", "yj-plain-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	ep := filepath.Join(dir, "plain.endpoint")

	var calls atomic.Int64
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = Serve(func(s *Session) { calls.Add(1) }, ep, stop)
		close(done)
	}()
	waitForEndpoint(t, ep)
	defer func() { close(stop); <-done }()

	published, err := svcendpoint.Read(ep)
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(published.HostPort)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", port), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// The REAL token, over plaintext, followed by a valid request. Even holding the
	// credential, a client that skips TLS gets nowhere — otherwise the pin would be
	// decorative.
	_ = raw.SetDeadline(time.Now().Add(2 * time.Second))
	writeTokenFrameRaw(t, raw, published.Token)
	_ = frameproto.WriteRequest(raw, []byte(`{"jail_id":"attacker","mode":"list"}`))
	buf := make([]byte, 1)
	if n, err := raw.Read(buf); err == nil && n > 0 {
		t.Errorf("plaintext client got a response byte %#02x; want no ack", buf[0])
	}
	_ = raw.Close()

	time.Sleep(200 * time.Millisecond)
	if n := calls.Load(); n != 0 {
		t.Errorf("handler invoked %d times over plaintext, want 0", n)
	}
}
