---
title: "The wire bridge as the jail's model gateway: signing, routing by model and by agent, failover, and allowlists"
date: 2026-09-25
status: in-review
tags: [wire-bridge, bedrock, aws, sigv4, routing, failover, models, allowlist, providers, subscription]
summary: "What the wire bridge may do once it stands in front of an agent's model traffic. Four parts are ruled and unbuilt: it signs its own AWS requests with SigV4, routes claude's everything profile by model id, offers a sign-only OpenAI chat-completions route, and carries claude's subscription with opt-in per-model failover to Bedrock. A fifth part, ruled 2026-09-25: a profile can send its agent's traffic through the bridge (native pass-through or translated) instead of the agent's own client, so the bridge can enforce the picker's model list (on by default) and route each agent by a per-agent path prefix. Every question is ruled; the signer keys on the upstream address now and on a provider marker once one exists."
vantage:
  status-chip: true
---

# The wire bridge as the jail's model gateway

**The question this doc answers:** once the wire bridge stands between an agent and its model
provider, what may it do? It could sign for AWS, choose an upstream per model or per agent,
fail over when a subscription runs out, or refuse a model that is not on a list.

**Status:** DESIGN, 2026-09-25, one ruling owed ([OQ-WG6](#OQ-WG6), found while starting Part 3). Split out of [`bedrock-plumbing.md`](bedrock-plumbing.md) that
day, carrying its bridge questions with their ids unchanged. **Part 1 (signing) is BUILT,
2026-09-25** ([§2](#2-part-1--the-bridge-signs-its-own-upstream-requests-ruled)), except the
region-composed upstream URL. Parts 2–5 are DECIDED and unbuilt; Part 5's four questions were ruled
in review on 2026-09-25. **MEASURED:** what the bridge does today
([§1](#1-what-the-bridge-does-today)), from the code at `5e8e64f6`, symbols re-checked at
`ee8154f2`; the signer against AWS's published SigV4 test suite (31 cases) and through the real
bridge handler with the network stubbed. **UNMEASURED:** no request has reached real Bedrock
through the bridge with any credential, so AWS has never accepted one of its signatures. Nobody
has exercised runtime's Anthropic Messages route or the subscription's usage-limit response.

**Needs your ruling:**

[OQ-WG6](#OQ-WG6): what a profile writes to put its agent on the bridge path; it gates Parts 3 and 5. [OQ-WG1](#OQ-WG1)–[OQ-WG5](#OQ-WG5) are ruled ([Decision Ledger](#decision-ledger)).
[OQ-WG1](#OQ-WG1) carries a follow-up that waits on [OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2):
re-key the signer on the provider's Bedrock marker.

**Where the provider split ended up**, in plain words, because every part below leans on it.
Each agent reaches Bedrock in one of two ways. It either uses **its own native Bedrock client**
(the **native arm**, coined in [`bedrock-plumbing.md`](bedrock-plumbing.md)), or it goes
**through the wire bridge** (the **bridge arm**, coined there too). The **wire bridge** is the
in-jail daemon that serves an Anthropic endpoint on the jail's loopback and forwards requests to
one upstream ([`wire-bridge.md`](../reference/wire-bridge.md)). yolo ships one Bedrock endpoint
family, `bedrock-runtime` ([DIR-BR3](bedrock-plumbing.md#DIR-BR3)). On it:

| Agent | Native arm | Bridge arm |
| :--- | :--- | :--- |
| claude | `-p bedrock`: Claude Code's own Bedrock mode, Anthropic models only | the **everything profile**: every model in one session ([OQ-BR11](bedrock-plumbing.md#OQ-BR11)) |
| codex, opencode | yes | only when the profile picks the bridge path ([OQ-WG5](#OQ-WG5)) |
| pi | Converse | also: the sign-only route ([OQ-BR5](bedrock-plumbing.md#OQ-BR5), ruled "both") |
| copilot | none | the only way in |
| oh-omp | its own client speaks mantle, so it goes unused | the sign-only route |

The **everything profile** is a term coined in [`bedrock-plumbing.md`](bedrock-plumbing.md). It
is a claude profile that points claude at the bridge, so that one session can switch between
Anthropic models and every other Bedrock model. Its name is still
[OQ-BR1](bedrock-plumbing.md#OQ-BR1)'s to decide, and how many providers back these profiles is
[OQ-BR9](bedrock-plumbing.md#OQ-BR9)'s. What a provider and a profile should mean at all is
[`providers-and-profiles-redesign.md`](providers-and-profiles-redesign.md)'s question.

**Reads with:** [`wire-bridge.md`](../reference/wire-bridge.md) (the bridge as built),
[`bedrock-plumbing.md`](bedrock-plumbing.md) (which transport each agent has),
[`model-lists-and-pickers.md`](model-lists-and-pickers.md) (what a model list holds, what each
picker shows, and the bridge's `/v1/models`), [`bedrock-web-search.md`](bedrock-web-search.md)
(the search proxy that shares the signer), [`sso-backed-bedrock.md`](sso-backed-bedrock.md)
(where the bridge's credential comes from), and
[`gateway-provider-packs.md`](gateway-provider-packs.md) (OpenRouter and Kilo).

---

## 1. What the bridge does today

MEASURED from the code at `5e8e64f6`; the symbols named were re-checked present at `ee8154f2`.

- **It carries a declared provider to claude and copilot.** `adaptEndpoints`
  (`internal/packload/providers.go`) gives every provider with an `openai` endpoint an
  `anthropic` endpoint at the bridge's loopback address, whenever a selected agent speaks
  `anthropic` and the provider has no `anthropic` route. `packs/claude` `needs`
  `packs/wire-bridge` unconditionally.
- **One upstream, chat-completions only.** `routeFor` (`internal/wirebridged/boot.go`) takes the
  provider's `openai` endpoint as the one upstream, and refuses any `wire_api` other than
  `openai-chat-completions`. The bridge's other route, Responses, serves claude's `openai-codex`
  subscription profile alone.
- **Its own bearer; model ids passed through.** The bridge strips only Claude's `[1m]` suffix
  (`internal/wirebridge/request.go`). It sends `Authorization: Bearer` with the key the
  provider's `api_key_env_name` names, and ignores the agent's credential
  ([WB-D4](../reference/wire-bridge.md#wb-d4)). `bridgeHandler.doUpstream`
  (`internal/wirebridged/handler.go`) builds the upstream request from a body already in memory,
  which signing needs.
- **What translation loses.** `cache_control`, `thinking` and every unmapped key are dropped.
  `count_tokens` is refused with a 404 ([WB-D14](../reference/wire-bridge.md#wb-d14)). Only
  `POST /v1/messages` is served.
- **copilot prefers the Anthropic endpoint** (`packs/copilot/derive.lua`), so a bridged provider
  reaches copilot through the bridge.

**So today the bridge reaches Bedrock with an API key and nothing else.** A **Bedrock API key**
is AWS's Bedrock-only credential, sent as-is as a bearer from `AWS_BEARER_TOKEN_BEDROCK`. It is
one of the three credentials yolo supports; the others are a static key pair and an SSO session
through `aws-auth` ([OQ-SSO7](sso-backed-bedrock.md#13-decision-ledger);
[`bedrock-plumbing.md`](bedrock-plumbing.md#64-the-credential-three-are-supported)). A user
provider pointing `endpoints.openai` at runtime's `/openai/v1` with
`wire_api: "openai-chat-completions"` and such a key should give claude and copilot every
chat-completions model runtime serves. That is INFERRED; no request has been made. Nothing signs
SigV4: `vendor/` holds no AWS module, and nothing under `internal/` or `cmd/` signs (MEASURED).

Three consequences, all of which fall away for claude and copilot once the bridge signs:

1. An SSO or static-key user has no way onto the bridge except a key minted from their
   credentials: [`sso-backed-bedrock.md`](sso-backed-bedrock.md#4-five-options)'s option D,
   unbuilt.
2. The credential preflight demands `AWS_BEARER_TOKEN_BEDROCK`, and a jail that also carries
   `aws-auth`'s pointer is refused at launch. So the bridge and the SSO arm cannot share a jail.
3. An account denying `bedrock:CallWithBearerToken` and `bedrock-mantle:CallWithBearerToken`
   rejects every request the bridge makes.

**AWS is not the obstacle; yolo is.** AWS supports SigV4 on runtime's OpenAI-compatible route:
the Chat Completions page says *"The `bedrock-runtime` endpoint supports AWS SigV4
authentication and Amazon Bedrock API key authentication"*, with a SigV4 example signed for the
service `bedrock` against `/openai/v1/chat/completions` (SOURCED 2026-09-24,
[§11](#11-evidence)). yolo's provider has one credential field, `api_key_env_name`, no derive
tells a generic OpenAI client to sign, and the bridge sends a bearer (MEASURED). Whether any
agent's generic client *could* sign is unread.

---

## 2. Part 1 — the bridge signs its own upstream requests (ruled)

**BUILT 2026-09-25.** The signer is `internal/sigv4`: standard library only, pinned by AWS's
published SigV4 test suite (`internal/sigv4/testdata/v4`, the header-signing cases of
awslabs/aws-c-auth). The bridge's Bedrock arm is `internal/wirebridged/signing.go`: a route whose
upstream host is `bedrock-runtime.<region>.amazonaws.com` gets `route.SignRegion` at boot, and
`doUpstream` signs after every header is set. The credential order, lazy single-flight
resolution, the three failure statuses, and the expired-signature retry are built as
[§2.1](#21-behavior-the-signer-fixes) states.
Two things are not:
- **The region-composed upstream URL**, [§2.1](#21-behavior-the-signer-fixes)'s last bullet. No shipped provider routes Bedrock
  through the bridge yet, so a Bedrock upstream reaches the signer only as a provider's
  `endpoints.openai.base_url`. Composing it from `region` lands with the route that needs it
  (Part 2 and [OQ-BR9](bedrock-plumbing.md#OQ-BR9)).
- **A live request.** AWS has not yet accepted a signature from this signer, and that is
  [Risk R6](#21-behavior-the-signer-fixes)'s whole exposure.

[OQ-BR10](#OQ-BR10) is ruled: the bridge signs with **SigV4**, AWS's request-signing scheme
([AWS docs](https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv.html)). The signer
is written on Go's standard library and pinned by AWS's published SigV4 test vectors before any
live request. Nothing SigV4 is vendored, and vendoring the AWS SDK would put an AWS module into
the hermetic build.

**Why a signer and not a minted key.** SSO on the bridge route leaves two choices: a signer, or
a Bedrock API key minted from the session (option D). A key minted from `aws-auth`'s narrowed
session lives **at most an hour**. The narrowed credential is an assumed role taken from an SSO
role session, which is
[role chaining](sso-backed-bedrock.md#33-four-aws-words-this-doc-leans-on), and STS caps role
chaining at one hour. A bearer inherits that expiry and is frozen for the launch. From an
un-narrowed permission-set session it lasts that session's remaining one to twelve hours.
INFERRED from the two documented caps; not measured. A minted key also fails outright where a
service control policy denies `CallWithBearerToken`.

**Credential order**, the AWS SDK's own:

1. static `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY`;
2. the container-credentials endpoint `aws-auth` serves, cached until five minutes before its
   `Expiration`;
3. `AWS_BEARER_TOKEN_BEDROCK`, sent unsigned as a bearer.

**The seam is `bridgeHandler.doUpstream`**, where the body SigV4 must hash is already in memory.
The chat-completions response is OpenAI server-sent events, so signing the request is the whole
job: there is no AWS binary event stream to decode.

**What it buys.** The SSO credential refreshes itself inside a running jail, and an org denying
`CallWithBearerToken` still works. Option D becomes unnecessary for every shipped agent, copilot
included; retiring it stays [`sso-backed-bedrock.md`](sso-backed-bedrock.md)'s call. This is
**not** that doc's rejected option E, a host-side signing proxy: the credential still crosses as
the `aws-auth` pointer, the signer runs in the jail, and the host sees no prompt. It amends
[WB-D4](../reference/wire-bridge.md#wb-d4), whose outbound credential is today one key file read
at boot.

**One signer package, two users.** The search proxy in
[`bedrock-web-search.md`](bedrock-web-search.md) signs calls to an AgentCore gateway with the
same package ([OQ-BR21](bedrock-web-search.md#OQ-BR21)). This doc names that proxy and does not
build it. [OQ-WG1](#OQ-WG1) lets one signer serve both without waiting on the provider redesign.

### 2.1 Behavior the signer fixes

- **When it signs:** for an upstream whose host matches a signing pattern, per
  [OQ-WG1](#OQ-WG1)'s ruling; the re-key on the provider's Bedrock marker follows
  [OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2). The earlier draft keyed this on a provider's Bedrock marker, which
  is [OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2), now moved to the redesign doc. It
  never sends both a signature and a bearer, and never logs a credential.
- **Resolution is lazy**, on the first request, because `aws-auth`'s adapter may start after
  the bridge. Concurrent requests share one fetch, and the cached set is replaced whole.
- **The container-credentials fetch** times out at 5 s, with no retry inside one request.
- **No credential resolvable** → an Anthropic-shaped 401 naming the three sources it tried.
- **Adapter unreachable** → a 503 naming `aws-auth` and the `aws sso login` its lapsed-session
  message already names.
- **Signature rejected as expired** → refresh once, retry once. That is safe: a rejected request
  did not run, and `doUpstream` builds a fresh request per attempt. Any other AWS error passes
  through, as today.
- **The upstream URL** is composed from the provider's `region` on runtime's host, never shipped
  as a literal. A pack cannot know the region.

**Risk R6.** A signer bug fails every bridged request with a 403, for every user alike. The test
vectors pin the signer before any live request, the 403 body names the signature mismatch, and
the bridge log carries it.

---

## 3. Part 2 — routing by model id, for claude's everything profile (ruled)

[OQ-BR11](bedrock-plumbing.md#OQ-BR11), ruled in [`bedrock-plumbing.md`](bedrock-plumbing.md),
gives claude both a native profile and an everything profile. That ruling is the authority; this
part is the mechanism. One claude process has one transport, so the everything profile points
claude at the bridge (`ANTHROPIC_BASE_URL`, never `CLAUDE_CODE_USE_BEDROCK`), and **the bridge
routes by model id**:

- **An Anthropic id** is forwarded **untranslated** to runtime's own Anthropic Messages route,
  signed like every request, so `cache_control`, `thinking` and `count_tokens` survive. The
  bridge knows an id is Anthropic's from the list entry's declared **vendor** (the model's
  maker, coined in [`bedrock-plumbing.md`](bedrock-plumbing.md), defined now in
  [`model-lists-and-pickers.md`](model-lists-and-pickers.md)), never by parsing the id.
- **Every other id** is translated to chat-completions, as today.

This **amends** the bridge's rule that it dials only the upstream selected at boot
([`wire-bridge.md`](../reference/wire-bridge.md#lifecycle-and-failure-behavior)): the everything
route has two upstreams under one provider, chosen per request.

**UNMEASURED, and the first thing to measure.** AWS's endpoints page lists the Messages API on
`bedrock-runtime` (read 2026-09-25). Nobody has read that route's path, or whether it streams
Anthropic SSE or AWS's binary event-stream. If the latter, the bridge must re-frame it.

**Risk R7.** Translation loses something a non-Anthropic model needs: tool-call fidelity,
reasoning, or a vendor's streaming quirk. The bridge already fails closed on an unknown block
type ([WB-D5](../reference/wire-bridge.md#wb-d5)). Measure one turn per vendor in the org's list
before shipping that vendor in a company pack.

**Done-condition** (carried from [`bedrock-plumbing.md`](bedrock-plumbing.md)'s done-condition
6), once [OQ-BR9](bedrock-plumbing.md#OQ-BR9), [OQ-BR12](model-lists-and-pickers.md#OQ-BR12) and
[OQ-BR13](model-lists-and-pickers.md#OQ-BR13) rule. Claude's everything profile completes one
turn against a non-Anthropic model on runtime (a DeepSeek or Qwen id), once under each of the
three credentials, and copilot does the same. Under the SSO credential the turn still succeeds
after the first credential set expires, with no relaunch. It is a manual runbook on a real host;
automated tests never make API calls.

---

## 4. Part 3 — the sign-only OpenAI chat-completions route (ruled)

The **sign-only route** *(coined here)* is a second bridge route beside the Anthropic one: an
OpenAI chat-completions endpoint on the jail's loopback that forwards the request unchanged and
adds only the signature. It is the same signer at the same seam, with no translation step.
INFERRED; unbuilt. **Its authority is two rulings, not one:** [OQ-BR5](bedrock-plumbing.md#OQ-BR5)
(ruled 2026-09-25) for pi, and [DIR-BR2](bedrock-plumbing.md#DIR-BR2)'s whole-matrix direction
(2026-09-24) for oh-omp. No ruling names oh-omp's route directly: the matrix needs one, and
oh-omp's own client cannot reach runtime. It serves:

- **oh-omp**, whose own Bedrock provider takes a bearer only and speaks mantle, so it goes
  unused on runtime.
- **pi's bridge version.** [OQ-BR5](bedrock-plumbing.md#OQ-BR5) ruled both: *"native converse
  is just basically the pi agent's native which we're supplying for all the others and then also
  there will be the bridge version because I'm sure there will be some different features we can
  offer and we're just going to want to support both as fully as possible."* So pi's native
  Converse profile and pi through the bridge ship side by side, and this route is the bridge half.
- **Any agent a user points at the gateway route**, which speaks OpenAI already and gains SigV4
  with no client change.

---

## 5. Part 4 — the subscription arm, and opt-in failover (ruled)

**The maintainer's question** (2026-09-24): *"say I log in through Claude Teams and then I run
out of usage, need to switch to my Bedrock for Claude. Is that going to align the model names? …
can we also run Claude Teams through a wire bridge in some way to align all of them to make this
switching easier?"*, and whether the switch can be automatic.

**Today the names do not align, and the switch is a relaunch.** A Teams login speaks Anthropic's
ids (`claude-opus-4-8`) or claude's tier aliases; Bedrock needs inference-profile ids
(`us.anthropic.claude-opus-4-8`). The shipped `bedrock` provider declares no `models`, so after
`-p bedrock` claude falls back to its own Bedrock defaults, which need not match the Teams
session's model. The transport is chosen at launch, so Teams to Bedrock means a new claude
process.

The **subscription arm** *(coined in [`bedrock-plumbing.md`](bedrock-plumbing.md))* is claude's
Teams login routed through the wire bridge like any other upstream: the bridge forwards claude's
request untranslated, with the subscription's OAuth bearer it arrived with, to Anthropic. That
claude sends that bearer to whatever `ANTHROPIC_BASE_URL` names is MEASURED
([`agent-auth-modes.md` §8.1](agent-auth-modes.md#81-measured-2026-09-02-the-subscription-bearer-follows-anthropic_base_url)).
[OQ-BR16](#OQ-BR16) puts it on the everything profile, [OQ-BR17](#OQ-BR17) rules opt-in
failover, and [OQ-BR18](#OQ-BR18) clears the terms question. Then:

- **One name per model, across sources.** An agent's **effective list** *(coined in
  [`bedrock-plumbing.md`](bedrock-plumbing.md); owned now by
  [`model-lists-and-pickers.md`](model-lists-and-pickers.md))* is the ordered list its picker
  offers for the selected provider. It names a model once, and each entry carries the id each
  upstream needs: Anthropic's for the subscription, the inference profile for Bedrock. claude
  shows one `claude-opus-4-8`, and the bridge picks the upstream.
- **Failover per model, on the subscription's own limit signal.** When the subscription answers
  with its usage-limit response, the bridge resends that request to Bedrock under the mapped id,
  and keeps doing so for that model until the limit resets. It is enabled per profile, and every
  switch is disclosed. This reverses the deferral in
  [`agent-auth-modes.md` OQ-1](agent-auth-modes.md#12-decision-ledger), because the bridge
  removes all three of its reasons: the limit is no longer opaque to yolo; the switch is per
  model, not global; and claude never changes credentials mid-session.

**What it costs:**

- **The bridge touches subscription model traffic**, a deliberate exception to
  [`claude-oauth-interposition.md`](../reference/claude-oauth-interposition.md)'s rule that yolo
  never does. The bridge holds the subscription bearer for the session, in the jail that already
  holds that login.
- **Billing and data terms move** to the AWS account on a failed-over request. That is why
  failover is opt-in and each switch is disclosed.
- **The prompt cache does not follow a switch**: the first Bedrock request pays full input cost.
- **The upstream needs a provider row.** [§7](#7-what-the-bridge-still-refuses) confines the
  bridge to hosts composed from provider rows, and no shipped `kind: "provider"` names
  `api.anthropic.com` (MEASURED at `ee8154f2`). So building this arm includes declaring one.
  INFERRED; the implementer's step, not a new question.

**UNMEASURED:** the usage-limit response's exact shape (status, error type, the reset headers
claude reads); whether claude tolerates a response from a different upstream mid-conversation
(it should, since each request carries the whole conversation); and how a switch is shown, since
the bridge cannot write into claude's interface.

---

## 6. Part 5 — all traffic through the bridge (new direction, DESIGN)

<a id="DIR-WG1"></a>**[DIR-WG1](#DIR-WG1)** is the maintainer's direction of 2026-09-25, given in
answer to [OQ-BR4](provider-credential-scope.md#OQ-BR4) (*"no I don't want it to leak"*):
*"having an option to send all traffic through wire bridge or whatever for all endpoints so that
we can do things like filter models — like when I'm using OpenRouter I'd want to set a specific
set of models and not allow it to use other models … the only way to do that even if you're
talking about pi … is through wire bridge … maybe we can identify specific agents so we can
route them to different places … give them different endpoints … another design doc spawning out
of this"*.

**All-traffic mode** *(coined here)* is an opt-in in which every covered agent's model traffic
reaches its provider through the bridge, including an agent that could call the provider itself.

**It is a property of the profile** ([OQ-WG2](#OQ-WG2)). An agent has one active profile at a
time, so the profile is what decides how that agent reaches the world. Each profile picks exactly
one path ([OQ-WG5](#OQ-WG5)):
- **Native:** the agent's own client talks to the provider, and the bridge is not involved.
- **Bridge:** the agent talks to the bridge. Depending on the profile, the bridge either passes
  the agent's own wire protocol straight through (**pass-through**: it signs, routes and
  enforces, and never rewrites the body), or translates it to the provider's protocol.

A profile that says nothing is native, so existing launches are unchanged.

It buys what no per-agent config can:

1. **A hard model allowlist.** pi's `enabledModels` is a default view, never a boundary: Tab
   shows every credentialed model, `--model` bypasses it, and switching checks auth only
   ([`provider-credential-scope.md` §2.4.1](provider-credential-scope.md#241-what-pis-enabledmodels-actually-constrains)).
   So for an OpenRouter user who wants a fixed set of models, the bridge is the only place a
   refusal can live ([OQ-WG3](#OQ-WG3)).
2. **Per-agent routing**: each agent gets its own upstream or endpoint. The bridge tells agents
   apart by a **path prefix per agent on its one listen port** (`http://127.0.0.1:<port>/agent/<name>/`),
   which each agent's derive writes into that agent's base URL ([OQ-WG4](#OQ-WG4)). A request
   without a known prefix is refused with a protocol-shaped error naming the base URL it should
   have used; it is never routed to a default.
3. **Parts 1–4 for agents that bypass the bridge today**: signing, model routing, failover.

**The allowlist is not a second list** ([OQ-WG3](#OQ-WG3)). It is the effective list after a
`models` `only` ([OQ-BR12](model-lists-and-pickers.md#OQ-BR12)), which the pickers already
render: a model the picker offers is allowed, so what the menu shows and what the bridge admits
cannot drift. **Whether the bridge enforces it is a separate switch**, a profile setting that
defaults to **on**. With it on, the bridge refuses any other model id with an error shaped for
the agent's protocol (Anthropic or OpenAI) that names the list. With it off, the list only
shapes the pickers. The switch can bite only on the bridge path, because native traffic never
reaches the bridge. For OpenRouter and Kilo the maps an `only`
narrows are user-curated ([OQ-GP2](gateway-provider-packs.md#decision-ledger), as amended), and
Kilo already reaches claude and copilot through the bridge
([OQ-GP3](gateway-provider-packs.md#decision-ledger)).

**A rejected alternative, revisited.** [`provider-switching.md`](provider-switching.md) rejected
refusing a launch when the config holds an id outside the selected provider's `models`, "for v1 —
reconsider later": it would catch residue loudly but refuse most legitimate hand-picked models.
A launch-time check also cannot see a mid-session `/model` switch or a `--model` flag, and the
bridge sees every request. So the rejection stands as the *enforcement point*, while its intent
moves here.

**How it relates to [OQ-BR1](bedrock-plumbing.md#OQ-BR1).** That question is restated around an
agent's own native Bedrock client versus the wire bridge. A profile that forces the bridge for an
agent with a native client is the Bedrock instance of this switch ([OQ-WG2](#OQ-WG2)).

---

## 7. What the bridge still refuses

Three earlier non-licenses are reopened here by name:

- [`wire-bridge.md`](../reference/wire-bridge.md#what-a-wire-bridge-is-not): *"Not a gateway.
  One upstream … No routing tables, no failover, no budgets, no model remapping beyond what
  translation requires"*, echoed in its
  [What this does not license](../reference/wire-bridge.md#what-this-does-not-license).
  **Reversed** for routing by model (Part 2), failover (Part 4), and per-agent routing and
  allowlists (Part 5). Budgets stay refused.
- [`sso-backed-bedrock.md` §9](sso-backed-bedrock.md#9-non-goals): *"Not a gateway, not a
  proxy, not a model router."* **Reopened** by [DIR-WG1](#DIR-WG1) for the in-jail bridge only.
  The non-goal still holds for the credential service that doc builds, and its option E, a
  *host-side* proxy, stays rejected.
- [`bedrock-plumbing.md`](bedrock-plumbing.md)'s non-goal *"No bridge for an agent that has a
  native arm"*, whose one exception was the everything profile. [OQ-BR5](bedrock-plumbing.md#OQ-BR5)'s ruling puts pi through
  the bridge, and DIR-WG1 offers it to every agent. **Revised** to "no bridge for an agent with a
  native arm **unless a profile asks for it**" ([OQ-WG5](#OQ-WG5)).

**Still refused:**

- **No budgets or spend caps.** A budget needs a price per model and a ledger per session: the
  model catalog yolo does not keep.
- **No logging of request or response bodies.** In all-traffic mode the bridge sees every
  prompt. It logs routing decisions, model ids, status codes and switches, never a body or a
  credential.
- **No host not composed from a provider row.** The bridge dials only a declared endpoint, or
  runtime's host built from a declared `region`, from the launch's composed provider table. This
  replaces the earlier draft's "only the upstream selected at boot", which Part 2 contradicts,
  and keeps what it protected: nothing in a request can name a new destination.
- **No host-side bridge and no inbound authentication**, as
  [`wire-bridge.md`](../reference/wire-bridge.md#what-this-does-not-license) states. A per-agent
  token ([OQ-WG4](#OQ-WG4)) would be a routing key, not authentication.

---

## 8. Build order

1. **The signer** ([OQ-BR10](#OQ-BR10); [§2.1](#21-behavior-the-signer-fixes)), with its test
   vectors and lazy resolution. **BUILT 2026-09-25**, except the region-composed URL. It has no dependency on the model-list work, and on its own it
   closes the SSO gap for copilot and the everything profile. Keyed on the upstream host
   ([OQ-WG1](#OQ-WG1)); the marker re-key waits on [OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2). (Build step 8.1 in the source.)
2. **Routing by model id**, measuring runtime's Messages route first
   ([§3](#3-part-2--routing-by-model-id-for-claudes-everything-profile-ruled)). (Build step 8.3.)
3. **The sign-only route**
   ([§4](#4-part-3--the-sign-only-openai-chat-completions-route-ruled)), for oh-omp and pi.
4. **The subscription arm and failover**
   ([§5](#5-part-4--the-subscription-arm-and-opt-in-failover-ruled)), after measuring the
   usage-limit response.
5. **All-traffic mode** ([§6](#6-part-5--all-traffic-through-the-bridge-new-direction-design)),
   once [OQ-BR12](model-lists-and-pickers.md#OQ-BR12) gives the list an `only`. Pass-through
   routes land in this order: OpenAI chat-completions (oh-omp, pi), then Responses (codex), then
   Bedrock Converse. Before an agent's derive relies on the path prefix, measure that the agent
   keeps a base URL's path; an agent that drops it gets its own port instead.

---

## 9. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| **Mint a Bedrock API key for the bridge** ([option D](sso-backed-bedrock.md#4-five-options)) | **Superseded by [OQ-BR10](#OQ-BR10).** At most an hour over the narrowed session, frozen for the launch, and refused where `CallWithBearerToken` is denied. |
| **A host-side signing proxy** ([option E](sso-backed-bedrock.md#4-five-options)) | **Not needed.** An in-jail signer gets the refresh and the policy resilience without the host seeing any prompt. |
| **Vendor the AWS SDK's signer** ([OQ-BR10](#OQ-BR10)'s option C) | **Not taken; the implementer's call**, to keep the hermetic build free of an AWS module. The test vectors pin the standard-library signer instead. |
| **Two claude profiles, no routing by model** ([OQ-BR11](bedrock-plumbing.md#OQ-BR11)'s leaning) | **Overruled**: one session could never switch between Claude and an OpenAI model. |
| **Refuse a launch on an id outside the provider's `models`** ([`provider-switching.md`](provider-switching.md)) | **Still rejected as the enforcement point**; its intent moves to the bridge ([OQ-WG3](#OQ-WG3)). |
| **Sign on the provider's Bedrock marker** | **Deferred, not rejected ([OQ-WG1](#OQ-WG1))**: the follow-up once [OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2) gives providers a marker, because host patterns cannot know every configuration. |

---

## 10. Open Questions

1. ✅ <a id="OQ-WG1"></a>**[OQ-WG1](#OQ-WG1): how does the bridge know which requests to add an
   AWS signature to?** In plain words: Bedrock rejects any request that doesn't carry an AWS
   signature (SigV4), and every other provider has never heard of one. So the ruled signer
   ([Part 1](#2-part-1--the-bridge-signs-its-own-upstream-requests-ruled)) needs a rule for
   when to sign. There are two ways to decide:
   - **(a) By the address the request is going to.** Sign when the upstream host is an AWS
     Bedrock address: `bedrock-runtime.<region>.amazonaws.com` for model traffic, or
     `*.gateway.bedrock-agentcore.<region>.amazonaws.com` for the web-search proxy.
   - **(b) By a flag on the provider** saying "this provider is Bedrock". That flag is
     [OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2)'s marker, which you called *"a bigger
     decision than you're making it look"*, and which now waits on the provider redesign.

   Stakes: under (b) the signer can't ship until the redesign does.

   _Leaning:_ **(a)**. The bridge already knows the address when it starts, a signer keyed on it
   can never sign a request bound for a non-AWS provider, and the signer ships now without
   waiting on [OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2).


   **Answer:**
   > **(a) now, with (b) owed as a follow-up** (ruled in review, 2026-09-25): *"Yes, let's do A,
   > but I do want to follow up with B because A is cheating and I have no idea what other
   > people's configurations are."* The signer ships keyed on the upstream host. The host
   > patterns miss any Bedrock reached at an address they do not name, such as a VPC or FIPS
   > endpoint or a corporate proxy, so the signer re-keys on the provider's Bedrock marker once
   > [OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2) gives it one. Until then, an unmatched
   > Bedrock address is unsigned and fails with AWS's own error.

2. ✅ <a id="OQ-WG2"></a>**[OQ-WG2](#OQ-WG2): is all-traffic mode opted into per profile, per
   agent or per jail, and is it on by default anywhere?** Stakes: default-on changes every
   existing launch's traffic path, and a per-jail switch cannot say "pi through the bridge, codex
   native".

   _Leaning:_ Per profile, opt-in, default off, so existing launches are unchanged.
   [OQ-BR1](bedrock-plumbing.md#OQ-BR1)'s bridge-forcing profile is then the Bedrock instance of
   the same switch.


   **Answer:**
   > **Per profile, opt-in, off by default** (ruled in review, 2026-09-25): *"this should be a
   > property of the profile … We're not going to let people have multiple profiles active,
   > which means the profile is the primary and the thing that gets attached to for how the
   > world works."* Folded into [§6](#6-part-5--all-traffic-through-the-bridge-new-direction-design).

3. ✅ <a id="OQ-WG3"></a>**[OQ-WG3](#OQ-WG3): what enforces a model allowlist?** Candidates: a
   second list the bridge owns; the effective list the pickers already render; a launch-time
   refusal. Stakes: two lists drift, and a soft picker enforces nothing. It also decides whether
   a curated OpenRouter or Kilo map ([`gateway-provider-packs.md`](gateway-provider-packs.md))
   becomes an enforced allowlist rather than only the set a picker shows.

   _Leaning:_ The effective list after a `models` `only`
   ([OQ-BR12](model-lists-and-pickers.md#OQ-BR12)) is the one allowlist. The bridge refuses any
   other model id with an error shaped for the agent's protocol (Anthropic or OpenAI) that names
   the list. The bridge is the only hard enforcer for agents whose picker is soft (pi's
   `enabledModels`). The launch-time "refuse unknown id" of
   [`provider-switching.md`](provider-switching.md) stays rejected as the enforcement point.


   **Answer:**
   > **One list, and a separate gate that defaults on** (ruled in review, 2026-09-25): *"I don't
   > see a use case for having two lists. If we put it in the model picker, it's allowed … let's
   > have it even default on enforced. But just because you specify a list of models to be
   > filtered down to does not mean that we need the bridge to deny it."* So there are two
   > settings: the list (the picker's effective list) and whether the bridge enforces it (a
   > profile setting, default on). Folded into [§6](#6-part-5--all-traffic-through-the-bridge-new-direction-design).

4. ✅ <a id="OQ-WG4"></a>**[OQ-WG4](#OQ-WG4): how does the bridge tell agents apart, so each can
   be routed to a different upstream?** Candidates: a loopback port per agent; a path prefix per
   agent (`http://127.0.0.1:<port>/agent/pi/`); a per-agent token in the credential slot the
   bridge ignores today ([WB-D4](../reference/wire-bridge.md#wb-d4)); a header. Stakes: an
   identity the agent can drop silently misroutes. The adapter's address is one user-overridable
   port today, so a port per agent multiplies what
   [the listen-port section](../reference/wire-bridge.md#what-can-hold-the-listen-port-before-the-bridge-does)
   must cover.

   _Leaning:_ A port or path per agent, written by each agent's derive. Never a header the agent
   could omit: every agent can be given a base URL, and not every agent can be given a header.
   That every agent keeps a base URL's path is INFERRED, not read.


   **Answer:**
   > **A path prefix per agent on the one listen port**, delegated to the implementer in
   > review (*"you decide"*, 2026-09-25) and decided so. Each agent's derive writes
   > `http://127.0.0.1:<port>/agent/<name>/` as its base URL; an unknown or missing prefix is
   > refused, never routed to a default. One port keeps the listen-port collision surface as it
   > is today. An agent measured to drop a base URL's path gets its own port instead. Never a
   > header. Folded into [§6](#6-part-5--all-traffic-through-the-bridge-new-direction-design).

5. ✅ <a id="OQ-WG5"></a>**[OQ-WG5](#OQ-WG5): when all-traffic mode covers an agent with a
   native client (pi, codex, opencode), does the bridge replace the native path or sit beside it
   as a second, named profile? Which native wire protocols get pass-through routes?** Stakes:
   replacing discards what each native client does well (the credential chain, codex's Bedrock
   mode), and every pass-through protocol is another wire the bridge must frame.

   _Leaning:_ Beside, never replacing, following [OQ-BR5](bedrock-plumbing.md#OQ-BR5)'s "both".
   Chat-completions sign-only first (omp, pi), Responses (codex) later, Converse none. This
   revises [`bedrock-plumbing.md`](bedrock-plumbing.md)'s non-goal to "no bridge unless a profile
   asks for it".


   **Answer:**
   > **One path per profile: native, or the bridge** (ruled in review, 2026-09-25): *"a straight
   > up native mode and then there is no bridge. If the bridge is enabled, then we can have,
   > depending on configuration, the translated version of the native ones … we could just pass
   > the native straight through the bridge."* The leaning's "beside" is withdrawn: nothing runs
   > both paths at once. A bridged profile either passes the agent's native protocol through or
   > translates it. Every native wire gets a pass-through route, Converse included; the order is
   > [§8](#8-build-order)'s. Folded into [§6](#6-part-5--all-traffic-through-the-bridge-new-direction-design).

6. 💬 <a id="OQ-WG6"></a>**[OQ-WG6](#OQ-WG6): what does a profile write to put its agent on
   the bridge path?** Found 2026-09-25 while starting Part 3. [OQ-WG2](#OQ-WG2) ruled all-traffic
   mode a property of the profile, but no schema carries it. The bridge serves a route only when
   `routeFor` finds a composed provider whose `anthropic` endpoint is the jail's loopback, and
   that shape exists only because `packload.adaptEndpoints` writes an adapter's address into a
   protocol the provider does NOT already offer. The sign-only route has OpenAI chat-completions
   on both sides, so an `openai → openai` adapter can never be composed, and nothing today can
   say "front this provider's `openai` endpoint with a signing hop". Stakes: Part 3 and Part 5
   have no selection, so a handler built now would have no production call site.
   - **(a) A provider marker** (via [OQ-BR9](bedrock-plumbing.md#OQ-BR9)'s Bedrock pack) that
     `routeFor` reads as "sign-only upstream".
   - **(b) A profile field**, for example `via: "bridge"`. The agent's derive writes its base URL
     as the bridge's per-agent path ([OQ-WG4](#OQ-WG4)), and `routeFor` takes the upstream from
     the provider's own `openai` endpoint.

   _Leaning:_ **(b)**. It is [OQ-WG2](#OQ-WG2) and [OQ-WG4](#OQ-WG4) as already ruled, made
   concrete, and it needs no new provider vocabulary. (a) would tie selection to the provider
   redesign ([OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2)) that WG1 was ruled to avoid.

   <!-- vantage: oq id=OQ-WG6 leaning="(b): a profile field (e.g. via: bridge); the derive writes the agent's base URL as the bridge's per-agent path (WG4) and routeFor takes the upstream from the provider's own openai endpoint. It is WG2 and WG4 made concrete and needs no new provider vocabulary." -->

   **Answer:**
   > _(empty — fill in when decided)_

### 10.1 Ruled, moved here from bedrock-plumbing

- <a id="OQ-BR10"></a>[**OQ-BR10**](#OQ-BR10) (answered 2026-09-24 by
  [DIR-BR2](bedrock-plumbing.md#DIR-BR2)): **Should the wire bridge sign its own upstream
  requests with SigV4?** Yes: Part 1. It still touches
  [OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2), which [OQ-WG1](#OQ-WG1) sidesteps, and
  [OQ-BR8](providers-and-profiles-redesign.md#OQ-BR8), under whose leaning the bridge reads
  `aws-auth`'s adapter address from the composed provider row.
- <a id="OQ-BR16"></a>[**OQ-BR16**](#OQ-BR16) (ruled 2026-09-24): **Does the everything profile
  carry the Claude subscription too?** Yes, *"that would be amazing"*: Part 4. Amends
  [`claude-oauth-interposition.md`](../reference/claude-oauth-interposition.md)'s "model traffic
  is never touched" once built.
- <a id="OQ-BR17"></a>[**OQ-BR17**](#OQ-BR17) (ruled 2026-09-24): **Does the bridge fail over
  from the subscription to Bedrock when the subscription runs out?** Yes, opt-in: Part 4.
- <a id="OQ-BR18"></a>[**OQ-BR18**](#OQ-BR18) (ruled 2026-09-24): **May yolo's bridge carry a
  Claude subscription at all?** Yes, the maintainer's call on terms and company policy.

### Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-BR10 | **The bridge signs its own requests (option A: a standard-library signer pinned by AWS's test vectors).** Answered by DIR-BR2: SSO on the bridge route rules out option B, whose minted key lives at most an hour. A over C (vendoring the AWS SDK's signer) is the implementer's call, taken to keep the hermetic build free of an AWS module | 2026-09-24 | [§2](#2-part-1--the-bridge-signs-its-own-upstream-requests-ruled) (moved from [`bedrock-plumbing.md`](bedrock-plumbing.md)) | 2026-09-25: `internal/sigv4`, pinned by AWS's SigV4 test suite; the bridge signs through `internal/wirebridged/signing.go`. Not yet accepted by a live Bedrock |
| OQ-BR18 | **Yes, the bridge may carry a Claude subscription.** The maintainer's call on Anthropic's terms and company policy | 2026-09-24 | [§5](#5-part-4--the-subscription-arm-and-opt-in-failover-ruled) (moved) | — |
| OQ-BR16 | **The everything profile carries the subscription too**, forwarded untranslated with its own bearer, so one model list spans Teams and Bedrock. *"yes that would be amazing"* | 2026-09-24 | [§5](#5-part-4--the-subscription-arm-and-opt-in-failover-ruled) (moved) | — |
| OQ-BR17 | **Opt-in automatic failover, per model, from the subscription to Bedrock** on the subscription's usage-limit response, every switch disclosed. *"yes, opt in"*. Supersedes [agent-auth-modes OQ-1](agent-auth-modes.md#12-decision-ledger)'s deferral for this path | 2026-09-24 | [§5](#5-part-4--the-subscription-arm-and-opt-in-failover-ruled) (moved) | — |
| OQ-WG1 | **The signer keys on the upstream host now** (`bedrock-runtime.<region>.amazonaws.com`, `*.gateway.bedrock-agentcore.<region>.amazonaws.com`), **and re-keys on the provider's Bedrock marker once [OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2) gives one** — host patterns cannot know other people's configurations | 2026-09-25 | [§2.1](#21-behavior-the-signer-fixes) | 2026-09-25, (a): `sigv4.BedrockRuntimeRegion` decides at boot (`route.SignRegion`). (b), the marker re-key, waits on [OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2) |
| OQ-WG2 | **All-traffic mode is a property of the profile**, opt-in and off by default; one active profile per agent decides how it reaches the world | 2026-09-25 | [§6](#6-part-5--all-traffic-through-the-bridge-new-direction-design) | — |
| OQ-WG3 | **One list** (the picker's effective list after an `only`), **and a separate enforcement switch** on the profile, **default on**; off means the list only shapes pickers | 2026-09-25 | [§6](#6-part-5--all-traffic-through-the-bridge-new-direction-design) | — |
| OQ-WG4 | **A path prefix per agent on the one listen port**, written by each derive; an unknown prefix is refused; a port per agent only for an agent measured to drop a base URL's path (delegated, decided in review) | 2026-09-25 | [§6](#6-part-5--all-traffic-through-the-bridge-new-direction-design) | — |
| OQ-WG5 | **Each profile picks one path, native or the bridge**; a bridged profile passes the native protocol through or translates it; every native wire gets a pass-through route, Converse included | 2026-09-25 | [§6](#6-part-5--all-traffic-through-the-bridge-new-direction-design), [§8](#8-build-order) | — |
| DIR-WG1 | **An option to send every agent's model traffic through the wire bridge, so it can filter models and route each agent to its own upstream.** *"having an option to send all traffic through wire bridge or whatever for all endpoints so that we can do things like filter models — like when I'm using OpenRouter I'd want to set a specific set of models and not allow it to use other models … the only way to do that even if you're talking about pi … is through wire bridge … maybe we can identify specific agents so we can route them to different places … give them different endpoints … another design doc spawning out of this"*. Given in answer to [OQ-BR4](provider-credential-scope.md#OQ-BR4). Reverses [`wire-bridge.md`](../reference/wire-bridge.md#what-a-wire-bridge-is-not)'s "Not a gateway". Reopens, for the in-jail bridge only, the stance of [`sso-backed-bedrock.md` §9](sso-backed-bedrock.md#9-non-goals) ("not a model router"), whose non-goal still holds for the credential service that doc builds; with [OQ-BR10](#OQ-BR10), amends WB-D4. Built through [OQ-WG2](#OQ-WG2)–[OQ-WG5](#OQ-WG5). A direction, so no question id | 2026-09-25 | [§6](#6-part-5--all-traffic-through-the-bridge-new-direction-design) | — |

Part 2 rests on [OQ-BR11](bedrock-plumbing.md#OQ-BR11) (2026-09-24), and Part 3 on
[OQ-BR5](bedrock-plumbing.md#OQ-BR5) (2026-09-25) for pi and
[DIR-BR2](bedrock-plumbing.md#DIR-BR2) (2026-09-24) for oh-omp; all three are recorded in
[`bedrock-plumbing.md`'s Decision Ledger](bedrock-plumbing.md#decision-ledger), not here.

---

## 11. Evidence

Every AWS row was read 2026-09-24 unless marked, and no request was made to AWS for any of them.
Re-check instructions:
[`bedrock-plumbing.md` §14](bedrock-plumbing.md#14-evidence-and-how-to-re-check-it).

| Claim | Source |
| :--- | :--- |
| SigV4 and API keys both supported on `bedrock-runtime` and `bedrock-mantle`; *"Whether you use SigV4 or a Bedrock API key, the associated IAM principal must have permission for the required action"* | Bedrock endpoints page |
| *"The `bedrock-runtime` endpoint supports AWS SigV4 authentication and Amazon Bedrock API key authentication"*; a SigV4 `curl` example with `--aws-sigv4 "aws:amz:us-east-1:bedrock"` against `/openai/v1/chat/completions`; mantle *"supports Amazon Bedrock API key authentication, AWS credentials, and the OpenAI SDK"* | Chat Completions page |
| *"Amazon Bedrock API key (required for OpenAI SDK)"*, *"AWS credentials (supported for HTTP requests)"* | Responses API page |
| Short-term key *"Recommended for production use"*, long-term *"Recommended only for exploration"*, no deprecation; a short-term key is valid for the shorter of 12 hours or the generating principal's session, single-Region | Bedrock API keys page; How Bedrock API keys work |
| `bedrock:CallWithBearerToken` and `bedrock-mantle:CallWithBearerToken` control API-key use; *"To fully prevent all API key-based access, you must deny both actions"* | API keys permissions page |
| *"use temporary security credentials provided by AWS Security Token Service (AWS STS) service whenever possible"*; keys *"when your use case blocks the use of temporary AWS STS credentials"*; SCPs to block both actions. Posted 2025-10-17, updated 2026-07-01 | AWS Security Blog, *Securing Amazon Bedrock API keys* |
| Long-term key mechanics: one IAM user per key, two per user for rotation, expiry from one day to never | IAM user guide, read 2026-09-04 |
| The Messages API is listed on `bedrock-runtime`; path and stream framing unread | Bedrock endpoints page, read 2026-09-25 |

**Repo claims**, re-checkable in seconds:

```console
$ rg -n 'func adaptEndpoints|func routeFor|doUpstream' internal/packload internal/wirebridged
$ rg -n -i 'sigv4|aws4_request' internal cmd vendor/modules.txt   # expect no output
$ rg -n -F '[1m]' internal/wirebridge/request.go
$ rg -n 'api.anthropic.com' packs/*/pack.json                     # expect no output
```
