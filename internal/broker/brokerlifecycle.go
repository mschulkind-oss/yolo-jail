// Package broker provides the HOST-WIDE DAEMON singletons — the lifecycle engine,
// the `yolo host-daemon {status,stop,restart,logs}` command bodies, and the
// `yolo broker` alias over the Claude one. A host-wide daemon is one process per
// host serving every running jail, so the lifecycle helpers inspect, probe, spawn
// and kill such a singleton.
//
// THE ENGINE IS NO LONGER THE BROKER'S ALONE. `host_daemon.scope: "host"`
// (loopholedecl.ScopeHost) is the manifest vocabulary for "one daemon per host,
// serving every jail", and the run pipeline honors it through SingletonDeps below
// — which is this same flock-recheck-spawn-wait sequence with the paths and the
// argv supplied by the loophole's own record instead of by the constants here.
//
// NEITHER IS THE CLI, SINCE OQ-HD2. This comment used to end "the broker is the
// only loophole that declares it today, and the package keeps its name because
// the `yolo broker` COMMAND is genuinely broker-specific" — a scope that EXPIRED
// rather than being wrong: `openai-auth-broker` declared `scope: "host"` on
// 2026-09-15 and `aws-auth` made it three on 2026-09-18, at which point a
// Claude-only management verb meant one daemon with four verbs and two with none.
// The command bodies now take a Singleton (hostdaemons.go); `broker` is retained
// as an ALIAS resolving to the Claude one, which is why the package keeps its
// name. Do not restore a count here — the set is derived, and the last count in
// this comment is what dated it.
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
	"context"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/execx"
	"github.com/mschulkind-oss/yolo-jail/internal/heldchildren"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/logcap"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

// BrokerSingletonSocket / BrokerSingletonPIDFile / BrokerSingletonLock are the Claude
// broker's host-singleton paths. They are DERIVED from paths.HostSingleton* rather than
// spelled here, so `yolo broker status`, `yolo check`'s broker section and the run
// pipeline's front cannot reach different files, and a test package's private
// paths.HostSingletonDir reaches them too. In production they are, byte for byte, the
// retired Python constants: /tmp/yolo-claude-oauth-broker.{sock,pid,lock}. The socket
// lives under /tmp so AF_UNIX path-length limits aren't a concern (108 bytes on Linux,
// 104 on macOS) and a host reboot leaves a clean slate.
func BrokerSingletonSocket() string { return paths.HostSingletonSocket(BrokerLoopholeName) }

// BrokerSingletonPIDFile is the Claude broker's PID file (see BrokerSingletonSocket).
func BrokerSingletonPIDFile() string { return paths.HostSingletonPIDFile(BrokerLoopholeName) }

// BrokerSingletonLock is the Claude broker's spawn lock (see BrokerSingletonSocket).
func BrokerSingletonLock() string { return paths.HostSingletonLock(BrokerLoopholeName) }

const BrokerLoopholeName = "claude-oauth-broker"

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
// The json tags make this type `yolo broker status --format json` directly:
// every field the human report prints is already here, so the document is the
// struct rather than a second assembly of the same facts that could disagree
// with it (docs/reference/self-documenting-cli.md item 7).
type Status struct {
	PID          int  `json:"pid"`
	PIDPresent   bool `json:"pid_present"` // pid is not None
	PIDLive      bool `json:"pid_live"`
	SocketExists bool `json:"socket_exists"`
	// Reachable is the socket's ACCEPT answer, not a protocol round trip — see
	// SingletonReachable for why the frame-protocol ping this replaced can no
	// longer be spoken from the host side. The json name says which: a consumer
	// reading `reachable` would assume a round trip that never happened.
	Reachable bool   `json:"socket_accepting"`
	Socket    string `json:"socket"`   // display path (== Deps.SocketPath)
	PIDFile   string `json:"pid_file"` // display path (== Deps.PIDFilePath)
	// Healthy is the verdict the human report ends on and the exit code carries:
	// PIDLive && Reachable. It is a field rather than something a consumer
	// re-derives, because the rule for "healthy" is yolo's to change and a
	// consumer that re-implemented it would not notice when it did.
	Healthy bool `json:"healthy"`
}

// Deps are the injectable seams. RealDeps wires them to the real singleton
// paths, process signals, socket reachability, filesystem, and clock; tests
// substitute fakes. The path fields default to the /tmp singleton constants but
// are fields (not consts) so a test can retarget them at a temp dir instead of
// clobbering a real host broker — and so SingletonDeps can derive them from a
// loophole name that is not the broker's.
type Deps struct {
	// Name is the LOOPHOLE this singleton belongs to, and it exists for the
	// diagnostics rather than for the mechanics: every path below is already
	// derived from it, so nothing in the lifecycle reads it back. What reads it is
	// reportFailedSpawn, whose warning used to say "the Claude OAuth broker
	// singleton" unconditionally — true while one loophole declared
	// `scope: "host"`, and a lie once a second one did, printed at exactly the
	// moment its daemon failed to start. An empty Name degrades to the generic
	// phrasing rather than to a wrong one.
	Name        string
	SocketPath  string
	PIDFilePath string
	LockPath    string
	LogPath     string // GLOBAL_STORAGE/logs/host-service-<name>.log
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
	// Pgrep returns the PIDs of THIS singleton's strays — live processes whose argv
	// passes its socket as `--socket` (RealPgrepStrays) — already self-filtered
	// (os.getpid() excluded). BrokerKill falls back to it when the PID file is gone.
	Pgrep func() []int

	// Spawn launches the broker daemon detached (own session, stdout+stderr to
	// logPath, close_fds), returning its PID and a poll func reporting whether
	// it has exited.
	// close_fds=True) + proc.poll().
	Spawn func(argv []string, logPath string) (pid int, exited func() bool, err error)

	// SpawnWithReason launches an opted-in daemon with a private startup-reason channel.
	// The returned connection and attempt token are consumed only during this spawn's readiness wait.
	SpawnWithReason func(argv []string, logPath, service string) (pid int, exited func() bool,
		reasonConn net.Conn, attempt string, err error)
	// StartupReason enables reason transport for this manifest-declared daemon only; the
	// default false preserves the existing spawn contract for legacy singletons.
	StartupReason bool

	// PrepareLocked runs after the singleton flock is acquired and before the
	// liveness check. A non-nil returned action makes the lifecycle stop an
	// existing singleton, run the action, and only then continue to the normal
	// spawn path. It exists for one-time state migrations that must happen after
	// the daemon using the old path has stopped, with the singleton lock held
	// across the entire transition.
	PrepareLocked func() (afterStop func() error, err error)
	// PublishSettings atomically publishes the caller's already-validated frozen settings bytes
	// while this singleton's flock is held, before drift comparison or any stop/spawn transition.
	PublishSettings func() error
	// DesiredSettings is the frozen snapshot PublishSettings would publish. When set, a lock
	// failure judges the running daemon's drift against these bytes rather than the stable
	// settings file, which the failed ensure never got to write (so it may still hold the
	// previous launch's settings and would hide the drift).
	DesiredSettings []byte

	// SettingsPath is the settings file the daemon's argv hands it (the manifest's
	// `{settings}` token), or "" when it is handed none. The spawn RECORDS what that file
	// held, and the ensure restarts a live daemon whose record differs from what the file
	// holds now (settingsrecord.go). SingletonDeps derives it from the name and the argv.
	SettingsPath string

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

	// These package-private hooks make the deadline/result handoff deterministic in
	// tests; production leaves them unset and uses the real readiness poll and timer.
	waitForSocketUntil   func(string, time.Time, func() bool, chan startupReasonResult) bool
	waitForStartupReason func(chan startupReasonResult, time.Duration) (startupReasonResult, bool)
}

// RealDeps returns Deps backed by the real singleton paths and OS effects.
func RealDeps() Deps {
	return SingletonDeps(BrokerLoopholeName,
		BrokerSpawnArgv(execx.SelfExecArgv([]string{"yolo"}), BrokerSingletonSocket()))
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
	sock := paths.HostSingletonSocket(name)
	return Deps{
		Name:        name,
		SocketPath:  sock,
		PIDFilePath: paths.HostSingletonPIDFile(name),
		LockPath:    paths.HostSingletonLock(name),
		LogPath:     SingletonLogPath(name),
		Argv:        argv,
		// Derived, never passed: the settings file is a function of the loophole NAME, and
		// the argv decides only whether this daemon is handed it at all.
		SettingsPath: settingsPathIn(name, argv),

		Now:        time.Now,
		Sleep:      time.Sleep,
		PathExists: func(p string) bool { _, err := os.Lstat(p); return err == nil },
		Reachable:  SingletonReachable,
		Alive:      execx.IsAlive,
		Kill:       func(pid int, sig syscall.Signal) error { return syscall.Kill(pid, sig) },
		// Scoped to THIS singleton's socket, never to a spawn form: every singleton got
		// the Claude broker's pattern here, so stopping any of them without a PID file
		// SIGTERMed every Claude broker on the machine (strayscope_test.go).
		Pgrep:           func() []int { return RealPgrepStrays(sock) },
		Spawn:           realSpawn,
		SpawnWithReason: realSpawnWithReason,
		Out:             os.Stdout,
		// Resolved here, through the one gate, because this layer never probes
		// the terminal again (see Deps.Color).
		Color: tty.Color(nil, true, isTTYStdoutReal()),
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
		// The SAME conjunction PrintStatus's verdict and exit code use — set
		// here so there is one rule, not two that agree today.
		Healthy: pidLive && reachable,
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
	removeIgnoreMissing(singletonStampPath(deps))
	removeIgnoreMissing(launchCheckStampPath(deps))
	removeIgnoreMissing(settingsRecordPath(deps))
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
//
// It is EnsureSingleton for the callers that need only the path.
func BrokerSpawn(deps Deps) string { return EnsureSingleton(deps).Socket }

// Ensured is what EnsureSingleton leaves behind.
type Ensured struct {
	// Socket is the daemon's socket path, whatever the outcome.
	Socket string
	// Stale is non-nil when a LIVE daemon is serving settings other than the ones its
	// settings file now holds and this ensure could NOT replace it — the one outcome a
	// caller must not paper over by fronting the daemon anyway. It names changed keys only.
	Stale *SettingsDrift
	// Started is true when THIS ensure spawned the daemon, false when it reused one it found
	// alive or started nothing. A caller that finds the socket refusing right after an ensure
	// needs it: a reused daemon may have stopped since, and ensuring again starts a fresh one,
	// while one this ensure just started may still be coming up, and ensuring again would start
	// a second copy beside it (nothing here stops a live process whose socket is not bound).
	Started bool
	// SettingsErr is a failed frozen-settings publication. EnsureSingleton leaves the current
	// daemon and its settings record untouched when this is non-nil.
	SettingsErr error
	// StartupReason is the current spawn's bounded cooperative refusal, never a shared-log read.
	StartupReason *hostservice.StartupReason
	// Outcome is value-based evidence from this EnsureSingleton call. Legacy fields above remain
	// unchanged; this outcome never interprets socket-path readiness as an accepted connection.
	Outcome hostservice.StartupOutcome
}

// EnsureSingleton is BrokerSpawn with the outcome a caller can act on.
//
// A LIVE DAEMON IS REUSED ONLY WHILE IT RUNS THE CURRENT SETTINGS. Inside the flock, a live
// daemon whose settings record differs from the file its argv names — or that has no record
// at all — is stopped (the `yolo host-daemon restart` sequence: SIGTERM, a drain of in-flight
// requests, SIGKILL after BrokerKillTimeout) and respawned, with one line naming the changed
// KEYS (docs/design/host-daemon-ownership.md HD-D2). Under the flock, so two launches with
// the same new settings restart it once: the second finds the first's record matching.
//
// Why a restart is safe for the OTHER jails sharing it: each jail's front owns its own
// certificate and bearer token and dials the daemon's socket afresh for every connection
// (svcendpoint's splice), so a respawned daemon at the same path serves every existing front
// from its next request, and nothing inside a jail changes. What a restart costs them is the
// requests in flight past the drain grace, and the connections refused in the gap before the
// new daemon binds.
func EnsureSingleton(deps Deps) Ensured {
	done := Ensured{Socket: deps.SocketPath, Outcome: hostservice.StartupOutcome{
		// One EnsureSingleton call is one owner-local attempt, attributed from entry so every known
		// exit (lock, publication, preparation, migration, reuse, spawn) carries it. A caller
		// retrying the ensure renumbers its own attempts (startHostSingleton).
		Owner: hostservice.StartupOwnerSingleton, Service: deps.Name, Attempt: 1, Kind: hostservice.StartupKindUnknown,
		Phase:      hostservice.StartupPhaseLock,
		ReasonRead: hostservice.StartupReasonReadOutcome{Kind: hostservice.StartupReasonReadNotEnabled},
	}}
	_ = os.MkdirAll(filepath.Dir(deps.LockPath), 0o755)
	lockF, err := os.OpenFile(deps.LockPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		// Cannot take the lock file at all, so nothing may be spawned — two launches
		// racing without it would each start a daemon on one socket. The return value is
		// unchanged; what changed is that the refusal SAYS so, instead of leaving the
		// reachability witness to fail later naming the socket (host-daemon-ownership.md
		// OQ-HD8).
		reportLockFailure(deps, "open", err)
		done.Outcome.Kind = hostservice.StartupKindLockFailed
		done.Stale = staleUnreplaceable(deps)
		return done
	}
	defer lockF.Close()
	if err := syscall.Flock(int(lockF.Fd()), syscall.LOCK_EX); err != nil {
		// Same refusal, same reason to say it. No test drives this branch: a blocking
		// LOCK_EX on a descriptor just opened fails for an interrupted wait (EINTR) or a
		// kernel out of lock records (ENOLCK), and a unit test can arrange neither.
		reportLockFailure(deps, "lock", err)
		done.Outcome.Kind = hostservice.StartupKindLockFailed
		done.Stale = staleUnreplaceable(deps)
		return done
	}
	if deps.PublishSettings != nil {
		if err := deps.PublishSettings(); err != nil {
			done.SettingsErr = err
			done.Outcome.Kind = hostservice.StartupKindPublicationFailed
			done.Outcome.Phase = hostservice.StartupPhasePublication
			if deps.Out != nil {
				richtext.Printer{W: deps.Out, Color: deps.Color}.Print(
					"[yellow]Could not publish the validated settings for host-wide daemon '" +
						deps.Name + "'; the current daemon was left untouched.[/yellow]")
			}
			return done
		}
	}
	if deps.PrepareLocked != nil {
		afterStop, prepErr := deps.PrepareLocked()
		if prepErr != nil {
			done.Outcome.Kind = hostservice.StartupKindPreparationFailed
			done.Outcome.Phase = hostservice.StartupPhasePreparation
			if deps.Out != nil {
				richtext.Printer{W: deps.Out, Color: deps.Color}.Print(
					"[yellow]Warning: could not prepare host-wide daemon '" + deps.Name +
						"': " + prepErr.Error() + "[/yellow]")
			}
			return done
		} else if afterStop != nil {
			oldPID, oldPIDKnown := BrokerReadPID(deps)
			BrokerKill(deps, syscall.SIGTERM, BrokerKillTimeout)
			done.Outcome.PreviousStopRequested = true
			if oldPIDKnown {
				if deps.Alive(oldPID) {
					done.Outcome.PreviousProcess = hostservice.StartupProcessAlive
				} else {
					done.Outcome.PreviousProcess = hostservice.StartupProcessExited
				}
			}
			if err := afterStop(); err != nil {
				done.Outcome.Kind = hostservice.StartupKindMigrationFailed
				done.Outcome.Phase = hostservice.StartupPhaseMigration
				if deps.Out != nil {
					richtext.Printer{W: deps.Out, Color: deps.Color}.Print(
						"[yellow]Warning: could not migrate state for host-wide daemon '" + deps.Name +
							"': " + err.Error() + "[/yellow]")
				}
				return done
			}
		}
	}

	if BrokerIsAlive(deps) {
		drift, judged := RunningSettingsDrift(deps)
		if !judged || !drift.Stale() {
			done.Outcome.Kind = hostservice.StartupKindReused
			done.Outcome.Phase = hostservice.StartupPhaseReadiness
			done.Outcome.Reused = true
			done.Outcome.Process = hostservice.StartupProcessAlive
			// THE REUSE BOUNDS THE LOG TOO. A daemon whose settings still match is never
			// respawned, so its log is never reopened, and the spawn's own trim would leave
			// a daemon that outlives weeks of launches writing to an unbounded file. Trimmed
			// in place (logcap says why), so the live daemon keeps writing to its log.
			if deps.LogPath != "" {
				_ = logcap.Trim(deps.LogPath)
			}
			return done
		}
		reportSettingsRestart(deps, drift)
		BrokerKill(deps, syscall.SIGTERM, BrokerKillTimeout)
	}

	// A Deps with no argv cannot spawn anything, and saying so beats handing an
	// empty argv to exec.Command — which indexes argv[0] and panics. It is
	// reachable only through a hand-built Deps (both constructors fill the field),
	// so the report is for whoever built one, not for a user.
	if len(deps.Argv) == 0 {
		done.Outcome.Kind = hostservice.StartupKindDaemonStartFailed
		done.Outcome.Phase = hostservice.StartupPhaseSpawn
		if deps.Out != nil {
			richtext.Printer{W: deps.Out, Color: deps.Color}.Print(
				"[yellow]Warning: the host-wide daemon at " + deps.SocketPath +
					" has no spawn argv; nothing was started.[/yellow]")
		}
		return done
	}

	// Clean any stale socket left by a crashed prior broker; a second bind(2)
	// on a stale path fails with EADDRINUSE.
	removeIgnoreMissing(deps.SocketPath)

	// The argv is the CALLER'S, and for the broker it is self-exec'd: the launcher
	// is the running yolo binary, so the spawned
	// `yolo internal daemon claude-oauth-broker` re-execs THIS process rather than
	// resolving "yolo" on PATH (RealDeps and the run pipeline's record-driven
	// SingletonDeps both apply execx.SelfExecArgv before they get here).
	// Read BEFORE the spawn, as close as yolo can get to the read the daemon makes at its
	// own startup; recorded only once there is a PID for the record to describe.
	spawnSettings := readSpawnSettings(deps)
	done.Outcome.Phase = hostservice.StartupPhaseSpawn
	var reasonConn net.Conn
	var reasonAttempt string
	var pid int
	var exited func() bool
	if deps.StartupReason && deps.SpawnWithReason != nil {
		pid, exited, reasonConn, reasonAttempt, err = deps.SpawnWithReason(deps.Argv, deps.LogPath, deps.Name)
	} else {
		pid, exited, err = deps.Spawn(deps.Argv, deps.LogPath)
	}
	if deps.StartupReason && reasonConn == nil && err == nil {
		done.Outcome.ReasonRead = hostservice.StartupReasonReadOutcome{Kind: hostservice.StartupReasonReadChannelFault,
			Phase: hostservice.StartupReasonReadPhaseUnknown, Fault: hostservice.StartupReasonFaultUnavailable}
	}
	if err != nil {
		if reasonConn != nil {
			_ = reasonConn.Close()
		}
		done.Outcome.Kind = hostservice.StartupKindDaemonStartFailed
		done.Outcome.Phase = hostservice.StartupPhaseSpawn
		// Return the socket path anyway: the caller's liveness re-check is
		// what reports a daemon that never started.
		return done
	}
	done.Started = true
	done.Outcome.Spawned = true
	done.Outcome.Process = hostservice.StartupProcessAlive
	done.Outcome.Readiness = hostservice.StartupReadinessNotReady
	_ = os.WriteFile(deps.PIDFilePath, []byte(strconv.Itoa(pid)+"\n"), 0o644)
	// Stamp the singleton as one THIS build started, so a later launch can tell a
	// compatible daemon from one predating the fronted conversion (SingletonSpeaksPreamble),
	// and one an older yolo left running from one whose program lacks the launch check its
	// manifest declares (SpawnedKnowingLaunchCheck).
	StampPreamble(deps)
	StampLaunchCheck(deps, pid)
	writeSettingsRecord(deps, spawnSettings)
	readyDeadline := deps.Now().Add(BrokerSpawnTimeout)
	var reasonResults chan startupReasonResult
	var reasonReadDone chan struct{}
	var reasonReadCancel context.CancelFunc
	if reasonConn != nil {
		readCtx, cancel := context.WithCancel(context.Background())
		reasonReadCancel = cancel
		reasonResults = make(chan startupReasonResult, 1)
		reasonReadDone = make(chan struct{})
		// The reader owns no process or log state. It publishes one value, while this function
		// owns cancellation, connection close, and joining it on every readiness disposition.
		go func() {
			defer close(reasonReadDone)
			read := hostservice.ReadStartupReasonOutcome(readCtx, reasonConn, deps.Name, reasonAttempt, readyDeadline)
			result := startupReasonResult{read: read}
			if read.Kind == hostservice.StartupReasonReadRecord {
				result.reason = &hostservice.StartupReason{Version: 1, Service: deps.Name, Attempt: reasonAttempt,
					Class: read.ReasonClass, Reason: read.Reason, Remedy: read.Remedy}
			} else {
				result.err = startupReasonReadError(read)
			}
			reasonResults <- result
		}()
	} else if deps.StartupReason {
		done.Outcome.ReasonRead = hostservice.StartupReasonReadOutcome{Kind: hostservice.StartupReasonReadChannelFault,
			Phase: hostservice.StartupReasonReadPhaseUnknown, Fault: hostservice.StartupReasonFaultUnavailable}
	}
	var ready bool
	if deps.waitForSocketUntil != nil {
		ready = deps.waitForSocketUntil(deps.SocketPath, readyDeadline, exited, reasonResults)
	} else {
		ready = brokerWaitForSocketUntil(deps, deps.SocketPath, readyDeadline, exited)
	}
	var result startupReasonResult
	resultReceived := false
	if !ready && reasonResults != nil {
		// Readiness owns the deadline. An early terminal process may leave time to finish
		// this attempt's reader, but the reason can never add a new budget.
		remaining := readyDeadline.Sub(deps.Now())
		if remaining > 0 {
			result, resultReceived = waitForStartupReason(deps, reasonResults, remaining)
		} else {
			select {
			case result = <-reasonResults:
				resultReceived = true
			default:
			}
		}
	}
	if reasonReadCancel != nil {
		reasonReadCancel()
		_ = reasonConn.Close()
		<-reasonReadDone
		if !resultReceived {
			select {
			case result = <-reasonResults:
				resultReceived = true
			default:
			}
		}
	}
	if resultReceived {
		done.Outcome.ReasonRead = result.read
		if result.read.Kind == hostservice.StartupReasonReadRecord {
			done.Outcome.ReasonClass = result.read.ReasonClass
			done.Outcome.Reason = result.read.Reason
			done.Outcome.Remedy = result.read.Remedy
			if !ready {
				// Preserve the legacy refusal carrier only on the existing failed-readiness path.
				done.StartupReason = result.reason
			}
		}
	}
	if exited != nil && exited() {
		done.Outcome.Process = hostservice.StartupProcessExited
	} else {
		done.Outcome.Process = hostservice.StartupProcessAlive
	}
	done.Outcome.Phase = hostservice.StartupPhaseReadiness
	if ready {
		done.Outcome.Kind = hostservice.StartupKindSocketObserved
		done.Outcome.Readiness = hostservice.StartupReadinessObserved
	} else {
		done.Outcome.Readiness = hostservice.StartupReadinessNotReady
		switch {
		case resultReceived && result.read.Kind == hostservice.StartupReasonReadRecord:
			done.Outcome.Kind = hostservice.StartupKindCooperativeRefusal
		case done.Outcome.Process == hostservice.StartupProcessExited:
			done.Outcome.Kind = hostservice.StartupKindProcessExited
		default:
			done.Outcome.Kind = hostservice.StartupKindReadinessTimedOut
		}
	}
	if !ready {
		if done.StartupReason == nil {
			reportFailedSpawn(deps, exited)
		} else if done.StartupReason.Class != "configuration" && deps.Out != nil {
			reportCooperativeSpawnRefusal(deps, done.StartupReason)
		}
	}
	return done
}

type startupReasonResult struct {
	reason *hostservice.StartupReason
	err    error
	read   hostservice.StartupReasonReadOutcome
}

func startupReasonReadError(read hostservice.StartupReasonReadOutcome) error {
	if read.Kind == hostservice.StartupReasonReadNoRecord {
		return errors.New("startup reason record absent")
	}
	if read.Kind == hostservice.StartupReasonReadCancelled {
		return errors.New("startup reason read cancelled by owner")
	}
	if read.Kind == hostservice.StartupReasonReadChannelFault {
		return errors.New("startup reason channel fault")
	}
	return errors.New("startup reason read returned no record")
}

func waitForStartupReason(deps Deps, results chan startupReasonResult, remaining time.Duration) (startupReasonResult, bool) {
	if deps.waitForStartupReason != nil {
		return deps.waitForStartupReason(results, remaining)
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case result := <-results:
		return result, true
	case <-timer.C:
		return startupReasonResult{}, false
	}
}

// staleUnreplaceable is the drift an ensure that could not take the spawn lock leaves
// standing: a live daemon whose record names CHANGED keys. Unrecorded is not reported here —
// it is "cannot tell", not "known stale", and the lock failure is already on the screen.
func staleUnreplaceable(deps Deps) *SettingsDrift {
	if !BrokerIsAlive(deps) {
		return nil
	}
	drift, judged := RunningSettingsDrift(deps)
	if deps.DesiredSettings != nil && deps.SettingsPath != "" {
		values, err := parseFlatSettings(deps.DesiredSettings)
		if err != nil {
			return &SettingsDrift{Unrecorded: true}
		}
		drift, judged = compareRecorded(deps, values), true
	}
	if !judged || len(drift.Changed) == 0 {
		return nil
	}
	return &drift
}

// reportFailedSpawn writes the line brokerWaitForSocketUntil's return value exists
// FOR. The detector has always been able to separate a dead singleton from a
// slow one in milliseconds — its own doc comment below says exactly that — and
// the caller here threw the answer away. That is how a broker which died at
// startup 2,549 times in a single jail stayed invisible for months: the only
// record was a log nobody reads, and the consequence surfaced three layers later
// as a refused launch (docs/reference/claude-oauth-interposition.md#a-failed-spawn-reports-itself).
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
//
// IT NAMES THE LOOPHOLE FROM THE RECORD, not "the Claude OAuth broker" as it did
// while this engine was the broker's alone. `host_daemon.scope: "host"` made the
// lifecycle general (SingletonDeps), and a warning that hardcodes one loophole's
// name is the half of a generalization that gets left behind — the second
// host-scoped daemon to fail its spawn would send its owner to `yolo broker
// status` for a daemon that is not the broker. Deps.Name is empty only on a
// hand-built Deps, which degrades to the generic phrase rather than a wrong one.
func reportFailedSpawn(deps Deps, exited func() bool) {
	if deps.Out == nil {
		return
	}
	reason := "did not bind its socket within " + BrokerSpawnTimeout.String()
	if exited != nil && exited() {
		reason = "exited at startup without binding its socket"
	}
	richtext.Printer{W: deps.Out, Color: deps.Color}.Print(
		"[yellow]Warning: " + singletonSubject(deps) + " " + reason +
			" — every client of it will fail until it does. Expected " +
			deps.SocketPath + "; see " + deps.LogPath + "[/yellow]")
}

func reportCooperativeSpawnRefusal(deps Deps, reason *hostservice.StartupReason) {
	if deps.Out == nil || reason == nil {
		return
	}
	line := "[yellow]Warning: " + singletonSubject(deps) + " refused startup: " + richtext.Escape(reason.Reason)
	if reason.Remedy != "" {
		// The pack's text is sanitized to one bounded line but may still hold brackets: it is
		// literal here, never markup (host-service-startup-diagnostics.md §3.1).
		line += " Fix: " + richtext.Escape(reason.Remedy)
	}
	line += "; see " + deps.LogPath + "[/yellow]"
	richtext.Printer{W: deps.Out, Color: deps.Color}.Print(line)
}

// singletonSubject names the daemon a warning is about: the record's loophole when the
// Deps carries one, the generic phrase otherwise (see reportFailedSpawn for why it is never
// a hardcoded loophole name).
func singletonSubject(deps Deps) string {
	if deps.Name == "" {
		return "the host-wide daemon"
	}
	return "the host-wide daemon for '" + deps.Name + "'"
}

// reportLockFailure writes lockFailureLine to deps.Out; a nil writer silences it, as it
// silences every other warning here.
func reportLockFailure(deps Deps, step string, err error) {
	if deps.Out == nil {
		return
	}
	richtext.Printer{W: deps.Out, Color: deps.Color}.Print(
		"[yellow]" + lockFailureLine(deps, step, err) + "[/yellow]")
}

// lockFailureLine is the warning for a spawn refused because the singleton's lock could not
// be taken: which file, the OS's reason, whose daemon, and what it means.
//
// ONE CAUSE IS NAMED beyond the OS's words, and only under a permission error: the
// singleton's paths are fixed and carry no user component (paths.HostSingletonLock), so on a
// host where two people share the directory, the second user's launch cannot open the first
// user's 0644 lock file. That is docs/design/host-daemon-ownership.md OQ-HD8, whose answer
// keeps this message fix for as long as the singleton ships. Under any other error the guess
// would send the reader looking for a user who does not exist, so it is not made.
func lockFailureLine(deps Deps, step string, err error) string {
	line := "Warning: could not " + step + " the lock file " + deps.LockPath + " for " +
		singletonSubject(deps) + ": " + err.Error() + " — nothing was started, and every " +
		"in-jail client of it will fail until it is."
	if errors.Is(err, fs.ErrPermission) {
		line += " The lock's path has no user component, so the likeliest cause is another " +
			"user on this host whose yolo created it first " +
			"(docs/design/host-daemon-ownership.md OQ-HD8)."
	}
	return line
}

// brokerWaitForSocketUntil ports _broker_wait_for_socket: poll until the socket
// appears or the absolute deadline elapses; a dead child (exited() true) is a genuine
// failure detected in milliseconds. Returns whether the socket exists at the end.
func brokerWaitForSocketUntil(deps Deps, sock string, deadline time.Time, exited func() bool) bool {
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
// singletonStamp marks a singleton as started by a build whose daemon sits BEHIND A
// FRONT and therefore expects yolo's connection preamble. Bump it only if that wire
// contract changes again.
const singletonStamp = "fronted-preamble-v1"

// singletonStampPath is the stamp file, a sibling of the PID file so the lifecycle
// that already owns that path creates, finds and removes it.
func singletonStampPath(deps Deps) string { return deps.PIDFilePath + ".capability" }

// SingletonSpeaksPreamble reports whether a RUNNING singleton was started by a build
// that expects the connection preamble.
//
// # The failure this exists to make loud
//
// The singleton's socket path deliberately did not change when the broker moved behind
// a front (2026-08-19) — that is what makes the upgrade seamless, and it is also what
// makes this failure possible. A pre-conversion daemon is still listening there,
// speaking the raw protocol.
//
// EVERY LIVENESS SURFACE SAYS HEALTHY, because every one of them is a CONNECT:
// BrokerIsAlive dials and closes, and the in-jail reachability witness does the same
// through the front. Both succeed. The break is one layer up — the front prepends a
// host-asserted preamble frame, and a daemon that predates the front consumes it AS THE
// REQUEST. Reproduced 2026-08-19: a framed ping returns {"error":"creds_unreadable"}
// while the daemon logs the preamble's own keys, so EVERY Claude OAuth refresh on that
// host fails and the only surface that notices is `yolo check`'s per-jail row, which
// makes the full round trip.
//
// # Why a stamp rather than asking the daemon
//
// Speaking the protocol to test it would mean FORGING a jail identity in the preamble —
// the one thing the front exists to assert honestly, and the reason the liveness probe
// became a connect-and-close in the first place. The stamp answers "did a build like
// mine start this?" without a byte of protocol.
//
// # Why this only REPORTS
//
// It deliberately does not kill and respawn. Two yolo versions sharing one host would
// then take turns killing each other's daemon on every launch, trading a loud failure
// for an invisible restart loop. Whether an upgrade should replace the daemon outright
// is a maintainer call; making the broken state SAY so is not.
func SingletonSpeaksPreamble(deps Deps) bool {
	b, err := os.ReadFile(singletonStampPath(deps))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(b)) == singletonStamp
}

func SingletonReachable(socketPath string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("unix", socketPath, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// RealPgrepStrays ports _broker_pgrep_strays: PIDs of the running processes serving
// the singleton at socketPath, regardless of PID-file state, with our own PID
// filtered out. A missing pgrep / timeout / error yields no PIDs (never an error
// the "tool absent = no-op" invariant).
//
// A STRAY IS WHATEVER PASSES THIS SINGLETON'S SOCKET AS `--socket`, and nothing
// wider. The socket is the singleton's identity: it is derived from the loophole
// name (paths.HostSingletonSocket), and every spawn form a host-wide daemon has had
// names it — `<yolo> internal daemon <name> --socket {socket}` from every shipped
// manifest, and the Claude broker's retired standalone binary
// (`yolo-claude-oauth-broker-host --socket {socket}`), which is why the second
// pgrep pattern that binary used to need is gone.
//
// It used to match the CLAUDE broker's two spawn forms anywhere on the machine, for
// every singleton, since SingletonDeps wires this into all of them. So stopping
// `aws-auth` or `openai-auth-broker` with its PID file gone SIGTERMed every Claude
// broker on the host, and reported the wrong daemon stopped; and under
// `go test ./...` one package's cleanup stopped another package's daemon, in a
// different private singleton directory, mid-test (strayscope_test.go).
func RealPgrepStrays(socketPath string) []int {
	// pgrep -f matches an extended regex against the argv joined by spaces.
	cmd := exec.Command("pgrep", "-f", "(^| )--socket "+regexp.QuoteMeta(socketPath)+"( |$)")
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
	// Bounded at open (internal/logcap): past the cap the log moves to one archived generation.
	if lf, err := logcap.Open(logPath, 0o644); err == nil {
		cmd.Stdout, cmd.Stderr = lf, lf
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, nil, err
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	// A test binary stops the daemons it started through this handle (heldchildren); in
	// production nothing is held.
	heldchildren.Hold(cmd.Process, done)
	exited := func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}
	return cmd.Process.Pid, exited, nil
}

func realSpawnWithReason(argv []string, logPath, service string) (int, func() bool, net.Conn, string, error) {
	parent, child, attempt, err := hostservice.NewStartupReasonChannel()
	if err != nil {
		return 0, nil, nil, "", err
	}
	_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
	cmd := exec.Command(argv[0], argv[1:]...)
	if lf, err := logcap.Open(logPath, 0o644); err == nil {
		cmd.Stdout, cmd.Stderr = lf, lf
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.ExtraFiles = append(cmd.ExtraFiles, child)
	cmd.Env = append(os.Environ(),
		hostservice.StartupReasonFDEnv+"=3",
		hostservice.StartupReasonAttemptEnv+"="+attempt,
		hostservice.StartupReasonServiceEnv+"="+service)
	if err := cmd.Start(); err != nil {
		_ = parent.Close()
		_ = child.Close()
		return 0, nil, nil, "", err
	}
	_ = child.Close()
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	// As realSpawn: a test binary stops the daemons it started through this handle.
	heldchildren.Hold(cmd.Process, done)
	exited := func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}
	return cmd.Process.Pid, exited, parent, attempt, nil
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
