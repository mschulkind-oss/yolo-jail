// Package brokerlifecycle provides the Claude OAuth broker singleton lifecycle
// helpers. The broker is a host-wide daemon —
// one per host, serving every running jail — so these helpers inspect, ping,
// spawn, and kill that singleton.
//
// This package is the lifecycle engine consumed by internal/brokercmd (the
// `yolo broker {status,stop,restart,logs}` command bodies). Every side effect
// (process liveness, kill, spawn, socket ping, filesystem, clock) is behind an
// injectable Deps seam so the whole lifecycle is unit-testable against a fake
// socket/pid without a live host daemon (the pscmd/loopholes precedent).
//
// The socket/pid/lock PATH strings are cross-language singleton contracts: a
// Python yolo and a Go yolo on the same host MUST agree on them or they'd spawn
// two brokers. They are byte-identical to loopholes_runtime.BROKER_SINGLETON_*.
package brokerlifecycle

import (
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/execx"
	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// Singleton path constants — byte-identical to loopholes_runtime:
//
//	BROKER_SINGLETON_SOCKET = Path("/tmp/yolo-claude-oauth-broker.sock")
//	BROKER_SINGLETON_PID_FILE = Path("/tmp/yolo-claude-oauth-broker.pid")
//	BROKER_SINGLETON_LOCK = Path("/tmp/yolo-claude-oauth-broker.lock")
//	BROKER_LOOPHOLE_NAME = "claude-oauth-broker"
//
// The socket lives under /tmp so AF_UNIX path-length limits aren't a concern
// (108 bytes on Linux, 104 on macOS) and a host reboot leaves a clean slate.
const (
	BrokerSingletonSocket  = "/tmp/yolo-claude-oauth-broker.sock"
	BrokerSingletonPIDFile = "/tmp/yolo-claude-oauth-broker.pid"
	BrokerSingletonLock    = "/tmp/yolo-claude-oauth-broker.lock"
	BrokerLoopholeName     = "claude-oauth-broker"

	// BrokerConsoleName is the LEGACY standalone console-script / Go-binary name
	// the singleton used to be spawned as. It is retained ONLY as a pgrep
	// pattern (RealPgrepStrays), so a broker started by a not-yet-upgraded yolo
	// on the same host is still discoverable for one release. The current spawn
	// form is `yolo internal daemon claude-oauth-broker` (see BrokerSpawnArgv).
	BrokerConsoleName = "yolo-claude-oauth-broker-host"

	// BrokerDaemonPattern matches the current spawn form's argv
	// ("<yolo> internal daemon claude-oauth-broker …") for pgrep. It is the
	// forward half of the dual-pattern in RealPgrepStrays.
	BrokerDaemonPattern = "internal daemon claude-oauth-broker"
)

// Timing knobs — behavior-identical to the historical hardcoded values in
// loopholes_runtime (TIGHT poll interval, GENEROUS deadline).
const (
	// BrokerSpawnTimeout is the deadline for a just-spawned broker to bind its
	// socket (BROKER_SPAWN_TIMEOUT = 5.0).
	BrokerSpawnTimeout = 5 * time.Second
	// SocketPollInterval is the poll interval for socket-appearance and
	// PID-exit waits (SOCKET_POLL_INTERVAL = 0.05).
	SocketPollInterval = 50 * time.Millisecond
	// BrokerKillTimeout is _broker_kill's default SIGTERM grace before SIGKILL
	// (the `timeout: float = 3.0` default).
	BrokerKillTimeout = 3 * time.Second
	// PingTimeout is _broker_ping's default `timeout=2.0`.
	PingTimeout = 2 * time.Second
)

// Status is the snapshot _broker_status returns: pid (present?), pid liveness,
// socket presence, ping result, and the display path strings. Python models
// absent pid as None; here PIDPresent=false plays that role.
type Status struct {
	PID          int
	PIDPresent   bool // pid is not None
	PIDLive      bool
	SocketExists bool
	PingOK       bool
	Socket       string // display path (== Deps.SocketPath)
	PIDFile      string // display path (== Deps.PIDFilePath)
}

// Deps are the injectable seams. RealDeps wires them to the real singleton
// paths, process signals, socket ping, filesystem, and clock; tests substitute
// fakes. The path fields default to the /tmp singleton constants but are fields
// (not consts) so a test can retarget them at a temp dir instead of clobbering a
// real host broker.
type Deps struct {
	SocketPath  string
	PIDFilePath string
	LockPath    string
	LogPath     string // GLOBAL_STORAGE/logs/host-service-claude-oauth-broker.log
	Now         func() time.Time
	Sleep       func(time.Duration)
	PathExists  func(string) bool

	// Ping dials socketPath and runs the frame-protocol ping (see BrokerPing).
	Ping func(socketPath string, timeout time.Duration) bool
	// Alive reports process liveness (kill(pid,0) tri-state collapsed to bool:
	// EPERM counts as alive).
	Alive func(pid int) bool
	// Kill sends sig to pid (os.kill). Errors are swallowed by callers.
	Kill func(pid int, sig syscall.Signal) error
	// Pgrep returns PIDs of stray broker-host processes (current + legacy spawn
	// forms; see RealPgrepStrays), already self-filtered (os.getpid() excluded).
	Pgrep func() []int

	// Getenv / accessX back DaemonLauncher (the YOLO_GO_DAEMONS resolution).
	Getenv  func(string) string
	IsExecX func(path string) bool
	// Spawn launches the broker daemon detached (own session, stdout+stderr to
	// logPath, close_fds), returning its PID and a poll func reporting whether
	// it has exited.
	// close_fds=True) + proc.poll().
	Spawn func(argv []string, logPath string) (pid int, exited func() bool, err error)

	Out io.Writer // launcher warnings (info-parity, Go-native)
}

// RealDeps returns Deps backed by the real singleton paths and OS effects.
func RealDeps() Deps {
	return Deps{
		SocketPath:  BrokerSingletonSocket,
		PIDFilePath: BrokerSingletonPIDFile,
		LockPath:    BrokerSingletonLock,
		LogPath:     BrokerLogPath(),

		Now:        time.Now,
		Sleep:      time.Sleep,
		PathExists: func(p string) bool { _, err := os.Lstat(p); return err == nil },
		Ping:       BrokerPing,
		Alive:      execx.IsAlive,
		Kill:       func(pid int, sig syscall.Signal) error { return syscall.Kill(pid, sig) },
		Pgrep:      RealPgrepStrays,
		Getenv:     os.Getenv,
		IsExecX: func(p string) bool {
			info, err := os.Stat(p)
			return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
		},
		Spawn: realSpawn,
		Out:   os.Stdout,
	}
}

// BrokerLogPath returns GLOBAL_STORAGE/logs/host-service-claude-oauth-broker.log
// the singleton's shared log (one across every jail).
func BrokerLogPath() string {
	return filepath.Join(paths.GlobalStorage(), "logs", "host-service-claude-oauth-broker.log")
}

// BrokerReadPID ports _broker_read_pid: the integer PID from the singleton PID
// file, or (0,false) if the file is absent / unreadable / malformed.
func BrokerReadPID(deps Deps) (int, bool) {
	raw, err := os.ReadFile(deps.PIDFilePath)
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0, false
	}
	return n, true
}

// BrokerStatus ports _broker_status: pid (present?), pid_live, socket_exists,
// ping_ok, plus the display paths. ping is probed only when the socket exists
// (matching the Python `sock_exists and _broker_ping(...)`).
func BrokerStatus(deps Deps) Status {
	pid, present := BrokerReadPID(deps)
	pidLive := present && deps.Alive(pid)
	sockExists := deps.PathExists(deps.SocketPath)
	pingOK := sockExists && deps.Ping(deps.SocketPath, PingTimeout)
	return Status{
		PID:          pid,
		PIDPresent:   present,
		PIDLive:      pidLive,
		SocketExists: sockExists,
		PingOK:       pingOK,
		Socket:       deps.SocketPath,
		PIDFile:      deps.PIDFilePath,
	}
}

// BrokerIsAlive ports _broker_is_alive: PID file present + PID live + socket
// present + ping round-trips. All four must hold.
func BrokerIsAlive(deps Deps) bool {
	pid, present := BrokerReadPID(deps)
	if !present || !deps.Alive(pid) {
		return false
	}
	if !deps.PathExists(deps.SocketPath) {
		return false
	}
	return deps.Ping(deps.SocketPath, PingTimeout)
}

// BrokerKill ports _broker_kill: send sig to the singleton (PID file first, else
// pgrep-discovered strays), wait for every signaled PID to exit (escalating to
// SIGKILL on stragglers), then remove the PID file + socket. Returns true iff a
// broker was running (something to signal); false if nothing was running (still
// clears a stale socket). Preserves the SIGTERM-then-wait-then-SIGKILL sequence.
func BrokerKill(deps Deps, sig syscall.Signal, timeout time.Duration) bool {
	var pids []int
	if primary, ok := BrokerReadPID(deps); ok {
		pids = append(pids, primary)
	} else {
		pids = append(pids, deps.Pgrep()...)
	}

	if len(pids) == 0 {
		// Nothing to kill — still remove a stale socket so the next spawn gets
		// a clean slate (unlink, ignore missing).
		removeIgnoreMissing(deps.SocketPath)
		return false
	}

	// Signal every PID. A ProcessLookupError/OSError is swallowed (continue);
	// the pid stays in `survivors` and the liveness filter drops the dead ones.
	for _, pid := range pids {
		_ = deps.Kill(pid, sig)
	}

	// Wait for every signaled PID to actually exit before declaring success.
	deadline := deps.Now().Add(timeout)
	survivors := append([]int(nil), pids...)
	for len(survivors) > 0 && deps.Now().Before(deadline) {
		survivors = liveOnly(deps, survivors)
		if len(survivors) > 0 {
			deps.Sleep(SocketPollInterval)
		}
	}
	// Escalate to SIGKILL on stragglers.
	for _, pid := range survivors {
		_ = deps.Kill(pid, syscall.SIGKILL)
	}

	// Cleanup: PID file then socket (unlink, ignore missing).
	removeIgnoreMissing(deps.PIDFilePath)
	removeIgnoreMissing(deps.SocketPath)
	return true
}

// liveOnly returns the subset of pids that are still alive.
func liveOnly(deps Deps, pids []int) []int {
	var out []int
	for _, p := range pids {
		if deps.Alive(p) {
			out = append(out, p)
		}
	}
	return out
}

// BrokerSpawnArgv builds the singleton spawn argv from a yolo-binary launcher
// prefix: [*launcher, "internal", "daemon", "claude-oauth-broker", "--socket",
// <socketPath>]. In production `launcher` is the self-exec'd running yolo (see
// BrokerSpawn), so the broker host daemon is served by re-execing THIS binary
// as `yolo internal daemon claude-oauth-broker`. Tests pass a literal launcher
// to assert the expansion.
func BrokerSpawnArgv(launcher []string, socketPath string) []string {
	argv := append([]string{}, launcher...)
	return append(argv, "internal", "daemon", BrokerLoopholeName, "--socket", socketPath)
}

// BrokerSpawn ports _broker_spawn: flock the lock file, re-check liveness inside
// the lock (the race loser returns without spawning), clear any stale socket,
// resolve the launcher, spawn the daemon detached, write the PID file, and wait
// for the socket to bind. Returns the socket path regardless of outcome (Python
// leaves the PID file for `yolo broker status` when the bind fails).
func BrokerSpawn(deps Deps) string {
	_ = os.MkdirAll(filepath.Dir(deps.LockPath), 0o755)
	lockF, err := os.OpenFile(deps.LockPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		// Cannot take the lock file at all — best-effort, return the socket path.
		return deps.SocketPath
	}
	defer lockF.Close()
	if err := syscall.Flock(int(lockF.Fd()), syscall.LOCK_EX); err != nil {
		return deps.SocketPath
	}

	if BrokerIsAlive(deps) {
		return deps.SocketPath
	}

	// Clean any stale socket left by a crashed prior broker; a second bind(2)
	// on a stale path fails with EADDRINUSE.
	removeIgnoreMissing(deps.SocketPath)

	// Self-exec: the launcher is the running yolo binary, so the spawned
	// `yolo internal daemon claude-oauth-broker` re-execs THIS process rather
	// than resolving "yolo" on PATH.
	launcher := execx.SelfExecArgv([]string{"yolo"})
	argv := BrokerSpawnArgv(launcher, deps.SocketPath)
	pid, exited, err := deps.Spawn(argv, deps.LogPath)
	if err != nil {
		// Popen would raise in Python; return the socket path and let the
		// caller's liveness re-check report the failure (divergence D12).
		return deps.SocketPath
	}
	_ = os.WriteFile(deps.PIDFilePath, []byte(strconv.Itoa(pid)+"\n"), 0o644)
	brokerWaitForSocket(deps, deps.SocketPath, BrokerSpawnTimeout, exited)
	return deps.SocketPath
}

// brokerWaitForSocket ports _broker_wait_for_socket: poll until the socket
// appears or the deadline elapses; a dead child (exited() true) is a genuine
// failure detected in milliseconds. Returns whether the socket exists at the end.
func brokerWaitForSocket(deps Deps, sock string, timeout time.Duration, exited func() bool) bool {
	deadline := deps.Now().Add(timeout)
	for deps.Now().Before(deadline) {
		if deps.PathExists(sock) {
			return true
		}
		if exited != nil && exited() {
			return deps.PathExists(sock)
		}
		deps.Sleep(SocketPollInterval)
	}
	return deps.PathExists(sock)
}

// DaemonLauncher resolves a console-script daemon name to its launch argv.
// The console script IS the Go binary on PATH now, so this returns the name
// unconditionally (Python did no PATH-existence check at the tail — that
// nil-vs-bare-name difference vs runcmd's daemonLauncher is preserved pending
// their unification). The former YOLO_GO_DAEMONS/YOLO_GO_BIN_DIR migration seam
// (dead — nothing set those vars) was removed.
func DaemonLauncher(_ Deps, consoleName string) []string {
	return []string{consoleName}
}

// BrokerPing ports _broker_ping: connect to socketPath, send the length-prefixed
// {"action":"ping"} request byte-for-byte, and expect a pong:true data frame
// (stream 0) before the exit frame (stream 2). Any error → false (a boolean
// liveness probe). Reuses internal/frameproto for the frozen frame protocol and
// internal/jsonx for the pong decode.
func BrokerPing(socketPath string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("unix", socketPath, timeout)
	if err != nil {
		return false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	// Send the exact bytes Python sends (b'{"action":"ping"}'), not a
	// re-serialized map, so the request is byte-faithful.
	if err := frameproto.WriteRequest(conn, []byte(`{"action":"ping"}`)); err != nil {
		return false
	}
	for {
		frame, err := frameproto.ReadFrame(conn)
		if err != nil {
			return false
		}
		switch frame.StreamID {
		case frameproto.StreamStdout:
			decoded, err := jsonx.Decode(frame.Payload)
			if err != nil {
				return false
			}
			obj, ok := decoded.(*jsonx.OrderedMap)
			if !ok {
				return false
			}
			pong, _ := obj.Get("pong")
			b, _ := pong.(bool)
			return b
		case frameproto.StreamExit:
			// Exit frame without a pong on stream 0 → not alive.
			return false
		}
		// Any other stream id (e.g. 1=stderr): keep reading (Python loops).
	}
}

// RealPgrepStrays ports _broker_pgrep_strays: PIDs of running broker-host
// processes the OS knows about, regardless of PID-file state, with our own PID
// filtered out. A missing pgrep / timeout / error yields no PIDs (never an error
// the "tool absent = no-op" invariant).
//
// Dual-pattern for ONE release: the pgrep regex matches BOTH the current spawn
// form ("<yolo> internal daemon claude-oauth-broker …", BrokerDaemonPattern) AND
// the legacy standalone binary name (BrokerConsoleName). Without the legacy
// alternative a broker still running from a pre-self-exec yolo on this host
// would be invisible to `yolo broker {stop,restart}`, leaking a stray daemon.
// Drop the legacy alternative next release.
func RealPgrepStrays() []int {
	cmd := exec.Command("pgrep", "-f", BrokerDaemonPattern+"|"+BrokerConsoleName)
	out, err := cmd.Output()
	if err != nil {
		// Non-zero rc (no match) or spawn failure → nothing to reap.
		return nil
	}
	self := os.Getpid()
	var pids []int
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(line)
		if err != nil {
			continue
		}
		if pid == self {
			continue
		}
		pids = append(pids, pid)
	}
	return pids
}

// realSpawn launches the broker daemon detached and returns its PID plus an
// exited() poll. The log fd is intentionally left open for the child's lifetime
// (Python's _broker_spawn never closes it either). A background Wait reaps the
// child so exited() reflects real state (poll() semantics) without leaving a
// zombie during the socket wait.
func realSpawn(argv []string, logPath string) (int, func() bool, error) {
	_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
	cmd := exec.Command(argv[0], argv[1:]...)
	if lf, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		cmd.Stdout, cmd.Stderr = lf, lf
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, nil, err
	}
	var done int32
	go func() { _ = cmd.Wait(); atomic.StoreInt32(&done, 1) }()
	pid := cmd.Process.Pid
	exited := func() bool { return atomic.LoadInt32(&done) == 1 }
	return pid, exited, nil
}

// removeIgnoreMissing unlinks p, ignoring a not-exist error (Python's
// try/except FileNotFoundError: pass).
func removeIgnoreMissing(p string) {
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		// Other errors (e.g. permission) are swallowed like the Python path,
		// which only guards FileNotFoundError but runs in a context where the
		// files are ours.
		_ = err
	}
}
