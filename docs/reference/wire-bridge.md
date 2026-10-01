---
status: current
stage: CURRENT
verified: 2026-10-01
verified_commit: d4e435a3
covers:
  - internal/wirebridge/
  - internal/wirebridged/
  - internal/packload/needs.go
  - internal/packdecl/needs.go
  - internal/cli/run/packservices.go
  - internal/cli/run/macosuserservices.go
  - internal/launchservice/
  - internal/cli/run/providerlocal.go
  - internal/cli/run/hostports.go
  - internal/cli/run/callertokens.go
  - internal/sigv4/
  - internal/packload/via.go
  - internal/packload/carrier.go
  - packs/wire-bridge/
tags: [packs, providers, services, claude, translation, needs, networking, diagnosis]
summary: "A translating reverse proxy, in a jail or run for one host or macos-user launch, that manufactures an Anthropic-Messages endpoint on the jail's loopback for OpenAI chat-completions providers and Claude's Codex Responses profile, and passes a via agent's own OpenAI chat-completions or Responses traffic through to its provider — plus the `service` contribution kind and `needs`, a pack dependency resolved at selection."
---

# The wire bridge — an Anthropic endpoint on the jail's loopback

**Status:** verified 2026-10-01 against `d4e435a3`, the whole doc. What has been watched
running, by area:

- **The adapter routes, and [streamed usage](#streamed-usage).** The usage mapping is read from
  Claude Code's bundled script and the providers' SDKs, and tested in-process; against a live
  upstream it is MEASURED only on Bedrock, as below, and unmeasured on Cerebras and Kilo. On the host that reported the listen-port collision, that the
  provider-table fix ends it is inferred from an in-process reproduction, and no launch there has
  been observed succeeding
  ([what can hold the listen port](#what-can-hold-the-listen-port-before-the-bridge-does)). The
  first translated request to reach a live upstream, on 2026-10-01, was refused: Bedrock's GPT-6.1
  Sol answered the `max_tokens` the translation then sent with a 400. Since the route sends
  [the output cap](#the-output-cap) as `max_completion_tokens`, Sol and GPT-6 Astra answered four
  requests the same day, streamed and not, and each streamed answer's `message_delta` carried the
  upstream's input and output counts
  ([`wire-bridge-gateway.md` §2.4](../design/wire-bridge-gateway.md#24-the-first-live-requests-measured-2026-10-01)).
  No agent sent them.
- **[The via route](#the-via-route--one-route-per-agent-under-agentname)**, both wires. MEASURED
  in-process against a stubbed upstream, and its Responses wire against real Bedrock on
  2026-10-01 with a request shaped like codex's
  ([`wire-bridge-gateway.md` §2.4](../design/wire-bridge-gateway.md#24-the-first-live-requests-measured-2026-10-01));
  no agent has sent a request through it.
- **[The host half](#at-the-host-notch).** MEASURED through `hostMain` in `internal/cli`'s unit
  tests, a real host half serving a fake agent against a stubbed upstream and a fake host broker,
  and the macos-user arm by unit tests with the start stubbed. No agent CLI and no Mac have run it.
- **[The Messages pass-through](#the-messages-pass-through-on-a-bedrock-upstream).** Its route
  and stream framing are SOURCED from AWS's documentation and the Anthropic SDK's source, and it
  is MEASURED in-process against a fake upstream serving that documented format. MEASURED live on
  2026-10-01: a streamed Claude Opus 5.5 request reached runtime through it, signed by the bridge,
  and came back as Anthropic server-sent events
  ([`wire-bridge-gateway.md` §2.4](../design/wire-bridge-gateway.md#24-the-first-live-requests-measured-2026-10-01)).
  No agent has sent a request through it.
- **[Which upstream is Bedrock's](#which-upstream-is-bedrocks), the region-composed upstream and
  [the model allowlist](#the-model-allowlist).** MEASURED the same way: the production boot over
  the shipped packs, against a stubbed upstream. On 2026-10-01 the boot over a jail's own tables
  signed for the shipped `bedrock` provider and composed runtime's `us-east-1` URL, and AWS
  answered each request it sent, refusing none for its signature
  ([`wire-bridge-gateway.md` §2.4](../design/wire-bridge-gateway.md#24-the-first-live-requests-measured-2026-10-01)).
  No request has tested the allowlist against AWS.

A **wire bridge** *(coined here)* is a daemon that manufactures, on the agent's loopback, a wire
protocol a provider does not natively serve, by translating to one it does. It runs in the jail,
or, at the host and on macos-user, for one launch ([the host half](#at-the-host-notch)).
Two translating routes exist: **Anthropic Messages → OpenAI chat-completions** for declared
providers, and the Claude Codex-profile route to OpenAI Responses described in
[`omp-and-codex-claude-profile.md`](omp-and-codex-claude-profile.md). A third kind translates
nothing: the [via route](#the-via-route--one-route-per-agent-under-agentname), which a profile's
`via` selects, passes an agent's own OpenAI chat-completions or Responses traffic through to its
provider, adding only the credential. And on a Bedrock upstream the Anthropic route itself
translates only some requests: a Claude model the provider's list marks as Anthropic's goes
through untranslated to Bedrock's own Messages route
([the Messages pass-through](#the-messages-pass-through-on-a-bedrock-upstream)).

It exists because an agent can speak exactly one wire protocol and some providers serve only the
other one. No amount of configuration closes that gap: yolo's derives translate config *dialects*,
and a wire protocol is not a config dialect. So the missing thing is a protocol speaker, and it
has to run somewhere — the jail is the only place that needs it, because what the agent consumes
is a base URL, and a base URL can be the jail's own loopback.

**The endpoint trick is the load-bearing part, and it changes no derive.** The bridge declares
its own address: `packs/wire-bridge` carries an `adapter` contribution per conversion —
`adapts: {from, to}` plus an `address` — and core composes that address into every provider entry
that offers the `from` and lacks the `to`
([`protocol-resolution.md`](protocol-resolution.md)). The agent's existing derive emits the
composed URL as its base URL exactly as it would emit any other. The derive cannot see whether a
bridge exists, and should not.

<a id="the-listen-address"></a>

> [!WARNING]
> **The listen address is manifest-borne and overridable, and BOTH routes now honour it.** Each
> derives its bind from the composed provider entry, which is where an `adapters` override is
> already applied — so a user-scope `adapters.<from>-><to>.address` moves the listener and the
> agent together, on either route.
>
> ⚠ **Do not let a route choose its bind before it reads the composed entry.** The Codex
> `openai-responses → anthropic` route is selected by agent and provider name in `routeFor`, and a
> branch that returns there before reading `endpoints.anthropic.base_url` binds
> `wirebridged.CodexResponsesListenAddr` whatever the `adapters` key says, while Claude's base URL
> follows the override — the agent then dials a port nothing listens on. The constant is the
> DEFAULT for an entry naming no anthropic endpoint, never a second writer of the port.

| Component | Lives in |
| :--- | :--- |
| Translation, both directions, I/O-free | `internal/wirebridge` (`request.go`, `response.go`, `stream.go`, and `responses.go` for the Codex route) |
| The daemon: listener, upstream dial, SSE framing, status codes, key read | `internal/wirebridged` (`boot.go`, `handler.go`, `endpoint.go`, `keyfile.go`) |
| The serve-or-idle decision, made once | `internal/wirebridged` (`WillServe`, `routeFor`) |
| `needs` — the schema and its validation | `internal/packdecl` (`needs.go`, `PackNeed`, `WhenBins`) |
| `needs` — the selection closure | `internal/packload` (`ResolveNeeds`) |
| Wiring the endpoint and readiness variables only when the daemon will serve | `internal/cli/run` (`serviceEndpointEnvArgs`) |
| The daemon's one log sink, and the sentence naming what holds a port | `internal/wirebridged` (`diag.go`, `describePortHolder`), over `internal/listeners` |
| Implicit localhost-provider forwards, their merge and their disclosure | `internal/cli/run` (`localProviderForwardSources`, `mergeHostForwards`, `discloseImplicitProviderForwards`, `briefingPortsFor`) |
| In-jail forwarders, the readiness wait, the orphan refusal | `internal/entrypoint` (`startContainerPortForwarding`, `startJailDaemonSupervisor`, `refuseOnOrphanedJailDaemons`) |
| The pack itself — the first `kind: "service"`, and the two `adapter` contributions that declare its addresses | `packs/wire-bridge` |
| The via route: the per-agent table, the prefix mux, the wire split, the pass-through, per-upstream credentials | `internal/wirebridged` (`via.go`: `viaRoutesFor`, `viaUpstreams`, `viaMux`, `viaWireSplit`, `passthroughHandler`, `viaHandlerFor`); the serve plan in `boot.go` (`planFor`, `servePlan`) |
| The Messages pass-through on a Bedrock upstream: the list's Anthropic ids, the split by model, the untranslated forward and relay | `internal/wirebridged` (`messages.go`: `anthropicModelIDs`, `messagesPassthrough`; `route.AnthropicModels` in `boot.go`; the split in `bridgeHandler.ServeHTTP`) |
| The via selection: the profile field, the address, the pack closure, the per-agent URL | `internal/packdecl` (`ProfileContribution.Via`, `ServiceContribution.ViaAddress`); `internal/packload` (`via.go`: `ResolveVias`; `profiles.go`: `ViaURLFor`) |
| Which upstream is Bedrock's, its region, and runtime's URL composed from it | `internal/wirebridged` (`bedrockroute.go`: `bedrockSigning`, `regionalBedrock`, `envRegion`, `route.resolveRegion`); `internal/sigv4` (`RegionVars`, `ValidRegion`, `BedrockRuntimeHost`) |
| The adapter address composed onto a provider of a fronted platform, marked for a via | `internal/packdecl` (`AdapterPair.FromPlatforms`); `internal/packload` (`adaptEndpoints`, `ForViaKey`, `EndpointsForProfile`); `packs/wire-bridge` (`from_platforms`) |
| The model allowlist | `internal/wirebridged` (`allowlist.go`: `allowlistsFor`, `adapterAllowlist`, `modelAllowlist.checks`); `internal/packdecl` (`unlisted_background_models`, `SendsUnlistedModels`) |

**Reads with:** [`providers.md`](providers.md) (what a provider declares and which agent reads
which endpoint — the authority), [`pack-system.md`](pack-system.md) (the contribution model, and
the `needs` key's place in it), [`loophole-protocol.md`](loophole-protocol.md) and
[`loophole-system.md`](loophole-system.md) (the daemon machinery a service reuses, and the trust
model it does *not* need), [`loopback-tls-reachability.md`](loopback-tls-reachability.md) (the
witness that makes an unpublishable endpoint fatal).

---

## What a wire bridge is not

- **Not a loophole.** A loophole reaches *out* from the jail to a host capability the jail lacks:
  the host grants something and the security model reviews it. The bridge serves *inward* — it
  binds the jail's loopback, crosses no boundary, reads no host state, and needs no grant. The
  *machinery* is the same (a supervised in-jail daemon, an endpoint file, the reachability
  witness); the *trust* is not, and the vocabulary says so.
- **Not a gateway.** One upstream, chosen by the launch's own selection machinery. No routing
  tables, no failover, no budgets, no model remapping beyond what translation requires. A gateway
  is a product; a bridge is a shim. That was the bridge as first built. SigV4 signing for a
  Bedrock upstream is now built ([Auth](#the-protocol-surface)), and so are routing by model id
  on one ([the Messages pass-through](#the-messages-pass-through-on-a-bedrock-upstream)), the
  all-traffic mode a profile's `via` selects
  ([the via route](#the-via-route--one-route-per-agent-under-agentname)) and its
  [model allowlist](#the-model-allowlist); opt-in failover is ruled and unbuilt, all in
  [`wire-bridge-gateway.md`](../design/wire-bridge-gateway.md).

## `kind: "service"` — the vocabulary it landed as

A **service** *(coined here; the bridge is the first instance)* is a daemon a pack contributes to
a namespace — a jail daemon, a host daemon, or both — plus its endpoint file, its restart policy,
and its reachability witness. **No grants, no boundary, no host state.**

One kind carries both halves because they share the lifecycle, the endpoint and the witness, and
differ only in which namespace the daemon lands in. Both run: a container launch runs the jail
daemon, and a host or macos-user launch runs the host half for its own command
([the host half](#at-the-host-notch)).

The decomposition that makes this coherent, and the map for re-forming the existing loopholes
around it:

| Belongs to the **service** half | Belongs to the **loophole** half — the boundary |
| :--- | :--- |
| the jail daemon and its restart policy, the endpoint machinery, the witness | a daemon on the *host* side of the boundary |
| the platforms it can run on at all | host filesystem and device grants *into* the jail |
| the capabilities it names as served | host state crossing the boundary, least-privilege scoped |
| the config keys it owns | the interception broker, host-capability probes, environment injected on a loophole's behalf |

**Loopholes form around services**: a loophole is its service half *plus* boundary grants layered
on, rather than a monolith that happens to contain a daemon. Re-forming the shipped loophole packs
that way is a named follow-up and explicitly **not** a prerequisite — their manifests keep their
meaning, and the table above is the map that follow-up walks.

> [!WARNING]
> **Do not reuse the loophole manifest for a service "temporarily".** Vocabulary accreted
> temporarily is how a four-value enum with two meanings happens: a kind that misnames its
> instances does not get split later, it gets copied.

## `needs` — a conditional pack dependency

A pack manifest may carry a top-level `needs` array. Each entry names another pack and the
condition under which the dependency is live:

- **the named pack** must be one of the **embedded official** packs. A fetched pack `needs`-ing
  another fetched pack would make *selection itself* a supply-chain channel; refusing it keeps the
  `packs` key the only place unreviewed code enters a launch.
- **the condition** is a set of **bins**: the need is live when some *selected* pack installs one
  of them. Core speaks bins, not agents — "claude" here means "a selected pack installs the
  `claude` CLI", which is exactly "claude is in use". Multiple bins are OR'd. No condition means
  unconditional; Claude uses this for its Codex profile's credential service and bridge, which
  then idle unless that profile is selected.

**Resolution happens host-side, at selection, before staging** — because the mount is the filter,
so the closure must be computed before anything stages:

1. Start from the user's `packs` list.
2. For each selected pack, for each live need, add the named pack if absent.
3. Repeat until a full pass adds nothing — a **transitive closure**, so a need whose condition
   only a later addition satisfies is still honored. A **cycle refuses the launch, naming the
   loop.**
4. **Print every addition, with its cause.** One line at launch, and the same line in
   `yolo check`.
5. From there the added pack is *ordinary selection*: staged, footprint-accounted, preflighted,
   its daemon contributed. There is no special path after the closure runs.

The closure is **pure**: it performs no I/O and reaches nothing outside its arguments — the caller
hands it the selected packs and a predicate over the universe a need may draw from. That is what
lets the launch pipeline and `yolo check` share one resolution, and it is why *printing* is the
caller's duty rather than the function's.

> [!WARNING]
> **A pack nobody typed joining a launch silently is the one forbidden behavior.** The selected
> set is no longer literally the user's list, which is exactly the hazard the config-change
> confirmation exists for — and printing is the mitigation. It is not a config-change prompt:
> nothing in the user's config changed.

> [!NOTE]
> **The embedded-only rule is checked on the DECLARATION, ahead of the join.** A need naming a
> pack outside the universe refuses whether or not the user happens to also select that pack
> today. Masked, it would surface the day they dropped it — as a launch failure for a manifest
> they never changed.

> [!NOTE]
> **User config carries no `needs` key** — manifests only. So a user-declared jail-local
> `anthropic` endpoint with no bridge pack selected is a dead URL of the user's own making, and
> `yolo check` reports it as a **warning, never a refusal**. That is the one behavior an
> "error if the bridge is missing" design would have guarded, kept as diagnosis instead.

## Coarse condition, lazy daemon

The condition is deliberately *coarse*: "a bridge consumer is selected", not "that agent's active
profile routes at a bridged provider". The precision is recovered at the other end, by the daemon
being **selection-lazy**.

At boot the daemon reads the composed provider table and the resolved selection. If some agent's
active profile names a provider whose `anthropic` endpoint is jail-local — routed *at this
bridge* — it serves that provider's `openai` endpoint upstream. Otherwise it **idles healthy**:
binds nothing, publishes nothing, and prints one line naming the exact absent fact — once per
distinct reason, because the answer is re-evaluated on every poll of the channel. While idle, it
watches the complete per-entry channel for a later attach and wakes when that attach selects a
routed provider. Once serving, its upstream stays fixed for the daemon lifetime: concurrent
entries may still use the earlier route, so the latest attach must not redirect their traffic.

The one boot on which an idle is **not** healthy is a boot waiting on the bridge's readiness. The
launcher asks for readiness only when its own serve decision said *serve*, so a daemon that idles
there has disagreed with the launcher about one decision. It answers `failed` with its idle reason
rather than leaving the entrypoint waiting for an attach that will never come
([the readiness wait](#lifecycle-and-failure-behavior)).

Coarse condition + lazy daemon = precise behavior, with vocabulary a manifest can state without
knowing anything about profiles. The cost is a healthy idle process in launches where the agent
rides something else.

> [!IMPORTANT]
> **The reachability witness is not free, and the fix is one decision with two call sites.** The
> witness keys off the per-service endpoint environment variable the *launcher* sets. Emitting it
> unconditionally would make every *idle* bridge a fatal "unpublished service" — the exact
> contradiction of the healthy idle. So the serve-or-idle decision is **one pure function over the
> composed tables**, called by the launcher when it decides whether to emit the variable and by
> the daemon when it decides whether to serve. The variable, the endpoint file and the witness
> probe cannot disagree without the code having been forked first.
>
> The variable's *value* is the endpoint file name the **manifest** declares, read off the
> contribution rather than reconstructed from the daemon's own constant: if the two ever name
> different files, the variable points at a file nothing writes and the witness says so loudly,
> which is the failure a silent reconstruction would hide.

**Who is served is decided by the selection table, not by an agent name.** More than one agent's
derive prefers a provider's `anthropic` endpoint when it declares one, so a bridged URL reaches
each of them the same way. The consumers of an anthropic endpoint decide the need's condition; the
selection table decides the serving. One table, so the two cannot disagree about who the bridge is
for.

## The protocol surface

The bridge implements **what the agent sends**, not the whole Anthropic API.

| Inbound | Bridge does | Upstream |
| :--- | :--- | :--- |
| `POST /v1/messages` (non-streaming) | translate | `POST /chat/completions` |
| `POST /v1/messages` (SSE) | translate event-for-event | the same, with streaming on |
| `POST /v1/messages/count_tokens` | **refuse (404)** | no upstream call |
| top-level `system` (string or block array) | flatten to one system message | a leading system message; on the Codex Responses route, `instructions` |
| `messages[].role: "system"` (text only) | preserve its conversation position | a system message; on the Codex Responses route, a `developer` input message |
| text and image blocks | copy; images as base64 data URIs | content parts |
| `tools[]` with an input schema | rename the schema field; **never request strict mode** | `tools[]` |
| Claude `web_search_*` server tools on the Codex route | translate each versioned Anthropic definition to Responses' hosted `web_search`; discard its provider-only lifecycle records while retaining the final answer | hosted Responses web search |
| tool-use / tool-result blocks | bidirectional mapping | tool calls / tool-role messages |
| thinking config and beta headers | chat-completions route: **strip**; Responses route: translate `enabled` budget to a conservative effort, while every non-budget mode leaves the provider default | documented Responses reasoning option, when explicit |
| response retention and token cap | Codex subscription Responses route: set `store: false` and omit Claude's token cap; other Responses routes preserve their documented cap mapping | ChatGPT subscription endpoint requires no retention and rejects `max_output_tokens` |
| upstream reasoning content | **drop, do not surface** | plain text deltas only |
| token and stop-sequence limits | map; on the chat-completions route the token limit goes as `max_completion_tokens`, or as `max_tokens` for a provider that declares `max_tokens_field` `"max_tokens"` ([the output cap](#the-output-cap)) | the upstream's equivalents |
| stop reasons | map onto the upstream's finish reasons | finish reason |
| usage | map into Anthropic's terms: the upstream's prompt count **minus** its cached count is `input_tokens`, the cached count is `cache_read_input_tokens`, completion tokens are `output_tokens`; streamed, all three ride `message_delta` ([streamed usage](#streamed-usage)) | usage |
| the stream flag, on the chat-completions route | pass it, and also ask for `stream_options.include_usage`, unless the provider declares `supports_usage_in_streaming` `"false"` | the upstream's final usage chunk |
| cache-control blocks | strip — silently ignored, never an error | — |
| the model id | **passthrough**, normalized only for gateways: Claude's `[1m]` context suffix is stripped, and a bare `deepseek-…` id gains its `deepseek/` vendor prefix | model |

**Auth.** Inbound: the launch's **caller token**, which every request must carry and which the
bridge checks and never forwards ([caller authentication](#caller-authentication),
[WB-D18](#wb-d18); until 2026-09-28 this was "none"). Outbound: a bearer header carrying the
provider's key, read once at boot, for every upstream but one kind. A Bedrock upstream is
**signed with SigV4** instead (`internal/sigv4`, built 2026-09-25): one on a provider whose
`platform` is `aws-bedrock`, at any `https` address, or one whose host is
`bedrock-runtime.<region>.amazonaws.com` on any provider
([which upstream is Bedrock's](#which-upstream-is-bedrocks),
[OQ-WG1](../design/wire-bridge-gateway.md#OQ-WG1)). Its credential chain is the AWS SDK's order:
a static `AWS_ACCESS_KEY_ID` + `AWS_SECRET_ACCESS_KEY` pair, then the `aws-auth` pointer
`AWS_CONTAINER_CREDENTIALS_FULL_URI`, then `AWS_BEARER_TOKEN_BEDROCK` sent unsigned as a bearer.
Exactly one is used per request, so a signature and a bearer never travel together.
- The variables are read once at boot from the key channel below; the credential behind the
  pointer is fetched lazily, single-flight, and cached until five minutes before its
  `Expiration`.
- No source set: the route idles unpublished. A source set but unusable per request: no source
  resolves is a 401 naming all three; `aws-auth` unreachable is a 503 naming it and
  `aws sso login`; `aws-auth`'s own refusal is a 401 carrying its message.
- AWS refusing a signature or session token as expired gets one refresh and one retry. Any other
  AWS error is relayed with AWS's own message.
- A FIPS or VPC endpoint, or a proxy, is signed when its provider declares the platform; on a
  provider that declares none, an address the runtime-host pattern does not name carries the
  provider's bearer key, as any other upstream does.

> [!WARNING]
> **`count_tokens` refuses rather than answering.** No estimate, no zero-stub. A measured
> alternative answers it with a zero count, which is exactly why an agent then displays "0
> tokens" — a zero-stub poisons the count it was asked for. A 404 sends the agent to its own
> estimator, which is a real estimate, so refusing is the only answer that invents nothing and
> lies about nothing.

> [!WARNING]
> **Never request strict-mode tool schemas upstream.** A strict-mode implementation rejects
> schema keywords that an agent's tool definitions contain freely, so the request fails on
> something the bridge could have simply not asked for.

> [!NOTE]
> **Only the compatible hosted server tool crosses the Codex route.** Claude's versioned web
> search definitions and Responses' hosted web search have the same server-executed contract;
> the bridge translates them directly. Other Claude server tools remain refused by name rather
> than being guessed at or misrepresented as client-executed functions.

> [!WARNING]
> **Do not surface upstream reasoning as thinking blocks.** Emitting them obliges the bridge to
> strip them on replay — agents echo thinking back — which is real complexity for no
> coding-agent value. The chat-completions route always leaves the upstream reasoning default
> alone. On the Responses route, an explicit `enabled` budget maps conservatively to `medium`
> or `high`; every other thinking mode omits the option, so the provider chooses its default.

> [!NOTE]
> **The ChatGPT subscription Responses route is narrower than the public Responses API.** It
> requires `store: false` and rejects `max_output_tokens`. The bridge always requests no
> retention and omits Claude's token cap on this route; a model's own output limit remains in
> force.

### The Messages pass-through, on a Bedrock upstream

Everything the table above strips, a Claude model on Bedrock keeps. When the Anthropic route's
upstream is Bedrock's ([which upstream is Bedrock's](#which-upstream-is-bedrocks)), the bridge
reads the provider's model list at boot, and each request's `model` picks one of two upstreams
([`wire-bridge-gateway.md` §3.1](../design/wire-bridge-gateway.md#31-how-it-is-built)):

- **A model the list declares `"vendor": "anthropic"` for** is forwarded untranslated to
  `/anthropic/v1/messages` where the upstream's own `/openai/v1` is, Bedrock's own Anthropic
  Messages route (`https://bedrock-runtime.<region>.amazonaws.com/anthropic/v1/messages` on
  runtime's own host). A proxy that serves runtime under a path prefix keeps it, and an upstream
  whose address does not end in `/openai/v1` gets the route at its host's root. The body crosses as the agent sent it, so `cache_control`, `thinking`,
  `metadata`, tools and every other field reach Claude unchanged. The agent's `anthropic-version`
  (`2023-06-01` when it sends none) and `anthropic-beta` headers go with it, and its caller token
  never does. The answer, streamed as Anthropic server-sent events or not, is relayed byte for
  byte, thinking blocks and cache usage included.
- **Every other model** (another maker's, one listed with no vendor, one not listed) is
  translated to chat-completions exactly as the table says. The id is looked up in the list and
  never parsed for a maker, so a Claude model reaches the pass-through only by being listed with
  its vendor. Claude's `[1m]` suffix is trimmed for the lookup and from the body sent, and from
  the list's own ids, so a list that spells an id `…[1m]` for claude names the same model.

**Where the vendor comes from.** The list entry declares it. A pack declares it as
`model_options.<alias>.vendor` on its provider: a company pack's, or the local pack's. A user
declares it as the `vendor` of an object-form entry in their own `providers` config, such as
`"mine": {"id": "<a Claude model id>", "vendor": "anthropic"}`
([providers.md](providers.md#the-shipped-bedrock-provider)). A plain `"alias": "id"` declares
none, so a model the user adds that way is translated. Two consequences of reading the
declaration rather than the id:

- An alias that declares no vendor changes nothing. A user's plain alias for a pack's Claude id
  leaves that id on the pass-through. Two aliases that declare *different* vendors for one id,
  a pack's and a user's included, leave it translated, and the serve log names the id.
- A pack's vendor belongs to the id the pack wrote. When the user's config points the pack's
  alias at another id, composition drops the pack's vendor for it
  ([providers.md](providers.md#a-re-pointed-alias-loses-the-packs-vendor)), so that id is
  translated unless the user's entry declares its own vendor.

Both upstreams share the route's SigV4 signer and credential chain. A Bedrock API key goes as
`x-api-key` on the Messages route, the header AWS documents there. On the Messages route:

| Case | What the agent gets |
| :--- | :--- |
| an upstream refusal | its own status. An Anthropic-shaped body is relayed as sent; AWS's `{"message": …}` envelope is put into the Anthropic shape with AWS's message |
| an expired signature | one credential refresh and one retry, as on the translating upstream |
| a credential the bridge cannot resolve | the signer's answer, as on the translating upstream, before anything is sent: a 401 naming the sources it tried, or a 503 naming `aws-auth` and `aws sso login` when that credential service does not answer |
| an answer framed as AWS's binary event stream (`application/vnd.amazon.eventstream`) | a 502 naming that framing, since AWS documents SSE for this route |
| an answer that fails mid-body, or an SSE answer (whether or not the request asked to stream) that ends before `message_stop` or an `error` event | its connection aborted, so a truncated answer never reads as finished; the log names the model, the byte count and the cause. The agent hanging up is not logged as a cut |
| no response headers within ten minutes | a 504. Once headers arrive the stream runs as long as the upstream keeps sending |
| `POST /v1/messages/count_tokens` | still **404** ([WB-D14](#wb-d14)): runtime documents no `count_tokens` on this route |

The serve line names the Messages URL and the ids it carries, and each request line that went
untranslated says so, with its model id. No body is ever logged.

<a id="which-upstream-is-bedrocks"></a>

### Which upstream is Bedrock's, and the region-composed one

**The provider's `platform` decides, then the host**
([`WG-I37`](../design/wire-bridge-gateway.md#WG-I37)). A provider whose `platform` is
`aws-bedrock` is signed with SigV4 at whatever `https` address it names: runtime's own host, a
FIPS or VPC endpoint, or a proxy. A provider that declares no such platform is signed only when
its upstream host is exactly `bedrock-runtime.<region>.amazonaws.com`, and any other address
carries the provider's bearer key as before. A Bedrock provider at a plain `http` address is not
served: the route idles with that reason.

**The region** ([`WG-I38`](../design/wire-bridge-gateway.md#WG-I38)) is the runtime host's
region when the upstream is that host; else the provider's own `region`; else the served agent's
`AWS_REGION`, then `AWS_DEFAULT_REGION`, read at boot from the same key channel as its credential
(the credential gate's region fill puts a region from `~/.aws/config` in `AWS_REGION`). A value
that is not a region's shape is refused by name, never used, so no variable can name a host. With
no region, the adapter route idles and a via route answers `503`, each naming the variables it
read. The serve line says which variable the region came from.

**A Bedrock provider that names no address** ([`WG-I39`](../design/wire-bridge-gateway.md#WG-I39)),
such as the shipped `bedrock`, is reached at runtime's own
`https://bedrock-runtime.<region>.amazonaws.com/openai/v1`, composed from that region:

- **On a via route** that one base carries both wires, so pi's, opencode's and oh-omp's
  chat-completions and codex's Responses reach it alike.
- **On the adapter route**, which claude and copilot reach, the provider needs an anthropic
  address to be routed at. `packs/wire-bridge`'s `openai → anthropic` adapter declares
  `"from_platforms": ["aws-bedrock"]`, so composition gives such a provider the adapter's address
  and marks it `"for_via": "wire-bridge"` in the composed table. The mark means the address is
  used only for an agent its profile routes through the bridge: under a profile whose `via` names
  the bridge (`bedrock-bridge`), or, on a profile naming no via, an agent with no client of the
  provider's platform, which the bridge carries
  ([WG-I44](../design/wire-bridge-gateway.md#WG-I44); copilot, and oh-omp on its via route, on
  `-p bedrock`). The daemon serves the route only for such an agent, copilot's derive composes
  nothing without one, and on `-p bedrock` claude keeps its own Bedrock client. An address with
  the mark never makes a provider unspeakable to an agent that cannot use it, and is no endpoint
  at all for an agent its profile does not route through the bridge, so codex, opencode and pi on
  `-p bedrock` are unchanged ([protocol resolution](protocol-resolution.md#an-address-composed-for-a-via)).
  Which agents a profile naming no via routes through the bridge crosses in `YOLO_PROFILES` under
  `_carrier`, `_carrier_base` and `_carried`, beside the via pair's reserved keys.

<a id="streamed-usage"></a>

### Streamed usage

Claude builds its cost and context figures from the usage in the stream, so a bridged stream has
to carry the upstream's real counts, in Anthropic's shape. Both routes now do, and the non-streaming
answers use the same mapping, so one turn reports one set of numbers whether it streamed or not.

**Where the counts go.** Anthropic's stream reports usage twice: in `message_start`, and in
`message_delta`, whose usage is cumulative and may carry `input_tokens` and the cache counts beside
`output_tokens`. An OpenAI upstream reports usage only at the end of its stream, so the bridge sends
`message_start` with what the first chunk reported (for most upstreams, zeros) and puts the real
counts in `message_delta`. That is enough for Claude: Claude Code 2.1.282's stream handler merges a
`message_delta`'s `input_tokens`, `cache_read_input_tokens` and `cache_creation_input_tokens` over
what `message_start` said whenever they are above zero (READ from the shipped binary's bundled
script, not measured against a live turn).

**The arithmetic.** Both OpenAI dialects count cached tokens *inside* their prompt figure
(`prompt_tokens` and `prompt_tokens_details.cached_tokens` on chat completions; `input_tokens` and
`input_tokens_details.cached_tokens` on Responses). Anthropic's `input_tokens` is the *uncached*
remainder. So the bridge reports prompt minus cached as `input_tokens` and the cached count as
`cache_read_input_tokens`. An upstream that reports more cached tokens than prompt tokens gets
`input_tokens` 0, never a negative count. OpenAI's wire has no cache-write count, so
`cache_creation_input_tokens` is never sent. In `message_delta`, a count the upstream never reported
is left out rather than sent as zero. An upstream that reports usage on every chunk sends running
totals, so each count takes the latest value reported for it.

> [!WARNING]
> **On the chat-completions route, the finish chunk is not the last chunk.** An upstream asked for
> `stream_options.include_usage` sends `finish_reason` on one chunk with no usage, then one more
> chunk with `"choices": []` and the request's usage, then `[DONE]`. So the translator closes the
> open content block at the finish, but holds `message_delta` and `message_stop` until the usage
> chunk arrives. When the stream ends without one (`[DONE]`, the body closing, or a read error after
> the finish), the relay calls the translator's `End`, which closes the message with whatever usage
> it has. An upstream that has already reported usage by the finish chunk gets both events there,
> with no wait. A chunk after the finish that does not decode also closes the message with the usage
> reported so far, because the answer is already whole; the translator returns a
> `wirebridge.UsageLostError` beside those closing events, and the daemon log names the upstream and
> the decode error, once per upstream.
>
> The bridge used to emit `message_stop` on the finish chunk, drop everything after it, never ask
> for usage, and keep only the output count. Every bridged turn reached Claude with zero input
> tokens. Do not move the close back onto the finish chunk, and do not skip `End`: without it every
> upstream that sends no usage chunk ends in the truncation error below.

**Asking for it.** A streamed chat-completions request carries `"stream_options":
{"include_usage": true}`, because an OpenAI-compatible upstream sends no usage in a stream unless it
is asked. A provider that declares the option `supports_usage_in_streaming` as `"false"` is not
asked. That is the same service fact pi's derive reads as `supportsUsageInStreaming`
([`packs/llamacpp/README.md`](../../packs/llamacpp/README.md#the-compat-facts--what-this-server-does-and-does-not-support)),
read by the same rule: only the JSON spelling `"false"` turns it off. The bridge reads it from the
selected profile's resolved options, which hold the provider's declared default with a user's value
over it. For such an upstream Claude's counts stay zero, and the daemon log says so once per upstream.
An upstream that rejects the field answers 400, which reaches Claude with the upstream's own message.
The fix is to declare the option `"false"` for that provider, in the user config:
`{"providers": {"<name>": {"options": {"supports_usage_in_streaming": "false"}}}}`. That value
reaches the provider's resolved profiles (checked 2026-09-25 against `kilo`).

**The shipped upstreams accept it.** The chat-completions providers the shipped packs route
through the bridge are `cerebras` (`https://api.cerebras.ai/v1`), `kilo`
(`https://api.kilo.ai/api/gateway`) and `bedrock`. None is gated. For the first two the reasons
were READ 2026-09-25, not measured against the live services:

- **Cerebras.** Its official Python SDK, which is generated from its API definition, declares
  `stream_options` with `include_usage` on chat completions
  (`src/cerebras/cloud/sdk/types/chat/completion_create_params.py` in `Cerebras/cerebras-cloud-sdk-python`).
  pi 0.87.1's built-in Cerebras models turn off `supportsStore` and `supportsDeveloperRole` but
  leave `supportsUsageInStreaming` at its default of true, so pi sends the field on every streamed
  Cerebras request. Cerebras's own API reference page does not list the parameter.
- **Kilo.** Kilo's own pi provider for this gateway (`src/models.ts` in `Kilo-Org/kilo-pi-provider`)
  sets `supportsStore: false` for its chat-completions models and leaves `supportsUsageInStreaming`
  at pi's default, so Kilo's own client sends the field to the same base URL. Kilo's API reference
  does not list the parameter, and documents usage as present "only in the final chunk".
- **Bedrock.** MEASURED on 2026-10-01: two streamed requests carrying `stream_options.include_usage`, to GPT-6.1 Sol and
  GPT-6 Astra on runtime's chat completions, were answered 200, and the bridge's `message_delta`
  for each carried 13 input and 5 output tokens
  ([`wire-bridge-gateway.md` §2.4](../design/wire-bridge-gateway.md#24-the-first-live-requests-measured-2026-10-01)).

**The Responses route needs no opt-in.** Its terminal event always carries the usage. That event is
`response.completed`, or `response.incomplete` when the output limit stopped the answer, which maps
to `max_tokens`. `message_delta` and `message_stop` go out there, mapped as above. `response.incomplete`
used to be refused as an unsupported event, which turned a partial answer into an error and lost its
usage. The stop stays `max_tokens` when the limit cut off a function call: an open function call
turns only a finished answer (`end_turn`) into `tool_use`, because the call's arguments may be
truncated JSON that Claude would otherwise try to run. The chat-completions route maps
`finish_reason` `"length"` to `max_tokens` whatever is open, and the non-streamed Responses answer
follows the same rule as the stream.

### The output cap

Every Anthropic Messages request carries `max_tokens`, which that API requires. The
chat-completions route sends it upstream as `max_completion_tokens`, OpenAI's current name for the
field. OpenAI's reference says of `max_tokens`: *"This value is now deprecated in favor of
`max_completion_tokens`, and is not compatible with o-series models"*
([create chat completion](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create)).
The route used to send `max_tokens`, and on 2026-10-01 GPT-6.1 Sol on Bedrock refused it with a
400, so every Claude Code and Copilot turn on that model failed. Since the change, Sol and GPT-6
Astra on Bedrock answered it, streamed and not
([`wire-bridge-gateway.md` §2.4](../design/wire-bridge-gateway.md#24-the-first-live-requests-measured-2026-10-01)).

**A provider that takes only `max_tokens` says so.** A provider that declares the option
`max_tokens_field` as `"max_tokens"` gets the cap under that field alone. That is the same
service fact pi's derive reads as `maxTokensField`
([`packs/llamacpp/README.md`](../../packs/llamacpp/README.md#the-compat-facts--what-this-server-does-and-does-not-support)),
read by the rule `supports_usage_in_streaming` is: only the exact spelling `"max_tokens"` moves the
cap off the default, and an absent or unrecognized value keeps `max_completion_tokens`. The bridge
reads it from the selected profile's resolved options, so a provider of your own declares it in
the user config: `{"providers": {"<name>": {"options": {"max_tokens_field": "max_tokens"}}}}`.
No shipped provider the route reaches declares it.

**The shipped upstreams accept `max_completion_tokens`** (READ 2026-10-01; Bedrock also MEASURED
that day):

| Upstream | What it takes | Source |
| :--- | :--- | :--- |
| Bedrock runtime's chat completions, for the OpenAI models on `bedrock`'s list | `max_completion_tokens`. GPT-6.1 Sol refuses `max_tokens` | AWS's [OpenAI models](https://docs.aws.amazon.com/bedrock/latest/userguide/model-parameters-openai.html) page writes the cap as `max_completion_tokens` in every example body that sets one, and maps that field to Converse's `maxTokens`. Its request guidance is written for the gpt-oss models, and its chat-completions examples set no cap. MEASURED for Sol and Astra |
| Cerebras (`cerebras`) | both. `max_tokens` is *"An alias for `max_completion_tokens`. Do not send both parameters in the same request."* | Cerebras's [chat completions reference](https://inference-docs.cerebras.ai/api-reference/chat-completions) |
| Kilo's gateway (`kilo`) | `max_completion_tokens`, which Kilo's own client sends. Kilo does not document whether the gateway applies it as the cap | Kilo's [API reference](https://kilo.ai/docs/gateway/api-reference) lists only `max_tokens`. Kilo's own pi provider (`Kilo-Org/kilo-pi-provider`) points its chat models at the same base URL (`src/api.ts`) and sets no `maxTokensField` in their compat (`src/models.ts`), so pi sends its default, `max_completion_tokens` (pi-ai's `openai-completions.js`, which picks `max_tokens` only for a list of other hosts) |

`llamacpp` and `zai` declare an Anthropic endpoint of their own, so the route does not reach them as
shipped. llama-server's request schema takes both fields, as aliases of `n_predict`
(`tools/server/server-schema.cpp` in `ggml-org/llama.cpp`). `packs/llamacpp` still declares
`max_tokens_field` `"max_tokens"`, pi's statement about that server, and the route honors it for
a user who removes the Anthropic endpoint.

## The via route — one route per agent under `/agent/<name>/`

A **via profile** *(coined here)* is a profile whose `via` names a service pack:
`"via": "wire-bridge"` ([`providers.md`](providers.md#routing-a-profile-through-the-bridge-via)).
Selecting one for an agent puts that agent on its own **via route** *(coined here)*: a
pass-through to the profile's provider, served by this daemon on the wire-bridge service's
declared `via_address`, under the path prefix `/agent/<agent>/`. The design and its rulings are
[`wire-bridge-gateway.md` §4.1](../design/wire-bridge-gateway.md#41-how-it-is-built).

- **The adapter routes do not move.** They keep the roots of their own ports, so claude's
  `ANTHROPIC_BASE_URL` and its route are byte-identical with or without any via profile. The via
  address is a third listener beside them, in the same daemon.
- **One prefix per agent.** The agent's derive writes `<via_address>/agent/<agent>` as its
  selected provider's base URL (the derive input `ctx.via_url`). Several agents with via profiles
  share the one port, each under its own prefix, each to its own provider.
- **An unknown or missing prefix is refused**, with a 404 OpenAI-shaped error that lists the
  served agents. Nothing is routed to a default.
- **Only a canonical path is forwarded.** A path after the prefix with a `.`, `..` or empty
  segment (one trailing `/` aside), or with an encoded `?` or `#`, gets a 404 and sends nothing
  upstream, so the path the route classifies is the path the provider receives. The forwarded
  path is re-escaped, never decoded twice
  ([WG-I23](../design/wire-bridge-gateway.md#WG-I23)).
- **Nothing is translated.** The method, the path after the prefix, the query and the body are
  forwarded unchanged to one of the provider's own base URLs. The `Content-Type` and `Accept`
  headers are copied, and a response streams back chunk by chunk, flushed as it arrives.
- **Two wires, and the path picks one.** A route carries the provider's chat-completions
  endpoint (its `openai` endpoint with `wire_api` unset or `openai-chat-completions`) and its
  Responses endpoint (its `openai-responses` endpoint, else its `openai` endpoint with `wire_api`
  unset or `openai-responses`). `/responses` and below go to the Responses endpoint,
  `/chat/completions` and below to the chat-completions one, and any other path to the
  chat-completions endpoint, or the Responses one when that is all there is. A path naming a wire
  the provider does not declare gets a 404 naming the provider, and is never translated or sent to
  the other endpoint. pi, oh-omp and opencode send chat-completions; codex sends Responses.
- **The credential is per upstream.** A route reads its provider's `api_key_env_name` from the
  same key channel as the adapter route ([WB-D4](#wb-d4)). A Bedrock upstream
  ([which upstream is Bedrock's](#which-upstream-is-bedrocks)) is signed with SigV4 instead,
  through the same chain as the adapter route, and a Bedrock provider that names no address is
  reached at runtime's own `/openai/v1` in the served agent's region. The agent's own
  `Authorization` header, which carries the launch's [caller token](#caller-authentication), is
  never forwarded.
- **A model off a narrowed list is refused.** When a `models` `only` narrowed the provider's list
  and the agent's profile keeps `enforce_models` on, a request naming another model gets a `400`
  `invalid_request_error` naming the model, the list and the switch, and nothing is sent upstream
  ([the model allowlist](#the-model-allowlist)).
- **An upstream with no credential idles alone.** It answers 503 with an OpenAI-shaped error
  naming the variable (or, for Bedrock, the credential sources) it needs, and the other routes
  still serve. The daemon log states each route's upstream and credential source, or why it idles.
- **Errors are OpenAI-shaped** (`{"error": {"message", "type", "code"}}`), because a via agent
  speaks OpenAI. An unreachable upstream is a 502, and one that sends no response headers within
  ten minutes a 504; an upstream's own error status and body pass through.
- **A stream the upstream cuts short fails at the agent.** Once the status line has gone out, an
  upstream read error is logged with the byte count and cause, and the agent's connection is
  aborted, so its read fails instead of ending like a finished reply. The agent closing its own
  request is not logged. Only the wait for headers is bounded: a stream runs as long as the
  upstream keeps sending ([WG-I24](../design/wire-bridge-gateway.md#WG-I24)).
- **What is skipped.** A provider offering neither wire is skipped, and so is `openai-codex`, the
  ChatGPT subscription, whose credential a via route does not carry; each skip is logged with its
  reason.

**Selecting a via profile brings the pack in.** An active via profile adds the pack its `via`
names, the way a live [`needs`](#needs--a-conditional-pack-dependency) entry does, and the launch
prints `+ wire-bridge (via of profile <name>, active for <agent>)`. So a pi-only jail gets the
daemon. As with `needs` ([WB-D9](#wb-d9)), a via may name only an embedded official pack, and that
pack must declare a service with a `via_address`; either failure refuses the launch, naming the
profile.

<a id="the-model-allowlist"></a>

### The model allowlist

On the bridge path the bridge is the one hard refusal of a model outside the provider's list
([`wire-bridge-gateway.md` §6](../design/wire-bridge-gateway.md#6-part-5--all-traffic-through-the-bridge-new-direction-design),
[OQ-WG3](../design/wire-bridge-gateway.md#OQ-WG3)). There is no second list: the allowlist is the
provider's composed list after a `models` `only`, the one every picker renders
([`model-lists-and-pickers.md`](../design/model-lists-and-pickers.md#7-how-a-company-pack-shapes-a-list-the-models-kind)).
A list no `only` narrowed refuses nothing. The switch is the profile's `enforce_models`, on unless
the profile says `false`.

| Route | Refuses when | The refusal |
| :--- | :--- | :--- |
| a via route | the provider's list is narrowed and the agent's own profile has the switch on | an OpenAI-shaped `400` |
| the adapter route | the list is narrowed and every agent reaching the provider there (claude, copilot) has the switch on and none is exempt, because the bridge cannot tell their requests apart | an Anthropic-shaped `400`, before the Messages pass-through or the translation |

Each refusal names the model, the provider, the list and the setting that turns it off, and a body
naming `model` twice is refused too, since the bridge reads one and the provider might read the
other. The key counts in any letter case (`Model`, `MODEL`), as the bridge's own JSON parser
counts it ([WG-I40](../design/wire-bridge-gateway.md#WG-I40), [WG-I43](../design/wire-bridge-gateway.md#WG-I43)).
A request that names no model, such as `GET /models`, is not the list's. On the adapter route an
id is compared as the translation sends it upstream, so a bare `deepseek-` id on the list and the
`deepseek/`-prefixed spelling claude uses on Kilo are one model.

**Exempt agents** ([WG-I41](../design/wire-bridge-gateway.md#WG-I41)). A program whose pack
declares `"unlisted_background_models": true` sends requests for models off the list that yolo does
not pin to it, so the bridge admits every model it sends and logs an off-list one. `packs/codex`
declares it (its review and memories models bypass the picker) and so does `packs/copilot` (which
ids its background requests carry is unmeasured). claude does not: yolo pins every one of its
tiers to the list. The daemon reads the declarations from the jail's staged pack tree, and when
it cannot read one it refuses no model and says so in its log
([WG-I42](../design/wire-bridge-gateway.md#WG-I42)). The serve line says, per route, whether a
model off the list is refused or logged.

## Caller authentication

Every request to every listener the bridge binds must carry the launch's **caller token**, or it
is refused `401` before any route sees it. The bridge forwards that token to no upstream.

**Caller token** is a term this doc coins. It means a random secret of 256 bits, which the
launcher mints for each launch that selects a pack service with a jail daemon, and for each host
or macos-user launch that starts a service's [host half](#at-the-host-notch). The bridge is the
only such service today. The launcher hands the secret to that daemon and to every agent a pack
derive points at the daemon's addresses, and the daemon demands it of every caller. It is carried
in `YOLO_SERVICE_<SERVICE>_TOKEN`, which for the bridge is `YOLO_SERVICE_WIRE_BRIDGE_TOKEN`.
A loophole's jail daemon gets one too when its manifest declares `jail_daemon.caller_token`, as
the OpenAI and AWS credential adapters do
([notch convergence §2.3](../plans/notch-convergence.md#23-the-fix-every-service-authenticates-its-caller-at-every-notch)).

### Why the jail is not the boundary

[WB-D4](#wb-d4) ruled inbound auth out because the jail was the trust boundary. That holds only
for a jail with its own loopback. Three setups break it:

- **A jail on `network.mode: host`** shares the host's loopback.
- **A nested jail** shares its parent jail's loopback, because a nested podman is forced onto
  `--net=host`. That is the host's loopback only when the parent jail is itself on host mode.
- **A `macos-user` launch** has no loopback of its own: its sandbox runs on the host. That backend
  declines a pack service's jail daemon, so it runs the bridge's [host half](#at-the-host-notch)
  instead, on a port the launch picked and behind this token. Until 2026-09-28 the launch pointed claude at
  the bridge's declared host ports, which nothing of yolo's ever bound and any local user could
  take first.

There the bridge's ports are reachable from every process on that loopback, other jails
included. Such a process could spend the user's provider keys and ChatGPT subscription through
an unauthenticated bridge. A process that took a port before the bridge bound it, or on
`macos-user` at any time, received whatever each client sent there. For claude,
that included its saved Claude login: with no `ANTHROPIC_AUTH_TOKEN` set, claude sends that
login's OAuth bearer to whatever `ANTHROPIC_BASE_URL` names
([`agent-auth-modes.md` §8.1](../design/agent-auth-modes.md#81-measured-2026-09-02-the-subscription-bearer-follows-anthropic_base_url)).
The Codex route set no `ANTHROPIC_AUTH_TOKEN`, so claude on the Codex profile sent it, and on
`macos-user`, where the bridge never binds, that was the default setup. Copilot
and claude on a bridged provider such as Cerebras sent the provider's own key.

The maintainer's ruling, 2026-09-27, verbatim:

> calling the jail the boundary here seems also just as bad for security because jails don't need
> to be bridge type, they can be house type and then um it's identical. So uh if you think this is
> an issue, we need to solve it in both places.

"Both places" are this jail-side bridge and the host notch's bridge, which carries the same
check ([the host half](#at-the-host-notch)). The ruling is [WB-D18](#wb-d18).

### How the token travels

- **Minted per launch, reused on attach.** A fresh launch mints a new token from `crypto/rand`.
  An attach reads the running jail's token back from the live channel file that jail's launch
  wrote, and delivers that one. The daemon read its token once at boot, so a new one would lock
  every new entry's clients out.
- **Only through the per-entry channel.** The token is a plain-form line in the `0600`
  `yolo-user-env.sh` channel section, which the daemon's boot hydrates and every jail process
  inherits. The credential gate also composes it into the derives that point an agent at the
  bridge. It is never on the container argv, where `podman inspect` would print it. It is never in
  a rendered config file, where each derive writes the variable's name instead. It is never in a
  log.
- **The address names its credential.** Core composes `api_key_env_name` onto every endpoint a
  pack service serves, beside its `base_url`, and the launch hydrates it into that endpoint's
  `api_key`. A via route's derive gets the variable as `ctx.via_api_key_env_name`.

| Client | Route | What it sends |
| :--- | :--- | :--- |
| claude | an adapter route: a bridged provider, or the Codex profile | `ANTHROPIC_AUTH_TOKEN`, which overrides claude's saved login, sent as `Authorization: Bearer` |
| copilot | an adapter route | `COPILOT_PROVIDER_API_KEY` |
| pi | a via route | `apiKey: "${YOLO_SERVICE_WIRE_BRIDGE_TOKEN}"`, as a bearer |
| oh-omp | a via route | `apiKey: YOLO_SERVICE_WIRE_BRIDGE_TOKEN`, a name OMP resolves from its environment, as a bearer |
| opencode | a via route | `apiKey: "{env:YOLO_SERVICE_WIRE_BRIDGE_TOKEN}"`, as a bearer |
| codex | a via route | `env_key = "YOLO_SERVICE_WIRE_BRIDGE_TOKEN"`, as a bearer |

The bridge accepts the token as `Authorization: Bearer <token>` or as `x-api-key: <token>`, and
compares it in constant time. A request with no token, or the wrong one, gets `401` with a body
in the shape that listener's clients read. That is Anthropic's `authentication_error` on the
adapter routes and OpenAI's error on the via address. The body says whether the token was missing
or wrong and names the variable, and it never echoes a presented value. A daemon handed no token,
or a malformed one, binds nothing and idles, naming the variable in its log. If the boot is
waiting on it, it reports not-ready instead. `boot.log` records that the bridge requires caller
auth, naming the variable and never the value.

### What it does not defend against

The token is not a secret from the jail's own processes, which are the bridge's intended callers.
Nor is it a secret from a host process running as the user who owns the jail. That process can
read `<workspace>/.yolo/home/yolo-user-env.sh`, but it can also read that user's credentials
directly. What the token closes is every other caller on a shared loopback: another user's
process, another jail, and a process that squats a port before the bridge binds it. A squatter now
receives a token that is good only against this launch's bridge.

## Lifecycle and failure behavior

- **Process.** One in-jail daemon under the existing supervisor, as a subcommand of the jail
  daemon binary rather than a new binary — so the "a new `cmd/` binary must be registered in two
  more places or it vanishes" trap never even opens. Restart on failure.
- **Startup order.** Daemons start at boot, before the agent command. The bridge **binds its port
  before writing its endpoint file** (a temp file renamed into place), so the file appearing means
  the listener exists. It announces the address it is about to bind before trying, so the log
  records how far it got even when the bind hangs.
- **The readiness wait — a bridge that cannot serve refuses the boot.** When the launcher's serve
  decision says the bridge will serve, it also names the bridge as a *ready-required* daemon. The
  entrypoint then hands the supervisor a one-shot readiness pipe, prints the daemon's log path, and
  blocks until the bridge answers `ready` or `failed <reason>`. Every way a serving boot can fall
  short answers `failed`: a bind error, a failed endpoint publish, a missing provider credential,
  a Codex route with no credential-service endpoint, or a daemon that idles on a boot the launcher
  registered, which is a contradiction between the two call sites of one decision. The entrypoint
  refuses the boot with `jail daemon "wire-bridge" cannot publish its required endpoint: <reason>`.
  The reachability witness, keyed on the endpoint variable, is the second line behind it. Either
  way the failure lands at boot, which is what the preflight philosophy wants: the alternative is
  an agent handed a base URL that dies at first request in a way nobody attributes. On a bind
  failure the reason carries the address, the syscall error and **what holds the port** — see
  [what can hold the listen port](#what-can-hold-the-listen-port-before-the-bridge-does).
- **An orphaned daemon refuses the boot; it is never killed.** When the entrypoint finds no live
  supervisor (a fresh container, or a re-entry whose supervisor died), it looks for processes whose full argv matches a
  configured daemon. If it finds any, it names each one with its PID and refuses, and it neither
  kills nor adopts ([OQ-PC3](#oq-pc3)).
- **The key channel.** On the chat-completions route the credential crosses as it does for every
  non-first-party agent: the launcher writes a `0600` environment file from hydrated env sources,
  and the bridge reads that file at startup, once, into memory. Since the credential gate
  ([`providers.md`](providers.md#the-credential-gate)) a provider's key sits in the env file of
  the agent that selected it, so a route reads the file of the agent it is served for
  (`~/.config/yolo-agent-env/<agent>.sh`), then the shared `yolo-user-env.sh`. One writer, one
  reader; the daemon never appears in a process listing with the key. Its own process environment is the fallback
  for notches where the file may not exist, and a file that exists but cannot be read is reported
  rather than silently skipped. The Codex route reads no key at all: it asks the OpenAI credential
  service for an access-only token view per request, and a 401 gets exactly one fresh view before
  the refusal is relayed. So the route needs the machine's OpenAI login to exist before claude's
  first request, and claude's launcher ensures it: claude's env derive emits
  `YOLO_AUTH_PRELAUNCH_CLAUDE_LOGIN=1` whenever claude's selected provider is `openai-codex` and
  its anthropic address resolves, keyed on the provider rather than on a profile named `codex`
  ([`OQ-BR8`](../design/providers-and-profiles-redesign.md#OQ-BR8)), and the launcher then asks the
  credential service for that token view before claude starts, writing no file, and starts the
  OpenAI login at a terminal when there is none, as the codex and pi launchers do
  ([ES-D28](../design/credential-sources-separation.md#10-decision-ledger)).
- **Shutdown.** The daemon stops on `SIGINT`/`SIGTERM` (the supervisor's stop signal) and, when it
  was serving, removes its endpoint file, because a file that outlives its listener points the
  next reader at a closed port. A healthy idle waits on the same signal.
- **Upstream failures.** A 4xx is relayed with the **same status**, anthropic-shaped, so the agent
  renders it and owns the retry decision. Every 5xx, timeout, or unreachable dial becomes a **502**
  in the same shape — the family an agent backs off on — logged as one line of status and latency,
  never a body. The upstream's own error *message* is forwarded (it goes to the agent, which
  displays it) but never logged, and a body that does not parse is replaced by a status line
  rather than quoted: an HTML error page in a JSON field helps nobody. **The bridge adds no
  retries of its own** — the agent's retry loop is the retry policy — apart from the Codex route's
  single retry on a 401 with a fresh token view. A stream that fails mid-flight, or ends without a
  finish reason, is closed with an anthropic `error` event, the only legal way to fail inside
  SSE, and the cause goes to the log. A response the client never received is logged as a failed
  delivery rather than as the status the bridge intended.
- **Concurrency.** A stateless proxy; concurrent requests are fine and there is no shared mutable
  state after boot. Ordering cannot matter, because every request is independent.

**Forbidden, and each for its own reason:** never dial anything but the boot-selected upstream
(read once from the composed table, never from request content); never listen off loopback; never
log request or response bodies, or the key; never cache bodies to disk.

## What can hold the listen port before the bridge does

The bridge's listen port is on the jail's own loopback, in a network namespace no other jail
shares. So the process that can take the port first is one this same boot started. A report of
`cannot bind 127.0.0.1:<port>: … address already in use` is therefore a question about **this
container's own boot**, and this section is the map for answering it.

⚠ **Not on a jail that shares its launcher's network namespace** (`network.mode: "host"`, or a jail
started inside another). There the port is one the launch picked on the shared loopback, and every
process on it can bind it. The launch holds the port from the pick, and its keeper lets it go just
before it starts the container
([NC-D69](../plans/notch-convergence.md#NC-D69), [NC-D70](../plans/notch-convergence.md#NC-D70)).
So none of that launch's own listeners can take it, but any other process that binds it during
the container's start or boot, before the bridge does, can, another jail's launch included.

### Forwarders start before daemons, so a forward always wins

The entrypoint starts the in-jail port forwarders (`startContainerPortForwarding`) before the
`start_jail_daemon_supervisor` step. Each forward is a `socat` listening on the jail's
`127.0.0.1:<port>` with `reuseaddr`, and `reuseaddr` does not help a later binder. So when the
launch's forward list carries a port that is also an adapter's address, the forwarder takes it
unconditionally and the bridge, binding second, is the one that reports.

The ordering is not a defect and does not change. Nothing legitimate ever puts an adapter's
address in the forward list, so the protection is keeping that list clean, not re-sequencing the
boot.

### Only a user's provider URL becomes an implicit forward

The forward list has exactly two sources: the user's `network.forward_host_ports`, and **implicit
localhost-provider forwards**. An implicit forward is the port of a provider URL whose host is a
loopback spelling. A user who writes `http://localhost:11434` in `providers` is naming an inference
server on the machine that launches yolo, so yolo forwards that host port into the jail at the same
number and leaves the URL unchanged.

**Pack endpoints never become forwards.** A pack's endpoint is a service fact, and wire-bridge
legitimately names jail loopback. So the implicit forwards are read from the **user's own
`providers` config map**, never from the composed table: the composed table carries the adapter
addresses, and reading it would turn every bridged provider into a forward of the bridge's own
port.

The rest of the rule:

- **An explicit forward wins.** `mergeHostForwards` keeps a user's `forward_host_ports` entry for
  a local port and drops the implicit one, because an explicit `8080:9090` is a deliberate remap.
- **Bridge network mode only.** Implicit forwards are merged only when the applied network mode is
  bridge. Under host networking the jail already shares the launcher's loopback.
- **Disclosed, by port and provider** ([OQ-PC2](#oq-pc2)). The launch prints one line per implicit
  port, naming the provider that asked for it. A port the user also wrote in `forward_host_ports`
  is not named, because their config already shows it. The line is printed once, from the launch
  pipeline, before the merge: the container-argv assembly performs the same merge silently, so
  the disclosure is not printed twice. The briefing's **Forwarded Host Ports** section is fed
  from the merged list. `briefingPortsFor` does the merge itself, so a caller cannot forget it.
  The disclosure is a launch line and a briefing section. It is **not** a row in the pack
  banner, so a reader who sees only the banner learns nothing about a forwarded port.

> [!NOTE]
> **Both halves of that disclosure have call-site tests.** The launch line is pinned by
> `TestRunContainerDisclosesImplicitProviderForwardsBeforeTheMerge`
> (`internal/cli/run/providerforwarddisclosure_test.go`). The call sits after the image load,
> where no unit test can drive a launch, so the test reads `runContainer`'s syntax tree, as
> `currentimagecallsite_test.go` does. It fails if the call is deleted, moved out of the
> bridge-mode block, moved below `mergeHostForwards` or below the host forwarders' start,
> handed any list but the pre-merge `forwardHostPorts` and the channel's
> `localProviderForwardSources`, given a printer that writes nowhere, or called from a second
> function. The briefing half is pinned by rendering the real briefing
> (`TestRenderedBriefingNamesAnImplicitProviderForward`).

### The invariant that keeps the adapter's address out of the user's map

The adapter pass writes each adapter's address into every eligible provider entry as
`endpoints.anthropic.base_url`, and it runs last over the finished composed table. That write is
safe only because **the composed provider table owns its output**. `packload.ComposeProviders`
deep-copies the user layer on the way in, and `shippedProviderEntry` allocates every level it
emits, so no object reachable from the table belongs to the caller or to a pack declaration. The
adapter pass is therefore a writer of the table alone.

The failure this prevents is exactly the collision above. Take a user-scope `providers.<name>`
that no selected pack ships, carrying `endpoints.openai` or `endpoints.openai-responses` and no
`anthropic` endpoint. That is the shipped shape for a local inference server. If the table stores
that entry by reference, the adapter pass writes the bridge's own loopback address into the
user's own map. The implicit-forward read then finds a loopback URL and forwards the bridge's
port, and the forwarder takes it before the bridge starts. Grepping `~/.config/yolo-jail/` for the
port returns nothing in that state, because the number is composed in at launch.

> [!WARNING]
> **The copy has to be DEEP, and a one-level clone looks sufficient and is not.** A provider
> entry is a tree. Clone only the entry and `endpoints` stays shared, so the adapter pass writes
> through it unchanged and the symptom is identical. `jsonx.DeepCopy` is the copier: unlike
> `jsonx.Plain` it lowers nothing, so key order and integer literals survive and a copied config
> re-encodes byte-identically. One copy at the composer's read covers every sink, including
> `mergeUnder` writing sub-values of the user's entry into a pack-shipped one. Reverting it
> turns `TestComposeProvidersDoesNotWriteThroughToTheUsersMap` and
> `TestTheComposedTableSharesNoObjectWithTheUsersMap` (`internal/packload`) and
> `TestComposingProvidersAddsNoImplicitHostForward` (`internal/cli/run`) red, the last one
> reporting the leaked port by number.

### The other holders, and what each says

- **A forward that could not start.** When a forward's port is already bound, the forwarder skips
  that port and says what holds it. It reads `/proc` through `internal/listeners`, once for the
  whole loop and only if some port is held. The skip is reported in two registers. A holder whose argv is this boot
  path's own `socat` for that port is an already-established forward, on a re-entered container
  working as designed. Anything else is a warning that the forward will not exist.
- **The bridge's own bind failure** carries the address, the syscall error and the port holder
  (`describePortHolder`, over the same `internal/listeners` snapshot). The holder lookup is
  tri-state: it names the holder, or says that no LISTEN socket on that port is in this
  namespace's tables, or says it could not look and why. An unreadable table is never reported
  as an absence.
- **An orphaned daemon.** A daemon whose supervisor died reparents to PID 1 and keeps its listener.
  The entrypoint runs again on every attach to the same container, and a fresh supervisor would
  otherwise spawn a rival that prints this same bind error. After both the current and the legacy
  supervisor PID files prove dead, the entrypoint walks `/proc` for processes whose **full argv**
  matches a configured daemon: same length, same executable base name, every later element equal.
  A process that merely holds the port is never a candidate. If it finds one, it names each orphan
  and its PID and refuses the boot ([OQ-PC3](#oq-pc3)).
- **Another jail.** This is impossible in bridge mode, where `/proc` and the loopback belong to
  the container: yolo passes no `--pid` flag. A nested jail is the exception. It is forced onto
  `--net=host`, so two nested jails share one loopback and a genuine cross-jail collision can
  happen there.

> [!NOTE]
> **Local packs are outside this map.** The only adapters yolo ships are `packs/wire-bridge`'s two.
> A local pack may declare its own `adapter`, a `service` with a `jail_daemon`, or a provider.
> A second declaration of an adapter's address would be a different bug with the same message.

### Where the post-mortem lives

A refused boot tears the container down (`--rm`), and with it the process table and the
listeners. These are the facts that survive:

- **The bridge's own log**, `~/.local/state/yolo-jail-daemons/wire-bridge.log` in the jail. The
  supervisor points the daemon's stdout and stderr at it, and the readiness wait prints its path
  as `Daemon diagnostics:`. It sits in the jail home, so it outlives the container in the
  workspace's home overlay under `<workspace>/.yolo/home`. Nothing the daemon logs is gated: no
  verbosity dial exists, by the same rule that gives a launch no quiet mode
  ([`OQ-RO3`](report-tiers.md#why-its-this-way)).
- **The host socat log**, `<cname>-socat.log` under `~/.local/share/yolo-jail/logs/`. The launcher
  creates it only when the launch has at least one forward, so its existence alone is evidence of
  a forward on a config that declares none. The jail-side forwarders write their own
  `~/.yolo-socat.log`.
- **Not host `ss`.** The host half of a forward is `socat UNIX-LISTEN:…`, a filesystem socket, so
  `ss -ltnp` on the host is empty while the jail-side socat holds the TCP port. An empty host
  table clears nothing.
- **`YOLO_HOLD_ON_REFUSAL=1`** makes a boot that is about to refuse print how to get in and then
  block, so the failed container can be `podman exec`'d into instead of reconstructed.

> [!NOTE]
> **Two misleading lines accompany this failure.** The reachability reporter that follows a
> refused bridge calls it a "host service". Its lead sentence, *"yolo requested host-loopback
> forwarding for this jail"*, quotes the launch's `YOLO_HOST_LOOPBACK` disposition. That
> disposition is per launch and names no service. Neither line means the bridge was given a
> forward.

## <a id="at-the-host-notch"></a>The host half — a bridge for one launch

The host and `macos-user` run no pack service's jail daemon, so there the bridge runs as its service's **host
half**: `packs/wire-bridge` declares `host_daemon` with the argv `yolo internal daemon
wire-bridge`, and the launch runs it as a **launch-owned service** (a term
[`host-notch-services.md`](../design/host-notch-services.md#12-terms) coins: a child of one
launch, for the one agent it runs, stopped when that agent exits). The mechanism is
`internal/launchservice`, which `yolo host --` and the macos-user arm share
([OQ-NC1](../plans/notch-convergence.md#OQ-NC1) ruled A, [OQ-HS4](../design/host-notch-services.md#OQ-HS4)).

- **The trigger is the gate's own refusal.** The launch composes with nothing served first. A
  pairing only the bridge's adaptation resolves refuses at the protocol gate as
  `UnservedAdapterError`, and a launch that owns its command then admits the service, plans it
  and composes again with it served. So `yolo host -p codex -- claude` and `-p cerebras --
  claude` start it, and copilot on cerebras, which speaks the provider's own wire, starts
  nothing. On macos-user every profiled agent's pairing counts, since that arm writes every
  profiled agent's env file.
- **Only a pack yolo ships.** `launchservice.Admit` runs a host half only for a pack the
  embedded set supplied (`packload.Pack.Official`) and only when its argv names `yolo`. A
  fetched or local pack's host half is refused by name, and the refusal names the container jail
  where the profile works.
- **The address is the launch's.** Each of the bridge's adapter addresses moves to a loopback
  port the launch picked, and a user's `adapters` override of those conversions does not apply
  ([WB-D13](#wb-d13) at a jail only). The launch picks the port by binding port 0, holds it, and
  hands the bridge the bound socket when it starts it, so no other listener, the launch's own
  host-service fronts included, can be given the port in between
  ([HS-D26](../design/host-notch-services.md#HS-D26)).
- **The inputs come from the launch, never from a jail path.** A 0600 file in a 0700 directory
  of its own, named by `YOLO_HOST_SERVICE_INPUT` and removed by the bridge once read, carries the
  three wire tables, the caller token, the host broker's private socket, and the `env_sources`
  the credential gate delivers to the agent for its provider. Nothing is on the argv. The Codex
  route takes its access-token view from the host socket (`openauthclient.RequestAccessTokenUnix`),
  a key comes from that input or the bridge's own environment and never a key file, and no
  endpoint file is published: the launch reads the readiness line instead. A via stays inert
  outside a jail ([WG-I12](../design/wire-bridge-gateway.md#WG-I12)), so the host half serves
  the adapter route alone, and a plan with nothing to serve fails its readiness and exits.
- **Its life is the launch's.** The launch starts it after the agent resolves on `PATH` and
  after the OpenAI prelaunch, waits at most 5 seconds for `ready wire-bridge` on the readiness
  pipe, and refuses otherwise, naming the service, its argv and its log. It then runs the agent
  as its own child: SIGTERM and SIGHUP are forwarded to the agent, and a terminal's SIGINT
  reaches the agent alone, since the bridge runs in its own process group. When the agent exits,
  by any route, the bridge gets SIGTERM and, 2 seconds later, SIGKILL. If the launch dies without
  cleanup, the bridge sees its lifeline pipe close and exits at once. A bridge that dies
  mid-session is named on stderr once and not restarted.
- **It is said.** Every start prints one line naming the service, its pack, its pid, the
  address its agent was pointed at and its log. That address is the one route the bridge opens,
  though the plan picked a port for each of its adapters
  ([HS-D24](../design/host-notch-services.md#HS-D24)). `yolo host env` refuses a bridged profile, naming the `yolo host --`
  spelling, and `yolo host apply` writes no bridged address and says a bridged selection in the
  `profile` key takes effect only through `yolo host --` or the wrappers.

```console
$ yolo host -p codex -- claude
yolo host: + bedrock (needed by claude)
yolo host: + openai-auth (needed by claude)
yolo host: + wire-bridge (needed by claude)
yolo host: + aws-auth (needed by bedrock)
yolo host: Profile codex: declared by claude; claude → provider "openai-codex", on its "anthropic" endpoint
yolo host: Not set at this notch, because nothing here serves it (docs/plans/notch-convergence.md §2.4):
  profile "bedrock-bridge"'s via — its service does not run here, so its agents keep their own clients rather than routing through it
yolo host: started the "wire-bridge" service (pack "wire-bridge", pid 96868) for claude on 127.0.0.1:36501; it answers only this launch's caller token and stops when claude exits. Its log: ~/.local/share/yolo-jail/logs/launch-service-wire-bridge.log
```

That is the stderr of `TestHostCodexClaudeRunsThroughALaunchOwnedBridge`'s launch, with the log
path shortened; the pid and port are the kernel's, the cause lines are [WB-D12](#wb-d12)'s, and a
machine never logged in to OpenAI prints the prelaunch's login lines before the service line. <a id="no-macos-user-bridge"></a>On **macos-user** the same host half runs
outside Seatbelt, started after the launch's host services and stopped when the sandboxed command
returns. The jail-daemon decline names the bridge's jail daemon with "(its host half runs for this
launch instead)". A dry run says what it would start. The host socket and the sandbox's reach to
the picked port have not run on a Mac: the macos-user arm is pinned by unit tests only.

## What this does not license

- **No second protocol family.** A bridge for a different wire (and therefore a different agent)
  is a different document with its own cost case.
- **No gateway features** — routing, failover, budgets, multi-provider fan-out, or model-name
  remapping beyond passthrough.
- **No long-lived host bridge.** A host bridge lives only for the launch that starts it
  ([OQ-HS3](../design/host-notch-services.md#OQ-HS3), ruled 2026-09-28), so nothing at the host
  listens between launches and no file names its address. An agent started without yolo runs
  without the bridge, which the ruling accepts.
- **No unauthenticated caller.** It stopped being true that the jail is the trust boundary, so the
  bridge grew auth before it grew anything else: every request carries the launch's caller token
  or is refused ([caller authentication](#caller-authentication), [WB-D18](#wb-d18)).
- **No provider-side knob for the port.** The address is the *adapter's* own declaration, with
  exactly one user-scope override (`adapters.<from>-><to>.address`) and nothing else: a provider
  cannot move it, and a workspace config cannot set it at all. The override moves the bind and the
  agent's URL together, on both routes — see [the listen address](#the-listen-address).

## Open questions

### <a id="oq-wb1"></a>✅ [`OQ-WB1`](#oq-wb1) — what does the Codex route do with `response.failed`? — **RULED (b), BUILT 2026-09-25**

<!-- vantage: oq id=OQ-WB1 -->

Opened 2026-09-25. *WB* stands for "wire bridge"; the prefix is new with this question. **What
follows is the defect as it stood before the ruling was built**; the answer below says what the bridge
does now. The Responses
stream translator (`ResponsesStreamTranslator.Chunk`, `internal/wirebridge/responses.go`) handles
`response.completed` and `response.incomplete` as terminal events and passes a fixed list of lifecycle
markers. Every other event type reaches its default arm, which returns
`unsupported Responses stream event`. That includes `response.failed`, the upstream's own way of saying
the response failed, and a top-level `error` event, since the switch has no case for either. The daemon
then closes the stream with an anthropic `error` event of type `api_error`, whose message is
`wire-bridge: upstream stream did not translate: …` (`failStream`, `internal/wirebridged/handler.go`). So
the agent is told the bridge could not translate a known event, and the upstream's own reason is lost:
the struct the translator decodes has no field for the failed response's error.

- **(a) Leave the refusal.** The stream still fails closed, in the spirit of [WB-D5](#wb-d5). Cost: the
  message is wrong about the cause, and the upstream's reason reaches neither the agent nor the log.
- **(b) Handle `response.failed` as a terminal failure.** Close any open block and end the stream with
  an anthropic `error` event carrying the upstream's error message, still typed `api_error`. The same
  arm takes a top-level `error` event. The log names the upstream's error code. This matches how the
  daemon already treats a 4xx before the stream starts: the upstream's message goes to the agent and is
  never logged.
- **(c) As (b), and also map the upstream's error code to an anthropic error type** (a rate limit to
  `rate_limit_error`, for example). Cost: the type changes how the agent retries, so the mapping needs
  the set of codes the ChatGPT backend actually sends, which nothing here has measured.


_Leaning:_ **(b).** It replaces a misleading message with the upstream's own, and it changes no retry
behavior, because the event type stays `api_error`. Mapping codes waits until the codes are measured.

**Answer:**
> **(b)**, ruled in review 2026-09-25: handle `response.failed` and a top-level `error` event as a
> terminal failure that forwards the upstream's message as `api_error`. It fixes the misleading
> "unsupported" message and loses nothing; mapping codes to anthropic error types waits until the
> codes are measured.

**Built 2026-09-25.** `ResponsesStreamTranslator.Chunk` (`internal/wirebridge/responses.go`) treats
`response.failed` and a top-level `error` event, flat or with a nested `error` object, as terminal: it
closes any open block and returns an `*UpstreamFailedError` with the upstream's code and message.
`relayStream` (`internal/wirebridged/handler.go`) writes the close, logs the event and the code, never the
message, and ends the stream with an `api_error` event carrying `UpstreamFailedError.ClientMessage`. That
is the upstream's message verbatim, or a sentence naming the code when the upstream sent none. Pinned by
`TestResponsesStreamFailedIsATerminalUpstreamError`, `TestResponsesStreamTopLevelErrorIsATerminalUpstreamError`,
`TestResponsesStreamFailedWithoutAMessageStillSaysWhat` and `TestResponsesStreamFailedForwardsTheUpstreamsMessage`.

## Why it's this way

Rulings a future change would otherwise undo, with their original IDs.

| Ruling | Why it holds |
| :--- | :--- |
| <a id="wb-d1"></a>[**WB-D1**](#wb-d1) — one protocol pair AT A TIME | A second pair is a second cost case, and bundling them makes the first one unreviewable. ⚠ **This cell read “exactly one protocol pair” until 2026-09-18, and the doc has contradicted it since the Codex route landed** — its own second paragraph says *“Two routes exist”*, and [`packs/wire-bridge/pack.json`](../../packs/wire-bridge/pack.json) declares two `adapter` contributions. Re-read as a rule about how a pair is ADDED rather than how many may exist, which is the reason the cell gives and which the second pair honoured by arriving on its own review. If that reading is wrong the ruling needs restating rather than re-wording — the contradiction is recorded here rather than resolved silently. |
| <a id="wb-d2"></a>[**WB-D2**](#wb-d2) — the URL reaches the agent as the provider's `endpoints.anthropic`, one writer owns it, and the agent's derive is untouched | The ruling holds; **the writer moved on 2026-09-18.** It used to be each consuming provider's manifest, which made the adapter's port a fact stated by every one of its N consumers; it is the adapter's own `address` now, composed into the provider entry by core ([`protocol-resolution.md`](protocol-resolution.md)). What the ruling protects is untouched: one writer for the address, and a derive that could see the bridge would make composition responsible for a fact selection already decides. |
| <a id="wb-d3"></a>[**WB-D3**](#wb-d3) — the dependency is real manifest vocabulary, auto-included at selection and printed | The rejected shape expressed dependency as an *error message the user must act on* rather than a declaration the launcher acts on. A mechanism the manifest cannot state is the wrong mechanism. |
| <a id="wb-d4"></a>[**WB-D4**](#wb-d4) — inbound auth: the launch's caller token, never forwarded (amended 2026-09-28, [WB-D18](#wb-d18); it was "none"); outbound: the `0600` env file read once at boot — a bearer key, or for a Bedrock upstream the SigV4 credential chain those variables name (amended 2026-09-25, [Part 1](../design/wire-bridge-gateway.md#2-part-1--the-bridge-signs-its-own-upstream-requests-ruled)) | The inbound half's premise, that the jail is the boundary, fell to [WB-D18](#wb-d18). The outbound half holds: the file stays the one place the bridge learns an upstream credential, no inbound credential is ever one, and the signer only resolves, per request, what those variables point at, so an SSO session refreshes inside a running jail. |
| <a id="wb-d5"></a>[**WB-D5**](#wb-d5) — upstream reasoning is dropped, and an unknown block type fails **closed** with a named 400 | A silently-mistranslated request is the failure mode that cannot be debugged; naming the block keeps drift visible at first request. |
| <a id="wb-d6"></a>[**WB-D6**](#wb-d6) — strict mode is never sent upstream | Agent tool schemas contain what strict mode rejects. |
| <a id="wb-d7"></a>[**WB-D7**](#wb-d7) — build in-repo, stdlib only | The hermetic build stays hermetic, and the surveyed off-the-shelf options were a framework where a shim was needed, or dormant, or solving a different problem. |
| <a id="wb-d8"></a>[**WB-D8**](#wb-d8) — a bridged provider declares its context window | Auto-compaction has to trigger at the *real* window rather than the agent's default; the conservative (free-tier) figure is the declaration, and a paid user overrides it in their own profile. |
| <a id="wb-d9"></a>[**WB-D9**](#wb-d9) — `needs` may name only embedded official packs | Selection must not become a supply-chain channel for unreviewed code. |
| <a id="wb-d10"></a>[**WB-D10**](#wb-d10) — needs resolve as a transitive closure before staging; cycles refuse the launch naming the loop; explicit user selection is joined, never overridden | The mount is the filter, so a pack the closure adds after staging renders nothing. The join rule alone would terminate the walk, so the cycle is checked **structurally** rather than left to termination to imply: manifests that need each other are an authoring bug the user is owed by name. |
| <a id="wb-d11"></a>[**WB-D11**](#wb-d11) — user config carries no `needs` key; a dead loopback URL is a `yolo check` warning | The user's own config is the user's own dead URL, and refusing it would make a diagnosis into an error. |
| <a id="wb-d12"></a>[**WB-D12**](#wb-d12) — every auto-inclusion prints, at launch and in `yolo check` | A silent join is the one forbidden behavior of the closure. |
| <a id="wb-d13"></a>[**WB-D13**](#wb-d13) — the listen port is manifest-borne | It lives in `packs/wire-bridge`'s own `adapter` contributions, not in each consumer's provider entry. It is not *fixed*: a user-scope `adapters.<from>-><to>.address` replaces it, because on `macos-user` there is no network namespace and an adapter's ports are host ports. One writer and a boot-fatal collision are what the ruling buys. The override reaches both routes, and [the listen address](#the-listen-address) says what breaks if a route binds before reading the entry. |
| <a id="wb-d14"></a>[**WB-D14**](#wb-d14) — `count_tokens` refuses (404) | A zero-stub is measurably worse than a refusal: it poisons the number it answers, where a 404 falls back to the agent's own estimator. |
| <a id="wb-d15"></a>[**WB-D15**](#wb-d15) — thinking has a route-specific disposition: chat-completions omits it; Responses maps explicit `enabled` budgets conservatively and lets every non-budget mode use the provider default | Responses exposes documented effort values but no adaptive value. An explicit budget is enough intent to map; every other mode means the provider, not the bridge, chooses. |
| <a id="wb-d16"></a>[**WB-D16**](#wb-d16) — `kind: "service"` is primary vocabulary; loopholes re-form as service + boundary grants | A daemon with no grants is not a loophole, and naming it one would make the trust model unreadable. The re-forming is a follow-up, not a prerequisite. |
| <a id="oq-pc1"></a>[**OQ-PC1**](#oq-pc1) — fix the mutation, not the read: `ComposeProviders` deep-copies the user layer, and the implicit-forward read stays on the user's config map | A composer that writes through its input is a one-writer violation, and the implicit-forward read was only the first consumer caught by it. Reordering that one read would leave every later reader of the config looking at an edited map. See [the invariant](#the-invariant-that-keeps-the-adapters-address-out-of-the-users-map). |
| <a id="oq-pc2"></a>[**OQ-PC2**](#oq-pc2) — an implicit provider forward is disclosed: one launch line per port naming the provider, and the briefing's Forwarded Host Ports section fed from the merged list | A forward is a hole into the host, and the user's own config cannot be grepped for a port they never wrote. The launch has no quiet mode ([`OQ-RO3`](report-tiers.md#why-its-this-way)), so the line is permanent, and that is right: it reports something yolo **did** (it bound a port in the jail and opened a socket on the host), not an absence. Do not gate it, and do not move it after the merge, where the declared and implicit ports can no longer be told apart. |
| <a id="oq-pc3"></a>[**OQ-PC3**](#oq-pc3) — the orphan check keeps its detection and refuses, naming each orphan's PID; it never kills and never adopts | `SIGKILL` on an argv match acts irreversibly on an *inference* about ownership. A straight revert would lose the only guard against an in-container fault that prints the bridge's bind error. Adoption is rejected because an orphan's supervisor is gone, so the orphan holds no readiness pipe. Adopting it would treat a process as serving its endpoint on the strength of its argv, which is the same inference. |
| <a id="wb-d17"></a>[**WB-D17**](#wb-d17) — more than one agent bin is a bridge consumer, and the serve predicate walks every active profile | Found while building: a derive that *prefers* an anthropic endpoint when a provider declares one makes that agent a consumer too, and a single-bin condition would have shipped those launches a dead URL with no bridge included. |
| <a id="wb-d19"></a>[**WB-D19**](#wb-d19) — at the host and on macos-user the bridge runs as its service's host half, a launch-owned child for one launch's agent, on ports that launch picked, fed by a 0600 input file, stopped with the agent; only an official pack's host half runs (2026-09-28) | The maintainer ruled every notch runs the selected packs' services ([OQ-NC1](../plans/notch-convergence.md#OQ-NC1), A) and that a host service lives per launch ([OQ-HS3](../design/host-notch-services.md#OQ-HS3)). The mechanism and its decisions are [`host-notch-services.md`](../design/host-notch-services.md)'s HS-D rows; see [the host half](#at-the-host-notch). |
| <a id="wb-d18"></a>[**WB-D18**](#wb-d18) — every request carries the launch's caller token or is refused `401`; the token is minted per launch, reused by an attach, delivered only through the per-entry channel, and never forwarded upstream (2026-09-28) | The maintainer, 2026-09-27: *"calling the jail the boundary here seems also just as bad for security because jails don't need to be bridge type, they can be house type and then um it's identical. So uh if you think this is an issue, we need to solve it in both places."* A jail on the host's loopback shares the bridge's ports with every host process, and a client sends its real credential to whatever holds the port. For claude, the Claude login went too ([§8.1](../design/agent-auth-modes.md#81-measured-2026-09-02-the-subscription-bearer-follows-anthropic_base_url)). The token is the one credential a bridged client may send there, so the address names it, and the host notch's bridge will reuse it. See [caller authentication](#caller-authentication). |

## Current values

Verified at `d4e435a3`. The prose above explains what each of these is for; this table is the
only place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Service name (supervisor entry, endpoint stem, manifest `endpoint`) | `wire-bridge` | `wirebridged.ServiceName` |
| Endpoint file | `wire-bridge.endpoint` under the jail services dir; it names the first listener bound — the adapter route's when one serves, else the via address | `wirebridged.EndpointFile`, `wirebridged.servePlan` |
| Listen address, `openai → anthropic` | `http://127.0.0.1:8214` — the adapter's declared `address`, composed into each eligible provider's `endpoints.anthropic.base_url` and parsed back out by the daemon. On a jail sharing its launcher's network namespace (`network.mode: "host"`, or nested) the launch composes a port it picked instead, and the daemon parses that one back out ([NC-D41](../plans/notch-convergence.md#NC-D41)). A user-scope `adapters.openai->anthropic.address` replaces it | `packs/wire-bridge/pack.json`, read by `wirebridged.routeFor` |
| Listen address, `openai-responses → anthropic` (the Codex route) | `http://127.0.0.1:8215` — the adapter's declared `address`, composed into `openai-codex`'s `endpoints.anthropic.base_url` and parsed back out by the daemon, exactly as the row above, a picked port included; `wirebridged.CodexResponsesListenAddr` is the DEFAULT when the entry names no anthropic endpoint, not a bypass of it | `packs/wire-bridge/pack.json`, read by `wirebridged.routeFor`; default in `wirebridged.CodexResponsesListenAddr` |
| Listen address, via routes | `http://127.0.0.1:8216` — the service's declared `via_address`, or a port the launch picked on a jail sharing its launcher's network namespace, as above; every via agent's base URL is the served address plus `/agent/<agent>` | `packs/wire-bridge/pack.json`, read by `packload.ViaServiceAddress`; served by `wirebridged.viaRoutesFor` |
| Address override key | `adapters.<from>-><to>.address`, **user scope only** | `internal/config/adapters.go`, `yolo config-ref` |
| Caller token | `YOLO_SERVICE_WIRE_BRIDGE_TOKEN`: 64 lowercase hex characters, 256 bits from `crypto/rand`, one per launch; accepted as `Authorization: Bearer` or `x-api-key`; anything else is `401` | `paths.ServiceCallerTokenEnv`, `run.launchCallerTokens`, `svcendpoint.NewToken`; checked in `wirebridged/auth.go` |
| Restart policy | on failure | `packs/wire-bridge/pack.json` |
| Served path | adapter routes: `POST /v1/messages` and nothing else; via routes: any canonical path under `/agent/<agent>/` (no `.`, `..` or empty segment, no encoded `?` or `#`) | `internal/wirebridged/handler.go`; `wirebridged.viaMux`, `wirebridged.canonicalViaTail` |
| Upstream path | the provider's `openai` base URL plus `/chat/completions`; on the Codex route, the composed `openai-codex` entry's `openai-responses` base URL (the subscription's, as shipped) plus `/responses`; on a via route, the provider's chat-completions or Responses base URL (the one the path names) plus the path after the prefix | `wirebridged.NewHandler`, `wirebridged.CodexResponsesBaseURL`, `wirebridged.viaUpstreams`, `wirebridged.passthroughHandler` |
| Via request body limit | 64 MiB; larger is a 413 | `wirebridged.maxViaBody` |
| Upstream timeout | 10 minutes, the one timeout the daemon adds. The adapter routes bound the whole exchange; a via route bounds only the wait for response headers, and a timeout there is a 504 | `wirebridged.upstreamTimeout`; `wirebridged.viaHeaderTimeout` |
| Streamed-usage request field, chat-completions route | `"stream_options": {"include_usage": true}` on every streamed request; left off when the selected profile's `supports_usage_in_streaming` is `"false"` | `wirebridge.TranslateRequestWith`, `wirebridge.ChatOptions`; the option read in `wirebridged.routeFor` |
| Output-cap request field, chat-completions route | `max_completion_tokens`; `max_tokens` alone when the selected profile's `max_tokens_field` is `"max_tokens"` | `wirebridge.TranslateRequestWith`, `wirebridge.ChatOptions.CapAsMaxTokens`; the option read in `wirebridged.routeFor` |
| Upstream error mapping | 4xx same-status; every 5xx, timeout or dial failure → 502 | `bridgeHandler.relayUpstreamError` |
| Endpoint variable | `YOLO_SERVICE_WIRE_BRIDGE_ENDPOINT`, emitted only when the daemon will serve | `run.serviceEndpointEnvArgs`, `wirebridged.WillServe` |
| Ready-required daemons | `YOLO_JAIL_DAEMON_READY_NAMES=wire-bridge`, emitted beside the endpoint variable | `paths.JailDaemonReadyNamesEnv` |
| Readiness pipe | `YOLO_JAIL_DAEMON_READY_FD`, always descriptor 3 in the daemon; one `ready <name>` or `failed <name> <reason>` line | `paths.JailDaemonReadyFDEnv` |
| Daemon log | `~/.local/state/yolo-jail-daemons/wire-bridge.log` in the jail | `supervisor.LogDir` |
| Host forward log | `~/.local/share/yolo-jail/logs/<cname>-socat.log`, created only when a launch forwards a port | `internal/cli/run/network.go` |
| In-jail forward log | `~/.yolo-socat.log` | `entrypoint.startContainerPortForwarding` |
| Hold a refused boot open | `YOLO_HOLD_ON_REFUSAL=1` | `paths.HoldOnRefusalEnv` |
| Host half argv | `yolo internal daemon wire-bridge`, resolved to the launch's own binary | `packs/wire-bridge/pack.json`; `wirebridged.HostMain`; `launchservice.SelfExec` |
| Host half input file | named by `YOLO_HOST_SERVICE_INPUT`, `0600` in a `0700` temp dir, removed once read | `launchservice.InputEnv`, `launchservice.Input` |
| Host half lifeline | descriptor 4, named by `YOLO_HOST_SERVICE_LIFELINE_FD`; EOF ends the bridge | `launchservice.LifelineFDEnv`, `launchservice.Lifeline` |
| Host half readiness wait, stop grace | 5 seconds; SIGTERM then SIGKILL after 2 seconds | `launchservice.ReadyTimeout`, `launchservice.StopGrace` |
| Host half log | `~/.local/share/yolo-jail/logs/launch-service-wire-bridge.log`, appended by every launch; past 4 MiB the starting launch moves its newest lines to the one archive beside it, `.1`, and empties it | `launchservice.LogPath`; the bound `logcap.MaxBytes`, applied by `launchservice.Start` |
| Selection inputs the daemon re-reads | the composed providers, use-profiles and resolved-profiles tables | `wirebridged.routeFor` |
| `needs` entry fields | the pack name, and the bin condition | `internal/packdecl/needs.go` |
| Manifest top-level keys | see [`pack-system.md`](pack-system.md)'s Current values | `packdecl.Manifest` |
