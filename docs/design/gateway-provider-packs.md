---
title: "Gateway packs expose stable endpoints while users curate selectable models"
date: 2026-09-15
status: in-review
tags: [providers, packs, openrouter, kilo, models]
summary: "A built design for opt-in OpenRouter and Kilo packs whose model lists are supplied by user configuration. One question is open: OQ-GP4, whether the Kilo special-casing a later change added to the pi and claude derives stays."
vantage:
  status-chip: true
---

# Gateway packs expose stable endpoints while users curate selectable models

**Status:** DESIGN, 2026-09-26 — one question is open, [OQ-GP4](#OQ-GP4): whether the Kilo
special-casing in the pi and claude derives stays. Everything else was BUILT on 2026-09-15 and
re-checked against the tree 2026-09-24 — MEASURED: the two
manifests, the codex credential-field fix and the per-agent projections are pinned by
`internal/entrypoint/providerderive_test.go`. UNMEASURED: no live OpenRouter or Kilo run — no
agent session through either gateway is recorded, the one recorded request being a read of
Kilo's model catalog on 2026-09-20 ([providers.md](../reference/providers.md#per-agent-delivery)).
**NOT A GRADUATION CANDIDATE**: two of this doc's rulings were contradicted by a later change,
and [OQ-GP4](#OQ-GP4) must be ruled before its
durable half can move to the reference tree (the warning below). **Amended 2026-09-25:**
[OQ-GP2](#decision-ledger)'s *"ship no models"* is narrowed by
[OQ-BR3](model-lists-and-pickers.md#OQ-BR3) (the ledger note below); this doc's gateway packs
still ship no models.

> [!WARNING]
> **`caaaae1b` added hard-coded Kilo policy to two agent derives, against
> [§3](#3-failure-and-safety-rules) and [§4](#4-what-this-does-not-propose).** `packs/pi/derive.lua`
> and `packs/claude/derive.lua` now detect Kilo by provider name **or** by a substring of its base
> URL, rewrite a bare `deepseek-`-prefixed model id into a slash-qualified one, and supply a
> hard-coded context window for such ids when the provider declares none. `packs/pi/derive.lua`
> additionally treats the selected profile's `model` option as a **literal model id** when the user
> declared no aliases at all. `packs/claude/derive.lua` does the same **for every provider**, not
> only Kilo: a profile `model` option naming no alias, in a map with no `default`, becomes the
> model id.
>
> That last one is the direct contradiction: [§3](#3-failure-and-safety-rules) says *"an alias
> missing from the map writes no selection, rather than substituting a sole model or a gateway
> default"*, and for Kilo it now does. [§4](#4-what-this-does-not-propose)'s *"model names … are
> never hard-coded"* survives only on the technicality that the hard-coding is in a pack rather
> than in core. None of it carries the provenance comment
> [`providers.md`](../reference/providers.md#derives-the-delivery-mechanism) requires of a derive's
> gateway-specific vocabulary.
>
> **What is owed:** [OQ-GP4](#OQ-GP4) — whether the Kilo special-casing stays (in which case
> [§3](#3-failure-and-safety-rules) and [OQ-GP2](#decision-ledger) are amended and the behaviour is
> documented with its provenance) or goes. Until then this doc describes a system that is not
> there, and graduating it would publish that description as evergreen.
>
> **One thing has changed under it since.** Per-model facts became declarable on 2026-09-20
> (`b16fa0aa`): a `models.<alias>` value may be an object carrying `context_window`,
> `max_tokens`, `cost` and more, and [`providers.md`](../reference/providers.md#per-agent-delivery)'s
> worked example is a Kilo row. So the hard-coded context window no longer has to live in a
> derive — a user's curated map can state it.

> **In short.** OpenRouter and Kilo are provider packs, not special cases: each
> contributes stable endpoint and credential facts, while a user's finite model
> map becomes the only catalog yolo renders and profiles select its defaults.

**Why it matters.** Gateway catalogs change continually; shipping them would make
yolo's defaults stale and expose a noisy, unchosen set in agent pickers.

**The shape.** Two CLI-less packs compose ordinary provider entries into the
existing provider table, which every agent pack derives into its own dialect.

**Cost.** A user must write model aliases before a profile can select a default.

**Needs your ruling:** [OQ-GP4](#OQ-GP4).

**Reads with:** [`gateway-providers.md`](../research/gateway-providers.md) (protocol evidence)
and [`providers.md`](../reference/providers.md) (the existing system). The implementation plan
is retired; its one trap the reference lacked, codex's `env_key` credential field, is now in
[`providers.md`](../reference/providers.md#per-agent-delivery).
Also [`wire-bridge-gateway.md`](wire-bridge-gateway.md), whose direction
[DIR-WG1](wire-bridge-gateway.md#DIR-WG1) (routing every agent's model traffic through the wire
bridge) names OpenRouter model filtering as its motivating case, and whose
[OQ-WG3](wire-bridge-gateway.md#OQ-WG3) asks whether a curated OpenRouter or Kilo map
([§2](#2-a-selected-set-not-a-synchronized-catalog)) becomes an enforced allowlist rather than
only the set a picker shows.

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

## Open Questions

1. 💬 <a id="OQ-GP4"></a>**[OQ-GP4](#OQ-GP4): does the Kilo special-casing in the pi and claude derives stay?**
   `caaaae1b` (2026-09-16) made both derives detect Kilo by provider name or base-URL substring,
   rewrite a bare `deepseek-` id to `deepseek/…`, supply a context window for such ids, and — in
   pi for Kilo, in claude for every provider — use an unmapped profile `model` option as a
   literal id. That contradicts [§3](#3-failure-and-safety-rules)'s "an alias missing from the
   map writes no selection" and [OQ-GP2](#decision-ledger)'s "ship no models", and none of it
   carries the provenance comment a derive's gateway vocabulary needs. The stakes: whether this
   doc amends its rulings to describe what ships, or the derives lose behaviour a Kilo user may
   now rely on.

   <!-- vantage: oq id=OQ-GP4 leaning="Split it. Keep the literal-id fallback, since a profile naming an exact id is a user's explicit choice, and amend §3 to say so for every provider. Move the context window out of the derives and into the user's curated map, which per-model facts (b16fa0aa) now make possible. Keep the deepseek- rewrite only with a provenance comment naming Kilo's catalog and the date, or drop it." -->

   _Leaning:_ **Split it.** Keep the literal-id fallback — a profile naming an exact id is the
   user's explicit choice — and amend [§3](#3-failure-and-safety-rules) to say so for every
   provider. Move the context window out of the derives into the curated map, which per-model
   facts now allow. Keep the `deepseek-` rewrite only with a provenance comment naming Kilo's
   catalog and a date, or drop it.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-GP1 | Ship two packs rather than one generic gateway pack; endpoint capability differs by provider. | 2026-09-15 | [§1](#1-the-provider-facts) | shipped |
| OQ-GP2 | Ship no models; users curate the finite selectable map and profiles choose defaults. **Amended 2026-09-25 by [OQ-BR3](model-lists-and-pickers.md#OQ-BR3):** where an agent cannot fall back to its own default by yolo having no opinion, yolo does pick model ids, shipped in a built-in pack that comes with yolo rather than in core (*"I do want to pick these … a built-in pack (it's not in core per se but it comes with [yolo]) with these that tries to pick these different models"*). Where an agent can default, yolo still ships nothing, and the user still curates the selectable map. The OpenRouter and Kilo packs are unchanged. Whether a pack may also carry a list an org reshapes is [OQ-BR12](model-lists-and-pickers.md#OQ-BR12), still open. | 2026-09-15; amended 2026-09-25 | [§2](#2-a-selected-set-not-a-synchronized-catalog); amendment in [`model-lists-and-pickers.md`](model-lists-and-pickers.md#OQ-BR3) | shipped; amendment not built |
| OQ-GP3 | Kilo is Chat Completions only until a documented, verified Responses route exists; Claude and Copilot reuse the existing wire bridge. | 2026-09-15 | [§3](#3-failure-and-safety-rules) | shipped |
