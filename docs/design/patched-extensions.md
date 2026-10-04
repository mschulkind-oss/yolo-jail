---
title: "A patched fork can follow a pi extension's upstream — the same ratchet, ending in a read-only tree pi loads in place"
date: 2026-10-04
status: in-review
stage: DESIGN
tags: [design, packs, files, pi, extensions, forks, evergreen, git]
summary: "The maintainer asked on 2026-10-04 for the patched-fork mode to cover pi extensions. A `files` contribution may name an upstream `source` and a `patches` series in place of `from`. yolo checks the upstream at most hourly, replays the series on the host exactly as a patched fork does, takes the newest upstream version the series fits, builds it in the sealed capture jail and admits the result as a tree. Each fresh jail launch mounts its own copy of this machine's good build read-only, and pi loads it as a local package through a list entry the pack author writes, so pi never installs or updates it. Two calls are new and the maintainer's: what pi starts with when no build serves, and whether the version is pinned in packs.lock.json; the four patched-fork questions bind this mode too."
next: "Rule OQ-PPX1 and OQ-PPX2 in the sitting that rules patched-forks.md's OQ-PFK1, OQ-PFK3 and OQ-PFK4, since one ruling of those covers both routes"
depends-on:
  - patched-forks.md#OQ-PFK1
  - patched-forks.md#OQ-PFK3
  - patched-forks.md#OQ-PFK4
---

# A patched fork can follow a pi extension's upstream — the same ratchet, ending in a read-only tree pi loads in place

**Status:** 2026-10-04. Nothing built. Evidence read at `48491fd4a`; pi read, not run, from 0.99.1 as
installed in this jail. MEASURED the same day in fresh blobless clones with git 2.55.0, npm 11.17.0
and node v24.19.0: the maintainer's five extension forks replayed onto their upstreams, the
[newest-fit walk](#6-detection-and-the-newest-fit-walk) over each, two trees built, one fork's
`dist/` rebuilt from a series without it, and pi's dependency command run on a toy package.
UNMEASURED: no pi has loaded a tree built this way.

> **In short.** An extension is a tree, not a program. So a patched extension keeps a patched fork's
> whole ratchet and changes only what the build leaves and where it goes: an admitted tree, copied for
> each launch and mounted read-only where pi loads it as a local package it never updates.

**Why it matters.** The maintainer keeps hand-rebased forks of five pi extensions, and three of them no
longer replay onto their upstream ([§3.2](#32-the-maintainers-five-forks)). On 2026-10-03 he asked for
patched forks: *"For the fork stuff that we're currently using for a PI fork, I want another mode
where you can specify a set of patches that will then get applied to the latest of the upstream. So
we can essentially have an evergreen build as long as these patches clean apply. So basically you
will still detect updates in the upstream. When that happens you will fetch the upstream, apply the
patches, and then build so that we don't have to just do essentially clean rebases for every new
upstream version."* On 2026-10-04: *"I want the fork patch thing to cover pi extensions as well."*

**The shape.** A `files` contribution names `source` and `patches` in place of `from`. Patched forks'
check, replay, ratchet and explicit acts run unchanged under an **owner key**
([PF-D22](patched-forks.md#PF-D22)); the sealed build leaves a tree; a fresh launch mounts a copy of
the good build at `into`; and the pack author's list entry points pi at it.

**Cost.** A copy of each tree per fresh launch (free under reflink; on ext4 a full copy, 21 MB and
14 MB for the two trees measured), a launch that waits for a build once per upstream version taken,
and two collisions with standing pi rulings ([OQ-PPX1](#OQ-PPX1), [OQ-PPX2](#OQ-PPX2)). It amends
[PF-D10](patched-forks.md#PF-D10) for both routes.

**Start at [§5](#5-what-carries-over-from-patched-forks)**: what carries over unchanged, and the three
places it cannot, which [§7](#7-the-build-and-the-admit) and [§8](#8-delivery) take.

**Needs your ruling:** [OQ-PPX1](#OQ-PPX1), [OQ-PPX2](#OQ-PPX2). Patched forks'
[OQ-PFK1](patched-forks.md#OQ-PFK1), [OQ-PFK3](patched-forks.md#OQ-PFK3) and
[OQ-PFK4](patched-forks.md#OQ-PFK4) bind this mode as written ([§12](#12-dependencies)).

**Reads with:** [`patched-forks.md`](patched-forks.md) (the mode this extends; every PF term and PF-D
row cited here is its), [`pi-git-extension-caching.md`](pi-git-extension-caching.md) (pi git
extensions as immutable local trees; its [OQ-2](pi-git-extension-caching.md#OQ-2) is what
[OQ-PPX1](#OQ-PPX1) collides with), [`pi-extension-lifecycle.md`](pi-extension-lifecycle.md) (the
pre-launch refresh, and the pin [OQ-PPX2](#OQ-PPX2) weighs), [`pack-pi-resources.md`](pack-pi-resources.md)
(the other route that hands pi a local package), [`patched-forks-plan.md`](patched-forks-plan.md) (the
implementation sketch for both routes, incomplete while questions are open).

---

## 1. Defined terms

Coined here unless a link says otherwise:

- **Patched extension**: a `files` contribution that declares `source` and `patches` in place of
  `from`. Its bytes are the upstream at a commit with the series replayed, built in the sealed capture
  jail. The mechanism is generic: it delivers any home-relative tree, and "extension" names the case
  that motivated it. It is not a patched fork (no bin, nothing on PATH, no base program) and not a pi
  `git:` package (pi never fetches, installs or updates it).
- **Built tree**: the directory a patched extension's build leaves and the capture store admits. Not
  PF's [patched tree](patched-forks.md#53-the-patched-tree), which is the git tree object recorded
  before the build, and not the [tree](pi-git-extension-caching.md#defined-terms) of the caching
  design, which a jail builds into a store every jail can write.
- **Extension key**: `<pack>/<name>`, where `<pack>` is the contributing pack and `<name>` the last
  segment of `into`. It is PF's fork key `<pack>/<bin>` for a tree.
- **Owner key** *(coined in [PF-D22](patched-forks.md#PF-D22))*: the key the check, its record and
  lock, the replay, the ratchet and the explicit acts use: the fork key for a patched fork, the
  extension key for a patched extension.
- **Owning agent pack**: the selected pack one of whose `state` contributions has an `at` that is a
  whole-segment prefix of `into`, the longest such `at` winning. For `.pi/…` that is `pi`, which
  declares `.pi` as workspace state (READ
  [`packs/pi/pack.json:184-188`](../../packs/pi/pack.json#L184-L188)); `.pi-shared-npm` is not a
  prefix of `.pi/agent`. There may be none. It is not necessarily the contributing pack: in the
  maintainer's case `matt` contributes and `pi` owns.
- **Newest fit** *(coined in [`patched-forks.md` §6.4](patched-forks.md#64-the-newest-fit-and-the-first-advance))*:
  the first entry of the walk's list ([§6.2](#62-the-newest-fit-walk)) onto which the series replays
  cleanly. Not the newest version, which the series may not fit.

Every other PF term keeps [`patched-forks.md`](patched-forks.md)'s meaning: series, series digest,
follow rule, check, check stamp, check record, candidate, pending, good build, advance, replay, apply
error, held.

## 2. The verdict, and three more principles

The request, quoted whole above, is the patched-fork request taken one step further: detect upstream
updates, fetch, apply the patches and build, for an extension as for a program.

**Build it as a placement of the patched-fork mode on `files`, sharing one implementation through the
owner key.** Detection, the replay, the ratchet, the record and the explicit acts are patched forks'.
Three things are new: the build leaves a tree, a launch serves a copy of it, and pi learns of it from a
list entry. My read is that "the fork patch thing … as well" asks for the same ratchet, not a second
mechanism with its own failure rules.

[P1 to P5](patched-forks.md#1-the-verdict-and-five-principles) carry over unchanged. Three more:

- **PE6. A launch serves a copy, never the store.** The capture store's reclaimer holds that
  *"RECLAIMING IS NEVER A CORRECTNESS EVENT"* (READ [`gc.go:19-24`](../../internal/capture/gc.go#L19-L24)),
  because nothing runs from an entry itself: a materialized copy survives its entry's removal.
  Binding an entry into a running jail would end that, since removing the entry would empty the
  directory the jail is reading.
- **PE7. Append, never rewrite.** pi learns of a built tree only through an ordinary list entry its
  author writes. yolo rewrites nobody's entry, which [`OQ-LT2`](../reference/pack-system.md#oq-lt2)
  forbids doing after the merge.
- **PE8. Holding the agent holds its extensions.** Today `agent_updates` off for `pi` turns off the
  launcher's pre-launch `pi update --extensions` (READ
  [`prelaunchrefresh.go:140-141`](../../internal/entrypoint/prelaunchrefresh.go#L140-L141)), so a frozen
  pi has frozen extensions. A patched extension keeps that.

## 3. What exists today, precisely

### 3.1 How pi takes an extension, and where yolo can put a tree

pi 0.99.1, `dist/core/package-manager.js` in this jail's install, and the tree at `48491fd4a`:

| Fact | Where | How known |
| :--- | :--- | :--- |
| A `git:` entry is cloned into `~/.pi/agent/git/<host>/<path>` and its dependencies installed with `install --omit=dev --legacy-peer-deps`, no `--ignore-scripts`; a failed install deletes the clone | `package-manager.js:1483-1499`, `:1565-1578` | READ |
| `pi update --extensions` takes every `git:` entry, pinned or not, fetches its ref, and runs `git reset --hard` and `git clean -fdx` in any checkout whose HEAD is not that commit, so a local commit is always erased | `:861-879`, `:1581-1593`, `:1625-1666` | READ |
| A local path is loaded in place, never installed; a path that does not exist is skipped with no message; update collects only npm and git entries | `:1067-1091`, `:861-879` | READ |
| A local package loads its `package.json` `pi` manifest, else its conventional `extensions/`, `skills/`, `prompts/`, `themes/` | `:1848-1886`; resolved this way in pi 0.99.2 by [`pack-pi-resources.md`](pack-pi-resources.md) | READ; MEASURED there |
| Identity is `npm:<name>`, `git:<host>/<path>` or `local:<resolved path>`, so one extension as a `git:` and a local entry loads twice | `:1392-1406` | READ |
| pi serves its own packages and `typebox` to every extension, so a tree carries only its other dependencies | `dist/core/extensions/virtual-modules.js:13-37` | READ |
| npm 11.17.0 runs a package's `prepare` and `postinstall` under pi's dependency argv, and neither with `--ignore-scripts` | a toy package in scratch | MEASURED |
| `files` mounts a staged tree `:ro` at `/home/agent/<into>` with no read-only-floor check, joining `from` onto the pack root unconditionally | [`packfiles.go:93`](../../internal/cli/run/packfiles.go#L93), [`:123-140`](../../internal/cli/run/packfiles.go#L123-L140) | READ |
| A `files` destination on the jail's PATH is refused for every pack | [`contributes.go:3698`](../../internal/packdecl/contributes.go#L3698), [`packdecl.go:813`](../../internal/packdecl/packdecl.go#L813) | READ |
| The capture store is mounted `:ro` at `/ctx/captures`, and not at all below Apple Container's read-only floor, so no fork is built there | [`assemble.go:937-940`](../../internal/cli/run/assemble.go#L937-L940), [`captures.go:72`](../../internal/cli/run/captures.go#L72), [`run/forkbuild.go:63-68`](../../internal/cli/run/forkbuild.go#L63-L68) | READ |
| Materialize tries reflink, then hardlink, then copy; only a hardlink shares the store's inode, whose files are frozen read-only | [`materialize.go:28-54`](../../internal/capture/materialize.go#L28-L54), [`store.go:340`](../../internal/capture/store.go#L340) | READ |
| A fork's sealed build runs `capture-run` with the full content-reference scan, captures only the program surfaces (`.npm-global`, `.local`, `go`, and Codex's standalone payload), and refuses an empty delta and a link into its own workspace | [`cli/forkbuild.go:145`](../../internal/cli/forkbuild.go#L145), [`:244`](../../internal/cli/forkbuild.go#L244), [`:388-399`](../../internal/cli/forkbuild.go#L388-L399), [`paths.go:649-670`](../../internal/paths/paths.go#L649-L670), [`capture/inner.go:290`](../../internal/capture/inner.go#L290) | READ |
| `source`, `build` and `produces` are refused on every kind but a `program` delivered `via: "source"` | [`fork.go:47-88`](../../internal/packdecl/fork.go#L47-L88) | READ |
| The image puts Node and npm at `/bin`, outside every pack: nodejs 24.20.0 in this jail's image | [`flake.nix:1257`](../../flake.nix#L1257); `/bin/node --version`, `/bin/npm`'s link | READ; MEASURED |
| An attach compares each pack's staged content digest with the running jail's | [`run/packtree.go:316-317`](../../internal/cli/run/packtree.go#L316-L317) | READ |
| A pack tree is per launch, and goes once its container is known gone; the boot's fallback walk reads every directory at a tree's top level as a pack | [`paths/packtree.go:5-21`](../../internal/paths/packtree.go#L5-L21), [`trackingcleanup.go:64`](../../internal/cli/run/trackingcleanup.go#L64), [`packtreerecord.go:22-27`](../../internal/packload/packtreerecord.go#L22-L27) | READ |
| The host `files` render copies file by file under an ownership record, and skips a contribution with no `from` | [`hostfilestree.go:59-89`](../../internal/entrypoint/hostfilestree.go#L59-L89) | READ |
| The footprint shows a `files` tree as *"read-only tree"*, not marked for review | [`footprint.go:489-498`](../../internal/packload/footprint.go#L489-L498) | READ |
| An autonomy posture carries config, launch flags and lists, never `files` | [`contributes.go:1361-1384`](../../internal/packdecl/contributes.go#L1361-L1384) | READ |

Four consequences, INFERRED from those rows:

- **Patched bytes cannot live in pi's git checkout**, which the hourly refresh resets and cleans.
- **A built tree must arrive complete**, dependencies and build output included, because a local path
  is the one form pi neither installs nor updates.
- **A tree mounted where pi looks needs no store mount at all**, so it reaches backends the fork route
  cannot.
- **A sealed build of an extension needs no agent pack for its toolchain**, since `npm` is the image's.
  The fork fields' placement refusal is the one rule that has to move.

### 3.2 The maintainer's five forks

Each fork's commits since its merge base, exported with `git format-patch --base`, then replayed by
[PF-D6](patched-forks.md#PF-D6)'s replay. MEASURED 2026-10-04; owners are left out on purpose.

| Extension | Members | Series base | Onto the newest version and the head | Newest fit ([§6.2](#62-the-newest-fit-walk)) | Build it needs |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `pi-subagents` | 7 | untagged, 10 past v0.71.0 | conflict at member 1, `src/tui/fleet-status.ts` (v0.75.0 and head) | none of 6 versions above the base: the base | dependencies only |
| `pi-dynamic-workflows` | 22, 17 carrying `dist/` | v3.13.0 | conflict at member 4, `src/agent.ts` (v3.13.1, the head) | v3.13.0, the base | `tsc`: upstream ignores `dist/`, which `extensions/workflow.ts` imports |
| `pi-archimedes` | 11 | v2.8.0 | conflict at member 1, `packages/session-name/src/index.test.ts` (v2.9.0, the head) | v2.8.0, the base | dependencies only |
| `pi-automode` | 4 | the head, 2 past v1.17.0 | clean | no version contains the base | dependencies only |
| `pi-background-tasks` | 1 | the head; no tags | clean | no versions | none |

For all five, the base with the series applied is byte-for-byte the fork's tree (MEASURED, by tree
object). The longest walk was `pi-subagents`' six replays, 2.15 s with blobs fetched lazily into a
fresh blobless clone (MEASURED). The maintainer's own pack lists all five as `git:` entries, with
`pi-automode` in a guarded-only posture list, which reaches the host and no jail (READ, its staged
copy at `/ctx/packs/matt/pack.json`, and [`contributes.go:1365-1369`](../../internal/packdecl/contributes.go#L1365-L1369)).

## 4. The declaration

The maintainer's own pack, two of the five rewritten:

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
| `into` | Required, home-relative. On the jail's PATH it is refused, as for every `files` destination today: something built and placed on PATH is a `program`, and a patched fork delivers one. `.pi/agent/yolo-patched/` is the examples' choice; core reserves no directory |
| `source`, `follow` | As [PF-D1](patched-forks.md#PF-D1), [PF-D3](patched-forks.md#PF-D3) and [PF-D4](patched-forks.md#PF-D4): the upstream, a mandatory `?ref=`, and a branch followed by `release`, `release:<prefix>` or `head` |
| `patches` | Required; the series rules of [PF-D2](patched-forks.md#PF-D2). Zero patches would be a plain `git:` entry, which is [PF alternative H](patched-forks.md#11-alternatives-with-verdicts)'s verdict |
| `build` | Optional: one command line run by bash in the checkout in the sealed jail, as a fork's. Without it no command runs, but the tree still passes through the sealed jail and the admit, so every built tree has one admit path |
| `produces` | Optional: tree-relative paths the build must leave |
| `from`, `fork_of`, `agent`, `agents` | Refused beside `source`, each naming why: `from` names the pack's own tree, `fork_of` a program, and `agent`/`agents` a slot whose landing core picks |

On `files`, `source`, `follow`, `build` and `produces` are accepted only beside `patches`, so a `files`
contribution with `build` and no series is still refused, with the fork fields' message. The extension
key is unique per pack across its patched extensions and its patched programs' bins, and a duplicate
is refused naming both declarations. Two packs landing a tree at one `into` are what any two `files`
claims on one path are today, a collision `yolo pack footprint` reports (READ
[`footprint.go:905-956`](../../internal/packload/footprint.go#L905-L956)); this design adds no rule
there. Validation is static, and the series' contents are read at each advance, as for a patched fork.

**Where to land it.** Outside every directory the agent discovers by itself. For pi that means outside
`~/.pi/agent/extensions/`, where a subdirectory loads only through its manifest's extensions or an
index file ([`pack-pi-resources.md`](pack-pi-resources.md#1-what-pi-loads-from-where-and-in-what-form)),
so skills and prompts would be lost and a list entry beside it would load it twice. `pi-subagents` and
`pi-dynamic-workflows` both declare skills (READ, their `package.json` at v0.75.0 and v3.13.0). Core
knows no agent's paths, so this is guidance, not a check.

## 5. What carries over from patched forks

| PF decision | For a patched extension |
| :--- | :--- |
| [PF-D1](patched-forks.md#PF-D1) | Replaced by [PPX-D1](#PPX-D1): the placement is `files`, not `program` |
| [PF-D2](patched-forks.md#PF-D2), [D3](patched-forks.md#PF-D3), [D4](patched-forks.md#PF-D4) | Unchanged: the series, the ref and `follow`, what a version is |
| [PF-D5](patched-forks.md#PF-D5) | Generalized: the triggers are [§6.1](#61-where-the-check-runs)'s |
| [PF-D6](patched-forks.md#PF-D6), [D18](patched-forks.md#PF-D18) | Unchanged: the replay, its config regime, the blob prefetch |
| [PF-D7](patched-forks.md#PF-D7) | Generalized: the recipe and the selection are [§7.2](#72-identity-and-selection)'s |
| [PF-D8](patched-forks.md#PF-D8) | Unchanged: the ratchet, its compare-and-swap and its disclosure line, unless [OQ-PPX1](#OQ-PPX1) is ruled C |
| [PF-D9](patched-forks.md#PF-D9) | Unchanged, keyed by the extension key |
| [PF-D10](patched-forks.md#PF-D10) | Amended for both routes on 2026-10-04: every advance takes the newest fit ([§6.2](#62-the-newest-fit-walk)) |
| [PF-D11](patched-forks.md#PF-D11), [D15](patched-forks.md#PF-D15), [D17](patched-forks.md#PF-D17), [D21](patched-forks.md#PF-D21) | Unchanged |
| [PF-D12](patched-forks.md#PF-D12) | Generalized: `yolo capture <pack>/<name>` builds; `yolo pack update`, `install` and `status` act on the extension key |
| [PF-D13](patched-forks.md#PF-D13) | Unchanged, addressed `yolo pack rebase <pack>/<name>` |
| [PF-D14](patched-forks.md#PF-D14) | Not applicable: the host floor installs programs; the host delivery is [§8.3](#83-at-the-host) |
| [PF-D16](patched-forks.md#PF-D16) | Its principle holds (the good build is machine-local); its fork-lock mechanics do not apply, and `packs.lock.json` is [OQ-PPX2](#OQ-PPX2) |
| [PF-D19](patched-forks.md#PF-D19) | Generalized: a hold comes from the contributing pack or the owning agent pack ([PPX-D9](#PPX-D9)) |
| [PF-D20](patched-forks.md#PF-D20) | Simplified: a move reaps every other build of the key at once, because jails hold copies ([§8.1](#81-in-a-jail)) |
| [PF §10](patched-forks.md#10-migration-from-a-plain-fork) | Not applicable; this mode's migration is [§13](#13-migrating-the-maintainers-five-forks) |

## 6. Detection, and the newest-fit walk

### 6.1 Where the check runs

The check is [PF-D5](patched-forks.md#PF-D5)'s, per extension key: at most once per
`BranchRefreshInterval` (3600 s) from the last attempt, gated by a check stamp read before any git, due
at once when the repository, subdirectory, ref or `follow` it last read changed, and a fetch with no
checkout. It runs at:

- **a fresh jail launch**, in a second arm beside the fork builds in their slot, below every attach
  site and under the launch lock (READ [`run.go:1323-1332`](../../internal/cli/run/run.go#L1323-L1332));
- **the host's `files` render**: `yolo host apply`, and `yolo host -- <agent>` under
  `host_apply_on_launch`, before the gate compares the render in observe posture
  ([§8.3](#83-at-the-host));
- **the explicit acts**, which force it: `yolo pack update`, `yolo pack install`,
  `yolo capture <pack>/<name>` and, under [OQ-PFK4](patched-forks.md#OQ-PFK4)'s leaning,
  `yolo pack rebase <pack>/<name>`.

It never runs on an attach, a dry run, in a jail, in a capture or build jail, or under a hold with a
good build ([PPX-D9](#PPX-D9)). Checks of different keys may run concurrently, since
[PF-D17](patched-forks.md#PF-D17) gives them no shared lock; their lines print in declaration order.

### 6.2 The newest-fit walk

This amends [PF-D10](patched-forks.md#PF-D10) for both routes, and its one normative statement is
[`patched-forks.md` §6.4](patched-forks.md#64-the-newest-fit-and-the-first-advance). In brief: an
advance replays the series down a list, newest first (the branch's tip under `follow: "head"`, then the
version tags merged into the branch that contain the series' base), and takes the first entry it fits.
A conflict is recorded per [PF-D9](patched-forks.md#PF-D9), so no entry is replayed twice for one
series. No fit holds the good build or, on a first advance, builds the base. The walk is bounded at
60 s.

**Why the core changes.** "The newest, else the base" drops a second machine, or an edited series, to
the base even where v1.2 fits and only v1.3 conflicts. Restricting tags to those containing the base
keeps an older version from passing for an update: for `pi-automode` today the newest version,
v1.17.0, is older than the base, and the series replays onto it cleanly (MEASURED,
[§3.2](#32-the-maintainers-five-forks)). Day one for the maintainer does not change: for the three
that conflict, the newest fit is the base ([§3.2](#32-the-maintainers-five-forks)).

## 7. The build and the admit

### 7.1 The build act

It is a fork's build act ([§7 of patched forks](patched-forks.md#7-the-build-and-the-launch)) with two
changes:

- **The seal is narrowed to the contributing pack**, where a fork's is narrowed to the fork and its
  base ([FP-D9](forked-programs-as-packs.md#FP-D9); READ
  [`cli/forkbuild.go:405`](../../internal/cli/forkbuild.go#L405)). The toolchain is the image's
  `/bin/node` and `/bin/npm` ([§3.1](#31-how-pi-takes-an-extension-and-where-yolo-can-put-a-tree)),
  and it is recorded beside the build as a fork's is. A build that needs another Node or another
  package manager says so in its own `build` line.
- **One fixed final step.** After `build`, if any, the jail copies the checkout, links kept as links,
  into a reserved directory under `~/.local` (spelled `~/.local/share/yolo-tree/<name>/` here; the
  spelling is the implementer's). `.local` is one of the surfaces the capture driver already captures
  ([`paths.go:649-655`](../../internal/paths/paths.go#L649-L655)), so the driver is unchanged.

The checkout is [PF §5.1](patched-forks.md#51-where-it-runs)'s replay, copied into the staging
workspace's `src/`. The wait for another build of the same key, the bound (`forkBuildWaitBound`, 20
minutes) and the waiter taking the winner's result are [PF-D17](patched-forks.md#PF-D17)'s; whether the
launch waits at all is [OQ-PFK3](patched-forks.md#OQ-PFK3).

**The admit** keeps the empty-delta refusal and the link check (`linksIntoTheBuild`), and adds three:

- **Every `produces` path exists** in the tree.
- **Nothing in the delta lies outside the reserved directory.** Strays are named, and the build fails:
  the tree would not carry them. A tool that keeps a store under a captured surface (pnpm's default
  store is under `~/.local/share`, INFERRED from its XDG default and not measured here) is pointed at
  the workspace by the build line.
- **The full content scan finds no reference to the build jail's home.** The tree lands at another
  path in every jail and at the host, so a tree with one is a failed build naming the files. The
  program route's relocation cannot rescue it: relocation rewrites one home prefix into another
  (READ [`relocate.go:1-21`](../../internal/capture/relocate.go#L1-L21)), and a tree also moves
  within the home, from the reserved directory to `into`.

Each failure is a failed build with [PF-D9](patched-forks.md#PF-D9)'s back-off, and the good build
serves.

### 7.2 Identity and selection

- **The revision is the upstream commit**, as [PF-D7](patched-forks.md#PF-D7) has it.
- **The recipe is `["tree", build, sorted produces, subdir, series digest]`.** The `"tree"` tag means a
  tree's recipe can never equal a program's.
- **The `build` receipt** gains PF-D7's fields, with the owner key where a fork's names its fork key.
- **Selection is by extension key and platform, then an exact lookup** of repository, subdirectory,
  commit and recipe. `yolo prune`'s rule is unchanged, as it is for a patched fork
  ([PF §6.3](patched-forks.md#63-what-is-served-and-the-store-key)): it keeps the newest build per
  selection key, which is the good build except after a crash between an admit and the record's
  write. Reaping that one costs a rebuild and never a running jail's tree ([§8.1](#81-in-a-jail)).

**The series lint.** A member that adds a path the upstream's `.gitignore` ignores at the base is said
once, at `yolo pack lint` and the advance, naming `build` as the place that output comes from. In the
`pi-dynamic-workflows` fork, 17 of 22 members carry `dist/`, which upstream ignores (MEASURED; READ
`v3.13.0:.gitignore`).

## 8. Delivery

### 8.1 In a jail

1. **At the fork-build slot, after any advance, the good build is materialized** into a per-launch
   directory beside the launch's pack tree: reflink, else copy, never a hardlink. A hardlink would share
   the store's inode, and a jail that can write its mount (below Apple Container's read-only floor)
   could then change the store.
   - Never inside a pack's staged directory: the attach's digest would report that pack changed on
     every attach ([`run/packtree.go:316-317`](../../internal/cli/run/packtree.go#L316-L317)).
   - Never at the tree's top level, which the boot's fallback walk reads as packs.
   - It goes with the tree, once the container is known gone, or with the workspace's staging by the
     orphan reaper.
2. **The `files` emitter mounts that copy at `/home/agent/<into>:ro`.** No store is mounted for it.
3. **The jail learns what it got** from `YOLO_PATCHED_TREES`, read once at boot: extension key to
   `into` and the handed build, or the reason there is none. It is a sibling of the bin-keyed
   `YOLO_FORK_BUILDS` ([`forklauncher.go:40`](../../internal/entrypoint/forklauncher.go#L40)), not an
   overload of it.
4. **An attach names the build its jail was handed**, from the record each fresh launch leaves beside
   its tree ([PF-D20](patched-forks.md#PF-D20)), and says when a newer good build waits for the next
   fresh launch.

A move reaps every other build of the key at once: no jail reads the store for a tree, so no running
jail's copy can be stranded. A launch that finds its entry reaped between the lookup and the copy
re-reads the record once.

### 8.2 How pi finds it

- **The author writes `~/<into>` in pi's `packages` list**, with no trailing slash, and drops the
  extension's `git:` entry in the same edit. pi loads it as a local package: never installed, never
  updated, untouched by the pre-launch refresh, with the package's skills and prompts.
- **The pi pack's subagents MCP render still fires.** Its pattern matches
  `~/.pi/agent/yolo-patched/pi-subagents` and does not match it with a trailing slash (MEASURED with
  node, against [`packs/pi/pack.json:140`](../../packs/pi/pack.json#L140)).
- **Two lints**, at `yolo pack lint` and once at launch, each a warning naming the line to change:
  - no `config-list` or posture-list entry in the contributing pack equals `~/<into>`, so the tree is
    mounted and pi never loads it;
  - the same list carries another entry whose last segment, less any `@ref` and `.git`, is `<name>`,
    so pi would load both.

### 8.3 At the host

On Linux, the readiness act is the `files` render, and a patched extension gets an arm of its own: the
existing render copies file by file and skips a contribution with no `from`
([`hostfilestree.go:59-89`](../../internal/entrypoint/hostfilestree.go#L59-L89)), and `pi-subagents`'
tree alone is 1,386 files (MEASURED).

- **The check and any advance run first**, before `yolo host -- <agent>`'s gate compares the render in
  observe posture, so the comparison sees the build the apply would install. They run outside that
  comparison, never inside it: the comparison is the apply itself with its writes withheld, bounded
  at one second as a stuck-detector (READ
  [`hostapplygate.go:62-72`](../../internal/cli/hostapplygate.go#L62-L72)), and a check alone may
  wait 60 s on the network. Inside the comparison the patched arm only reads which build the link
  names.
- **The good build is materialized into a host-private versioned directory**, one per build key, in
  yolo's state directory where no jail mounts it.
- **`~/<into>` is a symbolic link the render owns**, recorded in the `files` ownership record and
  swapped by writing a new link beside it and renaming it over.
- **The previous version is kept until the next move**, so a host pi already running keeps reading the
  tree it loaded. INFERRED: jiti 2.7.0, pi's loader, resolves a loaded file to its real path (READ,
  minified `jiti.cjs`), as [the caching design's §3.7](pi-git-extension-caching.md#37-running-sessions-and-moving-pointers)
  also infers; UNMEASURED with pi.
- **A revert removes the link** as it removes any path the record says yolo wrote, and the versioned
  directories with it.

A `pi` started outside yolo at the host finds whatever the link names, and with no link pi skips the
path silently. **macOS host:** none, since the build is the jail's Linux platform; a line says so once.

## 9. Failure, and the next step

[PF §8.1](patched-forks.md#81-the-failure-table) holds, with these changes:

| Failure | What serves | Said, with the next step |
| :--- | :--- | :--- |
| A newer upstream does not replay or build | the good build ([PF-D8](patched-forks.md#PF-D8)), unless [OQ-PPX1](#OQ-PPX1) is ruled C | the held suffix, naming `yolo pack rebase <pack>/<name>` |
| A `produces` path is missing, the tree names the build's home, or the delta has strays | the good build; a failed build with back-off | the paths, and the build line as the fix |
| The entry was reaped between the lookup and the copy | the record re-read once; else rebuilt from its inputs | as a first build |
| The per-launch copy fails (a full disk) | nothing for this launch; the store is untouched | the error and the extension |
| Nothing serves | [OQ-PPX1](#OQ-PPX1) | see below |

**Nothing serves.** Under [OQ-PPX1](#OQ-PPX1)'s leaning, the owning agent pack's launchers, the base's
and any fork's, stop before exec, naming the extension, the cause and the next step, and the jail's
shell stays up. Notches that never build trees (macos-user, the macOS host) start pi with a line said
once, in [FP-D3](forked-programs-as-packs.md#FP-D3)'s shape. With no owning agent pack, the launch says
it and nothing stops. A mountpoint podman made on an earlier launch may be left empty at `~/<into>`
(INFERRED: `.pi` is a workspace state directory on the host). pi does not skip an empty directory
as it skips a missing path: it tries to load the directory itself as one extension, which fails
(READ `package-manager.js:1079-1085`;
[`pack-pi-resources.md` §1](pack-pi-resources.md#1-what-pi-loads-from-where-and-in-what-form)).
Under option A the launch's line has to name it.

**The jail launch itself is never refused**, so [PF §6.7](patched-forks.md#67-what-the-mode-never-does)
holds.

## 10. Trust and disclosure

- **The series is data on the host** ([P3](patched-forks.md#1-the-verdict-and-five-principles)). An
  upstream's install and build scripts run only in the sealed, credential-free jail. That is tighter
  than today's `git:` route, where pi's dependency step runs them inside the user's jail
  ([§3.1](#31-how-pi-takes-an-extension-and-where-yolo-can-put-a-tree)).
- **The footprint marks a patched extension for review**, naming the source, ref, follow rule, series,
  build and landing, in place of *"read-only tree"*.
- **No flag hides the launch line, the move line or the held suffix**
  ([`OQ-RO3`](../reference/report-tiers.md#why-its-this-way)). A launch line reads, for example:
  *"matt/pi-subagents: held at 6f1027f7 (the series' base) + 7 patches; upstream v0.75.0 does not take
  0001-….patch — `yolo pack rebase matt/pi-subagents`"*.
- **Stated, not gated.** A fetched pack's pin fixes its series and the upstream's address, not the
  upstream's bytes, which move hourly. That is true of patched forks too, and under
  [`OQ-TP9`](trust-paths.md#decision-ledger) the boundary is disclosure.

## 11. Notch coverage

| Notch | Patched extension |
| :--- | :--- |
| **jail, podman** | yes |
| **jail, Apple Container** | delivery at every version: the mount is a per-launch copy, so below the read-only floor, where `:ro` is ignored, a write changes only that copy, as for any `files` tree. Builds as [PF §9](patched-forks.md#9-notch-coverage) says: a capture jail cannot start beside a running jail (INFERRED there), so a candidate stays pending and the good build serves |
| **jail, macos-user** | none: no sealed build there ([FP-D3](forked-programs-as-packs.md#FP-D3)); the line names `YOLO_RUNTIME=podman`, as a fork's does |
| **`yolo host`, Linux** | yes ([§8.3](#83-at-the-host)) |
| **`yolo host`, macOS** | none |
| **guest** | unbuilt, as every verb there is |

## 12. Dependencies

**Needed:**

- [Patched forks' steps 1 and 2](patched-forks.md#14-what-i-would-build-in-order), built over the owner
  key and with the walk.
- Rulings on patched forks' three blocking questions, whose leanings read the same here:
  - [OQ-PFK1](patched-forks.md#OQ-PFK1)'s B also fits the caching design's
    [OQ-2](pi-git-extension-caching.md#OQ-2);
  - under [OQ-PFK3](patched-forks.md#OQ-PFK3)'s A the wait is mostly the build jail's boot, since the
    measured builds took 0.3 s and about 7.4 s;
  - [OQ-PFK4](patched-forks.md#OQ-PFK4)'s verb takes the `<pack>/<name>` address.
- [OQ-PFK2](patched-forks.md#OQ-PFK2) only picks a default, but `pi-background-tasks` has no tags, so
  it needs `follow: "head"` whatever the default is.

**Not needed:**

- **The held extension store.** It can coexist: its rewrite passes local entries through untouched
  ([caching §3.2](pi-git-extension-caching.md#32-pointing-pi-at-a-tree)). Its store is writable by every
  jail ([caching §4](pi-git-extension-caching.md#4-invariants-and-done-conditions)), so nothing in it
  may ever feed a host build.
- **[OQ-6](pi-git-extension-caching.md#OQ-6) and [`OQ-LT2`](../reference/pack-system.md#oq-lt2)**:
  nothing is rewritten (PE7).
- **[OQ-PR1](pack-pi-resources.md#OQ-PR1).** Its slot registers one tree per pack; if it ships, a
  registration per contribution could later write the list entry the author writes by hand here.

## 13. Migrating the maintainer's five forks

For each extension:

1. Export the series: `git format-patch --base=$(git merge-base <upstream>/main HEAD) -o <pack>/patches/<name> <upstream>/main..HEAD`.
   - For `pi-dynamic-workflows`, append `-- . ':(exclude)dist'` and declare `build`. MEASURED: the
     export keeps all 22 members and its `base-commit:` line, replays at its base, and the declared
     `build` then rebuilds the fork's committed `dist/` byte for byte, 118 of 118 files.
   - For `pi-background-tasks`, declare `follow: "head"`: its upstream has no tags
     ([§12](#12-dependencies)).
2. In one edit, add the `files` contribution and replace the extension's `git:` entry with
   `~/<into>`. Leaving both loads it twice.
3. Launch.

**Day one**, from [§3.2](#32-the-maintainers-five-forks): base plus series is each fork's tree today.

- `pi-automode` and `pi-background-tasks` run their base, which is their upstream's head.
- `pi-subagents`, `pi-dynamic-workflows` and `pi-archimedes` are held at their base, which is
  exactly the fork's current tree, and the held suffix names `yolo pack rebase`.
- `pi-automode`'s list entry is guarded-only, and a posture cannot carry `files`, so every jail
  builds and mounts a tree its pi never loads, and the Linux host, where the guarded list reaches,
  loads it: minor.

**An older yolo** refuses the manifest at the host, as it refuses a patched fork's, and
[PF §10](patched-forks.md#10-migration-from-a-plain-fork)'s mitigations apply: a pack author keeps
the `git:` manifest where existing users point and publishes this one on a new ref.

## 14. Alternatives, and what this does not cover

| Alternative | Verdict |
| :--- | :--- |
| **A. The program route**, a `program` with `fork_of` | **Rejected.** An extension has no bin and no base program, and the fork fields are refused off `program` with `via: "source"` ([`fork.go:67-70`](../../internal/packdecl/fork.go#L67-L70)) |
| **B. Patch pi's own git checkout** | **Rejected.** The hourly refresh's update cleans and resets it ([§3.1](#31-how-pi-takes-an-extension-and-where-yolo-can-put-a-tree)) |
| **C. Patch the published npm package** | **Rejected.** Some are build output: `pi-subagents`' published 0.75.0 names `./index.js`, its git source `./index.ts` (MEASURED), so a series written against the source has nothing to apply to |
| **D. Land in `extensions/<name>`** | **Rejected.** It loses skills and prompts, and loads twice beside a list entry ([§4](#4-the-declaration)) |
| **E. A new contribution kind** | **Rejected.** The delivery is `files`' read-only mount; a kind would be a second path to it |
| **F. Rewrite the `git:` entry automatically** | **Rejected for now.** It needs [OQ-6](pi-git-extension-caching.md#OQ-6) and an [`OQ-LT2`](../reference/pack-system.md#oq-lt2) amendment (PE7) |
| **G. The held extension store as the base**, a replay in the jail into its per-commit trees | **Rejected.** It replays in the jail (against P3), runs git on every launch (against P4), stops pi when a new build fails (no ratchet), needs [OQ-6](pi-git-extension-caching.md#OQ-6), has no host delivery, and keeps its records in a store every jail can write. Its walk idea is kept ([§6.2](#62-the-newest-fit-walk)) |
| **H. Bind the store entry read-only, with no copy** | **Rejected.** Reaping becomes a correctness event (PE6), prune must learn about running jails, and below Apple Container's floor no store is mounted |
| **I. A check record per declaration** | **Rejected.** Packs are user-scope, one pack serving every workspace (READ [`paths.go:921-922`](../../internal/paths/paths.go#L921-L922)), and the exact lookup already keeps each series' build its own |
| **J. Copy into the pack's staged directory** | **Rejected.** Every attach would report the pack changed ([§8.1](#81-in-a-jail)) |

**Not covered:**

- **A tree with no build, admitted without a jail.** It would reach macos-user; deferred.
- **Writing the list entry for the author.** It waits on [OQ-PR1](pack-pi-resources.md#OQ-PR1).
- **Moving a tree inside a running jail.** A running jail keeps what it booted with.
- **A `files` contribution scoped to a posture or notch.**
- **Resolving conflicts, and writing the series.** As [patched forks §13](patched-forks.md#13-what-this-does-not-cover).

## 15. Risks and costs

| Risk | Mitigation |
| :--- | :--- |
| A clean pick that is wrong code | As for patched forks: the build is the only automatic check; every move is disclosed; a hold is one ref edit or one `agent_updates` key |
| Unreviewed upstream code runs inside pi's process | The same exposure today's `git:` entries carry, minus their scripts running in the user's jail ([§10](#10-trust-and-disclosure)) |
| An extension in a long-lived jail moves only at a fresh launch, where today's hourly refresh moves it in a running jail | Stated; a fresh launch is the readiness act ([PF-D5](patched-forks.md#PF-D5)) |
| An extension that writes into its own directory fails on the read-only mount | Said by pi as the extension's error; the fix is the extension's |
| `pi-archimedes` takes its core package from the registry: the fork's root depends on `@pi-archimedes/core` `2.8.0` and declares no npm `workspaces`, and one series member patches `packages/core` (MEASURED), so under npm that member never reaches runtime (INFERRED) | The build line must build and link the workspace package; a lint cannot see this |
| A native module built against the jail's Node runs under another at the host | The toolchain is on the receipt; pi reports the load error |
| No pi has loaded a tree that carries its own `node_modules` | The first step of [§16](#16-what-i-would-build-in-order) measures it in a nested jail |

**Costs**, MEASURED 2026-10-04 unless marked:

- **Replay:** at most 0.63 s for one series' first replay, and 2.15 s for the longest walk from a fresh
  blobless clone.
- **Trees:** 21 MB and 1,386 files for `pi-subagents` (0.3 s to install), 14 MB and 302 files for
  `pi-dynamic-workflows` at its base (3.8 s `npm ci`, 3.0 s `tsc`, 0.6 s prune).
- **Per-launch copy:** about free under reflink; this jail's workspace and home are btrfs (MEASURED,
  `stat -f`; the host's being the same, and the cost, are INFERRED from
  [`materialize.go`](../../internal/capture/materialize.go)). Under ext4, a full copy per fresh
  launch.
- **Cadence:** 13 tags dated in the 14 days to 2026-10-04 across the four tagged upstreams, so at
  most about one build a day under `release`, fewer where the series conflicts.

## 16. What I would build, in order

1. **Patched forks' steps 1 and 2** with the owner key and the walk, and a nested-jail run in which a
   real pi loads a hand-made tree with its own `node_modules` from a local entry. That run is a
   human's check, not a test: no automated test starts pi (AGENTS.md, Testing).
2. **The declaration**: validation, the extension key, the lints and the footprint claim.
3. **The tree build**: the final copy, the admit's three checks, the recipe and the receipt.
4. **Jail delivery**: the per-launch copy, the mount, `YOLO_PATCHED_TREES`, the attach line and the
   launchers' reason.
5. **The host render**: the versioned directories and the owned link.
6. **Migrate the five**, and verify in a nested jail.

Every call site gets a test that fails when the call site is deleted: the trigger's arm, the mount's
source, the host link and the launchers' gate. The traps the implementation must avoid are in the
[sketch](patched-forks-plan.md#patched-extensions).

**Done looks like:**

- pi lists the built tree's tools and skills, checked by hand, and the tree is read-only in the
  jail.
- A second launch inside the hour runs no git.
- A conflict is said once, and the good build runs.
- `pi update --extensions` leaves the tree byte-identical.
- Neither `yolo prune` nor a move changes a running jail's copy.
- An attach never reports that the pack set differs.
- The pi pack's subagents MCP file is still written.
- `forks.lock.json` holds no entry for a tree.

## Open Questions

Patched forks' [OQ-PFK1](patched-forks.md#OQ-PFK1) to [OQ-PFK4](patched-forks.md#OQ-PFK4) bind this
mode as written, and one ruling of each covers both routes ([§12](#12-dependencies)).

1. 💬 **OQ-PPX1: When a patched extension's build cannot be had, what does pi start with?**

   Patched forks keep the good build when a newer upstream fails ([PF-D8](patched-forks.md#PF-D8));
   the caching design's [OQ-2](pi-git-extension-caching.md#OQ-2) starts pi on nothing but what its
   config calls for ([§9](#9-failure-and-the-next-step)).

   - **A — pi always starts**, without the extension when nothing serves, and the launch says so.
   - **B — The good build serves; with none, pi's launchers stop before exec.** Notches that never
     build trees are exempt.
   - **C — Strict [OQ-2](pi-git-extension-caching.md#OQ-2).** pi stops whenever the newest fit is not
     built and admitted.

   Under B or C, a fresh offline machine, or Apple Container beside a running jail, starts no pi
   until a first build lands; under A the extension can go missing unnoticed.

   <!-- vantage: question id=OQ-PPX1 leaning="B — a good build is a complete, admitted, immutable build of this machine's own, not another launch's leftover, so serving it is not what OQ-2 forbade; nothing to serve is exactly that ruling's case; and a forked pi with nothing to serve does not run either." -->

   _Leaning:_ B — a good build is a complete, admitted, immutable build of this machine's own, not
   another launch's leftover, so serving it is not what [OQ-2](pi-git-extension-caching.md#OQ-2)
   forbade; nothing to serve is exactly that ruling's case; and a forked pi with nothing to serve does
   not run either.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-PPX2: Is a patched extension's version pinned in `packs.lock.json`?**

   [`pi-extension-lifecycle.md` OQ-2](pi-extension-lifecycle.md#OQ-2) ruled that yolo resolves and pins
   pi packages through `packs.lock.json`; that pin is unbuilt. The request asks for an evergreen build,
   and [PF-D16](patched-forks.md#PF-D16) keeps a patched fork's good build off every lock that travels.

   - **A — No pin.** The good build is machine-local; a hold is a tag ref or `agent_updates`.
   - **B — A pin**, moved only by `yolo pack update`; a second machine builds that exact commit.

   <!-- vantage: question id=OQ-PPX2 leaning="A — lifecycle OQ-2's purpose, putting the version choice and a rollback in yolo, is met by the check and the ratchet; a pin that moves at every admit is not a pin; and B brings back the cross-machine coupling PF-D16 removes." -->

   _Leaning:_ A — [lifecycle OQ-2](pi-extension-lifecycle.md#OQ-2)'s purpose, putting the version
   choice and a rollback in yolo, is met by the check and the ratchet; a pin that moves at every admit
   is not a pin; and B brings back the cross-machine coupling PF-D16 removes.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

Every row is an implementation decision made in drafting, reversible, and none is built. The calls
that are the maintainer's are [OQ-PPX1](#OQ-PPX1) and [OQ-PPX2](#OQ-PPX2), above. The two core
changes this design makes to patched forks are recorded there:
[PF-D10](patched-forks.md#PF-D10), amended, and [PF-D22](patched-forks.md#PF-D22).

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="PPX-D1"></a>PPX-D1 | *Implementation decision.* **A patched extension is a `files` contribution with `source` and `patches` in place of `from`; there is no new kind.** `from`, `fork_of`, `agent` and `agents` are refused beside `source`, and `patches` is required | 2026-10-04 | [§4](#4-the-declaration) | — |
| <a id="PPX-D2"></a>PPX-D2 | *Implementation decision.* **The extension key is `<pack>/<last segment of into>`, unique per pack across its patched extensions and its patched programs' bins; every record, lock, selection, message and explicit act uses it** | 2026-10-04 | [§1](#1-defined-terms), [§4](#4-the-declaration) | — |
| <a id="PPX-D3"></a>PPX-D3 | *Implementation decision.* **`into` is required and may not land on PATH, for any pack; `build` is optional and the sealed jail runs either way; `produces` is optional and tree-relative** | 2026-10-04 | [§4](#4-the-declaration) | — |
| <a id="PPX-D4"></a>PPX-D4 | *Implementation decision.* **The owning agent pack is the selected pack one of whose `state` contributions has an `at` that is a whole-segment prefix of `into`, the longest such `at` winning; there may be none** | 2026-10-04 | [§1](#1-defined-terms) | — |
| <a id="PPX-D5"></a>PPX-D5 | *Implementation decision.* **The build is a fork's build act with the seal narrowed to the contributing pack and one fixed final step that copies the checkout into a reserved directory under `~/.local`; the admit adds the `produces` paths, no stray delta and no reference to the build home to the empty-delta and link checks** | 2026-10-04 | [§7.1](#71-the-build-act) | — |
| <a id="PPX-D6"></a>PPX-D6 | *Implementation decision.* **The recipe is `["tree", build, sorted produces, subdir, series digest]`; selection is by extension key and platform with an exact lookup; the receipt gains PF-D7's fields; `yolo prune`'s newest-per-selection-key rule is unchanged** | 2026-10-04 | [§7.2](#72-identity-and-selection) | — |
| <a id="PPX-D7"></a>PPX-D7 | *Implementation decision.* **A fresh launch materializes the good build, by reflink or copy and never by hardlink, into a per-launch directory beside its pack tree, and the `files` emitter mounts it read-only; no store is mounted for it, and a move reaps every other build of the key at once** | 2026-10-04 | [§8.1](#81-in-a-jail) | — |
| <a id="PPX-D8"></a>PPX-D8 | *Implementation decision.* **The jail learns what it was handed from `YOLO_PATCHED_TREES`, a once-at-boot sibling of `YOLO_FORK_BUILDS`** | 2026-10-04 | [§8.1](#81-in-a-jail) | — |
| <a id="PPX-D9"></a>PPX-D9 | *Implementation decision, applying [PF-D19](patched-forks.md#PF-D19).* **`agent_updates` off for the contributing pack or the owning agent pack holds a patched extension; with a fork of the owning agent's program selected, the fork pack counts too** | 2026-10-04 | [§2](#2-the-verdict-and-three-more-principles) PE8 | — |
| <a id="PPX-D10"></a>PPX-D10 | *Implementation decision.* **pi learns of a built tree only through a `~/<into>` list entry its author writes; two lints warn when no entry names it and when a same-named entry would load it twice** | 2026-10-04 | [§8.2](#82-how-pi-finds-it) | — |
| <a id="PPX-D11"></a>PPX-D11 | *Implementation decision.* **The Linux host renders a patched extension as a host-private versioned copy and an owned link swapped by rename, after the check and the advance and before the launch gate's comparison; the previous version is kept until the next move** | 2026-10-04 | [§8.3](#83-at-the-host) | — |
| <a id="PPX-D12"></a>PPX-D12 | *Implementation decision, under [OQ-PPX1](#OQ-PPX1).* **The owning agent pack's launchers, the base's and any fork's, act on a patched extension's reason as [OQ-PPX1](#OQ-PPX1) rules; the jail launch is never refused** | 2026-10-04 | [§9](#9-failure-and-the-next-step) | — |
| <a id="PPX-D13"></a>PPX-D13 | *Implementation decision.* **Checks of different extension keys may run concurrently, and their lines print in declaration order** | 2026-10-04 | [§6.1](#61-where-the-check-runs) | — |
| <a id="PPX-D14"></a>PPX-D14 | *Implementation decision, applying [PF-D8](patched-forks.md#PF-D8).* **A newer upstream that does not replay or build leaves the good build serving, unless [OQ-PPX1](#OQ-PPX1) is ruled C** | 2026-10-04 | [§9](#9-failure-and-the-next-step) | — |
| <a id="PPX-D15"></a>PPX-D15 | *Implementation decision.* **The footprint marks a patched extension for review, naming its source, ref, follow rule, series, build and landing; no flag hides its launch, move or held lines** | 2026-10-04 | [§10](#10-trust-and-disclosure) | — |
| <a id="PPX-D16"></a>PPX-D16 | *Implementation decision.* **A series member that adds a path the upstream ignores at the base is said once, naming `build`** | 2026-10-04 | [§7.2](#72-identity-and-selection) | — |
| <a id="PPX-D17"></a>PPX-D17 | *Implementation decision.* **The check record is per extension key, as [PF-D9](patched-forks.md#PF-D9)'s is per fork key, never per declaration** | 2026-10-04 | [§14](#14-alternatives-and-what-this-does-not-cover) | — |
