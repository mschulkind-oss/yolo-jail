package oauthterminator

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestIsRefreshGrant(t *testing.T) {
	cases := map[string]bool{
		`{"grant_type":"refresh_token"}`:           true,
		`{"grant_type":"refresh_token","extra":1}`: true,
		`{"grant_type":"authorization_code"}`:      false,
		`{"grant_type":"refresh_token"} trailing`:  false, // trailing data -> not parseable
		`{}`:                false,
		``:                  false,
		`not json`:          false,
		`["refresh_token"]`: false, // not an object
		`"refresh_token"`:   false,
	}
	for body, want := range cases {
		if got := IsRefreshGrant([]byte(body)); got != want {
			t.Errorf("IsRefreshGrant(%q) = %v, want %v", body, got, want)
		}
	}
}

// shortDir: AF_UNIX path cap.
func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "yj-term-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

// TestAskHostBrokerRelayLayerMissingSocket: a missing socket path is the relay
// layer ("relay unreachable — ...").
func TestAskHostBrokerRelayLayerMissingSocket(t *testing.T) {
	_, err := AskHostBroker(filepath.Join(shortDir(t), "nope.sock"), singleton("action", "ping"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.HasPrefix(err.Error(), "relay unreachable") {
		t.Errorf("err = %q, want relay-layer prefix", err.Error())
	}
}

// TestAskHostBrokerBrokerLayerEOF: a relay that accepts then closes WITHOUT an
// exit frame is the broker layer ("host broker unreachable through the relay
// (connection closed without an exit frame)").
func TestAskHostBrokerBrokerLayerEOF(t *testing.T) {
	dir := shortDir(t)
	sock := filepath.Join(dir, "relay.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		// Read the request, then close without any response frame.
		hdr := make([]byte, 4)
		io.ReadFull(c, hdr)
		length := binary.BigEndian.Uint32(hdr)
		io.ReadFull(c, make([]byte, length))
		c.Close()
	}()
	_, err = AskHostBroker(sock, singleton("action", "refresh"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unreachable through the relay") ||
		!strings.Contains(err.Error(), "without an exit frame") {
		t.Errorf("err = %q, want broker-layer EOF message", err.Error())
	}
	// Crucially, it must NOT be the relay-layer wording.
	if strings.HasPrefix(err.Error(), "relay unreachable") {
		t.Errorf("broker-down misattributed to relay layer: %q", err.Error())
	}
}

// TestAskHostBrokerSuccess: a relay that returns a framed JSON stdout + exit 0
// yields the parsed object.
func TestAskHostBrokerSuccess(t *testing.T) {
	dir := shortDir(t)
	sock := filepath.Join(dir, "relay.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		hdr := make([]byte, 4)
		io.ReadFull(c, hdr)
		length := binary.BigEndian.Uint32(hdr)
		io.ReadFull(c, make([]byte, length))
		// stdout frame with a JSON body, then exit 0.
		payload := []byte(`{"pong": true}`)
		fh := make([]byte, 5)
		fh[0] = streamStdout
		binary.BigEndian.PutUint32(fh[1:], uint32(len(payload)))
		c.Write(fh)
		c.Write(payload)
		ex := make([]byte, 5) // stream 2, len 4
		ex[0] = streamExit
		binary.BigEndian.PutUint32(ex[1:], 4)
		c.Write(ex)
		c.Write([]byte{0, 0, 0, 0})
	}()
	resp, err := AskHostBroker(sock, singleton("action", "ping"))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := resp.Get("pong"); v != true {
		t.Errorf("resp pong = %v", v)
	}
}

// TestProxyUpstreamMapping: a broker {error} response maps to 502.
func TestProxyUpstream502OnBrokerError(t *testing.T) {
	dir := shortDir(t)
	sock := filepath.Join(dir, "relay.sock")
	serveOnce(t, sock, func() *jsonx.OrderedMap {
		m := jsonx.NewOrderedMap()
		m.Set("error", "upstream_unreachable")
		m.Set("message", "no DNS")
		return m
	})
	res := ProxyUpstream(sock, "GET", "/foo", map[string]string{}, nil)
	if res.Status != 502 {
		t.Errorf("status = %d, want 502", res.Status)
	}
}

// TestProxyUpstreamPassthrough: a well-formed proxy response passes the
// upstream status/body through verbatim.
func TestProxyUpstreamPassthrough(t *testing.T) {
	dir := shortDir(t)
	sock := filepath.Join(dir, "relay.sock")
	serveOnce(t, sock, func() *jsonx.OrderedMap {
		m := jsonx.NewOrderedMap()
		m.Set("status", jsonx.IntValue(418))
		h := jsonx.NewOrderedMap()
		h.Set("X-Test", "yes")
		m.Set("headers", h)
		m.Set("body_b64", "aGVsbG8=") // "hello"
		return m
	})
	res := ProxyUpstream(sock, "GET", "/foo", map[string]string{}, nil)
	if res.Status != 418 {
		t.Errorf("status = %d, want 418", res.Status)
	}
	if string(res.Body) != "hello" {
		t.Errorf("body = %q, want hello", res.Body)
	}
	if res.Headers["X-Test"] != "yes" {
		t.Errorf("header X-Test = %q", res.Headers["X-Test"])
	}
}

// TestRefreshMapping: broker {error} -> 400; success -> 200.
func TestRefreshMapping(t *testing.T) {
	dir := shortDir(t)
	sockErr := filepath.Join(dir, "err.sock")
	serveOnce(t, sockErr, func() *jsonx.OrderedMap {
		m := jsonx.NewOrderedMap()
		m.Set("error", "no_refresh_token")
		return m
	})
	if res := Refresh(sockErr); res.Status != 400 {
		t.Errorf("error refresh status = %d, want 400", res.Status)
	}

	sockOK := filepath.Join(dir, "ok.sock")
	serveOnce(t, sockOK, func() *jsonx.OrderedMap {
		m := jsonx.NewOrderedMap()
		m.Set("access_token", "AT")
		m.Set("token_type", "Bearer")
		return m
	})
	res := Refresh(sockOK)
	if res.Status != 200 {
		t.Errorf("ok refresh status = %d, want 200", res.Status)
	}
	if !strings.Contains(string(res.Body), `"access_token": "AT"`) {
		t.Errorf("ok refresh body = %q", res.Body)
	}
}

// serveOnce starts a one-shot relay double that reads a framed request and
// replies with respFn()'s object framed as stdout + exit 0.
func serveOnce(t *testing.T, sock string, respFn func() *jsonx.OrderedMap) {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		hdr := make([]byte, 4)
		io.ReadFull(c, hdr)
		length := binary.BigEndian.Uint32(hdr)
		io.ReadFull(c, make([]byte, length))
		body, _ := json.Marshal(mapOf(respFn()))
		fh := make([]byte, 5)
		fh[0] = streamStdout
		binary.BigEndian.PutUint32(fh[1:], uint32(len(body)))
		c.Write(fh)
		c.Write(body)
		ex := make([]byte, 5)
		ex[0] = streamExit
		binary.BigEndian.PutUint32(ex[1:], 4)
		c.Write(ex)
		c.Write([]byte{0, 0, 0, 0})
	}()
	// Give the listener a moment.
	time.Sleep(20 * time.Millisecond)
}

// mapOf flattens an OrderedMap to a plain map for json.Marshal in the test
// double (order doesn't matter for the double's response — the terminator
// decodes it with jsonx anyway).
func mapOf(m *jsonx.OrderedMap) map[string]any {
	out := map[string]any{}
	for _, k := range m.Keys() {
		v, _ := m.Get(k)
		out[k] = plain(v)
	}
	return out
}

func plain(v any) any {
	switch t := v.(type) {
	case *jsonx.OrderedMap:
		return mapOf(t)
	default:
		// jsonx.IntValue etc. re-encode via DumpsCompact; for the double just
		// pass strings/bools through and stringify the rest.
		switch t.(type) {
		case string, bool, float64:
			return t
		default:
			s, _ := jsonx.DumpsCompact(t)
			// numeric literal -> number
			var n json.Number
			if err := json.Unmarshal([]byte(s), &n); err == nil {
				if i, err := n.Int64(); err == nil {
					return i
				}
			}
			return s
		}
	}
}

// --- macOS tcpfile: transport (issue #31) ----------------------------------

// tcpFront is a loopback TCP relay double for the macOS transport: it expects a
// leading token frame (4-byte BE len + token), records it, then behaves like a
// framed relay — reads one request frame and replies stdout {"pong":true} +
// exit 0.
type tcpFront struct {
	ln       net.Listener
	mu       sync.Mutex
	gotToken string
}

// startTCPFront starts the double on a loopback TLS listener (with a self-signed
// cert whose SAN matches paths.BrokerTLSServerName) and publishes
// "127.0.0.1:<port> <base64 cert>" to publishPath, exactly as the real relay does.
func startTCPFront(t *testing.T, publishPath string) *tcpFront {
	t.Helper()
	cert, certB64 := mintTestRelayCert(t)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(publishPath, []byte(ln.Addr().String()+" "+certB64+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &tcpFront{ln: ln}
	go f.serve()
	t.Cleanup(func() { ln.Close() })
	return f
}

// mintTestRelayCert mints a self-signed leaf mirroring the real relay's cert
// (CN/SAN = paths.BrokerTLSServerName), returning it and its base64(DER).
func mintTestRelayCert(t *testing.T) (tls.Certificate, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: paths.BrokerTLSServerName},
		DNSNames:              []string{paths.BrokerTLSServerName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, base64.StdEncoding.EncodeToString(der)
}

func (f *tcpFront) serve() {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(c)
	}
}

func (f *tcpFront) handle(c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(c, hdr); err != nil {
		return
	}
	tok := make([]byte, binary.BigEndian.Uint32(hdr))
	if _, err := io.ReadFull(c, tok); err != nil {
		return
	}
	f.mu.Lock()
	f.gotToken = string(tok)
	f.mu.Unlock()
	rhdr := make([]byte, 4)
	if _, err := io.ReadFull(c, rhdr); err != nil {
		return
	}
	io.ReadFull(c, make([]byte, binary.BigEndian.Uint32(rhdr)))
	payload := []byte(`{"pong": true}`)
	fh := make([]byte, 5)
	fh[0] = streamStdout
	binary.BigEndian.PutUint32(fh[1:], uint32(len(payload)))
	c.Write(fh)
	c.Write(payload)
	ex := make([]byte, 5)
	ex[0] = streamExit
	binary.BigEndian.PutUint32(ex[1:], 4)
	c.Write(ex)
	c.Write([]byte{0, 0, 0, 0})
}

func (f *tcpFront) token() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gotToken
}

// TestAskHostBrokerTCPFileSuccess: a "tcpfile:" address reads the relay's
// published host:port, dials it, presents the per-jail token as the leading
// frame, and round-trips a framed request.
func TestAskHostBrokerTCPFileSuccess(t *testing.T) {
	pub := filepath.Join(shortDir(t), "relay.tcp")
	const token = "per-jail-token-xyz"
	t.Setenv(paths.BrokerTokenEnv, token)
	front := startTCPFront(t, pub)

	resp, err := AskHostBroker(paths.BrokerTCPFileSentinel+pub, singleton("action", "ping"))
	if err != nil {
		t.Fatalf("tcpfile dial failed: %v", err)
	}
	if v, _ := resp.Get("pong"); v != true {
		t.Errorf("resp pong = %v", v)
	}
	if front.token() != token {
		t.Errorf("relay saw token %q, want %q (token frame must precede the request)", front.token(), token)
	}
}

// TestAskHostBrokerTCPFileMissingFileRelayLayer: a "tcpfile:" address whose file
// doesn't exist yet (relay not up) must attribute to the relay layer (ENOENT),
// not a generic error — so the jail log says the right thing.
func TestAskHostBrokerTCPFileMissingFileRelayLayer(t *testing.T) {
	missing := filepath.Join(shortDir(t), "nope.tcp")
	_, err := AskHostBroker(paths.BrokerTCPFileSentinel+missing, singleton("action", "ping"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.HasPrefix(err.Error(), "relay unreachable") {
		t.Errorf("err = %q, want relay-layer prefix for a missing endpoint file", err.Error())
	}
}
