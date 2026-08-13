package oauthbroker

import (
	"encoding/binary"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// TestSingletonSocketIsAUnixSocket pins the ONE thing three call sites depend on:
// what Main binds at --socket must be an AF_UNIX socket that speaks the frame
// protocol.
//
// internal/brokerrelay (per-connection dial), internal/broker.BrokerPing (the
// liveness gate `yolo run` uses before it will ensure a relay at all) and
// internal/cli/check all reach the host-wide singleton with
// net.Dial("unix", …). None of them can reach a loopback-TLS endpoint file, and
// nothing in the tree teaches them to.
func TestSingletonSocketIsAUnixSocket(t *testing.T) {
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
	sockDir := shortSocketDir(t)
	sock := filepath.Join(sockDir, "yolo-claude-oauth-broker.sock")

	logPath := filepath.Join(dir, "daemon.log")
	cmd := exec.Command(os.Args[0], "-singleton-child",
		"--socket", sock, "--no-background-refresh", "--log-file", logPath)
	cmd.Env = append(os.Environ(),
		"HOME="+dir,
		"XDG_CONFIG_HOME="+filepath.Join(dir, ".config"),
		"YOLO_BROKER_STATE_DIR="+state,
	)
	var sb strings.Builder
	cmd.Stdout, cmd.Stderr = &sb, &sb
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Lstat(sock); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	st, err := os.Lstat(sock)
	if err != nil {
		t.Fatalf("the singleton published nothing at --socket %s in 10s: %v\ndaemon output:\n%s",
			sock, err, sb.String())
	}
	if st.Mode()&fs.ModeSocket == 0 {
		body, _ := os.ReadFile(sock)
		t.Errorf("--socket %s is %s, not an AF_UNIX socket; the relay, the liveness "+
			"ping and `yolo check` all dial it with net.Dial(\"unix\", …).\ncontents: %q",
			sock, st.Mode().String(), string(body))
	}

	// Behavioural, not just a mode bit: the frame-protocol ping must round-trip,
	// which is exactly what broker.BrokerIsAlive requires before `yolo run` will
	// ensure this jail's relay.
	conn, err := net.DialTimeout("unix", sock, 3*time.Second)
	if err != nil {
		t.Fatalf("net.Dial(\"unix\", %s) failed: %v\ndaemon output:\n%s", sock, err, sb.String())
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	req := []byte(`{"action":"ping"}`)
	frame := make([]byte, 4+len(req))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(req)))
	copy(frame[4:], req)
	if _, err := conn.Write(frame); err != nil {
		t.Fatalf("writing the ping frame failed: %v", err)
	}
	// Response framing is <1-byte stream_id><4-byte BE length><payload>, which is
	// NOT the request framing above (4-byte length, no stream id).
	hdr := make([]byte, 5)
	if _, err := readFullConn(conn, hdr); err != nil {
		t.Fatalf("no response frame from the singleton: %v\ndaemon output:\n%s", err, sb.String())
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
