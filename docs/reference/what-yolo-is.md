---
status: current
verified: 2026-09-09
verified_commit: 356bcec8
covers:
  - internal/agentcfg/
  - internal/macosuser/seatbelt.go
  - internal/agentcfg/luahook/
  - flake.nix
tags: [charter, boundaries, architecture, separability, packs]
---

# What yolo is — the boundaries

**Status:** CURRENT as of 2026-09-09, verified against `356bcec8`.

yolo describes the environment an agent works in — its tools, its config, its skills, its
credentials — reproducibly, declaratively, per workspace. **Confinement is one attribute of that
description**, and a jail is the strongest setting of that attribute and the default.

That ordering is the charter, and it is the opposite of the intuitive one. At the wall yolo is not
novel: the confinement is a small, bounded set of container flags on the container backends, and on
macOS a Seatbelt profile that says *"SandVault-parity"* about itself
(`macosuser.SeatbeltProfile`). The credential boundary is **code that isn't there** — `~/.ssh` is
absent because nothing mounts it — and a boundary defined by omission is necessarily small and
necessarily commoditizable. Everything *inside* is the product: a locked package set, composed agent
config, skills and house rules, credential brokers, host-capability bridges.

This page is the reference for **which parts of yolo are separable from the wall, and which are
not** — the question you land on when deciding how much of yolo becomes packs, whether a subsystem
could ship on its own, or where a new mechanism belongs.

| Concern | Lives in |
| :--- | :--- |
| The composition engine — the separable core | `internal/agentcfg` (+ `codec`, `manifest`, `luahook`) |
| Codec helpers, its only first-party dependencies | `internal/jsonx`, `internal/tomlx` |
| Jail-side render of composed surfaces | `internal/entrypoint` (`ConfigurePackSurfaces`) |
| Host-side render, and `yolo config render` | `internal/cli/config.go` |
| The wall itself | `internal/cli/run` (container flags), `internal/macosuser` (Seatbelt) |
| Reproducibility | `flake.nix` |

**Reads with:** [`pack-system.md`](pack-system.md) (what a pack contributes and how it is rendered),
[`third-party-pack-logic.md`](third-party-pack-logic.md) (how a pack ships *logic*, and where the
unbuilt tier lives), [`../design/yolo-as-environment-manager.md`](../design/yolo-as-environment-manager.md)
(the same charter worked through from the user's side, and still the design of record for the
unbuilt notches and verbs).

---

## The separability test: could you use the config engine without a jail?

**Yes, for the engine.** `internal/agentcfg` is a leaf. Its complete transitive first-party
dependency set is its own subpackages plus the two codec helpers, and its only third-party
dependencies are a TOML decoder and `gopher-lua`. There are **zero** references to `internal/cli`,
`internal/entrypoint`, `internal/paths`, `internal/config`, mounts, containers, or anything
jail-shaped. Re-measure with:

```console
$ go list -deps ./internal/agentcfg/... | grep yolo-jail
```

And it *runs* jail-free: `yolo config render <agent>` executes host-side, composes from the real
host config file, and prints the result — no `/ctx` mount, no container. The engine never touches
the filesystem itself; path policy belongs to the CLI, which expands `~/` and resolves the workspace
placeholder before handing a surface over.

The surface **data** is jail-independent too, and it was not always: a surface once carried the
literal path `/workspace` in a defaults layer, which was a latent correctness bug on any run whose
workspace is elsewhere. The seam that fixed it is `agentcfg.WorkspacePlaceholder` (`${workspace}`),
substituted by `SubstituteWorkspace` at render time in both the boot path and `config render`. That
is also what un-blocks surfaces-as-pack-data: a pack cannot ship a jail-specific absolute path.

### What the jail adds is not capability but *two sides*

| Concern | With a jail | Without |
|---|---|---|
| where the `host` layer comes from | a `:ro` mount under `/ctx` | just the real host file |
| where the output goes | the jail's writable overlay | your actual `~/.claude/` — you are composing over your own config |
| the credential boundary | meaningful: two sides, one of which must not see the other's secrets | **meaningless** — there is only one side |
| capture sidecars | per-workspace, agent edits vs yolo renders | still coherent, but it is now *your* edits vs the tool's |

That last row is the finding: **composition is jail-independent; the credential boundary is not.** A
standalone "compose my agent config from layers" tool is a coherent product — roughly
*chezmoi/home-manager for agent config*. It is just not a *sandbox* feature, and the
user-scope-only rule on `host_files` would have nothing to enforce.

## What is jail-shaped, and what only looks it

Almost nothing outside the confinement policy is *fundamentally* jail-shaped: not the packages, not
the surfaces, not the skills, not the briefings, not the loopholes. Half the config surface consists
of **grants** — ways to poke a hole through a wall — which presuppose a wall without creating one,
and go **inert rather than wrong** when the wall is absent. That inertness is the property that
makes the notches coherent at all.

Reproducibility is the one thing that is not commoditizable-adjacent. Another image format could
supply the same binaries, but nix is load-bearing for a property nothing else here provides — a
*reproducible* environment, which is what makes "the same jail on two machines" true rather than
aspirational.

So the pack system is **also** not fundamentally a jail feature. It is an agent-config distribution
mechanism that yolo happens to be a good host for. Worth knowing before deciding how much of yolo
becomes packs.

## The three code-execution seams

yolo ships logic three ways, and a new mechanism almost always belongs in one of them rather than
being invented. Each has a real precedent to copy.

| Seam | What executes | Isolation |
| :--- | :--- | :--- |
| **Lua** (`internal/agentcfg/luahook`) | a script, in-process | **Strong.** `openSandboxLibs` opens base, table, string and math and nothing else — `package`, `os`, `io`, `debug`, `coroutine` and `channel` are deliberately never opened — under an instruction budget. Pure computation: it cannot read a file or spawn a process. |
| **Loopholes** (`internal/loopholes`) | a host or jail daemon named by a manifest | None by design, but the command is an argv `[]string` rather than a shell string, so there is no shell-injection surface. A loophole's own directory is bind-mounted `:ro` into the jail, and a loophole shipping and running its own script is a *tested* contract. |
| **MCP servers** | arbitrary executables, installed on first boot | None. **This is already pack-shipped third-party logic in production** — and it is the ecosystem's own model: fetched at runtime, cached in a writable overlay, invoked over a JSON protocol. |

The third row answers "is there a story for shipping compiled third-party code?" — there already
is, and the same shape recurs in the lazy agent launchers and the LSP installs. A pack that needs
real logic should look like an MCP server, not like a plugin.

## Traps

> [!WARNING]
> **Do not propose Go plugins (`plugin.Open`). The finding is a measurement, not a preference.**
> `plugin` requires CGO and the shipped binaries are built `CGO_ENABLED=0`, so `plugin.Open` returns
> `plugin: not implemented`. Forcing CGO on does not rescue it: the hermetic build also passes
> `-trimpath`, and a non-trimpath plugin fails with *"plugin was built with a different version of
> package internal/goarch"*. Underneath both is a version lock — a plugin must match the host
> binary's Go version and every dependency version exactly, and yolo is built by nix while packs are
> not — so flipping CGO would buy a mechanism that still breaks on every toolchain bump.

> [!WARNING]
> **Nix hermeticity is "no *unpinned* network", not "no network".** Fixed-output derivations and
> builtin fetchers are allowed to reach out, because an output hash pins the result — and `flake.nix`
> already relies on this for pinned-nixpkgs specs and fetched source tarballs. So "we cannot fetch
> that at build time" is the wrong reason to reject something. The right reason is **cost and
> coupling**: pack *content* as an image input puts the pack in the image's store path, so every pack
> edit costs a full rebuild plus a host load — which destroys the point of packs. A pack needing a
> system **capability** is different, and feeds the derivation exactly as a `packages` entry does.

> [!WARNING]
> **The `goSrc` fileset is a constraint on *linking*, not on language.** The hermetic build sees only
> `go.mod`, `go.sum`, `vendor/`, `cmd/`, `internal/` and `packs/`, so a Go package outside that set
> vanishes. That rules out exactly one thing: linking third-party Go code into the yolo binary. A
> separate binary, built separately and invoked over a protocol, is unaffected — which is why "pack
> logic can't be Go" is false. See [`third-party-pack-logic.md`](third-party-pack-logic.md).

## What this does not license

- **Not a licence to extract `agentcfg` today.** The engine being a leaf is an argument for
  extracting the *engine* if reuse is the goal — not for extracting the *agent support*, which is a
  different project and the only one that needs the pack machinery.
- **Not a claim that the wall is unimportant.** It is small, well-understood and largely borrowed;
  that is a statement about where the novelty is, not about whether the wall matters. A jail is
  still the default, and still what nearly everyone wants nearly all the time.
- **Not a claim that a grant is meaningful without a wall.** Grants go inert at the host notch. A
  feature that reads as "poke a hole" and does something *else* when there is no hole is a bug in
  that feature, not an exception to the model.
- **Not a substitute for the notch design.** Which confinement notches exist, what each refuses, and
  which verbs realize a description are
  [`../design/yolo-as-environment-manager.md`](../design/yolo-as-environment-manager.md)'s, and part
  of that is still unbuilt.
