// Package broker provides the Claude OAuth broker singleton — both the lifecycle
// engine and the `yolo broker {status,stop,restart,logs}` command bodies. The
// broker is a host-wide daemon — one per host, serving every running jail — so
// the lifecycle helpers inspect, probe, spawn, and kill that singleton.
//
// THE ENGINE IS NO LONGER THE BROKER'S ALONE. `host_daemon.scope: "host"`
// (loopholedecl.ScopeHost) is the manifest vocabulary for "one daemon per host,
// serving every jail", and the run pipeline honors it through SingletonDeps below
// — which is this same flock-recheck-spawn-wait sequence with the paths and the
// argv supplied by the loophole's own record instead of by the constants here.
// The broker is the only loophole that declares it today, and the package keeps
// its name because the `yolo broker` COMMAND is genuinely broker-specific; what
// generalized is the lifecycle, not the CLI.
//
// The lifecycle engine (this file) is consumed by the command layer (brokercmd.go)
// in the same package. Every side effect (process liveness, kill, spawn, socket
// reachability, filesystem, clock) is behind an injectable Deps seam so the whole
// lifecycle is unit-testable against a fake socket/pid without a live host daemon
// (the pscmd/loopholes precedent). The command layer wraps that lifecycle Deps in
// its own CLIDeps (console writers + tail runner).
//
// The socket/pid/lock PATH strings are cross-language singleton contracts: a
// Python yolo and a Go yolo on the same host MUST agree on them or they'd spawn
// two brokers. They are byte-identical to loopholes_runtime.BROKER_SINGLETON_*.
package broker

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
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
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
	// ReachTimeout is the singleton reachability probe's deadline (the historical
	// `timeout=2.0` the frame-protocol ping used).
	ReachTimeout = 2 * time.Second
)

// Status is the snapshot _broker_status returns: pid (present?), pid liveness,
// socket presence, reachability, and the display path strings. Python models
// absent pid as None; here PIDPresent=false plays that role.
type Status struct {
	PID          int
	PIDPresent   bool // pid is not None
	PIDLive      bool
	SocketExists bool
	// Reachable is the socket's ACCEPT answer, not a protocol round trip — see
	// SingletonReachable for why the frame-protocol ping this replaced can no
	// longer be spoken from the host side.
	Reachable bool
	Socket    string // display path (== Deps.SocketPath)
	PIDFile   string // display path (== Deps.PIDFilePath)
}

// Deps are the injectable seams. RealDeps wires them to the real singleton
// paths, process signals, socket reachability, filesystem, and clock; tests
// substitute fakes. The path fields default to the /tmp singleton constants but
// are fields (not consts) so a test can retarget them at a temp dir instead of
// clobbering a real host broker — and so SingletonDeps can derive them from a
// loophole name that is not the broker's.
type Deps struct {
	SocketPath  string
	PIDFilePath string
	LockPath    string
	LogPath     string // GLOBAL_STORAGE/logs/host-service-claude-oauth-broker.log
	Now         func() time.Time
	Sleep       func(time.Duration)
	PathExists  func(string) bool

	// Reachable reports whether the singleton's socket accepts a connection (see
	// SingletonReachable).
	Reachable func(socketPath string, timeout time.Duration) bool
	// Alive reports process liveness (kill(pid,0) tri-state collapsed to bool:
	// EPERM counts as alive).
	Alive func(pid int) bool
	// Kill sends sig to pid (os.kill). Errors are swallowed by callers.
	Kill func(pid int, sig syscall.Signal) error
	// Pgrep returns PIDs of stray broker-host processes (current + legacy spawn
	// forms; see RealPgrepStrays), already self-filtered (os.getpid() excluded).
	Pgrep func() []int

	// Spawn launches the broker daemon detached (own session, stdout+stderr to
	// logPath, close_fds), returning its PID and a poll func reporting whether
	// it has exited.
	// close_fds=True) + proc.poll().
	Spawn func(argv []string, logPath string) (pid int, exited func() bool, err error)

	// Argv is the singleton's spawn argv, already fully substituted (the running
	// yolo's own path at argv[0] where the manifest wrote the bare `yolo` token,
	// and SocketPath in place of `{socket}`).
	//
	// IT IS A FIELD RATHER THAN A CONSTRUCTION so the argv can come from the
	// loophole's MANIFEST — which is what makes `scope: "host"` a declaration
	// rather than a second name for the broker. RealDeps fills it with the
	// broker's own for the `yolo broker` command paths, which have no record to
	// read; SingletonDeps fills it from the record.
	Argv []string

	// Out receives launcher warnings (info-parity, Go-native) — today the one
	// reportFailedSpawn emits. A nil writer silences them, so a zero-value Deps
	// in a test stays quiet without wiring anything.
	Out io.Writer
	// Color requests ANSI markup on Out. Resolve it to (wanted && on a TTY)
	// before setting, exactly as CLIDeps does: this layer never probes the
	// terminal, so a redirected launch log stays clean by the caller's choice.
	Color bool
}

// RealDeps returns Deps backed by the real singleton paths and OS effects.
func RealDeps() Deps {
	return SingletonDeps(BrokerLoopholeName,
		BrokerSpawnArgv(execx.SelfExecArgv([]string{"yolo"}), BrokerSingletonSocket))
}

// SingletonDeps returns Deps for the host-wide daemon of the loophole named
// `name`, spawned with `argv`. It is the general form of RealDeps: every path is
// derived from the loophole name (paths.HostSingleton*) and the argv comes from
// the caller, so a `host_daemon.scope: "host"` record drives this engine without
// anything here knowing which loophole it is.
//
// For claude-oauth-broker the derived paths are BYTE-IDENTICAL to the
// BrokerSingleton* constants — TestSingletonPathsMatchTheBrokerConstants pins
// that, and it has to hold or `yolo broker status`, `yolo check`'s broker section
// and the run pipeline's front would each ensure or inspect a different file.
func SingletonDeps(name string, argv []string) Deps {
	return Deps{
		SocketPath:  paths.HostSingletonSocket(name),
		PIDFilePath: paths.HostSingletonPIDFile(name),
		LockPath:    paths.HostSingletonLock(name),
		LogPath:     SingletonLogPath(name),
		Argv:        argv,

		Now:        time.Now,
		Sleep:      time.Sleep,
		PathExists: func(p string) bool { _, err := os.Lstat(p); return err == nil },
		Reachable:  SingletonReachable,
		Alive:      execx.IsAlive,
		Kill:       func(pid int, sig syscall.Signal) error { return syscall.Kill(pid, sig) },
		Pgrep:      RealPgrepStrays,
		Spawn:      realSpawn,
		Out:        os.Stdout,
		Color:      isTTYStdoutReal(),
	}
}

// BrokerLogPath returns GLOBAL_STORAGE/logs/host-service-claude-oauth-broker.log
// the singleton's shared log (one across every jail).
func BrokerLogPath() string { return SingletonLogPath(BrokerLoopholeName) }

// SingletonLogPath returns a host-wide daemon's shared log — ONE file across
// every jail, unlike a per-jail daemon's, and spelled `host-service-<name>.log`
// so it lands beside them and `yolo check`'s "see <log>" advice reads the same
// either way (internal/cli/run's startExternalService builds the same path).
func SingletonLogPath(loopholeName string) string {
	return filepath.Join(paths.GlobalStorage(), "logs", "host-service-"+loopholeName+".log")
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
// reachable, plus the display paths. Reachability is probed only when the socket
// exists (matching the Python `sock_exists and _broker_ping(...)`).
func BrokerStatus(deps Deps) Status {
	pid, present := BrokerReadPID(deps)
	pidLive := present && deps.Alive(pid)
	sockExists := deps.PathExists(deps.SocketPath)
	reachable := sockExists && deps.Reachable(deps.SocketPath, ReachTimeout)
	return Status{
		PID:          pid,
		PIDPresent:   present,
		PIDLive:      pidLive,
		SocketExists: sockExists,
		Reachable:    reachable,
		Socket:       deps.SocketPath,
		PIDFile:      deps.PIDFilePath,
	}
}

// BrokerIsAlive ports _broker_is_alive: PID file present + PID live + socket
// present + socket reachable. All four must hold.
func BrokerIsAlive(deps Deps) bool {
	pid, present := BrokerReadPID(deps)
	if !present || !deps.Alive(pid) {
		return false
	}
	if !deps.PathExists(deps.SocketPath) {
		return false
	}
	return deps.Reachable(deps.SocketPath, ReachTimeout)
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

	// A Deps with no argv cannot spawn anything, and saying so beats handing an
	// empty argv to exec.Command — which indexes argv[0] and panics. It is
	// reachable only through a hand-built Deps (both constructors fill the field),
	// so the report is for whoever built one, not for a user.
	if len(deps.Argv) == 0 {
		if deps.Out != nil {
			richtext.Printer{W: deps.Out, Color: deps.Color}.Print(
				"[yellow]Warning: the host-wide daemon at " + deps.SocketPath +
					" has no spawn argv; nothing was started.[/yellow]")
		}
		return deps.SocketPath
	}

	// Clean any stale socket left by a crashed prior broker; a second bind(2)
	// on a stale path fails with EADDRINUSE.
	removeIgnoreMissing(deps.SocketPath)

	// The argv is the CALLER'S, and for the broker it is self-exec'd: the launcher
	// is the running yolo binary, so the spawned
	// `yolo internal daemon claude-oauth-broker` re-execs THIS process rather than
	// resolving "yolo" on PATH (RealDeps and the run pipeline's record-driven
	// SingletonDeps both apply execx.SelfExecArgv before they get here).
	pid, exited, err := deps.Spawn(deps.Argv, deps.LogPath)
	if err != nil {
		// Popen would raise in Python; return the socket path and let the
		// caller's liveness re-check report the failure (divergence D12).
		return deps.SocketPath
	}
	_ = os.WriteFile(deps.PIDFilePath, []byte(strconv.Itoa(pid)+"\n"), 0o644)
	if !brokerWaitForSocket(deps, deps.SocketPath, BrokerSpawnTimeout, exited) {
		reportFailedSpawn(deps, exited)
	}
	return deps.SocketPath
}

// reportFailedSpawn writes the line brokerWaitForSocket's return value exists
// FOR. The detector has always been able to separate a dead singleton from a
// slow one in milliseconds — its own doc comment below says exactly that — and
// the caller here threw the answer away. That is how a broker which died at
// startup 2,549 times in a single jail stayed invisible for months: the only
// record was a log nobody reads, and the consequence surfaced three layers later
// as a refused launch (docs/design/broker-ca-and-nested-hosts.md §3.1).
//
// Deliberately NOT fatal, and BrokerSpawn's return value is unchanged. The
// broker is a host-wide singleton; a jail without Claude auth is degraded, not
// unlaunchable, and the reachability witness is already the gate that refuses.
// This is the diagnostic that names why that gate is about to fire — emitted at
// the moment the fact is known rather than inferred later from its effects.
//
// The wording deliberately reuses the sibling host-service warning's shape
// (internal/cli/run/loopholesruntime.go: what failed, what was expected, and the
// log that holds the reason) instead of inventing a second warning grammar for
// the same class of event — the two print into the same launch output.
//
// The two fault classes are told apart because they send the reader to different
// places: an exit means the log's tail IS the reason (the missing-openssl case
// is one stderr line), while a timeout means the process is still alive and
// stuck, and the log may hold nothing at all.
func reportFailedSpawn(deps Deps, exited func() bool) {
	if deps.Out == nil {
		return
	}
	reason := "did not bind its socket within " + BrokerSpawnTimeout.String()
	if exited != nil && exited() {
		reason = "exited at startup without binding its socket"
	}
	richtext.Printer{W: deps.Out, Color: deps.Color}.Print(
		"[yellow]Warning: the Claude OAuth broker singleton " + reason +
			" — in-jail Claude auth will fail until it does. Expected " +
			deps.SocketPath + "; see " + deps.LogPath + "[/yellow]")
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

// SingletonReachable reports whether the host-wide daemon's socket ACCEPTS a
// connection. Connect, then close — no bytes are written and none are read.
//
// # It used to be a frame-protocol ping, and it cannot be one any more
//
// This function was `BrokerPing`: dial, write `{"action":"ping"}` as a framed
// request, expect a `pong:true` data frame. That worked while the singleton's
// socket was host-to-host and the first thing on the wire was the client's own
// request. It is now a FRONTED socket (`publishes: "socket"` + `scope: "host"`),
// so every connection the daemon accepts begins with yolo's CONNECTION PREAMBLE
// (svcendpoint/preamble.go) and a bare `{"action":"ping"}` is read as a preamble
// with no `v` — rejected, connection dropped, no pong. The ping would report every
// healthy broker as dead, and brokerEnsure would respawn the singleton on every
// single launch.
//
// The obvious repair — have the prober write a preamble too — is the wrong one and
// is deliberately not taken: the preamble is yolo asserting WHICH JAIL is on the
// other end, and a host-side liveness probe belongs to no jail. Forging one would
// put a fabricated jail_id in the daemon's audit line and would make
// svcendpoint's "yolo is the only producer" a convention instead of a property of
// the type system (encodePreamble is unexported for exactly that reason).
//
// So liveness for a fronted daemon is what it already is everywhere else in the
// tree: its socket accepts a connection (internal/cli/run's socketConnectable,
// the readiness predicate for every other `publishes: "socket"` daemon). The
// preamble reader is built to survive this — a connect-and-close "degrades exactly
// as 'closed before a request' already does", one log line and nothing else.
//
// WHAT THIS NO LONGER CATCHES, stated rather than glossed: a daemon whose accept
// loop is alive but whose handler is wedged now reads as reachable, where the ping
// would have called it dead. The end-to-end protocol check survives in the one
// place it can still be spoken — `yolo check`'s per-jail probe, which goes THROUGH
// the front and therefore sends a real preamble before its ping (internal/cli/check).
func SingletonReachable(socketPath string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("unix", socketPath, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
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
