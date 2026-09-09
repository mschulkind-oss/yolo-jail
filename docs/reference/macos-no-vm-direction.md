---
status: current
verified: 2026-09-09
verified_commit: d8cf1cf8
covers:
  - internal/macosuser/
  - internal/darwinpkg/
  - internal/containerbuilder/
  - internal/config/validate.go
tags: [macos, backends, nix, decision, packages, builder]
summary: "The standing macOS direction: runtime, builder and packages are three orthogonal axes, and the two macOS backends compose into one product rather than competing — macos-user as the fast native default, an Apple Container cell as the fallback that needs real Linux. Includes the acceptance bar that separates a yolo backend from a sandbox wrapper, and why Colima is refused."
---

# The macOS direction — three axes, one composed product

**Status:** CURRENT as of 2026-09-09, verified against `d8cf1cf8`.

yolo ships **two** macOS paths and they are not competing backends: `macos-user` is the fast
native default, and an Apple Container cell is the fallback for what native darwin cannot
cover. This document is the standing decision behind that shape, and the vocabulary that keeps
the recurring argument from restarting.

**The problem it answers.** On Linux a jail starts in seconds and just works. On macOS every
container runtime interposes a **Linux VM**: slow to start, a RAM ceiling you have to guess
ahead of time, that RAM permanently held while it runs, plus the whole class of VM problems —
disk-image growth, a filesystem boundary over the workspace, daemon lifecycle. The goal is a
macOS path as fast and convenient as Linux, with **no VM and no RAM pre-commitment**, without
throwing away the things that make this yolo rather than a sandbox wrapper.

| Component | Lives in |
| :--- | :--- |
| The native, no-VM backend | `internal/macosuser` |
| Native darwin package realization (the acceptance bar) | `internal/darwinpkg` |
| The on-demand Linux builder for the container runtimes | `internal/containerbuilder` |
| The runtime enumeration and its refusals | `internal/config` (`validate.go`) |

**Reads with:** [`macos-user-nix-and-features.md`](macos-user-nix-and-features.md) (that
backend as built), [`nix-across-backends.md`](nix-across-backends.md) (what nix produces for
each), [`../design/happy-path-principle.md`](../design/happy-path-principle.md) (one path per
matrix cell), [`../guides/macos.md`](../guides/macos.md) (user-facing setup).

---

## The three axes — do not blur them

The recurring confusion is treating *runtime*, *builder* and *packages* as one choice. They
are independent.

| Axis | Decides | Options |
| :--- | :--- | :--- |
| **1. Runtime** — where the agent runs | VM or not | **(a)** a container (Apple Container / podman) — a Linux container inside a VM, running the Linux nix image; **(b)** `macos-user` — a native macOS account plus Seatbelt, **no VM and no Linux image** |
| **2. Builder** — how you get the Linux image | **exists only for runtime 1(a)** | a binary-cache download (no VM — the happy path); an ephemeral container builder, which offloads an uncached build to a tiny nix-plus-sshd container on the runtime that is *already up* and then tears it down — no VM, no `sudo` |
| **3. Packages** — how `packages:` is materialized | per runtime | container → baked into the Linux image; `macos-user` → a native darwin `buildEnv` profile |

**The insight that un-blurs it: the builder exists only for the container runtime.**
`macos-user` needs no builder at all — it runs native darwin binaries, so there is no Linux
image to produce. "Which builder?" is a question *inside* the container track.

### They compose into one product

- **`macos-user` is the fast native default.** No VM, `packages:` through darwin nix.
- **The container cell is the fallback** for what native darwin cannot cover: a declared
  package with no darwin build, or a user who wants VM-grade isolation over Seatbelt.

That satisfies the happy-path principle — one path per matrix cell, with the container as the
"needs real Linux" escape hatch — rather than two backends the user has to choose between.

### The acceptance bar

> **A macOS backend that cannot carry the nix layer is not a yolo backend.**

`macos-user` was excised once, and the reason is the bar: its first version delivered a
sandbox and dropped yolo's nix layer entirely — no `packages:` handling of any kind — so it
read as a clone of an existing sandbox tool that did nothing yolo does. **That was a gap in
the implementation, not proof the idea was wrong**, and the revive was conditioned on honoring
`packages:` through native darwin nix **from day one**. It does; a run-plan invariant asserts
every darwin store `bin` dir actually reached the launch PATH, which is the bar expressed as a
check rather than as a promise.

## What must survive on any macOS path

If a no-VM path cannot preserve most of this, it is not worth building — a plain sandbox tool
already exists. In rough order of how much it distinguishes yolo:

1. **Predictable, declarative packages.** A declared list resolved against a **locked
   nixpkgs**, so every machine and every agent gets the same tools. This is *the*
   differentiator; a sandbox wrapper uses whatever is on the host.
2. **A per-workspace config surface** — MCP and LSP servers, mise tools, blocked tools,
   network and ports, env sources — all per project, all applied by the same generators.
3. **Per-workspace isolation** — separate workspace and overlay state per project, not one
   shared home.
4. **The pack model** — which tools install per project, the autonomy flags a pack injects,
   the config-safety approval flow.
5. **Cross-platform sameness** — the same config file behaves the same on Linux and macOS. A
   macOS-only backend that reads a *different subset* of config breaks this, which is why
   every feature a container implements with a flag or a mount must either work natively or
   say it does not.

The credential and isolation boundary — a separate account plus Seatbelt — is the part
borrowed from prior art. Everything above is what must not be lost.

## The core tension, stated plainly

> The very mechanism that makes yolo predictable — a locked **Linux** nix image — is the
> mechanism that forces the VM on macOS.

A no-VM macOS backend therefore **cannot run the Linux image**. It has to deliver the
*properties* (predictable, declarative, per-workspace, isolated) through a macOS-native
substrate. That is what the third axis is for, and it has one honest consequence:

**Packages on `macos-user` are declaratively identical, not byte-identical.** Same nixpkgs
attributes, different platform. "Predictable across macOS machines" holds; "identical to the
Linux jail" does not, and cannot without a VM. That is accepted — it is what any native tool
gives you — and it is why the container cell stays available for anyone who needs the Linux
artifact itself.

**Isolation is Seatbelt-grade, not VM-grade.** Also accepted, and documented rather than
hidden: the container path remains for anyone who wants the stronger boundary.

**A declared package with no darwin build is a hard error**, not a warn-and-skip. A silently
dropped tool the config *declared* masks a typo and diverges from the documented contract; see
[`macos-user-nix-and-features.md`](macos-user-nix-and-features.md#ordering-and-what-aborts) for
the mechanism and the platform escape hatch.

## Refuted alternatives

> [!WARNING]
> **Colima is refused, and it loses on every axis.** It reads as "just run the Linux builds
> and shut down after", and: it is a **builder** question, so it helps the container track
> only; it is **still a VM**, which is zero help for the no-VM goal; it is a Docker/containerd
> VM rather than a nix builder, so building nix in it means installing nix *inside* Colima and
> copying closures — strictly **more** per-user setup than the ephemeral container builder,
> which needs none; and the "shut down after" capability it is wanted for is what the ephemeral
> builder gets for free, since it exists only during a build.

> [!WARNING]
> **"No emulation" is not the argument, and using it once cost the backend.** The first
> excision argued that the container is already native arm64 so there is no emulation to
> avoid. True, and it answers the wrong question: a *native* arm64 Linux VM still boots slowly,
> still reserves RAM up front, still holds it, and still puts a filesystem boundary between the
> host and the workspace. **Emulation and overhead are different things.** `macos-user` was the
> only thing that actually removed the VM, and it was deleted on an argument about something
> else.

> [!WARNING]
> **A user's own persistent Linux builder is an escape hatch, not a shipped option.** Someone
> who already runs one in their own nix configuration will use it, and that is fine. It is not
> a path yolo provisions, because it is per-user infrastructure — the thing the happy-path
> principle exists to keep off the default path.

**Accepting "macOS means a VM"** and investing only in tuning it is the option the goal
rejects: the whole premise is that the VM overhead is bad enough that a reasonable person
reaches for a different tool. Tuning the container path is still worthwhile — it is the
fallback cell — but it is not the answer to the stated problem.

## What this does not license

- **Not** two competing macOS backends with a user-facing choice between them. One composed
  product: native default, container fallback.
- **Not** a macOS backend that reads a different subset of config than Linux. Cross-platform
  sameness is on the must-survive list, and a divergence has to be **said** at the launch.
- **Not** a second hypervisor for builds. The builder runs as a container on the runtime that
  is already up.
- **Not** relitigating the axes. A proposal that mixes runtime, builder and packages into one
  choice is the confusion this document exists to end.

## Current values

Verified at `d8cf1cf8`. The prose above explains what each of these is for; this table is the
only place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Resolvable runtimes | `podman`, `container`, `macos-user` — and `docker` is a refusal naming its replacement | `internal/config/validate.go` |
| Package realization, `macos-user` | a darwin `buildEnv` profile, **not** an imperative nix profile | `internal/darwinpkg` |
| Linux builder for the container runtimes | an ephemeral nix-plus-sshd container, driven over nix's remote-builder protocol | `internal/containerbuilder` (`BuilderImage`, `BuilderContainer`, `BuilderHostPort`) |
| Builder key material | a per-machine key dir under the machine storage root | `containerbuilder.BuilderKeyDir`, `BuilderKey` |
| The acceptance-bar check | every darwin store `bin` dir must reach the launch PATH | `macosuser.PlanInvariants` |

## Why it's this way

Forward-facing rulings a maintainer would otherwise undo.

| Ruling | Why it stays |
| :--- | :--- |
| **Pursue both, as one composed product** — not two competing backends | Framing them as competitors forces a user-facing choice between "fast" and "works for this package", which is exactly the matrix cell the happy-path principle says should have one path. The container is the *escape hatch*, and naming it that is what keeps the native path the default. |
| **The builder axis exists only for the container runtime** | Every argument that starts "which builder should macOS use?" is a container-track question. Blurring it is what makes a VM-based builder look like an answer to a no-VM goal. |
| **A darwin `buildEnv` profile, not an imperative nix profile** | An imperative profile accumulates state nobody declared and drifts from the config that was supposed to define it. A `buildEnv` is a pure function of the declared list. |
| **The persistent on-demand VM builder is gone; the ephemeral container builder is the only shipped one** | A VM builder needs idle-stop logic, a RAM commitment and `sudo`. A builder that exists only during a build is zero-idle by construction, so the whole idle-stop concern disappears rather than being managed. |
| **Declaratively identical is good enough; byte-identical needs a VM** | It is what any native tool gives you, and pretending otherwise would mean either shipping a VM by default or claiming a guarantee the platform cannot make. The container cell is the honest answer for anyone who needs the Linux artifact. |
| **Seatbelt-grade isolation is documented, not hidden** | The stronger boundary is one config key away. A backend that quietly implied VM-grade isolation would be the more dangerous failure. |
