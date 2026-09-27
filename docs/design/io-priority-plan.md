---
title: "Implementation sketch: disk I/O priority"
date: 2026-09-27
status: draft
tags: [sketch, plan, io, resources, cgroups, implementation]
summary: "Companion implementation sketch for docs/design/io-priority.md: the files the priority touches, the config shape, the syscall sequence and its traps, the scheduler resolution, and the test matrix. Entries resting on an open question name it."
vantage:
  status-chip: true
---

# Implementation sketch: disk I/O priority

**Status:** SKETCH, 2026-09-27 — incomplete, and unstable while questions are open. File map
checked against `b0460995`.

> [!IMPORTANT]
> This is the companion sketch for [`io-priority.md`](io-priority.md), and it is not a hand-off:
> do not build from it while it says SKETCH. **The design wins on behavior**; this file holds
> only material nobody needs to rule on. Steps 1 to 4 of the design's
> [build order](io-priority.md#9-what-i-would-build-in-order) wait on no open question.

## 1. Files the priority touches

| Where | What changes |
| :--- | :--- |
| [`internal/config/config.go`](../../internal/config/config.go) | `knownResourcesKeys` gains `io`, and a new known-keys set holds `priority` for the object form |
| [`internal/config/validate.go`](../../internal/config/validate.go) | `validateResources` accepts `io` as a string or an object ([design §4](io-priority.md#4-the-configuration-surface)) |
| [`internal/config/inherit.go`](../../internal/config/inherit.go) | the `resources` census reason ("memory/cpu limits…") mentions the I/O priority |
| [`internal/cli/config_ref.txt`](../../internal/cli/config_ref.txt) | an `io` entry under `resources`. ⚠ The block's lead sentence says the limits *"are enforced by the kernel — the jail cannot exceed them"*, which is false for a priority the agent can raise, so word the `io` entry on its own |
| [`userguide/reference/settings-per-setup.md`](../../userguide/reference/settings-per-setup.md) | a row per backend × host OS, from [design §5.2](io-priority.md#52-the-launcher-one-decision-per-backend)'s table |
| [`internal/cli/run/backendcaps.go`](../../internal/cli/run/backendcaps.go) | the per-backend decision, beside `appliedResourceLimits`. It needs the backend and whether the host is macOS, and no probe |
| [`internal/cli/run/assemble.go`](../../internal/cli/run/assemble.go) | pass the decided value to the container environment; print the Warned line for Apple Container and podman on macOS |
| the briefing path (`briefedResourceLimits` in `backendcaps.go`) | state the class when passed, without calling it kernel-enforced |
| [`internal/entrypoint/boot.go`](../../internal/entrypoint/boot.go) | the first call in `Main`, ahead of every `genStep` |
| `internal/entrypoint/ioprio_linux.go`, new | the syscall sequence in [§3](#3-the-syscall-sequence-and-its-traps) |
| `internal/entrypoint/ioprio_other.go`, new | a no-op: the entrypoint's non-Linux build exists only so cross-compiles type-check ([`exec.go`](../../internal/entrypoint/exec.go)) |
| `internal/cli/check/`, a new section file | the scheduler row ([§4](#4-resolving-the-parent-disk)) |
| [`internal/macosuser/orchestrator.go`](../../internal/macosuser/orchestrator.go) | build step 5 only: drop the priority from the "resources are NOT enforced" key list once `setiopolicy_np` ships |
| [`internal/cli/run/hostloopback.go`](../../internal/cli/run/hostloopback.go) | only if [OQ-IO7](io-priority.md#OQ-IO7) ships a cgroup half: `podmanInfo.Host` gains `CgroupControllers []string` (`json:"cgroupControllers"`) |
| [`cmd/yolo-cglimit`](../../cmd/yolo-cglimit) | only if [OQ-IO4](io-priority.md#OQ-IO4) says yes |

## 2. The config shape

`resources` is a `*jsonx.OrderedMap`, walked by `validateResources` and read with `mapGet` in
`appliedResourceLimits`; there is no typed `ResourcesConfig`. Read `io` the same way: a string, or
an `*jsonx.OrderedMap` with a `priority` string. Resolve both to one of three values in one helper,
so the validator, the launcher and `yolo check` cannot read the shorthand differently.

## 3. The syscall sequence and its traps

- **Numbers.** The standard library's `syscall.SYS_IOPRIO_SET` and `SYS_IOPRIO_GET` exist on both
  shipped arches: 251 and 252 on amd64, 30 and 31 on arm64 (Go 1.26.7). `which`: 1 is
  `IOPRIO_WHO_PROCESS`, 2 is `IOPRIO_WHO_PGRP`.
- **Value.** `class << 13 | level`, with class 2 for `BE` and 3 for `IDLE`. Unset is 0.
  ⚠ Check the level range yourself: the kernel now reads the bits above the 3-bit level as hint
  bits, so level 8 was accepted and read back as level 0 with a hint (measured), not rejected.
- **Sequence** ([IO-D1](io-priority.md#11-decision-ledger)):
  1. If `getpgrp() != getpid()`, and the entrypoint's controlling terminal (if any) has its group
     in the foreground, apply nothing and warn. Otherwise call `setpgid(0, 0)`.
  2. `EPERM` from `setpgid` means the entrypoint is a session leader, which already leads its
     group. That is expected, not a failure.
  3. `ioprio_set(IOPRIO_WHO_PGRP, 0, value)`.
  4. On any error, `ioprio_set(IOPRIO_WHO_PGRP, 0, 0)` to reset, and warn once
     ([IO-D4](io-priority.md#11-decision-ledger)). The kernel's group loop stops at its first
     failure (`block/ioprio.c`), so a partial set is possible.
- ⚠ **Do not "simplify" to `IOPRIO_WHO_PROCESS`, `LockOSThread`, or setting just before
  `execBash`.** Each was measured to leave most threads or children unset
  ([design §3.1](io-priority.md#31-process-priority), [§6](io-priority.md#6-alternatives-considered)).
- **The environment value** crosses host to jail, so it is a host↔jail contract: the source-skew
  gate covers it (AGENTS.md, *"THE TWO HALVES DEPLOY ON DIFFERENT CADENCES"*). An attach inherits
  the launch's environment and runs the launch's mounted entrypoint, so no contract tag is needed.
  The variable's name is the implementer's.

## 4. Resolving the parent disk

1. Find the mount for the path in `/proc/self/mountinfo` and take its **source**
   (`/dev/mapper/root` here), not its device number. On btrfs both the mount table and `st_dev`
   give anonymous numbers (`0:28`, measured), and in-jail the source device node does not exist.
2. Map the source to a `/sys/class/block/<name>`. For `/dev/mapper/<name>`, match
   `/sys/block/dm-*/dm/name` (`root` reads back from `dm-0`, measured).
3. If the device has entries under `slaves/`, recurse into each (LUKS and LVM). If it has a
   `partition` file, step to its parent directory. A leaf with neither is a parent disk.
4. Read the bracketed name in `queue/scheduler`. Note dm-crypt on the way down:
   `/sys/block/dm-N/dm/uuid` starts with `CRYPT-`.
5. At the host, add podman's storage root (`podman info`'s `store.graphRoot`). In-jail, the
   workspace mount is the one that matters: build output lands there.

Measured chain on the verification host: `/workspace` → `/dev/mapper/root` → `dm-0`
(`CRYPT-LUKS2-…`) → `nvme0n1p2` → `nvme0n1` → `[kyber]`.

## 5. Test matrix

1. **Config (`internal/config`):** the string and object forms; each of the three values;
   `null` and `{}` as unset; `""`, `"Low"`, a number, an unknown object key, and `weight` (an
   error until [OQ-IO7](io-priority.md#OQ-IO7) ships one).
2. **Entrypoint (`internal/entrypoint`):** start several busy goroutines first, so the runtime
   has many threads. Then apply `"low"` and assert every tid under `/proc/self/task` reads `BE7`
   through `ioprio_get`, and that children spawned from other goroutines inherit it. Assert
   `"normal"` makes no call. The test must fail if the call site in `Main` is deleted, so drive
   `Main`'s own entry, not the helper alone.
3. **Launcher (`internal/cli/run`):** the value is passed on podman (Linux and nested) and not on
   Apple Container or podman under macOS, which print the Warned line. The briefing states the
   class exactly where it was passed.
4. **Check (`internal/cli/check`):** the grading table over fake sysfs trees: `bfq`,
   `mq-deadline`, `kyber` and `none`; a dm-crypt chain onto BFQ; an unreadable tree giving
   `[SKIP]`; no row when undeclared.
5. **Integration (`integration/`):** a jail with `"resources": {"io": "low"}`. Read the priority
   of every thread of PID 2 and of a child shell with a small `ioprio_get` helper, not
   `/proc/<pid>/io` (that is I/O accounting) and not `ionice` (absent from the image, and
   `ionice -p` reads one thread).
