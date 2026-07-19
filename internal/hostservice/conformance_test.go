package hostservice

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// TestConformanceHandlerErrorFrame proves a panicking handler produces the
// exact behavior: stderr "handler error: <e>\n" then exit(1).
func TestConformanceHandlerErrorFrame(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "yj-conf-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "err.sock")

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = Serve(func(s *Session) { panic(errString("boom")) }, sock, stop)
		close(done)
	}()
	waitForSocket(t, sock)
	defer func() { close(stop); <-done }()

	conn, _ := net.DialTimeout("unix", sock, 5*time.Second)
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
	dir, err := os.MkdirTemp("/tmp", "yj-conf-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "sig.sock")

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = Serve(func(s *Session) {
			s.ExecAllowlisted(
				func(*jsonx.OrderedMap) []string { return []string{"sh", "-c", "kill -TERM $$"} },
				map[string]struct{}{"sh": {}, "-c": {}, "kill -TERM $$": {}},
				nil, 5*1e9,
			)
		}, sock, stop)
		close(done)
	}()
	waitForSocket(t, sock)
	defer func() { close(stop); <-done }()

	conn, _ := net.DialTimeout("unix", sock, 5*time.Second)
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

func waitForSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
			c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never connectable", path)
}

type errString string

func (e errString) Error() string { return string(e) }
