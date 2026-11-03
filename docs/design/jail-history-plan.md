---
title: "Plan seed: the workspace event record, the launch id and yolo history"
date: 2026-11-02
status: draft
stage: SKETCH
next: "Build slice 1 (the approvals twin's fields, the yolo wait footer, yolo history requests); the rest waits on the design's rulings"
depends-on:
  - jail-history.md#OQ-JH1
  - jail-history.md#OQ-JH2
  - jail-history.md#OQ-JH4
tags: [plan, seed, diagnostics, approvals, broker, launch, skills]
summary: "Seed of the implementation plan for jail-history.md: what the design work surfaced, parked here so the design stays at altitude. Not a hand-off. Slice 1 needs no ruling and is named in full; everything after it waits on OQ-JH1, OQ-JH2 and OQ-JH4."
---

# Plan seed: the workspace event record, the launch id and `yolo history`

**Status:** 2026-11-02 — seeded during design; not a hand-off, and incomplete until the design
is accepted. **Nothing is built from this file while it is stamped `SKETCH`**, except slice 1
below, which rests on no open question and is named so a builder can take it now.
**Design:** [`jail-history.md`](jail-history.md). Precedence: the design wins on behavior, the
tree wins on fact, this file is advice and the first thing to be wrong.

The planner completes this file at the design's `status: accepted`: map, reuse, traps, tests,
build order. Every line below is a candidate, not evidence; each was read at `9506ea4e0` on
2026-11-02 and nothing was searched for to fill this file.

## Slice 1 — needs no ruling: the approvals twin says how, and the verb reads it

The thin first slice ([JH-D16](jail-history.md#decision-ledger)): no record, no launch id, no
new file under `.yolo`. Three edits to things that exist.

1. **The twin's new fields.** `approvalwait.Record` (`internal/approvalwait/record.go`) gains
   `answer` (`once|for|add|remember|deny`), `via` (`on_request|host`), `jail`, `launch` (the
   broker scope's per-source id until the real launch id exists; the planner decides whether to
   leave the field empty instead), `hook` (`ran|failed|none`), `spent_at`, `reason`
   (`broker-stopped` for a refusal). Written by `brokerstore.Store.publish`
   (`internal/brokerstore/record.go`), which already has the request; the decision kind comes
   from the transaction that called it (`Decide` for once/deny/`--for` whole-set, `DecideWorking`
   for per-resource `--for`/`--add`, `approveremember.go` for `--remember`, `spendIn` for spent,
   `End` for broker-stopped). Fields are additive and `omitempty`; an older record reads as
   before.
   - `via: on_request`: `yolo approve` sees `YOLO_REQUEST_ID` in its environment equal to the id
     it is answering (`brokerhook.Env`, `internal/brokerhook/brokerhook.go`, sets it for the
     hook). Check it is not also set in some other path before relying on it.
   - `hook`: the runner's `noteFailure` path exists (`NoteHookFailure`); the *ran* path has no
     store write today — one is added, to the request record, not to the audit log
     (`store.go` says hook failure is not audited; keep that).
2. **`yolo wait` bare.** `approvalwait.List` (`internal/approvalwait/wait.go`) keeps its filter,
   counts what it skipped for `Used || Lapsed` within the day, and prints the footer. A record
   whose `answer` is `for` or `add` is listed with its window or "working set" and without the
   "run its command again" instruction. The usage text in `internal/cli/wait.go` and the briefing
   sentence *"A bare `yolo wait` lists the requests still waiting on you"*
   (`internal/jailcontent/briefing.go`, `hostRunSection`) stay true and need no change; the
   footer's wording is the implementer's within the design's shape.
3. **`yolo history requests` and `yolo history r-<id>`** over the twin directory alone: every
   record, newest last, with filed/decided/spent times, the answer and the next step. Registered
   in the dispatch registry the way `wait` is (`internal/cli/commands.go`; `cli.go` routes `wait`
   before the front door — decide whether `history` is routed the same way: it is read by an
   agent, so probably yes). Refuse nothing in a jail. On the host, read the host store for the
   same request when `-C` names a workspace (the planner checks `brokerstore` opens read-only
   without a daemon).

A failing test exists for each: a `Used` record is not listed and the footer names it; a `for`
record is listed without the retry instruction; `yolo history r-<id>` prints the answer kind
from a fixture twin. `TestConfigRefTOMLSectionNamesTheVerbsTheCLIHas` and the help tests
(`hintcommands_test.go`, `subhelp_test.go`) will notice a new verb.

## Parked: the launch id ([§5.2](jail-history.md#52-the-launch-id))

- Mint beside `o.armLaunchRecord()` in `run.Run` (`internal/cli/run/run.go`): it is already
  the first act above every guard. `launchrecord.go` has the jail short hash the form needs.
- `launch.log` header: `launchlog.go` prints `=== yolo launch <time> ===`; add the id on that
  line, not a new line, so the block delimiter grep stays one regex.
- `launches.log`: `launchrecord.go`'s line gains `launch=`; its 1 MiB cap and `.1` rotation are
  unaffected (~20 more bytes a line).
- `boot.log`: `bootLogFacts` (`internal/entrypoint/bootlog.go`) is the list; add `YOLO_LAUNCH_ID`.
- Container env: the explicit `-e` list in `internal/cli/run/assemble.go` (the
  `YOLO_HOST_LOOPBACK` pair is the shape; it is always emitted). macos-user: the baked list in
  `internal/macosuser` (`env -i …`); check how `YOLO_HOST_LOOPBACK` reaches the guest there.
- Broker scope file: `brokerscope.File` gains the field; `ghbroker`/`gitbroker` read it where
  they read `LaunchID`.
- Attach: where the attach reads the live pack tree (`pack-trees/.live`, `packtree.go`) is where
  the running launch's id is kept and read back; the planner names the file.

## Parked: the record ([§5.1](jail-history.md#51-the-workspace-event-record)) — blocked on [OQ-JH1](jail-history.md#OQ-JH1)

Blocked on [OQ-JH1](jail-history.md#OQ-JH1) — the file's location decides the opener.

- Writer: `entrypoint.AppendReceiptLine` (`internal/entrypoint/capturereceipt.go`) is the
  confined trim-and-append the design names; it lives in `entrypoint`, and host writers
  (launcher, brokers, `yolo approve`) cannot import that package cleanly. Expect to lift the
  append into a small package both sides import (the lock, the confined open, the `logcap`
  trim), leaving `AppendReceiptLine` as a caller.
- Opener: `paths.OpenStateDirRoot` / `OpenStateSubdirRoot` and the create flags in
  `approvalwait.Publish` are the host-side pattern (no-follow, exclusive temp, rename). An
  append is not a rename; the planner reconciles "append under a root that refuses links" with
  `logcap`'s in-place trim.
- Readers: `yolo history` sorts by `t` then file order, tolerates an unreadable line, and treats
  a missing file as "pre-feature" with the fallback text.
- Event kinds and fields are the design's closed catalogue
  ([§5.5](jail-history.md#55-the-event-catalogue)); a Go type per kind is the implementer's
  choice, the JSON field names are not.
- Clock: all backends share the host kernel clock or the host's VM clock; no skew handling.

## Parked: the writers, per lifecycle

- **Request events** — `brokerstore.Store`: `fileIn`, `Decide`, `DecideWorking`, `spendIn`,
  `lapsedIn`, `End`, the lazy expiry at `store.go:292-301`; each already calls `publish`, so
  the event write sits beside it. The store knows `Workspace`.
- **Hook events** — `brokerhook.Runner.fire`; a *ran* outcome has no write today.
- **Call events** — blocked on [OQ-JH2](jail-history.md#OQ-JH2). The github-broker's per-call
  line goes in `ghbroker.ServeContext` where the audit `call` event is appended
  (`daemon.go:345-367`); the git-broker already has one (`broker.go:269`), lacking the request id.
- **Config events** — `run.ConfigApply` (`liveapply.go`: the `len(plan)==0` branch at the
  "Nothing to apply" print, the result block after `runApplySteps`); the launch gate in
  `preflight.go` where `brokeredSkippedGateLines` is appended; `yolo request`'s
  `reportRequestSkipped`; `yolo allow` (host only).
- **Launch events** — `run.Run` start, `recordLaunchExit`, the attach branch at the
  "Attaching to jail" print (`run.go` ~3223), the keeper's "last session left".
- **Boot events** — `entrypoint` boot start, `bootLog.finish` (complete or refused), the
  reachability witness (`reachability.go`), required-service failure (`requiredservice.go`).
- **Wait events** — `approvalwait.Wait`, `hostrun.Wait`.
- **Host-run events** — `internal/hostrun` already writes `request.json`/`result.json` under
  `.yolo/host-run/<id>/`; the event is a pointer to that directory, not a copy.
- **Daemon and auth events** — last; `hostdaemonsupervise.go` already mirrors deaths into
  `launch.log`, so the event sits beside that mirror.

## Parked: `yolo history` ([§5.3](jail-history.md#53-yolo-history))

- Host vs jail: `config.InJail()` is the switch `yolo audit` uses; on the host the request facts
  come from `brokerstore` (read-only open) and `brokeraudit.Read`.
- The free-text pointer (`launch.log:6301-6320`): the `=== yolo launch` headers delimit blocks;
  the line range is the block that holds the launch id.
- Output color goes through `tty.Color` like every verb (`cli-color.md`).
- `--since` parsing exists in `audit.go` (`parseSince`).

## Parked: the skill ([§6](jail-history.md#6-the-skill-read-the-record-before-you-say-i-cant-tell)) — blocked on [OQ-JH4](jail-history.md#OQ-JH4)

Blocked on [OQ-JH4](jail-history.md#OQ-JH4) — whether a third built-in skill exists.

- Built-in skills are rendered from a fact sheet: `RenderBuiltinSkills` lists the two names
  (`internal/jailcontent/mechanicsrender.go`); a third joins that list and the embed
  (`builtinskills/embed.go`). The jail-mechanics index (`mechanicsrender.go`, its template) takes
  one line. The host notch renders built-ins too (`applyhostskills.go`): the skill must read
  correctly on the host, where `yolo history` runs in the workspace.
- The briefing sentence goes in `hostRunSection` (`briefing.go`), beside *"A bare `yolo wait`
  lists…"*; `briefing_test.go` pins that section's lines.
- `diagnosing-the-jail` §6 and §7 cross-link; nothing else in it changes.
- Content is the design's outline, in the second person, steps not explanations
  ([JM-P4](../reference/jail-mechanics-skill.md#JM-P4)).

## Searches for the planner, written as searches

- Whether `pack-trees/.live` or `.yolo/last-launch.json` is the better home for the running
  launch's id, given that `last-launch.json` is rewritten by a fresh launch and an attach must
  read, not write.
- Whether `brokerstore` can be opened read-only without a daemon holding its lock, for the
  host-side `yolo history`.
- Whether any other path sets `YOLO_REQUEST_ID` in `yolo approve`'s environment.
- How `YOLO_HOST_LOOPBACK` reaches the macos-user guest, to route `YOLO_LAUNCH_ID` the same way.
- Whether the scope file carries a written-at time (it may not), before relying on it for the
  config-applied event's "grants widened at" fact.
- Whether `TestTheLaunchHasNoQuietFlag` or any `runFlags` test is touched by nothing here (it
  should not be: no launch flag is added).

## Don't

- Don't parse `launch.log` or the broker logs to build the timeline; the record is the source,
  the logs are what it points at ([§8](jail-history.md#8-alternatives-considered)).
- Don't copy `audit.jsonl` lines anywhere a jail reads; the argv there is unredacted
  ([§5.6](jail-history.md#56-the-trust-boundary)).
- Don't make the broker scope's `LaunchID` the launch id; there is one per brokered pack.
- Don't change bare `yolo wait`'s filter; add the footer.
- Don't read the record anywhere to decide anything; a test that greps it for a decision is the
  wrong test.
