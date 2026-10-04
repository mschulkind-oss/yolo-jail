---
title: "Is macos-user faster than Apple Container? What the sources say, and a benchmark to find out"
date: 2026-10-01
status: in-review
stage: DESIGN
next: "The maintainer rules on OQ-MB1, what backs an Apple Container jail's /mise so that two jails can run at once; a Mac session reruns §7's two-jail check on container 1.5.0; a Mac launch pass confirms that auto-capture now stores claude, codex and agy (fixed from the code 2026-10-03, OQ-PD24 to OQ-PD26); macos-user's go_test (M8) is re-run now that the harness trusts the clone's mise.toml; the Results section's other defects are filed and the Corrections section's edits made"
tags: [research, macos, apple-container, macos-user, performance, memory, benchmark, virtiofs]
summary: "The maintainer asked for a benchmark instead of an assumption: is macos-user really faster than Apple Container? Sources answer part of it. Apple Container gives each container its own small VM; the VM takes RAM only as the guest touches it, but keeps every page it touched until the container stops, so the maintainer's reading is half right. CPU work should run within a few percent of native, while file work in the shared workspace is where the VM probably costs most: about 2.7 times native in one published measurement of the same macOS file sharing, and 6 to 9 times by Apple's maintainer's rough figures for builds. The doc lists every claim the repo makes about the two backends' speed and memory, a protocol for one Mac running both against one workspace, and a POSIX sh harness that runs the protocol and writes the results table. It measures; it does not choose a backend."
vantage:
  status-chip: true
---

# Is macos-user faster than Apple Container? What the sources say, and a benchmark to find out

**Status:** 2026-10-01; research and a benchmark protocol, nothing ruled here. MEASURED on one
Mac: Apple Container and the native control on 2026-10-02, macos-user and the native control on
2026-10-03, and the two-jail volume check ([§8](#8-results),
[§7](#7-found-on-the-way-two-apple-container-jails-may-mount-one-ext4-disk)). The two backends were
measured on different days, so each is compared with its own day's native run. Two follow-ups
grew out of the results:
[why Apple Container's file work is slow](apple-container-file-cost.md) (per-file cost, not
bandwidth, and what that means for a large Python monorepo),
[whether any macOS VM can give memory back](macos-vm-memory-reclaim.md), and
[a Python, Django and Postgres workload on three macOS VMs](macos-vm-runtime-comparison.md). CI logs hold launch times for each backend, but they come from two different Macs. The
upstream source was read on 2026-09-30, at apple/container `0a48a1bd` (one day after release 1.5.0)
and apple/containerization `f24df2ac` (1.5.0 is built on its tag 0.47.0). yolo evidence is at
`a5665814`.

**The question,** in the maintainer's words (2026-09-30): *"we need to benchmark if there is
actually a performance advantage to Mac OS user performance rather than just assuming it. Even
though we will continue with both implementations, I think we need to know this."* The prompt was
reading that Apple Container "dynamically allocates RAM and will return it to the operating
system", which would make it close to an ordinary container. That reading contradicts
[the macOS direction](../reference/macos-no-vm-direction.md), whose premise is that a Mac's
container VM holds its RAM for as long as it runs
([direction:32-37](../reference/macos-no-vm-direction.md#L32-L37)) and whose warning says it
reserves that RAM up front ([direction:342-344](../reference/macos-no-vm-direction.md#L342-L344)).

> **In short.**
>
> - **Memory: half right.** Apple Container's VM takes RAM only as the guest touches it, so
>   nothing is reserved up front. But it gives nothing back while the container runs: every page
>   the guest ever touched stays with the VM until the container stops
>   ([§2.2](#22-memory-backed-on-first-touch-kept-until-the-container-stops)).
> - **CPU: probably a wash.** Native arm64, no emulation, within a few percent of other
>   runtimes in published runs. yolo caps an Apple Container jail at half the cores, which is a
>   setting, not overhead ([§2.4](#24-cpu)).
> - **Files: probably macos-user's real advantage.** Apple Container shares the workspace, the
>   jail's home and its caches with the Mac through virtiofs. One published measurement of that
>   same macOS mechanism put an `npm install` at about 2.7 times native, and Apple's maintainer's
>   rough figures put builds at 6 to 9 times ([§2.3](#23-shared-folders-virtiofs)).
> - **Launch: mostly yolo's own work, not the VM.** A VM boots in under a second; a yolo launch
>   on Apple Container took about 9 s in CI ([§2.1](#21-one-vm-per-container-and-its-boot),
>   [§2.6](#26-what-ci-logs-already-hold)).
> - **None of this has been compared on one Mac.** [§4](#4-the-protocol) is the protocol and
>   [Appendix A](#appendix-a--the-harness) the harness that settles it.

---

## Terms

- **Apple Container** — Apple's `container` command-line tool and the
  [Containerization](https://github.com/apple/containerization) library under it, which runs
  Linux containers on a Mac. yolo's runtime name for it is `container`. Not Podman Machine, the
  other Mac container setup, which runs every container in one shared VM.
- **macos-user** — yolo's backend with no VM: the agent runs as a hidden macOS account,
  `_yolojail`, inside Apple's built-in sandbox, Seatbelt
  ([user guide](../../userguide/guides/macos.md#the-macos-user-backend)).
- **Virtualization framework (VZ)** — Apple's API for running virtual machines on macOS
  ([Apple's documentation](https://developer.apple.com/documentation/virtualization)). Apple
  Container is built on it.
- **virtiofs** — the Linux driver for a paravirtualized device "for guest<->host file system
  sharing" ([kernel documentation](https://docs.kernel.org/filesystems/virtiofs.html)). Apple
  Container uses it for every host folder it shares with a container. Not a disk image: a disk
  image (virtio-blk, [§2.3](#23-shared-folders-virtiofs)) is a file the guest formats and owns.
- **Memory balloon** — a virtual device through which the host asks a guest to hand pages back
  ([VZ's traditional balloon](https://developer.apple.com/documentation/virtualization/vzvirtiotraditionalmemoryballoondevice)).
  The host has to ask; the guest does not volunteer.
- **Free page reporting** — the Linux mechanism by which a guest tells the hypervisor, unasked,
  which pages it no longer uses ([kernel documentation](https://docs.kernel.org/mm/free_page_reporting.html)).
  Not ballooning, which the host drives.
- **Resident size (RSS)** — the pages of a process that are in RAM right now. **Footprint** —
  Apple's measure of the memory charged to a process: its dirty pages plus its compressed ones,
  with clean pages, which the system can reload from disk, left out ([WWDC18 session 416,
  "iOS Memory Deep Dive"](https://developer.apple.com/videos/play/wwdc2018/416/); macOS shares
  that memory system). The two differ exactly when macOS has compressed a process's cold pages,
  which is why the harness records both.
- **XProtect** — macOS's built-in malware scanner, which "scans only apps that have been changed
  or apps at first launch" ([Apple Platform Security](https://support.apple.com/guide/security/protecting-against-malware-sec469d47bd8/web)).
- **The darwin floor** — yolo's: the native macOS build of the jail's core tool list that a
  macos-user launch builds with nix ([provisioning reference](../reference/macos-user-provisioning.md#the-floor)).
- **Fresh launch** and **attach** — yolo's: a `yolo` that starts the workspace's jail because
  none is running, and a `yolo` that enters a jail already running
  ([the keeper design](../design/jail-lifetime-last-session-wins.md#94-how-re-entering-works-and-changing-agents)).
  macos-user has no attach: every launch there is a fresh sandbox.
- **Holding session** *(coined here)* — a session the harness keeps open in an Apple Container
  jail so that the attaches it times have a running jail to enter.
- **Warm-up run** *(coined here)* — the untimed first run of each timed metric, which fills
  caches and is reported separately.
- **Native control** *(coined here)* — the same workload run directly on the Mac as the invoking
  user, with macos-user's own darwin floor on `PATH` and a scratch home. It is a control, not a
  third backend: macos-user minus native is the cost of the sandbox account and Seatbelt, and
  Apple Container minus native is the cost of the VM and its shared folders.
- **Evidence labels.** **SOURCED**: read in a named source (vendor documentation, upstream code
  at a named commit, a third party's published numbers). **MEASURED**: a command run for this
  doc, or a CI log read for it, with the result stated. **INFERRED**: reasoned from the above
  and not observed.

---

## 1. Claims under test

Every claim the repo makes about how fast, or how memory-hungry, the two macOS backends are. None
of them cites a measurement on one Mac.

| # | Claim | Where | What the research says |
| :--- | :--- | :--- | :--- |
| C1 | macos-user is "the fast native" path and "starts fastest" | [direction:18-19](../reference/macos-no-vm-direction.md#L18-L19), [direction:263](../reference/macos-no-vm-direction.md#L263), [macos.md:23](../../userguide/guides/macos.md#L23), [macos.md:223](../../userguide/guides/macos.md#L223), [confinement.md:36](../../userguide/guides/confinement.md#L36) | **Unsettled.** CI has an Apple Container launch at 8.9–9.1 s and a warm macos-user launch at 2.2–4.6 s, but on two different Macs (MEASURED, [§2.6](#26-what-ci-logs-already-hold)). The VM boot is under 1 s of that (SOURCED), and both backends run a nix build at every launch (SOURCED by code: [stockimage.go:356-364](../../internal/image/stockimage.go#L356-L364), [orchestrator.go:472](../../internal/macosuser/orchestrator.go#L472)). |
| C2 | A Mac container VM is "slow to start" and "still boots slowly" | [direction:32-34](../reference/macos-no-vm-direction.md#L32-L34), [direction:342](../reference/macos-no-vm-direction.md#L342) | **Partly.** Apple claims sub-second starts; others measured 0.70–0.94 s per `container run`, against 0.19–0.30 s for runtimes that reuse a VM already running (SOURCED). yolo pays it once per fresh launch, not per attach (INFERRED). |
| C3 | "a RAM ceiling you have to guess ahead of time" | [direction:33-34](../reference/macos-no-vm-direction.md#L33-L34) | **Half.** The ceiling is real and fixed at boot (SOURCED). yolo picks it, not the user: half of host RAM, at least 4 GB, and half the cores, at least 2 (SOURCED by code: [helpers.go:172-199](../../internal/cli/run/helpers.go#L172-L199), [backendcaps.go:279-293](../../internal/cli/run/backendcaps.go#L279-L293)). |
| C4 | The VM "still reserves RAM up front" | [direction:343](../reference/macos-no-vm-direction.md#L343) | **Contradicted.** VZ reserves address space "but doesn't allocate immediately"; pages are backed on first touch (SOURCED). |
| C5 | "that RAM permanently held while it runs"; "still holds it" | [direction:34](../reference/macos-no-vm-direction.md#L34), [direction:343](../reference/macos-no-vm-direction.md#L343) | **Supported.** Freed guest pages are "not relinquished to the host" (SOURCED), and Containerization attaches no balloon (MEASURED). All of it returns when the container stops (SOURCED). |
| C6 | The maintainer's reading: Apple Container "dynamically allocates RAM and will return it to the operating system" | the request quoted above | **Half right:** allocation on demand, yes (C4); return only when the container stops (C5). |
| C7 | "a filesystem boundary over the workspace" | [direction:35](../reference/macos-no-vm-direction.md#L35), [direction:343-344](../reference/macos-no-vm-direction.md#L343-L344) | **Supported, and probably the largest cost.** Every shared folder is virtiofs through one VZ device (MEASURED, by reading the source); yolo shares the workspace, the home and the cache that way (SOURCED by code); the same macOS mechanism measured about 2.7 times native for an `npm install` (SOURCED). |
| C8 | "the VM overhead is bad enough that a reasonable person reaches for a different tool" | [direction:354-357](../reference/macos-no-vm-direction.md#L354-L357) | **No source settles it.** The benchmark measures its parts. |
| C9 | Mac container startup is "~2-3s" | [platform-comparison.md:263](platform-comparison.md#L263), [sandbox-comparison.md:437](sandbox-comparison.md#L437) | **Contradicted for a yolo launch:** about 9 s on Apple Container in CI (MEASURED), of which the VM is under 1 s (SOURCED). The first doc is marked partly stale. |
| C10 | virtiofs is "near-native"; CPU work is "~95-98% native" | [platform-comparison.md:264](platform-comparison.md#L264), [platform-comparison.md:266](platform-comparison.md#L266), [platform-comparison.md:270](platform-comparison.md#L270) | **Files: contradicted** for metadata-heavy work (SOURCED). **CPU: plausible**, within a few percent of other runtimes, with no native baseline published (SOURCED). |
| C11 | Parallel jails are bounded by memory, and Apple Container's per-VM footprint "is the real cap" | [integration-parallelism.md:82](../plans/integration-parallelism.md#L82), [integration-parallelism.md:108](../plans/integration-parallelism.md#L108) | **Consistent with C5** (INFERRED); never measured. |
| C12 | On Apple Container the scratch folders "are always held in memory" and count against the cap | [macos.md:109-110](../../userguide/guides/macos.md#L109-L110) | **SOURCED by code** ([assemble_parts.go:64-71](../../internal/cli/run/assemble_parts.go#L64-L71)). INFERRED from C5: a deleted temporary file's pages stay with the VM until it stops. |
| C13 | macos-user has "No resource limits. Nothing caps memory or CPU" | [macos.md:232](../../userguide/guides/macos.md#L232) | **SOURCED by code.** INFERRED: memory a sandboxed process frees goes back to macOS at once, as for any process. |
| C14 | "AC's non-reclaiming memory balloon: accepted" | [macos-revival-and-distribution-plan.md:1272](../plans/macos-revival-and-distribution-plan.md#L1272) | **Consistent with C5, with one correction:** there is no balloon device at all (MEASURED). |

---

## 2. What the sources say

### 2.1 One VM per container, and its boot

- **Each container gets its own lightweight VM on VZ** (SOURCED: the
  [Containerization README](https://github.com/apple/containerization); WWDC25 session 346,
  ["each container inside of its own lightweight virtual machine while still providing sub-second start times"](https://developer.apple.com/videos/play/wwdc2025/346/)).
  Inside it, a small init written in Swift, `vminitd`, takes orders over vsock.
- **The default kernel since 1.3.0 is Kata Containers 3.32.0's `vmlinux-6.18.35-197-debug`**
  (SOURCED: [ContainerSystemConfig.swift:168-170](https://github.com/apple/container/blob/0a48a1bdbfaa7451c810372d98b045fa8b486b6a/Sources/ContainerPersistence/ContainerSystemConfig.swift#L168-L170),
  PR [#2143](https://github.com/apple/container/pull/2143)). The `debug` build adds eBPF,
  kprobes, ftrace and BTF (SOURCED: the PR); what that costs at run time is unmeasured.
- **Apple claims sub-second starts** (WWDC25, and WWDC26 session 389), and a shell "within a few
  hundred milliseconds" in the WWDC25 demo (SOURCED).
- **Others measured 0.70–0.94 s per `container run`** (SOURCED):

  | Version and Mac | `container run` | Compared with | Source |
  | :--- | :--- | :--- | :--- |
  | 0.1.0, M3 Pro | 0.929 s unsigned build, 0.698 s signed | Docker 0.299 s | [issue #58](https://github.com/apple/container/issues/58) |
  | 0.1.0, M4 Air | 0.733 s | — | [Madhavapeddy](https://anil.recoil.org/notes/apple-containerisation) |
  | about 0.5, October 2025 | 0.800 s | OrbStack 0.203 s | [issue #738](https://github.com/apple/container/issues/738) |
  | 0.6.0, M4 mini, 20 runs | 0.940 s | Docker 0.187 s, OrbStack 0.228 s | [RepoFlow](https://www.repoflow.io/blog/apple-containers-vs-docker-desktop-vs-orbstack) |
  | 0.11.0, M3, macOS 26.4.1 | 0.935 s | Colima 0.291 s | [zot24](https://github.com/zot24/macos-container-benchmarks) |

- **Why it is slower than Docker or OrbStack:** `container` boots the VM and the workload
  together, while the others reuse a VM that is already running. A pool of pre-booted VMs is not
  possible today, because CPU, memory and network are fixed at boot and cannot be hot-plugged
  (SOURCED: the maintainer in issues #58 and #738; the pool request,
  [#1924](https://github.com/apple/container/issues/1924), is open). No published number covers
  any 1.x release.
- **For yolo the boot is paid once per fresh launch.** A jail is one long-lived container, and
  an attach execs into it (INFERRED from [the keeper design](../design/jail-lifetime-last-session-wins.md#9-the-keeper-design-2026-09-29)).

### 2.2 Memory: backed on first touch, kept until the container stops

1. **Reserved, not allocated.** VZ's guest RAM "is a contiguous block of virtual memory that the
   host system reserves, but doesn't allocate immediately" (SOURCED:
   [`memorySize`](https://developer.apple.com/documentation/virtualization/vzvirtualmachineconfiguration/memorysize),
   read 2026-10-01).
2. **Backed on first touch.** Apple's example: `--memory 16g`, and about 2 GiB in Activity
   Monitor (SOURCED: [technical overview, "Releasing container memory to macOS"](https://github.com/apple/container/blob/0a48a1bdbfaa7451c810372d98b045fa8b486b6a/docs/technical-overview.md#releasing-container-memory-to-macos);
   the same text ships in 0.1.0 and 1.5.0).
3. **Never given back while it runs.** Same section: "memory pages freed to the Linux operating
   system by processes running in the container's VM are not relinquished to the host … you may
   need to occasionally restart them". It blames VZ's "partial support for memory ballooning".
4. **Containerization attaches no balloon.** MEASURED:
   `git -C containerization grep -i balloon 0.47.0 -- Sources vminitd` exits 1, and `rg -i balloon`
   finds nothing in either repository's sources. VZ does offer a traditional balloon, which the
   host has to drive. One outside contributor proposed attaching it: an RFC issue,
   containerization [#882](https://github.com/apple/containerization/issues/882), and two PRs,
   [#893](https://github.com/apple/containerization/pull/893) (the device) and
   [#894](https://github.com/apple/containerization/pull/894) (an optional reclaim loop). A
   maintainer closed all three on 2026-08-28 with no comment, **in one sweep with the same
   contributor's other 30 or so PRs of 2026-08-27**, so the closure is not a ruling on ballooning
   (SOURCED, through the GitHub API's issue events). And driving VZ's balloon may not return
   memory to macOS at all: the one published host-side measurement saw the guest give up 7 GiB and
   the host's footprint rise
   ([memory-reclaim research](macos-vm-memory-reclaim.md#3-what-virtualizationframework-offers)).
   Apple's guest kernel config does enable `CONFIG_VIRTIO_BALLOON` and `CONFIG_PAGE_REPORTING`
   ([config-arm64:920](https://github.com/apple/containerization/blob/f24df2ac817df66fe149a80103251dec987c32dc/kernel/config-arm64#L920),
   [config-arm64:3047](https://github.com/apple/containerization/blob/f24df2ac817df66fe149a80103251dec987c32dc/kernel/config-arm64#L3047));
   the host simply never offers either device. VZ lists no free page reporting device, and
   OrbStack's author wrote in 2023 that VZ ["doesn't support page reporting"](https://news.ycombinator.com/item?id=34720219) (SOURCED).
5. **Everything returns when the container stops:** "If no containers are running, no resources
   will be allocated" (SOURCED: WWDC25).
6. **Under pressure macOS can compress or swap the VM's cold pages**, which pages them out rather
   than giving them back (INFERRED; the contributor states it in #882).

What that means for a yolo jail (all INFERRED):

- **The guest's file cache and its tmpfs count as touched memory.** Linux fills spare RAM with
  file cache, so a session heavy on file work pushes the VM towards its `--memory` ceiling and
  keeps it there.
- **yolo puts every scratch folder of an Apple Container jail in tmpfs**
  ([assemble_parts.go:64-71](../../internal/cli/run/assemble_parts.go#L64-L71)), so a temporary
  file deleted an hour ago still has its pages in the VM.
- **The ceiling is half of host RAM.** `container`'s own default is 1 GiB and 4 CPUs
  ([ContainerSystemConfig.swift:116-117](https://github.com/apple/container/blob/0a48a1bdbfaa7451c810372d98b045fa8b486b6a/Sources/ContainerPersistence/ContainerSystemConfig.swift#L116-L117)),
  but yolo passes half of host RAM, at least 4 GB. On a 32 GB Mac each jail can grow towards
  16 GB and keep it until it stops.

### 2.3 Shared folders: virtiofs

- **Every host folder goes through one virtiofs device**, a `VZMultipleDirectoryShare` served by
  VZ itself (MEASURED, by reading
  [VZVirtualMachineInstance.swift:494-514](https://github.com/apple/containerization/blob/f24df2ac817df66fe149a80103251dec987c32dc/Sources/Containerization/VZVirtualMachineInstance.swift#L494-L514)).
- **yolo shares the workspace, the jail's home and its cache that way**
  ([assemble_parts.go:59-62](../../internal/cli/run/assemble_parts.go#L59-L62)). Only `/mise` is a
  named volume, `yolo-mise-data-v2` ([assemble.go:25](../../internal/cli/run/assemble.go#L25)).
- **The root disk and named volumes are ext4 images over virtio-blk**, cached and fsynced by
  default ([Mount.swift:322-323](https://github.com/apple/containerization/blob/f24df2ac817df66fe149a80103251dec987c32dc/Sources/Containerization/Mount.swift#L322-L323)).
  Since 0.8.0 the root disk's cached mode puts its block I/O level with Docker Desktop's
  (SOURCED: PR [#1041](https://github.com/apple/container/pull/1041)).
- **Apple's maintainer on builds** (SOURCED, informal: "It's been a while since I've
  benchmarked", [discussion #1516](https://github.com/apple/container/discussions/1516),
  2026-05-15): virtiofs "is pretty slow for heavy I/O workloads like builds where writes are
  relatively small and there's a fair number of metadata writes"; a native clean build is about
  2–3 times faster than one on virtio-blk, which is about 3 times faster than one on virtiofs.
  Together that puts virtiofs at about 6–9 times native for builds (INFERRED).
- **The same VZ virtiofs against native APFS** (SOURCED: Mainardi, January 2025, M4 Pro,
  `npm install` of a create-react-app project, [post](https://www.paolomainardi.com/posts/docker-performance-macos-2025)):

  | Setup | Native | The VM's own disk | virtiofs share |
  | :--- | :--- | :--- | :--- |
  | Lima (VZ and virtiofs) | 3.38 s | 3.70 s | 8.99 s |
  | Docker on VZ | 3.37 s | 3.75 s | 9.53 s |

  The share is about 2.7–2.8 times native; the VM's own disk about 1.1 times.
- **Apple Container's own file numbers have no native baseline.** RepoFlow, 0.6.0: small-file
  stat 4.0 s, read 1.8 s, create 5.2 s, copy 4.0 s, against Docker's 5.4, 1.4, 6.2, 3.0 s and
  OrbStack's 4.2, 0.8, 3.8, 1.0 s. zot24, 0.11.0: 1,000 writes of 4 KB in 1.257 s, against
  0.62–0.76 s for the others, but that includes about 0.8 s of VM boot (SOURCED; the split is
  INFERRED from its `benchmark.sh`).
- **macOS 26 roughly doubled volume write throughput** for runtimes built on VZ (SOURCED: zot24,
  macOS 15 against 26).

### 2.4 CPU

- **Native arm64, no emulation, and small overhead** (SOURCED: RepoFlow, 0.6.0, sysbench events
  per second; no native macOS baseline was run):

  | Runtime | One thread | All threads | Memory (MiB/s) |
  | :--- | ---: | ---: | ---: |
  | Apple Container | 11,090 | 54,718 | 103,288 |
  | Docker Desktop | 10,506 | 53,302 | 77,506 |
  | OrbStack | 11,047 | 55,135 | 90,177 |

- **Rosetta does not apply to yolo.** `container` turns it on for an amd64 image on an arm64 Mac,
  or with `--rosetta` ([Utility.swift:231](https://github.com/apple/container/blob/0a48a1bdbfaa7451c810372d98b045fa8b486b6a/Sources/Services/ContainerAPIService/Client/Utility.swift#L231)),
  and Apple says the Rosetta features "have an impact on the virtual machine execution of memory
  operations" even for native code (SOURCED: quoted in
  [lima-vm/lima #1269](https://github.com/lima-vm/lima/issues/1269); Geekbench single-core fell
  from 1672 to 1225). yolo never passes `--rosetta` and its image is arm64 (MEASURED:
  `rg -i -l rosetta internal -g '!*_test.go'` finds nothing).
- **yolo caps an Apple Container jail at half the cores** (C3). Any parallel work gets half the
  Mac there and all of it on macos-user. That is a default, not a VM cost, so
  [§4.2](#42-the-metrics) pins thread counts where it matters.

### 2.5 Process start, new binaries, networking

- **Nothing published measures process start inside an Apple Container guest.** INFERRED:
  fork and exec there are ordinary Linux, plus the page-fault cost of the VM's second level of
  address translation; a `container exec` adds a round trip from the CLI through
  `container-runtime-linux` to `vminitd` over vsock.
- **Native macOS scans every new executable on its first run** (SOURCED: Nethercote, September
  2025, [post](https://nnethercote.github.io/2025/09/04/faster-rust-builds-on-mac.html)). The
  rustc `tests/ui` suite went from 9 min 42 s to 3 min 33 s once the terminal was marked a
  developer tool, and Cargo build scripts from 0.48–3.88 s each to 0.06–0.14 s. A Linux guest
  does not pay this. Whether a macos-user jail does depends on which app macOS holds responsible
  for the sandboxed process (INFERRED, untested).
- **No credible measurement of Seatbelt's own cost per check exists.** Bazel's 2018 numbers for
  its macOS sandbox mostly measure its symlink-forest setup (SOURCED:
  [Bazel blog](https://blog.bazel.build/2018/04/13/preliminary-sandboxfs-support.html)).
- **Older macOS-against-Linux data, all weak predictors** (Intel, old macOS, different machines;
  SOURCED): walking a 250,000-file tree took about 38–44 s on macOS 10.13 and 10.14 against about
  4.2 s on Linux ext4, from a global lock in APFS
  ([Szorc, 2018](https://gregoryszorc.com/blog/2018/10/29/global-kernel-locks-in-apfs/)); fork
  and exec were about 10 times faster on Linux than on macOS 10.12
  ([bitsnbites, 2017](https://www.bitsnbites.eu/benchmarking-os-primitives/)).
- **Networking** (SOURCED: zot24, 0.11.0 on macOS 26.4.1): an HTTP fetch takes about the same
  time on every runtime (0.771 s against 0.716–0.830 s); container-to-container iperf3 is 23 Gbps
  against 110–130 Gbps for runtimes sharing one VM; a port-mapped nginx's time to first byte is
  3.24 ms against 1.56–2.69 ms. The protocol does not measure networking
  ([§6](#6-what-the-harness-cannot-measure)).
- **Firecracker, which prompted the question, cannot run on a Mac.** It needs Linux and KVM
  (SOURCED: [Firecracker](https://github.com/firecracker-microvm/firecracker),
  `SPECIFICATION.md`). Its published bounds are a useful contrast: at most 125 ms to the guest's
  init, at most 5 MiB for the VM manager, and a balloon with free page reporting.

### 2.6 What CI logs already hold

MEASURED from the logs of the named runs (`gh run view <id> --log`), read 2026-09-30. The two
backends ran on **different machines**, so these are not a comparison.

- **Apple Container**, on the maintainer's self-hosted Mac (macOS 26.5, `container` 1.1.0,
  arm64), runs 36350256008, 36370612280 and 36378256230:
  - one full `yolo run --accept-config-changes -- bash -lc '<probe>'` with the claude pack:
    `TestAppleContainerJailStarts` 9.06, 9.13 and 9.06 s;
  - a bare `container run --rm <ref> sh -c …` with no yolo involved:
    `TestAppleContainerHonorsReadOnlyBinds` 1.66, 1.55 and 1.54 s;
  - the image delivery table of `TestMacArchiveDeliveryReusesLayers`: the `image.nix_build` span
    took 3.2–4.4 s per launch. Apple Container gets no stock image tag, so it runs that build at
    every launch ([stockimage.go:356-364](../../internal/image/stockimage.go#L356-L364)).
- **macos-user**, on GitHub-hosted `macos-latest` (macOS 26.6.2, arm64):
  - a cold first launch that built the floor from the binary cache:
    `TestMacosUserFloorReachesTheSandboxPath` 73.61 s (run 36134719833);
  - warm single launches: the same test at 3.22–3.58 s, `TestMacosUserFooterSaysJail` 2.35–3.87 s
    (runs 36437881715 and 36719581090, among others);
  - the provisioning test's own split, in whole seconds: launch total 4–7 s, of which 2–4 s comes
    before the provisioning stage (the nix floor, staging and bootstrap).
- **Neither backend has a memory measurement anywhere** in the repository or in CI.

INFERRED from those: Apple Container's 9 s is mostly yolo's own work (the nix build, staging,
the boot steps), not the VM's sub-second boot. macos-user also runs a nix build at every
launch, so the two backends' fixed per-launch costs are closer than "VM against no VM" suggests.

### 2.7 How yolo's two backends map onto these mechanisms

| Aspect | Apple Container | macos-user |
| :--- | :--- | :--- |
| Per-launch host work | an image identity eval and a nix build at every launch (no stock tag), staging, the VM's boot, then the entrypoint's boot steps | a nix eval and a nix build of the darwin floor, a series of root `sudo` staging steps, the bootstrap, then `sandbox-exec` |
| Second session | an attach: `container exec` into the running VM | none: every `yolo` is a fresh sandbox |
| Workspace, home, caches | virtiofs shares of host folders | the Mac's own APFS, read and written natively |
| Scratch folders | tmpfs, in the VM's RAM | on the Mac's own disk |
| Memory | capped at half of host RAM; touched pages kept until stop | no cap; ordinary macOS processes |
| CPU | capped at half the cores | every core |
| Tools | one list by name ([coreFloorNames in flake.nix](../../flake.nix#L1246-L1313)), built for Linux and baked into the image | the same list less the names with no darwin build, built for darwin |

Sources for the macos-user column: [`internal/macosuser`](../../internal/macosuser) and
[`internal/darwinpkg`](../../internal/darwinpkg) (SOURCED by code, at `a5665814`).

---

## 3. Predictions the benchmark tests

All INFERRED from [§2](#2-what-the-sources-say). Each names the metric of
[§4.2](#42-the-metrics) that settles it.

| Prediction | Metric |
| :--- | :--- |
| A fresh launch is a few seconds slower on Apple Container, mostly not because of the VM | M1, M2, and the spans `YOLO_TIMING=1` records |
| An attach on Apple Container is faster than any macos-user launch, which has no attach | M3 against M1 |
| Apple Container's VM keeps the anonymous load, the scratch load and the file-cache load after each ends; macos-user returns all three at once | M4 |
| Metadata-heavy work in the workspace is 2–9 times slower on Apple Container | M5, M6, M7, M8 |
| One-thread CPU work is within a few percent; a parallel build is slower on Apple Container by its core cap, and within a few percent once pinned | M9, M10 |
| A process-spawn loop is no slower in the Linux guest, and maybe faster | M11 |
| The first run of a newly built binary costs more on macOS (macos-user and native) than in the Linux guest | M12 |
| Apple Container's disk footprint is several GB of image and snapshot; macos-user's is its floor's closure in the nix store | M13 |

---

## 4. The protocol

### 4.1 Ground rules

- **One Apple silicon Mac on macOS 26 or later**, with Apple Container installed and
  `container system start` done, and `yolo macos-setup` done for macos-user. On mains power,
  with Low Power Mode off and other heavy applications closed. The harness records the model,
  the core counts, the macOS, `container` and yolo versions, and the power state.
- **No other jail running.** The harness refuses to start while an Apple Container jail is up
  or a process runs as `_yolojail`, because the waits and the memory readings must be its own,
  and while a self-hosted Actions runner is loaded, because a CI job would launch jails mid-run.
- **One workspace for both backends,** `/Users/Shared/yolo/bench-macos-backends`. macos-user
  only accepts projects under `/Users/Shared/yolo`, and Apple Container accepts any folder. Both
  backends read the same files, the same workspace config and the same user config; the harness
  saves `yolo config dump` beside its results.
- **The same tools by name.** Both backends get git, ripgrep, node 24, go and bash from one list
  ([coreFloorNames](../../flake.nix#L1246-L1313)), resolved for Linux in the image and for
  darwin in the floor, from one locked nixpkgs (SOURCED by code). The native control uses the
  floor's copies, so its tools are byte for byte macos-user's.
- **No agent sessions and no model calls.** Every command the jails run is the harness's own.
  A first launch on Apple Container may still capture an installer for a selected pack; that is
  an install, it lands in the warm-up launch, and it is reported there.
- **Backends one after another, phase by phase,** in the order container, macos-user, native.
  If a verdict is close to the threshold, run the harness again with
  `BENCH_BACKENDS="native macos-user container"` and report both.
- **Every run's output is kept** under the results folder, so any figure can be traced to its
  log.

### 4.2 The metrics

`JOBS` is the Apple Container jail's CPU count, read inside that jail at its warm-up launch. The
in-jail clock is bash's `time` keyword at millisecond resolution; the host clock is a small node
helper that timestamps the child's output.

| ID | Metric | What runs | Measured by | Runs | Caches | Worth reporting |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| M1 | Fresh launch to a prompt | `YOLO_TIMING=1 yolo run --accept-config-changes -- bash -c 'echo __BENCH_""READY__'` with no jail of the workspace running | host: ms from spawn until the command prints the marker; the empty quotes keep it out of the `Executing:` line an Apple Container session prints first, which repeats the command | 5, after one warm-up launch | one-time work (image, floor, captures) done by the warm-up; each fresh launch boots its own VM by definition | the rule in [§4.4](#44-what-counts-as-a-difference) |
| M2 | Fresh launch, end to end | the same run, to exit: `yolo -- true` plus one `echo` | host: ms until `yolo` exits; on Apple Container that includes the jail's teardown, which the last session waits for | same | same | same |
| M3 | Attach | the same command while a holding session keeps the jail up; Apple Container only | host: as M1 and M2 | 10 | warm | same |
| M4 | Memory: idle, under load, after the load | one session: 30 s idle, then three loads of `LOAD_MB` each (anonymous memory held by a child process, a scratch file, one read of a workspace file), each sampled while held and 0, 10 and 120 s after it ends, then a drop-caches step, then exit | host only: `vm_stat`; `sysctl kern.memorystatus_level`, INFERRED to be the free percentage `memory_pressure` prints; and the RSS (`ps`) and `top`'s MEM column, INFERRED to be the footprint, of the VZ process (Apple Container) or of the `_yolojail` processes (macos-user) | one session per backend | not applicable | "held" if at least 50% of the load is still charged 120 s after it ended, "returned" if at most 10% |
| M5 | `git status`, large repository | `git status --porcelain` in a 100,000-file synthetic repository in the workspace | in-jail `time` | 5, after one warm-up run | warm; the warm-up fills them | the rule in [§4.4](#44-what-counts-as-a-difference) |
| M6 | ripgrep over a large tree | `rg -j JOBS -c <pattern that never matches>` over that tree | same | same | same | same |
| M7 | npm install | `npm ci --prefer-offline --ignore-scripts` of five pinned packages and their dependencies, with `node_modules` removed before each run | same | same; the warm-up fills the jail's npm cache over the network | npm cache warm; the timed runs use no network | same |
| M8 | `go test -short`, this repository | `go test -short -count=1 -p JOBS` in a clone of yolo-jail, over the packages that have tests on both Linux and darwin, under `env -i` as in CI | same; packages passed and failed are counted per run | 3, after one warm-up run | Go's build cache warm, in each backend's own home | the rule in [§4.4](#44-what-counts-as-a-difference) over the runs with the backend's usual pass and fail counts, whatever their exit code, and only between backends whose counts match |
| M9 | CPU, one thread | a 2,000,000,000-iteration integer loop in node | same | 5, after one warm-up run | none involved | same |
| M10 | CPU-bound build | `go build ./cmd/yolo` from an empty `GOCACHE` in the scratch folder: once with `-p` and `GOMAXPROCS` set to `JOBS`, once at each backend's default | same | 3 of each, after one warm-up run each | build cache empty by construction; sources warm | same; the pinned row is the like-for-like one |
| M11 | Process spawn | 2,000 execs of `true` (resolved by path, not the builtin) in a bash loop | same | 5, after one warm-up run | warm | same |
| M12 | First exec of a new binary | ten newly built tiny Go binaries in the scratch folder, each run twice in a row | same, per exec | 10 binaries | each binary is new by construction | the rule across backends; within one backend, a first-exec median at least twice the second-exec median |
| M13 | Disk footprint | `du` of Apple Container's data root and `container system df`; the darwin floor's closure (`nix path-info -S`); the sandbox account's home and state folder; the workspace's `.yolo` | host | once | not applicable | a gap of at least 10% |

The native control runs M5 to M12 too.

### 4.3 The memory session

- **One launch per backend, so one VM.** The session's shell stays alive throughout; each load
  runs as a child process or a file that ends before the "after" samples. On Apple Container the
  VM outlives every load, which is the case a real agent session is in. On macos-user there is
  nothing to outlive them.
- **The three loads differ on purpose.** Anonymous memory (node fills a buffer of `LOAD_MB`
  MiB and waits) is what any build or test allocates. The scratch file (`dd` of `LOAD_MB` MiB
  into `/tmp`) is RAM on Apple Container and disk on macOS. One read of a `LOAD_MB` MiB workspace
  file fills the guest's file cache on Apple Container, and the Mac's own on macos-user.
- **`LOAD_MB` defaults to an eighth of host RAM, from 256 to 2048 MiB**, so it stands clear of
  the noise in the Mac's system-wide figures and stays well under the jail's cap.
- **The drop-caches step** asks the Linux guest to free its caches
  (`echo 3 > /proc/sys/vm/drop_caches`). If the guest refuses, the refusal is recorded. Either
  way the question is whether the VM's footprint falls, and Apple's documentation predicts it
  does not.
- **Attribution.** The harness lists processes before the session and again once it is idle,
  and takes the one new process whose command line names `Virtualization` as the VM. If it finds
  none or several, it says so, records both lists, and reports only the system-wide figures and
  the `container-*` helpers' RSS. That VZ runs each VM in its own helper process is INFERRED, not
  sourced, which is why the harness checks rather than assumes it.
- **Everything is read from the host,** as the maintainer asked. The guest's own view
  (`container stats`) is not the question: the guest can report memory free that macOS still
  charges to the VM.

### 4.4 What counts as a difference

A difference between two backends is reported only when **both have at least three timed runs
that exited 0, their medians are at least 10% apart, and their ranges (fastest to slowest run)
do not overlap.** Otherwise the table says "no difference shown", which is not the same as
"equal": with five runs, non-overlapping ranges are a crude test and a small real difference can
fail it. A failed run is left out of the table and listed beneath it. M8 is the exception: a
test that fails on every run does not make its timing wrong, so it keeps the runs whose pass
and fail counts are that backend's usual ones, exit code aside, and compares two backends only
when those counts are equal.

### 4.5 Confounds, and what the harness does about each

| Confound | What the harness does |
| :--- | :--- |
| Apple Container has half the cores | M6, M8 and the pinned M10 use `JOBS` on every backend; the default M10 row shows the cap as users meet it |
| Performance and efficiency cores | records both counts; does not pin |
| Linux and darwin builds of each tool | same versions from one nixpkgs; records each jail's versions |
| Git refuses a repository another account owns | runs git with `safe.directory=*` on the command line |
| Apple Container runs a nix build at every launch | left in, because users pay it; the spans `YOLO_TIMING=1` records name it (`image.nix_build`) |
| One-time launch work | done by the warm-up launch, reported apart |
| `sudo` at every macos-user launch | asks once at the start and keeps the credential fresh; refuses at the start if sudo keeps no credential (`timestamp_timeout=0`), when every launch would ask again; the time a person spends typing a password is not measured |
| XProtect, and whether the terminal is a developer tool | M12 measures the gap; the operator notes the terminal's setting beside the results |
| Different test sets by platform | `go test` compiles different files on Linux and darwin, so M8 runs only the packages with tests on both; it runs under `env -i`, because a jail's own variables fail tests that pass in CI; pass and fail counts are recorded per run |
| Host file cache | not cleared, and macOS has no unprivileged way to clear it; every timed metric has a warm-up run, so all backends start warm |
| Sleep during an unattended run | holds a `caffeinate` assertion until it exits, and records the sleep settings |
| Spotlight, Time Machine and endpoint security reading the fixtures | the operator excludes the workspace before the session ([Appendix A](#appendix-a--the-harness)); the harness records the volume's indexing state and any endpoint-security extension |
| `_yolojail` processes the harness did not start | refuses to start while one runs; one that outlives a session by 120 s is listed in `leftovers-macos-user.txt` and left out of every later wait and memory row |

---

## 5. What this does not decide

- **Both backends continue.** The maintainer's words: "we will continue with both
  implementations". This doc measures; it does not choose, rank, or change a default.
- **It does not reopen the macOS direction's rulings**
  ([why it's this way](../reference/macos-no-vm-direction.md#why-its-this-way)). Those rest on
  more than speed: the isolation boundary, the Linux artifact some users need, and package
  availability on darwin.
- **It does not decide whether to tune Apple Container**, for example by restarting idle jails
  to give memory back, changing the cap, or moving caches off virtiofs. Each would be its own
  design, and would use these numbers.
- **It does not decide the auto-detection order** on macOS.

## 6. What the harness cannot measure

- **A machine-cold launch,** after a reboot or a fresh `container system start`, and a first
  image delivery. The warm-up launch absorbs whichever of those the Mac needs, and it runs once.
- **Seatbelt's cost apart from the separate account and `sudo`.** The native control removes all
  three at once.
- **Agent workloads, networking, energy use, and hours of thermal load.** No agent runs, by rule,
  and the session should take about 90 minutes.
- **Real memory pressure.** The harness does not force macOS to reclaim memory. Running
  `memory_pressure -S -l warn` during the memory session's last sample is a manual extension.
- **Several jails at once.**
- **The VM's own footprint, if the process diff does not find exactly one VZ process**
  ([§4.3](#43-the-memory-session)).
- **Why a first exec is slow.** M12 measures the gap, not its cause.
- **Nix store bytes for Apple Container's image,** which the harness does not separate from
  yolo's machine state.
- **Intel Macs, macOS 15, and Podman Machine.**

## 7. Found on the way: two Apple Container jails may mount one ext4 disk

Not a performance question, and unverified. **Every unsealed Apple Container jail mounts the named
volume `yolo-mise-data-v2` at `/mise`** ([assemble.go:25](../../internal/cli/run/assemble.go#L25),
[assemble_parts.go:62](../../internal/cli/run/assemble_parts.go#L62)), and on Apple Container a
named volume is an ext4 disk image attached to the guest as a block device
([RuntimeService.swift:1515-1524](https://github.com/apple/container/blob/0a48a1bdbfaa7451c810372d98b045fa8b486b6a/Sources/Services/RuntimeLinux/Server/RuntimeService.swift#L1515-L1524)).
`container` checks whether a volume is in use only when deleting it
([VolumesService.swift:375-381](https://github.com/apple/container/blob/0a48a1bdbfaa7451c810372d98b045fa8b486b6a/Sources/Services/ContainerAPIService/Server/Volumes/VolumesService.swift#L375-L381));
the attach path resolves the volume and mounts it with no such check
([Utility.swift:175-184](https://github.com/apple/container/blob/0a48a1bdbfaa7451c810372d98b045fa8b486b6a/Sources/Services/ContainerAPIService/Client/Utility.swift#L175-L184))
(MEASURED, by reading the source). So two jails in two workspaces may be two Linux kernels
mounting one ext4 filesystem read-write, which corrupts it (INFERRED). VZ may also refuse to
attach an image another VM holds, in which case the second jail would fail to start; nothing read
here says which.

**The check,** on a Mac, with two throwaway workspaces, after the harness and not before it: the
harness refuses to start while these jails run. They end after ten minutes, or
`container stop <name>` ends one at once (`yolo stop` does not see Apple Container jails). ⚠ If
the volume is shared, this can damage it; it holds only mise's tool installs, and
`container volume rm yolo-mise-data-v2` followed by a launch rebuilds it.

```console
$ mkdir -p /Users/Shared/yolo/vol-a /Users/Shared/yolo/vol-b
$ (cd /Users/Shared/yolo/vol-a && YOLO_RUNTIME=container yolo -- sleep 600 </dev/null >/tmp/vol-a.log 2>&1) &
$ (cd /Users/Shared/yolo/vol-b && YOLO_RUNTIME=container yolo -- sleep 600 </dev/null >/tmp/vol-b.log 2>&1) &
$ sleep 60; container ls
$ for c in $(container ls -q | grep '^yolo-vol-'); do container exec "$c" grep ' /mise ' /proc/mounts; done
```

Two running jails that each show `/mise` as `ext4` with `rw` confirm it. A second jail that fails
to start, with an error naming the volume or its disk image, refutes it and records a different
defect: two Apple Container jails cannot run at once.

**Run on 2026-10-02** (M1 Max, macOS 26.5, `container` 1.1.0, yolo `e09919d2`, with
`YOLO_NO_AUTO_CAPTURE=1` and `yolo run --accept-config-changes`): **refuted, and the other defect
confirmed** (MEASURED). The first jail booted with `/dev/vdc /mise ext4 rw,relatime`. The second
failed at once with `VZErrorDomain Code=2 "The storage device attachment is invalid."`, which does
not say which attachment it refused. As a control, the second workspace launched alone straight
afterwards and mounted `/mise` normally, so the cause is the first jail holding the disk
(INFERRED that it is `yolo-mise-data-v2` and not another shared attachment). **So, with this
`container`, two unsealed Apple Container jails in two workspaces cannot run at once**; nothing
was corrupted. Not yet re-run on 1.5.0.

**Fixing it is a trade-off, so it waits on a ruling.** No backing gets Apple Container all three
at once: one store that every workspace shares, the speed of the VM's own disk, and two jails
running together. The run above points to a volume's disk attaching to one VM at a time, and only
virtiofs reaches several. Two facts the options rest on:

- **A sealed build already takes its `/mise` from a host folder over virtiofs on this
  backend**, a folder of its workspace's own ([seal.go](../../internal/cli/run/seal.go)), so
  option B's mount is one yolo emits today (read from the code, not run on a Mac).
- **A Linux tool tree can hold names that differ only in case.** python-build-standalone's
  CPython 3.13.16 for `aarch64-unknown-linux-gnu` (release `20261003`, the `install_only` and
  `install_only_stripped` builds alike) lists 25 such pairs, all under `share/terminfo`, such as
  `e/eterm` and `E/Eterm` (MEASURED, from the archive listing). On a case-insensitive APFS
  volume, macOS's default, each pair is one file (INFERRED).

1. 💬 <a id="OQ-MB1"></a>**OQ-MB1: What should back an Apple Container jail's `/mise`, now that one jail's volume shuts out the next?**

   Podman's machine-wide volume is untouched either way.

   - **A — A volume per workspace.** Keeps the VM disk's speed and a case-sensitive store. Each
     workspace downloads its toolchains once and keeps its own copy (Node 24.21.0 for Linux
     arm64 unpacks to 189 MiB, MEASURED), and `yolo prune` must learn to remove a deleted
     workspace's volume.
   - **B — The machine's mise folder over virtiofs, as on Linux.** One shared store and almost
     no code. Workspace file work over virtiofs ran 3 to 5 times native in [§8](#8-results);
     `/mise`'s own reads were not measured. The store lands on APFS, where those case pairs
     collapse.

   <!-- vantage: question id=OQ-MB1 leaning="A, a volume per workspace: it keeps today's speed and a case-sensitive store, and its costs are one download per workspace and disk that yolo prune can reclaim, where B's cost would land on every jail's toolchain reads." -->

   _Leaning:_ A. It keeps today's speed and a case-sensitive store, and its costs are one
   download per workspace and disk that `yolo prune` can reclaim, where B's cost would land on
   every jail's toolchain reads.

   **Answer:**

   > _(empty — fill in when decided)_

## 8. Results

**Two sessions on one Mac.** 2026-10-02: Apple Container and the native control, run unattended
by an agent. 2026-10-03: macos-user and the native control, run by the maintainer from a
terminal, because each macos-user launch needs `sudo`'s password. The tables below are the first
session's; [macos-user, 2026-10-03](#macos-user-2026-10-03) is the second's, and
[the side-by-side](#the-two-backends-side-by-side) puts them together.

**The Mac:** Apple M1 Max (8 performance and 2 efficiency cores), 32 GiB, macOS 26.5 (25F71), on
mains power. `container` CLI **1.1.0**, older than the 1.5.0 whose source [§2](#2-what-the-sources-say) read. yolo
`0.11.0+358.ge09919d2`, nix 2.34.7. The Apple Container jail had 5 CPUs (`JOBS=5`) and a
16 GiB cap. The harness ran from an agent's shell and not from a terminal app, so the
developer-tool setting does not apply and was not recorded. **yolo's own nix builder VM,
`yolo-ac-builder` (8 CPUs, 12 GiB cap), ran throughout**: it ships the Linux image builds, so it
is part of every launch's cost, and it sits inside every system-wide memory figure below.

**Two deviations from Appendix A as it stood at `e09919d2`**, before the pre-checks' fixes landed. (1) The harness's jail filter matched
`yolo-ac-builder` as a running jail, so its preflight would refuse and every `wait_quiet` would
wait out its 120 s. That copy was patched to skip the builder, and the fix is now in Appendix A.
(2) A second, launch-only pass ran with `YOLO_NO_AUTO_CAPTURE=1`, for the reason under
*Launch* below. Raw output from both passes: `results-20261002-165253` (full) and
`results-20261002-175834` (launch only), under `/Users/Shared/yolo/bench-macos-backends/bench/`
on that Mac, not in the repository.

### Launch (M1 to M3)

| Metric | Apple Container, as run | Apple Container, `YOLO_NO_AUTO_CAPTURE=1` |
| :--- | :--- | :--- |
| warm-up launch | 139.2 s | 7.7 s |
| M1 fresh, to the marker | 67.3 s (66.0-68.2), n=5 | **6.9 s** (6.8-7.1), n=5 |
| M2 fresh, to exit | 67.6 s (66.4-68.6), n=5 | **7.2 s** (7.2-7.4), n=5 |
| M3 attach, to the marker | 16.2 s (15.8-16.4), n=10 | **1.8 s** (1.8-1.8), n=10 |
| M3 attach, to exit | 16.2 s (15.9-16.5), n=10 | 1.9 s (1.8-1.9), n=10 |

**About 90% of the as-run launch time was yolo's auto-capture failing and retrying on every
launch, not the VM** (MEASURED). Every launch, attach included, found claude, codex and agy
"never recorded on this machine" and ran each one's installer in a capture jail. Each installer
"left nothing in the capture surfaces", so nothing was stored, and the next launch tried again:
`launch.auto_capture` took 61 s per fresh launch and 14 s per attach. That was a yolo defect in
its own right, diagnosed from the code and fixed on main 2026-10-03; the fix is not yet measured
on a Mac. The capture walked surface paths that exist only on podman
([OQ-PD24](../design/program-delivery.md#decision-ledger)), an attach repeated the fresh launch's
attempt ([OQ-PD25](../design/program-delivery.md#decision-ledger)), and a failure was never
remembered ([OQ-PD26](../design/program-delivery.md#decision-ledger)). The next Mac run should see
the first fresh launch store all three programs, and no later launch spend about 61 s in
`launch.auto_capture`. An attach should not run it at all. If that first launch still stores
nothing, it now says when it will try again and later launches skip the capture until then, a
day after a first failure (INFERRED from `internal/cli/autocapture.go`, not run on a Mac), so the
61 s stops either way; only the stored programs show that
[OQ-PD24](../design/program-delivery.md#decision-ledger)'s fix works.
Without the auto-capture, a fresh launch's 6.9 s is mostly two steps: `image.nix_build` at 3.2
to 3.5 s (a no-op build that still runs at every launch, through the builder VM) and
`launch.run_with_proxy` at 2.5 s (VM boot plus the boot script). The attach's own `attach.exec`
took 1.4 s.

### Timings (M5 to M12)

Seconds: median (min-max). The verdict is [§4.4](#44-what-counts-as-a-difference)'s rule applied to container against native; the
harness's own verdict column compares only container with macos-user, so it was empty.

| Metric | container | native | container against native |
| :--- | :--- | :--- | :--- |
| M5 `git status`, 100,000 files | 0.860 (0.207-0.866), n=5 | 0.162 (0.161-0.170), n=5 | container **5.3× slower** |
| M6 ripgrep, same tree, `-j 5` | 12.528 (12.491-12.567), n=5 | 2.491 (2.479-2.621), n=5 | container **5.0× slower** |
| M7 `npm ci`, offline | 5.115 (5.041-5.184), n=5 | 1.575 (1.554-1.606), n=5 | container **3.2× slower** |
| M8 `go test -short -p 5` | 132.8 (132.5-132.8), n=3 | 218.9 (218.0-223.1), n=3 | not comparable (below) |
| M9 node loop, one thread | 15.791 (15.787-15.868), n=5 | 15.921 (15.919-15.931), n=5 | no difference shown (medians 0.8% apart) |
| M10 `go build`, pinned to 5 | 9.778 (9.767-9.812), n=3 | 8.428 (8.367-8.441), n=3 | container **1.16× slower** |
| M10 `go build`, each default | 9.872 (9.839-9.991), n=3 | 8.134 (8.034-8.175), n=3 | container 1.21× slower (5 cores against 10) |
| M11 2,000 execs of `true` | 1.285 (1.274-1.368), n=5 | 8.639 (7.859-8.658), n=5 | container **6.7× faster** |
| M12 first exec of a new binary | 0.001 (0.001-0.002), n=10 | 0.400 (0.387-0.452), n=10 | container **about 400× faster** |
| M12 second exec | 0.001 (0.001-0.001), n=10 | 0.007 (0.006-0.007), n=10 | container faster |

- **Files: the prediction holds, and at the high end.** Work over the shared workspace ran 3 to 5
  times native, between the 2.7× and the 6 to 9× of [§2.3](#23-shared-folders-virtiofs). One M5 run took 0.207 s against 0.86 s
  for the rest, so warm `git status` on virtiofs is bimodal here; the cause was not looked at.
- **CPU: a wash on one thread, about 16% slower on a parallel build** at the same thread count.
- **Process start is where the Mac loses.** A darwin exec costs about 4 ms against Linux's
  0.6 ms, and a binary's first exec costs about 0.4 s, 57 times its second ([§4.2](#42-the-metrics)'s rule for a
  first-exec gap is twice). Why is not measured ([§6](#6-what-the-harness-cannot-measure)); XProtect's first-launch scan is the
  candidate [§4.5](#45-confounds-and-what-the-harness-does-about-each) names. A native control shares this cost with macos-user, which this run did not
  measure.
- **M8 has no verdict:** every timed run exited 1 on both sides, with 111 packages passing on
  both, 9 failing in the jail and 5 natively. The harness leaves non-zero runs out of the table,
  and the two runs did not test the same thing.

### Memory (M4), Apple Container

MiB, from the host. *footprint* is `top`'s MEM for the VM process (pid found by the process
diff, as [§4.3](#43-the-memory-session) intends), *resident* its `ps` RSS. The load was 2048 MiB.

| When | VM footprint | VM resident | Mac free |
| :--- | ---: | ---: | ---: |
| before the session | - | - | 3127 |
| idle, 30 s after boot | 878 | 1062 | 1667 |
| anonymous load held | 3012 | 3248 | 81 |
| 120 s after it ended | 3017 | 3256 | 322 |
| scratch file held, then 120 s after | 3021 | 3260 / 3261 | 289 / 291 |
| workspace file read, then 120 s after | 3023 | 5309 / 5310 | 57 / 232 |
| after the guest's drop-caches step | 3023 | 5316 | 251 |
| 10 s after the jail exited | - | - | 4025 |

- **Held, as [§2.2](#22-memory-backed-on-first-touch-kept-until-the-container-stops) predicts** (MEASURED): 120 s after each load ended, the VM still held 104% of
  it over idle. Dropping the guest's caches gave nothing back. Only the jail's exit did.
- **The later loads added no footprint.** The scratch file and the workspace read each left the
  footprint within 6 MiB of where the anonymous load left it. INFERRED: the guest reused pages
  it had already touched, so loads in this order measure the peak, not their sum. The workspace
  read raised RSS by 2 GiB without moving the footprint, which looks like the file's pages being
  counted on the Mac's side of virtiofs; that is not checked.
- macOS's memory status level read 94 throughout, so the Mac was never under pressure.

### Disk (M13)

Apple Container's data root: 26.0 GiB (`du`). `container system df` reports 6 images totalling
26.72 GB with 93% reclaimable, one 1.02 GB container and a 149.3 MB volume. yolo's machine state
(`~/.local/share/yolo-jail`) came to 2.3 GiB, and the workspace's `.yolo` to 17 MiB.

### macos-user, 2026-10-03

Same Mac, yolo `0.11.1+15.g5ca9b748`, from a terminal. **One deviation:** the run needed
`BENCH_ALLOW_RUNNING=1`, because macOS had started its per-user agents for `_yolojail`
(`distnoted`, `lsd`, `cfprefsd`, `secd`) and the harness counted them as a running jail. Appendix
A now leaves out every `_yolojail` process under `/usr/libexec`, `/usr/sbin` or `/System`. Raw
output: `results-20261003-131315`, beside the first session's.

| Metric | macos-user | native, same day | macos-user against native |
| :--- | :--- | :--- | :--- |
| M1 fresh, to the marker | **5.5 s** (5.4-5.6), n=5 | - | - |
| M2 fresh, to exit | 5.5 s (5.5-5.6), n=5 | - | - |
| warm-up launch | 8.4 s to the marker, 15.4 s to exit | - | - |
| M5 `git status`, 100,000 files | 0.174 (0.173-0.178) | 0.174 (0.171-0.177) | no difference |
| M6 ripgrep, same tree | 2.881 (2.777-2.972) | 2.748 (2.717-2.774) | 5% slower; not a difference by [§4.4](#44-what-counts-as-a-difference) |
| M7 `npm ci`, offline | 1.773 (1.756-1.796) | 1.740 (1.696-1.794) | no difference |
| M8 `go test` | did not run | 232.2 (227.8-235.8), 113 ok, 0 FAIL | - |
| M9 node loop | 16.260 (16.253-16.329) | 16.229 (16.212-16.237) | no difference |
| M10 `go build`, pinned to 5 | 9.306 (9.279-9.345) | 9.278 (9.179-9.383) | no difference |
| M10 `go build`, each default | 8.994 (8.983-9.227) | 9.029 (9.022-9.046) | no difference |
| M11 2,000 execs of `true` | 9.465 (9.150-9.508) | 10.298 (9.431-10.347) | no difference (ranges overlap) |
| M12 first exec of a new binary | 0.384 (0.375-0.430) | 0.386 (0.378-0.442) | no difference |
| M12 second exec | 0.009 (0.006-0.011) | 0.008 (0.007-0.010) | no difference |

- **macos-user is native**, within noise on every metric it ran (MEASURED). The Seatbelt profile
  and the second account cost nothing measurable here.
- **macos-user has no attach** (M3): every launch is fresh, and 5.5 s is its whole cost.
- **M8 did not run**: inside the sandbox `go` is a mise shim, and mise refused the clone's
  untrusted `mise.toml`, so every run exited 1 in 0.03 s. Appendix A now sets
  `MISE_TRUSTED_CONFIG_PATHS` to the clone; the re-run is owed.
- **Memory: everything came back** (MEASURED). 120 s after each 2048 MiB load the `_yolojail`
  processes held 9 to 77 MiB more than idle, 0 to 2% of the load. The scratch file was ordinary
  macOS file cache (file-backed rose by 2 GiB while it existed and fell when it was deleted), not
  RAM held by anything yolo runs.
- **Disk:** the darwin package closure is 2.2 GiB, the sandbox account's home 566 MiB and
  `/var/yolo-jail` 58 MiB.

### The two backends side by side

Each backend against its own day's native run, because native itself moved by up to 20% between
the two days (M8 219 s to 232 s, M11 8.6 s to 10.3 s; the cause, CrowdStrike Falcon's
endpoint-security extension or something else, was not looked at).

| | Apple Container | macos-user |
| :--- | :--- | :--- |
| fresh launch | 6.9 s without auto-capture, 67 s with it | 5.5 s |
| attach | 1.8 s | none; every launch is fresh |
| files in the workspace (M5-M7) | 3 to 5 times native | native |
| one-thread CPU (M9) | native | native |
| parallel build (M10) | 16% slower at 5 threads; it gets half the cores | native |
| process start (M11, M12) | 7 to 400 times **faster** than native | native |
| memory after a load ends | held until the jail stops | returned |
| sudo | no | at every launch |

**What decides between them is file work against process start.** Apple Container loses badly on
anything that walks many files in the shared workspace and wins on anything that starts many
processes or new binaries; macos-user is the Mac, for better and worse. Why the file cost is so
high, and what it means for a large Python monorepo with a database, is
[its own write-up](apple-container-file-cost.md).

This table stays the dated record of these two runs; the standing comparison, with Podman Machine
and the runs without yolo beside them, is
[the macOS direction's measured section](../reference/macos-no-vm-direction.md#what-each-macos-path-costs-measured).

### Defects found on the way

Each is a yolo defect unless it says otherwise. The first two are being worked on; the rest are
not filed yet.

1. **Auto-capture retries on every launch** and takes about 90% of an Apple Container launch.
   Fixed from the code on 2026-10-03, not yet re-run on a Mac (*Launch*, above).
2. **Two Apple Container jails cannot run at once**: the second is refused by VZ. Waits on
   [OQ-MB1](#OQ-MB1) ([§7](#7-found-on-the-way-two-apple-container-jails-may-mount-one-ext4-disk)).
3. **One workspace cannot alternate between the two backends.** After an Apple Container jail,
   macos-user's warm-up failed on three real, empty directories in `.yolo/home`
   (`darwin_home_layout`); after macos-user, Apple Container failed on the symlinks macos-user left
   there (`mount failed with errno 17: failed to create directory '.claude-shared-credentials'`).
   Both were cleared by hand. `yolo check` reported neither.
4. **A jail daemon dials a host service the launch already called unreachable.** Each Apple
   Container launch says the openai-auth loophole "is inert on this backend", then the in-jail
   `openai-auth-broker` daemon still dials it and logs `lookup host.containers.internal on
   192.168.64.1:53: no such host` (the name is podman's). Noise, not a failure: the launch goes on.
5. **Harness (fixed in Appendix A, untested on macos-user):** macOS's per-user agents for
   `_yolojail` counted as jail processes, and mise's trust check stopped macos-user's `go test`.

## 9. Corrections the results feed

Owed by the session that records the results, and not made here: the direction doc's text is a
ruling, and this doc only adds a pointer beside its premise. That pointer is in place, and leads to
[the direction's measured section](../reference/macos-no-vm-direction.md#what-each-macos-path-costs-measured).

- **[direction:343](../reference/macos-no-vm-direction.md#L343), "still reserves RAM up front"**,
  is wrong on Apple's own documentation (C4) and needs no measurement to correct.
- **[direction:32-37](../reference/macos-no-vm-direction.md#L32-L37)**: "slow to start" takes M1's
  and M3's figures; "a RAM ceiling you have to guess" becomes the ceiling yolo picks (C3); "that
  RAM permanently held" takes M4's.
- **[direction:18-19](../reference/macos-no-vm-direction.md#L18-L19) and
  [direction:263](../reference/macos-no-vm-direction.md#L263), "the fast native path"**: measured,
  macos-user starts a fresh jail faster, runs workspace file work at native speed and gives memory
  back, and is slower at re-entry and at starting processes
  ([side by side](#the-two-backends-side-by-side)). A VM jail with the
  folders only it uses on VM-local disks would also be faster at installing, testing and setting up
  a database (INFERRED, from
  [the runtime comparison](macos-vm-runtime-comparison.md#31-on-a-vm-local-disk)). That bears on the
  ruling to pursue both backends as one composed product
  ([why it's this way](../reference/macos-no-vm-direction.md#why-its-this-way)), whose reason casts
  the choice as "fast" against "works for this package": measured, the choice is which work is
  fast. The ruling is the maintainer's to weigh this against.
- **[platform-comparison.md:263-270](platform-comparison.md#L263-L270)** and
  **[sandbox-comparison.md:437](sandbox-comparison.md#L437)** carry startup and file figures that
  CI and the sources contradict (C9, C10).
- **[macos-revival-and-distribution-plan.md:1272](../plans/macos-revival-and-distribution-plan.md#L1272)**
  names a balloon that does not exist (C14).
- **The user guide's "fastest start"** ([macos.md:23](../../userguide/guides/macos.md#L23),
  [macos.md:223](../../userguide/guides/macos.md#L223),
  [confinement.md:36](../../userguide/guides/confinement.md#L36)) stays or is qualified on M1
  and M3. Qualified on 2026-10-04 to "fastest fresh start", beside a table of the measured
  differences ([how they compare on speed](../../userguide/guides/macos.md#how-they-compare-on-speed)).
- **[macos-user-provisioning.md:627](../reference/macos-user-provisioning.md#L627)** says nobody
  has recorded what a first macos-user launch costs; CI has one, 73.61 s on a hosted runner
  ([§2.6](#26-what-ci-logs-already-hold)).

---

## Fast-moving — verify before building

- **`container` releases often:** 0.1.0 on 2025-06-09, 1.5.0 on 2026-09-29. The default kernel,
  the default resources and the boot time can all change between releases.
- **The balloon proposals were closed unmerged on 2026-08-28.** Apple may still ship one, which
  would change C5.
- **macOS 27 lets apps implement their own Virtio devices** (WWDC26 session 224), which could
  make free page reporting possible.
- **Containerization tests guest kernels from 6.14.9 up**
  ([README](https://github.com/apple/containerization/blob/f24df2ac817df66fe149a80103251dec987c32dc/README.md#L86)).
- **No published boot time covers any 1.x release.**
- **The pre-booted VM pool,** [#1924](https://github.com/apple/container/issues/1924), would
  change C2 if it lands.

## Sources

- [apple/container technical overview at `0a48a1bd`](https://github.com/apple/container/blob/0a48a1bdbfaa7451c810372d98b045fa8b486b6a/docs/technical-overview.md) — Apple's own statement that freed guest pages are not returned to macOS.
- [apple/containerization](https://github.com/apple/containerization) — the one-VM-per-container design; the source read for the virtiofs device, the disk cache modes and the absent balloon.
- [WWDC25 session 346](https://developer.apple.com/videos/play/wwdc2025/346/) and [WWDC26 session 389](https://developer.apple.com/videos/play/wwdc2026/389) — Apple's sub-second start claims and the "no resources when nothing runs" statement.
- [VZ `memorySize`](https://developer.apple.com/documentation/virtualization/vzvirtualmachineconfiguration/memorysize) and [VZ's traditional balloon](https://developer.apple.com/documentation/virtualization/vzvirtiotraditionalmemoryballoondevice) — reserve-but-not-allocate, and the only reclaim device VZ offers.
- apple/container issues [#58](https://github.com/apple/container/issues/58), [#738](https://github.com/apple/container/issues/738) and [#1924](https://github.com/apple/container/issues/1924); PRs [#1041](https://github.com/apple/container/pull/1041) and [#2143](https://github.com/apple/container/pull/2143); [discussion #1516](https://github.com/apple/container/discussions/1516) — measured boot times, why there is no VM pool, the root disk's cache mode, the kernel default, and the maintainer on virtiofs builds.
- apple/containerization [#882](https://github.com/apple/containerization/issues/882), [#893](https://github.com/apple/containerization/pull/893), [#894](https://github.com/apple/containerization/pull/894) — the balloon proposals (one issue, two PRs), closed unmerged in a sweep of one contributor's PRs.
- [zot24/macos-container-benchmarks](https://github.com/zot24/macos-container-benchmarks) — boot, file, network and macOS 15 against 26 figures for 0.11.0, with its scripts.
- [RepoFlow](https://www.repoflow.io/blog/apple-containers-vs-docker-desktop-vs-orbstack) — CPU, memory and small-file figures for 0.6.0 against Docker and OrbStack.
- [Mainardi](https://www.paolomainardi.com/posts/docker-performance-macos-2025) — the one source with a native baseline for the same VZ virtiofs mechanism.
- [Madhavapeddy](https://anil.recoil.org/notes/apple-containerisation) — 0.1.0's boot time and kernel.
- [OrbStack on dynamic memory](https://orbstack.dev/blog/dynamic-memory) and [its author on VZ page reporting](https://news.ycombinator.com/item?id=34720219) — the 2023 statement that VZ lacks free page reporting.
- [lima-vm/lima #1269](https://github.com/lima-vm/lima/issues/1269) — Apple's answer on Rosetta slowing native code in a VZ VM.
- [Nethercote](https://nnethercote.github.io/2025/09/04/faster-rust-builds-on-mac.html) — XProtect's first-run scan and its measured cost.
- [Szorc](https://gregoryszorc.com/blog/2018/10/29/global-kernel-locks-in-apfs/), [bitsnbites](https://www.bitsnbites.eu/benchmarking-os-primitives/), [Bazel](https://blog.bazel.build/2018/04/13/preliminary-sandboxfs-support.html) — older macOS figures, kept as weak predictors.
- [Firecracker](https://github.com/firecracker-microvm/firecracker) (`SPECIFICATION.md`, `docs/ballooning.md`, issue #1180) — the microVM that prompted the question; Linux and KVM only.

---

## Appendix A — the harness

**What it is.** A POSIX sh script that runs [§4](#4-the-protocol) on one Mac and writes
`results.md`, plus every log, under `$BENCH_WS/bench/results-<stamp>/`. It writes a bash payload,
which runs inside each jail, and a node timing helper beside it. Apart from what macOS ships, it
needs `yolo`, `git`, `node` and `npm` on `PATH`, Apple's `container` for that backend, and, for
macos-user, the `nix` that backend already requires. It installs nothing.

**Before the session,** on the Mac:

1. **Install what you pulled.** `git pull && just install` in the checkout; `yolo --version`
   should then name the pulled commit, and `type -a yolo` should list one `yolo`. Leave
   `YOLO_ALLOW_SOURCE_SKEW` unset: with `YOLO_REPO_ROOT` set to a checkout newer than the
   installed `yolo`, every Apple Container launch refuses.
2. **Get a green Apple Container CI run of that commit,** the one test of the launch path the
   container column runs on. Let a pending `apple-container.yml` run finish, or start one with
   `gh workflow run apple-container.yml --ref main`, and wait until it is green.
3. **Then stop the self-hosted runner and its dispatcher until the session ends,** and push
   nothing to `main` meanwhile: a CI job launches Apple Container jails, and the harness refuses
   while the runner is loaded (`BENCH_ALLOW_RUNNER=1` overrides). The commands come from
   [the runner runbook](../plans/runbooks/mac-actions-runner.md); the last two restore both
   afterwards.

   ```console
   $ (cd ~/actions-runner && ./svc.sh stop)
   $ launchctl bootout gui/$(id -u)/com.yolo-jail.mac-runner-dispatch
   $ (cd ~/actions-runner && ./svc.sh start)
   $ launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.yolo-jail.mac-runner-dispatch.plist
   ```

4. **Check both backends in the bench workspace,** and fix every failure they report: a warm-up
   launch that fails stops the run.

   ```console
   $ mkdir -p /Users/Shared/yolo/bench-macos-backends && cd /Users/Shared/yolo/bench-macos-backends
   $ YOLO_RUNTIME=container yolo check --no-build
   $ YOLO_RUNTIME=macos-user yolo check --no-build
   ```

5. **Keep Spotlight out of the workspace** in System Settings → Spotlight → Search Privacy, by
   adding `/Users/Shared/yolo/bench-macos-backends`. Not with `mdutil`, which turns indexing on or
   off for a whole volume, and `/Users/Shared` is on the Data volume with everything else. If
   Time Machine is on, also run `tmutil addexclusion /Users/Shared/yolo/bench-macos-backends`.
6. **Plug the Mac in,** with Low Power Mode off. The harness keeps it awake itself, with
   `caffeinate`, until it exits.
7. **Leave no jail running:** `container ls`, then `container stop <name>` for each `yolo-` name
   (`yolo stop` does not see Apple Container jails), and end any macos-user session.

**Run it** from a yolo-jail checkout on the Mac, in a terminal tab of your own rather than an
agent's shell, because `sudo -v` asks on the terminal. The `awk` line copies the block below out
of this file; a full run should take about 90 minutes (an estimate from its run counts) and asks
for your password once.

```console
$ awk '/^````sh$/ { p = 1; next } p && /^````$/ { exit } p' docs/research/macos-backend-performance.md > /tmp/macos-backend-bench.sh
$ sh /tmp/macos-backend-bench.sh
```

**Settings,** all environment variables: `BENCH_BACKENDS` (default `container macos-user native`),
`BENCH_PHASES` (default `launch memory io cpu spawn disk`), `BENCH_RUNS`, `BENCH_ATTACH_RUNS`,
`BENCH_BUILD_RUNS`, `BENCH_FILES`, `BENCH_LOAD_MB`, `BENCH_SETTLE`, `BENCH_JOBS`, `BENCH_SRC_URL`
and `BENCH_SRC_REF` (a branch or tag of yolo-jail to clone for M8 and M10; a local checkout path
works as the URL). Two more override the preflight's refusals, which name them:
`BENCH_ALLOW_RUNNING=1` (Apple Container jails or `_yolojail` processes already running; it
leaves those processes out of its waits and readings) and `BENCH_ALLOW_RUNNER=1` (a loaded
Actions runner). The fixtures stay in the workspace between runs; delete
`/Users/Shared/yolo/bench-macos-backends` when done.

**How far it has been checked.** MEASURED in a Linux jail on 2026-10-01, and again on 2026-10-02
after two pre-checks, one reading every Mac command the harness runs against Apple's sources and
one reading the harness against yolo's code as it now stands, and the fixes they found. The
script passes `sh -n`, `dash -n` and shellcheck 0.11.0 at warning level, and the payload passes
shellcheck as bash. A run of every phase completed against stub commands standing in for `yolo`,
`container`, `sudo`, `id`, `nix`, `uname`, `vm_stat`, `top`, `sysctl`, `sw_vers`, `pmset`,
`caffeinate`, `launchctl`, `mdutil` and `systemextensionsctl`, with the `/Users/Shared/yolo`
check pointed at a scratch folder, and wrote a complete `results.md`. Shorter stubbed runs took
each failure path:

- a warm-up launch that fails stops the run at once;
- a memory session that starts late, stalls, or never starts ends within its bounds instead of
  hanging (tested with those bounds cut to seconds);
- a `_yolojail` process that outlives a session costs one 120 s wait, not one per launch, and
  stays out of later memory rows;
- a loaded runner, a running `_yolojail` process and a sudo that keeps no credential each refuse
  before the first launch;
- M8 keeps runs that exit non-zero when their pass and fail counts are the backend's usual ones,
  and does not compare backends whose counts differ.

In the same jail, with yolo's own environment, `go test` of this repository at that day's `main`
failed six tests in four packages as the harness used to run it, and passed every package it ran
under the harness's `env -i`. That proves the control flow and the report, not the Mac commands
themselves. Their output formats were read from Apple's sources and parsed here under BWK awk,
the awk macOS ships, not observed on a Mac: a `vm_stat` line the harness cannot find prints as
`-`, and the raw `vm_stat` and `top` output behind every memory row is kept in
`raw-memory.txt`, so check that file before trusting the memory table.

**On a Mac, 2026-10-02**, with the harness as it stood at `e09919d2`, before the two pre-checks'
fixes: every phase ran to completion for `container` and `native` ([§8](#8-results)).
`vm_stat`'s 16 KiB pages were read correctly, and the process diff found exactly one VM process.
Its jail filter counted yolo's builder VM as a jail, which is fixed above. The `macos-user`
paths, `sudo` included, have still not run on a Mac.

````sh
#!/bin/sh
# macos-backend-bench.sh: time and size Apple Container and macos-user on one Mac.
# From docs/research/macos-backend-performance.md (Appendix A). POSIX sh.
# It starts no agent session and makes no model call.
#
#   sh macos-backend-bench.sh                        # everything; about 90 minutes
#   BENCH_PHASES="launch memory" sh macos-backend-bench.sh
#   BENCH_BACKENDS="container" sh macos-backend-bench.sh
#
# Needs macOS on Apple silicon with yolo, git, node and npm on PATH; Apple's
# `container` for that backend; `yolo macos-setup` already run for macos-user.
# Everything it writes is under $BENCH_WS. Results: $BENCH_WS/bench/results-<stamp>/results.md
set -eu

BENCH_WS=${BENCH_WS:-/Users/Shared/yolo/bench-macos-backends}
BENCH_BACKENDS=${BENCH_BACKENDS:-container macos-user native}
BENCH_PHASES=${BENCH_PHASES:-launch memory io cpu spawn disk}
BENCH_RUNS=${BENCH_RUNS:-5}               # timed runs per metric, after one untimed warm-up run
BENCH_ATTACH_RUNS=${BENCH_ATTACH_RUNS:-10}
BENCH_BUILD_RUNS=${BENCH_BUILD_RUNS:-3}   # timed runs of go test and go build
BENCH_FILES=${BENCH_FILES:-100000}        # files in the synthetic repository
BENCH_SPAWNS=${BENCH_SPAWNS:-2000}        # execs per spawn-loop run
BENCH_FRESH_BINS=${BENCH_FRESH_BINS:-10}  # newly built binaries, each run twice
BENCH_CPU_ITERS=${BENCH_CPU_ITERS:-2000000000}
BENCH_IDLE=${BENCH_IDLE:-30}              # seconds a booted jail sits before the idle sample
BENCH_SETTLE=${BENCH_SETTLE:-120}         # seconds after a load ends before its last sample
BENCH_LOAD_MB=${BENCH_LOAD_MB:-}          # default: an eighth of RAM, 256 to 2048 MiB
BENCH_JOBS=${BENCH_JOBS:-}                # default: the Apple Container jail's CPU count
BENCH_SRC_URL=${BENCH_SRC_URL:-https://github.com/mschulkind-oss/yolo-jail.git}
BENCH_SRC_REF=${BENCH_SRC_REF:-main}

STAMP=$(date +%Y%m%d-%H%M%S)
CTL=$BENCH_WS/bench/ctl
FIX=$BENCH_WS/bench/fixtures
R=$BENCH_WS/bench/results-$STAMP
MARK=__BENCH_READY__
# The ready command. bash drops the empty quotes when it runs it, so the marker is in the
# command's output and not in yolo's "Executing: <command>" banner, which prints it unchanged.
MARK_CMD='echo __BENCH_""READY__'
FLOOR=$HOME/.local/share/yolo-jail/build/package-roots/packages
NATIVE_HOME=$BENCH_WS/bench/native-home
BG_PID=
VM_PID=
SUDO_KEEPER=
SANDBOX_UID=-1

case $BENCH_WS in *"'"*) echo "BENCH_WS must not contain a single quote" >&2; exit 1 ;; esac
mkdir -p "$CTL" "$FIX" "$R/out" "$NATIVE_HOME"
: >"$R/unmeasured.txt"

say() { printf '%s %s\n' "$(date +%H:%M:%S)" "$*" | tee -a "$R/harness.log" >&2; }
die() { say "FATAL: $*"; exit 1; }
unmeasured() { printf -- '- %s\n' "$*" >>"$R/unmeasured.txt"; say "not measured: $*"; }
has() { command -v "$1" >/dev/null 2>&1; }
in_list() { case " $2 " in *" $1 "*) return 0 ;; esac; return 1; }
drop_backend() {
  BENCH_BACKENDS=$(printf '%s\n' $BENCH_BACKENDS | awk -v b="$1" '$0 != b' | tr '\n' ' ')
  unmeasured "$1: $2"
}
# yolo-ac-builder is scripts/mac-ac-linux-builder.sh's nix builder, not a jail.
running_jails() {
  container ls -q 2>/dev/null | awk -v b="${YOLO_AC_BUILDER_NAME:-yolo-ac-builder}" '/^yolo-/ && $0 != b' | tr '\n' ' '
}
# The _yolojail processes, as "uid pid rss-KiB command", less the ones in SANDBOX_IGNORE: pids
# that were running before the harness (BENCH_ALLOW_RUNNING=1) or outlived a session by 120 s.
# Those are listed once, in leftovers-macos-user.txt, and no wait or memory row counts them again.
SANDBOX_IGNORE=
# macOS also starts its per-user agents for the account (distnoted, cfprefsd, secd, lsd, ...);
# they are not the jail's, live under /usr/libexec, /usr/sbin or /System, and are never counted.
sandbox_list() { # sandbox_list [more ps columns]
  local os
  os=$(ps -axww -o uid=,pid=,comm= | awk -v u="$SANDBOX_UID" \
    '$1 == u && $3 ~ /^\/(usr\/libexec|usr\/sbin|System)\// { printf " %s", $2 }')
  ps -axww -o "uid=,pid=,${1:-rss=,comm=}" | awk -v u="$SANDBOX_UID" -v ign=" $SANDBOX_IGNORE $os " \
    '$1 == u && index(ign, " " $2 " ") == 0'
}
sandbox_procs() { sandbox_list | awk 'END { print NR }'; }
ignore_sandbox() { SANDBOX_IGNORE=$(sandbox_list | awk -v s="$SANDBOX_IGNORE" '{ s = s (s == "" ? "" : " ") $2 } END { print s }'); }

cleanup() {
  if [ -n "$SUDO_KEEPER" ]; then kill "$SUDO_KEEPER" 2>/dev/null || true; fi
  if [ -n "$BG_PID" ]; then kill "$BG_PID" 2>/dev/null || true; fi
  : >"$CTL/stop-hold" 2>/dev/null || true
  if [ -d "$CTL/mem" ]; then release_mem 2>/dev/null || true; fi
}
trap cleanup EXIT
trap 'exit 130' INT TERM

# ---------------------------------------------------------------- preflight
preflight() {
  [ "$(uname -s)" = Darwin ] || die "this harness runs on macOS"
  [ "$(uname -m)" = arm64 ] || say "warning: not Apple silicon; Apple Container will not run here"
  # Idle sleep would pause the VM and stretch any timing or settle window it lands in; output to
  # a terminal does not count as activity. -w ends the assertion when the harness ends.
  if has caffeinate; then caffeinate -ims -w $$ & else say "warning: no caffeinate; keep the Mac awake yourself"; fi
  for t in yolo git node npm; do has "$t" || die "$t is not on PATH"; done
  NODE=$(command -v node)
  [ "$BENCH_SETTLE" -ge 20 ] || die "BENCH_SETTLE must be at least 20"
  if in_list container "$BENCH_BACKENDS"; then
    if ! has container; then
      drop_backend container "Apple's container CLI is not on PATH"
    elif ! container system status >/dev/null 2>&1; then
      drop_backend container "container system status failed; run container system start"
    elif [ -n "$(running_jails)" ] && [ -z "${BENCH_ALLOW_RUNNING:-}" ]; then
      die "Apple Container jails are running ($(running_jails)); stop each with container stop <name> (yolo stop does not see them) so the memory readings are this harness's alone, or set BENCH_ALLOW_RUNNING=1"
    fi
  fi
  if in_list macos-user "$BENCH_BACKENDS"; then
    if ! SANDBOX_UID=$(id -u _yolojail 2>/dev/null); then
      drop_backend macos-user "there is no _yolojail account; run yolo macos-setup"
    else
      case $BENCH_WS in
        /Users/Shared/yolo/*) ;;
        *) drop_backend macos-user "BENCH_WS is not under /Users/Shared/yolo, where macos-user projects live" ;;
      esac
    fi
  fi
  if in_list macos-user "$BENCH_BACKENDS" && [ "$(sandbox_procs)" -ne 0 ]; then
    sandbox_list etime=,rss=,command= | tee "$R/leftovers-macos-user.txt" >&2
    [ -n "${BENCH_ALLOW_RUNNING:-}" ] ||
      die "processes already run as _yolojail (listed above); end that macos-user session so the waits and the memory readings are this harness's alone, or set BENCH_ALLOW_RUNNING=1 to leave exactly these out"
    ignore_sandbox
  fi
  [ -n "$(printf '%s' $BENCH_BACKENDS)" ] || die "no backend is left to measure"
  # A self-hosted Actions runner on this Mac takes Apple Container CI jobs (apple-container.yml),
  # which launch jails and load images in the middle of the run.
  if launchctl list 2>/dev/null | grep -q 'actions\.runner\.' && [ -z "${BENCH_ALLOW_RUNNER:-}" ]; then
    die "an actions.runner launchd agent is loaded, so a CI job could launch Apple Container jails during the run; stop it and its dispatcher for the session ((cd ~/actions-runner && ./svc.sh stop); launchctl bootout gui/$(id -u)/com.yolo-jail.mac-runner-dispatch), or set BENCH_ALLOW_RUNNER=1"
  fi
  memsize=$(sysctl -n hw.memsize 2>/dev/null || echo 0)
  ncpu=$(sysctl -n hw.ncpu 2>/dev/null || echo 2)
  if [ -n "$BENCH_LOAD_MB" ]; then LOAD_MB=$BENCH_LOAD_MB; else
    LOAD_MB=$((memsize / 8 / 1048576))
    if [ "$LOAD_MB" -gt 2048 ]; then LOAD_MB=2048; fi
    if [ "$LOAD_MB" -lt 256 ]; then LOAD_MB=256; fi
  fi
  if [ -n "$BENCH_JOBS" ]; then JOBS=$BENCH_JOBS; else
    JOBS=$((ncpu / 2)); if [ "$JOBS" -lt 2 ]; then JOBS=2; fi   # yolo's Apple Container default
  fi
  if in_list macos-user "$BENCH_BACKENDS"; then
    say "macos-user runs sudo at every launch: asking for your password once and keeping it fresh"
    sudo -v || die "sudo -v failed"
    sudo -n true 2>/dev/null ||
      die "sudo kept no credential after sudo -v (timestamp_timeout=0?), so every sudo in every macos-user launch would ask again; see sudo -l, or leave macos-user out of BENCH_BACKENDS"
    ( while kill -0 $$ 2>/dev/null; do sudo -n -v 2>/dev/null || true; sleep 50; done ) &
    SUDO_KEEPER=$!
  fi
}

record_env() {
  {
    echo "stamp=$STAMP"
    sw_vers 2>/dev/null || true
    echo "arch=$(uname -m)"
    echo "cpu=$(sysctl -n machdep.cpu.brand_string 2>/dev/null || echo unknown)"
    echo "hw.memsize=$memsize hw.ncpu=$ncpu"
    echo "p-cores=$(sysctl -n hw.perflevel0.physicalcpu 2>/dev/null || echo -) e-cores=$(sysctl -n hw.perflevel1.physicalcpu 2>/dev/null || echo -)"
    echo "yolo=$(yolo --version 2>&1 | head -1)"
    if has container; then echo "container=$(container --version 2>&1 | head -1)"; fi
    if has nix; then echo "nix=$(nix --version 2>&1 | head -1)"; fi
    echo "git=$(git --version) node=$(node --version) npm=$(npm --version)"
    pmset -g batt 2>/dev/null | head -2 || true
    pmset -g 2>/dev/null | awk '/lowpowermode|powermode| sleep |displaysleep/' || true
    pmset -g assertions 2>/dev/null | awk '/PreventUserIdleSystemSleep|PreventSystemSleep/' || true
    mdutil -s /System/Volumes/Data 2>&1 | tail -1 || true
    # The endpoint_security section with its rows: the rows name each product and its state,
    # and only the "--- com.apple.system_extension.endpoint_security" header holds the word
    systemextensionsctl list 2>/dev/null | awk '/^---/ { p = /endpoint_security/ } p || /endpoint_security/' || true
    launchctl list 2>/dev/null | awk '/actions\.runner\./ { print "runner loaded: " $3 }' || true
    if [ -n "$SANDBOX_IGNORE" ]; then echo "_yolojail pids left out (BENCH_ALLOW_RUNNING): $SANDBOX_IGNORE"; fi
    echo "backends=$BENCH_BACKENDS"
    echo "phases=$BENCH_PHASES"
    echo "runs=$BENCH_RUNS attach_runs=$BENCH_ATTACH_RUNS build_runs=$BENCH_BUILD_RUNS files=$BENCH_FILES"
    echo "load_mb=$LOAD_MB idle=$BENCH_IDLE settle=$BENCH_SETTLE spawns=$BENCH_SPAWNS fresh_bins=$BENCH_FRESH_BINS"
  } >"$R/env.txt" 2>&1
  (cd "$BENCH_WS" && yolo config dump) >"$R/config-dump.txt" 2>&1 ||
    unmeasured "yolo config dump failed, so the effective config is not recorded"
}

# ---------------------------------------------------------------- fixtures
setup_fixtures() {
  if [ ! -d "$FIX/tree/.git" ]; then
    say "making a $BENCH_FILES-file git repository (once)"
    rm -rf "$FIX/tree"
    mkdir -p "$FIX/tree"
    (cd "$FIX/tree" && awk -v n="$BENCH_FILES" 'BEGIN {
        for (i = 0; i < n; i++) {
          d = sprintf("d%03d", int(i / 1000))
          if (i % 1000 == 0) system("mkdir -p " d)
          f = sprintf("%s/f%06d.txt", d, i)
          for (l = 0; l < 16; l++) printf "line %d of file %d: the quick brown fox jumps over the dog\n", l, i > f
          close(f)
        }
      }' && git init -q && git add -A &&
      git -c user.name=bench -c user.email=bench@example.invalid commit -q -m tree) ||
      die "could not make the synthetic repository"
  fi
  if [ ! -d "$FIX/yolo-jail/.git" ]; then
    say "cloning $BENCH_SRC_URL (once)"
    git clone -q "$BENCH_SRC_URL" "$FIX/yolo-jail" || die "git clone $BENCH_SRC_URL failed"
  fi
  git -C "$FIX/yolo-jail" checkout -q --detach "$BENCH_SRC_REF" || die "cannot check out $BENCH_SRC_REF"
  echo "src=$(git -C "$FIX/yolo-jail" rev-parse HEAD)" >>"$R/env.txt"
  if [ ! -f "$FIX/npm/package-lock.json" ]; then
    say "resolving the npm fixture's lockfile (once)"
    mkdir -p "$FIX/npm"
    cat >"$FIX/npm/package.json" <<'EOF'
{
  "name": "bench-npm",
  "version": "1.0.0",
  "private": true,
  "dependencies": {
    "@babel/core": "7.26.0",
    "eslint": "9.14.0",
    "jest": "29.7.0",
    "typescript": "5.6.3",
    "webpack": "5.96.1"
  }
}
EOF
    (cd "$FIX/npm" && npm install --package-lock-only --ignore-scripts --no-audit --no-fund --loglevel=error) ||
      die "npm could not resolve the fixture's lockfile"
  fi
  want=$((LOAD_MB * 1048576))
  if [ ! -f "$FIX/blob" ] || [ "$(wc -c <"$FIX/blob" | tr -d ' ')" -ne "$want" ]; then
    say "writing a $LOAD_MB MiB file for the file-cache load (once)"
    dd if=/dev/urandom of="$FIX/blob" bs=1048576 count="$LOAD_MB" 2>/dev/null || die "cannot write $FIX/blob"
  fi
  mkdir -p "$FIX/fresh"
  printf 'package main\n\nvar n string\n\nfunc main() {\n\tif len(n) == 0 {\n\t\tprintln("unset")\n\t}\n}\n' >"$FIX/fresh/main.go"
  write_env
  write_timer
  write_payload
}

write_env() {
  cat >"$CTL/env.sh" <<EOF
MARK=$MARK
OUT_ROOT=bench/results-$STAMP/out
RUNS=$BENCH_RUNS
BUILD_RUNS=$BENCH_BUILD_RUNS
JOBS=$JOBS
LOAD_MB=$LOAD_MB
SPAWNS=$BENCH_SPAWNS
FRESH_BINS=$BENCH_FRESH_BINS
CPU_ITERS=$BENCH_CPU_ITERS
STAMP=$STAMP
EOF
}

# The host-side clock: milliseconds to the first line holding the marker, and to exit.
write_timer() {
  cat >"$CTL/timer.js" <<'EOF'
// timer.js <result-file> <marker> <command> [args...]
// Writes "<ms until the marker appeared, or -1> <ms until exit> <exit code>" to
// <result-file>, and everything the command printed to <result-file>.log. When
// <result-file>.stop appears it ends the command: SIGTERM, then 10 s later SIGKILL and exit.
const { spawn } = require("child_process");
const fs = require("fs");
const [out, marker, cmd, ...args] = process.argv.slice(2);
const t0 = process.hrtime.bigint();
const ms = () => Number(process.hrtime.bigint() - t0) / 1e6;
const log = fs.openSync(out + ".log", "w");
let ready = -1;
let tail = "";
const watch = (d) => {
  fs.writeSync(log, d);
  if (ready < 0) {
    tail = (tail + d.toString()).slice(-4096);
    if (tail.includes(marker)) ready = ms();
  }
};
const child = spawn(cmd, args, { stdio: ["ignore", "pipe", "pipe"] });
child.stdout.on("data", watch);
child.stderr.on("data", watch);
let failed = false;
const stopper = setInterval(() => {
  if (!fs.existsSync(out + ".stop")) return;
  clearInterval(stopper);
  fs.writeSync(log, "\ntimer.js: stopped by the harness\n");
  child.kill("SIGTERM");
  setTimeout(() => {
    child.kill("SIGKILL");
    if (!failed) fs.writeFileSync(out, `${ready.toFixed(0)} ${ms().toFixed(0)} 143\n`);
    process.exit(0);
  }, 10000).unref();
}, 500);
child.on("error", (e) => {
  failed = true;
  clearInterval(stopper);
  fs.writeSync(log, String(e) + "\n");
  fs.writeFileSync(out, `-1 ${ms().toFixed(0)} 127\n`);
});
child.on("close", (code) => {
  clearInterval(stopper);
  if (!failed) fs.writeFileSync(out, `${ready.toFixed(0)} ${ms().toFixed(0)} ${code === null ? 128 : code}\n`);
});
EOF
}

# The in-jail half. Bash 3.2 or later: macOS's /bin/bash is 3.2.
write_payload() {
  cat >"$CTL/payload.sh" <<'EOF'
# payload.sh <phase> <backend>: runs in a jail, or on the host for the native control.
set -u
phase=$1 backend=$2
ws=$(pwd)
. "$ws/bench/ctl/env.sh"
echo "$MARK"
out=$ws/$OUT_ROOT/$backend
fix=$ws/bench/fixtures
ctl=$ws/bench/ctl
tmp=${TMPDIR:-/tmp}
mkdir -p "$out"
log=$out/payload.log
TIMEFORMAT=%3R
export GOTOOLCHAIN=local GOFLAGS=

seqn() { local i=0; while [ "$i" -le "$1" ]; do echo "$i"; i=$((i + 1)); done; }

# t <metric> <run> <command...>: time one run; append "metric run seconds rc" to times.tsv.
t() {
  local m=$1 r=$2 s rc
  shift 2
  s=$( { time "$@" >>"$log" 2>&1; } 2>&1 )
  rc=$?
  printf '%s\t%s\t%s\t%s\n' "$m" "$r" "$s" "$rc" >>"$out/times.tsv"
}

info() {
  {
    echo "backend=$backend"
    echo "uname=$(uname -a)"
    echo "nproc=$( (nproc || sysctl -n hw.ncpu || getconf _NPROCESSORS_ONLN) 2>/dev/null | head -1)"
    echo "workspace=$ws home=$HOME tmp=$tmp"
    echo "true=$(type -P true)"
    for c in bash git rg node npm go; do echo "$c=$(command -v "$c")"; done
    echo "git-version=$(git --version 2>&1)"
    echo "rg-version=$(rg --version 2>&1 | head -1)"
    echo "node-version=$(node --version 2>&1) npm-version=$(npm --version 2>&1)"
    echo "go-version=$(go version 2>&1)"
    echo "bash-version=$BASH_VERSION"
    if [ -r /proc/meminfo ]; then head -3 /proc/meminfo; fi
    echo "--- mounts"
    (cat /proc/mounts 2>/dev/null || mount) | awk '$2 ~ /^\/(workspace|home\/agent|home\/agent\/\.cache|tmp|mise)?$/ || $3 ~ /^\/(Users|private\/tmp)/'
    echo "--- df"
    df -k "$ws" "$tmp" 2>&1
  } >"$out/info.txt" 2>&1
}

hold() { while [ ! -e "$ctl/stop-hold" ]; do sleep 1; done; }

mem() {
  local d=$ctl/mem np tf
  state() { echo "$1" >"$d/state.tmp" && mv "$d/state.tmp" "$d/state"; }
  waitgo() { while [ ! -e "$d/go-$1" ]; do sleep 0.2; done; }
  state idle; waitgo idle
  # 1. anonymous memory, held by a child process until the host has sampled it
  node -e 'const fs = require("fs"); const [d, mb] = process.argv.slice(1);
    const b = Buffer.alloc(Number(mb) * 1048576, 1);
    fs.writeFileSync(d + "/anon-ready", String(b.length));
    const iv = setInterval(() => { if (fs.existsSync(d + "/anon-release") && b[0] === 1) clearInterval(iv); }, 200);' \
    "$d" "$LOAD_MB" >>"$log" 2>&1 &
  np=$!
  while [ ! -e "$d/anon-ready" ] && kill -0 "$np" 2>/dev/null; do sleep 0.2; done
  [ -e "$d/anon-ready" ] || echo failed >"$d/anon.failed"
  state anon-held; waitgo anon-held
  : >"$d/anon-release"; wait "$np"
  state anon-freed; waitgo anon-freed
  # 2. a scratch file: /tmp is RAM on Apple Container and disk on macOS
  tf=/tmp/bench-fill.$$
  if ! dd if=/dev/zero of="$tf" bs=1048576 count="$LOAD_MB" 2>"$d/dd.err"; then
    rm -f "$tf"; tf=$tmp/bench-fill.$$
    dd if=/dev/zero of="$tf" bs=1048576 count="$LOAD_MB" 2>>"$d/dd.err" || echo failed >"$d/dd.failed"
  fi
  echo "$tf" >"$d/dd.path"
  state tmp-held; waitgo tmp-held
  rm -f "$tf"
  state tmp-freed; waitgo tmp-freed
  # 3. the file cache: read a workspace file once
  cat "$fix/blob" >/dev/null
  state cache-read; waitgo cache-read
  # 4. ask the guest kernel for its caches back (Linux only; refused where /proc/sys is read-only)
  (sync; echo 3 >/proc/sys/vm/drop_caches) 2>"$d/drop.err"
  echo $? >"$d/drop.rc"
  state dropped; waitgo dropped
  state finished
}

rg_tree() { rg -j "$JOBS" -c 'zqxj_bench_nomatch_[0-9]{9}' "$fix/tree"; [ $? -le 1 ]; }
npm_ci() { (cd "$fix/npm" && npm ci --prefer-offline --no-audit --no-fund --ignore-scripts --loglevel=error); }
# go_pkgs: the packages with tests on both Linux and darwin, one per line. A package can have tests
# on one platform only, and with ./... the two platforms' pass counts could never be equal.
go_pkgs() {
  local f='{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}'
  (cd "$fix/yolo-jail" && GOOS=linux go list -f "$f" ./... && echo -- && GOOS=darwin go list -f "$f" ./...) |
    awk '$0 == "--" { d = 1; next } NF && !d { a[$0] = 1 } NF && d && ($0 in a)'
}
# go_test <run>: under env -i, as in CI, because the jail's own variables (YOLO_* and the rest)
# fail tests that pass on a clean machine. MISE_TRUSTED_CONFIG_PATHS: where go is a mise shim
# (macos-user), mise refuses the clone's untrusted mise.toml and no test runs.
go_test() {
  # shellcheck disable=SC2086 # GO_PKGS is a list of import paths, one word each
  (cd "$fix/yolo-jail" &&
    env -i PATH="$PATH" HOME="$HOME" USER="$(id -un)" TMPDIR="$tmp" GOTOOLCHAIN=local \
      MISE_TRUSTED_CONFIG_PATHS="$fix/yolo-jail" \
      go test -short -count=1 -p "$JOBS" $GO_PKGS >"$out/go-test.$1.txt" 2>&1)
}
io() {
  local r
  for r in $(seqn "$RUNS"); do t git_status "$r" git -c safe.directory='*' -C "$fix/tree" status --porcelain; done
  for r in $(seqn "$RUNS"); do t rg_tree "$r" rg_tree; done
  for r in $(seqn "$RUNS"); do rm -rf "$fix/npm/node_modules"; t npm_ci "$r" npm_ci; done
  rm -rf "$fix/npm/node_modules"
  GO_PKGS=$(go_pkgs 2>>"$log")
  if [ -z "$GO_PKGS" ]; then echo "go list named no packages, so go test runs ./..." >>"$log"; GO_PKGS=./...; fi
  printf '%s\n' "$GO_PKGS" >"$out/go-test.packages"
  for r in $(seqn "$BUILD_RUNS"); do t go_test "$r" go_test "$r"; done
}

# go_build <cache-dir> <jobs, or empty for the platform default>
go_build() {
  (cd "$fix/yolo-jail" || exit 1
    export GOCACHE=$1
    if [ -n "$2" ]; then
      GOMAXPROCS=$2 go build -p "$2" -buildvcs=false -o /dev/null ./cmd/yolo
    else
      go build -buildvcs=false -o /dev/null ./cmd/yolo
    fi)
}
cpu() {
  local r gc
  for r in $(seqn "$RUNS"); do
    t node_loop "$r" node -e "let x = 0; for (let i = 0; i < $CPU_ITERS; i++) { x = (x * 31 + i) | 0 } if (x === 42) console.log(x)"
  done
  for r in $(seqn "$BUILD_RUNS"); do
    gc=$(mktemp -d "$tmp/bench-gocache.XXXXXX"); t go_build_pinned "$r" go_build "$gc" "$JOBS"; rm -rf "$gc"
  done
  for r in $(seqn "$BUILD_RUNS"); do
    gc=$(mktemp -d "$tmp/bench-gocache.XXXXXX"); t go_build_all "$r" go_build "$gc" ""; rm -rf "$gc"
  done
}

spawn_loop() { local i=0; while [ "$i" -lt "$SPAWNS" ]; do "$1"; i=$((i + 1)); done; }
spawn() {
  local r k bins tb
  tb=$(type -P true)
  for r in $(seqn "$RUNS"); do t spawn_loop "$r" spawn_loop "$tb"; done
  bins=$(mktemp -d "$tmp/bench-fresh.XXXXXX")
  k=1
  while [ "$k" -le "$FRESH_BINS" ]; do
    (cd "$fix/fresh" && go build -buildvcs=false -ldflags "-X main.n=$STAMP-$backend-$k" -o "$bins/b$k" main.go) >>"$log" 2>&1
    t exec_first "$k" "$bins/b$k"
    t exec_again "$k" "$bins/b$k"
    k=$((k + 1))
  done
  rm -rf "$bins"
}

case $phase in
  info | hold | mem | io | cpu | spawn) "$phase" ;;
  ready) ;;
  *) echo "unknown phase $phase" >&2; exit 2 ;;
esac
EOF
}

# ---------------------------------------------------------------- launching
# launch <backend> <result-file> <command for bash -c> [timing]
# YOLO_TIMING=1 records every launch's spans in <workspace>/.yolo/host-perf.log and prints
# nothing. --timing prints them too, and on Apple Container runs an in-jail report after the
# command (two node starts and an awk over ~/.yolo-perf.log), inside the launch's time; so only
# the untimed warm-up passes it.
launch() {
  case $1 in
    container | macos-user)
      if [ "$1" = macos-user ]; then sudo -n -v 2>/dev/null || say "warning: sudo needs a password again"; fi
      if [ -n "${4:-}" ]; then
        (cd "$BENCH_WS" && YOLO_RUNTIME=$1 YOLO_TIMING=1 "$NODE" "$CTL/timer.js" "$2" "$MARK" \
          yolo run --timing --accept-config-changes -- bash -c "$3")
      else
        (cd "$BENCH_WS" && YOLO_RUNTIME=$1 YOLO_TIMING=1 "$NODE" "$CTL/timer.js" "$2" "$MARK" \
          yolo run --accept-config-changes -- bash -c "$3")
      fi
      ;;
    native)
      (cd "$BENCH_WS" && HOME=$NATIVE_HOME PATH=$NATIVE_PATH "$NODE" "$CTL/timer.js" "$2" "$MARK" bash -c "$3")
      ;;
  esac
}
launch_bg() { launch "$@" & BG_PID=$!; }
wait_bg() {
  if [ -n "$BG_PID" ]; then wait "$BG_PID" || true; fi
  BG_PID=
}
# end_bg <result-file> <what>: let the background launch end by itself for up to 300 s (a last
# session waits for its jail's teardown), then have timer.js stop it, which takes at most 10 s more
end_bg() {
  eb_i=0
  while [ -n "$BG_PID" ] && [ ! -s "$1" ] && [ "$eb_i" -lt 300 ]; do sleep 1; eb_i=$((eb_i + 1)); done
  if [ -n "$BG_PID" ] && [ ! -s "$1" ]; then
    unmeasured "$2 was still running 300 s after the harness let it go, so the harness stopped it (see $1.log)"
    : >"$1.stop"
  fi
  wait_bg
}
# payload_cmd <phase> <backend>: the bash -c string that runs the payload from the workspace
payload_cmd() {
  case $2 in
    container) echo "cd /workspace || exit 90; exec bash bench/ctl/payload.sh $1 $2" ;;
    *) echo "cd '$BENCH_WS' || exit 90; exec bash bench/ctl/payload.sh $1 $2" ;;
  esac
}

# wait_quiet <backend>: until no jail of that backend runs (at most 120 s)
wait_quiet() {
  wq_i=0
  while :; do
    case $1 in
      container) if [ -z "$(running_jails)" ]; then return 0; fi ;;
      macos-user) if [ "$(sandbox_procs)" -eq 0 ]; then return 0; fi ;;
      *) return 0 ;;
    esac
    wq_i=$((wq_i + 1))
    if [ "$wq_i" -gt 120 ]; then
      unmeasured "$1: a jail was still running 120 s after its last session ended (see $R/leftovers-$1.txt)"
      echo "=== $(date +%H:%M:%S)" >>"$R/leftovers-$1.txt"
      case $1 in
        container) running_jails >>"$R/leftovers-$1.txt"; echo >>"$R/leftovers-$1.txt" ;;
        macos-user) sandbox_list etime=,rss=,command= >>"$R/leftovers-$1.txt"; ignore_sandbox ;;
      esac
      return 0
    fi
    sleep 1
  done
}

# record_launch <backend> <kind> <run> <result-file>
record_launch() {
  if [ -s "$4" ]; then read -r rd tot rc <"$4"; else rd=-1 tot=-1 rc=-; fi
  awk -v b="$1" -v k="$2" -v r="$3" -v rd="$rd" -v t="$tot" -v rc="$rc" 'BEGIN {
    printf "%s\t%s\t%s\t%s\t%s\t%s\n", b, k, r, (rd < 0) ? "-" : rd / 1000, (t < 0) ? "-" : t / 1000, rc }' >>"$R/launch.tsv"
  if [ "$rc" != 0 ]; then say "$1 $2 launch $3 exited $rc; see $4.log"; fi
}

# The warm-up launch: one-time work (image delivery, captures, the darwin floor) lands here.
warmup() {
  say "$1: warm-up launch, which records the jail's tools and CPU count"
  wait_quiet "$1"
  launch "$1" "$R/warmup-$1" "$(payload_cmd info "$1")" timing
  record_launch "$1" warmup 0 "$R/warmup-$1"
  # A launch that fails here fails every later time too; better to stop now than after an hour
  wrc=-; if [ -s "$R/warmup-$1" ]; then wrc=$(awk '{ print $3 }' "$R/warmup-$1"); fi
  [ "$wrc" = 0 ] ||
    die "$1: the warm-up launch exited $wrc, and every later $1 launch would fail the same way; fix what $R/warmup-$1.log names, then run again"
  wait_quiet "$1"
}

launch_phase() {
  [ "$1" != native ] || return 0
  lp_i=1
  while [ "$lp_i" -le "$BENCH_RUNS" ]; do
    sleep 5
    say "$1: fresh launch $lp_i of $BENCH_RUNS"
    launch "$1" "$R/fresh-$1-$lp_i" "$MARK_CMD"
    record_launch "$1" fresh "$lp_i" "$R/fresh-$1-$lp_i"
    wait_quiet "$1"
    lp_i=$((lp_i + 1))
  done
  if [ "$1" = container ]; then
    rm -f "$CTL/stop-hold"
    launch_bg container "$R/hold-container" "$(payload_cmd hold container)"
    lp_i=0
    until grep -q "$MARK" "$R/hold-container.log" 2>/dev/null; do
      lp_i=$((lp_i + 1))
      if [ "$lp_i" -gt 600 ] || [ -s "$R/hold-container" ]; then break; fi
      sleep 1
    done
    if grep -q "$MARK" "$R/hold-container.log" 2>/dev/null; then
      lp_i=1
      while [ "$lp_i" -le "$BENCH_ATTACH_RUNS" ]; do
        sleep 2
        launch container "$R/attach-$lp_i" "$MARK_CMD"
        record_launch container attach "$lp_i" "$R/attach-$lp_i"
        lp_i=$((lp_i + 1))
      done
    else
      unmeasured "container: the holding session never became ready, so attach is not measured (see $R/hold-container.log)"
    fi
    : >"$CTL/stop-hold"
    end_bg "$R/hold-container" "container: the holding session"
    wait_quiet container
  fi
  mkdir -p "$R/perf-$1"
  cp "$BENCH_WS/.yolo/host-perf.log" "$R/perf-$1/" 2>/dev/null || true
  # Apple Container binds <workspace>/.yolo/home whole at /home/agent, so the jail's ~/.yolo-perf.log is here
  if [ "$1" = container ]; then cp "$BENCH_WS/.yolo/home/.yolo-perf.log" "$R/perf-$1/jail-perf.log" 2>/dev/null || true; fi
}

# ---------------------------------------------------------------- memory
to_mib() { awk '{ v = $1; sub(/[+-]$/, "", v); u = substr(v, length(v)); n = v + 0
  if (u == "K") n /= 1024; else if (u == "G") n *= 1024; else if (u == "B") n /= 1048576
  printf "%d", n }'; }

# sample <backend> <label>: one row of host-side memory readings. The raw vm_stat and top
# output goes to raw-memory.txt, so a figure read from a format this did not expect can be checked.
sample() {
  raw=$(vm_stat 2>&1 || true)
  printf '=== %s %s %s\n%s\n' "$1" "$2" "$(date +%H:%M:%S)" "$raw" >>"$R/raw-memory.txt"
  vm=$(printf '%s\n' "$raw" | awk '
    function mib(x) { return (x == "") ? "-" : sprintf("%d", x * m) }
    NR == 1 { if (match($0, /[0-9]+ bytes/)) ps = substr($0, RSTART, RLENGTH) + 0 }
    /^Pages free:/ { free = $NF + 0 } /^Pages speculative:/ { spec = $NF + 0 }
    /^Pages wired down:/ { wired = $NF + 0 } /^Pages occupied by compressor:/ { comp = $NF + 0 }
    /^Anonymous pages:/ { anon = $NF + 0 } /^File-backed pages:/ { file = $NF + 0 }
    END { if (!ps) { print "-\t-\t-\t-\t-"; exit }
      m = ps / 1048576
      printf "%s\t%s\t%s\t%s\t%s", (free == "") ? "-" : mib(free + spec), mib(wired), mib(comp), mib(anon), mib(file) }')
  [ -n "$vm" ] || vm=$(printf -- '-\t-\t-\t-\t-')
  mp=$(sysctl -n kern.memorystatus_level 2>/dev/null || true)
  [ -n "$mp" ] || mp=-
  rss=- mem=- helpers=-
  case $1 in
    container)
      if [ -n "$VM_PID" ] && kill -0 "$VM_PID" 2>/dev/null; then
        rss=$(ps -o rss= -p "$VM_PID" | awk '{ printf "%d", $1 / 1024 }')
        topout=$(top -l 1 -pid "$VM_PID" -stats mem 2>&1 | tail -1 | tr -d ' ')
        printf 'top: %s\n' "$topout" >>"$R/raw-memory.txt"
        # top prints MEM in at most four digits and a unit: from 10000 MiB up it is whole GiB, too
        # coarse against the load, and a VM gone before top looked leaves the bare "MEM" header.
        # Either way the row has no footprint, and held() uses the resident column for that pair.
        case $topout in
          *[0-9][BKM] | *[0-9][BKM][+-]) mem=$(printf '%s\n' "$topout" | to_mib) ;;
          *) mem=- ;;
        esac
      fi
      helpers=$(ps -axo rss=,comm= | awk '/container-(apiserver|runtime-linux|core-images|network-vmnet)/ { s += $1 } END { printf "%d", s / 1024 }')
      ;;
    macos-user)
      rss=$(sandbox_list | awk '{ s += $3 } END { printf "%d", s / 1024 }')
      ;;
  esac
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$1" "$2" "$vm" "$mp" "${rss:--}" "${mem:--}" "$helpers" >>"$R/memory.tsv"
}

# find_vm_pid: the one Virtualization process that appeared since procs-before was taken
find_vm_pid() {
  ps -axo pid=,rss=,comm= >"$R/procs-after-container.txt"
  VM_PID=$(awk 'NR == FNR { seen[$1] = 1; next } !($1 in seen) && /Virtualization/ { print $1 }' \
    "$R/procs-before-container.txt" "$R/procs-after-container.txt")
  n=$(printf '%s\n' "$VM_PID" | awk 'NF { n++ } END { print n + 0 }')
  if [ "$n" -ne 1 ]; then
    unmeasured "container: $n new Virtualization processes appeared, so the VM's own footprint is not attributed (see procs-*.txt)"
    VM_PID=
  fi
  echo "vm_pid=${VM_PID:--}" >>"$R/env.txt"
}

# wait_state <state> <timeout-s>: wait for the memory session to report a state
wait_state() {
  ws_i=0
  while [ "$(cat "$CTL/mem/state" 2>/dev/null || true)" != "$1" ]; do
    if [ -s "$R/mem-$B" ]; then return 1; fi
    ws_i=$((ws_i + 1))
    if [ "$ws_i" -gt $(($2 * 5)) ]; then return 1; fi
    sleep 0.2
  done
}
go_on() { : >"$CTL/mem/go-$1"; }
# release_mem: let a session the harness gave up on run through every state to its end, instead of
# waiting in the jail for a go-<state> file nothing would write
release_mem() { for s in idle anon-held anon-freed tmp-held tmp-freed cache-read dropped; do go_on "$s"; done; }
settle() { # settle <label>: sample at once, after 10 s, and after BENCH_SETTLE s
  sample "$B" "$1+0s"; sleep 10
  sample "$B" "$1+10s"; sleep $((BENCH_SETTLE - 10))
  sample "$B" "$1+${BENCH_SETTLE}s"
}

memory_phase() {
  B=$1
  [ "$B" != native ] || return 0
  rm -rf "$CTL/mem"
  mkdir -p "$CTL/mem"
  wait_quiet "$B"
  ps -axo pid=,rss=,comm= >"$R/procs-before-$B.txt"
  VM_PID=
  sample "$B" before
  say "$B: memory session (idle, then three $LOAD_MB MiB loads, each followed by $BENCH_SETTLE s)"
  launch_bg "$B" "$R/mem-$B" "$(payload_cmd mem "$B")"
  if ! wait_state idle 900; then
    unmeasured "$B: the memory session did not start (see $R/mem-$B.log)"
    release_mem
    end_bg "$R/mem-$B" "$B: the memory session"
    return 0
  fi
  if [ "$B" = container ]; then find_vm_pid; fi
  sleep "$BENCH_IDLE"
  sample "$B" idle
  go_on idle
  for st in anon-held anon-freed tmp-held tmp-freed cache-read dropped; do
    if ! wait_state "$st" 900; then
      unmeasured "$B: the memory session stopped before $st (see $R/mem-$B.log)"
      release_mem
      break
    fi
    case $st in
      *-held) sample "$B" "$st" ;;
      dropped) sleep 10; sample "$B" "dropped+10s" ;;
      *) settle "$st" ;;
    esac
    go_on "$st"
  done
  for f in anon.failed dd.failed; do
    if [ -e "$CTL/mem/$f" ]; then unmeasured "$B: the ${f%.failed} load failed (see $CTL/mem)"; fi
  done
  for f in dd.path dd.err drop.rc drop.err; do cp "$CTL/mem/$f" "$R/out/$B-$f" 2>/dev/null || true; done
  end_bg "$R/mem-$B" "$B: the memory session"
  wait_quiet "$B"
  sleep 10
  sample "$B" "exited+10s"
}

# ---------------------------------------------------------------- the rest
payload_phase() { # payload_phase <backend> <phase>
  wait_quiet "$1"
  say "$1: $2"
  launch "$1" "$R/$2-$1" "$(payload_cmd "$2" "$1")"
  rc=-
  if [ -s "$R/$2-$1" ]; then rc=$(awk '{ print $3 }' "$R/$2-$1"); fi
  if [ "$rc" != 0 ]; then say "$1 $2 exited $rc; see $R/$2-$1.log and $R/out/$1/payload.log"; fi
}

disk_phase() {
  : >"$R/disk.tsv"
  add() { printf '%s\t%s\t%s\n' "$1" "$2" "${3:--}" >>"$R/disk.tsv"; }
  kib() { du -sk "$1" 2>/dev/null | awk '{ print $1 }'; }
  for b in $BENCH_BACKENDS; do
    case $b in
      container)
        add container "Apple Container data root (du)" "$(kib "$HOME/Library/Application Support/com.apple.container")"
        container system df >"$R/container-system-df.txt" 2>&1 ||
          unmeasured "container system df failed; only the du figure is recorded"
        ;;
      macos-user)
        if [ -e "$FLOOR" ] && has nix; then
          add macos-user "darwin floor closure (nix path-info -S)" \
            "$(nix --extra-experimental-features nix-command path-info -S "$(readlink "$FLOOR")" 2>/dev/null | awk '{ printf "%d", $2 / 1024 }')"
        else
          unmeasured "macos-user: no darwin floor link at $FLOOR, or no nix, so the floor's size is not recorded"
        fi
        add macos-user "sandbox home /Users/_yolojail (sudo du)" "$(sudo -n du -sk /Users/_yolojail 2>/dev/null | awk '{ print $1 }')"
        add macos-user "sandbox state /var/yolo-jail (sudo du)" "$(sudo -n du -sk /var/yolo-jail 2>/dev/null | awk '{ print $1 }')"
        ;;
    esac
  done
  add both "workspace state <workspace>/.yolo" "$(kib "$BENCH_WS/.yolo")"
  add both "yolo machine state ~/.local/share/yolo-jail" "$(kib "$HOME/.local/share/yolo-jail")"
}

# ---------------------------------------------------------------- report
stats() { sort -n | awk 'NF { a[++n] = $1 } END {
  if (!n) { print "0 - - -"; exit }
  m = (n % 2) ? a[(n + 1) / 2] : (a[n / 2] + a[n / 2 + 1]) / 2
  printf "%d %.3f %.3f %.3f\n", n, m, a[1], a[n] }'; }
# gosig <backend> <run>: "<ok> <FAIL>", the packages that go test run passed and failed; empty if none passed
gosig() {
  awk '$1 == "ok" { o++ } $1 == "FAIL" && NF > 1 { f++ } END { if (o) printf "%d %d\n", o, f + 0 }' \
    "$R/out/$1/go-test.$2.txt" 2>/dev/null || true
}
# gomode <backend>: the "<ok> <FAIL>" that most of the backend's timed go test runs share
gomode() {
  awk -F'\t' '$1 == "go_test" && $2 != "0" { print $2 }' "$R/out/$1/times.tsv" 2>/dev/null |
    while read -r gr; do gosig "$1" "$gr"; done | sort | uniq -c | sort -rn | awk 'NR == 1 { print $2, $3 }'
}
values() { # values <backend> <metric>
  case $2 in
    go_test) # the runs with the backend's usual package counts, whatever their exit code
      gm=$(gomode "$1")
      if [ -n "$gm" ]; then
        awk -F'\t' '$1 == "go_test" && $2 != "0" { print $2, $3 }' "$R/out/$1/times.tsv" 2>/dev/null |
          while read -r gr gs; do if [ "$(gosig "$1" "$gr")" = "$gm" ]; then echo "$gs"; fi; done
      fi
      ;;
    fresh_ready | fresh_total | attach_ready | attach_total)
      c=4
      if [ "${2#*_}" = total ]; then c=5; fi
      awk -F'\t' -v b="$1" -v k="${2%_*}" -v c="$c" '$1 == b && $2 == k && $6 == "0" { print $c }' "$R/launch.tsv" 2>/dev/null || true
      ;;
    *) awk -F'\t' -v m="$2" '$1 == m && $2 != "0" && $4 == "0" { print $3 }' "$R/out/$1/times.tsv" 2>/dev/null || true ;;
  esac
}
fmt() { echo "$1" | awk '{ if ($1 == 0) print "-"; else printf "%s (%s-%s), n=%s", $2, $3, $4, $1 }'; }
# verdict "<n med min max>" "<n med min max>": container first, macos-user second
verdict() { echo "$1 $2" | awk '{
  if ($1 < 3 || $5 < 3) { print "too few runs"; exit }
  lo = ($2 < $6) ? $2 : $6; hi = ($2 < $6) ? $6 : $2
  if (lo <= 0) { print "-"; exit }
  apart = ($4 < $7 || $8 < $3)
  if ((hi - lo) / lo >= 0.10 && apart) printf "%s faster, x%.2f\n", ($2 < $6) ? "container" : "macos-user", hi / lo
  else print "no difference shown" }'; }
row() {
  sc=$(values container "$1" | stats); sm=$(values macos-user "$1" | stats); sn=$(values native "$1" | stats)
  v=$(verdict "$sc" "$sm")
  if [ "$1" = go_test ]; then
    gc=$(gomode container); gu=$(gomode macos-user)
    if [ -n "$gc" ] && [ -n "$gu" ] && [ "$gc" != "$gu" ]; then
      v="not compared: packages ok/FAIL differ ($(echo "$gc" | tr ' ' /) against $(echo "$gu" | tr ' ' /))"
    fi
  fi
  printf '| `%s` | %s | %s | %s | %s |\n' "$1" "$(fmt "$sc")" "$(fmt "$sm")" "$(fmt "$sn")" "$v"
}
# held <backend> <after-label> <compared-with>: MiB the backend still holds over a baseline. It
# compares footprint with footprint when both rows have one, else resident with resident.
held() { awk -F'\t' -v b="$1" -v a="$2" -v z="$3" -v ld="$LOAD_MB" '
  $1 == b && $2 == a { xr = $9; xf = $10 } $1 == b && $2 == z { yr = $9; yf = $10 }
  END { if (xf != "" && xf != "-" && yf != "" && yf != "-") { x = xf; y = yf; w = "footprint" }
    else { x = xr; y = yr; w = "resident" }
    if (x == "" || y == "" || x == "-" || y == "-") { print "-"; exit }
    d = x - y; p = 100 * d / ld
    printf "%d MiB of %s (%d%% of the load): %s", d, w, p, (p >= 50) ? "held" : (p <= 10) ? "returned" : "partly held" }' "$R/memory.tsv" 2>/dev/null || echo -; }

report() {
  f=$R/results.md
  {
    echo "# macOS backend benchmark, $STAMP"
    echo
    echo '```text'
    cat "$R/env.txt"
    echo '```'
    echo
    echo "## Timings"
    echo
    echo "Seconds: median (min-max) over the timed runs that exited 0; warm-up runs are excluded. A"
    echo "verdict needs three runs on each side, medians at least 10% apart, and ranges that do not overlap."
    echo "go_test counts its timed runs by package instead, whatever their exit code: the runs with the"
    echo "ok and FAIL counts most of that backend's runs share (listed below), and it compares two"
    echo "backends only when those counts are equal."
    echo
    echo "| Metric | container | macos-user | native | container against macos-user |"
    echo "|---|---|---|---|---|"
    for m in fresh_ready fresh_total attach_ready attach_total git_status rg_tree npm_ci go_test \
      node_loop go_build_pinned go_build_all spawn_loop exec_first exec_again; do
      row "$m"
    done
    echo
    echo "Runs that exited non-zero, left out of the table unless go_test's rule counts them:"
    echo
    echo '```text'
    awk -F'\t' '$6 != "0" { print "launch", $0 }' "$R/launch.tsv" 2>/dev/null || true
    for b in container macos-user native; do
      awk -F'\t' -v b="$b" '$4 != "0" { print b, $0 }' "$R/out/$b/times.tsv" 2>/dev/null || true
    done
    echo '```'
    echo
    echo "Warm-up launches (one-time work included):"
    echo
    echo '```text'
    awk -F'\t' '$2 == "warmup"' "$R/launch.tsv" 2>/dev/null || true
    echo '```'
    echo
    echo "go test results per run (packages ok / FAIL; run 0 is the warm-up):"
    echo
    echo '```text'
    for b in container macos-user native; do
      for g in "$R/out/$b"/go-test.*.txt; do
        if [ -f "$g" ]; then
          printf '%s %s %s\n' "$b" "$(basename "$g")" \
            "$(awk '$1 == "ok" { o++ } $1 == "FAIL" && NF > 1 { f++ } END { printf "ok=%d FAIL=%d", o, f }' "$g")"
        fi
      done
    done
    echo '```'
    echo
    echo "## Memory"
    echo
    echo "MiB, read from the host. For container, resident and footprint are the VM process's own (ps"
    echo "RSS, top's MEM); for macos-user, resident is the summed RSS of the _yolojail processes. free to"
    echo "file-backed are vm_stat's; memorystatus level is sysctl kern.memorystatus_level. Each held"
    echo "figure below compares footprint where both of its rows have one, else resident, and says which."
    echo
    echo "| Backend | When | free | wired | compressed | anonymous | file-backed | memorystatus level | resident | footprint | container helpers |"
    echo "|---|---|---|---|---|---|---|---|---|---|---|"
    awk -F'\t' '{ printf "| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n", $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11 }' "$R/memory.tsv" 2>/dev/null || true
    echo
    echo "Still held $BENCH_SETTLE s after each load ended, against the idle reading:"
    echo
    for b in container macos-user; do
      for l in anon-freed tmp-freed cache-read; do
        echo "- $b, $l: $(held "$b" "$l+${BENCH_SETTLE}s" idle)"
      done
      echo "- $b, after the drop-caches step, which only a Linux guest can take: $(held "$b" dropped+10s idle)"
    done
    echo
    echo "## Disk"
    echo
    echo "| Backend | What | KiB |"
    echo "|---|---|---|"
    awk -F'\t' '{ printf "| %s | %s | %s |\n", $1, $2, $3 }' "$R/disk.tsv" 2>/dev/null || true
    echo
    echo "## Not measured in this run"
    echo
    if [ -s "$R/unmeasured.txt" ]; then cat "$R/unmeasured.txt"; else echo "- nothing beyond what the doc lists"; fi
  } >"$f"
  say "results: $f"
}

# ---------------------------------------------------------------- main
preflight
record_env
setup_fixtures
for b in $BENCH_BACKENDS; do
  if [ "$b" != native ]; then warmup "$b"; fi
done
# The control runs macos-user's own darwin tools, which its warm-up launch has just built.
if [ -d "$FLOOR/bin" ]; then NATIVE_PATH=$FLOOR/bin:/usr/bin:/bin:/usr/sbin:/sbin; else NATIVE_PATH=$PATH; fi
if [ -z "$BENCH_JOBS" ] && [ -f "$R/out/container/info.txt" ]; then
  JOBS=$(awk -F= '$1 == "nproc" { print $2 }' "$R/out/container/info.txt")
  [ -n "$JOBS" ] || JOBS=2
  write_env
fi
echo "jobs=$JOBS" >>"$R/env.txt"
if in_list native "$BENCH_BACKENDS" && [ ! -d "$FLOOR/bin" ]; then
  unmeasured "native: no darwin floor at $FLOOR, so the control ran the host's own tools, not macos-user's"
fi
for p in launch memory io cpu spawn; do
  in_list "$p" "$BENCH_PHASES" || continue
  for b in $BENCH_BACKENDS; do
    case $p in
      launch) launch_phase "$b" ;;
      memory) memory_phase "$b" ;;
      *) payload_phase "$b" "$p" ;;
    esac
  done
done
if in_list disk "$BENCH_PHASES"; then disk_phase; fi
report
````
