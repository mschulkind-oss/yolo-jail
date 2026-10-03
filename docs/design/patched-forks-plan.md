---
title: "Patched forks — implementation sketch"
date: 2026-10-03
status: draft
stage: SKETCH
tags: [plan, packs, programs, forks, build]
summary: "Where the patched-fork mode's code would land, read against the tree at 026fca672, and the traps found while designing it. A parking lot for the design, not a hand-off: nothing here may be built from while it is a sketch."
next: "Promote against the tree once OQ-PFK1, OQ-PFK3 and OQ-PFK4 are ruled, with the implementation-plan skill"
depends-on:
  - patched-forks.md#OQ-PFK1
  - patched-forks.md#OQ-PFK3
  - patched-forks.md#OQ-PFK4
---

# Patched forks — implementation sketch

**Status:** 2026-10-03 — incomplete, and unstable while questions are open.

**Design:** [`patched-forks.md`](patched-forks.md). **Precedence:** the design wins on behavior,
the tree wins on fact. No design decision is made here; an entry that would need one is a question
in the design.

## Map, sketched

Read at `026fca672`. Every row is a pointer to check before relying on it.

| Where | What the mode touches |
| :--- | :--- |
| [`packdecl/contributes.go`](../../internal/packdecl/contributes.go#L48-L88) | `patches` and `follow` beside the four fork fields; their placement refusal in `forkFieldPlacementProblems` ([`fork.go:67`](../../internal/packdecl/fork.go#L67)) |
| [`packdecl/fork.go`](../../internal/packdecl/fork.go#L280-L286) | the recipe's canonical array gains the series digest for a patched fork only; a plain fork's bytes must not move |
| [`packdecl/packdecl.go`](../../internal/packdecl/packdecl.go#L471) | the strict `Decode`'s unknown-field refusal gains *"a newer yolo may read this field"* (the design's PF-D1) |
| [`packload/forks.go`](../../internal/packload/forks.go#L47-L65) | `Fork` gains the series (read once, `lstat` on every component, from the fork pack's `Root`) and the follow rule; `ApplyForks` carries the digest onto the base's `Install`; `forkClaimDetail` names the series |
| `packsrc` (new) | the check record and its per-fork lock, beside `stamps/`; a fetch-and-resolve with no checkout, split from `refreshMirror` above its `materialize` ([`refresh.go:420`](../../internal/packsrc/refresh.go#L420)); the follow rule |
| [`packsrc/forkpin.go`](../../internal/packsrc/forkpin.go#L79), [`packload/forkpin.go`](../../internal/packload/forkpin.go#L101) | patched forks kept out of `PinForks` at every caller ([`run/forkbuild.go:113`](../../internal/cli/run/forkbuild.go#L113), [`cli/hostfloor.go:105-118`](../../internal/cli/hostfloor.go#L105-L118), [`capturehost.go:267-271`](../../internal/cli/capturehost.go#L267-L271)); `InJailForkPins`'s reason for a patched fork names the host build, not an install |
| [`cli/forkpin.go`](../../internal/cli/forkpin.go#L38) | `pinForks` skips a patched fork and drops a plain-fork entry under its key; `pack update`, `pack install` and `pack status` arms; `forkBuiltState` ([`:180-189`](../../internal/cli/forkpin.go#L180-L189)) reads the good build |
| [`cli/forkbuild.go`](../../internal/cli/forkbuild.go#L82) | the build id from repository and subdirectory, not the source as written |
| [`cli/forkbuild.go`](../../internal/cli/forkbuild.go#L310-L326) | `checkOutForkSource`'s patched arm: the scratch repository outside staging, the prefetch into the mirror, the replay, the copy into `src/`, the patched tree |
| [`cli/forkbuild.go`](../../internal/cli/forkbuild.go#L107-L212) | `buildFork`'s record re-read after the lock ([`:125-131`](../../internal/cli/forkbuild.go#L125-L131)); runtime-refused told apart from a build line that failed; `buildForksForLaunch` looks up the good build, runs the advance, swaps and reaps |
| [`cli/capturematerialize.go`](../../internal/cli/capturematerialize.go#L311-L332), [`capture/select.go`](../../internal/capture/select.go#L41-L52) | the exact lookup by fork key; the fork key in a patched build's selection key, which [`capture/gc.go`](../../internal/capture/gc.go) reaps by too |
| [`entrypoint/buildreceipt.go`](../../internal/entrypoint/buildreceipt.go#L40-L52) | `fork`, `series` and `tree` |
| [`run/forkbuild.go`](../../internal/cli/run/forkbuild.go#L42-L85), [`run/packtree.go`](../../internal/cli/run/packtree.go) | a patched fork bypasses the `Commit == ""` reason ([`:50-52`](../../internal/cli/run/forkbuild.go#L50-L52)); the delivery record beside the jail's pack tree, read by an attach and by the reaper |
| [`cli/pack.go`](../../internal/cli/pack.go#L257-L280) | the `rebase` subcommand, if [OQ-PFK4](patched-forks.md#OQ-PFK4) takes its leaning |
| [`hostfloor/ensure.go`](../../internal/hostfloor/ensure.go#L155-L190), [`floor.go`](../../internal/hostfloor/floor.go#L200-L215), [`built.go`](../../internal/hostfloor/built.go#L176) | the four recipe readers read the good build's (`servesANearMiss`, `buildPending` at [`floor.go:521`](../../internal/hostfloor/floor.go#L521), `ResolveBuild` and `Build` at [`cli/hostfloor.go:121-129`](../../internal/cli/hostfloor.go#L121-L129), the record); `refresh` gains a source arm under `UpdatesAllowed` |

## Traps

- **The check cannot reuse the per-mirror step's stamp.** `refreshMirror` runs `rev-parse` before
  it reads its stamp ([`refresh.go:316-335`](../../internal/packsrc/refresh.go#L316-L335)), and the
  launch's pack refresh can leave that stamp fresh. The fork's own stamp is read first.
- **The check must not sit in `noteForkPins`.** That runs above the dispatch on every launch,
  attach included ([`run.go:319`](../../internal/cli/run/run.go#L319)); the check belongs with the
  build, in the fresh-launch slot ([`run.go:1330`](../../internal/cli/run/run.go#L1330)).
- **`refreshMirror` checks out every commit it resolves** ([`refresh.go:420`](../../internal/packsrc/refresh.go#L420)),
  and nothing reaps the pack store's `trees/`. A check through it would leave a 23 MB tree of pi per
  move of `main`.
- **A repository that borrows the mirror's objects has no promisor.** Its checkout cannot fetch a
  missing blob whatever `GIT_NO_LAZY_FETCH` says; the blobs go into the mirror first, through the
  store's checked prefetch, under the mirror's lock. Measured in the design's [§5.1](patched-forks.md#51-where-it-runs).
- **`storeGitConfig` honors the user's git config** ([`store.go:259-276`](../../internal/packsrc/store.go#L259-L276)).
  The replay must not run through the store's `gitCmd` environment as it stands: it needs
  `GIT_CONFIG_GLOBAL=/dev/null`, `GIT_CONFIG_NOSYSTEM=1`, no signing, `--whitespace=nowarn` and a
  fixed identity. The fetch must keep the user's config, for its credential helpers.
- **The staging workspace is the build jail's `/workspace`** ([`cli/forkbuild.go:403-406`](../../internal/cli/forkbuild.go#L403-L406)).
  The scratch repository goes outside it, and `src/` gets the subdirectory through `copySourceTree`.
- **The git version.** `git cherry-pick --empty=drop` needs git 2.45, and the store runs on older
  gits, which ignore `GIT_NO_LAZY_FETCH` ([`store.go:320`](../../internal/packsrc/store.go#L320)).
  The pick's mechanism either works on the oldest git the store does or names the git it needs in
  the fork's reason.
- **`PinForks` writes an entry before any build** ([`forkpin.go:121-153`](../../internal/packsrc/forkpin.go#L121-L153)),
  and three callers reach it. Filtering one of them leaves a patched fork pinned at a branch head by
  the other two.
- **`resolveForkBuild` is newest-then-check, keyed on the source as written**
  ([`capturematerialize.go:316`](../../internal/cli/capturematerialize.go#L316)), and so is the build
  id ([`cli/forkbuild.go:82`](../../internal/cli/forkbuild.go#L82)). A patched fork needs the exact
  lookup, and the selection key change reaches `gc.go`, which reaps by the same selection.
- **`buildFork` re-checks only for a store hit after its lock** ([`cli/forkbuild.go:125-131`](../../internal/cli/forkbuild.go#L125-L131)),
  so a waiter rebuilds a failure unless it reads the record there.
- **Every pack-store flock blocks with no bound** ([`refresh.go:668-704`](../../internal/packsrc/refresh.go#L668-L704)).
  The design's lock order is the only thing between two launches and a hang; a test should take the
  locks in both orders and fail on the inverted one.
- **The source launcher materializes lazily** ([`forklauncher.go`](../../internal/entrypoint/forklauncher.go)),
  so a reap that ignores the running jails' delivery records leaves a jail's `pi` missing.
- **The call-site rule** (AGENTS.md, Testing): the trigger's test must fail when the check's call in
  the fresh-launch slot is deleted, as `TestALaunchWiresTheForkBuildTrigger` does for the build.

## Waiting on the design

- How the launch path is shaped after the check, waiting or not, and whether `yolo host -- <bin>`
  waits, is blocked on [OQ-PFK3](patched-forks.md#OQ-PFK3).
- Whether the good build is served, and the floor keeps it, after the user's own edit fails is
  blocked on [OQ-PFK1](patched-forks.md#OQ-PFK1); the map above assumes its leaning.
- The `rebase` subcommand is blocked on [OQ-PFK4](patched-forks.md#OQ-PFK4).
- [OQ-PFK2](patched-forks.md#OQ-PFK2) picks only the default of `follow`, which is one constant.

## Measurements to make

- The maintainer's own pi series against the newest upstream version: the replay's time on the
  host, and the build time the design quotes from a stand-in and one host log.
- The prefetch of one pi commit into a blobless mirror of the GitHub remote, which the store measured
  at 4.9 s for a commit of 2,992 files ([`store.go:756-768`](../../internal/packsrc/store.go#L756-L768));
  the design's blobless measurement used a `file://` remote.
