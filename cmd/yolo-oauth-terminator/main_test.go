package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestVerbatimResponseHeaderNames is the regression for the BLOCKER: proxied
// upstream response header names must reach the client byte-for-byte (Python
// send_header writes them verbatim), NOT canonicalized by net/http. It stands
// up the real handler + a fake broker relay returning a lowercase header name
// and asserts the client sees the lowercase name.
func TestVerbatimResponseHeaderNames(t *testing.T) {
	if testing.Short() {
		t.Skip("network; -short")
	}
	dir := t.TempDir()
	sock := filepath.Join(dir, "relay.sock")
	// Fake relay: reply to action=proxy with a lowercase 'x-request-id' header.
	startFakeRelay(t, sock, map[string]any{
		"status":   418,
		"headers":  map[string]any{"x-request-id": "abc123"},
		"body_b64": "aGk=", // "hi"
	})

	// Serve the handler over an httptest-style plain server (we only assert
	// header casing, which the handler controls before TLS).
	srv := &http.Server{Handler: makeHandler(sock)}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go srv.Serve(ln)
	defer srv.Close()

	// Raw HTTP/1.1 request so we can read the response header bytes verbatim
	// (Go's http.Client would canonicalize them on our side).
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(c, "GET /whatever HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")
	raw, _ := io.ReadAll(c)
	head := string(raw)
	if !strings.Contains(head, "x-request-id: abc123") {
		t.Errorf("response headers not verbatim; want lowercase 'x-request-id', got:\n%s", firstLines(head, 12))
	}
	if strings.Contains(head, "X-Request-Id") {
		t.Errorf("header name was canonicalized to X-Request-Id:\n%s", firstLines(head, 12))
	}
}

// TestPinnedHTTP11 asserts the TLS server does not negotiate h2.
func TestPinnedHTTP11(t *testing.T) {
	if testing.Short() {
		t.Skip("network; -short")
	}
	dir := t.TempDir()
	cert, key := genSelfSigned(t, dir)
	sock := filepath.Join(dir, "relay.sock")
	startFakeRelay(t, sock, map[string]any{"status": 200, "headers": map[string]any{}, "body_b64": ""})

	srv := &http.Server{
		Handler:      makeHandler(sock),
		TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){},
	}
	srv.SetKeepAlivesEnabled(false)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go srv.ServeTLS(ln, cert, key)
	defer srv.Close()
	time.Sleep(100 * time.Millisecond)

	// Client offering h2 must negotiate http/1.1 (or no ALPN), never h2.
	conn, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{"h2", "http/1.1"},
	})
	if err != nil {
		t.Fatalf("tls dial: %v", err)
	}
	defer conn.Close()
	if proto := conn.ConnectionState().NegotiatedProtocol; proto == "h2" {
		t.Errorf("negotiated ALPN %q, want http/1.1 (HTTP/2 voids keep-alive parity)", proto)
	}
}

// --- helpers ---

// startFakeRelay serves one framed action=proxy request, replying with resp
// framed as stdout JSON + exit 0.
func startFakeRelay(t *testing.T, sock string, resp map[string]any) {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				hdr := make([]byte, 4)
				if _, err := io.ReadFull(c, hdr); err != nil {
					return
				}
				n := binary.BigEndian.Uint32(hdr)
				io.ReadFull(c, make([]byte, n))
				body, _ := json.Marshal(resp)
				fh := make([]byte, 5)
				binary.BigEndian.PutUint32(fh[1:], uint32(len(body)))
				c.Write(fh) // stream 0
				c.Write(body)
				ex := make([]byte, 5)
				ex[0] = 2 // exit
				binary.BigEndian.PutUint32(ex[1:], 4)
				c.Write(ex)
				c.Write([]byte{0, 0, 0, 0})
			}(c)
		}
	}()
	time.Sleep(30 * time.Millisecond)
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// genSelfSigned writes a throwaway self-signed cert+key for the TLS test.
func genSelfSigned(t *testing.T, dir string) (certPath, keyPath string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Unix(1_600_000_000, 0),
		NotAfter:     time.Unix(4_000_000_000, 0),
		DNSNames:     []string{"localhost", "127.0.0.1"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	cf, _ := os.Create(certPath)
	pem.Encode(cf, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	cf.Close()
	kb, _ := x509.MarshalECPrivateKey(priv)
	kf, _ := os.Create(keyPath)
	pem.Encode(kf, &pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})
	kf.Close()
	return certPath, keyPath
}
