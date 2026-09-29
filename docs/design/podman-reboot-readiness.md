---
title: "Let Podman finish waking before a jail gives up"
date: 2026-09-29
status: in-review
tags: [design, launch, podman, reliability]
summary: "A bounded, shared readiness gate for concurrent Linux jail launches after reboot."
vantage:
  status-chip: true
---

# Let Podman finish waking before a jail gives up

**Status:** DESIGN, 2026-09-29. Nothing built; evidence checked against `0d1865b8`.

> **In short.** A transient failure of `podman info` while the host is restoring
> containers should delay a jail launch, not cancel it. Concurrent yolo launches
> should share a short readiness gate and still fail within a finite time when
> Podman genuinely cannot answer.

**Why it matters.** At reboot, an otherwise usable jail failed before its container
started; the next launch worked once Podman finished its cleanup.

**The shape.** One host-wide readiness gate covers the initial Linux Podman probe,
with retries and one deadline shared by lock waiting and probing.

**Cost.** A broken Podman may take longer to report failure, and an unrelated
workspace may wait behind the process performing the first check.

**Start at [the proposed gate](#the-proposed-gate)** — its deadline and ownership
are the decisions that determine whether a reboot recovers.

**Needs your ruling:** [OQ-PR1](#OQ-PR1).

**Reads with:** [`podman-reboot-readiness-plan.md`](podman-reboot-readiness-plan.md)
(the implementation sketch, incomplete while the ruling is open),
[`perf-logging.md`](../reference/perf-logging.md) (the existing host timing log).

---

## Verdict and boundary

Build a **readiness gate** *(coined here)*: a short, host-wide admission check
that lets one yolo process test whether Podman can answer while other yolo
launches wait. It is **not** a lock around container creation or a Podman
service, and it does not protect Podman against non-yolo clients.

This is for **Linux launches selecting Podman**, whether selected explicitly or
by autodetection. It does not change Apple Container, the native macOS backend,
or macOS Podman Machine semantics: an intentionally stopped VM should retain its
existing start hint rather than incur a Linux reboot grace period. A missing
Podman binary still fails immediately. Existing containers and state are neither
migrated nor changed by the gate.

The design must not remove containers, reset Podman's database, start a host
service, accept a stale image, or turn a failed probe into permission to launch.
A successful `podman info` is still required.

## What happened, and what remains uncertain

At the September 29 reboot, yolo reported a failed `podman info` after a
22.5-second progress line. Its stderr contained Podman errors cleaning up old
volumes and refreshing a removed container. Host journal events show Podman
removing containers and volumes around 10:49:14 local time. This workspace's
[host timing log](../reference/perf-logging.md) records a later successful
Podman facts query at 10:50:00, taking 0.529 seconds. These observations support
transient startup contention; they do **not** establish whether Podman's internal
lock, I/O pressure, or another process was the cause.

Today each launch checks the runtime independently and treats the first failed
probe as fatal ([runtime selection](../../internal/cli/run/preflight.go#L251-L301),
[one 10-second probe](../../internal/cli/run/preflight.go#L359-L379)). The
subprocess timeout is not a total wall-clock bound: after killing the process,
the caller waits for it to exit ([execution](../../internal/cli/run/runcmd.go#L779-L809)).
The 22.5-second report is thus a real elapsed observation, not evidence that the
configured probe deadline is 22.5 seconds. The existing progress line narrates
the probe ([progress](../../internal/cli/run/launchprogress.go#L38-L54)).

A separate host journal warning reported very low free space to Syncthing.
Space pressure deserves host maintenance, but this record does not establish
that it caused the Podman timeout. Do not encode a disk cleanup policy into
runtime readiness.

## The proposed gate

The gate begins when a Linux launch first tries to check Podman, before it
starts image delivery or creates a container. One process owns an exclusive
host-wide advisory lock for its readiness attempts; other yolo processes wait
on that same lock. Ownership ends immediately after success or failure. Each
waiter then verifies Podman itself: the lock holder's success is not a cached
verdict about what a subsequent process can reach.

**Advisory lock** means cooperation only among yolo launchers using the same
host-user state directory; `podman` and other clients do not take it. The lock
file stays at a stable path across launches, is not deleted while processes may
be waiting on it, and a crashed owner releases its operating-system lock. The
lock has no workspace-specific location: different projects must meet at the
same gate. A lock creation or acquisition error is disclosed, then the process
runs its own bounded readiness attempts without serialization; it never treats
an inability to coordinate as evidence Podman is available.

The default proposal is a **60-second total wall-clock budget**, measured from
entry to the gate, including lock waiting, each attempt, and delays between
attempts. A single `podman info` attempt is capped at the existing 10-second
subprocess deadline or the remaining budget, whichever is shorter. After a
failed attempt, wait up to two seconds before trying again, never beyond the
budget. The launch succeeds only when an attempt exits successfully before the
budget expires. An error exit and a timeout are both retryable within the
budget; Podman stderr is diagnostic, not an error-classification protocol. A
binary that cannot start and a missing binary fail immediately.

If the budget expires waiting for the lock, the launch reports that it could
not verify Podman because another yolo check occupied the gate; it does not
start its own extra 60 seconds. If attempts exhausted the budget, the launch
reports the number of attempts, elapsed time, and last probe error or stderr,
then exits nonzero with the existing `podman info` hint. A successful later
attempt does not print earlier stderr as a fatal error. No retries occur during
image delivery or container creation: those operations have different failure
semantics.

A deadline is a **best-effort subprocess limit**, not a promise that a stuck
kernel process can be reaped instantly. The process must report actual elapsed
time if cleanup overruns it, and it must not start a further attempt after the
budget is exhausted.

## Reporting and measurement

An ordinary warm launch stays quiet at the existing progress grace period. A
wait or retry says on the launch stream why it is waiting; the line remains
visible rather than producing one error banner per failed attempt. The
[launch log](../reference/report-tiers.md) preserves a concise record of wait
time, attempts, elapsed time, and final result, including for a failed launch.
When timing collection is enabled, the readiness gate also records its wait and
attempt durations; a failed probe occurs before the current `probes.done` mark
([call site](../../internal/cli/run/run.go#L167-L172)), so that mark alone cannot
explain the failure. Neither log records secrets or arbitrary Podman environment.

A real-host reboot check should start multiple independent workspaces at once,
include a launch whose first Podman query times out during startup, and verify
that each jail eventually starts or receives a bounded, actionable refusal.
Tests also need to demonstrate the call path from runtime selection through the
gate; a test of an isolated retry helper would not prove that launches use it.
A healthy warm launch must not pay a fixed sleep. The result should report
actual readiness wait and probe durations, not just whether a test passed.

## Alternatives and risks

| Option | Disposition |
| :--- | :--- |
| Increase the single probe timeout | Rejected: a first failure still cancels the launch, and simultaneous probes remain simultaneous. |
| Retry independently in every workspace | Rejected as the only measure: it can recover, but each launch repeats Podman's cold-start work under contention. The lock complements the retries. |
| Lock the entire jail launch | Rejected: one image build or interactive jail would block unrelated projects. |
| Ignore failed `podman info` and try to launch anyway | Rejected: it conceals a broken runtime and moves the failure into image or container operations. |
| Make Waykeeper serialize every restored workspace | Not the primary fix: other callers can also start concurrent jails, and runtime readiness belongs to yolo. Waykeeper pacing remains an optional operational mitigation. |

| Risk | Containment |
| :--- | :--- |
| A stuck leader delays all yolo waiters | The total budget covers both lock waiting and attempts; a dead process drops its lock. |
| A permanently broken Podman now takes longer to fail | One finite, disclosed deadline, with the last error retained. |
| Other Podman clients still contend | State explicitly that coordination is advisory; check the real host after shipping rather than claiming the lock removes all contention. |
| Timeouts can overrun while the operating system reaps a process | Report actual time and never promise a hard wall-clock bound that the process layer cannot enforce. |

## Open Questions

1. 💬 **OQ-PR1: How long may a restored jail wait for Podman?** The default
   budget controls both recovery probability at a busy reboot and how late a
   genuinely broken runtime reports failure. It applies to the whole gate, not
   separately to every waiter and every attempt.

   - **A — 60 seconds total.** Covers several cold attempts, at the cost of a
     minute before a persistent fault is reported.
   - **B — 30 seconds total.** Faster failure, but the observed first attempt
     alone consumed 22.5 seconds, leaving little recovery room.
   - **C — Another finite budget.** Choose it from measured reboot timings;
     the gate and reporting rules otherwise stay the same.

   <!-- vantage: oq id=OQ-PR1 leaning="60 seconds total, shared by lock waiting and attempts; the observed first failure already consumed 22.5 seconds." -->

   _Leaning:_ 60 seconds total, shared by lock waiting and attempts; the
   observed first failure already consumed 22.5 seconds.

   **Answer:**
   > _(empty — fill in when decided)_
