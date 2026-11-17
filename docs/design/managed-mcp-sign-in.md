---
title: "Managed MCP sign-in: the agent's own sign-in first, then capture, delivery, status and recovery"
date: 2026-11-17
status: in-review
stage: DESIGN
next: "Close MS-E1 callback routing, MS-E2 licence/provenance and MS-E3 bundle-delivery capability before accepting the design or completing the seed"
depends-on: [mcp-auth-sharing.md]
tags: [design, mcp, oauth, credentials, broker, packs, status, recovery]
summary: "The first credential for a one-sign-in MCP server comes from the ordinary sign-in inside a yolo-managed agent, at a sign-in point a selected pack delivers. The existing broker captures and refreshes it; the source agent's duplicate plain entry becomes a header route; a token-free, zero-effect `yolo mcp-auth status` and recovery lines say which layer failed and which native server to authenticate. No third login command, no client id invented or borrowed by core."
---

# Managed MCP sign-in: the agent's own sign-in first, then capture, delivery, status and recovery

**Status:** 2026-11-17 — proposed and unbuilt; awaiting engineering closure of [MS-E1, MS-E2 and MS-E3](#open-items-engineering-none-is-an-owner-question). This is not an accepted build hand-off.

> **In short.** The existing broker already adopts, refreshes and (for pi) delivers a one-sign-in credential. This document adds the first step it lacks, a declared place to sign in inside a managed agent. It also repairs Claude's duplicate unauthenticated entry, and makes every failure layer visible without printing or spending a token.

**Needs your ruling:** None. The owner's rulings of 2026-11-17 are recorded as MS-R1 to MS-R5; engineering closure remains open below.

**Reads with:** [`managed-mcp-sign-in-plan.md`](managed-mcp-sign-in-plan.md) (the companion seed, not a build hand-off), [source-read grounding](../research/managed-mcp-sign-in-grounding.md) (source/public evidence and its limits), [`mcp-auth-sharing.md`](mcp-auth-sharing.md) (the sharing contract).

**Authority.** This is a companion to the accepted [`mcp-auth-sharing.md`](mcp-auth-sharing.md) (the *sharing design*), not a second owner of its subject.

- The sharing design keeps scope, adoption, refresh ownership, the canonical store, delivery routes, sign-out and parity.
- This document owns four things that design lacks or leaves unbuilt:
  - the **sign-in point** ([§4](#4-the-sign-in-point));
  - the **callback route** ([§5](#5-the-callback-route));
  - the **delivery choice for a source agent** ([§6](#6-delivery-choice-including-the-source-agent));
  - the **status and recovery contract** ([§7](#7-token-free-status), [§8](#8-recovery-lines)).
- Where the two disagree on those four, this document governs. [§13](#13-amendments-owed-to-the-sharing-design) lists the pointer edits the sharing design is owed.

**Evidence boundary.**

- Shipped-baseline statements come from the [2026-11-17 source-read grounding](../research/managed-mcp-sign-in-grounding.md) at baseline `a97a0e055`, and from the sharing design's text, read in full.
- No test ran. No agent, credential store, provider or installer was touched.
- Everything not under "shipped baseline" is **proposed**.

## 1. Requirement

The owner's requirement, as relayed in the dispatch brief. The exact wording is the brief's, not a verbatim quote.

- A normal sign-in inside a yolo-managed agent completes browser authorization.
- yolo captures that credential, refreshes it on the host, and delivers it to every participating agent and jail.
- There is no separately installed host client and no third yolo login ceremony.
- Status is token-free, and recovery is truthful about which layer failed.

## 2. Vocabulary

Terms from the sharing design (one-sign-in server, sign-in identity, bundle, pack-delivered bundle, unknown server, delivery route, canonical record) keep their meaning in its [vocabulary](mcp-auth-sharing.md#2-vocabulary). The following terms originate here and are shared through the [glossary](../glossary.md):

- **[Sign-in point](../glossary.md#sign-in-point)**: the one native server entry inside one agent at which the user's ordinary sign-in for a one-sign-in server can succeed. It is not a yolo command or a delivery route.
- **[Managed sign-in point](../glossary.md#managed-sign-in-point)**: a sign-in point yolo delivered through a selected pack. An **unmanaged** one is a bundle installed outside yolo whose entry the baseline already adopts by URL and issuer.
- **[Callback route](../glossary.md#callback-route)**: the path by which the host browser's redirect to a fixed loopback port reaches the agent listening for it.
- **[Source agent](../glossary.md#source-agent)**: an agent whose pack declares an adoptable store. At baseline that is Claude only.
- **[Usable record](../glossary.md#usable-record)**: a canonical record that is `live` and unexpired at the moment it is read.

## 3. Shipped baseline, and what it lacks

| Area | Shipped at `a97a0e055` (packet) | Gap this document closes |
|---|---|---|
| Capture | A 30-second poll adopts a Claude native entry whose URL and issuer match. It runs on Linux podman and Linux `yolo host`; the launcher runs it at boundaries on Apple Container and podman-on-Mac, and for the session on macos-user. | Nothing declares where the sign-in is made. |
| Refresh | Host-only refresh token, per-record lock, public-client refresh only. The access token is written back to the source entry. | None here. |
| Delivery | pi renders `Authorization = !yolo mcp-auth header <name>`. Claude and Codex render an ordinary remote entry. | Claude's plain `slack` entry is unauthenticated beside a working plugin entry. |
| Status | The daemon answers `op=status` without tokens, but its once rows are empty when the launch has no source. The CLI has only `header SERVER`; `status` exits 2. The handler's store constructor creates directories. | No human status, and the zero-effect property does not hold. |
| Recovery | The hint is "run /mcp in claude and authenticate {name}", with the configured name. | For Slack that sends the user to the plain entry, which cannot register. |
| Callback | No production path publishing a native callback port was found. | Unknown whether a host browser redirect reaches a jail listener. |

The motivating configuration is a user-scope `mcp_servers.slack` with `url = "https://mcp.slack.com/mcp"` and `sign_in = "once"`, without `oauth.client_id`. **Whether a credential is missing or merely undelivered is not established**; nothing here assumes either.

What public documents establish (packet, with the sharing design's Appendix C):

- Slack has no dynamic registration and requires a registered app.
- Slack's published Claude Code plugin carries a public client id and callback port 3118.
- A PKCE public client refreshes with the client id alone.
- Access tokens last 12 hours, and refresh tokens are single-use.
- Claude's `headersHelper` must print a JSON string map, has a ten-second budget, and reruns on reconnect and once after a 401 or 403. A configured `Authorization` turns off Claude's OAuth fallback.

## 4. The sign-in point

**MS-D1. A managed sign-in point is a server inside a pack-delivered bundle.** The bundle is delivered whole and unmodified, by the sharing design's existing rule ([§5](mcp-auth-sharing.md#5-which-servers-are-user-level), [OQ-MA2](mcp-auth-sharing.md#decision-ledger)).

- The bundle's own manifest is the only carrier of the client id and callback port.
- Core reads that manifest by format name, as it already does to disclose bundle code.
- Core never writes a client id into any agent's config and never starts a flow. MA-D18 holds unamended.

**Resolution is a pure decision.**

- Inputs: the launch's one-sign-in servers, the servers of every bundle the user-scope composition delivers, and each agent pack's declarations.
- Output, per one-sign-in server and agent: zero or one managed sign-in point. It carries the native name, the delivering pack, the public client id and the callback port.
- Match rule: the bundle server's URL and transport equal the one-sign-in server's.
- Tie-break: two matches in one agent resolve to the pack earlier in composition order, and status names both.

**What packs declare.** Core names no agent and no server.

- **The agent pack** that loads a bundle format supplies the native display name of a bundle server. For Claude that is the `plugin:<plugin>:<server>` form its match function already maps back.
- **The pack delivering the bundle** declares its provenance: the upstream origin and the pinned revision. Status prints it.

**Scope.**

- A sign-in point exists only for a user-scope one-sign-in server.
- It comes only from a pack the user's configuration selects.
- A workspace cannot create, redirect or remove one for the user. It can only drop the server from its own launch, as now.

**Unmanaged sign-in points keep working exactly as at baseline (MS-D2).**

- An externally installed plugin's entry is still adopted by URL and issuer, as in the sharing design's [§9.3](mcp-auth-sharing.md#93-adopting-a-sign-in-an-agent-already-has).
- Status labels it `unmanaged` once its entry exists.
- yolo does not install, modify or remove such a plugin.

**A server whose agent can register itself needs no bundle.** Where the server offers dynamic registration, or the user set their own client:

- The agent's plain rendered entry is the sign-in point, as at baseline.
- A user's `oauth.client_id` still wins, per MA-D12. It is part of the sign-in identity, so an adoption candidate whose native client id differs is refused as `client-mismatch` rather than adopted under the wrong identity.
- The second point is a **proposed tightening**. The packet found no shipped configured-client check.

**No sign-in point anywhere** is a named state, not an error at launch:

- The server stays configured, and each agent keeps its baseline behavior ([OQ-MA8](mcp-auth-sharing.md#decision-ledger)).
- Status and the launch block name the two ways forward: select a pack that delivers a supported bundle, or set the user's own `oauth.client_id`.

**Rejected alternatives.**

- A pack-authored minimal manifest that repeats a vendor's client id. yolo would then be asserting another product's identity, which MA-D18 and the sharing design's [§14](mcp-auth-sharing.md#14-alternatives-considered) forbid.
- A generic `client_id` field on `[[mcp_auth]]`, for the same reason.
- `yolo mcp-auth login`, by MS-R2.

## 5. The callback route

**Required behavior (proposed).** A managed sign-in point with a fixed callback port P gets a callback route for the life of the launch.

- A browser on the host that opens `http://localhost:P/…` reaches the agent listener inside that launch.
- The listener is host-loopback only.
- The route exists only for ports that a user-selected pack's delivered bundle declares.
- It carries inbound redirect traffic only. It is not a general port publication and grants the jail nothing outbound.

**Per backend.**

| Backend | Route |
|---|---|
| `yolo host`, macos-user | The agent listens on the host loopback already. No route is needed (inferred; Mac witness). |
| Linux podman | Host loopback P forwards to the jail's P. **The mechanism is not chosen: see MS-E1.** |
| Apple Container, podman-on-Mac | Unmeasured. Status reports `callback: unverified on this backend` until a witness passes. |

**Concurrency.**

- The agent listens on P only while a sign-in is in progress, so several launches may share one host port.
- A redirect is delivered to a launch that is listening.
- If two launches sign in to the same port at once, at most one completes. The agent's own OAuth `state` check rejects a foreign redirect, so no token reaches the wrong agent.
- The store is machine-wide for Claude, so either sign-in serves both.

**Failure.**

- If the host port is held by a foreign process, the launch continues. Status reports `callback: port P busy on the host`.
- The recovery line then names the agent's paste-the-callback-URL fallback, where the agent pack declares it has one. For Claude this is recalled from public documentation and unverified here; it needs a witness.

**MS-E1 (evidence owed before the plan fixes a mechanism).** One scout question: what in the tree publishes a host loopback port into a podman jail for a native agent listener, and does rootless networking permit it? This is engineering evidence, not an owner question. Nested podman cannot answer it.

## 6. Delivery choice, including the source agent

**A pure decision**, per agent and one-sign-in server, taken at launch.

Inputs:

- the agent pack's declared header-route support, and the helper output form it expects;
- whether this backend's launch can reach the header endpoint;
- whether the agent has a managed sign-in point for the server;
- whether a usable record exists, read through the strict read-only reader.

| Agent kind | Condition | Rendered entry |
|---|---|---|
| Not a source agent, route supported and reachable | always (baseline, pi) | Header route, own OAuth off |
| Source agent, route supported and reachable | a managed sign-in point exists in this agent, **or** a usable record exists at launch | Header route, own OAuth off. This realizes MA-D27: the header route replaces the plain entry. |
| Source agent, route supported and reachable | neither | Plain native entry, as at baseline. It is this agent's sign-in point. |
| Any | route unsupported or unreachable | Baseline plain entry ([OQ-MA8](mcp-auth-sharing.md#decision-ledger)). Status says `delivered plain`. |

**Why two conditions for a source agent.** Replacing the plain entry unconditionally would leave a self-registering server with nowhere to sign in. Never replacing it leaves Slack's dead duplicate. The two conditions cover both cases.

**Header output form (MS-D4).**

- The header command gains an explicit output-form argument that the pack's derive selects: the bare value (baseline, pi) or a JSON string map (Claude).
- Core offers both command strings in the derive context. The pack chooses, and core does not switch on the agent.
- The token is written only to the command's stdout. It never appears in an argument, an environment variable or a rendered file.
- Non-authorization headers the user configured are preserved.

**Helper time budget.**

- A header request for a still-valid token answers within the pack-declared helper budget.
- It never waits on the network past that budget.
- If a refresh attempt is still running when the budget ends, the still-valid token is returned.

**Mid-session transitions.**

- A record adopted during the session does not re-render anything. The source keeps working through writeback, and header consumers succeed on their next connection.
- A record that dies while a source agent is header-rendered with no sign-in point makes the helper fail. The failure line says to restart that agent, whose next launch renders the plain sign-in point.

**Codex is unchanged and still blocked** on native-origin proof and a measured helper. Client metadata does not supply either.

**Ship gate.** Claude's header route ships only after the sharing design's two measurements with a real sign-in ([§16 step 4](mcp-auth-sharing.md#16-what-i-would-build-in-order)): the helper reruns on reconnect or a 401, and Claude starts no OAuth for a helper-rendered server.

## 7. Token-free status

**`yolo mcp-auth status [SERVER] [--json]`** is the verb the sharing design already names in [§8.6](mcp-auth-sharing.md#86-what-the-launch-prints) and MA-D23. It is not a new ceremony.

**Effect contract.** Status performs none of these:

- a network request, OAuth discovery or a refresh;
- a browser open or an agent execution;
- starting a poll;
- creating a directory, lock or file;
- repairing a mode;
- taking an agent's store lock;
- issuing a header.

Three consequences:

- Missing or unsafe state is reported, never fixed.
- Status must not be built on the header path, the adopting broker, or any constructor that creates state.
- Status emits no access token, refresh token or secret, and no prefix or hash of one, on stdout, in JSON, in logs or in audit.

Public client ids, native server names, expiries, generations and store paths may be shown.

**Where it reads.**

- At the host, it reads the composed user scope, the selected pack declarations, the canonical record through the strict reader, an unlocked snapshot of each declared source store, and the last recorded adoption outcome.
- In a jail, it asks this launch's endpoint (`op=status`), under the same contract.
- Where no endpoint exists it says so, names running it at the host, and exits non-zero.

**The source snapshot** is classified exactly as adoption classifies entries. Only presence, expiry and whether a refresh token is present leave the reader.

**Rows.** One block per one-sign-in server:

| Layer | Values |
|---|---|
| Configuration | scope and deciding layer; identity digest (short); own client set or not |
| Sign-in point, per agent | `managed` (native name, pack, provenance, client id); `unmanaged` (native name); `plain entry`; `none` |
| Callback | `direct`; `forwarded`; `port busy`; `unverified on this backend`; `not needed` |
| Source store | `no store`; `unreadable` (reason); `no entry`; `entry awaiting adoption`; `managed entry` |
| Last adoption outcome | `adopted`; `refused: issuer-mismatch`; `refused: client-mismatch`; `refused: refresh-refused`; `refused: needs-secret`; `deferred: discovery or network`; `none recorded` |
| Record | `none`; `live` (generation, expiry, adopted from which native name); `expired`; `dead` |
| Refresher | this launch's broker; launcher session; launch-boundary only; `not determinable without effect` |
| Delivery, per agent | `header`; `native sign-in point`; `delivered plain` (why); `withheld` (why) |
| Writeback | `current`; `pending`, when the source entry is older than the record |

**Two additions to the broker (MS-D6).**

- The broker records each adoption outcome in its existing token-free audit. The baseline drops an unparsed source silently; it gains an `unreadable` outcome.
- Once rows are produced for a header-only launch with no local source.

**Diagnosis.** A pure function of the rows yields one verdict and one next step from a closed set:

- `ready`
- `not one-sign-in here` (workspace scope, or not opted in)
- `no sign-in point`
- `sign-in needed`
- `source unreadable`
- `awaiting adoption`
- `adoption refused` (with the reason)
- `record dead`
- `record expired`
- `delivered plain`
- `broker unreachable`

A record that outlives its configuration is listed as `retained, unused`.

**Exit code** is 0 whenever status could be read. It is not a health gate.

## 8. Recovery lines

- **Every line names the native server to authenticate**, taken from the sign-in point. Example: `slack: not signed in. In claude, run /mcp and authenticate plugin:slack:slack`. A line never offers the configured name when that entry cannot register.
- **No line names `yolo mcp-auth login`** (MS-R2). The two ways back are an agent sign-in at a named sign-in point, and the user's own `oauth.client_id`.
- **`delivered plain` explains the duplicate**: `slack is signed in (adopted from plugin:slack:slack); claude lists a second slack entry because <reason>; it needs no sign-in`.
- **The header command's refusal, the launch block and status use the same diagnosis.** A refusal prints to stderr only, and never a token.
- **After a rotation whose canonical write failed**, the line says a new sign-in is required and why. After a restart the same state surfaces as `refused: refresh-refused`, with the same next step.

## 9. Identity, concurrency, withdrawal, failure

- **Identity.** Shared-server authority stays composition-proven (P2). A sign-in point never widens it. Among several adoptable entries the baseline rule holds: latest sign-in wins. Status shows which native entry won. Account or team selection is not added.
- **Takeover race.** A source agent may still hold the refresh token in memory during adoption; the packet marks this unproven.
  - If the agent redeems it first, its new entry is a newer sign-in and is adopted.
  - If that redemption invalidates the broker's token, the record goes `dead` and the line asks for one sign-in.
  - No state leaves two refreshers standing (MA-D10).
- **Concurrent launches.** Resolution and delivery choice are per-launch and pure. Two launches may render differently around an adoption, and both converge at their next launch. Record locks are unchanged.
- **Withdrawal.**
  - Removing `sign_in = "once"`, the server, or the bundle's pack takes effect at the next launch. Running launches keep their frozen composition.
  - Removing the pack removes the managed sign-in point but not an adopted record.
  - A native sign-out at the origin store remains sign-out everywhere, from a cleanly parsed store only ([OQ-MA4](mcp-auth-sharing.md#decision-ledger)).
  - Revocation and `logout` remain the sharing design's and are unbuilt. This document does not add them.
- **Unreadable inputs.** A bundle manifest core cannot read yields no sign-in point and a status reason. It is never a guess. A source store that fails to parse is never read as signed out.

## 10. Compatibility

- No configuration key changes meaning. No existing grant, store or record is rewritten.
- `sign_in`, `mcp_sign_in`, own-client settings and the user-only credential keys behave as before.
- `header SERVER` with no form argument prints the bare value, as today.
- `status` changes from exit 2 to a working verb. The test that pins the old exit is updated deliberately.
- A launch with no bundle pack and an external plugin differs from today in one way only: once a usable record exists, Claude's plain entry becomes a header route.

## 11. Non-goals

- A third login command, a host-installed client, or broker-run authorization.
- A yolo-published client.
- Rendering or borrowing a vendor's client id.
- Codex capture or helper delivery.
- Mac host Keychain adoption.
- Confidential-client refresh.
- Revocation and logout.
- Account selection.
- Changing any live store or grant.

## 12. Acceptance scenarios

All fixtures are synthetic, with loopback only where an effect is under test. No agent, installer or provider call is made. Each scenario drives the production callsite named, so deleting the wiring fails it.

| # | Scenario | Passes when | Seam |
|---|---|---|---|
| A1 | A user-selected pack delivers a bundle whose server URL equals a one-sign-in server | Resolution yields one managed sign-in point with the native name, the manifest's client id and port, and the pack's provenance | composition → launch input producer |
| A2 | The same bundle comes from a `-p` pack, or the server is workspace-redefined | No sign-in point and no callback route; status says why | the same producer, plus the frozen-scope test |
| A3 | The user sets their own `oauth.client_id`, and the native entry has another client | Refused as `client-mismatch`; no record is written; no client id appears in any other agent's render | the daemon's `serve → runOnce → Pass` |
| A4 | Native first sign-in: a synthetic entry is written atomically at the sign-in point | The production poll adopts it; a second consumer's header request succeeds with an access token only; the source loses its refresh token | daemon main with injected ticks |
| A5 | Claude render with a sign-in point and no record | The plain entry is a header route with JSON form, other headers are kept, and there is no `oauth` block; the plugin entry is untouched | the derive context builder through the launcher's apply caller |
| A6 | Claude render with no sign-in point | With no record the entry is plain native; with a usable record it is a header route | as A5, with a strict-reader fixture |
| A7 | Header route unreachable on this backend | The baseline plain entry is rendered, and status shows `delivered plain` with the reason | launcher callsites for Apple Container and podman-on-Mac |
| A8 | JSON-form header | Stdout is exactly one JSON string map and the token appears nowhere else; the bare form is byte-identical to baseline | `cli.Main` |
| A9 | Header request, valid token, refresh server stalled | It answers within the declared budget with the valid token | the host header path with a blocking transport |
| A10 | Status over missing, unsafe or healthy state | No directory, lock or mode change; zero calls on a fake transport; no poll started; no fixture token bytes in stdout, JSON, logs or audit | `cli.Main` and the daemon's `op=status` |
| A11 | Status in a header-only launch with no local source | A full row set, including the record and delivery, not an empty list | the daemon's `op=status` |
| A12 | Each diagnosis state | Each fixture yields its one verdict and a next step naming the native server; none names `login` | the pure diagnosis, then `cli.Main` for wording |
| A13 | An unparsed source store | Outcome `unreadable` is recorded, with no sign-out and no rewrite | `Pass` with a torn file |
| A14 | Two brokers, one due record, a single-use refresh server | One redemption; the loser rereads | the existing record-lock seam |
| A15 | Canonical persist fails after redemption | The line demands a new sign-in; after restart the state reads `refused: refresh-refused` | the adopt path with a failing store |
| A16 | The source re-signs in after adoption | The newer entry is adopted, generation plus one, with a single refresher | `Pass` with a fake clock |
| A17 | Callback route | The launch arguments carry the forward only for a user-scope managed port; a busy port is reported and the launch proceeds | launcher argument seam (after MS-E1) |
| A18 | Withdrawal | After `once` or the pack is removed, the next launch renders baseline and status shows `retained, unused`; origin deletion signs out only from a clean parse | producer plus `Pass` |
| A19 | Existing callsite tests | The host-resident, Apple Container start, keeper and macos-user session tests still pass unchanged | existing |

**Native witnesses owed.** These are the owner's checks and are not claimed here:

- the Slack plugin, delivered by pack, signs in from a rootless podman jail through the callback route;
- Claude keeps working after its refresh token is removed;
- Claude reruns the helper on reconnect and on a 401, and starts no OAuth for a helper entry;
- Claude's paste fallback;
- Slack accepts a client-id-only refresh, and a second presenter of the token;
- the Mac file-versus-Keychain checks already listed in the sharing design.

## 13. Amendments owed to the sharing design

These are pointer edits only. No ruling changes.

- **Introduction and the MA-D27 Built cell.** "The header route is not built" is stale: it is built for pi (plan ID-22 to ID-24).
- **[§9.3 step 1](mcp-auth-sharing.md#93-adopting-a-sign-in-an-agent-already-has).** Shipped capture reads an unlocked snapshot rather than reading under the agent's lock, and it redeems public clients only.
- **[§9.2](mcp-auth-sharing.md#92-the-opt-in).** Turn-off revocation is not shipped.
- **[§9.4](mcp-auth-sharing.md#94-the-brokers-client-identity) to [§9.7](mcp-auth-sharing.md#97-refresh-sign-out-and-where-it-cannot-work) and MA-D13.** Mark `yolo mcp-auth login` withdrawn by MS-R2. Lines that name it take this document's wording.
- **[§8.6](mcp-auth-sharing.md#86-what-the-launch-prints), [§8.7](mcp-auth-sharing.md#87-audit-and-readout) and [§9.6](mcp-auth-sharing.md#96-delivery-routes).** Point at this document for status and for the source agent's delivery choice.
- **[§9.1](mcp-auth-sharing.md#91-what-the-protocol-allows) against [§9.6](mcp-auth-sharing.md#96-delivery-routes).** The two give different Codex versions for helper support. Flag it for measurement.
- **[`packs/mcp-auth/README.md`](../../packs/mcp-auth/README.md).** Its "not integrated", "no refresh" and "no command" statements are stale.

## Decision Ledger

`MS-R` rows are the owner's rulings as relayed in the 2026-11-17 brief. `MS-D` rows are engineering decisions inside them. Settled rulings carried unchanged from the sharing design: P3, MA-D10, MA-D12, MA-D18, [OQ-MA1](mcp-auth-sharing.md#decision-ledger), [OQ-MA2](mcp-auth-sharing.md#decision-ledger), [OQ-MA4](mcp-auth-sharing.md#decision-ledger), [OQ-MA7](mcp-auth-sharing.md#decision-ledger), [OQ-MA8](mcp-auth-sharing.md#decision-ledger), MA-D27 and MA-D28.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| MS-R1 | **Owner:** the normal sign-in inside a yolo-managed agent is the first credential source; capture and sharing happen behind it | 2026-11-17 | [§1](#1-requirement) | — |
| MS-R2 | **Owner:** no separately installed host client and no third yolo login ceremony. `yolo mcp-auth login` (MA-D13) is withdrawn from the path | 2026-11-17 | [§4](#4-the-sign-in-point), [§8](#8-recovery-lines) | — |
| MS-R3 | **Owner:** first-login client metadata comes through pack declarations, not a manual install or an invented registration | 2026-11-17 | [§4](#4-the-sign-in-point) | — |
| MS-R4 | **Owner:** token-free status and truthful recovery ship with the capture and delivery repair, Claude's duplicate entry included | 2026-11-17 | [§6](#6-delivery-choice-including-the-source-agent) to [§8](#8-recovery-lines) | — |
| MS-R5 | **Owner:** the own-client preference, user-only credential settings and live stores are unchanged | 2026-11-17 | [§10](#10-compatibility) | — |
| MS-D1 | A managed sign-in point is a server of a pack-delivered bundle, delivered unmodified. Its manifest alone carries the client identity; core reads, never writes | 2026-11-17 | [§4](#4-the-sign-in-point) | — |
| MS-D2 | Unmanaged sign-in points are adopted as at baseline and labelled; yolo never installs or edits them | 2026-11-17 | [§4](#4-the-sign-in-point) | — |
| MS-D3 | A source agent's plain entry becomes a header route when it has a managed sign-in point or a usable record at launch; otherwise it stays the sign-in point | 2026-11-17 | [§6](#6-delivery-choice-including-the-source-agent) | — |
| MS-D4 | The header output form is an explicit argument the pack's derive selects; bare remains the default | 2026-11-17 | [§6](#6-delivery-choice-including-the-source-agent) | — |
| MS-D5 | The callback route is host-loopback only, for user-scope managed ports, for the launch's life. Its mechanism waits on MS-E1 | 2026-11-17 | [§5](#5-the-callback-route) | — |
| MS-D6 | Status has zero effects and a closed diagnosis set. The broker records adoption outcomes, an unreadable source included | 2026-11-17 | [§7](#7-token-free-status) | — |
| MS-D7 | With the user's own `oauth.client_id` set, a native entry under another client is refused, not adopted | 2026-11-17 | [§4](#4-the-sign-in-point) | — |

## Open items (engineering; none is an owner question)

- **MS-E1.** The podman callback publication mechanism, and whether rootless networking permits it ([§5](#5-the-callback-route)).
- **MS-E2.** Whether a pack may redistribute Slack's plugin, vendored or pinned. This is a licence fact for the plan. Until it is known, the unmanaged route (MS-D2) is the working one.
- **MS-E3.** Whether the composer's reading of pack-delivered bundle servers into the scope tables is shipped or only designed. The packet does not say.