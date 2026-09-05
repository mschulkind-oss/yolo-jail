---
title: "The wire bridge — claude on chat-completions providers, without leaving the jail"
date: 2026-09-04
status: accepted
tags: [packs, providers, daemons, claude, cerebras, translation]
summary: "Claude Code speaks exactly one wire protocol, and Cerebras serves exactly the other one. A wire bridge — an in-jail translating reverse proxy — manufactures the endpoint claude needs on the jail's own loopback, with no host grant and no boundary crossing. This doc designs it: the kind: service vocabulary it lands as, the real needs dependency that includes it when claude is in use, and the exact wire surface it translates."
---

# The wire bridge — claude on chat-completions providers, without leaving the jail

**Status:** DECIDED, 2026-09-04 — every open question ruled by the maintainer (Decision
Ledger, below). Nothing built. Answers OQ-1 of
[`cerebras-pack-and-copilot-delivery.md`](cerebras-pack-and-copilot-delivery.md), which left
this exact question open.

**The short version.** claude cannot ride Cerebras because the two speak disjoint wire
protocols and yolo's derives translate *configuration*, never wire. The fix is a **wire
bridge** *(coined here)*: an in-jail daemon that listens on the jail's loopback, speaks the
Anthropic Messages protocol to claude, and speaks OpenAI chat-completions upstream. The
provider declares the bridge's loopback URL as its `anthropic` endpoint — the existing
claude derive needs **zero changes** — and the dependency is REAL PACK VOCABULARY: cerebras
declares `needs: [{pack: wire-bridge, when_bins: [claude]}]`, and the launcher includes the
bridge pack automatically when claude is among the launch's agents. It is
loophole-*shaped* (in-jail daemon, supervised, endpoint file) but not a loophole: it
crosses no boundary and holds no host grant, which is exactly why it should not be called
one.

**The most important thing in this doc is §3** (the dependency vocabulary) — everything
else follows from it.

**Reads with:** [`providers.md`](../reference/providers.md) (the provider system the bridge
plugs into; §"Per-agent delivery" is why claude is the only consumer),
[`cerebras-pack-and-copilot-delivery.md`](cerebras-pack-and-copilot-delivery.md) (the audit
that opened this; its OQ-1 is this doc's subject, its D-4 gets revisited in §6),
[`loophole-protocol.md`](loophole-protocol.md) (the daemon machinery a bridge reuses),
[`local-model-endpoints.md`](../research/local-model-endpoints.md) (the runtime ruling that
kills the buy-don't-build alternatives).

---

## 1. The diagnosis, precisely

Verified 2026-09-04 (cerebras-pack-and-copilot-delivery.md §research, four primary sources):
Cerebras serves `POST /v1/chat/completions` and nothing else — no `/v1/responses`, no
Anthropic-compatible endpoint. Claude Code's entire provider surface is
`ANTHROPIC_BASE_URL` + `ANTHROPIC_AUTH_TOKEN`; every request is Anthropic Messages wire.
z.ai works with claude for one reason: it *operates* an anthropic-compatible route. No
amount of yolo configuration can help when the service itself does not speak the protocol —
the derives translate config dialects, and a wire protocol is not a config dialect.

So the missing thing is a protocol speaker. It has to run *somewhere*, and the jail is the
only place that needs it: claude dials a base URL, and a base URL can be the jail's own
loopback.

## 2. What a wire bridge is — and is not

A **wire bridge** *(coined here)* is an in-jail daemon that manufactures, on the jail's
loopback, a wire protocol a provider does not natively serve, by translating to one it
does. This doc designs exactly one: `anthropic → openai-chat-completions`.

It is deliberately **not**:

- **Not a loophole.** Loopholes reach *out* from the jail to host capabilities the jail
  lacks — the host grants something, and the security model reviews it
  (security-shim.md, loophole-protocol.md). The bridge serves *inward*: it binds the
  jail's loopback, crosses no boundary, reads no host state, and needs no grant. The
  maintainer's instinct ("I don't love calling it a loophole, but it's very similar") is
  correct on both halves: the *machinery* is the same (supervised in-jail daemon, endpoint
  under `/run/yolo-services/`, reachability witness), the *trust* is not — and the kind
  ruling below (§2.1) makes the vocabulary say so.
- **Not a gateway.** One upstream, chosen by the launch's own selection machinery. No
  routing tables, no failover, no budgets, no model remapping beyond what translation
  requires. A gateway is a product; a bridge is a shim.

### 2.1 The `service` kind is primary — loopholes form around it (ruled)

**Ruled by the maintainer, 2026-09-04:** "can we make service the primary and have
loopholes form around them? or host services? not sure what the loophole piece looks like
precisely." All three halves, answered:

A **service** *(coined here; first instance is this bridge)* is a daemon a pack
contributes to a namespace — a jail daemon (a `yolo-jaild` subcommand under `supervise`)
or a host daemon (a `yolo internal daemon` self-exec), or both — plus its endpoint file,
its restart policy, and its reachability witness. No grants, no boundary, no host state.
The claude-oauth-broker is the existing shape wearing loophole vocabulary: a host daemon
plus a jail-side terminator, endpoint under `/run/yolo-services/` — a service in all but
name. "Or host services?" — yes: one kind carries both halves, because they share the
lifecycle, the endpoint, and the witness, and differ only in which namespace the daemon
lands in.

What the loophole piece actually is, precisely — the `Loophole` struct's fields
(loopholes.go:75-146), decomposed:

| Belongs to the **service** half | Belongs to the **loophole** half (the boundary) |
| :--- | :--- |
| `JailDaemon` (+ its `restart` policy), endpoint machinery, witness | `HostDaemon` — a daemon on the *host* side of the boundary |
| `Platforms` — where it can run at all | `HostBindMount`, `HostDevices` — host filesystem/devices granted *into* the jail |
| `Serves` — capabilities named | `StateFiles` — host state crossing the boundary, least-priv scoped |
| `Settings` — config keys it owns | `Intercepts`, `BrokerIP`, `CACert` — the loopback-TLS interception broker; `Requires` — host-capability probes; `JailEnv` — env injected on the loophole's behalf |

The refactor direction the ruling names — **loopholes form around services**: a loophole
becomes its service half *plus* boundary grants layered on, rather than a monolith that
happens to contain a daemon. The bridge lands as `kind: service` now, as the first
instance; re-forming the five existing loophole packs around the new kind is a named
follow-up with its own doc, explicitly NOT a prerequisite — their manifests keep their
meaning, and the decomposition above is the map that follow-up walks.

> [!WARNING]
> **Rejected on the way here: loophole-manifest reuse.** My first draft leaned "reuse the
> loophole manifest for v1, record the misnomer, split the kind when a second daemon
> exists." The maintainer's ruling is the better call for the reason the draft itself
> dissented with: vocabulary accreted "temporarily" is exactly how `wire_api`'s four-value
> enum happened — a kind that misnames its instances does not get split later; it gets
> copied.

## 3. The dependency — real pack vocabulary, conditionally included

**Ruled by the maintainer, 2026-09-04:** "we should have real pack vocab for this, and
the optional inclusion if claude is in use." The two shapes this doc first drafted —
fold-in (the OQ-A10 precedent) and *enforcement-as-refusal* (the launch errors if the
bridge pack is missing) — are both rejected: the first makes cerebras the owner of a
general capability, and the second expresses dependency as an error message the user has
to act on rather than as a declaration the launcher acts on. Real vocabulary it is.

### 3.1 The vocabulary

A pack manifest may carry a top-level `needs` array; each entry is a **conditional pack
dependency**:

```jsonc
// packs/cerebras/pack.json
{
  "name": "cerebras",
  "needs": [
    {"pack": "wire-bridge", "when_bins": ["claude"]}
  ],
  "contributes": [ ... ]
}
```

- **`pack`** names another pack — today, only a pack in the **embedded official set**
  (WB-D9). A fetched pack `needs`-ing another fetched pack would make selection itself a
  supply-chain channel; refusing it keeps `packs:` the only place unreviewed code enters a
  launch.
- **`when_bins`** is the condition: the need is live only when some *selected* pack
  installs one of the named bins. Core speaks bins, not agents ("AGENTS ARE PACKS", and
  the profile-gating `profile:` modifier already keys on bins the same way —
  packload's `profileActive`); `claude` here means "a selected pack installs the `claude`
  CLI," which is exactly "claude is in use." Multiple bins are OR. An absent `when_bins`
  means unconditional — allowed, though nothing ships one yet.

**Resolution** happens host-side, at selection, *before* staging (the mount is the
filter, so the closure must be computed before `stagePacks` sees a set):

1. Start from the user's `packs` list.
2. For each selected pack, for each live need (condition evaluated against the selected
   set), add the named pack if absent.
3. Repeat until stable — **transitive closure** (WB-D10). A cycle refuses the launch,
   naming the loop. Idempotent by construction: a pack already selected is a no-op,
   including a pack the *user* selected (explicit selection is never overridden, only
   joined).
4. The launch prints the additions — one line, `+ wire-bridge (needed by cerebras:
   claude selected)` — and `yolo check` shows the same beside the pack list. Not a
   config-change confirmation: nothing in the user's config changed, and the banner line
   is where every other launch-time fact lives.
5. From there the added pack is *ordinary selection*: staged, footprint-accounted,
   preflighted, its daemon contributed. No special path after the closure runs.

The trigger, precisely: **on every launch and every `yolo check`**, over the resolved
selected set, before staging and before any preflight. Not lazily, not at daemon start —
the daemon list composes from staged manifests, so the closure is upstream of everything.

### 3.2 Why this is the right shape (and what it costs)

The condition is deliberately *coarse* — "claude selected," not "claude's active profile
routes at a bridged provider." The precision is recovered by the daemon being
selection-lazy (§3.4): claude selected but riding zai (or nothing) while pi rides cerebras
includes the bridge, which boots, reads the selection, finds no bridged anthropic route
for claude, and idles healthy. Coarse condition + lazy daemon = precise behavior with
vocabulary a manifest can state without knowing anything about profiles.

Costs, named:

- **Manifest vocabulary grows** — a top-level `needs` key, its validation (strict path:
  refused; skew path: skipped-and-reported like every unknown key), and a resolution step
  in the launch pipeline. That is the price of "real vocab," paid once.
- **The selected set is no longer literally the user's list** — which is why the banner
  line is non-negotiable (a pack nobody typed joining a launch silently is the exact
  hazard the config-change confirmation exists for; printing is the mitigation).
- **`yolo pack footprint`/`yolo pack lint`** must present the dependency edge (as an
  edge, not a claim — `needs` claims nothing itself; it extends selection).

### 3.3 The endpoint trick — unchanged

The moving parts still line up without touching the derive, which remains the
load-bearing trick:

1. **The URL is a provider fact.** `validateProviderEndpoints` accepts any http/https
   URL without userinfo, so `endpoints.anthropic.base_url:
   "http://127.0.0.1:8214"` is already a legal declaration
   (internal/packdecl/contributes.go, `validateProviderEndpoints`). The manifest URL is
   the *single source* of the port — the daemon reads the composed table at boot and
   binds exactly what the URL says. No second knob; one writer.
2. **The claude derive is unchanged.** It already emits `ANTHROPIC_BASE_URL` from
   `endpoints.anthropic.base_url` (packs/claude/derive.lua). A loopback URL composes like
   any other. The derive cannot even see whether a bridge exists — and should not; the
   closure in §3.1 is what makes the declaration true, not composition's business.

### 3.4 The daemon is selection-lazy, not config-lazy

At boot it reads `YOLO_PROVIDERS` + the resolved selection
(`internal/entrypoint/providers.go` already parses both). If claude's active profile
names a provider whose `anthropic` endpoint is jail-local, the bridge serves that
provider's `openai` endpoint upstream; otherwise it idles healthy. This laziness is what
licenses the *coarse* `when_bins` condition (§3.2): including the bridge whenever claude
is selected costs a healthy idle process in the launches where claude rides something
else, and precision finer than the manifest can state is recovered here, by the same
selection table every agent honors.

A user-config provider (no pack) can join for free: declare
`endpoints.anthropic.base_url: "http://127.0.0.1:8214"` on your own provider entry,
select the bridge pack, and the same machinery serves it. Not a feature; a consequence,
recorded so nobody mistakes it for an accident to close. (User config carries no `needs`
key — WB-D11 — so a user-configured loopback endpoint with no bridge pack selected is a
dead URL of the user's own making; `yolo check` reports a jail-local anthropic endpoint
whose launch selects no bridge as a WARNING, not a refusal. That is the one behavior the
old enforcement shape guarded, kept as diagnosis instead of error.)

## 4. What must translate — the protocol surface claude actually utters

The bridge implements what Claude Code sends, not the whole Anthropic API. Verified
against the claude 2.1.259 binary and live z.ai traffic, 2026-09-04:

| Claude sends | Bridge does | Upstream (chat-completions) |
| :--- | :--- | :--- |
| `POST /v1/messages` (non-stream) | translate | `POST /chat/completions` |
| `POST /v1/messages` (SSE) | translate event-for-event | `stream: true` chunks |
| `POST /v1/messages/count_tokens` | **refuse (404)** — no estimate, no zero-stub. Measured 2026-09-04: z.ai's anthropic route *answers* count_tokens with `200 {"input_tokens":0}`, which is exactly why claude displays "0 tokens" in places — a zero-stub poisons the count it was asked for. A 404 sends claude to its own estimator (a real estimate), so refusing is the only answer that invents nothing and lies about nothing (WB-D14) | no upstream call |
| `system` (string or block array) | flatten to one system message | `messages[0] {role: system}` |
| `messages[]` text/image blocks | copy; images as base64 data URIs (qwen-3.8-27b accepts them) | content parts |
| `tools[]` (`input_schema`) | rename to `parameters`; **never set `strict: true`** — qwen's strict mode rejects `pattern`/`format` that claude's schemas freely contain (cerebras tool-use doc, measured limits) | `tools[]` |
| `tool_use` / `tool_result` blocks | bidirectional mapping | `tool_calls` / `role: tool` |
| `thinking` config / `anthropic-beta` headers | strip. The upstream's reasoning default always stands — no provider option, no bridge default (WB-D15, ruled: "upstream to stay its default, but also allow setting it from within the agent when possible"). The from-the-agent half: a request that names a thinking level is translated **only** where a 1:1 mapping exists — and none exists on today's anthropic wire (a `budget_tokens` → `reasoning_effort` threshold would be invented behavior), so v1 translates nothing and the door is this row | `reasoning_effort` never set by the bridge |
| upstream reasoning content | **drop, do not surface** — emitting anthropic `thinking` blocks obliges the bridge to strip them on replay (claude echoes thinking back), a complexity with no coding-agent value; plain text deltas only | `choices[].delta.content` |
| `max_tokens`, `stop_sequences` | map | `max_tokens`, `stop` |
| stop reasons | map: `end_turn→stop`, `max_tokens→max_tokens` (length), `tool_use→tool_calls` | `finish_reason` |
| `usage` | map through | `usage` |
| `cache_control` blocks | strip; cerebras has no prompt cache — silently ignored, never an error | — |
| model id | **passthrough verbatim** — the wire-true alias rule (04b3f039) already guarantees the id the upstream expects; `[1m]` never enters (context < 1M ⇒ the claude derive emits no suffix) | `model` |

**Auth.** Inbound: none. The bridge ignores whatever token claude sends — the jail is the
boundary, and every process in it already reads `yolo-user-env.sh`. (claude's derive still
emits `ANTHROPIC_AUTH_TOKEN` = the provider key next to the loopback URL; it rides the
argv exactly as it does for z.ai — the recorded D8 exposure, no new one — and the bridge
discards it.) Outbound: `Authorization: Bearer <the provider's key>`, read at boot (§5).

## 5. Lifecycle, key channel, and failure behavior

- **Process.** One in-jail daemon under the existing supervise machinery
  (`yolo-jaild supervise` reading `YOLO_JAIL_DAEMONS` — live shape this session:
  `[{"name": "claude-oauth-broker", "cmd": ["yolo-jaild", "oauth-terminator"], "restart":
  "on-failure"}]`). The bridge is a `yolo-jaild` subcommand, not a new binary — the
  five-binary trap (AGENTS.md, shippedclients_test.go) never even opens. `restart:
  on-failure`, no restart limit beyond supervise's default.
- **Startup order.** Daemons start at boot, before the agent command. The bridge binds
  its port *before* writing its endpoint file `/run/yolo-services/wire-bridge.endpoint`;
  the file appearing means the listener exists. The jail's reachability witness then
  covers it for free: a bridge that cannot bind refuses the boot (the same
  `unreachable-service-is-fatal` refusal the oauth broker lives under, since 2026-08-18),
  which is precisely the failure mode the preflight philosophy wants — claude must never
  get a base URL that dies at first request in a way nobody attributed.
- **The key channel.** The credential crosses as it does for every non-claude agent: the
  launcher writes `yolo-user-env.sh` (0600) from hydrated `env_sources`
  (userenv.go:44-65), and the bridge reads that file at startup — one read, then
  in-memory. One writer (the launcher), one reader (the bridge); the daemon never appears
  in `ps` with the key (unlike the claude argv channel). Fallback: the daemon's own
  process environment, for `yolo host`-style notches where the file may not exist.
- **Upstream failures.** 4xx → same-status anthropic-shaped error (claude renders it);
  5xx/timeout → `529`/`502` anthropic-shaped overloads (claude retries with backoff);
  upstream unreachable at *request* time → `502`, logged one line (status + latency,
  never bodies). The bridge adds no retries of its own — claude's retry loop is the retry
  policy.
- **Concurrency.** Stateless request-at-a-time proxy; N concurrent requests fine; no
  shared mutable state after boot. Ordering cannot matter; every request is independent.
- **Forbidden.** Never dial anything but the selected provider's `openai` base URL (the
  upstream is read once at boot from the composed table, never from request content).
  Never listen off loopback. Never log request/response bodies or the key. Never cache
  bodies to disk.

## 6. Consequences for packs/cerebras (and one reversed ruling)

With a bridge selectable, claude *can* ride cerebras — which reverses one ruling and adds
one declaration:

- **D-4 of the cerebras doc is void where claude rides the bridge**: the doc refused
  `context_window`/`api_timeout_ms` on cerebras because "claude cannot ride it, and dead
  options read as promises." A bridged launch makes them live. cerebras should declare
  `context_window: "65536"` (tokens; the free-tier figure — the conservative bound; a
  paid-tier user overrides to `"131072"` in their own profile) so claude's auto-compact
  triggers at the *real* window instead of claude's default, and `api_timeout_ms` stays
  absent (Cerebras is ~1500 tok/s; no evidence of 50-minute turns).
- The `anthropic` endpoint declaration is conditional on this design shipping — until
  then it must NOT land (a loopback URL without a bridge is a lie in the manifest and a
  refusal at preflight once §3's rule exists; landing the rule and the declaration in the
  same commit as the daemon is the sequencing, §8).
- `gpt-oss-120b`'s exclusion (D-1) is unaffected — the bridge changes who can *speak*,
  not which models are fit for agentic use.

## 7. What this does not license

- **No `/v1/responses` bridge for codex.** A second protocol family for a second agent is
  a second doc with its own cost case; codex-on-cerebras stays unwireable.
- **No gateway features** — routing, failover, budgets, multi-provider fan-out, model
  name remapping beyond passthrough.
- **No host-side bridge.** The bridge exists only in-jail; `yolo host -- claude` gets no
  bridged routing in v1 (the notch can adopt it later by running the same subcommand —
  the code having no jail dependencies is what keeps that door open, not a promise to
  walk through it).
- **No inbound authentication scheme.** The jail is the trust boundary (§4). If that ever
  stops being true, the bridge grows auth before it grows anything else.
- **No streaming of reasoning.** Dropped at the bridge (§4) — revisiting it is allowed
  only with a measured need, not an aesthetic one.

## 8. What I would build, in order

1. **The vocabulary, both pieces** — `needs` (packdecl schema + validation: strict
   refuse, tolerant skip-and-report; the selection closure host-side before staging,
   transitive, cycle-refusing, embedded-only per WB-D9/D10; the banner + `yolo check`
   lines; `pack footprint`/`pack lint` presenting the edge) and `kind: service` (the
   §2.1 kind: a jail or host daemon contribution, endpoint, restart policy, witness — no
   grants). Shipped with tests using fixture packs — a needing pack, a needed pack, a
   minimal service — and no bridge anywhere yet: the vocabulary is general and lands on
   its own, exactly because the maintainer asked for vocabulary and not a bridge-shaped
   special case.
2. **The translator as a library** (pure Go, stdlib `net/http` only — no new vendored
   deps; the hermetic build stays hermetic), with table tests over recorded
   request/response fixture pairs: every row of §4's table, both directions, streaming
   included. This is where all the protocol risk lives; it is testable without a container.
3. **The daemon**: subcommand, endpoint file, witness registration, the boot-time table
   read, the key read. Unit tests with an in-memory listener; an integration test that
   curls the bridge with anthropic-shaped fixtures against a stub upstream — **no agent
   ever runs** (the no-agent-tests rule); the stub is an `httptest` server, not cerebras.
4. **The pack + the cerebras change, in one commit**: `packs/wire-bridge/` — the first
   `kind: service` instance, README opening with the §2 not-a-loophole paragraph, census
   bumps, embed list, the same drumbeat the cerebras pack walked — *together with* cerebras's `needs` entry, the
   loopback `anthropic` endpoint, and `context_window`. The need and the endpoint it
   makes true cannot ship apart, or there is a window where the closure stages a daemon
   nothing routes at (harmless) or — the bad direction — the endpoint routes at a daemon
   no closure includes (the dead URL, now only reachable by hand-editing, which is the
   user's own config again).

**Done looks like:** in a nested jail (`cd /tmp/yolo-nested && YOLO_REPO_ROOT=/workspace
yolo -- bash`, never from `/workspace`) with `claude` and `cerebras` selected (wire-bridge
NOT in the config) and `-p cerebras`: the banner prints
`+ wire-bridge (needed by cerebras: claude selected)`;
`curl $ANTHROPIC_BASE_URL/v1/messages` (anthropic-shaped body) returns anthropic-shaped
SSE with qwen-3.8-27b content from the real upstream; the count_tokens endpoint answers;
the same config minus the `claude` pack stages no bridge at all and routes nothing at
loopback; without `-p cerebras` the bridge boots, finds no bridged route, and idles.
Human check beyond that: run claude on a real task — which only the maintainer can do,
and the doc names it rather than pretending a test covers it.

## 9. Risks

| Risk | Mitigation |
| :--- | :--- |
| Translation drift: claude sends a shape the bridge mishandles (new beta blocks, tool variants) | §4's table is the contract; unknown block types fail **closed** — an anthropic-shaped 400 naming the unrecognized block, never a silently-mistranslated request |
| Claude Code updates change its dialect | the claude-api surface is stable and small; the fixture tests pin what we translated and break loudly when claude's binary changes what it sends is *not* testable from here — the 400-name-the-block failure keeps a drift visible at first request instead of mysterious |
| Loopback port collision inside the jail | the manifest URL owns the port (one writer); 8214 chosen clear of baked services; a collision refuses the boot via the witness, not via a mystery failure |
| An auto-included pack surprises the user | WB-D12: the banner and `yolo check` name every addition with its cause; a silent join is the one forbidden behavior of the closure |
| The bridge becomes a de-facto gateway (scope creep) | §7 is the fence; a gateway is a different doc |
| Key exposure grows a new channel | the key rides the 0600 file + boot-time read only; `ps` shows no key; the argv exposure that already exists (claude's token) is unchanged, not extended |

## Decision Ledger

| ID | Ruling | Date | Settled in |
| :--- | :--- | :--- | :--- |
| WB-D1 | The bridge translates exactly one protocol pair: anthropic Messages ↔ openai-chat-completions | 2026-09-04 | §2, §7 |
| WB-D2 | The URL is a provider fact (manifest `endpoints.anthropic.base_url`), the port lives only there, and the claude derive is untouched | 2026-09-04 | §3 |
| WB-D3 | Dependency is real manifest vocabulary — top-level `needs` with `when_bins` — auto-included at selection resolution and printed on the banner. SUPERSEDES this doc's own first draft (enforcement-as-refusal), rejected by the maintainer 2026-09-04: a mechanism the manifest cannot state is the wrong mechanism | 2026-09-04 | §3.1 |
| WB-D4 | Inbound auth: none; the jail is the boundary. Outbound key: the 0600 `yolo-user-env.sh`, read once at boot | 2026-09-04 | §4, §5 |
| WB-D5 | Upstream reasoning content is dropped, not surfaced as thinking blocks; unknown block types fail closed with a named 400 | 2026-09-04 | §4, §9 |
| WB-D6 | `strict: true` is never sent upstream — claude's schemas contain what qwen's strict mode rejects | 2026-09-04 | §4 |
| WB-D7 | Build in-repo, stdlib-only. Runtime axis already ruled: Go/Rust over Python (local-model-endpoints.md, 2026-08-20); Bifrost is a framework where a shim is needed; the four community `claude-code-proxy` repos are Python/dormant/wrong-problem | 2026-09-04 | §8 |
| WB-D8 | cerebras declares `context_window: "65536"` when this ships (reversing cerebras-doc D-4's reasoning for claude-only options, not its method) | 2026-09-04 | §6 |
| WB-D9 | `needs` may name only embedded official packs — selection must not become a supply-chain channel for unreviewed code | 2026-09-04 | §3.1 |
| WB-D10 | Needs resolve as a transitive closure at selection, before staging; cycles refuse the launch naming the loop; explicit user selection is joined, never overridden | 2026-09-04 | §3.1 |
| WB-D11 | User config carries no `needs` key — manifests only. A user-declared loopback anthropic endpoint with no bridge selected is their own dead URL; `yolo check` warns, never refuses | 2026-09-04 | §3.4 |
| WB-D12 | The auto-inclusion prints on the launch banner and in `yolo check` — non-negotiable: a pack nobody typed must never join a launch silently | 2026-09-04 | §3.1 |
| WB-D13 | The listen port is fixed and manifest-borne — 8214, carried only in the provider's `anthropic` base_url; a collision is witness-fatal in a fresh namespace (OQ-3, ruled 2026-09-04) | 2026-09-04 | §3.1, §3.3 |
| WB-D14 | `count_tokens` refuses (404). No estimate, no zero-stub — measured: z.ai answers it `200 {"input_tokens":0}`, the source of claude's "0 tokens" display; a refusal falls back to claude's own estimator. Ruled: "I don't want to invent behavior" (OQ-4) | 2026-09-04 | §4 |
| WB-D15 | Upstream reasoning default stands — no provider option, no bridge default. A request naming a thinking level translates only on a 1:1 mapping; none exists today, so v1 translates nothing (OQ-5, ruled: "upstream to stay its default, but also allow setting it from within the agent when possible") | 2026-09-04 | §4 |
| WB-D16 | `kind: service` is primary vocabulary — a jail or host daemon contribution with endpoint, restart, and witness, no grants. The bridge is its first instance; loopholes re-form as service + boundary grants (the §2.1 decomposition) as a named follow-up, not a prerequisite (OQ-2, ruled: "make service the primary and have loopholes form around them") | 2026-09-04 | §2.1 |
