---
title: "Podman reboot readiness implementation sketch"
date: 2026-09-29
status: draft
tags: [plan, podman, launch]
summary: "Preliminary codebase map for the proposed shared runtime readiness gate; not an implementation hand-off."
vantage:
  status-chip: true
---

# Podman reboot readiness — implementation sketch

**Status:** SKETCH, 2026-09-29 — incomplete and unstable while the design's
budget question is open. Do not build from this sketch.

**Reads with:** [`podman-reboot-readiness.md`](podman-reboot-readiness.md)
(the design, which wins on behavior). A settled implementation plan must revisit
the current tree before this sketch can become a hand-off.

- Runtime selection calls the one-shot probe through
  [`preflight.go`](../../internal/cli/run/preflight.go#L251-L379); both explicit
  and autodetected Podman must use the same gate. Apple Container and macOS
  Podman Machine retain their current paths.
- [`runcmd.go`](../../internal/cli/run/runcmd.go#L779-L809) kills and then waits
  for a timed-out process. Verify how the remaining budget is passed to each
  attempt, and how overruns are reported without promising an impossible hard
  cutoff.
- Existing host-side locking and progress patterns are in
  [`flock.go`](../../internal/cli/run/flock.go) and
  [`launchprogress.go`](../../internal/cli/run/launchprogress.go).
- Existing runtime selection tests are in
  [`probes_test.go`](../../internal/cli/run/probes_test.go). Add a regression
  through runtime selection, plus a multi-process lock test; the test must fail
  if the production call site bypasses the new gate.
- The host timing collector is initialized before runtime selection in
  [`run.go`](../../internal/cli/run/run.go#L167-L172). The existing
  [`perf-logging.md`](../reference/perf-logging.md) defines its event format.
- Blocked on [OQ-PR1](podman-reboot-readiness.md#OQ-PR1): the total time
  budget determines retry assertions and the host reboot acceptance check.
