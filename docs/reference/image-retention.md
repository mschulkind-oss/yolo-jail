---
status: current
verified: 2026-09-24
verified_commit: f491d192
covers:
  - internal/prune/imageroots.go
  - internal/prune/imageroots_probe.go
  - internal/prune/currentimages.go
  - internal/prune/autoreap.go
  - internal/prune/probes.go
  - internal/prune/prunecmd.go
  - internal/cli/run/currentimage.go
  - internal/image/image.go
tags: [prune, images, storage, retention, gc-roots]
summary: "What a reap may delete, and on what evidence. Two reapers ask two different questions — a container image's reaper asks liveness and gets it from the runtime; the nix GC-root reaper asks a cache question and gets a pure age cutoff. Image retention is one pointer per workspace at that workspace's current image, with no undo buffer and no global count; an unreachable authority declines the sweep rather than sweeping."
---

# Image and GC-root retention — two reapers, two questions

**Status:** CURRENT as of 2026-09-24, verified against `f491d192`.

yolo reclaims two different kinds of bytes, and confusing them once destroyed four running
jails. A **container image** in the runtime's own storage backs running containers: removing one
that is in use destroys the container. A **nix GC root** is a symlink that pins a store closure
against `nix-collect-garbage`: losing one costs a rebuild and never a running container, because
the runtime already holds its own copy of the image.

So the two reapers are asking different questions, and the whole of this document follows from
that:

- the image reaper asks **is anything using this?** — a **liveness** question, answered by the
  runtime;
- the GC-root reaper asks **would I rather not rebuild this closure?** — a **cache** question,
  answered by age alone.

| Component | Lives in |
| :--- | :--- |
| The GC-root reaper and its retention horizon | `internal/prune` (`PruneOrphanImageRoots`, `ImageRootRetention`) |
| The image reaper, its liveness veto and its decline vocabulary | `internal/prune` (`PruneOldImages`, `ImageReapDecline`, `probes.go`) |
| The per-workspace current-image pointers | `internal/prune` (`currentimages.go`), `internal/cli/run` (`currentimage.go`) |
| The debounced automatic pass | `internal/prune` (`AutoReapOldImages`, `DueForAutoImageReap`) |
| The load sentinel itself — writer, reader, cap | `internal/image` (`AddLoadedPath`, `ReadLoadedPaths`) |
| What still reads the sentinel | `internal/prune` (`ProtectedImagePaths`), and the load diagnosis |

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
holds throughout `internal/prune`, with one deliberate exception named below: it does not apply to
the GC-root reaper, because that reaper consults no authority it could fail to reach.

## The load sentinel, and what it may be cited for

The **load sentinel** *(the term is this repo's)* is one file per container runtime holding recently
used nix store paths, most-recent-last, deduplicated on write and capped at a small fixed number of
entries. It is a most-recently-used list and nothing more.

Three properties decide what it can be evidence for, and all three point the same way:

- **It is appended on launch**, so a jail that is *up* but not *relaunching* contributes nothing
  and ages off the end while still in use.
- **The reader discards the order**, treating the file as an unordered set, so "how recent" is not
  even available to a consumer.
- **Since the stock-tag short-circuit, a launch that builds nothing appends nothing** — and on an
  unchanged flake that is the common case rather than a degraded one. A *relaunching* jail need not
  refresh it either.

> [!WARNING]
> **Never cite the sentinel as liveness.** It was once the load decision's oracle and was demoted
> to diagnosis when the image ref became content-addressed, on exactly the reasoning above: ask the
> runtime, do not infer from a history file. That demotion was half-finished for a while, and the
> gap is what killed four jails that had been up for days — ten distinct store paths were loaded
> after they last launched, the images' sort key was when the archive was *streamed* so the
> longest-running jail sorted oldest, and a forcing removal took the running containers with the
> images.

What still reads it, legitimately: the store-GC refusal's protected set, which is recency answering
a cache question, and the human-readable *"why did this load?"* diagnosis. **Image retention has
not read it since [OQ-LS3](#why-its-this-way).**

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

## GC-root retention — a pure age policy, deliberately

The GC-root reaper gets **no liveness evidence at all**, and this inverts the rule above on
purpose. Its question is a prediction about future want, and liveness is a wrong predictor in both
directions: a jail stopped five seconds ago is not live, and a jail up three weeks pins a closure
nobody will build again. So it is an age cutoff — reap a root untouched for longer than the
retention horizon — with any size cap applied *after* age and never instead of it.

This is also why [P3](#principles) stops applying to this reaper rather than being weakened for it:
an age policy reads the modification time off the link and has no authority it could fail to reach.

> [!IMPORTANT]
> **What that modification time means changed, and the meaning is weaker than it looks.** Adding a
> GC root refreshes the link's own mtime even when it already points at the same store path, so the
> mtime is the last time a launch **built and delivered** this image — not the last time one used
> it. Since the stock-tag short-circuit, a launch that finds a matching image already in the
> runtime returns before the build and registers no root, so an image used nightly can age past the
> horizon untouched. That is survivable and is why this stayed a pure age cutoff: what the root
> protects is the *store* copy of an image the runtime already holds, so reaping one costs a
> rebuild on the next cache miss and never a broken launch.

- 💬 <a id="oq-ls4"></a>**[`OQ-LS4`](#oq-ls4) — does [OQ-LS1](#why-its-this-way)'s premise, that
  losing an image root costs a rebuild and never a running jail, hold on podman/Linux?** There,
  with a host nix daemon, the host `/nix/store` is bind-mounted read-only over the jail's
  (`hostNixStore`, `internal/cli/run/assemble.go`), which is how a host GC broke a running jail's
  `/bin` on 2026-07-22 ([`storage-lifecycle.md`](../plans/storage-lifecycle.md)). A jail up for
  more than the retention horizon on an image no later launch rebuilt then has an unrooted closure
  again. Read from code, not measured. Filed here 2026-09-26, beside the ruling it questions:
  the storage-lifecycle plan reports the disagreement for a ruling and says it is not where that
  is decided, and nothing records one yet.

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

- **Deleting the sentinel.** It is the right instrument for the store-GC refusal's protected set
  (`UnrootedProtectedPaths` over `ProtectedImagePaths`: a recently loaded closure without a root
  refuses `yolo prune --nix-gc`) and for the load diagnosis. The GC-root reaper itself reads
  neither it nor liveness.
- **Restoring a global image count** in any spelling — see the warning above.
- **Giving the GC-root reaper a liveness veto**, which is [OQ-LS1](#why-its-this-way) run backwards.
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
`internal/prune`, `internal/cli` and `AGENTS.md`.

| ID | Ruling | Why it holds |
| :--- | :--- | :--- |
| OQ-LS1 | **No liveness veto for the GC-root reaper — an age cutoff, and a size cap only after age.** Ruled *against* the leaning | The question is a prediction about future want, and liveness is a wrong predictor in both directions. The reaper loses its protected set and its liveness gate, and [P3](#principles) stops applying to it rather than being weakened, because an age policy consults no authority it could fail to reach |
| OQ-LS2 | **A decline says so: an error where the user asked, and nothing where they did not** | The proposed "one dim line" was the wrong volume — the only routine case says nothing at all. Moving the candidate listing before the guards is what makes "declined" mean *prevented work* rather than *fresh machine* |
| OQ-LS3 | **A global keep-count is the wrong mechanism, not the wrong number, and there is no undo** | The window was global and had no notion of a workspace or a configuration. The unit is the configuration and the superseded-per-config count is zero, which leaves a count with no depth to bound. It was replaced by the per-workspace pointers, not tuned |

## Current values

Verified at `f491d192`. The prose above says what each of these is for; this table is the only place
the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| GC-root retention horizon | one week | `prune.ImageRootRetention` |
| Load sentinel | one file per runtime under the machine-wide build dir, `last-load-<runtime>` | `internal/image` (`AddLoadedPath`) |
| Sentinel capacity and order | a small fixed cap, most-recent-last, deduplicated on write | `internal/image` (`AddLoadedPath`) |
| Current-image pointers | one file per workspace under the machine-wide build dir, holding the store path and the workspace that recorded it | `internal/prune` (`currentimages.go`) |
| Automatic-reap debounce | once per day, per machine | `internal/prune` (`DueForAutoImageReap`) |
| Automatic-reap hatch | `YOLO_NO_AUTO_IMAGE_REAP=1` | `internal/cli/run` |
| The removed flag | `--keep-images` — **refused**, not ignored | `internal/cli` (`commands.go`, `subhelp.go`) |
| Housekeeping record | `<workspace>/.yolo/housekeeping.log` | `internal/cli/run` |
