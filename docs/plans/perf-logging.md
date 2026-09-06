# Plan: performance logging behind `--timing` / `--verbose`

**Design:** [`../design/perf-logging.md`](../design/perf-logging.md) · **Status:** ready ·
Written against `6580186c`, 2026-09-06.

**Precedence:** the design wins on behavior; the tree wins on fact; this file is advice and is
the first thing to be wrong. Never twist code to match it — correct it in the commit.

## Map

| Path | Change |
| :--- | :--- |
| `internal/perf/perf.go` | New: the span collector — `Log`/`Span`/`Mark`/`Event`/`Sink`, nil-safe, thread-safe, `SlowSpanThreshold`, the report renderer in the entrypoint's register. |
| `internal/perf/filesink.go` | New: the incremental append sink — run-header at open, one line per event, trim-to-last-50-runs at open, warn-once-then-quiet. |
| `internal/perf/perf_test.go`, `internal/perf/filesink_test.go` | New: the callee pins (nil-safety, idempotent End, register bytes, race-tested concurrency, trim, warn-once). |
| `internal/cli/run/runcmd.go` | `Options.Perf *perf.Log` seam; `timingEnabled()` helper (`Timing` ‖ `YOLO_TIMING` ‖ `YOLO_VERBOSE`). |
| `internal/cli/run/run.go` | Collector construction after the refusal guards; `launch.*` spans down `runContainer`; `child.*` marks at the `runWithProxy` calls; `teardownAfterExit` extraction + `shutdown.*` spans; `terminate.*` spans + report inside `onTerminate`; the report replacing the single-Total block. |
| `internal/cli/run/perfevents.go` | New: Window A attribution via `o.Exec`'d `podman events`, `--until`-bounded, silent on every failure. |
| `internal/cli/run/loopholesruntime.go` | `shutdown.stop_front.<name>` sub-spans + the `container_check` mark inside `stopLoopholes`. |
| `internal/cli/run/network.go` | `shutdown.cleanup_port_forwarding` span. |
| `internal/ttyproxy/ttyproxy.go` | `StageHook` + `RunWithProxyHooked` (signature-compatible); stage marks at spawn, child-exit, drain-done, termios-restore — both paths, nil-guarded. |
| `internal/cli/run/proxy_linux.go`, `proxy_other.go` | `runWithProxy` grows the hook parameter; the Linux half builds it from `o.Perf`. |
| `internal/cli/verbose.go` | New: global `--verbose`/`-v` strip at the front door (the `--user-layer` pattern), published via `os.Setenv`. |
| `internal/cli/cli.go` | Apply the verbose strip after the help branch, before `RewriteArgv`. |
| `internal/cli/paths or run` | `TimingEnv`/`VerboseEnv` name constants. |
| `internal/cli/stop.go` | `stop.*` spans + report when the env gate is on. |
| `internal/cli/commands.go` | `process.title_restore` span around the deferred restore. |
| `internal/cli/runcmd.go` usage, `internal/cli/config_ref.txt` | Document the grown `--timing`, `--verbose`, `YOLO_TIMING`, `YOLO_VERBOSE`. |
| `internal/cli/run/timingspans_test.go` | New: call-site pins — `teardownAfterExit` emits the exact span set; `stopLoopholes` sub-spans; env-gate table; Window A parse fixture. |
| `internal/ttyproxy/ttyproxy_test.go` | Stage-hook order pin on the plain path. |
| `integration/timing_test.go` | New: end-to-end `--timing` and `YOLO_TIMING=1` launches assert the report and the file. |
| `docs/plans/README.md`, `docs/plans/roadmap.md` | Rows; one 💬 entry citing [the OQ-T family](../design/perf-logging.md#7-open-questions). |

## Reuse

- `internal/crossaudit` — the warn-once-then-quiet sink discipline and the
  observability-may-never-fail-a-launch principle.
- `internal/entrypoint/boot.go`'s perfLog — the report's column register and the
  trim-to-last-50-runs retention idiom.
- `internal/cli/userlayer.go` — the global-flag-strip-and-publish pattern `--verbose` copies.
- `run.Options` seams (`Getenv`, `Exec`, `Now`, `Stderr`) — every new behavior stays
  unit-testable; `goldenOptions` extends unchanged.
- `integration/harness_test.go`'s `runCommand`/`jailRunArgs` — the integration pins.

## Build order

1. **Docs** (this pair) — `just test-fast` unaffected.
2. **`internal/perf`** + unit tests — `go test -race -count=1 ./internal/perf/`.
3. **Gates**: `--verbose` strip, env constants, `timingEnabled`, usage/config-ref —
   `just test-fast`.
4. **ttyproxy stage hook** + plain-path test — `go test -race -count=1 ./internal/ttyproxy/`.
5. **Launch spans + report + file sink wiring** — `just test-fast`.
6. **Shutdown arms + `stopLoopholes` sub-spans + `yolo stop` + title restore** — `just test-fast`.
7. **Window A attribution** — `just test-fast`.
8. **Integration pins** — `go test -count=1 -timeout 0 -run 'TestTiming' ./integration`.
9. **Nested-jail verification** (host-launcher-side change, fully observable nested — unlike
   reachability changes): `just build-go`, then from a throwaway workspace
   `cd /tmp/yolo-nested && YOLO_REPO_ROOT=/workspace /workspace/dist-go/linux-$(go env GOARCH)/yolo --timing -- bash -c 'exit 0'`;
   read the stderr report and `<ws>/.yolo/host-perf.log`; exercise the signal arm once
   (`kill -TERM` the yolo pid) and confirm `terminate.*` spans reached the file; confirm
   `yolo --version` writes nothing.

The report's pin debt: `run.go`'s old timing block carried a comment saying whoever next touched
it owed a pin — step 5's `timingspans_test.go` and step 8's integration test are that pin.
