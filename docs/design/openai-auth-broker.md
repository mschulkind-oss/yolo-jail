---
title: "One OpenAI refresh owner for Codex, Pi, hosts, and jails"
date: 2026-09-14
status: accepted
tags: [authentication, codex, pi, oauth, design]
summary: "A machine-wide OpenAI credential service owns refresh-token rotation and browser callbacks while agents retain isolated runtime state."
---

# One OpenAI refresh owner for Codex, Pi, hosts, and jails

**Status:** IN PROGRESS, 2026-09-14. The canonical transaction, host service,
container adapters, pack dependency, browser login, managed host launch, status
and self-check are implemented. Host-only import/logout and final backend
verification remain.

> **In short.** A yolo host service owns one OpenAI subscription grant and is
> the only component allowed to refresh it. Codex and Pi receive compatible
> views while their config, sessions, caches, and histories stay per workspace.

**Why it matters.** Sharing credential files alone creates a race around a
single-use refresh token; separate logins avoid the race by making the user
repeat browser authentication for every workspace and agent.

**The shape.** One canonical credential, one serialized refresh transaction,
two thin agent adapters, and a browser-callback relay on container backends.

**Cost.** Host Codex shares this login only when launched through yolo's managed
environment and therefore uses a yolo-managed Codex home. A directly launched
host Codex keeps its existing home and, if logged in there, an independent grant.

**Start at [§2](#2-one-writer-and-two-views)** — the ownership rule.

**Needs your ruling:** **None.**

**Reads with:** [`openai-auth-broker-plan.md`](openai-auth-broker-plan.md) (the
implementation hand-off), [`../research/openai-subscription-auth.md`](../research/openai-subscription-auth.md)
(source evidence), and [`../reference/agent-credentials.md`](../reference/agent-credentials.md)
(existing tiers).

---

## 1. User experience

The first Codex or Pi launch without a credential prints one browser URL and
opens it on the host when a browser opener exists. The callback reaches a
machine-wide host listener and completes the requesting jail's login. When no
browser opener exists, the printed URL is the manual path.

One successful login becomes available to Codex and Pi in every workspace on
that machine. Logout is explicit and machine-wide, and the command must say that
before deleting the grant. A new login replaces the old grant atomically.

Host sharing is automatic for `yolo host -- codex` and generated host wrappers.
That environment selects a yolo-managed Codex home, links the ordinary host
configuration and skills into it, and routes refresh through the same service.
Yolo never rewrites the user's ordinary `~/.codex/auth.json` or silently changes
a directly launched host Codex. An explicit import can seed the broker from that
file once; after import the files are independent and the broker owns rotation.
That explicit import command is not implemented yet.

## 2. One writer and two views

The host service is the sole writer of the canonical access token, refresh
token, expiry, account ID, and generation. It guards login, refresh, replacement,
and logout with one machine-wide lock. The persisted update is an atomic rename
of a mode-0600 file in a mode-0700 directory.

For a refresh request, the service takes the lock, reloads canonical state, and
compares the caller's opaque generation marker with the current generation. If
the current access token has more than five minutes remaining, or the caller is
stale, it returns the current generation without contacting OpenAI. Otherwise it
refreshes exactly once, persists the returned rotation, and then responds.

Codex keeps a workspace `auth.json` view because its native client expects one.
Its refresh endpoint points at the service. The view carries
`yolo-broker:<generation>` in Codex's required refresh-token field, rather than
the canonical refresh token. A stale marker receives the current generation
without consuming an upstream token.

Pi receives an access-token view and never receives the canonical refresh token.
Its provider record contains the current access token and expiry plus a
nonsecret marker in Pi's required `refresh` field. Pi's normal five-minute
refresh window invokes the yolo provider adapter, which asks the broker for the
current generation and rewrites that workspace's provider record. Concurrent
workspaces can all take their own Pi file locks and ask; the broker returns a
cached generation or performs exactly one upstream refresh under its machine
lock. After an unauthorized response, the adapter asks once more before failing.
Pi's other provider credentials remain in its workspace `auth.json`.

The broker also refreshes proactively when the canonical access token enters
the five-minute window. This is the same availability measure as the Claude
broker: an idle agent or a sleeping machine can resume with a current access
token even before an agent initiates its own refresh path.

This deliberately replicates the Claude broker's refresh semantics: a
machine-wide flock, reload under lock, stale-caller detection, cached-token
return, one upstream redemption, atomic persistence, proactive refresh, and
fingerprint-only diagnostics. It does not copy Claude's hostname interception.
Codex already exposes a refresh URL override, and Pi exposes a provider
extension API; using those supported seams avoids installing an OpenAI-signing
CA and intercepting unrelated host traffic. The shared broker engine and client
protocol should be generalized once, with provider-specific upstream and
credential-view adapters rather than a second independent implementation.

## 3. Browser callback relay

One host listener binds loopback port 1455 while at least one login is pending.
A jail registers the random OAuth `state`, its reachable callback target, and a
15-minute deadline before presenting the authorization URL. The listener accepts
only exact callback paths, looks up and consumes `state`, and streams the request
to that target. Unknown, reused, or expired state fails without forwarding.

Registration is safe to repeat for the same jail and state. Multiple jails can
wait concurrently because routing uses state rather than port ownership. The
listener exits after the final registration expires or completes. Port 1457 is
the fallback if another host process owns 1455; the authorization URL must use
the port actually bound.

On `macos-user`, the agent is a sandboxed native process in the host network
namespace. Its loopback listener is already the browser's loopback listener, so
normal Codex and Pi browser callbacks need no relay. The relay exists only for
container backends where host and jail loopback differ.

## 4. Backend transport

The refresh algorithm is identical on every backend; only the local adapter's
route to the host service differs.

Container jails use the existing per-jail authenticated loopback-TLS front and
endpoint file. Codex points its supported refresh URL override at a small
in-jail HTTP adapter. Pi's provider extension calls the same front. Neither
requires interception of `auth.openai.com`, so normal authorization-code and
device login traffic still goes directly to OpenAI.

`macos-user` starts the same host singleton before entering the Seatbelt sandbox.
The sandboxed account reaches its authenticated loopback-TLS front directly on
host loopback and reads its endpoint credential from the sandbox-visible yolo
state prepared for that launch. It uses the same Codex and Pi adapters. It does
not run the container-only in-jail daemon, install a CA into the system trust
store, edit host DNS, or intercept all host traffic for `auth.openai.com`.

`yolo host -- codex` uses the same host singleton through a private mode-`0600`
Unix socket and starts a dynamic loopback adapter on `127.0.0.1:0`. Yolo remains
as the Codex parent and closes that adapter as soon as Codex exits. `yolo host --
pi` and the Pi extension use the private socket directly. Generated wrappers
delegate to these same host launch paths.

## 5. Failure and recovery

- A transient upstream error leaves canonical credentials unchanged and returns
  a retryable error. There is no automatic second redemption.
- A permanent refresh error marks the grant as requiring login without deleting
  the last state; status remains inspectable.
- A client disconnect does not cancel an upstream refresh after redemption may
  have started. The service finishes and persists rotation before releasing the
  lock.
- A service restart reloads persisted state and pending callbacks disappear;
  the CLI prints a fresh login URL on retry.
- A stale Codex view repairs itself on its next brokered refresh. Pi refreshes
  its workspace view through the broker before expiry or once after an
  unauthorized response.

## 6. Security and observability

The service is available only through the authenticated yolo loophole transport
and the loopback callback listener. Requests carry the jail identity assigned by
the launcher. Responses never include credentials other than the minimum view
for that agent.

Status and logs expose token fingerprints, expiry, generation decisions,
latency, caller identity, and upstream error metadata. They never expose token
bodies, authorization codes, PKCE verifiers, or callback query strings.

## 7. Completion criteria

- Two Codex processes and two Pi processes can cross one expiry boundary
  concurrently while exactly one upstream refresh occurs.
- Codex and Pi work from one browser login in different workspaces.
- An opted-in host Codex and jail Codex cross expiry concurrently without a
  reused-token failure.
- Ordinary host Codex state is untouched.
- Browser login works from a bridged-network jail with two simultaneous pending
  logins and no static per-jail host-port reservation.
- The same login and expiry crossing work under `macos-user` without DNS or
  system trust-store changes.

## 8. Decision ledger

| ID | Decision | Date |
| :--- | :--- | :--- |
| OQ-OA1 | One machine-wide host service is the sole refresh-token writer. | 2026-09-14 |
| OQ-OA2 | Pi receives an access-token view; Codex uses its native refresh override with an opaque generation marker. Neither receives the canonical refresh token. | 2026-09-14 |
| OQ-OA3 | `yolo host -- codex` shares the broker through a managed Codex home; direct host Codex remains untouched. | 2026-09-14 |
| OQ-OA4 | Container browser callbacks use one temporary, state-routed host relay; `macos-user` uses its native loopback. | 2026-09-14 |
| OQ-OA5 | All backends use authenticated loopback TLS and the same refresh algorithm; none intercepts `auth.openai.com`. | 2026-09-14 |
