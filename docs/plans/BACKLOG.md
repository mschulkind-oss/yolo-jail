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

## Stage D — the rip-out — ✅ mechanisms COMPLETE (2026-07-27)

**D1 is RESOLVED and mostly deleted.** Composition **stays in the container**; only
image-build inputs and host-file reads run host-side. There is no port. See
[open-rulings.md](open-rulings.md) ruling 3.

| # | Item | Gate |
|---|---|---|
| ✅ D1 | ~~Decide where composition runs~~ → **replaced by: host-side *validation* of pack contributions** at `yolo check` + run assembly, so a bad pack is caught before the container starts. Precedent: `checkHostFileLayer`/`checkHostFileDest` | defense in depth, now that A12 makes failures fatal |
| ✅ D2 | Three engine mechanisms: `stateful`, `computed`, `read_modify_write` | needs B2 |
| ✅ D3 | Agent registry + surfaces + skills + briefings become official packs | **the SEAM is built and proven**: `manifest.DecodeSurfaces` + `agentcfg.ManifestWith` let data-defined surfaces compose and override builtins, through the same validation. Migrating the shipped agents' definitions from Go literals into embedded pack files is now mechanical — and is a separate, revertable step per agent |
| ✅ D4 | `AgentSpec.HostFiles` becomes pack data | **the GATE is built**: `PackEntry.MayGrantHostFiles()` keys on content ORIGIN — embedded and local may grant, fetched never. The two docs contradicted each other here; resolved 2026-07-27. Scope alone was NOT sufficient: it covers the config channel, not the content channel |
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

## Stage E — parked design work

| # | Item | Status |
|---|---|---|
| E1 | `host_files` modes 4→3 (`copy` merges into `readonly`) | open — behavior change on a shipped key, blocked on E2 |
| E2 | `readonly` as a real `:ro` mount instead of `0o444` | open — needs a per-surface design pass; you cannot compose *into* a `:ro` mount |
| E3 | Capture timing (`yolo config capture` + capture on terminate) | open, **not urgent** — nothing is lost today, only observability lags |
| E4 | Comment preservation on `json`/`toml` surfaces | open — starts from decisions, not blank |
| E5 | `managed`/`defaults` array-append pinning | open — no user surface has needed it |
| ✅ E6 | ~~Non-agent prism ports (MCP/LSP/identity)~~ | **premise stale.** MCP and LSP *are* ported — they ride the **computed layer** into per-agent surfaces (`copilot/mcp`, `copilot/lsp`, `agy/mcp`), which is the right model: a standalone `mcp` surface would have no file of its own to write. `identity` is deliberately **host-composed and `:ro`-mounted** (`gitIdentityMountArgs`), settled by the identity-prism decision. `config render mcp` reporting "no surfaces" is therefore correct, not a gap |
| ✅ E7 | Renaming the recovered state | **done** — three live terms are NOT synonyms (act / state / layer); vocabulary defined rather than flattened |

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
