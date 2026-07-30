# Pack declaration reform — implementation plan

**Status:** plan, 2026-07-29. Design: [`../design/pack-declaration-reform.md`](../design/pack-declaration-reform.md).
Reasoning for each item lives in that doc's section cited beside it; this plan only sequences the
**decided** work and says what is blocked on an open question.

## How many open questions must be answered before work starts?

**Two, and both are answered** (2026-07-29): OQ1 (the canonical type + `derive` projection + typed
exports) and OQ2 (`config-overlay` + per-key provenance). Those two are the only ones that shape
the *format* enough to gate a first cut — everything OQ1 decides is load-bearing (the design says
so), and OQ2 decides whether a `config` kind is sole-owned or overlayable.

The remaining nine are **not** start-blockers, for a specific reason each:

| OQ | Why it does not block starting |
|---|---|
| 3 — footprint in the description hash | Additive. The footprint is computed first (Phase 2); hashing it is a later consumer that changes no schema. |
| 4 — how far to generalize the assembler + provenance format | The plan builds the provenance manifest for config *now* (OQ2 forces it); "how far to generalize" is a later-slice decision, not a Phase-1 one. |
| 5 — skills reshape seam | Leaning is "keep today's plain tree copy"; that is *no change*, so it blocks nothing. Only a second skills format would reopen it. |
| 6 — machine-scope gating | Policy on an existing field (`sharedDirs` → `state`); the kind ships either way, the gate is tunable later. |
| 7 — refuse vs report user-config collisions | Severity knob on Phase-2's collision check; both behaviors sit on the same computed union. |
| 8 — per-notch matrix as a registry column | Only bites at `guest`/`host`, which do not render pack surfaces yet (that is its own broken-middle work). Jail-only Phase 1 is unaffected. |
| 9 — `derive` per-surface vs per-pack | Authoring-layout detail; pick one when writing Phase 3, does not change what a `derive` *is*. |
| 10 — tier-2 effect Lua | Decided **no**; nothing to build. |
| 11 — retire `retireMiseTools`/`RetireOnFirstRender` | Cleanup ejection; independent of the new schema, do it whenever. |

So: **start now**, on the slice OQ1+OQ2 fully specify, and treat the other nine as decisions that
land *inside* the phase that first needs them.

## The through-line

The design's one rule (`§3.6`) is **every file has exactly one writer**: a pack owns a file, or it
feeds typed inputs to a neutral core assembler. Config already has that assembler (the compose
engine); this reform makes the *manifest* express contributions as kinds, makes the assembler
**record who wrote what** (provenance), and pulls the **MCP definition** to a shared location with
per-agent `derive` projections. That is the spine; the phases below are slices of it that each ship
standing on their own.

## Guardrails (from the design, non-negotiable while building)

- **No switch on a tool name in core** (`§4.1`). Core may name domain nouns (`mcp_server`), never
  `claude`. Every phase that touches core is checked against this.
- **One reshape mechanism: `derive`** (`§3.3`, OQ1). No `Copy`/`Fold`/`Inject` op DSL is introduced
  — `internal/agentcfg/project` is on the chopping block, not the foundation.
- **The manifest stays static data** (`§2`). `derive` is a *value* computed at compose time in the
  existing sandbox; the manifest that declares "there is a derive here" stays lintable/hashable.
- **Verify in a nested jail**, not just unit tests (AGENTS.md): every phase touching `cmd/` or
  `internal/` ends with `./dist-go/linux-$(go env GOARCH)/yolo -- bash` from in-jail.

---

## Phase 0 — Freeze the kind registry (no behavior change)

The vocabulary must exist before anything consumes it. This is pure scaffolding and ships green
with zero runtime change.

1. **Add a closed `kind` registry** — a `knownKinds` set in `internal/packdecl`, one entry per kind
   from `§3.2` (`program`, `skills`, `briefing`, `files`, `config`, `config-overlay`, `state`,
   `reads-host`, `launch`, `hook`), each carrying its footprint descriptor (claim shape + conflict
   rule). Modeled on the existing closed sets (`knownModes`, `knownComputedSources`, `KnownHooks`)
   so validation is loud on an unknown kind. (`§3.1`)
2. **No `contributes[]` parsing yet** — Phase 0 only lands the registry + footprint types + tests.
   The manifest is untouched; nothing reads the registry until Phase 1.

**Done when:** `knownKinds` exists with a footprint descriptor per kind, unit-tested, and
`go test ./internal/packdecl/` is green. No pack changes.

---

## Phase 1 — The footprint: compute and check the one-writer rule

This is the highest-value early slice because it delivers the "good citizen" guarantee (`§1.4`) and
is a prerequisite for provenance, and it can run **over today's manifest fields** — it does not wait
for `contributes[]`.

1. **Map existing fields → kinds → claims.** A function that reads a loaded pack's *current*
   declarations (`mounts`/`writableDirs`/`sharedDirs`/`hostFiles`/`install`/`surfaces`/`launchFlags`)
   and emits the footprint (list of claims, each tagged with its kind and conflict rule). This is
   the compatibility shim that lets footprints exist before the manifest is rewritten.
2. **Compute the union across selected packs + user config**, and apply the per-kind conflict table
   (`§3.2`). Replaces the scattered `HostFileConflicts` (one pack, one kind) and `union()` (silent
   dedup) with one pass. (OQ7 decides severity: **refuse** pack-vs-pack, **report** pack-vs-user —
   land the computation now, wire the severity per OQ7 when it is answered; default to report+warn
   until then.)
3. **`yolo pack explain --footprint`** — the view in `§3.2`. Prints each claim, its kind, the
   combine/conflict rule, and flags the review-worthy ones (machine-scope `state`, `reads-host`,
   installer URL).
4. **Fold the check into `yolo check`** so a collision is caught host-side before boot.

**Done when:** `yolo pack explain --footprint` prints the table for the shipped packs; `yolo check`
reports a synthetic two-pack `files` collision; the `packload.Embedded*`-not-selection-gated
workaround (`§1.4`) can be retired because the union is now computable at the right time. Nested-jail
verified.

---

## Phase 2 — The provenance manifest (config assembler records who wrote what)

OQ2 makes this non-optional: `config-overlay` needs per-key provenance to be legible. Build it as a
generalization of the compose engine's existing `last_render` sidecar (`§3.6`).

1. **Extend the compose result to carry per-key source** — for each key in a composed surface, which
   layer/contribution set it (defaults / host / computed / derive / managed / which pack's overlay).
   The engine already tracks layers internally; this surfaces it as a recorded artifact.
2. **Write a machine-readable provenance record** per composed file (format is OQ4's call, but the
   minimal shape — one entry per file, contributing sources, winning source per key — is decided by
   OQ2 and can ship now). Must survive across runs (drift/`--footprint` read it).
3. **`config-overlay` kind** (OQ2): a pack declares `kind: "config-overlay"` naming a target surface
   owned by another pack; the assembler merges it *after* the owner (later-wins) and records the
   override. **Refuse silent same-surface duplicates** — an overlay must name its target.
4. **Surface overrides** in `--footprint` and at lint ("key X: claude pack lost to house-rules
   overlay").

**Done when:** a `house-rules` overlay adding a `managed` key to `claude/settings` composes
correctly, the provenance record shows the per-key winner, and an override of an owner's key is
reported. Nested-jail verified against the shipped `claude` surfaces (byte-equality of every
unchanged surface before/after — this touches the A12-fatal boot render).

---

## Phase 3 — MCP to a shared location, projection as `derive` (OQ1)

> **⚠ BLOCKED on a ruling (found 2026-07-29 during implementation).**
> Plain-language walkthrough of this whole blocker: [phase3-reconcile-explained.md](phase3-reconcile-explained.md).
> The plan's step-4
> "delete `computed.go` wholesale, all reshape → `derive`" does not survive contact with the code:
> **one `computed[]` use is stateful and a `derive` function is pure.** Enumerated across the six
> shipped packs, the `computed[]` blocks split into:
>
> | Use | Packs | Portable to a pure `derive`? |
> |---|---|---|
> | passthrough / project (reshape one entry) | agy/mcp, codex/config, copilot/mcp, copilot/lsp, opencode/config | **Yes** — pure function of the entry |
> | tombstone (delete a key) | claude/settings | **Yes** — `out.k = nil` |
> | flags (conditional key on a table) | claude/settings ×2 | **Yes** — the §3.3 worked example |
> | **`reconcile` (managed dynamic table on an RMW file)** | **claude/config** | **NO** — it is sidecar-STATEFUL: it removes the MCP names yolo asserted *last boot* and re-adds the current set, so an agent-added server survives while a yolo-dropped one is removed. A pure `derive` has no memory of last boot; `reconcileRMWTables` (`prism.go:430`) tracks it via a managed-name sidecar. This is the exact `.claude.json` reconciliation the hand-written writer did before the DSL generalized it. |
>
> So `derive` can absorb 8 of the 9 `computed[]` uses; the 9th (`reconcile`) was a genuinely
> stateful RMW mechanism that is not a projection at all.
>
> **RESOLVED (OQ12 decided (d), 2026-07-29): reconcile is DELETED, not relocated.** yolo owns the
> top-level `mcpServers` block and regenerates it wholesale each boot (with a drop notice); the
> preserve-hand-edits behavior — Claude's alone, and contrary to "regenerate, don't reconcile" — is
> gone. Shipped in **Phase 3a** (commit `feat(entrypoint)!: delete reconcile`). This removed the
> last stateful use of `computed[]`, so the remaining 8 uses are all pure and the DSLs can die.

**Phase status (2026-07-29):**

- **3a — delete reconcile ✅ DONE.** `reconcileRMWTables` + the managed-name sidecar gone;
  `regenerateManagedTables` regenerates `mcpServers` wholesale; nested-jail verified.
- **3b — port the 8 pure reshapes to `derive`, then delete `computed.go`/`project.go` — NOT YET.**
  This is a substantial sub-project of its own, not a tail of 3a, because the `derive` slot as it
  exists today **cannot yet do a projection**: the luahook `Ctx` (`internal/agentcfg/luahook`)
  exposes `config`/`stage`/`managed`/`agent`/`surface` but **not the live tables**
  (`ctx.mcp_servers`, `ctx.lsp_servers`) or the `ctx.tombstone` sentinel that the §3.3 example
  needs. So 3b is: (1) extend the luahook `Ctx` + VM to expose the live tables + tombstone,
  (2) run `derive` to PRODUCE the computed layer (it feeds `Inputs.Computed`, so no merge-order
  change), (3) write the five packs' `derive.lua` (agy/mcp, copilot/mcp, copilot/lsp,
  codex/config, opencode/config projections + claude/settings tombstone+flags), (4) delete
  `computed.go`/`project.go` and the `Surface.Computed`/`SurfaceDTO.Computed` fields, (5) byte-equal
  regression-gate every shipped surface. Its own focused change; sequenced next.

The load-bearing decision, built last of the core phases because it leans on Phase 2's assembler
and the `derive` slot must be solid first.

1. **Name the canonical `mcp_server` type in core** — `name → {command, args, env}`, **open and
   additively-versioned** (OQ1): new optional fields (`url`, `transport`) never break a projection
   that does not read them. This is a core domain noun (`§4.1`), not a tool concept.
2. **Port the four shipped projections to `derive` functions** — codex/opencode/claude/gemini's MCP
   reshapes (`agent_configs.go:131` etc.) become per-agent-pack `derive` functions over the
   canonical entry. **This is also the acceptance test** (OQ1): opencode's rename+fold+inject must
   stay a few obvious lines of Lua. If any needs more than a sandboxed pure function, that is the
   signal for the subprocess projector (`§3.4`) — *not* an op DSL.
3. **Typed exports/imports** (`packs-and-the-prism.md §2.6`): a pack `exports: {mcp_servers}`, agent
   packs `import` the type with their `derive` projection, never naming the producer. The server
   *instances* are config/pack data; the *type + sandbox* are core; the *projection* is the agent
   pack's.
4. **Delete the dead reshape code** — once projections are `derive`, `internal/agentcfg/project`
   (`Copy`/`Fold`/`Inject`) and the `computed[]`/`Flag` DSL (`§3.3`) come out. This is the "24 keys,
   both DSLs gone" payoff, and it only lands once (2) proves `derive` covers every shipped case.

**Done when:** all four agents render their MCP config from the shared canonical type via `derive`;
`internal/agentcfg/project` and the `computed`/`Flag` DSLs are deleted; the per-agent surfaces are
byte-identical to before (regression gate). Nested-jail verified.

---

## Phase 4 — Migrate the manifest to `contributes[]` (the schema rewrite)

The visible format change (`§3.1`). Deliberately last: everything above works over today's fields
via the Phase-1 shim, so this is a *representation* swap with the semantics already proven.

1. **Parse `contributes[]`** into the kind registry, each entry validated against its kind's schema.
2. **Delete the nine effect fields** (`mounts`/`writableDirs`/`sharedDirs`/`hostFiles`/`install`/
   `surfaces`/`launchFlags`/`flagAliases`/`hooks`/`retireMiseTools`) and the magic-string dispatch
   (`isBriefingMount`, `from != "skills"` — `§1.2`).
3. **Rewrite the six shipped `pack.json` files** to `contributes[]`.
4. **`retireMiseTools`/`RetireOnFirstRender`** (OQ11): eject to a one-shot host migration rather than
   carry a cleanup kind — decide and do it here since the manifest is being rewritten anyway.

**Done when:** all six shipped packs use `contributes[]`, the magic-string dispatch sites are
deleted (not relocated), and `go list ./...` + nested-jail launch of each agent is green. This is a
breaking format change with **no migration path** (design scope) — third-party packs re-author.

---

## Sequencing and dependencies

```
Phase 0 (registry) ──► Phase 1 (footprint) ──► Phase 2 (provenance + config-overlay)
                                    │                        │
                                    └────────────────────────┴──► Phase 3 (MCP + derive, delete DSLs)
                                                                          │
                                                                          └──► Phase 4 (contributes[] rewrite)
```

- **0 → 1**: footprint needs the registry.
- **1 → 2**: provenance is a richer footprint; the collision pass is its skeleton.
- **2 → 3**: MCP projections write through the assembler; provenance must record derive output.
- **3 → 4**: the schema rewrite is safe only once semantics are proven over the old fields.

Phases 1–3 each ship standing on their own and improve the product without the format rewrite;
Phase 4 is the one irreversible step and comes last on purpose.

## Open questions that land inside a phase (not before)

- **OQ7** (collision severity) → Phase 1, step 2.
- **OQ4** (provenance format / assembler generality) → Phase 2, step 2.
- **OQ9** (`derive` per-surface vs per-pack) → Phase 3, step 2.
- **OQ3** (footprint in description hash) → after Phase 1 (a consumer of the footprint; own slice).
- **OQ5, OQ6, OQ8, OQ11** → as noted in the table; none blocks a phase from starting.

## What this plan explicitly does NOT do

- **No `guest`/`host` per-notch work** (OQ8) — the confinement matrix is
  [yolo-as-environment-manager.md](../design/yolo-as-environment-manager.md)'s concern; this plan is
  jail-only. The kind registry leaves room for the per-notch column, but Phase 1–4 render at `jail`.
- **No tier-2 effect Lua** (OQ10, decided no) — hooks stay the closed set until a real second case.
- **No general assembler for non-config kinds yet** (OQ4) — provenance ships for config (Phase 2);
  unifying skills/briefing into the same module waits for the first shared file that is neither.
