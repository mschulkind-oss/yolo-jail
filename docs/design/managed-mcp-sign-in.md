---
title: "Managed MCP sign-in: the agent's own sign-in first, then capture, delivery, status and recovery"
date: 2026-11-17
status: accepted
stage: DECIDED
next: "Complete the companion SKETCH plan against the current tree; native witnesses remain verification owed after the synthetic gate"
depends-on: [mcp-auth-sharing.md]
tags: [design, mcp, oauth, credentials, broker, packs, status, recovery]
summary: "The first credential for a one-sign-in MCP server comes from the ordinary sign-in inside a yolo-managed agent, at a sign-in point a selected pack delivers. The existing broker captures and refreshes it; the source agent's duplicate plain entry becomes a header route; a token-free, zero-effect `yolo mcp-auth status` and recovery lines say which layer failed and which native server to authenticate. No third login command, no client id invented or borrowed by core."
---

# Managed MCP sign-in: the agent's own sign-in first, then capture, delivery, status and recovery

**Status:** 2026-11-17 — proposed and unbuilt; the parent accepted the engineering closure. MS-E1, MS-E2 and MS-E3 are [closed](#engineering-closure) by MS-D8 to MS-D13; no engineering item is open. The companion plan remains a seed, not a build hand-off. Native witnesses are verification still owed, not proof of behavior.

> **In short.** The existing broker already adopts, refreshes and (for pi) delivers a one-sign-in credential. This document adds the first step it lacks, a declared place to sign in inside a managed agent. It also repairs Claude's duplicate unauthenticated entry, and makes every failure layer visible without printing or spending a token.

**Needs your ruling:** None. The owner's rulings of 2026-11-17 are recorded as MS-R1 to MS-R5; the engineering closure is recorded as MS-D8 to MS-D13.

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
| Callback | Podman bridge launches can publish a host port and forward it to a listener on the jail's loopback, for the user's `network.ports` only. No native-callback capability uses it, and nothing decides which of several jails owns a host port. Nested podman, Apple Container and podman-on-Mac lack that forward. | No declared, exclusively owned callback route. |
| Bundle | A wrapped plugin is delivered as a whole tree under `skills_tier = "namespaced"`, into the agent's skills destination. Delivery adds two ownership keys to the manifest and may rewrite its `name` to a namespaced one. Core detects that MCP components exist but reads no individual server. No pack carries Slack's plugin. The sharing design's [§5](mcp-auth-sharing.md#5-which-servers-are-user-level) claim that the composer reads bundle servers is not shipped. | No sign-in point inventory, and no managed Slack bundle. |

The motivating configuration is a user-scope `mcp_servers.slack` with `url = "https://mcp.slack.com/mcp"` and `sign_in = "once"`, without `oauth.client_id`. **Whether a credential is missing or merely undelivered is not established**; nothing here assumes either.

What public documents establish (packet, with the sharing design's Appendix C):

- Slack has no dynamic registration and requires a registered app.
- Slack's published Claude Code plugin carries a public client id and callback port 3118.
- A PKCE public client refreshes with the client id alone.
- Access tokens last 12 hours, and refresh tokens are single-use.
- Claude's `headersHelper` must print a JSON string map, has a ten-second budget, and reruns on reconnect and once after a 401 or 403. A configured `Authorization` turns off Claude's OAuth fallback.

## 4. The sign-in point

**MS-D1. A managed sign-in point is a server inside a sign-in bundle that a user-selected integration pack delivers.** A **[sign-in bundle](../glossary.md#sign-in-bundle)** is a pack-delivered bundle whose pack declares it as a place to sign in.

- The bundle's own server content is the only carrier of the client id and callback port.
- Core reads that content by format name. It never writes a client id into any agent's config and never starts a flow. MA-D18 holds unamended.

**Provisioning (MS-D8).** The managed Slack route is an integration pack. The proposed name is `slack-mcp`; no such pack ships today.

- **Contents.** The pack carries Slack's published plugin tree, pinned at upstream commit `5b8802105740b5c045c97247fded2e73b205e95e` (tag `v1.8.0`), with its MIT `LICENSE` retained. The tree is part of the pack. Nothing is fetched at launch, and no marketplace, installer or agent command is involved.
- **Selection.** On the host, the user selects it with `yolo pack add slack-mcp` (the verb edits user scope), and `yolo config check` validates it.
- **Delivery.** The next launch delivers it through the existing whole-plugin route. The agent loads it as an in-place plugin. Public Claude documentation describes that loading; it is not measured here.
- **No server contribution.** The pack contributes no `mcp_servers` entry and sets no `sign_in`. The user's configuration keeps declaring the one-sign-in server.
- **Rejected:** conditional delivery by the agent pack whenever a matching server is configured. It is a larger declaration change, and it moves a vendor's tree into a pack that does not own it.

**What delivery may change (MS-D9).**

- Delivery keeps the two shipped ownership marks, and the namespaced `name` where the configuration namespaces the pack.
- The bundle's server content is never changed. The root `.mcp.json` is byte-identical to the pinned upstream file, and a manifest's `mcpServers` value is value-identical.
- A delivery that would alter either is refused, and status reports the bundle as `not delivered` with the reason.
- "Unmodified" in earlier drafts meant this, not a byte-identical manifest.

**What the delivering pack declares.** One `[[mcp_sign_in_bundle]]` table per bundle:

| Field | Type and default | Meaning |
|---|---|---|
| `bundle` | pack-relative path; required | Root of the bundle tree. |
| `format` | string; required | Names the bundle reader. `claude-plugin` is the only value. An unknown value fails `yolo config check`. |
| `origin` | `https://` URL; required | Upstream repository. Printed by status. |
| `revision` | 40 hex characters; required | Upstream commit the tree was taken from. Printed by status. |
| `license` | pack-relative path; required | The retained licence file. A missing file fails `yolo config check`. |
| `callback_route` | boolean; default `false` | Permits the [§5](#5-the-callback-route) route for the callback port the bundle's own content names. |

**What the agent pack declares.** It declares the bundle formats its agent loads. It also supplies one pure function from the delivered manifest name and the server key to the native server name. Core names no agent and no server.

**The `claude-plugin` reader.** It is a pure read of the delivered tree.

- **Name.** It takes the plugin name from the delivered `.claude-plugin/plugin.json`, after any namespacing. It never uses the source directory or the pack name. `plugin:slack:slack` is therefore an example, not a fixed name.
- **Sources.** Servers come from the root `.mcp.json` and from the manifest's `mcpServers`, inline or as a bundle-relative file.
- **Accepted shape.** An entry with a literal `https://` `url`, a transport of `http` or `sse`, and optionally `oauth.clientId` and `oauth.callbackPort` in 1024 to 65535.
- **Unsupported shape.** An entry with headers, variable interpolation, or any other field that bears on identity is reported as `unsupported shape`. It is never a sign-in point.
- **Ambiguity.** One server key in two sources is `ambiguous`, and neither is used.
- **Confinement.** A referenced file outside the bundle root, or reached through a symlink, is refused.
- **Command servers** are ignored.
- **Separation.** The result is a sign-in point inventory and nothing else. It never enters `mcp_servers` composition, the scope tables or any render. Wider bundle sharing ([OQ-MA2](mcp-auth-sharing.md#decision-ledger)) stays the sharing design's and is unbuilt.

**Resolution is a pure decision.**

- Inputs: the launch's one-sign-in servers with each one's expected client id (empty when the user set none), the inventory of every sign-in bundle the user-scope composition delivers, and each agent pack's declarations.
- Output, per one-sign-in server and agent: zero or one managed sign-in point. It carries the native name, the delivering pack, its provenance, the public client id and the callback port.
- Match rule: the bundle server's URL and transport equal the one-sign-in server's.
- Own client: when the user's expected client id is set and the bundle server's differs, that server is not a sign-in point. Status says so.
- Tie-break: two matches in one agent resolve to the pack earlier in composition order, and status names both.

**Placed is not loaded.** Core knows the bundle was placed. It cannot know the agent loaded it, and an organization setting can block in-place plugins. Status says `placed`, never `loaded`.

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
- A user's `oauth.client_id` still wins, per MA-D12 (MS-D7).
  - **Shipped:** the launch input for a one-sign-in server carries its name, identity and URL but no expected client id, and adoption redeems under whatever client the native entry names.
  - **Proposed:** the launch input carries the expected client id. When it is set and the native entry's client id differs, adoption refuses with `refused-client-mismatch` before any discovery or redemption request, and writes no record.
  - When the user set no client id, nothing changes.

**No sign-in point anywhere** is a named state, not an error at launch:

- The server stays configured, and each agent keeps its baseline behavior ([OQ-MA8](mcp-auth-sharing.md#decision-ledger)).
- Status and the launch block name the two ways forward: select a pack that delivers a sign-in bundle for the server (for Slack, `yolo pack add slack-mcp`, then relaunch), or set the user's own `oauth.client_id`. Neither line names an install outside yolo.

**Rejected alternatives.**

- A pack-authored minimal manifest that repeats a vendor's client id. yolo would then be asserting another product's identity, which MA-D18 and the sharing design's [§14](mcp-auth-sharing.md#14-alternatives-considered) forbid.
- A generic `client_id` field on `[[mcp_auth]]`, for the same reason.
- `yolo mcp-auth login`, by MS-R2.

## 5. The callback route

**Two ways a sign-in completes.** Both are the agent's own sign-in.

- **Paste.** The agent prints its authorization URL. The user opens it in the host browser and pastes the full callback URL back into the agent's prompt. This needs no route and works on every backend. Public Claude documentation describes it, and the agent pack declares that its agent has it.
- **Callback route.** The browser's redirect reaches the agent's listener with no paste. This is a convenience on one backend, below.

No sign-in depends on the callback route existing.

**Authority.** A launch asks for a callback route for port P only when all of these hold:

- a user-selected pack's `[[mcp_sign_in_bundle]]` sets `callback_route = true`;
- that bundle's own content names P;
- the bundle server is a managed sign-in point for a user-scope one-sign-in server in this launch.

A workspace cannot request, redirect or widen one. The user's `network.ports` is a separate setting and is neither read as this authority nor changed by it.

**One owner per port (MS-D5).** A host port has one binder, so the route belongs to exactly one launch at a time. The earlier draft's claim that several launches share one port was wrong: a publication holds the host port whether or not an agent is listening.

- **[Callback lease](../glossary.md#callback-lease).** A per-user, per-port record names the launch that owns host loopback port P. Acquisition is serialized across launches.
- **Acquire.** A launch that qualifies tries to take the lease before its runtime is created. If it gets the lease, its runtime publishes `127.0.0.1:P` to the jail's port P, TCP only. It never publishes on all interfaces.
- **Second launch.** A launch that finds the lease held starts normally without the route. Its status names the owning launch.
- **Foreign holder.** If a process outside yolo holds P, the runtime cannot bind it. The launch still starts, without the route, and releases the lease. A callback port never fails a launch.
- **The user's own publication.** If this launch's `network.ports` already publishes host port P, no route is added and the lease is not taken.
- **Attach.** Attaching to a running jail never takes or moves the lease. The running owner keeps it.
- **Release.** The lease is released when the owner's runtime is confirmed gone. If liveness cannot be determined, the lease is kept. A later launch reclaims it only after confirming the recorded owner is gone.
- **IPv4 only.** The route binds `127.0.0.1`. A browser that reaches `localhost` over `::1` alone is not served. Status says `IPv4 only`, and paste covers it.

**Per backend.**

| Backend | Callback row in status | What exists |
|---|---|---|
| Linux podman, bridge network | `owner`, `held by <launch>`, or `port busy` | The route above, on the shipped publish-and-forward path. |
| Linux podman, host network or nested | `not published on this backend` | Paste. |
| Apple Container, podman-on-Mac | `not published on this backend` | Paste. This document designs no route there. |
| `yolo host`, macos-user | `not needed` | The agent listens on the host stack itself. No lease is taken. Two native sessions contending for the listener is the agent's own behavior. |

**What `owner` means.** The lease is held and the publication was created. It does not assert that a redirect will arrive.

- If the jail-side forward cannot be installed, the shipped boot warning is the disclosure. Its text gains the paste next step.
- Rootless forwarding on a real host is a witness in [§12](#12-acceptance-scenarios).

**Next steps status gives.**

- `held by <launch>`: sign in from the owning launch, or paste in this one, or relaunch after the owner exits. The first works because a bridged Claude store is machine-wide, so that sign-in serves this launch too.
- `port busy`: paste, or free the port and relaunch.
- `not published on this backend`: paste.

**Rejected.**

- A shared host gateway that routes to whichever jail is signing in. It needs a declared native lifecycle event to name the flow's owner, and none exists.
- A different port per jail. The bundle's registered redirect fixes the port, and rewriting it would alter server content (MS-D9).

## 6. Delivery choice, including the source agent

**A pure decision**, per agent and one-sign-in server, taken at launch.

Inputs:

- the agent pack's declared header-route support, and the helper output form it expects;
- whether this backend's launch can reach the header endpoint;
- whether the agent has a managed sign-in point for the server;
- whether that sign-in point is **[capturable](../glossary.md#capturable)** in this launch, meaning the agent writes sign-ins to a source store the broker adopts from here;
- whether a usable record exists, read through the strict read-only reader.

**Capturable is a shipped boundary, not a new one.**

- A bridged launch is capturable.
- A launch under the Claude credential view, or with alternate secure storage, has no adoption source. So does `yolo host` on a Mac.
- There the bundle is still placed and the agent can use it for itself, but nothing it signs in to is captured. This document adds no capture support for those modes.

| Agent kind | Condition | Rendered entry |
|---|---|---|
| Not a source agent, route supported and reachable | always (baseline, pi) | Header route, own OAuth off |
| Source agent, route supported and reachable | a capturable managed sign-in point exists in this agent, **or** a usable record exists at launch | Header route, own OAuth off. Every case variant of a configured `Authorization` header is removed. This realizes MA-D27: the header route replaces the plain entry. |
| Source agent, route supported and reachable | neither | Plain native entry, as at baseline. It is this agent's sign-in point. |
| Any | route unsupported or unreachable | Baseline plain entry ([OQ-MA8](mcp-auth-sharing.md#decision-ledger)). Status says `delivered plain`. |

**Why two conditions for a source agent.** Replacing the plain entry unconditionally would leave a self-registering server with nowhere to sign in. Never replacing it leaves Slack's dead duplicate. The two conditions cover both cases.

**Header output form (MS-D4).**

- The header command gains an explicit output-form argument that the pack's derive selects: the bare value (baseline, pi) or a JSON string map (Claude).
- Core offers both command strings in the derive context. The pack chooses, and core does not switch on the agent.
- The token is written only to the command's stdout. It never appears in an argument, an environment variable or a rendered file.
- Non-authorization headers the user configured are preserved.

**Header issuance budget (MS-D10).** Claude gives a helper ten seconds end to end.

- **Shipped:** the header command and the daemon allow 30 seconds. A header request with under five minutes left attempts a refresh; lock waits alone can take ten seconds, and source writeback two more.
- **Proposed:** the rules below. They apply to both output forms.

Rules:

- **Total deadline: 8 seconds**, measured by the header command from its start. At the deadline it exits non-zero with a token-free diagnosis on stderr.
- **Unexpired token.** It is returned at once. The header path makes no provider request, does not wait on a refresh lock, and does not write back to a source store. Refresh ahead of expiry belongs to the poll alone.
- **Canonical read.** The header path waits at most 2 seconds for the canonical lock. Past that it refuses as `busy`.
- **Expired token on a live record, in a jail.** The endpoint asks this launch's poll owner for one immediate pass on that record and waits at most 5 seconds for a fresh token.
  - The pass runs in the daemon to its durable commit whether or not the requester is still waiting, so a rotated token is never abandoned.
  - One pass per record runs at a time, under the record lock.
  - A request bypasses transient backoff at most once per 30 seconds. It never retries a definitively refused token.
- **Expired token on a live record, at the host.** The header command never redeems, because it is a short-lived process that cannot guarantee the commit. It refuses as `record expired`. The session's own poll refreshes within its 30-second interval.
- **Dead or missing record.** Refused. An expired token is never returned.

**Mid-session transitions.**

- A record adopted during the session does not re-render anything. The source keeps working through writeback, and header consumers succeed on their next connection.
- A record that dies while a source agent is header-rendered with no sign-in point makes the helper fail. The failure line says to restart that agent, whose next launch renders the plain sign-in point.

**Codex is unchanged and still blocked** on native-origin proof and a measured helper. Client metadata does not supply either.

**Two gates, kept apart.**

- **Synthetic gate.** It is account-free and must pass before the route lands: the scenarios in [§12](#12-acceptance-scenarios). The local implementation is not blocked on a real sign-in.
- **Native validation.** It is owed after landing. The sharing design's two measurements ([§16 step 4](mcp-auth-sharing.md#16-what-i-would-build-in-order)) are that the helper reruns on reconnect or a 401, and that Claude starts no OAuth for a helper-rendered server. Public Claude documentation states both. Neither is measured on an installed version.
- **Why the route may land before the measurement.** The entry it replaces is, for Slack, one that cannot sign in at all. A wrong helper assumption therefore leaves the user no worse off than baseline.
- **If a measurement fails.** The Claude pack withdraws its header-route declaration, and the baseline plain entry returns at the next launch.
- **Reporting.** Until the measurements pass, the Built column reads "synthetic gate only". Nothing reports the route as working against a provider.

## 7. Token-free status

**`yolo mcp-auth status [SERVER] [--json]`** is the verb the sharing design already names in [§8.6](mcp-auth-sharing.md#86-what-the-launch-prints) and MA-D23. It is not a new ceremony.

**Effect contract.** Status performs none of these:

- a request to any provider or any other egress, OAuth discovery or a refresh;
- staging, materializing or resolving a pack in any way that writes;
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

- **At the host**, it reads:
  - the composed user scope;
  - pack declarations and delivered bundles that are already staged;
  - the canonical record through the strict reader, over a store description that creates nothing;
  - an unlocked snapshot of each declared source store;
  - the outcome note and the callback lease, both as plain file reads.
- **Packs not staged.** The sign-in point rows read `not determinable without effect`, with the next step: launch once, or run status inside a jail.
- **yolo's own lock.** The strict reader takes yolo's canonical lock on an existing lock file for the read, waiting at most 2 seconds, then reports `busy`. It never creates the lock. This is the one lock status takes; it is not an agent's store lock.
- **In a jail**, it asks this launch's endpoint (`op=status`), under the same contract. That exchange is an authenticated local read on the launch's own loopback endpoint and is the read itself. It is not the provider or egress traffic the contract forbids.
- Where no endpoint exists it says so, names running it at the host, and exits non-zero.

**The source snapshot** is classified exactly as adoption classifies entries. Only presence, expiry and whether a refresh token is present leave the reader.

**Rows.** One block per one-sign-in server:

| Layer | Values |
|---|---|
| Configuration | scope and deciding layer; identity digest (short); own client set or not |
| Sign-in point, per agent | `managed, placed` (native name, pack, origin, revision, client id); `bundle not usable` (`not delivered`, `unsupported shape`, `ambiguous`, or `client differs from yours`); `unmanaged` (native name); `plain entry`; `none`; `not determinable without effect` |
| Callback | `owner` (IPv4 only); `held by <launch>`; `port busy`; `not published on this backend`; `not needed`; `not requested` |
| Source store | `not capturable here` (credential view, alternate storage, or Mac host); `no store`; `malformed`; `unsafe` (mode or path); `no entry`; `entry awaiting adoption`; `managed entry` |
| Last adoption outcome | `adopted`; `refused-issuer-mismatch`; `refused-client-mismatch`; `refused-by-server`; `unsupported-token-type`; `rotated-not-saved`; `deferred-network`; `source-malformed`; `source-unsafe`; `none recorded` |
| Record | `none`; `live` (generation, expiry, adopted from which native name); `expired`; `dead` |
| Refresher | this launch's broker; launcher session; launch-boundary only; `not determinable without effect` |
| Delivery, per agent | `header`; `native sign-in point`; `delivered plain` (why); `withheld` (why) |
| Writeback | `current`; `pending`, when the source entry is older than the record |

**Two additions to the broker (MS-D6).**

- **Outcome note.** The shipped audit event carries only an action, a server and a generation, so it cannot carry a reason. The broker instead keeps one **[outcome note](../glossary.md#outcome-note)** per approved sign-in identity, in host-only state beside the canonical store.
  - **Fields:** schema version, server name, identity digest, native entry name, one outcome code from the closed set above, the time, and the generation.
  - **Never held:** a token, a hash of one, or any provider response text.
  - **Writers.** Only an adopting or refreshing pass writes it, by atomic replace, while it holds the record's refresh lock. Status never writes it.
  - **Write failure.** It is logged and never fails the pass.
  - **Lifetime.** It survives a restart, which the shipped in-memory refusal memory does not.
- **Codes mean what was observed.**
  - `refused-by-server` is a definitive refusal of the refresh. It does not guess whether a secret was needed.
  - `rotated-not-saved` is written only when a redemption succeeded and the canonical write then failed.
  - If the note cannot be written either, nothing is invented. After a restart the spent token is tried once, and only the server's actual refusal is then recorded.
- **Unparsed source.** The shipped pass drops it silently. It gains `source-malformed` and `source-unsafe`, with no sign-out and no rewrite.
- **Header-only launches.** Once rows are produced for a header-only launch with no local source.

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
- `not capturable here`
- `sign-in lost` (from `rotated-not-saved`)
- `busy`

The callback row never changes the verdict. It only selects the next step's wording between the redirect and paste.

A record that outlives its configuration is listed as `retained, unused`.

**Exit code** is 0 whenever status could be read. It is not a health gate.

## 8. Recovery lines

- **Every line names the native server to authenticate**, resolved from the delivered bundle. The name is never assumed.
  - Example: `slack: not signed in. In claude, run /mcp and authenticate <resolved native name>`.
  - Where no callback route is owned, the line adds: `if the browser cannot reach the agent, paste the full callback URL into its prompt`.
  - A line never offers the configured name when that entry cannot register.
- **`no sign-in point` names provisioning**: `slack: no agent here can sign in. Run: yolo pack add slack-mcp, then relaunch`. It names the user's own `oauth.client_id` as the alternative.
- **`not capturable here` names the mode**: `slack: a sign-in made in this launch is not shared (credential view). Sign in from a bridged managed claude`.
- **No line names `yolo mcp-auth login`** (MS-R2). The two ways back are an agent sign-in at a named sign-in point, and the user's own `oauth.client_id`.
- **`delivered plain` explains the duplicate**: `slack is signed in (adopted from plugin:slack:slack); claude lists a second slack entry because <reason>; it needs no sign-in`.
- **The header command's refusal, the launch block and status use the same diagnosis.** A refusal prints to stderr only, and never a token.
- **After a rotation whose canonical write failed**, the line says a new sign-in is required and why (`sign-in lost`).
  - That reason persists across a restart only when the outcome note was written.
  - Otherwise status reports `refused-by-server`, once the server has actually refused the spent token.
  - The next step is the same in both cases.

## 9. Identity, concurrency, withdrawal, failure

- **Identity.** Shared-server authority stays composition-proven (P2). A sign-in point never widens it. Among several adoptable entries the baseline rule holds: latest sign-in wins. Status shows which native entry won. Account or team selection is not added.
- **Takeover race.** A source agent may still hold the refresh token in memory during adoption; the packet marks this unproven.
  - If the agent redeems it first, its new entry is a newer sign-in and is adopted.
  - If that redemption invalidates the broker's token, the record goes `dead` and the line asks for one sign-in.
  - No state leaves two refreshers standing (MA-D10).
- **Concurrent launches.** Resolution and delivery choice are per-launch and pure. Two launches may render differently around an adoption, and both converge at their next launch. Record locks are unchanged. The callback lease gives one launch the route; every other launch runs without it and says so ([§5](#5-the-callback-route)).
- **One refresher is the rule, not a proven fact.** A source agent that keeps a refresh token in memory can still compete once. The outcomes above bound that case; a native witness measures it.
- **Withdrawal.**
  - Removing `sign_in = "once"`, the server, or the bundle's pack takes effect at the next launch. Running launches keep their frozen composition.
  - Removing the pack removes the managed sign-in point but not an adopted record.
  - A native sign-out at the origin store remains sign-out everywhere, from a cleanly parsed store only ([OQ-MA4](mcp-auth-sharing.md#decision-ledger)).
  - Revocation and `logout` remain the sharing design's and are unbuilt. This document does not add them.
- **Unreadable inputs.** A bundle manifest core cannot read yields no sign-in point and a status reason. It is never a guess. A source store that fails to parse is never read as signed out.

## 10. Compatibility

- No configuration key changes meaning. No existing grant, store or record is rewritten.
- `sign_in`, `mcp_sign_in`, own-client settings and the user-only credential keys behave as before.
- `header SERVER` with no form argument prints the bare value, as today. Two behaviors change for both forms, by MS-D10:
  - the total deadline drops from 30 to 8 seconds;
  - a header request no longer redeems a refresh token for an unexpired record, nor at the host.
- A launch that selects a sign-in bundle with `callback_route = true` may bind one host loopback port. No launch does so without that selection.
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
- A callback route on Apple Container, podman-on-Mac or nested podman, an IPv6 route, or simultaneous routes for several jails on one port.
- Capturing a sign-in made under the credential view or alternate secure storage.
- Reading bundle servers into `mcp_servers` composition or sharing them generally.
- Fetching or updating a bundle at launch.
- Changing any live store or grant.

## 12. Acceptance scenarios

All fixtures are synthetic, with loopback only where an effect is under test. No agent, installer or provider call is made. Each scenario drives the production callsite named, so deleting the wiring fails it.

| # | Scenario | Passes when | Seam |
|---|---|---|---|
| A1 | A user-selected pack declares and delivers a sign-in bundle whose server URL equals a one-sign-in server, once plain and once namespaced | Resolution yields one managed sign-in point. Its native name comes from the delivered manifest name in both cases, with the bundle's client id and port and the pack's origin and revision | composition → whole-plugin delivery → launch input producer |
| A2 | The same bundle comes from a `-p` pack, or the server is workspace-redefined | No sign-in point, no lease and no publication; status says why | the same producer, plus the frozen-scope test |
| A3 | The user sets their own `oauth.client_id`; the native entry, and the bundle, name another client | The bundle is not a sign-in point. Adoption is refused as `refused-client-mismatch` with zero transport calls. No record is written. No client id appears in any other agent's render | the daemon's `serve → runOnce → Pass` with a counting transport |
| A4 | Native first sign-in: a synthetic entry is written atomically at the sign-in point | The production poll adopts it; a second consumer's header request succeeds with an access token only; the source loses its refresh token | daemon main with injected ticks |
| A5 | Claude render with a capturable sign-in point and no record | The plain entry is a header route with JSON form, other headers are kept, and there is no `oauth` block; the plugin entry is untouched | the derive context builder through the launcher's apply caller |
| A6 | Claude render with no capturable sign-in point | With no record the entry is plain native; with a usable record it is a header route | as A5, with a strict-reader fixture |
| A7 | Header route unreachable on this backend | The baseline plain entry is rendered, and status shows `delivered plain` with the reason | launcher callsites for Apple Container and podman-on-Mac |
| A8 | JSON-form header | Stdout is exactly one JSON string map and the token appears nowhere else; the bare form is byte-identical to baseline | `cli.Main` |
| A9 | Header request with an unexpired token while the provider transport is blocked on a channel | The token is returned with zero transport calls and no refresh-lock wait, in jail and at host. Asserted by counters and injected deadlines, not elapsed time | `cli.Main` and the daemon header handler |
| A10 | Status over missing, unsafe or healthy state | No directory, lock or mode change; zero calls on a fake transport; no poll started; no fixture token bytes in stdout, JSON, logs or audit | `cli.Main` and the daemon's `op=status` |
| A11 | Status in a header-only launch with no local source | A full row set, including the record and delivery, not an empty list | the daemon's `op=status` |
| A12 | Each diagnosis state | Each fixture yields its one verdict and a next step naming the native server; none names `login` | the pure diagnosis, then `cli.Main` for wording |
| A13 | An unparsed source store | Outcome `unreadable` is recorded, with no sign-out and no rewrite | `Pass` with a torn file |
| A14 | Two brokers, one due record, a single-use refresh server | One redemption; the loser rereads | the existing record-lock seam |
| A15 | Canonical persist fails after redemption, once with the outcome note writable and once not | Writable: the note reads `rotated-not-saved` and survives a restart. Not writable: nothing is recorded until the fake server refuses the spent token, then `refused-by-server`. Both lines demand a new sign-in | the adopt path with a failing store, then a second broker instance |
| A16 | The source re-signs in after adoption | The newer entry is adopted, generation plus one, with a single refresher | `Pass` with a fake clock |
| A17 | Callback route, bridge podman | The runtime arguments carry a `127.0.0.1`-bound publication of P and the jail-side forward entry only for the lease owner, only with `callback_route = true` on a user-selected pack and a matching user-scope server. They never carry an all-interface bind, and never arise from a workspace setting | the run-command assembly callsite |
| A20 | Two launches qualify for one port | The first owns the lease and publishes. The second starts without a publication and its status reads `held by <first>`. An attach to the first changes nothing | the launcher with a lease fixture |
| A21 | A foreign process holds the port, or this launch's `network.ports` publishes it | The launch starts, usable, without the route. The lease is not left held. Status reads `port busy` | the launcher create path with a failing bind |
| A22 | The owner exits; then an owner of unknown liveness | Confirmed gone: the lease is released and the next launch owns it. Unknown: the lease is kept and the next launch reads `held by` | the keeper's confirmed-gone callsite |
| A23 | Host-network or nested podman, Apple Container, podman-on-Mac, macos-user, `yolo host` | No lease and no publication. The status row is `not published on this backend` or `not needed`, as in [§5](#5-the-callback-route) | each backend's launch callsite |
| A24 | Bundle reader over the default `.mcp.json`, an inline manifest entry, a file reference, a duplicate key, a path escape, an entry with headers, and a command server | Only the accepted shape yields a server. Each other case yields its named reason and no sign-in point | the reader, then the launch input producer |
| A25 | Whole-plugin delivery of a sign-in bundle | The ownership marks and any namespaced name are present. `.mcp.json` is byte-identical and `mcpServers` value-identical. A delivery that would change them is refused | the layer writer's plugin delivery |
| A26 | A managed sign-in point under the credential view | The plain entry is not replaced on the sign-in point's account. Status reads `not capturable here`. No source is polled | the launch input producer and the derive context builder |
| A27 | Header request with an expired token on a live record | In a jail: one immediate pass, a wait of at most the injected 5-second budget, and the commit completes after the requester gives up. At the host: refused with no redemption. A dead record is refused in both | the daemon header handler and `cli.Main` |
| A28 | Status with packs not staged, and with a missing canonical directory | Nothing is staged or created. The sign-in point rows read `not determinable without effect` and the record row reads `none` | `cli.Main` with a write-counting filesystem |
| A29 | Outcome note | Each outcome code is written under the record lock, holds no fixture token bytes or provider text, and is read back by status after a broker restart | `Pass`, then status |
| A18 | Withdrawal | After `once` or the pack is removed, the next launch renders baseline and status shows `retained, unused`; origin deletion signs out only from a clean parse | producer plus `Pass` |
| A19 | Existing callsite tests | The host-resident, Apple Container start, keeper and macos-user session tests still pass unchanged | existing |

**Native witnesses owed.** These are the owner's checks and are not claimed here:

- Claude loads the placed bundle and shows the resolved native name;
- the Slack plugin, delivered by pack, signs in from a rootless podman jail through the callback route, with the host's rootless and network facts recorded. Nested podman cannot stand in for this;
- a browser that prefers `::1` for `localhost` still completes, or the IPv4-only limit is confirmed;
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
- **[§5](mcp-auth-sharing.md#5-which-servers-are-user-level), "Two things are built for it".** Neither is shipped as written. Bundle placement and disclosure are shipped. Reading individual bundle servers into the scope tables is not.
- **[§9.6](mcp-auth-sharing.md#96-delivery-routes).** Point at this document for the header command's refresh behavior: it no longer refreshes under five minutes for an unexpired record (MS-D10).
- **[§16 step 4](mcp-auth-sharing.md#16-what-i-would-build-in-order).** Point at this document's two gates for Claude's route.

## Decision Ledger

`MS-R` rows are the owner's rulings as relayed in the 2026-11-17 brief. `MS-D` rows are engineering decisions inside them. Settled rulings carried unchanged from the sharing design: P3, MA-D10, MA-D12, MA-D18, [OQ-MA1](mcp-auth-sharing.md#decision-ledger), [OQ-MA2](mcp-auth-sharing.md#decision-ledger), [OQ-MA4](mcp-auth-sharing.md#decision-ledger), [OQ-MA7](mcp-auth-sharing.md#decision-ledger), [OQ-MA8](mcp-auth-sharing.md#decision-ledger), MA-D27 and MA-D28.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| MS-R1 | **Owner:** the normal sign-in inside a yolo-managed agent is the first credential source; capture and sharing happen behind it | 2026-11-17 | [§1](#1-requirement) | — |
| MS-R2 | **Owner:** no separately installed host client and no third yolo login ceremony. `yolo mcp-auth login` (MA-D13) is withdrawn from the path | 2026-11-17 | [§4](#4-the-sign-in-point), [§8](#8-recovery-lines) | — |
| MS-R3 | **Owner:** first-login client metadata comes through pack declarations, not a manual install or an invented registration | 2026-11-17 | [§4](#4-the-sign-in-point) | — |
| MS-R4 | **Owner:** token-free status and truthful recovery ship with the capture and delivery repair, Claude's duplicate entry included | 2026-11-17 | [§6](#6-delivery-choice-including-the-source-agent) to [§8](#8-recovery-lines) | — |
| MS-R5 | **Owner:** the own-client preference, user-only credential settings and live stores are unchanged | 2026-11-17 | [§10](#10-compatibility) | — |
| MS-D1 | A managed sign-in point is a server of a sign-in bundle that a user-selected integration pack declares and delivers. The bundle's own server content alone carries the client identity; core reads, never writes (amended by the closure: see MS-D8 and MS-D9) | 2026-11-17 | [§4](#4-the-sign-in-point) | — |
| MS-D2 | Unmanaged sign-in points are adopted as at baseline and labelled; yolo never installs or edits them | 2026-11-17 | [§4](#4-the-sign-in-point) | — |
| MS-D3 | A source agent's plain entry becomes a header route when it has a managed sign-in point or a usable record at launch; otherwise it stays the sign-in point | 2026-11-17 | [§6](#6-delivery-choice-including-the-source-agent) | — |
| MS-D4 | The header output form is an explicit argument the pack's derive selects; bare remains the default | 2026-11-17 | [§6](#6-delivery-choice-including-the-source-agent) | — |
| MS-D5 | The callback route has one owner per host port, by a per-user, per-port lease. It is an IPv4 loopback publication on bridge podman only. A second launch, a foreign holder and every other backend run without it and say so; paste completes the same sign-in. Closes MS-E1 | 2026-11-17 | [§5](#5-the-callback-route) | — |
| MS-D6 | Status has zero effects and a closed diagnosis set. It makes no provider request and stages nothing; a jail's exchange with its own endpoint is the read. The broker keeps a token-free outcome note per identity | 2026-11-17 | [§7](#7-token-free-status) | — |
| MS-D7 | The launch input carries the user's expected client id. A native entry or bundle server under another client is refused before any provider request, and is not a sign-in point | 2026-11-17 | [§4](#4-the-sign-in-point) | — |
| MS-D8 | Managed Slack provisioning is an integration pack (proposed `slack-mcp`) that carries Slack's MIT plugin pinned at `5b880210`, selected by the user and delivered by the shipped whole-plugin route. No fetch at launch, no installer, no server contribution. Closes MS-E2 | 2026-11-17 | [§4](#4-the-sign-in-point) | — |
| MS-D9 | Delivery keeps the shipped ownership marks and namespaced name. Server content is never altered. The native name comes from the delivered manifest | 2026-11-17 | [§4](#4-the-sign-in-point) | — |
| MS-D10 | Header issuance has an 8-second total deadline. An unexpired token is returned with no refresh. An expired one triggers one daemon pass of at most 5 seconds in a jail and is refused at the host. An expired token is never returned | 2026-11-17 | [§6](#6-delivery-choice-including-the-source-agent) | — |
| MS-D11 | A sign-in point inventory is read from delivered sign-in bundles by a format-named reader and kept apart from `mcp_servers` composition. Closes MS-E3 | 2026-11-17 | [§4](#4-the-sign-in-point) | — |
| MS-D12 | A sign-in point counts toward replacing the plain entry only when it is capturable. Credential view, alternate storage and Mac host are reported as `not capturable here` and gain no capture support | 2026-11-17 | [§6](#6-delivery-choice-including-the-source-agent) | — |
| MS-D13 | The synthetic gate precedes landing. Native measurements are validation owed afterward; a failed one withdraws the pack's route declaration. Nothing reports provider success before they pass | 2026-11-17 | [§6](#6-delivery-choice-including-the-source-agent) | — |

## Engineering closure

Closed 2026-11-17 from the Fable-authored source-read closure at `a97a0e055`, accepted by the parent. No engineering item is open. Source/public evidence below is not native behavioral verification.

- **MS-E1, closed by MS-D5.**
  - **Found:** podman bridge launches already [publish a host port](../../internal/cli/run/assemble.go#L730-L760) and [forward it to the jail's loopback](../../internal/entrypoint/runtime.go). Nothing owned a port across jails.
  - **Decided:** an exclusive lease, with paste as the route-free path.
  - **Validation owed:** rootless forwarding on a real host.
- **MS-E2, closed by MS-D8.**
  - **Found:** Slack's plugin is MIT-licensed ([pinned licence](https://github.com/slackapi/slack-skills-plugin/blob/5b8802105740b5c045c97247fded2e73b205e95e/LICENSE)). Its tag `v1.8.0` and `main` both resolved to commit `5b8802105740b5c045c97247fded2e73b205e95e` in the closure evidence; those moving refs are not the redistribution pin.
  - **Decided:** an integration pack carries it with the licence retained. The unmanaged route is no longer described as the working one; it remains only as unchanged baseline behavior (MS-D2).
- **MS-E3, closed by MS-D9 and MS-D11.**
  - **Found:** whole-plugin placement and disclosure are shipped ([plugin recognition](../../internal/pluginpack/pluginpack.go)), and [delivery marks the manifest](../../internal/hostskills/plugin.go). Reading individual servers is not shipped.
  - **Decided:** a format-named reader feeding a separate sign-in point inventory.