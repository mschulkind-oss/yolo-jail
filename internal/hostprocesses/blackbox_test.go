package hostprocesses

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
)

// The black-box suite: drive the daemon over a real socket with a
// PATH-shimmed fake `ps`, covering list/tree/pid, the exit-code contract
// (0/1/2/3/124), per-request config re-read, empty-allowlist, and the
// failure/edge paths (non-string mode, tree timeout, tree ps-nonzero-empty ->
// exit 0). Byte-level where the fake ps makes output deterministic. The daemon
// runs in-process (BuildHandler + hostservice.Serve).

func startDaemon(t *testing.T, configPath, fakePSDir string) (sock string, stop func()) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "yj-hp-bb-")
	if err != nil {
		t.Fatal(err)
	}
	sock = filepath.Join(dir, "hp.sock")
	// Prepend the fake-ps dir to PATH for the daemon's exec of `ps`.
	oldPath := os.Getenv("PATH")
	os.Setenv("PATH", fakePSDir+":"+oldPath)
	stopCh := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = hostservice.Serve(BuildHandler(configPath), sock, stopCh)
		close(done)
	}()
	waitSock(t, sock)
	return sock, func() {
		os.Setenv("PATH", oldPath)
		close(stopCh)
		<-done
		os.RemoveAll(dir)
	}
}

func waitSock(t *testing.T, sock string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("unix", sock, time.Second); err == nil {
			c.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("daemon socket never appeared")
}

// query sends a request and returns (stdout, stderr, rc).
func query(t *testing.T, sock string, req map[string]any) ([]byte, []byte, int) {
	t.Helper()
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	body, _ := json.Marshal(req)
	if err := frameproto.WriteRequest(c, body); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr []byte
	for {
		f, err := frameproto.ReadFrame(c)
		if err != nil {
			return stdout, stderr, -999
		}
		switch f.StreamID {
		case frameproto.StreamStdout:
			stdout = append(stdout, f.Payload...)
		case frameproto.StreamStderr:
			stderr = append(stderr, f.Payload...)
		case frameproto.StreamExit:
			rc, _ := frameproto.ExitCode(f.Payload)
			return stdout, stderr, rc
		}
	}
}

func writeConfig(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "yolo-jail.jsonc")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// fakePS writes a fake `ps` that echoes its argv (deterministic; real ps has
// volatile fields). Optional behavior knobs via extra shell.
func fakePS(t *testing.T, extra string) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "yj-fakeps-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	script := "#!/bin/sh\n" + extra
	if err := os.WriteFile(filepath.Join(dir, "ps"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestBlackboxListMode(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns ps; -short")
	}
	cfgDir := t.TempDir()
	cfg := writeConfig(t, cfgDir, `{"host_processes":{"visible":["sway","waykeeper"],"fields":["pid","comm"]}}`)
	ps := fakePS(t, `echo "ARGS: $*"`+"\n")
	sock, stop := startDaemon(t, cfg, ps)
	defer stop()

	out, _, rc := query(t, sock, map[string]any{"mode": "list"})
	if rc != 0 {
		t.Fatalf("list rc=%d, want 0", rc)
	}
	// sorted comms, -C per comm.
	if string(out) != "ARGS: -o pid,comm -C sway -C waykeeper\n" {
		t.Errorf("list argv = %q", out)
	}
}

func TestBlackboxEmptyAllowlistExit3(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	cfgDir := t.TempDir()
	cfg := writeConfig(t, cfgDir, `{"host_processes":{"visible":[]}}`)
	ps := fakePS(t, "echo x\n")
	sock, stop := startDaemon(t, cfg, ps)
	defer stop()
	_, stderr, rc := query(t, sock, map[string]any{"mode": "list"})
	if rc != 3 {
		t.Errorf("empty allowlist rc=%d, want 3", rc)
	}
	if string(stderr) != "host_processes.visible is empty in yolo-jail.jsonc — nothing to show\n" {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestBlackboxNonStringModeExit2(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	cfgDir := t.TempDir()
	cfg := writeConfig(t, cfgDir, `{"host_processes":{"visible":["sway"]}}`)
	ps := fakePS(t, "echo x\n")
	sock, stop := startDaemon(t, cfg, ps)
	defer stop()
	// A non-string mode (5) must be rejected exit 2, NOT silently run list.
	_, stderr, rc := query(t, sock, map[string]any{"mode": 5})
	if rc != 2 {
		t.Errorf("non-string mode rc=%d, want 2", rc)
	}
	if string(stderr) != "unknown mode: '5'\n" {
		t.Errorf("stderr = %q, want \"unknown mode: '5'\\n\"", stderr)
	}
}

func TestBlackboxUnknownModeExit2(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	cfgDir := t.TempDir()
	cfg := writeConfig(t, cfgDir, `{"host_processes":{"visible":["sway"]}}`)
	ps := fakePS(t, "echo x\n")
	sock, stop := startDaemon(t, cfg, ps)
	defer stop()
	_, stderr, rc := query(t, sock, map[string]any{"mode": "bogus"})
	if rc != 2 || string(stderr) != "unknown mode: 'bogus'\n" {
		t.Errorf("unknown mode: rc=%d stderr=%q", rc, stderr)
	}
}

func TestBlackboxTreeNonzeroEmptyExit0(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	cfgDir := t.TempDir()
	cfg := writeConfig(t, cfgDir, `{"host_processes":{"visible":["sway"]}}`)
	// fake ps exits 1 with EMPTY stdout -> stdout is read regardless ->
	// exit 0 empty, NOT an error.
	ps := fakePS(t, "exit 1\n")
	sock, stop := startDaemon(t, cfg, ps)
	defer stop()
	out, stderr, rc := query(t, sock, map[string]any{"mode": "tree"})
	if rc != 0 {
		t.Errorf("tree ps-nonzero-empty rc=%d, want 0 (stdout read regardless of exit)", rc)
	}
	if len(out) != 0 || len(stderr) != 0 {
		t.Errorf("tree ps-nonzero-empty out=%q stderr=%q, want empty", out, stderr)
	}
}

func TestBlackboxPidModeNotAllowlisted(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	cfgDir := t.TempDir()
	cfg := writeConfig(t, cfgDir, `{"host_processes":{"visible":["definitely-not-our-comm"]}}`)
	ps := fakePS(t, "echo x\n")
	sock, stop := startDaemon(t, cfg, ps)
	defer stop()
	// Our own pid's comm won't be in the allowlist -> exit 2.
	_, stderr, rc := query(t, sock, map[string]any{"mode": "pid", "pid": os.Getpid()})
	if rc != 2 {
		t.Errorf("pid not-allowlisted rc=%d, want 2 (stderr=%q)", rc, stderr)
	}
}

func TestBlackboxConfigReReadBetweenRequests(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	cfgDir := t.TempDir()
	cfg := writeConfig(t, cfgDir, `{"host_processes":{"visible":[]}}`)
	ps := fakePS(t, `echo "ARGS: $*"`+"\n")
	sock, stop := startDaemon(t, cfg, ps)
	defer stop()
	// First request: empty allowlist -> exit 3.
	if _, _, rc := query(t, sock, map[string]any{"mode": "list"}); rc != 3 {
		t.Fatalf("pre-edit rc=%d, want 3", rc)
	}
	// Edit config between requests — daemon re-reads per request.
	writeConfig(t, cfgDir, `{"host_processes":{"visible":["sway"],"fields":["pid"]}}`)
	out, _, rc := query(t, sock, map[string]any{"mode": "list"})
	if rc != 0 {
		t.Fatalf("post-edit rc=%d, want 0", rc)
	}
	if string(out) != "ARGS: -o pid -C sway\n" {
		t.Errorf("post-edit argv = %q (config not re-read?)", out)
	}
}
