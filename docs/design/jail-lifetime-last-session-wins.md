---
title: "A shared jail should end with its last session, not its first"
date: 2026-09-29
status: in-review
tags: [design, lifecycle, attach, sessions, host-services, teardown, keeper, podman, apple-container, herdr]
summary: "Several agents can share one workspace's jail, but the jail ends when the FIRST session's agent quits, because that agent is the container's main process and the first launcher's process hosts every host service. The maintainer directed the owner on 2026-09-29: a small background process, never a first terminal that waits. So pid 1 becomes a hold process, every session enters by exec, a host-side kernel lock counts sessions, and a keeper per running container jail, spawned by the fresh launch before any host service or the container exists, owns the jail's host services and tears the jail down when the lock says the last session is gone or the runtime says the container is. The first terminal gets its prompt back when its agent quits, and re-entering from it is an ordinary attach. OQ-JL5 was ruled on 2026-09-29: a keeper at every notch that starts a long-lived host service or sidecar, if supportable. §9.9 designs it for yolo host and macos-user: one keeper per workspace per notch; a yolo host launch that has one stays resident instead of exec'ing, because an inherited lock descriptor was measured to miscount both ways; and macos-user's keeper cannot own what runs as the sandbox account, because it cannot run sudo. OQ-JL6, OQ-JL7 and OQ-JL8 were ruled too: no linger; a killed keeper's sessions run on and a new arrival is refused; an agent ends with its pane. One question remains: whether yolo host's keeper also holds the services a launch starts for its own agent."
vantage:
  status-chip: true
---

# A shared jail should end with its last session, not its first

**Status:** DESIGN, 2026-09-29. **Steps 1 and 2 of [§7](#7-what-i-would-build-in-order) are
built** (each entry there names the code and the tests that pin it). Step 1, 2026-09-30: an
attach whose terminal closes ends its own session's processes in the jail and nothing else, no
key sequence detaches a session's client, and a session whose jail ends under it says why
([JL-D51](#JL-D51) to [JL-D54](#JL-D54)). Step 2: the hold as the container's main
process, every session by exec, provisioning's recorded outcome, and the session lock the orphan
reaper honors. Until the keeper
(step 3) exists, the first terminal still ends the jail with its own session
([JL-D46](#JL-D46)), so quitting it still ends every other session. [OQ-JL1](#OQ-JL1) was
directed by the maintainer on 2026-09-29, and the design it produced is
[§9](#9-the-keeper-design-2026-09-29); [OQ-JL5](#OQ-JL5) to [OQ-JL8](#OQ-JL8) were ruled the
same day, and [§9.9](#99-the-keeper-at-yolo-host-and-macos-user) designs the keeper at
`yolo host` and macos-user; one question remains. Every citation, and the keeper research with
its measurements, was verified against `232e4dcd`. The citations the review of 2026-09-29 added
(JL-D28 to JL-D35) were verified against `d3c6970a`, and [§9.9](#99-the-keeper-at-yolo-host-and-macos-user)'s citations and
measurements (JL-D36 to JL-D43) against `30b65282`. A review of [§9.9](#99-the-keeper-at-yolo-host-and-macos-user) the same day corrected its
`yolo host` re-entry answer, which re-asks [OQ-JL9](#OQ-JL9), and added JL-D44 and JL-D45; those
citations were verified against `303e0367`.

> **In short.** "Last one out turns off the lights" needs one small background process per
> running container jail, and no central watcher. The **keeper** owns the jail's host services
> from the jail's first moment, so no terminal is ever the owner: the first terminal gets its
> prompt back when its agent quits, re-entering from it is an ordinary attach, and the keeper
> ends itself when the kernel says the last session is gone or the runtime says the container is.
> Since [OQ-JL5](#OQ-JL5), a workspace's sessions at `yolo host` and at macos-user get one too,
> whenever a launch there starts something they share ([§9.9](#99-the-keeper-at-yolo-host-and-macos-user)).

**Why it matters.** Today two agents in one workspace share one container. When the first
agent quits, the second one dies mid-task, with no message
([§2](#2-what-ties-a-jail-to-its-first-terminal-today)).

**The shape.** A hold process as the container's pid 1; every session, the first included,
entering by `exec`; a host-side session lock the kernel keeps; and a keeper per running jail
that holds the jail's host services and tears the jail down when that lock comes free.

**Cost.** Medium-large, not terrible. The fresh-launch arm splits at the service boundary: the
code that starts and stops the host services moves into the keeper unchanged, and the first
session's boot output, exit code and quit-time instrumentation come from new places.

**Start at [§9](#9-the-keeper-design-2026-09-29)**, the keeper: what starts it, what it owns,
how the first terminal gets its prompt back, how re-entering works, how it ends itself, what it
discloses, its signals, and each notch. [§2.4](#24-everything-the-first-terminals-process-owns-today)
is what it takes over, and [§5](#5-how-terrible-is-it) answers "how terrible is this".

**Needs your ruling** ([OQ-JL5](#OQ-JL5) was ruled A, a keeper at every notch if supportable, and
[OQ-JL6](#OQ-JL6), [OQ-JL7](#OQ-JL7) and [OQ-JL8](#OQ-JL8) A, all 2026-09-29):

- [OQ-JL9](#OQ-JL9): at `yolo host`, does the keeper also hold what one launch starts for its own
  agent: the bridge's host half, which no other launch uses, and the Codex refresh adapter, whose
  managed home every host Codex launch on the machine already shares.

**Reads with:** [`jail-lifetime-last-session-wins-plan.md`](jail-lifetime-last-session-wins-plan.md)
(the implementation plan for step 3, the keeper at the container backends),
[`herdr-integration.md` §3.4](../research/herdr-integration.md#34-closing-a-pane-is-a-kill)
(what closing a pane does), and
[`central-yolo-watcher.md`](../research/central-yolo-watcher.md) (the sibling exploration of a
machine-wide watcher). [§12](#12-the-neighbors) lists the rest.

---

## 1. The verdict, and the words it uses

**Yes, without a central watcher, at medium-large cost.** The maintainer directed who owns the
jail on 2026-09-29 ([OQ-JL1](#OQ-JL1)): *"I don't want a solution where the first terminal
waits. The idea is to get my terminal back and reuse it. … I think it is going to have to be
some sort of small background process. But then it needs to know how to end itself as well."*
That process is the keeper, started with the jail rather than handed the jail later
([JL-D14](#JL-D14)).

- **Nothing new exists while no jail runs.** There is one keeper per running container jail,
  one per workspace at each of macos-user and `yolo host` while a launch there holds something
  long-lived ([§9.9](#99-the-keeper-at-yolo-host-and-macos-user)), and none otherwise.
- **Nothing in the jail's transport changes.** The host services run today's code, in a
  different process.
- **The pieces have precedents:**
  - a kernel-flock liveness registry (the macos-user session dirs,
    [`servicessession.go`](../../internal/cli/run/servicessession.go));
  - a pid-1 hold (the refused-boot hold, [`hold.go`](../../internal/entrypoint/hold.go));
  - a descriptor whose EOF means "the launch is gone" (`launchservice.Lifeline`,
    [`launchservice.go`](../../internal/launchservice/launchservice.go));
  - a detached self-exec helper that outlives its launcher (`startDetached`, which starts
    `yolo internal scratch-rm`, [`scratchremoval.go`](../../internal/cli/run/scratchremoval.go)),
    a weak precedent, because losing that helper costs nothing.

**Why the keeper must be born before the services, not handed them.** The first launcher's
process *is* the jail's host services today ([§2.4](#24-everything-the-first-terminals-process-owns-today)).
Go cannot fork, so nothing already running in that process can be moved into another one: a
front is a goroutine in the process that started it, and a fronted daemon can be reaped and
stopped only through the `exec.Cmd` its parent holds (`killServiceGroup`,
[`loopholesruntime.go`](../../internal/cli/run/loopholesruntime.go)). A process that should own
them for the jail's life has to start them itself.

### 1.1 Terms

- **Session.** One `yolo` invocation that runs a command in a container jail: its launcher
  process on the host, plus the process tree it started inside the jail. This extends the
  word's macos-user meaning (`paths.HostServicesSessionPrefix`, *"one macos-user invocation of
  yolo"*) to the container backends. There, several sessions can share one container. At
  macos-user and `yolo host` a session is one invocation at that notch, whose `yolo` process
  stays up for its command's life ([§9.9.2](#992-what-a-session-is-there-and-why-yolo-host-stays-resident)).
- **Owner** *(this doc's use)*. The one host process that holds a running jail's host services
  (the credential fronts, fronted daemons, cgroup delegate and port forwards) and runs its
  teardown. Today it is always the fresh launch's `yolo` process, and the owner-PID file
  (`writeOwnerPID`, [`lifecycle.go`](../../internal/cli/run/lifecycle.go)) names it. Under this
  design it is the keeper.
- **Keeper** *(coined here)*. A host process per running container jail, spawned once by the
  fresh launch before any host service or the container exists, that is the jail's owner for
  the jail's whole life and exits once the jail is known gone
  ([§9](#9-the-keeper-design-2026-09-29)). Since [OQ-JL5](#OQ-JL5), also a host process per
  workspace at each of macos-user and `yolo host`, spawned by the first launch there that holds
  something long-lived, and ending with that workspace's last session at that notch
  ([§9.9](#99-the-keeper-at-yolo-host-and-macos-user)). Its verb is
  `yolo internal daemon jail-keeper`, a hidden self-exec subcommand of `yolo`, as every host
  daemon is.
  - **It is not a supervisor.** It restarts nothing, and nothing restarts it.
  - **It is not a central watcher.** It serves one jail and holds nothing machine-wide.
  - **It is not a housekeeper.** It cleans up only what its own jail made
    ([JL-D23](#JL-D23)).
- **Hold process.** The container's main process, which does nothing but keep the container
  running. It has the shape of `hold.go`'s `holdContext`, which today is used only to keep a
  refused boot open for inspection.
- **Draining** *(coined here)*. The interval between the last session ending and the container
  being known gone.
- **Liveness lock** *(coined here)*. An exclusive kernel flock the keeper holds for its whole
  life, in host state no jail mounts. A free one means the keeper is gone, however it died. It is
  one file per container name that is never unlinked ([JL-D28](#JL-D28)). It is not the session
  lock ([§4.2](#42-the-count-a-host-side-session-lock)), which counts sessions.
- **E3 config capture.** The step at a jail's end that captures the edits made to the jail's
  composed config files, so that `yolo config diff` on the host shows them. "E3" is the tree's
  name for it; it lives in `captureConfigOnTerminate`
  ([`runcmd.go`](../../internal/cli/run/runcmd.go)). It records edits and applies none.
- **Window A.** The stretch of a shutdown after the container's pid 1 dies and before the
  `podman run` client exits, when podman tears down (conmon's exit file, the network, the
  unmounts) and no yolo code runs. The tree's name, from
  [`perf-logging.md`](../reference/perf-logging.md#window-a-attribution).
- **Unkept jail** *(coined here)*. A running container whose keeper is known dead. Its sessions
  still run, but its host services died with the keeper. What they then see was ruled in
  [OQ-JL7](#OQ-JL7): they run on, each told when it ends, and a new arrival is refused
  ([JL-D13](#JL-D13)).
- **Key** *(coined here)*. The name one keeper's locks, roster and log are keyed on: the
  container name at a container backend, and the workspace's container name with the notch
  appended at macos-user and at `yolo host`. The sessions of one key share one keeper
  ([§9.9.3](#993-one-keeper-per-workspace-per-notch)).
- **Roster** *(coined here)*. The `0600` file a keeper at macos-user or `yolo host` writes in host
  state no jail mounts once its services are up, naming what it runs, where, and behind which
  caller tokens, so that a later launch of its key can use them
  ([§9.9.4](#994-what-the-keeper-owns-there)).
- **Joining launch** *(coined here)*. At macos-user or `yolo host`, a launch of a key whose
  keeper is live. It is that notch's counterpart of an attach: it counts itself, reads the
  roster and starts nothing the keeper holds ([§9.9.5](#995-how-a-second-launch-joins)).
- **Notch.** The confinement level a launch runs at: `jail` (a container backend), `guest` (on
  a Mac, the macos-user account) or `host` (`yolo host`, no confinement). Coined in
  [`yolo-as-environment-manager.md` §4](yolo-as-environment-manager.md#4-confinement-a-dial-with-three-notches).
  It is not the backend: podman and Apple Container are two backends of one notch.
- **Pane close.** A terminal multiplexer closing one of its terminals. herdr, the one this doc
  measured against, signals every process in that terminal's session: SIGHUP, SIGTERM about
  250 ms later, and SIGKILL about 250 ms after that
  ([`herdr-integration.md` §2.6](../research/herdr-integration.md#26-restore-worktrees-automation-notifications-plugins-and-remotes)).
  An ordinary window close hangs up the same session, without the SIGKILL.
- **Lifeline.** A pipe whose write end only the launch holds, so the child reading it sees EOF
  the moment the launch dies, however it dies. It is `launchservice.Lifeline`'s mechanism,
  introduced by [HS-D11](host-notch-services.md#HS-D11).

### 1.2 Principles

- <a id="JL-P1"></a>**JL-P1. A shared jail lives while any session does.** The maintainer's
  "last one wins", 2026-09-29: *"make it the last one win so that you don't need to have the
  original one open."* That the jail then ends with its last session, and not some time after,
  is my reading rather than the maintainer's words. It is not a principle here: [JL-D12](#JL-D12)
  derives it from the E3 config capture, and [OQ-JL6](#OQ-JL6) asks whether it still holds for a
  tab that was the jail's only session.
- <a id="JL-P2"></a>**JL-P2. The count is the host's, and the kernel keeps it.** Nothing inside
  the jail can hold the jail open. A jail-visible counter would let a jail process keep the
  jail's host credential services alive after every human has left.
- <a id="JL-P3"></a>**JL-P3. "Could not count" is never zero.** If the keeper cannot tell
  whether sessions remain, it leaves the jail running and says so. This is the polarity the
  orphan reaper already takes (`pidAlive`: *"never reap a jail we can't prove is orphaned"*,
  [`lifecycle.go`](../../internal/cli/run/lifecycle.go)).
- <a id="JL-P4"></a>**JL-P4. One owner per jail, for the jail's whole life.** No handoff between
  processes. This is [HD-R1](host-daemon-ownership.md#HD-R1)'s ledger text, *"spawned by the
  launch that wants it and ends with that jail"*, and its failure-mode section, *"ends with its
  jail"* ([mode 7](host-daemon-ownership.md#mode-7-it-runs-settings-the-config-no-longer-says)).
  Both name the jail, not the launch process, as the unit. SOURCED. A keeper started with the
  jail satisfies it literally; a successor handed the jail later does not ([JL-D14](#JL-D14)).

---

## 2. What ties a jail to its first terminal today

Six rows. Rows 1, 2 and 3 are independent causes: each on its own ties the other sessions to
the first terminal. Row 5 becomes one the moment the first launcher is gone, which is what any
fix that lets that terminal leave produces. Rows 4 and 6 are not causes but constraints on a
fix, and row 6 follows from row 3.

| # | Coupling | Evidence | Kind |
|---|---|---|---|
| 1 | **The first agent is the container's main process.** The run flags are `--rm -i --init --read-only --name <cname>`. The command tail is `bash -c '<provision>; <mise activate>; <banner>; <target>'`, entered through `yolo-entrypoint`, which execs bash. When the agent exits, bash exits, the init's child exits, and the container stops | `runFlags` in `assembleRunCmd` ([`assemble.go`](../../internal/cli/run/assemble.go)), the `finalInternalCmd` append in `runContainer` ([`run.go`](../../internal/cli/run/run.go)), `buildFinalInternalCmd` ([`command.go`](../../internal/cli/run/command.go)), `execBash` ([`boot.go`](../../internal/entrypoint/boot.go)) | SOURCED |
| 2 | **When pid 1 exits, every exec session dies.** An `exec -d sleep 61` was gone from the container and the host once pid 1 exited. This is the kernel's rule: when a pid namespace's init dies, every other process in it gets SIGKILL | Research lens, 2026-09-29: nested podman 5.8.7, a probe container from `localhost/yolo-jail:latest` with its entrypoint overridden. [pid_namespaces(7)](https://man7.org/linux/man-pages/man7/pid_namespaces.7.html) | MEASURED |
| 3 | **The first launcher's process hosts the jail's host services.** Each loophole's front is a goroutine in it, and so is the cgroup delegate. Fronted daemons are Setsid children whose stop handles only it holds. The socat port forwards are its plain children, with no Setsid | the comment above `stopLoopholes` (*"a goroutine in THIS process"*), `startExternalService`'s Setsid and `frontRun` ([`loopholesruntime.go`](../../internal/cli/run/loopholesruntime.go)); `startCgroupDelegateInProc` ([`cgddaemon_linux.go`](../../internal/cli/run/cgddaemon_linux.go)); `startHostPortForwarding` ([`network.go`](../../internal/cli/run/network.go)) | SOURCED |
| 4 | **An attach will not heal them.** *"A jail whose launcher is gone is relaunched, not attached-and-repaired"* | the *"nothing to heal here"* comment in `attachExisting`, above its exec ([`run.go`](../../internal/cli/run/run.go)) | SOURCED |
| 5 | **The owner-PID reaper.** Every launch runs `reapOrphanedJails` before its attach decision. That call stops any live `yolo-*` container whose recorded owner PID is dead. So a first launcher that simply left would have its jail stopped by the next `yolo` in *any* workspace. It is a no-op on Apple Container | `runContainer`'s reap and its `writeOwnerPID` (fresh path only) ([`run.go`](../../internal/cli/run/run.go)), `reapOrphanedJails` ([`lifecycle.go`](../../internal/cli/run/lifecycle.go)) | SOURCED |
| 6 | **Teardown runs only in that process.** The chain (`teardownAfterExit`, and the signal arm `onTerminate`) needs values only that launch holds: the socat handles, the daemon stop functions, the scratch-volume launch id, the home skeleton's name and the pack tree | `onTerminate` and `teardownAfterExit` ([`run.go`](../../internal/cli/run/run.go)); [`trackingcleanup.go`](../../internal/cli/run/trackingcleanup.go) (*"this launch is the only one that knows its name"*) | SOURCED |

Rows 1 and 2 are the container coupling. Rows 3 to 6 are the host coupling, and they are what
the keeper takes over. Row 1 describes the tree before step 2 of [§7](#7-what-i-would-build-in-order),
which made pid 1 a hold; the coupling it names now lives on only as
[JL-D46](#JL-D46), until the keeper.

> [!NOTE]
> **Pressing Ctrl-C in the first agent does not run the launcher's teardown.** Since the
> 2026-09-19 ruling, the TTY proxy forwards `0x03` into the jail as a byte
> ([`ttyproxy.go`](../../internal/ttyproxy/ttyproxy.go), `proxyLoop`). So "I Ctrl-C'd the first
> agent" means the agent chose to exit, which is coupling 1. Only a real SIGINT, SIGHUP or
> SIGTERM delivered to the launcher runs `onTerminate`: a window close, a pane close, or `kill`.
> That arm runs `stopJail` (`podman stop -t 5`) first, on purpose, and so ends every session a
> second way. SOURCED.

### 2.1 What already works for any process

Parts of the teardown already key on evidence rather than on which process runs them. The
keeper can call these unchanged:

- **`stopLoopholes`' directory removal and `forgetGoneContainer`'s tracking-file removal.** Each
  takes the workspace launch lock non-blocking, then asks the tri-state `ps -a` probe whether
  the container still exists. `reapOrphanedJails` already calls `stopLoopholes(nil, …)` from a
  *different* process ([`lifecycle.go`](../../internal/cli/run/lifecycle.go),
  [`loopholesruntime.go`](../../internal/cli/run/loopholesruntime.go)). SOURCED.
- **The E3 config capture.** It is idempotent and takes only the workspace and the runtime
  (`captureConfigOnTerminate`, [`runcmd.go`](../../internal/cli/run/runcmd.go)). SOURCED.
- **The scratch-volume remover.** It is detached, and it waits until podman says no container
  references the volumes, so it does not depend on who started it (`startScratchRemoval` and
  `startDetached`, [`scratchremoval.go`](../../internal/cli/run/scratchremoval.go)). SOURCED.
- **The endpoint file.** In-jail clients re-read it on every dial. The code calls this *"the ONLY
  channel that can update an already-running container"*
  ([`svcendpoint/dial.go`](../../internal/svcendpoint/dial.go)). SOURCED.
- **The in-jail daemon supervisor.** It already has container lifetime. A later exec's
  `yolo-entrypoint` reuses the live supervisor rather than stacking a second one
  (`startJailDaemonSupervisor`, `anyJailDaemonSupervisorAlive` in
  [`entrypoint/runtime.go`](../../internal/entrypoint/runtime.go)). SOURCED.

### 2.2 Which backends have the problem

| Backend | Shared jail? | Has the defect? | Why |
|---|---|---|---|
| podman on Linux | yes | **yes** | rows 1, 2, 3 and 5 |
| podman on macOS (machine) | yes | **yes** | the same causes. The launcher also has **no signal arm at all**: [`proxy_other.go`](../../internal/cli/run/proxy_other.go) ignores `onTerminate`, so a window close skips teardown entirely |
| Apple Container | yes | **yes** | rows 1 to 3, and no signal arm. The owner-PID reaper is a no-op here (row 5 does not apply), so an orphan is never reaped |
| macos-user | **no** | **yes, in another form**: a second session breaks a running Codex's refreshes, and an agent started later in the first session's shell names the second's listeners ([§9.9.1](#991-where-the-re-entry-problem-is-real)) | *"This backend has no attach — every macos-user invocation is a fresh sandbox"* (the macos-user arm of `Run`, [`run.go`](../../internal/cli/run/run.go)). Since [HD-D1](host-daemon-ownership.md#HD-D1), each session also has its own host-services dir ([`servicessession.go`](../../internal/cli/run/servicessession.go)) |
| `yolo host` | no | not today: the one case, the Codex adapter's shared home, was fixed by a shared token ([§9.9.1](#991-where-the-re-entry-problem-is-real)) | each launch owns its services as its own children ([OQ-HS3](host-notch-services.md#OQ-HS3)), but every `yolo host -- codex` on the machine runs on one managed Codex home ([NC-D18](../plans/notch-convergence.md#NC-D18)) |

So "last one wins" means making the container backends behave the way the other two notches
already do: no session's end takes another session's services with it. That holds at those two
notches only in part. At macos-user a second session rewrites the workspace's per-agent env files
and Codex's `auth.json` with its own listeners and tokens, which end with it. At `yolo host` it
holds because of a fix made for one case: every host Codex launch on the machine shares one
managed Codex home, and a shared caller token with a lock of its own keeps one launch from
breaking another's refreshes ([NC-D18](../plans/notch-convergence.md#NC-D18)). And at both notches
the sidecar design shares a sidecar, a ping box and a master per workspace. That is
why [OQ-JL5](#OQ-JL5) gives them a keeper too ([§9.9.1](#991-where-the-re-entry-problem-is-real)).

### 2.3 Four defects found on the way

These exist today, independent of this design. Each should be fixed whatever else lands.

1. **Closing an attach terminal probably leaves its agent running headless.** Killing a
   `podman exec` client does not end the process it started. This held for SIGKILL, SIGHUP,
   SIGTERM and SIGINT to an `exec -it` client: conmon keeps the session and its pty, and
   `ExecIDs` kept listing it. The attach passes no `onTerminate` (`attachExisting`'s
   `runWithProxy(runCmd, nil, nil, o)`, [`run.go`](../../internal/cli/run/run.go)), so after a
   window close the proxy SIGKILLs only its client.
   - The client behavior was MEASURED by the research lens on 2026-09-29, with `sleep` under a
     pty harness, and again under a pane-close sequence this revision ran
     ([§3.1](#31-what-a-pane-close-does-measured)).
   - That the same happens to a real agent is INFERRED.
   - **Fixed 2026-09-30** for an attach, as [OQ-JL8](#OQ-JL8) ruled ([JL-D51](#JL-D51),
     [JL-D52](#JL-D52)): its signal arm hangs up the session's own processes in the jail before it
     kills the client, and never stops the jail. MEASURED in a nested jail: a hung-up attach's
     `sleep` was gone from the jail, which kept running with its first session
     (`TestAHungUpAttachEndsItsOwnSessionAndNoOther`). The first session's arm still stops the
     jail until the keeper ([JL-D46](#JL-D46)).
2. **podman's default detach sequence is live wherever the host's `containers.conf` leaves it
   at the default.** yolo sets no `--detach-keys` (`rg detach-keys internal/` finds nothing,
   SOURCED). podman's built-in default for `run -it` and `exec -it` is `ctrl-p,ctrl-q`
   (`podman run --help` and `podman exec --help`, podman 5.8.7 in this jail, MEASURED), and a
   host may override it in `containers.conf`
   ([podman-exec](https://docs.podman.io/en/latest/markdown/podman-exec.1.html), SOURCED).
   Typing that sequence in the first session detaches its client. The launcher would then tear
   down the host services under a container that keeps running. INFERRED from
   `teardownAfterExit`'s order. Passing `--detach-keys=""`, documented as disabling the
   feature, fixes it whatever the host's setting.
   - **Fixed 2026-09-30** ([JL-D27](#JL-D27), [JL-D54](#JL-D54)): every podman `run` and `exec`
     yolo issues into a jail passes `--detach-keys=`. MEASURED on podman 5.8.7 under a pty: with
     the default, `ctrl-p` then `ctrl-q` typed as two keystrokes made both a `run -it` and an
     `exec -it` client exit 0 with their process still running in the container; with the empty
     value the keystrokes reached the process. podman takes the sequence only when each key
     arrives in a read of its own, which is how a person types it.
3. **An attached session is never told why its jail ended.** After the exec returns, the attach
   prints only the broken-prefix post-mortem and the macOS OOM hint (`diagnoseBrokenPrefix` and
   `maybeWarnAboutOOMKiller` at the end of `attachExisting`). SOURCED.
   - **Fixed 2026-09-30** ([JL-D53](#JL-D53)): every stop yolo makes records why before it stops,
     and an attach whose jail ended under it prints the record, or says that nothing recorded
     why. MEASURED in a nested jail for the first session's quit, for `yolo stop`, and for a
     `podman stop` from outside yolo, which records nothing
     (`TestAnAttachWhoseJailEndedSaysWhy`).
4. **The orphan reaper already kills live sessions.** It exists because an uncatchable kill
   leaves the container running (*"Sweep jails orphaned by an uncatchable kill"*, above the reap
   in `runContainer`). When the first launcher is SIGKILLed, its `podman run` client dies but the
   container does not, so the first agent keeps running headless and any attached sessions keep
   running too. The next `yolo` in *any* workspace then stops all of them: `reapOrphanedJails`
   checks only the owner PID, and no session ([`lifecycle.go`](../../internal/cli/run/lifecycle.go)).
   The code is SOURCED; that the container outlives its client is INFERRED from the reaper's own
   premise. The fix is [JL-D7](#JL-D7)'s reaper rule.

### 2.4 Everything the first terminal's process owns today

Read from the code at `232e4dcd`. "Owns" means the thing exists in that process, is its child,
or is a record only it removes. The last three columns say what happens to it at each way the
launch ends. They are SOURCED, except the SIGKILL column, which is INFERRED from the same code,
and the cells marked MEASURED.

**A container jail** (the fresh arm, `runContainer` in [`run.go`](../../internal/cli/run/run.go)):

| What | How the process holds it | Its agent quits | Window or pane close | The launcher is SIGKILLed |
|---|---|---|---|---|
| The container's main process | the first agent is pid 1's child (row 1 of [§2](#2-what-ties-a-jail-to-its-first-terminal-today)) | exits, and every session dies with it | `stopJail` first, so every session dies | the container keeps running, headless |
| The `podman run -it` client | its child on the TTY proxy's pty, left in the launcher's process group on the host terminal (*"NO Setsid"*, [`ttyproxy.go`](../../internal/ttyproxy/ttyproxy.go)) | exits with the container; Window A is its lingering exit, recorded by the tree | gets the hangup itself and forwards it into pid 1 (MEASURED, [§3.1](#31-what-a-pane-close-does-measured)); the arm then SIGKILLs it | orphaned; the container stays |
| The TTY proxy | in-process: raw mode, the pty, `^Z`, SIGWINCH, the signal arm | restores the terminal | runs `onTerminate`, then `os.Exit(128+n)` | the terminal is left raw |
| The workspace launch lock | a flock (`holdLaunchLock`), released once the container is seen running (`onStarted`) | long released | long released | the kernel drops it |
| Port forwards | socat processes, plain children in its process group (`startHostPortForwarding`) | `cleanupPortForwarding` | hung up with the group (a plain child died in [§3.1](#31-what-a-pane-close-does-measured)), then cleaned up | orphaned, INFERRED |
| The cgroup delegate | a goroutine (`startCgroupDelegateInProc`) | stopped | stopped | dies with the process |
| Loophole fronts | goroutines (`frontRun`), each publishing an endpoint file into the host-services dir | closed, and the dir removed by `stopLoopholes`' guards | the same | die; their endpoint files name dead ports until a reaper's `stopLoopholes(nil, …)` |
| Fronted per-jail daemons | Setsid children whose stop handle only it holds (`startExternalService`, `killServiceGroup`) | group SIGTERM, 5 s, SIGKILL | the same | orphaned: the reaper removes their dir, not them, INFERRED from `stopLoopholes(nil, …)` |
| Host-wide singletons (the Claude broker and the other `scope: "host"` daemons) | ensured, not owned (`brokerEnsure`, `startHostSingleton`) | outlive it by design, until [HD-R1](host-daemon-ownership.md#HD-R1) is built | the same | the same |
| The credential-view registration | a record the broker keeps (`registerClaudeCredentialView`) | outlives it | outlives it | outlives it |
| Tracking file, owner-PID file, home skeleton, pack tree and its live-tree record | files only this launch knows the names of | removed once the container is known gone (`forgetGoneContainer`, `clearOwnerPID`) | the same | left for the reaper and `yolo prune` |
| Scratch volumes | their names, minted per launch (`newScratchLaunchID`) | handed to the detached remover | the same | left for the housekeeping slot's reap |
| The E3 config capture | a teardown step (`captureConfigOnTerminate`) | runs | runs | skipped |
| The launch log, the timing collector and the linger probe | in-process: the `launch.log` tee (`attachLaunchLog`), `host-perf.log`, Window A (`startLingerProbe`, `recordWindowA`) | recorded | recorded by the arm | cut off |
| The housekeeping slot | a goroutine started from `onStarted` (`runHousekeeping`) | dies at exit, restartable by design ([`housekeeping.go`](../../internal/cli/run/housekeeping.go)) | the same | the same |
| The terminal's jail indicator | the tmux border or kitty tab set by the front door, restored by `RestoreTerminal` | restored | restored inside the arm | left wearing the jail's colors |
| The embedded pack tree lease | a process-lifetime lease (`packload.ReleaseEmbedded`) | released | released by the arm | its fallback tree is left for a later sweep |

**macos-user** (the macos-user arm of `Run`, and `RunMacosUser` in
[`orchestrator.go`](../../internal/macosuser/orchestrator.go)) owns the same kinds of thing, but
**only its own session's**: the session's host-services dir and the flock on its `.session.lock`
(`openServicesSession`), the fronts and fronted daemons through the same `startLoopholesDisclosed`,
the doorways and launch-owned services as `launchservice` children (Setpgid, with a lifeline),
the sandbox's jail-daemon supervisor (`startJailDaemons`, Setpgid, stopped when the agent
exits), the sandboxed command itself (a plain child through `proxy_other.go`, with no signal
arm), and the launch log. When it ends, by any route, nothing another session uses goes with
it: the `launchservice` children end on their lifeline's EOF even after a SIGKILL, and the next
session's sweep collects a dead session's dir ([HD-D1](host-daemon-ownership.md#HD-D1)). The
Setsid fronted daemons are orphaned by a SIGKILL there too, INFERRED. SOURCED otherwise.

**`yolo host`** execs the agent when nothing needs owning (`hostSyscallExec`,
[`host.go`](../../internal/cli/host.go)), so no yolo process remains. When a launch-owned service
or the managed Codex adapter is needed, it stays resident as their parent
(`launchservice.RunAgent`, [`agent.go`](../../internal/launchservice/agent.go)): Setpgid
children with a lifeline, stopped when the agent exits. Two host launches share the host-wide
brokers, and every `yolo host -- codex` on the machine, in any workspace, also shares one managed
Codex home (`host-agents/<pack>` under the machine-wide store, `prepare` in
[`host.go`](../../internal/openaiauthhost/host.go)): its `auth.json`, one caller token, and a
shared lock that counts the home's live launches (`sharedCallerToken`,
[NC-D18](../plans/notch-convergence.md#NC-D18)). SOURCED.

---

## 3. The options

One more fact is needed to read the table. The design needs **a container that outlives the
first session**. The simplest way is that pid 1 is something other than an agent, and every
agent session enters by `exec`, the first included (coupling 2).

- **One variant was considered and rejected.** pid 1 could be a subreaper that runs the *first*
  agent as its own child and outlives it, with later sessions entering by exec. The first
  terminal would keep `podman run -it`'s attach, and its client would be detached
  programmatically when its agent exits, by sending the detach sequence. That keeps today's
  first-session exit code path, but it makes the first session different from every other one
  (two exit-code paths, two argvs), it depends on the very detach sequence
  [§2.3](#23-four-defects-found-on-the-way) item 2 wants disabled, and it leaves a `podman run`
  client in the first terminal's pane, which a pane close turns into a stopped jail
  ([§3.1](#31-what-a-pane-close-does-measured)). INFERRED, and MEASURED for the last part.
- **What an exec costs is not yet measured.** An exec round trip of `/bin/true` on an idle
  container costs **56 to 71 ms** (research lens, 3 × `podman exec /bin/true`, MEASURED). A
  session's exec does more than that: it runs `/opt/yolo-jail/bin/yolo-entrypoint <target>`
  (`attachExisting`'s argv), and `entrypoint.Main` runs the whole boot step table before
  `execBash` (`runBootSteps`, [`bootsteps.go`](../../internal/entrypoint/bootsteps.go)). *"The entrypoint
  re-runs every generator on each attach"*
  ([`../reference/composed-file-permissions.md`](../reference/composed-file-permissions.md)).
  SOURCED. So under this design the first session pays a second generator pass after pid 1's
  boot, as every attach already does. The number owed is `podman exec <cname>
  /opt/yolo-jail/bin/yolo-entrypoint true` on a booted jail ([§8](#8-what-done-looks-like)).
  Skipping the table for an exec into a booted jail would be an entrypoint contract change, and
  is not proposed here.

So the options below differ only in **who owns the host services and who runs the teardown**.

| | Option | How | Cost | What breaks or stays broken | Verdict |
|---|---|---|---|---|---|
| **O0** | **Legibility only** | Keep "the jail lives with the first terminal". When sessions remain, the quitting terminal says *"ending this jail ended N other sessions"*. Record a stop reason that an attach reads after its exec ends | Small | Not what was asked. The first terminal still owns every session | **Its pieces go into the design** ([§2.3](#23-four-defects-found-on-the-way)), not as the answer |
| **O1** | **Hold the door, and survive the hangup** | The first launcher stays the owner. When its own agent exits with sessions remaining, it **holds** the jail: it hands the terminal back to cooked mode, says so, and waits for the session count to reach zero, surviving a window close the way `nohup` does | Small to medium. No new process kind | The first terminal's prompt stays occupied while others run. Its launcher cannot outlive a pane close: it leads its own process group, so it cannot leave the pane's session ([setsid(2)](https://man7.org/linux/man-pages/man2/setsid.2.html) fails with `EPERM` for a group leader), and herdr SIGKILLs it within 0.5 s | **Rejected by the ruling** ([OQ-JL1](#OQ-JL1)): *"I don't want a solution where the first terminal waits"* |
| **O2** | **Successor at quit** | The first launcher spawns a detached successor only when it quits with sessions remaining. The successor re-fronts the running daemons, re-publishes their endpoint files, respawns socat and rebinds the cgroup delegate, then tears down when the count reaches zero | Medium-large. A single-session launch costs nothing extra | A **handoff**, which [JL-P4](#JL-P4) forbids. The path runs only in the multi-session case, so it rots. In-flight connections drop at the handoff. Re-published fronts are never checked by the boot-time reachability witness. Under a pane close the handoff must finish inside herdr's 0.5 s budget with no launcher left to wait on it | **Rejected** ([JL-D14](#JL-D14)) |
| **O3** | **Keeper from the start** | The fresh launch does its pre-flights, approvals, build, staging and disclosures in the terminal as today, then spawns the keeper. The keeper starts the host services and the container, relays boot progress until the jail is ready, and runs the teardown chain in-process when the count reaches zero or the container ends. The first terminal is then an ordinary session | Medium-large. A heavy-track change to the fresh arm | Every container launch pays one process spawn and a pipe. The fresh arm's output stream, exit code and Window A accounting move ([§5](#5-how-terrible-is-it)). A keeper that dies leaves an **unkept jail** ([JL-D13](#JL-D13)) | **Chosen** ([OQ-JL1](#OQ-JL1), [JL-D14](#JL-D14)) |
| **O4** | **Migrate among live launchers** | O1's container, plus an owner election over a kernel flock. When the owner leaves, one attached launcher wins and rebuilds the host services from the running jail's records | Medium-large. No detached process at all | A handoff again, with attach skew becoming ownership skew: the winner's binary and config may differ from the jail's. It needs a record of caller tokens and daemon process groups and a guarantee that only one front exists at a time, which the attach arm's comment forbids breaking (*"starting a second front over the same endpoint file would hand the jail a credential its terminator never asked for"*). The gap is total on SIGKILL | Rejected. It spreads one jail's ownership across whichever terminals happen to be open |
| **O5** | **Per-session host services** | Each session owns the services its own agent uses, as macos-user already does | Large | The genuinely jail-wide services remain: port forwards bound at fixed mounted socket paths, the cgroup delegate, and in-jail daemons that serve every session. Those still need an owner | Rejected as a replacement. Worth revisiting later to shrink what the keeper holds |
| **O6** | **podman's own lifetime features** | `podman pod create --exit-policy=stop` stops a pod *"when the last container exits"* ([podman-pod-create](https://docs.podman.io/en/latest/markdown/podman-pod-create.1.html), SOURCED). That would mean one container per session sharing a pod's namespaces. `--restart` conflicts with `--rm` (research lens, MEASURED). Quadlet and healthcheck timers are systemd | Large, and backend-specific | Pods count containers, not exec sessions. Apple Container has no pods. None of these owns the host services | Rejected |
| **O7** | **The container counts its own sessions** | pid 1 exits after the last session, counting either per-session `--preserve-fd` death pipes or locks in a jail-visible directory | Medium | It solves nothing on the host: the services still need an owner. `podman exec --preserve-fds=N` passes descriptors with no runtime restriction in its documentation (the list form `--preserve-fd` is documented as crun-only), and neither is available with the remote Podman client, which is every Mac ([podman-exec](https://docs.podman.io/en/latest/markdown/podman-exec.1.html), SOURCED; both flags in `podman exec --help`, 5.8.7, MEASURED). A jail-visible count breaks [JL-P2](#JL-P2) | Rejected as the count. The death pipe is kept as an optional refinement on local Linux podman ([JL-D4](#JL-D4)) |

### 3.1 What a pane close does, measured

MEASURED 2026-09-29, in this jail's nested rootful podman 5.8.7, with a Python pty standing in
for a pane. A "launcher" shell in the pane started a setsid'd child (the keeper's stand-in), a
plain child (socat's stand-in), and a runtime client in the foreground. The probe then did what
herdr does: it listed the pane session's processes once, sent them SIGHUP, SIGTERM 250 ms later
and SIGKILL 250 ms after that, and closed the pty master. Every container ran
`localhost/yolo-jail:latest` with its entrypoint replaced by a bash that logs each HUP, INT or
TERM it receives and exits on TERM.

| Shape in the pane | The setsid'd child | The plain child | The container | What pid 1 logged |
|---|---|---|---|---|
| **The keeper's shape:** the container started outside the pane (`run -d --rm --init`); the pane runs `podman exec -it` | survived | died | **kept running**; the exec'd process ran on headless | nothing |
| **Today's fresh launch:** the pane runs an attached `podman run -it --rm --init` | survived | died | **stopped and removed** | (gone with the container) |
| The same, with `--sig-proxy=false` | survived | died | **kept running** | nothing |

What that settles:

- **A process in its own session survives a pane close,** and it is the only kind that does.
  A plain child of the launcher, which is what the socat forwards are today, is signalled with
  the pane. SOURCED for herdr's side (`shutdown_pane_processes` takes its list once, and a
  process that called `setsid` is in another session,
  [`herdr-integration.md` §2.6](../research/herdr-integration.md#26-restore-worktrees-automation-notifications-plugins-and-remotes)).
- **podman's signal proxy alone stops a jail whose run client sits in the pane.** The probe had
  no launcher signal arm, so no `stopJail` ran: the attached client forwarded what it received
  into pid 1 (`--sig-proxy`, *"Proxy received signals to the process (default true)"*,
  `podman run --help`), and that was enough. `--sig-proxy=false` stops it, and a container with
  no attached client in the pane receives nothing. `podman exec` has no signal proxy to disable.
- **`-d` and `--sig-proxy=false` combine without error:** `podman run -d --rm --sig-proxy=false`
  reached the OCI runtime (MEASURED).
- **The hold must not die of a stray hangup anyway.** Today's `holdContext` cancels on SIGINT
  and SIGTERM only, and a Go program with no handler for SIGHUP exits on it
  ([os/signal](https://pkg.go.dev/os/signal#hdr-Default_behavior_of_signals_in_Go_programs)).
  With `--init`, catatonit is pid 1 and the entrypoint its child (`hold.go`'s comment on
  `holdReleaseFile`), and a signal podman forwards reaches that child: the forwarded HUP in
  [`herdr-integration.md` §3.4](../research/herdr-integration.md#34-closing-a-pane-is-a-kill)
  did. SOURCED and MEASURED there.

What it does not settle: a rootless host, where the pane's processes, conmon and the keeper sit
in systemd cgroup scopes that a nested jail does not reproduce ([§8](#8-what-done-looks-like)).

---

## 4. The proposed shape

The container half ([§4.1](#41-the-container-a-hold-process-as-pid-1-and-every-session-an-exec))
and the count ([§4.2](#42-the-count-a-host-side-session-lock)) make a jail that can outlive any
one session. The keeper makes its host services outlive any one session too; everything about
it is [§9](#9-the-keeper-design-2026-09-29). This section is the container, the count, and what
a user sees.

```mermaid
sequenceDiagram
    participant T1 as Terminal 1 (fresh launch)
    participant K as Keeper
    participant C as Container (pid 1 = hold)
    participant T2 as Terminal 2 (attach)
    T1->>T1: pre-flights, build, staging, disclosures (the keeper named)
    T1->>T1: launch lock, no old keeper alive, take session lock SHARED
    T1->>K: spawn in its own session, with plan, progress pipe, lifeline and the launch lock
    K->>K: into its own scope, liveness lock, owner-PID file
    K->>K: host services, socat, cgroup delegate
    K->>C: start the container (attached non-tty client, no runtime client in any pane)
    K->>K: container running: release the launch lock
    C-->>K: boot output, then boot done
    K-->>T1: boot lines, then ready
    T1->>C: exec agent 1 (provisioning runs first, on this tty)
    T2->>T2: launch lock, container running, take session lock SHARED
    T2->>C: exec agent 2 (after provisioning's recorded outcome)
    T1-->>T1: agent 1 quits: drop lock, one line, prompt back
    T2-->>T2: agent 2 quits: lock drops, waits for the teardown
    K->>K: session lock EXCLUSIVE acquired = zero sessions (draining, held until exit)
    K->>C: stop
    K->>K: today's teardown chain, in-process
    K-->>T2: teardown lines, through the keeper's log (JL-D11, JL-D19)
    K->>K: release liveness lock, exit
```

### 4.1 The container: a hold process as pid 1, and every session an exec

- **pid 1 is the entrypoint's boot followed by a hold.** It runs today's boot, then blocks. It
  never runs an agent.
- **The boot stays in pid 1, not in the first session.** The jail-daemon supervisor stays in
  the process group of the entrypoint that starts it (`startJailDaemonSupervisor`: *"stay in the
  same process group as PID 1"*, [`entrypoint/runtime.go`](../../internal/entrypoint/runtime.go)),
  and a session's hangup ends that session's own process tree ([JL-D4](#JL-D4), as
  [OQ-JL8](#OQ-JL8) ruled). A supervisor started from the first session's exec would die
  with that session. SOURCED for the process
  group, INFERRED for the consequence.
- **The keeper starts it with an attached, non-tty client, and no runtime client ever sits in a
  pane** ([JL-D15](#JL-D15)). The keeper keeps `<rt> run --sig-proxy=false` attached and reads
  pid 1's output from that client's pipes, in order, so the boot lines, a refusal's text and the
  hold notice reach the terminal as they do today
  ([OQ-RO3](../reference/report-tiers.md#why-its-this-way)). A detached start would lose that
  output. Every podman run passes `--log-driver none` (`assembleRunCmd`,
  [`assemble.go`](../../internal/cli/run/assemble.go), SOURCED), so with `-d` pid 1's stdout and
  stderr go nowhere, and the keeper would have to relay them from the jail-writable
  `.yolo/boot.log` or change the log driver. Both shapes were MEASURED to leave pid 1 untouched
  by a pane close ([§3.1](#31-what-a-pane-close-does-measured),
  [§9.7](#97-signal-handling-sig-proxy-and-a-pane-close)).
- **The keeper builds its container argv without `-t`.** The plan's argv is computed in the
  fresh launch, where `IsTTYStdout` adds `-t` (`assembleRunCmd`, SOURCED), and the keeper's client
  has no terminal.
- **The hold ignores SIGHUP and SIGINT, and ends on SIGTERM**, which is what `podman stop`
  sends. Nothing in a session's lifecycle signals it; the keeper's drain and `yolo stop` end it
  through `podman stop` ([JL-D15](#JL-D15)). It ignores them through `signal.Notify` handlers that
  do nothing, never `signal.Ignore`: that sets `SIG_IGN`, which every process it starts afterward
  inherits across exec.
- **Only `provisionScript` leaves the command tail, and it goes to the first session, not to
  pid 1.** Today `buildFinalInternalCmd` puts three things before the target
  ([`command.go`](../../internal/cli/run/command.go)): `provisionScript`, `miseActivate` and
  the executing banner. SOURCED.
  - `miseActivate` and the banner are per-shell. `execBash` already redoes both for every exec
    (it sources `yolo-user-env.sh`, evals `mise env` and prints `executingLine`,
    [`boot.go`](../../internal/entrypoint/boot.go)), so they stay per session. SOURCED.
  - `provisionScript` runs once per container, and its failure branch asks *"Provisioning failed
    — continue anyway? [Y/n]"* only when `[ -t 0 ]`
    ([`provision.go`](../../internal/provision/provision.go)). SOURCED. pid 1 has no tty, so
    provisioning there would silently continue where a user at a terminal is asked today. So it
    runs inside the **first session's** exec, which has the first terminal's tty.
  - **Every other session waits for provisioning's recorded outcome, not for a done marker**
    ([JL-D33](#JL-D33)). A marker that only success writes leaves a waiter hanging whenever
    provisioning ends another way: `RefusedStatus` or an `n` answer, on both of which the script
    exits the shell (`provision.go`, SOURCED); the first pane closed mid-run, which JL-D4's hangup
    ends; or the first launcher SIGKILLed. The waiter holds its shared session lock, so the
    keeper never drains while it waits. And a waiter that got in after a refusal would enter the
    jail the refusal closed: the *"continue anyway"* that
    [OQ-AR3](../reference/agent-program-runtimes.md#oq-ar3) ruled out (the comment above the
    refusal test in [`provision.go`](../../internal/provision/provision.go)).
    - Provisioning runs under an in-jail flock and records one outcome: **done**, which includes
      a failure the first terminal chose to continue past, or **refused**, which is
      `RefusedStatus` or a failure the first terminal declined.
    - A free flock with no recorded outcome means provisioning was **abandoned**.
    - A waiter that reads *refused* is refused with the same reason, and the count then ends the
      jail. On *abandoned*, a waiter with a tty reruns provisioning on its own tty, and one
      without a tty is refused, since it would otherwise continue where a person is asked.
    - A waiter never waits while holding the launch lock, and its wait is bounded
      ([JL-D34](#JL-D34)).
- **Every session enters by exec.** This is today's attach argv, `<rt> exec -i [-t] <cname>
  /opt/yolo-jail/bin/yolo-entrypoint <target>` (`attachExisting`), for the first session too.
- **A session's exit code is its command's.** `yolo -- make` returns `make`'s code, as it does
  today. The macOS OOM hint (`maybeWarnAboutOOMKiller`, keyed on exit 137) is re-derived from
  the exec's code, since the container's own code is no longer the first session's. One case
  needs the hold's code as well: a jail stopped from outside has its sessions SIGKILLed by the
  kernel, so the first session reports 137 where the launch returned 143 before, and the hint
  would blame the VM for a stop ([JL-D50](#JL-D50)).
- **No exec starts before pid 1's boot and the first session's provisioning are done.** This
  includes an attach that races the first launch. Today such an attach can enter once the
  container is merely running (`onStarted` releases the lock after `awaitRunningContainer`). How
  pid 1 reports that its boot finished is the implementer's; the recorded outcome covers
  provisioning.
- **The refused-boot hold keeps working, and keeps its signals.** A boot that refuses under the
  hold opt-in (`hold.go`) is ended by its release file, by `yolo stop`, or by a SIGINT or SIGTERM
  sent to the entrypoint, which is what its notice tells the user (`holdNotice`: *"Sending
  SIGINT or SIGTERM to this entrypoint ends it too"*, SOURCED). So the pid-1 hold's rule of
  ignoring SIGINT does not reach the refused-boot hold; a change that let it would have to change
  that notice in the same commit. It is never ended by the session count
  ([§4.4](#44-failure-paths)).

### 4.2 The count: a host-side session lock

- **Each session holds a shared lock for its whole life.** Its launcher holds `LOCK_SH` on a
  host-only file keyed by the container name, in host state that no jail mounts. The kernel
  drops the lock however the launcher dies, SIGKILL included.
- **It is taken under the launch lock, before that lock is released.** There is therefore no
  instant at which a session is inside the jail but not counted. The fresh launch takes its
  lock before it spawns the keeper ([JL-D16](#JL-D16)), so the keeper can never see zero before
  the first session has begun. At a fresh launch the launch lock then passes to the keeper,
  which releases it once the container is seen running ([JL-D31](#JL-D31)).
- **The keeper waits for the exclusive lock.** It blocks on `LOCK_EX` on the same file.
  Acquiring it means zero sessions, and at that moment **the jail is draining**: every later
  `LOCK_SH|LOCK_NB` fails. flock(2) warns that converting a held lock *"is not guaranteed to be
  atomic: the existing lock is first removed"*, so no process ever upgrades a lock it holds.
- **The keeper holds `LOCK_EX` from the moment it drains until it exits** ([JL-D28](#JL-D28)).
  On observation 2 of [§9.5](#95-how-it-ends-itself) that take is the drain. On observation 3
  and on a SIGTERM it takes the lock after its chain, once each session whose exec ended has
  dropped its shared lock. A session always drops its own lock before it waits on the keeper. A
  session still holding on after [JL-D34](#JL-D34)'s bound is logged and left, and its stale
  lock can then only keep the next jail up longer, never end it sooner ([JL-P3](#JL-P3)).
- **Neither lock file is ever unlinked or renamed over** ([JL-D28](#JL-D28)). Each is one file
  per container name, so every process that opens it locks the same inode. A keeper that removed
  the session-lock file as one of its records would hand the next keeper a fresh inode, whose
  `LOCK_EX` succeeds at once under live sessions.
- **One shared file hides the count, and that is the layout.** With one file, a session learns
  it was last only after the fact. The alternative was `servicessession.go`'s registry shape:
  one file per session, each held `LOCK_EX` by its session. A quitting session could then probe
  its siblings with `LOCK_SH|LOCK_NB` before it releases its own, and know it is last. The
  registry costs a directory scan per count, and two sessions quitting at once can each see the
  other alive, so the keeper still decides. It also has no one file the keeper can hold
  exclusively from its drain to its exit, which [JL-D28](#JL-D28) needs. So the layout is one
  shared file; [JL-D11](#JL-D11) does not need a session to know beforehand.
- **An arrival checks for a keeper still ending the jail before it counts itself**
  ([JL-D28](#JL-D28)). After it takes the launch lock, an arrival that finds no running container
  probes the name's liveness lock. One that finds the container running but fails its
  `LOCK_SH|LOCK_NB` is arriving during a drain. In either case, while a keeper holds the liveness
  lock, the arrival **releases the launch lock**, prints that the previous jail is still shutting
  down, waits for the liveness lock, then takes the launch lock again and starts over
  ([JL-D12](#JL-D12)).
  - **Why the probe comes first.** On observation 3 of [§9.5](#95-how-it-ends-itself), the
    container is already gone while the keeper's chain still runs. An arrival that skipped the
    probe would take a shared lock nobody opposes and spawn a second keeper for the same name.
    The old chain would then run its path-keyed cleanup over the new jail and count the dying
    jail's sessions toward it.
  - **It must not wait while holding the launch lock.** The teardown's guards take that lock
    non-blocking and back off, so a waiting arrival that held it would make `stopLoopholes`
    leave the sockets dir (*"Another yolo invocation is launching …"*,
    [`loopholesruntime.go`](../../internal/cli/run/loopholesruntime.go)) and
    `forgetGoneContainer` leave the tracking file, skeleton and pack tree
    ([`trackingcleanup.go`](../../internal/cli/run/trackingcleanup.go)). SOURCED. That would
    turn the common "quit, then relaunch" into a leak, left for a reaper meant only for
    launches that *"died with no teardown at all"*.
- **Lock ordering, stated so it cannot deadlock.** An arrival takes the launch lock and then
  the session lock, and never waits on the keeper while holding the launch lock. The keeper
  **never blocks on the launch lock**: it holds it only as the fresh launch's handoff, from the
  spawn until the container is seen running or its start fails ([JL-D31](#JL-D31)), and its
  teardown's guards take it non-blocking. A relaunch that does hold it (the attach-skew restart,
  which holds it throughout on purpose: `restartJailForAttach` in
  [`contracttags.go`](../../internal/cli/run/contracttags.go)) keeps its own host-services dir,
  exactly as today.
- **`ExecIDs` is for display only.** `jailSessionCount` stays the number shown to a human
  ([`contracttags.go`](../../internal/cli/run/contracttags.go)). Its `+1` for "the main
  process" goes, because the main process is no longer a session. It never decides anything,
  because it also counts headless orphans ([§2.3](#23-four-defects-found-on-the-way) item 1),
  and Apple Container's value is unmeasured. Built at step 2, and pinned by
  `TestTheSessionCountLeavesOutAHoldMainProcess`: the `+1` stays only for a jail whose
  environment lacks `YOLO_JAIL_MAIN=hold`, one launched before step 2.

### 4.3 Lifecycle

```mermaid
stateDiagram-v2
    [*] --> Booting: fresh launch spawns the keeper
    Booting --> Running: boot + provisioning done
    Booting --> Gone: lifeline EOF / boot refused (no hold) / a service or the container failed to start
    Running --> Running: session arrives (LOCK_SH) / leaves (lock dropped)
    Running --> Draining: keeper takes LOCK_EX (zero sessions)
    Running --> Draining: container ended (yolo stop, OOM, crash)
    Running --> Unkept: keeper killed, sessions remain
    Draining --> Gone: stop, teardown chain, keeper exits
    Unkept --> Gone: the last session reaps it as it quits, or yolo stop does
```

### 4.4 Failure paths

| Event | What happens | Who finds out |
|---|---|---|
| Any session's launcher gets SIGHUP, SIGTERM or SIGINT (window close, pane close, `kill`), the first session's included | **Only that session ends.** Its signal arm never calls `stopJail`. It sends SIGHUP to its own in-jail process tree ([JL-D4](#JL-D4), as [OQ-JL8](#OQ-JL8) ruled), restores the terminal and exits. Its lock drops. Under herdr the arm has about 500 ms before the SIGKILL, and one exec round trip is 56 to 71 ms (MEASURED), so the hangup fits, INFERRED | nobody else. That is the fix |
| A session's launcher is SIGKILLed | Its lock drops, so the count is right. Its in-jail agent may keep running headless until the jail stops. On local Linux podman, the optional death pipe closes this gap | the next quit's display count may include it |
| The last session's launcher dies by any means | The keeper drains within one scheduling tick of the lock dropping | [JL-D11](#JL-D11): the teardown goes to the keeper's log and `launch.log`, and a last session still alive streams it |
| A herdr server restart | Every pane's launcher is signalled at once, every lock drops, and the keeper drains: no session is left, so the jail ends | the keeper's log. herdr brings the panes back as shells, and each `yolo` there launches fresh |
| The fresh launch dies before the jail is ready | The keeper sees the lifeline's EOF, releases the launch lock it was handed, stops what it started, removes its records and exits. Its writes to the progress pipe fail with `EPIPE` rather than killing it ([JL-D29](#JL-D29)) | the keeper's log |
| A host service or the container fails to start | The keeper releases the launch lock it was handed ([JL-D31](#JL-D31)), then stops what it started and removes its records, reports it over the progress pipe, and exits non-zero; the fresh launch prints it and exits non-zero, as today. The fresh launch learns of a keeper that died without reporting from its child's exit, not from the pipe's EOF ([JL-D29](#JL-D29)) | the launching terminal |
| Provisioning is refused, declined or abandoned | Its recorded outcome, or a free provisioning lock with none, tells every waiting session ([JL-D33](#JL-D33)): a refusal refuses them, and an abandoned run is rerun on a waiter's own tty or refused | each waiting terminal |
| The keeper cannot open the session lock | [JL-P3](#JL-P3): it never drains on the count. It waits only for the container's own end, and logs why | `yolo ps` and the next arrival, which says the jail will end only by `yolo stop` |
| The keeper cannot ask the runtime | Every runtime call it makes is bounded. It keeps holding and retries; it never treats the silence as "gone". Whoever is waiting on it gives up after a bound and says so, naming its pid, its log and what it is doing ([JL-D34](#JL-D34)) | the keeper's log, then the next session's quit or arrival ([JL-D19](#JL-D19)) |
| The keeper gets SIGTERM or SIGINT (a shutdown, a logout, `kill`) | It ends the jail in order: it records the reason, stops the container, runs the chain and exits ([JL-D24](#JL-D24)) | every session, which sees its exec end and prints the reason |
| The keeper is SIGKILLed or OOM-killed, or its scope is killed | **Unkept jail** ([§9.5](#95-how-it-ends-itself)). Its children end with it ([JL-D32](#JL-D32)). The sessions run on without host services and a new arrival is refused, as [OQ-JL7](#OQ-JL7) ruled, and the last session reaps the jail as it quits ([JL-D30](#JL-D30)) | each session as it quits, and the next arrival ([JL-D13](#JL-D13)) |
| An attach-skew restart from another terminal | It stops the jail holding the launch lock, as today, and spawns its own keeper only once the old keeper's liveness lock is free, as every fresh launch does ([JL-D26](#JL-D26), [JL-D28](#JL-D28)) | every session, which prints the recorded reason |
| A session arrives during a drain, or while the keeper runs its chain after the container ended | It finds no running container and a held liveness lock, or its `LOCK_SH\|LOCK_NB` fails. It releases the launch lock, prints that the jail is shutting down, waits for the keeper's liveness lock, then takes the launch lock again and starts over ([JL-D28](#JL-D28)) | that terminal |
| `--rm` fails to remove the container because a headless exec was live at stop | Removal was MEASURED to fail in nested podman (`openByHandleAt: operation not permitted`), leaving a stopped container. After its stop, the keeper confirms the container is gone and removes a stopped leftover. It never removes a running one | the keeper's log. A real rootless host is owed a measurement ([§8](#8-what-done-looks-like)) |
| A refused boot under the hold opt-in | The keeper does not drain on the count. It waits for the hold's release or `yolo stop`, then tears down | the launching terminal, as today |

### 4.5 What the user sees

- **A fresh launch looks as it does today, up to the agent,** with one more line naming the
  keeper ([JL-D21](#JL-D21)). The boot lines are relayed from the keeper and printed by the
  launch.
- **The first agent quitting while others run returns the prompt at once,** with one line.
  Suggested wording: *"jail `<cname>` stays up for N other sessions; `yolo -- <agent>` re-enters
  it, `yolo stop` ends them all."* Any non-last quit prints the same line.
- **The last quit** prints the teardown lines ([JL-D11](#JL-D11)).
- **Every session whose jail ended under it** prints the recorded stop reason (`yolo stop`, OOM,
  the keeper gone, an attach-skew restart from another terminal). The keeper writes the record,
  and it is also printed when a session is refused during a drain. Built at step 1 for the jail's
  owners before the keeper, each of which writes it before it stops the jail ([JL-D53](#JL-D53)).
- **A session's quit also prints what the keeper recorded while that session was in**, when
  there is anything ([JL-D19](#JL-D19)).
- **`ps` shows one `yolo internal daemon jail-keeper` per running container jail,** one per
  workspace at each of macos-user and `yolo host` while a launch there holds something
  long-lived ([§9.9](#99-the-keeper-at-yolo-host-and-macos-user)), and nothing once none runs.
- **`yolo stop` keeps its effect:** it ends every session and names the count
  (`jailSessionsPhrase`), and it returns once the keeper's teardown has finished, streaming it
  ([JL-D25](#JL-D25)). When no keeper holds the liveness lock and the recorded owner is dead, it
  runs the keeper's chain itself ([JL-D30](#JL-D30)), and its wait is bounded ([JL-D34](#JL-D34)). Its help text changes. Today `stopUsage` says *"a jail lives in the
  terminal that launched it, and exiting (or Ctrl-C) that session tears the jail down with you"*
  ([`stop.go`](../../internal/cli/stop.go)). That becomes: a jail lives while any session in it
  does.
- **`yolo config diff` in the first terminal while others still run shows the last capture,**
  because E3 runs at the jail's end. That is what a second terminal sees today while the jail
  runs.

---

## 5. How terrible is it

Plainly: **not terrible, and not small.** Medium-large, heavy-track, with no new transport and
no change to what the host services are.

| Area | Size | Why |
|---|---|---|
| pid 1 hold, first session by exec, provisioning on the first session's tty, readiness | small to medium | the hold shape exists (`hold.go`), and the exec argv exists (the attach arm) |
| The session lock, and the reaper honoring it | small | `servicessession.go` is the pattern, and the reaper gains one more tri-state input ([JL-D7](#JL-D7)) |
| Moving services and teardown into the keeper | **medium-large** | `runContainer` splits at the service boundary into a plan the launch computes and discloses, and a keeper that executes it. The code moves; it is not rewritten |
| The fresh arm's output stream | **medium** | the relay must keep every progress line the terminal shows today, in order ([OQ-RO3](../reference/report-tiers.md#why-its-this-way)) |
| Window A, the linger probe, the timing report | medium | they instrument the `podman run` client's exit, and that client moves to the keeper. They re-home to the keeper's log, or are retired by name |
| The keeper's own cgroup scope | small to medium | a direct spawn moved into a scope with `StartTransientUnit` over D-Bus, with a Setsid-only fallback ([JL-D5](#JL-D5)) |
| The keeper's descriptors, locks and bounds | small to medium | the launch-lock handoff, close-on-exec on every inherited descriptor, the progress writer, and a bound on every wait ([JL-D29](#JL-D29), [JL-D31](#JL-D31), [JL-D34](#JL-D34)) |
| Verification | **the real cost** | a nested jail verifies the lifecycle and the pane-close behavior ([§3.1](#31-what-a-pane-close-does-measured)). The two carve-outs (loopback reachability and rootless paths), the scope move, and both Mac backends need CI or a real host |

What is **not** on the list:

- **Rewriting the fronts.** The keeper runs today's code.
- **Changing the in-jail transport.** In-jail consumers re-read endpoint files per dial and
  bound their own ports at boot. One in-jail change is owed: `provisionScript` leaves the
  container's command tail for the first session's exec
  ([§4.1](#41-the-container-a-hold-process-as-pid-1-and-every-session-an-exec)).
- **A central process.**

### 5.1 How a keeper squares with the rulings

| Ruling | How the keeper meets it |
|---|---|
| [HD-R1](host-daemon-ownership.md#HD-R1): *"A host-side daemon is spawned by the launch that wants it and ends with that jail"* | Literally. The keeper is spawned by the launch that starts the jail, holds only that jail's services, and ends with the jail. It is one per jail, never machine-wide, so it is not the singleton HD-R1 retired |
| [YW-P3](../research/central-yolo-watcher.md#YW-P3): *"Every jail-facing listener, credential and grant stays owned by something whose life is bounded by a jail: a launch or a keeper"*, and [YW-D7](../research/central-yolo-watcher.md#YW-D7): HD-R1 already covers any resident host process | Named as an allowed owner by the sibling exploration. It holds only because the keeper's children end with it ([JL-D32](#JL-D32)): a socat or fronted daemon left behind by a SIGKILLed keeper would be a jail-facing listener with no owner |
| [OQ-HS3](host-notch-services.md#OQ-HS3): *"A host service lives for the launch that starts it"*; the alternative it rejected was a service that outlives launches, rendered into files, with a secret that outlives every launch | A keeper's services outlive the first terminal but not the jail, are never rendered into files, and use secrets minted for that jail. [OQ-JL5](#OQ-JL5) was ruled A, so for what a keeper holds HS3's "launch" is the keeper's key at every notch: the jail at a container backend, and the sessions of one workspace at macos-user or `yolo host`. For what a session keeps it reads literally ([§9.9.9](#999-what-changes-for-a-host-services-lifetime)) |
| [`minimal-disk-footprint.md`](minimal-disk-footprint.md)'s A6: a daemon *"adds a lifecycle yolo does not have"* | The ruling accepts one new lifecycle, per jail. The keeper sweeps nothing: it cleans up only its own jail's records ([JL-D23](#JL-D23)), and the housekeeping slot stays in the foreground launch ([JL-D10](#JL-D10)) |
| A launch has no quiet mode, and a disclosure is never suppressible ([OQ-RO3](../reference/report-tiers.md#why-its-this-way)) | Disclosures stay in the terminal before the spawn, the keeper is itself disclosed, and nothing it records after ready is only in a file ([JL-D19](#JL-D19), [JL-D21](#JL-D21)) |
| One code path per concern ([NC-D1](../plans/notch-convergence.md#7-decision-ledger)) | One start, disclosure and teardown path for host services at every notch. Under [OQ-JL5](#OQ-JL5)'s ruling the keeper runs it wherever a launch holds something long-lived, at every notch ([§9.9](#99-the-keeper-at-yolo-host-and-macos-user)) |

A machine-wide watcher would add, for this feature, only recovery from a keeper crash, and it
would bring back a singleton that HD-R1 retired. What a watcher would buy elsewhere is the
sibling doc's subject ([`central-yolo-watcher.md`](../research/central-yolo-watcher.md)).

| Risk | Mitigation |
|---|---|
| A keeper crash leaves live sessions without credentials | A free liveness lock beside a held session lock makes the state known rather than guessed. What the sessions and an arrival then see was ruled in [OQ-JL7](#OQ-JL7). The keeper holds no state a relaunch needs, and it sits in its own session and scope, where nothing a terminal does reaches it ([§9.7](#97-signal-handling-sig-proxy-and-a-pane-close)) |
| A single-session launch gets slower | Its session enters by exec, and that exec runs the boot table a second time after pid 1's ([§3](#3-the-options)). The `/bin/true` round trip is 56 to 71 ms (MEASURED); the entrypoint exec is owed a measurement ([§8](#8-what-done-looks-like)). Add one spawn and a pipe. Window A leaves the terminal, which may more than pay for it |
| Two yolo versions on one machine | An older `yolo` attaching to a kept jail takes no session lock, so it is uncounted and ends when the counted sessions do. A newer `yolo` meeting a pre-feature jail falls back to today's owner-PID rule ([JL-D7](#JL-D7)). The lock files outlive their keepers ([JL-D28](#JL-D28)), so a pre-feature jail is recognized not by a missing file but by a free liveness lock beside an owner-PID file whose process is alive, an older yolo's launcher; it is attached to as today, and never treated as unkept. The owner-PID file names the keeper, so an older reaper sees a live owner ([JL-D18](#JL-D18)) |
| The keeper's output is lost after its terminal is gone | Its own log, mirrored into `launch.log`, and the next session's quit or arrival prints what it recorded ([JL-D19](#JL-D19)) |
| The keeper runs a binary `just install` replaced | Linux spawns from the inode the launch started from; elsewhere the plan's build stamp refuses a mismatched keeper ([JL-D20](#JL-D20)) |

---

## 6. What this does not cover

- **A jail with zero sessions that stays up on purpose,** like a `tmux detach`.
  [JL-D12](#JL-D12) rules out a jail with no sessions staying up, because the last quit waits for
  the teardown ([JL-D11](#JL-D11)). A deliberately detached jail is the sibling watcher doc's
  territory. [OQ-JL6](#OQ-JL6) asks only about a short linger for switching agents.
- **Detaching from a running agent and coming back to it.** podman cannot re-attach to an exec
  session ([§9.4](#94-how-re-entering-works-and-changing-agents)); a terminal
  multiplexer is the tool for that.
- **Sharing one jail across different pack sets.** Attach semantics for a different pack set
  are [OQ-ACP1](../plans/agent-config-packs.md#-oq-acp1--what-happens-when-two-people-attach-to-the-same-jail-with-different-pack-sets).
  An attach reads the running jail's pack tree, as today.
- **Retiring the host-scoped singletons.** That is [HD-R1](host-daemon-ownership.md#HD-R1)'s
  build, with its own [OQ-HD10](host-daemon-ownership.md#OQ-HD10). The keeper holds each
  launch's front and per-jail daemons under either state of that retirement.
- **An agent started with a raw `podman exec`** outside yolo. It is not counted and may be
  stopped under its user. That is [OQ-HS3](host-notch-services.md#OQ-HS3)'s *"may lack features
  if not launched correctly"*, applied here.

---

## 7. What I would build, in order

1. **The four defects** ([§2.3](#23-four-defects-found-on-the-way)): an attach's signal arm
   ends its own in-jail process tree (the shape [OQ-JL8](#OQ-JL8) ruled);
   `--detach-keys` is emptied on podman's run and exec ([JL-D27](#JL-D27)); an attach
   whose jail ended prints why; and the reaper declines a jail whose session lock is held
   (which needs the session lock, so it lands with step 2). Each is useful today.
   - **Built** 2026-09-30, the first three (the fourth landed with step 2). An attach names its
     session in the jail and runs its exec under a signal arm of the launch arm's kind, whose
     teardown hangs that session up through the entrypoint's `--yolo-hangup-session` form
     ([`run/sessionhangup.go`](../../internal/cli/run/sessionhangup.go),
     [`entrypoint/sessionhangup.go`](../../internal/entrypoint/sessionhangup.go);
     [JL-D51](#JL-D51), [JL-D52](#JL-D52)). Every podman run and exec turns the detach sequence
     off ([`runtime/detachkeys.go`](../../internal/runtime/detachkeys.go); [JL-D27](#JL-D27),
     [JL-D54](#JL-D54)). Every stop records why, and an attach whose jail ended prints it
     ([`run/stopreason.go`](../../internal/cli/run/stopreason.go); [JL-D53](#JL-D53)). Pinned
     end to end by `TestAHungUpAttachEndsItsOwnSessionAndNoOther`,
     `TestTheDetachSequenceNeverLeavesAnAttachedSessionHeadless` and
     `TestAnAttachWhoseJailEndedSaysWhy`, each run in a nested jail and each failing with its
     call site removed. Not run: either Mac backend, where the attach now has the same arm
     (step 4 owes that run) and Apple Container has no detach-keys flag from yolo.
2. **The container half and the count.** The hold process as pid 1, every session by exec,
   provisioning on the first session's tty with its recorded outcome ([JL-D33](#JL-D33)),
   readiness, the hold's signal rules, and the session lock with the reaper honoring it. **It changes nothing a user can see on its own:** the first
   launcher is still the owner and still tears the jail down when its own session ends. That is
   why it can land first and be verified in a nested jail by itself.
   - **Built**: the entrypoint's half in
     [`entrypoint/jailmain.go`](../../internal/entrypoint/jailmain.go), the launcher's in
     [`run/jailmain.go`](../../internal/cli/run/jailmain.go) and
     [`run/sessionlock.go`](../../internal/cli/run/sessionlock.go), pinned end to end by
     `TestTheMainProcessIsAHoldAndTheFirstSessionAnExec` and
     `TestAnOrphanSweepSparesAJailWithASessionInIt`. Readiness is a line pid 1 prints on its
     stderr, which the launcher relays up to and swallows ([JL-D47](#JL-D47)). To keep the first
     terminal's behavior while no keeper exists, the hold also ends when the first session's
     process does ([JL-D46](#JL-D46)); step 3 removes that. The fourth defect of
     [§2.3](#23-four-defects-found-on-the-way) is fixed with it: the orphan reaper leaves a jail
     whose session lock is held, and an arrival at such a jail is warned that its launcher is
     gone ([JL-D48](#JL-D48)). Three more keep what the first terminal did before: one signal
     arm covers the whole launch, the first session's exec included and the wait for the main
     process's client after it ([JL-D49](#JL-D49)); a jail stopped from outside still returns
     143 ([JL-D50](#JL-D50)); and `boot.log` stays pid 1's boot, a session's pass going to a log
     of its own ([JL-D47](#JL-D47)).
3. **The keeper.** Split `runContainer` into the plan the launch computes and discloses and the
   keeper that executes it, as `yolo internal daemon jail-keeper`, a hidden subcommand as
   AGENTS.md requires, not a binary. Build the progress relay, the lifeline, the liveness lock,
   the launch-lock handoff ([JL-D31](#JL-D31)), the descriptor rules ([JL-D29](#JL-D29)), the
   scope move, the keeper's log, its disclosure line ([JL-D21](#JL-D21)) and its signal rules
   ([JL-D24](#JL-D24)). Give its children an end of their own ([JL-D32](#JL-D32)), make every
   fresh launch wait for an old keeper ([JL-D28](#JL-D28)), bound every wait on it
   ([JL-D34](#JL-D34)), make `yolo stop` wait for its teardown or run it
   ([JL-D25](#JL-D25), [JL-D30](#JL-D30)), and move the owner-PID file onto the keeper. **This is
   the step that fixes the symptom:** quitting the first agent no longer ends the others, and
   the first terminal gets its prompt back.
4. **The Mac backends.** Add the session signal arm [`proxy_other.go`](../../internal/cli/run/proxy_other.go)
   lacks, measure `container exec`'s client-death behavior and the detach keys on Apple
   Container, and run the lifecycle on both Mac runners.
5. **The keeper at macos-user and `yolo host`** ([§9.9](#99-the-keeper-at-yolo-host-and-macos-user)),
   after step 3, whose keeper it reuses with a key per notch. At macos-user it takes over what
   the launch starts outside the sandbox, which fixes the per-agent env files of
   [§9.9.1](#991-where-the-re-entry-problem-is-real) on its own. At `yolo host` it has something
   to hold only once [`agent-event-watchers.md`](agent-event-watchers.md)'s host-side sidecars
   or ping box land (its [§10](agent-event-watchers.md#10-what-i-would-build-in-order) step 6),
   unless [OQ-JL9](#OQ-JL9) is ruled B or C. The resident host session ([JL-D36](#JL-D36)) lands with
   it, and never before: a host launch with no keeper keeps exec'ing. So does the macos-user
   session launcher's signal arm ([§9.9.6](#996-what-the-first-terminal-sees-and-how-the-keeper-ends)),
   without which a Ctrl-C could drop a session's lock while its sandbox runs on.

---

## 8. What done looks like

Each of these is observable by a human:

1. **Two sessions, quit the first.** Two terminals in one workspace run agents. Quitting the
   first agent returns its prompt at once, with the one line of [§4.5](#45-what-the-user-sees),
   and the second keeps working. The second's credential refresh (a broker front request) still
   succeeds afterwards.
2. **Re-enter from the first terminal.** In that same terminal, `yolo -- <another agent>`
   attaches to the running jail beside the second session.
3. **Close the first window, and close a herdr pane.** Either leaves every other session
   unaffected, and `ps` still shows the keeper. As [OQ-JL8](#OQ-JL8) ruled, the closed
   pane's agent is gone from the jail's own `ps` too.
4. **Quit the last.** The last quit shows the teardown ([JL-D11](#JL-D11)). Afterwards `podman
   ps -a` lists no container of the name, `yolo config diff` reflects that session's edits, and
   no keeper process remains.
5. **Scripts and single sessions.**
   - `yolo -- true` exits 0 and leaves no process and no container behind.
   - `yolo -- false` exits 1.
   - A single-session launch's quit time is within noise of today's, by the records.
   - `podman exec <cname> /opt/yolo-jail/bin/yolo-entrypoint true` on a booted jail is measured,
     so the second generator pass a first session now pays has a number ([§3](#3-the-options)).
6. **`kill -9` the last session's launcher.** The jail drains within a second, and the keeper's
   log records it.
7. **`kill -9` the keeper while a session runs.** As [OQ-JL7](#OQ-JL7) ruled, the
   session keeps running, the next arrival is refused as [JL-D13](#JL-D13) says, and no launch in
   any workspace reaps the jail. No socat or fronted daemon of that jail outlives the keeper
   ([JL-D32](#JL-D32)). When the surviving session quits, it reaps the jail itself
   ([JL-D30](#JL-D30)), on Apple Container too: no container, keeper child or
   `/tmp/yolo-fwd-<cname>` remains, and `yolo config diff` reflects its edits.
8. **`kill` the keeper (SIGTERM) while sessions run.** The jail ends in order, every session
   prints the recorded reason, and no container or keeper remains ([JL-D24](#JL-D24)).
9. **`kill -9` the fresh launch during boot.** The keeper stops what it started and exits; no
   container of the name remains.
10. **The upgrade day.** On a machine with a jail started by the previous yolo, a new `yolo`
   attaches exactly as today, and that jail's first launcher still owns it.
11. **The real-host measurements.** On a real rootless systemd host: the `--rm` removal with a
    headless exec live at stop; whether the keeper's scope survives closing the terminal
    application, and ends the jail in order at a logout; and a pane close there, repeating
    [§3.1](#31-what-a-pane-close-does-measured).
12. **Relaunch during a teardown.** Quit the last session, then type `yolo -- <agent>` in another
    tab of the workspace at once, and again after `yolo stop`. Each waits with a message, then
    launches fresh. `ps` never shows two keepers for one container name, and the new jail's
    owner-PID file names the new keeper ([JL-D28](#JL-D28)).
13. **A keeper that does not answer.** `kill -STOP` the keeper, then run `yolo stop`, then
    `yolo -- <agent>` in the workspace. `yolo stop` returns after the bound, and the launch is
    refused after it, each naming the keeper's pid and log ([JL-D34](#JL-D34)). `kill -CONT` lets
    the teardown finish.
14. **Two macos-user sandboxes in one workspace.** Both launches print the keeper line, the
    first as its spawner and the second as a joiner ([JL-D41](#JL-D41)), and `ps` shows one
    keeper for the key. From the first sandbox's login shell, quit the second sandbox, then start
    a Bedrock-profiled agent: its credential pointer and caller token are the keeper's, and a
    turn succeeds ([§9.9.1](#991-where-the-re-entry-problem-is-real)). A Codex left running in the
    first sandbox while a Codex starts in the second still refreshes its login. Quitting the last sandbox
    streams the teardown, and no keeper, host-services dir or roster remains.
15. **Two `yolo host` terminals in one workspace with a sidecar enabled.** Each is a resident
    `yolo host` whose child is the agent, one keeper holds the sidecar and the box, and quitting
    the first leaves both running for the second ([JL-D36](#JL-D36)).
16. **`yolo host` with nothing long-lived.** With no sidecar and no service, `yolo host -- claude`
    leaves no `yolo` process in `ps` while claude runs, and no keeper: it execs as today
    ([JL-D42](#JL-D42)).
17. **A program that closes what it inherits.** In a workspace whose `yolo host` keeper holds a
    sidecar, `yolo host -- ssh <host>` keeps the keeper up for as long as `ssh` runs, which an
    inherited descriptor would not ([§9.9.2](#992-what-a-session-is-there-and-why-yolo-host-stays-resident)).
18. **Kill a keeper at `yolo host`.** With two `yolo host` sessions and a sidecar in one
    workspace, `kill -9` the keeper. Both sessions run on. A third `yolo host` there is refused,
    naming the two and `yolo stop --at host`, and no second keeper appears in `ps`.
    `yolo stop --at host` then ends both, and no roster, box or keeper remains
    ([JL-D44](#JL-D44)).

---

## 9. The keeper (design, 2026-09-29)

This is the design the maintainer's direction on [OQ-JL1](#OQ-JL1) produced: *"some sort of
small background process. But then it needs to know how to end itself as well."* The **keeper**
*(coined here, defined in [§1.1](#11-terms))* is that process. There is one per running
container jail. The fresh launch spawns it, and it is the jail's owner for the jail's whole life,
so no terminal ever is. Sections 9.1 to 9.8 design it for the container backends;
[§9.9](#99-the-keeper-at-yolo-host-and-macos-user) carries it to `yolo host` and macos-user, one
per workspace at each, under [OQ-JL5](#OQ-JL5)'s ruling.

| Question | Short answer | Where |
|---|---|---|
| What starts it | the fresh launch, once, after everything that needs the terminal and before any host service or the container exists | [§9.1](#91-what-starts-it) |
| What it owns | the jail's host services, the container, the jail's records and its teardown, and no terminal | [§9.2](#92-what-it-owns) |
| How the first terminal gets its prompt back | the first session is an exec like every other, and its launcher exits with its agent | [§9.3](#93-how-the-first-terminal-gets-its-prompt-back) |
| How re-entering works | it is an ordinary attach | [§9.4](#94-how-re-entering-works-and-changing-agents) |
| How it ends itself | on three observations, never on a timer | [§9.5](#95-how-it-ends-itself) |
| What it discloses at launch | itself, and the plan it will run, in the terminal, before the spawn | [§9.6](#96-what-it-discloses-at-launch-and-where-its-output-goes) |
| Signals | nothing a pane close sends reaches it or pid 1 | [§9.7](#97-signal-handling-sig-proxy-and-a-pane-close) |
| Which notches have one | every notch, wherever a launch holds something long-lived ([OQ-JL5](#OQ-JL5), ruled) | [§9.8](#98-per-notch-podman-apple-container-macos-user-yolo-host), [§9.9](#99-the-keeper-at-yolo-host-and-macos-user) |

### 9.1 What starts it

- **The fresh launch, once, after everything that needs the terminal and before anything the
  keeper will own.** The pre-flights, the config-change approval, the build, the staging and
  every disclosure run in the terminal as today. The launch then takes its own session lock
  ([JL-D16](#JL-D16)) and spawns the keeper, and the keeper starts the host services and the
  container. An attach never spawns one.
- **Every fresh launch waits for an old keeper first, the attach-skew restart included**
  ([JL-D28](#JL-D28)). A fresh launch that finds a keeper still holding the name's liveness lock
  releases the launch lock, says the previous jail is still shutting down, waits, and takes the
  lock again ([§4.2](#42-the-count-a-host-side-session-lock)). The restart
  (`restartJailForAttach`, [`contracttags.go`](../../internal/cli/run/contracttags.go)) stops
  the jail while holding the launch lock, as today, and waits holding it ([JL-D26](#JL-D26)). So
  one container name never has two keepers.
- **In its own session.** The spawn sets Setsid, as `startDetached` does
  ([`scratchremoval.go`](../../internal/cli/run/scratchremoval.go)). That is legal because the
  keeper is a child and not a group leader, and it takes the keeper out of a pane close's reach
  and out of the terminal's hangup ([§3.1](#31-what-a-pane-close-does-measured)).
- **In its own cgroup scope where systemd is present.** Setsid changes the session and process
  group, not the cgroup, so on a systemd host a detached keeper still lives in the first
  terminal's scope. systemd-oomd acts on whole cgroups
  ([systemd-oomd.service(8)](https://www.freedesktop.org/software/systemd/man/latest/systemd-oomd.service.html)),
  and `KillUserProcesses=` ends a session's processes at logout
  ([logind.conf(5)](https://www.freedesktop.org/software/systemd/man/latest/logind.conf.html)).
  SOURCED. podman runs conmon in a scope of its own, for what I read as the same reason
  (INFERRED, not checked on a host here).
  - **The keeper moves itself into a transient scope before it starts anything**, with
    `StartTransientUnit` over the user's D-Bus and its own pid in the unit's `PIDs`. That is how
    podman moves conmon, as I read podman's source (INFERRED, not read here). Every child it
    starts afterward lands in that scope too.
  - **It is not started through `systemd-run --user --scope`.** That command registers the
    scope and then execs the command in its own process. A `/proc/self/exe` handed to it would
    name `systemd-run`, and the keeper would stop being the launch's child, which the launch's
    `cmd.Wait` needs ([JL-D29](#JL-D29)).
  - Where there is no systemd user bus it falls back to Setsid alone, and records which in its
    log. INFERRED; owed on a real host ([§8](#8-what-done-looks-like)).
- **From the binary the launch started from** ([JL-D5](#JL-D5)). On Linux the launch spawns the
  keeper by exec of `/proc/self/exe`, which names the running binary's inode even after
  `just install` has replaced the file at its path, so nothing has to be opened early.
  `execx.SelfExecArgv` would not do: it re-resolves `os.Executable()`, and the spawn comes after
  the nix build, which is long enough for a replacement
  ([`execx.go`](../../internal/execx/execx.go)). The keeper builds its own later self-execs the
  same way, from its own `/proc/self/exe` and never through `SelfExecArgv`; otherwise the scratch
  remover it starts at a teardown days later would run the replaced binary. macOS has no such
  file, so there a keeper of another build refuses the launch's plan by its build stamp rather
  than running it ([JL-D20](#JL-D20)).
- **With four things handed to it:**
  - one **plan**, everything the launch computed for the services and the container, which is
    the value its disclosure was printed from ([JL-D20](#JL-D20)). It names packs by identity,
    never by a path into the launch's own per-process trees ([JL-D35](#JL-D35));
  - a **progress pipe** back to the launch;
  - a **lifeline**, whose EOF before the jail is ready means the launch died during boot;
  - the workspace **launch lock**, which the keeper releases once the container is seen running
    or before any unwind ([JL-D31](#JL-D31)).
- **Its descriptors are set, not inherited by accident** ([JL-D29](#JL-D29)).
  - Its stdin, stdout and stderr are `/dev/null`, as `startDetached` gives its child.
  - The progress pipe, the lifeline and the launch lock arrive as `ExtraFiles`, and the keeper
    marks each close-on-exec first thing. A Go child receives them without that flag, by
    convention: *"Programs that know they inherit fds >= 3 will need to set them
    close-on-exec"* (`syscall/exec_linux.go`, SOURCED). Without it every long-lived child the
    keeper starts would hold them: socat, the fronted daemons, the runtime client, the scratch
    remover. A keeper that died before ready would then leave the pipe open, and a leaked lock
    would keep a jail or a keeper looking alive.
  - No session-lock or liveness-lock descriptor is ever passed to a child or left inheritable.
    The launch lock to the keeper and the scratch remover's own in-flight lock are the only
    deliberate handoffs.
- **Its first acts** are to move into its scope, to mark its inherited descriptors
  close-on-exec, to take its **liveness lock** ([§1.1](#11-terms)), and to write the owner-PID
  file naming itself.
  - The liveness-lock file is one per container name, and is never renamed over or unlinked
    ([JL-D28](#JL-D28)).
  - The gap before the keeper locks it needs none of `servicessession.go`'s pending-name trick.
    The fresh launch already holds its shared session lock and the launch lock it handed over.
    Nothing acts on a free liveness lock without also holding the session lock exclusively
    ([JL-D7](#JL-D7), [JL-D30](#JL-D30)), and no arrival probes it without the launch lock
    ([JL-D28](#JL-D28)). So no reaper can act on the gap ([JL-D18](#JL-D18)).

### 9.2 What it owns

**It takes over the host half of [§2.4](#24-everything-the-first-terminals-process-owns-today)'s
container table:**

- the port forwards (the socat processes);
- the cgroup delegate;
- the loophole fronts and the fronted per-jail daemons, which is `startLoopholesDisclosed`'s set
  ([`packloopholes.go`](../../internal/cli/run/packloopholes.go));
- the credential-view registration step (`registerClaudeCredentialView`,
  [`claudecredentialview.go`](../../internal/cli/run/claudecredentialview.go));
- the container, and its attached runtime client ([JL-D15](#JL-D15));
- the tracking, owner-PID, home-skeleton and pack-tree records, and the scratch-volume names;
- the E3 config capture, and the Window A instrumentation;
- its own embedded pack tree lease and its own pack resolution ([JL-D35](#JL-D35)). It never
  reads a `Pack.Root` inside the fresh launch's per-process trees. Those are the fallback tree, a
  private copy of the shipped packs a process takes when the shared per-build copy is unusable,
  and the **process pack tree**, the name `processtree.go` coins for the one leased directory per
  process that holds the configured packs a command staged for its own lifetime
  (`packload.ProcessPackDir`). `ReleaseEmbedded` deletes both when the first terminal quits,
  while the keeper still runs daemons from them and reads them at teardown
  (`runWithProxy`'s comment in [`proxy_linux.go`](../../internal/cli/run/proxy_linux.go): *"that
  teardown can still read an embedded Pack.Root"*). That is the failure
  [`processtree.go`](../../internal/packload/processtree.go) names: a copy that *"would die with
  a process that a host-scope daemon spawned from it outlives"*. SOURCED.

**Each session's own launcher keeps what is about its terminal:**

- its session lock ([§4.2](#42-the-count-a-host-side-session-lock));
- the TTY proxy and its signal arm ([§9.7](#97-signal-handling-sig-proxy-and-a-pane-close));
- the workspace launch lock while it decides to attach;
- the terminal's jail indicator, and its own lines of `launch.log`;
- its own embedded pack tree lease, released when it exits, as today;
- for the fresh launch only, the housekeeping slot ([JL-D10](#JL-D10)).

Each piece runs today's code. The code moves; it is not rewritten, and
[§1](#1-the-verdict-and-the-words-it-uses) says why the keeper must start them rather than be
handed them. The in-jail daemons, the doorways of [HS-D15](host-notch-services.md#HS-D15)
included, are started by the in-jail supervisor at pid 1's boot, and the caller tokens the
launch minted travel in the plan.

It sweeps nothing: it cleans up only what its own jail made ([JL-D23](#JL-D23)). When
[`agent-event-watchers.md`](agent-event-watchers.md)'s host-side sidecars and ping box land,
they are the keeper's too, at every notch ([EW-D24](agent-event-watchers.md#EW-D24),
[EW-D25](agent-event-watchers.md#EW-D25)).

### 9.3 How the first terminal gets its prompt back

- **The first session is an exec, like every attach** ([JL-D16](#JL-D16)), under the TTY proxy.
- **When its agent exits, its launcher drops its session lock and exits with the agent's code.**
  If other sessions remain it prints one line ([§4.5](#45-what-the-user-sees)) and returns at
  once. Nothing in it waits on the keeper, the container, or a `podman run` client.
- **Window A leaves the terminal.** Window A ([§1.1](#11-terms)) is recorded by the tree today
  because the terminal waits through it. This workspace's own `host-perf.log` holds values from 0.03 s to 32 s (the 30 most recent
  runs, 2026-09-18 to 2026-09-29, MEASURED from the records). Under the keeper that client, if
  there is one, is the keeper's.
- **If it was the last session, it waits for the teardown and streams it** ([JL-D11](#JL-D11)).
  That is today's quit. The records put the host-side chain after the client's exit at 0.10 to
  0.62 s, and stopping a detached hold took 0.19 to 0.40 s in nested podman, with the `--rm`
  removal done about 15 ms later (3 runs, MEASURED).

### 9.4 How re-entering works, and changing agents

**Re-entering is an attach, and it needs nothing new** ([JL-D22](#JL-D22)). Once the keeper owns
the jail, the first terminal is an ordinary session, so typing `yolo -- codex` where
`yolo -- claude` just quit runs `attachExisting`, which already:

- composes this entry's channel over the running jail's own packs (`runningJailPackView`,
  [`packtree.go`](../../internal/cli/run/packtree.go));
- injects that jail's launch flags for the new command;
- delivers this entry's profile environment into the running jail before its exec
  (`deliverChannelOnAttach`, [`run.go`](../../internal/cli/run/run.go)).

**"Hand ownership over" needs no mechanism,** because no terminal ever owned the jail. The
keeper was the owner from the jail's first moment, which is what makes [JL-P4](#JL-P4)'s "no
handoff" and the maintainer's "hand ownership over" the same wish.

**What an attach already refuses stays refused.** A running jail keeps the packs it booted
with ([OQ-PK2](../reference/pack-system.md#oq-pk2)). An agent whose pack was added to the config
since the launch, or a profile whose served daemon the jail's launch did not start, takes the
attach-skew disposition: a restart prompt that says it ends every session
(`askToRestartJail`, `jailSessionsPhrase`), a refusal, or the acknowledgment hatch
([`contracttags.go`](../../internal/cli/run/contracttags.go)). SOURCED. Nothing here changes
that, and under last-session-wins that prompt matters more, because the sessions it ends were
expected to outlive this tab.

**Two cases are not an attach:**

- **The tab was the jail's only session.** The jail ended with it ([JL-D12](#JL-D12)), so the
  next `yolo -- <agent>` launches fresh. That is [OQ-JL6](#OQ-JL6). The records put an attach at
  0.12 to 0.20 s of host-side time before its exec, and a warm fresh launch at 2.15 to 2.55 s
  before its runtime client starts, plus the in-jail boot (MEASURED from this workspace's
  `host-perf.log`: 5 attaches and 14 of 21 fresh launches; the other 7 fresh ones took 3.1 to
  64 s).
- **Switching agents without leaving the jail at all.** A bare `yolo` opens the jail's shell,
  and that shell is the session. Agents started from it come and go with no teardown and no
  host round trip. That works today.

**Re-attaching to an agent whose terminal is gone is not possible.** podman's CLI has no command
that re-attaches to a running exec session: `podman attach` attaches to the container's main
process (`podman --help`, `podman attach --help`, 5.8.7, MEASURED). A multiplexer such as herdr
is where an agent outlives its window, and [OQ-JL8](#OQ-JL8) ruled that a closed pane's agent
ends with the pane.

### 9.5 How it ends itself

The keeper ends on exactly three observations, and never on a timer ([JL-D17](#JL-D17)):

1. **Before the jail is ready, the launch died or a start failed:** EOF on the lifeline, or a
   host service or the container that did not start. It releases the launch lock it was
   handed ([JL-D31](#JL-D31)), stops what it started, removes its records, and exits, non-zero
   for a failed start. After ready it stops reading the lifeline, because from then on the
   first launch's death is one session ending, which the lock counts.
2. **Zero sessions:** its exclusive take of the session lock succeeds. It records the stop
   reason, stops the container, confirms through the tri-state `ps -a` probe that it is gone,
   removes a stopped leftover ([JL-D9](#JL-D9)), runs the teardown chain, releases its liveness
   lock, and exits.
3. **The container ended some other way:** `yolo stop`, an OOM kill, a crash, or a `podman stop`
   by hand. It learns this from the runtime (`podman wait`, or its attached client's exit,
   whichever comes first), without polling. In nested podman `podman wait` returned within 5 ms
   of a `podman stop` (MEASURED). It confirms with the same probe, records the reason and runs
   the chain. It then takes the session lock exclusively, once the sessions whose exec ended have
   dropped theirs, and exits ([§4.2](#42-the-count-a-host-side-session-lock), [JL-D28](#JL-D28)).
   Sessions still attached see their exec end and print the reason. `yolo stop` itself waits
   for the liveness lock and streams the teardown, as a last session does ([JL-D25](#JL-D25)).
   An arrival during this chain finds no running container but a held liveness lock, and waits
   ([JL-D28](#JL-D28)).

A SIGTERM or SIGINT sent to the keeper is a fourth way in, not a fourth observation: it ends
the jail in order, through observation 3's path ([JL-D24](#JL-D24),
[§9.7](#97-signal-handling-sig-proxy-and-a-pane-close)).

Everything else keeps it holding:

- **It cannot open the session lock:** it never drains on the count and waits only for the
  container's end ([JL-D3](#JL-D3)).
- **The runtime does not answer:** "could not ask" is never "gone". It keeps the services up,
  retries, and logs each failure. Every runtime call it makes is bounded, like
  `trackingProbeTimeout`, rather than today's unbounded probe: `stopLoopholes` runs its `ps` with
  timeout 0 (*"if the runtime hangs here this mark is the last line"*,
  [`loopholesruntime.go`](../../internal/cli/run/loopholesruntime.go), SOURCED). Whoever waits on
  a keeper stuck like this gives up after a bound and says what it is waiting on
  ([JL-D34](#JL-D34)).
- **After it has stopped the container, it never waits on its own runtime client's exit** once
  the probe answers that the container is gone. Otherwise the last session would wait through
  Window A again.

**If it is killed without ending itself** (SIGKILL, an OOM kill, or a kill of its cgroup scope),
the jail is **unkept**:

- **Everything it held ends with it.** The fronts and the cgroup delegate are goroutines in it.
  The port forwards and the fronted daemons end because each has an end that does not depend on
  the keeper ([JL-D32](#JL-D32)).
  - Without that rule they would not end. socat is the keeper's plain child in the keeper's own
    session, so no pane close or hangup reaches it, and the fronted daemons lead sessions of
    their own. After a SIGKILL each would go on serving the unkept jail with no owner, which
    breaks [YW-P3](../research/central-yolo-watcher.md#YW-P3). Each would also run on after the
    jail was reaped, since neither the reaper nor `stopLoopholes(nil, …)` kills them. INFERRED
    from `startHostPortForwarding` and `startExternalService`.
  - The forwards' socket dir, `/tmp/yolo-fwd-<cname>`, is bind-mounted read-write into the
    workspace's next jail (`fwdSocketDir` and its `-v`,
    [`assemble_parts.go`](../../internal/cli/run/assemble_parts.go)), and
    `startHostPortForwarding` removes only the sockets it is about to forward
    ([`network.go`](../../internal/cli/run/network.go)). SOURCED. So an orphaned socat for a
    forward the config has since dropped would stay reachable from the new jail. That function
    therefore removes the whole dir before it spawns anything ([JL-D32](#JL-D32)).
- The kernel frees its liveness lock, and that, not its PID, is the evidence ([JL-D18](#JL-D18)).
- What the running sessions and the next arrival then see was ruled in [OQ-JL7](#OQ-JL7) (A): the
  sessions run on without host services and are told when they quit, and the next arrival is
  refused and offered `yolo stop` ([JL-D13](#JL-D13)). The **last session to quit reaps the
  jail itself**, on every backend, and `yolo stop` does the same ([JL-D30](#JL-D30)). The next
  launch's reaper stays as a backstop, and waits until the sessions are gone
  ([JL-D7](#JL-D7)). Nothing restarts the keeper.
- **Why the reaper alone is not enough.** `reapOrphanedJails` returns at once on Apple Container
  (*"Apple Container has no owner-PID lifecycle yet, so it's a no-op there"*,
  [`lifecycle.go`](../../internal/cli/run/lifecycle.go)). On podman it runs only when some `yolo`
  runs in some workspace. Today an orphan still ends when its headless first agent exits,
  because that agent is pid 1; with a hold as pid 1 it would not end at all. The reaper also does
  only part of the teardown: `stopJail` and `stopLoopholes(nil, …)`, with no E3 capture, no
  `forgetGoneContainer`, no scratch removal and no socat cleanup. And `yolo stop` runs only
  `<rt> stop` ([`stop.go`](../../internal/cli/stop.go)), so under [JL-D25](#JL-D25) alone it would
  wait on a liveness lock that is already free while nobody tore down the host side. SOURCED.
- **How the last session reaps it** ([JL-D30](#JL-D30)). After dropping its shared lock, a
  quitting session takes the liveness lock with `LOCK_EX|LOCK_NB`, then the session lock the
  same way. If it wins both while the owner-PID file names a dead process, the jail is unkept
  and it was last. Holding both, it runs the keeper's chain in-process: `stopJail`,
  `stopLoopholes(nil, …)`, `forgetGoneContainer`, E3 and the scratch removal. Holding the
  liveness lock makes an arrival wait for it as for a draining keeper. If it wins only the
  liveness lock, other sessions remain. If the owner is alive, it is an older yolo's launcher,
  and the jail stays its own. Either way the session lets its locks go and returns.
  - The names only the keeper knew, the home skeleton and the scratch-volume launch id, come
    from a record the keeper writes at its start, in host state no jail mounts, beside its
    liveness lock. Whatever that record does not name is left for `yolo prune`, as a reaped
    orphan's is today.

### 9.6 What it discloses at launch, and where its output goes

**The keeper is disclosed at every fresh launch, in the terminal, before the spawn**
([JL-D21](#JL-D21)). A process that outlives the terminal it was started from is exactly what a
launch's disclosures exist to name, and a launch has no quiet mode
([OQ-RO3](../reference/report-tiers.md#why-its-this-way)). One line, then its pid once started.
Suggested wording:

```text
keeper: yolo internal daemon jail-keeper will hold this jail's host services (the claude-oauth-broker front, 2 port forwards, the cgroup delegate) until its last session leaves; log: <host state>/jail-keeper-<cname>.log
keeper: started, pid 48213
```

The services named and the log's place are illustrative; the line lists what the plan holds, and
the log lives in host state no jail mounts ([JL-D19](#JL-D19)).

> [!WARNING]
> **The service disclosures still precede the spawn.** The host-execution disclosure *"has to
> precede the spawn"* (the comment above `startLoopholesDisclosed`'s call in `runContainer`,
> [`run.go`](../../internal/cli/run/run.go)), because a pack-shipped daemon's disclosure is the
> whole trust boundary today ([`packhostgrants.go`](../../internal/cli/run/packhostgrants.go):
> *"the boundary today is DISCLOSURE, not consent"*). So the fresh launch prints it in the
> terminal, from the plan, **before** it spawns the keeper, and the keeper runs exactly that
> plan and refuses any daemon it did not name ([JL-D6](#JL-D6), [JL-D20](#JL-D20)). A keeper that
> printed its own disclosures after the spawn would print a notification rather than a
> disclosure.

**Its output is never only in a file** ([JL-D19](#JL-D19)):

- **Until ready,** everything it prints goes over the progress pipe to the fresh launch, which
  prints it through its own writers, so the terminal and `launch.log` both have it. Relaying
  changes only which process wrote a line.
- **After ready,** it writes to its own log in host state no jail mounts, and mirrors each line
  into `launch.log`.
- **What it records reaches a terminal.** A service that died, a scope fallback, a stop reason:
  each session's quit prints what the keeper recorded while that session was in, and an arrival
  prints any service the keeper recorded as down.
- **One switchable writer carries all of it** ([JL-D29](#JL-D29)). The keeper's `o.Stdout` and
  `o.Stderr` both sit behind it: it writes to the progress pipe until ready, and to the
  host-only log always. Reused code captures its writer when it starts a service:
  `startExternalService` takes `out := o.pr(o.Stdout)` and hands it to the stop closure that
  calls `killServiceGroup` ([`loopholesruntime.go`](../../internal/cli/run/loopholesruntime.go),
  SOURCED). So pointing `o.Stdout` somewhere else after ready would not move the teardown's
  lines. Only a writer that switches underneath does.
- **A closed pipe never kills it.** The fresh launch stops reading when it exits: after ready
  once the first terminal quits, and before ready when a pane closes during boot. Go exits on
  `SIGPIPE` for a write to a broken pipe on descriptor 1 or 2 unless the program calls `Notify`
  for it ([os/signal](https://pkg.go.dev/os/signal#hdr-SIGPIPE), SOURCED). That would kill the
  keeper before it unwinds and leave its services and container behind. So the keeper's stdio is
  `/dev/null`, the pipe is an `ExtraFiles` descriptor, where a failed write is only `EPIPE`, and
  the keeper still calls `signal.Notify` for `SIGPIPE`. It never calls `signal.Ignore`, whose
  `SIG_IGN` its children would inherit.

### 9.7 Signal handling: sig-proxy and a pane close

The rule is that **nothing a pane close or a window close sends can reach the keeper or pid 1**,
and each session's launcher ends only its own session. [§3.1](#31-what-a-pane-close-does-measured)
measured the three shapes this rests on.

| Process | Where it sits | A pane close or window close | A SIGTERM sent to it | A SIGKILL sent to it |
|---|---|---|---|---|
| A session's launcher, the first included | in the pane's session, under the TTY proxy | its signal arm ends only its own session: never `stopJail`, and, as [OQ-JL8](#OQ-JL8) ruled, a hangup to its own in-jail process tree ([JL-D4](#JL-D4)); then it restores the terminal and exits, and its session lock drops | the same | its lock drops; its agent may run on headless ([§4.4](#44-failure-paths)) |
| That session's `podman exec -it` client | in the pane | dies with the pane. conmon keeps the exec session, so without the arm's hangup its process runs on (MEASURED, [§3.1](#31-what-a-pane-close-does-measured)). `podman exec` has no signal proxy | — | — |
| The keeper | its own session and cgroup scope | not reached: a process in its own session survived every pane close measured | ends the jail in order ([JL-D24](#JL-D24)) | unkept jail ([§9.5](#95-how-it-ends-itself), [OQ-JL7](#OQ-JL7)) |
| The runtime client for pid 1: attached, with no tty | the keeper's child, in no pane | not reached | it exits without forwarding the signal, because it runs with `--sig-proxy=false` ([JL-D15](#JL-D15)); the container runs on | the same |
| pid 1's hold (the entrypoint, under catatonit) | the container | receives nothing: no client in any pane forwards to it | ends, which is what `podman stop` sends | the container ends |

What each row rests on:

- **`--sig-proxy` matters only for a client that stays attached.** Today's attached
  `podman run -it` forwards what the pane receives into pid 1, and that alone stopped the jail
  in the measurement. The keeper keeps an attached, non-tty client with `--sig-proxy=false`,
  which left pid 1 untouched, and reads pid 1's output from it ([JL-D15](#JL-D15),
  [§4.1](#41-the-container-a-hold-process-as-pid-1-and-every-session-an-exec)). A detached
  start also left pid 1 untouched, but would send its output nowhere under `--log-driver none`.
- **The hold ignores SIGHUP and SIGINT anyway.** Today's `holdContext` cancels on SIGINT and
  SIGTERM only ([`hold.go`](../../internal/entrypoint/hold.go)), and a Go program with no handler
  for SIGHUP exits on it
  ([os/signal](https://pkg.go.dev/os/signal#hdr-Default_behavior_of_signals_in_Go_programs)).
  SOURCED. So the hold installs SIGHUP and SIGINT handlers that do nothing, through
  `signal.Notify` and never `signal.Ignore`, and ends only on SIGTERM. The refused-boot hold keeps
  ending on SIGINT, as its notice says ([§4.1](#41-the-container-a-hold-process-as-pid-1-and-every-session-an-exec)).
- **The keeper ignores SIGHUP and ends the jail in order on SIGTERM or SIGINT** ([JL-D24](#JL-D24)).
  It ignores SIGHUP and SIGPIPE through `signal.Notify`, never `signal.Ignore`, whose `SIG_IGN`
  socat and the fronted daemons would inherit ([JL-D29](#JL-D29)).
  A process in a session of its own has no controlling terminal, so a hangup reaches it only
  from `kill` ([setsid(2)](https://man7.org/linux/man-pages/man2/setsid.2.html)). A SIGTERM is
  what a shutdown, a logout or `systemctl --user stop` sends to its scope, and an orderly end
  there beats an unkept jail the shutdown ends anyway.
- **The arm's hangup fits herdr's budget.** herdr sends SIGKILL about 500 ms after its SIGHUP,
  and one exec round trip is 56 to 71 ms (MEASURED), so the hangup fits. INFERRED. A launcher
  SIGKILLed before it runs leaves its agent headless; on local Linux podman the optional death
  pipe of [JL-D4](#JL-D4) closes that gap.
- **Ctrl-C is still a byte.** The TTY proxy forwards `0x03` into the jail
  ([`ttyproxy.go`](../../internal/ttyproxy/ttyproxy.go)), so Ctrl-C reaches the agent, as today,
  and never the arm.
- **The detach sequence is off on podman.** Every podman `run` and `exec` yolo issues passes
  `--detach-keys=""` ([§2.3](#23-four-defects-found-on-the-way) item 2, [JL-D27](#JL-D27)), so
  typing `ctrl-p,ctrl-q` cannot leave a session's client detached from its agent. Apple
  Container gets the same rule only once `container run --help` or a probe shows it has a flag or
  a detach sequence ([§9.8](#98-per-notch-podman-apple-container-macos-user-yolo-host)).
- **A pane closed during boot** ends the fresh launch before the jail is ready; the keeper sees
  its lifeline's EOF and unwinds everything ([§9.5](#95-how-it-ends-itself) item 1). Its writes
  to the progress pipe then fail with `EPIPE`, and never kill it
  ([§9.6](#96-what-it-discloses-at-launch-and-where-its-output-goes)).
- **A herdr server restart** closes every pane at once: every session's lock drops, the keeper
  drains, and the jail ends ([§4.4](#44-failure-paths)).

### 9.8 Per notch: podman, Apple Container, macos-user, yolo host

A **notch** is the confinement level a launch runs at ([§1.1](#11-terms)). Under
[OQ-JL5](#OQ-JL5)'s ruling every notch has a keeper wherever a launch holds something long-lived.
At the jail notch's backends that is every launch, since the container is such a thing; the
other two notches are [§9.9](#99-the-keeper-at-yolo-host-and-macos-user).

| Notch or backend | Keeper? | What changes | What is owed |
|---|---|---|---|
| podman on Linux | yes | everything above | a rootless-host run for the `--rm` removal behavior, the keeper's scope move, and a pane close and a logout with the keeper in its own scope. The loopback carve-out does not move: the keeper binds the same loopback the launcher did, and a nested jail still cannot check it |
| podman on macOS | yes | the same design. The session and liveness locks are flocks on the Mac, so they work with the remote client. There is no `/proc/self/exe`, so a keeper of another build refuses the plan ([JL-D20](#JL-D20)); there is no systemd, so the keeper has Setsid alone. Each session needs a signal arm for [JL-D4](#JL-D4): an attach's has one since step 1, through the same plain spawn in [`proxy_other.go`](../../internal/cli/run/proxy_other.go) ([JL-D52](#JL-D52)), not yet run on a Mac | a Mac run, including a pane close and a logout |
| Apple Container | yes | the same design, through `container run` and `container exec`. Today it has an owner, the first launcher, but no orphan reaper (row 5 of [§2](#2-what-ties-a-jail-to-its-first-terminal-today)). Under this design the keeper owns it, an unkept jail's last session reaps it ([JL-D30](#JL-D30)), and the next-launch reaper is extended to its keeper-era jails, whose liveness lock is the evidence it lacked ([JL-D7](#JL-D7)). The same signal arm, an attach's since step 1 and unmeasured there. It gets no `--detach-keys` until one is shown to exist ([JL-D27](#JL-D27)) | whether its attached client can be kept from forwarding signals, as `--sig-proxy=false` does on podman; whether `container exec`'s process survives its client's death; and whether it has detach keys at all (all unmeasured) |
| macos-user | yes, one per workspace, whenever a launch starts anything outside the sandbox ([§9.9](#99-the-keeper-at-yolo-host-and-macos-user)) | the fronts, fronted daemons, doorways, launch-owned services, host-side sidecars and ping box move from each session's launcher to the keeper, and every session uses the same ones. What runs as the sandbox account stays each session's, since a keeper cannot run `sudo` ([§9.9.4](#994-what-the-keeper-owns-there)) | a Mac run: two sandboxes in one workspace, the write grant on the ping box, a logout, and the session launcher's signal arm under a Ctrl-C and a pane close |
| `yolo host` | yes, one per workspace, whenever a launch holds a sidecar or the ping box, and, if [OQ-JL9](#OQ-JL9) is ruled B or C, its own services ([§9.9](#99-the-keeper-at-yolo-host-and-macos-user)) | a launch with a keeper stays resident as its session instead of exec'ing ([JL-D36](#JL-D36)); one with none execs as today. Whether the launch's own services move in is [OQ-JL9](#OQ-JL9) | a host run on Linux and on a Mac, including a pane close |

### 9.9 The keeper at yolo host and macos-user

[OQ-JL5](#OQ-JL5) was ruled A on 2026-09-29, *"if that's something that we can support"*: a
keeper at every notch that starts a long-lived host service or sidecar, with no backend carved
out and every feasibility problem named. **It is supportable at both notches.** It costs two
things, and neither is a carve-out, because the same rule decides at every notch:

- a `yolo host` launch that has a keeper stays resident as its agent's parent instead of
  exec'ing it, because an inherited lock descriptor was measured to miscount a session both ways
  ([§9.9.2](#992-what-a-session-is-there-and-why-yolo-host-stays-resident));
- at macos-user the keeper cannot own anything that runs as the sandbox account, because it
  cannot run `sudo` ([§9.9.4](#994-what-the-keeper-owns-there)).

Every problem found is listed in [§9.9.10](#9910-every-feasibility-problem-found).

| Question | Short answer | Where |
|---|---|---|
| Is the maintainer's re-entry problem real there? | yes at both: at macos-user today, and at `yolo host` for the Codex adapter, where a shared token already fixes it; there the bridge alone shares nothing | [§9.9.1](#991-where-the-re-entry-problem-is-real) |
| What a session is | one invocation whose `yolo` process stays up for its command's life and holds the session lock | [§9.9.2](#992-what-a-session-is-there-and-why-yolo-host-stays-resident) |
| Which sessions share a keeper | those of one workspace at one notch | [§9.9.3](#993-one-keeper-per-workspace-per-notch) |
| What it owns | at macos-user everything the launch starts outside the sandbox; at `yolo host` the sidecars and the ping box | [§9.9.4](#994-what-the-keeper-owns-there) |
| How a second launch joins | it counts itself, reads the keeper's roster and composes against it | [§9.9.5](#995-how-a-second-launch-joins) |
| What the first terminal sees, and how the keeper ends | the prompt as the command exits; the last session streams the teardown; two observations end the keeper | [§9.9.6](#996-what-the-first-terminal-sees-and-how-the-keeper-ends) |
| What it discloses | the container keeper's line, naming the notch; a joining launch names the keeper it joined | [§9.9.7](#997-what-it-discloses) |
| A launch with nothing a keeper there holds | no keeper; it runs as today | [§9.9.8](#998-a-launch-with-nothing-long-lived) |
| What changes for [OQ-HS3](host-notch-services.md#OQ-HS3) | "the launch" becomes the key's life, for what a keeper holds | [§9.9.9](#999-what-changes-for-a-host-services-lifetime) |

#### 9.9.1 Where the re-entry problem is real

The maintainer gave re-entry as the reason for A: *"if you can re-enter … you're just gonna have
all the same problems"*, and asked to be told if that is wrong. At these two notches there is no
running container to attach to, so re-entering is a second launch in the same workspace. Whether
it meets "the same problems" depends on whether one session uses anything another started. The
answer differs by what is shared:

- **At macos-user it is right, today.** Each session opens its own doorways and launch-owned
  services, on ports it picked, behind caller tokens it minted
  ([HS-D18](host-notch-services.md#HS-D18)). But the per-agent env files the launch writes for
  them are the workspace's, not the session's: the arm calls
  `writeMacosUserAgentEnvFiles(paths.WorkspaceHomeState(o.Workspace), channel)`
  ([`run.go`](../../internal/cli/run/run.go)), and each file says *"Rewritten by every entry;
  sourced by its launcher"* (`agentEnvFileContent`,
  [`agentenvfiles.go`](../../internal/cli/run/agentenvfiles.go)). A file carries *"the gated env
  its own selection satisfies"* (that file's header), which is where aws-auth's credential
  pointer and caller token land (the `env` contribution in
  [`packs/aws-auth/pack.json`](../../packs/aws-auth/pack.json)). So once a second session starts,
  an agent started afterwards from the first session's login shell sources the second session's
  listener and token, and when the second session quits, that agent's pointer names a closed
  port.

  **A running agent is hit too, not only one started later.** `.codex` is per-workspace state
  there (`{"at": ".codex", "kind": "state", "scope": "workspace"}` in
  [`packs/codex/pack.json`](../../packs/codex/pack.json)), so two sandboxes of one workspace share
  `.codex/auth.json`. Each session's Codex launcher binds that session's own doorway token into
  the file's refresh marker (`$YOLO_SERVICE_OPENAI_AUTH_BROKER_TOKEN`, the comment in
  [`macosuserguestdaemons.go`](../../internal/cli/run/macosuserguestdaemons.go)), while each
  Codex's `CODEX_REFRESH_TOKEN_URL_OVERRIDE` names its own session's doorway, fixed when it
  started. Codex rereads `auth.json` before each refresh, so once a Codex starts in the second
  session, the first session's running Codex sends the second session's token to the first
  session's doorway, which answers only its own, and its refresh fails. That is
  [NC-D18](../plans/notch-convergence.md#NC-D18)'s host defect again, at macos-user.

  SOURCED for the files and the binding; both failures are INFERRED and unmeasured. macos-user has
  run on a Mac, and this class of defect was measured there once already: two sessions sharing one
  host-services dir, where *"the surviving session's claude-oauth-broker endpoint was gone, and
  before that it had named the other session's front"* ([HD-D1](host-daemon-ownership.md#HD-D1),
  the header of [`servicessession.go`](../../internal/cli/run/servicessession.go)). That is the
  container defect in another form: one session's start or end takes something another uses.
- **At `yolo host` it is right too, and it has been met twice already.** Only the bridge's host
  half is the launch's alone: its port and secret go into that launch's agent in-process,
  `yolo host apply` renders no per-launch address, and `yolo host env` refuses a bridged profile
  ([`host-notch-services.md` §4.6](host-notch-services.md#46-every-front-door)). The managed
  Codex refresh adapter is not. Each launch listens on a port of its own, but every
  `yolo host -- codex` on the machine, in any workspace, runs on one managed `CODEX_HOME`, keyed
  on the codex pack in the machine-wide store (`prepare` in
  [`host.go`](../../internal/openaiauthhost/host.go)), so on one `auth.json`, which Codex rereads
  before every refresh. When each launch bound a caller token of its own into that file, the
  newest replaced the others', whose adapters then refused their own Codex: *"two host sessions
  at once, which worked before item 1, broke"* ([NC-D18](../plans/notch-convergence.md#NC-D18)).
  The fix shares one token among the home's live launches, counted by a shared lock on the home's
  `.yolo-live.lock` (`sharedCallerToken`). Codex's own background server was the same problem
  again: a setsid'd copy of Codex that *"inherits the environment of the launch that STARTED it,
  so it keeps posting refreshes to that first launch's closed port"*
  ([`codexdaemon.go`](../../internal/openaiauthhost/codexdaemon.go)).
  [OQ-CDX1](../research/codex-background-service.md#OQ-CDX1) turned it off wherever yolo launches
  Codex, giving up `codex queue`. SOURCED. So re-entry at `yolo host` did bring *"the same
  problems"*, and each was patched on its own. The patch for Codex is scoped to the machine, not
  to a workspace.
- **At both notches it is right for what the sidecar design shares.** One sidecar instance per
  workspace ([EW-D19](agent-event-watchers.md#EW-D19)), one ping box per workspace, and one
  master per box ([EW-DIR3](agent-event-watchers.md#EW-DIR3)) are used by every session of the
  workspace. EW-D19 passed the sidecar from launch to launch by a kernel lock, which is the
  migration [JL-D14](#JL-D14) rejected for container jails.

So the keeper is needed at macos-user for everything its launches start outside the sandbox, and
at both notches for the sidecar feature. At `yolo host`, what the Codex adapter shares belongs to
the machine, not to a workspace, so a per-workspace keeper cannot simply take it over
([§9.9.10](#9910-every-feasibility-problem-found) row 8). Whether it and the bridge move into the
keeper, and how, is [OQ-JL9](#OQ-JL9).

#### 9.9.2 What a session is there, and why yolo host stays resident

A session at these notches is one invocation, with its launcher. What holds the session lock is
the question, because the kernel drops a flock only when every descriptor for it is closed.

- **At macos-user the launcher already stays up.** It runs the sandboxed command as its child,
  `sudo --user=_yolojail … sandbox-exec …` (`LaunchArgv`,
  [`macosuser.go`](../../internal/macosuser/macosuser.go)), and waits for it: the last step of
  `RunMacosUser` is `deps.RunWithProxy(plan.LaunchArgv)`
  ([`orchestrator.go`](../../internal/macosuser/orchestrator.go)). So it holds the session lock
  exactly as a container session's launcher does, and nothing crosses into the sandbox. Nothing
  could: before it runs a command, sudo *"will close all open file descriptors other than
  standard input, standard output, and standard error"*, from 3 by default
  ([sudoers(5)](https://www.sudo.ws/docs/man/sudoers.man/), `closefrom`). SOURCED.
- **At `yolo host` the launch execs its agent today** whenever it owns nothing
  (`hostSyscallExec`, [`host.go`](../../internal/cli/host.go)), so no `yolo` process is left to
  hold a lock. [OQ-JL5](#OQ-JL5)'s answer supposed the agent could inherit the lock's descriptor across that
  exec. It can, and it counts the wrong thing:
  - **The lock survives the exec.** *"Locks created by flock() are preserved across an
    execve(2)"* ([flock(2)](https://man7.org/linux/man-pages/man2/flock.2.html)), and a
    descriptor stays open across an exec unless its close-on-exec flag is set
    ([POSIX exec](https://pubs.opengroup.org/onlinepubs/9799919799/functions/exec.html)), which
    also keeps the process ID. Go opens every file close-on-exec, so the launch would clear the
    flag with `fcntl(fd, F_SETFD, 0)` before `syscall.Exec`. macOS has the same rule: *"Locks
    are on files, not file descriptors"*, and a duplicate from `dup` or `fork` is one of
    *"multiple references to a single lock"*
    ([flock(2) on macOS](https://keith.github.io/xcode-man-pages/flock.2.html)). SOURCED on both,
    MEASURED on Linux only.
  - **But the lock then lasts as long as any process holding the descriptor, not as long as the
    agent.** MEASURED 2026-09-29 in this jail (go1.26.7, Linux 7.2.7): a Go launcher took
    `LOCK_SH` on a file, exec'd the program in the first column, and a second process tried
    `LOCK_EX|LOCK_NB` while it ran and after it exited. "Held" means another process still had
    the lock.

    | The launch exec'd into | While it ran | After it exited |
    |---|---|---|
    | `sleep`, the flag cleared | held, with the launcher's PID | free |
    | `sleep`, Go's default close-on-exec | **free** | — |
    | `sh`, which backgrounded a `sleep 3` and exited | — | **held** until that `sleep` exited |
    | a Go program whose `os/exec` child, Setsid, outlived it | — | **held** until the child exited |
    | Node v24.19.0 | held | free, while a `child_process` shell it started still ran; that shell's open descriptors were 0, 1 and 2 |
    | OpenSSH 10.5p1 `ssh`, connecting for 3 s | **free** | — |

  - So the count would be right for an agent that runs on Node, which pi, copilot and omp
    probably do, their packs installing them with `"via": "npm"` (INFERRED), and it is wrong both
    ways for much else `yolo host --` may run. A shell's or a Go program's children keep a session alive after its command exits,
    which keeps the keeper's services up for nobody. A program that closes what it inherits, as
    `ssh` does, reads as a session that ended while it runs, so the keeper may drain under it,
    which is the polarity [JL-P3](#JL-P3) forbids. claude and codex are native binaries their
    packs install with `"via": "installer"`, and what their children inherit is UNMEASURED.
  - **A PID watch is the other way, and is not taken.** The PID survives the exec, and Linux's
    pidfd or macOS's kqueue `EVFILT_PROC` with `NOTE_EXIT` reports that one process's exit; an
    exec is a separate `NOTE_EXEC` ([kqueue(2)](https://keith.github.io/xcode-man-pages/kqueue.2.html)).
    It miscounts neither way, but the keeper would keep the count in memory, while the reaper, an
    arrival's probe and the last session's reap read the kernel lock ([JL-D7](#JL-D7),
    [JL-D28](#JL-D28), [JL-D30](#JL-D30)). That is a second count with nothing to reconcile the
    two.
- **So a `yolo host` launch that has a keeper stays resident as its session**
  ([JL-D36](#JL-D36)). It holds the key's session lock, close-on-exec as Go opens it, so the
  agent never inherits it; runs the agent as its child; and exits with the agent's code. That is
  the shape `launchservice.RunAgent` already has for a launch with a service
  ([`agent.go`](../../internal/launchservice/agent.go)): it absorbs SIGINT and SIGQUIT, which the
  terminal delivers to the agent directly, and forwards SIGTERM and SIGHUP. A launch with no
  keeper execs as today. The cost is one resident `yolo` per host session that has a keeper,
  which a bridged host launch already pays.

#### 9.9.3 One keeper per workspace per notch

- **A key** ([§1.1](#11-terms)) at macos-user or `yolo host` is the workspace's container name
  (`FromWorkspace`, [`naming.go`](../../internal/runtime/naming.go)) with the notch appended. So
  one workspace on a Mac can have three keys at once: its podman or Apple Container jail, its
  macos-user sessions and its `yolo host` sessions ([JL-D37](#JL-D37)).
- **A key never spans two notches.** The notch decides the confinement, and what one key's
  sessions share would cross it. The ping box is the sharpest case: a box a container jail
  writes, read by a `yolo host` agent, would let the jail put text in front of an unconfined
  agent, the reverse of [EW-D8](agent-event-watchers.md#EW-D8)'s rule that *"nothing the jail
  writes there can reach the host"*. The same holds between macos-user's sandbox and `yolo host`.
- **Each key has its own arrival lock**, which plays the launch lock's part in
  [§4.2](#42-the-count-a-host-side-session-lock) for that key: an arrival takes it, probes the
  liveness lock, takes its shared session lock, and never waits on the keeper while holding it.
  At macos-user the workspace launch lock keeps guarding the staging window it guards today
  (`holdLaunchLock`), and is a different file.
- **The two locks are taken in one order, and the keeper takes neither.** At macos-user a launch
  holds the workspace launch lock from its staging through its `sudo` steps, which can wait on a
  password prompt without bound, and releases it before the sandboxed command (`RunMacosUser`).
  So an arrival that takes both takes the workspace launch lock first and the key's arrival lock
  second, and one that must wait on a drain releases both before it waits. The keeper holds only
  the arrival lock the fresh launch hands it, until ready, and its teardown never takes the
  workspace launch lock: its exclusive session lock and its liveness lock already exclude every
  arrival. That is today's macos-user teardown, which passes `stopLoopholes` no container name
  and so takes no lock and backs off from nothing (the two calls in
  [`servicessession.go`](../../internal/cli/run/servicessession.go)). A keeper that took the
  container teardown's non-blocking guard instead would leave its dir behind whenever a joiner sat
  at a `sudo` prompt, the leak [§4.2](#42-the-count-a-host-side-session-lock) designs against.
- **A `yolo host` launch from a directory `paths.WorkspaceScopeBreach` refuses as a workspace**,
  such as the home directory a launcher widget starts in
  ([`workspacescope.go`](../../internal/paths/workspacescope.go)), is not a workspace yolo keeps
  state for: `paths.EnsureWorkspaceStateDir` refuses to make a `.yolo` there, so no sidecar is
  enabled for it and no enablement record names it
  ([`agent-event-watchers.md` §12.3](agent-event-watchers.md#123-the-command)). It gets no key
  and no keeper, and runs as today. A sidecar's state and log are in host state no jail mounts in
  any case, never in a workspace's `.yolo/`
  ([EW-D31](agent-event-watchers.md#EW-D31)).

#### 9.9.4 What the keeper owns there

**At macos-user, everything the launch starts outside the sandbox**, which is the macos-user
paragraph of [§2.4](#24-everything-the-first-terminals-process-owns-today) minus what is the
session's ([JL-D38](#JL-D38)):

- the host-services dir, one per key instead of one per session.
  [HD-D1](host-daemon-ownership.md#HD-D1) gave each session its own because two owners wrote one
  dir and the first to end removed it under the other
  ([`servicessession.go`](../../internal/cli/run/servicessession.go)). With one owner that
  reason is gone, and the dir keeps that file's lock-and-sweep shape;
- the loophole fronts and fronted daemons (`startLoopholesDisclosed`), and the credential-view
  registration step;
- the credential doorways ([HS-D15](host-notch-services.md#HS-D15)) and the launch-owned
  services (`startMacosUserServices`), each keeping its lifeline, whose write end the keeper now
  holds ([JL-D32](#JL-D32));
- host-side sidecars and the ping box.

**What stays each session's at macos-user, because a keeper cannot take it.** Everything that
runs as the sandbox account or needs root, which is every `sudo` step of `RunMacosUser`
([`orchestrator.go`](../../internal/macosuser/orchestrator.go)): the Seatbelt profile and the session
env file (both keyed per workspace today, [§9.9.10](#9910-every-feasibility-problem-found) row 6),
the bootstrap, the provisioning stage, the grants that let the sandbox read the
keeper's endpoint files, the guest's jail-daemon supervisor, and the sandboxed command. A keeper
has no terminal, and by default *"sudoers uses a separate record for each terminal"*
([sudoers(5)](https://www.sudo.ws/docs/man/sudoers.man/), `timestamp_type`), so it can neither
reuse the launching terminal's authentication nor ask for its own. And since sudo closes every
inherited descriptor above 2, no lifeline could cross it to end what it started. SOURCED.
- No shipped pack gives the guest supervisor a daemon today
  ([HS-D20](host-notch-services.md#HS-D20)), so nothing a user runs moves out of the keeper's
  reach.
- An agent-side sidecar at macos-user inherits this limit, and so it is not started there
  ([`agent-event-watchers.md` §3.2](agent-event-watchers.md#32-lifecycle-per-side-and-per-notch)):
  it would run under that supervisor, one per session, and nothing could keep it to one instance
  per workspace ([§9.9.10](#9910-every-feasibility-problem-found) row 9). A user-declared one is
  the case; no shipped pack declares any.

**At `yolo host`, the sidecars and the ping box.** Every sidecar is host side there. What a
launch starts for its own agent (the bridge's host half, the managed Codex refresh adapter, and
the AWS doorway once built, [HS-D20](host-notch-services.md#HS-D20)) is
[OQ-JL9](#OQ-JL9)'s: under its leaning, A, it stays the launch's own, and the managed Codex home
keeps NC-D18's machine-wide token and lock; under B the keeper holds each launch's services; under
C it holds the Codex adapter over a Codex home made per workspace.

**The caller tokens of what a keeper holds are the keeper's, not a session's.** The fresh launch
mints them into the plan, as at a container backend ([JL-D20](#JL-D20)), and every session of the
key uses them, as every session of a container jail uses the jail's. That is also what fixes
[§9.9.1](#991-where-the-re-entry-problem-is-real)'s macos-user defects: every entry rewrites the
workspace's per-agent env files with the same listeners and tokens, every session's Codex binds
the same doorway token into the shared `.codex/auth.json`, and those live until the key's last
session.

**A long-lived keeper needs nothing for a re-login.** The aws-auth service mints from the host SSO
login per request. On 2026-09-29 one ran about ten hours across the maintainer's four-hourly SSO
logout and login, with no restart and no failed request
([`sso-backed-bedrock.md` §11](sso-backed-bedrock.md#11-evidence-and-how-to-re-check-it),
done-condition 4, from the service log and the maintainer's report). He repeated it while this
section was reviewed: *"I have something that logs out and logs back in, specifically the SSO
profile, every four hours on the host. And it's been much more than four hours. And I haven't
lost any connectivity."* A keeper holding the aws-auth front for a day of sessions is the same
shape.

**The roster** ([§1.1](#11-terms)) is what makes the keeper's listeners usable by a later launch.
The keeper writes it once its services are up, `0600`, in host state no jail mounts, beside its
liveness lock ([JL-D38](#JL-D38)):

- It names the keeper's pid and build stamp, each endpoint file, each doorway's and service's
  address and caller token, the box, and the sidecars it runs.
- It replaces [JL-D30](#JL-D30)'s start record at these notches, and the keeper removes it at its
  end.
- It holds what the keeper started, never the plan, so it is not the plan-on-disk that
  [OQ-JL7](#OQ-JL7)'s rejected C would have kept. Its tokens are on disk for the key's life, as a container
  jail's are in its endpoint files and shared env file.
- **It carries a contract version** ([JL-D45](#JL-D45)), the counterpart of a container jail's
  contract tags ([`attach-skew-and-contract-guardrails.md`](attach-skew-and-contract-guardrails.md)).
  A keeper can run for a working day, across a `brew upgrade` or a `just install`, and then its
  roster's format and the token variables it names are a channel between two builds. A joiner
  reads a roster only in a contract version it knows. One of another build that knows the version
  joins, and its keeper line says the keeper runs another build. One that does not is refused,
  naming the keeper's pid and build, the live sessions and `yolo stop`, since the key's next fresh
  launch runs the new build. [JL-D20](#JL-D20)'s build-stamp refusal covers only the keeper's
  spawn, and [JL-D39](#JL-D39) only a differing service set.

#### 9.9.5 How a second launch joins

- **What an arrival finds decides what it is** ([JL-D44](#JL-D44)). Under the key's arrival lock,
  an arrival probes the liveness lock, then the session lock:
  - **a held liveness lock** is a live keeper, and the arrival joins it, or waits through a drain
    (below);
  - **a free liveness lock beside a session lock that `LOCK_EX|LOCK_NB` cannot take** is an
    **unkept key**: its keeper died and its sessions run on. The arrival is refused, as
    [OQ-JL7](#OQ-JL7) ruled and [JL-D13](#JL-D13) says of an unkept jail: it names the key's live
    sessions and `yolo stop`, and it never spawns a replacement keeper, which would be that
    question's rejected C, a repair;
  - **both free** is no keeper and no session, and the arrival is the fresh launch. If a roster
    still names a keeper that is gone, because the unkept key's last session died without
    reaping it, the fresh launch first removes that keeper's records as [JL-D30](#JL-D30)'s reap
    would, then gives up its exclusive take and takes its shared one, still under the arrival
    lock.
- **The fresh launch** runs as [§9.1](#91-what-starts-it) says, with its own notch's steps in place of the container's:
  pre-flights, approval, staging and disclosures in the terminal; then its arrival lock, its
  shared session lock, and the keeper's spawn with the plan, the progress pipe, the lifeline and
  the arrival lock. The keeper is ready once its services listen and its roster is written, and
  it releases the arrival lock then, as [JL-D31](#JL-D31) hands over the launch lock.
- **A joining launch** ([§1.1](#11-terms)) is any later launch of the key while that keeper is
  live. Under the arrival lock it probes the liveness lock, takes `LOCK_SH|LOCK_NB` on the
  session lock, reads the roster, and releases the arrival lock. It composes its session's
  environment against the roster and starts nothing the keeper holds. A drain in progress sends
  it through [§4.2](#42-the-count-a-host-side-session-lock)'s wait, as it does a container
  arrival ([JL-D28](#JL-D28)).
  - At macos-user it then runs its own session: its `sudo` steps, its own guest supervisor when
    it has one, and its own sandbox. The grants on the keeper's endpoint files name the one
    sandbox account (a `user:_yolojail` ACE, `sandboxFileReadAce` in
    [`envfile.go`](../../internal/macosuser/envfile.go)), so the fresh launch's session makes
    them and a joiner has none to repeat. That is new: today every grant lands on a fresh file in
    a per-session dir ([HD-D1](host-daemon-ownership.md#HD-D1)), and what a repeated `chmod +a`
    of one ACE on one file does is UNMEASURED, so a joiner grants only a file it finds
    ungranted.
    Two sandboxes in one workspace are two sessions of one key.
  - At `yolo host` it has nothing privileged to run. Two terminals in one workspace are two
    resident sessions of one key, sharing its sidecars and its box.
- **A joiner whose composition needs a host service the keeper does not run** takes the
  container attach's refusal or its acknowledgment hatch (`YOLO_ALLOW_ATTACH_SKEW`, which runs it
  without what it cannot receive), and never its restart ([JL-D39](#JL-D39)). That restart is
  container code: `restartJailForAttach` runs `stopJail`, a `podman` or `container` stop, then
  polls until the container is gone ([`contracttags.go`](../../internal/cli/run/contracttags.go)).
  At these notches the same act would end the key's other sessions and their agents, which at
  macos-user run as the sandbox account. Only `yolo stop` does that ([JL-D44](#JL-D44)), from a
  terminal the user chose to type it in, so the refusal names what the keeper lacks, the live
  sessions and `yolo stop`, after which the next launch is fresh.
  - At macos-user the service set comes from every profiled agent's pairing, not the launched
    command's ([HS-D14](host-notch-services.md#HS-D14)), so a joiner of an unchanged config never
    differs. A changed config, or a `-p` that pairs an agent through a service, can.
  - At `yolo host`, under [OQ-JL9](#OQ-JL9)'s leaning, the keeper holds only what the config
    decides. A sidecar enabled since the keeper started runs from the key's next fresh launch, and
    the joiner's sidecar line says so
    ([`agent-event-watchers.md` §12.5](agent-event-watchers.md#125-what-the-launch-says)).
    Whether the joiner may hand it to the running keeper instead is
    [OQ-EW13](agent-event-watchers.md#OQ-EW13).

#### 9.9.6 What the first terminal sees, and how the keeper ends

- **The first terminal gets its prompt back when its command exits**, with its exit code. When
  other sessions of the key remain it prints one line, [§4.5](#45-what-the-user-sees)'s with the
  notch named: *"this workspace's macos-user host services stay up (keeper pid 48213) for 1 other
  session."* At macos-user this is today's return. At `yolo host` it is the resident session's
  return, with nothing to stop, since the keeper holds it.
- **The last session waits for the teardown and streams it** ([JL-D40](#JL-D40)), as
  [JL-D11](#JL-D11) has it wait at a container backend, within [JL-D34](#JL-D34)'s bound. Only the
  second of JL-D11's two reasons applies here: there is no E3 capture outside a container
  (`captureConfigOnTerminate` is called only in the container arm of
  [`run.go`](../../internal/cli/run/run.go)), but a teardown printed after the prompt returns is
  off the terminal ([OQ-RO3](../reference/report-tiers.md#why-its-this-way)). The wait is bounded
  by the stop graces already in the code: `launchservice.StopGrace`, 2 s, for a launch-owned
  child ([`launchservice.go`](../../internal/launchservice/launchservice.go)), and 5 s for a
  fronted daemon's group ([§2.4](#24-everything-the-first-terminals-process-owns-today)). One code
  path serves every notch.
- **The keeper ends on two of [§9.5](#95-how-it-ends-itself)'s observations.** Before ready, its
  lifeline's EOF or a failed start; after, its exclusive take of the key's session lock. The
  third, the container ending, has no counterpart here.
  - A SIGTERM or SIGINT ends it in order ([JL-D24](#JL-D24)).
  - A SIGKILL leaves the key **unkept**: its sessions run on without its services and are told
    when they end, and a new arrival is refused ([§9.9.5](#995-how-a-second-launch-joins)), as
    [OQ-JL7](#OQ-JL7) ruled for every notch. The key's last session removes its records (the
    host-services dir, the box and the roster) under [JL-D30](#JL-D30)'s rule, with no container to
    stop. Since no arrival can spawn a second keeper meanwhile, nothing else can sit at those
    paths; each is still removed only while the roster names the dead keeper, as
    [JL-D28](#JL-D28) (4) removes the owner-PID file only while it names its keeper.
- **`yolo stop` ends a key at these notches too** ([JL-D44](#JL-D44)), because the refusal
  [OQ-JL7](#OQ-JL7) ruled names it as the remedy. `yolo stop --at host` picks the `yolo host` key,
  the spelling `yolo apply` already takes, and a bare `yolo stop` picks the key of the backend a
  launch in that workspace would use. It sends SIGTERM to the key's keeper, which ends in order
  ([JL-D24](#JL-D24)), and to each live session of the key:
  - a `yolo host` session forwards it to its agent, as `launchservice.RunAgent` does
    ([`agent.go`](../../internal/launchservice/agent.go));
  - a macos-user session forwards it to its `sudo` child, which *"will relay signals it receives
    to the command"* ([sudo(8)](https://www.sudo.ws/docs/man/sudo.man/)), then stops its guest
    supervisor as it does today, by signalling that `sudo`'s process group (`startBackgroundReal`,
    [`real.go`](../../internal/macosuser/real.go)). That needs the signal arm the macos-user
    launcher lacks today (this section's last bullet). No step needs `sudo`: the host user signals its own
    launchers, and `sudo`, whose real user is the host user's, relays. INFERRED; unmeasured on a
    Mac.

  It then streams the teardown within [JL-D34](#JL-D34)'s bound and, for an unkept key, runs the
  reap itself, as [JL-D30](#JL-D30) has it do for a jail. It finds the sessions through
  **per-session records**: each session of a key at these notches writes one, naming its pid and
  that process's start time, in the key's host state no jail mounts, and removes it as it exits.
  A SIGKILLed session's record is known stale by its pid and start time. The records name the
  live sessions in a refusal and tell `yolo stop` whom to signal; they never count
  ([JL-D2](#JL-D2)), and a session with no record, an older yolo's, is left alone. `kill <pid>`
  on the keeper still ends it in order, and JL-D34's bounded waits name the pid for that.
- **Signals.** [§9.7](#97-signal-handling-sig-proxy-and-a-pane-close)'s rows hold: the keeper is
  in a session of its own, so no pane close reaches it. macOS has no systemd, so the keeper has
  Setsid alone there, as [§9.8](#98-per-notch-podman-apple-container-macos-user-yolo-host) says
  for podman on macOS; a logout's SIGTERM ends it in order (INFERRED).
- **The macos-user session launcher gets a signal arm** ([JL-D40](#JL-D40)). Today it has none: it
  runs the sandboxed command as a plain child in the launcher's process group, ignoring `onTerminate`
  ([`proxy_other.go`](../../internal/cli/run/proxy_other.go)), and nothing in `internal/cli` or
  `internal/macosuser` installs a handler for it. So a terminal's SIGINT, which reaches the whole
  foreground group, kills the launcher by Go's default, while a sandboxed program that survives
  SIGINT (a REPL in cooked mode, or a program under a shell with no job control) runs on. Its
  session lock drops under a live session, and the keeper may drain under it, the polarity
  [JL-P3](#JL-P3) forbids. SOURCED for the code; the consequence INFERRED. So the launcher takes
  `launchservice.RunAgent`'s shape, as a `yolo host` session does ([JL-D36](#JL-D36)): it absorbs
  SIGINT and SIGQUIT, which reach the command anyway, directly or through `sudo`; it forwards
  SIGTERM and SIGHUP to its `sudo` child, which relays them
  ([sudo(8)](https://www.sudo.ws/docs/man/sudo.man/)); and it exits with the command's code once
  the command has exited. A pane close then ends the command first and the lock after it. This is
  the arm [§7](#7-what-i-would-build-in-order) step 4 owes the Mac container backends, and it
  lands with step 5, before a macos-user session is counted.

#### 9.9.7 What it discloses

- **The fresh launch prints [JL-D21](#JL-D21)'s line before the spawn**, naming the notch and its
  sessions instead of a container. Suggested wording, with the services illustrative:

  ```text
  keeper: yolo internal daemon jail-keeper will hold this workspace's macos-user host services (the claude-oauth-broker front, the aws-auth doorway, the ci-watch sidecar, the ping box) until its last macos-user session leaves; log: <host state>/jail-keeper-<key>.log
  keeper: started, pid 48213
  ```

- **Every service, doorway and sidecar disclosure still precedes the spawn**
  ([§9.6](#96-what-it-discloses-at-launch-and-where-its-output-goes)'s warning, [JL-D6](#JL-D6)).
- **A joining launch prints one line naming the keeper it joined**, followed by those
  disclosures in their existing words, each marked as held by that keeper ([JL-D41](#JL-D41)). A
  joining terminal is served by host code it did not start, and a launch has no quiet mode.

  ```text
  keeper: joined yolo internal daemon jail-keeper (pid 48213), which holds this workspace's macos-user host services for 2 sessions; log: <host state>/jail-keeper-<key>.log
  ```

#### 9.9.8 A launch with nothing long-lived

**No keeper is spawned when a launch's plan holds nothing that a keeper at its notch holds**
([JL-D42](#JL-D42)), which [§9.9.4](#994-what-the-keeper-owns-there) lists per notch: at
macos-user anything the launch would start outside the sandbox (a front, a fronted daemon, a
doorway, a launch-owned service), and at either notch a sidecar or the ping box. At `yolo host` a
launch's own services are on that list only if [OQ-JL9](#OQ-JL9) is ruled B or C. Under its
leaning a bridged `yolo host -p codex -- claude` with no sidecar gets no keeper and stays resident
as its services' parent, as today. Such a launch runs exactly as today: `yolo host` execs its
agent, or stays resident for its own services, and macos-user runs its sandbox with nothing to
stop. It takes no session lock, and a
keeper that a later launch of the key spawns neither counts nor serves it, since it composed
nothing from that keeper. Reasons:

- a keeper with nothing to hold would be only a spawn, a `ps` line and a disclosure line, the
  *"process with nothing to outlive"* in [OQ-JL5](#OQ-JL5)'s own setup;
- *"nothing new exists while no jail runs"* ([§1](#1-the-verdict-and-the-words-it-uses)) holds at
  every notch.

**This is not a backend carve-out.** The rule is the same at every notch: a container launch's
plan always holds the container, so every container launch has a keeper, and a macos-user
launch has one whenever any loophole is active, since each active loophole's front is on its
list: a goroutine the keeper runs there for every session of the key.

**Whether a ping box alone makes a keeper** follows from
[OQ-EW11](agent-event-watchers.md#OQ-EW11). Under its leaning (B) a box exists only where a
sidecar will start, so it never comes alone. Under its A every launch has a box, so every launch
at every notch has a keeper, and every `yolo host` launch stays resident.

#### 9.9.9 What changes for a host service's lifetime

[OQ-HS3](host-notch-services.md#OQ-HS3) ruled that *"a host service lives for the launch that
starts it"*. From now on it reads this way ([JL-D43](#JL-D43)):

- **For what a keeper holds, "the launch" is the key's life**, from the fresh launch that spawned
  the keeper to the key's last session. At a container backend that is the jail, which is
  [HD-R1](host-daemon-ownership.md#HD-R1)'s unit ([JL-P4](#JL-P4)). At macos-user and `yolo host`
  it is the sessions of one workspace at that notch.
- **For what a session keeps, it reads literally**: macos-user's sandbox-account processes, and,
  under [OQ-JL9](#OQ-JL9)'s leaning, a `yolo host` launch's own services, whose Codex adapter keeps
  the managed home's machine-wide token ([NC-D18](../plans/notch-convergence.md#NC-D18)).
- **What HS3 rejected stays rejected.** Nothing outlives the key's last session. `yolo host apply`
  still renders no per-launch address, and `yolo host env` still refuses a bridged profile, since
  it is no session and nothing would hold the lock for its output. The tokens are minted per
  keeper, never one that outlives every launch. And an agent started without yolo still lacks
  the feature.
- **[HS-D15](host-notch-services.md#HS-D15), the doorway rule, reads the same way.** That ruling
  is about where a doorway listens: on the loopback the agent sees, so inside a container's
  network namespace, and outside the sandbox at macos-user and at `yolo host`. Its words *"the
  launch opens the doorway outside as a launch-owned listener"* name the owner those notches had
  when it was ruled. At macos-user the owner is now the keeper ([JL-D38](#JL-D38)), and the
  doorway still listens on the machine's loopback, outside Seatbelt, behind the caller token the
  plan carries. At `yolo host` the Codex adapter stays launch-owned under [OQ-JL9](#OQ-JL9)'s
  leaning, and the AWS doorway is not built ([HS-D20](host-notch-services.md#HS-D20)).
- **[`host-notch-services.md` §4.4](host-notch-services.md#44-lifetime)'s steps move to the keeper
  for what it holds.** Order and readiness are unchanged. "The agent exits" becomes "the key's
  last session exits". "The launch dies without cleanup" becomes "the keeper dies", with the same
  lifeline. Item 6's *"two host launches … share nothing but the OpenAI broker"* was already
  untrue of the Codex adapter's home after NC-D18, and it becomes, at macos-user, two sessions
  sharing one keeper's services.

#### 9.9.10 Every feasibility problem found

| # | Problem | Evidence | What the design does |
|---|---|---|---|
| 1 | An inherited lock descriptor miscounts a `yolo host` session both ways: a child that inherits it keeps the session after its command exits, and a program that closes it (OpenSSH's `ssh`) reads as gone while it runs | MEASURED on Linux, [§9.9.2](#992-what-a-session-is-there-and-why-yolo-host-stays-resident)'s table; the same semantics SOURCED on macOS; claude's and codex's native binaries UNMEASURED | a launch with a keeper stays resident ([JL-D36](#JL-D36)) |
| 2 | The keeper cannot run `sudo`, so at macos-user it can neither start nor end what runs as the sandbox account | [sudoers(5)](https://www.sudo.ws/docs/man/sudoers.man/): one record per terminal, and every descriptor from 3 closed; SOURCED | those stay each session's, and what ends them runs where a terminal is: the session's own launcher, or `yolo stop` signalling it ([JL-D44](#JL-D44)); a joiner is never offered a restart ([JL-D39](#JL-D39)). A scoped `NOPASSWD` sudoers rule installed at setup would let the keeper run the steps itself, and is not taken: the tree installs none, the macos-user runbook promises *"no NOPASSWD rule is ever installed"* ([`mac-macos-user-e2e.md`](../plans/runbooks/mac-macos-user-e2e.md)), and the staging argv's root copy and move make such a rule *"a write-anything-as-root primitive unless the rule pins the source path exactly"* ([`macos-revival-and-distribution-plan.md`](../plans/macos-revival-and-distribution-plan.md)). No shipped pack gives the guest supervisor a daemon today ([HS-D20](host-notch-services.md#HS-D20)) |
| 3 | macos-user's per-agent env files and Codex's `auth.json` are the workspace's, and today name one session's listeners and tokens, so a second session breaks a running Codex's refreshes and a later agent's pointers | `agentenvfiles.go`, [`packs/codex/pack.json`](../../packs/codex/pack.json)'s workspace-scoped `.codex`, [HS-D18](host-notch-services.md#HS-D18); SOURCED; the failures INFERRED | the keeper's listeners and tokens are what every entry writes ([§9.9.4](#994-what-the-keeper-owns-there)) |
| 4 | The ping box at macos-user must be written by two accounts, the host user's sidecars and the sandbox's `yolo notify`, and read by the sandbox's deliverers | the per-file grants a launch makes are read-only (`sandboxFileReadRights`, [`envfile.go`](../../internal/macosuser/envfile.go)), but setup already provisions the two-account write shape: a host-user-owned, group-`_yolojail`, setgid `2770` dir with inheriting ACEs, the host user being in that group (`SharedRootProvisionCommands`, `WorkspaceACLAces` and `CreateUserCommands` in [`macosuser.go`](../../internal/macosuser/macosuser.go)); SOURCED | the box goes in a dir of that shape outside every workspace, which the keeper, as the host user, can create without `sudo`; the box in it is unbuilt, and that shape's use for it is UNMEASURED on a Mac |
| 5 | macOS has no `/proc/self/exe` and no systemd | [§9.8](#98-per-notch-podman-apple-container-macos-user-yolo-host) | the build-stamp refusal ([JL-D20](#JL-D20)) and Setsid alone, as for podman on macOS |
| 6 | macos-user's session env file and its Seatbelt profile are described as per session but keyed per workspace (`SandboxEnvFile(cname, …)`, `<stateDir>/env/<cname>.env`; `SessionProfilePath(cname, …)`, `profile-<cname>.sb`), and every launch rewrites both; each session also removes the env file at its end. `RunMacosUser` releases the workspace lock before the sandbox reads either, so a second session writing them in that gap would hand the first sandbox the second's environment, which is scoped to another launched agent, or its profile | [`envfile.go`](../../internal/macosuser/envfile.go), [`macosuser.go`](../../internal/macosuser/macosuser.go), and `RunMacosUser`'s `InstallRootFile`, deferred removal and `release()` before `RunWithProxy`; SOURCED; the race INFERRED | both stay the session's, so the keeper does not fix them; a per-session name would, and it is recorded here as found |
| 7 | A `yolo host` launch from the home directory is not a workspace yolo keeps state for, so no sidecar is enabled for it | `paths.WorkspaceScopeBreach`, which `paths.EnsureWorkspaceStateDir` enforces | no key and no keeper there ([§9.9.3](#993-one-keeper-per-workspace-per-notch)) |
| 8 | State shared across workspaces cannot be owned by a per-workspace keeper. Every `yolo host -- codex` on the machine runs on one managed Codex home keyed on the pack, whose `auth.json` carries one caller token that its live launches share. Two workspaces' keepers, each minting a token into that file, would bring NC-D18's breakage back | `prepare` and `sharedCallerToken` in [`host.go`](../../internal/openaiauthhost/host.go), [NC-D18](../plans/notch-convergence.md#NC-D18); SOURCED | under [OQ-JL9](#OQ-JL9)'s A and B the home keeps NC-D18's machine-wide token and lock; its C makes the home per workspace, the scope `.codex` has in a jail and at macos-user. The host-wide brokers stay outside every keeper, as [HD-R1](host-daemon-ownership.md#HD-R1)'s build leaves them |
| 9 | An agent-side sidecar at macos-user cannot be kept to one instance per workspace. It would run under each session's guest supervisor, which runs as the sandbox account and is started by that session's `sudo`; only keepers take [EW-D25](agent-event-watchers.md#EW-D25)'s one-instance lock, and a keeper cannot start what runs as the sandbox account (row 2), so two sandboxes would each ping | [EW-D25](agent-event-watchers.md#EW-D25), and the guest supervisor's start in `RunMacosUser` ([`orchestrator.go`](../../internal/macosuser/orchestrator.go)); SOURCED | not started at macos-user, and each launch says so; the same sidecar declared host side runs there under the keeper. It covers user-declared sidecars, which row 2's "no shipped pack" does not |

None of these blocks the ruling, and none is an exception to it. Each is a limit on what a keeper
can hold at that notch, stated with its reason (1, 2, 5, 7, 8 and 9), work the design owes (4), or a
defect found on the way: the keeper fixes 3, and 6 is left for its own fix.

---

## 10. Open Questions

1. ✅ <a id="OQ-JL1"></a>**[OQ-JL1](#OQ-JL1): Who owns a shared jail's host services once the
   first terminal may leave?** Directed 2026-09-29.

   **The setup, as it was asked.** Matt has two terminals in one workspace's jail. The first
   terminal's `yolo` process *is* the jail's host services
   ([§2.4](#24-everything-the-first-terminals-process-owns-today)), so when its agent quits every
   other session dies. The question was which process holds those services instead:

   - **A.** A keeper per jail, started with the jail (O3 in [§3](#3-the-options)).
   - **B.** A successor, spawned only when the first launcher quits with sessions left (O2).
   - **C.** Ownership migrates to a surviving terminal (O4).
   - **D.** The first launcher holds the jail after its own agent quits, and survives its window
     closing (O1). This was the leaning.

   **Answer:**
   > **Directed 2026-09-29: a small background process, and never a first terminal that
   > waits.** The maintainer: *"I don't want a solution where the first terminal waits. The idea
   > is to get my terminal back and reuse it. I would even like to be able to reenter the jail
   > with that first one, like change agents in that tab or something like that, so you could
   > hand ownership over. I don't know if that's possible. I think it is going to have to be some
   > sort of small background process. But then it needs to know how to end itself as well."*
   > D, the leaning, is the waiting terminal, and he rejected it in those words. C has no
   > background process, so it goes with it.

   What the direction produced is [§9](#9-the-keeper-design-2026-09-29):

   - **Which background process:** a keeper started with the jail (A), not B's successor handed
     the jail at quit ([JL-D14](#JL-D14)).
   - **Getting the terminal back and reusing it:** the first session is an exec like every other,
     and its launcher exits with its agent ([§9.3](#93-how-the-first-terminal-gets-its-prompt-back)).
   - **Re-entering from that tab and changing agents:** an ordinary attach. "Hand ownership over"
     needs no mechanism, because no terminal is ever the owner
     ([§9.4](#94-how-re-entering-works-and-changing-agents), [JL-D22](#JL-D22)).
   - **Ending itself:** three observations, never a timer
     ([§9.5](#95-how-it-ends-itself), [JL-D17](#JL-D17)).

   What it left for a ruling is [OQ-JL5](#OQ-JL5) to [OQ-JL8](#OQ-JL8).

2. ✅ <a id="OQ-JL5"></a>**[OQ-JL5](#OQ-JL5): Do `yolo host` and macos-user get a keeper too,
   or only the backends where sessions share a jail?**

   **The setup.** Matt runs two container-jail tabs in one workspace on his Linux box, and on his
   Mac he runs `yolo host -- claude` in one tab and a macos-user `yolo -- codex` in another. On
   the Linux box, quitting the first agent leaves the keeper holding the jail for the second tab.
   On the Mac nothing needs keeping. A host launch owns its services as its own children and
   shares them with no other launch, and macos-user has no attach, so every invocation is its own
   sandbox with its own services ([§2.4](#24-everything-the-first-terminals-process-owns-today)).
   Both already hand the prompt back when their agent exits. **Why it is a question:** the
   notches were put on one code path per concern
   ([NC-D1](../plans/notch-convergence.md#7-decision-ledger)), and a second shape of host-service
   ownership is a ruling, not an implementation choice (the reason [JL-D8](#JL-D8) was
   withdrawn). It also decides how [OQ-HS3](host-notch-services.md#OQ-HS3)'s *"a host service
   lives for the launch that starts it"* reads from now on.

   - **A. A keeper at every notch.** Every `yolo host` launch that starts a service, and every
     macos-user launch, also spawns `yolo internal daemon jail-keeper`. Matt sees one more process
     in `ps` and one more disclosure line per launch, and nothing else changes: the agent behaves
     the same and the prompt returns the same. The keeper there keeps nothing alive that the
     launch would not, because no second session can join.
   - **B. A keeper wherever sessions share a jail, which today means the container backends.**
     The rule is written once: a jail's host services are owned by one process that lives as long
     as the sessions sharing that jail. At `yolo host` and on macos-user that set is always one
     session, so the launch is that process, and Matt sees no change there. HS3 then reads "per
     launch" at those notches and "per jail" at the container backends, which is HD-R1's unit.

   <!-- vantage: oq id=OQ-JL5 leaning="B: the code that starts, discloses and stops the services is one path at every notch; whether sessions can share a jail decides which process runs it, the way the notch decides the confinement, and a keeper where nothing can share would be a process with nothing to outlive." -->

   _Leaning:_ **B.** The code that starts, discloses and stops host services stays one path at
   every notch. What differs is which process runs it, and that is decided by whether sessions
   can share a jail, the same way the notch decides the confinement. A keeper at `yolo host`
   would be a process with nothing to outlive. **The trap:**
   [`agent-event-watchers.md`](agent-event-watchers.md)'s
   [EW-D19](agent-event-watchers.md#EW-D19) (designed, not built) would give the host notch and
   macos-user one shared sidecar per workspace, passed from launch to launch by a kernel lock.
   That is sharing, so under B's own rule it would want a keeper there, and the lock handoff is
   the migration shape [JL-D14](#JL-D14) rejected for container jails. If EW-D19 lands as
   written, B must be applied to it rather than grow a second rule.

   **Answer:** **A, a keeper at every notch, if supportable** (maintainer, 2026-09-29). The set the
   maintainer answered from lettered these two options the other way round, so the "B" said
   there is this A; the words are the ruling:
   > "I think they have to get a keeper if that's something that we can support because otherwise we're just going to be right back at the same issue right like we have some long-lived daemons the brokers that need to be launched and if you can re-enter i mean tell me if this is wrong like if you can re-enter like you're just gonna have all the same problems i don't see how uh it changes jail or not container not container"

   Read as: one ownership model for a workspace's long-lived host services (the brokers' doorways,
   aws-auth, sidecars) at every notch, with no backend carved out; a concrete feasibility problem is
   to be named, never silently turned into an exception. The workhorse's feasibility read: nothing
   found blocks it. At `yolo host`, where the launch execs the agent, a session's liveness can be a
   lock descriptor the agent inherits across that exec; macos-user's launch-owned listeners move to
   the keeper the same way. [EW-D19](agent-event-watchers.md#EW-D19)'s lock handoff is then
   replaced by the keeper, as this question's trap asked, not kept as a second rule. How the keeper
   runs at those two notches is being designed, and its questions come back.

   **Designed 2026-09-29 in [§9.9](#99-the-keeper-at-yolo-host-and-macos-user)** (JL-D36 to
   JL-D43). The feasibility read above was half right: an inherited lock descriptor does survive
   the exec, but it was measured to miscount a session both ways, so a `yolo host` launch that has
   a keeper stays resident instead ([§9.9.2](#992-what-a-session-is-there-and-why-yolo-host-stays-resident)).
   On *"tell me if this is wrong"*: it is right. Re-entry brings the same problem at macos-user
   today, through the workspace's per-agent env files and Codex's `auth.json`, and at both notches
   for the sidecar feature. At `yolo host` it has already been met twice: two host Codex sessions
   broke each other's refreshes until [NC-D18](../plans/notch-convergence.md#NC-D18) gave them one
   shared token, and Codex's own background server outlived its launch until
   [OQ-CDX1](../research/codex-background-service.md#OQ-CDX1) turned it off. Only the bridge's host
   half shares nothing. Whether the keeper takes over the host's per-launch services, whose Codex
   state belongs to the machine rather than the workspace, is [OQ-JL9](#OQ-JL9)
   ([§9.9.1](#991-where-the-re-entry-problem-is-real)).
   [EW-D19](agent-event-watchers.md#EW-D19) is replaced by
   [EW-D25](agent-event-watchers.md#EW-D25).

3. ✅ <a id="OQ-JL6"></a>**[OQ-JL6](#OQ-JL6): When the tab you quit was the jail's only
   session, does switching agents in that tab reuse the jail?**

   **The setup.** Matt has one tab open in a workspace and runs `yolo -- claude`. Nothing else is
   in the jail. He quits Claude and types `yolo -- codex` in the same tab. Under
   [JL-D12](#JL-D12) the quit was the jail's last session, so the keeper tore the jail down while
   Claude's tab waited for it ([JL-D11](#JL-D11)), and the Codex launch boots a new jail. Had a
   second tab been open, the same switch would have been an attach into the running jail
   ([§9.4](#94-how-re-entering-works-and-changing-agents)). **Why it is a question:** the
   maintainer asked to *"reenter the jail with that first one, like change agents in that tab"*,
   and JL-D12, which rules out a grace window, was ledgered before that request. A fresh jail also
   starts with empty per-launch scratch: `/tmp`, `/var/tmp` and the nested podman store
   (`ScratchMountArgs`, [`runmount.go`](../../internal/cli/run/runmount.go)), and every in-jail
   process restarts.

   - **A. As ledgered: the switch is a fresh launch.** Matt sees Claude's quit print the
     teardown, then Codex's launch print a boot: about 2.2 to 2.6 s of host-side work plus the
     in-jail boot, against 0.12 to 0.20 s for an attach (this workspace's records,
     [§9.4](#94-how-re-entering-works-and-changing-agents)). `/tmp` and the nested podman store
     start empty. To switch agents without any of that, he opens the jail's shell with a bare
     `yolo` and starts agents from it; the shell is the session, so the jail lives across
     switches.
   - **B. A short linger after the last session, about 10 s.** Claude's quit returns the prompt
     at once, and a `yolo --` in the workspace within the linger attaches. Matt sees an instant
     switch that keeps `/tmp`. The cost: every last quit leaves the jail up for the linger;
     `yolo config diff` and any launch have to wait for the drain after it, or E3's race reopens;
     the last quit no longer shows the teardown; and the lifecycle gets its one timer.

   <!-- vantage: oq id=OQ-JL6 leaning="A: the jail's own shell already switches agents with no teardown, and B buys a few seconds and /tmp for a timer in the lifecycle and a capture that runs after the prompt returns." -->

   _Leaning:_ **A.** The jail's own shell already switches agents with no teardown and no new
   machinery. B buys a few seconds and a warm `/tmp`, and pays with the lifecycle's only timer
   and a config capture that runs after the prompt has returned. **The trap:** if switching
   agents in a lone tab is how Matt normally works, A charges a boot on every switch, and B's
   linger is cheap in wall time by comparison. Then B is the better answer, and JL-D11 and
   JL-D12 are reopened with it.

   **Answer:** **A** (maintainer, 2026-09-29):
   > "I think this is your leaning. As soon as the last one exits, the jail shuts down. So no, with one tab there is no way to re-enter the same jail."

   As ledgered: [JL-D11](#JL-D11) and [JL-D12](#JL-D12) stand, with no linger. A lone tab's switch is
   a fresh launch; the jail's own shell switches agents with no teardown.

4. ✅ <a id="OQ-JL7"></a>**[OQ-JL7](#OQ-JL7): When the keeper is killed while sessions run,
   what do those sessions and the next arrival see?**

   **The setup.** Matt has Claude in one tab and Codex in another, both in one workspace's jail.
   The keeper is killed outright: the kernel's OOM killer picks it during a memory spike, or a
   `kill -9` meant for something else hits it. Both agents keep running, because nothing in a
   session needs the keeper to be alive. What the keeper held died with it
   ([§9.5](#95-how-it-ends-itself)): the credential fronts, so Claude's next login refresh through
   the broker fails; the port forwards and the fronted daemons, which the design gives an end of
   their own so that none outlives the keeper unowned ([JL-D32](#JL-D32)); and the cgroup
   delegate. A third tab's `yolo -- claude` would enter a jail with none of them. **Why it is a
   question:** every answer gives up something. Letting the agents run on leaves them degraded,
   stopping the jail throws away their work to be honest sooner, and repairing the jail puts a
   second owner on it, which [JL-P4](#JL-P4) forbids. What [JL-D13](#JL-D13), [JL-D18](#JL-D18)
   and [JL-D30](#JL-D30) record today is A; the first two were written before this was filed as
   a question.

   - **A. The sessions run on without host services, and are told when they end.** Matt sees
     both agents keep working until one needs a host service, and then that agent's own error (a
     failed refresh, a dropped port). Each tab's quit prints *"the jail's keeper died at 14:02;
     its host services were down from then"*. The third tab's `yolo` is refused, naming the two
     live sessions and offering `yolo stop`, which tears the whole jail down. Whichever of the
     two tabs quits last reaps the jail itself on the way out, on every backend
     ([JL-D30](#JL-D30)).
   - **B. The jail stops at once.** Each session's launcher waits on the keeper's liveness lock,
     so the first to see it come free runs the `yolo stop` path. Matt sees both agents end within
     a second, each tab printing why; `yolo -- <agent>` then launches fresh.
   - **C. The jail is repaired.** The third tab's `yolo`, or a surviving tab's launcher, starts a
     replacement keeper. It restarts the host services from the launch's plan and re-publishes
     their endpoint files, which in-jail clients re-read on their next dial, so the agents
     recover. Matt sees one line saying the keeper was replaced, and the third tab attaches. The
     costs are out of sight: the plan, which carries the jail's per-launch caller tokens, stays on
     disk for the jail's life instead of being read once and removed ([JL-D20](#JL-D20)); the
     repair runs only after a crash, so it is the path that rots; a replacement from a newer build
     (after `just install`) runs an older launch's plan; and JL-P4 is reopened.

   <!-- vantage: oq id=OQ-JL7 leaning="A: a keeper death is rare, since nothing a terminal does reaches it; A loses nobody's work and makes the state known rather than guessed, B trades every agent's work for an earlier message, and C reopens JL-P4 and keeps the jail's secrets on disk for a crash path." -->

   _Leaning:_ **A.** Nothing a terminal does reaches the keeper
   ([§9.7](#97-signal-handling-sig-proxy-and-a-pane-close)), and an orderly signal ends the jail
   in order ([JL-D24](#JL-D24)), so only an outright kill leaves an unkept jail. A loses nobody's
   work and makes the state known rather than guessed. B trades every agent's work for an earlier
   message, and C reopens JL-P4 and keeps the jail's secrets on disk for a path that runs only
   after a crash. **The trap:** if keeper deaths are not rare where it matters (a Mac, where it
   has no scope of its own, or a host whose systemd-oomd picks it), A's degraded agents fail
   mid-task in confusing ways, and B's plain stop would be kinder.

   **Answer:** **A** (maintainer, 2026-09-29): *"93A."* The sessions run on without host services and
   are told when they end; a new arrival is refused, naming the live sessions and `yolo stop`; the
   container is reaped once they leave.

5. ✅ <a id="OQ-JL8"></a>**[OQ-JL8](#OQ-JL8): When a pane or window closes, does that
   session's agent end, or keep running?**

   **The setup.** Matt works in herdr with two panes on one workspace: Claude in pane 1, halfway
   through a long refactor, and Codex in pane 2. He closes pane 1. herdr hangs up everything in
   that pane and SIGKILLs what is left within half a second, so pane 1's `yolo` is gone. Claude's
   process is not in the pane: conmon holds its exec session, and in the measurement the process
   an exec started ran on after its client died ([§3.1](#31-what-a-pane-close-does-measured)). So
   Claude ends only if yolo ends it. Today an attached pane's agent runs on headless
   ([§2.3](#23-four-defects-found-on-the-way) item 1), and closing the first pane stops the whole
   jail. **Why it is a question:** closing a terminal ends the program in it on a host, while a
   tmux user expects work to go on. And yolo cannot bring a headless agent back to a terminal:
   podman has no way to re-attach to an exec session
   ([§9.4](#94-how-re-entering-works-and-changing-agents)).

   - **A. The agent ends with its pane.** The session's launcher hangs up its own in-jail
     process tree before it exits ([JL-D4](#JL-D4)). Matt sees pane 1's Claude gone and pane 2
     carrying on, and the jail stays up for pane 2. A new pane's `yolo -- claude` attaches, and
     whatever Claude keeps on disk is there. A window close that only detaches a herdr client
     changes nothing, since the pane stays.
   - **B. The agent keeps running headless, and keeps the jail up until it exits.** Matt sees
     nothing of it: no pane shows its output, which is lost, while files in the workspace keep
     changing. The jail's shell (`yolo`, then `ps`) shows it, and `yolo stop` ends it with
     everything else. The jail stays up after the last pane closes, for as long as that agent
     runs. For that, the count must include a process no terminal holds, which
     [JL-P2](#JL-P2) rules out: an in-jail process would keep the jail's credential services
     alive after every human has left.
   - **C. The agent keeps running headless, but uncounted.** This is what an attached pane does
     today. Claude works on until it finishes or until the last counted session leaves, and then
     dies with the jail, mid-edit if it was editing.

   <!-- vantage: oq id=OQ-JL8 leaning="A: closing a terminal ends its program, as on the host; herdr already keeps panes alive across a window close, so a pane close is deliberate, and B needs an in-jail process to hold the jail open, which JL-P2 rules out, while C is today's defect." -->

   _Leaning:_ **A.** It is what closing a terminal does on the host. herdr already keeps an agent
   alive across a window close, because closing herdr's window only detaches its client
   ([`herdr-integration.md` §3.4](../research/herdr-integration.md#34-closing-a-pane-is-a-kill)),
   so a pane close is a deliberate act. B needs an in-jail process to hold the jail open, which
   [JL-P2](#JL-P2) forbids, and C is defect 1 of [§2.3](#23-four-defects-found-on-the-way). A
   hangup can miss: a launcher SIGKILLed before its arm runs leaves its agent headless, and on
   local Linux podman the death pipe of JL-D4 closes that gap. **The trap:** if Matt closes panes
   to tidy up while he expects agents to keep working (a tmux habit), A ends work he meant to
   keep. Then B is what he wants, and JL-P2 needs a count the host can still bound, for example a
   headless session counted only until its agent exits and listed by `yolo ps`.

   **Answer:** **A** (maintainer, 2026-09-29): *"94A."* The agent ends with its pane: the session's
   launcher hangs up its own in-jail processes before it exits, and the other sessions and the jail
   carry on.

6. 💬 <a id="OQ-JL9"></a>**[OQ-JL9](#OQ-JL9): At `yolo host`, does the keeper also hold what one
   launch starts for its own agent?** *Re-asked 2026-09-29.* The first draft said nothing another
   host launch reads names these services; that is untrue of the Codex refresh adapter.

   **The setup.** On his Mac, with `ci-watch` enabled in yolo-jail, Matt runs
   `yolo host -p codex -- claude` in one yolo-jail terminal and `yolo host -- codex` in another,
   and a third `yolo host -- codex` in his dotfiles repository. The yolo-jail keeper at `yolo host`
   holds `ci-watch` and the ping box, which both yolo-jail terminals share
   ([§9.9.4](#994-what-the-keeper-owns-there)). Each launch also starts something for its own
   agent, and the two kinds differ in what they share:

   - **The bridge's host half**, which the first terminal's claude needs, is that launch's alone.
     Its port and secret go into that claude's environment and into no file another launch reads
     ([§9.9.1](#991-where-the-re-entry-problem-is-real)).
   - **The Codex refresh adapter** listens on a port of each launch's own, but all three Codex
     sessions, in both workspaces, run on one managed Codex home in yolo's machine-wide state, so on
     one `auth.json`, which Codex rereads before every refresh. Re-entry already broke it once: each
     launch wrote a caller token of its own there, the newest replaced the others, and their
     refreshes failed. Since [NC-D18](../plans/notch-convergence.md#NC-D18) the home's live launches
     share one token, counted by a lock of the home's own. Codex's own background server was the
     same problem again, and [OQ-CDX1](../research/codex-background-service.md#OQ-CDX1) turned it
     off.

   **Why it is a question:** the ruling's reading names *"the brokers' doorways"* among what a
   keeper owns, with no backend carved out. The bridge has nothing to share. What the Codex adapter
   shares belongs to the machine, while a keeper belongs to one workspace: two workspaces' keepers,
   each writing a token into the one `auth.json`, would bring NC-D18's breakage back
   ([§9.9.10](#9910-every-feasibility-problem-found) row 8). So the keeper either leaves the adapter
   alone, or the managed Codex home has to change what it is.

   - **A. They stay the launch's own, as today.** Each terminal starts what its agent needs and
     stops it when its agent exits ([`host-notch-services.md` §4.4](host-notch-services.md#44-lifetime)),
     and the Codex home keeps NC-D18's shared token and lock. Matt sees each terminal's service line
     as today, and the keeper line names only `ci-watch` and the box. The cost: two things count
     sessions at `yolo host`, the keeper per workspace and NC-D18's lock per machine.
   - **B. The keeper holds them, still one per launch.** Each launch prints its service lines and
     hands its services to the keeper as an addition to its plan; the keeper starts each, and stops
     it when that launch's session ends. Matt sees one keeper in `ps` holding every host service of
     the workspace. The cost: a lifeline from each session to the keeper, a second path from a
     disclosure to a start (the plan plus additions, against [JL-D20](#JL-D20)'s one plan), and the
     Codex token still follows NC-D18's machine-wide lock, since the home is still shared across
     workspaces. The processes move and the ownership does not.
   - **C. The keeper holds the Codex adapter, over a Codex home made per workspace, and the bridge
     stays the launch's own.** A workspace's host Codex sessions share one adapter and one token the
     keeper mints, as a jail's sessions do, and NC-D18's lock retires for them. Matt sees the Codex
     adapter on the keeper's line. The cost: Codex's sessions and history at `yolo host` split per
     workspace, as they already are in a jail and at macos-user, where `.codex` is a per-workspace
     `state` dir ([`packs/codex/pack.json`](../../packs/codex/pack.json)); today's machine-wide home
     is left behind or moved once; every `yolo host -- codex` gets a keeper; and one started from
     the home directory, which has no key ([§9.9.3](#993-one-keeper-per-workspace-per-notch)),
     still needs a home and a lock of its own.

   <!-- vantage: oq id=OQ-JL9 leaning="A: the keeper holds what a workspace's sessions share, at the scope they share it; the bridge is shared by no one, and the Codex home is shared by the whole machine, where NC-D18 already counts it; B moves processes without moving ownership, and C splits Codex's history per workspace to retire one count and still leaves the home-directory launch its own." -->

   _Leaning:_ **A.** The keeper holds what a workspace's sessions share, at the scope they share
   it: every container host service, everything macos-user starts outside its sandbox, and the
   sidecar feature at every notch. The bridge is shared by no one. The Codex home is shared by the
   whole machine, and NC-D18 already counts it at that scope, built and correct. B moves processes
   without moving the ownership. C retires one count by splitting Codex's history per workspace,
   and still leaves the home-directory launch its own. **The trap:** if "one ownership model"
   means one owner per workspace for every host service, C is the answer, and Codex at `yolo host`
   then keeps its history per workspace, as it does in a jail.

   **Answer:**
   > _(empty — fill in when decided)_

---

## 11. Decision Ledger

The first row is the maintainer's direction on [OQ-JL1](#OQ-JL1), whose answer quotes him in
full. The rest are implementation decisions, mine to make under the ruled principles; each names
what it rests on. A row that depends on a question still open says so.

| ID | Decision | Date | Settled in | Built |
|---|---|---|---|---|
| [OQ-JL1](#OQ-JL1) | **Directed by the maintainer: a small background process owns a shared jail, and the first terminal never waits for the others.** *"I don't want a solution where the first terminal waits. The idea is to get my terminal back and reuse it. … I think it is going to have to be some sort of small background process. But then it needs to know how to end itself as well."* That rejects D, the leaning, which was the first launcher holding the jail after its own agent quits; C, ownership migrating to a surviving terminal, has no background process and goes with it. Which background process is [JL-D14](#JL-D14); how it ends itself is [JL-D17](#JL-D17); re-entry and "hand ownership over" are [JL-D22](#JL-D22) | 2026-09-29 | [§9](#9-the-keeper-design-2026-09-29) | — |
| [OQ-JL5](#OQ-JL5) | **Maintainer ruling:** A, if supportable: a keeper at every notch that starts a long-lived host service or sidecar, `yolo host` and macos-user included, with no backend carved out and any feasibility problem named rather than turned into an exception. Presented with the letters swapped; ruled by its words. Designed in [§9.9](#99-the-keeper-at-yolo-host-and-macos-user) (JL-D36 to JL-D43); its question is [OQ-JL9](#OQ-JL9) | 2026-09-29 | [§10](#10-open-questions) | designed, not built |
| [OQ-JL6](#OQ-JL6) | **Maintainer ruling:** A; no linger. The jail tears down when its last session exits, so a lone tab's agent switch is a fresh launch ([JL-D11](#JL-D11), [JL-D12](#JL-D12) stand) | 2026-09-29 | [§10](#10-open-questions) | as ledgered |
| [OQ-JL7](#OQ-JL7) | **Maintainer ruling:** A; a killed keeper leaves its sessions running without host services, each told when it ends; a new arrival is refused naming the live sessions and `yolo stop`; the container is reaped once they leave | 2026-09-29 | [§10](#10-open-questions) | pending |
| [OQ-JL8](#OQ-JL8) | **Maintainer ruling:** A; a session's agent ends with its pane (its launcher hangs up its own in-jail processes before exiting); the other sessions and the jail carry on | 2026-09-29 | [§10](#10-open-questions) | ✅ for an attach ([JL-D51](#JL-D51), [JL-D52](#JL-D52)); the first session at step 3 |
| <a id="JL-D1"></a>JL-D1 | *Implementation decision.* **pid 1 is a hold process, and every session enters by exec.** Forced by coupling 2 (MEASURED). The subreaper-plus-detach variant was weighed and rejected ([§3](#3-the-options)). The remaining design space is who owns the host half | 2026-09-29 | [§4.1](#41-the-container-a-hold-process-as-pid-1-and-every-session-an-exec) | ✅ `entrypoint.holdJail`, `run.startJailMain`, `run.firstSessionExecCmd`; `TestTheMainProcessIsAHoldAndTheFirstSessionAnExec` |
| <a id="JL-D2"></a>JL-D2 | *Implementation decision.* **The count is a host-only kernel lock**, taken under the launch lock. `ExecIDs` is for display only. Follows [JL-P2](#JL-P2): a lock is SIGKILL-safe and needs no runtime call, which also sidesteps Apple Container's unmeasured `ExecIDs`. The layout is one shared file per container name, because [JL-D28](#JL-D28) needs one file the keeper can hold exclusively from its drain to its exit; a per-session registry has none ([§4.2](#42-the-count-a-host-side-session-lock)) | 2026-09-29 | [§4.2](#42-the-count-a-host-side-session-lock) | ✅ [`sessionlock.go`](../../internal/cli/run/sessionlock.go) (the lock and its takers; the keeper's drain on it is step 3); `TestAnOrphanSweepSparesAJailWithASessionInIt` |
| <a id="JL-D3"></a>JL-D3 | *Implementation decision.* **An unopenable lock means "sessions remain".** The keeper then waits only for the container's own end. Follows [JL-P3](#JL-P3) | 2026-09-29 | [§4.4](#44-failure-paths) | — |
| <a id="JL-D4"></a>JL-D4 | *Implementation decision.* **A session's signal arm ends only its own session.** It never calls `stopJail`. It also sends SIGHUP to its in-jail process tree, as [OQ-JL8](#OQ-JL8) ruled (A, 2026-09-29). On local Linux podman, an optional death pipe passed with `podman exec --preserve-fds` also covers a SIGKILLed launcher; on the remote client (every Mac) and elsewhere, a SIGKILLed session's agent runs until the jail stops. `yolo stop` stays the way to end every session. The first session is no exception | 2026-09-29 | [§4.4](#44-failure-paths) | ✅ for an attach's session, without the death pipe ([JL-D52](#JL-D52)); the first session's arm still stops the jail until step 3 ([JL-D46](#JL-D46)) |
| <a id="JL-D5"></a>JL-D5 | *Implementation decision, under the [OQ-JL1](#OQ-JL1) ruling.* **The keeper is started at the fresh launch, from the inode the launch started from**, detached, in its own cgroup scope where systemd is present, and never after the fact. It is `yolo internal daemon jail-keeper`. On Linux the launch spawns it directly by exec of `/proc/self/exe`, which names the running inode even after `just install` replaced the path, so nothing is opened early; the keeper then moves itself into a transient scope with `StartTransientUnit` over D-Bus, passing its own pid, before it starts anything. Not `systemd-run --user --scope`, which execs its command in its own process, so a `/proc/self/exe` handed to it names `systemd-run`, and the keeper would stop being the launch's child. The keeper's own later self-execs, the scratch remover included, are built from its own `/proc/self/exe`, never `execx.SelfExecArgv`, which re-resolves `os.Executable()` to the replaced binary. Elsewhere the build-stamp refusal of [JL-D20](#JL-D20) closes the skew window | 2026-09-29 | [§9.1](#91-what-starts-it) | — |
| <a id="JL-D6"></a>JL-D6 | *Implementation decision.* **Disclosures stay in the fresh launch, in the terminal, before any spawn.** Boot progress is relayed until ready, and later output goes to the keeper's log ([JL-D19](#JL-D19)). Follows the disclosure-before-spawn rule (the comment above `startLoopholesDisclosed`'s call in `runContainer`) and [OQ-RO3](../reference/report-tiers.md#why-its-this-way). [JL-D20](#JL-D20) makes the disclosed plan and the executed plan one value | 2026-09-29 | [§9.6](#96-what-it-discloses-at-launch-and-where-its-output-goes), [§4.5](#45-what-the-user-sees) | — |
| <a id="JL-D7"></a>JL-D7 | *Implementation decision.* **The reaper reaps a jail only when its owner is dead and it takes that jail's liveness lock and session lock `LOCK_EX\|LOCK_NB` itself, holding both across `stopJail` and `stopLoopholes`.** Holding the liveness lock makes an arrival wait for the reap as for a draining keeper ([JL-D28](#JL-D28)). Because a free liveness lock is evidence, the reaper now covers Apple Container's keeper-era jails, where `reapOrphanedJails` returns at once today; a pre-feature Apple Container jail has no liveness lock and stays unreaped, as today. It stays a backstop: an unkept jail's last session reaps it first ([JL-D30](#JL-D30)). Reading the two facts in some order and then stopping is a check-then-act race whichever order is used; holding the lock is what makes an arrival's `LOCK_SH\|LOCK_NB` fail and route to the drain path. A jail started before this ships falls back to today's owner-PID rule; it is recognized by a free liveness lock beside a live owner, since the lock files outlive their keepers ([JL-D28](#JL-D28)). Follows the reaper's own polarity: a jail with live sessions is provably not orphaned (`pidAlive`, [`lifecycle.go`](../../internal/cli/run/lifecycle.go)). "Owner dead" is the keeper's free liveness lock where one exists ([JL-D18](#JL-D18)) | 2026-09-29 | [§2.3](#23-four-defects-found-on-the-way) item 4 | partial ✅ `reapOrphanedJails` with `tryExclusiveSessionLock`; `TestTheReaperSparesAnOrphanWithASessionInIt`: the session-lock half, held across the stop and the host-services cleanup; the liveness-lock half and Apple Container's reaping wait for the keeper |
| <a id="JL-D8"></a>JL-D8 | *Withdrawn.* It declared "the keeper and the count exist only where sessions share a jail" to be parity-neutral. Whether host-service ownership may take two shapes is a ruling, not mine, so it is now [OQ-JL5](#OQ-JL5) | 2026-09-29 | [OQ-JL5](#OQ-JL5) | — |
| <a id="JL-D9"></a>JL-D9 | *Implementation decision.* **After its stop, the keeper confirms the container is gone and removes a stopped leftover,** and never a running one. The trigger is the `--rm` removal failure MEASURED in nested podman when a headless exec was live | 2026-09-29 | [§4.4](#44-failure-paths) | — |
| <a id="JL-D10"></a>JL-D10 | *Implementation decision.* **The housekeeping slot stays in the foreground launch.** [OQ-BF5](disk-levers-and-backfill.md#OQ-BF5) placed it in a process that dies with the launch, and the keeper is not that process | 2026-09-29 | [§9.2](#92-what-it-owns) | — |
| <a id="JL-D11"></a>JL-D11 | *Implementation decision* (drafted as the second open question). **The session that ends a jail waits for the keeper's teardown and streams it** (from the keeper's log, [JL-D19](#JL-D19), until the keeper's liveness lock frees, within [JL-D34](#JL-D34)'s bound). It drops its own shared lock before it waits, as every session does. A session that was not last returns at once, and one that cannot tell says the jail may be draining and returns rather than hang. Forced twice: returning before the teardown reopens the race E3 closes (*"before anyone can ask `yolo config diff` and get last session's answer"*, `teardownAfterExit` in [`run.go`](../../internal/cli/run/run.go)), and it would take the teardown's progress off the terminal, against [OQ-RO3](../reference/report-tiers.md#why-its-this-way). Waiting only for "container gone and E3 done" is a later optimization, once the chain is measured. [OQ-JL6](#OQ-JL6) B would reopen this | 2026-09-29 | [§4.5](#45-what-the-user-sees) | — |
| <a id="JL-D12"></a>JL-D12 | *Implementation decision* (drafted as the third open question). **An arrival during a drain releases the launch lock, waits for the drain, then launches fresh. There is no grace window.** A window of N seconds would make every last quit wait N seconds longer under JL-D11, or else return while the jail still runs, and reopen E3's race. It would also be the one timer-based rule in the lifecycle. The arrival says why it waits. [OQ-JL6](#OQ-JL6) asks whether this holds for switching agents in a lone tab | 2026-09-29 | [§4.2](#42-the-count-a-host-side-session-lock) | — |
| <a id="JL-D13"></a>JL-D13 | *Implementation decision* (drafted as the fourth open question), **as [OQ-JL7](#OQ-JL7) ruled (A, 2026-09-29).** **An arrival at an unkept jail is refused**, and so is one at an unkept key at macos-user or `yolo host` ([JL-D44](#JL-D44)). It names the jail and its live session count, and offers `yolo stop` followed by a launch; `yolo stop` then tears the jail down itself, since no keeper is left to ([JL-D30](#JL-D30)). The alternatives were each already ruled out: reaping it kills sessions the reaper's polarity says are not orphaned (JL-D7), and attaching with a warning contradicts *"A jail whose launcher is gone is relaunched, not attached-and-repaired"* (`attachExisting`) | 2026-09-29 | [§9.5](#95-how-it-ends-itself), [§4.4](#44-failure-paths) | — (step 2 attaches with a warning in the interim, [JL-D48](#JL-D48)) |
| <a id="JL-D14"></a>JL-D14 | *Implementation decision, under the [OQ-JL1](#OQ-JL1) ruling.* **The background process is a keeper started with the jail (O3), not a successor spawned at quit (O2).** O2 is a handoff, which [JL-P4](#JL-P4) forbids; its path runs only when several sessions share a jail, so it is the rarely run path that rots; the fronts it re-creates are never checked by the boot-time reachability witness, while the keeper's exist before the container boots; and under a pane close it would have to finish inside herdr's 0.5 s with no launcher left to wait. What O3 costs a single session is one spawn and a pipe | 2026-09-29 | [§3](#3-the-options), [§9](#9-the-keeper-design-2026-09-29) | — |
| <a id="JL-D15"></a>JL-D15 | *Implementation decision.* **No runtime client ever sits in a pane, and the hold does not die of a stray signal.** The keeper keeps an attached, non-tty client with `--sig-proxy=false`, and reads pid 1's output from it. It builds that argv without `-t`, which the fresh launch's `IsTTYStdout` would add. A detached start is not used: podman runs are `--log-driver none` (`assembleRunCmd`), so with `-d` the boot lines, a refusal's text and the hold notice would go nowhere, and would have to be relayed from the jail-writable `.yolo/boot.log` ([OQ-RO3](../reference/report-tiers.md#why-its-this-way)). The hold ignores SIGHUP and SIGINT and ends on SIGTERM, which `podman stop` sends; it installs those handlers with `signal.Notify`, never `signal.Ignore`, whose `SIG_IGN` its later children would inherit. The refused-boot hold keeps ending on SIGINT or SIGTERM, which its notice (`holdNotice`) tells the user. Rests on the pane-close measurement: an attached client in the pane stopped the jail, and both a detached start and `--sig-proxy=false` left pid 1 untouched ([§3.1](#31-what-a-pane-close-does-measured)) | 2026-09-29 | [§4.1](#41-the-container-a-hold-process-as-pid-1-and-every-session-an-exec), [§9.7](#97-signal-handling-sig-proxy-and-a-pane-close) | partial ✅ `entrypoint.holdUntil`, `run.startJailMain`; `TestTheHoldDropsHangupsAndInterruptsAndEndsOnSigterm`, `TestTheMainProcessClientRunsDetachedFromTheTerminalsSignals`: the hold's signal rules, the refused-boot hold unchanged, and the attached non-tty client with `--sig-proxy=false`, in a process group of its own; it is the fresh launch's child until the keeper exists |
| <a id="JL-D16"></a>JL-D16 | *Implementation decision.* **The first session is an exec, like every attach, and it is counted before the keeper exists.** The fresh launch takes its shared session lock before it spawns the keeper and holds it through its exec, so the keeper cannot see zero sessions before the first one has begun. When its agent exits, it drops the lock and exits with the agent's code, printing one line if others remain | 2026-09-29 | [§9.3](#93-how-the-first-terminal-gets-its-prompt-back) | partial ✅ `run.firstSessionExecCmd`, `run.holdSessionLock`; `TestTheFreshLaunchRunsTheJailAsAHoldAndItsFirstSessionByExec`: the first session is an exec, counted from before its container starts, and exits with its command's code ([JL-D50](#JL-D50) for a jail stopped under it); returning at once while others remain waits for the keeper ([JL-D46](#JL-D46)) |
| <a id="JL-D17"></a>JL-D17 | *Implementation decision*, answering the ruling's *"it needs to know how to end itself"*. **The keeper ends on three observations only:** before the jail is ready, its lifeline's EOF or a start that failed; its exclusive take of the session lock; or the runtime reporting the container ended, confirmed by the tri-state existence probe. It never ends on a timer, and "could not ask" never ends it ([JL-P3](#JL-P3)). Once the probe answers that the container is gone, its teardown never waits on its own runtime client's exit, so Window A stays off every terminal | 2026-09-29 | [§9.5](#95-how-it-ends-itself) | — |
| <a id="JL-D18"></a>JL-D18 | *Implementation decision.* **A keeper's liveness lock is the evidence that it is dead, and a dead keeper is never restarted, respawned or replaced.** The second half is what [OQ-JL7](#OQ-JL7)'s ruling (A) keeps; its rejected C would have reversed it. It holds an exclusive lock for its whole life on one file per container name, which nothing renames over or unlinks ([JL-D28](#JL-D28)); the kernel frees it however the keeper dies. No pending-name rename is needed: until the keeper locks it, the fresh launch holds its shared session lock and the launch lock it handed over, and nothing acts on a free liveness lock without the session lock held exclusively or the launch lock held. The owner-PID file still names the keeper, so an older yolo's PID-only reaper sees a live owner. The first half stands on its own; the second rests on [JL-P4](#JL-P4) and *"relaunched, not attached-and-repaired"* | 2026-09-29 | [§9.5](#95-how-it-ends-itself) | — |
| <a id="JL-D19"></a>JL-D19 | *Implementation decision.* **The keeper's output is never only in a file.** Until ready, it goes over the progress pipe and the fresh launch prints it, so the terminal and `launch.log` have it. Both go through one switchable writer ([JL-D29](#JL-D29)). After ready, it goes to the keeper's own log in host state no jail mounts (never under `cache/`, never in the jail-writable workspace), mirrored into `launch.log` through the workspace-state opener that refuses a planted link (`paths.OpenWorkspaceStateFile`, as the housekeeping notes use). Each session's quit prints what the keeper recorded while that session was in, and an arrival prints any service the keeper recorded as down. The last session streams its teardown from the host-only log | 2026-09-29 | [§9.6](#96-what-it-discloses-at-launch-and-where-its-output-goes) | — |
| <a id="JL-D20"></a>JL-D20 | *Implementation decision.* **The launch hands the keeper one plan, and the keeper runs exactly that plan.** The plan is everything the launch computed for the services and the container, the value its disclosure was printed from. It travels in `launchservice.Input`'s shape: a `0600` file in a `0700` directory, read once and removed, never on an argv. It carries the launch's build stamp, and a keeper of another build refuses it, as does a keeper asked to start a daemon the plan does not name. It names packs by identity and carries the launch's staged pack-tree path, never a path into the launch's own per-process trees ([JL-D35](#JL-D35)) | 2026-09-29 | [§9.1](#91-what-starts-it) | — |
| <a id="JL-D21"></a>JL-D21 | *Implementation decision.* **The keeper is disclosed at every fresh launch,** by one line printed before the spawn, saying what it will hold, that it ends when the last session leaves, and where it logs, followed by its pid once it has started ([§9.6](#96-what-it-discloses-at-launch-and-where-its-output-goes) has suggested wording). Follows the no-quiet-mode rule: a process that outlives the terminal is exactly what a reader must be told about | 2026-09-29 | [§9.6](#96-what-it-discloses-at-launch-and-where-its-output-goes) | — |
| <a id="JL-D22"></a>JL-D22 | *Implementation decision.* **Re-entering, and changing agents, is an ordinary attach, with no new verb.** Once the keeper owns the jail, no terminal is special. The attach-skew disposition for a pack or profile daemon the jail lacks is unchanged | 2026-09-29 | [§9.4](#94-how-re-entering-works-and-changing-agents) | — |
| <a id="JL-D23"></a>JL-D23 | *Implementation decision.* **The keeper cleans up only what its own jail made:** its skeleton, pack tree, tracking and owner-PID files, host-services dir and scratch volumes, through today's guarded functions. It never sweeps another jail's leftovers; that stays with the reapers and the housekeeping slot. Keeps A6's "not a housekeeper" true of it | 2026-09-29 | [§9.2](#92-what-it-owns), [§5.1](#51-how-a-keeper-squares-with-the-rulings) | — |
| <a id="JL-D24"></a>JL-D24 | *Implementation decision.* **The keeper ignores SIGHUP, and a SIGTERM or SIGINT makes it end the jail in order:** it records the reason, stops the container, confirms it gone, runs the chain and exits, as for observation 3 of [§9.5](#95-how-it-ends-itself). A process in a session of its own has no controlling terminal, so a hangup reaches it only from `kill`; a SIGTERM is what a shutdown, a logout or `systemctl --user stop` sends to its scope, and an orderly end there beats an unkept jail the shutdown ends anyway. SIGKILL cannot be handled and leaves an unkept jail ([OQ-JL7](#OQ-JL7)) | 2026-09-29 | [§9.7](#97-signal-handling-sig-proxy-and-a-pane-close) | — |
| <a id="JL-D25"></a>JL-D25 | *Implementation decision.* **`yolo stop` returns only once the keeper's teardown has finished, and streams it**, by waiting for the keeper's liveness lock after its `podman stop`, as a last session does under [JL-D11](#JL-D11). Forced by the same two reasons: returning earlier reopens E3's race for a `yolo config diff` typed next, and would take the teardown's lines off the terminal. When it finds the liveness lock free, no keeper is left to tear down, so it runs the chain itself ([JL-D30](#JL-D30)). Its wait is bounded ([JL-D34](#JL-D34)). A jail started before this ships, whose free liveness lock sits beside a live owner, keeps today's `yolo stop` | 2026-09-29 | [§9.5](#95-how-it-ends-itself) | — |
| <a id="JL-D26"></a>JL-D26 | *Implementation decision.* **An attach-skew restart spawns its own keeper only once the old keeper's liveness lock is free.** It holds the launch lock throughout, as `restartJailForAttach` does today, so the old keeper's guarded teardown leaves the host-services dir to it. Waiting while holding the launch lock cannot deadlock, because a keeper never blocks on that lock ([§4.2](#42-the-count-a-host-side-session-lock)). It keeps one keeper per container name at every instant, which the liveness lock's name assumes. [JL-D28](#JL-D28) makes the same wait universal: every fresh launch makes it, and only this one makes it holding the launch lock | 2026-09-29 | [§9.1](#91-what-starts-it) | — |
| <a id="JL-D27"></a>JL-D27 | *Implementation decision.* **Every podman `run` and `exec` yolo issues, from the local or the remote client, passes `--detach-keys=""`.** Forced by [§2.3](#23-four-defects-found-on-the-way) item 2: a host's `containers.conf` may leave podman's `ctrl-p,ctrl-q` live, and a detached session client would leave its agent running headless and uncounted. The only evidence is podman's help text, so the rule is podman's. Apple Container's `container run` and `container exec` get a half only once `container run --help` or a probe shows a flag or a detach sequence; passing podman's flag there unmeasured could fail every launch and attach on an unknown flag | 2026-09-29 | [§9.7](#97-signal-handling-sig-proxy-and-a-pane-close) | ✅ `runtime.DetachKeysArgs` ([JL-D54](#JL-D54)); `TestEveryRunAndExecIntoAJailTurnsOffTheDetachSequence`, `TestTheDetachSequenceNeverLeavesAnAttachedSessionHeadless` |
| <a id="JL-D28"></a>JL-D28 | *Implementation decision.* **One keeper per container name at every instant, on every path.** (1) Every fresh launch, meaning an arrival that found no running container, probes the name's liveness lock after taking the launch lock and before its `LOCK_SH`. If a keeper holds it, the launch releases the launch lock, says the previous jail is still shutting down, waits, and takes the lock again ([JL-D12](#JL-D12)'s path); only the attach-skew restart waits holding it ([JL-D26](#JL-D26)). (2) The keeper holds `LOCK_EX` on the session lock from its drain until it exits; on observation 3 and on a SIGTERM it takes it after its chain. (3) The session-lock and liveness-lock files are one per name and are never unlinked or renamed over, so a pre-feature jail is recognized by its live owner, never by a missing file ([JL-D7](#JL-D7)). (4) A path-keyed record is removed only while it still names this keeper: the owner-PID file only while it holds the keeper's pid, as `forgetGoneContainer` removes the live-tree record only while it names this launch's tree. `teardownAfterExit` and `stopJail` call `clearOwnerPID` unconditionally today, so each becomes that comparison. Forced by observation 3 of [§9.5](#95-how-it-ends-itself), which covers `yolo stop`, an OOM, a crash and [JL-D24](#JL-D24)'s SIGTERM. There, the container is gone while the chain runs, so an arrival without (1) finds no container, takes an unopposed `LOCK_SH` and spawns a second keeper. The old chain's path-keyed cleanup then runs over the new jail, including the removal of its owner-PID file. The dying jail's sessions are counted toward the new one. A fresh lock inode lets the next keeper drain under live sessions. That is the race [JL-D11](#JL-D11) and [JL-D25](#JL-D25) exist to close | 2026-09-29 | [§4.2](#42-the-count-a-host-side-session-lock), [§9.1](#91-what-starts-it) | — |
| <a id="JL-D29"></a>JL-D29 | *Implementation decision.* **The keeper's descriptors and output writers are set, never inherited by accident.** Its stdio is `/dev/null`, as `startDetached` gives its child. The progress pipe, the lifeline and the handed launch lock are `ExtraFiles`, which a Go child receives without close-on-exec (`syscall/exec_linux.go`), so the keeper marks each close-on-exec first thing; otherwise socat, the fronted daemons, the runtime client and the scratch remover would hold them, a keeper dead before ready would leave the pipe open, and a leaked lock would keep a jail or a keeper looking alive. No session-lock or liveness-lock descriptor ever reaches a child. The keeper calls `signal.Notify` for SIGPIPE and SIGHUP and never `signal.Ignore`, whose `SIG_IGN` its children would inherit. Its `o.Stdout` and `o.Stderr` sit behind one switchable writer, the pipe until ready and the host-only log always, because reused code captures its writer at start (`startExternalService`'s `out`, which its stop closure keeps). The fresh launch detects a keeper that died by its child's exit (`cmd.Wait`), not by the pipe's EOF. Forced by Go's defaults: a program with no `Notify` for SIGPIPE exits on a broken-pipe write to descriptor 1 or 2 ([os/signal](https://pkg.go.dev/os/signal#hdr-SIGPIPE)), and the reader exits at [§9.7](#97-signal-handling-sig-proxy-and-a-pane-close)'s pane close during boot and whenever the first terminal quits | 2026-09-29 | [§9.1](#91-what-starts-it), [§9.6](#96-what-it-discloses-at-launch-and-where-its-output-goes) | — |
| <a id="JL-D30"></a>JL-D30 | *Implementation decision*, **as [OQ-JL7](#OQ-JL7) ruled (A, 2026-09-29).** **An unkept jail is reaped by its last session as it quits, and by `yolo stop`, on every backend.** After dropping its shared lock, a quitting session takes the liveness lock and then the session lock, each `LOCK_EX\|LOCK_NB`. Winning both, while the owner-PID file names a dead process, means no keeper and no other session; a live owner there is an older yolo's launcher, and the jail is left to it. Holding both, it runs the keeper's chain in-process: `stopJail`, `stopLoopholes(nil, …)`, `forgetGoneContainer`, E3 and the scratch removal. The names only the keeper knew come from a record it wrote at its start in host state no jail mounts. `yolo stop` runs the same chain when it finds the liveness lock free and the owner dead. The next-launch reaper stays as the backstop ([JL-D7](#JL-D7)). Forced because the reaper alone never ends an unkept jail on Apple Container (`reapOrphanedJails` returns at once there), ends one on podman only when some `yolo` runs, and does only part of the teardown. And with a hold as pid 1, an orphan no longer ends when its first agent exits. At macos-user and `yolo host` the same reap removes the key's host-services dir, box and roster, each only while the roster names the dead keeper ([JL-D44](#JL-D44)) | 2026-09-29 | [§9.5](#95-how-it-ends-itself) | — |
| <a id="JL-D31"></a>JL-D31 | *Implementation decision.* **The fresh launch hands its workspace launch lock to the keeper.** It passes the lock's descriptor in `ExtraFiles`, as the scratch remover receives its lock, and closes its own copy without `LOCK_UN`, because `workspaceLock.Close` unlocks every duplicate ([`flock.go`](../../internal/cli/run/flock.go)). The keeper calls `LOCK_UN` at today's `onStarted` point, once the container is seen running, or before any unwind. That keeps today's order inside one process: release the lock, then clean up. The attach-skew restart hands its lock over the same way. Forced by both alternatives. A launch that released the lock at the spawn would let a racing arrival find no container and spawn a second keeper into the same cname-keyed endpoint files and socket paths. A launch that held it until the container ran, as `onStarted` does today, would make a failed start's unwind back off: `forgetGoneContainer` and `stopLoopholes` take it non-blocking (*"Each calls it AFTER the launch's own workspace lock is released"*, [`trackingcleanup.go`](../../internal/cli/run/trackingcleanup.go)), which is why `runContainer`'s `runErr` branch releases it first | 2026-09-29 | [§4.2](#42-the-count-a-host-side-session-lock), [§9.1](#91-what-starts-it) | — |
| <a id="JL-D32"></a>JL-D32 | *Implementation decision.* **Every child the keeper starts ends without it.** The port forwards become in-process Go forwarding, whose goroutines die with the keeper as the fronts do, or each socat gets a lifeline; each fronted daemon gets a lifeline ([YW-D1](../research/central-yolo-watcher.md#YW-D1)'s fix). Which mechanism serves the forwards is the implementer's, and one of them is required. The keeper also records its systemd scope, so the reap of an unkept jail ([JL-D30](#JL-D30)) can stop the whole scope. `startHostPortForwarding` removes the whole `/tmp/yolo-fwd-<cname>` dir before it spawns anything. Forced because socat, the keeper's plain child in the keeper's own session, and the Setsid fronted daemons are reached by no pane close. After a keeper SIGKILL they would serve the unkept jail unowned, breaking [YW-P3](../research/central-yolo-watcher.md#YW-P3), and run forever, since no reaper kills them. And that dir is bind-mounted read-write into the workspace's next jail while the function removes only the sockets it now forwards, so an orphan's forward the config has since dropped would stay reachable from it | 2026-09-29 | [§9.5](#95-how-it-ends-itself) | — |
| <a id="JL-D33"></a>JL-D33 | *Implementation decision.* **Provisioning records its outcome, and every other session waits for that outcome, never for a done marker.** It runs under an in-jail flock and records *done*, including a failure the first terminal chose to continue past, or *refused*, meaning `RefusedStatus` or a failure the first terminal declined. A free flock with no outcome reads as *abandoned*. A waiter that reads *refused* is refused, and the count then ends the jail. On *abandoned*, a waiter with a tty reruns provisioning on its own tty, and one without is refused. A waiter never waits holding the launch lock, and its wait is bounded ([JL-D34](#JL-D34)). Forced because a marker only success writes leaves a waiter, which holds its shared lock, hanging after a refusal, an `n`, a pane closed mid-run or a SIGKILLed first launcher. And a waiter that entered anyway would be the *"continue anyway"* [OQ-AR3](../reference/agent-program-runtimes.md#oq-ar3) ruled out | 2026-09-29 | [§4.1](#41-the-container-a-hold-process-as-pid-1-and-every-session-an-exec) | ✅ `entrypoint.sessionGate`; `TestTheFirstSessionProvisionsAndEveryOtherWaitsForTheOutcome`, `TestALateFirstSessionActsOnATakenOverRunInsteadOfRunningItAgain`. A waiter waits before its own boot pass, as well as for pid 1's boot; a first session that never claims the run within two minutes is read as abandoned. A Ctrl-C during the run is the stage's to decide, and the first session records what it decided (`TestACtrlCDuringProvisioningIsTheStagesToDecide`) |
| <a id="JL-D34"></a>JL-D34 | *Implementation decision.* **Nothing waits on a keeper without a bound, and the keeper waits on no runtime call without one.** Each runtime call the keeper makes has a timeout, like `trackingProbeTimeout`, never `stopLoopholes`' timeout 0. The waiters are the last session, `yolo stop`, an arrival during a drain, a provisioning waiter and the attach-skew restart. After its bound, each prints the keeper's pid, its log and what it is doing. A quit or `yolo stop` then returns; an arrival or a restart is refused with that remedy. Ctrl-C during a streamed teardown stops the stream and leaves the keeper running. Forced because a hung teardown held one closable terminal until now, while a wedged keeper would hold every later `yolo` in the workspace, and `yolo stop` too, which today returns once `podman stop` does. [JL-P3](#JL-P3) has the keeper retry while the runtime is silent, for example on a Mac whose podman machine is stopped | 2026-09-29 | [§9.5](#95-how-it-ends-itself), [§4.4](#44-failure-paths) | partial ✅ `bootWaitLimit`, `provisionWaitLimit`, `claimWaitLimit`, `sessionLockWait`: the provisioning waiter's and the boot waiter's bounds, and a session's wait for its count while a reaper holds the lock; the keeper's waiters are step 3 |
| <a id="JL-D35"></a>JL-D35 | *Implementation decision.* **The keeper takes its own embedded pack tree lease and resolves its packs itself.** The plan names packs by identity and carries the launch's staged pack-tree path. It never carries a `Pack.Root` inside the fresh launch's per-process fallback tree or its process pack tree (`packload.ProcessPackDir`). `ReleaseEmbedded` deletes those when the first terminal quits, while the keeper runs daemons from them and reads them at teardown. [`processtree.go`](../../internal/packload/processtree.go) names that failure: a copy that *"would die with a process that a host-scope daemon spawned from it outlives"*. A keeper whose resolution disagrees with the plan's identities refuses the plan, as for a daemon the plan does not name ([JL-D20](#JL-D20)) | 2026-09-29 | [§9.2](#92-what-it-owns) | — |
| <a id="JL-D36"></a>JL-D36 | *Implementation decision, under [OQ-JL5](#OQ-JL5)'s ruling.* **A `yolo host` launch that has a keeper stays resident as its session**: it holds the key's shared session lock close-on-exec, runs the agent as its child in `launchservice.RunAgent`'s shape, and exits with the agent's code. A launch with no keeper execs as today. Rejected: exec with the lock's descriptor inherited, which survives the exec (flock(2), POSIX exec) but was MEASURED to miscount both ways, the lock staying held by a `sh`'s or a Go program's orphaned child after the command exited, and dropping while OpenSSH 10.5p1's `ssh` ran, the polarity [JL-P3](#JL-P3) forbids; it was exact only for Node, and claude's and codex's native binaries are UNMEASURED. Rejected too: a PID watch (pidfd, or kqueue's `NOTE_EXIT`), exact but a second count, in the keeper's memory, that the reaper and the arrival probe cannot read ([JL-D7](#JL-D7), [JL-D28](#JL-D28)) | 2026-09-29 | [§9.9.2](#992-what-a-session-is-there-and-why-yolo-host-stays-resident) | — |
| <a id="JL-D37"></a>JL-D37 | *Implementation decision.* **One keeper per key, and one key per workspace per notch**: the container name at a container backend, and the workspace's container name with the notch appended at macos-user and `yolo host`, each with its own session lock, liveness lock, arrival lock, roster and log. A launch that takes the workspace launch lock takes it before the arrival lock and releases both before it waits on a drain, and the keeper's teardown there takes neither, as today's macos-user teardown takes none. A key never spans two notches, since what its sessions share (a ping box above all) would carry text from a more confined notch to a less confined agent, against [EW-D8](agent-event-watchers.md#EW-D8). A `yolo host` launch from a directory `paths.WorkspaceScopeBreach` refuses as a workspace gets no key | 2026-09-29 | [§9.9.3](#993-one-keeper-per-workspace-per-notch) | — |
| <a id="JL-D38"></a>JL-D38 | *Implementation decision.* **At macos-user the keeper holds everything the launch starts outside the sandbox**: the host-services dir (one per key), the fronts and fronted daemons, the credential-view step, the doorways, the launch-owned services, host-side sidecars and the ping box. **What runs as the sandbox account or needs root stays each session's**, because a keeper has no terminal to authenticate `sudo` against and `sudo` closes every descriptor from 3, so no lifeline crosses it ([sudoers(5)](https://www.sudo.ws/docs/man/sudoers.man/)). At `yolo host` it holds the sidecars and the box; the launch's own services are [OQ-JL9](#OQ-JL9). The caller tokens of what it holds are minted per keeper into the plan and used by every session of the key, which also fixes the macos-user per-agent env files and the shared `.codex/auth.json`, which today name one session's listeners and token. It writes a **roster** (`0600`, host state no jail mounts) naming what it runs, where, and behind which tokens, and removes it at its end | 2026-09-29 | [§9.9.4](#994-what-the-keeper-owns-there) | — |
| <a id="JL-D39"></a>JL-D39 | *Implementation decision.* **A joining launch counts itself, reads the roster and composes against it, and starts nothing the keeper holds.** Under the key's arrival lock it probes the liveness lock, takes `LOCK_SH\|LOCK_NB` and reads the roster, and on a drain it waits as [JL-D28](#JL-D28) says. At macos-user it still runs its own `sudo` steps and sandbox. A joiner that needs a host service the keeper does not run takes the container attach's refusal, naming what the keeper lacks, the live sessions and `yolo stop` ([JL-D44](#JL-D44)), or its acknowledgment hatch, `YOLO_ALLOW_ATTACH_SKEW` ([JL-D22](#JL-D22)). It is never offered the restart: at a container that is a runtime stop (`restartJailForAttach`), and at these notches it would end other terminals' sessions and, at macos-user, sandbox-account processes, which only `yolo stop` does, from a terminal the user chose. Chosen over the keeper adding a service for a joiner, which would make the one disclosed plan ([JL-D20](#JL-D20)) a plan plus additions; at macos-user every profiled agent's pairing counts ([HS-D14](host-notch-services.md#HS-D14)), so an unchanged config never differs. A sidecar enabled since the keeper started is not a service here: whether a joiner may hand one to the keeper is [OQ-EW13](agent-event-watchers.md#OQ-EW13) | 2026-09-29 | [§9.9.5](#995-how-a-second-launch-joins) | — |
| <a id="JL-D40"></a>JL-D40 | *Implementation decision.* **At every notch the last session waits for the keeper's teardown and streams it, within [JL-D34](#JL-D34)'s bound, and the keeper ends on its lifeline's EOF or a failed start before ready, or on its exclusive take of the session lock after.** Outside a container only [JL-D11](#JL-D11)'s second reason applies, since `captureConfigOnTerminate` runs only in the container arm: a teardown printed after the prompt is off the terminal ([OQ-RO3](../reference/report-tiers.md#why-its-this-way)). The wait is bounded by the existing stop graces, 2 s for a launch-owned child and 5 s for a fronted daemon. `yolo stop` ends a key at the other two notches too ([JL-D44](#JL-D44)), and `kill <pid>` still ends a keeper in order ([JL-D24](#JL-D24)). The macos-user session launcher takes `launchservice.RunAgent`'s signal shape: it absorbs SIGINT and SIGQUIT, forwards SIGTERM and SIGHUP to its `sudo` child, which relays them, and exits after the command. Forced because with no arm (`proxy_other.go`) a terminal's SIGINT kills the launcher under Go's default while a sandboxed program that survives it runs on, so its session lock would drop under a live session, against [JL-P3](#JL-P3) | 2026-09-29 | [§9.9.6](#996-what-the-first-terminal-sees-and-how-the-keeper-ends) | — |
| <a id="JL-D41"></a>JL-D41 | *Implementation decision.* **At macos-user and `yolo host` the fresh launch prints [JL-D21](#JL-D21)'s keeper line before the spawn, naming the notch, and a joining launch prints one line naming the keeper it joined, followed by the service, doorway and sidecar disclosures marked as held by it.** A joining terminal is served by host code it did not start, and a launch has no quiet mode ([OQ-RO3](../reference/report-tiers.md#why-its-this-way)) | 2026-09-29 | [§9.9.7](#997-what-it-discloses) | — |
| <a id="JL-D42"></a>JL-D42 | *Implementation decision, answering "when a launch has nothing long-lived to own".* **No keeper is spawned when a launch's plan holds nothing that a keeper at its notch holds**: at macos-user a front, fronted daemon, doorway or launch-owned service, at either notch a sidecar or the ping box, and at `yolo host` the launch's own services only if [OQ-JL9](#OQ-JL9) is ruled B or C. Such a launch runs as today, `yolo host` exec'ing or resident for its own services, and takes no session lock; a keeper a later launch spawns neither counts nor serves it. Not a backend carve-out: the same rule gives every container launch a keeper, since its plan holds the container. A keeper with nothing to hold would be a spawn, a `ps` line and a disclosure for nothing. Whether a ping box alone counts follows [OQ-EW11](agent-event-watchers.md#OQ-EW11): under its leaning a box never comes without a sidecar | 2026-09-29 | [§9.9.8](#998-a-launch-with-nothing-long-lived) | — |
| <a id="JL-D43"></a>JL-D43 | *Implementation decision,* the reading [OQ-JL5](#OQ-JL5)'s setup said it decides. **[OQ-HS3](host-notch-services.md#OQ-HS3)'s "launch" is the key's life for what a keeper holds, and the launch itself for what a session keeps.** At a container backend the key's life is the jail, [HD-R1](host-daemon-ownership.md#HD-R1)'s unit; at macos-user and `yolo host` it is one workspace's sessions at that notch. What HS3 rejected stays rejected: nothing outlives the key's last session, `yolo host apply` renders no per-launch address, `yolo host env` still refuses a bridged profile, and tokens are minted per keeper. [HS-D15](host-notch-services.md#HS-D15)'s *"launch-owned listener"* reads the same way: where a doorway listens is unchanged, and at macos-user its owner is the keeper. The managed Codex home at `yolo host` is not a keeper's under [OQ-JL9](#OQ-JL9)'s leaning, and its token stays the machine's live launches', as [NC-D18](../plans/notch-convergence.md#NC-D18) has it | 2026-09-29 | [§9.9.9](#999-what-changes-for-a-host-services-lifetime) | — |
| <a id="JL-D44"></a>JL-D44 | *Implementation decision, carrying [OQ-JL7](#OQ-JL7)'s ruling to macos-user and `yolo host`, as [OQ-JL5](#OQ-JL5)'s "no backend carved out" requires.* **Under the key's arrival lock, an arrival that finds the liveness lock free probes the session lock with `LOCK_EX\|LOCK_NB`. If sessions hold it, the key is unkept and the arrival is refused, naming the live sessions and `yolo stop`, and no replacement keeper is spawned. If none does, the arrival is the fresh launch, and removes a dead keeper's records first.** The last session's reap removes the host-services dir, the box and the roster only while the roster names the dead keeper, as [JL-D28](#JL-D28) (4) does for the owner-PID file. `yolo stop` ends a key at these notches: SIGTERM to its keeper and to each live session (a `yolo host` session forwards it to its agent, a macos-user session to its `sudo` child, which relays it), then the streamed teardown, or the reap for an unkept key. It finds the sessions through a per-session record (pid and start time, host state no jail mounts), which names and signals and never counts ([JL-D2](#JL-D2)). `--at host` picks the `yolo host` key, as `yolo apply` spells it. Forced because, read as "the first launch while no keeper holds the liveness lock", a fresh launch after a keeper's SIGKILL would take a shared lock beside the old sessions and spawn a second keeper at the same key-named paths, whose records the old sessions' reap would then remove: the repair [OQ-JL7](#OQ-JL7) rejected, and a refusal with no working remedy, since `yolo stop` had been left container-only | 2026-09-29 | [§9.9.5](#995-how-a-second-launch-joins), [§9.9.6](#996-what-the-first-terminal-sees-and-how-the-keeper-ends) | — |
| <a id="JL-D45"></a>JL-D45 | *Implementation decision.* **A keeper's roster at macos-user and `yolo host` carries a contract version, and a joiner reads only a version it knows.** One of another build that knows the version joins and says so on its keeper line; one that does not is refused, naming the keeper's pid and build, the live sessions and `yolo stop` ([JL-D44](#JL-D44)), and the key's next fresh launch runs its build. The counterpart of a container jail's contract tags ([`attach-skew-and-contract-guardrails.md`](attach-skew-and-contract-guardrails.md)). Forced because a keeper can outlive a `brew upgrade` or `just install` by a working day, and neither [JL-D20](#JL-D20)'s build-stamp refusal, which covers only the spawn, nor [JL-D39](#JL-D39), which covers only a differing service set, stops a joiner of another build from reading the roster's format and token variables as its own | 2026-09-29 | [§9.9.4](#994-what-the-keeper-owns-there) | — |
| <a id="JL-D46"></a>JL-D46 | *Implementation decision, for step 2 of [§7](#7-what-i-would-build-in-order) only; step 3 removes it.* **Until the keeper exists, the hold also ends when the first session's process does.** The first session registers its pid in the jail's `/run/yolo/main/first-session`, and the hold polls it. The fresh launch still waits for the main process's client after its own exec returns, and stops the jail itself when the hold has not followed within a grace. Forced by step 2's *"changes nothing a user can see"*: without it, a first launcher SIGKILLed while its agent ran would leave a container whose main process never ends, where today the jail ended with that agent. The session count cannot stand in yet, since nothing drains on it before the keeper | 2026-09-29 | [§7](#7-what-i-would-build-in-order) | ✅ `entrypoint.followFirstSession`, `run.awaitJailMainEnd`; `TestTheHoldFollowsTheFirstSession`, `TestTheJailEndsWithTheFirstSessionEvenWhenTheHoldDoesNotFollow` |
| <a id="JL-D47"></a>JL-D47 | *Implementation decision, answering [§4.1](#41-the-container-a-hold-process-as-pid-1-and-every-session-an-exec)'s "how pid 1 reports that its boot finished is the implementer's".* **pid 1 reports its boot done with one line on its stderr, and in the jail's `/run/yolo/main/boot`.** The launcher relays pid 1's stderr to the terminal line by line and starts the first session's exec on that line, which it does not print, so every boot line precedes it by construction and the boot reads as it did; a session exec'd in meanwhile waits on the file. `boot.log` stays pid 1's boot, the one that started the jail's daemons and ran the reachability witness: every session's own boot pass goes to `boot.session.log`, which keeps the pass before it as `boot.session.log.prev`, and the first session's pass, the jail's second, to that log alone, since the terminal has just shown pid 1's. A session's pass that rotated `boot.log` pushed pid 1's boot into `boot.log.prev` at every launch, and a refused launch's log out of both after one relaunch. When the keeper exists the same line ends its relay over the progress pipe | 2026-09-29 | [§4.1](#41-the-container-a-hold-process-as-pid-1-and-every-session-an-exec) | ✅ `entrypoint.BootReadyLine`, `run.readyRelay`, `entrypoint.attachPassLog`; `TestTheRelayPrintsTheBootAndSwallowsTheReadyLine`, `TestASessionsPassLeavesTheJailsBootLog` |
| <a id="JL-D48"></a>JL-D48 | *Implementation decision, for step 2 only, and open to the maintainer's overrule; at step 3 [JL-D13](#JL-D13)'s refusal replaces it.* **An arrival at a jail whose first launcher is dead and whose session lock is held attaches, and says that the launcher and its host services are gone and that `yolo stop` then a launch restores them** (`noteGoneOwner`). Step 2's reaper rule ([JL-D7](#JL-D7)) is what makes this state reachable: the arrival's own sweep used to reap such a jail under its other sessions and launch fresh. [OQ-JL7](#OQ-JL7) was ruled A for a jail whose keeper is dead: its sessions run on, and a new arrival is refused, naming them and `yolo stop` ([JL-D13](#JL-D13)). Step 2 has no keeper, and its owner, the first launcher, is not one, so the ruling's words do not reach this state; this row keeps the arrival working, as it worked before step 2, when it got a fresh jail, and says what it walks into. It is the "attaching with a warning" JL-D13 sets aside for an unkept jail, so the departure is named rather than hidden: a ruling that [OQ-JL7](#OQ-JL7) reaches the step-2 owner as well moves JL-D13's refusal ahead of the keeper and retires this row | 2026-09-29 | [§4.4](#44-failure-paths) | ✅ `noteGoneOwner`; `TestAnAttachIntoAJailWhoseLauncherIsGoneSaysSo` |
| <a id="JL-D49"></a>JL-D49 | *Implementation decision, for step 2; the keeper's own arm ([JL-D24](#JL-D24)) and each session's ([JL-D4](#JL-D4)) replace it at step 3.* **A fresh launch has one signal arm for its whole child window.** It is installed before the main process starts and kept until that process's client has exited, so it covers the boot, the first session's exec and the wait after it, which is where Window A now falls. The TTY proxy runs the first session with no arm of its own and hands this one the handle it needs to put the terminal back and end the exec client (`ttyproxy.Observer.Arm`). Forced because the first build handed the arm to the proxy's and took it back afterwards: a signal between the handoff and the proxy's own registration was swallowed, and a SIGHUP, SIGTERM or `kill %1` once the first session had returned killed the launcher with no teardown, leaving the host services, and during the grace the container, behind. The terminal is the shell's again once the first session returns, so a Ctrl-C there is a SIGINT and runs the teardown too | 2026-09-29 | [§4.4](#44-failure-paths) | ✅ `run.launchSignalArm`, `run.runFirstSession`; `TestTheLaunchSignalArmStaysArmedOnceTheFirstSessionReturns`, `TestACallerArmOwnsTheSignalsOfARun`, `TestAHangupWhileTheMainProcessLingersStillEndsTheJail` |
| <a id="JL-D50"></a>JL-D50 | *Implementation decision, for step 2, answering [§4.1](#41-the-container-a-hold-process-as-pid-1-and-every-session-an-exec)'s re-derived OOM hint.* **The hold exits 143 when a SIGTERM ended it, and 0 when it followed its first session out, and the launch returns 143 for a failed first session over a hold a SIGTERM it did not send ended.** A pid namespace whose init exits has every other process SIGKILLed, so after `yolo stop` or an attach-skew restart the first session's exec reports 137, or the runtime's own failure when the stop lands as it starts, where the launch returned 143 before; on a Mac's small Podman machine the 137 also printed the OOM-killer hint for a stop. The hold's status is the only fact that tells a stop from a session killed while its jail ran, since both SIGKILL the session | 2026-09-29 | [§4.1](#41-the-container-a-hold-process-as-pid-1-and-every-session-an-exec) | ✅ `entrypoint.holdExitStatus`, `run.firstSessionStatus`; `TestAJailStoppedFromOutsideReturnsWhatItDidBefore`, `TestStopEndsTheWorkspaceJail` |
| <a id="JL-D51"></a>JL-D51 | *Implementation decision, building [OQ-JL8](#OQ-JL8) (A) for an attach at step 1.* **A session's hangup is what a terminal's hangup sends: SIGHUP, then SIGCONT, to the session's own process, every descendant of it, and every other process still in its session, never to pid 1 or to the hangup itself, and with no SIGKILL after it.** SIGCONT follows so a stopped process gets to act on the SIGHUP, the order the kernel uses when a terminal hangs up. The session is taken whole because every exec was MEASURED to lead a session of its own (pid, process group and session id equal, on nested podman 5.8.7, with a terminal and without), so a descendant its parent's exit reparented is still found there; a recorded process that leads no session reaches its descendants only, since its session is someone else's. No SIGKILL, because a process that ignores a hangup, `nohup make` in the agent's shell, chose to outlive its terminal and does so past a closed terminal anywhere | 2026-09-30 | [§2.3](#23-four-defects-found-on-the-way) | ✅ `entrypoint.hangupTargets`, `entrypoint.hangUpSession`; `TestHangupTargetsAreTheSessionsOwnProcesses`, `TestAHangupEndsARealSessionAndItsChildren` |
| <a id="JL-D52"></a>JL-D52 | *Implementation decision, building [JL-D4](#JL-D4) for an attach at step 1.* **An attach names its session in the jail, and its signal arm asks the jail's entrypoint to hang that session up before it kills its exec client.** The launcher mints 16 random bytes as the session's id and passes it as `YOLO_SESSION_ID` on the exec; the session's entrypoint records its pid and start time under the id in `/run/yolo/sessions`, before anything that can wait, and keeps that pid through its exec of bash; `yolo-entrypoint --yolo-hangup-session <id>`, a two-argument form like the hold's, reads the record, skips a pid whose start time changed, sends [JL-D51](#JL-D51)'s signals and removes it. The arm runs that exec with a 2 s bound, puts the terminal and its jail indicator back, kills the client and exits 128+N. It is the fresh launch's arm kind (`launchSignalArm`, through `runArmedSession`), so it covers an attach with no terminal, where the proxy's plain spawn installed no arm at all, and a Mac's attach, unmeasured there. Only a jail whose frozen contract tags carry the new `session-hangup` tag is named a session: an older entrypoint would read the hangup form as a session's command and run a whole boot pass, so an attach to such a jail runs as before and its arm says it cannot end the session. The death pipe [JL-D4](#JL-D4) allows on local Linux podman is not built, so a SIGKILLed attach launcher still leaves its session running | 2026-09-30 | [§2.3](#23-four-defects-found-on-the-way) | ✅ `attachSessionID`, `attachSignalArm`, `entrypoint.registerSession`; `TestAnAttachRunsItsSessionUnderItsOwnArm`, `TestAnAttachNamesItsSessionOnTheExec`, `TestAHungUpAttachEndsItsOwnSessionAndNoOther` |
| <a id="JL-D53"></a>JL-D53 | *Implementation decision, building [§2.3](#23-four-defects-found-on-the-way) item 3 and [§4.5](#45-what-the-user-sees)'s recorded stop reason before the keeper exists.* **Whatever ends a container jail records why before it does, in a host-only stop record, and an attach whose jail ended under it prints the record.** The record is `<machine storage>/owners/<container name>.stopped`, beside the owner-PID file and mounted by no jail, holding the time, the reason and the writer's pid, replaced whole. A process that stops the jail writes it first and replaces any record, since it is the cause: `stopJail`, which now takes the reason from each caller (the fresh launch's signal arm, an attach-skew restart, the orphan reaper, a hold that did not follow its first session), `yolo stop`, and `yolo check`'s orphan cleanup, which removes a running jail at a terminal's yes. The fresh launch also records its first session's end, which ends the jail with no stop of its own, unless a record was written since that session began, which then says what ended it. When that session's exec returned 137, 125 or 255, the statuses a jail's end gives an exec (as below), the record waits for the main process's end and is written only when the main process followed the session out with 0. A signalled main process means the jail ended under the session, from outside yolo when nothing else recorded why (a `podman stop`, an out-of-memory kill), and a first-session record would then blame the wrong end (`settleFirstSessionEnd`). An attach begins by noting the time; when its exec returns 137 (the kernel's SIGKILL of a pid namespace whose init exited, MEASURED on nested podman within 35 ms of a `podman stop`) or podman's 125 or 255, and the runtime answers that no container of the name is running, it prints the record written after that time, waiting up to a second for one that lands as the jail ends, or says nothing recorded why. A recorded stop explains the 137, so the macOS OOM hint is skipped then. Any other status, a jail still running and a runtime that cannot answer claim nothing. The keeper takes the writer's role at step 3 | 2026-09-30 | [§2.3](#23-four-defects-found-on-the-way) | ✅ `recordJailStop`, `recordFirstSessionEnd`, `settleFirstSessionEnd`, `whyTheJailEnded`; `TestEveryStopYoloMakesRecordsItsCause`, `TestTheOrphanCleanupRecordsWhyBeforeItRemovesAJail`, `TestAJailEndedFromOutsideIsNotRecordedAsItsFirstSessionsEnd`, `TestAnAttachWhoseJailEndedSaysWhy` (unit and integration) |
| <a id="JL-D54"></a>JL-D54 | *Implementation decision, building [JL-D27](#JL-D27).* **Its "every" is every podman `run` and `exec` argv yolo builds for a jail, kept so by a source sweep, and not the Linux builder's detached run.** The flag comes from one helper, `runtime.DetachKeysArgs` (podman only), called by the main process's run flags, the first session's exec, an attach's exec, the hangup's exec and `yolo check`'s in-jail probe; a sweep of the non-test source fails on any `<runtime> run` or `<runtime> exec` string-slice literal in a function that does not call it. The main process's client and the probes hold no terminal, so nothing can type the sequence at them; they carry it so no exception exists for a copied line to inherit. The containerbuilder's `run -d` starts no jail and attaches no client, so it is left alone. MEASURED on podman 5.8.7: the flag is accepted on a detached run and on an exec with no terminal, and both the local and the remote client's help list it for `run` and `exec` | 2026-09-30 | [§9.7](#97-signal-handling-sig-proxy-and-a-pane-close) | ✅ `TestEveryRunAndExecIntoAJailTurnsOffTheDetachSequence`, `TestTheFirstSessionIsAnExecOfTheFirstSessionForm` |

---

## 12. The neighbors

- [`docs/research/central-yolo-watcher.md`](../research/central-yolo-watcher.md), the sibling
  exploration. It covers what a machine-wide watcher would fix beyond this feature, why this
  feature does not need one, and names a per-jail keeper as an allowed owner (YW-P3).
- [`docs/research/herdr-integration.md`](../research/herdr-integration.md): what a pane close does
  ([§3.4](../research/herdr-integration.md#34-closing-a-pane-is-a-kill)), carried here as
  [§3.1](#31-what-a-pane-close-does-measured) by its [HR-D6](../research/herdr-integration.md#HR-D6),
  and what the keeper design does about it, [§9.7](#97-signal-handling-sig-proxy-and-a-pane-close).
  [OQ-JL8](#OQ-JL8) ruled that a pane close ends its agent.
- [`host-daemon-ownership.md`](host-daemon-ownership.md): [HD-R1](host-daemon-ownership.md#HD-R1),
  whose ledger text makes the jail the unit of a daemon's life ([JL-P4](#JL-P4)).
- [`host-notch-services.md`](host-notch-services.md): [OQ-HS3](host-notch-services.md#OQ-HS3),
  per-launch services at the host notch and the "launched outside yolo" ruling, and
  [HS-D15](host-notch-services.md#HS-D15), the doorways, which at the container backends are
  jail daemons bound at pid 1's boot and so need nothing from the keeper beyond the fronts they
  forward to, and at macos-user are launch-owned listeners the keeper now holds. How [OQ-HS3](host-notch-services.md#OQ-HS3) reads
  under the keeper is [§9.9.9](#999-what-changes-for-a-host-services-lifetime).
- [`agent-event-watchers.md`](agent-event-watchers.md): host-side sidecars and the ping box
  become the keeper's at every notch ([EW-D24](agent-event-watchers.md#EW-D24),
  [EW-D25](agent-event-watchers.md#EW-D25), which replaces [OQ-JL5](#OQ-JL5)'s trap, EW-D19);
  the master session a ping wakes ([its §3.6](agent-event-watchers.md#36-which-session-a-ping-wakes-the-master)),
  whose session list lives in the box and not in the keeper; and
  [OQ-EW11](agent-event-watchers.md#OQ-EW11), which decides whether a ping box alone makes a
  keeper ([§9.9.8](#998-a-launch-with-nothing-long-lived)).
- [`durable-scratch-space.md`](durable-scratch-space.md): the per-launch scratch volumes, which
  the keeper's teardown still hands to the detached remover. Its step 3 (*"When the owning
  launch's command exits, the container stops, its attached sessions end"*) changes with this
  design.
- [`attach-skew-and-contract-guardrails.md`](attach-skew-and-contract-guardrails.md): the attach
  contract gate, unchanged here, and `jailSessionCount` (SK-D7).
