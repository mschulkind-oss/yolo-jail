---
title: "Gateway packs expose stable endpoints while users curate selectable models"
date: 2026-09-15
status: accepted
tags: [providers, packs, openrouter, kilo, models]
summary: "A settled design for opt-in OpenRouter and Kilo packs whose model lists are supplied by user configuration."
vantage:
  status-chip: true
---

# Gateway packs expose stable endpoints while users curate selectable models

**Status:** BUILT, 2026-09-15 — MEASURED: the two manifests, the codex credential-field fix and
the per-agent projections are pinned by `internal/entrypoint/providerderive_test.go`. **NOT A
GRADUATION CANDIDATE**: two of this doc's rulings were contradicted by a later change and it needs
a ruling before its durable half can move to the reference tree (the warning below).

> [!WARNING]
> **`caaaae1b` added hard-coded Kilo policy to two agent derives, against
> [§3](#3-failure-and-safety-rules) and [§4](#4-what-this-does-not-propose).** `packs/pi/derive.lua`
> and `packs/claude/derive.lua` now detect Kilo by provider name **or** by a substring of its base
> URL, rewrite a bare `deepseek-`-prefixed model id into a slash-qualified one, and supply a
> hard-coded context window for such ids when the provider declares none. `packs/pi/derive.lua`
> additionally treats the selected profile's `model` option as a **literal model id** when the user
> declared no aliases at all.
>
> That last one is the direct contradiction: [§3](#3-failure-and-safety-rules) says *"an alias
> missing from the map writes no selection, rather than substituting a sole model or a gateway
> default"*, and for Kilo it now does. [§4](#4-what-this-does-not-propose)'s *"model names … are
> never hard-coded"* survives only on the technicality that the hard-coding is in a pack rather
> than in core. None of it carries the provenance comment
> [`providers.md`](../reference/providers.md#derives-the-delivery-mechanism) requires of a derive's
> gateway-specific vocabulary.
>
> **What is owed:** a ruling on whether the Kilo special-casing stays (in which case
> [§3](#3-failure-and-safety-rules) and [OQ-GP2](#decision-ledger) are amended and the behaviour is
> documented with its provenance) or goes. Until then this doc describes a system that is not
> there, and graduating it would publish that description as evergreen.

> **In short.** OpenRouter and Kilo are provider packs, not special cases: each
> contributes stable endpoint and credential facts, while a user's finite model
> map becomes the only catalog yolo renders and profiles select its defaults.

**Why it matters.** Gateway catalogs change continually; shipping them would make
yolo's defaults stale and expose a noisy, unchosen set in agent pickers.

**The shape.** Two CLI-less packs compose ordinary provider entries into the
existing provider table, which every agent pack derives into its own dialect.

**Cost.** A user must write model aliases before a profile can select a default.

**Needs your ruling:** the Kilo special-casing above.

**Reads with:** [`gateway-provider-packs-plan.md`](gateway-provider-packs-plan.md)
(the implementation hand-off), [`gateway-providers.md`](../research/gateway-providers.md)
(protocol evidence), and [`providers.md`](../reference/providers.md) (the existing system).

---

## 1. The provider facts

`openrouter` contributes `OPENROUTER_API_KEY`, an `openai` endpoint at
`https://openrouter.ai/api/v1` using `openai-responses`, and an `anthropic`
endpoint at `https://openrouter.ai/api`. `kilo` contributes `KILO_API_KEY`, an
`openai` endpoint at `https://api.kilo.ai/api/gateway` using
`openai-chat-completions`. It contributed the wire bridge's local `anthropic`
endpoint at `http://127.0.0.1:8216` as well when this was written; that half is
**gone** — protocol resolution moved every bridge address into
[`packs/wire-bridge`](../../packs/wire-bridge/pack.json)'s own `adapter`
contributions, and `8216` was eliminated rather than relocated, because kilo now
shares the single `openai → anthropic` adaptation
([`../reference/protocol-resolution.md`](../reference/protocol-resolution.md)).

Each pack contributes a same-named profile. Both declare the existing `model`
profile option with no default, so selecting a profile does not silently select
or invent a model.

The provider table's existing per-agent gates determine reachability:

| Pack | Claude | Codex | Pi | OpenCode | Copilot |
| :--- | :---: | :---: | :---: | :---: | :---: |
| OpenRouter | yes | yes | yes | yes | yes |
| Kilo | yes, via bridge | no | yes | yes | yes, via bridge |

`agy` has no provider extension point and remains out of scope.

## 2. A selected set, not a synchronized catalog

A **curated model map** *(coined here)* is the user-provided map from a stable
local alias to a gateway model id. It is not automatic model discovery, a
gateway-wide allowlist, or a guarantee that a model supports every agent's
protocol. The user owns it because its membership is a spending and usability
choice; yolo only projects it.

Users add the map under the selected provider's existing `models` field and
make named profiles that point their `model` option at an alias. The model map is
the full selectable set for Pi and OpenCode. The selected profile supplies the
default for every agent that has a route and recognizes that profile's CLI name.
No profile activation means no selection keys are written, preserving each
agent's interactive choice under the existing selection rules.

```jsonc
{
  "packs": ["openrouter", "kilo"],
  "providers": {
    "openrouter": {
      "models": {
        "coding": "~anthropic/claude-sonnet-latest",
        "reasoning": "~openai/gpt-latest"
      }
    },
    "kilo": {
      "models": { "economy": "kilo-auto/efficient" }
    }
  },
  "profiles": {
    "router-coding": { "provider": "openrouter", "model": "coding" },
    "kilo-economy": { "provider": "kilo", "model": "economy" }
  },
  "use_profiles": { "pi": "router-coding", "opencode": "kilo-economy" }
}
```

## 3. Failure and safety rules

- Missing either selected pack's key follows the existing credential preflight
  and refuses the launch before an agent starts.
- An absent model map still renders the provider where the agent has a catalog,
  but a selected profile writes no incomplete model selection.
- An alias missing from the map writes no selection, rather than substituting a
  sole model or a gateway default.
- A Kilo profile selected for Codex is inert: no undocumented Responses
  protocol is emitted. Claude and Copilot use the existing wire bridge, which
  translates their Anthropic Messages traffic to Kilo Chat Completions.
- Model names, prices, and gateway policy are never hard-coded in core or
  refreshed behind the user's back.

## 4. What this does not propose

This design does not add model discovery, provider fallback policy, cost
controls, request headers, or Kilo's non-documented Responses route. It reuses
the shipped wire bridge; it does not add a second proxy.

## 5. Done means

With a key and a curated map, selecting `openrouter` produces all five supported
agent configurations, including Codex's credential binding. Selecting `kilo`
produces Claude, Pi, OpenCode, and Copilot configurations; Claude and Copilot
reach its Chat Completions API through the existing bridge. With neither pack in
the config, the existing behavior is unchanged.

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-GP1 | Ship two packs rather than one generic gateway pack; endpoint capability differs by provider. | 2026-09-15 | [§1](#1-the-provider-facts) | shipped |
| OQ-GP2 | Ship no models; users curate the finite selectable map and profiles choose defaults. | 2026-09-15 | [§2](#2-a-selected-set-not-a-synchronized-catalog) | shipped |
| OQ-GP3 | Kilo is Chat Completions only until a documented, verified Responses route exists; Claude and Copilot reuse the existing wire bridge. | 2026-09-15 | [§3](#3-failure-and-safety-rules) | shipped |
