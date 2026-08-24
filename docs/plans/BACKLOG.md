# Backlog — the one implementable list

**Status:** the single entry point for *what to build next* on the composed-config / packs
cluster. Created 2026-07-26; **restamped 2026-08-23** (header + Stage E + Stage G).

**Why this exists.** The design work produced 8 docs / ~4,800 lines, and the actionable items
ended up spread across three of them. This file is the only place that answers *"what do I
pick up?"* Everything else is reasoning, and is linked per item.

**Rule:** an item lives here once. Reasoning lives in the design doc. Sequencing lives in
[roadmap.md](roadmap.md). Nothing gets three homes.

**Corollary added 2026-08-23:** an open *question* lives in a doc too — with stakes, a
leaning, and an empty `**Answer:**` — and `roadmap.md` links to it by ID rather than restating
it. Where a question has no design doc of its own, **Stage E below is that doc.**

**Current state (2026-08-23).** Stages A–F are complete, and the pack-declaration reform
that produced today's `contributes[]` design is shipped (see
[../design/pack-system.md](../design/pack-system.md)).

**Stage G is no longer this file's problem, and its headline bug is fixed.** The old header
here said *"Stage G — host-side composition — is the one substantive open pack-adjacent
stage, and G1 within it is a live data-loss bug"*; both halves of that are now stale. Stage G
moved into [environment-manager-plan.md](environment-manager-plan.md) on 2026-07-31, and that
plan's own build status records **Phases 0, 1, 2, 3 and 9 as SHIPPED, and Phases 4, 5, 6 and 8 as
PARTIAL** (re-verified against the tree 2026-08-23 — the flat "Phases 0–6, 8, 9 SHIPPED 2026-08-01"
summary this used to restate has been retracted in the plan itself).
Spot-checked against the tree, verified 2026-08-23:

- **G1/G2 (Phase 0) is FIXED** — `refuseHostSideWrite` (`internal/cli/configdiff.go:84-93`)
  refuses host-side `config reset`/`capture` without `--force`, wired at `configdiff.go:645`
  and `:848`.
- **G4/G5/G3 (Phase 1) shipped** — `internal/render/` exists with `target.go`, `fieldset.go`,
  `modes.go`, `confinement.go`.
- **Phase 2 shipped** — the `confinement` key at `internal/config/confinement.go:3-45`.
- **G6 (Phase 4) shipped** — `apply --host` at `internal/cli/apply.go:63`; `--sealed`
  (Phase 5) at `apply.go:69`.
- **Phase 7 (the `guest` notch) is the one unbuilt phase**, and it is host/Mac-gated rather
  than design-blocked: `internal/render/modes.go:185` still answers `KindGuest` with
  `UndecidedModes(...)`, whose reason string reads *"the guest notch's mode policy is Phase
  7's to state"*.

**So the one substantive open stage left in this file is Stage E**, which as of today holds
**seven open questions** — the parked `host_files` follow-ups plus four questions
(`S5`, `OQ-CO`, `OQ-S4`, `OQ-E4`) that until now had no doc home anywhere and were being
restated in [roadmap.md](roadmap.md) instead of linked from it.

---

## Where the reasoning lives

| Doc | Answers | Read it when |
|---|---|---|
| [../design/composed-file-permissions.md](../design/composed-file-permissions.md) | ro/rw postures, the Derived/Shared/State taxonomy, the defect audit, writer classes | touching any composed file's permissions or the capture overlay |
| [../design/pack-system.md](../design/pack-system.md) | **the pack system, whole**: the `contributes[]` manifest, the **fifteen** kinds + footprints + conflict rules (ten when this row was written; the count is pinned by `packdecl/kinds_test.go`), the one-writer rule, the compose engine, `derive`, and selection/fetch/origin-gate | authoring, debugging, or changing a pack; changing the schema or a kind |
| [../design/yolo-as-environment-manager.md](../design/yolo-as-environment-manager.md) | **what yolo is**: confinement as a `jail`/`sandbox`/`host` dial rather than a `runtime` backend choice, the description as the product, the verbs (`apply`/`describe`/`diff`/`check --at`), what a pack means per notch, and what the wider identity costs | deciding whether a feature is jail-shaped, adding a config key, or writing user-facing copy |
| [../design/environment-manager-user-stories.md](../design/environment-manager-user-stories.md) | the same design **from the outside**: five worked stories (drift across two machines, a Mac fleet rollout that hits G3 then G1, the minimal-ladder user, the agent reading its own briefing at `host`, a security questionnaire) — plus **11** open questions the stories surfaced (Q1 · Q1a · Q1b · Q2–Q9; the row said 8) | pressure-testing a verb's output, or deciding what `apply`/`describe` must print |
| [../design/host-render-target.md](../design/host-render-target.md) | **the host as a reduced render target**: the two duplicated render paths and the one `Target`-parameterized renderer that replaces them, which manifest fields even apply off-container, the confinement axis (jail / macos-user / host), `FieldSet` | adding a backend, touching host-side `config reset`/`capture`, or changing the boot render |
| [../design/what-yolo-is.md](../design/what-yolo-is.md) | subsystem boundaries; where composition could run; how logic ships | deciding *where* something executes |
| [../design/third-party-pack-logic.md](../design/third-party-pack-logic.md) | the projector protocol; build/source tiers; trust | implementing pack logic |
| [agent-config-packs.md](agent-config-packs.md) | the concrete pack proposal: fetch, lockfile, staging, verbs | implementing pack plumbing |
| [composed-config-work.md](composed-config-work.md) | per-item detail for the prism items below | implementing a prism item |

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
**Stage B is fully unblocked.** Detail: [open-rulings.md](open-rulings.md) ruling 1,
[composed-config-work.md §2.1](composed-config-work.md).

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
launching a jail with its pack delivered.

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
[open-rulings.md](open-rulings.md) ruling 3.

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

Reasoning: [../design/host-render-target.md](../design/host-render-target.md) — §3 is the
design (one renderer, several targets), §6 is the finding these items came from.

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
nothing"*) was true when it was written and is now history — G1/G2 are fixed at
`internal/cli/configdiff.go:84-93`. Original order G1 → G2 → G4 → G3 → G5 → G6 was preserved
as Phase 0 → 1 → 4. **Extracting any of this into a separate util
is settled: no** (`host-render-target.md` §2.3, decided 2026-07-27) — the field census puts the
boundary through the middle of a single manifest, so the capability lives in yolo as
`internal/render`.

## Stage E — parked design work, and the questions with no other home

**Restamped 2026-08-23.** This stage is now two things at once: the parked `host_files`
follow-ups (E1–E5) it always held, and **four questions that had no design-doc home anywhere**
— `S5`, `OQ-CO`, `OQ-S4`, `OQ-E4`. Those four were carried as a single line in
[roadmap.md](roadmap.md) ("the small ones with no design-doc home"), which breaks that file's
own governing rule: *a question lives in its design doc, with stakes and a leaning, and the
roadmap links to it by ID*. They live here now, in full, so the roadmap can cite them and stop
restating them. **This section is their doc.**

> [!IMPORTANT]
> **IDs are an API — do not renumber these into the E-series.** `S5`, `OQ-CO`, `OQ-S4` and
> `OQ-E4` keep the exact spellings they were born with in the deleted `outstanding-work.md`
> (last intact at commit `58ae8227`, 2026-08-13). They are cited today by
> [shipped-2026-08-12.md](shipped-2026-08-12.md) (§S4 and the E4 entry) and by
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
| ✅ E4 | Comment preservation on `json`/`toml` surfaces | **mostly shipped 2026-08-12**; the one live residue is `OQ-E4` |
| **E5** | `managed`/`defaults` array-append pinning | **open** — speculative; the named trigger has not fired |
| **S5** | A jail resolves a skill-name collision silently | **open** — live gap, the only place the S1 silent loss survives |
| **OQ-CO** | Two packs writing one `config-overlay` key | **open** — nothing blocked; no shipped pack collides |
| **OQ-S4** | Should the jail narrow its skills fan-out to match the host? | **open** — a product call about what `into` promises |
| **OQ-E4** | Do `stateful` surfaces get comment preservation too? | **open** — `rmw` shipped, `computed`/`json` ruled out |

---

### 💬 **E1 — collapse `host_files` modes 4→3 (`copy` merges into `readonly`)**

Also **E2** and **`pack-host-management-plan.md` OQ-B**: *these three are one decision.* See
the shared block below.

`host_files` still accepts four modes — `readonly`, `once`, `copy`, `capture`
(`internal/config/hostfiles.go:56-61`, verified 2026-08-23). E1 asks whether `copy` earns its
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

### 💬 **E2 — `readonly` as a real `:ro` mount instead of `0o444`**

**This is the same underlying decision as E1 and as `OQ-B` in
[pack-host-management-plan.md](pack-host-management-plan.md) (line ~940). Decide all three
together or none of them** — OQ-B's own leaning already says so.

The asymmetry, stated once: **`0o444` is a posture; `:ro` is enforcement.** `0o444` is DAC
only, and yolo's own docs already admit it — `internal/cli/config_ref.txt:539`: *"Note 0444 is
DAC, not kernel enforcement: an agent running as root (Claude YOLO runs UID 0) bypasses the
mode bits"*, and anyone can `chmod +w`. Three places in the tree make the same trade today
(all verified 2026-08-23):

| Site | What it does | Which ID |
|---|---|---|
| `internal/entrypoint/hostfiles.go:112-157` | `host_files` `readonly` renders `0o444` (`0o555` if the source is executable) every boot | **E2** |
| `internal/config/hostfiles.go:40-56` | the four-mode vocabulary that names it | **E1** |
| `internal/entrypoint/hostfilestree.go:182-200` | pack-delivered `files` at the host notch: *"READ-ONLY (0o444/0o555) mirrors the jail's `:ro` mount, which is the closest a plain [mode] can get"* | **OQ-B** |

`hostfilestree.go:157` is the asymmetry in one line: the writer has to **chmod a `0o444` file
back to writable to reopen it**, which is precisely the move a user who wants to hand-edit
will make — and then lose on the next apply.

**Stakes.** The blocker is structural, not effort: **you cannot compose *into* a `:ro`
mount.** Making `readonly` a real mount means the composed output has to be produced in a
staging tree and bound in, which changes the delivery path for every `readonly` entry and
re-opens how `packstage` and `hostfilestree` deliver. Against that: `0o444` makes a promise
the product does not keep for the one agent that matters — Claude YOLO runs UID 0 and walks
straight through it.

_Leaning:_ **keep `0o444` everywhere and fix the WORDS instead.** Rename the posture from
"read-only" to what it is (asymmetric / advisory), which `config_ref.txt:539` already says in
a note nobody reads at the point of decision. The enforcement upgrade is real work on a live
boot path and buys nothing against the threat model that actually applies (a root agent), so
it should wait for a threat it defeats. **What would change my read:** the `guest` notch
(env-manager Phase 7) landing with a non-root agent, where DAC bits *are* enforcement.

**Answer:**
> _(empty — fill in when decided)_

### ✅ **E3 — capture timing. SHIPPED 2026-08-15, both halves.**

**This row was stale and is now closed.** The old entry said *"open, not urgent — nothing is
lost today, only observability lags"*. Verified against the tree 2026-08-23:

- `yolo config capture` (the on-demand half) — `internal/cli/configdiff.go:843`.
- **Capture on terminate** (the half the entry was actually about) —
  `internal/cli/configcapture.go`, whose opening line is literally *"configcapture.go is E3's
  second half: capture on TERMINATE"*. Shipped in commit `29ccf212`, 2026-08-15,
  *"feat(config): capture in-jail config edits when a jail terminates (E3)"*.
- **It is wired, not orphaned** — `internal/cli/commands.go:572` injects it as
  `run.Options.CaptureOnTerminate`, with the comment naming E3.
- It ships **unconditional, not opt-in**, and `configcapture.go:10-45` records the four-point
  argument for that (idempotent with the boot capture; only touches `mode: capture` surfaces;
  the gap is a default-correctness property of a *reporting* command; and BACKLOG's own
  "nothing is lost today" is the reason every failure warns and proceeds rather than a reason
  to gate it).

Nothing left to decide. Kept as a row rather than deleted because the E-numbers are cited
elsewhere.

### ✅ **E4 — comment preservation. Mostly shipped 2026-08-12; the residue is `OQ-E4`.**

**Why `E4` is absent from the roadmap's list while `OQ-E4` is present.** They are not the same
item. `E4` was "comment preservation on `json`/`toml` surfaces" across all modes; three of its
four cases are now closed, and the fourth was promoted to its own question ID:

| case | ruling | evidence (verified 2026-08-23) |
|---|---|---|
| `rmw` | **preserve** — shipped | `internal/entrypoint/tomltrivia.go`, whose header reads *"tomltrivia.go is E4's `rmw` half"*; drops are reported by key via `HostRenderResult.Formatting` (`internal/entrypoint/hostrender.go:104-113`) |
| `computed` | **do not preserve, and that is correct** | yolo is sole author, so any comment would be one *yolo wrote* — a different feature |
| `json` (any mode) | **provably vacuous** | strict JSON has no comment syntax, so a commented file never decodes and the RMW path refuses it byte-untouched; now pinned by a test |
| `stateful` | **still open** → **`OQ-E4`** | see below |

Full argument: [host-file-staging.md](host-file-staging.md) §*"What shipped: option 3, for
`rmw` only"*, and [shipped-2026-08-12.md](shipped-2026-08-12.md) §E4.

### 💬 **E5 — `managed`/`defaults` array-append pinning**

`managed`/`defaults` merge is RFC-7386 object merge at every depth
(`internal/agentcfg/engine.go:39-49`, verified 2026-08-23), which means **an array in a
`managed` layer REPLACES the on-disk array wholesale**. So a pack that wants to *add one
entry* to a list the user also maintains cannot: it either clobbers the user's whole list or
leaves the key alone. Settled as deferred at implementation time —
[host-file-staging.md](host-file-staging.md), *"Resolved during implementation"*: *"Array-append
pinning was deferred: no user surface has needed it. Still open if one does."*

**Stakes:** low today and self-limiting. The trigger is named and has not fired — no shipped
surface needs it. The risk is in the *fix*, not the gap: changing `mergeValue` globally would
alter every shipped surface's render and move `TestRenderFingerprintStable`.

_Leaning:_ **do not build it speculatively.** When something needs it, the shape is a per-key
merge-strategy annotation on the `managed` declaration (opt-in, one surface at a time), never
a change to the engine's default merge. Recording that shape now is the whole value of leaving
this row open.

**Answer:**
> _(empty — fill in when decided)_

### 💬 **S5 — a jail resolves a skill-name collision SILENTLY**

**Context for a reader who has never seen the roadmap.** Two selected packs can each ship a
skill directory with the same name aimed at the same destination (`~/.claude/skills/review`
from both). The two notches answer that differently, and only one of them says anything:

- **Host (`apply --host`) — FATAL, before anything is written.**
  `hostskills.RenderHostSkills` calls `Collisions(dests)` first and returns `CollisionError`
  with every collision named, so one run tells the user every rename to make
  (`internal/hostskills/compose.go:698-700`). This is the S1 ruling.
- **Jail — silent last-one-wins.** `jailcontent.PrepareSkills` loops `packSkillDirs` in config
  order (`internal/jailcontent/skills.go:107-118`) and `copySkillSubdirs` does
  `os.RemoveAll(target)` then copy (`skills.go:154-160`). There is no collision concept on any
  jail path: `RenderHostSkills` — the sole caller of `Collisions` — is reached only from
  `internal/cli/applyhostskills.go:253`. **Verified 2026-08-23.**

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

**Answer:**
> _(empty — fill in when decided)_

### 💬 **OQ-CO — two packs writing one `config-overlay` key is silent last-one-wins**

**Context.** `config-overlay` is the kind that lets pack B contribute keys to a surface pack A
owns (the `matt-fzf` → `claude/settings` `fileSuggestion` case). Overlays fold in after the
workspace layer **in pack order, later wins** (`internal/agentcfg/compose.go:362-369`), and the
kind's combine rule is `CombineOverlay`, which is **deliberately non-colliding** —
`internal/packdecl/kinds.go:156-158`, and `internal/packload/footprint.go:247`: *"It does not
collide (CombineOverlay), so it is a claim line only."* **Verified 2026-08-23.**

So two packs claiming the same *surface* is the feature. Two packs claiming the same **key**
with different values is undetected, and the loser is never told.

**What is already mitigated, and what is not.** Per-key provenance IS recorded — the winning
layer is labelled `config-overlay:<pack>` (`internal/agentcfg/compose.go:170-176`) and
`yolo config diff` prints it. That is **after-the-fact reporting, not a refusal**, and at the
HOST notch the annotation is *inferred rather than measured*:
[pack-config-collaboration.md](../design/pack-config-collaboration.md) §8 *"Still open after
Option 2"* measures it printing `fileSuggestion contributed by fzf-overlay but managed won`
when the overlay's value is the one that actually landed and no `managed` value existed.

**Stakes.** Nothing is blocked. No shipped pack collides — all declare disjoint identities
(§9 *"What it did NOT change"*). It is worth deciding anyway because the **neighbouring kind
answers the same shaped question the opposite way**: since 2026-08-02 a same-identity `config`
declaration is a LOUD collision, named in `yolo pack footprint` and refused at launch and by
`apply --host` (§9). Two adjacent kinds with opposite silence policies is the drift.
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

### 💬 **OQ-S4 — should the jail narrow its skills fan-out to match the host?**

**Or, stated as the real question: does a declaration NARROW delivery, or only ADD to it?**

**Context.** A `skills` contribution may name an `into` (a destination directory). The two
notches disagree about what that declaration means, measured by the S4 audit
([shipped-2026-08-12.md](shipped-2026-08-12.md) §S4, which also says loudly **"AUDITED: the
gate holds. Do not re-audit this"** — this is a fan-out question, not a security one):

- **Jail:** *every* loaded pack's skills reach *every* declared destination.
  `PrepareSkills` copies every entry of `packSkillDirs` into every `packSkillTarget`
  (`internal/jailcontent/skills.go:92-118`).
- **Host:** a pack's skills reach only the destinations that pack declared —
  `packload.ResolveDestinations` (`internal/cli/apply.go:256`). Verified 2026-08-23:
  `ResolveDestinations` has **no caller** under `internal/cli/run` or `internal/entrypoint`.

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

_Leaning:_ **(2)**, because it makes both notches answer from one inference instead of two,
makes `into` mean what it says, and makes `yolo pack footprint` true — the same argument F1
used to give the host the jail's inference, applied in the other direction. **(1) is the
honest fallback and is worth doing either way**, since even under (2) the footprint should say
what a *borrowed* destination is.

**Answer:**
> _(empty — fill in when decided)_

### 💬 **OQ-E4 — do `stateful` surfaces get comment preservation too?**

**Context.** The residue of E4 above. `rmw` preserves comments (shipped 2026-08-12,
`internal/entrypoint/tomltrivia.go`); `computed` correctly does not; `json` is provably
vacuous. `stateful` is the remaining mode and it is **a different problem wearing the same
words**: the file is composed from layers, so a comment can only come from the `host` layer,
and putting it in the render is a **projection out of one file into another** rather than an
in-place edit. `tomltrivia.go:24` and `:60` name exactly this, and note that the emitter
`stateful` would have to change is also the one the A12-fatal boot path and the render
fingerprint gate depend on.

**Stakes.** Three options, priced in [host-file-staging.md](host-file-staging.md):

1. **Do it.** `Codec` grows an optional `TriviaCodec`; trivia rides the compose result; the
   staleness rule ① is keyed on `Result.Provenance` (emit a comment only where the winning
   layer is `host`), which is already computed — one map lookup per key. *Cost:* it lands on
   the A12-fatal boot path, and it needs an answer for the **Lua transform boundary** — a hook
   returns a table, so trivia must either survive that or be documented as dropped by any
   transform.
2. **Extend the yolo-authored header that is already there** to point at the `:ro` original.
   Measured 2026-08-12: the composed `~/.codex/config.toml` already opens with *"Generated by
   yolo-jail — composed at jail start; hand edits may be reverted or lost…"*
   (`internal/entrypoint/prism.go:400`, verified 2026-08-23). What it does not carry is the
   `/ctx/host-user/<slug>` path to the untouched source. *Cost:* near nothing — but strict
   JSON has nowhere to put a header line, so it serves `toml` only.
3. **Leave it, and say so.** `raw` already round-trips a hand-written file byte-exact, and
   `config-ref` already documents the structured-codec trade. *Cost:* none new.

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

---

### Settled, kept for the record

| # | Item | Disposition |
|---|---|---|
| ✅ E6 | ~~Non-agent prism ports (MCP/LSP/identity)~~ | **premise stale.** MCP and LSP *are* ported — they ride the **computed layer** into per-agent surfaces (`copilot/mcp`, `copilot/lsp`, `agy/mcp`), which is the right model: a standalone `mcp` surface would have no file of its own to write. `identity` is deliberately **host-composed and `:ro`-mounted** (`gitIdentityMountArgs`), settled by the identity-prism decision. `config render mcp` reporting "no surfaces" is therefore correct, not a gap |
| ✅ E7 | Renaming the recovered state | **done** — three live terms are NOT synonyms (act / state / layer); vocabulary defined rather than flattened |
| ✅ E8 | ~~Nightly-macOS builder arch mismatch~~ — **DONE 2026-08-03**. The original entry called this "a maintainer decision, not a fix"; that framing was wrong — `nightly-macos.yml:44` documents the Intel runner as the only hosted one with nested virt, testing the macOS *code paths* rather than Intel as a target, so it always had a technical answer. **(1)** `7cc54a0`: `publish.yml` pushes per-arch builder tags plus a multi-arch index, so `:latest` resolves on both arches. **(2)** `BuildersLine`'s advertised system was hardcoded `aarch64-linux` — *"the arch a Mac needs"* — so an x86_64 host connected to a working builder, was told it serves aarch64-linux, and nix declined to offload while the builder looked healthy. Now `BuilderSystem()` derives it from GOARCH. **(3+4) Two MORE instances of the same assumption, found by grepping the literal rather than trusting the entry's scope** — `nixdiag.HasLinuxBuilderFromConfig` decided "is a builder configured?" by matching `aarch64-linux`, so on x86_64 it reported *no builder* while one ran (and, with (2) fixed, would have accepted an arm64-only builder for an x86_64 build); and `DiagnoseNixBuildFailure` keyed its cross-build branch on the same literal, dropping x86_64 hosts to a raw stderr dump instead of the Linux-builder remedy. Both now take/match the wanted system. All four mutation-tested. **Remaining caveat, not a defect:** `publish.yml` is tag-triggered, so the multi-arch image does not reach GHCR until the next release — the nightly stays red until then, and that first nightly is the real proof | ✅ done, verified by the next release's nightly |

---

## Rulings — ALL ANSWERED 2026-07-26

Nothing is blocked on a decision any more. Context: [open-rulings.md](open-rulings.md).

0. **Config/pack generator failure** → **fatal: loud and halting, the jail does not start.**
   Removes `genStep`'s fail-open behavior. New item **A12**, and it retires the biggest stated
   risk of keeping composition in-jail.
1. **First-migration vs discard** → `reset` also truncates the surface to the pure render.
   Unblocks stage B.
2. **Pack state scope** → **two tiers, both by design**: per-workspace by default,
   **machine-global for identity/credential state** — claude auth is deliberately shared across
   all workspaces and jails via a symlink out to `GlobalHome/.claude-shared-credentials`
   (`entrypoint/claude.go:106-108`, `assemble.go:174-175`). Pack selection stays user-level;
   the machine-global tier becomes a **pack-declared field** (new item B5); removal leaves
   abandoned per-workspace state in place, deliberately and with a report.
3. **Where composition runs** → **split by dependency, not preference.** Image-build inputs and
   host-file reads on the host; everything else **stays in the container**. Deletes the D1 port;
   replaces it with host-side pack validation.
4. **Re-render while running** → **not supported.** This was ruling 3's premise, so it needs no
   separate work.

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
