# ROADMAP — sequencing the active plans

**Date:** 2026-07-20. **Purpose:** one ordering for everything under
`docs/plans/`, so "what do I work on next?" has a single answer. Every
dependency below was checked against the plan docs **and** the code they cite
(verified 2026-07-20) — where a claim rests on the tree, the file:line is named.

This is a **meta-doc**: it sequences the plans, it does not restate them. Each
plan remains the source of truth for its own work items. The
[macos-revival-and-distribution-plan.md](macos-revival-and-distribution-plan.md)
is the roadmap of record for the macOS/distribution effort (Tracks J/D/M); this
ROADMAP reconciles with its internal "Sequencing at a glance" and folds the
post-Go-port backlog (nix-ld, color audit, consolidation) into the same picture.

## The plans

| Plan | One-liner | Lane |
|---|---|---|
| [macos-revival-and-distribution-plan.md](macos-revival-and-distribution-plan.md) | Tracks J (Linux-jail fixes), D (distribution/source-access), M (Mac hardware). Roadmap of record. | mixed — see per-track below |
| [handoff-cachix-cache.md](handoff-cachix-cache.md) | The revival plan's **D4**: publish the OCI image to a Cachix cache. | human-gated |
| [nix-ld-dynamic-linking.md](nix-ld-dynamic-linking.md) | Replace the `LD_LIBRARY_PATH` whack-a-mole with nix-ld; closes the custom-`mcp_servers` startup gap. | host-gated |
| [cli-color-audit.md](cli-color-audit.md) | Make `prune`/`builder`/`macos-*` render rich markup instead of stripping it; consolidate the duplicated printers. | jail-side |
| [module-consolidation-and-cleanup.md](module-consolidation-and-cleanup.md) | Collapse the ~34 Python-mirroring `internal/*` packages; drop parity machinery; §4 OSS-hygiene remnants. | jail-side, last |
| [agent-settings-composition.md](agent-settings-composition.md) | Unbuilt "Prism" RFC for composing agent settings across host/jail. | parked (needs re-grounding) |
| [runbooks/](runbooks/) | Track M verification procedures (see [Runbooks](#runbooks) below). | hardware-gated |

## Lanes — not everything is one linear sequence

Three lanes run in parallel; only the jail-side lane is a strict sequence. The
other two are gated on a resource an in-jail agent doesn't have.

- **Jail-side (agent-completable).** Developable and testable from inside a
  jail; `internal/` changes still get a nested-jail sanity run per AGENTS.md.
  Members: the in-flight **CI fix**, **cli-color-audit**, most of **J2**
  (native-Go macos-user bootstrap re-port — Mac verification deferred to M1),
  **D2**, **J3**, and **module-consolidation**.
- **Host-gated (needs a human at a host with nix).** A `flake.nix` / image
  change that AGENTS.md says needs `just load && just install` on a real host
  and **cannot be validated in-jail**. Members: **nix-ld**, and any future image
  rebuild. Not a blocker on jail-side work — schedule it whenever a maintainer
  next has a host session.
- **Hardware/human-gated (needs a real Mac or a maintainer action).** No
  in-jail agent can complete these. Members: **Track M** (M0→M1→M2, needs Apple
  Silicon), **D4 Cachix** (needs an account created). These are lanes, not
  sequential blockers on the jail-side thread — the jail work does not wait on
  them (though M1 consumes J2's output; see below).

## Current state — what's already done

Marked here so the "start here" arrow points at the real next item.

- ✅ **J1.1–J1.4** (2026-07-20) — runtime unification, darwinpkg stderr drain,
  builder VM reaping, `yolo --help`. Each RED-then-GREEN; J1.1 nested-jail
  verified.
- ✅ **D1** (2026-07-20) — `just deploy` records `repo_path`; `check` honors it.
  Verified: `internal/repopath/` exists, wired into the install recipe.
- ✅ **D3** (2026-07-20) — Go-era source bundle ships so checkout-less installs
  build the image. Verified the staged tree evaluates.
- ⏳ **CI fix — in flight** (separate agent, `integration/`, `internal/config`,
  `internal/entrypoint`). The tree-green precondition for the J2 thread, which
  reopens `internal/entrypoint`. Do not start anything that touches those files
  until it lands.

Everything else below is **open**.

## Recommended order (jail-side thread)

The jail-side lane is the spine. The revival plan's own sequencing —
`J1.1–J1.4  D1 → J2.1..4 + D2 → D3 → J3` — still holds; with J1/D1/D3 done, the
live remainder is **J2 (+D2) → J3 → consolidation**, plus the two backlog items
slotted where their coupling puts them.

1. **cli-color-audit** — *do now, in parallel with the CI fix.* It is the one
   standalone jail item that does **not** collide with the in-flight CI files:
   its targets are `internal/prune/prunecmd.go:438`, `internal/builder/
   buildercmd.go:90`, and `internal/macosuser/orchestrator.go:83` — all three
   confirmed still `richTagRe.ReplaceAllString(s, "")` (strip-always), against
   the already-correct reference `internal/cli/run/console.go:59` (`richToANSI`
   + `stripRich`, gated `Color && IsTTYStdout()`). Small, cosmetic, no
   byte-parity risk. If you land the shared renderer here, module-consolidation
   inherits it; if not, consolidation lands it (see coupling note).

2. **J2 — native-Go macos-user bootstrap re-port (J2.1 → J2.4) + D2.** *The
   critical-path Mac-backend item; start once the CI fix clears
   `internal/entrypoint`.* The dead piece is real: `internal/cli/commands.go:375`
   still sets `RepoSrc = repoRoot/src` and `internal/macosuser/runplan.go:152,175`
   still stage/require a `python3` interpreter — and the tracked `src/` tree no
   longer exists (`git ls-files src/` → empty; the untracked `src/` +
   `yolo_jail.egg-info/` in the tree are stale Python build artifacts, not the
   shipped source). J2.1 threads container literals through `*entrypoint.Env`
   and J2.2 adds a darwin-native generation entry — **both touch
   `internal/entrypoint`, so J2 is gated on the CI fix.** D2 (graceful repo-root
   degradation) pairs naturally with J2 step 3 — both touch the run front door
   and the `RepoSrc` contract; land them together. J2's Mac-side behavior
   (password apply, path_helper OQ-1, fresh-inode re-exec) is verified in **M1**,
   not the jail.

3. **J3 — container-builder rewiring.** After J2 (macos-user needs no builder at
   all). Resurrect `internal/containerbuilder` from git history (verified GONE —
   deleted with zero importers) and wire it into `internal/image/autoload.go`.
   Jail-developable; its verification runbook
   ([runbooks/mac-ac-container-builder.md](runbooks/mac-ac-container-builder.md))
   is zero-sudo and agent-runnable, so Track M can confirm it from inside a
   sandbox — and that cell already **PASSED** on real HW (2026-07-17).

4. **module-consolidation-and-cleanup** — *last, by its own admission.* Collapse
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

## What unblocks the gated lanes

- **nix-ld (host-gated).** Independent of the jail-side thread — it's an
  image-layer change (`flake.nix` interpreter retarget + an `internal/entrypoint`
  `/run` symlink). Verified not started (`rg nix-ld flake.nix` → nothing;
  `LD_LIBRARY_PATH=/lib:/usr/lib` still live at `flake.nix:688`,
  `assemble.go:379`, and `mcp_wrappers.go:20,65,73`). **Ready any time a
  maintainer has a host with nix** — it needs `just load && just install`, so an
  in-jail agent can't finish it. **User-visible payoff worth flagging:** this is
  what finally lets *custom* `mcp_servers` start without the wrapper
  `LD_LIBRARY_PATH` hack — the open gap where an MCP server that bypasses the
  node wrapper silently fails to load `libstdc++` under a scrubbed env. Note the
  overlap with J2/J3's `internal/entrypoint` touches — sequence it so it doesn't
  race them for the same files.
- **D4 Cachix (human-gated).** Account creation + uncommenting the `flake.nix`
  `nixConfig` block; the CI push job already self-enables once the secret/var
  exist. Composes with D3 (already done) to give checkout-less Mac installs the
  image by download. Not a sequential blocker — see
  [handoff-cachix-cache.md](handoff-cachix-cache.md).
- **Track M (hardware-gated).** M0 (SandVault bootstrap) can start on a Mac
  **now** — it doesn't wait on the jail thread. **M1 consumes J2's output** (it
  verifies the re-ported bootstrap on real hardware via
  [runbooks/mac-macos-user-e2e.md](runbooks/mac-macos-user-e2e.md)), so M1 gates
  on J2 landing. **M2 (dogfood flip + docs) gates on M1 green.** M1 is also the
  only place OQ-1 (path_helper PATH) and finding-6 (password apply) get
  observed — they are unverified until then.

## The whole picture

```
 DONE ──────────────────────────────►│ now │──────────────────────────────────────►

 jail    J1.1–J1.4 ✓   D1 ✓   D3 ✓
 (agent)                                 [CI fix ⏳ in flight: config/entrypoint/integration]
                                              │ (unblocks the entrypoint-touching J2 thread)
         cli-color-audit ───────────────────►│ (1) standalone, non-colliding — do in parallel
                                              ▼
                                   J2.1 J2.2 J2.3 J2.4  +  D2  ──► J3 ──► module-consolidation
                                   (2, gated on CI fix)   (w/J2.3)  (3)     (4, settles the tree;
                                              │                             folds in color renderer)
 host    nix-ld  ── ready ANY host session (independent image layer; closes custom-mcp_servers gap) ─►
 (human) D4 Cachix ── needs account (composes with D3 ✓; not a blocker) ─────────────────────────────►

 mac     M0 (SandVault bootstrap, startable now) ── M1 (e2e verify, gated on J2) ──► M2 (dogfood + docs)
 (hw)                                                    ▲ consumes J2 output; sole home of OQ-1 verify
```

Everything on the jail row left of J3 is quota-light, self-contained commits in
this jail. The host and mac rows are gated lanes that run on their own clock.

## Parked

- **agent-settings-composition (Prism RFC)** — proposed, unbuilt. Verified its
  "what exists today" (§2) is grounded in the deleted Python
  (`config.py`/`agent_configs.py` line refs) and needs a **re-grounding pass in
  `internal/config` + `internal/entrypoint`** before any execution. Not blocked
  by the others, but should not be started until a maintainer commits to
  building Prism and someone re-bases §2 on the Go tree. Left out of the numbered
  order deliberately.

## Runbooks

The Mac verification procedures moved here from `docs/guides/runbooks/` — they
are Track M **verification gates**, not user-facing reference (the maintainer's
"mostly plans in disguise" call). They now live under
[`docs/plans/runbooks/`](runbooks/):

- [runbooks/mac-macos-user-e2e.md](runbooks/mac-macos-user-e2e.md) — **ACTIVE**
  Track M gate. The you-drive/agent-advise macos-user acceptance-bar test
  (§5 `which jq` → `/nix/store/…`); the M1 anchor until J2's dry-run golden
  exists. macos-user is still unverified on hardware.
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
