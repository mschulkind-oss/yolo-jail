package journald

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// The `publishes: "socket"` door, end to end, with nothing stubbed: the daemon binds
// a plain AF_UNIX socket, yolo's real svcendpoint front holds the jail-facing
// loopback-TLS listener in front of it, and the client runs the jail's real dial path
// (read the published file, pin that cert, present that token).
//
// WHAT IT ACTUALLY PINS is the preamble read, and the failure it prevents is total
// rather than subtle. yolo prepends its connection preamble to every spliced
// connection (a manifest's `preamble` defaults ON), and this daemon's request parser
// reads bytes up to the first newline — so a ServeFrontedUnix that skipped
// ReadPreamble would hand the preamble's 4-byte length header and JSON body to
// readHeaderCapped and answer EVERY request with "malformed request", exit 2. There
// is no partial version of this bug.
func TestFrontedJournalConsumesThePreamble(t *testing.T) {
	_, _, endpoint := startFrontedJournal(t, ModeUser, 32)

	conn, err := svcendpoint.DialLocal(endpoint, 5*time.Second)
	if err != nil {
		t.Fatalf("dialing the published endpoint: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := conn.Write([]byte(`{"args":["-n","5"]}` + "\n")); err != nil {
		t.Fatal(err)
	}
	out, rc := readFrames(t, conn)
	if rc != 0 {
		t.Fatalf("rc=%d, want 0 — a non-zero exit here is the daemon reading yolo's "+
			"connection preamble AS the request header (got stdout %q)", rc, string(out))
	}
	if want := bytes.Repeat([]byte("x"), 32); !bytes.Equal(out, want) {
		t.Fatalf("stdout = %q, want %q", string(out), string(want))
	}
}

// yolo's own readiness check for a fronted daemon is a bare connect-and-close on the
// UPSTREAM socket, bypassing the front entirely (socketConnectable, in
// internal/cli/run/loopholesruntime.go). It sends no preamble and no request, so it
// takes the ReadPreamble error path on EVERY launch — and a daemon that treated that
// as fatal would die on its own health check before any jail ever dialed it.
func TestFrontedJournalSurvivesTheBareReadinessProbe(t *testing.T) {
	_, sock, endpoint := startFrontedJournal(t, ModeUser, 8)

	probe, err := dialUnix(sock)
	if err != nil {
		t.Fatalf("the readiness probe could not reach the socket: %v", err)
	}
	_ = probe.Close()

	// And the accept loop is still there afterwards, which the probe alone does not say.
	conn, err := svcendpoint.DialLocal(endpoint, 5*time.Second)
	if err != nil {
		t.Fatalf("the daemon stopped accepting after a bare probe: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := conn.Write([]byte(`{"args":["-n","1"]}` + "\n")); err != nil {
		t.Fatal(err)
	}
	if _, rc := readFrames(t, conn); rc != 0 {
		t.Fatalf("rc=%d after a bare probe, want 0", rc)
	}
}

// startFrontedJournal brings up ServeFrontedUnix with a real front over it and a fake
// journalctl on PATH emitting bodyLen bytes. It returns the dir, the upstream socket
// and the published endpoint.
func startFrontedJournal(t *testing.T, mode string, bodyLen int) (dir, sock, endpoint string) {
	t.Helper()
	// os.MkdirTemp creates 0700, which svcendpoint REQUIRES of a directory it
	// publishes a credential into. t.TempDir() creates 0755 and is refused.
	dir, err := os.MkdirTemp("/tmp", "yj-journal-fronted-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	writeFakeJournalctl(t, filepath.Join(dir, "journalctl"), bodyLen)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	sock = filepath.Join(dir, "upstream.sock")
	endpoint = filepath.Join(dir, "journal.endpoint")

	daemonStop := make(chan struct{})
	daemonDone := make(chan struct{})
	go func() {
		defer close(daemonDone)
		if err := ServeFrontedUnix(sock, mode, daemonStop); err != nil {
			t.Errorf("ServeFrontedUnix: %v", err)
		}
	}()
	waitForUnixSocket(t, sock)

	frontStop := make(chan struct{})
	frontDone := make(chan struct{})
	go func() {
		defer close(frontDone)
		_ = svcendpoint.ServeFront(endpoint, "127.0.0.1", sock, frontStop)
	}()
	waitForPublishedEndpoint(t, endpoint)

	t.Cleanup(func() {
		close(frontStop)
		<-frontDone
		close(daemonStop)
		<-daemonDone
	})
	return dir, sock, endpoint
}

// waitForUnixSocket waits for the daemon's bind BY TYPE rather than by dialing: a
// connect-and-close poll would write a preamble-rejected line into the daemon's own
// log for no reason.
func waitForUnixSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSocket != 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("ServeFrontedUnix never bound %s", path)
}

func waitForPublishedEndpoint(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if svcendpoint.Probe(path) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the front never published %s", path)
}

// Both retired flags REFUSE and name their replacement, and the reason each one is a
// refusal rather than a fallback is the direction its silence would go wrong in:
// honouring --mode's absence would DROP an escalation somebody asked for, and
// honouring --endpoint would publish a bearer-token regular file where the front
// expects a socket.
func TestRetiredFlagsRefuseAndNameTheirReplacement(t *testing.T) {
	cases := []struct {
		name  string
		argv  []string
		wants []string
	}{
		{
			name:  "--mode",
			argv:  []string{"--socket", "/tmp/nope.sock", "--mode", "full"},
			wants: []string{"--mode is retired", "--settings", "loopholes.journal.settings", `"full": true`},
		},
		{
			name:  "--endpoint",
			argv:  []string{"--endpoint", "/tmp/nope.endpoint"},
			wants: []string{"--endpoint is retired", "--socket", `publishes = "socket"`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stderr := captureStderr(t, func() {
				if rc := Main(tc.argv); rc != 2 {
					t.Errorf("Main(%v) = %d, want 2 — a retired flag must refuse rather "+
						"than start a daemon on a configuration nobody wrote", tc.argv, rc)
				}
			})
			for _, want := range tc.wants {
				if !strings.Contains(stderr, want) {
					t.Errorf("refusal missing %q — a user hitting this has to be able to fix "+
						"it without reading a design doc; got:\n%s", want, stderr)
				}
			}
		})
	}
}

// captureStderr runs fn with os.Stderr redirected to a pipe and returns what it wrote.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r)
		done <- buf.String()
	}()
	fn()
	os.Stderr = saved
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// dialUnix is the bare connect the run pipeline's readiness probe makes.
func dialUnix(path string) (net.Conn, error) {
	return net.DialTimeout("unix", path, 5*time.Second)
}
