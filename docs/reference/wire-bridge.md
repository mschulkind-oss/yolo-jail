---
status: current
verified: 2026-09-09
verified_commit: 38873c0d
covers:
  - internal/wirebridge/
  - internal/wirebridged/
  - internal/packload/needs.go
  - internal/packdecl/needs.go
  - internal/cli/run/packservices.go
  - packs/wire-bridge/
tags: [packs, providers, services, claude, translation, needs]
summary: "An in-jail translating reverse proxy that manufactures an Anthropic-Messages endpoint on the jail's loopback for a provider that serves only OpenAI chat-completions — plus the two pieces of vocabulary it landed as: the `service` contribution kind, and `needs`, a conditional pack dependency resolved at selection."
---

# The wire bridge — an Anthropic endpoint on the jail's loopback

**Status:** CURRENT as of 2026-09-09, verified against `38873c0d`.

A **wire bridge** *(coined here)* is an in-jail daemon that manufactures, on the jail's own
loopback, a wire protocol a provider does not natively serve, by translating to one it does.
Exactly one exists: **Anthropic Messages → OpenAI chat-completions**.

It exists because an agent can speak exactly one wire protocol and some providers serve only the
other one. No amount of configuration closes that gap: yolo's derives translate config *dialects*,
and a wire protocol is not a config dialect. So the missing thing is a protocol speaker, and it
has to run somewhere — the jail is the only place that needs it, because what the agent consumes
is a base URL, and a base URL can be the jail's own loopback.

**The endpoint trick is the load-bearing part, and it changes no derive.** A provider declares the
bridge's loopback URL as its `anthropic` endpoint; the agent's existing derive emits that URL as
its base URL exactly as it would emit any other. The derive cannot see whether a bridge exists,
and should not.

| Component | Lives in |
| :--- | :--- |
| Translation, both directions, I/O-free | `internal/wirebridge` (`request.go`, `response.go`, `stream.go`) |
| The daemon: listener, upstream dial, SSE framing, status codes, key read | `internal/wirebridged` (`boot.go`, `handler.go`, `endpoint.go`, `keyfile.go`) |
| The serve-or-idle decision, made once | `internal/wirebridged` (`WillServe`, `routeFor`) |
| `needs` — the schema and its validation | `internal/packdecl` (`needs.go`, `Need`, `WhenBins`) |
| `needs` — the selection closure | `internal/packload` (`ResolveNeeds`) |
| Wiring the endpoint variable only when the daemon will serve | `internal/cli/run` (`serviceEndpointEnvArgs`) |
| The pack itself — the first `kind: "service"` | `packs/wire-bridge` |

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
  is a product; a bridge is a shim.

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
  unconditional, which is allowed and unused.

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
binds nothing, publishes nothing, sleeps, and prints one line naming the exact absent fact.

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
| `system` (string or block array) | flatten to one system message | a leading system message |
| text and image blocks | copy; images as base64 data URIs | content parts |
| `tools[]` with an input schema | rename the schema field; **never request strict mode** | `tools[]` |
| tool-use / tool-result blocks | bidirectional mapping | tool calls / tool-role messages |
| thinking config and beta headers | **strip** | never sets a reasoning option |
| upstream reasoning content | **drop, do not surface** | plain text deltas only |
| token and stop-sequence limits | map | the upstream's equivalents |
| stop reasons | map onto the upstream's finish reasons | finish reason |
| usage | map through | usage |
| cache-control blocks | strip — silently ignored, never an error | — |
| the model id | **passthrough verbatim** | model |

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

> [!WARNING]
> **Do not surface upstream reasoning as thinking blocks.** Emitting them obliges the bridge to
> strip them on replay — agents echo thinking back — which is real complexity for no
> coding-agent value. And a thinking *request* is translated only where a 1:1 mapping exists:
> none does today, so nothing is translated and the upstream's own reasoning default stands.
> Inventing a threshold mapping would be inventing behavior.

## Lifecycle and failure behavior

- **Process.** One in-jail daemon under the existing supervisor, as a subcommand of the jail
  daemon binary rather than a new binary — so the "a new `cmd/` binary must be registered in two
  more places or it vanishes" trap never even opens. Restart on failure.
- **Startup order.** Daemons start at boot, before the agent command. The bridge **binds its port
  before writing its endpoint file**, so the file appearing means the listener exists. The
  reachability witness then covers it: a bridge that cannot bind refuses the boot — which is
  precisely the failure mode the preflight philosophy wants, because the alternative is an agent
  handed a base URL that dies at first request in a way nobody attributes.
- **The key channel.** The credential crosses as it does for every non-first-party agent: the
  launcher writes a `0600` environment file from hydrated env sources, and the bridge reads that
  file at startup, once, into memory. One writer, one reader; the daemon never appears in a
  process listing with the key. Its own process environment is the fallback for notches where the
  file may not exist.
- **Upstream failures.** A 4xx is relayed with the **same status**, anthropic-shaped, so the agent
  renders it and owns the retry decision. Every 5xx, timeout, or unreachable dial becomes a **502**
  in the same shape — the family an agent backs off on — logged as one line of status and latency,
  never a body. The upstream's own error *message* is forwarded (it goes to the agent, which
  displays it) but never logged, and a body that does not parse is replaced by a status line
  rather than quoted: an HTML error page in a JSON field helps nobody. **The bridge adds no
  retries of its own** — the agent's retry loop is the retry policy.
- **Concurrency.** A stateless proxy; concurrent requests are fine and there is no shared mutable
  state after boot. Ordering cannot matter, because every request is independent.

**Forbidden, and each for its own reason:** never dial anything but the boot-selected upstream
(read once from the composed table, never from request content); never listen off loopback; never
log request or response bodies, or the key; never cache bodies to disk.

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
- **No second knob for the port.** The provider's declared URL is the single source; the daemon
  binds exactly what it says.

## Why it's this way

Rulings a future change would otherwise undo, with their original IDs.

| Ruling | Why it holds |
| :--- | :--- |
| <a id="wb-d1"></a>[**WB-D1**](#wb-d1) — exactly one protocol pair | A second pair is a second cost case, and bundling them makes the first one unreviewable. |
| <a id="wb-d2"></a>[**WB-D2**](#wb-d2) — the URL is a provider fact, the port lives only there, and the agent's derive is untouched | One writer for the port; and a derive that could see the bridge would make composition responsible for a fact selection already decides. |
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
| <a id="wb-d13"></a>[**WB-D13**](#wb-d13) — the listen port is fixed and manifest-borne | One writer. A port collision is witness-fatal in a fresh namespace rather than a mystery failure. |
| <a id="wb-d14"></a>[**WB-D14**](#wb-d14) — `count_tokens` refuses (404) | A zero-stub is measurably worse than a refusal: it poisons the number it answers, where a 404 falls back to the agent's own estimator. |
| <a id="wb-d15"></a>[**WB-D15**](#wb-d15) — the upstream's reasoning default stands; no provider option, no bridge default | A request naming a thinking level translates only on a 1:1 mapping, and none exists on this wire today, so translating one would be invented behavior. |
| <a id="wb-d16"></a>[**WB-D16**](#wb-d16) — `kind: "service"` is primary vocabulary; loopholes re-form as service + boundary grants | A daemon with no grants is not a loophole, and naming it one would make the trust model unreadable. The re-forming is a follow-up, not a prerequisite. |
| <a id="wb-d17"></a>[**WB-D17**](#wb-d17) — more than one agent bin is a bridge consumer, and the serve predicate walks every active profile | Found while building: a derive that *prefers* an anthropic endpoint when a provider declares one makes that agent a consumer too, and a single-bin condition would have shipped those launches a dead URL with no bridge included. |

## Current values

Verified at `38873c0d`. The prose above explains what each of these is for; this table is the
only place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Service name (supervisor entry, endpoint stem, manifest `endpoint`) | `wire-bridge` | `wirebridged.ServiceName` |
| Endpoint file | `wire-bridge.endpoint` under the jail services dir | `wirebridged.EndpointFile` |
| Listen port | carried **only** in the provider's `endpoints.anthropic.base_url` | `packs/cerebras/pack.json`, read by `wirebridged` |
| Restart policy | on failure | `packs/wire-bridge/pack.json` |
| Served path | `POST /v1/messages` and nothing else | `internal/wirebridged/handler.go` |
| Upstream path | the provider's `openai` base URL plus chat-completions | `internal/wirebridged/boot.go` |
| Upstream error mapping | 4xx same-status; every 5xx, timeout or dial failure → 502 | `bridgeHandler.relayUpstreamError` |
| Endpoint variable | `YOLO_SERVICE_WIRE_BRIDGE_ENDPOINT`, emitted only when the daemon will serve | `run.serviceEndpointEnvArgs`, `wirebridged.WillServe` |
| Selection inputs the daemon re-reads | the composed providers, use-profiles and resolved-profiles tables | `wirebridged.routeFor` |
| `needs` entry fields | the pack name, and the bin condition | `internal/packdecl/needs.go` |
| Manifest top-level keys | see [`pack-system.md`](pack-system.md)'s Current values | `packdecl.Manifest` |
