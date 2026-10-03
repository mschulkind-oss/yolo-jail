---
status: current
verified: 2026-09-24
verified_commit: f491d192
covers:
  - internal/prune/imageroots.go
  - internal/prune/imageroots_probe.go
  - internal/prune/liveimageroots.go
  - internal/prune/currentimages.go
  - internal/prune/autoreap.go
  - internal/prune/probes.go
  - internal/prune/prunecmd.go
  - internal/cli/run/currentimage.go
  - internal/image/image.go
  - internal/image/stockimage.go
tags: [prune, images, storage, retention, gc-roots]
summary: "What a reap may delete, and on what evidence. A container image's reaper asks liveness and gets it from the runtime. The nix GC-root reaper never reaps the root of an image a container is running on, also asked of the runtime, and ages out every other root after a week. Image retention is one pointer per workspace at that workspace's current image, with no undo buffer and no global count; an unreachable authority declines the sweep rather than sweeping."
---

# Image and GC-root retention — two reapers, two questions

**Status:** CURRENT as of 2026-09-24, verified against `f491d192`.

yolo reclaims two different kinds of bytes, and confusing them once destroyed four running
jails. A **container image** in the runtime's own storage backs running containers: removing one
that is in use destroys the container. A **nix GC root** is a symlink that pins a store closure
against `nix-collect-garbage`. For an image no container is running on, losing its root costs a
rebuild and nothing else. For an image a container *is* running on, it can cost that jail its
tools: on podman/Linux the host `/nix/store` is mounted over the jail's own, so the jail executes
from the closure the root pins.

So the reapers ask two questions, and the whole of this document follows from that:

- **is anything using this?** A **liveness** question, answered by the runtime. The image reaper
  asks it of every image. The GC-root reaper asks it too, and never reaps the root of an image a
  container is running on ([OQ-LS4](#why-its-this-way)).
- **would I rather not rebuild this closure?** A **cache** question, answered by age alone. The
  GC-root reaper asks it of every root no container is running on
  ([OQ-LS1](#why-its-this-way)).

| Component | Lives in |
| :--- | :--- |
| The GC-root reaper and its retention horizon | `internal/prune` (`PruneOrphanImageRoots`, `ImageRootRetention`) |
| Mapping a running container's image to its GC root, and the store GC's refusal | `internal/prune` (`liveimageroots.go`) |
| The stock record: which store path a stock tag names | `internal/image` (`stockimage.go`) |
| The image reaper, its liveness veto and its decline vocabulary | `internal/prune` (`PruneOldImages`, `ImageReapDecline`, `probes.go`) |
| The per-workspace current-image pointers | `internal/prune` (`currentimages.go`), `internal/cli/run` (`currentimage.go`) |
| The debounced automatic pass | `internal/prune` (`AutoReapOldImages`, `DueForAutoImageReap`) |
| The load sentinel itself — writer, reader, cap | `internal/image` (`AddLoadedPath`, `ReadLoadedPaths`) |
| What still reads the sentinel | the load diagnosis, and nothing else |

**Reads with:** [`image-staging-vs-baking.md`](image-staging-vs-baking.md) (how an image is built,
addressed and delivered — including the stock-tag short-circuit that changed what the sentinel
records), [`storage-and-config.md`](storage-and-config.md) (where machine-wide state lives and what
serializes it).

---

## Principles

**P1. Recency is a cache policy, not a liveness proof.** A bounded most-recently-used list answers
*"what would I like to still have"*. It cannot answer *"what is in use"*.

**P2. A destructive sweep asks the authority.** Where an authority exists and is cheap — the
runtime's own container and image listings — evidence derived from a side-file is a guess.

**P3. Unknown is not permission.** If the authority cannot be reached, the sweep declines. This
holds throughout `internal/prune`, the GC-root reaper included. From
[OQ-LS1](#why-its-this-way) until [OQ-LS4](#why-its-this-way) that reaper was the one exception,
because an age cutoff consults no authority it could fail to reach. It asks the runtime again now,
so a runtime that cannot answer, or a running jail whose image yolo cannot map to a root, means it
reaps nothing that pass.

## The load sentinel, and what it may be cited for

The **load sentinel** *(the term is this repo's)* is one file per container runtime holding recently
used nix store paths, most-recent-last, deduplicated on write and capped at a small fixed number of
entries. It is a most-recently-used list and nothing more.

Three properties decide what it can be evidence for, and all three point the same way:

- **It is appended on launch**, so a jail that is *up* but not *relaunching* contributes nothing
  and ages off the end while still in use.
- **The reader discards the order**, treating the file as an unordered set, so "how recent" is not
  even available to a consumer.
- **A launch that matches the stock tag appends only when this host recorded the image's store
  path** ([the stock record](image-staging-vs-baking.md#the-stock-tag-and-the-question-asked-before-the-build)).
  One with no valid record appends nothing, and an attach to a running jail never does.

> [!WARNING]
> **Never cite the sentinel as liveness.** It was once the load decision's oracle and was demoted
> to diagnosis when the image ref became content-addressed, on exactly the reasoning above: ask the
> runtime, do not infer from a history file. That demotion was half-finished for a while, and the
> gap is what killed four jails that had been up for days — ten distinct store paths were loaded
> after they last launched, the images' sort key was when the archive was *streamed* so the
> longest-running jail sorted oldest, and a forcing removal took the running containers with the
> images.

What still reads it, legitimately: the human-readable *"why did this load?"* diagnosis, and nothing
else. **Image retention has not read it since [OQ-LS3](#why-its-this-way)**, and the store GC's
refusal stopped reading it with [OQ-LS4](#why-its-this-way): that half refused the GC for recently
loaded closures no container runs, which is a cache the age cutoff is entitled to give up.

## Image retention — one pointer per workspace

**The unit of retention is the configuration, and there is no undo buffer.** Keep each
configuration's current image; never hold a superseded copy in order to go back to it. That is the
whole rule, and it is what left a global count with nothing to bound — so the count was **removed
rather than retuned**, and passing its flag is an error rather than being silently ignored.

> [!WARNING]
> **A global "keep the newest N" window is not a smaller version of this rule; it is a different
> and wrong one.** It sorted every image row by creation time with no notion of a workspace or a
> configuration, so on a machine with several workspaces it evicted N images per pass however
> recently each had been used — and because the sort key was when the archive was streamed, the
> longest-running jail sorted first. (Every image now carries the same constant creation time, so
> that sort would not even be an order any more.) Reintroducing any global count reintroduces the
> first defect whatever it sorts by.

**The pointer is not a configuration identity, and confusing the two is the trap.** Grouping images
*by* configuration looks like it needs a value equal across every image of one config, and nothing
in the tree records one — the image store key is per image, and the flake's image identity is
deliberately invariant across `packages:` lists. That is a real dead end and it is the wrong
problem: **a grouping key is only needed when a group has more than one member.** With zero
superseded copies, "each configuration's current image" is a *set of pointers*, one per workspace,
and the launcher already knows the store path it just used.

Four properties of the pointers, each of which a plausible change would get wrong:

- **They are machine-wide state, not per-workspace state.** A single pointer read by its own
  workspace could live under that workspace's state dir; the *reaper* needs the union, and it has
  no way to enumerate workspaces that is not itself a registry. So they are keyed by the
  deterministic container name and sit beside the sentinel and the GC roots.
- **No honoured pointer means the pass declines.** That tri-state *is* the migration: a machine
  upgraded but not yet relaunched has no pointers, and nothing is reaped on the strength of an
  absence.
- **A pointer whose workspace is gone protects nothing, and its file is kept anyway.** A deleted
  workspace has no configuration to retain for; the file is tiny, and a workspace can be
  temporarily absent, so deleting it would cost the re-stream it would save.
- **One-per-configuration is the floor, not a target to beat.** A configuration whose current image
  is in use is protected by liveness regardless of any count, and under a shared-base layer plan
  every kept image also holds the base in place.

**Liveness comes from the runtime, and removal does not force.** The container listing yields the
image IDs with containers on them and nothing in that set is ever selected. Removal is then a plain
delete rather than a forcing one, so the safety survives a *wrong* answer instead of depending on a
right one — an in-use image simply refuses to be removed.

## GC-root retention — age, except under a running image

The GC-root reaper has two rules, and which one a root gets depends on a single question: is a
container running on its image right now?

**No container on the image: an age cutoff.** Reap a root untouched for longer than the
retention horizon. Its question is a prediction about future want, and liveness predicts that badly
in both directions: a jail stopped five seconds ago is not live, and a jail that ran for three weeks
last month pins a closure nobody will build again. That is [OQ-LS1](#why-its-this-way), and it
still holds for every root below.

**A container on the image: never reaped, however old.** This is [OQ-LS4](#why-its-this-way). The
reaper asks the runtime which images have running containers, and maps each one's ref to the root
that pins its closure. A content-addressed ref maps by its tag. A stock ref maps through the
**stock record** *(the term is this repo's: one small file per image identity, holding the store
path the load that wrote the stock tag built the image from)*. The rule is the one the install
prefix's roots already had, for the same reason: a running jail executes from the closure. With it
in place, a plain `nix-collect-garbage` on any schedule is safe for every jail yolo can map.

The reaper reaps **nothing** that pass when it cannot tell which roots are live. That happens when
the runtime cannot be asked, or when a running jail's image cannot be mapped: the legacy `latest`
tag, a stock tag with no record, or a bare image ID. A container from any other image repository is
not a yolo jail and is ignored. A root whose target is already gone pins nothing, so it is still
reaped once old, even under a running image.

> [!IMPORTANT]
> **The age half's clock is the last *fresh* launch of the image, not the last use.** Adding a GC
> root refreshes the link's own modification time even when it already points at the same store
> path. A launch that builds the image does that, and so does one that matches the stock tag with a
> valid stock record. An attach to a running jail does not, and neither does a jail simply staying
> up. That is survivable only because of the liveness half. Before [OQ-LS4](#why-its-this-way), a
> jail up for longer than the horizon lost its root, and on podman/Linux the next store GC could
> break it. That is how a host GC left a running jail's `/bin` dangling on 2026-07-22
> ([`storage-lifecycle.md`](../plans/storage-lifecycle.md)).

- ✅ <a id="oq-ls4"></a>**[`OQ-LS4`](#oq-ls4) — does [OQ-LS1](#why-its-this-way)'s premise, that
  losing an image root costs a rebuild and never a running jail, hold on podman/Linux?** There,
  with a host nix daemon, the host `/nix/store` is bind-mounted read-only over the jail's
  (`hostNixStore`, `internal/cli/run/assemble.go`), which is how a host GC broke a running jail's
  `/bin` on 2026-07-22 ([`storage-lifecycle.md`](../plans/storage-lifecycle.md)). A jail up for
  more than the retention horizon on an image no later launch rebuilt then has an unrooted closure
  again. Read from code, not measured. Filed 2026-09-26.

  <!-- vantage: question id=OQ-LS4 -->

  **Answer (ruled 2026-09-28):** No, it does not hold for a running image. Proposed to the
  maintainer: *"rule [OQ-LS4](#oq-ls4) so the image root of a running container is held by liveness, the way
  the mounted yolo binaries' roots already are — the age reaper skips any image a container is
  running on; then plain nix-collect-garbage on any schedule is safe for every yolo jail, and the
  guard becomes redundant and can be deleted"*. The maintainer: *"yes, record that ruling"*. How it
  was built is in [the ledger](#why-its-this-way), [OQ-LS4](#oq-ls4) and LS-D1 to LS-D5.

## The store GC's refusal

`yolo prune --nix-gc` runs a bounded `nix store gc`, capped at a size ceiling, and refuses inside a
jail. Before it runs, it asks the runtime which images have running containers. It then refuses
only in the two cases the liveness rule above cannot cover:

- **a running image yolo cannot map to a root at all**: no tag, an image from another repository,
  the legacy `latest`, or a stock tag with no stock record;
- **a mapped image whose root does not exist**. Liveness can keep a root, but it cannot create
  one. A registration that failed at launch leaves this state. So does a jail started before stock
  records existed, if its root has since aged out.

Each refused image is named with its reason. The remedy is a **fresh** host launch of that image's
workspace, which registers the root and, for a stock image, writes the stock record. It is not an
attach, which loads no image. It is not `just load` either, which registers no root under the
build dir. A container yolo did not start has to be stopped for the length of the GC.

## Declining, and how loud

A decline is not the same as "nothing to reclaim", and the volume follows from which of the two it
is:

- **Where the user asked for the work**, a declined sweep is an **error**: the command exits
  non-zero naming the missing evidence and what supplies it.
- **Where nobody asked** — the automatic pass — a decline is **recorded**, and a debounced pass
  that simply is not due yet says nothing at all and carries no reason.

> [!WARNING]
> **"Loud" never means the terminal.** The automatic path's notices went to stdout, then to stderr,
> and stderr is the same terminal — so a reclaim printed on top of a running agent's TUI. Loud means
> *not silent*: the record goes to the workspace's housekeeping log, and the non-zero exit lands
> where a human actually asked.

To make "declined" mean *prevented work* rather than *fresh machine*, the candidate listing runs
**before** the guards. A sweep that could never have selected anything is not a decline.

## Failure modes

- **The runtime's container list is unreadable, errors, or times out** → decline the entire sweep,
  not just the entry that failed.
- **A removal fails because the image is in use** → that is the guard working, not an error to
  report loudly.
- **No sentinel, or an empty one** → the image reaper does not consult it at all, and the GC-root
  reaper does not either; an absent sentinel is not a condition for either.
- **More live jails than the sentinel's cap** → irrelevant, which is the point. The cap stopped
  bounding safety when liveness got its own evidence.

## What this does not license

- **Deleting the sentinel.** It is still the right instrument for the load diagnosis. Nothing
  else reads it, and nothing should start to: it is not liveness evidence.
- **Restoring a global image count** in any spelling — see the warning above.
- **Extending the GC-root reaper's liveness rule past running containers**, to recently stopped
  ones for example. That is [OQ-LS1](#why-its-this-way) run backwards. [OQ-LS4](#why-its-this-way)
  covers exactly the case where liveness is the right answer, a jail executing from the closure,
  and no other.
- **A heartbeat that re-appends to the sentinel while a jail runs.** It invents a liveness signal
  that already exists, and adds a second writer to a file with no locking.
- **Protecting by container name rather than by image.** Names are per workspace; the reaper's unit
  is the image, and one image can back several jails.
- **A new persistence format, database or daemon**, and no change to capture-store GC, which has
  its own model.

> [!NOTE]
> **Sorting images by last-used rather than by creation time is deferred, not rejected — and it no
> longer decides anything about retention.** With the count gone, the sort orders only the
> report; and since layer-aware delivery every image reports the same constant creation time, so
> today's sort is a stable no-op
> ([`image-staging-vs-baking.md`](image-staging-vs-baking.md#what-a-copy-reports), [`OQ-LI4`](image-staging-vs-baking.md#why-its-this-way)). A
> last-used order would need a signal that does not exist, and liveness already makes it
> unnecessary for *safety*; it would be a report improvement, not a retention one.

## Why it's this way

Rulings a maintainer reading only the normative text would otherwise undo. The ids are cited from
`internal/prune`, `internal/cli` and `AGENTS.md`. The `LS-D` rows are implementation decisions made
while building [OQ-LS4](#oq-ls4) on 2026-09-28, not maintainer rulings.

| ID | Ruling | Why it holds |
| :--- | :--- | :--- |
| OQ-LS1 | **No liveness veto for the GC-root reaper — an age cutoff, and a size cap only after age.** Ruled *against* the leaning. *Amended by [OQ-LS4](#oq-ls4)* for the root of an image a container is running on | The question is a prediction about future want, and liveness is a wrong predictor in both directions. The reaper loses its protected set and its liveness gate, and [P3](#principles) stopped applying to it (until [OQ-LS4](#oq-ls4)) because an age policy consults no authority it could fail to reach |
| OQ-LS2 | **A decline says so: an error where the user asked, and nothing where they did not** | The proposed "one dim line" was the wrong volume — the only routine case says nothing at all. Moving the candidate listing before the guards is what makes "declined" mean *prevented work* rather than *fresh machine* |
| OQ-LS3 | **A global keep-count is the wrong mechanism, not the wrong number, and there is no undo** | The window was global and had no notion of a workspace or a configuration. The unit is the configuration and the superseded-per-config count is zero, which leaves a count with no depth to bound. It was replaced by the per-workspace pointers, not tuned |
| OQ-LS4 | **The root of an image a container is running on is held by liveness, like the install prefix's roots; the age cutoff keeps every other root.** Ruled 2026-09-28: *"yes, record that ruling"* | [OQ-LS1](#why-its-this-way)'s premise, that a lost root costs only a rebuild, is false for a running image on podman/Linux, where the jail executes from the host store. With the running roots held, a plain `nix-collect-garbage` on any schedule is safe for every jail yolo can map, and the store GC's refusal shrinks to what liveness cannot give (LS-D4). The reaper asks the runtime again, so [P3](#principles) applies to it again |
| LS-D1 | *Implementation decision.* **A stock tag maps to its root through a stock record** written by the load that writes the tag: one file per identity under the build dir's `stock-images/`, holding the store path. It is written whenever a stock launch tags the image, including when the image was already present under its content ref, so a tag an older yolo wrote gains its record on the first launch that finds it unrecorded | The tag carries the flake's identity and the root is keyed by the store path's hash. Only the load knows both. Asking the runtime for the image's other tags would also work, but it costs a probe per ref and still has no answer for a stock-tagged archive this host never built |
| LS-D2 | *Implementation decision.* **A stock-tag match roots the recorded store path, but only after `nix-store --check-validity` proves the path is still in the store**. It also appends the path to the load sentinel and returns it as the launch's store path, so the workspace's current-image pointer names it | Rooting an invalid path is `--realise`, which substitutes or builds: the build the match exists to skip. Before this, a match registered no root, so a normally launched jail's root aged out under it, and the store GC could not map its ref and refused while it ran |
| LS-D3 | *Implementation decision.* **A match with no valid record builds when the jail will read the host store, and runs, disclosing the missing root, when it will not.** The deciding input is the launcher's own predicate for mounting the host store | A jail that reads the host store would start with its tools missing. One that does not runs on the image's own copy and loses nothing, and on a darwin host with no Linux builder, building is the failure the short-circuit exists to avoid |
| LS-D4 | *Implementation decision.* **The store GC's refusal keeps two cases: a running image yolo cannot map to a root, and a mapped one whose root does not exist.** The sentinel-based half, which refused for recently loaded closures without a root, is deleted along with its readers | Liveness keeps a root and cannot create one, so the second case stays. The maintainer's rule deletes a second path that compensates for a yolo bug; this check instead detects a state the reaper cannot reach. The deleted half protected closures no container runs, and after [OQ-LS1](#why-its-this-way) that is a cache |
| LS-D5 | *Implementation decision.* **An unanswerable runtime, or a running jail whose image cannot be mapped, makes the GC-root reaper reap nothing, reported as a dim skip naming the missing evidence.** It is not a failed command. Containers from other repositories are ignored | Reaping is the dangerous direction, and any root could belong to an unmappable jail. The skip matches the install-prefix reaper beside it, and like that one it is an exception to [how loud a decline is](#declining-and-how-loud). It clears once that jail is relaunched. A foreign container runs on no root of yolo's. Apple Container is asked with `container ls` and its IMAGE column, because it has no `ps --format`; without that, every `yolo prune` there would stop reaping image roots. That column's exact spelling is read from test fixtures, not from a Mac |

## Current values

Verified at `f491d192`. The prose above says what each of these is for; this table is the only place
the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| GC-root retention horizon | one week | `prune.ImageRootRetention` |
| Load sentinel | one file per runtime under the machine-wide build dir, `last-load-<runtime>` | `internal/image` (`AddLoadedPath`) |
| Sentinel capacity and order | a small fixed cap, most-recent-last, deduplicated on write | `internal/image` (`AddLoadedPath`) |
| Current-image pointers | one file per workspace under the machine-wide build dir, holding the store path and the workspace that recorded it | `internal/prune` (`currentimages.go`) |
| Stock records | one file per image identity under the machine-wide build dir's `stock-images/`, named by the identity's hex digest and holding one store path | `internal/image` (`stockimage.go`) |
| Automatic-reap debounce | once per day, per machine | `internal/prune` (`DueForAutoImageReap`) |
| Automatic-reap hatch | `YOLO_NO_AUTO_IMAGE_REAP=1` | `internal/cli/run` |
| The removed flag | `--keep-images` — **refused**, not ignored | `internal/cli` (`commands.go`, `subhelp.go`) |
| Housekeeping record | `<workspace>/.yolo/housekeeping.log` | `internal/cli/run` |
