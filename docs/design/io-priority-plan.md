---
title: "Implementation sketch: disk I/O priority, build steps 5 and 6"
date: 2026-09-27
status: draft
stage: SKETCH
next: "Step 5 is built (IO-D13); step 6 waits on OQ-IO7"
depends-on:
  - io-priority.md#OQ-IO7
tags: [sketch, plan, io, resources, cgroups, implementation]
summary: "Companion sketch for docs/design/io-priority.md: the record of build step 5 (macos-user), built 2026-10-04, and what step 6 (a cgroup half) would touch and reuse. Step 6 is blocked, so nothing here is a hand-off."
vantage:
  status-chip: true
---

# Implementation sketch: disk I/O priority, build steps 5 and 6

**Status:** 2026-10-04 — incomplete, and unstable while questions are open. Build steps 1
to 5 landed; the plan for 1 to 4 went with them, and this file's own history has it. Step 5's
measurement, `TestMacosUserIOPolicyAcrossTheLaunchArgv`
([`macosuseriopolicy_test.go`](../../integration/macosuseriopolicy_test.go)), ran in the scheduled
`macos-user.yml` job on 2026-10-03 (GitHub Actions run 37121866798) and logged `IOPOL VERDICT:
SURVIVES`, and step 5 was built on it. UNMEASURED: step 5's own set on hardware,
`TestMacosUserIOPriorityIsApplied`, until that job runs it.

> [!IMPORTANT]
> This is the companion sketch for [`io-priority.md`](io-priority.md), and it is not a hand-off:
> do not build from it while it says SKETCH. **The design wins on behavior.** The one remaining
> step is blocked, and its entry names what blocks it.

## Step 5: macos-user

**Done 2026-10-04** ([IO-D13](io-priority.md#11-decision-ledger)). It waited only on the Mac
measurement, which came back `IOPOL VERDICT: SURVIVES`
([IO-D7](io-priority.md#11-decision-ledger)).

| Where | What changed |
| :--- | :--- |
| [`internal/ioprio`](../../internal/ioprio) | `Priority.DarwinPolicy` maps the value (pure, tested everywhere); `diskpolicy_darwin.go` and its assembly stub call `setiopolicy_np` and `getiopolicy_np` through a libSystem trampoline; `diskpolicy_other.go` refuses off darwin |
| [`internal/macosuser/orchestrator.go`](../../internal/macosuser/orchestrator.go) | the launcher sets the policy on itself after the plan's invariants pass and before the bootstrap, through the `SetDiskIOPolicy` and `DiskIOPolicy` seams, and warns once if it does not hold; `unenforcedResourceKeys` stops naming `io`; the dry-run plan names the policy |
| [`internal/cli/run/backendcaps.go`](../../internal/cli/run/backendcaps.go) | `appliedIOPriority` answers the declaration on macos-user, so the briefing states it |
| [`internal/jailcontent/briefing.go`](../../internal/jailcontent/briefing.go) | a macos-user line naming `IOPOL_UTILITY` or `IOPOL_THROTTLE`, advisory, with no Linux class or scheduler words |
| [`internal/cli/check/section_iopriority.go`](../../internal/cli/check/section_iopriority.go) | the macos-user row is a `[PASS]` naming the policy |

- The measurement it was built on, `TestMacosUserIOPolicyAcrossTheLaunchArgv`: a wrapper set
  `IOPOL_THROTTLE` on itself at process scope and execed `yolo` (the harness's
  `withLauncherPrefix`), and Python's `ctypes` over libSystem's `setiopolicy_np`/`getiopolicy_np`
  set and read it. It stays as the inheritance regression.
- The check of the built step, `TestMacosUserIOPriorityIsApplied`: no wrapper, `resources.io` of
  `"idle"`, `{"priority": "low"}` and `{}`, read inside the sandbox by the same reader, expecting
  3, 4 and the launcher's own baseline.

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
- ⚠ An attach briefs what the jail was launched with, never the current config
  ([IO-D12](io-priority.md#11-decision-ledger)). `refreshJailBriefings` takes the priority from
  its caller for that reason, and a cgroup half's briefing line needs the same treatment: on an
  attach, what the container was created with, from its `inspect`.
- Reuse: `noteIOPriority` (`internal/cli/run/iopriority.go`) is where a dropped flag's one launch
  line goes, and `sectionIOPriority` is where the delegation row goes, as a `hostFact` `[SKIP]`
  in a jail.
