---
title: "Let Podman finish waking before a jail gives up"
date: 2026-09-29
status: in-review
tags: [design, launch, podman, reliability]
summary: "A patient, budget-bounded Podman readiness probe shared by the launch and yolo check, whose one answer every later Podman fact on the launch path reads."
vantage:
  status-chip: true
---

# Let Podman finish waking before a jail gives up

**Status:** DESIGN, 2026-09-29, revised the same day after review. Nothing built;
evidence checked against `51620f7e`. Code is cited by symbol, not by line.

> **In short.** The first `podman info` after a boot does Podman's own
> post-boot cleanup. yolo kills that probe at 10 seconds and cancels the launch,
> though the cleanup was probably seconds from done. The fix is to stop killing
> it: wait up to one budget for Podman to answer, retry only after an early
> error, and reuse that one answer for every later Podman fact the launch needs.

**Why it matters.** At the 2026-09-29 reboot, six workspaces relaunched within
44 seconds and a seventh five minutes later. One launch was refused after a 22.5-second progress line,
before its container existed. That failure fits yolo aborting a Podman that was
about to answer (see [the mechanism](#why-a-timed-out-probe-is-not-a-failed-podman)).
Separately, a launch that passes today's probe runs a second `podman info` of its
own. If that second query fails, the jail starts with every loopback-TLS service
unreachable, and nothing is printed.

**The shape.** One readiness function in `internal/runtime`, called by the
launch and by `yolo check`. It lets an attempt run until the attempt exits or
the budget ends, retries only after an early error exit, never kills Podman, and
hands its parsed answer to every later Podman fact on the launch path.
**There is no cross-workspace lock**, because Podman already serializes its own
cleanup.

**Cost.** A genuinely broken Podman on Linux reports its final refusal only at
the end of the budget. The reason for the first failure does appear within the
progress grace period (2 s). A refused launch may leave one `podman` running,
and it names that process.

**Start at [the gate](#the-gate).** Its no-kill rule is what makes a reboot
recoverable.

**Needs your ruling:** [OQ-PR1](#OQ-PR1) (the budget),
[OQ-PR2](#OQ-PR2) (whether housekeeping stands down on a slow launch),
[OQ-PR3](#OQ-PR3) (a machine-wide record of launch outcomes).

**Reads with:** [`podman-reboot-readiness-plan.md`](podman-reboot-readiness-plan.md)
(the implementation sketch),
[`perf-logging.md`](../reference/perf-logging.md) (the host timing log),
[`loopback-tls-reachability.md`](../reference/loopback-tls-reachability.md)
(the host-loopback disposition this design protects).

---

## Verdict and boundary

Build a **readiness gate** *(a term coined here)*: the step of runtime selection
that waits, within one fixed budget, for `podman info --format json` to answer.
Its successful answer is the launch's **Podman facts** *(also coined here)*: the
one parsed `podman info` document that every later reader on the launch path
uses. It is not a lock, not a cache between processes, and not a Podman service.

**In scope:** a Linux launch whose runtime is Podman, whether selected
explicitly or by autodetection, on the host or in a nested jail. `yolo check`
runs the same gate. **Out of scope, and pinned by a test:** Apple Container,
`macos-user`, and macOS Podman Machine, which keep today's one-shot probe and
its `podman machine start` hint ([PR-D7](#PR-D7)). **Also out of scope:** the
other things a launch restored at boot depends on. Network readiness is one:
the pack refresh runs before runtime selection, and at this reboot it failed DNS
for github.com. Nix-daemon readiness is the other. Each gets its own design if
it bites.

The gate must not remove containers, reset Podman's database, start a host
service, accept a stale image, or treat a probe it could not complete as
permission to launch (the tri-state rule: "could not ask" is never a yes).

## What happened, and what is still unknown

Every observation below is labeled by how it was obtained. The full timeline and
sources are in [Appendix A](#appendix-a-evidence).

- **REPORTED, not re-checkable here.** The originating agent recorded a refused
  launch: `Cannot query container runtime`, a 22.5 s progress line, and Podman
  stderr about cleaning up old volumes and refreshing a removed container. It
  also recorded journal events near 10:49:14 local and a 0.529 s facts query at
  10:50:00. The report does not name which workspace failed. None of these lines
  is in the logs a jail here can read: this workspace's `.yolo/` and the host log
  directory mounted at `/ctx/host-yolo-logs`.
- **MEASURED, this workspace (yolo-jail).** It launched at 10:34:08 EDT
  (14:34:08Z) and probed Podman in **4.0 s**. That is the only probe in its log
  slow enough to show the progress line since the line shipped on 2026-09-28. Its second `podman info` took 0.144 s at
  14:34:21Z. The jail then ran without a break until 15:59:14Z. So the host did
  not reboot at 10:49, and "the next launch worked 16 minutes later" says
  nothing about how long Podman took to wake.
- **MEASURED, the whole machine.** Host brokers restarted at 10:34:21 EDT. In the
  loophole crossing log, seven jails reached their fronts between 14:34:24Z and
  14:39:24Z, six of them within 28 s. This is the **restore storm**
  *(coined here)*: the burst of launches a session restorer starts after login.
  On this machine that restorer is Waykeeper, the maintainer's own
  session-restore tool, which is not part of yolo. The storm ran 44 s from the
  first launch to the last storm jail being up. This was the machine's second
  measured storm: `internal/image/copylock.go` records 11 jails launched at once
  by a reboot on 2026-09-14.
- **MEASURED, yolo's own share of the load.** This workspace's launch, the
  first whose container came up, ran its housekeeping slot for **16.1 s**
  (14:34:24Z to 14:34:40Z). In that time it removed 7 images with `podman rmi` and started the remover for 4
  leftover scratch volumes. The slot holds the machine-wide housekeeping lock,
  and the image-load step of every other launch blocks on that lock
  (`lockHousekeepingFn`). At 14:34:40Z, the second the slot ended, aws-auth and
  openai-auth-broker each logged 9 unattributed connections. One launch makes
  about 3 per service, so that is about three launches released at once
  (**INFERRED**).

**The most likely reading (INFERRED, but it fits every number).** The failing
launch started about 14:33:50Z, just before this workspace. Another yolo process
was indeed already running at 14:34:08Z: this workspace's launch waited for it
to release a pack mirror. That launch was
the first Podman client after boot, so its `podman info` did Podman's post-boot refresh.
yolo killed the probe's direct process at 10 s. Podman's re-executed child
survived that kill and finished the refresh about 12.5 s later. This workspace's
probe, started at about 14:34:08Z, waited behind that refresh and got its answer
at 14:34:12Z, 4.0 s later. The killed probe's `Wait` returned at the same moment,
so yolo reported a 22.5 s timeout just as Podman became ready. The
[mechanism section](#why-a-timed-out-probe-is-not-a-failed-podman) shows why each
step happens.

**The alternative reading.** Podman ran a second refresh at 10:49 local with no
reboot. That happens if its runtime directory under `/run/user/<uid>` was
removed, for example when the user's last login session ended with linger off.
The [real-host check](#testing-and-the-real-host-check) records what settles
this: the failing workspace's `launch.log` header, `loginctl` linger, and the
launch's `XDG_RUNTIME_DIR`. Neither reading changes the design. Both are "a
`podman info` that is doing the refresh, killed at 10 s."

A host journal warning the same morning reported very low free space to
Syncthing. Nothing here ties it to the timeout, and this design encodes no disk
policy. It does bear on the cost of [OQ-PR2](#OQ-PR2).

## Why a timed-out probe is not a failed Podman

Three facts together decide the design.

**yolo's timeout does not bound the wait.** `realExec`
([`runcmd.go`](../../internal/cli/run/runcmd.go)) captures output into
`strings.Builder`s, so `os/exec` copies it through pipes. On timeout it calls
`cmd.Process.Kill()` on the direct child and then waits on `cmd.Wait()`. With no
`WaitDelay` set, `Wait` does not return until every process holding those pipes
has closed them, and that includes descendants that survived the kill.
**MEASURED:** a reproduction of that exact shape, a 1 s timeout on
`sh -c 'sleep 30 & sleep 100'`, returned after 100.0 s. The repository already
fixes this class for git: `gitWaitDelay` and `DetachGit` in
[`packsrc/store.go`](../../internal/packsrc/store.go).

**Podman serializes its own post-boot refresh.** **SOURCED** from libpod's
`makeRuntime` (`libpod/runtime.go`, containers/podman `main`): every Podman process takes the `alive.lck` lock in its engine
tmpdir. The one that finds no `alive` file runs `refresh()`, and every other
process blocks on that lock until the refresh is done. `refresh()` writes the
`alive` file only **after** it has refreshed containers, pods and volumes. This
host runs Podman 6.1.2 (`podman.facts` in `host-perf.log`); the host check
confirms that its code has this shape.

**A kill affects rootless and rootful Podman differently.** **SOURCED** from
`pkg/rootless`:

| Podman | What the 10 s kill hits | Result |
| :--- | :--- | :--- |
| Rootless, first command after boot (no pause process yet) | The outer process. It dropped `alive.lck` and re-executed itself into a new user namespace. The child (`reexec_in_user_namespace`) sets **no** parent-death signal, keeps stdout and stderr, and does the refresh, while the parent only waits and forwards signals. SIGKILL cannot be forwarded. | The child finishes the refresh while `realExec` waits on its pipes. yolo then reports `Timeout` and discards a working answer. This is the most likely reading of the 22.5 s. |
| Rootless, joining an existing pause process | A parent whose child (`reexec_userns_join`) has `PR_SET_PDEATHSIG` set to SIGTERM | The child dies. If it was the refresher, no `alive` file is written, and the next client starts the refresh again. |
| Rootful | The refresher itself | The refresh is aborted, and the next client starts it again. A refresh longer than 10 s then never completes under kill-and-retry. |

So a per-attempt kill either discards the answer or destroys the work every
later client is waiting on. A cross-workspace yolo lock would add nothing:
Podman already runs the refresh once, while the other clients wait cheaply.

## The gate

One function in `internal/runtime`, used by `run`'s runtime selection (both
`resolveRuntime` and `validateExplicitRuntime`) and by `yolo check`. The
duplicate probe in [`check/probes.go`](../../internal/cli/check/probes.go),
which already differs by discarding stderr, is deleted ([PR-D6](#PR-D6)).

1. **An attempt** runs `podman info --format json` in its own process group, so
   a Ctrl-C at the terminal does not reach it. Its stdout and stderr go to files,
   not pipes, so a surviving descendant can never stretch a wait
   ([PR-D3](#PR-D3)). No deadline applies to an attempt except the remaining
   budget. **yolo never kills it** ([PR-D2](#PR-D2)).
2. **Success** is exit 0 with JSON that parses. The parsed document becomes the
   launch's Podman facts ([PR-D5](#PR-D5)).
3. **An early exit**, meaning nonzero, or zero with unparsable output, is
   retried after a backoff of 1, 2, then 4 s (capped at 4 s), never past the
   budget. Stderr is kept for diagnosis and never used to classify the error
   ([PR-D4](#PR-D4)). A missing binary, or one that cannot start, fails at once,
   as today.
4. **At budget expiry, or on an interrupt,** yolo stops waiting. It does not
   kill the attempt: that `podman` may be doing the refresh every later client
   needs. The refusal names the process: *"podman (pid N) is still running; yolo
   left it to finish."* An interrupt exits 130.
5. **The budget** is fixed by [OQ-PR1](#OQ-PR1): a constant, with no config key
   and no `YOLO_*` dial. A budget that is too short would be a yolo bug, and
   escape hatches are only for broken user config ([PR-D11](#PR-D11)).

A warm launch pays nothing new: one `podman info --format json` in place of
today's two or three separate queries, and no sleep.

### What the gate's answer replaces

A Linux Podman launch makes several more Podman queries after runtime selection.
Each has its own failure behavior. **SOURCED** on `51620f7e`:

| Caller | Query | On failure today | Under this design |
| :--- | :--- | :--- | :--- |
| `hostLoopbackFactsFor` ([`hostloopback.go`](../../internal/cli/run/hostloopback.go)) | its own `podman info --format json`, 10 s | Returns empty facts, so no `--network` option, no warning, and `YOLO_HOST_LOOPBACK=unknown`, which never escalates ([OQ-R3](../reference/loopback-tls-reachability.md#oq-r3)). **Every loopback-TLS service is unreachable in that jail, and nothing is printed.** | Reads the Podman facts. The query is gone. |
| `AutoLoadOptions.Rootless` → `image.PodmanRootlessness` ([`storewrite.go`](../../internal/image/storewrite.go)) | its own `podman info --format json`, only before a copy | `RootlessUnknown`, so the bare copy runs, which a rootless store cannot take | Reads the Podman facts. The query is gone. |
| `findRunningContainer`, the attach decision ([`lifecycle.go`](../../internal/cli/run/lifecycle.go)) | `podman ps -q`, **no deadline** | A nonzero exit reads as "not running", and the launch goes fresh | Tri-state, in the `probeExistingContainer` shape. "Could not ask" refuses ([PR-D8](#PR-D8)). |
| `reapOrphanedJails` → `liveYoloContainers` | `podman ps -a`, 10 s | Already tri-state: unknown means no reap | Unchanged |
| `yolo check`'s image-delivery section | `image.PodmanRootlessness` | as above | Reads the gate's answer |

Everything after the gate stays single-shot: those calls assume a Podman that
has just answered. The gate adds no retries to image delivery or container
creation. Their existing rules are unchanged, including the image copy's one
retry ([failure paths](../reference/image-staging-vs-baking.md#failure-paths)).

## What the user sees, and what is recorded

**On the [launch stream](../reference/report-tiers.md#the-launch-stream)**
(stderr), with nothing that can be hidden:

- A warm probe prints nothing within the 2 s progress grace (`progress.DefaultGrace`).
- A slow first attempt shows `Checking that podman is running` with live detail
  through `Line.Set`: *"waiting for podman to answer (it may be finishing
  post-boot cleanup), 14s of 60s"*.
- Each early exit prints one line above the progress line through
  `Line.Println`: *"podman info: exit 125: <first stderr line>; retrying in 2s"*.
  A fast-failing permanent fault therefore shows its reason within the grace
  period. The user can press Ctrl-C at that point rather than wait out the budget.
- Success after a wait closes with *"done after 3 attempts (41s)"*.
- The refusal keeps today's stream (`o.Stdout`, like every runtime-selection
  refusal; both streams are teed to `launch.log`). It names the attempt count,
  the elapsed time, the last stderr line, and any `podman` left running.

**In `host-perf.log`**, when timing is on ([PR-D10](#PR-D10)): a `runtime.ready`
span around the gate; one `runtime.ready.attempt` note per attempt, giving its
duration, its outcome (exit code, still running, or interrupted) and its first
stderr line, truncated; and `podman.facts` emitted from the gate's answer.
Every event is also written on the failure path, before the refusal returns.
Today's `probes.done` mark comes after runtime selection returns, so a failed
launch records nothing about its probe.

## Alternatives and risks

| Option | Disposition |
| :--- | :--- |
| A host-wide exclusive lock around readiness (this doc's first draft) | **Rejected** ([PR-D1](#PR-D1)). Its premise, that concurrent launches repeat Podman's cold-start work, is false: `alive.lck` already runs the refresh once. The lock would queue every waiter's own verification, trading parallelism for slowness (ruling 4). A queued waiter could also fail with no Podman fault at all. Revisit only if a measured restore storm shows concurrent probes lengthen recovery. |
| Kill each attempt at 10 s and retry | **Rejected** ([PR-D2](#PR-D2)). See [the kill table](#why-a-timed-out-probe-is-not-a-failed-podman). |
| Kill the whole process group at expiry (`Setsid` plus a group kill, as `DetachGit` does) | **Rejected for this probe.** It is right for git, whose helpers do no shared work, but here it aborts the refresh every later client needs. |
| One longer single attempt with no retries | **Subsumed.** The gate is exactly that plus retries after an early exit. |
| Ignore a failed `podman info` and launch anyway | **Rejected.** It hides a broken runtime and moves the failure into image or container operations. |
| Have Waykeeper pace its restores | **Not the fix.** Any caller can start concurrent jails, and runtime readiness is yolo's job. Pacing remains an operational option. |

| Risk | Containment |
| :--- | :--- |
| A permanent fault costs the full budget. Examples: a needed `podman system migrate` after an upgrade, no `XDG_RUNTIME_DIR` for a restorer started before login (linger off), a `CONTAINER_HOST` or default connection pointing at an inactive socket, a stopped Linux `podman machine` | Its reason is printed within 2 s and the user may interrupt. [OQ-PR1](#OQ-PR1) option D would scope the long budget to the boot window instead. Linux remote connections and Linux `podman machine` get the same hint as today; better hints are out of scope. |
| A refused launch leaves a `podman` running | Named in the refusal. It is Podman's own work and finishes or blocks on Podman's own lock. yolo never waits for it again. |
| Other Podman clients still contend: `podman-restart.service`, quadlet units, other tools, and yolo's own housekeeping | The budget absorbs them. yolo's own share is [OQ-PR2](#OQ-PR2). The host check records which actors ran. |
| The same slow probe occurs with no reboot, for example behind another workspace's `podman volume rm`, which blocks other Podman runs (measured, [`scratchremoval.go`](../../internal/cli/run/scratchremoval.go)) | The gate does not care why Podman is slow. It covers steady-state storage contention too. |

## Testing and the real-host check

The tests listed in [the plan](podman-reboot-readiness-plan.md) run through runtime
selection, not an isolated helper. Each fails if the `run` path (explicit or
autodetected) or `yolo check` stops calling the gate. They also count the `info`
argv across one whole fake launch and require exactly one. Two cases use a real
subprocess: a grandchild that holds stdout or stderr open, and an attempt that
outlives the budget and is left running.

A nested jail partly exercises this, because its first Podman command is a cold
refresh. But a nested Podman is forced rootful (`--userns=host`), so it takes the
rootful row of [the kill table](#why-a-timed-out-probe-is-not-a-failed-podman),
never the re-exec row. **The rootless behavior is settled only on a real
rootless host.**

**The host check** (the maintainer's, after shipping):

1. **Reproduce first, before building.** As a user with **no running
   containers**, delete `alive` from the rootless engine tmpdir to force a
   refresh with several stopped `--rm` jails present. Then time a 10 s-capped
   `podman info` against an uncapped one. If the capped one reports a timeout
   while its child finishes, the inferred reading of the 22.5 s is confirmed.
2. At a real reboot, let the restorer start its usual set, about six or seven
   workspaces within 30 s plus a later straggler. Each must start or refuse
   within the budget. Read `runtime.ready` from each workspace's log.
3. Record, per launch: host uptime, `loginctl show-user --property=Linger`, the
   launch's `XDG_RUNTIME_DIR`, `podman info --format '{{.Host.Security.Rootless}}'`,
   and the active Podman user and system units (`podman-restart`, quadlets).
   These are the actors the gate is deliberately blind to.

## Open Questions

1. 💬 <a id="OQ-PR1"></a>**[OQ-PR1](#OQ-PR1): How long may a Linux Podman launch
   wait for Podman to answer?** This applies to every Linux Podman launch and
   every `yolo check`, including one typed by hand. It sets how late a
   permanently broken Podman gives its final refusal. It is also the only bound
   on a patient probe, since there is no per-attempt kill and no lock. The
   measured numbers: this workspace's cold probe at the reboot took 4.0 s. The
   inferred refresh finished at about 22.5 s (not a probe duration). One Podman
   storage operation on this btrfs host has stalled 31.9 s. The storm took 44 s,
   including a 16.1 s housekeeping hold.

   - **A. 60 seconds, constant.** Covers the inferred 22.5 s refresh with 2.5×
     margin, and one 32 s stall plus a retry. A permanent fault shows its reason
     at about 2 s, and its final refusal comes at 60 s.
   - **B. 30 seconds, constant.** Fails sooner. That leaves 7.5 s of margin over
     the inferred refresh and no room for one 32 s stall.
   - **C. 120 seconds, constant.** Refresh time plausibly grows with the number
     of stale containers. It doubles the wait before the final refusal for no
     measured case.
   - **D. Boot-relative.** 60 s while `/proc/uptime` is under 10 minutes,
     otherwise today's single 10 s attempt, with an unreadable uptime counting as
     "not recently booted". Permanent faults fail fast on a warm host. The cost
     is a heuristic in place of a cause, and losing recovery from steady-state
     contention and from a runtime-directory refresh, both of which happen
     outside the boot window.

   <!-- vantage: oq id=OQ-PR1 leaning="A: 60 s constant, no config key or YOLO_ dial; it covers the inferred 22.5 s refresh and one measured 32 s stall, and a permanent fault's reason already prints within the 2 s grace." -->

   _Leaning:_ A. It covers every measured number with margin. And since the
   first failure's reason prints within 2 s, the late refusal is a cost the user
   can see and cut short, not a silent minute. Re-measure with host-check step 1
   before building, and change the constant then if the forced refresh runs
   longer.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 <a id="OQ-PR2"></a>**[OQ-PR2](#OQ-PR2): Should a launch that found Podman
   slow skip its housekeeping's Podman work for that pass?** After a reboot,
   every pre-reboot jail's leftovers are due at once. So the first restored
   launch runs its biggest reaping pass (7 `podman rmi`s at this reboot) while
   the other workspaces still need Podman. It also holds the housekeeping lock
   that their image loads block on, for 16.1 s at this reboot.

   - **A. Unchanged.** Simplest. The reboot window keeps a yolo-made Podman load
     and a 16 s machine-wide hold.
   - **B. Skip the classes that run Podman (image reap, scratch-volume reap) on a
     launch whose gate took more than one attempt or waited past the grace
     period.** No cross-process signal is needed. The debounce stamps are
     untouched, so the next ordinary launch runs them. At this reboot, the 4.0 s
     probe would have triggered it. The cost: on a disk-starved host (the same
     morning's Syncthing warning), reclamation waits one more launch.
   - **C. Skip the same classes while host uptime is under a fixed window.**
     This also covers a launch whose own probe was fast but whose neighbors'
     probes are slow. It is a timer standing in for a cause.

   <!-- vantage: oq id=OQ-PR2 leaning="B: a launch whose readiness gate needed a retry or waited past the grace skips the Podman-running housekeeping classes for that pass; their debounce is untouched, so the next launch runs them." -->

   _Leaning:_ B. The slot already skips when its lock is held, so skipping when
   Podman is struggling extends an existing rule rather than adding a mechanism.
   It removes a contender yolo itself creates, which is the fix-the-cause
   reading of ruling 4. It is not proven that housekeeping slowed anyone's
   probe: this workspace's probe had finished before its slot began. What is
   measured is the 16.1 s lock hold on the load path.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 <a id="OQ-PR3"></a>**[OQ-PR3](#OQ-PR3): Should every launch leave one
   line in a machine-wide log, so one reboot's storm can be read in one place?**
   This investigation could not find the failing launch. Launch records live in
   each workspace's `.yolo/`. The storm was reconstructed only from the loophole
   crossing log, which records jails that got as far as a loophole, and a
   launch refused at runtime selection never does.

   - **A. Per-workspace only** (`launch.log`, `host-perf.log`). Nothing new is
     disclosed, and the next incident is as hard to reconstruct as this one.
   - **B. One line per launch in `~/.local/share/yolo-jail/logs/`** (UTC time,
     `paths.JailShortHash` of the container name, gate wait, attempts, outcome),
     beside `crossings.log`. A jail that mounts that directory, as this one does
     at `/ctx/host-yolo-logs`, sees other workspaces' launch times under the same
     short hashes `crossings.log` already shows. It sees no names and no
     secrets.
   - **C. B, but with workspace names.** Easier to read. A jail that mounts the
     log directory then learns the names of the user's other projects.
   - **D. Only when timing is on.** Off exactly when an unplanned reboot happens.

   <!-- vantage: oq id=OQ-PR3 leaning="B: one line per launch beside crossings.log, keyed by the jail short hash crossings.log already uses, so it discloses nothing new." -->

   _Leaning:_ B. The host check needs this record to be read at all, and keying
   it by the existing short hash adds no disclosure beyond `crossings.log`.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

Implementation decisions made in this doc. Each one yields to a ruling on the
questions above. Maintainer rulings 1 to 5 (host parity, no quiet mode,
tri-state, never limit parallelism, hatches only for broken user config) bind
every row.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="PR-D1"></a>PR-D1 | *Implementation decision.* No yolo readiness lock of any scope. Podman's `alive.lck` already serializes the refresh, and a yolo lock would queue verifications (ruling 4). Revisit only on a measured storm in which concurrent probes lengthen recovery | 2026-09-29 | [Mechanism](#why-a-timed-out-probe-is-not-a-failed-podman), [Alternatives](#alternatives-and-risks) | — |
| <a id="PR-D2"></a>PR-D2 | *Implementation decision.* An attempt is never killed. Its only bound is the remaining budget. Retries follow only an early exit | 2026-09-29 | [The gate](#the-gate) | — |
| <a id="PR-D3"></a>PR-D3 | *Implementation decision.* The probe's stdout and stderr go to unlinked temp files, not pipes, and the probe runs in its own process group (`Setpgid`). A surviving descendant cannot hold `Wait`, closing an output end cannot SIGPIPE a refresh mid-way, and a terminal Ctrl-C does not reach Podman. At expiry or on an interrupt, yolo stops waiting and names the pid | 2026-09-29 | [The gate](#the-gate) | — |
| <a id="PR-D4"></a>PR-D4 | *Implementation decision.* Every early exit is retryable, after a backoff of 1, 2, then 4 s (capped at 4 s) that never runs past the budget. Stderr is kept for diagnosis and never used to classify. A missing or unstartable binary fails at once | 2026-09-29 | [The gate](#the-gate) | — |
| <a id="PR-D5"></a>PR-D5 | *Implementation decision.* The gate's probe is `podman info --format json`, and its parsed answer is the launch's one Podman facts record. `hostLoopbackFactsFor`, `AutoLoadOptions.Rootless` and `yolo check`'s image-delivery section read it, and `podman.facts` is noted from it. One `info` per launch, pinned by a whole-launch test | 2026-09-29 | [What the gate's answer replaces](#what-the-gates-answer-replaces) | — |
| <a id="PR-D6"></a>PR-D6 | *Implementation decision.* `yolo check` calls the same gate with the same budget and the same progress line (ruling 1). `check/probes.go`'s copy of the probe is deleted. A shorter budget for check would bring back the disagreement this removes | 2026-09-29 | [The gate](#the-gate) | — |
| <a id="PR-D7"></a>PR-D7 | *Implementation decision.* Linux only. Apple Container and macOS Podman Machine keep the one-shot probe, with an explicit macOS arm pinned by a test. To `podman info`, a stopped VM and a starting VM look alike, and a wait would charge every stopped-VM user the budget | 2026-09-29 | [Verdict](#verdict-and-boundary) | — |
| <a id="PR-D8"></a>PR-D8 | *Implementation decision.* The attach decision's `ps` becomes tri-state, with a deadline, and "could not ask" refuses the launch rather than launching fresh (ruling 3). The other `findRunningContainer` callers are unchanged | 2026-09-29 | [What the gate's answer replaces](#what-the-gates-answer-replaces) | — |
| <a id="PR-D9"></a>PR-D9 | *Implementation decision.* The wait, retry and success lines go on the launch stream through `Line.Set` and `Line.Println`. The refusal stays on `o.Stdout` with the other runtime-selection refusals. Neither can be hidden (ruling 2) | 2026-09-29 | [What the user sees](#what-the-user-sees-and-what-is-recorded) | — |
| <a id="PR-D10"></a>PR-D10 | *Implementation decision.* Perf events: the `runtime.ready` span, `runtime.ready.attempt` notes, and `podman.facts` from the gate. All are written on the failure path too | 2026-09-29 | [What is recorded](#what-the-user-sees-and-what-is-recorded) | — |
| <a id="PR-D11"></a>PR-D11 | *Implementation decision.* The budget is a constant: no config key and no `YOLO_*` dial (ruling 5) | 2026-09-29 | [The gate](#the-gate) | — |

## Appendix A: evidence

Times are given in UTC (Z), with EDT (UTC−4) where the source writes local time.
`launch.log` headers and the `host-perf.log` launch headers are local time.
`host-perf.log` events, `housekeeping.log` and `crossings.log` are UTC. The
broker service logs are local time.

| When (UTC) | What | Source |
| :--- | :--- | :--- |
| 03:54:27Z | Last loophole crossing before the reboot; none again until 14:34:24Z | `/ctx/host-yolo-logs/crossings.log` |
| 09:53:08Z | Claude broker's last line before the boot (a background refresh) | `host-service-claude-oauth-broker.log` (05:53:08 local) |
| 14:34:08Z | yolo-jail launch starts. It waits on another process's pack mirror, then fails DNS for github.com | `/workspace/.yolo/launch.log`, header `10:34:08-0400` |
| ~14:34:08–14:34:12Z | Runtime probe, `done (4.0s)`; `probes.done` at 14:34:12.143Z | `launch.log`; `host-perf.log` |
| 14:34:21Z | Brokers start (`startup:`) | broker logs, 10:34:21 local |
| 14:34:21.5Z | Second `podman info` (`assemble.host_loopback_probe`) takes 0.144 s; `podman.facts version=6.1.2 … rootless=true network=pasta` | `host-perf.log` |
| 14:34:24–14:34:40Z | Housekeeping slot, 16.095 s: 7 images removed, 97 store outputs, 27 flake-bundle generations, and 4 leftover scratch volumes of the gone `yolo-swarf-2954fcc7` jail (removed in the background in 0.3 s) | `host-perf.log`; `/workspace/.yolo/housekeeping.log` |
| 14:34:24–14:34:52Z | First crossings of six jails: `ae3a2fd4` (yolo-jail), `391dc586` (dotfiles), `f96ed222`, `f7d01a63` (forms), `cd67013c` (vantage), `dba4428e` (waykeeper; again at 14:35:10Z) | `crossings.log`. The IDs are `paths.JailShortHash`; two were checked by hand (`yolo-yolo-jail-887995ca`, `yolo-waykeeper-a3f2a09f`) |
| 14:34:40Z | 12 unattributed connections to the Claude broker, and 9 each to aws-auth and openai-auth-broker. A single launch makes about 3 per service (compare 15:59:17Z) | broker logs |
| 14:39:24Z | A seventh jail, `f2d82aaf`, first reaches a front | `crossings.log` |
| 14:39:24Z–15:59:11Z | No crossing on any front | `crossings.log` |
| 15:59:14Z | The yolo-jail jail from 14:34 exits, after `launch.run_with_proxy dur=5091.691s` | `host-perf.log` |

**Stall measurements on this host.** `launch.log` records a `podman run --rm`
client that stayed 31.9 s in `unlinkat` and another 8.0 s in `getdents64`.
`scratchremoval.go` records the same 2026-09-28 measurement, and it is why scratch
volumes are now named and removed outside the client. `housekeeping.log` records
scratch removals of 14.4 s, 17.4 s and 16.4 s before this reboot.

**Not found anywhere readable here:** the refused launch, its 22.5 s line, its
stderr, the 10:49:14 journal events, and the 0.529 s query. `host-service-journal.log`
was last written 2026-09-28 16:00:47Z, so no jail queried the host journal that
morning. The swarf workspace (`8259b056`) never reached a front after the boot,
so it is a candidate for the failing launch. So are `f2d82aaf` and the waykeeper
jail's second first-crossing.

**Sources outside this repository.** containers/podman `main`:
`libpod/runtime.go` (`makeRuntime`'s `alive.lck` / `alive` / `refresh()`
sequence), `pkg/rootless/rootless_linux.go` (`BecomeRootInUserNS` waits and
forwards catchable signals) and `pkg/rootless/rootless_linux.c`
(`reexec_in_user_namespace` sets no parent-death signal;
`reexec_userns_join` sets SIGTERM). Go `os/exec`: with `WaitDelay` zero, `Wait`
reads pipes until EOF, which may not come until orphaned descendants close them.
