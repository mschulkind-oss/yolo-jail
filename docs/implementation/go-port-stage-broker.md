# Go-port Stage broker — `yolo broker {status,stop,restart,logs}` (handoff)

**Status:** landed (library + command bodies). Ports the broker command group
from `src/cli/broker_cmd.py` plus the SINGLETON lifecycle helpers from
`src/cli/loopholes_runtime.py` (the `_broker_*` family). Front-door dispatch is
the orchestrator's job — this stage delivers clean importable funcs
(`brokercmd.{Status,Stop,Restart,Logs}` + `RealDeps`) for a one-line
`runBroker` dispatcher; **no `cmd/` or `internal/frontdoor` edits here**.
**Built on:** `internal/frameproto` (frozen wire protocol), `internal/jsonx`
(pong decode), `internal/execx` (kill(pid,0) tri-state), `internal/paths`
(GlobalStorage).

## What landed

Commits (main):
- `7e4a692` feat(go): internal/brokerlifecycle — broker singleton lifecycle (seams + fakes)
- `9308363` feat(go): internal/brokercmd — yolo broker {status,stop,restart,logs}
- (this doc + the D13 ledger entry)

### `internal/brokerlifecycle` — the lifecycle engine

The broker is a **host-wide singleton** (one daemon per host, serving every
running jail). This package ports the `_broker_*` helpers, every side effect
behind an injectable `Deps` seam (`RealDeps()` wires production):

- **Path constants** (byte-identical to `loopholes_runtime`):
  `BrokerSingletonSocket = /tmp/yolo-claude-oauth-broker.sock`,
  `BrokerSingletonPIDFile = /tmp/yolo-claude-oauth-broker.pid`,
  `BrokerSingletonLock = /tmp/yolo-claude-oauth-broker.lock`,
  `BrokerLoopholeName = "claude-oauth-broker"`,
  `BrokerConsoleName = "yolo-claude-oauth-broker-host"`.
  `BrokerLogPath()` = `GLOBAL_STORAGE/logs/host-service-claude-oauth-broker.log`.
- **`BrokerReadPID`** (`_broker_read_pid`): int from the PID file, or absent.
- **`BrokerStatus`** (`_broker_status`): pid/pid_live/socket_exists/ping_ok +
  the display paths; **ping is gated on socket presence** (matches the Python
  `sock_exists and _broker_ping(...)`).
- **`BrokerIsAlive`** (`_broker_is_alive`): all four gates (pid present + pid
  live + socket present + ping) must hold.
- **`BrokerKill`** (`_broker_kill`): PID-file-first, else pgrep-discovered
  strays; **SIGTERM → wait-to-exit → SIGKILL stragglers** sequence preserved;
  cleans PID file + socket; returns true iff something was running (still clears
  a stale socket when nothing was).
- **`BrokerSpawn`** (`_broker_spawn`): **flock the lock file**, re-check liveness
  inside the lock (race loser returns without spawning), clear a stale socket,
  resolve the launcher, spawn detached (own session + close_fds), write the PID
  file, wait for the socket to bind. **Argv byte-exact:** `BrokerSpawnArgv` =
  `[*launcher, "--socket", <socket>]`.
- **`DaemonLauncher`** (`_daemon_launcher`): the `$YOLO_GO_BIN_DIR/<name>` Go
  binary when gated via `YOLO_GO_DAEMONS` and executable, else the console-script
  name (returned unconditionally — no PATH-existence check at the tail, matching
  Python); prints the "using the Python daemon" warning for the missing-gated
  sub-case.
- **`BrokerPing`** (`_broker_ping`): dial the socket, send the **byte-exact**
  `{"action":"ping"}` request, expect a `pong:true` stdout frame (stream 0)
  before the exit frame (stream 2) — reuses `internal/frameproto` +
  `internal/jsonx` rather than re-implementing the frame protocol.
- **`RealPgrepStrays`** (`_broker_pgrep_strays`): `pgrep -f
  yolo-claude-oauth-broker-host`, self-PID filtered, tool-absent = no-op.

### `internal/brokercmd` — the command bodies

- **`Status`** (`broker_status_cmd`): the health snapshot lines, then **exit 0**
  when `pid_live AND ping_ok` else the cycle hint + **exit 1**.
- **`Stop`** (`broker_stop_cmd`): kill the singleton; "Stopped broker." /
  "No broker was running."; always exit 0.
- **`Restart`** (`broker_restart_cmd`): kill → spawn → verify alive; **exit 0**
  with `socket=<path>` when live, else the `Check <log>` hint + **exit 1**.
- **`Logs`** (`broker_logs_cmd`): no-log-file → dim line + exit 0; else the
  **byte-exact** `["tail", "-n<lines>", ("-f")?, <path>]` argv (also exposed as
  the pure `BuildTailArgv`), running attached to the terminal; a SIGINT death of
  `tail -f` (Ctrl-C) is swallowed like Python's `KeyboardInterrupt`.

Output is **rich-console → INFO-parity** (approved OQ): a closed markup→ANSI map
(`markup.go`) renders the same information with purposeful color, or strips tags
in plain mode. **Exit codes and the socket/pid/log PATH strings are byte-exact**
vs Python.

## Verification

- `go test ./internal/brokercmd/ ./internal/brokerlifecycle/` — **PASS**.
  - Lifecycle unit tests drive every branch against a **fake socket/pid**: status
    shape (healthy/empty/ping-gated), the four is-alive gates, kill
    (nothing-running / graceful / SIGKILL-escalation / pgrep-fallback), spawn
    (happy / already-alive-skip / stale-socket-clear / dead-child-fast) and the
    argv builder, launcher gating (default / gated-go-binary / missing-fallback),
    and the log-path string.
  - A **real Unix-socket** round-trip (`ping_test.go`) serves a fake broker via
    `frameproto` and exercises `BrokerPing` end-to-end (pong true/false /
    exit-before-pong / no-socket).
  - Command tests cover status exit 0/1, stop, restart success/failure (with the
    log-path hint), logs argv + no-file, and the color-mode ANSI rendering.
- **`TestParityVsLivePython`** (shells to `uv run python` / `python3`, **SKIP**
  when absent): imports LIVE `src.cli.loopholes_runtime`, monkeypatches the
  socket/pid/ping probes, and compares — byte-exact — the PATH constants, the
  spawn argv (via `_daemon_launcher`), the tail argv (`f"-n{lines}"` form), and
  the `_broker_status` dict shape/values against Go's real `BrokerStatus` for the
  same scenarios. **PASS** against the in-jail Python.
- `gofmt` (scoped to the new files), `go vet`, `staticcheck` — all clean.
- `GOOS=darwin GOARCH=arm64 go build ./internal/brokercmd/ ./internal/brokerlifecycle/`
  — **PASS**. (A `./...` darwin build fails only in `internal/prune`, an
  unrelated concurrent work-stream's untracked `probes.go`; my packages are
  clean on both linux and darwin.)

**Not exercised (host daemon, heavy):** a full live `yolo broker restart` that
actually forks the host broker. The lifecycle is unit-tested via the seams + a
fake socket/pid, and the argv/paths are byte-tested against live Python, per the
stage's nested-jail rider — a real restart cycles the one host-wide daemon and
is out of scope for an automated test.

## Divergences

- **D13** (`docs/design/go-port-divergences.md`, proposed): an **unlaunchable**
  broker binary makes Python's `_broker_spawn` crash with an uncaught
  `Popen` exception; the Go `BrokerSpawn` degrades to the graded **exit 1** +
  log-path hint (the same outcome the restart command already documents for the
  launch-but-no-bind case). Strictly more graceful; unreachable in a real
  install.

## Wiring (NOT done here — for the orchestrator)

Add a one-line dispatcher to `cmd/yolo/native.go`, e.g.:

```go
func runBroker(args []string) int {
    deps := brokercmd.RealDeps()
    var sub string
    var rest []string
    if len(args) > 1 { sub, rest = args[1], args[2:] }
    switch sub {
    case "status":  return brokercmd.Status(deps)
    case "stop":    return brokercmd.Stop(deps)
    case "restart": return brokercmd.Restart(deps)
    case "logs":    // parse -n/--lines (default 50) and -f/--follow from rest
        return brokercmd.Logs(deps, lines, follow)
    default:        return delegateToPython(args)
    }
}
```

`Logs` takes `(deps, lines int, follow bool)`; the flag parsing (`-n/--lines`
default 50, `-f/--follow`) belongs in that dispatcher alongside the other
native command flag parsers.
