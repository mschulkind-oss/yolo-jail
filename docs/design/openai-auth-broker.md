---
title: "One OpenAI refresh owner for Codex, Pi, hosts, and jails"
date: 2026-09-14
status: accepted
stage: BUILT
next: "Graduate into agent-credentials.md's OpenAI subscription section (system-doc), which then states the build rather than §3's relay; §7's login and expiry checks stay owed to a human"
tags: [authentication, codex, pi, opencode, oauth, design]
summary: "A machine-wide OpenAI credential service owns refresh-token rotation and browser callbacks while agents retain isolated runtime state."
---

# One OpenAI refresh owner for Codex, Pi, hosts, and jails

**Status:** 2026-09-24, re-checked 2026-09-30. **MEASURED:** a brokered refresh through the jail's
published endpoint on rootless podman, both architectures
(`TestOpenAIAuthBrokerRoundTripsAnImportedToken`, passing in `ci.yml` run 36662086103,
2026-09-30), and the `macos-user` doorway on a hosted Mac: reachable from inside the sandbox,
refusing a refresh without the launch's caller token, admitting one bound to it, and gone when the
session ends (`TestMacosUserOpensTheCodexDoorwayOutsideTheSandbox`, passing in `macos-user.yml`
run 36719581090 at `8f7468dd`, 2026-09-30; it runs no Codex). **UNMEASURED:** the
[§7](#7-completion-criteria) criteria that need a ChatGPT account and wall-clock time: a browser
login end to end, and agents crossing a real expiry, on any backend. Every part is built, the last
backend's refresh consumer on 2026-09-29 ([OQ-OA6](#OQ-OA6): on `macos-user` the launch opens
Codex's refresh doorway outside the sandbox, `fea3b6c7`). The one sentence of
[§2](#2-one-writer-and-two-views) that turned out unbuildable, Pi asking again after an
unauthorized response, was dropped the same day ([OQ-OA7](#OQ-OA7)). The canonical transaction,
host service, container adapters, pack dependency, browser login, managed host launch, status and
self-check are implemented, and so are host-only import and logout (`4de78ac0`, 2026-09-18; the
public `yolo openai-auth` verb since `fafb7493`, 2026-09-20) and Apple Container reporting the
service inert (`36c47baa`, 2026-09-18). Two places where the build and this body part ways, both
recorded in the [plan](openai-auth-broker-plan.md): [§3](#3-browser-callback-relay)'s state-routed
relay was not built, because the host daemon owns the whole login flow and the jail only streams
the URL back (plan step 5), and [§2](#2-one-writer-and-two-views)'s shared broker engine was not
generalized: `internal/openaiauth` sits beside `internal/oauthbroker` (the plan's Blockers).
**opencode joined 2026-10-01** on Pi's view ([OA-D1](#OA-D1)): MEASURED by unit tests only (the
view writer, the host prelaunch, and yolo's opencode plugin under node against a fake `yolo`);
no opencode session has sent a request on the subscription.

> **In short.** A yolo host service owns one OpenAI subscription grant and is
> the only component allowed to refresh it. Codex, Pi and opencode receive
> compatible views while their config, sessions, caches, and histories stay per
> workspace.

**Why it matters.** Sharing credential files alone creates a race around a
single-use refresh token; separate logins avoid the race by making the user
repeat browser authentication for every workspace and agent.

**The shape.** One canonical credential, one serialized refresh transaction,
two thin agent adapters, and a browser-callback relay on container backends.

**Cost.** Host Codex shares this login only when launched through yolo's managed
environment and therefore uses a yolo-managed Codex home. A directly launched
host Codex keeps its existing home and, if logged in there, an independent grant.

**Start at [§2](#2-one-writer-and-two-views)** — the ownership rule.

**Needs your ruling:** none.
[OQ-OA6](#OQ-OA6) was ruled 2026-09-29, and
[OQ-OA7](#OQ-OA7) was decided the same day as an implementation decision.

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

Host sharing is automatic for `yolo host -- codex` and generated host wrappers, on
the prelaunch the command's pack declares, the one a jail's launcher reads
([notch convergence item 15](../plans/notch-convergence.md#tier-4--the-host-runs-the-jails-checks-p1-p4));
the host starts the browser login only at a terminal.
That environment selects a yolo-managed Codex home, copies the ordinary host
configuration into it and links the skills, and routes refresh through the same service.
Yolo never rewrites the user's ordinary `~/.codex/auth.json` or silently changes
a directly launched host Codex. An explicit import can seed the broker from that
file once; after import the files are independent and the broker owns rotation.
Both are built as the public verb `yolo openai-auth` — `import --from <file>` and
`logout`, host-socket only (`4de78ac0`, 2026-09-18; public since `fafb7493`,
2026-09-20).

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
lock. The adapter refreshes before expiry only. It does not ask again after an
unauthorized response, because pi gives an extension no way to see one
([OQ-OA7](#OQ-OA7)). Pi's other provider credentials remain in its workspace
`auth.json`.

opencode receives Pi's view, under its own `openai` key in its own `auth.json`: the current access
token, its expiry and account, and the `yolo-broker:<generation>` marker as the refresh value
([OA-D1](#OA-D1)). opencode files its own ChatGPT login and an OpenAI API key under that same key,
so the view replaces either one, and the launch names what it replaced. opencode's built-in
ChatGPT support keys on that `oauth` entry, but its own request `fetch` refreshes against a
hard-coded `auth.openai.com` and stores the refresh token it gets back, and no config key or
variable redirects it. So yolo's opencode plugin replaces that fetch, which opencode allows, since
a later plugin's auth loader outranks a built-in one. The plugin asks the broker for the access
token, again only within five minutes of its expiry, and opencode never sends a refresh. A plugin
that failed to load leaves opencode's own fetch to send the marker, which is no credential, once
the token expires.

> [!NOTE]
> **Why Pi has no ask-once-more after an unauthorized response ([OQ-OA7](#OQ-OA7), decided
> 2026-09-29).** An earlier draft of this section said the adapter asks the broker once more
> after an unauthorized (HTTP 401) response. **Measured 2026-09-22 against pi 0.87.0, that
> cannot be built** ([the plan's warning](openai-auth-broker-plan.md)). Pi builds its
> `oauth.refresh(...)` from the extension's `refreshToken`, and both places pi calls it check
> expiry first. 401 is in none of pi's retry classifiers (the checks that decide which failed
> requests pi retries). And the extension API exposes no response status to hook. Both call
> sites still check expiry first in pi 0.99.1, re-read 2026-09-29. The broker's proactive refresh
> (next paragraph) covers what the clause was for: by the time a Pi view reaches its five-minute
> window, the broker already holds a current token. Nothing covers a token OpenAI rejects
> before its recorded expiry: pi reports that request as failed. Re-open this if pi adds a
> status hook, meaning a way for an extension to see a response's HTTP status.

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
in-jail HTTP adapter. Pi's provider extension and yolo's opencode plugin call
the same front. None of them requires interception of `auth.openai.com`, so
normal authorization-code and device login traffic still goes directly to OpenAI.

`macos-user` starts the same host singleton before entering the Seatbelt sandbox.
The sandboxed account reaches its authenticated loopback-TLS front directly on
host loopback and reads its endpoint credential from the sandbox-visible yolo
state prepared for that launch. It uses the same Codex and Pi adapters, and
the Codex one is the adapter `yolo host -- codex` serves: the launch runs it
outside the sandbox as a launch-owned listener on a port it picked, answering
only the caller token the Codex launcher binds into the refresh marker, and
stops it when the command exits ([OQ-OA6](#OQ-OA6), by
[HS-D15](host-notch-services.md#HS-D15)). It does not run the container-only
in-jail daemon, install a CA into the system trust store, edit host DNS, or
intercept all host traffic for `auth.openai.com`.

`yolo host -- codex` uses the same host singleton through a private mode-`0600`
Unix socket and starts a dynamic loopback adapter on `127.0.0.1:0`. Yolo remains
as the Codex parent and closes that adapter as soon as Codex exits. `yolo host --
pi` and the Pi extension use the private socket directly, and so do
`yolo host -p codex -- opencode` and yolo's opencode plugin, which offers the
shared login in opencode's `/connect` because the host's opencode `auth.json`
is the user's own and yolo does not write it; until that file holds the view,
each such launch says so. Generated wrappers delegate to
these same host launch paths.

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
  its workspace view through the broker before expiry and at no other time. An
  unauthorized response is not retried, because pi does not show an extension
  the response status ([OQ-OA7](#OQ-OA7)).

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
| OQ-OA6 | Route (b): on `macos-user` the Codex refresh doorway is a launch-owned listener, by [HS-D15](host-notch-services.md#HS-D15)'s doorway rule. Built `fea3b6c7` ([HS-D16](host-notch-services.md#HS-D16) to [HS-D20](host-notch-services.md#HS-D20)). | 2026-09-29 |
| OQ-OA7 | Implementation decision: the Pi adapter refreshes before expiry only, with no ask-once-more after an unauthorized response. Measured against pi 0.87.0, no extension can build that step, and the broker's proactive refresh covers the expiry case. Re-open if pi adds a status hook. | 2026-09-29 |
| <a id="OA-D1"></a>OA-D1 | Implementation decision, applying [OQ-OA1 and OQ-OA2](#8-decision-ledger) to a third agent: **opencode receives Pi's access-token view**, merged under `openai` into its own `auth.json` by `yolo internal openai-auth-client token --opencode-auth`, and **yolo's opencode plugin replaces opencode's request `fetch`** on that entry alone, so every request's token comes from the broker and opencode never refreshes. Codex's seam does not exist in opencode: its refresh address is hard-coded, and neither config nor environment moves it (read from opencode 1.18.34's source, never run). At the host the view is the private socket, as Pi's is. Built 2026-10-01 | 2026-10-01 |

## 9. Open questions

1. ✅ <a id="OQ-OA6"></a>**OQ-OA6: On `macos-user`, does Codex's refresh adapter come from a launch-owned listener, or wait for native jail daemons?**
   [§4](#4-backend-transport) says this backend uses the same adapters and does not run the
   container-only in-jail daemon, but the shipped Codex adapter IS that daemon
   (`yolo-jaild openai-auth-adapter`, declared as the manifest's `jail_daemon`), and
   `macos-user` starts no jail daemon at all. So `packs/codex`'s static
   `CODEX_REFRESH_TOKEN_URL_OVERRIDE` reaches the sandbox pointing at a port nothing binds: a
   session works until its first access token expires, then every refresh fails. The two ways
   out are route (a), wait for
   [`jail-daemon-on-macos-user-plan.md`](jail-daemon-on-macos-user-plan.md)'s steps 3 and 4,
   which are blocked on [OQ-DP8](declaration-parity.md#OQ-DP8) and
   [OQ-DP9](declaration-parity.md#OQ-DP9) (both still open 2026-09-24), and route (b), give this one service a
   launch-owned adapter on `127.0.0.1:0` beside the host services, carry its URL into the
   sandbox environment over the pack's static value, and close it when the agent exits. Route
   (b) is the shape `internal/openaiauthhost` already ships for `yolo host -- codex`.

   **What it decides:** whether `macos-user` Codex outlives its first access token before the
   generic jail-daemon work lands, and whether one manifest key may have two delivery
   mechanisms, a declared jail daemon on containers and a launcher-owned listener here.

   <!-- vantage: oq id=OQ-OA6 -->

   _Leaning:_ **Route (b).** It is already written once, it matches what
   [§4](#4-backend-transport) says this backend does, and it is subject to neither blocker. The
   cost is real: two delivery mechanisms for one manifest key, which
   [OQ-DP9](declaration-parity.md#OQ-DP9)'s confinement ruling might later make unnecessary.

   **Answer:**
   > **Ruled 2026-09-29, route (b), under a rule stated once for every backend
   > ([HS-D15](host-notch-services.md#HS-D15)):** the host service is the same everywhere, and its
   > doorway (the thin adapter that checks the launch's caller token and forwards to the host
   > service) opens on whichever loopback the agent sees. A container has a loopback of its own, so
   > the doorway runs inside it as a jail daemon; `macos-user` and `yolo host` share the Mac's, so
   > the launch opens it outside as a launch-owned listener. The maintainer, on why an in-jail
   > placement was never a principle: *"the host doesn't have to run [in the jail] anyway, so the
   > host can access it."* The same rule covers aws-auth's credential adapter (port 1461).

2. ✅ <a id="OQ-OA7"></a>**[OQ-OA7](#OQ-OA7): does [§2](#2-one-writer-and-two-views) drop "after an unauthorized response, the adapter asks once more"?**
   Measured against pi 0.87.0 (2026-09-22): pi never calls a provider's `refreshToken` on a
   401 — both call sites are expiry-gated — and the extension API exposes no status, so the
   Pi adapter cannot see an unauthorized response to react to. The other consumers are
   unaffected: Codex refreshes through its native override, and the wire bridge's Codex route
   (the one claude's `codex` profile uses) already retries once after a 401
   (`wirebridged.NewCodexResponsesHandler` sets `retryUnauthorized`). **What it decides:** whether
   the design states only what pi allows (refresh before expiry), or keeps a requirement that
   waits on an upstream pi hook.

   <!-- vantage: oq id=OQ-OA7 -->

   _Leaning:_ **Drop it for Pi, and say why.** The expiry-gated refresh plus the broker's
   proactive refresh inside the five-minute window already covers what the clause was for; a
   requirement no extension can meet reads as a missing feature forever. Re-open if pi grows a
   status hook.

   **Answer:**
   > **Decided 2026-09-29 (implementation decision, no ruling needed):** drop the clause for Pi,
   > as leaned. [§2](#2-one-writer-and-two-views) now says the adapter refreshes before expiry only,
   > and its note gives the reason. Measured against pi 0.87.0, the clause cannot be built: both
   > of pi's refresh call sites check expiry first, 401 is in none of its retry classifiers, and
   > the extension API exposes no response status. Both call sites still check expiry first in pi
   > 0.99.1 (re-read 2026-09-29). The broker's proactive refresh inside the five-minute window
   > covers the case the clause was for. The one gap left is a token OpenAI rejects before its
   > recorded expiry. Re-open if pi adds a status hook, meaning a way for an extension to see a
   > response's HTTP status.
