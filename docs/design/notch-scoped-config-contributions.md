---
title: "Notch-Scoped Config Contributions — Adding Host-Only Packages and Overlays Without In-Jail Leakage"
date: 2026-09-27
status: in-review
tags: [packs, notch, host, config-list, config-overlay, pi, permissions, autonomy]
summary: "Enable non-owning packs to scope config-list and config-overlay contributions to specific confinement notches (such as host-only Pi permission gates), and sanitize readsHost layers so host-applied entries do not leak into containerized jails."
vantage:
  status-chip: true
---

# Notch-Scoped Config Contributions — Adding Host-Only Packages and Overlays Without In-Jail Leakage

**Status:** DESIGN, 2026-09-27. Evidence verified at `d6f875df`. Nothing built.

> **In short.** Cross-pack contributions (`config-list` and `config-overlay`) are
> currently notch-blind, while the `autonomy` kind is exclusive to surface owners
> and restricted to whole-object merge patches. Adding a `notches` selector to
> `config-list` and `config-overlay` lets non-owning packs declare host-only or
> jail-only contributions, while an in-jail list-path sanitization step prevents
> host-applied entries from leaking into containerized environments through
> `readsHost` mounts.

**Why it matters.** A developer configuring Pi needs `@czottmann/pi-automode`
active when running on the unconfined host (`yolo host` or direct IDE launches)
to gate tool actions and prevent unintended modifications to their workstation.
Inside a yolo jail, the agent is already sandboxed in a container or LSM
boundary, making the gate redundant, noisy, and expensive in model tokens.
Because `config-list` currently applies unconditionally across notches and
`autonomy` cannot append to non-owned arrays, a pack cannot configure Auto Mode
for the host without polluting the jail. Furthermore, because `pi/settings`
declares `readsHost: true`, any entry written to the host file by `yolo host apply`
is mounted into the jail as the `host` layer and leaks into the jail's settings
unless explicitly filtered.

**The shape.** A two-part extension: (1) a `notches` modifier on `config-list` and
`config-overlay` declarations evaluated during `packoverlay.Collect`, and (2) a
host-layer list-path filter in `prism.go` that strips declared non-jail list
entries from `hostBytes` before composing `readsHost` surfaces inside a jail.

**Cost.** Adds one optional selector to `config-list` and `config-overlay` schemas;
requires `packoverlay.Collect` to check the target notch; adds list-path entry
filtering to the `readsHost` ingestion pipeline.

**Start at [§3](#3-the-two-fold-breakdown-blind-collection-and-readshost-leakage)** —
how blind collection and `readsHost` reflection create the gap. The rest falls out of it.

**Needs your ruling:** [OQ-1](#OQ-1), [OQ-2](#OQ-2), [OQ-3](#OQ-3), [OQ-4](#OQ-4).

**Reads with:** [`notch-scoped-config-contributions-plan.md`](notch-scoped-config-contributions-plan.md)
(the companion implementation sketch — incomplete while questions are open),
[`../reference/pack-system.md`](../reference/pack-system.md#adding-entries-to-an-array-config-list)
(the `config-list` specification and capture mechanics),
[`host-render-target.md`](host-render-target.md)
(how yolo renders surfaces at the host notch).

---

## Terms, in plain words

- **Notch.** Yolo's three confinement presets: `jail` (namespaces or VM container),
  `guest` (macOS Seatbelt or Linux Landlock sandbox), and `host` (unconfined host execution).
- **Postures (`autonomous` vs `guarded`).** The policy state of agent autonomy.
  `autonomous` (permission prompts bypassed) is the preset default for `jail` and `guest`.
  `guarded` (prompts enforced) is the preset default for `host`.
- **List Contribution (`config-list`).** A pack contribution that appends entries
  to a target array at an RFC 6901 pointer on a config surface owned by any selected pack.
- **Surface Owner vs Contributor.** The pack declaring `kind: "config"` owns the
  surface identity (`agent/name`), its target path, codec, and lifecycle mode. Any
  other pack contributes keys via `config-overlay` or array entries via `config-list`.
- **Host Layer (`readsHost`).** A surface grant where the user's host copy of a config
  file is mounted read-only into `/ctx/` and folded into the jail's config stack as
  `defaults < host < workspace < config-overlay…`.
- **List-Path Leakage** *(coined here)*. The phenomenon where an entry appended to an
  array on the host (by `yolo host apply`) crosses into a containerized jail through
  a `readsHost` mount and is adopted as base content, defeating notch exclusion unless
  explicitly sanitized before composition.

---

## 1. The Incident and Problem Statement

On 2026-09-27, an attempt to configure `@czottmann/pi-automode@1.17.0` as a default
permission gate for Pi on the host exposed an architectural boundary gap:

1. **Host necessity:** Direct Pi invocations and IDE launches on the host run with
   the user's real privileges and host filesystem access. A permission gate like
   Auto Mode is required to classify tool calls and prompt before destructive actions.
2. **Jail redundancy:** Inside a yolo jail, Pi is already confined in a Linux
   container or macOS Seatbelt sandbox. Loading Auto Mode inside the jail costs extra
   classifier model tokens per tool call and interrupts automated runs with redundant
   prompts.
3. **The contribution block:** Contributing to `pi/settings#/packages` requires
   `kind: "config-list"` because JSON Merge Patch (`config-overlay`) replaces arrays
   wholesale. But `config-list` is unconditional: it applies at both the `host` and
   `jail` notches. A trial adding Auto Mode committed to `yolo-packs/matt` had to be
   immediately reverted because it rendered inside all jails.
4. **The autonomy block:** Yolo's `kind: "autonomy"` contribution has `autonomous` and
   `guarded` postures, but its `config` patches are strictly restricted to surfaces
   **owned by that pack** (`packload.go:191`). A personal or shared pack (like `matt`)
   contributing to Pi cannot use `autonomy` because the builtin `pi` pack owns
   `pi/settings`. Furthermore, `autonomy.Config` is a merge patch of `managed` keys
   and cannot append to an array without replacing it.

A non-owning pack currently has no declarative mechanism to contribute configuration
to the host notch without also applying it to containerized jails.

---

## 2. Load-Bearing Principles

- **P1. Autonomy is not confinement, and confinement is not platform.**
  `confinement` selects the enforcement boundary (`jail`, `guest`, `host`).
  `AgentAutonomy` is a policy bit (`true` for `jail`/`guest`, `false` for `host`).
  Contributions may be conditioned on the confinement notch because of environment
  dependencies (e.g. host-only CLI tools, native keychains) as well as permission postures.
- **P2. Contributors never own surfaces, and owners never anticipate all contributors.**
  An agent pack (like `pi` or `claude`) owns its surface schema. A personal or team
  pack (`matt`, `corp-defaults`) contributes extensions, MCP servers, and gates.
  Contribution channels (`config-overlay`, `config-list`) must offer the scoping levers
  contributors need without requiring changes to the owning pack.
- **P3. Symmetrical contribution channels.**
  `config-overlay` and `config-list` are sibling contribution mechanisms: one sets keys,
  the other appends array entries. Any scoping condition available to one must be
  available to the other.
- **P4. No silent inheritance across boundaries.**
  A host-scoped contribution written to the host filesystem must not reflect into a
  jail through `readsHost` mounts. If a pack declares an entry as host-only, the jail
  must not adopt it from the host file.
- **P5. Backward compatibility and fail-closed validation.**
  Omitting a notch selector means unconditional application across all notches
  (preserving all existing pack behavior). An invalid or misspelled notch name must be a
  fatal validation error, never a silent fallback.

---

## 3. The Two-Fold Breakdown: Blind Collection and `readsHost` Leakage

Achieving host-only array contributions requires resolving two independent failures:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ Problem A: Collection (internal/packoverlay/packoverlay.go)                 │
│ • Collect() iterates all packs' config-list declarations unconditionally.  │
│ • Both `yolo host apply` and jail boot receive the exact same entries.      │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
                                       ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│ Problem B: Reflection Leakage (internal/entrypoint/prism.go)                │
│ • `yolo host apply` writes the entry into host `~/.pi/agent/settings.json`. │
│ • Jail boots with `pi/settings` declaring `readsHost: true`.               │
│ • Jail mounts host file as `hostBytes` at `/ctx/host-pi/settings.json`.    │
│ • `hostBytes` contains the host entry in `packages: [...]`.                 │
│ • Jail folds `defaults < host < workspace < config-overlay < lists…`.       │
│ • Result: Host entry enters the jail via `hostBytes` even if Problem A is   │
│   solved!                                                                   │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 3.1 Problem A: Notch-Blind Collection

In [`internal/packoverlay/packoverlay.go`](../../internal/packoverlay/packoverlay.go),
`Collect` processes `config-list` contributions in Pass 3:

```go
for _, p := range packs {
    for _, cl := range p.Decl.ConfigListContributions() {
        // ...
        list, err := agentcfg.NewListContribution(p.Name, cl.Path, cl.Add)
        set.listsByTarget[key] = append(set.listsByTarget[key], list)
    }
}
```

The function receives `autonomy bool`, but tests pin that `autonomy` is inert for
contributions (`autonomyinert_test.go`). It does not receive or consult the render target's
`render.Kind` (`jail`, `host`, `guest`). Consequently, every list contribution is placed
into every render target regardless of where it is running.

### 3.2 Problem B: List-Path Reflection via `readsHost`

Even if `packoverlay.Collect` skips the host-only contribution when collecting for a jail,
the entry still leaks into the jail if the surface declares `readsHost: true`.

1. Running `yolo host apply` applies the host-scoped contribution, writing
   `"npm:@czottmann/pi-automode@1.17.0"` into `~/.pi/agent/settings.json` on the host.
2. When a jail is launched for the workspace, `internal/cli/run/packhostgrants.go`
   mounts `~/.pi/agent/settings.json` read-only at `/ctx/host-pi/settings.json`.
3. In [`internal/entrypoint/prism.go`](../../internal/entrypoint/prism.go),
   `renderSurfaceStateful` reads that file as `hostBytes`.
4. In [`internal/agentcfg/compose.go`](../../internal/agentcfg/compose.go), the layer
   stack folds in order:
   ```
   defaults < host < workspace < config-overlay < lists < capture < list-capture < computed < managed
   ```
5. Because `hostBytes` already contains `"npm:@czottmann/pi-automode@1.17.0"`, the base
   array at `/packages` holds the entry **before any in-jail list contributions are folded**.
6. Because list contributions can only append (`add`), an absent jail list contribution
   does not remove the entry.
7. The jail's rendered `settings.json` ends up with Auto Mode installed, completely
   defeating the host-only constraint.

Any solution that only filters during `packoverlay.Collect` will fail acceptance tests
against real `readsHost` surfaces like `pi/settings`.

---

## 4. Proposed Solution

### 4.1 Manifest Syntax: The `notches` Modifier

Extend `Contribution` in [`internal/packdecl/contributes.go`](../../internal/packdecl/contributes.go)
to support an optional `notches` field on `config-list` and `config-overlay`:

```json
{
  "kind": "config-list",
  "surface": "pi/settings",
  "path": "/packages",
  "notches": ["host"],
  "add": [
    "npm:@czottmann/pi-automode@1.17.0"
  ]
}
```

For authoring ergonomics, allow either a single string (`"notch": "host"`) or an array
of strings (`"notches": ["host"]`), normalized at decode time into `[]string`.

- **Allowed values:** `["jail", "host", "guest"]` (the closed set from `render.SelectableNotches`).
- **Default (absent/empty):** Unconditional (applies at all notches).
- **Validation:** Any value outside the allowed set produces a fatal validation error in
  `configListProblems` and `configOverlayProblems`.
- **Refused on other kinds:** Fields `notch` and `notches` are refused on kinds that do
  not support notch filtering (e.g. `program`, which uses `platforms`, or `state`, which
  uses `scope`).

### 4.2 Target-Aware Collection in `packoverlay`

Thread the target notch (`render.Kind`) into `packoverlay.Collect`:

```go
func Collect(packs []*packload.Pack, notch render.Kind, profiles map[string]string) *OverlaySet
```

During Pass 2 (`config-overlay`) and Pass 3 (`config-list`), evaluate the notch gate:

```go
if len(cl.Notches) > 0 && !cl.MatchesNotch(notch.String()) {
    continue
}
```

An inactive notch contribution is cleanly skipped: no error, no orphan notice, and no
provenance entry recorded.

### 4.3 Sanitizing `readsHost` List Paths in `prism.go`

To close Problem B, the in-jail renderer must sanitize `hostBytes` before passing it to
`agentcfg.ComposeStateful` or `agentcfg.ComposeRMW`.

In [`internal/entrypoint/prism.go`](../../internal/entrypoint/prism.go), when `e.renderTarget()`
is a jail target (`KindOf() == KindJail`) and `surface.ReadsHost` is true:

1. Identify all list paths targeted by selected packs.
2. For each list path, collect the set of entries contributed by selected packs that are
   scoped **exclusively to non-jail notches** (e.g. `notches: ["host"]`).
3. If `hostBytes` contains an array at that path, filter out any entry that exactly matches
   a known non-jail pack contribution.
4. Pass the sanitized `hostBytes` to `ComposeStateful`.

```
hostBytes (/ctx/host-pi/settings.json)
  ├── theme: "dark"
  └── packages: ["kilo-provider", "pi-automode"]
         │
         ▼ [Prism List-Path Sanitizer: remove known host-only entries]
sanitizedHostBytes
  ├── theme: "dark"
  └── packages: ["kilo-provider"]
         │
         ▼ [Compose Stateful Layer Stack]
rendered jail ~/.pi/agent/settings.json
  └── packages: ["kilo-provider"]  <-- pi-automode never enters the jail!
```

This ensures that:
- Host Pi gets Auto Mode from `yolo host apply`.
- Jail Pi gets only shared and jail-scoped packages.
- User-added packages in `~/.pi/agent/settings.json` that do not match any host-only pack
  declaration remain intact and are preserved in the jail.
- No host-only entry is captured into `<workspace>/.yolo/prism/pi-settings.list-capture.json`.

---

## 5. Alternatives Considered

| Alternative | Description | Verdict |
| :--- | :--- | :--- |
| **A. `notches: ["host"]` on contributions (Proposed)** | Add an optional notch selector to `config-list` and `config-overlay`. | **Recommended.** Symmetrical, composable, supports both security gates and host-tool packages, follows `platforms` precedent. |
| **B. `posture: "guarded"` selector** | Condition on the `AgentAutonomy` policy bit (`guarded` vs `autonomous`). | **Rejected as sole selector.** While Auto Mode is a permission gate, other host-only packages (e.g. 1Password integrations, host macOS notification extensions) need host scoping due to environment capabilities, not permission prompt policies. |
| **C. Expand `kind: "autonomy"` to accept `config-list`** | Allow non-owning packs to declare `autonomy.guarded.lists = [...]`. | **Rejected.** Recreates the [`OQ-PT8`](../reference/providers.md#oq-pt8) container-kind antipattern. `autonomy` is exclusive to agent-owning packs (`CombineExclusive`). Nesting lists under autonomy breaks single-responsibility contribution kinds. |
| **D. Mount host `list-record.json` into `/ctx/`** | Mount the host's rmw insertion record into the jail and subtract inserted entries during jail adoption. | **Rejected as overcomplicated.** Requires new cross-boundary bind mounts and coupling between host provenance storage and in-jail runtime. In-jail sanitization using staged pack manifests achieves the same guarantee with zero mount changes. |
| **E. Lua transform hook** | Use a Lua `yolo.transform("pi", ...)` script on host apply. | **Rejected.** Lua transforms were deliberately removed from yolo's architecture (2026-09-24). Packs must remain declarative data. |

---

## 6. Risks and Mitigations

| Risk | Impact | Mitigation |
| :--- | :--- | :--- |
| **User manually installs same package on host** | If a user manually added `@czottmann/pi-automode` to their host settings, in-jail sanitization strips it from the jail's host layer. | This is the desired behavior: the pack declared that this package must not run in jails. If the user explicitly wants it in a specific jail, they install it in-jail via `pi install`, which records an in-jail `list-capture` addition that outranks the host layer. |
| **Typo in notch name** | `"notches": ["hosst"]` could silently fail to apply anywhere. | Strict validation in `packdecl`: any notch name not in `["jail", "host", "guest"]` causes a fatal load error. |
| **Multiple notches specified** | Author specifies `notches: ["jail", "guest"]`. | Supported naturally by array membership check. |
| **Repeated composition / idempotency** | `yolo host apply` run multiple times duplicates array entries. | Existing `config-list` deduplication (`entryKey` comparison) already prevents duplicate additions. |

---

## 7. Open Questions

1. 💬 **OQ-1: Selector vocabulary — Confinement notch (`notches`) vs Autonomy posture (`postures`) vs both.**
   Should the selector field be named `notches: ["host"]` (matching confinement levels `jail`, `guest`, `host`), `postures: ["guarded"]` (matching `autonomous`/`guarded`), or should both concepts be unified?

   <!-- vantage: oq id=OQ-1 leaning="notches: ['host'] (with singular 'notch': 'host' accepted). Yolo's user-facing dial is confinement (--at <notch>, confinement: <notch>). Non-security host tools care about the host environment, not permission prompts. For guest notch, autonomy is on by default, so host-only cleanly isolates unconfined execution." -->

   _Leaning:_ `notches: ["host"]` (with singular `"notch": "host"` accepted as syntactic sugar).
   Yolo's primary boundary axis is the confinement notch (`render.Kind`). Host-specific tools
   (e.g., extensions that talk to host Docker, native macOS keychains, or host notification daemons)
   need host scoping because of host environment reach, not permission policies. The host notch
   is currently the sole unconfined notch (`AgentAutonomy: false`), so `"host"` satisfies both
   permission gates and host-environment packages.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-2: Filtering host-only entries out of `readsHost` layers in the jail.**
   When `pi/settings` declares `readsHost: true`, the host's `~/.pi/agent/settings.json`
   (which contains host-applied packages) is mounted into the jail as `hostBytes`.
   How should the in-jail renderer prevent host-applied entries from entering the jail base array?

   <!-- vantage: oq id=OQ-2 leaning="Sanitize hostBytes at known list paths using the staged pack declarations. The jail already holds all selected pack manifests; it knows exactly which entries are scoped non-jail. Stripping them from hostBytes at those paths before composition requires no new mounts or sidecar plumbing." -->

   _Leaning:_ Sanitize `hostBytes` at list paths in `prism.go` using the staged pack declarations.
   Because the jail already has the full pack manifests staged under `YOLO_PACK_ROOT`, it knows
   every entry contributed with `notches: ["host"]`. Stripping matching entries from the
   mounted host array before `ComposeStateful` ensures they never enter the layer fold,
   cannot be captured by in-jail edits, and require no new host provenance mounts.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-3: Symmetry — extending the selector to `config-overlay`.**
   Should `config-overlay` receive the exact same `notches` modifier as `config-list`?

   <!-- vantage: oq id=OQ-3 leaning="Yes. Symmetrical cross-pack contribution capabilities. A pack author configuring an agent on the host often needs to set host-specific settings (such as paths or local endpoints) in addition to package lists." -->

   _Leaning:_ Yes. `config-overlay` and `config-list` are sibling contribution mechanisms
   collected in `packoverlay.Collect`. Adding `notches` to both maintains architectural
   symmetry and prevents a follow-up RFC when a pack needs a host-only scalar config setting.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-4: Interaction with `yolo config` inspection commands.**
   How should `yolo config ls` and `yolo config render` display notch-scoped contributions
   when inspecting configuration?

   <!-- vantage: oq id=OQ-4 leaning="Honor the target notch specified via --at <notch> (defaulting to cwd context). config ls shows contributions applicable to the selected notch, and notes inactive contributions as filtered by notch." -->

   _Leaning:_ Respect the resolved `t.notch` in `internal/cli/config.go`. `yolo config ls --at host`
   displays the host contributions as active; `yolo config ls --at jail` displays jail
   contributions as active and marks host-only contributions as filtered.

   **Answer:**
   > _(empty — fill in when decided)_

---

## 8. Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-1 | Pending user ruling: `notches` selector on contributions | — | [§4.1](#41-manifest-syntax-the-notches-modifier) | — |
| OQ-2 | Pending user ruling: In-jail `hostBytes` list-path sanitization | — | [§4.3](#43-sanitizing-readshost-list-paths-in-prismgo) | — |
| OQ-3 | Pending user ruling: Symmetrical `notches` on `config-overlay` | — | [§4.1](#41-manifest-syntax-the-notches-modifier) | — |
| OQ-4 | Pending user ruling: CLI inspection reflects resolved notch | — | [§7](#7-open-questions) | — |
