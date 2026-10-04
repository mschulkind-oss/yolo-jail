---
status: current
verified: 2026-09-09
verified_commit: d14bdab7
covers:
  - internal/macosuser/
  - internal/darwinpkg/
  - internal/containerbuilder/
  - internal/config/validate.go
tags: [macos, backends, nix, decision, packages, builder]
summary: "The standing macOS direction: runtime, builder and packages are three orthogonal axes, and the two macOS backends compose into one product rather than competing — macos-user as the fast native path and the intended default (explicit opt-in today; auto-detection still picks Apple Container), an Apple Container cell as the fallback that needs real Linux. Includes the acceptance bar that separates a yolo backend from a sandbox wrapper, why Colima is refused, and what each macOS path measured, performance first: macos-user native on files and memory, the VM backends faster at starting processes and slower on shared files."
---

# The macOS direction — three axes, one composed product

**Status:** CURRENT as of 2026-09-09, verified against `d14bdab7`.

yolo ships **two** macOS paths and they are not competing backends: `macos-user` is the fast
native one, and an Apple Container cell is the fallback for what native darwin cannot cover.
This document is the standing decision behind that shape, and the vocabulary that keeps the
recurring argument from restarting.

> [!IMPORTANT]
> **"Native default" here is the DIRECTION, not today's selection.** Auto-detection on macOS
> still tries `container` then `podman`, and `macos-user` is reachable only by naming it —
> `runtime: "macos-user"` or `YOLO_RUNTIME=macos-user` — because it lives in
> `paths.NativeRuntimes` rather than `paths.SupportedRuntimes` and is deliberately never
> probed. So the *shipped* macOS default is Apple Container. Everything below that calls the
> native path "the default" is stating where the default is going, and flipping the
> auto-detect order is the step that has not been taken.

**The problem it answers.** On Linux a jail starts in seconds and just works. On macOS every
container runtime interposes a **Linux VM**: slow to start, a RAM ceiling you have to guess
ahead of time, that RAM permanently held while it runs, plus the whole class of VM problems —
disk-image growth, a filesystem boundary over the workspace, daemon lifecycle. The goal is a
macOS path as fast and convenient as Linux, with **no VM and no RAM pre-commitment**, without
throwing away the things that make this yolo rather than a sandbox wrapper.

**Measured since:** both macOS paths ran the same benchmark on one Mac on 2026-10-02 and 2026-10-03; [What each macOS path costs, measured](#what-each-macos-path-costs-measured) gives the results dimension by dimension.

| Component | Lives in |
| :--- | :--- |
| The native, no-VM backend | `internal/macosuser` |
| Native darwin package realization (the acceptance bar) | `internal/darwinpkg` |
| The on-demand Linux builder for the container runtimes | `internal/containerbuilder` |
| The runtime enumeration and its refusals | `internal/config` (`validate.go`) |

**Reads with:** [`macos-user-nix-and-features.md`](macos-user-nix-and-features.md) (that
backend as built), [`nix-across-backends.md`](nix-across-backends.md) (what nix produces for
each), [`fill-the-matrix-principle.md`](fill-the-matrix-principle.md) (one path per
matrix cell), [`../guides/macos.md`](../../userguide/guides/macos.md) (user-facing setup).

---

## What each macOS path costs, measured

**As of 2026-10-04.** Every figure but the cold-first-launch row comes from one Mac, and each
links to the run that produced it; [Caveats](#caveats) says what that Mac and those runs were.
This section reports. The rulings in this document are unchanged, and
[What the numbers bear on](#what-the-numbers-bear-on) names the two sentences of theirs that the
numbers bear on: they contradict the warning, and parts of the problem statement.

**Labels.** **MEASURED** is a recorded run, through yolo unless it says *without yolo*.
**SOURCED** is read in a named outside source, **READ** in this repository's code or docs.
**INFERRED** is reasoned and not observed, and **not measured** means nobody has run it. A **fresh
launch** starts a workspace's jail and an **attach** enters one already running
([the benchmark's terms](../research/macos-backend-performance.md#terms)). A **shared folder** is a
Mac folder shown inside the VM through virtiofs; a **VM-local disk** is a disk image the guest
formats and serves itself. **VZ** is Apple's Virtualization.framework, which Apple Container and
Podman's applehv provider run on, and **libkrun** is the library behind Podman's other provider
([the runtime comparison's terms](../research/macos-vm-runtime-comparison.md#terms)). M1 to M13
are [the benchmark's metrics](../research/macos-backend-performance.md#42-the-metrics).

**The answer, performance first.** `macos-user` is the Mac at native speed, and memory it used
comes back; it pays macOS's cost of starting processes, `sudo`, and a fresh launch for
every terminal. A VM backend is Linux: fast at starting processes and on its own disk, slow on
every file it shares with the Mac, and it holds memory while its VM runs. So which is faster
depends on how much of the work touches the shared folders.

### Dimension by dimension

MEASURED unless the cell says otherwise, each backend against the same Mac running the same work
natively on the same day.

| Dimension | `macos-user` | Apple Container | Podman Machine | Source |
| :--- | :--- | :--- | :--- | :--- |
| Fresh launch, to a prompt | **5.5 s**, with `sudo` already authorized | 6.9 s with auto-capture off; 67.3 s as run, [a defect](#what-will-change) | not measured on Apple silicon | [M1, Apple Container](../research/macos-backend-performance.md#launch-m1-to-m3); [M1, macos-user](../research/macos-backend-performance.md#macos-user-2026-10-03) |
| A second terminal in the same workspace | another fresh launch and `sudo` again; INFERRED from M1's 5.5 s, not timed beside a running jail | **an attach, 1.8 s** | an attach; not measured | [M3](../research/macos-backend-performance.md#launch-m1-to-m3); `sudo` per launch: READ, [the user guide](../../userguide/guides/macos.md#the-macos-user-backend) |
| Workspace file work: `git status`, ripgrep and `npm ci` over a 100,000-file tree | **native** | 5.3, 5.0 and 3.2 times native | *without yolo*: applehv within about 25% of Apple Container; libkrun 1.6 to 9 times slower than VZ on all but one step | [M5 to M7](../research/macos-backend-performance.md#timings-m5-to-m12); [on a shared folder](../research/macos-vm-runtime-comparison.md#32-on-a-shared-mac-folder) |
| Folders only the jail uses (`.venv`, `node_modules`, `~/.cache`, the home), where yolo puts them today | the Mac's own disk; the Mac and the sandbox share `.venv` and `node_modules` | READ: shared folders, at the workspace's cost; only `/mise` is a VM-local disk, and scratch is RAM | READ: shared folders; `/mise` and scratch are on the VM's disk | [Apple Container's mounts](../../internal/cli/run/assemble_parts.go#L59-L71); [Podman's](../../internal/cli/run/assemble_parts.go#L139-L205) and [its scratch](../../internal/cli/run/runmount.go#L34-L56); [the jail's own copies](../../internal/cli/run/mounts.go#L124-L127) |
| One-thread CPU | native | native | not measured | [M9](../research/macos-backend-performance.md#timings-m5-to-m12) |
| Parallel `go build`, and the default share of cores | native, every core | 16% slower on 5 threads each, and 21% slower at defaults (MEASURED); READ: the default is half the cores | the machine's CPUs; not measured | [M10](../research/macos-backend-performance.md#timings-m5-to-m12); [the default](../../internal/cli/run/backendcaps.go#L257-L268) |
| Process start: 2,000 execs, then a new binary's first exec | native: 9.5 s, then 0.38 s | **6.7 times faster than native** (1.3 s), then **about 400 times** (0.001 s) | not measured; INFERRED like Apple Container, being a Linux guest | [M11, M12](../research/macos-backend-performance.md#timings-m5-to-m12) |
| Memory after a 2 GiB load ends | **returned**: 0 to 2% still held 120 s later | held: 104% still charged 120 s later, until the jail stops | *without yolo*: held, on both providers | [M4](../research/macos-backend-performance.md#memory-m4-apple-container); [macos-user](../research/macos-backend-performance.md#macos-user-2026-10-03); [without yolo](../research/macos-vm-runtime-comparison.md#4-memory-does-a-vm-give-a-freed-2-gib-back) |
| Idle memory | recorded on the Mac, not copied into the repository (`results-20261003-131315/memory.tsv`); no VM holds memory for it | the VM's footprint 878 MiB and resident size 1,062 MiB under a 16 GiB cap; READ: the default cap is half of host RAM, at least 4 GB | *without yolo*: one VM for every jail, resident 1,540 MiB on libkrun and 1,636 MiB on applehv | [M4](../research/macos-backend-performance.md#memory-m4-apple-container); [the cap](../../internal/cli/run/helpers.go#L174-L199); [without yolo](../research/macos-vm-runtime-comparison.md#4-memory-does-a-vm-give-a-freed-2-gib-back) |
| Several jails at once | not measured | **no**: a second workspace's jail is refused on `container` 1.1.0 | not measured; one VM serves every jail | [the two-jail check](../research/macos-backend-performance.md#7-found-on-the-way-two-apple-container-jails-may-mount-one-ext4-disk) |
| `sudo` | at every launch | never | never | READ, [the user guide](../../userguide/guides/macos.md#the-macos-user-backend); [side by side](../research/macos-backend-performance.md#the-two-backends-side-by-side) |
| Disk | the [darwin floor](macos-user-provisioning.md#the-floor)'s closure 2.2 GiB, the sandbox home 566 MiB, `/var/yolo-jail` 58 MiB | the data root 26.0 GiB, where `container system df` counts 26.72 GB of images, 93% reclaimable | not measured | [M13](../research/macos-backend-performance.md#disk-m13); [macos-user](../research/macos-backend-performance.md#macos-user-2026-10-03) |
| Cold first launch | 73.61 s on a hosted CI runner; never recorded on a developer Mac | machine-cold not measured; a first image delivery into a warm content store 45 s, a `packages:` change 30 s | on GitHub's Intel runner: a first delivery 18 min 6 s, a `packages:` change 1 min 46 s | [CI logs](../research/macos-backend-performance.md#26-what-ci-logs-already-hold); [image delivery](../research/macos-layer-reusing-image-delivery.md#mac-results-2026-09-25) |

yolo's machine state (`~/.local/share/yolo-jail`), which both backends use, is in neither Disk cell:
it came to 2.3 GiB in the 2026-10-02 run ([M13](../research/macos-backend-performance.md#disk-m13)).

### What holds even when both work

- **A shared folder's per-file cost is VZ's, and VZ has no setting for it.** Without yolo, Apple
  Container and Podman's applehv provider cost the same within about 25% on every row
  (MEASURED, [on a shared folder](../research/macos-vm-runtime-comparison.md#32-on-a-shared-mac-folder)),
  and VZ's virtio-fs device takes only a folder and a tag (SOURCED,
  [the open-stack survey](../research/macos-vm-runtime-comparison.md#6-is-there-an-open-stack-with-faster-shared-folders)).
  No open stack tried beat it: libkrun with complete permission semantics tied it, and QEMU with a
  macOS virtiofsd port was slower even at its most aggressive caching (MEASURED without yolo, the
  same section); NFS from macOS's own `nfsd`, which needs `sudo`, is untried.
  Per-file operations there cost 12 to 115 times what they cost on the VM's own disk (MEASURED,
  [per-file cost](../research/apple-container-file-cost.md#21-per-file-cost-not-bandwidth)). So the
  workspace, which the Mac must see, stays slower on either VZ backend whatever yolo does with the
  other folders.
- **Apple Container's and Podman Machine's VMs hold the memory they touched while they run**:
  on Apple Container until the jail stops (MEASURED, M4), and on Podman Machine, whose one VM
  serves every jail, until the machine stops (INFERRED). VZ offers no free page reporting (SOURCED,
  [what VZ offers](../research/macos-vm-memory-reclaim.md#3-what-virtualizationframework-offers)),
  Apple Container attaches no balloon (MEASURED by a source search,
  [the benchmark's §2.2, item 4](../research/macos-backend-performance.md#22-memory-backed-on-first-touch-kept-until-the-container-stops)),
  and libkrun's reporting returned nothing in the one run here (MEASURED without yolo,
  [memory](../research/macos-vm-runtime-comparison.md#4-memory-does-a-vm-give-a-freed-2-gib-back)).
  The one VM measured that gave memory back, OrbStack's, is not a yolo backend (MEASURED without
  yolo, the same run).
- **Starting processes favors the Linux guest, until its cause is known.** XProtect's first-run
  scan is the candidate (SOURCED,
  [process start](../research/macos-backend-performance.md#25-process-start-new-binaries-networking)),
  and CrowdStrike Falcon was active on the Mac; neither has been separated from macOS's own cost.
- **Re-entry.** A VM backend attaches a second terminal to the running jail. `macos-user` has no
  attach, so every terminal is a fresh launch and a `sudo` (READ,
  [the benchmark's terms](../research/macos-backend-performance.md#terms)).
- **Linux against darwin.** A VM jail runs the Linux image; `macos-user` runs darwin builds of
  the same package names on BSD tools, and a package with no darwin build stops the launch
  ([the core tension](#the-core-tension-stated-plainly)).
- **The grade of isolation**: a VM against Seatbelt and a separate account
  ([the core tension](#the-core-tension-stated-plainly)).

### What will change

Defects and unbuilt work behind some of the numbers above:

- **Apple Container's auto-capture retry**, 61 s of every fresh launch and 14 s of every attach
  as run. Auto-capture records an agent's install the first time a machine launches that agent,
  and on Apple Container it failed and ran again at every launch. Fixed on main on 2026-10-03
  ([OQ-PD24 to OQ-PD26](../design/program-delivery.md#decision-ledger)), after release 0.11.1, and
  not re-measured on a Mac yet ([the launch run](../research/macos-backend-performance.md#launch-m1-to-m3)).
- **Apple Container's no-op nix build at every launch**, 3.2 to 3.5 s of the 6.9 s (MEASURED). That
  backend never gets the
  [stock tag](image-staging-vs-baking.md#the-stock-tag-and-the-question-asked-before-the-build),
  the tag that lets a launch skip the build when the image it wants is already loaded, so it
  builds even when nothing changed (READ, [stockimage.go](../../internal/image/stockimage.go#L362-L370)). The skip podman takes covers only
  a workspace with no `packages:` (READ, [autoload.go](../../internal/image/autoload.go#L479-L483)),
  which bounds what the tag would save there (INFERRED).
- **Two Apple Container jails at once** waits on
  [OQ-MB1](../research/macos-backend-performance.md#OQ-MB1), the ruling on what backs `/mise`.
- **Folders only the jail uses sit on shared folders** on both VM backends. yolo already gives
  the jail its own copy of `.venv`, `node_modules` and each `per_side_paths` entry, but backs that
  copy with a Mac folder, so it still crosses virtiofs (READ,
  [mounts.go](../../internal/cli/run/mounts.go#L124-L127)). A VM-local backing for such folders is
  [the file-cost research's sketch](../research/apple-container-file-cost.md#4-a-design-sketch-vm-local-volumes-for-chosen-workspace-folders),
  and is unbuilt.
- **`macos-user`'s launch cannot be split into its parts.** It records no timing spans past the
  backend dispatch (READ,
  [perf logging](perf-logging.md#macos-user-native-runs-have-no-collector-past-dispatch)), and it
  evaluates and builds its darwin floor at every launch, at a cost nobody has measured (READ,
  [orchestrator.go](../../internal/macosuser/orchestrator.go#L458-L492)).
- **The durable-dir size walk** runs on every boot pass: twice on a fresh launch (the jail's own
  boot and the first session's) and once on every attach, on both container backends, and in the
  `macos-user` bootstrap (READ: [boot.go](../../internal/entrypoint/boot.go#L595-L645),
  [bootsteps.go](../../internal/entrypoint/bootsteps.go#L246-L252),
  [darwin.go](../../internal/entrypoint/darwin.go#L84-L87)). Each walk stops at 2 s (READ,
  [report.go](../../internal/durable/report.go#L21)), and the walk at an attach goes against
  [DS-D11](../design/durable-scratch-space.md#DS-D11)'s "never at an attach". So it is inside
  Apple Container's 1.8 s attach (INFERRED). On the maintainer's Linux host one launch's two
  passes took 0.95 s and 0.30 s on 2026-10-04 (MEASURED, from the entrypoint perf log of
  yolo-jail's own workspace, which is not in the repository). On a VM backend the durable dir is
  inside the shared workspace, so each pass would come nearer that limit (INFERRED).
- **Podman's provider.** Podman's own installer gives a new machine libkrun since Podman 6.0.0,
  and Homebrew's Podman patches the default back to applehv (SOURCED: Podman 6.1.3's
  [`platform_darwin.go`](https://github.com/podman-container-tools/podman/blob/v6.1.3/pkg/machine/provider/platform_darwin.go)
  and Homebrew's [`podman.rb`](https://github.com/Homebrew/homebrew-core/blob/master/Formula/p/podman.rb),
  read 2026-10-04). Without yolo, libkrun's shared folders were the slowest measured. yolo neither
  reads nor reports which provider a machine runs (READ: outside tests, `internal/` and `cmd/`
  name a provider only in one comment, and no code reads a machine's `VMType`).

### What VM-local disks would change

Apple Container without yolo, one run per row, Python 3.11 in the VM and 3.14 natively (MEASURED,
[on a VM-local disk](../research/macos-vm-runtime-comparison.md#31-on-a-vm-local-disk) and
[on a shared folder](../research/macos-vm-runtime-comparison.md#32-on-a-shared-mac-folder)). The
other VMs were within about 12% of it on every Python step on their own disks.

| Step | VM-local disk | Shared folder | Native |
| :--- | ---: | ---: | ---: |
| pip install, offline | 2.01 s | 8.35 s | 4.01 s |
| pytest, 2,000 tests, median | 0.90 s | 1.32 s | 1.85 s |
| `django.setup()` | 0.136 s | 0.307 s | 0.290 s |
| ripgrep over the virtualenv | 0.019 s | 1.568 s | 0.292 s |
| Postgres `initdb` | 0.37 s | 2.27 s | 0.91 s |
| `pgbench` load, scale 20 | 1.27 s | 11.36 s | 2.34 s |
| `pgbench` read-write, transactions per second | 9,205 | 4,103 | 15,575 |

**INFERRED from that table**, taking `macos-user` as native, as it was on every metric it ran: a
VM jail with its virtualenv, `node_modules`, caches and database on VM-local disks would beat
`macos-user` at installing, importing, testing and setting up a database. It would still lose at
walking the source tree (`git status` and ripgrep at 3 to 5 times native, since the workspace stays
shared), at Postgres writes, and on memory.

**Not measured:** that mixed placement in a real yolo jail, with the source on the shared workspace
and the rest on VM-local disks, against the same work under `macos-user`.

### Caveats

- **One Mac, two days, two yolo commits**, for every row but the cold first launch. An M1 Max
  with 32 GiB, on macOS 26.5 with `container` 1.1.0. Apple Container ran on 2026-10-02 at
  `e09919d2` and `macos-user` on 2026-10-03 at `5ca9b748`. Native moved by up to 20% between the
  days, so each backend is read against its own day's native run
  ([side by side](../research/macos-backend-performance.md#the-two-backends-side-by-side)).
- **The cold-first-launch row is from CI**: GitHub-hosted arm64 (`macos-latest`) and Intel
  (`macos-26-intel`) runners, and the Apple Container self-hosted runner on 2026-09-25 at
  `22011184`.
- **CrowdStrike Falcon's endpoint-security extension was active.** It touches native file
  operations and not the guest's own disk ([the setup](../research/apple-container-file-cost.md#1-the-setup)).
- **yolo's builder VM ran during every Apple Container run**, with 8 CPUs and a 12 GiB cap, and it
  is inside every system-wide memory figure ([the results](../research/macos-backend-performance.md#8-results)).
- **Python 3.14 natively against 3.11 in the VMs**, in the runs without yolo
  ([what differs](../research/macos-vm-runtime-comparison.md#2-what-differs-between-the-runs-and-does-not-matter-here)).
- **One run per row** in the file-cost and runtime-comparison runs, so a difference under about 10%
  there is noise ([the setup](../research/macos-vm-runtime-comparison.md#1-the-setup)).
- **Podman Machine has never run through yolo on Apple silicon**, so its column is the runs
  without yolo, or empty.
- **`container` 1.5.0 shipped on 2026-09-29**, and every run here used 1.1.0
  ([fast-moving](../research/macos-backend-performance.md#fast-moving--verify-before-building)).

### What the numbers bear on

Both are standing text of this ruling document, and stay as written until the maintainer
rewords them:

- **The problem statement** ([lines 32-37](#L32-L37)) calls a Mac container VM "slow to start", its
  RAM a ceiling "to guess ahead of time", and that RAM "permanently held". Measured: a fresh launch
  took 6.9 s with auto-capture off, 2.5 s of it the VM's boot and boot script, against
  `macos-user`'s 5.5 s; a second terminal attaches in 1.8 s (M3), where `macos-user` needs another
  fresh launch; yolo picks the ceiling, half of host RAM and at least 4 GB; and the RAM is held, as
  stated.
- **The warning under [Refuted alternatives](#refuted-alternatives)** says a VM "still reserves RAM
  up front". VZ backs guest memory on first touch instead (SOURCED,
  [memory](../research/macos-backend-performance.md#22-memory-backed-on-first-touch-kept-until-the-container-stops)).

The research lists every correction it owes in
[its corrections section](../research/macos-backend-performance.md#9-corrections-the-results-feed).

### When to refresh this

Refresh this section, and its as-of date, when any of these lands:

- a Mac launch pass after the auto-capture fix, ideally on `container` 1.5.0 with the two-jail
  check;
- a measurement of the mixed placement above in a real yolo jail;
- Apple Container getting the stock-image skip, or `macos-user` getting timing spans;
- a fix to the durable-dir walk;
- a VM-local backing for the folders only the jail uses, measured;
- Podman Machine run through yolo on Apple silicon.

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

- **`macos-user` is the fast native path, and the intended default.** No VM, `packages:`
  through darwin nix. Selecting it is still explicit (see the note at the top).
- **The container cell is the fallback** for what native darwin cannot cover: a declared
  package with no darwin build, or a user who wants VM-grade isolation over Seatbelt.

That satisfies the [fill-the-matrix principle](fill-the-matrix-principle.md) — one path per matrix cell, with the container as the
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
> a path yolo provisions, because it is per-user infrastructure — the thing the fill-the-matrix
> principle exists to keep off the default path.

**Accepting "macOS means a VM"** and investing only in tuning it is the option the goal
rejects: the whole premise is that the VM overhead is bad enough that a reasonable person
reaches for a different tool. Tuning the container path is still worthwhile — it is the
fallback cell — but it is not the answer to the stated problem.

## What this does not license

- **Not** two competing macOS backends with a user-facing choice between them. One composed
  product: native path as the intended default, container fallback.
- **Not** a macOS backend that reads a different subset of config than Linux. Cross-platform
  sameness is on the must-survive list, and a divergence has to be **said** at the launch.
- **Not** a second hypervisor for builds. The builder runs as a container on the runtime that
  is already up.
- **Not** relitigating the axes. A proposal that mixes runtime, builder and packages into one
  choice is the confusion this document exists to end.

## Current values

Verified at `d14bdab7`. The prose above explains what each of these is for; this table is the
only place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Resolvable runtimes | `podman`, `container`, `macos-user` — and `docker` is a refusal naming its replacement | `internal/config/validate.go` |
| macOS auto-detection order | `container`, then `podman`. `macos-user` is never probed — it is a *native* runtime, reachable only when named | `run.resolveRuntime`, `paths.NativeRuntimes` / `paths.SupportedRuntimes` |
| Package realization, `macos-user` | a darwin `buildEnv` profile, **not** an imperative nix profile | `internal/darwinpkg` |
| Linux builder for the container runtimes | an ephemeral nix-plus-sshd container, driven over nix's remote-builder protocol | `internal/containerbuilder` (`BuilderImage`, `BuilderContainer`, `BuilderHostPort`) |
| Builder key material | a per-machine key dir under the machine storage root | `containerbuilder.BuilderKeyDir`, `BuilderKey` |
| The acceptance-bar check | every darwin store `bin` dir must reach the launch PATH | `macosuser.PlanInvariants` |

## Why it's this way

Forward-facing rulings a maintainer would otherwise undo.

| Ruling | Why it stays |
| :--- | :--- |
| **Pursue both, as one composed product** — not two competing backends | Framing them as competitors forces a user-facing choice between "fast" and "works for this package", which is exactly the matrix cell the fill-the-matrix principle says should have one path. The container is the *escape hatch*, and naming it that is what makes the native path the default it is headed for. |
| **The builder axis exists only for the container runtime** | Every argument that starts "which builder should macOS use?" is a container-track question. Blurring it is what makes a VM-based builder look like an answer to a no-VM goal. |
| **A darwin `buildEnv` profile, not an imperative nix profile** | An imperative profile accumulates state nobody declared and drifts from the config that was supposed to define it. A `buildEnv` is a pure function of the declared list. |
| **The persistent on-demand VM builder is gone; the ephemeral container builder is the only shipped one** | A VM builder needs idle-stop logic, a RAM commitment and `sudo`. A builder that exists only during a build is zero-idle by construction, so the whole idle-stop concern disappears rather than being managed. |
| **Declaratively identical is good enough; byte-identical needs a VM** | It is what any native tool gives you, and pretending otherwise would mean either shipping a VM by default or claiming a guarantee the platform cannot make. The container cell is the honest answer for anyone who needs the Linux artifact. |
| **Seatbelt-grade isolation is documented, not hidden** | The stronger boundary is one config key away. A backend that quietly implied VM-grade isolation would be the more dangerous failure. |
