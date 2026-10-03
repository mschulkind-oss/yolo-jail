---
title: "Podman reboot readiness implementation sketch"
date: 2026-09-29
status: accepted
stage: BUILT
next: "Retire this sketch with the design when it graduates: nothing here is left to build, and the design names the tests"
tags: [plan, podman, launch]
summary: "Codebase map for the patient Podman readiness gate: where it lives, what it replaces, and the tests that pin its call sites."
vantage:
  status-chip: true
---

# Podman reboot readiness — implementation sketch

**Status:** 2026-09-29. This sketch was checked against `51620f7e` and built
as written, with three changes the rulings of the same day made and the design's
ledger records: the gate refuses at once on an answer that cannot clear on its own
([OQ-PR1](podman-reboot-readiness.md#OQ-PR1),
[PR-D13](podman-reboot-readiness.md#PR-D13)); the housekeeping step below is not the
skip it sketched but a lock held one deletion at a time
([OQ-PR2](podman-reboot-readiness.md#OQ-PR2),
[PR-D14](podman-reboot-readiness.md#PR-D14) to
[PR-D16](podman-reboot-readiness.md#PR-D16)); and the machine-wide record is built
([OQ-PR3](podman-reboot-readiness.md#OQ-PR3),
[PR-D17](podman-reboot-readiness.md#PR-D17)). The design's
[testing section](podman-reboot-readiness.md#testing-and-the-real-host-check) names
the tests. Code is cited by symbol. MEASURED by those unit tests, on a fake podman whose
clock the test drives. UNMEASURED at a real reboot, and on a rootless podman at all.

**Reads with:** [`podman-reboot-readiness.md`](podman-reboot-readiness.md) (the
design, which wins on behavior).

## Where the pieces go

- **The gate** is a new function in `internal/runtime`. It takes an
  injected attempt runner, clock and sleep, and returns a tri-state result:
  ready with parsed facts, not ready with attempt history, or interrupted.
  Callers:
  - `Options.resolveRuntime` and `Options.validateExplicitRuntime`
    ([`preflight.go`](../../internal/cli/run/preflight.go)) replace
    `runtimeIsConnectable`/`probeRuntime` for Podman on Linux. The macOS arm and
    Apple Container keep the current probe ([PR-D7](podman-reboot-readiness.md#PR-D7)).
  - `yolo check`'s `resolveRuntime` and `runtimeIsConnectable`
    ([`check/probes.go`](../../internal/cli/check/probes.go)) and its probe table
    in [`check.go`](../../internal/cli/check/check.go) call the same gate. The
    duplicate probe is deleted.
- **The attempt runner** is not `realExec` ([`runcmd.go`](../../internal/cli/run/runcmd.go)).
  `realExec`'s kill-then-`<-done` over `strings.Builder` pipes is exactly the
  overrun the design removes. The new runner uses `Setpgid`, points stdout and
  stderr at unlinked temp files, waits on the process only, and, when the budget
  ends, returns without killing and reports the pid
  ([PR-D3](podman-reboot-readiness.md#PR-D3)). A precedent for process hygiene,
  though with the opposite kill policy, is `DetachGit` and `gitWaitDelay` in
  [`packsrc/store.go`](../../internal/packsrc/store.go). The precedent for one
  deadline across a sequence of runs is `budget` in the same file.
- **The facts hand-off.** The gate's parsed `podman info --format json` goes on
  `Options`. `hostLoopbackFactsFor`
  ([`hostloopback.go`](../../internal/cli/run/hostloopback.go)) reads it instead
  of running `podman info`, and emits the `podman.facts` note from it.
  `AutoLoadOptions.StoreFacts` ([`image/autoload.go`](../../internal/image/autoload.go),
  whose seam already exists, and which now carries the store as well as the
  rootless answer) is set from it. So is `yolo check`'s
  image-delivery section ([`section_imagedelivery.go`](../../internal/cli/check/section_imagedelivery.go)).
  `image.ReadPodmanStoreFacts` ([`storespec.go`](../../internal/image/storespec.go))
  stays the parser for both, decoding the rootless field as a pointer so
  missing and false remain distinct.
- **The attach decision.** `findRunningContainer`'s use in the fresh-or-attach
  branch of `Run` ([`run.go`](../../internal/cli/run/run.go)) becomes tri-state in
  the `probeExistingContainer` shape ([`lifecycle.go`](../../internal/cli/run/lifecycle.go)),
  with a deadline. On "could not ask" it refuses
  ([PR-D8](podman-reboot-readiness.md#PR-D8)).
- **Progress.** `withStderrProgress`
  ([`launchprogress.go`](../../internal/cli/run/launchprogress.go)) gives the step
  no handle on its `*progress.Line`. The gate's caller needs a variant that
  passes the line, so the step can call `Line.Set` and `Line.Println`
  ([`progress.go`](../../internal/progress/progress.go)).
- **Timing.** `initPerf` runs early in `Run`, and the `probes.done` mark comes
  after `resolveRuntime` returns. The `runtime.ready` span and its
  `runtime.ready.attempt` notes are written inside the gate's caller, on both
  outcomes ([PR-D10](podman-reboot-readiness.md#PR-D10)). The event format is in
  [`perf-logging.md`](../reference/perf-logging.md).
- **No lock.** [PR-D1](podman-reboot-readiness.md#PR-D1) removed the first
  draft's host-wide lock, so this sketch no longer needs a deadline-bounded flock
  or an explicit-`LOCK_UN` release (the rule `97ecebce` added to `flock.go`
  because forked children inherit the lock). If a measured storm ever brings a
  lock back, those two are its requirements. The pattern to follow is the
  machine-wide `lockImageCopy` in [`image/copylock.go`](../../internal/image/copylock.go),
  not `flock.go`'s per-container-name `acquireWorkspaceLock`.
- **Housekeeping**, as [OQ-PR2](podman-reboot-readiness.md#OQ-PR2) ruled it — none
  of the sketch's options: `runHousekeeping`
  ([`housekeeping.go`](../../internal/cli/run/housekeeping.go)) runs as one pass under
  `withHousekeepingPass`, which holds a separate pass lock non-blocking for the whole
  pass and hands every class a `prune.Guard` that takes the shared housekeeping lock
  around each deletion. Each reaper has a `…Guarded` variant in `internal/prune` (and
  `flakebundle.ReapGuarded`) whose recheck runs under that lock; the manual `yolo prune`
  keeps the unguarded names.
- **The machine-wide launch line** ([OQ-PR3](podman-reboot-readiness.md#OQ-PR3)):
  [`launchrecord.go`](../../internal/cli/run/launchrecord.go), armed first thing in
  `Run`, above the three guards that refuse before any other work, and written once,
  from the keeper's `spawned` event, the attach, the macos-user dispatch, or `Run`'s
  return, under a lock file beside the log that rotation never renames.

## Tests

All of these run under `go test -short` except the host check.

- **Through runtime selection**, in
  [`probes_test.go`](../../internal/cli/run/probes_test.go), with a fake attempt
  runner and an injected clock and sleep:
  - fails N times and then succeeds, including success on the last attempt the
    budget allows;
  - one attempt that is still running at expiry: refused, not killed, with its
    pid named;
  - an error exit with stderr every time: every failure line is printed, and
    the refusal carries the last stderr line;
  - missing binary: fails at once;
  - macOS, with both Podman and Apple Container: exactly one attempt and no
    wait.

  Each test fails if either the explicit or the autodetected path stops calling
  the gate.
- **`yolo check`**: the same fake, through `check`'s runtime resolution. It fails
  if check keeps a probe of its own.
- **One `info` per launch**: a whole fake launch counts the `podman info` argv
  across `Run` and requires exactly one. It fails if `hostLoopbackFactsFor` or
  the rootless check brings back its own query. The existing
  `TestPodmanFactsAreRecordedFromTheExistingInfoCall` pins only the call inside
  `hostLoopbackFactsFor`.
- **A real subprocess**, using the re-exec helper pattern: a child that forks a
  grandchild holding stdout and stderr open, then exits. The runner must return
  when the child exits. A second case outlives the budget and must be left
  running.
- **The attach decision**: `ps` returning RC=125 refuses the launch.
- **Recovery end to end**: on a pasta host, a launch that recovers inside the
  gate gets `YOLO_HOST_LOOPBACK=requested`. This takes a real rootless host or
  CI; a nested jail cannot see it (AGENTS.md, the two carve-outs).
- **The host check**: the three steps in the design's
  [testing section](podman-reboot-readiness.md#testing-and-the-real-host-check).
  Step 1, the forced refresh, runs **before** building, because it confirms the
  mechanism and may move the [PR-D12](podman-reboot-readiness.md#PR-D12) constant.
