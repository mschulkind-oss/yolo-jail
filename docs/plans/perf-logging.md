# Plan: performance logging behind `--timing` / `--verbose`

**Design:** [`../design/perf-logging.md`](../design/perf-logging.md) · **Status:** BUILT 2026-09-06 ·
Written against `6580186c`, landed through `03b18afb`.

**Precedence:** the design wins on behavior; the tree wins on fact; this file is advice and is
the first thing to be wrong. Never twist code to match it — correct it in the commit.

**Rebuilt against the tree at close.** The Map below is what the commits actually touched, not what
this file predicted: the predicted `internal/cli/run/network.go` edit never happened (the
`shutdown.cleanup_port_forwarding` span wraps the CALL, in `teardownAfterExit`, so the callee is
untouched), the env constants landed in `internal/paths/paths.go` rather than the hedged
"`internal/cli/paths or run`", and five test/harness files the plan never listed had to move.

## Map

| Path | Change |
| :--- | :--- |
| `internal/perf/perf.go` | New: the span collector — `Log`/`Span`/`Mark`/`Event`/`Sink`, nil-safe, thread-safe, `SlowSpanThreshold`, `LastEvent`/`StartTime` for Window A, the report renderer in the entrypoint's register. |
| `internal/perf/filesink.go` | New: the incremental append sink — run-header at open, one line per event, trim-to-last-50-runs at open, warn-once-then-quiet. |
| `internal/perf/perf_test.go`, `internal/perf/filesink_test.go` | New: the callee pins (nil-safety, idempotent `End`, byte-exact register, race-tested concurrency, trim, warn-once). |
| `internal/cli/run/runcmd.go` | `Options.Perf` + `perfReportOnce` seams; `timingEnabled()`; `initPerf`/`newTimingLog`/`TimingLogFor`; `HostPerfLogName`. |
| `internal/cli/run/run.go` | Collector construction after the live-overlay refusal; `probes.done` mark; `launch.*` spans (incl. `launch.auto_capture`); `teardownAfterExit` extraction + `shutdown.*`; `terminate.*` + the report inside `onTerminate`; `emitTimingReport` replacing the single-Total block. |
| `internal/cli/run/perfevents.go` | New: Window A attribution via `o.Exec`'d `podman events`, `--until`-bounded, 3s timeout, silent on every failure. |
| `internal/cli/run/loopholesruntime.go` | `shutdown.stop_front.<name>` sub-spans + the `shutdown.container_check` mark in front of the unbounded `podman ps`. |
| `internal/cli/run/assemble.go` | The `YOLO_PROFILE=1` crossing reads `timingEnabled()` so the env gates drive the jail half too. |
| `internal/ttyproxy/ttyproxy.go` | `StageHook` + `RunWithProxyHooked` (the old signature delegates with nil); stage calls at spawn, child-exit, drain-done, termios-restore, on both paths, panic-contained. |
| `internal/cli/run/proxy_linux.go`, `proxy_other.go` | `runWithProxy` grows an `*Options`; the Linux half builds the hook, the fallback marks spawn/exit directly. |
| `internal/cli/verbose.go` | New: global `--verbose`/`-v` strip at the front door (the `--user-layer` pattern), published via `os.Setenv`. |
| `internal/cli/cli.go` | Apply the verbose strip after the help branch, before `RewriteArgv`. |
| `internal/paths/paths.go` | `TimingEnv` / `VerboseEnv` name constants, with the host-only ruling (D5) on each. |
| `internal/cli/stop.go` | `stop.*` spans + report behind the env gate; `stopJail` grows a `*perf.Log` parameter. |
| `internal/cli/commands.go` | `process.title_restore` span around the deferred restore. |
| `internal/cli/runcmd.go`, `internal/cli/config_ref.txt` | Document the grown `--timing`, `YOLO_TIMING`, `YOLO_VERBOSE`. |
| `internal/cli/run/timingspans_test.go` | New: the call-site pins — `teardownAfterExit`'s exact span set, the off-path writing nothing, the gate table, `initPerf`, the Window A parse + failure modes. |
| `internal/cli/run/timingenv_test.go` | Doc comment only: the gate is `timingEnabled()` now, and names where the report's pin lives. |
| `internal/cli/verbose_test.go`, `internal/cli/stop_test.go` | New/extended: the strip-and-publish table (incl. `--` boundary), and stop's span pins. |
| `internal/ttyproxy/ttyproxy_test.go` | Stage-hook order on the plain path + a panicking hook that must not take the proxy down. |
| `integration/timing_test.go` | New: end-to-end `--timing`, `YOLO_TIMING=1`, and the off-by-default contract. |
| `integration/harness_test.go` | `withEnv` runOption (additive) so an env-gated feature is exercisable without leaking into every test's environment. |
| `docs/plans/README.md`, `docs/plans/roadmap.md` | Rows citing [the OQ-T family](../design/perf-logging.md#81-deferred-work-each-with-the-trigger-that-fires-it) — no longer a 💬 entry: the list was triaged empty on 2026-09-08. |

## Reuse

- `internal/crossaudit` — the warn-once-then-quiet sink discipline and the
  observability-may-never-fail-a-launch principle.
- `internal/entrypoint/boot.go`'s perfLog — the report's column register and the
  trim-to-last-50-runs retention idiom.
- `internal/cli/userlayer.go` — the global-flag-strip-and-publish pattern `--verbose` copies.
- `run.Options` seams (`Getenv`, `Exec`, `Stderr`) — every new behavior stays unit-testable;
  `goldenOptions` extends unchanged. **Never `Options.Now`** — see design D8.
- `integration/harness_test.go`'s `runCommand`/`jailRunArgs` — the integration pins.

## What the build order actually cost

Each step below shipped as one commit, in this order, with the verifying command that gated it.

1. **Docs** (this pair + rows) — `f9eee104`.
2. **`internal/perf`** + unit tests — `03650811`; `go test -race -count=1 ./internal/perf/`.
3. **Gates** (`--verbose` strip, env constants, `timingEnabled`, usage/config-ref) — `6fda5488`;
   `just test-fast`.
4. **ttyproxy hook + every span + both shutdown arms + `yolo stop` + title restore** — `4bd0c7c4`
   (steps 4–6 of the original plan collapsed: the hook has no meaning without a consumer, and
   splitting them would have committed a signature change no caller used); `just test-fast`.
5. **Window A attribution** — `f54dab0a`; `go test -run 'TestParseDie|TestAttributeWindowA'`.
6. **Integration pins** — `f25e7e5c`;
   `YOLO_TEST_REBUILD_IMAGE=1 go test -count=1 -timeout 0 -run TestTiming ./integration`
   (the rebuild flag is not optional here — `internal/` changes move the image's `goSrc`, so the
   suite's skew gate aborts against the previously-loaded image).
7. **Nested-jail verification + its four fixes** — `03b18afb`. See below.

## What the nested jail found that the unit suite could not

Run from a throwaway workspace, by path, against the live tree:

```console
$ mkdir -p /tmp/yolo-nested && cd /tmp/yolo-nested
$ YOLO_REPO_ROOT=/workspace /workspace/dist-go/linux-$(go env GOARCH)/yolo --timing -- bash -c 'exit 0'
```

- **A 109-second hole between two spans.** The very first report spent 109 of its 125 seconds
  between `probes.done` and the first launch span — `autoCaptureInstallerPrograms`, unspanned.
  Now `launch.auto_capture`. A span table's blind spots are only visible on a real launch.
- **Two reports and two 3-second Window A queries** on the signal path: the terminate arm's
  `stopJail` is what makes the child exit, which unblocks the normal-exit arm *while onTerminate is
  still running*. That interleaving is ordinary, not a fault — the report is once-per-`Run` now
  (`*sync.Once`; embedded it made `Options` uncopiable, which `go vet`'s copylocks caught).
- A doubled `===` in the file header, and a nil `Sink` panicking rather than being skipped.

Signal-arm verification needs a real pty. `script` is not in this image; a `pty.fork()` in Python
is (`os.execve` the binary, sleep, `os.kill(pid, SIGTERM)`), and it showed `terminate.stop_jail`
0.202s, the `terminate.*` spans reaching the file **after** the arm's `os.Exit`, and one report at
rc 143 — which is the whole argument for writing events as they happen rather than at exit.

Also confirmed: `yolo --version` under `YOLO_TIMING=1` writes no file and creates no directory, and
a launch the live-overlay guard refuses writes nothing at all.

The report's pin debt is paid: `run.go`'s old timing block carried a comment saying whoever next
touched it owed a pin — `timingspans_test.go` and `integration/timing_test.go` are that pin.
