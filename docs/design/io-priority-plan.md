---
title: "Implementation sketch: disk I/O priority"
date: 2026-09-27
status: draft
tags: [sketch, plan, io, resources, cgroups, implementation]
summary: "Companion implementation sketch for docs/design/io-priority.md: the files the priority touches, the config shape, the pinned-set-and-re-exec sequence and its traps, the shared scheduler resolver, and the test matrix. Entries resting on an open question name it."
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
| [`userguide/reference/settings-per-setup.md`](../../userguide/reference/settings-per-setup.md) | a row per backend × host OS, from [design §5.2](io-priority.md#52-the-launcher-one-decision-per-backend-and-the-disk-it-lands-on)'s table |
| [`internal/cli/run/backendcaps.go`](../../internal/cli/run/backendcaps.go) | the per-backend decision, beside `appliedResourceLimits`. It needs the backend and whether the host is macOS |
| the shared resolver, new | [§4](#4-resolving-the-parent-disk)'s resolver and the design's grading table, in a package both `internal/cli/run` and `internal/cli/check` can import. Where it lives is the implementer's |
| [`internal/cli/run/assemble.go`](../../internal/cli/run/assemble.go) | pass the decided value to the container environment; print the disclosure line where the grading says the value has no effect; print the Warned line for Apple Container and podman on macOS |
| [`internal/cli/run/run.go`](../../internal/cli/run/run.go) | the attach branch (beside `warnIfNoPacks`) repeats the disclosure line for the value the jail was launched with. The jail's frozen boot baseline, `<workspace>/.yolo/config-boot.json` ([`drift.go`](../../internal/config/drift.go)), is one place to read it from |
| the briefing path (`briefedResourceLimits` in `backendcaps.go`) | state the class when passed, without calling it kernel-enforced or effective |
| [`internal/entrypoint/boot.go`](../../internal/entrypoint/boot.go) | the pinned set and re-exec as the first thing in `Main`, before `EnvFromOS` and `attachBootLog`; the outcome report right after `attachBootLog` |
| `internal/entrypoint/ioprio_linux.go`, new | the sequence in [§3](#3-the-pinned-set-the-re-exec-and-their-traps) |
| `internal/entrypoint/ioprio_other.go`, new | a no-op. The package's darwin build is live (`yolo internal darwin-bootstrap` runs its generators, [`darwin.go`](../../internal/entrypoint/darwin.go)), but that path never calls `Main`, so the no-op is never reached there |
| `internal/cli/check/`, a new section file | the scheduler row, on the shared resolver |
| [`internal/macosuser/orchestrator.go`](../../internal/macosuser/orchestrator.go) | build step 3: leave `io` out of the "resources are NOT enforced" key list when it resolves to `"normal"` ([IO-D8](io-priority.md#11-decision-ledger)). Build step 5: drop it from the list once `setiopolicy_np` ships |
| [`internal/cli/run/hostloopback.go`](../../internal/cli/run/hostloopback.go) | only if [OQ-IO7](io-priority.md#OQ-IO7) ships a cgroup half: `podmanInfo.Host` gains `CgroupControllers []string` (`json:"cgroupControllers"`) |
| [`cmd/yolo-cglimit`](../../cmd/yolo-cglimit) | only if [OQ-IO4](io-priority.md#OQ-IO4) says yes |

## 2. The config shape

`resources` is a `*jsonx.OrderedMap`, walked by `validateResources` and read with `mapGet` in
`appliedResourceLimits`; there is no typed `ResourcesConfig`. Read `io` the same way: a string, or
an `*jsonx.OrderedMap` with a `priority` string. Resolve both to one of three values in one helper,
so the validator, the launcher, macos-user's resources line and `yolo check` cannot read the
shorthand differently.

## 3. The pinned set, the re-exec, and their traps

- **Numbers.** The standard library's `syscall.SYS_IOPRIO_SET` and `SYS_IOPRIO_GET` exist on both
  shipped arches: 251 and 252 on amd64, 30 and 31 on arm64 (Go 1.26.7). `which` 1 is
  `IOPRIO_WHO_PROCESS`, which with `who` 0 means the calling thread.
- **Value.** `class << 13 | level`, with class 2 for `BE` and 3 for `IDLE`. Unset is 0.
  ⚠ Check the level range yourself: the kernel now reads the bits above the 3-bit level as hint
  bits, so level 8 was accepted and read back as level 0 with a hint (measured), not rejected.
- **Sequence** ([IO-D1](io-priority.md#11-decision-ledger), [IO-D4](io-priority.md#11-decision-ledger)):
  1. If the marker is in the environment, this is the re-executed image: remove the marker
     (`os.Unsetenv`), record success, and go to step 6.
  2. If the launcher's value is absent or `"normal"`, record nothing and go to step 6. An
     unrecognized value is recorded as a warning and not applied.
  3. `runtime.LockOSThread()`, then `ioprio_set(IOPRIO_WHO_PROCESS, 0, value)`. On error, record
     the failure and go to step 6: nothing was set anywhere.
  4. `syscall.Exec("/proc/self/exe", os.Args, <environment plus the marker>)`. The argv must be
     exactly `os.Args`, because `Main`'s args choose the command the shell runs.
  5. The exec returned, so it failed: `ioprio_set(IOPRIO_WHO_PROCESS, 0, 0)` on the same,
     still-locked thread, record the failure (and a failed reset, if it failed), then
     `runtime.UnlockOSThread()`.
  6. After `attachBootLog`: report what was recorded. A failure is a warning naming
     `resources.io.priority`; a success is a log-only note (`e.LogOnly`).
- **Why the whole sequence comes first.** It reads the launcher's value with `os.Getenv` and runs
  before `EnvFromOS`, so the environment `EnvFromOS` copies no longer holds the marker. Nothing
  earlier in `Main` has side effects the exec would repeat: `packload.ReleaseEmbedded` is deferred
  but nothing is leased yet. `attachBootLog` does have one: it renames `boot.log` to the
  previous-boot name before creating a fresh one, so a re-exec after it rotates twice and loses
  the previous boot's log.
- ⚠ **Do not "simplify" to an unlocked set, to `LockOSThread` without the re-exec, or to setting
  just before `execBash`.** An unlocked set-then-exec arrived with the class in 3 of 30 runs; a
  locked set with no re-exec reached 0 of 16 children started from other goroutines (both
  measured). Setting just before `execBash` is deterministic once locked, 30 of 30, but misses
  every child `Main` started earlier ([design §6](io-priority.md#6-alternatives-considered)).
- ⚠ **Do not bring back the process-group route** (`setpgid`, then `IOPRIO_WHO_PGRP`). It reaches
  every process in the group, which is why it needed a group of its own and a foreground-group
  guard; the re-exec touches one thread.
- **The environment value** crosses host to jail, so it is a host↔jail contract: the source-skew
  gate covers it (AGENTS.md, *"THE TWO HALVES DEPLOY ON DIFFERENT CADENCES"*). An attach inherits
  the launch's environment and runs the launch's mounted entrypoint, so no contract tag is needed.
  The variable's name and the marker's name are the implementer's. The marker never leaves the
  entrypoint: step 1 removes it before any child exists.

## 4. Resolving the parent disk

1. Find the mount for the path in `/proc/self/mountinfo` by the **longest mount-point prefix**,
   and take its **source** (`/dev/mapper/root` here) and **filesystem type**.
   ⚠ Never match `stat`'s `st_dev` against the mount table's device number: on btrfs they differ,
   `0:28` in mountinfo against `0:61` for `/workspace` and `0:62` for `/nix/store` (measured), and
   in-jail the source device node does not exist.
2. Branch on what was found:
   - `virtiofs` (an Apple Container or podman-machine jail): no disk; the design's Warned row.
   - A source that is not a block device (ZFS, NFS, tmpfs, overlay): no disk; `[SKIP]` naming
     the filesystem type.
   - Otherwise map the source to a `/sys/class/block/<name>`. For `/dev/mapper/<name>`, match
     `/sys/block/dm-*/dm/name` (`root` reads back from `dm-0`, measured).
3. If the device has entries under `slaves/`, recurse into each (LUKS and LVM). If it has a
   `partition` file, step to its parent directory. A leaf with neither is a parent disk.
4. Read the bracketed name in `queue/scheduler`. Note dm-crypt on the way down:
   `/sys/block/dm-N/dm/uuid` starts with `CRYPT-`.
5. The check, at the host, also resolves podman's storage root (`podman info`'s
   `store.graphRoot`). The launch resolves the workspace only. In-jail, the workspace mount is the
   one that matters: build output lands there.

Measured chain on the verification host: `/workspace` → `/dev/mapper/root` → `dm-0`
(`CRYPT-LUKS2-…`) → `nvme0n1p2` → `nvme0n1` → `[kyber]`.

## 5. Test matrix

1. **Config (`internal/config`):** the string and object forms; each of the three values;
   `null` and `{}` as unset; `""`, `"Low"`, a number, an unknown object key, and `weight` (an
   error until [OQ-IO7](io-priority.md#OQ-IO7) ships one).
2. **Entrypoint (`internal/entrypoint`):** the re-exec replaces the process, so run the apply in a
   subprocess: the test binary re-executing itself under a `TestMain` guard is the usual shape.
   Start several busy goroutines first, so the runtime has many threads. Then apply `"low"` and
   assert every tid under `/proc/self/task` of the re-executed image reads `BE7` through
   `ioprio_get`, that children spawned from other goroutines inherit it, that the marker is gone
   from the child environment, and that a second pass does not re-execute. Assert `"normal"`
   makes no call. The test must fail if the call site in `Main` is deleted, so drive `Main`'s own
   entry, not the helper alone.
3. **Launcher (`internal/cli/run`):** the value is passed on podman (Linux and nested) and not on
   Apple Container or podman under macOS, which print the Warned line. Over fake sysfs and
   mountinfo trees: the disclosure line prints for `"low"` on Kyber and on mq-deadline and not on
   BFQ, and nothing prints for an unresolvable mount. The attach branch repeats it. The briefing
   states the class exactly where it was passed. macos-user's resources line omits an `io` of
   `"normal"`, `null` or `{}`.
4. **Check (`internal/cli/check`):** the grading table over fake sysfs trees: `bfq`,
   `mq-deadline`, `kyber` and `none`; a dm-crypt chain onto BFQ; a `virtiofs` mount giving the
   Warned `[WARN]`; a tmpfs mount giving a `[SKIP]` that names `tmpfs`; an unreadable tree giving
   `hostFact`'s `[SKIP]` only when in-jail; no row when undeclared.
5. **Integration (`integration/`):** a jail with `"resources": {"io": "low"}`. Read the priority
   of every thread of PID 2 and of a child shell with a small `ioprio_get` helper, not
   `/proc/<pid>/io` (that is I/O accounting) and not `ionice` (absent from the image, and
   `ionice -p` reads one thread).
