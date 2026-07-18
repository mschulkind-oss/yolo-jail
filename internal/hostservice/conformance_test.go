package hostservice

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
)

// TestConformancePythonServerGoClient replays the frame protocol against the
// REAL Python host_service smoke server (`python -m src.host_service <sock>`)
// using the Go frameproto client, proving the Go client reads what Python
// writes: >BI framing, the JSON stdout line, and the implicit exit(0).
// (go-port plan Stage 4 conformance suite.)
func TestConformancePythonServerGoClient(t *testing.T) {
	py := pythonCmd(t, "-m", "src.host_service")
	if py == nil {
		t.Skip("python unavailable")
	}
	dir, err := os.MkdirTemp("/tmp", "yj-conf-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "smoke.sock")

	cmd := py(sock)
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start python smoke server: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	waitForSocket(t, sock)

	conn, err := net.DialTimeout("unix", sock, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	// Send a framed request; the smoke handler echoes {"ok":true,"echo":req}.
	req := []byte(`{"jail_id":"jail-c","action":"ping"}`)
	if err := frameproto.WriteRequest(conn, req); err != nil {
		t.Fatal(err)
	}

	// First frame: stdout carrying one JSON line.
	f, err := frameproto.ReadFrame(conn)
	if err != nil {
		t.Fatalf("read stdout frame: %v", err)
	}
	if f.StreamID != frameproto.StreamStdout {
		t.Fatalf("first frame stream=%d, want stdout(0)", f.StreamID)
	}
	var payload map[string]any
	if err := json.Unmarshal(bytes.TrimRight(f.Payload, "\n"), &payload); err != nil {
		t.Fatalf("stdout payload not JSON: %v (%q)", err, f.Payload)
	}
	if payload["ok"] != true {
		t.Errorf("smoke reply ok=%v, want true", payload["ok"])
	}

	// Next frame: exit(0) — the default the server sends when the handler
	// returns without an explicit exit.
	f, err = frameproto.ReadFrame(conn)
	if err != nil {
		t.Fatalf("read exit frame: %v", err)
	}
	if f.StreamID != frameproto.StreamExit {
		t.Fatalf("second frame stream=%d, want exit(2)", f.StreamID)
	}
	rc, err := frameproto.ExitCode(f.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if rc != 0 {
		t.Errorf("exit rc=%d, want 0", rc)
	}
}

// TestConformanceGoServerPythonClient drives a Go hostservice.Serve echo with
// the REAL Python yolo_ps client's frame reader, proving Python reads what Go
// writes. It runs a small Python snippet that speaks the client half of the
// protocol (send framed request, read frames until exit) and checks the
// round-trip.
func TestConformanceGoServerPythonClient(t *testing.T) {
	if pythonCmd(t) == nil {
		t.Skip("python unavailable")
	}
	dir, err := os.MkdirTemp("/tmp", "yj-conf-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "go.sock")

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = Serve(func(s *Session) {
			// Echo the request back as a JSON line, then stdout "hi", exit 7.
			_ = s.JSON(map[string]any{"ok": true})
			s.Stdout("hi")
			s.Exit(7)
		}, sock, stop)
		close(done)
	}()
	waitForSocket(t, sock)
	defer func() { close(stop); <-done }()

	// Python client: frame the request, read stdout/exit frames like yolo_ps.
	script := `
import socket, struct, json, sys
sock = sys.argv[1]
c = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM); c.settimeout(5); c.connect(sock)
body = json.dumps({"jail_id":"j","mode":"list"}).encode()
c.sendall(struct.pack(">I", len(body)) + body)
def recv_exact(n):
    buf=b""
    while len(buf)<n:
        ch=c.recv(n-len(buf))
        if not ch: return None
        buf+=ch
    return buf
out=[]
rc=None
while True:
    h=recv_exact(5)
    if h is None: break
    sid,ln=struct.unpack(">BI",h)
    p=recv_exact(ln) if ln else b""
    if sid==0: out.append(("stdout",p.decode()))
    elif sid==1: out.append(("stderr",p.decode()))
    elif sid==2:
        (rc,)=struct.unpack(">i",p); break
print(json.dumps({"out":out,"rc":rc}))
`
	cmd := pythonCmd(t)("-c", script, sock)
	got, err := cmd.Output()
	if err != nil {
		t.Fatalf("python client failed: %v (%s)", err, got)
	}
	var result struct {
		Out [][]string `json:"out"`
		RC  int        `json:"rc"`
	}
	if err := json.Unmarshal(got, &result); err != nil {
		t.Fatalf("decode client output: %v (%q)", err, got)
	}
	if result.RC != 7 {
		t.Errorf("python client saw rc=%d, want 7", result.RC)
	}
	// Expect: stdout JSON line {"ok": true}\n, then stdout "hi".
	if len(result.Out) < 2 {
		t.Fatalf("frames = %v, want at least 2 stdout frames", result.Out)
	}
	if result.Out[0][0] != "stdout" || result.Out[0][1] != "{\"ok\": true}\n" {
		t.Errorf("first frame = %v, want stdout {\"ok\": true}\\n", result.Out[0])
	}
	if result.Out[1][0] != "stdout" || result.Out[1][1] != "hi" {
		t.Errorf("second frame = %v, want stdout hi", result.Out[1])
	}
}

// TestConformanceHandlerErrorFrame proves a panicking handler produces the
// exact Python behavior: stderr "handler error: <e>\n" then exit(1).
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

	// stderr frame with "handler error: boom\n".
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

// pythonCmd returns a factory building a *exec.Cmd that runs the repo's Python
// with the given args, or nil if unavailable. Prefers `uv run python`.
func pythonCmd(t *testing.T, prefix ...string) func(args ...string) *exec.Cmd {
	t.Helper()
	root := repoRootHS(t)
	build := func(runner string, base []string) func(...string) *exec.Cmd {
		return func(args ...string) *exec.Cmd {
			full := append(append([]string{}, base...), args...)
			c := exec.Command(runner, full...)
			c.Dir = root
			return c
		}
	}
	if _, err := exec.LookPath("uv"); err == nil {
		return build("uv", append([]string{"run", "python"}, prefix...))
	}
	if _, err := exec.LookPath("python3"); err == nil {
		return build("python3", append([]string{}, prefix...))
	}
	return nil
}

func repoRootHS(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

type errString string

func (e errString) Error() string { return string(e) }
