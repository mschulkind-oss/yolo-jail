---
title: "Which model ids yolo ships, how an org shapes the list, and what each picker shows"
date: 2026-09-25
status: accepted
stage: DECIDED
next: "OQ-MM1, both halves of OQ-MM3, OQ-ML4 and OQ-ML5 are built (MM-D44, ML-D12); one known gap: on the first launch after upgrading, a stale own-login saved model with no tier word is not cleared (MM-D44). MM-D41's opt-in supplied-list warning is implemented and parent-gate checked without changing list rendering or enforcement. OQ-MM6 is built (MM-D35 to MM-D40); live Claude/Copilot/Pi/AWS turns remain unmeasured. A nested-jail check of `yolo host -p pi=` with a provider-only profile (§14.11) is still owed. No question in this doc is open"
tags: [providers, profiles, models, aliases, pickers, packs, bedrock, currency]
summary: "Where yolo must pick model ids itself (the 2026-09-25 ruling on OQ-BR3: only where an agent cannot just fall back to its own default), how those picks ship in a built-in pack that comes with yolo rather than in core, how a company pack adds to or narrows a model list, how each agent's picker renders the result, and how the list stays current without yolo keeping a catalog."
vantage:
  status-chip: true
---

# Which model ids yolo ships, how an org shapes the list, and what each picker shows

**Status:** 2026-09-25, and built in parts since. On 2026-09-27: the `openai-codex` list is
declared once, in [`packs/codex-auth/pack.toml`](../../packs/codex-auth/pack.toml), and every
consumer reads it ([ML-D1](#ML-D1) to [ML-D7](#ML-D7)), in a jail and, since 2026-09-28, at the
host notch too ([OQ-HC1](../reference/host-agent-environment.md#oq-hc1), which superseded [ML-D8](#ML-D8)). On
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
the measurement ruled out. codex's menu at `yolo host` is built too
([§14.7](#147-codexs-menu-at-yolo-host-designed-2026-09-30),
[§14.8](#148-what-was-built-at-the-host-2026-09-30)), and since 2026-10-05 a host `-p` moves
codex, opencode, pi and oh-omp onto its selection for that launch without writing their files,
codex's menu following it ([MM-D30](#MM-D30), deciding [OQ-MM5](#16-decision-ledger) on its leaning;
[§14.9](#149-a-host--p-moves-the-program-built-2026-10-05)). copilot's `providers.json` ([MM-D10](#MM-D10)) stopped on
2026-09-30, when its measurement found that the file's models join GitHub's own, and
[OQ-MM4](#16-decision-ledger) was ruled B on 2026-10-05: it is built, in a jail, for every provider whose list
names a model ([MM-D31](#MM-D31), [MM-D33](#MM-D33);
[§14.10](#1410-what-was-built-2026-10-05-no-bedrock-list-and-copilots-whole-list)). Also on
2026-10-05, [MM-D32](#MM-D32), amending [ML-D9](#ML-D9), withdrew the Bedrock list
`packs/bedrock` shipped: each agent starts on its own Bedrock default, and copilot, which has no
Bedrock catalog, on one cheap open-weight model ([MM-D34](#MM-D34)). On 2026-10-01: `yolo check`'s line saying a profile's
`enforce_models` off leaves opencode's menu unnarrowed ([MM-D5](#MM-D5)), read from a fact
opencode's pack declares ([MM-D29](#MM-D29)). MEASURED at
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
  in a built-in pack that comes with yolo, not in core ([OQ-BR3](#16-decision-ledger)). That also answers
  provider-switching's "does yolo ship the ids?" ([OQ-PSW3](#16-decision-ledger)).
- **Built, 2026-09-27:** the `openai-codex` list moved into data, in the provider's own pack.
  That is [OQ-ML1](#16-decision-ledger)'s option (b), which the question's 2026-09-29 ruling kept: each
  provider declares its own default in its own pack ([ML-D1](#ML-D1)). pi writes no model scope
  for it any more ([ML-D2](#ML-D2)).
- **Built, 2026-09-29, and withdrawn 2026-10-05:** the Bedrock list, declared once on the
  `bedrock` provider in its own pack, each entry naming its maker, and each agent's pick made by
  [OQ-ML2](#16-decision-ledger)'s rule over the entries that agent can call ([ML-D9](#ML-D9)).
  [MM-D32](#MM-D32) ruled that yolo ships none (*"this is another opinion I don't want to
  have"*): `packs/bedrock` declares no models, each agent starts on its own Bedrock default,
  copilot on `openai.gpt-oss-120b-1:0` ([MM-D34](#MM-D34)), and the same rule picks among a list a
  pack or the user supplies. Built the same day
  ([§14.10](#1410-what-was-built-2026-10-05-no-bedrock-list-and-copilots-whole-list)).
- **Built, 2026-09-29:** a pack can shape any provider's list with the `models` kind
  ([OQ-BR12](#16-decision-ledger)), a profile's `enforce_models` switch governs every refusal yolo installs
  ([MM-D5](#MM-D5)), and each agent renders a list an `only` narrowed as [§14.1](#141-the-table) says, except
  codex's catalog, copilot's whole list and pi's refusal
  ([§14.5](#145-what-was-built-2026-09-29)). Ruled 2026-10-15 ([OQ-MM3](#OQ-MM3)): where the
  agent has no catalog of its own for a provider, a list no `only` narrowed refuses nothing, and a
  typed id goes to the provider, `openai-codex` excepted; and claude's saved `model` is written
  once when the provider changes. claude's half of both is built ([MM-D44](#MM-D44)); pi's is
  not. What a list that only adds renders was ruled 2026-10-15 ([OQ-MM1](#OQ-MM1)): it sits
  beside the agent's catalog, which opencode, pi, oh-omp and, since 2026-10-15, claude's native
  profile do.
- **Built, 2026-09-30, each on a measurement [§14.4](#144-build-order) records:** pi's refusal,
  a `streamSimple` wrapper in its registration of a narrowed list ([MM-D21](#MM-D21),
  [MM-D6](#MM-D6)), and on `openai-codex` in the registration that carries the subscription
  login, whose path through the wrapper was measured on its own ([MM-D23](#MM-D23)); and
  codex's exact menu on `openai-codex`, a catalog its launcher writes from codex's own before
  the exec ([MM-D9](#MM-D9), [MM-D22](#MM-D22); [§14.6](#146-what-was-built-2026-09-30)).
- **Built, 2026-09-30, as designed the same day:** codex's menu at `yolo host`, from a list the
  launch composes for itself ([MM-D24](#MM-D24) to [MM-D28](#MM-D28);
  [§14.7](#147-codexs-menu-at-yolo-host-designed-2026-09-30),
  [§14.8](#148-what-was-built-at-the-host-2026-09-30)). The design found that a host `-p` did not
  choose codex's provider, which only the configured profile did there, so the menu was built only
  where the two agreed, and which should win was [OQ-MM5](#16-decision-ledger).
- **Built, 2026-10-05, deciding [OQ-MM5](#16-decision-ledger) on its leaning B:** a host `-p` moves a program
  whose provider lives in its own config file onto the `-p`'s selection for that launch, handed
  per process and never written, and codex's menu follows it ([MM-D30](#MM-D30), superseding
  [MM-D25](#MM-D25); [§14.9](#149-a-host--p-moves-the-program-built-2026-10-05)). Each program's
  pack declares how, in `launch_selection`: codex as `-c` overrides, opencode in
  `OPENCODE_CONFIG_CONTENT`, pi and oh-omp as their own flags, never in front of a subcommand
  either reads only as its first word.
- **Built, 2026-10-01:** `yolo check` warns where a profile's `enforce_models` off leaves
  opencode's menu unnarrowed ([MM-D5](#MM-D5)). opencode's pack declares that its menu narrows
  only by refusing, and which provider's whole list it narrows to with no `only`, so core names
  no agent ([MM-D29](#MM-D29)).
- **Ruled and built 2026-10-05:** where no pack supplies a Bedrock list, yolo fetches the
  region's list from Bedrock itself, on the host, and a launch that is left with no list and no
  agent default fails and says why ([OQ-MM6](#16-decision-ledger); built as [MM-D35](#MM-D35) to
  [MM-D40](#MM-D40)). claude on `-p bedrock-bridge` runs in its own Bedrock mode at the bridge,
  which signs and passes through Bedrock's own invoke routes, and translates a model the list
  declares another maker's, so the everything profile still reaches every model
  ([MM-D39](#MM-D39), [OQ-BR11](bedrock-plumbing.md#decision-ledger)). Not yet run against AWS: one claude
  turn on a Claude model, one on another maker's, and one copilot turn under `-p bedrock-bridge`
  are the human check.
- **Built, 2026-10-05, on [OQ-MM4](#16-decision-ledger)'s ruling B:** copilot's `providers.json`
  ([MM-D31](#MM-D31), which builds [MM-D10](#MM-D10)), stopped on 2026-09-30 by the measurement
  that the file's providers are additive to GitHub's own. In a jail a provider whose list names
  any model reaches copilot as that file, its whole list beside GitHub's models, a GitHub pick
  served by the signed-in account. The file carries its key as literal text, so it is an **agent
  file** *(coined in [MM-D33](#MM-D33))*, which only a jail writes; at `yolo host` copilot keeps
  one model. Where no pack or config gives the provider a list, the file's rows are the fetched
  list ([MM-D35](#MM-D35)), combined at the landing merge on 2026-10-06.
- **Ruled 2026-10-16, built the same day ([ML-D12](#ML-D12)):** a pi model row keeps every catalog fact it does not state,
  and loses one only when it writes it as `null` ([OQ-ML4](#OQ-ML4)); a model's facts may be
  written once for all its routes, with each route able to override any of them
  ([OQ-ML5](#OQ-ML5)). The short shared form is written only for a pi that reads it
  ([ML-D11](#ML-D11)).
- **Moved here on 2026-09-25**, with their ids unchanged: [OQ-BR3](#16-decision-ledger) and
  [OQ-BR12](#16-decision-ledger)–[OQ-BR15](#16-decision-ledger) from [`bedrock-plumbing.md`](bedrock-plumbing.md), and
  [OQ-PSW1](#16-decision-ledger) and [OQ-PSW3](#16-decision-ledger) from the retired `provider-switching.md`.
  Those two carried the old prefix `PS`, as `PS1` and `PS3`, until later that day, when that doc's series was
  renamed `OQ-PSW` because [`provisioner-sets.md`](provisioner-sets.md) also uses `OQ-PS`.

**Needs your ruling:** nothing. [OQ-ML4](#OQ-ML4) and [OQ-ML5](#OQ-ML5), added 2026-10-13 at a downstream pack consumer's request, were ruled 2026-10-16; [OQ-MM1](#OQ-MM1) and [OQ-MM3](#OQ-MM3) were ruled 2026-10-15; [OQ-MM4](#16-decision-ledger) ([MM-D31](#MM-D31)) [OQ-MM5](#16-decision-ledger) ([MM-D30](#MM-D30)) and [OQ-MM6](#16-decision-ledger) were ruled 2026-10-05. Decided
2026-09-30 as implementation choices, each reversible: [OQ-BR14](#16-decision-ledger) ([MM-D16](#MM-D16)) and [OQ-PSW1](#16-decision-ledger) ([MM-D17](#MM-D17)). Ruled 2026-09-29:
[OQ-ML1](#16-decision-ledger), [OQ-ML2](#16-decision-ledger) and [OQ-BR12](#16-decision-ledger). [OQ-BR13](#16-decision-ledger) was directed
the same day (set the model selection however each agent allows) and is researched in
[§14](#14-how-each-agents-menu-is-set-researched-2026-09-29). That research also settled two
questions without asking: [OQ-BR15](#16-decision-ledger) on evidence (the bridge serves no model list), and
[OQ-MM2](#16-decision-ledger) on a corrected premise (copilot's key already sits in a 0600 per-agent file).

- [OQ-ML1](#16-decision-ledger): the shape of the built-in picks pack. Ruled 2026-09-29: there is no separate
  picks pack. Each provider declares its own default in its own pack, and that default layers
  like all config.
- [OQ-ML2](#16-decision-ledger): which provider × agent cases get a yolo pick. Ruled 2026-09-29, narrower
  than the leaning: yolo picks a model only to make a session valid, and never steers a valid
  choice unless the config opts in.
- [OQ-BR12](#16-decision-ledger): a `models` contribution kind so a company pack can shape a list. Ruled
  2026-09-29: yes, with `add` and `only`, for any provider. The engineer's own config writes last.
- [OQ-BR13](#16-decision-ledger): how each derive renders the list into its agent's picker. Directed
  2026-09-29: however each agent allows; the mechanisms are
  [§14](#14-how-each-agents-menu-is-set-researched-2026-09-29)'s table.
- [OQ-MM1](#OQ-MM1): what a list that only adds does to an agent's own catalog. Ruled
  2026-10-15, A: it sits beside the catalog, and only an `only` makes a menu exact
  ([the ruling](#oq-mm1-setup)).
- [OQ-MM2](#16-decision-ledger): where copilot's model file gets its key, if it cannot read it from the
  environment. Settled 2026-09-29: in a second file beside the 0600 per-agent env file that
  already holds that key ([MM-D10](#MM-D10)).
- [OQ-MM3](#OQ-MM3): whether a list with no `only` refuses other models on a gateway that serves
  more than the list, and what keeps claude's start valid where nothing refuses. Ruled
  2026-10-15: it refuses nothing, and yolo writes claude's saved `model` once when the provider
  changes, choosing the previous model's equivalent ([the ruling](#oq-mm3-setup)).
- [OQ-MM4](#16-decision-ledger): whether copilot should show a whole list in the only mode that can, which
  also shows GitHub's own models. Ruled 2026-10-05: B, write the file on every provider; built
  ([MM-D31](#MM-D31)).
- [OQ-MM5](#16-decision-ledger): whether a `-p` at `yolo host` moves codex onto its provider for that
  launch, so codex's menu there can follow the `-p`. Ruled 2026-10-05, B ([MM-D30](#MM-D30)): for
  opencode, pi and oh-omp too, handed per process rather than through the `CODEX_HOME` the leaning
  named; the parity build had built it on the leaning the day before.
- [OQ-MM6](#16-decision-ledger): what yolo does when neither the agent nor a pack has a Bedrock list. Ruled
  2026-10-05: it fetches the region's list on the host, and fails the launch, saying why, only when
  that fetch fails too. Built the same day ([MM-D35](#MM-D35) to [MM-D40](#MM-D40)).
- [OQ-BR14](#16-decision-ledger): how the lists stay current. Decided 2026-09-30 ([MM-D16](#MM-D16)): the
  agents' own catalogs, plus a `yolo check` warning. Built 2026-09-30, reading the catalog files
  an agent's pack declares ([MM-D19](#MM-D19)); today pi's alone.
- [OQ-PSW1](#16-decision-ledger): claude's derive reads `balanced`/`fast`. Decided 2026-09-30
  ([MM-D17](#MM-D17)): yes, keeping `sonnet`/`haiku` as synonyms. Built 2026-09-30.
- [OQ-BR15](#16-decision-ledger): the bridge serves `GET /v1/models`. Settled 2026-09-29 on the
  measurement it waited on: never ([MM-D4](#MM-D4)).
- [OQ-ML4](#OQ-ML4): whether a pi model row keeps the catalog facts it does not state. Ruled
  2026-10-16: yes, merged field by field, and a fact goes only when a row writes it as `null`
  ([the ruling](#oq-ml4-ml5-setup)). Built 2026-10-16 ([ML-D12](#ML-D12)).
- [OQ-ML5](#OQ-ML5): whether a model's facts can be written once for all its routes. Ruled
  2026-10-16: yes, and each route can still override any of them, because providers differ for
  the same model ([the ruling](#oq-ml4-ml5-setup)). Built 2026-10-16 ([ML-D12](#ML-D12)).

**Out of scope, and where it lives instead.** Refusing an off-list model at the wire:
[`wire-bridge-gateway.md`](wire-bridge-gateway.md#decision-ledger), which reads this doc's effective
list. Which vendors each agent can call on Bedrock, and the per-agent filter table:
[`bedrock-plumbing.md`](bedrock-plumbing.md#decision-ledger). Clearing a model id when you stop
selecting a profile: [`OQ-PSW2`](../reference/providers.md#oq-psw2). Withholding a
credential so that a menu shrinks: [`providers.md`'s OQ-CN4](../reference/providers.md#oq-cn4).
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
- **vendor** *(coined in [`bedrock-plumbing.md`](bedrock-plumbing.md#decision-ledger))*: the model's
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
([OQ-BR11](bedrock-plumbing.md#decision-ledger)): the **native profile**, claude's own Bedrock mode,
Anthropic models only; and the **everything profile** *(coined in [`bedrock-plumbing.md`](bedrock-plumbing.md))*, which
points claude at the wire bridge so one session can switch between Anthropic and every other
Bedrock model. copilot goes through the bridge. One provider holds every family: that was
[OQ-BR9](bedrock-plumbing.md#decision-ledger), ruled and built 2026-09-29 ([ML-D9](#ML-D9)).

---

## 2. The ruling, and how it fits what was said before

The maintainer, 2026-09-25, on [OQ-BR3](#16-decision-ledger): *"in cases where we can't just fall to the
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
| the third question of agent-auth-modes.md (that doc deleted 2026-10-16): *"User-level config is source of truth in v1. No binary presets baked"* | **Survives in intent, narrowed in letter.** Embedded packs ship inside the yolo binary, so a pick is literally in it. What that ruling protected still holds: the user's config is the last writer ([§7.2](#72-composition-rules)), and a pick is declarative pack data, visible as a pack and replaceable, never a Go table. |

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
  [`packs/codex-auth/pack.toml`](../../packs/codex-auth/pack.toml) declares its ids with an
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
    [OQ-HC1](../reference/host-agent-environment.md#oq-hc1), superseding [ML-D8](#ML-D8);
  - opencode (since 2026-10-01): `model` and `small_model` `openai/<id>`, and the list as rows of
    opencode's own `openai` provider, which its built-in ChatGPT support puts on the subscription,
    each `[1m]` variant naming its base as the id opencode sends, with the `whitelist` that makes
    the menu exact while the profile's switch is on ([MM-D5](#MM-D5), [MM-D7](#MM-D7)). No row
    for `openai-codex` itself ([OQ-2](pi-codex-provider-shadowing.md#10-decision-ledger)). The
    helper is the fourth verbatim copy.

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
  profile or a `default` alias names an Anthropic one. ⚠ **Changed again 2026-10-05**
  ([MM-D32](#MM-D32)): the list is gone, and the provider is a bare name with a platform again.
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

| Provider × agent | If yolo writes nothing | Evidence | Pick? (under [OQ-ML2](#16-decision-ledger)'s ruling) |
| :--- | :--- | :--- | :--- |
| Bedrock runtime × codex | codex starts on its own default slug. Its catalog maps slugs to mantle's bare ids, and holds runtime `global.` ids for Terra and Luna only. An id it does not know runs on fallback metadata (D4) | INFERRED from codex 0.156.1 strings; not run ([bedrock-plumbing evidence](bedrock-plumbing.md#14-evidence-and-how-to-re-check-it)) | **yes**; built 2026-09-29, the first OpenAI entry ([ML-D9](#ML-D9)); **no** since 2026-10-05 ([MM-D32](#MM-D32)) |
| Bedrock runtime × pi | pi's catalog lists the bare `openai.gpt-5.6-sol` against a runtime base URL: the wrong-spelling 404 bedrock-plumbing's P1 names, shipped in a vendor catalog | pi-ai 0.87.1 source, read not run ([bedrock-plumbing §4](bedrock-plumbing.md#4-what-each-agent-can-actually-do)) | **yes**; built 2026-09-29, the list's first entry; **no** since 2026-10-05, the vendor's fault left to it ([MM-D32](#MM-D32)) |
| Bedrock runtime × opencode | its default for `amazon-bedrock` is unread | UNMEASURED | **yes until measured**, then drop if opencode's own default is callable; built 2026-09-29, the list's first entry; **no** since 2026-10-05 ([MM-D32](#MM-D32)) |
| claude everything profile | claude's built-in options are Anthropic names; it has no idea a Kimi or GPT id exists | [OQ-BR11](bedrock-plumbing.md#decision-ledger); claude 2.1.282 | **yes** |
| copilot through the bridge | `COPILOT_MODEL` unset: copilot has no Bedrock catalog | `packs/copilot/derive.lua` | **yes** (the first callable entry of the same list); since 2026-10-05, with no list, copilot's own one open-weight default ([MM-D34](#MM-D34)) |
| `openai-codex` × claude, pi, codex | already shipped as picks, and since 2026-09-27 in data: one declaration on the provider, in its own pack ([§3](#3-what-exists-today), [ML-D1](#ML-D1)) | [`packs/codex-auth/pack.toml`](../../packs/codex-auth/pack.toml) | **yes, already**, in data |
| claude native profile (`bedrock`) | Claude Code resolves its own tier words to Anthropic models, older ones on Bedrock | vendor docs, 2026-09-04 | **no** under the ruling: the family is right; built 2026-09-29 as no pick ([ML-D9](#ML-D9)) |
| first-party `anthropic` provider × claude | Claude Code's own current defaults | — | **no** |
| gateway packs (OpenRouter, Kilo) | the user curates, per [OQ-GP2](gateway-provider-packs.md#decision-ledger) | — | **no** |

⚠ **Since 2026-10-05 the Bedrock rows above read no.** [MM-D32](#MM-D32): *"we should just
leave it unfiltered, whatever defaults you get, and then allow packs to override that if
needed"*, and *"if their upstream is broken, that's fine. We're not fixing that."* yolo picks a
Bedrock model only for copilot, which has no catalog to default from and refuses to start
without one ([MM-D34](#MM-D34)), and among a list a pack or the user supplies, by the same rule
as before.

**The rule ([OQ-ML2](#16-decision-ledger)): yolo picks a model only to make a session valid**,
when the model the agent would start on is not one of the active provider's models (a
wrong-family id, or none). A valid non-default choice, the user's or the agent's own, is never
steered unless the config opts in. The pick is the agent's upstream default when it is one of
that provider's models, otherwise the provider's declared default, otherwise the first model it
lists. When one agent runs on several providers at once, the rule reads over their union: the
start model is valid when it belongs to any of them, and the pick is the first-listed
provider's default
([`active-provider-sets.md` §4.4](active-provider-sets.md#44-models-the-union-and-the-start-model)).

⚠ **The ruling costs one planned behavior.** The retired `provider-switching.md`
planned a `models` map on claude's native `bedrock` provider, and a done-condition that `-p
anthropic` and `-p bedrock` put the same tier word on the same model. Under the ruling neither
gets a pick, so `opus` on Bedrock means what Claude Code says it means there, which may be an
older model. Restoring both would mean shipping
Anthropic-on-Bedrock ids whose geographic prefixes differ per model and must each be read off
that model's AWS card ([§5.3](#53-the-prerequisite-verify-before-an-id-ships)).

---

## 5. The picks pack

⚠ **[OQ-ML1](#16-decision-ledger) ruled this pack out (2026-09-29): there is no separate picks
pack, and nothing is always on.** Each provider declares its own default in its own pack, the
way everything else is declared, and that default layers like all config: a company or user
pack, or user config, overrides it. One simple default per provider, which the user changes;
yolo does not chase the newest model. [§5.1](#51-its-shape) and [§5.2](#52-how-it-joins-a-launch) below are the shape that was proposed and
not adopted; [§5.3](#53-the-prerequisite-verify-before-an-id-ships), the verification prerequisite, still applies to any id a pack ships.

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
and its order is the order users see. This was [OQ-ML1](#16-decision-ledger)'s sub-choice; the
ruling's layering keeps its effect, since a pack's or the user's list overrides a provider's
own default.

Every pick is dated in the pack README: the date, and which agent catalog or AWS page it was
checked against. The staleness warning ([§9](#9-currency-the-agents-catalogs-plus-a-staleness-warning))
covers every pick.

### 5.3 The prerequisite: verify before an id ships

An unverified prefix is exactly the 404-on-unknown-model failure bedrock-plumbing's P1 describes.
So no Anthropic id enters the picks pack until its prefixes are read from AWS's own pages and
dated. This was [OQ-PSW3](#16-decision-ledger)'s blocker. It is now a build prerequisite, not a question.

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
That part is unchanged and needs no ids: under [OQ-ML2](#16-decision-ledger)'s ruling claude defaults on
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
| `frontier` | the most capable tier | nowhere; added 2026-09-28 by [OQ-XM2](../reference/providers.md#oq-xm2) |

A derive resolves one for the selected provider with `yolo.model_for(alias)`, which returns
`provider/id` and warns when a provider lacks one of the four
([OQ-XM1](../reference/providers.md#oq-xm1),
[XM-D1](../reference/providers.md#XM-D1)).

They are capability-shaped, not vendor-shaped, because a vendor tier name (`sonnet`, `terra`)
cannot survive a switch to another vendor. **It is a convention with a warning, not an enum.** A
provider missing one gets one launch warning naming which, and the launch proceeds. A provider
that also declares `sol` has four aliases, and that is not an error.

**claude is where it bit.** Its env derive read the vendor names `sonnet` and `haiku`
literally. Decided 2026-09-30 ([OQ-PSW1](#16-decision-ledger), [MM-D17](#MM-D17)) and built the same day:
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

A new contribution kind, `models` ([OQ-BR12](#16-decision-ledger)). It names a provider and carries an
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
  selection for that agent, as for any unreachable provider; the check in [§7.4](#74-the-empty-effective-list-check-decided-mechanism-2026-10-08)
  warns only for selected program packs that opt in and a supplied list with no callable rows.
  The exception is claude's native profile, where an empty list is today's shipped case: no model
  variables, and claude chooses. A pack's `models` entry with no `vendor` is refused at load,
  naming the entry. (A user's string-form entry has no vendor and is offered to every agent, as today.)
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

### 7.4 The empty-effective-list check: decided mechanism (2026-10-08)

[§7.2](#72-composition-rules)'s empty-list diagnostic needs no new owner ruling. [MM-D41](#MM-D41)
implements a `yolo check` warning for selected program packs that opt in when a reachable,
configured provider has a supplied list but no callable model. The check does not alter the list
or make a launch fail.

The production-caller tests exercise a callable primary provider followed by an empty provider,
duplicate profiles for one provider, and active profile selection. They also pin warning identity,
remedy and count for object aliases, user alias repointing, canonical-id fact precedence, a literal
profile model with `enforce_models` off, and Codex's native and `bedrock-bridge` routes. Callable
rows and allowed-maker controls remain silent; the blocked canonical maker warns. Deleting the
production report call or the composition-observer forwarding makes positive caller tests fail.

Codex's `model_list_check` rule restricts Bedrock to OpenAI without a `via` selector, matching its
unchanged derive on both paths. Claude's distinct `via: false` rule restricts only native Bedrock
to Anthropic and leaves its live via route unrestricted. Tests compare both rules with the
unchanged Claude and Codex derives, including unknown-maker, vendorless and empty-source rows.
The macos-user fixture exercises via recomposition. That recomposition does not mutate list
presence: serving adaptations affect service addresses, while provider model composition remains
independent. The test proves the report still runs after recomposition, not that a service changes
the list.

The evaluator follows the derives' alias precedence, including a user alias whose key is the
empty string: it excludes that key from the canonical-id pass but retains it in the sorted
secondary fact pass. Section-caller, evaluator precedence controls and unchanged Codex-render
tests pin the vendorless row and conflicting same-id facts. The 2026-10-08 parent whole-tree and
applicable integration gates passed, and independent source and documentation review is complete;
[OQ-MM1](#OQ-MM1) and [OQ-MM3](#OQ-MM3), ruled 2026-10-15, were built on 2026-10-16 (merge `560c83e5e`);
the nested and live checks the frontmatter's `next:` names are still owed, so the design stays DECIDED. The warning
does not imply an agent must fail: its own default or a literal profile model may still work.

**The missing fact is the maker filter, not another catalog.** `callableModels` in the claude,
codex, pi, opencode and copilot derives expands the composed alias map into distinct ids,
merges facts from the id-spelled alias first and other aliases in sorted order, and filters
only on a declared `vendor`. `claudeMakers` restricts native Bedrock to Anthropic; codex's
`codexBedrockMakers` restricts Bedrock to OpenAI on both native and via paths. pi, opencode and
copilot use no maker restriction; oh-omp's `ompNarrowedRun` uses the supplied ids without a
maker restriction. The existing `protocols`, `built_in_providers`, `exact_menu_refuses` and
`needs_model_list` declarations do not state these filters.

**One optional pack-wide fact, `model_list_check`, is sufficient.** It is a map keyed only by
non-fork program bins owned by that manifest, not a global agent registry. A missing bin leaves
an older/custom pack's behavior unchanged; an empty object for a bin opts it in without maker
restrictions. Its `rules` can restrict by platform, route and maker.

The current pack schema uses this root-level optional `model_list_check` field:

```jsonc
// At the claude manifest's root, beside contributes, NOT inside its program:
"model_list_check": {
  "claude": {
    "rules": [
      { "platform": "aws-bedrock", "via": false,
        "makers": ["anthropic"], "native_empty_ok": true }
    ]
  }
}
// At the codex manifest's root:
"model_list_check": {
  "codex": { "rules": [{ "platform": "aws-bedrock", "makers": ["openai"] }] }
}
// At each other owning manifest's root (opencode shown; likewise copilot, omp):
"model_list_check": { "opencode": {} }
// pi's adds `references`, the model-reference check of §7.5.
```

**Compatibility boundary.** The field must not be placed inside a program: the v0.12.1 reader
drops a whole program contribution with an unknown nested field. It ignores and discloses an
unknown pack-wide field without changing generic unknown-field policy. Pack validation binds the
map to locally owned, non-fork programs; delivery rewrites keep the base manifest's fact. The
existing release-probe identity checks show no additional program loss from this metadata: five
shipped programs are retained directly; Pi was already dropped because of its unchanged nested
`launch_selection.requires`. A fixture removes only that existing key to isolate this field and
retain all six programs. Production flags and generic compatibility policy are unchanged.

A rule's `platform` matches the provider's composed platform. Optional boolean `via` matches
whether `ViaURLFor` gives this program a live route, including a carrier; omitted covers both
paths. Omitted `makers` accepts every declared maker; a non-empty list accepts only its named
makers. A model row with no maker remains callable for every program. Rules with overlapping
selectors are refused, rather than choosing by order. `native_empty_ok` is allowed only with
`via: false`: it suppresses a supplied list that is empty **before** maker filtering on this
native path, Claude's shipped defaulting exception. A non-empty list filtered to zero is still
reported, including on Claude's native path; no list at all is silent everywhere.

**Composition and reporting.** A check-only presence callback distinguishes a supplied empty
list from no list without adding data to the provider sent to an agent. An explicit empty map,
user deletion of the last alias or an `only` intersection that leaves no models remains supplied;
a provider removed by `null` does not. Neither the callback nor this warning changes fetched-list
policy. `yolo check` evaluates selected programs against their reachable configured profiles and
active provider sets, deduplicates by program/provider, and reports the first governing profile.
It does not guess an unconfigured `-p`, inspect unrelated profiles, execute agent code, fetch a
catalog, hydrate credentials or probe a service. A missing or unreachable provider remains an
existing pairing/profile finding, not an empty-list warning.

**The report is configuration-only and file-free.** In `sectionPacks`, use the selected packs,
configured profiles and their governing active sets, the same notch-aware composition and
profile/via resolution as `protocolPairingGap`, and the pack-owned program identity. A pure
packload helper mirrors `callableModels`' row/fact precedence, then applies the declared rule;
render-parity tests hold the mirror and maker declaration to the unchanged derives. Check only
providers the program reaches here (`profileReach`), deduplicate by program/provider, and do
not diagnose an absent selection, a missing/disabled provider or an unusable protocol pairing
as an empty model list. No agent executable, catalog, credential hydration, model fetch or
service probe is needed.

One warning per empty program/provider names both, the governing profile and the declaring
pack. Its next step names `providers.<provider>.models` for a callable alias, or the pack's
`only` to widen when that made the list empty; it offers no invented provider/profile command.
It says the supplied list contributes no callable model, **not** that the agent has no default,
no menu at all or no possible selection. A literal profile model is not a list row and does
not fill the diagnostic's list. Neither `enforce_models` nor the warning changes rendering,
refusal, native-provider ownership or adapter policy.

### 7.5 Model references: a name nothing resolves, and a picked model outside the scope (built 2026-10-18)

A downstream user's handoff of 2026-10-18 found two silent failures, both met only when a child
agent was dispatched, in another process, with a message naming a model rather than the line that
produced it. A profile's `subagents` model, or a pack's `modelScope` pattern, could name a model no
registry row carries and still pass `yolo check`. And a model picked in pi's own picker persists by
design while the scope a child runs under is re-derived each boot, so with `strict: true` a child
inheriting a picked model from another provider was refused.

Two terms are coined for the fix, in
[`packdecl.ModelReferences`](../../internal/packdecl/modelreferences.go). A **model reference** is
one value in an agent's config that names a model a later process looks up: a qualified
`<provider>/<id>`, or a pattern over those in which `*` matches any run of characters, ignoring
case. An agent's **model registry** is what it can resolve one against: every id the composed
provider table lists, under the agent's own id for the provider; every id the agent's own model
files hold as its derive renders them (pi's `models.json` rows, the `openai-codex` list with its
`[1m]` variants, the lists an `only` narrows); and the ids its installed catalog names for a
provider it runs itself (`built_in_providers`, `model_catalog`). Core knows no agent, so the
agent's pack says where its references and its model files are, in
`model_list_check.<bin>.references`; pi's names pi-subagents' block, its scope array, pi's picker
pair and its three model files:

```jsonc
"references": {
  "surface": "pi/settings",
  "models": ["/subagents/defaultModel", "/subagents/agentOverrides/*/model"],
  "patterns": ["/subagents/modelScope/allow", "/subagents/modelScope/agents/*/allow"],
  "scope": "/subagents/modelScope/allow",
  "selection": {"provider": "/defaultProvider", "model": "/defaultModel"},
  "profile_subagents": true,
  "registry": [
    {"surface": "pi/models", "ids": "/providers/*/models/*/id"},
    {"surface": "pi/codex-models", "ids": "/models/*/id", "provider": "openai-codex"},
    {"surface": "pi/model-lists", "ids": "/*/models/*/id"}
  ]
}
```

**`yolo check`** ([MM-D45](#MM-D45)) resolves the governing profile's `subagents` values the way
pi's derive does. On a provider with a list, pi resolves a value in this order: alias, exact model
id, then the list's `default` alias as fallback; a value resolving nowhere is a refusal when that
list has no `default`. `yolo check` uses that same result: an off-list value is a FAIL where the
derive refuses it and a warning where the derive falls back to `default`, naming the model the
children get. On a provider with no list, the value passes through as written, so it is resolved
against the registry. Every value a selected pack writes at those pointers (the owner's own layers,
a `config-overlay`, a `config-list`) is resolved against the registry too, the model files composed
by pi's own derives over the launch's tables, writing nothing. Each finding names the declaration
and the file and line it is in, the nearest ids, and the next step. A reference whose provider runs
on a catalog the check cannot read is a skip, not a miss, and a value with neither a `/` nor a `*`
(pi-subagents' `inherit`) is not judged.

**The boot** ([MM-D45](#MM-D45), [MM-D46](#MM-D46)) reads the same pointers back out of the file it
rendered, once per boot, because a jail cannot run the host render: a reference that matches nothing
is one line, pointing at `yolo check` for the declaration, or at the issue tracker where no
declaration set it; and when the saved picker pair is outside
the scope array, one line names the model, the scope, the profile that derives it and the two fixes.
The scope is not widened to admit the pick: [OQ-XM3](../reference/providers.md#oq-xm3) rules that a
child "shouldn't be able to cross providers", and [OQ-MM3](#OQ-MM3) keeps a refusal under an `only`.

Known limits. A catalog read is not split by provider, so an id any catalog names counts for every
provider the agent runs itself, as [MM-D16](#MM-D16) already counts it. Nothing here refuses a
launch: the one FAIL predicts a refusal the derive makes. **Review repair:** the derive now tries
an exact listed id before the list's `default` fallback, matching the alias → exact-id → fallback
order; a regression test pins both the profile selection and subagent model. The host check and boot
tests also cover the shipped profile outputs, including model ids only present in pi's rendered
files.

---

## 8. Rendering the effective list into each picker

Each derive writes the effective list into its own agent's picker ([OQ-BR13](#16-decision-ledger)). The
table below is the summary; each agent's mechanism, what it refuses and the evidence are
[§14](#14-how-each-agents-menu-is-set-researched-2026-09-29), researched 2026-09-29.

| Agent | Picker surface | Notes |
| :--- | :--- | :--- |
| claude | `modelPicker.options`, `availableModels` | `availableModels` puts the resolved default first, then the rest in list order; `modelPicker` uses list order. claude's Default row always stays ([§14.2](#142-what-each-row-rests-on)) |
| pi | an extension's registration of exactly the list; `models.json` rows for a list that adds | built for `openai-codex` ([ML-D2](#ML-D2), [ML-D3](#ML-D3)) and generalized by [MM-D6](#MM-D6). `enabledModels` alone is a soft shortlist, never a boundary ([`providers.md`'s credential gate](../reference/providers.md#pi-enabledmodels-is-a-shortlist)) |
| opencode | its provider `whitelist` | also refuses every other model ([MM-D7](#MM-D7)) |
| oh-omp | `enabledModels` in `config.yml`; `models.yml` rows | the scope fails open ([MM-D8](#MM-D8)) |
| codex | `model_catalog_json` | full entries copied from codex's own catalog at prelaunch ([MM-D9](#MM-D9)), built 2026-09-30 for `openai-codex` ([MM-D22](#MM-D22)), at `yolo host` too, following a `-p` there since 2026-10-05 ([MM-D30](#MM-D30)); on every other provider, the selection only |
| copilot | one `COPILOT_MODEL`; in a jail, `providers.json` for a provider with a list | the whole list beside GitHub's models, built 2026-10-05 ([MM-D31](#MM-D31), [MM-D33](#MM-D33)); at `yolo host` one model |

Rules:

- **Written once per launch** at the boot render, as managed layers, like every derive output.
- **An interactive `/model` choice still wins** until yolo's own selection changes (the
  `selection` namespace, unchanged).
- **claude's built-in options are replaced on every routed provider and under an `only`**
  ([MM-D1](#MM-D1)), which revises the 2026-09-25 plan of replacing them only on the everything
  profile: on a routed provider they only restate the tier pins ([MM-D2](#MM-D2)) under Anthropic
  labels. On the native profile a list that only adds appends its rows beside the built-ins
  ([OQ-MM1](#OQ-MM1), ruled 2026-10-15).
- **A refusal is set only where an `only` narrowed the list, or on `openai-codex`**, and only
  while the profile's enforcement switch is on ([MM-D5](#MM-D5)). A list that merely adds beside
  an agent's own catalog must never lock a user out of that catalog. `openai-codex` is a whole
  universe whose service serves nothing else, so it keeps its enforcement. On a gateway that
  serves more than the list, such as z.ai, OpenRouter or Kilo, a list with no `only` refuses
  nothing: a typed id goes to the provider, which serves it or errors ([OQ-MM3](#OQ-MM3), ruled
  2026-10-15, narrowing [OQ-WG3](wire-bridge-gateway.md#decision-ledger)'s default-on enforcement there).
  Not yet built for pi's routing-declared lists.
- **The GPT-6 lists moved into data** (BUILT 2026-09-27, [ML-D1](#ML-D1)): into the provider's
  own pack rather than the picks pack of [§5.1](#51-its-shape). claude's rendered `openai-codex`
  output stayed byte-identical, and a test fails when any consumer's read of the declaration is
  replaced by a literal. Two outputs changed on purpose: pi writes no `enabledModels` for it
  ([ML-D2](#ML-D2)), and pi-subagents' `modelScope.allow` is the exact declared ids, not a
  wildcard ([ML-D5](#ML-D5)).

**The model must be in the menu.** DIR-BR2's *"I want to use Kimi in Claude through Bedrock by
just choosing it in the menu"* ([bedrock-plumbing ledger](bedrock-plumbing.md#decision-ledger))
needs three pieces at once: the everything profile ([OQ-BR11](bedrock-plumbing.md#decision-ledger)); a
list that contains Kimi ([OQ-BR12](#16-decision-ledger), [OQ-BR14](#16-decision-ledger), or a pick under
[OQ-ML2](#16-decision-ledger)); and the claude derive rendering it into `modelPicker` ([OQ-BR13](#16-decision-ledger)).
Switching from an OpenAI model to Claude in one session is the everything profile's whole
purpose.

---

## 9. Currency: the agents' catalogs, plus a staleness warning

With no pack or user narrowing, an agent with a vendor catalog shows its own: pi-ai's, and
opencode's embedded models.dev. That is current with the agent's version, and yolo adds nothing
to it ([OQ-BR14](#16-decision-ledger)).

A list yolo *does* carry, the picks included, is checked by `yolo check`:

- **Only `yolo check` runs it, never a launch.** No model-list network call is made at launch.
  Runtime has no list endpoint anyway, so the only source would be the Bedrock control plane,
  which the `aws-auth` example policy does not grant. ⚠ **Amended for Bedrock on 2026-10-05**
  ([OQ-MM6](#16-decision-ledger)): where no pack supplies a Bedrock list, the `aws-auth` daemon on the host
  reads the region's list from that control plane, with the SSO role's credentials before they
  are narrowed, and caches it once a day per account and region. Built 2026-10-05: the launch
  asks for it while it composes the provider table, and waits at most five seconds
  ([MM-D37](#MM-D37)). A jail's credential is never used for the fetch, so it needs no list
  permission and gains none. When no pack supplies a list, the agent has no default of its
  own, and the fetch fails, the launch stops, saying why and what to do ([MM-D36](#MM-D36)).
  MEASURED 2026-10-05 by the maintainer on his own host: his SSO role may call
  `ListInferenceProfiles` in `us-east-1`. Not yet watched: a claude turn on a Claude model and
  on another maker's, and one copilot turn, under `-p bedrock-bridge`.
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
the list with no settings write ([OQ-BR15](#16-decision-ledger)). Today the bridge answers only
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
- a network call for models at launch, but for [OQ-MM6](#16-decision-ledger)'s host-side Bedrock fetch,
  made by the `aws-auth` daemon and cached once a day, where no pack supplies a list;
- a refusal, `enforceAvailableModels` included, on a list that only adds beside an agent's own
  catalog ([MM-D1](#MM-D1), [MM-D5](#MM-D5)), or on a gateway's list that no `only` narrowed
  ([OQ-MM3](#OQ-MM3));
- a pick for a case the agent can default on;
- a pick with no date and no source in the pack README;
- an Anthropic-on-Bedrock id whose geographic prefix is unverified.

The old list's third line, *"the bridge dialing anything but its boot-selected upstream"*, is
dropped here. [OQ-BR11](bedrock-plumbing.md#decision-ledger) amended that rule for the everything
profile, and what the bridge may dial is now
[`wire-bridge-gateway.md`](wire-bridge-gateway.md)'s question.

**Non-goals:**

- **No catalog.** yolo tracks no provider's full model list, region matrix or pricing
  ([§2](#2-the-ruling-and-how-it-fits-what-was-said-before)).
- **No closed alias vocabulary.** `default`/`fast`/`balanced`/`frontier` warn; they never refuse.
- **No enforcement at the wire.** Refusing an off-list model in the request path is
  [`wire-bridge-gateway.md`](wire-bridge-gateway.md#decision-ledger)'s. The refusals an agent can make
  itself, listed in [§14.1](#141-the-table), follow that doc's switch ([MM-D5](#MM-D5)).

Rejected or leaning against, each argued in its question: fetching Bedrock's list at launch
and shipping a dated catalog snapshot ([OQ-BR14](#16-decision-ledger) options B and C), and picks scattered
through each provider's own pack ([OQ-ML1](#16-decision-ledger) option (b)). The first came back in part on
2026-10-05: once [MM-D32](#MM-D32) shipped no Bedrock list, [OQ-MM6](#16-decision-ledger) had yolo fetch the
region's list on the host wherever no pack supplies one.

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

1. **The `models` kind** with `alias` and `description` ([OQ-BR12](#16-decision-ledger),
   [§7.3](#73-two-fields-the-entry-shape-is-missing)), then **picker rendering**, moving the
   GPT-6 lists into data under the byte-identical test ([OQ-BR13](#16-decision-ledger)). The move itself
   is BUILT (2026-09-27), into the provider's own pack rather than a `models` kind
   ([ML-D1](#ML-D1)); a `models` kind would take over that declaration. Picker rendering per
   agent has its own order, [§14.4](#144-build-order), whose first step needs no `models` kind.
2. **The picks pack** ([OQ-ML1](#16-decision-ledger)), with the cases [OQ-ML2](#16-decision-ledger) rules, each id dated;
   Anthropic ids only after the prefix check ([§5.3](#53-the-prerequisite-verify-before-an-id-ships)).
   provider-switching's three-line `models` map for claude's native `bedrock` provider lands here
   only if [OQ-ML2](#16-decision-ledger) gives that profile a pick.
3. **The alias vocabulary**: the default-only warning, the claude derive's synonym reading, and a
   line in `providers.md` naming the three ([OQ-PSW1](#16-decision-ledger)). The synonym reading and the
   `providers.md` line were built 2026-09-30 ([MM-D17](#MM-D17)).
4. **First-party providers** for claude, with no picks under [OQ-ML2](#16-decision-ledger)'s ruling
   ([§5.4](#54-first-party-providers-are-a-use-of-this)).
5. **The `yolo check` staleness warning** ([OQ-BR14](#16-decision-ledger)). Built 2026-09-30
   ([MM-D16](#MM-D16), [§9](#9-currency-the-agents-catalogs-plus-a-staleness-warning)).
6. ~~A bridge `GET /v1/models`~~: dropped 2026-09-29, never to be built ([MM-D4](#MM-D4)).

**Done when:**

1. A company pack with `only` over five ids makes exactly those five appear in claude's, pi's,
   opencode's and codex's pickers, each filtered to what that agent can call: in the pack's
   order except opencode's, which sorts by release date, and below claude's Default row. oh-omp
   shows the same five only on a provider it has a list for: the via row under yolo's `bedrock`
   key, or a key omp does not ship. omp's native Bedrock client gets no list
   ([MM-D8](#MM-D8)).
   copilot's menu in a jail is the same five, beside GitHub's own models for a copilot signed in
   to GitHub ([MM-D31](#MM-D31), the ruling of [OQ-MM4](#16-decision-ledger)), and its first entry alone at
   `yolo host`. codex's picker is exact on `openai-codex` alone, since its
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
6. *(Only if [OQ-ML2](#16-decision-ledger) gives claude's native profile a pick.)* On claude, `-p anthropic`
   and `-p bedrock` put the same tier word on the right id, with no hand-editing.

---

## 14. How each agent's menu is set (researched 2026-09-29)

This section answers [OQ-BR13](#16-decision-ledger)'s direction, *"set the model selection however we
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
  The bridge's is [OQ-WG3](wire-bridge-gateway.md#decision-ledger)'s: one list, and an enforcement switch
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
contribution narrowed the list; a list that only adds renders beside an agent's own catalog
([OQ-MM1](#OQ-MM1), ruled 2026-10-15).

| Agent, path | Shows exactly the list by | Other models refused by | Build step |
| :--- | :--- | :--- | :--- |
| **claude, native profile** (its own Bedrock client) | Never quite exactly. `modelPicker` with `replaceBuiltInOptions: true` shows claude's Default row, which cannot be removed, then the list, plus the session's current model when that is off the list (MEASURED). With `availableModels` and `enforceAvailableModels`, Default keeps the tier default when that tier's model is listed, and otherwise takes the first listed entry shaped like an Anthropic id (MEASURED, [§14.2](#142-what-each-row-rests-on)) | claude's own `availableModels`, client side, with four gaps ([§14.2](#142-what-each-row-rests-on)). The bridge never sees this path | Under an `only`, the three keys; every tier pinned to a list id beside `CLAUDE_CODE_USE_BEDROCK` ([MM-D1](#MM-D1), [MM-D2](#MM-D2), [MM-D5](#MM-D5)). Under an `add`, the rows appended beside the built-ins with no allowlist, with the tier pins as before: the list's default or the profile's `model` pins Opus ([OQ-MM1](#OQ-MM1), built 2026-10-15; [MM-D2](#MM-D2)) |
| **claude, routed** (`ANTHROPIC_BASE_URL` at the bridge, or at a gateway's own Anthropic endpoint) | `modelPicker` with the built-ins always replaced, since the list is the whole universe: Default, then the list. Built today for `openai-codex` only | claude's `availableModels` where it renders: on `openai-codex` and under an `only`, never on a gateway's list no `only` narrowed ([OQ-MM3](#OQ-MM3)). On the bridge path, the bridge's allowlist, which also sees claude's background requests | Generalize the `openai-codex` branch of `yolo.derive("claude", "settings")` to every routed provider; pin every tier; stop steering the start model ([MM-D1](#MM-D1) to [MM-D3](#MM-D3)) |
| **copilot** (through the bridge on Bedrock and the other bridged providers; direct otherwise, [§14.2](#142-what-each-row-rests-on)) | Today one entry, `COPILOT_MODEL`, because copilot's environment-variable setup carries one model (SOURCED). A `providers.json` file, present in 1.0.89 and absent in 1.0.48, holds a provider and a row per model, so the whole list can show (SOURCED) | No copilot setting refuses a model configured this way (SOURCED). On a bridged provider the bridge's allowlist is the only refusal, and every copilot request passes it. On a direct provider nothing refuses | After four measurements, a `copilot/providers` surface, or a file beside copilot's env file if the key cannot come from the environment ([MM-D10](#MM-D10)). **Stopped 2026-09-30**: the file's models join GitHub's own ([§14.4](#144-build-order), [OQ-MM4](#16-decision-ledger)). **Built 2026-10-05** on [OQ-MM4](#16-decision-ledger)'s ruling B, as the file beside copilot's env file, in a jail only ([MM-D31](#MM-D31), [MM-D33](#MM-D33)) |
| **pi, a provider pi ships** (`amazon-bedrock`, `zai`, `cerebras`, `openrouter`, `openai-codex`) | An extension's `registerProvider(<id>, { models })` replaces that provider's list and keeps pi's own client, address and credential (MEASURED on 0.87.1 and 0.99.1). Built for `openai-codex` ([ML-D3](#ML-D3)). A `models.json` row cannot narrow: it adds beside pi's catalog | Every menu path is bound to the list, but `--model <provider>/<unlisted id>` still runs, with a warning (MEASURED). A `streamSimple` wrapper in the same registration refuses it in-process, and hands a listed model to pi's own stream with pi's own credential (MEASURED 2026-09-30 on 0.99.1's shipped bundle against a mock endpoint, [§14.4](#144-build-order)). The bridge only on a via route | Generalize `pi/codex-models` into one per-provider data file; register exactly under an `only`; add the wrapper under the switch ([MM-D6](#MM-D6)). **Built 2026-09-30** ([MM-D21](#MM-D21)), and on `openai-codex` in the list's own registration beside the subscription login, after a measurement of its own ([MM-D14](#MM-D14), [MM-D23](#MM-D23)) |
| **pi, a provider yolo defines** (a key pi does not ship: yolo's `bedrock` via row, `kilo`, `llamacpp`) | Already exact: a `models.json` row under a key pi does not ship is the whole list (MEASURED) | The same `--model` gap and the same wrapper, delegating to pi's api registry since pi ships no provider of that id (MEASURED 2026-09-30 on 0.99.1 for `kilo`); the bridge on a via route | Nothing for the menu; the wrapper as above, **built 2026-09-30** |
| **oh-omp** (0.15.3) | `enabledModels` in `~/.oh-omp/agent/config.yml`: the selector then shows only that scope, with no "all" view (SOURCED). It fails open when no pattern matches. omp's own Bedrock catalog stops at Claude 4.6, and `models.yml` cannot add a Converse model, so a current Bedrock list exists only as the via row under yolo's `bedrock` key, which is exact | `--model` refuses an id omp does not know, but accepts any known model outside the scope, and `modelRoles` can name one too (SOURCED). The bridge on the via row | A new `oh-omp/settings` surface writing `enabledModels`, with the start model through the selection ([MM-D8](#MM-D8)) |
| **opencode, native** (`amazon-bedrock`, `openai` for yolo's `openai-codex` since 2026-10-01, and yolo providers named like catalog ones: `openrouter`, `kilo`, `zai`, `cerebras`) | `provider.<id>.whitelist` set to the list's ids, each also declared under `provider.<id>.models` (SOURCED). The order stays opencode's: newest release date first | The whitelist itself: any other model fails as "Model not found". Hiding and refusing cannot be separated. The bridge on a via route | Write the whitelist; take the display name from the entry, never the alias ([MM-D7](#MM-D7)) |
| **codex** (0.158.0) | `model_catalog_json`, a file of full catalog entries applied at startup for every provider; `priority` sets the order and `display_name` the name (SOURCED). The entries must be copied from codex's own catalog: one with no instructions runs codex with empty system instructions | Nothing in codex: an off-catalog model runs on fallback metadata (SOURCED). The bridge on a via route, which `openai-codex` never has ([WG-I21](wire-bridge-gateway.md#WG-I21)) | A new prelaunch step that reads codex's bundled catalog and writes the filtered file; last ([MM-D9](#MM-D9)). **Built 2026-09-30** for `openai-codex`; the bundled catalog is OpenAI's whatever the provider, so Bedrock runtime gets none ([§14.4](#144-build-order), [MM-D22](#MM-D22)). At `yolo host`, **built 2026-09-30** ([§14.8](#148-what-was-built-at-the-host-2026-09-30)), and following a host `-p` since 2026-10-05, which moves codex onto its provider ([§14.9](#149-a-host--p-moves-the-program-built-2026-10-05), [MM-D30](#MM-D30)) |

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
>    last word, as [OQ-BR12](#16-decision-ledger) was ruled.
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
- **The claude env derive steers against [OQ-ML2](#16-decision-ledger)'s ruling today** (SOURCED + read).
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
  start there; ruled 2026-10-15, the saved `model` written once per provider change replaces it
  ([MM-D43](#MM-D43)). **Fixed 2026-09-29** where the allowlist renders — `openai-codex` and a list an
  `only` narrowed, while `enforce_models` is on: every tier is pinned to the list, and
  `ANTHROPIC_MODEL` only on `pin_model` ([§14.5](#145-what-was-built-2026-09-29)). **Fixed
  2026-10-15 everywhere else**: `ANTHROPIC_MODEL` only on `pin_model` on every provider, and the
  saved `model` written once per provider change in its place ([MM-D44](#MM-D44)).
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
  Where the key goes if it does not is [OQ-MM2](#16-decision-ledger), settled as [MM-D10](#MM-D10) says.
  Read again on 2026-09-30 ([§14.4](#144-build-order)): the shape 1.0.89 documents takes no
  variable, and `apiKeyCommand` is still unread.
- **GitHub's own models join the menu.** `app.js` merges these models into copilot's own list
  (`mergeByokModelList`), so a copilot also signed in to GitHub may show GitHub's models beside
  yolo's (INFERRED here; MEASURED 2026-09-30 on 1.0.89, [§14.4](#144-build-order): the file's
  providers are additive to GitHub's, and a GitHub model picked there is GitHub's to serve). That
  is [OQ-MM4](#16-decision-ledger).
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
> says the bridge is the only place a refusal can live, and [OQ-CN4](../reference/providers.md#oq-cn4)
> rests on pi's only lever on the "all" view being the credential. A registration narrows the "all"
> view, and a wrapper refuses. Neither changes what [OQ-CN4](../reference/providers.md#oq-cn4)
> ruled, since withholding stays the security property. A note is owed in both docs.

- **Narrowing does not reopen the shadowing trap** (MEASURED for the models-only form). A
  registration passing only `models` keeps the built-in provider's address, wire and credential,
  so it works however [pi-codex-provider-shadowing OQ-3](pi-codex-provider-shadowing.md#10-decision-ledger) is
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
> ([§3](#3-what-exists-today)). That is intended: a list that only adds sits beside the catalog
> ([OQ-MM1](#OQ-MM1), ruled 2026-10-15). *Display name* (INFERRED): the derive sets each model's `name` to its yolo
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
switch is on ([OQ-WG3](wire-bridge-gateway.md#decision-ledger)). It reaches copilot only on a bridged
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
   start valid, so it stays until [OQ-MM3](#OQ-MM3)'s replacement, a saved `model` written once per provider change ([MM-D43](#MM-D43)), is built. Also the display
   names in opencode's, pi's and oh-omp's rows ([MM-D7](#MM-D7)). Nothing new is needed but the
   opt-in's option. **Built 2026-09-29.**
2. **The `models` kind with `only`** ([§13](#13-build-order-and-what-done-looks-like) step 1),
   which every "under an `only`" cell waits on. **Built 2026-09-29.**
3. **Under an `only`, the menus:** claude's native `modelPicker`, pi's generalized registration
   (after the nested-jail check of the bundled catalog import), and oh-omp's `enabledModels`.
   **Built 2026-09-29**, all but the nested-jail check, which a real launch still owes.
4. **The refusal switch** ([MM-D5](#MM-D5)), and with it every refusal it governs: claude's
   allowlist beyond `openai-codex` (under an `only` alone, [OQ-MM3](#OQ-MM3)), opencode's whitelist,
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
   2026-09-30** on the second of them, recorded below, and filed as [OQ-MM4](#16-decision-ledger). **Built
   2026-10-05** on its ruling B ([MM-D31](#MM-D31), [MM-D33](#MM-D33);
   [§14.10](#1410-what-was-built-2026-10-05-no-bedrock-list-and-copilots-whole-list)).
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
    The item is stopped and the choice is [OQ-MM4](#16-decision-ledger). Whether the file's mode starts at
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
an agent's own catalog, ruled 2026-10-15) nor [OQ-MM3](#OQ-MM3) (whether a list no `only` narrowed refuses on a
gateway, and what keeps claude's start valid where nothing refuses, ruled 2026-10-15) decides. Where the code
meets one of those questions it keeps today's rendering, with a comment naming the question.
Each row's test drives the production call site: the boot render, `packload.AgentEnv`, or
`packload.ComposeProviders`.

| Piece | What it does now | Pinned by |
| :--- | :--- | :--- |
| The `models` kind ([OQ-BR12](#16-decision-ledger)) | `{"kind": "models", "provider": …, "add": […]}` or `"only": […]`, any provider, one verb per contribution. `packload.ComposeProviders` applies every `add` in pack order, then every `only`, which drops every entry it does not name, adjacent ones included (`TestAModelsOnlyDropsAdjacentEntries`), then the user's own `providers.<name>.models` per alias, and marks a narrowed entry `models_only` ([MM-D11](#MM-D11)). `yolo check` names a duplicate, an `only` id nothing added and a provider nothing declares. `description` joins a user's object-form entry ([§7.3](#73-two-fields-the-entry-shape-is-missing)) | `internal/packdecl/models_test.go`, `internal/packload/modellists_test.go`, `internal/cli/check/modellists_test.go` |
| The switch ([MM-D5](#MM-D5)) | `enforce_models`, a profile field that defaults on, reaches both derive paths as `ctx.enforce_models` ([MM-D13](#MM-D13)). It governs claude's allowlist (on `openai-codex` too) and opencode's whitelist. Since 2026-10-01 `yolo check` warns, per agent and provider, where it is off over a list an agent's menu narrows only by refusing ([MM-D29](#MM-D29)) | `internal/entrypoint/enforcemodels_test.go`; the check line, `internal/cli/check/unnarrowedmenu_test.go` and `TestTheUnnarrowedMenuLineAgreesWithOpencodesWhitelist` |
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
measurements first, and stopped on them ([OQ-MM4](#16-decision-ledger)), until the ruling of 2026-10-05
([MM-D31](#MM-D31)). pi's refusing `streamSimple`
wrapper ([MM-D6](#MM-D6)) waited on its credential path, since measured and built
([§14.6](#146-what-was-built-2026-09-30)). The bridge's allowlist
([OQ-WG3](wire-bridge-gateway.md#decision-ledger)) was the bridge's own build, since done (2026-09-30,
[`wire-bridge-gateway.md` §6.1](wire-bridge-gateway.md#61-how-the-allowlist-is-built)). The `yolo check` line saying
opencode's menu is not narrowed with the switch off ([MM-D5](#MM-D5)) was built on 2026-10-01
([MM-D29](#MM-D29)). The per-agent "empty effective list" line of [§7.2](#72-composition-rules) is now an opt-in
`yolo check` warning with the production-caller, compatibility and derive-parity evidence in
[§7.4](#74-the-empty-effective-list-check-decided-mechanism-2026-10-08). It preserves the existing
provider lists and launch policy. The parent whole-tree and applicable integration gates passed;
independent source and documentation review is complete. [OQ-MM1](#OQ-MM1) and [OQ-MM3](#OQ-MM3), ruled 2026-10-15, were
built on claude's native profile on 2026-10-16 (merge `560c83e5e`; their ledger rows name the
tests).
pi's native Bedrock selection (Bedrock step 2's, built beside this) still writes its
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
| pi's refusing wrapper ([MM-D6](#MM-D6), [MM-D21](#MM-D21)) | `yolo.derive("pi", "model-lists")` gives each narrowed list the switch of the profile that governs it, as `enforce`, and the api of the models.json row it writes, as `api`. With `enforce` on, `yolo-model-lists.js` registers the list with that api, or the one pi's catalog gives it, and a `streamSimple` that throws yolo's refusal for an id off the list and hands a listed one to pi's own stream: the built-in provider of that id when it serves the api, else pi's api registry. A list spanning two pi apis, or one whose stream the extension cannot find, registers the menu alone, keeping another extension's rows ([ML-D15](#ML-D15)), and warns once. With `enforce` off, `models` alone, as before | `TestPiModelListsExtensionRefusesAModelOutsideTheList`, `TestPiModelListsExtensionRegistersTheRenderedListAlone`, `TestPiModelListsExtensionDelegatesAProviderPiDoesNotShipToItsAPIRegistry`, `TestPiModelListsExtensionSaysWhenAListSpanningTwoAPIsCannotBeRefused`, `TestPiModelListsExtensionReadsTheBedrockAPIFromPisCatalog`, `TestPiNarrowedListCarriesItsProfilesModelSwitch`, `TestPiGetsANarrowedListToRegister` |
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
  ([OQ-HC3](../reference/host-agent-environment.md#oq-hc3); `composeHostInputs`). The launch's `-p` reaches
  codex's environment (the provider's variables, the three wire tables of
  [FT-D2](../reference/agent-footer.md#FT-D2)), and nothing in its argv or in the managed copy of its config.
  The prelaunch does not depend on it either, since no profile gates those two variables. So
  [MM-D22](#MM-D22)'s premise, that a list composed for the launch's `-p` follows the provider
  codex runs on, holds only when the `-p` and the configured profile name the same provider.
  Which one should win was [OQ-MM5](#16-decision-ledger), decided on 2026-10-05 for the `-p`
  ([MM-D30](#MM-D30), [§14.9](#149-a-host--p-moves-the-program-built-2026-10-05)).
- **codex reads the menu again at every thread start** (SOURCED, codex 0.159.2: the app server's
  `thread_processor.rs` loads the config through `ConfigManager::load_with_overrides`, which
  re-applies the launch's `-c` overrides, `current_cli_overrides`, and fails the thread on a
  config error). So a menu file must outlive codex's startup: one removed while codex runs fails
  its next `/new`. In a jail every launch reads the list the boot rendered, so all of them name
  one menu at one fixed path, replaced only when codex itself changes.

**The design**, each piece an implementation decision that holds whichever way
[OQ-MM5](#16-decision-ledger) is ruled:

1. **The list is composed by the launch** ([MM-D24](#MM-D24)): `yolo host --` runs the pack's
   own list derive over the launch's wire tables, the derive a jail's boot runs, and renders no
   file.
2. **It is built only when the launch and the configured profile agree** ([MM-D25](#MM-D25)):
   the provider the launch selected for the program is the one its configured profile selects.
   Otherwise the launch adds no menu and says why. That is the intersection of
   [OQ-MM5](#16-decision-ledger)'s options, so no launch hands codex another provider's models.
   **Superseded 2026-10-05** by [MM-D30](#MM-D30): a `-p` now moves codex onto its provider, and
   the menu follows it; the rule survives only for a program whose pack declares no launch
   selection.
3. **The step is the jail's** ([MM-D26](#MM-D26)): `internal/modelmenu`'s build, with the list
   as an argument, run by `yolo host --` against the program it is about to run. The flag goes
   right after `argv[0]`, as in a jail, and is disclosed with the pack's flags.
4. **The menu lives in yolo's state and goes only when nothing can read it**
   ([MM-D27](#MM-D27)): one file per cache key (the hash of the declaration, the list and
   codex's binary by which [MM-D22](#MM-D22) reuses a menu), under a lock each running program
   holds, so concurrent launches with different lists never overwrite or delete each other's
   menu.

A codex started outside yolo, from an IDE or a shell without yolo's wrappers, gets no menu at the
host: [OQ-HS3](host-notch-services.md#11-decision-ledger) ruled that an agent launched outside yolo lacks a
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
| The agreement ([MM-D25](#MM-D25), **superseded 2026-10-05** by [MM-D30](#MM-D30)) | Built 2026-09-30: with no `-p`, or a `-p` whose provider was the one the config's `profile` selects for codex, the menu was built; otherwise no flag and a line naming [OQ-MM5](#16-decision-ledger). Since 2026-10-05 the menu follows the provider the program runs on (`hostComposition.menuFollowsTheLaunch`), which a `-p` now moves codex onto ([§14.9](#149-a-host--p-moves-the-program-built-2026-10-05)); the old rule holds only for a program whose pack declares no launch selection | `TestHostCodexMovesOntoAPOverAnotherProviderAndGetsItsMenu`, `TestTheMenuOfAProgramWithNoLaunchSelectionFollowsOnlyItsFile`, `TestTheMenuStepBuildsNoMenuForAPTheProgramWasNotMovedOnto` |
| The step ([MM-D26](#MM-D26)) | `modelmenu.Request`, the build the jail's `yolo internal model-menu` now runs through too, against the resolved target, its catalog run given the environment the launch composed for codex. The flag goes right after `argv[0]`, ahead of the pack's launch flags, and is disclosed in their words, the last line saying what the file is (`LaunchInjection.Why`). The managed launch's `--no-daemon` still goes in last. `YOLO_NO_LAUNCH_FLAGS=1` skips the step, and in a jail it builds nothing, the jail's own launcher building codex's menu there | `TestHostCodexOnTheConfiguredSubscriptionGetsYolosMenu`, `TestHostCodexGetsNoMenuWhereThereIsNone`, `TestHostManagedCodexHoldsTheMenusLockWhileItRuns`, `TestHostCodexReadsItsCatalogInTheEnvironmentTheLaunchComposed`, `TestHostCodexInAJailLeavesTheMenuToTheJailsLauncher`, `internal/modelmenu/modelmenu_test.go` |
| The store ([MM-D27](#MM-D27)) | `modelmenu.Request.WriteIn` into `~/.local/share/yolo-jail/model-menus/<pack>/<bin>/` (`paths.HostModelMenusDir`), one `<key>.json` per cache key, under a shared lock on the directory's `.live.lock`: held by the resident `yolo host` on a managed launch, and on the exec path by codex, through a descriptor whose close-on-exec is cleared just before the exec (`Held.KeepAcrossExec`). A launch that writes a new menu removes the others only when it can take that lock exclusively. No jail mounts the directory, and `yolo stores` lists it as yolo's, self-bounded | the lock at the exec and on the resident path in `TestHostCodexOnTheConfiguredSubscriptionGetsYolosMenu` and `TestHostManagedCodexHoldsTheMenusLockWhileItRuns`; `TestWriteInKeepsEveryMenuARunningProgramHolds`, `TestWriteInCollectsMenusOnlyWhenNoProgramHoldsOne`, `TestWriteInReusesAMenuAndRepeatsTheMissingIDWarning`, `TestWriteInSerializesTwoFirstLaunchesOnTheDecisionLock`, `TestHeldKeepsItsLockAcrossTheExec`, `TestAssembleNeverMountsTheHostFloor`, `TestHostModelMenusRowIsYolosOwn` |

**Not built, and why.** A `-p` over another provider than the configured one got no menu, since
what it should do was [OQ-MM5](#16-decision-ledger)'s; that is built since 2026-10-05
([§14.9](#149-a-host--p-moves-the-program-built-2026-10-05)). copilot's `providers.json` is built
in a jail since 2026-10-05 ([MM-D31](#MM-D31)), and at the host copilot keeps one model
([MM-D33](#MM-D33)).

**What only a real run can confirm.** That codex 0.159.2 reads a menu at a path under yolo's state
as it reads the jail's, at startup and again at each thread start; and that it keeps the inherited
lock descriptor open for its life, which this build took as far as the descriptor codex is exec'd
with. A codex that closed it would let a later launch that writes another menu remove this one
while it runs, and its next `/new` would fail.

### 14.9 A host `-p` moves the program (built 2026-10-05)

[OQ-MM5](#16-decision-ledger) decided on its leaning B under the maintainer's 2026-10-04 delegation, on
[NC-D1](../plans/notch-convergence.md#7-decision-ledger) (*"host is supposed to act like everywhere
else"*), and open to the maintainer's revision; built as [MM-D30](#MM-D30) records, which
supersedes [MM-D25](#MM-D25). In a jail `yolo -p codex -- codex` starts codex on the subscription
with its menu, because the boot writes the `-p`'s selection into codex's file. At the host only
`yolo host apply` writes that file, for the configured profile alone
([OQ-HC3](../reference/host-agent-environment.md#oq-hc3)), so a `-p` reached a program's
environment and never the provider it ran on, for every program whose provider lives in its own
config file: codex (`model_provider`), opencode (`model`, `enabled_providers`), pi
(`defaultProvider`, `defaultModel`, `enabledModels`) and oh-omp (`enabledModels` under an `only`).

**What a launch does now.** It runs the program's own config-surface derive over its own wire
tables, the path [MM-D24](#MM-D24) runs the list on, and again over the configured profile's. When
a `-p` was typed and the two differ, it hands the program the `-p`'s selection for that process
alone, in the words its pack declares in `launch_selection` (`packdecl.LaunchSelection`, coined
for this): codex gets `-c model_provider="…"`, `-c model="…"` and its provider's
`model_providers` row as `-c` leaves, right after `codex`, the subscription's selection naming
`openai` for the provider a jail's render clears; opencode gets `OPENCODE_CONFIG_CONTENT`, one
document with its selection and the rows `enabled_providers` names, merged over a value the user
already set there; pi gets `--provider` (only beside an explicit `--model`, [§14.11](#1411-pis-provider-flag-travels-only-with-an-explicit-model-built-2026-10-07)), `--model` and `--models`, with the two model-list files
its extensions read handed whole in `YOLO_PI_OPENAI_CODEX_MODELS` and `YOLO_PI_MODEL_LISTS`, which
the extensions read before the files; and oh-omp gets `--models`. Nothing is written, so the
user's own files and the managed `CODEX_HOME`, rebuilt from the user's `~/.codex/config.toml`, keep
the configured selection; no `-p`, an agreeing `-p` and every host wrapper hand nothing. Nor does a
launch whose first word is a subcommand the pack names, since pi and oh-omp read one only there:
`yolo host -p zai -- pi update` runs `pi update` as typed and says so. A `-p` that composes no
selection at all, such as `-p zai` for codex, which cannot speak z.ai's chat completions, is said
on every launch, with the profiles that do move the program, since naming the `-p`'s profile in the
config would compose the same nothing. codex's menu then follows the provider the `-p` moved codex
onto.

| Piece | What it does now | Pinned by |
| :--- | :--- | :--- |
| The declaration | `launch_selection` on a `program` (`packdecl.LaunchSelection`): the surface, one of three forms (`each`, `flags`, `env`), the rows the selection names, defaults, further surfaces handed in variables, and the `subcommands` no argv word may go in front of; refused where no launch could hand it, and ignored by a reader older than it | `internal/packdecl/launchselection_test.go`; `packs/releasedecode_test.go` (the last release's reader decodes the shipped packs) |
| The composition | `entrypoint.HostLaunchSelection` runs the derive over a launch's tables and writes nothing; `LaunchSelection.Same`, `Argv` and `Vars` compare and spell it, refusing what the words cannot spell | `TestHostLaunchSelectionSpellsCodexsSelectionAsItsDashC`, `TestHostLaunchSelectionHandsOpencodeOneDocumentMergedOverTheUsersOwn`, `TestHostLaunchSelectionHandsPiItsFlagsAndItsLists`, `TestALaunchSelectionRefusesWhatItsWordsCannotSpell` |
| The step | `hostComposition.launchSelection`, called from `hostExec` before the menu: a `-p` that composes nothing said first, with the profiles that do move the program (`profilesThatMove`); the comparison with `configuredWireTables`; a typed subcommand left as typed; the argv rewrite after `argv[0]` disclosed in `LaunchInjection`'s words, the variables disclosed by name and what they carry, and a line naming the next step wherever it hands nothing for a `-p`; nothing in a jail | `TestHostCodexMovesOntoAPOverAnotherProviderAndGetsItsMenu`, `TestHostCodexMovesOntoAUserProviderWithItsRow`, `TestHostCodexOnAnAgreeingPGetsNoSelection`, `TestHostCodexLetsTheUsersOwnLaterDashCWin`, `TestHostCodexSaysWhenAPCannotMoveIt`, `TestHostManagedCodexGetsTheSelectionInItsArgv`, `TestHostRunsPiOnItsWholeSet`, `TestHostRunsOpencodeOnItsWholeSet`, `TestHostMovesOhOmpsScopeUnderAnOnly`, `TestHostLeavesASubcommandWhereTheUserTypedIt`, `TestHostCodexInAJailLeavesTheMenuToTheJailsLauncher` |
| The menu | `hostComposition.menuFollowsTheLaunch`, asked by `modelMenu` itself: the `-p`'s provider when the selection moved the program; [MM-D25](#MM-D25)'s rule only for a program whose pack declares no launch selection | `TestHostCodexMovesOntoAPOverAnotherProviderAndGetsItsMenu`, `TestTheMenuOfAProgramWithNoLaunchSelectionFollowsOnlyItsFile`, `TestTheMenuStepBuildsNoMenuForAPTheProgramWasNotMovedOnto` |
| pi's extensions | `yolo-openai-auth.js` and `yolo-model-lists.js` read the launch's variable before their file | `TestPiExtensionsReadTheLaunchsListsBeforeTheirFiles` |
| `yolo host env -p` | exports opencode's document; for a selection that needs argv it exports none of it and names `yolo host -p <profile> -- <agent>` | `TestHostEnvHandsTheSelectionAScriptCanCarry` |

**Not moved, and why.** A key a jail's deselect would clear and no declared default stands for
keeps the file's value: pi's and oh-omp's `enabledModels` when the `-p` composes no scope, and
opencode's `model` when the `-p`'s provider declares none. pi-subagents' `subagents` block, a
computed key of pi's settings, is [MM-D42](#MM-D42)'s gap: the explicit host launch profile must
govern profile-derived subagent defaults and model restrictions as well as the parent selection,
for this process and its children, without rewriting shared settings or disabling enforcement,
and the configured default still applies when no profile override is supplied.

**MM-D42's mechanism, built 2026-10-15; yolo ships no declaration of it (amended 2026-10-16).**
The root cause: a launch selection hands only the reserved `selection` namespace (scalars and
lists), and the `subagents` block is an object outside it, written into `~/.pi/agent/settings.json`
only by a jail's boot or by `yolo host apply` for the configured profile. So a `-p pi=codex` over a
configured OpenRouter profile moved the parent onto `openai-codex` while pi-subagents read the
OpenRouter `defaultModel` and the `openrouter/*` scope from the file; and two profiles on one
provider with different role pins compared equal, so the `-p` handed nothing at all. The mechanism
is a pack-wide declaration, `launch_computed` (`packdecl.LaunchComputedFlag`, a term coined for it:
a computed key of a launch selection's surface handed as argv for one launch;
[the reference](../reference/pack-system.md#launch_computed)), top-level so an older yolo ignores
it instead of skipping the program. A declaration hands the computed key as one word of compact
JSON with sorted keys, a tombstoned key as `null`, compared with the configured profile's apart
from the selection, so a `-p` that agrees on the parent but differs on that key hands the key alone
and leaves a model the user picked standing; no `-p`, an agreeing `-p`, a declared subcommand and
`YOLO_NO_LAUNCH_FLAGS=1` hand nothing. Nothing is written. **The shipped pi pack declares none.**
Upstream pi-subagents registers no flag for the policy, and pi 1.0.1 exits with `Unknown option`
over a `--flag` no loaded extension registers, so only a patched pi-subagents could read one, and
by the maintainer's ruling of 2026-10-16 yolo does not depend on such a fork
([MM-D42](#MM-D42)). The pi settings derive still composes the `subagents` block, which `yolo host
apply` and a jail's boot write into `settings.json` for upstream pi-subagents to read; a user whose
own pi-subagents reads a flag declares `launch_computed` for `pi` in the pack that forks pi, or in
one of their own that declares the `pi` program
([LC-D1](../reference/pack-system.md#LC-D1)).

A via row in pi's `models.json` also stays the configured provider's. A subcommand the pack names runs with none of the selection, argv or variables: pi
1.0.1's `auth`, `config`, `install`, `list`, `mcp`, `remove`, `uninstall` and `update`, and every
name and alias in oh-omp 0.15.3's command table, `launch` among them, plus `help` (read in the
installed dist and the shipped binary, never run). A codex started outside yolo moves on nothing, as it gets no menu
([OQ-HS3](host-notch-services.md#11-decision-ledger)).

**What only a real run can confirm.** That codex names the `-p`'s provider in `/status` and still
does after `/new` (it re-applies the launch's `-c` at every thread start, SOURCED at 0.159.2); that
pi 1.0.1 starts on `--provider` and `--model` without saving them as its default (INFERRED); that
opencode 1.18.34 takes `enabled_providers` and the `provider` rows from `OPENCODE_CONFIG_CONTENT`
as it takes `model` and `small_model` (MEASURED for those two only); and that oh-omp 0.15.3 takes
`--models` as its binary reads (read in the shipped bundle, never run).

### 14.10 What was built (2026-10-05): no Bedrock list, and copilot's whole list

The two rulings of 2026-10-05, built the same day.

**No Bedrock list ([MM-D32](#MM-D32)).** `packs/bedrock`'s provider declares no `models` and no
`model_options`, and no shipped pack contributes a `models` list to it. No derive changed what it
does: each already picks only from the list it is handed (`callableModel`), and an empty list
names no model, so codex, opencode and pi start on their own Bedrock catalogs' defaults, claude's
own Bedrock client on its own, and pi and opencode list their whole catalogs. A list a pack's
`models` contribution or the user's `providers.bedrock.models` supplies is picked from by the
rule [ML-D9](#ML-D9) built. Two consequences follow from the rulings rather than from a choice:
pi's catalog spells some ids bedrock-runtime refuses ([§4](#4-where-yolo-picks-the-cannot-default-cases)),
*"if their upstream is broken, that's fine"*; and under the everything profile a Claude model is
now translated like any other, since the wire bridge passes one untranslated only when a list
names it Anthropic's (`anthropicModelIDs`) and core never reads a maker out of an id. claude
under `bedrock-bridge` with no model named sends Claude Code's own default id, which names no
Bedrock model. copilot, which has no Bedrock catalog, starts on one model of its pack's
([MM-D34](#MM-D34)).

**copilot's whole list ([MM-D31](#MM-D31)).** copilot's env derive composes its `providers.json`
for every provider whose list names a model: one provider, the endpoint and key its environment
setup already sends, and one row per entry in list order, the wire id as both `id` and
`wireModel` and the entry's name and limits where the list states them. `COPILOT_MODEL` then names
the start model as the file's selection id, `<provider>/<id>`. The file carries the key as literal
text, so it is an **agent file** ([MM-D33](#MM-D33)): the jail's per-agent env writer puts it
beside `copilot.sh` and points `COPILOT_PROVIDERS_CONFIG` at it. At `yolo host`, which writes no
agent file, copilot keeps its environment's one model. Keeping a GitHub pick off GitHub, which the
ruling wants where cheap, has no cheap mechanism: the one copilot offers is its offline mode,
which also turns off its web tools, its GitHub MCP server and its updates
([OQ-MM4](#16-decision-ledger)'s option D), so nothing prevents it.

| Piece | Where | Pinned by |
| :--- | :--- | :--- |
| No list ships | `packs/bedrock/pack.json` | `TestTheShippedBedrockProviderDeclaresNoModelList` |
| No agent starts on a yolo-picked Bedrock model but copilot | every derive, rendered together | `TestNoAgentOnBedrockStartsOnAModelYoloPickedButCopilot`, which scans every file the boot writes and every variable each env derive composes, on `bedrock` and `bedrock-bridge`; each agent's `…OnBedrock…` test; at a real launch, `TestBedrockRendersEachAgentsOwnClient` |
| copilot's Bedrock default, and a profile's own model over it | `packs/copilot/derive.lua`'s `bedrockStartModel` | `TestCopilotReachesBedrockThroughTheBridgeOnEitherProfile`, `TestAProfilesModelReplacesCopilotsBedrockDefault`, `TestHostCopilotOnABedrockBridgeProfileRunsThroughALaunchOwnedBridge`, `TestPlainBedrockReachesRuntimeForCopilotAndOmp` |
| A supplied list reaches copilot's file whole | `packs/copilot/derive.lua`'s `providersFile` | `TestABedrockListAPackSuppliesReachesCopilotsProvidersFileWhole`, `TestCopilotByokComposesCerebrasThroughTheBridge`, `TestCopilotByokHandlesLocalProviderAndContext`; at a real launch, the copilot case of `TestProvidersRenderInTheAgentsOwnVocabulary`, which reads the file copilot's env file points at, its mode and its rows, inside the jail |
| Agent files | `packdecl` (`agent_files`), `packload` (`WithAgentFiles`, `AgentDelivery.Files`), `run.writeAgentEnvFiles` | `TestAgentFilesIsRefusedWhereNothingCouldWriteIt`, `TestAgentFilesLeaveTheEnvironmentAndReachTheDelivery`, `TestAnAgentFileLandsBesideItsEnvFileAndLeavesWithIt`, `TestHostCopilotOnASuppliedBedrockListKeepsItsEnvironmentsOneModel` |
| Claude translated with no list | `internal/wirebridged` | `TestWithNoListTheBridgeTranslatesAClaudeModelToo` |

**MEASURED 2026-10-05 22:28:30 UTC**, for [MM-D34](#MM-D34)'s id, by one signed request from a
jail in `us-east-1` with the SSO credential `aws-auth` serves (its container pointer, the jail's
only AWS credential), signed by yolo's own `internal/sigv4` for the service `bedrock` in a
throwaway test deleted after the run. `POST https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1/chat/completions`,
the route the bridge translates copilot's requests onto, with the fields its translation sends:
`model` `openai.gpt-oss-120b-1:0`, one user message *"Reply with the one word: pong"* and
`max_completion_tokens: 64`, not streamed. Answered **200** in 0.49 s: `model`
`openai.gpt-oss-120b-1:0`, `finish_reason` `stop`, usage 74 in and 47 out, and `content` holding
the model's reasoning inside `<reasoning>…</reasoning>` and then `pong`. No agent was started and
nothing was stored or created.

**What only a real run can confirm.** That copilot 1.0.92 reads the file at a path under
`~/.config/yolo-agent-env` and shows its rows beside GitHub's models; that `COPILOT_MODEL`
selects a row as `<provider>/<id>` (the SDK documents that spelling as the selection id; copilot's
own rule is in its native runtime, UNMEASURED); and that the file's mode starts with no GitHub
login at all. copilot was never run.

### 14.11 Pi's provider flag travels only with an explicit model (built 2026-10-07)

A host launch hands pi `--provider` only when the composed selection also has `defaultModel`. A
provider-only selection keeps its existing `--models` scope, leaving pi to resolve its own startup
model. Pi treats `--provider` as a qualifier for `--model`, not as a standalone provider default,
so a provider-only profile handed as `--provider deepseek --models deepseek/*` was rejected during
session setup. Provider-only handoff therefore no longer independently sets pi's provider; no
synthetic `--model` is added, and the native settings file is not edited by a host launch. This
closes only the transport question: it does not decide that a profile locks resumed or saved
sessions to its provider, and it restricts no calls (an active profile's call restriction is
[the provider-policy extension](simultaneous-auth-and-pack-isolation.md)'s).

**The flag-presence rule.** `yolo host -p` composes a pack's selection and, only when it differs
from the configured profile's selection, hands it through the pack's declared `launch_selection`
form ([§14.9](#149-a-host--p-moves-the-program-built-2026-10-05)). For the flags form, a
declaration may give a flag a `requires` list (`LaunchSelectionFlag.requires`): other selection
keys whose presence is required before that flag is emitted. The flag is emitted only when its own
selection key and every required selection key are present in the handoff (including declared
defaults); other flags are evaluated independently. An absent prerequisite suppresses that flag,
not the rest of the selection.

Pi declares:

- `defaultProvider` as `--provider {value}`, requiring `defaultModel`;
- `defaultModel` as `--model {value}`;
- `enabledModels` as `--models {value}`.

Therefore a provider-only selection such as `defaultProvider=deepseek` with
`enabledModels=["deepseek/**"]` hands only `--models deepseek/**`. When `defaultModel` is explicit,
pi still receives the provider-qualified pair `--provider <provider> --model <id>`. Keeping the
provider on that explicit path matters when providers expose the same model ID: the list scope does
not qualify pi's explicit `--model` lookup. The other declared list flags and variables are
unchanged.

This is a transport repair, not a provider lock or authorization boundary. It does not choose a
model, turn `--model "*"` into a wildcard, change the native `settings.json`, or promise that pi
will replace a saved in-scope selection or a resumed session. `--models` is a scope; pi's native
startup and session behavior remains in control. If a composed selection has no explicit model and
no usable scope, yolo does not fabricate one; the provider flag is omitted and pi retains its own
configuration behavior.

With no differing profile selection, with a profile that composes no selection, or when a declared
subcommand is first in the user's argv, the existing host launch-selection rules still hand no
injected pi flags. Explicit user arguments remain after the injected words and retain existing
precedence.

**Evidence and limits.** The production path is `internal/cli/host.go` → `hostmodelmenu.go` →
`entrypoint.HostLaunchSelection` → `LaunchSelection.Argv`, with flag grammar and validation in
`packdecl`. The pi consumer is declared in [`packs/pi/pack.toml`](../../packs/pi/pack.toml); the
derive continues to supply the existing provider, model (when explicit), and scope selection in
[`packs/pi/derive.lua`](../../packs/pi/derive.lua). The source audit read the installed
`@earendil-works/pi-coding-agent` **1.0.4** source and bundle, whose runtime build reported
`forkCommit: null`. It found pi's provider/model coupling check during session setup, and that its
explicit-model resolver does not use the `--models` scope to disambiguate overlapping IDs. This
evidence is version-bounded: it does not identify another machine's fork revision and cannot
establish that runtime's behavior. The implementation makes no API or inference requests.
Regression coverage exercises declaration decoding/validation and cloning, argv for a
provider-only selection, explicit overlapping IDs and a missing prerequisite while retaining
`--models`, and the production `yolo host` exec caller with a provider-only profile. It also
verifies the native settings file remains unchanged. Focused source tests and an independent
review were complete on 2026-10-07; no pi process or model request was made.

**Still owed, UNMEASURED.** A nested-jail check of `yolo host -p pi=` with a provider-only
profile has not been run.

---

## 15. The latest rulings, in practice

No question in this doc is open. Every ruling is in the [Decision Ledger](#16-decision-ledger); the three below changed what a user sees, so each is spelled out here.

<a id="oq-mm1-setup"></a>**What a list that only adds shows beside an agent's catalog (ruled
2026-10-15, [OQ-MM1](#OQ-MM1)).** An `add` sits beside the agent's own catalog for that
provider, and only an `only` makes a menu exact. claude's native built-in rows stay, and
opencode and pi keep their catalogs. Where the agent has no catalog for the provider, such as
any provider claude is routed to or a key the agent does not ship, the menu is exact anyway.
The verbs already say it: `add` means "these too" and `only` means "just these", so a company
gets either with one line. Making every list exact would let a provider pack's own one-model
default, which [OQ-ML1](#16-decision-ledger)'s ruling has every provider declare, shrink a menu to one model.

**What this means in practice.** Priya's company pack adds three Bedrock models, GPT-6 Astra,
Kimi and Claude Opus 5.5, to the `bedrock` provider with an `add` and no `only`, and she runs
`yolo -p bedrock -- opencode`. She sees opencode's 166 Bedrock models with her three among
them, pi's 176, and claude's built-in rows with Opus 5.5 appended. The other native Bedrock
clients take one family each ([§1](#1-the-words-plainly) for claude;
[`bedrock-plumbing.md`](bedrock-plumbing.md#where-the-split-ended-up) for codex), so claude's
`-p bedrock`, its native profile, can append Opus 5.5 at most, and codex's takes GPT-6 Astra.
GPT-6 Astra and Kimi reach claude only on the everything profile, whose list is exact anyway.
One `only` line in her company's pack makes opencode's and pi's menus exactly her list, and
claude's exactly Opus 5.5 below its Default row. Dana's five-model
`providers.openrouter.models` shows among opencode's whole OpenRouter catalog, about 374
models, because opencode starts any provider named `openrouter` from its catalog, until she
states them as an `only`; pi's `zai` menu is its own 7 models plus yolo's. A user's own list
and a pack's mean the same thing.

<a id="oq-mm3-setup"></a>**What a list with no `only` refuses on a gateway, and what keeps
claude's start valid (ruled 2026-10-15, [OQ-MM3](#OQ-MM3)).** Two rulings, both about providers
where the agent has no catalog of its own: every gateway claude is routed to, such as z.ai,
OpenRouter or Kilo, and every key pi, oh-omp or opencode does not ship.

- **No refusal without an `only`.** A list no `only` narrowed refuses nothing there. A model
  id the user types goes to the provider, which serves it or returns an error. A refusal stays
  under an `only`, and on `openai-codex`, whose list is everything the subscription serves.
  That narrows [OQ-WG3](wire-bridge-gateway.md#decision-ledger)'s "enforced by default" on these
  providers. Refusing by default wherever the list is the whole universe is rejected.
- **claude's saved model is written once when the provider changes.** With no allowlist,
  nothing would check the model claude starts on. Its saved `/model` choice, the `model` key in
  `~/.claude/settings.json`, could name a model of the previous provider and fail at the first
  request. So when the selected provider changes, yolo writes that key once. The value is the
  previous model's equivalent on the new provider, such as Opus on Bedrock for Opus on a
  Claude Teams login, matched by claude's tier word (`opus`, `sonnet`, `haiku`). When there is
  no tier word in the saved value, or the new provider has no model in that tier, yolo writes
  the new provider's default model. [OQ-ML1](#16-decision-ledger)'s ruling has every provider declare its
  default, so there always is one. While the provider stays the same, yolo leaves the key
  alone, so the user's own `/model` choice is kept. On the next provider change yolo replaces
  the value, and on a deselect it clears it only while it still holds what yolo wrote. That is
  the record-what-yolo-wrote rule pi and codex already follow
  ([`providers.md`'s deselection](../reference/providers.md#deselection-clear-what-yolo-wrote-keep-what-the-user-wrote)).
  The mechanism, and what the code still lacks, is [MM-D43](#MM-D43).

**Why yolo did not write claude's model before.** "yolo never writes claude a model id" is a
note in [`providers.md`](../reference/providers.md#deselection-clear-what-yolo-wrote-keep-what-the-user-wrote)
(the IMPORTANT box beginning "Claude needs no clear"), not a ruling. yolo never needed to write
the key, because it set the model on every launch through environment variables
(`ANTHROPIC_MODEL`), and a per-launch variable overrides the user's `/model` choice every
launch. That box is owed an update once this is built. A `claude` started outside yolo also
reads the written value; the owner has said such launches do not matter under host management.

**What this means in practice.** Lee's z.ai list has three GLM models, and `/model glm-4.7`
still reaches z.ai. Omar's saved model is the direct-Anthropic `claude-opus-5-5`. When he moves
to z.ai, whose list declares no Opus-tier model, yolo writes the z.ai list's default. When he
moves to a Bedrock profile whose list names an `opus` model, he gets that one.

<a id="oq-ml4-ml5-setup"></a>**How a pi model row gets its facts (ruled 2026-10-16,
[OQ-ML4](#OQ-ML4) and [OQ-ML5](#OQ-ML5)).** A model's **facts** here are what pi needs to know
about it besides its id: its price, context size, output limit, input types, whether it
reasons, and its thinking levels. yolo writes them into two files: `models.json`, which pi
itself reads, and `yolo-model-lists.json`, pi's menu, which yolo's own extension reads. pi also
ships a **catalog** of these facts for the models it knows.

- **A row keeps every catalog fact it does not replace ([OQ-ML4](#OQ-ML4)).** yolo merges a row
  over pi's catalog entry field by field. A fact the row states wins. A fact the row leaves out
  comes from the catalog entry for the row's id, or for its `base` when it names one. Today a
  `models.json` row replaces the catalog entry whole, so a row that lists no thinking levels has
  none, and `--thinking xhigh` is turned down. The menu file already merges this way.
- **A fact is removed only when a row says so.** A **tombstone** is a fact written as `null`:
  the model then has no such fact, whatever any layer below says ([ML-D10](#ML-D10)). It is not
  "leave this out", which inherits. The word is the one yolo's config layers already use for a
  `null` that deletes a key ([config surfaces](../reference/pack-system.md#config-surfaces-and-the-compose-engine)).
  yolo never drops a catalog fact on its own.
- **Facts may be written once and shared by a model's routes ([OQ-ML5](#OQ-ML5)).** One model
  often appears once per route, such as Bedrock and OpenRouter, and yolo copies its facts into
  each row. They may instead be written once and shared. But providers can charge different
  prices, or serve a different context size or output limit, for the same model, so every
  route's row can override any shared fact. The order is: the route's own value, then the
  shared facts, then pi's catalog. A tombstone works at each of those layers.
- **The shared form goes only to a pi that reads it ([ML-D11](#ML-D11)).** The short spelling,
  `sharedModels` holding the facts once and a row saying `extends` to use them, is read by the
  downstream consumer's own pi build. Stock pi ignores both keys without an error, so a row that
  relies on them silently gets pi's generic defaults. yolo writes the shared form only where the
  pi that runs reads it, and full merged rows everywhere else.

**In practice.** A route row for a model on OpenRouter that states only its price keeps the
catalog's thinking levels, context size and output limit. The same model's Bedrock row that
states a smaller context size uses that size and shares everything else. A row that writes
`"reasoning": null` has no reasoning flag at all, even though the catalog has one.

Both rulings are built ([ML-D12](#ML-D12) says how): the merge with pi's catalog runs in
yolo's pi extension, and in `models.json` itself on pi's own Bedrock client, where a row naming a
catalog id is written as pi's `modelOverrides` entry ([ML-D13](#ML-D13)); the shared facts are a top-level `model_facts` map named by a model entry's
`extends`, and the pack that installs a pi taking the short form declares `reads_shared_models`
on its program, or on its fork of packs/pi's ([ML-D14](#ML-D14)); packs/pi does not, so stock
pi gets full rows.

---

## 16. Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-BR3 | **yolo picks model ids where an agent cannot default, and ships them in a built-in pack that comes with yolo, not in core.** *"in cases where we can't just fall to the default by just not having an opinion and letting the agent pick … I do want to pick these … let's actually ship this in core some way … a built-in pack (it's not in core per se but it comes with [yolo]) with these that tries to pick these different models."* Moved from [`bedrock-plumbing.md`](bedrock-plumbing.md), id kept. Narrows [OQ-GP2](gateway-provider-packs.md#decision-ledger) (note made there) and, in letter, the third question of agent-auth-modes.md (that doc is deleted) | 2026-09-25 | [§5](#5-the-picks-pack) | — |
| OQ-PSW3 | **Answered by [OQ-BR3](#16-decision-ledger)'s ruling: yolo ships the ids, in a built-in pack.** Moved from the retired `provider-switching.md`, where it was `PS3` before the rename noted at the top of this doc. The Anthropic-on-Bedrock geo-prefix verification stays a build prerequisite, not a question; which cases get ids is [OQ-ML2](#16-decision-ledger) | 2026-09-25 | [§5.3](#53-the-prerequisite-verify-before-an-id-ships) | — |
| <a id="ML-D1"></a>ML-D1 | *Implementation decision.* **The `openai-codex` model list is declared once, on the provider, in [`packs/codex-auth/pack.toml`](../../packs/codex-auth/pack.toml), and every consumer reads it.** That pack is the provider's only owner, and pi, claude and codex each `needs` it unconditionally, so it is in every launch that has a consumer. The composed provider entry is also the only way pack data reaches a derive, whose sandbox has no `io` and no `require`. **Shape:** `models` maps each alias to an identical id, as `packs/zai` does, so a profile's `model` means the same thing read as an alias or as an id. Only wire-true base ids are declared: `[1m]` is a client spelling that Claude Code, the extension's request hook and the wire bridge each strip. **Facts**, in `model_options`: `order`, because the map is unordered all the way from the Go map through the composed table to Lua's `pairs`; `name`; `description`; `context_window`; and `long_context_window`, meaning the model has a 1M-context variant. The default is the lowest `order`. There are no provider `options`, because declaring any turns on the profile-option census and would refuse user `codex` profiles that pass today. There is no `default` alias, because it would repeat an id every lister then has to drop. **Expansion:** one helper, copied verbatim into the claude, pi and codex derives because a derive cannot load another file, with a test failing when the copies differ. This builds [OQ-ML1](#16-decision-ledger) option (b); ML1, [OQ-BR12](#16-decision-ledger) and [OQ-BR13](#16-decision-ledger) stay open, and ruling (a) later moves only the declaration, since every consumer reads the composed `openai-codex` entry. The three ids are the GPT-6 entries of pi-ai 0.87.1's own `openai-codex` catalog, each at a 272,000-token window, read 2026-09-27 | 2026-09-27 | [§3](#3-what-exists-today) | ✅ `bbb7c223`; opencode, the fourth consumer, 2026-10-01 (`TestEveryCodexModelConsumerReadsTheOneDeclaration` renders it, `TestCodexModelListHelperIsIdenticalInEveryDerive` checks its copy) |
| <a id="ML-D2"></a>ML-D2 | *Implementation decision.* **pi writes no `enabledModels` for `openai-codex`.** The maintainer, 2026-09-27: *"So I just want them aligned for the moment, or we can just stop filling in scoped so there's only all. I don't actually need both of them right now."* The extension registers exactly the declared list ([ML-D3](#ML-D3)), so pi's "all" view for `openai-codex` is that list, and a scope could only restate it. pi 0.87.1 shows its Scope toggle only when the scoped list is non-empty (`ModelSelectorComponent`), and with no scope a fresh session starts on the saved `defaultProvider`/`defaultModel` pair (`findInitialModel`). **Existing jails need no migration code:** the selection's per-key deselect rule ([OQ-PSW2](../reference/providers.md#oq-psw2)) clears the list an older yolo recorded, with the codex profile still active, and notes it in `boot.log` ([OQ-PSW4](../reference/providers.md#oq-psw4)). A list the user or pi's save-as-default edited is kept. v0.10.0 wrote the scope as a plain computed key that no record names, and a jail it booted loses the list too: the upgrade boot recomposes the file from its layers, and none asserts the key any more. The log line used to give the reason "the profile that set it is no longer selected", false on this path, and now says "yolo's selection no longer sets it". **What it rules against:** it narrows the letter of [OQ-CN4](../reference/providers.md#oq-cn4) for `openai-codex`, whose menu narrowing in pi is now the registered catalog rather than `enabledModels`, and supersedes, for `openai-codex` only, CN-D17's *"pi's `enabledModels` is unchanged"* (CN-D20 in [that ledger](../reference/providers.md#the-credential-gate-build-ledger)) and the deleted pi-model-selection-ux.md's recommendation. Every other provider keeps its scope; the non-codex arms render it from the same `models` map as their catalog rows | 2026-09-27 | [§8](#8-rendering-the-effective-list-into-each-picker) | ✅ `2a34a176`; the v0.10.0 upgrade pinned `05065539` |
| <a id="ML-D3"></a>ML-D3 | *Implementation decision.* **pi's extension reads the list from a data file yolo renders, and takes every pi-dialect fact from pi's own catalog.** The file is a computed JSON surface, `pi/codex-models`, at `~/.pi/agent/yolo-openai-codex-models.json`: beside `extensions/`, outside pi's extension discovery and outside the extension's own read-only delivery. It renders on every jail launch, with or without a profile, because the extension registers the provider on every launch. `yolo host apply` renders no list into it ([ML-D8](#ML-D8)). Each entry carries the id, a variant's `base`, the name and the context window. The extension looks each one up with `getBuiltinModel("openai-codex", base ?? id)`, imported from `@earendil-works/pi-ai/providers/all` (an alias pi's extension loader installs), and overrides id, name and context window: the consumer translates ([OQ-CS4](../reference/providers.md#oq-cs4)), so yolo no longer re-copies pi-ai. The catalog entry's address fields are dropped, so the registration alone says where requests go. An id pi's catalog lacks gets pi's own models.json defaults (`modelFromJson`), and the extension says so ([ML-D7](#ML-D7)). A missing, malformed or empty file registers no `models`, so pi keeps its built-in catalog and login never depends on the file. MEASURED 2026-09-27 by loading the shipped extension through pi 0.87.1's own `loadExtensions` with an isolated home, no CLI and no session: the import resolves, `gpt-6-sol[1m]` gets its base's facts at a 1,000,000-token window, an unknown id gets the defaults, and no file registers no `models`. The six GPT-6 definitions it registers equal, field for field, the ones the hand copy carried, so pi's cost, thinking and image behavior for them is unchanged. **Rejected channels:** an env var, which composes nothing without a profile while the extension always registers; a `models.json` row, the shadow [OQ-2](pi-codex-provider-shadowing.md#10-decision-ledger) forbids and which the extension's layer would override anyway; parsing `YOLO_PROVIDERS` in JavaScript, a second copy of the expansion reading an internal transport; and rendering the extension itself from Lua, which gives up its read-only files delivery | 2026-09-27 | [pi-codex-provider-shadowing §2.2](pi-codex-provider-shadowing.md#22-pi-pi-coding-agent) | ✅ `bbb7c223` |
| <a id="ML-D4"></a>ML-D4 | *Implementation decision.* **The GPT-5.x models are gone from pi's `openai-codex` list.** The extension registered five GPT-5.x ids only because pi-ai's catalog had been copied into it by hand in `e2f7bb89`: a registration replaces a provider's model list, and cannot add to it. claude's list and pi's scope had already dropped them as superseded (`2f11de95`). **Getting one back** takes one alias in the declaration, with its `model_options` for order, name and a 1M variant, or one alias in your own config, `providers.openai-codex.models` (`"gpt-5.6-sol": "gpt-5.6-sol"`). A model added in your config lists after the declared ones, with no 1M variant, since `order`, `description` and `long_context_window` are declaration facts the user's model entry does not take. Either way it appears in claude's picker too. **One consequence for a first login:** pi 0.87.1's own default for `openai-codex` is `gpt-5.5` (`defaultModelPerProvider`, `core/model-resolver.js`), and after `/login` a pi with no model yet selects that id from the provider's models (`completeProviderAuthentication`, `modes/interactive/interactive-mode.js`). In a jail the extension no longer registers it, so that login selects nothing and pi says *"its default model \"gpt-5.5\" is not available. Use /model to select a model."* v0.10.0, whose extension registered no models, selected `gpt-5.5` there. A `-p codex` launch is unaffected, since its settings pair names the declared default. At the host notch the extension registers no list ([ML-D8](#ML-D8)), so pi's catalog, `gpt-5.5` included, stays in place and the login selects it as before. Registering `gpt-5.5` again to satisfy the pick would put back the GPT-5 model this decision removes. Found in review, 2026-09-27 | 2026-09-27 | [§3](#3-what-exists-today) | ✅ `bbb7c223` |
| <a id="ML-D5"></a>ML-D5 | *Implementation decision.* **pi-subagents' `modelScope.allow` for `openai-codex` is the declared ids, exactly**: `openai-codex/<id>` for each entry, 1M variants included. It replaces the hand-written `openai-codex/gpt-6-*` glob, which was the family written down a third time. pi-subagents 0.35.1's `globToRegExp` escapes `[` and `]` before it turns `*` into a wildcard (`src/runs/shared/model-scope.ts`), so an id ending in `[1m]` matches literally. `enforce` and `strict` are unchanged. An empty list writes no `modelScope`, since an enforced, strict, empty allow would refuse every child model. *Superseded for the empty list on 2026-09-28 by [XM-D4](../reference/providers.md#XM-D4): it now scopes `openai-codex/*`, and every provider gets the block* | 2026-09-27 | [§8](#8-rendering-the-effective-list-into-each-picker) | ✅ `bbb7c223` |
| <a id="ML-D6"></a>ML-D6 | *Implementation decision.* **Each id is one row, and the alias spelled as the id carries its facts.** Another alias naming the same id fills only a fact that row still lacks, taken in sorted alias order. The helper first kept whichever alias sorted first. `default` and `fast`, yolo's conventional alias names, sort before every declared id, so a user's `providers.openai-codex.models.default = "gpt-6-sol"` gave Sol that alias's facts, which were none. Sol then listed last with no name and no 1M variant, and every agent's default moved to Astra. Merging only into gaps also lets a second alias supply a fact the id's own alias lacks, such as a `name` for a model the user added. Found in review, 2026-09-27 | 2026-09-27 | [§3](#3-what-exists-today) | ✅ `4725106c` |
| <a id="ML-D7"></a>ML-D7 | *Implementation decision.* **The extension warns, once per load, when pi's catalog cannot describe a model it registers.** Without pi's catalog entry a model registers with the defaults [ML-D3](#ML-D3) names: text-only, no thinking levels and a 16,384-token output cap. That happens when the catalog import fails, when the module no longer exports `getBuiltinModel`, or when the catalog lacks an id, such as a model the user added. pi is not version-pinned in `packs/pi/pack.json`, and the hand copy the lookup replaced could not degrade this way, so a renamed alias would otherwise cost every model silently. The warning goes through pi's own `ctx.ui.notify(…, "warning")` at `session_start` when there is a UI, and to stderr when there is none, which is what pi's extension runner does with its own diagnostics. It names the ids the catalog lacks, or the import failure. An empty list attempts no import and says nothing, since pi's built-in catalog stays in place. MEASURED 2026-09-27 through pi 0.87.1's own `discoverAndLoadExtensions` with an isolated home: the shipped six-entry list resolves every entry from the catalog and warns nothing, and a list adding `gpt-6-nova` warns once, naming it. Found in review, 2026-09-27 | 2026-09-27 | [pi-codex-provider-shadowing §2.2](pi-codex-provider-shadowing.md#22-pi-pi-coding-agent) | ✅ `20769a45` |
| <a id="ML-D8"></a>ML-D8 | *Implementation decision*, **SUPERSEDED 2026-09-28** by [OQ-HC1](../reference/host-agent-environment.md#oq-hc1): `yolo host apply` now renders the declared `openai-codex` list into `pi/codex-models` at the host, and host pi registers it ([what each surface gets at the host](../reference/host-agent-environment.md#what-each-surface-gets-at-the-host)); `TestTheHostNotchLeavesPiOnItsOwnCodexCatalog` is inverted as `TestTheHostNotchGivesPiTheDeclaredCodexList`. What this row decided: *Implementation decision.* **At the host notch pi keeps its own `openai-codex` catalog, and the docs say so.** `yolo host apply` renders a surface from its declared layers only: it runs no derive for content, since the host target's computed layer is empty by design ([host-render-target §3.3](host-render-target.md#33-what-each-target-supplies)), and it composes no provider table. `pi/codex-models` holds nothing but a derive's expansion of the declaration, so it renders as `{}` under `host_management: assert` and is refused under `own`, which runs no `computed` surface. The extension the host notch delivers then registers no models, and host pi shows pi-ai's built-in `openai-codex` list: in pi 0.87.1 the three GPT-6 ids with no `[1m]` variant, beside five GPT-5.x ids (MEASURED 2026-09-27 with `getBuiltinModels`). That is what v0.10.0 gave host pi too, since its extension registered the login alone. The hand copy that registered yolo's list at the host came later and never shipped in a release. There is still one list at the host, because a host apply selects no profile and no other agent renders an `openai-codex` list there. Whether the host should render the declaration is [OQ-ML3](#16-decision-ledger). Three places said the file renders at every boot, and each now says every jail boot: [ML-D3](#ML-D3), the extension's comment and [pi-codex-provider-shadowing §2.2](pi-codex-provider-shadowing.md#22-pi-pi-coding-agent). `TestTheHostNotchLeavesPiOnItsOwnCodexCatalog` runs the host render, the host files delivery and the delivered extension in both contracts, and fails once the host renders a list. Found in review, 2026-09-27 | 2026-09-27 | [§3](#3-what-exists-today) | ✅ `f3da48dc` |
| <a id="ML-D9"></a>ML-D9 | ⚠ **Amended 2026-10-05 by [MM-D32](#MM-D32)**: yolo ships no Bedrock list, so no agent but copilot starts on a model yolo picked, and the rule below picks only among a list a pack or the user supplies. *Implementation decision.* **The Bedrock list is declared once, on the `bedrock` provider in [`packs/bedrock/pack.toml`](../../packs/bedrock/pack.toml), and each agent picks among the entries its own client can call.** [OQ-ML1](#16-decision-ledger)'s ruling (each provider's own pack declares its default) and [OQ-ML2](#16-decision-ledger)'s (yolo picks only to make a session valid), applied to a provider of several makers ([`bedrock-plumbing.md` OQ-BR9](bedrock-plumbing.md#decision-ledger)). Each entry carries `vendor` beside `order`, `name`, `context_window`, `max_tokens` and `input` in `model_options`, the channel [ML-D1](#ML-D1) set; there is no `default` alias, so each agent's default is the first entry it can call. The pick: codex, opencode and pi get one, because their own defaults on Bedrock are a first-party slug, unread, or possibly a bare id runtime refuses ([§4](#4-where-yolo-picks-the-cannot-default-cases)); claude's own Bedrock client gets none unless the profile or a user `default` names an Anthropic entry, since its own default is valid. The upstream-default clause of [OQ-ML2](#16-decision-ledger) is not implemented as a comparison: no derive can read its agent's catalog default, and with none of the shipped entries being such a default the rule resolves to the declared one. [§5.3](#53-the-prerequisite-verify-before-an-id-ships)'s verify-before-an-id-ships rule is met per id, each read from its AWS model card on 2026-09-29 and dated in the pack README. The full set of per-agent decisions is [`bedrock-plumbing.md`'s BR-D6 to BR-D16](bedrock-plumbing.md#BR-D6) | 2026-09-29 | [§4](#4-where-yolo-picks-the-cannot-default-cases) | ✅ pinned by `TestTheShippedBedrockListDeclaresEachEntrysMaker` (deleted 2026-10-05 with the list, MM-D32), `TestBedrockModelListHelperIsIdenticalInEveryDerive`, `TestEachBedrockAgentStartsOnTheRuledModelInEveryRegion` (deleted 2026-10-05 with the list, MM-D32) (codex on GPT-6.1 Sol and opencode and pi on Claude Opus 5.5 in every Region, with GPT-6 Sol not shipped, [BR-D19](bedrock-plumbing.md#BR-D19), which superseded [BR-D17](bedrock-plumbing.md#BR-D17)'s global-first pick) and each agent's `…OnBedrock…` test |
| OQ-ML3 | **A, for now: `yolo host apply` renders no `openai-codex` list, and host pi keeps pi-ai's own catalog.** *"basically option 1, but then make sure there's a design doc about the host option left."* Whether the host notch should derive what a jail derives is [`host-agent-environment.md`'s computed layer at the host](../reference/host-agent-environment.md#what-yolo-host-apply-renders-into-a-derived-surface)'s [OQ-HC1](../reference/host-agent-environment.md#oq-hc1) | 2026-09-27 | [§3](#3-what-exists-today) | ✅ [ML-D8](#ML-D8) |
| OQ-ML1 | **No separate picks pack, and nothing always on: each provider declares its own default in its own pack, and that default layers like all config.** A company or user pack, or user config, overrides it, and yolo does not chase the newest model. Several active providers per agent moved to [`active-provider-sets.md`](active-provider-sets.md) | 2026-09-29 | [§5](#5-the-picks-pack) | the `openai-codex` declaration ([ML-D1](#ML-D1)) |
| OQ-ML2 | **yolo picks a model only to make a session valid, and never steers a valid choice unless the config opts in.** *"YOLO by default should never pick a default model … whenever you start up a session, we always want to make sure that we have picked one of those models."* The pick is the agent's upstream default when that is one of the provider's models, else the provider's declared default, else the first model it lists. Narrower than the leaning | 2026-09-29 | [§4](#4-where-yolo-picks-the-cannot-default-cases) | — |
| OQ-BR12 | **A: a `models` contribution kind, with the verbs `add` and `only`, for any provider; the engineer's own `providers.<name>.models` writes last.** *"of course their own config still has the last word because they could make that happen regardless."* Moved from [`bedrock-plumbing.md`](bedrock-plumbing.md), id kept | 2026-09-29 | [§7](#7-how-a-company-pack-shapes-a-list-the-models-kind) | ✅ `packload.applyModelContributions`, pinned by `internal/packload/modellists_test.go` ([MM-D11](#MM-D11)) |
| OQ-BR13 | **Directed: set the model selection however each agent allows.** *"I want us to set the model selection however we can … I need you to research whatever it is and then come back to me if there's a real decision here."* Researched per agent in [§14](#14-how-each-agents-menu-is-set-researched-2026-09-29); the mechanisms are [MM-D1](#MM-D1) to [MM-D10](#MM-D10), and what is left to rule is [OQ-MM1](#OQ-MM1) and [OQ-MM3](#OQ-MM3). [OQ-MM2](#16-decision-ledger) was settled on a corrected premise | 2026-09-29 | [§14](#14-how-each-agents-menu-is-set-researched-2026-09-29) | `openai-codex` ([ML-D1](#ML-D1)); on 2026-09-29 every mechanism [OQ-MM1](#OQ-MM1) and [OQ-MM3](#OQ-MM3) do not decide ([§14.5](#145-what-was-built-2026-09-29)); on 2026-09-30 pi's wrapper and MM-D9 on `openai-codex` ([§14.6](#146-what-was-built-2026-09-30)); MM-D10 stopped ([OQ-MM4](#16-decision-ledger)) |
| OQ-BR15 | **Never: the bridge serves no `GET /v1/models`.** Settled by the measurement the question waited on, under [OQ-BR13](#16-decision-ledger)'s direction ([MM-D4](#MM-D4)) | 2026-09-29 | [§14.3](#143-the-bridges-part-and-get-v1models) | — |
| OQ-BR14 | **The agents' own catalogs, plus a `yolo check` warning for a listed id no installed catalog knows.** Decided as an implementation choice, reversible ([MM-D16](#MM-D16)). The policy half was ruled by [OQ-ML1](#16-decision-ledger) and [OQ-ML2](#16-decision-ledger): each provider's default is one simple pick the user changes (*"pick something simple for default, let users change it"*), so yolo does not chase the newest model, which rules out asking the Bedrock control plane at launch and a dated catalog snapshot in yolo. Moved from [`bedrock-plumbing.md`](bedrock-plumbing.md), id kept | 2026-09-30 | [§9](#9-currency-the-agents-catalogs-plus-a-staleness-warning) | ✅ [MM-D16](#MM-D16) |
| OQ-PSW1 | **claude's derive reads `balanced` for the Sonnet tier, `fast` for the Haiku tier and `frontier` for the Opus tier, with `sonnet`, `haiku` and `opus` kept as synonyms that win where both are declared.** Decided as an implementation choice, reversible ([MM-D17](#MM-D17)), under the vocabulary [OQ-XM2](../reference/providers.md#oq-xm2) ruled. Moved from the retired `provider-switching.md`, where it was `PS1` | 2026-09-30 | [§6](#6-tier-aliases-default-fast-balanced) | ✅ [MM-D17](#MM-D17) |
| <a id="OQ-MM1"></a>[OQ-MM1](#OQ-MM1) | **A: an `add` sits beside the agent's own catalog for that provider, and only an `only` makes a menu exact.** claude's native built-in rows stay, and opencode and pi keep their catalogs; a provider the agent has no catalog for (any provider claude is routed to, or a key the agent does not ship) is exact anyway. A user's own list and a pack's mean the same. The owner, in review: *"A: an add sits beside the agent's own catalog for that provider, and only an only makes the menu exact (claude's native built-ins stay, opencode and pi keep their catalogs); a provider the agent has no catalog for (any provider claude is routed to, or a key the agent does not ship) is exact anyway. The verbs already say 'these too' and 'just these', and B would let yolo's own one-model default shrink a menu."* Rejected: any list exact (B), and exact only for the user's own list (C) | 2026-10-15 | [What a list that only adds shows](#oq-mm1-setup) | ✅ Built. opencode, pi and oh-omp already leave an `add` beside their catalogs, rendering a narrowing only under an `only`: `TestOpencodeWritesNoWhitelistForAnUnnarrowedList` and the unnarrowed half of the oh-omp scope test (`internal/entrypoint/modelsonlyagents_test.go`), pi's `add` a `models.json` row (`packs/pi/derive.lua`). ✅ a routed claude's list replaces the built-ins ([MM-D1](#MM-D1)). ✅ claude's native profile, 2026-10-15: an `add` appends its Anthropic rows to claude's built-in rows with `replaceBuiltInOptions: false` and no allowlist, and keeps the tier pins it had: the list's default or the profile's `model` pins Opus in the launched process, and a tier the list names by alias takes that alias there and in `settings.json`'s `env` block ([MM-D1](#MM-D1), [MM-D2](#MM-D2)); `TestClaudeAppendsAListThatOnlyAddsBesideItsOwnMenu`, and `TestClaudeNativeBedrockDefaultPinsOpusAndIsTheSavedModel` for the kept pins with the saved model |
| <a id="OQ-MM3"></a>[OQ-MM3](#OQ-MM3) | **No refusal without an `only` where the agent has no catalog, and claude's saved `model` written once per provider change.** On every gateway claude is routed to (z.ai, OpenRouter, Kilo) and every key pi, oh-omp or opencode does not ship, a list no `only` narrowed refuses nothing: a typed model id goes to the provider, which serves it or errors. A refusal stays under an `only` and on `openai-codex`, where the list is everything the subscription serves. This narrows [OQ-WG3](wire-bridge-gateway.md#decision-ledger)'s "enforced by default" on those providers. When the selected provider changes, yolo writes `model` in claude's `settings.json` once: the previous model's equivalent on the new provider, matched by claude's tier word, else the new provider's default model, which [OQ-ML1](#16-decision-ledger)'s ruling guarantees exists. The key is left alone while the provider stays the same, and cleared on a deselect only while it still holds what yolo wrote. The owner: *"if you pass only, it should be filtered to only those. I think we agree there. If you add things, it's these two. It can be from the users config, the companies config, wherever. And then there are agents that you specify manual IDs. If that manual ID, if there's no only here, I think that manual ID should go through and it will work or not. For the default model, I'm not sure what we can do here. I guess do the best thing possible. ... we can't know what we don't know. And we can't know if that model ID is going to work"*; on refusing by default: *"You're saying if Lee just has the Z.AI pack configured and he chooses a model that should be involved, it will always be refused? ... That just sounds totally wrong?"*; on the write: *"yes, I like the "once" option. can we know the previous session's model? if so, choose the queivalent, like opus on bedrock vs opus on claude teams. so is this a coherent option now?"*; on the fallback: *"When the saved value has no tier word, which I guess means you can't figure out this mapping, and when that tier doesn't exist, I guess it's essentially the same case. We should just pick the default model. Every provider should have a default model."* Standing: *"we generally just don't care about launches outside YOLO if you're using host management."* Rejected: refusing by default wherever the list is the whole universe (the old A), and its default kept with a pin only when the switch is off (the old C). Mechanism: [MM-D43](#MM-D43) | 2026-10-15 | [The ruling](#oq-mm3-setup) | ✅ Built 2026-10-16. pi: no refusing wrapper and no strict pi-subagents scope without an `only` (`TestPiSubagentsScopeIsStrictOnlyUnderAnOnly`, `TestPiBedrockUnderAnOnlyIsNarrowedAndStrict`); claude: no allowlist without an `only`, and the saved `model` written once per change, kept while valid on its route ([MM-D43](#MM-D43), [MM-D44](#MM-D44); `TestClaudeUpgradeKeepsAValueValidOnItsRoute`, `TestClaudeChangeRemovesAPickTheNewRouteCannotCall`); opencode and the bridge already refused nothing. Known gap: a stale own-login value with no tier word survives the first launch after upgrading (MM-D44) |
| OQ-MM2 | **B: if copilot's `providers.json` cannot read its key from the environment, the launch's per-agent env writer puts the file beside `agent-env/copilot.sh`, with the same mode and lifetime.** Settled on a corrected premise, rather than asked: the key is already in that 0600 file, so a second file adds no reader, while a rendered config file would break the caller-token rule ([MM-D10](#MM-D10)) | 2026-09-29 | [OQ-MM2](#16-decision-ledger) | — |
| <a id="MM-D1"></a>MM-D1 | *Implementation decision.* **claude's picker keys follow where the list reaches.** On a routed provider, whose list is the whole universe (the bridge, a gateway's own Anthropic endpoint, `openai-codex`), `yolo.derive("claude", "settings")` always writes `modelPicker` with `replaceBuiltInOptions: true`. The built-ins go because on a routed provider they are only claude's tier names, meaning whatever the tier pins say ([MM-D2](#MM-D2)), so the list's own rows show instead. That revises [OQ-BR13](#16-decision-ledger)'s sub-choice "built-ins replaced only on the everything profile". `availableModels`, with the default first, and `enforceAvailableModels: true` render on `openai-codex`, as today, and under an `only`, while the profile's switch is on ([MM-D5](#MM-D5)). The allowlist is claude's one way to make a session valid without steering it, which [OQ-ML2](#16-decision-ledger)'s ruling asks for: an off-list saved model is replaced at startup by Default, which the tier pins make a list model ([§14.2](#142-what-each-row-rests-on)), while a valid `/model` choice survives the next launch ([MM-D3](#MM-D3)). It does not render on a routed list with no `only`, where a gateway such as z.ai, Kilo or OpenRouter serves more than the list and a typed id the list lacks goes to the gateway ([OQ-MM3](#OQ-MM3), ruled 2026-10-15). On the native profile it writes the same three under an `only`. What an `add` writes there is [OQ-MM1](#OQ-MM1)'s; under its ruling (A, 2026-10-15), rows appended with `replaceBuiltInOptions: false` and no allowlist. This restates [§11](#11-forbidden-and-non-goals)'s ban as: no refusal on a list that sits beside an agent's own catalog. The keys go to `~/.claude/settings.json`, the one scope besides managed settings and `--settings` that reads `modelPicker` | 2026-09-29 | [§14.1](#141-the-table) | ✅ the routed picker, `TestClaudeOnARoutedProviderShowsTheListAndPinsEveryTier` and `TestClaudeOnABridgedProviderShowsTheList`; the three keys under an `only`, native and routed, `TestClaudeUnderAnOnlyOnItsOwnBedrockClient` and `TestClaudeUnderAnOnlyOnARoutedProvider`; no allowlist on a routed list no `only` narrowed, as [OQ-MM3](#OQ-MM3) rules, pinned by the z.ai half of `TestClaudeOnARoutedProviderShowsTheListAndPinsEveryTier` |
| <a id="MM-D2"></a>MM-D2 | *Implementation decision.* **Every list that replaces claude's built-ins pins all of claude's tiers to list ids**: a routed list ([MM-D1](#MM-D1)), and an `only`. `ANTHROPIC_DEFAULT_OPUS_MODEL`, `_SONNET_`, `_HAIKU_` and `_FABLE_`, with their `_NAME` and `_DESCRIPTION` where the entry has a name or description: each takes the tier's alias where the list declares one (which names count is [OQ-PSW1](#16-decision-ledger)'s, decided as [MM-D17](#MM-D17)), else the default entry. Why: claude's background, hook and classifier requests pick by tier and `availableModels` does not cover them, so an unpinned tier reaches an off-list model or draws the bridge's refusal. And the Default row resolves by tier whenever that tier's model is allowed, so on a list of non-Anthropic ids the pins are the only thing that makes Default a list model; under a managed source it resolves by tier too ([§14.2](#142-what-each-row-rests-on)). **Beside claude's own rows**, under an `add` on the native profile ([OQ-MM1](#OQ-MM1)'s ruling, 2026-10-15), the tier pins stay as they were before that ruling, by the owner's ruling of 2026-10-16: *"keep today's behavior"*. The list's default entry, or the profile's `model`, still pins the Opus tier, Sonnet and Haiku take their own aliases or fall back to it, and those pins travel in the process env alone; a tier the list names by alias takes that alias, in the process env and in `settings.json`'s `env` block, where a bare `claude` keeps it. That entry is also the saved model a provider change writes ([MM-D43](#MM-D43)). An earlier draft of this row pinned only the tiers the list names by alias; it was built on 2026-10-15 and reverted the next day. This supersedes [OQ-BR13](#16-decision-ledger)'s sub-choice "the Fable tier only from a declared `fable` alias" wherever the built-ins are replaced. **A pin travels with what makes it valid:** in the process env beside `ANTHROPIC_BASE_URL` on a routed provider, and in `claude/settings`' `env` block beside `CLAUDE_CODE_USE_BEDROCK` on the native profile, the channel [PV-D8](../reference/providers.md#pv-d8) chose because a host apply and a bare `claude` both keep it. A routed pin never goes in the settings file, where a bare `claude` would send it to Anthropic | 2026-09-29 | [§14.2](#142-what-each-row-rests-on) | ✅ `openai-codex`, `TestClaudeOnTheCodexListPinsTheStartOnlyOnTheOptIn`; routed, `TestClaudeOnARoutedProviderShowsTheListAndPinsEveryTier`; under an `only`, native in `settings.json`'s `env`, `TestClaudeUnderAnOnlyOnItsOwnBedrockClient`; beside claude's own rows under an `add`, the default still pinning Opus, `TestClaudeNativeBedrockDefaultPinsOpusAndIsTheSavedModel` and `TestClaudeNativeBedrockTiersReadTheConventionalAliases` |
| <a id="MM-D3"></a>MM-D3 | *Implementation decision.* **claude's start model is pinned only on an opt-in the derive can see.** `ANTHROPIC_MODEL` and `CLAUDE_CODE_SUBAGENT_MODEL` are emitted only when the profile sets `pin_model` *(an option name coined here, provisional)*, the opt-in [OQ-ML2](#16-decision-ledger)'s ruling allows; like any profile option, the provider's declared option set must admit it (the census [ML-D1](#ML-D1) names). The profile's `model` option cannot be the opt-in. `packload.ResolveProfiles` merges each provider's declared option defaults under the profile's own values, so that *"a derive never has to know whether a value was stated or inherited"* (`ResolvedProfile`). zai's declared `glm-5.3`, and cerebras's and llamacpp's `default`, therefore read exactly like a user's pin, and [OQ-ML1](#16-decision-ledger)'s ruling has every provider declare a default, so every provider would count as opted in. `model` keeps its job of naming the list's default entry, which [MM-D2](#MM-D2)'s tier pins and `availableModels`' order lead with. With `availableModels` and enforcement on, an off-list start model is already replaced by Default, which the tier pins make a list model ([MM-D2](#MM-D2)), so a valid start needs no pin; where the allowlist does not render, what keeps the start valid is [OQ-MM3](#OQ-MM3)'s, ruled 2026-10-15: a saved `model` written once per provider change ([MM-D43](#MM-D43)). Built 2026-10-15, so no provider keeps a pin without the opt-in ([MM-D44](#MM-D44)). The variable would override every `/model` choice at every launch, since claude returns to it each time. Subagents otherwise inherit the session's model or their tier, both on the list by [MM-D2](#MM-D2). Changes the `openai-codex` branch of `yolo.env("claude")` and its provider-`models` branch | 2026-09-29 | [§14.2](#142-what-each-row-rests-on) | ✅ `openai-codex`, `TestClaudeOnTheCodexListPinsTheStartOnlyOnTheOptIn`, and under an `only`, `TestClaudeUnderAnOnlyOnARoutedProvider`; `pin_model` declared, with no default, on the shipped providers that declare options; every other provider, 2026-10-15 ([MM-D44](#MM-D44)), `TestClaudeOnARoutedProviderShowsTheListAndPinsEveryTier` and `TestClaudeOnTheCodexListWithEnforcementOff` |
| <a id="MM-D4"></a>MM-D4 | *Implementation decision.* **The bridge serves no `GET /v1/models`, now or later.** claude's gateway discovery survives `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` but keeps only ids containing "claude" or "anthropic", never runs while a `CLAUDE_CODE_USE_*` variable is set, adds only allowed models, and is hidden by `replaceBuiltInOptions`; opencode, codex and copilot never ask such a route (MEASURED and SOURCED, [§14.3](#143-the-bridges-part-and-get-v1models)). [WB-D14](../reference/wire-bridge.md#wb-d14)'s one-route surface is unchanged | 2026-09-29 | [§14.3](#143-the-bridges-part-and-get-v1models) | — (nothing to build) |
| <a id="MM-D5"></a>MM-D5 | *Implementation decision.* **One switch decides every refusal, and a list never changes an agent's path.** [OQ-WG3](wire-bridge-gateway.md#decision-ledger)'s enforcement switch, a profile setting that defaults on, governs each refusal yolo installs: the bridge's allowlist, claude's `availableModels` and `enforceAvailableModels`, and pi's wrapper. With it off the list only shapes menus: claude gets `modelPicker` alone, so an off-list saved model is no longer replaced at startup, and pi gets a registration without the wrapper. Nothing yolo writes then checks claude's start, which [OQ-ML2](#16-decision-ledger)'s ruling requires; [OQ-MM3](#OQ-MM3), ruled 2026-10-15, has yolo write claude's saved `model` once per provider change ([MM-D43](#MM-D43), built 2026-10-15). On a gateway such as z.ai, OpenRouter or Kilo, and on any key the agent does not ship, a list no `only` narrowed installs no refusal at all, so the switch governs nothing there but an `only` ([OQ-MM3](#OQ-MM3)). opencode cannot shape its menu without refusing, so with the switch off it gets no whitelist, and `yolo check` says its menu is then not narrowed. **On claude's native profile the refusal is claude's own, client side**, with [§14.2](#142-what-each-row-rests-on)'s four gaps, and yolo describes it that way, as [OQ-CN4](../reference/providers.md#oq-cn4) requires of a soft limit. An `only` never moves claude onto the bridge, since a profile picks one path ([OQ-WG5](wire-bridge-gateway.md#decision-ledger)); a company that wants the wire to refuse selects a bridged profile. yolo writes no managed Claude Code settings file, which would close the prefix gap but override the engineer's own settings, against [OQ-BR12](#16-decision-ledger)'s *"their own config still has the last word"* | 2026-09-29 | [§14.1](#141-the-table) | ✅ the switch, `enforce_models` ([MM-D13](#MM-D13)), `TestTheModelEnforcementSwitchReachesBothDerivePaths`; it governs claude's allowlist, `TestClaudeUnderAnOnlyOnARoutedProvider` and `TestClaudeOnTheCodexListWithEnforcementOff`, and opencode's whitelist, `TestOpencodeWhitelistsANarrowedList`; the bridge's allowlist, 2026-09-30 ([`wire-bridge-gateway.md` WG-I40](wire-bridge-gateway.md#WG-I40)); pi's wrapper, 2026-09-30 ([MM-D21](#MM-D21)), `TestPiModelListsExtensionRefusesAModelOutsideTheList` and `TestPiNarrowedListCarriesItsProfilesModelSwitch`, and on `openai-codex` ([MM-D23](#MM-D23)), `TestPiOpenAIAuthExtensionRefusesNothingWithTheSwitchOff`; the `yolo check` line for opencode with the switch off, 2026-10-01 ([MM-D29](#MM-D29)) |
| <a id="MM-D6"></a>MM-D6 | *Implementation decision.* **pi: an `only` becomes an extension registration of exactly the list, for any provider pi ships.** The data travels the way `pi/codex-models` does ([ML-D3](#ML-D3)), generalized into one derive-rendered file keyed by pi's provider id (yolo's native `bedrock` is pi's `amazon-bedrock`), and each entry's facts come from `getBuiltinModel`. With the switch off the registration passes `models` only, which keeps pi's own address, wire and credential (MEASURED). With it on ([MM-D5](#MM-D5)) the registration is `models`, `api` and a `streamSimple` wrapper, since pi 0.99.1 refuses a `streamSimple` without `api`. That pi's own credential still reaches the delegated stream in that form is INFERRED, and measured before it ships ([§14.4](#144-build-order)). Either form sets its own `name`, so pi 0.99 does not label yolo's `openai-codex` provider "(legacy)", and each row's display name follows [MM-D7](#MM-D7)'s rule. Where the registration is exact, no `enabledModels` is written for that provider, as [ML-D2](#ML-D2) did for `openai-codex`. An `add` stays a `models.json` row beside pi's catalog ([OQ-MM1](#OQ-MM1), ruled 2026-10-15). **An `only` narrows only the provider it names:** providers the engineer signed into (pi's `auth.json`, oh-omp's `agent.db`) are untouched, and withholding a credential stays [`providers.md`'s OQ-CN4](../reference/providers.md#oq-cn4)'s | 2026-09-29 | [§14.2](#142-what-each-row-rests-on) | ✅ the models-only registration under an `only`, `TestPiGetsANarrowedListToRegister`, `TestPiModelListsExtensionRegistersTheRenderedListAlone` and `TestPiRegistersNoNarrowedListItCannotUse` ([MM-D14](#MM-D14)); the `openai-codex` registration's name, `TestPiOpenAIAuthExtensionNamesTheProviderItRegisters`; the selection's `defaultModel` on the list's default entry under an `only` that dropped the profile's model, `TestPiStartsOnANarrowedListsDefault`; the refusing wrapper, 2026-09-30, once its credential path was MEASURED ([§14.4](#144-build-order), [MM-D21](#MM-D21)), `TestPiModelListsExtensionRefusesAModelOutsideTheList`; and on `openai-codex`, whose list reaches pi through `yolo-openai-auth.js`'s registration of the subscription login ([MM-D14](#MM-D14)), the same day, once the login's path through a wrapper was MEASURED too (the first measurement covered a bearer token, SigV4 keys and a models.json row's key), `TestPiOpenAIAuthExtensionRefusesAModelOutsideTheCodexList` ([MM-D23](#MM-D23)) |
| <a id="MM-D7"></a>MM-D7 | *Implementation decision.* **opencode: `provider.<id>.whitelist` is the list's ids, each also declared under `provider.<id>.models`**, beside today's `enabled_providers`, wherever the menu is to be exact (a whole-universe list or an `only`, [§14.1](#141-the-table)) and the switch is on ([MM-D5](#MM-D5)). **A model's display `name` is the entry's own `name` fact, or absent so the agent's catalog name shows, never its yolo alias**, which today shows an entry under `default` as "default". The rule is stated once for every catalog-writing derive: opencode's `provider.<id>.models`, pi's `models.json` rows and registration ([MM-D6](#MM-D6)), and oh-omp's `models.yml` rows ([MM-D8](#MM-D8)), which today use the alias the same way. opencode's order, newest first, is accepted; yolo writes no made-up `release_date` | 2026-09-29 | [§14.2](#142-what-each-row-rests-on) | ✅ display names, `TestNoCatalogRowIsNamedAfterItsAlias`; the whitelist under an `only`, `TestOpencodeWhitelistsANarrowedList`; the selection on the list's default entry under an `only` that dropped the profile's model, `TestOpencodeStartsOnANarrowedListsDefault`; the whitelist on opencode's own Bedrock row, `TestOpencodeWhitelistsANarrowedListOnItsOwnBedrockClient`; a whole-universe list's whitelist waits on [OQ-MM3](#OQ-MM3) |
| <a id="MM-D8"></a>MM-D8 | *Implementation decision.* **oh-omp gets a settings surface, `~/.oh-omp/agent/config.yml`, writing `enabledModels` as the list's exact ids**, and `modelRoles.default` through the selection namespace, the way pi's `defaultModel` is written. Its `models.yml` rows take [MM-D7](#MM-D7)'s display-name rule. A scope naming ids omp does not know fails open to its full list; yolo cannot see that at render time, and it is the case [OQ-BR14](#16-decision-ledger)'s `yolo check` warning exists for. On 0.15.x a current Bedrock list exists only as the via row under yolo's `bedrock` key, so omp's native Bedrock client gets no list. `packs/omp` stays on its pinned fork here; whether it should move to upstream oh-my-pi, which appears to keep runtime registrations, is that pack's question | 2026-09-29 | [§14.2](#142-what-each-row-rests-on) | ✅ `TestOmpScopesANarrowedList`, with no `modelRoles.default` write ([MM-D12](#MM-D12)) |
| <a id="MM-D9"></a>MM-D9 | *Implementation decision.* **codex gets an exact menu from a catalog yolo filters at prelaunch.** Before codex starts (it installs lazily, so not at boot), the launcher reads `codex debug models --bundled`, which its help describes as *"Skip refresh and dump only the bundled catalog shipped with this binary"*, so offline for the OpenAI catalog (SOURCED). For Bedrock runtime this waits on a measurement ([§14.4](#144-build-order)): `--bundled`, with that provider selected, must print the Bedrock runtime catalog (`amazon_bedrock/catalog.rs`), not the OpenAI one, offline and with no credential. The launcher keeps the list's ids; renumbers `priority` in list order; applies each entry's name as `display_name`; clears `upgrade` and `availability_nux`; and writes a per-launch file that `model_catalog_json` names, through the selection. **An id the installed catalog lacks is left out, with a warning like [ML-D7](#ML-D7)'s**, never written without instructions, since that runs codex with empty system instructions. Only providers whose ids codex's own catalog carries get a file (`openai-codex`, and Bedrock runtime once that measurement holds); a custom provider keeps `model` alone until an entry safe for a non-OpenAI model is measured. When the bridge refuses on a via route, the derive points `review_model` and the memories models at listed ids. It is the first step where a program's output feeds a render, so it lives in the launcher, not a derive, whose sandbox has no `io`. It freezes the subscription's live list for the session, as claude and pi already do for `openai-codex` | 2026-09-29 | [§14.2](#142-what-each-row-rests-on) | ✅ 2026-09-30 for `openai-codex`, as [MM-D22](#MM-D22) builds it: `TestCodexLauncherHandsCodexItsModelMenu`, `TestCodexModelListIsTheSubscriptionsListOnly`. The Bedrock runtime half is not built: its measurement did not hold, since `--bundled` prints OpenAI's catalog whatever the provider ([§14.4](#144-build-order)). The `review_model` and memories pins wait on a via route, which `openai-codex` has none of. (The selection under an `only`, `TestCodexSelectsANarrowedListsDefault`) |
| <a id="MM-D10"></a>MM-D10 | *Implementation decision.* **copilot's whole list goes in a `providers.json` file named by `COPILOT_PROVIDERS_CONFIG`**: one provider, the endpoint copilot's env derive picks today with its `anthropic` or `openai` type (the bridge's on a bridged provider, the provider's own on a direct one, [§14.2](#142-what-each-row-rests-on)), and one model row per entry with `wireModel` the wire-true id. Its key is the value `COPILOT_PROVIDER_API_KEY` carries today: the launch's caller token on a bridged provider, and the provider's own key, such as z.ai's, on a direct one. **Where the file can name its key by environment variable**, it is a secret-free `copilot/providers` surface rendered by `yolo.derive("copilot", "providers")`. **Where it cannot**, it is written host-side by the per-agent env writer, beside `agent-env/copilot.sh`, with the same mode and lifetime, from the env derive that already holds the key, since a surface derive never receives one ([OQ-MM2](#16-decision-ledger)). It needs a copilot that has `providers.json`, which 1.0.89 has and 1.0.48 lacks, and four measurements first ([§14.4](#144-build-order)). Until then copilot keeps one `COPILOT_MODEL`. A `model` default for copilot, if ever written, belongs in `~/.copilot/settings.json`, where copilot has kept user settings since 1.0.35, moving any it finds in `config.json` | 2026-09-29 | [§14.2](#142-what-each-row-rests-on) | ✅ **built 2026-10-05** as [MM-D31](#MM-D31), its key branch as [MM-D33](#MM-D33), after [OQ-MM4](#16-decision-ledger)'s ruling. Before it, **stopped 2026-09-30** on its measurement: the file's models join GitHub's own ([§14.4](#144-build-order)), which was [OQ-MM4](#16-decision-ledger)'s to rule (`COPILOT_MODEL` under an `only`, `TestCopilotStartsOnANarrowedListsDefault`; the `settings.json` home for a copilot setting, [MM-D15](#MM-D15)) |
| <a id="MM-D11"></a>MM-D11 | *Implementation decision.* **The `models` kind composes in two passes inside `packload.ComposeProviders`, between the packs' own providers and the user's entries.** Every `add` applies, in pack order then declaration order, and then every `only`, the `only`s intersecting. Two passes because the literal "packs apply in `packs` order" of [§7.2](#72-composition-rules) would let an `add` from a pack listed after a company's `only` re-open it. An added entry lands under its own id, its facts in `model_options` in the flat vocabulary a user's object-form entry lowers into, plus `vendor`, `description` and `order`, and its `alias` beside it; a duplicate id or alias keeps the first writer's, and an `only` id nothing added is dropped. For a provider some contribution names, the list's current order is first written down as `order` facts, which added entries continue, because every consumer sorts entries with an `order` ahead of those without; a provider no contribution names composes byte-identically. A narrowed entry carries `models_only: true` (`packload.ModelsOnlyKey`), a composed fact no user key can spell, and it is what each derive renders an exact menu from. The user's `providers.<name>.models` composes last PER ALIAS rather than replacing the list, as [§7.2](#72-composition-rules)'s letter says: replacing would end [ML-D4](#ML-D4)'s one-alias recovery and change how every string-form map composes today. A provider only the user declares is shaped too, and one nobody declares is inert. `yolo check` names what the pass could not do (`packload.WithModelNotes`) | 2026-09-29 | [§7.2](#72-composition-rules) | ✅ `packload.applyModelContributions`, pinned by `internal/packload/modellists_test.go`; an `only` dropping two adjacent entries, which kept the second, fixed and pinned by `TestAModelsOnlyDropsAdjacentEntries` |
| <a id="MM-D12"></a>MM-D12 | *Implementation decision.* **oh-omp's start needs no `modelRoles.default` write, revising [MM-D8](#MM-D8).** The selection namespace lifts top-level scalars and arrays only, and `modelRoles.default` is a key of a record. With a scope and no `--model`, oh-omp 0.15.3 starts on the model its own `modelRoles.default` remembers when that is in the scope, else on the scope's first entry (read in the shipped binary's startup code, not run). So an `enabledModels` scope that leads with the default entry makes every start valid without steering a valid saved choice, which is [OQ-ML2](#16-decision-ledger)'s ruling | 2026-09-29 | [§14.2](#142-what-each-row-rests-on) | ✅ `TestOmpScopesANarrowedList`; the default-first scope pinned where the default is not first in list order, `TestOmpScopeLeadsWithADefaultNotFirstInOrder` |
| <a id="MM-D13"></a>MM-D13 | *Implementation decision.* **The switch is spelled `enforce_models`, a profile FIELD that defaults on** (the name is coined here). A field like `via`, not a provider option, so no provider's option census has to admit it: it is a fact about the selection. It is a boolean in user config (a string is refused), and a `kind: "profile"` contribution may carry it; the user's value wins over a pack's. It crosses in `YOLO_PROFILES` under the reserved `_enforce_models` key only when a profile states it, and a derive reads `ctx.enforce_models`, true unless the profile says false, so an entrypoint older than the key hands its derives the default. `pin_model`, [MM-D3](#MM-D3)'s provisional name, is kept, and declared with no default on the shipped providers that declare options (zai, cerebras, llamacpp, kilo, openrouter), so their census admits it | 2026-09-29 | [§14.1](#141-the-table) | ✅ `TestAProfileEnforceModelsIsABooleanField`, `TestTheModelEnforcementSwitchReachesBothDerivePaths`, `TestClaudeUnderAnOnlyOnARoutedProvider` |
| <a id="MM-D14"></a>MM-D14 | *Implementation decision.* **pi's narrowed lists travel in a second data file, `pi/model-lists` (`~/.pi/agent/yolo-model-lists.json`), keyed by pi's provider id and registered by a second extension, `yolo-model-lists.js`,** rather than in one generalized file, as [MM-D6](#MM-D6)'s letter says. The `openai-codex` registration also carries the subscription login, so it stays in `yolo-openai-auth.js` and `pi/codex-models`, where a narrowed `openai-codex` list already arrives through the composed table. The file holds only providers pi can reach: one with a models.json row, the via row, or yolo's Bedrock provider as pi's `amazon-bedrock`. pi refuses a registration for a provider it has no address or credential for, and that would fail the extension's whole load | 2026-09-29 | [§14.2](#142-what-each-row-rests-on) | ✅ `TestPiGetsANarrowedListToRegister`, `TestPiRegistersNoNarrowedListItCannotUse` |
| <a id="MM-D15"></a>MM-D15 | *Implementation decision.* **copilot's footer default moves to a second read-modify-write surface, `copilot/settings` at `~/.copilot/settings.json`; `copilot/config` keeps `config.json` and its `yolo: true` default.** copilot 1.0.48's migration moves only the keys of its settings schema out of `config.json`, and `yolo` is not one of them (read in `app.js`), so it stays; keeping the surface keeps its token-preserving read-modify-write behavior unchanged | 2026-09-29 | [§14.2](#142-what-each-row-rests-on) | ✅ `TestCopilotStatusLineDefaultLandsInSettingsJSON` |
| <a id="MM-D16"></a>MM-D16 | *Implementation decision*, under [OQ-ML1](#16-decision-ledger) and [OQ-ML2](#16-decision-ledger). **A list stays current through the agents' own catalogs and the user's own config, and `yolo check` warns about a listed id that no installed agent's catalog knows** ([OQ-BR14](#16-decision-ledger)'s option A). The check is `yolo check`'s alone: a launch makes no model-list network call. An unknown id is a warning, never a refusal. When no catalog could be read the check says it could not ask, a different answer from "not found". There is no "a newer model exists" report, because no catalog names a family. **Why:** the 2026-09-29 rulings make each provider's default one simple pick the user changes, so yolo does not chase the newest model, which rules out a launch-time control-plane call (B) and a catalog snapshot in yolo (C), and [providers.md](../reference/providers.md#what-this-does-not-license) forbids discovery. Of the two left, A over D because under D a retired id surfaces as a 404 at the first request, while [OQ-ML2](#16-decision-ledger) asks that a session start on a valid model; the warning is the cheapest way to say so first. Reversible: dropping the warning is D. *Amended for Bedrock on 2026-10-05 by [OQ-MM6](#16-decision-ledger): where no pack supplies a Bedrock list, the `aws-auth` daemon reads the region's list on the host, cached once a day* | 2026-09-30 | [§9](#9-currency-the-agents-catalogs-plus-a-staleness-warning) | ✅ 2026-09-30: `modelCatalogReport` (`internal/cli/check/modellists.go`), called from the Packs section beside the `models` kind's notes, over the catalogs [MM-D19](#MM-D19) reads and the lists [MM-D20](#MM-D20) names; `TestCheckWarnsAboutAListedIDNoInstalledCatalogKnows`, `TestCheckSaysItCouldNotAskWhenNoCatalogIsInstalled`, `TestCheckPassesWhenEveryListedIDIsKnown`, `TestCheckReadsTheHostFloorsCopyAtTheHost` and `TestCheckReadsPisCatalogThroughTheShippedDeclaration` (`internal/cli/check/modelcatalog_test.go`) drive the section and each fails with the call removed |
| <a id="MM-D19"></a>MM-D19 | *Implementation decision, building [MM-D16](#MM-D16).* **An agent's catalog is the JSON files its pack names in a new `model_catalog` field of an npm `program`, read where the agent is installed, and nothing is run to learn it.** Each entry is a glob relative to the installed package's directory; every string an object holds under an `id` key, at any depth, is a known id; an id counts as known when any catalog read names it, under any provider. The directory is the jail's npm prefix in a jail, and at the host yolo's floor copy (`hostfloor.Record.NpmPackageDir`) when the floor's disposition (`hostfloor.Floor.Status`, the answer the Host agent floor section prints) says it is the copy `yolo host --` runs. A program with no floor entry runs from the launch's PATH, so a copy the floor still holds for it is never read. An agent not installed there, one with no floor entry, or a release whose files the glob does not match, is a catalog the check could not ask. **Why:** core may not know where an agent keeps its catalog ("core does not know what an agent is"), so the pack declares it, and only its pack can keep it current. Reading files keeps `yolo check` an observe verb: the alternative, running `codex debug models --bundled` or pi's own model listing, starts an agent program and, behind a lazy launcher, would install one. The cost is coverage: of the agents installed in this jail only pi's catalog is a data file (pi-ai's `dist/providers/data/*.json` in 0.99.1, MEASURED; codex 0.145.0, copilot 1.0.48 and opencode 1.18.32 ship none, and opencode's and oh-omp's are built into their binaries, [§14.2](#142-what-each-row-rests-on)), so today the check reads pi alone. A union rather than a per-provider lookup because the catalogs key providers by the agent's own ids (pi's `amazon-bedrock` for yolo's `bedrock`), a mapping core would have to learn; a union can only miss a warning, never invent one | 2026-09-30 | [§9](#9-currency-the-agents-catalogs-plus-a-staleness-warning) | ✅ 2026-09-30: `packdecl` (`Contribution.ModelCatalog`, its validation, `Install.ModelCatalog`), `internal/modelcatalog` (`Read`), `packs/pi`'s declaration; `internal/packdecl/modelcatalog_test.go`, `internal/modelcatalog/modelcatalog_test.go`, `internal/hostfloor/npmpackagedir_test.go`. Found in review the same day: the host read took any record the prefix held, so with `host_floor` leaving pi out it checked against a copy `yolo host` never runs; it now asks the disposition (`TestCheckDoesNotReadAFloorCopyYoloHostDoesNotRun`). And in a jail it said there was nowhere to look when `NPM_CONFIG_PREFIX` was unset, which the macos-user sandbox's environment list never sets; it now resolves the prefix by the entrypoint's one rule, `$HOME/.npm-global` by default, as every generated launcher installs (`TestCheckReadsTheJailsDefaultNpmPrefix`) |
| <a id="MM-D20"></a>MM-D20 | *Implementation decision, building [MM-D16](#MM-D16).* **Every provider in the composed table is checked except one every declared endpoint of which is on this machine (`localhost`, a loopback address, `host.containers.internal`); an unknown id is one warning per provider, no catalog read is one skip, and a clean check is one pass naming what it read.** Every provider, the shipped lists included, because [OQ-BR14](#16-decision-ledger)'s premise is that the warning keeps yolo's own ids honest. A local server's ids are what that server serves, which no vendor catalog lists, so checking `packs/llamacpp`'s `llama` would warn on every run and train a reader to skip the badge. A skip rather than a warning or silence when nothing could be read, [MM-D16](#MM-D16)'s "could not ask": a warning would be a finding there is none of, and silence would read as a pass | 2026-09-30 | [§9](#9-currency-the-agents-catalogs-plus-a-staleness-warning) | ✅ 2026-09-30: `listedModelIDs` and `onlyLocalEndpoints`; the local provider in `TestCheckWarnsAboutAListedIDNoInstalledCatalogKnows` draws no warning |
| <a id="MM-D17"></a>MM-D17 | *Implementation decision*, under [OQ-XM2](../reference/providers.md#oq-xm2). **claude's derive reads the conventional aliases for its tiers: `balanced` pins the Sonnet tier, `fast` the Haiku tier, and `frontier` the Opus tier wherever a tier pin reads an alias ([MM-D2](#MM-D2)'s pins); `sonnet`, `haiku` and `opus` stay as synonyms, and a vendor name wins where a provider declares both** ([OQ-PSW1](#16-decision-ledger), moved with synonyms). On a routed provider the Opus pin stays the selected model, as today. **Why:** [OQ-XM2](../reference/providers.md#oq-xm2) ruled the vocabulary yolo publishes and that each adapter maps it onto its agent's own names, and [XM-D2](../reference/providers.md#XM-D2) warns every provider that lacks one of the four, so providers will declare them; a claude derive that reads only vendor names would leave its Sonnet and Haiku tiers on the default for such a provider. The vendor name wins because whoever wrote `sonnet` wrote it for claude. The synonyms keep every existing config working. Reversible: claude's tier names are read in one table (`claudeTiers`) and one routed branch | 2026-09-30 | [§6](#6-tier-aliases-default-fast-balanced) | ✅ 2026-09-30: each tier's names in `claudeTiers` (`packs/claude/derive.lua`), read by `tierAlias` in all four tier reads: the `openai-codex` list's pins, the pins under an `only`, the routed fable pin, and the provider branch's Sonnet and Haiku pins. `TestClaudeRoutedTiersReadTheConventionalAliases`, `TestClaudeTierVendorNameWinsOverTheConventionalAlias`, `TestClaudeNativeBedrockTiersReadTheConventionalAliases` and `TestClaudeUnderAnOnlyReadsTheConventionalAliases` (`internal/entrypoint/claudetieraliases_test.go`), and on the `openai-codex` list `TestAnAliasForADeclaredCodexIDChangesNoConsumer`, whose `fast` alias now moves claude's Haiku tier; all but the vendor-name cell fail with the conventional names dropped from the table |
| <a id="MM-D18"></a>MM-D18 | *Implementation decision, building [MM-D17](#MM-D17).* **A tier takes the first of its names that the provider declares for a model claude's client can call; a name it cannot call gives way to the tier's next name.** On claude's own Bedrock client, a `sonnet` naming another maker's model (Bedrock's Messages API serves Claude alone, [OQ-BR9](bedrock-plumbing.md#decision-ledger)) no longer leaves the Sonnet tier on the default when `balanced` names a Claude model; under an `only`, a name outside the narrowed list gives way the same way. The letter of MM-D17, "a vendor name wins where a provider declares both", is kept wherever both names are usable. Chosen over letting the unusable vendor name win and falling to the default, because the default is what MM-D17 exists to stop a tier landing on when the provider named a model for it. One helper, `tierAlias`, answers every tier read | 2026-09-30 | [§6](#6-tier-aliases-default-fast-balanced) | ✅ 2026-09-30: `TestClaudeNativeBedrockTiersReadTheConventionalAliases` (its `sonnet` names a Moonshot model and its `balanced` a Claude one) |
| <a id="MM-D21"></a>MM-D21 | *Implementation decision, building [MM-D6](#MM-D6)'s refusing form on the 2026-09-30 measurement ([§14.4](#144-build-order)).* **The wrapper hands a listed model to the stream pi would have run it on, rebuilt from what pi exports: the built-in provider of that id when it serves the model's api, else pi's api registry, with pi's options untouched.** That is what pi 0.99.1's `composeModelProvider` does for a registration without a `streamSimple`, and pi resolves the credential into those options before it calls the wrapper, so the credential needs no handling of yolo's (MEASURED for a bearer token, SigV4 keys and a models.json row's `${VAR}` key). **The registration names one api**: the derive states the api of the models.json row it writes for the provider, and for pi's own Bedrock client, which has no row, the extension takes the api pi's catalog gives the listed ids. **A list whose models run on two pi apis, or whose stream cannot be found, is registered as the menu alone, and pi warns once that it cannot refuse there.** Since 2026-11-10 that menu keeps another extension's rows for the provider, as an unenforced list does ([ML-D15](#ML-D15)). pi hands a wrapper only the models of its registration's api, and pi's `--model` fallback copies a listed model of either api, so a wrapper on one would refuse only part of what `--model` reaches, which reads as enforcement and is not. No list the derive renders reaches that branch today: every provider but pi's own Bedrock client carries its models.json row's one api, OpenRouter's included although pi's own OpenRouter catalog mixes apis, and pi's Bedrock catalog is on one api (`TestPiNarrowedOpenRouterListNamesItsRowsOneAPI`), so it guards a pi whose catalog or exports change. ⚠ **Superseded in part 2026-10-05 by [OQ-3](pi-codex-provider-shadowing.md#10-decision-ledger)'s build:** a provider pi has built in (OpenRouter, zai, cerebras) has no models.json row, so its list names no api and the extension takes each listed model's from pi's own catalog. OpenRouter's narrowed list mixing Anthropic models with others now reaches the two-api branch, the menu alone and no refusal (the same test pins it, beside kilo, which keeps its row's one api); whether the ruling reaches such a list at all is [OQ-4](pi-codex-provider-shadowing.md#OQ-4). **Each list carries the switch of the profile that governs it**, the active-set entry's for its provider, else the primary's, the rule opencode's whitelist already follows, so one profile's `enforce_models` means the same in both agents. The refusal names the model, the list and `"enforce_models": false`, as the bridge's does. **The registered ids are the ids pi's models.json row sends**, since the registration replaces that row's list and the refusal compares against it: on Kilo a bare `deepseek-…` id is spelled `deepseek/deepseek-…`, as the row and the selection spell it (`normalizeKiloModel`) | 2026-09-30 | [§14.2](#142-what-each-row-rests-on) | ✅ 2026-09-30: `yolo.derive("pi", "model-lists")` (`enforce` from `piEnforceFor`, `api` from the row) and `packs/pi/home/.pi/agent/extensions/yolo-model-lists.js` (`listApi`, `delegateFor`); `TestPiModelListsExtensionRefusesAModelOutsideTheList`, `TestPiModelListsExtensionDelegatesAProviderPiDoesNotShipToItsAPIRegistry`, `TestPiModelListsExtensionSaysWhenAListSpanningTwoAPIsCannotBeRefused`, `TestPiModelListsExtensionReadsTheBedrockAPIFromPisCatalog`, `TestPiNarrowedListCarriesItsProfilesModelSwitch`, `TestPiNarrowedKiloListRegistersTheIdsItsRowSends`, `TestPiNarrowedOpenRouterListNamesItsRowsOneAPI` |
| <a id="MM-D22"></a>MM-D22 | *Implementation decision, building [MM-D9](#MM-D9) on the 2026-09-30 measurement ([§14.4](#144-build-order)).* **The launcher names codex's menu with `-c model_catalog_json=<file>` for the run it wrote the file for, never from `config.toml` through the selection, as MM-D9's letter says.** codex refuses to load a config whose `model_catalog_json` names no file, and a key the boot renders would name the file for every codex start: one the launcher never ran for, one whose menu could not be written (codex too old to print its catalog, a list none of whose ids it knows), and a `codex` resolved past the launcher. The flag is added only after the file exists, and `-c` is global and later wins, so a user's own `-c model_catalog_json` still does. **The step is a pack declaration, `model_menu` on a `program` (`packdecl.ModelMenu`, coined 2026-09-30), and the projection is Go, `yolo internal model-menu` (`internal/modelmenu`)**: the argv that prints the catalog, the list and menu paths, the flag, and the catalog's entry, id, order and name keys and the keys to clear and set are packs/codex's words, so both shared launcher templates carry one step keyed on no bin, and the shell only reads the flag words, NUL-separated. **The menu is rebuilt only when codex's binary, the list or the declaration changed** (a key file beside it), so a launch runs `codex debug models --bundled` once per change, not per start. The key file also records the listed ids the menu left out, so every launch that reuses the menu repeats [MM-D9](#MM-D9)'s warning for them, not only the one that rebuilt it (`TestRunSaysAtEveryLaunchWhichListedIDTheMenuLeavesOut`). **`YOLO_NO_LAUNCH_FLAGS=1` skips it**, since its words are a pack's flag. **At the host notch nothing is built yet, which leaves [NC-D1](../plans/notch-convergence.md#7-decision-ledger)'s convergence owed here.** Not for want of a place: `yolo host --` already rewrites codex's argv (the pack's launch flags, a managed launch's `--no-daemon`), and a subscription launch runs in a yolo-owned `CODEX_HOME` whose config yolo rewrites at every launch (`openaiauthhost.prepareCodexHome`), where a menu file could go. What is missing is the list: it is a surface, and a host render writes a surface for the configured selection, never a launch's `-p` ([OQ-HC3](../reference/host-agent-environment.md#oq-hc3)), so a list `yolo host apply` wrote cannot tell a `yolo host -p codex` launch from a `-p openrouter` one, and a menu built from it would hand OpenAI's models to codex on another provider. Building it takes the list composed for the launch itself. Until then the list surface is `notAtHost` and host codex keeps its own menu | 2026-09-30 | [§14.2](#142-what-each-row-rests-on) | ✅ 2026-09-30: `packs/codex/pack.json` (`model_menu`, the `codex/model-list` surface), `yolo.derive("codex", "model-list")`, `modelMenuShellFn` in both launcher templates, `modelmenu.Run`; `TestCodexLauncherHandsCodexItsModelMenu`, `TestCodexLauncherAddsNoMenuWhereThereIsNone`, `TestTheNpmLauncherHandsItsProgramAModelMenuToo`, `TestYoloInternalModelMenuRunsTheMenuAgainstHome`, `TestRunReusesTheMenuUntilTheListOrTheProgramChanges`. The host half: designed and built 2026-09-30 ([§14.7](#147-codexs-menu-at-yolo-host-designed-2026-09-30), [§14.8](#148-what-was-built-at-the-host-2026-09-30), [MM-D24](#MM-D24) to [MM-D28](#MM-D28)); that design found this row's "list composed for the launch" right only where the launch's `-p` and the configured profile select the same provider, since a host `-p` does not choose codex's provider ([OQ-MM5](#16-decision-ledger)), so the host builds a menu only there |
| <a id="MM-D23"></a>MM-D23 | *Implementation decision, building [MM-D6](#MM-D6)'s refusing form on `openai-codex` on the 2026-09-30 measurement of the subscription login ([§14.4](#144-build-order)).* **The wrapper goes in `yolo-openai-auth.js`'s own registration, the one that carries the login: with a list and `enforce` on, it adds a `streamSimple` that throws yolo's refusal for an id off the list and hands a listed one, options untouched, to pi's built-in `openai-codex` provider, else pi's api registry for `openai-codex-responses`.** The login needs no handling of yolo's: pi composes the registration's `oauth` as the provider's auth whatever else it carries, and turns the stored credential into the bearer token before it calls the wrapper, refreshing it through the registration's own `refreshToken` first when fewer than five minutes remain, and pi's own stream reads the account id out of that token (MEASURED). One registration states the list, the login and the refusal together, so the refusal can only compare against the list it registers. A second `openai-codex` registration from `yolo-model-lists.js` was the alternative, rejected because pi merges a re-registration's defined keys over the earlier one (`ModelRuntime.registerProvider`), so the refused list and the registered one would come from two files in whichever order pi loads them; [MM-D14](#MM-D14) kept the list in this registration for the login's sake already. **It refuses on the declared list whether or not an `only` narrowed it**, as claude's allowlist does on `openai-codex` ([MM-D1](#MM-D1)): the registration replaces pi's whole `openai-codex` catalog ([ML-D3](#ML-D3)), so the list is pi's exact menu for the provider either way, and every option of [OQ-MM3](#OQ-MM3) refuses there. **Its switch is `piEnforceFor`'s**, the rule [MM-D21](#MM-D21) reads for pi's other lists: the active-set entry's on `openai-codex`, else the primary profile's, else the default, on. So a launch that selects no profile refuses too, since pi registers the provider on every launch and can switch to it after a `/login`; `pi/codex-models` carries the switch as `enforce`, beside the list. **A stream that cannot be found registers the exact menu alone and warns once**, as [MM-D21](#MM-D21)'s does, rather than a wrapper with nowhere to hand a listed model. **The refusal's words are one text in both extensions**, copied rather than shared: pi loads every `.js` file in its extensions directory as an extension and reports one that exports no factory as a load error (`discoverExtensionsInDir`, `dist/core/extensions/loader.js`), so a shared module would be one more file delivered elsewhere for one sentence, and a test keeps the copies identical | 2026-09-30 | [§14.2](#142-what-each-row-rests-on) | ✅ 2026-09-30: `yolo.derive("pi", "codex-models")` (`enforce` from `piEnforceFor`) and `packs/pi/home/.pi/agent/extensions/yolo-openai-auth.js` (`codexDelegate`, the registration's `streamSimple`); `TestPiOpenAIAuthExtensionRefusesAModelOutsideTheCodexList`, `TestPiOpenAIAuthExtensionRefusesWithNoProfileSelected`, `TestPiCodexListTakesTheSwitchOfTheActiveSetEntryOnOpenAICodex`, `TestPiOpenAIAuthExtensionRefusesNothingWithTheSwitchOff`, `TestPiOpenAIAuthExtensionRefusesNothingWithoutAList`, `TestPiOpenAIAuthExtensionDelegatesToPisAPIRegistryWithoutABuiltIn`, `TestPiOpenAIAuthExtensionSaysWhenItCannotRefuse`, `TestPisTwoRefusalsAreWordedAlike`; in a real `-p codex` launch, `TestCodexProfileRendersOneModelListForEveryAgent` |
| <a id="MM-D24"></a>MM-D24 | *Implementation decision, designing [MM-D22](#MM-D22)'s host half ([§14.7](#147-codexs-menu-at-yolo-host-designed-2026-09-30)).* **At `yolo host` the list is composed by the launch: `yolo host --` runs the program's pack's list derive, the one registered on the surface whose path its `model_menu.list` names (`codex/model-list`), in-process over the launch's own wire tables (`hostComposition.wireTables`, [FT-D2](../reference/agent-footer.md#FT-D2)), and hands the answer to the menu step. No file is rendered, and the surface stays `notAtHost`.** The derive and the wire readers are the ones a jail's boot runs ([HC-D13](../reference/host-agent-environment.md#HC-D13)), so the list keeps one declaration ([ML-D1](#ML-D1)) and one code path ([NC-D1](../plans/notch-convergence.md#7-decision-ledger)); the notch changes only where the tables come from. A file `yolo host apply` rendered was the alternative, and fails twice: it is written for the configured profile alone ([OQ-HC3](../reference/host-agent-environment.md#oq-hc3)), and it can be older than the launch, since only a wrapped launch with `host_apply_on_launch` on renders first. The surface's `notAtHost` reason, which says `yolo host --` runs codex with no launcher, is reworded when this is built | 2026-09-30 | [§14.7](#147-codexs-menu-at-yolo-host-designed-2026-09-30) | ✅ 2026-09-30: `entrypoint.HostModelList`, called from `hostComposition.modelMenu`, and the `notAtHost` reason reworded; `TestHostModelListIsTheLaunchsListAndWritesNothing`, `TestHostModelListNamesADeclarationNoSurfaceWrites`, `TestHostCodexMenuFollowsTheLaunchsListAndLeadsThePacksFlags` |
| <a id="MM-D25"></a>MM-D25 | *Implementation decision, built around [OQ-MM5](#16-decision-ledger) ([§14.7](#147-codexs-menu-at-yolo-host-designed-2026-09-30))*, **SUPERSEDED 2026-10-05** by [MM-D30](#MM-D30), which decided [OQ-MM5](#16-decision-ledger): a `-p` now moves codex onto its provider and the menu follows it, and this rule holds only for a program whose pack declares no launch selection. As built 2026-09-30: **The host builds a menu only when the provider the launch selected for the program is the provider its configured profile selects: no `-p`, or a `-p` over the same provider as the program's `profile` entry.** Otherwise it adds no flag and prints one line saying that the `-p` does not choose codex's provider at the host, so codex keeps its own menu. Why: a host `-p` reaches codex's environment but not the config file that decides its provider, which only `yolo host apply` writes, for the configured profile (read at `6068e889`), so a list for a `-p` over another provider could hand OpenAI's models to codex on z.ai, the fault [MM-D22](#MM-D22) exists to prevent. When the two agree, every option of [OQ-MM5](#16-decision-ledger) builds this very menu, so this much can be built before the ruling. A launch through a generated wrapper carries no `-p`, so it always agrees; a config file older than the configuration is the staleness `host_apply_on_launch` re-renders. The comparison is of providers, not profile names, because the list derive reads the provider | 2026-09-30 | [§14.7](#147-codexs-menu-at-yolo-host-designed-2026-09-30) | ✅ 2026-09-30: `hostComposition.menuProviderDisagreement` over `configuredProfile` (both deleted 2026-10-05; [MM-D30](#MM-D30)'s `menuFollowsTheLaunch` keeps the rule for a pack declaring no launch selection), with the line [MM-D28](#MM-D28) words; `TestHostCodexWithAPOverAnotherProviderGetsNoMenu` (inverted 2026-10-05 by [MM-D30](#MM-D30) as `TestHostCodexMovesOntoAPOverAnotherProviderAndGetsItsMenu`) |
| <a id="MM-D26"></a>MM-D26 | *Implementation decision ([§14.7](#147-codexs-menu-at-yolo-host-designed-2026-09-30)).* **The host step is the jail's: `internal/modelmenu`'s build, split so the list is an argument, which the jail's `yolo internal model-menu` reads from its file and `yolo host --` passes from [MM-D24](#MM-D24).** The catalog run, the projection, the cache key and the missing-id warning stay one implementation. `yolo host --` runs it after the binary resolves and the pack's flags are added, against the resolved target, so the catalog is the program that runs (the floor's copy where there is one). The flag goes right after `argv[0]`, ahead of the pack's flags and the user's own argv, where the jail's launcher puts it, so a user's own `-c model_catalog_json` still wins, `-c` being global and last-wins ([§14.4](#144-build-order)). The managed launch's `--no-daemon` rewrite still runs last, as today. `YOLO_NO_LAUNCH_FLAGS=1` skips it, as in a jail. It is disclosed with the pack's flags (`LaunchInjection.DisclosureLines`), because a launch has no quiet mode ([OQ-RO3](../reference/report-tiers.md#why-its-this-way)), and the missing-id warning prints at every launch whose menu lacks an id ([MM-D22](#MM-D22)). Nothing in it can refuse the launch: every failure is a warning, and codex keeps its own menu. `yolo host env`, which launches nothing, builds none | 2026-09-30 | [§14.7](#147-codexs-menu-at-yolo-host-designed-2026-09-30) | ✅ 2026-09-30: `modelmenu.Request`, which the jail's `modelmenu.Run` builds through too, `hostMenu.rewrite` and `LaunchInjection.Why`; `TestHostCodexOnTheConfiguredSubscriptionGetsYolosMenu`, `TestHostCodexGetsNoMenuWhereThereIsNone`, `TestHostManagedCodexHoldsTheMenusLockWhileItRuns` |
| <a id="MM-D27"></a>MM-D27 | *Implementation decision ([§14.7](#147-codexs-menu-at-yolo-host-designed-2026-09-30)).* **At the host the menu lives in yolo's machine state, never in the user's real `~/.codex`: one directory per pack and program, one file per cache key, and a lock each running program holds for its whole life.** codex reads the file again at every thread start (SOURCED, 0.159.2), and two host launches of one program may hold different lists at once, one with a company's `only` and one without, or one with no list at all. A fixed path, the jail's, would let one launch replace or remove the menu another codex still reads, and a codex whose menu is gone fails its next `/new`. So each menu is named by its cache key, the hash of the declaration, the list and the program's binary by which [MM-D22](#MM-D22) already decides to reuse a menu, and is never overwritten. Every launch that names a menu holds the directory's lock shared for its program's life, and one that writes a new menu first removes the others, only when it can take that lock exclusively, that is when no program holding a menu of the directory is running: collected by liveness, never by age, and a lock it cannot take removes nothing. That is the exclusive-then-shared pattern by which the managed `CODEX_HOME`'s live-launch lock already tells a launch it is alone (`sharedCallerToken`). The lock is held by the resident `yolo host` process where it stays (a managed launch, a launch with services), and on the exec path by the program itself: Go opens files close-on-exec, so the step clears that flag on the lock's descriptor before the exec. The directory is not the managed `CODEX_HOME`, because a launch without yolo's login has none, and core cannot know that `CODEX_HOME` is codex's. `yolo stores` lists it as yolo's. In a jail nothing changes: every launch of one jail reads the list its boot rendered, so one path serves them all | 2026-09-30 | [§14.7](#147-codexs-menu-at-yolo-host-designed-2026-09-30) | ✅ 2026-09-30: `modelmenu.Request.WriteIn`, `Held.KeepAcrossExec` and `paths.HostModelMenusDir`; `TestWriteInKeepsEveryMenuARunningProgramHolds`, `TestWriteInCollectsMenusOnlyWhenNoProgramHoldsOne`, `TestHeldKeepsItsLockAcrossTheExec`, `TestHostModelMenusRowIsYolosOwn` |
| <a id="MM-D28"></a>MM-D28 | *Implementation decision, building [MM-D24](#MM-D24) to [MM-D27](#MM-D27) ([§14.8](#148-what-was-built-at-the-host-2026-09-30)).* **The choices the host build made that the design left open.** (1) **[MM-D25](#MM-D25)'s line is printed whenever the two providers differ**, whether or not the `-p`'s provider has a list, since what it reports, that a host `-p` does not choose codex's provider, holds either way, and a launch has no quiet mode ([OQ-RO3](../reference/report-tiers.md#why-its-this-way)). The configured profile is the fold of the user-scope `profile` with no `-p`, over the launch's own selected packs (`hostProfileFold`), its first entry, as the launch's is. (2) **The step runs before the OpenAI prelaunch**, so it depends on nothing the managed `CODEX_HOME` holds, and its catalog run gets the environment the launch composed for codex (`childEnviron`), as the jail's runs in its launcher's; `codex debug models --bundled` reads no config and no credential ([§14.4](#144-build-order)). (3) **Its disclosure is a block of its own after the pack flags' block**, whose "you asked for" is the argv those flags left, as the managed launch's `--no-daemon` block already is, and whose last line says what the file is through a new optional `LaunchInjection.Why`, since a path in yolo's state says nothing on its own. (4) **The directory is `~/.local/share/yolo-jail/model-menus/<pack>/<bin>` (`paths.HostModelMenusDir`), 0700, and no jail mounts it in any mode**: a menu carries the prompt text codex runs its model with, so a copy a jail could write would choose an unconfined agent's instructions. `TestAssembleNeverMountsTheHostFloor` pins it beside the floor. (5) **One cache key for both notches** (`Request.Key`), over the parsed list rather than the list file's bytes, so the host's key names what its menu holds, and a jail rebuilds its menu once after the change. (6) **A reuse never collects, and the decision lock is held across the catalog run**, so two first launches of one program read its catalog once and the second reuses the menu. (7) **The step keys on the launched command's base name**, as the host composition's profile and prelaunch do, so `yolo host -- /path/to/codex` gets the menu too. (8) Every line the step prints at the host starts `yolo host: ` (`Request.Prefix`). (9) **In a jail the step builds nothing**, as the floor installs nothing there (`resolveHostLaunchTarget`): `yolo host --` in a jail runs the PATH copy, the jail's own launcher, which builds the program's menu from the list the jail's boot rendered for the jail's own selection, workspace scope and the jail launch's `-p` included. A menu composed from the user scope's `profile` would come later in the argv and win, `-c` being last-wins, handing codex the models of a selection it is not running on, and would make this store in the jail's own home. The store's limit, stated rather than hidden: a program that closed the inherited descriptor would let a later writer remove a menu it still reads, and a long-lived child that keeps the descriptor postpones collection until it exits | 2026-09-30 | [§14.8](#148-what-was-built-at-the-host-2026-09-30) | ✅ 2026-09-30: `hostComposition.modelMenu`, `menuProviderDisagreement` (deleted 2026-10-05 for `menuFollowsTheLaunch`, [MM-D30](#MM-D30)), `hostMenu`, `modelmenu.Request.WriteIn`; `TestHostCodexWithAPOverAnotherProviderGetsNoMenu` (inverted 2026-10-05 by [MM-D30](#MM-D30) as `TestHostCodexMovesOntoAPOverAnotherProviderAndGetsItsMenu`), `TestHostCodexOnTheConfiguredSubscriptionGetsYolosMenu`, `TestHostManagedCodexHoldsTheMenusLockWhileItRuns`, `TestHostCodexReadsItsCatalogInTheEnvironmentTheLaunchComposed` (2), `TestAssembleNeverMountsTheHostFloor` (4), `TestWriteInReusesAMenuAndRepeatsTheMissingIDWarning`, `TestWriteInSerializesTwoFirstLaunchesOnTheDecisionLock` (6), `TestHostCodexInAJailLeavesTheMenuToTheJailsLauncher` (9) |
| <a id="MM-D29"></a>MM-D29 | *Implementation decision, building [MM-D5](#MM-D5)'s `yolo check` line.* **The check names an agent whose menu a profile's switch leaves unnarrowed from a fact the agent's pack declares, `exact_menu_refuses` on its program, never from an agent's name.** Which agent cannot narrow its menu without refusing is a fact about that agent, which core may not know ("core does not know what an agent is"), so `packs/opencode` declares it, as `packs/codex` declares `unlisted_background_models` for the bridge. The fact covers every list a `models` `only` narrowed, and the whole list of each provider its `providers` names: opencode names `openai-codex`, whose one list ([ML-D1](#ML-D1)) its derive writes as the whitelist of opencode's own `openai` provider whenever the switch is on, with no `only` ([§3](#3-what-exists-today)). **Whose switch:** the one a derive reads for that provider's row, the agent's primary profile for the provider it selects, else the first later entry of its active set on that provider, each entry carrying its own profile's switch as every entry is live ([AP-P1](active-provider-sets.md#AP-P1)); the set is the one a derive is handed. **Left out:** a provider naming no endpoint and no platform, the agent's own first-party API ([OQ-PR2](../reference/protocol-resolution.md#oq-pr2)), where yolo's table points no row anywhere, so no filter is written with the switch on either (a provider naming a platform and no endpoint is taken to have a row, the agent's own client for that platform, since core cannot tell which platforms an agent has one for; opencode's derive writes it only on `aws-bedrock`, so on another such platform the line names a menu the switch does not decide); and a list no `only` narrowed, outside `providers`, which opencode's derive leaves unnarrowed with the switch on or off, what it shows being [OQ-MM1](#OQ-MM1)'s (ruled 2026-10-15: the catalog beside it) and whether it refuses [OQ-MM3](#OQ-MM3)'s. One warning per agent and provider, never a refusal, naming the agent, the provider, the profile and the pack, and saying only what is true whether or not the agent keeps a catalog of its own for the provider: yolo writes no filter, and the menu is not held to the list. Like every prediction in the Packs section it reads the configured `profile`, never a `-p`. **Why a declaration and not a render:** the check's entrypoint dry run renders the shipped packs with no profile selected, and reading what an agent's rendered config narrows needs that agent's own key (opencode's `whitelist`), which is the agent knowledge core may not hold. The cost is that the declaration and the derive state one rule twice, so a test renders the shipped derive with the switch on and off and fails wherever the check would name a menu the switch does not decide, or miss one it does | 2026-10-01 | [§14.5](#145-what-was-built-2026-09-29) | ✅ 2026-10-01: `packdecl` (`Contribution.ExactMenuRefuses`, its validation, the fork refusal; the fork rewrite keeps it, `TestTheForkRewriteKeepsAndReplacesExactlyFPD6sFields`), `packload.UnnarrowedMenus`, `unnarrowedMenuReport` called from the Packs section (`internal/cli/check/packs.go`), `packs/opencode`'s declaration; `internal/packdecl/exactmenu_test.go`; `internal/cli/check/unnarrowedmenu_test.go`, which drives the section and fails with the call or the declaration removed; and `TestTheUnnarrowedMenuLineAgreesWithOpencodesWhitelist` (`internal/entrypoint/unnarrowedmenus_test.go`), which renders the shipped derive |
| OQ-MM5 | **Ruled in review, B: a host `-p` moves codex for that launch, and opencode, pi and omp with it, so the menu follows the `-p`.** *"130 B for sure."* The vehicle is per-process flags, never a file, so the user's own config files are untouched and concurrent host launches cannot collide. Confirms what a parity batch took on the leaning under the 2026-10-04 delegation; carried out as [MM-D30](#MM-D30) | 2026-10-05 | [§14.9](#149-a-host--p-moves-the-program-built-2026-10-05) | ✅ [MM-D30](#MM-D30) |
| <a id="MM-D30"></a>MM-D30 | **Ruled in review 2026-10-05 ([OQ-MM5](#16-decision-ledger) B), confirming the implementation decision the parity build took on the leaning under the maintainer's 2026-10-04 delegation; SUPERSEDES [MM-D25](#MM-D25).** A host `-p` moves codex, opencode, pi and omp for that launch by per-process flags, never by writing a config file. *As built:* **A host `-p` moves a program whose provider lives in its own config file onto the `-p`'s selection for that launch, handed to that process alone and never written, and codex's menu follows it.** [NC-D1](../plans/notch-convergence.md#7-decision-ledger) rules that the host acts like every other notch, and at every other notch the `-p` decides the provider. **B's letter, writing the selection into the managed `CODEX_HOME`'s `config.toml`, is rejected as the vehicle**: that home is one per pack, shared by every concurrent `yolo host -- codex`, so two launches with different `-p`s would race on a file codex rereads at every thread start (SOURCED, 0.159.2), and it exists only with yolo's OpenAI login, so the `-p` would move codex for a subscription user alone; nor does it reach opencode, pi or oh-omp. Per process holds for every launch. **The mechanism is a pack declaration, `launch_selection` on a `program` (`packdecl.LaunchSelection`, a term coined 2026-10-05: the selection a program's config-surface derive composes for ONE host launch, handed as argv or variables instead of a file)**: the surface whose derive composes it; one of three forms, `each` (argv per leaf, `{key}` the dotted path and `{value}` the value in the surface's codec), `flags` (argv per selection key, a list joined by commas) or `env` (one document variable); the rows of a table the selection names; defaults; and further computed surfaces handed whole in variables. Core names no agent: codex declares `-c {key}={value}` over `model_providers`, opencode `OPENCODE_CONFIG_CONTENT` over `provider`, pi `--provider`, `--model` and `--models` with its two list files, oh-omp `--models` (read in 0.15.3's binary, `parsedArgs.models ?? settings.get("enabledModels")`). **One derive, one path with the jail** ([MM-D24](#MM-D24)): `entrypoint.HostLaunchSelection` runs the pack's derive over the launch's wire tables, and again over the configured profile's (`hostComposition.configuredWireTables`, the launch's packs and provider table with the configured selection), and **it hands anything only when a `-p` was typed and the two differ**: no `-p`, an agreeing `-p` and every host wrapper leave the program on its file, which [OQ-HC3](../reference/host-agent-environment.md#oq-hc3) keeps the configured profile's, so a model the user picked in the program since `yolo host apply` stands. Argv goes right after `argv[0]`, so a user's own later flag still wins ([MM-D22](#MM-D22)'s rule), and never in front of a word the pack declares in `subcommands`, which pi and oh-omp read as a subcommand only as `argv[1]` (`pi --provider zai update` is a session whose first prompt is "update"): a launch whose typed `argv[1]` is one hands nothing and says so; it is disclosed in `LaunchInjection`'s words and a variable by its name and what it carries, never its value (a launch has no quiet mode, [OQ-RO3](../reference/report-tiers.md#why-its-this-way)); `YOLO_NO_LAUNCH_FLAGS=1` skips it with a line; in a jail it does nothing, for [MM-D28](#MM-D28) (9)'s reason; every reason it hands nothing for a `-p` is a line naming a next step that works, and none refuses the launch: a `-p` that composes no selection at all is said before the comparison, which counts two empty selections the same and so silenced it where the config names no profile, and names the profiles whose selection would move the program (`profilesThatMove`, one derive per resolved profile on that path alone), not `yolo host apply`, which runs the same derive and would compose the same nothing; a typed subcommand names `yolo host -p <profile> -- <program>`, which starts the program itself on the selection; every other reason names `yolo host apply` with the profile configured. **The menu follows the provider the program runs on**: the `-p`'s when the selection moved it or agrees; none when a declared selection could not be handed; and [MM-D25](#MM-D25)'s provider rule only for a program whose pack declares no launch selection. **The choices the build made:** (1) codex's subscription selection names no `model_provider`, which a jail's deselect clears; the declared default `openai`, codex's built-in subscription provider, stands for that clear (MEASURED: `-c model_provider=openai` loads with no row; `-c` overrides `config.toml`, and a full `-c model_providers.<id>.*` row loads, on codex 0.145.0). (2) A row is handed whenever the selection names one, whether or not `yolo host apply` wrote it, so a program never meets a selection whose row its file lacks. (3) opencode's document is merged over a value the program's environment already holds for the variable, objects key by key, the selection's keys winning and every other key of the user's kept, since the user typed the `-p` for this launch and nothing of theirs is thrown away; a value that is not a JSON object is left as set and opencode is not moved, with a line. (4) pi's two list files travel as their CONTENT in `YOLO_PI_OPENAI_CODEX_MODELS` and `YOLO_PI_MODEL_LISTS`, which both extensions read before the file, rather than as per-launch copies under yolo's state as the brief proposed: a variable needs no lock and no collection, cannot outlive the process, and reaches pi's own child processes alike; codex's menu stays a file because `model_catalog_json` takes only a path. (5) `yolo host env -p` exports the `env` form and, for a selection that needs argv, exports none of it and names `yolo host -p <profile> -- <agent>`, since pi's lists without its `--provider` would hand pi half a selection. (6) A value the declared words cannot spell, a `{key}` segment holding the `.` that joins the path or a list item holding the `,` that joins the list, hands nothing and says so. (7) The selection and the menu share the launch's `host.model_menu` span. (8) A pack's `subcommands` are its program's words, read from a release (pi 1.0.1's dist; oh-omp 0.15.3's command table, `launch` included, plus `help`); `subcommands` is refused for the `env` form, which moves no word, and for a word that is empty, a flag, holds a space or repeats; the comparison is the typed `argv[1]` alone, as both programs read it. **Known limits:** a subcommand a later release adds is moved until its pack names it; a key a jail's deselect clears and no default stands for keeps the file's value (pi's and oh-omp's `enabledModels` when the `-p` composes no scope; opencode's `model` when the `-p`'s provider declares none); pi-subagents' `subagents` block and a via row in pi's `models.json` stay the configured provider's ([§14.9](#149-a-host--p-moves-the-program-built-2026-10-05)) | 2026-10-05 | [§14.9](#149-a-host--p-moves-the-program-built-2026-10-05) | ✅ 2026-10-05: `packdecl.LaunchSelection` (`launchSelectionProblems`, the projection), `entrypoint.HostLaunchSelection`, `hostComposition.launchSelection` called from `hostExec` and from `yolo host env`, `menuFollowsTheLaunch`, the four packs' declarations, and the two pi extensions' variable reads; `internal/packdecl/launchselection_test.go`, `internal/entrypoint/hostlaunchselection_test.go` (`TestPiExtensionsReadTheLaunchsListsBeforeTheirFiles` among them), and at the call site `TestHostCodexMovesOntoAPOverAnotherProviderAndGetsItsMenu`, `TestHostCodexMovesOntoAUserProviderWithItsRow`, `TestHostCodexOnAnAgreeingPGetsNoSelection`, `TestHostCodexLetsTheUsersOwnLaterDashCWin`, `TestHostCodexSaysWhenAPCannotMoveIt`, `TestHostManagedCodexGetsTheSelectionInItsArgv`, `TestHostRunsPiOnItsWholeSet`, `TestHostRunsOpencodeOnItsWholeSet`, `TestHostMovesOhOmpsScopeUnderAnOnly`, `TestHostEnvHandsTheSelectionAScriptCanCarry`, `TestTheMenuOfAProgramWithNoLaunchSelectionFollowsOnlyItsFile`, `TestTheMenuStepBuildsNoMenuForAPTheProgramWasNotMovedOnto`, `TestHostLeavesASubcommandWhereTheUserTypedIt` |
| OQ-MM4 | **Ruled in review, B: yolo writes copilot's whole list in `providers.json` on every provider.** *"This would have to be like a company co-pilot account or whatever GitHub account they're logged into. Like, I don't see a huge risk in that, but also the YOLO bridge probably wouldn't pass it through … if we could prevent this routing, it would be nice, but it's not like necessary."* GitHub's own models appear beside the list, and a GitHub pick is served by the GitHub account copilot is signed in to. Keeping a GitHub pick off GitHub is wanted where a mechanism allows it cheaply, not required. Carried out as [MM-D31](#MM-D31) | 2026-10-05 | [§14.10](#1410-what-was-built-2026-10-05-no-bedrock-list-and-copilots-whole-list) | ✅ [MM-D31](#MM-D31) |
| <a id="MM-D31"></a>MM-D31 | **Ruled in review ([OQ-MM4](#16-decision-ledger) B):** copilot gets its whole list in `providers.json`; GitHub's own models appear beside it and go to the signed-in GitHub account; preventing that routing is wanted, not required. **As built:** every provider whose list names a model, by a pack's declaration, a `models` contribution or the user's `providers.<name>.models`, reaches copilot in a jail as that file: one provider (the endpoint, type, wire API and key its environment setup sends) and a row per entry in list order, the wire id as `id` and `wireModel`, with the entry's name, context window and output cap, the profile's context window as each row's prompt cap, and a profile's own unlisted id as a row of its own. `COPILOT_MODEL` names the start model as `<provider>/<id>`; the `COPILOT_PROVIDER_*` variables stay beside it, inert while the file declares anything. Nothing keeps a GitHub pick off GitHub: copilot's one mechanism, its offline mode, costs its web tools, GitHub MCP server and updates ([§14.10](#1410-what-was-built-2026-10-05-no-bedrock-list-and-copilots-whole-list)) | 2026-10-05 | [§14.10](#1410-what-was-built-2026-10-05-no-bedrock-list-and-copilots-whole-list) | ✅ 2026-10-05: `packs/copilot/derive.lua` (`providersFile`), through [MM-D33](#MM-D33); `TestABedrockListAPackSuppliesReachesCopilotsProvidersFileWhole`, `TestCopilotByokComposesCerebrasThroughTheBridge`, `TestCopilotByokHandlesLocalProviderAndContext` |
| <a id="MM-D32"></a>MM-D32 | **Ruled in review, amending [ML-D9](#ML-D9):** yolo ships no curated Bedrock model list. *"this is another opinion I don't want to have. we should just leave it unfiltered, whatever defaults you get, and then allow packs to override that if needed."* Each agent uses its own Bedrock catalog and defaults (pi, codex, opencode, claude), an upstream catalog's own faults included (*"if their upstream is broken, that's fine. We're not fixing that"*); copilot, which has no Bedrock catalog, gets one cheap open-weight model as its starting default (the walkthrough's option C); a pack may narrow or replace any of it. *What yolo does where neither the agent nor a pack has a list is [OQ-MM6](#16-decision-ledger), ruled the same day: it fetches the region's list on the host, and stops the launch only when that fails too* | 2026-10-05 | the walkthrough; [§4](#4-where-yolo-picks-the-cannot-default-cases) | ✅ 2026-10-05: `packs/bedrock/pack.json` declares no models; copilot's default is [MM-D34](#MM-D34); `TestTheShippedBedrockProviderDeclaresNoModelList`, `TestNoAgentOnBedrockStartsOnAModelYoloPickedButCopilot` ([§14.10](#1410-what-was-built-2026-10-05-no-bedrock-list-and-copilots-whole-list)) |
| <a id="MM-D33"></a>MM-D33 | *Implementation decision, reversible, building [MM-D31](#MM-D31) through [MM-D10](#MM-D10)'s second branch ([OQ-MM2](#16-decision-ledger)).* **copilot's `providers.json` is an AGENT FILE, a term coined here: a file the jail's per-agent env writer puts beside an agent's per-agent env file, whose content the agent's env derive composes under a variable the agent's pack names, and whose path the agent then receives under that variable.** A program declares them in `agent_files` (variable → file name; `packs/copilot`: `COPILOT_PROVIDERS_CONFIG` → `providers.json`), so core names no agent. The env derive composes the content, a string or a table written as JSON, only where it is told the notch writes one (`ctx.agent_files`), and the credential gate takes every declared variable out of the environment before it becomes a shape var, so no vehicle that serializes an environment hands a document to a program as a value. The jail notch collects them (`packload.WithAgentFiles`, `AgentDelivery.Files`) and its writer, the one the container backends and macos-user share, puts each at `~/.config/yolo-agent-env/<bin>.<name>` with the env file's mode and lifetime, written before the env file, rewritten on every entry and removed with it, and the env file sets the variable to the path in the jail home (`/home/agent`, or the sandbox account's on macos-user) as a default a user's own value wins over. **Why the env derive:** the file carries its key as literal text (MEASURED in copilot 1.0.89: no expansion, and `apiKeyCommand` unread), and a surface derive never receives a key; the env derive is the one composition that holds it, and the env file's directory already holds that key at rest, owner-only. **At `yolo host` no file is written**, so copilot keeps its environment's one model there and is told nothing of a file: a key-bearing file outliving an exec'd process would need a liveness reaper of its own, which nothing asked for. **Rejected:** a `copilot/providers` surface rendered at boot (no key), copilot's own `~/.copilot/providers.json` (persistent workspace state holding a provider's key), and a launcher step writing the file at exec (a second writer of a secret, in shell) | 2026-10-05 | [§14.10](#1410-what-was-built-2026-10-05-no-bedrock-list-and-copilots-whole-list) | ✅ 2026-10-05: `internal/packdecl/agentfiles.go`, `internal/packload/agentfiles.go`, `ctx.agent_files`, `run.writeAgentEnvFiles` (`agentFileLines`); `TestAgentFilesIsAProgramsFactReadByBin`, `TestAgentFilesIsRefusedWhereNothingCouldWriteIt`, `TestAgentFilesLeaveTheEnvironmentAndReachTheDelivery`, `TestAnAgentFileIsWithheldWhereNoneIsWrittenAndRefusedWhenMalformed`, `TestAnAgentFileLandsBesideItsEnvFileAndLeavesWithIt`, `TestHostCopilotOnASuppliedBedrockListKeepsItsEnvironmentsOneModel` |
| <a id="MM-D34"></a>MM-D34 | *Implementation decision, reversible, building [MM-D32](#MM-D32)'s copilot clause.* **copilot's starting model on a Bedrock provider whose list names nothing is `openai.gpt-oss-120b-1:0`, OpenAI's open-weight gpt-oss-120b as bedrock-runtime spells it in-Region**, in `packs/copilot/derive.lua` (`bedrockStartModel`), applied only through the bridge (copilot's one way to Bedrock), only on the `aws-bedrock` platform, and only when neither the profile nor a list names a model, so a pack's `models` contribution, the user's list or a profile's `model` replaces it. A profile's own id there is passed through as written, ahead of a list's first entry, as every other Bedrock binding passes one; copilot used to take only an id its list's aliases named. **Why this model:** it is open-weight (Apache 2.0); it is among the cheapest models Bedrock serves, at $0.15 per million input tokens and $0.60 per million output tokens in the US (AWS's pricing page, read 2026-10-05, shows $0.1545 and $0.6180 for Sydney, and three 2026 summaries of that page cite the US rates), where only smaller models are cheaper (gpt-oss-20b at $0.07 and $0.30, Gemma 3 4B at $0.04 and $0.08), none of which this decision would trust to drive a coding agent's tools; it is served on bedrock-runtime's chat completions, the route the wire bridge translates copilot's requests onto, among the families [`bedrock-plumbing.md` §2](bedrock-plumbing.md#2-what-bedrock-is--sourced-from-awss-pages) lists that route serving, and its AWS card says runtime serves it on Chat Completions and not on the Responses API; and it is in-Region in us-east-1, us-east-2 and us-west-2 at least (its card, read 2026-10-05), with no global inference id and a geographic one in GovCloud alone. MEASURED 2026-10-05: one signed chat-completions request from a jail in us-east-1, with the fields the bridge sends, answered 200 ([§14.10](#1410-what-was-built-2026-10-05-no-bedrock-list-and-copilots-whole-list)). **The cost, MEASURED in the same answer:** the model writes its reasoning into `content` inside `<reasoning>…</reasoning>` before the reply, so copilot shows it. **Rejected:** Qwen3 Coder 30B A3B, the same price on AWS's page, whose id was not read or sent here; gpt-oss-20b, cheaper and weaker at tool use; and a model in the `bedrock` provider's own list, which every agent would then read | 2026-10-05 | [§14.10](#1410-what-was-built-2026-10-05-no-bedrock-list-and-copilots-whole-list) | ✅ 2026-10-05: `TestCopilotReachesBedrockThroughTheBridgeOnEitherProfile`, `TestAProfilesModelReplacesCopilotsBedrockDefault`, `TestNoAgentOnBedrockStartsOnAModelYoloPickedButCopilot`, `TestHostCopilotOnABedrockBridgeProfileRunsThroughALaunchOwnedBridge` |
| OQ-MM6 | **Ruled in review, A, widened so that yolo fetches a list before it stops.** *"206 A. I think if there's no model list, the agent has no default handling, and we can't fetch models, we just fail and tell them that."* Wherever no pack supplies a Bedrock list, the `aws-auth` daemon on the host reads the region's list from `ListFoundationModels` and `ListInferenceProfiles` with the SSO role's credentials before they are narrowed, cached once a day per account and region; a jail's credential gains no list permission. The wire bridge takes each model's maker from AWS's `providerName` ([OQ-BR9](bedrock-plumbing.md#decision-ledger)'s "declared, never parsed" holds), copilot's picker shows the list ([MM-D31](#MM-D31)), and claude on `-p bedrock-bridge` runs in Claude Code's own Bedrock mode pointed at the bridge, so its own Bedrock defaults name the model and the bridge only signs (*"so let's do that fix"*). No pack list, no agent default and a failed fetch stop the launch, saying why and what to do. Amends [MM-D16](#MM-D16)'s "no model-list network call at launch" for Bedrock, and [Part 2](wire-bridge-gateway.md#3-part-2--routing-by-model-id-for-claudes-everything-profile-ruled)'s "never `CLAUDE_CODE_USE_BEDROCK`" | 2026-10-05 | [§9](#9-currency-the-agents-catalogs-plus-a-staleness-warning) | ✅ 2026-10-05, as [MM-D35](#MM-D35) to [MM-D40](#MM-D40); not yet run against AWS. *Combined at the landing merge, 2026-10-06, with [MM-D31](#MM-D31) to [MM-D34](#MM-D34), which landed on a branch beside it:* copilot's `providers.json` rows read the fetched list where no pack or config gives one, and [MM-D34](#MM-D34)'s cheap start model comes ahead of [MM-D40](#MM-D40)'s pick wherever the fetched list holds it, or no list was fetched, so the fetched pick answers only in a region that does not serve it. copilot's `needs_model_list` still stops a launch whose fetch failed ([MM-D36](#MM-D36)): with no list, nothing shows that the region serves the start model. Lifting that stop, which MM-D40 anticipated once MM-D34 landed, is left for the maintainer |
| <a id="MM-D35"></a>MM-D35 | *Implementation decision, building [OQ-MM6](#16-decision-ledger).* **A fetched list rides the composed provider table under its own key, `fetched_models`, never `models`** (the *fetched list*, a term coined here: the list yolo reads from a provider's platform where no pack and no config gives the provider one). It is an array of `{"id", "vendor", "name", "legacy", "created"}` objects (the last three only where set, [MM-D38](#MM-D38)) on the provider's entry in `YOLO_PROVIDERS`, written only on an entry with no list ([MM-D36](#MM-D36)'s `HasModelList`). **Its readers are only what has nothing else to fall back on:** the wire bridge, which takes each model's maker from it, and copilot's derive. **Why:** pi's, codex's, opencode's and claude's derives read `models` to narrow a menu or pin a start, and [MM-D32](#MM-D32) keeps each of those agents on its own Bedrock catalog; a fetched list written as `models` would narrow all four to a list nobody chose. Reversible: a derive that should read it reads the key | 2026-10-05 | [§9](#9-currency-the-agents-catalogs-plus-a-staleness-warning) | ✅ `packload.FetchedModelsKey`, `SetFetchedModels`, `FetchedModelsOf`; `TestOnlyCopilotsDeriveReadsTheFetchedList` (`internal/entrypoint/fetchedlist_test.go`) fails when another shipped derive names the key |
| <a id="MM-D36"></a>MM-D36 | *Implementation decision, building [OQ-MM6](#16-decision-ledger)'s stop.* **"The agent has no default handling" is a pack fact, `needs_model_list` on a `program`: the platforms on which the program has no catalog and no default model of its own.** packs/copilot declares `"aws-bedrock"`. A launch wants a fetched list for a provider that declares a `platform` and has no `models` when some agent on it either declares the need, or reaches it through a service (its profile's `via` or carrier: the bridge reads the maker). **The launch stops only for an agent that declares the need, whose profile names no `model`, when no list was obtained**, one line per agent naming the provider, the region, why the fetch failed, and the three ways out: fix that, name a `model` on the profile, or give the provider a list with a pack's `models` contribution or `providers.<name>.models`. An agent that wants the list only for the bridge is never refused; the launch says what the bridge then does with no maker to read on each of its routes, and how to give it a list. **And only where a service carries the agent there** (its profile's via or carrier, live at this notch): with none, an agent with no client of the platform reaches nothing whatever the list holds, so the launch fetches nothing for it and refuses nothing, and the profile line says it reaches nothing. A `model` of `"default"` names no model, as every derive reads it **Why:** core may not know which agent has a catalog ("core does not know what an agent is"), so the pack says so, as `exact_menu_refuses` does ([MM-D29](#MM-D29)). A profile's `model` counts as default handling, since copilot starts on it; copilot's derive now passes a literal profile id through on the bridge, as [MM-D32](#MM-D32)'s build does. **A list a pack's `only` narrowed, to nothing included, is a list** (`HasModelList` reads `models_only`), so nothing is fetched to fill it | 2026-10-05 | [§9](#9-currency-the-agents-catalogs-plus-a-staleness-warning) | ✅ `packdecl.Contribution.NeedsModelList` and its validation (`internal/packdecl/needsmodellist_test.go`), `packload.ListWants`, `packload.HasModelList`, `run.fetchedListRefusal`; `TestALaunchLeftWithNothingToStartOnSaysWhyAndWhatToAdd`, `TestWithNothingToCarryCopilotTheLaunchFetchesNothingAndStopsNothing`, `TestAProfileModelOfDefaultNamesNoModel`, `TestNoListTellsClaudeAtTheBridgeWhatItMeansAndWhatToDo` (`internal/cli/run`), `TestAnAgentNoServiceCarriesWantsNoList`, `TestAListNarrowedToNothingIsAList` (`internal/packload`) and, at a real launch through the front door, `TestABedrockLaunchWithNoListAndNoFetchStopsSayingWhy` (`integration/bedrock_test.go`), which holds the machine lock exclusive and refuses to run beside a live aws-auth service, whose host socket is machine-wide |
| <a id="MM-D37"></a>MM-D37 | *Implementation decision, building [OQ-MM6](#16-decision-ledger).* **The launch obtains the list while it composes the provider table, after the credential gate's first answer and before the env derives it feeds, and the gate composes again with the list in the table.** The gate's first answer is what knows the region each agent is handed (the provider's own `region`, else its region fill from `~/.aws/config`), and copilot's start model comes from an env derive. **It asks the aws-auth service first**, on the service's private host socket, within five seconds; the service answers from its cache when the region's list is a day old or less, else fetches, else answers its cache's last good list with why it was not refreshed. **When no service is running to ask** (the first Bedrock launch on a machine composes before it starts the service), the launch runs the service's own fetch itself, as the configured profile, writing the same cache under the same lock and within the same budget; without that, a launch that needs a list would be refused before it started the service that could fetch one, every time. A launch with no consumer of the list asks nothing. One line per provider says where the list came from: the cache and its age, a fetch, or a stale cache and why. **The fetch is a seam the front door installs (`Options.FetchModelList`)**, nil in every hand-built `Options`, so no test of the run pipeline can reach AWS. The host notch (`yolo host`) fetches nothing yet | 2026-10-05 | [§9](#9-currency-the-agents-catalogs-plus-a-staleness-warning) | ✅ `run.composeFetchedLists`, called from `composePackChannelWith`; `run.DefaultFetchModelList`, installed in `internal/cli/commands.go`; `TestTheLaunchHandsTheFetchedListToTheJail`, `TestNoSeamFetchesNothing`, `TestTheLaunchAsksTheRunningServiceFirst`, `TestWithNoServiceTheLaunchFetchesTheListItself` (`internal/cli/run`), `TestTheFrontDoorInstallsTheModelListFetch` (`internal/cli`); each fails with its call site removed |
| <a id="MM-D38"></a>MM-D38 | *Implementation decision, building [OQ-MM6](#16-decision-ledger)'s fetch.* **The aws-auth service fetches with the `aws` CLI it already runs, as its configured profile and so with the SSO role's own credentials, before any narrowing: `aws sts get-caller-identity` (the account), `aws bedrock list-foundation-models --by-output-modality TEXT` and `aws bedrock list-inference-profiles --type-equals SYSTEM_DEFINED`**, each with `--profile`, `--region` and `--output json`, run side by side. **The join:** a model with TEXT output is listed under its own id when AWS says it is callable `ON_DEMAND`, and every ACTIVE system-defined inference profile backed by one is listed under the profile's id (`us.anthropic.…`), since recent Claude models are callable on demand only that way; image and embedding models are left out. **The maker is AWS's `providerName`, lowercased** (`anthropic`, `openai`, `mistral ai`), so it stays declared and never parsed ([OQ-BR9](bedrock-plumbing.md#decision-ledger)). Entries are sorted by maker, then id. **Two more of AWS's facts ride each entry**, for copilot's start ([MM-D40](#MM-D40)): `legacy` where the model's `modelLifecycle` is LEGACY (on a profile, its backing model's), and on a profile its `createdAt` as `created`, RFC 3339 at UTC to the second. **The cache** is `bedrock-models.json` in the service's state directory, 0600 in 0700, keyed by account and region with an index from each profile to its account, so a warm launch makes no `aws` call; fresh for a day; one writer at a time under its own lock. **The service's `bedrock-models` action is answered on its host socket only**: through a front it is refused, since a jail would be spending the un-narrowed credential. Application inference profiles (ARN-shaped ids) are not listed. ⚠ The list describes the SSO role's account; a narrowing role in another account may see a different set | 2026-10-05 | [§9](#9-currency-the-agents-catalogs-plus-a-staleness-warning) | ✅ `awsauth.ModelLister`, `awsauth.JoinModels`, `awsauth.StoreModelList`, `awsauthdaemon.ObtainModelList` and the `bedrock-models` action; `internal/awsauth/modellist_test.go` (`TestTheJoinCarriesAWSsLifecycleAndProfileDates` for the two facts), `internal/awsauthdaemon/modellists_test.go` (with `TestAJailCannotAskForTheList`), every `aws` invocation a stand-in |
| <a id="MM-D39"></a>MM-D39 | *Implementation decision, building [OQ-MM6](#16-decision-ledger)'s "claude through the bridge".* **On a profile that routes claude through the wire bridge to a Bedrock provider whose anthropic address is the one composed for that via, claude's env derive sets Claude Code's documented gateway settings and nothing that routes it elsewhere:** `CLAUDE_CODE_USE_BEDROCK=1`, `ANTHROPIC_BEDROCK_BASE_URL` at the bridge's adapter, `CLAUDE_CODE_SKIP_BEDROCK_AUTH=1`, and the bridge's caller token as `ANTHROPIC_AUTH_TOKEN`, which Claude Code sends there as `Authorization: Bearer` (SOURCED 2026-10-05, <https://code.claude.com/docs/en/llm-gateway-connect>, "Route to a cloud provider through a gateway"; the names also read in the 2.1.290 binary, which was never run). No `ANTHROPIC_BASE_URL` and no routed picker: claude's own Bedrock defaults name the model unless the profile does, and a profile's `model` or tier alias of any maker on the list is pinned, as on the everything profile before. What claude's picker shows for a list that only adds is [OQ-MM1](#OQ-MM1)'s (ruled 2026-10-15: its rows beside claude's built-ins), as on the native profile; under an `only` it is the narrowed list, every maker's entries included. **No `AWS_BEARER_TOKEN_BEDROCK` in claude's process:** Claude Code sends it, when it holds one, as its `Authorization` in place of the caller token (read, not run, in the 2.1.290 binary's Bedrock branch), so the bridge would refuse every request 401 and the long-lived key would go to a loopback port; the derive's tombstone removes it from the launched process alone, and claude's env file, the bridge's key channel, still carries the gate's delivery for the bridge to sign with. The settings file's switch stays the native profile's, since a bare `claude` outside yolo has no bridge to reach. **The bridge signs and passes through Bedrock's own routes**, `POST /model/{id}/invoke`, `/invoke-with-response-stream` and `/count-tokens`, which is what that mode sends (SOURCED from Claude Code's [gateway compatibility guide](https://code.claude.com/docs/en/llm-gateway-protocol)): the body, the status and AWS's binary event stream relayed byte for byte, the id re-encoded as the AWS SDKs encode it, `X-Amzn-Bedrock-*` request headers kept, and no inbound credential forwarded; the startup control-plane reads (`GET /inference-profiles…`) are refused 404 in AWS's error shape, and Claude Code then falls back on its built-in Bedrock ids. **And the everything profile stands** ([OQ-BR11](bedrock-plumbing.md#decision-ledger), ruled 2026-09-24, which [OQ-MM6](#16-decision-ledger) did not amend): that mode sends Anthropic's request format for any model, so the bridge translates a model its list declares another maker's, as its Messages route does, rather than forwarding it: the InvokeModel body becomes the Messages request it carries (the path's model and the route's stream flag set, `anthropic_version` and `anthropic_beta` dropped), translated to runtime's chat completions, and the answer goes back as InvokeModel's: Anthropic's message JSON, or AWS's event stream with one `chunk` message per Anthropic event, a failure inside the stream an exception message, and count-tokens refused 404 as WB-D14 refuses it. The framing is byte-identical to the AWS SDK for Go's event-stream encoder (checked once, out of tree), and Claude Code's Bedrock client reads a chunk back into SSE by its `type` and an exception as an `error` event (read in the 2.1.290 binary). *Corrected after review, 2026-10-05:* the first build refused such a model, which dropped [OQ-BR11](bedrock-plumbing.md#decision-ledger) under an implementation decision | 2026-10-05 | [§9](#9-currency-the-agents-catalogs-plus-a-staleness-warning); [wire-bridge-gateway.md WG-I47](wire-bridge-gateway.md#WG-I47) | ✅ `bridgedBedrock`, `claudeBedrock` and `claudeMakers` in `packs/claude/derive.lua`; `wirebridged`'s `invoke.go` and `invoketranslate.go`; `TestClaudeRunsItsOwnBedrockModeAtTheBridgeUnderAVia` (`internal/entrypoint`), `TestTheBridgedBedrockProfileRunsClaudesBedrockModeAtTheBridge` (`internal/packload`), the bridged case of `TestClaudeOnBedrockTakesNothingItsOwnClientCannotUse`, `TestClaudeAtTheBridgeNeverHoldsABedrockAPIKey` and `TestClaudeAtTheBridgeOnMacosUserHoldsNoBedrockAPIKey` (`internal/cli/run`), and `internal/wirebridged/invoke_test.go` (`TestTheInvokeRouteTranslatesAnotherMakersModel`, `TestATranslatedModelsStreamIsAWSsEventStream`, `TestATranslatedModelsFailuresAreAWSs`, `TestCountTokensOfATranslatedModelIsRefused`, and `TestTheBootHandsTheInvokeRouteTheListsMakers` over the production boot). Not run against AWS: claude turns under `-p bedrock-bridge` on a Claude model and on another maker's are the human check |
| <a id="MM-D40"></a>MM-D40 | *Implementation decision, building [OQ-MM6](#16-decision-ledger)'s "who reads it".* **The wire bridge reads each model's maker from the fetched list as it reads a pack list's `vendor`** (`declaredVendors`), so a Claude model on it goes untranslated to runtime's Messages route and the invoke route translates another maker's. **copilot starts, where no pack or config gives a list and its profile names no model, on the fetched list's newest active Anthropic entry**: ranked by AWS's own facts, a model not marked LEGACY first, then an Anthropic model, then the newest by its profile's creation date (`created`, [MM-D38](#MM-D38)), ties in the list's order. An Anthropic model, since a start model must be one the bridge carries and an Anthropic model is the one it forwards untranslated (measured 2026-10-01); a model it would translate is carried only where runtime's chat completions serves it, which the list does not say. Not the list's first Anthropic entry, which the first build took: the list's (maker, id) order puts an old model callable on demand (`anthropic.…`) ahead of every cross-region profile (`us.…`), through which recent Claude models are callable, so copilot started on the oldest Claude still callable on demand. Never under an `only`, whose narrowing is the list ([MM-D36](#MM-D36)). **copilot's picker showing the whole fetched list is [MM-D31](#MM-D31)'s `providers.json`**, which is built on a branch not yet on main; where it lands, its rows read the fetched list where `models` is empty, and [MM-D32](#MM-D32)'s cheap start model, where that lands too, comes ahead of the first Anthropic entry. ⚠ When both land, copilot's `needs_model_list` no longer stops a launch, since that start model is copilot's default handling | 2026-10-05 | [§9](#9-currency-the-agents-catalogs-plus-a-staleness-warning) | ✅ `wirebridged.declaredVendors`, `fetchedStart` in `packs/copilot/derive.lua`; `TestAFetchedListsMakerRoutesAClaudeModelUntranslated` (`internal/wirebridged`), `TestCopilotStartsOnTheFetchedListWhereNoPackSuppliesOne` and `TestCopilotStartsOnTheNewestActiveClaudeTheListHolds` (`internal/entrypoint`), `TestTheLaunchCarriesTheFactsCopilotStartsBy` (`internal/cli/run`). Not run against AWS: one copilot turn under `-p bedrock-bridge` is the human check |
| <a id="MM-D41"></a>MM-D41 | *Implementation decision, reversible, implementing only [§7.2](#72-composition-rules)'s empty-effective-list diagnostic.* **An optional pack-wide `model_list_check` map keyed only by locally owned non-fork program bins declares callable-maker restrictions and the native empty-source exception; core compares pack-owned strings and knows no agent or maker.** Missing bin means no new diagnostic for an older/custom pack; an empty object for that bin opts in without maker restrictions. **Corrected after review, 2026-10-08:** a program field made v0.12.1 drop the whole program; a pack-wide field is ignored without changing generic skew policy. Existing release-probe identity checks show no extra loss from this metadata; Pi's unchanged nested flag `requires` already loses its program, isolated by a fixture-only edit. Platform/path rules restrict Claude's native Bedrock to Anthropic and Codex's native/via Bedrock to OpenAI, matching unchanged derives. Claude's native exception suppresses a supplied source list already empty, not a non-empty source filtered to zero. A check-only list-presence observer distinguishes a supplied empty list from omission without adding wire keys or changing fetch policy; a pure evaluator follows alias/fact precedence, including empty-string aliases. The warning names the program, provider, active profile and pack, suggests a list edit, and never refuses a launch. No change to menus, model selection, native-provider ownership, adapters or enforcement. Production-caller, schema/compatibility and derive-parity tests pass; the 2026-10-08 parent whole-tree and applicable integration gates passed. Independent source and documentation review is complete; no live agent or AWS-backed model turn has been observed. Neighboring owner questions [OQ-MM1](#OQ-MM1) (ruled after this, 2026-10-15) and [OQ-MM3](#OQ-MM3) were unanswered then | 2026-10-08 | [§7.4](#74-the-empty-effective-list-check-decided-mechanism-2026-10-08) | Built: source, applicable gates and independent review passed. This warning does not answer [OQ-MM1](#OQ-MM1)/[OQ-MM3](#OQ-MM3) or make this partly built design BUILT |
| <a id="MM-D42"></a>MM-D42 | **Maintainer correction:** an explicit `yolo host -p` profile must govern profile-derived Pi subagent defaults and model restrictions as well as the parent session. A configured OpenRouter default must not leave OpenRouter-only child restrictions under an explicit Codex launch. Supersedes the subagent-policy limitation of [MM-D30](#MM-D30), not its per-process/no-shared-file rule; preserve enforcement and configured defaults without an override. *YOLO side built 2026-10-15:* root cause, a launch selection hands only the `selection` namespace while the `subagents` block is a computed object only a jail's boot or `yolo host apply` (configured profile) writes; repaired by the pack-wide `launch_computed` declaration (`packdecl.LaunchComputedFlag`), with which `packs/pi` hands the `-p` profile's `subagents` block per process as `--subagents-model-settings <compact JSON>`, compared apart from the selection, writing nothing, and OFF unless `YOLO_PI_SUBAGENTS_MODEL_SETTINGS=1` because pi 1.0.1 exits over an unregistered extension flag. **Amended 2026-10-16 by the maintainer:** *"yolo shouldn't depend on my custom pi fork in any way, or even know it exists."* The flag above was one only a patched pi-subagents would register, so yolo ships no pi-subagents flag: the `launch_computed` block and its opt-in variable are removed from `packs/pi`. The mechanism stays and is available to any pack; a user's own pack that declares the `pi` program, or forks it ([LC-D1](../reference/pack-system.md#LC-D1)), may declare it ([§14.9](#149-a-host--p-moves-the-program-built-2026-10-05)) | 2026-10-09 | [§14.9](#149-a-host--p-moves-the-program-built-2026-10-05) | Mechanism built 2026-10-15 (`entrypoint.LaunchSelection.ComputedArgv`, `hostComposition.launchSelection`; `internal/cli/hostlaunchcomputed_test.go` over a test pack, `TestHostLaunchSelectionHandsAComputedKeyAsOneJSONWord`, `internal/packdecl/launchcomputed_test.go`); no shipped pack declares it (`TestHostShippedPiHandsNoComputedFlag`) |
| <a id="MM-D43"></a>MM-D43 | *Implementation decision, reversible, building [OQ-MM3](#OQ-MM3)'s write.* **claude's saved `model` goes through the existing selection namespace, and the equivalent is the saved value's tier word.** (1) *The write:* claude's settings derive emits `selection.model`; `agentcfg.ApplySelectionReport` already writes a selection value when it differs from the one yolo last wrote, keeps a user edit while the value is unchanged, and clears an unedited value of yolo's on a deselect, in a jail and at `yolo host apply` alike (`prism.go`). (2) *The value:* the tier is the tier word the saved value is, or contains as a word (`opus` in `claude-opus-5-5`); the new value is the new provider's model for that tier: on a listed provider, the id its `models` map gives the tier's alias, claude's own or yolo's (`tierAlias`, [MM-D17](#MM-D17)), and on a provider with no list, such as first-party, the tier word itself, which claude resolves natively. No tier word, or no model in that tier: the provider's default, and where the provider declares none (first-party, or `bedrock` since [MM-D32](#MM-D32)) claude's own default, reached by removing the key. (3) *A provider-specific id, not a bare tier word, on a listed provider:* a bare word would be the same value on every provider, so the selection would never see a change. (4) *Stable on one provider:* a value that already is the provider's model for its tier maps to itself, so a `/model` choice made there is not rewritten. (5) The per-launch `ANTHROPIC_MODEL` pin on a routed provider goes, as it did on `openai-codex` ([MM-D3](#MM-D3)), since it would override the saved choice every launch. **What the code lacks, read 2026-10-15:** the derive context carries no file value and no previous provider (`luahook/derive.go`); the selection record (`<agent>-<surface>.selection.json`, `readSelectionRecord`) holds only the values yolo wrote, keyed by file key, not the provider; and with no record, `ApplySelectionReport` keeps a file value that differs from the selection as the user's, which is exactly a stale saved model on the first provider change. So the build needs two new inputs: the file's current value of a selection key, handed to the derive, and a provider-change trigger, such as the record also naming the provider its value was written for. UNMEASURED: whether Claude Code's `/model` saves a tier word or a full id, and whether claude rewrites the key itself | 2026-10-15 | [The ruling](#oq-mm3-setup) | ✅ 2026-10-15, with the two inputs [MM-D44](#MM-D44) chose: `TestClaudeSavedModelFollowsAProviderChange` (the tier equivalent, a same-provider pick kept, the default where no tier matches, yolo's value removed where there is no default), `TestClaudeSavedModelIsTheTierWordWhereTheProviderHasNoList`, `TestClaudeSavedModelLeavesARemovedKeyAloneOnTheSameProvider`; the apply's rules, `TestApplySelectionTrigger` |
| <a id="MM-D44"></a>MM-D44 | *Implementation decision, reversible, building [MM-D43](#MM-D43)'s two inputs.* **The provider change is a selection TRIGGER, and the file's value reaches the derive as `ctx.saved`.** (1) *The trigger*, a term coined here: a reserved entry of the selection body, `@trigger` (`agentcfg.SelectionTriggerKey`), whose value is the choice whose change, and only whose change, makes the selection a new one. claude's settings derive emits `<provider>|<via>|<model>|<pin_model>` there: the selected provider (`""` for claude's own login), `via` when the profile routes claude through a via service, and the profile's `model` and `pin_model` options (see (5) and (7)). `ApplySelectionReport` keeps it in the selection record beside the values, never lifts it into the file, and, when a selection carries one, decides each selected key by it: trigger changed, the value is written over a user's edit; trigger unchanged, a user's value stands and a key the user removed (claude's Default) stays removed, while yolo's own unedited value still moves with the selection; no trigger recorded, as on the first launch after an upgrade, reads as a change (amended 2026-10-16 on review: every existing jail has no record, and with (6) a valid value is its own equivalent, so only a stale one is replaced). While the trigger is unchanged, a key the derive stops selecting clears by the ordinary rule, only while the file holds yolo's value. On a change, a key the derive does not select is cleared whatever the file holds, the user's own value included (amended 2026-10-16 on the second review): the derive selects nothing only when no value is valid on the new route and the provider declares no default, which is where the ruling says to remove the key so claude uses its own; a draft that cleared only yolo's value left a z.ai pick on claude's own login, which cannot call it. A selection with no trigger (pi, codex, opencode, omp) decides exactly as before. Rejected: keying the edge on the value moving, the old rule, because the value is computed from the user's pick and so would move whenever the user picks, overwriting every `/model` choice; and a second top-level reserved key beside `selection`, because the host notch rebuilds the computed layer from `TakeSelection`'s output (`buildHostLayer`) and would have dropped it. (2) *The file's value*: `luahook.DeriveCtx.Saved`, exposed as `ctx.saved`, the surface file's own top-level string values before the render, filled by the one surface-derive constructor (`entrypoint.deriveCtx`) at both notches. Strings and top level only, since that is all a selection key is; claude's `env` table never crosses. (3) *Skew*: the derive emits the selection only when `ctx.saved` is a table, because an entrypoint older than the field also predates the trigger and would write `@trigger` into the file as a key. (4) *The value*, beyond MM-D43's (2): a saved id the new provider lists is its own equivalent, which makes MM-D43's (4) hold for every listed id, not only a tier's; and `pin_model` keeps `ANTHROPIC_MODEL` on every provider, including `openai-codex` with the switch off, where the pin used to stay. **Amended 2026-10-16 on review**, each pinned in `internal/entrypoint/claudesavedmodelreview_test.go`: (5) *The route is part of the trigger*: `bedrock` and `bedrock-bridge` share the provider, but claude's own Bedrock client cannot call what the bridge translates, so switching between them is a change (a yes/no, never the address). (6) *A value valid on the route is its own equivalent, on every route and every change* (generalized 2026-10-16 on the second review, which found the first-launch-after-upgrade change rewriting valid Bedrock and z.ai values): it is written back as it is, version and `[1m]` included (`validOnRoute`). Valid means: under an `only` or on `openai-codex`, a listed id (`[1m]` stripped for the comparison); on claude's own Bedrock client, a listed id, a tier name such as `opus[1m]`, or a Bedrock-only Anthropic id (the pattern `packs/claude/pack.json` gives the `/model` provider key), and through the bridge any region- or maker-dotted Bedrock id; on a routed gateway, a listed id or anything that is neither a Bedrock id nor a first-party `claude-` id, since a typed id goes to the gateway ([the ruling](#oq-mm3-setup)); on claude's own login, a claude id or alias (a tier word, a `claude` prefix, `default`, `opusplan`) tied to no other platform (no dotted region or maker prefix, no `vendor/`, no `arn:`). Only a value the route cannot call is replaced, by its tier equivalent, else the default, else removed; a value is never rewritten without a change, at the jail or at a `yolo host apply`. (7) *A change of the profile's `model` or `pin_model` is a change*, the user's own config edit: the derive reads the recorded trigger (`ctx.saved_trigger`, `luahook.DeriveCtx.SavedTrigger`), and when only those parts moved it writes the profile's new default once, not the previous model's equivalent; a `/model` pick afterwards stands. (8) *A kept value never escapes a provider-key drop*: a value the selection keeps as the user's at a key the render's provider keys drop (`packs/claude/pack.json` declares `/model` one, for a Bedrock-only id off Bedrock) is not lifted into the computed layer (`entrypoint.dropKeptAtProviderKeys`), so the drop removes it | 2026-10-15 | [MM-D43](#MM-D43) | ✅ `TestApplySelectionTrigger`, `TestApplySelectionTriggerOutranksAHostValue` (`internal/agentcfg`); through the boot render, `internal/entrypoint/claudesavedmodel_test.go`; the host value case, `TestAProfileChoiceOverridesTheImportedHostSettingsInAJail` |
| <a id="MM-D45"></a>MM-D45 | *Implementation decision, reversible, building the 2026-10-18 handoff's finding 3.* **An agent's pack declares where its config names models (`model_list_check.<bin>.references`), and `yolo check` and the boot resolve each against the agent's model registry, warning about one that matches nothing.** Both terms are coined in [§7.5](#75-model-references-a-name-nothing-resolves-and-a-picked-model-outside-the-scope-built-2026-10-18). Nested in `model_list_check` because the last release reads that fact without an unknown member and keeps the program (`packs/releasedecode_test.go`). The registry includes the agent's own rendered model files (`references.registry`), so the derive's own values resolve: `yolo check` composes them through the agent's derives, the boot reads the files. `yolo check` names the profile key and its file and line, or the pack and pointer, the nearest ids, and the fix; a `subagents` value off a listed provider's list is a FAIL where the render refuses it and a warning where it falls back to the list's `default`; an unread catalog is a skip. The boot reads the rendered file, so the derive's own computed values are checked too, and points at `yolo check` for the declaration. Warnings otherwise | 2026-10-18 | [§7.5](#75-model-references-a-name-nothing-resolves-and-a-picked-model-outside-the-scope-built-2026-10-18) | ✅ `TestCheckWarnsAboutASubagentModelPisRegistryDoesNotHold`, `TestCheckFailsASubagentModelOffAListWithNoDefault`, `TestCheckWarnsAboutAPacksScopePatternThatMatchesNothing`, `TestCheckResolvesAPatternAgainstPisRenderedModelFiles`, `TestTheBootNamesASubagentModelPisRegistryDoesNotHold`, `TestTheBootNamesNothingForTheShippedProfilesOwnValues` |
| <a id="MM-D46"></a>MM-D46 | *Implementation decision under [OQ-XM3](../reference/providers.md#oq-xm3) and [OQ-MM3](#OQ-MM3), building the handoff's finding 11.* **A picked model outside the derived scope is named at the next boot, never admitted into the scope.** The boot compares the rendered picker pair (`references.selection`) with the `references.scope` array and warns, naming the model, the scope, the profile and the fixes: pick a model the scope admits, or select a profile whose provider serves the pick. Admission was rejected: [OQ-XM3](../reference/providers.md#oq-xm3)'s *"regardless, it shouldn't be able to cross providers"* forbids widening the scope to another provider, and [OQ-MM3](#OQ-MM3) keeps a refusal under an `only` | 2026-10-18 | [§7.5](#75-model-references-a-name-nothing-resolves-and-a-picked-model-outside-the-scope-built-2026-10-18) | ✅ `TestTheBootNamesAPickedModelTheDerivedScopeExcludes` |
| <a id="OQ-ML4"></a>[OQ-ML4](#OQ-ML4) | **A, as a field-by-field merge: a fact a pi model row states wins, a fact it leaves out comes from pi's catalog, and only a tombstone removes one.** The catalog entry is the one for the row's id, or for its `base` when it names one. It holds for both files yolo writes, `models.json` and the menu file. A fact is removed only by a tombstone the row writes ([ML-D10](#ML-D10)); yolo never drops a catalog fact unless a row asks. The owner confirmed the problem first: today a row replaces the catalog entry whole, so a fact it omits, such as the thinking levels, is lost and `--thinking xhigh` is turned down. Then: *"we need to support tombstones, and then just write in the values given, essentially. So don't drop things unnecessarily."* Rejected: `base` in `models.json` only, so a row borrows facts only when it says so (B), and leaving `base` menu-only (C) | 2026-10-16 | [How a pi model row gets its facts](#oq-ml4-ml5-setup) | ✅ Built 2026-10-16 ([ML-D12](#ML-D12)): the menu file's rows merge over the catalog field by field (`definition` in [`yolo-model-lists.js`](../../packs/pi/home/.pi/agent/extensions/yolo-model-lists.js)), and so do the rows of pi's own Bedrock client, which also ride the menu file marked `keep_catalog` so the extension registers pi's catalog with each row merged in (`withCatalog`); a tombstone removes the catalog's fact there. `TestPiBedrockRowsMergeOverPisCatalogAndTombstonesRemove`. A `models.json` row on a provider pi ships no catalog for has no catalog entry to inherit from, so it is the row as stated |
| <a id="OQ-ML5"></a>[OQ-ML5](#OQ-ML5) | **Facts may be written once and shared by a model's routes, and every route's row can override any shared fact.** The order is: the route's own value, then the shared facts, then pi's catalog ([OQ-ML4](#OQ-ML4)); a tombstone ([ML-D10](#ML-D10)) works at each layer. **How it differs from the old A:** that option shared the facts and treated any per-route copy as drift, failing a test on a repeated value. The ruling keeps sharing but lets each route state its own price, context size or output limit, because providers can differ for the same model. The owner: *"It would be nice to be able to write these facts once, but I'm not sure we can guarantee this is always true. Different providers may have different prices, context sizes, whatever for a model. There's no guarantee they're the same. We still need that possibility."* Rejected: sharing in the menu file only (B), no sharing (C), and sharing with no per-route override (the old A). Mechanism: [ML-D11](#ML-D11) | 2026-10-16 | [How a pi model row gets its facts](#oq-ml4-ml5-setup) | ✅ Built 2026-10-16 ([ML-D12](#ML-D12)): the top-level `model_facts`, named by a model entry's `extends`, laid under each route's own facts (`packload.liftModelFacts`); full rows by default, the shared form for a pi that declares it reads it, a fork's declaration included ([ML-D14](#ML-D14)). `TestRoutesInheritSharedFactsAndOverrideThem`, `TestPiRoutesShareFactsAsFullRowsByDefault`, `TestPiWritesTheSharedFormOnlyForAPiThatReadsIt`, `TestModelFactsAndExtendsValidate`. Not built: a pack cannot declare shared facts of its own, only point a `model_options.<alias>.extends` at the user's |
| <a id="ML-D10"></a>ML-D10 | *Implementation decision, reversible, building [OQ-ML4](#OQ-ML4) and [OQ-ML5](#OQ-ML5).* **A tombstone is the fact written as `null`, in the same places a fact is written today: a user's object-form `models.<alias>` entry, a pack's `model_options.<alias>`, and the shared facts.** It means "this model has no such fact", and it beats every layer below it, pi's catalog included. Leaving the fact out means inherit. Why `null`: yolo's config already spells removal that way (a `null` under a provider's `models` removes a model, and the compose fold deletes a key set to `null`, per [config surfaces](../reference/pack-system.md#config-surfaces-and-the-compose-engine)), while an empty value cannot be the spelling because some empty values are real facts or are already refused (`reasoning: false`, an empty `input`). One consequence: a user's `null` over a pack's fact now removes the fact rather than falling back to the catalog; to get the catalog's value back, the user restates it. In a rendered `models.json` row a tombstoned fact is left out of a full row, so pi applies its own default for a missing field. **What the build needs:** the `null` must survive the config merge and the pack lowering to reach the derive as a removal; today the model-entry validator only skips a `null` fact (`validateModelEntry`) | 2026-10-16 | [How a pi model row gets its facts](#oq-ml4-ml5-setup) | ✅ Built 2026-10-16: the config merge keeps the null (`TestAModelFactNullSurvivesTheConfigMerge`); a user's object-form null, a pack's `model_options` null (`packdecl.ModelFacts`) and a `model_facts` null lower as nulls (`packload.overlayFacts`; `TestAUserNullOverAPackFactIsCarriedAsATombstone`, `TestAPackNullFactIsCarriedAsATombstone`); each derive reads one as `ctx.tombstone` under `model_options.<alias>` and `model_shared.<name>` (`luahook.markModelFactTombstones`); pi leaves the field out of a `models.json` row and names it in a menu row's `tombstones` |
| <a id="ML-D11"></a>ML-D11 | *Implementation decision, reversible, building [OQ-ML5](#OQ-ML5).* **yolo writes the shared form (`sharedModels` holding the facts once, a row saying `extends`) only to a reader that reads it, and full merged rows everywhere else.** The form is the downstream consumer's own pi build's. MEASURED 2026-10-16 on stock pi 1.0.1 (`@earendil-works/pi-coding-agent`, the copy installed in this jail), by loading a `models.json` through its own loader (`ModelConfig.load`): a top-level `sharedModels` and a row's `extends` load with no error and are ignored, because pi's file schema accepts unknown keys. So a stock pi handed the shared form does not refuse it; it silently gives the row pi's generic sizes and loses the facts. Stock pi's own per-field layer is `modelOverrides`, which applies over a model pi already has and cannot express a tombstone. The menu file is read by yolo's own extension, which does not read the shared form either today, so the same rule applies there until it does. **How yolo would know:** nothing reaches the derive today that says which pi will run; no pi version or capability is in its context. The nearest precedent is `built_in_providers`, a fact `packs/pi/pack.json` declares and core hands the derive. The build needs one like it, declared by the pack that ships the pi build reading the shared form; with no such declaration, yolo writes full rows | 2026-10-16 | [How a pi model row gets its facts](#oq-ml4-ml5-setup) | ✅ Built 2026-10-16: `reads_shared_models` on a `program` (`packdecl.Contribution.ReadsSharedModels`), read by bin ownership into ctx.reads_shared_models (`packload.ReadsSharedModels`); packs/pi does not declare it, so stock pi gets full rows. The menu file always gets full rows, beside the shared form's keys ([ML-D14](#ML-D14)) |
| <a id="ML-D12"></a>ML-D12 | *Implementation decision, reversible, building [OQ-ML4](#OQ-ML4), [OQ-ML5](#OQ-ML5), [ML-D10](#ML-D10) and [ML-D11](#ML-D11).* **The merge with pi's catalog runs in yolo's pi extension, and the shared facts are a top-level `model_facts` map a model entry names with `extends`.** (1) *Where the merge runs.* The derive cannot see pi's catalog, and yolo shipping catalog facts it read would be a second copy of pi's catalog to keep current, so the extension, which already reads the catalog (`getBuiltinModel`), does it. A `models.json` row still carries what yolo states, the fallback if the extension does not load; where pi has a catalog for the row's provider, pi's own Bedrock client, the same rows also ride `yolo-model-lists.json` marked `keep_catalog`, and the extension registers pi's catalog for the provider with each row merged over its own entry, so the menu is not narrowed. (2) *Tombstones on the wire.* The composed table carries a null fact as a null under `model_options.<alias>` and `model_shared.<name>`, and the derive context sets `ctx.tombstone` there, since a Lua table cannot hold nil and an absent key means inherit. A menu row names its tombstoned pi fields in `tombstones`, not as nulls, because the computed layer reads a null as "delete this key" and the host render strips nulls; the extension gives a tombstoned field the default pi's `models.json` loader gives a row that states none, or removes it when there is none. (3) *The shared facts' spelling.* A top-level `model_facts` map, each entry a model entry's facts minus what only a route can say (`id`, `vendor`, `wire_id`; `openrouter_routing` was here until the downstream fix 27 made it a shared fact, a route's own object replacing the shared one whole), named by `extends` on a user's object-form entry or a pack's `model_options.<alias>`; an `extends` naming no entry is refused. The composition lays the shared facts under the route's own (`packload.WithModelFacts`, passed where a launch composes the table its derives read: `run.composedProviders`, the macos-user variant, and `cli.composedHostProviders`). (4) *The shared form and tombstones.* In the shared form a fact a row leaves out inherits the shared one, so a row that tombstones a shared fact is written in full | 2026-10-16 | [How a pi model row gets its facts](#oq-ml4-ml5-setup) | ✅ `packs/pi/derive.lua` (`piShareRow`, `pi/model-lists`), `packs/pi/home/.pi/agent/extensions/yolo-model-lists.js` (`definition`, `withCatalog`), `packload.liftModelFacts`, `config.validateModelFacts` |
| <a id="ML-D13"></a>ML-D13 | *Implementation decision, reversible, building [OQ-ML4](#OQ-ML4)'s `models.json` half, which [ML-D12](#ML-D12) left to the extension.* **On pi's own Bedrock client, a row naming a catalog id is written to `models.json` as pi's `modelOverrides` entry holding the facts it states, not as a `models` row.** MEASURED 2026-10-17 on stock pi 1.0.1 by loading the rendered file through its own `ModelConfig` and composing `amazon-bedrock` with its own composer and catalog: a `models` row replaces the catalog entry of its id, so `global.openai.gpt-6-luna` lost the catalog's `thinkingLevelMap` (`xhigh`), while a `modelOverrides` entry merges over it field by field (`applyModelOverride`) and keeps it, and applies after any extension's registration too. A row naming a `base` or a `wire_id` (an id the catalog lacks, by its own declaration) or carrying a tombstone (which a merge over the catalog cannot express) stays a full `models` row. **Cost:** an id pi's catalog lacks that says none of these is listed only through the `keep_catalog` registration ([ML-D12](#ML-D12)), so a pi without yolo's extension does not offer it; with the extension it is offered as before | 2026-10-17 | [How a pi model row gets its facts](#oq-ml4-ml5-setup) | ✅ `packs/pi/derive.lua` (the native Bedrock row); `TestPiOnBedrockKeepsTheCatalogsThinkingLevelsInModelsJSON`, which also runs stock pi's composer where pi is installed |
| <a id="ML-D14"></a>ML-D14 | *Implementation decision, reversible, completing [ML-D11](#ML-D11).* **(1) A fork's `reads_shared_models` is the one that counts: the fork rewrite gives the base's program the fork's value, replacing the base's.** What a program reads is a fact about its bytes, and a fork (`fork_of`) supplies them. Before, the rewrite kept the base's value with the rest of what the program does, so a downstream consumer's patched pi declaring it on its fork pack did not switch the render (reported 2026-10-17). yolo names no fork: any fork of `pi` declaring it switches pi's render. **(2) The menu file carries the shared form's keys beside full rows:** for a pi that reads the shared form, `yolo-model-lists.json` holds the same top-level `sharedModels` and each row whose alias `extends` shared facts says so, while every row keeps its facts in full, because yolo's own extension is that file's reader and resolves no `extends`. **(3) An override row ([ML-D13](#ML-D13)) stays full in `models.json`:** the shared form is written for `models` rows only, since nothing says a pi reading `extends` reads it on a `modelOverrides` entry; so on pi's own Bedrock client the shared facts reach `models.json` only through rows naming a `base`, a `wire_id` or a tombstone | 2026-10-17 | [How a pi model row gets its facts](#oq-ml4-ml5-setup) | ✅ `packload.forkedProgram`, `packs/pi/derive.lua` (`piMarkShared`); `TestAPiForkDeclaringReadsSharedModelsSwitchesTheRender`, `TestAStockPiMenuFileCarriesNoSharedForm` |
| <a id="ML-D15"></a>ML-D15 | *Implementation decision, reversible, under [OQ-ML4](#OQ-ML4)'s "don't drop things unnecessarily" and [MM-D21](#MM-D21).* **yolo's pi registration keeps a provider another pi extension registered before it, and adds only yolo's declared rows whose ids that registration lacks; a list the profile enforces still replaces it, and says so, only where pi can actually refuse outside it.** *The problem*, reported by a downstream pack: its extension registers `amazon-bedrock` rows of its own (long-context `[1m]` variants with their own prices, and a request hook), and pi applies yolo's `keep_catalog` registration of the same provider later. pi lays a re-registration's defined keys over the previous one's, so its `models` replaces the list whole (pi 1.0.1 `ModelRuntime.registerProvider`), and every variant row was lost. *Options weighed.* **Yield** (skip a provider already registered) drops yolo's declared rows. **Skip the `keep_catalog` entry when it adds nothing** fixes only the case where every row restates the catalog, which a row missing pi's thinking levels does not. **Merge**, chosen, keeps both sides: the other extension's rows exactly as it registered them, its other keys (pi's own key merge keeps its `api` and request hook, since yolo's config then names only `models`), then each of yolo's declared rows, merged over pi's catalog as [ML-D12](#ML-D12) built, whose id it lacks. pi's catalog filler is not added back: the other extension replaced pi's list by its own choice. *Mechanism.* An extension cannot read another's registration through pi's extension API: before pi binds the extensions, registrations are queued write-only (`core/extensions/loader.js`). The one place both are visible is pi's model runtime as it applies a registration, where `getRegisteredProviderConfig` returns the one before it. So the extension wraps `ModelRuntime.prototype.registerProvider` once per process, imported from `@earendil-works/pi-coding-agent` through pi's own extension module aliases, and the wrapper acts only on a config carrying yolo's marker symbol; every other registration passes through untouched. Where pi does not export `ModelRuntime` with both methods, the registration is yolo's list as before. A marker on the registered array records what the other extension had, so a second registration of yolo's merges with that and not with yolo's own earlier rows. *Enforced lists.* A list the profile enforces is the list pi must refuse outside ([MM-D21](#MM-D21)), so where the extension can install that refusal it replaces the other extension's models and hook as before, and the first session start says so, naming `"enforce_models": false` as the way to keep them. An enforced list it cannot refuse outside ([MM-D21](#MM-D21)'s two-api branch, such as `github-copilot`'s, whose models run on three pi apis) merges as an unenforced one does: replacing there would take away the other extension's rows, a pack's `[1m]` copies among them, and buy no refusal. Its one warning is that pi cannot refuse there (2026-11-10). *Not covered:* an extension that registers the same provider *after* yolo, or registers a native provider (pi applies those after every config registration), still replaces yolo's list whole, by pi's own rule; and an earlier extension's `refreshModels` or `oauth.modifyModels`, which pi's key merge keeps, can replace the merged list when it runs. MEASURED 2026-10-17 with a library load of pi 1.0.1 (`loadExtensions` and a real `ModelRuntime`, offline): the class an extension imports is the one pi runs; with the previous extension a pack's two `amazon-bedrock` rows were gone and pi's whole catalog was the list, and with this one the list was the pack's two rows then yolo's row, with the pack's request hook kept | 2026-10-17 | [How a pi model row gets its facts](#oq-ml4-ml5-setup) | ✅ `packs/pi/home/.pi/agent/extensions/yolo-model-lists.js` (`installMerge`, `foreignModels`); `TestPiModelListsKeepAnEarlierExtensionsRegistration`, `TestPiModelListsWithNoEarlierRegistrationAreUnchanged`, `TestPiEnforcedModelListReplacesAnEarlierRegistrationAndSaysSo`, `TestPiUnenforceableModelListKeepsAnotherExtensionsRows` |

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
