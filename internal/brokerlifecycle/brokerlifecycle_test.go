package brokerlifecycle

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"
)

// fakeDeps returns a Deps wired to a temp dir with controllable probes. The
// filesystem paths are real temp files (so BrokerReadPID / os.WriteFile /
// removeIgnoreMissing exercise the real code), but liveness / ping / kill /
// pgrep / spawn are fakes.
type fakeState struct {
	alive  map[int]bool
	pingOK bool
	pgrep  []int
	killed []struct { // ordered log of (pid, sig)
		pid int
		sig syscall.Signal
	}
	spawnPID    int
	spawnExited bool
	spawnErr    error
	spawnArgv   []string
	now         time.Time
	mu          sync.Mutex
}

func newFakeDeps(t *testing.T, st *fakeState) Deps {
	t.Helper()
	dir := t.TempDir()
	st.now = time.Unix(1000, 0)
	return Deps{
		SocketPath:  filepath.Join(dir, "broker.sock"),
		PIDFilePath: filepath.Join(dir, "broker.pid"),
		LockPath:    filepath.Join(dir, "broker.lock"),
		LogPath:     filepath.Join(dir, "broker.log"),
		Now:         func() time.Time { return st.now },
		Sleep:       func(d time.Duration) { st.now = st.now.Add(d) },
		PathExists:  func(p string) bool { _, err := os.Lstat(p); return err == nil },
		Ping:        func(string, time.Duration) bool { return st.pingOK },
		Alive: func(pid int) bool {
			st.mu.Lock()
			defer st.mu.Unlock()
			return st.alive[pid]
		},
		Kill: func(pid int, sig syscall.Signal) error {
			st.mu.Lock()
			defer st.mu.Unlock()
			st.killed = append(st.killed, struct {
				pid int
				sig syscall.Signal
			}{pid, sig})
			return nil
		},
		Pgrep:   func() []int { return st.pgrep },
		Getenv:  func(string) string { return "" },
		IsExecX: func(string) bool { return false },
		Spawn: func(argv []string, _ string) (int, func() bool, error) {
			st.spawnArgv = argv
			if st.spawnErr != nil {
				return 0, nil, st.spawnErr
			}
			return st.spawnPID, func() bool { return st.spawnExited }, nil
		},
		Out: os.Stderr,
	}
}

func writePID(t *testing.T, deps Deps, pid int) {
	t.Helper()
	if err := os.WriteFile(deps.PIDFilePath, []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func touch(t *testing.T, p string) {
	t.Helper()
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBrokerReadPID(t *testing.T) {
	st := &fakeState{}
	deps := newFakeDeps(t, st)
	if _, ok := BrokerReadPID(deps); ok {
		t.Error("absent pid file should be (0,false)")
	}
	if err := os.WriteFile(deps.PIDFilePath, []byte("  4242\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pid, ok := BrokerReadPID(deps)
	if !ok || pid != 4242 {
		t.Errorf("got (%d,%v), want (4242,true)", pid, ok)
	}
	// Malformed → (0,false).
	if err := os.WriteFile(deps.PIDFilePath, []byte("notanint"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := BrokerReadPID(deps); ok {
		t.Error("malformed pid file should be (0,false)")
	}
}

func TestBrokerStatusShape(t *testing.T) {
	// Fully healthy: pid live + socket + ping.
	st := &fakeState{alive: map[int]bool{99: true}, pingOK: true}
	deps := newFakeDeps(t, st)
	writePID(t, deps, 99)
	touch(t, deps.SocketPath)
	got := BrokerStatus(deps)
	want := Status{
		PID: 99, PIDPresent: true, PIDLive: true,
		SocketExists: true, PingOK: true,
		Socket: deps.SocketPath, PIDFile: deps.PIDFilePath,
	}
	if got != want {
		t.Errorf("status = %+v, want %+v", got, want)
	}

	// No PID file: not running.
	st2 := &fakeState{}
	deps2 := newFakeDeps(t, st2)
	got2 := BrokerStatus(deps2)
	if got2.PIDPresent || got2.PIDLive || got2.SocketExists || got2.PingOK {
		t.Errorf("empty status wrong: %+v", got2)
	}
	if got2.Socket != deps2.SocketPath || got2.PIDFile != deps2.PIDFilePath {
		t.Errorf("display paths wrong: %+v", got2)
	}
}

func TestBrokerStatusPingGatedOnSocket(t *testing.T) {
	// ping is probed only when the socket exists. Even if Ping would return
	// true, a missing socket must yield PingOK=false without dialing.
	pinged := false
	st := &fakeState{alive: map[int]bool{5: true}}
	deps := newFakeDeps(t, st)
	deps.Ping = func(string, time.Duration) bool { pinged = true; return true }
	writePID(t, deps, 5)
	// No socket touched.
	got := BrokerStatus(deps)
	if got.PingOK {
		t.Error("PingOK must be false when socket absent")
	}
	if pinged {
		t.Error("Ping must not be dialed when socket absent")
	}
}

func TestBrokerIsAliveAllFourGates(t *testing.T) {
	mk := func(pidLive, sock, ping bool) Deps {
		alive := map[int]bool{7: pidLive}
		st := &fakeState{alive: alive, pingOK: ping}
		deps := newFakeDeps(t, st)
		writePID(t, deps, 7)
		if sock {
			touch(t, deps.SocketPath)
		}
		return deps
	}
	if !BrokerIsAlive(mk(true, true, true)) {
		t.Error("all-true should be alive")
	}
	if BrokerIsAlive(mk(false, true, true)) {
		t.Error("dead pid → not alive")
	}
	if BrokerIsAlive(mk(true, false, true)) {
		t.Error("missing socket → not alive")
	}
	if BrokerIsAlive(mk(true, true, false)) {
		t.Error("failed ping → not alive")
	}
	// Missing pid file → not alive.
	st := &fakeState{}
	if BrokerIsAlive(newFakeDeps(t, st)) {
		t.Error("no pid file → not alive")
	}
}

func TestBrokerKillNothingRunning(t *testing.T) {
	st := &fakeState{}
	deps := newFakeDeps(t, st)
	// Stale socket present, no pid file, pgrep empty.
	touch(t, deps.SocketPath)
	if BrokerKill(deps, syscall.SIGTERM, BrokerKillTimeout) {
		t.Error("should return false when nothing running")
	}
	if len(st.killed) != 0 {
		t.Errorf("no PID should be signaled, got %v", st.killed)
	}
	// Stale socket cleaned.
	if deps.PathExists(deps.SocketPath) {
		t.Error("stale socket should be removed")
	}
}

func TestBrokerKillGracefulExit(t *testing.T) {
	st := &fakeState{alive: map[int]bool{123: true}}
	deps := newFakeDeps(t, st)
	writePID(t, deps, 123)
	touch(t, deps.SocketPath)
	// The process exits after the first SIGTERM: flip alive on the first Kill.
	deps.Kill = func(pid int, sig syscall.Signal) error {
		st.killed = append(st.killed, struct {
			pid int
			sig syscall.Signal
		}{pid, sig})
		if sig == syscall.SIGTERM {
			st.alive[pid] = false
		}
		return nil
	}
	if !BrokerKill(deps, syscall.SIGTERM, BrokerKillTimeout) {
		t.Error("should return true when a broker was running")
	}
	if len(st.killed) != 1 || st.killed[0].sig != syscall.SIGTERM || st.killed[0].pid != 123 {
		t.Errorf("expected one SIGTERM to 123, got %v", st.killed)
	}
	if deps.PathExists(deps.PIDFilePath) || deps.PathExists(deps.SocketPath) {
		t.Error("pid file + socket should be cleaned up")
	}
}

func TestBrokerKillEscalatesToSIGKILL(t *testing.T) {
	st := &fakeState{alive: map[int]bool{55: true}}
	deps := newFakeDeps(t, st)
	writePID(t, deps, 55)
	// Never dies from SIGTERM → escalates to SIGKILL after the timeout.
	if !BrokerKill(deps, syscall.SIGTERM, 300*time.Millisecond) {
		t.Error("want true")
	}
	var sawTerm, sawKill bool
	for _, k := range st.killed {
		if k.pid == 55 && k.sig == syscall.SIGTERM {
			sawTerm = true
		}
		if k.pid == 55 && k.sig == syscall.SIGKILL {
			sawKill = true
		}
	}
	if !sawTerm || !sawKill {
		t.Errorf("expected SIGTERM then SIGKILL, got %v", st.killed)
	}
}

func TestBrokerKillPgrepFallback(t *testing.T) {
	// No PID file → discover strays via pgrep.
	st := &fakeState{alive: map[int]bool{900: false, 901: false}, pgrep: []int{900, 901}}
	deps := newFakeDeps(t, st)
	if !BrokerKill(deps, syscall.SIGTERM, BrokerKillTimeout) {
		t.Error("want true (strays found)")
	}
	if len(st.killed) != 2 {
		t.Errorf("both strays should be signaled, got %v", st.killed)
	}
}

func TestBrokerSpawnArgv(t *testing.T) {
	got := BrokerSpawnArgv([]string{"yolo-claude-oauth-broker-host"}, "/tmp/yolo-claude-oauth-broker.sock")
	want := []string{"yolo-claude-oauth-broker-host", "--socket", "/tmp/yolo-claude-oauth-broker.sock"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("argv = %v, want %v", got, want)
	}
	// With a gated Go binary launcher (two-token launcher), the tail is preserved.
	got2 := BrokerSpawnArgv([]string{"/opt/dist/yolo-claude-oauth-broker-host"}, "/s.sock")
	want2 := []string{"/opt/dist/yolo-claude-oauth-broker-host", "--socket", "/s.sock"}
	if !reflect.DeepEqual(got2, want2) {
		t.Errorf("argv = %v, want %v", got2, want2)
	}
}

func TestBrokerSpawnHappy(t *testing.T) {
	st := &fakeState{spawnPID: 4321}
	deps := newFakeDeps(t, st)
	// The spawned daemon binds its socket "immediately": create it so
	// brokerWaitForSocket returns on the first poll.
	touch(t, deps.SocketPath)
	sock := BrokerSpawn(deps)
	if sock != deps.SocketPath {
		t.Errorf("spawn returned %q, want %q", sock, deps.SocketPath)
	}
	// Argv is byte-exact.
	wantArgv := []string{BrokerConsoleName, "--socket", deps.SocketPath}
	if !reflect.DeepEqual(st.spawnArgv, wantArgv) {
		t.Errorf("spawn argv = %v, want %v", st.spawnArgv, wantArgv)
	}
	// PID file written.
	pid, ok := BrokerReadPID(deps)
	if !ok || pid != 4321 {
		t.Errorf("pid file = (%d,%v), want (4321,true)", pid, ok)
	}
}

func TestBrokerSpawnSkipsWhenAlreadyAlive(t *testing.T) {
	// Inside the lock, a live broker means the race loser returns without
	// spawning.
	st := &fakeState{alive: map[int]bool{11: true}, pingOK: true}
	deps := newFakeDeps(t, st)
	writePID(t, deps, 11)
	touch(t, deps.SocketPath)
	_ = BrokerSpawn(deps)
	if st.spawnArgv != nil {
		t.Errorf("should not spawn when already alive, got argv %v", st.spawnArgv)
	}
}

func TestBrokerSpawnStaleSocketCleared(t *testing.T) {
	// Not alive (no pid), stale socket present → BrokerSpawn unlinks it before
	// spawning. The fake Spawn does not re-bind, so after spawn the socket is
	// gone and the wait times out (D12: PID file left for status).
	st := &fakeState{spawnPID: 7, spawnExited: false}
	deps := newFakeDeps(t, st)
	touch(t, deps.SocketPath)
	_ = BrokerSpawn(deps)
	if st.spawnArgv == nil {
		t.Fatal("should have spawned")
	}
	if deps.PathExists(deps.SocketPath) {
		t.Error("stale socket should have been cleared before spawn")
	}
	// PID file is still present (D12 behavior).
	if _, ok := BrokerReadPID(deps); !ok {
		t.Error("PID file should be left for `yolo broker status` after a failed bind")
	}
}

func TestBrokerSpawnDeadChildFast(t *testing.T) {
	// A child that exits immediately without binding → brokerWaitForSocket
	// returns fast (exited() true), no full-deadline burn.
	st := &fakeState{spawnPID: 8, spawnExited: true}
	deps := newFakeDeps(t, st)
	start := deps.Now()
	_ = BrokerSpawn(deps)
	// With the fake clock, Sleep advances now; a dead-child fast path means at
	// most one poll interval elapsed.
	if deps.Now().Sub(start) > SocketPollInterval {
		t.Errorf("dead child should short-circuit; elapsed %v", deps.Now().Sub(start))
	}
}

func TestDaemonLauncherDefault(t *testing.T) {
	st := &fakeState{}
	deps := newFakeDeps(t, st)
	// The console script IS the Go binary on PATH now → bare name, always.
	// (The former YOLO_GO_DAEMONS/YOLO_GO_BIN_DIR seam was dead code, removed.)
	got := DaemonLauncher(deps, BrokerConsoleName)
	if !reflect.DeepEqual(got, []string{BrokerConsoleName}) {
		t.Errorf("got %v, want [%s]", got, BrokerConsoleName)
	}
}

func TestBrokerLogPathString(t *testing.T) {
	t.Setenv("HOME", "/home/x")
	want := "/home/x/.local/share/yolo-jail/logs/host-service-claude-oauth-broker.log"
	if got := BrokerLogPath(); got != want {
		t.Errorf("log path = %q, want %q", got, want)
	}
}
