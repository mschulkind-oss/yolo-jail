---
title: "Build plan: agent-scoped exact model menus"
status: accepted
stage: DECIDED
next: "Review the committed candidate; parent owns the combined landing gate"
depends-on:
  - claude-exact-model-menu.md
  - claude-exact-model-menu-grounding.md
tags: [providers, models, packs, claude]
summary: "Tree-grounded implementation handoff for the accepted agent-scoped models-only design."
---

# Plan: agent-scoped exact model menus

**Status:** 2026-11-16. Implementation and focused verification completed against `bec6e62042b5e65e762ca40171444c701f8bc341`; candidate commit and reviewer/parent gates are recorded in the final receipt. The historical Pi wildcard/`keep_catalog` fixture expectation was corrected to current-source behavior in the design's [§7](./claude-exact-model-menu.md#7-acceptance-tests) and grounding evidence.
**Design:** [`claude-exact-model-menu.md`](claude-exact-model-menu.md). **Source grounding:** [`claude-exact-model-menu-grounding.md`](claude-exact-model-menu-grounding.md).

**Precedence:** the design controls behavior; the tree controls facts; this plan is advice. Do not twist code to match a stale map. Stop and report if a required regression does not go red, a named symbol is gone, or implementation would cross this file map.

## Map

| Path | Change |
| :--- | :--- |
| `internal/packdecl/contributes.go` | accept `agents` only on an agent-scoped `models.only`; keep singular `agent` refused; validate scoped audience. |
| `internal/packdecl/models.go` | retain audience on `ModelsContribution`; refuse empty/repeated audiences and `agents` with `add`. |
| `internal/packload/modellists.go` | collect scoped-only facts without changing shared rows/order/`models_only`; record view after user merge; retain existing unscoped path. |
| `internal/packload/providers.go`, `providerinherits.go` | integrate post-user scoped view composition with parent/child composition and strip it from typed declarations. |
| `internal/modelview/` (new) | pure projection of generic/composed provider tables; strips the composer key for every agent and narrows only matching agent. |
| `internal/agentcfg/luahook/derive.go` | apply projection while assembling each Lua context, before any surface or env derive can see the table. |
| `internal/packload/deriveenv.go`, `unnarrowedmenus.go` | project before host role-variable selection and evaluate enforcement-off menu state for the appropriate agent. |
| `internal/packdecl/models_test.go`, `internal/packload/modellists_test.go`, `internal/packload/providerinherits_test.go` | permanent source-named validation, composition, intersection, alias/order and inheritance coverage. |
| `internal/modelview/*_test.go`, `internal/agentcfg/luahook/*_test.go` | projection purity/no-mutation and actual derive-boundary key-stripping checks. |
| `internal/entrypoint/claudeexactmodelmenu_test.go` (new) | real boot surface and host `AgentEnv` callers: exact Claude rows/order and tier pins, enforcement off/on, same-agent scoped/unscoped equivalence, byte-identical Pi/Copilot outputs (ten Pi model rows), and a non-vacuous Copilot file assertion. |
| `internal/wirebridged/allowlist_test.go` | staged-pack `allowlistsFor` regression proving a Claude menu view does not create a bridge allowlist. |
| `docs/design/claude-exact-model-menu.md` | retained Fable design; mark only observed built claims after verification. |
| `docs/design/claude-exact-model-menu-plan.md` | this grounded handoff. |
| `docs/design/claude-exact-model-menu-grounding.md` | concise source-to-design evidence. |

## Reuse

- `packdecl.Contribution.Agents` and the existing bin-namespace validation are the declaration vocabulary; avoid a new agent registry.
- `packload.ModelsOnlyKey`, `WithModelNotes`, `userModelIDs`, `model_options`, and `jsonx.DeepCopy` are the existing model facts and note conventions.
- `ComposeProviderDeclarations` already removes composer-only facts; add the view key to that same boundary.
- `entrypoint.renderClaudeModels` / `ConfigurePackSurfaces` fixtures in `claudemodelpins_test.go`, plus `companyModelsPack` and `testPacksForAgent` in `modelsonlyagents_test.go`, already drive shipped derives through real rendering paths.
- `luahook.buildDeriveCtxTable` is the shared derivation context assembly point; put the projection there, not in `packs/claude/derive.lua` or individual agent derives.

## Traps

- Unscoped `only` still intersects after every `add` and must continue writing `models_only`; scoped `only` must write neither shared rows nor `models_only`.
- `models_views` must be absent from Lua for every agent and from typed provider declarations. Shared route, credential, fetch, and bridge consumers must continue reading the unprojected list.
- A scoped-only provider view is inherited with its parent's finished provider entry. Child-local scope is combined after child user configuration; do not accidentally narrow the parent.
- Preserve alias row order, and remove only matching aliases plus their `model_options`; user-declared IDs survive.
- `models` contribution test helpers that decode JSON must explicitly assert both `agents` and `agent` validation rules; an empty JSON array differs from an absent field.

## Task 1 — declaration, composition, and projection

**Settled (do not re-open):** `agents` may accompany `models.only` only; absent audience is byte-identical to current behavior; scoped views intersect, user IDs survive, shared rows do not change, and projection strips the view fact for every agent. Scoped `only` is not a wire refusal.

**Preconditions:** verify `git rev-parse HEAD` is `bec6e62042b5e65e762ca40171444c701f8bc341`, clean tree, and read each map symbol before editing. If not, stop and report. `go env GOCACHE GOMODCACHE GOENV` was recorded; use shared caches.

**Tests first:**
- `internal/packdecl/models_test.go`: valid `only` with `agents:["claude"]`; refuse `agents:[]`, repeated audience, `agents` with `add`, and singular `agent` on models.
- `internal/packload/modellists_test.go`: scoped only leaves shared models, model_options, provider order and marker unchanged; projection tests prove intersection plus user-ID retention and survivor order; two contributions intersect and scoped/unscoped filters intersect.
- `internal/packload/providerinherits_test.go`: parent scoped audience reaches child, child's own scope intersects, parent remains unmodified.
- `internal/modelview/` and `luahook`: input immutability, no key leakage even when unmatched, only matching agent receives narrowed rows/`models_only`.
- Red command: `go test ./internal/packdecl ./internal/packload ./internal/modelview ./internal/agentcfg/luahook`.

**Change:** extend `ModelsContribution`; allow audience in kind validation with existing bin rules plus duplicate/empty-list rules; compose scoped fact after target's user merge, before descendants inherit, and for child after its user merge; remove fact from typed declarations; add the pure generic projection; invoke it at derive context assembly and project `AgentEnv` before role-variable computation. Keep unscoped behavior untouched.

**Gates:** same focused package command; `go test ./internal/entrypoint`; targeted integration tests found by `rg -n 'models_only|models.*only|provider.*model' integration`; `just format`; Vantage check as available. Host-launch and shared-wire rows must remain unchanged.

**Fences:** do not change `packs/claude/derive.lua`, shared provider services/routes/grants, any user's live config, request scratch, or primary worktree. No live agent, AWS/model calls, install, push, or branch switch.

**Commit:** `feat(models): scope exact menus to agents`.

## Task 2 — production-callsite regression and docs

**Settled (do not re-open):** real callers must prove exact Claude picker order (Fable 5.2, Haiku 5.5, Opus 5.5, Sonnet 5.5), `replaceBuiltInOptions:true`, enforcement-on allowlist/default ordering, and enforcement-off replacement without either allowlist field. Pi settings/models/surface and Copilot are byte-identical without/with the scoped declaration. A same-agent scoped render equals equivalent unscoped `only`; shared/bridge list remains unprojected; absent/null provider is silent as designed; unscoped add retains built-in menu.

**Tests first:** source-named `internal/entrypoint/claudeexactmodelmenu_test.go`, built from helpers in `claudemodelpins_test.go` and `modelsonlyagents_test.go`, drives `ConfigurePackSurfaces` and `packload.AgentEnv`. Red-phase receipts are retained and linked from [`claude-exact-model-menu-grounding.md`](./claude-exact-model-menu-grounding.md). `internal/wirebridged/allowlist_test.go` additionally checks the production `allowlistsFor` path with a staged pack tree, without starting a service or making a request.

**Change:** add the production-caller regression; update [`model-lists-and-pickers.md`](./model-lists-and-pickers.md) only where its existing rule now needs to state scoped audience; update the Fable design's Built column/status only for observed checks. Preserve the decision and unbuilt evidence honestly; do not create a system doc before landing/acceptance.

**Gates:** focused changed-package tests, the production-caller entrypoint selector, and the wire-bridge allowlist selector; relevant integration tests are identified by the source search in the implementation receipt. `just format`; docs checker on changed docs. Run completion-census if tests read outside its package. Parent owns the combined whole-tree landing gate.

**Commit:** same coherent feature commit after Task 1. Include red/green commands, `git diff --stat`, clean status and residual acceptance gaps in report.

## Ships with / do not

- No config key, migration, shipped manifest, changelog, generated file, live permission or wire schema changes.
- No edits to pack derives; no provider-per-agent table composition; no CLI additions; no agent-scoped `add`.
- Full landing gate, mandatory nested-jail launch verification and final combined integration run remain parent serial gates; do not duplicate the slow full suite here.
