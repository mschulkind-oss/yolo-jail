# ROADMAP — sequencing the active plans

**Date:** 2026-07-22. **Purpose:** one ordering for everything under
`docs/plans/`, so "what do I work on next?" has a single answer. Every
dependency below was checked against the plan docs **and** the code they cite
(re-verified 2026-07-22, after the agent-config prism cutover + agy landed and
the cache-relocation host acceptance step was discharged) — where a claim rests
on the tree, the file:line is named.

## Open work at a glance

Everything not marked done below reduces to **three** open items. In priority /
lane order:

| # | Open item | Lane | Blocker |
|---|---|---|---|
| 1 | **config-composition — non-agent surface ports** (mise, standalone MCP/LSP, git identity onto the prism, then delete their bespoke generators) | jail-side | none — the main remaining agent-completable thread |
| 2 | **Remove the VM builder** (delete `internal/builder` + the `yolo builder {setup,start,stop,status}` commands; rewire `yolo check`'s Image Build section onto the container-builder reality) | jail-side (macOS-runtime-gated) | none — decided 2026-07-23 (revival plan Open Decision #3, RESOLVED). The container builder (J3, proven both runtimes) covers the surface automatically and zero-setup; the VM builder's QEMU/sudo/KEYS complexity is dead weight. **Supersedes** the earlier "fix the KEYS bug" item — that fix is moot if the code is deleted. See [linux-builder-lifecycle.md](../design/linux-builder-lifecycle.md) |
| 3 | **D4 Cachix** (one Mac download proof) | hardware-gated | substituter enabled + account/cache/CI-push all done (2026-07-22); needs only a real Mac to prove the download path |

Not on this list because they are **done or held**: J1–J3, D1/D2/D3, Track M
M0–M2, module-consolidation, the agent-config prism cutover, agy, **and nix-ld**
are all **done** (nix-ld shipped Variant A on 2026-07-22 — `e05666a`/`1d614e1`/
`d38463a`/`d6d2e65`/`c434f35`; only a host-gated `env -i` acceptance matrix
remains before `just load`); `cache-relocation` work items 1–10 are **done**
(host acceptance step discharged 2026-07-22) and item 11 (`yolo cache relocate`)
is **held** pending a level-of-abstraction design question, not scheduled.

This is a **meta-doc**: it sequences the plans, it does not restate them. Each
plan remains the source of truth for its own work items. The
[macos-revival-and-distribution-plan.md](macos-revival-and-distribution-plan.md)
is the roadmap of record for the macOS/distribution effort (Tracks J/D/M); this
ROADMAP reconciles with its internal "Sequencing at a glance" and folds the
post-Go-port backlog (nix-ld, color audit, consolidation) into the same picture.

## The plans

| Plan | One-liner | Lane / status |
|---|---|---|
| [macos-revival-and-distribution-plan.md](macos-revival-and-distribution-plan.md) | Tracks J (Linux-jail fixes), D (distribution/source-access), M (Mac hardware). Roadmap of record. | **J1–J3 + D1/D2/D3 done, Track M M0/M1/M2 PROVEN on HW 2026-07-21; only D4 Mac-download proof remains.** |
| [handoff-cachix-cache.md](handoff-cachix-cache.md) | The revival plan's **D4**: publish the OCI image to a Cachix cache. | human-gated — substituter ENABLED (flake.nix:13-16, 730c258); Cachix account + cache exist and CI has pushed data; only the Mac download proof remains |
| [nix-ld-dynamic-linking.md](nix-ld-dynamic-linking.md) | Replace the `LD_LIBRARY_PATH` whack-a-mole with nix-ld; closes the custom-`mcp_servers` startup gap. | jail-side — **DONE 2026-07-22** (Variant A: custom `nix-ld.overrideAttrs`, `DEFAULT_NIX_LD` baked; MCP-wrapper exports removed; `yolo check` tripwire added). Only a host-gated `env -i` acceptance matrix remains before `just load`. |
| [agent-settings-composition.md](agent-settings-composition.md) | Layered regeneration of any generated config (agent settings, MCP, LSP, mise, identity) + a Lua transform. **Design FINALIZED 2026-07-20.** | jail-side — **agent-config surfaces DONE 2026-07-22: prism is the sole boot config path (gate retired, bespoke writers deleted); non-agent surfaces (mise/MCP/LSP/identity) still to port** |
| [cache-relocation.md](cache-relocation.md) | User-scope-only `cache_relocations` so a huge cold cache subdir can live on other storage; unblinds `prune`/`purge`. Podman behavior proven 2026-07-21; host acceptance discharged 2026-07-22. | **DONE (items 1–10); item 11 held on a design question** |
| [../design/linux-builder-lifecycle.md](../design/linux-builder-lifecycle.md) | Decision record: the two builder mechanisms (VM vs container), why the **VM builder is being removed** and the container builder is the sole builder, plus the KEYS-bug diagnosis kept as evidence + a manual unblock. | jail-side (macOS-runtime-gated) — **OPEN**: delete `internal/builder` + `yolo builder` commands; rewire `yolo check` Image Build onto the container builder |
| [cli-color-audit.md](cli-color-audit.md) | Shared rich→ANSI renderer + TTY gate across commands. | jail-side — **DONE 2026-07-22** (renderer consolidated, TTY probe unified, check/doctor leak fixed, all commands classified) |
| [antigravity-agy-support.md](antigravity-agy-support.md) | Support Google Antigravity CLI (`agy`) as a native agent inside `yolo-jail`. | jail-side — **DONE 2026-07-22** (born on the prism; all eight touchpoints landed) |
| [module-consolidation-and-cleanup.md](module-consolidation-and-cleanup.md) | Collapse the parity-era `internal/*` split; drop parity machinery; §4 OSS-hygiene remnants. | **DONE 2026-07-21** (package-merge declined) |

| [integration-parallelism.md](integration-parallelism.md) | Bounded `t.Parallel()` for the container suite (needs per-test GlobalStorage first). | parked (test speed) |
| [runbooks/](runbooks/) | Track M verification procedures (see [Runbooks](#runbooks) below). | hardware-gated |

## Lanes — not everything is one linear sequence

Three lanes run in parallel; only the jail-side lane is a sequence. The other
two are gated on a resource an in-jail agent doesn't have.

- **Jail-side (agent-completable).** Developable and testable from inside a
  jail; `internal/` changes still get a nested-jail sanity run per AGENTS.md.
  With the Jul-21/22 wave landed, the jail-side work left is a single thread —
  the **non-agent config-composition surfaces** (porting mise/MCP/LSP/identity
  onto the prism — the agent-config surfaces and `agy` are done). J2, J3, D2,
  cli-color-audit (now fully DONE — renderer consolidated, TTY probe unified,
  check/doctor leak fixed, all commands classified), module-consolidation, the
  agent-config prism cutover, agy, and **nix-ld** (shipped Variant A 2026-07-22)
  have all landed.
- **Host-gated (needs a human at a host with nix) — for SHIPPING, not
  validating.** A nested `yolo -- bash` rebuilds the flake and runs the new
  image, so a `flake.nix` / image change is fully validated in-jail, runtime
  behavior included (verified 2026-07-22; AGENTS.md "Build & deploy"). What still
  needs a host session is loading the proven image into the maintainer's OWN
  day-to-day jails (`just load`) — that's shipping, and it never blocks jail-side
  development or verification. **nix-ld** is the live example: it is fully
  implemented and in-jail-validated, and its only remaining step is the
  host-gated `env -i` acceptance matrix before `just load`. The one genuinely
  hardware-gated remnant is **D4's Mac download proof** (see the next bullet),
  which needs real Mac hardware, not just a host with nix.
- **Hardware-gated (needs a real Mac).** No in-jail agent can complete these.
  Members: **D4 Cachix** — as of 2026-07-22 the account + cache + CI push are
  done, so only the one Mac download proof remains. Track M's M0/M1/M2 are
  already PROVEN on real Apple Silicon (2026-07-21), so the hardware gate is
  discharged for the current scope.

## Current state — what's already done

Marked here so the "start here" arrow points at the real next item.

- ✅ **J1.1–J1.4** (2026-07-20) — runtime unification, darwinpkg stderr drain,
  builder VM reaping, `yolo --help`. Each RED-then-GREEN; J1.1 nested-jail
  verified.
- ✅ **D1** (2026-07-20) — `just deploy` records `repo_path`; `check` honors it.
  Verified: `internal/repopath/` exists, wired into the install recipe.
- ✅ **D2 — graceful launch degradation** (2026-07-21) — repo-root resolution is
  no longer a hard gate. When it fails the launch proceeds degraded on a
  cached/loaded image (`image.AutoLoadOptions.SkipBuild`), and `Run` prints a
  soft notice instead of exiting 1 (commit 8f1d612). Nested-jail verified both
  paths: normal launch + rebuilds; degraded runs the cached image.
  *(The intermediate `repoBound`-gated `/opt/yolo-jail:ro` bind + `YOLO_REPO_ROOT`
  env described in the original commit were later removed entirely by the
  prebuilt-bundle cutover — 2026-07-23: `/opt/yolo-jail` is now a baked install
  prefix and no `YOLO_REPO_ROOT` is injected into the jail.)*
- ✅ **D3** (2026-07-20) — Go-era source bundle ships so checkout-less installs
  build the image. Verified the staged tree evaluates. *(Superseded 2026-07-23:
  the source-tree/`git archive` bundle + `stageInstalledWheel` staging were
  replaced by the prebuilt "two files and a binary" bundle — `flake.nix` +
  `flake.lock` + prebuilt `bin/linux-<arch>/`, consumed by the flake's prebuilt
  short-circuit with no staging. See `docs/research/repo-root-and-distribution.md`.)*
- ✅ **CI green** (2026-07-20) — the `TestShimPersistence` failure (shim
  mount-anchor / `ClearContents`) is fixed and the four test-merges landed; the
  full CI run (both arches, integration incl.) passed.
- ✅ **cli-color-audit — DONE** (2026-07-20/22) — the shared `internal/richtext`
  renderer landed and `prune`/`builder`/`macosuser`/`broker` (plus the top-level
  `cli`/`config`/`ps` commands) route through it with a TTY gate. The tail closed
  2026-07-22: `internal/cli/run/console.go` migrated onto `internal/richtext`
  (`67454a8`), the two TTY-probe conventions unified onto `internal/tty`
  (`b76b2ba`), a `check`/`doctor` ANSI-leak-to-a-pipe fixed (`c9ea5e8`), and the
  last commands (`loopholes`/`init`/`init-user-config`) classified.
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
  codex) plus MCP/LSP/mise; `mergeAccumulate` tombstone fix.
- ✅ **config-composition — agent-config surfaces wired + cut over** (2026-07-22) —
  boot (`internal/entrypoint`) and `yolo check` now render the agent-config
  surfaces through `agentcfg` via the `Configure*Prism` writers; the
  `YOLO_PRISM_SURFACES` cutover gate is retired (prism is unconditional), and the
  six bespoke `Configure*` writers plus their dead helpers are deleted. `agy` was
  born directly on the prism. Obsolete snapshot/managed-MCP sidecars self-clean on
  each surface's first-migration boot. **Remaining:** the non-agent surfaces
  (mise/MCP/LSP/identity) still use bespoke generators; `host_*_files` keys stay
  (the prism host layer reads through them).
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
- ✅ **nix-ld — DONE** (2026-07-22) — Variant A: a custom `nix-ld.overrideAttrs`
  with `DEFAULT_NIX_LD` baked to the real glibc loader is the `/lib64` + `/lib`
  FHS interpreter; the fallback lib dir is the baked non-store
  `/usr/share/nix-ld/lib`; the three MCP-wrapper `LD_LIBRARY_PATH` exports are
  removed (`1d614e1`) and the custom-`mcp_servers` gap closed for free; the baked
  Env + cli `-e` `LD_LIBRARY_PATH` are deliberately kept (`d38463a`) as the
  nix-process dlopen-by-soname path; a `yolo check` FHS-loader tripwire
  (`d6d2e65`) guards regressions. Only the broader host-gated `env -i` acceptance
  matrix remains before `just load`.

Everything else below is **open**: config-composition non-agent surfaces
(mise/MCP/LSP/identity ports) and the D4-download human step. (cli-color-audit
and nix-ld are now fully DONE — see above.)

## Recommended order (jail-side thread)

With J1/D1/D2/D3/J2/J3/consolidation, the agent-config prism cutover, and agy all
done, the jail-side lane has collapsed to two independent items — there is no
longer a critical-path chain:

1. **config-composition — non-agent surface ports** — *the main remaining jail
   work; its own self-contained thread (see below).* The engine drives boot for
   the agent-config surfaces already; what remains is folding the non-agent
   surfaces (mise, standalone MCP/LSP, git identity) onto the prism and then
   deleting their bespoke generators. This is where the real design
   nuance lives — see the [config-composition build](#config-composition-build-own-self-contained-thread)
   section and `agent-settings-composition.md`.

2. ~~**cache-relocation**~~ — **DONE 2026-07-21; host acceptance discharged
   2026-07-22.** Work items 1–10 landed (user-scope-only `cache_relocations`,
   nested rw bind mount, prune/purge accounting, docs) and were verified end to
   end in a nested jail: a write to `~/.cache/<subdir>` inside the jail lands on
   the relocated target and the host-side stub stays empty. The host-gated
   acceptance step is now done too — the maintainer moved a HuggingFace cache to
   cold storage on another machine successfully (2026-07-22). `yolo cache
   relocate` (item 11) is **held**, not merely deferred: the maintainer is not
   sure `cache_relocations` sits at the right level of abstraction and does not
   want a command locked around it until that resolves (see the plan's "Is
   `cache_relocations` the right level?" open question). Note for whoever revisits
   **module-consolidation**: this touched `internal/prune/prunecmd.go` and
   `report.go`, so rebase before starting there.

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

### Coupling: cli-color-audit ↔ module-consolidation (resolved)

Verified overlap — both plans called for the *same* shared color-aware rich→ANSI
renderer to replace the four+ near-duplicate `richTagRe` printers. cli-color-audit
ran first and landed the shared helper (`internal/richtext`), so if
module-consolidation is ever revisited it simply inherits it — don't build it
twice.

### cli-color-audit tail — DONE (2026-07-22)

The tail closed: `internal/cli/run/console.go` migrated off its private
`richTagRe`/`richToANSI`/`stripRich` onto `internal/richtext` (`67454a8`), the
two TTY-probe conventions unified onto `internal/tty` (`b76b2ba`), a genuine
`check`/`doctor` ANSI-leak-to-a-pipe fixed (`c9ea5e8`), and the remaining
commands (`loopholes`/`init`/`init-user-config`) classified. Nothing left.

## Config-composition build (own self-contained thread)

[agent-settings-composition.md](agent-settings-composition.md) is **finalized**
and jail-side, independent of the macOS J/D/M tracks. Its shape is **serial
foundation, then parallel fan-out, then deletion** — and Phases A and B have
**landed**:

**Phase A — the engine (DONE).** Built as a leaf library: pure `decode`/
`deepMerge`/`enforce`/`render` per codec (json/toml), the locked-down gopher-lua
VM + `ctx` bridge, the manifest schema + loader, and the fixture corpus that is
the spec. `yolo config render` is the thin read-only CLI over it.

**Phase B — surface migrations (DONE for agent configs).** Every agent surface
(pi/claude/gemini/copilot/opencode/codex, plus `agy` born on the prism) renders
through `agentcfg` at boot via its `Configure*Prism` writer, each verified at
parity in a nested jail. MCP/LSP/mise/identity are **not** yet manifest-modeled
and are **not** reachable via `yolo config render` (`config render mise` →
"no surfaces"); they still run through bespoke generators — their ports remain.

**Phase C — deletion + boot-wiring (DONE for agent configs, 2026-07-22).** Boot
and `yolo check` render the agent-config surfaces through `Compose`; the
`YOLO_PRISM_SURFACES` cutover gate is retired (prism unconditional), and the six
bespoke `Configure*` writers plus their dead helpers (the three-way merge, codex
TOML dumper, numeric-equality cluster) are deleted. `host_*_files` keys stay (the
prism host layer reads through them). **Remaining:** wire + cut over the non-agent
surfaces (mise/MCP/LSP/identity), then delete their bespoke generators — a serial
cleanup pass with nested-jail parity verification per surface.

## What unblocks the gated lanes

- **nix-ld — DONE (2026-07-22); only the host `env -i` acceptance matrix +
  `just load` remain.** Shipped as an image-layer change: the `flake.nix`
  interpreter retarget (`nixLd = imagePkgs.nix-ld.overrideAttrs`, `DEFAULT_NIX_LD`
  baked; `/lib` + `/lib64` `$LINKER_BASENAME` → `${nixLd}/libexec/nix-ld`) plus
  the baked non-store fallback dir `/usr/share/nix-ld/lib`. The entrypoint `/run`
  symlink turned out **unnecessary** under this variant (the built binary has
  zero `/run/current-system` references). The three MCP-wrapper
  `LD_LIBRARY_PATH` exports are gone (`mcp_wrappers.go`, `1d614e1`); the baked
  Env (`flake.nix:732`) + cli `-e` re-export (`assemble.go:405`) are deliberately
  kept as the nix-process dlopen-by-soname path (`d38463a`). Built + validated
  in a nested `yolo -- bash` (AGENTS.md "Build & deploy") and guarded by a
  `yolo check` FHS-loader tripwire (`d6d2e65`). The only host steps left are the
  broader `env -i` acceptance matrix (Claude native binary, copilot, MCP spawn,
  ctypes `dlopen`, aarch64) and `just load` to ship it to the maintainer's own
  jails. **User-visible payoff realized:** *custom* `mcp_servers` now start
  without the wrapper `LD_LIBRARY_PATH` hack — the gap where an MCP server that
  bypassed the node wrapper silently failed to load `libstdc++` under a scrubbed
  env is **closed**.
- **D4 Cachix (hardware-gated now).** The `flake.nix` `nixConfig` substituter is
  enabled (flake.nix:13-16, 730c258) and the CI push job self-enables with the
  `CACHIX_AUTH_TOKEN` secret. **As of 2026-07-22 the Cachix account and cache
  exist and already hold image data pushed from CI runs** — so account/token and
  first push are DONE. The ONLY remaining gate is hardware: prove one real Mac
  *downloads* the prebuilt image (substituter hit) instead of building from
  source. Composes with D3 (done) to give checkout-less Mac installs the image by
  download. See [handoff-cachix-cache.md](handoff-cachix-cache.md).

## The whole picture

```
 DONE ─────────────────────────────────────────────────►│ now │──────────────►

 jail    J1.1–J1.4 ✓  D1 ✓  D2 ✓  D3 ✓  CI ✓  cli-color-audit (FULLY DONE) ✓
 (agent)  J2.1–J2.4 ✓  J3 ✓  module-consolidation ✓  agy ✓  nix-ld ✓ (Variant A)
          config Phase A ✓  B ✓  agent-config cutover ✓ ─► non-agent surface ports

 (hw)    D4 Cachix ── substituter enabled ✓  account + cache + CI push ✓; needs only a Mac download proof ──►

 mac     M0 ✓  M1 ✓  M2 ✓  ── PROVEN on real Apple Silicon 2026-07-21 ────────────────────
 (hw)          (OQ-1 path_helper + finding-6 password observed and passing)
```

## Parallelization — what can run concurrently right now

The lanes have thinned out to essentially one open jail-side thread:

- **jail:** the config-composition **non-agent surface ports** thread is the only
  open agent-completable work — a wire-then-delete cleanup pass per surface,
  verifying each in a nested jail. (nix-ld, previously the parallel jail-side
  item, landed 2026-07-22.)
- **hardware (D4 Cachix Mac-download proof):** on its own clock; does not block
  the jail lane.

**Today's slice:** config-composition non-agent ports — jail-side, nested-jail
validatable per surface. There is no longer a hard cross-lane dependency — M1's
dependency on J2 is discharged (both landed), and nix-ld is done.

## Parked

- **integration-parallelism** — bounded `t.Parallel()` for the container suite.
  Parked on purpose: CI is free (wall time is only a convenience) and the fast
  local loop (`just test-fast`, `-short`) skips every container test, so this only
  pays off for a full local `just test`. It also needs real work first — per-test
  `GlobalStorage` isolation to unstick the shared `last-load` sentinel race
  (`autoload.go:143`) — before `t.Parallel()` is safe; N is bound by memory (each
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
