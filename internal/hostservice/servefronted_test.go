package hostservice

import (
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// The ServeFrontedUnix suite — and the first coverage of a SOCKET-shaped entry
// point this package has ever had. ServeUnix has no test in its own package
// (every other file here drives ServeEndpoint), which is exactly why the
// `publishes: "socket"` shape got one before it was spawned anywhere.
//
// The shape under test is production's: the daemon binds a plain AF_UNIX socket
// and knows nothing else, while yolo's own svcendpoint.ServeFront holds the
// jail-facing loopback-TLS listener, authenticates, prepends the connection
// preamble and splices. Nothing here stubs the transport — the client below runs
// the jail's real dial path (read the published file, pin that cert, present that
// token, wait for the ack) and only the advertised host differs.

// startFronted brings up a ServeFrontedUnix daemon with a real front in front of
// it, and returns the endpoint the client dials plus the upstream socket the
// front splices to (which the readiness-probe test dials directly).
func startFronted(t *testing.T, handler Handler) (endpoint, sock string) {
	t.Helper()
	// os.MkdirTemp creates 0700, which svcendpoint requires of a directory it
	// publishes a credential into. t.TempDir() creates 0755 and is REFUSED.
	dir, err := os.MkdirTemp("/tmp", "yj-fronted-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock = filepath.Join(dir, "upstream.sock")
	endpoint = filepath.Join(dir, "fronted.endpoint")

	daemonStop := make(chan struct{})
	daemonDone := make(chan struct{})
	go func() {
		_ = ServeFrontedUnix(handler, sock, daemonStop)
		close(daemonDone)
	}()
	waitForSocket(t, sock)

	frontStop := make(chan struct{})
	frontDone := make(chan struct{})
	go func() {
		_ = svcendpoint.ServeFront(endpoint, "127.0.0.1", sock, frontStop)
		close(frontDone)
	}()
	waitForEndpoint(t, endpoint)

	t.Cleanup(func() {
		close(frontStop)
		<-frontDone
		close(daemonStop)
		<-daemonDone
	})
	return endpoint, sock
}

// waitForSocket waits for the daemon's bind, by TYPE rather than by dialing: a
// connect-and-close poll would write a "conn closed without a request" line into
// the very log the assertions below read.
func waitForSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSocket != 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("ServeFrontedUnix never bound %s", path)
}

// captureLog redirects the package Logger for one test and returns the buffer.
func captureLog(t *testing.T) *syncBuf {
	t.Helper()
	prev := Logger
	logs := &syncBuf{}
	Logger = log.New(logs, "", 0)
	t.Cleanup(func() { Logger = prev })
	return logs
}

// TestServeFrontedUnixRoundTripsAndTheHostNamesTheJail is the step's point: a
// daemon that binds nothing but a Unix socket answers a jail's request, and the
// tier-2 line it writes names the jail from the PUBLICATION PATH rather than from
// anything the client said.
//
// That is what ServeFrontedUnix buys over ServeUnix, and it is invisible from the
// socket's side — both are AF_UNIX, 0600, same bind — so it has to be asserted
// through the front or not at all.
func TestServeFrontedUnixRoundTripsAndTheHostNamesTheJail(t *testing.T) {
	logs := captureLog(t)
	endpoint, _ := startFronted(t, func(s *Session) { s.Stdout("pong\n") })

	// DialLocal, not Dial: this client is on the same machine as the listener and
	// keeps the published port while substituting 127.0.0.1. Same pin, same token,
	// same ack.
	conn, err := svcendpoint.DialLocal(endpoint, 5*time.Second)
	if err != nil {
		t.Fatalf("dial the front: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	// THE SPOOF IS THE NEGATIVE CASE and is deliberately on the wire: a client
	// naming itself is precisely what the preamble exists to override, so the
	// assertions below mean nothing without a lie in the request.
	if err := frameproto.WriteRequest(conn, []byte(`{"jail_id":"i-said-so","mode":"ping"}`)); err != nil {
		t.Fatal(err)
	}
	f, err := frameproto.ReadFrame(conn)
	if err != nil {
		t.Fatalf("no response through the front: %v", err)
	}
	if f.StreamID != frameproto.StreamStdout || string(f.Payload) != "pong\n" {
		t.Errorf("first frame = stream %d %q, want stdout %q", f.StreamID, f.Payload, "pong\n")
	}

	// The request DID reach the handler, so the daemon consumed the preamble and
	// then the request — not the preamble AS the request.
	waitFor(t, func() bool { return strings.Contains(logs.String(), "rc=0") })
	line := logs.String()
	wantJail := filepath.Base(filepath.Dir(endpoint))
	for _, want := range []string{"jail=" + wantJail, "keys=jail_id,mode", "rc=0"} {
		if !strings.Contains(line, want) {
			t.Errorf("tier 2 access log missing %q; got:\n%s", want, line)
		}
	}
	if strings.Contains(line, "jail=i-said-so") {
		t.Errorf("a fronted daemon took the CLIENT's word for the jail's identity; the "+
			"connection preamble's host-asserted jail_id must win. Line:\n%s", line)
	}
}

// TestServeFrontedUnixSurvivesTheBareReadinessProbe pins the degradation the
// preamble's arrival makes possible to get wrong.
//
// yolo's own readiness check for a fronted service is a bare connect-and-close on
// the UPSTREAM socket that bypasses the front entirely (socketConnectable, in
// internal/cli/run/loopholesruntime.go), so it sends no preamble and no request.
// A daemon that treated a missing preamble as fatal would die on its own health
// check, on every launch, before any jail ever dialed it. It must degrade exactly
// as a connection that closes before a request already does: one log line, one
// closed connection, the accept loop untouched.
func TestServeFrontedUnixSurvivesTheBareReadinessProbe(t *testing.T) {
	logs := captureLog(t)
	endpoint, sock := startFronted(t, func(s *Session) { s.Stdout("pong\n") })

	probe, err := net.DialTimeout("unix", sock, 5*time.Second)
	if err != nil {
		t.Fatalf("the readiness probe could not reach the socket: %v", err)
	}
	_ = probe.Close()

	waitFor(t, func() bool { return strings.Contains(logs.String(), "conn closed without a request") })

	// And the accept loop is still there afterwards — the assertion the log line
	// alone does not make.
	conn, err := svcendpoint.DialLocal(endpoint, 5*time.Second)
	if err != nil {
		t.Fatalf("the daemon stopped accepting after a bare probe: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := frameproto.WriteRequest(conn, []byte(`{"mode":"ping"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := frameproto.ReadFrame(conn); err != nil {
		t.Fatalf("no response after a bare probe: %v", err)
	}
}
