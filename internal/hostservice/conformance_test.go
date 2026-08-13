package hostservice

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// THE ASSERTIONS IN THIS FILE ARE UNCHANGED BY THE TRANSPORT MIGRATION. Only how a
// connection is obtained changed: `net.DialTimeout("unix", …)` became a cert-pinned,
// token-authenticated svcendpoint.Dial. That the handler-panic and signal-death
// contracts still hold byte-for-byte over the new transport is the mechanical proof
// that a daemon never learns which transport carried its bytes.

// TestConformanceHandlerErrorFrame proves a panicking handler produces the
// exact behavior: stderr "handler error: <e>\n" then exit(1).
func TestConformanceHandlerErrorFrame(t *testing.T) {
	advertiseLoopback(t)
	dir, err := os.MkdirTemp("/tmp", "yj-conf-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	ep := filepath.Join(dir, "err.endpoint")

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = ServeEndpoint(func(s *Session) { panic(errString("boom")) }, ep, stop)
		close(done)
	}()
	waitForEndpoint(t, ep)
	defer func() { close(stop); <-done }()

	conn := dialEndpoint(t, ep)
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	_ = frameproto.WriteRequest(conn, []byte(`{"jail_id":"j"}`))

	f, err := frameproto.ReadFrame(conn)
	if err != nil {
		t.Fatal(err)
	}
	if f.StreamID != frameproto.StreamStderr || string(f.Payload) != "handler error: boom\n" {
		t.Errorf("err frame = stream %d %q, want stderr 'handler error: boom\\n'", f.StreamID, f.Payload)
	}
	f, err = frameproto.ReadFrame(conn)
	if err != nil {
		t.Fatal(err)
	}
	rc, _ := frameproto.ExitCode(f.Payload)
	if f.StreamID != frameproto.StreamExit || rc != 1 {
		t.Errorf("exit frame = stream %d rc %d, want exit(1)", f.StreamID, rc)
	}
}

// TestExecAllowlistedSignalDeathExitCode: a child killed by a signal must
// round-trip as -N (e.g. SIGTERM -> -15).
func TestExecAllowlistedSignalDeathExitCode(t *testing.T) {
	advertiseLoopback(t)
	dir, err := os.MkdirTemp("/tmp", "yj-conf-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	ep := filepath.Join(dir, "sig.endpoint")

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = ServeEndpoint(func(s *Session) {
			s.ExecAllowlisted(
				func(*jsonx.OrderedMap) []string { return []string{"sh", "-c", "kill -TERM $$"} },
				map[string]struct{}{"sh": {}, "-c": {}, "kill -TERM $$": {}},
				nil, 5*1e9,
			)
		}, ep, stop)
		close(done)
	}()
	waitForEndpoint(t, ep)
	defer func() { close(stop); <-done }()

	conn := dialEndpoint(t, ep)
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	_ = frameproto.WriteRequest(conn, []byte(`{"jail_id":"j"}`))
	for {
		f, err := frameproto.ReadFrame(conn)
		if err != nil {
			t.Fatal("no exit frame")
		}
		if f.StreamID == frameproto.StreamExit {
			rc, _ := frameproto.ExitCode(f.Payload)
			if rc != -15 {
				t.Errorf("signal-death rc = %d, want -15 (SIGTERM)", rc)
			}
			return
		}
	}
}

// --- helpers ---

// advertiseLoopback makes Listen publish 127.0.0.1 rather than the container
// runtime's gateway name, because the client here is on the same machine as the
// listener. This exercises the JAIL's exact dial path (read the file, pin that cert,
// present that token, wait for the ack) — only the name inside the file differs, so
// nothing about the handshake is stubbed out.
//
// os.MkdirTemp is used for the publish dir throughout: it creates 0700, which
// svcendpoint requires. t.TempDir() creates 0755 and is correctly REFUSED.
func advertiseLoopback(t *testing.T) {
	t.Helper()
	t.Setenv(svcendpoint.AdvertiseHostEnv, "127.0.0.1")
}

// waitForEndpoint waits for a COMPLETE, USABLE endpoint file. Probe, not existence:
// a truncated or older-format file exists but names nothing dialable.
func waitForEndpoint(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if svcendpoint.Probe(path) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never published a usable endpoint", path)
}

func dialEndpoint(t *testing.T, path string) net.Conn {
	t.Helper()
	conn, err := svcendpoint.Dial(path, 5*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", path, err)
	}
	return conn
}

type errString string

func (e errString) Error() string { return string(e) }
