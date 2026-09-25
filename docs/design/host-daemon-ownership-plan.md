---
title: "Plan sketch: host-daemon ownership"
date: 2026-09-19
status: draft
tags: [plan, sketch, daemons, loopholes, lifecycle]
summary: "The parking lot for implementation material that surfaced while writing the host-daemon ownership design. Not a hand-off: every entry rests on an unruled question, and nothing here has been checked against the tree as a build plan."
---

# Plan sketch: host-daemon ownership

**Status:** SKETCH, 2026-09-19 — incomplete, and unstable while questions are open.

**Design:** [`host-daemon-ownership.md`](host-daemon-ownership.md). **Precedence:** the
design wins on behavior; this file is the first thing here to be wrong.

> [!WARNING]
> **Do not build from this.** It is a parking lot that keeps implementation detail out of
> the design, not a hand-off artifact. A real plan's product is codebase knowledge — the
> map, the reuse, the traps — and only an agent that has just read the tree can write one.
> No design decision is made here; anything that would choose behavior is an `OQ-HD*` in
> the design instead.

---

## Blocked on [OQ-HD2](host-daemon-ownership.md#OQ-HD2) — generalizing the management surface

The lifecycle half already generalized; only the CLI is name-bound. What a builder would
need to know:

- `broker.CLIRealDeps()` → `RealDeps()` → `SingletonDeps(BrokerLoopholeName, BrokerSpawnArgv(...))`.
  `SingletonDeps` already takes a name and derives every path from it, so the change is
  in the caller, not the engine.
- The argv is the obstacle worth naming early. `RealDeps` carries the broker's own argv as
  a literal because it is reached "from the launch path before discovery has run, and from
  `yolo broker restart`, which has no launch at all". A generalized verb has to resolve a
  loophole record from outside a launch — which means running discovery, which means a
  workspace and a config, which `yolo broker` today does not need. That is the real design
  cost, and it is why this is not a rename.
- `broker.Status`'s json tags are already the `--format json` document; a per-daemon report
  reuses it unchanged.
- The wrong-command string lives in `startHostSingleton`
  ([`loopholesruntime.go`](../../internal/cli/run/loopholesruntime.go)) and reads
  `Fix it with: yolo broker restart`. `reportFailedSpawn` in
  [`brokerlifecycle.go`](../../internal/broker/brokerlifecycle.go) is the worked example of
  the same message made record-driven — copy its shape, including the empty-`Name` fallback.
- `yolo check`'s singleton probe duplicates the socket and PID literals rather than calling
  `paths.HostSingleton*`, and has **no lock literal**. Whatever generalizes should collapse
  that third copy.

## Blocked on [OQ-HD1](host-daemon-ownership.md#OQ-HD1) — a version in the rendezvous

- The unit would be a **wire-contract generation**, which already exists as a constant:
  `singletonStamp = "fronted-preamble-v1"`. Promoting it into the path means
  `paths.HostSingletonSocket` gaining a second input, and the `sun_path` budget is the
  constraint to check first (108 bytes on Linux, 104 on darwin) — today's longest name is
  `claude-oauth-broker`.
- Three places carry byte-frozen copies of the Claude broker's paths and a test pins them
  equal: `broker.BrokerSingleton*`, `paths.HostSingleton*`, and `internal/cli/check`'s own
  literals. A path change touches all three plus the pin.
- `RealPgrepStrays`'s dual pattern is the precedent for "recognize the previous generation
  for one release", and its comment already says when to drop the legacy alternative.

## Blocked on [OQ-HD6](host-daemon-ownership.md#OQ-HD6) — making an idle singleton visible

- The cheap half is a `yolo check` section, not a reaper. `checkHostServiceLiveness` already
  walks every loophole with a `HostDaemon`, but probes the per-jail endpoint file and
  returns early when no jails are running. A host-scoped daemon's liveness has no jail in it
  and should not be behind that early return.
- If enumeration lands ([OQ-HD7](host-daemon-ownership.md#OQ-HD7)), the same list is what a
  docs check could compare the prose tables against.

## Blocked on [OQ-HD4](host-daemon-ownership.md#OQ-HD4) — the jail-daemon reclaim contract

- If "a jail daemon is a request-scoped forwarder holding no unflushed state" becomes a
  stated requirement, the natural home is the `jail_daemon` doc comment in
  [`packdecl`](../../internal/packdecl/packdecl.go) / the loophole manifest reference, since
  those doc comments *are* the schema reference.
- A daemon that needed a graceful stop would need a `jail_daemon` key to say so, and the
  reclaimer would then have two branches. Worth resisting until a second daemon shape exists.

## Not blocked — documentation drift the design found

These three are wrong in the tree today and need no ruling. They are listed here rather than
fixed in passing because they are somebody else's files and each is a one-line edit with its
own commit:

- [`../guides/loopholes.md`](../../userguide/guides/loopholes.md): the manifest schema census omits
  `host_daemon.scope`, and its `host_daemon` comment describes the per-jail lifecycle
  ("yolo spawns this ON THE HOST at jail startup") for a block that can declare the
  host-wide one.
- [`../reference/agent-credentials.md`](../reference/agent-credentials.md): the
  current-values table names two `scope: "host"` daemons; `aws-auth` is absent from the file.
- [`../guides/USER_GUIDE.md`](../../userguide/README.md): the host-service Lifecycle list says
  the container exiting `SIGTERM`s and `SIGKILL`s each service, with no host-scope carve-out
  — the opposite of the ruling.

## Facts worth keeping, wherever this lands

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
