---
title: "Models that follow you into pi's extensions — without yolo owning the extensions"
date: 2026-09-25
status: draft
tags: [pi, extensions, models, subagents, workflows, tiers, defaults, research]
summary: "pi extensions that spawn agents mostly inherit the session's model, so yolo's default already follows them. The ones that don't keep their own role config (tiers, model classes, per-agent overrides) in files yolo never renders, and pi has no model-role concept to aim at. The proposal: core exposes yolo's existing tier aliases to pack derives, extensions get ADAPTER PACKS that someone other than yolo ships, the one adapter yolo ships today (pi-subagents' block in pi's derive) moves out, and a model-roles setting is proposed upstream to pi."
---

# Models that follow you into pi's extensions — without yolo owning the extensions

**The question this doc answers:** pi extensions (workflows, sub-agent runners) pick models for the
agents they launch. How does the default model a user carries everywhere reach those child agents,
when yolo can configure pi but not each extension, and must not take an opinion on which
extensions to support?

**Status:** DESIGN, 2026-09-25. Nothing built. **MEASURED:** the model-selection code of eight
extensions and of pi 0.87.1, read from the published packages (versions in
[Appendix A](#appendix-a-evidence)). **UNMEASURED:** no extension was run; no agent was started.

**Needs your ruling:** [OQ-XM1](#OQ-XM1) (the core helper), [OQ-XM2](#OQ-XM2) (the role
vocabulary), [OQ-XM3](#OQ-XM3) (the `subagents` block yolo already ships), [OQ-XM4](#OQ-XM4)
(an env convention, or not yet), [OQ-XM5](#OQ-XM5) (the upstream proposal).

**Reads with:** [`model-lists-and-pickers.md` §6](../design/model-lists-and-pickers.md#6-tier-aliases-default-fast-balanced)
(the tier aliases this reuses), [`pi-model-selection-ux.md`](pi-model-selection-ux.md) ([OQ-PM1](pi-model-selection-ux.md#OQ-PM1),
which this reframes), [`provider-credential-scope.md`](../design/provider-credential-scope.md#OQ-CN6)
(why a jail-wide env var is the wrong vehicle today).

---

## Defined terms

- **Inherit** — an extension that, when nothing names a model, gives the child agent the parent
  session's current model (pi's `ctx.model`).
- **Role** — a capability name an extension asks for instead of a model id: `fast`, `balanced`,
  `small`, `big`. pi has none; several extensions invent their own.
- **Tier alias** — yolo's existing name for the same idea on a provider: `default`, `fast`,
  `balanced` in the provider's `models` map
  ([model-lists-and-pickers §6](../design/model-lists-and-pickers.md#6-tier-aliases-default-fast-balanced)).
- **Adapter pack** *(coined here)* — an ordinary yolo pack whose only job is to render yolo's
  resolved tier aliases into ONE extension's own config file. It needs no new pack kind: a
  `config` surface plus a derive does it (see [§4](#4-what-already-exists-to-build-on)).

## 1. The answer in short

- **Most extensions already follow the user.** Six of the eight read inherit when nothing names a
  model, and yolo already writes pi's `defaultProvider`/`defaultModel`/`enabledModels`. A user who
  never configured an extension's models gets yolo's default in every child agent today.
- **The failure is extensions with their own role config.** It lives in a file yolo never writes
  (`~/.pi/workflows/model-tiers.json`, `~/.pi/agent/config/pi-task-models/config.json`, pi-subagents'
  `subagents.agentOverrides`). A value set there does not move when the profile moves. And because
  `~/.pi` is per-workspace state in a jail, it doesn't follow the user to the next workspace either.
- **pi has no role concept to aim at.** Extensions get `ctx.model`, `ctx.scopedModels` and the
  registry, and nothing named "fast". So every extension that wants a cheaper child model invents
  a vocabulary: tiers `small`/`medium`/`big`, classes `fast`/`balanced`/`frontier`/`fav`.
- **yolo already solves this for claude.** Claude Code has roles built in (`opus`/`sonnet`/`haiku`,
  subagent `model: inherit`), and yolo's claude derive maps the provider's tier aliases onto them
  through `ANTHROPIC_DEFAULT_*_MODEL`. pi needs the same mapping. The difference is that pi's roles
  live in each extension rather than in the agent.

## 2. How the extensions pick models

Every row was read from the published package; paths and lines are in
[Appendix A](#appendix-a-evidence).

| Extension | When nothing names a model | Its own model config | Follows yolo's default? |
| :--- | :--- | :--- | :--- |
| `pi-subagents` 0.35.1 / 0.71.0 | inherits the parent session model | `subagents.defaultModel`, `modelScope`, `agentOverrides` in pi's `settings.json`; agent frontmatter `model:`. Bundled agents pin no model | **yes**, until the user sets `defaultModel` or an override |
| `pi-archimedes` subagent | agent `model` → call `model` → the active session model | per-agent overrides in `~/.pi/agent/agents.local.json` | **yes**, until a local override |
| `@quintinshaw/pi-dynamic-workflows` 3.13.0 | explicit → `tier` → session model when no tiers file; untagged → `medium` tier if configured | tiers in `~/.pi/workflows/model-tiers.json`, a per-project overlay | **yes** with no tiers file; **no** once tiers are set |
| `@arhen/pi-core-subagent` 1.3.56 | `ctx.model` | agent files' `model` | **yes** |
| `@bacnh85/pi-subagent` 0.22.6 | the authenticated parent model | explicit only | **yes** |
| `pi-prompt-template-model` 0.12.3 | the inherited model | a template's `models` list | **yes** |
| `@henryqw/pi-subagent` 22.2.0 | a `modelClass` route (`fast` by default) | via `@henryqw/pi-task-models` | **no**: needs the shared config |
| `@henryqw/pi-task-models` 7.0.2 | a class with no config is "missing" and warns | `~/.pi/agent/config/pi-task-models/config.json` | **no** |

`@henryqw/pi-task-models` is worth singling out. It is the ecosystem's own attempt at the missing
layer: a shared control plane where extensions declare tasks with a default class, and the user
maps each class to a model once. Today only its author's extensions consume it.

## 3. What pi offers, and what other agents do

**pi 0.87.1 has no roles.** Its model settings are `defaultModel`, `enabledModels` and
`modelThinkingLevels` (`docs/settings.md`). An extension's `ExtensionContext` carries `model`
(the current model), `scopedModels` (`enabledModels` resolved against the catalogue) and
`modelRegistry`. So "inherit" is well supported, and "give me the fast model" is not expressible.

**The other agents put roles in the agent:**

| Agent | Roles | Inherit | yolo today |
| :--- | :--- | :--- | :--- |
| Claude Code | `opus`, `sonnet`, `haiku` (and `fable`); a subagent's `model:` field | `inherit` from the main conversation; `CLAUDE_CODE_SUBAGENT_MODEL` forces one | maps tier aliases → `ANTHROPIC_DEFAULT_*_MODEL` |
| opencode | `small_model` for side tasks | a subagent with no model uses the invoking primary agent's | maps an alias → `small_model` |
| pi | none | via `ctx.model`, per extension | writes the default and the scope only |

So claude plugins that ship agents saying `model: haiku` already follow the user's provider,
because the role is resolved by the agent. That's why [§5](#5-options-with-verdicts)'s
mechanism doesn't need to reach claude or opencode plugins today.

## 4. What already exists to build on

- **The vocabulary.** Providers declare tier aliases (`default`, `fast`, `balanced`) in their
  `models` map; claude's and opencode's derives already consume them.
- **The delivery path.** A pack derive receives `ctx.providers`, `ctx.selected_provider` and
  `ctx.profile` ([pack-system reference](../reference/pack-system.md)). So a pack that declares a
  `config` surface for an extension's file can, today, resolve the selected provider's aliases and
  write them in that extension's format. An adapter is an ordinary pack. Not verified: that a
  non-pi pack may declare a surface under `~/.pi` without a reservation conflict; the collision
  rules suggest yes.
- **Git packs fetch at launch** (`9dbbfd04`), so an adapter living in the extension author's own
  repo reaches a user by adding one `packs` entry.
- **The one adapter yolo already ships.** `packs/pi/derive.lua` writes pi-subagents' `subagents`
  block (`defaultModel`, a strict `modelScope`) in its `openai-codex` branch, added 2026-09-15
  (`d3360c00`, "constrain codex workflow models"). It is exactly an adapter, embedded in the
  agent's own pack and naming one extension. [OQ-PM1](pi-model-selection-ux.md#OQ-PM1)
  asks what it should do for other providers.

## 5. Options, with verdicts

| Option | Who writes what | An extension nobody adapted | Verdict |
| :--- | :--- | :--- | :--- |
| **(a) Adapter packs.** Core resolves roles for derives; an adapter pack renders them into one extension's file | core: a small derive helper. Adapters: the extension's author (a pack in their repo) or the user's local pack. yolo ships none | inherits (most do), or keeps its own config | **Take it**, as the main layer |
| **(b) A published env convention** (`YOLO_MODEL_FAST`, …) that extensions may read | core: the vars. Extensions: opt in | unaffected | **Defer.** A jail-wide var is wrong when two agents run different profiles, until per-agent env exists ([OQ-CN6](../design/provider-credential-scope.md#OQ-CN6)) |
| **(c) Upstream: a model-roles setting in pi** (and `ctx.modelFor(role)`) | pi. Then yolo's pi derive writes one key | follows once it adopts the setting | **Propose it**; the long-run fix, not ours to ship |
| **(d) Inherit-first only.** yolo writes the default and scope; extensions that inherit follow | nothing new | follows if it inherits; diverges if configured | **Keep as the floor**, already true; not enough alone |
| **(e) Document and stop** | docs | diverges | **No.** The maintainer wants something layered |
| **(f) yolo ships adapters for chosen extensions** | yolo, per extension | — | **No.** It's the opinion and the responsibility the maintainer declined |

**Leaning: (d) as the floor, (a) as the layer, (c) proposed upstream, (b) later.** The pieces:

1. **Core, generic, small:** a derive helper that resolves a tier alias for the selected provider
   to a full `provider/model` id, the same resolution claude's derive does by hand. With it, an
   adapter is about ten lines of Lua. Core names no extension.
2. **yolo ships no adapters.** It documents the adapter contract and one worked example. The
   example lives in docs, not in `packs/`.
3. **The `subagents` block moves out of pi's derive** into an adapter pack that yolo does not
   ship by default ([OQ-XM3](#OQ-XM3)). pi's derive then names no extension, and [OQ-PM1](pi-model-selection-ux.md#OQ-PM1)'s policy
   question becomes that adapter's business.
4. **Adapters compose with the default-models pack and `only`.** An adapter reads the same resolved
   aliases the pickers render, so a company pack's `only` narrows what an adapter can write too.
   Nothing reads a second list.

**What this does not fix.** An extension whose user edits its model config by hand in the jail
keeps that edit: an adapter's surface follows the same capture rules as any composed file, so an
in-jail edit is the user's. That's the intended behavior, not a gap.

## 6. Open questions

1. 💬 <a id="OQ-XM1"></a>**[OQ-XM1](#OQ-XM1): Does core expose tier resolution to pack derives?**
   A helper, for example `yolo.model_for("fast")`, returning the selected provider's aliased
   model as `provider/id`, or nil. Stakes: without it every adapter re-implements the alias
   fallback rules claude's derive encodes, and they drift.

   _Leaning:_ yes. It's the one core change, it names no extension, and claude's and opencode's
   derives can adopt it to lose their hand-written copies.

   <!-- vantage: oq id=OQ-XM1 leaning="Yes: a derive helper resolving a tier alias for the selected provider to provider/id. The one core change; names no extension; claude's and opencode's derives can adopt it." -->

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 <a id="OQ-XM2"></a>**[OQ-XM2](#OQ-XM2): Which role names does yolo publish?** yolo's
   aliases are `default`, `fast` and `balanced`. The ecosystem uses `small`/`medium`/`big`
   (pi-dynamic-workflows) and `fast`/`balanced`/`frontier`/`fav` (pi-task-models), and claude
   uses `opus`/`sonnet`/`haiku`. Stakes: an adapter maps yolo's names onto the extension's, and a
   missing `frontier` leaves every "big" slot on the default.

   _Leaning:_ keep yolo's three and add `frontier` as a fourth conventional alias, with the same
   warn-don't-refuse rule. Each adapter maps names; core does not.

   <!-- vantage: oq id=OQ-XM2 leaning="Keep default, fast, balanced and add frontier as a fourth conventional alias under the same warn-don't-refuse rule; adapters map names, core does not." -->

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 <a id="OQ-XM3"></a>**[OQ-XM3](#OQ-XM3): What happens to the `subagents` block pi's derive
   writes today?** It's yolo's one adapter, embedded in the agent's pack.
   - **(a)** Move it to an optional adapter pack yolo ships but never selects by default.
   - **(b)** Move it to the maintainer's own pack, out of yolo entirely.
   - **(c)** Keep it where it is, as the one sanctioned case.

   Stakes: under (a) or (b), a codex user without the pack loses the strict GPT-6 scope for child
   agents. Their children still inherit the codex model, so only the enforcement goes.

   _Leaning:_ (a), and it answers [OQ-PM1](pi-model-selection-ux.md#OQ-PM1) the same way: the adapter writes `defaultModel` from the
   profile's `default` alias and `modelScope` from the provider's models, for every provider.

   <!-- vantage: oq id=OQ-XM3 leaning="(a): move it to an optional adapter pack yolo ships but never selects by default; the adapter writes defaultModel from the profile's default alias and modelScope from the provider's models, for every provider, which also answers OQ-PM1." -->

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 <a id="OQ-XM4"></a>**[OQ-XM4](#OQ-XM4): Does yolo also publish role env vars?** Stakes: an
   env var reaches extensions with no adapter, but a jail-wide one is wrong when claude and pi run
   different profiles.

   _Leaning:_ not until per-agent env exists ([OQ-CN6](../design/provider-credential-scope.md#OQ-CN6));
   then as `YOLO_MODEL_<ROLE>` per agent.

   <!-- vantage: oq id=OQ-XM4 leaning="Not until per-agent env exists (OQ-CN6); then per-agent YOLO_MODEL_<ROLE>." -->

   **Answer:**
   > _(empty — fill in when decided)_

5. 💬 <a id="OQ-XM5"></a>**[OQ-XM5](#OQ-XM5): Propose a model-roles setting to pi upstream?**
   A `modelRoles` map in `settings.json` and `ctx.modelFor(role)` on the extension context, so
   extensions stop inventing vocabularies. Stakes: none for yolo's build; it's the maintainer's
   call whether to spend upstream capital.

   _Leaning:_ yes. It would let yolo's pi derive write one key and most adapters retire, and
   `@henryqw/pi-task-models` shows the demand.

   <!-- vantage: oq id=OQ-XM5 leaning="Yes: a modelRoles settings map plus ctx.modelFor(role); yolo's pi derive then writes one key and most adapters retire; pi-task-models shows the demand." -->

   **Answer:**
   > _(empty — fill in when decided)_

## Appendix A: evidence

Read 2026-09-25. npm packages from their published tarballs; git extensions from the checkouts in
this jail's `~/.pi/agent/git` (read-only).

- **pi-subagents 0.35.1** (installed): `src/runs/shared/model-fallback.ts:214-237`
  (`resolveEffectiveSubagentModel`: explicit, else agent model, else the parent model);
  `src/agents/agents.ts:748-779` (`subagents.defaultModel`, `modelScope`, `agentOverrides`);
  `agents/*.md` set `thinking` only, no `model`.
- **pi-subagents 0.71.0** (npm latest): `agents/*.md` pin no `model`.
- **pi-archimedes** (`c7878f0`): `packages/subagent/src/spawn.ts:207-208` ("agent.model >
  options.model > options.activeModel"); `local-config.ts:19` (`agents.local.json`).
- **@quintinshaw/pi-dynamic-workflows 3.13.0** (`bd9245d`): `src/agent.ts:212-236`
  (`resolveAgentModelSpec`); `src/model-tier-config.ts:66` (`~/.pi/workflows/model-tiers.json`),
  `:185-209` (defaults ranked from the registry).
- **@arhen/pi-core-subagent 1.3.56**: `src/manager.ts:140` (`resolveChildModel` returns `ctx.model`).
- **@bacnh85/pi-subagent 0.22.6**: `extensions/model.ts:9,68` (falls back to the authenticated
  parent model).
- **pi-prompt-template-model 0.12.3**: `subagent-step.ts:264` (the inherited model).
- **@henryqw/pi-subagent 22.2.0**: `README.md:173` (`modelClass`), `CONTEXT.md:67` (route precedence).
- **@henryqw/pi-task-models 7.0.2**: `README.md` Config section
  (`~/.pi/agent/config/pi-task-models/config.json`), `:119,127` (a missing file is `"missing"`).
- **pi 0.87.1** (`@earendil-works/pi-coding-agent`): `dist/core/extensions/types.d.ts`
  (`ExtensionContext.model`, `.scopedModels`, `.modelRegistry`); `docs/settings.md:12-16`.
- **Claude Code docs** (`docs.claude.com/en/docs/claude-code/sub-agents`, fetched 2026-09-25):
  built-in subagents inherit the main conversation's model unless `CLAUDE_CODE_SUBAGENT_MODEL` is set.
- **opencode docs** (`opencode.ai/docs/agents`, fetched 2026-09-25): a subagent with no model
  uses the model of the primary agent that invoked it.
- **yolo:** `packs/claude/derive.lua:162-169,296-306`; `packs/opencode/derive.lua:178-187`;
  `packs/pi/derive.lua:492-509` (the `subagents` block, added in `d3360c00`).
