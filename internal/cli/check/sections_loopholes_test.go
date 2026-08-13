package check

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// privateDir returns a 0700 dir. t.TempDir() creates 0755, which svcendpoint
// correctly REFUSES to publish a credential into.
func privateDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "svc")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func probeOnce(t *testing.T, endpointPath string) (*reporter, string) {
	t.Helper()
	var buf bytes.Buffer
	r := newReporter(&buf, false)
	o := &Options{}
	fillDefaults(o)
	o.checkLoopbackTLSService(r, "loophole svc @ jail", endpointPath, "svc")
	return r, buf.String()
}

// TestCheckLoopbackTLSServiceNamesTheLayer: the host-side prober must distinguish
// the three faults, because they have three different fixes.
//
// The prober also has to AUTHENTICATE, not merely stat a path — otherwise it reports
// a dead daemon as healthy. That it can authenticate at all is a consequence of
// putting the token in the endpoint file: the prober reads the same 0600 file as the
// same uid that published it.
func TestCheckLoopbackTLSServiceNamesTheLayer(t *testing.T) {
	dir := privateDir(t)

	t.Run("missing", func(t *testing.T) {
		r, out := probeOnce(t, filepath.Join(dir, "absent.endpoint"))
		if r.failed != 1 || !strings.Contains(out, "no endpoint published") {
			t.Errorf("failed=%d out=%q", r.failed, out)
		}
	})

	t.Run("incomplete", func(t *testing.T) {
		p := filepath.Join(dir, "partial.endpoint")
		// Two fields: an older publication, and also what a torn write looks like.
		if err := os.WriteFile(p, []byte("127.0.0.1:1 Y29zdA==\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		r, out := probeOnce(t, p)
		if r.failed != 1 || !strings.Contains(out, "endpoint file incomplete") {
			t.Errorf("failed=%d out=%q", r.failed, out)
		}
	})

	t.Run("complete but dead", func(t *testing.T) {
		p := filepath.Join(dir, "dead.endpoint")
		ln, err := svcendpoint.Listen(p, "")
		if err != nil {
			t.Fatal(err)
		}
		ep, err := svcendpoint.Read(p)
		if err != nil {
			t.Fatal(err)
		}
		_ = ln.Close() // Close unlinks; republish the same bytes as a crashed daemon would leave.
		if err := svcendpoint.Publish(p, ep); err != nil {
			t.Fatal(err)
		}
		r, out := probeOnce(t, p)
		if r.failed != 1 || !strings.Contains(out, "listener unreachable") {
			t.Errorf("a complete file naming a DEAD listener passed: failed=%d out=%q", r.failed, out)
		}
	})

	t.Run("auth rejected", func(t *testing.T) {
		p := filepath.Join(dir, "live.endpoint")
		ln, err := svcendpoint.Listen(p, "")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		ep, err := svcendpoint.Read(p)
		if err != nil {
			t.Fatal(err)
		}
		ep.Token = strings.Repeat("a", len(ep.Token))
		bad := filepath.Join(dir, "wrongtoken.endpoint")
		if err := svcendpoint.Publish(bad, ep); err != nil {
			t.Fatal(err)
		}
		r, out := probeOnce(t, bad)
		if r.failed != 1 || !strings.Contains(out, "rejected the token") {
			t.Errorf("a wrong token was not attributed to auth: failed=%d out=%q", r.failed, out)
		}
	})

	t.Run("healthy", func(t *testing.T) {
		p := filepath.Join(dir, "ok.endpoint")
		// The DEFAULT advertise host on purpose: the gateway name, exactly as a real
		// daemon publishes it. DialLocal keeping the port and substituting 127.0.0.1
		// is the whole reason a host-side prober works at all, so a test that
		// published 127.0.0.1 would prove nothing.
		ln, err := svcendpoint.Listen(p, "")
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
				_ = c.Close()
			}
		}()
		r, out := probeOnce(t, p)
		if r.failed != 0 || r.passed != 1 || !strings.Contains(out, "endpoint accepting") {
			t.Errorf("a live listener did not pass: passed=%d failed=%d out=%q", r.passed, r.failed, out)
		}
	})
}

// brokerRelayProbeOnce runs the broker-relay probe with the in-jail visibility
// exec stubbed out (rt/cname empty => the tri-state probe returns unknown, which
// the caller treats as "don't second-guess the host-side answer").
func brokerRelayProbeOnce(t *testing.T, endpointPath string) (*reporter, string) {
	t.Helper()
	var buf bytes.Buffer
	r := newReporter(&buf, false)
	o := &Options{}
	fillDefaults(o)
	o.checkBrokerRelay(r, "loophole claude-oauth-broker @ jail", endpointPath, "", "")
	return r, buf.String()
}

// TestCheckBrokerRelayProbesTheHopTheJailUses: the relay probe must go through the
// endpoint file — pin, token, then ping — not through the relay's own socket.
//
// That socket is host-only now, so probing it would test a path no jail travels: it
// can be perfectly healthy while the jail's half is unpublished, stale or
// mismatched, which is exactly the outage this probe exists to name. The probe
// authenticates as the same uid that published the file, which is possible only
// because the token lives in the file rather than in the jail's environment.
func TestCheckBrokerRelayProbesTheHopTheJailUses(t *testing.T) {
	dir := privateDir(t)

	t.Run("endpoint missing", func(t *testing.T) {
		r, out := brokerRelayProbeOnce(t, filepath.Join(dir, "absent.endpoint"))
		if r.failed != 1 || !strings.Contains(out, "relay endpoint missing") {
			t.Errorf("failed=%d out=%q", r.failed, out)
		}
	})

	t.Run("endpoint incomplete", func(t *testing.T) {
		p := filepath.Join(dir, "relay-partial.endpoint")
		if err := os.WriteFile(p, []byte("127.0.0.1:1 Y29zdA==\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		r, out := brokerRelayProbeOnce(t, p)
		if r.failed != 1 || !strings.Contains(out, "relay endpoint incomplete") {
			t.Errorf("failed=%d out=%q", r.failed, out)
		}
	})

	t.Run("auth rejected is its own message", func(t *testing.T) {
		p := filepath.Join(dir, "relay-live.endpoint")
		ln, err := svcendpoint.Listen(p, "")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		ep, err := svcendpoint.Read(p)
		if err != nil {
			t.Fatal(err)
		}
		ep.Token = strings.Repeat("b", len(ep.Token))
		bad := filepath.Join(dir, "relay-wrongtoken.endpoint")
		if err := svcendpoint.Publish(bad, ep); err != nil {
			t.Fatal(err)
		}
		r, out := brokerRelayProbeOnce(t, bad)
		if r.failed != 1 || !strings.Contains(out, "rejected this jail's token") {
			t.Errorf("failed=%d out=%q", r.failed, out)
		}
		// It must NOT be reported as the broker failing behind a working relay.
		if strings.Contains(out, "broker unreachable") {
			t.Errorf("a token mismatch was blamed on the broker: %q", out)
		}
	})

	t.Run("relay authenticates but broker does not answer", func(t *testing.T) {
		p := filepath.Join(dir, "relay-nobroker.endpoint")
		ln, err := svcendpoint.Listen(p, "")
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
				_ = c.Close() // authenticated, then nothing: the broker behind it is down
			}
		}()
		r, out := brokerRelayProbeOnce(t, p)
		if r.failed != 1 || !strings.Contains(out, "broker unreachable") {
			t.Errorf("failed=%d out=%q", r.failed, out)
		}
	})

	t.Run("healthy end to end", func(t *testing.T) {
		p := filepath.Join(dir, "relay-ok.endpoint")
		// The DEFAULT advertise host, as a real relay publishes it — DialLocal
		// substituting 127.0.0.1 for the gateway name is what makes a host-side probe
		// possible at all.
		ln, err := svcendpoint.Listen(p, "")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		go serveBrokerPong(ln)
		r, out := brokerRelayProbeOnce(t, p)
		if r.failed != 0 || r.passed != 1 || !strings.Contains(out, "broker answers through it") {
			t.Errorf("a live relay with a live broker did not pass: passed=%d failed=%d out=%q",
				r.passed, r.failed, out)
		}
		if !strings.Contains(out, "token-authenticated") {
			t.Errorf("the success line does not record that the probe authenticated: %q", out)
		}
	})
}

// serveBrokerPong answers the check's framed {"action":"ping"} with {"pong":true}
// + exit 0, standing in for the singleton behind the relay.
func serveBrokerPong(ln *svcendpoint.Listener) {
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
			if _, err := io.ReadFull(c, make([]byte, binary.BigEndian.Uint32(hdr))); err != nil {
				return
			}
			body := []byte(`{"pong": true}`)
			fh := make([]byte, 5)
			binary.BigEndian.PutUint32(fh[1:], uint32(len(body)))
			_, _ = c.Write(fh) // stream 0
			_, _ = c.Write(body)
			ex := make([]byte, 5)
			ex[0] = 2
			binary.BigEndian.PutUint32(ex[1:], 4)
			_, _ = c.Write(ex)
			_, _ = c.Write([]byte{0, 0, 0, 0})
		}(c)
	}
}

// TestJailEndpointProbeUsesTestF: the in-jail visibility probe must test a regular
// FILE. `test -S` would report every healthy jail as broken, because what crosses
// into the jail is an endpoint file, not a socket.
func TestJailEndpointProbeUsesTestF(t *testing.T) {
	var gotArgv []string
	o := &Options{}
	fillDefaults(o)
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		gotArgv = argv
		return ExecResult{Ran: true, RC: 0}
	}
	v := o.relayEndpointVisibleInJail("podman", "yolo-ws-abcd1234")
	if v == nil || !*v {
		t.Fatalf("rc=0 should read as visible, got %v", v)
	}
	joined := strings.Join(gotArgv, " ")
	if !strings.Contains(joined, "test -f ") {
		t.Errorf("probe argv = %q, want a `test -f` on the endpoint file", joined)
	}
	if strings.Contains(joined, "test -S") {
		t.Errorf("probe still tests for a SOCKET: %q", joined)
	}
	if !strings.Contains(joined, "/run/yolo-services/claude-oauth-broker.endpoint") {
		t.Errorf("probe does not name the in-jail endpoint file: %q", joined)
	}
}
