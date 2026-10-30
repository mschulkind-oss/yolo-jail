---
title: "Models in the new config: shared facts, names, twins, and a conversion that proves itself"
date: 2026-10-30
status: in-review
stage: DESIGN
next: "Rule OQ-MF1 (whether a model keeps a short name of its own); the build waits on it"
tags: [config, models, providers, migration, toml, model-facts, twins, design]
summary: "The maintainer's personal config declares its models four ways the TOML config cannot say yet: facts written once and shared by a model's routes, a short name for a long wire id, a 300k twin that sends its sibling's id, and facts added to a pack's `default`. This doc gives each a TOML spelling (existing where one exists, one new line otherwise), the migration's conversion rule, and what the equality proof compares, so `yolo config migrate` converts that file with nothing left over. Part of the readable-config redesign."
---

# Models in the new config: shared facts, names, twins, and a conversion that proves itself

**Status:** in review, 2026-10-30. Design only; nothing here is built. The evidence is a dry run of
`yolo config migrate` over the maintainer's own user config on 2026-10-30, read against the
composition and the converter at the current tree ([appendix B](#appendix-b-evidence)).

**How to read this.** [§1](#1-what-the-file-says) is the five things the file says about its models,
[§2](#2-where-the-toml-stops-today) where the TOML stops, [§3](#3-the-spellings-and-their-proofs) the
spelling, conversion rule and proof for each. [§4](#4-what-is-deliberately-not-said) names what stays
unsaid. The lane decisions are in the [ledger](#decision-ledger); the one owner question is
[OQ-MF1](#OQ-MF1). The worked example is [appendix A](#appendix-a-the-maintainers-model-sections-converted).

**Needs your ruling:** [OQ-MF1](#OQ-MF1).

> **In short.** Every model construct in the file already has a readable word except two: a short
> name for a model, and a row that sends another row's id while keeping its own facts. The first
> is a one-line spelling packs already write (`default = "qwen-3.8-27b"`); the second is `wire_id`,
> which the row vocabulary already decodes. Shared facts keep their ruled name, `model_facts`, in
> TOML. With those three admitted, the migration converts the whole file and the provider table a
> launch resolves comes out equal, with one derived fact (the maker an id names) compared as derived.

**Reads with:** [`readable-config.md`](readable-config.md) (front door and ledger),
[`config-migration.md` §4](config-migration.md#4-what-yolo-config-migrate-does) (what the migration
proves), [`model-lists-and-pickers.md`](model-lists-and-pickers.md#oq-ml4-ml5-setup) (how a model
row gets its facts; the shared-facts ruling), [`pack-manifests.md` §4](pack-manifests.md#4-flags-that-add-and-a-model-said-once)
(a model said once, `every_model`, `twin_of`).

---

## 1. What the file says

The file is `~/.dotfiles/tag-personal/config/yolo-jail/personal.jsonc`, 496 lines, included by the
user config on personal machines. Its model sections say five things, each in the legacy
vocabulary (an object under `providers.<name>.models.<alias>` carrying an `id`):

| What it says | How the file spells it | Where |
| :--- | :--- | :--- |
| **Facts written once, shared by a model's routes.** Two entries, one per OpenRouter tier (`:floor`, `:nitro`), each with `base`, `name`, `reasoning`, `input`, `cost`, `context_window`, `max_tokens` and an `openrouter_routing` pin; each entry is named by two rows, a 1M row and its 300k twin | top-level `model_facts.<name>`, a row's `extends` | the OpenRouter rows |
| **A short name for a long wire id.** `deepseek-floor` for `deepseek/deepseek-v4.1-flash-1017:floor`, `mimo` for `xiaomi/mimo-v2.6-flash`, `default` for `deepseek-flash` | the row's key, with `id` the wire id | every provider |
| **A twin: one provider, one wire id, two windows.** `deepseek-floor-300k` has its own client id (`…:floor@300k`), sends its sibling's (`wire_id`), shares its facts (`extends`) and overrides `name` and `context_window` | `wire_id` beside `id` and `extends` | the two 300k rows |
| **Facts added to a pack's `default`.** The cerebras pack ships `default` as an alias for `qwen-3.8-27b`; the file writes the same alias with the same id and six facts | `models.default = { id = "qwen-3.8-27b", … }` | cerebras |
| **The provider's starting model named by its short name.** `deepseek-floor`, `default` | `options.model` | deepseek, openrouter |

The file's own comments say why each is shaped so: the pin rides in the shared entry so a tier and
its twin cannot split; the twin is a row of `openrouter` itself, not a second provider; `max_tokens`
is load-bearing for routing. Those are the owner's reasons, and the TOML has to keep each one
sayable. The owner ruled the first of them explicitly: facts may be written once and shared by a
model's routes, and every route's row may override any shared fact
([OQ-ML5](model-lists-and-pickers.md#oq-ml4-ml5-setup), 2026-10-16: *"It would be nice to be able
to write these facts once, but I'm not sure we can guarantee this is always true. Different
providers may have different prices, context sizes, whatever for a model."*).

Terms used from here on. A model's **facts** are what an agent needs to know about it besides
its id: name, price, context size, output limit, input types, whether it reasons, its catalog
`base`, its OpenRouter pin ([the ruling's definition](model-lists-and-pickers.md#oq-ml4-ml5-setup)).
A **row** is a readable model entry, keyed by the id it sends, with no `id` field
([RC-D29](readable-config.md#RC-D29)). A **legacy row** is the object form with an `id`. The
**provider table** is the map `packload.ComposeProviders` resolves for a launch, which every
agent's derive reads; the **proof** is `yolo config migrate`'s equality check, which compares that
table from the old files and from the files it would write and refuses to write on any difference
([config-migration.md §4](config-migration.md#4-what-yolo-config-migrate-does),
[RC-D30](readable-config.md#RC-D30)).

## 2. Where the TOML stops today

The dry run converts the user config's other keys and refuses the models in 33 lines, every model
key of the file among them ([appendix B.1](#b1-the-dry-run)). The refusals fall into four gaps, and
the fourth is one the dry run cannot see yet:

1. **A row keyed by a name that is not its id has no readable spelling.** `readableFromLegacy`
   converts a legacy row only when its key equals its `id`; RC-D30 (2) left every other row
   unconverted on purpose. The readable schema types `providers.*.models.*` as an object, so the
   string shorthand packs write (`models = { default = "qwen-3.8-27b" }` in `packs/cerebras/pack.toml`)
   is refused in a user TOML. Twelve of the file's thirteen rows are keyed by a name.
2. **`model_facts` is a removed root of the readable schema**, so the TOML refuses it outright
   (`"it has no spelling in the TOML config"`), and the rename table points it and a row's
   `extends` at `providers.*.every_model`. `every_model` is one provider's facts for every row of
   that provider; `model_facts` is one model's facts for every route of that model, on any
   provider. They are not the same thing, and the file's two entries cannot be one `every_model`:
   they differ in `name` and in their pin, and `mimo` shares neither.
3. **`wire_id` is refused in TOML**, its rename row naming `twin_of`. The row decoder already reads
   it (`packdecl.ProviderModel.WireID`, honored by `composeProviderModels`); only the schema and the
   rename table stand in the way. `twin_of` does say what the file's twins mean (take the named
   row's facts whole, send its id), but not what every `wire_id` row could mean.
4. **The maker an id names.** A row keyed by its id infers its `vendor` from a `<maker>/` prefix
   (`inferProviderMaker`), and the flat facts carry it. A legacy row with no `vendor` carries none.
   So a converted `deepseek/…` or `xiaomi/…` row gains a `vendor` the old table lacked, and the proof
   would refuse the conversion of every OpenRouter row even once gaps 1 to 3 are closed. The
   existing conversion never met this because its fixture rows write their maker
   (`legacyBedrockRows` in `internal/configmigrate/models_test.go`).

Gap 4 is the one that makes "zero not-converted lines" a design question rather than a schema
edit: the readable vocabulary infers a fact the legacy one left unsaid, and the proof has to say
which of the two is the launch's truth.

## 3. The spellings, and their proofs

One subsection per construct. Each gives the TOML spelling, the conversion rule
(`internal/configmigrate`), and what the proof compares. The rulings each rests on are cited;
where this doc decides, the ledger row is named.

### 3.1 A name for a model

**Spelling.** A string under `models` is a **name** for the id it holds, the same line every
shipped pack writes and the only line the composition already leaves untouched as "the
shorthand" (`liftModelFacts`: *"A string alias is left untouched"*):

```toml
[providers.openrouter.models]
deepseek-floor = "deepseek/deepseek-v4.1-flash-1017:floor"          # a name for the row below
"deepseek/deepseek-v4.1-flash-1017:floor" = { extends = "deepseek-floor-1017" }
```

The readable schema admits the string (`providers.*.models.*` becomes string-or-table,
[MF-D1](#decision-ledger)). The facts still live in exactly one place, the row keyed by the id;
the name adds nothing but a word. That keeps [config-user.md §3](config-user.md#3-principles)'s
second principle, *the key is the id*, for every fact.

**What a name means to the launch.** A name that holds the id of a row in the same provider lists
that row under the name: the row's composed facts, pin and wire id are laid under the name's key in
`model_options` and `model_routing`, and the id itself is not listed a second time. Two names for
one row list it twice, as two legacy aliases for one id did. A name whose id has no row is what it
is today, an alias with no facts. A name is listed by its own spelling, as a legacy alias is, and
takes no place in the row order ([RC-D30](readable-config.md#RC-D30) (3)), so a converted file
lists its models exactly where the old one did ([MF-D1](#decision-ledger)).

This is also the only spelling that says *add facts to the pack's `default`*. The refusal
[RC-D29](readable-config.md#RC-D29) (7) built offers two remedies: write `id = "qwen-3.8-27b"`,
which the TOML refuses (the `id` key is in the rename table), or key the row by `qwen-3.8-27b`,
which today lists a second model beside the pack's `default` and leaves `default`, the pack's
`options.model`, without the facts. With names, the second remedy is exactly right: the pack's
`default = "qwen-3.8-27b"` names the user's row, and the table is the old one. The refusal's
message is rewritten to name only that remedy ([MF-D1](#decision-ledger), under
[RC-D40](readable-config.md#RC-D40)).

**Conversion.** A legacy row whose key is not its `id` converts to a name line `<key> = "<id>"`
and a row keyed by the id with the row's facts respelled as [RC-D30](readable-config.md#RC-D30)
(2) respells them. Two legacy rows naming one id with different facts cannot both become the one
row, and are reported not converted naming both keys. A name line that a selected pack already
ships, same name and same id, is not written: the pack's line names the row
([MF-D1](#decision-ledger); a convention in the sense of
[config-user.md §8](config-user.md#8-lines-you-never-write), proved like a dropped default).
The file's `default` for cerebras is that case; its `default` for deepseek, a provider no pack
ships, keeps its line.

**Proof.** Byte-equal: the table's `models`, `model_options` and `model_routing` keep their
alias keys, and `(model order)` is unchanged because a name takes no order.

Whether a model keeps a short name at all is the one question this doc puts to the owner:
[OQ-MF1](#OQ-MF1). Everything else in this section is the mechanism if the answer is yes.

### 3.2 Facts shared by a model's routes

**Spelling.** `model_facts` keeps its ruled name and becomes a readable top-level table, user
scope, each entry carrying the readable fact spellings (`accepts`, `price`, `context`), the
catalog `base`, and `openrouter_routing`; a row names one with `extends`
([MF-D2](#decision-ledger)):

```toml
[model_facts.deepseek-floor-1017]
base = "deepseek/deepseek-v4.1-flash"
name = "DeepSeek Floor"
reasoning = true
accepts = ["text", "image"]
price = { input = 0.098, output = 0.294, cache_read = 0.00294, cache_write = 0 }
context = 1_048_576
max_tokens = 131_072
openrouter_routing = { order = ["morph", "streamlake", "cloudflare"], allow_fallbacks = false }
```

The name is [ML-D12](model-lists-and-pickers.md#ML-D12)'s and the schema already admits `extends`
on a row (`providers.*.models.*.extends` is a readable schema key). An entry may say only what
every route of the model shares, so it refuses what only a route can say: `wire_id`, `twin_of`,
`compact_at`, and `extends` itself, the same exclusion `lowerEveryModel` applies. An entry lowers
into the flat vocabulary through the same projection a row and `every_model` take, so one
spelling cannot lower two ways.

**Why not `every_model`.** The rename table's `model_facts.* → providers.*.every_model` can
never hold: `every_model` is per provider and under every row, `model_facts` is per model and
across providers (the ruling's own example is one model on Bedrock and on OpenRouter), and a
fold that dropped `extends` changes the table (`model_options.<alias>.extends` and
`model_shared` leave it) and so can never prove equal. For the maintainer it would also change
what pi reads: his pi fork declares `reads_shared_models`, so `models.json` carries
`sharedModels` and `{ id, extends }` rows ([ML-D11](model-lists-and-pickers.md#ML-D11),
[ML-D14](model-lists-and-pickers.md#ML-D14)); full rows mean the same model but a different file.
`every_model` stays what it is, one provider's facts under all of its rows, and the two compose
as [RC-D29](readable-config.md#RC-D29) (5) says.

**The pin is one fact.** `openrouter_routing` in an entry replaces whole and never merges key by
key, in a readable row as in a legacy one: `MergeProviderModel` replaces the object, and
`laySharedRouting` lays the shared pin only under a route that stated none. The file's comments
rest on that, and nothing here moves it.

**Conversion.** Each entry's keys are respelled as a row's are (`input → accepts`, `cost → price`,
`context_window → context`), `extends` on a row passes through, and a `null` fact in an entry has
no TOML spelling and is reported, as every null is. The rename rows for `model_facts.*` and
`providers.*.models.*.extends` become identity rows, so the TOML accepts both and the converter
passes them ([MF-D2](#decision-ledger)).

**Proof.** Byte-equal: `model_options.<alias>` carries the same `extends`, `model_shared.<name>`
the same flattened facts, `model_routing.<alias>` the same pin.

### 3.3 A twin: one wire id at two windows

**Spelling.** `twin_of` where it says what the file means, `wire_id` where it does not
([MF-D3](#decision-ledger)):

```toml
"deepseek/deepseek-v4.1-flash-1017:floor@300k" = { twin_of = "deepseek/deepseek-v4.1-flash-1017:floor", name = "DeepSeek Floor (300k)", context = 307_200 }
```

`twin_of` takes every fact of the named row, its `extends` and its pin included, and sends its id
([pack-manifests.md §4](pack-manifests.md#4-flags-that-add-and-a-model-said-once),
[RC-D29](readable-config.md#RC-D29) (3)); the twin then states what it alone decides. That is the
file's own description of its twins (*"a twin states only `name` and `context_window` and inherits
everything else, upstream order included"*), said in one key instead of two, and it is what makes
the owner's rule hold by construction: a tier and its twin cannot split, because the twin has no
facts of its own to drift.

`wire_id` is admitted on a readable row for the twin `twin_of` cannot say: one whose sibling has
facts of its own that the twin did not inherit (under `extends` only the shared entry was
inherited), or whose sibling is a pack's name rather than a row. The decoder reads it today; the
schema gains the key and the rename row goes. A row with both keys sends its `wire_id`, the
composition's existing order (`row.WireID` wins over the inherited id).

**`context`, not `compact_at`.** The file overrides `context_window`, the declared window, and
the flat vocabulary has one key for the declared window and the compaction window alike
([RC-D29](readable-config.md#RC-D29) (4)). The conversion respells it `context`, which is what
the file said. `compact_at = 307_200` would lower to the same flat `context_window` and is the
readable word for *a narrower working budget on the same wire id*; the owner may switch by
hand, and the table does not move.

**Conversion.** A legacy row with `wire_id` becomes a `twin_of` row when a row of the same
provider has that id and the proof holds for the result; otherwise it becomes a row with
`wire_id` (and its `extends`, if any), which is the legacy row respelled and always proves.
The plan tries `twin_of` first and plans again with `wire_id` on a difference, the way it drops
folds ([RC-D30](readable-config.md#RC-D30) (4), [RC-D36](readable-config.md#RC-D36)); the
transcript names each twin it wrote as `twin_of`.

**Proof.** Byte-equal for the file: the twin's flat facts are the sibling's with `name` and
`context_window` overridden, `wire_id` is the sibling's id (`WireIDAuthored` is set by
`twin_of`), `extends` and `base` are inherited so `model_shared` is unchanged, and its pin
comes from the shared entry as before.

### 3.4 The starting model named by a name

`options.model = "deepseek-floor"` passes through the conversion today (the dry run does not
list it) and keeps meaning the name, which [§3.1](#31-a-name-for-a-model) keeps. Nothing to
convert ([MF-D4](#decision-ledger)). The readable provider-level `model` key exists for a
profile's starting model ([RC-D32](readable-config.md#RC-D32)); the file's profiles already
convert to `variants` with their literal ids and are not this doc's subject.

### 3.5 The maker an id names

**The fact.** A row's `vendor` is the maker whose model it is; it is read by each derive's
`callableModels` (offered to every agent when absent, filtered by a maker set only on Bedrock
for claude and codex) and by the wire bridge's Anthropic routing (`anthropicModelIDs`). A readable
row infers it from a `global.<maker>.` prefix or a `<maker>/` route and leaves it unwritten
([RC-D30](readable-config.md#RC-D30) (2) calls a vendor the id names *derived*); a legacy row
carries only what it wrote.

**Proof.** The provider-table layer compares a vendor the id names as derived on both sides: a
legacy `model_options.<alias>` with no `vendor` whose model id names a maker is compared as if
it carried that maker ([MF-D5](#decision-ledger)). It is the same equivalence RC-D30 (2) already
applies when it drops a written vendor, read in the other direction. The plan's transcript names
each row this touched (`vendor "deepseek" follows from the id`), because the launch after
migration does carry it. For the file's providers no reader acts on it: every `callableModels`
call on openrouter, deepseek and cerebras passes no maker set, and the bridge routes on
`anthropic` alone. On a Bedrock provider a legacy row gaining its true maker would be withdrawn
from an agent whose client cannot call that maker, which is the ruled behavior for the pack's
own rows ([OQ-BR9](bedrock-plumbing.md#decision-ledger)) and is named in the transcript when it
happens.

### 3.6 What the proof compares, in one place

| Layer | Old side | New side | Equal when |
| :--- | :--- | :--- | :--- |
| the config layers (user, project, merged) | the old files lowered, folds undone | the written files read back | every key the lowering converts matches; `model_facts` and `extends` pass through |
| the provider table the launch resolves | `ComposeProviders` over the old merged read, with a vendor the id names filled in ([§3.5](#35-the-maker-an-id-names)) | the same over the written files | `models`, `model_options`, `model_routing`, `model_shared`, `variants`, `options` and `(model order)` are byte-equal per provider |

A plan that fails the table with `twin_of` is planned again with `wire_id`; one that fails with
folds, without them ([RC-D30](readable-config.md#RC-D30) (4)); one that still fails writes nothing
and prints the difference key by key, as today.

## 4. What is deliberately not said

- **A tombstone.** A fact written as `null` ([ML-D10](model-lists-and-pickers.md#ML-D10)) has no
  TOML spelling, in a row, in `every_model` or in `model_facts`; the converter reports it as it
  reports every null. The file writes none. Whether `false` says "none" for a model fact, as it
  does in a manifest ([RC-D25](readable-config.md#RC-D25)), is a question for the doc that owns
  tombstones, not this one.
- **A twin of a pack's name.** `twin_of` names a row, and a pack's `default` is a name, so a user
  cannot twin a pack's model at a narrower window without writing the pack's id. `wire_id` on a
  row keyed by a client id of the user's own says it; the file does not need it.
- **A fold of shared facts into `every_model` or into the rows.** Never made by the migration: it
  changes the table and, for a pi that reads the shared form, the file pi reads. The owner may
  simplify by hand; [appendix A.2](#a2-what-the-owner-could-write-afterwards-by-hand) shows what the
  file becomes and what that changes.
- **Dropping a name because nothing refers to it.** A name is shown by opencode's and codex's
  menus and may be the spelling in `options.model`; the migration keeps every name the file
  wrote, and drops only one a selected pack already ships ([§3.1](#31-a-name-for-a-model)).
- **The maintainer's own shape test.** `tests/yolo-config.test.sh` in the dotfiles reads
  `personal.jsonc` and asserts the legacy shapes; it is his to move once the file is TOML, and
  nothing here reads it.

## Decision Ledger

Lane decisions, 2026-10-30, each reversible and each citing the ruling it builds. Nothing is
built; the Built column is empty on purpose.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="MF-D1"></a>MF-D1 | **A string under `models` is a name for the id it holds; a name whose id has a row in the same provider lists that row under the name, takes no row order, and is not listed again under the id. The schema admits the string in user config; the migration writes a name line and an id-keyed row for a legacy row keyed by a name, and omits a name a selected pack ships with the same id. The RC-D29 (7) refusal names only the id-keyed row.** Building [RC-D29](readable-config.md#RC-D29) (2), (7) and [RC-D30](readable-config.md#RC-D30) (2), (3). The line is the one packs already write; the facts stay in one place, the row; and it is the only spelling of "add facts to the pack's `default`". Conditional on [OQ-MF1](#OQ-MF1) | 2026-10-30 | [§3.1](#31-a-name-for-a-model) | — |
| <a id="MF-D2"></a>MF-D2 | **`model_facts` keeps its name as a readable top-level table with the row fact spellings, refusing route-only keys; `extends` stays a row key; both rename rows become identity rows and the converter passes both through.** Building [OQ-ML5](model-lists-and-pickers.md#oq-ml4-ml5-setup), [ML-D12](model-lists-and-pickers.md#ML-D12), [ML-D14](model-lists-and-pickers.md#ML-D14). `every_model` is per provider, `model_facts` per model across providers, and a fold into `every_model` changes `model_options.*.extends` and `model_shared`, so the rename row's fold could never prove equal and would change what a shared-form pi reads | 2026-10-30 | [§3.2](#32-facts-shared-by-a-models-routes) | — |
| <a id="MF-D3"></a>MF-D3 | **A legacy `wire_id` row converts to `twin_of` when a row of the same provider has that id and the proof holds; otherwise to a row with `wire_id`, which the schema admits and whose rename row goes. `context_window` on a twin respells to `context`.** Building [pack-manifests.md §4](pack-manifests.md#4-flags-that-add-and-a-model-said-once), [RC-D29](readable-config.md#RC-D29) (3), (4), the retry pattern of [RC-D30](readable-config.md#RC-D30) (4). `twin_of` is what the file's twins mean and makes "a tier and its twin cannot split" structural; `wire_id` is what the decoder already reads and covers the twin `twin_of` cannot say | 2026-10-30 | [§3.3](#33-a-twin-one-wire-id-at-two-windows) | — |
| <a id="MF-D4"></a>MF-D4 | **`options.model` naming a name is left as written.** It converts today and keeps its meaning under MF-D1 | 2026-10-30 | [§3.4](#34-the-starting-model-named-by-a-name) | — |
| <a id="MF-D5"></a>MF-D5 | **The proof's provider-table layer compares a vendor the id names as derived: a legacy row with no `vendor` is compared as if it carried the maker `InferModelVendor` gives its id, and the transcript names each such row.** Building [RC-D30](readable-config.md#RC-D30) (2)'s "a vendor the id names is derived" and [OQ-BR9](bedrock-plumbing.md#decision-ledger). The one place the table is not byte-equal; without it no `<maker>/` row ever converts. The launch after migration carries the maker; no reader acts on it for a provider whose derive passes no maker set | 2026-10-30 | [§3.5](#35-the-maker-an-id-names) | — |

## Open Questions

1. 💬 **OQ-MF1: Should a model keep a short name of its own in the new config?**

   Matt runs `yolo config migrate`. His personal file names twelve of its thirteen models with a
   short word (`deepseek-floor`, `mimo`, `default`) and spells the wire id once under it; the
   cerebras pack ships `default` the same way. Today the dry run refuses every one of those rows,
   because a readable row's key is the id it sends and a name is not that. The new config has to
   say either "here is the id, and here is what I call it" or "the id is the name".

   - **A — A name stays a one-line word for an id.** The file keeps `deepseek-floor = "deepseek/…:floor"`
     beside the row keyed by the id, the same line every shipped pack writes. Menus, `options.model`
     and `-p` spellings read as before; adding facts to the pack's `default` is a row keyed by the
     pack's id, nothing more. Cost: two spellings for one model in one table, and the schema admits
     a string where it admits a table today.
   - **B — The id is the only name.** Every row is keyed by its id, `options.model` is respelled to
     the id, and the migration proves equality after resolving each old name to its id. The file
     loses twelve words and the config has one spelling. Cost: opencode's and codex's menus show
     the long ids; a pack's `default` cannot take user facts at all, since nothing can name that
     row but the pack's own line; and the converted file is harder to read than the old one.

   <!-- vantage: question id=OQ-MF1 leaning="A — the pack's `default` already is such a line, and B cannot say 'add facts to it'." -->

   _Leaning:_ A. The pack's `default` already is such a line, and B cannot say "add facts to it";
   the maintainer's own comments pick models by registered id where pi needs it and by name
   everywhere else.

   **Answer:**
   > _(empty — fill in when decided)_

## Implementation sketch

The seed of the implementation plan, in the `design-doc` skill's sense: what the design surfaced,
not a hand-off, and not to be built from until [OQ-MF1](#OQ-MF1) is ruled. The planner completes it
against the tree.

**Order.** Schema and validation first, so a hand-written file is accepted before the converter
writes one; then the composition, so the launch reads what the schema accepts; then the
converter; then the proof; then the end-to-end fixture.

1. **Schema and validation** (`internal/config/schema.go`, `renames.go`, `validatereadablemodels.go`,
   `readablevalidate.go`). Take `model_facts` out of the readable schema's removed roots and add
   `model_facts.*` with the fact keys `every_model` has, user scope. Type `providers.*.models.*`
   string-or-table (on [OQ-MF1](#OQ-MF1) A). Add `providers.*.models.*.wire_id`. Turn the
   `model_facts.*`, `providers.*.models.*.extends` and `providers.*.models.*.wire_id` rename rows
   into identity rows. Decode a `model_facts` entry through `packdecl.ProviderModel`, refusing
   `wire_id`, `twin_of`, `compact_at`, `extends`. Tests: `TestModelFactsIsAReadableTopLevelTable`,
   `TestAModelFactsEntryRefusesWhatOnlyARouteCanSay`, `TestAReadableRowAcceptsWireID`,
   `TestANameLineIsAStringUnderModels`. Check whether `readableUnknownMessage`'s rename branch still
   fires for `providers.*.models.*.id` and keep it; the id key stays refused.
2. **Composition** (`internal/packload/readablemodels.go`, `providers.go`). In `lowerReadableModels`,
   after the projection: for each readable row id, the names in `models` holding it (strings equal
   to the id, key not the id) take the row's `model_options` and `model_routing` under the name's
   key, and the id's own entries are removed when at least one name holds it; a name's facts carry
   no `order`. Lower a readable `model_facts` entry into the flat vocabulary with the
   `lowerEveryModel` projection before `layShared`, so `flattenModelFacts` (legacy spellings) and
   the readable path meet in one flat map; `WithModelFacts` takes either spelling. Rewrite
   `readableRowOverAlias`'s message to name the id-keyed row only. Tests:
   `TestANameForARowListsTheRowUnderTheName`, `TestTwoNamesForOneRowListItTwice`,
   `TestAPacksDefaultNamesTheUsersRow`, `TestReadableSharedFactsLayUnderARowThatExtendsThem`,
   `TestATwinOfRowInheritsExtendsAndItsPin`, `TestAWireIDRowSendsItAndKeepsItsOwnFacts`,
   `TestTheReadableRowOverAliasRefusalNamesTheRowToWrite`. The regression that must stay green:
   `TestProviderTableIsByteIdenticalForEveryExistingConfigAndShippedPack` (`internal/cli/run`).
3. **Conversion** (`internal/configmigrate/models.go`, `lower.go`). `readableFromLegacy` returns a
   name line and an id-keyed row when key and id differ; `extends` passes through; a `wire_id` row
   becomes a `twin_of` candidate (sibling row present) else a `wire_id` row; a name a selected
   pack ships with the same id is dropped and listed with the dropped defaults; two legacy rows
   naming one id with different facts are reported. `lower` gains a `model_facts` case respelling
   each entry's keys. Tests: `TestPlanConvertsANamedRowToANameAndARow`,
   `TestPlanDropsANameThePackAlreadyShips`, `TestPlanReportsTwoNamesForOneIDWithDifferentFacts`,
   `TestPlanConvertsAWireIDTwinToTwinOf`, `TestPlanKeepsWireIDWhenTwinOfWouldChangeTheTwin`,
   `TestPlanConvertsModelFactsAndExtendsUnchanged`. Extend `TestPlanLeavesAnAliasedLegacyRowNotConverted`
   into the first of these rather than deleting it: the pinned behavior is replaced, not dropped.
4. **Proof** (`internal/configmigrate/models.go` `providerTable`, `plan.go`). On the old side, fill
   a `vendor` the id names into each `model_options.<alias>` lacking one, recording the aliases
   for the transcript; add the `twin_of`-then-`wire_id` retry beside the fold retry, with the
   plan's reason line. Tests: `TestPlanComparesAVendorTheIDNamesAsDerived`,
   `TestPlanFallsBackToWireIDWhenTwinOfChangesTheTable`, and a transcript test naming both lines.
5. **The end-to-end fixture.** A test fixture shaped like [appendix A.1](#a1-what-yolo-config-migrate-writes)'s
   source: `model_facts` with two entries, a named row with `extends`, two `wire_id` twins, a
   full named row, a pack-aliased `default` with facts, a no-pack provider with `default`, and
   `options.model` naming a name. `TestPlanConvertsTheMaintainersModelSectionsWithNothingLeft`
   asserts `Equal`, an empty `NotConverted`, the written TOML's lines, and that `Apply` reads it
   back through the same proof. Then the dry run of the real file on the maintainer's machine, which
   only he can run; its output is the design's measurement.

**Not in this plan.** Tombstones in TOML; `twin_of` naming a pack's name; the `brokered → allow`
conversion of the user config, which another lane is converting now.

## Appendix A: the maintainer's model sections, converted

### A.1 What `yolo config migrate` writes

`~/.dotfiles/tag-personal/config/yolo-jail/personal.toml`, the model sections only, under
[OQ-MF1](#OQ-MF1) A. Comments are kept in order by the migration and are left out here. The
profiles are shown as RC-D32 converts them, for completeness; they are not this doc's subject.

```toml
[model_facts.deepseek-floor-1017]
base = "deepseek/deepseek-v4.1-flash"
name = "DeepSeek Floor"
reasoning = true
accepts = ["text", "image"]
price = { input = 0.098, output = 0.294, cache_read = 0.00294, cache_write = 0 }
context = 1048576
max_tokens = 131072
openrouter_routing = { order = ["morph", "streamlake", "cloudflare"], allow_fallbacks = false }

[model_facts.deepseek-nitro-1017]
base = "deepseek/deepseek-v4.1-flash"
name = "DeepSeek Nitro"
reasoning = true
accepts = ["text", "image"]
price = { input = 0.098, output = 0.294, cache_read = 0.00294, cache_write = 0 }
context = 1048576
max_tokens = 131072
openrouter_routing = { order = ["streamlake", "morph", "cloudflare"], allow_fallbacks = false }

[providers.deepseek]
api_key_env_name = "DEEPSEEK_API_KEY"
endpoints.openai.base_url = "https://api.deepseek.com"
endpoints.anthropic.base_url = "https://api.deepseek.com/anthropic"
options = { model = "default" }

[providers.deepseek.models]
default = "deepseek-flash"
"deepseek-flash" = { name = "DeepSeek Flash", reasoning = true, accepts = ["text", "image"], price = { input = 0.3, output = 1.2, cache_read = 0.006, cache_write = 0 }, context = 1048576, max_tokens = 384000 }

[providers.cerebras.models]
# `default` names this row: the cerebras pack ships `default = "qwen-3.8-27b"`, so the line is not written
"qwen-3.8-27b" = { name = "Qwen 27B", reasoning = true, accepts = ["text", "image"], price = { input = 0.99, output = 1.49, cache_read = 0, cache_write = 0 }, context = 65536, max_tokens = 32768 }

[providers.openrouter]
options = { model = "deepseek-floor" }

[providers.openrouter.models]
deepseek-floor      = "deepseek/deepseek-v4.1-flash-1017:floor"
deepseek-nitro      = "deepseek/deepseek-v4.1-flash-1017:nitro"
deepseek-floor-300k = "deepseek/deepseek-v4.1-flash-1017:floor@300k"
deepseek-nitro-300k = "deepseek/deepseek-v4.1-flash-1017:nitro@300k"
mimo                = "xiaomi/mimo-v2.6-flash"
"deepseek/deepseek-v4.1-flash-1017:floor"      = { extends = "deepseek-floor-1017" }
"deepseek/deepseek-v4.1-flash-1017:nitro"      = { extends = "deepseek-nitro-1017" }
"deepseek/deepseek-v4.1-flash-1017:floor@300k" = { twin_of = "deepseek/deepseek-v4.1-flash-1017:floor", name = "DeepSeek Floor (300k)", context = 307200 }
"deepseek/deepseek-v4.1-flash-1017:nitro@300k" = { twin_of = "deepseek/deepseek-v4.1-flash-1017:nitro", name = "DeepSeek Nitro (300k)", context = 307200 }
"xiaomi/mimo-v2.6-flash" = { name = "MiMo Flash", reasoning = true, accepts = ["text", "image"], price = { input = 0.14, output = 0.28, cache_read = 0.0028, cache_write = 0 }, context = 1048576, max_tokens = 131072 }

[providers.openrouter.variants.or]
model = "deepseek/deepseek-v4.1-flash-1017:floor"

[providers.openrouter.variants.or-300k]
model = "deepseek/deepseek-v4.1-flash-1017:floor@300k"
subagents = { default = "deepseek/deepseek-v4.1-flash-1017:floor" }

[providers.openai-codex.models]
"gpt-6.1-sol" = { name = "Sol 6.1" }

[providers.meta]
```

What each line proves against the old table: the five names list the five OpenRouter models under
their old keys in their old order; the two `extends` rows carry the same `extends`, `base` and
shared facts, and `model_shared` holds the same two entries; the two twins carry `wire_id` equal to
their sibling's id, the overridden name and window, and the sibling's pin; `mimo` carries its own
facts; `default` on deepseek and the pack's `default` on cerebras each list one row with the file's
facts; the OpenRouter and MiMo rows carry `vendor` `deepseek` and `xiaomi`, compared as derived
([§3.5](#35-the-maker-an-id-names)). Nothing folds into `every_model`: no fact is written alike on
every OpenRouter row once the twins and the `extends` rows state only what they decide.

### A.2 What the owner could write afterwards, by hand

The same models without `model_facts`, which the migration never writes because it changes the
table and the shared form pi reads. Shown so the choice is visible:

```toml
[providers.openrouter.every_model]
reasoning = true
accepts = ["text", "image"]
context = 1048576
max_tokens = 131072

[providers.openrouter.models]
deepseek-floor      = "deepseek/deepseek-v4.1-flash-1017:floor"
deepseek-nitro      = "deepseek/deepseek-v4.1-flash-1017:nitro"
deepseek-floor-300k = "deepseek/deepseek-v4.1-flash-1017:floor@300k"
deepseek-nitro-300k = "deepseek/deepseek-v4.1-flash-1017:nitro@300k"
mimo                = "xiaomi/mimo-v2.6-flash"

[providers.openrouter.models."deepseek/deepseek-v4.1-flash-1017:floor"]
base = "deepseek/deepseek-v4.1-flash"
name = "DeepSeek Floor"
price = { input = 0.098, output = 0.294, cache_read = 0.00294, cache_write = 0 }
openrouter_routing = { order = ["morph", "streamlake", "cloudflare"], allow_fallbacks = false }

[providers.openrouter.models."deepseek/deepseek-v4.1-flash-1017:nitro"]
base = "deepseek/deepseek-v4.1-flash"
name = "DeepSeek Nitro"
price = { input = 0.098, output = 0.294, cache_read = 0.00294, cache_write = 0 }
openrouter_routing = { order = ["streamlake", "morph", "cloudflare"], allow_fallbacks = false }

[providers.openrouter.models]
"deepseek/deepseek-v4.1-flash-1017:floor@300k" = { twin_of = "deepseek/deepseek-v4.1-flash-1017:floor", name = "DeepSeek Floor (300k)", compact_at = 307200 }
"deepseek/deepseek-v4.1-flash-1017:nitro@300k" = { twin_of = "deepseek/deepseek-v4.1-flash-1017:nitro", name = "DeepSeek Nitro (300k)", compact_at = 307200 }
"xiaomi/mimo-v2.6-flash" = { name = "MiMo Flash", price = { input = 0.14, output = 0.28, cache_read = 0.0028, cache_write = 0 } }
```

Every model resolves to the same facts, pin and wire id. What changes: `model_options.*.extends`
and `model_shared` leave the table, so his pi fork's `models.json` gets full rows instead of
`sharedModels`; the price of the two tiers is written twice; and the file no longer says "these
two rows are one model's facts" anywhere but in `base`.

## Appendix B: evidence

### B.1 The dry run

`yolo config migrate` run on 2026-10-30 against the maintainer's user config as his dotfiles lay
it out (`config.jsonc` including `personal.jsonc`; the work and overrides files absent on that
machine). It converts `config.jsonc` 112 lines to 67 and `personal.jsonc` 496 to 350, keeps 297
comments in order, and refuses to write on 33 model lines of `personal.jsonc`, with these reasons
(each prefixed `this yolo has no lossless conversion to … yet` or, for the root, `it has no
spelling in the TOML config`):

- `model_facts`, and `model_facts.deepseek-floor-1017`, `model_facts.deepseek-nitro-1017` → `providers.*.every_model`
- every `providers.*.models.*.id` → `providers.*.models.<id>` (seven rows)
- every `extends` → `providers.*.every_model` (four rows); every `wire_id` → `providers.*.models.*.twin_of` (two rows)
- every `reasoning`, `input`, `cost`, `context_window`, `max_tokens` on a row keyed by a name → the row or `every_model` spelling

Its provider-table layer refused outright on cerebras with the RC-D29 (7) message, naming
`id = "qwen-3.8-27b"` as the remedy. The `brokered` lines of the same run belong to another lane.

### B.2 What the composition does today, by symbol

Read at the tree this doc was written against; cite the symbol, not a line.

- `packdecl.ProviderModel` decodes `base`, `wire_id`, `extends`, `openrouter_routing`, `twin_of`,
  `compact_at` beside the facts; `packload.ValidateProviderModelGraph` refuses an unknown
  `twin_of` and a cycle.
- `packload.composeProviderModels`: catalog, then `every_model`, then the twin's base whole, then
  the row; `row.WireID` wins over the inherited wire id; `WireIDAuthored` is set by `wire_id` or
  `twin_of`; `vendor` is inferred by `inferProviderMaker` when the row states none.
- `packload.ProjectProviderModelsForLegacyDerives` emits `vendor`, `base`, `wire_id` (when
  authored) and `extends` into the flat facts, and `order` for each row.
- `packload.lowerReadableModels` runs before `liftModelFacts` so a readable row's `extends`
  resolves as a legacy alias's; `layShared` and `laySharedRouting` lay the shared facts and pin
  under every alias whose flat facts say `extends`, readable rows included.
- `packload.readableRowOverAlias` is RC-D29 (7)'s refusal and names both remedies.
- `config.ReadableSchemaKeys` lists `model_facts` among the removed roots and types
  `providers.*.models.*` as an object; it names `base`, `extends`, `openrouter_routing`, `twin_of`,
  `compact_at` on a row and not `wire_id`. `config.configRenames` maps `model_facts.*` and a row's
  `extends` to `every_model` and `wire_id` to `twin_of`.
- `configmigrate.readableFromLegacy` converts only a row whose key is its `id`; `legacyRowFacts`
  leaves `extends` and `wire_id` out by comment; `providerTable` is the proof's table layer and
  `Plan` retries without folds on a table difference.
- The maintainer's pi fork declares `reads_shared_models` in its pack manifest, so
  `packs/pi/derive.lua`'s `piShareRow` writes the shared form for him.
- `callableModels` in the pi, claude, codex and opencode derives reads `vendor`; the pi, opencode
  and non-Bedrock call sites pass no maker set. `internal/wirebridged`'s `anthropicModelIDs`
  reads the vendor `anthropic` only.

### B.3 The owner's stated reasons, from the file

Quoted from `personal.jsonc`'s comments, which the migration keeps: *"`openrouter_routing` IS a
shared fact … each route's upstream pin is written ONCE here"*; *"an object-valued fact is ONE
fact: a row's own `openrouter_routing` REPLACES this whole object"*; *"`wire_id` is the one key that
makes a twin possible on one provider … the request is byte-identical to its 1M sibling's at a
smaller declared window"*; *"The 300k twins are NOT a second provider."* Each is kept by
[§3.2](#32-facts-shared-by-a-models-routes) and [§3.3](#33-a-twin-one-wire-id-at-two-windows).
