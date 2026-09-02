---
title: "A catalog and a selection are two features — and only one of them ships"
date: 2026-09-01
status: accepted
tags: [providers, profiles, packs, derives, selection, zai]
summary: "Splits the knot that profiles-as-pack-variants and zai-plumbing left tangled. Populating an agent's provider directory and telling that agent which provider to USE are different features with different triggers; today the first works for every agent and the second is implemented for claude only. Measured in a live jail: pi, opencode and codex all carry zai in their catalog with no selection key set, so `-p zai` reaches three of the four agents it claims. The disable-without-deleting complaint falls out of the same conflation."
---

# A catalog and a selection are two features — and only one of them ships

**Status:** DECIDED, 2026-09-01 — all questions ruled the day they were asked (ledger, §10); the
tenth was **withdrawn as never having been a design question**. **Nothing built.** §3's empty pi
row was **filled 2026-09-02 from source** (pi 0.84.4, opencode `v1.18.18`) — the research blocker
is closed, and the verification found two delivery defects recorded in the sibling doc as
D10/D11: the pi and opencode catalog entries render fields those agents do not read.

**The short version.** *"Put z.ai in my agents' provider directory"* and *"start this agent
**using** z.ai"* are two features. yolo drives both off one table and one selector, which is why
neither behaves the way the docs describe. **Measured in a live jail today:** pi, opencode and
codex each carry a `zai` entry in their provider catalog and **none of them has a selection key
set**, so `-p zai` changes the behaviour of exactly one agent — claude, through `env_shape`. The
catalog half works everywhere. The selection half exists for claude and **is not implemented for
the other three at all**. Separating the two also dissolves the disable-without-deleting problem,
which is not a bug in `null` handling but a consequence of catalog presence being the only thing
a provider entry can express.

> [!IMPORTANT]
> **This doc introduces no new vocabulary.** Two words, both already meaningful: **catalog** (the
> agent's directory of providers it *could* use) and **selection** (which one it *does* use).
> "Provider" and "profile" keep their current meanings. An earlier draft of the sibling doc floated
> "mode" as a third word; it is withdrawn — it was a rename candidate for the selector, not a
> concept, and a third generic noun here costs more than it buys.

**Reads with:** [`provider-table-fidelity.md`](provider-table-fidelity.md) (the sibling — a defect
report on the same machinery; its §5.4 asks what a profile *is*, this one asks what the two
features *are*), [`zai-plumbing.md`](zai-plumbing.md) (§5's resolution table assumes selection is
solved), [`profiles-as-pack-variants.md`](profiles-as-pack-variants.md) (the parent design).

---

## 1. The two features, named

| | **Catalog** | **Selection** |
| :--- | :--- | :--- |
| The want | "my agents should know z.ai exists" | "start this agent using z.ai" |
| Natural trigger | a provider entry being **present** | an explicit act — `-p zai`, `use_profiles` |
| Scope | every agent that speaks a protocol the provider offers | the agent(s) being launched |
| Lifetime | durable — it is a directory | per-launch |
| Ships today | ✅ all four agents *(at the file level — D10/D11 below)* | ⚠️ **claude only** |

They are orthogonal. A user can reasonably want a catalog of five providers and select one; or a
catalog of one and select nothing. The confusion this doc exists to end is that **one table drives
both**, so wanting a durable catalog entry and not wanting to use it right now has no spelling.

> [!NOTE]
> **"Ships today ✅" is a claim about files, not about the wire (verified 2026-09-02).** The pi
> and opencode catalog entries render fields those agents do not read: opencode takes
> `baseURL`/`apiKey` only under an `options` object the derive does not emit (D10), and pi has no
> `apiKeyEnv` field at all (D11) — its indirection is `apiKey: "${VAR}"`. Both defects and their
> measurements live in [`provider-table-fidelity.md`](provider-table-fidelity.md) §3.5, and both
> fixes ride this doc's build-order steps 3–5, which touch those derives anyway.

---

## 2. What ships today — measured, not inferred

Read out of this jail's own rendered agent configs, **2026-09-01**, with `packs: ["claude", "zai"]`
and `providers.zai` in user config:

| Agent | Catalog | Selection key | Set? |
| :--- | :--- | :--- | :--- |
| pi | `providers: {bedrock-mantle, zai}` in `~/.pi/agent/models.json` | — the file has **only** a `providers` key | **absent** |
| opencode | `provider: {zai}` in `~/.config/opencode/opencode.json` | top-level `model` | **absent** |
| codex | `[model_providers.zai]` in `~/.codex/config.toml` | `model_provider` / `model` | **absent** |
| claude | *(no catalog — claude has no provider directory)* | `ANTHROPIC_BASE_URL` + `ANTHROPIC_AUTH_TOKEN` | **set, via `env_shape`** |

**So the answer to "what do pi and opencode do if no provider is specified?" is: whatever they did
before yolo wrote anything.** They fall through to their own built-in default, and the `zai` entry
sits in the directory unused. That is not a defect in the derives — it is the catalog feature
working exactly as designed, in the absence of the selection feature.

`packs/zai/README.md` already says this out loud (*"The catalog is presence, not choice"*), and
[`zai-plumbing.md`](zai-plumbing.md)'s stated want — *"`-p zai` works anywhere; every selected
agent fires at z.ai"* — is therefore **met for one agent of four.**

> [!NOTE]
> **The derives are not the constraint.** Nothing about the current shape is load-bearing; the
> three derives are ordinary Lua in the packs that own each agent, rewritable in the same commit
> that decides what they should write. The reason selection is missing is that nobody wrote it,
> not that something prevents it.

---

## 3. Where a selection would have to land, per agent

This is the table the design needs and does not have. Confidence is stated per row, because two of
the four are the same class of unverified claim the sibling doc charges D1 with.

| Agent | Selection surface | Value shape | Evidence |
| :--- | :--- | :--- | :--- |
| claude | process env | `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN` | **shipped and working** |
| codex | `config.toml` | `model_provider = "<name>"` + `model = "<id>"` | **good** — [`local-model-endpoints.md`](../research/local-model-endpoints.md) §"Codex CLI", verified from the binary 2026-08-20 |
| opencode | `opencode.json` | top-level `model`, as `"<provider>/<model>"` (split on the FIRST `/`) | **source-verified 2026-09-02** — upstream `v1.18.18` = the installed binary: `packages/core/src/v1/config/config.ts:74-76` ("Model to use in the format of provider/model"), split at `model.ts:33-39`; an unknown prefix is `ModelNotFoundError` with no silent fallback; with `model` unset, opencode falls back to its own persisted interactive choice (`~/.local/state/opencode/model.json`) — OQ-CS2's ruling, confirmed from source |
| pi | `~/.pi/agent/settings.json` | `defaultProvider = "<id>"` + `defaultModel = "<bare model id>"` — a **pair**, not a slash string | **source-verified 2026-09-02** — pi 0.84.4 `dist/core/settings-manager.d.ts:71-72`; ids match exactly (`===`) against the provider's model list; pi's own writer persists exactly this pair (`core/settings-manager.js:460-475`), and its save path overlays only modified fields under a lock (`:376-399`), so a pre-written selection survives unrelated in-session saves |

**The pi row's blocker is closed** (2026-09-02): the surface is `~/.pi/agent/settings.json`, the
keys are `defaultProvider` + `defaultModel`, and both were read out of the published package the
launcher installs (0.84.4 — npm-extracted to a scratch prefix, the CLI never run) and confirmed
against the live files in this jail, which carry exactly that pair for `zai`/`glm-5`. Two
implementation notes that fell out of the verification: a project-scope twin (`.pi/settings.json`
in the working directory) deep-merges over the global file, so the global file is the right
surface for a jail-wide default; and pi resolves a saved default only when the provider's
credential is configured — which is D11's fix (`apiKey: "${VAR}"`), not this table's business.


### 3.1 `env_shape` is declared by the provider and describes the AGENT — the mirror of D1

*"It's more that the claude pack specifically needs the env_name config, although I guess that could
be shared with other agents that need this rename."* That is the right instinct and the conclusion is
stronger than it sounds: **the rename is not the provider's fact to state, and today it is stated
there.**

Measured 2026-09-01 — who declares `env_shape`, and about whom:

| Declared in | On provider | Variables it names | Whose facts are those? |
| :--- | :--- | :--- | :--- |
| `packs/zai` | `zai` | `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN` | **claude's** |
| `packs/claude` | `bedrock` | `ANTHROPIC_DEFAULT_{OPUS,SONNET,HAIKU}_MODEL`, `AWS_REGION` | claude's (right pack, wrong record) |

**z.ai does not care what environment variable you use.** `ANTHROPIC_AUTH_TOKEN` is a fact about
claude that a provider pack is forced to restate — which is
[`provider-table-fidelity.md`](provider-table-fidelity.md)'s D1 pointed the other way. There, an
agent's vocabulary was hoisted into a core enum; here, an agent's requirement is pushed down into a
provider declaration. Both put a fact in a record that cannot own it.

Two costs, and the second is the one that bites:

1. **Restatement that grows with providers.** Every provider serving claude copies claude's
   variable names. Two providers today; the maintainer's own config already carries a
   `CEREBRAS_API_KEY`, so the third is not hypothetical.

   *(Framing corrected 2026-09-01 — an earlier draft claimed the fix "kills the N×M", and the
   maintainer's response is the accurate one: **"I don't think we can get around N×M, but we can
   make it easy to fill in the grid."** Right. Moving the binding to the agent makes the COMMON case
   N+M — each agent declares one binding per protocol, each provider declares one endpoint per
   protocol, and any pair whose protocols meet works with nothing written for that pair. The grid
   does not vanish: a pairing that needs something specific still needs a cell, and the design should
   make that cell cheap to write rather than pretend it is never needed. What is eliminated is the
   **obligatory** cell for every pair, not the possibility of one.)*
2. **Nothing says what claude actually needs, so each provider guesses a different subset.** zai
   declares endpoint + token and **no model variables**; bedrock declares models + region and no
   endpoint or token. Each is defensible alone — bedrock authenticates through the ambient AWS chain
   and needs no token — but the *reason* lives nowhere, and a third provider author has no
   declaration to copy from. The visible consequence: **`packs/zai` ships `models: {default:
   glm-4.7, fast: glm-4.7-air}` and nothing delivers them to claude**, because the `{model:…}`
   placeholders exist only inside `packs/claude`'s bedrock declaration.

> [!NOTE]
> **This is a gap to close, not an uncertainty to weigh.** An earlier draft hedged on whether the
> missing model delivery *matters* — z.ai's Anthropic route may remap the model name it is sent, so
> claude-on-zai may work anyway. That hedge let a current limitation shape the design, which is
> backwards: **if models are not plumbed, plumb them.** Whether today's specific pairing happens to
> survive the omission is worth knowing and is not the design question. The runtime half still needs
> an authenticated request this repo's tests may not make, so it stays unverified — what changes is
> that it is filed as work rather than as a reason to wait.

**The shape of the fix, and it is the same rule §3 is already built on.** Each **agent pack**
composes how a selection reaches it — codex and opencode into a config key, claude into environment
variables. *(Ruled 2026-09-01, OQ-CS8: it is composed by the agent pack's own derive, not declared.
The block below is kept as a statement of WHAT claude needs delivered; §9 OQ-CS8 has the shape that
delivers it and the list of what that deletes.)*

```jsonc
// packs/claude declares its own binding, once, for every anthropic provider
{ "kind": "binding", "protocol": "anthropic",
  "env": { "ANTHROPIC_BASE_URL": "{endpoint}", "ANTHROPIC_AUTH_TOKEN": "{key}",
           "AWS_REGION": "{region}",
           "ANTHROPIC_DEFAULT_OPUS_MODEL": "{model:default}" } }
```

Then `packs/zai` declares **no `env_shape` at all** — it declares an `anthropic` endpoint, and that
is sufficient. A placeholder whose input is missing already drops its variable, so the same
declaration serves bedrock (no endpoint, no key, a region) and zai (endpoint and key, no region)
with no per-provider subset to choose. **claude stops being the special case**: three agents deliver
a selection through a config file, one through the environment, and all four declare it themselves.

Whether the binding is a new contribution kind or a field on the existing ones is OQ-CS8.

> [!NOTE]
> **The example above is written in today's placeholder vocabulary, and that vocabulary is GONE.**
> [`provider-table-fidelity.md`](provider-table-fidelity.md) OQ-PT9 ruled 2026-09-01 that everything
> goes to the derive, credential included — so read the block below as *what claude needs delivered*,
> not as the syntax that delivers it. In the ruled shape it is Lua over `prov.endpoints`,
> `prov.models` and the resolved key.
>
> Two things follow. **Derives gain the ability to emit environment** (*"of course derive needs to be
> able to augment the env, that's need to be built"*), which is the capability gap that made a
> template language look necessary. And **§3.1's missing model loop gets written rather than encoded
> in a new placeholder** — nothing about the credential stays declarative, because the trust argument
> I built for keeping it so did not survive review: a derive already controls `mcp_servers` commands,
> and a fetched pack's `env` is already granted in-jail exec unapproved
> ([`trust-paths.md`](trust-paths.md) row 18).

---

## 4. Why "disable it but keep my settings" has no spelling

The complaint that started this doc: *a user wants their `providers.zai` customizations to survive
turning zai off.* Today they cannot, and the reason is entirely §1's conflation.

1. The three derives iterate **every** entry — `for name, prov in pairs(ctx.providers)` in all
   three ([`packs/pi/derive.lua`](../../packs/pi/derive.lua),
   [`codex`](../../packs/codex/derive.lua), [`opencode`](../../packs/opencode/derive.lua)). So
   **presence is the catalog trigger**, with no gate.
2. Therefore "not in the catalog" can only mean "not present".
3. The only absence-maker is `providers.zai: null`, which **replaces the settings it disables**.
4. And there is no alternative: `knownProviderKeys` is a closed set of eight, so `enabled: false`
   is not expressible and would be refused as an unknown key.

So the user's options are *keep it and have it appear everywhere*, or *delete it*. Once catalog and
selection are separate, the want is trivial — the entry stays, and nothing selects it.

**And catalog membership is what should carry the credential requirement.** The maintainer's rule,
2026-09-01: *pack presence means in the dictionary, which also means fatal errors if no API key
found.*

> [!WARNING]
> **"Pack presence" is not catalog membership, and an earlier draft of this section said it was.**
> A selected pack's providers all reach the **composed table**; only the ones carrying an
> **endpoint for a protocol the agent speaks** reach a **catalog**, because that is what the derives
> gate on. The two are not the same set, and conflating them would have made the rule demand
> credentials for a provider no agent can browse to. Measured 2026-09-01 with `packs: ["claude"]`:
> `bedrock` composes into the table with **no endpoints and no `api_key_env_name`** — only an
> `env_shape` — so it reaches no catalog and the preflight demands nothing for it, with nothing
> hydrated at all.

**Which answers "what happens to bedrock?"** — nothing, and by construction rather than by
exemption. **Bedrock is a selection-only provider**: it has no endpoints, so it can never be in
anyone's dictionary, and no credential pointer, because its credential is the ambient AWS chain that
yolo cannot inspect. `zai` is in both — endpoints *and* a key — so it is catalogued and its key is
demanded. The rule lands exactly where it was aimed and nowhere else. **A user who has never touched
AWS is not asked for anything**, and that is not a special case for Bedrock; it is what "has no
endpoint" already means.

Keyed properly, the rule is stricter than what ships and better defined — and adopting it repairs a defect the
sibling doc reports separately. Today the requirement is computed from what a pack **declares**
(`requiredProviders` walks `p.Decl.Providers()`), not from the composed catalog, so a user who drops
a provider with `null` **still has the launch refused for it** — measured: with `packs: ["claude"]`
and `providers: {"bedrock": null}` the catalog composes empty and `bedrock` is still required. Keyed
to catalog membership instead, the null removes the entry and the requirement with it, and the rule
reads as one sentence: **in a dictionary means you need the key; not in one means you do not** —
where "in a dictionary" is *present in the composed table AND carrying an endpoint an agent speaks*,
which is the same predicate the derives already apply.

> [!NOTE]
> **A smaller, separate bug found while measuring this** (belongs in the sibling doc's defect list,
> recorded here because §4 is where it surfaced): the `null`-drop convention is implemented only at
> the **top level** of `ComposeProviders`. `mergeUnder` does not honor it, so
> `providers.zai.models.fast: null` composes to a literal `"fast": null` rather than removing the
> alias. The delete convention works one level deep and silently does not below that.

---

## 5. The design

### 5.0 Options, with verdicts

| # | Shape | Verdict |
| :--- | :--- | :--- |
| **A** | **Status quo** — catalog from presence, selection for claude only. | **Rejected.** It is the state this doc reports, and it makes the parent design's headline claim false for three agents of four. |
| **B** | **Gate the catalog on selection** — a provider reaches an agent's directory only when selected. | **Rejected.** It fixes disable by deleting the catalog feature. pi and opencode both have interactive model pickers, so a populated directory is a real affordance, and `-p` would become mandatory to use any provider at all. |
| **C** | **Keep catalog from presence; add an explicit non-`null` disable** (`providers.zai.enabled: false`, or an exclusion list). | **Viable, and the smallest change.** Fixes §4 exactly and nothing else — selection stays unimplemented. Worth doing only if D is judged too large; it is not a substitute for it. |
| **D** | **Catalog from presence, selection written into each agent's own selection key** — `-p zai` sets `model_provider`/`model`/`model` (per §3) the way it already sets claude's env. | **RULED IN, 2026-09-01 (OQ-CS1).** *"Activating a profile should work for all."* It is the feature the parent design claims and does not have; it makes §4's want expressible for free (an unselected entry is inert *as a selection* while staying in the catalog); and it needs no new config surface, only the per-agent knowledge §3 is missing two rows of. |
| **E** | D, plus C's explicit disable for the catalog half. | **The likely end state**, but sequence it after D — a user who can select is much less bothered by a catalog entry they do not use, so C's urgency is unknown until D lands. |


### 5.1 The no-profile case is the agent's own (OQ-CS2)

**When no profile is active for an agent, yolo writes nothing to its selection key** — not a
default, not a clear. *"Default can be left to the specific agent."*

This is a deliberate exception to how the prism normally behaves, and it is worth naming as one
rather than letting it read as an oversight. Every other composed surface is regenerated wholesale
each boot, which is what makes a dropped input disappear rather than needing to be un-applied. A
selection key cannot follow that rule, because pi and opencode both let a user change the model
**interactively, mid-session**: a key yolo re-asserted every boot would let that choice survive
until the next launch and then silently revert. So the rule is *write on activation, never on
absence*, and the surface is one yolo touches sometimes and not others.

The consequence to keep in view: yolo can turn a selection **on** and cannot turn it **off**. A
user who selects a profile once and then launches without one keeps the selection that launch
wrote. Whether that needs an explicit "back to the agent's default" spelling is not settled here —
it is the first thing to re-examine once D has shipped and there is real usage to look at.


### 5.2 What a profile is — user-declared intent, over a surface the provider defines

**The maintainer's ruling, 2026-09-01:** *"that's what I want a profile to be. user declared, user
intent … being customized just as you say, and then config surface of a profile needs to be defined
by the provider."*

So, stated as the definition this corpus has been missing:

> A **profile** is a **named selection over a provider** — user intent, expressed in user config.
> Its name is the selector `-p` sets. Its body says **which provider** and **which of that
> provider's tunables**, and the set of tunables it may carry is **defined by the provider**, not by
> the profile and not by core.

That is one meaning, in one place, and it is the meaning a user already assumes when they type
`-p zai`. Three properties follow, and each closes something that was open:

1. **A user declares them.** Not only a pack. Same shape either way, so a pack-shipped profile is a
   *default* a user overrides, exactly as a pack-shipped provider is.
2. **The provider owns the schema.** A profile for zai may name a model because zai declares
   `models`; it may not name a knob zai does not have. The provider is the extension point, which
   is the shape [`extension-point-principle.md`](extension-point-principle.md) already asks for and
   the reason a profile does not need a schema of its own.
3. **Every profile names a provider, and declaration is MANDATORY.**

> [!WARNING]
> **Property 3 said the opposite in the first draft of this section, and it was wrong.** It read
> *"a profile that names no provider is still a valid gate — declaration is optional"*, offered for
> backward compatibility, and the maintainer's objection is correct: it re-created the exact overload
> this definition exists to end. A word that means *a named selection over a provider* in one place
> and *a bare gate string* in another is the two-meanings problem wearing a new coat. The property is
> **inverted**, not softened.
>
> The cost is nil today and the benefit is real. Both profiles in the tree name a provider already
> (`packs/claude`'s `bedrock`, `packs/zai`'s `zai`), so nothing shipped has to change shape. And a
> mandatory declaration is what makes the `profile:` modifier reference *something*: an unmatched
> name becomes diagnosable instead of silently inert, which is the fail-closed property
> [`stringly-typed-references-principle.md`](stringly-typed-references-principle.md) asks for and
> that free-form names could never have.
>
> **This reverses a ruling, and the reversal is confirmed** (OQ-CS6, 2026-09-01: *"reversing old
> decisions is fine. we're debugging a mess of a design and need to change things."*).
> `profiles-as-pack-variants.md` OQ-5 made profile names free-form and global — ruled when a profile
> WAS a bare string. The definition changed underneath that ruling, so OQ-5 is **superseded**, not
> contradicted, and a name with no declaration becomes a reportable error rather than a silent no-op.

#### The worked examples

**Shipped as they are today** — the pack supplies the service facts and one default profile:

```jsonc
// packs/zai/pack.json (contributions, abridged)
{ "kind": "provider", "name": "zai",
  "api_key_env_name": "ZAI_API_KEY",
  "endpoints": { "anthropic": {...}, "openai": {...} },
  "models": { "default": "glm-4.7", "fast": "glm-4.7-air" } }

{ "kind": "profile", "name": "zai", "provider": "zai" }      // the default intent
```

**A user adding a second intent over the same provider** — the case that is not expressible today
(measured: `ProviderFor(claude, "zai-fast")` returns `"zai-fast"`, the name fallback, so the second
name resolves to a provider that does not exist):

```jsonc
// ~/.config/yolo-jail/config.jsonc
{
  "packs": ["claude", "codex", "zai"],
  "profiles": {
    "zai-fast": { "provider": "zai", "model": "fast" }       // NEW name, SAME provider
  },
  "use_profiles": { "claude": "zai-fast" }                   // or: yolo -p zai-fast
}
```

**A user customizing the pack's own profile**, by declaring the same name — field merge, pack under
user, the convention `providers` already uses:

```jsonc
{ "profiles": { "zai": { "model": "fast" } } }               // keep provider zai, change the model
```

**What each agent then gets**, from one selection — this is §3's table doing its job:

| Agent | Written | From |
| :--- | :--- | :--- |
| claude | `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN` | the provider's `env_shape` *(ships today)* |
| codex | `model_provider = "zai"`, `model = "glm-4.7-air"` | the profile's provider + resolved model |
| opencode | `model = "zai/glm-4.7-air"` | same, in opencode's spelling |
| pi | **unknown** | §3's empty row — the blocker |

#### What happens to the pack's variant body

`kind: "profile"` today carries a `config` patch, `launch` flags and an `env` map — a *body*, not a
selection. Under this definition those are not profiles at all; they are **contributions gated on a
profile name**, which is the `profile:` modifier `568d5a3a` already built for `config-overlay` and
whose own commit message says is contemplated for `env` and `launch`.

So `packs/claude`'s `bedrock` — the one real variant body in the tree — decomposes cleanly:

| Today, inside `kind: "profile"` | Becomes |
| :--- | :--- |
| `requires_provider: "bedrock"` | `{"kind": "profile", "name": "bedrock", "provider": "bedrock"}` — the selection |
| `env: {CLAUDE_CODE_USE_BEDROCK: "1"}` | `kind: "env"` with `profile: "bedrock"` |
| `config:` patch on `claude/settings` | `kind: "config-overlay"` with `profile: "bedrock"` |

**And this repairs the reachability defect rather than working around it.** A variant body is
unreachable today for a pack that installs no CLI, because `ActiveProfiles` iterates the pack's own
`InstallBins()` — measured, `packs/zai` can never activate its variant under any profile table. The
`profile:` modifier gates on the *target surface's* owning agent instead, so it works for a provider
pack. Moving the body to modifiers is the fix for that, not merely a tidier spelling of it.

#### The schema is the provider's — declared, not derived (OQ-CS4)

**Ruled 2026-09-01:** *"model can't be the only config we'll want, which is why I said the config
surface/schema is dictated by the provider."* A previous draft of this section fixed the field set at
two (`provider`, `model`) and deferred the general form; that is overruled. **A provider declares
what a profile for it may carry**, and the profile is an instance of that declaration.

The distinction that keeps this from being magic: the provider declares the **options**, and the
*derive* decides how each one lands in its agent's config. Nothing is discovered by reflection over
the provider's shape, and core learns no option names at all.

```jsonc
// the provider declares its surface — a flat map of option name to DEFAULT VALUE
{ "kind": "provider", "name": "zai",
  "endpoints": { … }, "models": { "default": "glm-4.7", "fast": "glm-4.7-air" },
  "options": { "model": "default", "thinking": "off" } }

// a profile is an instance of it, stating only what it changes
{ "profiles": { "zai-fast": { "provider": "zai", "model": "fast", "thinking": "low" } } }
```

`model` stops being special — it is one option like any other. **The provider says what can be set
and what it defaults to; the agent's own derive says where it lands and what the value means.**

> [!WARNING]
> **An option nothing consumes is inert, and that has to stay true rather than become an error.**
> A provider may offer `thinking` and an agent's derive may not know it; that composes to nothing,
> the way an endpoint for a protocol no agent speaks already does. Refusing an unconsumed option
> would make every provider's surface hostage to the least capable agent selected. The temptation to
> close this vocabulary should be resisted for the reason `wire_api`'s enum records: a set validated
> at one end and delivered verbatim at the other is not type safety, and closing an option set here
> would repeat it one layer up. **Which validation an option DOES get is OQ-CS7** — the declaration
> can be checked against itself (an enum's value is in its own list) without core knowing what
> `thinking` means.


### 5.3 What `api_key_env_name` is actually used for — two mechanisms, not one

Traced 2026-09-01, because *"do we read it and pass it on directly to agents and embed the key in
their config? would they have read from the env instead?"* has a different answer per agent.

| Agent | What yolo does with `ZAI_API_KEY` | Does yolo read the secret? |
| :--- | :--- | :--- |
| pi | writes the **name** into `models.json` as `apiKeyEnv: "ZAI_API_KEY"` | **no** |
| codex | writes the **name** into `config.toml` as `api_key_env = "ZAI_API_KEY"` | **no** |
| opencode | writes a **name reference** into `opencode.json` as `apiKey: "{env:ZAI_API_KEY}"` | **no** |
| claude | reads the **value** and re-emits it as `ANTHROPIC_AUTH_TOKEN=<secret>` | **yes** |

**So for three of the four, the answer to "would they have read from the env instead?" is: they do,
already.** yolo passes the name through and never touches the secret. **The key is not embedded in
any agent's config file** — no derive writes a value, and `env_shape`'s `{key}` feeds only the
process environment, never a composed surface.

**claude is the exception, and it is a rename rather than an embed.** claude reads
`ANTHROPIC_AUTH_TOKEN`; the user's variable is `ZAI_API_KEY`. Nothing can alias one environment
variable to another, so the only way to serve both *"one key, spelled once"* and *"claude reads its
own variable"* is to copy the value. That copy is the entire reason `{key}` exists, and it is why
`api_key_env_name` is a **binding** (a fact about this machine) rather than a service fact — the
point [`provider-table-fidelity.md`](provider-table-fidelity.md) §5.4 makes about the field sitting
in the wrong record.

> [!WARNING]
> **The secret does reach a file, and the file is world-readable.** `writeUserEnvFile`
> ([`internal/cli/run/userenv.go`](../../internal/cli/run/userenv.go)) writes the hydrated
> `env_sources` values to `<workspace>/.yolo/yolo-user-env.sh` at **mode 0644**, mounted into the
> jail at `~/.config/yolo-user-env.sh`. Measured in this jail 2026-09-01: `-rw-r--r--`, holding
> `ZAI_API_KEY` and `CEREBRAS_API_KEY` in plaintext. The path is gitignored, so it is not a
> commit risk — but [`packs/zai/README.md`](../../packs/zai/README.md) tells the user to keep the
> key in a file that is *"untracked, 0600"*, and yolo's own copy of it downgrades the mode. The
> claude path additionally puts the value on the `podman run` argv as `-e ANTHROPIC_AUTH_TOKEN=…`,
> which is visible in `ps` to any process on the host that can see it. Neither is this doc's to
> fix — both are recorded as **D8** in
> [`provider-table-fidelity.md`](provider-table-fidelity.md), which owns the defect list.


### 5.4 Two keys, and why the names are wrong

*"Why do we have profiles and pack profiles? What's the diff?"* They are genuinely two things —
and the names hide that, which is a fair reason to distrust them.

| | Holds | Keyed by | Lifetime |
| :--- | :--- | :--- | :--- |
| **declaration** (`profiles`) | *what a profile IS* — provider + options | profile name | durable |
| **selection** (`use_profiles`, `pack_profiles` today) | *which profile each CLI uses* | **CLI name** | per-launch intent, persisted |

The split is the same one §1 draws between catalog and selection, one level up: a declaration is
inert until something selects it, and a selection is a pointer, not a definition. Merging them
re-creates the conflation this whole doc exists to undo — a value that is sometimes a name and
sometimes an object.

**But `pack_profiles` names neither of the things it holds.** Its keys are **CLI names**, validated
as such ([`config.UseProfileCLINames`](../../internal/config/packs.go)); no pack is named anywhere
in it. It was `agent_profiles` until 2026-09-01 and was renamed on the grounds that *"the keys were
always CLI names and core knows packs, not agents"* — which is true about `agent` and does not make
`pack` right. The rename traded one wrong word for another, and putting `profiles` beside it now
would leave two keys that read as synonyms and are not.

**RULED 2026-09-01: the selection key becomes `use_profiles`.**

```jsonc
{ "profiles":     { "zai-fast": { "provider": "zai", "model": "fast" } },   // what it IS
  "use_profiles": { "claude": "zai-fast" } }                               // which CLI uses it
```

It reads as *"use profile zai-fast for claude"* and cannot be mistaken for the declaration map.
**And the migration is ONE step, not two**, which is the maintainer's own `api_key_env` precedent
applied a second time. `pack_profiles` landed 2026-08-31 and `v0.8.0` is dated 2026-08-13, so **the
intermediate name has never been in a release** — nothing outside this machine has ever written it.
So it gets no deprecation path and becomes an ordinary unknown key, exactly as `api_key_env` did.
What *keeps* its by-name rename message is `agent_profiles`, because that spelling IS out there: it
is in every host-generated jail snapshot in existence. The rule both cases follow: **a retired-key
message is for a spelling users have, not for one that was briefly in the tree.**

**The rename is not free and the size should be on the record before it is scheduled**: 92 non-test
occurrences of `pack_profiles` / `YOLO_PACK_PROFILES` / `PackProfiles` across 12+ files, plus 88 in
tests (counted 2026-09-01). It crosses a config key, an env var, a Lua `ctx` field the derives read,
and the Go identifiers between them — so it is one mechanical commit rather than a hard one, but it
is not a one-liner and it should not ride another change.

---

## 6. What this does NOT propose

- **No new config key for the common case.** D writes into keys the agents already define; it adds
  no yolo-side surface.
- **No change to `kind: "provider"`, the composition, or the credential boundary.** All of that is
  the sibling doc's territory and none of it is implicated here.
- **No user-declared profile bodies.** An earlier line of thinking wanted a user-side gated config
  layer; §5 D makes it unnecessary for the provider case, which was its only motivating example.
- **Not a claim that the catalog should be per-agent-filtered.** Which providers an agent can
  *speak to* is already answered by protocol resolution (a provider with no `openai` endpoint does
  not reach the OpenAI-shaped agents); this doc is about *use*, not reachability.
- **No change to `-p`'s spelling or scope.** Whether `--profile` keeps two meanings is
  [`provider-table-fidelity.md`](provider-table-fidelity.md) OQ-PT5.

---

## 7. Risks

| Risk | Mitigation |
| :--- | :--- |
| **Writing a selection key overwrites a choice the user made in the agent's own UI.** pi and opencode both let a user pick a model interactively; a launch that rewrites it every boot is hostile. | The prism regenerates every boot, so this is a real hazard rather than a theoretical one. Likely answer: write the selection only when a profile is *explicitly* active for that agent, and never otherwise — which is what OQ-CS2 asks. |
| **§3's opencode and pi rows are not source-verified**, and the sibling doc exists because a value was written into an agent's config without checking its vocabulary. | Do not build D past codex until §8 step 1 closes. The same rule that produced D1 applies here, and this time it is written down first. |
| **`model` needs a model id, not just a provider.** Selecting a provider does not say which of its models to use. | The provider entry already carries `models` aliases; `default` is the obvious source. But it makes selection depend on an alias vocabulary that is open (OQ-CS3). |

---

## 8. What I would build, in order

1. **Research pi's and opencode's selection surface** and write it into §3 with source-verified
   evidence. **DONE 2026-09-02** — both rows filled from source (pi 0.84.4; opencode `v1.18.18`),
   two delivery defects found and filed as the sibling doc's D10/D11.
2. **Selection for codex**, the row that is already verified — `model_provider` + `model` from the
   active profile's provider. One derive, one behaviour, testable in isolation.
3. **Selection for pi and opencode**, once step 1 says where.
4. **The explicit disable (option C)**, if D has not made it moot.

The `mergeUnder` null bug in §4's note rides whichever commit touches composition next; it is
unrelated to the sequence above.

---

## 9. Open Questions

1. ✅ **OQ-CS1: Is D the shape — selection written into each agent's own selection key? — RESOLVED
   (2026-09-01).** *"Activating a profile should work for all."* Option D; B is off the table with
   it. Folded into §5 and §1's ships-today column.

2. ✅ **OQ-CS2: When a launch has no active profile, does yolo write the selection key? — RESOLVED
   (2026-09-01).** *"Default can be left to the specific agent."* Never touch it — the no-profile
   case is the agent's own business, and yolo writing a default would silently revert an
   interactive `/model` choice on the next boot. Folded into §5.1.

3. ✅ **OQ-CS4: Does a provider ever get to ADD a profile field? — RESOLVED (2026-09-01). YES, and
   it is the point.** *"Model can't be the only config we'll want, which is why I said the config
   surface/schema is dictated by the provider."* A provider declares an `options` block; a profile is
   an instance of it; the agent's derive decides where each option lands. My leaning — fix the field
   set at two and defer — is overruled. Folded into §5.2.

4. ✅ **OQ-CS5: At what SCOPE do user-declared profiles live? — RESOLVED (2026-09-01). User scope
   only, both keys.** *"Yes of course user."* Same rule `packs` follows: a workspace config travels
   with the repo and is agent-editable, and a profile steers which endpoint an agent talks to. The
   cost stands and is accepted — a project cannot ship "use this model for this repo". *(The naming
   half resolved in the same round: the selection key is `use_profiles` — §5.4.)*

5. ✅ **OQ-CS6: Confirm reversing `profiles-as-pack-variants.md` OQ-5 — RESOLVED (2026-09-01).
   Reversed.** *"Reversing old decisions is fine. We're debugging a mess of a design and need to
   change things."* Profile names stop being free-form: declaration is mandatory, and an undeclared
   name becomes a reportable error rather than a silent no-op. OQ-5 is **superseded** — it was ruled
   when a profile was a bare string. Folded into §5.2 property 3.

6. ✅ **OQ-CS3: Which model does selecting a provider select? — RESOLVED (2026-09-01), and the
   question dissolved.** *"Who is doing this selecting? Isn't this up to derive?"* — yes, and asking
   it exposes that I had not propagated
   [`provider-table-fidelity.md`](provider-table-fidelity.md) OQ-PT9 into this doc. Once everything
   goes to the derive, **core resolves no model at all**: it hands the derive the active profile
   (which carries the alias the user chose, if any) and the composed provider entry (which carries
   `models`), and the derive writes its agent's selection key. If the profile names no alias, the
   fallback is that agent's business — its own default, or the provider's `default` if its author
   wants that. **Core reserves nothing, so `default` stays an ordinary open-vocabulary alias** and
   the small closing my leaning proposed does not happen.

7. ✅ **OQ-CS8: Where does an agent's protocol binding live? — RESOLVED (2026-09-01), and my
   answer was stale.** *"'Move' means the pack handles it? I need clearer details here."* Fair — the
   leaning said "move the field" and by then there was no field to move:
   [`provider-table-fidelity.md`](provider-table-fidelity.md) OQ-PT9 had already ruled the whole
   substitution vocabulary out. Concretely, and this is the detail the question asked for:

   **Nothing declares the binding. The agent's pack composes it in its own derive.** `packs/claude`
   gains an env-emitting derive that reads the composed provider entry and returns the variables
   claude needs:

   ```lua
   yolo.derive("claude", "env", function(ctx)
     local p = ctx.providers[ctx.selected_provider]      -- the active profile's provider
     if not p then return {} end
     local ep = p.endpoints and p.endpoints.anthropic
     return {
       ANTHROPIC_BASE_URL  = ep and ep.base_url,
       ANTHROPIC_AUTH_TOKEN = p.api_key,                 -- resolved value (OQ-PT9)
       AWS_REGION          = p.region,
       ANTHROPIC_DEFAULT_OPUS_MODEL = p.models and p.models[ctx.profile.model or "default"],
     }
   end)
   ```

   **What this deletes, counted 2026-09-01** — the reason to prefer it over any declarative form:

   | Goes | Where |
   | :--- | :--- |
   | `EnvShape` field, `ValidateProviderEnvShape`, `KnownEnvShapeValue`, the four placeholder constants | [`packdecl/contributes.go`](../../internal/packdecl/contributes.go) |
   | `validateProviderEnvShape`, `env_shape` in `knownProviderKeys` | [`config/validate.go`](../../internal/config/validate.go) |
   | `agentProtocols`, `ProtocolFor`, `Resolve`, `providerVars` — most of a 250-line package | [`internal/agentenv`](../../internal/agentenv/agentenv.go) |
   | the env_shape skew-tolerance tests at both levels | packdecl, packload |

   **`agentProtocols` going is the one worth naming.** It is core's agent → wire-protocol table, and
   its own comment concedes it exists only because the manifest has no field for it. With the derive
   composing, each agent's derive reads the endpoint it speaks and core stops holding a table of
   agent names — which is `pack-code-separation.md`'s rule ("core does not know what an agent is")
   arriving somewhere it had been explicitly excepted.

   What core still does: compose the providers table, resolve which profile is active, hydrate the
   credential, and run derives. What it stops doing is knowing what any of it *means*.

8. ✅ **OQ-CS7: Does a provider's `options` declaration carry any validation or schema? — RESOLVED
   (2026-09-01). Option (i), and FLAT.** *"Yes, (i), but I don't get why `{options: {model: {default:
   foo}}}` and not just `{options: {model: foo}}`."* — no reason, and the nesting was the half-schema
   leaving a hole to climb back through. Once `kind`, `values` and enum checking are gone, `default`
   is the only field, and a one-key object is a wrapper with nothing to wrap.

   ```jsonc
   "options": { "model": "default", "thinking": "off" }    // name → default value
   ```

   It also matches its neighbour: `models` in the same manifest is already a flat name → value map,
   so the two read alike instead of one being an object-of-objects for no reason.

   **What core does, complete:** merge the declared defaults under the profile's own values, refuse a
   profile key the provider does not declare (naming what it does accept), and hand the result to the
   derive. It never asks what a value means — the derive validates and its errors propagate.

   > [!NOTE]
   > **One wrinkle, stated rather than discovered later: `null` is NOT the delete convention here.**
   > A declared option with no sensible default needs a spelling, and `"thinking": null` is it —
   > *declared, no default*, so a profile may set it and the derive gets nothing when the profile
   > does not. It deliberately does **not** mean "un-declare this option", which is the meaning
   > `null` carries almost everywhere else in this config. The reason to depart: un-declaring is a
   > thing nobody wants (a user gains nothing by removing an option their provider offers, since an
   > unset option already reaches the derive as nothing), while "keep the option, drop the default"
   > is a real override. Since the two readings would otherwise pick different behaviours for the
   > same syntax, the rule is worth writing into the key's own documentation.

9. ✅ **OQ-CS9: Does a profile point at a provider, inherit from another profile, or both? —
   RESOLVED (2026-09-01). Point only — option (a).** No `extends`, no inheritance semantics to
   define. A provider declares its options with defaults (OQ-CS4), so a profile states only what it
   changes and there is little left to duplicate. The residual case — a variant of a heavily
   customized profile, which would restate every option — is recorded as the evidence that would
   reopen this, and it does not exist yet because no profile in the tree carries more than one
   option. Same-NAME merging (a user's `zai` over a pack's `zai`, per field) is unaffected: that is
   a different feature and it stays.



10. ✅ **OQ-CS10: Does the HOST notch run the env derive? — WITHDRAWN (2026-09-01). It was not a
    design question.** *"I don't get it, why would we NOT support env on the host too?"* — right, and
    there is no case on the other side. `yolo host -- claude` composing the same environment is
    behaviour [`host-agent-environment.md`](host-agent-environment.md) §2.2 already fixes and this
    doc never proposed changing. Asking it as though it were open invited a "no" that nobody wants.

    **The underlying fact is real and moves to the plan**, where it is a constraint rather than a
    choice: env composition is host-launch-time with three consumers —
    [`profilechannel.go:93`](../../internal/cli/run/profilechannel.go#L93),
    [`host.go:432`](../../internal/cli/host.go#L432) (**no jail there**), and macos-user via
    `Options.PackEnv` — while a derive runs in-jail at boot. So OQ-CS8's work must give the host
    notch a way to run the env derive; it does not get to skip it.

    > [!WARNING]
    > **And the seam I named does not do what I said.** I leaned on
    > [`hostrender.go:377`](../../internal/entrypoint/hostrender.go#L377) as "reusing a path rather
    > than building one". `hostTableKeys` runs a derive host-side as a **key-name probe against
    > SENTINEL inputs**, and its own comment is explicit that content deliberately does not cross:
    > *"The CONTENT does not, and must not. A jail's derived MCP table embeds jail-absolute paths …
    > which is exactly why a host render passes no computed layer."* It cannot be reused to compose
    > real values. A host-side env derive needs REAL tables, which is a new invocation — and the
    > jail-absolute-path hazard that rule exists for is worth a pack-authoring note, since a derive
    > could put a jail path in an env value the host then exports.


---

## 10. Decision Ledger

Rulings compact into this table from §9, keeping their `OQ-CS<n>` IDs, with the normative text
folded into the section named in the last column.

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| OQ-CS1 | **Option D** — catalog from presence, selection written into each agent's own selection key. *"Activating a profile should work for all."* B (gating the catalog) rejected with it. | 2026-09-01 | §5, §5.1 |
| OQ-CS10 | **WITHDRAWN — not a design question.** `yolo host` composing the same env is behaviour already fixed; the fact underneath (host-launch-time composition, three consumers, one with no jail) is a plan CONSTRAINT. The `hostrender.go` seam cited for it is a sentinel key-name probe and cannot compose real values. | 2026-09-01 | §9 OQ-CS10 |
| OQ-CS7 | **Option (i), flat: `options` is a name → default-value map.** No `kind`, no `values`, no enum — `default` was the only field left, so the wrapper object goes; it now reads like its neighbour `models`. Core merges defaults, refuses an undeclared profile key, and validates no value. `null` means *declared, no default* — deliberately not the delete convention. | 2026-09-01 | §5.2, §9 OQ-CS7 |
| OQ-CS3 | **Core resolves no model** — it hands the derive the active profile and the provider entry; the derive writes its agent's selection key and picks its own fallback. `default` stays an ordinary open-vocabulary alias. | 2026-09-01 | §9 OQ-CS3 |
| OQ-CS8 | **Nothing declares the binding — the agent's pack composes it in its own env-emitting derive.** Deletes `env_shape` and its validators, the four placeholder constants, and most of `internal/agentenv` including core's `agentProtocols` agent→protocol table. | 2026-09-01 | §3.1, §9 OQ-CS8 |
| OQ-CS5 *(scope)* | **User scope only, both keys** — a profile steers which endpoint an agent talks to, and a workspace config is agent-editable. Same rule `packs` follows. | 2026-09-01 | §9 OQ-CS5 |
| OQ-CS5 *(naming half)* | **The selection key becomes `use_profiles`**; `pack_profiles` retired by name — it named neither packs nor profiles, its keys being CLI names. The scope half stays open. | 2026-09-01 | §5.4 |
| OQ-CS9 | **Profiles POINT at a provider; no `extends`.** Provider-declared option defaults already remove the duplication inheritance would fix; same-name merging is a separate feature and stays. | 2026-09-01 | §5.2, §5.4 |
| OQ-CS4 | **A provider declares an `options` block; a profile is an instance of it** — *"model can't be the only config we'll want."* The derive decides where each option lands; core learns no option names. Supersedes the fixed two-field draft. | 2026-09-01 | §5.2 |
| OQ-CS6 | **OQ-5's free-form profile names are SUPERSEDED** — declaration is mandatory, and an undeclared name is a reportable error, not a silent no-op. *"Reversing old decisions is fine."* | 2026-09-01 | §5.2 property 3 |
| OQ-CS2 | **Never write the selection key when no profile is active** — *"default can be left to the specific agent."* The no-profile case is the agent's own, and a written default would revert an interactive choice each boot. | 2026-09-01 | §5.1 |

---

## 11. Success criteria

- `yolo -p zai -- pi` starting pi **on z.ai**, not on pi's built-in default with z.ai sitting
  unused in the directory.
- A user turning a provider off without editing the settings they want back — and the same config
  turning it on again with one word.
- Every row of §3 carrying source-verified evidence, including the two that do not today.
- `yolo -- pi` with no profile leaving pi's own model choice exactly as the user left it.
