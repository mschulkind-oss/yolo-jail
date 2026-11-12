package callbroker

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

func fixturePinnedHTTPServer(t *testing.T, handler http.Handler, handshakeGate ...func()) (*httptest.Server, httpExecutor, string) {
	t.Helper()
	var gate func()
	if len(handshakeGate) != 0 {
		gate = handshakeGate[0]
	}
	return startPinnedHTTPServer(t, handler, gate, nil)
}

// pinnedTLSWitness observes the fixture server's side of each accepted TLS connection.
// Every callback names the connection by the same net.Conn value, so a test can bind an
// event to the one connection it gated and ignore every other (a lookup read, say).
type pinnedTLSWitness struct {
	// gate runs inside GetCertificate, before the server has answered the ClientHello.
	gate func(conn net.Conn)
	// handshake reports the server's Handshake result, once per accepted connection.
	// err is nil only after Handshake returned nil: in TLS 1.3 the server has then read
	// and verified the client's Finished, which the client sends only after it verified
	// the server certificate against its pinned roots and name.
	handshake func(conn net.Conn, state tls.ConnectionState, err error)
	// ended reports the first Read the HTTP server makes on a handshaken connection: its
	// byte count and error. n > 0 means the client sent application bytes; see
	// pinnedTLSWitnessEndedUnused for what a clean end means.
	ended func(conn net.Conn, n int, err error)
}

// fixturePinnedHTTPServerWithTLSWitness is fixturePinnedHTTPServer with the same real
// certificate, pinned root and name, served through a listener that completes each TLS
// handshake itself so the witness sees the result. Nothing about the trust is relaxed.
func fixturePinnedHTTPServerWithTLSWitness(t *testing.T, handler http.Handler, witness *pinnedTLSWitness) (*httptest.Server, httpExecutor, string) {
	t.Helper()
	return startPinnedHTTPServer(t, handler, nil, witness)
}

func startPinnedHTTPServer(t *testing.T, handler http.Handler, handshakeGate func(), witness *pinnedTLSWitness) (*httptest.Server, httpExecutor, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fixture.example"}, DNSNames: []string{"fixture.example"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	encoded, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(encoded)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	if witness == nil {
		server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{encoded}, PrivateKey: key}}}
		if handshakeGate != nil {
			server.TLS.GetCertificate = func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
				handshakeGate()
				return &server.TLS.Certificates[0], nil
			}
		}
		server.StartTLS()
		t.Cleanup(server.Close)
	} else {
		leaf := &tls.Certificate{Certificate: [][]byte{encoded}, PrivateKey: key}
		ctx, cancel := context.WithCancel(context.Background())
		listener := &pinnedTLSWitnessListener{Listener: server.Listener, witness: witness, ctx: ctx, cancel: cancel,
			ready: make(chan net.Conn), done: make(chan struct{})}
		// Session tickets are disabled so the server writes no NewSessionTicket the client
		// never reads; unread bytes could turn a raw close into a reset instead of a FIN.
		listener.config = &tls.Config{Certificates: []tls.Certificate{*leaf}, SessionTicketsDisabled: true, GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if witness.gate != nil {
				witness.gate(hello.Conn)
			}
			return leaf, nil
		}}
		// The listener is swapped in before Start, which serves whatever server.Listener
		// holds at that moment; the server itself speaks plain HTTP over the connections
		// this listener has already secured.
		server.Listener = listener
		listener.work.Add(1)
		go listener.accept()
		server.Start()
		t.Cleanup(func() {
			server.Close()
			joined := make(chan struct{})
			go func() {
				listener.work.Wait()
				close(joined)
			}()
			select {
			case <-joined:
			case <-time.After(90 * time.Second):
				t.Error("pinned TLS witness listener goroutines still live after Close; a handshake gate was not released before teardown")
			}
		})
	}
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	resolver := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	return server, httpExecutor{resolve: resolver, roots: roots}, "https://fixture.example:" + parsed.Port()
}

// pinnedTLSWitnessListener accepts loopback connections, runs each server-side TLS
// handshake on its own goroutine (so one gated handshake cannot stall the others), and
// hands only handshaken connections to the HTTP server.
type pinnedTLSWitnessListener struct {
	net.Listener
	config  *tls.Config
	witness *pinnedTLSWitness
	ctx     context.Context
	cancel  context.CancelFunc
	ready   chan net.Conn
	done    chan struct{}
	closing sync.Once
	work    sync.WaitGroup
}

func (l *pinnedTLSWitnessListener) accept() {
	defer l.work.Done()
	for {
		raw, err := l.Listener.Accept()
		if err != nil {
			return
		}
		l.work.Add(1)
		go l.secure(raw)
	}
}

func (l *pinnedTLSWitnessListener) secure(raw net.Conn) {
	defer l.work.Done()
	counted := &tlsRawEOFConn{Conn: raw}
	secured := tls.Server(counted, l.config)
	if err := secured.HandshakeContext(l.ctx); err != nil {
		if l.witness.handshake != nil {
			l.witness.handshake(counted, tls.ConnectionState{}, err)
		}
		_ = raw.Close()
		return
	}
	if l.witness.handshake != nil {
		l.witness.handshake(counted, secured.ConnectionState(), nil)
	}
	select {
	case l.ready <- &pinnedTLSWitnessConn{Conn: secured, counted: counted, witness: l.witness}:
	case <-l.done:
		_ = secured.Close()
	}
}

func (l *pinnedTLSWitnessListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.ready:
		return conn, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *pinnedTLSWitnessListener) Close() error {
	var err error
	l.closing.Do(func() {
		close(l.done)
		l.cancel()
		err = l.Listener.Close()
	})
	return err
}

// pinnedTLSWitnessConn is deliberately not a *tls.Conn: the HTTP server then does not
// handshake again, and the first Read it makes is reported to the witness.
type pinnedTLSWitnessConn struct {
	net.Conn
	counted *tlsRawEOFConn
	witness *pinnedTLSWitness
	first   sync.Once
}

func (c *pinnedTLSWitnessConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.first.Do(func() {
		if c.witness.ended != nil {
			c.witness.ended(c.counted, n, err)
		}
	})
	return n, err
}

// errTLSRawTransportClosed replaces a raw io.EOF. crypto/tls reports a bare io.EOF at a
// record boundary as io.EOF, the same error a close_notify produces, so the raw close is
// renamed before it reaches the TLS layer.
var errTLSRawTransportClosed = errors.New("fixture raw transport closed without TLS shutdown alert")

// tlsRawEOFConn passes reads through and maps a raw io.EOF to errTLSRawTransportClosed.
type tlsRawEOFConn struct {
	net.Conn
}

func (c *tlsRawEOFConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if err == io.EOF {
		err = errTLSRawTransportClosed
	}
	return n, err
}

// pinnedTLSWitnessEndedUnused is the withdrawal witness's predicate on the server's first
// read of a handshaken connection. Only a decrypted close_notify reads as a clean io.EOF
// with no bytes; a raw close reads as errTLSRawTransportClosed.
func pinnedTLSWitnessEndedUnused(n int, err error) bool {
	return n == 0 && err == io.EOF
}

// TestPinnedTLSWitnessEndedUnusedOnlyForCloseNotify is the negative arm of the withdrawal
// witness. After a completed handshake, the predicate the withdrawal test applies holds
// after a close_notify and fails after a bare socket close. No application bytes are
// sent, and the fixture handler must see no request in either arm.
func TestPinnedTLSWitnessEndedUnusedOnlyForCloseNotify(t *testing.T) {
	// hangGuard only turns a hang into a red test. Each pass condition is an event.
	const hangGuard = 90 * time.Second
	type readEnd struct {
		n   int
		err error
	}
	for _, tc := range []struct {
		name        string
		closeNotify bool
	}{
		{name: "bare socket close after a completed handshake", closeNotify: false},
		{name: "close_notify after a completed handshake", closeNotify: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handshakes := make(chan error, 1)
			ends := make(chan readEnd, 1)
			var requests atomic.Int32
			witness := &pinnedTLSWitness{
				handshake: func(_ net.Conn, _ tls.ConnectionState, err error) { handshakes <- err },
				ended:     func(_ net.Conn, n int, err error) { ends <- readEnd{n, err} },
			}
			_, executor, origin := fixturePinnedHTTPServerWithTLSWitness(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				requests.Add(1)
			}), witness)
			parsed, err := url.Parse(origin)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
			defer cancel()
			raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", parsed.Port()))
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			client := tls.Client(raw, &tls.Config{RootCAs: executor.roots, ServerName: "fixture.example", MinVersion: tls.VersionTLS13})
			if err := client.HandshakeContext(ctx); err != nil {
				t.Fatalf("pinned client handshake: %v", err)
			}
			select {
			case err := <-handshakes:
				if err != nil {
					t.Fatalf("server handshake failed: %v", err)
				}
			case <-time.After(hangGuard):
				t.Fatalf("no server handshake result within the %s hang guard", hangGuard)
			}
			if tc.closeNotify {
				if err := client.Close(); err != nil {
					t.Fatalf("close_notify: %v", err)
				}
			} else if err := raw.Close(); err != nil {
				t.Fatalf("raw close: %v", err)
			}
			select {
			case got := <-ends:
				if pinnedTLSWitnessEndedUnused(got.n, got.err) != tc.closeNotify {
					t.Fatalf("first server read n=%d err=%v: predicate=%v, want %v", got.n, got.err, !tc.closeNotify, tc.closeNotify)
				}
			case <-time.After(hangGuard):
				t.Fatalf("server never read the connection to its end within the %s hang guard", hangGuard)
			}
			if requests.Load() != 0 {
				t.Fatalf("fixture handler received %d requests on a connection that sent none", requests.Load())
			}
		})
	}
}

func TestPinnedHTTPExecutorCapsResponseAndRedactsExactKnownToken(t *testing.T) {
	const secret = "synthetic-known-token"
	var mode atomic.Int32
	var requests atomic.Int32
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch mode.Load() {
		case 1:
			_, _ = w.Write(bytes.Repeat([]byte("x"), httpResponseLimit+1))
		case 2:
			w.Header().Set("Location", "https://other.example/resource?token="+secret)
			w.WriteHeader(http.StatusFound)
		default:
			w.Header().Set("X-Echo", "before-"+secret+"-after")
			_, _ = w.Write([]byte("before-" + secret[:8]))
			_, _ = w.Write([]byte(secret[8:] + "-after"))
		}
	}))
	binding := loopholedecl.CallBrokerHTTP{Origin: origin, Credential: loopholedecl.CallBrokerHTTPCredential{Source: "host_env", Key: "SYNTHETIC", Header: "Authorization"}}
	call := HTTPCall{Method: "GET", Path: "/items/a1"}
	out, err := executor.run(context.Background(), binding, call, secret)
	if err != nil || !out.PossibleTransmission || strings.Contains(string(out.Body), secret) || !strings.Contains(string(out.Body), "[redacted]") || strings.Contains(out.Headers.Get("X-Echo"), secret) {
		t.Fatalf("exact secret leaked: response=%+v err=%v", out, err)
	}
	mode.Store(1)
	out, err = executor.run(context.Background(), binding, call, secret)
	if err == nil || !out.PossibleTransmission || len(out.Body) != 0 || requests.Load() != 2 {
		t.Fatalf("oversized response not refused as possible transmission: outcome=%+v err=%v requests=%d", out, err, requests.Load())
	}
	mode.Store(2)
	out, err = executor.run(context.Background(), binding, call, secret)
	if err == nil || !out.PossibleTransmission || strings.Contains(fmt.Sprint(out.Headers), secret) || requests.Load() != 3 {
		t.Fatalf("known token in redirect URL not refused: outcome=%+v err=%v requests=%d", out, err, requests.Load())
	}
}

func TestPinnedHTTPExecutorSendsCanonicalRawTargetWithoutRewritingEscapes(t *testing.T) {
	var got string
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.RequestURI
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	binding := loopholedecl.CallBrokerHTTP{Origin: origin, Credential: loopholedecl.CallBrokerHTTPCredential{Source: "host_env", Key: "SYNTHETIC", Header: "Authorization"}}
	call := HTTPCall{Method: "GET", Path: "/items/%2fa", Query: "x=%7e%2F"}
	out, err := executor.run(context.Background(), binding, call, "synthetic-only-secret")
	if err != nil || out.Status != 200 || got != "/items/%2fa?x=%7e%2F" {
		t.Fatalf("canonical raw target changed: receiver=%q status=%d err=%v", got, out.Status, err)
	}
}

func TestPinnedHTTPExecutorNeverForwardsCallerAuthorityOrRedirect(t *testing.T) {
	const secret = "synthetic-pinned-token"
	var first, second atomic.Int32
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		second.Add(1)
	}))
	defer redirect.Close()
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		first.Add(1)
		if r.Method != "POST" || r.URL.EscapedPath() != "/graphql" || r.URL.RawQuery != "op=rename" || r.Header.Get("Authorization") != "Bearer "+secret || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Cookie") != "" || r.Header.Get("User-Agent") != "" {
			t.Errorf("received altered or unapproved request: method=%q path=%q query=%q headers=%v", r.Method, r.URL.EscapedPath(), r.URL.RawQuery, r.Header)
		}
		body := make([]byte, len(`{"operationName":"Rename"}`))
		_, _ = r.Body.Read(body)
		if !bytes.Equal(body, []byte(`{"operationName":"Rename"}`)) {
			t.Errorf("receiver got altered body %q", body)
		}
		w.Header().Set("Location", redirect.URL)
		w.Header().Set("Set-Cookie", "token="+secret)
		w.Header().Set("X-Token-Echo", "prefix-"+secret+"-suffix")
		w.WriteHeader(http.StatusFound)
		_, _ = w.Write([]byte("before-"))
		_, _ = w.Write([]byte(secret[:8]))
		_, _ = w.Write([]byte(secret[8:]))
		_, _ = w.Write([]byte("-after"))
	}))
	binding := loopholedecl.CallBrokerHTTP{Origin: origin, Credential: loopholedecl.CallBrokerHTTPCredential{Source: "host_env", Key: "SYNTHETIC", Header: "Authorization", Prefix: "Bearer "}, RequestHeaders: []string{"Content-Type"}}
	call := HTTPCall{Method: "POST", Path: "/graphql", Query: "op=rename", Headers: []HTTPHeader{{Name: "Content-Type", Values: []string{"application/json"}}}, BodyPresent: true, Body: []byte(`{"operationName":"Rename"}`)}
	out, err := executor.run(context.Background(), binding, call, secret)
	if err != nil || out.Status != http.StatusFound || !out.PossibleTransmission || first.Load() != 1 || second.Load() != 0 {
		t.Fatalf("redirect request/response: first=%d second=%d out=%+v err=%v", first.Load(), second.Load(), out, err)
	}
	if out.Headers.Get("Set-Cookie") != "" || !strings.Contains(string(out.Body), "[redacted]") || strings.Contains(string(out.Body), secret) || strings.Contains(fmt.Sprint(out.Headers), secret) {
		t.Fatalf("secret or cookie reached the caller: headers=%v body=%q", out.Headers, out.Body)
	}
	call.Query = "token=" + secret
	_, err = executor.run(context.Background(), binding, call, secret)
	if err == nil || first.Load() != 1 {
		t.Fatalf("credential in URL dispatched: first=%d err=%v", first.Load(), err)
	}
}
