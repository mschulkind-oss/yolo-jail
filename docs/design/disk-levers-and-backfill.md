---
title: "Disk levers and backfill — the bytes the fixes left behind, and whether yolo deletes them or offers to"
date: 2026-09-06
status: accepted # re-stamped 2026-09-09: ruled 2026-09-08 and nine of ten rulings built, so it is no longer in review
tags: [design, disk, prune, podman, nix, backfill]
summary: "Two questions the maintainer asked together: what are the big space-reduction levers, ranked and costed; and how does yolo clean up — or offer to clean up — the stores that already grew before each fix. Nine of the ten rulings shipped 2026-09-08/09 — including the finding that yolo's own nix outputs were never collected, which now has a collector. OQ-BF10, host-CAS aliasing, is the deferred slice."
vantage:
  status-chip: true
---

# Disk levers and backfill — the bytes the fixes left behind, and whether yolo deletes them or offers to

**Status:** RULED 2026-09-08, and MOSTLY BUILT 2026-09-09 — see [§11.1](#111-decision-ledger)'s
Built column for which rulings shipped and which did not ([OQ-BF10](#OQ-BF10) is a later slice by
its own ruling; [OQ-BF9](#OQ-BF9)'s sibling [OQ-LS3](./the-load-sentinel-is-not-a-liveness-oracle.md#111-decision-ledger)
is blocked on a key that does not exist yet). Written as a design sketch on 2026-09-06, when nothing
was built. Every number below was measured in this
development jail on 2026-09-06 (times given where the store moved during the day) and is
labelled **MEASURED** / **NOT MEASURED** in the manner of the measurements behind
[`image-staging-vs-baking.md`](../reference/image-staging-vs-baking.md#cost-model).

**The short version.** The maintainer's two questions are one distinction. **Steady-state
retention** — stopping new growth — is mostly shipped: C3 stopped the tars, C8 stopped the
per-commit image rebuild, [OQ-DF3](./minimal-disk-footprint.md#OQ-DF3) gave the podman reaper a
trigger. **Backfill** — the bytes already on disk from before each fix — is the larger number on
this machine today and nothing addresses it: **~49 GiB** of cache files older than the
30-day rule that `yolo prune` would already purge, **~52.7 GB** of podman images the shipped reaper has never
been allowed to run against, **~20.6 GB** of image tars that are dead on a streaming runtime but
kept by a `keep=3` default, and **≥ 28.8 GB** of yolo's own unrooted `/nix/store` outputs that no
collector has ever visited, because **nothing on this machine ever runs `nix store gc`** —
not the daemon (`min-free = 0`) and not yolo (opt-in, human-typed). [§3](#3-the-levers-ranked) ranks the
levers by measured bytes and by effort. [§5](#5-the-shape-measure-late-offer-early-delete-in-the-slot)
proposes the disposition: **automatic** where a reaper's evidence is complete and regeneration is
cheap (the same footing [OQ-DF3](./minimal-disk-footprint.md#OQ-DF3) already ruled for images), **offered while non-zero** ([OQ-BF1](#OQ-BF1)) where the
evidence is partial, the store is shared, or the re-fetch is expensive — and in both cases a
first pass over a backlog is a **one-time event per store per machine**, remembered, that must
never delay a launch.

**The most important section is [§4](#4-why-backfill-and-steady-state-do-not-share-a-disposition)**, because it
is where the maintainer's phrase *"offer to clean up"* turns into a rule an implementer can apply
to a store nobody has measured yet.

**Scope note — why this is a sibling and not more sections in [`minimal-disk-footprint.md`](minimal-disk-footprint.md).**
That doc owns the *mechanism* for the three copies of a loaded image (Ledgers A, B, C), and its
open questions [OQ-DF2](./minimal-disk-footprint.md#OQ-DF2)/[OQ-DF3](./minimal-disk-footprint.md#OQ-DF3)/[OQ-DF4](./minimal-disk-footprint.md#OQ-DF4)
stay its. Its [§8](./minimal-disk-footprint.md#8-what-this-does-not-cover) explicitly declines the host
`/nix/store` beyond yolo's roots, the per-workspace overlays and the uncovered cache subdirs —
which is most of what the maintainer's second question is about. Three things put the material
here rather than there: the lever table is *cross-store* (images, store garbage, caches, vendor
versions, captures, worktrees) and that doc is image-scoped by design; the backfill disposition is
a *policy* that has to apply to every store the same way, not a per-ledger mechanism; and that
doc's status paragraph has already been rewritten three times in twelve days — the skill's own
rule is to split when a use case grows its own blockers. Where this doc reaches a number that is
minimal-disk-footprint's knob (the tar `keep`, the component that deletes Ledger B), it says so
and defers.

**Reads with:**

- [`minimal-disk-footprint.md`](minimal-disk-footprint.md) — the three ledgers, invariants P1–P7, and the
  [OQ-DF3](./minimal-disk-footprint.md#OQ-DF3) ruling whose automatic-on-launch disposition this doc
  either extends or distinguishes, per store.
- [`image-staging-vs-baking.md`](../reference/image-staging-vs-baking.md) — the [cost model](../reference/image-staging-vs-baking.md#cost-model)
  (the layer-chain and time measurements), [store-delivered packages](../reference/image-staging-vs-baking.md#store-delivered-packages)
  and [the mounted prefix](../reference/image-staging-vs-baking.md#the-mounted-prefix) (shipped today), and [OQ-6](../reference/image-staging-vs-baking.md#why-its-this-way) (C6).
- [`../plans/storage-lifecycle.md`](../plans/storage-lifecycle.md) — the consumer map, the rooting
  work, and the bounded store GC this doc proposes to replace with something narrower.
- [`agent-cli-copies.md`](../reference/agent-cli-copies.md) [§5.1](../reference/agent-cli-copies.md#the-v-axis-prune) and
  [`../plans/evergreen-agent-updates.md`](../plans/evergreen-agent-updates.md) — A7, the vendor-version prune, which is the one reclaimer here whose steady state shipped with a trigger that skips its own backfill.

---

## 1. The verdict, and the two problems the question turns on

Two terms, both coined in this doc because the corpus uses neither in this sense (checked
2026-09-06: "backfill" appears only as "backfill missing tests"):

**Steady-state retention** *(coined here)* — bounding the growth a mechanism produces from now
on: a reaper with a trigger, or a write path that leaves nothing behind. It says nothing about
what is already on disk. Not a budget (a budget is a number; this is a rule), and not a backfill.

**Backfill** *(coined here)* — the bytes a mechanism put on disk *before* its steady-state fix
shipped, which the fix stopped producing but did not remove. Not a leak (a leak is still growing)
and not the steady state's own residue (a `keep=2` window is retention, not backfill). The
defining property: **the reaper's own evidence usually does not reach it.** The load sentinel is
an LRU of ten; the pre-C2 `<none>` rows have no tag; an install prefix realized in July has no
ledger at all. A steady-state reaper knows what it made; a backfill pass has to reason about what
something else made.

Four principles, numbered so [§5](#5-the-shape-measure-late-offer-early-delete-in-the-slot) and the open questions can cite them:

**P1. Backfill and steady state get separate dispositions, decided per store.** One is "delete a
superseded artifact you can prove is superseded, daily"; the other is "delete tens of gigabytes,
once, most of it older than your own records." [§4](#4-why-backfill-and-steady-state-do-not-share-a-disposition)
says why the [OQ-DF3](./minimal-disk-footprint.md#OQ-DF3) precedent transfers to some stores and not to others.

**P2. A backfill pass is a one-time event per store per machine, and it is remembered.** Once the
backlog is gone the steady-state reaper owns the store; the pass never recurs, and its outcome —
done, declined, or never — is a stamp on disk, not a memory.

**P3. Automatic where the reaper's evidence is complete and regeneration is bounded and known.**
Complete evidence means the same veto that protects live state protects everything the pass could
touch. Bounded regeneration means "a `nix build` or a `podman load`", measured, not "a re-download
of unknown size".

**P4. Offered — once, on a TTY, with the size — where the evidence is partial, the store is shared
with the user's non-yolo work, or regeneration is expensive.** An offer is a prompt, not a hint:
[`minimal-disk-footprint.md`](minimal-disk-footprint.md) [§1.1](./minimal-disk-footprint.md#11-what-a-human-noticing-is-worth-measured)
measured what a hint is worth (404 GiB, 33 days), and this doc does not propose another one.

Two negatives up front, because both are things a reasonable implementer might otherwise do:
**this doc never proposes a daemon, a timer, or a cron entry** (minimal-disk-footprint's A6 stands),
and **it never proposes that yolo delete anything under a workspace working tree except its own
`.yolo/`** — the worktrees in [§2.1](#21-every-store-one-table) are named so they are not mistaken for yolo's.

---

## 2. Measured, 2026-09-06

### 2.1 Every store, one table

Levels, growth, who reclaims today, and how much of the level is backfill. "Trigger" is the
*only* thing that runs the reclaimer. The host `GLOBAL_CACHE` is visible from this jail at
`~/.cache` (a rw bind of the host's `~/.local/share/yolo-jail/cache`, per
[`jail-home.md`](../reference/jail-home.md)); the nested jail's own state dir is `~/.local/share/yolo-jail`.

| Store | Level (MEASURED unless marked) | Growth | Reclaimer / trigger today | Backfill |
| :--- | :--- | :--- | :--- | :--- |
| Podman image store (nested, this jail) | **30 rows / 29 images / 52.73 GB**, 100 % reclaimable (0 containers) at ~23:20; **24 images / 38.68 GB** earlier the same day, before six C4/C5/C8 verification launches. 28 are `yolo-jail`: 24 tagged, 4 `<none>` | 28 images between 2026-09-04 13:54 and 2026-09-06 21:45 — ~12/day at the dev-loop rate | `PruneOldImages` (`internal/prune/probes.go`), veto-protected; **auto-trigger shipped today** (`AutoReapOldImages`), debounced 24 h. **Has never fired here** — `build/last-image-reap` is absent | The whole store: the reap's first pass selects **14 of 24** tagged images ([§2.2](#22-the-image-reap-priced-against-this-store)) |
| yolo's own outputs in `/nix/store` | **231** `*-yolo-jail-install-prefix` paths, **19.77 GB**, **220 unrooted**; **245** `*-yolo-jail-go-0-dev` paths, **10.02 GB**, **all 245 unrooted**; **316** `*-stream-yolo-jail` scripts, 298 unrooted (closures NOT MEASURED) | +79 prefixes, +68 Go builds, +104 streams since 2026-08-15 (the 2026-08-15 baseline behind [`image-staging-vs-baking.md`](../reference/image-staging-vs-baking.md#cost-model): 152/177/212) — **≈ 0.43 GB/day** of unrooted garbage | **None.** `nix store gc` is reachable only via `yolo prune --nix-gc --apply` (host-only, default off) and the daemon's `min-free = 0` ([§2.3](#23-yolos-own-store-outputs-are-never-collected--the-c8-finding)) | **≥ 28.8 GB** (220 × 85.6 MB + 245 × 40.9 MB), plus the unrooted stream closures |
| Host `GLOBAL_CACHE` — `paths.GlobalCache()`, **shared by every workspace**; a jail sees it as `~/.cache` | **114 G**: pants 40 G (`lmdb_store` 27 G + `named_caches` 14 G), uv 34 G, go-build 18 G, images 11 G, pex 6.1 G, nce 2.0 G, pip 1.9 G, npm 1.5 G, staticcheck 941 M, nix 298 M, copilot 102 M | NOT MEASURED (no second sample) | `PurgeCacheByAge` (30 d) over uv/pip/npm/go-build/mise/pex/pants/node-gyp/gopls; `yolo prune --apply` only. `nce`, `staticcheck` uncovered | **49.34 GiB in ~369 k files older than 30 d** (pants 39.36 / 211 056 files, pex 6.44, uv 3.06, npm 0.41, pip 0.07, go-build 0); **+1.86 GiB** in `nce` (uncovered) |
| The NESTED jail's own `paths.GlobalCache()` | **14 G**: images 9.9 GiB, npm 2.1 G, go-build 75 M, uv 88 K, nix 352 K | NOT MEASURED | same reaper, run by the in-jail `yolo` | a second, smaller instance of the row above — **not** a correction to it ([§2.5](#25-does-anything-ever-read-it-back--reuse-per-store)) |
| Image tars, host (`~/.cache/images`) | **3 tars, 10.7 GB**, newest 2026-08-24 | zero since C3 | `PruneImageCache` **keep=3** — so "none" to remove | **all 10.7 GB**: dead on podman since C3, kept only by the default |
| Image tars, nested (`cache/images`) | **3 tars, 9.9 GiB**, newest 2026-08-25 19:17 | zero since C3 | same | **all 9.9 GiB** |
| Nested state dir (`~/.local/share/yolo-jail`) | **15.0 GiB**: cache 13.4 (images 9.9, npm 2.1, copilot 1.3), mise 1.1, captures 406.5 MiB, agents 47.2 MiB | NOT MEASURED | per-subdir, all `yolo prune` only | see rows above |
| npm `_cacache` | nested **2.1 G** (newest 2026-09-06 17:46); host **1.4 G** (newest 2026-09-04). The brief's *"~672 MB, +27 MB in six days"* is a third cache I could not locate from here — NOT MEASURED | growing | `PurgeCacheByAge` (npm is in the default list); `yolo prune` only | host: 0.41 GiB > 30 d; nested: NOT MEASURED |
| Vendor version dirs (`~/.local/share/claude/versions`) | **7 builds, 1.6 GB** in this workspace (2.1.165 … 2.1.263) | one per vendor release, per workspace | A7's `_prune_versions` (`internal/entrypoint/shims.go`), **keep 2**, shipped 2026-09-04 — but it runs **only after a successful update** | **≈ 1.28 GB** here (the 5 builds A7 would drop), × every workspace |
| Capture store (`<gs>/captures`) | **3 entries, 407 MB**; nothing superseded | one entry per program per capture | `PruneSupersededCaptures` (K = 1 per program); `yolo prune` only | 0 here |
| Agent staging orphans / retired loophole state | **443 dirs, 36.5 MiB** / **6 generations, 1.9 MiB** | slow | `yolo prune` only, liveness-gated | all of it, and it is small |
| Agent worktrees (`/workspace/.claude/worktrees`) | **20 dirs, 985 MB**; `.git/worktrees` 22 entries, 4.0 M; 2 prunable (`/tmp/yolo-base-wt`, `/tmp/yolo-prev-wt` — directories already gone) | one per Claude Code worktree agent | **Not yolo's.** Claude Code's; `git worktree prune` for the two | not yolo's to reclaim |
| Apple Container image store and tars | **NOT MEASURED** — no `container` runtime in this jail | — | none (podman-only reapers) | unknown |
| Root device | **3.7 T, 3.0 T used, 82 %, 699 G free** (`df /nix/store`) | [`minimal-disk-footprint.md`](minimal-disk-footprint.md) [§2.2](./minimal-disk-footprint.md#22-rates--the-numbers-to-argue-from): ≈ +55 GiB/day in August | — | — |

Two facts about the nested podman row that change how to read it. Its `GraphRoot` is
`/var/lib/containers/storage` **inside** this container, which sits on the outer container's
overlay upper directory on the host disk (MEASURED: `/proc/mounts`), so it is real host disk for
as long as this jail lives — up since **2026-09-04 08:46** — and it is discarded when the jail is
removed. And the auto-reap that would have kept it at ~10 images never ran here because the
verification launches deliberately set `YOLO_NO_AUTO_IMAGE_REAP=1`; on the host, the same reaper
fires only on a host launch with a binary that carries it.

### 2.2 The image reap, priced against this store

`yolo prune` dry-run, tree-built binary, 2026-09-06 ~23:20: **"Old yolo-jail images (keep=2) —
would remove: 14"** of the 24 tagged images. The arithmetic is worth writing down once, because it
is what "the shipped reaper does not reclaim the whole backlog" actually means:

- `ProtectedImageTags` maps every store path in the load sentinel's LRU-10 to its content tag. All
  ten map to images **in this store** (MEASURED: sha256 of each sentinel line against `podman
  images` — none stale), and the two newest by `CreatedAt` are among them.
- So the protected set is ten images, `keep=2` protects nothing the LRU did not already protect,
  and 24 − 10 = **14**. On a dev-loop machine the retention is **effectively "the last ten images
  used"**, and `keep` is inert.
- The ten survivors carry **≈ 24.3 GB** of unique layers plus their shared bases. The four `<none>`
  rows show 91.3 kB unique each *today*, but they are re-stream twins of four of the fourteen
  (`639f5cb6836b`, `ba3cb2623615`, `b375bfe47a73`, `8bed182f126f`, each 91.3 kB unique / 3.565 GB
  shared): once the tagged twins go, **the nameless rows become the last holders of a 3.565 GB
  chain**. [OQ-DF3](./minimal-disk-footprint.md#OQ-DF3)'s REACH half is therefore worth ~3.5 GB on this machine, but only *after* the
  tagged pass — before it, the same rows look free.
- Estimate, not a measurement (an `--apply` was out of bounds): the store lands near **29 GB**
  and the first pass frees **≈ 24 GB** of 52.73. `podman system df -v` cannot express layers
  shared only among the removed set, which is the uncertainty.

> [!IMPORTANT]
> **The LRU-10 sets the floor, and C6 is what lowers it.** Ten protected images at the measured
> ~2.7 GB unique each ([the cost model](../reference/image-staging-vs-baking.md#cost-model)) is a **~25 GB steady state** on any machine
> that mints images faster than the LRU ages them, and no `keep` value changes it. A stable layer
> chain ([OQ-6](../reference/image-staging-vs-baking.md#why-its-this-way)) would make ten coexisting images cost ten trailing layers instead of ten
> re-chained tails — the same lever priced there as time is priced here as the veto's floor.

### 2.3 yolo's own store outputs are never collected — the C8 finding

The brief asked whether C8 opened a new accumulation vector. The honest answer is narrower and
worse: **the vector is old, C8 made it the whole of the per-commit cost, and nothing has ever
collected it.**

**MEASURED, 2026-09-06, host store as seen through the daemon** (`nix-store --query --roots` per
path — read-only; a `nix store gc --dry-run` was deliberately NOT run, because it takes the GC
write lock and would stall every concurrent host build for the minutes a 40 459-path store takes):

| Class | Paths | Bytes | Rooted | How the rooted ones are rooted |
| :--- | ---: | ---: | ---: | :--- |
| `*-yolo-jail-install-prefix` | 231 | 19 766 664 112 B (19.77 GB; 85.6 MB each — 53 M binaries + 53 M flake bundle) | **11** | every one **transitively**, through a pre-C8 image's durable root (`/home/matt/.local/share/yolo-jail/build/roots/<sha16>` → `*-stream-yolo-jail`); **not one through a `jail-prefix-*` link** |
| `*-yolo-jail-go-0-dev` | 245 | 10 021 614 266 B (10.02 GB; 40.9 MB each) | **0** | build-time only: `installPrefix` copies the binaries, `keep-outputs = false`, so a Go build's output is garbage the moment the prefix exists |
| `*-stream-yolo-jail` | 316 | scripts; closures NOT MEASURED | 18 | image roots |

Host daemon config, read from in-jail (`nix config show` speaks to the host daemon):
**`min-free = 0`, `max-free = 9223372036854775807`, `keep-outputs = false`, `keep-derivations = true`.**
So the daemon never collects on its own. yolo's only collector is `RunNixStoreGC`
(`internal/prune/nixgc.go`), behind `--nix-gc`, default off, host-only, reached from `yolo prune`
alone — the trigger shape [`minimal-disk-footprint.md`](minimal-disk-footprint.md) [§1](./minimal-disk-footprint.md#1-the-ruling-and-what-the-bug-actually-is)
measured failing. **Nothing on this machine runs `nix store gc`.** The store's only collector since
the 2026-07-22 incident is a human who has not typed the command.

Three consequences, stated as findings:

1. **C8 traded a tracked cost for an untracked one.** Before C8 a Go-only commit cost podman ~2.7 GB
   and ~26 s (tracked: Ledger C has a reaper and, since today, a trigger) *and* realized an install
   prefix plus a Go build in the store (~125 MB, untracked). After C8 the podman half is gone
   ([the mounted prefix](../reference/image-staging-vs-baking.md#the-mounted-prefix)) and the store half is **all that is left**:
   ≈ 0.43 GB/day at the 22-day rate, with no reaper, no trigger and no ledger. The 152 prefixes
   that existed on 2026-08-15 prove the accumulation predates C8; what C8 changed is that a
   superseded prefix becomes garbage *immediately* (its checkout's single out-link moves on)
   rather than when a pre-C8 image's root was eventually reaped.

2. **A running jail's prefix can be durably unrooted, and this jail's is.** The prefix this jail
   executes from — `/opt/yolo-jail/bin/*` resolve into `i4zjr5jc…-yolo-jail-install-prefix`, and
   `yolo-jaild supervise` (pid 52) runs from it — has **no `jail-prefix-*` root**. It is pinned today
   only by two pre-C8 image roots (`build/roots/97c9bd26ef324d85`, `build/roots/410d4c15aa88abee`)
   that happen to contain a byte-identical prefix, and those roots are exactly what
   `PruneOrphanImageRoots` reaps once their images age out of the LRU. `image.JailPrefixOutLink` is
   one link **per checkout**, overwritten on every build (`internal/image/prefix.go`; pinned by
   `TestJailPrefixOutLinkIsPerSourceTree`), so it roots the *newest* prefix, never the ones older
   jails from the same checkout are still running. Whether nix's runtime-root scan of `/proc` covers
   a container's processes is **NOT ESTABLISHED**: one prefix (`i9m84xhz…`) reported a `{censored}`
   runtime root, the one this jail's daemons execute from reported none. I do not rely on it.

3. **In-jail, the out-link is not a root at all.** This jail's `build/jail-prefix-f630c83785fec26b →
   69pivd58…` reports zero roots — the same fact `RegisterImageRoot`'s doc comment records for image
   roots ("the host daemon prunes any root pointing into the jail's /home tree as stale") and gates
   on `!inJail`. `BuildJailPrefix` has no such gate. Nested-only, and `nix store gc` refuses in-jail,
   so today it costs nothing; it matters the day [§5](#5-the-shape-measure-late-offer-early-delete-in-the-slot)
   makes any store deletion automatic.

> [!WARNING]
> **`SweepDanglingOutLinks` never fires on a store that is never collected.** It reaps a
> `run-result-*` link whose target is gone; on this machine `run-result-701949` (2026-07-27) still
> resolves because nothing has deleted its target, so the sweep keeps it as "a live root for an
> in-flight build". It is not a root (query: none). The sweep's precondition — a GC happened — is
> the event that never happens, so a reaper whose only job is post-GC cleanup has a permanent
> zero. Not a bug in the sweep; a symptom of the finding above.

### 2.4 Caveats

- **One machine, one jail.** Same limitation as every sibling measurement (the one-machine caveat
  [`image-staging-vs-baking.md`](../reference/image-staging-vs-baking.md#cost-model) states over its cost model).
  The *shape* — a reaper whose trigger is a human, a floor set by an LRU, a store nobody collects —
  does not depend on the magnitude.
- **The podman store moved during the day.** 38.68 GB / 24 images was true around midday; 52.73 GB
  / 30 rows is the ~23:20 reading after the C4/C5/C8 verification launches (two of the new rows are
  the 1.5 GB `.#ociImageLean` images — 58 % smaller than the 3.58 GB stock, matching C5's measured
  1.91 GiB). Both are cited with their time.
- **Unrooted store bytes are a lower bound.** Only three name patterns were counted; the unrooted
  stream closures include nixpkgs generations from `flake.lock` bumps (~3.3 GiB each) whose roots
  `PruneOrphanImageRoots` reaped — NOT MEASURED without the GC dry-run I declined to run.
- **The age-purge figure is by mtime**, which is the rule `PurgeCacheByAge` applies. A cache file
  read daily but never modified counts as dead; that is the accepted contract of the
  "regenerable-but-expensive" class ([`minimal-disk-footprint.md`](minimal-disk-footprint.md) [§4](./minimal-disk-footprint.md#4-a-budget-not-a-retention-count)).
- **The reap estimate is an estimate.** ≈ 24 GB freed is derived from per-image unique sizes and
  the shared-chain reasoning in [§2.2](#22-the-image-reap-priced-against-this-store); the exact figure needs the apply.

### 2.5 Does anything ever read it back? — reuse per store

Every row in [§2.1](#21-every-store-one-table) has a retention policy. A retention policy is a claim
that the bytes will be **reused**, and the size it picks is a claim about *when*. Neither claim was
ever checked. This section checks them, because the maintainer's question was the right one: some of
these stores cannot be reused at all, and still have a policy sized as though they could.

The root filesystem mounts `relatime`, so a file's atime is a usable *last-read* time: it is updated
on read whenever the stored atime is older than the mtime or older than 24 h. That makes "has
anything read this since it was written" a measurement rather than an argument. Measured 2026-09-07.

**Class A — reuse is impossible, or reachable only through a path we have already ruled against.**
A retention *count* is the wrong instrument here; the right number is zero.

| Store | The only reader | Read-evidence | Verdict |
| :--- | :--- | :--- | :--- |
| Image tars on a streaming runtime (20.6 GB) | `newestTars`, reached only when a build FAILED **and** the operator set `YOLO_ALLOW_STALE_IMAGE=1` **and** no image exists in the runtime (`internal/image/autoload.go`, the `SkipBuild`-or-ignored-failure branch) | all three nested tars were last read **33–72 ms after being written** — that is the write-back — and **not once in the 13 days since** | `keep=3` is not a cache policy, it is insurance whose payout is a two-week-old image. That is the "stale image looked like a working jail" failure C1 exists to end. Keep **0** ([OQ-BF6](#OQ-BF6)) |
| `*-yolo-jail-go-0-dev` (245 paths, 10.02 GB) | nothing. `installPrefix` **copies** the binaries and `keep-outputs = false`, so no live path references one | n/a — unrooted by construction ([§2.3](#23-yolos-own-store-outputs-are-never-collected--the-c8-finding)) | reusable only by rebuilding the identical derivation, which nix short-circuits on the *prefix* anyway. Delete by name ([OQ-BF3](#OQ-BF3)) |

**Class B — reuse is real, but the retained size is not what the reuse evidence supports.**

`*-yolo-jail-install-prefix`: 231 paths, 19.77 GB. A prefix is reused when a launch resolves the same
store path and mounts it, which reads its `yolo-entrypoint` — so atime on that binary is exactly
"when did a jail last boot from this prefix". Measured over all 231:

| Last read | Paths | ≈ Bytes |
| :--- | ---: | ---: |
| < 1 day | 7 | 0.60 GB |
| 1–6 days | 21 | 1.80 GB |
| 7–29 days | 78 | 6.68 GB |
| **≥ 30 days** | **125** | **10.70 GB** |

Oldest last-read: **45 days**. So 54 % of the bytes belong to commits no jail has booted in a month,
and real reuse is concentrated in 28 paths ≈ 2.4 GB. A retention rule of "prefixes read within 7
days" holds an eighth of what is retained today. This is the evidence [OQ-BF3](#OQ-BF3) needs.

> [!IMPORTANT]
> **The image store carries two policies with two different justifications, and only one of them is
> about reuse.** `--keep-images` (default 2) WAS a reuse-and-undo buffer — deleted 2026-09-09 by
> [`OQ-LS3`](./the-load-sentinel-is-not-a-liveness-oracle.md#111-decision-ledger), which replaced the global window with one current-image pointer per workspace. The load sentinel's LRU is a
> **liveness veto** — `AddLoadedPath`'s own comment reasons about *concurrent jails*: "several images
> stay loaded at once now, and a jail can legitimately run one whose load was many launches ago."
> Those are different claims, and [§2.2](#22-the-image-reap-priced-against-this-store) measured that the LRU, not `keep`, sets the floor. Its size is
> a bare `10` with no derivation. Bounded by its *own* stated purpose it should be the number of jails
> that can run at once — and after C8 the count of *distinct* images barely moves at all, since the
> image now changes only on `flake.*` and `packages:`. A liveness veto is doing duty as a reuse cache
> it was never justified as ([OQ-BF8](#OQ-BF8)).

**Class C — yolo's directory, another workspace's asset, and no per-workspace accounting.** This is
the largest bucket and the most interesting one, because the first reading of it was wrong twice.

`paths.GlobalCache()` is documented as *"the shared cache dir"* — **machine-wide, shared by every
workspace** — and a jail sees it mounted at `~/.cache`. Verified from `/proc/self/mountinfo`: this
jail's `/home/agent/.cache` has its mount root at `/@home/matt/.local/share/yolo-jail/cache`. So the
114 G tree IS yolo's own global cache, and `yolo prune` on the HOST walks exactly it.

What wrote the 40 G is a real pants build cache — `lmdb_store` 27 G plus `named_caches` 14 G, pants'
own layout. `pants` is not on PATH in *this* workspace and appears in this repository only in
`internal/prune/cachepurge.go`'s default subdir list and its test. So it was written by a
**different workspace's jail**: a pants-using project, pooling into the shared dir by design.

That makes the maintainer's point sharper rather than weaker. The sharing is deliberate and it buys
something real (a warm npm cache across workspaces). But the same mechanism charges one project's
unbounded third-party build cache to yolo's global store, where **the workspace that generated it is
not the workspace that can see it**, no per-workspace accounting exists, and yolo's 30-day rule is
the only thing that would ever bound it — a rule that has never run. A store can therefore grow from
assets whose generating workspace may never exist again, and nothing in the model notices.

**And there is a second axis nobody has pulled: the host may already have the same cache.** Pooling
across workspaces is done. Sharing with the HOST is not done at all — the host's own `~/.cache` is
not mounted into a jail (verified from `/proc/self/mountinfo`: no mount has its root under
`/@home/matt/.cache`). So a machine where pants runs both on the host and in a jail carries **two**
caches. yolo already has the pattern for the alternative — it shares the host's nix store rather than
duplicating it (`hostNixStore`, `internal/cli/run/hostprobes.go`) — it simply has never applied it to
a cache.

Whether that is safe splits the pants cache exactly down its middle, and the split is the answer:

| Half | Size | Shareable? | Why |
| :--- | ---: | :--- | :--- |
| `lmdb_store` | 27 G | **yes** — content-addressed (`cache/`, `directories/`, `files/`, `immutable/`) | pants' own default is ONE per-user store shared by every repo on the machine, so multi-process sharing is the designed norm, not a workaround |
| `named_caches` | 14 G | **no** | not content-addressed, and **path-poisoned**: a sampled `pex_root/…/INTERP-INFO` records interpreters as `"/home/agent/.cache/nce/…/python"` — the JAIL's home. On the host those records read `/home/matt/…`. Shared, each side would hold interpreter records naming a home that does not exist there |

> [!NOTE]
> **The blocker is path-shaped, not arch-shaped**, which is worth stating because arch is the
> intuitive worry. On a Linux host with a matching arch both halves are *binary*-compatible; it is the
> embedded absolute home path that breaks `named_caches` anyway. Arch and OS do matter in one place —
> a macOS host, where the jail is Linux in a VM: there the split is moot because nothing is
> compatible, and the honest answer is not to share at all. `storePackagesEligible` already spells
> that predicate for C4's own reasons.
>
> `nce` (2.0 G, and *uncovered* by the purge's default subdir list) is the tail of this: it is the
> content-addressed download store those `INTERP-INFO` records point INTO, so it is coupled to
> `named_caches` and cannot be reasoned about separately from it.

> [!WARNING]
> **A path expression must be evaluated in the frame that runs it.** `PurgeCacheByAge` is rooted at
> `joinPath(gs, "cache")`, which resolves to the 114 G host tree when the HOST `yolo` runs it and to
> the nested jail's own 14 G tree when an in-jail `yolo` does. Reading that expression in the jail's
> frame while pricing a host operation is what produced a retraction of this lever that was itself
> wrong, on 2026-09-07. Both numbers are correct; they describe different machines.

> [!WARNING]
> **atime is contaminated in that tree and cannot be used as reuse evidence.** A spread sample of
> 2176 files under `~/.cache/pants` were *all* last read on the same day, 2026-09-04 — one bulk
> traversal, not organic reuse. Any future attempt to price that cache by read-time has to account
> for whatever scan did that; by mtime, which is the rule `PurgeCacheByAge` applies, 39.36 GiB of it
> is older than 30 days.

**Class D — policy justified, with a named reader and a named trigger.**

| Store | Reader and trigger | Why the size holds |
| :--- | :--- | :--- |
| npm `_cacache` (2.1 G in the purge target) | npm, on every install | a genuine content cache; the 30-day rule is the right instrument, and this is what is left of L1 |
| Vendor version dirs, keep 2 | a human rolling back one release; the launcher only ever moves forward | keep 2 = exactly one step back. The defect is the trigger, not the count ([§3](#3-the-levers-ranked) L7) |
| Capture store, K = 1 per program | materialize on a cache miss | already the tightest count that leaves a hit possible |
| Agent staging orphans, retired loophole state | nothing — orphaned and retired by definition | Class A in kind, kilobytes in size; swept for tidiness, not bytes |

---

## 3. The levers, ranked

Ranked by **measured bytes on this machine**, with what each costs to pull. "Kind" says whether
the lever is steady-state, backfill, or both — the distinction [§1](#1-the-verdict-and-the-two-problems-the-question-turns-on)
turns on.

| # | Lever | Kind | Bytes here (dated 2026-09-06) | What it costs to pull | State |
| :--- | :--- | :--- | :--- | :--- | :--- |
| L1 | **Run the cache age-purge that already exists** (`PurgeCacheByAge`, 30 d) on the host `GLOBAL_CACHE`; add `nce` to the default list | both | **49.34 GiB** covered + **1.86 GiB** `nce`; steady state unbounded today | a trigger, not a reaper: the walk over ~369 k files is the expensive part (minutes; NOT MEASURED precisely); regeneration is a **re-download of unknown size** (pants 39 GiB). ⚠ The bytes are a THIRD-PARTY build cache yolo pools ([§2.5](#25-does-anything-ever-read-it-back--reuse-per-store) Class C) | reaper shipped 2026-07-22; trigger = human |
| L2 | **Let the shipped image reap run** — the first `AutoReapOldImages` pass against the backlog | backfill (steady state shipped) | **≈ 24 GB** of 52.73 here; ~3.5 GB more behind the `<none>` rows ([OQ-DF3](./minimal-disk-footprint.md#OQ-DF3) REACH) | zero code; the cost is **time on the launch path** — fourteen `rmi -f` of multi-GB images before the container starts (NOT MEASURED; see [§5.3](#53-triggers-defaults-and-the-post-launch-slot)) | shipped today; never fired here |
| L3 | **Delete yolo's own superseded store outputs** — named `nix store delete` of unrooted `*-install-prefix` / `*-go-0-dev` paths, never a blanket GC | both | **≥ 28.8 GB**; +0.43 GB/day | new mechanism; **prerequisite: per-jail durable prefix roots** ([OQ-BF4](#OQ-BF4)) or nix's liveness check is the only veto and its view of containers is unestablished; host-only | nothing built |
| L4 | **`ImageCacheKeep` → 0 where the runtime streams** (podman) | backfill | **≈ 20.6 GB** (10.7 host + 9.9 nested) | one constant, one predicate on the runtime; regeneration = a build; the fallback reader `newestTars` keeps working on whatever exists | knob is [`minimal-disk-footprint.md`](minimal-disk-footprint.md)'s ([OQ-DF1](./minimal-disk-footprint.md#112-open-questions) already ruled "keep zero" for the writer) |
| L5 | **C6 — a stable layer chain** ([OQ-6](../reference/image-staging-vs-baking.md#why-its-this-way)) | steady state | lowers the LRU floor from ~10 × 2.7 GB to ~10 × (trailing layers); saves ~17.6 s of podman write per image that still rebuilds | a `flake.nix` change against an already-loaded base ref; Apple Container unproven | re-opened, unbuilt; **re-priced down for storage by C8** (the Go-only trigger is gone; what still rebuilds is `flake.*` and `packages:`) and **up as the floor-setter** |
| L6 | **C4 opt-in (`YOLO_STORE_PACKAGES=1`)** | steady state | one lean image per machine (1.5 GB) instead of one ~3 GB-unique image per distinct `packages:` list | shipped; opt-in per launch | shipped today |
| L7 | **Run A7's version prune on every launcher invocation**, not only after an update | backfill | **≈ 1.28 GB** per workspace here; × N workspaces | a call-site change in the launcher template; evidence is the live symlink, complete by construction | steady state shipped 2026-09-04 |
| L8 | **Small liveness-gated sweeps into the automatic slot** — agent staging orphans, retired loophole state, superseded captures | both | 36.5 MiB + 1.9 MiB + 0 here | already tri-state gated; trigger only | reapers shipped |
| L9 | **Alias a host cache instead of pooling a second copy of it** — the CAS half of pants (`lmdb_store`), and the same question for npm/uv/pip/go-build | **neither backfill nor retention — a third kind** | up to **27 G** of pants alone stops existing twice; unmeasured for the others (the host's own copies are not visible from a jail) | a writable host bind is a bigger trust step than the `:ro` nix store precedent (`hostNixStore`, `internal/cli/run/hostprobes.go`); gated on host OS/arch matching, and **only for the content-addressed half** | shipped 2026-09-09 (`internal/hostcas`); [OQ-BF10](#OQ-BF10) |
| — | Worktrees under `/workspace/.claude/worktrees` | not yolo's | 985 MB + 4 M metadata | `git worktree prune` for the two prunable entries; the rest are Claude Code's | out of scope, named so it is not mistaken |
| — | Host `min-free` | not yolo's to pull | would bound the store's dead set continuously | a human edits `nix.conf`; `yolo check` already warns (P5) | the warning is the hint pattern again |

Effort-weighted, the order changes: **L2** (zero code, already shipped, ~24 GB) and **L4** (one
constant, ~20 GB) first; **L7** (a call-site change) and **L8** with them; **L1** next, because its
walk cost and its re-fetch cost are the reason it needs the offered disposition rather than the
automatic one; **L3** last, behind its prerequisite. L5 and L6 are image-staging's to sequence.

---

## 4. Why backfill and steady state do not share a disposition

[OQ-DF3](./minimal-disk-footprint.md#OQ-DF3) ruled the image reap **automatic** on the launch path,
debounced to 24 h, with `YOLO_NO_AUTO_IMAGE_REAP=1` as the opt-out. That is the precedent, and the
question is whether a one-time pass over a 38–53 GB backlog is the same kind of act. Three
differences say it is not always, and each one is a test an implementer can apply to a store this
doc has not seen:

1. **Evidence.** The steady-state reap deletes what it can prove superseded: a newer image exists,
   the older one is not in the last ten used. Its evidence is complete because the same process
   wrote the ledger. A backfill pass reasons about artifacts that predate the ledger: `<none>` rows
   with no tag, prefixes with no link, cache files whose last reader nobody recorded. Where the
   evidence is partial, [`minimal-disk-footprint.md`](minimal-disk-footprint.md) P2 says decline —
   and a human's consent is the only thing that turns "decline" into "delete".

2. **Regeneration.** A superseded image is a `podman load` away (26 s, measured). A superseded
   prefix is a `nix build .#installPrefix` (6.5 s + substitution). Those are bounded and known.
   Thirty-nine GiB of `pants` cache is a re-fetch of unknown duration from servers yolo does not
   control, and A7's version dirs are vendor downloads. The class table in
   [`minimal-disk-footprint.md`](minimal-disk-footprint.md) [§4](./minimal-disk-footprint.md#4-a-budget-not-a-retention-count)
   already separates "regenerable bulk" from "regenerable-but-expensive"; the disposition follows
   the class.

3. **Magnitude on the launch path.** Deleting two images takes seconds. Deleting fourteen
   multi-GB images, or 211 056 files, holds the launch for as long as it takes — and the reap runs
   *before* the container starts. A steady-state pass can afford to sit in front of the launch; a
   backfill pass cannot, or the first launch after an upgrade is the slow one.

So the rule is P3/P4, and the [OQ-DF3](./minimal-disk-footprint.md#OQ-DF3) disposition transfers exactly
where its three properties hold — complete evidence, bounded regeneration, seconds of work — and
has to be replaced by an **offer** where they do not. The image backlog itself passes all three
except the third, which is why [§5.3](#53-triggers-defaults-and-the-post-launch-slot) moves the
*work* rather than the *decision*.

---

## 5. The shape: measure late, offer early, delete in the slot

### 5.1 The housekeeping slot

**Housekeeping slot** *(coined here)* — the window in the host `yolo` process **after the container
is running and attached**, during which the launcher is otherwise waiting on its child. It exists
structurally: the launcher spawns the runtime and waits (`cmd.Wait()` in `internal/cli/run/runcmd.go`;
the in-process TTY proxy in `internal/ttyproxy`), it does not `exec` into it, and it owns the
per-jail broker front for the jail's lifetime. Work done there never delays the jail, needs no
daemon (it dies with the launch — A6 is respected), and runs on a host process with the host's
view of every store. Not a background job: it is the same process, the same lifetime, the same
`YOLO_*` environment the launch had.

Everything slow in this design runs there: the cache walk, the deletes, the store queries. Nothing
that needs a TTY runs there — by then the TTY is the container's.

> [!WARNING]
> **"The TTY is the container's" means BOTH STREAMS, and reading it as stdout-only shipped a
> regression.** The slot's notices went to stdout, moved to stderr when the reap landed here, and
> stderr is the same terminal — so a reclaim printed on top of a running agent's TUI. Reported by
> the maintainer 2026-09-09, one launch after it shipped.
>
> The slot's only output surface is `<workspace>/.yolo/housekeeping.log` (`housekeepingNote`),
> beside `boot.log` and `host-perf.log`. `yolo stores` and `yolo prune` are the human-facing
> surfaces; a launch is not one. This applies to the DECLINE warnings too: [OQ-LS2](./the-load-sentinel-is-not-a-liveness-oracle.md#111-decision-ledger)'s
> "loud" means "not silent", and a greppable log line is not silence — the non-zero exit still
> lands where a human actually asked, on `yolo prune`. The **offer** is the one thing that does
> use the terminal, and legitimately: it happens BEFORE the container attaches.

### 5.2 Two tiers, one mapping

| Store | Tier | Why (P3/P4 test) | First pass over the backfill | Steady state after |
| :--- | :--- | :--- | :--- | :--- |
| Tagged podman images | **automatic** (already ruled, [OQ-DF3](./minimal-disk-footprint.md#OQ-DF3)) | evidence complete (sentinel veto); regeneration = a load | the shipped pass, **moved into the slot** ([§5.3](#53-triggers-defaults-and-the-post-launch-slot)) | unchanged |
| Image tars on a streaming runtime | **automatic** | evidence complete (the runtime streams; a tar is one-shot, P3 of minimal-disk); regeneration = a build | `keep=0` on the first pass takes all of it | zero tars |
| Superseded vendor versions (A7) | **automatic** | evidence complete (the live symlink, per workspace); regeneration = a vendor download of one build, which the launcher does anyway | `_prune_versions` at every launcher invocation, keep 2 | unchanged |
| Agent staging, loophole state, captures | **automatic** | tri-state gated already; kilobytes to hundreds of MB | in the slot | in the slot |
| yolo's own unrooted store outputs | **automatic, gated on [OQ-BF4](#OQ-BF4)**; until then **offered** | evidence complete only once every running jail's prefix has a durable root; nix's own liveness check is the second veto; regeneration = a `nix build` | first pass deletes every unrooted `*-install-prefix` / `*-go-0-dev` by name (`nix store delete`, which refuses a live path) | per launch, in the slot, host-only |
| Cache age-purge (host `GLOBAL_CACHE`) | **offered while non-zero, then automatic** ([OQ-BF1](#OQ-BF1)) | evidence complete (mtime) but regeneration is an unbounded re-fetch — P4 | a prompt with the measured size; **y** deletes in the slot and enables the steady state; **n** asks again in 7 d; **never** opts the class out. The prompt returns whenever the class is ≥ 1 GiB again and no answer stands — this cache grows by normal use, so an offer shown once and retired would be the same trigger defect one level up | automatic, 30 d, in the slot, once consented |
| `<none>` podman rows | **offered** until [OQ-DF3](./minimal-disk-footprint.md#OQ-DF3) REACH is ruled | evidence partial (no tag); the store is shared with non-yolo images | the offer names the count and the chain bytes they hold; acceptance is a `podman rmi` of the listed IDs, never `image prune` | whatever REACH rules |
| Worktrees | **never** | not yolo's bytes | none | none |

The offer has one shape for every offered class, so a user learns it once — and it is the shape a
user may meet again, since consent is per class and is not consumed by being given
([OQ-BF1](#OQ-BF1)):

```console
$ yolo
Reclaimable on this machine (measured 2026-09-06 21:45, older than yolo's rules allow):
  cache files older than 30 d      49.3 GiB   (pants 39.4, pex 6.4, uv 3.1, npm 0.4)
  nameless podman images            4 rows    (holding a 3.6 GB layer chain)
Reclaim now? [y]es / [n]ot now (ask again in 7 days) / ne[v]er (yolo prune stays available)
```

### 5.3 Triggers, defaults, and the post-launch slot

Stated once, with units, because "periodically" is not a trigger:

- **Automatic tier — trigger:** every launch, in the housekeeping slot, **at most once per 24 h
  per store class** (the existing `AutoReapInterval` and `last-image-reap` stamp generalise to one
  stamp per class under `BuildDir()`). Host launches act on host stores; a nested launch acts on
  the nested podman store and the nested state dir only, and **never** on `/nix/store` (the same
  in-jail refusal `RunNixStoreGC` has today).
- **The image reap moves from before the container starts to the slot.** The property that
  justified its current placement — this launch's own image is already in the sentinel before the
  reap reads it — holds in the slot too, since `AddLoadedPath` ran at load. What changes is that
  the first pass over a backlog stops holding the launch.
- **Offered tier — trigger:** **before** the container attaches, on a TTY, when a class's last
  measurement shows **≥ 1 GiB** reclaimable and no answer is on record, or the recorded answer is
  "not now" and **≥ 7 days** old. Non-TTY: one printed line naming the size and `yolo prune --apply`;
  no prompt, no deletion (the same polarity [`config-safety.md`](../reference/config-safety.md)'s
  [OQ-D2](../reference/config-safety.md#why-its-this-way) chose for a non-interactive config change: never an
  implicit yes). **This trigger is per class and recurs** — [OQ-BF1](#OQ-BF1) ruled that an offer is
  not consumed by being given once. `y` moves the class to the automatic tier so it is never asked
  again; `never` is the only answer that stops the asking without deleting anything; everything else
  is a deferral with a date on it.
- **Measure late, offer early.** The size a prompt shows comes from the **previous** launch's slot
  (a measurement stamped with its time), or from a probe cheap enough to run now (**< 1 s**:
  `podman images`, a `ReadDir` of three tars). A cache walk is never run in front of a launch.
  Each walk has a **60 s budget**; a class that exceeds it reports what it summed so far as
  "≥ N GiB (partial)" and the offer says so.
- **Defaults, with units:** debounce **24 h**; re-ask **7 d**; offer threshold **1 GiB**; cache age
  **30 d** (unchanged); images: one current per workspace (was `keep` **2**, deleted 2026-09-09 —
  [`OQ-LS3`](./the-load-sentinel-is-not-a-liveness-oracle.md#111-decision-ledger)); versions keep **2** (unchanged);
  `ImageCacheKeep` **0 on podman, 3 elsewhere**; walk budget **60 s**; store-delete scope
  **yolo's own output names only** (`*-yolo-jail-install-prefix`, `*-yolo-jail-go-0-dev`, and any
  path a `run-result-*`/`jail-prefix-*` link of this machine ever pointed at, if a ledger of them
  exists — the implementer's choice between name-pattern and ledger, provided the set never widens
  to a path yolo did not realize).
- **State that already exists on the day this ships — which is the backfill:** the mapping in
  [§5.2](#52-two-tiers-one-mapping) is the whole answer. Nothing is migrated; every store is
  either reaped on the first eligible slot, offered on the first TTY launch, or left alone.
- **Degenerate inputs:** an empty store or a class with 0 B reclaimable is silent (no line, no
  stamp beyond the debounce). A machine with no TTY launches ever never sees an offer and never
  loses a byte in an offered class. A store the probe cannot read (podman down, `ReadDir` error)
  declines the class for this pass and does **not** stamp the debounce — the same polarity the
  shipped reap chose for an unreadable liveness ledger.

### 5.4 One writer, concurrency, failure

**One writer per store.** The reaper for a store is its only deleter in yolo, and the manual
`yolo prune --apply` calls the same function — never a second implementation. Stamps and recorded
answers under `BuildDir()` are written only by the launcher process that holds the housekeeping
lock below. The sentinel (`last-load-<runtime>`) keeps its one writer, `AddLoadedPath`.

**Concurrency — the hard part, and automation makes it load-bearing.** Two workspaces can launch
at once; the per-workspace flock (`acquireWorkspaceLock`, `internal/cli/run/flock.go`) does not
serialise them against each other. The race that matters: jail B revisits an image whose path had
aged out of the LRU (warm path, no load), writes the sentinel, and starts its container, while
jail A's slot read the sentinel a moment earlier and is about to `rmi -f` that image — which kills
B's container. The invariant this design states: **no automatic pass may interleave between
reading the liveness ledger and removing an artifact with another launch's image-selection step on
the same machine.** The implementer's choice of mechanism — my leaning is a **machine-wide
`locks/housekeeping` flock taken by both the load-and-record step and the pass, non-blocking on
the pass side (a loser skips this launch, leaving the debounce unstamped)** — provided the window
is closed rather than narrowed. Re-reading the sentinel before each `rmi` narrows it; dropping `-f`
makes podman refuse when a container already exists; neither closes the gap between the sentinel
write and the container's creation.

**Failure paths.** A measurement that fails skips its class and prints nothing. A delete that
fails midway stops that class, reports what was removed and what was not (by name — a capture or
an image is someone's gigabyte), and does **not** stamp the debounce, so the next slot retries.
A `nix store delete` that nix refuses ("still alive") is a success of the veto, logged at debug
level, never retried with `--ignore-liveness`. A prompt has no timeout: it is a human at a TTY,
and Ctrl-C aborts the launch exactly as the config-change prompt does. A stamp that cannot be
written fails in the safe direction: more frequent checks, never a debounce stuck open.

**Forbidden.** The pass never: deletes outside yolo's own stores (`GlobalStorage()` subtrees, the
`yolo-jail` repository in the runtime, yolo's own store output names); touches a workspace working
tree beyond `.yolo/`; runs `podman image prune`, `podman system prune`, `nix-collect-garbage`, or an
unbounded `nix store gc`; removes a path any `roots/*`, `jail-prefix-*`, or resolving
`run-result-*` link points at; acts on `/nix/store` from inside a jail; prompts when stdin is not a
TTY; blocks the container's start on any delete or any walk; or retries a deletion whose first
attempt was refused for liveness.

### 5.5 `yolo stores` — the inventory, including what nothing reclaims

> [!NOTE]
> **BUILT 2026-09-08 — `internal/cli/stores`.** `yolo stores [--json] [--age] [--no-record]`,
> registered in the CLI dispatch table, with [OQ-BF9](#OQ-BF9)'s bounded sample ledger at
> `<state>/stores/<store>.samples` and the [OQ-DF3](./minimal-disk-footprint.md#OQ-DF3) REACH
> warning row (untagged podman rows: count, bytes, why yolo declines, and that `podman image
> prune` is the user's to run). Two deviations from the plan's file map, both forced: the engine
> lives in the command's own package rather than `internal/prune/stores.go` (that package was
> being edited by a concurrent slice), and the 60 s budget is enforced by a walker in that
> package rather than by a deadline seam on `purgeOldFilesUnder`, for the same reason.

**Decided by the maintainer, 2026-09-07:** before any of the dispositions above are built, yolo gets
a command that lists every store and its size — *"even if we need some time to chip away at their
control."* This section is that command's design. It is the executable form of
[§2.1](#21-every-store-one-table), and its reason to exist is the half of that table `yolo prune` cannot show.

**Why it is not `prune --dry-run`.** `prune` already prices what it would reclaim, and that is a
different question. `prune --dry-run` answers *"what would I delete?"*; `stores` answers *"what
exists on this machine, how big is it, and is anything at all responsible for it?"* Every store in
[§2.1](#21-every-store-one-table) with **no reclaimer and no trigger** is invisible to `prune` by construction — the
unrooted `/nix/store` outputs, the `nce` cache, Apple Container's store — and those are precisely
the rows the maintainer wants a handle on. A listing that could only show reclaimable stores would
reproduce the blind spot it exists to close.

**What it prints, per store:** the path; the size, and whether that size was *measured, cached or
unknown*; the growth rate when a prior sample exists, else `—`; the reclaimer, or **`none`**; the
trigger, or **`none`**; and a plain verdict of whether yolo can reclaim it at all
(`yolo` / `human` / `not yolo's`). A store nothing owns must be as legible as one that is swept.

> [!IMPORTANT]
> **The header states which machine's view it measured, and this is not cosmetic.**
> `paths.GlobalCache()` resolves to the host's 114 G tree when the host `yolo` runs it and to a
> nested jail's own 14 G tree when an in-jail `yolo` does. Reading that expression in the wrong frame
> is what produced a wrong retraction of L1 on 2026-09-07 ([§2.5](#25-does-anything-ever-read-it-back--reuse-per-store) Class C). A report that does not
> say whose disk it walked invites the same error, so the frame is part of the output, not a footnote.

**Cost, because the walk is the expensive part.** Default is one directory-level size per store root —
seconds. The age breakdown (the *dead* column, which is what makes a number actionable) needs a
per-file walk over ~369 k files and costs minutes, so it is opt-in behind `--age` and prints its own
elapsed time. Any figure that came from a cached sample rather than this run is labelled with the
sample's date. **No figure is ever printed without saying how it was obtained** — that is the
discipline this whole doc is built on, and the command is where it becomes a product surface.

**Degenerate inputs.** A store that does not exist prints `absent`, size 0 — not an error, because a
fresh machine has almost none of them. A store that cannot be read prints `unknown` with the reason.
**One unreadable store never fails the command**: a partial inventory is the useful answer, and
exiting non-zero on it would make the command unusable exactly where it matters most.

**Triggers and forbidden behavior.** On demand only — never on the launch path, and never in the
housekeeping slot ([§5.1](#51-the-housekeeping-slot)); the slot's own measurement is the cheap one it already needs. The
command **must never delete, move or mutate any store**, must never take the nix GC lock (which
would stall concurrent host builds — the same reason [§2.4](#24-caveats) declined the GC dry-run), and must
never launch a container to measure one. `--json` is a first-class output for agents, on the
`config drift` precedent.

**Growth needs two samples**, which is the one thing a read-only command cannot give itself; see
[OQ-BF9](#OQ-BF9).

### 5.6 What done looks like

Observable, on a machine that upgrades onto this:

- The first TTY launch shows one offer with dated sizes; **y** frees at least the shown figure
  within the jail's lifetime; a second launch shows no offer; `yolo prune` dry-run reports "none"
  for every automatic class within 24 h.
- `podman images yolo-jail` on the host holds at most the LRU-10 plus `keep`; the nested store, on
  a nested launch without the opt-out, likewise; `build/last-<class>-reap` stamps exist and move.
- `~/.cache/images` and the nested `cache/images` are empty on a podman machine.
- `nix-store --query --roots` on the prefix of **every running jail** returns a durable root
  ([OQ-BF4](#OQ-BF4)); the count of unrooted `*-yolo-jail-install-prefix` paths is ≤ the number of
  checkouts on the machine, and `*-yolo-jail-go-0-dev` paths number 0 after the slot runs.
- A launch's wall clock is unchanged by any reclaim: measured before and after on the same
  workspace, the cold and warm figures in [the cost model](../reference/image-staging-vs-baking.md#cost-model) hold.

---

## 6. Alternatives considered

| # | Alternative | Verdict |
| :--- | :--- | :--- |
| A1 | **Everything automatic, the [OQ-DF3](./minimal-disk-footprint.md#OQ-DF3) way, backfill included.** | **Rejected for the offered classes.** [§4](#4-why-backfill-and-steady-state-do-not-share-a-disposition) item 2: a 39 GiB re-fetch is not a bounded regeneration, and item 1: `<none>` rows have no evidence. Adopted for every class that passes P3. |
| A2 | **Everything offered — `yolo prune` prints the backlog and a human types `--apply`.** | **Rejected.** It is the hint, measured at 404 GiB and 33 days ([`minimal-disk-footprint.md`](minimal-disk-footprint.md) [§1.1](./minimal-disk-footprint.md#11-what-a-human-noticing-is-worth-measured)); an offer that is a printed line is A1 of that doc under a new name. A *prompt* is different in kind: it stops the launch until answered. |
| A3 | **Automatic once, then off** — a single upgrade-time sweep. | **Rejected as the whole answer, adopted as P2's shape.** The one-time property is right (a backfill does not recur); "then off" is wrong, because the steady-state reaper must keep running or the backlog re-forms. |
| A4 | **A bounded `nix store gc --max N` on the launch path** ([`../plans/storage-lifecycle.md`](../plans/storage-lifecycle.md) [§3](../plans/storage-lifecycle.md#3-bounded-rooting-aware-store-gc-in-yolo-prune--after-1-and-2), made automatic). | **Rejected in favour of named `nix store delete`.** A GC collects *everything* unrooted up to N bytes, including the user's own dead paths — the "reaches beyond its own artifacts" shape P6 refuses. Deleting yolo's own output names is narrower, and nix refuses a live path either way. |
| A5 | **Run the reclaim in the container's entrypoint** (in-jail). | **Rejected.** The jail sees the nested stores only, cannot root anything on the host ([§2.3](#23-yolos-own-store-outputs-are-never-collected--the-c8-finding) item 3), and `nix store gc` rightly refuses there. Host stores are the host process's. |
| A6 | **A daemon / timer / cron.** | **Rejected**, as in minimal-disk-footprint A6. The housekeeping slot gets the same benefit — slow work off the launch path — without a lifecycle yolo does not have. |
| A7 | **Make the offer a config key instead of a prompt** (`prune.auto_backfill: true`). | **Rejected as the default, kept as the "never" answer's durable form.** A key nobody has heard of is a hint; a prompt is a decision. The recorded "never" can live wherever `yolo config` puts it — implementer's choice. |
| A8 | **Widen the LRU-10 veto to a time window** so old-but-recent-by-count images stop pinning 25 GB. | **Not proposed here.** The LRU is a thrash guard by design (a revisited workspace does not reload); the floor it sets is L5's problem, not a veto problem. Named so nobody re-derives it. |

---

## 7. Costs, honestly

- **What this deletes:** ≈ 24 GB of podman backlog, ≈ 20.6 GB of dead tars, ≥ 28.8 GB of unrooted
  store outputs, ≈ 49 GiB of aged cache (on consent), ~1.3 GB per workspace of vendor builds — on
  this machine, today. The steady state after is the LRU floor plus live caches.
- **What it complicates:** a second stamp family and a recorded-answer file under `BuildDir()`; a
  machine-wide lock the load path has to take; a prompt on the launch path, which
  [`config-safety.md`](../reference/config-safety.md) already put there once, so the pattern exists but the
  surface grows.
- **What it moves:** the shipped image reap from pre-start to the slot — an ordering change to a
  three-line call site, but one whose only test today is a real launch
  ([`minimal-disk-footprint.md`](minimal-disk-footprint.md) [§9](./minimal-disk-footprint.md#9-risks) R7).
- **What it forecloses:** nothing in image-staging's candidate ranking (its shipped rows are now
  [`image-staging-vs-baking.md`](../reference/image-staging-vs-baking.md)'s body; C6 is
  [`layer-aware-image-delivery.md`](layer-aware-image-delivery.md)'s). C6 and C4 stay exactly as ranked there;
  this doc only re-prices C6 as the floor-setter.
- **What it does not buy:** the ~65 GB of *live* cache on this host, the device's non-yolo growth
  ([`minimal-disk-footprint.md`](minimal-disk-footprint.md) [§2.4](./minimal-disk-footprint.md#24-re-measured-2026-09-02--the-backlog-is-gone-here-and-the-device-kept-filling-anyway)),
  and the host's `min-free`. Those are a budget ([OQ-DF4](./minimal-disk-footprint.md#OQ-DF4)) or a human.

---

## 8. Risks

| # | Risk | Mitigation |
| :--- | :--- | :--- |
| R1 | **Deleting the prefix a running jail executes from.** Observed possible today: this jail's prefix has no durable root of its own ([§2.3](#23-yolos-own-store-outputs-are-never-collected--the-c8-finding) item 2). | [OQ-BF4](#OQ-BF4) before L3 is automatic; `nix store delete` refuses a live path as the second veto; never `--ignore-liveness`. |
| R2 | **A first-pass reap holds the launch** — fourteen `rmi -f` before the container starts. | Move the reap to the housekeeping slot ([OQ-BF5](#OQ-BF5)). |
| R3 | **The reap-versus-warm-launch race**, now on a schedule set by other people's jails (minimal-disk P7). | The invariant in [§5.4](#54-one-writer-concurrency-failure) and a machine-wide lock; the pass loses ties. |
| R4 | **An offer on a non-TTY is silently a "no" forever.** | Correct by design (never an implicit yes), and the printed line names `yolo prune --apply`. A CI host has no backlog worth an offer. |
| R5 | **A cache purge re-fetches something expensive** (pants, uv). | That is why it is offered, once, with the size, and why the 30 d rule is unchanged. |
| R6 | **The walk itself costs minutes on a 369 k-file cache.** | Runs only in the slot, debounced 24 h, under a 60 s budget with a partial figure. |
| R7 | **The in-jail out-link is not a root and never was**, so a nested jail's prefix is unrooted from birth. | Gate `BuildJailPrefix`'s rooting the way `RegisterRoot` is gated, and have the *host* root what nested jails run — or accept it as nested-only, which is today's state. Part of [OQ-BF4](#OQ-BF4). |
| R8 | **One-machine measurement.** | Stated in [§2.4](#24-caveats); the design rests on shapes, not magnitudes. |

---

## 9. What this does NOT cover

- **Which component deletes Ledger B's remaining tars on Apple Container**, and the write-path
  versus launch-path question generally — [OQ-DF2](./minimal-disk-footprint.md#OQ-DF2). This doc says
  *whether* the first pass is automatic; that one says *who*.
- **The reach into `<none>` rows** — [OQ-DF3](./minimal-disk-footprint.md#OQ-DF3). Priced here (~3.5 GB
  after the tagged pass), ruled there.
- **A byte budget** — [OQ-DF4](./minimal-disk-footprint.md#OQ-DF4). The ~65 GB of live cache on this host is
  its problem, not a backfill.
- **The host's `min-free`** — P5; `yolo check` warns and must not edit.
- **Agent logs and transcripts, browser profiles** — durable user data, excluded as in
  [`minimal-disk-footprint.md`](minimal-disk-footprint.md) [§8](./minimal-disk-footprint.md#8-what-this-does-not-cover).
- **`macos-user`** — no image, no tars, no podman; its `buildEnv` closure is rooted by its own
  profile ([`macos-user-nix-and-features.md`](../reference/macos-user-nix-and-features.md)).
- **Apple Container's image store** — NOT MEASURED, no reaper, not designed against here.
- **The worktrees** — named in [§2.1](#21-every-store-one-table) so they are not mistaken for yolo's; never touched.
- **C6's design** — image-staging's [OQ-6](../reference/image-staging-vs-baking.md#why-its-this-way); this doc only adds the floor argument.

---

## 10. Sequencing — what I would build, in order

1. **`yolo stores` — the inventory first** ([§5.5](#55-yolo-stores--the-inventory-including-what-nothing-reclaims)). It is the maintainer's stated
   minimum, it is the only item here that needs no ruling on any of [§11.2](#112-the-questions-as-argued)'s questions, and it
   makes every later step checkable: each disposition below can then be shown to have moved a
   number rather than argued to have. It also surfaces the rows nothing owns, which is where the
   next policy will have to come from.
2. **L4 and L7 — two small changes with no new mechanism.** `ImageCacheKeep` 0 where the runtime
   streams; `_prune_versions` at every launcher invocation. Both are P3-clean, both take a backlog
   with them on their first run, both are testable at the callee *and* the call site.
3. **L2's ordering — move the shipped image reap into the housekeeping slot**, with the machine-wide
   lock ([§5.4](#54-one-writer-concurrency-failure)). This is the first thing that makes the slot exist,
   and the first pass over a backlog stops holding a launch. Fold L8's small sweeps into the same
   slot.
4. **The offer** — the recorded-answer file, the measurement stamp, the pre-attach prompt — with L1
   as its first client. This is where P4 becomes code.
5. **[OQ-BF4](#OQ-BF4) — durable roots for every running jail's prefix**, and the in-jail gate. A
   correctness fix independent of disk, and L3's prerequisite.
6. **L3 — named `nix store delete` of yolo's own superseded outputs**, automatic in the slot,
   host-only, once 4 has landed. Until then the same set is *offered*, computed by name.
7. **Re-measure**, in this jail and on the host: the tables in [§2](#2-measured-2026-09-06) are the
   baseline, and the done-conditions in [§5.6](#56-what-done-looks-like) are what to check.

---

## 11. Decisions

The maintainer asked two questions; the levers ([§3](#3-the-levers-ranked)) are an answer, the
disposition ([§5](#5-the-shape-measure-late-offer-early-delete-in-the-slot)) was a proposal and is
now ruled. [§11.1](#111-decision-ledger) is the durable form; [§11.2](#112-the-questions-as-argued)
keeps the arguments, because several rulings turn on a distinction that is worth more than its
verdict.

### 11.1 Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| [OQ-BF1](#OQ-BF1) | **Two tiers — and the offered tier is offered whenever a class has ≥ 1 GiB reclaimable, not once.** `y` promotes the class to automatic; `never` is the only sticky stop. Three of the five backfill classes recur by normal use, so a retired offer would be the trigger defect one level up; for the two that do not, the offer's return **is** the bug report | 2026-09-08 | [§5.2](#52-two-tiers-one-mapping), [§5.3](#53-triggers-defaults-and-the-post-launch-slot) || ✅ `3a08a829` |
| [OQ-BF2](#OQ-BF2) | **Yes — offered while non-zero, then automatic in the slot**, 30 d unchanged, `nce` added, `staticcheck` left out. MEASURED: `go-build` and `staticcheck` self-trim (Go's 5-day/1-day trimmer, `trim.txt` present); pants, uv, pip, npm, pex and `nce` do not — so the ones the purge cannot help are exactly the ones that manage themselves, and the 49.34 GiB is almost all pants | 2026-09-08 | [§5.2](#52-two-tiers-one-mapping), [§2.1](#21-every-store-one-table) || ✅ `3a08a829` |
| [OQ-BF3](#OQ-BF3) | **Yes, once [OQ-BF4](#OQ-BF4) roots every running jail's prefix:** delete unrooted `*-yolo-jail-install-prefix` / `*-yolo-jail-go-0-dev` by name in the slot, host-only, never a blanket gc, never `--ignore-liveness`. Offer the same set until BF4 lands. Go-build outputs are garbage the moment the prefix exists | 2026-09-08 | [§2.3](#23-yolos-own-store-outputs-are-never-collected--the-c8-finding), [§3](#3-the-levers-ranked) L3 | ✅ `2a49467d` |
| [OQ-BF4](#OQ-BF4) | **Yes — a durable root per prefix store path, registered at launch, gated on `!inJail`, held while a container is running from it.** A bug fix, not a disposition: today a long-running jail's pid1 binary is rooted by nothing. **Neither** the load sentinel's LRU (it cannot answer liveness) **nor** an age cutoff (the policy [OQ-LS1](./the-load-sentinel-is-not-a-liveness-oracle.md#111-decision-ledger) ruled for image closures, which would reap a long-running jail's own binaries). One test separates them: can losing it cost only a rebuild? | 2026-09-08 | [§2.3](#23-yolos-own-store-outputs-are-never-collected--the-c8-finding), [§8](#8-risks) R7 | ✅ `8768db9d` |
| [OQ-BF5](#OQ-BF5) | **Move the image reap into the housekeeping slot, and take the machine-wide housekeeping lock in the same change** — in both the load-and-record step and the pass. The lock is not a follow-up: the slot widens the window the race already has | 2026-09-08 | [§5.1](#51-the-housekeeping-slot), [§5.4](#54-one-writer-concurrency-failure) | ✅ `65ae67c6` |
| [OQ-BF6](#OQ-BF6) | **`ImageCacheKeep` 0 on podman, unchanged at 3 on Apple Container** until [OQ-DF2](./minimal-disk-footprint.md#OQ-DF2) names the component that deletes on success. Zero retained tars is not zero tars readable — the fallback READER survives | 2026-09-08 | [§3](#3-the-levers-ranked) L4, [§5.3](#53-triggers-defaults-and-the-post-launch-slot) | ✅ `a9a7dbb0` |
| [OQ-BF7](#OQ-BF7) | **Retired from the question list — an outage with one measured cause needs a fix, not a disposition.** Shipped as a **refusal** (`prefixUnreachableFromVM`), not as staging or baking. Its claimed coupling to BF3/BF4 is discharged: under the refusal there is no third place for the prefix to live | 2026-09-08 | [§11.2](#112-the-questions-as-argued) [OQ-BF7](#OQ-BF7) | ✅ `6a855b6d` |
| [OQ-BF8](#OQ-BF8) | **Dissolved — the premise is being removed.** Liveness moves to `podman ps` ([`the-load-sentinel-is-not-a-liveness-oracle.md`](./the-load-sentinel-is-not-a-liveness-oracle.md)), so the LRU stops being retention and the cap needs no derivation. It is **not** deleted: it keeps its MRU role for GC roots and the load diagnosis, and whether that half survives is [OQ-LS1](./the-load-sentinel-is-not-a-liveness-oracle.md#111-decision-ledger) | 2026-09-08 | [§2.2](#22-the-image-reap-priced-against-this-store) | n/a — dissolved |
| [OQ-BF9](#OQ-BF9) | **Yes — one dated line per store per run, default on, `--no-record` to opt out, `yolo stores` the single writer, bounded to 30 samples per store.** Unblocks [OQ-DF4](./minimal-disk-footprint.md#OQ-DF4), which has been waiting for a first sample; rule this first | 2026-09-08 | [§5.5](#55-yolo-stores--the-inventory-including-what-nothing-reclaims) | ✅ `e85e0690` |
| [OQ-BF10](#OQ-BF10) | **Content-addressed stores only — and the reason is injection, not size.** A path-keyed store like `named_caches` lets a jail write content the host tool reads because of where it sits; a CAS rejects a blob that does not match its digest. Gated on matching OS and arch, writable, never on macOS. Scope stops here pending a post-implementation storage analysis | 2026-09-08 | [§11.2](#112-the-questions-as-argued) [OQ-BF10](#OQ-BF10) | ✅ `2bceedef` |

### 11.2 The questions as argued

Kept rather than compacted away: [OQ-BF2](#OQ-BF2)'s self-trimming measurement,
[OQ-BF7](#OQ-BF7)'s discharged coupling, [OQ-BF8](#OQ-BF8)'s "not deleted, demoted" correction and
[OQ-BF10](#OQ-BF10)'s CAS boundary are each worth more than the verdict they support, and a ledger
row cannot carry them.

1. ✅ **[OQ-BF1](#OQ-BF1) — RULED 2026-09-08: is the two-tier disposition right — automatic where P3
   holds, offered where P4 does — or does "offer to clean up" mean every backfill is offered?** This is the closure
   question for the whole doc: it decides whether the first pass over the podman backlog and the
   dead tars runs on its own (as [OQ-DF3](./minimal-disk-footprint.md#OQ-DF3) already lets the steady state do) or
   waits for a prompt like the cache purge does. It also decides the offer's shape — a prompt with
   `y / n (7 d) / never`, TTY only, non-TTY prints and skips.

   <!-- vantage: oq id=OQ-BF1 leaning="Two tiers. Automatic for the classes whose reaper's veto already protects live state and whose regeneration is a build or a load (images, tars on a streaming runtime, vendor versions, the small sweeps) - the same footing OQ-DF3 ruled. Offered once, with the measured size, for the cache purge (an unbounded re-fetch) and for anything whose evidence is partial or whose store is shared. Offering everything would put the podman backlog behind a prompt the steady-state reaper already has permission to skip, which is inconsistent; automating everything would re-fetch 39 GiB of pants without asking." -->

   _Leaning:_ **Two tiers.** Automatic where the veto is the one that already protects live state
   and regeneration is a build or a load; offered once, with the size, where the re-fetch is
   unbounded or the evidence is partial. Offering everything is inconsistent with what
   [OQ-DF3](./minimal-disk-footprint.md#OQ-DF3) already allows daily; automating everything re-fetches 39 GiB of `pants` without asking.

   **Answer (2026-09-08):**
   > **Two tiers — and the offered tier is offered whenever the class has reclaimable bytes, not
   > once.** The maintainer's reasoning: *"if someone skips it the first time, we should probably
   > offer it again."* An offer shown once and never again is a one-shot migration prompt, and the
   > classes on the offered side are not one-shot.
   >
   > Concretely, replacing "offered once" everywhere: a class is offered when it has **≥ 1 GiB**
   > reclaimable, no answer is on record **or** the recorded answer is `not now` and ≥ 7 days old.
   > `never` stays sticky per class and is the only answer that stops the asking; `yolo prune`
   > stays available regardless. Nothing about the debounce or the threshold changes — what changes
   > is that consent is not consumed by being given once.
   >
   > **On "these are states we can never get in without a bug": true for two classes and false for
   > three, and the split is worth stating because it decides what a recurring offer MEANS.**
   >
   > | Class | Recurs by normal use? | So a non-zero backfill after the fixes means |
   > | :--- | :--- | :--- |
   > | Image tars on podman | **No** — C3 stopped writing them ([OQ-DF1](./minimal-disk-footprint.md#112-open-questions)) | a bug, an un-upgraded launcher, or another backend |
   > | Tagged podman image backlog | **No**, once the reap actually runs | the reap did not run — a bug, or `YOLO_NO_AUTO_IMAGE_REAP` |
   > | Host cache age-purge | **Yes** — third-party build caches grow by building | nothing; this is the steady state, and 30 d is a retention rule, not a repair |
   > | yolo's own unrooted store outputs | **Yes** — ≈ 0.43 GB/day, until [OQ-BF4](#OQ-BF4) plus the slot | nothing, until those land; after them, a bug |
   > | `<none>` podman rows | **Yes** — a tagged twin's removal creates one | nothing |
   >
   > Two consequences. The recurring offer is **correct** for the three recurring classes and is the
   > steady state for them, not a leftover. And for the two one-shot classes the offer reappearing
   > **is the bug report** — a prompt that comes back for dead tars on a podman host says the writer
   > or the reaper is broken, which is strictly better than the silence that let 404 GiB accrue.

2. ✅ **[OQ-BF2](#OQ-BF2) — RULED 2026-09-08: does the cache age-purge get a launch-path trigger at all?** It is the largest
   measured backfill (49.34 GiB) and the same trigger defect as the tars — a 30-day rule that
   has never run — but its regeneration cost is the one this doc cannot bound, and the bytes are a
   third-party build cache yolo pools ([§2.5](#25-does-anything-ever-read-it-back--reuse-per-store) Class C). The answer decides
   whether L1 is "offered once, then automatic in the slot" (the [§5.2](#52-two-tiers-one-mapping)
   row) or stays manual, and whether `nce` and `staticcheck` join the default list.

   <!-- vantage: oq id=OQ-BF2 leaning="Yes: offered once for the backlog, then automatic in the housekeeping slot, debounced 24 h, 30 d unchanged; add nce to the default list (1.86 GiB dead here) and leave staticcheck out until it shows age. A 30-day rule that never runs is the tar defect again; the offer is what makes the first 49 GiB a consented re-fetch rather than a surprise." -->

   _Leaning:_ **Yes — offered once for the backlog, then automatic in the slot**, 30 d unchanged;
   add `nce` to the default list (1.86 GiB dead here) and leave `staticcheck` out until it shows
   age. The offer is what makes the first pass a consented re-fetch rather than a surprise.

   **Answer (2026-09-08):**
   > **Yes — offered while non-zero (per [OQ-BF1](#OQ-BF1)), then automatic in the slot**, 30 d
   > unchanged, `nce` added, `staticcheck` left out. The one change from the leaning is the trigger's
   > shape, which BF1 settled for every offered class at once.
   >
   > **The maintainer's question — do none of these caches manage their own size? — turned out to
   > have a measured answer, and it is "two of them do".** MEASURED in this jail 2026-09-08:
   >
   > | Cache | Self-manages? | Evidence |
   > | :--- | :--- | :--- |
   > | `go-build` | **Yes** | Go's own trimmer: entries unused for **5 days** are evicted, swept **at most once a day** (`trimLimit`/`trimInterval`, `$GOROOT/src/cmd/go/internal/cache/cache.go:341-350`, go1.26.7), with `~/.cache/go-build/trim.txt` recording the last sweep |
   > | `staticcheck` | **Yes** | same cache library, same marker — `~/.cache/staticcheck/trim.txt` is present |
   > | `pants`, `uv`, `pip`, `npm`, `pex`, `nce` | **No** | no trim marker, no documented eviction; growth is bounded only by what is built |
   >
   > **[§2.1](#21-every-store-one-table)'s own table is the corroboration, and it was hiding in plain
   > sight**: of the classes measured for files older than 30 days, `go-build` reads **0** — the only
   > zero in the row — against pants' 39.36 GiB in 211 056 files. An 18 G cache with nothing older
   > than a month is what a self-trimming cache looks like from outside.
   >
   > **This changes two list memberships, and gives the leaning's hunch a reason.**
   > `staticcheck` stays off the purge list not "until it shows age" but **because it cannot show
   > age** — it trims itself, so a 30-day rule there can only ever be a no-op that costs a walk.
   > `go-build` is already on the list and is the same no-op; it stays for now (removing it is a
   > behaviour change with no bytes behind it) but it must not be cited as evidence the purge works.
   > **`nce` is the opposite case and is the one to add**: content-addressed, no eviction, 1.86 GiB
   > already dead, and covered by nothing today.
   >
   > So the honest general answer to the question: **no, they do not all self-manage, and the ones
   > that do are exactly the ones this purge cannot help.** The 49.34 GiB the offer is about is
   > almost entirely pants — a cache whose own tool ships no reclaimer that has run here.

3. ✅ **[OQ-BF3](#OQ-BF3) — RULED 2026-09-08 (the maintainer's words, verbatim): may yolo delete its
   own superseded `/nix/store` outputs by name, automatically, on the host launch path?** This is the C8 finding's remedy ([§2.3](#23-yolos-own-store-outputs-are-never-collected--the-c8-finding)):
   ≥ 28.8 GB today, +0.43 GB/day, no collector. `nix store delete <path>` refuses a live path, so
   nix's own liveness is a second veto — but it is narrower than the bounded GC of
   [`../plans/storage-lifecycle.md`](../plans/storage-lifecycle.md) [§3](../plans/storage-lifecycle.md#3-bounded-rooting-aware-store-gc-in-yolo-prune--after-1-and-2)
   and touches only what yolo realized. The answer decides whether P5 ("never carelessly GC the
   host store") admits a *named, self-scoped* deletion as something other than a GC.

   <!-- vantage: oq id=OQ-BF3 leaning="Yes, once OQ-BF4 has given every running jail's prefix a durable root: delete unrooted *-yolo-jail-install-prefix and *-yolo-jail-go-0-dev paths by name in the housekeeping slot, host-only, never a blanket gc, never --ignore-liveness. Until then, offer the same set by name through the prompt. Go-build outputs are garbage the moment the prefix exists (keep-outputs=false) and could go on the write path immediately." -->

   _Leaning:_ **Yes, gated on [OQ-BF4](#OQ-BF4).** Delete unrooted prefixes and Go builds by name in
   the slot, host-only, never a blanket GC, never `--ignore-liveness`; until BF4 lands, offer the
   same set. The Go-build outputs are garbage the moment the prefix exists and could go on the
   write path immediately.

   **Answer (2026-09-08):**
   > **"Yes, once [OQ-BF4](#OQ-BF4) has given every running jail's prefix a durable root: delete
   > unrooted `*-yolo-jail-install-prefix` and `*-yolo-jail-go-0-dev` paths by name in the
   > housekeeping slot, host-only, never a blanket gc, never `--ignore-liveness`. Until then, offer
   > the same set by name through the prompt. Go-build outputs are garbage the moment the prefix
   > exists (`keep-outputs=false`) and could go on the write path immediately."**
   >
   > The leaning, ruled as written. Two things this fixes in P5's phrasing: a **named, self-scoped**
   > deletion of paths yolo itself realized is not the "careless GC of the host store" P5 forbids,
   > and nix's own refusal to delete a live path stays as the second veto rather than being argued
   > around. The ordering is load-bearing — until BF4 lands, an automatic pass here can delete the
   > prefix a running jail's pid1 came from ([§2.3](#23-yolos-own-store-outputs-are-never-collected--the-c8-finding)),
   > which is why "offer until then" is part of the ruling and not a hedge.

4. ✅ **[OQ-BF4](#OQ-BF4) — RULED 2026-09-08, and it needed no maintainer judgment: does a running
   jail's binaries get protected from deletion?**

   > [!NOTE]
   > **This entry was unreadable, and the maintainer said so** — *"I don't understand this. is this
   > something that really needs me?"* No, and it should not have been in a list of questions that
   > do. It is restated below in plain words and ruled on its own merits; the mechanism paragraph is
   > kept because the implementer needs it.

   **In plain words.** Since C8, the yolo binaries a jail runs — including pid1 — are not in the
   image; they are a directory in `/nix/store` that the launch bind-mounts. Nix will not delete a
   store path that something *roots*, and yolo keeps exactly **one** root per checkout, pointed at
   the **newest** prefix it built. So a jail that has been running for a while is running from a
   path nothing roots any more. Nothing collects the store today, which is the only reason this has
   not bitten: the day anything does — this doc's own [L3](#3-the-levers-ranked), a `nix-collect-garbage`, the daemon's
   `min-free` — that jail loses the binary under its own pid1. **Measured:** this jail is in exactly
   that state right now ([§2.3](#23-yolos-own-store-outputs-are-never-collected--the-c8-finding) items 2-3): its prefix is pinned only by two pre-C8 image
   roots that a reap will remove, and the in-jail out-link is not a root at all.

   **The mechanism, for the implementer.** One durable root per store path under `BuildDir()`,
   registered at launch the way `RegisterImageRoot` registers an image, protected by the same age
   floor and reaped when no recent launch used it, gated on `!inJail` exactly as `RegisterRoot` is;
   the per-checkout out-link stays as the build's own output link. It is [OQ-BF3](#OQ-BF3)'s stated
   prerequisite, and it is a fix regardless of disk.

   <!-- vantage: oq id=OQ-BF4 leaning="Yes: register the prefix the way RegisterImageRoot registers an image - a durable root per store path under BuildDir(), protected by the same sentinel LRU and age floor PruneOrphanImageRoots already applies, reaped when no recent launch used it; gate the registration on !inJail exactly as RegisterRoot is. Keep the per-checkout out-link for the build itself. This is a prerequisite for OQ-BF3 and a fix regardless of disk." -->

   _Leaning:_ **Yes.** Register the prefix as images are registered — a durable root per store
   path under `BuildDir()`, protected by the same age floor `PruneOrphanImageRoots` applies, reaped
   when no recent launch used it — and gate it on `!inJail` exactly as `RegisterRoot` is. Keep the
   per-checkout out-link for the build. A fix regardless of disk.

   **Answer (2026-09-08):**
   > **Yes — and this is a bug fix, not a disposition, so it is ruled here rather than escalated.**
   > There is no version of "a running jail may lose pid1's binary" that is a preference, and no
   > option on the other side to weigh: the alternative is to never collect yolo's own store outputs
   > at all, which is the state [§2.3](#23-yolos-own-store-outputs-are-never-collected--the-c8-finding) measures at ≥ 28.8 GB and ≈ 0.43 GB/day.
   >
   > **Two corrections to the leaning as written**, both forced by the sibling doc, and they pull in
   > opposite directions — which is the point.
   >
   > **(1) Not the load sentinel's LRU.** That list is a most-recently-used cache and cannot answer
   > "is this in use" —
   > [`the-load-sentinel-is-not-a-liveness-oracle.md`](./the-load-sentinel-is-not-a-liveness-oracle.md)'s
   > P1 — and protecting a prefix with it is the same class of mistake in a new place.
   >
   > **(2) Not an age floor either, which is what this answer first said.**
   > [OQ-LS1](./the-load-sentinel-is-not-a-liveness-oracle.md#111-decision-ledger) ruled image-closure roots to
   > be **purely age-based**, on the grounds that losing one costs a rebuild and nothing more. **A
   > prefix fails that test.** A running jail is *executing* from its prefix — pid1 included — so
   > losing it is not a rebuild, it is a jail with no binaries; and an age cutoff is precisely
   > wrong for it, because the longer a jail runs the more certainly its prefix would be reaped.
   >
   > So the two roots take **opposite policies, from one test** — *can losing it cost only a
   > rebuild?* Image closure: yes, so age. Prefix of a running jail: no, so a **liveness
   > guarantee** — the root exists while a container is running from it, and is reaped once none
   > is. That is what "registered at launch, gated on `!inJail`" has to mean, and why prefix roots
   > must not live in `build/roots`, whose reaper deletes any root that is not a loaded image.

5. ✅ **[OQ-BF5](#OQ-BF5) — RULED 2026-09-08 (the maintainer's words, verbatim): does the shipped
   image reap move from before the container starts to the housekeeping slot?** It is the difference between a first pass that holds the launch for
   fourteen `rmi -f` (NOT MEASURED; the deletes were out of bounds) and one the user never
   notices. The property that placed it — this launch's image is already in the sentinel — holds
   in the slot. The cost is the machine-wide lock [§5.4](#54-one-writer-concurrency-failure) asks
   for, which the pre-start placement needs just as much and does not have.

   <!-- vantage: oq id=OQ-BF5 leaning="Yes, move it, and take the machine-wide housekeeping lock in both the load-and-record step and the pass so the reap can never interleave with another launch's warm-path sentinel write. Land the lock in the same change: the race exists today and automation is what made it load-bearing." -->

   _Leaning:_ **Yes, move it, and land the lock in the same change.** The race exists today; the
   pre-start placement neither closes it nor spares the launch.

   **Answer (2026-09-08):**
   > **"Yes, move it, and take the machine-wide housekeeping lock in both the load-and-record step
   > and the pass so the reap can never interleave with another launch's warm-path sentinel write.
   > Land the lock in the same change: the race exists today and automation is what made it
   > load-bearing."**
   >
   > The leaning, ruled as written, including the part that is easy to drop: **the lock is not a
   > follow-up.** Moving the pass without it converts a race that currently needs two launches in
   > the same second into one with a much wider window, because the slot is where the pass now
   > spends its time. This also discharges the placement half of
   > [OQ-DF3](./minimal-disk-footprint.md#OQ-DF3)'s TRIGGER — the trigger stays the launch, the
   > moment moves — and it is why that ruling's ledger row records the placement as revised rather
   > than reversed.

6. ✅ **[OQ-BF6](#OQ-BF6) — RULED 2026-09-08 (the maintainer's words, verbatim): does `ImageCacheKeep` default to 0 where the runtime streams?** ~20.6 GB on this
   machine (host and nested) is kept by a `3` that predates C3; [OQ-DF1](./minimal-disk-footprint.md#112-open-questions)
   ruled "keep zero" for the *writer* and left the *reaper's* default alone. The knob is
   [`minimal-disk-footprint.md`](minimal-disk-footprint.md)'s and the component that runs it is
   [OQ-DF2](./minimal-disk-footprint.md#OQ-DF2)'s; this question only asks whether the number follows the ruling.

   <!-- vantage: oq id=OQ-BF6 leaning="Yes: 0 on podman (nothing writes a tar there and the fallback reader newestTars keeps working on whatever exists), unchanged at 3 on Apple Container until OQ-DF2 names the component that deletes on success. Automatic under P3: the tar is one-shot (minimal-disk P3), regeneration is a build, the evidence is the runtime itself." -->

   _Leaning:_ **Yes — 0 on podman, unchanged on Apple Container** until [OQ-DF2](./minimal-disk-footprint.md#OQ-DF2)
   rules its component. Automatic under P3: the tar is one-shot, regeneration is a build, the
   evidence is the runtime itself.

   **Answer (2026-09-08):**
   > **"Yes: 0 on podman (nothing writes a tar there and the fallback reader `newestTars` keeps
   > working on whatever exists), unchanged at 3 on Apple Container until
   > [OQ-DF2](./minimal-disk-footprint.md#OQ-DF2) names the component that deletes on success.
   > Automatic under P3: the tar is one-shot (minimal-disk P3), regeneration is a build, the
   > evidence is the runtime itself."**
   >
   > The leaning, ruled as written, and the parenthesis is the part that makes it safe rather than
   > merely smaller: `keep=0` changes the **reaper's** default, and the offline fallback is a
   > **reader** that loads whatever tar it finds. Zero retained tars is not zero tars readable — it
   > is "yolo stops keeping one for a case Ledger C already covers". A change that also removed
   > `newestTars` would be a different and worse decision, and
   > [OQ-DF1](./minimal-disk-footprint.md#112-open-questions)'s ledger row says so too.

---

7. ✅ **[OQ-BF7](#OQ-BF7) — RETIRED FROM THIS LIST 2026-09-08: it was never a question for the
   maintainer, and the outage half is fixed. On macOS, where does the mounted prefix come from — or
   does that backend keep baking it?**

   > [!NOTE]
   > **The maintainer's ruling was about the entry, not the answer** — *"isn't this an implementation
   > detail? not design?"* Yes. A live outage with one measured cause does not need a disposition; it
   > needs a fix. It stays recorded here because this doc is where the coupling to
   > [OQ-BF3](#OQ-BF3)/[OQ-BF4](#OQ-BF4) was claimed, and that claim turns out to be discharged
   > rather than merely moved — see the Answer. C8 bind-mounts the prefix out of `/nix/store`, and a macOS podman runs in a VM that
   shares no host store. The nightly is a **total outage**: 55 failures and 59
   `statfs /nix/store/…-install-prefix/opt/yolo-jail/share/yolo-jail: no such file or directory`
   (run `34117863296`); the pre-C8 nightly had zero. The constraint was already written down in the
   sibling feature — `storePackagesEligible` refuses macOS with *"a macOS podman runs in a VM that
   shares no /nix/store with the host"* — and `jailprefix.go` has no equivalent guard, because C8
   reasoned about the prefix being a *darwin derivation producing linux binaries* and never about the
   *mount* crossing into a VM. This is the one question here that is a live outage rather than a
   disposition, and it belongs to this doc because staging the prefix anywhere else changes what
   roots it ([OQ-BF4](#OQ-BF4), [R7](#8-risks)).

   > [!WARNING]
   > **`--volume /nix:/nix` in `nightly-macos.yml` is HALF of what shipped, and this warning was
   > right about the other half.** The flag alone greens CI while every real macOS user stays broken
   > and retires the only signal that says so. What makes it acceptable in `6a855b6d` is that the
   > signal was **moved before it was retired**, not dropped: the product now refuses the launch and
   > names both fixes, so a macOS user meets a sentence instead of `statfs`. **The residual cost is
   > real and is not paid**: the nightly now exercises the `/nix`-shared configuration, so the
   > DEFAULT macOS live-checkout path — the one the refusal governs — is no longer covered by any
   > job. A regression in the refusal itself would be invisible. The test that would close that is a
   > unit assertion on the refusal (`TestDarwinRefusesAStorePrefix`, shipped in the same commit),
   > which pins the decision but not the podman behaviour behind it.

   <!-- vantage: oq id=OQ-BF7 leaning="Stage the prefix under a path the VM already shares (the host home) rather than mounting it from /nix/store, and keep the store path as the build output it is copied from. Baking on macOS only is the safe fallback but it re-splits the backends C8 just unified, and it leaves the macos-user notch with a third shape." -->

   _Leaning:_ **Stage it under a path the VM already shares** (the host home) rather than mounting
   it out of `/nix/store`. Baking on macOS only is the safe fallback, but it re-splits the backends
   C8 just unified. Either way the choice decides what roots the prefix, so it gates
   [OQ-BF3](#OQ-BF3).

   **Answer (2026-09-08): the outage is fixed by a THIRD option, and the coupling this entry
   claimed is discharged rather than deferred.**
   > **What shipped (`6a855b6d`).** Neither staging nor baking: a **refusal**.
   > `prefixUnreachableFromVM` (`internal/cli/run/jailprefix.go`) declines the launch on darwin when
   > either mount source is under `/nix/store` and `YOLO_NIX_HOST_DAEMON` is unset, naming two ways
   > forward — share `/nix` with the VM (`podman machine init -v /nix:/nix`, which is init-only) and
   > set that variable, or launch from an installed bundle, which stages under `$HOME` and ships
   > prebuilt binaries so nothing is built in the store at all. It reuses that variable rather than
   > adding a dial because it already means exactly "my runtime VM shares `/nix`".
   >
   > **Why not the leaning.** Staging under the host home is a ~66 MB copy per build plus a
   > lifecycle to reap — a second delivery mechanism for the binaries, which is the one thing C8
   > exists to have only one of. And the population it serves is a macOS developer pointed at a live
   > checkout: every Homebrew and `just install` user is already on an `$HOME` bundle and was never
   > broken. One line of machine setup is cheaper than a mechanism. **The leaning is not refuted** —
   > if that population grows, staging is still the shape, and it would then also remove the
   > refusal.
   >
   > **The coupling to [OQ-BF3](#OQ-BF3) is gone, and that is the part worth reading.** This entry
   > claimed the choice "decides what roots the prefix". Under the refusal there is no third
   > location to root: a macOS launch either shares `/nix` and holds a store path — rooted by
   > [OQ-BF4](#OQ-BF4)'s mechanism, identically to Linux — or runs from an `$HOME` bundle, which is
   > not a store path and has nothing to root or reclaim. So BF3 and BF4 need no macOS arm, and
   > neither is gated on this any more.
   >
   > **MEASURED ON HARDWARE 2026-09-09 (run 34300425634): the statfs failure is GONE — zero
   > occurrences against 59 in the pre-fix run — and it exposed the next layer.** With the prefix
   > mount reachable, every launch got as far as the image build and then failed there:
   >
   > ```text
   > IMAGE BUILD FAILED — the jail image was NOT rebuilt from this source tree.
   >   cannot build on 'ssh-ng://root@127.0.0.1:31022': failed to start SSH connection
   > ```
   >
   > Two facts, both new, and neither is a regression from the fix. **(1)** A darwin-side eval
   > cannot vouch for a Linux-runner-built image, so the launcher refuses it as "a DIFFERENT source
   > tree" and demands a from-source rebuild — an `x86_64-linux` derivation on a darwin host, which
   > offloads to the Linux builder container. **(2) That offload has never run in this job at all**
   > — 0 starts in the pre-fix run and 0 in the last green nightly (2026-09-04), verified in both
   > full logs. It was unreachable behind the statfs failure and is now the visible one.
   >
   > CI answers this with `YOLO_ALLOW_STALE_IMAGE=1`, which is the hatch's stated purpose: the job
   > asserts its own state, because the loaded image IS this commit's, built by the `build-image`
   > job two steps earlier. The harness already downgrades its own skew check on darwin for the
   > identical structural reason. What stays unproven is the container-builder offload on macOS,
   > which the [`macos.md`](../guides/macos.md) runbook claims and no CI job exercises.
   >
   > **Still not verified on hardware:** Apple Container. The same VM fact applies and no
   > measurement exists ([`image-staging-vs-baking.md`](../reference/image-staging-vs-baking.md#backends)).
   > The refusal covers it — it is keyed on darwin, not on the runtime — so that backend now fails
   > with a sentence rather than `statfs`, which is the most that can be claimed from here.

8. ✅ **[OQ-BF8](#OQ-BF8) — DISSOLVED 2026-09-08 by a sibling doc: is the load sentinel's LRU of 10
   the right size, now that it is the floor?**
   [§2.2](#22-the-image-reap-priced-against-this-store) measured that the LRU, not `--keep-images`, decides what survives a reap — all ten
   entries mapped to images in the store, so `keep=2` protected nothing extra. `AddLoadedPath`
   justifies the list as a **liveness veto** over *concurrent jails*, not as a reuse cache, and the
   `10` has no derivation. Bounded by its own stated purpose it is the number of jails that can run
   at once; after C8 the count of distinct images barely moves at all, since the image now changes
   only on `flake.*` and `packages:` ([§2.5](#25-does-anything-ever-read-it-back--reuse-per-store)).

   <!-- vantage: oq id=OQ-BF8 leaning="Keep the veto but stop letting its length set retention: size it by concurrent jails (a small number, and derivable from the container list rather than guessed), and let --keep-images own the reuse buffer it is already named for. Do not simply lower 10 to a smaller constant with no derivation - that repeats the defect at a new number." -->

   _Leaning:_ **Keep the veto, stop letting its length set retention.** Size it by concurrent jails
   — derivable from the container list rather than guessed — and let `--keep-images` own the reuse
   buffer it is already named for. *(⚠ Preserved as the leaning it was: `--keep-images` no longer
   exists, deleted 2026-09-09 by [`OQ-LS3`](./the-load-sentinel-is-not-a-liveness-oracle.md#111-decision-ledger), so this half of the leaning is now unbuildable as
   written. The answer below went further in the same direction.)* Lowering `10` to another underived constant repeats the defect
   at a new number.

   **Answer (2026-09-08): the question dissolves — its premise is being removed, not answered.**
   > The maintainer pointed at the newer work: *"read the latest design docs added, there are
   > comments on this LRU and I think we're going to ditch it totally."*
   > [`the-load-sentinel-is-not-a-liveness-oracle.md`](./the-load-sentinel-is-not-a-liveness-oracle.md)
   > goes further than this entry's leaning and in the same direction: **liveness comes from
   > `podman ps`**, and the sentinel stops being cited as liveness anywhere. Its P1 is the general
   > form of what this entry noticed locally — *recency is a cache policy, not a liveness proof*.
   >
   > So "is 10 the right size" has no answer to give, because 10 stops being safety. Once the veto
   > is the runtime's, the cap is a pure cache dial: it decides how many nix closures yolo would
   > rather not rebuild, nothing more, and it needs no derivation from concurrent jails. This
   > entry's actual contribution survives as the measurement that motivated the change —
   > [§2.2](#22-the-image-reap-priced-against-this-store) is where "the LRU, not `--keep-images`,
   > decides what survives a reap" was measured, all ten entries mapping to images in the store.
   >
   > **One correction to "ditch it totally", so the next reader does not over-apply the ruling.**
   > That doc explicitly does **not** delete the sentinel: it keeps its MRU role for nix GC-root
   > retention and for the human-readable "why did this load?" diagnosis, and says so in its
   > [§7](./the-load-sentinel-is-not-a-liveness-oracle.md#7-what-this-does-not-propose). What is
   > being ditched is the sentinel's **authority over liveness**. Whether the GC-root half loses it
   > too is that doc's [OQ-LS1](./the-load-sentinel-is-not-a-liveness-oracle.md#111-decision-ledger), and it
   > belongs there rather than here.
   >
   > **What this doc must not do in the meantime**, since it proposes new automatic reapers:
   > [OQ-BF4](#OQ-BF4)'s prefix roots must not be protected by the sentinel — see that entry's
   > correction, which is this same mistake in a new place.

9. ✅ **[OQ-BF9](#OQ-BF9) — RULED 2026-09-08, BUILT 2026-09-08 (`internal/cli/stores`; the maintainer's words, verbatim): may `yolo stores` write a sample ledger, so growth is measurable at all?** A
   growth rate needs two dated samples, and a read-only command cannot produce the first one.
   Almost every "growth" cell in [§2.1](#21-every-store-one-table) reads NOT MEASURED for exactly this reason — there
   was no second sample — so the inventory would ship able to say how big a store is and unable to
   say whether it is growing, which is the number that decides whether a store needs a policy at
   all. The cost is that [§5.5](#55-yolo-stores--the-inventory-including-what-nothing-reclaims)'s
   "must never mutate" acquires one exception, and an exception to a forbidden-behavior rule is
   worth a ruling rather than a default.

   <!-- vantage: oq id=OQ-BF9 leaning="Yes: append one dated line per store per run under the state dir, default on, --no-record to opt out, and the command is the single writer of that ledger. Size it bounded (last 30 samples per store) so the handle on growth cannot itself become a store that grows. A pure-read command that can never report a rate fails the maintainer's stated purpose - keeping a handle on stores whose control comes later." -->

   _Leaning:_ **Yes — append one dated line per store per run**, default on, `--no-record` to opt
   out, the command as the ledger's single writer, and the ledger itself **bounded** (last 30
   samples per store) so the handle on growth cannot become a store that grows. A pure-read command
   that can never report a rate fails the stated purpose.

   **Answer (2026-09-08):**
   > **"Yes: append one dated line per store per run under the state dir, default on, `--no-record`
   > to opt out, and the command is the single writer of that ledger. Size it bounded (last 30
   > samples per store) so the handle on growth cannot itself become a store that grows. A pure-read
   > command that can never report a rate fails the maintainer's stated purpose — keeping a handle
   > on stores whose control comes later."**
   >
   > The leaning, ruled as written. This is the exception [§5.5](#55-yolo-stores--the-inventory-including-what-nothing-reclaims)'s
   > "must never mutate" acquires, and it is bounded in three ways rather than one: one writer, one
   > line per store per run, thirty samples retained.
   >
   > **It unblocks a question in another doc**, which is worth recording because that doc has been
   > waiting on exactly this: [OQ-DF4](./minimal-disk-footprint.md#OQ-DF4) ("a stated number or only
   > a policy?") held itself open pending a re-measurement that nothing could take, since a rate
   > needs two dated samples and nothing had ever taken the first. This ledger is that instrument.
   > **Rule this before DF4**, in that order.

10. ✅ **[OQ-BF10](#OQ-BF10) — RULED 2026-09-08: should yolo ALIAS a host cache rather than pool a
    second copy of it — and for which caches?** This is a third disposition the rest of the doc does not have. Backfill deletes
    what accumulated; retention bounds what accumulates; aliasing makes the store **not exist
    twice**. For pants' content-addressed half that is up to 27 G on this machine, and the same
    question applies to npm, uv, pip and go-build, whose host-side copies a jail cannot see to
    measure. yolo already shares the host's nix store read-only rather than duplicating it
    (`hostNixStore`), so the pattern exists; it has never been applied to a cache.

    Three things the ruling has to settle, because they do not follow from each other:
    **(1) which half** — only the content-addressed one; `named_caches` is path-poisoned and must
    stay per-frame ([§2.5](#25-does-anything-ever-read-it-back--reuse-per-store) Class C).
    **(2) writable or read-only** — a cache is useless read-only, so this is a WRITABLE host bind,
    which is a strictly bigger trust step than the `:ro` nix store precedent: jail code could then
    write into the host user's own cache. **(3) the migration** — aliasing does not delete the
    40 G already pooled jail-side; it strands it, so a ruling here creates its own backfill item.

    <!-- vantage: oq id=OQ-BF10 leaning="Yes for the content-addressed half, gated on the host being the same OS and arch as the jail, writable, and only for caches whose tool documents a shared per-user store (pants lmdb_store, npm, go-build). Not named_caches, ever. Treat the stranded jail-side copy as a backfill row on the first aliased launch. Do NOT alias on macOS - the jail is Linux in a VM and nothing is compatible." -->

    _Leaning:_ **Yes for the content-addressed half only**, gated on the host matching the jail's OS
    and arch, writable, and limited to caches whose own tool documents a shared per-user store —
    pants' `lmdb_store`, npm, go-build. Never `named_caches`. Not on macOS at all. And the stranded
    jail-side copy becomes a backfill row on the first aliased launch, which is the honest cost.

    **Answer (2026-09-08):**
    > **CAS only, and the reason is a threat model rather than a size: *"I think it must be CAS
    > only. otherwise that's an injection mechanism from jail to host?"*** Yes — and that framing is
    > sharper than the leaning's, so it replaces it as the rule's justification.
    >
    > A **content-addressed** store is one whose keys are digests of their own contents. A jail
    > writing into it can add a blob, and it can add a *wrong* blob only under a name that does not
    > match its digest, which the reading tool rejects. A **path-keyed** store like pants'
    > `named_caches` has no such property: the key is a name the jail chooses, so a jail write is
    > content the host tool will later read **because of where it sits**. That is the injection
    > channel, and it is why `named_caches` is not "lower value" here but categorically excluded —
    > the same distinction that lets yolo share the host's nix store at all.
    >
    > So the ruling is the leaning **minus any latitude on the CAS boundary**: aliased if and only
    > if content-addressed, gated on the host matching the jail's OS and arch, writable, limited to
    > tools that document a shared per-user store; never `named_caches`; never on macOS, where the
    > jail is Linux in a VM and nothing is compatible anyway. The stranded jail-side copy becomes a
    > backfill row on the first aliased launch.
    >
    > **And the scope stops here, deliberately:** *"let's keep it this way and we can analyze
    > storage after all this is implemented to see if we should go further."* Widening the aliased
    > set is not a follow-up item in this doc; it is a question to re-ask against a
    > post-implementation measurement, which [OQ-BF9](#OQ-BF9)'s sample ledger is what makes
    > possible.

    > [!NOTE]
    > **BUILT 2026-09-09 — `internal/hostcas`**, emitted by `internal/cli/run/hostcasalias.go` and
    > explained by a new `yolo stores` section. Six things the build had to settle that the ruling
    > left implicit, recorded here because each is a decision a reader would otherwise re-litigate:
    >
    > 1. **No config key, and no env dial.** The ruling's "gated on …" list is exhaustive and
    >    contains no user opt-in, the precedent it names (`hostNixStore`) is a capability probe
    >    rather than a dial, and there is nothing for a key to carry: yolo picks BOTH sides of the
    >    mount from a table, so no path is user-supplied. The recognised set is therefore code
    >    (`hostcas.Stores`), which is also what "the scope stops here" needs — a config key would
    >    hand the CAS test to a user who cannot check it.
    > 2. **The set is pants' `lmdb_store` ALONE**, which NARROWS the leaning's "pants
    >    `lmdb_store`, npm, go-build". Under "aliased if and only if content-addressed" npm and
    >    go-build both fail, and the reason is MEASURED rather than cautious: npm's
    >    `_cacache/index-v5` maps a REQUEST URL to an integrity digest (sampled entry keyed
    >    `make-fetch-happen:request-cache:https://registry.npmjs.org/…`), and Go's `<actionID>-a`
    >    maps a writer-chosen action ID to an output ID. In both, a jail that writes the index AND
    >    the blob has the host tool read its bytes for that key — the injection channel, in a store
    >    whose content half really is a CAS. Half a directory whose halves must stay consistent is
    >    not an aliasable unit. `uv` and `pip` are index/URL-keyed too.
    > 3. **The CAS claim is measured, not asserted.** `lmdb_store/immutable/files/<2-hex>/<64-hex>`
    >    — and the 64-hex name IS the sha256 of the file's contents, verified on a 232 MB blob
    >    2026-09-09; those files are mode `r-xr-xr-x`.
    > 4. **Two gates the ruling does not name, both found by asking what the happy path costs.**
    >    (a) **The cold-start refusal.** An EMPTY host store aliased over a warm private copy hides
    >    a cache and buys nothing, forcing exactly the unbounded re-fetch
    >    [§5.2](#52-two-tiers-one-mapping)'s offered tier exists to never impose without asking (P4).
    >    Emptiness is one `ReadDir`, so it costs nothing. The partial case — a small host store over
    >    a large private one — is allowed: it is a one-time partial re-fetch into a store BOTH sides
    >    then reuse. (b) **`cache_relocations` wins.** A user who relocated `pants` moved 40 G off
    >    the disk `$HOME` is on; the alias source IS under the home cache, and both mounts would
    >    apply (podman orders by destination depth, so the deeper alias wins for its own subtree),
    >    putting 27 G of it silently back — *the exact failure `cache_relocations` exists to
    >    prevent, caused by the feature meant to save space*. An explicit config decision outranks
    >    an automatic optimisation, so a store whose cache segment is relocated is never aliased.
    >    It is also the one SILENT decline the user caused, which is where the launch's disclosure
    >    line is drawn: an active alias always prints (a writable bind of the host user's cache is
    >    host access), and so does a decline the user could act on — unwritable, not a directory,
    >    empty over a warm copy, or an unmakeable mountpoint. Everything else is a structural fact
    >    about the machine with no action behind it, and prints nothing, on §5.3's degenerate rule
    >    and §5.1's warning about what launch noise costs. `yolo stores` is where every decline is
    >    legible on demand.
    > 5. **Concurrency adds no new class, and yolo serialises nothing.** It cannot: it does not
    >    launch the host's tool. It does not need to either — yolo ALREADY pools one `lmdb_store`
    >    across every workspace's jail (that is how the 27 G got there), and each jail is its own
    >    PID namespace, so concurrent multi-namespace writers are the shipped status quo rather
    >    than something aliasing introduces. What aliasing widens is the participant SET, not its
    >    kind. **The named residual:** LMDB's stale-reader reaping is keyed on pid through an
    >    `fcntl` record lock on `lock.mdb` — namespace-blind and shared, so it cannot reap a LIVE
    >    reader; the failure direction is a slot nothing reclaims, i.e. wasted space, which is the
    >    ruling's own stated worst case. Whether its process-shared robust write mutex recovers a
    >    dead owner whose TID was in another PID namespace is **NOT MEASURED**.
    > 6. **Nothing is recorded, so there is no writer to name.** The decision is a pure function of
    >    the host's filesystem and platform, so the launcher and `yolo stores` re-derive it through
    >    one `hostcas.Plan`. A stamp would be a second source of truth for a question the disk
    >    answers, and it would go stale the first time a user deleted their pants cache.
    >
    > **The stranded copy, per the ruling:** nothing is migrated and nothing is deleted by the
    > alias. The bind lands ON the private copy's own directory, so the bytes simply stop being
    > read; `PurgeCacheByAge` already covers `pants`, and the new `yolo stores` row names the path
    > and the reclaimer (pinned against prune's own subdir list, so the row cannot promise a
    > reclaimer that would not run).
    >
    > **Not double-counted:** the aliased row is its OWN section with verdict `not yolo's`, because
    > [§5.5](#55-yolo-stores--the-inventory-including-what-nothing-reclaims)'s state-dir row sums the
    > shared-cache section and a host-owned 27 G tree folded in there would inflate yolo's own
    > footprint. `yolo prune` needs no change: `PurgeCacheByAge` is rooted at
    > `paths.GlobalCache()/<subdir>` and the alias source is under the host user's XDG cache root,
    > which the same-tree gate keeps outside it.

## 12. Inherited rulings

Not this doc's decisions; recorded because its shape depends on them. The owning docs' ledgers
are authoritative.

| ID | Ruling | What it fixes here |
| :--- | :--- | :--- |
| `image-staging` [OQ-5](../reference/image-staging-vs-baking.md#why-its-this-way) | The tar backlog is a **bug**; minimal disk is the goal; yolo **may** delete without `--apply`. | Licenses the automatic tier at all; the offered tier is this doc's refinement for classes that ruling did not measure. |
| `minimal-disk` [OQ-DF1](./minimal-disk-footprint.md#112-open-questions) | *"Stream, keep zero tars."* | L4's number is that ruling applied to the reaper's default ([OQ-BF6](#OQ-BF6)). |
| `minimal-disk` [OQ-DF3](./minimal-disk-footprint.md#OQ-DF3) (NUMBER + TRIGGER) | `keep=2`, automatic on the launch path, debounced 24 h, `YOLO_NO_AUTO_IMAGE_REAP=1`. | The precedent P3 generalises and P4 distinguishes ([§4](#4-why-backfill-and-steady-state-do-not-share-a-disposition)); its REACH half stays open there. |
| `image-staging` [OQ-8](../reference/image-staging-vs-baking.md#why-its-this-way) (C8) | yolo's binaries are delivered by mount; the out-link is the prefix's GC root, keyed by checkout. | The store-garbage finding ([§2.3](#23-yolos-own-store-outputs-are-never-collected--the-c8-finding)) and [OQ-BF4](#OQ-BF4). |
| `agent-cli-copies` A7 / [`../plans/evergreen-agent-updates.md`](../plans/evergreen-agent-updates.md) | Keep-newest-2 over the vendor's version dir, run by the act that installed the new one. | L7 widens the trigger, not the rule. |
| `program-delivery` [OQ-PD17](./program-delivery.md#decision-ledger) | Capture store reap is the complement of the resolver; K = 1; no age floor. | L8 gives it a trigger, not a policy. |
