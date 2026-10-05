---
title: "Patched forks — implementation sketch"
date: 2026-10-03
status: draft
stage: SKETCH
tags: [plan, packs, programs, forks, build]
summary: "Where the patched-fork mode's code would land, for programs and for pi extensions, read against the tree at 026fca672 and 48491fd4a, and the traps found while designing it. A parking lot for the designs, not a hand-off: nothing here may be built from while it is a sketch."
next: "Built and integrated at e87f1ba88 (2026-10-05); what is left is the maintainer's: his test with the migration kit, OQ-PFK5, PPX-D16's lint, and graduating this map into a system reference once the build is on main"
depends-on:
  - patched-forks.md
  - patched-extensions.md
---

# Patched forks — implementation sketch

**Status:** 2026-10-03, extended 2026-10-04 to patched extensions. Built 2026-10-04: patched forks'
steps 1 to 4 and patched extensions' steps 2 to 5, each recorded below as it landed, and integrated
on 2026-10-05 at `e87f1ba88` ([Integration](#integration-landed-2026-10-05)). Open:
[OQ-PFK5](patched-forks.md#OQ-PFK5), macos-user delivery, which is the maintainer's.

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
arm. The interim `PatchedForkPinReason` goes when the advance lands, and with it the interim lines
of [PF-D35](patched-forks.md#PF-D35): `cli.patchedNotBuilt` becomes "the next launch builds it",
`yolo pack status` gets its next-check line back, and `captureFork`'s patched arm builds. Step 2
also writes `GoodBuild.Read` at the move, which the list's cut reads
([PF-D34](patched-forks.md#PF-D34)). Step 3's `yolo pack rebase` replaces `cli.rebaseSteps`, the
conflict message's by-hand rebase.

## Step 2, landed 2026-10-04

[`patched-forks.md` §14](patched-forks.md#14-what-i-would-build-in-order) step 2, the advance at a
fresh launch, for the jail notches (podman on Linux or a macOS VM, and Apple Container through its
read-only floor, which the fork slot already reads). What the tree changed against the map above:

| Where | What landed |
| :--- | :--- |
| [`entrypoint/buildreceipt.go`](../../internal/entrypoint/buildreceipt.go), [`capture/select.go`](../../internal/capture/select.go), [`capture/gc.go`](../../internal/capture/gc.go) | the receipt's `fork`, `series`, `tree`, `tag`, `version`; selection and the prune keyed by fork (`Record.Program`); `capture.Scan` and `Store.ReapEntry` ([PF-D36](patched-forks.md#PF-D36)) |
| [`packsrc/replay.go`](../../internal/packsrc/replay.go), [`store.go`](../../internal/packsrc/store.go), [`patchcheck.go`](../../internal/packsrc/patchcheck.go), [`pidlock`](../../internal/pidlock/pidlock.go) | `WalkOptions.OnFit`, the hook step 1 left for the copy into `src/`; `Store.Ctx`; `CheckRecord.HeldAt`; `Mode.Cancel` |
| [`cli/patchedadvance.go`](../../internal/cli/patchedadvance.go) | the advance: the check, the pending list, the walk, the build, the move under the record lock, the hand and the reap, the recovery, the waiter's re-read, the failure record and its back-off; `resolvePatchedBuild`, the exact lookup |
| [`cli/forkbuild.go`](../../internal/cli/forkbuild.go) | the build id from the fork key, repository and subdirectory; the patched recipe; the replay into `src/`; a jail that never ran its build line told apart ([PF-D39](patched-forks.md#PF-D39)) |
| [`cli/forkbuildchild.go`](../../internal/cli/forkbuildchild.go), [`run/interruptscope.go`](../../internal/cli/run/interruptscope.go) | the interruptible wait: the build jail as a child, `yolo internal fork-build-jail`, under an interrupt scope ([PF-D38](patched-forks.md#PF-D38)) |
| [`run/forkbuild.go`](../../internal/cli/run/forkbuild.go), [`run/forkhanded.go`](../../internal/cli/run/forkhanded.go), [`run/patchedforkline.go`](../../internal/cli/run/patchedforkline.go) | `ForkBuildRequest`; a patched fork routed to the advance before the no-pin reason, never in a jail; the delivery record ([PF-D37](patched-forks.md#PF-D37)); the fork block's line and the attach's ([PF-D43](patched-forks.md#PF-D43)) |
| [`cli/capturehost.go`](../../internal/cli/capturehost.go), [`cli/patchedfork.go`](../../internal/cli/patchedfork.go), [`entrypoint/forklauncher.go`](../../internal/entrypoint/forklauncher.go) | `yolo capture`'s patched arm; status's store state and next-check line; `patchedNotBuilt` and `PatchedForkPinReason` reworded for what now builds; the launcher's update-mode note |
| [`integration/patchedfork_test.go`](../../integration/patchedfork_test.go) | a local upstream: the first launch builds and runs the patched program, a new tag advances with the old build reaped, a conflicting tag is held with the previous build running |

Corrected in review the same day: with nothing serving, a fit that fails to build, or a walk that
stops on an apply error, sends the same advance on to the series' base ([PF-D40](patched-forks.md#PF-D40));
the build's replay into `src/` shares the walk's bound ([PF-D44](patched-forks.md#PF-D44)); an apply
error is replayed by the next check, not every launch ([PF-D45](patched-forks.md#PF-D45)); a build is
settled under its build lock, and a move's reap passes over one another advance has not settled
([PF-D46](patched-forks.md#PF-D46)); a build the bound stopped is a failed build however far its jail
got ([PF-D39](patched-forks.md#PF-D39)); and the host floor's line for a patched fork names what `yolo
host` does ([PF-D35](patched-forks.md#PF-D35)). **The trap for step 4**: a caller of `buildFork` that
records anything a waiter reads does it in `buildMode.settle`, never after `buildFork` returns.

Left for later steps: `yolo pack rebase` (step 3), which replaces `cli.rebaseSteps`; the host
floor's four recipe readers, its refresh arm, `UpdatesAllowed` and `yolo host -- <bin>`'s wait
(step 4, whose seam is `advanceOptions`: `launch` false, a host platform, a hand that writes the
floor's record); macos-user, which delivers no fork at all yet (FP-D3) and says so for a patched
fork too; and patched extensions, which reuse the advance through the owner key.

## Step 3, landed 2026-10-04

[`patched-forks.md` §14](patched-forks.md#14-what-i-would-build-in-order) step 3, `yolo pack rebase`
([PF-D26](patched-forks.md#PF-D26)), a host verb that every notch's conflict line names. What the
tree changed against the map above:

| Where | What landed |
| :--- | :--- |
| [`packsrc/rebase.go`](../../internal/packsrc/rebase.go), [`replay.go`](../../internal/packsrc/replay.go) | the rebase clone and its marker ([PF-D47](patched-forks.md#PF-D47), [PF-D48](patched-forks.md#PF-D48)): the clone in the store's regime with no template, the blobs prefetched, the replay's `git am` on a branch and the walk's pick, `git rebase --onto` only for a conflict; `ResolveRebaseTarget`, `RefKind`; `applyAtBase` takes the branch |
| [`cli/patchedrebase.go`](../../internal/cli/patchedrebase.go), [`cli/pack.go`](../../internal/cli/pack.go) | the verb and its dispatch and usage: the directory's refusals, its own clone and `--restart`, the forced check, the default target, the continue and the export for a local or a fetched fork pack ([PF-D49](patched-forks.md#PF-D49)) |
| [`cli/patchedfork.go`](../../internal/cli/patchedfork.go), [`cli/patchedadvance.go`](../../internal/cli/patchedadvance.go), [`run/patchedforkline.go`](../../internal/cli/run/patchedforkline.go) | `cli.rebaseSteps`, the by-hand rebase, replaced by `rebaseCommand` in the explicit acts' and the launch's conflict message, `yolo pack status`'s conflict line, the held suffix and the nothing-to-build line |

The trap the step found: **a clone's template is its own repository config**, which the replay's
environment cannot reach, so a clone the replay runs in takes `--template=` as the scratch repository
does. Its review found two more. **No line printed for pasting may depend on the line before it
having succeeded**: a pasted block runs every line, so the export is one `&&` line behind a test
that the rebase is finished ([PF-D49](patched-forks.md#PF-D49)). And **`REBASE_HEAD` survives a
finished rebase** (git 2.55), so neither it nor HEAD tells a finished rebase from a stopped one; the
rebased branch, which git moves only when the rebase finishes, does. Left for later steps: the host floor (step 4) and patched extensions, whose conflict lines name
the same verb through the owner key once they have one.

## Step 4, landed 2026-10-04

[`patched-forks.md` §14](patched-forks.md#14-what-i-would-build-in-order) step 4, the host floor,
and the macos-user lines. What the tree changed against the map above:

| Where | What landed |
| :--- | :--- |
| [`hostfloor/patched.go`](../../internal/hostfloor/patched.go), [`floor.go`](../../internal/hostfloor/floor.go), [`ensure.go`](../../internal/hostfloor/ensure.go), [`built.go`](../../internal/hostfloor/built.go) | the patched arm: `Floor.Patched` (the offline read) and `Floor.Advance`; the four recipe readers on the good build (`patchedServesANearMiss`, `patchedPending`, the install from the good build's entry, its record); `ensurePatched`, whose advance is the refresh arm under `UpdatesAllowed` for the fork pack and its base ([PF-D50](patched-forks.md#PF-D50), [PF-D52](patched-forks.md#PF-D52)); the copy checked whole ([PF-D51](patched-forks.md#PF-D51)); a Mac's no-copy line names a jail ([PF-D54](patched-forks.md#PF-D54)) |
| [`cli/hostfloor.go`](../../internal/cli/hostfloor.go), [`cli/patchedadvance.go`](../../internal/cli/patchedadvance.go) | `floorAdvance` (the fresh launch's advance as a launch, at the host: `advanceOptions.host`) and `floorPatchedState`; `recoverGoodBuild`, which writes nothing; the fork's line at `yolo host -- <bin>` ([PF-D53](patched-forks.md#PF-D53)); `hostFloorPatchedReason` retired |
| [`packdecl`](../../internal/packdecl/packdecl.go), [`packload/forks.go`](../../internal/packload/forks.go), [`packsrc/addr.go`](../../internal/packsrc/addr.go), [`capture/store.go`](../../internal/capture/store.go) | `Install.ForkRoot`, set by the fork rewrite; `packsrc.BuildSource`, which `cli.patchedBuildSource` now is; `capture.Entry.Complete` |
| [`run/forkbuild.go`](../../internal/cli/run/forkbuild.go), [`run/run.go`](../../internal/cli/run/run.go), [`macosuser/runplan.go`](../../internal/macosuser/runplan.go) | macos-user: the warning names a patched fork and both container backends, the sandbox is handed each fork's reason through `YOLO_FORK_BUILDS`, and the fork block's edited-series clause names a container backend's launch ([PF-D54](patched-forks.md#PF-D54)) |

The traps above held as written for programs. The host-render gate's trap, written for patched
extensions, is met by placement: the floor's install runs in `resolveHostLaunchTarget`, after
`hostApplyGate` returns, and only for the program being launched (`TestTheHostFloorsAdvanceRunsAfterTheRenderGatesObservePass`).
Patched extensions still need the advance before the gate, because their advance changes the render.
The plan's seam for this step ("`launch` false, a host platform, a hand that writes the floor's
record") was taken as `launch` true and no hand ([PF-D50](patched-forks.md#PF-D50),
[PF-D51](patched-forks.md#PF-D51)).

Corrected in review the same day: the advance decided what serves from the capture store alone,
so the floor's own installed copy counted for nothing once the good build's store entry was gone,
and the fork's line read the check record rather than what the floor runs. The floor now hands its
advance the installed copy that serves ([PF-D55](patched-forks.md#PF-D55)), an install whose good
build has no store entry stops on `yolo capture <bin>` instead of starting, and the line names the
floor's build ([PF-D53](patched-forks.md#PF-D53)).

Left: macos-user delivery, [OQ-PFK5](patched-forks.md#OQ-PFK5), which waits on
[`install-capture.md`'s hand-off H4](../plans/install-capture.md#hand-offs--what-is-not-wired-and-the-exact-line-that-wires-it);
and a hardware run of a host floor install, since every floor test here stands in for the build
jail.

## Patched extensions, steps 2 to 5, landed 2026-10-04

[`patched-extensions.md` §16](patched-extensions.md#16-what-i-would-build-in-order) steps 2 to 5, over
the shared advance. What the tree changed against the [patched-extension map](#patched-extensions):

| Where | What landed |
| :--- | :--- |
| [`packdecl/patchedext.go`](../../internal/packdecl/patchedext.go), [`fork.go`](../../internal/packdecl/fork.go), [`contributes.go`](../../internal/packdecl/contributes.go) | the declaration, its refusals and the owner-key uniqueness; the placement refusal lets the fork fields onto `files` beside `patches` only; `TreeRecipe` |
| [`packload/patchedtrees.go`](../../internal/packload/patchedtrees.go), [`forks.go`](../../internal/packload/forks.go), [`footprint.go`](../../internal/packload/footprint.go) | a patched extension is a `packload.Fork` with `Into` (and `Owner`, `OwnerForks`, where its entry reaches); `PatchedTrees`, `HoldPacks`, `Label`, `LintPatchedTrees`; the review-marked claim |
| [`cli/forkbuild.go`](../../internal/cli/forkbuild.go), [`forkbuildchild.go`](../../internal/cli/forkbuildchild.go), [`patchedadvance.go`](../../internal/cli/patchedadvance.go) | the tree's recipe, jail argv, seal and admit; the child's `--tree`; the advance's lines name the extension, and its move reaps without reading delivery records |
| [`capture/treecopy.go`](../../internal/capture/treecopy.go) | `CopyTree`: one subtree of an entry, reflink or copy, never a hardlink |
| [`cli/treedelivery.go`](../../internal/cli/treedelivery.go), [`run/patchedtrees.go`](../../internal/cli/run/patchedtrees.go), [`run/packfiles.go`](../../internal/cli/run/packfiles.go) | the tree arm in the fork slot, the per-launch copy and its marker check, the mount from the copy, `YOLO_PATCHED_TREES`, the launch block, the attach line, macos-user's line |
| [`entrypoint/patchedtrees.go`](../../internal/entrypoint/patchedtrees.go), [`shims.go`](../../internal/entrypoint/shims.go), [`forklauncher.go`](../../internal/entrypoint/forklauncher.go) | the owner's launchers' gate |
| [`cli/hosttrees.go`](../../internal/cli/hosttrees.go), [`applyhostfiles.go`](../../internal/cli/applyhostfiles.go), [`host.go`](../../internal/cli/host.go), [`hostapply.go`](../../internal/cli/hostapply.go) | the host render, the advance before it, and the host stop |
| [`cli/capturehost.go`](../../internal/cli/capturehost.go), [`forkpin.go`](../../internal/cli/forkpin.go), [`patchedfork.go`](../../internal/cli/patchedfork.go) | the explicit acts on an extension key |

Run once by hand, 2026-10-04, in a nested jail from a throwaway workspace with a scratch `HOME`
(`env -u YOLO_VERSION`, `YOLO_NO_AUTO_IMAGE_REAP=1`, `YOLO_REPO_ROOT` at the stage's worktree), with a
local upstream of two files, a one-patch series and an agent pack owning a `packages` list: the first
launch replayed the series, built the tree in a real sealed build jail (its admit passing on the real
capture manifest, 6 paths), and the jail read the patched file at `~/<into>`, found it read-only,
and was handed `YOLO_PATCHED_TREES` with the build and an empty gate; a second launch inside the
hour ran no git and mounted the same build; and after the build line was edited to `false`, the
failed build left nothing mounted, the earlier mountpoint retired, and the agent's launcher carrying
the stop with the host's reason.

Corrected in review the same day: a build line ending in a `# comment` no longer comments out the
subshell's close and the final copy, the line sitting on lines of its own; a patched extension's claim
is disclosed at launch ([PPX-D29](patched-extensions.md#PPX-D29)); its build jail no longer reports
the contributing pack's own list entry as ownerless ([PPX-D30](patched-extensions.md#PPX-D30)); a
revert removes the host's link and copies ([PPX-D31](patched-extensions.md#PPX-D31)); `yolo host --
<bin>` names the build its link names, is silent under `host_management: none`, and on a macOS host
says the agent starts without the tree ([PPX-D25](patched-extensions.md#PPX-D25),
[PPX-D26](patched-extensions.md#PPX-D26)); a host advance's wait and interrupt lines name the host;
an attach reads the delivery record once and says an unreadable one once, with its next step; and
`yolo pack status` heads its section for what it lists, which the commit before said it did and did
not. Five launch call sites, four host ones and the native launcher's gate gained the tests that fail
with each deleted.

Left: [PPX-D16](patched-extensions.md#PPX-D16)'s series lint, which needs the upstream's `.gitignore`
at the base from the replay's scratch repository; step 6, migrating the five and a nested-jail run in
which a real pi loads a built tree (a human's check, AGENTS.md's no-agent rule); and an integration
test of a tree through a real build jail, which this stage could not run (parallel stages share the
session image).

## Integration, landed 2026-10-05

Steps 3 and 4 and the patched extensions were built in parallel from step 2 and merged in that
order. Step 4's ledger rows, which took the same free ids as step 3's, moved up by three (PF-D50 to
PF-D55). The advance's lines kept one set of words for the three places it runs: a jail launch,
the host floor's install of a patched fork (`yolo host`, and its next `yolo host -- <bin>`), and
the host's render of a patched extension (the host, and its next `yolo host apply --assert`).

Integrating found two defects that no single branch could, and fixed both:

- **`yolo pack rebase` refused a patched extension's key**, which every extension's conflict line
  names: step 3 read only the forks of programs ([PPX-D32](patched-extensions.md#PPX-D32)).
- **`yolo pack update` built and moved the good build** on a Linux host, through the `yolo host
  apply --assert` it runs, which both step 4 and the extensions had taught to advance. The patched
  fork integration test caught it: its next launch disclosed no move
  ([PF-D56](patched-forks.md#PF-D56)).

[`integration/patchedextension_test.go`](../../integration/patchedextension_test.go) is the
extensions' container-level cell, the one their build could not run: a tree built in a real sealed
jail, mounted read-only where the agent's list names it, handed to the jail, disclosed, then held
at a conflict whose printed `yolo pack rebase` stops at it.

**The trap the maintainer's own series found:** a fork whose history has merges does not export as
one `git format-patch` range at its merge base. Where it merged upstream releases (his pi fork),
`format-patch` refuses, its base not being an ancestor of every commit in the range; where it merged
two of its own branches (his `pi-dynamic-workflows`), the series does not apply at its base. The
migration kit exports such a fork as its first-parent chain, each merge one member, and for the pi
fork the integration merge's whole diff against the upstream release as the first; both then
apply at their base and reproduce the fork's tree.

## Measurements to make

- The maintainer's own pi series against the newest upstream version: the replay's time on the
  host, and the build time the design quotes from a stand-in and one host log.
- The prefetch of one pi commit into a blobless mirror of the GitHub remote, which the store measured
  at 4.9 s for a commit of 2,992 files ([`store.go:756-768`](../../internal/packsrc/store.go#L756-L768));
  the design's blobless measurement used a `file://` remote. MEASURED 2026-10-05, in part: the first
  `yolo pack update` of the maintainer's pi series against a cold blobless mirror of
  `github.com/earendil-works/pi` stopped at the replay's 60 s bound, an apply error; the next,
  with the mirror warm, replayed three versions in about 7 s for the whole command. A first launch
  that meets the cold case builds the series' base ([PF-D45](patched-forks.md#PF-D45)), which for
  this series is also its newest fit.
