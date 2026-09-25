---
title: "The wire bridge as the jail's model gateway: signing, routing by model and by agent, failover, and allowlists"
date: 2026-09-25
status: draft
tags: [wire-bridge, bedrock, aws, sigv4, routing, failover, models, allowlist, providers, subscription]
summary: "What the wire bridge may do once it stands in front of an agent's model traffic. Four parts are ruled and unbuilt: it signs its own AWS requests with SigV4, routes claude's everything profile by model id, offers a sign-only OpenAI chat-completions route, and carries claude's subscription with opt-in per-model failover to Bedrock. A fifth part is a new direction: an opt-in mode that sends every agent's traffic through the bridge, so it can enforce a model allowlist and route each agent to its own upstream."
vantage:
  status-chip: true
---

# The wire bridge as the jail's model gateway

**The question this doc answers:** once the wire bridge stands between an agent and its model
provider, what may it do? It could sign for AWS, choose an upstream per model or per agent,
fail over when a subscription runs out, or refuse a model that is not on a list.

**Status:** DESIGN, 2026-09-25. Split out of [`bedrock-plumbing.md`](bedrock-plumbing.md) that
day, carrying its bridge questions with their ids unchanged. **Nothing of this doc is built.**
Part 1 (signing) is DECIDED and ready to build once [OQ-WG1](#OQ-WG1) is ruled. Parts 2–4 are
DECIDED and unbuilt. Part 5 is DESIGN. **MEASURED:** what the bridge does today
([§1](#1-what-the-bridge-does-today)), from the code at `5e8e64f6`, symbols re-checked at
`ee8154f2`. **UNMEASURED:** no request has been sent to Bedrock through the bridge with any
credential. Nobody has exercised runtime's Anthropic Messages route or the subscription's
usage-limit response. The SigV4 signer does not exist.

**Needs your ruling:**

- [OQ-WG1](#OQ-WG1): does the signer key on the upstream host rather than on a provider marker?
  *Leaning: yes. This unblocks the ruled signer.*
- [OQ-WG2](#OQ-WG2): is all-traffic mode opted into per profile, per agent or per jail?
  *Leaning: per profile, off by default.*
- [OQ-WG3](#OQ-WG3): what enforces a model allowlist? *Leaning: the effective list after an
  `only`, enforced by the bridge.*
- [OQ-WG4](#OQ-WG4): how does the bridge tell agents apart? *Leaning: a port or path per agent,
  never a header.*
- [OQ-WG5](#OQ-WG5): does the bridge replace an agent's native client or sit beside it?
  *Leaning: beside it, as a second named profile.*

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
| codex, opencode | yes | no, unless [OQ-WG5](#OQ-WG5) adds a profile |
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
  [OQ-WG1](#OQ-WG1)'s leaning. The earlier draft keyed this on a provider's Bedrock marker, which
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
It buys what no per-agent config can:

1. **A hard model allowlist.** pi's `enabledModels` is a default view, never a boundary: Tab
   shows every credentialed model, `--model` bypasses it, and switching checks auth only
   ([`provider-credential-scope.md` §2.4.1](provider-credential-scope.md#241-what-pis-enabledmodels-actually-constrains)).
   So for an OpenRouter user who wants a fixed set of models, the bridge is the only place a
   refusal can live ([OQ-WG3](#OQ-WG3)).
2. **Per-agent routing**: once the bridge can tell agents apart ([OQ-WG4](#OQ-WG4)), each agent
   gets its own upstream or endpoint.
3. **Parts 1–4 for agents that bypass the bridge today**: signing, model routing, failover.

**The allowlist is not a second list.** It is the effective list after a `models` `only`
([OQ-BR12](model-lists-and-pickers.md#OQ-BR12)), which the pickers already render, so what the
menu shows and what the bridge admits cannot drift. For OpenRouter and Kilo the maps an `only`
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
   vectors and lazy resolution. It has no dependency on the model-list work, and on its own it
   closes the SSO gap for copilot and the everything profile. It waits only on
   [OQ-WG1](#OQ-WG1). (Build step 8.1 in the source.)
2. **Routing by model id**, measuring runtime's Messages route first
   ([§3](#3-part-2--routing-by-model-id-for-claudes-everything-profile-ruled)). (Build step 8.3.)
3. **The sign-only route**
   ([§4](#4-part-3--the-sign-only-openai-chat-completions-route-ruled)), for oh-omp and pi.
4. **The subscription arm and failover**
   ([§5](#5-part-4--the-subscription-arm-and-opt-in-failover-ruled)), after measuring the
   usage-limit response.
5. **All-traffic mode** ([§6](#6-part-5--all-traffic-through-the-bridge-new-direction-design)),
   once [OQ-WG2](#OQ-WG2)–[OQ-WG5](#OQ-WG5) rule and
   [OQ-BR12](model-lists-and-pickers.md#OQ-BR12) gives the list an `only`.

---

## 9. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| **Mint a Bedrock API key for the bridge** ([option D](sso-backed-bedrock.md#4-five-options)) | **Superseded by [OQ-BR10](#OQ-BR10).** At most an hour over the narrowed session, frozen for the launch, and refused where `CallWithBearerToken` is denied. |
| **A host-side signing proxy** ([option E](sso-backed-bedrock.md#4-five-options)) | **Not needed.** An in-jail signer gets the refresh and the policy resilience without the host seeing any prompt. |
| **Vendor the AWS SDK's signer** ([OQ-BR10](#OQ-BR10)'s option C) | **Not taken; the implementer's call**, to keep the hermetic build free of an AWS module. The test vectors pin the standard-library signer instead. |
| **Two claude profiles, no routing by model** ([OQ-BR11](bedrock-plumbing.md#OQ-BR11)'s leaning) | **Overruled**: one session could never switch between Claude and an OpenAI model. |
| **Refuse a launch on an id outside the provider's `models`** ([`provider-switching.md`](provider-switching.md)) | **Still rejected as the enforcement point**; its intent moves to the bridge ([OQ-WG3](#OQ-WG3)). |
| **Sign on the provider's Bedrock marker** | **Leaning against ([OQ-WG1](#OQ-WG1))**: the marker ([OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2)) now waits on a redesign. |

---

## 10. Open Questions

1. 💬 <a id="OQ-WG1"></a>**[OQ-WG1](#OQ-WG1): does the signer decide to sign from the upstream
   host, never from a provider's Bedrock marker?** The patterns would be
   `bedrock-runtime.<region>.amazonaws.com` for the bridge and
   `*.gateway.bedrock-agentcore.<region>.amazonaws.com` for the search proxy. Stakes: the signer
   is ruled, but the draft keyed it on [OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2)'s
   marker, which the maintainer called *"a bigger decision than you're making it look"* and which
   now waits on a redesign. Unruled, this holds the signer back too.

   _Leaning:_ Yes. The host is a fact the bridge already holds at boot, and a signer keyed on it
   cannot mis-sign for a non-AWS upstream. The ruled signer ships without waiting on
   [OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2).

   <!-- vantage: oq id=OQ-WG1 leaning="Yes: sign on the upstream host pattern (bedrock-runtime.<region>.amazonaws.com; *.gateway.bedrock-agentcore.<region>.amazonaws.com), never on the provider's Bedrock marker. The host is a fact the bridge already holds at boot, a signer keyed on it cannot mis-sign for a non-AWS upstream, and it lets the ruled signer ship without waiting on OQ-BR2." -->

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 <a id="OQ-WG2"></a>**[OQ-WG2](#OQ-WG2): is all-traffic mode opted into per profile, per
   agent or per jail, and is it on by default anywhere?** Stakes: default-on changes every
   existing launch's traffic path, and a per-jail switch cannot say "pi through the bridge, codex
   native".

   _Leaning:_ Per profile, opt-in, default off, so existing launches are unchanged.
   [OQ-BR1](bedrock-plumbing.md#OQ-BR1)'s bridge-forcing profile is then the Bedrock instance of
   the same switch.

   <!-- vantage: oq id=OQ-WG2 leaning="Per profile, opt-in, default off, so existing launches are unchanged. OQ-BR1's bridge-forcing profile reads as the Bedrock instance of the same switch." -->

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 <a id="OQ-WG3"></a>**[OQ-WG3](#OQ-WG3): what enforces a model allowlist?** Candidates: a
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

   <!-- vantage: oq id=OQ-WG3 leaning="The effective list after a models `only` (OQ-BR12) is the one allowlist; the bridge refuses any other model id with a protocol-shaped (Anthropic or OpenAI) error naming the list. The bridge is the only hard enforcer for soft pickers (pi's enabledModels). provider-switching's launch-time refuse-unknown-id stays rejected as the enforcement point." -->

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 <a id="OQ-WG4"></a>**[OQ-WG4](#OQ-WG4): how does the bridge tell agents apart, so each can
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

   <!-- vantage: oq id=OQ-WG4 leaning="A loopback port or path prefix per agent, written by each agent's derive; never a header the agent could omit, because every agent can be given a base URL and not every agent can be given a header." -->

   **Answer:**
   > _(empty — fill in when decided)_

5. 💬 <a id="OQ-WG5"></a>**[OQ-WG5](#OQ-WG5): when all-traffic mode covers an agent with a
   native client (pi, codex, opencode), does the bridge replace the native path or sit beside it
   as a second, named profile? Which native wire protocols get pass-through routes?** Stakes:
   replacing discards what each native client does well (the credential chain, codex's Bedrock
   mode), and every pass-through protocol is another wire the bridge must frame.

   _Leaning:_ Beside, never replacing, following [OQ-BR5](bedrock-plumbing.md#OQ-BR5)'s "both".
   Chat-completions sign-only first (omp, pi), Responses (codex) later, Converse none. This
   revises [`bedrock-plumbing.md`](bedrock-plumbing.md)'s non-goal to "no bridge unless a profile
   asks for it".

   <!-- vantage: oq id=OQ-WG5 leaning="Beside, never replacing (the OQ-BR5 'both' pattern): chat-completions sign-only first (omp, pi), Responses (codex) later, Converse none. Revises ng-no-bridge-for-native to 'no bridge unless a profile asks for it'." -->

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
| OQ-BR10 | **The bridge signs its own requests (option A: a standard-library signer pinned by AWS's test vectors).** Answered by DIR-BR2: SSO on the bridge route rules out option B, whose minted key lives at most an hour. A over C (vendoring the AWS SDK's signer) is the implementer's call, taken to keep the hermetic build free of an AWS module | 2026-09-24 | [§2](#2-part-1--the-bridge-signs-its-own-upstream-requests-ruled) (moved from [`bedrock-plumbing.md`](bedrock-plumbing.md)) | — |
| OQ-BR18 | **Yes, the bridge may carry a Claude subscription.** The maintainer's call on Anthropic's terms and company policy | 2026-09-24 | [§5](#5-part-4--the-subscription-arm-and-opt-in-failover-ruled) (moved) | — |
| OQ-BR16 | **The everything profile carries the subscription too**, forwarded untranslated with its own bearer, so one model list spans Teams and Bedrock. *"yes that would be amazing"* | 2026-09-24 | [§5](#5-part-4--the-subscription-arm-and-opt-in-failover-ruled) (moved) | — |
| OQ-BR17 | **Opt-in automatic failover, per model, from the subscription to Bedrock** on the subscription's usage-limit response, every switch disclosed. *"yes, opt in"*. Supersedes [agent-auth-modes OQ-1](agent-auth-modes.md#12-decision-ledger)'s deferral for this path | 2026-09-24 | [§5](#5-part-4--the-subscription-arm-and-opt-in-failover-ruled) (moved) | — |
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
