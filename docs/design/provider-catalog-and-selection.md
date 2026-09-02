---
title: "A catalog and a selection are two features — and only one of them ships"
date: 2026-09-01
status: draft
tags: [providers, profiles, packs, derives, selection, zai]
summary: "Splits the knot that profiles-as-pack-variants and zai-plumbing left tangled. Populating an agent's provider directory and telling that agent which provider to USE are different features with different triggers; today the first works for every agent and the second is implemented for claude only. Measured in a live jail: pi, opencode and codex all carry zai in their catalog with no selection key set, so `-p zai` reaches three of the four agents it claims. The disable-without-deleting complaint falls out of the same conflation."
---

# A catalog and a selection are two features — and only one of them ships

**Status:** DESIGN, 2026-09-01. Nothing built. **OQ-CS1 and OQ-CS2 ruled the same day**
(ledger, §10); OQ-CS3 open, and §3's research gap is the real blocker.

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
| Natural trigger | a provider entry being **present** | an explicit act — `-p zai`, `pack_profiles` |
| Scope | every agent that speaks a protocol the provider offers | the agent(s) being launched |
| Lifetime | durable — it is a directory | per-launch |
| Ships today | ✅ all four agents | ⚠️ **claude only** |

They are orthogonal. A user can reasonably want a catalog of five providers and select one; or a
catalog of one and select nothing. The confusion this doc exists to end is that **one table drives
both**, so wanting a durable catalog entry and not wanting to use it right now has no spelling.

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
| opencode | `opencode.json` | top-level `model`, as `"<provider>/<model>"` | **moderate** — the shipped binary's `models` command prints ids in exactly that form; not read from source |
| pi | **unknown** | — | **none.** `models.json` carries only `providers`; pi's default model lives somewhere this doc has not identified |

**The pi row is the blocker**, and it is a research task, not a design one: until it is known where
pi records "the model I use", no design can say what selecting a provider for pi means. §8 puts it
first for that reason.

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
found.* That is stricter than what ships and better defined — and adopting it repairs a defect the
sibling doc reports separately. Today the requirement is computed from what a pack **declares**
(`requiredProviders` walks `p.Decl.Providers()`), not from the composed catalog, so a user who drops
a provider with `null` **still has the launch refused for it** — measured: with `packs: ["claude"]`
and `providers: {"bedrock": null}` the catalog composes empty and `bedrock` is still required. Keyed
to catalog membership instead, the null removes the entry and the requirement with it, and the rule
reads as one sentence: **in the dictionary means you need the key; not in it means you do not.**

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
3. **A profile that names no provider is still a valid gate.** Declaration is *optional*: the name
   alone continues to gate `profile:`-modified contributions and to reach a derive as
   `ctx.pack_profiles`, which is what today's free-form names already do. Declaring a profile adds
   selection semantics; it does not become a prerequisite for the name working.

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
  "pack_profiles": { "claude": "zai-fast" }                  // or: yolo -p zai-fast
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

#### What this leaves undecided

The provider-defines-the-surface rule has a cheap reading and a general one, and §9's OQ-CS4 asks
which. The cheap one: a profile's legal fields are **derived** from the provider's own shape —
`model` is legal because the provider has `models`, and nothing else is legal yet. The general one:
a provider declares an explicit options block naming its tunables. The cheap one needs no schema at
all and is what the worked examples above assume.

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
   evidence. This is a documentation task with no code, and **nothing else here can be designed
   until it lands** — one row of §3 is empty and one is inferred.
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

3. 💬 **OQ-CS4: Is a profile's legal field set DERIVED from the provider's shape, or DECLARED by
   it?** §5.2. Derived: `model` is legal because the provider has `models`, full stop — no schema,
   nothing to author. Declared: a provider carries an options block naming its tunables, which is
   the general extension point and is schema describing schema.

   _Leaning:_ Derived, for v1. It needs nothing authored, it covers every case in the tree, and the
   ruling it implements — *"the config surface of a profile needs to be defined by the provider"* —
   is satisfied either way, because the provider's own shape IS a definition. The declared form
   earns its place the first time a provider has a tunable that is not a model, and not before;
   building it now would mean designing an options vocabulary against zero consumers, which is the
   mistake `wire_api`'s enum already made once.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-CS5: Where do user-declared profiles live in config, and at which scope?** §5.2's
   examples assume a top-level `profiles` key beside `pack_profiles` (declaration and selection
   kept separate, since one is durable and the other per-launch). The scope question is the sharper
   half: `packs` is **user-scope only**, because a workspace config travels with the repo and is
   agent-editable.

   _Leaning:_ A separate top-level `profiles` key, **user-scope only**, for the same reason `packs`
   is. A profile names a provider and therefore steers which endpoint an agent talks to; a repo
   that could set that could point its own agent at a service the user never chose. Merging
   declaration into `pack_profiles`' values (making them objects as well as strings) is the
   tempting alternative and I would not take it — it overloads one key with a durable declaration
   and a per-launch selection, which is the exact conflation this doc exists to undo.

   **Answer:**
   > _(empty — fill in when decided)_

5. 💬 **OQ-CS3: Which model does selecting a provider select?** A provider carries `models` aliases
   (`default`, `fast`, …) and the alias vocabulary is deliberately open. Selection needs one
   concrete id.

   _Leaning:_ The `default` alias, and refuse the selection with a named error when the provider
   has no `default` — rather than picking one, which would make the choice depend on map order.
   That effectively promotes `default` from an open-vocabulary alias to a reserved one, which is a
   small closing of something the parent design deliberately left open, and should be recorded as
   such if it is taken.

   **Answer:**
   > _(empty — fill in when decided)_

---

## 10. Decision Ledger

Rulings compact into this table from §9, keeping their `OQ-CS<n>` IDs, with the normative text
folded into the section named in the last column.

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| OQ-CS1 | **Option D** — catalog from presence, selection written into each agent's own selection key. *"Activating a profile should work for all."* B (gating the catalog) rejected with it. | 2026-09-01 | §5, §5.1 |
| OQ-CS2 | **Never write the selection key when no profile is active** — *"default can be left to the specific agent."* The no-profile case is the agent's own, and a written default would revert an interactive choice each boot. | 2026-09-01 | §5.1 |

---

## 11. Success criteria

- `yolo -p zai -- pi` starting pi **on z.ai**, not on pi's built-in default with z.ai sitting
  unused in the directory.
- A user turning a provider off without editing the settings they want back — and the same config
  turning it on again with one word.
- Every row of §3 carrying source-verified evidence, including the two that do not today.
- `yolo -- pi` with no profile leaving pi's own model choice exactly as the user left it.
