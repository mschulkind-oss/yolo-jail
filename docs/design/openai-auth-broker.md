---
title: "One OpenAI refresh owner for Codex, Pi, hosts, and jails"
date: 2026-09-14
status: accepted
tags: [authentication, codex, pi, oauth, design]
summary: "A machine-wide OpenAI credential service owns refresh-token rotation and browser callbacks while agents retain isolated runtime state."
---

# One OpenAI refresh owner for Codex, Pi, hosts, and jails

**Status:** DECIDED, 2026-09-14. Nothing built.

> **In short.** A yolo host service owns one OpenAI subscription grant and is
> the only component allowed to refresh it. Codex and Pi receive compatible
> views while their config, sessions, caches, and histories stay per workspace.

**Why it matters.** Sharing credential files alone creates a race around a
single-use refresh token; separate logins avoid the race by making the user
repeat browser authentication for every workspace and agent.

**The shape.** One canonical credential, one serialized refresh transaction,
two agent adapters, and one temporary browser-callback relay.

**Cost.** Host Codex shares this login only when launched through yolo's managed
environment. An unmanaged host Codex must keep an independent grant.

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
machine-wide host listener and completes the requesting jail's login. Device
authorization remains the fallback for SSH-only hosts.

One successful login becomes available to Codex and Pi in every workspace on
that machine. Logout is explicit and machine-wide, and the command must say that
before deleting the grant. A new login replaces the old grant atomically.

Host sharing is opt-in through a yolo-managed host environment. That environment
routes host Codex refresh through the same service. Yolo never rewrites the
user's ordinary `~/.codex/auth.json` or silently changes a directly launched
host Codex.

## 2. One writer and two views

The host service is the sole writer of the canonical access token, refresh
token, expiry, account ID, and generation. It guards login, refresh, replacement,
and logout with one machine-wide lock. The persisted update is an atomic rename
of a mode-0600 file in a mode-0700 directory.

For a refresh request, the service takes the lock, reloads canonical state, and
compares the caller's refresh-token fingerprint with the current generation. If
the current access token has more than five minutes remaining, or the caller is
stale, it returns the current generation without contacting OpenAI. Otherwise it
refreshes exactly once, persists the returned rotation, and then responds.

Codex keeps a workspace `auth.json` view because its native client expects one.
Its refresh endpoint points at the service. The view may contain the refresh
token because Codex must submit a native refresh request, but that token is a
generation identifier at the broker: a stale value cannot consume an upstream
token.

Pi receives an access-token view and never receives the refresh token. Its yolo
adapter asks again before expiry and once after an unauthorized response. Pi's
other provider credentials remain in its workspace `auth.json`.

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

## 4. Failure and recovery

- A transient upstream error leaves canonical credentials unchanged and returns
  a retryable error. There is no automatic second redemption.
- A permanent refresh error marks the grant as requiring login without deleting
  the last state; status remains inspectable.
- A client disconnect does not cancel an upstream refresh after redemption may
  have started. The service finishes and persists rotation before releasing the
  lock.
- A service restart reloads persisted state and pending callbacks disappear;
  the CLI prints a fresh login URL on retry.
- A stale Codex view repairs itself on its next brokered refresh. A long-lived Pi
  process requests a fresh access token after an unauthorized response.

## 5. Security and observability

The service is available only through the authenticated yolo loophole transport
and the loopback callback listener. Requests carry the jail identity assigned by
the launcher. Responses never include credentials other than the minimum view
for that agent.

Status and logs expose token fingerprints, expiry, generation decisions,
latency, caller identity, and upstream error metadata. They never expose token
bodies, authorization codes, PKCE verifiers, or callback query strings.

## 6. Completion criteria

- Two Codex processes and two Pi processes can cross one expiry boundary
  concurrently while exactly one upstream refresh occurs.
- Codex and Pi work from one browser login in different workspaces.
- An opted-in host Codex and jail Codex cross expiry concurrently without a
  reused-token failure.
- Ordinary host Codex state is untouched.
- Browser login works from a bridged-network jail with two simultaneous pending
  logins and no static per-jail host-port reservation.

## 7. Decision ledger

| ID | Decision | Date |
| :--- | :--- | :--- |
| OQ-OA1 | One machine-wide host service is the sole refresh-token writer. | 2026-09-14 |
| OQ-OA2 | Pi receives access tokens only; Codex uses its native refresh override. | 2026-09-14 |
| OQ-OA3 | Host sharing is opt-in and broker-routed; unmanaged host Codex keeps a separate grant. | 2026-09-14 |
| OQ-OA4 | Browser callbacks use one temporary, state-routed host relay. | 2026-09-14 |
