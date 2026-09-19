---
title: "The instrument stops at the boundary"
date: 2026-09-19
status: in-review
tags: [diagnostics, observability, entrypoint, boundary, logging, launch]
summary: "yolo's observability facility is mature and entirely host-side. The one dial that calls itself a diagnostics gate, --verbose, has no vocabulary past the host↔jail boundary at all — two of its jail-side call sites read a variable P5 guarantees never arrives. So every failure whose answer lives inside the container is diagnosed by hypothesis instead of measurement. The fix is not more lines: it is a jail-side diagnostic tier writing to the sink that already survives a refused boot, a bounded state snapshot at every give-up led by a listener inventory read from /proc, and a give-up discipline for the abandon-with-no-report shape — measured at 153 sites where the hazard is documented and still reports nothing, against 316 where it was made loud. Two constraints are forced rather than chosen: boot.log is closed at handover, and a give-up ON a sink cannot be reported TO that sink, which is why the canonical specimen is still silent."
---

# The instrument stops at the boundary

**Status:** DESIGN, 2026-09-19, evidence read at `16ef96cb`. Five rulings owed; pieces of
[§4.3](#43-the-listener-inventory-in-go)/[§4.4](#44-a-give-up-reports-the-rule-and-its-narrow-shape)
are in flight concurrently and nothing here waits on them ([§7](#7-risks) R3).

> **In short.** The problem is not log volume. yolo's observability facility is careful,
> principled — and it stops at the container wall, so the measurements that settle a
> jail-side failure are exactly the ones no sink carries. More host-side verbosity would
> not have shortened a single one of today's six investigations.

**Why it matters.** Four wrong hypotheses went into the `127.0.0.1:8214` collision because
the decisive fact — *which process in that jail holds the port* — is recorded nowhere, and
the container is gone at the refusal. The host answer actively misled: `ss -ltnp` was empty,
the host end being a UNIX socket.

**The shape.** A **jail diagnostic tier** *(coined here)* — `boot.log` through the log-only
channel that already exists for boot-time facts, and one service-independent sink for
lifecycle facts, because `boot.log` is closed at handover; a bounded **boot snapshot**
*(coined here)* at every give-up, led by a listener inventory read from `/proc`; and one rule
for the give-up shape.

**Cost.** One more dial crossing the boundary, which `perf-logging.md`'s P5 makes a deliberate
act. Nothing added to the launch terminal. One possible new sink, and only because a give-up on
a sink cannot be reported to it ([OQ-DB5](#oq-db5)).

**Start at [§3](#3-the-failures-are-the-specification)** — the six failures and the one
measurement each needed. [§4](#4-the-proposal) falls out of that table and nothing else.

**Needs your ruling:** [OQ-DB1](#oq-db1), [OQ-DB2](#oq-db2), [OQ-DB3](#oq-db3),
[OQ-DB4](#oq-db4), [OQ-DB5](#oq-db5).

**Reads with:** [`perf-logging.md`](../reference/perf-logging.md) (the host half this extends;
P5 forbids the obvious shortcut), [`report-tiers.md`](../reference/report-tiers.md) ([`OQ-RO3`](../reference/report-tiers.md#why-its-this-way),
the ruling this must not dent), [`wire-bridge-port-collision.md`](wire-bridge-port-collision.md)
(the motivating investigation; its [`OQ-PC2`](wire-bridge-port-collision.md#oq-pc2) owns *disclosure*, a different question from
*recording*), [`loopback-tls-reachability.md`](../reference/loopback-tls-reachability.md) (the
witness whose failure is this design's trigger).

---

## 1. Goal, and non-goals

**Goal.** Make a jail-side failure diagnosable from the artifacts one failed launch leaves
behind, without a second launch and without a live container.

### Non-goals — what this does not license

- **Not a level system for the launch terminal.** Nothing here adds, removes or gates a
  line the launch prints. [§5](#5-why-this-is-compatible-with-the-no-quiet-mode-ruling) is
  the argument, and it is load-bearing rather than defensive.
- **Not a tracer, and not a metrics system.** No spans with children, no attributes, no
  propagation, no counters. [`perf-logging.md`](../reference/perf-logging.md)'s
  *What this does not do* already rules that and this inherits it whole.
- **Not a remote sink.** Everything lands in files a human can `cat`, in directories that
  already exist.
- **Not forwarding `YOLO_VERBOSE`.** That is the obvious move and P5 forbids it, for a
  stated reason: a second host↔jail spelling of one variable is a deploy-skew bug, and the
  two halves of yolo deploy on different cadences ([§2.3](#23-the-one-dial-that-crosses-and-the-one-that-does-not)).
- **Not a fix for any of the six failures.** Each has or will have its own owner. This
  designs the instrument that would have found them.

## 2. What exists today, precisely

### 2.1 The host half is mature

`internal/perf` plus the run pipeline give the host process spans, marks, records, an
incremental file sink, a slow-span notice, and a Window A attribution for the stretch no
yolo code is present for. Two gates separate **recording** from **reporting**, and the rule
for classifying the next opt-in is written down: an explicit per-invocation flag prints, a
persistent setting records silently. Read
[`perf-logging.md`](../reference/perf-logging.md); this doc changes none of it.

### 2.2 The jail half is one always-on log and a two-line gated vocabulary

The jail side is not uninstrumented. It has a good sink and almost no writers for the part
of it that matters.

- **`boot.log`** is a tee installed on `e.Stderr` itself, so every warning the boot path
  emits is persisted in `<workspace>/.yolo/` — on the bind mount, which is the whole point:
  it survives a boot that refused, when there is no container left to read anything from
  (`internal/entrypoint/bootlog.go`). It rotates one generation aside as `boot.log.prev`,
  because the natural reaction to a broken jail is to launch it again, and that would
  otherwise overwrite the only evidence.
- **`Env.LogOnly`, reached through `e.note`,** is a second channel into the same file that
  does *not* reach the terminal. Its docstring states the exact problem this doc
  generalises: *"silence is ambiguous exactly where it is most expensive… 'ran and was
  silent' then reads identically to 'never ran'"* (`internal/entrypoint/env.go`).
- **The gated jail vocabulary is two call sites**, both in
  `internal/entrypoint/runtime.go`: the supervisor-reuse notice and the
  per-service-ready notice. Both are about **daemon lifecycle**, not about timing, and both
  are gated on `YOLO_JAIL_TIMING`.

The uneven part is measurable. In `internal/entrypoint`, `e.warn` has **62** production
call sites and `e.note` — the detail channel, the one with no volume budget because nobody
is watching the file — has **four**: the boot catalog, one host-layer note, one reconcile
note, and the reachability roll-up. The channel was built, argued for at length, and then
used four times.

### 2.3 The one dial that crosses, and the one that does not

| Dial | Set by | Reaches the jail? | What it does there |
| :--- | :--- | :--- | :--- |
| `YOLO_JAIL_TIMING` | the launcher, as an explicit `-e` pair on the container argv, under the **reporting** gate | **yes** | three `date +%s%N` deltas in generated bash, a re-print of `~/.yolo-perf.log`, and the two `runtime.go` notices |
| `YOLO_VERBOSE` | `--verbose`/`-v` at the front door, via `os.Setenv` | **no** | nothing |

`YOLO_VERBOSE` does not reach the jail on any backend, and this is by ruling rather than by
oversight — P5: *"read by the launcher and deliberately not forwarded… so a grep for one can
never match the other and forwarding can never look intended."* Verified three ways at
`16ef96cb`: the container env is a closed list of explicit `-e` pairs in
`internal/cli/run/assemble.go` and `YOLO_VERBOSE` is not among them; no code path passes
`--env-host`; and `macos-user` crosses into its sandbox through
`sudo --user=… /usr/bin/env -i …`, a scrubbed environment whose baked list contains no
verbosity variable (`internal/macosuser` has zero references to one).

> [!WARNING]
> **Two jail-side conditions read `paths.VerboseEnv`, and that conjunct is dead.**
> `internal/entrypoint/runtime.go` gates both its notices on
> `VerboseEnv != "" || YOLO_JAIL_TIMING != ""`. The first disjunct can never be true in a
> jail. The lines still work — the second disjunct is live — so nothing fails, no test
> touches `VerboseEnv` anywhere in `internal/entrypoint`, and the code reads as though the
> host flag reaches the jail. It does not. This is what a boundary with no vocabulary of its
> own looks like from the inside: an author reached for the host's spelling because there
> was no jail one to reach for.

`internal/cli/verbose.go`'s header still calls itself *"the future gate for launcher
diagnostics, wired in v1 to the same timing-span surface `--timing` drives"*. That
reservation was **spent on 2026-09-12**: [`OQ-RO2`](../reference/report-tiers.md#why-its-this-way) made `--verbose` the detail gate for
`yolo host apply`'s report (`reportVerbose`, `internal/cli/hostapplydetail.go`), and
[`perf-logging.md`](../reference/perf-logging.md) records the spending in its own
*What this does not do*. So `--verbose` is not a placeholder. It has three host-side
meanings — the timing report, `yolo stop`'s table, and the `host apply` detail view — and
**zero jail-side meaning**, which is a narrower and more actionable statement than
"placeholder".

### 2.4 The sinks, and who owns each

**Fourteen diagnostic files, three directories, and no index except the `diagnosing-the-jail`
skill.** Every writer named below is the only writer of its file — the one-writer property
holds throughout, which is the good news in this table.

| Sink | Sole writer | Gate | Bound | Survives a refused boot |
| :--- | :--- | :--- | :--- | :---: |
| `<ws>/.yolo/boot.log` | `entrypoint.attachBootLog` (tee on `e.Stderr`, + `e.LogOnly`) | always-on | **1 generation** (`boot.log.prev`) | **yes** |
| `<ws>/.yolo/launch.log` | `run.teeLog` | always-on | newest 49 run blocks | yes (host-side) |
| `<ws>/.yolo/host-perf.log` | `perf.FileSink` | recording gate | newest `perf.MaxRuns` = 50 runs | yes (host-side) |
| `<ws>/.yolo/housekeeping.log` | `run.housekeepingNote` | when a slot fires | **none** | yes (host-side) |
| `<ws>/.yolo/startup.log` | a shell `tee` in the generated final command (`provision.StartupLog`) | always-on | **none** | only if provisioning was reached |
| `<ws>/.yolo/receipts.jsonl` | `entrypoint.AppendReceiptLine` — the one sink that **returns** its error | when a capture realizes | **none** | yes |
| `~/.yolo-perf.log` | `entrypoint`'s `perfLog.dump` | always-on | last 50 runs, **hand-rolled** | via the home overlay |
| `~/.yolo-socat.log` | `entrypoint.startContainerPortForwarding`, fd inherited by every forked `socat` | port forwards configured | **none** | via the home overlay |
| `~/.yolo-shared-creds.log` | `entrypoint.logSharedCreds` | a `shared_credentials` hook | **none** | via the home overlay |
| `~/.local/state/yolo-jail-daemons/<name>.log` | `internal/supervisor` (`openLog`, `child.logf`) | always-on per daemon | **5 MB × 2** generations | via the home overlay |
| `<state>/logs/host-service-<name>.log` | `run` loophole runtime (child's stdout+stderr) | per spawned host service | **none** | host-side |
| `<state>/logs/<cname>-socat.log` | `run.network` — open error **discarded** | `forward_host_ports` | **none** | host-side |
| `<state>/logs/crossings.log` | `internal/crossaudit` | always-on per crossing | **4 MiB + 1 archive = 8 MiB, ever** | host-side |
| `<state>/logs/cgroup-delegate-audit.log` | `internal/cgd.Append` | on a delegate request | **none** | host-side |

Four facts follow, and each shapes [§4](#4-the-proposal):

1. **`boot.log` is the only sink designed for the refused-boot case**, and the only one whose
   retention is expressed in *boots* rather than in *runs* — which is the unit a diagnostic
   reader actually has ("did it work last time?").
2. **Eight of the fourteen have no bound at all**, including every host-side service log and
   both in-jail append logs. `internal/perf/filesink.go` states the doctrine — *"a
   per-workspace diagnostic file must be self-limiting or it quietly becomes a
   disk-exhaustion bug on a directory the user cannot see into from the jail"* — and exports
   `TrimRunsInFile` so there is one implementation. Those eight never adopted it, and a ninth
   — `~/.yolo-perf.log` — **re-implements it by hand** in `boot.go`. That is the drift
   `filesink.go`'s own comment predicts, arrived at in both available directions.
3. **Two of the fourteen ever tell a human the sink itself failed** — `host-perf.log`
   (`warnOnce`) and the cgroup audit log, which warns once and then keeps *returning* failure
   so a later successful append cannot be mistaken for a complete record. One more,
   `receipts.jsonl`, returns its error. The other eleven fail silently by explicit design.
4. **`startup.log` is the exception to the invariant the workspace-side sinks rest on.**
   `launch.log`'s own header states the rule — the `.yolo` directory is inside the live
   workspace bind, so *"a jail can write the host's record. Nothing reads this file back to
   make a decision, which is what keeps that a disclosure rather than a trust hole."*
   `startup.log` **is** read back to make a decision: `jailcontent.ReadProvisioningFailed`
   greps it from the host side to decide whether to print the provisioning-failure banner.
   Noted rather than fixed here; it bounds what any new sink may be used for
   ([§4.2](#42-the-boot-snapshot-and-what-it-captures)).

A fifth, smaller fact: a reader who does not already know this table cannot find the daemon
logs. The one line that names them (`"  Daemon diagnostics: …"`) prints only on a launch that
waits for readiness.

## 3. The failures are the specification

Six real investigations from 2026-09-19. Each row is an acceptance test: the design is
right if the middle column would have been in an artifact before the second hypothesis was
formed.

| Failure | The measurement that settles it | Where it belongs |
| :--- | :--- | :--- |
| **`127.0.0.1:8214` held before the bridge binds it.** Four hypotheses; the cause was a provider-table aliasing that let a pack's jail-loopback address into the user-provider table, so the in-jail `socat` took the port before the supervisor started | **the jail's own listener table, with owning PIDs and argv**, at the moment the bind failed. `ss -ltnp` on the host was empty because the host end is a UNIX socket | the boot snapshot, at readiness failure |
| **A ~10 s silent gap at teardown**, between the agent's goodbye and yolo's last two lines | a span or mark covering the gap — the host half already has the shape (a dangling `start` is the answer to "who is doing it"); what is missing is a span over that stretch | host `host-perf.log`; owned elsewhere — see [§7](#7-risks) R3 |
| **`dropComputedTables` blamed for the wrong remedy.** Dropping a user's `enabledPlugins` or `env` entry prints *"add under `mcp_servers` to keep it"* | the dropped key's own name in the remedy. The message is a `Fprintf` with the table name in the subject and `mcp_servers` hardcoded in the predicate | not a diagnostic-tier problem at all — a one-line defect, [§3.1](#31-one-of-the-six-is-not-this-designs-problem) |
| **`supervisor.waitTimeout` abandons live goroutines** after 10 s and reports nothing, leaking a process that can hold a port — which is how the 8214 port came to be held | *that the deadline fired*, and which children had not settled. ⚠ **It was examined and deliberately left unfixed**, and the reason is this design's central constraint rather than an oversight: the only channel available is the per-daemon log that may itself be the wedged thing ([§4.1.1](#411-the-sink-may-not-be-the-resource-under-diagnosis-and-it-must-outlive-the-boot)) | a sink independent of any service — [OQ-DB5](#oq-db5) |
| **The Apple Container stale-image loop** (three compounding faults) | which image identity the launch resolved, which it found loaded, and why it did not replace it — each as a recorded fact rather than an inference from a retry | host-side; `launch.log` already carries the disclosures, so this is a *coverage* gap in what the image path states, not a tier gap |
| **A darwin-only `unlinkat …: bad file descriptor`** surfacing in an unrelated test. Cause: `os.NewFile` arms a finalizer unconditionally, so every daemon spawn minted a second owner of an inherited fd and a GC closed it. Fixed in `16ef96cb`/`281acc5a`; the `terminate`/`start` publish race found alongside it was a real but separate bug (`13ecc5da`), **not** this failure's cause | that a process held a descriptor it did not own. ⚠ **The only visible trace was GitHub's own orphan-process cleanup step** — the diagnosis came from CI infrastructure rather than from anything yolo emitted, which is this table's thesis arriving from outside the product | CI; out of scope for the tier, and named so the table is honest |

**Three of the six are inside this design's reach, and the table says which.** That is the
useful reading of it: a diagnostic tier is not a universal remedy, and pretending otherwise
is how one gets built for the wrong three.

### 3.1 One of the six is not this design's problem

The wrong-remedy message is worth stating precisely, because the fault was mis-attributed
during the investigation and the mis-attribution points at the wrong file. `dropComputedTables`
(`internal/agentcfg/staterender.go`) reports **nothing at all** — it silently drops keys from
an adopted residue, and the over-drop it is known for is
[`roadmap.md`](../plans/roadmap.md) row `0b`. The message naming `mcp_servers` comes from a
different pass in a different package: `noteDroppedManagedEntries`
(`internal/entrypoint/prism.go`), which interpolates the dropped table's name into the
sentence and then hardcodes `mcp_servers` in the remedy clause, so `claude/settings`'
`enabledPlugins` and `env` both get advice that cannot be followed.

It earns its row for one reason: **a remedy that names the wrong key is worse than no
remedy**, and it is the same defect class as a give-up with no report — a diagnostic that
is present, confident and wrong. The fix is one `Fprintf`, and it belongs to whoever closes
row `0b`.

### 3.2 The 8214 failure, as the worked case

The ordering that makes it fatal is a fact, re-verified independently of
[`wire-bridge-port-collision.md`](wire-bridge-port-collision.md): `internal/entrypoint/boot.go`
calls `startContainerPortForwarding` and *then* `startJailDaemonSupervisor`, four lines
apart. Two properties of the earlier call are what made the failure mute:

1. **The collision branch is the one silent branch in a loud function.** An invalid entry
   warns, a missing socket warns, a missing `socat` warns, a failed `Start` warns — and
   `if portInUse(localPort) { continue }` says nothing, in either direction. A forward that
   silently did not happen and a forward that silently stole a port are the same
   observation from outside.
2. **`portInUse` is the repo's only listener probe, and it probes by binding.** It answers
   *occupied* and can never answer *by whom* — which is the only answer that was wanted.
   There is no `/proc/net/tcp` parsing anywhere in the tree at `16ef96cb`.

Note what this is not: [`OQ-PC2`](wire-bridge-port-collision.md#oq-pc2) in the sibling doc asks whether an implicit provider forward
should be **disclosed** on the launch terminal. That is a question about the user's
attention and it stays there. This doc asks only whether it is **recorded**, which is a
question about a file, and the two answers are independent.

## 4. The proposal

### 4.1 The jail diagnostic tier

A **jail diagnostic tier** is the class of jail-side fact that is worth persisting and not
worth a terminal line: a check that ran and found nothing, a table of state at a decision
point, the branch a bounded wait actually took. It is a tier in exactly
[`report-tiers.md`](../reference/report-tiers.md)'s sense — a property of the *fact*, assigned
where the fact is produced — and deliberately not a level an emitter picks in the moment.

**For a fact produced during the boot, its sink is `boot.log`, through `e.note`** — no new
file, no new retention policy, one existing writer. Three consequences, all of them wanted:

- It is **absent from the terminal by construction**, so it cannot dent [`OQ-RO3`](../reference/report-tiers.md#why-its-this-way).
- It **survives a refused boot**, because that is what `boot.log` was built for.
- It is **bounded in boots, not in runs** — one generation of history, which is the unit
  "did it work last time?" is asked in.

What gets a tier line, at minimum, for this design to be done:

1. **Every bounded wait states which branch it took** ([§4.4](#44-a-give-up-reports-the-rule-and-its-narrow-shape)).
2. **The port-forward loop states every skip**, with the port and the reason — including
   the `portInUse` branch, which today is the silent one.
3. **The daemon supervisor states its lifecycle**: started with pid N, reused the live one,
   reclaimed an orphan, each service's readiness verdict. The two `runtime.go` notices
   already say two of these and are gated on a timing dial; they become tier lines and lose
   the gate.

Items 1 and 3 are where "the sink is `boot.log`" stops being true, for two independent
reasons, and getting this wrong is what has kept the canonical defect unfixed.

#### 4.1.1 The sink may not be the resource under diagnosis, and it must outlive the boot

**Two hard constraints, both already stated in the tree, neither guessable from the sink
table.** They are constraints rather than choices, which is why they are here and not in an
Open Question — what is open is only which sink satisfies them
([OQ-DB5](#oq-db5)).

**First: `boot.log` is closed at handover, so it cannot carry a lifecycle fact.**
`bootLog.finish` closes the file, and `internal/entrypoint/runtime.go` says what that means
for anything reporting later — the supervisor's `Wait` error *"arrives … after Main closed
boot.log, so e.Stderr is a MultiWriter over a closed file. Reporting from here would be a
write race on a sink that no longer exists, against a reader who has already been handed the
terminal."* The same file states the correct resolution one function over, for the socat
case: a forward's diagnostics go to the log it inherited, *"which outlives the boot and is the
right reader for a forward that dies hours in."* So the tier has **two sinks by necessity**:
`boot.log` for boot-time facts, and something else for everything a jail does afterwards.

**Second, and sharper: a give-up ON a sink cannot be reported TO that sink.** This is the
measured reason `supervisor.Run`'s `waitTimeout` is still silent. It was examined
deliberately and left, because the supervisor's only reporting channel is `child.logf` →
`openLog` → **the same per-daemon log that may itself be the wedged thing being waited on**,
and its own stderr is `nil` by construction (`startJailDaemonSupervisor` sets `cmd.Stderr =
nil`, which `supervisor.go` records as *"the supervisor's own stderr is /dev/null in a real
jail … So an error reported anywhere else is an error reported nowhere"*). The obvious
instruction — *write to the sink the package already uses* — is **exactly wrong here**, and
following it produces either a report nobody can read or a test that asserts a sentence
rather than an observation.

So: **the tier needs one sink that no single service can take down with it.** That is the
requirement. Which sink meets it is [OQ-DB5](#oq-db5), and an implementer who is not handed
an answer will reach the same dead end and leave the same gap — which is the outcome this
subsection exists to prevent.

### 4.2 The boot snapshot, and what it captures

A **boot snapshot** is a bounded dump of jail state written into `boot.log` at a point where
the boot is about to give up. Its trigger is precise: **a readiness failure or a refused
boot** — the two paths where the container is about to stop existing.

It captures, in this order:

1. **The listener inventory** — every listening socket in the jail's network namespace with
   the owning pid, process name and argv ([§4.3](#43-the-listener-inventory-in-go)).
2. **The daemon roster** — for each name in `YOLO_JAIL_DAEMONS`: pid if alive, the endpoint
   file's presence and mtime, and the **last 20 lines** of its
   `~/.local/state/yolo-jail-daemons/<name>.log`. This is the step that stops the reader
   needing to already know [§2.4](#24-the-sinks-and-who-owns-each)'s table.
3. **The boundary facts** — the `bootLogFacts` set is already written at the head of every
   `boot.log` and is not repeated; the snapshot adds only what has *changed* since, which
   today is nothing, so this component starts empty and exists to be extended.

**Bounds, with units.** 20 lines per daemon log; **64 KiB** total for one snapshot, and the
listener inventory is written first so a truncation loses the least valuable component.
Truncation is announced in the snapshot itself — a diagnostic that silently truncated is
this design's own failure mode.

**It is never fatal, and it is never a step the boot waits on.** Same ruling as
`attachBootLog`'s and `perf`'s P2, for the same reason: an instrument that can fail the thing
it measures is a worse bug than the blindness. Every failure inside the snapshot degrades to
a single line in `boot.log` naming the component that failed, and the refusal the boot was
already going to emit is unchanged and unreordered. The snapshot is written **before**
`bootLog.finish` closes the file.

**Degenerate cases, stated.** No `YOLO_JAIL_DAEMONS`: the roster is one line saying so — an
empty roster and a roster that was never gathered must not read alike, which is `e.note`'s
own argument. No listeners: likewise one line. `/proc` unreadable, or a non-Linux backend
where it does not exist: the inventory is one line naming the reason and the snapshot
continues. A pid that exits mid-walk: the row is emitted with what was read, marked
incomplete; no retry, no lock.

### 4.3 The listener inventory, in Go

Parsed from `/proc/net/tcp`, `/proc/net/tcp6` and their UDP siblings for the socket→inode
mapping, joined to owners by walking `/proc/<pid>/fd` for `socket:[<inode>]` links. **No
subprocess, and specifically not `ss` or `lsof`**: the jail may bake neither, `macos-user`
bakes nothing at all, and a diagnostic whose availability depends on `packages:` is a
diagnostic that is missing from the minimal jail that most needs it.

One property is worth stating because it is what the 8214 case turned on: **the inventory is
taken from inside the namespace**, so it sees what the jail sees. A host-side `ss` cannot,
and in that case actively misled — the host end of the forward was a UNIX socket, so the
host's TCP table was correctly empty.

Permission is not a blocker and should not be assumed to be one: the entrypoint runs as the
same identity as the daemons it is inventorying. A socket whose owner cannot be resolved is
listed **with its inode and no owner**, which is still the answer to "is this port taken".

### 4.4 A give-up reports: the rule, and its narrow shape

**Any code path that abandons work on a deadline states that it did, and what it abandoned.**
That is the whole rule. It is not "log more"; it is a ban on one shape: **a bounded wait, in
a lifecycle or teardown path, whose two branches are indistinguishable to the caller.**

#### The belief is not what is missing

This is the part worth getting right, because it inverts the obvious framing. Silence-as-defect
is not a scattered motif in this codebase — it is its **dominant explanatory idiom**. A
comment-level survey of `internal/` + `cmd/` found **946 distinct production sites** reasoning
about silence (`silently` alone accounts for 516 lines), clustered in the launch pipeline
(`internal/cli/run` 173, `internal/cli` 134) and the entrypoint (105). A further **1,004 sites
in test files**, of which **161 encode the polarity in the test's own name**.

Classifying the production sites by what the adjacent code actually does:

| Class | Count | What it is |
| :--- | ---: | :--- |
| **(a) made loud** | 316 | the hazard was named and a report was added beside it |
| **(b) hazard noted, still silent** | 153 | the give-up is documented, and still reports nothing |
| **(unclassifiable)** | 477 | file- or type-level historical prose, no adjacent branch to judge |

**So the deficit is not belief, and it is not documentation — it is application, and it is
measurable at 153 sites.** The `socat` loop is the cleanest illustration in the tree: four
loud branches and one silent one in the same `for` body, written by an author who plainly
held the belief. `internal/cli/run/loopholesruntime.go` carries the density that makes the
shape worth naming rather than fixing one at a time — **18** discarded errors and **four**
`case <-time.After` branches, of which **three are silent give-ups** and the fourth is a
50 ms loop pacer, not a give-up at all. Two of those three write the hazard down and delegate
it: *"a wedged front must not hold up teardown, and the sockets-dir rmtree is the backstop."*
That is class (b) in one sentence.

#### The carve-out: when the reporting channel is what broke

A distinct sub-pattern runs through the (b) set and it is **not** a defect — it is a design
position, arrived at independently in at least four places (`supervisor`, `launchlog`,
`perf/filesink`, `perf`). `internal/supervisor` states it best: *"Best-effort by necessity — a
supervisor cannot report that it could not report. When openLog is itself what failed, the
caller's error is lost, and that is the one remaining silent path."* `launch.log` reaches the
same answer from the other end — a log that announces its own failure adds a line to the
launch stream that P4 requires stay readable, so it degrades silently by choice.

**The rule does not apply to a sink reporting on itself.** Stating the carve-out is what keeps
the rule narrow enough to be a rule: without it, the first honest reading of this section is a mandate
to make fourteen sinks announce their own failures onto the terminal [`OQ-RO3`](../reference/report-tiers.md#why-its-this-way) protects.

#### The canonical specimen, and the shape of the test that misses it

`internal/supervisor`'s `waitTimeout` `select`s between a `done` channel and `time.After` and
returns **identically either way**, so ten seconds of abandoned goroutines and a clean settle
are one event to its caller. Note the contrast one file over: `waitAndMaybeRestart`'s docstring
states the rule this design generalises — *"Every give-up is announced in `<name>.log`: an
abandoned daemon that says nothing is indistinguishable from a running one"* — and honours it
in three branches. The same file both states the rule and, twenty lines later, breaks it.

One existing test is worth reading before writing any gate for [OQ-DB4](#oq-db4), because it
is exactly the shape [`AGENTS.md`](../../AGENTS.md) warns about — *a test that pins the CALLEE
while the CALL SITE is unpinned*. `internal/cli/run/flock_test.go` names the defect outright
(`"no waiting notice while the lock was held (the silent-hang defect)"`) and pins
`flock.go`'s notice. But the string `"Waiting for concurrent jail launch"` occurs in exactly
three places in the tree — the format string, the notice, and that test's own assertion — and
**nothing asserts the production wiring** at `run.go:924`, which supplies the `waiting`
closure. Delete that one line and the silent hang returns with a green suite. `flock.go`
itself flags the adjacent hazard (a `warn, waiting` argument pair that "would be silent and
exactly backwards" if transposed) and mitigates it with a struct rather than a test.

What the rule must not become is a general "handle every error" campaign. `_ = os.Setenv` on
an unset variable is fine, `internal/perf`'s package-level ruling that *"no method returns an
error; sinks swallow theirs and go quiet"* is ruled ground, and `internal/loopholes`'
swallow-per-manifest is a documented interface contract. The 153 is a population to work
through with judgement, not a lint target.

### 4.5 What `--verbose` means past the boundary

It means *"the launcher puts the jail dial on the container argv"* — never *"the jail reads
`YOLO_VERBOSE`"*. The precedent is already in the tree and is the right one:
`YOLO_JAIL_TIMING` is a separately-named variable the launcher emits as an explicit `-e`
pair, host-side-generated, moving in one commit with the code that reads it.

Three consequences:

- **The dead conjunct in `runtime.go` goes.** Its two notices become tier lines
  ([§4.1](#41-the-jail-diagnostic-tier)) and stop being gated on a timing dial, which is not
  what they are about.
- **The new dial is named where it is enforced**, not in a reference table. `YOLO_*` dials
  have no authority row by policy, so its documentation is: the invariant in
  [`AGENTS.md`](../../AGENTS.md) that names it, a `config-ref` entry only if it becomes a
  config key, and the boot-log header. Concretely it joins `bootLogFacts`, which is already
  the list of launch-shaping decisions recorded inside the jail — *"from in here they are
  unknowable, and each one changes how a later line in this same log should be read"* is a
  description of exactly this dial.
- **Whether the host flag implies it** is [OQ-DB3](#oq-db3).

### 4.6 Retention and size, decided

For the boot half, no new sink and therefore no new retention policy: `boot.log` keeps one
generation, the snapshot is capped at 64 KiB and rides inside it, and the upper bound this
design adds is two boot logs per workspace. **The lifecycle half may need one new sink**
([OQ-DB5](#oq-db5)), and if it does, its bound is stated **in bytes with a total ceiling** on
`crossaudit`'s model rather than as a trim — an unbounded append is what eight of the existing
fourteen already are, and adding a ninth while arguing for bounds would be the doc
contradicting itself.

**The pre-existing gap is larger than this design and is named rather than fixed here.** Eight
of the fourteen sinks in [§2.4](#24-the-sinks-and-who-owns-each) have no bound, against a
doctrine in `internal/perf/filesink.go` that says every one of them must
([§2.4](#24-the-sinks-and-who-owns-each) fact 2). The two that matter most for this design's
own failure cases: `~/.yolo-socat.log`, `O_APPEND` with its fd inherited by every forked
`socat` for the life of the jail and never trimmed — which is the log of the mechanism that
caused the 8214 collision — and `<state>/logs/<cname>-socat.log`, whose *open error is
discarded outright*, so a failure to create it is invisible in both directions.

The remedy is not per-sink judgement; it is adopting `perf.TrimRunsInFile` or a byte cap at
each one, and deleting the hand-rolled copy in `entrypoint/boot.go`. That is a mechanical
change gated on nothing here, and it does not wait on any ruling. `internal/crossaudit` is
the model to copy: a stated total ceiling (4 MiB active + one archive, *"8 MiB of crossing
log exists, ever"*) rather than a trim whose worst case nobody has written down.

## 5. Why this is compatible with the no-quiet-mode ruling

[`report-tiers.md`](../reference/report-tiers.md)'s [`OQ-RO3`](../reference/report-tiers.md#why-its-this-way) says a launch may **compress**
progress but may never **suppress** a disclosure, and `TestTheLaunchHasNoQuietFlag` fails if
a flag that could hide one appears on `runFlags`. This design is compatible in the strongest
available sense: **it is purely additive to a file, and it adds nothing to the terminal.**
No line moves off the stream, no existing line acquires a gate, and no flag is added to
`runFlags`.

The ruling and this tier are about different readers, and the distinction is already in the
tree rather than invented here: `e.note` exists precisely so a positive record can reach the
log *without* putting a line on every healthy launch, and `boot.log`'s own docstring frames
"too much on the terminal" as answered by reading the file.

Two places it *would* come into tension, stated so the next author does not walk into them:

- **If a tier line were ever promoted to the terminal under a gate**, that gate becomes a
  quiet flag by the back door — the line is now suppressible, and [`OQ-RO3`](../reference/report-tiers.md#why-its-this-way) is about
  suppressibility rather than about volume. The tier's sink is the file, full stop.
- **If the boot snapshot's own trigger were made suppressible**, a refused boot could be
  made to explain less than it does today. It is therefore never gated *down*; the only
  live question is whether it is gated *up* ([OQ-DB2](#oq-db2)).

## 6. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| **Forward `YOLO_VERBOSE` into the container** — one spelling, no new name | **Rejected, by ruling.** P5 forbids it and states the cost: two halves that deploy on different cadences, plus a prefix collision that makes one grep conflate two mechanisms. The `YOLO_PROFILE` → `YOLO_JAIL_TIMING` rename (D13) exists because that already happened once |
| **A numeric log level in the jail** (`-v`, `-vv`, `-vvv`) | **Rejected for v1**, and it is [OQ-DB1](#oq-db1)'s subject. A level is a property the emitter picks; `report-tiers.md` ruled the opposite model for the same codebase, and a level system re-decides density at every call site — which is how the current unevenness arose |
| **A structured event stream** (JSON lines, one schema) | **Rejected as premature.** Nothing consumes it, and that is measured rather than assumed: **no code in the tree reads `boot.log` back** — `startup.log` is the sink with a programmatic reader ([§2.4](#24-the-sinks-and-who-owns-each) fact 4), and conflating the two is the mistake to avoid. A schema buys parseability nobody has asked for and costs the tee, which is what makes the sink complete rather than curated |
| **Shell out to `ss`/`lsof` for the listener table** | **Rejected.** The minimal jail bakes neither and `macos-user` bakes nothing; a diagnostic that is missing exactly where the jail is most stripped is the wrong shape |
| **Keep the container alive on a refused boot and let a human `exec` in** | **Already shipped, and complementary rather than alternative** (`6c54c575`, `internal/entrypoint/hold.go`). It requires a human at a terminal at the moment of failure. The snapshot is what serves the reader who was not there, and CI, which never is |
| **More host-side verbosity, which is what was asked for** | **Rejected as the primary answer.** It would not have shortened any of [§3](#3-the-failures-are-the-specification)'s three in-scope investigations, because in all three the missing fact was inside the jail |

## 7. Risks

| Risk | Mitigation |
| :--- | :--- |
| **R1. The snapshot becomes the thing that fails the boot** — a `/proc` walk on a huge process table, a blocked read on a daemon log | Non-fatal by construction, bounded in bytes, and it runs only on a path that is already refusing. No lock, no retry, no wait |
| **R2. The tier becomes a dumping ground**, and `boot.log` stops being readable — the exact fate that made the terminal stream need [`OQ-RO3`](../reference/report-tiers.md#why-its-this-way) | The tier has an admission test, not a level: a fact is tier-worthy if a reader of a *failed* boot would want it. One generation of retention is also a self-limiting budget |
| **R3. Parts of this are being built while it is being designed.** The teardown gap and the supervisor leak are under investigation, and work on the listener inventory and on several class-(b) sites in `svcendpoint`, `wirebridged`, `loopholesruntime` and the entrypoint boot path is in flight concurrently | **The doc is written not to depend on any of it**, and this is the deliberate choice rather than an accident of timing. Every specimen cited — `waitTimeout`, the `socat` branch, the four `time.After` sites — is cited as *evidence of a shape*, never as work to schedule. If each is fixed before this is built, [§4.4](#44-a-give-up-reports-the-rule-and-its-narrow-shape)'s rule is unchanged and loses examples; the 153-site population and the four rulings are what survive. The one thing to re-check at build time is [§9](#9-success-criteria)'s first criterion, which names a live defect that may by then be closed |
| **R4. The listener inventory is the one component with no cross-backend answer** — `macos-user` has no `/proc` and no namespace | Degrades to one line naming the reason, like every other component. Worth stating rather than discovering: the backend that most needs a listener table is the one that cannot have this implementation of it |

## 8. What I would build, in order

Steps 1 and 2 are in flight as this is written ([§7](#7-risks) R3); they are listed because the
order is the argument, not because they are unclaimed.

1. **The give-up rule applied to the port-forward loop**, which is one branch and buys the
   8214 case most of its answer. It needs no dial and no ruling.
2. **The listener inventory**, as a standalone readable unit with no caller. It is the
   component with real content and it is testable against a fixture `/proc`.
3. **The boot snapshot**, wired to readiness failure and to the refusal path, capped.
4. **The tier lines** in the daemon-supervisor lifecycle, with the dead `VerboseEnv`
   conjunct removed as part of it.
5. **The dial**, last — if [OQ-DB1](#oq-db1) and [OQ-DB2](#oq-db2) leave one to build. Built
   first, it would decide the answers by default.

## 9. Success criteria

Observable outcomes a human can check, not test names:

- Reproduce the 8214 collision, launch once, and read `<ws>/.yolo/boot.log`: it names the
  process holding 8214, its argv, and the branch the port-forward loop took.
- Kill a required daemon so readiness fails, launch once: `boot.log` holds the daemon roster
  and the tail of the dead daemon's own log, without the reader knowing that log's path.
- Launch a jail with no daemons at all: the snapshot is not silent — it says the roster is
  empty.
- `git grep` finds no bounded wait in a lifecycle or teardown path whose two branches are
  indistinguishable to its caller.
- **Wedge a daemon so `supervisor.Run`'s 10 s deadline fires, and find the record** — in a
  sink the wedged daemon does not own. This is the criterion that fails today for a *structural*
  reason rather than a missing line ([§4.1.1](#411-the-sink-may-not-be-the-resource-under-diagnosis-and-it-must-outlive-the-boot)),
  so it is the one that proves [OQ-DB5](#oq-db5) was answered rather than worked around. Its
  test must assert on the sink, not on the sentence — a test that greps for wording is the dead
  end that left this gap in place.
- A launch's terminal output is byte-identical to today's for a healthy jail. This one is a
  check on [§5](#5-why-this-is-compatible-with-the-no-quiet-mode-ruling) and it is the one
  most worth automating.

## Open Questions

1. <a id="oq-db1"></a>💬 **[OQ-DB1](#oq-db1): Does the jail side get a level, or a boolean?**
   A level (`-v`/`-vv`/`-vvv`, or `YOLO_JAIL_DIAG=2`) lets a call site pick its own density;
   a boolean means one dial and the tier decides. This is the closure question for
   [§4.1](#41-the-jail-diagnostic-tier) and it is ruled ground next door —
   [`report-tiers.md`](../reference/report-tiers.md) chose "a tier is a property of the fact,
   not a level the emitter picks" for the host half, and the two halves disagreeing about
   that is a vocabulary skew a future author has to hold in their head.

   <!-- vantage: oq id=OQ-DB1 leaning="A boolean, and probably not even that — the tier's sink is a file with no volume budget, so most of what a level would gate should simply be always-on. Match report-tiers' model: the fact carries the tier, the emitter does not pick." -->

   _Leaning:_ A boolean — and possibly no dial at all. The tier writes to a file nobody is
   watching, so the volume argument that motivates levels does not apply; most of what a
   level would gate should just be always-on. Matching `report-tiers`' model keeps one
   vocabulary across the boundary.

   **Answer:**
   > _(empty — fill in when decided)_

2. <a id="oq-db2"></a>💬 **[OQ-DB2](#oq-db2): Is the boot snapshot always-on, or gated?**
   Always-on means every refused boot explains itself with no foresight required, and the
   cost is up to 64 KiB in `boot.log` on a path that is already failing. Gated means a
   second launch — and a second launch is exactly what
   [`bootlog.go`](../../internal/entrypoint/bootlog.go) rotates `boot.log.prev` to survive,
   because the natural reaction to a broken jail is to re-run it. This decides whether the
   6 s of a snapshot is spent on every refusal or on none of the first ones.

   <!-- vantage: oq id=OQ-DB2 leaning="Always-on, on the refusal path only. A refused boot has no second chance by construction, the cost lands only when something is already wrong, and OQ-RO3 governs the terminal rather than the file." -->

   _Leaning:_ Always-on, and only on the give-up paths. A refused boot is a one-shot event
   whose container is about to vanish; the cost is paid only when something is already
   wrong; and [`OQ-RO3`](../reference/report-tiers.md#why-its-this-way) constrains the terminal, not a file. A gate here is a gate on the one
   case the whole design exists for.

   **Answer:**
   > _(empty — fill in when decided)_

3. <a id="oq-db3"></a>💬 **[OQ-DB3](#oq-db3): Does a typed `--verbose` imply the jail dial?**
   If [OQ-DB1](#oq-db1) leaves a dial at all: `--verbose` today reaches the jail only as
   `YOLO_JAIL_TIMING`, under the reporting gate. Making it also imply the diagnostic dial
   gives the user one spelling; keeping them independent means `--verbose` acquires a fourth
   meaning that is not the other three, and P5's skew argument applies to the *implication*
   as much as to the variable.

   <!-- vantage: oq id=OQ-DB3 leaning="Imply it — one spelling for the user, the launcher translating to the separately-named jail variable, exactly as it already does for YOLO_JAIL_TIMING. The translation is the thing P5 protects; a second user-facing flag is not." -->

   _Leaning:_ Imply it. The launcher translates one user-facing spelling into the
   separately-named jail variable — which is precisely the shape `YOLO_JAIL_TIMING` already
   has and which P5 endorses. P5 protects against a second *variable*, not against a second
   *meaning* for one flag.

   **Answer:**
   > _(empty — fill in when decided)_

4. <a id="oq-db4"></a>💬 **[OQ-DB4](#oq-db4): Does the give-up rule get a gate, or stay a convention?**
   [§4.4](#44-a-give-up-reports-the-rule-and-its-narrow-shape)'s rule is narrow enough to
   check mechanically — a `select` over a `done` channel and `time.After` whose branches are
   indistinguishable to the caller. A convention is what produced today's unevenness (four
   loud branches and one silent one in the same `for` body), which is the argument for a
   gate. Against it: a shape-matching lint has false positives, and yolo's existing gates
   pin *named* things (`TestTheLaunchHasNoQuietFlag`, `shippedclients_test.go`) rather than
   shapes.

   <!-- vantage: oq id=OQ-DB4 leaning="A gate, but on a list rather than a pattern: enumerate the bounded waits and assert each reports, so a new one is added deliberately. Cheaper than a shape lint, and it must pin the CALL SITE — flock_test.go is the cautionary precedent, pinning the notice while nothing asserts the wiring that supplies it." -->

   _Leaning:_ A gate, but over an enumerated list rather than a syntactic pattern — name the
   bounded waits and assert each one reports. That avoids lint false positives. Whichever way
   this is ruled, the gate must pin the **call site**: `flock_test.go` is the cautionary
   precedent in this very class ([§4.4](#44-a-give-up-reports-the-rule-and-its-narrow-shape)),
   a test that names the silent-hang defect and pins the notice while nothing asserts the one
   production line that supplies it.

   **Answer:**
   > _(empty — fill in when decided)_

5. <a id="oq-db5"></a>💬 **[OQ-DB5](#oq-db5): Which sink carries a lifecycle fact, given that it may not be the resource under diagnosis?**
   `boot.log` is closed at handover and the per-daemon log may be the wedged thing, so neither
   can carry a supervisor teardown ([§4.1.1](#411-the-sink-may-not-be-the-resource-under-diagnosis-and-it-must-outlive-the-boot)).
   The candidates: give `yolo-jaild` its own always-append log beside the per-daemon ones,
   which is a fifteenth sink and needs a bound; or stop detaching the supervisor with
   `cmd.Stderr = nil` and give it a real stderr, which changes a boot-path decision made for
   other reasons. **This is the question the implementer cannot route around**, and it is the
   one place this design's *"no new sink"* cost line may have to give.

   <!-- vantage: oq id=OQ-DB5 leaning="A single supervisor-owned yolo-jaild.log, byte-capped on the crossaudit model with a stated total ceiling. It is the only candidate a wedged daemon cannot take down, it keeps the one-writer property every sink in §2.4 has, and it is assertable in a test — which is what makes a reportable timeout testable at all." -->

   _Leaning:_ One supervisor-owned `yolo-jaild.log`, byte-capped on `crossaudit`'s model with
   a stated total ceiling rather than an unbounded append. It is the only candidate no single
   wedged daemon can take down; it preserves the one-writer property every sink in
   [§2.4](#24-the-sinks-and-who-owns-each) has; and — decisively — it is a sink a test can
   assert on, which is what makes *"does the test fail if I delete the call site?"* answerable
   for a timeout at all. Repointing the supervisor's stderr is the cheaper edit and the worse
   answer: it re-decides a boot-path detachment for a diagnostic reason.

   **Answer:**
   > _(empty — fill in when decided)_
