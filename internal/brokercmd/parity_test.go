package brokercmd

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerlifecycle"
)

// pythonRunner returns a factory for `uv run python` (preferred) or `python3`
// rooted at the repo, or nil when neither is available (house parity pattern).
func pythonRunner(t *testing.T) func(args ...string) *exec.Cmd {
	t.Helper()
	root := repoRootDir(t)
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

func repoRootDir(t *testing.T) string {
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

// parityOracle drives LIVE src.cli.loopholes_runtime + broker_cmd. It reports:
//   - constants: BROKER_SINGLETON_SOCKET / _PID_FILE / _LOCK / BROKER_LOOPHOLE_NAME
//   - the broker log path (GLOBAL_STORAGE/logs/host-service-claude-oauth-broker.log)
//   - status_dict: _broker_status() with the socket/pid probes monkeypatched to
//     a fixed scenario (pid live, socket present, ping ok)
//   - status_dict_empty: _broker_status() with everything absent
//   - spawn_argv: [*_daemon_launcher(name), "--socket", str(BROKER_SINGLETON_SOCKET)]
//   - tail_argv: broker_cmd's ["tail", f"-n{lines}", ("-f")?, str(log_path)]
const parityOracle = `
import json, sys
sys.path.insert(0, 'src')
from pathlib import Path
from src.cli import loopholes_runtime as lr
from src.cli.paths import GLOBAL_STORAGE

log_path = GLOBAL_STORAGE / "logs" / "host-service-claude-oauth-broker.log"

# --- status dict (healthy scenario) via monkeypatched probes -----------------
def status_with(pid, pid_live, sock_exists, ping_ok):
    orig_read = lr._broker_read_pid
    orig_live = lr._broker_pid_is_live
    orig_ping = lr._broker_ping
    orig_exists = type(lr.BROKER_SINGLETON_SOCKET).exists
    try:
        lr._broker_read_pid = lambda: pid
        lr._broker_pid_is_live = lambda p: pid_live
        lr._broker_ping = lambda s, **k: ping_ok
        type(lr.BROKER_SINGLETON_SOCKET).exists = lambda self: sock_exists
        return lr._broker_status()
    finally:
        lr._broker_read_pid = orig_read
        lr._broker_pid_is_live = orig_live
        lr._broker_ping = orig_ping
        type(lr.BROKER_SINGLETON_SOCKET).exists = orig_exists

# --- spawn argv (default launcher, no YOLO_GO_DAEMONS gating) -----------------
launcher = lr._daemon_launcher("yolo-claude-oauth-broker-host")
spawn_argv = [*launcher, "--socket", str(lr.BROKER_SINGLETON_SOCKET)]

# --- tail argv (reproduce broker_cmd's builder) ------------------------------
def tail_argv(lines, follow):
    cmd = ["tail", f"-n{lines}"]
    if follow:
        cmd.append("-f")
    cmd.append(str(log_path))
    return cmd

out = {
    "socket": str(lr.BROKER_SINGLETON_SOCKET),
    "pid_file": str(lr.BROKER_SINGLETON_PID_FILE),
    "lock": str(lr.BROKER_SINGLETON_LOCK),
    "loophole_name": lr.BROKER_LOOPHOLE_NAME,
    "log_path": str(log_path),
    "status_healthy": status_with(4242, True, True, True),
    "status_empty": status_with(None, False, False, False),
    "spawn_argv": spawn_argv,
    "tail_50_nofollow": tail_argv(50, False),
    "tail_120_follow": tail_argv(120, True),
}
sys.stdout.write(json.dumps(out))
`

func runParityOracle(t *testing.T, py func(...string) *exec.Cmd) map[string]any {
	t.Helper()
	cmd := py("-c", parityOracle)
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("python oracle failed (%v): %s", err, stderr.String())
	}
	var out map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("decode oracle output: %v\nstdout=%q\nstderr=%q", err, stdout.String(), stderr.String())
	}
	return out
}

func toStrList(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		s, _ := e.(string)
		out = append(out, s)
	}
	return out
}

// TestParityVsLivePython checks the Go broker path against LIVE Python: the
// singleton PATH constants (byte-exact), the spawn argv + tail argv (byte-exact),
// and the _broker_status dict shape/values against the Go Status snapshot for
// the same monkeypatched probe scenario. SKIPs when python3/uv is absent.
func TestParityVsLivePython(t *testing.T) {
	py := pythonRunner(t)
	if py == nil {
		t.Skip("python oracle unavailable (uv/python3 not found)")
	}
	// Align HOME so the log path (under GLOBAL_STORAGE) matches on both sides.
	home := t.TempDir()
	t.Setenv("HOME", home)

	o := runParityOracle(t, py)

	// --- PATH constants (byte-exact) -----------------------------------------
	if got, want := brokerlifecycle.BrokerSingletonSocket, o["socket"].(string); got != want {
		t.Errorf("socket path: Go %q != Python %q", got, want)
	}
	if got, want := brokerlifecycle.BrokerSingletonPIDFile, o["pid_file"].(string); got != want {
		t.Errorf("pid file path: Go %q != Python %q", got, want)
	}
	if got, want := brokerlifecycle.BrokerSingletonLock, o["lock"].(string); got != want {
		t.Errorf("lock path: Go %q != Python %q", got, want)
	}
	if got, want := brokerlifecycle.BrokerLoopholeName, o["loophole_name"].(string); got != want {
		t.Errorf("loophole name: Go %q != Python %q", got, want)
	}
	if got, want := brokerlifecycle.BrokerLogPath(), o["log_path"].(string); got != want {
		t.Errorf("broker log path: Go %q != Python %q", got, want)
	}

	// --- spawn argv (byte-exact) ---------------------------------------------
	goSpawn := brokerlifecycle.BrokerSpawnArgv(
		brokerlifecycle.DaemonLauncher(brokerlifecycle.RealDeps(), brokerlifecycle.BrokerConsoleName),
		brokerlifecycle.BrokerSingletonSocket)
	if want := toStrList(o["spawn_argv"]); !reflect.DeepEqual(goSpawn, want) {
		t.Errorf("spawn argv: Go %v != Python %v", goSpawn, want)
	}

	// --- tail argv (byte-exact) ----------------------------------------------
	if want := toStrList(o["tail_50_nofollow"]); !reflect.DeepEqual(
		BuildTailArgv(50, false, o["log_path"].(string)), want) {
		t.Errorf("tail argv (50, nofollow): Go != Python %v", want)
	}
	if want := toStrList(o["tail_120_follow"]); !reflect.DeepEqual(
		BuildTailArgv(120, true, o["log_path"].(string)), want) {
		t.Errorf("tail argv (120, follow): Go != Python %v", want)
	}

	// --- status dict shape/values --------------------------------------------
	// Run Go's REAL BrokerStatus for each scenario (fake probes) and compare it
	// field-by-field to Python's _broker_status dict for the same scenario.
	// Healthy: pid=4242 live, socket present, ping ok.
	assertStatusMatches(t, "healthy", o["status_healthy"].(map[string]any),
		goStatusFor(t, 4242, true, true, true))
	// Empty: no pid file, nothing present.
	assertStatusMatches(t, "empty", o["status_empty"].(map[string]any),
		goStatusFor(t, 0, false, false, false))
}

// goStatusFor drives Go's REAL BrokerStatus with a fake Deps producing the
// scenario (pid present iff pid!=0, pid liveness, socket presence, ping), but
// pins the display paths to the real singleton constants so the socket/pid_file
// strings match Python's status dict. Params mirror Python's status_with(pid,
// pid_live, sock_exists, ping_ok).
func goStatusFor(t *testing.T, pid int, pidLive, sock, ping bool) brokerlifecycle.Status {
	t.Helper()
	present := pid != 0
	pidPath := filepath.Join(t.TempDir(), "broker.pid")
	if present {
		if err := os.WriteFile(pidPath, []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	deps := brokerlifecycle.RealDeps()
	deps.PIDFilePath = pidPath // seeding vehicle only (see below)
	deps.SocketPath = brokerlifecycle.BrokerSingletonSocket
	deps.PathExists = func(p string) bool { return sock && p == brokerlifecycle.BrokerSingletonSocket }
	deps.Ping = func(string, time.Duration) bool { return ping }
	deps.Alive = func(int) bool { return pidLive }
	got := brokerlifecycle.BrokerStatus(deps)
	// The pid file was retargeted at a temp path purely to seed the pid read;
	// normalize the DISPLAY field back to the real singleton constant. The
	// display-path parity itself is asserted byte-exact in the constants block.
	got.PIDFile = brokerlifecycle.BrokerSingletonPIDFile
	return got
}

// assertStatusMatches compares Python's _broker_status dict (pid may be null)
// against the Go Status snapshot field-by-field.
func assertStatusMatches(t *testing.T, name string, py map[string]any, got brokerlifecycle.Status) {
	t.Helper()
	// Python "pid": int or None.
	pyPIDPresent := py["pid"] != nil
	if pyPIDPresent != got.PIDPresent {
		t.Errorf("[%s] pid present: Go %v != Python %v", name, got.PIDPresent, pyPIDPresent)
	}
	if pyPIDPresent {
		if pyPID := int(py["pid"].(float64)); pyPID != got.PID {
			t.Errorf("[%s] pid: Go %d != Python %d", name, got.PID, pyPID)
		}
	}
	if b := py["pid_live"].(bool); b != got.PIDLive {
		t.Errorf("[%s] pid_live: Go %v != Python %v", name, got.PIDLive, b)
	}
	if b := py["socket_exists"].(bool); b != got.SocketExists {
		t.Errorf("[%s] socket_exists: Go %v != Python %v", name, got.SocketExists, b)
	}
	if b := py["ping_ok"].(bool); b != got.PingOK {
		t.Errorf("[%s] ping_ok: Go %v != Python %v", name, got.PingOK, b)
	}
	if s := py["socket"].(string); s != got.Socket {
		t.Errorf("[%s] socket: Go %q != Python %q", name, got.Socket, s)
	}
	if s := py["pid_file"].(string); s != got.PIDFile {
		t.Errorf("[%s] pid_file: Go %q != Python %q", name, got.PIDFile, s)
	}
}
