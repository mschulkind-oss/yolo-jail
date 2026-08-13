package check

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
