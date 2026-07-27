# Backlog — the one implementable list

**Status:** the single entry point for *what to build next* on the composed-config / packs
cluster. 2026-07-26.

**Why this exists.** The design work produced 8 docs / ~4,800 lines, and the actionable items
ended up spread across three of them. This file is the only place that answers *"what do I
pick up?"* Everything else is reasoning, and is linked per item.

**Rule:** an item lives here once. Reasoning lives in the design doc. Sequencing lives in
ROADMAP. Nothing gets three homes.

---

## Where the reasoning lives

| Doc | Answers | Read it when |
|---|---|---|
| [../design/composed-file-permissions.md](../design/composed-file-permissions.md) | ro/rw postures, the Derived/Shared/State taxonomy, the defect audit, writer classes | touching any composed file's permissions or the capture overlay |
| [../design/packs-and-the-prism.md](../design/packs-and-the-prism.md) | what packs *are*; provision vs compose phases; the 4 contribution kinds; typed exports between packs | deciding pack shape |
| [../design/what-yolo-is.md](../design/what-yolo-is.md) | subsystem boundaries; where composition could run; how logic ships | deciding *where* something executes |
| [../design/three-decisions.md](../design/three-decisions.md) | the three open decisions in depth; the 3 engine mechanisms; the 5 projections | before starting any pack work |
| [../design/third-party-pack-logic.md](../design/third-party-pack-logic.md) | the projector protocol; build/source tiers; trust | implementing pack logic |
| [agent-config-packs.md](agent-config-packs.md) | the concrete pack proposal: fetch, lockfile, staging, verbs | implementing pack plumbing |
| [composed-config-work.md](composed-config-work.md) | per-item detail for the prism items below | implementing a prism item |
| [packs-rip-out.md](packs-rip-out.md) | what design remains before the rip-out | scoping the rip-out |

---

## Stage A — prism prerequisites (unblocked, start here)

Nothing in this stage needs a decision. All of it is prerequisite to packs, so doing packs
first means porting known defects into a new mechanism.

| # | Item | Kind | Size |
|---|---|---|---|
| A1 | Remove the `gemini` agent | subtractive | medium (~8 files) |
| A2 | Reserve symlink targets (`~/.config/git/config`, `~/.claude/claude.json`) | defect | small |
| A3 | Mark `claude/config` non-rendered | defect | small |
| A4 | Fix `writeInPlaceString`'s umask claim | defect (latent) | small |
| A5 | Make `~/.gitconfig`'s unwritability legible | defect | small |
| A6 | Fix `config-ref`'s `reset`-re-seeds-`once` promise | docs lie | small |
| A7 | Feed `config render` the overlay + computed layers | defect | small–medium |
| A8 | Give `pi`/`codex`/`opencode` a skills dir | defect | **two lines** |
| A9 | Wire `Surface.Transform` — a documented key that does nothing | defect | small |
| A10 | Steer directed agents at composed surfaces (skills + header) | improvement | docs-only |
| A11 | Parameterize `/workspace` out of `builtin.go` | blocker for packs | small |

**Do A1 first** (subtractive — shrinks every later table, and deletes one of the five
projections A-stage work must satisfy). **Do A8 + A10 together** (A10's guidance is worthless
to three agents until A8 lands). **A9 and A11 are the two that gate pack work**: A9 because
the per-surface Lua seam has never executed and pack projections would be built on it, A11
because surface data can't become pack data while it hardcodes the jail path.

## Stage B — the data-loss chain (one decision, then ordered)

**Gate:** how does the engine tell "first migration" from "the user asked to discard"? Both are
"no baseline" today, so adopt-on-first-migration would make `yolo config reset` a no-op.
Cheapest fix: `reset` also truncates the surface to the pure render.
Detail: [composed-config-work.md §2.1](composed-config-work.md).

| # | Item | Kind |
|---|---|---|
| B1 | Adopt-on-first-migration (one branch in `staterender.go`) | ⚠ data-loss fix |
| B2 | De-compose the credential surfaces onto read-modify-write | ⚠ data-loss fix |
| B3 | Separate durable overlay state from the one-boot `last_render` baseline | naming |
| B4 | Sidecar location / scope — needs a per-workspace-vs-per-machine ruling | improvement |

**B2 is load-bearing for packs**: it is also the third engine mechanism
(`read_modify_write`) that agents-as-packs needs, so it earns its place twice.

## Stage C — the pack foundation

Buildable once A is done. Does **not** need the composition-site decision (D1).

| # | Item | Notes |
|---|---|---|
| C1 | `packs` config key, user scope only, `file://` sources | no workspace scope — settled |
| C2 | Tree executor: walk, `only`/`exclude`, exec-bit refusal, copy | |
| C3 | `PrepareSkills` + `ComposeBriefing` packs pass | delivers A8/A10's value via packs |
| C4 | `yolo pack init\|lint\|ls\|explain` | authoring loop |
| C5 | Git sources: `internal/packsrc`, lockfile, approval, `add/install/update/rollback` | the ~1-week chunk |
| C6 | Port the 5 real projections to the declarative operation set | **validates the design** — do before freezing the op set |
| C7 | The projector protocol (tier 2 escape hatch) | only if C6 proves the op set insufficient |

**C6 is the highest-information item in the whole plan.** If `conditional-OMIT` vs
`tombstone-null` or the cross-type LSP→MCP derivation won't fit, the operation set is wrong,
and learning that costs far less now than after packs ship.

## Stage D — the rip-out

| # | Item | Gate |
|---|---|---|
| D1 | **Decide where composition runs** (host-side vs in-jail) | the fork — reprices everything below |
| D2 | Three engine mechanisms: `stateful`, `computed`, `read_modify_write` | needs B2 |
| D3 | Agent registry + surfaces + skills + briefings become official packs | needs A11, C1–C6 |
| D4 | `AgentSpec.HostFiles` becomes pack data | safe *because* packs are user-scope only |
| D5 | No agent by default | already works — `agents: []` boots (verified) |
| D6 | Make the MCP bootstrap a pack contribution | it currently installs 112 npm packages for zero agents |

## Stage E — parked design work

`host_files` modes 4→3; `readonly` as a real `:ro` mount (cheaper after D1); capture timing;
comment preservation; array-append pinning; non-agent prism ports (MCP/LSP/identity);
renaming the recovered state.

---

## Open rulings still owed

**Full context, options and a recommendation per ruling: [open-rulings.md](open-rulings.md).**

1. **First-migration vs user-asked-to-discard** — gates all of stage B. Recommendation:
   `reset` also truncates the surface to the pure render (adds no new state).
2. **Is agent/pack state per-machine or per-jail?** **Correction:** it is already
   *per-workspace* — `<workspace>/.yolo/home/<agent>/` holds the real state and the
   `GlobalHome` entries are empty mountpoints (verified). Only
   `.claude-shared-credentials` is machine-wide. Recommendation: packs follow the
   per-workspace pattern; removal does not chase per-workspace effects.
3. **Where composition runs** — D1, the largest call. Recommendation: host-side, keeping
   binary installs in-jail.
4. **Does a running jail need to re-render?** Only bites if 3 goes host-side.
   Recommendation: re-render at run start only.

## Suggested order

**A1 → A8+A10 → A9 → A11 → rest of A → B (after its gate) → C1–C5 → C6 → D1 → D2–D6.**

A is ~11 items of mostly one-sitting work that fixes five verified defects and removes both
pack blockers. C6 is the cheap experiment that de-risks the expensive part. D1 is the one
decision worth taking time over.
