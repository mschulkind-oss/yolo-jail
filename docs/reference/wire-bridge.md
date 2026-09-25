---
status: current
verified: 2026-09-23
verified_commit: 7ad8358c
covers:
  - internal/wirebridge/
  - internal/wirebridged/
  - internal/packload/needs.go
  - internal/packdecl/needs.go
  - internal/cli/run/packservices.go
  - internal/cli/run/providerlocal.go
  - internal/cli/run/hostports.go
  - packs/wire-bridge/
tags: [packs, providers, services, claude, translation, needs, networking, diagnosis]
summary: "An in-jail translating reverse proxy that manufactures an Anthropic-Messages endpoint on the jail's loopback for OpenAI chat-completions providers and Claude's Codex Responses profile — plus the `service` contribution kind and `needs`, a pack dependency resolved at selection."
---

# The wire bridge — an Anthropic endpoint on the jail's loopback

**Status:** CURRENT as of 2026-09-23, verified against `7ad8358c`; [streamed usage](#streamed-usage)
was rewritten 2026-09-25, after that verification, and is UNMEASURED against the live upstreams. UNMEASURED on the host that
reported the listen-port collision: that the provider-table fix ends it is inferred from an
in-process reproduction, and no launch there has been observed succeeding
([what can hold the listen port](#what-can-hold-the-listen-port-before-the-bridge-does)).

A **wire bridge** *(coined here)* is an in-jail daemon that manufactures, on the jail's own
loopback, a wire protocol a provider does not natively serve, by translating to one it does.
Two routes exist: **Anthropic Messages → OpenAI chat-completions** for declared
providers, and the Claude Codex-profile route to OpenAI Responses described in
[`omp-and-codex-claude-profile.md`](omp-and-codex-claude-profile.md).

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
  is a product; a bridge is a shim. That is the bridge as built: rulings of 2026-09-24 (unbuilt)
  amend it with SigV4 signing, routing by model id and opt-in failover, and an all-traffic mode is
  proposed, all in [`wire-bridge-gateway.md`](../design/wire-bridge-gateway.md).

## `kind: "service"` — the vocabulary it landed as

A **service** *(coined here; the bridge is the first instance)* is a daemon a pack contributes to
a namespace — a jail daemon, a host daemon, or both — plus its endpoint file, its restart policy,
and its reachability witness. **No grants, no boundary, no host state.**

One kind carries both halves because they share the lifecycle, the endpoint and the witness, and
differ only in which namespace the daemon lands in.

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
| token and stop-sequence limits | map | the upstream's equivalents |
| stop reasons | map onto the upstream's finish reasons | finish reason |
| usage | map into Anthropic's terms: the upstream's prompt count **minus** its cached count is `input_tokens`, the cached count is `cache_read_input_tokens`, completion tokens are `output_tokens`; streamed, all three ride `message_delta` ([streamed usage](#streamed-usage)) | usage |
| the stream flag, on the chat-completions route | pass it, and also ask for `stream_options.include_usage`, unless the provider declares `supports_usage_in_streaming` `"false"` | the upstream's final usage chunk |
| cache-control blocks | strip — silently ignored, never an error | — |
| the model id | **passthrough**, normalized only for gateways: Claude's `[1m]` context suffix is stripped, and a bare `deepseek-…` id gains its `deepseek/` vendor prefix | model |

**Auth.** Inbound: **none** — the bridge ignores whatever token the agent sends, because the jail
is the boundary and every process in it already reads the same environment file. (The agent's
derive still emits an auth token beside the loopback URL; it rides the launch exactly as it does
for any other provider, and the bridge discards it.) Outbound: a bearer header carrying the
provider's key, read once at boot.

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

**The shipped upstreams accept it.** The two chat-completions providers the shipped packs route
through the bridge are `cerebras` (`https://api.cerebras.ai/v1`) and `kilo`
(`https://api.kilo.ai/api/gateway`). Neither is gated, for these reasons (READ 2026-09-25, not
measured against the live services):

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

**The Responses route needs no opt-in.** Its terminal event always carries the usage. That event is
`response.completed`, or `response.incomplete` when the output limit stopped the answer, which maps
to `max_tokens`. `message_delta` and `message_stop` go out there, mapped as above. `response.incomplete`
used to be refused as an unsupported event, which turned a partial answer into an error and lost its
usage. The stop stays `max_tokens` when the limit cut off a function call: an open function call
turns only a finished answer (`end_turn`) into `tool_use`, because the call's arguments may be
truncated JSON that Claude would otherwise try to run. The chat-completions route maps
`finish_reason` `"length"` to `max_tokens` whatever is open, and the non-streamed Responses answer
follows the same rule as the stream.

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
  and the bridge reads that file at startup, once, into memory. One writer, one reader; the daemon
  never appears in a process listing with the key. Its own process environment is the fallback
  for notches where the file may not exist, and a file that exists but cannot be read is reported
  rather than silently skipped. The Codex route reads no key at all: it asks the OpenAI credential
  service for an access-only token view per request, and a 401 gets exactly one fresh view before
  the refusal is relayed.
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

## What this does not license

- **No second protocol family.** A bridge for a different wire (and therefore a different agent)
  is a different document with its own cost case.
- **No gateway features** — routing, failover, budgets, multi-provider fan-out, or model-name
  remapping beyond passthrough.
- **No host-side bridge.** The bridge exists only in-jail. The code has no jail dependencies,
  which is what keeps the door open for a host notch to run the same subcommand later — not a
  promise to walk through it.
- **No inbound authentication scheme.** The jail is the trust boundary. If that ever stops being
  true, the bridge grows auth before it grows anything else.
- **No provider-side knob for the port.** The address is the *adapter's* own declaration, with
  exactly one user-scope override (`adapters.<from>-><to>.address`) and nothing else: a provider
  cannot move it, and a workspace config cannot set it at all. The override moves the bind and the
  agent's URL together, on both routes — see [the listen address](#the-listen-address).
- **No `macos-user` bridge.** That backend starts no jail daemons: a launch there names each
  declared one, the bridge included, as not running, and does not fail. It also has no network
  namespace, so an adapter's port there would be a host port, and nothing in
  [what can hold the listen port](#what-can-hold-the-listen-port-before-the-bridge-does) has been
  worked through for that blast radius.

## Why it's this way

Rulings a future change would otherwise undo, with their original IDs.

| Ruling | Why it holds |
| :--- | :--- |
| <a id="wb-d1"></a>[**WB-D1**](#wb-d1) — one protocol pair AT A TIME | A second pair is a second cost case, and bundling them makes the first one unreviewable. ⚠ **This cell read “exactly one protocol pair” until 2026-09-18, and the doc has contradicted it since the Codex route landed** — its own second paragraph says *“Two routes exist”*, and [`packs/wire-bridge/pack.json`](../../packs/wire-bridge/pack.json) declares two `adapter` contributions. Re-read as a rule about how a pair is ADDED rather than how many may exist, which is the reason the cell gives and which the second pair honoured by arriving on its own review. If that reading is wrong the ruling needs restating rather than re-wording — the contradiction is recorded here rather than resolved silently. |
| <a id="wb-d2"></a>[**WB-D2**](#wb-d2) — the URL reaches the agent as the provider's `endpoints.anthropic`, one writer owns it, and the agent's derive is untouched | The ruling holds; **the writer moved on 2026-09-18.** It used to be each consuming provider's manifest, which made the adapter's port a fact stated by every one of its N consumers; it is the adapter's own `address` now, composed into the provider entry by core ([`protocol-resolution.md`](protocol-resolution.md)). What the ruling protects is untouched: one writer for the address, and a derive that could see the bridge would make composition responsible for a fact selection already decides. |
| <a id="wb-d3"></a>[**WB-D3**](#wb-d3) — the dependency is real manifest vocabulary, auto-included at selection and printed | The rejected shape expressed dependency as an *error message the user must act on* rather than a declaration the launcher acts on. A mechanism the manifest cannot state is the wrong mechanism. |
| <a id="wb-d4"></a>[**WB-D4**](#wb-d4) — inbound auth: none; outbound: the `0600` env file read once at boot | The jail is the boundary, and a second inbound scheme would protect the jail from itself. |
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

## Current values

Verified at `7ad8358c`. The prose above explains what each of these is for; this table is the
only place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Service name (supervisor entry, endpoint stem, manifest `endpoint`) | `wire-bridge` | `wirebridged.ServiceName` |
| Endpoint file | `wire-bridge.endpoint` under the jail services dir | `wirebridged.EndpointFile` |
| Listen address, `openai → anthropic` | `http://127.0.0.1:8214` — the adapter's declared `address`, composed into each eligible provider's `endpoints.anthropic.base_url` and parsed back out by the daemon; a user-scope `adapters.openai->anthropic.address` replaces it | `packs/wire-bridge/pack.json`, read by `wirebridged.routeFor` |
| Listen address, `openai-responses → anthropic` (the Codex route) | `http://127.0.0.1:8215` — the adapter's declared `address`, composed into `openai-codex`'s `endpoints.anthropic.base_url` and parsed back out by the daemon, exactly as the row above; `wirebridged.CodexResponsesListenAddr` is the DEFAULT when the entry names no anthropic endpoint, not a bypass of it | `packs/wire-bridge/pack.json`, read by `wirebridged.routeFor`; default in `wirebridged.CodexResponsesListenAddr` |
| Address override key | `adapters.<from>-><to>.address`, **user scope only** | `internal/config/adapters.go`, `yolo config-ref` |
| Restart policy | on failure | `packs/wire-bridge/pack.json` |
| Served path | `POST /v1/messages` and nothing else | `internal/wirebridged/handler.go` |
| Upstream path | the provider's `openai` base URL plus `/chat/completions`; on the Codex route, the subscription base plus `/responses` | `wirebridged.NewHandler`, `wirebridged.CodexResponsesBaseURL` |
| Upstream timeout | 10 minutes, the one timeout the daemon adds | `wirebridged.upstreamTimeout` |
| Streamed-usage request field, chat-completions route (added 2026-09-25, after the commit this table was verified at) | `"stream_options": {"include_usage": true}` on every streamed request; left off when the selected profile's `supports_usage_in_streaming` is `"false"` | `wirebridge.TranslateRequestWith`, `wirebridge.ChatOptions`; the option read in `wirebridged.routeFor` |
| Upstream error mapping | 4xx same-status; every 5xx, timeout or dial failure → 502 | `bridgeHandler.relayUpstreamError` |
| Endpoint variable | `YOLO_SERVICE_WIRE_BRIDGE_ENDPOINT`, emitted only when the daemon will serve | `run.serviceEndpointEnvArgs`, `wirebridged.WillServe` |
| Ready-required daemons | `YOLO_JAIL_DAEMON_READY_NAMES=wire-bridge`, emitted beside the endpoint variable | `paths.JailDaemonReadyNamesEnv` |
| Readiness pipe | `YOLO_JAIL_DAEMON_READY_FD`, always descriptor 3 in the daemon; one `ready <name>` or `failed <name> <reason>` line | `paths.JailDaemonReadyFDEnv` |
| Daemon log | `~/.local/state/yolo-jail-daemons/wire-bridge.log` in the jail | `supervisor.LogDir` |
| Host forward log | `~/.local/share/yolo-jail/logs/<cname>-socat.log`, created only when a launch forwards a port | `internal/cli/run/network.go` |
| In-jail forward log | `~/.yolo-socat.log` | `entrypoint.startContainerPortForwarding` |
| Hold a refused boot open | `YOLO_HOLD_ON_REFUSAL=1` | `paths.HoldOnRefusalEnv` |
| Selection inputs the daemon re-reads | the composed providers, use-profiles and resolved-profiles tables | `wirebridged.routeFor` |
| `needs` entry fields | the pack name, and the bin condition | `internal/packdecl/needs.go` |
| Manifest top-level keys | see [`pack-system.md`](pack-system.md)'s Current values | `packdecl.Manifest` |
