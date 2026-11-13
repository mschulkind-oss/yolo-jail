---
status: accepted
stage: CURRENT
verified: 2026-10-16
verified_commit: 8e65f3444
covers:
  - internal/packdecl/patchedext.go
  - internal/packload/patchedtrees.go
  - internal/packload/duplicateloads.go
  - internal/packload/treenotch.go
  - internal/capture/treecopy.go
  - internal/cli/treedelivery.go
  - internal/cli/treepool.go
  - internal/cli/hosttrees.go
  - internal/cli/hostrevert.go
  - internal/cli/run/patchedtrees.go
  - internal/cli/run/treenotch.go
  - internal/cli/run/missingbuilds.go
  - internal/cli/run/skipextensions.go
  - internal/packload/treefallback.go
  - internal/cli/run/seal.go
  - internal/entrypoint/patchedtrees.go
  - internal/entrypoint/sealedbuild.go
  - internal/entrypoint/bootrefusal.go
  - internal/entrypoint/buildcause.go
  - internal/entrypoint/prelaunchrefresh.go
  - internal/entrypoint/sharedlink.go
  - internal/hostfloor/prelaunch.go
  - packs/pi/pack.json
tags: [packs, files, pi, extensions, forks, evergreen, git, refresh, launchers]
summary: "How yolo delivers pi extensions. A patched extension is a `files` contribution naming an upstream `source` and a patch series in place of `from`: the patched-fork ratchet builds it in the sealed capture jail, and each fresh launch mounts a read-only copy of this machine's good build where pi loads it as a local package it never updates. The pre-launch refresh runs pi's own `pi update --extensions` before pi starts, throttled hourly, bounded, and serialized by a non-blocking lock."
---

# Pi extensions — patched trees and the pre-launch refresh

**Status:** Verified 2026-10-16 against `8e65f3444`. MEASURED 2026-10-04 in fresh blobless clones
(git 2.55.0, npm 11.17.0, node v24.19.0): the maintainer's five extension forks replayed onto their
upstreams, the newest-fit walk over each, two trees built, and one fork's `dist/` rebuilt from a
series without it; and an extension run end to end through a real build jail in the integration
suite. UNMEASURED: no pi has loaded a tree yolo built ([§16](#16-unbuilt-and-unmeasured)); the
pre-launch refresh is unit-tested only, never observed in a real jail ([§17](#17-the-pre-launch-refresh)).
No question is open. The recovery hatches for an extension
([§9.1](#91-recovery-hatches-for-an-extension)) were verified 2026-10-16 against `15c739590`, from
the code and its unit tests.

yolo delivers a pi extension two ways. A **patched extension** follows an upstream with a patch
series of the user's own: the patched-fork mode of [`patched-forks.md`](patched-forks.md) checks the
upstream, replays the series and builds it in the sealed capture jail, and the build leaves a tree
instead of a program. Each fresh launch mounts a read-only copy of this machine's good build, and pi
loads it as a local package, so pi never installs or updates it. Every other extension pi installs
itself, and the **pre-launch refresh** ([§17](#17-the-pre-launch-refresh)) runs `pi update
--extensions` before pi starts so those stay current.

**Reads with:** [`patched-forks.md`](patched-forks.md) (the mode this extends; every PF term and
PF-D id cited here is its), [`pi-extension-store-builds.md`](../design/pi-extension-store-builds.md)
(the held store: pi git extensions as immutable local trees, an unmodified extension built by this
mode with an empty series, and the parallel build pool — its XB-D ids), [`pack-pi-resources.md`](pack-system.md)
(the other route that hands pi a local package), and the user guide's
[patch a pi extension](../../userguide/guides/patch-series.md#patch-a-pi-extension).

| Component | Lives in |
| :--- | :--- |
| Declaration, the extension key, the reserved build directory | `internal/packdecl` (`IsPatchedExtension`, `patchedExtensionProblems`, `TreeRecipe`, `TreeReservedRoot`) |
| The trees a selection declares; the owning agent pack; where each is delivered; the lints | `internal/packload` (`PatchedTrees`, `owningAgentPack`, `Fork.DeliveredInJail`, `LintPatchedTrees`, `LintDuplicateLoads`) |
| The tree build act and its admit | `internal/cli` (`treeBuildJailArgv`, `treeAdmitProblem`, `sealPacks`, `deliverTree`, `treepool.go`) |
| The per-launch copy | `internal/capture` (`CopyTree`), `internal/cli/run` (`patchedtrees.go`, `PatchedCopySlug`) |
| The macos-user per-build copy | `internal/cli` (`treecopyreuse.go`), `internal/paths` (`TreeCopyRoot`) |
| A launch's refusal when a needed tree has no build | `internal/cli/run` (`refuseMissingBuilds`, `missingbuilds.go`) |
| The jail's view and the launchers' gate | `internal/entrypoint` (`PatchedTreesEnv`, `treeGateFor`, `TreeBuildEnv`, `SealedBuildEnv`, `BootRefusal`, `BuildCause`) |
| The host render: versioned copies and an owned link | `internal/cli` (`hosttrees.go`, `renderHostTree`, `advanceHostTrees`, `linkHostLaunchTrees`, `revertHostTreeLinks`) |
| The pre-launch refresh | `internal/packdecl` (`Refresh`, in `packdecl.go`), `internal/entrypoint` (`prelaunchrefresh.go`), `packs/pi` |

---

## 1. Defined terms

Coined for this mode unless a link says otherwise:

- **Patched extension**: a `files` contribution that declares `source` and `patches` (or
  `patch_branch`) in place of `from`. Its bytes are the upstream at a commit with the series
  replayed, built in the sealed capture jail. The mechanism delivers any home-relative tree;
  "extension" names the case that motivated it. It is not a patched fork (no bin, nothing on PATH, no
  base program) and not a pi `git:` package (pi never fetches, installs or updates it). A `source`
  with no series is an unmodified extension built the same way with an empty series
  ([XB-D35](../design/pi-extension-store-builds.md#XB-D35)).
- **Built tree**: the directory a patched extension's build leaves and the capture store admits. Not
  PF's [patched tree](patched-forks.md#53-the-patched-tree), the git tree object recorded before the
  build, and not [the held store](../design/pi-extension-store-builds.md#the-held-store), which a
  jail builds into a store every jail can write.
- **Extension key**: `<pack>/<name>`, where `<pack>` is the contributing pack and `<name>` the last
  segment of `into`. It is PF's fork key `<pack>/<bin>` for a tree.
- **Owner key** *(coined in [PF-D22](patched-forks.md#PF-D22))*: the key the check, its record and
  lock, the replay, the ratchet and the explicit acts use: the fork key for a patched fork, the
  extension key for a patched extension.
- **Owning agent pack**: the selected pack that declares the surface of the list entry naming the
  tree — the `config-list` or posture-list entry, in the contributing pack, naming `~/<into>` or a
  path inside it ([PPX-D4](#PPX-D4), [PPX-D36](#PPX-D36)). `pi/settings` is the pi pack's surface,
  so in the maintainer's case `matt` contributes and `pi` owns. With no such entry there is none,
  and pi never loads the tree. It is not read from where the tree lands: a `state` declaration says
  which pack keeps a directory, not which agent loads a tree in it.
- **Newest fit** *(coined in [`patched-forks.md` §6.4](patched-forks.md#64-the-newest-fit-and-the-first-advance))*:
  the first entry of the walk's list onto which the series replays cleanly. Not the newest version.

Every other PF term keeps [`patched-forks.md`](patched-forks.md)'s meaning: series, series digest,
follow rule, check, check stamp, check record, candidate, pending, good build, advance, replay, apply
error, held.

## 2. Principles

A patched extension is a placement of the patched-fork mode on `files`, sharing one implementation
through the owner key: detection, the replay, the ratchet, the record and the explicit acts are
patched forks'. Three things are its own: the build leaves a tree, a launch serves a copy of it, and
pi learns of it from a list entry. The maintainer's request, 2026-10-04: *"I want the fork patch
thing to cover pi extensions as well."* [P1 to P5](patched-forks.md#1-principles) carry over, and
three more:

- **PE6. A launch serves a copy, never the store.** The capture store's reclaimer holds that
  reclaiming is never a correctness event, because nothing runs from an entry itself. Binding an
  entry into a running jail would end that: removing the entry would empty the directory the jail
  reads.
- **PE7. Append, never rewrite.** pi learns of a built tree only through an ordinary list entry its
  author writes. yolo rewrites nobody's entry, which [`OQ-LT2`](pack-system.md#oq-lt2) forbids
  after the merge.
- **PE8. Holding the agent holds its extensions.** `agent_updates` off for `pi` turns off the
  pre-launch refresh, so a frozen pi has frozen extensions; a patched extension keeps that
  ([PPX-D9](#PPX-D9)).

## 3. How pi takes an extension

### 3.1 How pi takes an extension, and where yolo can put a tree

Read from pi's `dist/core/package-manager.js` (0.99.1), and the tree at the verification commit:

- **A `git:` entry** is cloned into `~/.pi/agent/git/<host>/<path>` and its dependencies installed
  by npm with install scripts allowed. `pi update --extensions` resets and cleans any checkout whose
  HEAD is not its ref's commit, so a local commit there is always erased.
- **A local path** is loaded in place, never installed; a path that does not exist is skipped with
  no message; update collects only npm and git entries. A local package loads its `package.json`
  `pi` manifest, else its conventional `extensions/`, `skills/`, `prompts/`, `themes/`
  ([`pack-pi-resources.md`](pack-system.md)).
- **Identity** is `npm:<name>`, `git:<host>/<path>` or `local:<resolved path>`, so one extension as
  a `git:` and a local entry loads twice.
- **pi serves its own packages and `typebox`** to every extension, so a tree carries only its other
  dependencies.
- **A `files` contribution** mounts a staged tree `:ro` at `/home/agent/<into>`, and a `files`
  destination on the jail's PATH is refused for every pack.
- **The capture store** is mounted `:ro` at `/ctx/captures`, and not at all below Apple Container's
  read-only floor.

Four consequences follow. **Patched bytes cannot live in pi's git checkout**, which the refresh
resets and cleans. **A built tree must arrive complete**, dependencies and build output included,
because a local path is the one form pi neither installs nor updates. **A tree mounted where pi
looks needs no store mount**, so it reaches backends the fork route cannot. **A sealed build of an
extension needs no agent pack for its toolchain**, since Node and npm are the image's.

### 3.2 The maintainer's five forks

The mode was built for the maintainer's hand-rebased forks of five pi extensions, three of which no
longer replayed onto their upstream. MEASURED 2026-10-04: for all five, the base with the series
applied is byte-for-byte the fork's tree; for the three that conflict, the newest fit is the base;
one upstream has no tags at all, so it needs `follow: "head"`; one's newest version predates its
series' base, so its walk's list is empty and it builds the base; one carries 17 of 22 members that
add a `dist/` its upstream ignores, which its `build` regenerates byte for byte from a series
without them. The longest walk, six replays from a fresh blobless clone, took about two seconds.

## 4. The declaration

```jsonc
{ "kind": "files",
  "into": ".pi/agent/yolo-patched/pi-subagents",
  "source": "git+https://github.com/<upstream>/pi-subagents?ref=main",
  "patches": "patches/pi-subagents",
  "build": "npm install --omit=dev --legacy-peer-deps --ignore-scripts" },
{ "kind": "files",
  "into": ".pi/agent/yolo-patched/pi-dynamic-workflows",
  "source": "git+https://github.com/<upstream>/pi-dynamic-workflows?ref=main",
  "patches": "patches/pi-dynamic-workflows",
  "build": "npm ci --ignore-scripts && npm run build && npm prune --omit=dev --ignore-scripts",
  "produces": ["dist/pi-extension.js"] },
// and in the pack's existing config-list on pi/settings at /packages, in the same edit:
//  - "git:github.com/<maintainer>/pi-subagents"
//  + "~/.pi/agent/yolo-patched/pi-subagents"
```

| Field | Rule |
| :--- | :--- |
| `into` | Required, home-relative, never on the jail's PATH ([PPX-D3](#PPX-D3)): something built and placed on PATH is a `program`, and a patched fork delivers one. `.pi/agent/yolo-patched/` is the examples' choice; core reserves no directory |
| `source`, `follow` | As for a patched fork ([PF-D1](patched-forks.md#PF-D1), [PF-D3](patched-forks.md#PF-D3), [PF-D4](patched-forks.md#PF-D4)): the upstream, a mandatory `?ref=`, and a branch followed by `release`, `release:<prefix>` or `head` |
| `patches`, `patch_branch` | The series rules of [PF-D2](patched-forks.md#PF-D2), or a [patch branch](patched-forks.md#35-a-patch-branch-instead-of-a-series); never both |
| `build` | Optional: one command line run by bash in the checkout in the sealed jail. Without it no command runs, but the tree still passes through the sealed jail and the admit, so every built tree has one admit path |
| `produces` | Optional: tree-relative paths the build must leave |
| `from`, `fork_of`, `agent`, `agents` | Refused beside `source`, each naming why ([PPX-D1](#PPX-D1)) |

On `files`, `build` and `produces` are accepted only beside a `source`. The extension key is unique
per pack across its patched extensions and its patched programs' bins ([PPX-D2](#PPX-D2)). Two packs
landing a tree at one `into` are a collision `yolo pack footprint` reports, as for any two `files`
claims on one path. Validation is static; the series' contents are read at each advance.

> [!WARNING]
> **Land a tree outside every directory the agent discovers by itself.** For pi that means outside
> `~/.pi/agent/extensions/`. There, pi's discovery loads the extension whether or not a list entry
> names it, so the entry stops being the one switch: with no entry pi loads the extension without
> its skills and prompts, and after the entry is removed discovery keeps loading it (MEASURED with
> pi 0.99.1's resolver). Core knows no agent's paths, so this is guidance, not a check.

## 5. What carries over from patched forks

| PF decision | For a patched extension |
| :--- | :--- |
| [PF-D1](patched-forks.md#PF-D1) | Replaced by [PPX-D1](#PPX-D1): the placement is `files`, not `program` |
| [PF-D2](patched-forks.md#PF-D2), [D3](patched-forks.md#PF-D3), [D4](patched-forks.md#PF-D4) | Unchanged: the series, the ref and `follow`, what a version is |
| [PF-D5](patched-forks.md#PF-D5) | Generalized: the triggers are [§6.1](#61-where-the-check-runs)'s |
| [PF-D6](patched-forks.md#PF-D6), [D18](patched-forks.md#PF-D18) | Unchanged: the replay, its config regime, the blob prefetch |
| [PF-D7](patched-forks.md#PF-D7) | Generalized: the recipe and the selection are [§7.2](#72-identity-and-selection)'s |
| [PF-D8](patched-forks.md#PF-D8) | Unchanged: the ratchet, its compare-and-swap and its disclosure line ([OQ-PPX1](#why-its-this-way)); a newer build that fails stops the act, as for a fork ([OQ-PPX3](#OQ-PPX3)) |
| [PF-D9](patched-forks.md#PF-D9), [PF-D10](patched-forks.md#PF-D10) | Unchanged, keyed by the extension key; every advance takes the newest fit |
| [PF-D12](patched-forks.md#PF-D12) | Generalized: `yolo capture <pack>/<name>` builds; `yolo pack update`, `install` and `status` act on the extension key ([PPX-D28](#PPX-D28)) |
| [PF-D16](patched-forks.md#PF-D16) | Its principle holds (the good build is machine-local); nothing is pinned in `packs.lock.json` ([PPX-D19](#PPX-D19)) |
| [PF-D19](patched-forks.md#PF-D19) | Generalized: a hold comes from the contributing pack or the owning agent pack ([PPX-D9](#PPX-D9)) |
| [PF-D20](patched-forks.md#PF-D20) | Simplified: a move reaps every other build of the key at once, because jails hold copies ([§8.1](#81-in-a-jail)) |
| [PF-D81](patched-forks.md#PF-D81) to [PF-D85](patched-forks.md#PF-D85) | Unchanged: a patch failure stops the operation by default, its error block prints the rebase recipe, and the same bypasses apply |
| [PF-D14](patched-forks.md#PF-D14) | Not applicable: the host floor installs programs; the host delivery is [§8.3](#83-at-the-host) |

## 6. Detection, and the newest-fit walk

### 6.1 Where the check runs

The check is [PF-D5](patched-forks.md#PF-D5)'s, per extension key, with its throttle and its fetch.
It runs at:

- **a fresh jail launch**, in the fork-build slot beside the fork builds, below every attach site and
  under the launch lock, every key in one pool ([PPX-D13](#PPX-D13));
- **the host's `files` render**: `yolo host apply --assert`, and `yolo host -- <bin>` under
  `host_apply_on_launch` when `<bin>` is a program of the owning agent pack or of a fork of it, before
  the render gate compares in observe posture ([§8.3](#83-at-the-host)). So `yolo host -- claude`
  runs no check for a pi extension;
- **the explicit acts**, which force it: `yolo pack update`, `yolo pack install`, and
  `yolo capture <pack>/<name>` ([PPX-D28](#PPX-D28)).

It never runs on an attach, a dry run, in a jail, in a capture or build jail, or under a hold with a
good build. Checks of different keys run concurrently, except where two name one upstream repository
(two `//subdir` extensions of a monorepo, or an extension and a patched fork of one repository),
which serialize on its mirror lock. Their lines print in declaration order.

### 6.2 The newest-fit walk

The walk is [`patched-forks.md` §6.4](patched-forks.md#64-the-newest-fit-and-the-first-advance)'s,
and its one normative statement is there. In brief: an advance replays the series down a list,
newest first, and takes the first entry it fits; tags must contain the series' base, so an older
version cannot pass for an update; an empty list leaves nothing pending and shows no held suffix.

## 7. The build and the admit

### 7.1 The build act

It is a fork's build act ([`patched-forks.md` §7](patched-forks.md#7-the-build-and-the-launch)) with
two changes ([PPX-D5](#PPX-D5), [PPX-D20](#PPX-D20)):

- **The seal is narrowed to the contributing pack**, where a fork's is narrowed to the fork and its
  base ([FP-D9](../design/forked-programs-as-packs.md#FP-D9)), and carries the base of every fork
  that pack declares, and in turn every base those bases fork ([PPX-D39](#PPX-D39)). The launch gates
  that ask whether another selected pack provides what one names do not run there, since the
  narrowing is what dropped that pack, and its boot renders no pack-declared surface and runs no pack
  hook, since it runs no agent ([PPX-D41](#PPX-D41)). The toolchain is the image's `/bin/node` and
  `/bin/npm`, recorded beside the build; a build that needs another says so in its own `build` line.
- **One fixed final step.** After `build`, if any, the jail copies the checkout, links kept as links,
  into a reserved directory under `~/.local`, one of the surfaces the capture driver already
  captures, so the driver is unchanged. The build line runs in a subshell with the image's tools
  first on PATH, so a `cd` or a trailing comment in it cannot move the copy's source.

The checkout is the patched fork's replay ([PF §5.1](patched-forks.md#51-where-it-runs)). The wait
for another build of the same key and its bound are [PF-D17](patched-forks.md#PF-D17)'s, and one
Ctrl-C ends the act's whole wait ([PF-D57](patched-forks.md#PF-D57)). A tree's build jail is told
which extension it builds (`YOLO_TREE_BUILD`, [PPX-D30](#PPX-D30)).

**On macos-user, and at a Mac's host, the build is darwin's** ([PPX-D45](#PPX-D45)): the act is the
sealed macos-user build act a patched fork's darwin build runs in
([`patched-forks.md` PF-D97](patched-forks.md#PF-D97),
[FP-D24](../design/forked-programs-as-packs.md#FP-D24)), as the sandbox account under the sealed
Seatbelt profile, in a staging tree keyed by the build. Its script is the tree's: the toolchain record
(the yolo, the darwin floor, the macOS release, and the floor's own `node` and `npm`), the optional
build line in a subshell in the checkout, then the same fixed copy into the reserved directory, with
macOS's `/bin/cp`. The whole script runs under the platform's own base system, macOS's `/bin/bash`
3.2 and its BSD tools, so a build line must work on the Mac it builds for ([PPX-D46](#PPX-D46)); a
fork's build does too. Its bootstrap is told on its argv that it is a sealed build's, so, as in a
container build jail, the staging home gets no agent's config and the build prints no notice about
one ([PPX-D48](#PPX-D48)). When the build line exits, whatever its result, the act ends its whole process group, so
nothing it started outlives the build ([PPX-D49](#PPX-D49)). A failed build says whether its build line
failed, with the line's own exit status, or the capture after a line that succeeded; its start line says
the build runs on this Mac's own network ([PPX-D50](#PPX-D50)). The admit is the same, its link check reading that staging tree. The good build is
per platform, so a Mac that launches on podman and on macos-user builds one commit once for each.

**The admit** keeps the empty-delta refusal and the link check, and adds three ([PPX-D21](#PPX-D21)):

- **Every `produces` path exists** in the tree.
- **Nothing in the delta lies outside the reserved directory.** Strays are named and the build fails,
  since the tree would not carry them. A tool that keeps a store under a captured surface is pointed
  at the workspace by the build line.
- **The full content scan finds no reference to the build jail's home.** The tree lands at another
  path in every jail and at the host, so a tree with one is a failed build naming the files. The
  program route's relocation cannot rescue it: relocation rewrites one home prefix into another, and
  a tree also moves within the home. A scan that did not run counts as a reference.

Each failure is a failed build with [PF-D9](patched-forks.md#PF-D9)'s back-off. While a good build
is on this machine it stops the launch, which runs that build only under a recovery hatch or an
`agent_updates` hold ([OQ-PPX3](#OQ-PPX3)). So does a newer build one of yolo's own steps keeps from
running: its build jail not starting, a lock wait running out, an apply error
([PD-NF1](../design/program-delivery.md#PD-NF1), [`patched-forks.md` PF-D91](patched-forks.md#PF-D91)).

### 7.2 Identity and selection

- **The revision is the upstream commit**, as [PF-D7](patched-forks.md#PF-D7) has it.
- **The recipe is `["tree", build, sorted produces, subdir, series digest]`** ([PPX-D6](#PPX-D6)). The
  `"tree"` tag means a tree's recipe can never equal a program's.
- **The `build` receipt** records the extension's name as its `bin` and the extension key as its
  `fork`, so selection and `yolo prune` file it under (name, platform, extension key) with no new
  field ([PPX-D21](#PPX-D21)).
- **Selection is by extension key and platform, then an exact lookup** of repository, subdirectory,
  commit and recipe. `yolo prune` keeps the newest build per selection key; reaping one never touches
  a running jail's tree ([§8.1](#81-in-a-jail)).

## 8. Delivery

### 8.1 In a jail

1. **At the fork-build slot, after any advance, the good build is materialized** into a per-launch
   directory beside the launch's pack tree: reflink, else copy, never a hardlink ([PPX-D7](#PPX-D7),
   [PPX-D22](#PPX-D22)). A hardlink would share the store's inode, and a jail that can write its
   mount (below Apple Container's read-only floor) could then change the store. Never inside a pack's
   staged directory, whose content digest an attach compares, and never at the tree's top level,
   which the boot's fallback walk reads as packs. It goes with the tree, once the container is known
   gone.
2. **The `files` emitter mounts that copy at `/home/agent/<into>:ro`.** No store is mounted for it.
3. **The jail learns what it got** from `YOLO_PATCHED_TREES`, read once at boot: per extension key,
   `into`, the handed build or the reason there is none, the owning agent pack, whether to stop, and
   the build's cause ([PPX-D8](#PPX-D8), [PPX-D23](#PPX-D23)). It is a sibling of the bin-keyed
   `YOLO_FORK_BUILDS`, not an overload of it.
4. **An attach names the build its jail was handed**, from the delivery record each fresh launch
   leaves beside its tree, and says when a newer good build waits for the next fresh launch.

**On macos-user there is no mount** ([PPX-D45](#PPX-D45)): the copy is laid into the
launch's home overlay at `~/<into>`, the same overlay that carries every other `files` tree there. It
is staged host-owned and read-only under `/Users/Shared/yolo-staged`, the sandbox's bootstrap installs
it into the sandbox home, and the session's Seatbelt profile denies writes to it, which is this
backend's `:ro`. `YOLO_PATCHED_TREES` is relayed to the bootstrap, so the boot reads the same
decisions a container's does.

**The macos-user copy is made once per build, not once per launch** ([PPX-D51](#PPX-D51)). The
overlay reads it once and never mounts it, so it lives in the workspace's
`AGENTS_DIR/<cname>/tree-copies`, keyed by its store entry, and a later launch handed the same build
gets the same copy with nothing copied. Each launch that hands a copy holds a lease on it for the rest
of its process, and the next launch to settle a copy of that extension removes the copies of other
builds that no launch holds.

A move reaps every other build of the key at once: no jail reads the store for a tree, so no running
jail's copy can be stranded. A copy can still be cut short by another workspace's launch moving the
good build, and a tree has no installer to fall through to, so **a copy is checked against its
entry's completion marker once it ends**. A reap removes the marker before any of the tree, so a
marker still there means the copy read a whole tree; a marker gone means the copy may be partial even
if it reported no error, and it is removed and the record re-read once.

### 8.2 How pi finds it

- **The author writes `~/<into>` in pi's `packages` list**, or a path inside it, such as one package
  of a monorepo's tree ([PPX-D36](#PPX-D36)), and drops the extension's `git:` entry in the same
  edit. pi loads it as a local package: never installed, never updated, untouched by the pre-launch
  refresh, with the package's skills and prompts ([PPX-D10](#PPX-D10)).
- **A list entry loads a tree** when, cleaned as a path, it is `~/<into>` or lies inside it:
  `~/<into>/` and `~/<into>/packages/session-name` count, `~/<into>-other` and `~/<into>/../x` do
  not. Core reads none of pi's grammar beyond the path.
- **The pi pack's subagents MCP render still fires**: its pattern matches `~/.pi/agent/yolo-patched/pi-subagents`.
- **One lint**, a warning at `yolo pack lint` and a line in every launch's "Patched extensions this
  launch" block ([PPX-D27](#PPX-D27)): no `config-list` or posture-list entry in the contributing pack
  loads the tree, so it is mounted and pi never loads it.
- **A lint for the old entry left beside the new one** ([PPX-D34](#PPX-D34)), at `yolo pack lint`
  only: one list holding an entry that loads the tree and a remote entry (`git:`, `npm:`, or a URL)
  of the same final name, which loads the extension twice. The final name matches the extension's
  own name or its upstream's. An old entry under another name needs pi's whole package-source
  grammar, which core does not read ([§14](#14-alternatives-and-what-this-does-not-cover)).

### 8.3 At the host

On Linux, the readiness act is the `files` render, and a patched extension gets an arm of its own
([PPX-D11](#PPX-D11), [PPX-D25](#PPX-D25)), since the plain render copies file by file and skips a
contribution with no `from`:

- **The check and any advance run first**, at `yolo host apply --assert`, and at `yolo host -- <bin>`
  only when `<bin>` is a program of the owning agent pack or of a fork of it, before the render gate
  compares in observe posture, so the comparison sees the build the apply would install. Without that
  scope, `yolo host -- claude` would wait on a pi extension's fetch and build.
- **They run outside that comparison, never inside it**: the comparison is the apply with its writes
  withheld, bounded tightly as a stuck-detector, and a check alone may wait on the network for far
  longer. Inside it, and at every other bin's launch, the patched arm only reads which build the link
  names.
- **A launch links the tree it built even when the comparison overruns** ([PPX-D44](#PPX-D44)): the
  check and build before the gate made the tree for this very launch, and the stop after the gate
  refuses without it, so a gate that renders nothing still links the launch's own trees to the good
  build that serves each, taking nothing the render would not.
- **The good build is materialized into a host-private versioned directory**, one per build key, in
  yolo's state directory where no jail mounts it, checked against the completion marker and renamed in.
- **`~/<into>` is a symbolic link the render owns**, recorded in the `files` ownership record and
  swapped by writing a new link beside it and renaming it over.
- **The previous version is kept until the next move**, so a host pi already running keeps reading
  the tree it loaded. INFERRED from pi's loader resolving a loaded file to its real path; UNMEASURED
  with pi.
- **A revert removes the link** the ownership record names, and the versioned directories with it
  ([PPX-D31](#PPX-D31)). A plain `files` tree's output is not withdrawn by a revert.
- **The host apply `yolo pack update` runs builds nothing**: it links the good build already here,
  and an extension with none says `yolo host apply --assert` builds it ([PPX-D33](#PPX-D33)).
- **`yolo host -- <bin>` of an owning agent stops before exec** when a tree its entry reaches the host
  with names no directory at `~/<into>`, reading the link rather than the record, since what the
  agent loads is what the link names ([PPX-D26](#PPX-D26)).

A `pi` started outside yolo at the host finds whatever the link names, and with no link pi skips the
path silently. **macOS host:** the same render, with a darwin build made by the macos-user build act
([§7.1](#71-the-build-act), [PPX-D45](#PPX-D45)). Where that act cannot run on this Mac (no sandbox
account yet), the line says why and names its step.

## 9. Failure, and the next step

[`patched-forks.md` §8.1](patched-forks.md#81-the-failure-table) holds, with these changes:

| Failure | What serves | Said, with the next step |
| :--- | :--- | :--- |
| A newer upstream does not replay | nothing new: the operation stops by default, and `YOLO_ALLOW_PATCH_FAILURES` mounts the good build for that launch ([PF-D81](patched-forks.md#PF-D81)); the other hatches are in [§9.1](#91-recovery-hatches-for-an-extension) | the error block, whose recipe rebases the series and whose hatch list names the extension's key |
| A newer upstream replays but does not build | nothing new: the act stops, its good build included, unless a recovery hatch is set ([OQ-PPX3](#OQ-PPX3), [§9.1](#91-recovery-hatches-for-an-extension)); inside the back-off every launch stops again with no rebuild | the error block: the build's error, `yolo capture <pack>/<name>` under `Repair:`, and the hatches, `YOLO_USE_CACHED_GOOD` among them at a jail launch |
| A newer build one of yolo's own steps keeps from running: its build jail does not start (Apple Container beside a running jail included), a lock wait runs out, an earlier build jail is not yet gone, or the series cannot be replayed (an apply error) | nothing new: the act stops, its good build included, unless a recovery hatch is set ([PD-NF1](../design/program-delivery.md#PD-NF1), [`patched-forks.md` PF-D91](patched-forks.md#PF-D91)); a jail that did not start is recorded with the back-off, and an apply error stops every launch until the next check ([PF-D92](patched-forks.md#PF-D92), [PF-D93](patched-forks.md#PF-D93)) | the cause once for every extension it stopped, then each error block: what stopped it, what to fix, `yolo capture <pack>/<name>`, and the hatches |
| A `produces` path is missing, the tree names the build's home, or the delta has strays | as the row above: a failed build with back-off, which stops the act | the paths, and the build line as the fix |
| The entry was reaped between the lookup and the copy, or during it | the partial copy removed and the record re-read once; else rebuilt | as a first build |
| The per-launch copy fails (a full disk) | nothing for this launch; the store is untouched | the error and the extension |
| Nothing serves | nothing: a fresh launch refuses before booting ([PPX-D40](#PPX-D40)) | see below |

**Nothing serves.** At a notch that builds trees, a fresh launch refuses before booting when the
owning agent pack's list entry reaches it, whatever the command, naming the extension, the cause and
the ways back: the fix the build's own failure names; `yolo pack series check` and the rebase recipe
for a series that no longer applies; `yolo capture <pack>/<name>` to retry; dropping the list entry,
or the pack, to run without it; and the recovery hatches that would get the launch running
([§9.1](#91-recovery-hatches-for-an-extension)). `YOLO_SKIP_EXTENSIONS` is the one that starts pi
without the extension. `YOLO_ALLOW_MISSING_PROGRAMS`, the readiness act's bypass, still starts the
jail and says so, but pi's launchers stop for the missing tree, so only the shell works. It is decided where the trees are, before the image step, reading no command:
yolo never reads the command line ([HP-DIR2](host-agent-environment.md#HP-DIR2)). A build
that fails while a good build exists refuses too, unless a recovery hatch is set
([OQ-PPX3](#OQ-PPX3)).

- **Each cause is said once** ([PPX-D42](#PPX-D42)), with every extension it left without a build. A
  refused boot writes its structured cause beside its `boot.log`, and the host reads it rather than
  parse prose; a **build cause** *(coined in PPX-D42)* is the key-free lines saying whose fault it is,
  the packs the jail was sealed to, and the build's log. **One refusal stops the rest**: a build jail
  whose own config was refused at boot starts no later build sealed the same way. A generator refused
  at a read-only path under a directory the user's own jail makes writable is **yolo's bug**, said so
  with where to report it; a read-only path under no such directory is the pack's to fix.
- **The launchers still stop before exec** ([PPX-D18](#PPX-D18), [PPX-D24](#PPX-D24)): the owning
  agent pack's launchers, the base's and any fork's, carry a gate that runs first, before any install,
  update or refresh ([XB-D25](../design/pi-extension-store-builds.md#XB-D25)), and the jail's shell
  stays up. It is the backstop for an attach, which boots nothing, and a nested launch.
- **Notches that build no tree** (Apple Container below its read-only floor, a nested launch) start
  pi with a line said once, naming `YOLO_RUNTIME=podman`. macos-user and a Mac's host build one
  ([PPX-D45](#PPX-D45)); macos-user's refusal also names the two container backends,
  `YOLO_RUNTIME=container yolo` and `YOLO_RUNTIME=podman yolo`, as a patched fork's does.
- **With no owning agent pack** no list entry names the tree, pi does not load it, the lint says so,
  and nothing stops. **A tree is built and mounted only at a notch its entry reaches**
  ([PPX-D35](#PPX-D35)): one listed only in a guarded posture list reaches the host and no jail, so
  no jail builds it and pi there starts without it.

> [!WARNING]
> A mountpoint podman made on an earlier launch may be left empty at `~/<into>`. pi does not skip an
> empty directory as it skips a missing path: it tries to load the directory itself as one extension,
> which fails ([`pack-pi-resources.md` §1](pack-system.md)).
> This is one reason the launch refuses rather than starting pi with nothing at the path.

**Two departures from the held store's rule**
([OQ-2](../design/pi-extension-store-builds.md#OQ-2) there): a launch that waited for another takes
that launch's result, failure included ([PF-D17](patched-forks.md#PF-D17)), and a failed build is
backed off while a good build is here ([PF-D9](patched-forks.md#PF-D9)): inside the back-off a launch
stops on the recorded failure without building it again ([OQ-PPX3](#OQ-PPX3)). Without them, a
build that keeps failing would cost every fresh launch a rebuild.

### 9.1 Recovery hatches for an extension

The [recovery hatches of `patched-forks.md`](patched-forks.md#85-recovery-hatches) serve an extension
as they serve a fork ([OQ-PBR6](patched-forks.md#why-its-this-way)), named by the extension's key
(`<pack>/<name>`), its pack or its name, under the same grammar. What differs:

- **`YOLO_SKIP_EXTENSIONS`** ([PBR-R8](patched-forks.md#PBR-R8)) covers a built extension, patched or
  unmodified. For that launch it is not advanced, built, copied or mounted, and **every entry of the
  owning agent pack's own lists that loads it is left out** (`packload.ApplyTreeLists`), so pi starts
  without it rather than failing on an empty mountpoint. A fresh jail launch marks it `skipped` on the
  wire to the jail's boot, which drops the entries and stops no launcher for it; the missing-build
  refusal does not count it; one line names each skipped extension, and an extension with a
  `fallback` takes no fallback while skipped. At the host, `yolo host apply --assert` and `yolo host
  -- pi` leave it out the same way: no link is written or moved and the entries are dropped from the
  render. Nothing is recorded, so the next launch without the variable delivers it as before. An
  entry the user wrote, or another pack's, is not touched. An attach keeps the extensions its jail
  booted with, and a launch in a jail delivers no tree; each says the variable has no effect there.
  On macos-user it works as on podman ([PPX-D45](#PPX-D45)).
- **`YOLO_USE_CACHED_GOOD`** takes an extension's key too: the record's older good build's tree,
  validated as a fork's is, becomes the launch's per-launch copy, with one line naming the older
  build. No check, replay or build runs, and the record is not written. With nothing usable it hands
  no copy, and the missing-build refusal says why.
- **The walk-back** (`YOLO_ALLOW_OLDER_UPSTREAM`, [`patched-forks.md` §8.6](patched-forks.md#86-the-walk-back))
  runs through the same advance, with the same budget, hold and disclosure line, which names the
  extension: `extension <key>: walked back to v1.1.0; upstream v1.2.0 does not take the series`.
- **`YOLO_ALLOW_MISSING_PROGRAMS` is not the way past a missing extension.** It starts the jail, and
  the owner's launchers still stop for the missing tree ([PPX-D18](#PPX-D18)), so the refusal lists
  `YOLO_SKIP_EXTENSIONS` for an extension instead.

## 10. Trust and disclosure

- **The series is data on the host** ([P3](patched-forks.md#1-principles)). An upstream's install and
  build scripts run only in the sealed, credential-free jail, which is tighter than a `git:` entry,
  whose dependency step runs them inside the user's jail.
- **The footprint marks a patched extension for review**, naming the source, ref, follow rule, series,
  build and landing, in place of *"read-only tree"*; the claim is disclosed at every launch, routed as
  a patched fork's `program` claim is ([PPX-D15](#PPX-D15), [PPX-D29](#PPX-D29)).
- **No flag hides the launch line, the move line or the held suffix**
  ([`OQ-RO3`](report-tiers.md#why-its-this-way)). A launch line reads, for example: *"matt/pi-subagents:
  held at 6f1027f7 (the series' base) + 7 patches; upstream v0.75.0 does not take 0001-….patch —
  `yolo pack status` prints the git commands that rebase it"*.
- **Stated, not gated.** A fetched pack's pin fixes its series and the upstream's address, not the
  upstream's bytes, which move hourly. Under [`OQ-TP9`](../design/trust-paths.md#decision-ledger)
  the boundary is disclosure.

## 11. Notch coverage

| Notch | Patched extension |
| :--- | :--- |
| **jail, podman** | yes |
| **jail, Apple Container** | from the read-only floor up, yes, built as a patched fork is: a capture jail cannot start beside a running jail (INFERRED), so a candidate stays pending and the good build serves. **Below the floor no tree is built**, because `:ro` is ignored there and a build jail's read-only binds would be writable, which the seal does not allow for. A good build already on this machine is still delivered as a per-launch copy; with none, pi starts without the extension and the line names `YOLO_RUNTIME=podman` |
| **jail, macos-user** | yes, built for darwin by the sealed macos-user build act and copied into the sandbox home through the home overlay ([PPX-D45](#PPX-D45)). MEASURED on a GitHub-hosted Mac: the `macos-user.yml` cells passed in [run 43120634525](https://github.com/mschulkind-oss/yolo-jail/actions/runs/43120634525) on 2026-11-11 ([`patched-forks.md` §14](patched-forks.md#14-unbuilt-and-unmeasured)), with a hermetic fixture and no pi loading the tree |
| **`yolo host`, Linux** | yes ([§8.3](#83-at-the-host)) |
| **`yolo host`, macOS** | yes, the Linux host's render with a darwin build from the same act ([PPX-D45](#PPX-D45)). UNMEASURED on a Mac |
| **guest** | unbuilt, as every verb there is |

## 12. Relation to the other pi routes

- **The held store** ([`pi-extension-store-builds.md`](../design/pi-extension-store-builds.md#the-held-store))
  coexists: its rewrite passes local entries through untouched. Its store is writable by every jail,
  so nothing in it may ever feed a host build.
- **Nothing is rewritten** (PE7), so neither [OQ-6](../design/pi-extension-store-builds.md#OQ-6)
  nor an [`OQ-LT2`](pack-system.md#oq-lt2) amendment is needed.
- **[OQ-PR1](pack-system.md#oq-pr1)'s slot**, if it ships, registers one tree per
  pack; a registration per contribution could later write the list entry the author writes by hand.

## 13. Migrating the maintainer's five forks

For each extension:

1. Export the series: `git format-patch --base=$(git merge-base <upstream>/main HEAD) -o <pack>/patches/<name> <upstream>/main..HEAD`.
   For a fork that commits build output its upstream ignores, append `-- . ':(exclude)dist'` and
   declare a `build` that regenerates it. For an upstream with no tags, declare `follow: "head"`,
   or the default rule leaves it at its series' base ([PF-D60](patched-forks.md#PF-D60)).
2. In one edit, add the `files` contribution and replace the extension's `git:` entry with
   `~/<into>`. Leaving both loads it twice, which [PPX-D34](#PPX-D34)'s lint reports.
3. Launch.

On day one, base plus series is each fork's tree: those whose series conflict are held at their base,
which is exactly the fork's current tree, and the held suffix names `yolo pack status`. A tree listed
only in a guarded posture list is built and loaded at the Linux host and in no jail
([PPX-D35](#PPX-D35)).

On macos-user and a Mac's host each migrated extension is built for darwin, as on Linux
([PPX-D45](#PPX-D45)). The first launch there builds every one, so it takes longer.

**An older yolo** refuses the manifest at the host, as it refuses a patched fork's; the mitigations
are [`patched-forks.md` §10](patched-forks.md#10-migration-from-a-plain-fork)'s, and `yolo features`
names `patched-extensions`.

## 14. Alternatives, and what this does not cover

> [!WARNING]
> The obvious alternatives, each rejected for a reason that still holds:
>
> - **The program route** (`fork_of`): an extension has no bin and no base program, and the fork
>   fields are refused off a `program` with `via: "source"`.
> - **Patching pi's own git checkout**: the refresh resets and cleans it.
> - **Patching the published npm package**: some packages publish build output, so a series written
>   against the source has nothing to apply to.
> - **Landing in `extensions/<name>`**: discovery loads it there whether or not a list entry names
>   it ([§4](#4-the-declaration)).
> - **A new contribution kind**: the delivery is `files`' read-only mount; a kind would be a second
>   path to it.
> - **Rewriting the `git:` entry automatically**: it needs [OQ-6](../design/pi-extension-store-builds.md#OQ-6)
>   and an [`OQ-LT2`](pack-system.md#oq-lt2) amendment (PE7).
> - **The held store as the base**: it replays in the jail (against P3), runs git on every launch
>   (against P4), has no ratchet, no host delivery, and keeps its records where every jail can write.
> - **Binding the store entry read-only, with no copy**: reaping becomes a correctness event (PE6),
>   and below Apple Container's floor no store is mounted.
> - **A check record per declaration**: packs are user-scope, one pack serving every workspace, and
>   the exact lookup already keeps each series' build its own ([PPX-D17](#PPX-D17)).
> - **Copying into the pack's staged directory**: every attach would report the pack changed.

**Not covered:**

- **A tree with no build, admitted without a jail.** macos-user builds trees now
  ([PPX-D45](#PPX-D45)), so this is no longer needed there.
- **Writing the list entry for the author.** It waits on [OQ-PR1](pack-system.md#oq-pr1).
- **Moving a tree inside a running jail.** A running jail keeps what it booted with; a tree moves
  only at a fresh launch, where pi's own refresh would move a `git:` package in a running jail.
- **A `files` contribution, or a list entry, scoped to a posture or notch.**
- **A lint for an old entry under another name** than the extension's or its upstream's. The pi pack
  could declare the pattern, as it does for the subagents render's `whenListed.matches`.
- **Resolving conflicts, and writing the series**, as for [patched forks](patched-forks.md#13-what-this-does-not-cover).

## 15. Risks that stay live

| Risk | Mitigation |
| :--- | :--- |
| A clean pick that is wrong code | As for patched forks: the build is the only automatic check; every move is disclosed; a hold is one ref edit or one `agent_updates` key |
| Unreviewed upstream code runs inside pi's process | The same exposure a `git:` entry carries, minus its scripts running in the user's jail |
| An extension that writes into its own directory fails on the read-only mount | Said by pi as the extension's error; the fix is the extension's |
| A monorepo whose root takes a workspace package from the registry would never ship a later member that patches that package under npm (INFERRED) | If such a member appears, the build line must build and link the workspace package; a lint cannot see this |
| A native module built against the jail's Node runs under another at the host | The toolchain is on the receipt; pi reports the load error |
| A per-launch copy costs a full copy per fresh launch on a filesystem without reflink (ext4) | Free under reflink, which btrfs gives; stated |

## 16. Unbuilt and unmeasured

- **No pi has loaded a tree yolo built.** Whether a real pi lists a built tree's tools and skills,
  with its own `node_modules`, from a local entry, is a human's check in a nested jail; no automated
  test starts pi (AGENTS.md, Testing). This and migrating the maintainer's five forks with the
  migration kit are left for the maintainer, who may overrule [PPX-D18](#PPX-D18) and
  [PPX-D19](#PPX-D19) once he has tested a build.
- **The series lint** ([PPX-D16](#PPX-D16)) is not built: a member that adds a path the upstream's
  `.gitignore` ignores at the base, said once naming `build`. It needs the upstream's `.gitignore` at
  the base, read in the replay's scratch repository.
- **A host pi keeping the tree it loaded across a move** is INFERRED from pi's loader, UNMEASURED.
- **A patched extension on macos-user** is measured by fixture only: the `macos-user.yml` cells
  passed on a GitHub-hosted Mac on 2026-11-11
  ([`patched-forks.md` §14](patched-forks.md#14-unbuilt-and-unmeasured)), and no pi has loaded a
  darwin tree yolo built.

## 17. The pre-launch refresh

The **pre-launch refresh** *(coined for this build, 2026-09-25)* is a step a pack declares on a
`program`, which yolo's generated launcher runs before it execs the program. `packs/pi` declares one
that runs `pi update --extensions`, so the extensions pi installs itself (`npm:` and `git:` entries)
are current before pi starts, and pi's startup check finds nothing newer and shows no *"Package
Updates Available"* box. Core does not know it is pi: `packdecl.Refresh` documents the field, and
`internal/entrypoint` renders whatever a pack declares into both launcher templates. Its tests are in
`prelaunchrefresh_test.go` (`internal/entrypoint`) and `refresh_test.go` (`internal/packdecl`).

### 17.1 The storage tier, retired

From 2026-09-21 to 2026-10-05 pi's npm prefix, `~/.pi/agent/npm`, was a link into one machine-scoped
store shared by every jail ([OQ-1](#why-its-this-way)), installed by a `shared_directory` hook — the directory
twin of `shared_credentials`, migrating a workspace's populated prefix into an empty store in the
jail, since `rename(2)` across two bind mounts is `EXDEV`. It is **retired**: one jail's install
could change what another jail ran, so
[XB-D14](../design/pi-extension-store-builds.md#XB-D14) made the prefix per workspace again and moved
the refresh's lock, stamp and seen markers into the workspace's own `.pi`. The `shared_directory`
hook remains for any pack, and its emptiness test still skips yolo's own `.yolo-` bookkeeping
entries.

### 17.2 The execution tier

The refresh runs inside the generated launcher, strictly before the exec:

1. **The throttle.** A stamp per program, with the lock's scope (the workspace's `.pi`), checked
   against the launchers' `UPDATE_INTERVAL`. The refresh's stamp is separate from the program's own
   update stamp. A changed package list is due at once: the refresh declares `due_on_change` on
   `.pi/agent/settings.json`, keyed on content.
2. **The policy gate.** `agent_updates` off for `pi` skips it unconditionally (PE8). Under
   `"agent_updates": { "pi": "next-launch" }` the hourly refresh runs as a detached job behind pi, so
   pi does not wait and the next launch runs what it installed; a launch whose settings content is
   new still refreshes first ([OQ-PD30](../design/program-delivery.md#decision-ledger)). With the
   refresh behind the TUI, pi may show its update box for that session.
3. **When it is worth running.** Only while pi's user or project settings name a package pi installs
   itself (`refresh.only_if`, [XB-D23](../design/pi-extension-store-builds.md#XB-D23)); `pi --version`
   and `pi -v` run no refresh ([XB-D24](../design/pi-extension-store-builds.md#XB-D24)); a refresh that
   fails on new settings content is retried once its failure is an hour old, not at every launch
   ([XB-D26](../design/pi-extension-store-builds.md#XB-D26)); a launcher a patched extension stops
   pays for none of it first ([XB-D25](../design/pi-extension-store-builds.md#XB-D25)).
4. **Execution.** Under the resolved Node, since `pi` declares a `node_floor`; stdin from
   `/dev/null`, so it never reads the user's terminal; stdout to stderr, so a piped `pi -p …` gets
   only pi's output; bounded by the launcher's `_bounded` (no controlling terminal, SIGTERM at
   `UPDATE_TIMEOUT`, SIGKILL shortly after, through `yolo internal no-terminal`, which bounds it on
   macos-user too). A launcher with neither that verb nor `timeout(1)` runs it unbounded.
5. **Failure is reported and the launch goes ahead.** A timeout, an interruption and any other
   non-zero status are each reported; all are stamped, and pi still launches. `yolo pack update`
   (`YOLO_PACK_UPDATE=1`) and the launcher's re-entry path exit before reaching it.

**Offline**, read statically from pi 0.87.1 and not measured: a failed `npm view` makes pi go on to
`npm install`, whose failure sets exit code 1, so an offline refresh most likely exits 1 after pi's
own network timeouts, inside the launcher's bound. With `PI_OFFLINE=1` exported, pi returns at once.

> [!WARNING]
> **Do not suppress pi's startup check with `PI_OFFLINE=1`**: it also disables model provider calls
> and network fetches elsewhere in pi. And do not reimplement pi's package manager in Go: pi ships a
> complete one (`pi update --extensions`, verified 2026-09-22 to be parsed only under `update` and to
> be non-interactive, by reading pi 0.87.0's bundle), and shelling out to it keeps fidelity with
> upstream. Leave pi's in-app notification untouched ([OQ-3](#why-its-this-way)): an exact pin never triggers it,
> and a range is compared only within its range.

### 17.3 The concurrency tier

A non-blocking directory lock serializes the refresh's writers, its path declared by the pack
(`.pi/.yolo-update.lock`), taken by `_take_refresh_lock`:

- **mkdir, not flock.** A flock arbitrates only within one kernel, and Apple Container runs each jail
  in its own VM over a shared host directory, where only the host filesystem's own `mkdir(2)` is
  atomic. There is no `flock(1)` in the image or on a stock macOS.
- **Three answers**: *taken*, *held*, and *cannot be taken at all* (the directory is missing or
  read-only), so a user whose mount never happened is not told another update is in progress. The
  launcher never creates the store. The launch fails open and the refresh fails closed.
- **A non-blocking fallback.** A held lock older than `STALE_LOCK` is broken and retried; otherwise
  the launch prints a notice naming the lock and runs what is installed. A jail never blocks waiting
  on the lock. The contended launch writes no stamp; the holder writes one when it finishes.
- **An owner token.** The lock holds a token (pid, random and time, since pids repeat across jails'
  namespaces); release checks it, so a holder whose lock was broken as stale never deletes the new
  holder's lock. A stale break removes only the token file and an empty directory, never `rm -r` on a
  path a manifest named.
- **A heartbeat keeps a held lock young.** While it refreshes, the launcher touches the lock every
  `REFRESH_HEARTBEAT` with `touch -c`, so only a lock whose launcher died ages past `STALE_LOCK`, and
  an unbounded refresh does not let a second one in.
- **The lock's name marks it as yolo's bookkeeping.** `packdecl` refuses a `lock` whose name does not
  start with `.yolo-` (`packdecl.StoreBookkeepingPrefix`), so a store's emptiness test can skip it.

> [!WARNING]
> **One race remains.** Breaking a stale lock is check-then-act, so two launchers can judge the same
> lock stale at once and both hold it. That needs a holder whose launcher died and two launches within
> a few syscalls of each other.

### 17.4 Pi's own installs

The lock serializes only the launcher's refresh. pi installs packages at one more point, its own
startup: its resource loader installs any configured npm package missing from the prefix or outside
its configured range, pinned or not, with no lock (read statically from pi 0.87.1). The refresh
reaches a package first only when it runs first; it does not when it is throttled, when the launch is
contended (which execs pi at once), or for an exact pin, which `pi update --extensions` skips.

**Ruled 2026-10-05 ([OQ-4](#why-its-this-way)): accept it and document it.** Two pi sessions starting together
may both install a hand-added package, as on any machine pi runs on; with the npm prefix per
workspace the race stays inside one workspace. The maintainer: *"I don't really care if it's par for
the course anyway."* The user guide says so ([packs and skills](../../userguide/guides/packs-and-skills.md)).
The held store's rewrite of `npm:` entries to local trees would remove the case for every entry a
pack declares ([`pi-extension-store-builds.md` §4.4](../design/pi-extension-store-builds.md#44-entries-no-pack-declares)).

**Not built: yolo resolving and pinning pi packages.** [OQ-2](#why-its-this-way) ruled that yolo should resolve
and pin the package set through `internal/packsrc` and `packs.lock.json`, leaving the launcher only to
materialize it. Only the materializer exists: nothing in yolo resolves or records a pi package's
version, so `pi update --extensions` resolves as well as installs, and the version choice is still
pi's and the registry's, the in-range maximum at refresh time. The pack resolver that would be
extended ships, but resolves and pins packs only. A pack can still *declare* its pi packages with a
`config-list` on `pi/settings` at `/packages` ([`pack-system.md`](pack-system.md#adding-entries-to-an-array-config-list)),
which composes with that missing half.

### 17.5 Failure modes

| Failure | System response | What the user sees |
| :--- | :--- | :--- |
| Offline, or the registry is down | the bound or pi's non-zero exit is caught; the stamp is touched; pi starts | pi runs with what is installed; no retry until the stamp is due |
| Concurrent launch | the lock is held; the launch skips the refresh | pi starts at once; a package still missing is installed by that pi itself ([§17.4](#174-pis-own-installs)) |
| Stale lock (a crashed launcher) | the lock is broken past `STALE_LOCK` and the refresh proceeds | nothing to do |
| The lock's directory is missing or read-only | reported as *cannot take the refresh lock*, never as contention; the stamp is touched | the notice repeats when the stamp is due, not every launch |
| A malformed package spec | pi skips the bad entry | pi's error on stderr |

## Open questions

None open.

## Current values

Verified at `8e65f3444`. The prose above explains what each of these is for; this table is the only
place the numbers themselves are stated. The patched-fork values (the check throttle, the bounds, the
back-off) are in [`patched-forks.md`](patched-forks.md#current-values).

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| The tree build's reserved directory | `~/.local/share/yolo-tree/<name>` | `packdecl.TreeReservedRoot` |
| A launch's per-launch copy | `<tree>.patched/<key, "/" as "--">-<8 hex of the key's sha256>` beside the pack tree | `run.PatchedCopySlug`, `patchedCopiesSuffix` |
| A macos-user launch's copy | `AGENTS_DIR/<cname>/tree-copies/<the same slug>/<32 hex of the entry and prefix>/tree`, beside its `lease` | `paths.TreeCopyRoot`, `internal/cli`'s `reusableTreeCopy` |
| The host's versioned copies | `~/.local/share/yolo-jail/host-trees/<slug>/<entry key>`, mode 0700 | `paths.HostTreesDir` |
| The jail's tree decisions | `YOLO_PATCHED_TREES` | `entrypoint.PatchedTreesEnv` |
| A tree's build jail | `YOLO_TREE_BUILD=<name>` | `entrypoint.TreeBuildEnv` |
| A sealed build jail | `YOLO_SEALED_BUILD=1` | `entrypoint.SealedBuildEnv` |
| A refused boot's structured cause | `<workspace>/.yolo/boot-refusal.json` | `entrypoint.BootRefusalName` |
| Missing-build bypass | `YOLO_ALLOW_MISSING_PROGRAMS` (any non-empty value) | `paths.AllowMissingProgramsEnv` |
| Skip an extension | `YOLO_SKIP_EXTENSIONS=<1 or names>` | `patchrecovery.SkipExtensionsEnv` |
| The wire's mark for a skipped extension | `"skipped": true` in its `YOLO_PATCHED_TREES` entry | `entrypoint.TreeDelivery.Skipped` |
| pi's refresh | `"refresh": {"argv": ["update", "--extensions"], "due_on_change": [".pi/agent/settings.json"], "lock": ".pi/.yolo-update.lock", "only_if": …}` | `packs/pi/pack.json` |
| The refresh's stamp and seen markers | `.pi/.yolo-refresh/`, beside the lock | `entrypoint.RefreshStateDirName` |
| Refresh throttle | `UPDATE_INTERVAL`, 3600 s | the launcher templates |
| Refresh bound | `UPDATE_TIMEOUT`, 60 s, then SIGKILL 5 s later | the launcher templates (`_bounded`) |
| Stale lock | `STALE_LOCK`, 600 s | the launcher templates |
| Lock heartbeat | `REFRESH_HEARTBEAT`, 60 s | `internal/entrypoint/prelaunchrefresh.go` |
| A refresh lock's required name prefix | `.yolo-` | `packdecl.StoreBookkeepingPrefix` |

## Why it's this way

Rulings and implementation decisions a maintainer would otherwise undo, keeping their original ids —
cited from Go comments, tests and sibling docs, and this is where they resolve. Graduated from the
patched-extension and pi-extension-lifecycle design docs, whose full ledgers, with each row's test
names, are in git history. [OQ-PPX1](#why-its-this-way) and [OQ-PPX2](#why-its-this-way) were decided on their leanings under the maintainer's
delegation of 2026-10-04 (*"I want to get the patched forks and patched extensions out as soon as
possible. So if there's design decisions you can make, make them and build it. And we can always
adjust later."*), and remain his to overrule once he has tested a build.

| ID | Ruling |
| :--- | :--- |
| <a id="OQ-PPX3"></a>[OQ-PPX3](#OQ-PPX3) | **The maintainer's ruling, 2026-10-16, correcting the reading recorded under [PPX-D40](#PPX-D40):** a build that fails is fatal even while a last good build exists; yolo never falls back to an older build on its own, because a warning can be missed. Only a recovery hatch the user sets runs an older build ([§9.1](#91-recovery-hatches-for-an-extension): `YOLO_USE_CACHED_GOOD`, `YOLO_ALLOW_PATCH_FAILURES`, `YOLO_ALLOW_OLDER_UPSTREAM`), or an `agent_updates` hold the user declared. *"if a build fails, we don't want to automatically roll back to some other version because warnings may be missed but if the escape hatch is used it should be able to go back. None of this happens without one of the escape hatches selected."* Applies to patched forks and patched extensions alike ([OQ-PBR6](patched-forks.md#why-its-this-way)). **Built:** a newer build that fails while a good build of the series as it stands is here stops every act that would run it — a fresh jail launch, `yolo host -- <bin>`, `yolo host apply`, a next-launch delivery after the background advance recorded the failure — with the patch failure's one refusal shape, until the back-off ends and a launch builds it again, or a newer release passes it by. Supersedes [PPX-D14](#PPX-D14) and [`patched-forks.md` PF-D8](patched-forks.md#PF-D8)'s serving half. **Extended 2026-10-16 by [PD-NF1](../design/program-delivery.md#PD-NF1)** to a newer build one of yolo's own steps keeps from running — the build jail not starting, Apple Container unable to start one beside a running jail, a lock wait that runs out, an apply error in the walk or the build's own replay — which were "not failed builds" under [`patched-forks.md` PF-D21](patched-forks.md#PF-D21) and [PF-D45](patched-forks.md#PF-D45). **Built:** the same refusal, its head and repair naming the step ([PF-D91](patched-forks.md#PF-D91)–[PF-D96](patched-forks.md#PF-D96)); an upstream yolo cannot reach is no failure, and runs the good build, said. |
| <a id="PPX-D1"></a>PPX-D1 | A patched extension is a `files` contribution with `source` and a series in place of `from`; there is no new kind; `from`, `fork_of`, `agent` and `agents` are refused beside `source` |
| <a id="PPX-D2"></a>PPX-D2 | The extension key is `<pack>/<last segment of into>`, unique per pack across its patched extensions and its patched programs' bins; every record, lock, selection, message and explicit act uses it |
| <a id="PPX-D3"></a>PPX-D3 | `into` is required and may not land on PATH, for any pack; `build` is optional and the sealed jail runs either way; `produces` is optional and tree-relative |
| <a id="PPX-D4"></a>PPX-D4 | The owning agent pack is the selected pack that declares the surface of the contributing pack's list entry naming the tree (widened by [PPX-D36](#PPX-D36)); with none, pi never loads it. Not read from a `state` prefix of `into` |
| <a id="PPX-D5"></a>PPX-D5 | The build is a fork's build act with the seal narrowed to the contributing pack and one fixed final copy into a reserved directory under `~/.local`; the admit adds the `produces` paths, no stray delta and no reference to the build's home |
| <a id="PPX-D6"></a>PPX-D6 | The recipe is `["tree", build, sorted produces, subdir, series digest]`; selection by extension key and platform with an exact lookup; `yolo prune`'s rule unchanged |
| <a id="PPX-D7"></a>PPX-D7 | A fresh launch materializes the good build by reflink or copy, never hardlink, into a per-launch directory beside its pack tree, mounted read-only; no store is mounted; a move reaps every other build of the key at once, marker first; a copy whose marker is gone when it ends is removed and the record re-read once |
| <a id="PPX-D8"></a>PPX-D8 | The jail learns what it was handed from `YOLO_PATCHED_TREES`, a once-at-boot sibling of `YOLO_FORK_BUILDS` |
| <a id="PPX-D9"></a>PPX-D9 | `agent_updates` off for the contributing pack or the owning agent pack holds a patched extension; with a fork of the owning agent's program selected, the fork pack counts too |
| <a id="PPX-D10"></a>PPX-D10 | pi learns of a built tree only through a list entry its author writes; one lint warns when no entry loads it. Amended by [PPX-D34](#PPX-D34) and [PPX-D36](#PPX-D36) |
| <a id="PPX-D11"></a>PPX-D11 | The Linux host renders a patched extension as a host-private versioned copy and an owned link swapped by rename, after the check and the advance and before the launch gate's comparison; the previous version is kept until the next move |
| <a id="PPX-D12"></a>PPX-D12 | The owning agent pack's launchers act on a patched extension's reason, only at a notch its entry reaches. Its "the jail launch is never refused" is superseded by [PPX-D40](#PPX-D40); the launchers' half stands |
| <a id="PPX-D13"></a>PPX-D13 | Checks of different extension keys run concurrently, except two naming one upstream repository, which serialize on its mirror lock; lines print in declaration order. Since 2026-10-05 the launch checks and builds them at once in its slot's pool ([XB-D56](../design/pi-extension-store-builds.md#XB-D56)) |
| <a id="PPX-D14"></a>PPX-D14 | A newer upstream that does not build leaves the good build serving (for one that does not apply, [PF-D81](patched-forks.md#PF-D81)). **Superseded by [OQ-PPX3](#OQ-PPX3), 2026-10-16:** it stops the act, and the good build runs only under a recovery hatch or an `agent_updates` hold |
| <a id="PPX-D15"></a>PPX-D15 | The footprint marks a patched extension for review, naming its source, ref, follow rule, series, build and landing; no flag hides its launch, move or held lines |
| <a id="PPX-D16"></a>PPX-D16 | A series member that adds a path the upstream ignores at the base is said once, naming `build`. Not built ([§16](#16-unbuilt-and-unmeasured)) |
| <a id="PPX-D17"></a>PPX-D17 | The check record is per extension key, never per declaration |
| <a id="PPX-D18"></a>PPX-D18 | ([OQ-PPX1](#why-its-this-way).) The good build serves; with none, the owning agent pack's launchers stop before exec and say why; notches that never build trees are exempt. Amended by [PPX-D40](#PPX-D40): the launchers' stop is now the backstop for an attach and a nested launch |
| <a id="PPX-D19"></a>PPX-D19 | ([OQ-PPX2](#why-its-this-way).) A patched extension writes no pin to `packs.lock.json`; its good build is machine-local |
| <a id="PPX-D20"></a>PPX-D20 | The reserved directory is `~/.local/share/yolo-tree/<name>`; the tree's build jail writes its toolchain record first, runs the build line in a subshell with the image's tools first on PATH, on lines of its own so a trailing comment cannot reach the copy, and ends in `cp -a` into the reserved directory |
| <a id="PPX-D21"></a>PPX-D21 | A tree's receipt records the extension's name as its `bin` and the extension key as its `fork`, so selection and prune need no new field; the admit counts any reference the content scan records, any file it says embeds the home, and a scan that did not run, as a reference to the build's home |
| <a id="PPX-D22"></a>PPX-D22 | The per-launch copy is `<tree>.patched/<slug>`, made by `capture.CopyTree` keeping every directory's owner write bit so it can be removed, and removed with its tree; the record's one re-read is the advance run again |
| <a id="PPX-D23"></a>PPX-D23 | `YOLO_PATCHED_TREES` carries, per extension key, `into`, the handed entry or the reason there is none, the owning agent pack, and `stop`; the delivery record gains an additive `trees` map, which an attach reads |
| <a id="PPX-D24"></a>PPX-D24 | A jail's `stop` is set when nothing serves, the owner's entry reaches a jail, and the notch builds trees; the gate is baked into each of the owner's launchers immediately before exec. Amended: a fresh launch refuses instead ([PPX-D40](#PPX-D40)), and the gate runs first in each launcher ([XB-D25](../design/pi-extension-store-builds.md#XB-D25)) |
| <a id="PPX-D25"></a>PPX-D25 | The host's versioned copies live under `~/.local/share/yolo-jail/host-trees/`, 0700 and never mounted by a jail; the advance runs at `yolo host apply --assert` and `yolo apply --at host --assert`, never a dry run, and at `yolo host -- <bin>` only under `host_apply_on_launch` when the owning agent pack declares `<bin>`; a macOS host refuses each tree naming a jail. **Amended by [PPX-D45](#PPX-D45):** a Mac's host builds its trees for darwin |
| <a id="PPX-D26"></a>PPX-D26 | At the Linux host, `yolo host -- <bin>` of an owning agent stops before exec when a tree its entry reaches the host with names no directory at `~/<into>`, reading the link rather than the record; under `host_management: none` it stops nothing |
| <a id="PPX-D27"></a>PPX-D27 | The no-entry lint is a warning at `yolo pack lint`, never a failure, and a line in every launch's "Patched extensions this launch" block; lint counts a series directory as content the pack ships |
| <a id="PPX-D28"></a>PPX-D28 | The explicit acts take the extension key: `yolo capture <pack>/<name>` (a slashed name routes there only where the selection declares a patched extension), and `yolo pack update`, `install` and `status`; lines name "extension `<key>`" |
| <a id="PPX-D29"></a>PPX-D29 | A patched extension's review-marked claim is disclosed at every launch, routed per claim as a patched fork's `program` claim is |
| <a id="PPX-D30"></a>PPX-D30 | A tree's sealed build jail is told which extension it builds (`YOLO_TREE_BUILD`), and its boot names no orphaned overlay or list, since it renders no pack surface ([PPX-D41](#PPX-D41)) |
| <a id="PPX-D31"></a>PPX-D31 | `yolo host apply --revert` removes every link the `files` ownership record names into the host's versioned copies, and every versioned copy no recorded link names; a plain `files` tree's output is left |
| <a id="PPX-D32"></a>PPX-D32 | Superseded by [`patched-forks.md` PF-D85](patched-forks.md#PF-D85): `yolo pack rebase` is retired; an extension's conflict prints the git recipe under its owner key's error block |
| <a id="PPX-D33"></a>PPX-D33 | The host apply `yolo pack update` runs builds no patched extension: it links the good build already here, and an extension with none says `yolo host apply --assert` builds it ([PF-D56](patched-forks.md#PF-D56)) |
| <a id="PPX-D34"></a>PPX-D34 | `yolo pack lint` warns when one list loads a patched extension twice: an entry that loads the tree and a remote entry of the same final name (its last path segment, after an npm scope, without `.git`, cut at its first `@`), matching the extension's own name or its upstream's; a warning at lint only. Asked for by the maintainer, 2026-10-05 |
| <a id="PPX-D35"></a>PPX-D35 | A patched extension is built and mounted only at a notch its owning agent pack's entry reaches: a tree listed only in the guarded posture is built by no jail, and the host retires a link to a tree listed only in the autonomous posture. A tree with no owning agent pack is delivered at every notch |
| <a id="PPX-D36"></a>PPX-D36 | A list entry loads a tree when, cleaned as a path, it is `~/<into>` or lies inside it; core reads none of pi's grammar beyond the path. So a monorepo's tree can load two of its packages without a series member editing its root manifest |
| <a id="PPX-D37"></a>PPX-D37 | A patched extension's series is read by the same reader as a patched fork's, and its errors name the extension's own remedies, never "declare a plain fork instead" |
| <a id="PPX-D38"></a>PPX-D38 | On a host that builds no tree for itself (a macOS host), a tree listed only in a guarded posture list reaches no notch that has it, and each line says so with the step that works: move the entry to a `config-list`, or drop it |
| <a id="PPX-D39"></a>PPX-D39 | The seal does not trip over its own narrowing: a build jail whose selection the seal narrowed runs none of the launch gates that ask whether another selected pack provides what one names (an `agents` selector, a `supersedes` capability, a via route, a required capability), and the seal carries the base of every fork the contributing pack declares, down the chain. A build jail that stops before its build line is relayed with its own last lines. Found at the maintainer's first real launch |
| <a id="PPX-D40"></a>PPX-D40 | The maintainer's ruling in review of [OQ-PPX3](#OQ-PPX3), 2026-10-05: *"if a build fails, of course that's fatal. we should offer solutions and workarounds when that happens for happy path, but it's fatal"*. A fresh jail launch refuses before booting when a patched extension a selected pack needs has no build to deliver at a notch that builds trees, whatever the command, decided where the trees are, before the image step, naming the ways back; `YOLO_ALLOW_MISSING_PROGRAMS` launches anyway, and since [PBR-R8](patched-forks.md#PBR-R8) `YOLO_SKIP_EXTENSIONS` starts the owning agent without it. Supersedes [PPX-D12](#PPX-D12)'s "never refused". The reading of "a build fails" was settled by [OQ-PPX3](#OQ-PPX3) on 2026-10-16: a newer build that fails is fatal even with a good build, as a patch failure is ([PF-D81](patched-forks.md#PF-D81)) |
| <a id="PPX-D41"></a>PPX-D41 | A sealed build jail renders no pack-declared surface and runs no pack hook, a patched extension's and a fork's alike, told by `YOLO_SEALED_BUILD=1` read before the user's env is hydrated, so only the launcher can make a boot a sealed build. Found at the maintainer's launch: a pack surface under a directory only a dropped pack made writable refused every build jail at boot |
| <a id="PPX-D42"></a>PPX-D42 | A refused boot writes its structured cause (`boot-refusal.json`) and the host reads it rather than parse prose; each **build cause** is said once, with every build it left without; one config refusal at boot stops the later builds sealed the same way; a read-only path under a directory the user's own jail makes writable is yolo's bug, said so with where to report it. Found when one file's refusal was relayed four times in unreadable form |
| <a id="PPX-D43"></a>PPX-D43 | Superseded 2026-10-06 by [PPX-D40](#PPX-D40) and deleted: a host-side line, keyed on the command's base name, warning that the program would not start |
| <a id="PPX-D44"></a>PPX-D44 | A host launch links the tree it built even when the render gate's observe pass overruns or names a decision the launch cannot ask (`linkHostLaunchTrees`), taking nothing the render would not; the overrun's line is about the rest of the render. Found at the maintainer's first `yolo host -- pi` with a patched extension, which a successful build turned into a refusal |
| <a id="PPX-D45"></a>PPX-D45 | **The maintainer's rulings, 2026-10-16:** *"Patched forks definitely need to work on macOS user. Sure, we can refuse until then, but let's fix it now ASAP"* ([`patched-forks.md` PF-D97](patched-forks.md#PF-D97)), and earlier *"All of my opinions here go for anything that could be patched, the PI agent or PI extensions."* So **a patched extension works on macos-user and at a Mac's host too**, superseding this doc's "no sealed build there" for both. The advance, the hatches (`YOLO_SKIP_EXTENSIONS` included), the refusal and [OQ-PPX3](#OQ-PPX3) are the container's. The builder is the sealed macos-user build act running the tree's script for darwin. Delivery is a copy through the home overlay into the sandbox home, write-protected by Seatbelt, in place of the read-only mount, and `YOLO_PATCHED_TREES` is relayed to the sandbox's bootstrap. A tree with no build line needs no build, only that copy, but still passes the act and the admit (one admit path, [§4](#4-the-declaration)). Amends [PPX-D25](#PPX-D25)'s "a macOS host refuses each tree"; [PPX-D38](#PPX-D38) stays for a host that still builds none. **MEASURED on a Mac 2026-10-17**, on a developer Mac (macOS 26.5, arm64); `macos-user.yml` has not confirmed it ([the record](../design/macos-user-suite-on-a-developer-mac.md#7-measurements-this-run-produced)): all three cells of [`macosuserpatchedext_test.go`](../../integration/macosuserpatchedext_test.go) pass |
| <a id="PPX-D46"></a>PPX-D46 | **The maintainer's ruling, 2026-10-17:** a macos-user build, a fork's and a patched extension's alike, runs with the platform's own base system: its script (the toolchain record, the build line and a tree's subshell) runs under macOS's `/bin/bash` 3.2, with the BSD tools. A recipe must work on the platform it builds for. yolo supplies a tool from its own floor only when the base system lacks it outright, never to replace a base tool with a GNU or newer version: *"We're only compiling things meant for the platform they came from, so it sure better work on a Mac. ... if you start deciding this build environment based on this, we're going to have this whole GNU versus BSD ... thing ... with other utilities also. ... it would be different if ... the build depends on some dependency ... because Apple's base system doesn't work, and we need to pull that in."* This reverses a same-day decision that ran the script under the darwin floor's bash. Found on the maintainer's Mac: a build line using `shopt -s globstar`, which needs bash 4, built in a container and failed on macos-user; that was the recipe's bug, fixed in its pack |
| <a id="PPX-D47"></a>PPX-D47 | A build whose build line ran and failed is never said as a build jail that did not start: the toolchain record the script writes first is the witness, so the macos-user build act moves it beside the store whether or not the build succeeded, as a container build's workspace keeps it, and the host's reason says the build line ran and failed, its build jail's exit and the build's own log, inside the headline a launch's refusal keeps. Found on the maintainer's Mac: a build line that failed under bash 3.2 was said as "its build jail refused to start on the host" |
| <a id="PPX-D48"></a>PPX-D48 | A sealed build jail prints no line meant for an agent session, on every backend: the macos-user build act's bootstrap is a sealed build's, told by `darwin-bootstrap --sealed-build` on its argv (never the environment, which carries the user's channel), so it renders no pack surface, names no ownerless overlay, list or autonomy entry, and says nothing of `mcp_presets`; and no sealed launch prints the shared-network line. A decision, not a quiet mode: [OQ-RO3](report-tiers.md#why-its-this-way)'s "a launch has no quiet mode" is about the user's launch, and a build jail is none — it runs no agent, and its build's start line already discloses the seal and the network the build reaches. The read and exec disclosures of the packs it runs stay. Found on the maintainer's Mac: every build jail printed the `mcp_presets` warning, eight "no owner" lines and the network line |
| <a id="PPX-D49"></a>PPX-D49 | The sealed macos-user build act runs its build line in a process group of its own and ends that group when the line exits, whatever its result — SIGTERM, a short grace, then SIGKILL — sent by the capture driver, which runs as the sandbox account through the one run-as rule (`capture-run --end-process-group`); the build's script also turns npm's update check off, as a container jail has it. A child that starts a session of its own is outside the group and is not ended. So the act's guest pack tree is removed as on a normal exit. Found on the maintainer's Mac: a failed build left a sandbox-account process alive past the act's three-second wait, and the tree was kept for `yolo prune` |
| <a id="PPX-D50"></a>PPX-D50 | A failed build's reason says which part failed, on every backend: the capture driver records the build line's own exit status the moment it exits (`capture-run --line-status`, beside the toolchain record; the macos-user act moves it beside the store as it moves the record), so the host says "the build line ran and failed with exit status N" with the line's real status, "the build line succeeded, then capturing its output failed" when the driver failed after it, and with no record claims neither. Before, both read as the driver's exit, always 1, and as a line that ran and failed. Every failed build's reason keeps the path of the build's own log in the headline a launch or a launcher says: the headline is cut at the first " — " outside parentheses, since the reason carries the build's error, which may say " — " itself (an empty result, each admission check), and the log's path in parentheses. A macos-user build's start line says it runs on the Mac's own network, never "the network of the jail it is launched from": the build runs as the sandbox account on the Mac, with no network namespace. Found in the review of [PPX-D47](#PPX-D47)–[PPX-D49](#PPX-D49) |
| <a id="PPX-D51"></a>PPX-D51 | *Implementation decision.* A macos-user launch's copy of a patched extension is made once per build, in the workspace's `AGENTS_DIR/<cname>/tree-copies` keyed by store entry and prefix, renamed into place only once whole and checked against the entry's completion marker, and handed as it is to a later launch of the same build. Liveness, not age, decides what goes: each launch holding a copy keeps a shared lock on its lease for its process's life, and a launch settling a copy removes every other copy of the extension whose lease it can lock exclusively. Forced by an unchanged relaunch copying the whole tree out of the store again only for the home overlay, itself reused on the entry, to read none of it. A container keeps its per-launch copy ([PPX-D22](#PPX-D22)): it mounts the copy for the jail's life |
| OQ-PPX1 | Decided on its leaning (B) under delegation: the good build serves; with none, the owning agent pack's launchers stop before exec and say why; notches that never build trees are exempt. Recorded as [PPX-D18](#PPX-D18), amended by [PPX-D40](#PPX-D40) |
| OQ-PPX2 | Decided on its leaning (A) under delegation: no pin in `packs.lock.json`; a hold is a tag ref or `agent_updates`. Recorded as [PPX-D19](#PPX-D19) |
| OQ-1 | Machine-scoped storage for pi's npm prefix, ruled 2026-09-20 and built 2026-09-21. **Retired 2026-10-05** by [XB-D14](../design/pi-extension-store-builds.md#XB-D14): one jail's install could change what another ran ([§17.1](#171-the-storage-tier-retired)) |
| OQ-2 | Ruled 2026-09-20: yolo resolves and pins pi packages through `internal/packsrc` and `packs.lock.json`; the launcher only materializes, under a non-blocking lock. Only the materializer is built ([§17.4](#174-pis-own-installs)) |
| OQ-3 | Ruled 2026-09-20: leave pi's in-app update notification untouched; the refresh runs before the TUI, so the check passes in the normal path. Nothing to build |
| OQ-4 | Ruled 2026-10-05: accept and document the race between two sessions installing a package the refresh did not reach; it is any pi user's, and stays inside one workspace. Documented in the user guide |
