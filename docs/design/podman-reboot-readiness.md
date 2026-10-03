---
title: "Let Podman finish waking before a jail gives up"
date: 2026-09-29
status: accepted
stage: BUILT
next: "Run the real-host check on a rootless Linux host: reproduce the cold refresh, then read runtime.ready from each launch at a real reboot"
tags: [design, launch, podman, reliability]
summary: "Built 2026-09-29: a patient, budget-bounded Podman readiness probe shared by the launch and yolo check, whose one answer every later Podman fact on the launch path reads; answers that cannot clear fail at once (OQ-PR1), the housekeeping slot holds its lock one deletion at a time (OQ-PR2), and every launch leaves one machine-wide log line (OQ-PR3)."
vantage:
  status-chip: true
---

# Let Podman finish waking before a jail gives up

**Status:** 2026-09-29, the same day it was designed and its three questions ruled
([the ledger](#decision-ledger)), then reviewed the same day, whose fixes are ledgered
beside the rows they correct. MEASURED by unit tests through `run.Run`,
runtime selection and `check.Check`, on a fake podman whose clock the test drives, plus a real
subprocess for the attempt runner; [the testing section](#testing-and-the-real-host-check) names
them. UNMEASURED on a real reboot, and on a rootless podman at all: a nested jail's podman is
rootful, so the re-exec row of [the kill table](#why-a-timed-out-probe-is-not-a-failed-podman) is
settled only on CI or a real rootless host. Pre-build evidence was checked against `51620f7e`;
podman's error texts against podman v6.1.2's source. Code is cited by symbol, not by line.

> **In short.** The first `podman info` after a boot does Podman's own
> post-boot cleanup. yolo kills that probe at 10 seconds and cancels the launch,
> though the cleanup was probably seconds from done. The fix is to stop killing
> it: wait up to one budget for Podman to answer, retry only after an early
> error, and reuse that one answer for every later Podman fact the launch needs.

**Why it matters.** At the 2026-09-29 reboot, six workspaces relaunched within
44 seconds and a seventh five minutes later. One launch was refused after a 22.5-second progress line,
before its container existed. That failure fits yolo aborting a Podman that was
about to answer (see [the mechanism](#why-a-timed-out-probe-is-not-a-failed-podman)).
Separately, a launch that passes today's probe runs more `podman info` queries of
its own. If the host-loopback one fails, the launcher passes no forwarding option
and prints nothing, and the jail boots with every loopback-TLS service
unreachable. The jail's boot output does warn about each unreachable service, but
it cannot name the cause, and the launch is not refused.

**The shape.** One readiness function in `internal/runtime`, called by the
launch and by `yolo check`. It lets an attempt run until the attempt exits or
the budget ends, retries only after an early error exit that can clear on its own,
never kills Podman, and hands its parsed answer to every later Podman fact on the
launch path. **There is no cross-workspace lock**, because Podman already
serializes its own cleanup. Two rulings rode with it: the housekeeping slot now
holds its lock one deletion at a time ([OQ-PR2](#OQ-PR2)), and every launch leaves
one line in a machine-wide log ([OQ-PR3](#OQ-PR3)).

**Cost.** A Podman that is broken in a way yolo recognises (a permission or
configuration error, a missing helper, an explicit "run `podman system migrate`")
refuses at once and names the fix ([OQ-PR1](#OQ-PR1), [PR-D13](#PR-D13)). One broken
in a way yolo does not recognise reports its final refusal only at the end of the
budget; the reason for its first failure appears within the progress grace period
(2 s). A refused launch may leave one `podman` running, and it names that process.

**Start at [the gate](#the-gate).** Its no-kill rule is what makes a reboot
recoverable.

**Needs your ruling:** none. [OQ-PR1](#OQ-PR1), [OQ-PR2](#OQ-PR2) and [OQ-PR3](#OQ-PR3) were ruled 2026-09-29; the build is released.

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
  also recorded journal events near 10:49:14 local and, for the next launch,
  which it reported as working, a 0.529 s facts query at 10:50:00. The report
  does not name which workspace failed. That successful 10:50:00 launch left no
  trace in the host logs: no loophole crossing from 14:39:24Z to 15:59:12Z, and
  no aws-auth or openai-auth-broker connection between 10:39 and 11:59 local.
  So either the reported times are off, or that workspace selects no pack that
  reaches a loophole. None of the reported lines
  is in the logs a jail here can read: this workspace's `.yolo/` and the host log
  directory mounted at `/ctx/host-yolo-logs`.
- **MEASURED, this workspace (yolo-jail).** It launched at 10:34:08 EDT
  (14:34:08Z) and probed Podman in **4.0 s**. That is the only probe in its log
  slow enough to show the progress line since the line shipped on 2026-09-28. It
  ran two more `podman info` queries: the image copy's rootlessness check at
  about 14:34:19.9Z (untimed, and run with no deadline at all), and the
  host-loopback query, which took 0.144 s at 14:34:21Z. The jail then ran without a break until 15:59:14Z. So the host did
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
launch started its probe about 14:33:50Z, just before this workspace. That launch
was the first Podman client after boot, so its `podman info` did Podman's
post-boot refresh.
yolo killed the probe's direct process at 10 s. Podman's re-executed child
survived that kill and finished the refresh about 12.5 s later. This workspace's
probe, started at about 14:34:08Z, waited behind that refresh and got its answer
at 14:34:12Z, 4.0 s later. The killed probe's `Wait` returned at the same moment,
so yolo reported a 22.5 s timeout just as Podman became ready. The
[mechanism section](#why-a-timed-out-probe-is-not-a-failed-podman) shows why each
step happens.

One observation neither supports nor contradicts this reading. At 14:34:08Z this
workspace's launch waited for another yolo process to release a pack mirror. The
pack refresh runs before runtime selection (`Run` calls `refreshPacks` before
`resolveRuntime`), so that process was still in its pack phase, not inside a
runtime probe. It shows a second storm launch running at that moment. It cannot
be the failing launch if that launch's probe began at about 14:33:50Z. So the
refresher was some other process: another launch, or a Podman client that is not
yolo, such as `podman-restart.service` or a quadlet unit. The host check settles
which.

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
   budget ([PR-D4](#PR-D4)) — unless podman's own words say it cannot clear on its
   own. [OQ-PR1](#OQ-PR1)'s ruling added that rule: a permission or configuration
   error, a missing helper, or an explicit "run `podman system migrate`" refuses at
   once and names the fix. The classification reads podman's fatal lines only, from
   a table of podman v6.1.2's error texts; a line that says podman is busy wins over
   one that looks permanent; and an error the table does not recognise keeps
   retrying within the budget, never refusing early ([PR-D13](#PR-D13)). A retry
   starts only if the budget left after its backoff is longer than the **retry
   floor** *(coined here)*: how long podman took to give the answer being retried,
   capped at 5 s, so the gate never starts an attempt it could only abandon
   mid-answer ([PR-D22](#PR-D22)).
   An attempt that cannot start at all is read by its errno, under the same rule: a
   missing binary, or one this user cannot execute, fails at once and names the fix;
   one that can clear, such as a binary busy being replaced (`ETXTBSY`) or a fork
   refused at a process limit, is retried like an early exit; and a failure of yolo's
   own scratch files is named as that ([PR-D23](#PR-D23)).
4. **At budget expiry, or on an interrupt,** yolo stops waiting. It does not
   kill the attempt: that `podman` may be doing the refresh every later client
   needs. The refusal names the process: *"podman (pid N) is still running; yolo
   left it to finish."* An interrupt exits 130.
5. **The budget** is 60 s ([PR-D12](#PR-D12)): a constant, with no config key
   and no `YOLO_*` dial. A budget that is too short would be a yolo bug, and
   escape hatches are only for broken user config ([PR-D11](#PR-D11)).
   [OQ-PR1](#OQ-PR1) ruled that it applies to every launch and every `yolo check`,
   never keyed on a reboot.

A warm launch pays nothing new: one `podman info --format json` in place of
today's two or three separate queries, and no sleep.

### What the gate's answer replaces

A Linux Podman launch makes several more Podman queries after runtime selection.
Each has its own failure behavior. **SOURCED** on `51620f7e`:

| Caller | Query | On failure today | Under this design |
| :--- | :--- | :--- | :--- |
| `hostLoopbackFactsFor` ([`hostloopback.go`](../../internal/cli/run/hostloopback.go)) | its own `podman info --format json`, 10 s | Returns empty facts, so no `--network` option, no launcher warning, and `YOLO_HOST_LOOPBACK=unknown`, which never escalates ([OQ-R3](../reference/loopback-tls-reachability.md#oq-r3)). **Every loopback-TLS service is unreachable in that jail, and the launch is not refused.** The jail's reachability witness still warns at boot for each unreachable service, but its explanation cannot name the cause, a failed probe on the launcher side | Reads the Podman facts. The query is gone. |
| `AutoLoadOptions.Rootless` → `image.PodmanRootlessness` ([`storewrite.go`](../../internal/image/storewrite.go)) | its own `podman info --format json`, only before a copy, **no deadline** (`runCapture`) | `RootlessUnknown`, so the bare copy runs, which a rootless store cannot take | Reads the Podman facts. The query is gone. |
| `findRunningContainer`, the attach decision ([`lifecycle.go`](../../internal/cli/run/lifecycle.go)) | `podman ps -q`, **no deadline** | A nonzero exit reads as "not running", and the launch goes fresh | Tri-state, in the `probeExistingContainer` shape. "Could not ask" refuses ([PR-D8](#PR-D8)). |
| `reapOrphanedJails` → `liveYoloContainers` | `podman ps -a`, 10 s | Already tri-state: unknown means no reap | Unchanged |
| `yolo check`'s image-delivery section | `image.PodmanRootlessness` | as above | Reads the gate's answer |

Everything after the gate stays single-shot: those calls assume a Podman that
has just answered. The gate adds no retries to image delivery or container
creation. Their existing rules are unchanged, including the image copy's one
retry ([failure paths](../reference/image-staging-vs-baking.md#failure-paths)).

### Where the budget applies

Moved here verbatim from [OQ-PR1](#OQ-PR1), where its answer is. This applies to
every Linux Podman launch and every `yolo check`, including one typed by hand.
It decides how late a permanently broken Podman on a warm host gives its final
refusal, against whether a slow Podman outside the boot window is waited for.
The number itself is a measured constant ([PR-D12](#PR-D12)), not this question.

- **A. Always.** Every launch gets the full budget. A permanent fault shows
  its reason at about 2 s, and its final refusal comes at 60 s, on a warm host
  too. A Podman refresh that runs outside a boot (the
  [alternative reading](#what-happened-and-what-is-still-unknown): its runtime
  directory removed with linger off) is recovered the same way, and so is any
  other slow `info` whatever its cause.
- **B. Boot window only.** The full budget while `/proc/uptime` is under 10
  minutes, otherwise today's single 10 s attempt, with an unreadable uptime
  counting as "not recently booted". Permanent faults fail fast on a warm
  host. The cost is a timer standing in for a cause: the runtime-directory
  refresh and any slow `info` outside the window get today's 10 s refusal
  back, though without the kill ([PR-D2](#PR-D2) still holds).

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

### A machine-wide launch log

Moved here verbatim from [OQ-PR3](#OQ-PR3), where its answer is. This
investigation could not find the failing launch. Launch records live in
each workspace's `.yolo/`. The storm was reconstructed only from the loophole
crossing log, which records jails that got as far as a loophole, and a
launch refused at runtime selection never does.

- **A. Per-workspace only** (`launch.log`, `host-perf.log`). Nothing new is
  disclosed, and the next incident is as hard to reconstruct as this one.
- **B. One line per launch in `~/.local/share/yolo-jail/logs/`** (UTC time,
  `paths.JailShortHash` of the container name, gate wait, attempts, outcome),
  beside `crossings.log`. It adds no names and no secrets, but it does disclose
  something new: `crossings.log` records only jails that reached a loophole,
  and this line records the time and outcome of every launch, refused ones and
  ones with no loophole included. A jail that mounts that directory, as this
  one does at `/ctx/host-yolo-logs`, sees all of it under hashes that are
  opaque but stable.
- **C. B, but with workspace names.** Easier to read. A jail that mounts the
  log directory then learns the names of the user's other projects.
- **D. Only when timing is on.** Off exactly when an unplanned reboot happens.

## Alternatives and risks

| Option | Disposition |
| :--- | :--- |
| A host-wide exclusive lock around readiness (this doc's first draft) | **Rejected** ([PR-D1](#PR-D1)). Its premise, that concurrent launches repeat Podman's cold-start work, is false: `alive.lck` already runs the refresh once. The lock would add nothing but a queue of every waiter's own cheap verification. A queued waiter could also fail with no Podman fault at all. Revisit only if a measured restore storm shows concurrent probes lengthen recovery. |
| Kill each attempt at 10 s and retry | **Rejected** ([PR-D2](#PR-D2)). See [the kill table](#why-a-timed-out-probe-is-not-a-failed-podman). |
| Kill the whole process group at expiry (`Setsid` plus a group kill, as `DetachGit` does) | **Rejected for this probe.** It is right for git, whose helpers do no shared work, but here it aborts the refresh every later client needs. |
| One longer single attempt with no retries | **Subsumed.** The gate is exactly that plus retries after an early exit. |
| Ignore a failed `podman info` and launch anyway | **Rejected.** It hides a broken runtime and moves the failure into image or container operations. |
| Have Waykeeper pace its restores | **Not the fix.** Any caller can start concurrent jails, and runtime readiness is yolo's job. Pacing remains an operational option. |

| Risk | Containment |
| :--- | :--- |
| A permanent fault podman names in words yolo recognises: a needed `podman system migrate`, a refused user namespace, a broken `newuidmap`, an unparsable `containers.conf`, a permission error | Refused at once, naming the fix ([OQ-PR1](#OQ-PR1), [PR-D13](#PR-D13)). |
| A permanent fault yolo does not recognise costs the full budget. Examples: a `CONTAINER_HOST` or default connection pointing at an inactive socket, a stopped Linux `podman machine` | Its reason is printed within 2 s and the user may interrupt. Unrecognised is retried by rule: a socket that comes up late at boot is exactly such an answer, and it clears. Linux remote connections and Linux `podman machine` get the same hint as before; better hints are out of scope. |
| A refused launch leaves a `podman` running | Named in the refusal. It is Podman's own work and finishes or blocks on Podman's own lock. yolo never waits for it again. |
| Other Podman clients still contend: `podman-restart.service`, quadlet units, other tools, and yolo's own housekeeping | The budget absorbs them. yolo's own share is smaller: the housekeeping slot now holds its lock one deletion at a time ([OQ-PR2](#OQ-PR2)). The host check records which actors ran. |
| A slow probe with no reboot, for example behind another workspace's `podman volume rm`. **INFERRED, untested for `podman info`:** [`scratchremoval.go`](../../internal/cli/run/scratchremoval.go) measured such a removal blocking other `podman run`s that mount a volume, and nothing yet shows `info` waiting behind it (host-check step 1 measures it) | The gate does not care why Podman is slow. If `info` does wait there, the gate covers steady-state storage contention too. |

### Housekeeping after a reboot

Moved here verbatim from [OQ-PR2](#OQ-PR2), where its answer is. After a
reboot, every pre-reboot jail's leftovers are due at once. So the first restored launch runs its biggest
reaping pass (7 `podman rmi`s at this reboot) while the other workspaces still
need Podman. Five of the slot's seven classes run Podman: the image reap; the
store-output and flake-bundle reaps, which ask `podman ps -a` and
`podman inspect` which containers are live; the small classes (`podman ps -a`);
and the scratch-volume reap. The slot also holds the machine-wide
housekeeping lock, which every other launch's image load takes around its
re-inspect, for 16.1 s at this reboot. The image reap was about 4 s of that.
The store outputs ran until 14:34:34Z, and the small classes and flake bundles
until 14:34:40Z.

- **A. Unchanged.** Simplest. The reboot window keeps a yolo-made Podman load
  and a 16 s machine-wide hold.
- **B. Skip the whole slot on a launch whose gate took more than one attempt
  or waited past the grace period.** Every class is debounced on its own
  stamp, and the stamps are untouched, so the next ordinary launch runs them.
  No cross-process signal is needed, and the lock is not taken at all. At this
  reboot, the 4.0 s probe would have triggered it. The cost: on a disk-starved
  host (the same morning's Syncthing warning), every class's reclamation waits
  one more launch, including the two that run no Podman (the cache purge and
  the image tars).
- **C. Skip the same slot while host uptime is under a fixed window.** This
  also covers a launch whose own probe was fast but whose neighbors' probes
  are slow. It is a timer standing in for a cause.

## Testing and the real-host check

**What is built and pinned (2026-09-29).** Every test below runs under
`go test -short` and asks whether it fails if its call site goes:

- **Through runtime selection**, once down each of the three ways a launch reaches
  it — `YOLO_RUNTIME`, the config's `runtime` key, autodetection
  (`internal/cli/run/podmanready_test.go`): a podman busy for sixteen attempts that
  answers on the last one the budget allows; an attempt still running at the end,
  refused with its pid and left running; every early exit printed and the last one
  in the refusal; a permanent error refused at once with its fix; an unstartable
  binary refused with its fix, and a busy one retried; macOS with Podman Machine and
  with Apple Container keeping one one-shot probe; the span and notes written on a
  refusal; a Ctrl-C exiting 130, once on the gate's interrupt seam and once as a real
  `SIGINT` to the test process, so the launch's own catch is what stops the wait.
- **One `podman info` per launch**, across a whole fake fresh launch through
  `run.Run` (`TestALaunchAsksPodmanOnceAndEveryReaderTakesTheGatesAnswer`): the gate
  asks, the host-loopback decision and the image copy's store facts read its answer,
  and a rootless pasta host that recovered inside the gate gets the forwarding option
  and `YOLO_HOST_LOOPBACK=requested` on its argv — the wiring, not the network.
- **`yolo check`** (`internal/cli/check/podmanready_test.go`): one gate per check,
  read by the runtime section and the runtime resolution; a permanent error refused
  at once; macOS never asking the gate. The image delivery section and the Disk I/O
  priority section read the gate's answer and run no `info` of their own
  (`section_imagedelivery_test.go`, `section_iopriority_test.go`).
- **The attach decision** refuses on a `ps` that exits 125
  (`TestAnAttachDecisionThatCannotAskRefuses`), and Apple Container's `container ls`
  arm keeps the same tri-state (`TestAppleContainersAttachProbeKeepsTheTriState`). The
  refusal names each runtime's own list command, driven through `runContainer`
  (`TestTheAttachRefusalNamesEachRuntimesListCommand`).
- **The attempt runner, on real subprocesses** (`internal/runtime/podmanattempt_test.go`):
  it returns when a child exits though a grandchild holds its output; an attempt past
  its deadline is left running, in its own process group; an interrupt stops the wait
  and not the process; a temporary directory yolo cannot use is named as yolo's own
  scratch file; and, on Linux, a binary busy being written (`ETXTBSY`) is retried until
  it answers.
- **The classification**, row by row against podman's own texts
  (`TestClassifyPodmanFailure`), with its logrus lines ignored and transient winning,
  and errno by errno for a start error (`TestClassifyStartError`). The retry floor
  starts no attempt it would have to abandon, and a refusal behind a still-running
  attempt keeps podman's last error, a retried start error included
  (`internal/runtime/podmanready_test.go`).
- **The housekeeping slot** (`internal/cli/run/housekeeping_test.go`): two passes never
  interleave; the shared lock is held during each deletion and free before, between
  and after; a deletion waits for a launch's hold and rechecks after it; every class
  hands the guard to its reaper, pinned by behavior for the agent-staging,
  retired-loophole and image-tar classes (a store changed while a deletion waits keeps
  the item). Each class's recheck is in `internal/prune/guard_test.go` and
  `internal/flakebundle/flakebundle_test.go`.
- **The launch line** (`internal/cli/run/launchrecord_test.go`): refused at the gate,
  refused before it, refused by each of the three guards `Run` runs first, interrupted
  at the gate and by a signal after pack staging, attached, started — written while the jail runs, and before the macos-user backend
  is handed the launch — and rotation, alone and under a crowd of launches at the
  boundary. No test there writes the machine's real log: the package's `TestMain`
  starts every test in a `HOME` of its own (`TestThisPackageStartsInAHomeOfItsOwn`).

The test package's `TestMain` makes the real attempt runner refuse
([PR-D20](#PR-D20)), so no unit test can reach the machine's podman.

A nested jail partly exercises this, because its first Podman command is a cold
refresh. But a nested Podman is forced rootful (`--userns=host`), so it takes the
rootful row of [the kill table](#why-a-timed-out-probe-is-not-a-failed-podman),
never the re-exec row. **The rootless behavior is settled only on a real
rootless host or CI**, and so is the whole of a real reboot.

**The host check** (the maintainer's, after shipping):

1. **Reproduce first, before building.** As a user with **no running
   containers**, delete `alive` from the rootless engine tmpdir to force a
   refresh with several stopped `--rm` jails present. Then time a 10 s-capped
   `podman info` against an uncapped one. If the capped one reports a timeout
   while its child finishes, the inferred reading of the 22.5 s is confirmed, and
   the uncapped time is the refresh duration that sizes the budget
   ([PR-D12](#PR-D12)). In the same session, time a `podman info` started during a
   `podman volume rm` of a large volume, to settle whether storage contention
   reaches `info` at all.
2. At a real reboot, let the restorer start its usual set, about six or seven
   workspaces within 30 s plus a later straggler. Each must start or refuse
   within the budget. Read `runtime.ready` from each workspace's log.
3. Record, per launch: host uptime, `loginctl show-user --property=Linger`, the
   launch's `XDG_RUNTIME_DIR`, `podman info --format '{{.Host.Security.Rootless}}'`,
   and the active Podman user and system units (`podman-restart`, quadlets).
   These are the actors the gate is deliberately blind to.

## Open Questions

1. ✅ <a id="OQ-PR1"></a>**[OQ-PR1](#OQ-PR1): Does the 60 s budget apply to every
   Linux Podman launch, or only to one shortly after a boot?** What it decides,
   and each option in full: [where the budget applies](#where-the-budget-applies).

   - **A. Always.** Every launch gets the full budget.
   - **B. Boot window only.** The full budget while `/proc/uptime` is under 10
     minutes, otherwise today's single 10 s attempt, with an unreadable uptime
     counting as "not recently booted".

   _Leaning:_ A. The first failure's reason prints within 2 s, so the late
   refusal is a cost the user can see and cut short, not a silent minute. And the
   alternative reading of this incident is a refresh at 10:49 with no reboot,
   which B would not recover.

   <!-- vantage: question id=OQ-PR1 -->

   **Answer:**
   > **Ruled 2026-09-29: the budget applies always (the doc's A), and never on a reboot test.**
   > The maintainer: *"We definitely shouldn't detect a reboot, so we certainly shouldn't do
   > something only after a reboot. But ideally we can distinguish between 'nobody attempted to
   > start Podman ever' or 'it's not installed and it's just not responding' so we don't wait for
   > something to come up that's never going to come up guaranteed. But if we can't do that …
   > we're going to have that 60 second delay, so we can take your leaning."* So the 60 s budget
   > covers every launch and `yolo check`, and the build adds a fail-fast rule: yolo retries only
   > an answer that can clear on its own (Podman busy, a lock held, a database mid-migration) and
   > refuses at once on one that cannot (no `podman` binary, a permission or configuration error,
   > an explicit "run `podman system migrate`"), naming the fix, instead of spending the minute
   > on it.

   **Built 2026-09-29** as [PR-D13](#PR-D13): the classification is a table of podman
   v6.1.2's own error texts, and anything it does not recognise keeps retrying.

2. ✅ <a id="OQ-PR2"></a>**[OQ-PR2](#OQ-PR2): Should a launch that found Podman
   slow skip its housekeeping pass?** What a reboot's first pass costs, and each
   option in full: [housekeeping after a reboot](#housekeeping-after-a-reboot).

   - **A. Unchanged.** Simplest. The reboot window keeps a yolo-made Podman load
     and a 16 s machine-wide hold.
   - **B. Skip the whole slot on a launch whose gate took more than one attempt
     or waited past the grace period.**
   - **C. Skip the same slot while host uptime is under a fixed window.** This
     also covers a launch whose own probe was fast but whose neighbors' probes
     are slow. It is a timer standing in for a cause.

   _Leaning:_ B. The slot already skips when its lock is held, so skipping when
   Podman is struggling extends an existing rule rather than adding a mechanism,
   and it removes a contender yolo itself creates. Skipping only the classes that
   run Podman would save little: five of seven do, and the lock would still be
   held for the rest. It is not proven that housekeeping slowed anyone's probe:
   this workspace's probe had finished before its slot began. What is measured is
   the 16.1 s lock hold on the load path.

   <!-- vantage: question id=OQ-PR2 -->

   **Answer:**
   > **Ruled 2026-09-29, none of the doc's options as written: the cleanup takes its lock one
   > deletion at a time.** The maintainer rejected skipping on a slow probe: *"why are we going
   > to skip a cleanup pass? … You're trying to, like, guess the load from what happens? That
   > sounds like a bad idea."* Then, shown the new option: *"42, yes, take the leaning, A."* The
   > measured cost was only the 16.1 s hold on the lock every other launch's image re-inspect
   > takes. So the housekeeping slot takes the machine-wide lock around each deletion and
   > releases it between deletions; a launch's re-inspect waits at most one deletion, nothing is
   > skipped and nothing infers load. Each class re-checks that an item is unused under the lock
   > right before deleting it (image removal already refuses an image a container uses).

   **Built 2026-09-29** as [PR-D14](#PR-D14) to [PR-D16](#PR-D16): a separate pass lock keeps
   two passes from interleaving, and each class's recheck reads what a launch can change
   while a pass runs.

3. ✅ <a id="OQ-PR3"></a>**[OQ-PR3](#OQ-PR3): Should every launch leave one
   line in a machine-wide log, so one reboot's storm can be read in one place?**
   Why it was asked, and each option in full:
   [a machine-wide launch log](#a-machine-wide-launch-log).

   - **A. Per-workspace only** (`launch.log`, `host-perf.log`).
   - **B. One line per launch in `~/.local/share/yolo-jail/logs/`** (UTC time,
     `paths.JailShortHash` of the container name, gate wait, attempts, outcome),
     beside `crossings.log`.
   - **C. B, but with workspace names.** Easier to read. A jail that mounts the
     log directory then learns the names of the user's other projects.
   - **D. Only when timing is on.** Off exactly when an unplanned reboot happens.

   _Leaning:_ B. The host check needs this record to be read at all. What it adds
   for a jail that mounts the log directory is every other workspace's launch
   times and outcomes under stable hashes, never which project a hash is.

   <!-- vantage: question id=OQ-PR3 -->

   **Answer:**
   > **Ruled 2026-09-29, as leaned: B.** One line per launch in `~/.local/share/yolo-jail/logs/`,
   > refused or not, keyed by the short workspace code the helper log already uses. The
   > maintainer: *"I don't see why we're worried about a jail getting access to that log. It's
   > just like anything else on the host, it's outside of their view. So if there's something
   > useful to write there, yes, write it there. It'll be an interesting place to sort of
   > passively gather paths that we can use for researching the machine … and the future agents
   > debugging things."*

   **Built 2026-09-29** as [PR-D17](#PR-D17): `~/.local/share/yolo-jail/logs/launches.log`,
   one line per launch, written when its fate is known.

## Decision Ledger

Implementation decisions made in this doc. Each one yields to a ruling on the
questions above. Three standing rules bind the rows that cite them: a launch has
no quiet mode ([OQ-RO3](../reference/report-tiers.md#why-its-this-way)); "could
not ask the runtime" is never a yes (the tri-state rule in
[AGENTS.md](../../AGENTS.md#architecture)); and an escape hatch is for broken
user config, never for a yolo bug
([attach-skew-and-contract-guardrails.md](attach-skew-and-contract-guardrails.md#decision-ledger)).

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="PR-D1"></a>PR-D1 | *Implementation decision.* No yolo readiness lock of any scope. Podman's `alive.lck` already serializes the refresh, so a yolo lock would only queue each waiter's cheap verification behind it. Revisit only on a measured storm in which concurrent probes lengthen recovery | 2026-09-29 | [Mechanism](#why-a-timed-out-probe-is-not-a-failed-podman), [Alternatives](#alternatives-and-risks) | ✅ 2026-09-29 — nothing to build: no such lock exists |
| <a id="PR-D2"></a>PR-D2 | *Implementation decision.* An attempt is never killed. Its only bound is the remaining budget. Retries follow only an early exit | 2026-09-29 | [The gate](#the-gate) | ✅ 2026-09-29 — `runtime.WaitForPodman`; `TestAPodmanStillRunningAtTheBudgetIsNamedAndLeftRunning` |
| <a id="PR-D3"></a>PR-D3 | *Implementation decision.* The probe's stdout and stderr go to unlinked temp files, not pipes, and the probe runs in its own process group (`Setpgid`). A surviving descendant cannot hold `Wait`, closing an output end cannot SIGPIPE a refresh mid-way, and a terminal Ctrl-C does not reach Podman. At expiry or on an interrupt, yolo stops waiting and names the pid | 2026-09-29 | [The gate](#the-gate) | ✅ 2026-09-29 — `runtime.RunPodmanAttempt`, `run.readyInterrupt`; `TestTheAttemptReturnsWhenTheChildExitsThoughAGrandchildHoldsItsOutput`, `TestAnAttemptThatOutlivesTheDeadlineIsLeftRunning`, `TestAnInterruptStopsTheWaitNotTheAttempt`, and `TestARealCtrlCDuringTheWaitStopsTheWaitAndExitsWith130`, which sends the process a real `SIGINT` |
| <a id="PR-D4"></a>PR-D4 | *Implementation decision.* Every early exit is retryable, after a backoff of 1, 2, then 4 s (capped at 4 s) that never runs past the budget. A missing or unstartable binary fails at once. **Amended by the [OQ-PR1](#OQ-PR1) ruling:** stderr now classifies, and an answer that cannot clear on its own refuses at once ([PR-D13](#PR-D13)); this row's backoff governs every other early exit. **Amended again at review:** a start error is classified by its errno, and only one that cannot clear fails at once ([PR-D23](#PR-D23)); a retry must also fit its retry floor ([PR-D22](#PR-D22)) | 2026-09-29 | [The gate](#the-gate) | ✅ 2026-09-29 — `runtime.podmanReadyBackoff`; `TestTheGateRetriesAnEarlyExitUntilTheLastAttemptTheBudgetAllows` |
| <a id="PR-D5"></a>PR-D5 | *Implementation decision.* The gate's probe is `podman info --format json`, and its parsed answer is the launch's one Podman facts record. `hostLoopbackFactsFor`, `AutoLoadOptions.StoreFacts` and `yolo check`'s image-delivery section read it, and `podman.facts` is noted from it. One `info` per launch, pinned by a whole-launch test | 2026-09-29 | [What the gate's answer replaces](#what-the-gates-answer-replaces) | ✅ 2026-09-29 — `run.acceptPodmanFacts`, `run.storeFactsFromGate`, `image.ParsePodmanStoreFacts`; `TestALaunchAsksPodmanOnceAndEveryReaderTakesTheGatesAnswer` |
| <a id="PR-D6"></a>PR-D6 | *Implementation decision.* `yolo check` calls the same gate with the same budget and the same progress line, so check and the launch cannot disagree about whether Podman is up. `check/probes.go`'s copy of the probe, which already differs by discarding stderr, is deleted. A shorter budget for check would bring back the disagreement this removes | 2026-09-29 | [The gate](#the-gate) | ✅ 2026-09-29 — `check.podmanGate`; `TestCheckAsksTheReadinessGateOnceForEveryReader`, `TestChecksRuntimeResolutionReadsTheGate`. At review the Disk I/O priority section still ran its own `podman info` for the storage root, through `Exec` with a 10 s kill; it now reads `store.graphRoot` from the gate's answer (`check.podmanGraphRoot`; every `section_iopriority_test.go` case fails on an `info` through `Exec`) |
| <a id="PR-D7"></a>PR-D7 | *Implementation decision.* Linux only. Apple Container and macOS Podman Machine keep the one-shot probe, with an explicit macOS arm pinned by a test. To `podman info`, a stopped VM and a starting VM look alike, and a wait would charge every stopped-VM user the budget | 2026-09-29 | [Verdict](#verdict-and-boundary) | ✅ 2026-09-29 — `usesReadinessGate` in `run` and `check`; `TestMacOSKeepsTheOneShotProbe`, `TestCheckOnMacOSNeverAsksTheGate` |
| <a id="PR-D8"></a>PR-D8 | *Implementation decision.* The attach decision's `ps` becomes tri-state, with a deadline, and "could not ask" refuses the launch rather than launching fresh (the tri-state rule). The other `findRunningContainer` callers are unchanged | 2026-09-29 | [What the gate's answer replaces](#what-the-gates-answer-replaces) | ✅ 2026-09-29 — `run.probeRunningContainer`; `TestAnAttachDecisionThatCannotAskRefuses` |
| <a id="PR-D9"></a>PR-D9 | *Implementation decision.* The wait, retry and success lines go on the launch stream through `Line.Set` and `Line.Println`. The refusal stays on `o.Stdout` with the other runtime-selection refusals. Neither can be hidden ([OQ-RO3](../reference/report-tiers.md#why-its-this-way)) | 2026-09-29 | [What the user sees](#what-the-user-sees-and-what-is-recorded) | ✅ 2026-09-29 — `runtime.WaitForPodmanShowing`, `runtime.ReadyResult.Refusal`; `TestEveryEarlyExitIsPrintedAndTheRefusalCarriesTheLast` |
| <a id="PR-D10"></a>PR-D10 | *Implementation decision.* Perf events: the `runtime.ready` span, `runtime.ready.attempt` notes, and `podman.facts` from the gate. All are written on the failure path too | 2026-09-29 | [What is recorded](#what-the-user-sees-and-what-is-recorded) | ✅ 2026-09-29 — `run.waitForPodman`; `TestTheGateRecordsItsSpanAndAttemptsOnARefusal`, `TestPodmanFactsAreRecordedFromTheReadinessGatesAnswer` |
| <a id="PR-D11"></a>PR-D11 | *Implementation decision.* The budget is a constant: no config key and no `YOLO_*` dial (the hatch rule) | 2026-09-29 | [The gate](#the-gate) | ✅ 2026-09-29 — `runtime.PodmanReadyBudget` |
| <a id="PR-D12"></a>PR-D12 | *Implementation decision.* The budget is 60 s. It covers the inferred 22.5 s refresh with 2.5× margin, room for its retries, and the 44 s the whole restore storm took. Host-check step 1 re-measures the forced refresh, and this row changes if it runs longer. [OQ-PR1](#OQ-PR1) ruled it applies to every launch, never keyed on a boot | 2026-09-29 | [The gate](#the-gate) | ✅ 2026-09-29, built before host-check step 1 ran: the constant stands until that measurement moves it |
| <a id="PR-D13"></a>PR-D13 | *Implementation decision*, under [OQ-PR1](#OQ-PR1). An early exit is classified from podman's FATAL lines only — `Error: …`, and the bare lines its rootless C code prints before exiting — never its logrus lines, since a refresh logs an error for one container and still succeeds. The table (`runtime.podmanFailurePatterns`) is podman v6.1.2's own texts, with the source file each group comes from named beside it. **Refused at once, naming a fix:** a user namespace refused or disabled, `newuidmap`/`newgidmap` broken or missing, a subuid mapping error, BoltDB removed or left behind, any message asking for `podman system migrate`, a database configuration mismatch, a `containers.conf` or `storage.conf` that does not parse or validate, a missing OCI runtime or conmon, a permission error, an unknown flag. **Retried:** a locked database, `EAGAIN`/`EBUSY`/`EINTR`/`ETXTBSY`, a timeout, and everything the table does not know (the tri-state rule). "No space left on device" and "cannot connect" are left out on purpose: yolo's own scratch remover may be freeing space, and a socket can come up late at boot. A transient line wins over a permanent one in the same answer | 2026-09-29 | [The gate](#the-gate) | ✅ 2026-09-29 — `runtime.ClassifyPodmanFailure`; `TestClassifyPodmanFailure`, `TestAnAnswerThatCannotClearRefusesAtOnceNamingTheFix`, `TestAnUnrecognizedErrorIsRetriedWithinTheBudget` |
| <a id="PR-D14"></a>PR-D14 | *Implementation decision*, under [OQ-PR2](#OQ-PR2). Two locks. `housekeeping-pass.lock` is taken non-blocking for the whole pass, so a second pass still skips rather than waits and two passes never interleave. The shared `housekeeping.lock` is taken blocking around each deletion through a `prune.Guard`, and released with `LOCK_UN` between deletions. A pass from a yolo older than this knows only the shared lock and holds it for its whole pass. With mixed versions two passes CAN interleave: an old pass skips while a new one is mid-deletion, but between a new pass's deletions it finds the shared lock free and runs whole, and the new pass's next deletion waits for it. Their deletions never overlap, and each of the new pass's later deletions rechecks after the old pass is done ([PR-D15](#PR-D15)); only while an upgrade is under way does a launch's re-inspect again wait a whole old pass. *Corrected at review:* this row said mixed versions never interleave. Keeping them apart would mean holding the shared lock for the whole pass, which [OQ-PR2](#OQ-PR2) ruled out. The manual `yolo prune` takes neither lock and passes no guard, unchanged | 2026-09-29 | [OQ-PR2](#OQ-PR2) | ✅ 2026-09-29 — `run.withHousekeepingPass`, `run.deletionGuard`; `TestTwoPassesNeverInterleave`, `TestAPassHoldsTheSharedLockOnlyAroundEachDeletion`, `TestAPassSkipsWhileAnotherPassRuns`, `TestADeletionWaitsForALaunchsReinspectAndRechecksAfterIt` |
| <a id="PR-D15"></a>PR-D15 | *Implementation decision*, under [OQ-PR2](#OQ-PR2). Each class rechecks under the shared lock, right before each deletion, only what a launch can change while a pass runs. **Images:** the current-image pointers, and the load sentinel a launch records under the same lock after its re-inspect, so an image recorded since the pass began is kept; a container created since is `rmi`'s own refusal. **Store outputs:** their roots. **Cache, image tars, delivery directories:** a fresh stat against the listing's own test. **Agent staging:** its age floor, which staging restamps, and its tracking file. **Retired loophole state:** still present and still older than the newest kept. **Flake-bundle generations:** still not the one the stable link names, and still past the grace period. None asks the runtime again per item | 2026-09-29 | [OQ-PR2](#OQ-PR2) | ✅ 2026-09-29 — the `…Guarded` reapers in `internal/prune`, `flakebundle.ReapGuarded`; `internal/prune/guard_test.go`, `TestAGuardedReapKeepsAGenerationActivatedDuringThePass`, `TestEveryClassDeletesUnderTheGuard`, and, since review showed a nil guard and an empty tracking directory passing that text match, `TestTheSmallClassesDeleteThroughTheGuardAndRecheckTracking` and `TestTheImageTarClassDeletesThroughTheGuardAndRechecks`, which run the classes over a store a launch changes while a deletion waits |
| <a id="PR-D16"></a>PR-D16 | *Implementation decision*, under [OQ-PR2](#OQ-PR2). The scratch-volume class deletes nothing in the slot: it starts the detached remover, which removes each volume only once podman says no container references it, and takes no housekeeping lock, since nothing on the load side reads a volume. The per-deletion lock has no deletion to bracket there | 2026-09-29 | [OQ-PR2](#OQ-PR2) | ✅ 2026-09-29 — `reapScratchVolumes` unchanged, outside the guard by design |
| <a id="PR-D17"></a>PR-D17 | *Implementation decision*, under [OQ-PR3](#OQ-PR3). The file is `~/.local/share/yolo-jail/logs/launches.log`, mode `0600`, beside `crossings.log`, rotated to one `.1` generation past 1 MiB under one `flock` with the write, taken on a sibling `launches.log.lock` that is never rotated (*corrected at review:* a lock on the log's own descriptor locks the inode being renamed, and two launches at the boundary deleted the archived generation). One line per launch, refused or not, the three guards `Run` runs before any other work included (*corrected at review:* the record was armed below them), written once, the moment its fate is known: `<start, UTC> launch jail=<paths.JailShortHash> runtime=<rt> podman_wait=<s> tries=<n> outcome=<started, attached, not-started or interrupted> rc=<n> after=<s>`, with `-` for a field the launch has no value for. `started` means the runtime was spawned (or the macos-user backend handed the launch), written before the container wait. The time is the launch's start, which is what orders a storm. *Corrected at a later review:* a signal after pack staging ends a container launch through its launch guard, the signal handler that covers it until its keeper's or its attach's takes over ([JL-D75](jail-lifetime-last-session-wins.md#JL-D75)), whose exit skips `Run`'s deferred record, so that launch left no line. The guard's exit now writes it first, as `interrupted` with the exit code, 128 plus the signal's number. The same line is written for an outer launch when a launch it runs in the same process, such as a capture jail's, ends the process on a signal | 2026-09-29 | [OQ-PR3](#OQ-PR3) | ✅ 2026-09-29 — `run.recordLaunchOutcome`, `run.appendLaunchLine`; `internal/cli/run/launchrecord_test.go`, including `TestAStartedLaunchWritesItsLineWhileTheJailRuns`, `TestALaunchRefusedByAnEarliestGuardLeavesItsLine` and `TestConcurrentLaunchesAtTheRotationBoundaryLoseNothing`; the guard's exit by `run.interruptedArmExit`, pinned by `TestALaunchASignalEndsAfterItsPackStagingLeavesItsLine` and `TestALaunchEndedByTheLaunchInsideItLeavesItsLine` |
| <a id="PR-D18"></a>PR-D18 | *Implementation decision*, under [PR-D8](#PR-D8). The attach decision's deadline is 30 s: it runs right after the gate heard podman answer, so it is not waiting out a refresh, only another launch's container create. Apple Container's `container ls` takes the same tri-state. The refusal names the runtime and the jail, says why launching fresh would be wrong, and names the command that lists the runtime's running containers (`container ls` on Apple Container) | 2026-09-29 | [What the gate's answer replaces](#what-the-gates-answer-replaces) | ✅ 2026-09-29 — `run.attachProbeTimeout`, `run.runningListCommand`; `TestAppleContainersAttachProbeKeepsTheTriState`, `TestTheAttachRefusalNamesEachRuntimesListCommand` |
| <a id="PR-D19"></a>PR-D19 | *Implementation decision*, under [PR-D6](#PR-D6). `yolo check` asks the gate once and keeps the answer; its progress line and retry lines go to stderr, because the report on stdout may be JSON; the Container Runtime section's failure carries the start hint first and the gate's refusal after it. check catches no Ctrl-C: interrupting it ends it as before, and the probe, in its own process group, runs on | 2026-09-29 | [The gate](#the-gate) | ✅ 2026-09-29 — `check.podmanGate`; `TestTheRuntimeSectionCarriesTheGatesRefusal` |
| <a id="PR-D20"></a>PR-D20 | *Implementation decision.* The gate's parts are seams (`Options.PodmanReadiness` on the launch and on check: the attempt runner, its clock, its sleep and its interrupt), never `Exec`, whose kill at a timeout is what the gate exists to avoid. Each test package's `TestMain` makes the real attempt runner refuse, so a unit test that reaches the gate without a fake fails loudly instead of running the machine's podman. Its start error wraps `exec.ErrNotFound`, so the refusal comes at once and never waits out the budget ([PR-D23](#PR-D23)) | 2026-09-29 | [Testing](#testing-and-the-real-host-check) | ✅ 2026-09-29 — `refusingPodmanAttempt` in `internal/cli/run` and `internal/cli/check` |
| <a id="PR-D21"></a>PR-D21 | *Implementation decision.* The gate's elapsed time is the larger of its clock and the backoffs it slept, so a clock that does not move, such as a test's frozen seam, still ends the loop at the budget | 2026-09-29 | [The gate](#the-gate) | ✅ 2026-09-29 — `TestAFrozenClockStillEndsTheGate` |
| <a id="PR-D22"></a>PR-D22 | *Implementation decision*, made at review. A retry starts only if the budget left after its backoff is longer than the **retry floor**: how long podman took to give the answer being retried, capped at 5 s. Without it, a podman failing the same way in 3 s got a tenth attempt with 2 s left, which the gate then stopped waiting on mid-answer, leaving a podman running for nothing and a refusal that named it instead of podman's error. The cap keeps an error that waited behind a lock, whose next attempt may answer at once, from costing more than 5 s of the budget. A refusal whose last attempt was still running also gives the last error an earlier attempt returned | 2026-09-29 | [The gate](#the-gate) | ✅ 2026-09-29 — `runtime.retryFloor`, `runtime.ReadyResult.Refusal`; `TestTheGateStartsNoRetryItWouldHaveToAbandon`, `TestARefusalBehindARunningAttemptCarriesTheLastErrorPodmanGave` |
| <a id="PR-D23"></a>PR-D23 | *Implementation decision*, under [OQ-PR1](#OQ-PR1), made at review. An attempt that cannot start is classified by its errno, not its words. Refused at once, naming the fix: no binary (`ENOENT`, `ENOTDIR`, exec's not-found and relative-path refusals), one this user may not execute (`EACCES`, `EPERM`), a file that is not a program (`ENOEXEC`, `EISDIR`). Retried like an early exit: `ETXTBSY` (a binary being replaced by an upgrade), `EAGAIN`, `ENOMEM`, `EMFILE`, `ENFILE`, and every errno the list does not name. A failure of yolo's own scratch files is a `ProbeScratchError`, named as yolo's, refused only when the temporary directory is missing, not a directory, not writable or read-only. Every start error used to refuse at once with "check that it is installed and on PATH" | 2026-09-29 | [The gate](#the-gate) | ✅ 2026-09-29 — `runtime.ClassifyStartError`, `runtime.ProbeScratchError`; `TestClassifyStartError`, `TestAStartErrorThatCanClearIsRetried`, `TestARefusalAfterRetriedStartErrorsCarriesTheStartError`, `TestAScratchFileYoloCannotCreateIsNamedAsYolos`, and on Linux `TestABinaryBusyBeingWrittenIsRetriedNotRefused` on a real subprocess |
| [OQ-PR1](#OQ-PR1) | **Maintainer ruling:** the 60 s budget applies to every launch and `yolo check`, never keyed on a reboot; answers that cannot clear on their own fail at once | 2026-09-29 | [The gate](#the-gate) | ✅ 2026-09-29 — the budget always; the fail-fast rule is [PR-D13](#PR-D13) |
| [OQ-PR3](#OQ-PR3) | **Maintainer ruling:** B; one machine-wide line per launch, keyed by the workspace code | 2026-09-29 | [OQ-PR3](#OQ-PR3) | ✅ 2026-09-29 — [PR-D17](#PR-D17) |
| [OQ-PR2](#OQ-PR2) | **Maintainer ruling:** the housekeeping slot holds the lock one deletion at a time; no skip, no load inference | 2026-09-29 | [OQ-PR2](#OQ-PR2) | ✅ 2026-09-29 — [PR-D14](#PR-D14) to [PR-D16](#PR-D16) |

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
| ~14:34:19.9Z | Second `podman info`: the image copy's rootlessness check (`image.PodmanRootlessness`, no deadline), inside the `image.layer_copy` span (14:34:19.922Z to 14:34:21.362Z); its own duration is not timed. `launch.log` prints its answer as the `podman unshare` store-write note | `host-perf.log`; `launch.log` |
| 14:34:21.5Z | Third `podman info` (`assemble.host_loopback_probe`) takes 0.144 s; `podman.facts version=6.1.2 … rootless=true network=pasta` | `host-perf.log` |
| 14:34:24–14:34:40Z | Housekeeping slot, 16.095 s: 7 images removed, 97 store outputs, 27 flake-bundle generations, and 4 leftover scratch volumes of the gone `yolo-swarf-2954fcc7` jail (removed in the background in 0.3 s) | `host-perf.log`; `/workspace/.yolo/housekeeping.log` |
| 14:34:24–14:34:52Z | First crossings of six jails: `ae3a2fd4` (yolo-jail), `391dc586` (dotfiles), `f96ed222`, `f7d01a63` (forms), `cd67013c` (vantage), `dba4428e` (waykeeper; again at 14:35:10Z) | `crossings.log`. The IDs are `paths.JailShortHash`; two were checked by hand (`yolo-yolo-jail-887995ca`, `yolo-waykeeper-a3f2a09f`) |
| 14:34:40Z | 12 unattributed connections to the Claude broker, and 9 each to aws-auth and openai-auth-broker. A single launch makes about 3 per service (compare 15:59:17Z) | broker logs |
| 14:39:24Z | A seventh jail, `f2d82aaf`, first reaches a front | `crossings.log` |
| 14:39:24Z–15:59:11Z | No crossing on any front | `crossings.log` |
| 15:59:14Z | The yolo-jail jail from 14:34 exits, after `launch.run_with_proxy dur=5091.691s` | `host-perf.log` |

**Stall measurements on this host.** `launch.log` records a `podman run --rm`
client that stayed 31.9 s in `unlinkat` and another 8.0 s in `getdents64`.
`scratchremoval.go` records the same 2026-09-28 measurement, and it is why scratch
volumes are now named and removed outside the client. That was one client
deleting its own anonymous volumes, not a Podman lock other clients waited on,
so it does not size the readiness budget. `housekeeping.log` records
scratch removals of 14.4 s, 17.4 s and 16.4 s before this reboot.

**Not found anywhere readable here:** the refused launch, its 22.5 s line, its
stderr, the 10:49:14 journal events, and the 0.529 s query. `host-service-journal.log`
was last written 2026-09-28 16:00:47Z, so no jail queried the host journal that
morning. The swarf workspace (`8259b056`) uses loopholes (its last crossings are
at 2026-09-28 16:00:48Z) and never reached a front after the boot, so it is a
candidate for the failing launch. But the REPORTED successful launch at 10:50:00
local left no crossing and no broker connection, so it was neither swarf nor any
other workspace that uses loopholes, unless the reported times are off.

**Sources outside this repository.** containers/podman `main`:
`libpod/runtime.go` (`makeRuntime`'s `alive.lck` / `alive` / `refresh()`
sequence), `pkg/rootless/rootless_linux.go` (`BecomeRootInUserNS` waits and
forwards catchable signals) and `pkg/rootless/rootless_linux.c`
(`reexec_in_user_namespace` sets no parent-death signal;
`reexec_userns_join` sets SIGTERM). Go `os/exec`: with `WaitDelay` zero, `Wait`
reads pipes until EOF, which may not come until orphaned descendants close them.
