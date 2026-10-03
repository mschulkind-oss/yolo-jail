---
title: "Can a macOS VM give memory back while it runs? Apple Container's missing balloon, and the alternatives"
date: 2026-10-03
status: accepted
stage: DECIDED
next: "Find out why libkrun's free page reporting returned nothing on this Mac (macos-vm-runtime-comparison.md §4): a krunkit build with debug logging, or the Lima krunkit driver, shows whether reports reach the device; measure OrbStack and Docker Desktop's reclaim once their trials are installed"
tags: [research, macos, apple-container, memory, balloon, libkrun, podman]
summary: "The maintainer asked whether Apple Container has a balloon that returns memory, whether yolo could add one, and whether another macOS VM does it. Apple Container is Apache-2.0 and takes outside contributions, but attaches no balloon. The one proposal was an outside contributor's issue and two PRs, closed in a sweep of that contributor's 30 or so PRs, not on their merits. Virtualization.framework offers only a traditional balloon with no free page reporting, and the one published measurement saw the host's footprint rise, not fall, when it was driven. libkrun, the default Podman Machine provider on macOS, does report free pages and one third party saw memory come back; yolo already supports podman on macOS, so that is the path to test first."
vantage:
  status-chip: true
---

# Can a macOS VM give memory back while it runs? Apple Container's missing balloon, and the alternatives

**Status:** 2026-10-03, research only, and nothing is ruled. **Measured since:** a libkrun Podman
Machine did *not* give a freed 2 GiB back on yolo's Mac, even under host memory pressure
([the runtime comparison, §4](macos-vm-runtime-comparison.md#4-memory-does-a-vm-give-a-freed-2-gib-back)),
so §4's libkrun row is SOURCED from others' runs and contradicted by ours. Read through the GitHub API, Apple's documentation and the issue trackers named in each
item, by an agent on 2026-10-03. It follows
[the macOS backend benchmark](macos-backend-performance.md), which measured an Apple Container jail
holding all of a 2 GiB load 120 s after it ended, and giving it back only when the jail stopped
([Apple Container's memory results](macos-backend-performance.md#memory-m4-apple-container)).

**The questions,** in the maintainer's words (2026-10-03): *"I thought apple container had a balloon
to get memory back? can we tune that?"* and *"Is Apple Container open source? … Are there other
micro VM solutions we can use that do have that? Are there ways that we can put this into Apple
Container ourselves? Like if we can make this more efficient and better for people, we should."*

> **In short.**
>
> - **No balloon, and nothing to tune.** Apple Container attaches no memory balloon, and its own
>   documentation says freed guest memory is not returned ([§2](#2-the-balloon-proposal-and-what-happened-to-it)).
> - **Open source, and outside PRs are merged.** Both repositories are Apache-2.0 ([§1](#1-apple-container-is-open-source-and-takes-outside-work)).
> - **The balloon proposal was not rejected on its merits.** It was swept closed with about 30
>   other PRs from the same contributor, with no comment ([§2](#2-the-balloon-proposal-and-what-happened-to-it)).
> - **Adding one would probably not help.** Virtualization.framework's only device is a traditional
>   balloon, and the one published measurement of driving it saw the host's footprint *rise* ([§3](#3-what-virtualizationframework-offers)).
> - **libkrun does give memory back**, and it is Podman Machine's default provider on macOS, which
>   yolo already supports. That is the thing to measure first ([§4](#4-other-macos-vms), [§5](#5-adding-it-to-apple-container-ourselves)).

## Terms

- **VZ** — Apple's Virtualization.framework, the macOS hypervisor API that Apple Container, Lima's
  `vz` driver, UTM's Apple backend, Tart and vfkit all build on.
- **Memory balloon** — a virtio device through which the host asks the guest to give up memory:
  the guest's driver allocates pages, tells the host which ones, and the host can drop them. A
  **traditional balloon** works only when the host sets a target size; nothing happens otherwise.
- **Free page reporting** — a virtio balloon feature (`VIRTIO_BALLOON_F_REPORTING`, feature bit 5)
  by which the guest tells the host, on its own, which pages it has freed. It needs no host-side
  policy loop. Linux's guest side is `CONFIG_PAGE_REPORTING`.
- **Footprint** — macOS's `phys_footprint`, the figure `top`'s MEM column and Activity Monitor
  show for a process. A page freed with `MADV_FREE` stays in footprint until macOS needs it, so
  footprint can miss memory that is in fact reclaimable.
- **libkrun** — Red Hat's Apache-2.0 library for running a VM inside a process, using macOS's
  Hypervisor.framework directly rather than VZ. **krunkit** is its command-line launcher, which
  Podman Machine uses.

## 1. Apple Container is open source and takes outside work

- **Licence:** [apple/container](https://github.com/apple/container) and
  [apple/containerization](https://github.com/apple/containerization) are both Apache-2.0
  (SOURCED: GitHub's licence field).
- **Contributions:** each repository's
  [CONTRIBUTING.md](https://github.com/apple/containerization/blob/main/CONTRIBUTING.md) welcomes
  them, requires signed commits, asks that large features start as an issue, and says "Low-effort,
  unexplained submissions will be deprioritized" (SOURCED).
- **Outside PRs land:** of the PRs merged since 2026-08-01, 63 in container and 36 in
  containerization came from authors GitHub marks CONTRIBUTOR, against 5 and 2 from MEMBER
  (SOURCED: GitHub search). CONTRIBUTOR can include Apple staff who don't show their membership,
  but authors outside Apple's maintainer list were merged in that window.

## 2. The balloon proposal and what happened to it

- **[containerization #882](https://github.com/apple/containerization/issues/882)** (an issue,
  2026-08-27): an RFC from an outside contributor proposing to attach VZ's balloon and asking where
  a reclaim policy should live.
- **[#893](https://github.com/apple/containerization/pull/893)** (a PR): attaches
  `VZVirtioTraditionalMemoryBalloonDeviceConfiguration` and adds a call that sets the target size,
  compacting the guest first.
- **[#894](https://github.com/apple/containerization/pull/894)** (a PR): an opt-in reclaim loop
  that sets the target to the guest's anonymous memory plus 256 MiB and backs off when the guest
  refaults pages.
- **Their size:** both PRs show over 2,000 changed lines because each is stacked on six or seven
  others. The balloon commits alone come to about 735 lines, about 50 of them in the VZ code and
  most of the rest the policy and its tests (SOURCED: the commits through the GitHub API).
- **Their test checks less than the description claims.** The description says it "asserts the
  host's footprint falls"; the test checks only that the guest's `MemFree` drops (SOURCED: the
  patch).
- **The closure:** a listed Apple maintainer closed all three on 2026-08-28, with no comment or
  review, in one sweep with every one of the about 30 PRs that contributor opened on 2026-08-27
  (SOURCED: the issue events API). INFERRED: a reaction to the batch, plausibly under the
  "discuss first" and low-effort rules above, and not a ruling on ballooning.
- **No maintainer has said anything about plans.** The other requests, container
  [#1698](https://github.com/apple/container/issues/1698),
  [#1867](https://github.com/apple/container/issues/1867) and
  [#1921](https://github.com/apple/container/issues/1921), have no maintainer reply (SOURCED).

## 3. What Virtualization.framework offers

- **One device, the traditional balloon, since macOS 11.** Apple's
  [documentation](https://developer.apple.com/documentation/virtualization/vzvirtiotraditionalmemoryballoondevice)
  says that when the target is lowered, "the virtual machine releases those pages back to the host"
  (SOURCED). VZ has no free page reporting device, and Virtualization's updates page has no 2026
  entry about memory (SOURCED).
- **The one published measurement contradicts the documentation.** On macOS 26.5.2, a commenter on
  [lima-vm/lima #4220](https://github.com/lima-vm/lima/issues/4220) lowered the target from 8 GiB to
  1 GiB. The guest gave up exactly 7 GiB, and the host's footprint rose by about 2 GB and never
  fell. The guest's feature bits showed VZ offering only `MUST_TELL_HOST` and `DEFLATE_ON_OOM`, not
  `REPORTING` (SOURCED, one run by one person). OrbStack's author found the same in 2023: "nothing
  actually seems to get freed" ([Hacker News](https://news.ycombinator.com/item?id=34720219)).
- **No VZ-based tool drives the balloon.** Lima, UTM and vfkit attach it and never set a target;
  Lima's adaptive controller, [#4828](https://github.com/lima-vm/lima/pull/4828), is still open
  (SOURCED).
- **Apple's guest kernel is ready**: it enables `CONFIG_VIRTIO_BALLOON` and
  `CONFIG_PAGE_REPORTING`
  ([§2.2 of the benchmark](macos-backend-performance.md#22-memory-backed-on-first-touch-kept-until-the-container-stops)).
  The host side is what is missing.

## 4. Other macOS VMs

| | Open source | Gives memory back while running | Usable as a yolo backend |
| :--- | :--- | :--- | :--- |
| Apple Container (VZ) | yes, Apache-2.0 | no | yes, today |
| Lima / Colima on `vz` | yes | no ([lima #2789](https://github.com/lima-vm/lima/issues/2789), open) | no |
| Lima with its krunkit driver | yes | yes, through libkrun (lima #4220) | no |
| Tart (VZ) | source-available | no balloon in its code | no |
| UTM, Apple backend | yes | attaches a balloon, never drives it | no |
| **Podman Machine, libkrun provider** | **yes** | **yes, through libkrun (INFERRED for podman's build)** | **yes, today** |
| OrbStack | no ([FAQ](https://docs.orbstack.dev/faq)) | yes, by an undisclosed mechanism since 1.7.0 ([blog](https://orbstack.dev/blog/dynamic-memory)) | no; Docker-compatible only, and yolo removed Docker |
| Docker Desktop | no | stops the VM when idle ([Resource Saver](https://docs.docker.com/desktop/use-desktop/resource-saver/)); Docker VMM returns idle memory ([docs](https://docs.docker.com/desktop/features/vmm/)) | no; Docker was removed |

Firecracker, the microVM that prompted the original question, is Linux and KVM only and does not
run on macOS (SOURCED: [the benchmark's sources](macos-backend-performance.md#sources)).

**libkrun is the one open implementation that does it.** libkrun 1.19.6 attaches a balloon to
every VM except confidential-computing builds and offers stats, free page hinting and free page
reporting; on macOS it frees each reported page with `MADV_FREE` (SOURCED: libkrun's
`builder.rs` and `balloon/device.rs`; [libkrun #703](https://github.com/containers/libkrun/pull/703),
merged 2026-06-03). That PR's author saw host memory pressure drop "right away", and lima #4220's
run saw memory return with krunkit 1.3.2 on libkrun 1.19.4. ⚠ Because of `MADV_FREE`, Activity
Monitor and footprint do not show it; host memory pressure does (SOURCED). The benchmark's harness
reads footprint, so it would report a libkrun VM as holding memory that macOS can in fact take.

**Podman Machine's default provider on macOS is libkrun**, with `applehv` (VZ) as the alternative
(SOURCED: `podman-machine(1)`), and yolo already runs on a Podman Machine
([getting started](../../userguide/getting-started.md)). So reclaim may already work there.
INFERRED, and two things are unchecked: whether the Fedora CoreOS guest kernel enables
`CONFIG_PAGE_REPORTING`, and whether the krunkit podman bundles has libkrun 1.19 or later. One
difference from Apple Container: every Podman Machine container shares one VM, so the VM's memory
is shared by every jail rather than kept per jail.

## 5. Adding it to Apple Container ourselves

- **It is small.** About 20 lines to attach the device in containerization's
  `VZVirtualMachineInstance.swift`, about 50 for a setter and its protocol, and about 370 for a
  policy loop that reads guest memory through `vminitd`, all already written in the closed PRs
  (SOURCED).
- **It can be done as a fork.** Apache-2.0 allows it, but `container` would have to be rebuilt
  against the fork, signed and shipped, which yolo does not do today. Upstreaming would start
  with a design issue, given how #882 went.
- **Its costs** (INFERRED): a traditional balloon gives memory back to the guest only when the host
  raises the target, so a lagging loop can push the guest into its OOM killer; `DEFLATE_ON_OOM`
  softens that. Compacting the guest costs CPU.
- **It would probably gain nothing.** The one host-side measurement ([§3](#3-what-virtualizationframework-offers)) shows VZ keeping the
  pages. Before writing a patch, reproduce that with incompressible data (from `/dev/urandom`) and
  read host memory pressure and the compressor, not footprint.
- **What would actually help is a free page reporting device in VZ**, which only Apple can add.
  Asking through Feedback Assistant, with lima #4220's measurement, is the cheap step. WWDC26
  session 224's custom Virtio devices for apps (macOS 27) might allow a third party to supply one;
  not checked.

Until then the levers on Apple Container are the ones the benchmark already names: stop jails
sooner, and lower the memory cap yolo passes (half of host RAM, at least 4 GB).

## Sources

- [apple/containerization #882](https://github.com/apple/containerization/issues/882),
  [#893](https://github.com/apple/containerization/pull/893),
  [#894](https://github.com/apple/containerization/pull/894) — the proposal and its closure.
- [VZ's traditional balloon](https://developer.apple.com/documentation/virtualization/vzvirtiotraditionalmemoryballoondevice) — the only reclaim device VZ offers.
- [lima-vm/lima #4220](https://github.com/lima-vm/lima/issues/4220) — the VZ balloon measured on
  macOS 26.5.2, and krunkit returning memory.
- [containers/libkrun #703](https://github.com/containers/libkrun/pull/703) — free page reporting
  freed with `MADV_FREE` on macOS.
- [OrbStack on dynamic memory](https://orbstack.dev/blog/dynamic-memory) and
  [its author on VZ](https://news.ycombinator.com/item?id=34720219).
- [Docker Desktop Resource Saver](https://docs.docker.com/desktop/use-desktop/resource-saver/) and
  [Docker VMM](https://docs.docker.com/desktop/features/vmm/).
