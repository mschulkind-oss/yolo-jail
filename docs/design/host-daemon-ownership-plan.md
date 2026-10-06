---
title: "Plan sketch: host-daemon ownership"
date: 2026-09-19
status: draft
stage: SKETCH
next: "Write a plan for HD-R1 against the tree, now unblocked on spawn: OQ-HD10 was answered 2026-10-05 (HD-D4), so the plan's per-workspace spawn guard on macos-user answers for each row of this sketch's table of what the spawn flock covers; this sketch is that plan's input, not the plan"
depends-on:
  - host-daemon-ownership.md#OQ-HD10
tags: [plan, sketch, daemons, loopholes, lifecycle]
summary: "The parking lot for implementation material from the host-daemon ownership design, pruned against the tree on 2026-10-01: what the spawn flock covers today that OQ-HD10 has to answer for, the spawn sites HD-R1 reworks, the OQ-HD4 note and the timing facts. Not a hand-off."
---

# Plan sketch: host-daemon ownership

**Status:** 2026-10-01 — pruned against the tree at `d4e435a3`. The three entries parked on
[OQ-HD2](host-daemon-ownership.md#OQ-HD2), [OQ-HD1](host-daemon-ownership.md#OQ-HD1) and
[OQ-HD6](host-daemon-ownership.md#OQ-HD6) are gone, and so is the documentation-drift list: the
first question is built, the other two were dissolved by
[HD-R1](host-daemon-ownership.md#HD-R1), and all three drifted documents now say the right thing
([what was pruned](#what-was-pruned-and-why)). What is left is the material a plan for HD-R1 will
need: [what the spawn flock covers today](#what-the-spawn-flock-covers-today), which is
what [OQ-HD10](host-daemon-ownership.md#OQ-HD10) has to answer for, the
[spawn sites the ruling reworks](#the-spawn-sites-hd-r1-reworks), the
[OQ-HD4](host-daemon-ownership.md#OQ-HD4) note, and the timing facts. Nothing here plans HD-R1
itself, and nothing here has been watched running beyond what the design records. Since
2026-10-05 [OQ-HD10](host-daemon-ownership.md#OQ-HD10) is answered from its Mac runs
([HD-D4](host-daemon-ownership.md#HD-D4)): per-jail daemons owe macos-user a per-workspace spawn
guard, and the table below is what that guard answers for.

**Design:** [`host-daemon-ownership.md`](host-daemon-ownership.md). **Precedence:** the
design wins on behavior; this file is the first thing here to be wrong.

> [!WARNING]
> **Do not build from this.** It is a parking lot that keeps implementation detail out of
> the design, not a hand-off artifact. A real plan's product is codebase knowledge — the
> map, the reuse, the traps — and only an agent that has just read the tree can write one.
> No design decision is made here; anything that would choose behavior is an `OQ-HD*` in
> the design instead.

---

## What was pruned, and why

Each entry below was checked against the tree at `d4e435a3` on 2026-10-01.

| Entry | Disposition | Evidence |
| :--- | :--- | :--- |
| Blocked on [OQ-HD2](host-daemon-ownership.md#OQ-HD2): generalize the management surface | **Built** 2026-09-20 | `yolo host-daemon {status,stop,restart,logs}` is `runHostDaemon` in [`commands.go`](../../internal/cli/commands.go). The wrong-command line the entry quoted now prints `broker.CycleCommand(name)` ([`hostdaemoncmd.go`](../../internal/broker/hostdaemoncmd.go)), from `startHostSingleton` in [`loopholesruntime.go`](../../internal/cli/run/loopholesruntime.go) and from [`launchcheck.go`](../../internal/cli/run/launchcheck.go). `yolo check`'s broker probe takes its socket and PID paths from `paths.HostSingleton*` ([`check/broker.go`](../../internal/cli/check/broker.go)), so the third copy of those paths is gone |
| Blocked on [OQ-HD1](host-daemon-ownership.md#OQ-HD1): a version in the rendezvous | **Dissolved** by HD-R1 | A launcher-spawned child is one build, so there is nothing for a version to tell apart. The entry was also stale: the three byte-frozen copies of the broker's paths it counted are one now, since `broker.BrokerSingletonSocket` and its siblings call `paths.HostSingleton*` ([`brokerlifecycle.go`](../../internal/broker/brokerlifecycle.go)) |
| Blocked on [OQ-HD6](host-daemon-ownership.md#OQ-HD6): making an idle singleton visible | **Dissolved** by HD-R1 | The design's [§5.2](host-daemon-ownership.md#52-what-the-ruling-deletes-from-that-table) row for `yolo check`'s zero-jails blind spot: with no jail running there is no daemon to report. The early return the entry described is still in `checkHostServiceLiveness` ([`sections_loopholes.go`](../../internal/cli/check/sections_loopholes.go)), and it becomes correct under the ruling rather than a gap |
| Documentation drift: the loopholes guide's manifest census omitted `host_daemon.scope` | **Fixed** | The census moved to [`writing-loopholes.md`](../../userguide/guides/writing-loopholes.md), which shows `scope` in the manifest example and has a `scope` bullet saying a `"host"` program keeps running when a jail exits |
| Documentation drift: `agent-credentials.md`'s current values named two `scope: "host"` daemons and not `aws-auth` | **Fixed** | Its current-values table has a row for each of the three, `aws-auth` included ([`agent-credentials.md`](../reference/agent-credentials.md)) |
| Documentation drift: the user guide's host-service lifecycle list had no host-scope carve-out | **Fixed** | The list moved to [`host-services.md`](../../userguide/guides/host-services.md), which describes a service declared in the user config; the carve-out for a pack's `"host"` program is the `scope` bullet in `writing-loopholes.md` |

---

## What the spawn flock covers today

The spawn flock is `paths.HostSingletonLock(name)`. `broker.EnsureSingleton` takes it, blocking,
and does all of the following inside it; `broker.BrokerSpawn` is the same call returning only the
socket path ([`brokerlifecycle.go`](../../internal/broker/brokerlifecycle.go)). [OQ-HD10](host-daemon-ownership.md#OQ-HD10) asks what
replaces it; this is the list of what it covers today, so an answer can be checked against each row. A
**rider** is [OQ-HD10](host-daemon-ownership.md#OQ-HD10)'s word for a duty the flock performs without being asked to, such as a
write the child makes while the spawner holds it. READ FROM CODE 2026-10-01; none of the "under
HD-R1" cells is measured, because nothing is built.

| What the flock covers | Today | Under HD-R1 |
| :--- | :--- | :--- |
| **Socket ownership and process identity**: the name-keyed socket, the PID file, the capability stamp and the settings record | The flock, at the `paths.HostSingleton*` paths | On macos-user the socket half has no shared path left to guard, if the retired daemons take the per-jail spawn path that the design's [§5.2](host-daemon-ownership.md#52-what-the-ruling-deletes-from-that-table) names. Since [HD-D1](host-daemon-ownership.md#HD-D1) each macos-user session has a host-services dir of its own, and a per-jail fronted daemon's upstream socket is keyed by that dir (`frontShortHash` and `frontSocketFile` in [`loopholesruntime.go`](../../internal/cli/run/loopholesruntime.go); [`servicessession.go`](../../internal/cli/run/servicessession.go)). So two sessions of one workspace would not share a socket. UNMEASURED: no per-jail credential daemon exists to run |
| **The Claude broker's CA and leaf mint**, which the child runs at startup while the spawner holds the flock across its socket wait | A lock of its own as well, `cert.lock`, taken by `EnsureCAAndLeaf` through `withCertLock` ([`cert.go`](../../internal/oauthbroker/cert.go)), since `4ac8f11bc` (2026-09-20) | Already independent of the spawn flock. [OQ-HD10](host-daemon-ownership.md#OQ-HD10) named this rider as an unlanded change; it landed |
| **Refreshes and the shared state they write** | `refresh.lock` for the Claude broker (`ConfigureStore` in [`store.go`](../../internal/oauthbroker/store.go)) and the OpenAI one (`openaiauth.Broker.LockPath`), `mint.lock` for `aws-auth` | Unchanged. This is the design's [§1.1](host-daemon-ownership.md#11-the-premise-the-scope-was-built-on-is-false) |
| **The OpenAI legacy-state migration**, the ensure's `PrepareLocked` hook | Runs inside the flock and takes no other lock. `prepareLegacyOpenAIAuthState` ([`openaiauthmigration.go`](../../internal/cli/run/openaiauthmigration.go)) reads the singleton's PID file to find a legacy daemon's working directory and argv, and returns the step that moves a `{state}` credential file into the canonical state dir; the ensure stops the running daemon before it runs that step | The design's [§5.2](host-daemon-ownership.md#52-what-the-ruling-deletes-from-that-table) keeps it ("migrate, then spawn") without naming the lock it runs under. Without the spawn flock, two launches could run the relocation at once, and its legacy-daemon test has no singleton PID to read. [OQ-HD10](host-daemon-ownership.md#OQ-HD10)'s text does not list this rider |
| **A changed-settings restart** ([HD-D2](host-daemon-ownership.md#HD-D2)) | Inside the flock, so two launches with the same new settings restart the daemon once | Retires with the singleton, as HD-D2 says |

**A second population has the same question, and [OQ-HD10](host-daemon-ownership.md#OQ-HD10)'s text names only
macos-user.** Three host-side actors reach the OpenAI daemon with no jail at all, by its
machine-wide name: `yolo host -- codex`, `yolo host -- pi` (the prelaunch, `Prepare`) and
`yolo openai-auth <status|import|logout>` ([`operator.go`](../../internal/openaiauthhost/operator.go)).
All three ensure it through `openaiauthhost.ensureSingleton`
([`host.go`](../../internal/openaiauthhost/host.go)). The private socket every host-side consumer
then dials is `openaiauthhost.HostSocketPath()`, derived from `paths.HostSingletonSocket`, and its
readers are [`internal/cli/host.go`](../../internal/cli/host.go),
[`macosuserservices.go`](../../internal/cli/run/macosuserservices.go) and
[`macosuserdoorways.go`](../../internal/cli/run/macosuserdoorways.go).

The design's [§5.2](host-daemon-ownership.md#52-what-the-ruling-deletes-from-that-table) covers only `ensureSingleton`'s kill-and-replace, as gone. Under
HD-R1 these actors still need a daemon. What they spawn, at what path, and what serializes two of
them at once is [OQ-HD10](host-daemon-ownership.md#OQ-HD10)'s question again, about the machine rather than a workspace.

[OQ-HD9](host-daemon-ownership.md#OQ-HD9) is the other ruling a build waits on: it decides whether
the retirement must add a refresh for when no jail runs, which today's background refreshers in the
singletons provide.

---

## The spawn sites HD-R1 reworks

Every place the tree ensures a host-wide daemon today, READ FROM CODE 2026-10-01. The design's
[§5](host-daemon-ownership.md#5-who-may-act-on-a-host-daemon) table is the authority for what each
actor may do; this is where each one lives.

| Site | Where | Reached by |
| :--- | :--- | :--- |
| `startHostSingleton` → `broker.EnsureSingleton` | [`loopholesruntime.go`](../../internal/cli/run/loopholesruntime.go) | every fresh launch, once per enabled host-scoped loophole |
| `brokerEnsure` → `broker.BrokerSpawn` | [`loopholesruntime.go`](../../internal/cli/run/loopholesruntime.go) | a launch, before the argv is built; the Claude broker only, from `broker.RealDeps`'s own argv |
| `yolo host-daemon restart` and the `yolo broker` alias | [`brokercmd.go`](../../internal/broker/brokercmd.go) | a human, with no launch |
| `openaiauthhost.ensureSingleton` | [`host.go`](../../internal/openaiauthhost/host.go) | `yolo host -- codex`, `yolo host -- pi`, `yolo openai-auth` |

[HD-D3](host-daemon-ownership.md#HD-D3) leaves one decision to this build: whether a per-launch
daemon also exits when its state directory is archived under it.

---

## Parked on [OQ-HD4](host-daemon-ownership.md#OQ-HD4) — the jail-daemon reclaim contract

- If "a jail daemon is a request-scoped forwarder holding no unflushed state" becomes a
  stated requirement, the natural home is the doc comments on the two declarations of a jail
  daemon, since those doc comments *are* the schema reference: `JailDaemon` in
  [`loopholedecl.go`](../../internal/loopholedecl/loopholedecl.go) for a loophole manifest's
  `jail_daemon`, and `ServiceJailDaemon` in [`contributes.go`](../../internal/packdecl/contributes.go)
  for a service pack's.
- A daemon that needed a graceful stop would need a `jail_daemon` key to say so, and the
  reclaimer would then have two branches. Worth resisting until a second daemon shape exists.

---

## Facts worth keeping, wherever this lands

Re-checked 2026-10-01.

- Timing constants, all in [`brokerlifecycle.go`](../../internal/broker/brokerlifecycle.go):
  spawn deadline 5 s, socket poll 50 ms, kill grace 3 s before `SIGKILL`, reach timeout 2 s.
  The front's stop grace is 2 s and the per-service readiness default is 5 s
  ([`loopholesruntime.go`](../../internal/cli/run/loopholesruntime.go)).
- Exactly one jail daemon is readiness-gated today — the wire bridge, the only name written
  into `YOLO_JAIL_DAEMON_READY_NAMES` by
  [`packservices.go`](../../internal/cli/run/packservices.go). Anything that reasons about
  "the boot notices" has to check that list rather than assume it.
- A failed `genStep` refuses the jail. That is the severity of any error returned from
  `startJailDaemonSupervisor`, including a reclaim that could not complete.
