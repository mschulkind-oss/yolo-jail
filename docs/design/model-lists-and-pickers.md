---
title: "Which model ids yolo ships, how an org shapes the list, and what each picker shows"
date: 2026-09-25
status: draft
tags: [providers, profiles, models, aliases, pickers, packs, bedrock, currency]
summary: "Where yolo must pick model ids itself (the 2026-09-25 ruling on OQ-BR3: only where an agent cannot just fall back to its own default), how those picks ship in a built-in pack that comes with yolo rather than in core, how a company pack adds to or narrows a model list, how each agent's picker renders the result, and how the list stays current without yolo keeping a catalog."
vantage:
  status-chip: true
---

# Which model ids yolo ships, how an org shapes the list, and what each picker shows

**Status:** DESIGN, 2026-09-25. Nothing here is built. MEASURED at `ee8154f2` (2026-09-24): a
pack's provider `models` is a flat alias → id map; the object form of a model is user config
only; `packs/claude/derive.lua` and `packs/pi/derive.lua` hard-code three GPT-6 ids for the
`openai-codex` provider; packs/claude's `bedrock` provider declares no `models`. SOURCED
2026-09-25 from AWS's model cards: the Anthropic-on-Bedrock geographic prefixes are `us.`, `eu.`,
`au.`, `jp.` and `global.`, and the set differs per model
([§5.3](#53-the-prerequisite-verify-before-an-id-ships)). UNMEASURED: whether Claude Code's
gateway model discovery survives `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`, and the format of
codex's `model_catalog_json`. Code claims cite a symbol, never a line.

**The question this doc answers.** When you pick a provider with `-p`, something has to decide
which model the agent starts on and which models its menu offers. Usually the agent can decide
that itself. Sometimes it cannot. Then yolo has to pick. This doc says where yolo picks, where
those picks live, how a company changes them, what each agent's menu shows, and how the ids stay
current.

**Where things stand.**

- **Ruled 2026-09-25:** yolo picks model ids where an agent cannot just default, and ships them
  in a built-in pack that comes with yolo, not in core ([OQ-BR3](#OQ-BR3)). That also answers
  provider-switching's "does yolo ship the ids?" ([OQ-PSW3](#OQ-PSW3)).
- **Built:** nothing of this design. Today's model lists are per-pack alias maps plus two
  hard-coded GPT-6 lists ([§3](#3-what-exists-today)).
- **Moved here on 2026-09-25**, with their ids unchanged: [OQ-BR3](#OQ-BR3) and
  [OQ-BR12](#OQ-BR12)–[OQ-BR15](#OQ-BR15) from [`bedrock-plumbing.md`](bedrock-plumbing.md), and
  [OQ-PSW1](#OQ-PSW1) and [OQ-PSW3](#OQ-PSW3) from [`provider-switching.md`](provider-switching.md).
  Those two carried the old prefix `PS`, as `PS1` and `PS3`, until later that day, when that doc's series was
  renamed `OQ-PSW` because [`provisioner-sets.md`](provisioner-sets.md) also uses `OQ-PS`.

**Needs your ruling:** [OQ-ML2](#OQ-ML2) first, because it decides how many ids the other questions are about; then [OQ-ML1](#OQ-ML1), [OQ-BR12](#OQ-BR12), [OQ-BR13](#OQ-BR13), [OQ-BR14](#OQ-BR14), [OQ-PSW1](#OQ-PSW1) and [OQ-BR15](#OQ-BR15).

- [OQ-ML1](#OQ-ML1): the shape of the built-in picks pack. Leaning: one yolo-shipped pack that
  adds `models` entries to other packs' providers, joined through `needs`, and yielding to any
  other writer.
- [OQ-ML2](#OQ-ML2): which provider × agent cases get a yolo pick. Leaning: exactly the cases
  where the agent would otherwise start on a wrong-family id or on none. Cost: claude's native
  `-p bedrock` gets no pick, so `-p anthropic` and `-p bedrock` may put `opus` on different
  models; ruling the other way ships Anthropic-on-Bedrock ids whose geographic prefixes differ per
  model and must each be read off that model's AWS card
  ([§5.3](#53-the-prerequisite-verify-before-an-id-ships)).
- [OQ-BR12](#OQ-BR12): a `models` contribution kind so a company pack can shape a list. Leaning:
  yes, with `add` and `only`.
- [OQ-BR13](#OQ-BR13): how each derive renders the list into its agent's picker. Leaning: each
  derive renders the one list into its own agent's surface.
- [OQ-BR14](#OQ-BR14): how the lists stay current. Leaning: the agents' own catalogs, plus a
  `yolo check` warning.
- [OQ-PSW1](#OQ-PSW1): claude's derive reads `balanced`/`fast`. Leaning: yes, keeping
  `sonnet`/`haiku` as synonyms.
- [OQ-BR15](#OQ-BR15): the bridge serves `GET /v1/models`. **Later, measure first.**

**Out of scope, and where it lives instead.** Refusing an off-list model at the wire:
[`wire-bridge-gateway.md`](wire-bridge-gateway.md#OQ-WG3), which reads this doc's effective
list. Which vendors each agent can call on Bedrock, and the per-agent filter table:
[`bedrock-plumbing.md`](bedrock-plumbing.md#OQ-BR9). Clearing a model id when you stop
selecting a profile: [`provider-switching.md`](provider-switching.md#decision-ledger). Withholding a
credential so that a menu shrinks: [`provider-credential-scope.md`](provider-credential-scope.md#OQ-CN4).
What a provider and a profile should mean at all:
[`providers-and-profiles-redesign.md`](providers-and-profiles-redesign.md).

---

## 1. The words, plainly

- A **provider** is where model requests go, plus the model ids that place understands. It may
  name no URL when the agent's own client knows the service ([`providers.md`](../reference/providers.md)).
  A **profile** is the name you type after `-p`: it selects one provider and may set options,
  such as the model to start on. Why the pair confuses, and what it might become, is
  [`providers-and-profiles-redesign.md`](providers-and-profiles-redesign.md)'s question.
- A **model alias** is a word a provider maps to one of its ids (`default` →
  `global.openai.gpt-6-astra`). A **tier alias** *(coined here; it names the words of the shared tier
  vocabulary that [`provider-switching.md`](provider-switching.md)'s 2026-09-24 version proposed,
  which moved here on 2026-09-25)* is one meant to mean the same capability on
  every provider ([§6](#6-tier-aliases-default-fast-balanced)).
- **vendor** *(coined in [`bedrock-plumbing.md`](bedrock-plumbing.md#OQ-BR9))*: the model's
  maker, one lowercase open-vocabulary string per entry, read by derives, uninterpreted by core.
- <a id="term-company-pack"></a>A **company pack** *(coined in
  [`bedrock-plumbing.md`](bedrock-plumbing.md)'s 2026-09-24 version, from the maintainer's
  phrasing; defined here since 2026-09-25)*: a pack an organization publishes to its people that carries policy
  rather than a program, here which models they see and in what order. It is usually fetched from
  a git remote.
- <a id="term-effective-list"></a>An agent's **effective list** *(coined in
  [`bedrock-plumbing.md`](bedrock-plumbing.md)'s 2026-09-24 version; defined here since
  2026-09-25)*: the
  ordered list its picker offers for the selected provider, composed from every pack and the
  user's config, then filtered to what that agent's transport can call. Not a catalog.
- A **cannot-default case** *(coined here)*: a provider × agent pairing where, if yolo writes no
  model, the agent starts on a wrong-family id or on none
  ([§4](#4-where-yolo-picks-the-cannot-default-cases)). A **pick** *(coined here)* is one dated
  id yolo ships for such a case, and the **picks pack** *(coined here)* carries them
  ([§5](#5-the-picks-pack)). A **picker** is the agent's own model menu.

**Where the Bedrock split ended up**, since every example below uses it. yolo ships one Bedrock
endpoint family, `bedrock-runtime` ([DIR-BR3](bedrock-plumbing.md#decision-ledger)). codex,
opencode and pi reach it through their own native Bedrock clients. claude has two profiles
([OQ-BR11](bedrock-plumbing.md#OQ-BR11)): the **native profile**, claude's own Bedrock mode,
Anthropic models only; and the **everything profile** *(coined in [`bedrock-plumbing.md`](bedrock-plumbing.md))*, which
points claude at the wire bridge so one session can switch between Anthropic and every other
Bedrock model. copilot goes through the bridge. Whether one provider holds every family is
[OQ-BR9](bedrock-plumbing.md#OQ-BR9), still open there.

---

## 2. The ruling, and how it fits what was said before

The maintainer, 2026-09-25, on [OQ-BR3](#OQ-BR3): *"in cases where we can't just fall to the
default by just not having an opinion and letting the agent pick … I do want to pick these …
let's actually ship this in core some way … a built-in pack (it's not in core per se but it comes
with [yolo]) with these that tries to pick these different models."*

Four earlier statements said yolo ships no models. The ruling narrows them. **The reconciled
rule: core carries no model ids, and a yolo-shipped pack may carry dated picks, only for the
cannot-default cases.**

| Earlier statement | What survives |
| :--- | :--- |
| *"No model catalog in yolo"* (bedrock-plumbing's non-goals, 2026-09-24; moved to this doc's [§11](#11-forbidden-and-non-goals) on 2026-09-25): no tracking of Bedrock's model list, region matrix or pricing | **Survives, narrowed.** yolo still tracks no catalog. A handful of dated picks is not a catalog: it states no region matrix and no price, and it is checked against the agents' own catalogs ([§9](#9-currency-the-agents-catalogs-plus-a-staleness-warning)), never used as one |
| *"No model catalog in core"* (provider-switching's "does not license" list, 2026-09-24; moved to this doc's [§11](#11-forbidden-and-non-goals) on 2026-09-25); the tier vocabulary is three names and the ids stay in replaceable provider entries | **Survives whole.** The picks are pack data, and the user's config replaces them |
| [OQ-GP2](gateway-provider-packs.md#decision-ledger): *"ship no models"* | **Narrowed**, for the cannot-default cases only. The ledger note is already in [`gateway-provider-packs.md`](gateway-provider-packs.md). The OpenRouter and Kilo packs still ship none |
| [agent-auth-modes OQ-3](agent-auth-modes.md#12-decision-ledger): *"User-level config is source of truth in v1. No binary presets baked"* | **Survives in intent, narrowed in letter.** Embedded packs ship inside the yolo binary, so a pick is literally in it. What that ruling protected still holds: the user's config is the last writer ([§7.2](#72-composition-rules)), and a pick is declarative pack data, visible as a pack and replaceable, never a Go table. A ledger note is owed there saying so |

Rejected, as before: a canonical model-name translation table in core, one id per model per
provider. It is *"the `wire_api` enum mistake at model granularity"* (provider-switching's alternatives table,
2026-09-24; the alternative moved here on 2026-09-25, and [§11](#11-forbidden-and-non-goals)
forbids it): a mapping that
changes weekly and goes wrong silently.

---

## 3. What exists today

MEASURED at `ee8154f2`, 2026-09-24, unless marked.

- **A pack's models are a flat alias → id map.** `Models map[string]string` on the provider
  contribution in `internal/packdecl/contributes.go`. The object form of a model, with `name`,
  `context_window`, `cost`, `reasoning`, `input` and `max_tokens`, is user config only
  (`knownModelKeys` in `internal/config/config.go`). That set has no `description` field.
- **Two derives hard-code the same three GPT-6 ids, for `openai-codex` only.** In
  `packs/claude/derive.lua`, `availableModels` lists `gpt-6-sol`, `gpt-6-astra`, `gpt-6-luna`
  (the default first, so Claude Code's Default row resolves to Sol), with
  `enforceAvailableModels`; `modelPicker.options` lists Astra, Sol, Luna, with labels and the
  descriptions "Frontier", "Balanced", "Fast", and `replaceBuiltInOptions`. In
  `packs/pi/derive.lua`, `enabledModels` lists the same three, most capable first, with a literal
  fallback default of `gpt-6-sol` and a subagent `modelScope.allow` of `openai-codex/gpt-6-*`.
  For every other provider pi renders `enabledModels` from the provider's `models` map, sorted.
- **No pack can add to or narrow another pack's provider list.** `KindProvider` combines
  exclusively. `config-list` appends to a list but cannot narrow one, and refuses a `profile` gate
  (`internal/packdecl`, `configlist_test.go`).
- **packs/claude's `bedrock` provider is a bare name**, with no `models`. With no profile `model`
  option, the claude env derive emits no model variable and Claude Code chooses its own.
- **Claude resolves its own tier words, per provider.** `--model opus` is provider-relative, and
  on Bedrock `sonnet` reaches an older Sonnet than on the first-party API (vendor docs, SOURCED
  2026-09-04). The claude env derive emits `ANTHROPIC_MODEL` and
  `ANTHROPIC_DEFAULT_OPUS/SONNET/HAIKU_MODEL` from the selected provider's `models` map. Since
  `f7b14308` and `caaaae1b` (2026-09-15/16), a missing `sonnet` or `haiku` alias falls back to
  the selected model (`m.sonnet or selected`), and a profile `model` option that names no alias,
  in a map with no `default`, is used as a literal id. So a Bedrock profile carrying
  `model: "us.anthropic.…"` already pins every tier with no map.
- **Runtime has no model-list endpoint** (SOURCED 2026-09-24): there is no
  `GET /openai/v1/models` on `bedrock-runtime`, so nothing yolo ships can ask Bedrock which models
  exist.
- **The agents' own Bedrock catalogs** (2026-09-24): pi-ai 0.87.1 ships 165 Bedrock entries, all
  on Converse (MEASURED count). opencode 1.18.32's embedded models.dev has 179 Bedrock entries
  (SOURCED from the research pass, not re-counted). claude and copilot have no catalog for a
  non-Anthropic model.

---

## 4. Where yolo picks: the cannot-default cases

The ruling's test is whether the agent can pick if yolo says nothing. Candidates, each with why:

| Provider × agent | If yolo writes nothing | Evidence | Pick? (under [OQ-ML2](#OQ-ML2)'s leaning) |
| :--- | :--- | :--- | :--- |
| Bedrock runtime × codex | codex starts on its own default slug. Its catalog maps slugs to mantle's bare ids, and holds runtime `global.` ids for Terra and Luna only. An id it does not know runs on fallback metadata (D4) | INFERRED from codex 0.156.1 strings; not run ([bedrock-plumbing evidence](bedrock-plumbing.md#14-evidence-and-how-to-re-check-it)) | **yes** |
| Bedrock runtime × pi | pi's catalog lists the bare `openai.gpt-5.6-sol` against a runtime base URL: the wrong-spelling 404 bedrock-plumbing's P1 names, shipped in a vendor catalog | pi-ai 0.87.1 source, read not run ([bedrock-plumbing §4](bedrock-plumbing.md#4-what-each-agent-can-actually-do)) | **yes** |
| Bedrock runtime × opencode | its default for `amazon-bedrock` is unread | UNMEASURED | **yes until measured**, then drop if opencode's own default is callable |
| claude everything profile | claude's built-in options are Anthropic names; it has no idea a Kimi or GPT id exists | [OQ-BR11](bedrock-plumbing.md#OQ-BR11); claude 2.1.282 | **yes** |
| copilot through the bridge | `COPILOT_MODEL` unset: copilot has no Bedrock catalog | `packs/copilot/derive.lua` | **yes** (the first callable entry of the same list) |
| `openai-codex` × claude, pi | already shipped as hard-coded picks ([§3](#3-what-exists-today)) | derive source | **yes, already**; they move into data ([§8](#8-rendering-the-effective-list-into-each-picker)) |
| claude native profile (`bedrock`) | Claude Code resolves its own tier words to Anthropic models, older ones on Bedrock | vendor docs, 2026-09-04 | **no** under the leaning: the family is right |
| first-party `anthropic` provider × claude | Claude Code's own current defaults | — | **no** |
| gateway packs (OpenRouter, Kilo) | the user curates, per [OQ-GP2](gateway-provider-packs.md#decision-ledger) | — | **no** |

⚠ **The leaning costs one planned behavior.** [`provider-switching.md`](provider-switching.md)
planned a `models` map on claude's native `bedrock` provider, and a done-condition that `-p
anthropic` and `-p bedrock` put the same tier word on the same model. Under the leaning neither
gets a pick, so `opus` on Bedrock means what Claude Code says it means there, which may be an
older model. Ruling [OQ-ML2](#OQ-ML2) the other way restores both, at the price of shipping
Anthropic-on-Bedrock ids whose geographic prefixes differ per model and must each be read off
that model's AWS card ([§5.3](#53-the-prerequisite-verify-before-an-id-ships)).

---

## 5. The picks pack

### 5.1 Its shape

One yolo-shipped pack, `packs/default-models` for example, whose whole content is `models`
contributions ([§7](#7-how-a-company-pack-shapes-a-list-the-models-kind)) to providers other
packs own. It installs no program and ships no provider. The same shape as `packs/zai`, a pack
of declarative facts only.

```jsonc
// packs/default-models/pack.json — the shape, not the file; ids are checked at build time
{
  "name": "default-models",
  "contributes": [
    { "kind": "models", "provider": "bedrock", "add": [
        { "id": "global.openai.gpt-6-astra", "vendor": "openai", "alias": "default",
          "name": "GPT-6 Astra" },
        { "id": "…", "vendor": "openai", "alias": "fast" }
    ] },
    { "kind": "models", "provider": "openai-codex", "add": [
        { "id": "gpt-6-astra", "vendor": "openai", "name": "GPT-6 Astra", "description": "Frontier" },
        { "id": "gpt-6-sol",   "vendor": "openai", "alias": "default", "name": "GPT-6 Sol", "description": "Balanced" },
        { "id": "gpt-6-luna",  "vendor": "openai", "alias": "fast", "name": "GPT-6 Luna", "description": "Fast" }
    ] },
    { "kind": "models", "provider": "openai-codex", "only": ["gpt-6-astra", "gpt-6-sol", "gpt-6-luna"] }
  ]
}
```

`global.openai.gpt-6-astra` is SOURCED from its model card (2026-09-25). The `alias` and
`description` fields do not exist in any schema today ([§7.3](#73-two-fields-the-entry-shape-is-missing)).

### 5.2 How it joins a launch

"Nothing is active by default" holds: an empty config still gets no picks. The picks pack joins
only when a selected provider pack `needs` it, the same way `packs/claude` pulls in
`wire-bridge` ([`needs`](../reference/wire-bridge.md#needs--a-conditional-pack-dependency)).
That join is printed at launch with its cause, and `yolo check` prints it too. Entries naming a
provider no selected pack declares are inert.

**Picks yield.** A pick is yolo's opinion where nobody else has one. So the picks pack's entries
for a provider apply only when no other pack and no user config wrote a list for it. A company
pack that `add`s its own list therefore replaces yolo's picks rather than appending after them,
and its order is the order users see. This is [OQ-ML1](#OQ-ML1)'s sub-choice.

Every pick is dated in the pack README: the date, and which agent catalog or AWS page it was
checked against. The staleness warning ([§9](#9-currency-the-agents-catalogs-plus-a-staleness-warning))
covers every pick.

### 5.3 The prerequisite: verify before an id ships

An unverified prefix is exactly the 404-on-unknown-model failure bedrock-plumbing's P1 describes.
So no Anthropic id enters the picks pack until its prefixes are read from AWS's own pages and
dated. This was [OQ-PSW3](#OQ-PSW3)'s blocker. It is now a build prerequisite, not a question.

**SOURCED 2026-09-25 from AWS's model cards**, which are now where AWS lists inference
profile ids. Its [inference-profile support page](https://docs.aws.amazon.com/bedrock/latest/userguide/inference-profiles-support.html)
says each model's ids are "now documented on the model's detail page". The set this doc
assumed, `us.`, `eu.` and `global.`, was incomplete. Across the seven Anthropic cards read, the
`bedrock-runtime` prefixes are `us.`, `eu.`, `au.`, `jp.` and `global.`:

| Model (card) | Runtime model id | `us.` | `eu.` | `au.` | `jp.` | `global.` |
| :--- | :--- | :---: | :---: | :---: | :---: | :---: |
| [Claude Opus 5.5](https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-opus-5-5.html) | `anthropic.claude-opus-5-5` | ✓ | ✓ | ✓ | ✓ | ✓ |
| [Claude Fable 5.1](https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-fable-5-1.html) | `anthropic.claude-fable-5-1` | ✓ | — | — | — | ✓ |
| [Claude Mythos 5.1](https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-mythos-5-1.html) (gated preview) | `anthropic.claude-mythos-5-1` | ✓ | — | — | — | ✓ |
| [Claude Opus 5](https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-opus-5.html) | `anthropic.claude-opus-5` | ✓ | ✓ | ✓ | — | ✓ |
| [Claude Sonnet 5](https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-sonnet-5.html) | none: runtime needs a geo or global id | ✓ | ✓ | ✓ | — | ✓ |
| [Claude Haiku 4.5](https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-haiku-4-5.html) | none: runtime needs a geo or global id | ✓ | ✓ | ✓ | ✓ | ✓ |
| [Claude Sonnet 4.5](https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-sonnet-4-5.html) | `anthropic.claude-sonnet-4-5-20250929-v1:0` | ✓ | ✓ | ✓ | ✓ | ✓ |

Each id is the prefix plus the model's base id, for example `au.anthropic.claude-opus-5-5`.
The 5.x base ids are undated. The 4.5 ones keep the dated `-YYYYMMDD-v1:0` form, for example
`global.anthropic.claude-haiku-4-5-20251001-v1:0`. What the prefixes mean, per the cards'
data-residency notes: `us.` "keeps data within US and Canada regions", `eu.` within EU
Regions, `au.` within Australia, `jp.` within Japan, and `global.` "routes worldwide with no
residency constraints".

What this changes for the build (SOURCED facts, not a ruling):

- **A pick cannot be composed as `<prefix>.<base id>`.** The prefix set differs per model.
  Fable 5.1 has no `eu.`. Opus 5 and Sonnet 5 have no `jp.`. So every (model, prefix) pair has
  to be read off that model's card and dated, which is the rule above applied per id rather
  than once per set.
- **`global.` is the only prefix on every card read.** It is also the one with no residency
  guarantee.
- **There is no `apac.` or `us-gov.` id on any card read.** The general pages still name APAC
  as an example geography ([geographic cross-Region inference](https://docs.aws.amazon.com/bedrock/latest/userguide/geographic-cross-region-inference.html)),
  but the cards split it into `au.` and `jp.`. GovCloud rows show Geo support, and Sonnet 4.5's
  geo table lists `us-gov-east-1` and `us-gov-west-1` as source Regions of its `us.` profile.
  So GovCloud callers use `us.`, at least for that model.
- **One AWS page is stale.** The inference-profile support page says global profiles are
  "currently only supported on Anthropic Claude Sonnet 4". Every card above lists a `global.`
  id, so that sentence should not be cited.

**Not covered:** Claude Opus 4.8, 4.7, 4.6 and 4.5, Sonnet 4.6, Sonnet 4, Opus 4.1, Mythos 5,
Fable 5, and the 3.x models. Their cards were not read. Read one before any of them gets a
pick.

### 5.4 First-party providers are a use of this

[`provider-switching.md`](provider-switching.md) proposed shipping an endpoint-less `anthropic`
provider and profile in packs/claude, so `-p anthropic` ↔ `-p bedrock` swaps a whole bundle in
one word. It has no URL and no `api_key_env_name`, so the credential preflight demands nothing.
That part is unchanged and needs no ids: under [OQ-ML2](#OQ-ML2)'s leaning claude defaults on
its own first-party endpoint, so the `anthropic` provider ships with no picks. The same holds for
any other agent whose first-party endpoint it can default on.

---

## 6. Tier aliases: default, fast, balanced

**P2** *(provider-switching's second principle)*: **a human names a capability; a provider names
a model.** A person means "opus" or "fast". If they have to type `us.anthropic.claude-opus-5`,
the abstraction has failed, and it fails when they switch providers.

So every provider is expected to declare three conventional aliases, and may declare more:

| Alias | Means | Today |
| :--- | :--- | :--- |
| `default` | what you get when nothing is said | cerebras and llamacpp, and every derive's fallback |
| `fast` | cheap and quick | nowhere; zai's aliases became its wire ids (`8e901423`) |
| `balanced` | the middle tier, where one exists | nowhere |

They are capability-shaped, not vendor-shaped, because a vendor tier name (`sonnet`, `terra`)
cannot survive a switch to another vendor. **It is a convention with a warning, not an enum.** A
provider missing one gets one launch warning naming which, and the launch proceeds. A provider
that also declares `sol` has four aliases, and that is not an error.

**claude is where it bites.** Its env derive reads the vendor names `sonnet` and `haiku`
literally. The proposal: read `balanced` → `ANTHROPIC_DEFAULT_SONNET_MODEL` and `fast` →
`ANTHROPIC_DEFAULT_HAIKU_MODEL`, keeping `sonnet`/`haiku` as synonyms so no user config breaks
([OQ-PSW1](#OQ-PSW1)). It says nothing yet about the opus tier, or the Fable tier
(`ANTHROPIC_DEFAULT_FABLE_MODEL`), which [OQ-BR13](#OQ-BR13) maps only from a declared `fable`
alias.

---

## 7. How a company pack shapes a list: the `models` kind

### 7.1 The kind

A new contribution kind, `models` ([OQ-BR12](#OQ-BR12)). It names a provider and carries an
ordered list of object-form entries: `id`, `vendor`, and the existing optional `name`,
`context_window`, `cost`, `reasoning`, `input` and `max_tokens`. Its verb is `add`, which appends
entries, or `only`, which narrows the list to the ids named. It exists because nothing today lets
one pack shape another pack's list ([§3](#3-what-exists-today)). It reads nothing from the host,
so it needs no fetched-pack approval.

### 7.2 Composition rules

- **Order.** Packs apply in `packs` order, then the user's config. `add` appends entries not
  already present. A duplicate `id` keeps the first writer's position and fields whole, and
  `yolo check` names the duplicate. The picks pack yields rather than taking a position
  ([§5.2](#52-how-it-joins-a-launch)).
- **`only`** narrows to the ids it names and never adds one. Two `only` contributions for one
  provider intersect. An id that `only` names and no one added is dropped, and `yolo check`
  names it.
- **The user is the last writer.** A user's `providers.<name>.models` replaces the composed list
  for that provider. Today's string-form maps keep working unchanged.
- **Degenerate lists.** An empty effective list for one agent writes no catalog row and no
  selection for that agent, as for any unreachable provider, and `yolo check` names the agent and
  provider. The exception is claude's native profile, where an empty list is today's shipped
  case: no model variables, and claude chooses. A pack's `models` entry with no `vendor` is
  refused at load, naming the entry. (A user's string-form entry has no vendor and is offered to
  every agent, as today.)
- **Resolving the default:** the profile's `model` option if it names an entry this agent can
  call; else the provider's `default` alias if callable; else the first callable entry in list
  order. No error when nothing is callable: that is the empty case.

### 7.3 Two fields the entry shape is missing

Found while writing this doc, against the tree at `ee8154f2`:

- **`alias`.** A user's object form is keyed by alias (`models.<alias>`), but the kind's entries
  are a list keyed by `id`, so a pack could not say which entry is `default` or `fast`. The picks
  and [§6](#6-tier-aliases-default-fast-balanced)'s tiers both need it. Proposal: an optional
  `alias` per entry, unique per provider after composition.
- **`description`.** claude's `openai-codex` picker shows "Frontier", "Balanced", "Fast", and
  `knownModelKeys` has no field to carry them. Moving that list into data byte-identically
  ([§8](#8-rendering-the-effective-list-into-each-picker)) needs one, added to both the pack schema
  and the user's closed key set.

---

## 8. Rendering the effective list into each picker

Each derive writes the effective list into its own agent's picker ([OQ-BR13](#OQ-BR13)):

| Agent | Picker surface | Notes |
| :--- | :--- | :--- |
| claude | `modelPicker.options`, `availableModels` | `availableModels` puts the resolved default first, then the rest in list order; `modelPicker` uses list order |
| pi | `enabledModels` | a soft shortlist, never a boundary: Tab shows every credentialed model ([`provider-credential-scope.md`](provider-credential-scope.md#241-what-pis-enabledmodels-actually-constrains)) |
| opencode | its provider `whitelist` | per the research pass |
| oh-omp | `models.yml` | not installed here; unverified |
| codex | `model_catalog_json` | format unread, so codex gets the selection only until it is read |
| copilot | one `COPILOT_MODEL` | the first entry it can call |

Rules:

- **Written once per launch** at the boot render, as managed layers, like every derive output.
- **An interactive `/model` choice still wins** until yolo's own selection changes (the
  `selection` namespace, unchanged).
- **claude's built-in options are replaced only on the everything profile**, whose built-ins are
  Anthropic names that profile should not lead with. The native profile keeps them and adds the
  list's entries beside them.
- **`enforceAvailableModels` is set only when an `only` narrowed the list.** A list that merely
  adds must never lock a user out of the agent's built-in choices. `openai-codex` keeps its
  enforcement with no exception: the picks pack states that list as an `add` plus an `only` over
  the same three ids.
- **The hard-coded GPT-6 lists move into data** ([§5.1](#51-its-shape)), and the rendered
  `openai-codex` output stays byte-identical, pinned by a test that fails when the derive's call
  site is deleted. pi's subagent `modelScope.allow` wildcard is policy, not a list, and stays in
  the derive; the test pins it too.

**The model must be in the menu.** DIR-BR2's *"I want to use Kimi in Claude through Bedrock by
just choosing it in the menu"* ([bedrock-plumbing ledger](bedrock-plumbing.md#decision-ledger))
needs three pieces at once: the everything profile ([OQ-BR11](bedrock-plumbing.md#OQ-BR11)); a
list that contains Kimi ([OQ-BR12](#OQ-BR12), [OQ-BR14](#OQ-BR14), or a pick under
[OQ-ML2](#OQ-ML2)); and the claude derive rendering it into `modelPicker` ([OQ-BR13](#OQ-BR13)).
Switching from an OpenAI model to Claude in one session is the everything profile's whole
purpose.

---

## 9. Currency: the agents' catalogs, plus a staleness warning

With no pack or user narrowing, an agent with a vendor catalog shows its own: pi-ai's, and
opencode's embedded models.dev. That is current with the agent's version, and yolo adds nothing
to it ([OQ-BR14](#OQ-BR14)).

A list yolo *does* carry, the picks included, is checked by `yolo check`:

- **Only `yolo check` runs it, never a launch.** No model-list network call is made at launch.
  Runtime has no list endpoint anyway, so the only source would be the Bedrock control plane,
  which the `aws-auth` example policy does not grant.
- **An id absent from every installed agent's catalog is a warning, never a refusal.**
- **When no catalog could be read, the check says it could not ask.** "Not found" and "could not
  ask" are different answers.
- **No "a newer model exists in this family" report.** No catalog states a family, and it would
  warn on every list, all the time.

---

## 10. Later: the bridge's `GET /v1/models`

Claude Code's `CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY` fills its picker from a gateway's
`/v1/models`. If the wire bridge served that from the effective list, claude's picker would follow
the list with no settings write ([OQ-BR15](#OQ-BR15)). Today the bridge answers only
`POST /v1/messages` ([WB-D14](../reference/wire-bridge.md#wb-d14)'s refusal path covers every
other path). There is nothing upstream to proxy: runtime has no model-list endpoint, so the
bridge would serve the list yolo composed, from memory. That is not "discovery" in
[providers.md](../reference/providers.md#what-this-does-not-license)'s sense.

UNMEASURED, and the thing to measure first: whether discovery survives the
`CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` the claude derive sets on every routed launch. It
lives here beside [OQ-BR13](#OQ-BR13) because each is the other's alternative for claude. If
built, it amends WB-D14's one-route surface.

---

## 11. Forbidden, and non-goals

**Forbidden:**

- a model id in core: no Go table, no translation table;
- a network call for models at launch;
- `enforceAvailableModels` without an `only`;
- a pick for a case the agent can default on;
- a pick with no date and no source in the pack README;
- an Anthropic-on-Bedrock id whose geographic prefix is unverified.

The old list's third line, *"the bridge dialing anything but its boot-selected upstream"*, is
dropped here. [OQ-BR11](bedrock-plumbing.md#OQ-BR11) amended that rule for the everything
profile, and what the bridge may dial is now
[`wire-bridge-gateway.md`](wire-bridge-gateway.md)'s question.

**Non-goals:**

- **No catalog.** yolo tracks no provider's full model list, region matrix or pricing
  ([§2](#2-the-ruling-and-how-it-fits-what-was-said-before)).
- **No closed alias vocabulary.** `default`/`fast`/`balanced` warn; they never refuse.
- **No enforcement at the wire.** A picker is a menu. Refusing an off-list model in the request
  path is [`wire-bridge-gateway.md`](wire-bridge-gateway.md#OQ-WG3)'s.

Rejected or leaning against, each argued in its question: fetching Bedrock's list at launch
and shipping a dated catalog snapshot ([OQ-BR14](#OQ-BR14) options B and C), and picks scattered
through each provider's own pack ([OQ-ML1](#OQ-ML1) option (b)).

---

## 12. Risks

| Risk | Mitigation |
| :--- | :--- |
| **R1** *(was bedrock-plumbing R8)*. A company pack's list goes stale: AWS retires an id, and every picker offers a model that 404s | The `yolo check` warning ([§9](#9-currency-the-agents-catalogs-plus-a-staleness-warning)). The failure itself is AWS's model error, naming the id |
| **R2** *(was bedrock-plumbing R9)*. The effective list and an agent's own catalog disagree about an id's context window or cost | The object-form entry's fields win where the agent reads them, and a pack states them only where it has a source |
| **R3** *(was provider-switching R2)*. Reading `balanced`/`fast` breaks a config that declares `sonnet`/`haiku` | Synonyms, not a rename; a test pins both |
| **R4** *(was provider-switching R3)*. Shipped picks rot | They are defaults, replaced in two lines; dated in the README; covered by the warning; and they yield to any other list ([§5.2](#52-how-it-joins-a-launch)) |
| **R5** *(new)*. A pick joins a launch nobody asked for | The `needs` join prints its cause; the picks apply only to a provider the user selected |

---

## 13. Build order, and what done looks like

1. **The `models` kind** with `alias` and `description` ([OQ-BR12](#OQ-BR12),
   [§7.3](#73-two-fields-the-entry-shape-is-missing)), then **picker rendering**, moving the
   GPT-6 lists into data under the byte-identical test ([OQ-BR13](#OQ-BR13)).
2. **The picks pack** ([OQ-ML1](#OQ-ML1)), with the cases [OQ-ML2](#OQ-ML2) rules, each id dated;
   Anthropic ids only after the prefix check ([§5.3](#53-the-prerequisite-verify-before-an-id-ships)).
   provider-switching's three-line `models` map for claude's native `bedrock` provider lands here
   only if [OQ-ML2](#OQ-ML2) gives that profile a pick.
3. **The alias vocabulary**: the default-only warning, the claude derive's synonym reading, and a
   line in `providers.md` naming the three ([OQ-PSW1](#OQ-PSW1)).
4. **First-party providers** for claude, with no picks under [OQ-ML2](#OQ-ML2)'s leaning
   ([§5.4](#54-first-party-providers-are-a-use-of-this)).
5. **The `yolo check` staleness warning** ([OQ-BR14](#OQ-BR14)).
6. **A bridge `GET /v1/models`** waits on its measurement ([OQ-BR15](#OQ-BR15)).

**Done when:**

1. A company pack with `only` over five ids makes exactly those five, in its order, appear in
   claude's, pi's and opencode's pickers, each filtered to what that agent can call. copilot's
   `COPILOT_MODEL` is the first entry it can call.
2. The `openai-codex` pickers render byte-identically to today after the GPT-6 lists move into
   data, and the test fails if the render's call site is deleted.
3. `yolo check` warns about an id no installed catalog knows. With no agent installed, it says it
   could not check.
4. A provider declaring only `default` produces one warning naming the two missing aliases, and a
   working launch.
5. `yolo -p bedrock -- codex` with no user model config starts on a pick, and a company pack's
   `add` list for the same provider replaces the pick.
6. *(Only if [OQ-ML2](#OQ-ML2) gives claude's native profile a pick.)* On claude, `-p anthropic`
   and `-p bedrock` put the same tier word on the right id, with no hand-editing.

---

## 14. Open Questions

1. 💬 <a id="OQ-ML1"></a>**[OQ-ML1](#OQ-ML1): What shape is the built-in picks pack, and how
   does it join a launch without breaking "nothing is active by default"?**
   Options: **(a)** one yolo-shipped pack (`packs/default-models`, say) that adds `models`
   entries ([OQ-BR12](#OQ-BR12)'s kind) to other packs' providers, joined by those provider
   packs' `needs`, the wire-bridge pattern; **(b)** ids inside each provider's own pack, which is
   today's mechanism and buildable now; **(c)** always staged. Sub-choice: do picks **yield**,
   applying only when no other pack and no user wrote a list for that provider
   ([§5.2](#52-how-it-joins-a-launch))? Stakes: where every shipped opinion lives, whether a
   company's list replaces yolo's or trails it, and whether "nothing is active by default" gains
   an exception.

   <!-- vantage: oq id=OQ-ML1 leaning="(a): one yolo-shipped picks pack of models entries on other packs' providers, joined through the provider packs' needs and printed at launch. Every shipped opinion lives in one dated, replaceable file, OQ-BR12 becomes its prerequisite, and -p bedrock gets defaults with no new always-on rule. And picks yield: they apply only when no other pack or user wrote a list for that provider." -->

   _Leaning:_ (a), with picks that yield. Every shipped opinion lives in one dated, replaceable
   file. [OQ-BR12](#OQ-BR12) becomes its prerequisite, a company pack replaces it just by
   shipping a list, and `-p bedrock` gets defaults with no new always-on rule. The cost: (b)
   could ship this week and (a) waits on a new kind.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 <a id="OQ-ML2"></a>**[OQ-ML2](#OQ-ML2): Which provider × agent cases count as "we can't
   just fall to the default", and so get a yolo pick?** The candidates are
   [§4](#4-where-yolo-picks-the-cannot-default-cases)'s table: Bedrock runtime for codex, pi and
   opencode; claude's everything profile; copilot through the bridge; the already-shipped
   `openai-codex` lists; and the borderline ones, claude's native `bedrock` profile and the
   first-party `anthropic` provider, where the agent defaults to the right family but maybe not
   the newest model. Stakes: how many ids yolo keeps current, and whether provider-switching's
   same-tier-word goal survives for claude.

   <!-- vantage: oq id=OQ-ML2 leaning="Exactly the cases where the agent would otherwise start on a wrong-family id or on none: Bedrock runtime for codex, pi and opencode (opencode until its own default is measured), claude's everything profile, copilot through the bridge, and the openai-codex lists. Not claude's native bedrock profile or the first-party anthropic provider, where claude defaults to the right family. Each id is dated in the pack README and covered by OQ-BR14's warning." -->

   _Leaning:_ Exactly the cases where the agent would otherwise start on a wrong-family id or on
   none. That excludes claude's native profile and the `anthropic` provider, which drops
   provider-switching's same-tier-word done-condition unless ruled otherwise. Each id is dated in
   the pack README and covered by [OQ-BR14](#OQ-BR14)'s warning.

   **Answer:**
   > _(empty — fill in when decided)_

3. ✅ <a id="OQ-BR3"></a>**[OQ-BR3](#OQ-BR3): Does yolo ship model aliases at all?** (moved
   from [`bedrock-plumbing.md`](bedrock-plumbing.md) on 2026-09-25, id kept.) **Ruled
   2026-09-25:** yes, where the agent cannot default, in a built-in pack that comes with yolo
   ([Decision Ledger](#15-decision-ledger)). The old proposal, `default`/`balanced`/`fast` →
   `global.openai.gpt-5.6-{sol,terra,luna}`, was already stale before the ruling: pi-ai 0.87.1's catalog lists
   `openai.gpt-6-astra` (bare, `us.`, `global.`) beside GPT-5.6, Bedrock serves GPT-6 Astra on
   runtime as `us.openai.gpt-6-astra` / `global.openai.gpt-6-astra` (SOURCED
   2026-09-25), and yolo's codex-subscription defaults moved to GPT-6 in September (`2f11de95`,
   `7ad8358c`). Which ids ship is a build-time check, dated in the README.

4. ✅ <a id="OQ-PSW3"></a>**[OQ-PSW3](#OQ-PSW3): Does yolo ship the model ids, or only the empty
   provider shape?** (moved from [`provider-switching.md`](provider-switching.md) on 2026-09-25,
   where it was `PS3` before the rename noted at the top of this doc.) **Answered 2026-09-25 by [OQ-BR3](#OQ-BR3)'s ruling:** yolo ships the ids, in a
   built-in pack. It was always the same decision. Which of its two cases (the first-party
   provider, claude's `bedrock` map) get ids is [OQ-ML2](#OQ-ML2)'s. The geo-prefix verification
   stays a build prerequisite ([§5.3](#53-the-prerequisite-verify-before-an-id-ships)).

5. 💬 <a id="OQ-BR12"></a>**[OQ-BR12](#OQ-BR12): Is there a `models` contribution kind, so a
   company pack can shape another pack's provider?** (moved from [`bedrock-plumbing.md`](bedrock-plumbing.md), id kept.)
   [§7](#7-how-a-company-pack-shapes-a-list-the-models-kind). Stakes: whether an org can ship its
   model selection once, or every user copies it into their own config.

   | Option | Verdict |
   | :--- | :--- |
   | **A.** A new kind: it names a provider, verbs `add` and `only`, ordered object-form entries carrying `vendor` | **Leaning** |
   | **B.** Let `provider` combine as an overlay across packs | Ends sole ownership for every provider field, and needs a merge rule per field, to get one list |
   | **C.** Widen `config-list` to narrow, and to target a provider's models | `config-list` writes an agent's config surface, so the org would restate the list per agent |
   | **D.** User config only, as [OQ-GP2](gateway-provider-packs.md#decision-ledger) ruled for gateway packs | The org cannot ship it |

   ⚠ **Premise changed 2026-09-25.** [OQ-BR3](#OQ-BR3)'s ruling makes yolo's own picks pack the
   kind's first user under [OQ-ML1](#OQ-ML1)'s leaning, not only company packs. And the entries
   need `alias` and `description` ([§7.3](#73-two-fields-the-entry-shape-is-missing)), which the
   leaning did not name.

   <!-- vantage: oq id=OQ-BR12 leaning="A: a new models contribution kind naming a provider, with add and only verbs and ordered object-form entries carrying vendor; add unions in pack order, only intersects, the user's providers.<name>.models is the last writer. Reopens OQ-GP2 only in that a pack may now carry a list — yolo's own packs still ship as few ids as OQ-BR3 rules." -->

   _Leaning:_ A. `add` unions in pack order, `only` intersects, and the user's config is the last
   writer. yolo's own packs ship as few ids as [OQ-BR3](#OQ-BR3)'s ruling allows.

   **Answer:**
   > _(empty — fill in when decided)_

6. 💬 <a id="OQ-BR13"></a>**[OQ-BR13](#OQ-BR13): How does each derive render the effective list
   into its agent's picker?** (moved from [`bedrock-plumbing.md`](bedrock-plumbing.md), id kept.)
   [§8](#8-rendering-the-effective-list-into-each-picker). Stakes: what a user sees at launch,
   whether an org's `only` can lock a picker, and whether the hard-coded GPT-6 lists become data.
   Four sub-choices: claude's built-ins replaced only on the everything profile;
   `enforceAvailableModels` only under an `only`; the Fable tier mapped only from a declared
   `fable` alias (`ANTHROPIC_DEFAULT_FABLE_MODEL` is present in claude 2.1.282 and mapped by no
   derive); codex gets selection only until `model_catalog_json`'s format is read.

   Options: **A**, render into each agent's own surface from the one list, with the four
   sub-choices (leaning); **B**, render the default only and leave pickers to the agents'
   catalogs, which fails for claude and copilot, who have no Bedrock catalog, and makes an org's
   `only` meaningless; **C**, for claude, a bridge `/v1/models` instead ([OQ-BR15](#OQ-BR15),
   unmeasured).

   <!-- vantage: oq id=OQ-BR13 leaning="A: each derive renders the one effective list into its agent's own picker — claude modelPicker/availableModels, pi enabledModels, opencode whitelist, omp models.yml, copilot's first callable entry. Replace built-ins only on claude's bridged profile, set enforceAvailableModels only under an only, map the Fable tier only from a declared fable alias, and hold codex's model_catalog_json until its format is read. Move the hard-coded GPT-6 lists into data under a byte-identical test." -->

   _Leaning:_ A. Move the GPT-6 lists into data under a byte-identical test, which needs the
   `description` field.

   **Answer:**
   > _(empty — fill in when decided)_

7. 💬 <a id="OQ-BR14"></a>**[OQ-BR14](#OQ-BR14): How do the lists stay current?** (moved from
   [`bedrock-plumbing.md`](bedrock-plumbing.md), id kept.) [§9](#9-currency-the-agents-catalogs-plus-a-staleness-warning).
   Stakes: the maintainer's "up to date when it launches", against "no catalog in yolo" and
   [providers.md](../reference/providers.md#what-this-does-not-license)'s "no provider registry or
   discovery".

   Options: **A**, the agents' own catalogs plus a `yolo check` warning (leaning); **B**, ask the
   Bedrock control plane at launch, a call the example IAM policy denies, making models depend on
   the network at boot; **C**, a dated catalog snapshot in yolo, rotting on a schedule nobody
   runs; **D**, a dated README only, today's state, where a retired id is a 404 at first request.

   ⚠ **Premise changed 2026-09-25:** with picks now shipped ([OQ-BR3](#OQ-BR3)), the warning is
   what keeps yolo's own ids honest, not only a company's.

   <!-- vantage: oq id=OQ-BR14 leaning="A: currency comes from each agent's own vendor catalog (pi-ai's Bedrock catalog, opencode's embedded models.dev, codex's bundled catalog), plus a yolo check warning — never a refusal — for any listed id no installed catalog knows, saying so when no catalog could be read. No network call for models at launch, and no newer-model-exists report." -->

   _Leaning:_ A. Warn, never refuse; say so when no catalog could be read; no launch network
   call; no "newer model exists" report.

   **Answer:**
   > _(empty — fill in when decided)_

8. 💬 <a id="OQ-PSW1"></a>**[OQ-PSW1](#OQ-PSW1): Does claude's derive move to capability
   aliases?** (moved from [`provider-switching.md`](provider-switching.md), where it was `PS1` before the rename noted at the top of this doc.) It reads `sonnet` and `haiku`
   literally today. The proposal reads `balanced` and `fast`, keeping the old names as synonyms
   ([§6](#6-tier-aliases-default-fast-balanced)). Stakes: whether one alias vocabulary spans every
   provider, or claude keeps a dialect and a `-p` swap means something slightly different there.

   <!-- vantage: oq id=OQ-PSW1 leaning="Move, with synonyms. A vendor tier name cannot survive a switch to another vendor, which is the whole problem this doc is about; and synonyms make the move free for anyone's existing config." -->

   _Leaning:_ Move, with synonyms. A vendor tier name cannot survive a switch to another vendor,
   and synonyms make the move free for existing config.

   **Answer:**
   > _(empty — fill in when decided)_

9. 💬 <a id="OQ-BR15"></a>**[OQ-BR15](#OQ-BR15): Does the bridge serve `GET /v1/models`, and
   when?** (moved from [`bedrock-plumbing.md`](bedrock-plumbing.md), id kept.) **Later; measure first.**
   [§10](#10-later-the-bridges-get-v1models). Stakes: whether claude's picker follows the list with
   no settings write, and whether the bridge grows a second endpoint whose value is unproven.

   Options: **A**, later, after measuring discovery under
   `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` (leaning); **B**, now, beside
   [OQ-BR13](#OQ-BR13)'s settings rendering, on an unmeasured dependency; **C**, never, which is
   fine if settings rendering suffices and forecloses nothing A does not defer.

   <!-- vantage: oq id=OQ-BR15 leaning="A, later: first measure whether CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY survives CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1; if it does, the bridge serves GET /v1/models from the composed effective list with no upstream call — runtime has no list endpoint to proxy." -->

   _Leaning:_ A. Measure first. If discovery survives, serve the effective list from memory and
   never call upstream for it.

   **Answer:**
   > _(empty — fill in when decided)_

---

## 15. Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-BR3 | **yolo picks model ids where an agent cannot default, and ships them in a built-in pack that comes with yolo, not in core.** *"in cases where we can't just fall to the default by just not having an opinion and letting the agent pick … I do want to pick these … let's actually ship this in core some way … a built-in pack (it's not in core per se but it comes with [yolo]) with these that tries to pick these different models."* Moved from [`bedrock-plumbing.md`](bedrock-plumbing.md), id kept. Narrows [OQ-GP2](gateway-provider-packs.md#decision-ledger) (note made there) and, in letter, [agent-auth-modes OQ-3](agent-auth-modes.md#12-decision-ledger) (note owed there) | 2026-09-25 | [§5](#5-the-picks-pack) | — |
| OQ-PSW3 | **Answered by [OQ-BR3](#OQ-BR3)'s ruling: yolo ships the ids, in a built-in pack.** Moved from [`provider-switching.md`](provider-switching.md), where it was `PS3` before the rename noted at the top of this doc. The Anthropic-on-Bedrock geo-prefix verification stays a build prerequisite, not a question; which cases get ids is [OQ-ML2](#OQ-ML2) | 2026-09-25 | [§5.3](#53-the-prerequisite-verify-before-an-id-ships) | — |

---

## 16. Evidence

**Repo**: every code claim is in [§3](#3-what-exists-today), cited by symbol and measured at
`ee8154f2` (2026-09-24). The `needs` join rule is
[`wire-bridge.md`'s](../reference/wire-bridge.md#needs--a-conditional-pack-dependency).

**claude 2.1.282** (`~/.local/share/claude/versions/2.1.282`), 2026-09-24, MEASURED presence
only by `grep -c -a`: `modelPicker` (15), `replaceBuiltInOptions` (5), `enforceAvailableModels`
(9), `ANTHROPIC_CUSTOM_MODEL_OPTION` (12), `CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY` (6),
`ANTHROPIC_DEFAULT_FABLE_MODEL` (15). Documented at code.claude.com per the 2026-09-24 research
pass. None was exercised, and what discovery does under
`CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` is unread.

**Claude Code's tier aliases**, SOURCED 2026-09-04: they resolve per provider, to older models on
Bedrock, and `ANTHROPIC_DEFAULT_*_MODEL` repoints them
([model configuration](https://code.claude.com/docs/en/model-config);
[Claude Code model configuration](https://support.claude.com/en/articles/11940350-claude-code-model-configuration)).
Read against 2.1.261, the version installed then.

**Model catalogs**, 2026-09-24: pi-ai 0.87.1's `dist/providers/data/amazon-bedrock.json` holds
165 entries, all `bedrock-converse-stream` (MEASURED count; 0.85.1 holds 121). opencode 1.18.32's
embedded models.dev has 179 Bedrock entries (SOURCED from the research pass, not re-counted).
opencode's provider `whitelist`/`blacklist`, pi's `enabledModels`, oh-omp's `models.yml` and
copilot's single `COPILOT_MODEL` come from the same pass; pi's and copilot's are what their
derives write today. codex 0.156.1's catalog and its fallback-metadata message are in
[bedrock-plumbing's evidence](bedrock-plumbing.md#14-evidence-and-how-to-re-check-it).

**AWS**, SOURCED: runtime has no `GET /openai/v1/models` (endpoints and model API compatibility
pages, 2026-09-24); GPT-6 Astra is `us.openai.gpt-6-astra` / `global.openai.gpt-6-astra` on
runtime, Converse included, with no in-Region id
([its model card](https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-openai-gpt-6-astra.html),
2026-09-25).

**AWS, Anthropic geographic prefixes**, SOURCED 2026-09-25 from seven model cards (linked in
[§5.3](#53-the-prerequisite-verify-before-an-id-ships), which holds the table). The prefixes are
`us.`, `eu.`, `au.`, `jp.` and `global.`. The set differs per model, and only `global.` appears
on every card read. The earlier assumption, `us.`, `eu.` and `global.`, missed `au.` and `jp.`.
Cards not read are listed there; they still block a pick for their models.
