---
status: current
verified: 2026-09-18
verified_commit: 7da7b153
covers:
  - packs/cerebras/
  - packs/copilot/derive.lua
  - internal/cli/run/cerebraspack_test.go
  - internal/cli/run/copilotbyok_test.go
tags: [packs, providers, profiles, cerebras, copilot, delivery]
summary: "Provider reach, per agent: which of the seven shipped agent CLIs can be pointed at a third-party provider and which cannot, why the answer is decided by wire protocol rather than by configuration, copilot's env-only BYOK contract, and the Cerebras pack that is the worked example of a provider serving one protocol."
---

# Provider reach — the Cerebras pack and copilot's BYOK

**Status:** CURRENT as of 2026-09-18, verified against `7da7b153`.

**Provider reach** *(coined here)* is the question of which agent CLIs a given provider can
actually drive. It is not the same question as [provider *delivery*](providers.md#per-agent-delivery),
which is about the channel — a catalog file, a selection key, a process environment — and assumes
the pairing is possible at all. Reach is decided one level below delivery, by **wire protocol**: an
agent speaks exactly one, a provider serves some set, and no amount of configuration closes a gap
between them.

Two things make this a reference rather than a footnote in [`providers.md`](providers.md). The
reach answer is **stable per agent and unintuitive** — two of the seven shipped agent CLIs can
never ride a third-party provider, for two unrelated reasons — and the one agent whose reach
changed most recently, copilot, receives a provider through a channel no other agent uses.

The **Cerebras pack** is the worked example throughout, because Cerebras serves exactly one wire
protocol (OpenAI chat completions) and so exercises every branch of the reach table at once.

| Component | Lives in |
| :--- | :--- |
| The Cerebras provider and profile declarations | `packs/cerebras` (`pack.json`, `README.md`) |
| Copilot's provider delivery — the one `yolo.env` producer beside claude's | `packs/copilot` (`derive.lua`) |
| The catalog derives that give pi, opencode, codex and oh-omp their provider rows | `packs/pi`, `packs/opencode`, `packs/codex`, `packs/omp` (`derive.lua`) |
| The composed provider table both halves read | `internal/packload` (`providers.go`) |
| Pins on the pack as shipped, and on what copilot's BYOK composes | `internal/cli/run` (`cerebraspack_test.go`, `copilotbyok_test.go`) |

**Reads with:** [`providers.md`](providers.md) (what a provider declares, and the per-agent
delivery channels this doc's reach table sits on top of), [`wire-bridge.md`](wire-bridge.md) (the
in-jail translator that manufactures reach where the protocols do not line up), and
[`zai-plumbing.md`](zai-plumbing.md) (the first purely-declarative provider pack, whose shape
Cerebras repeats).

---

## Reach is a protocol fact, not a configuration one

A yolo derive translates config **dialects** — it turns one canonical declaration into whatever
spelling an agent's own config file wants. It cannot translate a **wire protocol**, because that is
a property of the bytes on the connection rather than of any file. So when an agent speaks
Anthropic Messages and a provider serves OpenAI chat completions, the honest answers are exactly
two: run a translator ([`wire-bridge.md`](wire-bridge.md)), or record that the pairing is
unreachable.

This is why the table below has three columns of answer and not two. "No" splits into *no, and a
bridge fixes it* and *no, and nothing can*.

## Which agents a provider can reach

Seven packs declare an agent CLI (`kind: "program"`); this is what each one can be pointed at.

| Agent | How a provider reaches it | Can ride a chat-completions-only provider |
| :--- | :--- | :--- |
| pi | catalog derive + selection keys | **yes**, natively |
| opencode | catalog derive + selection key | **yes**, natively |
| copilot | env derive only — BYOK has no config file | **yes**, natively (and through the bridge when the provider declares an `anthropic` endpoint) |
| oh-omp | catalog derive, **no selection** | **catalog only** — it gets the provider row; nothing selects it |
| claude | env derive only | **only through the wire bridge** — claude speaks Anthropic Messages, so a provider with no anthropic endpoint has nothing to point `ANTHROPIC_BASE_URL` at |
| codex | catalog derive + selection keys | **no, and no bridge helps** — codex speaks OpenAI Responses only, and the bridge translates exactly one pair, Anthropic Messages ↔ chat completions |
| agy | nothing | **no, and nothing can** — see below |

> [!WARNING]
> **`agy` is Google-locked and this is not a gap to close.** Its `modelProvider` setting accepts
> exactly one value, and its only custom-endpoint hook speaks the Gemini protocol, so a
> non-Gemini endpoint behind it is not a provider yolo declined to wire — it is a request the
> agent cannot express. The non-Google models in its picker ride Google's own backend. Do not add
> a provider derive to `packs/agy`; there is no key to write. Revisit only if agy ships an
> OpenAI-compatible provider hook.

> [!WARNING]
> **codex + a chat-completions-only provider stays unwireable, and the bridge is not the
> workaround.** The bridge's single route is Anthropic Messages → chat completions, which is the
> wrong direction for codex. This is the second recorded instance of the pairing (the first was
> codex + z.ai); treat a third as confirmation of the rule rather than as a new bug.

**oh-omp's half-reach is deliberate and is the [catalog/selection split](providers.md#per-agent-delivery)
showing through.** Its derive composes a provider row from the canonical facts and writes no
selection key, so a user who selects a profile gets the provider *available* in oh-omp and still
has to choose it inside the agent.

## Copilot's BYOK contract

Copilot is the only agent whose entire provider surface is process environment. It has no provider
directory and no config file with provider keys, so `packs/copilot/derive.lua`'s `yolo.env`
producer **is** copilot's provider delivery — there is nothing else to write.

Four properties of that contract are worth knowing before changing the producer, because each one
is a copilot-side behavior rather than a yolo choice:

- **The base URL is the sole activation gate.** Copilot ignores every other `COPILOT_PROVIDER_*`
  variable unless the base URL is set, so composing the rest without it does nothing at all.
- **A model is mandatory.** Copilot's BYOK refuses to start without one. So a provider that
  declares no resolvable model alias makes the producer compose **nothing** — arming the base URL
  alone would trade a working GitHub-authenticated copilot for a copilot-side refusal.
- **BYOK is a mode switch, not an addition.** GitHub authentication is skipped entirely once BYOK
  activates. Selecting a provider for copilot replaces its identity; it does not extend it.
- **No canonical protocol is unspeakable to copilot.** It talks both surviving protocol families,
  so unlike every other agent the producer never has to decline a provider on protocol grounds. It
  declines only when the provider names no endpoint at all.

### Endpoint preference, and why anthropic wins

The producer resolves an endpoint in a fixed order: the provider's `anthropic` endpoint, then its
`openai` endpoint, then the single-protocol shorthand (`base_url` with `wire_api`). Anthropic
first is deliberate — where a provider offers both, the anthropic route is the richer surface, and
`anthropic` is copilot's own first-class provider type. The canonical protocol names map onto
copilot's spellings in the producer, with the provenance of each spelling recorded beside it.

Two consequences that look like bugs and are not:

- Copilot's wire-api variable is meaningful **only** for the openai type; the producer emits none
  for the anthropic type, because copilot's own enum for it speaks only to openai.
- A provider whose anthropic endpoint is manufactured by the [wire bridge](wire-bridge.md) — as
  Cerebras's is — therefore reaches copilot *through the bridge* rather than through Cerebras's
  native chat-completions endpoint, even though copilot could speak the native one directly. That
  is the preference rule working as written.

### What the producer composes beyond the four core variables

Three behaviors the original design did not specify and the implementation added:

- **A context ceiling.** When the selected profile or the provider declares a context window, the
  producer passes it to copilot as a maximum prompt size. This is what makes a provider's
  `context_window` option meaningful to copilot and not only to claude.
- **A credential for a local endpoint.** A provider whose base URL is on loopback and carries no
  key gets a placeholder credential, because a local server that wants no authentication still
  needs the variable present. The loopback spellings the producer recognises are the ones yolo's
  own endpoints use.
- **A credential from provider options.** The hydrated `api_key` is preferred; an `api_key` under
  the provider's `options` is the fallback.

## The Cerebras pack

`packs/cerebras` is the second **purely declarative** pack — one `kind: "provider"`, one
`kind: "profile"`, a README, no CLI, no loophole, no surfaces — and the first shipped pack to carry
a top-level `needs` entry.

What it declares, and the reasoning that should survive a future edit:

- **Two endpoints, one of them manufactured.** The `openai` endpoint is Cerebras's real service
  (chat completions). The `anthropic` endpoint is the wire bridge's loopback URL — a claim the pack
  makes true by *needing* the bridge whenever claude or copilot is in the launch. The
  `needs` entry is conditional on those two binaries precisely because the bridge exists for them.
- **One model alias, and only one.** The alias is wire-true: the id is sent verbatim by every
  agent's catalog, so an alias that is not the provider's own spelling is a wire error waiting to
  happen. The second public model is deliberately absent — Cerebras's own documentation warns it
  may invoke tools it was not given, and its live capability flags disable parallel tool calls, so
  it has no tier in an unattended agent, not even a fast one. A user who wants it merges an alias
  in user config, since `providers` is a merged-scope key.
- **A context window, stated at the free tier.** The option exists so claude's auto-compact and
  copilot's prompt ceiling trigger at the real window rather than at an agent default. It is stated
  conservatively; a paid key states the larger figure in user config, which merges over the pack's
  options.
- **The key by name, never by value.** `api_key_env_name` carries the *name* of the variable that
  holds the credential. The value crosses through an `env_sources` channel or the invoking
  environment; a manifest that could carry a secret would be a manifest that leaks one.

> [!WARNING]
> **Put the key in an `env_sources` channel, not only in the invoking environment, whenever claude
> or copilot is in the launch.** The bridge reads its upstream credential from the jail's private
> env file at boot, so a key that exists only in the invoking environment reaches the agents and
> not the daemon — and a bridge that cannot authenticate refuses the launch rather than serving
> unauthenticated upstream traffic.

## Invariants

- **A manifest carries a credential's name, never its value.** This holds for every provider pack,
  and it is what lets provider packs be fetched, shared and linted.
- **Model ids are wire-true.** An alias is a local nickname for an id the provider itself
  recognises; the id is sent verbatim.
- **A derive composes nothing rather than half-arming an agent.** Where an agent would refuse, or
  would silently talk to the wrong host, the producer returns an empty table. A partially composed
  provider environment is worse than none.
- **A credential travels with the address it was minted for, or not at all.** A provider that
  named a protocol yolo's agent cannot reach must not have its key composed anyway — that sends a
  third-party credential to the agent's default first-party host. See
  [OQ-2](#why-its-this-way).

## What this does not license

- **A translation proxy per provider.** Manufacturing an endpoint is the
  [wire bridge](wire-bridge.md)'s job, it translates exactly one protocol pair, and a second pair
  is a change to that system rather than a provider pack's business.
- **A provider derive for `agy`.** See the warning above: there is no key to write.
- **Declaring an endpoint a provider does not serve and nothing manufactures.** Cerebras's
  `anthropic` endpoint is legitimate *because* the `needs` entry stages a listener that answers on
  it. An endpoint with neither a service nor a bridge behind it is a lie the derives will faithfully
  propagate.
- **Options that no agent reads.** A provider option exists to be consumed by some agent's derive.
  One that no reachable agent reads is dead weight that reads as a promise.

## Why it's this way

These ids are cited from the Go and Lua trees and from sibling documents; this appendix is where
they resolve.

| ID | Ruling | Why it must not be quietly undone |
| :--- | :--- | :--- |
| D-1 | Cerebras ships exactly one model alias | The excluded model's documented tool-hallucination warning and disabled parallel tool calls disqualify it from unattended use; user config is the place to add it back |
| D-2 | Copilot's delivery is env-only, with no config surface | Copilot's BYOK has no file keys at all — there is no config file to write, so a config derive would render into nothing |
| D-3 | Copilot's producer prefers the `anthropic` endpoint when a provider declares both | The anthropic route is the richer surface and `anthropic` is copilot's first-class provider type. Cited from `internal/wirebridged` |
| D-4 | **Reversed by the bridge.** Cerebras now declares a context window | D-4 ruled the option out when claude could not ride Cerebras. The bridge made claude and copilot reachable, and both consume the window — so the option is live, and the original reasoning (an option no reachable agent reads is dead weight) still stands as the *rule* |
| OQ-1 | Ship a claude-wire translation proxy as its own pack, joined by `needs` | Shipped as the wire bridge; it is what gave Cerebras an anthropic endpoint. [`wire-bridge.md`](wire-bridge.md) is the reference |
| OQ-2 | A credential is composed only when the provider's declaration does not name a protocol this agent cannot reach | Three provider shapes reach claude's env producer and only the middle one is wrong. **Interim by design, and now spent:** [`protocol-resolution.md`](protocol-resolution.md) made the state unreachable — the pairing is refused above the derive — and the branch was deleted rather than reworked |
| OQ-3 | The pack README states the rate limits | A free tier that cannot sustain an agent loop must say so where the user chooses the pack |

## Current values

Verified at `7da7b153`. The prose above says what each of these is for; this table is the only
place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Cerebras service endpoint (`openai`) | `https://api.cerebras.ai/v1`, `openai-chat-completions` | `packs/cerebras/pack.json` |
| Cerebras bridge endpoint (`anthropic`) | the wire bridge's jail loopback URL | `packs/cerebras/pack.json` |
| Credential variable | `CEREBRAS_API_KEY` | `packs/cerebras/pack.json` (`api_key_env_name`) |
| Model alias | `default` → `qwen-3.8-27b` | `packs/cerebras/pack.json` |
| Context window option | the free-tier window | `packs/cerebras/pack.json` (`options.context_window`) |
| Bridge dependency | `wire-bridge`, when `claude` or `copilot` is selected | `packs/cerebras/pack.json` (`needs`) |
| Copilot BYOK variables | `COPILOT_PROVIDER_BASE_URL`, `COPILOT_PROVIDER_TYPE`, `COPILOT_PROVIDER_WIRE_API`, `COPILOT_MODEL`, `COPILOT_PROVIDER_API_KEY`, `COPILOT_PROVIDER_MAX_PROMPT_TOKENS` | `packs/copilot/derive.lua` |
| Placeholder credential for a loopback endpoint | `local` | `packs/copilot/derive.lua` |
