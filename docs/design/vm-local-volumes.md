---
title: "Where a VM jail keeps the folders only it uses, and why they should stop crossing to the Mac"
date: 2026-10-08
status: in-review
stage: DESIGN
next: "Rule OQ-VL1 to OQ-VL4. Before the build, one Mac run on container 1.5.0 checks premise P3 (a named volume mounted after the workspace share, at a path inside it), what a fresh volume's root holds, and how many volumes one Apple Container VM takes, by hand or by the Mac-runner probe in apple-container-file-cost.md §5"
tags: [design, macos, apple-container, podman-machine, virtiofs, performance, per-side, volumes]
summary: "On a Mac, an Apple Container or Podman Machine jail keeps its own copy of .venv, node_modules and every other per_side_paths entry in a Mac folder shared into the VM over virtiofs, where each small-file operation the guest cannot serve from its cache is a round trip to the Mac. The host never reads those copies; they exist because the two sides need different binaries. This design backs each per-side path with a named volume per workspace, which the VM formats and serves itself: measured without yolo, an offline pip install takes 2.01 s on such a disk against 8.35 s on a shared folder. No new key: the per-side set already names the folders. Open: whether it is the default, how the first launch fills the disks, whether ~/.cache follows, and how database folders join."
vantage:
  status-chip: true
---

# Where a VM jail keeps the folders only it uses, and why they should stop crossing to the Mac

**Status:** 2026-10-08. Nothing built. Evidence read at `337086f64`; every timing is from
the Mac runs in [the file-cost research](../research/apple-container-file-cost.md) and
[the runtime comparison](../research/macos-vm-runtime-comparison.md), none of them through yolo
with this design.

> **In short.** The per-side set is already the list of folders a VM jail owns alone, so on a
> VM backend each should live on a disk the VM serves itself, not on a Mac folder the jail
> reaches through virtiofs.

**Why it matters.** A Python or Node project's hottest trees are its virtualenv and its
`node_modules`. On a shared folder every install, every cold import and every tree walk pays a
round trip per file: an offline `pip install` took 8.35 s there and 2.01 s on a VM-local disk,
and ripgrep over the virtualenv 1.568 s against 0.019 s
([the runtime comparison, §3.1](../research/macos-vm-runtime-comparison.md#31-on-a-vm-local-disk)).
Warm work the guest has cached runs closer to native.

**The shape.** One labelled named volume per workspace per per-side path, mounted over that path
in place of today's host-folder bind, on the model of
[the tool disk](../research/macos-backend-performance.md#10-decision-ledger).

**Cost.** The host can no longer look inside the jail's copies, a moved workspace loses them, and
the first launch after the change fills each one again ([OQ-VL4](#OQ-VL4)).

**Start at [§3](#3-the-design)** — the mechanism; [§4](#4-behavior-in-every-case) says what each
case does.

**Needs your ruling:** [OQ-VL1](#OQ-VL1), [OQ-VL2](#OQ-VL2), [OQ-VL3](#OQ-VL3), [OQ-VL4](#OQ-VL4).

**Reads with:** [apple-container-file-cost.md](../research/apple-container-file-cost.md) (why file
work is slow, and the sketch this replaces),
[the state-separation reference](../reference/jail-state-separation-design.md) (what the per-side
set is, and [SS-3](../reference/jail-state-separation-design.md#ss-3), the ruling this partly
supersedes), [the backend benchmark's ledger](../research/macos-backend-performance.md#10-decision-ledger)
(the tool disk), [vm-local-volumes-plan.md](vm-local-volumes-plan.md) (the implementation sketch,
incomplete while the questions are open).

---

## Terms

- **Per-side path** — a workspace-relative path the host and the jail each see their own copy
  of: `.venv`, `node_modules`, the venv path a workspace's `mise.toml` names, and every
  `per_side_paths` entry. Defined in
  [the state-separation reference](../reference/jail-state-separation-design.md); `yolo config-ref`
  has the key.
- **Shared folder** — a Mac folder shown inside a VM through virtiofs, so the Mac's file system
  answers every operation the guest cannot serve from its cache
  ([the file-cost research's terms](../research/apple-container-file-cost.md#terms)).
- **VM-local disk** — a disk the guest's own kernel serves. On Apple Container a named volume is
  an ext4 image attached as a block device; on Podman Machine a named volume is a directory on the
  machine's own disk.
- **Tool disk** — the named volume an Apple Container jail mounts at `/mise`, one per workspace,
  coined in [the backend benchmark](../research/macos-backend-performance.md#terms).
- **Side disk** *(coined here)* — the named volume that backs one per-side path of one workspace
  on a VM backend. Not a tool disk (that holds mise's installs, at `/mise`, outside the
  workspace), and not a scratch volume (per launch, deleted with it).
- **VM backend** — Apple Container, and Podman on a Mac (Podman Machine). Not podman on Linux,
  whose binds are already the host's own file system, and not `macos-user`, which has no VM.

## 1. Goal and non-goals

**Goal.** Every per-side path of a VM-backend jail is served by the VM's own kernel, with no new
config key and no change to what the host sees of the workspace.

**Non-goals:**

- **The workspace itself stays a shared folder.** The source tree is what both sides edit.
  `git status` and ripgrep over it stay about 5 times native
  ([the benchmark's M5 and M6](../research/macos-backend-performance.md#timings-m5-to-m12)); faster
  sharing is a different question
  ([the runtime comparison, §6](../research/macos-vm-runtime-comparison.md#6-is-there-an-open-stack-with-faster-shared-folders)).
- **No change on podman on Linux or on `macos-user`.** Linux binds are native already;
  `macos-user` shadows nothing (the per-side set is not enforced there, `yolo config-ref` says so).
- **No sync between the two copies.** The per-side rule is that the sides never share these
  trees; this design keeps it.
- **No new mount surface.** The `mounts` key keeps its rule that a writable mount lands under
  `/ctx` and only from user scope (`ParseMountElement` and `rwMountTrusted` in
  [`internal/config/mounts.go`](../../internal/config/mounts.go)).
- **No change to the jail's home or `/tmp`.** The home stays the workspace's `.yolo/home` over
  virtiofs, and scratch stays tmpfs. `~/.cache` is [OQ-VL2](#OQ-VL2).

## 2. What exists today

READ at `337086f64`:

- **The per-side set** is computed in [`internal/perside`](../../internal/perside/perside.go):
  `DefaultRels` gives `.venv`, `node_modules` and the mise venv path when it differs from `.venv`,
  and `UserRels` adds `per_side_paths`. The set is root-relative; a monorepo's nested
  `node_modules` must be listed.
- **Its backing** is a host folder per path, `<workspace>/.yolo/home/venv-shadows/<rel>` with `/`
  written as `__`, bound over `/workspace/<rel>` by `venvShadowMountArgs`
  ([`internal/cli/run/mounts.go`](../../internal/cli/run/mounts.go)), after the workspace's own
  mount. Every container backend takes the same bind, sealed launches included. Placing the backing
  in the workspace state dir is the ruling
  [SS-3](../reference/jail-state-separation-design.md#ss-3), so it moves and is deleted with the
  workspace.
- **On a VM backend that bind is a shared folder.** On Apple Container it is one more virtiofs
  share; on Podman Machine the bind source crosses the machine's share (podman's behavior, not
  yolo's; not verified on a Mac).
- **Apple Container binds the whole `.yolo/home` at `/home/agent`**
  (`appleContainerBaseMounts`, [`assemble_parts.go`](../../internal/cli/run/assemble_parts.go)),
  so INFERRED from the two argv builders: the jail also sees its per-side copies under
  `~/venv-shadows/`.
- **The tool disk** is built: a labelled named volume per workspace, created before
  `container run` on a fresh launch, reaped by `yolo prune` once its workspace is gone, and listed
  by `yolo stores`
  ([MB-D1 to MB-D8](../research/macos-backend-performance.md#10-decision-ledger)). It exists on
  Apple Container only; its code is specific to `/mise` and to the `container` command
  (`internal/prune/misevolumes.go`, `internal/cli/run/actooldisk.go`,
  `internal/cli/stores/tooldisks.go`), and its listing drops any volume name it does not parse.
- **The jail runs as root** (no `--user`), so a root-owned fresh volume is writable by the agent.
  On a rootless Podman Machine the container's root maps to the machine's user, which owns the
  volume it creates.
- **One workspace runs one jail.** A second `yolo` in a workspace attaches to the running jail
  (`runContainer` in [`internal/cli/run/run.go`](../../internal/cli/run/run.go)); only a capture
  jail skips the attach, and it runs in a scratch workspace of its own.
- **Nothing removes a per-side copy today** but deleting the workspace. No verb deletes
  `venv-shadows`, and `yolo stores` does not list it.

## 3. The design

### 3.1 Principles

- **P1. The per-side set is the whole selection.** A path is VM-local exactly when it is
  per-side. No second list, no new key: the set already means "the host must not see the jail's
  copy", which is the one condition under which a VM-local disk loses nothing the host uses.
- **P2. One side disk per workspace per path.** Never shared between workspaces, so two
  workspaces' jails run at once, as the tool disk made possible for `/mise`.
- **P3. The mount nests.** A side disk is mounted at `/workspace/<rel>` after the workspace share,
  so inside it. That works for binds today; for a named volume on Apple Container it is
  **unverified**, and the build waits on one Mac run that shows it ([§7](#7-risks)).
- **P4. A side disk never refuses a launch.** A failure to create or attach one falls back to
  today's host-folder bind for that path, said with the command that clears it.
- **P5. yolo never deletes a side disk a workspace may still use.** A workspace it cannot prove
  gone keeps its disks.
- **P6. Only the jails a user works in get side disks.** Sealed launches and capture jails keep
  host-folder binds: a sealed build keeps its state in its own workspace by design (`seal.go`),
  and a capture jail's scratch workspace is deleted after one run.

### 3.2 Mechanism

Implementation decisions, recorded in the [ledger](#decision-ledger):

- **Name** ([VL-D1](#VL-D1)): `<container name>.side.<first 12 hex digits of SHA-256 of the
  path>`, the path first normalized (`path.Clean`, so `./x`, `x/` and `x` are one disk). The name
  parses back to its workspace's container name exactly, as the tool disk's does.
- **Labels** ([VL-D2](#VL-D2)): the tool disk's owner and workspace labels, plus
  `org.yolo-jail.per-side=<path>`, the normalized path. The label is how prune and `yolo stores`
  say which folder a disk holds.
- **Creation** ([VL-D3](#VL-D3)): on a fresh launch, before the runtime's `run`, for each path in
  the set: inspect, and create with the labels if missing. Never on an attach (P6 excludes sealed
  and capture launches). A create that fails because the disk now exists is success. A
  `--dry-run` launch creates nothing and prints the argv with the side-disk mounts it would use.
- **Mount** ([VL-D4](#VL-D4)): `-v <side disk>:/workspace/<path>` replaces the host-folder bind
  for that path, in the same argv position, after the workspace mount. Everything keyed on the
  path string (the persistence map, the skills reader's exclusion, the jail's venv precreate) is
  unchanged.
- **Reaping** ([VL-D5](#VL-D5)): `yolo prune` removes a side disk only when its workspace label
  names a directory that does not exist **and whose parent directory does exist**, the label
  matches the name, and no container names the disk; never forced. A workspace under a missing
  parent (an unmounted share, an unplugged disk) reads as *unknown* and keeps its disks (P5).
- **Listing** ([VL-D6](#VL-D6)): `yolo stores` lists each side disk with its workspace, its path
  and prune's verdict. Its size is the allocated size where the runtime exposes the image (Apple
  Container), and *unknown* otherwise.
- **First-launch message** ([VL-D7](#VL-D7)): when a launch created a side disk and the old
  `venv-shadows/<rel>` folder is non-empty, it says so once for that path, with the folder's size
  and the `rm -rf` that frees it. Creating the disk is what makes the message once.

### 3.3 Where each backend lands

| Backend | Per-side path backed by | Change |
| :--- | :--- | :--- |
| Apple Container | a side disk (an ext4 image on virtio-blk) | yes |
| Podman Machine (macOS) | a side disk (a podman volume on the machine's disk), subject to [OQ-VL1](#OQ-VL1) | yes |
| podman on Linux | the host folder, as today | none |
| `macos-user` | nothing; the sides share the path, as today | none |
| any backend, sealed or capture launch | the host folder, as today | none |

### 3.4 Podman Machine's lifecycle

Podman has none of the tool disk's machinery, so it is specified here rather than inherited:

- **Create:** `podman volume create --label …` before `podman run`, as on Apple Container. Podman
  would create a missing named volume itself, unlabelled; yolo creates first so the labels exist.
- **List and reap:** `podman volume ls --filter label=org.yolo-jail.owner=yolo`, with the same
  rule as [VL-D5](#VL-D5); `podman volume rm`, never forced.
- **Size:** *unknown* in `yolo stores`; the volume lives inside the machine's disk, which the host
  cannot measure per volume.
- **`podman machine rm`** deletes every side disk with the machine; the next launch creates new,
  empty ones ([OQ-VL4](#OQ-VL4) decides how they fill).
- **A full machine disk** surfaces as a write error in the jail; it is the machine's disk filling,
  as it would for any podman volume, and `podman system df` is the next step to name.

## 4. Behavior in every case

| Case | What happens |
| :--- | :--- |
| **Default set** | `.venv` and `node_modules` are always in it, so two side disks; three when a mise venv path other than `.venv` is declared |
| **Path absent in the workspace** | The side disk is created empty and mounted; what the runtime leaves in the host tree as a mountpoint is podman's documented behavior and unverified on Apple Container |
| **Path is a file or symlink on the host** | Same as today: skipped with a warning, no side disk made |
| **Duplicate entries** | The set is normalized and deduplicated before this runs; one side disk |
| **Nested entries** (`a` and `a/b` both per-side) | Each gets a disk. Apple Container mounts in argv order, unverified; podman sorts mounts by destination depth. The argv puts a parent before its child either way |
| **First launch after the change** | Each disk starts empty or is filled as [OQ-VL4](#OQ-VL4) rules. The jail's venv precreate makes a `.venv` only where a mise config declares one and mise resolves `uv` and `python`; elsewhere `.venv` is an empty directory until the user installs. The [VL-D7](#VL-D7) message names the old folder |
| **A fresh disk's root is not empty** (ext4's `lost+found`, if the formatter makes one) | Unverified; the Mac run checks it. If it is there, a tool that refuses a non-empty target (`uv` without `pyvenv.cfg`, `npm`) is the risk, and the design reopens on how to hide it |
| **Old host copy in `venv-shadows`** | Left in place, untouched; still visible at `~/venv-shadows/` on Apple Container |
| **Create fails** (runtime error, disk full) | Warned with the runtime's error and the `volume rm` that removes a half-made disk; that path falls back to the host-folder bind (P4); the launch goes on |
| **The runtime refuses an attachment at `run`** | Only an Apple Container failure carrying `VZErrorDomain` with "The storage device attachment is invalid" counts. yolo removes the half-created container, retries once with every side disk replaced by its host-folder bind, and says that the side disks were refused, that the jail now sees the old host copies, and what the user can send upstream. Nothing is remembered: the next fresh launch tries the disks again. Any other `run` failure is reported as today, with no retry |
| **Attach to a running jail** | No disks are created or checked; the running jail already has its mounts |
| **Two workspaces at once** | Each has its own disks (P2); no conflict |
| **Two jails of one workspace** | Cannot arise: the second launch attaches. Capture jails take no side disks (P6) |
| **Workspace deleted** | Its disks become reclaimable once its parent exists; the next `yolo prune --apply` removes them |
| **Workspace moved or renamed** | The disks stay with the old path and become reclaimable; the moved workspace's next launch makes new ones. A regression against today, where the copies move with `.yolo` ([§6](#6-cost)) |
| **Workspace on an unmounted share** | Its parent is missing, so it reads as unknown and keeps its disks (P5) |
| **Path removed from `per_side_paths`** | Its disk is kept and listed with its path; `yolo stores` does not say whether the path is still per-side, since it does not load each workspace's config. The path is the workspace's own folder again, as today |
| **A jail writes `per_side_paths`** | The workspace config is writable from the jail, so the next launch could create a disk per new entry. The launch's config-change confirmation shows the change first ([config-safety.md](../reference/config-safety.md)) |
| **Older yolo on the same workspace** | Binds the host folder as before and never sees the disks; the two copies diverge, which is the per-side rule's normal state |

**Concurrency.** Creation runs inside the fresh launch, while the launch lock that serializes
launches of one workspace is held, so two creators of one disk cannot race. Two workspaces create
different disks.

**Forbidden behavior:**

- never create, inspect or remove a side disk on an attach;
- never force a removal, never remove a disk a stopped container still names;
- never remove a disk whose workspace yolo cannot prove gone;
- never refuse a launch over a side disk;
- never mount a side disk anywhere but `/workspace/<its path>`.

**Done looks like:**

- In an Apple Container jail, `/proc/mounts` shows `/workspace/.venv` and
  `/workspace/node_modules` as `ext4`, and the workspace itself as `virtiofs`.
- In one session, the same offline `pip install` (interpreter and wheels in the same places)
  takes at most half as long into a side disk as into the host-folder bind.
- No mount in a jail launched after the change targets `.yolo/home/venv-shadows/`.
- `yolo stores` lists each side disk with its path; deleting the workspace and running
  `yolo prune --apply` removes them, and unplugging the disk a workspace lives on does not.
- Two workspaces' Apple Container jails run at once.
- On Podman Machine, `podman volume ls` shows the side disks with their labels, and the jail's
  `/workspace/.venv` is on the machine's own disk.

## 5. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| **A new key listing VM-local folders**, as the file-cost sketch proposed | Rejected: the per-side set already names them, and two lists would drift. A folder the jail must own on a VM is per-side by definition |
| **One machine-wide volume holding every workspace's per-side trees** | Rejected on Apple Container: a volume attaches to one VM at a time, so it shuts out the next workspace's jail, the defect [OQ-MB1](../research/macos-backend-performance.md#10-decision-ledger) fixed for `/mise` |
| **A single side disk per workspace, with each path a subdirectory of it** | Rejected: a subdirectory mount of a named volume is not a `container run` option read in the source, and one disk per path keeps reaping per path |
| **Faster shared folders instead** (a cache policy on VZ's virtio-fs, or another stack) | Not a substitute: no open option measured near a VM-local disk ([the runtime comparison, §6](../research/macos-vm-runtime-comparison.md#6-is-there-an-open-stack-with-faster-shared-folders)); pursued separately as an upstream request |
| **`macos-user` for these workloads** | Already the answer for native-speed files; this design is for the jails that run on a VM |

## 6. Cost

- **The host cannot inspect the jail's copies.** Today it can read
  `.yolo/home/venv-shadows/`; with a side disk only `container exec` (or `podman exec`) reaches
  it. Nothing in yolo reads those copies from the host.
- **[SS-3](../reference/jail-state-separation-design.md#ss-3) no longer holds on a VM backend.**
  The backing leaves the workspace state dir for the runtime's volume store, so a moved or
  renamed workspace loses its jail copies, and `rm -rf <workspace>` no longer frees them until
  `yolo prune --apply`.
- **One refill per path per workspace** on the first launch after the change ([OQ-VL4](#OQ-VL4)).
- **Disk.** A side disk is sparse, but whether space a guest frees (deleting a `node_modules`)
  goes back to the image is not verified, so a disk may stay at its high-water mark. Each
  defaults to `container`'s 512 GB sparse ceiling, as the tool disk does.
- **The old host copies stay** until the user deletes them; the launch names each once.

## 7. Risks

| Risk | Mitigation |
| :--- | :--- |
| **P3 is false on Apple Container**: a named volume cannot mount inside a virtiofs mount | Checked on one Mac before the build, on `container` 1.5.0, in yolo's order: `container run -v <dir>:/workspace -v <vol>:/workspace/.venv …`, then `/proc/mounts`; the reverse order recorded beside it. If yolo's order fails, this design reopens, since no path in the workspace could take a side disk |
| **The VM refuses past some number of block devices** | Not measured; the default set adds two or three to the tool disk. The same Mac run counts how many volumes one VM takes. The attachment-refused row in [§4](#4-behavior-in-every-case) keeps the launch alive |
| **A fresh disk's root holds `lost+found`** | The same Mac run lists a fresh disk's root and runs `uv sync` into it |
| **A refusal names no device** | `VZErrorDomain Code=2` did not say which attachment it refused in [the two-jail check](../research/macos-backend-performance.md#7-found-on-the-way-two-apple-container-jails-may-mount-one-ext4-disk), hence the all-or-nothing retry |
| **A user's per-side path holds data they care about** (a database folder, [OQ-VL3](#OQ-VL3)) | P5 and [VL-D5](#VL-D5): a disk is removed only when its workspace is provably gone. A moved workspace still strands it |
| **A test harness collects disks** | The integration harness removes a test workspace's side disks by its workspace label, since their names are hashes it cannot derive without the config |

## Open Questions

1. 💬 **OQ-VL1: On which VM backends are side disks the default for every per-side path?**

   Decides whether existing Mac users' next launch moves their jail copies unasked, and partly
   supersedes [SS-3](../reference/jail-state-separation-design.md#ss-3) there.

   - **A — Default on both VM backends.** Every Mac VM jail gets the speedup. Podman Machine's
     libkrun provider gains most: its shared folders ran pip about 4 times slower than VZ's.
   - **B — Opt-in, by a new boolean.** No surprise, but two co-equal paths per backend, which the
     [fill-the-matrix principle](../reference/fill-the-matrix-principle.md) argues against.
   - **C — Default on Apple Container only.** Podman Machine has no Mac CI job and needs a
     reaper of its own ([§3.4](#34-podman-machines-lifecycle)); it follows later.

   <!-- vantage: question id=OQ-VL1 leaning="A, default on both: the host never reads these copies, and the backend that gains most is Podman Machine on libkrun." -->

   _Leaning:_ A, default on both: the host never reads these copies, and the backend that gains
   most is Podman Machine on libkrun.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-VL2: Does an Apple Container jail's `~/.cache` move to a disk of its own?**

   `~/.cache` is the machine's one cache, shared into every jail over virtiofs; pip, uv, npm and
   Pants read it on every install. [OQ-MB1](../research/macos-backend-performance.md#10-decision-ledger)
   left it open. A disk there is outside the per-side rule (P1), a third kind beside tool and side
   disks.

   - **A — A disk per workspace, like `/mise`.** VM-disk speed; each workspace downloads into its
     own cache once; the host-side readers of the machine cache (the image archive cache, agent
     log purging, `yolo stores`) stop seeing the jail's half.
   - **B — Keep the machine's shared cache.** One download per machine; every cache read pays
     the shared-folder cost.

   <!-- vantage: question id=OQ-VL2 leaning="Undecided between A and B: A is faster, but it takes the jail's cache out of reach of the host-side tools that manage the machine cache, and nobody has measured how much an install's time is cache reads." -->

   _Leaning:_ Undecided between A and B: A is faster, but it takes the jail's cache out of reach
   of the host-side tools that manage the machine cache, and nobody has measured how much an
   install's time is cache reads.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-VL3: How does a database's data folder get a side disk?**

   Postgres on a VM-local disk ran its `pgbench` load in 1.27 s against 11.36 s on a shared
   folder, and yolo cannot guess where a project keeps its data.

   - **A — The user lists it in `per_side_paths`.** No new key. On podman on Linux it is then
     shadowed too, so host and jail keep separate databases; on `macos-user` they still share it.
   - **B — A second key for VM-local-only paths.** Linux unchanged, at the price of a key and a
     second list.
   - **C — Nothing.** Databases in the workspace stay slow; users run them outside the jail.

   <!-- vantage: question id=OQ-VL3 leaning="A: a database folder the jail writes is the jail's own on every backend that can shadow it, so per-side is already its meaning, and it needs no new key." -->

   _Leaning:_ A: a database folder the jail writes is the jail's own on every backend that can
   shadow it, so per-side is already its meaning, and it needs no new key.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-VL4: Does a new side disk start empty, or with the jail's old copy?**

   Decides whether the first launch after the change reinstalls every virtualenv and
   `node_modules`.

   - **A — Empty.** The user reinstalls once per path; nothing is copied, and the old folder is
     named for deletion.
   - **B — A one-time copy of `venv-shadows/<rel>`.** No reinstall; costs a copy over virtiofs at
     that launch (on Apple Container the jail already sees the folder at `~/venv-shadows`; Podman
     Machine needs it mounted for the copy).

   <!-- vantage: question id=OQ-VL4 leaning="A: a reinstall from a lockfile is the per-side rule's normal recovery, and a copy adds a slow first launch and a second code path for one use." -->

   _Leaning:_ A: a reinstall from a lockfile is the per-side rule's normal recovery, and a copy
   adds a slow first launch and a second code path for one use.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision ledger

Implementation decisions under the per-side rule and the tool disk's precedent; each is
reversible and none changes what a user configures.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="VL-D1"></a>VL-D1 | *Implementation decision.* A side disk is named `<container name>.side.<12 hex of SHA-256 of the normalized path>`; the path itself goes in a label, because a path can hold characters a volume name cannot | 2026-10-08 | [§3.2](#32-mechanism) | — |
| <a id="VL-D2"></a>VL-D2 | *Implementation decision.* Labels: the tool disk's owner and workspace labels, plus `org.yolo-jail.per-side=<normalized path>` | 2026-10-08 | [§3.2](#32-mechanism) | — |
| <a id="VL-D3"></a>VL-D3 | *Implementation decision.* Created on a fresh launch only, under the launch lock, never for a sealed or capture launch; inspect then create; a create that fails because the disk exists is success; a dry run creates nothing | 2026-10-08 | [§3.2](#32-mechanism), [§4](#4-behavior-in-every-case) | — |
| <a id="VL-D4"></a>VL-D4 | *Implementation decision.* The side disk replaces the host-folder bind at the same path string and argv position; nothing keyed on the path changes | 2026-10-08 | [§3.2](#32-mechanism) | — |
| <a id="VL-D5"></a>VL-D5 | *Implementation decision.* Prune removes a side disk only when its workspace is missing and that workspace's parent exists, the label matches, and no container names it; never forced. A missing parent reads as unknown | 2026-10-08 | [§3.2](#32-mechanism) | — |
| <a id="VL-D6"></a>VL-D6 | *Implementation decision.* `yolo stores` lists side disks with workspace, path and prune's verdict; sized by allocated blocks on Apple Container, unknown on Podman Machine | 2026-10-08 | [§3.2](#32-mechanism), [§3.4](#34-podman-machines-lifecycle) | — |
| <a id="VL-D7"></a>VL-D7 | *Implementation decision.* The old-folder message fires when a launch created a disk and the old folder is non-empty | 2026-10-08 | [§3.2](#32-mechanism) | — |
| <a id="VL-D8"></a>VL-D8 | *Implementation decision.* Only an Apple Container storage-attachment refusal triggers the one retry with host-folder binds, after removing the half-created container; nothing is remembered between launches | 2026-10-08 | [§4](#4-behavior-in-every-case) | — |
