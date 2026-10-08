---
title: "Implementation plan: Pi startup build cancellation"
date: 2026-10-07
status: in-review
stage: DECIDED
next: "Parent: combined just check-ci, a fresh-binary nested-jail smoke, the full integration suite, and macOS/Apple Container runtime verification"
tags: [pi, startup, builds, cancellation, implementation]
summary: "Record the bounded process-group repair and its fail-first regression without expanding into Pi's separate in-jail refresh or TTY-proxy design."
---

# Implementation plan: Pi startup build cancellation

**Status:** 2026-10-08. Built on the local branch `lane/pi-cancel` (two commits, not on main). The repair and one independent review's fixes are in: a cancel that dispatched no backend is not retained, a retained workspace is never a failed build, and admission waits out late container removal. Targeted unit, race and mutation checks and the four fork/patched-extension integration tests are green on Linux. The parent's combined gates and non-Linux runtime verification remain.

## Work items

- [x] Trace `run.Run` → `runForkBuildSlot` → `buildPool` → `runForkBuildChild`, plus the TTY proxy, nested build launch, keeper, entrypoint readiness, and Pi pre-exec refresh boundaries.
- [x] Add a fail-first Linux pty test with five inert compiler fixtures and a four-build pool, exercising the production scheduler and child runner.
- [x] Isolate each build child in its own process group; make the act the sole owner of interrupt forwarding; signal and escalate against the group.
- [x] Preserve Ctrl-C as exit status 130 even when the child handles SIGINT and exits zero, so partial output is not reported as a successful build.
- [x] Cover an interrupt-ignoring descendant, ordinary compiler failure, parallel successful retry, queued work, bounded stdout/stderr and jail-stream drainage, and torn capture-store publication.
- [x] Audit the interrupted repair: preserve interrupted fork-build staging and jail state; gate same-ID cleanup and `Store.Stage` reuse on workspace-lock ownership, keeper completion, and the actual resolved backend from the ordinary capture pipeline, failing closed if that evidence was never recorded or changes before the original backend is known absent.
- [x] Re-verify the final interrupted tree: run targeted tests, targeted race tests, and the docs checker; preserve fail-first, mutation-red and restored-green evidence.
- [x] Independent review (2026-10-08): its blocker (a pre-dispatch Ctrl-C fenced the key for good) and two majors (retention recorded as a failed build; no wait for late container removal) are fixed with tests; two minors are fixed; a corrupt-sidecar fail-closed change was declined because the keeper-record fallback is pinned deliberately; macos-user's run-returned record on retry stays open as a minor.
- [x] Lane: the four fork/patched-extension integration tests pass in-jail on the rebased tree.
- [ ] Parent: run `just check-ci`, `just build-go`, a fresh-binary nested-jail smoke test from a throwaway workspace, and the applicable integration suite on the combined tree.
- [ ] Verify actual macOS/XNU and runtime-specific behavior on supported hosts; Linux pty tests are not evidence for those platforms.

## Reversal and boundaries

This changes only the launch-owned fork-build child lifecycle. It does not alter the jail TTY proxy, keeper protocol, Pi's own `pi update --extensions` launcher refresh, startup readiness, build-pool parallelism, cache format, or policy. A descendant that deliberately leaves the child's process group with `setsid`/`setpgid` is outside group signaling and cannot be killed by this repair. The explicit stdout/stderr drain is bounded, and an incomplete drain fails the build, but a deliberately detached process itself is not reaped; current compiler tools are expected to inherit the group. This is recorded as a residual platform/build-command assumption, not hidden by the Linux pty tests.
