package svcendpoint

import (
	"bytes"
	"encoding/base64"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// sampleEndpoint returns a complete, usable endpoint (a real cert, so Probe's
// x509 parse succeeds).
func sampleEndpoint(t *testing.T) Endpoint {
	t.Helper()
	_, der, err := mintCert()
	if err != nil {
		t.Fatal(err)
	}
	tok, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	return Endpoint{HostPort: "host.containers.internal:41234", CertDER: der, Token: tok}
}

func TestEndpointFormatParseRoundTrip(t *testing.T) {
	ep := sampleEndpoint(t)
	line := ep.Format()
	if !strings.HasSuffix(line, "\n") {
		t.Error("Format did not terminate the line")
	}
	if n := strings.Count(strings.TrimSpace(line), " "); n != 2 {
		t.Errorf("Format produced %d separators, want 2 (three fields)", n)
	}
	got, err := Parse(line)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.HostPort != ep.HostPort || got.Token != ep.Token || !bytes.Equal(got.CertDER, ep.CertDER) {
		t.Errorf("round trip changed the endpoint:\n got %+v\nwant %+v", got.HostPort, ep.HostPort)
	}
}

// TestTwoFieldEndpointIsMalformed pins the exactly-3 decision. #32's format was
// two fields and its parser tolerated ">= 2"; that tolerance was forward
// compatibility for the token, which has now arrived. Keeping it would make an
// older or truncated publication parse as "no token" — which either fails later
// with a confusing error or, if anyone writes `if token != ""`, authenticates
// nothing.
func TestTwoFieldEndpointIsMalformed(t *testing.T) {
	ep := sampleEndpoint(t)
	twoField := ep.HostPort + " " + base64.StdEncoding.EncodeToString(ep.CertDER) + "\n"

	if _, err := Parse(twoField); !errors.Is(err, ErrEndpointMalformed) {
		t.Errorf("Parse(2-field) error = %v, want ErrEndpointMalformed", err)
	}

	path := filepath.Join(privateDir(t), "old.endpoint")
	if err := os.WriteFile(path, []byte(twoField), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Read(path)
	if !errors.Is(err, ErrEndpointMalformed) {
		t.Errorf("Read(2-field) error = %v, want ErrEndpointMalformed", err)
	}
	if errors.Is(err, ErrEndpointMissing) {
		t.Error("a file that EXISTS was reported as missing")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name the offending file", err)
	}
	if Probe(path) {
		t.Error("Probe accepted a 2-field endpoint file")
	}
}

// TestMissingEndpointPreservesENOENT: the OAuth terminator's frozen two-layer
// attribution gates on the ERRNO, so a missing endpoint must keep reading as
// ENOENT while ALSO being identifiable as this package's own sentinel.
func TestMissingEndpointPreservesENOENT(t *testing.T) {
	path := filepath.Join(privateDir(t), "absent.endpoint")

	_, err := Read(path)
	if err == nil {
		t.Fatal("Read succeeded on a missing file")
	}
	if !errors.Is(err, ErrEndpointMissing) {
		t.Errorf("errors.Is(err, ErrEndpointMissing) = false for %v", err)
	}
	if !errors.Is(err, syscall.ENOENT) {
		t.Errorf("errors.Is(err, syscall.ENOENT) = false for %v — the relay-layer "+
			"attribution in internal/oauthterminator depends on it", err)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("errors.Is(err, os.ErrNotExist) = false for %v", err)
	}

	// Dial must carry the same attribution through, unchanged.
	_, derr := Dial(path, 0)
	if !errors.Is(derr, ErrEndpointMissing) || !errors.Is(derr, syscall.ENOENT) {
		t.Errorf("Dial on a missing endpoint = %v, want both ErrEndpointMissing and ENOENT", derr)
	}
	if Probe(path) {
		t.Error("Probe true for a missing file")
	}
}

// TestEndpointFilePublishedMode0600: the file carries a bearer token, so its mode
// is load-bearing rather than cosmetic.
func TestEndpointFilePublishedMode0600(t *testing.T) {
	path := filepath.Join(privateDir(t), "mode.endpoint")
	if err := Publish(path, sampleEndpoint(t)); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("published mode = %#o, want 0600 — this file is a CREDENTIAL", perm)
	}

	// Republishing over an existing file must not inherit a widened mode.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Publish(path, sampleEndpoint(t)); err != nil {
		t.Fatal(err)
	}
	st, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode after republish = %#o, want 0600", perm)
	}
}

// TestPublishDirIsPrivateAndVerified: the publication directory sits at a fully
// deterministic path under a world-writable /tmp and now holds a credential.
// MkdirAll succeeds on an already-existing foreign directory without changing its
// owner or mode, so the mode/owner/symlink checks — and FAILING CLOSED on them —
// are what stop us publishing a credential into somebody else's directory.
func TestPublishDirIsPrivateAndVerified(t *testing.T) {
	t.Run("creates 0700 when absent", func(t *testing.T) {
		dir := filepath.Join(privateDir(t), "fresh")
		if err := Publish(filepath.Join(dir, "svc.endpoint"), sampleEndpoint(t)); err != nil {
			t.Fatal(err)
		}
		st, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if perm := st.Mode().Perm(); perm != 0o700 {
			t.Errorf("created publish dir mode = %#o, want 0700", perm)
		}
	})

	t.Run("group or world accessible fails closed", func(t *testing.T) {
		for _, mode := range []os.FileMode{0o755, 0o750, 0o701, 0o770} {
			dir := filepath.Join(privateDir(t), "open")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(dir, mode); err != nil {
				t.Fatal(err)
			}
			err := Publish(filepath.Join(dir, "svc.endpoint"), sampleEndpoint(t))
			if err == nil {
				t.Errorf("Publish into a %#o directory succeeded", mode)
				continue
			}
			if !strings.Contains(err.Error(), "group/world-accessible") {
				t.Errorf("error for %#o = %v, want it to name the mode problem", mode, err)
			}
		}
	})

	t.Run("symlinked dir fails closed", func(t *testing.T) {
		base := privateDir(t)
		real := filepath.Join(base, "real")
		if err := os.Mkdir(real, 0o700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(base, "link")
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		err := Publish(filepath.Join(link, "svc.endpoint"), sampleEndpoint(t))
		if err == nil {
			t.Fatal("Publish through a symlinked directory succeeded")
		}
		if !strings.Contains(err.Error(), "symlink") {
			t.Errorf("error = %v, want it to name the symlink", err)
		}
		if _, serr := os.Stat(filepath.Join(real, "svc.endpoint")); serr == nil {
			t.Error("a credential was written through the symlink")
		}
	})

	t.Run("foreign-owned dir fails closed", func(t *testing.T) {
		if os.Getuid() != 0 {
			t.Skip("needs root to chown a directory to another uid")
		}
		dir := filepath.Join(privateDir(t), "theirs")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(dir, 65534, 65534); err != nil {
			t.Skipf("chown unavailable: %v", err)
		}
		t.Cleanup(func() { _ = os.Chown(dir, 0, 0) })
		err := Publish(filepath.Join(dir, "svc.endpoint"), sampleEndpoint(t))
		if err == nil {
			t.Fatal("Publish into a foreign-owned directory succeeded")
		}
		if !strings.Contains(err.Error(), "owned by uid") {
			t.Errorf("error = %v, want it to name the owner problem", err)
		}
	})

	t.Run("listen refuses the same directories", func(t *testing.T) {
		dir := filepath.Join(privateDir(t), "open")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := Listen(filepath.Join(dir, "svc.endpoint"), "127.0.0.1"); err == nil {
			t.Error("Listen bound a port for a publication it must refuse to write")
		}
	})
}

// TestPublishIsAtomic hammers Read across republishes. A client re-reads this file
// on every dial, so a torn read would hand back a truncated token — os.WriteFile
// races that reader, temp-file-plus-rename does not.
func TestPublishIsAtomic(t *testing.T) {
	path := filepath.Join(privateDir(t), "atomic.endpoint")
	base := sampleEndpoint(t)
	if err := Publish(path, base); err != nil {
		t.Fatal(err)
	}

	const rounds = 200
	tokens := map[string]bool{base.Token: true}
	published := make([]Endpoint, 0, rounds)
	for i := 0; i < rounds; i++ {
		ep := base
		tok, err := NewToken()
		if err != nil {
			t.Fatal(err)
		}
		ep.Token = tok
		tokens[tok] = true
		published = append(published, ep)
	}

	done := make(chan struct{})
	type failure struct{ msg string }
	fails := make(chan failure, 16)
	go func() {
		defer close(done)
		for _, ep := range published {
			if err := Publish(path, ep); err != nil {
				fails <- failure{"Publish: " + err.Error()}
				return
			}
		}
	}()

	reads := 0
	for {
		select {
		case <-done:
			if reads == 0 {
				t.Fatal("the reader never ran; the test proves nothing")
			}
			close(fails)
			for f := range fails {
				t.Error(f.msg)
			}
			return
		default:
		}
		ep, err := Read(path)
		reads++
		if err != nil {
			t.Fatalf("Read during republish (%d reads in): %v — a torn or missing file", reads, err)
		}
		if !tokens[ep.Token] {
			t.Fatalf("Read returned a token that was never published (%d chars) — torn read", len(ep.Token))
		}
		if !IsToken(ep.Token) {
			t.Fatalf("Read returned a malformed token (%d chars) — torn read", len(ep.Token))
		}
	}
}

// TestProbeRejectsIncompleteEndpoint: EXISTENCE IS NOT HEALTH. Every one of these
// files exists, and none of them names a usable listener — so a wait or health
// predicate built on os.Stat would report each as ready forever.
func TestProbeRejectsIncompleteEndpoint(t *testing.T) {
	good := sampleEndpoint(t)
	goodCert := base64.StdEncoding.EncodeToString(good.CertDER)

	cases := []struct {
		name    string
		content string
	}{
		{"empty", ""},
		{"whitespace only", "   \n\t\n"},
		{"one field", good.HostPort + "\n"},
		{"two fields", good.HostPort + " " + goodCert + "\n"},
		{"four fields", good.HostPort + " " + goodCert + " " + good.Token + " extra\n"},
		{"cert not base64", good.HostPort + " !!!not-base64!!! " + good.Token + "\n"},
		{"cert not a certificate", good.HostPort + " " + base64.StdEncoding.EncodeToString([]byte("nope")) + " " + good.Token + "\n"},
		{"host without port", "justahost " + goodCert + " " + good.Token + "\n"},
		{"token not hex", good.HostPort + " " + goodCert + " NOT-A-TOKEN\n"},
		{"token too short", good.HostPort + " " + goodCert + " abcdef\n"},
		{"garbage", "\x00\x01\x02\n"},
	}
	dir := privateDir(t)
	for _, tc := range cases {
		path := filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "-")+".endpoint")
		if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
			t.Fatal(err)
		}
		if Probe(path) {
			t.Errorf("Probe(%s) = true, want false", tc.name)
		}
	}

	full := filepath.Join(dir, "good.endpoint")
	if err := Publish(full, good); err != nil {
		t.Fatal(err)
	}
	if !Probe(full) {
		t.Error("Probe rejected a complete, published endpoint")
	}
}

// TestParsePlainAcceptsExactlyTheBareAddress pins the plain format's
// structural rule (see ParsePlain): one host:port field, and nothing else
// parses — a credential triple must never read as a plain endpoint or a
// truncated write of one could pass a plain probe.
func TestParsePlainAcceptsExactlyTheBareAddress(t *testing.T) {
	addr, err := ParsePlain("127.0.0.1:8214\n")
	if err != nil || addr != "127.0.0.1:8214" {
		t.Errorf("ParsePlain(bare address) = %q, %v, want the address", addr, err)
	}
	for name, bad := range map[string]string{
		"empty":           "",
		"no port":         "127.0.0.1",
		"two fields":      "127.0.0.1:8214 extra",
		"a TLS triple":    "127.0.0.1:8214 " + strings.Repeat("A", 16) + " deadbeef",
		"whitespace only": " \n\t ",
	} {
		if _, err := ParsePlain(bad); !errors.Is(err, ErrEndpointMalformed) {
			t.Errorf("ParsePlain(%s) = %v, want ErrEndpointMalformed", name, err)
		}
	}
}

// TestReadPlainAndDialPlain: the witness's plain path — read through the SAME
// stat gate as Read (a fifo at the path is refused, never opened), and a dial
// that reaches a live plain listener and fails (with the dial error, not a
// parse error) on a dead port.
func TestReadPlainAndDialPlain(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close() // connect-then-close is the whole contract
		}
	}()

	path := filepath.Join(t.TempDir(), "plain.endpoint")
	if err := os.WriteFile(path, []byte(ln.Addr().String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	addr, err := ReadPlain(path)
	if err != nil || addr != ln.Addr().String() {
		t.Fatalf("ReadPlain = %q, %v, want the published address", addr, err)
	}
	conn, err := DialPlain(path, time.Second)
	if err != nil {
		t.Fatalf("DialPlain against a live listener: %v", err)
	}
	_ = conn.Close()

	// A dead published port is a dial failure carrying no malformed-file claim.
	if err := os.WriteFile(path, []byte("127.0.0.1:1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := DialPlain(path, 200*time.Millisecond); err == nil ||
		errors.Is(err, ErrEndpointMalformed) {
		t.Errorf("DialPlain against a dead port = %v, want a plain dial error", err)
	}

	// A credential triple at the path is not a plain endpoint — exactly-three
	// and exactly-one refuse each other by construction.
	if err := os.WriteFile(path, []byte("h:1 "+strings.Repeat("Q", 8)+" tok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPlain(path); !errors.Is(err, ErrEndpointMalformed) {
		t.Errorf("ReadPlain over a credential triple = %v, want ErrEndpointMalformed", err)
	}
}
