---
title: "The cerebras pack, and closing the copilot delivery gap"
date: 2026-09-04
status: accepted
tags: [packs, providers, profiles, cerebras, copilot, delivery]
summary: "A second purely-declarative provider pack (Cerebras, one key, qwen-3.8-27b as the agentic main), plus the per-agent delivery audit the maintainer's ask surfaced: copilot is the one agent that can receive a provider and doesn't, and its BYOK is env-var-only — a yolo.env derive closes it. agy is unwireable and recorded as such."
---

# The cerebras pack, and closing the copilot delivery gap

**Status:** ACCEPTED, 2026-09-04. The zai-side fix this workstream also produced (the
recommended-env alignment and the wire-true alias correction) shipped as `8631caeb` and
`04b3f039`; this doc designs what remains: the pack itself and the copilot derive.

**The want** *(the maintainer's words, 2026-09-04, lightly compressed)*: "a pack for
cerebras for using it as the main agentic model… qwen3.8-27b is now available for real
agentic. I want a pack to hook up to this with just a key. Do we support injecting model
lists into all agents? We should. And figure out which are possible on cerebras."

## The research (all measured or source-verified 2026-09-04)

### Cerebras the service

- **Base URL** `https://api.cerebras.ai/v1`; **`POST /v1/chat/completions`** is the
  primary surface, OpenAI-client compatible. **No `/v1/responses`** (absent from the
  OpenAPI spec, the doc index, the changelog, and the compatibility page — four primary
  sources). **No Anthropic-compatible endpoint** (same four; third parties route Claude
  Code through translating proxies for exactly this reason).
- **Auth**: `Authorization: Bearer`, conventional env `CEREBRAS_API_KEY`, keys from
  cloud.cerebras.ai.
- **Models** (live `/public/v1/models`): `qwen-3.8-27b` — public since **2026-09-03**,
  agentic-coding-tuned, parallel tool calls + `strict: true` schemas + structured
  outputs, vision, reasoning default `high` (`reasoning_effort` accepted), 64K context
  free / 128K paid, ~1500 tok/s; `gpt-oss-120b` — 131K context, ~3000 tok/s, but
  `parallel_tool_calls: false` and a documented "may invoke tools it wasn't given"
  warning; `gemma-4-31b` — removed from public endpoints 2026-09-03 (the live catalog
  lags).
- **No official MCP server.** `cerebras-code-mcp` is a community npm package exposing
  one `write` tool hardcoded to a deprecated model — not a channel into the lineup. (The
  maintainer's "many of their models are through the mcp server" premise does not hold
  for the official surface.)
- **Cerebras Code** is a $50–200/mo subscription (currently sold out), BYO-editor; the
  docs' own integrations index lists **OpenCode** among the officially supported tools.

### The delivery audit ("do we support injecting model lists into all agents?")

No — four of six. The reference doc's per-agent table (providers.md §"Per-agent
delivery") is the authority; what it does not say in so many words is the gap:

| Agent | Provider delivery today | Can ride Cerebras | Why |
| :--- | :--- | :--- | :--- |
| claude | env derive | **no** | no anthropic endpoint exists to point `ANTHROPIC_BASE_URL` at; a translating proxy is a different product than a provider fact |
| codex | config derive | **no** | codex speaks `responses` only; Cerebras serves chat completions only — the same unwireable pairing as codex+zai (providers.md's recorded warning, second instance) |
| pi | config derive | **yes** | `openai-chat-completions` → pi's `openai-completions` dialect |
| opencode | config derive | **yes** | base-URL only; and Cerebras's own docs bless OpenCode as a client |
| copilot | **none** | **yes, once this doc ships** | BYOK is env-var-only and first-class: `COPILOT_PROVIDER_BASE_URL` activates it, `COPILOT_PROVIDER_TYPE ∈ {openai, azure, anthropic}`, `COPILOT_PROVIDER_WIRE_API ∈ {completions, responses}`, `COPILOT_MODEL` required, `COPILOT_PROVIDER_API_KEY` optional — verified from copilot 1.0.48's own `help providers` topic (2026-08-20, local-model-endpoints.md) and re-confirmed against GitHub's BYOK docs (2026-09-04) |
| agy | **none** | **no — unrepresentable** | Google-locked: `modelProvider` accepts exactly `gemini`; the only custom-endpoint hook (`GOOGLE_GEMINI_BASE_URL`) speaks the Gemini protocol; Claude/GPT models in its picker ride Google's backend, not custom endpoints |

## The design

### 1. `packs/cerebras` — the second purely-declarative pack

Exactly zai's shape: one `kind: "provider"` + one `kind: "profile"` + a README; no CLI,
no loophole, no surfaces.

- `endpoints.openai`: `base_url: https://api.cerebras.ai/v1`,
  `wire_api: openai-chat-completions`. No anthropic endpoint — declaring one would be a
  lie about the service, and the derives already compose nothing for an absent key.
- `api_key_env_name: CEREBRAS_API_KEY` — the conventional spelling; the value crosses
  the same channel zai's does (env_sources or the invoking environment), never a manifest.
- `models`: `default: qwen-3.8-27b`. **WIRE-TRUE, and only the one alias.** Not
  `gpt-oss-120b`: the hallucinated-tool-call warning plus `parallel_tool_calls: false`
  disqualify it from any tier an agent would use unattended, and a `fast` alias that
  invents tool calls is worse than no `fast` alias. A user who wants it can merge it in
  user config (`providers` is a merged-scope key); the README says so.
- `options`: `model: "default"`. No `context_window`/`api_timeout_ms`: those exist for
  claude's benefit (auto-compact, request ceiling), and claude cannot ride Cerebras —
  declaring them would be dead weight that reads as promise. Cerebras is fast enough
  (~1500 tok/s) that no timeout knob is warranted.
- Profile `cerebras`, provider `cerebras`. Name refuses `=` (packdecl rule), and it
  doesn't contain one.

The pack's README carries the delivery table above (with copilot's row reflecting the
derive below) and the credential-refusal contract: select the pack without
`CEREBRAS_API_KEY` hydrated and the launch preflight refuses (catalog membership, OQ-PT4).

### 2. `packs/copilot/derive.lua` — the fifth delivery

A `yolo.env("copilot", …)` producer, same rules as claude's:

- **Endpoint resolution**: prefer `endpoints.anthropic` (copilot's `anthropic` type);
  else `endpoints.openai` (type `openai`). The `azure` type has no canonical spelling
  and no yolo provider would declare one — nothing to do.
- **Dialect map** (canonical → copilot's spellings, with provenance in the comment, per
  house style): `anthropic` → `COPILOT_PROVIDER_TYPE=anthropic` (no WIRE_API);
  `openai-chat-completions` → `TYPE=openai`, `WIRE_API=completions`;
  `openai-responses` → `TYPE=openai`, `WIRE_API=responses`; absent wire_api → copilot's
  own default (`completions`). Unspeakable: nothing (copilot speaks both surviving
  protocols — the one agent for which no canonical value is unspeakable).
- **Model**: `COPILOT_MODEL` = the id under the alias the profile's `model` option
  names, falling back to `default` — the same resolution rule every other derive uses.
  Copilot refuses BYOK without a model, so a provider declaring no models composes
  nothing at all (activation without a model is a copilot-side refusal; yolo should not
  arm it).
- **Key**: `COPILOT_PROVIDER_API_KEY` from the hydrated `api_key`, when present.
- **WIRE_MODEL note**: the ids are sent verbatim (copilot has no `[1m]`-style syntax),
  which the wire-true alias rule (04b3f039) already guarantees.

Census consequences: copilot's README row in every provider pack's delivery table stops
saying "no provider delivery"; providers.md's per-agent table gains the copilot row and
an agy verdict line; the zai README's "copilot and agy ship no provider delivery"
paragraph splits (copilot now does; agy still doesn't and never can).

### 3. What is deliberately NOT in scope

- **A claude↔Cerebras translation proxy** (Bifrost-style): claude riding Cerebras needs
  an anthropic-wire translator, which is a running service, not a provider fact. If
  wanted later it is a loophole-shaped pack, decided by its own doc. OQ-1 below.
- **gpt-oss-120b / gemma-4-31b aliases**: see the models ruling above.
- **agy delivery**: unrepresentable; recorded, not worked around. Revisit if agy ever
  ships an openai-compatible provider hook.

## Open questions

| OQ | Question | Status |
| :--- | :--- | :--- |
| OQ-1 | Should yolo ever ship a claude-wire translation proxy so claude can ride chat-completions-only providers? | **In design** — answered by [`wire-bridge.md`](wire-bridge.md) (2026-09-04): a `wire-bridge` pack included through new real pack-dependency vocabulary (`needs` + `when_bins`, ruled by the maintainer that day), and revisits D-4 below (the `context_window` option becomes live the day claude can ride the bridge) |
| OQ-2 | The claude derive emits `ANTHROPIC_AUTH_TOKEN` even when the provider has no anthropic endpoint (recorded in zai-plumbing §3) — selected-for-claude + openai-only provider sends a wrong-token credential to api.anthropic.com. Gate the token on the URL? | OPEN — pre-existing recorded behavior, not this doc's change; flagged because cerebras is the first pack where a user might plausibly `-p cerebras` a claude-selecting launch |
| OQ-3 | Cerebras's free tier is 5 req/min — thin for an agent loop. Does the pack README say so? | Resolved in the README: yes, with the Developer-tier numbers beside it |

## Decision ledger

| ID | Ruling | Why |
| :--- | :--- | :--- |
| D-1 | cerebras ships ONE model alias, `default: qwen-3.8-27b` | it is the only public model fit for unattended agentic use; a hallucination-prone `fast` tier is a footgun, and user config can add aliases where the pack refuses |
| D-2 | copilot's delivery is env-only (`yolo.env`), no config surface | copilot's BYOK is env-var-only by its own `help providers` topic — there is no file key to write |
| D-3 | copilot's derive prefers the anthropic endpoint when both exist | zai is the worked example: the anthropic route is the richer surface (tier translation), and type=anthropic is copilot's first-class spelling for it |
| D-4 | no `context_window`/`api_timeout_ms` on cerebras | both options exist solely as claude-derive inputs; claude cannot ride cerebras, and dead options read as promises |
