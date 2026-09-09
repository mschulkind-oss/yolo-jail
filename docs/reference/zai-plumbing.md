---
status: current
verified: 2026-09-09
verified_commit: 41dde711
covers:
  - packs/zai/
  - packs/codex/derive.lua
  - packs/pi/derive.lua
  - packs/opencode/derive.lua
  - internal/cli/run/providerpreflight.go
  - internal/cli/run/zaipack_test.go
tags: [providers, profiles, zai, resolution, endpoints]
---

# The zai pack — one provider, every agent, as a worked example

**Status:** CURRENT as of 2026-09-09, verified against `41dde711`.

`packs/zai` is the **first real consumer** of the provider/profile pair: a pack whose whole content
is declarative facts about one service, so that a user's own config shrinks to an API key. Selecting
it and setting one variable is enough to fire every selected agent that can reach z.ai at GLM, by
whatever protocol that agent speaks.

This doc is the worked example. **[`providers.md`](providers.md) is the authority** for the
mechanism — how the provider table composes, what crosses to the jail, the canonical `wire_api`
vocabulary, the derives, selection, and per-agent delivery. Nothing general is restated here.

| Component | Lives in |
| :--- | :--- |
| The pack — provider facts plus its profile | `packs/zai` (`pack.json`, `README.md`) |
| Per-protocol endpoint resolution, per agent | each agent pack's `derive.lua` (`providerEndpoint`, the dialect maps) |
| The missing-credential refusal | `internal/cli/run/providerpreflight.go` |
| The shipped-pack acceptance test | `internal/cli/run/zaipack_test.go` |

**Reads with:** [`providers.md`](providers.md) (the mechanism — read it first),
[`../design/profiles-as-pack-variants.md`](../design/profiles-as-pack-variants.md) (the parent
design, and the index for that arc's `OQ-N` rulings),
[`pack-system.md`](pack-system.md) (the `provider` and `profile` kinds as pack contributions).

---

## Three different things carry the word "zai"

The schema confusion is real and it is one word doing three jobs. Separated:

| Thing | Where it lives | What it is |
| :--- | :--- | :--- |
| **the provider name** | the key in the `providers` map, or a `kind: "provider"` contribution's `name` | pure namespace. The derives iterate it and it lands in each agent's file as the provider id. Nothing resolves it; it is what other things reference. |
| **the profile's `provider` field** | a `kind: "profile"` declaration | a **reference, and mandatory**. It names which provider this selection routes to. |
| **the profile name** | the value `-p` sets | the selector. It gates `profile:`-modified contributions and reaches every selected pack's derive, whether or not that pack declared the name. |

> [!WARNING]
> **`requires_provider` is a TOMBSTONE, not a synonym for `provider`.** It is decodable and then
> refused by name, in user config too, with a message pointing at the live field. The old field was
> an *assertion* — "when this profile is active, that provider must exist and its key must be
> hydrated" — and the live one is a *reference*. Do not reintroduce the assertion spelling.

## The z.ai service facts

The **GLM Coding Plan** — z.ai's coding-plan subscription; z.ai is Zhipu AI's international brand
([docs.z.ai](https://docs.z.ai/devpack/tool/others)) — speaks **both** wire protocols, each on its
own base URL, and **one API key from the z.ai console serves both.** That is the property the whole
`endpoints`-by-protocol schema exists for: one provider, one key, any shape an agent needs.

**The OpenAI route speaks chat-completions only.** Measured with an authenticated probe:
`POST /v4/responses` is 404 on both the plain and coding-plan routes, while
`/v4/chat/completions` returns a real completion on the same host. A keyless probe cannot settle
this — z.ai's edge 401s garbage paths too, authenticating before routing.

> [!WARNING]
> **A provider's HTTP surface is not an agent's config vocabulary, and measuring one says nothing
> about the other.** The probe above was once read as evidence for a *derive default*, which it is
> not: an omitted `wire_api` means the agent's own single accepted value where it has one, and the
> derive's choice where it does not. The canonical vocabulary is protocol-shaped names nobody
> speaks, translated per agent and never passed through — see
> [`providers.md`](providers.md#the-canonical-wire_api-vocabulary).

The URLs, the model aliases and the option defaults are all in
[`packs/zai/pack.json`](../../packs/zai/pack.json), which is the only place they are stated. Model
names move on the vendor's cadence; the pack is what a user's overrides compose over.

## The resolution table

One provider, endpoints keyed by protocol, and every agent resolves to the endpoint for a protocol
it speaks. Resolution is a **fixed table, not a pack per agent** — which is what keeps this from
being an N×M problem:

| Agent | Resolves | For zai |
| :--- | :--- | :--- |
| claude | `anthropic` | the anthropic endpoint, as process env from the claude pack's env derive |
| pi | `openai` | catalog entry plus selection |
| opencode | `openai` | catalog entry plus selection |
| copilot | `openai` | process env from the copilot pack's env derive |
| **codex** | `openai` | **nothing at all** — see below |

**codex gets no zai entry, and that is the resolution rule working rather than failing.** z.ai's
openai route speaks chat-completions; codex accepts `responses` only. The canonical value therefore
has no codex spelling, and a derive emits **nothing** for a canonical value its agent cannot spell
rather than inheriting a default that would hide the mismatch behind a 404.

Three closure rules the schema owes, and the reason each is per-agent rather than global:

1. **`base_url` is valid only alone.** It is the single-protocol shorthand. `endpoints` and
   `base_url` together is a validation error whose message points at `endpoints`; **neither** is
   also fine — a provider may exist only to be named.
2. **The derive gate is the provider's URL FOR THE PROTOCOL THAT AGENT RESOLVES**, never
   `base_url` alone. Gating on `base_url` silently dropped an `endpoints`-only provider from every
   catalog. Name the reason per agent, because it is not one reason: for codex it is **incapacity**
   — its dialect map has no `anthropic` row. For pi it is **resolution, not incapacity** — pi does
   speak the anthropic protocol, and still gets no row from an anthropic-only provider because it
   resolves the `openai` endpoint key. opencode consumes no `wire_api` at all, so for it the
   resolution key is the whole story.
3. **The derives write a CATALOG (presence), not a choice.** Selection — each agent's
   use-this-one field — is a separate act. Presence is not selection: without a profile selected,
   the catalogs still contain zai and nothing routes to it.

## What a user actually does

The whole setup, and its two halves are deliberately in different files:

```jsonc
// ~/.config/yolo-jail/config.jsonc — user scope
{ "packs": [ "…", "zai" ] }
```

```bash
# ~/.config/yolo-jail/env — untracked, 0600, reached through env_sources
ZAI_API_KEY=<key>
```

Then `yolo -p zai` (or `-p claude=zai -- claude`) routes every selected agent that can reach z.ai.
Overrides — a different region, an extra model alias — are lines of `providers.zai` in user config,
composed **over** the pack's facts; authoring a whole provider is never required.

**A selected `packs/zai` with no key refuses the launch outright**, at both notches, rather than
starting an agent that will 401 on its first call. That makes the shipped pack set no longer
credential-silent, which is recorded as the one deliberate exception by
`TestShippedPacksRequireNoCredential`.

## What this does not license

- **Not two provider entries sharing one key.** `zai` plus `zai-claude` was a bridge and is ruled
  out as an end-state: one provider and one key must produce any shape needed.
- **Not an env reference form in user-written config.** The credential's *name* crosses; the value
  is hydrated per derive invocation. yolo's own `${VAR}` expansion was removed from config
  rendering deliberately — ambient process env must not be a config-rendering input.
- **Not a special case for claude.** The pack ships service facts; every agent's own derive
  translates them. An earlier shape carried a claude-specific `config-overlay` bridge and an
  `env_shape` block on the provider; both are gone, and `env_shape` is now an ordinary unknown key.
- **Not a claim about any other GLM route.** The measurement above covers the two routes named.

## Why it's this way

Rulings a future change would otherwise undo, kept with their original IDs — the parent design and
code comments cite them, and this appendix is where they resolve.

| Ruling | Why it holds |
| :--- | :--- |
| <a id="oq-z1"></a>[**OQ-Z1**](#oq-z1) — z.ai's OpenAI route speaks **chat-completions only** | Authenticated probe on both routes; a keyless probe cannot settle it because the edge 401s nonexistent paths. Its consequence is that z.ai's openai endpoint has no codex spelling. ⚠ Its *derive-default* half is superseded by [`OQ-Z7`](#oq-z7): the probe measured z.ai's HTTP surface, which is not codex's config vocabulary. |
| <a id="oq-z2"></a>[**OQ-Z2**](#oq-z2) — **one provider, one API key, any shape needed** | The two-entry bridge duplicated a credential and a set of model aliases to express what is one service. `endpoints`-by-protocol is the required end-state, and the shape axis lives inside the entry. |
| <a id="oq-z3"></a>[**OQ-Z3**](#oq-z3) — **no env reference form; user-written env stays literal-only** | Ambient process env must not be a config-rendering input, so yolo writes references verbatim and the consumer resolves them. An alias costs one literal line in the same `0600` file. ⚠ Note the boundary: the *derive* `ctx` is not secret-free — the env producer's copy of the providers table is hydrated — but the composed table the launch relays is, pinned by `TestAgentEnvHydratesOnlyTheDerivedCopy`. |
| <a id="oq-z4"></a>[**OQ-Z4**](#oq-z4) — **Claude Code honors the settings `env` block before its first API call** | Measured against a controlled listener with inherited `ANTHROPIC_*` scrubbed: a settings-only base URL produced traffic identical to the process-env control. This is what makes the settings block a real delivery channel and not just a convenience. |
| <a id="oq-z5"></a>[**OQ-Z5**](#oq-z5) — a pack patches another pack's surface through `config-overlay`, never through a profile's own config body | A profile's config patches fold only into surfaces the same pack **owns**, and `packs/zai` owns no claude surface. Found as a live bug in the flagship manifest by a completeness audit, and pinned by `packload`'s fold-note test — a patch written for a surface the pack does not own is silently dropped. |
| <a id="oq-z6"></a>[**OQ-Z6**](#oq-z6) — the three `endpoints` closure rules | Each closes a hole the schema sketch left: an ambiguous coexistence, a gate that dropped endpoints-only providers, and a catalog mistaken for a selection. |
| <a id="oq-z7"></a>[**OQ-Z7**](#oq-z7) — **`wire_api` is translated, never passed through; `openai-chat` is retired, not renamed** | Each derive owns a provenance-bearing dialect map and emits nothing for a canonical value its agent cannot spell. Reopening the enum as a free string restores the pass-through failure the closed vocabulary closed. |

## Current values

Verified at `41dde711`. The prose above explains what each of these is for; this table is the only
place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| The pack's provider facts — base URLs, wire protocols, model aliases, option defaults | one manifest, and the only statement of them | [`packs/zai/pack.json`](../../packs/zai/pack.json) |
| Credential variable | `ZAI_API_KEY`, by name only | the pack's `api_key_env_name` |
| Provider entry keys | `base_url`, `endpoints`, `wire_api`, `api_key_env_name`, `models`, `region`, `capabilities`, `options` | `config.knownProviderKeys`; `yolo config-ref` for the user-facing schema |
| Profile body | `{name, provider}` and nothing else | `packdecl` (`Contribution.Provider`; `RequiresProvider` is the tombstone) |
| Selection spellings | `-p <name>` / `-p <cli>=<name>`, and `use_profiles` in user config | [`providers.md`](providers.md#per-agent-delivery) |
