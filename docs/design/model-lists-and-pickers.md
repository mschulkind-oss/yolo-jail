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

**Status:** DESIGN, 2026-09-25, with two pieces BUILT. On 2026-09-27: the `openai-codex` list is
declared once, in [`packs/openai-auth/pack.json`](../../packs/openai-auth/pack.json), and every
consumer reads it ([ML-D1](#ML-D1) to [ML-D7](#ML-D7)), in a jail and, since 2026-09-28, at the
host notch too ([OQ-HC1](host-computed-layer.md#OQ-HC1), which superseded [ML-D8](#ML-D8)). On
2026-09-29: the parts of [§14](#14-how-each-agents-menu-is-set-researched-2026-09-29) that
neither [OQ-MM1](#OQ-MM1) nor [OQ-MM3](#OQ-MM3) decides — the `models` kind, the enforcement
switch, each agent's rendering under an `only`, claude's routed picker and tier pins, and the
four defects [§14](#14-how-each-agents-menu-is-set-researched-2026-09-29) found ([§14.5](#145-what-was-built-2026-09-29)). On
2026-09-30: `yolo check`'s currency warning ([MM-D16](#MM-D16), [§9](#9-currency-the-agents-catalogs-plus-a-staleness-warning))
and claude's conventional tier aliases ([MM-D17](#MM-D17)). Also on 2026-09-30, each after the
measurement it waited on, recorded in [§14.4](#144-build-order): pi's refusing wrapper
([MM-D6](#MM-D6), [MM-D21](#MM-D21); [§14.6](#146-what-was-built-2026-09-30)), on
`openai-codex` too once a second measurement followed the subscription login through it
([MM-D23](#MM-D23)), and codex's
prelaunch catalog on `openai-codex` ([MM-D9](#MM-D9), [MM-D22](#MM-D22)), whose Bedrock half
the measurement ruled out. codex's menu at `yolo host` is built too, where the launch's
provider is the configured profile's ([§14.7](#147-codexs-menu-at-yolo-host-designed-2026-09-30),
[§14.8](#148-what-was-built-at-the-host-2026-09-30)); what a `-p` over another provider should do
there waits on [OQ-MM5](#OQ-MM5). copilot's `providers.json` ([MM-D10](#MM-D10)) is stopped: its
measurement found that the file's models join GitHub's own, which the decision did not expect,
and that is now [OQ-MM4](#OQ-MM4). MEASURED at
`ee8154f2` (2026-09-24): a pack's provider `models` is a flat alias → id map; the object form of
a model is user config only; `packs/claude/derive.lua` and `packs/pi/derive.lua` hard-coded the
`openai-codex` ids then; packs/claude's `bedrock` provider declares no `models`. SOURCED
2026-09-25 from AWS's model cards: the Anthropic-on-Bedrock geographic prefixes are `us.`, `eu.`,
`au.`, `jp.` and `global.`, and the set differs per model
([§5.3](#53-the-prerequisite-verify-before-an-id-ships)). Read 2026-09-29, and no longer
unmeasured: Claude Code's gateway model discovery survives
`CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` but cannot carry a non-Anthropic id, and codex's
`model_catalog_json` is a file of full catalog entries
([§14](#14-how-each-agents-menu-is-set-researched-2026-09-29)). Code claims cite a symbol, never
a line.

**The question this doc answers.** When you pick a provider with `-p`, something has to decide
which model the agent starts on and which models its menu offers. Usually the agent can decide
that itself. Sometimes it cannot. Then yolo has to pick. This doc says where yolo picks, where
those picks live, how a company changes them, what each agent's menu shows, and how the ids stay
current.

**Where things stand.**

- **Ruled 2026-09-25:** yolo picks model ids where an agent cannot just default, and ships them
  in a built-in pack that comes with yolo, not in core ([OQ-BR3](#OQ-BR3)). That also answers
  provider-switching's "does yolo ship the ids?" ([OQ-PSW3](#OQ-PSW3)).
- **Built, 2026-09-27:** the `openai-codex` list moved into data, in the provider's own pack.
  That is [OQ-ML1](#OQ-ML1)'s option (b), which the question's 2026-09-29 ruling kept: each
  provider declares its own default in its own pack ([ML-D1](#ML-D1)). pi writes no model scope
  for it any more ([ML-D2](#ML-D2)).
- **Built, 2026-09-29:** the Bedrock list, declared once on the `bedrock` provider in its own
  pack, as [OQ-ML1](#OQ-ML1)'s ruling says, each entry naming its maker, and each agent's pick
  made by [OQ-ML2](#OQ-ML2)'s rule over the entries that agent can call ([ML-D9](#ML-D9)). No
  picker renders it yet ([OQ-BR13](#OQ-BR13)).
- **Built, 2026-09-29:** a pack can shape any provider's list with the `models` kind
  ([OQ-BR12](#OQ-BR12)), a profile's `enforce_models` switch governs every refusal yolo installs
  ([MM-D5](#MM-D5)), and each agent renders a list an `only` narrowed as [§14.1](#141-the-table) says, except
  codex's catalog, copilot's whole list and pi's refusal. What a list that only adds renders, and
  whether a list no `only` narrowed refuses on a gateway, are unchanged and wait on
  [OQ-MM1](#OQ-MM1) and [OQ-MM3](#OQ-MM3) ([§14.5](#145-what-was-built-2026-09-29)).
- **Built, 2026-09-30, each on a measurement [§14.4](#144-build-order) records:** pi's refusal,
  a `streamSimple` wrapper in its registration of a narrowed list ([MM-D21](#MM-D21),
  [MM-D6](#MM-D6)), and on `openai-codex` in the registration that carries the subscription
  login, whose path through the wrapper was measured on its own ([MM-D23](#MM-D23)); and
  codex's exact menu on `openai-codex`, a catalog its launcher writes from codex's own before
  the exec ([MM-D9](#MM-D9), [MM-D22](#MM-D22); [§14.6](#146-what-was-built-2026-09-30)).
- **Built, 2026-09-30, as designed the same day:** codex's menu at `yolo host`, from a list the
  launch composes for itself ([MM-D24](#MM-D24) to [MM-D28](#MM-D28);
  [§14.7](#147-codexs-menu-at-yolo-host-designed-2026-09-30),
  [§14.8](#148-what-was-built-at-the-host-2026-09-30)). The design found that a host `-p` does not
  choose codex's provider, which only the configured profile does there, so the menu is built only
  where the two agree, and which should win is [OQ-MM5](#OQ-MM5).
- **Stopped, 2026-09-30, on its measurement:** copilot's `providers.json`
  ([MM-D10](#MM-D10)). The file's providers are additive to GitHub's own, so a copilot signed in
  to GitHub would show GitHub's models beside the list and send a GitHub pick to GitHub
  ([OQ-MM4](#OQ-MM4)).
- **Moved here on 2026-09-25**, with their ids unchanged: [OQ-BR3](#OQ-BR3) and
  [OQ-BR12](#OQ-BR12)–[OQ-BR15](#OQ-BR15) from [`bedrock-plumbing.md`](bedrock-plumbing.md), and
  [OQ-PSW1](#OQ-PSW1) and [OQ-PSW3](#OQ-PSW3) from the retired `provider-switching.md`.
  Those two carried the old prefix `PS`, as `PS1` and `PS3`, until later that day, when that doc's series was
  renamed `OQ-PSW` because [`provisioner-sets.md`](provisioner-sets.md) also uses `OQ-PS`.

**Needs your ruling:** [OQ-MM1](#OQ-MM1) first, because it decides what most agents' menus
show; then [OQ-MM3](#OQ-MM3); then [OQ-MM4](#OQ-MM4), filed 2026-09-30, on copilot's whole list;
then [OQ-MM5](#OQ-MM5), filed the same day, on what a host `-p` means for codex. Decided
2026-09-30 as implementation choices, each reversible: [OQ-BR14](#OQ-BR14) ([MM-D16](#MM-D16)) and [OQ-PSW1](#OQ-PSW1) ([MM-D17](#MM-D17)). Ruled 2026-09-29:
[OQ-ML1](#OQ-ML1), [OQ-ML2](#OQ-ML2) and [OQ-BR12](#OQ-BR12). [OQ-BR13](#OQ-BR13) was directed
the same day (set the model selection however each agent allows) and is researched in
[§14](#14-how-each-agents-menu-is-set-researched-2026-09-29). That research also settled two
questions without asking: [OQ-BR15](#OQ-BR15) on evidence (the bridge serves no model list), and
[OQ-MM2](#OQ-MM2) on a corrected premise (copilot's key already sits in a 0600 per-agent file).

- [OQ-ML1](#OQ-ML1): the shape of the built-in picks pack. Ruled 2026-09-29: there is no separate
  picks pack. Each provider declares its own default in its own pack, and that default layers
  like all config.
- [OQ-ML2](#OQ-ML2): which provider × agent cases get a yolo pick. Ruled 2026-09-29, narrower
  than the leaning: yolo picks a model only to make a session valid, and never steers a valid
  choice unless the config opts in.
- [OQ-BR12](#OQ-BR12): a `models` contribution kind so a company pack can shape a list. Ruled
  2026-09-29: yes, with `add` and `only`, for any provider. The engineer's own config writes last.
- [OQ-BR13](#OQ-BR13): how each derive renders the list into its agent's picker. Directed
  2026-09-29: however each agent allows; the mechanisms are
  [§14](#14-how-each-agents-menu-is-set-researched-2026-09-29)'s table.
- [OQ-MM1](#OQ-MM1): what a list that only adds does to an agent's own catalog. Leaning: it sits
  beside the catalog, and only an `only` makes a menu exact.
- [OQ-MM2](#OQ-MM2): where copilot's model file gets its key, if it cannot read it from the
  environment. Settled 2026-09-29: in a second file beside the 0600 per-agent env file that
  already holds that key ([MM-D10](#MM-D10)).
- [OQ-MM3](#OQ-MM3): whether a list with no `only` refuses other models on a gateway that serves
  more than the list, and what keeps claude's start valid where nothing refuses. Leaning: refuse
  by default. With the switch off, yolo writes the start model once through claude's selection.
- [OQ-MM4](#OQ-MM4): whether copilot should show a whole list in the only mode that can, which
  also shows GitHub's own models. Leaning: no; copilot keeps one model.
- [OQ-MM5](#OQ-MM5): whether a `-p` at `yolo host` moves codex onto its provider for that
  launch, so codex's menu there can follow the `-p`. Leaning: yes, through the `CODEX_HOME` yolo
  already rebuilds at every launch with its login.
- [OQ-BR14](#OQ-BR14): how the lists stay current. Decided 2026-09-30 ([MM-D16](#MM-D16)): the
  agents' own catalogs, plus a `yolo check` warning. Built 2026-09-30, reading the catalog files
  an agent's pack declares ([MM-D19](#MM-D19)); today pi's alone.
- [OQ-PSW1](#OQ-PSW1): claude's derive reads `balanced`/`fast`. Decided 2026-09-30
  ([MM-D17](#MM-D17)): yes, keeping `sonnet`/`haiku` as synonyms. Built 2026-09-30.
- [OQ-BR15](#OQ-BR15): the bridge serves `GET /v1/models`. Settled 2026-09-29 on the
  measurement it waited on: never ([MM-D4](#MM-D4)).

**Out of scope, and where it lives instead.** Refusing an off-list model at the wire:
[`wire-bridge-gateway.md`](wire-bridge-gateway.md#OQ-WG3), which reads this doc's effective
list. Which vendors each agent can call on Bedrock, and the per-agent filter table:
[`bedrock-plumbing.md`](bedrock-plumbing.md#OQ-BR9). Clearing a model id when you stop
selecting a profile: [`OQ-PSW2`](../reference/providers.md#oq-psw2). Withholding a
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
  vocabulary that the retired `provider-switching.md`'s 2026-09-24 version proposed,
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
Bedrock model. copilot goes through the bridge. One provider holds every family: that was
[OQ-BR9](bedrock-plumbing.md#OQ-BR9), ruled and built 2026-09-29 ([ML-D9](#ML-D9)).

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
- **The `openai-codex` list is one declaration** (BUILT 2026-09-27, [ML-D1](#ML-D1); until then
  `packs/claude/derive.lua` and `packs/pi/derive.lua` each hard-coded it, and pi's extension
  carried a third copy that had drifted from both). The `openai-codex` provider in
  [`packs/openai-auth/pack.json`](../../packs/openai-auth/pack.json) declares its ids with an
  order, a name, a description, a context window, and whether each has a 1M-context variant,
  which every consumer lists right after its base as `<id>[1m]`. The consumers:
  - claude: `availableModels` with `enforceAvailableModels`, `modelPicker.options` with
    `replaceBuiltInOptions`, and the env that pins the start model and the retained Default row.
    Its output is byte-identical to the literals it replaced;
  - codex: `model`;
  - pi: the `defaultProvider`/`defaultModel` pair, and pi-subagents' `modelScope.allow` as the
    exact ids ([ML-D5](#ML-D5));
  - pi's extension, which registers exactly the list, from a data file yolo renders in a jail
    ([ML-D3](#ML-D3)). `yolo host apply` renders the same file at the host since
    [OQ-HC1](host-computed-layer.md#OQ-HC1), superseding [ML-D8](#ML-D8).

  The first declared id is the default wherever the profile names no model. pi writes no
  `enabledModels` for `openai-codex` ([ML-D2](#ML-D2)). For every other provider pi renders
  `enabledModels` from the provider's `models` map, sorted with the default first.
- **No pack can add to or narrow another pack's provider list.** `KindProvider` combines
  exclusively. `config-list` appends to a list but cannot narrow one, and refuses a `profile` gate
  (`internal/packdecl`, `configlist_test.go`).
- **packs/claude's `bedrock` provider is a bare name**, with no `models`. With no profile `model`
  option, the claude env derive emits no model variable and Claude Code chooses its own.
  ⚠ **Changed 2026-09-29** ([ML-D9](#ML-D9)): the provider is `packs/bedrock`'s now, with a list
  of four entries of two makers, and claude's derive still emits no model variable unless a
  profile or a `default` alias names an Anthropic one.
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
  on Converse (MEASURED count). opencode 1.18.32's embedded models.dev has 166 Bedrock entries
  (MEASURED 2026-09-29 in the installed binary; the research pass's 179 was never re-counted).
  claude and copilot have no catalog for a non-Anthropic model.

---

## 4. Where yolo picks: the cannot-default cases

The ruling's test is whether the agent can pick if yolo says nothing. Candidates, each with why:

| Provider × agent | If yolo writes nothing | Evidence | Pick? (under [OQ-ML2](#OQ-ML2)'s leaning) |
| :--- | :--- | :--- | :--- |
| Bedrock runtime × codex | codex starts on its own default slug. Its catalog maps slugs to mantle's bare ids, and holds runtime `global.` ids for Terra and Luna only. An id it does not know runs on fallback metadata (D4) | INFERRED from codex 0.156.1 strings; not run ([bedrock-plumbing evidence](bedrock-plumbing.md#14-evidence-and-how-to-re-check-it)) | **yes**; built 2026-09-29, the first OpenAI entry ([ML-D9](#ML-D9)) |
| Bedrock runtime × pi | pi's catalog lists the bare `openai.gpt-5.6-sol` against a runtime base URL: the wrong-spelling 404 bedrock-plumbing's P1 names, shipped in a vendor catalog | pi-ai 0.87.1 source, read not run ([bedrock-plumbing §4](bedrock-plumbing.md#4-what-each-agent-can-actually-do)) | **yes**; built 2026-09-29, the list's first entry |
| Bedrock runtime × opencode | its default for `amazon-bedrock` is unread | UNMEASURED | **yes until measured**, then drop if opencode's own default is callable; built 2026-09-29, the list's first entry |
| claude everything profile | claude's built-in options are Anthropic names; it has no idea a Kimi or GPT id exists | [OQ-BR11](bedrock-plumbing.md#OQ-BR11); claude 2.1.282 | **yes** |
| copilot through the bridge | `COPILOT_MODEL` unset: copilot has no Bedrock catalog | `packs/copilot/derive.lua` | **yes** (the first callable entry of the same list) |
| `openai-codex` × claude, pi, codex | already shipped as picks, and since 2026-09-27 in data: one declaration on the provider, in its own pack ([§3](#3-what-exists-today), [ML-D1](#ML-D1)) | [`packs/openai-auth/pack.json`](../../packs/openai-auth/pack.json) | **yes, already**, in data |
| claude native profile (`bedrock`) | Claude Code resolves its own tier words to Anthropic models, older ones on Bedrock | vendor docs, 2026-09-04 | **no** under the leaning: the family is right; built 2026-09-29 as no pick ([ML-D9](#ML-D9)) |
| first-party `anthropic` provider × claude | Claude Code's own current defaults | — | **no** |
| gateway packs (OpenRouter, Kilo) | the user curates, per [OQ-GP2](gateway-provider-packs.md#decision-ledger) | — | **no** |

⚠ **The leaning costs one planned behavior.** The retired `provider-switching.md`
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

The retired `provider-switching.md` proposed shipping an endpoint-less `anthropic`
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

So every provider is expected to declare four conventional aliases, and may declare more:

| Alias | Means | Today |
| :--- | :--- | :--- |
| `default` | what you get when nothing is said | cerebras and llamacpp, and every derive's fallback |
| `fast` | cheap and quick | nowhere; zai's aliases became its wire ids (`8e901423`) |
| `balanced` | the middle tier, where one exists | nowhere |
| `frontier` | the most capable tier | nowhere; added 2026-09-28 by [OQ-XM2](../research/extension-model-defaults.md#OQ-XM2) |

A derive resolves one for the selected provider with `yolo.model_for(alias)`, which returns
`provider/id` and warns when a provider lacks one of the four
([OQ-XM1](../research/extension-model-defaults.md#OQ-XM1),
[XM-D1](../research/extension-model-defaults.md#XM-D1)).

They are capability-shaped, not vendor-shaped, because a vendor tier name (`sonnet`, `terra`)
cannot survive a switch to another vendor. **It is a convention with a warning, not an enum.** A
provider missing one gets one launch warning naming which, and the launch proceeds. A provider
that also declares `sol` has four aliases, and that is not an error.

**claude is where it bit.** Its env derive read the vendor names `sonnet` and `haiku`
literally. Decided 2026-09-30 ([OQ-PSW1](#OQ-PSW1), [MM-D17](#MM-D17)) and built the same day:
it reads `balanced` → `ANTHROPIC_DEFAULT_SONNET_MODEL` and `fast` → `ANTHROPIC_DEFAULT_HAIKU_MODEL`,
keeping `sonnet`/`haiku` as synonyms so no user config breaks, and `frontier` for the opus
tier where a tier pin reads an alias. The vendor name wins where a provider declares both, and a
name whose model claude's client cannot call gives way to the tier's other name
([MM-D18](#MM-D18)). Whenever a list replaces
claude's built-ins (a routed list, or an `only`), every tier, the Fable tier
(`ANTHROPIC_DEFAULT_FABLE_MODEL`) included, is pinned to a list id: the tier's own alias where
the list declares one, and the default entry otherwise. A list that sits beside claude's own rows
pins only the tiers it names by alias ([MM-D2](#MM-D2)).

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
  for that provider. Today's string-form maps keep working unchanged. *As built
  ([MM-D11](#MM-D11)): it composes over the shaped list per alias, the way it composes over a
  provider's own list today, so an alias, an addition and a null each win; replacing the list
  whole would have ended the one-alias recovery [ML-D4](#ML-D4) documents. Every `add` also
  applies before every `only`, whatever the pack order.*
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

Each derive writes the effective list into its own agent's picker ([OQ-BR13](#OQ-BR13)). The
table below is the summary; each agent's mechanism, what it refuses and the evidence are
[§14](#14-how-each-agents-menu-is-set-researched-2026-09-29), researched 2026-09-29.

| Agent | Picker surface | Notes |
| :--- | :--- | :--- |
| claude | `modelPicker.options`, `availableModels` | `availableModels` puts the resolved default first, then the rest in list order; `modelPicker` uses list order. claude's Default row always stays ([§14.2](#142-what-each-row-rests-on)) |
| pi | an extension's registration of exactly the list; `models.json` rows for a list that adds | built for `openai-codex` ([ML-D2](#ML-D2), [ML-D3](#ML-D3)) and generalized by [MM-D6](#MM-D6). `enabledModels` alone is a soft shortlist, never a boundary ([`provider-credential-scope.md`](provider-credential-scope.md#241-what-pis-enabledmodels-actually-constrains)) |
| opencode | its provider `whitelist` | also refuses every other model ([MM-D7](#MM-D7)) |
| oh-omp | `enabledModels` in `config.yml`; `models.yml` rows | the scope fails open ([MM-D8](#MM-D8)) |
| codex | `model_catalog_json` | full entries copied from codex's own catalog at prelaunch ([MM-D9](#MM-D9)), built 2026-09-30 for `openai-codex` ([MM-D22](#MM-D22)), at `yolo host` too where the launch's provider is the configured profile's ([MM-D25](#MM-D25)); on every other provider, the selection only |
| copilot | one `COPILOT_MODEL`; later `providers.json` | the whole list once [MM-D10](#MM-D10) is built |

Rules:

- **Written once per launch** at the boot render, as managed layers, like every derive output.
- **An interactive `/model` choice still wins** until yolo's own selection changes (the
  `selection` namespace, unchanged).
- **claude's built-in options are replaced on every routed provider and under an `only`**
  ([MM-D1](#MM-D1)), which revises the 2026-09-25 plan of replacing them only on the everything
  profile: on a routed provider they only restate the tier pins ([MM-D2](#MM-D2)) under Anthropic
  labels. What the native profile shows for a list that only adds is [OQ-MM1](#OQ-MM1).
- **A refusal is set only where the list is the provider's whole universe or an `only` narrowed
  it**, and only while the profile's enforcement switch is on ([MM-D5](#MM-D5)). A list that
  merely adds beside an agent's own catalog must never lock a user out of that catalog.
  `openai-codex` is a whole universe whose service serves nothing else, so it keeps its
  enforcement. Whether a whole-universe list refuses on a gateway that serves more than the list,
  such as z.ai, OpenRouter or Kilo, is [OQ-MM3](#OQ-MM3).
- **The GPT-6 lists moved into data** (BUILT 2026-09-27, [ML-D1](#ML-D1)): into the provider's
  own pack rather than the picks pack of [§5.1](#51-its-shape). claude's rendered `openai-codex`
  output stayed byte-identical, and a test fails when any consumer's read of the declaration is
  replaced by a literal. Two outputs changed on purpose: pi writes no `enabledModels` for it
  ([ML-D2](#ML-D2)), and pi-subagents' `modelScope.allow` is the exact declared ids, not a
  wildcard ([ML-D5](#ML-D5)).

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

**As built, 2026-09-30** ([MM-D16](#MM-D16), [MM-D19](#MM-D19), [MM-D20](#MM-D20)). An agent's
catalog is what its pack declares in the new `model_catalog` field of an npm `program`: JSON files
inside the installed package, whose objects' `id` strings are the ids it knows. `yolo check`
(`modelCatalogReport`, in the Packs section, over the composition the launch runs) reads them where
the agent is installed, the jail's npm prefix in a jail and yolo's floor copy at the host when the
floor holds the copy `yolo host --` runs, and runs nothing. Only pi's pack declares one, pi-ai's per-provider data files. The installs in this jail
of codex 0.145.0, copilot 1.0.48 and opencode 1.18.32 ship no catalog file (MEASURED 2026-09-30:
no JSON file but their package manifests), and opencode's and oh-omp's catalogs are built into
their binaries ([§14.2](#142-what-each-row-rests-on)), so none of those declares one. Read against
the pi 0.99.1 installed in this jail, the shipped lists draw two warnings: `us.openai.gpt-6.1-sol`
on `bedrock` and `glm-4.6` on `zai` (MEASURED 2026-09-30, a unit run with the real install as the
npm prefix). With nothing readable installed, one skip says it could not check.

---

## 10. Later: the bridge's `GET /v1/models`

Claude Code's `CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY` fills its picker from a gateway's
`/v1/models`. If the wire bridge served that from the effective list, claude's picker would follow
the list with no settings write ([OQ-BR15](#OQ-BR15)). Today the bridge answers only
`POST /v1/messages` ([WB-D14](../reference/wire-bridge.md#wb-d14)'s refusal path covers every
other path). There is nothing upstream to proxy: runtime has no model-list endpoint, so the
bridge would serve the list yolo composed, from memory. That is not "discovery" in
[providers.md](../reference/providers.md#what-this-does-not-license)'s sense.

**Settled 2026-09-29: never** ([MM-D4](#MM-D4)). The thing to measure first was whether
discovery survives the `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` the claude derive sets on
every routed launch. It does, but discovery keeps only ids containing "claude" or "anthropic",
never runs on claude's native profile, and is hidden once the built-ins are replaced, and no
other agent reads such a route ([§14.3](#143-the-bridges-part-and-get-v1models)). So
[WB-D14](../reference/wire-bridge.md#wb-d14)'s one-route surface stays.

---

## 11. Forbidden, and non-goals

**Forbidden:**

- a model id in core: no Go table, no translation table;
- a network call for models at launch;
- a refusal, `enforceAvailableModels` included, on a list that only adds beside an agent's own
  catalog ([MM-D1](#MM-D1), [MM-D5](#MM-D5)). A list on a gateway that serves more than it is
  [OQ-MM3](#OQ-MM3)'s;
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
- **No closed alias vocabulary.** `default`/`fast`/`balanced`/`frontier` warn; they never refuse.
- **No enforcement at the wire.** Refusing an off-list model in the request path is
  [`wire-bridge-gateway.md`](wire-bridge-gateway.md#OQ-WG3)'s. The refusals an agent can make
  itself, listed in [§14.1](#141-the-table), follow that doc's switch ([MM-D5](#MM-D5)).

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
   GPT-6 lists into data under the byte-identical test ([OQ-BR13](#OQ-BR13)). The move itself
   is BUILT (2026-09-27), into the provider's own pack rather than a `models` kind
   ([ML-D1](#ML-D1)); a `models` kind would take over that declaration. Picker rendering per
   agent has its own order, [§14.4](#144-build-order), whose first step needs no `models` kind.
2. **The picks pack** ([OQ-ML1](#OQ-ML1)), with the cases [OQ-ML2](#OQ-ML2) rules, each id dated;
   Anthropic ids only after the prefix check ([§5.3](#53-the-prerequisite-verify-before-an-id-ships)).
   provider-switching's three-line `models` map for claude's native `bedrock` provider lands here
   only if [OQ-ML2](#OQ-ML2) gives that profile a pick.
3. **The alias vocabulary**: the default-only warning, the claude derive's synonym reading, and a
   line in `providers.md` naming the three ([OQ-PSW1](#OQ-PSW1)). The synonym reading and the
   `providers.md` line were built 2026-09-30 ([MM-D17](#MM-D17)).
4. **First-party providers** for claude, with no picks under [OQ-ML2](#OQ-ML2)'s leaning
   ([§5.4](#54-first-party-providers-are-a-use-of-this)).
5. **The `yolo check` staleness warning** ([OQ-BR14](#OQ-BR14)). Built 2026-09-30
   ([MM-D16](#MM-D16), [§9](#9-currency-the-agents-catalogs-plus-a-staleness-warning)).
6. ~~A bridge `GET /v1/models`~~: dropped 2026-09-29, never to be built ([MM-D4](#MM-D4)).

**Done when:**

1. A company pack with `only` over five ids makes exactly those five appear in claude's, pi's,
   opencode's and codex's pickers, each filtered to what that agent can call: in the pack's
   order except opencode's, which sorts by release date, and below claude's Default row. oh-omp
   shows the same five only on a provider it has a list for: the via row under yolo's `bedrock`
   key, or a key omp does not ship. omp's native Bedrock client gets no list
   ([MM-D8](#MM-D8)).
   copilot's menu is the same five once [MM-D10](#MM-D10) is built, its first entry until then;
   whether that can be met at all is [OQ-MM4](#OQ-MM4)'s, since the file that carries the list
   also brings GitHub's models. codex's picker is exact on `openai-codex` alone, since its
   catalog carries no other provider's ids ([MM-D22](#MM-D22)).
   With the profile's enforcement switch on, each agent that can refuse
   ([§14.1](#141-the-table)) refuses a sixth id.
2. The `openai-codex` pickers render byte-identically to today after the GPT-6 lists move into
   data, and the test fails if the render's call site is deleted. Met for claude on 2026-09-27;
   pi's changed on purpose, since it gets no scope for `openai-codex` ([ML-D2](#ML-D2)).
3. `yolo check` warns about an id no installed catalog knows. With no agent installed, it says it
   could not check. Met 2026-09-30 for the catalogs a pack can declare, pi's today
   (`internal/cli/check/modelcatalog_test.go`).
4. A provider declaring only `default` produces one warning naming the two missing aliases, and a
   working launch.
5. `yolo -p bedrock -- codex` with no user model config starts on a pick, and a company pack's
   `add` list for the same provider replaces the pick.
6. *(Only if [OQ-ML2](#OQ-ML2) gives claude's native profile a pick.)* On claude, `-p anthropic`
   and `-p bedrock` put the same tier word on the right id, with no hand-editing.

---

## 14. How each agent's menu is set (researched 2026-09-29)

This section answers [OQ-BR13](#OQ-BR13)'s direction, *"set the model selection however we
can"*, one agent at a time. Three research passes read each agent's shipped bundle or source at
the version named. pi's model code was also loaded as a library, with an isolated home and no
network. No agent CLI was run and no model request was sent. Every claim is marked
MEASURED (observed in a shipped bundle or a library load), SOURCED (read in vendor source or
docs) or INFERRED. Vendor code is cited as file and line at the version named, which does not
move; yolo's own code is cited by symbol, as everywhere else in this doc.

**Versions read:** Claude Code 2.1.285, the release `packs/claude`'s installer tracks. copilot
1.0.89, npm's latest on 2026-09-29. This jail's copilot 1.0.48 is a leftover: the copilot pack is not selected
here, so no launcher updates it (MEASURED: `~/.yolo/bin/launch` has no copilot launcher). pi
0.87.1, the copy the jail's launcher had installed when it was read, and 0.99.1, published
2026-09-29; both were measured on 2026-09-29. oh-omp 0.15.3, the version
`packs/omp` pins; 0.15.4 changes no model code. opencode 1.18.32. codex 0.158.0.

Three words used below:

- An **exact menu** *(coined here)*: the agent's model menu for the selected provider offers the
  effective list's models and no others. It is not a refusal. A menu can be exact while the agent
  still runs a model typed on its command line.
- A **refusal** is the agent, or the wire bridge, declining a request for a model outside the list.
  The bridge's is [OQ-WG3](wire-bridge-gateway.md#OQ-WG3)'s: one list, and an enforcement switch
  on the profile that defaults on. It can act only on the **bridge path**
  ([wire-bridge-gateway §6](wire-bridge-gateway.md#6-part-5--all-traffic-through-the-bridge-new-direction-design)),
  never on an agent's native client, and for pi, oh-omp, opencode and codex that means a **via
  route** ([wire-bridge-gateway §4](wire-bridge-gateway.md#4-part-3--the-sign-only-openai-chat-completions-route-ruled)).
- A list is the provider's **whole universe** *(coined here)* when the agent has no catalog of its
  own for that provider, so yolo's list is the only way a model reaches its menu: every provider
  claude is routed to through `ANTHROPIC_BASE_URL`, every copilot provider, and every provider key
  pi, oh-omp or opencode does not ship. There an exact menu takes nothing away from the menu. Not
  the same as an `only`, which narrows a provider the agent may well have a catalog for, and not a
  claim that the service behind it serves nothing else: a gateway usually serves more.

### 14.1 The table

What each agent can show, and what refuses. "Under an `only`" marks what renders when a `models`
contribution narrowed the list; what a list that only adds renders beside an agent's own catalog
is [OQ-MM1](#OQ-MM1).

| Agent, path | Shows exactly the list by | Other models refused by | Build step |
| :--- | :--- | :--- | :--- |
| **claude, native profile** (its own Bedrock client) | Never quite exactly. `modelPicker` with `replaceBuiltInOptions: true` shows claude's Default row, which cannot be removed, then the list, plus the session's current model when that is off the list (MEASURED). With `availableModels` and `enforceAvailableModels`, Default keeps the tier default when that tier's model is listed, and otherwise takes the first listed entry shaped like an Anthropic id (MEASURED, [§14.2](#142-what-each-row-rests-on)) | claude's own `availableModels`, client side, with four gaps ([§14.2](#142-what-each-row-rests-on)). The bridge never sees this path | Under an `only`, the three keys; every tier pinned to a list id beside `CLAUDE_CODE_USE_BEDROCK` ([MM-D1](#MM-D1), [MM-D2](#MM-D2), [MM-D5](#MM-D5)) |
| **claude, routed** (`ANTHROPIC_BASE_URL` at the bridge, or at a gateway's own Anthropic endpoint) | `modelPicker` with the built-ins always replaced, since the list is the whole universe: Default, then the list. Built today for `openai-codex` only | claude's `availableModels` where it renders: on `openai-codex` and under an `only`, and on a gateway's list as [OQ-MM3](#OQ-MM3) rules. On the bridge path, the bridge's allowlist, which also sees claude's background requests | Generalize the `openai-codex` branch of `yolo.derive("claude", "settings")` to every routed provider; pin every tier; stop steering the start model ([MM-D1](#MM-D1) to [MM-D3](#MM-D3)) |
| **copilot** (through the bridge on Bedrock and the other bridged providers; direct otherwise, [§14.2](#142-what-each-row-rests-on)) | Today one entry, `COPILOT_MODEL`, because copilot's environment-variable setup carries one model (SOURCED). A `providers.json` file, present in 1.0.89 and absent in 1.0.48, holds a provider and a row per model, so the whole list can show (SOURCED) | No copilot setting refuses a model configured this way (SOURCED). On a bridged provider the bridge's allowlist is the only refusal, and every copilot request passes it. On a direct provider nothing refuses | After four measurements, a `copilot/providers` surface, or a file beside copilot's env file if the key cannot come from the environment ([MM-D10](#MM-D10)). **Stopped 2026-09-30**: the file's models join GitHub's own ([§14.4](#144-build-order), [OQ-MM4](#OQ-MM4)) |
| **pi, a provider pi ships** (`amazon-bedrock`, `zai`, `cerebras`, `openrouter`, `openai-codex`) | An extension's `registerProvider(<id>, { models })` replaces that provider's list and keeps pi's own client, address and credential (MEASURED on 0.87.1 and 0.99.1). Built for `openai-codex` ([ML-D3](#ML-D3)). A `models.json` row cannot narrow: it adds beside pi's catalog | Every menu path is bound to the list, but `--model <provider>/<unlisted id>` still runs, with a warning (MEASURED). A `streamSimple` wrapper in the same registration refuses it in-process, and hands a listed model to pi's own stream with pi's own credential (MEASURED 2026-09-30 on 0.99.1's shipped bundle against a mock endpoint, [§14.4](#144-build-order)). The bridge only on a via route | Generalize `pi/codex-models` into one per-provider data file; register exactly under an `only`; add the wrapper under the switch ([MM-D6](#MM-D6)). **Built 2026-09-30** ([MM-D21](#MM-D21)), and on `openai-codex` in the list's own registration beside the subscription login, after a measurement of its own ([MM-D14](#MM-D14), [MM-D23](#MM-D23)) |
| **pi, a provider yolo defines** (a key pi does not ship: yolo's `bedrock` via row, `kilo`, `llamacpp`) | Already exact: a `models.json` row under a key pi does not ship is the whole list (MEASURED) | The same `--model` gap and the same wrapper, delegating to pi's api registry since pi ships no provider of that id (MEASURED 2026-09-30 on 0.99.1 for `kilo`); the bridge on a via route | Nothing for the menu; the wrapper as above, **built 2026-09-30** |
| **oh-omp** (0.15.3) | `enabledModels` in `~/.oh-omp/agent/config.yml`: the selector then shows only that scope, with no "all" view (SOURCED). It fails open when no pattern matches. omp's own Bedrock catalog stops at Claude 4.6, and `models.yml` cannot add a Converse model, so a current Bedrock list exists only as the via row under yolo's `bedrock` key, which is exact | `--model` refuses an id omp does not know, but accepts any known model outside the scope, and `modelRoles` can name one too (SOURCED). The bridge on the via row | A new `oh-omp/settings` surface writing `enabledModels`, with the start model through the selection ([MM-D8](#MM-D8)) |
| **opencode, native** (`amazon-bedrock`, and yolo providers named like catalog ones: `openrouter`, `kilo`, `zai`, `cerebras`) | `provider.<id>.whitelist` set to the list's ids, each also declared under `provider.<id>.models` (SOURCED). The order stays opencode's: newest release date first | The whitelist itself: any other model fails as "Model not found". Hiding and refusing cannot be separated. The bridge on a via route | Write the whitelist; take the display name from the entry, never the alias ([MM-D7](#MM-D7)) |
| **codex** (0.158.0) | `model_catalog_json`, a file of full catalog entries applied at startup for every provider; `priority` sets the order and `display_name` the name (SOURCED). The entries must be copied from codex's own catalog: one with no instructions runs codex with empty system instructions | Nothing in codex: an off-catalog model runs on fallback metadata (SOURCED). The bridge on a via route, which `openai-codex` never has ([WG-I21](wire-bridge-gateway.md#WG-I21)) | A new prelaunch step that reads codex's bundled catalog and writes the filtered file; last ([MM-D9](#MM-D9)). **Built 2026-09-30** for `openai-codex`; the bundled catalog is OpenAI's whatever the provider, so Bedrock runtime gets none ([§14.4](#144-build-order), [MM-D22](#MM-D22)). At `yolo host`, **built 2026-09-30** where the launch's provider is the configured profile's ([§14.8](#148-what-was-built-at-the-host-2026-09-30), [MM-D25](#MM-D25)) |

### 14.2 What each row rests on

**claude**, 2.1.285.

- **Keys and scopes** (SOURCED, [settings reference](https://code.claude.com/docs/en/settings-reference.md)
  and [model configuration](https://code.claude.com/docs/en/model-config.md)). `modelPicker` is
  `{ options: [{ model, label, description, behavesAs }], replaceBuiltInOptions }`, read only from
  managed settings, `--settings` and user settings, never a project's. `availableModels` may sit in
  any file: a managed list replaces the rest, otherwise user, project and local lists concatenate.
  `enforceAvailableModels` may sit in any file but is ignored whenever a managed source exists.
  `availableModelsMatch` and `deniedModels` are managed-only. `/model`'s Enter saves `model` into
  user settings. yolo writes these keys to `~/.claude/settings.json`, the `claude/settings`
  surface.
- **The Default row stays** (MEASURED, binary offset 201940036:
  `if(s.replaceBuiltInOptions===!0)return[...e.filter((k)=>k.value===null),...S]`). A curated row the
  allowlist drops is removed, and when every row is dropped claude falls back to its built-ins.
  "Default, resolving to the list's default, then the list" is the closest claude gets, and it is
  what yolo renders for `openai-codex` today, where the tier pins, not the allowlist, make
  Default the declared default.
- **What Default resolves to under enforcement** (MEASURED by reading 2.1.285's enforcement
  fallback, `function Fd(e,n,r)` at offset 198349654; not run). This replaces the unmeasured
  claim, made here before, that Default resolves to the list's first entry. Default keeps the
  tier default whenever that tier's model is allowed. Only when it is not does claude walk
  `availableModels` in order (offset 198351832). It skips every entry that fails `Od()`, which
  accepts an id matching `^((us|eu|apac|jp|au|us-gov|global)\.)?(anthropic\.|claude-)`
  (`fD`, offset 198357325), a Bedrock ARN, or anything on Foundry. An id shaped like
  `gpt-6-sol` or `glm-5.3` is first rewritten to `claude-gpt-6-sol` (`vs=/^[a-z]+-\d/`, offset
  198352336), and what claude then selects for it is unresolved. When no entry survives, claude
  logs *"keeping the tier default"* (offset 198353071). So on a routed or everything list of
  Kimi, GPT or GLM ids, list order does not decide what Default resolves to. The tier pins do
  ([MM-D2](#MM-D2)).
- **What `availableModels` refuses** (MEASURED at offsets 198322072 and 198322244; SOURCED,
  model-config "Restrict model selection"): `/model <x>` for an unlisted id; an unlisted
  `--model`, `ANTHROPIC_MODEL` or saved `model`, each replaced at startup by Default; a subagent's
  or teammate's model, which falls back; a skill's model override; and a tier variable pointing
  outside the list. A plain id matches exactly and, at user scope, also by `-`-segment prefix,
  with a `claude-` prefix tried as well (gap 1; `$r` at offset 198322072, `OM` beside `ny`). So a
  plain `gpt-6-sol` also admits `gpt-6-sol-<anything>` and `claude-gpt-6-sol…`.

> [!WARNING]
> **Four gaps in claude's own refusal**, all on the client, all MEASURED in 2.1.285 unless marked:
>
> 1. **At user scope an entry matches by prefix**, one `-` segment at a time (offset 198318674,
>    `function ny(e,n)`), so `us.anthropic.claude-opus-5` also admits `us.anthropic.claude-opus-5-5`,
>    a different model ([§5.3](#53-the-prerequisite-verify-before-an-id-ships) has both). The exact
>    form is managed-only.
> 2. **Haiku background requests, hooks and helper requests that pick their own model, and the
>    auto-mode classifier's Opus fallback on non-Anthropic providers are not restricted** (SOURCED,
>    the `availableModelsMatch` description and model-config). Pinning every tier to a list id
>    ([MM-D2](#MM-D2)) routes them onto the list.
> 3. **A repository's `.claude/settings.json` can add entries** (lists concatenate) **and switch
>    enforcement off.** It cannot change `modelPicker`. That is the engineer's own config having the
>    last word, as [OQ-BR12](#OQ-BR12) was ruled.
> 4. **Any managed source switches user-level enforcement off silently** unless it sets both keys
>    itself (offsets 198355187 and 198349782): `/etc/claude-code/managed-settings.json`, an MDM
>    profile, or a `managed-settings.d` directory. A managed `availableModels` also replaces yolo's
>    list. No container jail has `/etc/claude-code` (MEASURED here); the host notch and a managed Mac
>    can. Default then resolves by tier: on Bedrock the opus tier (offset 198349496); behind a
>    gateway, unresolved in the minified code, so every tier is pinned.

- **Gateway model discovery cannot carry the list** (MEASURED + SOURCED,
  [gateway protocol](https://code.claude.com/docs/en/llm-gateway-protocol.md)). It does survive
  `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` since 2.1.257, the fact [§10](#10-later-the-bridges-get-v1models)
  waited on. But it keeps only ids matching `/(claude|anthropic)/i` (offset 198300426), never runs
  while any `CLAUDE_CODE_USE_*` is set, adds only models `availableModels` allows, and
  `replaceBuiltInOptions` hides what it found. So a bridge `/v1/models` could never carry a GPT or
  Kimi entry ([MM-D4](#MM-D4)).
- **The claude env derive steers against [OQ-ML2](#OQ-ML2)'s ruling today** (SOURCED + read).
  `ANTHROPIC_MODEL` outranks the saved `model`, and claude returns to it at every launch
  (model-config, "Set a default model for new sessions"). `yolo.env("claude")` sets it on every
  `openai-codex` launch. On any other provider it sets it whenever a model resolves: the
  resolved `model` option names an alias in the provider's `models` map, or the map has a
  `default` alias, or, with neither, the option is set to anything but `default`, which is then
  used as a literal id. A map with no such alias and no option emits nothing. The resolved option
  includes the provider's declared default, which the derive cannot tell from the profile's own
  value (`packload.ResolvedProfile`), so a plain `-p zai` sets `glm-5.3` at every launch. A valid
  `/model` choice is overridden each launch. Its `openai-codex` branch also pins only the opus
  tier. Both are implementation fixes ([MM-D2](#MM-D2), [MM-D3](#MM-D3)). Outside `openai-codex`
  the steering fix waits on [OQ-MM3](#OQ-MM3), since that pin is today the only check on claude's
  start there. **Fixed 2026-09-29** where the allowlist renders — `openai-codex` and a list an
  `only` narrowed, while `enforce_models` is on: every tier is pinned to the list, and
  `ANTHROPIC_MODEL` only on `pin_model` ([§14.5](#145-what-was-built-2026-09-29)).
- **Ids and windows** (SOURCED). Picker rows take full provider-form ids verbatim; for Bedrock the
  docs' own example is `us.anthropic.claude-opus-4-8`. Provider prefixes are not stripped and
  `[1m]` is stripped on both sides. The context window is one global value
  (`CLAUDE_CODE_MAX_CONTEXT_TOKENS`); `[1m]` is its only per-row variation. `behavesAs` (2.1.257+)
  lends a row a known Claude model's capabilities.

**copilot**, 1.0.89, read from the `copilot.tgz` inside `@github/copilot-linux-x64`'s
single-executable binary.

- **Which path copilot takes** (read in `packs/copilot/derive.lua` and `packload.adaptEndpoints`).
  The derive sends copilot to the provider's `anthropic` endpoint when there is one, and to its
  `openai` endpoint otherwise. Core composes the bridge's address as the `anthropic` endpoint of
  every provider that offers `openai` and lacks `anthropic`, when the bridge is served and a
  selected agent speaks `anthropic`. So in a jail copilot goes through the bridge on Bedrock,
  Cerebras and Kilo, with the launch's caller token. It goes direct to z.ai's, OpenRouter's and
  llama.cpp's own Anthropic endpoints, with the provider's own key. At the host notch Cerebras and
  Kilo go direct too, since copilot speaks their own wire
  ([wire-bridge.md](../reference/wire-bridge.md#at-the-host-notch)).
- **Environment-variable setup is one model** (SOURCED, the `copilot help providers` text in
  `app.js`): `COPILOT_MODEL`, or `COPILOT_PROVIDER_MODEL_ID` with `COPILOT_PROVIDER_WIRE_MODEL`, and
  a model is mandatory. `packs/copilot`'s env derive writes the one under the profile's alias. There
  is no discovery for these models, and a second one cannot be picked in the TUI
  ([copilot-cli#3795](https://github.com/github/copilot-cli/issues/3795),
  [#3282](https://github.com/github/copilot-cli/issues/3282), both open).
- **`providers.json`** (SOURCED,
  [config-dir reference](https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-config-dir-reference#providersjson)):
  an object with `providers` and `models`, at `COPILOT_PROVIDERS_CONFIG` or `~/.copilot/providers.json`,
  which overrides every `COPILOT_PROVIDER_*` variable once it declares anything. Present in
  1.0.89 and absent from 1.0.48 (MEASURED: no match in its `app.js`); which release added it is
  unread. GitHub publishes no schema. The fields are INFERRED from the
  SDK built for 1.0.89 (`@github/copilot-sdk` 1.0.15: `NamedProviderConfig` with `apiKey` and
  `bearerToken`, `ProviderModelConfig` with `wireModel`), which the native validator's messages
  match. A picker selection is `<provider>/<id>`.
- **Its credential is literal text.** A derive never receives the hydrated key
  ([providers.md](../reference/providers.md#derives-the-delivery-mechanism)). An `apiKeyCommand`
  field appears among the provider fields in the native runtime's strings, but the 1.0.15 SDK's
  types carry no such field (MEASURED 2026-09-29), so whether the file accepts one is UNMEASURED.
  Where the key goes if it does not is [OQ-MM2](#OQ-MM2), settled as [MM-D10](#MM-D10) says.
  Read again on 2026-09-30 ([§14.4](#144-build-order)): the shape 1.0.89 documents takes no
  variable, and `apiKeyCommand` is still unread.
- **GitHub's own models join the menu.** `app.js` merges these models into copilot's own list
  (`mergeByokModelList`), so a copilot also signed in to GitHub may show GitHub's models beside
  yolo's (INFERRED here; MEASURED 2026-09-30 on 1.0.89, [§14.4](#144-build-order): the file's
  providers are additive to GitHub's, and a GitHub model picked there is GitHub's to serve). That
  is [OQ-MM4](#OQ-MM4).
- **No copilot setting refuses these models** (SOURCED, the config-dir reference). The repository's
  `.github/allowed_models.txt` "cannot filter out custom models added using BYOK". A host
  `allowedModels` list exists only as an SDK session option, with no flag or variable. The managed
  `model` key is a default only.
- ⚠ **Background requests.** copilot's help names prompt refinement and session and branch naming as
  separate requests, and the runtime holds hard-coded small-model ids such as `gpt-4o-mini`. Which
  ids those carry in this mode must be measured before the bridge's default-on refusal applies to
  copilot, or it breaks them loudly (INFERRED).
- **A side defect for `packs/copilot`**, outside this doc: since 1.0.35 user settings live in
  `~/.copilot/settings.json`, and copilot moves any it finds in `config.json` there at startup
  (SOURCED, the changelog's 1.0.35 entry; MEASURED present in 1.0.48, whose `app.js` holds
  *"Settings migration: moved ${m} setting(s) from config.json to settings.json"*). The pack
  writes its status-line defaults into `config.json` as a read-modify-write surface, so every
  boot rewrites what copilot then drains. That holds at 1.0.48, the version the copilot env
  derive's provenance cites. **Fixed 2026-09-29** ([MM-D15](#MM-D15)): the default goes to a
  `copilot/settings` surface at `~/.copilot/settings.json`.

**pi**, 0.87.1 and 0.99.1.

- **The list is built in layers** (SOURCED, 0.99.1 `dist/core/provider-composer.js`:332-352): pi's
  catalog (plus a pi.dev overlay on the catalog only); then `models.json`, which adds or replaces
  by id and never removes (137-170); then an extension's `registerProvider` with `models`, which
  replaces the provider's whole list (171-178; `docs/custom-provider.md`:30); then metadata-only
  overrides. 0.87.1 has the same semantics.
- **Registration makes a pi-shipped provider exact** (MEASURED on both versions, library load,
  `PI_OFFLINE=1`, a dummy `AWS_BEARER_TOKEN_BEDROCK`). `amazon-bedrock` has 176 entries on 0.99.1
  and 165 on 0.87.1. A `models.json` row makes it 177. A registration of three ids makes it exactly
  3, including an id absent from the catalog, all on `bedrock-converse-stream` to the built-in
  endpoint, with the credential pi's own chain finds. `models: []` hides a provider entirely.
- **A `models.json` row is exact only under a key pi does not ship** (MEASURED on 0.99.1). yolo's
  `bedrock` row gives exactly its 2 models; an `openrouter` row with one model gives 398; the `zai`
  row gives 8, pi's 7 plus yolo's `glm-4.6`. So the `zai`, `cerebras` and `openrouter` lists yolo
  renders for pi today are not exact menus.
- **A registered model gets only `api` and `baseUrl` from pi** (SOURCED, provider-composer.js:111-136).
  Reasoning, cost, window and input types must come with each definition, from `getBuiltinModel`,
  as `yolo-openai-auth.js` already does. Both helpers are still exported in 0.99.1, from a module
  pi's extension loader provides (`virtual-modules.js`:28).
- **`--model` escapes the list** (MEASURED on both versions). `pi --model amazon-bedrock/eu.anthropic.claude-opus-5-5`
  against an exact list builds a custom model and prints *"Model … not found for provider … Using
  custom model id."* (`model-resolver.js`:130-143, 431-456); nothing downstream checks the id
  (`model-runtime.js`:447-477). Every other path is bound to the list: `/model` and its Tab "all"
  view, Ctrl+P, `/model <term>`, RPC `set_model`, and `enabledModels` patterns.
- **pi can refuse in-process** (MEASURED on 0.99.1). A registration carrying `api` and a
  `streamSimple` wrapper that throws for an off-list id and hands a listed one to pi's own provider
  object ended the `--model` turn above with *"yolo: amazon-bedrock/eu.anthropic.claude-opus-5-5 is
  not in this jail's model list"* (provider-composer.js:360-363; pi-ai `dist/api/lazy.js`:36-46).
  The listed id reached the delegate; the real Bedrock stream behind it was not called then. That
  the delegate authenticates as pi's own path does was MEASURED on 2026-09-30 against a mock
  endpoint ([§14.4](#144-build-order)): pi resolves the credential into the stream's options before
  it calls the wrapper. It covers the `--model` fallback,
  resumed sessions and an extension's `setModel`, not `--no-extensions`. The weaker hooks cannot
  refuse: a throw in `before_provider_request` is caught and the request goes ahead
  (`extensions/runner.js`:1060-1088).

> [!NOTE]
> **Two claims elsewhere are now wrong for pi.** [wire-bridge-gateway §6](wire-bridge-gateway.md#6-part-5--all-traffic-through-the-bridge-new-direction-design)
> says the bridge is the only place a refusal can live, and [OQ-CN4](provider-credential-scope.md#OQ-CN4)
> rests on pi's only lever on the "all" view being the credential. A registration narrows the "all"
> view, and a wrapper refuses. Neither changes what [OQ-CN4](provider-credential-scope.md#OQ-CN4)
> ruled, since withholding stays the security property. A note is owed in both docs.

- **Narrowing does not reopen the shadowing trap** (MEASURED for the models-only form). A
  registration passing only `models` keeps the built-in provider's address, wire and credential,
  so it works however [pi-codex-provider-shadowing OQ-3](pi-codex-provider-shadowing.md#OQ-3) is
  ruled. The refusing form cannot be models-only: pi 0.99.1's `validateExtensionProvider` throws
  *"\"api\" is required when registering streamSimple"* (provider-composer.js:326-328), so it
  carries `models`, `api` and `streamSimple`. That this form still authenticates through pi's own
  chain was INFERRED here, and MEASURED on 2026-09-30 ([§14.4](#144-build-order)).
- **pi 0.99.0 renamed its `openai-codex` provider "OpenAI Codex (legacy)"** (SOURCED, pi-ai
  `dist/providers/openai-codex.js`:9). yolo's registration sets no `name`, and the composed name
  falls back to the built-in's (provider-composer.js:378), so pi 0.99 labels yolo's provider
  "(legacy)" (INFERRED). **Fixed 2026-09-29:** the registration names the provider "OpenAI
  Codex" (`yolo-openai-auth.js`). 0.99.0 also gives pi's `openai` provider a ChatGPT login, which yolo does
  not use, and adds `registerVirtualModel()`, which adds rows rather than filtering them.
- **Catalog currency** (MEASURED counts). 0.87.1's `openai-codex` catalog lacks `gpt-6.1-sol`,
  yolo's declared default; 0.99.1 has it. A jail still on 0.87.1 registers it with defaults, and
  [ML-D7](#ML-D7) warns. On 0.99.1 pi's own default after `/login` is `gpt-6.1-sol`, which the list
  has, so [ML-D4](#ML-D4)'s "not available" message should stop (INFERRED).
- **Whether the shipped `pi` bundle resolves the extensions' dynamic catalog import** was
  UNMEASURED here. Unbundled library use fell back to defaults on both versions; [ML-D3](#ML-D3) and
  [ML-D7](#ML-D7) measured success through pi's own loader at 0.87.1. **MEASURED 2026-09-30 on
  0.99.1** for the specifier both extensions import, `@earendil-works/pi-ai/providers/all`
  ([§14.4](#144-build-order)): the npm package's `pi` runs `dist/bundle/cli.js`, a bundled build
  whose extension loader serves that specifier as a virtual module, and `yolo-model-lists.js`
  loaded through it named zai's models from pi's catalog. `yolo-openai-auth.js` itself was not
  loaded.

**oh-omp**, tag v0.15.3 source and the shipped `@oh-labs/oh-omp-linux-x64` binary.

- **`models.yml` adds or replaces by provider and id, and cannot narrow a built-in provider**
  (SOURCED, `packages/coding-agent/src/config/model-registry.ts`:900-932). A key omp does not ship is
  exact. Its `api` enum has no `bedrock-converse-stream` (181-191, 243-253). The Bedrock catalog has
  94 entries whose Anthropic ids end at `claude-opus-4-6`; the codex static list ends at `gpt-5.4`,
  and codex models are otherwise discovered online.
- **An extension's registration does not last** (SOURCED, model-registry.ts:1778-1862 and 785-832;
  the same code in the binary at offsets 106789493 and 106820899). It needs a base URL and a key,
  `models: []` does nothing, and the next refresh rebuilds from the catalog and `models.yml`. The
  selector refreshes every time it opens without a scope (`model-selector.ts`:295-305, binary offset
  123442148), and so do login and logout.
- **The `enabledModels` scope is exact** (SOURCED, `settings-schema.ts`:253; `main.ts`:655-662;
  `model-resolver.ts`:622-715; model-selector.ts:127-128 and 295-301). It is resolved once at
  startup, the selector shows only the scope, and it fails open when no pattern matches. `--model`
  resolves against every registry model and refuses an unknown one (717-811; binary offset
  106849446 holds "not found. Use --list-models"). `modelRoles` (`default`, `smol`, `slow`, `plan`,
  `commit`) may name models outside the scope.
- Upstream oh-my-pi's `main` appears to keep runtime registrations across reloads. It was read
  lightly and is not relied on here.

**opencode**, tag v1.18.32 source, read and not run.

- **Keys** (SOURCED, `packages/core/src/v1/config/config.ts`:68-79; `provider.ts`:13-126):
  `enabled_providers`, `disabled_providers` (checked first), `model`, `small_model`, and per
  provider `whitelist`, `blacklist` and `models`.
- **The filter** (SOURCED, `packages/opencode/src/provider/provider.ts`:1448-1452 and 1671-1719).
  After the catalog, plugins and config merge, the loader drops every provider not allowed and
  applies `whitelist` and `blacklist` to every model left, the user's own included, matching on
  opencode's model key. The TUI lists exactly that. Any other model fails in `getModel`
  (1871-1893); an invalid `--model` is skipped quietly; an off-list `small_model` falls back to the
  main model (1941-1946).
- **Order is opencode's** (SOURCED, `packages/tui/src/component/dialog-model.tsx`:23-129 and
  186-196): each provider's models newest release date first, then by title, under Favorites and
  Recent. A made-up `release_date` is the only lever, and yolo does not fake one.

> [!WARNING]
> **Two defects in what `packs/opencode` renders today.** *Catalog collision* (MEASURED on the
> installed 1.18.32 binary): `openrouter`, `kilo`, `zai` and `cerebras` are also catalog ids, and
> opencode starts a config provider from the catalog entry of the same id (provider.ts:1483-1491).
> So `-p openrouter` offers the whole catalog, about 374 models, plus the user's map; about 381 for
> `kilo`, 18 for `zai`, 2 for `cerebras`. Native `amazon-bedrock` has 166
> ([§3](#3-what-exists-today)). Whether that is a defect or intended is
> [OQ-MM1](#OQ-MM1). *Display name* (INFERRED): the derive sets each model's `name` to its yolo
> alias, and a config name beats the catalog's, so an entry under `default` shows as "default".
> The same holds for pi and oh-omp: `packs/pi/derive.lua` builds `{ id = modelId, name = alias }`
> unless the entry's `model_options` names it, and `packs/omp/derive.lua` builds
> `{ id = id, name = alias }` always, so cerebras's and llamacpp's one model shows as "default"
> there too. Fixed for all three by [MM-D7](#MM-D7), built 2026-09-29: a row's name is the
> entry's own `name` fact, or none (`modelDisplayName`, one text in the three derives).

- **Bedrock** (SOURCED, `packages/core/src/models-dev.ts`:160-247; provider.ts:301-460). The catalog
  is built in and refreshed hourly from models.opencode.ai; a whitelist makes the menu immune to
  that drift. opencode adds a region prefix to a bare Claude or Nova id from `AWS_REGION` at request
  time, so the menu's id and the wire's can differ, which matters only to a bridge allowlist.
- `experimental.policies` works per provider only, and in 1.18.32 only `opencode debug v2` reads it
  (SOURCED). Config layers replace arrays whole (remeda `mergeDeep`), so a repository's
  `opencode.json` can replace yolo's whitelist, the engineer's own config again having the last
  word. `/etc/opencode` outranks everything but MDM.

**codex**, tag rust-v0.158.0 source; the installed 0.158.0 binary.

- **The picker** (SOURCED, `codex-rs/models-manager/src/manager.rs`:168-180 and 725-731;
  `protocol/src/openai_models.rs`:942-975; `tui/src/chatwidget/model_popups.rs`:82-106 and 207-228).
  It sorts the catalog by `priority`, shows entries whose `visibility` is `list`, and marks the
  first as the default. `/model` takes no inline argument, so the picker is the only way to choose
  in the TUI.
- **`model_catalog_json = "<absolute path>"`** (SOURCED, `config/src/config_toml.rs`:396-399;
  `core/src/config/mod.rs`:2129-2158; `model-provider/src/provider.rs`:546-617). A top-level key,
  also allowed per codex profile and in managed requirements. At startup it swaps in a static
  catalog for every provider, the ChatGPT subscription and Bedrock runtime included, with no fetch.
  The file is `{"models": [ModelInfo, …]}` with at least one model, the shape
  `codex debug models [--bundled]` prints. That is the format this doc's status line called unread.
- ⚠ **An entry cannot be thin** (SOURCED, openai_models.rs:404-479;
  `prompts/src/model_instructions.rs`:8-17). Ten fields are required, and an entry with no
  `model_messages.instructions_template` or `base_instructions` runs codex with **empty** system
  instructions, logging only a warning. Bundled entries are 20 to 65 KB each, mostly prompt text
  (MEASURED). So entries are copied from codex's own catalog, and copied entries must clear
  `upgrade` and `availability_nux` (`amazon_bedrock/catalog.rs`:112-113), or codex's migration
  prompt steers users off the list: in 0.158.0's catalog `gpt-5.6-*` and `gpt-5.5` carry an
  upgrade to `gpt-6-sol` or `gpt-6-luna`.
- **Without the file** (SOURCED, manager.rs:480-620). On a ChatGPT login codex lists its bundled
  catalog merged with the subscription's backend list, cached for 300 s. So `-p codex`'s picker
  shows the subscription's list rather than yolo's three ids (INFERRED; the backend list is
  UNMEASURED). A custom provider gets codex's bundled OpenAI catalog, so `-p openrouter` on codex
  lists OpenAI model names against a non-OpenAI endpoint (INFERRED).
- **codex refuses nothing** (SOURCED, manager.rs:201-225 and 647-684). An explicit model runs on
  fallback metadata with a warning. `review_model`, `default_subagent_model`, the memories models
  and codex's hard-coded background models bypass the picker (codex-auto-review, or `gpt-5.6-luna`
  on an API key; `global.openai.gpt-5.6-luna` and `-terra` on Bedrock runtime).
- **`gpt-6.1-sol`, yolo's declared default, is absent from the installed 0.158.0 binary**
  (MEASURED: `grep -a -c` counts 0), whose catalog has `gpt-6-sol`. 0.159.1's catalog adds it at
  priority 1 (SOURCED). 0.159.2's bundled catalog holds all three of the subscription's ids
  (MEASURED 2026-09-30, [§14.4](#144-build-order)).

### 14.3 The bridge's part, and `GET /v1/models`

The bridge's allowlist exists once an `only` has narrowed the list, and only while the profile's
switch is on ([OQ-WG3](wire-bridge-gateway.md#OQ-WG3)). It reaches copilot only on a bridged
provider (Bedrock, and in a jail Cerebras and Kilo, [§14.2](#142-what-each-row-rests-on)),
claude on any profile whose base URL is the bridge, and pi, oh-omp, opencode and codex only on a
via route. It never reaches an agent's native client: claude's native profile, pi's, opencode's
and codex's own Bedrock clients, and codex's `openai-codex`, which has no via route
([WG-I21](wire-bridge-gateway.md#WG-I21)). Nor does it reach copilot or claude on a gateway's own
endpoint, such as z.ai's.

Before its default-on refusal applies to an agent, that agent's background traffic has to be on the
list. For claude, pinning every tier does it ([MM-D2](#MM-D2)). For codex, the derive points
`review_model` and the memories models at listed ids. For copilot, the ids are measured first
([§14.2](#142-what-each-row-rests-on)). **Built 2026-09-30**
([`wire-bridge-gateway.md` §6.1](wire-bridge-gateway.md#61-how-the-allowlist-is-built)): until
those two steps are, codex's and copilot's packs declare `unlisted_background_models`, and the
bridge admits their requests whole and logs an off-list model
([WG-I41](wire-bridge-gateway.md#WG-I41)).

**No agent would read a `GET /v1/models`.** claude's discovery drops every id without "claude" or
"anthropic" in it, never runs on the native profile, and is hidden once the built-ins are replaced.
opencode never asks an OpenAI-compatible or Bedrock provider for its models (SOURCED, provider.ts:671
and 1661-1669). codex asks only with `model_catalog_url` plus an off-by-default feature, and
expects its own format. copilot has no such discovery. So the bridge never serves one
([MM-D4](#MM-D4)).

### 14.4 Build order

1. **claude, on today's provider maps.** Render `modelPicker` for every routed provider and pin
   every tier ([MM-D1](#MM-D1), [MM-D2](#MM-D2)). The allowlist keys stay where they are today,
   on `openai-codex` alone, so that is where `ANTHROPIC_MODEL` moves onto the opt-in
   ([MM-D3](#MM-D3)). On every other provider today's pin is the only thing keeping claude's
   start valid, so it stays until [OQ-MM3](#OQ-MM3) says what replaces it. Also the display
   names in opencode's, pi's and oh-omp's rows ([MM-D7](#MM-D7)). Nothing new is needed but the
   opt-in's option. **Built 2026-09-29.**
2. **The `models` kind with `only`** ([§13](#13-build-order-and-what-done-looks-like) step 1),
   which every "under an `only`" cell waits on. **Built 2026-09-29.**
3. **Under an `only`, the menus:** claude's native `modelPicker`, pi's generalized registration
   (after the nested-jail check of the bundled catalog import), and oh-omp's `enabledModels`.
   **Built 2026-09-29**, all but the nested-jail check, which a real launch still owes.
4. **The refusal switch** ([MM-D5](#MM-D5)), and with it every refusal it governs: claude's
   allowlist beyond `openai-codex` (where [OQ-MM3](#OQ-MM3) puts it), opencode's whitelist,
   which cannot hide without refusing, pi's wrapper, and the bridge's allowlist that
   [`wire-bridge-gateway.md`](wire-bridge-gateway.md#8-build-order) builds, each after that
   agent's background ids are on the list. **No refusal ships before its off switch does**, so
   nothing before this step refuses beyond what `openai-codex` refuses today. **Built
   2026-09-29** for the switch, claude's allowlist under an `only` and opencode's whitelist, and
   **2026-09-30** for the bridge's allowlist
   ([`wire-bridge-gateway.md` §6.1](wire-bridge-gateway.md#61-how-the-allowlist-is-built)), which
   admits codex's and copilot's requests whole, their background ids not being on the list yet;
   and **2026-09-30** for pi's wrapper, after its measurement below ([MM-D21](#MM-D21)), and on
   `openai-codex` after a second one, of the subscription login ([MM-D23](#MM-D23)).
5. **copilot's `providers.json`** ([MM-D10](#MM-D10)), after its four measurements. **Stopped
   2026-09-30** on the second of them, recorded below, and filed as [OQ-MM4](#OQ-MM4). Not built.
6. **codex's catalog at prelaunch** ([MM-D9](#MM-D9)), last, because it alone needs a new
   mechanism. **Built 2026-09-30** for `openai-codex`, after its measurement below
   ([MM-D22](#MM-D22)); the Bedrock half is not, because that measurement did not hold.

**Measurements it waits on**, each readable from a shipped bundle or against a mock provider,
never a real model, and each marked here as it is taken:

- **Whether a pi registration carrying the refusal wrapper still authenticates through pi's own
  chain. MEASURED 2026-09-30 on pi 0.99.1**, npm's latest that day. pi's shipped bundle
  (`dist/bundle/index.js`, the build the package's `pi` command runs) was loaded as a library under
  Node 24, with an isolated home and `PI_OFFLINE=1`; pi's own resource loader discovered the shipped
  `yolo-model-lists.js` beside a list file, and one request per case went through pi's
  `ModelRuntime` to a mock endpoint on 127.0.0.1. No session ran and no model was reached. On
  `amazon-bedrock`, redirected with `AWS_ENDPOINT_URL_BEDROCK_RUNTIME`, a listed id reached the mock
  carrying `Authorization: Bearer <AWS_BEARER_TOKEN_BEDROCK>`, and with SigV4 keys an
  `AWS4-HMAC-SHA256 Credential=<AWS_ACCESS_KEY_ID>/…` signature. The model pi's own `--model`
  resolver built for `eu.anthropic.claude-opus-5-5` ended with yolo's refusal and sent nothing. On
  `zai`, a models.json row over a provider pi ships, and `kilo`, one pi does not ship, a listed id
  reached the mock with the row's `${VAR}` key as its bearer token, and an unlisted one was refused.
  The models-only form, run as the control, sent the unlisted id every time. It supports
  [MM-D6](#MM-D6) as decided, and it is built ([MM-D21](#MM-D21)).
- **The shipped `pi` bundle's catalog import. MEASURED the same day, in the same runs:** the bundled
  loader serves `@earendil-works/pi-ai/providers/all` as a virtual module, and the extension named
  zai's models from pi's catalog (`GLM-5.3`, not the bare id).
- **Whether the subscription login, which `openai-codex`'s registration carries, reaches a listed
  model through the same wrapper. MEASURED 2026-09-30 on pi 0.99.1**, still npm's latest, from
  the published `@earendil-works/pi-coding-agent` and `@earendil-works/pi-ai` tarballs, read and
  then loaded as before. Read: pi composes a registration's `oauth` into the provider's auth
  whatever else the registration carries, and hands every model of the registration's `api` to
  its `streamSimple` (`composeModelProvider`, `dist/core/provider-composer.js`). Before it calls
  that, pi resolves the credential (`ModelRuntime.prepareRequest`, `dist/core/model-runtime.js`):
  for a stored `oauth` credential, the kind yolo's prelaunch writes into `auth.json`, it calls the
  registration's `refreshToken` under pi's lock when fewer than five minutes remain, then puts
  `getApiKey(credential)` into the options as `apiKey` (`resolveStoredOAuth`, pi-ai
  `dist/auth/resolve.js`). pi's own `openai-codex-responses` stream reads the account id out of
  that token and sends it as `chatgpt-account-id` beside `Authorization`
  (`extractAccountId`, pi-ai `dist/api/openai-codex-responses.js`). Loaded: the built
  `yolo-openai-auth.js`, its address pointed at a mock on 127.0.0.1 and nothing else changed,
  discovered by pi's own resource loader beside the shipped list with `enforce` on and an
  `auth.json` entry shaped as yolo's prelaunch writes it. A listed `gpt-6.1-sol` reached the mock
  with `Authorization: Bearer <the stored access token>` and the account id from the token, on
  the websocket handshake and on the SSE request pi fell back to. `gpt-5.5`, the custom model
  pi's own `--model` resolver (`resolveCliModel`) built with its *"not found … Using custom model
  id"* warning, ended with yolo's refusal and sent nothing. With the stored credential expired,
  pi called the extension's `refreshToken` once, a stand-in for `yolo internal
  openai-auth-client` answered, the listed `gpt-6-astra` reached the mock with the refreshed
  token, and `auth.json` then held the new broker marker. The shipped extension without the
  wrapper, run as the control, and the built one with `enforce` off both sent `gpt-5.5`. It
  supports [MM-D6](#MM-D6) on `openai-codex`, and it is built ([MM-D23](#MM-D23)).
- **copilot's four, read 2026-09-30 in copilot 1.0.89**, npm's latest that day: the package
  embedded in `@github/copilot-linux-x64`'s single-executable binary (its `app.js`, the
  `copilot-sdk` it bundles, `schemas/api.schema.json`, and the strings of
  `prebuilds/linux-x64/runtime.node`, the native runtime). Nothing was run. What each one found:
  - **Whether `providers.json` takes its key from the environment. MEASURED: not as the package
    documents the file.** `app.js` reads the file named by `COPILOT_PROVIDERS_CONFIG`, else
    `providers.json` in copilot's config directory, and hands its `providers` and `models` arrays
    unchanged to the native runtime (`providerContextCreateFromConfig`), expanding nothing. The
    provider shape the package documents, `NamedProviderConfig` in `api.schema.json`, allows no
    other property and carries the credential only as a literal `apiKey` or `bearerToken`, or as
    `hasBearerTokenProvider`, which asks an SDK host for a token and has no host when the CLI
    reads a file. The `COPILOT_PROVIDER_*` variables stop applying once the file declares
    anything. The native runtime's struct has 11 fields to the schema's 10, and an
    `apiKeyCommand` string sits among its provider keys, so whether the file takes a key command
    is still UNMEASURED: strings cannot show which struct owns it. For [MM-D10](#MM-D10) this is
    its second branch, a file beside `agent-env/copilot.sh`, and no contradiction.
  - **Whether a signed-in copilot merges GitHub's models. MEASURED: yes, and this contradicts
    [MM-D10](#MM-D10).** The bundled SDK's `SessionConfig.providers` says named providers are
    *"additive: they coexist with Copilot API auth so models from CAPI and one or more BYOK
    providers can be mixed within a single session"*, while with the environment variables, in
    copilot's own help, *"the CLI uses this provider instead of GitHub Copilot's model routing"*.
    And `app.js` merges the file's models (`mergeByokModelList`) into the list GitHub's API
    returns. So in the file's mode a copilot signed in to GitHub shows GitHub's models beside the
    list, a GitHub model picked there is served by GitHub rather than the profile's provider, an
    `only` no longer decides copilot's menu, and the bridge never sees that request. MM-D10 promised the
    list, [§13](#13-build-order-and-what-done-looks-like)'s "copilot's menu is the same five".
    The item is stopped and the choice is [OQ-MM4](#OQ-MM4). Whether the file's mode starts at
    all with no GitHub login is decided in the native runtime: UNMEASURED.
  - **Whether `COPILOT_MODEL` or the settings `model` accepts `<provider>/<id>`. MEASURED in
    part:** every file model is selected as `provider/id` (the schema's `ProviderModelConfig.id`),
    and `app.js` hands `--model`, the settings `model` and `COPILOT_MODEL` to the native runtime
    (`modelCliStartupConfiguration`), whose rule is UNMEASURED.
  - **Which ids copilot's background requests carry. UNMEASURED.** The native runtime picks
    them; only a copilot process against a mock provider would show them, and this build starts
    none.
- **Whether `codex debug models --bundled`, with the Bedrock runtime provider selected, prints the
  Bedrock runtime catalog (`amazon_bedrock/catalog.rs`) rather than the OpenAI one, offline and
  credential-free. MEASURED 2026-09-30 on codex 0.159.2**, npm's latest that day, read in its
  source at tag `rust-v0.159.2` and confirmed in the shipped `linux-x64` binary's strings; nothing
  was run. **It does not.** `run_debug_models_command` (`cli/src/main.rs`) answers `--bundled`
  with `bundled_models_response()` alone, the `models-manager/models.json` compiled into the
  binary, before it builds a config or a provider, so no provider selection reaches it. That
  catalog is OpenAI's: 11 entries, the subscription's `gpt-6.1-sol`, `gpt-6-astra` and
  `gpt-6-luna` among them, each 20 to 65 KB of prompt text. Bedrock runtime's catalog is built in
  memory by the Bedrock provider's models manager (`static_runtime_model_catalog`, the `global.`
  and `us.` spellings of seven OpenAI slugs). Only `codex debug models` without `--bundled`
  reaches it, a run that loads codex's whole config and auth first, and whether that run is
  offline and credential-free on Bedrock is UNMEASURED. So MM-D9's Bedrock half does not hold as
  decided, and by its own rule Bedrock runtime gets no file: its ids are not in the catalog
  `--bundled` prints. The openai-codex half holds: `--bundled` is offline and credential-free,
  and codex's own test of it (`cli/tests/debug_models.rs`) runs it with an empty `CODEX_HOME`.
  **Two more facts the build rests on, read the same way.** A `model_catalog_json` naming no file
  stops codex's config from loading (`load_catalog_json` in `core/src/config/mod.rs` reads the
  file with `?`, and the binary holds its *"failed to parse model_catalog_json path"* and
  *"must contain at least one model"* messages). And `-c` is a global, appending flag, whose
  root-level values rank below those given after a subcommand, and whose value, when it does not
  parse as TOML, is taken as a string, which an absolute path never parses as
  (`utils/cli/src/config_override.rs`). Both decide [MM-D22](#MM-D22).
- **What the subscription's backend list holds. UNMEASURED**, and neither a shipped bundle nor a
  mock provider can answer it: it is the live answer of the ChatGPT backend for one account. The
  menu yolo builds replaces it for the session ([MM-D9](#MM-D9)), so no build waits on it.

### 14.5 What was built (2026-09-29)

Built: every part of [§14.4](#144-build-order) that neither [OQ-MM1](#OQ-MM1) (what a list that only adds does to
an agent's own catalog) nor [OQ-MM3](#OQ-MM3) (whether a list no `only` narrowed refuses on a
gateway, and what keeps claude's start valid where nothing refuses) decides. Where the code
meets one of those questions it keeps today's rendering, with a comment naming the question.
Each row's test drives the production call site: the boot render, `packload.AgentEnv`, or
`packload.ComposeProviders`.

| Piece | What it does now | Pinned by |
| :--- | :--- | :--- |
| The `models` kind ([OQ-BR12](#OQ-BR12)) | `{"kind": "models", "provider": …, "add": […]}` or `"only": […]`, any provider, one verb per contribution. `packload.ComposeProviders` applies every `add` in pack order, then every `only`, which drops every entry it does not name, adjacent ones included (`TestAModelsOnlyDropsAdjacentEntries`), then the user's own `providers.<name>.models` per alias, and marks a narrowed entry `models_only` ([MM-D11](#MM-D11)). `yolo check` names a duplicate, an `only` id nothing added and a provider nothing declares. `description` joins a user's object-form entry ([§7.3](#73-two-fields-the-entry-shape-is-missing)) | `internal/packdecl/models_test.go`, `internal/packload/modellists_test.go`, `internal/cli/check/modellists_test.go` |
| The switch ([MM-D5](#MM-D5)) | `enforce_models`, a profile field that defaults on, reaches both derive paths as `ctx.enforce_models` ([MM-D13](#MM-D13)). It governs claude's allowlist (on `openai-codex` too) and opencode's whitelist | `internal/entrypoint/enforcemodels_test.go` |
| claude, routed ([MM-D1](#MM-D1), [MM-D2](#MM-D2)) | Every routed provider's list replaces the built-ins in `modelPicker`, each row spelled as the tier pins spell it, and every tier (fable included) is pinned to a list id with its name. No allowlist on a list no `only` narrowed, and the start pin stays there ([OQ-MM3](#OQ-MM3)). A provider whose pack ships no list (OpenRouter, Kilo) writes no picker until a list is declared for it | `TestClaudeOnARoutedProviderShowsTheListAndPinsEveryTier`, `TestClaudeOnABridgedProviderShowsTheList`, `TestClaudeOnAGatewayWithNoListKeepsItsMenu` |
| claude, `openai-codex` ([MM-D2](#MM-D2), [MM-D3](#MM-D3)) | Every tier on the default entry; `ANTHROPIC_MODEL` only on `pin_model`, since the allowlist keeps the start valid; with `enforce_models` off, no allowlist and today's start pin | `TestClaudeOnTheCodexListPinsTheStartOnlyOnTheOptIn`, `TestClaudeOnTheCodexListWithEnforcementOff` |
| claude, under an `only` | Native: the list's Anthropic entries as `modelPicker`, the allowlist while the switch is on, every tier pinned in `settings.json`'s `env` beside `CLAUDE_CODE_USE_BEDROCK`. Routed: the allowlist while the switch is on. The start pin only on `pin_model` while the switch is on | `TestClaudeUnderAnOnlyOnItsOwnBedrockClient`, `TestClaudeUnderAnOnlyOnARoutedProvider` |
| opencode ([MM-D7](#MM-D7)) | Under an `only`, `provider.<id>.whitelist` while the switch is on, on its own Bedrock client's `amazon-bedrock` row too, and `model`, `small_model` and `enabled_providers` on the list's default entry ([§7.2](#72-composition-rules)) when the only dropped the profile's model | `TestOpencodeWhitelistsANarrowedList`, `TestOpencodeWhitelistsANarrowedListOnItsOwnBedrockClient`, `TestOpencodeStartsOnANarrowedListsDefault`, `TestOpencodeAndPiPreferANarrowedListsDefaultAlias` |
| pi ([MM-D6](#MM-D6)) | Under an `only`, an extension registers exactly the list for each provider pi can reach, from a derive-rendered file keyed by pi's id ([MM-D14](#MM-D14)), and pi's selection writes no `enabledModels` for it and a `defaultModel` on the list's default entry ([§7.2](#72-composition-rules)) | `TestPiGetsANarrowedListToRegister`, `TestPiModelListsExtensionRegistersTheRenderedListAlone`, `TestPiStartsOnANarrowedListsDefault` |
| oh-omp ([MM-D8](#MM-D8)) | Under an `only`, `enabledModels` in `~/.oh-omp/agent/config.yml`, the default first, through the selection; no `modelRoles` write ([MM-D12](#MM-D12)) | `TestOmpScopesANarrowedList`, `TestOmpScopeLeadsWithADefaultNotFirstInOrder` |
| copilot | Under an `only`, `COPILOT_MODEL` is the narrowed list's default entry ([§7.2](#72-composition-rules)) | `TestCopilotStartsOnANarrowedListsDefault` |
| codex | Under an `only`, the selection is the narrowed list's default entry | `TestCodexSelectsANarrowedListsDefault` |
| Display names ([MM-D7](#MM-D7)) | opencode's, pi's and oh-omp's catalog rows are named by the entry's `name`, never by the alias | `TestNoCatalogRowIsNamedAfterItsAlias` |
| copilot's settings file | The footer default goes to `~/.copilot/settings.json`, where copilot 1.0.35+ keeps settings ([MM-D15](#MM-D15)) | `TestCopilotStatusLineDefaultLandsInSettingsJSON` |
| pi's "(legacy)" label | yolo's `openai-codex` registration names the provider "OpenAI Codex" | `TestPiOpenAIAuthExtensionNamesTheProviderItRegisters` |

**Not built, and why.** codex's catalog at prelaunch ([MM-D9](#MM-D9)): a new mechanism, a
program's output feeding a render, and its Bedrock half waits on a measurement; built 2026-09-30
for `openai-codex`, the Bedrock half ruled out by that measurement
([§14.6](#146-what-was-built-2026-09-30)). copilot's `providers.json` ([MM-D10](#MM-D10)): four
measurements first, and stopped on them ([OQ-MM4](#OQ-MM4)). pi's refusing `streamSimple`
wrapper ([MM-D6](#MM-D6)) waited on its credential path, since measured and built
([§14.6](#146-what-was-built-2026-09-30)). The bridge's allowlist
([OQ-WG3](wire-bridge-gateway.md#OQ-WG3)) was the bridge's own build, since done (2026-09-30,
[`wire-bridge-gateway.md` §6.1](wire-bridge-gateway.md#61-how-the-allowlist-is-built)). The `yolo check` line saying
opencode's menu is not narrowed with the switch off ([MM-D5](#MM-D5)). The per-agent "empty
effective list" line of [§7.2](#72-composition-rules), which needs each agent's callable-maker
filter. pi's native Bedrock selection (Bedrock step 2's, built beside this) still writes its
`enabledModels` scope under an `only`, a restatement of the list its registration already makes
exact. opencode's native `amazon-bedrock` row, the other place the two met, now takes the
whitelist a generic row takes (`TestOpencodeWhitelistsANarrowedListOnItsOwnBedrockClient`).

**What only a real run can confirm.** That Claude Code 2.1.285 resolves Default to the pinned
tier under the allowlist, as [§14.2](#142-what-each-row-rests-on) read it; that a pi session shows
yolo's refusal where a real Bedrock or gateway model would have answered, which the
[§14.4](#144-build-order) measurement took as far as the request (the bundle's catalog import,
this list's question until 2026-09-30, was measured there too); that pi
0.99 shows "OpenAI Codex" for the registration's `name`; that oh-omp 0.15.3 reads the scope from
`config.yml` beside the settings it writes there itself; that copilot keeps the footer
default in `settings.json` rather than moving it again; and that opencode's `whitelist` narrows
its built-in `amazon-bedrock` catalog as the [§14.1](#141-the-table) research read it doing for a gateway it ships.

### 14.6 What was built (2026-09-30)

Built on the measurements [§14.4](#144-build-order) records, each after its own. Each row's test
drives the production call site, the boot render or the shipped file, and fails with it removed.

| Piece | What it does now | Pinned by |
| :--- | :--- | :--- |
| pi's refusing wrapper ([MM-D6](#MM-D6), [MM-D21](#MM-D21)) | `yolo.derive("pi", "model-lists")` gives each narrowed list the switch of the profile that governs it, as `enforce`, and the api of the models.json row it writes, as `api`. With `enforce` on, `yolo-model-lists.js` registers the list with that api, or the one pi's catalog gives it, and a `streamSimple` that throws yolo's refusal for an id off the list and hands a listed one to pi's own stream: the built-in provider of that id when it serves the api, else pi's api registry. A list spanning two pi apis, or one whose stream the extension cannot find, registers the exact menu alone and warns once. With `enforce` off, `models` alone, as before | `TestPiModelListsExtensionRefusesAModelOutsideTheList`, `TestPiModelListsExtensionRegistersTheRenderedListAlone`, `TestPiModelListsExtensionDelegatesAProviderPiDoesNotShipToItsAPIRegistry`, `TestPiModelListsExtensionSaysWhenAListSpanningTwoAPIsCannotBeRefused`, `TestPiModelListsExtensionReadsTheBedrockAPIFromPisCatalog`, `TestPiNarrowedListCarriesItsProfilesModelSwitch`, `TestPiGetsANarrowedListToRegister` |
| pi's refusal on `openai-codex` ([MM-D6](#MM-D6), [MM-D23](#MM-D23)) | `yolo.derive("pi", "codex-models")` writes, beside the list, the switch of the profile that governs `openai-codex` as `enforce` (`piEnforceFor`, so on with no profile selected). With a list and `enforce` on, `yolo-openai-auth.js`'s registration, the one carrying the subscription login, adds a `streamSimple` that throws yolo's refusal for an id off the list and hands a listed one, options untouched, to pi's built-in `openai-codex` provider, else pi's api registry for `openai-codex-responses`. With no stream found, it registers the exact menu alone and warns once. With `enforce` off, or no list, the registration is what it was. The refusal's words are one text in both extensions | `TestPiOpenAIAuthExtensionRefusesAModelOutsideTheCodexList`, `TestPiOpenAIAuthExtensionRefusesWithNoProfileSelected`, `TestPiCodexListTakesTheSwitchOfTheActiveSetEntryOnOpenAICodex`, `TestPiOpenAIAuthExtensionRefusesNothingWithTheSwitchOff`, `TestPiOpenAIAuthExtensionRefusesNothingWithoutAList`, `TestPiOpenAIAuthExtensionDelegatesToPisAPIRegistryWithoutABuiltIn`, `TestPiOpenAIAuthExtensionSaysWhenItCannotRefuse`, `TestPisTwoRefusalsAreWordedAlike`, and in a real `-p codex` launch `TestCodexProfileRendersOneModelListForEveryAgent` |
| codex's menu on `openai-codex` ([MM-D9](#MM-D9), [MM-D22](#MM-D22)) | `yolo.derive("codex", "model-list")` writes the subscription's list, ids and names in order, to `~/.codex/yolo-model-list.json` on `-p codex` alone. The codex program's new `model_menu` declaration tells its launcher to run `codex debug models --bundled` through `yolo internal model-menu`, which keeps that catalog's entries for the listed ids in list order, renumbers `priority`, takes each name as `display_name`, clears `upgrade` and `availability_nux`, sets `visibility`, writes `~/.codex/yolo-model-menu.json`, and prints `-c model_catalog_json=<file>`, which the launcher puts ahead of the user's argv. An id the catalog lacks is left out with a warning; with none left, or no catalog, no flag. The menu is rebuilt only when codex, the list or the declaration changed. Both launcher templates carry the step | `TestCodexLauncherHandsCodexItsModelMenu`, `TestCodexLauncherAddsNoMenuWhereThereIsNone`, `TestCodexModelListIsTheSubscriptionsListOnly`, `TestCodexModelMenuReadsTheListItsSurfaceWrites`, `TestTheNpmLauncherHandsItsProgramAModelMenuToo`, `TestYoloInternalModelMenuRunsTheMenuAgainstHome`, `internal/modelmenu/modelmenu_test.go`, `internal/packdecl/modelmenu_test.go` |

**What only a real run can confirm.** That codex 0.159.2 reads the menu through
`-c model_catalog_json=<file>` and its `/model` picker shows exactly those entries, which this
build took as far as the argv codex is exec'd with; and that a pi session shows the wrapper's
refusal as the turn's error, on `openai-codex` as on the other providers, which each
measurement took as far as the refused stream.

### 14.7 codex's menu at `yolo host` (designed 2026-09-30)

[MM-D22](#MM-D22) built codex's menu in a jail and left the host owed under
[NC-D1](../plans/notch-convergence.md#7-decision-ledger), which rules one code path per concern
with the notch as an input. This section designs the host half, and it is built as designed,
with the choices the build made ledgered as [MM-D28](#MM-D28)
([§14.8](#148-what-was-built-at-the-host-2026-09-30)).

**What `yolo host -- codex` did before the build** (read in yolo's code at `6068e889`, not run):

- It resolves the binary (the host floor's copy, else the launch PATH), adds the pack's launch
  flags (`packload.InjectLaunchFlags`), and runs the declarative OpenAI prelaunch. codex's pack
  sets the variables that ask the prelaunch for codex's auth file (`YOLO_AUTH_PRELAUNCH_CODEX_FLAG`
  and `YOLO_AUTH_PRELAUNCH_CODEX_PATH`) in an `env` contribution no profile gates, so with yolo's
  OpenAI login present codex runs in a yolo-owned `CODEX_HOME` whose `config.toml` is rebuilt
  from the user's `~/.codex/config.toml` at every launch (`prepareCodexHome`,
  `writeManagedCodexConfig`), and `yolo host` stays resident for the refresh adapter
  (`openaiauthhost.Launch.Run`). Without the login it execs codex.
- `codex/model-list` is `notAtHost`, no launcher runs, and codex keeps its own menu.
- ⚠ **A host `-p` does not choose codex's provider.** codex reads its provider and model from
  its config file, and at the host only `yolo host apply` writes that file, for the profile the
  config's `profile` key names, never a launch's `-p`
  ([OQ-HC3](host-computed-layer.md#OQ-HC3); `composeHostInputs`). The launch's `-p` reaches
  codex's environment (the provider's variables, the three wire tables of
  [FT-D2](agent-footer.md#FT-D2)), and nothing in its argv or in the managed copy of its config.
  The prelaunch does not depend on it either, since no profile gates those two variables. So
  [MM-D22](#MM-D22)'s premise, that a list composed for the launch's `-p` follows the provider
  codex runs on, holds only when the `-p` and the configured profile name the same provider.
  Which one should win is [OQ-MM5](#OQ-MM5).
- **codex reads the menu again at every thread start** (SOURCED, codex 0.159.2: the app server's
  `thread_processor.rs` loads the config through `ConfigManager::load_with_overrides`, which
  re-applies the launch's `-c` overrides, `current_cli_overrides`, and fails the thread on a
  config error). So a menu file must outlive codex's startup: one removed while codex runs fails
  its next `/new`. In a jail every launch reads the list the boot rendered, so all of them name
  one menu at one fixed path, replaced only when codex itself changes.

**The design**, each piece an implementation decision that holds whichever way
[OQ-MM5](#OQ-MM5) is ruled:

1. **The list is composed by the launch** ([MM-D24](#MM-D24)): `yolo host --` runs the pack's
   own list derive over the launch's wire tables, the derive a jail's boot runs, and renders no
   file.
2. **It is built only when the launch and the configured profile agree** ([MM-D25](#MM-D25)):
   the provider the launch selected for the program is the one its configured profile selects.
   Otherwise the launch adds no menu and says why. That is the intersection of
   [OQ-MM5](#OQ-MM5)'s options, so no launch hands codex another provider's models.
3. **The step is the jail's** ([MM-D26](#MM-D26)): `internal/modelmenu`'s build, with the list
   as an argument, run by `yolo host --` against the program it is about to run. The flag goes
   right after `argv[0]`, as in a jail, and is disclosed with the pack's flags.
4. **The menu lives in yolo's state and goes only when nothing can read it**
   ([MM-D27](#MM-D27)): one file per cache key (the hash of the declaration, the list and
   codex's binary by which [MM-D22](#MM-D22) reuses a menu), under a lock each running program
   holds, so concurrent launches with different lists never overwrite or delete each other's
   menu.

A codex started outside yolo, from an IDE or a shell without yolo's wrappers, gets no menu at the
host: [OQ-HS3](host-notch-services.md#OQ-HS3) ruled that an agent launched outside yolo lacks a
launch-owned feature, and this is one.

### 14.8 What was built at the host (2026-09-30)

[§14.7](#147-codexs-menu-at-yolo-host-designed-2026-09-30) as designed, with the choices its build
had to make ledgered as [MM-D28](#MM-D28). Every row has a test that drives the production call
site, `hostExec`, through `hostMain` with the exec replaced and a stub codex on `PATH` that prints
a catalog, and fails with the call removed (the `TestHostCodex…` and `TestHostManagedCodex…`
tests); the others pin the pieces it calls. No codex ran.

| Piece | What it does now | Pinned by |
| :--- | :--- | :--- |
| The list ([MM-D24](#MM-D24)) | `entrypoint.HostModelList` runs the codex pack's derive for the surface whose path `model_menu.list` names, `codex/model-list`, over the launch's three wire tables (`hostComposition.wireTables`), and reads the answer with the jail's list reader (`modelmenu.ParseList`). Nothing is written, and the surface stays `notAtHost`, its reason reworded | `TestHostModelListIsTheLaunchsListAndWritesNothing`, `TestHostModelListNamesADeclarationNoSurfaceWrites`, `TestHostCodexMenuFollowsTheLaunchsListAndLeadsThePacksFlags` |
| The agreement ([MM-D25](#MM-D25)) | With no `-p`, or a `-p` whose provider is the one the config's `profile` selects for codex (`hostComposition.configuredProfile`), the menu is built. Otherwise no flag and no catalog run, and one line naming both selections, saying that a host `-p` does not move codex, and naming [OQ-MM5](#OQ-MM5) | `TestHostCodexWithAPOverAnotherProviderGetsNoMenu` |
| The step ([MM-D26](#MM-D26)) | `modelmenu.Request`, the build the jail's `yolo internal model-menu` now runs through too, against the resolved target, its catalog run given the environment the launch composed for codex. The flag goes right after `argv[0]`, ahead of the pack's launch flags, and is disclosed in their words, the last line saying what the file is (`LaunchInjection.Why`). The managed launch's `--no-daemon` still goes in last. `YOLO_NO_LAUNCH_FLAGS=1` skips the step | `TestHostCodexOnTheConfiguredSubscriptionGetsYolosMenu`, `TestHostCodexGetsNoMenuWhereThereIsNone`, `TestHostManagedCodexHoldsTheMenusLockWhileItRuns`, `internal/modelmenu/modelmenu_test.go` |
| The store ([MM-D27](#MM-D27)) | `modelmenu.Request.WriteIn` into `~/.local/share/yolo-jail/model-menus/<pack>/<bin>/` (`paths.HostModelMenusDir`), one `<key>.json` per cache key, under a shared lock on the directory's `.live.lock`: held by the resident `yolo host` on a managed launch, and on the exec path by codex, through a descriptor whose close-on-exec is cleared just before the exec (`Held.KeepAcrossExec`). A launch that writes a new menu removes the others only when it can take that lock exclusively. No jail mounts the directory, and `yolo stores` lists it as yolo's, self-bounded | the lock at the exec and on the resident path in `TestHostCodexOnTheConfiguredSubscriptionGetsYolosMenu` and `TestHostManagedCodexHoldsTheMenusLockWhileItRuns`; `TestWriteInKeepsEveryMenuARunningProgramHolds`, `TestWriteInCollectsMenusOnlyWhenNoProgramHoldsOne`, `TestWriteInReusesAMenuAndRepeatsTheMissingIDWarning`, `TestHeldKeepsItsLockAcrossTheExec`, `TestAssembleNeverMountsTheHostFloor`, `TestHostModelMenusRowIsYolosOwn` |

**Not built, and why.** A `-p` over another provider than the configured one still gets no menu,
since what it should do is [OQ-MM5](#OQ-MM5)'s. copilot's `providers.json` is still stopped on
[OQ-MM4](#OQ-MM4).

**What only a real run can confirm.** That codex 0.159.2 reads a menu at a path under yolo's state
as it reads the jail's, at startup and again at each thread start; and that it keeps the inherited
lock descriptor open for its life, which this build took as far as the descriptor codex is exec'd
with. A codex that closed it would let a later launch that writes another menu remove this one
while it runs, and its next `/new` would fail.

---

## 15. Open Questions

1. ✅ <a id="OQ-ML1"></a>**[OQ-ML1](#OQ-ML1): What shape is the built-in picks pack, and how
   does it join a launch without breaking "nothing is active by default"?**
   Options: **(a)** one yolo-shipped pack (`packs/default-models`, say) that adds `models`
   entries ([OQ-BR12](#OQ-BR12)'s kind) to other packs' providers, joined by those provider
   packs' `needs`, the wire-bridge pattern; **(b)** ids inside each provider's own pack, which is
   today's mechanism and buildable now; **(c)** always staged. Sub-choice: do picks **yield**,
   applying only when no other pack and no user wrote a list for that provider
   ([§5.2](#52-how-it-joins-a-launch))? Stakes: where every shipped opinion lives, whether a
   company's list replaces yolo's or trails it, and whether "nothing is active by default" gains
   an exception.

      _Leaning:_ (a), with picks that yield. Every shipped opinion lives in one dated, replaceable
   file. [OQ-BR12](#OQ-BR12) becomes its prerequisite, a company pack replaces it just by
   shipping a list, and `-p bedrock` gets defaults with no new always-on rule. The cost: (b)
   could ship this week and (a) waits on a new kind.

   **Answer:**
   > **Ruled 2026-09-29: neither a separate picks pack nor always-on.** Each provider declares
   > its own default in its pack, the way everything else is declared, and the default layers
   > like all config: a company or user pack, or user config, overrides it. One simple default
   > per provider, which the user changes; yolo does not chase the newest model. Multiple active
   > providers per agent is split into its own design doc,
   > [`active-provider-sets.md`](active-provider-sets.md).

2. ✅ <a id="OQ-ML2"></a>**[OQ-ML2](#OQ-ML2): Which provider × agent cases count as "we can't
   just fall to the default", and so get a yolo pick?** The candidates are
   [§4](#4-where-yolo-picks-the-cannot-default-cases)'s table: Bedrock runtime for codex, pi and
   opencode; claude's everything profile; copilot through the bridge; the already-shipped
   `openai-codex` lists; and the borderline ones, claude's native `bedrock` profile and the
   first-party `anthropic` provider, where the agent defaults to the right family but maybe not
   the newest model. Stakes: how many ids yolo keeps current, and whether provider-switching's
   same-tier-word goal survives for claude.

      _Leaning:_ Exactly the cases where the agent would otherwise start on a wrong-family id or on
   none. That excludes claude's native profile and the `anthropic` provider, which drops
   provider-switching's same-tier-word done-condition unless ruled otherwise. Each id is dated in
   the pack README and covered by [OQ-BR14](#OQ-BR14)'s warning.

   **Answer:**
   > **Ruled 2026-09-29, narrower than the leaning** (the maintainer: *"YOLO by default should
   > never pick a default model … whenever you start up a session, we always want to make sure
   > that we have picked one of those models. You just want a valid configuration … We will not
   > otherwise steer if it's some sort of valid yet non-default configuration. Unless, of course,
   > the configuration specifically says like an opt-in … If you have a provider pack, it's going
   > to have to pick a default … follow upstream default if defaults exist … pick something
   > simple for default, let users change it."*) yolo picks a model only to make a session VALID:
   > when the model the agent would start on is not one of the active provider's models (a
   > wrong-family id, or none). A valid non-default choice, the user's or the agent's own, is
   > never steered, unless the config opts in. The pick is the provider's default: the agent's
   > upstream default when it is one of that provider's models, otherwise the provider's declared
   > default, otherwise the first model it lists.

   **Several active providers.** When one agent runs on several providers at once, the rule reads
   over their union: the start model is valid when it belongs to any of them, and the pick is
   the first-listed provider's default. Designed in
   [`active-provider-sets.md` §4.4](active-provider-sets.md#44-models-the-union-and-the-start-model).

3. ✅ <a id="OQ-BR3"></a>**[OQ-BR3](#OQ-BR3): Does yolo ship model aliases at all?** (moved
   from [`bedrock-plumbing.md`](bedrock-plumbing.md) on 2026-09-25, id kept.) **Ruled
   2026-09-25:** yes, where the agent cannot default, in a built-in pack that comes with yolo
   ([Decision Ledger](#16-decision-ledger)). The old proposal, `default`/`balanced`/`fast` →
   `global.openai.gpt-5.6-{sol,terra,luna}`, was already stale before the ruling: pi-ai 0.87.1's catalog lists
   `openai.gpt-6-astra` (bare, `us.`, `global.`) beside GPT-5.6, Bedrock serves GPT-6 Astra on
   runtime as `us.openai.gpt-6-astra` / `global.openai.gpt-6-astra` (SOURCED
   2026-09-25), and yolo's codex-subscription defaults moved to GPT-6 in September (`2f11de95`,
   `7ad8358c`). Which ids ship is a build-time check, dated in the README.

4. ✅ <a id="OQ-PSW3"></a>**[OQ-PSW3](#OQ-PSW3): Does yolo ship the model ids, or only the empty
   provider shape?** (moved from the retired `provider-switching.md` on 2026-09-25,
   where it was `PS3` before the rename noted at the top of this doc.) **Answered 2026-09-25 by [OQ-BR3](#OQ-BR3)'s ruling:** yolo ships the ids, in a
   built-in pack. It was always the same decision. Which of its two cases (the first-party
   provider, claude's `bedrock` map) get ids is [OQ-ML2](#OQ-ML2)'s. The geo-prefix verification
   stays a build prerequisite ([§5.3](#53-the-prerequisite-verify-before-an-id-ships)).

5. ✅ <a id="OQ-BR12"></a>**[OQ-BR12](#OQ-BR12): Is there a `models` contribution kind, so a
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

   _Leaning:_ A. `add` unions in pack order, `only` intersects, and the user's config is the last
   writer. yolo's own packs ship as few ids as [OQ-BR3](#OQ-BR3)'s ruling allows.

   **Answer:**
   > **Ruled 2026-09-29, as leaned: A, for every provider.** The maintainer: *"you ship a pack
   > that narrows that overall bedrock provider, in the same way it could narrow one of the
   > individual packs' providers. But also … an engineer is going to do whatever they want to do.
   > So of course their own config still has the last word because they could make that happen
   > regardless."* A `models` contribution names any provider (the `bedrock` provider or a single
   > pack's own) and either adds entries or keeps only the ones it names; the user's own
   > `providers.<name>.models` writes last, not as a policy yolo chooses but because an engineer
   > can always override their own config.

6. ✅ <a id="OQ-BR13"></a>**[OQ-BR13](#OQ-BR13): How does each derive render the effective list
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

   _Leaning:_ A. Move the GPT-6 lists into data under a byte-identical test, which needs the
   `description` field.

   **Answer:**
   > **Directed 2026-09-29: set the model selection however each agent allows.** The maintainer:
   > *"For BR 13, yes, I want us to set the model selection however we can … you can like send it
   > over the wire or whatever with wire bridge … I know there are options here. I need you to
   > research whatever it is and then come back to me if there's a real decision here."* Option A,
   > widened from "each agent's picker surface" to whatever each agent offers, a refusal included.

   The research is [§14](#14-how-each-agents-menu-is-set-researched-2026-09-29): its table gives
   each agent's mechanism, and the implementation decisions are [MM-D1](#MM-D1) to
   [MM-D10](#MM-D10). What happened to the four sub-choices: claude's built-ins are replaced on
   every routed provider and under an `only`, not only on the everything profile ([MM-D1](#MM-D1));
   refusal follows where the list reaches and the profile's switch, so `enforceAvailableModels`
   renders on `openai-codex` and under an `only`, while a routed list with no `only` on a gateway
   that serves more is [OQ-MM3](#OQ-MM3) ([MM-D1](#MM-D1), [MM-D5](#MM-D5)); the Fable tier is
   pinned like every other tier wherever the built-ins are replaced ([MM-D2](#MM-D2)); codex's
   catalog format is now read, and codex gets a catalog last ([MM-D9](#MM-D9)). Two real decisions
   are left: [OQ-MM1](#OQ-MM1), what a list that only adds does to an agent's own catalog, and
   [OQ-MM3](#OQ-MM3), whether a list refuses on a gateway that serves more than it, and what keeps
   claude's start valid where nothing refuses. [OQ-MM2](#OQ-MM2), copilot's key, was settled on a
   corrected premise.

7. ✅ <a id="OQ-BR14"></a>**[OQ-BR14](#OQ-BR14): How do the lists stay current?** (moved from
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

   _Leaning:_ A. Warn, never refuse; say so when no catalog could be read; no launch network
   call; no "newer model exists" report.

   **Answer:**
   > Decided as an implementation choice ([MM-D16](#MM-D16)), reversible: A, the agents' own
   > catalogs plus a `yolo check` warning for a listed id no installed catalog knows. The policy
   > half was ruled on 2026-09-29: [OQ-ML1](#OQ-ML1) and [OQ-ML2](#OQ-ML2) make each provider's
   > default one simple pick the user changes (*"pick something simple for default, let users
   > change it"*), so yolo does not chase the newest model, which rules out B and C. What was left,
   > A against D, is whether `yolo check` says so when a listed id has gone.

8. ✅ <a id="OQ-PSW1"></a>**[OQ-PSW1](#OQ-PSW1): Does claude's derive move to capability
   aliases?** (moved from the retired `provider-switching.md`, where it was `PS1` before the rename noted at the top of this doc.) It reads `sonnet` and `haiku`
   literally today. The proposal reads `balanced` and `fast`, keeping the old names as synonyms
   ([§6](#6-tier-aliases-default-fast-balanced)). Stakes: whether one alias vocabulary spans every
   provider, or claude keeps a dialect and a `-p` swap means something slightly different there.

   _Leaning:_ Move, with synonyms. A vendor tier name cannot survive a switch to another vendor,
   and synonyms make the move free for existing config.

   **Answer:**
   > Decided as an implementation choice ([MM-D17](#MM-D17)), reversible: claude's derive reads
   > `balanced` for the Sonnet tier and `fast` for the Haiku tier, and `frontier` for the Opus
   > tier where a tier pin reads an alias, with `sonnet`, `haiku` and `opus` kept as synonyms that
   > win where both are declared. The vocabulary was ruled on 2026-09-28 by
   > [OQ-XM2](../research/extension-model-defaults.md#OQ-XM2) (keep `default`, `fast` and
   > `balanced`, add `frontier`; each adapter maps yolo's names onto its agent's, and core maps
   > none), so claude's derive is one more such mapping, and a provider that declares only the
   > four conventional names no longer leaves claude's Sonnet and Haiku tiers on its default.

9. ✅ <a id="OQ-BR15"></a>**[OQ-BR15](#OQ-BR15): Does the bridge serve `GET /v1/models`, and
   when?** (moved from [`bedrock-plumbing.md`](bedrock-plumbing.md), id kept.) **Later; measure first.**
   [§10](#10-later-the-bridges-get-v1models). Stakes: whether claude's picker follows the list with
   no settings write, and whether the bridge grows a second endpoint whose value is unproven.

   Options: **A**, later, after measuring discovery under
   `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` (leaning); **B**, now, beside
   [OQ-BR13](#OQ-BR13)'s settings rendering, on an unmeasured dependency; **C**, never, which is
   fine if settings rendering suffices and forecloses nothing A does not defer.

   _Leaning:_ A. Measure first. If discovery survives, serve the effective list from memory and
   never call upstream for it.

   **Answer:**
   > **Settled 2026-09-29 by the measurement it waited on: C, never** ([MM-D4](#MM-D4)). Discovery
   > does survive `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`, so the leaning's condition held,
   > but the same read showed discovery cannot carry the list: it keeps only ids containing
   > "claude" or "anthropic", never runs on claude's native profile, and is hidden once
   > `replaceBuiltInOptions` replaces the built-ins. opencode, codex and copilot never read such a
   > route either ([§14.3](#143-the-bridges-part-and-get-v1models)). One sensible answer, so it
   > was decided rather than asked, under [OQ-BR13](#OQ-BR13)'s direction.

10. ✅ <a id="OQ-ML3"></a>**[OQ-ML3](#OQ-ML3): Does `yolo host apply` render the `openai-codex`
    list for pi?** Today it does not ([ML-D8](#ML-D8)): the host notch runs no derive for content,
    so pi on the host keeps pi-ai's own `openai-codex` catalog, with no 1M variants and with the
    GPT-5.x ids the jail list dropped. Stakes: whether host pi and a `-p codex` jail offer the
    same models, against the host render's rule that its computed layer is empty
    ([host-render-target §3.3](host-render-target.md#33-what-each-target-supplies)).

    Options: **A**, leave it, since the host has one list, pi's, and v0.10.0 behaved the same;
    **B**, compose the provider table for a host apply and run the derive for a surface whose
    pack declares it target-independent, a new manifest field; the surface would also need a mode
    `host_management: own` accepts, since `own` refuses `computed`; **C**, deliver the list to the
    host some other way, such as a host-notch file the extension reads, a second channel ML-D3
    rejected for the jail.

    **Answer:** A, for now. The maintainer, 2026-09-27: *"basically option 1, but then make sure
    there's a design doc about the host option left."* Host pi keeps pi-ai's own `openai-codex`
    catalog ([ML-D8](#ML-D8)). Whether the host notch should derive what the jail derives, for this
    surface and the others with a computed layer, is
    [`host-computed-layer.md`](host-computed-layer.md)'s question,
    [OQ-HC1](host-computed-layer.md#OQ-HC1), rather than answered here. **Superseded
    2026-09-28** by that ruling: the host runs the jail's derives, so host pi gets the declared
    list ([ML-D8](#ML-D8) records what this answer decided).

11. 💬 <a id="OQ-MM1"></a>**[OQ-MM1](#OQ-MM1): When a list only adds models, does the agent's
    menu still show the agent's own catalog for that provider?** Stakes: what every agent with a
    catalog shows by default, and whether yolo's own default pick can shrink a menu.

    **The setup.** Priya's company pack adds three Bedrock models, GPT-6 Astra, Kimi and Claude
    Opus 5.5, to the `bedrock` provider with an `add` and no `only`. She runs
    `yolo -p bedrock -- opencode`. With [§14.1](#141-the-table) built, opencode and pi can show
    exactly those three: pi without refusing anything, opencode only by refusing every other model
    too. The other native Bedrock clients take one family each ([§1](#1-the-words-plainly) for
    claude; [`bedrock-plumbing.md`](bedrock-plumbing.md#where-the-split-ended-up) for codex):
    claude's `-p bedrock`, its native profile, shows its Default row and Opus 5.5 at most, and
    codex's shows GPT-6 Astra. GPT-6 Astra and Kimi reach claude only on the everything profile,
    whose list is exact anyway. Nobody has decided what an `add` means for the catalog the agent
    already has.
    The same shape exists today with a user's own list: Dana's five-model
    `providers.openrouter.models` shows in opencode among its whole OpenRouter catalog, about 374
    models, because opencode starts any provider named `openrouter` from its catalog; pi's `zai`
    menu is its own 7 models plus yolo's. The research passes recommended opposite answers, so it
    is asked.

    Options, with what Priya and Dana would see:

    - **A. An `add` sits beside the agent's catalog; only an `only` makes a menu exact.** Priya sees
      opencode's 166 Bedrock models with her three among them, pi's 176, and claude's built-in rows
      with Opus 5.5 appended. One `only` line in her company's pack makes opencode's and pi's
      menus exactly her list, and claude's exactly Opus 5.5 below its Default row. Dana sees her
      five among opencode's 374 until she states them as an `only`. Where the agent has no
      catalog for the provider, such as any provider claude is routed to or a key the agent does
      not ship, every menu is exact anyway.
    - **B. Any list makes the menu exact.** Priya sees exactly her three in opencode and pi, and
      Opus 5.5 alone below claude's Default row, and opencode refuses every other model. Dana sees
      exactly her five. A provider pack's own one-model default, which [OQ-ML1](#OQ-ML1)'s ruling has every provider declare, then shrinks
      that agent's menu to one model, unless a declared default is told apart from a list.
    - **C. Exact for the user's own list, beside for a pack's.** Dana sees exactly her five; Priya
      sees her three beside the catalog. The same list means two things depending on who wrote it.

    <!-- vantage: oq id=OQ-MM1 leaning="A: an add sits beside the agent's own catalog for that provider, and only an only makes the menu exact (claude's native built-ins stay, opencode and pi keep their catalogs); a provider the agent has no catalog for (any provider claude is routed to, or a key the agent does not ship) is exact anyway. The verbs already say 'these too' and 'just these', and B would let yolo's own one-model default shrink a menu." -->

    _Leaning:_ A. The verbs already say it: `add` means "these too" and `only` means "just these",
    so a company gets either with one line. B lets yolo's own default pick cut a menu to one
    model, and C gives one list two meanings.

    **Answer:**
    > _(empty — fill in when decided)_

12. ✅ <a id="OQ-MM2"></a>**[OQ-MM2](#OQ-MM2): If copilot's model file cannot read its key from
    the environment, may yolo write the key into a file, so copilot shows the whole list?**
    Stakes: copilot's menu, one model or the list, against where a credential may be written.

    **The setup.** Sam runs `yolo -p bedrock -- copilot`. copilot reaches Bedrock only through
    the wire bridge, and its menu has one model, the list's default, because copilot's
    environment-variable setup carries exactly one ([§14.2](#142-what-each-row-rests-on)).
    copilot 1.0.89 can show a whole list from a `providers.json` file, but the file holds its key
    as literal text. That key is whatever `COPILOT_PROVIDER_API_KEY` carries today: the launch's
    caller token on a bridged provider ([WB-D18](../reference/wire-bridge.md#wb-d18)), and the
    provider's own key on a direct one, such as z.ai. **It is already in a file.** On the
    container backends an env derive's output is written to
    `<workspace>/.yolo/home/agent-env/copilot.sh`, 0600 in a 0700 directory, rewritten on every
    entry and kept in the workspace's state between launches
    ([the credential gate](../reference/providers.md#the-credential-gate)). What the rules forbid
    is narrower: the caller token is *"never in a rendered config file, where each derive writes
    the variable's name instead"* ([caller authentication](../reference/wire-bridge.md#caller-authentication)),
    and a derive, which renders surfaces, never receives a hydrated key
    ([providers.md](../reference/providers.md#derives-the-delivery-mechanism)). Two unmeasured
    fields might let the file take its key from the environment: a key command, or a fallback to
    `COPILOT_PROVIDER_API_KEY`. The build measures them first, and if either works this question
    closes by itself.

    Options, with what Sam would see:

    - **A. Keep one model.** Sam's menu shows only the default. Another listed model is reachable
      only by typing its id, and on a bridged provider the bridge still refuses anything off the
      list.
    - **B. A second file in the per-agent env directory.** The launch's per-agent env writer puts
      the file beside `copilot.sh`, with the same mode and lifetime, and points copilot at it with
      `COPILOT_PROVIDERS_CONFIG`. Sam sees the whole list, and no reader gains anything
      `copilot.sh` does not already give it.
    - **C. The file in copilot's home, as a rendered surface.** Sam sees the whole list, but the
      key sits in `~/.copilot/providers.json`, a rendered config file, which the caller-token rule
      forbids, and the derive that renders it never holds the key.

    _Leaning:_ B. It adds a second file of a kind that already exists, in the same directory, with
    the same readers.

    **Answer:**
    > **Settled 2026-09-29 on a corrected premise, rather than asked: B** ([MM-D10](#MM-D10)). The
    > question was first drafted on the premise that no secret is ever written to a file, and
    > offered B as a file under `/run` that lived as long as the launch, with a named exception to
    > that rule. The credential gate already writes this very value to `agent-env/copilot.sh`, so
    > B adds no new reader and needs no exception. A gives up the list that
    > [OQ-BR13](#OQ-BR13)'s direction asks for, and C breaks the caller-token rule. That left one
    > sensible answer, so it was decided.

13. 💬 <a id="OQ-MM3"></a>**[OQ-MM3](#OQ-MM3): On a gateway that serves more than its list, does
    a list with no `only` refuse other models, and what keeps claude's start valid where nothing
    refuses?** Stakes: whether a gateway model that works today stops working once yolo renders a
    list, and whether [OQ-ML2](#OQ-ML2)'s "every start valid" survives the enforcement switch
    being off.

    **The setup.** Where the agent has no catalog of its own for a provider, the list is the
    provider's whole universe ([§14](#14-how-each-agents-menu-is-set-researched-2026-09-29)):
    every provider claude is routed to, and every key pi, oh-omp or opencode does not ship.
    [§8](#8-rendering-the-effective-list-into-each-picker) sets a refusal there by default, under
    the profile's switch ([MM-D5](#MM-D5)). On `openai-codex` that costs nothing, since the
    subscription serves only the list. On a gateway it does. Lee's z.ai list has three GLM models,
    and `/model glm-4.7` works on z.ai today. With claude's allowlist on a z.ai launch, claude
    refuses it until Lee lists it or turns the switch off, and pi's wrapper does the same on a Kilo
    list. [OQ-WG3](wire-bridge-gateway.md#OQ-WG3) ruled the list enforced by default (*"If we put
    it in the model picker, it's allowed … let's have it even default on enforced"*). That ruling
    was about the bridge, and it added that a list *"does not mean that we need the bridge to deny
    it"*. claude cannot separate the two either: its allowlist is also its only start check that
    does not steer ([§14.2](#142-what-each-row-rests-on)). With the switch off claude gets
    `modelPicker` alone, and nothing yolo writes checks the start. Omar's saved first-party
    `claude-opus-5-5`, carried into the everything profile, then starts unchecked and fails at its
    first request (INFERRED). That breaks [OQ-ML2](#OQ-ML2)'s *"whenever you start up a session,
    we always want to make sure that we have picked one of those models"*.

    Options, with what Lee and Omar would see:

    - **A. Refuse by default wherever the list is the whole universe**, as [MM-D1](#MM-D1) was
      first drafted. Lee's `glm-4.7` is refused until he lists it or turns the switch off. With
      the switch off, Omar starts unchecked, so a start is valid only while the switch is on.
    - **B. Refuse only on `openai-codex` and under an `only`, and check claude's start with a pin
      everywhere else.** yolo writes the list's default into claude's settings `model` key once,
      through the selection namespace, so a later `/model` choice is kept until the provider
      changes. Lee keeps `glm-4.7` with the switch on, and Omar starts on the list's default. The
      cost: an exception to providers.md's rule that
      [yolo never writes claude a model id through the selection namespace](../reference/providers.md#deselection-clear-what-yolo-wrote-keep-what-the-user-wrote),
      and a `claude` started outside yolo reads that id too.
    - **C. A's default, plus B's pin whenever the switch is off.** Lee is refused by default, and
      turning the switch off gives him `glm-4.7` with a start that stays valid. Omar starts valid
      either way.

    <!-- vantage: oq id=OQ-MM3 leaning="C: refuse by default wherever the list is the whole universe, as OQ-WG3's default-on switch says, and when the switch is off check claude's start with a pin yolo writes once into claude's settings model key through the selection namespace, so a later /model choice is kept. It keeps both rulings as spoken, and the one exception to providers.md's rule is paid only with the switch off." -->

    _Leaning:_ C. It keeps both rulings as spoken: the list enforced by default
    ([OQ-WG3](wire-bridge-gateway.md#OQ-WG3)) and every start valid ([OQ-ML2](#OQ-ML2)). A gateway
    user then has one switch that costs only the refusal. B is kinder to gateways, but refuses
    nothing by default where [OQ-WG3](wire-bridge-gateway.md#OQ-WG3) said to, and pays the
    exception on every gateway launch.

    **Answer:**
    > _(empty — fill in when decided)_

14. 💬 <a id="OQ-MM4"></a>**[OQ-MM4](#OQ-MM4): copilot can show a whole model list only in a mode
    that also shows GitHub's own models. Should yolo use that mode?** Filed 2026-09-30, when the
    measurement [MM-D10](#MM-D10) waited on contradicted it ([§14.4](#144-build-order)). Stakes:
    copilot's menu, one model or the list, against whether a copilot signed in to GitHub can pick
    a model GitHub serves while its profile names another provider, past a company's `only` and
    past the wire bridge.

    **The setup.** Sam's company pack narrows `bedrock` to five models with an `only`, and Sam
    runs `yolo -p bedrock -- copilot`. Today copilot's menu is one model, the list's default, set
    through copilot's environment variables, the mode in which, in copilot's own help, *"the CLI
    uses this provider instead of GitHub Copilot's model routing"*. Every request goes to Bedrock
    through the wire bridge, which refuses a sixth model. [MM-D10](#MM-D10) planned the whole list
    through copilot's `providers.json`. Reading copilot 1.0.89 found that the file's providers are
    additive: they *"coexist with Copilot API auth"*, and copilot merges their models into the list
    GitHub's API returns. Sam is signed in to GitHub with a Copilot plan, so in that mode his menu
    shows the five and every model GitHub offers him, and a GitHub model he picks is served by
    GitHub, not Bedrock. The company's `only` then shapes only part of the menu, and the bridge
    never sees that request. Riya is not signed in, and would see just the five, if the file's mode
    starts at all without a GitHub login, which is UNMEASURED. Either way the file holds its key
    as literal text, so it would be written beside `agent-env/copilot.sh`, as
    [OQ-MM2](#OQ-MM2) settled.

    Options, with what Sam would see:

    - **A. Keep one model, and drop MM-D10.** Sam's menu is the list's default alone, and every
      request stays on Bedrock behind the bridge. Another listed model is reachable only by
      typing its id, and whether copilot accepts it that way is unmeasured.
    - **B. Write the file on every provider.** Sam sees the five and GitHub's models, and a GitHub
      pick goes to GitHub. An `only` narrows the provider's part of the menu and nothing else.
    - **C. Write the file only for a list no `only` narrowed, and keep one model under an
      `only`.** A list that merely adds sits beside copilot's own catalog, which is GitHub's, the
      way [OQ-MM1](#OQ-MM1)'s leaning has an `add` sit beside every agent's catalog. Under an
      `only` Sam keeps today's single model, on Bedrock, behind the bridge. One agent then runs in
      two modes, chosen by the verb.
    - **D. Write the file and keep copilot off GitHub**, with its offline mode. Sam sees the five
      alone, but offline also turns off copilot's web tools, its GitHub MCP server and its
      updates, and copilot's help says offline wants the environment variable
      `COPILOT_PROVIDER_BASE_URL`, so whether it accepts the file at all is unmeasured.

    <!-- vantage: oq id=OQ-MM4 leaning="A: keep copilot on one COPILOT_MODEL and drop MM-D10, since a profile picks one path (OQ-WG5, as MM-D5 reads it for a list) and every other agent's list stays inside the provider the profile selects; B and C would be the only renderings in this doc that let a session leave its provider, and D costs copilot's web tools, GitHub MCP server and updates on a mode not measured to accept the file. Revisit when copilot offers a way to show a file's models alone." -->

    _Leaning:_ A. A profile picks one path, as [OQ-WG5](wire-bridge-gateway.md#OQ-WG5) ruled for
    the native and bridged paths and [MM-D5](#MM-D5) reads for a list, and every other agent's
    list stays inside the provider the profile selects. B and C would be the only
    renderings in this doc that let a session leave its provider, for a longer menu. D pays for
    the menu with copilot's web tools, GitHub MCP server and updates, on a mode not measured to
    take the file. Revisit when copilot offers a way to show a file's models alone.

    **Answer:**
    > _(empty — fill in when decided)_

15. 💬 <a id="OQ-MM5"></a>**[OQ-MM5](#OQ-MM5): At `yolo host`, should a `-p` choose codex's
    provider for that launch, so its model menu can follow the `-p`?** Filed 2026-09-30, when
    designing codex's menu at the host found that [MM-D22](#MM-D22)'s premise holds only in part
    ([§14.7](#147-codexs-menu-at-yolo-host-designed-2026-09-30)). Stakes: whether
    `yolo host -p codex -- codex` runs codex on the ChatGPT subscription with yolo's menu, as a
    jail does, and whether a host `-p` means for codex what it means at every other notch.

    **The setup.** In a jail, `yolo -p codex -- codex` starts codex on the subscription with a
    menu of three models ([MM-D22](#MM-D22)). At the host, codex reads its provider from its
    config file, and only `yolo host apply` writes that file, for the profile the config's
    `profile` key names, never for a launch's `-p` ([OQ-HC3](host-computed-layer.md#OQ-HC3)). A
    `-p` reaches codex's environment, not its provider. Kim's config names
    no profile for codex, so her `yolo host -p codex -- codex` starts codex on its own default,
    which happens to be the ChatGPT login. Lee's config names `zai` for codex, so his starts codex
    on z.ai, without the z.ai key a z.ai launch would deliver. A menu built for the `-p` is right
    for Kim by luck and hands OpenAI's models to codex on z.ai for Lee, the fault
    [MM-D22](#MM-D22) exists to prevent. A menu built for the configured profile is right for both,
    but then the `-p` changes nothing codex shows. Until this is ruled, [MM-D25](#MM-D25) builds a
    menu only where the two agree.

    Options, with what Kim and Lee would see:

    - **A. codex stays on its configured provider at the host, and the menu follows that.** A host
      `-p` stays an environment choice for codex. Kim gets the menu once her config names `codex`
      for codex, with or without `-p`. Lee's `-p codex` runs z.ai with codex's own menu, and the launch
      says the `-p` did not move codex.
    - **B. A host `-p` moves codex for that launch, and the menu follows it.** The launch writes
      its own selection into the config of the `CODEX_HOME` yolo already rebuilds for codex at
      every launch with yolo's login, leaving the user's `~/.codex/config.toml` untouched. Kim and
      Lee both get the subscription and its menu, as in a jail. A launch without yolo's login has
      no such home and stays as in A. codex becomes the one host agent whose file-held selection
      follows a `-p`, until the same is done for pi, whose selection is also file-held.
    - **C. Neither: a `-p` naming another provider than the configured one is refused at the
      host for codex**, naming `yolo host apply` with that profile configured. Nothing runs on a
      provider the user did not mean, and `-p` stops being usable for codex at the host.

    <!-- vantage: oq id=OQ-MM5 leaning="B: a host -p moves codex for that launch by writing the launch's selection into the CODEX_HOME yolo already rebuilds at every launch with its login, and the menu follows it, since NC-D1 rules that the host acts like every other notch and at every other notch -p codex -- codex is the subscription with its menu; the user's own ~/.codex stays untouched, and until this is ruled MM-D25 builds only where the -p and the configured profile agree." -->

    _Leaning:_ B. [NC-D1](../plans/notch-convergence.md#7-decision-ledger) rules that the host acts
    like every other notch (*"host is supposed to act like everywhere else"*), and at every other
    notch `-p codex -- codex` is the subscription with its menu. The launch already rebuilds a
    config file for codex that yolo owns, so carrying the selection there touches nothing of the
    user's. A keeps a host `-p` that does not do what it says, and C takes it away.

    **Answer:**
    > _(empty — fill in when decided)_

---

## 16. Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-BR3 | **yolo picks model ids where an agent cannot default, and ships them in a built-in pack that comes with yolo, not in core.** *"in cases where we can't just fall to the default by just not having an opinion and letting the agent pick … I do want to pick these … let's actually ship this in core some way … a built-in pack (it's not in core per se but it comes with [yolo]) with these that tries to pick these different models."* Moved from [`bedrock-plumbing.md`](bedrock-plumbing.md), id kept. Narrows [OQ-GP2](gateway-provider-packs.md#decision-ledger) (note made there) and, in letter, [agent-auth-modes OQ-3](agent-auth-modes.md#12-decision-ledger) (note owed there) | 2026-09-25 | [§5](#5-the-picks-pack) | — |
| OQ-PSW3 | **Answered by [OQ-BR3](#OQ-BR3)'s ruling: yolo ships the ids, in a built-in pack.** Moved from the retired `provider-switching.md`, where it was `PS3` before the rename noted at the top of this doc. The Anthropic-on-Bedrock geo-prefix verification stays a build prerequisite, not a question; which cases get ids is [OQ-ML2](#OQ-ML2) | 2026-09-25 | [§5.3](#53-the-prerequisite-verify-before-an-id-ships) | — |
| <a id="ML-D1"></a>ML-D1 | *Implementation decision.* **The `openai-codex` model list is declared once, on the provider, in [`packs/openai-auth/pack.json`](../../packs/openai-auth/pack.json), and every consumer reads it.** That pack is the provider's only owner, and pi, claude and codex each `needs` it unconditionally, so it is in every launch that has a consumer. The composed provider entry is also the only way pack data reaches a derive, whose sandbox has no `io` and no `require`. **Shape:** `models` maps each alias to an identical id, as `packs/zai` does, so a profile's `model` means the same thing read as an alias or as an id. Only wire-true base ids are declared: `[1m]` is a client spelling that Claude Code, the extension's request hook and the wire bridge each strip. **Facts**, in `model_options`: `order`, because the map is unordered all the way from the Go map through the composed table to Lua's `pairs`; `name`; `description`; `context_window`; and `long_context_window`, meaning the model has a 1M-context variant. The default is the lowest `order`. There are no provider `options`, because declaring any turns on the profile-option census and would refuse user `codex` profiles that pass today. There is no `default` alias, because it would repeat an id every lister then has to drop. **Expansion:** one helper, copied verbatim into the claude, pi and codex derives because a derive cannot load another file, with a test failing when the copies differ. This builds [OQ-ML1](#OQ-ML1) option (b); ML1, [OQ-BR12](#OQ-BR12) and [OQ-BR13](#OQ-BR13) stay open, and ruling (a) later moves only the declaration, since every consumer reads the composed `openai-codex` entry. The three ids are the GPT-6 entries of pi-ai 0.87.1's own `openai-codex` catalog, each at a 272,000-token window, read 2026-09-27 | 2026-09-27 | [§3](#3-what-exists-today) | ✅ `bbb7c223` |
| <a id="ML-D2"></a>ML-D2 | *Implementation decision.* **pi writes no `enabledModels` for `openai-codex`.** The maintainer, 2026-09-27: *"So I just want them aligned for the moment, or we can just stop filling in scoped so there's only all. I don't actually need both of them right now."* The extension registers exactly the declared list ([ML-D3](#ML-D3)), so pi's "all" view for `openai-codex` is that list, and a scope could only restate it. pi 0.87.1 shows its Scope toggle only when the scoped list is non-empty (`ModelSelectorComponent`), and with no scope a fresh session starts on the saved `defaultProvider`/`defaultModel` pair (`findInitialModel`). **Existing jails need no migration code:** the selection's per-key deselect rule ([OQ-PSW2](../reference/providers.md#oq-psw2)) clears the list an older yolo recorded, with the codex profile still active, and notes it in `boot.log` ([OQ-PSW4](../reference/providers.md#oq-psw4)). A list the user or pi's save-as-default edited is kept. v0.10.0 wrote the scope as a plain computed key that no record names, and a jail it booted loses the list too: the upgrade boot recomposes the file from its layers, and none asserts the key any more. The log line used to give the reason "the profile that set it is no longer selected", false on this path, and now says "yolo's selection no longer sets it". **What it rules against:** it narrows the letter of [OQ-CN4](provider-credential-scope.md#OQ-CN4) for `openai-codex`, whose menu narrowing in pi is now the registered catalog rather than `enabledModels`, and supersedes, for `openai-codex` only, CN-D17's *"pi's `enabledModels` is unchanged"* (CN-D20 in [that ledger](provider-credential-scope.md#7-decision-ledger)) and [pi-model-selection-ux §4](../research/pi-model-selection-ux.md#4-recommendation). Every other provider keeps its scope; the non-codex arms render it from the same `models` map as their catalog rows | 2026-09-27 | [§8](#8-rendering-the-effective-list-into-each-picker) | ✅ `2a34a176`; the v0.10.0 upgrade pinned `05065539` |
| <a id="ML-D3"></a>ML-D3 | *Implementation decision.* **pi's extension reads the list from a data file yolo renders, and takes every pi-dialect fact from pi's own catalog.** The file is a computed JSON surface, `pi/codex-models`, at `~/.pi/agent/yolo-openai-codex-models.json`: beside `extensions/`, outside pi's extension discovery and outside the extension's own read-only delivery. It renders on every jail launch, with or without a profile, because the extension registers the provider on every launch. `yolo host apply` renders no list into it ([ML-D8](#ML-D8)). Each entry carries the id, a variant's `base`, the name and the context window. The extension looks each one up with `getBuiltinModel("openai-codex", base ?? id)`, imported from `@earendil-works/pi-ai/providers/all` (an alias pi's extension loader installs), and overrides id, name and context window: the consumer translates ([OQ-CS4](../reference/providers.md#oq-cs4)), so yolo no longer re-copies pi-ai. The catalog entry's address fields are dropped, so the registration alone says where requests go. An id pi's catalog lacks gets pi's own models.json defaults (`modelFromJson`), and the extension says so ([ML-D7](#ML-D7)). A missing, malformed or empty file registers no `models`, so pi keeps its built-in catalog and login never depends on the file. MEASURED 2026-09-27 by loading the shipped extension through pi 0.87.1's own `loadExtensions` with an isolated home, no CLI and no session: the import resolves, `gpt-6-sol[1m]` gets its base's facts at a 1,000,000-token window, an unknown id gets the defaults, and no file registers no `models`. The six GPT-6 definitions it registers equal, field for field, the ones the hand copy carried, so pi's cost, thinking and image behavior for them is unchanged. **Rejected channels:** an env var, which composes nothing without a profile while the extension always registers; a `models.json` row, the shadow [OQ-2](pi-codex-provider-shadowing.md#10-decision-ledger) forbids and which the extension's layer would override anyway; parsing `YOLO_PROVIDERS` in JavaScript, a second copy of the expansion reading an internal transport; and rendering the extension itself from Lua, which gives up its read-only files delivery | 2026-09-27 | [pi-codex-provider-shadowing §2.2](pi-codex-provider-shadowing.md#22-pi-pi-coding-agent) | ✅ `bbb7c223` |
| <a id="ML-D4"></a>ML-D4 | *Implementation decision.* **The GPT-5.x models are gone from pi's `openai-codex` list.** The extension registered five GPT-5.x ids only because pi-ai's catalog had been copied into it by hand in `e2f7bb89`: a registration replaces a provider's model list, and cannot add to it. claude's list and pi's scope had already dropped them as superseded (`2f11de95`). **Getting one back** takes one alias in the declaration, with its `model_options` for order, name and a 1M variant, or one alias in your own config, `providers.openai-codex.models` (`"gpt-5.6-sol": "gpt-5.6-sol"`). A model added in your config lists after the declared ones, with no 1M variant, since `order`, `description` and `long_context_window` are declaration facts the user's model entry does not take. Either way it appears in claude's picker too. **One consequence for a first login:** pi 0.87.1's own default for `openai-codex` is `gpt-5.5` (`defaultModelPerProvider`, `core/model-resolver.js`), and after `/login` a pi with no model yet selects that id from the provider's models (`completeProviderAuthentication`, `modes/interactive/interactive-mode.js`). In a jail the extension no longer registers it, so that login selects nothing and pi says *"its default model \"gpt-5.5\" is not available. Use /model to select a model."* v0.10.0, whose extension registered no models, selected `gpt-5.5` there. A `-p codex` launch is unaffected, since its settings pair names the declared default. At the host notch the extension registers no list ([ML-D8](#ML-D8)), so pi's catalog, `gpt-5.5` included, stays in place and the login selects it as before. Registering `gpt-5.5` again to satisfy the pick would put back the GPT-5 model this decision removes. Found in review, 2026-09-27 | 2026-09-27 | [§3](#3-what-exists-today) | ✅ `bbb7c223` |
| <a id="ML-D5"></a>ML-D5 | *Implementation decision.* **pi-subagents' `modelScope.allow` for `openai-codex` is the declared ids, exactly**: `openai-codex/<id>` for each entry, 1M variants included. It replaces the hand-written `openai-codex/gpt-6-*` glob, which was the family written down a third time. pi-subagents 0.35.1's `globToRegExp` escapes `[` and `]` before it turns `*` into a wildcard (`src/runs/shared/model-scope.ts`), so an id ending in `[1m]` matches literally. `enforce` and `strict` are unchanged. An empty list writes no `modelScope`, since an enforced, strict, empty allow would refuse every child model. *Superseded for the empty list on 2026-09-28 by [XM-D4](../research/extension-model-defaults.md#XM-D4): it now scopes `openai-codex/*`, and every provider gets the block* | 2026-09-27 | [§8](#8-rendering-the-effective-list-into-each-picker) | ✅ `bbb7c223` |
| <a id="ML-D6"></a>ML-D6 | *Implementation decision.* **Each id is one row, and the alias spelled as the id carries its facts.** Another alias naming the same id fills only a fact that row still lacks, taken in sorted alias order. The helper first kept whichever alias sorted first. `default` and `fast`, yolo's conventional alias names, sort before every declared id, so a user's `providers.openai-codex.models.default = "gpt-6-sol"` gave Sol that alias's facts, which were none. Sol then listed last with no name and no 1M variant, and every agent's default moved to Astra. Merging only into gaps also lets a second alias supply a fact the id's own alias lacks, such as a `name` for a model the user added. Found in review, 2026-09-27 | 2026-09-27 | [§3](#3-what-exists-today) | ✅ `4725106c` |
| <a id="ML-D7"></a>ML-D7 | *Implementation decision.* **The extension warns, once per load, when pi's catalog cannot describe a model it registers.** Without pi's catalog entry a model registers with the defaults [ML-D3](#ML-D3) names: text-only, no thinking levels and a 16,384-token output cap. That happens when the catalog import fails, when the module no longer exports `getBuiltinModel`, or when the catalog lacks an id, such as a model the user added. pi is not version-pinned in `packs/pi/pack.json`, and the hand copy the lookup replaced could not degrade this way, so a renamed alias would otherwise cost every model silently. The warning goes through pi's own `ctx.ui.notify(…, "warning")` at `session_start` when there is a UI, and to stderr when there is none, which is what pi's extension runner does with its own diagnostics. It names the ids the catalog lacks, or the import failure. An empty list attempts no import and says nothing, since pi's built-in catalog stays in place. MEASURED 2026-09-27 through pi 0.87.1's own `discoverAndLoadExtensions` with an isolated home: the shipped six-entry list resolves every entry from the catalog and warns nothing, and a list adding `gpt-6-nova` warns once, naming it. Found in review, 2026-09-27 | 2026-09-27 | [pi-codex-provider-shadowing §2.2](pi-codex-provider-shadowing.md#22-pi-pi-coding-agent) | ✅ `20769a45` |
| <a id="ML-D8"></a>ML-D8 | *Implementation decision*, **SUPERSEDED 2026-09-28** by [OQ-HC1](host-computed-layer.md#OQ-HC1): `yolo host apply` now renders the declared `openai-codex` list into `pi/codex-models` at the host, and host pi registers it ([host-computed-layer §14](host-computed-layer.md#14-what-was-built)); `TestTheHostNotchLeavesPiOnItsOwnCodexCatalog` is inverted as `TestTheHostNotchGivesPiTheDeclaredCodexList`. What this row decided: *Implementation decision.* **At the host notch pi keeps its own `openai-codex` catalog, and the docs say so.** `yolo host apply` renders a surface from its declared layers only: it runs no derive for content, since the host target's computed layer is empty by design ([host-render-target §3.3](host-render-target.md#33-what-each-target-supplies)), and it composes no provider table. `pi/codex-models` holds nothing but a derive's expansion of the declaration, so it renders as `{}` under `host_management: assert` and is refused under `own`, which runs no `computed` surface. The extension the host notch delivers then registers no models, and host pi shows pi-ai's built-in `openai-codex` list: in pi 0.87.1 the three GPT-6 ids with no `[1m]` variant, beside five GPT-5.x ids (MEASURED 2026-09-27 with `getBuiltinModels`). That is what v0.10.0 gave host pi too, since its extension registered the login alone. The hand copy that registered yolo's list at the host came later and never shipped in a release. There is still one list at the host, because a host apply selects no profile and no other agent renders an `openai-codex` list there. Whether the host should render the declaration is [OQ-ML3](#OQ-ML3). Three places said the file renders at every boot, and each now says every jail boot: [ML-D3](#ML-D3), the extension's comment and [pi-codex-provider-shadowing §2.2](pi-codex-provider-shadowing.md#22-pi-pi-coding-agent). `TestTheHostNotchLeavesPiOnItsOwnCodexCatalog` runs the host render, the host files delivery and the delivered extension in both contracts, and fails once the host renders a list. Found in review, 2026-09-27 | 2026-09-27 | [§3](#3-what-exists-today) | ✅ `f3da48dc` |
| <a id="ML-D9"></a>ML-D9 | *Implementation decision.* **The Bedrock list is declared once, on the `bedrock` provider in [`packs/bedrock/pack.json`](../../packs/bedrock/pack.json), and each agent picks among the entries its own client can call.** [OQ-ML1](#OQ-ML1)'s ruling (each provider's own pack declares its default) and [OQ-ML2](#OQ-ML2)'s (yolo picks only to make a session valid), applied to a provider of several makers ([`bedrock-plumbing.md` OQ-BR9](bedrock-plumbing.md#OQ-BR9)). Each entry carries `vendor` beside `order`, `name`, `context_window`, `max_tokens` and `input` in `model_options`, the channel [ML-D1](#ML-D1) set; there is no `default` alias, so each agent's default is the first entry it can call. The pick: codex, opencode and pi get one, because their own defaults on Bedrock are a first-party slug, unread, or possibly a bare id runtime refuses ([§4](#4-where-yolo-picks-the-cannot-default-cases)); claude's own Bedrock client gets none unless the profile or a user `default` names an Anthropic entry, since its own default is valid. The upstream-default clause of [OQ-ML2](#OQ-ML2) is not implemented as a comparison: no derive can read its agent's catalog default, and with none of the shipped entries being such a default the rule resolves to the declared one. [§5.3](#53-the-prerequisite-verify-before-an-id-ships)'s verify-before-an-id-ships rule is met per id, each read from its AWS model card on 2026-09-29 and dated in the pack README. The full set of per-agent decisions is [`bedrock-plumbing.md`'s BR-D6 to BR-D16](bedrock-plumbing.md#BR-D6) | 2026-09-29 | [§4](#4-where-yolo-picks-the-cannot-default-cases) | ✅ pinned by `TestTheShippedBedrockListDeclaresEachEntrysMaker`, `TestBedrockModelListHelperIsIdenticalInEveryDerive`, `TestEachBedrockAgentStartsOnTheRuledModelInEveryRegion` (codex on GPT-6.1 Sol and opencode and pi on Claude Opus 5.5 in every Region, with GPT-6 Sol not shipped, [BR-D19](bedrock-plumbing.md#BR-D19), which superseded [BR-D17](bedrock-plumbing.md#BR-D17)'s global-first pick) and each agent's `…OnBedrock…` test |
| OQ-ML3 | **A, for now: `yolo host apply` renders no `openai-codex` list, and host pi keeps pi-ai's own catalog.** *"basically option 1, but then make sure there's a design doc about the host option left."* Whether the host notch should derive what a jail derives is [`host-computed-layer.md`](host-computed-layer.md)'s [OQ-HC1](host-computed-layer.md#OQ-HC1) | 2026-09-27 | [OQ-ML3](#OQ-ML3) | ✅ [ML-D8](#ML-D8) |
| OQ-ML1 | **No separate picks pack, and nothing always on: each provider declares its own default in its own pack, and that default layers like all config.** A company or user pack, or user config, overrides it, and yolo does not chase the newest model. Several active providers per agent moved to [`active-provider-sets.md`](active-provider-sets.md) | 2026-09-29 | [OQ-ML1](#OQ-ML1) | the `openai-codex` declaration ([ML-D1](#ML-D1)) |
| OQ-ML2 | **yolo picks a model only to make a session valid, and never steers a valid choice unless the config opts in.** *"YOLO by default should never pick a default model … whenever you start up a session, we always want to make sure that we have picked one of those models."* The pick is the agent's upstream default when that is one of the provider's models, else the provider's declared default, else the first model it lists. Narrower than the leaning | 2026-09-29 | [OQ-ML2](#OQ-ML2) | — |
| OQ-BR12 | **A: a `models` contribution kind, with the verbs `add` and `only`, for any provider; the engineer's own `providers.<name>.models` writes last.** *"of course their own config still has the last word because they could make that happen regardless."* Moved from [`bedrock-plumbing.md`](bedrock-plumbing.md), id kept | 2026-09-29 | [§7](#7-how-a-company-pack-shapes-a-list-the-models-kind) | ✅ `packload.applyModelContributions`, pinned by `internal/packload/modellists_test.go` ([MM-D11](#MM-D11)) |
| OQ-BR13 | **Directed: set the model selection however each agent allows.** *"I want us to set the model selection however we can … I need you to research whatever it is and then come back to me if there's a real decision here."* Researched per agent in [§14](#14-how-each-agents-menu-is-set-researched-2026-09-29); the mechanisms are [MM-D1](#MM-D1) to [MM-D10](#MM-D10), and what is left to rule is [OQ-MM1](#OQ-MM1) and [OQ-MM3](#OQ-MM3). [OQ-MM2](#OQ-MM2) was settled on a corrected premise | 2026-09-29 | [§14](#14-how-each-agents-menu-is-set-researched-2026-09-29) | `openai-codex` ([ML-D1](#ML-D1)); on 2026-09-29 every mechanism [OQ-MM1](#OQ-MM1) and [OQ-MM3](#OQ-MM3) do not decide ([§14.5](#145-what-was-built-2026-09-29)); on 2026-09-30 pi's wrapper and MM-D9 on `openai-codex` ([§14.6](#146-what-was-built-2026-09-30)); MM-D10 stopped ([OQ-MM4](#OQ-MM4)) |
| OQ-BR15 | **Never: the bridge serves no `GET /v1/models`.** Settled by the measurement the question waited on, under [OQ-BR13](#OQ-BR13)'s direction ([MM-D4](#MM-D4)) | 2026-09-29 | [§14.3](#143-the-bridges-part-and-get-v1models) | — |
| OQ-MM2 | **B: if copilot's `providers.json` cannot read its key from the environment, the launch's per-agent env writer puts the file beside `agent-env/copilot.sh`, with the same mode and lifetime.** Settled on a corrected premise, rather than asked: the key is already in that 0600 file, so a second file adds no reader, while a rendered config file would break the caller-token rule ([MM-D10](#MM-D10)) | 2026-09-29 | [OQ-MM2](#OQ-MM2) | — |
| <a id="MM-D1"></a>MM-D1 | *Implementation decision.* **claude's picker keys follow where the list reaches.** On a routed provider, whose list is the whole universe (the bridge, a gateway's own Anthropic endpoint, `openai-codex`), `yolo.derive("claude", "settings")` always writes `modelPicker` with `replaceBuiltInOptions: true`. The built-ins go because on a routed provider they are only claude's tier names, meaning whatever the tier pins say ([MM-D2](#MM-D2)), so the list's own rows show instead. That revises [OQ-BR13](#OQ-BR13)'s sub-choice "built-ins replaced only on the everything profile". `availableModels`, with the default first, and `enforceAvailableModels: true` render on `openai-codex`, as today, and under an `only`, while the profile's switch is on ([MM-D5](#MM-D5)). The allowlist is claude's one way to make a session valid without steering it, which [OQ-ML2](#OQ-ML2)'s ruling asks for: an off-list saved model is replaced at startup by Default, which the tier pins make a list model ([§14.2](#142-what-each-row-rests-on)), while a valid `/model` choice survives the next launch ([MM-D3](#MM-D3)). Whether the allowlist also renders on a routed list with no `only` is [OQ-MM3](#OQ-MM3): there a gateway such as z.ai, Kilo or OpenRouter serves more than the list, and claude would refuse a typed id the list lacks. Rendering it there by default would also revise the sub-choice "`enforceAvailableModels` only under an `only`". On the native profile it writes the same three under an `only`. What an `add` writes there is [OQ-MM1](#OQ-MM1)'s; under its leaning, rows appended with `replaceBuiltInOptions: false` and no allowlist. This restates [§11](#11-forbidden-and-non-goals)'s ban as: no refusal on a list that sits beside an agent's own catalog. The keys go to `~/.claude/settings.json`, the one scope besides managed settings and `--settings` that reads `modelPicker` | 2026-09-29 | [§14.1](#141-the-table) | ✅ the routed picker, `TestClaudeOnARoutedProviderShowsTheListAndPinsEveryTier` and `TestClaudeOnABridgedProviderShowsTheList`; the three keys under an `only`, native and routed, `TestClaudeUnderAnOnlyOnItsOwnBedrockClient` and `TestClaudeUnderAnOnlyOnARoutedProvider`; the allowlist on a routed list no `only` narrowed waits on [OQ-MM3](#OQ-MM3) |
| <a id="MM-D2"></a>MM-D2 | *Implementation decision.* **Every list that replaces claude's built-ins pins all of claude's tiers to list ids**: a routed list ([MM-D1](#MM-D1)), and an `only`. `ANTHROPIC_DEFAULT_OPUS_MODEL`, `_SONNET_`, `_HAIKU_` and `_FABLE_`, with their `_NAME` and `_DESCRIPTION` where the entry has a name or description: each takes the tier's alias where the list declares one (which names count is [OQ-PSW1](#OQ-PSW1)'s, decided as [MM-D17](#MM-D17)), else the default entry. Why: claude's background, hook and classifier requests pick by tier and `availableModels` does not cover them, so an unpinned tier reaches an off-list model or draws the bridge's refusal. And the Default row resolves by tier whenever that tier's model is allowed, so on a list of non-Anthropic ids the pins are the only thing that makes Default a list model; under a managed source it resolves by tier too ([§14.2](#142-what-each-row-rests-on)). **Beside claude's own rows**, under an `add` on the native profile ([OQ-MM1](#OQ-MM1)'s leaning), only the tiers the list names by alias are pinned. The retained Sonnet and Haiku rows, and the haiku-tier background traffic, then keep resolving as claude resolves them, a valid choice of the agent's own that [OQ-ML2](#OQ-ML2) forbids yolo to steer. This supersedes [OQ-BR13](#OQ-BR13)'s sub-choice "the Fable tier only from a declared `fable` alias" wherever the built-ins are replaced. **A pin travels with what makes it valid:** in the process env beside `ANTHROPIC_BASE_URL` on a routed provider, and in `claude/settings`' `env` block beside `CLAUDE_CODE_USE_BEDROCK` on the native profile, the channel [PV-D8](../reference/providers.md#pv-d8) chose because a host apply and a bare `claude` both keep it. A routed pin never goes in the settings file, where a bare `claude` would send it to Anthropic | 2026-09-29 | [§14.2](#142-what-each-row-rests-on) | ✅ `openai-codex`, `TestClaudeOnTheCodexListPinsTheStartOnlyOnTheOptIn`; routed, `TestClaudeOnARoutedProviderShowsTheListAndPinsEveryTier`; under an `only`, native in `settings.json`'s `env`, `TestClaudeUnderAnOnlyOnItsOwnBedrockClient`; the tiers claude's own rows keep beside an `add` wait on [OQ-MM1](#OQ-MM1) |
| <a id="MM-D3"></a>MM-D3 | *Implementation decision.* **claude's start model is pinned only on an opt-in the derive can see.** `ANTHROPIC_MODEL` and `CLAUDE_CODE_SUBAGENT_MODEL` are emitted only when the profile sets `pin_model` *(an option name coined here, provisional)*, the opt-in [OQ-ML2](#OQ-ML2)'s ruling allows; like any profile option, the provider's declared option set must admit it (the census [ML-D1](#ML-D1) names). The profile's `model` option cannot be the opt-in. `packload.ResolveProfiles` merges each provider's declared option defaults under the profile's own values, so that *"a derive never has to know whether a value was stated or inherited"* (`ResolvedProfile`). zai's declared `glm-5.3`, and cerebras's and llamacpp's `default`, therefore read exactly like a user's pin, and [OQ-ML1](#OQ-ML1)'s ruling has every provider declare a default, so every provider would count as opted in. `model` keeps its job of naming the list's default entry, which [MM-D2](#MM-D2)'s tier pins and `availableModels`' order lead with. With `availableModels` and enforcement on, an off-list start model is already replaced by Default, which the tier pins make a list model ([MM-D2](#MM-D2)), so a valid start needs no pin; where the allowlist does not render, what keeps the start valid is [OQ-MM3](#OQ-MM3)'s. Until that is ruled, those providers keep today's pin: it steers, but dropping it would leave their start unchecked ([§14.4](#144-build-order)). The variable would override every `/model` choice at every launch, since claude returns to it each time. Subagents otherwise inherit the session's model or their tier, both on the list by [MM-D2](#MM-D2). Changes the `openai-codex` branch of `yolo.env("claude")` and its provider-`models` branch | 2026-09-29 | [§14.2](#142-what-each-row-rests-on) | ✅ `openai-codex`, `TestClaudeOnTheCodexListPinsTheStartOnlyOnTheOptIn`, and under an `only`, `TestClaudeUnderAnOnlyOnARoutedProvider`; `pin_model` declared, with no default, on the shipped providers that declare options; every other provider keeps today's pin until [OQ-MM3](#OQ-MM3) |
| <a id="MM-D4"></a>MM-D4 | *Implementation decision.* **The bridge serves no `GET /v1/models`, now or later.** claude's gateway discovery survives `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` but keeps only ids containing "claude" or "anthropic", never runs while a `CLAUDE_CODE_USE_*` variable is set, adds only allowed models, and is hidden by `replaceBuiltInOptions`; opencode, codex and copilot never ask such a route (MEASURED and SOURCED, [§14.3](#143-the-bridges-part-and-get-v1models)). [WB-D14](../reference/wire-bridge.md#wb-d14)'s one-route surface is unchanged | 2026-09-29 | [§14.3](#143-the-bridges-part-and-get-v1models) | — (nothing to build) |
| <a id="MM-D5"></a>MM-D5 | *Implementation decision.* **One switch decides every refusal, and a list never changes an agent's path.** [OQ-WG3](wire-bridge-gateway.md#OQ-WG3)'s enforcement switch, a profile setting that defaults on, governs each refusal yolo installs: the bridge's allowlist, claude's `availableModels` and `enforceAvailableModels`, and pi's wrapper. With it off the list only shapes menus: claude gets `modelPicker` alone, so an off-list saved model is no longer replaced at startup, and pi gets a registration without the wrapper. Nothing yolo writes then checks claude's start, which [OQ-ML2](#OQ-ML2)'s ruling requires; what does is [OQ-MM3](#OQ-MM3)'s, and under its leaning yolo writes the start model once through claude's selection namespace. opencode cannot shape its menu without refusing, so with the switch off it gets no whitelist, and `yolo check` says its menu is then not narrowed. **On claude's native profile the refusal is claude's own, client side**, with [§14.2](#142-what-each-row-rests-on)'s four gaps, and yolo describes it that way, as [OQ-CN4](provider-credential-scope.md#OQ-CN4) requires of a soft limit. An `only` never moves claude onto the bridge, since a profile picks one path ([OQ-WG5](wire-bridge-gateway.md#OQ-WG5)); a company that wants the wire to refuse selects a bridged profile. yolo writes no managed Claude Code settings file, which would close the prefix gap but override the engineer's own settings, against [OQ-BR12](#OQ-BR12)'s *"their own config still has the last word"* | 2026-09-29 | [§14.1](#141-the-table) | ✅ the switch, `enforce_models` ([MM-D13](#MM-D13)), `TestTheModelEnforcementSwitchReachesBothDerivePaths`; it governs claude's allowlist, `TestClaudeUnderAnOnlyOnARoutedProvider` and `TestClaudeOnTheCodexListWithEnforcementOff`, and opencode's whitelist, `TestOpencodeWhitelistsANarrowedList`; the bridge's allowlist, 2026-09-30 ([`wire-bridge-gateway.md` WG-I40](wire-bridge-gateway.md#WG-I40)); pi's wrapper, 2026-09-30 ([MM-D21](#MM-D21)), `TestPiModelListsExtensionRefusesAModelOutsideTheList` and `TestPiNarrowedListCarriesItsProfilesModelSwitch`, and on `openai-codex` ([MM-D23](#MM-D23)), `TestPiOpenAIAuthExtensionRefusesNothingWithTheSwitchOff`; the `yolo check` line for opencode with the switch off not built |
| <a id="MM-D6"></a>MM-D6 | *Implementation decision.* **pi: an `only` becomes an extension registration of exactly the list, for any provider pi ships.** The data travels the way `pi/codex-models` does ([ML-D3](#ML-D3)), generalized into one derive-rendered file keyed by pi's provider id (yolo's native `bedrock` is pi's `amazon-bedrock`), and each entry's facts come from `getBuiltinModel`. With the switch off the registration passes `models` only, which keeps pi's own address, wire and credential (MEASURED). With it on ([MM-D5](#MM-D5)) the registration is `models`, `api` and a `streamSimple` wrapper, since pi 0.99.1 refuses a `streamSimple` without `api`. That pi's own credential still reaches the delegated stream in that form is INFERRED, and measured before it ships ([§14.4](#144-build-order)). Either form sets its own `name`, so pi 0.99 does not label yolo's `openai-codex` provider "(legacy)", and each row's display name follows [MM-D7](#MM-D7)'s rule. Where the registration is exact, no `enabledModels` is written for that provider, as [ML-D2](#ML-D2) did for `openai-codex`. An `add` stays a `models.json` row (what it shows is [OQ-MM1](#OQ-MM1)'s). **An `only` narrows only the provider it names:** providers the engineer signed into (pi's `auth.json`, oh-omp's `agent.db`) are untouched, and withholding a credential stays [`provider-credential-scope.md`](provider-credential-scope.md#OQ-CN4)'s | 2026-09-29 | [§14.2](#142-what-each-row-rests-on) | ✅ the models-only registration under an `only`, `TestPiGetsANarrowedListToRegister`, `TestPiModelListsExtensionRegistersTheRenderedListAlone` and `TestPiRegistersNoNarrowedListItCannotUse` ([MM-D14](#MM-D14)); the `openai-codex` registration's name, `TestPiOpenAIAuthExtensionNamesTheProviderItRegisters`; the selection's `defaultModel` on the list's default entry under an `only` that dropped the profile's model, `TestPiStartsOnANarrowedListsDefault`; the refusing wrapper, 2026-09-30, once its credential path was MEASURED ([§14.4](#144-build-order), [MM-D21](#MM-D21)), `TestPiModelListsExtensionRefusesAModelOutsideTheList`; and on `openai-codex`, whose list reaches pi through `yolo-openai-auth.js`'s registration of the subscription login ([MM-D14](#MM-D14)), the same day, once the login's path through a wrapper was MEASURED too (the first measurement covered a bearer token, SigV4 keys and a models.json row's key), `TestPiOpenAIAuthExtensionRefusesAModelOutsideTheCodexList` ([MM-D23](#MM-D23)) |
| <a id="MM-D7"></a>MM-D7 | *Implementation decision.* **opencode: `provider.<id>.whitelist` is the list's ids, each also declared under `provider.<id>.models`**, beside today's `enabled_providers`, wherever the menu is to be exact (a whole-universe list or an `only`, [§14.1](#141-the-table)) and the switch is on ([MM-D5](#MM-D5)). **A model's display `name` is the entry's own `name` fact, or absent so the agent's catalog name shows, never its yolo alias**, which today shows an entry under `default` as "default". The rule is stated once for every catalog-writing derive: opencode's `provider.<id>.models`, pi's `models.json` rows and registration ([MM-D6](#MM-D6)), and oh-omp's `models.yml` rows ([MM-D8](#MM-D8)), which today use the alias the same way. opencode's order, newest first, is accepted; yolo writes no made-up `release_date` | 2026-09-29 | [§14.2](#142-what-each-row-rests-on) | ✅ display names, `TestNoCatalogRowIsNamedAfterItsAlias`; the whitelist under an `only`, `TestOpencodeWhitelistsANarrowedList`; the selection on the list's default entry under an `only` that dropped the profile's model, `TestOpencodeStartsOnANarrowedListsDefault`; the whitelist on opencode's own Bedrock row, `TestOpencodeWhitelistsANarrowedListOnItsOwnBedrockClient`; a whole-universe list's whitelist waits on [OQ-MM3](#OQ-MM3) |
| <a id="MM-D8"></a>MM-D8 | *Implementation decision.* **oh-omp gets a settings surface, `~/.oh-omp/agent/config.yml`, writing `enabledModels` as the list's exact ids**, and `modelRoles.default` through the selection namespace, the way pi's `defaultModel` is written. Its `models.yml` rows take [MM-D7](#MM-D7)'s display-name rule. A scope naming ids omp does not know fails open to its full list; yolo cannot see that at render time, and it is the case [OQ-BR14](#OQ-BR14)'s `yolo check` warning exists for. On 0.15.x a current Bedrock list exists only as the via row under yolo's `bedrock` key, so omp's native Bedrock client gets no list. `packs/omp` stays on its pinned fork here; whether it should move to upstream oh-my-pi, which appears to keep runtime registrations, is that pack's question | 2026-09-29 | [§14.2](#142-what-each-row-rests-on) | ✅ `TestOmpScopesANarrowedList`, with no `modelRoles.default` write ([MM-D12](#MM-D12)) |
| <a id="MM-D9"></a>MM-D9 | *Implementation decision.* **codex gets an exact menu from a catalog yolo filters at prelaunch.** Before codex starts (it installs lazily, so not at boot), the launcher reads `codex debug models --bundled`, which its help describes as *"Skip refresh and dump only the bundled catalog shipped with this binary"*, so offline for the OpenAI catalog (SOURCED). For Bedrock runtime this waits on a measurement ([§14.4](#144-build-order)): `--bundled`, with that provider selected, must print the Bedrock runtime catalog (`amazon_bedrock/catalog.rs`), not the OpenAI one, offline and with no credential. The launcher keeps the list's ids; renumbers `priority` in list order; applies each entry's name as `display_name`; clears `upgrade` and `availability_nux`; and writes a per-launch file that `model_catalog_json` names, through the selection. **An id the installed catalog lacks is left out, with a warning like [ML-D7](#ML-D7)'s**, never written without instructions, since that runs codex with empty system instructions. Only providers whose ids codex's own catalog carries get a file (`openai-codex`, and Bedrock runtime once that measurement holds); a custom provider keeps `model` alone until an entry safe for a non-OpenAI model is measured. When the bridge refuses on a via route, the derive points `review_model` and the memories models at listed ids. It is the first step where a program's output feeds a render, so it lives in the launcher, not a derive, whose sandbox has no `io`. It freezes the subscription's live list for the session, as claude and pi already do for `openai-codex` | 2026-09-29 | [§14.2](#142-what-each-row-rests-on) | ✅ 2026-09-30 for `openai-codex`, as [MM-D22](#MM-D22) builds it: `TestCodexLauncherHandsCodexItsModelMenu`, `TestCodexModelListIsTheSubscriptionsListOnly`. The Bedrock runtime half is not built: its measurement did not hold, since `--bundled` prints OpenAI's catalog whatever the provider ([§14.4](#144-build-order)). The `review_model` and memories pins wait on a via route, which `openai-codex` has none of. (The selection under an `only`, `TestCodexSelectsANarrowedListsDefault`) |
| <a id="MM-D10"></a>MM-D10 | *Implementation decision.* **copilot's whole list goes in a `providers.json` file named by `COPILOT_PROVIDERS_CONFIG`**: one provider, the endpoint copilot's env derive picks today with its `anthropic` or `openai` type (the bridge's on a bridged provider, the provider's own on a direct one, [§14.2](#142-what-each-row-rests-on)), and one model row per entry with `wireModel` the wire-true id. Its key is the value `COPILOT_PROVIDER_API_KEY` carries today: the launch's caller token on a bridged provider, and the provider's own key, such as z.ai's, on a direct one. **Where the file can name its key by environment variable**, it is a secret-free `copilot/providers` surface rendered by `yolo.derive("copilot", "providers")`. **Where it cannot**, it is written host-side by the per-agent env writer, beside `agent-env/copilot.sh`, with the same mode and lifetime, from the env derive that already holds the key, since a surface derive never receives one ([OQ-MM2](#OQ-MM2)). It needs a copilot that has `providers.json`, which 1.0.89 has and 1.0.48 lacks, and four measurements first ([§14.4](#144-build-order)). Until then copilot keeps one `COPILOT_MODEL`. A `model` default for copilot, if ever written, belongs in `~/.copilot/settings.json`, where copilot has kept user settings since 1.0.35, moving any it finds in `config.json` | 2026-09-29 | [§14.2](#142-what-each-row-rests-on) | — **stopped 2026-09-30** on its measurement: the file's models join GitHub's own ([§14.4](#144-build-order)), which is [OQ-MM4](#OQ-MM4)'s to rule (`COPILOT_MODEL` under an `only`, `TestCopilotStartsOnANarrowedListsDefault`; the `settings.json` home for a copilot setting, [MM-D15](#MM-D15)) |
| <a id="MM-D11"></a>MM-D11 | *Implementation decision.* **The `models` kind composes in two passes inside `packload.ComposeProviders`, between the packs' own providers and the user's entries.** Every `add` applies, in pack order then declaration order, and then every `only`, the `only`s intersecting. Two passes because the literal "packs apply in `packs` order" of [§7.2](#72-composition-rules) would let an `add` from a pack listed after a company's `only` re-open it. An added entry lands under its own id, its facts in `model_options` in the flat vocabulary a user's object-form entry lowers into, plus `vendor`, `description` and `order`, and its `alias` beside it; a duplicate id or alias keeps the first writer's, and an `only` id nothing added is dropped. For a provider some contribution names, the list's current order is first written down as `order` facts, which added entries continue, because every consumer sorts entries with an `order` ahead of those without; a provider no contribution names composes byte-identically. A narrowed entry carries `models_only: true` (`packload.ModelsOnlyKey`), a composed fact no user key can spell, and it is what each derive renders an exact menu from. The user's `providers.<name>.models` composes last PER ALIAS rather than replacing the list, as [§7.2](#72-composition-rules)'s letter says: replacing would end [ML-D4](#ML-D4)'s one-alias recovery and change how every string-form map composes today. A provider only the user declares is shaped too, and one nobody declares is inert. `yolo check` names what the pass could not do (`packload.WithModelNotes`) | 2026-09-29 | [§7.2](#72-composition-rules) | ✅ `packload.applyModelContributions`, pinned by `internal/packload/modellists_test.go`; an `only` dropping two adjacent entries, which kept the second, fixed and pinned by `TestAModelsOnlyDropsAdjacentEntries` |
| <a id="MM-D12"></a>MM-D12 | *Implementation decision.* **oh-omp's start needs no `modelRoles.default` write, revising [MM-D8](#MM-D8).** The selection namespace lifts top-level scalars and arrays only, and `modelRoles.default` is a key of a record. With a scope and no `--model`, oh-omp 0.15.3 starts on the model its own `modelRoles.default` remembers when that is in the scope, else on the scope's first entry (read in the shipped binary's startup code, not run). So an `enabledModels` scope that leads with the default entry makes every start valid without steering a valid saved choice, which is [OQ-ML2](#OQ-ML2)'s ruling | 2026-09-29 | [§14.2](#142-what-each-row-rests-on) | ✅ `TestOmpScopesANarrowedList`; the default-first scope pinned where the default is not first in list order, `TestOmpScopeLeadsWithADefaultNotFirstInOrder` |
| <a id="MM-D13"></a>MM-D13 | *Implementation decision.* **The switch is spelled `enforce_models`, a profile FIELD that defaults on** (the name is coined here). A field like `via`, not a provider option, so no provider's option census has to admit it: it is a fact about the selection. It is a boolean in user config (a string is refused), and a `kind: "profile"` contribution may carry it; the user's value wins over a pack's. It crosses in `YOLO_PROFILES` under the reserved `_enforce_models` key only when a profile states it, and a derive reads `ctx.enforce_models`, true unless the profile says false, so an entrypoint older than the key hands its derives the default. `pin_model`, [MM-D3](#MM-D3)'s provisional name, is kept, and declared with no default on the shipped providers that declare options (zai, cerebras, llamacpp, kilo, openrouter), so their census admits it | 2026-09-29 | [§14.1](#141-the-table) | ✅ `TestAProfileEnforceModelsIsABooleanField`, `TestTheModelEnforcementSwitchReachesBothDerivePaths`, `TestClaudeUnderAnOnlyOnARoutedProvider` |
| <a id="MM-D14"></a>MM-D14 | *Implementation decision.* **pi's narrowed lists travel in a second data file, `pi/model-lists` (`~/.pi/agent/yolo-model-lists.json`), keyed by pi's provider id and registered by a second extension, `yolo-model-lists.js`,** rather than in one generalized file, as [MM-D6](#MM-D6)'s letter says. The `openai-codex` registration also carries the subscription login, so it stays in `yolo-openai-auth.js` and `pi/codex-models`, where a narrowed `openai-codex` list already arrives through the composed table. The file holds only providers pi can reach: one with a models.json row, the via row, or yolo's Bedrock provider as pi's `amazon-bedrock`. pi refuses a registration for a provider it has no address or credential for, and that would fail the extension's whole load | 2026-09-29 | [§14.2](#142-what-each-row-rests-on) | ✅ `TestPiGetsANarrowedListToRegister`, `TestPiRegistersNoNarrowedListItCannotUse` |
| <a id="MM-D15"></a>MM-D15 | *Implementation decision.* **copilot's footer default moves to a second read-modify-write surface, `copilot/settings` at `~/.copilot/settings.json`; `copilot/config` keeps `config.json` and its `yolo: true` default.** copilot 1.0.48's migration moves only the keys of its settings schema out of `config.json`, and `yolo` is not one of them (read in `app.js`), so it stays; keeping the surface keeps its token-preserving read-modify-write behavior unchanged | 2026-09-29 | [§14.2](#142-what-each-row-rests-on) | ✅ `TestCopilotStatusLineDefaultLandsInSettingsJSON` |
| <a id="MM-D16"></a>MM-D16 | *Implementation decision*, under [OQ-ML1](#OQ-ML1) and [OQ-ML2](#OQ-ML2). **A list stays current through the agents' own catalogs and the user's own config, and `yolo check` warns about a listed id that no installed agent's catalog knows** ([OQ-BR14](#OQ-BR14)'s option A). The check is `yolo check`'s alone: a launch makes no model-list network call. An unknown id is a warning, never a refusal. When no catalog could be read the check says it could not ask, a different answer from "not found". There is no "a newer model exists" report, because no catalog names a family. **Why:** the 2026-09-29 rulings make each provider's default one simple pick the user changes, so yolo does not chase the newest model, which rules out a launch-time control-plane call (B) and a catalog snapshot in yolo (C), and [providers.md](../reference/providers.md#what-this-does-not-license) forbids discovery. Of the two left, A over D because under D a retired id surfaces as a 404 at the first request, while [OQ-ML2](#OQ-ML2) asks that a session start on a valid model; the warning is the cheapest way to say so first. Reversible: dropping the warning is D | 2026-09-30 | [§9](#9-currency-the-agents-catalogs-plus-a-staleness-warning) | ✅ 2026-09-30: `modelCatalogReport` (`internal/cli/check/modellists.go`), called from the Packs section beside the `models` kind's notes, over the catalogs [MM-D19](#MM-D19) reads and the lists [MM-D20](#MM-D20) names; `TestCheckWarnsAboutAListedIDNoInstalledCatalogKnows`, `TestCheckSaysItCouldNotAskWhenNoCatalogIsInstalled`, `TestCheckPassesWhenEveryListedIDIsKnown`, `TestCheckReadsTheHostFloorsCopyAtTheHost` and `TestCheckReadsPisCatalogThroughTheShippedDeclaration` (`internal/cli/check/modelcatalog_test.go`) drive the section and each fails with the call removed |
| <a id="MM-D19"></a>MM-D19 | *Implementation decision, building [MM-D16](#MM-D16).* **An agent's catalog is the JSON files its pack names in a new `model_catalog` field of an npm `program`, read where the agent is installed, and nothing is run to learn it.** Each entry is a glob relative to the installed package's directory; every string an object holds under an `id` key, at any depth, is a known id; an id counts as known when any catalog read names it, under any provider. The directory is the jail's npm prefix in a jail, and at the host yolo's floor copy (`hostfloor.Record.NpmPackageDir`) when the floor's disposition (`hostfloor.Floor.Status`, the answer the Host agent floor section prints) says it is the copy `yolo host --` runs. A program with no floor entry runs from the launch's PATH, so a copy the floor still holds for it is never read. An agent not installed there, one with no floor entry, or a release whose files the glob does not match, is a catalog the check could not ask. **Why:** core may not know where an agent keeps its catalog ("core does not know what an agent is"), so the pack declares it, and only its pack can keep it current. Reading files keeps `yolo check` an observe verb: the alternative, running `codex debug models --bundled` or pi's own model listing, starts an agent program and, behind a lazy launcher, would install one. The cost is coverage: of the agents installed in this jail only pi's catalog is a data file (pi-ai's `dist/providers/data/*.json` in 0.99.1, MEASURED; codex 0.145.0, copilot 1.0.48 and opencode 1.18.32 ship none, and opencode's and oh-omp's are built into their binaries, [§14.2](#142-what-each-row-rests-on)), so today the check reads pi alone. A union rather than a per-provider lookup because the catalogs key providers by the agent's own ids (pi's `amazon-bedrock` for yolo's `bedrock`), a mapping core would have to learn; a union can only miss a warning, never invent one | 2026-09-30 | [§9](#9-currency-the-agents-catalogs-plus-a-staleness-warning) | ✅ 2026-09-30: `packdecl` (`Contribution.ModelCatalog`, its validation, `Install.ModelCatalog`), `internal/modelcatalog` (`Read`), `packs/pi`'s declaration; `internal/packdecl/modelcatalog_test.go`, `internal/modelcatalog/modelcatalog_test.go`, `internal/hostfloor/npmpackagedir_test.go`. Found in review the same day: the host read took any record the prefix held, so with `host_floor` leaving pi out it checked against a copy `yolo host` never runs; it now asks the disposition (`TestCheckDoesNotReadAFloorCopyYoloHostDoesNotRun`). And in a jail it said there was nowhere to look when `NPM_CONFIG_PREFIX` was unset, which the macos-user sandbox's environment list never sets; it now resolves the prefix by the entrypoint's one rule, `$HOME/.npm-global` by default, as every generated launcher installs (`TestCheckReadsTheJailsDefaultNpmPrefix`) |
| <a id="MM-D20"></a>MM-D20 | *Implementation decision, building [MM-D16](#MM-D16).* **Every provider in the composed table is checked except one every declared endpoint of which is on this machine (`localhost`, a loopback address, `host.containers.internal`); an unknown id is one warning per provider, no catalog read is one skip, and a clean check is one pass naming what it read.** Every provider, the shipped lists included, because [OQ-BR14](#OQ-BR14)'s premise is that the warning keeps yolo's own ids honest. A local server's ids are what that server serves, which no vendor catalog lists, so checking `packs/llamacpp`'s `llama` would warn on every run and train a reader to skip the badge. A skip rather than a warning or silence when nothing could be read, [MM-D16](#MM-D16)'s "could not ask": a warning would be a finding there is none of, and silence would read as a pass | 2026-09-30 | [§9](#9-currency-the-agents-catalogs-plus-a-staleness-warning) | ✅ 2026-09-30: `listedModelIDs` and `onlyLocalEndpoints`; the local provider in `TestCheckWarnsAboutAListedIDNoInstalledCatalogKnows` draws no warning |
| <a id="MM-D17"></a>MM-D17 | *Implementation decision*, under [OQ-XM2](../research/extension-model-defaults.md#OQ-XM2). **claude's derive reads the conventional aliases for its tiers: `balanced` pins the Sonnet tier, `fast` the Haiku tier, and `frontier` the Opus tier wherever a tier pin reads an alias ([MM-D2](#MM-D2)'s pins); `sonnet`, `haiku` and `opus` stay as synonyms, and a vendor name wins where a provider declares both** ([OQ-PSW1](#OQ-PSW1), moved with synonyms). On a routed provider the Opus pin stays the selected model, as today. **Why:** [OQ-XM2](../research/extension-model-defaults.md#OQ-XM2) ruled the vocabulary yolo publishes and that each adapter maps it onto its agent's own names, and [XM-D2](../research/extension-model-defaults.md#XM-D2) warns every provider that lacks one of the four, so providers will declare them; a claude derive that reads only vendor names would leave its Sonnet and Haiku tiers on the default for such a provider. The vendor name wins because whoever wrote `sonnet` wrote it for claude. The synonyms keep every existing config working. Reversible: claude's tier names are read in one table (`claudeTiers`) and one routed branch | 2026-09-30 | [§6](#6-tier-aliases-default-fast-balanced) | ✅ 2026-09-30: each tier's names in `claudeTiers` (`packs/claude/derive.lua`), read by `tierAlias` in all four tier reads: the `openai-codex` list's pins, the pins under an `only`, the routed fable pin, and the provider branch's Sonnet and Haiku pins. `TestClaudeRoutedTiersReadTheConventionalAliases`, `TestClaudeTierVendorNameWinsOverTheConventionalAlias`, `TestClaudeNativeBedrockTiersReadTheConventionalAliases` and `TestClaudeUnderAnOnlyReadsTheConventionalAliases` (`internal/entrypoint/claudetieraliases_test.go`), and on the `openai-codex` list `TestAnAliasForADeclaredCodexIDChangesNoConsumer`, whose `fast` alias now moves claude's Haiku tier; all but the vendor-name cell fail with the conventional names dropped from the table |
| <a id="MM-D18"></a>MM-D18 | *Implementation decision, building [MM-D17](#MM-D17).* **A tier takes the first of its names that the provider declares for a model claude's client can call; a name it cannot call gives way to the tier's next name.** On claude's own Bedrock client, a `sonnet` naming another maker's model (Bedrock's Messages API serves Claude alone, [OQ-BR9](bedrock-plumbing.md#OQ-BR9)) no longer leaves the Sonnet tier on the default when `balanced` names a Claude model; under an `only`, a name outside the narrowed list gives way the same way. The letter of MM-D17, "a vendor name wins where a provider declares both", is kept wherever both names are usable. Chosen over letting the unusable vendor name win and falling to the default, because the default is what MM-D17 exists to stop a tier landing on when the provider named a model for it. One helper, `tierAlias`, answers every tier read | 2026-09-30 | [§6](#6-tier-aliases-default-fast-balanced) | ✅ 2026-09-30: `TestClaudeNativeBedrockTiersReadTheConventionalAliases` (its `sonnet` names a Moonshot model and its `balanced` a Claude one) |
| <a id="MM-D21"></a>MM-D21 | *Implementation decision, building [MM-D6](#MM-D6)'s refusing form on the 2026-09-30 measurement ([§14.4](#144-build-order)).* **The wrapper hands a listed model to the stream pi would have run it on, rebuilt from what pi exports: the built-in provider of that id when it serves the model's api, else pi's api registry, with pi's options untouched.** That is what pi 0.99.1's `composeModelProvider` does for a registration without a `streamSimple`, and pi resolves the credential into those options before it calls the wrapper, so the credential needs no handling of yolo's (MEASURED for a bearer token, SigV4 keys and a models.json row's `${VAR}` key). **The registration names one api**: the derive states the api of the models.json row it writes for the provider, and for pi's own Bedrock client, which has no row, the extension takes the api pi's catalog gives the listed ids. **A list whose models run on two pi apis, or whose stream cannot be found, is registered as the exact menu alone, and pi warns once that it cannot refuse there.** pi hands a wrapper only the models of its registration's api, and pi's `--model` fallback copies a listed model of either api, so a wrapper on one would refuse only part of what `--model` reaches, which reads as enforcement and is not. No list the derive renders reaches that branch today: every provider but pi's own Bedrock client carries its models.json row's one api, OpenRouter's included although pi's own OpenRouter catalog mixes apis, and pi's Bedrock catalog is on one api (`TestPiNarrowedOpenRouterListNamesItsRowsOneAPI`), so it guards a pi whose catalog or exports change. **Each list carries the switch of the profile that governs it**, the active-set entry's for its provider, else the primary's, the rule opencode's whitelist already follows, so one profile's `enforce_models` means the same in both agents. The refusal names the model, the list and `"enforce_models": false`, as the bridge's does. **The registered ids are the ids pi's models.json row sends**, since the registration replaces that row's list and the refusal compares against it: on Kilo a bare `deepseek-…` id is spelled `deepseek/deepseek-…`, as the row and the selection spell it (`normalizeKiloModel`) | 2026-09-30 | [§14.2](#142-what-each-row-rests-on) | ✅ 2026-09-30: `yolo.derive("pi", "model-lists")` (`enforce` from `piEnforceFor`, `api` from the row) and `packs/pi/extensions/yolo-model-lists.js` (`listApi`, `delegateFor`); `TestPiModelListsExtensionRefusesAModelOutsideTheList`, `TestPiModelListsExtensionDelegatesAProviderPiDoesNotShipToItsAPIRegistry`, `TestPiModelListsExtensionSaysWhenAListSpanningTwoAPIsCannotBeRefused`, `TestPiModelListsExtensionReadsTheBedrockAPIFromPisCatalog`, `TestPiNarrowedListCarriesItsProfilesModelSwitch`, `TestPiNarrowedKiloListRegistersTheIdsItsRowSends`, `TestPiNarrowedOpenRouterListNamesItsRowsOneAPI` |
| <a id="MM-D22"></a>MM-D22 | *Implementation decision, building [MM-D9](#MM-D9) on the 2026-09-30 measurement ([§14.4](#144-build-order)).* **The launcher names codex's menu with `-c model_catalog_json=<file>` for the run it wrote the file for, never from `config.toml` through the selection, as MM-D9's letter says.** codex refuses to load a config whose `model_catalog_json` names no file, and a key the boot renders would name the file for every codex start: one the launcher never ran for, one whose menu could not be written (codex too old to print its catalog, a list none of whose ids it knows), and a `codex` resolved past the launcher. The flag is added only after the file exists, and `-c` is global and later wins, so a user's own `-c model_catalog_json` still does. **The step is a pack declaration, `model_menu` on a `program` (`packdecl.ModelMenu`, coined 2026-09-30), and the projection is Go, `yolo internal model-menu` (`internal/modelmenu`)**: the argv that prints the catalog, the list and menu paths, the flag, and the catalog's entry, id, order and name keys and the keys to clear and set are packs/codex's words, so both shared launcher templates carry one step keyed on no bin, and the shell only reads the flag words, NUL-separated. **The menu is rebuilt only when codex's binary, the list or the declaration changed** (a key file beside it), so a launch runs `codex debug models --bundled` once per change, not per start. The key file also records the listed ids the menu left out, so every launch that reuses the menu repeats [MM-D9](#MM-D9)'s warning for them, not only the one that rebuilt it (`TestRunSaysAtEveryLaunchWhichListedIDTheMenuLeavesOut`). **`YOLO_NO_LAUNCH_FLAGS=1` skips it**, since its words are a pack's flag. **At the host notch nothing is built yet, which leaves [NC-D1](../plans/notch-convergence.md#7-decision-ledger)'s convergence owed here.** Not for want of a place: `yolo host --` already rewrites codex's argv (the pack's launch flags, a managed launch's `--no-daemon`), and a subscription launch runs in a yolo-owned `CODEX_HOME` whose config yolo rewrites at every launch (`openaiauthhost.prepareCodexHome`), where a menu file could go. What is missing is the list: it is a surface, and a host render writes a surface for the configured selection, never a launch's `-p` ([OQ-HC3](host-computed-layer.md#OQ-HC3)), so a list `yolo host apply` wrote cannot tell a `yolo host -p codex` launch from a `-p openrouter` one, and a menu built from it would hand OpenAI's models to codex on another provider. Building it takes the list composed for the launch itself. Until then the list surface is `notAtHost` and host codex keeps its own menu | 2026-09-30 | [§14.2](#142-what-each-row-rests-on) | ✅ 2026-09-30: `packs/codex/pack.json` (`model_menu`, the `codex/model-list` surface), `yolo.derive("codex", "model-list")`, `modelMenuShellFn` in both launcher templates, `modelmenu.Run`; `TestCodexLauncherHandsCodexItsModelMenu`, `TestCodexLauncherAddsNoMenuWhereThereIsNone`, `TestTheNpmLauncherHandsItsProgramAModelMenuToo`, `TestYoloInternalModelMenuRunsTheMenuAgainstHome`, `TestRunReusesTheMenuUntilTheListOrTheProgramChanges`. The host half: designed and built 2026-09-30 ([§14.7](#147-codexs-menu-at-yolo-host-designed-2026-09-30), [§14.8](#148-what-was-built-at-the-host-2026-09-30), [MM-D24](#MM-D24) to [MM-D28](#MM-D28)); that design found this row's "list composed for the launch" right only where the launch's `-p` and the configured profile select the same provider, since a host `-p` does not choose codex's provider ([OQ-MM5](#OQ-MM5)), so the host builds a menu only there |
| <a id="MM-D23"></a>MM-D23 | *Implementation decision, building [MM-D6](#MM-D6)'s refusing form on `openai-codex` on the 2026-09-30 measurement of the subscription login ([§14.4](#144-build-order)).* **The wrapper goes in `yolo-openai-auth.js`'s own registration, the one that carries the login: with a list and `enforce` on, it adds a `streamSimple` that throws yolo's refusal for an id off the list and hands a listed one, options untouched, to pi's built-in `openai-codex` provider, else pi's api registry for `openai-codex-responses`.** The login needs no handling of yolo's: pi composes the registration's `oauth` as the provider's auth whatever else it carries, and turns the stored credential into the bearer token before it calls the wrapper, refreshing it through the registration's own `refreshToken` first when fewer than five minutes remain, and pi's own stream reads the account id out of that token (MEASURED). One registration states the list, the login and the refusal together, so the refusal can only compare against the list it registers. A second `openai-codex` registration from `yolo-model-lists.js` was the alternative, rejected because pi merges a re-registration's defined keys over the earlier one (`ModelRuntime.registerProvider`), so the refused list and the registered one would come from two files in whichever order pi loads them; [MM-D14](#MM-D14) kept the list in this registration for the login's sake already. **It refuses on the declared list whether or not an `only` narrowed it**, as claude's allowlist does on `openai-codex` ([MM-D1](#MM-D1)): the registration replaces pi's whole `openai-codex` catalog ([ML-D3](#ML-D3)), so the list is pi's exact menu for the provider either way, and every option of [OQ-MM3](#OQ-MM3) refuses there. **Its switch is `piEnforceFor`'s**, the rule [MM-D21](#MM-D21) reads for pi's other lists: the active-set entry's on `openai-codex`, else the primary profile's, else the default, on. So a launch that selects no profile refuses too, since pi registers the provider on every launch and can switch to it after a `/login`; `pi/codex-models` carries the switch as `enforce`, beside the list. **A stream that cannot be found registers the exact menu alone and warns once**, as [MM-D21](#MM-D21)'s does, rather than a wrapper with nowhere to hand a listed model. **The refusal's words are one text in both extensions**, copied rather than shared: pi loads every `.js` file in its extensions directory as an extension and reports one that exports no factory as a load error (`discoverExtensionsInDir`, `dist/core/extensions/loader.js`), so a shared module would be one more file delivered elsewhere for one sentence, and a test keeps the copies identical | 2026-09-30 | [§14.2](#142-what-each-row-rests-on) | ✅ 2026-09-30: `yolo.derive("pi", "codex-models")` (`enforce` from `piEnforceFor`) and `packs/pi/extensions/yolo-openai-auth.js` (`codexDelegate`, the registration's `streamSimple`); `TestPiOpenAIAuthExtensionRefusesAModelOutsideTheCodexList`, `TestPiOpenAIAuthExtensionRefusesWithNoProfileSelected`, `TestPiCodexListTakesTheSwitchOfTheActiveSetEntryOnOpenAICodex`, `TestPiOpenAIAuthExtensionRefusesNothingWithTheSwitchOff`, `TestPiOpenAIAuthExtensionRefusesNothingWithoutAList`, `TestPiOpenAIAuthExtensionDelegatesToPisAPIRegistryWithoutABuiltIn`, `TestPiOpenAIAuthExtensionSaysWhenItCannotRefuse`, `TestPisTwoRefusalsAreWordedAlike`; in a real `-p codex` launch, `TestCodexProfileRendersOneModelListForEveryAgent` |
| <a id="MM-D24"></a>MM-D24 | *Implementation decision, designing [MM-D22](#MM-D22)'s host half ([§14.7](#147-codexs-menu-at-yolo-host-designed-2026-09-30)).* **At `yolo host` the list is composed by the launch: `yolo host --` runs the program's pack's list derive, the one registered on the surface whose path its `model_menu.list` names (`codex/model-list`), in-process over the launch's own wire tables (`hostComposition.wireTables`, [FT-D2](agent-footer.md#FT-D2)), and hands the answer to the menu step. No file is rendered, and the surface stays `notAtHost`.** The derive and the wire readers are the ones a jail's boot runs ([HC-D13](host-computed-layer.md#HC-D13)), so the list keeps one declaration ([ML-D1](#ML-D1)) and one code path ([NC-D1](../plans/notch-convergence.md#7-decision-ledger)); the notch changes only where the tables come from. A file `yolo host apply` rendered was the alternative, and fails twice: it is written for the configured profile alone ([OQ-HC3](host-computed-layer.md#OQ-HC3)), and it can be older than the launch, since only a wrapped launch with `host_apply_on_launch` on renders first. The surface's `notAtHost` reason, which says `yolo host --` runs codex with no launcher, is reworded when this is built | 2026-09-30 | [§14.7](#147-codexs-menu-at-yolo-host-designed-2026-09-30) | ✅ 2026-09-30: `entrypoint.HostModelList`, called from `hostComposition.modelMenu`, and the `notAtHost` reason reworded; `TestHostModelListIsTheLaunchsListAndWritesNothing`, `TestHostModelListNamesADeclarationNoSurfaceWrites`, `TestHostCodexMenuFollowsTheLaunchsListAndLeadsThePacksFlags` |
| <a id="MM-D25"></a>MM-D25 | *Implementation decision, built around [OQ-MM5](#OQ-MM5) ([§14.7](#147-codexs-menu-at-yolo-host-designed-2026-09-30)).* **The host builds a menu only when the provider the launch selected for the program is the provider its configured profile selects: no `-p`, or a `-p` over the same provider as the program's `profile` entry.** Otherwise it adds no flag and prints one line saying that the `-p` does not choose codex's provider at the host, so codex keeps its own menu. Why: a host `-p` reaches codex's environment but not the config file that decides its provider, which only `yolo host apply` writes, for the configured profile (read at `6068e889`), so a list for a `-p` over another provider could hand OpenAI's models to codex on z.ai, the fault [MM-D22](#MM-D22) exists to prevent. When the two agree, every option of [OQ-MM5](#OQ-MM5) builds this very menu, so this much can be built before the ruling. A launch through a generated wrapper carries no `-p`, so it always agrees; a config file older than the configuration is the staleness `host_apply_on_launch` re-renders. The comparison is of providers, not profile names, because the list derive reads the provider | 2026-09-30 | [§14.7](#147-codexs-menu-at-yolo-host-designed-2026-09-30) | ✅ 2026-09-30: `hostComposition.menuProviderDisagreement` over `configuredProfile`, with the line [MM-D28](#MM-D28) words; `TestHostCodexWithAPOverAnotherProviderGetsNoMenu` |
| <a id="MM-D26"></a>MM-D26 | *Implementation decision ([§14.7](#147-codexs-menu-at-yolo-host-designed-2026-09-30)).* **The host step is the jail's: `internal/modelmenu`'s build, split so the list is an argument, which the jail's `yolo internal model-menu` reads from its file and `yolo host --` passes from [MM-D24](#MM-D24).** The catalog run, the projection, the cache key and the missing-id warning stay one implementation. `yolo host --` runs it after the binary resolves and the pack's flags are added, against the resolved target, so the catalog is the program that runs (the floor's copy where there is one). The flag goes right after `argv[0]`, ahead of the pack's flags and the user's own argv, where the jail's launcher puts it, so a user's own `-c model_catalog_json` still wins, `-c` being global and last-wins ([§14.4](#144-build-order)). The managed launch's `--no-daemon` rewrite still runs last, as today. `YOLO_NO_LAUNCH_FLAGS=1` skips it, as in a jail. It is disclosed with the pack's flags (`LaunchInjection.DisclosureLines`), because a launch has no quiet mode ([OQ-RO3](../reference/report-tiers.md#why-its-this-way)), and the missing-id warning prints at every launch whose menu lacks an id ([MM-D22](#MM-D22)). Nothing in it can refuse the launch: every failure is a warning, and codex keeps its own menu. `yolo host env`, which launches nothing, builds none | 2026-09-30 | [§14.7](#147-codexs-menu-at-yolo-host-designed-2026-09-30) | ✅ 2026-09-30: `modelmenu.Request`, which the jail's `modelmenu.Run` builds through too, `hostMenu.rewrite` and `LaunchInjection.Why`; `TestHostCodexOnTheConfiguredSubscriptionGetsYolosMenu`, `TestHostCodexGetsNoMenuWhereThereIsNone`, `TestHostManagedCodexHoldsTheMenusLockWhileItRuns` |
| <a id="MM-D27"></a>MM-D27 | *Implementation decision ([§14.7](#147-codexs-menu-at-yolo-host-designed-2026-09-30)).* **At the host the menu lives in yolo's machine state, never in the user's real `~/.codex`: one directory per pack and program, one file per cache key, and a lock each running program holds for its whole life.** codex reads the file again at every thread start (SOURCED, 0.159.2), and two host launches of one program may hold different lists at once, one with a company's `only` and one without, or one with no list at all. A fixed path, the jail's, would let one launch replace or remove the menu another codex still reads, and a codex whose menu is gone fails its next `/new`. So each menu is named by its cache key, the hash of the declaration, the list and the program's binary by which [MM-D22](#MM-D22) already decides to reuse a menu, and is never overwritten. Every launch that names a menu holds the directory's lock shared for its program's life, and one that writes a new menu first removes the others, only when it can take that lock exclusively, that is when no program holding a menu of the directory is running: collected by liveness, never by age, and a lock it cannot take removes nothing. That is the exclusive-then-shared pattern by which the managed `CODEX_HOME`'s live-launch lock already tells a launch it is alone (`sharedCallerToken`). The lock is held by the resident `yolo host` process where it stays (a managed launch, a launch with services), and on the exec path by the program itself: Go opens files close-on-exec, so the step clears that flag on the lock's descriptor before the exec. The directory is not the managed `CODEX_HOME`, because a launch without yolo's login has none, and core cannot know that `CODEX_HOME` is codex's. `yolo stores` lists it as yolo's. In a jail nothing changes: every launch of one jail reads the list its boot rendered, so one path serves them all | 2026-09-30 | [§14.7](#147-codexs-menu-at-yolo-host-designed-2026-09-30) | ✅ 2026-09-30: `modelmenu.Request.WriteIn`, `Held.KeepAcrossExec` and `paths.HostModelMenusDir`; `TestWriteInKeepsEveryMenuARunningProgramHolds`, `TestWriteInCollectsMenusOnlyWhenNoProgramHoldsOne`, `TestHeldKeepsItsLockAcrossTheExec`, `TestHostModelMenusRowIsYolosOwn` |
| <a id="MM-D28"></a>MM-D28 | *Implementation decision, building [MM-D24](#MM-D24) to [MM-D27](#MM-D27) ([§14.8](#148-what-was-built-at-the-host-2026-09-30)).* **The choices the host build made that the design left open.** (1) **[MM-D25](#MM-D25)'s line is printed whenever the two providers differ**, whether or not the `-p`'s provider has a list, since what it reports, that a host `-p` does not choose codex's provider, holds either way, and a launch has no quiet mode ([OQ-RO3](../reference/report-tiers.md#why-its-this-way)). The configured profile is the fold of the user-scope `profile` with no `-p`, over the launch's own selected packs (`hostProfileFold`), its first entry, as the launch's is. (2) **The step runs before the OpenAI prelaunch**, so it depends on nothing the managed `CODEX_HOME` holds, and its catalog run gets the environment the launch composed for codex (`childEnviron`), as the jail's runs in its launcher's; `codex debug models --bundled` reads no config and no credential ([§14.4](#144-build-order)). (3) **Its disclosure is a block of its own after the pack flags' block**, whose "you asked for" is the argv those flags left, as the managed launch's `--no-daemon` block already is, and whose last line says what the file is through a new optional `LaunchInjection.Why`, since a path in yolo's state says nothing on its own. (4) **The directory is `~/.local/share/yolo-jail/model-menus/<pack>/<bin>` (`paths.HostModelMenusDir`), 0700, and no jail mounts it in any mode**: a menu carries the prompt text codex runs its model with, so a copy a jail could write would choose an unconfined agent's instructions. `TestAssembleNeverMountsTheHostFloor` pins it beside the floor. (5) **One cache key for both notches** (`Request.Key`), over the parsed list rather than the list file's bytes, so the host's key names what its menu holds, and a jail rebuilds its menu once after the change. (6) **A reuse never collects, and the decision lock is held across the catalog run**, so two first launches of one program read its catalog once and the second reuses the menu. (7) **The step keys on the launched command's base name**, as the host composition's profile and prelaunch do, so `yolo host -- /path/to/codex` gets the menu too. (8) Every line the step prints at the host starts `yolo host: ` (`Request.Prefix`). The store's limit, stated rather than hidden: a program that closed the inherited descriptor would let a later writer remove a menu it still reads, and a long-lived child that keeps the descriptor postpones collection until it exits | 2026-09-30 | [§14.8](#148-what-was-built-at-the-host-2026-09-30) | ✅ 2026-09-30: `hostComposition.modelMenu`, `menuProviderDisagreement`, `hostMenu`, `modelmenu.Request.WriteIn`; `TestHostCodexWithAPOverAnotherProviderGetsNoMenu`, `TestHostCodexOnTheConfiguredSubscriptionGetsYolosMenu`, `TestHostManagedCodexHoldsTheMenusLockWhileItRuns`, `TestAssembleNeverMountsTheHostFloor`, `TestWriteInReusesAMenuAndRepeatsTheMissingIDWarning` |

---

## 17. Evidence

**Repo**: every code claim is in [§3](#3-what-exists-today), cited by symbol and measured at
`ee8154f2` (2026-09-24), except the `openai-codex` list, measured at `2a34a176` (2026-09-27), and
the review's corrections to it, [ML-D6](#ML-D6) to [ML-D8](#ML-D8), measured at `f3da48dc`. The
test that keeps every consumer on its declaration is
[`codex_model_list_test.go`](../../internal/entrypoint/codex_model_list_test.go), and its tier
in a real `-p codex` launch is
[`integration/codex_model_list_test.go`](../../integration/codex_model_list_test.go). The `needs` join rule is
[`wire-bridge.md`'s](../reference/wire-bridge.md#needs--a-conditional-pack-dependency).

**claude 2.1.282** (`~/.local/share/claude/versions/2.1.282`), 2026-09-24, MEASURED presence
only by `grep -c -a`: `modelPicker` (15), `replaceBuiltInOptions` (5), `enforceAvailableModels`
(9), `ANTHROPIC_CUSTOM_MODEL_OPTION` (12), `CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY` (6),
`ANTHROPIC_DEFAULT_FABLE_MODEL` (15). Documented at code.claude.com per the 2026-09-24 research
pass. None was exercised, and what discovery does under
`CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` was unread then. Superseded by the 2026-09-29 read of
2.1.285 below.

**The 2026-09-29 build** ([§14.5](#145-what-was-built-2026-09-29)), read and never run: oh-omp
0.15.3's settings schema (`enabledModels`, `modelRoles`), its settings path
(`<agentDir>/config.yml`) and its startup's scoped-model choice, in the shipped
`@oh-labs/oh-omp-linux-x64` binary (fetched with `npm pack`, deleted afterwards); copilot
1.0.48's settings migration and settings schema in the installed `app.js`; pi 0.99.1's provider
display-name precedence (`composeModelProvider`) and its built-in `openai-codex` name, in the
installed package.

**The 2026-09-29 research** behind [§14](#14-how-each-agents-menu-is-set-researched-2026-09-29),
which carries each claim's citation beside it. Read, never run: claude 2.1.285
(`~/.local/share/claude/versions/2.1.285`, cited by binary offset); copilot 1.0.89 (the
`copilot.tgz` asset inside `@github/copilot-linux-x64`, fetched with `npm pack`, and
`@github/copilot-sdk` 1.0.15, built for it); codex 0.158.0 (the installed binary, and source at tag
`rust-v0.158.0`); opencode 1.18.32 (the installed binary, and source at tag `v1.18.32`); oh-omp
0.15.3 (source at tag `v0.15.3`, and the shipped binary). pi 0.87.1 and 0.99.1 were loaded as a
library through pi's own `ModelRuntime` and extension loader, under Node 24 with an isolated home
and `PI_OFFLINE=1`, and no session or request. Every fetched package was deleted afterwards.

**The 2026-09-30 measurements** behind [§14.6](#146-what-was-built-2026-09-30), each recorded in
[§14.4](#144-build-order) with its version. pi 0.99.1, installed from npm into a scratch directory
with `--ignore-scripts`, was loaded through its shipped bundle (`dist/bundle/index.js`) and its
own `createAgentSessionServices`, which loads extensions and creates no session, with an isolated
home and `PI_OFFLINE=1`. Its requests went to a Node HTTP server on 127.0.0.1 that recorded each
request's path and `Authorization` header and answered with an error, so no model ran and nothing
left the machine. copilot 1.0.89 was fetched with `npm pack` as `@github/copilot-linux-x64`; the
`copilot.tgz` its single-executable binary embeds was cut out at the gzip stream holding `app.js`
and unpacked, and `app.js`, the bundled `copilot-sdk`'s types, `schemas/api.schema.json` and the
native runtime's strings were read, none run. codex 0.159.2 was read in its source, the tag
`rust-v0.159.2` tarball from GitHub, and its npm `linux-x64` package's binary was searched for the
strings the source's catalog command and catalog loader print. Every fetched package was deleted
afterwards.

**The second 2026-09-30 pi measurement**, of the subscription login ([§14.4](#144-build-order),
[MM-D23](#MM-D23)): the `@earendil-works/pi-coding-agent` and `@earendil-works/pi-ai` 0.99.1
tarballs, fetched with `npm pack` and read, and the package installed from npm into a scratch
directory with `--ignore-scripts`, npm's cache there too. Its shipped bundle was loaded as a
library under Node 24 through `createAgentSessionServices`, with `HOME` and pi's agent directory
in that scratch tree and `PI_OFFLINE=1`; each request went through `ModelRuntime.streamSimple` to
a Node HTTP server on 127.0.0.1 that recorded each request's path, `Authorization` and
`chatgpt-account-id` headers, closed the websocket handshake and answered the SSE request with a
`400`. The login was an `auth.json` entry of the shape yolo's prelaunch writes, holding a
made-up token with an account-id claim, and the credential client the extension calls was a
shell stand-in on `PATH`. No session ran, no model was reached, and nothing left the machine.
Every fetched package was deleted afterwards.

**The host design** ([§14.7](#147-codexs-menu-at-yolo-host-designed-2026-09-30)), read and never
run: yolo's own `hostExec`, `composeHostVarsWith`, `composeHostInputs`,
`packload.InjectLaunchFlags` and `openaiauthhost`'s `prepare`, `prepareCodexHome`,
`writeManagedCodexConfig`, `sharedCallerToken` and `Launch.Run` at `6068e889`; and codex
0.159.2's source, the tag `rust-v0.159.2` tarball from GitHub, for where `model_catalog_json` is
read (`load_catalog_json` and its caller `load_config_with_layer_stack` in
`core/src/config/mod.rs`) and when the app server loads the config again
(`ConfigManager::load_with_overrides` and `current_cli_overrides` in
`app-server/src/config_manager.rs`, called at thread start from
`app-server/src/request_processors/thread_processor.rs`). The tarball was deleted afterwards.

**The host build** ([§14.8](#148-what-was-built-at-the-host-2026-09-30)), tested and never run
against codex: each test drove `yolo host -- codex` through `hostMain` with the exec replaced, over
a temporary home, the shipped codex pack and a shell stub named `codex` on `PATH` that printed a
catalog of codex 0.159.2's shape for `debug models --bundled` and nothing else. The lock checks
read the exec'd descriptor's close-on-exec flag from `/proc/self/fd`, so they are MEASURED on Linux
and skipped elsewhere.

**Claude Code's tier aliases**, SOURCED 2026-09-04: they resolve per provider, to older models on
Bedrock, and `ANTHROPIC_DEFAULT_*_MODEL` repoints them
([model configuration](https://code.claude.com/docs/en/model-config);
[Claude Code model configuration](https://support.claude.com/en/articles/11940350-claude-code-model-configuration)).
Read against 2.1.261, the version installed then.

**pi 0.87.1 and pi-subagents 0.35.1**, 2026-09-27, read and loaded, never run as a CLI: pi-ai's
`getBuiltinModels("openai-codex")` lists GPT-6 Sol, Astra and Luna at 272,000 tokens beside five
GPT-5.x ids; `findInitialModel` (`core/model-resolver.js`) takes the scoped list first and the
saved default second; `ModelSelectorComponent` starts on "scoped" only when the scoped list is
non-empty; pi-subagents' `globToRegExp` escapes brackets. The extension was loaded through pi's
own `loadExtensions` with an isolated home ([ML-D3](#ML-D3)).

**Model catalogs**, 2026-09-24: pi-ai 0.87.1's `dist/providers/data/amazon-bedrock.json` holds
165 entries, all `bedrock-converse-stream` (MEASURED count; 0.85.1 holds 121). opencode 1.18.32's
embedded models.dev has 166 Bedrock entries (MEASURED 2026-09-29 in the installed binary; the
research pass's 179 was never re-counted).
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
