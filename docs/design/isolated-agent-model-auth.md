---
title: "Model auth for an isolated agent process, without copying the login"
date: 2026-10-15
status: deprecated
stage: SUPERSEDED
next: "Nothing: the owner declined the request on 2026-10-15; reopen only on a new request"
tags: [design, credentials, pi, openai-auth, claude-oauth-broker, aws-auth, wire-bridge, testing]
summary: "DECLINED 2026-10-15 by the owner. A downstream request asked that a second Pi process inside a jail, with its own agent directory, a copied session and candidate extension code, get model auth without a copy of the login. This sketch laid out four options (an access-only view, a proxy that injects auth, a provider-scoped key, or mocked dispatch). The owner declined the request: inside one jail a brokered token is not a boundary, no per-token revocation exists for either subscription, Anthropic-subscription use through an intermediary is a question for Legal Counsel, and mocked offline tests are not blocked. Nothing is built. The analysis stays as the record of why."
---

# Model auth for an isolated agent process, without copying the login

**Status:** 2026-10-15, declined by the owner: *"Okay, let's reject this request from YOLO for
that isolated agent thing. We'll put that in a doc that we hand back to them with all of the rest
of the stuff."* Earlier the same day: *"I'm a little suspect of this whole request."* Nothing is
built and nothing will be. The reasons are the ones this sketch already found
([§10](#10-why-it-was-declined)). Every claim about current behavior was read from the tree at
`e57e89739`. No experiment ran, so each statement about a provider's token behavior is cited or
marked UNMEASURED.

> **In short.** The isolated process does not need a copy of the login. It needs either an
> access-only token from a broker that already exists, or no token at all, because a proxy
> adds auth on its way out. The second option is the only one where revocation is real.
> Either way, inside one jail neither option is a boundary against code running as the
> same user ([§3](#3-the-fact-that-bounds-every-option)).

**Why it matters.** To test candidate Pi extension code against a copied session, a live
model call is needed. Today that call has no auth unless someone copies `auth.json`,
refresh tokens included, into a scratch directory.

**The shape.** A request path *(coined here)* runs from the isolated process to a yolo
broker or proxy. The broker or proxy holds the grant and returns either an access token
(options A and C) or a proxied response (option B).

**Cost.** Options A and B each add a credential channel, which
[the credential boundary's first invariant](../reference/agent-credentials.md#invariants)
counts as a change to the mount-assembly or compose path.

**Start at [§4](#4-the-options)**, the options compared per provider.

**Needs your ruling:** nothing. [OQ-IA1](#OQ-IA1) to [OQ-IA6](#OQ-IA6) are moot, closed by
the decline ([§9](#9-decision-ledger)).

**Reads with:** [`agent-credentials.md`](../reference/agent-credentials.md) (every
credential channel that exists),
[`claude-login-without-interception.md`](claude-login-without-interception.md) (the Claude
credential view),
[`simultaneous-auth-and-pack-isolation.md`](simultaneous-auth-and-pack-isolation.md) (what
a Pi extension cannot confine), and [`wire-bridge.md`](../reference/wire-bridge.md) (the
proxy that already injects upstream auth). No implementation sketch was opened, and none will be.

---

## 1. The problem

**Isolated process** *(coined here)*: a second agent process started inside a running jail
for testing. It has its own agent directory (for Pi, `PI_CODING_AGENT_DIR`), a copied
session, and extension code that has not been reviewed. It is not a nested jail, and it is
not a subagent the running agent spawns through its own runtime.

**Live auth store** *(the requester's phrase)*: the files that hold a usable login today.
For Pi that is `~/.pi/agent/auth.json`, which the Pi pack links to the machine-scope
`.pi-shared-credentials/auth.json`. Pi keeps every provider's credential in that one file,
so the link shares every one of them machine-wide
([`packs/pi/pack.toml` L304-327](../../packs/pi/pack.toml)).

The isolated process finds no login, for three reasons:

- **Its agent directory has no `auth.json`.** `PI_CODING_AGENT_DIR` moves Pi's whole agent
  directory, and no `PI_` variable or flag moves `auth.json` alone
  ([`pi-host-openai-auth.md`, option E](pi-host-openai-auth.md#e--a-managed-pi-agent-directory-the-codex-precedent)).
- **yolo's pre-launch view writer targets one fixed path.** The Pi derive sets
  `YOLO_AUTH_PRELAUNCH_PI_PATH = ".pi/agent/auth.json"` and ignores a moved agent directory
  ([`packs/pi/derive.lua` L1789-1792](../../packs/pi/derive.lua#L1789-L1792)).
- **Copying `auth.json` copies refresh tokens.** Both subscription grants rotate a
  single-use refresh token, so a copy that refreshes signs the machine out
  ([`agent-credentials.md`, invariants](../reference/agent-credentials.md#invariants)). It
  also puts a standing grant into a scratch directory that candidate code can read. The
  requester has ruled this out.

The requirement: give the isolated process short-lived model auth that can be revoked and
audited, and never give it refresh material.

## 2. What exists today, per provider

| Provider | Who holds the grant | What a jail process can get without refresh material | Lifetime | Status |
| :--- | :--- | :--- | :--- | :--- |
| **OpenAI subscription** (`openai-codex`) | The host `openai-auth-broker` is the only writer. Refresh tokens never cross into a jail ([OQ-OA2](../reference/agent-credentials.md#oq-oa2)) | An **access view**: `access_token`, `expires_at`, `account_id`, `generation`, with no refresh or ID token ([`handler.go` L65-80, L230-235](../../internal/openaiauthdaemon/handler.go#L65-L80)). Pi's form of it puts a non-redeemable `yolo-broker:<generation>` marker in `refresh` ([`agent-credentials.md`](../reference/agent-credentials.md#one-writer-and-a-view-per-agent)) | UNMEASURED. The repo records no real expiry. | Built. A brokered refresh was measured on rootless podman. |
| **Anthropic, Claude Code login** | The host `claude-oauth-broker`. Its canonical login sits in a state directory no launch mounts ([CL-D2](claude-login-without-interception.md#CL-D2)) | A **credential view**: `accessToken`, `expiresAt`, `scopes`, `subscriptionType`, `rateLimitTier`, and no `refreshToken` ([CL-D1](claude-login-without-interception.md#CL-D1); allowlist at [`claudeview.go` L150-153](../../internal/claudeview/claudeview.go#L150-L153)) | 8 h, MEASURED ([F3](claude-login-without-interception.md#F3)) | Built behind `YOLO_CLAUDE_CREDENTIAL_VIEW=1`, off by default. No real Claude has run on a view. The machine-scope shared file, which holds the refresh token, is still bound into the jail ([CL-D20](claude-login-without-interception.md#CL-D20)) |
| **Anthropic, Pi's own login** | Nobody but Pi. The Claude broker serves only the claude pack, and Pi's Anthropic login (when it has one) is a plain entry in the live auth store | Nothing brokered | Not recorded | No yolo mechanism |
| **Bedrock** (`aws-auth`) | The host service mints from the host's SSO session, narrowed by `role_arn` and an optional `session_policy` | Short-lived STS credentials, served from a loopback URL. No key or token sits in any file or variable ([what crosses](../reference/agent-credentials.md#what-crosses-into-the-jail)) | 1 h, the role-chaining ceiling ([`awsauth.go` L45-49](../../internal/awsauth/awsauth.go#L45-L49)) | Built. One cached credential per profile, shared by every caller ([`broker.go` L120-183](../../internal/awsauth/broker.go#L120-L183)) |
| **OpenRouter** | The user's own API key, delivered by `env_sources` to the env file of the agent that selected the provider ([the credential gate](../reference/providers.md#the-credential-gate)) | The key itself. yolo mints no sub-keys | The key's own | Built. UNMEASURED live ([`gateway-provider-packs.md`](gateway-provider-packs.md)) |

The OpenAI broker's `token` action is not tied to one agent. Any process holding the jail's
endpoint can run `yolo internal openai-auth-client token`, which prints the access view, or
run it with `--pi-auth=<path>`, which merges a Pi-shaped view into any `auth.json` path it
is given ([`internal/openauthclient/main.go` L36-143](../../internal/openauthclient/main.go#L36-L143)).
The pre-launch step uses exactly that client
([`internal/entrypoint/shims.go` L1275-1281](../../internal/entrypoint/shims.go#L1275-L1281)).
So for `openai-codex`, option A needs no new broker capability. It needs a supported way to
aim the existing writer at the isolated process's agent directory.

**A proxy that injects auth already exists.** The wire bridge drops every inbound
credential, checks the launch's caller token, and builds outbound auth itself:

- a bearer key read from its `0600` env channel, for API-key providers;
- a SigV4 signature, for Bedrock;
- for Claude's Codex profile, an access-only view fetched from the OpenAI broker
  ([the Codex Responses route](../reference/wire-bridge.md#the-codex-responses-route)).

Pi already uses the bridge's **via route** (a pass-through with no translation, one path
prefix per agent), sending the caller token as its key. That route deliberately skips
`openai-codex`, "whose credential a via route does not carry"
([the via route](../reference/wire-bridge.md#the-via-route--one-route-per-agent-under-agentname)).

## 3. The fact that bounds every option

**The loophole framework names a sibling jail as the adversary, not the jail's own
processes** ([threat model](../reference/loophole-transport.md#threat-model)). The endpoint
file and every caller token are readable by any process running as the jail's user:

- The bridge's caller token "is not a secret from the jail's own processes"
  ([caller authentication](../reference/wire-bridge.md#what-it-does-not-defend-against)).
- The `aws-auth` caller token "is not a boundary" inside the jail
  ([what crosses](../reference/agent-credentials.md#what-crosses-into-the-jail)).
- The OpenAI broker identifies its caller only as `session.JailID`
  ([`handler.go` L71](../../internal/openaiauthdaemon/handler.go#L71)).
- `SO_PEERCRED` cannot tell processes apart here either.

It follows that an isolated process in the same jail, as the same user, can already read
the live auth store and every endpoint file. Pi's extension policy has the same limit:
[`simultaneous-auth-and-pack-isolation.md` §3.4](simultaneous-auth-and-pack-isolation.md#34-what-the-extension-cannot-close).
So **"never exposing refresh material" is a property of what yolo hands the process, not
something yolo can enforce against same-user candidate code**. Enforcing it would take a
different user ID, a nested jail, or a separate jail with no live auth store mounted.
[OQ-IA6](#OQ-IA6) asked whether that is in scope.

What each option can honestly promise:

- The process is given no refresh material.
- Every token it receives is short-lived and attributed.
- Under option B, the process's access can be cut off by yolo.

## 4. The options

### 4.1 A: an access-only view with an expiry

The broker writes, or returns, an access-only view for the isolated process: the same
shape agents get today, aimed at the isolated agent directory. The process refreshes by
asking the broker again, using the `yolo-broker:<generation>` marker for Pi.

- **Revocation: none for an individual token.** No yolo verb revokes a token upstream.
  Both logouts "revoke nothing" ([CL-D15](claude-login-without-interception.md#CL-D15);
  [import and logout](../reference/agent-credentials.md#import-and-logout--the-hosts-two-verbs)).
  - **Anthropic:** the repo has found no API for revoking one access token. A refresh
    *appears* to revoke the access token it replaces, which is SOURCED from third-party
    reports and unmeasured here (measure M7,
    [§6](claude-login-without-interception.md#6-costs-and-risks)). That makes a forced
    refresh a blunt kill switch: it ends every outstanding Claude token, and every view
    then needs a rewrite.
  - **OpenAI:** individual revocation and real lifetime are both UNMEASURED.
  - **Bedrock:** yolo has no revocation, and an issued STS credential works until it
    expires. AWS-side revocation was not researched for this doc, so it is UNMEASURED.
  - So, under A, revoking means not issuing again and waiting out the remaining lifetime
    (8 h for Anthropic, 1 h for Bedrock, unknown for OpenAI).
- **Audit:** per jail, not per process.
  - The OpenAI and Claude brokers log decisions with token fingerprints.
  - The AWS and OpenAI adapters add a best-effort `/proc`-derived caller name, on Linux
    only ([the adapters' logs](../reference/agent-credentials.md#the-adapters-logs)).
  - The framework records one line per connection and one per request
    ([access logging](../reference/loophole-protocol.md#access-logging)).
  - None of this goes to `yolo audit`, which reads only the GitHub broker's
    `audit.jsonl`.
- **Blast radius:** a bearer valid for its whole lifetime, usable from anywhere it is
  copied to, with the full scope of the user's subscription. That is the same exposure the
  jail's own agent already has.
- **Providers:**
  - `openai-codex`: works today in mechanism, by aiming the existing writer at another
    path.
  - Bedrock: works, since the SDK credential chain already serves short-lived credentials.
  - Anthropic through Claude's broker: the token was issued to Claude Code's client ID.
    Whether Pi's Anthropic provider accepts it, and whether Pi then tries to refresh with
    a view that has no refresh token, are both UNMEASURED.
  - Anthropic through Pi's own login: nothing holds that grant, so yolo would have to
    become its refresh owner first.
  - OpenRouter: not applicable, because an API key has no access/refresh split.

### 4.2 B: a proxy that injects auth, so the process never holds a token

The isolated process points its provider base URL at a yolo proxy, which is the wire
bridge's via route or a sibling of it. Instead of a provider credential it holds an
**auth handle** *(coined here)*: a random secret minted for that one process. The handle is
good only at that proxy, and it is not a provider credential and not the launch's caller
token. The proxy checks the handle, removes it, adds the real upstream auth, and forwards.

- **Revocation: real, and immediate.** Removing the handle makes the next request fail
  with 401 at the proxy. No upstream token is ever handed out, so nothing outlives the
  revocation, except a stream already in flight, if the owner allows that to finish
  ([OQ-IA5](#OQ-IA5)).
- **Audit:** one line per request at the proxy, naming the handle (and therefore the
  process it was issued for), the model, and the upstream status. This is the only option
  that can attribute every call.
- **Blast radius:** a stolen handle works only against this launch's proxy, only while the
  launch runs, and only until it is revoked. It never leaves the machine as a usable
  provider credential.
- **Providers:**
  - API-key providers (OpenRouter): the via route already does this.
  - Bedrock: the via route already signs with SigV4.
  - `openai-codex`: the bridge already injects an access view on Claude's Codex route, but
    the via route skips this provider today. Supporting it means carrying the subscription
    on a pass-through route, which the
    [Codex-route warning](../reference/wire-bridge.md#the-codex-responses-route)
    ("do not turn the bridge into a general OpenAI gateway") argues against. That warning
    needs re-ruling.
  - Anthropic subscription: no proxy exists. For Claude Code, the `ANTHROPIC_BASE_URL`
    shape was rejected because account calls bypass it
    ([F11](claude-login-without-interception.md#F11), [CL-D8](claude-login-without-interception.md#CL-D8)).
    Whether Pi's Anthropic provider has the same split is UNMEASURED.
- **Cost:** the proxy is in the inference path. The interposition reference recorded this
  as "a rejected alternative with a real cost" for Claude
  ([`claude-oauth-interposition.md`](../reference/claude-oauth-interposition.md)). A
  per-process handle is also new: today every credential in this framework is per launch.

> [!NOTE]
> **Flagged for Legal Counsel, not interpreted here.** Anthropic's published terms address
> intermediating Claude.ai credentials, and
> [`claude-login-without-interception.md` §6](claude-login-without-interception.md#6-costs-and-risks)
> quotes the text. Options A and B both route an Anthropic subscription grant to a process
> other than the unmodified Claude Code binary. This doc makes no judgment on how those
> terms apply. That question goes to Legal Counsel before any Anthropic-subscription row is
> built.

### 4.3 C: provider-native scoped keys where available

The isolated process gets a credential the provider itself scopes, such as a separate API
key, rather than any part of the subscription login.

- **Revocation:** whatever the provider offers for that key, independent of the main
  login. Revoking a dedicated key leaves the main session untouched.
- **Audit:** the provider's own usage records, plus yolo's existing per-agent `env_sources`
  gate.
- **Blast radius:** the key's scope and limits.
- **Providers:**
  - OpenRouter: a second user-created key works today through `env_sources`. Whether
    OpenRouter offers per-key spending limits or programmatic provisioning keys is
    UNMEASURED; nothing in the repo cites it.
  - Bedrock: a separate `aws-auth` profile with a tighter `session_policy` is within the
    pack's existing settings. The broker caches one credential per profile, and nothing
    is per process ([`awsauth` manifest](../../packs/aws-auth/loopholes/aws-auth/loophole.toml)).
  - Anthropic: an Anthropic API key from the Console is a separate billing path, not the
    subscription. `claude setup-token` mints a one-year, inference-only subscription token
    ([F9](claude-login-without-interception.md#F9)). One year is the opposite of
    short-lived, and whether Pi can use it is UNMEASURED.
  - OpenAI subscription: the repo records no scoped-key mechanism.

### 4.4 D: do nothing, and test against mocked dispatch

The isolated process runs with a stand-in provider that returns canned responses, and
never makes a live call. A Pi extension can register a provider in place of a real one;
yolo's own policy extension already does this with its blocking provider
([`simultaneous-auth-and-pack-isolation.md`](simultaneous-auth-and-pack-isolation.md)).

- **Revocation, audit, blast radius:** nothing to revoke, because there is no credential.
- **Providers:** all of them, because none are used.
- **Cost:** it does not meet the stated need, which is live-testing against copied
  session state. Model-dependent behavior goes untested.

### 4.5 Side by side

| | A: access-only view | B: injecting proxy | C: provider-scoped key | D: mocked |
| :--- | :--- | :--- | :--- | :--- |
| Process holds | an access token | an auth handle (not a provider credential) | a scoped provider key | nothing |
| Revoke one process | no; wait out the lifetime | yes, at the next request | yes, if the provider supports it | not needed |
| Per-process audit | no (per jail; best-effort `/proc` name) | yes | provider-side only | not needed |
| `openai-codex` | works today in mechanism | needs a re-ruled via route | none recorded | yes |
| Anthropic subscription | Claude's grant: UNMEASURED for Pi; Legal Counsel | no proxy; UNMEASURED; Legal Counsel | `setup-token` lasts 1 year | yes |
| Bedrock | yes (1 h) | yes (via route signs) | yes (separate profile) | yes |
| OpenRouter | not applicable | yes (via route) | yes (second key) | yes |
| New code | a path or flag for the view writer, plus attribution | per-process handles, revocation, possibly a subscription route | none, or a profile | a test provider |

## 5. Verdict, held loosely

My read: **B is the right end state, and A for `openai-codex` is the cheap first step.**
A reuses a writer that already exists and adds no exposure beyond what the jail's own Pi
has. It cannot revoke, and it cannot attribute a call to one process. B is the only option
where a token cannot outlive its revocation. It is also mostly built for Bedrock and API
keys. Anthropic stays out of scope until Legal Counsel has answered and the two
UNMEASURED Pi behaviors have been measured.

## 6. Non-goals

- **Not confinement of candidate code.** See [§3](#3-the-fact-that-bounds-every-option).
  Same-user code in the jail can read the live auth store whatever this design does.
- **Not a change to the main agent's auth.** The running Pi keeps its views and its
  `auth.json`.
- **Not a second refresh owner.** No option lets the isolated process redeem a refresh
  token. That would be the race each broker exists to prevent.
- **Not cross-jail or host use.** The isolated process lives inside one running jail.
- **Not general subagent auth.** Subagents spawned through Pi's own runtime inherit the
  parent's environment and auth, which this doc does not change.

## 7. Risks

| Risk | Mitigation |
| :--- | :--- |
| A view written into scratch is mistaken for a safe artifact and copied elsewhere | Expiry is real and short. B avoids the artifact altogether |
| Candidate code turns off yolo's OpenAI extension, so Pi's refresh sends the marker upstream | The marker is no credential, so the request fails ([OpenAI views](../reference/agent-credentials.md#one-writer-and-a-view-per-agent)) |
| A forced Anthropic refresh used as a kill switch signs out every Claude view for the length of a rewrite | Do not use refresh as revocation unless M7 measures it ([§6](claude-login-without-interception.md#6-costs-and-risks)) |
| A per-process handle leaks into child processes through the environment | The question of how a process is given a handle ([OQ-IA4](#OQ-IA4), moot) would have decided this |
| Usage terms on intermediating Claude.ai credentials | Deferred to Legal Counsel ([§4.2](#42-b-a-proxy-that-injects-auth-so-the-process-never-holds-a-token)) |

## 8. Open Questions

None. The six questions this sketch asked are moot, closed by the decline; each is a row in the
[ledger](#9-decision-ledger), where its anchor still resolves.

## 9. Decision Ledger

| ID | Decision | Date | Section |
| :--- | :--- | :--- | :--- |
| <a id="IA-D1"></a>IA-D1 | **Declined by the owner.** *"Okay, let's reject this request from YOLO for that isolated agent thing."* No option is built, and the request goes back to the requester with the reasons in [§10](#10-why-it-was-declined) | 2026-10-15 | [§4](#4-the-options) |
| <a id="OQ-IA1"></a>[OQ-IA1](#OQ-IA1) | *Moot by [IA-D1](#IA-D1).* Which option to build (A, B, C, D, or A then B). Its leaning was A for `openai-codex` now and B as the end state | 2026-10-15 | [§4.5](#45-side-by-side) |
| <a id="OQ-IA2"></a>[OQ-IA2](#OQ-IA2) | *Moot by [IA-D1](#IA-D1).* Which providers are in scope. Its leaning left the Anthropic subscription out until Legal Counsel answered | 2026-10-15 | [§2](#2-what-exists-today-per-provider) |
| <a id="OQ-IA3"></a>[OQ-IA3](#OQ-IA3) | *Moot by [IA-D1](#IA-D1).* Whether short-lived tokens that cannot be revoked are acceptable | 2026-10-15 | [§4.1](#41-a-an-access-only-view-with-an-expiry) |
| <a id="OQ-IA4"></a>[OQ-IA4](#OQ-IA4) | *Moot by [IA-D1](#IA-D1).* How an isolated process requests auth and how it is tied to one process | 2026-10-15 | [§7](#7-risks) |
| <a id="OQ-IA5"></a>[OQ-IA5](#OQ-IA5) | *Moot by [IA-D1](#IA-D1).* Under B, what revocation does to a request already in flight | 2026-10-15 | [§4.2](#42-b-a-proxy-that-injects-auth-so-the-process-never-holds-a-token) |
| <a id="OQ-IA6"></a>[OQ-IA6](#OQ-IA6) | *Moot by [IA-D1](#IA-D1).* Whether the isolated process must be kept out of the live auth store, not just handed none of it | 2026-10-15 | [§3](#3-the-fact-that-bounds-every-option) |

## 10. Why it was declined

The owner declined the request on 2026-10-15. The reasons are the ones this sketch had already
found:

- **Inside one jail, a brokered token is not a boundary.** Any process running as the jail's
  user can read the live `auth.json` and every endpoint file
  ([§3](#3-the-fact-that-bounds-every-option)). Candidate code given an access-only view could
  read the full login anyway.
- **No per-token revocation exists for either subscription.** Neither yolo nor the repo's
  research has a way to revoke one Anthropic or OpenAI subscription token, so under options A
  and C revoking means waiting out the lifetime ([§4.1](#41-a-an-access-only-view-with-an-expiry)).
- **Anthropic-subscription use through an intermediary is flagged for Legal Counsel.** Options A
  and B both route that grant to a process other than Claude Code
  ([§4.2](#42-b-a-proxy-that-injects-auth-so-the-process-never-holds-a-token)). This doc makes no
  judgment on it.
- **The tests are not blocked.** The requester's own request 13 records that mocked offline
  tests are not blocked, which is [option D](#44-d-do-nothing-and-test-against-mocked-dispatch).
  Live behavior can be verified in a relaunched jail.
