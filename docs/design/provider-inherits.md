---
title: "A provider may inherit another provider's settings"
date: 2026-11-10
status: accepted
stage: BUILT
next: "Graduate the remaining argument into docs/reference/providers.md, which already documents the key, and delete this doc"
depends-on:
  - bedrock-key-provider.md
  - model-lists-and-pickers.md
tags: [providers, bedrock, config, packs]
summary: "A provider's `inherits = \"<provider>\"` takes another provider's composed settings — models, default model, region, options, platform — and lays its own keys over them. Credentials, names and variants never carry over. The shipped bedrock-key inherits from bedrock, so a config that copied bedrock's model list into bedrock-key can delete the copy."
---

# A provider may inherit another provider's settings

**Status:** 2026-11-10. Three questions ruled by the owner on 2026-11-10; every other choice is a
lead decision in the [ledger](#decision-ledger). Built and landed on main the same day; the key is
documented in [`providers.md`](../reference/providers.md) and `yolo config-ref`.

> **In short.** `bedrock-key` is `bedrock` with a different credential. The fix says so in one
> key: `inherits = "bedrock"` on the shipped `bedrock-key`. The owner's config writes the model
> list once, under `providers.bedrock`, and `bedrock-key` gets it.

**Why it matters.** Each provider owns its own `models` table. A config that uses both Bedrock
routes has to copy the whole model list, the region and the default model into both entries, and
the owner's config guards the copy with an equality test. Every model added is two edits.

**Needs your ruling:** None.

**Reads with:** [`bedrock-key-provider.md`](bedrock-key-provider.md) (why `bedrock-key` exists and
what it claims), [`model-lists-and-pickers.md`](model-lists-and-pickers.md) (ML-D1: every consumer
reads only the composed table; MM-D11: the two-pass `models` contributions),
[`providers.md`](../reference/providers.md#how-the-table-composes) (how the table composes).

**Terms.** The *composed table* is the providers table the host CLI builds once per launch from the
packs' provider declarations, their `models` contributions, and the user's `providers` config, in
that order (`packload.ComposeProviders`). The *parent* is the provider an `inherits` key names; the
*child* is the provider that carries the key. Both words are coined here for this design.

---

## The design

### The key

`providers.<child>.inherits = "<parent>"` is a provider-level string, legal in a pack's
`pack.toml` and in the user's config. The child's composed entry is:

1. the parent's composed entry, after the parent's own pack layer, its `models` contributions and
   the user's `providers.<parent>` entry ([PI-D1](#PI-D1)), **minus** the keys that never carry over
   ([PI-D2](#PI-D2));
2. then the child's own pack declaration, merged over it per field (and per alias inside `models`
   and `model_options`), the merge the user layer already uses;
3. then the `models` contributions aimed at the child, `add`s and then `only`s
   ([PI-D3](#PI-D3));
4. then the user's `providers.<child>` entry, last, as always.

So a user who writes `[providers.bedrock.models]` gets those models on `bedrock-key` too, and a
user who writes `[providers.bedrock-key.models]` adds to or overrides them on `bedrock-key` alone.

What carries over is everything the parent's entry holds (the owner's ruling, [OQ-PI1](#OQ-PI1)):
`models`, `model_options`, `every_model`, the default `model`, `region`, `options`, `platform`,
`endpoints`, `capabilities`, `via`, and the `models_only` mark that says an `only` narrowed the list.

What never carries over ([PI-D2](#PI-D2)): `api_key_env_name` (the credential), `name` and `alias`
(selection names are sole-owned), `variants` (a variant's name is a top-level selection name, so
`bedrock-bridge` would collide, and the owner ruled that the parent's variants stay its own,
[OQ-PI3](#OQ-PI3)), and `inherits` itself.

`inherits = ""` opts out: the child composes from its own declaration alone. A user writes it to
undo a pack's inheritance ([PI-D6](#PI-D6)).

### The shipped `bedrock-key`

`packs/bedrock/pack.toml`'s `bedrock-key` becomes `inherits = "bedrock"`, its own
`api_key_env_name`, and its own `bridge` variant (`bedrock-key-bridge`). The duplicated `platform`
and region block goes away ([OQ-PI2](#OQ-PI2), [PI-D8](#PI-D8)).

### Refusals

Each one refuses the launch and `yolo check`, names the provider and the key, and gives the next
step ([PI-D4](#PI-D4)):

| Case | Next step the message names |
| :--- | :--- |
| The parent is not in the table: no selected pack ships it and the config does not declare it | `yolo pack add <pack>` for the pack that ships it, declare `providers.<parent>`, or `yolo config set providers.<child>.inherits '""' --user` |
| The parent itself inherits (a chain, including a cycle) | point the child at the root parent, or opt the parent out with `yolo config set providers.<parent>.inherits '""' --user` |
| The child names itself | the same `yolo config set … inherits '""'` |
| `inherits` in a workspace `yolo-jail.toml` or `yolo-jail.local.toml` | move it to the user config ([PI-D5](#PI-D5)) |

### Where it runs, and parity

Inheritance is resolved inside the one composition (`composeProviderTable`, which both
`ComposeProviders` and `ComposeProviderDeclarations` call), before readable model rows are lowered
and before the adapter pass. Every consumer — each agent's derive, the wire bridge, the credential
scope, the region pre-flight's composed-table half — reads the finished entry and never sees the key
([PI-D1](#PI-D1)). The host CLI composes for the jail, macos-user and `yolo host` alike, so the
three notches get the same table by construction.

## Decision Ledger

| ID | Decision | Date | Section | Evidence |
| :--- | :--- | :--- | :--- | :--- |
| <a id="OQ-PI1"></a>OQ-PI1 | **What is inherited: A — everything except credentials, names and variants: models, default model, region, options.** The owner: *"1 eys."* (the lead's framing: "bedrock-key is bedrock with a different credential.") | 2026-11-10 | [The key](#the-key) | |
| <a id="OQ-PI2"></a>OQ-PI2 | **The shipped `bedrock-key` inherits by default: A — yes.** The owner: *"2. yes."* | 2026-11-10 | [The shipped `bedrock-key`](#the-shipped-bedrock-key) | |
| <a id="OQ-PI3"></a>OQ-PI3 | **The parent's user-added variants do not follow: A — no.** The owner: *"3. A"* | 2026-11-10 | [The key](#the-key) | |
| <a id="PI-D1"></a>PI-D1 | **Materialize in the composed table, after the user layer.** The child starts from the parent's finished entry, so the user's `providers.<parent>` edits flow through; consumers read only composed entries (ML-D1), so no derive learns the key. The key is stripped from every composed entry. | 2026-11-10 | [Where it runs](#where-it-runs-and-parity) | |
| <a id="PI-D2"></a>PI-D2 | **Never carried over: `api_key_env_name`, `name`, `alias`, `variants`, `inherits`.** A credential is the one thing that makes the child a different route; a name or variant name is a sole-owned selection name. A user child with no `api_key_env_name` and an inherited `platform` co-claims that platform's names, exactly as a user provider that writes the `platform` itself does. | 2026-11-10 | [The key](#the-key) | |
| <a id="PI-D3"></a>PI-D3 | **A child's `models` contributions apply after it inherits.** Otherwise a pack's `only` aimed at the child narrows an empty list and the inherited list then escapes it. The child's contributions keep MM-D11's two passes (every `add`, then every `only`), and the child's user entry still writes last. A child's row may `twin_of` an inherited row, because twins are validated on the composed entry. | 2026-11-10 | [The key](#the-key) | |
| <a id="PI-D4"></a>PI-D4 | **One level only; a chain, a cycle, a self-reference or a missing parent refuses.** A chain would make the composed result depend on resolution order, and one level covers the case in hand. Each refusal names the verb that fixes it. | 2026-11-10 | [Refusals](#refusals) | |
| <a id="PI-D5"></a>PI-D5 | **`inherits` is user-scope only.** It can change a provider's `platform` and `endpoints`, which are user-scope only because they decide where a credential goes; a workspace file could otherwise point a provider at another provider's address or platform. `inherits = ""` is refused in a workspace too, since it can withhold a platform. | 2026-11-10 | [Refusals](#refusals) | |
| <a id="PI-D6"></a>PI-D6 | **`inherits = ""` (or a null) opts out.** The user's value replaces the pack's, as every scalar does. | 2026-11-10 | [The key](#the-key) | |
| <a id="PI-D7"></a>PI-D7 | **A disabled parent still lends its entry.** When the user sets `providers.<parent>` to null, the child inherits the parent's entry as the packs composed it (the user's null leaves no user layer to include), and only then is the parent dropped. Disabling the SSO route must not break the key route, which is what a machine with no SSO does. | 2026-11-10 | [The key](#the-key) | |
| <a id="PI-D8"></a>PI-D8 | **Declaration readers that look a provider up by name follow the pack-layer `inherits`.** The region variables a selected provider reads are a declaration fact (`region_env_name`), not a composed one, so the reader that lists a selected provider's region variables (`unknownlaunchenv.go`) takes the parent's when the child declares none. The region pre-flight keys on platform and is unchanged. | 2026-11-10 | [The shipped `bedrock-key`](#the-shipped-bedrock-key) | |
| <a id="PI-D9"></a>PI-D9 | **`yolo config why`/`dump` origin for inherited values is backlogged**, not built: those commands read config layers, not the composed table, so showing "inherited from bedrock" needs a composed-table origin trace they do not have. | 2026-11-10 | — | [BACKLOG](../plans/BACKLOG.md) |

Unaffected, checked: one agent's active set never holds two providers on one regional platform, so
inheriting `region` cannot give one agent two regions; PP-D9's same-platform co-claim reads the
composed `platform`, which the child now inherits rather than restates.

## Build plan

Two builders, partitioned by file. A and B run in parallel.

**A — composition** (`internal/packdecl`, `internal/packload`, `internal/cli/run/unknownlaunchenv.go`,
`packs/bedrock/pack.toml`, their tests, `internal/cli/run/testdata/provider-table-equivalence.golden`):

- `packdecl`: an `inherits` provider field (`Contribution.ProviderInherits`,
  `ProviderContribution.Inherits`), read from `pack.toml` and the legacy JSON spelling, written back
  by the TOML encoder; a provider naming itself is a manifest problem.
- `packload`: `shippedProviderEntry` and `canonicalProviderEntry` emit `inherits`;
  `composeProviderTable` resolves it per [The key](#the-key) and the ledger (a new
  `providerinherits.go` holds the resolver and refusals); `applyModelContributions` takes the set of
  children to defer.
- `unknownlaunchenv.go`: PI-D8.
- `packs/bedrock/pack.toml`: the new `bedrock-key`.
- Tests: the user's `providers.bedrock.models` reaches `bedrock-key`; the credential never does;
  variants and aliases never do; a child `only` narrows the inherited list; a child row twins an
  inherited row; a nulled parent still lends; opt-out; every refusal; each test fails when its
  call site is removed.

**B — config and docs** (`internal/config`, `internal/cli/config_ref.txt`,
`docs/reference/providers.md`, `userguide/`):

- `knownProviderKeys` gains `inherits`; a string value; the workspace-scope refusal (PI-D5).
- `config-ref`'s providers section and `providers.md` document the key, what carries over, the
  refusals, and the bedrock example.
- The PI-D9 backlog entry.
