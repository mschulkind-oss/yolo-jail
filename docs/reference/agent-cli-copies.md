---
status: current
verified: 2026-09-09
verified_commit: 41dde711
covers:
  - internal/entrypoint/shims.go
  - internal/entrypoint/versionprune_test.go
  - internal/capture/
  - internal/cli/capturematerialize.go
  - internal/cli/autocapture.go
  - internal/prune/prune.go
  - internal/paths/paths.go
tags: [delivery, disk, capture, workspaces, prune, reflink, evergreen]
---

# Agent CLI copies — the two axes, and who deletes the rest

**Status:** CURRENT as of 2026-09-09, verified against `41dde711`.

A machine running yolo accumulates copies of every agent CLI it installs, and the total is
`N × V × S` — **workspaces × retained versions × bytes per version**. The two multiplicands are
independent, they are driven by different things, and every reclaiming mechanism collapses at most
one of them. This doc is the vocabulary for that arithmetic and the account of which mechanism does
what, so that a proposed fix can be priced against the axis it actually touches.

- **The N axis** *(coined here)* — one machine, many workspaces, each holding its own copy of the
  same version. Its driver is the per-workspace install prefix: `~/.local`, `~/.npm-global` and
  `~/go` are per-workspace binds, so a second workspace installs its own copy of everything.
- **The V axis** *(coined here)* — one workspace, many *versions*, because a vendor's own updater
  writes a new version directory and never removes the old one. Its driver is the vendor, or under
  evergreen yolo's own update arm; either way it is a write into a workspace's writable home.
- **`S`** — bytes for one version of one program. Not an axis; the unit the two axes multiply.

| Component | Lives in |
| :--- | :--- |
| The V-axis prune, as generated into every native launcher | `internal/entrypoint` (`_prune_versions` in the launcher template; `versionprune_test.go`) |
| The capture store, its admit path and its reap rule | `internal/capture` (`PruneSupersededCaptures`, `select.go`, `clone_linux.go`) |
| Materialize, and the resolver the reap rule complements | `internal/cli` (`resolveCaptureFor`, `capturematerialize.go`) |
| Auto-capture on first launch | `internal/cli` (`autocapture.go`), `internal/cli/run` (`autocapture.go`) |
| Cross-workspace hardlink dedup | `internal/prune` (`HardlinkDuplicateFiles`, `WalkDedupableWorkspaces`) |
| The three dedupable/capturable home surfaces | `internal/paths` (`HomeSurfaces`) |

**Reads with:** [`../design/program-delivery.md`](../design/program-delivery.md) (the capture design
itself, the launcher, and the evergreen policy — the authority for all three),
[`../design/minimal-disk-footprint.md`](../design/minimal-disk-footprint.md) (who triggers a
reclaimer at all), [`jail-home.md`](jail-home.md) (the tier model that made the install prefix
per-workspace), [`image-staging-vs-baking.md`](image-staging-vs-baking.md) (the image and store
ledgers, which are a larger line item than program bytes by a wide margin).

---

## Principles

1. **P1. Name the axis before pricing the fix.** A mechanism that collapses one factor of
   `N × V × S` is not interchangeable with one that collapses another, and adding the two costs
   together into a single headline number hides which one a proposed fix would touch.

2. **P2. A design that is excellent on btrfs and harmful on ext4 must say so in those terms.** The
   filesystem is not a deployment detail for anything built on reflink; it inverts the sign of the
   change. See [the ext4 inversion](#the-ext4-inversion).

3. **P3. Prefer a mechanism whose reference set is local and complete.** A per-workspace versions
   directory has exactly one referrer — the symlink beside it. A machine-global store has an
   unknown set of referrers. Locality is not a nicety; it is the difference between a rule and a
   research question.

4. **P4. A shipped mechanism with no trigger is evidence about triggers, not about mechanisms.**
   Before building another reclaimer, it is worth knowing what an existing one would have
   reclaimed.

## Which axis each mechanism collapses

| Mechanism | Collapses | Filesystem dependence | Reference oracle it needs |
| :--- | :--- | :--- | :--- |
| **Keep-newest-K version prune** (`_prune_versions`) | **V** | none | **local and complete** — the symlink beside the versions dir |
| **Capture + materialize** | **N**, cold install only | **reflink**, else it *adds* one machine-wide copy | machine-wide; answered by [complementing the resolver](#reclaiming-a-capture-entry-is-never-unsafe) |
| **Cross-workspace hardlink dedup** | **N**, post-hoc | hardlink — universal, but within **one mount** | kernel-maintained `st_nlink` |
| **Machine-global program prefix** | **N**, *and the download*, structurally | none | none — there is one copy |
| **Disabling a vendor self-updater** | **V**, at source | none | none |
| **Evergreen updates** | nothing — it *drives* V | none | n/a |

Three consequences follow, and they are why the table is worth having.

**Capture's saving does not compound; it dilutes.** The materialize branch lives inside the
launcher's cold-install arm, reached only when the real binary is absent. Every other arm runs the
program's own update verb and has no materialize path, so **capture saves one copy per workspace,
once** — and every update after it escapes the store. After *k* updates a machine holds
`S + N×k×S`, asymptotically the same as `N×k×S` without a store at all.

**Evergreen and capture do not meet.** Evergreen multiplies V; capture collapses N. A sequencing
argument built on "evergreen multiplies exactly the cost capture removes" is an argument about two
different axes.

**Capture does not remove the need to prune versions.** A vendor's self-updater — or, under
evergreen, yolo's own update arm — writes full-size version directories into the workspace's
writable home whether or not the first one arrived from a store. Under capture the V-axis prune has
exactly the same work to do.

> [!WARNING]
> **Cross-workspace hardlink dedup carries the hazard capture's admit path calls its sharpest trap,
> and it ships today with no freeze.** A hardlinked file *is* the running program's bytes, so an
> installer that opens one for write reaches every workspace at once. `HardlinkDuplicateFiles`
> creates exactly that relationship between two workspaces' copies, with no admit-time read-only
> freeze to bound it. Its mitigating observation — that the claude updater writes new inodes — is
> not a general guarantee. **If dedup is ever given an automatic trigger, it needs capture's
> read-only freeze first.** Reachable only from a human typing `yolo prune` today, which is the
> whole of what bounds it.

## The V-axis prune

`_prune_versions` is a keep-newest-K rule over a program's own version directory, run **by the act
that created the new version**, in the same workspace, immediately after it succeeds. It is
generated into every native launcher and is called from both the update arm and the cold-install arm
— "whoever installed the new one" is the trigger, so both qualify.

It needs **no store, no oracle and no enumeration**, and that is a property of the tree rather than
a policy: the referrer set for `~/.local/share/<bin>/versions/*` is one symlink,
`~/.local/bin/<bin>`, in the same per-workspace tree, so everything else there is unreferenced *by
construction for that workspace* (P3). It needs no filesystem support either, so it behaves
identically on ext4 and btrfs — which capture does not (P2).

Two guards are load-bearing, and both were found by writing the tests before the code.

> [!WARNING]
> **The symlink is also the guard.** When `~/.local/bin/<bin>` is not a symlink *into* the versions
> directory, the prune does nothing at all. The referrer set is then unknown, and a prune that
> cannot name the live version has no business deleting anything. That is what makes the rule safe
> to call for every native program, including the ones that keep no version directory.

> [!WARNING]
> **The live entry is the DIRECTORY ENTRY, not the symlink's target.** Conflating the two deletes
> the running version. The claude builds are single *files* directly under `versions/`, where the
> two coincide — but a vendor that keeps a directory per version puts the binary one level deeper,
> and comparing whole target paths against the entry then never matches. The guard silently stops
> guarding in exactly the rollback shape (live == oldest) where it is the only thing between
> keep-newest-K and an unusable launcher.

**`K` here is not `N` and not the store's `K`.** This one is per workspace and over *versions*
— the live build plus one rollback target. The capture store's is machine-wide and is 1. `N` is the
workspace count. The launcher template's own comment states all three, because two of them are one
letter apart.

Rollback lives on the V axis, in the vendor's own version directory, which is what makes the
store's `K = 1` safe.

## Reflink, and where materialize inverts

Materialize's cheap arm is `FICLONE`: the destination is its **own inode** (`nlink 1`) sharing
extents with the source, so writing to it copies on write and touches nothing else. That is a
strictly better property than a hardlink's, not merely an equal one.

`link(2)` and `rename(2)` both fail with `EXDEV` across two binds of one filesystem — the kernel
compares the *mount*, not the device — while `FICLONE` succeeds across them. That is what retired
`st_nlink` as a reachability oracle for the store.

### The ext4 inversion

btrfs, XFS with `reflink=1`, and ZFS support reflink; **ext4 does not, and ext4 is the default
filesystem of most Linux installs and of every GitHub runner.** So the copy fallback is a path the
code takes in production, and the machine-wide arithmetic for one program at one captured version
splits in two:

| Filesystem | Store | Per workspace | Machine total, N workspaces | vs. no store |
| :--- | :--- | :--- | :--- | :--- |
| reflink-capable | `S` | ≈0, shared extents | **`S`** | −(N−1)·`S` |
| **no reflink, or store and home on different filesystems** | `S` | **`S`**, a full copy | **(N+1)·`S`** | **+`S`** |

On the filesystem most Linux machines use, capture's *disk* effect is to **add one machine-wide copy
while changing nothing per workspace.** It still saves the *download* — N−1 fetches — which is a
real and filesystem-independent win, but a download saving is not a disk saving.

What share of real installs are on ext4 is not known, which is itself an argument for preferring a
mechanism that does not depend on the answer.

### Reclaiming a capture entry is never unsafe

A reflinked destination survives its source's unlink byte-identical. That is copy-on-write working
as specified, and it means deleting a store entry strands nothing on any materialize arm:

| Materialize arm | Delete the store entry | The workspace loses |
| :--- | :--- | :--- |
| reflink | destination keeps its own inode and its extents | nothing |
| hardlink | `nlink` drops; the workspace's link survives | nothing |
| copy | independent bytes | nothing |

So reclaiming a capture entry is an **efficiency** question with a policy answer, never a
correctness question needing an unreferenced oracle. The only cost of being wrong is a re-capture —
a download plus an installer run plus a throwaway jail, which is not free but is bounded.

**The reap rule is the COMPLEMENT OF THE RESOLVER.** `resolveCaptureFor` selects newest-by-receipt
per `(bin, platform)`, so every other entry is already unreachable by the only reader; the reap
deletes exactly what the resolver would not select. Derived from the reader rather than agreed with
it, so the two cannot drift — a change to newest-wins moves the reap set in the same motion.

> [!WARNING]
> **Do not add an age floor to the store reap, and do not raise its `K` above 1.** An age floor
> guards an in-flight window the completion marker already covers, and the one real race — a reap
> unlinking an entry mid-materialize — is not fixed by it and needs no fix, because a failed
> materialize is a *miss* and a miss falls through to the installer. A rollback target in the store
> has nowhere to be used: the store is not a version history, and a materialized older version is
> updated by evergreen within the launcher's update interval anyway.

Each reaped entry's capture manifest is kept — it sits beside the tree, not in it — so drift
comparison survives a reap for kilobytes.

## What capture is for

Priced honestly, because the disk argument is not it. Four things only capture gives, and **none of
them is a disk property**:

1. **A manifest — the install becomes observable.** A capture records what the installer wrote,
   path by path. Without it, reconcile compares a single file's digest.
2. **Offline, deterministic materialize.** A new workspace gets an agent with no network, and gets
   *the same bytes* rather than whatever `@latest` meant that day.
3. **Drift becomes reportable.** An immutable reference tree is what makes *"the vendor updated
   itself under you"* a statement anything can make. Without one, a self-update is invisible by
   construction.
4. **A lockable identity for a class with no lockfile.** yolo writes its own record only where no
   native one exists; the installer class is that gap, and the capture hash fills it.

> [!WARNING]
> **Auto-capture default-on makes a stale store entry actively harmful, and the V-axis prune is
> what bounds it.** The store serves the cold install and nothing after it, so a one-off entry
> *ages*; a new workspace then materializes a superseded version, pays the copy, and downloads the
> current one anyway — worse than no capture at all. `_prune_versions` is what deletes the seeded
> corpse at the moment the update creates it, which is why it is a **prerequisite** of auto-capture
> rather than a companion.

The cheap evolution, when a one-off store stops being good enough, is **not** automatic re-capture
— a host-side scheduled act running third-party installers unattended, on the vendor's cadence, is
a far larger trust and lifecycle surface. It is for **materialize to decline an entry older than a
threshold** and fall through to the vendor installer: local, offline, no scheduled act, no new trust
surface, and it bounds the wasted copy to entries young enough to still be current. Reach for that
first.

## What this does not cover

- **The capture design itself** — layout, admit, manifests, relocation, the capture sandbox
  profile. That is [`../design/program-delivery.md`](../design/program-delivery.md)'s, and this doc
  proposes no change to any of it.
- **Where an automatic reclaimer lives.**
  [`../design/minimal-disk-footprint.md`](../design/minimal-disk-footprint.md) owns the trigger
  question for every reclaimer yolo has.
- **The image ledgers.** Cache tars, the nix store closure and the container image store are a
  larger line item than program bytes by a wide margin, and nothing here changes a byte of them —
  see [`image-staging-vs-baking.md`](image-staging-vs-baking.md).
- **Whether the home tier model should change generally.** [`jail-home.md`](jail-home.md) owns the
  tiers. The three program surfaces are the only ones in scope here — explicitly not pack `state`
  dirs, credentials, `~/.config` or `~/.ssh`, whose per-workspace scope is load-bearing.
- **Trust and provenance.** A capture records what you got, not that a publisher signed it;
  [`../design/trust-paths.md`](../design/trust-paths.md) owns that.
- **Any claim about the population of filesystems.** The table above says what happens on each; it
  does not say how many users are on which.

## Why it's this way

Rulings a future change would otherwise undo, kept with their original IDs — those IDs are cited
from sibling docs and from the launcher template, and this appendix is where they resolve.

| Ruling | Why it holds |
| :--- | :--- |
| <a id="oq-cp1"></a>[**OQ-CP1**](#oq-cp1) — **the disk justification for capture is RETRACTED, and evergreen ships BEFORE capture, carrying the V-axis prune** | Both premises of the opposite ordering measured false: capture collapses **N** while evergreen multiplies **V**, and *"under capture there is nothing to prune"* is wrong because the self-updater keeps writing full-size version dirs into the workspace. On ext4 capture *adds* a machine-wide copy and saves no disk at all. What capture buys is a manifest, an offline deterministic materialize, drift against an immutable reference, and a lockable identity — none of them a disk property, which is exactly why the disk argument was the wrong one to sequence on. *"Sooner was never the goal"* still stands: this reverses which ruled subsystem goes first, not the scope of either. |
| <a id="oq-cp2"></a>[**OQ-CP2**](#oq-cp2) — **the program prefix stays per-workspace; a machine-global prefix is refused on the LOCK and the BLAST RADIUS, not on "capture would replace it"** | The per-workspace split provides a concurrency guarantee that is real and currently free: no two jails write one program tree. Going machine-global buys that back with an install-prefix lock that does not exist, and widens the blast radius from one workspace to every jail on the machine. **The refusal is not "capture is better"** — sharing is the only N-axis answer needing no store, no oracle, no sweep and no reflink, and the only one that works identically on ext4 and btrfs. If capture is ever abandoned, this is the option to reopen, and the lock is the thing to build first. |
| <a id="oq-cp3"></a>[**OQ-CP3**](#oq-cp3) — **no unreferenced oracle for the capture store; the reap rule is the complement of the resolver, `K = 1`, no age floor** | Reclaiming is safe on every materialize arm, so this is policy, not correctness. Deriving the rule from the resolver rather than agreeing with it is what stops the two drifting. Both invented idioms — a rollback `K` and an age floor — fall out and neither survives. |
| <a id="oq-cp4"></a>[**OQ-CP4**](#oq-cp4) — **the store serves the COLD INSTALL and nothing after it; an evergreen update does not materialize** | Closing the gap needs a new capture per vendor release, and `yolo capture` is a host act whose capture jail deliberately gets no store mount, so a launcher cannot trigger one. Automatic re-capture is the wrong evolution; a materialize that declines an over-age entry is the right one. Safe only while the V-axis prune ships with evergreen. |

## Current values

Verified at `41dde711`. The prose above explains what each of these is for; this table is the only
place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Version-prune retention | `KEEP_VERSIONS=2` — the live build plus one rollback target, per workspace | `internal/entrypoint` launcher template (`_prune_versions`) |
| Store reap retention | 1, newest-by-receipt per `(bin, platform)` | `internal/capture` (`PruneSupersededCaptures`, `select.go`) |
| Vendor version dir | `~/.local/share/<bin>/versions`, with `~/.local/bin/<bin>` the one referrer | `internal/entrypoint` launcher template |
| Launcher update interval / timeout / stale lock | `UPDATE_INTERVAL`, `UPDATE_TIMEOUT`, `STALE_LOCK` | `internal/entrypoint` launcher template |
| Auto-capture opt-out | `YOLO_NO_AUTO_CAPTURE=1` | `internal/cli/autocapture.go` |
| Dedupable / capturable home surfaces | the three program surfaces, from one list with both consumers named | `paths.HomeSurfaces` |
| Dedup opt-outs at `yolo prune` | `--no-hardlink`, `--dedup-global` | `internal/prune/prunecmd.go` |
| Reflink support | btrfs, XFS `reflink=1`, ZFS; **not ext4** | `internal/capture/clone_linux.go` (`errCloneUnsupported`) |
