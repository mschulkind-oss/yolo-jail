---
title: "Implementation sketch: disk I/O priority, build steps 5 and 6"
date: 2026-09-27
status: draft
tags: [sketch, plan, io, resources, cgroups, implementation]
summary: "Companion sketch for docs/design/io-priority.md after build steps 1 to 4 landed: what steps 5 (macos-user) and 6 (a cgroup half) would touch, and what they reuse from the built code. Both are blocked, so nothing here is a hand-off."
vantage:
  status-chip: true
---

# Implementation sketch: disk I/O priority, build steps 5 and 6

**Status:** SKETCH, 2026-09-27 — incomplete, and unstable while questions are open. Build steps 1
to 4 landed, and their plan went with them; this file's own history has it.

> [!IMPORTANT]
> This is the companion sketch for [`io-priority.md`](io-priority.md), and it is not a hand-off:
> do not build from it while it says SKETCH. **The design wins on behavior.** Both remaining
> steps are blocked, and each entry below names what blocks it.

## Step 5: macos-user

Blocked on a Mac measurement: whether `setiopolicy_np` set by the launcher survives the setuid
`sudo` and `sandbox-exec` in `LaunchArgv` ([IO-D7](io-priority.md#11-decision-ledger)).

| Where | What changes |
| :--- | :--- |
| [`internal/macosuser/`](../../internal/macosuser) | the launcher calls `setiopolicy_np(IOPOL_TYPE_DISK, IOPOL_SCOPE_PROCESS, …)` before `LaunchArgv` runs, from `ioprio.FromResources` |
| [`internal/macosuser/orchestrator.go`](../../internal/macosuser/orchestrator.go) | `unenforcedResourceKeys` stops naming a declared `io` |
| [`internal/cli/check/section_iopriority.go`](../../internal/cli/check/section_iopriority.go) | the macos-user `[WARN]` becomes a row about the policy |
| `internal/ioprio` | a darwin `sys_darwin.go` beside `sys_other.go`, which `!linux` covers today |

- The measurement to run first: set `IOPOL_THROTTLE`, launch through the real `LaunchArgv`, and
  read `getiopolicy_np` from inside the sandbox.

## Step 6: a cgroup half

Blocked on [OQ-IO7](io-priority.md#OQ-IO7), which decides whether one ships and which.

| Where | What changes |
| :--- | :--- |
| [`internal/cli/run/hostloopback.go`](../../internal/cli/run/hostloopback.go) | `podmanInfo.Host` gains `CgroupControllers []string` (`json:"cgroupControllers"`), the gate [IO-D6](io-priority.md#11-decision-ledger) names |
| [`internal/cli/run/backendcaps.go`](../../internal/cli/run/backendcaps.go) | the flag decision beside `appliedIOPriority`, so the argv and the briefing read one answer |
| `internal/ioprio.Parse` | admits `weight` only if option (a) ships; today it is an unknown key, and `TestParseRefusesEverythingElse` pins that |
| [`cmd/yolo-cglimit`](../../cmd/yolo-cglimit) | only if [OQ-IO4](io-priority.md#OQ-IO4) says yes |

- ⚠ The flag is `--blkio-weight` for a weight or `--cgroup-conf io.prio.class=idle` for a class.
  There is no `--io-weight`, and crun fails container creation on an io file the cgroup lacks.
- Reuse: `noteIOPriority` (`internal/cli/run/iopriority.go`) is where a dropped flag's one launch
  line goes, and `sectionIOPriority` is where the delegation row goes, as a `hostFact` `[SKIP]`
  in a jail.
