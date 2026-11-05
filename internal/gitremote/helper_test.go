package gitremote

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// serve starts an in-test loopback-TLS endpoint running handler and returns its endpoint file.
func serve(t *testing.T, handler hostservice.Handler) string {
	t.Helper()
	t.Setenv(svcendpoint.AdvertiseHostEnv, "127.0.0.1")
	dir, err := os.MkdirTemp("/tmp", "yj-gitremote-test-")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := filepath.Join(dir, "git-broker.endpoint")
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = hostservice.ServeEndpoint(handler, endpoint, stop)
	}()
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(endpoint); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Cleanup(func() {
		close(stop)
		<-done
		_ = os.RemoveAll(dir)
	})
	return endpoint
}

func envOf(vars map[string]string, stdin string, stdout, stderr *bytes.Buffer) Env {
	return Env{
		Getenv: func(k string) string { return vars[k] },
		Stdin:  strings.NewReader(stdin),
		Stdout: stdout,
		Stderr: stderr,
	}
}

func TestHelperAdvertisesConnectOnly(t *testing.T) {
	var out, errb bytes.Buffer
	rc := Run([]string{"origin", "ssh://git@github.com/o/r"}, envOf(nil, "capabilities\n", &out, &errb))
	if rc != 0 || out.String() != "connect\n\n" {
		t.Fatalf("rc %d stdout %q stderr %q", rc, out.String(), errb.String())
	}
}

func TestHelperConnectRelaysBothWays(t *testing.T) {
	gotRequest := make(chan string, 1)
	endpoint := serve(t, func(s *hostservice.Session) {
		var parts []string
		for _, k := range s.Request.Keys() {
			v, _ := s.Request.Get(k)
			parts = append(parts, fmt.Sprintf("%q:%q", k, v))
		}
		gotRequest <- "{" + strings.Join(parts, ",") + "}"
		for {
			f, err := frameproto.ReadFrame(s.Conn())
			if err != nil {
				s.Exit(1)
				return
			}
			if len(f.Payload) == 0 {
				s.Exit(0)
				return
			}
			s.StdoutBytes(append([]byte("echo:"), f.Payload...))
		}
	})
	var out, errb bytes.Buffer
	vars := map[string]string{EndpointEnv: endpoint, "GIT_PROTOCOL": "version=2"}
	rc := Run([]string{"--", "origin", "ssh://git@github.com/o/r"},
		envOf(vars, "capabilities\nconnect git-upload-pack\nhello", &out, &errb))
	if rc != 0 {
		t.Fatalf("rc %d stderr %q", rc, errb.String())
	}
	want := `{"service":"git-upload-pack","url":"ssh://git@github.com/o/r","protocol":"version=2"}`
	if got := <-gotRequest; got != want {
		t.Fatalf("request frame %s, want %s", got, want)
	}
	if out.String() != "connect\n\n"+"\n"+"echo:hello" {
		t.Fatalf("stdout %q", out.String())
	}
}

func TestHelperRefusalHasNoConnectedLine(t *testing.T) {
	endpoint := serve(t, func(s *hostservice.Session) {
		s.Stderr("not in this workspace's approved scope\n")
		s.Exit(77)
	})
	var out, errb bytes.Buffer
	rc := Run([]string{"origin", "git@other.test:o/r.git"},
		envOf(map[string]string{EndpointEnv: endpoint}, "capabilities\nconnect git-receive-pack\n", &out, &errb))
	if rc != 77 {
		t.Fatalf("rc %d stderr %q", rc, errb.String())
	}
	if out.String() != "connect\n\n" {
		t.Fatalf("stdout after the capabilities %q", out.String())
	}
	if !strings.Contains(errb.String(), "not in this workspace's approved scope") {
		t.Fatalf("stderr %q", errb.String())
	}
}

func TestHelperNoEndpointNamesTheSwitch(t *testing.T) {
	if EndpointEnv != "YOLO_SERVICE_GIT_BROKER_ENDPOINT" {
		t.Fatalf("EndpointEnv %q", EndpointEnv)
	}
	var out, errb bytes.Buffer
	rc := Run([]string{"origin", "git@github.com:o/r.git"},
		envOf(map[string]string{"YOLO_HOST_DIR": "/home/u/app"}, "capabilities\nconnect git-upload-pack\n", &out, &errb))
	if rc != 69 {
		t.Fatalf("rc %d", rc)
	}
	if !strings.Contains(errb.String(), "run `yolo check` on the host,\n  in /home/u/app,") {
		t.Fatalf("stderr %q", errb.String())
	}
	if out.String() != "connect\n\n" {
		t.Fatalf("stdout %q", out.String())
	}
}

// countingEndpoint serves an endpoint that counts the requests reaching it and refuses each.
func countingEndpoint(t *testing.T) (string, func() int) {
	t.Helper()
	dialed := make(chan struct{}, 16)
	endpoint := serve(t, func(s *hostservice.Session) {
		dialed <- struct{}{}
		s.Exit(1)
	})
	return endpoint, func() int { return len(dialed) }
}

// Only upload-pack and receive-pack cross; anything else is refused with 64, before the
// endpoint is dialed even when one is published.
func TestHelperRefusesOtherServicesAndCommands(t *testing.T) {
	endpoint, dials := countingEndpoint(t)
	for _, in := range []string{"connect git-upload-archive\n", "list\n", "stateless-connect git-upload-pack\n"} {
		var out, errb bytes.Buffer
		rc := Run([]string{"origin", "ssh://git@github.com/o/r"},
			envOf(map[string]string{EndpointEnv: endpoint}, in, &out, &errb))
		if rc != 64 || out.Len() != 0 || !strings.Contains(errb.String(), "git-remote-yolo") {
			t.Errorf("%q: rc %d stdout %q stderr %q", in, rc, out.String(), errb.String())
		}
	}
	if n := dials(); n != 0 {
		t.Fatalf("the endpoint was dialed %d times for refused commands", n)
	}
}

// A remote with no URL reaches the helper as its name alone; the name is never sent as a URL.
func TestHelperRemoteWithNoURLIsRefused(t *testing.T) {
	endpoint, dials := countingEndpoint(t)
	var out, errb bytes.Buffer
	rc := Run([]string{"--", "origin"},
		envOf(map[string]string{EndpointEnv: endpoint}, "capabilities\nconnect git-upload-pack\n", &out, &errb))
	if rc != 64 || out.String() != "connect\n\n" {
		t.Fatalf("rc %d stdout %q stderr %q", rc, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), `the remote "origin" has no URL`) ||
		!strings.Contains(errb.String(), "git remote set-url origin <url>") {
		t.Fatalf("stderr %q", errb.String())
	}
	if n := dials(); n != 0 {
		t.Fatalf("the endpoint was dialed %d times", n)
	}
}

func TestForwardAccessRejectsMalformedStatusReplies(t *testing.T) {
	const valid = `{"schema":"yolo.access/1","source":"git","state":"running","read":"granted","next":"","repositories":["fixture.test/o/r"],"scope_grants":[{"kind":"read","repo":"fixture.test/o/r"}],"requests":[{"id":"request-1","state":"pending","filed":"2026-01-01T00:00:00Z","expires":"2026-01-01T01:00:00Z"}],"grants":[{"id":"grant-1","kind":"timed","set":"read","repo":"fixture.test/o/r","resource":"refs/heads/main","expires":"2026-01-01T01:00:00Z","uses":1}],"working_set":[{"id":"work-1","repo":"fixture.test/o/r","kind":"branch","resource":"refs/heads/main","by":"human"}],"limits":{"pending":1,"pending_cap":3}}`
	if !validGitStatus([]byte(valid)) {
		t.Fatal("valid status with omitted optional request fields rejected")
	}
	withOptionalFields := strings.Replace(valid,
		`"expires":"2026-01-01T01:00:00Z"}],`,
		`"expires":"2026-01-01T01:00:00Z","answer":"allowed","used":true}],`, 1)
	if !validGitStatus([]byte(withOptionalFields)) {
		t.Fatal("valid status with emitted optional request fields rejected")
	}
	for _, read := range []string{"", "granted", "partial", "not-granted"} {
		body := strings.Replace(strings.Replace(valid, `"state":"running"`, `"state":"unreachable"`, 1),
			`"read":"granted"`, `"read":"`+read+`"`, 1)
		if !validGitStatus([]byte(body)) {
			t.Errorf("unreachable status with producer read value %q rejected", read)
		}
	}
	cases := []struct {
		name, body string
	}{
		{"state-null", strings.Replace(valid, `"state":"running"`, `"state":null`, 1)},
		{"state-unsupported", strings.Replace(valid, `"state":"running"`, `"state":"ready"`, 1)},
		{"unreachable-read-unsupported", strings.Replace(strings.Replace(valid, `"state":"running"`, `"state":"unreachable"`, 1), `"read":"granted"`, `"read":"unknown"`, 1)},
		{"read-wrong-type", strings.Replace(valid, `"read":"granted"`, `"read":[]`, 1)},
		{"repositories-null", strings.Replace(valid, `"repositories":["fixture.test/o/r"]`, `"repositories":null`, 1)},
		{"scope-grants-wrong-type", strings.Replace(valid, `"scope_grants":[{"kind":"read","repo":"fixture.test/o/r"}]`, `"scope_grants":{}`, 1)},
		{"request-unsupported-state", strings.Replace(valid, `"state":"pending"`, `"state":"ready"`, 1)},
		{"request-missing-required-values", strings.Replace(valid, `{"id":"request-1","state":"pending","filed":"2026-01-01T00:00:00Z","expires":"2026-01-01T01:00:00Z"}`, `{}`, 1)},
		{"request-used-wrong-type", strings.Replace(valid, `"expires":"2026-01-01T01:00:00Z"}]`, `"expires":"2026-01-01T01:00:00Z","used":"yes"}]`, 1)},
		{"request-used-null", strings.Replace(valid, `"expires":"2026-01-01T01:00:00Z"}]`, `"expires":"2026-01-01T01:00:00Z","used":null}]`, 1)},
		{"request-answer-wrong-type", strings.Replace(valid, `"expires":"2026-01-01T01:00:00Z"}]`, `"expires":"2026-01-01T01:00:00Z","answer":[]}]`, 1)},
		{"request-answer-null", strings.Replace(valid, `"expires":"2026-01-01T01:00:00Z"}]`, `"expires":"2026-01-01T01:00:00Z","answer":null}]`, 1)},
		{"grant-missing-required-values", strings.Replace(valid, `{"id":"grant-1","kind":"timed","set":"read","repo":"fixture.test/o/r","resource":"refs/heads/main","expires":"2026-01-01T01:00:00Z","uses":1}`, `{}`, 1)},
		{"working-set-missing-required-values", strings.Replace(valid, `{"id":"work-1","repo":"fixture.test/o/r","kind":"branch","resource":"refs/heads/main","by":"human"}`, `{}`, 1)},
		{"limits-null", strings.Replace(valid, `"limits":{"pending":1,"pending_cap":3}`, `"limits":null`, 1)},
		{"limits-pending-wrong-type", strings.Replace(valid, `"pending":1`, `"pending":"1"`, 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := serve(t, func(s *hostservice.Session) {
				s.StdoutBytes([]byte(tc.body))
				s.Exit(0)
			})
			var stderr bytes.Buffer
			body, code := ForwardAccess("status", nil, Env{Getenv: func(k string) string {
				if k == EndpointEnv {
					return endpoint
				}
				return ""
			}, Stderr: &stderr})
			if code != exitUnavailable || body != nil || !strings.Contains(stderr.String(), "unsupported status") {
				t.Fatalf("malformed status accepted: code=%d body=%s stderr=%s", code, body, stderr.String())
			}
		})
	}
}

func TestForwardAccessStatusDeadlineIncludesTokenAck(t *testing.T) {
	endpoint, accepted := serveWithStalledAck(t)
	started := time.Now()
	var stderr bytes.Buffer
	body, code := ForwardAccess("status", nil, Env{Getenv: func(k string) string {
		if k == EndpointEnv {
			return endpoint
		}
		return ""
	}, Stderr: &stderr})
	elapsed := time.Since(started)
	select {
	case <-accepted:
	default:
		t.Fatal("controlled TLS peer did not receive the token before status returned")
	}
	if code != exitUnavailable || body != nil || elapsed >= statusSessionTimeout+time.Second {
		t.Fatalf("withheld token ack was not bounded by the status deadline: code=%d body=%s elapsed=%s stderr=%s", code, body, elapsed, stderr.String())
	}
}

func serveWithStalledAck(t *testing.T) (string, <-chan struct{}) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: svcendpoint.ServerName},
		DNSNames: []string{svcendpoint.ServerName}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := tls.NewListener(raw, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0o700); err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	endpoint := filepath.Join(dir, "git-broker.endpoint")
	if err := svcendpoint.Publish(endpoint, svcendpoint.Endpoint{HostPort: raw.Addr().String(), CertDER: der, Token: strings.Repeat("a", 64)}); err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	accepted := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		tlsConn := conn.(*tls.Conn)
		if err := tlsConn.Handshake(); err != nil {
			return
		}
		var header [4]byte
		if _, err := io.ReadFull(tlsConn, header[:]); err != nil || binary.BigEndian.Uint32(header[:]) != 64 {
			return
		}
		token := make([]byte, 64)
		if _, err := io.ReadFull(tlsConn, token); err != nil || string(token) != strings.Repeat("a", 64) {
			return
		}
		accepted <- struct{}{}
		_, _ = io.Copy(io.Discard, tlsConn)
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
	})
	return endpoint, accepted
}

func TestForwardAccessBoundsStatusSessionWhenEndpointIsSilent(t *testing.T) {
	const testBound = 4 * time.Second
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	endpoint := serve(t, func(s *hostservice.Session) {
		<-release
		s.StdoutBytes([]byte(`{"schema":"yolo.access/1","source":"git","state":"running","read":"granted","next":"","repositories":[],"scope_grants":[],"requests":[],"grants":[],"working_set":[],"limits":{"pending":0,"pending_cap":3}}`))
		s.Exit(0)
	})
	t.Cleanup(unblock)
	type result struct {
		code    int
		body    []byte
		stderr  string
		elapsed time.Duration
	}
	done := make(chan result, 1)
	go func() {
		started := time.Now()
		var stderr bytes.Buffer
		body, code := ForwardAccess("status", nil, Env{Getenv: func(k string) string {
			if k == EndpointEnv {
				return endpoint
			}
			return ""
		}, Stderr: &stderr})
		done <- result{code, body, stderr.String(), time.Since(started)}
	}()
	select {
	case got := <-done:
		if got.code != exitUnavailable || got.body != nil || got.elapsed >= testBound {
			t.Fatalf("silent endpoint was not bounded refusal: code=%d body=%s elapsed=%s stderr=%s", got.code, got.body, got.elapsed, got.stderr)
		}
	case <-time.After(testBound):
		unblock()
		got := <-done
		if got.code != exitUnavailable || got.elapsed < testBound {
			t.Fatalf("silent endpoint result after bound: code=%d elapsed=%s stderr=%s", got.code, got.elapsed, got.stderr)
		}
		t.Fatalf("silent endpoint exceeded bounded status session (%s)", got.elapsed)
	}
}

func TestForwardAccessCapsCombinedStatusStdoutAndStderr(t *testing.T) {
	endpoint := serve(t, func(s *hostservice.Session) {
		body := `{"schema":"yolo.access/1","source":"git","state":"running","read":"granted","next":"` + strings.Repeat("n", 700<<10) + `","repositories":[],"scope_grants":[],"requests":[],"grants":[],"working_set":[],"limits":{"pending":0,"pending_cap":3}}`
		s.Stderr(strings.Repeat("x", 400<<10))
		s.StdoutBytes([]byte(body))
		s.Exit(0)
	})
	var stderr bytes.Buffer
	body, code := ForwardAccess("status", nil, Env{Getenv: func(k string) string {
		if k == EndpointEnv {
			return endpoint
		}
		return ""
	}, Stderr: &stderr})
	if code != exitUnavailable || body != nil {
		t.Fatalf("combined response exceeding 1 MiB was accepted: code=%d body bytes=%d stderr bytes=%d", code, len(body), stderr.Len())
	}
}

// A blank line from git ends the helper cleanly.
func TestHelperBlankLineEnds(t *testing.T) {
	var out, errb bytes.Buffer
	if rc := Run(nil, envOf(nil, "capabilities\n\n", &out, &errb)); rc != 0 || out.String() != "connect\n\n" {
		t.Fatalf("rc %d stdout %q stderr %q", rc, out.String(), errb.String())
	}
}
