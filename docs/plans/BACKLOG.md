---
title: "Backlog — the one implementable list"
status: in-review
stage: DESIGN
next: "Rule E5: the 2026-10-01 survey found no managed or defaults array that needs an append, and the guarded posture's emptied additionalDirectories was fixed without a ruling, so the one call left is whether E5 closes"
tags: [backlog, packs, composed-config]
---

# Backlog — the one implementable list

**Status:** The single entry point for *what to build next* on the composed-config /
packs cluster, and **seven questions are live** in its stages. Created 2026-07-26; **restamped
2026-08-23** (header + Stage E + Stage G); **re-checked 2026-09-24** against the tree and the
designs it points at (E5's trigger fired, `file:line` anchors replaced by symbols).

**Added 2026-10-16:** [Ideas from deleted docs](#ideas-from-deleted-docs), the deferred ideas
that docs deleted that day held and no live doc tracked.

**Needs your ruling:** [E1](#E1), [E2](#E2), [E5](#E5), [OQ-CO](#OQ-CO), [OQ-S4](#OQ-S4), [OQ-E4](#OQ-E4).

**Why this exists.** The design work produced 8 docs / ~4,800 lines, and the actionable items
ended up spread across three of them. This file is the only place that answers *"what do I
pick up?"* Everything else is reasoning, and is linked per item.

**Rule:** an item lives here once. Reasoning lives in the design doc. Sequencing lives in
[roadmap.md](roadmap.md). Nothing gets three homes.

**Corollary added 2026-08-23:** an open *question* lives in a doc too — with stakes, a
leaning, and an empty `**Answer:**` — and [`roadmap.md`](roadmap.md) links to it by ID rather than restating
it. Where a question has no design doc of its own, **Stage E below is that doc.**

**Current state (2026-08-23).** Stages A–F are complete, and the pack-declaration reform
that produced today's `contributes[]` design is shipped (see
[../reference/pack-system.md](../reference/pack-system.md)).

**Stage G is no longer this file's problem, and its headline bug is fixed.** The old header
here said *"Stage G — host-side composition — is the one substantive open pack-adjacent
stage, and G1 within it is a live data-loss bug"*; both halves of that are now stale. Stage G
moved into [environment-manager-plan.md](environment-manager-plan.md) on 2026-07-31, and that
plan's build-status table is the authority for which phases are whole. This file does not restate
its tally: the restated one went stale twice. Two of the plan's "partial" residues have since shipped,
and the plan's table has said so since 2026-09-24: 5.3's `yolo config promote` (2026-09-12) and 6.4's
offer-to-run (the confirm-gated install in `yolo host apply --assert`, 2026-09-12).
Spot-checked against the tree, re-checked 2026-09-24:

- **G1/G2 (Phase 0) is FIXED** — `refuseHostSideWrite` (`internal/cli/configdiff.go`)
  refuses host-side `config reset`/`capture` without `--force`; `configReset` and
  `configCapture` both call it.
- **G4/G5/G3 (Phase 1) shipped** — `internal/render/` exists with `target.go`, `fieldset.go`,
  `modes.go`, `confinement.go`.
- **Phase 2 shipped** — the `confinement` key in `internal/config/confinement.go`.
- **G6 (Phase 4) shipped** — as `yolo host apply` (the `apply --host` spelling was removed
  2026-08-30); `--sealed` (Phase 5) is `applySealed` in `internal/cli/apply.go`.
- **Phase 7 (the `guest` notch) is the one unbuilt phase**, and it is host/Mac-gated rather
  than design-blocked: the `KindGuest` row in `internal/render/modes.go` still answers
  `UndecidedModes(...)`, whose reason string names Phase 7 as the one to state the mode policy.

**So the one substantive open stage left in this file is Stage E**, which as of today holds
**seven open questions** — the parked `host_files` follow-ups plus four questions
(`S5`, `OQ-CO`, [`OQ-S4`](#OQ-S4), [`OQ-E4`](#OQ-E4)) that until now had no doc home anywhere and were being
restated in [roadmap.md](roadmap.md) instead of linked from it.

---

## Where the reasoning lives

| Doc | Answers | Read it when |
|---|---|---|
| [../reference/composed-file-permissions.md](../reference/composed-file-permissions.md) | ro/rw postures, the Derived/Shared/State taxonomy, the defect audit, writer classes | touching any composed file's permissions or the capture overlay |
| [../reference/pack-system.md](../reference/pack-system.md) | **the pack system, whole**: the `contributes[]` manifest, every kind + footprints + conflict rules (the set is pinned by `packdecl/kinds_test.go`; this row stopped carrying a count after two went stale), the one-writer rule, the compose engine, `derive`, and selection/fetch/origin-gate | authoring, debugging, or changing a pack; changing the schema or a kind |
| [../design/environment-manager-user-stories.md](../design/environment-manager-user-stories.md) | the same design **from the outside**: five worked stories (drift across two machines, a Mac fleet rollout that hits G3 then G1, the minimal-ladder user, the agent reading its own briefing at `host`, a security questionnaire) — plus the open questions the stories surfaced (Q1 · Q1a · Q1b · Q2–Q9) | pressure-testing a verb's output, or deciding what `apply`/`describe` must print |
| [../design/host-render-target.md](../design/host-render-target.md) | **the host as a reduced render target**: the two duplicated render paths and the one `Target`-parameterized renderer that replaces them, which manifest fields even apply off-container, the confinement axis (jail / macos-user / host), `FieldSet` | adding a backend, touching host-side `config reset`/`capture`, or changing the boot render |
| [../reference/what-yolo-is.md](../reference/what-yolo-is.md) | **what yolo is**: confinement as a `jail`/`guest`/`host` dial rather than a `runtime` backend choice, the description as the product, the verbs (`apply`/`apply --sealed`/`describe`, deliberately no top-level `diff`, and the unbuilt `check --at`), what a pack means per notch, and what the wider identity costs; subsystem boundaries; where composition could run; how logic ships | deciding whether a feature is jail-shaped or *where* something executes, adding a config key, or writing user-facing copy |
| [../reference/third-party-pack-logic.md](../reference/third-party-pack-logic.md) | the projector protocol; build/source tiers; trust | implementing pack logic |

---

## Stage A — prism prerequisites — ✅ COMPLETE (2026-07-27)

All of stage A plus F1 has landed. Each item was verified in a real nested jail, not
only by unit test.

Nothing in this stage needs a decision. All of it is prerequisite to packs, so doing packs
first means porting known defects into a new mechanism.

| # | Item | Kind | Size |
|---|---|---|---|
| ✅ A1 | Remove the `gemini` agent | subtractive | medium (~8 files) |
| ✅ A2 | Reserve symlink targets (`~/.config/git/config`, `~/.claude/claude.json`) | defect | small |
| ✅ A3 | Stop `config render claude` composing the `claude/config` surface. **Narrowed:** it is *already* correctly labeled `unrendered` in `prismSurfaceMode` (`configls.go:62`), so `ls`/`diff`/`reset` skip it properly — only the `render` path still composes a file the jail never writes | defect | small |
| ✅ A4 | Fix `writeInPlaceString`'s umask claim | defect (latent) | small |
| ✅ A5 | Make `~/.gitconfig`'s unwritability legible | defect | small |
| ✅ A6 | Fix `config-ref`'s `reset`-re-seeds-`once` promise | docs lie | small |
| ✅ A7 | Feed `config render` the overlay + computed layers | defect | small–medium |
| ✅ A8 | Give `pi`/`codex`/`opencode` a skills dir | defect | **two lines** |
| ✅ A9 | Wire `Surface.Transform` — a documented key that does nothing | defect | small |
| ✅ A10 | Steer directed agents at composed surfaces (skills + header) | improvement | docs-only |
| ✅ A11 | Parameterize `/workspace` out of `builtin.go` | blocker for packs | small |
| ✅ A12 | **Make generator failures fatal** — `genStep` (`boot.go:532-537`) prints a warning and discards the error, so a failed config step still yields a running jail with a misconfigured agent. Ruled 2026-07-26: loud and halting. **28 call sites across TWO files: 19 in `boot.go` + 9 in `darwin.go`** (the earlier "20" counted only `boot.go` — the macos-user path has its own loop and was missed). Requires separating genuine failures from absent-optional-input first | **policy inversion** | medium |
| ✅ A13 | **A user-scope `config.lua` never reaches any jail** — `prism.go:65` reads `$HOME/.config/yolo-jail/config.lua`, but `userConfigMountArgs` (`cli/run/assemble_parts.go:462-474`) mounts only `filepath.Base(UserConfigPath())` = `config.jsonc`. Verified live: that dir contains only `config.jsonc` and mountinfo shows one bind. So the user half of the documented user-then-workspace transform pair is dead; only `<workspace>/yolo-jail.config.lua` works (it rides the `/workspace` bind). One extra `ROFileMountArg` | defect (**documented feature has no channel**) | small |

**Do A1 first** (subtractive — shrinks every later table, and deletes one of the five
projections A-stage work must satisfy). **Do A8 + A10 together** (A10's guidance is worthless
to three agents until A8 lands). **A9 and A11 are the two that gate pack work**: A9 because
the per-surface Lua seam has never executed and pack projections would be built on it, A11
because surface data can't become pack data while it hardcodes the jail path.

## Stage B — the data-loss chain — ✅ COMPLETE (2026-07-27)

**Gate RESOLVED 2026-07-26:** `reset` also truncates the surface file to the pure render, so
nothing is left to adopt and first-migration can safely adopt the on-disk file.
**Stage B is fully unblocked.** Detail: [Ruling 1](../reference/config-migration-to-prism.md#ruling-1).

| # | Item | Kind |
|---|---|---|
| ✅ B1 | Adopt-on-first-migration (one branch in `staterender.go`) | ⚠ data-loss fix |
| ✅ B2 | De-compose the credential surfaces onto read-modify-write | ⚠ data-loss fix |
| ✅ B3 | Separate durable overlay state from the one-boot `last_render` baseline | naming |
| ✅ B4 | Sidecar location / scope — **resolved**: stays per-workspace (that is already where state lives) | improvement |
| ✅ B5 | **Generalize shared-across-jail state as a pack-declared field** — today `.claude-shared-credentials` is the only machine-wide dir and it is hardcoded (`assemble.go:173-176`). Prerequisite for agents-as-packs | new, ruled 2026-07-26 |

**B2 is load-bearing for packs**: it is also the third engine mechanism
(`read_modify_write`) that agents-as-packs needs, so it earns its place twice.

## Stage C — the pack foundation — ✅ COMPLETE (2026-07-27)

All built and verified against real containers and real git. C7 proved unnecessary
(see C6). Launch is offline: verified by moving a source repo away and still
launching a jail with its pack delivered. *(Since 2026-09-25 a launch fetches a pack the store
lacks and re-fetches a branch hourly, by
[`OQ-PF1`](../reference/pack-system.md#oq-pf1). What this measured still holds: a failed fetch
of a pack already in the store warns and launches from the stored copy.)*

| # | Item | Notes |
|---|---|---|
| ✅ C1 | `packs` config key, user scope only, `file://` sources | no workspace scope — settled |
| ✅ C2 | Tree executor: walk, `only`/`exclude`, exec-bit refusal, copy | |
| ✅ C3 | `PrepareSkills` + `ComposeBriefing` packs pass | delivers A8/A10's value via packs |
| ✅ C4 | `yolo pack init\|lint\|ls\|explain` | authoring loop |
| ✅ C5 | Git sources: `internal/packsrc`, lockfile, approval, `add/install/update/rollback` | the ~1-week chunk |
| ✅ C6 | Port the 5 real projections to the declarative operation set | **validates the design** — do before freezing the op set |
| ⏸ C7 | The projector protocol (tier 2 escape hatch) | **not needed** — C6 proved the op set sufficient. Stays designed, unbuilt |

**C6 — ANSWERED 2026-07-27, and the answer is yes.** All three surviving projections
(codex MCP, opencode MCP, copilot LSP) are expressible in four data ops: copy (with rename
+ omitEmpty), fold, inject, default. Each is pinned by a test transcribed from the Go
builder it replaces. `conditional-OMIT` vs `tombstone-null` is handled as two separate
operations, because a null leaf is an RFC-7386 tombstone that DELETES downstream. The
cross-type LSP→MCP derivation the docs called hardest **died with A1** (it was gemini's).

**Consequence: C7 is not required.** The subprocess projector stays designed and unbuilt.

## Stage D — the rip-out — ✅ COMPLETE (2026-07-27)

**THE TRANSITION IS DONE.** Not just the mechanisms: the agent registry is deleted, all
six agents ship as `packs/*/pack.json`, and core contains no switch on any tool name.
What landed beyond what the table below anticipated:

- **`configureAgent`'s six-way switch is gone**, replaced by one loop over pack-declared
  surfaces (`entrypoint/packsurfaces.go`). Three new mechanisms were needed to get the
  per-agent data out of Go: declarative `computed` (which live table, which reshape, plus
  `flags` for conditional key assertions), `reconcile` (an RMW surface's key as a managed
  dynamic table — the gap the RMW docstring described), and `hooks` (the imperative
  residue, as a named capability a pack REQUESTS rather than code it ships).
- **Embedded packs are OPT-IN by bare name** (`packs: ["claude"]`), nothing on by default.
  This was a correction: activating six packs unconditionally while the launch warning said
  "no packs are configured, so this jail has no coding agent" was a contradiction a user
  could only find by looking in `~/.yolo-shims`.
- **The MOUNT is the filter.** The entrypoint renders every pack under `YOLO_PACK_ROOT`,
  so only selected packs are copied into the mounted tree, which is cleared each launch.
- **The ten Go surface literals were proved byte-equal** to the generated pack declarations
  before deletion; `BuiltinManifest()` keeps only `mise/config`.

Three bugs came from real nested jails rather than unit tests, and are now regression-
tested: the reconcile sidecar written into the `:ro` home root, a hook symlinking into a
directory nothing had created, and `yolo check` parsing `embedded:claude` as a fetchable
address.

**D1 is RESOLVED and mostly deleted.** Composition **stays in the container**; only
image-build inputs and host-file reads run host-side. There is no port. See
[Rulings 3 and 4](../reference/pack-system.md#open-rulings-3).

| # | Item | Gate |
|---|---|---|
| ✅ D1 | ~~Decide where composition runs~~ → **replaced by: host-side *validation* of pack contributions** at `yolo check` + run assembly, so a bad pack is caught before the container starts. Precedent: `checkHostFileLayer`/`checkHostFileDest` | defense in depth, now that A12 makes failures fatal |
| ✅ D2 | Three engine mechanisms: `stateful`, `computed`, `read_modify_write` | needs B2 |
| ✅ D3 | Agent registry + surfaces + skills + briefings become official packs | **DONE.** The registry is deleted (`internal/agents` keeps only skills staging, briefing composition, loopholes, the source-tree probe). All six agents are `packs/*/pack.json`, generated from the Go literals and proved byte-equal before those were removed |
| ✅ D4 | `AgentSpec.HostFiles` becomes pack data | **DONE.** `packdecl.HostFile` entries, gated by `PackEntry.MayGrantHostFiles()` on content ORIGIN — embedded and local may grant, fetched never. `packload.CtxPath` is the ONE definition of the `/ctx` destination, shared by the CLI that mounts it and the entrypoint that reads it; two copies would have silently composed the wrong user file |
| ✅ D5 | No agent by default | already works — `agents: []` boots (verified) |
| ✅ D6 | Make the MCP bootstrap a pack contribution | it currently installs 112 npm packages for zero agents |
| ⏸ D7 | Stage a third-party projector binary into the jail (compose runs in-jail, so it must be reachable there) | needs C7 |

## Stage F — findings from the verification pass — ✅ ALL ADDRESSED (2026-07-27)

An adversarial review checked every `file:line` in the design docs. These are the claims that
were **refuted and re-verified by hand**, plus the defects it surfaced. Each is real work.

| # | Item | Kind |
|---|---|---|
| ✅ F1 | **⚠ A workspace config controls agent selection, and therefore which host files mount.** `agents` is in `overrideListKeys` (`config/load.go:86`) so a workspace value *replaces* the user's wholesale — probed: user config selecting `[claude, pi, codex, agy]` became `[claude]` from a workspace `yolo-jail.jsonc`. Since `hostFileArgs` mounts each selected agent's `AgentSpec.HostFiles`, a repo-committed, agent-editable file decides a credential-boundary question. **This is the same threat `a84b11c` closed for `host_files`, still open via `agents`.** Under packs it gets worse if the enable list is the pack list | **security** |
| ✅ F2 | The credential-boundary field set is **`{HostFiles, Briefing.HostSource, Skills}`**, not just `HostFiles`. `BriefingSpec.HostSource` reads a host-home path every run (`agents/agentsmd.go:239-245`), and `Skills` is the *widest* — a recursive, symlink-dereferencing tree copy (`agents/skills.go:72-138`). Any pack-declared grant spec must cover all three | **security** |
| ✅ F3 | **Lua map iteration is nondeterministic** — but the cause is `goToLua`'s map branch (`luahook/marshal.go:78`), not `pairs()`. Fix by iterating keys in sorted order there (~3 lines), which fixes every hook rather than adding an author-facing rule | defect |
| ✅ F4 | **Any Lua hook converts TOML integers to floats** (`8192` → `8192.0`), because `luaToGo` returns `float64` for every `LNumber` (`marshal.go:99-100`). Fix at the marshalling boundary, **not** in the TOML emitter | defect |
| ✅ F5 | `prismSurfaceMode` (`cli/configls.go:50-63`) is a **fourth** hand-maintained surface table, pinned only by a test. Under packs the mode belongs in the manifest | duplication |
| ✅ F6 (premise corrected) | `agents.AllOverlayDirs`/`AllMiseRetire` are package-level initializers over **all** specs, not the selected set (`agents.go:238-259`) — so an empty default agent list does *not* shrink the reserved namespace. D5 needs these five call sites converted separately | correctness |
| | **RE-EXAMINED 2026-07-27 and the premise does NOT hold.** Those unions are RESERVATION lists — what a `host_files`/`writable_home_dirs` entry may not claim. Selection-gating them would make the same committed config validate in a jail that selects claude and fail in one that does not, so a repo's config would be portable only by accident. `storage.EnsureGlobalStorage` is all-agents for a related reason: it pre-creates GlobalHome MOUNTPOINTS, and the OCI runtime cannot mkdir inside a `:ro` bind. Pinned with a test naming the reasoning so the "fix" is not applied later | — |
| ✅ F7 (scoped) | RMW as an engine mode needs **three** things, not one mode field: a declared asserted-key set (a superset of today's `Managed` — `mcpServers` must be declarable), a durable record of what yolo asserted last boot to express *removals*, and a `config reset` story (reset is currently *defined over* capture surfaces, `configdiff.go:222`, and hard-errors otherwise) | design |
| ✅ F8 | `/ctx/<pack>` mountpoints need **no flake edit** — podman creates them on demand under `--read-only` (probed: `/ctx/host-pi` exists with a live mount). Drops a constraint from the pack-staging design | simplification |

**Doc corrections to fold in:** the "2,207 lines of per-agent logic" figure **counts test files** —
non-test `prism*.go` is **917 lines** (verified). And `claude/config` is **correctly** labeled
`unrendered` (`configls.go:62`), so item A3's framing needs narrowing to the `render`-only half.

## Stage G — host-side composition (found 2026-07-27) — **now sequenced in the env-manager plan**

Reasoning: [../design/host-render-target.md](../design/host-render-target.md) — [§3](../design/host-render-target.md#3-the-design-inside-yolo) is the
design (one renderer, several targets), [§6](../design/host-render-target.md#6-the-host-as-a-reduced-target) is the finding these items came from.

**Moved 2026-07-31.** Stage G is the host-render slice of the wider environment-manager
vision, and its six items now live — with their full evidence, the byte-equality gate, and
the build order — as the first phases of
[environment-manager-plan.md](environment-manager-plan.md), which sequences the whole vision
foundation-first. Kept here as the map from the old G-numbers to the plan phases (per this
file's "an item lives once" rule — the item now lives in the plan):

| G-item | What | Now |
|---|---|---|
| G1 | ⚠ host-side `config reset` destroys real user config (data-loss) | **Phase 0.1** |
| G2 | `config capture` leaks host config into the workspace (privacy) | **Phase 0.2** |
| G3 | macos-user renders zero pack surfaces, silently | **Phase 1.4** |
| G4 | collapse the two render paths into one `internal/render` + `Target` | **Phase 1.1–1.2** |
| G5 | `FieldSet` — refuse an inapplicable kind by name, not silently | **Phase 1.3** |
| G6 | `yolo config apply --host` — render the applicable subset into the real home | **Phase 4** |

**And all six have SHIPPED — restamped 2026-08-23.** The plan's own build status records
Phases 0–3 and 9 as shipped 2026-08-01, with 4, 5, 6 and 8 partial in ways that do not touch the
G-items; the spot-checks are in this file's header. The
sentence that used to sit here (*"G1 (Phase 0) is the one to do first, and it waits on
nothing"*) was true when it was written and is now history — G1/G2 are fixed by
`refuseHostSideWrite` in `internal/cli/configdiff.go`. Original order G1 → G2 → G4 → G3 → G5 → G6 was preserved
as Phase 0 → 1 → 4. **Extracting any of this into a separate util
is settled: no** (`host-render-target.md` [§2.3](../design/host-render-target.md#23-extraction-settled-and-the-answer-is-no), decided 2026-07-27) — the field census puts the
boundary through the middle of a single manifest, so the capability lives in yolo as
`internal/render`.

## Stage E — parked design work, and the questions with no other home

**Restamped 2026-08-23.** This stage is now two things at once: the parked `host_files`
follow-ups (E1–E5) it always held, and **four questions that had no design-doc home anywhere**
— `S5`, `OQ-CO`, [`OQ-S4`](#OQ-S4), [`OQ-E4`](#OQ-E4). Those four were carried as a single line in
[roadmap.md](roadmap.md) ("the small ones with no design-doc home"), which breaks that file's
own governing rule: *a question lives in its design doc, with stakes and a leaning, and the
roadmap links to it by ID*. They live here now, in full, so the roadmap can cite them and stop
restating them. **This section is their doc.**

> [!IMPORTANT]
> **IDs are an API — do not renumber these into the E-series.** `S5`, `OQ-CO`, [`OQ-S4`](#OQ-S4) and
> [`OQ-E4`](#OQ-E4) keep the exact spellings they were born with in the deleted `outstanding-work.md`
> (last intact at commit `58ae8227`, 2026-08-13). They are cited today by
> [`pack-system.md`](../reference/pack-system.md#skills-fanout-s4) (the S4 audit) and by
> `internal/cli/run/packskillsdelivery_test.go`. `S5` in particular is an S-series pack ID,
> not an E-series backlog ID, and re-spelling it would silently orphan those references.

**Index.** The 💬 glyph lives on the section heading below, once per question, so
`rg -n '^### 💬' docs/plans/BACKLOG.md` counts this stage exactly — **seven open**. This table
carries none, deliberately.

| ID | Question | State |
|---|---|---|
| **E1** | `host_files` modes 4→3 (`copy` merges into `readonly`) | **open** — one decision with E2 + OQ-B |
| **E2** | `readonly` as a real `:ro` mount instead of `0o444` | **open** — one decision with E1 + OQ-B |
| ✅ E3 | Capture timing | **SHIPPED 2026-08-15** — both halves. See below |
| ✅ E4 | Comment preservation on `json`/`toml` surfaces | **mostly shipped 2026-08-12**; the one live residue is [`OQ-E4`](#OQ-E4) |
| **E5** | `managed`/`defaults` array-append pinning | **open** — surveyed 2026-10-01: no surface needs the append, so the call is whether E5 closes |
| ✅ S5 | A jail resolves a skill-name collision silently | **ANSWERED 2026-09-28 by [OQ-NC11](notch-convergence.md#7-decision-ledger)** — a launch refusal, built. See below |
| **OQ-CO** | Two packs writing one `config-overlay` key | **open** — nothing blocked; no shipped pack collides |
| **OQ-S4** | Should the jail narrow its skills fan-out to match the host? | **open** — a product call about what `into` promises |
| **OQ-E4** | Do `stateful` surfaces get comment preservation too? | **open** — `rmw` shipped, `computed`/`json` ruled out |

---

### <a id="E1"></a>💬 **E1 — collapse `host_files` modes 4→3 (`copy` merges into `readonly`)**

Also **E2** and **[`pack-host-management-plan.md`](pack-host-management-plan.md) OQ-B**: *these three are one decision.* See
the shared block below.

`host_files` still accepts four modes — `readonly`, `once`, `copy`, `capture`
(the `HostFileMode*` constants in `internal/config/hostfiles.go`, re-checked 2026-09-24). E1 asks whether `copy` earns its
slot: `copy` overwrites the file every boot at `0o644` and loses an in-jail edit silently,
while `readonly` re-renders every boot at `0o444` so the edit fails loudly *at the moment of
the edit*. If E2 makes `readonly` a real `:ro` mount, the two modes stop differing in what a
boot does and differ only in whether the failure is loud — which may not be worth a mode.

**Stakes:** this is a behavior change on a **shipped config key**, so every existing
`mode: "copy"` entry changes meaning on upgrade. It is also the only one of the three that
can be decided *wrong* cheaply — the mode list is documented in `internal/cli/config_ref.txt`
and nothing else keys on the count.

_Leaning:_ **reject E1, and keep four modes.** `copy` and `readonly` answer *different*
questions — not "what does a boot do" (where they agree) but "what happens when the agent
writes", where silent-loss and loud-failure are genuinely distinct products. That distinction
is exactly what a mode name is for. **What would change my read:** E2 landing as a real `:ro`
mount, which makes `readonly` kernel-enforced and therefore no longer a "loud failure" mode
but an impossible-write mode — at which point `copy` becomes the only writable-and-clobbered
mode and the case for merging them collapses.

**Answer:**
> _(empty — fill in when decided)_

### <a id="E2"></a>💬 **E2 — `readonly` as a real `:ro` mount instead of `0o444`**

**This is the same underlying decision as E1 and as `OQ-B` in
[pack-host-management-plan.md](pack-host-management-plan.md#open-questions) (still open 2026-09-24). Decide all three
together or none of them** — OQ-B's own leaning already says so.

The asymmetry, stated once: **`0o444` is a posture; `:ro` is enforcement.** `0o444` is DAC
only, and yolo's own docs already admit it — `internal/cli/config_ref.txt`: *"Note 0444 is
DAC, not kernel enforcement: an agent running as root (Claude YOLO runs UID 0) bypasses the
mode bits"*, and anyone can `chmod +w`. Three places in the tree make the same trade today
(all verified 2026-08-23):

| Site | What it does | Which ID |
|---|---|---|
| `internal/entrypoint/hostfiles.go` | `host_files` `readonly` renders `0o444` (`0o555` if the source is executable) every boot | **E2** |
| `internal/config/hostfiles.go` | the four-mode vocabulary that names it | **E1** |
| `internal/entrypoint/hostfilestree.go` | pack-delivered `files` at the host notch: *"READ-ONLY (0o444/0o555) mirrors the jail's `:ro` mount, which is the closest a plain filesystem gets to the same statement"* | **OQ-B** |

`hostfilestree.go` carries the asymmetry in one line: the writer has to **chmod a `0o444` file
back to writable to reopen it**, which is precisely the move a user who wants to hand-edit
will make — and then lose on the next apply.

**Stakes.** The blocker is structural, not effort: **you cannot compose *into* a `:ro`
mount.** Making `readonly` a real mount means the composed output has to be produced in a
staging tree and bound in, which changes the delivery path for every `readonly` entry and
re-opens how `packstage` and `hostfilestree` deliver. Against that: `0o444` makes a promise
the product does not keep for the one agent that matters — Claude YOLO runs UID 0 and walks
straight through it.

_Leaning:_ **keep `0o444` everywhere and fix the WORDS instead.** Rename the posture from
"read-only" to what it is (asymmetric / advisory), which `config_ref.txt` already says in
a note nobody reads at the point of decision. The enforcement upgrade is real work on a live
boot path and buys nothing against the threat model that actually applies (a root agent), so
it should wait for a threat it defeats. **What would change my read:** the `guest` notch
(env-manager Phase 7) landing with a non-root agent, where DAC bits *are* enforcement.

**Answer:**
> _(empty — fill in when decided)_

### ✅ **E3 — capture timing. SHIPPED 2026-08-15, both halves.**

**This row was stale and is now closed.** The old entry said *"open, not urgent — nothing is
lost today, only observability lags"*. Verified against the tree 2026-08-23:

- `yolo config capture` (the on-demand half) — `configCapture`, `internal/cli/configdiff.go`.
- **Capture on terminate** (the half the entry was actually about) —
  `internal/cli/configcapture.go`, whose opening line is literally *"configcapture.go is E3's
  second half: capture on TERMINATE"*. Shipped in commit `29ccf212`, 2026-08-15,
  *"feat(config): capture in-jail config edits when a jail terminates (E3)"*.
- **It is wired, not orphaned** — `internal/cli/commands.go` injects it as
  `run.Options.CaptureOnTerminate`, with the comment naming E3.
- It ships **unconditional, not opt-in**, and `configcapture.go`'s header records the four-point
  argument for that (idempotent with the boot capture; only touches `mode: capture` surfaces;
  the gap is a default-correctness property of a *reporting* command; and BACKLOG's own
  "nothing is lost today" is the reason every failure warns and proceeds rather than a reason
  to gate it).

Nothing left to decide. Kept as a row rather than deleted because the E-numbers are cited
elsewhere.

### ✅ **E4 — comment preservation. Mostly shipped 2026-08-12; the residue is [`OQ-E4`](#OQ-E4).**

**Why `E4` and [`OQ-E4`](#OQ-E4) are different items.** They are not the same
item. `E4` was "comment preservation on `json`/`toml` surfaces" across all modes; three of its
four cases are now closed, and the fourth was promoted to its own question ID:

| case | ruling | evidence (verified 2026-08-23) |
|---|---|---|
| `rmw` | **preserve** — shipped | `internal/entrypoint/tomltrivia.go`, whose header reads *"tomltrivia.go is E4's `rmw` half"*; drops are reported by key via `HostRenderResult.Formatting` (`internal/entrypoint/hostrender.go`) |
| `computed` | **do not preserve, and that is correct** | yolo is sole author, so any comment would be one *yolo wrote* — a different feature |
| `json` (any mode) | **provably vacuous** | strict JSON has no comment syntax, so a commented file never decodes and the RMW path refuses it byte-untouched; now pinned by a test |
| `stateful` | **still open** → **[`OQ-E4`](#OQ-E4)** | see below |

Full argument: the retired `host-file-staging.md` §*"What shipped: option 3, for
`rmw` only"* (`git log -- docs/plans/host-file-staging.md`; the shipped rule is [the four modes](../reference/pack-system.md#the-four-modes)), and the retired `shipped-2026-08-12.md` §E4 (`git log -- docs/plans/shipped-2026-08-12.md`).

### <a id="E5"></a>💬 **E5 — `managed`/`defaults` array-append pinning**

`managed`/`defaults` merge is RFC-7386 object merge at every depth
(`internal/agentcfg/engine.go`, re-checked 2026-09-24), which means **an array in a
`managed` layer REPLACES the on-disk array wholesale**. So a pack that wants to *add one
entry* to a list the user also maintains cannot: it either clobbers the user's whole list or
leaves the key alone. Settled as deferred at implementation time —
the retired `host-file-staging.md` (`git log -- docs/plans/host-file-staging.md`), *"Resolved during implementation"*: *"Array-append
pinning was deferred: no user surface has needed it. Still open if one does."*

**Stakes:** low today and self-limiting. The trigger is named and has not fired — no shipped
surface needs it. The risk is in the *fix*, not the gap: changing `mergeValue` globally would
alter every shipped surface's render and move `TestRenderFingerprintStable`.

_Leaning:_ **do not build it speculatively.** When something needs it, the shape is a per-key
merge-strategy annotation on the `managed` declaration (opt-in, one surface at a time), never
a change to the engine's default merge. Recording that shape now is the whole value of leaving
this row open.

_Re-checked 2026-09-24 — the trigger fired, and the need was met in a different shape._ The
personal-overlay case (adding one Pi package without copying the `pi` pack's whole `packages`
list) is exactly "a pack that wants to add one entry to a list the user also maintains", and it
was built as the **`config-list` kind** on 2026-09-24
([`pack-system.md`](../reference/pack-system.md#adding-entries-to-an-array-config-list), both
questions ruled 2026-09-23). It kept this leaning's two constraints — opt-in per path, and no change to the
engine's default merge — but it is a **new contribution kind folded below capture, `computed`
and `managed`**, not an annotation on `managed`/`defaults`, and a `managed` array still replaces
wholesale. That design does not mention E5 and rules nothing about the `managed` layer. So what is
left of E5 is narrower: **does any surface need a `managed` or `defaults` array to append, now that
`config-list` covers a pack adding entries?** If not, E5 closes.

_Surveyed 2026-10-01, at `d4e435a3`: no surface needs a `managed` or `defaults` append._ Every
array a shipped pack puts in either layer, its autonomy postures included, is one of these (READ
from `packs/*/pack.json`, by walking every `managed` and `defaults` object):

| Pack, surface, layer | Array | What it needs |
| :--- | :--- | :--- |
| `claude/settings`, autonomous posture, `managed` | `permissions.additionalDirectories: ["/"]`, `permissions.allow: []`, `permissions.deny: []` | a **replace**, which is the point: the autonomous posture applies in a jail, and AGENTS.md states that `allow: []` is not an allowlist mechanism |
| `agy/settings`, autonomous posture, `managed` | `trustedWorkspaces: ["/workspace"]` | a replace, for the same reason |
| `claude/settings`, guarded posture, `managed` | `permissions.additionalDirectories: []`, **removed 2026-10-01** | was the **opposite of an append**. The guarded posture is what the host notch renders, and RFC 7386 replaces the array, so every `yolo host apply` emptied the user's own `additionalDirectories` in the real `~/.claude/settings.json`, under `host_management` `assert` and `own` alike, and emptied a directory added back by hand at the next apply (MEASURED 2026-10-01 through `applyHost` in a temp home: `TestHostApplyKeepsTheUsersOwnAdditionalDirectories` and `TestHostApplyKeepsADirectoryAddedAfterAnApply` failed before the removal). Its commit, `9a7b4d88`, says the posture *"resets defaultMode/additionalDirectories/skipDangerous to prompts-on (leaving allow/deny alone — no clobber)"*: the intent was to take out the jail's `/` grant, a per-entry **removal** no declarative op makes ([OQ-LT2](../reference/pack-system.md#oq-lt2)). The removal needed no ruling. The environment-manager plan gives claude's guarded block `defaultMode` alone and asks of every guarded block *"prompts on, no allow/deny clobber"* ([§9.0 and step 9.4](environment-manager-plan.md#90-the-sketch-that-resolved-oq-11-two-encodings-vs-the-real-packs)), and no published version wrote `/` into a host file: `yolo host apply` first shipped in v0.8.0, whose guarded posture already held the empty list, so the list only ever removed the user's own entries |
| `opencode/tui`, `defaults` | `plugin: ["./yolo/footer.js"]` | an append, and `config-list` is its shape rather than this one: a `defaults` value only fills an absent key, so a user who has a `plugin` list of their own gets no yolo footer (READ, not run) |

So E5's `managed`/`defaults` subject is empty, and the survey left two facts for other homes. The
guarded posture's empty array was a removal with no declarative form, [OQ-LT2](../reference/pack-system.md#oq-lt2)'s
accepted gap, but no published state needed the removal, so the array was deleted rather than
given a new op, and [OQ-LT2](../reference/pack-system.md#oq-lt2) is left with no shipped case. `TestNoShippedHostPostureReplacesAList`
(`internal/entrypoint`) now fails on any array in a shipped pack's host posture. And opencode's
footer entry is a `config-list` candidate on its owner's own surface. What is left to decide here
is one call: whether E5 closes.

**Answer:**
> _(empty — fill in when decided)_

### <a id="S5"></a>✅ **S5 — a jail resolves a skill-name collision SILENTLY**

**Context for a reader who has never seen the roadmap.** Two selected packs can each ship a
skill directory with the same name aimed at the same destination (`~/.claude/skills/review`
from both). The two notches answer that differently, and only one of them says anything:

- **Host (`yolo host apply`) — FATAL, before anything is written.**
  `hostskills.RenderHostSkills` calls `Collisions(dests)` first and returns `CollisionError`
  with every collision named, so one run tells the user every rename to make
  (`internal/hostskills/compose.go`). This is the S1 ruling.
- **Jail — silent last-one-wins** (the state this entry was filed against, until 2026-09-28; the
  Answer below records what replaced it). `jailcontent.PrepareSkills` looped `packSkillDirs` in config
  order and `copySkillSubdirs` does `os.RemoveAll(target)` then copy
  (`internal/jailcontent/skills.go`). There is no collision concept on any jail path: every
  caller of `hostskills.Collisions` is host-side — `RenderHostSkills`, and
  `reportSkillCollisions` (`internal/cli/applyhostskills.go`). `RenderHostSkills` is reached only
  from `internal/cli/applyhostskills.go`. **Verified 2026-08-23, re-checked 2026-09-24.**

**Measured 2026-08-05**, on the same two-pack set `apply --host` refuses: the jail came up,
`~/.codex/skills/mine` held the local pack's copy, the other pack's skill was absent, and
nothing said so.

**Stakes.** Not a regression — S1's ruling is explicit that the collision is fatal *at apply
time* — but this is now the **only** place the silent loss the ruling exists to remove still
survives, and it is the notch a user is in every day. It is a decision rather than a port
because refusing to **start a jail** is much heavier than refusing to write a real home, and
A12's fatal-generator policy would make it exactly that. Three options, ascending:

1. **A warning at launch** naming both packs. Closes the "nothing said so" half; strands nobody.
2. **A `yolo check` failure.** Loud where the user is already asking; non-fatal at launch.
3. **A boot refusal.** Consistent with the host, and the one that can strand someone mid-task.

_Leaning:_ **do (1) now, regardless of what you pick for (2)/(3).** The destinations and layers
are already computed and `hostskills.Collisions` is a pure function of them, so the cheap half
is small. Against (3): A12 makes a generator failure halt the jail, so a boot refusal here is
not "consistent with the host" in cost — the host user re-runs a command, the jail user loses
a session.

*(2026-09-28: notch-convergence item 25 raises this again as
[OQ-NC11](notch-convergence.md#7-decision-ledger). That entry adds that the jail ignores `skills_tier`, and
leans toward a refusal at launch, run on the host before any container starts, rather than an A12
boot failure. Answering either question answers both.)*

**Answer:**
> **Answered 2026-09-28 by [OQ-NC11](notch-convergence.md#7-decision-ledger)** (the maintainer, ruling
> by parity: *"yes, NC as parity for sure"*), which takes neither option (1) nor option (3) as
> written: a skill-name collision is **fatal at the launch, host-side, before the container
> exists**, and on an attach too. It is a launch pre-flight beside the agent-name one, not an A12
> boot failure, so the cost this entry weighed against (3) does not apply: the user re-runs a
> command, as at the host. The warning in option (1) is superseded. The jail also composes
> through the host's layer plan now, so `skills_tier` is honored and the message's namespacing
> remedy works in a jail. **Built** ([NC-D52](notch-convergence.md#NC-D52) to
> [NC-D56](notch-convergence.md#NC-D56)).

### <a id="OQ-CO"></a>💬 **OQ-CO — two packs writing one `config-overlay` key is silent last-one-wins**

**Context.** `config-overlay` is the kind that lets pack B contribute keys to a surface pack A
owns (the `matt-fzf` → `claude/settings` `fileSuggestion` case). Overlays fold in after the
workspace layer **in pack order, later wins** (`internal/agentcfg/compose.go`), and the
kind's combine rule is `CombineOverlay`, which is **deliberately non-colliding** —
`internal/packdecl/kinds.go`, and `internal/packload/footprint.go` renders it as *"a claim line
only."* **Verified 2026-08-23, re-checked 2026-09-24.**

So two packs claiming the same *surface* is the feature. Two packs claiming the same **key**
with different values is undetected, and the loser is never told.

**What is already mitigated, and what is not.** Per-key provenance IS recorded — the winning
layer is labelled `config-overlay:<pack>` (`OverlayLayer`, `internal/agentcfg/compose.go`) and
`yolo config diff` prints it. That is **after-the-fact reporting, not a refusal**, and at the
HOST notch the annotation is *inferred rather than measured*:
[pack-config-collaboration.md §8](../reference/pack-system.md#config-surfaces-and-the-compose-engine) *"Still open after
Option 2"* measures it printing `fileSuggestion contributed by fzf-overlay but managed won`
when the overlay's value is the one that actually landed and no `managed` value existed.

**Stakes.** Nothing is blocked. No shipped pack collides — all declare disjoint identities
([§9](../reference/pack-system.md#config-surfaces-and-the-compose-engine) *"What it did NOT change"*). *(Freshness note 2026-09-02: the kind stopped being hypothetical —
`packs/zai` is the first shipped `config-overlay` contributor (`980aed71`, targeting
`claude/settings`, gated on its profile), and it is that surface's sole contributor, so the
no-collision fact still holds. `568d5a3a`'s `profile` gate changes *whether* an overlay
participates, not what happens when two active overlays share a key — last-one-wins is unchanged,
re-verified in `internal/packoverlay` which has no collision logic.)* *(2026-09-24: the new
`config-list` kind is the list-shaped half of this question answered the other way on purpose —
two packs adding to one array both land, deduplicated, with no collision
([`pack-system.md`](../reference/pack-system.md#config-list-equality)). It does not touch same-KEY
scalar overlays, so this question stands.)* It is worth deciding anyway
because the **neighbouring kind answers the same shaped question the opposite way**: since 2026-08-02 a same-identity `config`
declaration is a LOUD collision, named in `yolo pack footprint` and refused at launch and by
`yolo host apply` ([§9](../reference/pack-system.md#config-surfaces-and-the-compose-engine)). Two adjacent kinds with opposite silence policies is the drift.
Provenance: born 2026-08-13 (`58ae8227`) as the generic residue of the retired auth-pack
`provides` mechanism — *"not auth-specific, and nothing is blocked on it."*

_Leaning:_ **refuse and name both packs — the same shape as the `config` exclusivity collision
that already ships** — but scoped to a genuine same-KEY overlap, never to two overlays on one
surface, which is the collaboration the kind exists for. The cheap first move is
`yolo pack footprint`: it already emits one claim per overlay contribution keyed by target
identity, and an overlay body's key set is statically known from the manifest, so widening the
claim from *surface* to *surface + key* turns this into a detectable collision with no new
mechanism. **What would change my read:** a case where two packs legitimately want the same
key and rely on order — I have not found one.

**Answer:**
> _(empty — fill in when decided)_

### <a id="OQ-S4"></a>💬 **[OQ-S4](#OQ-S4) — should the jail narrow its skills fan-out to match the host?**

**Or, stated as the real question: does a declaration NARROW delivery, or only ADD to it?**

**Context.** A `skills` contribution may name an `into` (a destination directory). The two
notches disagree about what that declaration means, measured by the S4 audit
([`pack-system.md`](../reference/pack-system.md#skills-fanout-s4), which also says loudly **"the
gate holds; do not re-audit it"** — this is a fan-out question, not a security one):

- **Jail:** *every* loaded pack's skills reach *every* declared destination.
  `PrepareSkills` copies every entry of `packSkillDirs` into every `packSkillTarget`
  (`internal/jailcontent/skills.go`).
- **Host:** a pack's skills reach only the destinations that pack declared —
  `packload.ResolveDestinations`, called from `internal/cli/apply.go`. Re-checked 2026-09-24:
  `ResolveDestinations` has **no caller** under `internal/cli/run` or `internal/entrypoint`; its
  one other production caller, `basehome.DeclsFromPacks`, reads the result for credential files,
  not skills. *(Re-checked 2026-09-30: `internal/cli/run` now calls it for addressed `files`
  (`packfiles.go`, notch-convergence item 21) and for the audience check
  (`unmatchedaudience.go`), still not for skills, so the jail's skills fan-out is unchanged.)*

Pinned deliberately by `internal/cli/run/packskillsdelivery_test.go`, so answering this either
way moves a test on purpose rather than rediscovering the behavior.

**Stakes.** Three options:

1. **Leave the jail; make the reporting honest.** Say the fan-out out loud in
   `pack-system.md`'s `skills` section and in `yolo pack footprint`. *Cost:* a manifest keeps
   understating delivery, so a pack its author scoped to one directory still reaches every
   selected agent — the reviewable artifact and the behavior stay out of step.
2. **Run `ResolveDestinations` on the jail path too.** A pack that DECLARES a destination
   delivers only there; a pack that declares nothing borrows every destination in the set —
   the zero-ceremony promise is preserved *by* the borrowing, which is what the function
   exists for. *Cost:* a behavior change on a shipped kind. No pack yolo ships carries skills,
   so it lands only on a third-party or local pack that both declares an `into` **and** ships
   skills.
3. **Widen the host to the jail's rule.** **Already rejected** by `ResolveDestinations`' own
   comment: a manifest would start writing into home directories its author never named.

**The trade to weigh before agreeing to (2).** A content pack that declares a unique path (say
`into: ".acme/skills"`) would deliver there and nowhere an agent reads — inert, where today it
reaches everything. That is arguably correct (it is what the pack declared) and arguably a
regression, and `pack-system.md`'s own advice pushed authors toward declaring rather than
staying silent.

<!-- vantage: question id=OQ-S4 leaning="(2): run ResolveDestinations on the jail path too, so a pack that declares a destination delivers only there and one that declares none borrows every destination; and do (1), honest reporting in pack-system.md and yolo pack footprint, either way." -->

_Leaning:_ **(2)**, because it makes both notches answer from one inference instead of two,
makes `into` mean what it says, and makes `yolo pack footprint` true — the same argument F1
used to give the host the jail's inference, applied in the other direction. **(1) is the
honest fallback and is worth doing either way**, since even under (2) the footprint should say
what a *borrowed* destination is.

**Answer:**
> _(empty — fill in when decided)_

### <a id="OQ-E4"></a>💬 **[OQ-E4](#OQ-E4) — do `stateful` surfaces get comment preservation too?**

**Context.** The residue of E4 above. `rmw` preserves comments (shipped 2026-08-12,
`internal/entrypoint/tomltrivia.go`); `computed` correctly does not; `json` is provably
vacuous. `stateful` is the remaining mode and it is **a different problem wearing the same
words**: the file is composed from layers, so a comment can only come from the `host` layer,
and putting it in the render is a **projection out of one file into another** rather than an
in-place edit. `tomltrivia.go`'s header names exactly this, and notes that the emitter
`stateful` would have to change is also the one the A12-fatal boot path and the render
fingerprint gate depend on.

**Stakes.** Three options, priced in the retired `host-file-staging.md` (`git log -- docs/plans/host-file-staging.md`):

1. **Do it.** `Codec` grows an optional `TriviaCodec`; trivia rides the compose result; the
   staleness rule ① is keyed on `Result.Provenance` (emit a comment only where the winning
   layer is `host`), which is already computed — one map lookup per key. *Cost:* it lands on
   the A12-fatal boot path. (The second cost this option carried, an answer for the **Lua
   transform boundary**, went away when that transform was removed on 2026-09-11 —
   [`OQ-LT1`](../reference/pack-system.md#oq-lt1). A `derive` script still returns a table, but
   it fills the `computed` layer, which never carried user comments.)
2. **Extend the yolo-authored header that is already there** to point at the `:ro` original.
   Measured 2026-08-12: the composed `~/.codex/config.toml` already opens with *"Generated by
   yolo-jail — composed at jail start; hand edits may be reverted or lost…"*
   (`render.GeneratedHeader`, `internal/render/surface.go`, re-checked 2026-09-24). What it does not carry is the
   `/ctx/host-user/<slug>` path to the untouched source. *Cost:* near nothing — but strict
   JSON has nowhere to put a header line, so it serves `toml` only.
3. **Leave it, and say so.** `raw` already round-trips a hand-written file byte-exact, and
   `config-ref` already documents the structured-codec trade. *Cost:* none new.

<!-- vantage: question id=OQ-E4 leaning="(3) for now: leave stateful surfaces without comment preservation and say so; build (1), the TriviaCodec route, only when a stateful surface's host source is a commented TOML file a user maintains." -->

_Leaning:_ **(3) for now, then (1) when something needs it.** The reader this was for — an
agent reading config to learn *why* a value is what it is — is now served on the file the
argument was actually about (`~/.codex/config.toml` at the host notch, where a real user's real
comments were being destroyed on every apply). (2) is a one-line follow-up worth folding into
whatever touches that header next, not a reason to open this.

**What would change my read:** a `stateful` surface whose HOST source is a commented `.toml`
a user actually maintains. Today there is none — the only shipped TOML surface is
`codex/config`, and the only shipped commented-config population is user-declared `host_files`
entries, which get `raw` by auto-detect and keep their bytes exactly.

**Answer:**
> _(empty — fill in when decided)_

### <a id="OQ-MM3-upgrade"></a>💬 **Claude's saved `model` survives the first launch after upgrading (gap in [OQ-MM3](../design/model-lists-and-pickers.md#OQ-MM3))**

On the first launch after upgrading, a stale `model` saved from the user's own Claude login, with no tier word in it (a full model id, for example), is not cleared. With no selection record yolo keeps a value that differs from its own last write as the user's, and the fix is unbuilt; see [OQ-MM3](../design/model-lists-and-pickers.md#OQ-MM3).

---

### <a id="census-stale-paths"></a>🔧 **The completion census keeps a deleted doc's path**

`python3 scripts/completion-census.py` regenerates `scripts/completion-readers.json` from what each
test opens or stats. `TestEveryDocCitationFromGoResolves` stats every doc path that file lists, so
the census records a deleted doc's path again, and the same test then fails on it. Found
2026-10-16, when 11 deleted docs survived a census run and had to be removed by hand. A fix is to
leave paths that do not exist out of the census, or to keep the citation test's own stats out of
it.

### <a id="scheduler-cwd"></a>🔧 **The preparation scheduler keeps the launching workspace as its directory**

`startPreparationScheduler` (`internal/cli/preparationscheduler.go`) starts a daemon that lives
until logout without setting its working directory, so it holds open the first workspace it was
launched from, even after that directory is deleted, and blocks unmounting it. Set it to `/` or
yolo's state directory. Found by the 2026-10-16 review of the integration cleanup fix.

A related question for the owner: should a launch that is refused (an unsatisfiable Node floor,
say) still register its program for background preparation, so the scheduler keeps retrying it?

### <a id="gh-pipe-grace"></a>🔧 **A killed `gh` call can still hang on a process that left its group**

Since 2026-10-16 the github broker re-sends its kill to `gh`'s process group until `gh`'s output
closes. A grandchild that leaves the group (`setsid`) and keeps that output open still hangs the
call, and the kill then repeats every 50 ms until it ends. After a bounded wait, close the read
ends instead, as Go's `exec.Cmd.WaitDelay` does.

### <a id="darwin-prep-fence"></a>🔧 **The integration cleanup cannot stop preparation workers on macOS**

The test cleanup that stops a test home's preparation workers finds them through `/proc`, so on a
macOS host it stops nothing, and a test there can still fail its temp-directory cleanup the way
`TestAnUnsatisfiableNodeFloorRefusesTheLaunch` did on Linux CI.

### <a id="aws-auth-gateway-readme"></a>🔧 **The aws-auth README does not mention the gateway route**

[OQ-SSO9](../reference/agent-credentials.md#sso-decision-ledger) retired option D in favor of a
gateway route that takes an API key, but `packs/aws-auth/README.md` never says so. Found
2026-10-16 while compacting that question.

### <a id="profile-line-grouping"></a>🔧 **The unreached-agents launch line repeats a shared reason**

Since 2026-10-16 a bare `-p` names every agent it does not reach on one line, each with its own
reason and fix. Agents that share a reason (two agents on the Bedrock platform case, say) repeat it
in full, so the line grows by a paragraph per agent. Group agents that share a reason. Found by the
review of the one-line change.

### <a id="capture-store-comment"></a>🔧 **`internal/capture/store.go`'s package comment says hardlink**

It says a materialized file is a hardlink; the code tries a reflink first. Found 2026-10-16 while
graduating the install-capture plan.

### <a id="claude-double-1m"></a>🔧 **claude's env derive can spell a model `x[1m][1m]`**

A declared window of 1M or more on a model id that already ends in `[1m]` gets a second suffix,
on every provider: the env derive's `selected .. suffix` has no guard, unlike `routedSpelling`.
Found 2026-10-16 by the review of the Kilo window fix.

### <a id="teardown-drops-top-level-deletion"></a>🔧 **The teardown capture drops a real top-level deletion**

The teardown capture and `yolo config capture` run the compose engine with no config layers, so
`retireConvergedOverlay` reads a real top-level deletion as already matching and drops its
tombstone. The next boot's capture records it again, so it is late rather than lost. Found
2026-10-16 while fixing the added-`null` data loss.

### <a id="image-identity-twice"></a>🔧 **A launch can evaluate the image identity twice**

`startImagePrewarm` always replaces the launch's image-identity memo, so a memo that
`launchImageIdentity` installed earlier in the same launch is discarded and the identity is
evaluated again. Found 2026-10-16 by the review of the prewarm-stop fix.

### <a id="mirror-gc-on-launch"></a>🔧 **A launch's mirror gc can stall it silently**

Since 2026-10-16 the pack store runs `git gc --auto` in the foreground after a fetch, on a fresh
store timeout. Once a mirror passes `gc.autoPackLimit`, a launch that fetches pays for a full
repack; on a very large mirror that outlasts the 60-second launch fetch budget it is killed and
retried at every launch, with nothing printed. Give the gc a shorter budget, or say when it runs.
`Sync`'s gc call (used by `yolo pack install` for forks) also has no test, and the comment above
`Sync` wrongly says no production path calls it.

### <a id="broker-signal-gaps"></a>🔧 **Two small signal-order gaps in the host brokers**

The github broker copies the host's `hosts.yml` into its run directory before it installs its
SIGTERM handler, so a SIGTERM in that moment leaves the copy until the next broker's sweep removes
it. The Claude broker removes its `--end-with-pid` socket before an in-flight refresh finishes,
where the OpenAI broker deliberately removes it after. Found 2026-10-16 by the review of the
Claude broker's SIGTERM-order fix.

### <a id="printed-format-patch-prefixes"></a>🔧 **Other printed `git format-patch` commands pass no prefix flags**

The rebase recipe now passes `--src-prefix=a/ --dst-prefix=b/`, because some gits read any
`format.noprefix` as true. Other commands yolo prints or documents for exporting a series do not:
the patch-failure block's `git format-patch --base=…` (which also has no `-o` and no range),
`yolo config-ref`'s, `contributes.go`'s, the patch-series guide's and `pi-extensions.md`'s. A user
with `format.noprefix` or `diff.noprefix` set exports a series `git am` rejects. Found 2026-10-16
by the review of the recipe fix.

### <a id="mac-host-extension-checks"></a>🔧 **`yolo host` on a Mac checks the sandbox account before it knows an extension is selected**

It runs the sandbox-account setup checks before checking whether any patched extension is selected,
and the launch block and `yolo host` word a Mac that has not run `yolo macos-setup` differently.
Found by the review of the patched-extensions lane.

### <a id="older-copy-attach-flake"></a>🔧 **`TestAnAttachIntoAnOlderYolosJailKeepsTheLineAndNamesTheStop` fails intermittently**

It failed once in a full `just check-ci` run with "the fixture does not read as an older yolo's
machine-wide copy" (`internal/cli/run/launchcheck_test.go`), then passed alone three times and in
the next full run. The fixture's stand-in copy is probably not answering yet under load. Found
2026-10-18.

### <a id="mount-table-test-race"></a>🔧 **`TestIsMountPointReadsTheProcessMountTable` races a changing mount table**

It reads the jail's live mount table, picks a line, then asks `IsMountPoint`, which reads the table
again. It failed once on `/var/lib/containers/storage/overlay` while integration tests in the same
jail were adding and removing podman mounts (`internal/entrypoint/fsx_test.go`). A fixture mount
table instead of the live one would remove the race. Found 2026-10-18.

### <a id="worker-sigterm-capture-jail"></a>🔧 **A stopped preparation worker leaves its capture jail's keeper running**

A shared-demand worker that gets SIGTERM while a `program-capture-jail` runs exits without waiting
for that jail's teardown, so the jail's `internal daemon jail-keeper` outlives it until something
else stops it. Seen 2026-10-18 by the integration cleanup fixture (`stopHomeProcesses`), which now
stops it in a later round. Whether the worker should end its capture jail first is open.

### <a id="stale-mise-shims"></a>🔧 **A mise shim another workspace installed shadows the jail's own tool**

The mise data directory, `/mise`, is shared by every jail on a machine, and its `shims/` directory
sits ahead of `/bin` on the jail's PATH (`BootPath`). So a tool any workspace installed with mise
leaves a shim that `command -v` reports in every other jail, whatever that workspace configures.
Measured 2026-10-18 in this repository's jail (mise 2026.10.3): `rg`, `python3` and `overmind`
resolve to `/mise/shims/` although no mise config this workspace reads declares them. Running the
shim falls through to the image's copy (`/mise/shims/rg --version` printed the image's ripgrep
15.2.0, not the installed 14.1.0, and `python3` reported `/bin/python3` as its executable), so what
runs is right today and only the reported path is wrong. The cost is a mise lookup on every call, a
`command -v` answer that names the wrong file, and a dependence on mise falling through. The
macos-user sandbox home has the same exposure; the order itself is by design on both backends
([OQ-DM3](../design/macos-user-suite-on-a-developer-mac.md#decision-ledger)). A shims directory
per workspace would end it.

### <a id="boot-baseline-fifo"></a>🔧 **The host reads `.yolo/config-boot.json` with a plain read under the launch lock**

`config.ReadWorkspaceBootBaseline` and `WorkspaceConfigDrift` open the agent-writable baseline with
`os.ReadFile`, so a FIFO planted there would hang a launch, an attach or `yolo config apply` while
it holds the workspace's launch lock. The workspace config file has the same exposure today. Found
by the live config apply review, 2026-10-18.

### <a id="launch-two-reads"></a>🔧 **A fresh launch reads the workspace config twice: once to load, once at its gate**

What a launch builds from comes from its first read, and what its gate approves from the second.
Nothing in the workspace runs during a fresh launch of a container jail, so this needs a host-side
writer to matter; `yolo config apply` closes the same gap for the running case. Found 2026-10-18.

### <a id="scope-widened-field"></a>🔧 **A live scope rewrite keeps a scope file's legacy `widened` list**

`brokerscope.ReplaceRepos` rewrites `repos` only, so on a scope file a build before WW-D12 wrote, a
repository listed under `widened` and removed from the config is never narrowed live. Such a file
belongs to a broker that cannot take live scope anyway. Found 2026-10-18.

### <a id="pi-paste-freeze"></a>🔧 **A long Kitty paste froze Pi's display twice; the cause is unknown and the work is parked**

The owner reports that pasting one long selection through `clip2path` twice left Pi's input,
output and Working animation stopped while Kitty stayed responsive, and Ctrl+Z returned a shell
([`kitty-paste-freeze-limits-20261028.md`](../research/kitty-paste-freeze-limits-20261028.md), which
says what that analysis does and does not establish). A 776-line offline probe and its write-up
([`pi-paste-probe.mjs`](../research/fixtures/pi-paste-probe.mjs), [`pi-terminal-paste.md`](../research/pi-terminal-paste.md)) exercised the
installed Pi 1.4.0 paste parser, editor and both TUI renderers with synthetic 57 KB input: every
case passed, so the code paths are separated but the incident is not reproduced and no cause is
established. The one defect it found is in yolo's own proxy, now fixed: the output write dropped
its count and error, and `writeOutputChunk` (`internal/ttyproxy/ttyproxy.go`, `624e81f60`) now
reports it; whether that write was ever under pressure during a paste is unknown. Parked; the probe and its write-up are on main. The next evidence is an unfiltered
synthetic stdin capture at Pi's TTY boundary; until then, put a long body in a file and pass the
path. Found 2026-10-26.

### <a id="font-copy-gaps"></a>🔧 **The macos-user font copy: four gaps the lane's review left**

The opt-in font copy (`share_host_fonts`, [PS-D19](../design/provisioner-sets.md#decision-ledger))
landed with these open. A dry run returns before the copy is built, so it neither shows the copy
nor its grant. Pruning can delete a copy a launch that lost the rename race is about to use, and
turning the opt-in off removes the copy under a running session. The file copy re-opens each
source by path after listing it, so a folder swapped for a link in that gap is followed. A launch
killed while building the copy root leaves a `.fonts.new-*` folder nothing sweeps. Found by the
lane's review, 2026-10-23.

### <a id="self-mode-tmpdir-trees"></a>🔧 **Trust-bearing trees sit in a `$TMPDIR` that macos-user's run-as-you mode can write**

`yolo pack build`, `yolo host` and background preparation stage packs into the process pack tree
under `os.TempDir()`, as do the embedded fallback and the patched-replay scratch repository. On a
Mac that is `DARWIN_USER_TEMP_DIR`, which the run-as-you Seatbelt profile grants read and write. An
agent in a concurrent run-as-you jail that swaps a staged directory for a link can redirect the
host-side digest and copy, since BG-D23 follows links above a tree's root. It takes a deliberate
race, out of scope under the misguided-agent principle, and no path was found for the redirected
bytes to reach a jail. The fix is to keep these trees out of a jail-writable `$TMPDIR`, or to treat
the tree root yolo made as the base. Found by the capture-symlink fix's review, 2026-10-23.

### <a id="concurrent-integration-suites"></a>🔧 **Two integration suites on one podman break each other without saying why**

Both use fixed-name volumes and the one `yolo-jail:latest` tag, and each reloads that tag from its
own tree. Two run at once on 2026-10-23 failed with "volume already exists" and a process-cleanup
error, neither naming the other run. The cross-run lock should cover the image load and the
fixed-name volumes, or a run should refuse while another holds the machine.

### <a id="timing-flakes-under-load"></a>🔧 **Two short deadlines fail `just check-ci` on a loaded machine**

`TestAHealthyMintPrintsNoLaunchCheckLine` (a 2-second mint deadline) and
`TestLargePasteDeadlockPinsNonblockingMasterForwarding` (a wall-clock ttyproxy deadline) each failed
once during a 2026-10-23 landing gate with other agents' builds running, and passed alone. Each
deadline should measure what it pins without a wall-clock bound the machine's load can cross.

### <a id="keeper-holder-flake"></a>🔧 **The keeper's keep line sometimes names no session-lock holder**

`TestTheKeeperLogsTheSessionLockHolderWhenItKeepsTheJail` (`internal/cli/run`) failed once in a
full `go test -short ./internal/...` run on 2026-10-29, while other agents' builds were running,
and passed three times alone. The keeper logged that the test's pid held the session lock, then
logged its "keeping the jail" decision without naming that pid. So the holder lookup at decision
time can come back empty when the earlier one did not. Found by the capture record-platform lane,
which touched nothing in `internal/cli/run`.

### <a id="mountinfo-reread-flake"></a>🔧 **The mount-table test races a podman that unmounts between two reads**

`TestIsMountPointReadsTheProcessMountTable` (`internal/entrypoint/fsx_test.go`) reads
`/proc/self/mountinfo`, then asks `IsMountPoint` about each mount point, and `IsMountPoint` reads the
table again. On 2026-10-29, during a `just check-ci` with podman running in the jail, an overlay
mount under `/var/lib/containers/storage` was gone by the second read. The test failed on it, then
passed three times alone. The test should compare against the same table it read, or skip mount
points that vanished in between.

### <a id="landing-gate-flakes-2026-11-09"></a>🔧 **Tests that failed once in a loaded landing gate and passed alone**

During the 2026-11-09 landings, with load average from 30 to 60, each of these failed in a full
`just check-ci` or integration run and then passed when run alone.

Every test on this list is now fixed:

- `TestQuitDoesNotWaitForTheScratchDelete` (`dffa13872`): it timed the quit against half a baseline
  delete. It now holds the detached remover at a gate the test opens after the quit, so the
  launcher returning while the remover still runs is the pass condition.
- `TestSelectedCLIExactOnceExpiresOrIsRevokedWithoutHostEffect` (`5213f370a`): its fixture
  classifier had to answer inside the production 2 s per-process deadline. Fixture classifiers now
  get a 2-minute failure guard.
- `TestPackCLIClassifierStopsOwnedDescendantAfterSuccessfulWrapperExit` (`c7afd4aad`, and
  `5213f370a` for its deadline): a product race. After the process group drained, os/exec had
  100 ms to finish copying the output, so a successful classifier or host program could fail with
  `WaitDelay expired before I/O complete`. That bound is now a 10 s failure guard.
- `TestRunProactiveSurvivesAFailedMint` (`a70ea3e91`): its 5 s wait became the test's own deadline
  before this list was written.
- `TestForwardAccessDisclosesDeniedGitGroupTerminationAtEveryPreflightProbe` (`7babb6e63`, then
  `17502ece3`): the session ends on an event, and the return is no longer timed.
- `TestAHungRemoteFetchTimesOutOnItsOwnBudgetAndStillBuilds` (`d4cb0270a`, then `8175df68b`): the
  check's whole budget was 4 s, which left about 2 s for the steps after the hung fetch. The check
  now has 10 minutes, and a test-only cap gives the hung fetch 500 ms.
- The two `TestSelectedNPMShared…` tests (`internal/cli/programcapturelifetime_test.go`), whose
  5–6 s barrier and lease waits `9f84fd251` replaced with the test's own hang guard.
- `TestPreparationActualFailureReasonAndBackoffSurviveIdleTicks`, whose 5 s wait for both failure
  records `305f0f500` replaced the same way.

Two other flakes from the same gates were fixed earlier: the proxy egress tests (`853751692`) and
`TestPerJailRecordlessStartupReasonCleanup` (`ed4a4d420`).

### <a id="landing-gate-flakes-2026-11-12"></a>🔧 **Tests that failed once in the 2026-11-12 fan-out and passed alone**

During the 2026-11-12 roadmap fan-out, with fourteen lanes testing at once, each of these failed in
a lane's run and then passed alone. These remain:

- `TestNoteUseProfilesCountsTheContainerArgvAsReachingTheAgent`
  (`internal/cli/run/useprofiletargets_test.go`) is not the defect. The `internal/cli/run` package
  hit its 45-minute timeout while that test had run for 0 s and was removing its temporary
  directories. The package runs serially, and on 2026-11-13, at a load average near 30, a
  `go test -short` of it took over seven minutes. Under fourteen lanes it outgrew the
  `SHORT_TEST_TIMEOUT` that `just test-fast` passes. Several of its slowest tests take 15 s each,
  which suggests they wait out a production bound rather than an event. A timeout's goroutine
  dump also lists `armLaunchSignalsOuter` goroutines parked for minutes: idle launch arms that
  earlier tests left installed, such as the launch that `runContainer` parks in `select {}` once
  its arm has taken a signal. They hold no test up, so they are not the hang; the running test's
  own duration in the dump's `running tests:` line is what tells a hang from slowness.

- `TestPackCLIClassifierDeadlineBoundsEachInvocationNotLookups` and
  `TestHTTPClassifierDeadlineBoundsEachInvocationNotLookups` (`internal/callbroker`) still need each
  classifier run to beat a 1 s or 2 s deadline; a combined `go test -short` run on 2026-11-13 failed
  once in `internal/callbroker` at a load average near 28, and its failing test was not captured.

Fixed from this list:

- `TestPreparationControlTickDetectsAReplacedBinary`: a tick send returns before the loop handles
  the tick, so the scheduler could stop at the tick before the one the test waited on.
- `TestTheSlotBuildsItsKeysAtOnceAndPrintsThemInOrder` (`305f0f500`): each build child waited at
  most 5 s for the fourth to start, and three keys waited on the pack mirror longer than that.
- `TestBackgroundFinishContentionPreservesTypedFailureClaim` and
  `TestPatchedAdvanceExternallyOwnedContextRetainsPosture` (`305f0f500`): 5 s and 2 s bounds on
  real git work.

Explained, not a defect: the `internal/packsrc` git-wrapper tests that "timed out twice in one lane" were not hung, and the
git broker environment does not reach them. That lane ran plain `go test -short`, whose 10-minute
default timeout is per package, not the 45 minutes `just test-fast` passes. The running test had
been going 4 s when the whole package ran out. The package's `TestMain` replaces every inherited
`GIT_CONFIG_KEY_*` and `GIT_CONFIG_VALUE_*` with its own tripwire (`ArmGitConfigTripwire`), and the
replay drops every `GIT_` variable but `GIT_EXEC_PATH`. With the broker's five rewrites set, the
running test, `TestAMemberUpstreamMergedAndThenEditedIsAlreadyUpstream`, passes in 0.26 s. On 2026-11-13 the whole package took 127 s at a load average of 93.

### <a id="landing-gate-flakes-2026-11-13"></a>🔧 **Tests that failed once in the 2026-11-13 briefing landing and passed alone**

Each of these failed once while other lanes' suites ran at the same time, then passed alone. Logs
are under `.yolo/durable/fanout-20261113/` (`briefing-t5/census-run2.log` and
`census-embedded-leak/`).

- `TestSelectedNPMTimerActualDispatchPreparesWithoutMovingRunningHome`
  (`internal/cli/programcapturescheduler_test.go`): "production timer did not reach admitted".
  `waitOrdinaryTimerPhase` polls for the phase against a 10 s deadline, an elapsed-time pass
  condition the event-driven test rule forbids.
- `TestPreparationReadinessReportsStartupFailure` (`internal/cli`): "lock held". Likely a
  production race: `probePreparationWorker` briefly takes `LOCK_EX` on `worker.lock`, so a worker
  starting at that moment fails its non-blocking acquire.
- `TestBackgroundSelectionDefersAFetchedPackWhoseCheckoutLockIsHeld` (`internal/cli`).
- `TestOwnedChildEarlySignalCleanupReapsChild` (`internal/ttyproxy`): timed out after 10 s
  waiting for its marker.

### <a id="launch-prep-runtime-leftovers"></a>🔧 **Two status-wording leftovers from the launch-preparation runtime run**

Found by [the 2026-11-13 nested run](../design/launch-update-preparation-plan.md#runtime-2026-11-13):

- Under `--update`, the "⚡ Executing" line prints before the in-jail refresh; the refresh still
  finishes before the command runs.
- The status headings still say "Tree preparation worker" and "built-tree preparations", though the
  worker now covers ordinary programs too.

### <a id="program-capture-record-platform"></a>🔧 **A program capture still judges an existing entry's platform by its manifest**

The capture store's [record-platform rule](../reference/install-capture.md#record-platform) makes
the platform a property of each record, not of the entry, so identical bytes serve every platform a
record names. Sealed program captures (`prepareProgramCapture`) do not follow it yet:
`validateProgramManifest` checks both the selected winner and the retained duplicate against
`inputs.Platform`, so a pure-JS npm package with the same bytes on Linux and darwin is refused when
the second platform is admitted. `TestProgramCaptureAdmissionLinuxDarwinSameBytesRetainsOldReceipt`
and the `platform` case of `TestProgramCaptureAdmissionRejectsRetainedMetadataConflict` pin that
refusal on purpose (commit `5be28a2d6`), so changing it reverses a deliberate decision. Today it is
unreachable, because program captures run only for a Linux capture jail's platform
(`programcapturedemand.go`). Revisit it when darwin program captures arrive.

### <a id="macos-unshared-parent-loop"></a>🔧 **A macos-user workspace under a private parent loops between two refusals**

A workspace outside the shared root `/Users/Shared/yolo` and outside every home (a `mktemp -d`
under `$TMPDIR`, whose `T` parent is `drwx------`) is refused for missing the sandbox group's
access-control entry, with text saying it was "MOVED or copied into /Users/Shared/yolo". The named
`yolo macos-fix-permissions <workspace>` succeeds, but the sandbox account still cannot get past the
parent, so the next launch fails six boot steps with `permission denied` and names the same
command again. The sharing precondition checks only the workspace's own entry, never its
parents. Found natively on 2026-10-29
([lab report §4](../lab-reports/2026-10-29-mac-native-fix-checks-and-unshared-ancestor.md#4-new-defect-a-workspace-under-a-private-parent-loops)).

### <a id="it-dangling-layout-links"></a>🔧 **macos-user integration runs leave dangling layout links in the real account home**

Each macos-user test that selects a pack with a state folder lays a link such as `~/.oh-omp` from
`/Users/_yolojail` into its temporary `yolo-it-*` workspace, and nothing removes it when the workspace
is deleted. On 2026-10-30 a dangling `~/.gemini` left by an Oct 18 run refused a real launch whose
pack delivers files under `.gemini`, until the maintainer ran `sudo rm` on it
([lab report §2](../lab-reports/2026-10-30-mac-sandbox-home-collisions-and-check-nudges.md#2-dangling-layout-links-from-integration-runs)).

### <a id="files-into-foreign-state-dir"></a>🔧 **A pack's files under another pack's state folder lock that pack out of the account home**

A `files` contribution into `.gemini/config/agents` (the maintainer's `matt-agentwork`), launched
without `agy`, creates a real `/Users/_yolojail/.gemini`. Every later launch selecting `agy`, whose
state folder that is, is then refused by `darwin_home_layout`, with the next step
`sudo rm -rf /Users/_yolojail` rather than the one folder. The refused launch still writes agy's
generated config into the real folder, and nothing warns ahead of time
([lab report §3](../lab-reports/2026-10-30-mac-sandbox-home-collisions-and-check-nudges.md#3-a-files-delivery-into-another-packs-state-folder); designed in [`pack-files-for-agent-state.md`](../design/pack-files-for-agent-state.md)).

### <a id="check-nudge-false-warnings"></a>🔧 **Two `yolo check` warnings name fixes that do not apply**

The aws-auth login nudge reads only `allow.aws-auth.profile`, so it warns on a JSONC config that
sets the legacy `loopholes.aws-auth.settings.profile`, which the loophole honours. The `stages 0
files` warning tells the user to fix `only`/`exclude` filters in a `packs` entry for the empty
conventional local pack, which has neither
([lab report §4](../lab-reports/2026-10-30-mac-sandbox-home-collisions-and-check-nudges.md#4-false-check-warnings)).

### Settled, kept for the record

| # | Item | Disposition |
|---|---|---|
| ✅ E6 | ~~Non-agent prism ports (MCP/LSP/identity)~~ | **premise stale.** MCP and LSP *are* ported — they ride the **computed layer** into per-agent surfaces (`copilot/mcp`, `copilot/lsp`, `agy/mcp`), which is the right model: a standalone `mcp` surface would have no file of its own to write. `identity` is deliberately **host-composed and `:ro`-mounted** (`gitIdentityMountArgs`), settled by the identity-prism decision. `config render mcp` reporting "no surfaces" is therefore correct, not a gap |
| ✅ E7 | Renaming the recovered state | **done** — three live terms are NOT synonyms (act / state / layer); vocabulary defined rather than flattened |
| ✅ E8 | ~~Nightly-macOS builder arch mismatch~~ — **DONE 2026-08-03**. The original entry called this "a maintainer decision, not a fix"; that framing was wrong — `nightly-macos.yml:44` documents the Intel runner as the only hosted one with nested virt, testing the macOS *code paths* rather than Intel as a target, so it always had a technical answer. **(1)** `7cc54a0`: `publish.yml` pushes per-arch builder tags plus a multi-arch index, so `:latest` resolves on both arches. **(2)** `BuildersLine`'s advertised system was hardcoded `aarch64-linux` — *"the arch a Mac needs"* — so an x86_64 host connected to a working builder, was told it serves aarch64-linux, and nix declined to offload while the builder looked healthy. Now `BuilderSystem()` derives it from GOARCH. **(3+4) Two MORE instances of the same assumption, found by grepping the literal rather than trusting the entry's scope** — `nixdiag.HasLinuxBuilderFromConfig` decided "is a builder configured?" by matching `aarch64-linux`, so on x86_64 it reported *no builder* while one ran (and, with (2) fixed, would have accepted an arm64-only builder for an x86_64 build); and `DiagnoseNixBuildFailure` keyed its cross-build branch on the same literal, dropping x86_64 hosts to a raw stderr dump instead of the Linux-builder remedy. Both now take/match the wanted system. All four mutation-tested. **Remaining caveat, not a defect:** `publish.yml` is tag-triggered, so the multi-arch image does not reach GHCR until the next release — the nightly stays red until then, and that first nightly is the real proof | ✅ done, verified by the next release's nightly |

---

## Ideas from deleted docs

**Added 2026-10-16.** Forty-four superseded, stale or finished docs were deleted that day (git
history keeps them). Each idea below was deferred, parked or left open in one of them, and no live
doc tracked it. Each entry names the deleted doc it came from and links the live doc that owns the
topic now. They are listed by area, in no priority order; nothing here is ranked or scheduled.

### Credentials and providers

- **Google refresh-token rotation for agy.** Find out whether Google rotates the refresh token
  behind agy's sign-in. agy's login is one file every jail on a machine shares, and no yolo service
  coordinates its refreshes, so if each refresh invalidates the previous token, two jails
  refreshing at once could log each other out. From `sequencing-2026-07.md`, deleted 2026-10-16.
  Owner: [where each agent's credentials live](../reference/agent-credentials.md#where-each-agents-credentials-live).
- **Copilot as a model source for Claude Code: closed, with reopen triggers.** A Copilot
  subscription cannot legitimately back Claude Code today: GitHub has no documented inference
  endpoint, a proxy would break GitHub's acceptable-use terms, and at Business rates it saves
  nothing. Revisit only if a Copilot plan's included credits clearly beat Anthropic's list prices
  or GitHub publishes an inference API, and re-read GitHub's terms first. From
  `copilot-as-a-provider.md`, deleted 2026-10-16. Owner:
  [Copilot's BYOK contract](../reference/providers.md#copilots-byok-contract).
- **pi's model picker: make the provider visible.** pi's `/model` rows end with the provider in
  brackets (`gpt-oss-120b [cerebras]`), which is easy to miss. If that still confuses people,
  propose a small upstream pi change that shows the provider first; ship a yolo picker extension,
  under a new command, only if the needs grow to provider tabs or favorites. From
  `pi-model-selection-ux.md`, deleted 2026-10-16. Owner:
  [rendering the effective list into each picker](../design/model-lists-and-pickers.md#8-rendering-the-effective-list-into-each-picker).
- **Show where an inherited provider value came from.** `yolo config why` and `yolo config dump`
  read config layers, not the composed providers table, so a value a provider took from another
  through `inherits` (`bedrock-key`'s model list, written under `providers.bedrock`) shows no
  origin. Showing "inherited from bedrock" needs a composed-table origin trace those commands do
  not have. Added 2026-11-10 as PI-D9. Owner:
  [provider inherits](../design/provider-inherits.md#PI-D9).
- **Two small gaps in provider `inherits`.** A null fact in a child's own `pack.toml`
  `model_options` deletes the inherited fact instead of composing as a removal marker that
  overrides lower layers, because the child's pack entry is folded in with the user layer's
  merge; no shipped pack writes one. And a child declared only in the user's config composes
  at the end of the table rather than at its place in the file, which matters only to a menu
  that follows table order. Found in the 2026-11-10 review. Owner:
  [provider inherits](../design/provider-inherits.md#the-key).

### Security model

- **One stated adversary for the project.** Write down, once for the whole project, which
  adversaries yolo defends against. Each subsystem names its own (a sibling jail for the loophole
  transport, an in-jail agent editing workspace config for the approval gate), and none says
  whether a malicious dependency running inside the jail, such as an npm package or an MCP server,
  is in scope. From `sequencing-2026-07.md`, deleted 2026-10-16. Owner:
  [the loophole transport's threat model](../reference/loophole-transport.md#threat-model).
- **Pack signing and revocation.** Optionally require that a fetched pack's commit is signed, and
  add a way to revoke a known-bad pack tree (a denylist plus a `yolo pack audit` check). Weigh it
  against the ruling that the pack boundary is disclosure, not an approval gate. From
  `agent-config-packs.md`, deleted 2026-10-16. Owner:
  [the credential boundary](../reference/pack-system.md#the-credential-boundary-disclosure-not-consent).

### Packs and skills

- **Team pack baseline.** One command that brings a fresh machine to a team's shared `packs` list.
  Once such a list is shared (for example through `include_if_found`), also commit a lock beside
  it that records the exact commit each pack resolved to. From `agent-config-packs.md`, deleted
  2026-10-16. Owner: [the `packs` key](../reference/pack-system.md#the-packs-key).
- **In-jail pack fetch.** Let a running jail fetch a new git pack mid-session through a host-side
  git credential helper over a socket, instead of fetching only on the host at launch. From
  `agent-config-packs.md`, deleted 2026-10-16. Owner:
  [fetch, refresh, lock](../reference/pack-system.md#fetch-refresh-lock).
- **Check the agents' skills folders.** Check, against each agent's installed package, that the
  user-level skills folder its pack declares (pi's `~/.pi/agent/skills`, codex's `~/.codex/skills`)
  is still where that CLI reads skills. If a CLI moves the folder, nothing breaks loudly. From
  `agent-config-distribution.md`, deleted 2026-10-16. Owner:
  [the `skills` kind](../reference/pack-system.md#skills).
- **`host_files` into a skills folder.** A `host_files` entry can target a path inside a skills
  folder yolo assembles (for example `.claude/skills`), and nothing refuses it, so two writers can
  claim one tree. From `synced-skill-trees-plan.md`, deleted 2026-10-16. Owner:
  [the `skills` kind](../reference/pack-system.md#skills).
- **Host probe of Claude's sync folders.** On a host with a real Claude login (a jail cannot do
  this, because its skills folder is a read-only bind of yolo's own copy), record when
  `~/.claude/skills/synced` first appears, what `installed_plugins.json` holds after a real plugin
  install, and whether switching organizations adds a second sync folder or replaces the first.
  Then correct the unverified notes. From `claude-plugin-registration-probe.md`, deleted
  2026-10-16. Owner:
  [two sync roots](../reference/pack-system.md#sync-root-two-roots).
- **Retire the `publishes: "endpoint"` manifest key.** No loophole a launch honors can declare
  it any more, but the enum member is the decoder's default and three refusals run through it, so
  deleting the member is the wrong way to retire the key. From `broker-as-a-pack.md`, deleted
  2026-10-16. Owner:
  [what emptying the bundled channel left behind](../reference/loophole-system.md#what-emptying-the-bundled-channel-left-behind).

### pi extensions

- **Make delivery of built pi extensions cheaper.** When the maintainer ruled that pack-declared
  pi extensions are built on the host and copied read-only into each new jail, he left two costs to
  optimize later: the copy made for every jail, and the wait on the first launch that builds an
  extension. From `pi-git-extension-caching.md`, deleted 2026-10-16. Owner:
  [the store builds' risks](../design/pi-extension-store-builds.md#11-risks).
- **pi's refresh does not watch a project's own settings file.** pi's pre-launch refresh becomes
  due when `~/.pi/agent/settings.json` changes, but never when a project's `.pi/settings.json`
  does, so a package newly added to a project's settings is installed by pi's own startup, outside
  the refresh lock. From `pi-git-extension-caching.md`, deleted 2026-10-16. Owner:
  [the `program` kind](../reference/pack-system.md#program).

### Program delivery and patched forks

- **Move opencode to its vendor's installer.** Two blockers were measured 2026-09-03: the
  installer hardcodes `$HOME/.opencode/bin` with no override, and the jail's home is read-only with
  no writable `.opencode` declared, so the move needs a writable directory and a way for the
  launcher to find a binary outside `~/.local/bin`. Whether `omp` has a native installer is also
  unchecked. From `native-installer-migration.md`, deleted 2026-10-16. Owner:
  [the per-agent facts](../design/program-delivery.md#per-agent-facts-verified-2026-09-03).
- **A patched fork's host-floor install on a real host.** Replay and build a series in a real
  sealed jail, then run it from the floor with `yolo host -- <bin>`. Every floor test so far stands
  the build jail in. From `patched-forks-plan.md`, deleted 2026-10-16. Owner:
  [unbuilt and unmeasured](../reference/patched-forks.md#14-unbuilt-and-unmeasured).
- **The patch-series guide's export command fails for a fork with merges.** `git format-patch`
  refuses when the fork merged upstream releases, and a series cut at the merge base does not
  apply when it merged its own branches. The workaround found while migrating the maintainer's
  forks is to export the first-parent chain, each merge one patch; when the fork merged upstream
  releases, the first patch is the integration merge's whole diff against the release. The guide
  should say so. From `patched-forks-plan.md`, deleted 2026-10-16. Owner:
  [patch a program](../../userguide/guides/patch-series.md#patch-a-program).

### macOS

- **macos-user agent-auth parity: find out whether four defects are still real.** An older
  version of the agent-auth design listed four macos-user agent-auth defects whose fixes needed a
  Mac to verify. By 2026-09-30 that doc no longer named them, so whether they were fixed or moved
  is unknown. Read them from `git show 937bddf27^:docs/design/agent-auth-modes.md` and check each
  against today's macos-user launch. From `handoff-macos-user-open-threads.md`, deleted 2026-10-16.
  Owner: [the macos-user manual checks](runbooks/macos-user-manual-checks.md).
- **The macos-user floor carries the image's core packages but not its extras.** The [`OQ-P1`](../reference/macos-user-provisioning.md#oq-p1)
  ruling says the floor is "everything the image bakes", but the floor's list excludes the image's
  extra packages, such as chromium. Either add the extras with explicit darwin exclusions, or
  narrow the ruling's wording. From `macos-revival-and-distribution-plan.md`, deleted 2026-10-16.
  Owner: [the floor](../reference/macos-user-provisioning.md#the-floor).
- **Reread the macos-user launch lines that changed on 2026-10-04, on a Mac.** Run a `--dry-run`
  from a throwaway workspace that declares a `/dev/cu.*` device, a raw disk,
  `ephemeral_storage: "tmpfs"` and a host nvim config. Check that each warning or disclosure reads
  correctly, and that a config declaring none of them prints none. Then drop the user guide's "not
  yet tried on a Mac" serial hedge, once the `declared_device_ioctl_allowed` case passes on
  `macos-user.yml` and a real USB serial adapter accepts `stty -f` from the sandbox. From
  `handoff-mac-unmeasured-claims.md`, deleted 2026-10-16. Owner:
  [networking and devices](../reference/macos-user-nix-and-features.md#networking-devices-gpu).
- **Push the darwin helpers a Mac builds for the stock image.** A Mac fetches every Linux path of
  the stock image but still builds 13 to 15 small darwin-system helpers locally, because only
  Linux runners push to the binary cache. A macOS job could push them; an Apple silicon push would
  also need a `macos-latest` job. Worth doing only if a timed build on an Apple silicon Mac shows
  they cost more than a few seconds. From `handoff-cachix-cache.md`, deleted 2026-10-16. Owner:
  [the binary cache](../reference/image-staging-vs-baking.md#the-binary-cache).
- **Pin the macOS builder image by digest.** The container that builds a Mac's Linux image is
  pulled by the `:latest` tag; the exploration that chose a yolo-built image recommended pinning
  it by digest so a build always uses a known builder. From
  `macos-container-builder-exploration.md`, deleted 2026-10-16. Owner:
  [how the container path works](../reference/image-staging-vs-baking.md#the-macos-linux-builder-for-a-linux-reader).
- **Measure the macOS builder's startup cost.** Nobody has measured how long the builder container
  takes to start on each build, or how long the first image pull takes. If it is slow, one builder
  container could stay warm for a session. From `macos-container-builder-exploration.md`, deleted
  2026-10-16. Owner:
  [how the container path works](../reference/image-staging-vs-baking.md#the-macos-linux-builder-for-a-linux-reader).
- **A darwin jail build for the macos-user guest.** The guest declines a jail daemon naming a
  `{jail_binary:}` path, because a manifest's jail build is a Linux one. Placing a darwin
  build there would let a pack's downloaded jail program run on macos-user. From
  `broker-as-a-pack.md` (BP-D6), deleted 2026-10-16. Owner:
  [the cache, and who fills it](../reference/pack-binaries.md#the-cache-and-who-fills-it).
- **Reclaim retained guest pack trees.** Each macos-user launch keeps its own sealed pack copy, and
  one whose holders yolo cannot prove gone is kept indefinitely. Content deduplication and
  collection after a crash are not built. From `jail-daemon-on-macos-user-plan.md`, deleted
  2026-10-16. Owner:
  [the guest pack tree](../reference/macos-user-provisioning.md#the-guest-pack-tree--one-per-launch-never-replaced).

### Other platforms and hardware

- **aarch64 run of the nix-ld tripwire.** Record one run of `yolo check`'s FHS-loader tripwire,
  the scrubbed-environment start of mise's node, on an aarch64 machine. CI's integration job
  covers both architectures, but this in-jail check has no recorded aarch64 run. From
  `sequencing-2026-07.md`, deleted 2026-10-16. Owner:
  [the regression tripwire](../reference/mise-node-dynamic-linking.md#the-regression-tripwire).
- **Windows through WSL 2 is untried.** yolo may work on WSL 2 (Windows' Linux layer) as a Linux
  host, but nobody has run it. From `sandbox-comparison.md`, deleted 2026-10-16. Owner:
  [what works in each setup](../../userguide/reference/settings-per-setup.md#what-works-in-each-setup).
- **GPU rembg on an AMD gfx1151 in a jail (not yolo work).** It needs an onnxruntime build with
  gfx1151 kernels (MIGraphX with a non-asserts LLVM, or AMD's TheRock wheels). That belongs to the
  downstream project that wanted it; it is recorded here only so the finding is not lost. From
  `rocm-gpu-jail-findings.md`, deleted 2026-10-16. Owner:
  [what ROCm passthrough does not do](../reference/rocm-passthrough.md#what-this-does-not-do).

### Checks, tests and tooling

- **Automate the corpus-honesty checks.** Turn them into a test with allowlists: every open
  question carries a status emoji and a stable id, every cited question id resolves in the doc it
  names, no doc cites the roadmap by a row or item name, and every backticked code path and commit
  SHA resolves. `scripts/check-doc-shas.sh` already covers the SHA part. From
  `further-roadmap-ideas.md` (its I1), deleted 2026-10-16. Owner:
  [keeping this corpus honest](README.md#keeping-this-corpus-honest--the-checks-so-they-are-re-runnable).
- **Flag a stale status line.** Flag a design or plan doc for re-checking when the date on its
  status line is older than the newest commit touching the code paths it cites. It only flags; it
  never decides whether the doc is right. From `further-roadmap-ideas.md` (its I1b), deleted
  2026-10-16. Owner:
  [keeping this corpus honest](README.md#keeping-this-corpus-honest--the-checks-so-they-are-re-runnable).
- **`yolo check`'s Running Jails section says nothing about the host when run in a jail.** Inside
  a jail it reports only the nested runtime's view, such as "No jails currently running", without
  saying the host may have some. Give it the same host-fact note the NVIDIA section now prints.
  From `further-roadmap-ideas.md` (its I3), deleted 2026-10-16. Owner:
  [OQ-3, host facts in `yolo check`](../reference/claude-oauth-interposition.md#oq-3).
- **Reproduce the loopback-forwarding outage in CI.** Add an integration test, run only when an
  environment variable is set, that binds a listener on the jail's loopback and dials it from a
  bare `podman run` inside the jail: it should fail without the loopback-mapping option and
  connect with it. From `further-roadmap-ideas.md` (its I4), deleted 2026-10-16. Owner:
  [a nested jail is blind to this](../reference/loopback-tls-reachability.md#a-nested-jail-is-structurally-blind-to-this).
- **Run the boundary broker's host-store slice once in a nested jail.** From a throwaway workspace
  with a throwaway home, confirm a forwarded write still exits 77 and files nothing, and that
  `yolo approve` list, status, deny and revoke work on host-side test records and refuse inside a
  jail. Never run a real GitHub write. From `boundary-broker-plan.md`, deleted 2026-10-16. Owner:
  [grants and the request store](../design/boundary-broker.md#7-grants-and-the-request-store).
- **File the vantage-check defect report upstream.** Its reference rules read a section or
  question number ending in a letter (such as 4.3b) without the letter, so they reject the correct
  link and accept a wrong one; they accept a link labeled with one section number that points at
  another; and they never report a heading-form open question as missing its directive. All three
  still reproduce on the pinned 0.10.0 (re-run 2026-10-16), and the report was never filed. Its
  text is in git history (`git show 4acb71205:docs/research/vantage-check-0.5.9-findings.md`);
  raise the pin once they are fixed. From `vantage-check-0.5.9-findings.md`, deleted 2026-10-16.
  Owner: [the lettered-number note](README.md#vantage-checks-suggested-link-is-wrong-for-a-lettered-number).
- **Keep config-verb tests off the jail's own workspace.** Inside a jail a config edit writes to
  the jail's workspace (`YOLO_WORKSPACE`, else `/workspace`), not the current directory, so a test
  that sets `YOLO_VERSION` and leaves `YOLO_WORKSPACE` unset edits the real project config when run
  in a jail. One did, on 2026-11-01, and was caught and reverted. Make the shared test helpers pin
  `YOLO_WORKSPACE` to the test's own temporary workspace, or refuse to run otherwise. Owner:
  [config commands](../reference/config/editing.md).

## Rulings — ALL ANSWERED 2026-07-26

Nothing is blocked on a decision any more. Where each ruling now lives: 1 is
[Ruling 1](../reference/config-migration-to-prism.md#ruling-1), 2–4 are
[in `pack-system.md`](../reference/pack-system.md#open-rulings), and 5 is
[`A12`](../reference/jail-home.md#why-its-this-way); the options weighed are in
`git log -- docs/plans/open-rulings.md`. The numbers are the original rulings doc's, which
`pack-system.md` uses too.

1. **First-migration vs discard** → `reset` also truncates the surface to the pure render.
   Unblocks stage B.
2. **Pack state scope** → **two tiers, both by design**: per-workspace by default,
   **machine-global for identity/credential state** — claude auth is deliberately shared across
   all workspaces and jails via a symlink out to `GlobalHome/.claude-shared-credentials`
   (the claude pack's `shared_credentials` hook, `HookSharedCredentials` in `internal/entrypoint/packhooks.go`; `assemble.go:174-175`). Pack selection stays user-level;
   the machine-global tier becomes a **pack-declared field** (new item B5); removal leaves
   abandoned per-workspace state in place, deliberately and with a report.
3. **Where composition runs** → **split by dependency, not preference.** Image-build inputs and
   host-file reads on the host; everything else **stays in the container**. Deletes the D1 port;
   replaces it with host-side pack validation.
4. **Re-render while running** → **not supported.** This was ruling 3's premise, so it needs no
   separate work.
5. **Config/pack generator failure** → **fatal: loud and halting, the jail does not start.**
   Removes `genStep`'s fail-open behavior. New item **A12**, and it retires the biggest stated
   risk of keeping composition in-jail.

## Suggested order

**F1 → A1 → A8+A10 → A9 → A11 → A12 → rest of A → B1–B5 → C1–C5 → C6 → D1 → D2–D7 → F-rest.**

**F1 first** — it is a live credential-boundary hole, independent of packs, and packs make it
worse if the pack-enable list inherits `agents`' merge semantics.

**A12 before stage C**, so packs land in a world where a broken pack halts loudly instead of
warning into a running jail.

A is ~11 items of mostly one-sitting work that fixes five verified defects and removes both
pack blockers. B is now unblocked. C6 is the cheap experiment that de-risks the expensive part.
D1 (host-side pack validation) should land **before** D3, so the first official pack cannot
fail silently at boot.
