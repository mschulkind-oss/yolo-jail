---
title: "The wire bridge as the jail's model gateway: signing, routing by model and by agent, failover, and allowlists"
date: 2026-09-25
status: accepted
tags: [wire-bridge, bedrock, aws, sigv4, routing, failover, models, allowlist, providers, subscription]
summary: "What the wire bridge may do once it stands in front of an agent's model traffic. Four parts are ruled: it signs its own AWS requests with SigV4 (built, keyed on the provider's platform marker since 2026-09-30), routes claude's everything profile by model id so Claude models reach Bedrock's own Messages route untranslated (built), offers a sign-only OpenAI chat-completions route (built), and carries claude's subscription with opt-in per-model failover to Bedrock (unbuilt). A Bedrock provider named by region alone is reached at runtime's own URL composed from the region (built 2026-09-30), so the shipped bedrock-bridge profile carries every agent. A fifth part, ruled 2026-09-25: a profile can send its agent's traffic through the bridge (native pass-through or translated) instead of the agent's own client, so the bridge can enforce the picker's model list (on by default; built 2026-09-30) and route each agent by a per-agent path prefix. The via route passes OpenAI chat-completions and Responses through (codex rides the second); Converse, the last native wire, is decided (WG-I36, 2026-09-30: pi's client sends a placeholder bearer and only the bridge signs) and not built; where pi's re-pointing row may live waits on pi-codex-provider-shadowing's OQ-3."
vantage:
  status-chip: true
---

# The wire bridge as the jail's model gateway

**The question this doc answers:** once the wire bridge stands between an agent and its model
provider, what may it do? It could sign for AWS, choose an upstream per model or per agent,
fail over when a subscription runs out, or refuse a model that is not on a list.

**Status:** DECIDED, 2026-09-25; the one question opened since, [OQ-WG8](#OQ-WG8), was decided as an implementation choice on 2026-09-30 ([WG-I36](#WG-I36)) — the SigV4 signer, [OQ-WG6](#OQ-WG6), [OQ-WG7](#OQ-WG7) and Part 3's sign-only route are BUILT, and the via route's Responses wire for codex is BUILT (2026-09-26, [WG-I20](#WG-I20)). Split out of [`bedrock-plumbing.md`](bedrock-plumbing.md) that
day, carrying its bridge questions with their ids unchanged. **Part 1 (signing) is BUILT,
2026-09-25** ([§2](#2-part-1--the-bridge-signs-its-own-upstream-requests-ruled)), with the
region-composed upstream URL and the re-key on the provider's platform marker built 2026-09-30
([§2.2](#22-how-the-bedrock-upstream-is-chosen-built-2026-09-30), [WG-I37](#WG-I37)–[WG-I39](#WG-I39)).
**Part 3 (the sign-only route) and Part 5's selection are BUILT,
2026-09-25** ([§4.1](#41-how-it-is-built)): a profile's `via` puts its agent on a per-agent route of
the bridge. **Part 2 (routing by model id) is BUILT, 2026-09-29** ([§3.1](#31-how-it-is-built),
[WG-I30](#WG-I30)–[WG-I35](#WG-I35)): on a Bedrock upstream, a model the provider's list declares
Anthropic's goes untranslated to runtime's own Messages route, and every other model is translated
as before. Since 2026-09-30 the shipped `bedrock-bridge` profile reaches it: the provider names a
region and no address, and the bridge composes runtime's URL from the region ([§8](#8-build-order),
step 1). **Part 5's allowlist is BUILT, 2026-09-30** ([§6.1](#61-how-the-allowlist-is-built),
[WG-I40](#WG-I40)–[WG-I43](#WG-I43)). Part 4 is DECIDED and unbuilt; Part 5's
four questions were ruled in review on 2026-09-25. **MEASURED:** what the bridge does today
([§1](#1-what-the-bridge-does-today)), from the code at `5e8e64f6`, symbols re-checked at
`ee8154f2`; the signer against AWS's published SigV4 test suite (31 cases) and through the real
bridge handler with the network stubbed. **SOURCED, 2026-09-29:** runtime's Messages route is
`/anthropic/v1/messages` and streams Anthropic server-sent events, read from AWS's docs and the
Anthropic SDK's source ([§3](#3-part-2--routing-by-model-id-for-claudes-everything-profile-ruled)).
**UNMEASURED:** no request has reached real Bedrock through the bridge with any credential, so AWS
has never accepted one of its signatures. Nobody has sent a request to runtime's Messages route or
seen the subscription's usage-limit response. The
via route is MEASURED in-process only: its tests run the real daemon, mux and signer against a
stubbed upstream, and the pi, oh-omp and opencode derives against the real shipped `derive.lua`.
No agent has sent a request through it, and whether `bedrock-runtime` serves Responses at
`/openai/v1/responses` is unread, so a codex route to Bedrock is signed but unproven. That pi,
opencode and codex keep a base URL's path is read
from their installed client sources, and oh-omp's from its published package
([§4.1](#41-how-it-is-built)); none is observed on the wire.

**Needs your ruling:** nothing here.

- [OQ-WG8](#OQ-WG8) — on pi's Converse pass-through, does pi's own client still use the AWS
  credential? Decided 2026-09-30 as an implementation choice ([WG-I36](#WG-I36)): (b), pi sends a
  placeholder bearer and only the bridge signs. Keeping the credential out of pi's environment is
  a separate matter, which [OQ-CN6](provider-credential-scope.md#OQ-CN6) decides. Converse is still
  not built: whether pi's re-pointing row may sit on pi's built-in `amazon-bedrock` key is
  [`pi-codex-provider-shadowing.md` OQ-3](pi-codex-provider-shadowing.md#OQ-3)'s, which is open.

[OQ-WG1](#OQ-WG1)–[OQ-WG7](#OQ-WG7) are settled ([Decision Ledger](#decision-ledger)); WG6 and WG7 are built.
[OQ-WG1](#OQ-WG1)'s follow-up, re-keying the signer on the provider's Bedrock marker
([OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2)'s `platform`), is built 2026-09-30
([WG-I37](#WG-I37)), and so is [OQ-WG3](#OQ-WG3)'s allowlist ([WG-I40](#WG-I40)). Part 2, which passes Claude models through
untranslated so prompt caching survives, needed no ruling: it is
[OQ-BR11](bedrock-plumbing.md#OQ-BR11)'s, released for build as [WG-I25](#WG-I25) and built
2026-09-29 ([§3.1](#31-how-it-is-built)).

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
Anthropic models and every other Bedrock model. [OQ-BR1](bedrock-plumbing.md#OQ-BR1) named it
`bedrock-bridge`, and [OQ-BR9](bedrock-plumbing.md#OQ-BR9) put one provider, `bedrock`, behind
both profiles (ruled and shipped 2026-09-29; the route itself reaches Bedrock since 2026-09-30, [WG-I39](#WG-I39)).
What a provider and a profile should mean at all is
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
  `POST /v1/messages` is served. Since 2026-09-29 a Claude model on a Bedrock upstream skips
  translation and keeps all of it but `count_tokens` ([§3.1](#31-how-it-is-built)).
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
upstream is Bedrock's gets `route.SignRegion` at boot, and `doUpstream` signs after every header
is set. The credential order, lazy single-flight resolution, the three failure statuses, and the
expired-signature retry are built as [§2.1](#21-behavior-the-signer-fixes) states. Which upstream
is Bedrock's was keyed on its host until 2026-09-30, when the re-key on the provider's `platform`
and the region-composed upstream URL were built ([§2.2](#22-how-the-bedrock-upstream-is-chosen-built-2026-09-30)).
One thing is not:
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

- **When it signs:** for an upstream whose provider declares the Bedrock platform, at any
  `https` address, and for an upstream whose host matches a signing pattern when the provider
  declares no such platform: [OQ-WG1](#OQ-WG1)'s ruling and its follow-up, built as
  [WG-I37](#WG-I37). It never sends both a signature and a bearer, and never logs a credential.
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
  as a literal. A pack cannot know the region. Built as [WG-I38](#WG-I38) and [WG-I39](#WG-I39),
  which read the region the served agent was handed when the provider declares none.

**Risk R6.** A signer bug fails every bridged request with a 403, for every user alike. The test
vectors pin the signer before any live request, the 403 body names the signature mismatch, and
the bridge log carries it.

### 2.2 How the Bedrock upstream is chosen (built 2026-09-30)

**BUILT 2026-09-30**, from [OQ-WG1](#OQ-WG1)'s follow-up and [§8](#8-build-order) step 1. Three
implementation decisions:

1. <a id="WG-I37"></a>**[WG-I37](#WG-I37)**: **the signer keys on the provider's `platform`, and
   the host rule decides only for a provider that declares no Bedrock platform.** A provider whose
   `platform` is `aws-bedrock` ([OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2)'s marker) is
   signed at whatever `https` address it names: a FIPS or VPC endpoint, or a corporate proxy, the
   configurations [OQ-WG1](#OQ-WG1)'s ruling named (*"A is cheating and I have no idea what other people's
   configurations are"*). A provider declaring no platform, or one the bridge does not know, keeps
   the host rule, signed exactly at `bedrock-runtime.<region>.amazonaws.com`, because an unknown
   platform is inert ([OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2)'s ruling) and dropping
   the rule would stop signing a provider that works today. A Bedrock provider at a plain `http`
   address is not served: a SigV4 signature can be replayed for minutes by whoever sees it.
   **Why the union and not the marker alone:** the marker fixes every miss the ruling named, and
   the host rule matches nothing but AWS's own runtime host, so keeping it widens nothing. A
   provider's `platform` and its addresses are user-scope only
   ([PP-D7](providers-and-profiles-redesign.md#PP-D7)), so a repository cannot send a signature
   anywhere. Lives in `wirebridged.bedrockSigning`.
2. <a id="WG-I38"></a>**[WG-I38](#WG-I38)**: **the region is the runtime host's, else the
   provider's `region`, else the served agent's `AWS_REGION`, then `AWS_DEFAULT_REGION`, read at
   boot.** The host's region comes first because the signature's scope must name the endpoint's
   region. The environment's comes last and at boot, from the same key channel as the credential,
   because that is where the launch delivers it: to the agent the route serves, including the
   region the credential gate's region fill reads from `~/.aws/config`
   ([BR-D20](bedrock-plumbing.md#BR-D20)), and never into the composed table, which is jail-wide.
   The two variables and their order are the AWS SDK's, named in `sigv4.RegionVars` beside the
   credential variables: the bridge is an AWS client here, like codex's and Claude Code's own, and
   each of those reads the same two. A value without a region's shape is refused by name, never
   skipped and never used, so no variable can name a host ([§7](#7-what-the-bridge-still-refuses)).
   With no region the adapter route idles and a via route answers 503, each naming the variables
   it read; the launch's region pre-flight ([OQ-BR6](bedrock-plumbing.md#OQ-BR6)) refuses that
   launch first in the ordinary case. Lives in `wirebridged.envRegion` and `route.resolveRegion`.
3. <a id="WG-I39"></a>**[WG-I39](#WG-I39)**: **a Bedrock provider that names no address is
   reached at runtime's own `/openai/v1`, and claude and copilot reach it through an adapter
   address composed for a via profile.** The via route's one upstream for both wires is
   `https://bedrock-runtime.<region>.amazonaws.com/openai/v1`, which codex's own
   `amazon-bedrock-runtime` client also uses for Responses. For the adapter route the provider
   needs an anthropic address, so `packs/wire-bridge`'s `openai → anthropic` adapter declares
   `"from_platforms": ["aws-bedrock"]` (`packdecl.AdapterPair.FromPlatforms`, refused on a pack
   with no service), and `packload.adaptEndpoints` gives such a provider the adapter's address,
   marked `"for_via": "wire-bridge"` (`packload.ForViaKey`). The mark means the address is for a
   profile routing through the bridge alone. The daemon serves it only for such a profile
   (`routeFor`), copilot's derive composes nothing on one without it, the protocol gate never
   refuses an agent over it (`ResolveProtocol`), and the profile line does not count it for a
   profile without the via (`packload.EndpointsForProfile`). **Why marked, and not an ordinary
   adapter address:** claude on `-p bedrock` runs its own Bedrock client, and a bridge serving it
   anyway would need a credential and a region the agent's own client may take from somewhere the
   bridge does not read (an `AWS_PROFILE` and `~/.aws`), so the witness would refuse a launch that
   works today; and codex, opencode and pi, which speak no anthropic, would be refused `-p bedrock`
   over an address they never use. **Why not the via listener:** claude and copilot reach the
   bridge through the adapter routes by design ([§4.1](#41-how-it-is-built)'s table), and Part 2's
   Messages pass-through is the adapter route's. **What this leaves:** [OQ-BR1](bedrock-plumbing.md#OQ-BR1)'s
   ruling reads *"through the wire bridge where it has none"* for `-p bedrock` too, and copilot on
   `-p bedrock` still reaches nothing, as before, and says so on the profile line. Serving it there
   needs the serve decision to know which agents have their own Bedrock client, which the tables
   the daemon boots from do not carry; `bedrock-bridge` is copilot's and oh-omp's Bedrock profile
   meanwhile. copilot under it starts on the list's first model, the rule
   [OQ-ML2](model-lists-and-pickers.md#OQ-ML2) gives a provider with no `default`. Lives in
   `wirebridged.regionalBedrock`, `packload.adaptEndpoints` and `packs/copilot/derive.lua`.

The three are MEASURED in-process only: the production boot over the shipped packs' composed table,
each served agent's key channel, and a stubbed upstream that records what was sent. No request has
reached AWS, and whether runtime serves Responses at `/openai/v1/responses` is still read from
codex's binary, not observed.

---

## 3. Part 2 — routing by model id, for claude's everything profile (ruled)

[OQ-BR11](bedrock-plumbing.md#OQ-BR11), ruled in [`bedrock-plumbing.md`](bedrock-plumbing.md),
gives claude both a native profile and an everything profile. That ruling is the authority; this
part is the mechanism. One claude process has one transport, so the everything profile points
claude at the bridge (`ANTHROPIC_BASE_URL`, never `CLAUDE_CODE_USE_BEDROCK`), and **the bridge
routes by model id**:

- **An Anthropic id** is forwarded **untranslated** to runtime's own Anthropic Messages route,
  signed like every request, so `cache_control`, `thinking` and `count_tokens` survive (as built,
  `count_tokens` does not: runtime documents none on that route, [WG-I35](#WG-I35)). The
  bridge knows an id is Anthropic's from the list entry's declared **vendor** (the model's
  maker, coined in [`bedrock-plumbing.md`](bedrock-plumbing.md), defined now in
  [`model-lists-and-pickers.md`](model-lists-and-pickers.md)), never by parsing the id.
- **Every other id** is translated to chat-completions, as today.

This **amends** the bridge's rule that it dials only the upstream selected at boot
([`wire-bridge.md`](../reference/wire-bridge.md#lifecycle-and-failure-behavior)): the everything
route has two upstreams under one provider, chosen per request.

**The measurement, SOURCED 2026-09-29: the route is `/anthropic/v1/messages`, and it streams
Anthropic SSE, so the bridge relays it byte for byte and re-frames nothing.** This was the
builder's first measurement, not a choice left to rule. It was read, and AWS was never called:

- **The path and the body.** AWS's *Inference using Anthropic Messages API* page gives runtime's
  base URL as `https://bedrock-runtime.{region}.amazonaws.com/anthropic`, and its `curl` example
  posts the first-party body, with `model` and `stream` in it, to `/anthropic/v1/messages` with an
  `anthropic-version: 2023-06-01` header. InvokeModel's body is different: it takes
  `anthropic_version` in the body and the model in the path.
- **The stream framing.** The same page streams from that route with the plain Anthropic SDK,
  `Anthropic(base_url=".../anthropic")` and `client.messages.stream`. That client's decoder is the
  SSE one. In the Anthropic Python SDK only the InvokeModel client, `AnthropicBedrock`, which
  rewrites a request to `/model/{id}/invoke-with-response-stream`, overrides `_make_sse_decoder`
  with an AWS event-stream decoder (`lib/bedrock/_client.py`, `_stream_decoder.py`). The Messages
  client for AWS's other endpoint family, `AnthropicBedrockMantle`, keeps the default SSE decoder
  (`lib/bedrock/_mantle.py`). The binary event stream is InvokeModel's framing, not this route's.
- **The credential.** The endpoints page marks SigV4 and Bedrock API keys as both supported on
  `bedrock-runtime`. The Messages page's API-key example sends the key as `x-api-key`. No example
  signs `/anthropic/v1/messages` with SigV4, so the bridge signing it for the service `bedrock`,
  the name runtime signs its other routes with, is INFERRED.
- **What the route does not serve.** No `count_tokens` is documented on runtime's Messages route.
  AWS's token-counting page says a Claude model offered only through cross-Region inference has
  no CountTokens on runtime at all, and points to the `bedrock-mantle` endpoint, which yolo does
  not ship ([DIR-BR3](bedrock-plumbing.md#DIR-BR3)).

A measurement read from documentation can still be wrong. So an answer framed as AWS's binary
event stream is refused by name rather than relayed ([WG-I30](#WG-I30)), and the first real turn
confirms or refutes the rest ([§3.1](#31-how-it-is-built)).

**Risk R7.** Translation loses something a non-Anthropic model needs: tool-call fidelity,
reasoning, or a vendor's streaming quirk. The bridge already fails closed on an unknown block
type ([WB-D5](../reference/wire-bridge.md#wb-d5)). Measure one turn per vendor in the org's list
before shipping that vendor in a company pack.

**Done-condition** (carried from [`bedrock-plumbing.md`](bedrock-plumbing.md)'s done-condition
6). It waited on [OQ-BR9](bedrock-plumbing.md#OQ-BR9) and
[OQ-BR12](model-lists-and-pickers.md#OQ-BR12), both ruled 2026-09-29, and on
[OQ-BR13](model-lists-and-pickers.md#OQ-BR13), directed the same day, so nothing it waits on is
open ([WG-I25](#WG-I25)). Claude's everything profile completes one
turn against a non-Anthropic model on runtime (a DeepSeek or Qwen id), once under each of the
three credentials, and copilot does the same. Under the SSO credential the turn still succeeds
after the first credential set expires, with no relaunch. The same session then switches to an
Anthropic model on the list and completes two turns: the second reports
`cache_read_input_tokens` above zero, and a turn with thinking on shows a thinking block. It is a
manual runbook on a real host; automated tests never make API calls.

### 3.1 How it is built

**BUILT 2026-09-29**, released as [WG-I25](#WG-I25). The route this part changes is the adapter
route: the `anthropic → openai` translation on its own port, which claude reaches through
`ANTHROPIC_BASE_URL` and copilot through its preferred anthropic endpoint. When that route's
upstream is Bedrock's (the signer's own test, `bedrockSignRegion` at first and `bedrockSigning` since [WG-I37](#WG-I37)), the route holds
a second upstream, runtime's Messages route on the same host. Each request's `model` picks one:

| Link | What it does | Lives in |
| :--- | :--- | :--- |
| The list | at boot, every id the provider's list declares vendor `anthropic` for, read off the composed entry (`models.<alias>` is the id, `model_options.<alias>.vendor` its maker) | `wirebridged.anthropicModelIDs`, called from `wirebridged.routeFor` into `route.AnthropicModels` |
| The handler | the signed chat handler gains the Messages pass-through, under the same signer and credential chain | `wirebridged.newSignedChatHandler`, `wirebridged.newMessagesPassthrough`, from `wirebridged.adapterHandler` |
| The split | a request whose `model` (its `[1m]` suffix trimmed) is on that set goes untranslated; every other request is translated as before | `bridgeHandler.ServeHTTP`, `messagesPassthrough.claims` |
| The pass-through | the body as sent to `https://<runtime host>/anthropic/v1/messages`, with `anthropic-version` and `anthropic-beta`; the answer relayed byte for byte and flushed | `messagesPassthrough.serve`, `.do`, `.relay` |
| The log | the serve line names the Messages URL and the ids it carries; each request line says when it went untranslated; never a body | `wirebridged.messagesServeNote`; `bridgeHandler.ServeHTTP` |

The build made six implementation decisions. Their ids start at 30 because the `bedrock` pack's
concurrent build numbered its own from WG-I26 on ([WG-I26](#WG-I26)):

1. <a id="WG-I30"></a>**[WG-I30](#WG-I30)**: **the framing is relayed, and the other framing is
   refused by name.** The stream is Anthropic SSE ([§3](#3-part-2--routing-by-model-id-for-claudes-everything-profile-ruled)),
   so the bridge copies it byte for byte and decodes nothing. A 2xx answer whose `Content-Type`
   is `application/vnd.amazon.eventstream` gets an Anthropic-shaped 502 naming that framing, and
   the log says the same. **Why:** the framing is read, not observed. If the read is wrong, the
   first request says so in words, and claude never receives binary frames it cannot parse.
2. <a id="WG-I31"></a>**[WG-I31](#WG-I31)**: **one credential chain for both upstreams, and the
   key goes as `x-api-key` on this route.** The pass-through signs with the route's own
   `bedrockSigner`, so the chat-completions and Messages upstreams share one credential cache,
   one expiry refresh and the same three failure statuses ([§2.1](#21-behavior-the-signer-fixes)).
   When `AWS_BEARER_TOKEN_BEDROCK` is the only source, it travels as `x-api-key: <key>`, the form
   AWS's Messages page documents for this route, where runtime's OpenAI-compatible routes take
   `Authorization: Bearer`. A signature and a key never travel together. The agent's own
   `Authorization` and `x-api-key` carry the caller token, and are never copied upstream
   ([WB-D18](../reference/wire-bridge.md#wb-d18)).
3. <a id="WG-I32"></a>**[WG-I32](#WG-I32)**: **a refusal keeps its status, in Anthropic's
   shape.** The route speaks claude's protocol, so its statuses already mean what an Anthropic
   client reads them as, and a 529 or a 429 reaches claude as itself. The translating route maps
   5xx to 502 because an OpenAI status does not. An Anthropic-shaped error body is relayed as
   sent. Any other, such as AWS's `{"message": …}` envelope, is put into the Anthropic shape with
   AWS's message and the status's Anthropic error type. `Retry-After`, `x-should-retry` and the
   request ids are copied back.
4. <a id="WG-I33"></a>**[WG-I33](#WG-I33)**: **a truncated stream aborts the agent's connection,
   and only the wait for headers is bounded.** A body read that fails, or an SSE stream that ends
   before `message_stop` or an `error` event, aborts the connection with `http.ErrAbortHandler`,
   after a log line naming the model, the byte count and the cause. The watch follows the
   answer's own `Content-Type`, not the request's `stream` flag: an SSE answer is checked for its
   closing event whatever the agent asked for, and a JSON answer is relayed whole with nothing to
   watch. The agent closing its own request is no cut and is logged as none. The bridge watches
   only the head of each line for those two events, so a text delta that happens to contain the
   words does not count. The translating route answers the same case with an `error` event it writes itself,
   but here the bytes are the upstream's and the cut may fall inside an event, so WG-I24's answer
   is the one that cannot corrupt the stream. The wait for response headers is bounded by
   `viaHeaderTimeout`, through the via route's own `sendHeaderBounded`. The body's read is not
   bounded, since a long thinking turn can outlast any whole-exchange timeout, which the
   translating route still has.
5. <a id="WG-I34"></a>**[WG-I34](#WG-I34)**: **the id is looked up, as the list spells it.** A
   request's `model` is matched against the list's ids after trimming Claude's `[1m]` suffix,
   which is Claude Code's client spelling of the 1M-context variant and never a wire id
   ([`providers.md`](../reference/providers.md)). When the suffix was there, the forwarded body's
   `model` is the trimmed id. That is the one body edit the pass-through makes, and the translator
   makes it too. The list's own ids are keyed the same way, so a list written for claude that
   spells an id `…[1m]` names that wire id. Only a declared vendor counts: an id two aliases
   declare different vendors for is none of them, and it stays translated with a log line naming
   it, while an alias that declares no vendor neither adds nor removes one, so a second, bare
   alias for a listed Claude id leaves it on the pass-through. An id the list does not name, and
   a listed id no alias declares a vendor for, are translated, so an Anthropic model reaches the
   pass-through only by being listed with `"vendor": "anthropic"`, by a pack's `model_options`
   or by a user's object-form entry (`vendor` joined the user's model entry in the
   [OQ-BR9](bedrock-plumbing.md#OQ-BR9) build). When a user's config points a pack's alias at
   another id, composition drops the pack's vendor for that alias
   (`packload.dropRepointedVendors`), because it named the maker of the pack's model; the vendor
   the user's own entry declares, if any, is the one the alias keeps.
6. <a id="WG-I35"></a>**[WG-I35](#WG-I35)**: **`count_tokens` stays refused, for Anthropic models
   too.** [§3](#3-part-2--routing-by-model-id-for-claudes-everything-profile-ruled)'s text expected
   it to survive the pass-through, but runtime documents no `count_tokens` on its Messages route,
   and AWS says a Claude model that launched with cross-Region inference only has no CountTokens
   on runtime at all. The one documented path for such a model is on `bedrock-mantle`, which yolo
   does not ship. An undocumented route could
   answer with something other than the 404 that sends claude to its own estimator
   ([WB-D14](../reference/wire-bridge.md#wb-d14)), so the refusal stands until a real run shows
   runtime serving it.

**What is not built, or not closed:**

- ~~**The shipped `bedrock-bridge` profile does not reach this route.**~~ It does since
  2026-09-30 ([§2.2](#22-how-the-bedrock-upstream-is-chosen-built-2026-09-30), [WG-I39](#WG-I39)):
  the bridge composes runtime's URL from the region, and the Messages route sits on the same host.
  The shipped list declares Claude Opus 5.5's vendor, so it goes untranslated. A provider of the
  Bedrock platform at an address of its own gets its Messages route on that address's host
  ([WG-I37](#WG-I37)).
- **The `anthropic-beta` values claude sends are forwarded as sent**, and nobody knows which of
  them runtime accepts. On `ANTHROPIC_BASE_URL`, claude sends the betas it sends the first-party
  API, and a value runtime rejects fails the request with a 400, which the bridge relays
  ([WG-I32](#WG-I32)). Only a real turn settles it.
- **No request has reached the route.** The tests run the production boot, handler, signer and
  relay against a fake upstream that serves the documented format.

---

## 4. Part 3 — the sign-only OpenAI chat-completions route (ruled)

The **sign-only route** *(coined here)* is a second bridge route beside the Anthropic one: an
OpenAI chat-completions endpoint on the jail's loopback that forwards the request unchanged and
adds only the signature. It is the same signer at the same seam, with no translation step.
**BUILT 2026-09-25** as the bridge's **via route** *(coined here: the route a profile's `via`
selects)*; [§4.1](#41-how-it-is-built) says how. **Its authority is two rulings, not one:** [OQ-BR5](bedrock-plumbing.md#OQ-BR5)
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

Since 2026-09-26 the same route also passes **OpenAI Responses** through, for codex, which speaks
nothing else ([WG-I20](#WG-I20)).

### 4.1 How it is built

**BUILT 2026-09-25**, with Part 5's selection ([OQ-WG6](#OQ-WG6), [OQ-WG7](#OQ-WG7)). A user
config or a pack ships a profile with `via: "wire-bridge"`; selecting that profile for an agent
puts the agent on its own route of the bridge:

```jsonc
// ~/.config/yolo-jail/config.jsonc
"profiles":     { "pi-zai": { "provider": "zai", "via": "wire-bridge" } },
"use_profiles": { "pi": "pi-zai" }
```

pi then talks to `http://127.0.0.1:8216/agent/pi`, and the bridge forwards each request to zai's
own `openai` endpoint with zai's key. The chain, one link per [OQ-WG7](#OQ-WG7) part:

| Link | What it does | Lives in |
| :--- | :--- | :--- |
| The field | `via` on a pack `kind: "profile"` and on a user `profiles` entry, a pack name; a user's value wins. It is a field, never a provider option | `packdecl.ProfileContribution.Via`, `packdecl.ValidPackName`; `config.checkProfileEntry` |
| The address (b) | the service's declared `via_address`, loopback `http` with a port and no path | `packdecl.ServiceContribution.ViaAddress`, `packdecl.ViaAddressProblem`; `packs/wire-bridge/pack.json` |
| Bringing the pack in (c) | an active via profile adds the pack its `via` names, like a live need, and prints a cause line (`+ wire-bridge (via of profile pi-zai, active for pi)`) | `packload.Selection.Close`, which runs `packload.ResolveVias`, called from `stagePacks` through `run.Options.launchSelection` ([WG-I11](#WG-I11)) |
| Resolution | the resolved profile carries `Via` and `ViaBase` (the `via_address`, empty when the pack is not in the launch) and crosses in `YOLO_PROFILES` | `packload.ResolveProfiles`, `packload.ProfilesWireTable`; `entrypoint.Env.LoadProfiles` |
| Each agent's URL (d) | `ctx.via_url` = `<via_address>/agent/<agent>`, set only for the agent whose active profile has via | `packload.ViaURLFor`; `entrypoint.surfaceSelectionFor`; `luahook.DeriveCtx.ViaURL` |
| The derives | pi, oh-omp and opencode write `ctx.via_url` as the SELECTED provider's base URL, speaking chat-completions there; codex writes it as that provider's `base_url`, speaking Responses, with no `env_key` ([WG-I22](#WG-I22)), for a provider it can reach or, since 2026-09-29, a Bedrock one ([WG-I26](#WG-I26)); every other provider row is untouched | `packs/pi/derive.lua`, `packs/omp/derive.lua`, `packs/opencode/derive.lua`, `packs/codex/derive.lua` |
| The routes (a, b) | one listener on the via address serves `/agent/<name>/` per via agent; the adapter routes keep their own ports, so claude's URL and route do not move | `wirebridged.viaRoutesFor`, `wirebridged.planFor`, `wirebridged.servePlan` |
| The pass-through | the body and the query cross unchanged; the upstream is the provider's chat-completions or Responses base, whichever the request's path names ([WG-I20](#WG-I20)), plus the path remainder, escaped ([WG-I23](#WG-I23)); SSE is copied chunk by chunk and flushed; a stream the upstream cuts short aborts the agent's connection, and only the wait for headers is bounded ([WG-I24](#WG-I24)); errors are OpenAI-shaped | `wirebridged.viaUpstreams`, `wirebridged.viaWireSplit`, `wirebridged.passthroughHandler` |
| Credentials (e) | per upstream: the provider's `api_key_env_name` from the key channel, or the SigV4 chain when the upstream is Bedrock's (an exact `bedrock-runtime` host, or since 2026-09-30 any `https` address of a provider whose `platform` says so, [WG-I37](#WG-I37)). An upstream with no credential idles with a 503 naming what it needs, and the others serve | `wirebridged.viaHandlerFor`, `wirebridged.viaUpstreamHandler`, `wirebridged.bedrockSigning` |
| The prefix refusal | a path without a known `/agent/<name>/` gets a 404 OpenAI error listing the served agents; nothing is routed to a default. A path after the prefix that is not canonical gets a 404 too ([WG-I23](#WG-I23)) | `wirebridged.viaMux`, `wirebridged.canonicalViaTail` |

**Which agents are wired.** Each agent must take a base URL and keep its path, or the prefix is
lost ([§8](#8-build-order), step 5):

| Agent | Wired | Why |
| :--- | :--- | :--- |
| pi | yes | its `openai-completions` client is the OpenAI SDK, which builds `new URL(baseURL + path)` (read in the installed `openai` 6.40.0 `buildURL`) |
| opencode | yes | `@ai-sdk/openai-compatible` builds `${baseURL}${path}` (read in the installed opencode 1.18.32 bundle) |
| oh-omp | yes | its `openai-completions` client is the OpenAI SDK, which builds `new URL(baseURL + path)` (read 2026-09-26 in the published `@oh-labs/oh-omp-linux-x64` 0.15.3 binary's bundled `openai` 6.33.0 `buildURL`, fetched with `npm pack` and not run). No derive change |
| codex | yes, since 2026-09-26 | a custom provider's client builds `format!("{base}/{path}")` (read in codex 0.157.0's `codex-api` `Provider::url_for_path`, the version installed here), and it speaks Responses, which the via route now passes through ([WG-I20](#WG-I20)) |
| claude, copilot | no, by design | they reach the bridge through the adapter routes, which (a) leaves unchanged |

**Implementation decisions.** [OQ-WG7](#OQ-WG7) was ruled an implementation decision. These are
the mechanism choices its build made, each the one answer that made the ruled behavior work:

1. <a id="WG-I1"></a>**[WG-I1](#WG-I1)** — the via pair crosses in `YOLO_PROFILES` under the
   reserved keys `_via` and `_via_base`. A provider option may be named `via`, so the plain
   spelling could collide.
2. <a id="WG-I2"></a>**[WG-I2](#WG-I2)** — the via address is a `via_address` field on the
   `service` contribution, not a fourth `adapter`: an adapter composes an address into a provider
   that lacks a protocol, and a via route fronts one it already has. Its value is
   `http://127.0.0.1:8216`, the next port after the Codex adapter's `:8215`.
3. <a id="WG-I3"></a>**[WG-I3](#WG-I3)** — one daemon serves every listener, and its endpoint
   file names the first one bound: the adapter route's when one serves, the via address
   otherwise.
4. <a id="WG-I4"></a>**[WG-I4](#WG-I4)** — the upstream is the provider's `openai` base URL plus
   whatever path the agent sent after its prefix. Nothing is hard-coded, so `/models` or
   `/embeddings` pass through as `/chat/completions` does.
5. <a id="WG-I5"></a>**[WG-I5](#WG-I5)** — the agent's own `Authorization` is never forwarded,
   including on a route with no credential of its own ([WB-D4](../reference/wire-bridge.md#wb-d4)).
   pi and opencode always send one, and oh-omp does for a keyed provider. For a keyed provider
   their via row carries the provider's own key reference (`${VAR}` for pi, `{env:VAR}` for
   opencode, the variable's name for oh-omp), so the agent sends the real key to the bridge,
   which drops it. For a keyless one pi and
   opencode send the `local` placeholder their derives give a loopback base URL, because pi drops
   a provider whose row has no key, and oh-omp's row says `auth = "none"`. **Corrected
   2026-09-26:** this item said the `local` placeholder was what they always sent.
   **Superseded by [WB-D18](../reference/wire-bridge.md#wb-d18) (2026-09-28):** every via row,
   keyed provider or keyless, now names the bridge's caller token variable,
   `YOLO_SERVICE_WIRE_BRIDGE_TOKEN`, and never the provider's key. Caller token is the term
   [`wire-bridge.md`](../reference/wire-bridge.md#caller-authentication) coins: a random
   per-launch secret the bridge demands of every request. So pi, opencode and oh-omp send the
   token, the bridge checks it and drops it, and the provider's key never leaves the bridge. The
   first sentence still holds: no inbound `Authorization`, the token included, is forwarded
   upstream.
6. <a id="WG-I6"></a>**[WG-I6](#WG-I6)** — a via route is served only for a provider whose
   `openai` endpoint is chat-completions (`wire_api` unset or `openai-chat-completions`). Any other
   is skipped with its reason in the daemon log. **Amended 2026-09-26 by [WG-I20](#WG-I20):** a
   provider offering chat-completions or Responses is served.
7. <a id="WG-I7"></a>**[WG-I7](#WG-I7)** — a via may name only an embedded official pack, the
   needs rule ([WB-D9](../reference/wire-bridge.md#wb-d9)), and one
   whose service declares a `via_address`. Either failure refuses the launch naming the profile.
8. <a id="WG-I8"></a>**[WG-I8](#WG-I8)** — at the host notch via is inert. `yolo host` runs
   neither closure, so the service pack is absent, `ViaBase` is empty and the agent keeps its own
   client. `ctx.via_url` is only ever read by file derives, which the host notch does not feed
   from `YOLO_PROFILES`. **Amended 2026-09-26 by [WG-I12](#WG-I12):** the service pack is present
   at the host when a user lists `wire-bridge` in `packs`, so the host now clears every via
   address instead. **Revised 2026-09-28 by
   [notch-convergence item 6](../plans/notch-convergence.md#tier-2--one-selection-p1-p2):** the host
   runs both closures through the one selection function every notch calls, so a selected pack's
   `needs` joins the service pack there too; WG-I12's clearing is what keeps via inert.
9. <a id="WG-I9"></a>**[WG-I9](#WG-I9)** — `stagePacks` reads the launch's config through a
   field on the run options (`stagingCfg`) rather than a new parameter, so its many test call
   sites stand; an empty config computes the same `use_profiles` defaults a launch would.

The Responses build (2026-09-26) made three more. Their ids start at 20 because a concurrent
track holds the ids from 10:

10. <a id="WG-I20"></a>**[WG-I20](#WG-I20)** — **a via route carries two upstreams, and the
    request's path picks one.** A route's chat-completions upstream is the provider's `openai`
    endpoint when its `wire_api` is unset or `openai-chat-completions`. Its Responses upstream is
    the provider's `openai-responses` endpoint when declared, else its `openai` endpoint when its
    `wire_api` is unset or `openai-responses`, which is the preference `packs/codex/derive.lua`
    reads. A path of `/responses` or below goes to the Responses upstream, `/chat/completions` or
    below to the chat-completions one, and any other path (`/models`) to the chat-completions
    upstream, or to the Responses one when that is all the provider offers. A path naming a wire
    the provider does not declare gets an OpenAI-shaped 404 naming the provider. It is never
    translated and never sent to the other endpoint; [WG-I23](#WG-I23) closes the path spellings
    that got past this. **Why by path:** the daemon has no table of
    which wire each agent speaks, and each agent's own client has already chosen one: pi, oh-omp
    and opencode send chat-completions, codex sends Responses. The destinations are still only
    the two the provider row declares, so [§7](#7-what-the-bridge-still-refuses)'s "nothing in a
    request can name a new destination" holds.
11. <a id="WG-I21"></a>**[WG-I21](#WG-I21)** — **no via route is served for `openai-codex`**, the
    ChatGPT subscription, and it is skipped by name with its reason. Reading `openai-responses`
    endpoints ([WG-I20](#WG-I20)) would otherwise make it a route, and its credential is
    `openai-auth`'s access-token view, which is neither a declared key nor SigV4. So the route
    would forward unauthenticated requests to the subscription backend. Every agent that speaks
    it implements it natively, and no derive re-points it
    ([`pi-codex-provider-shadowing.md` OQ-2](pi-codex-provider-shadowing.md#OQ-2)). The name,
    not a flag, follows that doc's [OQ-1](pi-codex-provider-shadowing.md#OQ-1). Whether codex's
    subscription should ride the bridge at all is Part 4's kind of question, not this build's.
12. <a id="WG-I22"></a>**[WG-I22](#WG-I22)** — **codex's via row names no `env_key`.** The bridge
    holds the provider's key and drops an inbound `Authorization` ([WG-I5](#WG-I5)). codex 0.157.0
    sends no `Authorization` for a custom provider with neither `env_key` nor
    `requires_openai_auth`, and fails each request with an environment-variable error when a
    declared `env_key` is unset: `resolve_provider_auth` calls the provider's `api_key` whenever a
    client is set up, not once at startup (`model-provider/src/auth.rs` `resolve_provider_auth`;
    `model-provider-info/src/lib.rs` `api_key`). So a via row that named one would make codex need
    a key it never uses. pi, opencode and oh-omp keep a key on their via rows: the provider's key
    reference for a keyed provider, and for a keyless one pi's and opencode's `local` placeholder,
    because pi drops a keyless row ([WG-I5](#WG-I5)). codex's row carries neither. Dropping the
    real key from their via rows too would match [OQ-BR4](provider-credential-scope.md#OQ-BR4)'s
    narrowing, and is those packs' change to make. **Superseded by
    [WB-D18](../reference/wire-bridge.md#wb-d18) (2026-09-28):** codex's via row now names
    `env_key = "YOLO_SERVICE_WIRE_BRIDGE_TOKEN"`, the bridge's caller token, and pi's, opencode's
    and oh-omp's rows name the same variable in place of the provider's key ([WG-I5](#WG-I5)).
    The bridge now demands that token of every request, so the key codex sends is one it does use.
    The measurement above still holds: codex fails each request when a declared `env_key` is
    unset. It cannot fire here, because a via profile brings in the bridge, a pack service with a
    jail daemon, and the launch then always writes that variable into the `0600` per-entry
    channel file every jail process inherits
    ([how the token travels](../reference/wire-bridge.md#how-the-token-travels)). The route needs no WebSocket upgrade: codex opens Responses over WebSocket only for a provider
    with `supports_websockets`, which defaults off for a custom provider, and the derive never
    sets it (`core/src/client.rs` `responses_websocket_enabled`). It compresses a request body
    only for its built-in OpenAI provider, so copying `Content-Type` and `Accept` alone loses
    nothing.

The via residuals build (2026-09-26) closed the first build's loose ends and made six more,
WG-I10 to WG-I15. Three terms are coined here. A **via agent** is an agent whose pack's first
declared protocol is one the via route carries (`openai` or `openai-responses`); an agent that
declares no protocols is not one. Its **preferred wire** is the wire that protocol names on the
route: chat-completions for `openai`, Responses for `openai-responses`. A via agent is
**re-pointed** when its derived config carries its via URL, so that some request it sends goes
to the bridge ([WG-I15](#WG-I15)).

13. <a id="WG-I10"></a>**[WG-I10](#WG-I10)** — **a via is active only for an agent a selected
    pack installs.** A `use_profiles` key is checked against every CLI any resolvable pack
    installs, selected or not, so a user-scope entry for an agent this launch does not carry is
    legal. Its via adds no pack, prints no "active for" line and is never refused. This is the
    protocol gate's rule: an agent no selected pack installs pairs with nothing. Lives in
    `packload.ActiveVias`, which `packload.ResolveVias` walks.
14. <a id="WG-I11"></a>**[WG-I11](#WG-I11)** — **one resolver for the whole selection
    closure.** `packload.Selection.Close` runs the needs closure, then the via closure over the
    needs-closed set, then the needs closure over what via added. The launch (`stagePacks`,
    through `run.Options.launchSelection`), `yolo check`'s Packs section, config validation
    (`config.resolveSelectedPacks`), `config promote`'s fold (`cli.loadPromoteFold`) and the lazy
    loophole resolvers (`run.resolveConfiguredPacks`) all call it. Each caller supplies only its
    selection table. The launch folds `-p` in, `check` reads the merged config's `use_profiles`,
    and the other three read the user scope through `config.UserScopeSelection`. That is the whole
    selection, because a workspace `use_profiles` is refused. A refused closure returns no
    additions. Before it, `check`, validation and `promote` ran the needs closure alone, so their
    pack lists omitted a pack a via profile adds.
15. <a id="WG-I12"></a>**[WG-I12](#WG-I12)** — **via is inert at the host notch through the
    launch's own predicate.** `packload.ViaURLFor` is what both derive paths ask. `yolo host`'s
    env composition (`cli.composeHostVars`) and the footer's tables (`cli.hostFooterTables`) pass
    their resolved table through `packload.ViaInert` (since 2026-09-28 `packload.ViaServedAt` with nothing served), which clears every `ViaBase` and keeps
    `Via`. So `ViaURLFor` answers "" there whatever the pack set holds. This amends
    [WG-I8](#WG-I8), whose "the service pack is absent" is false when a user lists `wire-bridge`
    in `packs`. Before it, the host's env derive (`packload.AgentEnv`) received a `ctx.via_url`
    for an address nothing serves there. The same gap held for the bridge's ADAPTER addresses,
    which this cleared nothing of. [`credential-sources-separation.md`
    ES-D18](credential-sources-separation.md#10-decision-ledger) (2026-09-27) closes it: the
    host's table composes no address a pack's own service serves, and a profile only such an
    address would pair refuses.
16. <a id="WG-I13"></a>**[WG-I13](#WG-I13)** — **the launch refuses a re-pointed via agent the
    daemon serves no route for.** It is the ninth launch pre-flight (`run.Options.checkViaRoutes`),
    and `yolo check` predicts it as a FAIL. Both call `wirebridged.ViaRouteGate`, which reads
    `wirebridged.viaRoutesFor`, the core the daemon boots from, so the launch and the daemon
    cannot disagree (`wirebridged.WillServe`'s rule: one decision, two call sites). The refusal
    names the profile, the agent, the daemon's reason (a provider declaring neither wire, one not
    in the composed table, or [WG-I21](#WG-I21)'s subscription), the via URL and what the agent
    would meet there. With no via route served at all the daemon binds no via listener, so the
    agent's connection is refused. When another agent's route is served, the prefix gets
    `wirebridged.viaMux`'s 404. **Refuse, not disclose:** no request to that prefix can succeed
    whatever client sends it, and the launch refuses a selection it knows it cannot honor
    (`checkProfileDeclarations`' rule). No hatch, because the remedy is a config line. It applies
    only to a via agent whose via URL is non-empty and whose config the via re-points
    ([WG-I15](#WG-I15)). So a via over `openai-codex` refuses opencode, whose derive re-points the
    selected provider whatever it is, and not pi, oh-omp or codex, which keep the subscription on
    their own client. claude and copilot prefer `anthropic`, agy declares no protocols, none of
    their derives read the via URL, and a via on their profile is neither refused nor disclosed.
    Before it, a provider offering neither wire still gave the agent `ctx.via_url`, and the agent
    met the failure at its first request, recorded only in the daemon's log.
17. <a id="WG-I14"></a>**[WG-I14](#WG-I14)** — **a route without a re-pointed agent's preferred
    wire is disclosed, not refused.** The route serves, but only the other wire, so the agent's
    requests get [WG-I20](#WG-I20)'s 404. The launch prints a warning on stderr and `yolo check` a
    WARN, each naming the profile, the agent, the provider and the endpoint it lacks. The
    preferred wire is read from the agent pack's declared `protocols`, because the via route has
    no declaration of its own. **Why disclose:** the launcher's evidence is an inference. For
    every shipped via agent the preference and the wire its via row speaks agree, so there the
    requests are certain to fail. But `protocols` is a preference list for protocol pairing, not
    a statement of what a derive writes on its via row. That wire is spelled in each agent's own
    config vocabulary (pi's `api`, codex's `wire_api`), which core does not read, so a pack yolo
    does not ship can write either. Refusing on the inference would refuse such a pack's working
    launch. `TestShippedViaRowsSpeakTheWireTheAgentPrefers` runs each wired agent's real derive
    with `ctx.via_url` set and checks that the row it re-points speaks the preferred wire: pi,
    oh-omp and opencode chat-completions, codex Responses. claude and copilot are not via agents.
    A declared via wire per agent would let the launch refuse it.
18. <a id="WG-I15"></a>**[WG-I15](#WG-I15)** — **the via checks ask the agent's own derives whether
    the via re-points it, and a via that re-points nothing is disclosed, never refused.**
    `packload.DerivedViaPointers` runs every rendered surface's derive the selected packs declare
    for the agent, and the agent's env producer, over the launch's own provider and profile
    tables with `ctx.via_url` set as the boot sets it. It returns each output string that is the
    via URL or lies under it. None means the agent's config is what it would be without via.
    **Why the derives:** a profile's `via` is an input to a derive, not an instruction to it, and
    each derive decides which rows ride it. pi and oh-omp keep `openai-codex` on their own client,
    codex writes a row only for a provider it can reach, and opencode re-points whatever provider
    is selected. No declaration states those rules, so a gate reading only manifests refused
    launches that worked: pi, oh-omp and codex on a via over `openai-codex`, and warned codex of
    404s on a chat-only provider it never sends to. **Why disclose:** the launch the user gets is
    the launch without via, which works, so a refusal would stop a working jail. The notice says
    the via has no effect and, where the daemon would skip the route too, why. A derive the check
    cannot run is disclosed as unchecked and not refused, because the boot runs the same derive
    and refuses the launch over its error. The derives run in the same Lua sandbox the boot uses,
    and the launcher already runs each agent's own pack script for the environment
    (`packload.AgentEnv`). Lives in `packload.DerivedViaPointers` and
    `wirebridged.ViaRouteGate`.

The review of the Responses build (2026-09-26) made two more, again numbered past the
concurrent track's:

19. <a id="WG-I23"></a>**[WG-I23](#WG-I23)** — **a via route forwards only a canonical path, and
    forwards it escaped.** A path after the prefix with a `.`, `..` or empty segment (one trailing
    `/` aside), or with a `?` or `#` in its decoded form, gets an OpenAI-shaped 404 and sends
    nothing upstream. The upstream URL is the base URL joined with the path's escaped form, not the
    decoded path concatenated. **Why:** the split classifies the path as written and an upstream
    normalizes it before routing, so `/x/../chat/completions` was refused as neither wire and
    served as chat-completions, which reached a Responses-only provider, reached the other of two
    endpoints, or climbed out of the provider's declared base path. The concatenation turned an
    encoded `?` into a query separator. The host never changed, so [§7](#7-what-the-bridge-still-refuses)'s
    rule held throughout. Bedrock failed closed already: the signer signs the path with its dot
    segments and AWS normalizes it first, so the signature mismatched. Refusing, not cleaning,
    keeps what the route classifies and what the provider receives the same without rewriting what
    the agent sent. None of the four wired clients builds such a path, since each joins its base
    URL with a fixed one. Lives in `wirebridged.viaMux`,
    `wirebridged.canonicalViaTail` and `passthroughHandler.do`.
20. <a id="WG-I24"></a>**[WG-I24](#WG-I24)** — **a stream the upstream cuts short aborts the
    agent's connection, and only the wait for response headers is bounded.** When the upstream's
    body fails after the status line went out, the daemon logs the cut, the byte count and the
    cause, and aborts the connection (`http.ErrAbortHandler`, as `httputil.ReverseProxy` does), so
    the agent's read fails. The agent closing its own request is no fault and is neither aborted
    nor logged. The wait for the upstream's headers is bounded by `upstreamTimeout`'s ten minutes
    (`wirebridged.viaHeaderTimeout`), and the pass-through's client has no whole-exchange timeout.
    **Why:** returning let net/http end the response cleanly. codex notices a Responses stream
    without `response.completed`, but a chat-completions client (pi, oh-omp and opencode, through
    the OpenAI SDK) reads an end of stream without `[DONE]` as a finished reply, so it accepted a
    truncated one, and the log said nothing. A `Client.Timeout` bounds reading the body too, so it
    cut every stream longer than ten minutes the same silent way. Lives in
    `passthroughHandler.ServeHTTP` and `passthroughHandler.do`. The adapter routes keep their
    whole-exchange timeout; they translate, and are not part of this build.

**What is not built, or not closed:**

- ~~Part 5's allowlist~~: built 2026-09-30 ([§6.1](#61-how-the-allowlist-is-built)).
- The Converse pass-through route, pi's: decided as [WG-I36](#WG-I36), and it waits on
  [`pi-codex-provider-shadowing.md` OQ-3](pi-codex-provider-shadowing.md#OQ-3) for where pi's row lives.
- ~~A Bedrock provider named by region alone gets no via route.~~ It does since 2026-09-30: its one
  upstream is runtime's `/openai/v1` in the region ([WG-I39](#WG-I39)), so the shipped
  `bedrock-bridge` profile ([`bedrock-plumbing.md` BR-D16](bedrock-plumbing.md#BR-D16)) carries
  codex, pi, opencode and oh-omp on their via routes, and claude on the adapter route. Before that,
  the four were refused ([WG-I13](#WG-I13)) and claude was warned ([WG-I26](#WG-I26)).
- A via agent whose derive speaks a wire its provider does not declare is served, and each request
  is refused with [WG-I20](#WG-I20)'s 404. pi, oh-omp and opencode speak chat-completions on the
  via route, so selecting a Responses-only provider (such as `openrouter`) for one of them lands
  here. Until [WG-I20](#WG-I20) that route was skipped at boot. The launch warns about it and
  `yolo check` reports a WARN ([WG-I14](#WG-I14)), but neither refuses: the daemon learns an
  agent's wire only from its requests, and the launcher reads it off the agent's `protocols`. A
  refusal would need the wire each derive's via row speaks declared somewhere both can read.

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

**A rejected alternative, revisited.** The providers reference
[rejects refusing a launch](../reference/providers.md#no-launch-time-model-id-refusal) when the
config holds an id outside the selected provider's `models`: it would catch residue loudly but refuse most legitimate hand-picked models.
A launch-time check also cannot see a mid-session `/model` switch or a `--model` flag, and the
bridge sees every request. So the rejection stands as the *enforcement point*, while its intent
moves here.

**How it relates to [OQ-BR1](bedrock-plumbing.md#OQ-BR1).** That question is restated around an
agent's own native Bedrock client versus the wire bridge. A profile that forces the bridge for an
agent with a native client is the Bedrock instance of this switch ([OQ-WG2](#OQ-WG2)).

### 6.1 How the allowlist is built

**BUILT 2026-09-30**, the part [OQ-WG3](#OQ-WG3) ruled, once
[OQ-BR12](model-lists-and-pickers.md#OQ-BR12)'s `only` existed. The list is the provider's
composed list when an `only` narrowed it (the entry's `models_only` mark,
[MM-D11](model-lists-and-pickers.md#MM-D11)); the switch is the profile's `enforce_models`
([MM-D13](model-lists-and-pickers.md#MM-D13)). A list no `only` narrowed refuses nothing, which is
[OQ-MM3](model-lists-and-pickers.md#OQ-MM3)'s open question for a gateway, not this build's.
[`model-lists-and-pickers.md` §14.3](model-lists-and-pickers.md#143-the-bridges-part-and-get-v1models)
sets the one precondition: before the default-on refusal applies to an agent, its background
traffic has to be on the list. Four implementation decisions:

1. <a id="WG-I40"></a>**[WG-I40](#WG-I40)**: **the allowlist is per route, and the adapter route
   refuses only for what every agent sharing it would be refused.** A via route is one agent's, so
   its own profile's switch decides. The adapter route carries every agent that reaches the
   provider at its anthropic endpoint (claude and copilot), and the one caller token cannot tell
   their requests apart ([WB-D18](../reference/wire-bridge.md#wb-d18)), so it refuses only when
   every such agent's profile has the switch on and none is exempt. An agent is such a sharer when
   its active profile selects the provider and its first declared protocol is one the via route does
   not carry; an address composed for a via ([WG-I39](#WG-I39)) counts only under the via. **Why:**
   an off switch one sharer set must stay reachable, the rule *"no refusal ships before its off
   switch does"* ([MM-D5](model-lists-and-pickers.md#MM-D5)), and a refusal applied to copilot's
   requests through claude's route would break what [§14.3](model-lists-and-pickers.md#143-the-bridges-part-and-get-v1models) says must not break. On the adapter route
   both the request's model and the list are read in the spelling the translation sends upstream
   (`wirebridge.NormalizeModel`): claude spells a bare Kilo `deepseek-` id with its `deepseek/`
   prefix, and compared as written its own pinned tiers were refused on a list naming the bare
   id. Lives in `wirebridged.allowlistsFor` and `adapterAllowlist`.
2. <a id="WG-I41"></a>**[WG-I41](#WG-I41)**: **an agent whose background traffic is not on the
   list declares it, and is admitted every model.** A program contribution may declare
   `"unlisted_background_models": true` (`packdecl.Contribution.UnlistedBackgroundModels`, refused
   on any other kind). packs/codex declares it: its review and memories models bypass the picker,
   and pointing them at listed ids is [MM-D9](model-lists-and-pickers.md#MM-D9)'s unbuilt step.
   packs/copilot declares it: which ids its background requests carry is unmeasured ([§14.3](model-lists-and-pickers.md#143-the-bridges-part-and-get-v1models)'s
   *"For copilot, the ids are measured first"*). The bridge admits every model such an agent sends
   and logs an off-list one, which is the evidence the measurement needs. claude declares nothing,
   since yolo pins every tier to the list ([MM-D2](model-lists-and-pickers.md#MM-D2)), and neither
   do pi, oh-omp and opencode, for which the design names no background model; that is INFERRED,
   and a refused background request of theirs would name the list and the switch. **Why a pack
   declaration and not a list in the bridge:** core names no agent, and what a binary sends is that
   binary's pack's fact, as `platform_switches` is. **Why opt-out:** the switch defaults on
   ([OQ-WG3](#OQ-WG3)), and the design lists exactly two agents whose precondition is unmet.
3. <a id="WG-I42"></a>**[WG-I42](#WG-I42)**: **the daemon reads that declaration from the jail's
   staged pack tree, and refuses nothing when it cannot.** The tables the bridge boots from
   (`YOLO_PROVIDERS`, `YOLO_PROFILES`, `YOLO_USE_PROFILES`) carry no pack facts, and the staged tree
   at `YOLO_PACK_ROOT` is the one copy of the selected packs' declarations in the jail, the one the
   boot rendered every surface from (`entrypoint.LoadJailPacks`). It is read only when some route's
   list is narrowed. A tree that cannot be read, or none (the host half), refuses no model and says
   so in the log: refusing blind would refuse the exempt agents. Relaying the fact in a new launch
   variable was the other choice, and would have been a second channel for a fact the tree already
   holds. The launcher's serve-or-idle answer does not depend on it, so the two halves cannot
   disagree.
4. <a id="WG-I43"></a>**[WG-I43](#WG-I43)**: **the refusal is a 400 `invalid_request_error` in the
   route's protocol, and a body naming `model` twice is refused.** Anthropic's shape on the adapter
   route and OpenAI's on a via route, naming the model, the provider, the list and the profile whose
   `enforce_models` puts it in force, before either upstream is dialed. A 400 because both clients
   show it as the request's own error and retry nothing; a 401 or 403 could read as a credential
   fault, and a 404 as a missing route. A body with two top-level `model` keys is refused, because
   the bridge's parser keeps one and a provider's may keep the other. A key is `model` in any
   letter case, since the bridge's own parser, Go's `encoding/json`, fills its model from `Model`
   or `MODEL` too and keeps the last: read case-sensitively, a listed `model` beside an off-list
   `Model` passed the check and the off-list one was translated upstream. A request naming no model
   (`GET /models`) is not the list's. Lives in `modelAllowlist.checks` and `requestModel`, called
   by `bridgeHandler.ServeHTTP` and `passthroughHandler.ServeHTTP`.

MEASURED in-process: the production boot over the shipped packs and a company pack's `only`, each
table crossed as the per-entry channel crosses it, a staged pack tree, and a stubbed upstream. No
agent has sent a request through it.

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
- **No host-side bridge**, as
  [`wire-bridge.md`](../reference/wire-bridge.md#what-this-does-not-license) states. **Amended
  2026-09-28 by [WB-D18](../reference/wire-bridge.md#wb-d18):** this bullet also refused inbound
  authentication, and a per-agent token ([OQ-WG4](#OQ-WG4)) was to be a routing key, not
  authentication. The bridge now authenticates every caller: each request must carry the launch's
  caller token, a random per-launch secret
  ([caller authentication](../reference/wire-bridge.md#caller-authentication)), or it is refused
  `401`. The token is one per launch, not one per agent, so it tells a caller that belongs to this
  launch from one that does not, and never one agent from another; the per-agent path prefix is
  still the routing key.

---

## 8. Build order

1. **The signer** ([OQ-BR10](#OQ-BR10); [§2.1](#21-behavior-the-signer-fixes)), with its test
   vectors and lazy resolution. **BUILT 2026-09-25**, and the region-composed URL and the marker
   re-key **BUILT 2026-09-30** ([§2.2](#22-how-the-bedrock-upstream-is-chosen-built-2026-09-30),
   [WG-I37](#WG-I37)–[WG-I39](#WG-I39)). It has no dependency on the model-list work, and on its
   own it closes the SSO gap for copilot and the everything profile. Keyed on the provider's
   `platform` ([OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2)), and on the upstream host for a
   provider that declares none ([OQ-WG1](#OQ-WG1)). (Build step 8.1 in the source.)
2. **Routing by model id**, measuring runtime's Messages route first
   ([§3](#3-part-2--routing-by-model-id-for-claudes-everything-profile-ruled)). (Build step 8.3.)
   <a id="WG-I25"></a>**[WG-I25](#WG-I25): released for build, 2026-09-29.** An implementation
   decision, numbered in the series [§4.1](#41-how-it-is-built)'s via build began; no ruling is
   needed. The behavior is [OQ-BR11](bedrock-plumbing.md#OQ-BR11)'s ruling (2026-09-24), and every ruling
   [§3](#3-part-2--routing-by-model-id-for-claudes-everything-profile-ruled)'s done-condition
   waited on is in. Build it with, or right after, the `bedrock-bridge` profile: the one shipped
   profile that forces the bridge, which for claude is the everything profile
   ([OQ-BR1](bedrock-plumbing.md#OQ-BR1), ruled 2026-09-29). Until this step lands, a Claude
   model picked under `-p bedrock-bridge` goes through translation to chat-completions, which
   strips `cache_control` (so no prompt is cached) and omits `thinking`
   ([WB-D15](../reference/wire-bridge.md#wb-d15)), and `count_tokens` stays refused
   ([WB-D14](../reference/wire-bridge.md#wb-d14)). The step starts with [§3](#3-part-2--routing-by-model-id-for-claudes-everything-profile-ruled)'s one measurement,
   SSE or binary event-stream.
   **BUILT 2026-09-29** ([§3.1](#31-how-it-is-built), [WG-I30](#WG-I30)–[WG-I35](#WG-I35)). The
   measurement, SOURCED: `/anthropic/v1/messages`, streaming Anthropic SSE. The route is live for
   a Bedrock provider the bridge reaches at runtime's `/openai/v1`, and since step 1's
   region-composed URL (2026-09-30) that includes the shipped `bedrock` under `-p bedrock-bridge`.
   `count_tokens` stays refused ([WG-I35](#WG-I35)).
3. **The sign-only route**
   ([§4](#4-part-3--the-sign-only-openai-chat-completions-route-ruled)), for oh-omp and pi.
   **BUILT 2026-09-25** as the via route, with Part 5's selection ([§4.1](#41-how-it-is-built)).
4. **The subscription arm and failover**
   ([§5](#5-part-4--the-subscription-arm-and-opt-in-failover-ruled)), after measuring the
   usage-limit response.
5. **All-traffic mode** ([§6](#6-part-5--all-traffic-through-the-bridge-new-direction-design)),
   once [OQ-BR12](model-lists-and-pickers.md#OQ-BR12) gives the list an `only`. Pass-through
   routes land in this order: OpenAI chat-completions (oh-omp, pi), then Responses (codex), then
   Bedrock Converse. Before an agent's derive relies on the path prefix, measure that the agent
   keeps a base URL's path; an agent that drops it gets its own port instead.
   The selection, the chat-completions route and the Responses route are BUILT
   ([§4.1](#41-how-it-is-built)), and so is the allowlist (2026-09-30,
   [§6.1](#61-how-the-allowlist-is-built)); Converse, decided as
   [WG-I36](#WG-I36), waits on [`pi-codex-provider-shadowing.md` OQ-3](pi-codex-provider-shadowing.md#OQ-3).

---

## 9. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| **Mint a Bedrock API key for the bridge** ([option D](sso-backed-bedrock.md#4-five-options)) | **Superseded by [OQ-BR10](#OQ-BR10).** At most an hour over the narrowed session, frozen for the launch, and refused where `CallWithBearerToken` is denied. |
| **A host-side signing proxy** ([option E](sso-backed-bedrock.md#4-five-options)) | **Not needed.** An in-jail signer gets the refresh and the policy resilience without the host seeing any prompt. |
| **Vendor the AWS SDK's signer** ([OQ-BR10](#OQ-BR10)'s option C) | **Not taken; the implementer's call**, to keep the hermetic build free of an AWS module. The test vectors pin the standard-library signer instead. |
| **Two claude profiles, no routing by model** ([OQ-BR11](bedrock-plumbing.md#OQ-BR11)'s leaning) | **Overruled**: one session could never switch between Claude and an OpenAI model. |
| **Refuse a launch on an id outside the provider's `models`** ([providers reference](../reference/providers.md#no-launch-time-model-id-refusal)) | **Still rejected as the enforcement point**; its intent moves to the bridge ([OQ-WG3](#OQ-WG3)). |
| **Sign on the provider's Bedrock marker** | **Taken, 2026-09-30 ([WG-I37](#WG-I37))**: [OQ-WG1](#OQ-WG1)'s follow-up once [OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2) gave providers a marker, because host patterns cannot know every configuration. The host rule stays for a provider that declares no Bedrock platform. |

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
   `enabledModels`). The launch-time "refuse unknown id"
   ([providers reference](../reference/providers.md#no-launch-time-model-id-refusal)) stays rejected as the enforcement point.


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

6. ✅ <a id="OQ-WG6"></a>**[OQ-WG6](#OQ-WG6): what does a profile write to put its agent on
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


   **Answer:**
   > **(b)**, ruled in review 2026-09-25: a profile field (e.g. `via: "bridge"`). The derive writes
   > the agent's base URL as the bridge's per-agent path ([OQ-WG4](#OQ-WG4)), and `routeFor` takes
   > the upstream from the provider's own `openai` endpoint. It is [OQ-WG2](#OQ-WG2) and
   > [OQ-WG4](#OQ-WG4) made concrete and needs no new provider vocabulary. **Built 2026-09-25**
   > ([§4.1](#41-how-it-is-built)); the example value became the service pack's name,
   > `via: "wire-bridge"`, by [OQ-WG7](#OQ-WG7) (c).

7. ✅ <a id="OQ-WG7"></a>**[OQ-WG7](#OQ-WG7): the multi-route bridge, in five parts, rule together.**
   Found 2026-09-25 when the [OQ-WG6](#OQ-WG6) build stopped before writing code. WG2, WG4 and
   WG6 say what a profile writes. They do not say how the daemon, the ports, the jail's pack set,
   the derives and the credentials carry more than one route. Checked against the tree: the
   daemon serves exactly one route (`routeFor` returns the first match, and `serve` builds one
   handler); `packs/wire-bridge` declares two adapter addresses (`:8214` for chat-completions to
   Anthropic, `:8215` for Responses to Anthropic); only `packs/claude` `needs` wire-bridge
   unconditionally; the composed provider table is jail-wide; and the outbound key is one file
   read at boot ([WB-D4](../reference/wire-bridge.md#wb-d4)).

   | Part | The question | Leaning |
   | :--- | :--- | :--- |
   | **a. Route layout** | Do today's adapter routes move under `/agent/<name>/`? | **No.** They stay at the root of their own ports, so claude's `ANTHROPIC_BASE_URL` and its derive are unchanged. [OQ-WG4](#OQ-WG4)'s "a missing prefix is refused" applies to the per-agent port only |
   | **b. The port** | Where do per-agent routes live? | **One new declared address** on the wire-bridge service (the next free port after `:8215`), serving every `via` route under `/agent/<name>/`. The two adapter ports are untouched |
   | **c. Bringing the bridge in** | How does a pi-only jail with a `via` profile get a bridge daemon, when core may not name one? | **`via`'s value names the service pack** (`via: "wire-bridge"`), and selecting such a profile adds that pack the way `needs` does. Core knows only "a service pack", never which. [OQ-WG6](#OQ-WG6)'s `"bridge"` was an example value |
   | **d. Each agent's URL** | The provider table is jail-wide; `via` is one agent's. How does a derive learn its own bridge URL? | **A per-agent derive input** (`ctx.via_url`), set only when that agent's active profile has `via`. Composition stays jail-wide; a derive writes `ctx.via_url` as its base URL when present |
   | **e. Credentials per route** | One key file cannot serve routes to several providers | **Per route, from the provider's declared `api_key_env_name`**, read at boot from the jail env the credential is already delivered to; a Bedrock route uses the signer's chain. One route with no credential idles and says so, and the others still serve |

   Stakes: without (a)–(e), a via route is either unreachable or breaks claude's existing one.

   _Leaning:_ all five as the table says. Each keeps today's launches byte-identical, and each
   extends an existing vocabulary (a declared address, `needs`, a derive ctx field, a provider's
   env name) rather than adding a new one.


   **Answer:**
   > **All five as tabled**, 2026-09-25. The maintainer's review: *"there's only one answer here …
   > we need to make it work, and this is an implementation decision."* It should not have been
   > filed as a question; it is recorded here as decided. **Built 2026-09-25**
   > ([§4.1](#41-how-it-is-built)), with the mechanism choices recorded as
   > [WG-I1](#WG-I1)–[WG-I9](#WG-I9).

8. ✅ <a id="OQ-WG8"></a>**[OQ-WG8](#OQ-WG8): on pi's Converse pass-through, does pi's own client
   use the AWS credential?** Found 2026-09-26, when Converse came next in [§8](#8-build-order)'s
   order, and reframed the same day in review. [OQ-WG5](#OQ-WG5) ruled that every native wire gets
   a pass-through route, Converse included. Converse is Bedrock's own API, and pi's Converse client
   is the AWS SDK. Read in the installed pi 0.87.1 (`pi-ai`'s `dist/api/bedrock-converse-stream.js`;
   nothing was run):
   - it takes a model's `baseUrl` as its endpoint whenever that is not a standard
     `bedrock-runtime` host, and keeps that URL's path, so the per-agent prefix works;
   - it has **three authorization modes**. With a bearer token resolved and
     `AWS_BEDROCK_SKIP_AUTH` unset, it sets the SDK's token and prefers `httpBearerAuth`, so it
     sends `Authorization: Bearer <token>` and **no SigV4 signature**. Under
     `AWS_BEDROCK_SKIP_AUTH=1` it signs with the literal keys `dummy-access-key` and
     `dummy-secret-key`. Otherwise it signs with the SDK credential chain;
   - the bearer token is taken from the request's options (`bearerToken`, then `apiKey`) before
     `AWS_BEARER_TOKEN_BEDROCK`, and a `models.json` `apiKey` on the `amazon-bedrock` provider becomes that `apiKey` with no
     environment variable: `provider-composer.js` `composeApiKeyAuth` hands a configured key to
     the built-in resolver, and `providers/amazon-bedrock.js` `resolve` returns it as the auth's
     `apiKey`.

   So a via override on pi's `amazon-bedrock` provider that carries a **placeholder `apiKey`**
   makes pi send a placeholder bearer, which the bridge drops ([WG-I5](#WG-I5)) before signing
   with its own chain. That is the shape pi's derive already gives a keyless loopback row
   (`apiKey = "local"`). The response is AWS's binary event stream, which the pass-through copies
   byte for byte, so nothing needs re-framing.

   Two properties are easy to conflate here, and only the first is this question:
   - **pi's client does not use the AWS credential.** The placeholder bearer gives it now, with
     no new delivery mechanism.
   - **pi's environment does not carry the AWS credential.** No mechanism here gives it. The
     jail-wide AWS variables reach every agent until
     [OQ-CN6](provider-credential-scope.md#OQ-CN6)'s per-agent env file (ruled 2026-09-26,
     unbuilt) narrows them, and `AWS_BEDROCK_SKIP_AUTH=1` does not remove them either. It follows
     [OQ-CN6](provider-credential-scope.md#OQ-CN6) whatever this question rules.

   The options:
   - **(a) pi keeps signing with its own credential.** The bridge discards pi's signature and
     signs with its own chain. Nothing new reaches pi's configuration, and two signers run per
     request.
   - **(b) pi sends a placeholder bearer, and only the bridge signs.** The via override carries
     `apiKey = "local"`, so pi's client uses no AWS credential. Environment narrowing follows
     [OQ-CN6](provider-credential-scope.md#OQ-CN6) separately. The dummy-key switch is the other way
     to stop pi's client signing with the real credential, but it is an environment variable, and
     one agent receives a variable alone only through
     [OQ-CN6](provider-credential-scope.md#OQ-CN6), so it is not the mechanism.
   - **(c) No Converse pass-through.** pi's bridge path stays the chat-completions route, as
     [`bedrock-plumbing.md`](bedrock-plumbing.md) records as [OQ-BR5](bedrock-plumbing.md#OQ-BR5)'s design consequence ("the
     bridge version speaks OpenAI chat-completions"), and WG5's "Converse included" is withdrawn.

   Stakes: under (a) pi's client goes on using the credential on the bridge path, so that path
   delivers it no more narrowly than native; (b) keeps it out of pi's requests from the first
   build and waits on no other ruling; (c) reverses part of a ruling. Three facts hold
   under (a) and (b) and are implementation, not part of the question. The upstream is
   `bedrock-runtime.<region>.amazonaws.com` composed from the provider's `region`
   ([§2.1](#21-behavior-the-signer-fixes)'s last bullet, built 2026-09-30 as [WG-I39](#WG-I39)), because the shipped `bedrock`
   provider declares no endpoint. A Converse path names the model, and an inference-profile ARN
   carries an encoded `/`, which the via mux decodes today, so the Converse route must forward the
   path as the agent encoded it. And the re-pointing lives in pi's derive, as a `baseUrl` on pi's
   built-in `amazon-bedrock` provider. Whether that override counts as a catalog entry under
   [`pi-codex-provider-shadowing.md` OQ-2](pi-codex-provider-shadowing.md#OQ-2)'s rule is for that
   doc to say, and the override keeps pi's own Converse client.

   _Leaning:_ **(b)**. [OQ-BR4](provider-credential-scope.md#OQ-BR4) ruled that nothing leaks and
   delivery is as specific as possible. A pass-through whose client still signs with the key
   delivers it no more narrowly than native, and (b) costs one placeholder in a derive that
   already writes one. The daemon half and the placeholder can land together with the
   region-composed upstream, and [OQ-CN6](provider-credential-scope.md#OQ-CN6)'s per-agent env
   file later takes the variables out of pi's environment. The placeholder path is read from
   pi's source, not measured: no pi session has sent a Converse request through it.

   **Answer:**
   > Decided as an implementation choice ([WG-I36](#WG-I36)), reversible: (b), pi's via override
   > carries a placeholder `apiKey`, so pi's client sends a placeholder bearer the bridge drops and
   > only the bridge signs. (c) would reverse [OQ-WG5](#OQ-WG5)'s "Converse included", and (a)
   > leaves pi's client signing with the credential where only the bridge needs it, against
   > [OQ-BR4](provider-credential-scope.md#OQ-BR4)'s *"as specific as possible"*. Where the row
   > lives is [`pi-codex-provider-shadowing.md` OQ-3](pi-codex-provider-shadowing.md#OQ-3)'s: its
   > broad reading would move it off pi's built-in `amazon-bedrock` key, and the placeholder works
   > the same under either key. Recorded 2026-09-30.

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
| OQ-WG1 | **The signer keys on the upstream host now** (`bedrock-runtime.<region>.amazonaws.com`, `*.gateway.bedrock-agentcore.<region>.amazonaws.com`), **and re-keys on the provider's Bedrock marker once [OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2) gives one** — host patterns cannot know other people's configurations | 2026-09-25 | [§2.1](#21-behavior-the-signer-fixes) | 2026-09-25, (a): `sigv4.BedrockRuntimeRegion` decides at boot (`route.SignRegion`). (b), the marker re-key, 2026-09-30 as [WG-I37](#WG-I37): `wirebridged.bedrockSigning` |
| OQ-WG6 | **A profile field (`via: "bridge"`) puts its agent on the bridge path**: the derive writes the agent's base URL as the bridge's per-agent path (WG4), and `routeFor` takes the upstream from the provider's own `openai` endpoint; no new provider vocabulary | 2026-09-25 | [OQ-WG6](#OQ-WG6) | 2026-09-25, [§4.1](#41-how-it-is-built) |
| OQ-WG7 | **The multi-route bridge, as tabled**: existing adapter routes stay at their ports' roots; one new declared address carries every `via` route under `/agent/<name>/`; `via`'s value names the service pack, which selecting the profile adds like `needs`; a per-agent `ctx.via_url` derive input; credentials per route from the provider's `api_key_env_name`, Bedrock through the signer chain. An implementation decision, recorded as one | 2026-09-25 | [OQ-WG7](#OQ-WG7) | 2026-09-25, [§4.1](#41-how-it-is-built) |
| WG-I1 | **The via pair crosses in `YOLO_PROFILES` under reserved keys `_via` / `_via_base`.** An implementation decision ([OQ-WG7](#OQ-WG7)) | 2026-09-25 | [WG-I1](#WG-I1) | 2026-09-25 |
| WG-I2 | **The via address is a `via_address` field on the `service` contribution, `http://127.0.0.1:8216`.** An implementation decision ([OQ-WG7](#OQ-WG7)) | 2026-09-25 | [WG-I2](#WG-I2) | 2026-09-25 |
| WG-I3 | **One daemon serves every listener; its endpoint file names the first one bound.** An implementation decision ([OQ-WG7](#OQ-WG7)) | 2026-09-25 | [WG-I3](#WG-I3) | 2026-09-25 |
| WG-I4 | **The upstream is the provider's `openai` base plus the agent's path remainder; nothing hard-coded.** An implementation decision ([OQ-WG7](#OQ-WG7)) | 2026-09-25 | [WG-I4](#WG-I4) | 2026-09-25 |
| WG-I5 | **The agent's own `Authorization` is never forwarded, on any via route.** An implementation decision ([OQ-WG7](#OQ-WG7)) | 2026-09-25 | [WG-I5](#WG-I5) | 2026-09-25 |
| WG-I6 | **A via route is served only for a chat-completions `openai` endpoint; others are skipped and logged.** An implementation decision ([OQ-WG7](#OQ-WG7)) | 2026-09-25 | [WG-I6](#WG-I6) | 2026-09-25 |
| WG-I7 | **A via names only an embedded official pack whose service declares a `via_address`, or the launch refuses.** An implementation decision ([OQ-WG7](#OQ-WG7)) | 2026-09-25 | [WG-I7](#WG-I7) | 2026-09-25 |
| WG-I8 | **Via is inert at the host notch: no closure runs there, so no `ViaBase`, and no file derive reads `ctx.via_url` there.** Amended by WG-I12. An implementation decision ([OQ-WG7](#OQ-WG7)) | 2026-09-25 | [WG-I8](#WG-I8) | 2026-09-25 |
| WG-I9 | **`stagePacks` reads the config through the run options' `stagingCfg` field.** An implementation decision ([OQ-WG7](#OQ-WG7)) | 2026-09-25 | [WG-I9](#WG-I9) | 2026-09-25 |
| WG-I20 | **A via route carries the provider's chat-completions and Responses endpoints, and each request's path picks one; a path naming an undeclared wire is refused, never translated.** Amends WG-I6. An implementation decision ([OQ-WG5](#OQ-WG5)'s Responses route) | 2026-09-26 | [WG-I20](#WG-I20) | 2026-09-26: `wirebridged.viaUpstreams`, `wirebridged.viaWireSplit` |
| WG-I21 | **No via route is served for `openai-codex`; it is skipped by name.** An implementation decision | 2026-09-26 | [WG-I21](#WG-I21) | 2026-09-26: `wirebridged.viaRoutesFor` |
| WG-I22 | **codex's via row names no `env_key`, and needs no WebSocket.** An implementation decision | 2026-09-26 | [WG-I22](#WG-I22) | 2026-09-26: `packs/codex/derive.lua` |
| WG-I10 | **A via is active only for an agent a selected pack installs**: an entry for an agent the launch does not carry adds no pack and is never refused. An implementation decision | 2026-09-26 | [WG-I10](#WG-I10) | 2026-09-26: `packload.ActiveVias` |
| WG-I11 | **One resolver for the selection closure (needs, via, needs), called by the launch, `yolo check`, config validation, `config promote` and the lazy loophole resolvers.** An implementation decision | 2026-09-26 | [WG-I11](#WG-I11) | 2026-09-26: `packload.Selection.Close`, `config.UserScopeSelection` |
| WG-I12 | **Via is inert at the host notch through `ViaURLFor`: the host's resolved table has every `ViaBase` cleared.** Amends WG-I8. An implementation decision | 2026-09-26 | [WG-I12](#WG-I12) | 2026-09-26: `packload.ViaInert`, called by `cli.composeHostVars` and `cli.hostFooterTables` |
| WG-I13 | **The launch refuses a re-pointed via agent the daemon serves no route for, through the daemon's own core; `yolo check` predicts it as a FAIL.** An implementation decision | 2026-09-26 | [WG-I13](#WG-I13) | 2026-09-26: `wirebridged.ViaRouteGate`, `run.Options.checkViaRoutes` |
| WG-I14 | **A route without a re-pointed agent's preferred wire (its first declared protocol) is disclosed as a warning, not refused.** An implementation decision | 2026-09-26 | [WG-I14](#WG-I14) | 2026-09-26: `wirebridged.ViaRouteGate`, `wirebridged.preferredViaWire` |
| WG-I15 | **The via checks run the agent's own derives to see whether the via re-points it; a via that re-points nothing is disclosed as having no effect, never refused.** An implementation decision | 2026-09-26 | [WG-I15](#WG-I15) | 2026-09-26: `packload.DerivedViaPointers`, `wirebridged.ViaRouteGate` |
| WG-I23 | **A via route forwards only a canonical path (no `.`, `..` or empty segment, no decoded `?` or `#`), and forwards it escaped; any other gets a 404.** An implementation decision, closing a bypass of WG-I20 | 2026-09-26 | [WG-I23](#WG-I23) | 2026-09-26: `wirebridged.canonicalViaTail`, `passthroughHandler.do` |
| WG-I24 | **A stream the upstream cuts short aborts the agent's connection and is logged; only the wait for response headers is bounded.** An implementation decision | 2026-09-26 | [WG-I24](#WG-I24) | 2026-09-26: `passthroughHandler.ServeHTTP`, `wirebridged.viaHeaderTimeout` |
| WG-I25 | **Part 2, routing by model id, is released for build: pass Anthropic ids untranslated to runtime's Messages route, built with or right after the `bedrock-bridge` profile.** Until then a Claude model on that profile is translated, losing `cache_control` and `thinking`. Whether runtime streams Anthropic SSE or AWS's binary event-stream is the builder's first measurement, not a choice. An implementation decision: the behavior is [OQ-BR11](bedrock-plumbing.md#OQ-BR11)'s, and [OQ-BR9](bedrock-plumbing.md#OQ-BR9), [OQ-BR12](model-lists-and-pickers.md#OQ-BR12) and [OQ-BR13](model-lists-and-pickers.md#OQ-BR13) have ruled or been directed | 2026-09-29 | [§8](#8-build-order) step 2 | 2026-09-29, [§3.1](#31-how-it-is-built): the adapter route on a `bedrock-runtime` upstream sends a model its list declares vendor `anthropic` for to `/anthropic/v1/messages` untranslated (`wirebridged.anthropicModelIDs`, `messagesPassthrough`); pinned through the production boot by `TestAnAnthropicModelGoesUntranslatedToBedrocksMessagesRoute` and `TestEveryOtherModelOnTheRouteIsStillTranslated`. Reached by `bedrock-bridge` since [§8](#8-build-order) step 1's region-composed URL, 2026-09-30 ([WG-I39](#WG-I39)) |
| <a id="WG-I26"></a>WG-I26 | **codex's via row covers a Bedrock provider that names no endpoint**: under a profile with a `via`, the selected provider of platform `aws-bedrock` gets codex's via row (Responses at its via URL, the first OpenAI entry of the list), as pi's, opencode's and oh-omp's derives already give every selected provider, rather than no row. The bridge, not codex, reaches Bedrock on that route, and a row the route cannot serve is the launch's to refuse ([WG-I13](#WG-I13)) rather than the derive's to hide: with no row, WG-I15 disclosed "the via has no effect", which was false, since without the via codex runs its own Bedrock client ([`bedrock-plumbing.md` BR-D16](bedrock-plumbing.md#BR-D16)). Until the region-composed URL landed ([§8](#8-build-order), step 1; 2026-09-30, [WG-I39](#WG-I39)), the shipped `bedrock-bridge` therefore refused codex, pi, opencode and oh-omp and warned claude; since then the route serves the row. An implementation decision | 2026-09-29 | [§4.1](#41-how-it-is-built) | 2026-09-29: `packs/codex/derive.lua` (`codexViaBedrock`); pinned by `TestTheShippedBedrockBridgeProfileMeetsEachAgentAsItCan` and `TestCodexOnABridgedBedrockProfileRidesItsViaRoute` |
| WG-I30 | **Runtime's Messages stream is relayed byte for byte, and an answer framed as AWS's binary event stream is refused 502 by name.** The framing is SOURCED, not observed. An implementation decision | 2026-09-29 | [WG-I30](#WG-I30) | 2026-09-29: `messagesPassthrough.serve`; `TestAnEventStreamAnswerIsRefusedByName` |
| WG-I31 | **The Messages upstream shares the route's signer and chain, and a Bedrock API key goes as `x-api-key` on it.** An implementation decision | 2026-09-29 | [WG-I31](#WG-I31) | 2026-09-29: `bedrockSigner.authorizeAs`, `messagesPassthrough.do`; `TestABedrockAPIKeyGoesAsXAPIKeyOnTheMessagesRoute`, `TestAnExpiredSignatureOnTheMessagesRouteIsRefreshedOnce`, and the 401 and 503 by `TestTheMessagesRouteKeepsTheSignersFailureStatuses` |
| WG-I32 | **A refusal on the Messages route keeps its status; an Anthropic-shaped body is relayed as sent, any other is put into Anthropic's shape.** An implementation decision | 2026-09-29 | [WG-I32](#WG-I32) | 2026-09-29: `relayMessagesError`; `TestMessagesRefusalsKeepTheirStatusInAnthropicsShape`, which also pins `Retry-After`, `x-should-retry` and both request ids coming back |
| WG-I33 | **A Messages stream that fails or ends before `message_stop` or an `error` event aborts the agent's connection; only the wait for headers is bounded.** Whether to watch follows the answer's `Content-Type`, not the request's `stream` flag. An implementation decision, WG-I24's rule on the adapter route | 2026-09-29 | [WG-I33](#WG-I33) | 2026-09-29: `messagesPassthrough.relay`, `sseClose`, `sendHeaderBounded`; `TestAMessagesStreamCutShortAbortsTheAgentsConnection`, `TestAMessagesStreamEndingBeforeMessageStopAborts`, `TestTheStreamWatchFollowsTheAnswersFraming`; over the real listener, `TestAnUnstreamedAnswerIsRelayedVerbatim` (a JSON answer is no cut) and `TestAnAgentThatHangsUpMidStreamIsNoCut` |
| WG-I34 | **A request's model is looked up in the list with its `[1m]` suffix trimmed, and sent trimmed; the list's ids are keyed the same way. Only a declared vendor counts: an id whose aliases declare different vendors stays translated, and an alias declaring none changes nothing. A pack's vendor does not follow an alias the user points at another id; a vendor the user's entry declares does.** An implementation decision | 2026-09-29 | [WG-I34](#WG-I34) | 2026-09-29: `messagesPassthrough.claims`, `withModel`, `anthropicModelIDs`, `packload.dropRepointedVendors`; `TestTheOneMillionSuffixIsTrimmedForLookupAndOnTheWire`, `TestAnthropicModelIDsReadsTheDeclaredVendor`, `TestAListIDSpelledWithTheOneMillionSuffixPassesThrough`, the conflict log line in `TestAnAnthropicModelGoesUntranslatedToBedrocksMessagesRoute`, `TestAUserLayerOverThePackListRoutesByTheDeclaredVendor` and `TestAVendorTheUserDeclaresRoutesTheirModel` (through `ComposeProviders` and `routeFor`), `TestARepointedAliasDropsTheShippedVendor`, `TestARepointedAliasKeepsTheVendorTheUserDeclares` |
| WG-I35 | **`count_tokens` stays refused for Anthropic models too**: runtime documents none on its Messages route. An implementation decision, amending [§3](#3-part-2--routing-by-model-id-for-claudes-everything-profile-ruled)'s "`count_tokens` survive[s]" | 2026-09-29 | [WG-I35](#WG-I35) | 2026-09-29: unchanged `bridgeHandler.ServeHTTP` refusal; `TestCountTokensStaysRefusedForAnAnthropicModel` |
| <a id="WG-I36"></a>WG-I36 | **On pi's Converse pass-through, pi's client sends a placeholder bearer and only the bridge signs** ([OQ-WG8](#OQ-WG8)'s (b)): pi's via override carries `apiKey = "local"`, the shape pi's derive already gives a keyless loopback row, so pi-ai's Converse client sends `Authorization: Bearer local` and no SigV4 signature, and the bridge drops it ([WG-I5](#WG-I5)) and signs with its own chain. **Why:** [OQ-WG5](#OQ-WG5) ruled a pass-through route for every native wire, Converse included, so (c) would reverse a ruling; and [OQ-BR4](provider-credential-scope.md#OQ-BR4) ruled delivery as specific as possible (*"no I don't want it to leak … as specific as possible"*), which (a) is not, since pi's client would go on signing with the credential on a path where only the bridge needs it. Environment narrowing stays [OQ-CN6](provider-credential-scope.md#OQ-CN6)'s. Where the override row lives, pi's built-in `amazon-bedrock` key or another, is [`pi-codex-provider-shadowing.md` OQ-3](pi-codex-provider-shadowing.md#OQ-3)'s, and the route is not built until that rules. Read from pi 0.87.1's source, not measured. An implementation decision, reversible: the placeholder is one derive field | 2026-09-30 | [OQ-WG8](#OQ-WG8) | — |
| WG-I37 | **The signer keys on the provider's `platform`: a provider of platform `aws-bedrock` is signed at any `https` address it names, and the host rule decides only for a provider that declares no Bedrock platform.** A Bedrock provider at a plain `http` address is not served. [OQ-WG1](#OQ-WG1)'s follow-up. An implementation decision | 2026-09-30 | [WG-I37](#WG-I37) | 2026-09-30: `wirebridged.bedrockSigning`, read by `routeFor` and `viaRoutesFor`; `TestTheSignerKeysOnTheProvidersPlatform` (the decision table, and a user's Bedrock provider at a proxy served signed on both routes), `TestOnlyABedrockRouteCarriesAnthropicModels` (a FIPS host's Messages route) |
| WG-I38 | **The signing region is the runtime host's, else the provider's `region`, else the served agent's `AWS_REGION` then `AWS_DEFAULT_REGION`, read at boot from its key channel; a value without a region's shape is refused by name.** An implementation decision | 2026-09-30 | [WG-I38](#WG-I38) | 2026-09-30: `sigv4.RegionVars`, `sigv4.ValidRegion`, `wirebridged.envRegion`, `route.resolveRegion`; `TestTheShippedBedrockBridgeReachesRuntimeInTheServedAgentsRegion`, `TestARegionNamedBedrockRouteIdlesWithoutARegion`, `TestADeclaredRegionComposesTheUpstreamAtBoot`, `TestARegionIsARegionAndComposesRuntimesHost` |
| WG-I39 | **A Bedrock provider that names no address is reached at runtime's own `/openai/v1` in the region; the wire bridge's chat-completions adapter declares `from_platforms: ["aws-bedrock"]`, so composition gives such a provider the adapter's anthropic address marked `for_via`, used only under a profile routing through the bridge.** The mark never makes a pairing unspeakable and is no endpoint for a profile without the via. copilot under the via starts on the list's first model. An implementation decision; copilot and oh-omp on `-p bedrock` still reach nothing | 2026-09-30 | [WG-I39](#WG-I39) | 2026-09-30: `wirebridged.regionalBedrock`, `packdecl.AdapterPair.FromPlatforms`, `packload.adaptEndpoints`, `packload.ForViaKey`, `packload.EndpointsForProfile`, `packs/wire-bridge/pack.json`, `packs/copilot/derive.lua`; `TestTheShippedBedrockBridgeProfileMeetsEachAgentAsItCan`, `TestTheAdapterAddressServesOnlyAViaProfile`, `TestTheBridgeFrontsTheRegionNamedBedrockForAViaProfile`, `TestCopilotReachesBedrockThroughTheBridgeOnlyUnderAVia`, `TestClaudeRidesTheAdapterRouteOnlyUnderAVia`, `TestNoNativeBedrockAgentIsRefusedOverTheBridgesAddress`, `TestTheAdapterFrontsExactlyThePlatformsTheDaemonReaches`, and at a real launch `TestBedrockBridgeCarriesPiToRuntimeInItsRegion` |
| WG-I40 | **The allowlist is per route: a via route follows its agent's profile, and the adapter route refuses only when every agent sharing it has `enforce_models` on and none is exempt.** An implementation decision ([OQ-WG3](#OQ-WG3)) | 2026-09-30 | [WG-I40](#WG-I40) | 2026-09-30: `wirebridged.allowlistsFor`, `adapterAllowlist`; `TestAViaRouteRefusesAModelOffANarrowedList`, `TestTheSwitchOffAndAListNoOnlyNarrowedRefuseNothing`, `TestTheAdapterRouteRefusesOnlyWhatEverySharerWouldBeRefused`, `TestClaudesOwnSpellingOfAListedKiloModelIsAdmitted` |
| WG-I41 | **A program whose background traffic is not on the list declares `unlisted_background_models`, and the bridge admits every model it sends, logging an off-list one; packs/codex and packs/copilot declare it.** An implementation decision, from [`model-lists-and-pickers.md` §14.3](model-lists-and-pickers.md#143-the-bridges-part-and-get-v1models)'s precondition | 2026-09-30 | [WG-I41](#WG-I41) | 2026-09-30: `packdecl.Contribution.UnlistedBackgroundModels`, `SendsUnlistedModels`; `TestUnlistedBackgroundModelsIsAProgramsFact`, `TestTheShippedAgentsSayWhoseBackgroundModelsAreOffTheList`, `TestAnAgentWhoseBackgroundModelsAreOffTheListIsAdmitted` |
| WG-I42 | **The daemon reads that declaration from the jail's staged pack tree, only when a list is narrowed, and refuses no model when it cannot read one.** An implementation decision | 2026-09-30 | [WG-I42](#WG-I42) | 2026-09-30: `wirebridged.allowlistsFor` over `entrypoint.LoadJailPacks`; the no-tree half of `TestAnAgentWhoseBackgroundModelsAreOffTheListIsAdmitted` |
| WG-I43 | **The refusal is a 400 `invalid_request_error` in the route's protocol naming the model, the provider, the list and the switch; a body naming `model` twice is refused.** An implementation decision | 2026-09-30 | [WG-I43](#WG-I43) | 2026-09-30: `modelAllowlist.checks`, `requestModel`; `TestAViaRouteRefusesAModelOffANarrowedList`, `TestTheAdapterRouteRefusesOnlyWhatEverySharerWouldBeRefused`, `TestAModelKeySpelledInAnotherCaseIsTheModel` |
| OQ-WG2 | **All-traffic mode is a property of the profile**, opt-in and off by default; one active profile per agent decides how it reaches the world | 2026-09-25 | [§6](#6-part-5--all-traffic-through-the-bridge-new-direction-design) | — |
| OQ-WG3 | **One list** (the picker's effective list after an `only`), **and a separate enforcement switch** on the profile, **default on**; off means the list only shapes pickers | 2026-09-25 | [§6](#6-part-5--all-traffic-through-the-bridge-new-direction-design) | 2026-09-30, [§6.1](#61-how-the-allowlist-is-built): the bridge refuses a model off a narrowed list while `enforce_models` is on ([WG-I40](#WG-I40)–[WG-I43](#WG-I43)) |
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
| Runtime's Messages base URL is `https://bedrock-runtime.{region}.amazonaws.com/anthropic`; a `curl` posts the first-party body (`model`, `stream`) to `/anthropic/v1/messages` with `anthropic-version: 2023-06-01` and the Bedrock API key as `x-api-key`; streaming uses the plain Anthropic SDK, `Anthropic(base_url=".../anthropic")` with `messages.stream` | *Inference using Anthropic Messages API* page, read 2026-09-29 |
| SigV4 and Bedrock API keys both supported on `bedrock-runtime`; IAM `bedrock:InvokeModel`, and `bedrock:InvokeModelWithResponseStream` for streaming; prompt caching supported on runtime | Bedrock endpoints page, read 2026-09-29 |
| Claude models that launched with cross-Region inference only have no CountTokens on `bedrock-runtime`; Anthropic's `count_tokens` is at `bedrock-mantle`'s `/anthropic/v1/messages/count_tokens` | *Monitor your token usage by counting tokens* page, read 2026-09-29 |
| Only `AnthropicBedrock` (InvokeModel: `/model/{id}/invoke-with-response-stream`) overrides `_make_sse_decoder` with `AWSEventStreamDecoder`; `AnthropicBedrockMantle` keeps the default SSE `Stream` and signs as `bedrock-mantle` | anthropic-sdk-python `main`, `src/anthropic/lib/bedrock/_client.py`, `_stream_decoder.py`, `_mantle.py`, read 2026-09-29 |

**Client sources**, read 2026-09-26; nothing was run:

| Claim | Source |
| :--- | :--- |
| codex joins a custom provider's `base_url` and a path as `format!("{base}/{path}")` | codex `rust-v0.157.0` (the version installed here), `codex-rs/codex-api/src/provider.rs` `Provider::url_for_path` |
| codex sends no `Authorization` for a provider with neither `env_key` nor `requires_openai_auth`; a declared `env_key` that is unset or empty is an error, raised each time a client is set up rather than at startup | `codex-rs/model-provider/src/auth.rs` `resolve_provider_auth`; `codex-rs/model-provider-info/src/lib.rs` `api_key` |
| codex uses Responses over WebSocket only when the provider sets `supports_websockets`, false by default for a custom provider, and compresses request bodies only for the OpenAI provider on ChatGPT auth | `codex-rs/core/src/client.rs` `responses_websocket_enabled`, `responses_request_compression`; `model-provider-info` `ModelProviderInfo` |
| oh-omp's `openai-completions` client is `new OpenAI({ baseURL })`, whose `buildURL` is `new URL(baseURL + path)` | `@oh-labs/oh-omp-linux-x64` 0.15.3 (`npm pack`), strings of the bundled `packages/ai/src/providers/openai-completions.ts` and `openai` 6.33.0 `client.mjs` |
| pi's Converse client uses a non-standard `baseUrl` as its endpoint, keeps the endpoint's path (`@smithy/core` `requestBuilder.bp`), and under `AWS_BEDROCK_SKIP_AUTH=1` signs with `dummy-access-key` / `dummy-secret-key` | pi 0.87.1, `dist/bundle/chunks/bedrock-converse-stream.js` |
| pi's Converse client sends a bearer and no SigV4 signature when a bearer token resolves (`options.bearerToken`, then `options.apiKey`, then `AWS_BEARER_TOKEN_BEDROCK`) and `AWS_BEDROCK_SKIP_AUTH` is not `1`: it sets `config.token` and `authSchemePreference = ["httpBearerAuth"]` | pi 0.87.1, `pi-ai/dist/api/bedrock-converse-stream.js` |
| a `models.json` `apiKey` on `amazon-bedrock` becomes that request `apiKey` with no environment variable | pi 0.87.1, `dist/core/provider-composer.js` `composeApiKeyAuth`; `pi-ai/dist/providers/amazon-bedrock.js` `resolve` |
| the SDK encodes a non-greedy path label, such as ConverseStream's `/model/{modelId}/converse-stream`, whole with `extendedEncodeURIComponent`, so a `/` in a model ARN crosses as `%2F` | pi 0.87.1's `@smithy/core` `protocols` `resolvedPath`; `@aws-sdk/client-bedrock-runtime` `dist-cjs/index.js` |

**Repo claims**, re-checkable in seconds:

```console
$ rg -n 'func adaptEndpoints|func routeFor|doUpstream' internal/packload internal/wirebridged
$ rg -n -i 'sigv4|aws4_request' internal cmd vendor/modules.txt   # expect no output
$ rg -n -F '[1m]' internal/wirebridge/request.go
$ rg -n 'api.anthropic.com' packs/*/pack.json                     # expect no output
```
