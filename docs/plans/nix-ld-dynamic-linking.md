# Plan: nix-ld — kill the `LD_LIBRARY_PATH` whack-a-mole

**Status:** OPEN — decided, not started. Pulled out of the archived
`go-port-post-transition.md` §1 (the Go-only cutover it was gated behind has
landed, so it's now actionable). It's a `flake.nix` + entrypoint image change,
which is **fully validatable in a nested jail** (`yolo -- bash` rebuilds the
flake and runs the new image — runtime behavior included; verified 2026-07-22,
see AGENTS.md "Build & deploy"). A host `just load` is only needed to ship it to
the maintainer's own day-to-day jails, not to prove it works. Watch the build
output: a failed nix build silently falls back to the stale image.

**Design + empirical validation:**
[../design/mise-node-dynamic-linking.md](../design/mise-node-dynamic-linking.md)
§Resolution is the full blueprint — this doc is just the sequenced work list.

## The problem (why this exists)

Non-nix binaries (the mise node, npm/pip packages, downloaded tools) find their
shared libs only via `LD_LIBRARY_PATH=/lib:/usr/lib`, which the jail sets in
every process (`flake.nix:685` baked Env, `internal/cli/run/assemble.go:379` the
`-e` re-export). Any consumer that **scrubs the environment** then can't load
`libstdc++` — so we paper over it with per-call-site wrapper hacks. The worst
offenders are the **MCP node wrappers**
(`internal/entrypoint/mcp_wrappers.go:20,65,73`), which re-export
`LD_LIBRARY_PATH` precisely so MCP servers can start under a scrubbed env. Custom
`mcp_servers` that don't use the wrapper silently fail to start — the open gap.

nix-ld replaces the `/lib64` FHS interpreter with a loader that resolves
`libstdc++` env-free, so the mise node (and everything else) links without the
`LD_LIBRARY_PATH` dance, and the wrapper hacks disappear.

## Current state (verified 2026-07-20)

- No nix-ld anywhere in the tree (`rg nix-ld flake.nix` → nothing). The sole
  mention is an aspirational forward-reference comment at
  `internal/entrypoint/mise.go:44` (added `743e053`) — cosmetic, nix-ld is still
  unimplemented, and the doc's own `rg nix-ld flake.nix → nothing` check holds.
- `LD_LIBRARY_PATH=/lib:/usr/lib[:/usr/lib/<multilib>]` is live in: the baked
  image Env (`flake.nix:685`), the CLI `-e` injection (`assemble.go:379`), and
  the three MCP wrapper scripts (`mcp_wrappers.go:20,65,73`).
- So the "custom-`mcp_servers` wrapper gap" (an MCP server that bypasses the
  node wrapper can't find libstdc++ under a scrubbed env) is still open.

## The work (from the blueprint)

- [ ] **flake.nix:** retarget the `/lib64` + `/lib` interpreter symlink → nix-ld.
  Variant A: a custom derivation with `DEFAULT_NIX_LD` baked to the real loader.
  Variant B: stock `pkgs.nix-ld` + `NIX_LD` env + `/run` wiring. Delivery rides
  the same CI / Cachix / on-demand-builder paths as the rest of the image.
- [ ] **flake.nix:** bake a minimal fallback lib dir at a **non-store** path
  (`/usr/share/nix-ld/lib/` — `ld.so` + the core farm-trio symlinks).
- [ ] **entrypoint (Go, `internal/entrypoint`):** idempotently create the
  `/run/current-system/sw/share/nix-ld/lib` symlink at startup, including on the
  reuse-`exec` paths. (One implementation now — the Python twin is gone.)
- [ ] **cli:** explicit mode on `--tmpfs /run` (Docker `-u` EACCES guard).
- [ ] **KEEP the baked `LD_LIBRARY_PATH`** (`flake.nix:685`) — it's the only
  dlopen-by-soname discovery path for *nix-built* processes (the user-packages
  contract). nix-ld replaces the *interpreter*, not soname discovery.
- [ ] **Staged DELETE (separate commits, each after nested-jail validation):**
  the `LD_LIBRARY_PATH` export lines in the MCP wrappers
  (`mcp_wrappers.go:20,65,73`); then evaluate dropping the CLI `-e` re-export
  (`assemble.go:379`). **The custom-`mcp_servers` wrapper gap closes for free**
  once the loader is env-independent.
- [ ] **Validation:** an `env -i` smoke suite in a nested jail — mise node, the
  `claude` binary, copilot addons, an MCP spawn, a ctypes `dlopen` — plus one
  `aarch64` run.

## Sequencing

Independent image change; do it as its own sequenced PR with a nested-jail gate
after each mutation (flake change → rebuild → verify node/MCP start env-free →
delete a wrapper export → re-verify). Per AGENTS.md, a nested `yolo -- bash`
rebuilds the flake and runs the new image, so every one of those gates runs
in-jail on the dev-override path — no host session needed to validate. The
final host `just load` only ships the proven change to the maintainer's own
jails.
