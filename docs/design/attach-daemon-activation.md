---
title: "A later profile can start its daemon without restarting the jail"
status: in-review
stage: DESIGN
next: "Rule OQ-AD1 on the wire-bridge scope, then prepare the implementation plan against this design"
tags: [attach, profiles, daemons, lifetime]
summary: "Add profile-served daemons to a running jail through its existing supervisor, without changing its packs, grants, mounts or serving routes."
---

# A later profile can start its daemon without restarting the jail

**Status:** 2026-10-08. Nothing built. Source inspected at `d50a833d9`; no runtime measurement.

> **In short.** Yolo manages the environment a session enters: choosing a profile later should start an already-authorized daemon, not end the other sessions sharing that environment.

**Why it matters.** The reported `yolo -p bedrock -- pi -c` meets a restart prompt when the jail started without any Bedrock selection.

**The shape.** The existing supervisor admits named additions from a launch-frozen declaration set; the launcher publishes their identities before requesting startup.

**Cost.** A new host–jail contract, a bounded attach transaction and entry-specific environment files; old running binaries cannot acquire this capability in place.

**Start at [§3](#3-admit-an-addition-not-a-new-environment)** — the authorization boundary.

**Needs your ruling:** [OQ-AD1](#OQ-AD1).

**Reads with:** [providers](../reference/providers.md#the-credential-gate) (selection and credential scope), [wire bridge](../reference/wire-bridge.md) (route ownership), [jail lifetime](jail-lifetime-last-session-wins.md) (sessions and keeper), [attach guardrails](attach-skew-and-contract-guardrails.md) (older contracts).

---

## 1. Recommendation and unchanged authority

My recommendation is **additive, on-demand activation by the existing in-jail supervisor on container backends**. No AWS-name switch in core, no adapter spawned by the attaching terminal, and no replacement supervisor beside a live one.

1. **P1 — Activation is not authorization.** The running jail's immutable pack tree, enabled loopholes, mounts, host grants and keeper-owned service fronts remain the ceiling. A new selection cannot add a missing prerequisite.
2. **P2 — Publish before starting; ready before executing.** Daemon and client must receive the same settled address and caller token. A process being alive is not readiness.
3. **P3 — Add, never replace.** An existing daemon's command, identity and serving configuration remain fixed. Neither an attach nor a deselection stops it under another session.
4. **P4 — A refused entry is not a refused jail.** Failure returns to the attaching terminal; sibling processes, listeners and the keeper remain untouched.
5. **P5 — Unknown is not missing or ready.** Failed inspection, missing control replies and an uncounted session cannot justify starting a second supervisor or claiming success.

The owner request changes the *fresh-launch-only* implementation of [OQ-CN7](../reference/providers.md#oq-cn7), not its credential-scoping ruling. A selected profile still triggers the daemon; its scoped token still reaches only selecting agents. [OQ-PK2](../reference/pack-system.md#oq-pk2)'s immutable pack tree and [WB-D18](../reference/wire-bridge.md#wb-d18)'s token authentication remain intact.

### What this does not license

- No pack re-staging, mounting into a running jail, host-daemon startup/restart, changed narrowing settings, new host-file grant or credential widening.
- No native keeper additions: a missing `macos-user` host doorway retains its existing refusal/skew behavior; container activation cannot supply it.
- No unconditional startup of every dormant adapter, runtime agent/API test, account-wide termination or automatic restart to repair activation.
- No hot replacement of an existing wire-bridge route. Broader bridge coexistence is [OQ-AD1](#OQ-AD1), not an implicit part of daemon activation.
- No claim of same-uid credential isolation: owner-only files are still readable by other processes of that jail's uid, as the [credential gate](../reference/providers.md#the-credential-gate) already states.
- No general isolation of agent homes or pack-rendered configuration files. This design isolates entry environments and daemon identities, not every file an agent may re-read.

## 2. What exists, and exactly where it stops

Verified on 2026-10-08 against the source, rather than the earlier research's line numbers:

| Existing behavior | Evidence |
| :--- | :--- |
| A daemon is profile-served when every pack env contribution naming it through `served_by` is gated by profile or provider platform. Any ungated contribution removes that classification. Selection decides its inclusion, not an AWS spelling in core. | [`profileserved.go`](../../internal/packload/profileserved.go), `profileServedDaemons`, `UnselectedProfileServedDaemons`; [`packservices.go`](../../internal/cli/run/packservices.go), `jailDaemonsFor` |
| Attach composes over the running pack tree. It compares desired profile-served daemons with frozen `YOLO_JAIL_DAEMONS`; a missing name takes the restart/refuse/acknowledge disposition. | [`run.go`](../../internal/cli/run/run.go#L2688-L2802), `attachExisting`; [`profiledaemons.go`](../../internal/cli/run/profiledaemons.go); [`contracttags.go`](../../internal/cli/run/contracttags.go#L375-L475) |
| Attach adopts running tokens and addresses. Tokens not selected by this entry remain unexported records, so a later selection can reuse them. It never allocates a new attach address today. | [`callertokens.go`](../../internal/cli/run/callertokens.go), `rekeyChannelForAttach`; [`servedaddresses.go`](../../internal/cli/run/servedaddresses.go) |
| Shared channel and per-agent env files are live-mounted and rewritten together by one composition. The launcher currently releases the launch lock **before** this delivery; another attach can overwrite them before the first entry reads them. | [`run.go`](../../internal/cli/run/run.go#L2850-L3015); [`agentenvfiles.go`](../../internal/cli/run/agentenvfiles.go), `deliverChannel` |
| An entrypoint reuses any live supervisor without extending its daemon set. `supervise` reads its environment once; no add/status control protocol exists in its dispatch. | [`runtime.go`](../../internal/entrypoint/runtime.go), `startJailDaemonSupervisor`; [`supervisorcmd.go`](../../internal/supervisor/supervisorcmd.go); [`main.go`](../../cmd/yolo-jaild/main.go) |
| AWS's adapter captures its scoped token at startup, then binds its listener. It does **not** emit the existing daemon-ready pipe message. | [`handler.go`](../../internal/awscredadapter/handler.go), `callerTokenLookup`, `Main` |
| A live idle wire bridge polls the entry channel and can wake on attach. Once serving, its plan stays fixed; it does not reload its routes. | [`boot.go`](../../internal/wirebridged/boot.go#L140-L230), `run`, `waitForActivePlan` |
| The session is counted under the launch lock before exec. Hangup targets that session's process tree and Unix session; daemons created as its descendants would therefore be the wrong lifetime. | [`sessionlock.go`](../../internal/cli/run/sessionlock.go); [`sessionhangup.go`](../../internal/entrypoint/sessionhangup.go) |
| At `macos-user`, confined guest supervision remains per invocation, but the workspace keeper starts and rosters host services and doorways, then holds them until the last session leaves. A joining launch starts none; a missing roster doorway takes the existing refusal/skew disposition. | [`run.go`](../../internal/cli/run/run.go#L698-L735); [`keeper.go`](../../internal/cli/run/keeper.go#L1030-L1126), `holdKey`; [`keeperspawn.go`](../../internal/cli/run/keeperspawn.go#L1368-L1444), `joinMacosUserKeeper`, `macosUserKeeperLacks`, `judgeKeeperLacks`; [`jaildaemon.go`](../../internal/macosuser/jaildaemon.go#L515-L597), `startJailDaemons` |

For the motivating AWS case **on a container backend admitting AWS**, the enabled loophole's host service/front is already independent of the profile gate. The profile gate omits the jail adapter, not the host half: compare `jailDaemonsFor` above with [`startPlannedLoopholes`](../../internal/cli/run/packloopholes.go) and [the AWS manifest](../../packs/aws-auth/loopholes/aws-auth/manifest.jsonc). If that front was never admitted, has died or is inaccessible, this proposal cannot create or repair it. That distinction does not authorize a later native doorway: its separate shared-keeper constraint remains as [§8](#8-support-and-old-running-jails) describes.

### Terms used below

- **Session** follows [jail lifetime's definition](jail-lifetime-last-session-wins.md#11-terms): one yolo invocation and the command tree it runs, not the container or a daemon.
- **Profile-served daemon** follows the selection behavior in [the credential gate](../reference/providers.md#oq-cn7); the structural classification above was coined in the pack loader. It is not every pack service or every daemon of a selected pack.
- **Scoped caller token** follows [the credential gate](../reference/providers.md#the-credential-gate): the daemon's authentication secret exported only to selecting agents, not a cloud credential or a new grant.
- **Served address** follows [the address contract](../plans/notch-convergence.md#24-the-addresses-those-secrets-protect-are-composed-not-literal): the loopback host and port actually used for this jail, not necessarily the manifest's declared port.
- **Entry snapshot** *(coined here)* is one immutable set of this entry's shared channel and per-agent env/files. It is not a copy of the pack tree or a private agent home.

## 3. Admit an addition, not a new environment

At a fresh launch, freeze two sets separately:

| Set | Meaning |
| :--- | :--- |
| Initial daemon set | The existing selected payload, which starts at boot as today |
| Eligible additions | Profile-served daemons omitted solely because no selected agent satisfied their gate, but whose other launch prerequisites are already present |

Eligible additions retain their resolved command, restart policy, declared listen address and caller-token requirement from the **same existing declarations**. They must pass the same runtime split and admission rules as an initial daemon. Their executable and declared paths must already be mounted; their required enabled loophole/front must belong to this jail's launch. Freeze the relevant launch settings and network disposition too. An absent gate, unsupported runtime or missing prerequisite contributes no eligible addition. An empty eligible set is valid and needs no idle control supervisor.

This adds a host–jail protocol, **not a new pack manifest field**. The new strict control request names daemon identities and an entry snapshot, never a shell command or replacement manifest. The supervisor accepts only names in its launch-frozen set. Duplicate identical names collapse; a conflicting duplicate, unknown name, stale jail instance or changed command rejects the whole request before starting anything.

A fresh jail with eligible additions keeps one supervisor available even when its initial daemon set is empty. It binds an owner-only Unix control socket under the jail's existing writable runtime directory, not on TCP and not in a host mount. Boot and control startup share an in-jail single-writer lock; a PID file alone is insufficient. Status returns the jail instance, protocol version and each daemon's current process/readiness state.

The socket is no privilege boundary against the jail's own uid. It exposes no new host authority: requests can only run frozen, already-authorized in-jail commands, and never alter a host service or its policy. Host-side reads of jail files remain confined and symlink-refusing; status is a runtime witness, never evidence authorizing host reads or execution.

Control messages are versioned JSON objects transported by a noninteractive runtime exec of a `yolo-jaild` control-client subcommand. Their request fields are the jail instance, operation (`status`, `prepare`, `commit`, `cancel`), request identifier, daemon-name list and committed snapshot identifier where needed. Secrets and commands never ride argv. Replies carry per-name state, settled addresses and typed failures, not token values. The maximum request is 1 MiB; reject malformed, oversized or unsupported messages without side effects. `prepare` returns a handle tied to the request and jail instance; `commit` and `cancel` are idempotent for that handle. `cancel` only releases a preparation that has not committed; it cannot withdraw a ready daemon. The supervisor serializes admission even if a jail process bypasses the host launch lock.

## 4. One transaction, two owners

The host launcher owns composition and token publication. The in-jail supervisor owns process state and port reservations for additions. Neither can overwrite the other's settled identities.

### 4.1 Attach ordering

1. **Hold the workspace launch lock.** Inspect the running jail, keeper, pack tree, held grants and contract tags. Use the running declarations throughout; never substitute newly configured packs. A compatible entry with no additions sends no activation request, but still pins its entry snapshot before releasing the lock. Confirm the jail's main boot and supervisor startup have settled before requesting additions; an in-progress boot uses the existing provisioning wait, and a failed boot is not repaired by activation.
2. **Count this session under that lock**, before any control request or publication. A keeper already draining follows the existing wait-for-fresh-launch path. If the lock or count cannot be established, refuse *new activation* without changing the jail; ordinary no-addition attaches retain their existing degraded behavior.
3. **Ask status, then prepare the missing names.** Query the supervisor rather than interpreting frozen `YOLO_JAIL_DAEMONS` as the current set. Existing identities are reused. A prepared addition reserves its address but starts no child. Validate the whole desired set and any bridge route compatibility before publication.
4. **Compose with the returned addresses and retained tokens.** Mint a token only for a genuinely new daemon that declares one. Run all current override, provider-credential and region preflights before publishing a new pointer. No front replacement or new host start occurs.
5. **Publish checked files.** Write the canonical token/address records, including the unexported scoped-token record in the existing shared channel, and finish this entry's snapshot. Confirm writes succeeded and the record, daemon inputs and selecting agent files agree. Only then request startup.
6. **Commit the prepared addition and wait for ready.** The supervisor reads the committed snapshot, verifies its identity and starts only the missing children. Startup uses that snapshot's shared inputs plus the daemon's own token, not the whole selecting agent's environment. Host launch checks still query the existing front; an expired login remains their warning, not adapter readiness.
7. **Release the launch lock, then exec the entry.** Carry its non-secret snapshot identifier/path through exec; the entrypoint and generated launchers read its files, not whichever attach last rewrote the shared files. The session lock stays held for the invocation's lifetime.

All runtime control operations together have a **30-second monotonic deadline**, starting with status; additions start concurrently within that common budget. Progress follows [WB-D20](../reference/wire-bridge.md#wb-d20): silent below two seconds, then one elapsed-time line naming the pending services. No runtime command gets an unbounded wait.

### 4.2 Publication and concurrent attaches

The existing shared channel remains the compatibility/discovery channel, with **inode-preserving writes** where it is a file bind. The entry snapshot lives below the already directory-mounted agent-env area (or the Apple Container home), with the existing owner-only modes and no new mount. It is published complete before its identifier is handed to the supervisor or entrypoint. Partial writes are errors, not today's best-effort success.

A separate canonical daemon record in that same mounted area retains tokens and served addresses for **every daemon ever admitted in this jail instance**, including after deselection. The host is its only writer under the launch lock. It is data parsed with the existing restricted channel grammar, never host-sourced shell code. A fresh launch creates a new instance record and pins its first session's snapshot by the same protocol; stale workspace records cannot authorize an addition.

Two attaches are serialized through readiness and publication, but not for their command lifetimes. The second re-reads status and the canonical record after acquiring the lock: the first addition is then ready, failed or still definitively supervised, not missing merely because the container's boot env lacks it. Same name and identity reuse one child; different names add independently. Requests with no additions are no-ops; retry after a lost response queries status rather than spawning blindly.

**Why both the longer lock and the snapshot?** Extending the lock through channel delivery fixes two writers racing, but release-before-exec still lets another writer win before the first boot reads. Pinning the snapshot closes that gap. Already-running processes keep their environments; later agent invocations within a snapshot-aware session use that session's agent files. Legacy sessions keep their existing last-written-file behavior; this contract cannot retrofit old processes.

Snapshots are retained until the jail is known gone, like its pack tree. Do not delete one when its launcher exits: a descendant may outlive that session. This costs small credential-bearing files for the jail lifetime; normal known-gone credential cleanup must include them, without an age-based sweep of live state.

### 4.3 Addresses are settled in the namespace that will bind

- **Existing daemons:** keep their address and token exactly; no relocation or rotation on attach.
- **New daemon, private network namespace:** reserve its declared loopback address inside the jail. A collision refuses activation; never assume an arbitrary listener is ours.
- **New daemon, shared network namespace:** reserve a port-zero choice inside the jail's actual network namespace and return the declared-to-served mapping before composition. This avoids choosing a macOS Podman Machine port on the Mac's unrelated loopback.
- **Explicit user address override:** preserve it; a collision refuses rather than silently changing the user's address.

The supervisor keeps reservations until immediately before child startup. The close-to-bind race with unrelated processes remains and fails closed; inherited sockets for arbitrary pack commands are not required in this slice. An unused prepare expires after 30 seconds, releasing its reservations. Once an identity is published, retries reuse its address and token; a failed bind reports the holder problem, not a new address behind an existing pointer.

This extends [NC-D44](../plans/notch-convergence.md#NC-D44)'s *reuse, never repick* rule only for daemons that did not previously exist. It preserves that rule for every running listener, and [NC-D69](../plans/notch-convergence.md#NC-D69)'s reservation-before-start ordering.

## 5. Readiness, failure and recovery

### 5.1 Ready belongs to a child start, not a PID

Use the existing `ready <name>` / `failed <name> <reason>` pipe protocol for each newly started child, with a separate pipe for that child start. The supervisor records its process generation, so an earlier readiness answer cannot certify a replacement process. Status reports ready only while the corresponding child is live; a restart returns it to starting until it reports again.

For an adapter, ready means **the authenticated handler has its settled token and its listener is bound**. It does not mean a cloud login is valid. AWS's adapter needs to participate in this protocol; it currently does not. A custom command that never reports readiness reaches the deadline and is refused, even if it has a PID. No AWS-name special case, log-scraping or bare TCP-open check substitutes for the generic protocol.

The supervisor retains each admitted child's immutable startup inputs for its existing restart policy. After first readiness, existing backoff and shutdown rules apply unchanged. It must never restart using the latest attach's mutable channel.

### 5.2 Outcomes are explicit

| Outcome | New entry | Jail and next step |
| :--- | :--- | :--- |
| All requested additions ready | Execute with its snapshot | Disclose newly activated names and served addresses, never token values |
| Pack, mount, grant or enabled front missing | Refuse before startup/publication | Name what is missing; select a profile the jail already supports, or wait for sibling sessions to finish and make a fresh launch with the prerequisite |
| Preflight or file publication fails | Refuse; no commit request | Release prepared reservations; name the failed check/path and retry after fixing it. Preserve settled service records; do not restore a stale whole-channel copy over another entry |
| Spawn/bind failure, malformed readiness or deadline | Refuse this entry, naming each failed/pending daemon, elapsed time and its log | Stop only newly started children that have **never** become ready; cancel their retry loops. Use the existing five-second terminate grace, then kill/reap those supervisor-owned children. Report cleanup failure; do not proceed as if ports were released |
| One addition ready, another fails | Refuse this entry | Keep successful additions and their identities. No all-or-nothing daemon rollback: another entry may reuse them |
| Control response lost or launcher interrupted | Do not execute the agent | Prepared-only work expires; committed startup completes or fails within its budget. Retain records and query status on retry. Cancellation never ends a ready daemon or a sibling session |
| Supervisor/control plane dead or ownership uncertain | Refuse new activation | No replacement/adoption based on PID or argv. Keep [OQ-PC3](../reference/wire-bridge.md#oq-pc3)'s inspect-and-reclaim remedy; a fresh launch waits until siblings are done |
| Existing front unreachable or settings drifted | Do not repair it | Keep the current reachability/refusal and host launch-check dispositions and their remedies; never silently start a second front |

The 30-second readiness deadline excludes the bounded five-second teardown grace; the caller allows that grace and a one-second reporting margin before declaring cleanup unconfirmed. A timed-out control helper is only a messenger: killing it must not kill the supervisor or any already-ready child.

[OQ-R8](../reference/loopback-tls-reachability.md#OQ-R8)'s `YOLO_ALLOW_UNREACHABLE_SERVICES=1` remains the explicit way to enter despite a service-readiness failure. It prints what is not working; no success line or fabricated ready result follows. It does not bypass the eligible-addition boundary, locks, identity validation, file-publication failure or orphan refusal. [Attach skew acknowledgment](attach-skew-and-contract-guardrails.md#5-remediation-what-happens-when-skew-is-detected) remains whole-channel withholding, not permission to activate an unknown command.

## 6. The jail owns daemons; sessions own commands

New children are spawned **by the original supervisor**, not by the control helper or attached entrypoint. They stay outside every entry's recorded process tree and Unix session. A jail that initially has only dormant candidates starts its idle supervisor from the jail's main boot, before any session can commit an addition; an attach cannot create a supervisor in its own process tree. Control-helper cancellation and the attaching launcher's signal guard release only this entry's session hold and pending preparation; they must never take the fresh-launch stop-jail action.

Closing the activating terminal, selecting a different profile or quitting that agent does not stop its admitted daemon. Later deselecting entries export no scoped token, but preserve its canonical token record. Daemons stop when the jail stops; they do not hold a session lock and therefore cannot keep a jail alive after its last counted session ends. The keeper and its front ownership are unchanged.

Session snapshots protect a sibling's environment and future launches through its session's generated launcher. They do not redirect already-serving bridge traffic or rewrite an old process's env. Shared pack-rendered configuration remains the existing system's limitation, explicitly outside this proposal; do not market this as full per-session agent-home isolation.

## 7. An existing wire-bridge route is immutable

A bridge is already **selection-lazy**: idle until a selection gives it a route, then serving a fixed plan ([the existing behavior](../reference/wire-bridge.md#coarse-condition-lazy-daemon)). Do not restart it to make it observe a new profile, or hand it a newly minted token.

For this proposal's baseline:

- An already-serving compatible route is reused after confirming its current readiness and route identity. The bridge resolves this entry's required routes with its existing plan resolver and compares every required route against the serving plan: protocol/path, upstream, credential source and effective model policy must agree. Unrelated serving routes may remain; a missing or differing required route is a conflict. This comparison is local and makes no provider/API request.
- An idle bridge may take its **first** plan from the committed entry snapshot, after dependencies such as a new credential adapter are ready. Bind precedes endpoint publication, as today. Activation must be acknowledged for that exact plan; the mere appearance of an old endpoint file is not enough. The bridge freezes the resolved route configuration and its credential inputs (agent-file inputs included), so a later shared-file rewrite cannot change a restart's plan. Refreshing credentials through the same declared host-service source remains allowed; replacing that source is not.
- A different upstream, credential source, model policy or missing per-agent route conflicts with a serving plan. Refuse the *new entry* before its pointer is delivered; leave the old plan byte-for-byte intact. Name the conflicting selection and offer a supported profile or a fresh jail after siblings finish.
- Do not depend on an uncontrolled idle poll of a partially rewritten channel. For snapshot-aware jails the first-plan handshake pins what the bridge will consume; it must not freeze a second attach's plan while the first awaits readiness.

This adds a route-status/first-plan acknowledgment inside the bridge, not provider-specific routing in core. The generic supervisor transports the daemon's readiness; the bridge itself knows whether its plan serves the selection. Changing that compatibility algorithm or adding separate listeners for later plans is the scope choice in [OQ-AD1](#OQ-AD1).

## 8. Support and old running jails

Support below is the **proposed target**, not a runtime result.

| Setup | Activation target and honest fallback |
| :--- | :--- |
| Podman on Linux, private namespace | Supported with the new contract, frozen prerequisites and ready protocol. The motivating Pi/Bedrock adapter addition needs no change to another session or bridge plan |
| Podman on Linux, host/shared namespace, including nested | Same protocol; reserve inside the actual jail namespace and authenticate every pointer. A nested result cannot validate rootless host-loopback forwarding |
| Podman Machine on macOS | Same container control protocol; reservations occur in the VM, not on the Mac. Host-front reachability still requires its own proof; no claim of macOS validation here |
| Apple Container | Generic in-jail-only additions can use the protocol and whole-home snapshot delivery. **The AWS motivating case is not supported by this change:** the current host-service start excludes AWS, and its missing front is not something attach may add. Say so before handing out a dead pointer; use an already-supported profile or a fresh Podman/native launch. A fresh Apple Container launch alone does not fix that backend limitation |
| `macos-user` | No container attach. Confined guest supervision is per invocation, but the workspace keeper owns host services and doorways, including AWS, until the last session leaves; joining starts nothing outside the sandbox. A missing roster doorway still refuses, or joins without it under `YOLO_ALLOW_ATTACH_SKEW=1`. Finish sibling sessions, then launch fresh for that doorway. This container-only design cannot add it or reuse another invocation's Seatbelt supervisor |
| `yolo host` | Not a jail backend or a container attach. Retain its per-invocation doorway/service lifecycle; this design adds no shared daemon manager there |

The native keeper is [built, with Mac verification still owed](jail-lifetime-last-session-wins.md#7-what-i-would-build-in-order). Its missing-doorway constraint is established by the source above, not merely an unmeasured support claim. Native activation is outside this proposal; neither refusal nor the skew hatch starts the missing doorway or ends siblings automatically.

New launches advertise a versioned contract tag covering the eligible set, add/status protocol, checked publication and entry snapshots. The running jail must advertise it **and** answer the control handshake for its own instance. No capability inferred from a host version, PID file or unreadable inspect result.

An older jail remains older: no hot-swapping its mounted binaries, changing `YOLO_JAIL_DAEMONS` under it or starting a second supervisor as a compatibility shim. If an addition is needed, use the existing explicit skew disposition and its disclosure of what a restart ends. The easiest non-disruptive remedy is to finish sibling sessions and launch fresh; a deliberate restart remains the owner's action, never a failed activation's automatic repair. Ordinary attaches needing no new feature keep their existing contract behavior.

## 9. Trade-offs, risks and sequencing

| Alternative | Verdict |
| :--- | :--- |
| Start every eligible adapter at initial boot | Rejected: defeats selection-gated startup and creates idle listeners/processes nobody selected |
| Poll a mutable daemon payload file and reconcile by replacement | Rejected: mixes activation with reconfiguration and lets a later attach withdraw another session's daemon |
| Spawn a standalone adapter during each attach | Rejected: duplicate listeners, token races and the wrong session lifetime |
| Restart the supervisor with the union of names | Rejected: interrupts every supervised service and loses fixed startup inputs |
| One additive manager with a frozen eligible set | Recommended: one process owner and explicit ready/refusal semantics; costs a control protocol and snapshots |

| Risk | Mitigation |
| :--- | :--- |
| A published pointer survives a failed start | No agent exec before readiness except the existing loud hatch; failed records remain retryable and do not claim a listener exists |
| Latest attach changes another session's selected credentials | Entry snapshots pin the environment; canonical daemon identities never rotate on deselection |
| An external process takes a reservation's port | Authenticate callers; bind failure refuses this entry. No ownership inferred from a port number |
| A daemon lies about readiness | Same trust in admitted pack code as initial boot; the protocol is not a security attestation. Never widen the host boundary based on its answer |
| Snapshot files retain secrets longer | Owner-only storage in existing mounts; known-gone cleanup, no live-state age pruning |
| Runtime support mistaken for credential-path support | Backend/prerequisite checks before activation, and separate real-host reachability evidence |

I would first settle the bridge scope, then make the generic addition and snapshot protocol work with a credential-free fixture daemon, then bring AWS's adapter into that existing readiness protocol. Only then exercise idle-bridge first-plan acknowledgment and old-jail refusal. A companion implementation plan must be written against the tree before code work; this document is not that handoff.

## 10. Open questions

1. 💬 **OQ-AD1: Should this first slice also serve a new wire-bridge plan beside one already serving?**

   Existing routes must stay immutable. The request establishes late daemon startup, but not whether a conflicting late bridge profile must work too; see [§7](#7-an-existing-wire-bridge-route-is-immutable).

   - **A — Refuse conflicting late plans in this slice.** Fix the direct Pi/Bedrock adapter case on supported container backends and idle-bridge first activation without multiplying listeners.
   - **B — Add independent listeners for later plans.** Cover more profile switches, but require per-plan address composition, identity and lifetime rules before implementation.

   <!-- vantage: question id=OQ-AD1 leaning="A — refuse conflicting late plans in this slice; it solves eligible container adapter startup without expanding bridge routing ownership." -->

   _Leaning:_ A — refuse conflicting late plans in this slice; it solves eligible container adapter startup without expanding bridge routing ownership.

   **Answer:**

   > _(empty — fill in when decided)_

## 11. Engineering decision ledger

These are reversible engineering choices for the proposed design, **not fabricated owner rulings**. All are unbuilt.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| AD-D1 | Generic named additions from a launch-frozen eligible set; no new pack manifest field or AWS switch | 2026-10-08 | [§3](#3-admit-an-addition-not-a-new-environment) | — |
| AD-D2 | Launch lock through readiness; session counted before activation; immutable entry snapshots close release-before-exec race | 2026-10-08 | [§4.1](#41-attach-ordering), [§4.2](#42-publication-and-concurrent-attaches) | — |
| AD-D3 | Host publishes token/served-address identities before startup; existing identities never rotate; new reservations occur in the jail namespace | 2026-10-08 | [§4.2](#42-publication-and-concurrent-attaches), [§4.3](#43-addresses-are-settled-in-the-namespace-that-will-bind) | — |
| AD-D4 | Thirty-second control/readiness budget; generation-specific child-ready protocol; bounded cleanup of only never-ready new children | 2026-10-08 | [§5](#5-readiness-failure-and-recovery) | — |
| AD-D5 | Daemons belong to the original supervisor and jail lifetime, not activating session; deselection revokes delivery, not the listener | 2026-10-08 | [§6](#6-the-jail-owns-daemons-sessions-own-commands) | — |
| AD-D6 | Versioned contract and live handshake; no old-jail bootstrap shim or activation-time automatic restart | 2026-10-08 | [§8](#8-support-and-old-running-jails) | — |

## 12. Observable acceptance and remaining evidence

Implementation is done only when credential-free fixtures demonstrate:

- A jail initially omitting a profile-served fixture daemon admits it on a later selected entry; the agent substitute starts only after ready and gets the identical published token/address.
- Two concurrent attaches start one same-named child and preserve distinct entry environments; a second attach during startup neither repicks nor rotates identities.
- A deselection and later re-selection reuse the daemon; closing the activating session leaves a sibling and that daemon alive. The last session still ends the jail.
- An empty initial set with eligible additions has one main-boot supervisor, and its added child is outside the attaching session's hangup targets.
- Missing packs/fronts/grants, unknown names, stale instances, conflicting identities, failed writes, spawn/bind failures, unresponsive children and lost control replies refuse or take the named hatch without ending siblings.
- A still-live but restarted child is not ready on the strength of its predecessor's answer. A nonparticipating custom command reaches the deadline, not a false green.
- An existing bridge plan is never replaced; idle first activation reads one committed snapshot, and a conflicting late plan follows the owner-approved scope.
- Old-contract jails cannot be repaired by a new host's imagination; their ordinary attach and explicit skew paths remain usable.

**Evidence still owed:** focused source/fixture checks after implementation; serialized container lifetime/concurrency tests; a real rootless host or CI for the host-front reachability path; Mac runs for Podman Machine and Apple Container. No automated agent turn or cloud credential is necessary to validate daemon activation. The owner may later try the original Pi command with their existing login; that is not a prerequisite for a fixture test or an authorization to change their account.
