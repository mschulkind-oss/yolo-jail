---
title: "What just happened in this jail: one record an agent can read, and the verb that reads it"
date: 2026-11-02
status: in-review
stage: DESIGN
next: "Rule OQ-JH1 (where the record lives) and OQ-JH2 (which brokered calls it carries); the thin first slice in the plan needs neither"
depends-on:
  - diagnostics-past-the-boundary.md
  - boundary-broker.md
  - live-config-apply.md
tags: [design, diagnostics, observability, approvals, broker, config, launch, skills, agent-briefings, happy-path]
summary: "An agent in a jail is asked 'what just happened?' and today answers from guesses, because the facts are spread over a dozen files on two sides of the boundary, most of them host-only free text, and the one structured record the jail can read hides a request the moment it is answered and used. The design adds one per-workspace, append-only event record every decider writes to at the moment it decides, a launch id that ties the host's and the jail's halves of one lifecycle together, a verb, yolo history, that reads the record from either side and answers the four questions the 2026-11-02 incident asked, and a built-in skill that tells the agent to read the record before saying 'I can't tell'. Nothing new reaches the terminal, no host-only store is mounted, and nothing reads the record to make a decision."
---

# What just happened in this jail: one record an agent can read, and the verb that reads it

**Status:** 2026-11-02; design, nothing built. Evidence read at `9506ea4e0`, and the incident's
facts from this workspace's own `.yolo/launch.log`, `.yolo/approvals/`, and the host logs mounted
read-only at `/ctx/host-yolo-logs` by this workspace's *local* config (`yolo-jail.local.toml`, a
`mounts` entry no other workspace has, so nothing below assumes it). No agent CLI was run.

> **In short.** Every lifecycle yolo runs already decides out loud, but each decider writes its
> sentence into its own sink, on its own side of the boundary, in its own prose. Give each decider
> one more place to write, a per-workspace event record, and one verb to read it, and "what did
> that toast approve" becomes a lookup instead of a hypothesis.

**Why it matters.** On 2026-11-02 an agent told the owner twice, wrongly, what a release dispatch
had done, then said *"I can't tell which request your toast approved."* The answer was on disk in
three places the agent could read and one it could not ([§3](#3-the-incident-as-the-specification)).
The owner's ask: *"you should be able to tell from the logs, and if you can't, fix it … not just
around approval, around other things as well. We're creating some complex life cycles."*

**The shape.** A **workspace event record** (one JSONL file per workspace, written by the host and
the jail at each decision), a **launch id** every event and every existing log line carries, the
verb **`yolo history`** that reads the record from either side and ends in a next step, and a
built-in skill, **`explaining-what-happened`**, that makes reading it the agent's first move.

**Cost.** One more file under `<workspace>/.yolo/`, one more environment pair on the container
argv, one new verb, three small schema additions to records that already exist. No terminal line
changes. No host store is mounted into a jail.

**Start at [§3](#3-the-incident-as-the-specification):** the four questions and the fact each
needed. [§5](#5-the-proposal) falls out of that table.

**Needs your ruling:** [OQ-JH1](#OQ-JH1), [OQ-JH2](#OQ-JH2), [OQ-JH3](#OQ-JH3), [OQ-JH4](#OQ-JH4).

**Reads with:** [`jail-history-plan.md`](jail-history-plan.md) (the implementation plan's seed,
not buildable while a question is open; its first slice needs no ruling),
[`diagnostics-past-the-boundary.md`](diagnostics-past-the-boundary.md) (the jail-side *failure*
tier this sits beside and does not replace, [§7](#7-position-relative-to-what-exists)),
[`boundary-broker.md`](boundary-broker.md) (the request store, `on_request`, and the ruling that
keeps the audit log host-only), [`live-config-apply.md`](live-config-apply.md) (what `yolo config
apply` compares), [`report-tiers.md`](../reference/report-tiers.md) (the no-quiet-mode ruling
this must not dent), [`happy-path-principle.md`](../reference/happy-path-principle.md) (every
stop names the next step, which is this verb's output contract).

---

## 1. Goal, and non-goals

**Goal.** From inside a jail, with no human at the host and no second launch, an agent can
answer "what just happened" for every lifecycle yolo runs across the boundary, in one command,
and the answer names what to do next.

### Non-goals

- **Not a failure diagnostic.** "Why did the boot refuse" is
  [`diagnostics-past-the-boundary.md`](diagnostics-past-the-boundary.md)'s question, answered by
  `boot.log` and a snapshot at give-up. This design records *decisions and outcomes*, not state at
  a failure. [§7](#7-position-relative-to-what-exists) draws the line.
- **Not a mount of any host-only store.** The brokers' audit log, request store and grants stay
  where [BB-D15](boundary-broker.md#BB-D15) put them. What the jail learns, it learns from a
  record the host *writes toward it*, filtered to this workspace at the writer.
- **Not a change to what a launch prints.** No line added, removed or gated on the terminal
  ([`OQ-RO3`](../reference/report-tiers.md#why-its-this-way)).
- **Not a replacement for the free-text logs.** `launch.log`, `boot.log`, the daemon logs stay the
  complete tee. The record is an index of decisions; the verb points at the log for the rest.
- **Not a trust source.** Nothing in yolo reads the record to decide anything. It is read by
  agents and humans, and it is written where a jail can also write, so it cannot be one.

## 2. Terms

- **Lifecycle.** One thing yolo takes from a start to an end across the host↔jail boundary, with
  decisions along the way: a broker request, a config change, a launch, a host-run line, a host
  daemon's life. [§4](#4-the-lifecycles-and-where-each-one-writes-today) inventories them.
- **Event** *(coined here)*. One decision or outcome in a lifecycle, as a JSON object on one line:
  when, which lifecycle, which subject, what was decided, by which side. It is not a log line (it
  is written in addition to one) and not a span (it has no duration of its own).
- **Workspace event record** *(coined here)*. The one append-only JSONL file of a workspace's
  events, written by every decider on both sides. Where it sits is [OQ-JH1](#OQ-JH1).
- **Launch id** *(coined here)*. One identifier the launcher mints before anything else can refuse,
  carried by every event, log header and container environment of that launch, so the host's and
  the jail's halves of one launch can be joined. Distinct from the broker scope's per-source
  `LaunchID` (`internal/brokerscope`, drawn once per brokered pack), which keys a scope file;
  from `YOLO_SESSION_ID`, which is per attached *session* (`internal/entrypoint/sessionhangup.go`);
  and from the container name, which is per workspace, not per launch. No per-launch id known on
  both sides exists today (checked 2026-11-02: the launch's only shared keys are the container
  name and timestamps).
- **Requested, allowed, applied, taken.** Four states of one capability, which the incident
  conflated. *Requested*: the project's `yolo-jail.toml` asks for it (`yolo request`). *Allowed*:
  the user's config permits that kind, always or on request (`yolo allow`, the **ceiling**).
  *Applied*: a human approved the project's asks at a config gate (`yolo config apply`, or the
  launch's y/N). *Taken*: the running jail's services hold it now. A relaunch moves nothing from
  *requested* to *allowed*: the ceiling is the user's file, and only `yolo allow` writes it.

## 3. The incident as the specification

Four questions the agent was asked or should have asked, the fact that answers each, and where
that fact was at the time. Verified against the files named, 2026-11-02.

| Question | The fact that answers it | Where it was | Readable in the jail? |
| :--- | :--- | :--- | :---: |
| **What did that toast approve?** | `r-66da…` filed 22:40:16, `allowed` 22:40:29, spent by the `gh workflow run` that returned rc 0 at 22:40:30 | `<ws>/.yolo/approvals/r-66da….json` (`state: allowed, used: true, decided: …`) | **yes** — but bare `yolo wait` omits a used record by design (`approvalwait.List`, `internal/approvalwait/wait.go`), so the agent never saw it |
| **How was it answered: once, for 15 m, added, remembered, or under a standing grant?** | the decision kind | the host store's `decision` audit event and grant record (`~/.local/share/yolo-jail/broker/`) | **no** — and the jail-readable copy has no field for it (`approvalwait.Record`, `internal/approvalwait/record.go`) |
| **Why did `yolo config apply` say "nothing to apply"?** | the project *requested* `dispatch_workflows`; the user's ceiling did not *allow* it on request; so the launch skipped the request, and the config the jail held already matched the file | printed by both launches: *"Your config does not allow dispatch_workflows on request, so this project runs without it … `yolo allow github on_request dispatch_workflows`"* (`internal/config/brokeredskipped.go`), teed to `<ws>/.yolo/launch.log` lines 6140 and 6306 | **yes** — free text, 6,300 lines in, never read |
| **Did the relaunch change my grants?** | no; the ceiling is user config, not touched by a launch; the same skip printed again | the two identical skip blocks in `launch.log`; `config-boot.json` lists the request both times | **yes** — by comparing two prose blocks by hand |
| **What happened to the first request, `r-bf10…`?** | filed 22:37:12; the jail ended at 22:38:12 and its broker stopped, so the store closed it `refused`; the in-jail `yolo wait r-bf10…` was orphaned (its lock file is still in `.yolo/approval-waiters/`) | the approvals record (`state: refused`), the broker log *"signal received, shutting down"* | **partly** — the record says refused, nothing says *why* |

**Two findings shape the whole design:**

1. **Most of the facts were already jail-readable.** The gap was not a wall; it was that no verb
   reads these files together, the one that reads one of them filters the answer out, and the
   facts that *were* host-only (how a request was answered) have a jail-readable twin that simply
   lacks the field.
2. **Every fact was produced by a decider that said it out loud somewhere.** The skip was printed
   by `brokeredskipped.go`; the decision was audited by `brokerstore.Decide`; the shutdown was
   logged by the broker. None of them wrote to a place a program reads. The fix is one more write
   at each of those sites, not a new way of finding things out.

## 4. The lifecycles, and where each one writes today

Verified 2026-11-02 at `9506ea4e0`; the "jail" column is what a process in the container can
read with no extra mount. `<ws>` is the workspace; `<state>` is
`~/.local/share/yolo-jail` on the host.

| Lifecycle | Decisions along it | Where they are written today | Jail | Structured |
| :--- | :--- | :--- | :---: | :---: |
| **Broker request** (`gh` write, `git push`) | filed; `on_request` hook ran/failed; answered once/`--for`/`--add`/`--remember`/deny; spent; lapsed; expired; refused when the broker stopped; covered by a grant or the working set with no request | host store `<state>/broker/<source>/{requests,grants,working}/*.json`; audit `<state>/broker/audit.jsonl` (`request`, `decision`, `grant-used`, `call`, `covered-authorized` events); jail twin `<ws>/.yolo/approvals/r-*.json` (state, command, filed, expires, decided, used, lapsed) | twin only | yes |
| **Brokered call** (the transport) | accepted, refused, bytes, elapsed | `<state>/logs/crossings.log` (one line per connection, no request id); `<state>/logs/host-service-<name>.log` (`jail=… keys=… rc=… elapsed_ms=…` is the transport's access line, `internal/frameproto`, `internal/hostservice`; it names the frame's *keys*, never the command or the request) | no | no |
| **Config: requested → allowed → applied → taken** | `yolo request` writes the project file; `yolo allow` writes the user file (host only); a launch or `yolo config apply` runs the gate and applies what can go live; a request outside the ceiling is skipped with a warning (`yolo check`, the launch's gate block, `yolo request` itself) | the two config files; the gate's y/N on the terminal, its approved parts in the host's `<state>/approvals/<cname>.json`; the skip warning on the terminal (teed to `launch.log`); `<ws>/.yolo/config-boot.json`, the jail's **effective** workspace config, folded per applied key by `yolo config apply` ([LA-D8](live-config-apply.md#LA-D8)) and overwritten in place with no time and no history; `<ws>/.yolo/last-launch-config.json`, and `launch.log`'s *"config: effective config changed since the previous launch"* diff, which is launch-to-launch only | the files and the tee | config-boot only; **when** it changed mid-session is recorded nowhere |
| **Launch, attach, relaunch** | invocation, version, source skew, image build or skip, pack-tree staging or the live tree, loophole startup, readiness, rc | `<ws>/.yolo/launch.log` (everything printed, under `=== yolo launch <time> ===` … `=== launch done, rc=N ===`); `<ws>/.yolo/boot.log` and `boot.session.log` (the entrypoint, with a `bootLogFacts` header); `<state>/logs/launches.log` (one machine-wide line per launch, keyed by the jail's short hash, `internal/cli/run/launchrecord.go`); `<ws>/.yolo/host-perf.log` | the workspace ones | no |
| **Jail end** | the keeper's "last session left"; a signal | `launch.log` (*"keeper: the last session of … left; ending the jail"*), the broker log's *"signal received, shutting down"* | tee only | no |
| **Host-run line** | requested, started, result | `<ws>/.yolo/host-run/<id>/{request,result}.json` + `stdout`, `stderr` (`internal/hostrun`) | yes | yes |
| **In-jail waiter** | `yolo wait r-<id>` started, woke, timed out, orphaned | `<ws>/.yolo/approval-waiters/<id>.lock` (presence only) | yes | no |
| **Host daemons, loopholes** | started, reused, stopped, refused | `<state>/logs/host-service-<name>.log`, the launch stream | no | no |
| **Auth brokers** (claude, openai refresh) | refreshed, failed, serialized | their host-service logs | no | no |
| **Nix roots, housekeeping** | kept, released, reaped | `<ws>/.yolo/housekeeping.log`, the in-jail `nix-roots.log` | yes | no |

Three sinks already follow the pattern this design generalizes, and they are the precedent for
every rule in [§5.1](#51-the-workspace-event-record): the approvals twin, the host-run mailbox and
`launch.log` are **host-written files under the workspace's `.yolo`**, which the jail mounts and
can also write, and which **nothing reads back to make a decision** (`approvalwait`'s header:
*"The record is a wake-up hint and nothing more … a forged 'allowed' record only makes the agent
retry, and the broker still decides the retry from its own store"*; `launch.log`'s: *"Nothing
reads this file back to make a decision, which is what keeps that a disclosure rather than a
trust hole"*).

## 5. The proposal

### 5.1 The workspace event record

One file per workspace, `events.jsonl`, under the workspace's `.yolo` directory if
[OQ-JH1](#OQ-JH1) is ruled as I lean. **Every decider appends one event at the moment it decides**,
beside the sentence it already prints or logs ([JM-P2](../reference/jail-mechanics-skill.md#JM-P2),
*the decider writes the sentence*, applied to a record). No collector, no daemon, no second pass
over logs.

**One event is one line:**

```json
{"t":"2026-11-03T03:40:29.313Z","launch":"L-20261103T033819Z-ae3a2fd4","by":"host",
 "kind":"request.answered","id":"r-66da45603d63de9d13bfdb1d4427d0b3","source":"github",
 "answer":"once","via":"on_request","command":"workflow run --repo=mschulkind-oss/yolo-jail --ref=main '--raw-field=[redacted]' '--raw-field=[redacted]' release-request.yml",
 "next":"run the refused command again, once"}
```

| Field | Always | Meaning |
| :--- | :---: | :--- |
| `t` | yes | RFC 3339 UTC with milliseconds, the writer's clock (host and jail share a kernel clock on every backend yolo ships) |
| `launch` | yes | the launch id ([§5.2](#52-the-launch-id)); `-` for an event no launch owns (a `yolo approve` run on the host between launches) |
| `by` | yes | `host` or `jail`: which side's process wrote it |
| `kind` | yes | `<lifecycle>.<decision>`, from the catalogue in [§5.5](#55-the-event-catalogue) |
| `id` | when the lifecycle has one | request id, host-run id, launch id, daemon name |
| `source` | broker events | `github`, `git` |
| `command` | request and call events | the display form the approvals twin already uses (`summaryArgv`: values of `--body`, `-f`, `-H` and the like shown as `[redacted]`); **never the audit log's full argv** |
| `answer`, `via`, `authority`, `rc`, `reason`, `values`, … | per kind | the decision's own fields ([§5.5](#55-the-event-catalogue)) |
| `next` | when a human or agent has a next step | one sentence or one command, the same text the decider prints ([happy path](../reference/happy-path-principle.md)) |
| `truncated` | when set | the line exceeded the cap and its string fields were cut |

**Rules, each with its reason:**

1. **Append-only, one writer per event, many writers per file.** The launcher, each broker
   daemon, `yolo approve`, `yolo config apply`, `yolo host-run`, the entrypoint and in-jail
   verbs all append. Writes go through the trim-and-append lock `receipts.jsonl` already uses
   (`entrypoint.AppendReceiptLine`'s shape: confined open beneath `.yolo`, never following a
   link, one lock), so two deciders at once interleave whole lines, never bytes.
2. **Bounded in bytes, not events:** 4 MiB active plus one archive, `logcap`'s model
   ([`diagnostics-past-the-boundary.md` §2.4](diagnostics-past-the-boundary.md#24-the-sinks-and-who-owns-each)),
   trimmed at append. A line over 8 KiB has its string fields cut and `truncated: true` set,
   because a record that silently loses a line is this design's own failure mode.
3. **Never fatal, never waited on.** A failed append costs the event and nothing else; the
   decider's own output is unchanged. The same ruling as `attachBootLog` and `perf`'s P2.
4. **Nothing reads it to decide.** Not the broker, not the gate, not the launcher. `yolo history`
   reads it to *report*. This is what lets the jail write it ([§5.6](#56-the-trust-boundary)).
5. **Per workspace by construction.** The file is under one workspace's state; a host process
   writing an event knows the workspace (the launcher, the broker's scope file, `yolo config
   apply`'s cwd, `yolo approve`'s request record all carry it). No other workspace's event can
   land in it, so no filter is needed at read time and none can be forgotten.
6. **No secret, by the same rule the approvals twin keeps.** Commands are the redacted display
   form; stdin is never recorded; no token, no path to a credential, no hook command line
   (the user's `on_request` command is host configuration; the event says `via: on_request` and
   nothing more).

**Degenerate cases, stated.** No file (a workspace never launched since this shipped): `yolo
history` says so and names `launch.log` and `boot.log` as what exists instead. Empty since the
launch: one line, *"no events since launch L-… at <time>"*. An unreadable line: skipped, counted,
and the count is reported at the end (*"2 lines unreadable"*), never silently dropped. Two
events with the same `t`: file order. A clock step between host and jail: not possible on podman
or macos-user (one kernel); on Apple Container the VM clock is the host's, and the verb sorts by
`t` then file order either way, so a skew shows as odd ordering rather than a missing event.

### 5.2 The launch id

**Minted once per launch, by the launcher, before any refusal can happen** — at the point
`armLaunchRecord` already sits, above the live-overlay and workspace-scope guards
(`internal/cli/run/run.go`). Form: `L-<UTC start, compact>-<jail short hash>`, so it sorts by
time and reads as a launch of this jail; the hash is the one `launches.log` and `crossings.log`
already use. **An attach mints its own id** and its `launch.attached` event names the running
launch's id, kept beside the host-side facts an attach already reads about the running jail (the
live pack tree, `pack-trees/.live`), so a session that joined a running jail and the jail's own
launch are two events that point at each other.

Where it goes, each already a place the launch writes something:

| Where | How |
| :--- | :--- |
| every event | the `launch` field |
| `launch.log` | on the `=== yolo launch <time> ===` header line |
| `launches.log` | one more `launch=` key on the existing line |
| `boot.log` | one more entry in `bootLogFacts`, which is already *"the list of launch-shaping decisions recorded inside the jail"* |
| the container environment | `YOLO_LAUNCH_ID`, an explicit `-e` pair on the argv like `YOLO_HOST_LOOPBACK`, so in-jail writers (the entrypoint, `yolo wait`, `yolo check`) stamp their events with it. It is a fact, not a dial, so P5's skew argument against forwarding a dial does not apply; it moves in one commit with its readers |
| the broker scope file | one more field beside the per-source `LaunchID`, so a broker's events carry the launch they serve |

### 5.3 `yolo history`

**A new verb, not an extension of `yolo wait`**, because `yolo wait`'s bare form answers a
different question ("what is waiting on *me*") and must keep answering it crisply; `yolo wait`
gains one footer pointing here ([§5.4](#54-the-concrete-gaps-fixed)). `yolo check` validates
config; `yolo audit` is the host's view of every workspace and stays host-only. Reading the
record is its own job.

**Surface, kept small:**

```
yolo history [--since DURATION|TIME] [--json] [SUBJECT]

  (none)        this launch's events, oldest first, then a next-step line
  requests      broker requests of this workspace: filed, answered how, spent, refused
  config        requested vs allowed vs applied vs taken, as this launch saw it
  launches      the launches and attaches of this workspace, with rc and why each ended
  r-<id>        one request, end to end, with the call that spent it
  <label>       one host-run line
```

**Default window: this launch.** "What just happened in this jail" means since the newest
`launch.started` event of this workspace; `--since 2h` widens it, `--since` with a time pins it.
An agent after a relaunch sees the relaunch first.

**Output contract, in the agent's terms** ([happy path](../reference/happy-path-principle.md)):
one line per event — time, side, what happened in plain words, the id — and **a last line that
names the next step**: for a pending request, the `yolo wait`; for a skipped config request, the
`yolo allow … on_request …` the warning already prints; for an allowed-and-spent request,
*"nothing to do: it ran"*. Where the record points at a free-text log for more, it names the file
and the line range (`launch.log:6301-6320`), so the agent reads twenty lines instead of six
thousand. `--json` prints the stored events, one per line, for a program.

The incident, as the verb would have shown it to the agent after the relaunch:

```
yolo history — /workspace, launch L-20261103T033819Z-ae3a2fd4 (started 22:38:19 -0500,
  invocation: yolo -- claude --continue; the previous launch ended 22:38:12, rc 0,
  keeper: the last session left)
22:37:12 jail  gh workflow run release-request.yml → needs approval r-bf10…  [previous launch]
22:37:12 host  r-bf10… filed; on_request hook ran
22:37:49 jail  yolo wait r-bf10… started
22:38:12 host  broker stopped with the jail: r-bf10… refused unanswered; its waiter was orphaned
22:38:19 host  launch started
22:38:19 host  ⚠ config: this project requests github dispatch_workflows (release-request.yml, …);
               your config does not allow it on request, so this launch runs without it
22:38:48 jail  boot ready
22:40:16 jail  gh workflow run release-request.yml → needs approval r-66da…
22:40:16 host  r-66da… filed; on_request hook ran
22:40:29 host  r-66da… allowed once (yolo approve, via on_request)
22:40:30 host  gh workflow run release-request.yml ran, rc 0, under approval r-66da… (14.2 s)

Next: r-66da… is spent and nothing waits on you. The ceiling still skips dispatch_workflows, so
the next dispatch asks again; to stop that, the human runs on the host:
    yolo allow github on_request dispatch_workflows
    yolo config apply
```

**On the host** the same verb runs in a workspace (`-C` as `yolo wait` has it) and reads the
same record — with one difference, [§5.6](#56-the-trust-boundary): request facts are read from
the host store, which is authoritative there, and the record fills in the jail-side events.

### 5.4 The concrete gaps, fixed

Each is a defect the incident exposed, independent of the record, and each is one decider
saying one more thing where it already speaks.

| Gap | Fix | Where |
| :--- | :--- | :--- |
| **Bare `yolo wait` omitted the answered-and-spent request.** Its filter is right (a spent Allow once no longer waits on the agent), but its silence about what it omitted is not | Keep the filter. End the listing with one line: *"N request(s) of this workspace were answered and acted on in the last day; `yolo history requests` lists them."* Zero omitted: no line | `approvalwait.List` |
| **A `--for` or `--add` answer stays listed as `allowed` for 24 h**, because only a digest grant is ever marked spent (`brokerstore.spendIn`), so the agent is told to "run its command again" after it already ran | The approvals twin carries `answer`; the listing shows `allowed (for 15m, until <time>)` or `allowed (added to the working set)` and tells the agent nothing waits on it | `approvalwait.Record`, `approvalwait.List` |
| **The jail-readable record cannot say how a request was answered.** The host store knows (`Decide` vs `DecideWorking`, `--remember`'s config write, a standing grant, the working set) | Add to the twin: `answer` (`once`, `for`, `add`, `remember`, `deny`), `via` (`on_request` when `yolo approve` runs under the hook's environment, `host` otherwise), `jail` and `launch` (which jail filed it), `hook` (`ran`, `failed`, `none`), `spent_at`. Written by the same `publish` that writes the twin today | `brokerstore.publish`, `approvalwait.Record` |
| **A request closed because its broker stopped reads `refused` with no reason.** The store's `End` knows why (`brokercore.Lifetimes.EndAll`) | The twin gains `reason: broker-stopped`; the record gains the `request.refused` event with the same reason | `brokerstore.Store.End` |
| **The github-broker has no per-call line of its own.** The `jail=… keys=argv rc=… elapsed_ms=…` line is the transport's (`internal/hostservice`), and `keys` is the frame's key list. The git-broker does write one per call (`git-broker: request <n>: …`) | The github-broker writes one line per call to its own log, the git-broker's shape, carrying the request id when one was filed or spent, the authority (`approval r-…`, `grant g-…`, `working-set`, `standing`, `read`), the set and the rc. The transport line is unchanged | `ghbroker.ServeContext` |
| **The `on_request` hook's success is recorded nowhere**; only its failure is (`NoteHookFailure`) | A `request.hook` event, `outcome: ran` or `failed`, from the runner; the twin's `hook` field | `brokerhook.Runner` |
| **The skipped-request warning is print-only** | A `config.request-skipped` event per skipped request, with source, capability, values and the `yolo allow` command, from the same function that prints it | `config/brokeredskipped.go`'s callers: the launch gate, `yolo config apply`, `yolo request` |
| **A mid-session `yolo config apply` leaves no dated trace in the jail.** `config-boot.json` is folded in place; the result block goes to the host's stdout; the next launch's "changed since the previous launch" diff is launch-to-launch | `config.applied`, `config.nothing-to-apply` and `config.gate` events from the apply's result, so "did the relaunch change my grants" and "when did my mounts change" are answered from the record; `config-boot.json` is unchanged | `run.ConfigApply` (`liveapply.go`) |
| **An orphaned `yolo wait` leaves only a lock file** | `wait.started`, `wait.woke` (with the exit code), `wait.timeout` events from the waiter; a lock with no `woke` after its launch ended reads as orphaned | `approvalwait.Wait` |

### 5.5 The event catalogue

The kinds, by lifecycle, with the decider that writes each. A kind not here is not an event; a
new one is added here first, with its fields.

| Lifecycle | Kinds | Fields beyond the common ones | Writer |
| :--- | :--- | :--- | :--- |
| launch | `launch.started`, `launch.attached`, `launch.refused`, `launch.done`, `jail.ended` | `invocation`, `version`, `backend`, `rc`, `reason` (skew, build failed, scope breach, …), `joined` (the running launch, for an attach), `ended_by` (keeper, signal) | the launcher (`run.Run`, the keeper) |
| boot | `boot.started`, `boot.ready`, `boot.refused`, `service.ready`, `service.unreachable` | `service`, `reason` | the entrypoint |
| config | `config.gate` (`answer: y|n|no-terminal`, `changes`), `config.applied` (`live`, `pending-restart` lists), `config.nothing-to-apply`, `config.request-skipped` (`source`, `capability`, `values`, `next`), `config.requested` (`yolo request`, the key written), `config.allowed` (`yolo allow`, the kind) | as named | the gate (launch and `yolo config apply`), `yolo request`, `yolo allow` |
| request | `request.filed`, `request.hook`, `request.answered` (`answer`, `via`), `request.spent`, `request.lapsed`, `request.expired`, `request.refused` (`reason`) | `id`, `source`, `command`, `answer`, `via`, `authority` | the broker store (`fileIn`, `Decide`, `DecideWorking`, `spendIn`, `End`), the hook runner |
| call | `call.ran`, `call.refused` (`reason`: needs-approval, out-of-scope, no-read-grant, refused-kind), `call.covered` (`authority`) | `source`, `command`, `rc`, `elapsed_ms`, `authority` | the broker daemons; **which calls qualify is [OQ-JH2](#OQ-JH2)** |
| host-run | `hostrun.requested`, `hostrun.started`, `hostrun.done` (`exit`), `hostrun.refused` | `id`, `label` | `yolo host-run` (host), the in-jail requester |
| wait | `wait.started`, `wait.woke` (`exit`), `wait.timeout` | `id` or `label` | `yolo wait` (jail) |
| daemon | `daemon.started`, `daemon.reused`, `daemon.stopped`, `daemon.failed` | `name`, `pid`, `reason` | the loophole runtime (host), the supervisor (jail) |
| auth | `auth.refreshed`, `auth.refresh-failed` | `provider`, `reason` (never a token, never an account) | the auth brokers |

What is **not** an event: a timing span (`host-perf.log` owns those), a tier line in `boot.log`
(the diagnostic tier owns those), a brokered *read* unless [OQ-JH2](#OQ-JH2) says otherwise, and
anything whose only reader is a human at the terminal at that moment.

### 5.6 The trust boundary

The record is written where the jail can write it (under [OQ-JH1](#OQ-JH1)'s leaning), so it
holds against a misguided agent and not an adversary, which is the threat model yolo defends
against ([`misguided-agent-principle.md`](../reference/misguided-agent-principle.md)). Three
consequences, each already true of the approvals twin and the host-run mailbox:

- **A forged event changes nothing.** Nothing decides on the record. An agent that writes itself
  an `allowed` line only runs a command the broker then judges from its own store.
- **The host does not read a jail-writable file as fact about the host.** `yolo history` on the
  host takes request and call facts from the host store and audit log, which are authoritative
  there, and reads the record for the jail-side events, labeling each `jail`. A record line that
  contradicts the store is shown with the store's version and marked.
- **The host writes toward the jail under the same discipline as the twin:** beneath a root that
  refuses a link at `.yolo` or at the file, through an exclusive, no-follow create, and it never
  reads anything back.

**What crosses the boundary that did not before**, by this design: how a request was answered
(`once`/`for`/`add`/`remember`/`deny`), whether the hook ran, the fact that a brokered write ran
and under which authority, the launch id, and the skipped-request facts the launch already prints
into the jail-readable `launch.log`. What does not cross: any other workspace's anything (per
workspace by construction), the audit log's full argv, grants' ids beyond the one that answered
this request, the user's hook command, the user's ceiling beyond the capability a skip names
([OQ-JH3](#OQ-JH3)).

**A per-workspace view of the audit log is safe only as a new record written by the broker**,
never as a filtered copy of `audit.jsonl`'s lines: the audit line stores argv in full
(`brokeraudit.Event.Argv`), and a copy would carry a `--body` an agent passed with a secret in it.
The broker writes the event from the same redacted display form it gives the approvals twin.

## 6. The skill: read the record before you say "I can't tell"

The incident's agent had `diagnosing-the-jail` and the briefing and still guessed. Neither tells
an agent what to do when the *user* asks what happened; both are about a broken environment or
a pending request. **The lesson to teach is a habit, and it needs its own trigger**, which is
what a skill description is for.

**Shape (my leaning on [OQ-JH4](#OQ-JH4)):** a third built-in skill,
`explaining-what-happened`, rendered from the same fact sheet as the other two
(`internal/jailcontent/mechanicsrender.go`, so paths are this jail's), plus one sentence in the
briefing's *Running a command on the host* section and a cross-link from `diagnosing-the-jail`
§6 (*"you're running stale code"*) and from the `jail-mechanics` index.

**Trigger description** (the one line an agent matches on):

> Explain what happened in this environment: what a request was answered, why a config change
> did or did not take, what a relaunch changed, what refused a push or a gh call, what a host
> command returned. Use before answering any "what happened" or "did that work" question, and
> before saying you cannot tell.

**Outline:**

1. **Read first, then speak.** `yolo history` (this launch), `yolo history requests`, `yolo
   history r-<id>`. Never tell the user "I can't tell" or name a cause before running it. If it
   says the record does not exist, read `launch.log` from its last `=== yolo launch` header and
   `boot.log`, and say what you read.
2. **Requested is not allowed is not applied.** The four states in plain words, and the one
   that each command moves: `yolo request` (the project asks), `yolo allow` (the human's
   ceiling, host only), `yolo config apply` (approve the asks). *"Nothing to apply"* with a
   `request-skipped` event means the ceiling, not the ask, is missing; the next step is the
   `yolo allow` line the event names.
3. **A relaunch changes nothing in the ceiling.** It re-reads the same two files. If the history
   shows the same skip in two launches, the relaunch was not the fix.
4. **A request that is `allowed` and spent is done.** Do not retry it and do not wait on it;
   `yolo history r-<id>` shows the call that spent it. `for` and `add` answers do not wait on
   you either.
5. **A request `refused` with `broker-stopped`** died with the jail that filed it. The command
   has to be run again; a new request will be filed.
6. **A waiter you started before a relaunch is gone.** Start it again only if the request is
   still pending.
7. **Where the free text is** when the record points there: `launch.log`, `boot.log`, the
   host-run mailbox, and that `yolo audit` is the host's and refuses here on purpose.

The three rules the owner named are items 1, 2 and 3. Items 4–6 are the incident's remaining
wrong turns, named so they are not re-derived.

## 7. Position relative to what exists

[`diagnostics-past-the-boundary.md`](diagnostics-past-the-boundary.md) and this design share a
sink table and nothing else, and the line between them is worth drawing once:

| | That design | This design |
| :--- | :--- | :--- |
| Question | why did the jail *fail* | what *happened*, failed or not |
| Fact | state at a give-up (listeners, a daemon roster) | a decision and its outcome |
| Reader | a human reading a file after a refused boot | a program, `yolo history`, read by an agent mid-session |
| Sink | `boot.log` (one generation) and a service-independent lifecycle sink ([OQ-DB5](diagnostics-past-the-boundary.md#oq-db5)) | the workspace event record |
| Boundary | one dial crosses, deliberately | one *fact* crosses, the launch id |

That design rejected *"a structured event stream (JSON lines, one schema)"* as premature because
*"nothing consumes it"*. The verdict stands for `boot.log`: this design does not touch the tee,
and the record is not a replacement for any log. What changed is that there is now a consumer,
with a measured need, and the record carries decisions rather than the stream. Its
[OQ-DB5](diagnostics-past-the-boundary.md#oq-db5) asks for a lifecycle sink no single daemon can
take down; the event record is such a sink for the *decision* a supervisor makes when it gives
up (`daemon.failed`), and it may carry that one line, but it does not carry the tier's volume
and does not answer that question for it.

[`boundary-broker.md`](boundary-broker.md) rules the audit log host-only ([BB-D15](boundary-broker.md#BB-D15))
and this design keeps it so; what the jail gets is the subset the broker already tells it in a
refusal or a result, written down. [`herdr-approvals.md`](herdr-approvals.md) and
[`agent-event-watchers.md`](agent-event-watchers.md) are about *delivering* an answer to the
agent at the moment it arrives; this is about *reading* it afterwards, and the two meet at the
request id. [`live-config-apply.md`](live-config-apply.md) defines what *applied* and *taken*
mean; this records the result of each run of its gate.

[`report-tiers.md`](../reference/report-tiers.md)'s [`OQ-RO3`](../reference/report-tiers.md#why-its-this-way):
nothing here adds, removes or gates a launch line. The one terminal change, `yolo wait`'s footer,
is a verb's own output, not the launch stream.

## 8. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| **Parse the existing logs** — teach `yolo history` to read `launch.log`, the broker logs and `crossings.log` | **Rejected as the mechanism, kept as the fallback.** The free text is a tee of human sentences that change with every wording fix; a parser over it is a test that asserts sentences. The host logs are not mounted by default and the per-call line has no request id. The verb reads them only to point at a line range |
| **Mount the host's `logs/` or `broker/` read-only into every jail** | **Rejected.** `logs/` is machine-wide (every workspace's crossings and daemons), and `broker/audit.jsonl` carries full argv; BB-D15 exists for this. A per-workspace projection written by the broker gives the jail its own facts and nothing else |
| **Extend `yolo wait` bare to list everything** | **Rejected.** Its answer to "what waits on me" must stay short enough to act on; the footer points at the history instead |
| **Extend `yolo check`** | **Rejected.** `yolo check` is a preflight over config and the entrypoint; mixing in a timeline muddles both outputs |
| **A per-workspace `audit.jsonl` copy under `.yolo`, filtered by workspace** | **Rejected** for the full-argv reason above; **accepted** in spirit as the `call.*` events, redacted at the writer, under [OQ-JH2](#OQ-JH2) |
| **Reuse `receipts.jsonl`** as the record | **Rejected.** Receipts are install and capture facts with their own reader; one file for two readers means each reader filters the other out. The *writer* is reused |
| **A host daemon that collects events** | **Rejected.** Every decider already runs; a collector adds a lifecycle to the list it is meant to explain |
| **Make the launch id the broker scope's `LaunchID`** | **Rejected.** That id is drawn once per brokered pack and keys a scope file; a launch with two brokered sources has two. The launch id is minted once, above everything, and the scope file carries it too |

## 9. Risks

| Risk | Mitigation |
| :--- | :--- |
| **R1. The record becomes a dumping ground** and `yolo history` scrolls like `launch.log` | Admission test: an event is a *decision or outcome* with a subject and, usually, a next step. No spans, no progress, no reads unless ruled. The catalogue in [§5.5](#55-the-event-catalogue) is closed until amended |
| **R2. Two writer sides disagree** (a jail event says one thing, the host store another) | The host's view takes the store; the jail's view has no store and says so when a record line is its only source. Nothing decides on either |
| **R3. The record is forged to mislead the human on the host** | The host's `yolo history` never shows a `jail`-labeled line as a host fact, and request facts come from the store. This is the misguided-agent bar, not an adversary bar, by principle |
| **R4. The launch id is a second spelling of something** | It is new: no existing id is per launch and known on both sides. The scope's `LaunchID` and the container name are named in [§2](#2-terms) so nobody reaches for them |
| **R5. A decider that cannot reach the workspace** (`yolo approve` run on the host after the workspace moved) | `launch: -` and the record under whatever workspace the request names; a request with no workspace writes no event, and the store still has it |
| **R6. macos-user has no container env** | The launch id reaches the guest the way `YOLO_HOST_LOOPBACK` already does there (`macosuser.SandboxPath`'s environment), and the record is under the shared workspace, which both sides already write |

## 10. What I would build, in order

1. **The approvals twin's new fields, the `yolo wait` footer, and `yolo history requests` over
   the twin alone.** No record, no launch id, no ruling: it answers "what did that toast approve"
   and "how" from a file the jail already reads, and fixes the `--for`/`--add` listing defect.
   This is the thin first slice the plan names.
2. **The launch id**, in the launcher, `launch.log`, `launches.log`, `boot.log` and the
   container env. Independent of the record; it makes the two halves joinable by hand before
   the record exists.
3. **The record and its writer**, with the launch, config and request kinds, and `yolo history`
   over it with the default window. After [OQ-JH1](#OQ-JH1).
4. **The github-broker's per-call line and the `call.*` events.** After [OQ-JH2](#OQ-JH2).
5. **The skill and the briefing sentence.** After [OQ-JH4](#OQ-JH4); its content does not wait
   on the record (item 1 already gives it a verb to point at).
6. The remaining lifecycles (host-run, wait, daemon, auth) as their deciders are touched.

## 11. Success criteria

Observable outcomes a human can check:

- Reproduce the incident (a dispatch outside the ceiling, a relaunch, a second dispatch approved
  from the toast). In the jail, `yolo history` prints both skips, the refused first request with
  `broker-stopped`, the second request `allowed once via on_request`, and the call that spent it
  — and ends with the `yolo allow` line. No free-text log is opened.
- `yolo history r-<id>` for a request answered `--for 15m` says it does not wait on the agent;
  bare `yolo wait` no longer lists it as something to act on.
- After a relaunch, `yolo history launches` shows two launches with ids, and `boot.log`'s header
  and `launch.log`'s header carry the same id as the events.
- On the host, in the same workspace, `yolo history` shows the same timeline with request facts
  from the store, and a forged `request.answered` line written from the jail appears marked, not
  as fact.
- `yolo history` in a workspace launched by an older yolo says the record does not exist and
  names `launch.log` and `boot.log`.
- A launch's terminal output is byte-identical to today's for a healthy jail.
- The skill's description matches "what happened", "did that work", "what did I approve"; an
  agent following it runs `yolo history` before answering. (Checked by reading a transcript, not
  by running an agent.)

## Open Questions

1. 💬 **<a id="OQ-JH1"></a>[OQ-JH1](#OQ-JH1): Should the event record live under the workspace's `.yolo`, where the jail can also write it, or in yolo's host state, mounted read-only into the jail?**

   Matt's agent runs `yolo history` after a relaunch to find out what the toast approved. The
   approvals records and the host-run mailbox it would read beside the new record already sit
   under `/workspace/.yolo`, written by the host, readable and writable by the jail, and nothing
   in yolo trusts them. The event record can follow them, or it can be the first per-workspace
   file the host keeps in its own state directory and mounts in.

   - **A — Under the workspace's `.yolo`, like the approvals twin.** The jail can read it on
     every backend with no new mount, including macos-user, which has no mounts at all; the jail
     can also write its own events into it. An agent could forge a line, and the only effect is
     that a human reading history on the host sees it marked as the jail's.
   - **B — In host state, mounted read-only at a fixed in-jail path.** The jail cannot forge a
     line. The cost: one more mount per jail, a different path per backend, no mount on
     macos-user so it needs a second mechanism there, and the jail's own events (its boot, its
     waiters) need a second file, so `yolo history` merges two.

   <!-- vantage: question id=OQ-JH1 leaning="A: the approvals twin and the host-run mailbox already made this choice and hold under the misguided-agent bar; B costs a mount per backend and a second file for the jail's own events, to defend against an adversary yolo does not defend against." -->

   _Leaning:_ **A.** The approvals twin and the host-run mailbox already made this choice, under
   the same principle, and nothing decides on the record. B pays a mount on every backend and a
   second file for the jail's own events to defend against the adversary yolo does not defend
   against.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **<a id="OQ-JH2"></a>[OQ-JH2](#OQ-JH2): Should every brokered call leave an event in the workspace, or only the writes, refusals and approvals?**

   Something in Matt's jail runs a `gh` read every thirty seconds (the broker log shows one
   `rc=0` line per half minute, all day). The audit log on the host records each as a `call`
   event, and `yolo audit` is how he reads them there. In the jail, an agent asking "what refused
   my push" needs the refusals and the writes; it never asked about the reads.

   - **A — Writes, refusals and approvals only.** `yolo history` in the jail shows the calls
     that changed something or were stopped; the reads stay in the host's audit log. A day's
     record is a few dozen lines.
   - **B — Every call, reads included.** The jail sees its own full call list, redacted. A day's
     record is thousands of lines, the 4 MiB cap turns over in days, and `yolo history`'s default
     view needs a filter to be readable.
   - **C — Every call, but reads collapsed into one count per launch** (*"412 reads ran"*).
     Keeps the cap, loses the per-read time.

   <!-- vantage: question id=OQ-JH2 leaning="A: the questions an agent asks are about what changed or was stopped; the reads have a host reader already, and recording them would make the cap, not the launch, the record's horizon." -->

   _Leaning:_ **A.** The agent's questions are about what changed or was stopped; the reads
   already have a reader on the host, and at this volume they would make the byte cap, not the
   launch, the record's horizon.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **<a id="OQ-JH3"></a>[OQ-JH3](#OQ-JH3): When a project's request is skipped, should the record say only that the capability is not allowed on request, or also what the user's ceiling does allow?**

   Matt's `yolo-jail.toml` requests `dispatch_workflows`; his user config allows `read` always
   and nothing on request. Today the launch prints *"Your config does not allow
   dispatch_workflows on request, so this project runs without it"* and the `yolo allow` line.
   The jail never sees the user's `allow` table: it is deliberately not inherited into the jail's
   config. The record can carry the skip as printed, or the ceiling's relevant kinds beside it.

   - **A — The skip as printed.** The event names the source, the capability, the values and
     the `yolo allow … on_request …` command. The agent can say why "nothing to apply" and what
     to run. It cannot say what else the project could ask for.
   - **B — The skip plus the ceiling for that source.** The event adds `allowed_always` and
     `allowed_on_request` lists. The agent can also say "you could request X instead". The jail
     learns the user's whole policy for that source, which the inheritance rule keeps out today.

   <!-- vantage: question id=OQ-JH3 leaning="A: it answers the incident's question with what the launch already discloses into launch.log; B crosses a user-config fact the inheritance rule withholds on purpose, for a question nobody asked." -->

   _Leaning:_ **A.** It answers the incident's question with facts the launch already discloses
   into the jail-readable `launch.log`; B crosses a user-config fact the inheritance rule
   withholds on purpose, for a question no agent has asked.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **<a id="OQ-JH4"></a>[OQ-JH4](#OQ-JH4): Does the agent learn to read the record from a new built-in skill, from a longer `diagnosing-the-jail`, or from the briefing alone?**

   Matt asks his agent "what did that toast approve?" The agent has the briefing (how to hand
   the human a command and wait), `diagnosing-the-jail` (a broken environment: provisioning,
   blocked tools, ports, stale code) and `jail-mechanics` (how to change things). None has a
   trigger for "what happened", so the agent answered from memory of what it ran.

   - **A — A third built-in skill, `explaining-what-happened`**, with the trigger and outline in
     the body, plus one briefing sentence and two cross-links. An agent matching "what happened"
     loads a page that says run `yolo history` first. Every jail ships one more skill.
   - **B — A new section in `diagnosing-the-jail`.** No new skill; but its description says
     "diagnose a broken coding environment", which "what did I approve" does not match, so the
     page may never load for that question.
   - **C — The briefing alone**, one paragraph. Always loaded, so always costs context; and the
     briefing is rationed on purpose ([information at the point of need](../reference/information-at-the-point-of-need.md)).

   <!-- vantage: question id=OQ-JH4 leaning="A: the lesson is a habit with its own trigger, and a skill description is the one place a trigger lives; the briefing gets one sentence so the habit is reachable even without the match." -->

   _Leaning:_ **A.** The lesson is a habit with its own trigger, and a skill's description is
   the only place a trigger lives; the briefing gets one sentence so the verb is reachable even
   when the match fails.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="JH-D1"></a>JH-D1 | One append-only JSONL record per workspace; every decider appends one event at the decision, beside what it already prints; no collector, no daemon, no second pass over logs | 2026-11-02 | [§5.1](#51-the-workspace-event-record) | no |
| <a id="JH-D2"></a>JH-D2 | Nothing in yolo reads the record to make a decision; it is read to report, by `yolo history` and by people | 2026-11-02 | [§5.1](#51-the-workspace-event-record), [§5.6](#56-the-trust-boundary) | no |
| <a id="JH-D3"></a>JH-D3 | Writes share `receipts.jsonl`'s confined trim-and-append lock; 4 MiB active plus one archive; a line over 8 KiB is cut and marked `truncated`; a failed append costs the event and nothing else | 2026-11-02 | [§5.1](#51-the-workspace-event-record) | no |
| <a id="JH-D4"></a>JH-D4 | Commands in events are the approvals twin's redacted display form (`summaryArgv`); never the audit log's full argv; never stdin, a token, a credential path or the user's hook command line | 2026-11-02 | [§5.1](#51-the-workspace-event-record), [§5.6](#56-the-trust-boundary) | no |
| <a id="JH-D5"></a>JH-D5 | A launch id, `L-<UTC start>-<jail short hash>`, minted once per launch where the machine-wide launch line is armed, before any guard; an attach mints its own and names the launch it joined | 2026-11-02 | [§5.2](#52-the-launch-id) | no |
| <a id="JH-D6"></a>JH-D6 | The launch id crosses as `YOLO_LAUNCH_ID`, an explicit `-e` pair like `YOLO_HOST_LOOPBACK`, and is written on `launch.log`'s header, `launches.log`'s line, `boot.log`'s facts and the broker scope file; it is a fact, not a dial | 2026-11-02 | [§5.2](#52-the-launch-id) | no |
| <a id="JH-D7"></a>JH-D7 | A new verb, `yolo history`, with subjects `requests`, `config`, `launches`, `r-<id>`, `<label>`, `--since`, `--json`; it runs in the jail and on the host; `yolo wait`, `yolo check` and `yolo audit` keep their jobs | 2026-11-02 | [§5.3](#53-yolo-history) | no |
| <a id="JH-D8"></a>JH-D8 | Default window is this launch (since the newest `launch.started` of the workspace); every output ends in a next-step line; a pointer into a free-text log names the file and a line range | 2026-11-02 | [§5.3](#53-yolo-history) | no |
| <a id="JH-D9"></a>JH-D9 | On the host, `yolo history` takes request and call facts from the host store and audit log, labels every jail-written line `jail`, and shows a record line that contradicts the store with the store's version, marked | 2026-11-02 | [§5.6](#56-the-trust-boundary) | no |
| <a id="JH-D10"></a>JH-D10 | Bare `yolo wait` keeps its filter and ends with one line naming how many answered-and-acted-on requests it omitted and `yolo history requests`; a `--for` or `--add` answer lists as not waiting on the agent | 2026-11-02 | [§5.4](#54-the-concrete-gaps-fixed) | no |
| <a id="JH-D11"></a>JH-D11 | The approvals twin gains `answer`, `via`, `jail`, `launch`, `hook`, `spent_at`, `reason`, written by the same `publish`; `via: on_request` is detected from the hook's environment in `yolo approve` | 2026-11-02 | [§5.4](#54-the-concrete-gaps-fixed) | no |
| <a id="JH-D12"></a>JH-D12 | The github-broker writes one per-call line of its own, the git-broker's shape, with request id, authority, set and rc; the transport's `jail=… keys=…` line is unchanged | 2026-11-02 | [§5.4](#54-the-concrete-gaps-fixed) | no |
| <a id="JH-D13"></a>JH-D13 | The skipped-request warning, the hook's run, and the waiter's start, wake and timeout each become an event from the function that already prints or decides them | 2026-11-02 | [§5.4](#54-the-concrete-gaps-fixed) | no |
| <a id="JH-D14"></a>JH-D14 | The event catalogue is closed: a kind not in [§5.5](#55-the-event-catalogue) is not an event; spans, progress and tier lines are not events | 2026-11-02 | [§5.5](#55-the-event-catalogue) | no |
| <a id="JH-D15"></a>JH-D15 | No launch line is added, removed or gated; the record is file-only; the `yolo wait` footer is a verb's output, not the launch stream | 2026-11-02 | [§7](#7-position-relative-to-what-exists) | no |
| <a id="JH-D16"></a>JH-D16 | The thin first slice is the approvals twin's fields, the `yolo wait` footer and `yolo history requests` over the twin; it needs no ruling and ships first | 2026-11-02 | [§10](#10-what-i-would-build-in-order) | no |
| <a id="JH-D17"></a>JH-D17 | The verb is named `history`; the `per_jail_history` pack hook (per-jail shell history files) is a different thing with a similar word, and neither is renamed | 2026-11-02 | [§5.3](#53-yolo-history) | no |
