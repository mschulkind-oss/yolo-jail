---
title: "Yielding the disk: which kernel lever reaches a jail build's I/O, and on which scheduler"
date: 2026-09-27
status: in-review
stage: DESIGN
next: "Read the Mac measurement step 5 waits on from the scheduled macos-user.yml run (TestMacosUserIOPolicyAcrossTheLaunchArgv's IOPOL VERDICT line) and record it at IO-D7"
tags: [resources, io, cgroups, bfq, storage, latency, podman, performance]
summary: "A jail build can saturate the host disk and stall the desktop. Process I/O priority is free to set and reaches every program the jail runs, but only their reads and synchronous writes, and only on BFQ or mq-deadline disks; buffered writeback answers to the cgroup io controller alone, which a stock rootless host neither delegates nor enables. The design sets a declared resources.io.priority on every thread of the jail and names, at launch and in yolo check, each place it does nothing, and that much is built. Of three filed questions, one is decided and two are answered from existing rulings; four stay open: the default, a per-command flag, the host notch, and whether any cgroup half ships."
vantage:
  status-chip: true
---

# Yielding the disk — which kernel lever reaches a jail build's I/O, and on which scheduler

**Status:** 2026-09-27. Build steps 1 to 4 are built; step 5 waits on a Mac, whose measurement
was written on 2026-10-01 for the scheduled `macos-user.yml` job, and step 6 on
[OQ-IO7](#OQ-IO7). MEASURED: in a jail nested in a rootless podman jail on Linux 7.1.8, every
thread but PID 1 read `be/7` after a launch and after an attach, and both named the Kyber NVMe
under LUKS. UNMEASURED: a BFQ disk's latency under load, and both macOS VM backends. Kernel
source read at v7.1.

> **In short.** Process I/O priority is free to set and reaches every program a jail runs, but only
> their reads and synchronous writes on BFQ or mq-deadline disks; buffered writes answer to a cgroup
> controller a stock rootless host never hands over. So yolo sets a declared priority on every
> thread of the jail and names, at launch and in `yolo check`, each place it does nothing.

**Why it matters.** A Rust build in a jail drove a host SSD to 95–99% utilization and stalled the
desktop, while the CPU weight set on the host did nothing for the disk ([§1](#1-what-happened)).

**The shape.** One key, `resources.io.priority`. `yolo-entrypoint` sets it on one pinned thread and
re-executes itself, so every thread and child descends from one that holds it; the launcher decides
per backend whether to pass it, and it shares one grading of the disk under the workspace with
`yolo check`.

**Cost.** Builds slow down whenever the disk is contended, and every boot or attach that declares a
priority pays one extra exec of the entrypoint. On a Kyber or `none` disk, the usual NVMe default
and the maintainer's own jail storage, nothing here acts without a root-only host change, and the
launch says so.

**Start at [§3](#3-what-each-kernel-lever-reaches)**, the table of which lever reaches which I/O on
which scheduler. The rest follows from it.

**Needs your ruling:** [OQ-IO3](#OQ-IO3), [OQ-IO4](#OQ-IO4), [OQ-IO6](#OQ-IO6), [OQ-IO7](#OQ-IO7).

**Reads with:** [`io-priority-plan.md`](io-priority-plan.md) (the implementation sketch for steps
5 and 6, both blocked), [`backend-parity.md`](backend-parity.md) (the disposition words
[§5.2](#52-the-launcher-one-decision-per-backend-and-the-disk-it-lands-on) uses),
[`declaration-parity.md`](declaration-parity.md) (P1, which decides the failure paths, and P4,
which bounds who may declare it at the host notch).

---

## Words this doc uses

- **Class.** The kernel's I/O priority class: `RT` (real time), `BE` (best effort, levels 0–7, 0
  highest), `IDLE`, or `NONE` (never set). A scheduler resolves `NONE` from the thread's CPU nice
  value: `BE` at level (nice + 20) / 5, so `BE4` at nice 0, and `IDLE` under `SCHED_IDLE`.
- **Priority.** A class and level set with `ioprio_set(2)`. It belongs to one **thread**, not to a
  process ([§3.1](#31-process-priority)).
- **Writeback.** The kernel writing dirty page-cache pages to disk from its own flusher threads,
  after a buffered `write(2)` has already returned.
- **The cgroup half** *(coined here)*. Any setting written into the container's cgroup `io.*`
  files: `io.bfq.weight`, `io.weight` or `io.prio.class`. It is not process priority, and no yolo
  key sets one today.
- **Parent disk** *(coined here)*. The physical disk a path's I/O lands on, found by following a
  mount's source through device-mapper slaves and partitions. Only a parent disk has a scheduler:
  a LUKS or LVM device has no `queue/scheduler` file (measured).
- **Honored, HonoredBy, Warned, Dropped, Refused** are the dispositions of
  [`backend-parity.md` §3](backend-parity.md#3-the-dispositions--the-most-important-section).

## 1. What happened

On 2026-09-27 a Rust build in the `juce-projects` jail ran beside the desktop. The report says the
host SSD `sdb` sat at 95–99% utilization and interactive programs stalled. The report observed four
things:

1. **The CPU weight did not help the disk.** `CPUWeight=20` throttled the CPU under contention,
   and disk I/O stayed unrestricted. That weight was a host systemd property. yolo has no
   CPU-weight key: `resources.cpus` maps to `--cpus`, a CFS quota.
2. **The disks ran different schedulers.** `sdb` ran BFQ, which honors process priority. The NVMe
   drive ran Kyber, which ignores it.
3. **The io controller was not delegated.** The user slice's `cgroup.controllers` held
   `cpu memory pids`, not `io`. That is systemd's stock `Delegate=pids memory cpu` for
   `user@.service`, and the jail this doc was verified in shows the same three.
4. **Wrapping commands by hand did not work for agents.** The build fanned out into `rustc`,
   build scripts and test harnesses, and prefixing commands with `ionice` was unreliable.
   `ionice` is not in the jail image either (measured).

The machine this doc was verified on has no `sdb`. Its jail storage sits on a Kyber NVMe under
LUKS ([§3.3](#33-what-that-means-on-real-hosts)), so the incident was not reproduced there.

## 2. Non-Goals

1. **No bandwidth caps.** Nothing sets `io.max`. When the disk is otherwise idle the jail should
   use all of it; the goal is to yield under contention, not to throttle.
2. **No host reconfiguration.** yolo never runs `sudo`, never writes `/etc/systemd`, and never
   changes a systemd unit's properties. Where a host change would help, yolo names it and a human
   makes it.
3. **No enforcement claim.** Process priority is advisory: any thread can raise its own class
   back to `BE0` without a capability (measured). yolo sets it and never tells anyone the agent
   cannot undo it.
4. **No per-job weight through the cgroup delegate.** With `io` delegated, the delegate
   ([`security-shim.md`](../reference/security-shim.md)) could one day write a per-job weight.
   That widens a protocol fixed at three operations and three controllers, so it is a
   security-shim change, not this doc's.
5. **Host-side watchers** (an indexer or IDE descending into a jail's build directory) are those
   tools' configuration.
6. **No priority for work a host process does for the jail.** That work keeps its own priority:
   - **`nix build`.** Wherever the host nix daemon is mounted, the jail runs with
     `NIX_REMOTE=daemon` ([`assemble.go`](../../internal/cli/run/assemble.go)), so a build's
     builders and store writes run in the daemon's processes on the host, which descend from the
     daemon, not from the entrypoint. Only the client, which evaluates and asks the daemon to
     build, runs in the jail.
   - **The host loophole daemons**, which descend from the host `yolo` that started them.

   Changing their priority is the act [§5.1](#51-the-entrypoint-one-pinned-thread-then-a-re-exec)
   forbids, and the nix daemon belongs to another user (root, on a standard install), so it would
   need `CAP_SYS_NICE` as well ([§3.1](#31-process-priority)). The lever for nix builds is a host
   change a human makes, and it covers every build on the host, not only a jail's: on NixOS,
   `nix.daemonIOSchedClass` and `nix.daemonIOSchedPriority` set the daemon unit's
   `IOSchedulingClass=` and `IOSchedulingPriority=`, which its builds inherit. They default to
   best effort, level 4 (read from the pinned nixpkgs source).

## 3. What each kernel lever reaches

This is the load-bearing section. Four levers exist, and they differ on the two things that
matter: whether they reach **writeback**, which is most of a build's `target/` output, and which
**scheduler** honors them.

| Lever | Who can set it | Reaches writeback | Honored by | Agent can undo it |
| :--- | :--- | :--- | :--- | :--- |
| **Process priority** (`ioprio_set`) | any thread, on itself; `BE` and `IDLE` need no capability | **no** | BFQ (class and level); mq-deadline (class only) | yes |
| `io.bfq.weight` (1–1000, default 100) | the container's cgroup, once `io` is delegated | yes | BFQ | no |
| `io.weight` (1–10000, default 100) | the same | yes | any disk for which root enabled iocost in the root cgroup's `io.cost.qos`; off by default | no |
| `io.prio.class` | the same | yes | mq-deadline; BFQ ignores it | no |

Kyber and `none` honor no process priority at all. The only lever that reaches them is `io.weight`
on a disk where root switched iocost on.

### 3.1 Process priority

- **It is per thread.** `ioprio_set(IOPRIO_WHO_PROCESS, 0, …)` sets the calling thread. A new
  thread or child copies the priority of the thread that created it
  ([`block/ioprio.c`, `blk-ioc.c`](https://github.com/torvalds/linux/tree/master/block)). In a Go
  program that thread is whichever OS thread the goroutine happened to run on. Measured with
  Go 1.26: after an unlocked set, 1 of 10 threads held `BE7`, and 13 of 16 `exec.Command`
  children came up unset. Pinned with `runtime.LockOSThread`, the one thread is deterministic: a
  set followed by an exec arrived with `BE7` in 30 of 30 runs, against 3 of 30 unlocked (measured).
  **"Set it once and children inherit" holds only once every thread holds it**, which
  [§5.1](#51-the-entrypoint-one-pinned-thread-then-a-re-exec) builds from that one pinned thread.
- **It needs no privilege, in either direction.** `BE` and `IDLE` on oneself need no capability,
  and podman's default seccomp profile allows both calls. Measured as uid 65534 with no
  capabilities: `IDLE` and `BE7` succeeded, and so did raising `IDLE` back to `BE0`. Setting
  *another* process needs the same uid or `CAP_SYS_NICE`. An LSM hook can still deny any of it.
- **`RT` is out of reach.** It needs `CAP_SYS_ADMIN` or `CAP_SYS_NICE` in the initial user
  namespace, so a rootless jail's root gets `EPERM` (measured).
- **Schedulers read it differently.**
  - BFQ weighs `BE` levels as (8 − level) × 10, so `BE7` gets a quarter of `BE4`'s share. It
    serves `IDLE` behind every other class, with one guarantee: once the idle class has gone
    200 ms without service (`BFQ_CL_IDLE_TIMEOUT`, `HZ/5`) it gets a turn, and an idle queue is
    expired after one request while other queues wait. So under contention `IDLE` gets about one
    request per 200 ms.
  - mq-deadline, since Linux 5.14, keeps one queue per class and ignores the level. So `BE7` is
    `BE4` there, and only `IDLE` yields. It dispatches a lower class's request once that request
    has waited 10 s, so `IDLE` is a lower class there, not strict idleness.
  - Kyber and `none` ignore priority.
- **It does not reach writeback.** A block request carries the priority of the task that
  *submits* it, and BFQ reads the submitting task's own priority. A buffered write is submitted
  later by a flusher thread, so process priority covers reads and synchronous writes only.
- **Under dm-crypt, BFQ sees it on reads only.** dm-crypt submits reads from the caller's context
  but, by default, writes from its own worker threads (a mapping opened with `no_write_workqueue`
  submits them from the caller). It copies the priority onto each write, so mq-deadline, which
  reads the request, still sees it there, while BFQ, which reads the submitting thread's I/O
  context (`bfq_check_ioprio_change`), does not.
- **An unset thread reads `none/0`.** `ioprio_get` returns the raw value, so a thread with no class
  still reads `none/0` after `nice(19)`, though BFQ serves it as `BE7`.

### 3.2 The cgroup io controller

- **It must be delegated, and it is not by default.** systemd's `user@.service` delegates
  `pids memory cpu`. A controller the user manager lacks is absent from every cgroup below it.
  Measured in the verification jail: `cgroup.controllers` is `cpu memory pids`; `io.pressure` exists, but
  `io.weight`, `io.max`, `io.stat`, `io.bfq.weight` and `io.prio.class` do not.
- **Delegation alone does not make a weight act.** `io.weight` acts only on a disk for which root
  enabled iocost, and that is off by default. BFQ obeys its own `io.bfq.weight`. `io.cost.qos`
  exists only on the root cgroup, so a jail cannot read whether iocost is on.
- **A weight is relative to siblings only.** Rootless podman's default cgroup parent is
  `user.slice` inside the user's systemd manager (libpod's `SystemdDefaultRootlessCgroupParent`).
  The jail's siblings are other containers, not the desktop's `app.slice`, so a weight on the
  container changes nothing against desktop apps while this jail is the only container running.
  The share between the jail and the desktop is set one level up, on units yolo never touches
  ([Non-Goal 2](#2-non-goals)). Unmeasured on a delegated host.
- **`io.prio.class` rewrites every request the cgroup issues** (kernel
  `Documentation/admin-guide/cgroup-v2.rst`): `idle` makes them all `IDLE`, and `restrict-to-be`
  turns an unset or `RT` class into `BE0`. Writeback is attributed to the cgroup that owns the
  inode (ext2, ext4, btrfs, f2fs and xfs implement this), so the policy reaches writeback and the
  agent cannot undo it. mq-deadline honors it and BFQ does not. Unmeasured.
- ⚠ **dm-crypt keeps the cgroup association on writes.** It allocates each write clone in its
  own worker, and allocation associates a bio with the allocating thread's cgroup, so a LUKS
  disk's writes look as if they escape every weight. They do not: every dm-crypt submission goes
  through `dm_submit_bio_remap`, which copies the original bio's association back first
  ([`drivers/md/dm.c`](https://github.com/torvalds/linux/blob/master/drivers/md/dm.c)). So all three
  cgroup files reach LUKS writes, writeback included. Read at v7.1; unmeasured.
- **The podman spellings.** `--blkio-weight` takes 10–1000; crun writes `io.bfq.weight`, or
  `io.weight` rescaled to 1–10000 when `io.bfq.weight` is absent. `--cgroup-conf KEY=VALUE` writes
  one file as given. **There is no `--io-weight` flag.**
- **An undelegated flag is fatal.** Asked to write an io file the cgroup lacks, crun fails
  container creation with ``controller `io` is not available under <path>`` (crun source; not run
  on a rootless host). So a cgroup-half flag may be emitted only on proof that `io` is there.

### 3.3 What that means on real hosts

| Disk under the workspace | `"idle"` | `"low"` | cgroup half, `io` delegated |
| :--- | :--- | :--- | :--- |
| BFQ | acts on reads and sync writes | acts on reads and sync writes | `io.bfq.weight` acts, among siblings |
| BFQ under LUKS | acts on reads | acts on reads | as above, writes included ([§3.2](#32-the-cgroup-io-controller)) |
| mq-deadline | acts on reads and sync writes | **no effect** | `io.prio.class` acts, writeback included |
| Kyber or `none` | **no effect** | **no effect** | only `io.weight`, with iocost on |

LUKS changes nothing on mq-deadline, which reads each request's own priority.

On the machine this doc was verified on, `/workspace`, the jail's home and `/nix/store` all sit
on one btrfs filesystem on `dm-0`, a LUKS2 device over a partition of an NVMe disk running Kyber.
Neither half of this design acts there. The incident's machine, a BFQ SSD, is the case it helps
most.

## 4. The configuration surface

`resources` gains an `io` key:

```jsonc
{
  "resources": {
    "memory": "8g",
    "io": { "priority": "low" }
  }
}
```

A string is shorthand for the priority alone: `"io": "low"` means `{"priority": "low"}` and
nothing else. It never implies a cgroup setting the user did not write.

| `priority` | Linux | macos-user ([§5.5](#55-macos-user-the-second-step)) |
| :--- | :--- | :--- |
| `"idle"` | class `IDLE` | `IOPOL_THROTTLE` |
| `"low"` | class `BE`, level 7 | `IOPOL_UTILITY` |
| `"normal"` | no call: the jail keeps whatever class its launcher's process tree holds, `NONE` on a stock host | no call |

- **Default:** unset means `"normal"`. [OQ-IO3](#OQ-IO3) asks whether to flip it.
- **Nesting.** A nested jail that declares nothing, or `"normal"`, inherits the outer jail's
  class: the nested podman, its runtime and the inner entrypoint all descend from outer-jail
  threads that hold it, and nothing in that chain resets it. A nested jail that declares a value
  sets its own.
- **Validation** (host and in-jail alike, like every `resources` key):
  - `io` is a string or an object. Anything else is an error.
  - The object admits `priority` only; an unknown key is an error, as `resources` reports one
    today. A `weight` key exists only if [OQ-IO7](#OQ-IO7) ships one.
  - `priority` is exactly one of the three lowercase strings. `""`, `"Low"`, a number or any
    other string is an error naming the three.
  - `null`, and an empty object, mean unset, as `null` already does for every `resources` key.
- **Scope:** both user and workspace config may declare it. It grants nothing, so the workspace
  refusals do not apply. An inner launcher inherits it with the rest of `resources`
  ([`inherit.go`](../../internal/config/inherit.go)).
- **State that exists:** no config written before the key existed carries it, because it was an
  unknown-key error until then, so nothing migrates.

## 5. Behavior, layer by layer

### 5.1 The entrypoint: one pinned thread, then a re-exec

- **Mechanism** ([IO-D1](#11-decision-ledger)). The entrypoint pins its goroutine to its OS
  thread, sets the class on that thread alone, and re-executes itself with the same argv and
  environment plus a marker. The new image starts with one thread, the one holding the class, and
  the kernel copies a set class into every thread and child a holder creates (`blk-ioc.c`), so
  every thread and child of the re-executed image holds it by construction.
- **Coverage.** Everything `Main` starts inherits the class: `ldconfig` (the first child, in
  `generateLdCache`), the `node --version` probes of launcher generation when a pack declares a
  Node floor, `mise uninstall` of retired tools, `iptables`, the `socat` port forwarders, the
  `yolo-jaild` supervisor and its daemons, the shell, and all their descendants
  ([OQ-IO1](#11-decision-ledger)). Work a host process does for the jail is not covered: a
  `nix build` through the host daemon, and the host loophole daemons, keep the host's priority
  ([Non-Goal 6](#2-non-goals)).
- **Trigger.** Once per entrypoint run, at the top of `Main`, before the boot log is attached
  ([`boot.go`](../../internal/entrypoint/boot.go)). Nothing before that point starts a process.
  Every attach runs the entrypoint again (`podman exec <container>
  /opt/yolo-jail/bin/yolo-entrypoint`, [`run.go`](../../internal/cli/run/run.go)), so an attach
  covers its own shell the same way.
  - ⚠ **Before the boot log, never after.** Attaching the log rotates `boot.log` into the
    previous-boot slot ([`bootlog.go`](../../internal/entrypoint/bootlog.go)), so a re-exec after
    it would rotate twice and lose the previous boot's log.
- **Value.** The launcher's decision reaches the entrypoint through the container environment
  ([§5.2](#52-the-launcher-one-decision-per-backend-and-the-disk-it-lands-on)); the entrypoint
  never reads the config for it. So an attach applies the value the jail was launched with, the
  way an attach reuses the running jail's other state instead of re-staging it. A config edit takes
  effect at the next fresh launch.
- **Report.** The outcome is reported right after the boot log is attached, so it lands in
  `<workspace>/.yolo/boot.log`: a success as a log-only note, a failure as one warning on the boot
  stream naming `resources.io.priority`. The re-executed image reads its own thread's priority
  back, so the note names the value it read, and a value other than the declared one is the
  warning ([IO-D9](#11-decision-ledger)).
- **Forbidden.**
  - Never change the priority of any thread or process but the entrypoint's own pinned thread.
  - Never re-execute more than once per run. The re-executed image finds the marker, removes it
    from its environment so no child sees it, and does not re-execute.
- **Failure.** Never fatal.
  - The set fails (an LSM denial, or the kernel reports the call unknown): no re-exec. Only one
    thread was touched and it holds nothing, so nothing needs resetting, and the boot warns.
  - The re-exec fails: the pinned thread is reset to unset, the boot continues in the first image
    with every thread unset, and the warning says so. If the reset fails too, the warning says
    that as well, because that thread's later children would inherit the class.
  - An unrecognized value (only a launcher/entrypoint skew could produce one) is warned about
    and not applied.
  - The briefing was written from the launcher's decision, so on these paths it overstates. I
    accept that: none of them has been observed, and the boot line tells the human.

### 5.2 The launcher: one decision per backend, and the disk it lands on

The launcher decides once per launch whether to pass the priority, from the backend and the host
OS. Where it passes one, it also grades the parent disks of the workspace with the resolver
`yolo check` uses ([IO-D5](#11-decision-ledger)). A declared priority the disk ignores is a
declaration doing nothing, and
[P1](declaration-parity.md#1-the-principle-and-what-it-does-not-say) says *"the one state that is
never legal is accepting a declaration and doing nothing"*.
[OQ-DP5](declaration-parity.md#OQ-DP5) (a) gives the shape: one disclosure line that can never
refuse a launch. The argv and the briefing read the backend decision, as they read
`appliedResourceLimits` ([`backend-parity.md` §6](backend-parity.md#6-the-second-shared-fix-compose-the-briefing-from-what-was-applied)).

| Backend | Priority | Disposition | What the launch says |
| :--- | :--- | :--- | :--- |
| podman on a Linux host | passed to the entrypoint | **Honored** where the workspace's disk honors the value ([§3.3](#33-what-that-means-on-real-hosts)); **Warned** where it has no effect | one line when the value has no effect on a parent disk of the workspace, naming the key, each such disk and its scheduler; nothing otherwise |
| podman nested in a jail | passed | the same, graded from inside the outer jail, whose `/sys/block` and mount table are readable (measured) | the same line |
| podman on a macOS host | not passed | **Warned** | one line: the jail runs in a VM, and its workspace reaches the Mac over VirtioFS, which carries no I/O priority |
| Apple Container | not passed | **Warned** | the same line |
| macos-user | not applied until [§5.5](#55-macos-user-the-second-step) ships | **Warned** | the existing line, *"resources are NOT enforced on macos-user … are read and ignored"*, which already names every declared `resources` key, `io` included ([IO-D8](#11-decision-ledger)) |
| the host notch | — | [OQ-IO6](#OQ-IO6) | — |

Every line in the table names the priority only when a priority other than `"normal"` is declared.
`"normal"`, `null` and `{}` declare nothing to deliver, and an unset key is not a declaration.

- **"No effect" means [§5.4](#54-yolo-check-the-disk-under-the-workspace)'s `[WARN]` cells**:
  `"low"` on mq-deadline, and either value on Kyber or `none`. One grading function serves the
  launch and the check, so the two cannot disagree. A value that acts on reads only, as on BFQ
  under LUKS, has an effect and gets no launch line.
- **An unresolvable disk prints nothing at launch.** A workspace on ZFS, NFS, tmpfs or an overlay
  has no scheduler to read, neither has a bio-based disk such as zram, and an unreadable sysfs
  proves nothing. yolo cannot tell whether the
  value acts there, and `yolo check` says which case it is.
- **An attach repeats the line**, graded against the value the jail was launched with, since that
  is the value its shell receives. A notice only the fresh launch prints is one a user of a
  long-lived jail may never see, which is why `warnIfNoPacks` runs on the attach path too
  ([`run.go`](../../internal/cli/run/run.go)). On podman on Linux the attach reads that value
  from the container's frozen environment, which `inspect` already returns for the attach's
  contract gate. On the two VM backends no value is ever passed, so the attach prints the Warned
  line for the current declaration, which is true of whatever the jail was launched with
  ([IO-D10](#11-decision-ledger)).
- **Warned is final for the workspace on the macOS VM backends.** Workspace I/O crosses VirtioFS,
  and the FUSE protocol it speaks has no priority field
  ([`include/uapi/linux/fuse.h`](https://github.com/torvalds/linux/blob/master/include/uapi/linux/fuse.h)),
  so no priority set inside the VM reaches the Mac's disk for build output. The VM's own disk is
  virtio-blk, whose request header does carry the priority Linux fills (`virtio_blk.c`), and Apple
  does not document honoring it. That covers the VM's root and scratch space, never the workspace.

The **briefing** states the class wherever it was passed, beside the other applied limits:
[`backend-parity.md` §6](backend-parity.md#6-the-second-shared-fix-compose-the-briefing-from-what-was-applied)'s
ruling is *report what is emitted*, and
[DP-D16](declaration-parity.md#7-ruled-divergent-and-the-ones-i-would-re-open) is one instance of
it. Where it was not passed, the briefing says nothing about it.

- **An attach states the launched value.** An attach re-renders the briefing, and the class it
  states is the one in the jail's frozen environment, never the current config's. On podman on
  Linux that is the value the attach's line grades ([IO-D10](#11-decision-ledger)); the VM
  backends never pass one, so their briefing states none. A config edit reaches the jail's
  processes only at its next fresh launch
  ([§5.1](#51-the-entrypoint-one-pinned-thread-then-a-re-exec)), so reading the config would name
  a class no process holds, or leave out one they all hold ([IO-D12](#11-decision-ledger)).
- **It claims neither enforcement nor effect.** It never calls the class kernel-enforced or
  effective, because the agent can raise it ([§3.1](#31-process-priority)) and the disk may ignore
  it.
- **It names what ignores the value from the one grading.** The schedulers it names come from the
  grading the launch line and `yolo check` use, so `"low"` names mq-deadline and `"idle"` does not.
- **It is scoped to the jail.** It says that work a host process does for the jail, a `nix build`
  through the host daemon, keeps the host's priority ([Non-Goal 6](#2-non-goals)).

The rest of the wording is the implementer's.

### 5.3 The cgroup half, if one ships

[OQ-IO7](#OQ-IO7) decides whether v1 has a cgroup half at all. Should one ship, existing rulings
already decide how it behaves ([OQ-IO2](#11-decision-ledger), [IO-D6](#11-decision-ledger)):

1. **Probe.** Read `host.cgroupControllers` from the `podman info` the launch already runs
   ([`hostloopback.go`](../../internal/cli/run/hostloopback.go), whose `podmanInfo` does not
   model the field yet). That is podman's own answer for the process that will create the
   container.
   - ⚠ **Not `/sys/fs/cgroup/user.slice/user-<uid>.slice/cgroup.controllers`.** That node lists
     what the *system* manager enabled for the user slice. It can include `io` while
     `user@<uid>.service` does not, and that false positive fails the launch
     ([§3.2](#32-the-cgroup-io-controller)).
   - The launch asks podman only for podman on Linux, not nested. So a nested launch and podman
     on macOS have no answer and never emit the flag.
2. **Emit only on a positive answer**: `io` present in that list. Any other answer (absent, no
   answer, podman not asked) drops the flag.
3. **A dropped flag degrades and says so**, when it carries a key the user declared. One launch
   line names the key, says it was not applied, and gives the host change. The launch never
   refuses, and the briefing omits the setting. Option (b) of [OQ-IO7](#OQ-IO7) adds no key: the
   process half already honors the declared priority, so a host without `io` loses only the
   writeback arm, which `yolo check` reports rather than every launch.
4. **The host change the line names** is the `user@.service` drop-in, never a
   `podman.service` override. That unit is podman's REST API service, which a `podman run` from a
   shell never goes through, and no user unit can be handed a controller its user manager lacks.

   ```console
   $ sudo mkdir -p /etc/systemd/system/user@.service.d
   $ printf '[Service]\nDelegate=cpu cpuset io memory pids\n' | sudo tee /etc/systemd/system/user@.service.d/delegate.conf
   $ sudo systemctl daemon-reload
   ```

   Then the user logs out and back in. The line also says what delegation does not change:
   which disks honor the setting ([§3.3](#33-what-that-means-on-real-hosts)).

### 5.4 `yolo check`: the disk under the workspace

- **A row only when a priority is declared** other than `"normal"`. An undeclared key prints
  nothing ([OQ-DP5](declaration-parity.md#OQ-DP5)).
- **Resolve the parent disks** of the workspace's mount and, at the host, of podman's storage
  root: follow the mount's source through device-mapper slaves and partitions, then read each
  disk's active scheduler. One row per parent disk, so an LVM volume spanning two disks gets two.
  - ⚠ **Find the mount by the longest mount-point prefix of the path, then follow its source
    name. Never match a device number.** On btrfs the mount table and `stat` disagree: here
    `/proc/self/mountinfo` gives `0:28` for `/workspace`, `/home/agent` and `/nix/store`, while
    `st_dev` is `0:61` for the first two and `0:62` for `/nix/store` (measured), because each
    subvolume gets an anonymous device of its own. And `/dev/mapper/root` does not exist inside a
    jail, while `/sys/block/dm-0/dm/name` reads `root`.
- **Grade:**

  | Parent disk's scheduler | `"idle"` | `"low"` |
  | :--- | :--- | :--- |
  | `bfq` | `[PASS]` | `[PASS]` |
  | `mq-deadline` | `[PASS]` | `[WARN]` |
  | `kyber`, `none` | `[WARN]` | `[WARN]` |

  When the path crosses dm-crypt onto a BFQ disk, the `[PASS]` row adds that writes lose the
  priority there ([§3.1](#31-process-priority)).
- **A `[WARN]` names** the disk, its scheduler, the path it backs and the declared value, and
  gives the root-only host change (switch that disk's scheduler, persisted with a udev rule). It
  never changes the exit code: `Check()` returns 1 on failures alone
  ([`reporter.go`](../../internal/cli/check/reporter.go)).
- **Where it runs.** The row depends on what the reader finds under the path:

  | What the reader finds | Row |
  | :--- | :--- |
  | a block-backed mount with readable sysfs: a Linux host, or a podman jail (measured readable) | graded by the table above |
  | a `virtiofs` mount (an Apple Container or podman-machine jail), or a macOS host with a VM backend | `[WARN]`: the priority is Warned there, because workspace I/O reaches the Mac over VirtioFS, which carries none ([§5.2](#52-the-launcher-one-decision-per-backend-and-the-disk-it-lands-on)) |
  | a macOS host with macos-user | `[WARN]` for the Warned disposition until [§5.5](#55-macos-user-the-second-step) ships |
  | a mount with no block device behind it (a ZFS dataset, NFS, tmpfs, an overlay) | `[SKIP]` naming the filesystem type: there is no scheduler to grade |
  | a parent disk with no `queue/scheduler` file (a bio-based device such as zram), or a scheduler name outside the grading table | `[SKIP]` naming the disk and the missing file or the scheduler; the launch prints nothing for it ([IO-D11](#11-decision-ledger)) |
  | a block-backed mount whose sysfs entries cannot be read, in a podman jail | `hostFact`'s `[SKIP]`, saying to run `cat /sys/block/<disk>/queue/scheduler` on the host |
  | the same at a host | `[SKIP]` naming the path that could not be read |

  `hostFact` appends *"a host fact, not visible from inside a jail"*, which is true of one reader
  only: a podman jail reading a Linux host's disk. Delegation of a cgroup half, should one ship,
  is always a host-fact `[SKIP]` in-jail.

Example output:

```text
[WARN] resources.io.priority "low" does nothing on nvme0n1 (scheduler kyber), the disk under /workspace
       kyber and none ignore I/O priority; bfq honors "low" and "idle", mq-deadline only "idle".
       Host change (root): switch nvme0n1's scheduler to bfq, and persist it with a udev rule.
```

### 5.5 macos-user, the second step

- **Mechanism.** `setiopolicy_np(IOPOL_TYPE_DISK, IOPOL_SCOPE_PROCESS, <policy>)`: `"low"` becomes
  `IOPOL_UTILITY` and `"idle"` becomes `IOPOL_THROTTLE` ([`getiopolicy_np(3)`](https://keith.github.io/xcode-man-pages/getiopolicy_np.3.html):
  UTILITY I/O is *"throttled to prevent a significant impact on the latency of IMPORTANT and
  STANDARD I/Os"*; THROTTLE is *"for long-running I/O intensive background work"*). The
  macos-user launcher applies it. That backend runs no `yolo-entrypoint` binary: its boot is
  `yolo internal darwin-bootstrap`, which runs the entrypoint package's generators and not `Main`
  ([`darwin.go`](../../internal/entrypoint/darwin.go)).
- **What is unmeasured.** The agent is launched as `sudo -u <sandbox account> /usr/bin/env -i …
  /usr/bin/sandbox-exec -f <profile> -- <agent>` (`LaunchArgv`,
  [`macosuser.go`](../../internal/macosuser/macosuser.go)). The man page says only that *"the I/O
  policy of a newly created process is inherited from its parent process"*. Whether it survives a
  setuid `sudo` and `sandbox-exec` needs a Mac, and that measurement is this step. It is written
  (2026-10-01) as an experiment the scheduled `macos-user.yml` job runs,
  `TestMacosUserIOPolicyAcrossTheLaunchArgv`
  ([`macosuseriopolicy_test.go`](../../integration/macosuseriopolicy_test.go)), and has not run
  yet; [the plan's step 5](io-priority-plan.md#step-5-macos-user) says how it reads the policy.
- **Until then it is Warned** by the existing line, which already names `io`. Once it ships, the
  cell becomes HonoredBy `setiopolicy_np`, and that line stops naming the priority.
- [DP-D1](declaration-parity.md#7-ruled-divergent-and-the-ones-i-would-re-open) does not rule this
  out. It rejected `RLIMIT_AS` and `RLIMIT_NPROC` as stand-ins for `memory` and `pids_limit`, not a
  disk policy.

## 6. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| Set the priority at `execBash`, or for the shell alone | **Rejected.** Everything `Main` started before it keeps class `NONE`. (Not for raciness: pinned with `LockOSThread`, a set-then-exec is deterministic, 30 of 30 runs against 3 of 30 unlocked, measured.) |
| A process-group call: `setpgid(0, 0)`, then `IOPRIO_WHO_PGRP` | **Rejected for [IO-D1](#11-decision-ledger)'s re-exec.** It reaches every thread alive at the call (all 10 threads and 16 of 16 children, measured), but it reaches every other process in the group too. So the entrypoint must lead a group of its own, must never leave its terminal's foreground group (an interactive bash in a background group stops itself with `SIGTTIN`), and where it cannot lead one, as an attach's `podman exec` process might, it could only apply nothing and warn. The re-exec needs none of that. |
| Walk `/proc/self/task`, setting each thread | **Rejected.** It races thread creation. |
| `runtime.LockOSThread` and a set, with no re-exec | **Rejected.** It covers one thread: 0 of 16 children started from other goroutines inherited it (measured). |
| CPU `nice 19` in the entrypoint (BFQ derives `BE7` from nice for an unset class) | **Rejected.** It also lowers CPU priority, which `cpus` and the host already govern. mq-deadline treats an unset class as `BE` whatever the nice value, and once a class is set, nice no longer moves I/O at all. |
| `SCHED_IDLE` (BFQ derives `IDLE`) | **Rejected** for the same reasons, and it starves the CPU side outright. |
| OCI `process.ioPriority`, set by the runtime before the container's first process | **Unavailable.** It would cover every thread by construction, but podman exposes no flag for it. |
| `--cgroup-conf io.weight=<n>` | **Rejected.** It acts only where root enabled iocost, which is off by default. |
| `--cgroup-conf io.bfq.weight=<n>` | **Subsumed.** `--blkio-weight` writes it, and falls back to `io.weight`. |
| `io.bfq.weight` or `io.prio.class` as v1's cgroup half | [OQ-IO7](#OQ-IO7)'s options (a) and (b). |
| Reach `io` through the existing cgroup delegate (`yolo-cglimit`, `internal/cgd`) | **Impossible without host delegation.** The delegate writes inside the container's own cgroup and enables only what the parent offers, and it enables `cpu`, `memory` and `pids` alone ([`ops.go`](../../internal/cgd/ops.go)). |
| A per-command flag on `yolo-cglimit` | [OQ-IO4](#OQ-IO4). |

## 7. Risks

| Risk | Mitigation |
| :--- | :--- |
| `"idle"` makes a build crawl under sustained desktop I/O: under contention BFQ gives the idle class about one request per 200 ms | Document `"low"` as the recommendation. Both schedulers bound `IDLE`'s starvation, BFQ at about 200 ms and mq-deadline by aging a request after 10 s. |
| The agent raises its own priority back | Not mitigated. It is advisory ([Non-Goal 3](#2-non-goals)); only a cgroup half is enforceable. |
| Users expect it to help on NVMe | The launch and `yolo check` say by name when the disk ignores it. |
| The briefing states a class the entrypoint failed to apply | The boot stream names the failure ([§5.1](#51-the-entrypoint-one-pinned-thread-then-a-re-exec)). |
| The re-exec costs one more start of the entrypoint on every boot and attach | Paid only when a priority other than `"normal"` is declared. |
| A cgroup-half flag emitted on a host without `io` fails the launch | Emit only on podman's own positive answer ([§5.3](#53-the-cgroup-half-if-one-ships)). |

## 8. Completeness

- **Degenerate inputs.** [§4](#4-the-configuration-surface) says what each shape means. An empty
  object and `null` are unset.
- **Failure paths.**
  - The entrypoint's set fails: no re-exec, warn, boot. The re-exec fails: reset the one thread,
    warn, boot ([§5.1](#51-the-entrypoint-one-pinned-thread-then-a-re-exec)).
  - A backend cannot act: not passed, Warned
    ([§5.2](#52-the-launcher-one-decision-per-backend-and-the-disk-it-lands-on)).
  - The disk ignores the value: passed, and the launch discloses it (the same section).
  - The disk cannot be resolved: nothing at launch, and `[SKIP]` in the check
    ([§5.4](#54-yolo-check-the-disk-under-the-workspace)).
- **Concurrency.** Each entrypoint run touches only its own pinned thread before re-executing. A
  concurrent attach runs its own entrypoint, so two runs never touch each other's processes.
- **Defaults.** `"normal"`: no call is made, and the launcher's class stands. Levels are the
  kernel's own numbers and have no unit.
- **Trigger.** The top of `Main`, before the boot log is attached, on every fresh launch and every
  attach. The launch's disk grading runs once per launch invocation, fresh or attach.
- **State that exists.** A running jail keeps its class until it is relaunched. An attach into
  a jail launched before this ships applies nothing, because its environment and its entrypoint
  predate it.
- **One writer per piece of state.**
  - The launcher decides.
  - The entrypoint is the only yolo writer of the jail's thread priorities.
  - The macos-user launcher is the only writer on that backend.
  - The agent can rewrite its own threads, and that is allowed.
- **Forbidden.**
  - Never refuse a launch over the priority or its absence.
  - Never run `sudo` or change host systemd.
  - Never touch a thread or process other than the entrypoint's own pinned thread.
  - Never re-execute more than once per run, and never after the boot log is attached.
  - Never emit a cgroup io flag without podman's positive answer.
- **Done looks like:**
  1. In a podman jail with `"low"`, every thread of every process except `podman-init` (PID 1),
     daemons and an attached shell included, reads `be/7`. The launcher starts nothing in the
     container except through the entrypoint (its one `podman exec` is the attach). Check it per
     thread under `/proc/<pid>/task` with `ioprio_get`, not `ionice -p`: that reads one thread,
     and it is not in the image.
  2. Unset or `"normal"`, under a launcher with no class set: every thread reads `none/0`, as today.
  3. On a Kyber disk with `"low"`, the fresh launch and every attach print the disclosure line, and
     `yolo check` prints the `[WARN]`. On a BFQ disk neither says anything about it.
  4. On Apple Container and podman under macOS, the launch and every attach print the Warned line
     naming VirtioFS, and the briefing does not mention the class.
  5. On a BFQ disk, under an `fio` random-read load from a jail at `"idle"`, a desktop process's
     read latency stays near its unloaded value. Nothing like it is expected on Kyber or `none`.

## 9. What I would build, in order

1. **The key**: validation, `knownResourcesKeys`, `config-ref`, the inheritance census reason, and
   the per-setup reference.
2. **The entrypoint**: the pinned set and the re-exec at the top of `Main`, with its reset and its
   report after the boot log is attached.
3. **The launcher**: the per-backend decision, the environment value, the disk disclosure line on
   the resolver step 4 shares, the Warned line, the briefing line, and leaving a `"normal"` `io`
   out of macos-user's resources line.
4. **`yolo check`**: the scheduler row, on the same resolver and grading.
5. **macos-user**, after a Mac shows the policy survives `sudo` and `sandbox-exec`.
6. **A cgroup half**, only if [OQ-IO7](#OQ-IO7) ships one.

Steps 1 to 4 waited on no open question and are built. [OQ-IO3](#OQ-IO3) changes only what an
unset key means.

## 10. Open Questions

Three filed questions are settled in the [ledger](#11-decision-ledger) and are not asked here:
[OQ-IO1](#11-decision-ledger) (scope) is decided as an implementation choice, and
[OQ-IO2](#11-decision-ledger) (missing delegation) and [OQ-IO5](#11-decision-ledger) (the check's
severity) are answered from existing rulings.

1. 💬 <a id="OQ-IO3"></a>**[OQ-IO3](#OQ-IO3): Does an unset `resources.io.priority` mean `"low"` for
   every jail?** This decides whether every jail's builds slow down under contention without the
   user asking. yolo already applies one uniform resource default silently, `--pids-limit 32768`,
   and keeps it out of the briefing ([`backendcaps.go`](../../internal/cli/run/backendcaps.go)).
   A default `"low"` would do nothing on mq-deadline, Kyber or `none` disks, which is to say it
   helps BFQ hosts only. It would print no disclosure line there either, because nothing was
   declared ([§5.2](#52-the-launcher-one-decision-per-backend-and-the-disk-it-lands-on)).

   <!-- vantage: question id=OQ-IO3 leaning="Keep it opt-in, default normal. A default low acts only on BFQ disks, slows every build that meets contention without anyone choosing it, and cuts against the house posture that nothing is active by default. Revisit after measuring real workloads." -->

   _Leaning:_ Keep it opt-in. The cost is that a user who never finds the key gets no protection,
   as the incident's user did not. The cost of flipping it is a slowdown nobody chose, on the one
   scheduler where it acts at all.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 <a id="OQ-IO4"></a>**[OQ-IO4](#OQ-IO4): Does `yolo-cglimit` gain an `--io-priority` flag for
   one command?** This decides whether the cgroup-delegate client grows a job that talks to no
   delegate. The facts that bear on it:
   - Setting a priority needs no delegate ([§3.1](#31-process-priority)).
   - `ionice` is absent from the image, but `"packages": ["util-linux"]` provides it.
   - `nice -n 19` moves I/O only on BFQ, and only for a thread with no class set, so under a
     declared priority it moves nothing.

   <!-- vantage: question id=OQ-IO4 leaning="No. Keep yolo-cglimit the cgroup-delegate client; the per-command lever already exists as util-linux's ionice through packages. Revisit if agents are seen needing it." -->

   _Leaning:_ No. The cost is that an agent wanting to demote one command needs `util-linux` in
   `packages` first. The cost of yes is a second job for a binary whose whole description is the
   delegate's client.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 <a id="OQ-IO6"></a>**[OQ-IO6](#OQ-IO6): Does `yolo host -- <cmd>` set the priority?** The
   filing said it would. No ruling decides it, and two things weigh against it. The one sketch of
   the host notch's `check` output,
   [`yolo-as-environment-manager.md` §3.4](yolo-as-environment-manager.md#34-check-becomes-is-this-description-satisfiable-here),
   lists `resources` as *"nothing to confine"*. And
   [P4](declaration-parity.md#1-the-principle-and-what-it-does-not-say) has `yolo host` read the
   user-scope config only, so a workspace's `resources.io` could never reach it.

   <!-- vantage: question id=OQ-IO6 leaning="No, out of scope. The host notch confines nothing, only a user-scope key could reach it, and a user who wants a host command demoted can run ionice on the host." -->

   _Leaning:_ No. The cost is that `yolo host -- cargo build` gets nothing from yolo, and the
   user reaches for `ionice` on the host. The cost of yes is amending that sketch's host-notch
   line, for a key that acts at the host only from user scope and at the jail from either scope.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 <a id="OQ-IO7"></a>**[OQ-IO7](#OQ-IO7): Does v1 ship a cgroup half, and which one?** This
   decides whether anything yolo ships governs a build's writeback. Every option needs `io`
   delegated, a one-time `sudo` change on the host, and none has been measured on a delegated host.
   What each option reaches, and on which scheduler: [background](#background-to-oq-io7).

   - **(a) a `resources.io.weight` key**, as `--blkio-weight`.
   - **(b) `"idle"` also written as the cgroup's `io.prio.class`**.
   - **(c) neither in v1.**

   <!-- vantage: question id=OQ-IO7 leaning="(c) neither in v1: ship the priority alone. (a) does nothing against desktop apps in the one-jail case, and (b) helps only mq-deadline disks on hosts that delegated io; revisit (b) first, after one measurement on a delegated mq-deadline host." -->

   _Leaning:_ (c). The cost is that v1 has no lever over writeback, though a stock host could not
   use one anyway. (b) is the one to revisit first, after a measurement on a delegated mq-deadline
   host.

   **Answer:**
   > _(empty — fill in when decided)_

### 10.1 Background to the open questions

#### Background to [OQ-IO7](#OQ-IO7)

An encrypted disk changes none of the options, because the cgroup association survives dm-crypt's
writes ([§3.2](#32-the-cgroup-io-controller)):
- **(a) a `resources.io.weight` key**, as `--blkio-weight`: it acts on BFQ, or through
  iocost. It is relative to siblings under the jail's parent slice, so it does nothing against
  desktop apps while this jail is the only container running ([§3.2](#32-the-cgroup-io-controller)).
- **(b) `"idle"` also written as the cgroup's `io.prio.class`**: it reaches writeback and the
  agent cannot undo it, but only on mq-deadline, and only for `"idle"`. `restrict-to-be` would
  *raise* an unset class to `BE0`, so `"low"` has no cgroup equivalent. It adds no key, and
  because the process half still honors the declaration, a host without `io` needs no launch
  line.
- **(c) neither in v1.**

## 11. Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-IO1 | *Implementation decision.* The whole jail or only the shell? The priority covers the whole jail: every thread and child of `yolo-entrypoint` holds it, so `ldconfig`, `mise`, `iptables`, the `socat` forwarders, the jail-daemon supervisor and its daemons, the shell and all descendants inherit it, and each attach does the same for its own entrypoint. With every thread covered no trade-off remains: daemon I/O is small and the setting is invisible to users. Its limits are the lever's, not the scope's: the agent can raise itself back, and writeback is not reached | 2026-09-27 | [§5.1](#51-the-entrypoint-one-pinned-thread-then-a-re-exec) | ✅ |
| OQ-IO2 | *Answered by existing rulings, not ruled.* Refuse or warn when `io` is not delegated? A declared cgroup setting the host does not delegate is dropped, and the launch goes ahead with one line naming the key and the host change; the briefing omits it. [OQ-R3](../reference/loopback-tls-reachability.md#oq-r3): *"a host yolo cannot fix degrades and launches"*. [OQ-DP5](declaration-parity.md#OQ-DP5) (a): a coded decline with one disclosure line that can never refuse a launch. [P1](declaration-parity.md#1-the-principle-and-what-it-does-not-say): never silence. The GPU precedent in [`assemble.go`](../../internal/cli/run/assemble.go) prints *"starting without GPU passthrough"*. [`backend-parity.md` §6](backend-parity.md#6-the-second-shared-fix-compose-the-briefing-from-what-was-applied): the briefing reports what was emitted. [OQ-R4](../reference/loopback-tls-reachability.md#oq-r4)'s refusal covers an enabled jail-facing service and does not apply | 2026-09-27 | [§5.3](#53-the-cgroup-half-if-one-ships) | — |
| OQ-IO5 | *Answered by existing rulings, not ruled.* `[INFO]` or `[WARN]` for a disk that ignores the priority? `yolo check` has four levels, and [`reporter.go`](../../internal/cli/check/reporter.go) rules out another: *"a fifth level would be a vocabulary nobody could keep straight"*. `[WARN]` is *"the badge that means act on this"*, and a declared priority the disk ignores is a declaration doing nothing ([P1](declaration-parity.md#1-the-principle-and-what-it-does-not-say)). No row for an undeclared key ([OQ-DP5](declaration-parity.md#OQ-DP5)). A host fact a podman jail cannot see is `hostFact`'s `[SKIP]`. A `[WARN]` never changes the exit code | 2026-09-27 | [§5.4](#54-yolo-check-the-disk-under-the-workspace) | ✅ |
| IO-D1 | *Implementation decision.* [OQ-IO1](#11-decision-ledger)'s mechanism: the entrypoint pins its goroutine with `runtime.LockOSThread`, sets the class on that thread with `IOPRIO_WHO_PROCESS`, and re-executes itself with the same argv and environment plus a marker, which the new image removes and which stops a second re-exec. The new image starts with that one thread, and every later thread and child copies its class. Measured with Go 1.26 on a busy process: after the re-exec, every thread and 16 of 16 children started from other goroutines read `BE7`, in 5 of 5 runs. Chosen over a process-group call (`setpgid`, then `IOPRIO_WHO_PGRP`), which reaches other processes in a shared group and so needs a group of its own, a foreground-group guard, and an apply-nothing arm on an attach that cannot lead one; over walking `/proc/self/task`, which races thread creation; and over `LockOSThread` without the re-exec, which covers one thread | 2026-09-27 | [§5.1](#51-the-entrypoint-one-pinned-thread-then-a-re-exec) | ✅ |
| IO-D2 | *Implementation decision.* The launcher decides per backend and passes the value in the container environment; the entrypoint never reads the config for it. The same decision feeds the briefing, as `appliedResourceLimits` does. Apple Container and podman on a macOS host get no value and a Warned line. Where it passes a value, the launch grades the workspace's parent disks with [IO-D5](#11-decision-ledger)'s resolver and grading and prints one line naming the key, each disk and its scheduler wherever the value has no effect ([P1](declaration-parity.md#1-the-principle-and-what-it-does-not-say); [OQ-DP5](declaration-parity.md#OQ-DP5) (a)); an unresolvable disk prints nothing. An attach applies the value the jail was launched with and repeats the line for it | 2026-09-27 | [§5.2](#52-the-launcher-one-decision-per-backend-and-the-disk-it-lands-on) | ✅ |
| IO-D3 | *Implementation decision.* `"io": "<value>"` is shorthand for `{"priority": "<value>"}` alone. A shorthand that implied a weight would print a line on every undelegated host for a value the user never wrote, where [OQ-DP5](declaration-parity.md#OQ-DP5) wants no disclosure for what was not declared. `"normal"` makes no call, leaving whatever class the launcher's process tree holds | 2026-09-27 | [§4](#4-the-configuration-surface) | ✅ |
| IO-D4 | *Implementation decision.* A failed set skips the re-exec; a failed re-exec resets the pinned thread to unset. Either way the boot continues with every thread unset and prints one boot-stream warning, never fatal. Only one thread is ever touched, so no partial state can outlive a failure except a failed reset, which the warning names | 2026-09-27 | [§5.1](#51-the-entrypoint-one-pinned-thread-then-a-re-exec) | ✅ |
| IO-D5 | *Implementation decision.* One resolver and one grading serve the launch and the check. The resolver finds a path's mount by the longest mount-point prefix, never by device number, and follows its source name through device-mapper slaves and partitions to its parent disks; the grading is [§5.4](#54-yolo-check-the-disk-under-the-workspace)'s table. The check resolves the workspace and, at the host, podman's storage root; the launch resolves the workspace | 2026-09-27 | [§5.4](#54-yolo-check-the-disk-under-the-workspace) | ✅ |
| IO-D6 | *Implementation decision.* A cgroup half, should [OQ-IO7](#OQ-IO7) ship one, is gated on `io` in `podman info`'s `host.cgroupControllers`, from the launch's existing call. It is never gated on a slice path, which can report `io` the user manager lacks. It is emitted as `--blkio-weight` for a weight or `--cgroup-conf io.prio.class=idle` for a class, never as the nonexistent `--io-weight` | 2026-09-27 | [§5.3](#53-the-cgroup-half-if-one-ships) | — |
| IO-D7 | *Implementation decision.* On macos-user, `"low"` is `IOPOL_UTILITY` and `"idle"` is `IOPOL_THROTTLE`, applied by the macos-user launcher. It ships only after a Mac shows the policy survives `sudo` and `sandbox-exec`. Until then the existing resources line keeps it Warned | 2026-09-27 | [§5.5](#55-macos-user-the-second-step) | — |
| IO-D8 | *Implementation decision.* macos-user's existing resources line names every present `resources` key whatever its value ([`orchestrator.go`](../../internal/macosuser/orchestrator.go)). It leaves out an `io` that resolves to `"normal"` (that string, `null` or `{}`), which makes no call on every backend and so is honored there already. Any other value is named until [§5.5](#55-macos-user-the-second-step) ships | 2026-09-27 | [§5.2](#52-the-launcher-one-decision-per-backend-and-the-disk-it-lands-on) | ✅ |
| IO-D9 | *Implementation decision.* The re-executed image reads its own thread's priority back with `ioprio_get`. The boot.log note names the value it read; a value other than the declared one is the boot-stream warning; a failed read is a note that says so, since the set before the exec succeeded | 2026-09-27 | [§5.1](#51-the-entrypoint-one-pinned-thread-then-a-re-exec) | ✅ |
| IO-D10 | *Implementation decision.* An attach on podman on Linux grades the value in the container's frozen environment, read from the `inspect` the attach already runs, and a jail whose environment has none applies nothing and prints nothing. On Apple Container and podman on macOS, where no value is ever passed, the attach prints the Warned line for the current declaration | 2026-09-27 | [§5.2](#52-the-launcher-one-decision-per-backend-and-the-disk-it-lands-on) | ✅ |
| IO-D11 | *Implementation decision.* A parent disk with no `queue/scheduler` file (a bio-based device such as zram) or a scheduler outside the grading table is ungraded: the launch prints nothing for it and `yolo check` prints a `[SKIP]` naming the disk and what is missing | 2026-09-27 | [§5.4](#54-yolo-check-the-disk-under-the-workspace) | ✅ |
| IO-D12 | *Implementation decision.* The briefing is handed the value the jail's environment carries and never derives one from the config: on a fresh launch the value the argv passes, and on an attach the one in the container's frozen environment on podman on Linux, or none on the VM backends, which never pass one. Its list of schedulers that ignore the value comes from [IO-D5](#11-decision-ledger)'s grading, and it says that work a host process does for the jail keeps the host's priority ([Non-Goal 6](#2-non-goals)) | 2026-09-27 | [§5.2](#52-the-launcher-one-decision-per-backend-and-the-disk-it-lands-on) | ✅ |
