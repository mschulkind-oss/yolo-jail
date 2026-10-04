---
title: "Patched forks — implementation sketch"
date: 2026-10-03
status: draft
stage: SKETCH
tags: [plan, packs, programs, forks, build]
summary: "Where the patched-fork mode's code would land, for programs and for pi extensions, read against the tree at 026fca672 and 48491fd4a, and the traps found while designing it. A parking lot for the designs, not a hand-off: nothing here may be built from while it is a sketch."
next: "The build started 2026-10-04 works from this map; record what the tree changed as each step lands, then graduate it into the designs' status lines"
depends-on:
  - patched-forks.md
  - patched-extensions.md
---

# Patched forks — implementation sketch

**Status:** 2026-10-03, extended 2026-10-04 to patched extensions — incomplete, and unstable while
questions are open.

**Designs:** [`patched-forks.md`](patched-forks.md), and [`patched-extensions.md`](patched-extensions.md),
which shares its implementation through the owner key ([PF-D22](patched-forks.md#PF-D22)).
**Precedence:** the designs win on behavior, the tree wins on fact. No design decision is made here;
an entry that would need one is a question in a design.

## Map, sketched

Read at `026fca672`. Every row is a pointer to check before relying on it.

| Where | What the mode touches |
| :--- | :--- |
| [`packdecl/contributes.go`](../../internal/packdecl/contributes.go#L48-L88) | `patches` and `follow` beside the four fork fields; their placement refusal in `forkFieldPlacementProblems` ([`fork.go:67`](../../internal/packdecl/fork.go#L67)) |
| [`packdecl/fork.go`](../../internal/packdecl/fork.go#L280-L286) | the recipe's canonical array gains the series digest for a patched fork only; a plain fork's bytes must not move |
| [`packdecl/packdecl.go`](../../internal/packdecl/packdecl.go#L471) | the strict `Decode`'s unknown-field refusal gains *"a newer yolo may read this field"* (the design's PF-D1) |
| [`packload/forks.go`](../../internal/packload/forks.go#L47-L65) | `Fork` gains the series (read once, `lstat` on every component, from the fork pack's `Root`) and the follow rule; `ApplyForks` carries the digest onto the base's `Install`; `forkClaimDetail` names the series |
| `packsrc` (new) | the check record and its lock per owner key ([PF-D22](patched-forks.md#PF-D22)), beside `stamps/`; a fetch-and-resolve with no checkout, split from `refreshMirror` above its `materialize` ([`refresh.go:420`](../../internal/packsrc/refresh.go#L420)); the follow rule, and the newest-fit walk's list ([PF-D10](patched-forks.md#PF-D10)) |
| [`packsrc/forkpin.go`](../../internal/packsrc/forkpin.go#L79), [`packload/forkpin.go`](../../internal/packload/forkpin.go#L101) | patched forks kept out of `PinForks` at every caller ([`run/forkbuild.go:113`](../../internal/cli/run/forkbuild.go#L113), [`cli/hostfloor.go:105-118`](../../internal/cli/hostfloor.go#L105-L118), [`capturehost.go:267-271`](../../internal/cli/capturehost.go#L267-L271)); `InJailForkPins`'s reason for a patched fork names the host build, not an install |
| [`cli/forkpin.go`](../../internal/cli/forkpin.go#L38) | `pinForks` skips a patched fork and drops a plain-fork entry under its key; `pack update`, `pack install` and `pack status` arms; `forkBuiltState` ([`:180-189`](../../internal/cli/forkpin.go#L180-L189)) reads the good build |
| [`cli/forkbuild.go`](../../internal/cli/forkbuild.go#L82) | the build id from repository and subdirectory, not the source as written |
| [`cli/forkbuild.go`](../../internal/cli/forkbuild.go#L310-L326) | `checkOutForkSource`'s patched arm: the scratch repository outside staging, the prefetch into the mirror, the replay down the walk's list, the copy into `src/`, the patched tree |
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

## Patched extensions

Read at `48491fd4a`, for [`patched-extensions.md`](patched-extensions.md). The rows above serve it
through the owner key; these are what only a tree touches.

| Where | What the mode touches |
| :--- | :--- |
| [`packdecl/contributes.go`](../../internal/packdecl/contributes.go#L3698) | `source`, `patches`, `follow`, `build` and `produces` on `files`; `from`, `fork_of`, `agent` and `agents` refused beside `source`; the extension key's uniqueness against the pack's patched bins |
| [`packdecl/fork.go`](../../internal/packdecl/fork.go#L47-L88) | `forkFieldPlacementProblems` lets `source`, `build` and `produces` onto `files` beside `patches` only; its message elsewhere is unchanged |
| [`run/packfiles.go`](../../internal/cli/run/packfiles.go#L67-L99) | a patched extension's mount source is the launch's per-launch copy, not `<root>/<from>` |
| [`run/run.go`](../../internal/cli/run/run.go#L1323-L1332) | the tree arm beside the fork builds in their slot: check, advance, copy, and `YOLO_PATCHED_TREES` |
| [`cli/forkbuild.go`](../../internal/cli/forkbuild.go#L388-L406) | the final copy into the reserved directory; the seal narrowed to the contributing pack; the admit's three new checks |
| [`capture/select.go`](../../internal/capture/select.go#L41-L52), [`capture/gc.go`](../../internal/capture/gc.go) | a selection that names no bin, by extension key, which gc's complement reaps by too |
| [`entrypoint/hostfilestree.go`](../../internal/entrypoint/hostfilestree.go#L59-L89) | the host arm: the versioned copies and the owned link |
| [`packload/footprint.go`](../../internal/packload/footprint.go#L489-L498) | the review-marked claim in place of *"read-only tree"* |

Traps:

- **The fork fields are refused on `files` today** ([`fork.go:62-88`](../../internal/packdecl/fork.go#L62-L88)):
  `source`, `build` and `produces` all fail placement on any kind but `program` with `via: "source"`.
- **`packFilesTargets` joins `from` unconditionally** ([`packfiles.go:93`](../../internal/cli/run/packfiles.go#L93)).
  A contribution with no `from` mounts the pack's whole staged tree at `into`.
- **The host render skips a contribution with no `from`** ([`hostfilestree.go:62`](../../internal/entrypoint/hostfilestree.go#L62)),
  so without its own arm the host drops a patched extension with no message.
- **The per-launch copy goes beside the pack tree, never in it.** The attach compares each pack's
  staged digest ([`run/packtree.go:316-317`](../../internal/cli/run/packtree.go#L316-L317)), and the
  boot's fallback walk reads every top-level directory as a pack
  ([`packtreerecord.go:22-27`](../../internal/packload/packtreerecord.go#L22-L27)). Nothing lists the
  tree root today (`PackTreeRoot`'s callers searched 2026-10-04), but a name a tree or `.live` could
  spell would collide.
- **Never hardlink.** `capture.Materialize` tries a hardlink second
  ([`materialize.go:227`](../../internal/capture/materialize.go#L227)), and on the host the copy and
  the store share a mount, so the arm would succeed.
- **`missingProduces` compares home-relative paths** ([`cli/forkbuild.go:217`](../../internal/cli/forkbuild.go#L217));
  a tree's `produces` are tree-relative and must be joined onto the reserved directory first.
- **At the host, the advance runs before the launch gate's observe comparison and outside it**
  ([`hostapplygate.go`](../../internal/cli/hostapplygate.go)). Run after it, the gate compares
  against a render the apply would change at once; run inside it, a check blows the one-second
  `hostApplyGateBudget` and the gate reports cannot-determine.
- **At `yolo host -- <bin>`, only for the owning agent pack's programs and their forks.** The gate
  surveys the whole render whatever `<bin>` is
  ([`hostapplygate.go:189`](../../internal/cli/hostapplygate.go#L189)), so a check wired in front of
  it unscoped makes `yolo host -- claude` wait on a pi extension's fetch and build.
- **The `files` emitter has no read-only-floor check** ([`packfiles.go:123-140`](../../internal/cli/run/packfiles.go#L123-L140)).
  That is what lets Apple Container below the floor take a per-launch copy; a fix that adds the check
  for other reasons must not drop this delivery.
- **The fork slot returns below Apple Container's read-only floor before any build**
  ([`run/forkbuild.go:63-68`](../../internal/cli/run/forkbuild.go#L63-L68)). The tree arm takes that
  return for its check and build, and must not take it for delivery: a good build already on the
  machine is still copied and mounted there.
- **A reap can empty a store entry while a launch copies it.** The store accepts that only because a
  failed materialize falls through to a vendor installer
  ([`gc.go:45-48`](../../internal/capture/gc.go#L45-L48)); a tree has none, so the copy is checked
  against the entry's completion marker after it ends, and the move must reap marker first, as
  `reapEntry` does ([`gc.go:219-224`](../../internal/capture/gc.go#L219-L224)).

## Decided on 2026-10-04

Every question this map waited on was decided on its leaning under the maintainer's delegation of
2026-10-04, and each stays his to overrule:

- The launch waits for the advance, bounded and interruptible, at a fresh launch and at
  `yolo host -- <bin>` ([PF-D25](patched-forks.md#PF-D25)).
- The owning agent pack's launchers serve the good build and stop before exec when none exists
  ([PPX-D18](patched-extensions.md#PPX-D18)).
- A patched extension writes no pin ([PPX-D19](patched-extensions.md#PPX-D19)).
- A user's own failed edit is not held ([PF-D23](patched-forks.md#PF-D23)).
- `yolo pack rebase` is built ([PF-D26](patched-forks.md#PF-D26)).
- `follow` defaults to the newest version tag ([PF-D24](patched-forks.md#PF-D24)).

## Step 1, landed 2026-10-04

[`patched-forks.md` §14](patched-forks.md#14-what-i-would-build-in-order) step 1, the declaration
and the check with no build. What the tree changed against the map above, and what step 2 starts
from:

| Where | What landed |
| :--- | :--- |
| [`packdecl/contributes.go`](../../internal/packdecl/contributes.go), [`fork.go`](../../internal/packdecl/fork.go), [`packdecl.go`](../../internal/packdecl/packdecl.go) | `patches` and `follow` on `Contribution` and `Install`; `patchedForkProblems`, `PatchesDirProblem` and the placement refusal for both fields; `PatchedForkRecipe`, with `Install.SourceRecipe()` empty for a patched fork ([PF-D31](patched-forks.md#PF-D31)); `unknownFieldHint` |
| [`packsrc/series.go`](../../internal/packsrc/series.go) | `ReadSeries`, `SeriesDigest`, `SeriesError` |
| [`packsrc/follow.go`](../../internal/packsrc/follow.go) | `ParseFollow`, the version grammar |
| [`packsrc/refresh.go`](../../internal/packsrc/refresh.go) | `fetchStep`, split from `refreshMirror` above its resolution and checkout; `fetchMode.keepTags` puts tags back on a forced fetch too |
| [`packsrc/checkrecord.go`](../../internal/packsrc/checkrecord.go) | the record, its lock, `WithCheckRecord`, `CheckDue` ([PF-D28](patched-forks.md#PF-D28)); `GoodBuild` and `OutcomeBuildFailed` are declared and unwritten |
| [`packsrc/patchcheck.go`](../../internal/packsrc/patchcheck.go) | `CheckPatched` (the throttle, the attempt, the fetch, the ref's kind, the base fetched by id, the list), `AboveGood`, `RecordWalk` |
| [`packsrc/replay.go`](../../internal/packsrc/replay.go) | `WalkSeries` ([PF-D29](patched-forks.md#PF-D29)). Built in step 1 rather than 2 because `yolo pack update` reports whether the series replays. Step 2's build act needs one more hook: a way to copy the fit's subdirectory out of the scratch repository into `src/` before the walk removes it |
| [`packload/forks.go`](../../internal/packload/forks.go), [`forkpin.go`](../../internal/packload/forkpin.go), [`footprint.go`](../../internal/packload/footprint.go) | `Fork.Root`, `Patches`, `Follow`, `ReadSeries`, `CheckWant`; the rewrite carries the two fields; the claim names the series; `PinForks` and `ForkPins` skip a patched fork with `PatchedForkPinReason` ([PF-D33](patched-forks.md#PF-D33)) |
| [`cli/patchedfork.go`](../../internal/cli/patchedfork.go), [`cli/forkpin.go`](../../internal/cli/forkpin.go), [`cli/hostfloor.go`](../../internal/cli/hostfloor.go) | `yolo pack update`, `install` and `status` ([PF-D32](patched-forks.md#PF-D32)); the plain-fork entry dropped; `floorForkBuild` carries `patches` |

Left for step 2, against the traps above: the trigger in the fresh-launch slot and its call-site
test; `forkDeliveriesFor` routing a patched fork to the advance before its `Commit == ""` reason;
the build id, the receipt fields, the exact lookup and the selection key; the copy into `src/`; the
swap, the reap and the delivery record; the waiter's record re-read; build failures and their
back-off; the record's recovery from the store; the launch's lines; and `yolo capture`'s patched
arm. The interim `PatchedForkPinReason` goes when the advance lands.

## Measurements to make

- The maintainer's own pi series against the newest upstream version: the replay's time on the
  host, and the build time the design quotes from a stand-in and one host log.
- The prefetch of one pi commit into a blobless mirror of the GitHub remote, which the store measured
  at 4.9 s for a commit of 2,992 files ([`store.go:756-768`](../../internal/packsrc/store.go#L756-L768));
  the design's blobless measurement used a `file://` remote.
