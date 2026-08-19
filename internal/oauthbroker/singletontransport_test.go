package oauthbroker

import (
	"encoding/binary"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMain doubles as the daemon child: `<test-binary> -singleton-child <args…>`
// runs Main with the remaining argv, so the test drives the REAL production entry
// point (`yolo internal daemon claude-oauth-broker --socket …`) in a process it
// can kill, rather than leaking a blocked Serve goroutine into the suite.
func TestMain(m *testing.M) {
	if len(os.Args) >= 2 && os.Args[1] == "-singleton-child" {
		os.Exit(Main(os.Args[2:]))
	}
	os.Exit(m.Run())
}

// TestSingletonSocketIsAFrontedUnixSocket pins the ONE thing three call sites
// depend on: what Main binds at --socket must be an AF_UNIX socket that speaks the
// frame protocol BEHIND YOLO'S FRONT.
//
// "Behind the front" is the part that changed, and it changed on the credential
// path. The singleton used to be host-to-host: the per-jail relay dialed it and
// wrote the client's request first. The relay is deleted
// (docs/design/broker-as-a-pack.md §7) and the manifest declares
// `publishes: "socket"` + `scope: "host"`, so yolo runs an svcendpoint front per
// jail over this one socket and every connection it accepts opens with the
// CONNECTION PREAMBLE. Reading it is what keeps the jail_id on this daemon's audit
// line host-asserted (invariant I1) now that nothing stamps the payload.
//
// The preamble frame is written out BY HAND here rather than through svcendpoint's
// encoder, deliberately: it is a frozen wire contract between two processes, and a
// test that called the same encoder the producer calls would agree with it by
// construction — including about a format change that breaks every already-running
// front on the host.
func TestSingletonSocketIsAFrontedUnixSocket(t *testing.T) {
	sock, sb, stop := startSingleton(t)
	defer stop()

	st, err := os.Lstat(sock)
	if err != nil {
		t.Fatalf("the singleton published nothing at --socket %s in 10s: %v\ndaemon output:\n%s",
			sock, err, sb())
	}
	if st.Mode()&fs.ModeSocket == 0 {
		body, _ := os.ReadFile(sock)
		t.Errorf("--socket %s is %s, not an AF_UNIX socket; yolo's front and the liveness "+
			"probe both reach it with net.Dial(\"unix\", …).\ncontents: %q",
			sock, st.Mode().String(), string(body))
	}

	// Behavioural: preamble, then the framed ping, then a pong — the exact byte
	// sequence a jail's request takes once the front has spliced it.
	conn, err := net.DialTimeout("unix", sock, 3*time.Second)
	if err != nil {
		t.Fatalf("net.Dial(\"unix\", %s) failed: %v\ndaemon output:\n%s", sock, err, sb())
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(framed(`{"jail_id":"yolo-ws-abcd1234","service":"claude-oauth-broker","v":1}`)); err != nil {
		t.Fatalf("writing the connection preamble failed: %v", err)
	}
	if _, err := conn.Write(framed(`{"action":"ping"}`)); err != nil {
		t.Fatalf("writing the ping frame failed: %v", err)
	}
	// Response framing is <1-byte stream_id><4-byte BE length><payload>, which is
	// NOT the request framing above (4-byte length, no stream id).
	hdr := make([]byte, 5)
	if _, err := readFullConn(conn, hdr); err != nil {
		t.Fatalf("no response frame from the singleton: %v\ndaemon output:\n%s", err, sb())
	}
	n := binary.BigEndian.Uint32(hdr[1:5])
	if n > 1<<20 {
		t.Fatalf("implausible response frame length %d", n)
	}
	payload := make([]byte, n)
	if _, err := readFullConn(conn, payload); err != nil {
		t.Fatalf("short response frame: %v", err)
	}
	if !strings.Contains(string(payload), "pong") {
		t.Errorf("ping did not pong: stream=%d payload=%q", hdr[0], string(payload))
	}
}

// TestSingletonSurvivesABareConnectAndClose is the OTHER half of the flip, and it
// is the one that decides whether a jail ever launches.
//
// yolo's liveness probe for the singleton is a bare connect-and-close
// (broker.SingletonReachable) — it cannot speak the daemon's protocol any more,
// because doing so would mean forging a jail identity in the preamble. So the
// daemon must treat "closed before a preamble" exactly as it already treats "closed
// before a request": one log line, one closed connection, ACCEPT LOOP UNTOUCHED. A
// daemon that died or wedged on the probe would be respawned by brokerEnsure on
// every launch — and the probe runs at least twice per launch.
func TestSingletonSurvivesABareConnectAndClose(t *testing.T) {
	sock, sb, stop := startSingleton(t)
	defer stop()

	for i := 0; i < 3; i++ {
		c, err := net.DialTimeout("unix", sock, 3*time.Second)
		if err != nil {
			t.Fatalf("probe %d could not connect: %v\ndaemon output:\n%s", i, err, sb())
		}
		_ = c.Close()
	}
	// And it still serves a real request afterwards.
	conn, err := net.DialTimeout("unix", sock, 3*time.Second)
	if err != nil {
		t.Fatalf("the daemon stopped accepting after three bare probes: %v\ndaemon output:\n%s",
			err, sb())
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = conn.Write(framed(`{"jail_id":"j","service":"claude-oauth-broker","v":1}`))
	_, _ = conn.Write(framed(`{"action":"ping"}`))
	hdr := make([]byte, 5)
	if _, err := readFullConn(conn, hdr); err != nil {
		t.Fatalf("no response after the bare probes: %v\ndaemon output:\n%s", err, sb())
	}
}

// framed wraps a JSON body in the request framing both the preamble and a
// frameproto request use: 4-byte big-endian length, then the body.
func framed(body string) []byte {
	out := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(out[:4], uint32(len(body)))
	copy(out[4:], body)
	return out
}

// startSingleton runs the REAL production entry point as a child process and waits
// for its socket to appear. Returns the socket path, an accessor for the daemon's
// combined output (for failure messages) and a stop func.
func startSingleton(t *testing.T) (string, func() string, func()) {
	t.Helper()
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	// Dummy CA/leaf so EnsureCAAndLeaf short-circuits on isFile() and the test
	// needs no openssl. The daemon never reads them on this path.
	for _, f := range []string{"ca.crt", "ca.key", "server.crt", "server.key"} {
		if err := os.WriteFile(filepath.Join(state, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// 0700, i.e. the FRIENDLIEST possible directory. The production path is
	// /tmp/yolo-claude-oauth-broker.sock, whose parent is 1777 — strictly harder.
	//
	// NOT under t.TempDir(): the socket name alone is 30 bytes, and a TMPDIR-rooted
	// parent overruns darwin's 104-byte sun_path. The daemon then fails to bind,
	// publishes nothing, and this test reports the 10s timeout — which reads as a
	// broken singleton rather than a too-long path. That is exactly how it failed
	// on check-macos while passing everywhere else.
	sock := filepath.Join(shortSocketDir(t), "yolo-claude-oauth-broker.sock")

	logPath := filepath.Join(dir, "daemon.log")
	cmd := exec.Command(os.Args[0], "-singleton-child",
		"--socket", sock, "--no-background-refresh", "--log-file", logPath)
	cmd.Env = append(os.Environ(),
		"HOME="+dir,
		"XDG_CONFIG_HOME="+filepath.Join(dir, ".config"),
		"YOLO_BROKER_STATE_DIR="+state,
	)
	var mu sync.Mutex
	var sb strings.Builder
	cmd.Stdout, cmd.Stderr = &lockedWriter{mu: &mu, w: &sb}, &lockedWriter{mu: &mu, w: &sb}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	out := func() string {
		mu.Lock()
		defer mu.Unlock()
		return sb.String()
	}
	stop := func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Lstat(sock); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return sock, out, stop
}

// lockedWriter serializes the child's stdout and stderr into one buffer that the
// test goroutine also reads — two pipes writing one strings.Builder is a data race
// the -race build reports.
type lockedWriter struct {
	mu *sync.Mutex
	w  *strings.Builder
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func readFullConn(c net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := c.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// shortSocketDir returns a per-test directory short enough to hold an AF_UNIX
// socket path. darwin's sun_path is 104 bytes including the NUL (Linux's is
// 108), and t.TempDir() is rooted at TMPDIR — which on macOS is
// /var/folders/<2>/<26>/T/, ~49 bytes before the test name is appended.
//
// The failure this prevents is indirect and therefore expensive: the daemon is a
// child process, so a too-long --socket surfaces here as "published nothing in
// 10s", not as a bind error. Reproduce on any platform by pointing TMPDIR at a
// long path; internal/cli/run pins the same property with a test.
func shortSocketDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "yj-broker-")
	if err != nil {
		t.Fatal(err)
	}
	if len(d)+len("/yolo-claude-oauth-broker.sock") > 103 {
		t.Fatalf("short socket dir is not short: %s", d)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}
