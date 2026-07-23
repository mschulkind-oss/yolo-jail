# Plan: nix-ld — kill the `LD_LIBRARY_PATH` whack-a-mole

**Status:** IMPLEMENTED (2026-07-22) — shipped as **Variant A** (a custom
`nix-ld.overrideAttrs` with `DEFAULT_NIX_LD` baked to the real glibc loader).
Commits: `e05666a` (flake: adopt nix-ld as the FHS interpreter), `1d614e1`
(drop the MCP-wrapper `LD_LIBRARY_PATH` exports), `d38463a` (keep + document the
cli `-e` re-export), `d6d2e65` (`yolo check` FHS-loader tripwire), `c434f35`
(record IMPLEMENTED in the design doc). The full as-shipped record — including
why items 3–4 below turned out unnecessary — lives in the design doc's
"Resolution" section (its items 1–8 are the authority; this list is reconciled
to them). **One residual, host-gated:** the broader `env -i` acceptance matrix
(Claude native binary, `copilot --version`, an MCP spawn, a ctypes `dlopen`,
aarch64) before the maintainer ships via `just load` — the mise-node crux is
already proven in-jail and guarded by the `yolo check` tripwire.

This was a `flake.nix` + entrypoint image change, **fully validatable in a
nested jail** (`yolo -- bash` rebuilds the flake and runs the new image —
runtime behavior included; verified 2026-07-22, see AGENTS.md "Build & deploy").
A host `just load` is only needed to ship it to the maintainer's own day-to-day
jails, not to prove it works. Watch the build output: a failed nix build
silently falls back to the stale image.

**Design + empirical validation:**
[../design/mise-node-dynamic-linking.md](../design/mise-node-dynamic-linking.md)
§Resolution is the full blueprint — this doc is just the sequenced work list.

## The problem (why this existed) — now solved

Non-nix binaries (the mise node, npm/pip packages, downloaded tools) found their
shared libs only via `LD_LIBRARY_PATH=/lib:/usr/lib`, which the jail sets in
every process (the baked image Env + the cli `-e` re-export). Any consumer that
**scrubbed the environment** then couldn't load `libstdc++` — so we papered over
it with per-call-site wrapper hacks. The worst offenders were the **MCP node
wrappers** (`internal/entrypoint/mcp_wrappers.go`), which re-exported
`LD_LIBRARY_PATH` precisely so MCP servers could start under a scrubbed env.
Custom `mcp_servers` that didn't use the wrapper silently failed to start — the
gap that is now closed.

nix-ld replaces the `/lib64` FHS interpreter with a loader that resolves
`libstdc++` env-free, so the mise node (and everything else) links without the
`LD_LIBRARY_PATH` dance, and the wrapper hacks are gone.

## Current state (as shipped 2026-07-22)

- nix-ld **is** the FHS ELF interpreter: `flake.nix` defines the `nixLd`
  derivation (`nix-ld.overrideAttrs`, `DEFAULT_NIX_LD` → real glibc `ld.so`,
  the lib-dir const `substituteInPlace`d to the baked non-store
  `/usr/share/nix-ld/lib`) and symlinks `/lib/$LINKER_BASENAME` +
  `/lib64/$LINKER_BASENAME` → `${nixLd}/libexec/nix-ld` (see the `nixLd` block
  and `mkBinPathLinks`). `internal/entrypoint/mise.go:44`'s forward-reference
  comment is now accurate rather than aspirational.
- `LD_LIBRARY_PATH=/lib:/usr/lib[:/usr/lib/<multilib>]` is **deliberately kept**
  in the baked image Env (`flake.nix:732`) and the CLI `-e` re-export
  (`assemble.go:405-409`) — it is the dlopen-by-soname discovery path for
  *nix-built* processes, a class nix-ld structurally cannot serve (nix binaries
  never traverse `/lib64`). The three MCP wrapper `LD_LIBRARY_PATH` exports are
  **removed** (`mcp_wrappers.go` now carries "No LD_LIBRARY_PATH export" notes).
- The "custom-`mcp_servers` wrapper gap" is **closed**: bare `node` commands now
  survive scrubbed-env (`env -i`) spawns with no wrapper, because the interpreter
  itself is env-independent. (Known farm-gap residuals — e.g. `keytar.node`
  wanting `libsecret-1.so.0`, not in the farm — are pre-existing and failed
  *with* the old wrapper too; see the design doc's "Known residuals".)

## The work (from the blueprint)

- [x] **flake.nix:** retarget the `/lib64` + `/lib` interpreter symlink → nix-ld.
  Shipped as **Variant A** — a custom `nix-ld.overrideAttrs` with
  `DEFAULT_NIX_LD` baked to the real loader (`e05666a`). Delivery rides the same
  CI / Cachix / on-demand-builder paths as the rest of the image.
- [x] **flake.nix:** bake a minimal fallback lib dir at a **non-store** path
  (`/usr/share/nix-ld/lib/` — `ld.so` + the core farm-trio symlinks). The
  lib-dir const is `substituteInPlace`d to this baked path.
- [x] ~~**entrypoint:** idempotently create the
  `/run/current-system/sw/share/nix-ld/lib` symlink at startup.~~ **NOT NEEDED
  under Variant A** — the flake bakes `DEFAULT_NIX_LD` + rewrites the lib-dir
  const to `/usr/share/nix-ld/lib`, so the built `nix-ld` binary has **zero**
  `/run/current-system` references and there is no runtime symlink to create.
  (This step existed only for Variant B's `/run` fallback.)
- [x] ~~**cli:** explicit mode on `--tmpfs /run`.~~ **MOOT under Variant A** (no
  `/run` dependency). `--tmpfs /run` is already unconditional in both mount
  modes (`internal/cli/run/runmount.go`) for unrelated reasons — nothing changed.
- [x] **KEEP the baked `LD_LIBRARY_PATH`** (`flake.nix:732`) — it's the only
  dlopen-by-soname discovery path for *nix-built* processes (the user-packages
  contract). nix-ld replaces the *interpreter*, not soname discovery. Kept.
- [x] **Staged DELETE** — the `LD_LIBRARY_PATH` export lines are removed from all
  three MCP wrappers (`mcp_wrappers.go`, `1d614e1`, nested-jail verified). The
  CLI `-e` re-export (`assemble.go`) was **evaluated and kept** (`d38463a`) —
  byte-identical to the baked `config.Env`, retained to keep the launch env
  self-describing and as the dlopen-by-soname path for nix processes. **The
  custom-`mcp_servers` wrapper gap closed for free.**
- [x] **Validation:** the mise-node `env -i` crux is proven in-jail and guarded
  by a `yolo check` FHS-loader tripwire (`d6d2e65`). ~~aarch64 + the broader
  smoke matrix (Claude native binary, copilot, an MCP spawn, a ctypes `dlopen`)~~
  remains a **host-gated acceptance step** before shipping via `just load`.

## Sequencing

Independent image change; do it as its own sequenced PR with a nested-jail gate
after each mutation (flake change → rebuild → verify node/MCP start env-free →
delete a wrapper export → re-verify). Per AGENTS.md, a nested `yolo -- bash`
rebuilds the flake and runs the new image, so every one of those gates runs
in-jail on the dev-override path — no host session needed to validate. The
final host `just load` only ships the proven change to the maintainer's own
jails.
