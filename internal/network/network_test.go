package network

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func TestParsePortForwards(t *testing.T) {
	entries := []any{
		jsonx.IntValue(8000), // int -> (8000,8000)
		"9000",               // plain string -> (9000,9000)
		"1234:5678",          // mapped -> (1234,5678)
		[]any{"nope"},        // invalid -> warned + skipped
	}
	var warnings []string
	got, err := ParsePortForwards(entries, func(s string) { warnings = append(warnings, s) })
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []PortForward{{8000, 8000}, {9000, 9000}, {1234, 5678}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parsed = %v, want %v", got, want)
	}
	if len(warnings) != 1 || warnings[0] != "Warning: invalid port forward entry: [nope]" {
		t.Errorf("warnings = %v", warnings)
	}
	// Non-numeric string aborts (Python raises ValueError).
	if _, err := ParsePortForwards([]any{"abc"}, nil); err == nil {
		t.Error("non-numeric string should error")
	}
	// split-once: "1:2:3" -> a="1", b="2:3" which is non-numeric -> error.
	if _, err := ParsePortForwards([]any{"1:2:3"}, nil); err == nil {
		t.Error("'1:2:3' should error on the non-numeric second half")
	}
}

func TestSocatArgv(t *testing.T) {
	got := SocatArgv("/tmp/yolo-fwd/port-8000.sock", 8000)
	want := []string{
		"socat",
		"UNIX-LISTEN:/tmp/yolo-fwd/port-8000.sock,fork,mode=777",
		"TCP:127.0.0.1:8000",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("argv = %v", got)
	}
}

func TestSocketPath(t *testing.T) {
	if got := SocketPath("/tmp/yolo-fwd", 8000); got != "/tmp/yolo-fwd/port-8000.sock" {
		t.Errorf("sock path = %q", got)
	}
}

func TestSocketNotReadyWarning(t *testing.T) {
	want := "Warning: socat socket(s) not ready after 2.0s: /a.sock, /b.sock"
	if got := SocketNotReadyWarning([]string{"/a.sock", "/b.sock"}); got != want {
		t.Errorf("warning = %q", got)
	}
}

// TestParityVsLivePython cross-checks parse results + socat argv + warning texts
// against the live network.py. Skips without Python.
func TestParityVsLivePython(t *testing.T) {
	py := pythonRunner(t)
	if py == nil {
		t.Skip("python unavailable")
	}
	script := `
import sys; sys.path.insert(0, 'src')
import json
from pathlib import Path
from cli.network import _parse_port_forwards, SOCKET_WAIT_DEADLINE_SECONDS
parsed = _parse_port_forwards([8000, "9000", "1234:5678"])
# Reconstruct the socat argv the way start_host_port_forwarding does.
sd = Path("/tmp/yolo-fwd")
argvs = []
for lp, hp in parsed:
    sp = sd / f"port-{lp}.sock"
    argvs.append(["socat", f"UNIX-LISTEN:{sp},fork,mode=777", f"TCP:127.0.0.1:{hp}"])
out = {
  "parsed": [[a, b] for a, b in parsed],
  "argvs": argvs,
  "warn": f"Warning: socat socket(s) not ready after {SOCKET_WAIT_DEADLINE_SECONDS}s: /a.sock, /b.sock",
}
print(json.dumps(out))
`
	outBytes, err := py("-c", script).Output()
	if err != nil {
		t.Skipf("python network import failed: %v", err)
	}
	var want struct {
		Parsed [][]int    `json:"parsed"`
		Argvs  [][]string `json:"argvs"`
		Warn   string     `json:"warn"`
	}
	if err := json.Unmarshal(outBytes, &want); err != nil {
		t.Fatalf("decode: %v", err)
	}
	goParsed, err := ParsePortForwards([]any{jsonx.IntValue(8000), "9000", "1234:5678"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(goParsed) != len(want.Parsed) {
		t.Fatalf("parsed count go=%d py=%d", len(goParsed), len(want.Parsed))
	}
	for i, w := range want.Parsed {
		if goParsed[i].LocalPort != w[0] || goParsed[i].HostPort != w[1] {
			t.Errorf("parsed[%d] go=%v py=%v", i, goParsed[i], w)
		}
		goArgv := SocatArgv(SocketPath("/tmp/yolo-fwd", goParsed[i].LocalPort), goParsed[i].HostPort)
		if !reflect.DeepEqual(goArgv, want.Argvs[i]) {
			t.Errorf("argv[%d]:\n go: %v\n py: %v", i, goArgv, want.Argvs[i])
		}
	}
	if got := SocketNotReadyWarning([]string{"/a.sock", "/b.sock"}); got != want.Warn {
		t.Errorf("warn:\n go: %q\n py: %q", got, want.Warn)
	}
}

func pythonRunner(t *testing.T) func(args ...string) *exec.Cmd {
	t.Helper()
	root := repoRoot(t)
	if _, err := exec.LookPath("uv"); err == nil {
		return func(args ...string) *exec.Cmd {
			c := exec.Command("uv", append([]string{"run", "python"}, args...)...)
			c.Dir = root
			return c
		}
	}
	if _, err := exec.LookPath("python3"); err == nil {
		return func(args ...string) *exec.Cmd {
			c := exec.Command("python3", args...)
			c.Dir = root
			return c
		}
	}
	return nil
}

func repoRoot(t *testing.T) string {
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
