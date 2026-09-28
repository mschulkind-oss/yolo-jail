---
title: "Plan: disk I/O priority, build steps 1 to 4"
date: 2026-09-27
status: accepted
tags: [plan, io, resources, cgroups, implementation]
summary: "Build hand-off for docs/design/io-priority.md steps 1 to 4: the file map, the helpers to reuse, the traps in the pinned-set-and-re-exec sequence and the disk resolver, the build order, and the tests and docs that ship with it. Steps 5 and 6 stay blocked."
vantage:
  status-chip: true
---

# Plan: disk I/O priority, build steps 1 to 4

**Design:** [`io-priority.md`](io-priority.md) · **Status:** ready for steps 1 to 4 · Written
against `8e753c46` (`b0460995` plus the design's two doc commits), 2026-09-27.

Precedence: the design wins on behavior, the tree wins on fact, and this file is advice and
the first thing to be wrong. Steps 5 and 6 are blocked (see [Blockers](#blockers)).

## Map

| Path | Change |
| :--- | :--- |
| `internal/ioprio/` | **new.** `Parse` / `FromResources` (the one reading of `resources.io`), `KernelValue`, `Resolve` (mount → parent disks), `Grade`, `NoEffect`; `sys_linux.go` / `sys_other.go` hold `ioprio_set` / `ioprio_get` |
| `internal/config/config.go` | `knownResourcesKeys` gains `io` |
| `internal/config/validate.go` | `validateResources` calls `ioprio.Parse` and adds its problems |
| `internal/config/inherit.go` | the `resources` census reason names the priority |
| `internal/cli/config_ref.txt` | an `io` entry; the block's "enforced by the kernel" lead is scoped to the three limits |
| `internal/entrypoint/iopriority.go` | **new.** `applyIOPriority` (the sequence) and `reportIOPriority` |
| `internal/entrypoint/boot.go` | `applyIOPriority` is `Main`'s first statement; `reportIOPriority` follows `attachBootLog` |
| `internal/cli/run/backendcaps.go` | `appliedIOPriority(rt, isMacOS, resCfg)`, the per-backend decision |
| `internal/cli/run/assemble.go` | `-e YOLO_IO_PRIORITY=<v>` beside `resourceArgs` |
| `internal/cli/run/iopriority.go` | **new.** `noteIOPriority` (the disclosure and the Warned line) and the attach's value from the inspected env |
| `internal/cli/run/run.go` | two call sites, each beside `warnIfNoPacks`: `runContainer` after the banner, `attachExisting` after "Attaching" |
| `internal/cli/run/prepare.go`, `internal/jailcontent/briefing.go` | `BriefingInput.IOPriority`, rendered on its own line, never inside the "kernel-enforced" limits line |
| `internal/macosuser/orchestrator.go` | the resources line drops an `io` that resolves to `"normal"` (IO-D8) |
| `internal/cli/check/section_iopriority.go`, `check.go` | **new** section, called after "Disk usage" |
| `integration/iopriority_test.go` | **new** end-to-end test |

## Reuse

- `attachExecArgv` (`internal/cli/run/attachnocolor_test.go`) drives the real `attachExisting`
  to its exec with a faked `inspect`; put `YOLO_IO_PRIORITY=low` in its frozen env.
- `methodDecl(t, "run.go", "runContainer")` (`contracttags_test.go`) for the fresh-launch
  call-site pin; `callsFunc` (`internal/entrypoint/hold_test.go`) for `Main`'s.
- `inspectContainerEnv`'s lines are the jail's launch environment. The attach reads the launched
  value there; nothing needs `.yolo/config-boot.json`.
- `e.warn` / `e.note` (`internal/entrypoint/env.go`): the boot stream and the log-only sink.
- `r.hostFact` (`internal/cli/check/reporter.go`) for the in-jail unreadable row.
- `startYoloBackground` (`integration/concurrentlaunch_test.go`) plus `runCommand` for a launch
  and an attach into one jail.

## House style that is not obvious

- Every `rt == "…"` line under `internal/cli/run` carries a trailing
  `// parity: <Disposition> — <reason>` or `TestTheParityCensusRejectsAnUndeclaredBranch` fails.
- A `check` method named `section*` is pinned to its call in `Check()` by
  `TestEverySectionIsWired`, for free.
- Launch notices go to `o.Stderr` through `o.pr(...)` rich markup, never stdout.

## Traps

- **Re-exec before `attachBootLog`, never after**: the attach rotates `boot.log` to
  `boot.log.prev`, so a second pass loses the previous boot's log.
- **Unset the marker before `EnvFromOS`**, or every child inherits it.
- **argv is exactly `os.Args`**: `Main`'s args choose the command the shell runs.
- **Level 8 is accepted by the kernel** and read back as level 0 plus a hint bit. Never pass a
  raw level; only the enum's two values reach `ioprio_set`.
- **`st_dev` is not the mount table's device number on btrfs**: match the longest mount-point
  prefix and follow the source name. `/dev/mapper/<n>` does not exist in a jail; match
  `/sys/class/block/dm-*/dm/name`.
- **A dm or zram device has no `queue/scheduler`.** Only a leaf under slaves and partitions does.
- **Darwin path class**: `Resolve` evaluates symlinks on the path, so a test fixture that names a
  real `t.TempDir()` path fails on macOS. Name fictional paths in fake mount tables.
- **Jail env skews `go test`**: run `env -u YOLO_VERSION -u YOLO_HOST_LAYERS go test …`, or
  `check` believes it is in a jail.
- **`ioprio_get` on an unset thread reads 0** even after `nice(19)`; compare raw values.

## Build order

1. `internal/ioprio` value half, config validation, config-ref, census reason.
   → `go test ./internal/ioprio ./internal/config ./internal/cli`
2. Entrypoint sequence, `Main` call sites, report. → `go test ./internal/entrypoint`
3. Resolver and grading in `internal/ioprio`; launcher decision, env, disclosure, attach
   repeat, briefing, macos-user line. → `go test ./internal/ioprio ./internal/cli/run
   ./internal/jailcontent ./internal/macosuser`; `GOOS=darwin go vet ./...`
4. `yolo check` section. → `go test ./internal/cli/check`
5. Integration test, user docs, changelog, design status and ledger.
   → `go test -count=1 -timeout 0 -run IOPriority ./integration`, then the whole suite.

## Ships with

- **Unit, `internal/ioprio`:** string and object forms; each value; `null` and `{}` unset; `""`,
  `"Low"`, a number, an unknown key and `weight` refused; resolver over fake trees for bfq,
  mq-deadline, kyber, none, a LUKS chain, LVM over two disks, a partition, tmpfs, virtiofs and
  an unreadable sysfs.
- **Unit, entrypoint:** a subprocess (a `TestMain` guard) that starts many threads, applies
  `"low"`, and reads `BE7` on every thread and on children from other goroutines, with the
  marker gone and exactly one re-exec; in-process fakes for a failed set, a failed exec, a
  failed reset and an unknown value; an AST pin on `Main`'s order.
- **Unit, launcher:** the env on podman Linux and nested, absent on Apple Container and podman
  on macOS; the disclosure on kyber and mq-deadline and not bfq; nothing for an unresolvable
  mount; the attach repeats it from the frozen env; briefing line exactly where passed;
  macos-user's line.
- **Unit, check:** each grade row, virtiofs, tmpfs, unreadable in-jail and at the host, no row
  when undeclared.
- **Integration:** `{"resources": {"io": "low"}}`, a launch and an attach into one jail; every
  thread but PID 1 reads `be/7`, from a static probe built by the test; the disclosure prints
  exactly where `ioprio.NoEffect` says the workspace's disk ignores it.
- **Docs:** `userguide/reference/settings-per-setup.md` (resources table and capability row),
  `userguide/guides/macos.md` (both backend tables), `CHANGELOG.md` Unreleased.
- Cheap and yours: package layout inside `internal/ioprio`, the wording of every line.

## Don't

- Don't set the priority in `execBash`, unlocked, or without the re-exec; don't bring back the
  process-group route ([design §6](io-priority.md#6-alternatives-considered)).
- Don't add `ionice` to the image, and don't read `/proc/<pid>/io` for priority.
- Don't emit any `--blkio-weight` or `--cgroup-conf` flag.

## Blockers

- Step 5 (macos-user): a Mac measurement that the policy survives `sudo` and `sandbox-exec`
  ([IO-D7](io-priority.md#11-decision-ledger)).
- Step 6 (a cgroup half): [OQ-IO7](io-priority.md#OQ-IO7).
- The default: [OQ-IO3](io-priority.md#OQ-IO3) changes what an unset key means and nothing here.
