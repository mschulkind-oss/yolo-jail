# ROADMAP — sequencing the active plans

**Date:** 2026-07-21. **Purpose:** one ordering for everything under
`docs/plans/`, so "what do I work on next?" has a single answer. Every
dependency below was checked against the plan docs **and** the code they cite
(re-verified 2026-07-21, after the Jul-21 J2/J3/consolidation/Track-M wave and
D2 landed) — where a claim rests on the tree, the file:line is named.

This is a **meta-doc**: it sequences the plans, it does not restate them. Each
plan remains the source of truth for its own work items. The
[macos-revival-and-distribution-plan.md](macos-revival-and-distribution-plan.md)
is the roadmap of record for the macOS/distribution effort (Tracks J/D/M); this
ROADMAP reconciles with its internal "Sequencing at a glance" and folds the
post-Go-port backlog (nix-ld, color audit, consolidation) into the same picture.

## The plans

| Plan | One-liner | Lane / status |
|---|---|---|
| [macos-revival-and-distribution-plan.md](macos-revival-and-distribution-plan.md) | Tracks J (Linux-jail fixes), D (distribution/source-access), M (Mac hardware). Roadmap of record. | **J1–J3 + D1/D2/D3 done, Track M M0/M1/M2 PROVEN on HW 2026-07-21; only D4-account remains.** |
| [handoff-cachix-cache.md](handoff-cachix-cache.md) | The revival plan's **D4**: publish the OCI image to a Cachix cache. | human-gated — substituter ENABLED (flake.nix:13-16, 730c258); only account + first push + Mac download proof left |
| [nix-ld-dynamic-linking.md](nix-ld-dynamic-linking.md) | Replace the `LD_LIBRARY_PATH` whack-a-mole with nix-ld; closes the custom-`mcp_servers` startup gap. | host-gated — **not started** |
| [agent-settings-composition.md](agent-settings-composition.md) | Layered regeneration of any generated config (agent settings, MCP, LSP, mise, identity) + a Lua transform. **Design FINALIZED 2026-07-20.** | jail-side — **engine BUILT (`internal/agentcfg`), Phase A+B done; NOT wired to boot; Phase C (deletion) + boot-wiring remain** |
| [cache-relocation.md](cache-relocation.md) | User-scope-only `cache_relocations` so a huge cold cache subdir can live on other storage; unblinds `prune`/`purge`. Podman behavior proven 2026-07-21. | jail-side (one host-gated acceptance step) |
| [cli-color-audit.md](cli-color-audit.md) | Shared rich→ANSI renderer + TTY gate across commands. | jail-side — **bug class fixed**; tail: migrate `run/console.go` off its private duplicate + unify the TTY probe |
| [module-consolidation-and-cleanup.md](module-consolidation-and-cleanup.md) | Collapse the parity-era `internal/*` split; drop parity machinery; §4 OSS-hygiene remnants. | **DONE 2026-07-21** (package-merge declined) |
| [integration-parallelism.md](integration-parallelism.md) | Bounded `t.Parallel()` for the container suite (needs per-test GlobalStorage first). | parked (test speed) |
| [runbooks/](runbooks/) | Track M verification procedures (see [Runbooks](#runbooks) below). | hardware-gated |

## Lanes — not everything is one linear sequence

Three lanes run in parallel; only the jail-side lane is a sequence. The other
two are gated on a resource an in-jail agent doesn't have.

- **Jail-side (agent-completable).** Developable and testable from inside a
  jail; `internal/` changes still get a nested-jail sanity run per AGENTS.md.
  With the Jul-21 wave landed, the only jail-side work left is the whole
  **config-composition** thread (Phase C + wiring the engine to boot) and the
  **cli-color-audit tail** (migrate `run/console.go` off its private printer +
  unify the TTY probe). J2, J3, D2, cli-color-audit's bug-class fix, and
  module-consolidation have all landed.
- **Host-gated (needs a human at a host with nix).** A `flake.nix` / image
  change that AGENTS.md says needs `just load && just install` on a real host
  and **cannot be validated in-jail**. Members: **nix-ld**, and any future image
  rebuild. Not a blocker on jail-side work — schedule it whenever a maintainer
  next has a host session.
- **Hardware/human-gated (needs a real Mac or a maintainer action).** No
  in-jail agent can complete these. Members: **D4 Cachix** (needs an account +
  the first push + one Mac download proof). Track M's M0/M1/M2 are already
  PROVEN on real Apple Silicon (2026-07-21), so the hardware gate is discharged
  for the current scope.

## Current state — what's already done

Marked here so the "start here" arrow points at the real next item.

- ✅ **J1.1–J1.4** (2026-07-20) — runtime unification, darwinpkg stderr drain,
  builder VM reaping, `yolo --help`. Each RED-then-GREEN; J1.1 nested-jail
  verified.
- ✅ **D1** (2026-07-20) — `just deploy` records `repo_path`; `check` honors it.
  Verified: `internal/repopath/` exists, wired into the install recipe.
- ✅ **D2 — graceful launch degradation** (2026-07-21) — repo-root resolution is
  no longer a hard gate. When it fails the launch proceeds degraded on a
  cached/loaded image (`image.AutoLoadOptions.SkipBuild`), the assembler drops
  the `/opt/yolo-jail:ro` bind + `YOLO_REPO_ROOT` behind one `repoBound` gate,
  and `Run` prints a soft notice instead of exiting 1 (commit 8f1d612).
  Nested-jail verified both paths: normal binds + rebuilds; degraded runs the
  cached image with neither.
- ✅ **D3** (2026-07-20) — Go-era source bundle ships so checkout-less installs
  build the image. Verified the staged tree evaluates.
- ✅ **CI green** (2026-07-20) — the `TestShimPersistence` failure (shim
  mount-anchor / `ClearContents`) is fixed and the four test-merges landed; the
  full CI run (both arches, integration incl.) passed.
- ✅ **cli-color-audit — bug class fixed** (2026-07-20/21) — the shared
  `internal/richtext` renderer landed and `prune`/`builder`/`macosuser`/`broker`
  (plus the top-level `cli`/`config`/`ps` commands) route through it with a TTY
  gate. **Not fully done:** `internal/cli/run/console.go` still carries the
  private `richTagRe`/`richToANSI`/`stripRich` that richtext was *extracted
  from* (it does not import richtext) — migrating it + unifying the two
  TTY-probe conventions is the remaining tail.
- ✅ **go baked into the image** (2026-07-20) — `imagePkgs.go` in corePackages,
  `miseBaseTools` now empty (all default runtimes baked). Built + evaluated
  in-jail AND green on both CI `build-image` arches.
- ✅ **Cachix D4 substituter enabled** (2026-07-20) — `nixConfig` substituter +
  key live at `flake.nix:13-16` (730c258); the CI push job self-enables once the
  `CACHIX_AUTH_TOKEN` secret exists. Account + first push + Mac download proof
  remain (human-gated).
- ✅ **config-composition Phase A + B** (2026-07-20/21) — engine (`internal/
  agentcfg`) + codecs + real gopher-lua sandbox VM + manifest landed; the
  exported `Compose` orchestrator + `yolo config render <agent> [--surface|
  --explain]` CLI cover every agent surface (pi/claude/gemini/copilot/opencode/
  codex) plus MCP/LSP/mise; `mergeAccumulate` tombstone fix. **Load-bearing
  caveat:** the engine is BUILT but **NOT wired to boot** — the only caller of
  `agentcfg.Compose` is `internal/cli/config.go` (the `yolo config render`
  preview). Boot still runs the bespoke `Configure*` writers in
  `internal/entrypoint` (nothing there imports agentcfg), and `yolo check` still
  validates via those same bespoke writers. No `host_*_files` key has been
  deleted. Phase C (deletion) + boot-wiring is the remaining work.
- ✅ **J2 — native-Go macos-user bootstrap re-port** (2026-07-21) — all four
  items (12d27cb/731dbe5/1e68e24/544a806/e65993a): platform literals threaded
  through `*Env`; darwin-native generation entry + Go writers; `yolo internal
  darwin-bootstrap` self-exec + launch-path swap (fresh-inode staging, Python
  machinery deleted, `RepoSrc`→`RepoRoot`); finding-6 password via stdin. Mac
  runtime behavior verified in **M1** on real hardware.
- ✅ **J3 — container-builder rewiring** (2026-07-21) — resurrected
  `internal/containerbuilder` (8abb67c) and wired the offload into
  `AutoLoadImage` (c2f0b94): a failed macOS from-source build retries over a
  container builder via ssh-ng before falling back. Behavioral e2e is the
  mac-ac-container-builder runbook (PASSED on HW).
- ✅ **module-consolidation-and-cleanup** (2026-07-21) — parity-era comments and
  machinery removed (743e053/d2b2db7/84b3e09); package-merge deliberately
  declined. `Status: DONE`.
- ✅ **Track M M0/M1/M2 — PROVEN on real Apple Silicon** (2026-07-21) —
  macos-user runs the agent under Seatbelt with native aarch64-darwin `packages:`
  (9933e7b/8763fd5/43bd846); OQ-1 (path_helper) and finding-6 (password apply)
  observed and passing. See `docs/research/macos-support-matrix.md`.
- ✅ **mise migration fix** (2026-07-20) — stale unpinned baked-runtime lines
  stripped on upgrade, workspace/injected pins preserved (nested-jail verified).

Everything else below is **open**: config-composition Phase C + boot-wiring,
the cli-color-audit tail, nix-ld (host-gated), and the D4-account human step.

## Recommended order (jail-side thread)

With J1/D1/D2/D3/J2/J3/consolidation all done, the jail-side lane has collapsed
to two independent items — there is no longer a critical-path chain:

1. **config-composition — Phase C + boot-wiring** — *the main remaining jail
   work; its own self-contained thread (see below).* The engine is built and
   fan-out (Phase B) is complete; what remains is wiring `agentcfg.Compose` into
   boot (`internal/entrypoint`) and `yolo check`, then the Phase C deletion of
   the bespoke merges and `host_*_files` keys. This is where the real design
   nuance lives — see the [config-composition build](#config-composition-build-own-self-contained-thread)
   section and `agent-settings-composition.md`.

2. ~~**cache-relocation**~~ — **DONE 2026-07-21.** Work items 1–10 landed
   (user-scope-only `cache_relocations`, nested rw bind mount, prune/purge
   accounting, docs) and were verified end to end in a nested jail: a write to
   `~/.cache/<subdir>` inside the jail lands on the relocated target and the
   host-side stub stays empty. `yolo cache relocate` (item 11) stays deferred;
   the plan's one host-gated acceptance step — a real cross-filesystem move
   confirming root-fs `df` drops — is still outstanding. Note for whoever picks
   up **cli-color-audit** or **module-consolidation**: this touched
   `internal/prune/prunecmd.go` and `report.go`, so rebase before starting there.

3. **J2 — native-Go macos-user bootstrap re-port (J2.1 → J2.4) + D2.** *The
   critical-path Mac-backend item; now unblocked (the CI fix cleared
   `internal/entrypoint`).* The dead piece is real: `internal/cli/commands.go:375`
   still sets `RepoSrc = repoRoot/src` and `internal/macosuser/runplan.go:152,175`
   still stage/require a `python3` interpreter — and the tracked `src/` tree no
   longer exists (`git ls-files src/` → empty; the untracked `src/` +
   `yolo_jail.egg-info/` in the tree are stale Python build artifacts, not the
   shipped source). J2.1 threads container literals through `*entrypoint.Env`
   and J2.2 adds a darwin-native generation entry — both touch
   `internal/entrypoint` (which the landed CI fix left green). D2 (graceful
   repo-root degradation) pairs naturally with J2 step 3 — both touch the run front door
   and the `RepoSrc` contract; land them together. J2's Mac-side behavior
   (password apply, path_helper OQ-1, fresh-inode re-exec) is verified in **M1**,
   not the jail.

4. **J3 — container-builder rewiring.** After J2 (macos-user needs no builder at
   all). Resurrect `internal/containerbuilder` from git history (verified GONE —
   deleted with zero importers) and wire it into `internal/image/autoload.go`.
   Jail-developable; its verification runbook
   ([runbooks/mac-ac-container-builder.md](runbooks/mac-ac-container-builder.md))
   is zero-sudo and agent-runnable, so Track M can confirm it from inside a
   sandbox — and that cell already **PASSED** on real HW (2026-07-17).

5. **module-consolidation-and-cleanup** — *last, by its own admission.* Collapse
   the Python-mirroring `internal/*` split and drop the parity machinery only
   **after** J2/J3 land, so it consolidates a settled tree rather than a moving
   one. This is where the shared rich→ANSI renderer belongs if cli-color-audit
   didn't already lift it.

### Coupling: cli-color-audit ↔ module-consolidation

Verified overlap — both plans call for the *same* shared color-aware rich→ANSI
renderer to replace the four+ near-duplicate `richTagRe` printers. They are
**deliberately the same deliverable seen from two angles**, and each doc points
at the other. Rule: whichever runs first lands the shared helper. If
consolidation runs first, do the renderer there; if the color audit lands first
(recommended — it's item 1), it lands the renderer and consolidation just
inherits it. Don't build it twice.

### cli-color-audit tail

**cli-color-audit tail** — *small, standalone.* Migrate
`internal/cli/run/console.go` off its private `richTagRe`/`richToANSI`/
`stripRich` (the last un-consolidated duplicate — richtext was extracted from
it) onto `internal/richtext`, and unify the two TTY-probe conventions. The
bug class is already fixed everywhere else; this is cleanup, no byte-parity
risk.

## Config-composition build (own self-contained thread)

[agent-settings-composition.md](agent-settings-composition.md) is **finalized**
and jail-side, independent of the macOS J/D/M tracks. Its shape is **serial
foundation, then parallel fan-out, then deletion** — and Phases A and B have
**landed**:

**Phase A — the engine (DONE).** Built as a leaf library: pure `decode`/
`deepMerge`/`enforce`/`render` per codec (json/toml), the locked-down gopher-lua
VM + `ctx` bridge, the manifest schema + loader, and the fixture corpus that is
the spec. `yolo config render` is the thin read-only CLI over it.

**Phase B — surface migrations (DONE).** Every agent surface (pi/claude/gemini/
copilot/opencode/codex) plus MCP/LSP/mise is modeled in the builtin manifest and
covered by `yolo config render`.

**Phase C — deletion + boot-wiring (OPEN — the remaining work).** The engine is
built but **not yet wired to boot**: boot runs the bespoke `Configure*` writers
in `internal/entrypoint`, `yolo check` validates through those same writers, and
nothing outside `internal/cli/config.go` imports `agentcfg`. Phase C wires
`Compose` into the boot + check paths, then removes the bespoke merges, snapshot
constants, per-agent mount blocks, and `host_*_files` keys once every surface is
migrated off them. This is a serial cleanup pass — do it once, carefully, with
nested-jail parity verification per surface.

## What unblocks the gated lanes

- **nix-ld (host-gated).** Independent of the jail-side thread — it's an
  image-layer change (`flake.nix` interpreter retarget + an `internal/entrypoint`
  `/run` symlink). Verified not started (`rg nix-ld flake.nix` → nothing;
  `LD_LIBRARY_PATH=/lib:/usr/lib` still live at `flake.nix:685`,
  `assemble.go:379`, and `mcp_wrappers.go:20,65,73`). **Ready any time a
  maintainer has a host with nix** — it needs `just load && just install`, so an
  in-jail agent can't finish it. **User-visible payoff worth flagging:** this is
  what finally lets *custom* `mcp_servers` start without the wrapper
  `LD_LIBRARY_PATH` hack — the open gap where an MCP server that bypasses the
  node wrapper silently fails to load `libstdc++` under a scrubbed env.
- **D4 Cachix (human-gated).** The `flake.nix` `nixConfig` substituter is
  already enabled (flake.nix:13-16, 730c258); the CI push job self-enables once
  the `CACHIX_AUTH_TOKEN` secret exists. What remains is the human step: create
  the Cachix account/token, land the first push, and prove one Mac download.
  Composes with D3 (done) to give checkout-less Mac installs the image by
  download. See [handoff-cachix-cache.md](handoff-cachix-cache.md).

## The whole picture

```
 DONE ─────────────────────────────────────────────────►│ now │──────────────►

 jail    J1.1–J1.4 ✓  D1 ✓  D2 ✓  D3 ✓  CI ✓  cli-color-audit (bug class) ✓
 (agent)  J2.1–J2.4 ✓  J3 ✓  module-consolidation ✓
          config Phase A ✓  Phase B ✓ ───────────────────► Phase C + boot-wiring
          cli-color-audit tail (migrate run/console.go + unify TTY probe) ──────►

 host    nix-ld  ── ready ANY host session (image layer; closes custom-mcp_servers gap) ─►
 (human) D4 Cachix ── substituter enabled ✓; needs account + first push + Mac download ──►

 mac     M0 ✓  M1 ✓  M2 ✓  ── PROVEN on real Apple Silicon 2026-07-21 ────────────────────
 (hw)          (OQ-1 path_helper + finding-6 password observed and passing)
```

## Parallelization — what can run concurrently right now

The lanes have thinned out; the concurrency picture is simpler than it was:

- **jail:** the config-composition **Phase C + boot-wiring** thread and the
  **cli-color-audit tail** are independent (different files) and can run
  concurrently. Phase C itself is a serial cleanup pass (wire, then delete,
  verifying each surface).
- **host (nix-ld), human (D4 Cachix account):** each on its own clock; neither
  blocks the jail lane.

**Best concurrent slice today:** config-composition Phase C + the cli-color-audit
tail (both jail-side, non-overlapping files), plus kicking off **nix-ld**
whenever a host session is free. There is no longer a hard cross-lane
dependency — M1's dependency on J2 is discharged (both landed).

## Parked

- **integration-parallelism** — bounded `t.Parallel()` for the container suite.
  Parked on purpose: CI is free (wall time is only a convenience) and the fast
  local loop (`just test-fast`, `-short`) skips every container test, so this only
  pays off for a full local `just test`. It also needs real work first — per-test
  `GlobalStorage` isolation to unstick the shared `last-load` sentinel race
  (`autoload.go:135`) — before `t.Parallel()` is safe; N is bound by memory (each
  jail is a VM/container), not the 32 cores. The 2026-07-20 launch-merges
  (zbar/cli/isolation/cgroup → single launches, landed in c4ae68a) already
  recovered ~120s/arch with zero parallelism risk. See
  [integration-parallelism.md](integration-parallelism.md).

## Runbooks

The Mac verification procedures moved here from `docs/guides/runbooks/` — they
are Track M **verification gates**, not user-facing reference (the maintainer's
"mostly plans in disguise" call). They now live under
[`docs/plans/runbooks/`](runbooks/):

- [runbooks/mac-macos-user-e2e.md](runbooks/mac-macos-user-e2e.md) — Track M
  gate. The you-drive/agent-advise macos-user acceptance-bar test
  (§5 `which jq` → `/nix/store/…`). **M1 PASSED on real HW (2026-07-21)** —
  kept as the repeatable procedure.
- [runbooks/mac-ac-container-builder.md](runbooks/mac-ac-container-builder.md) —
  a **PASSED** gate (real HW, 2026-07-17) kept as the repeatable zero-sudo
  procedure; Track-M / J3-adjacent, agent-runnable.
- [runbooks/mac-go-port-verification.md](runbooks/mac-go-port-verification.md) —
  **STALE, recommended for `git rm` (maintainer call).** Its method is "diff
  each Go command against `uv run python -m src.cli …` and bail back to Python" —
  dead post-wipe (the Python tree is gone; the doc's own footer admits it). It
  carries a prominent STALE banner and is kept only until the maintainer
  confirms deletion; the live gates are the two runbooks above. **Recommendation:
  delete it** — the diff-against-Python method cannot be revived.

New Track-M runbooks (e.g. the M0 `mac-sandvault-session.md` deliverable) land
here too.
