---
title: "Pi model-selection UX and provider scoping"
date: 2026-09-15
status: accepted
tags: [pi, models, providers, extensions, research]
summary: "What Pi already supports for provider-labelled model selection, what extensions can replace, and the smallest yolo integration that avoids mixed-provider ambiguity."
vantage:
  status-chip: true
---

# Pi model-selection UX and provider scoping

**Status:** source-verified 2026-09-15 against Pi 0.85.1 and upstream commit
`f9bcd351dc3cedf989bc5fc0f8aa012db5737df2`; [§2](#2-subagent-defaults-do-not-follow-non-codex-profiles)
source-verified 2026-09-23; checked against the tree 2026-09-24. Third-party examples were
checked only to establish prior art, not endorsed as dependencies.

**What shipped.** [§4](#4-recommendation)'s native-scope recommendation is **built**, with one
deliberate divergence: pi's settings derive writes an explicit list of three Codex models for
`openai-codex` rather than the `openai-codex/*` wildcard, because the wildcard would also make
retired models selectable (the derive's own comment). Every other reachable provider gets its
declared model ids, or `<provider>/*` when it declares none.
[`provider-credential-scope.md`](../design/provider-credential-scope.md#241-what-pis-enabledmodels-actually-constrains)
re-measured the scope at pi 0.87.1 and found it a soft shortlist, as [§1](#1-what-pi-0851-already-does) says: Tab and `--model`
both escape it. A hard allowlist can only live at the wire bridge, which is
[`wire-bridge-gateway.md`](../design/wire-bridge-gateway.md#OQ-WG3)'s question.

**Needs your ruling:** [OQ-PM1](#OQ-PM1).

> [!IMPORTANT]
> For an active yolo provider profile, use Pi's native model scope. A Codex
> profile should set `enabledModels` to its Codex models (proposed here as
> `openai-codex/*`; shipped as an explicit list); Pi then opens `/model`
> on Codex models while retaining an explicit Tab escape to the full catalog.
> A custom yolo model picker would duplicate a moving Pi interface and cannot
> replace Pi's built-in `/model` or its existing shortcut through the public
> extension API.

This document answers the model-picker questions adjacent to
[`openai-subscription-auth.md`](openai-subscription-auth.md). It does not
change how credentials are acquired or refreshed.

## 1. What Pi 0.85.1 already does

Pi already distinguishes providers in its built-in selector. Each visible row
is rendered as `<model id> [<provider id>]`, the detail line shows the model's
friendly `name`, and search includes the provider, model id,
`provider/model-id`, and friendly name. The picker sorts by provider after the
current and saved-default models. These facts are explicit in the
[upstream selector implementation](https://github.com/earendil-works/pi/blob/f9bcd351dc3cedf989bc5fc0f8aa012db5737df2/packages/coding-agent/src/modes/interactive/components/model-selector.ts#L312-L352)
and its
[provider-first search text](https://github.com/earendil-works/pi/blob/f9bcd351dc3cedf989bc5fc0f8aa012db5737df2/packages/coding-agent/src/modes/interactive/model-search.ts#L1-L21).

The provider suffix can still be easy to miss on a wide terminal because the
eye reaches the shared model-family names first. Renaming a model does not fix
that row: `models.json` supports a `modelOverrides.<id>.name` value, but the
selector uses the id in each row and reserves the name for the detail line.
This is documented in Pi's
[`modelOverrides` reference](https://github.com/earendil-works/pi/blob/f9bcd351dc3cedf989bc5fc0f8aa012db5737df2/packages/coding-agent/docs/models.md#L341-L385)
and visible at the selector link above.

### Native scoping is the intended control

Pi calls a restricted set of models a **model scope**: the models available for
cycling and initially shown by `/model`. This term comes from Pi's own
`scopedModels` API. A scope is resolved from the `--models` command-line flag or
the `enabledModels` setting using minimatch patterns against both the bare model
id and the canonical `provider/model-id` value. Pi's
[resolver source](https://github.com/earendil-works/pi/blob/f9bcd351dc3cedf989bc5fc0f8aa012db5737df2/packages/coding-agent/src/core/model-resolver.ts#L286-L345)
is the normative behavior; its
[extension documentation](https://github.com/earendil-works/pi/blob/f9bcd351dc3cedf989bc5fc0f8aa012db5737df2/packages/coding-agent/docs/extensions.md#L1013-L1019)
defines the public view.

When a scope is nonempty, `/model` starts in the scoped view. Tab switches
between `scoped` and `all`, so restricting the default view does not make other
configured providers unreachable. The behavior is in the
[selector's scope handling](https://github.com/earendil-works/pi/blob/f9bcd351dc3cedf989bc5fc0f8aa012db5737df2/packages/coding-agent/src/modes/interactive/components/model-selector.ts#L80-L112)
and
[Tab handler](https://github.com/earendil-works/pi/blob/f9bcd351dc3cedf989bc5fc0f8aa012db5737df2/packages/coding-agent/src/modes/interactive/components/model-selector.ts#L358-L375).

For the yolo Codex profile, the native configuration is therefore:

```json
{
  "defaultProvider": "openai-codex",
  "defaultModel": "<an exact Pi Codex model id>",
  "enabledModels": ["openai-codex/*"]
}
```

The warning `No models match pattern "openai-codex/*"` means the authenticated
Codex catalog was absent when Pi resolved the scope. It does not mean Pi lacks
provider scoping. In the observed yolo launch, other configured providers then
remained available and Pi fell back to one of them. The fix belongs in the
authentication-before-model-resolution path; changing the selector would only
hide the cause.

## 2. Subagent defaults do not follow non-Codex profiles

**Source-verified 2026-09-23.** The shipped Pi settings derive (`yolo.derive("pi", "settings", …)` in [`packs/pi/derive.lua`](../../packs/pi/derive.lua)) returns `subagents` only when `selected_provider == "openai-codex"`. That branch emits a qualified `openai-codex/gpt-6-sol` default (or the selected profile model), plus an enforced, strict `openai-codex/gpt-6-*` scope. For another reachable provider the derive returns `enabledModels` and `selection`, but **no `subagents`**. With no provider selected or an unreachable provider it returns an empty result. The existing codex profile test (`TestPiCodexProfileSelectsBuiltInProviderAndExplicitModel`, [`pi_codex_profile_test.go`](../../internal/entrypoint/pi_codex_profile_test.go)) checks the Codex branch; it does not check the non-Codex omission.

The installed `pi-subagents` documentation (`docs/models.md`, checked in this jail on 2026-09-23) says its precedence is per-run override → role override → agent frontmatter → `subagents.defaultModel` → **parent session model**. Therefore the absence of `subagents` is **not** an independent fixed package default: ordinary builtin agents inherit the parent model, while an agent declaring its own model can still use that. With no `modelScope`, no strict allow-list prevents a per-run or agent model outside the active provider. Scope is a rejection policy, not a model selector; it does not itself pin a default. The installed extension also says a project-level `modelScope` replaces the user-level one, so a generated user setting is not an absolute policy boundary.

This is a reproducible code-path gap, but **the claimed OpenRouter launch was not independently inspected here**. The current jail's `~/.pi/agent/settings.json`, inspected on 2026-09-23, instead has `defaultProvider: zai`, `defaultModel: glm-5` and an explicit Codex `subagents` policy; those values can reflect the host settings layer or an earlier write. A missing computed key does not by itself prove the final rendered file lacks that key: host, capture, workspace and overlays can preserve it. Verify a specific launch with `yolo config render --explain pi/settings` or its rendered file and provenance, rather than inferring it solely from the derive branch.

**Verdict:** fix the non-Codex policy gap only after [OQ-PM1](#OQ-PM1) decides whether child agents should be pinned to the profile's exact model, to its provider's configured model set, or to a separate explicit budget/policy set. Reusing the Codex `gpt-6-*` allow-list on OpenRouter would either block valid models or allow the wrong provider. Cover both branches through the production rendering call site, including transition from Codex to a non-Codex profile and a provider without a resolvable model. Do not assert that simply moving the Codex block out of its branch is sufficient.

### Open question

1. 💬 <a id="OQ-PM1"></a>**[OQ-PM1](#OQ-PM1): under a non-Codex profile, which models may pi's child agents use?**
   Three candidates: the profile's exact model, its provider's configured model set, or a
   separate explicit budget/policy set. Today pi's settings derive emits no `subagents` for any
   provider but `openai-codex`, so children inherit the parent model and nothing rejects an
   out-of-provider per-run model. The stakes: reusing the Codex `gpt-6-*` allow-list would
   block valid models on another provider or allow the wrong one, so the build waits on this.

   <!-- vantage: oq id=OQ-PM1 leaning="The provider's configured model set, as a strict scope with the profile's model as the default. It is the set the user already curated for that provider, it matches what enabledModels already holds, and it needs no new config key; a separate budget set is a new surface nobody has asked for yet." -->

   _Leaning:_ **The provider's configured model set**, strict, with the profile's model as the
   default. It is the set the user already curated, it matches what `enabledModels` already
   holds, and it needs no new config key; a separate budget set is a new surface nobody has
   asked for yet.

   **Answer:**
   > _(empty — fill in when decided)_

## 3. Existing extension solutions

Two published extensions demonstrate the main alternate interfaces:

| Project | Interface | Useful precedent | Disposition |
| :--- | :--- | :--- | :--- |
| [`pi-model-picker`](https://github.com/rilham97/pi-model-picker/tree/542891498f51b2cf3a75abdb39adcd303e9e6850) | `/models` and Ctrl+Shift+M; horizontal provider tabs, then search within one provider | Grouping makes the provider impossible to miss; it uses Pi's model registry and `pi.setModel()` | **Do not bundle.** It adds a parallel selector and tracks private-looking TUI composition closely. Useful evidence for an upstream grouped view. |
| [`@0xkahi/pi-model-select`](https://github.com/0xKahi/pi-model-select/tree/4212585c523b1bd88f374a87f27e79bb79b4083c) | `/select-model`; favorites plus an explicit `provider_filter` | Shows that a provider allowlist and full `provider/model-id` labels fit the public extension APIs | **Do not bundle.** Pi's native scope already represents the profile-specific allowlist without another config file. |
| [`pi-model-switch`](https://github.com/nicobailon/pi-model-switch/tree/c8605ea077e972e77166c3a0e6e5389e091741a9) | Agent tool for list, search, aliases, and switching | Confirms aliases are useful for agent-directed routing | **Out of scope.** It solves agent-directed switching, not human recognition in `/model`. |

The broader extension pattern is supported: extensions can read
`ctx.modelRegistry`, use `ctx.scopedModels`, render a custom component with
`ctx.ui.custom()`, and call `pi.setModel()`. Pi's own
[extension API reference](https://github.com/earendil-works/pi/blob/f9bcd351dc3cedf989bc5fc0f8aa012db5737df2/packages/coding-agent/docs/extensions.md#L1009-L1019)
and
[`setModel` documentation](https://github.com/earendil-works/pi/blob/f9bcd351dc3cedf989bc5fc0f8aa012db5737df2/packages/coding-agent/docs/extensions.md#L1704-L1721)
make this a supported customization route.

It is not a transparent replacement route. A command named `/model` conflicts
with the built-in interactive command and is removed from ordinary autocomplete
or assigned another invocation name; the
[conflict diagnostic](https://github.com/earendil-works/pi/blob/f9bcd351dc3cedf989bc5fc0f8aa012db5737df2/packages/coding-agent/src/modes/interactive/interactive-mode.ts#L620-L631)
states that behavior. Likewise, the extension runner rejects a shortcut that
conflicts with a built-in binding; see the
[shortcut conflict handling](https://github.com/earendil-works/pi/blob/f9bcd351dc3cedf989bc5fc0f8aa012db5737df2/packages/coding-agent/src/core/extensions/runner.ts#L645-L692).
An extension must therefore introduce another command and another key, exactly
as the two picker packages do.

## 4. Recommendation

### Adopt now: native profile scope

The yolo profile should set all three native values together:

1. `defaultProvider` chooses `openai-codex`.
2. `defaultModel` chooses a model that actually exists in the authenticated Pi
   Codex catalog.
3. `enabledModels: ["openai-codex/*"]` opens model selection in that provider's
   catalog and scopes cycling to it.

This keeps other packs usable by other agents in the same jail. Removing
Cerebras or Z.ai from the pack closure merely to clean Pi's picker would change
the jail's capabilities and is unnecessary.

### Upstream if needed: provider-first built-in rows

If the suffix remains confusing in unscoped sessions, propose a small upstream
Pi change from:

```text
gpt-oss-120b [cerebras]
```

to one of:

```text
[cerebras] gpt-oss-120b
cerebras/gpt-oss-120b
```

This is a localized change in the built-in selector's row renderer and aligns
the visible row with Pi's canonical `provider/model-id` references. Prefer this
over provider-name prefixes embedded in every model id or friendly name; those
would contaminate configuration identity to compensate for presentation.

### Reconsider a yolo extension only for a larger interaction

A yolo-shipped picker becomes justified if the desired behavior grows beyond
native scoping: provider tabs, favorites, metadata columns, or a deliberate
replacement command that users opt into. Reuse the public API pattern from the
two extensions above, read `ctx.scopedModels` rather than the entire registry
when a yolo profile is active, and expose a new command rather than pretending
to replace `/model`.

## 5. Sources and re-check points

- [Pi 0.85.1-era model selector source](https://github.com/earendil-works/pi/blob/f9bcd351dc3cedf989bc5fc0f8aa012db5737df2/packages/coding-agent/src/modes/interactive/components/model-selector.ts) — row format, provider sorting, model scope, and Tab behavior.
- [Pi model resolver source](https://github.com/earendil-works/pi/blob/f9bcd351dc3cedf989bc5fc0f8aa012db5737df2/packages/coding-agent/src/core/model-resolver.ts) — exact matching and glob resolution against provider-qualified ids.
- [Pi extension documentation](https://github.com/earendil-works/pi/blob/f9bcd351dc3cedf989bc5fc0f8aa012db5737df2/packages/coding-agent/docs/extensions.md) — supported model registry, scope, custom UI, command, shortcut, and model-setting APIs.
- [`pi-model-picker` source](https://github.com/rilham97/pi-model-picker/tree/542891498f51b2cf3a75abdb39adcd303e9e6850) — provider-tab picker and the separate-command precedent.
- [`pi-model-select` source](https://github.com/0xKahi/pi-model-select/tree/4212585c523b1bd88f374a87f27e79bb79b4083c) — provider filtering and favorites through a custom picker.
- [`pi-model-switch` source](https://github.com/nicobailon/pi-model-switch/tree/c8605ea077e972e77166c3a0e6e5389e091741a9) — alias and provider-filter prior art for agent-directed selection.

## Fast-moving — verify before building

Pi's selector and extension APIs are moving quickly. Re-check the command and
shortcut collision behavior, whether `enabledModels` still opens `/model` on a
scoped view, and the current asynchronous contract of
`modelRegistry.refresh()` before copying code from any third-party extension.
