---
title: "The wire bridge — claude on chat-completions providers, without leaving the jail"
date: 2026-09-04
status: in-review
tags: [packs, providers, daemons, claude, cerebras, translation]
summary: "Claude Code speaks exactly one wire protocol, and Cerebras serves exactly the other one. A wire bridge — an in-jail translating reverse proxy — manufactures the endpoint claude needs on the jail's own loopback, with no host grant and no boundary crossing. This doc designs it: the pack shape, how cerebras depends on it, what must translate, and what refuses when it is missing."
---

# The wire bridge — claude on chat-completions providers, without leaving the jail

**Status:** DESIGN, 2026-09-04. Nothing built. Answers OQ-1 of
[`cerebras-pack-and-copilot-delivery.md`](cerebras-pack-and-copilot-delivery.md), which left
this exact question open.

**The short version.** claude cannot ride Cerebras because the two speak disjoint wire
protocols and yolo's derives translate *configuration*, never wire. The fix is a **wire
bridge** *(coined here)*: an in-jail daemon that listens on the jail's loopback, speaks the
Anthropic Messages protocol to claude, and speaks OpenAI chat-completions upstream. The
provider declares the bridge's loopback URL as its `anthropic` endpoint — the existing
claude derive needs **zero changes** — and a preflight rule makes the dependency real:
routing claude at a jail-local anthropic endpoint without the bridge pack selected refuses
the launch, naming the pack. It is loophole-*shaped* (in-jail daemon, supervised, endpoint
file) but not a loophole: it crosses no boundary and holds no host grant, which is exactly
why it should not be called one.

**The most important thing in this doc is §3** (the dependency ruling) — everything else
follows from it.

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
  under `/run/yolo-services/`, reachability witness), the *trust* is not. Whether it gets
  its own contribution kind or reuses the loophole manifest is OQ-2.
- **Not a gateway.** One upstream, chosen by the launch's own selection machinery. No
  routing tables, no failover, no budgets, no model remapping beyond what translation
  requires. A gateway is a product; a bridge is a shim.

## 3. The dependency — selection, enforcement, or fold-in

The maintainer's sketch: "a proxy pack, and cerebras depends on it if you're using
claude." There is no pack→pack dependency channel today, and the nearest precedent
(loophole-activation.md OQ-A10, claude-oauth-broker) *removed* one by folding the daemon
into the pack that needs it — "selection is the dependency." Three shapes, then:

| Shape | What it is | Costs | Verdict |
| :--- | :--- | :--- | :--- |
| **A. Fold-in** (OQ-A10 pattern) | packs/cerebras contributes the bridge daemon itself; selecting cerebras always runs it | zero new vocabulary; misconfiguration unrepresentable; but every openai-only provider pack duplicates the contribution, the daemon runs even when claude never rides it, and a user-config provider can never get a bridge | **Rejected as the primary shape** — it makes cerebras the owner of a general capability, and the next provider (groq, together, …) copies it |
| **B. Standalone pack + enforced endpoint** (this design) | a `wire-bridge` pack contributes the daemon; the *cerebras provider* declares the bridge's loopback URL as its `anthropic` endpoint; a preflight rule refuses any launch that routes claude at a jail-local anthropic endpoint while the bridge is absent | one enforcement rule (host-side, `providerpreflight`'s neighborhood); the refusal state must be well-worded | **Proposed** — "depends on it" becomes *a refusal that names the pack*, which is how yolo says dependency everywhere else (missing keys, undeclared profiles) |
| **C. Capability vocabulary** | as B, but the bridge's manifest `serves: ["anthropic-wire"]` (the existing `Serves` machinery, loopholes.go:134-141) and the preflight checks capability presence rather than pack identity | one step more general than B; no second consumer exists yet | **Rejected for v1** — vocabulary ahead of need; B's rule is one `if`, and C can be retrofitted the day a second bridge ships without moving anything |

Under B the moving parts line up without touching the derive — this is the load-bearing
trick:

1. **The URL is a provider fact.** `validateProviderEndpoints` accepts any http/https
   URL without userinfo, so `endpoints.anthropic.base_url:
   "http://127.0.0.1:8214"` is already a legal declaration
   (internal/packdecl/contributes.go, `validateProviderEndpoints`). The manifest URL is
   the *single source* of the port — the daemon reads the composed table at boot and
   binds exactly what the URL says. No second knob; one writer.
2. **The claude derive is unchanged.** It already emits `ANTHROPIC_BASE_URL` from
   `endpoints.anthropic.base_url` (packs/claude/derive.lua). A loopback URL composes like
   any other. The derive cannot even see whether a bridge exists — and should not; that
   is enforcement's business (§3's refusal), not composition's.
3. **The daemon is selection-lazy, not config-lazy.** At boot it reads `YOLO_PROVIDERS` +
   the resolved selection (`internal/entrypoint/providers.go` already parses both). If
   claude's active profile names a provider whose `anthropic` endpoint is jail-local, the
   bridge serves that provider's `openai` endpoint upstream; otherwise it idles healthy.
   Selecting the pack is opt-in enough; the *use* is decided by the same selection table
   every agent honors.

A user-config provider (no pack) can join for free: declare
`endpoints.anthropic.base_url: "http://127.0.0.1:8214"` on your own provider entry,
select the bridge pack, and the same machinery serves it. Not a feature; a consequence,
recorded so nobody mistakes it for an accident to close.

**The enforcement rule, precisely.** Host-side preflight, after composition and selection
resolve, before any argv: *if claude's effective profile resolves to a provider whose
`anthropic` endpoint is a loopback address, and no selected pack contributes the bridge
daemon, refuse*, naming both the provider and the `wire-bridge` pack. Trigger: every
launch, at the same moment the credential preflight runs. Not claude-only-generalized:
pi/opencode/copilot resolve `openai` endpoints and never see the loopback URL, so the rule
is scoped to the one agent whose derive reads the `anthropic` key. (If a future agent
grows an anthropic derive, the rule widens with it — one place, named.)

## 4. What must translate — the protocol surface claude actually utters

The bridge implements what Claude Code sends, not the whole Anthropic API. Verified
against the claude 2.1.259 binary and live z.ai traffic, 2026-09-04:

| Claude sends | Bridge does | Upstream (chat-completions) |
| :--- | :--- | :--- |
| `POST /v1/messages` (non-stream) | translate | `POST /chat/completions` |
| `POST /v1/messages` (SSE) | translate event-for-event | `stream: true` chunks |
| `POST /v1/messages/count_tokens` | **estimate locally** (OQ-4) | no upstream call |
| `system` (string or block array) | flatten to one system message | `messages[0] {role: system}` |
| `messages[]` text/image blocks | copy; images as base64 data URIs (qwen-3.8-27b accepts them) | content parts |
| `tools[]` (`input_schema`) | rename to `parameters`; **never set `strict: true`** — qwen's strict mode rejects `pattern`/`format` that claude's schemas freely contain (cerebras tool-use doc, measured limits) | `tools[]` |
| `tool_use` / `tool_result` blocks | bidirectional mapping | `tool_calls` / `role: tool` |
| `thinking` config / `anthropic-beta` headers | strip | `reasoning_effort` from the provider's option (§6), default: absent — the upstream default stands |
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

1. **The translator as a library** (pure Go, stdlib `net/http` only — no new vendored
   deps; the hermetic build stays hermetic), with table tests over recorded
   request/response fixture pairs: every row of §4's table, both directions, streaming
   included. This is where all the protocol risk lives; it is testable without a container.
2. **The daemon**: subcommand, endpoint file, witness registration, the boot-time table
   read, the key read. Unit tests with an in-memory listener; an integration test that
   curls the bridge with anthropic-shaped fixtures against a stub upstream — **no agent
   ever runs** (the no-agent-tests rule); the stub is an `httptest` server, not cerebras.
3. **The enforcement rule** (host-side preflight) + **the cerebras manifest change** (the
   loopback `anthropic` endpoint, `context_window`) in the same commit — the rule and the
   declaration that trips it cannot ship apart, or there is a window where claude gets a
   dead URL.
4. **The pack**: `packs/wire-bridge/` with README (the §2 not-a-loophole paragraph is its
   opening), census bumps, embed list — the same drumbeat the cerebras pack walked.

**Done looks like:** in a nested jail (`cd /tmp/yolo-nested && YOLO_REPO_ROOT=/workspace
yolo -- bash`, never from `/workspace`) with `wire-bridge`, `claude`, and `cerebras`
selected and `-p cerebras`: `curl $ANTHROPIC_BASE_URL/v1/messages` (anthropic-shaped body)
returns anthropic-shaped SSE with qwen-3.8-27b content from the real upstream; the
count_tokens endpoint answers; with the bridge pack dropped from the selection, the same
launch **refuses** naming `wire-bridge`; the jail without `-p cerebras` boots with the
bridge idling and nothing routed. Human check beyond that: run claude on a real task —
which only the maintainer can do, and the doc names it rather than pretending a test
covers it.

## 9. Risks

| Risk | Mitigation |
| :--- | :--- |
| Translation drift: claude sends a shape the bridge mishandles (new beta blocks, tool variants) | §4's table is the contract; unknown block types fail **closed** — an anthropic-shaped 400 naming the unrecognized block, never a silently-mistranslated request |
| Claude Code updates change its dialect | the claude-api surface is stable and small; the fixture tests pin what we translated and break loudly when claude's binary changes what it sends is *not* testable from here — the 400-name-the-block failure keeps a drift visible at first request instead of mysterious |
| Loopback port collision inside the jail | the manifest URL owns the port (one writer); 8214 chosen clear of baked services; a collision refuses the boot via the witness, not via a mystery failure |
| The bridge becomes a de-facto gateway (scope creep) | §7 is the fence; a gateway is a different doc |
| Key exposure grows a new channel | the key rides the 0600 file + boot-time read only; `ps` shows no key; the argv exposure that already exists (claude's token) is unchanged, not extended |

## Open Questions

1. 💬 **OQ-1: The dependency shape — B (standalone + enforced endpoint) or A (fold-in)?**

   The maintainer's sketch is B-shaped ("a proxy pack, and cerebras depends on it"), and
   §3 proposes B. But A is the OQ-A10 precedent and is genuinely simpler: no enforcement
   rule, no refusal state, misconfiguration unrepresentable.

   _Leaning:_ **B.** The precedent folded a *structural* dependency (claude's own oauth);
   this one is a *shared* capability whose second consumer is a when, not an if. The
   refusal wording cost is paid once; the duplication cost of A is paid by every future
   provider pack forever.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-2: New contribution kind (`kind: service`) or loophole-manifest reuse?**

   The maintainer: "I don't love calling it a loophole." The machinery fit is real —
   `JailDaemon`, endpoint file, witness, `restart` policy all exist in the loophole
   pipeline (loopholes.go:75-146) — but a loophole manifest carries host-grant vocabulary
   (`intercepts`, `broker_ip`, `ca_cert`, host bind-mounts) that a bridge leaves entirely
   empty, and the security review a loophole *means* does not apply.

   _Leaning:_ **loophole-manifest reuse for v1, with the misnomer recorded in the
   manifest's own description** ("a wire bridge, not a loophole: see
   docs/design/wire-bridge.md §2") — the split into `kind: service` (an in-jail daemon
   contribution with no host grant) is cheap to do the day a *second* non-loophole daemon
   exists, and expensive to design correctly today with one example. Dissent worth
   having: the kinds list is vocabulary, and vocabulary accreted "temporarily" is how
   `wire_api`'s four-value enum happened.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-3: The bridge's default listen port — carry it in the cerebras manifest URL
   (8214 proposed) or allocate dynamically and rewrite the endpoint file only?**

   A fixed manifest-borne port keeps the URL a single-sourced provider fact (§3.1) and
   the derive untouched; a dynamic port survives collisions but needs the URL composed
   at launch time — which the manifest cannot do, and which would push port knowledge
   into the derive. Jail loopback is per-namespace, so collisions are limited to baked
   services.

   _Leaning:_ **fixed, manifest-borne, 8214** — collisions are near-impossible in a
   fresh namespace and fatal-by-witness when they happen, which is the good failure.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-4: `count_tokens` — local estimate, or refuse the endpoint?**

   Claude Code calls it for context accounting. A chars/4 estimate is trivially wrong on
   code; refusing (404) makes claude fall back to its own estimator (which it has, and
   which is also an estimate); proxying to nothing upstream is always possible.

   _Leaning:_ **local estimate (chars/4), stated as approximate** — it matches the
   precision of the alternative, costs one function, and keeps claude's auto-compact
   math on a path that exists.

   **Answer:**
   > _(empty — fill in when decided)_

5. 💬 🤷 **OQ-5: The provider option name for reasoning depth, if any.**

   qwen-3.8-27b defaults `reasoning_effort: high`, which burns tokens unattended. The
   bridge could honor a provider option (`reasoning_effort`) and cerebras could declare
   a default — or the bridge can leave the upstream default alone and stay out of it.

   _Leaning:_ **leave it alone in v1** — one more option is one more promise, and
   "medium is better than high for agent loops" is a measurement nobody has made yet.
   Easy to add; annoying to retract.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

| ID | Ruling | Date | Settled in |
| :--- | :--- | :--- | :--- |
| WB-D1 | The bridge translates exactly one protocol pair: anthropic Messages ↔ openai-chat-completions | 2026-09-04 | §2, §7 |
| WB-D2 | The URL is a provider fact (manifest `endpoints.anthropic.base_url`), the port lives only there, and the claude derive is untouched | 2026-09-04 | §3 |
| WB-D3 | Enforcement is a host-side preflight refusal naming the pack — never a silent dead URL | 2026-09-04 | §3 |
| WB-D4 | Inbound auth: none; the jail is the boundary. Outbound key: the 0600 `yolo-user-env.sh`, read once at boot | 2026-09-04 | §4, §5 |
| WB-D5 | Upstream reasoning content is dropped, not surfaced as thinking blocks; unknown block types fail closed with a named 400 | 2026-09-04 | §4, §9 |
| WB-D6 | `strict: true` is never sent upstream — claude's schemas contain what qwen's strict mode rejects | 2026-09-04 | §4 |
| WB-D7 | Build in-repo, stdlib-only. Runtime axis already ruled: Go/Rust over Python (local-model-endpoints.md, 2026-08-20); Bifrost is a framework where a shim is needed; the four community `claude-code-proxy` repos are Python/dormant/wrong-problem | 2026-09-04 | §8 |
| WB-D8 | cerebras declares `context_window: "65536"` when this ships (reversing cerebras-doc D-4's reasoning for claude-only options, not its method) | 2026-09-04 | §6 |
