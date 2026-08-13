package svcendpoint

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
)

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

// privateDir returns a 0700 temp dir.
//
// MEASURED, not assumed: t.TempDir()'s numbered subdirectory is created with mode
// 0777&^umask — 0755 under the default umask — which ensurePrivateDir rightly
// refuses to publish a credential into. Every test that publishes needs this, and
// SO WILL THE RUN PIPELINE: the three MkdirAll(…, 0o755) sites that create the
// per-jail host-services dir have to become 0700 or Listen will fail closed there
// exactly as it does here.
func privateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// testServer is a frameproto daemon behind a real Listener. calls counts HANDLER
// INVOCATIONS, so every negative test can assert the handler was never reached as
// a MEASURED fact rather than an inference from a client-side error.
type testServer struct {
	ln    *Listener
	path  string
	calls atomic.Int64
	delay time.Duration
}

// startServer binds a Listener advertising 127.0.0.1 (so tests can dial the
// published address) and serves an echo handler until cleanup.
func startServer(t *testing.T) *testServer {
	t.Helper()
	return startServerAt(t, filepath.Join(privateDir(t), "svc.endpoint"))
}

func startServerAt(t *testing.T, publishPath string) *testServer {
	t.Helper()
	ln, err := Listen(publishPath, "127.0.0.1")
	if err != nil {
		t.Fatalf("Listen(%s): %v", publishPath, err)
	}
	s := &testServer{ln: ln, path: publishPath}
	go s.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *testServer) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *testServer) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	body, err := frameproto.ReadRequestBytes(conn)
	if err != nil {
		return
	}
	s.calls.Add(1)
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	_, _ = frameproto.WriteFrame(conn, frameproto.StreamStdout, append([]byte("echo:"), body...))
	_, _ = frameproto.WriteExit(conn, 0)
}

// roundTrip sends one framed request and drains the response up to the exit frame.
func roundTrip(t *testing.T, conn net.Conn, body string) string {
	t.Helper()
	if err := frameproto.WriteRequest(conn, []byte(body)); err != nil {
		t.Fatalf("write request: %v", err)
	}
	var out []byte
	for {
		f, err := frameproto.ReadFrame(conn)
		if err != nil {
			t.Fatalf("read frame: %v", err)
		}
		if f.StreamID == frameproto.StreamExit {
			rc, err := frameproto.ExitCode(f.Payload)
			if err != nil {
				t.Fatalf("exit payload: %v", err)
			}
			if rc != 0 {
				t.Fatalf("exit rc = %d, want 0", rc)
			}
			return string(out)
		}
		out = append(out, f.Payload...)
	}
}

// dialPinnedRaw performs ONLY the pinned TLS dial, leaving the token frame to the
// caller. Every negative token test needs the transport up and the credential
// wrong, which Dial (which always sends the right token) cannot express.
func dialPinnedRaw(t *testing.T, endpointPath string) *tls.Conn {
	t.Helper()
	ep, err := Read(endpointPath)
	if err != nil {
		t.Fatalf("Read(%s): %v", endpointPath, err)
	}
	cert, err := x509.ParseCertificate(ep.CertDER)
	if err != nil {
		t.Fatalf("parse published cert: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", ep.HostPort, &tls.Config{
		RootCAs:    pool,
		ServerName: ServerName,
		MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		t.Fatalf("pinned TLS dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// writeRawTokenFrame writes an ARBITRARY length prefix and body, which is what the
// frame-bounds tests are about.
func writeRawTokenFrame(t *testing.T, conn net.Conn, length uint32, body []byte) {
	t.Helper()
	hdr := make([]byte, 4)
	binary.BigEndian.PutUint32(hdr, length)
	if _, err := conn.Write(hdr); err != nil {
		t.Fatalf("write length prefix: %v", err)
	}
	if len(body) > 0 {
		if _, err := conn.Write(body); err != nil {
			// A server that already hung up makes this a legitimate short write.
			t.Logf("write token body: %v", err)
		}
	}
}

// expectNoAck asserts the server hung up without acking: nothing is readable but
// EOF (or a reset), promptly.
func expectNoAck(t *testing.T, conn net.Conn) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var buf [1]byte
	n, err := conn.Read(buf[:])
	if err == nil {
		t.Fatalf("server sent %d byte(s) (%#02x) instead of dropping the connection", n, buf[0])
	}
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		t.Fatalf("server neither acked nor dropped the connection within 3s: %v", err)
	}
}

// syncBuf is a concurrency-safe log sink: the accept loop and its per-connection
// goroutines write while the test reads.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func captureLogger(t *testing.T) *syncBuf {
	t.Helper()
	buf := &syncBuf{}
	prev := Logger
	Logger = log.New(buf, "", 0)
	t.Cleanup(func() { Logger = prev })
	return buf
}

// serveAckAnyToken stands up a TLS listener with cert that accepts ANY token and
// acks it, then closes.
//
// This shape exists to remove a confound the obvious version has: a fake server
// that merely handshakes and hangs up makes Dial fail no matter what, so a pinning
// test built on it PASSES EVEN WHEN VERIFICATION SUCCEEDED — it would only be
// catching the missing ack. Acking means a successful handshake produces a
// SUCCESSFUL Dial, so `err != nil` really does mean the pin rejected the server.
func serveAckAnyToken(t *testing.T, cert tls.Certificate) net.Listener {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				hdr := make([]byte, 4)
				if _, err := io.ReadFull(conn, hdr); err != nil {
					return
				}
				n := binary.BigEndian.Uint32(hdr)
				if n > tokenFrameMax {
					return
				}
				if _, err := io.ReadFull(conn, make([]byte, n)); err != nil {
					return
				}
				_, _ = conn.Write([]byte{authAck})
			}()
		}
	}()
	return ln
}

// expectPinRejection asserts Dial failed BECAUSE the certificate was not the pinned
// one, not because of anything downstream of the handshake.
func expectPinRejection(t *testing.T, endpointPath string) {
	t.Helper()
	conn, err := Dial(endpointPath, 5*time.Second)
	if err == nil {
		_ = conn.Close()
		t.Fatal("Dial ACCEPTED a server whose certificate was not the pinned one")
	}
	if errors.Is(err, ErrAuthRejected) {
		t.Fatalf("the TLS handshake SUCCEEDED against an unpinned certificate — the dial only "+
			"failed later, at the token ack, so nothing about pinning was tested: %v", err)
	}
	if !strings.Contains(err.Error(), "certificate") && !strings.Contains(err.Error(), "authority") {
		t.Errorf("error = %v, want a certificate-verification failure", err)
	}
}

// ---------------------------------------------------------------------------
// The happy path
// ---------------------------------------------------------------------------

// TestEndpointRoundTripAuthenticates is the whole transport in one test: a client
// that knows only a file path reaches a frameproto daemon over pinned TLS with a
// token, and the reply arrives intact.
func TestEndpointRoundTripAuthenticates(t *testing.T) {
	s := startServer(t)

	conn, err := Dial(s.path, 5*time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if got, want := roundTrip(t, conn, `{"mode":"list"}`), `echo:{"mode":"list"}`; got != want {
		t.Errorf("response = %q, want %q", got, want)
	}
	if n := s.calls.Load(); n != 1 {
		t.Errorf("handler invoked %d times, want 1", n)
	}
}

// TestDialSetsNoSessionDeadline pins the contract cmd/yolo-ps depends on:
// dialTimeout bounds the dial and the ack, NOT the session. A whole-session
// deadline would pre-empt the daemon's own per-request timeout and destroy the
// canonical timeout-exit path.
func TestDialSetsNoSessionDeadline(t *testing.T) {
	s := startServer(t)
	s.delay = 600 * time.Millisecond

	conn, err := Dial(s.path, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// The response arrives 3x later than dialTimeout; a session deadline would
	// have killed it.
	if got, want := roundTrip(t, conn, `{"slow":true}`), `echo:{"slow":true}`; got != want {
		t.Errorf("response = %q, want %q", got, want)
	}
}

// TestRotationPickedUpWithoutRestart is the acceptance test for the settled
// token-in-the-endpoint-file decision: because the client re-reads the file on
// every dial, a listener that restarted on a NEW port with a NEW cert and a NEW
// token is picked up with no client restart and no jail relaunch.
func TestRotationPickedUpWithoutRestart(t *testing.T) {
	dir := privateDir(t)
	path := filepath.Join(dir, "svc.endpoint")

	first := startServerAt(t, path)
	before, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := Dial(path, 5*time.Second)
	if err != nil {
		t.Fatalf("first Dial: %v", err)
	}
	_ = roundTrip(t, conn, `{"n":1}`)
	_ = conn.Close()
	_ = first.ln.Close()

	second := startServerAt(t, path)
	after, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.HostPort == before.HostPort {
		t.Fatalf("restarted listener reused %s — the test cannot show a re-read", after.HostPort)
	}
	if after.Token == before.Token {
		t.Error("restarted listener republished the SAME token; rotation is not observable")
	}
	if bytes.Equal(after.CertDER, before.CertDER) {
		t.Error("restarted listener republished the SAME cert; the key is being persisted somewhere")
	}

	conn2, err := Dial(path, 5*time.Second)
	if err != nil {
		t.Fatalf("Dial after rotation: %v — the client cached something", err)
	}
	defer func() { _ = conn2.Close() }()
	if got, want := roundTrip(t, conn2, `{"n":2}`), `echo:{"n":2}`; got != want {
		t.Errorf("post-rotation response = %q, want %q", got, want)
	}
	if n := second.calls.Load(); n != 1 {
		t.Errorf("second listener's handler invoked %d times, want 1", n)
	}
}

// ---------------------------------------------------------------------------
// TLS: the pin, and only the pin
// ---------------------------------------------------------------------------

// TestPlaintextDialRejected: reachability is not authorization. A plaintext dial
// carrying a valid-looking token frame never reaches the handler.
func TestPlaintextDialRejected(t *testing.T) {
	s := startServer(t)
	ep, err := Read(s.path)
	if err != nil {
		t.Fatal(err)
	}

	conn, err := net.DialTimeout("tcp", ep.HostPort, 5*time.Second)
	if err != nil {
		t.Fatalf("plaintext dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	// The real token, sent in the clear: only the TLS layer can reject this.
	writeRawTokenFrame(t, conn, uint32(len(ep.Token)), []byte(ep.Token))
	expectNoAck(t, conn)

	if n := s.calls.Load(); n != 0 {
		t.Errorf("handler invoked %d times over a plaintext connection, want 0", n)
	}
}

// TestWrongCertRejected: a client pinning a DIFFERENT listener's published cert
// fails verification, which is what blocks a sibling impersonating the listener.
func TestWrongCertRejected(t *testing.T) {
	victim := startServer(t)
	other := startServer(t)

	victimEP, err := Read(victim.path)
	if err != nil {
		t.Fatal(err)
	}
	otherEP, err := Read(other.path)
	if err != nil {
		t.Fatal(err)
	}
	// The victim's address and token, the OTHER listener's cert.
	doctored := filepath.Join(privateDir(t), "doctored.endpoint")
	if err := Publish(doctored, Endpoint{
		HostPort: victimEP.HostPort, CertDER: otherEP.CertDER, Token: victimEP.Token,
	}); err != nil {
		t.Fatal(err)
	}

	expectPinRejection(t, doctored)
	if n := victim.calls.Load(); n != 0 {
		t.Errorf("handler invoked %d times behind a failed pin, want 0", n)
	}
}

// TestSystemRootsNotTrusted makes "pin the exact cert via a DEDICATED pool, never
// a CA" mechanical. It stands up a listener whose leaf chains to a CA that this
// process's SYSTEM root pool trusts, publishes an endpoint pinning an unrelated
// cert, and requires the dial to fail anyway.
//
// If Dial ever grew system roots — or MERGED them into the pinned pool, which is
// the tempting way to make a cert error go away — this dial would succeed. That
// merge is exactly the regression that would otherwise hide: see TestMain, which
// installs the fixture CA as this binary's system pool and warms the cache first so
// this test cannot be silently downgraded to a skip.
//
// docs/design/loophole-transport.md §5 is why: a CA we own had its private key
// readable inside every jail, so pinning must not depend on any CA.
func TestSystemRootsNotTrusted(t *testing.T) {
	if !pkgSystemPoolTrustsTestCA {
		t.Skip("this platform's system root pool ignores SSL_CERT_FILE (darwin reads the " +
			"keychain), so a system-trusted leaf cannot be built here; " +
			"TestUnpinnedButValidChainRejected covers the property deterministically")
	}
	leaf := mintLeafSignedBy(t, pkgTestCA, pkgTestCAKey)

	// Belt and braces: assert the premise against the pool Dial would have used.
	sysPool, err := x509.SystemCertPool()
	if err != nil {
		t.Fatalf("system cert pool: %v", err)
	}
	leafParsed, err := x509.ParseCertificate(leaf.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := leafParsed.Verify(x509.VerifyOptions{Roots: sysPool, DNSName: ServerName}); err != nil {
		t.Fatalf("premise lost: the leaf no longer verifies against the system pool: %v", err)
	}

	// The server ACKS any token, so a successful handshake yields a SUCCESSFUL
	// Dial — see serveAckAnyToken for why that matters.
	ln := serveAckAnyToken(t, leaf)

	// Pin something else entirely.
	_, unrelatedDER, err := mintCert()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(privateDir(t), "sysroots.endpoint")
	tok, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := Publish(path, Endpoint{HostPort: ln.Addr().String(), CertDER: unrelatedDER, Token: tok}); err != nil {
		t.Fatal(err)
	}
	expectPinRejection(t, path)
}

// TestUnpinnedButValidChainRejected is the deterministic half of the same claim,
// and unlike TestSystemRootsNotTrusted it can never skip: the server presents a
// COMPLETE, internally valid chain (leaf plus its issuing CA) and the client pins
// something else, so the dial must fail. "Pin the exact cert" means exactly this —
// a chain that would satisfy a normal verifier is still not enough.
func TestUnpinnedButValidChainRejected(t *testing.T) {
	caCert, caKey := mintTestCA(t)
	leaf := mintLeafSignedBy(t, caCert, caKey)

	ln := serveAckAnyToken(t, leaf)

	// Sanity: the chain really is valid against its own CA, so a failure below is
	// about the PIN and not about a broken fixture.
	leafParsed, err := x509.ParseCertificate(leaf.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	own := x509.NewCertPool()
	own.AddCert(caCert)
	if _, err := leafParsed.Verify(x509.VerifyOptions{Roots: own, DNSName: ServerName}); err != nil {
		t.Fatalf("fixture is broken — the leaf does not verify against its own CA: %v", err)
	}
	// And the fixture really does complete a Dial when the pin ALLOWS it, so the
	// rejection below cannot be an artifact of the double.
	pinnedToCA := filepath.Join(privateDir(t), "pinned-to-ca.endpoint")
	tok, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := Publish(pinnedToCA, Endpoint{HostPort: ln.Addr().String(), CertDER: caCert.Raw, Token: tok}); err != nil {
		t.Fatal(err)
	}
	okConn, err := Dial(pinnedToCA, 5*time.Second)
	if err != nil {
		t.Fatalf("control case failed: pinning the server's OWN issuer must complete a dial, "+
			"otherwise the rejection below proves nothing: %v", err)
	}
	_ = okConn.Close()

	_, unrelatedDER, err := mintCert()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(privateDir(t), "unpinned.endpoint")
	if err := Publish(path, Endpoint{HostPort: ln.Addr().String(), CertDER: unrelatedDER, Token: tok}); err != nil {
		t.Fatal(err)
	}
	expectPinRejection(t, path)
}

func mintTestCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	ca, key, err := newTestCA()
	if err != nil {
		t.Fatal(err)
	}
	return ca, key
}

func mintLeafSignedBy(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey) tls.Certificate {
	t.Helper()
	leaf, err := newLeafSignedBy(ca, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return leaf
}

// newLeafSignedBy issues a ServerName leaf under ca, returning the full chain
// (leaf + CA) so a server presenting it offers a complete, internally valid path.
func newLeafSignedBy(ca *x509.Certificate, caKey *ecdsa.PrivateKey) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: ServerName},
		DNSNames:     []string{ServerName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der, ca.Raw}, PrivateKey: key}, nil
}

// ---------------------------------------------------------------------------
// The token frame
// ---------------------------------------------------------------------------

// TestWrongTokenRejected: the right pin and the wrong credential is a rejection
// with its OWN error, not an EOF the caller has to guess about — and the handler's
// invocation count is unchanged, measured rather than inferred.
func TestWrongTokenRejected(t *testing.T) {
	s := startServer(t)
	ep, err := Read(s.path)
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(privateDir(t), "wrongtoken.endpoint")
	if err := Publish(path, Endpoint{HostPort: ep.HostPort, CertDER: ep.CertDER, Token: wrong}); err != nil {
		t.Fatal(err)
	}

	_, err = Dial(path, 5*time.Second)
	if err == nil {
		t.Fatal("Dial succeeded with the wrong token")
	}
	if !errors.Is(err, ErrAuthRejected) {
		t.Errorf("error = %v, want one satisfying errors.Is(err, ErrAuthRejected) — a token "+
			"mismatch must not be reported as a failure of whatever is behind the transport", err)
	}
	if n := s.calls.Load(); n != 0 {
		t.Errorf("handler invoked %d times with a rejected token, want 0", n)
	}
}

// TestAbsentTokenRejected: a connection that presents no token at all is dropped,
// the handler is never reached, and the listener keeps serving afterwards.
func TestAbsentTokenRejected(t *testing.T) {
	s := startServer(t)

	conn := dialPinnedRaw(t, s.path)
	if err := conn.Handshake(); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	_ = conn.Close()

	if n := s.calls.Load(); n != 0 {
		t.Errorf("handler invoked %d times with no token, want 0", n)
	}
	// Not wedged: a good client still gets through.
	good, err := Dial(s.path, 5*time.Second)
	if err != nil {
		t.Fatalf("Dial after an unauthenticated connection: %v", err)
	}
	defer func() { _ = good.Close() }()
	_ = roundTrip(t, good, `{"after":"drop"}`)
	if n := s.calls.Load(); n != 1 {
		t.Errorf("handler invoked %d times, want 1", n)
	}
}

// TestZeroLengthTokenFrameRejected: a zero length prefix is rejected as a FRAME,
// never treated as "no token supplied, allow".
func TestZeroLengthTokenFrameRejected(t *testing.T) {
	buf := captureLogger(t)
	s := startServer(t)

	conn := dialPinnedRaw(t, s.path)
	writeRawTokenFrame(t, conn, 0, nil)
	expectNoAck(t, conn)

	if n := s.calls.Load(); n != 0 {
		t.Errorf("handler invoked %d times on a zero-length token frame, want 0", n)
	}
	if !strings.Contains(buf.String(), "token frame length 0 out of range") {
		t.Errorf("log did not name the rejected frame length; got %q", buf.String())
	}
}

// TestOversizedTokenFrameRejected: the length cap is checked BEFORE the buffer is
// allocated, so a garbage prefix from an unauthenticated caller cannot make the
// daemon allocate. The drop must also be PROMPT — a server that tried to read 4 GiB
// would sit there until the handshake deadline.
func TestOversizedTokenFrameRejected(t *testing.T) {
	buf := captureLogger(t)
	s := startServer(t)

	conn := dialPinnedRaw(t, s.path)
	start := time.Now()
	writeRawTokenFrame(t, conn, 0xFFFFFFFF, nil)
	expectNoAck(t, conn)
	if elapsed := time.Since(start); elapsed > handshakeTimeout {
		t.Errorf("drop took %v, longer than the handshake timeout — the length was not "+
			"rejected before the read", elapsed)
	}

	if n := s.calls.Load(); n != 0 {
		t.Errorf("handler invoked %d times on an oversized token frame, want 0", n)
	}
	if !strings.Contains(buf.String(), "token frame length 4294967295 out of range") {
		t.Errorf("log did not report the oversized length; got %q", buf.String())
	}
}

// TestTokenFrameAtExactCap pins the boundary as ">", not ">=": a frame of exactly
// tokenFrameMax bytes is READ (and then fails the token comparison), which is a
// different rejection from an out-of-range length.
func TestTokenFrameAtExactCap(t *testing.T) {
	buf := captureLogger(t)
	s := startServer(t)

	conn := dialPinnedRaw(t, s.path)
	writeRawTokenFrame(t, conn, tokenFrameMax, bytes.Repeat([]byte("x"), tokenFrameMax))
	expectNoAck(t, conn)

	log := buf.String()
	if !strings.Contains(log, "token mismatch (4096 bytes)") {
		t.Errorf("a frame at the exact cap must be read and then MISMATCH; log = %q", log)
	}
	if strings.Contains(log, "out of range") {
		t.Errorf("a frame at the exact cap was rejected for its length; log = %q", log)
	}
	if n := s.calls.Load(); n != 0 {
		t.Errorf("handler invoked %d times, want 0", n)
	}
}

// TestTokenIsPerListenerAndWellFormed: scope is per-(jail, service), so two
// listeners must not share a credential; and the format is 64 lowercase hex, which
// is what keeps the endpoint file whitespace-separable with no escaping.
func TestTokenIsPerListenerAndWellFormed(t *testing.T) {
	a := startServer(t)
	b := startServer(t)

	epA, err := Read(a.path)
	if err != nil {
		t.Fatal(err)
	}
	epB, err := Read(b.path)
	if err != nil {
		t.Fatal(err)
	}
	if epA.Token == epB.Token {
		t.Error("two listeners published the SAME token: compromising one published file " +
			"would grant the other")
	}
	for _, tok := range []string{epA.Token, epB.Token} {
		if !IsToken(tok) {
			t.Errorf("IsToken(%q) = false, want 64 lowercase hex", tok)
		}
		if len(tok) != 64 {
			t.Errorf("token length = %d, want 64", len(tok))
		}
		if strings.ToLower(tok) != tok {
			t.Errorf("token %q is not lowercase", tok)
		}
	}
}

func TestIsTokenRejectsMalformed(t *testing.T) {
	good := strings.Repeat("ab", 32)
	cases := map[string]bool{
		good:                         true,
		"":                           false,
		strings.Repeat("ab", 31):     false, // too short
		strings.Repeat("ab", 33):     false, // too long
		strings.ToUpper(good):        false, // uppercase hex is not what we mint
		good[:63] + "g":              false, // not hex
		good[:63] + " ":              false, // whitespace would break Fields
		strings.Repeat("0", 64):      true,
		strings.Repeat("f", 64):      true,
		good[:32] + "\n" + good[33:]: false,
	}
	for in, want := range cases {
		if got := IsToken(in); got != want {
			t.Errorf("IsToken(%q) = %v, want %v", in, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// Logs
// ---------------------------------------------------------------------------

// TestNoTokenOrCertInLogs: the endpoint file is a credential, so a well-meant
// "print the endpoint for debugging" would write a live secret into the log dir and
// into every transcript. CI's secret scan runs --only-verified and would not catch
// it, so the guard has to be a test.
func TestNoTokenOrCertInLogs(t *testing.T) {
	buf := captureLogger(t)
	s := startServer(t)
	ep, err := Read(s.path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}

	// A success...
	conn, err := Dial(s.path, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = roundTrip(t, conn, `{"ok":true}`)
	_ = conn.Close()
	// ...and a mismatch.
	bad := dialPinnedRaw(t, s.path)
	writeRawTokenFrame(t, bad, 8, []byte("deadbeef"))
	expectNoAck(t, bad)

	log := buf.String()
	if log == "" {
		t.Fatal("captured no log output at all; the test proves nothing")
	}
	if strings.Contains(log, ep.Token) {
		t.Error("THE TOKEN APPEARS IN THE LOG")
	}
	if strings.Contains(log, base64.StdEncoding.EncodeToString(ep.CertDER)) {
		t.Error("the published cert appears in the log")
	}
	if strings.Contains(log, strings.TrimSpace(string(raw))) {
		t.Error("the whole endpoint line appears in the log")
	}
	if !strings.Contains(log, "token mismatch (8 bytes)") {
		t.Errorf("the mismatch line must carry a LENGTH and nothing else; log = %q", log)
	}
	if strings.Contains(log, "deadbeef") {
		t.Error("the REJECTED token value appears in the log")
	}
}

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

// TestAdvertiseHostDiffersFromBindHost pins the trap: bind 127.0.0.1 (off the LAN)
// but publish the gateway name the jail resolves. Reverse them and the jail dials
// its own loopback.
func TestAdvertiseHostDiffersFromBindHost(t *testing.T) {
	path := filepath.Join(privateDir(t), "advertise.endpoint")
	ln, err := Listen(path, "host.containers.internal")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	ep, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	host, port, err := net.SplitHostPort(ep.HostPort)
	if err != nil {
		t.Fatalf("published %q does not split: %v", ep.HostPort, err)
	}
	if host != "host.containers.internal" {
		t.Errorf("published host = %q, want the ADVERTISED name", host)
	}
	bound := ln.Addr().(*net.TCPAddr)
	if bound.IP.String() != "127.0.0.1" {
		t.Errorf("bound IP = %s, want 127.0.0.1", bound.IP)
	}
	if port != strconv.Itoa(bound.Port) {
		t.Errorf("published port %s != bound port %d", port, bound.Port)
	}
}

func TestAdvertiseHostDefaultsAndOverrides(t *testing.T) {
	t.Setenv(AdvertiseHostEnv, "")
	if got := AdvertiseHost(); got != DefaultAdvertiseHost {
		t.Errorf("AdvertiseHost with an empty override = %q, want %q", got, DefaultAdvertiseHost)
	}
	t.Setenv(AdvertiseHostEnv, "gateway.example")
	if got := AdvertiseHost(); got != "gateway.example" {
		t.Errorf("AdvertiseHost = %q, want the override", got)
	}
	// An empty advertiseHost argument must never publish a bare ":port".
	t.Setenv(AdvertiseHostEnv, "gateway.example")
	path := filepath.Join(privateDir(t), "default.endpoint")
	ln, err := Listen(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	ep, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if host, _, _ := net.SplitHostPort(ep.HostPort); host != "gateway.example" {
		t.Errorf("published host = %q, want the resolved advertise host", host)
	}
}

// TestListenPublishesOnlyAfterBind: a published file always names a LIVE listener
// (which is what makes Probe a meaningful health check), and a publication that
// cannot succeed leaves nothing behind.
func TestListenPublishesOnlyAfterBind(t *testing.T) {
	// Publication implies liveness: a dial straight after Listen works, with no
	// waiting or retry loop.
	s := startServer(t)
	if !Probe(s.path) {
		t.Fatal("Probe false immediately after Listen returned")
	}
	conn, err := Dial(s.path, 5*time.Second)
	if err != nil {
		t.Fatalf("the published endpoint did not name a live listener: %v", err)
	}
	_ = conn.Close()

	// A pre-bind failure (the publish directory is not a directory) publishes
	// nothing at all.
	dir := privateDir(t)
	notADir := filepath.Join(dir, "notadir")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(filepath.Join(notADir, "svc.endpoint"), "127.0.0.1"); err == nil {
		t.Error("Listen succeeded with a regular file for a publish directory")
	}

	// A POST-bind publish failure (rename cannot replace a non-empty directory)
	// still leaves no endpoint content and no temp file.
	occupied := filepath.Join(dir, "occupied.endpoint")
	if err := os.MkdirAll(filepath.Join(occupied, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(occupied, "127.0.0.1"); err == nil {
		t.Error("Listen succeeded when its publication could not be renamed into place")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".endpoint-") {
			t.Errorf("a failed publish left the temp file %s behind", e.Name())
		}
	}
}

// TestCloseUnlinksEndpoint: retiring the listener retires its credential in the
// same step, which is what makes token retirement identical to endpoint retirement.
func TestCloseUnlinksEndpoint(t *testing.T) {
	path := filepath.Join(privateDir(t), "close.endpoint")
	ln, err := Listen(path, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("nothing published: %v", err)
	}
	if err := ln.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Close left the credential on disk: stat err = %v", err)
	}
	// Idempotent: a second Close must not error on the already-removed file.
	if err := ln.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if _, err := ln.Accept(); err == nil {
		t.Error("Accept succeeded on a closed listener")
	}
}

// TestPrivateKeyNeverTouchesDisk walks the publication tree after a full round
// trip: the endpoint file is the ONLY artifact, and no file anywhere in it contains
// a private key. This is the mechanical form of "the TLS key lives only in process
// memory".
func TestPrivateKeyNeverTouchesDisk(t *testing.T) {
	dir := privateDir(t)
	path := filepath.Join(dir, "svc.endpoint")
	s := startServerAt(t, path)
	conn, err := Dial(path, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = roundTrip(t, conn, `{"x":1}`)
	_ = conn.Close()
	if n := s.calls.Load(); n != 1 {
		t.Fatalf("handler invoked %d times, want 1 — no round trip happened", n)
	}

	var files []string
	err = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		files = append(files, p)
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		for _, needle := range []string{"PRIVATE KEY", "BEGIN EC PARAMETERS"} {
			if bytes.Contains(data, []byte(needle)) {
				t.Errorf("%s contains %q", p, needle)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != path {
		t.Errorf("publication dir holds %v, want exactly [%s]", files, path)
	}
}

// TestNoUnixFallbackExists inverts #32's off-macOS-unix-fallback test: there is no
// unix path any more, so pointing Dial at an actual socket must FAIL rather than
// silently connect to it.
func TestNoUnixFallbackExists(t *testing.T) {
	dir := privateDir(t)
	sock := filepath.Join(dir, "svc.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	if Probe(sock) {
		t.Error("Probe accepted a unix socket as a published endpoint")
	}
	conn, err := Dial(sock, time.Second)
	if err == nil {
		_ = conn.Close()
		t.Fatal("Dial connected to a unix socket — the retired transport is still reachable")
	}
	if errors.Is(err, ErrEndpointMissing) {
		t.Errorf("a socket that EXISTS was reported as a missing endpoint: %v", err)
	}
}
