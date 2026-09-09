---
status: current
verified: 2026-09-09
verified_commit: 356bcec8
covers:
  - internal/config/validate.go
  - internal/config/confinement.go
  - internal/render/confinement.go
  - internal/containerbuilder/
tags: [principle, support-matrix, backends, builders, docs]
---

# Principle: fill the matrix, don't support every tool

**Status:** PRINCIPLE, current as of 2026-09-09, verified against `356bcec8`.

**Audience:** anyone adding a setup path, a runtime, a builder, an installer, or a "how do I do X on
platform Y" answer to yolo-jail. Read this before adding a second way to do something.

A principle keeps its rationale on purpose — the verdict alone ("only one path") reads as
arbitrary austerity, and the reasoning is what makes it applicable to a case this page has never
seen.

**Sibling principles:** [`extension-point-principle.md`](extension-point-principle.md) (who designs
an extension point) and [`gate-placement-principle.md`](gate-placement-principle.md) (put the gate
where the authority changes).

## The principle

For any capability, pick **one** happy-path tool that works across the whole support matrix, and
make *that* the documented, tooling-blessed path. Do not enumerate every tool that *could* work.
Coverage of the matrix is the goal; breadth of supported tools is not.

> Fill the matrix. Support one path per cell. Everything else is an escape hatch at most — never a
> co-equal option.

**The matrix** is backends × platforms. It has three backends — `podman`, `container` (Apple
Container) and `macos-user` — and `internal/config`'s `validateRuntime` is the enumeration. Docker
was a fourth and is not an escape hatch: it hard-errors by name, so a config that still names it
fails rather than silently degrading.

## Why

Every additional "supported" tool is a standing cost: more docs to keep correct, more `yolo check`
probes, more failure modes, more bug reports, more drift as each tool changes. A menu of options also
pushes the *choice* onto every user — which is exactly the work we should do once, for them. Three
mediocre options a user has to evaluate is worse than one that just works.

The upfront cost is fine. The maintainers happily pay a one-time setup cost — publish a cache, wire a
CI job — so that every user, on every platform, at any time, gets the trivial path. "Easy and
consistent for everyone, even if there's work up front" beats "flexible but everyone fends for
themselves."

## How to choose the one tool

Rank candidates by, in order:

1. **Matrix coverage.** Does it work on *every* platform we support (Linux, macOS arm64, macOS
   Intel), for a user with no special setup? A tool that only works in some cells loses to one that
   works in all.
2. **Least per-user infrastructure.** Prefer *download* over *build*; prefer *built-in* over *install
   a third-party thing*; prefer *zero daemons* over *run a VM*. The best path asks the user to
   install nothing new.
3. **Consistency.** The same command/answer on every platform beats a different dance per OS. One
   thing to learn, document, and debug.
4. **Acceptable one-time maintainer cost.** Prefer paths where *we* absorb a one-time setup (a cache,
   a CI job, a baked config) to give users the easy path — over paths that push recurring effort onto
   each user.
5. **Minimal support surface.** Fewer moving parts we have to keep working.

A second option only earns its place if it covers a matrix cell the first one genuinely cannot — not
because it's someone's preference or "also works."

## How this shows up in the code

- **`yolo check` and CLI messages name THE path, not a menu.** When something is missing, point at
  the one fix. Don't list three.
- **Tooling is wired for one path.** `just` recipes, CI jobs, and config default to the chosen tool.
  Alternatives, if documented at all, live in a prose "escape hatch" note — never in a check probe or
  a recommended command.
- **Docs lead with the happy path.** Alternatives come after, clearly marked "you probably don't need
  this," and are dropped entirely once they stop covering a unique cell.
- **A dial exposes presets, not a policy vector.** The `confinement` dial has three notches
  (`internal/config/confinement.go`) even though the enforcement primitives underneath are
  composable (`internal/render/confinement.go`). Only the three presets are user-selectable, and that
  restriction is this principle: the composability is a maintainer's tool, not a menu.

## Worked example — building the jail image on macOS

The capability: get a runnable Linux OCI image on any host.

- **Chosen happy path: a published binary cache (Cachix).** Fills every matrix cell identically — the
  user *downloads* the prebuilt image; no builder, no VM, no Nix knowledge. We pay the one-time cost.
  This is the answer for everybody, everywhere. The substituter and its trusted key are declared in
  `flake.nix`'s `nixConfig`; the delivery work is
  [`handoff-cachix-cache.md`](../plans/handoff-cachix-cache.md).
- **Single fallback, only when the cache can't help: the on-demand container builder.** Needed just
  for a custom package that isn't cached, or before the cache is live. A normal `yolo` run offloads
  the from-source build to a tiny nix+sshd Linux builder *container* on whichever runtime is already
  up, then tears it down (`internal/containerbuilder`). It wins criteria 1–5: zero setup (automatic,
  part of the build), no VM, no `sudo`, no first boot, and zero idle RAM — and it reuses the runtime
  the jail already needs, so it covers every runtime cell.

> [!WARNING]
> **Do not re-propose a persistent VM builder, Colima, or a hand-driven QEMU VM as a shipped
> builder.** Each *could* work; none covers a cell the container builder doesn't, so none is worth
> the support surface. `nix-darwin linux-builder` (a launchd-managed VM) loses criteria 2 and 5 —
> `sudo` setup, an interactive first boot, idle RAM. Colima is a Docker VM rather than a Nix builder,
> so it is strictly more setup and loses criterion 2. A hand-driven QEMU VM adds moving parts and
> loses 5. A user's own Linux builder — nix-darwin `linux-builder`, or a remote host in
> `/etc/nix/machines` — survives only as a one-line "advanced" prose escape hatch: it is their nix
> config, orthogonal to ours, and Nix uses it if present.

The instinct this refuses is the familiar one: *"the error said no builder, so document Colima + QEMU
+ remote as options A/B/C/D."* Four documented options is four things to keep correct and a choice
handed back to the user.
