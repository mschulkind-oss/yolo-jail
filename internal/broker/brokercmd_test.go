package broker

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

// lifeState is a controllable Deps backed by a temp dir.
type lifeState struct {
	alive     map[int]bool
	pingOK    bool
	killed    []int
	spawnPID  int
	spawnBind bool // does the spawned daemon bind the socket?
}

func newLifeDeps(t *testing.T, st *lifeState) Deps {
	t.Helper()
	dir := t.TempDir()
	if st.alive == nil {
		st.alive = map[int]bool{}
	}
	return Deps{
		SocketPath:  filepath.Join(dir, "broker.sock"),
		PIDFilePath: filepath.Join(dir, "broker.pid"),
		LockPath:    filepath.Join(dir, "broker.lock"),
		LogPath:     filepath.Join(dir, "broker.log"),
		Now:         time.Now,
		Sleep:       func(time.Duration) {},
		PathExists:  func(p string) bool { _, err := os.Lstat(p); return err == nil },
		Ping:        func(string, time.Duration) bool { return st.pingOK },
		Alive:       func(pid int) bool { return st.alive[pid] },
		Kill: func(pid int, _ syscall.Signal) error {
			st.killed = append(st.killed, pid)
			st.alive[pid] = false
			return nil
		},
		Pgrep: func() []int { return nil },
		Spawn: func(argv []string, _ string) (int, func() bool, error) {
			if st.spawnBind {
				_ = os.WriteFile(filepath.Join(dir, "broker.sock"), nil, 0o644)
				st.alive[st.spawnPID] = true
			}
			return st.spawnPID, func() bool { return !st.spawnBind }, nil
		},
		Out: os.Stderr,
	}
}

func newDeps(t *testing.T, st *lifeState) (CLIDeps, *bytes.Buffer) {
	t.Helper()
	life := newLifeDeps(t, st)
	var buf bytes.Buffer
	return CLIDeps{
		Life:      life,
		Out:       &buf,
		Err:       &buf,
		Color:     false,
		LogPath:   life.LogPath,
		LogIsFile: func(p string) bool { info, err := os.Stat(p); return err == nil && info.Mode().IsRegular() },
		RunTail:   func([]string) error { return nil },
	}, &buf
}

func TestStatusHealthy(t *testing.T) {
	st := &lifeState{alive: map[int]bool{77: true}, pingOK: true}
	deps, buf := newDeps(t, st)
	// pid file + socket present.
	_ = os.WriteFile(deps.Life.PIDFilePath, []byte("77\n"), 0o644)
	_ = os.WriteFile(deps.Life.SocketPath, nil, 0o644)
	rc := PrintStatus(deps)
	if rc != 0 {
		t.Errorf("healthy status rc = %d, want 0", rc)
	}
	out := buf.String()
	for _, want := range []string{
		"Claude OAuth broker (singleton)",
		"pid:          77  live",
		"socket:       " + deps.Life.SocketPath + "  present",
		"ping:         ok",
		"pid file:     " + deps.Life.PIDFilePath,
		"Broker healthy.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestStatusNotRunning(t *testing.T) {
	st := &lifeState{}
	deps, buf := newDeps(t, st)
	rc := PrintStatus(deps)
	if rc != 1 {
		t.Errorf("not-running status rc = %d, want 1", rc)
	}
	out := buf.String()
	if !strings.Contains(out, "not running") || !strings.Contains(out, "(no PID file)") {
		t.Errorf("missing not-running line:\n%s", out)
	}
	if !strings.Contains(out, "socket:") || !strings.Contains(out, "missing") {
		t.Errorf("missing socket-missing line:\n%s", out)
	}
	if !strings.Contains(out, "ping:") || !strings.Contains(out, "no response") {
		t.Errorf("missing ping line:\n%s", out)
	}
	if !strings.Contains(out, "Broker not fully healthy.") || !strings.Contains(out, "yolo broker restart") {
		t.Errorf("missing cycle hint:\n%s", out)
	}
}

func TestStatusDeadPIDExit1(t *testing.T) {
	// PID present but not live → degraded → exit 1.
	st := &lifeState{alive: map[int]bool{5: false}}
	deps, buf := newDeps(t, st)
	_ = os.WriteFile(deps.Life.PIDFilePath, []byte("5\n"), 0o644)
	rc := PrintStatus(deps)
	if rc != 1 {
		t.Errorf("dead-pid status rc = %d, want 1", rc)
	}
	if !strings.Contains(buf.String(), "pid:          5  dead") {
		t.Errorf("missing dead mark:\n%s", buf.String())
	}
}

func TestStopRunning(t *testing.T) {
	st := &lifeState{alive: map[int]bool{42: true}}
	deps, buf := newDeps(t, st)
	_ = os.WriteFile(deps.Life.PIDFilePath, []byte("42\n"), 0o644)
	rc := Stop(deps)
	if rc != 0 {
		t.Errorf("stop rc = %d, want 0", rc)
	}
	if !strings.Contains(buf.String(), "Stopped broker.") {
		t.Errorf("missing stopped line:\n%s", buf.String())
	}
	if len(st.killed) != 1 || st.killed[0] != 42 {
		t.Errorf("expected SIGTERM to 42, got %v", st.killed)
	}
}

func TestStopNothingRunning(t *testing.T) {
	st := &lifeState{}
	deps, buf := newDeps(t, st)
	rc := Stop(deps)
	if rc != 0 {
		t.Errorf("stop rc = %d, want 0", rc)
	}
	if !strings.Contains(buf.String(), "No broker was running.") {
		t.Errorf("missing no-broker line:\n%s", buf.String())
	}
}

func TestRestartSuccess(t *testing.T) {
	// Old broker (pid 1) running; kill it, spawn pid 2 which binds + becomes live.
	st := &lifeState{alive: map[int]bool{1: true}, pingOK: true, spawnPID: 2, spawnBind: true}
	deps, buf := newDeps(t, st)
	_ = os.WriteFile(deps.Life.PIDFilePath, []byte("1\n"), 0o644)
	_ = os.WriteFile(deps.Life.SocketPath, nil, 0o644)
	rc := Restart(deps)
	if rc != 0 {
		t.Errorf("restart rc = %d, want 0", rc)
	}
	out := buf.String()
	if !strings.Contains(out, "Broker restarted.") {
		t.Errorf("missing restarted line:\n%s", out)
	}
	if !strings.Contains(out, "socket="+deps.Life.SocketPath) {
		t.Errorf("missing socket= hint:\n%s", out)
	}
	if len(st.killed) == 0 {
		t.Error("old broker should have been killed")
	}
}

func TestRestartFailure(t *testing.T) {
	// Spawn does not bind → not alive after spawn → exit 1 + log-path hint.
	st := &lifeState{spawnPID: 9, spawnBind: false}
	deps, buf := newDeps(t, st)
	rc := Restart(deps)
	if rc != 1 {
		t.Errorf("failed restart rc = %d, want 1", rc)
	}
	out := buf.String()
	if !strings.Contains(out, "Broker failed to become live after spawn.") {
		t.Errorf("missing failure line:\n%s", out)
	}
	if !strings.Contains(out, "Check "+deps.LogPath) {
		t.Errorf("missing log-path hint:\n%s", out)
	}
}

func TestLogsNoFile(t *testing.T) {
	st := &lifeState{}
	deps, buf := newDeps(t, st)
	ran := false
	deps.RunTail = func([]string) error { ran = true; return nil }
	rc := Logs(deps, 50, false)
	if rc != 0 {
		t.Errorf("logs rc = %d, want 0", rc)
	}
	if ran {
		t.Error("tail should not run when the log file is absent")
	}
	if !strings.Contains(buf.String(), "No log file yet at "+deps.LogPath) {
		t.Errorf("missing no-log line:\n%s", buf.String())
	}
}

func TestLogsTailArgv(t *testing.T) {
	st := &lifeState{}
	deps, _ := newDeps(t, st)
	_ = os.WriteFile(deps.LogPath, []byte("line\n"), 0o644)
	var gotArgv []string
	deps.RunTail = func(argv []string) error { gotArgv = argv; return nil }

	rc := Logs(deps, 120, false)
	if rc != 0 {
		t.Errorf("rc = %d", rc)
	}
	want := []string{"tail", "-n120", deps.LogPath}
	if !reflect.DeepEqual(gotArgv, want) {
		t.Errorf("argv = %v, want %v", gotArgv, want)
	}

	// -f appends before the path.
	rc = Logs(deps, 10, true)
	if rc != 0 {
		t.Errorf("rc = %d", rc)
	}
	wantF := []string{"tail", "-n10", "-f", deps.LogPath}
	if !reflect.DeepEqual(gotArgv, wantF) {
		t.Errorf("follow argv = %v, want %v", gotArgv, wantF)
	}
}

func TestBuildTailArgv(t *testing.T) {
	cases := []struct {
		lines  int
		follow bool
		want   []string
	}{
		{50, false, []string{"tail", "-n50", "/l"}},
		{50, true, []string{"tail", "-n50", "-f", "/l"}},
		{0, false, []string{"tail", "-n0", "/l"}},
	}
	for _, c := range cases {
		got := BuildTailArgv(c.lines, c.follow, "/l")
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("BuildTailArgv(%d,%v) = %v, want %v", c.lines, c.follow, got, c.want)
		}
	}
}

func TestColorMarkupRendersANSI(t *testing.T) {
	st := &lifeState{alive: map[int]bool{1: true}, pingOK: true}
	deps, buf := newDeps(t, st)
	deps.Color = true
	_ = os.WriteFile(deps.Life.PIDFilePath, []byte("1\n"), 0o644)
	_ = os.WriteFile(deps.Life.SocketPath, nil, 0o644)
	PrintStatus(deps)
	out := buf.String()
	if !strings.Contains(out, "\x1b[") {
		t.Errorf("color mode should emit ANSI escapes:\n%q", out)
	}
	// No raw markup tags should leak through.
	if strings.Contains(out, "[green]") || strings.Contains(out, "[/green]") {
		t.Errorf("raw markup leaked:\n%q", out)
	}
}
