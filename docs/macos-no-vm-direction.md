# The macOS "no-VM" problem: what we actually want, what we missed

**Status:** DECIDED (2026-07-16) — **pursue BOTH, as ONE composed product**,
not two competing backends. See "## Decision" below. Supersedes the framing in
[macos-backend-direction.md](macos-backend-direction.md), which argued for
excision on a premise ("no emulation") that turns out not to be the
maintainer's real concern ("no VM").
**Date:** 2026-07-14 (reframe) → 2026-07-16 (decision)
**Reads with:** [macos-backend-direction.md](macos-backend-direction.md) (the
prior, narrower decision), [happy-path-principle.md](happy-path-principle.md),
[jail-version-predictability.md](jail-version-predictability.md).

## Decision — three orthogonal axes, don't blur them

The recurring confusion is treating "runtime", "builder", and "packages" as
one choice. They are independent:

| Axis | Decides | Options |
|------|---------|---------|
| **1. Runtime** (*where the agent runs*) | VM or not | **(a) Container** (Apple Container / Podman) — Linux container in a VM, runs the Linux nix image • **(b) macos-user** — native macOS user + Seatbelt, **NO VM, no Linux image** |
| **2. Builder** (*how you get the Linux image*) | **exists ONLY for runtime 1(a)** | Cachix download (no VM) • nix-darwin linux-builder (transient VM, on-demand) • ~~Colima~~ (rejected — see below) |
| **3. Packages** (*how `packages:` is materialized*) | per runtime | Container → baked into the aarch64-**linux** image • macos-user → native **aarch64-darwin nix devShell** (`nix print-dev-env`), NOT `nix profile` (see [macos-nix-shell-backend-proposal.md](macos-nix-shell-backend-proposal.md); the imperative profile path was rejected as drift-prone) |

**Key insight that un-blurs it:** the *builder only exists for the container
runtime*. **macos-user needs no builder at all** — it runs native darwin
binaries, so there is no Linux image to produce. "Which builder?" is a
question *inside* Track A only.

**These are NOT competing — they compose into one product:**
- **macos-user is the fast native default** (no VM, `packages:` via darwin nix).
- **The AC container is the fallback cell** for what native darwin can't cover:
  a `packages:` entry with no darwin build, or when the user wants VM-grade
  isolation over Seatbelt. This satisfies
  [happy-path-principle.md](happy-path-principle.md) — one path per matrix
  cell, container is the "needs real Linux" escape hatch.

So "pursue both" = **Track A (container, measured + Cachix-on) as the fallback,
Track B (macos-user + native nix) as the fast default** — one product.

### Colima: rejected (answering the recurring question)

Colima keeps coming up as "just run the Linux builds and shut down after." It
loses on every axis:
- It is a **builder** question (axis 2), and it's **still a VM** — zero help for
  the no-VM goal.
- It's a **Docker/containerd VM, not a nix builder**: building nix in it means
  installing nix *inside* Colima and copying closures — strictly MORE setup
  than `nix-darwin linux-builder` (the one-command purpose-built tool). Fails
  happy-path criterion 2 (least per-user infra); it's the doc's canonical
  "NOT supported" example.
- **"Shut down after"** is the idle-stop we already designed for the nix-darwin
  on-demand builder (`src/cli/builder.py`). The capability you want exists; it
  isn't Colima.

### Acceptance bar for Track B (do not repeat the first mistake)

macos-user was excised because it delivered SandVault's sandbox and dropped
yolo's nix layer. The revive is only worth it if, **from day one**, it honors
`packages:` via native **aarch64-darwin** nix (a devShell realized with
`nix print-dev-env` — see
[macos-nix-shell-backend-proposal.md](macos-nix-shell-backend-proposal.md)). If it can't carry the
nix layer, don't ship it — that's the line between "a yolo backend" and "an SV
clone." Recoverable at git tag `macos-user-experiment` (1471-line
`src/cli/macos_user.py` + tests), excision unpushed.

## The one-paragraph problem statement

On **Linux it takes two seconds and just works.** On **macOS it is bad enough
that a reasonable person reaches for a different tool.** The reason is the
**Linux VM** every macOS container runtime interposes: it's slow to start,
makes you **guess a RAM ceiling ahead of time**, **permanently steals that RAM**
while it runs, and drags in the whole class of VM problems (disk image growth,
filesystem indirection over VirtioFS, daemon lifecycle). We want a macOS path
that is **as fast and convenient as Linux** — no VM, no RAM pre-commitment —
**without throwing away the things that make it yolo and not just a sandbox
wrapper.** If we can't keep yolo's value, there is no reason for it to exist:
people should just use SandVault.

## What I got wrong last time (correcting the record)

The prior decision doc excised `macos-user` arguing "the container is already
native arm64, so there's no emulation to avoid." That is **true but answers
the wrong question.** The maintainer never cared about emulation — the
container was never emulating on arm. The concern was and is the **VM itself**:

- **Emulation ≠ overhead.** A *native* arm64 Linux VM still boots slowly,
  still reserves RAM up front, still permanently holds it, still puts a
  filesystem boundary between host and `/workspace`. "No emulation" does not
  mean "no cost." I conflated the two and used it to justify removal.
- `macos-user` was the **only** thing we built that actually removed the VM.
  Excising it on the emulation argument deleted the solution to the real
  problem. That may have been the wrong call, and this doc reopens it.

## What is actually true today (verified in the tree)

- **The VM has been there since April** (`df93fe8`, "full macOS support with
  three container runtimes"). Podman Machine and Apple Container are both
  Linux VMs. On an Apple-Silicon Mac the flow is: arm64 Mac → **Linux VM** →
  arm64 Linux container. Native arch, yes; VM-free, no.
- **`/workspace` mount + per-workspace overlays have worked on macOS the whole
  time** (`_workspace_readonly_mount_args`, `ws_state`, VirtioFS in
  `run_cmd.py`). The maintainer's memory of these is correct — they exist and
  still work on the container path.
- **`macos-user` (July 12–14, excised July 14) genuinely removed the VM** — it
  ran the agent as a native macOS user + Seatbelt, no Linux, no container.
- **But `macos-user` never consumed the workspace `packages` (nix) config.**
  Confirmed: its bootstrap generated mise config, shims, and agent configs,
  but there is no `packages`/`_effective_packages`/nix-profile handling in it.
  So it delivered SandVault's *sandbox* and dropped yolo's *nix-predictable
  package layer* — which is exactly why it read as "an SV clone that does what
  SV does and nothing yolo does." **That was a gap in the implementation, not
  proof the idea is wrong.**

## What makes it yolo (and not just SandVault) — the value we must keep

If a no-VM macOS path can't preserve most of this, it isn't worth building —
SandVault already exists. The non-negotiable-ish yolo value:

1. **Predictable, declarative packages.** `packages: [...]` in
   `yolo-jail.jsonc` resolved against a **locked nixpkgs** (`flake.lock`), so
   every machine/agent gets byte-identical tools. This is *the* yolo
   differentiator (see [jail-version-predictability.md](jail-version-predictability.md)).
   SandVault has nothing like it — it uses whatever's on the host.
2. **Per-workspace config surface.** `mcp_servers`, `lsp_servers`,
   `mise_tools`, `security.blocked_tools`, `network`/`ports`,
   `env_sources` — all per-project, all applied by the entrypoint.
3. **Per-workspace isolation.** Separate `/workspace` + overlay state per
   project, not one shared home.
4. **The agent library model.** Pick which agents install per project;
   consistent auto-YOLO-flag injection; the config-safety approval flow.
5. **Cross-platform sameness.** The *same* `yolo-jail.jsonc` behaves the same
   on Linux and macOS. A macOS-only backend that reads a different subset of
   config breaks this.

The credential/isolation boundary (separate user + Seatbelt) is the part we'd
**borrow** from SandVault. Everything above is what we must **not** lose.

## The core tension (state it plainly)

yolo's package predictability is **built on a Linux nix image**
(`flake.lock` → `aarch64-linux` closure). That image *is* the Linux artifact
that *needs a Linux userland* to run — which on macOS means a VM. So:

> The very mechanism that makes yolo predictable (a locked **Linux** nix
> image) is the mechanism that forces the VM on macOS.

A no-VM macOS backend therefore cannot run the Linux image. It must deliver
the *properties* (predictable, declarative, per-workspace, isolated) through a
**macOS-native** substrate. The question is whether that's achievable without
either (a) a VM or (b) degrading into "just a sandbox wrapper."

## Options (honest, with the hard parts named)

### A. Keep the VM, attack its specific pains (no new backend)
Stop treating "VM" as monolithic and fix what actually hurts:
- **RAM guessing / permanent steal:** modern Apple Virtualization.framework
  (Apple Container) supports more elastic memory than the classic
  fixed-size Podman Machine. Investigate whether AC's per-container VM can
  avoid the up-front `--memory` ceiling and release RAM on exit — if so, the
  worst two pains (guess ahead / steal forever) may already be softer under
  AC than under Podman Machine.
- **Startup:** a warm/prebooted VM, or keeping one AC VM alive, amortizes boot.
- **Verdict to test:** maybe "the VM" is really "Podman Machine's VM," and
  Apple Container already fixes most of it. **Cheapest to investigate; do this
  first.** Keeps 100% of yolo value; may or may not get to "Linux-fast."

### B. No-VM native backend, but carry the nix layer natively *(the real ask)*
Revive a `macos-user`-style backend (native user + Seatbelt, no VM) **and fix
the gap that doomed it**: make it honor `packages` by resolving them from
**native macOS nix** (`aarch64-darwin`), not the Linux image. Nix runs
natively on macOS with no VM; `nix profile`/a per-workspace profile could
materialize the declared packages as darwin binaries into the sandbox.
- **Keeps:** no VM (fast, no RAM ceiling), predictable declarative packages
  (via darwin nixpkgs + a lock), per-workspace config, the sandbox boundary.
- **Hard parts (name them honestly):**
  - Packages resolve to **darwin** builds, not the Linux ones — so it is *not*
    byte-identical to the Linux jail, only *declaratively* identical
    (same nixpkgs attrs, different platform). "Predictable across macOS
    machines," yes; "identical to Linux," no. Is that good enough? (Probably —
    it's what any native tool gives you.)
  - Not every nixpkgs attr builds on darwin / is cached for aarch64-darwin;
    some `packages:` entries that work in the Linux image may not have a mac
    build. Need a graceful "not available on macOS" story.
  - The per-workspace config surface (mcp/lsp/mise/blocked-tools) must be
    re-applied in the native context — the entrypoint already runs
    library-style outside a container (proved by `_entrypoint_preflight` and
    the old macos-user bootstrap), so this is wiring, not invention.
  - Isolation is Seatbelt-grade, not VM-grade (documented tradeoff).
- **This is the option that matches the stated goal.** It's more work than A,
  but it's the only one that delivers "Linux-fast on macOS" *and* keeps the
  nix-predictable identity.

### C. Accept macOS = VM, document it, invest elsewhere
Declare the container (VM) the only macOS path, make it as painless as
possible (Option A's tuning), and tell users macOS carries VM overhead by
nature. Honest, cheap, but concedes the thing the maintainer explicitly finds
unacceptable ("bad enough to use another software package").

## Recommendation

**Do A's investigation first (days), then decide B.** Specifically:
1. **Measure the real pain under Apple Container**, not Podman Machine: does AC
   already avoid the fixed RAM ceiling and release memory on exit? What's cold
   vs. warm startup? This tells us how much of "the VM problem" is actually
   just Podman Machine, and is cheap.
2. If AC doesn't close the gap, **pursue B** — but build it the right way this
   time: the acceptance bar is **"honors `packages:` via native darwin nix"**
   from day one. If it can't carry the nix layer, don't build it — that's the
   line between "a yolo backend" and "an SV clone," and we already learned
   that lesson the expensive way.

`macos-user` is fully recoverable (git tag `macos-user-experiment`, and the
excision is **unpushed** to origin), so reviving it for Option B costs
nothing but the decision.

## Open questions (need the maintainer)

1. **Is "declaratively identical (darwin builds)" acceptable**, or does macOS
   need byte-identical-to-Linux tools? (If the latter, only a VM can do it,
   and the answer is Option A/C.)
2. **How much VM pain does Apple Container actually still have?** (Option A
   investigation — needs measurement on real hardware; I can't from Linux.)
3. If we revive `macos-user` for Option B, **which `packages:` failures are
   acceptable** — hard-error, warn-and-skip, or a per-platform `packages`
   override in config?
4. Is the **Seatbelt (non-VM) isolation level** acceptable for the macOS
   audience, given the container/VM stays available for anyone who wants the
   stronger boundary?
