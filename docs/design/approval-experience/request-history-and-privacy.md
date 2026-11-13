---
title: "Four partial records of past requests, none complete: what a view may show, keep and claim"
date: 2026-11-09
status: draft
stage: DESIGN
next: "Build the host history view on whichever surface OQ-BB24 in boundary-broker.md rules; run the real-browser masking checks"
depends-on:
  - README.md
  - ../boundary-broker.md#OQ-BB24
tags: [design, approvals, history, privacy, audit, queue]
summary: "The request-history-and-privacy sibling of the approval-experience constellation (W2). Past-request data lives in four places (the host store, the audit log, the jail-readable approvals twin, and the queue's own observed history), each partial in a different way. The read-only queue shows pending requests in full, masks the values that commonly carry secrets behind a click while keeping the command digest visible, keeps observed history in memory only (200 entries) with each request as last seen under the same masking, and makes only the completeness claims listed here. A comprehensive host history needs a CLI extension, filed as OQ-BB24 in boundary-broker.md rather than invented here."
---

# Four partial records of past requests, none complete: what a view may show, keep and claim

**Status:** Observed-only memory history and the pending-request display/masking are built in
[`internal/approvalqueue`](../../../internal/approvalqueue/), checked against source at
`62b8bbda3`; the eviction count ([RH-D3](#RH-D3)) and the last-seen request detail
([RH-D6](#RH-D6)) are built since. The original CLI evidence was read and recorded 2026-11-09 from `04f300a9e` in a
throwaway home (sanitized: workspace `/home/example/code/app`, repositories `example/app` and
`git.example.test/example/app`). Comprehensive host history is not implemented; it waits on
[OQ-BB24](../boundary-broker.md#OQ-BB24). [§12](#12-what-done-looks-like) separates delivery from
remaining acceptance work.

> **In short.** No surface yolo has today can answer "what happened to every request" for a
> host user, so the queue must say exactly what it saw and since when. It may show a pending
> request in full because the viewer is the host user, but a page that repaints by itself is a
> *push* surface, and yolo already redacts secret-bearing values on every push surface it owns.

**Why it matters.** The queue's history is the owner's "past requests and approvals" ask
([AE-R3](README.md#11-settled-owner-intent)). A history that silently omits revocations, or a
page that paints a `gh secret set --body` value onto a shared screen, fails that ask in two
opposite directions.

**The shape.** Four records ([§3](#3-where-past-request-data-lives-today)), a per-adapter field
map ([§4](#4-fields-per-adapter)), and three rules for the read-only view: retention
([§5](#5-retention)), display ([§6](#6-what-a-read-only-view-may-show)) and claims
([§7](#7-honest-completeness-claims)).

**Cost.** One click to read a masked value. No CLI change for the read-only slice.

**Start at [§6.1](#61-the-display-ruling-tested)**, where the one change to the lead's display
ruling is argued.

**Needs your ruling:** None in this document. The CLI question it raises is
[OQ-BB24](../boundary-broker.md#OQ-BB24), in its owning document ([§8.3](#83-routed-to-boundary-brokermd)).

**Reads with:** [`README.md`](README.md) (the umbrella and its five rules),
[`web-request-queue.md`](web-request-queue.md) (the queue whose history and display this bounds),
[`browser-trust-boundary.md`](browser-trust-boundary.md) (who may connect; this doc decides what
they see), [`jail-history.md`](../jail-history.md) (the approvals twin and `yolo history`).

---

## 1. Scope and verdict

This doc owns the "Request history and privacy" row of
[the sibling map](README.md#7-proposed-sibling-map). It settles audit fields per adapter,
redaction and retention, and which completeness claims are honest. It changes no CLI surface
([README §3.2](README.md#32-shared-seams-without-a-new-prerequisite-schema)).

**Verdict.**

1. **The queue reads only the CLI JSON surface** in the read-only slice: no `yolo audit`, no
   store files, no approvals twin ([RH-D1](#RH-D1)).
2. **Pending requests show every field**, with values after body, field, header and input flags
   masked until clicked, and the command digest always visible ([RH-D2](#RH-D2), the proposed adjustment
   implemented in the read-only queue; [§6.1](#61-the-display-ruling-tested)).
3. **Observed history lives in the queue's memory only**, capped at 200 entries; the
   evictions are counted and shown ([RH-D3](#RH-D3)).
4. **Claims are limited to [§7](#7-honest-completeness-claims)'s list**; "complete history" is
   never one of them ([RH-D4](#RH-D4)).
5. **A history entry keeps its request as last seen**, masked as pending detail is
   ([RH-D6](#RH-D6)).
6. **A host history view needs the CLI to expose ended records**; that is
   [OQ-BB24](../boundary-broker.md#OQ-BB24) in [`boundary-broker.md`](../boundary-broker.md),
   which owns the surface ([§8.3](#83-routed-to-boundary-brokermd)).

**Non-goals.** Mutation (the trust boundary's), the jail-side history verb
([`jail-history.md`](../jail-history.md) owns it), changing audit retention, and any remote or
multi-user view.

## 2. Terms

- **Store record**: a request, grant or working-set entry file under the host's private broker
  directory, one directory per source. The authority ([README rule 1](README.md#31-five-rules-that-bind-every-sibling)).
- **Audit line**: one JSON line in the host's broker audit log, `audit.jsonl` under the same
  directory ([boundary-broker §8](../boundary-broker.md#8-audit)). Evidence, not authority.
- **Approvals twin**: the jail-readable copy of each request at `<workspace>/.yolo/approvals/<id>.json`
  ([`jail-history.md`](../jail-history.md)'s term). A wake hint the jail can edit; never authority.
- **Observed history**: the queue's in-memory list of what its own snapshots and watch events
  showed ([`web-request-queue.md` §4](web-request-queue.md#4-the-reconcile-rule)).
- **Push surface** *(coined here)*: one that puts request text in front of a person without that
  person asking at that moment: a toast, the `on_request` hook's environment, the approvals twin
  an agent reads, and a self-updating page. **Pull surface** *(coined here)*: one a person runs
  to read, then reads: `yolo approve`, `--json`, `yolo audit`. Not a statement about who may read;
  both kinds here are host-user only, except the twin.
- **Agent text**: any value the jail chose: argv values, titles, refs, ref names, push options,
  API paths. Not agent text: the action, target and headline yolo builds from its own vocabulary
  ([`about.go`](../../../internal/brokerstore/about.go#L88)).
- **Secret-bearing flag** *(coined here)*: one of `--body`, `--body-file`, `--field`/`-F`,
  `--raw-field`/`-f`, `--header`/`-H`, `--input`, the exact list
  [`summaryArgv`](../../../internal/brokerstore/watch.go#L417) redacts. Not a claim that other
  values are secret-free.

## 3. Where past-request data lives today

| Record | Who can read it | Written by | Lasts | Holds | Missing |
| :--- | :--- | :--- | :--- | :--- | :--- |
| Store records | Host user; refused in a jail. `0600` files, `0700` dirs | the store, under its lock | Pruned silently 7 days after a request is decided or a grant or working entry ends ([`store.go`](../../../internal/brokerstore/store.go#L399)) | Everything, including answer kind, `via`, `until`, spend and denial note | On the CLI, every record not currently open ([§4.1](#41-the-json-surface)) |
| Audit lines | Host user; `yolo audit` refuses in a jail ([`audit.go`](../../../internal/cli/audit.go#L69)). `0600`, re-chmodded on each append | each broker and the store's audit hook | Rotated by size: 8 MiB, four archives, about 40 MiB; no age limit ([`brokeraudit.go`](../../../internal/brokeraudit/brokeraudit.go#L38)) | request, decision, grant-used, call and evaluation events | grant end, revoke, expiry, working-set changes; how a request was answered ([§4.2](#42-the-audit-log)) |
| Approvals twin | The workspace's jails and the host user | the store on each request write ([`record.go`](../../../internal/brokerstore/record.go#L21)) | No reaper found in `internal/approvalwait` or `internal/prune` | redacted command, state, answer, `via`, `until`, spend, denial note | other workspaces; full argv; grants; it can be edited from the jail |
| Observed history | The queue's authenticated tab | the queue's reducer | the queue process ([RH-D3](#RH-D3)) | what the queue saw since it started | everything before it started, and anything between two polls |

The store's [`Snapshot`](../../../internal/brokerstore/watch.go#L44) holds decided requests,
ended grants and ended working entries for those 7 days, but
[`StateView`](../../../internal/brokerstore/watch.go#L332) keeps only what is open, so no CLI
prints them. `yolo history requests` reads the twin, per workspace, and is not a host authority
([JH-D2](../jail-history.md#JH-D2)).

## 4. Fields per adapter

**Samples.** Recorded 2026-11-09 from yolo `04f300a9e` in a throwaway home: six pending requests
(two git pushes, one with hostile ref names and a push option; three GitHub writes, two carrying a
fake token in `--body`; one synthetic CircleCI write), a watch transcript across allow, deny with
note, revoke, a `--for` answer and a watcher gap, and the audit log before and after. Limits: the
git requests were filed through the store API, not the real pkt-line parser; the CircleCI request
is synthetic because the shipped classifier files no writes; no twin was produced.

### 4.1 The JSON surface

`yolo approve --json` and the `"state"` line of `--watch --json` carry pending requests, live
grants and live working entries; watch events carry the request or grant concerned.

| Field (RequestView) | git broker | GitHub broker | Pack call broker (CircleCI) | Agent text | Can carry secrets |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `argv` | `git-receive-pack`, then `<old> <new> <ref>` per ref and `push-option=<opt>` | canonical `gh` argv, `--flag=value` form, bodies in full | classifier argv (CLI front) or `["http"]` (HTTP front) | yes, raw: controls and bidi kept, JSON-escaped only ([`watch.go`](../../../internal/brokerstore/watch.go#L297)) | **yes** (`--body`, headers, `gh api` fields) |
| `command` | `argv` quoted for reading | same | same | yes; controls shown as escapes, HTML untouched, not redacted | **yes** |
| `title`, `refs` | refs needing approval | title from `--title`, `--subject`, workflow or API path | absent: no `About` is set ([`decision.go`](../../../internal/callbroker/decision.go#L23)) | yes, terminal-safe ([`watch.go`](../../../internal/brokerstore/watch.go#L303)) | possible, uncommon |
| `description` | headline plus refs | headline plus title | `a circleci request` | yes, terminal-safe | as title |
| `action`, `target`, `headline` | yolo's words | yolo's words | headline only, generic | no | no |
| `needs_approval` | refs nothing covered | absent | absent | yes, raw | no |
| `repo`, `set` | repository identity, `push` | `owner/repo`, `read-write` | source name in both | no | no |
| `jail`, `workspace` | host-asserted | host-asserted | host-asserted | no | host paths |
| `hook_failure`, `hook_output` | user's hook, terminal-safe | same | same | no (the user's) | whatever the hook prints |
| Grant: `id`, `request`, `coverage`, `uses_left`, `expires`, `ended`, `end_reason` | identifiers and times only | same | same | no | no |
| Working entry `resource` | the full ref, raw | `#N` | `call:<cell>` chain | git: yes, raw ([`watch.go`](../../../internal/brokerstore/watch.go#L279)) | no |
| Event `summary` | secret-bearing values `[redacted]`, long values elided, capped at 2048 bytes ([`watch.go`](../../../internal/brokerstore/watch.go#L376)) | same | same | yes, terminal-safe | redacted |

**Held in the store, on no JSON surface:** `denial_note`, `answer`, `via`, `until`, spend time,
lapse, close reason, policy ceiling, original argv, resource chains, HTTP digests, working-entry
end, and every request after its decision event. A watch event's `time` is when the watcher saw
the change ([`watch.go`](../../../internal/brokerstore/watch.go#L289)); a request's `decided` is
the store's time, and for an expiry it is filing time plus one hour.

### 4.2 The audit log

| Event | git broker | GitHub broker | Pack call broker |
| :--- | :--- | :--- | :--- |
| `request` | full argv, repo, digests, workspace ([`brokercore/audit.go`](../../../internal/brokercore/audit.go#L13)) | same | digest, request id, `set: write` only ([`callbroker/audit.go`](../../../internal/callbroker/audit.go#L13)); not observed, the sample had no hook |
| `decision` | as `request`, `outcome` allowed or denied, `grant` (a `w-` id for a `--for` answer) | same | identifiers only, `argv: null` (observed) |
| `grant-used` | as `request`, jail = spender, `outcome: spent-before-dispatch` | same | identifiers only |
| `call` | `argv: [git-receive-pack]` only, remote, per-ref old/new/covered-by, exit, bytes, time ([`broker.go`](../../../internal/gitbroker/broker.go#L408)) | full argv, reported agent, stdin byte count and SHA-256, exit, `gh` version, redaction count ([`daemon.go`](../../../internal/ghbroker/daemon.go#L407)) | outcome, reason, digest |

Every argv in the audit is in full, every value, on every line that carries one (observed: the
fake token in `request`, `decision` and `grant-used`). The file header says so
([`brokeraudit.go`](../../../internal/brokeraudit/brokeraudit.go#L12)).

**The audit records no** grant end, revoke, request expiry, working-set addition or end, or hook
run: the store's only audit calls are request, decision, decision-withdrawn and grant-used
([`store.go`](../../../internal/brokerstore/store.go#L752)), and
[`Revoke`](../../../internal/brokerstore/store.go#L1362) and
[`End`](../../../internal/brokerstore/store.go#L1407) write none (observed: `--revoke` left no
line). A decision line carries no answer kind, duration, `via` or note.

### 4.3 Safe text is terminal-safe, not browser-safe

- `termsafe` makes controls, bidi and zero-width characters visible for a **terminal**
  ([`termsafe.go`](../../../internal/termsafe/termsafe.go#L21)). It leaves `<`, `>`, `&` and quotes
  alone (observed `<img src=x onerror=…>` in `title`).
- `argv`, `needs_approval` and working `resource` are not made terminal-safe at all.
- **No field is length-capped** except the event summary: a 5011-character ref reached `refs`,
  `description` and `command` unchanged.

The queue already renders with `textContent` only and markers for controls
([`browser-trust-boundary.md` §5](browser-trust-boundary.md#5-browser-hardening-and-safe-rendering));
these facts are why that rule covers every field, not only the ones yolo labels agent text.

## 5. Retention

| What | Where | How long | Trigger |
| :--- | :--- | :--- | :--- |
| Pending request | store | 1 hour, then `expired` ([`store.go`](../../../internal/brokerstore/store.go#L32)) | the next transaction, a watch poll included |
| Decided request, ended grant, ended working entry | store | 7 days after decision or end, longer while a live grant or working entry needs the request | any transaction on that source; no event |
| Audit line | host audit log | until 40 MiB of newer lines push it out; forever on an idle machine | each append checks size |
| Twin | workspace `.yolo/approvals` | no deletion found | n/a |
| Observed history | queue memory | the queue process; at most 200 entries | [RH-D3](#RH-D3) |

**Decision ([RH-D3](#RH-D3)): the queue persists nothing.** History is in memory, capped at 200
entries keyed by request or grant id; the oldest entry is evicted first and counted. The page
labels the start, the 200-entry cap, the gaps and, once an entry is evicted, "N older entries
dropped" (`dropped` in the published history; `TestHistoryCountsEvictionsAndPublishesThem` in
[`reduce_test.go`](../../../internal/approvalqueue/model/reduce_test.go)). The browser stores no history either: `sessionStorage` holds the
session token alone, and data routes are `no-store`
([`browser-trust-boundary.md` §8](browser-trust-boundary.md#8-the-read-only-slice-requirements)).

Why not persist: a file of observed requests would be a third on-disk copy of full argv, outside
the broker directory that [BB-D26](../boundary-broker.md#BB-D26)'s mount fence covers, with its own
retention to design; the store and audit already hold the durable copies, and the honest fix is
exposing them ([§8](#8-what-a-host-history-view-would-need)), not copying them.

## 6. What a read-only view may show

### 6.1 The display ruling, tested

The lead's ruling for the slice: pending requests in full, as the host terminal listing shows
them, because full argv is what is authorized and the viewer is the token-holding host user;
controls as markers; agent words labeled; history observed since start; no `yolo audit`.

| Test | Terminal listing | Queue tab | Holds? |
| :--- | :--- | :--- | :--- |
| Only the host user can connect | yes | yes, by token | holds |
| The user sees the exact command before deciding | yes | yes | holds |
| A value appears only when the user asks to look | yes: a pull surface | **no**: a new request paints itself, possibly during a screen share or recording | fails for secret-bearing values |
| A tab left open shows it later to whoever walks by | scrollback, if not cleared | yes, until the request leaves, and in history after | fails for the same values |
| Secret-bearing writes exist | `gh secret set NAME --body VALUE` is a brokered write that asks ([`classify_test.go`](../../../internal/ghbroker/classify_test.go#L426)) | same | the case is real, not hypothetical |

yolo already draws this line: every push surface it owns uses `summaryArgv` (the toast and hook
summary, the twin's command), and only pull surfaces carry full values. A self-updating page is a
push surface, so it takes the same default.

**Decision ([RH-D2](#RH-D2), proposed adjustment to the lead's ruling, implemented for pending requests):**

- Every field is shown, as ruled, **except the value following a secret-bearing flag**, which
  shows as `--body=[hidden: 47 characters, sha256 1a2b3c4d] Show`, modeled on yolo's own elision
  (`…[N chars, sha256 xxxxxxxx]`, [`watch.go`](../../../internal/brokerstore/watch.go#L457)) but
  hiding the whole value.
- **Show** reveals that one value in place. Reveal state lives in page memory per request and
  value, and every value re-masks when the page becomes hidden (`visibilitychange`) or reloads.
- **The command digest and stdin digest are always visible**, so the action's identity never
  depends on a click.
- The masked list is `summaryArgv`'s, exactly, including the joined (`--body=x`) and glued
  (`-Fx`) spellings; a test pins the two lists together.
- Nothing else is masked: titles, refs and the rest of argv show in full, as ruled. The data
  still reaches the page in full; masking defends against eyes, not against the tab's own script.

This narrows "nothing is elided" in [`web-request-queue.md` §7](web-request-queue.md#7-rendering-request-text)
and [`browser-trust-boundary.md` §8](browser-trust-boundary.md#8-the-read-only-slice-requirements):
a masked value is one click away, not dropped. Server-side withholding was considered and rejected:
anything holding the token could fetch the reveal route, so it adds a route and protects nothing
more.

### 6.2 The rest of the display rules

| Rule | Why |
| :--- | :--- |
| Every field as text, markers for controls and bidi, agent text labeled under yolo's own fields | [§4.3](#43-safe-text-is-terminal-safe-not-browser-safe); [`browser-trust-boundary.md` §5](browser-trust-boundary.md#5-browser-hardening-and-safe-rendering) |
| Long values wrap in a scrolling box; never truncated | an elided ref is a hidden ref |
| Every request and history entry shows its **workspace** and source | grants are held by workspace ([README §2.2](README.md#22-authority-facts-every-surface-must-preserve)); one view spans every workspace on the machine |
| `deny-only` sources and `source_status` shown beside the request | the CLI can refuse to allow them (exit 6) |
| A request's history entry keeps the request as last seen and follows the same masking ([RH-D6](#RH-D6)) | a decided request's body is no less secret, and a history line without the command cannot say what was decided |
| No field becomes a link, including repositories and refs | [`browser-trust-boundary.md` §5](browser-trust-boundary.md#5-browser-hardening-and-safe-rendering) |

**Cross-workspace.** `yolo approve --json` lists every workspace's requests, and the host user may
see all of them; workspaces are not a privacy boundary between the host user and themselves. They
are one toward jails: nothing the queue holds is written anywhere a jail can read, which is
[JH-D4](../jail-history.md#JH-D4)'s rule for the twin.

## 7. Honest completeness claims

| The queue may say | Because |
| :--- | :--- |
| "Pending as of HH:MM:SS" | the fetch start of the last good snapshot |
| "Observed since HH:MM:SS (queue start); N older entries dropped" | its own record, and its cap |
| "Allowed / denied / refused / expired at T" for a request it saw decided | the event, with `decided` from the store; T is the store's time, not the event's |
| "No longer pending; outcome not observed" | a snapshot dropped it with no event |
| "Grant used by jail X; the use was spent before the command ran" | `grant-used` |
| "Grant ended: used / expired / revoked" | the `end_reason` of an event it saw |

| The queue must never say | Because |
| :--- | :--- |
| "Complete history", "all requests", "nothing else happened" | four partial records; watcher gaps are not replayed (observed) |
| "Denied" or "expired" from disappearance | pruning and watcher gaps also remove things |
| "Ran", "succeeded", "pushed" from allowed or grant-used | spend happens before dispatch |
| "Revoked by the user" from a working entry leaving | working-set changes emit no event, and end, expiry and revoke look alike in a snapshot |
| The denial note, answer kind or duration | not on the JSON surface ([§4.1](#41-the-json-surface)) |

**Spend before dispatch.** The store marks a use spent and writes the `grant-used` audit line
before the broker runs the command
([README §2.2](README.md#22-authority-facts-every-surface-must-preserve)). A command that then
fails, or a broker that crashes, has still consumed the use; nothing refunds it. So "used" means
"authorization consumed". Whether the command ran is only in the audit's `call` line, with its
exit code, which the read-only slice does not read; the page says "outcome of the command not
shown here; `yolo audit` on the host has it".

## 8. What a host history view would need

### 8.1 Gaps

| Fact a history needs | Store has it | JSON surface | Audit |
| :--- | :--- | :--- | :--- |
| Decided requests after their event | 7 days | no | `decision` line, no answer kind |
| Answer kind, `--for` duration, `via`, denial note | yes | no | no |
| Grant ended, and why | 7 days | only as a live event | no |
| Revoke, who and when | yes (end reason) | only as a live event | no |
| Request expired | yes | only as a live event | no |
| Working-set entry added, ended, revoked | 7 days | live entries only, no events | no |
| Command outcome (exit) | no | no | git and GitHub `call` lines; pack lines have outcome and digest only |

### 8.2 Candidate extensions

| Candidate | Gives | Verdict |
| :--- | :--- | :--- |
| **A. `yolo approve --json --ended`** (spelling the owner's): the same state object plus records ended within the store's 7 days, with `answer`, `via`, `until`, `spent_at`, `reason`, `denial_note` added to schema 1 | a week of complete, authoritative store history for every source, from records that exist today | **recommended first**: additive, no new storage, the store already reads them |
| **B. Audit events** for grant end, revoke, expiry and working-set changes, and the answer fields on `decision` | history beyond 7 days, bounded by size | worth doing second; it also makes [boundary-broker §8](../boundary-broker.md#8-audit) true |
| C. The queue reads `audit.jsonl` itself | everything the audit has | rejected: a second reader of private host state ([`web-request-queue.md` §1](web-request-queue.md#1-scope)) |
| D. The queue reads the twin | answer and note per workspace | rejected: jail-editable, never authority ([JH-D2](../jail-history.md#JH-D2)) |
| E. `yolo audit --json` as the history source, unchanged | full argv for 40 MiB of every workspace | not alone: no ends, revokes or expiries; a later slice may add it behind [§6.1](#61-the-display-ruling-tested)'s masking |

### 8.3 Routed to boundary-broker.md

Per [README §3.2](README.md#32-shared-seams-without-a-new-prerequisite-schema), a CLI surface
change is ruled where the surface is owned. Which candidate closes [§8.1](#81-gaps)'s gaps is
[OQ-BB24](../boundary-broker.md#OQ-BB24), filed in
[`boundary-broker.md`'s past-requests section](../boundary-broker.md#past-requests-what-the-surface-does-not-list)
with candidates A, B and C of [§8.2](#82-candidate-extensions) as its options and A, then B, as
its leaning. This document adds no option of its own and builds nothing ahead of the ruling.

**What holds whichever way it is ruled:**

- The queue reads the chosen surface through the CLI, never store or audit files ([RH-D1](#RH-D1)).
- Every request it shows from that surface takes [§6.1](#61-the-display-ruling-tested)'s
  masking; for option B, [§8.4](#84-one-audit-log-for-every-workspace)'s workspace filter too.
- Ended records join the observed history under the same 200-entry cap
  ([RH-D3](#RH-D3)); the queue still persists nothing.
- The may-say table of [§7](#7-honest-completeness-claims) widens only by what the surface
  carries: with A, "denied, with this note" and the answer kind become sayable for a record
  within the store's 7 days, and "complete history" still never is.

### 8.4 One audit log for every workspace

The audit is one file for the machine; `--workspace` filters by exact path only. A host view that
reads it must filter to one workspace by default, label every line with its workspace, and say the
log is size-bounded ("the oldest line here is from T"). It must never copy lines toward a jail; a
per-workspace record is the broker writing a new, redacted event ([`jail-history.md` §5.6](../jail-history.md#56-the-trust-boundary)).

## 9. Privacy notes

- **The denial note reaches the agent, by ruling.** It is copied to the twin
  ([`record.go`](../../../internal/brokerstore/record.go#L29)) and printed by `yolo wait` and
  `yolo history` as "Human feedback" ([`wait.go`](../../../internal/approvalwait/wait.go#L144),
  [`history.go`](../../../internal/approvalwait/history.go#L254)), as
  [OQ-BX5](../pack-brokers.md#decision-ledger) rules (2026-11-04). It is stored as typed (at most
  500 characters, valid UTF-8, [`store.go`](../../../internal/brokerstore/store.go#L965)) and made
  terminal-safe only when printed. `yolo approve --help` says it is shown as feedback but not that
  the agent reads it; a future queue Deny box must say "the agent will read this".
- **The audit is host-private by placement only.** `0600` under the broker directory, which the
  workspace-scope mount fence covers; a user-scope `mounts` entry can still expose it
  ([`browser-trust-boundary.md` §2](browser-trust-boundary.md#2-threat-model)).
- **The twin is never pruned** as far as the code shows, and holds a redacted command, so it is
  low-risk but grows without bound.
- **Host paths** of every workspace appear in `workspace` fields and audit lines; the queue shows
  them to the host user only.

### Where yolo's own text disagrees with the code

- `yolo audit --help` says `--json` prints each line "as stored"; it re-encodes the decoded
  event ([`audit.go`](../../../internal/cli/audit.go#L138)), so unknown fields are dropped.
- [`brokeraudit.go`](../../../internal/brokeraudit/brokeraudit.go#L59)'s comment says lifetime
  teardown records `grant-ended`; it does not.

## 10. Alternatives

| Alternative | Verdict |
| :--- | :--- |
| Show pending requests with every value in full (the lead's ruling as stated) | Rejected for secret-bearing values only: a push surface paints them unasked ([§6.1](#61-the-display-ruling-tested)) |
| Show only the redacted summary | Rejected: the user must be able to read exactly what is authorized |
| Mask on the server, reveal through a route | Rejected: the token holder can call it; more surface, no gain |
| Persist observed history to disk | Rejected ([§5](#5-retention)) |
| Unbounded in-memory history | Rejected: a long-running queue grows without limit; 200 matches the queue doc's model size |

## 11. Risks

| Risk | Mitigation |
| :--- | :--- |
| A secret sits outside the masked flags (a title, `--notes`, a positional) | Stated as a limit, not a promise; the list is yolo's, and widening it widens every push surface together |
| The masked list drifts from `summaryArgv` | A test feeds both the same argv list |
| A reader takes "used" for "ran" | [§7](#7-honest-completeness-claims)'s wording on the page |
| History read as complete | Header line always states its start and dropped count |
| An eventual `yolo audit` view leaks cross-workspace argv | Workspace filter by default, same masking ([§8.4](#84-one-audit-log-for-every-workspace)) |

## 12. What done looks like

**Built source and automated coverage**, not new live-browser proof:

- Each request's history entry keeps the request as last seen, from the newest event or
  snapshot that carried it, and the page draws it with the pending card's masking under its own
  reveal keys. [`history_detail_test.go`](../../../internal/approvalqueue/model/history_detail_test.go)
  covers the decided, vanished, late-event and refreshed cases and that the raw command is
  never published; [`web_test.go`](../../../internal/approvalqueue/web/web_test.go) pins the
  shared card and the separate reveal keys.

- Pending values after secret-bearing flags are masked with length/hash and Show/Hide;
  digests stay visible. [`mask_test.go`](../../../internal/approvalqueue/model/mask_test.go)
  pins the list and spellings to yolo's `summaryArgv`. The page implements re-masking on
  visibility change; [`web_test.go`](../../../internal/approvalqueue/web/web_test.go) checks
  the static rendering constraints, not a real tab-switch interaction.
- The reducer reconciles missed transitions without inventing denial or expiry, keeps at most
  200 entries, counts the older entries it drops and labels history as observed since start with
  watch gaps. [`reduce_test.go`](../../../internal/approvalqueue/model/reduce_test.go) covers the
  cap, the dropped count and recorded watcher gap; [`publish.go`](../../../internal/approvalqueue/model/publish.go)
  publishes the limits. No queue/browser history persistence is implemented.
- Allow and use remain distinct from execution; the page explicitly says history is **not**
  complete. A disclaimer containing "complete" is not a completeness claim.

**Existing requirements still unmet:**

- Exercise Show/Hide (pending and history), tab-switch re-masking and restart behavior in a
  real browser against the real host path; the [umbrella's runtime evidence gaps](README.md#54-the-read-only-queue-as-built-yolo-approve---web)
  remain open.
- Comprehensive history needs the host interface [OQ-BB24](../boundary-broker.md#OQ-BB24)
  rules; the queue must not work around it by reading store or audit files.

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="RH-D1"></a>RH-D1 | The read-only queue reads only `yolo approve --json` and `--watch --json`; no audit, store or twin | 2026-11-09 | [§1](#1-scope-and-verdict) | Yes: CLI supervisor |
| <a id="RH-D2"></a>RH-D2 | Pending requests in full, except values after `summaryArgv`'s flags, masked with length and hash and revealed per value by click; digests always visible; re-mask when hidden (proposed change to the lead's ruling) | 2026-11-09 | [§6.1](#61-the-display-ruling-tested) | Yes: pending display/masking; real-browser proof outstanding |
| <a id="RH-D3"></a>RH-D3 | Observed history in queue memory only, 200 entries, oldest evicted and counted; nothing persisted by queue or browser | 2026-11-09 | [§5](#5-retention) | Yes: memory, cap, eviction and dropped count |
| <a id="RH-D4"></a>RH-D4 | The queue makes only the claims in the may-say table; never complete history, never inferred outcomes, never execution from use | 2026-11-09 | [§7](#7-honest-completeness-claims) | Observed-outcome labels and gap disclaimers built |
| <a id="RH-D5"></a>RH-D5 | Comprehensive host history waits on a CLI extension; `--ended` first and audit events second are the candidates, routed 2026-11-13 to boundary-broker.md as [OQ-BB24](../boundary-broker.md#OQ-BB24) | 2026-11-09 | [§8](#8-what-a-host-history-view-would-need) | No: waits on [OQ-BB24](../boundary-broker.md#OQ-BB24) |
| <a id="RH-D6"></a>RH-D6 | *Implementation decision* for [§6.2](#62-the-rest-of-the-display-rules)'s last-seen rule: a request entry holds a copy of the newest request object the queue saw for its id (the filing or deciding event's, a newer snapshot's while it is still pending, or the last snapshot's when it vanishes); an update the history ranking rejects keeps the entry's copy. It is published through the same function as pending detail, so the masking cannot drift, and drawn by the same card under reveal keys of its own. Grant entries carry none | 2026-11-13 | [§6.2](#62-the-rest-of-the-display-rules) | Yes: `internal/approvalqueue/model`, `web/app.js` |
