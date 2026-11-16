---
title: "A models `only` can name the agents it narrows, so one agent's menu goes exact and the others do not move"
date: 2026-11-16
status: accepted
stage: BUILT
next: "Review the repaired candidate, then run the parent-owned combined gates and scoped jail probe"
tags: [providers, models, pickers, packs, bedrock, claude, audience]
summary: "An `agents` audience on a `models` contribution's `only`, carried as one composer-owned fact and applied per agent where a derive context is built. Claude's Bedrock menu becomes exact; the shared provider table and every other agent's render stay byte-identical."
---

# A models `only` can name the agents it narrows, so one agent's menu goes exact and the others do not move

**Status:** 2026-11-16. Implemented in the isolated candidate, with focused repairs over `261b1aad96ea2bde781b83e941b4edf3338dc781`. Production-caller regressions cover readable user rows, helper-context projection, legacy config editing, host launch selection, native and bridged Claude menus, and unchanged Pi catalog behavior. Independent repair review, the combined CI/integration gates and a scoped-feature jail probe remain outstanding. Current candidate evidence is distinguished from historical requester renders in [§7](#7-acceptance-tests).

**Disposition:** IMPLEMENT

> **In short.** The feature makes sense, and the right form is an audience on the existing `only` verb, not a Claude knob. The shared provider table stays as it is today. An agent-scoped `only` is recorded as one composer-owned fact and applied only to the named agent's view at the single point where a derive context is built.

**Why it matters.** Today an `only` marks the whole provider, so making Claude's `/model` exact also turns Pi's wildcard into an explicit, enforced list and drops its catalog.

**The shape.** `agents = [...]` on a `models` contribution with `only`, a composed **agent view** record on the provider entry, and one pure projection at the derive boundary. `packs/claude/derive.lua` does not change.

**Cost.** One new composed key that every wholesale reader of the provider table must tolerate, and a scoped `only` that is a menu and client allowlist but never a wire refusal.

**Start at [§3](#3-selected-architecture)**. The rest follows from it.

**Needs your ruling:** None.

**Reads with:** [`model-lists-and-pickers.md`](model-lists-and-pickers.md) (owns the `models` kind, the two-pass composition and the additive-list ruling this design keeps).

---

## 1. Problem and non-goals

On the shared `bedrock` provider, Claude's `/model` shows the four declared Anthropic rows and Claude's own built-in rows, because nothing narrowed the list. The only lever that makes the menu exact is an `only`, and an `only` marks the provider for every agent.

The goal is that Claude's menu is exactly the declared Anthropic rows in provider order, while Pi, `pi/models`, `pi/settings` and Copilot render byte-identically with and without the Claude-specific declaration.

The four ids in the request are fixture data. No production code names a model id, a provider name or an agent name.

Non-goals:

- No change to what an unscoped `add` or `only` does, and no change to any default.
- No change to `enforce_models`: its meaning, scope and default stay as they are.
- No wire-level refusal from a scoped `only`.
- No new credential grant, route, provider identity, network fallback or live config change.
- No settings-overlay precedence change.
- No agent-scoped `add` ([SM-D3](#SM-D3)).

## 2. Evidence

Read directly at the baseline:

- `packdecl.ModelsContribution` carries `Provider`, `Add` and `Only`, and `modelsProblems` requires exactly one verb.
- `shapeProviderModels` applies every `add`, then intersects every `only`, then sets `models_only`. It runs before the user layer merges.
- A user-declared id is exempt from an `only`: the user's `providers.<name>.models` writes last, per alias.
- `materializeOrder` writes `order` facts onto any provider that a contribution names. So an unscoped contribution changes the shared entry even when it removes nothing.
- The claude derive's exact branch is keyed on `p.models_only == true`, and it already separates the picker from the allowlist with `ctx.enforce_models`.
- [OQ-MM1](model-lists-and-pickers.md#OQ-MM1) rules that only an `only` makes a menu exact. [MM-D11](model-lists-and-pickers.md#MM-D11) records the two-pass composition and that `models_only` is a composed fact no user key can spell.

Returned by one factual scout at the same commit (not re-read by this seat):

- `models_only` is read well beyond Claude: by the pi, omp, opencode, codex and copilot derives, and by `wirebridged`'s `narrowedList`, `HasModelList`, `UnnarrowedMenus` and the presence report. It is a shared marker.
- Every production caller of `ComposeProviders`, `ComposeProvidersAt` and `ComposeProviderDeclarations` is a shared consumer (services, credentials, routes, checks, validation). None has an agent to compose for.
- One provider table is shared by every derive in a render. The only per-agent projection today is of `mcp_servers`.
- Each derive context already carries the agent's name as `ctx.agent`. `AgentEnv` also knows the agent it serves.
- The audience namespace for `briefing`, `skills` and `files` is the bin namespace. `agent` and `agents` are refused on every other kind by `validateContribution`.
- There is no pack-owned option channel. Options are declared by the pack that declares the provider, are string-or-null, and reach every agent's derive as `ctx.profile`.
- `enforce_models` is a user-scope boolean on a provider variant (or legacy profile). It is not a Claude switch.

From the request's own experiments (rendered by the requester, not re-run here): an unscoped `only` of the four ids left Pi with all ten rows but flipped it to an explicit, enforced list. That shows the rows there are user-declared and survive an `only`, and that the harm to Pi comes from the provider-wide mark alone.

## 3. Selected architecture

### 3.1 The declaration

A `models` contribution whose verb is `only` may carry `agents`, a non-empty list of agent names. The names are the same opaque bin-namespace strings the audience on `briefing`, `skills` and `files` uses.

- Absent `agents`: today's contribution, unchanged in every respect.
- `agents = []`: refused by validation, because an empty audience names nobody.
- A repeated name: refused.
- `agents` with `add`: refused, with a message saying a scoped contribution may only narrow ([SM-D3](#SM-D3)).
- `agent` (singular, a destination's identity): still refused on `models`.
- A name no selected pack's program answers to: inert and silent. Core has no agent registry to check it against.

`yolo pack lint` accepts the declaration because lint runs the same manifest validation.

### 3.2 The composed fact

**Agent view** *(coined here)*: for one provider and one agent name, the set of model ids that agent's menu keeps. It is not a second model list, and it is not what a route serves.

The composer records agent views on the provider entry under one composer-owned key (spelling is the implementer's; `models_views` is used below). Like `models_only`, it is absent from the user schema, so config cannot author it, and `ComposeProviderDeclarations` removes it before typed decoding.

For each agent a scoped `only` names on a provider, the view's keep-set is:

- the intersection of the id sets of every scoped `only` whose audience includes that agent,
- plus every id the user's own `providers.<name>.models` declares.

A scoped contribution writes nothing else into the shared table. It does not materialize `order` facts, does not set `models_only`, does not delete a row, does not create a provider entry, and does not change the order of providers. With the view key removed, the shared table is byte-identical to the table composed without the contribution.

### 3.3 The projection

**Projection** *(coined here)*: a pure function from the composed provider table and an agent name to the table that agent's derive sees.

- For every provider, it removes the view key. No derive ever sees a view, its own or another agent's.
- For a provider with a view for this agent, it removes every alias whose id is outside the keep-set (and that alias's `model_options`), then sets `models_only` to true. Surviving rows keep their order and facts.
- For a provider with no view for this agent, nothing else changes.
- It never mutates the shared table. It copies the entries it changes.

The projection runs at the one place a derive context is assembled, where `ctx.agent` is already known, so every derive invocation is covered: surface renders, `AgentEnv`, host launch selection and the codex menu derive. Where the function's code lives is the implementer's choice.

### 3.4 What the named agent then does

Nothing new. Claude's derive sees an ordinary narrowed provider and takes its existing exact branch:

| Case | Result for Claude |
| :--- | :--- |
| Native Bedrock, `enforce_models` on | Exact picker, `availableModels`, `enforceAvailableModels`, tier pins from the list |
| Native Bedrock, `enforce_models = false` | Exact picker and tier pins; no `availableModels`, no `enforceAvailableModels` |
| Bridged Bedrock | Picker narrowed from the whole routed list to the view; the allowlist follows `enforce_models` as above |
| Host and jail | Same rules; both build derive contexts through the same projection |

Any other agent named in `agents` gets exactly what it already renders for an unscoped `only`.

### 3.5 Rejected alternatives

| Alternative | Disposition |
| :--- | :--- |
| **B: a Claude menu option** (`claude_menu = "exact"`) | Rejected. It makes an add-only list exact, which contradicts [OQ-MM1](model-lists-and-pickers.md#OQ-MM1)'s "only an `only` makes a menu exact". Options are owned by the provider's declaring pack, so a Claude-named knob would sit in a shared provider's vocabulary. It cannot choose a subset of rows, and it is a second way to say "exact". |
| **C: let a settings overlay override `modelPicker`** | Rejected. The derive owns that key, and the overlay would not write the allowlist or the tier pins. |
| **Compose a provider table per agent** | Rejected. Every composer caller is a shared consumer with no agent to pass, and a jail holds only the composed table. |
| **Have each derive read the view key itself** | Rejected. Every derive would learn a second shape and see other agents' views. |
| **Tier aliases alone** | Insufficient. They fix subagent defaults and leave the built-in rows in the picker. |
| **Scoped `add`** | Rejected for now. A row that exists for one agent but not for the shared services that read the list is a new incoherence nobody asked for; `vendor` already decides which agents are offered a row. |

The request's shortlist was checked against what the tree already has: no existing mechanism scopes a model list by agent, and the audience vocabulary exists only on the three content kinds.

## 4. Composition, precedence and ordering

1. The packs' own providers and inheritance compose as today.
2. Every unscoped `add` applies, then every unscoped `only` intersects, as [MM-D11](model-lists-and-pickers.md#MM-D11) records.
3. The user's `providers` layer merges last, as today.
4. Agent views are then recorded on providers that exist in the final table. This step reads the scoped contributions and the user's declared ids, and writes only the view key.
5. The projection applies a view when a derive context is built.

Rules that follow:

- **Intersection.** Several scoped `only`s for one agent intersect, whatever pack or order they come from. Because unscoped `only`s already narrowed the shared table, the agent's menu is the intersection of all applicable `only`s.
- **Never adds.** A view only removes rows. An id in a scoped `only` that the list does not hold is dropped.
- **User writes last.** A user-declared id survives a scoped `only`, as it survives an unscoped one. The user cannot author or delete a view; deselecting the pack is the way out.
- **Order.** Surviving rows keep the shared table's order. The order of ids in `only` means nothing.
- **Inheritance.** A scoped contribution names a provider and reaches a child that inherits from it exactly as an unscoped `only`'s mark does today (unverified detail in [§8](#8-risks-and-unverified-items)).
- **Empty view.** A view that keeps nothing gives the agent a narrowed, empty list, and it renders whatever it renders today for an unscoped `only` that keeps nothing.
- **Fetched lists.** A scoped `only` is not a supplied list. It does not change `HasModelList`, the presence report, or whether a Bedrock list is fetched.
- **Concurrency.** None arises: composition and projection are pure functions of their inputs, and each launch composes for itself.
- **Existing state.** Nothing migrates. A manifest without `agents` composes byte-identically.

Diagnostics, through the existing `WithModelNotes` channel read by `yolo check`:

- A scoped `only` on a provider nobody declares produces today's "shapes nothing" note, and nothing about its ids.
- A scoped `only` on a provider the user set to null says nothing.
- An id that no provider, `add` or user entry lists produces today's dropped-id note, naming the audience.
- A view that keeps nothing produces one note naming the provider and the agent.
- Notes are per contribution. A derive never sees them, so one agent's note cannot reach another agent's render.

## 5. Enforcement

- **One chokepoint.** The projection runs where the derive context is built, so a derive cannot receive an unprojected table. The view key reaching Lua is a defect that a test must catch.
- **Shared questions read the shared table.** Routes, services, credentials, the wire bridge's allowlist, fetch decisions and typed declarations never consult a view. A consumer's menu therefore cannot narrow a route or an authority.
- **Per-agent questions read the projected view.** `UnnarrowedMenus`, which asks whether one program's menu is narrowed while enforcement is off, evaluates that program's view.
- **`enforce_models` is the only enforcement switch.** A scoped `only` adds no second policy. With the switch off, the named agent keeps its exact menu and installs no refusal.
- **A scoped `only` is not a security boundary.** The bridge still serves every model the shared list holds. A company that needs a wire refusal ships an unscoped `only`.

## 6. Ownership, security and compatibility

- **Core names no agent.** Audience strings are opaque and matched by equality against the agent name the derive context already carries.
- **The Claude pack is unchanged.** Its derive keeps owning `modelPicker`, and the exact branch stays keyed on `models_only`.
- **One writer.** Only the composer writes the view key. Only the projection reads it.
- **No leak.** The key is stripped from every agent's view and from typed declarations.
- **The additive ruling is preserved.** An unscoped list that only adds still sits beside Claude's built-ins with `replaceBuiltInOptions: false` and no allowlist. Exactness still comes only from an `only`; this design lets that `only` say whose menu it means.
- **Old manifests.** Unaffected.
- **New manifest on an older yolo.** Refused by the existing "does not take `agents`" validation, which is the correct failure.
- **Host and jail skew.** A newer host handing a composed table to an older in-jail entrypoint is the pairing the existing source-skew check refuses.

## 7. Acceptance tests

Each is an observable outcome. The render tests must drive the production callers (`ConfigurePackSurfaces`, `AgentEnv`, host launch selection, run composition), in the manner of `modelsonlyagents_test.go` and `claudebedrocksettings_test.go`, so they fail if the projection's production call site is removed.

Fixture: a `bedrock` provider with ten declared rows, four of them Anthropic (the request's ids), and a pack declaring `only` of those four with `agents = ["claude"]`.

1. Claude's settings on native Bedrock hold exactly the four rows in provider order (Fable 5.2, Haiku 5.5, Opus 5.5, Sonnet 5.5) with `replaceBuiltInOptions: true`.
2. With `enforce_models = false`, the same picker renders, and `availableModels` and `enforceAvailableModels` are absent.
3. With enforcement on, `availableModels` holds the four ids, default first, and `enforceAvailableModels` is true.
4. `pi`, `pi/models`, `pi/model-lists`, `pi/settings` and the Copilot surface are byte-identical with and without the contribution. The company-add fixture supplies ten rows. Pi keeps ten explicit `amazon-bedrock/<id>` enabled models, and its native model-list artifact has `keep_catalog:true` and `enforce:false`. The request's historical `amazon-bedrock/*` enabled-model baseline is not reproduced by this fixture; current no-list fallback remains `amazon-bedrock/**`. Do not change Pi to recreate a historical render.
5. The composed provider table, with the view key removed, is byte-identical with and without the contribution.
6. Claude's render with the scoped `only` equals Claude's render with the same `only` unscoped.
7. On bridged Bedrock, Claude's picker holds the four rows, and the bridge's allowlist is unchanged from the no-contribution case.
8. Claude's launch environment (tier pins) and host launch selection follow the view, on host and in a jail.
9. No derive context exposes the view key, for any agent.
10. Two scoped `only`s for one agent intersect. A scoped and an unscoped `only` intersect.
11. `yolo pack lint` passes the fixture pack. It refuses `agents = []`, a repeated name, `agents` with `add`, and `agent` on `models`.
12. With no `bedrock` provider, every render is byte-identical to before and no note mentions an `only` id. With the provider set to null, there is no note at all.
13. An unscoped add-only list still renders beside Claude's built-ins, as `TestClaudeAppendsAListThatOnlyAddsBesideItsOwnMenu` pins today.

## 8. Risks and unverified items

- **R1. Wholesale readers of the provider table.** Any shared code that serializes, hashes or strictly decodes a whole provider entry will see the new key. Test 5 bounds the damage, but the planner must find those readers. Not surveyed here.
- **R2. The chokepoint is assumed, not proven.** The scout reports that both the surface render and `AgentEnv` build a derive context through the same layer that already filters `mcp_servers`. That every derive invocation passes through it is unverified. Test 9 is the guard.
- **R3. Inheritance parity is specified by reference.** How `models_only` reaches an inheriting provider today was not read. The rule is "the same as the unscoped mark"; the planner confirms what that is.
- **R4. User-declared rows survive.** In the owner's fixture the ten rows appear to be user-declared, so the `only` removes nothing and the menu is exact because Claude offers only the Anthropic rows. A fifth user-declared Anthropic row would appear in the menu. This is existing [MM-D11](model-lists-and-pickers.md#MM-D11) behavior, kept on purpose.
- **R5. A user-overridden alias.** Where the user re-points a pack's alias at another id, the view keeps the merged facts for that alias. An unscoped `only` would have dropped the pack's facts first. The difference is confined to facts on that one alias.
- **R6. Selected provider, empty list.** If the provider exists but nothing declares the ids (a fetched list only), today's dropped-id notes fire for a scoped `only` as they do for an unscoped one. The request asks for silence only when the provider is absent.
- **R7. Fetched rows and the view.** Where a fetched Bedrock list joins the table relative to the projection was not read. The rule is that the projection applies to whatever rows the table holds when the derive context is built.
- **R8. No live model call.** Candidate boot rendering and host launch selection are exercised without starting coding agents or calling model/AWS APIs. The matched development bundle's earlier no-pack nested launch verifies boot/cleanup only. A selected-pack scoped-feature jail render and the combined CI/integration gates remain parent-owned verification gaps; requester live renders are not candidate evidence.

## 9. Decision ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="SM-D1"></a>SM-D1 | Implement. An agent-scoped exact menu is a sound request, and the additive default stays. | 2026-11-16 | [§1](#1-problem-and-non-goals) | Implemented in candidate |
| <a id="SM-D2"></a>SM-D2 | The mechanism is an `agents` audience on a `models` contribution's `only`. A Claude option and an overlay override are both rejected. | 2026-11-16 | [§3.5](#35-rejected-alternatives) | Implemented in candidate |
| <a id="SM-D3"></a>SM-D3 | `agents` is accepted with `only` alone. With `add` it is refused; `agent` stays refused; an empty or repeated audience is refused. | 2026-11-16 | [§3.1](#31-the-declaration) | Implemented in candidate |
| <a id="SM-D4"></a>SM-D4 | A scoped `only` writes one composer-owned view key and nothing else into the shared table. | 2026-11-16 | [§3.2](#32-the-composed-fact) | Implemented in candidate |
| <a id="SM-D5"></a>SM-D5 | A pure projection applies the view and strips the key where the derive context is built. No derive learns a new shape, and `packs/claude/derive.lua` is unchanged. | 2026-11-16 | [§3.3](#33-the-projection) | Implemented in candidate |
| <a id="SM-D6"></a>SM-D6 | Applicable `only`s intersect, user-declared ids survive, and views are recorded after the user layer. | 2026-11-16 | [§4](#4-composition-precedence-and-ordering) | Implemented in candidate |
| <a id="SM-D7"></a>SM-D7 | Shared consumers never read a view. A scoped `only` is a menu and client allowlist under `enforce_models`, never a wire refusal. | 2026-11-16 | [§5](#5-enforcement) | Implemented in candidate |
| <a id="SM-D8"></a>SM-D8 | [OQ-MM1](model-lists-and-pickers.md#OQ-MM1)'s additive ruling is preserved, and this design extends [§7](model-lists-and-pickers.md#7-how-a-company-pack-shapes-a-list-the-models-kind) of that document with an audience. | 2026-11-16 | [§6](#6-ownership-security-and-compatibility) | Implemented in candidate |