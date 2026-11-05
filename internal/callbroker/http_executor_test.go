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
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

func fixturePinnedHTTPServer(t *testing.T, handler http.Handler, handshakeGate ...func()) (*httptest.Server, httpExecutor, string) {
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
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{encoded}, PrivateKey: key}}}
	if len(handshakeGate) != 0 && handshakeGate[0] != nil {
		server.TLS.GetCertificate = func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			handshakeGate[0]()
			return &server.TLS.Certificates[0], nil
		}
	}
	server.StartTLS()
	t.Cleanup(server.Close)
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
