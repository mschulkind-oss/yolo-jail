---
title: Agent-scoped exact model menus
status: accepted
stage: CURRENT
verified: 2026-11-16
verified_commit: fd3ecba47
covers:
  - internal/packdecl/models.go
  - internal/packdecl/contributes.go
  - internal/packload/modellists.go
  - internal/packload/providers.go
  - internal/packload/providerinherits.go
  - internal/packload/deriveenv.go
  - internal/packload/emptymodellists.go
  - internal/packload/unnarrowedmenus.go
  - internal/modelview/
  - internal/agentcfg/luahook/derive.go
  - internal/config/providersemantic_consumer.go
summary: A models-only audience narrows client menus without narrowing shared provider routes or authority.
---

# Agent-scoped exact model menus

**Status:** Verified 2026-11-16 against `fd3ecba47`. MEASURED: accepted candidate
production-callsite tests exercised native/bridged settings, host environment and
selection, config editing and shared bridge isolation. No live model/AWS call is implied.

Yolo renders the workspace's coding environment per agent. A pack's `models.only`
contribution can name an `agents` audience so one agent gets an exact menu while other
agents continue seeing the full shared provider list. This is presentation and client
enforcement, not additional confinement or a shared service policy.
The larger [model lists and pickers design](../design/model-lists-and-pickers.md) still
owns supplied lists, two-pass composition and its remaining work.

## Declaration

```toml
[[models]]
provider = "bedrock"
only = ["example.model-a", "example.model-b"]
agents = ["claude"]
```

An audience is a nonempty list of opaque program/bin names, matched to the derive's
agent name. Core has no registry of agents. An audience not selected by any program
is inert; it does not cause selection or create a provider.

- Without `agents`, existing provider-wide `only` behavior remains unchanged.
- Empty/repeated audiences, singular `agent`, and `agents` together with `add` are refused.
- `yolo pack lint` uses the same declaration validation as pack loading.
- An add-only list remains additive beside Claude's built-in menu.

The IDs above are examples. Production logic names no agent, provider or model ID.

## Shared composition and agent views

An **agent view** is the set of IDs retained for one provider and one agent's derive;
it is not a route's served model list. The composer records it only on a provider
that exists, after that provider's user-layer merge and before descendants inherit
its finished entry. A child's local views merge after the child's own user layer.

For each audience, applicable scoped `only` ID sets intersect. User-declared IDs join
the keep-set under the existing user-last rule, resolved by the readable-row rule
[below](#readable-row-identity). Existing unscoped additions/filters keep their established
two-pass behavior. Surviving aliases retain shared order and facts; `only` argument
order does not reorder models.

Scoped contributions write only `models_views`, a composer-owned fact. They do not
set shared `models_only`, delete rows/options, materialize order facts, create a
provider or change provider order. Removing the view fact leaves shared data equal
to the no-scoped-contribution composition for supported row forms.

The user schema cannot author this fact. Typed provider decoding strips it; the
legacy semantic config consumer tolerates this fact alone, not arbitrary unknown
provider keys or the deliberately refused unscoped marker. Config editor callers
therefore accept scoped views without weakening unrelated validation.

## Projection boundary

`ProjectProviderTable` in `internal/modelview` is pure: it takes composed providers
and an agent name and does not mutate inputs. It strips `models_views` from **every**
provider, whether or not that agent has a view. On matching entries it removes aliases
outside the keep-set and their `model_options`, then sets ordinary `models_only`.
An unmatched agent sees otherwise unchanged providers.

The Lua session creates a shallow per-agent context/table-map copy and projects before
Lua tables **or Go-side helpers** close over it. `yolo.model_for`, platform/capability
helpers and Lua therefore agree about retained rows. `AgentEnv` projects before
host role-variable calculation too. No individual agent derive learns a new shape;
Claude's shipped Lua is unchanged.

Surfaces, host environment, host launch selection and menu derives consume this
boundary. Per-program empty-list and unenforced-menu diagnostics use that program's
projection. Their route reach and supplied-list presence remain shared.

## Client behavior and shared boundaries

Claude sees its existing exact-list shape:

| Consumer case | Result |
| :--- | :--- |
| Native or bridged Claude, enforcement on | Exact picker, client available-model list/enforcement and tier pins |
| Native or bridged Claude, enforcement off | Exact picker and tier pins, no available-model/enforcement fields |
| Other audience-matched program | Its existing narrowed-list behavior |
| Unmatched Pi/Copilot | Same supplied-list artifacts/environment as without the scoped contribution |

For a supplied Bedrock list, the current Pi path emits explicit enabled IDs and keeps
its native catalog/non-enforcement behavior. The no-list path can emit a wildcard;
that historical wildcard is not imposed on the supplied-list fixture.

Shared routes, services, credential selection, fetched-list decisions, presence and
wire-bridge allowlists read the original table. A scoped menu cannot narrow any of
them. With enforcement disabled there is no client refusal policy; with it enabled,
the refusal remains the selected client's existing policy, not wire enforcement.
A company requiring a shared wire allowlist uses provider-wide policy, not an audience.

## Diagnostics

`WithModelNotes` carries composition notes to inspection/check callers, not derives.
Providers are processed in stable sorted name order. An absent provider gets the
existing shapes-nothing note; a user-null provider stays silent. Unlisted filter IDs
are reported with their audience. An empty projected list gets a provider/agent note,
based on actual listed/filter/user intersection rather than filter argument length.
A user-last listed row prevents a false empty-list note.

No persistent state, record migration, new config key, RPC, credential, grant or route
is introduced. A manifest without audience keeps its prior behavior. This does not
change settings-overlay precedence or permit scoped additions.

## Readable-row identity

A readable row's listed identity is its `model` when set, else its key
(`readableModelID`). Both the unscoped `only` in `shapeProviderModels` and the scoped
views in `applyAgentModelViews` read identity through that rule, so a third-party
pack-readable row is kept by its wire ID, and a partial user override of such a row
(for example only `name`) keeps the merged row's wire ID. Regressions:
`TestScopedPartialUserOverrideKeepsMergedPackWireID` and
`TestUnscopedOnlyKeepsReadablePackRowWithoutFalseNotes` in
`internal/packload/scopedmodelrows_test.go`.

## Where the code lives

| Responsibility | Anchor |
| :--- | :--- |
| Audience validation | `ModelsContribution` in `internal/packdecl` |
| Post-user facts and row identity | `applyAgentModelViews`, `declaredModelIDs` in `internal/packload` |
| Pure projection | `ProjectProviderTable` in `internal/modelview` |
| Lua and Go helper agreement | `newDeriveSession` in `internal/agentcfg/luahook` |
| Host environment and role pins | `AgentEnv` in `internal/packload` |
| Agent-local diagnostics | `EmptyModelLists`, `UnnarrowedMenus` in `internal/packload` |

## Why it's this way

| ID | Constraint |
| :--- | :--- |
| SM-D1 | Exact menus are opt-in; additive defaults remain. |
| SM-D2 | Audience belongs on the existing only verb, not a Claude-specific option or overlay. |
| SM-D3 | Audience may narrow only, never add; invalid audiences/singular agent are refused. |
| SM-D4 | Scoped composition writes one private fact and leaves shared model rows alone. |
| SM-D5 | Projection is at the derive boundary, not inside Lua packs. |
| SM-D6 | Applicable filters intersect and preserve supported user-last rows/order. |
| SM-D7 | Shared authority/routes remain unprojected; client enforcement still uses enforce_models. |
