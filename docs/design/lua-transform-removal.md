---
title: "The config transform is an escape hatch nobody uses, on a VM everybody does"
date: 2026-09-10
status: in-review
tags: [design, config, lua, agentcfg, removal, packs]
summary: "The Lua config transform — the config.lua hook between the merge and the managed-enforce step — has no user, its one worked example is now served by the declarative autonomy kind, and half of its parts shipped inert at least once. Remove it. But the Lua VM it runs on is the derive path's too, so the removal is a split of one package, not a deletion of Lua — and the managed floor has to be lifted out of that package before the transform half goes."
vantage:
  status-chip: true
---

# The config transform is an escape hatch nobody uses, on a VM everybody does

**Status:** DESIGN, 2026-09-10. **Nothing removed.** Every claim about the tree was verified on
2026-09-10 at `4975df07`; each carries its evidence inline. Two questions need the maintainer:
[OQ-LT1](#OQ-LT1) (what a user who still has a `config.lua` sees on the day this ships) and
[OQ-LT2](#OQ-LT2) (whether the design principle the transform embodied retires with it).

> **In short.** The transform slot in the config-composition pipeline should be deleted: it has no
> user, its motivating case is met by a declarative mechanism that works where the transform's
> premise did not, and its determinism requirement is stated in a doc and enforced by nothing. What
> makes the removal a design question rather than a `git rm` is that the transform's package,
> `luahook`, is **shared with the six shipped packs' `derive.lua`** — so the cut must split the
> package along a seam this doc draws, and must carry the managed floor (`Enforce`) out of a
> Lua-named package it never belonged in.

**Why it matters.** Three things, in order of weight. The workspace `yolo-jail.config.lua` is the
one input that executes code at every boot and sits **outside the config diff, drift and snapshot**
— [`trust-paths.md`](trust-paths.md) row 13 of its crossing inventory. The feature has a record of
shipping half-wired — a per-surface key nothing read, a user file nothing mounted, a stage-exclude
nothing acts on, a lint nothing calls — and a leftover from a careless removal would extend that
record. And the maintainer's own words: *"I'm not sure it's fully thought through."*

**The shape.** `luahook` is two halves and a seam. The **transform half** (`LuaVM`, `Transform`,
`Apply`, the `Ctx` bridge, `Stage`, `GopherLuaVM.Run`, the ctx-table builders, `ValidateSandbox`)
goes. The **shared core** (`GopherLuaVM` and its timeout, `openSandboxLibs`, `ForbiddenGlobals`,
`wrapLuaErr`, the marshallers, all of `derive.go`) stays and becomes the package's whole identity.
**`Enforce` moves** into `internal/agentcfg` as the pipeline's own step.

**Cost.** One documented escape hatch, one `host_files` key, one pack-manifest field, two
auto-loaded files, one bind mount, two provenance tokens and roughly thirty-five tests. A residual
capability gap, named plainly in [§6](#6-what-is-genuinely-lost). The design of record,
[`agent-settings-composition.md`](../plans/agent-settings-composition.md), is left with a title
that is half false.

**Start at [§4](#4-the-boundary--what-goes-what-stays-what-moves)** — the seam through `luahook`.
Getting that wrong is the main way this removal goes bad in either direction; everything else falls
out of it.

**Needs your ruling:** [OQ-LT1](#OQ-LT1), [OQ-LT2](#OQ-LT2).

**Reads with:** [`agent-settings-composition.md`](../plans/agent-settings-composition.md) (the
design of record; this retires its [§3](../plans/agent-settings-composition.md#3-the-lua-transform--the-abstraction)),
[`config-ownership-and-promotion.md`](config-ownership-and-promotion.md) (in review; names the
`transform` layer in four lines that will need a word each), [`trust-paths.md`](trust-paths.md)
(the ungated-crossing finding), [`host-file-staging.md`](../plans/host-file-staging.md) (the
`transform` key's own documentation and the raw-surface example), [`pack-system.md`](../reference/pack-system.md)
(the derive half, which stays), [`BACKLOG.md`](../plans/BACKLOG.md) (A9 and A13, the inert episodes).
No companion sketch: the removal surface is the design's own subject and lives in
[§5](#5-the-removal-surface-enumerated); an implementation plan follows [OQ-LT1](#OQ-LT1).

---

## 1. The verdict

Remove the transform. Keep the VM. Move the floor.

Three principles carry the rest of the doc; later sections cite them by number.

- **P1. The removal is a split, not a deletion.** `luahook` serves two callers. Removing the
  transform must leave `derive.lua` running byte-for-byte as it does today, and every proof the
  transform's tests currently supply about the *shared* sandbox must be re-expressed against the
  derive path **before** those tests are deleted. A green `derive_test.go` is the tripwire, not the
  goal.
- **P2. Nothing may go quiet.** A user with a `config.lua` today gets a loud, named outcome on the
  day this ships — never a plausible-looking config with the hook silently gone. That is the exact
  failure class the A9 fix ([§3.4](#34-shipped-inert-repeatedly)) was written against, and the
  removal must not reintroduce it. The disposition is [OQ-LT1](#OQ-LT1); "silently ignore" is not
  one of its options.
- **P3. The managed floor is the pipeline's, not the VM's.** `Enforce` — the step that re-asserts
  `managed` keys over everything below — runs today as a method on the transform's `Ctx`. It has
  nothing to do with Lua. It moves into `internal/agentcfg` with its semantics **unchanged**, and
  the doc says what "unchanged" pins ([§4.2](#42-what-moves-enforce)).

## 2. What exists today, stated precisely

### 2.1 The slot in the pipeline

Compose renders one surface as `decode → deepMerge → transform(Lua) → enforce(managed) → encode`
([`compose.go:8`](../../internal/agentcfg/compose.go#L8)). The transform step is one call —
[`compose.go:448-449`](../../internal/agentcfg/compose.go#L448-L449) builds a `luahook.Ctx` over
the merged value and runs `luahook.Apply`; an empty script is the identity
([`luahook.go:279-282`](../../internal/agentcfg/luahook/luahook.go#L279-L282)). Its edits are
attributed to a `transform` provenance layer, with a `transform (dropped)` variant for keys it
deleted ([`compose.go:457-472`](../../internal/agentcfg/compose.go#L457-L472)). Then the same
`Ctx` re-applies the managed layer: `ctx.Config = transformed; ctx.Enforce()`
([`compose.go:478-479`](../../internal/agentcfg/compose.go#L478-L479)).

The script reaches Compose as two `Inputs` fields, `Script` and `VM`
([`compose.go:80-83`](../../internal/agentcfg/compose.go#L80-L83)), and the transform's one other
output — the globs it asked to exclude from a staged tree — comes back as `Result.Excluded`
([`compose.go:103-105`](../../internal/agentcfg/compose.go#L103-L105)).

### 2.2 Three channels feed the script

| Channel | Where it is read | Where it is delivered |
| :--- | :--- | :--- |
| **User file** `~/.config/yolo-jail/config.lua` | [`prism.go:200-203`](../../internal/entrypoint/prism.go#L200-L203) (boot) and, separately, [`config.go:350-353`](../../internal/cli/config.go#L350-L353) (`yolo config render`) | bind-mounted `:ro` into the jail when present — [`inheritscope.go:209-218`](../../internal/cli/run/inheritscope.go#L209-L218), with an Apple Container twin |
| **Workspace file** `<workspace>/yolo-jail.config.lua` | [`prism.go:213-219`](../../internal/entrypoint/prism.go#L213-L219); the CLI copy reads it **cwd-relative** at [`config.go:356`](../../internal/cli/config.go#L356) | rides the `/workspace` bind |
| **Per-surface key** `transform: <path>` | appended after the two files by `surfaceScript` — [`prism.go:238-249`](../../internal/entrypoint/prism.go#L238-L249) | two declarers: the `host_files` config key ([`hostfiles.go:541-550`](../../internal/config/hostfiles.go#L541-L550) → [`entrypoint/hostfiles.go:193`](../../internal/entrypoint/hostfiles.go#L193)) and a pack's `config` surface via `SurfaceDTO` ([`load.go:34`](../../internal/agentcfg/manifest/load.go#L34), [`:68`](../../internal/agentcfg/manifest/load.go#L68)) |

The order is user, then workspace, then the surface's own hook, later registrations overriding
earlier ones for the same agent. A `config-overlay` contribution may **not** carry `transform` —
refused by name at [`overlay.go:71-72`](../../internal/agentcfg/manifest/overlay.go#L71-L72).

### 2.3 The package: two halves and a seam

`internal/agentcfg/luahook` is five source files. Verified 2026-09-10 by reading each and by
`go list` / `rg` over `internal/` and `cmd/`:

| File | Lines | Serves |
| :--- | ---: | :--- |
| [`luahook.go`](../../internal/agentcfg/luahook/luahook.go) | 339 | **Transform** — `LuaVM`, `Transform`, `Ctx`, `NewCtx`/`NewCtxKind`, `Apply`, `Stage`. Plus `Enforce`, which is neither half's ([§4.2](#42-what-moves-enforce)). |
| [`vm.go`](../../internal/agentcfg/luahook/vm.go) | 291 | **Mostly transform** — `GopherLuaVM.Run`, `registerYoloTable`, `buildCtxTable`, `readOnlyManaged`, `buildStageTable`. **Shared**: the `GopherLuaVM` type and `Timeout`, `openSandboxLibs`, `extraStrippedGlobals`, `wrapLuaErr`. |
| [`sandbox.go`](../../internal/agentcfg/luahook/sandbox.go) | 128 | **Shared**: `ForbiddenGlobals` (applied by `openSandboxLibs`). **Transform-named, uncalled**: `ValidateSandbox`, `AllowedGlobals` — no production caller anywhere in `internal/` or `cmd/`. |
| [`marshal.go`](../../internal/agentcfg/luahook/marshal.go) | 205 | **Shared** — `goToLua` / `luaToGo`; derive calls both ([`derive.go:418`](../../internal/agentcfg/luahook/derive.go#L418), [`:489`](../../internal/agentcfg/luahook/derive.go#L489)). |
| [`derive.go`](../../internal/agentcfg/luahook/derive.go) | 538 | **Derive** — `DeriveCtx`, `DeriveVM`, `Derive`, `DeriveRegistrations`, the tombstone sentinel. Borrows `openSandboxLibs` ([`:199`](../../internal/agentcfg/luahook/derive.go#L199)) and `wrapLuaErr` ([`:251`](../../internal/agentcfg/luahook/derive.go#L251), [`:280`](../../internal/agentcfg/luahook/derive.go#L280)). |

Outside the package, the transform half has exactly one production caller — `compose.go` — plus
the three sites that construct a `GopherLuaVM` to hand it a script
([`prism.go:346`](../../internal/entrypoint/prism.go#L346), [`:517`](../../internal/entrypoint/prism.go#L517),
[`config.go:204`](../../internal/cli/config.go#L204)). The derive half has three:
[`packsurfaces.go:283`](../../internal/entrypoint/packsurfaces.go#L283) (boot),
[`deriveenv.go:73`](../../internal/packload/deriveenv.go#L73) and [`:133`](../../internal/packload/deriveenv.go#L133)
(host-side registrations and the env producer). Six packs ship a `derive.lua` — `agy`, `claude`,
`codex`, `copilot`, `opencode`, `pi`, 716 lines between them — and **none of them can reach
`yolo.transform`**: the derive session registers only `yolo.derive` and `yolo.env`
([`derive.go:219-233`](../../internal/agentcfg/luahook/derive.go#L219-L233)), and an unknown
member is refused or reported by the guard. There is no third channel.

## 3. Diagnosis — why remove

The six facts the maintainer asked to have re-verified, in his order, with two corrections and
four additions the verification turned up.

### 3.1 The motivating case is dead

The design of record's only end-to-end example is the **pi permission gate**
([§6.5](../plans/agent-settings-composition.md#65-worked-example--the-pi-permission-gate-end-to-end)):
a host-side safety extension that should not cross into a jail "where the container *is* the
boundary", dropped from `extensions` by a transform and kept out of the tree by
`ctx.stage.exclude`. That disposition is now the **`autonomy` contribution kind**: `packs/pi/pack.json`
ships an `autonomy` block whose `autonomous` and `guarded` variants set `managed.defaultProjectTrust`
to `always` and `ask` ([`pack.json:51-79`](../../packs/pi/pack.json#L51-L79)). It is declarative,
per notch, and works at the **host** notch — where the transform's premise (the container is the
boundary) does not hold at all. The kind landed 2026-08-01 (`686cf418`); pi migrated to it the same
day (`0b3814d7`).

**Residue check.** `packs/pi/` has never contained the string `permission` in any revision
(`git log -S'permission' -- packs/pi/` returns nothing, checked 2026-09-10). The gate in
[§6.5](../plans/agent-settings-composition.md#65-worked-example--the-pi-permission-gate-end-to-end)
was never a shipped artifact — it exists as doc prose and as test fixtures that invent
`extensions/permission-gate.ts` ([`compose_test.go:25-42`](../../internal/agentcfg/compose_test.go#L25-L42),
[`cli/config_test.go:27-35`](../../internal/cli/config_test.go#L27-L35), and the `luahook`,
`prism` and `staterender` tests). The only live `defaultProjectTrust` in the tree is the `autonomy`
block.

### 3.2 Zero usage

- This workspace has no `yolo-jail.config.lua`. The user-scope `~/.config/yolo-jail/config.lua`
  exists and is **0 bytes** (dated Aug 17) — the identity transform by the code's own definition.
- No shipped pack declares `transform` on a surface, and no `derive.lua` mentions it (`rg` over
  `packs/`, 2026-09-10).
- No integration test, script, or `flake.nix` line mentions the transform or `config.lua`.
- The `luahook` package's history has **no transform-focused commit after `ce9860f9`
  (2026-07-27)**; every later commit is derive, profiles, providers or citation upkeep. The transform
  was introduced 2026-07-20 (`6ee79cda`, `e85819ef`, `f18d08ef`) — 52 days ago at this writing.

### 3.3 Pack-shippable after all — a correction

The verification brief said the manifest schema has no transform field because `rg transform
internal/packdecl/` returns nothing. It returns nothing because `packdecl` holds a pack's `config`
contribution as `json.RawMessage` ([`contributes.go:353`](../../internal/packdecl/contributes.go#L353))
and defers decoding to `manifest.DecodeSurfaces` ([`packload.go:148`](../../internal/packload/packload.go#L148)),
whose `SurfaceDTO` **does** carry `transform` ([`load.go:34`](../../internal/agentcfg/manifest/load.go#L34)).
[`pack-system.md`](../reference/pack-system.md) lists it among the surface fields a contributor may
not set. So a pack *can* declare it — copied verbatim onto `Surface.Transform`
([`load.go:68`](../../internal/agentcfg/manifest/load.go#L68)), read by `os.ReadFile` with no
pack-relative resolution and no `~` expansion ([`prism.go:243`](../../internal/entrypoint/prism.go#L243)),
so the path would have to be absolute and exist inside the jail. Nobody does. The conclusion is
unchanged; the reason is different, and it adds one item to the removal surface
([§5.3](#53-the-config-schema-and-the-cli)).

### 3.4 Shipped inert, repeatedly

The feature's history is the strongest single argument against leaving any part of it standing.
Each row was verified against `git log -S` on 2026-09-10.

| Episode | What was dead | For how long | Where it is recorded |
| :--- | :--- | :--- | :--- |
| **A9** | `Surface.Transform` — a documented `host_files` key, validated and copied onto the surface, read by nothing | 2026-07-24 (`2ec75e44`) → 2026-07-26 (`145c7f10`): **2 days** | docstring at [`prism.go:227-237`](../../internal/entrypoint/prism.go#L227-L237); [`BACKLOG.md`](../plans/BACKLOG.md) row A9 |
| **A13** | the user `config.lua` — advertised as auto-loaded, never mounted into the jail on any backend | first appearance → 2026-07-27 (`c069b28b`): **the feature's first week** | `c069b28b`'s message: *"A documented feature with no channel"*; [`BACKLOG.md`](../plans/BACKLOG.md) row A13 |
| **`ctx.stage.exclude`** | half of the worked example: the globs are recorded into `Result.Excluded` and **printed** by `yolo config render` ([`config.go:311-312`](../../internal/cli/config.go#L311-L312)); nothing prunes a tree. There is no tree surface to prune — the manifest has four modes and none is a tree ([`manifest.go:161-170`](../../internal/agentcfg/manifest/manifest.go#L161-L170)) | **still** | the design of record concedes it: [§10.2](../plans/agent-settings-composition.md#102-why-the-intended-replacement-isnt-built), *"Nothing acts on it"* |
| **`ValidateSandbox`** | the static lint that "rejects an obviously escaping script BEFORE it runs" | **still** — no caller outside its own tests | [`sandbox.go:73-99`](../../internal/agentcfg/luahook/sandbox.go#L73-L99) |
| **`config_transform`** | the config key [§3.4](../plans/agent-settings-composition.md#34-placement-in-config-sandbox-and-safety) says "may still point elsewhere for the unusual case" | **never built** — the string exists nowhere in `internal/` | the design of record, twice |
| **Two loaders** | [`prism.go:194-199`](../../internal/entrypoint/prism.go#L194-L199) says `targetTransformScript` is *"the convergence point … one Target-keyed loader instead of two hand-copies"*; the second hand-copy still exists at [`config.go:347-359`](../../internal/cli/config.go#L347-L359), reading the workspace file cwd-relative | since `c7a8c5aa` (2026-08-01) | both files |

Four of six are live today. A feature whose parts keep turning out to be disconnected is one whose
model nobody is holding in their head — which is the maintainer's instinct, measured.

### 3.5 Determinism: required, unenforced, and unenforceable

[§3.4](../plans/agent-settings-composition.md#34-placement-in-config-sandbox-and-safety) of the
design of record: *"The transform is a pure function of its inputs; determinism is required (a
non-deterministic transform breaks the overlay's diff)."* The reason is real — the capture overlay
is `mergeDiff(last_render, current_file)`, and a render that differs between two boots with no edit
would be captured as an edit forever.

Nothing checks it, and the sandbox does not even close the one stock door. `openSandboxLibs` opens
Lua's `math` library whole ([`vm.go:144-147`](../../internal/agentcfg/luahook/vm.go#L144-L147)), and
neither `ForbiddenGlobals` nor `extraStrippedGlobals` names `math.random`. **Measured 2026-09-10**
with a throwaway test in the package (run, then deleted): a transform calling `math.random()`
succeeds and `math.randomseed` is non-nil; the same is true on the derive path. So the requirement
is a sentence in a doc. It *could* be checked — render twice and compare — at the cost of running
every transform twice per surface per boot, for a feature with no user. Removal makes the
requirement true by construction.

> [!NOTE]
> The derive path inherits the same open door. A `derive.lua` is also required to be a pure
> function ([`pack-system.md`](../reference/pack-system.md), the derive section), and it too can
> call `math.random`. That is a real finding and **out of this doc's scope**
> ([§11](#11-what-this-doc-does-not-propose)) — the fix is one line in `extraStrippedGlobals` and
> belongs to whoever owns the derive sandbox.

### 3.6 The trust finding

[`trust-paths.md`](trust-paths.md)'s crossing inventory, row 13: the workspace
`yolo-jail.config.lua` is *"activated by existing"*, reaches *"agent context, transitively in-jail
exec"*, is disclosed **never** — *"not a config key, so outside the diff, drift and snapshot"* — and
changes *"every boot, with nothing to diff against"*. Every other code-shaped input in that table is
either a config key (so the config gate sees it) or a pack (so the pack machinery discloses it).
The transform is the one that is neither. Removing it closes the row; nothing else on this list
would.

### 3.7 What removal does not buy: the dependency

[`hostfiles.go:697-701`](../../internal/config/hostfiles.go#L697-L701) explains that
`builtinSurfacePaths` reads packs rather than `agentcfg.BuiltinManifest()` because the latter
*"would have pulled the Lua VM (agentcfg/luahook → gopher-lua) into internal/config and therefore
into every binary that reads config. internal/packload has no such dependency."* That was true when
written (`bbe84f1d`, 2026-07-27). It has been false since `f55f2109` (2026-09-02):
[`deriveenv.go:27`](../../internal/packload/deriveenv.go#L27) imports `luahook`, so `internal/config`
→ `packload` → `luahook` → `gopher-lua` today, transform or no transform.

Measured with `go list -deps` on 2026-09-10: `gopher-lua` is linked into **three of the eight**
`cmd/` binaries — `yolo`, `yolo-entrypoint`, `yolo-jaild` — and all three import `packload`. The
transform's removal changes that set by **zero**. The transform is roughly 600 lines of Go and
its tests; the VM and the dependency stay for the derive path. Anyone expecting a smaller binary
from this removal should not; anyone worried it would break the derive path should read
[§4](#4-the-boundary--what-goes-what-stays-what-moves).

### 3.8 One more mismatch, found while writing

Capture narrowing landed today (`aac5b569`, `19acebc1`): the overlay is narrowed against the
declarative layers above it — `computed` and `managed`
([`staterender.go:387`](../../internal/agentcfg/staterender.go#L387)) — so it stores only edits that
can win. The transform sits above the overlay too and can also override a captured key, but it
**cannot be narrowed against**: it is a function, not a layer, and its effect on a key is unknown
until it runs. The pipeline's own newest rule has no way to include it. That is the general shape of
the problem: every declarative layer composes with every other; the transform composes with none.

## 4. The boundary — what goes, what stays, what moves

This is the section the maintainer flagged as the way the removal goes bad in either direction.
The table is per exported symbol and per unexported helper, from reading the five files on
2026-09-10. **Goes** means deleted with the transform; **stays** means unchanged, now serving derive
alone; **moves** means re-homed per [P3](#1-the-verdict).

### 4.1 `luahook`, symbol by symbol

| Symbol | File | Disposition | Why |
| :--- | :--- | :--- | :--- |
| `LuaVM` (interface), `Transform`, `Apply` | `luahook.go` | **goes** | the transform's contract and its one entry point; sole caller is `compose.go` |
| `Ctx` and its fields `Config`, `Kind`, `Managed`, `Stage`, `Agent`, `Surface`; `NewCtx`, `NewCtxKind`, `newCtx`, `managedView`, `ConfigMap`, `ManagedMap` | `luahook.go` | **goes** | the bridge a hook sees; derive has its own `DeriveCtx` and does not share a field with it |
| `Ctx.enforced`, `Ctx.Enforce`, `enforceValue`, `deepCopyMap`, `deepCopyValue` | `luahook.go` | **moves** → `internal/agentcfg` | the managed floor — [§4.2](#42-what-moves-enforce) |
| `Stage`, `Stage.Exclude`, `Stage.Excluded` | `luahook.go` | **goes** | records globs nothing consumes ([§3.4](#34-shipped-inert-repeatedly)) |
| `GopherLuaVM` (type), `Timeout`, `DefaultTimeout` | `vm.go` | **stays** | derive's VM; `Derive` and `DeriveRegistrations` are its methods |
| `GopherLuaVM.Run` | `vm.go` | **goes** | the transform's executor |
| `openSandboxLibs`, `extraStrippedGlobals` | `vm.go` | **stays** | derive builds its sandbox with it ([`derive.go:199`](../../internal/agentcfg/luahook/derive.go#L199)) |
| `registerYoloTable`, `buildCtxTable`, `configAsAny`, `mapAsAny`, `readOnlyManaged`, `buildStageTable` | `vm.go` | **goes** | build the transform's `yolo` and `ctx` tables; derive builds its own ([`derive.go:219-247`](../../internal/agentcfg/luahook/derive.go#L219-L247)) |
| `wrapLuaErr` | `vm.go` | **stays** | derive's errors go through it; its `"lua transform error:"` prefix is quoted in derive's own docstring and tests, so renaming it is a wording choice the implementer may make and must then propagate |
| `ForbiddenGlobals` | `sandbox.go` | **stays** | applied by `openSandboxLibs` |
| `AllowedGlobals`, `ValidateSandbox`, `usesIdentifier`, `isIdentByte` | `sandbox.go` | **goes** | no production caller today; the VM environment is the boundary, and the derive path never used the lint |
| everything in `marshal.go` | `marshal.go` | **stays** | derive marshals in and out through it |
| everything in `derive.go` | `derive.go` | **stays** | untouched — [P1](#1-the-verdict) |
| the package doc ([`luahook.go:1-24`](../../internal/agentcfg/luahook/luahook.go#L1-L24)) | `luahook.go` | **rewritten** | it describes the package as "the config-composition Lua transform sandbox"; after the cut the package is the pack derive sandbox and should say so |

Two consequences the implementer must not be left to discover:

- **`internal/agentcfg` stops importing `luahook` entirely.** `compose.go` uses the package only
  for the four calls in [§2.1](#21-the-slot-in-the-pipeline). Once `Enforce` moves, the engine
  package has no Lua dependency at all — which is the correct dependency shape, and a checkable
  outcome ([§10](#10-what-i-would-do-in-order)).
- **The sandbox proofs live in the wrong tests.** `vm_test.go`'s `TestRealVM_*` prove the *shared*
  guarantees — forbidden globals absent, safe libs present, an infinite loop times out, a Lua error
  carries file and line, a compile error surfaces, nested and integer values round-trip — but they
  prove them **through `Apply`** ([`vm_test.go:66-175`](../../internal/agentcfg/luahook/vm_test.go#L66-L175)).
  `derive_test.go` proves only that `os` is absent ([`derive_test.go:197-202`](../../internal/agentcfg/luahook/derive_test.go#L197-L202)).
  Deleting `vm_test.go` before re-homing those proofs on `Derive` leaves the derive sandbox
  asserted by one test. [P1](#1-the-verdict) forbids that order.

### 4.2 What moves: `Enforce`

`Ctx.Enforce` ([`luahook.go:215-265`](../../internal/agentcfg/luahook/luahook.go#L215-L265)) is the
managed floor: it merges the enforced layer over the composed value, managed winning, **deep** for
objects (so a host `permissions.ask` survives beside a managed `permissions.allow`), whole-value for
a keyless surface. It uses the original enforced layer captured privately at construction, which is
what makes the Lua-visible `ctx.managed` effectively read-only. None of that is about Lua. It
belongs in `internal/agentcfg`, called from the same place in `Compose` it is called today.

"Unchanged" pins three things:

1. **The merge is not the fold's merge.** `mergeValue` ([`engine.go:63-90`](../../internal/agentcfg/engine.go#L63-L90))
   is RFC 7386: a `null` under a key **deletes** the key. `enforceValue`
   ([`luahook.go:251-265`](../../internal/agentcfg/luahook/luahook.go#L251-L265)) is not: a
   non-object managed value — including `nil` — is **assigned** by deep copy. The two disagree on a
   nil-valued managed key, and Compose's provenance loop already special-cases that value
   ([`compose.go:480-485`](../../internal/agentcfg/compose.go#L480-L485)). The move must **not**
   reuse `mergeValue`, however tempting one merge looks; it moves `enforceValue` verbatim and pins
   the nil case with a test before it moves.
2. **Keyless enforce replaces the whole value** ([`luahook.go:236-241`](../../internal/agentcfg/luahook/luahook.go#L236-L241)).
   Today the only test of that is `TestComposeRawManagedReplacesWholeFile`, and it proves it
   **through a transform script** ([`keyless_test.go:340-361`](../../internal/agentcfg/keyless_test.go#L340-L361)).
   That test is rewritten without the script first, or the keyless floor ships unproven.
3. **Boot output is byte-identical** for every jail that has no `config.lua` — which is every jail
   yolo knows of. `TestRenderFingerprintStable` ([`renderfingerprint_test.go:115`](../../internal/entrypoint/renderfingerprint_test.go#L115))
   and the eleven `TestCompose*EnforcesManaged`-shaped tests in `compose_test.go` are the guard;
   they must pass unmodified across the move.

Where in `internal/agentcfg` it lands — `compose.go`, `engine.go`, or a file of its own — is the
implementer's. That it lands there, and not in `luahook`, is not: leaving the floor in a package
named for a VM it no longer needs is exactly the disconnected-parts pattern of
[§3.4](#34-shipped-inert-repeatedly).

## 5. The removal surface, enumerated

Every consumer, from an `rg` sweep over `internal/`, `cmd/`, `packs/`, `docs/`, `integration/`,
`scripts/` and `flake.nix` on 2026-09-10, classified. Line numbers are as of `4975df07`; the
sections a removal must touch are what matter, and the linked anchors are what to re-check.

### 5.1 The engine

| What | Where | Disposition |
| :--- | :--- | :--- |
| `Inputs.Script`, `Inputs.VM` | [`compose.go:80-83`](../../internal/agentcfg/compose.go#L80-L83) | goes |
| the transform step and its attribution | [`compose.go:434-472`](../../internal/agentcfg/compose.go#L434-L472) | goes |
| `ctx.Config = transformed; ctx.Enforce()` | [`compose.go:478-479`](../../internal/agentcfg/compose.go#L478-L479) | becomes the moved `Enforce` call ([§4.2](#42-what-moves-enforce)) |
| `layerTransform` and the `" (dropped)"` variant | [`compose.go:139`](../../internal/agentcfg/compose.go#L139), [`:462`](../../internal/agentcfg/compose.go#L462), [`:467`](../../internal/agentcfg/compose.go#L467), [`:471`](../../internal/agentcfg/compose.go#L471) | goes — the provenance vocabulary loses two tokens; see [§7](#7-ship-day-state-that-already-exists) for records that still carry them |
| `Result.Excluded`, `dedupeStable` | [`compose.go:103-105`](../../internal/agentcfg/compose.go#L103-L105), [`:499-517`](../../internal/agentcfg/compose.go#L499-L517) | goes |
| the `luahook` import | [`compose.go:24`](../../internal/agentcfg/compose.go#L24) | goes |
| pipeline comments naming the step | [`compose.go:8`](../../internal/agentcfg/compose.go#L8), [`:31`](../../internal/agentcfg/compose.go#L31), [`:64-74`](../../internal/agentcfg/compose.go#L64-L74), [`engine.go:18`](../../internal/agentcfg/engine.go#L18), `staterender.go` (two comments; the file is under active edit — coordinate), [`codec.go:92-93`](../../internal/agentcfg/codec/codec.go#L92-L93), [`raw.go:8`](../../internal/agentcfg/codec/raw.go#L8) | shrink |

### 5.2 The producers and loaders

| What | Where | Disposition |
| :--- | :--- | :--- |
| `loadPrismTransformScript`, `targetTransformScript`, `surfaceScript` | [`prism.go:183-249`](../../internal/entrypoint/prism.go#L183-L249) | goes |
| the two `Script:`/`VM:` producers and their `luahook.LuaVM` locals | [`prism.go:340-356`](../../internal/entrypoint/prism.go#L340-L356), [`:511-526`](../../internal/entrypoint/prism.go#L511-L526) | shrink |
| `loadTransformScript`, `renderSurface`'s `script`/`vm` parameters, the excluded-files line, the `transform` colour | [`config.go:200-204`](../../internal/cli/config.go#L200-L204), [`:261`](../../internal/cli/config.go#L261), [`:284-288`](../../internal/cli/config.go#L284-L288), [`:311-312`](../../internal/cli/config.go#L311-L312), [`:323-331`](../../internal/cli/config.go#L323-L331), [`:344-359`](../../internal/cli/config.go#L344-L359) | goes / shrink |
| `yolo config --help` text naming transforms and the two files | [`config.go:7`](../../internal/cli/config.go#L7), [`:30`](../../internal/cli/config.go#L30), [`:65`](../../internal/cli/config.go#L65), [`:92-93`](../../internal/cli/config.go#L92-L93) | shrink — user-facing |
| `yolo config ls` layer column | [`configls.go:199-200`](../../internal/cli/configls.go#L199-L200), [`:220-221`](../../internal/cli/configls.go#L220-L221) | goes |
| `yolo config diff` explanation naming a transform as a cause | [`configdiff.go:461-465`](../../internal/cli/configdiff.go#L461-L465) | shrink |
| comments | [`prism.go:19`](../../internal/entrypoint/prism.go#L19), [`:264`](../../internal/entrypoint/prism.go#L264), [`:1041`](../../internal/entrypoint/prism.go#L1041), [`prism_mise.go:43`](../../internal/entrypoint/prism_mise.go#L43), [`tomltrivia.go:28`](../../internal/entrypoint/tomltrivia.go#L28), [`env.go:81`](../../internal/entrypoint/env.go#L81), [`:205-206`](../../internal/entrypoint/env.go#L205-L206), [`hostrender.go:161`](../../internal/entrypoint/hostrender.go#L161), [`richtext.go:37`](../../internal/richtext/richtext.go#L37) | shrink |

### 5.3 The config schema and the CLI

| What | Where | Disposition |
| :--- | :--- | :--- |
| `host_files[].transform` — the known-key list, the field, the validation, and the stale dependency comment | [`hostfiles.go:74`](../../internal/config/hostfiles.go#L74), [`:113-115`](../../internal/config/hostfiles.go#L113-L115), [`:541-550`](../../internal/config/hostfiles.go#L541-L550), [`:697-701`](../../internal/config/hostfiles.go#L697-L701) | goes → a **named refusal** replaces it ([OQ-LT1](#OQ-LT1)); the comment is rewritten either way, since it is wrong today ([§3.7](#37-what-removal-does-not-buy-the-dependency)) |
| `yolo config-ref` row | [`config_ref.txt:536`](../../internal/cli/config_ref.txt#L536) | goes |
| `manifest.Surface.Transform` and its comments | [`manifest.go:29`](../../internal/agentcfg/manifest/manifest.go#L29), [`:65-66`](../../internal/agentcfg/manifest/manifest.go#L65-L66), [`:107-110`](../../internal/agentcfg/manifest/manifest.go#L107-L110) | goes |
| `SurfaceDTO.Transform` | [`load.go:34`](../../internal/agentcfg/manifest/load.go#L34), [`:68`](../../internal/agentcfg/manifest/load.go#L68) | goes — `DecodeSurfaces` already refuses unknown fields ([`load.go:99`](../../internal/agentcfg/manifest/load.go#L99)), so a pack declaring `transform` fails loudly by construction; whether it fails **by name** is [OQ-LT1](#OQ-LT1) |
| the `config-overlay` refused-field row for `transform` | [`overlay.go:46`](../../internal/agentcfg/manifest/overlay.go#L46), [`:71-72`](../../internal/agentcfg/manifest/overlay.go#L71-L72) | goes — redundant once the field is unknown everywhere; keeping it as a named refusal is the implementer's call |
| `hostFileSurface`'s copy, and the A12 comment naming the hook | [`entrypoint/hostfiles.go:57`](../../internal/entrypoint/hostfiles.go#L57), [`:193`](../../internal/entrypoint/hostfiles.go#L193) | goes / shrink |

### 5.4 The mount channel

| What | Where | Disposition |
| :--- | :--- | :--- |
| the user `config.lua` single-file bind and its Apple Container twin, plus the doc comment explaining why it crosses unfiltered | [`inheritscope.go:139-153`](../../internal/cli/run/inheritscope.go#L139-L153), [`:209-218`](../../internal/cli/run/inheritscope.go#L209-L218) | goes |
| one of the reasons the staged pack tree is mounted `:ro` | [`assemble.go:650`](../../internal/cli/run/assemble.go#L650) | the **mount stays** (packs' `files` still need it); the sentence loses one clause |
| comment | [`inherit.go:23`](../../internal/config/inherit.go#L23) | shrink |

### 5.5 Tests

File-level, because the per-test list is implementation-plan material; the two rows marked ⚠ are
the ones [§4.2](#42-what-moves-enforce) says must be handled **before** anything is deleted.

| File | What it holds | Disposition |
| :--- | :--- | :--- |
| [`luahook_test.go`](../../internal/agentcfg/luahook/luahook_test.go) | `Apply`, read-only `ctx.managed`, `ValidateSandbox` | goes |
| ⚠ [`vm_test.go`](../../internal/agentcfg/luahook/vm_test.go) | the shared sandbox and marshal proofs, via `Apply` | **re-home on `Derive` first**, then goes |
| [`compose_test.go`](../../internal/agentcfg/compose_test.go) | five transform tests (the worked example, managed-wins-over-transform, computed-below-transform, Lua-error-fails-closed, script-without-VM) beside the enforce suite | the five go; the enforce suite **stays as the guard** |
| ⚠ [`keyless_test.go`](../../internal/agentcfg/keyless_test.go) | five raw/lines transform tests, and the keyless managed-floor proof written through a script | the five go; `TestComposeRawManagedReplacesWholeFile` is **rewritten without the script first** |
| [`probe_adv_test.go:70-77`](../../internal/agentcfg/probe_adv_test.go#L70-L77) | the pre-A9 probe that `Compose` ignores `Surface.Transform` | goes |
| [`retiredlayer_test.go:90-91`](../../internal/agentcfg/retiredlayer_test.go#L90-L91) | asserts a `transform` token proves nothing to `LayerAsserted` | **stays** — it is the pre-existing-state guarantee of [§7](#7-ship-day-state-that-already-exists) |
| [`entrypoint/hostfiles_test.go:548-604`](../../internal/entrypoint/hostfiles_test.go#L548-L604) | the two A9 tests (per-surface hook runs; a missing hook is an error) | goes |
| [`cli/config_test.go`](../../internal/cli/config_test.go) | the `piGateTransform` fixture, `--explain`'s `transform` row and colour | shrinks |
| [`inheritscope_test.go:265-290`](../../internal/cli/run/inheritscope_test.go#L265-L290), [`assemble_test.go:579-603`](../../internal/cli/run/assemble_test.go#L579-L603) | the A13 mount tests | goes |
| [`config/hostfiles_test.go:252`](../../internal/config/hostfiles_test.go#L252), [`config/inherit_test.go:184`](../../internal/config/inherit_test.go#L184), [`manifest/overlay_test.go:31`](../../internal/agentcfg/manifest/overlay_test.go#L31) | schema-level mentions | shrink; the overlay test's `transform` fixture becomes an unknown-field case |
| `derive_test.go`, `deriveapiskew_test.go`, `marshal_test.go`, the `zz_probe_*` order probes, `builtin_test.go` | derive, marshal, and an unrelated adjective | **untouched** |

### 5.6 Documentation

| Doc | What it says | Disposition |
| :--- | :--- | :--- |
| [`agent-settings-composition.md`](../plans/agent-settings-composition.md) | the design of record: title, [§1](../plans/agent-settings-composition.md#1-the-decision-in-one-paragraph)'s thesis ("Lua is the *only* transform mechanism"), principles 4 and 5 of [§2](../plans/agent-settings-composition.md#2-six-principles-the-line-in-the-sand), all of [§3](../plans/agent-settings-composition.md#3-the-lua-transform--the-abstraction), the pipeline lines, [§6.5](../plans/agent-settings-composition.md#65-worked-example--the-pi-permission-gate-end-to-end) | a **dated postscript at the top** in the removal commit, per the design-doc convention; the body keeps its tense. Graduating the still-true half (layers, capture overlay, `render`) into a reference doc is a separate task |
| [`host-file-staging.md`](../plans/host-file-staging.md) | the `transform` row of the key table and the whole *Transforms on non-object surfaces* section | postscript on the section; the row goes |
| [`pack-system.md`](../reference/pack-system.md) | layer-order lines with `[lua transform]`; the surface-field list; a registrations row that lists `yolo.transform` among **derive** registrations — which is wrong today ([§2.3](#23-the-package-two-halves-and-a-seam)) | the lines are corrected; the wrong row is corrected regardless |
| [`config-migration-to-prism.md`](../reference/config-migration-to-prism.md) | "Loading the Lua transform" as a boot step; `config.lua` as a user input | corrected |
| [`config-ownership-and-promotion.md`](config-ownership-and-promotion.md) | the layer stack ([§2.1](config-ownership-and-promotion.md#21-the-layer-stack)), one clause in [§5.4](config-ownership-and-promotion.md#54-promotion-moves-a-key-down-the-stack), the "no change to the precedence stack" promise in [§7](config-ownership-and-promotion.md#7-what-this-does-not-propose), and [OQ-CO6](config-ownership-and-promotion.md#OQ-CO6)'s framing | **in review — not edited by this doc.** Four one-word edits after its review closes; [OQ-CO6](config-ownership-and-promotion.md#OQ-CO6)'s argument survives on `computed` and `managed` alone |
| [`trust-paths.md`](trust-paths.md) | row 13 of the crossing inventory | becomes "removed 2026-…", the way that table records closed rows |
| [`BACKLOG.md`](../plans/BACKLOG.md) rows A9, A13; [`composed-config-work.md`](../plans/composed-config-work.md); [`open-rulings.md`](../plans/open-rulings.md) | history of the inert episodes | **kept** — a doc recording a defect is supposed to name it |
| [`roadmap.md`](../plans/roadmap.md) | one supporting clause in 💬 7 lists `transform` among the layers capture loses to | one word |
| [`cli-visual-polish.md`](../plans/cli-visual-polish.md), [`self-documenting-cli.md`](../reference/self-documenting-cli.md), the fzf example README, [`macos.md`](../guides/macos.md), and a dozen comment-level mentions | the `transform` colour, the discoverability claim, the field list, a file list | sweep with `rg -n 'transform|config\.lua' docs/` at removal time; none is normative |

## 6. What is genuinely lost

The steelman first, because it is real: the transform is the **only** mechanism that can reshape
composed output in a way the declarative layers cannot express — it sees the merged value and can
compute an edit from it. Everything else in the pipeline sets, overrides, or deletes a key by name.

What the declarative layers cover today, checked against the tree:

| Need | Declarative answer today | Residual |
| :--- | :--- | :--- |
| Force a key in the jail | `managed` — pack surface or `host_files` | none |
| An overridable base | `defaults` | none |
| A different posture per notch (host vs jail) | the `autonomy` kind ([§3.1](#31-the-motivating-case-is-dead)) | none — and it works at the host notch, which the transform never could |
| Another pack adds or overrides whole keys on a surface it does not own | `config-overlay` | none |
| Content computed from live tables or the active profile | `derive.lua` — sandboxed Lua, pre-merge, with tombstones ([`pack-system.md`](../reference/pack-system.md)) | none for a pack author; a user authors a local pack |
| Delete a key the host supplies | a `null` tombstone from a `computed` or `config-overlay` layer | none |
| Keep an in-jail edit across boots | the capture overlay (`mode: capture`) | it is per-workspace **state**, not a declaration |
| **Edit inside a host-supplied array, or conditionally on its value** | — | **gap.** RFC 7386 replaces arrays wholesale; nothing declarative says "remove the element matching X". This is exactly what [§6.5](../plans/agent-settings-composition.md#65-worked-example--the-pi-permission-gate-end-to-end)'s script did, and what `autonomy` sidestepped rather than solved |
| **A partial rewrite of a raw surface** — the documented example is `ctx.config:gsub("/Users/matt/", "/home/agent/")` on a host `.npmrc` ([`host-file-staging.md`](../plans/host-file-staging.md#transforms-on-non-object-surfaces)) | `host_files` with `content:` (an inline jail-specific copy) or `mode: capture` and edit once in the jail | **gap** for a declared, portable, *partial* edit of a file the host also owns |
| Keep a file out of a staged tree | — | **not a loss**: no tree surface exists and `ctx.stage.exclude` never acted ([§3.4](#34-shipped-inert-repeatedly)) |

So the honest statement is: **two real gaps, one shape** — a value-dependent, partial edit,
declared once and carried everywhere. Both have workarounds that are state rather than declaration
(capture once) or duplication rather than derivation (`content:`). Neither has had a user in 52
days. And the way to close either gap declaratively — a per-key `filter`, `replace`, or `gsub` op —
is the "data-filter vocabulary" that principle 4 of the design of record explicitly rejected in
favour of Lua. Removing the transform therefore also decides, silently unless said, that if this
need ever arrives it will be met by exactly the kind of op that principle rejected — or not at all.
That is [OQ-LT2](#OQ-LT2), and I would rather the maintainer rule on it than have the next author
rediscover the principle in `git log`.

## 7. Ship day: state that already exists

What is out there on the day the removal lands, and what happens to each. The disposition of the
first two rows is [OQ-LT1](#OQ-LT1); the rest are settled by evidence.

| Pre-existing state | Where | What the code does after removal, absent a rule | Ruling |
| :--- | :--- | :--- | :--- |
| a **non-empty** `~/.config/yolo-jail/config.lua` or `<workspace>/yolo-jail.config.lua` | any host that wrote one | nothing reads it; the file is inert and the user's agent config silently changes | [OQ-LT1](#OQ-LT1) — [P2](#1-the-verdict) forbids "nothing" |
| a `host_files[].transform` key, or a pack surface with `transform` | user config; a third-party pack | the config loader refuses the unknown key (the `host_files` known-key list is closed); `DecodeSurfaces` refuses the unknown field — both loud, neither names the removal | [OQ-LT1](#OQ-LT1) — the precedent is a refusal **that names its replacement** (`validateJournalRetired`, [`validate.go:426-470`](../../internal/config/validate.go#L426-L470)) |
| the **0-byte** `config.lua` on this very machine | `~/.config/yolo-jail/config.lua` | an empty script is the identity today ([`luahook.go:62-64`](../../internal/agentcfg/luahook/luahook.go#L62-L64)); after removal it is a stray file | ignored — nothing changes for it, so no message is owed; the leaning under [OQ-LT1](#OQ-LT1) keys on non-emptiness for this reason |
| provenance sidecars carrying a `transform` or `transform (dropped)` token, written by an earlier render | `<workspace>/.yolo/prism/*.provenance`; host-provenance records | `ParseProvenanceRecord` accepts any token; `LayerAsserted` is a closed set and already returns false for `transform` ([`retiredlayer_test.go:90-91`](../../internal/agentcfg/retiredlayer_test.go#L90-L91)); `colorLayer` falls through to plain text | **already fail-safe** — no migration, no action |
| a capture overlay narrowed against `computed` and `managed` today | `.yolo/prism/*.overlay.json` | unaffected; the transform was never a narrowing input ([§3.8](#38-one-more-mismatch-found-while-writing)) | none |
| a host `yolo` and a jail `yolo-entrypoint` on different sides of the removal | any skewed machine | old launcher mounts a file the new entrypoint ignores, or vice versa — inert either way, and `version.SourceSkew` refuses the pairing before boot | none |

## 8. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| **Keep it as is.** | Rejected. No user, four live disconnected parts ([§3.4](#34-shipped-inert-repeatedly)), an unenforced determinism requirement, and the one ungated code-execution crossing in [`trust-paths.md`](trust-paths.md). |
| **Keep it, gated behind the `config_transform` key the design promised.** | Rejected. A key makes the crossing visible to the config gate — closing [§3.6](#36-the-trust-finding) — but builds a fifth part for a feature with no user. Worth building only if [OQ-LT2](#OQ-LT2) rules that the escape hatch must survive. |
| **Move it to the pack side: let `derive.lua` register `yolo.transform`.** | Rejected for now. It would give pack authors a post-merge hook and inherit the derive path's disclosure rules. It also reintroduces value-dependent editing at pack level with zero demand behind it. If the [§6](#6-what-is-genuinely-lost) gap ever has a concrete case, this is the shape to revisit before any data vocabulary. |
| **Remove Lua entirely — `gopher-lua`, `luahook`, the derive path.** | Rejected. Six shipped packs compute their MCP/LSP reshapes and their provider environment in `derive.lua`; the provider system ([`providers.md`](../reference/providers.md)) depends on it. Out of scope by [P1](#1-the-verdict). |
| **Delete the transform half but leave `Ctx` and `Enforce` in `luahook`.** | Rejected. The engine would keep importing a Lua package to call a function that touches no Lua, and the package doc would describe a bridge nothing crosses — the [§3.4](#34-shipped-inert-repeatedly) pattern, reproduced on purpose. |
| **Enforce determinism instead of removing the feature** (render twice, compare; strip `math.random`). | Rejected as the *reason to keep it* — it doubles every surface render for a feature nobody uses. Stripping `math.random` is still right for the derive sandbox ([§11](#11-what-this-doc-does-not-propose)). |

## 9. Risks

| Risk | Mitigation |
| :--- | :--- |
| **R1 — `Enforce` changes meaning in the move.** The obvious "reuse `mergeValue`" changes what a nil-valued managed key does ([§4.2](#42-what-moves-enforce) item 1). | Move `enforceValue` verbatim; pin the nil case with a test before the move; `TestRenderFingerprintStable` and the `compose_test.go` enforce suite pass unmodified. |
| **R2 — the shared sandbox loses its proofs.** `vm_test.go` is where forbidden-globals, timeout, error-location and round-trip are proven, and it dies with `Apply`. | Re-home each on `Derive` first ([§4.1](#41-luahook-symbol-by-symbol)); a `derive_test.go` that proves only `os` is absent is not a sandbox proof. |
| **R3 — the keyless floor ships unproven.** Its only test today runs a transform ([§4.2](#42-what-moves-enforce) item 2). | Rewrite without the script first. |
| **R4 — a user's `config.lua` goes quiet.** | [OQ-LT1](#OQ-LT1); the refusal pattern exists and is named. |
| **R5 — old provenance records confuse a reader.** | Already fail-safe ([§7](#7-ship-day-state-that-already-exists)); `retiredlayer_test.go` stays as the guard. |
| **R6 — the cut takes shared code.** `openSandboxLibs`, `wrapLuaErr`, the marshallers, the `GopherLuaVM` type all *look* like transform code. | The per-symbol table in [§4.1](#41-luahook-symbol-by-symbol); `derive_test.go` and `deriveapiskew_test.go` green is the tripwire. |
| **R7 — an in-review doc is edited under its reviewer.** | [`config-ownership-and-promotion.md`](config-ownership-and-promotion.md) is not touched until its review closes; the four edits are listed in [§5.6](#56-documentation). |
| **R8 — `staterender.go` is under active edit** for the capture-narrowing fix. | The engine steps here touch `compose.go` and `luahook`; the two `staterender.go` comments are the last edit, not the first. |
| **R9 — the design of record's title becomes false.** | A dated postscript, in the removal commit, saying what changed and where the current story lives ([§5.6](#56-documentation)). |

## 10. What I would do, in order

Six steps, each a commit; the first two are safe to land **before** any ruling, because they change
no behaviour and make the rest of the removal checkable.

1. **Pin first.** A `Derive`-based twin of every shared proof in `vm_test.go`; `TestComposeRawManagedReplacesWholeFile`
   rewritten without a script; one new compose-level test for a nil-valued managed key, asserting
   whatever `Enforce` does today. Nothing deleted.
2. **Lift the floor.** `Enforce`, `enforceValue` and the deep-copy helpers into `internal/agentcfg`;
   `Compose` calls the moved function; the `luahook` import leaves `compose.go`. The checkable
   outcome is `go list -deps ./internal/agentcfg` no longer naming `gopher-lua`, with every
   existing test byte-for-byte green.
3. **Cut the producers** ([§5.1](#51-the-engine), [§5.2](#52-the-producers-and-loaders)): `Inputs.Script`/`VM`,
   the three loaders, both `Script:` sites, `renderSurface`'s parameters, `Result.Excluded`, the
   `transform` tokens and colour, the help text. `yolo config render` and `--explain` lose one line
   and one hue and are otherwise unchanged.
4. **Cut the channels** ([§5.3](#53-the-config-schema-and-the-cli), [§5.4](#54-the-mount-channel)) per
   [OQ-LT1](#OQ-LT1): the `host_files` key becomes a named refusal; `SurfaceDTO` loses the field; the
   `inheritscope.go` mount goes; the file probe lands in the launcher beside `refuseLiveWorkspaceLaunch`
   if the ruling asks for one.
5. **Delete the transform half of `luahook`** ([§4.1](#41-luahook-symbol-by-symbol)) and rewrite its
   package doc as the pack derive sandbox.
6. **Docs** ([§5.6](#56-documentation)): the postscript, the reference docs' layer-order lines,
   [`trust-paths.md`](trust-paths.md) row 13, the roadmap clause, and — after its review — the four
   lines in the ownership doc.

Steps 3 through 6 wait on [OQ-LT1](#OQ-LT1). Step 6's postscript should also record [OQ-LT2](#OQ-LT2)'s
ruling, whichever way it goes, because that is where the next author will look.

## 11. What this doc does not propose

- **Removing Lua, `gopher-lua`, or `luahook`.** The package shrinks to its derive half and stays.
- **Touching `derive.lua`, `yolo.derive`, or `yolo.env`.** Not one line.
- **Fixing the derive sandbox's `math.random`.** Found here ([§3.5](#35-determinism-required-unenforced-and-unenforceable)),
  owned elsewhere. One line in `extraStrippedGlobals`, plus a test.
- **Adding a declarative replacement** for the [§6](#6-what-is-genuinely-lost) gap. [OQ-LT2](#OQ-LT2)
  decides whether that is ever on the table; this doc does not design it.
- **Re-ordering the layer stack.** `defaults → host → workspace → config-overlay → overlay →
  computed → managed` is the stack with one element removed, not a new stack.
- **Editing [`config-ownership-and-promotion.md`](config-ownership-and-promotion.md)** while it is
  in review.
- **Building the `workspace` layer** the design of record calls "DECIDED BUT UNWIRED". Unrelated,
  and [OQ-CO8](config-ownership-and-promotion.md#OQ-CO8) depends on that wording staying put.

## 12. Open Questions

1. 💬 **OQ-LT1: What does a user who still has a transform see on ship day?** Three channels can
   carry one — the two auto-loaded files and the per-surface key in `host_files` or a pack manifest
   ([§2.2](#22-three-channels-feed-the-script)). The options are a **refusal that names the
   removal**, a **warning**, or **silence**; and for the files, whether the check is permanent or
   lives one release like `removeRetiredGeneratedDirs`. This decides step 4 of
   [§10](#10-what-i-would-do-in-order) and the shape of the one new piece of code the removal adds.
   Silence is the option [P2](#1-the-verdict) rules out and the A9 docstring argues against
   ("the user asked for a transform, got none, and the file looks plausibly correct").

   <!-- vantage: oq id=OQ-LT1 leaning="Refuse by name, permanently, for the keys — host_files transform and the pack DTO field — following validateJournalRetired. For the two files: refuse at launch when the file is NON-EMPTY, naming the removal and the file; say nothing for a 0-byte file, which was already the identity. A warning scrolls past in the banner and is the quiet failure by another route." -->

   _Leaning:_ Refuse by name, permanently, for the keys, following `validateJournalRetired`'s
   pattern. For the two files: refuse at launch when the file is **non-empty**, naming the removal
   and the path; stay silent for a 0-byte file, which was already the identity transform. A warning
   is the quiet failure by another route — it scrolls past in the launch banner, and the config
   still changes underneath the user. Permanent rather than one release: it is two `stat` calls,
   and a one-release probe is a hidden state machine.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-LT2: Does principle 4 retire with the transform?** Principle 4 of the design of record
   ([§2](../plans/agent-settings-composition.md#2-six-principles-the-line-in-the-sand)) is
   *"Transform with Lua, not a data vocabulary"* — reshaping is a hook, never a closed op-set.
   Removing the hook leaves the two gaps in [§6](#6-what-is-genuinely-lost) with no answer, and the
   only declarative answers are exactly the ops the principle rejected. This decides what the
   postscript on the design of record says, and whether a future "remove the element matching X"
   request is met with a design or with a pointer to this ledger.

   <!-- vantage: oq id=OQ-LT2 leaning="Yes, retire it: rule that the gaps are accepted, the workarounds (capture once, or content: for a jail-specific copy) are the answer, and a declarative op is designed only against a concrete case — the transform is not coming back in another guise." -->

   _Leaning:_ Yes, retire it. Rule that the two gaps are accepted, that the workarounds — capture
   once, or `content:` for a jail-specific copy — are the answer for now, and that a declarative op
   is designed only against a concrete case, if one ever arrives. Fifty-two days without a user is
   the evidence; the pack-side `yolo.transform` of [§8](#8-alternatives-considered) is the shape to
   reach for first if that changes, not a data vocabulary.

   **Answer:**
   > _(empty — fill in when decided)_

## 13. Decision Ledger

No rulings yet. Rows appear here as the questions in [§12](#12-open-questions) close.

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| — | — | — | — |
