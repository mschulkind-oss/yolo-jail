---
status: accepted
stage: DECIDED
next: "Every task is built; delete this plan when the design graduates"
depends-on: [../design/pack-naming.md, ../design/readable-config.md]
---

# Plan: pack naming

**Status:** 2026-10-31. Completed against the tree, written against `67b336e09` (main at
`efe94c655`, plus the held fixes of design [§9](../design/pack-naming.md#9-build-order) step 1).
**Design:** [`pack-naming.md`](../design/pack-naming.md), every question ruled
([Decision Ledger](../design/pack-naming.md#decision-ledger)).
Precedence: the design wins on behavior, the tree wins on fact, this file is advice and is the
first thing to be wrong. Stop and report, do not commit, when a test does not fail the way a task
predicts, a named symbol is gone, or a gate fails outside the task's scope.

Three words, defined in the design's [§3](../design/pack-naming.md#3-the-design): the **handle**
(the config spelling, `acme/tools`), the **identity** (what records key on), the **namespace**
(the word an agent-facing name starts with).

## Map

| Path | Change |
| :--- | :--- |
| `internal/packname/` | new — `Normalize`, `Word` (the `[a-z0-9-]` check that prints the legal spelling), `Delivered(ns, name)`, the 64-character limit |
| `internal/packsrc/references.go`, `naming.go` (new) | `bare` on a source definition; `{ pack = …, bare/namespace }` and `{ git = …, bare/namespace }` entries; each reference carries `Namespace`, `NamespaceOwner`, `Landing` |
| `internal/config/schema.go`, `packs.go`, `readablepacks.go`, `packresolve.go` | the new keys; `PackEntry` carries the three fields onto `packload.Pack` |
| `internal/packload/packload.go`, `packtreerecord.go` | `Pack.Namespace/NamespaceOwner/Landing`; `files` landing and `/ctx/host-…` use `Landing`; the tree record carries them to the jail |
| `internal/entrypoint/packsurfaces.go`, `internal/cli/run/macosctxtree.go`, `packhostgrants.go` | read the recorded naming; `/ctx` uses `CtxName()` |
| `internal/hostskills/` | delivered names on both notches: skill dirs and the copied `SKILL.md` `name`; a wrapped plugin's copied manifest `name`; the clash message per design [§3.5](../design/pack-naming.md#35-clashes) |
| `internal/packload/` (new `namecollisions.go`) | namespace-word and landing clashes, called from `yolo check`, the launch and `yolo host apply` |
| `internal/packdecl` lint | `skills_tier = "namespaced"` warns where it changes nothing (a pack wrapping no plugin), PN-D14 |
| `packs/claude`, `packs/copilot`, `yolo pack init --plugin` scaffold | keep `skills_tier = "namespaced"` where a plugin is wrapped whole (PN-D14) |
| `internal/configmigrate/` (new `identities.go`), `internal/packsrc/identity.go` | the identity index and the record moves on first read (step 3, as built in design PN-D15); write `bare = true`, folder-named sources, the old → new list (step 4) |
| `internal/cli/pack.go` + `packsourcerename.go` | `yolo pack source rename OLD NEW`, built with the PN-D10 slice |
| config key plumbing for `shipped_skill_prefix` | the user-only key list below (step 5) |

## Reuse

- `hostskills.ClaimsAt`, `layerClaims`, `Collisions` — the clash detector already runs on both
  notches; extend its claims, do not write a second one.
- `configmigrate` journal (`migrationJournal`, `finish`, `Resume`) and `respellPackRecords` with
  `packsrc.RenamePackLock/RenameForkLock/RenameCheckRecords` — the RC-D47 carry is the re-key
  machinery; the re-key is a different key function over the same writers.
- `configedit.ApplyTargeted` and `promptYesNo` (`internal/cli/pack.go`) — the rename verb's edit
  and confirmation.
- A user-only key: copy `promotion_target`'s path through `knownTopLevelConfigKeys`,
  `workspacescope.go`, `validate.go`, `inherit.go`, `changetiming`, `render/configkeys.go`,
  `setupcensus/configkeys.go` and `cli/config_ref.txt`; the key-census tests list every file.
- Fixtures: `testconfig.User`, `testsupport.IsolateHome`, `newMigrateEnv`, `runOfferLaunchWith`.

## Traps

- **Legacy JSONC entries and the conventional local pack carry no namespace** and land as before:
  only a TOML entry is parsed by `packsrc.ParsePackReference`. Advice: keep it that way; the
  migration writes `bare = true`, so the converted config lands the same names.
- **The embedded early return in `config.ResolvePack`** skips the naming copy; a shipped pack is
  bare, so that is correct, but a new field added later must be copied on both paths.
- **The jail rebuilds `packload.Pack` from the tree record**, not from config: a naming field not
  written by `WritePackTreeRecordForWorkspace` silently reverts to the handle in the jail.
- **The change predicate** (`Changed`, `deliveredDigest`) compares a delivered copy with its
  source byte for byte; a rewritten `SKILL.md` `name` makes every apply look changed unless the
  predicate compares the rewritten bytes.
- **A worktree-isolation hook** refuses compound shell commands naming git; run git as
  `git -C <worktree>` alone.

## Tasks

All four are built; the design's ledger names each commit and its tests.

1. **Namespaces and delivered names** (design [§9](../design/pack-naming.md#9-build-order) step 2). Settled, do not re-open: [§3.3](../design/pack-naming.md#33-the-namespace--what-agents-see)-[§3.5](../design/pack-naming.md#35-clashes),
   PN-D5, PN-D6, PN-D8, PN-D9. Tests: the [§3.4.1](../design/pack-naming.md#341-a-worked-example) example as a table of delivered names, and each
   row of its second table as a refusal naming both entries and the step. Gate:
   `go test -short ./internal/packsrc/... ./internal/packname/... ./internal/hostskills/... ./internal/config/... ./internal/cli/...`.
2. **Identity-keyed records** (after 1). Settled: [§3.2](../design/pack-naming.md#32-the-pack-identity--what-records-key-on), PN-D10. Built as an identity index and
   record moves on first read rather than a re-key in the RC-D47 journal (design PN-D15); a moved
   pack re-pins once and the launch says so.
3. **Rename verb and migration marking** (after 2). Settled: [§3.6](../design/pack-naming.md#36-renaming), [§7](../design/pack-naming.md#7-migration), PN-D7, PN-D12.
4. **`shipped_skill_prefix`** (after 1). Settled: [§3.7](../design/pack-naming.md#37-shipped-and-third-party), PN-D13.

Each task: `just format`, then `env -u YOLO_ALLOW_MISSING_PROGRAMS just check-ci` before landing.

## Ships with

- `docs/reference/pack-system.md` "skills collision" and its `skills_tier` text;
  [`config-user.md` §4](../design/config-user.md#4-naming-a-pack-that-is-not-shipped); the user guide's pack pages; `yolo config-ref`.
- Tests asserting the escaped or `<pack>:<skill>` names (`internal/hostskills` deliver, compose and
  plugin tests, `internal/cli` apply and init tests): rewrite them to the new names, never relax
  them until green.

## Don't

- Don't rename on a clash: PN-D3 forbids any name depending on the selected set.
- Don't edit `internal/ghbroker` or the grant code in `internal/config`: another agent owns it.

## Blockers

- **The plugin-shape hole.** Retiring `skills_tier = "namespaced"` (PN-D9) also removes the switch
  that today decides whether a wrapped plugin's whole tree (hooks, MCP servers) is delivered or
  only its skills. Advice taken: a wrapped plugin is always delivered as a tree under
  `<namespace>-<manifest name>`, downgraded to flat skills only where a foreign directory holds
  the name, as today. Reported to the owner as a design hole. Superseded by
  [PN-D14](../design/pack-naming.md#PN-D14): a wrapper delivers its plugin whole only when it
  opts in with `skills_tier = "namespaced"`; otherwise its skills go out flat and its hooks and
  MCP servers stay refused, as on main.
