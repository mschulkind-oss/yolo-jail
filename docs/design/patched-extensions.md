---
title: "A patched fork can follow a pi extension's upstream — the same ratchet, ending in a read-only tree pi loads in place"
date: 2026-10-04
status: accepted
stage: DECIDED
tags: [design, packs, files, pi, extensions, forks, evergreen, git]
summary: "The maintainer asked on 2026-10-04 for the patched-fork mode to cover pi extensions. A `files` contribution may name an upstream `source` and a `patches` series in place of `from`. yolo checks the upstream at most hourly, replays the series on the host exactly as a patched fork does, takes the newest upstream version the series fits, builds it in the sealed capture jail and admits the result as a tree. Each fresh jail launch mounts its own copy of this machine's good build read-only, and pi loads it as a local package through a list entry the pack author writes, so pi never installs or updates it. Two calls were new and the maintainer's, what pi starts with when no build serves and whether the version is pinned in packs.lock.json; they and the four patched-fork questions were decided on their leanings under his delegation of 2026-10-04."
next: "Build PPX-D40 (OQ-PPX3, ruled 2026-10-05): a fresh launch refuses before booting when a patched extension a selected pack needs has no build, naming the ways back, where the host-side line saying pi will not start is being built; steps 2 to 5 of §16 built 2026-10-04 (PPX-D20 to PPX-D31) and integrated at e87f1ba88 on 2026-10-05 (PPX-D32, PPX-D33); step 6, migrating the five with the migration kit and checking that a real pi loads a built tree, is the maintainer's, who may overrule PPX-D18 and PPX-D19"
depends-on:
  - patched-forks.md
---

# A patched fork can follow a pi extension's upstream — the same ratchet, ending in a read-only tree pi loads in place

**Status:** 2026-10-04. [§16](#16-what-i-would-build-in-order) steps 2 to 5 are built (the Built
column of the [ledger](#decision-ledger) says where), at the jail notches, the Linux host and as a line
at macos-user and a macOS host; [PPX-D16](#PPX-D16)'s series lint is not. Integrated with the patched
forks' steps 3 and 4 on 2026-10-05 at `e87f1ba88`, which added `yolo pack rebase <pack>/<name>`
([PPX-D32](#PPX-D32)), kept `yolo pack update`'s host apply from building ([PPX-D33](#PPX-D33)), and
ran an extension end to end through a real build jail. The maintainer's first real launch, on
2026-10-05, found every build jail of his `matt` pack refusing before its build line, which
[PPX-D39](#PPX-D39) fixes. No pi has yet loaded a tree
yolo built: that is step 6's check, a human's. Evidence read at `48491fd4a`; pi read, not run, from 0.99.1 as
installed in this jail. MEASURED the same day in fresh blobless clones with git 2.55.0, npm 11.17.0
and node v24.19.0: the maintainer's five extension forks replayed onto their upstreams, the
[newest-fit walk](#6-detection-and-the-newest-fit-walk) over each, two trees built, one fork's
`dist/` rebuilt from a series without it, and pi's dependency command run on a toy package.
UNMEASURED: no pi has loaded a tree built this way. [OQ-PPX3](#OQ-PPX3), filed 2026-10-05 from the
maintainer's first patched launch, was ruled the same day ([PPX-D40](#PPX-D40), not built): a
patched extension a selected pack needs with no build to run refuses a fresh launch before it
boots, which reverses [PPX-D12](#PPX-D12)'s *"the jail launch is never refused"*.

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
and two collisions with standing pi rulings ([OQ-PPX1](#OQ-PPX1), [OQ-PPX2](#OQ-PPX2)). On
macos-user and a macOS host it costs the extensions themselves: migrating drops each `git:` entry
from a list every notch reads, and those notches build no tree, so pi there runs without the
extension where today it installs the fork ([§11](#11-notch-coverage),
[§13](#13-migrating-the-maintainers-five-forks)). It amends [PF-D10](patched-forks.md#PF-D10) for
both routes.

**Start at [§5](#5-what-carries-over-from-patched-forks)**: what carries over unchanged, and the three
places it cannot, which [§7](#7-the-build-and-the-admit) and [§8](#8-delivery) take.

**Decided under your delegation of 2026-10-04, yours to overrule:** [OQ-PPX1](#OQ-PPX1) B and
[OQ-PPX2](#OQ-PPX2) A, each on its leaning. Patched forks' [OQ-PFK1](patched-forks.md#OQ-PFK1),
[OQ-PFK3](patched-forks.md#OQ-PFK3) and [OQ-PFK4](patched-forks.md#OQ-PFK4), decided the same day, bind
this mode as written ([§12](#12-dependencies)).

**Needs your ruling:** nothing. [OQ-PPX3](#OQ-PPX3) was ruled in review on 2026-10-05
([PPX-D40](#PPX-D40)); the reading of "a build fails" it records is yours to confirm.

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
- **Owning agent pack**: the selected pack that declares the surface of the list entry naming the
  tree: the `config-list` or posture-list entry, in the contributing pack, naming `~/<into>` or a
  path inside it ([PPX-D36](#PPX-D36)), which [§8.2](#82-how-pi-finds-it)'s lint looks for. A
  surface is named `agent/name` (READ
  [`contributes.go:399`](../../internal/packdecl/contributes.go#L399)), and `pi/settings` is the pi
  pack's (READ [`packs/pi/pack.json:84-91`](../../packs/pi/pack.json#L84-L91)), so in the
  maintainer's case `matt` contributes and `pi` owns. With no such entry there is none, and pi never
  loads the tree. It is not read from where the tree lands: [§4](#4-the-declaration) lets the author
  land it anywhere, and a `state` declaration says which pack keeps a directory, not which agent
  loads a tree in it.
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
index file ([`pack-pi-resources.md`](pack-pi-resources.md#1-what-pi-loads-from-where-and-in-what-form)).
There, the list entry stops being the one switch. Without it pi still loads the extension, but not
its skills and prompts; with it, pi resolves each file once, because it keeps one entry per
absolute path (READ `package-manager.js:2102-2107`); and once the entry is removed, discovery keeps
loading the extension. MEASURED with pi 0.99.1's package resolver, `HOME` in scratch and no
session: a package at `~/.pi/agent/extensions/demo` whose manifest names `./index.ts` and
`./skills` resolved to that extension and no skill with no list entry, and to the same one
extension and its skill with the entry. `pi-subagents` and `pi-dynamic-workflows` both declare
skills (READ, their `package.json` at v0.75.0 and v3.13.0). Core knows no agent's paths, so this is
guidance, not a check.

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
- **the host's `files` render**: `yolo host apply`, and `yolo host -- <bin>` under
  `host_apply_on_launch` when `<bin>` is a program of the owning agent pack or of a fork of it,
  before the gate compares the render in observe posture ([§8.3](#83-at-the-host)). So
  `yolo host -- claude` runs no check for a pi extension;
- **the explicit acts**, which force it: `yolo pack update`, `yolo pack install`,
  `yolo capture <pack>/<name>` and, under [OQ-PFK4](patched-forks.md#OQ-PFK4)'s leaning,
  `yolo pack rebase <pack>/<name>`.

It never runs on an attach, a dry run, in a jail, in a capture or build jail, or under a hold with a
good build ([PPX-D9](#PPX-D9)). Checks of different keys may run concurrently, except where two
name one upstream repository, such as two `//subdir` extensions of one monorepo, or an extension and
a patched fork of one repository: those share its mirror lock for the fetch
([PF §6.6](patched-forks.md#66-locks-and-their-order); READ the per-repository flock,
[`store.go:23`](../../internal/packsrc/store.go#L23), [`refresh.go:637-641`](../../internal/packsrc/refresh.go#L637-L641))
and serialize on it. Their lines print in declaration order.

### 6.2 The newest-fit walk

This amends [PF-D10](patched-forks.md#PF-D10) for both routes, and its one normative statement is
[`patched-forks.md` §6.4](patched-forks.md#64-the-newest-fit-and-the-first-advance). In brief: an
advance replays the series down a list, newest first (the branch's tip under `follow: "head"`, then the
version tags merged into the branch that contain the series' base), and takes the first entry it fits.
The check's candidate is that list's newest entry above the good build, found from the mirror with no
replay; an empty list leaves nothing pending and shows no held suffix. A conflict is recorded against
its entry ([PF-D9](patched-forks.md#PF-D9)), so no entry is replayed twice for one series. No fit
holds the good build or, on a first advance, builds the base. The walk has a 60 s bound of its own,
apart from the check's; the entries it did not reach by then stay pending for the next check.

**Why the core changes.** "The newest, else the base" drops a second machine, or an edited series, to
the base even where v1.2 fits and only v1.3 conflicts. Restricting tags to those containing the base
keeps an older version from passing for an update: for `pi-automode` today the newest version,
v1.17.0, is older than the base, and the series replays onto it cleanly (MEASURED,
[§3.2](#32-the-maintainers-five-forks)). So its list is empty: once its base is built nothing is
pending, and no launch inside the hour runs git or shows a held suffix. Day one for the maintainer
does not change: for the three that conflict, the newest fit is the base
([§3.2](#32-the-maintainers-five-forks)).

## 7. The build and the admit

### 7.1 The build act

It is a fork's build act ([§7 of patched forks](patched-forks.md#7-the-build-and-the-launch)) with two
changes:

- **The seal is narrowed to the contributing pack**, where a fork's is narrowed to the fork and its
  base ([FP-D9](forked-programs-as-packs.md#FP-D9); READ `sealPacks` in
  [`cli/forkbuild.go`](../../internal/cli/forkbuild.go)), and carries the base of any fork that
  pack declares besides, and in turn any base that base forks. The launch gates that ask whether another selected pack provides what one
  names do not run in it, since the narrowing is what dropped that pack
  ([PPX-D39](#PPX-D39)). The toolchain is the image's
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
launch waits at all is [OQ-PFK3](patched-forks.md#OQ-PFK3). One Ctrl-C ends the act's whole wait, the
extensions' included: an extension whose advance begins after it is handed its good build with no
check ([PF-D57](patched-forks.md#PF-D57)).

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

A copy in progress can still be cut short, by another workspace's launch moving the good build. The
store accepts a reap in the middle of a materialize only because a failed materialize falls through
to the vendor's installer (READ [`gc.go:45-48`](../../internal/capture/gc.go#L45-L48)), and a tree
has no installer to fall through to. So a copy is checked against its entry's completion marker once
it ends. A reap removes the marker before any of the tree (READ
[`gc.go:219-224`](../../internal/capture/gc.go#L219-L224)), and the move reaps the same way, so a
marker still there means the copy read a whole tree. A marker gone means the copy may be partial
even if it reported no error: it is removed and the record re-read once, as when the entry was
reaped before the copy began.

### 8.2 How pi finds it

- **The author writes `~/<into>` in pi's `packages` list**, or a path inside it, such as one package
  of a monorepo's tree ([PPX-D36](#PPX-D36)), and drops the extension's `git:` entry in the same
  edit. pi loads it as a local package: never installed, never updated, untouched by the
  pre-launch refresh, with the package's skills and prompts.
- **The pi pack's subagents MCP render still fires.** Its pattern matches
  `~/.pi/agent/yolo-patched/pi-subagents` and does not match it with a trailing slash (MEASURED with
  node, against [`packs/pi/pack.json:140`](../../packs/pi/pack.json#L140)).
- **One lint**, at `yolo pack lint` and once at launch, a warning naming the line to add: no
  `config-list` or posture-list entry in the contributing pack names `~/<into>` or a path inside
  it, so the tree is mounted and pi never loads it. Its test compares cleaned paths, so core reads
  none of pi's grammar ([PPX-D36](#PPX-D36)).
- **A lint for the old entry left beside the new one, by its final name** ([PPX-D34](#PPX-D34)),
  which loads the extension twice. A warning at `yolo pack lint` when one list holds `~/<into>` and
  a `git:` or `npm:` entry, or a URL, of the extension's name or its upstream's. Telling every old
  entry apart would mean parsing pi's package-source grammar, which core does not do
  ([caching §5](pi-git-extension-caching.md#5-alternatives)). The final name is the question the
  pi pack's subagents render already asks, and an entry under another name is still
  [§13](#13-migrating-the-maintainers-five-forks)'s guidance.

### 8.3 At the host

On Linux, the readiness act is the `files` render, and a patched extension gets an arm of its own: the
existing render copies file by file and skips a contribution with no `from`
([`hostfilestree.go:59-89`](../../internal/entrypoint/hostfilestree.go#L59-L89)), and `pi-subagents`'
tree alone is 1,386 files (MEASURED).

- **The check and any advance run first**, at `yolo host apply`, and at `yolo host -- <bin>` only
  when `<bin>` is a program of the owning agent pack or of a fork of it, before the gate compares
  the render in observe posture, so the comparison sees the build the apply would install. The
  scope is needed because the gate surveys the whole render whatever the bin, which it uses only for
  its messages and to pick which failures stop the launch (READ
  [`hostapplygate.go:108`](../../internal/cli/hostapplygate.go#L108), [`:189`](../../internal/cli/hostapplygate.go#L189),
  [`:221`](../../internal/cli/hostapplygate.go#L221)). Without it, `yolo host -- claude` would wait
  on a pi extension's fetch and build. A patched fork's host trigger is likewise its own program's
  floor install ([PF §4.1](patched-forks.md#41-where-the-check-runs)).
- **They run outside that comparison, never inside it**: the comparison is the apply itself with its
  writes withheld, bounded at one second as a stuck-detector (READ
  [`hostapplygate.go:62-72`](../../internal/cli/hostapplygate.go#L62-L72)), and a check alone may
  wait 60 s on the network. Inside the comparison, and at every other bin's launch, the patched arm
  only reads which build the link names against the good build the record names.
- **The good build is materialized into a host-private versioned directory**, one per build key, in
  yolo's state directory where no jail mounts it.
- **`~/<into>` is a symbolic link the render owns**, recorded in the `files` ownership record and
  swapped by writing a new link beside it and renaming it over.
- **The previous version is kept until the next move**, so a host pi already running keeps reading the
  tree it loaded. INFERRED: jiti 2.7.0, pi's loader, resolves a loaded file to its real path (READ,
  minified `jiti.cjs`), as [the caching design's §3.7](pi-git-extension-caching.md#37-running-sessions-and-moving-pointers)
  also infers; UNMEASURED with pi.
- **A revert removes the link** the `files` ownership record says the render wrote, and the versioned
  directories with it ([PPX-D31](#PPX-D31)). A plain `files` tree's output is not withdrawn by a
  revert, which removes keys.

A `pi` started outside yolo at the host finds whatever the link names, and with no link pi skips the
path silently. **macOS host:** none, since the build is the jail's Linux platform. A line says so
once and names a jail that has the extension: `YOLO_RUNTIME=podman yolo -- pi`.

## 9. Failure, and the next step

[PF §8.1](patched-forks.md#81-the-failure-table) holds, with these changes:

| Failure | What serves | Said, with the next step |
| :--- | :--- | :--- |
| A newer upstream does not replay or build | the good build ([PF-D8](patched-forks.md#PF-D8)), unless [OQ-PPX1](#OQ-PPX1) is ruled C | the held suffix, naming `yolo pack rebase <pack>/<name>` |
| A `produces` path is missing, the tree names the build's home, or the delta has strays | the good build; a failed build with back-off | the paths, and the build line as the fix |
| The entry was reaped between the lookup and the copy, or during it (its completion marker gone when the copy ends) | the partial copy removed and the record re-read once; else rebuilt from its inputs | as a first build |
| The per-launch copy fails (a full disk) | nothing for this launch; the store is untouched | the error and the extension |
| Nothing serves | nothing: a fresh launch refuses before booting ([PPX-D40](#PPX-D40)) | see below |

**Nothing serves.** At a notch that builds trees, a fresh launch refuses before booting when the
owning agent pack's list entry reaches it, whatever the command, naming the extension, the cause
and the ways back ([PPX-D40](#PPX-D40), ruled in [OQ-PPX3](#OQ-PPX3)). Under
[OQ-PPX1](#OQ-PPX1)'s leaning the owning agent pack's launchers, the base's and any fork's, still
stop before exec, naming the same, and the jail's shell stays up: the backstop for an attach, which
boots nothing. Notches that build no tree (macos-user, the macOS host, and Apple Container below its
read-only floor, [§11](#11-notch-coverage)) start pi with a line said once, in
[FP-D3](forked-programs-as-packs.md#FP-D3)'s shape, naming `YOLO_RUNTIME=podman`. With no owning agent
pack no list entry names the tree, so pi does not load it, [§8.2](#82-how-pi-finds-it)'s lint says so,
and nothing stops. Nor does a tree stop pi at a notch its entry does not reach, where it is not
built or mounted either ([PPX-D35](#PPX-D35)): `pi-automode`'s entry is in a guarded posture list,
which reaches the host and no jail ([§3.2](#32-the-maintainers-five-forks)), so no jail builds it,
and pi there starts without it. A mountpoint podman made on an earlier launch may be left empty at
`~/<into>` (INFERRED: `.pi` is a workspace state directory on the host). pi does not skip an empty directory
as it skips a missing path: it tries to load the directory itself as one extension, which fails
(READ `package-manager.js:1079-1085`;
[`pack-pi-resources.md` §1](pack-pi-resources.md#1-what-pi-loads-from-where-and-in-what-form)).
Under option A the launch's line has to name it.

**What [OQ-2](pi-git-extension-caching.md#OQ-2) asks, in two halves.** A launch starts pi on nothing
but what its config calls for, never on what another launch left; and a launch that waited for
another never inherits that launch's failure, but retries the failed step once itself (READ
[caching §3.5](pi-git-extension-caching.md#35-locks-waits-and-bounds) and
[§3.6](pi-git-extension-caching.md#36-failure-paths)). Patched forks depart from the second half by
design: a waiter takes another launch's result, failure included
([PF-D17](patched-forks.md#PF-D17)), and a failed build is backed off for a day, doubling to a
week, while a good build serves ([PF-D9](patched-forks.md#PF-D9)). The notches that build no tree,
which [OQ-PPX1](#OQ-PPX1)'s B exempts, are macos-user, the macOS host and Apple Container below its
read-only floor ([§11](#11-notch-coverage)). Under option C neither departure applies to a patched
extension, so a build that keeps failing costs every fresh launch a rebuild, and its pi.

**A jail launch is refused for one cause alone**: a needed tree with nothing to serve, at a notch
that builds trees ([PPX-D40](#PPX-D40)), which amends
[PF §6.7](patched-forks.md#67-what-the-mode-never-does) for both routes. A check, a replay or a
newer build that fails while a good build serves refuses nothing: the good build serves, with the
held suffix.

<a id="oq-ppx3-background"></a>**Why the refusal comes before the image step.** The host decides
every tree before the image step (`treeDeliveriesFor`, ahead of `autoLoadImage` in
`cli/run/run.go`), so it knows a needed tree has no build before any jail exists. Under
[PPX-D12](#PPX-D12) as built, a `yolo -- pi` still builds or loads the image, boots and provisions,
and pi's launcher installs or refreshes pi before the gate prints its lines and exits 1
([PPX-D24](#PPX-D24)), and `yolo -- pi` returns with it. A host-side line saying pi will not start,
printed right after the trees are decided, was being built when [OQ-PPX3](#OQ-PPX3) was filed. The
ruling makes that point the refusal, for every command alike ([PPX-D40](#PPX-D40)).

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
| **jail, Apple Container** | from the read-only floor up, yes, and builds as [PF §9](patched-forks.md#9-notch-coverage) says: a capture jail cannot start beside a running jail (INFERRED there), so a candidate stays pending and the good build serves. **Below the floor no tree is built**, as no fork is: the tree arm takes the slot's floor return ([`run/forkbuild.go:63-68`](../../internal/cli/run/forkbuild.go#L63-L68)). The floor's reason is that `:ro` is ignored there (READ [`backendcaps.go:82-91`](../../internal/cli/run/backendcaps.go#L82-L91)), so a build jail's read-only binds would be writable, which the seal ([FP-D9](forked-programs-as-packs.md#FP-D9)) does not allow for (INFERRED). A good build already on this machine for the jail's platform is still delivered: the mount is a per-launch copy, so a write changes only that copy, as for any `files` tree. With none, pi starts without the extension and the line names `YOLO_RUNTIME=podman` ([§9](#9-failure-and-the-next-step)) |
| **jail, macos-user** | none: no sealed build there ([FP-D3](forked-programs-as-packs.md#FP-D3)); the line names `YOLO_RUNTIME=podman`, as a fork's does |
| **`yolo host`, Linux** | yes ([§8.3](#83-at-the-host)) |
| **`yolo host`, macOS** | none; the line names `YOLO_RUNTIME=podman yolo -- pi` ([§8.3](#83-at-the-host)) |
| **guest** | unbuilt, as every verb there is |

## 12. Dependencies

**Needed:**

- [Patched forks' steps 1 and 2](patched-forks.md#14-what-i-would-build-in-order), built over the owner
  key and with the walk.
- Rulings on patched forks' three blocking questions, whose leanings read the same here:
  - [OQ-PFK1](patched-forks.md#OQ-PFK1)'s B also fits the caching design's
    [OQ-2](pi-git-extension-caching.md#OQ-2);
  - under [OQ-PFK3](patched-forks.md#OQ-PFK3)'s A the wait is the build jail's boot plus the build,
    measured at 0.3 s for one tree and 7.4 to 24 s for the other ([§15](#15-risks-and-costs));
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
     ([§12](#12-dependencies)), so the default rule leaves it at its series' base
     ([PF-D60](patched-forks.md#PF-D60)).
2. In one edit, add the `files` contribution and replace the extension's `git:` entry with
   `~/<into>`. Leaving both loads it twice.
3. Launch.

**Day one**, from [§3.2](#32-the-maintainers-five-forks): base plus series is each fork's tree today.

- `pi-automode` and `pi-background-tasks` run their base, which is their upstream's head.
- `pi-subagents`, `pi-dynamic-workflows` and `pi-archimedes` are held at their base, which is
  exactly the fork's current tree, and the held suffix names `yolo pack rebase`.
- `pi-automode`'s list entry is guarded-only, so no jail builds or mounts its tree
  ([PPX-D35](#PPX-D35)), and the Linux host, where the guarded list reaches, builds and loads it.
- **On macos-user and a macOS host every migrated extension is absent**, where today pi installs each
  fork from its `git:` entry there (INFERRED from [§3.1](#31-how-pi-takes-an-extension-and-where-yolo-can-put-a-tree)'s
  first row). Step 2 drops that entry from a list that reaches every notch
  (READ [`notch-scoped-config-contributions.md`](notch-scoped-config-contributions.md)'s summary:
  a list is placed at every notch, and only a posture list narrows it), no list can be narrowed to
  those notches alone ([§14](#14-alternatives-and-what-this-does-not-cover)), and those notches build
  no tree ([§11](#11-notch-coverage)). Each says so once and names `YOLO_RUNTIME=podman`.

**An older yolo** refuses the manifest at the host, as it refuses a patched fork's, and
[PF §10](patched-forks.md#10-migration-from-a-plain-fork)'s mitigations apply: a pack author keeps
the `git:` manifest where existing users point and publishes this one on a new ref. A yolo from this
release on skips a contribution it cannot read and names it
([PF-D68](patched-forks.md#PF-D68)), and `yolo features` names `patched-extensions`
([PF-D71](patched-forks.md#PF-D71)).

## 14. Alternatives, and what this does not cover

| Alternative | Verdict |
| :--- | :--- |
| **A. The program route**, a `program` with `fork_of` | **Rejected.** An extension has no bin and no base program, and the fork fields are refused off `program` with `via: "source"` ([`fork.go:67-70`](../../internal/packdecl/fork.go#L67-L70)) |
| **B. Patch pi's own git checkout** | **Rejected.** The hourly refresh's update cleans and resets it ([§3.1](#31-how-pi-takes-an-extension-and-where-yolo-can-put-a-tree)) |
| **C. Patch the published npm package** | **Rejected.** Some are build output: `pi-subagents`' published 0.75.0 names `./index.js`, its git source `./index.ts` (MEASURED), so a series written against the source has nothing to apply to |
| **D. Land in `extensions/<name>`** | **Rejected.** pi's discovery loads the tree there whether or not a list entry names it, so the entry is no longer the one switch: with no entry the extension loads without its skills and prompts, and it keeps loading after the entry is removed (MEASURED, [§4](#4-the-declaration)). With the entry, pi resolves each file once, so double loading is not the reason |
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
- **A `files` contribution scoped to a posture or notch**, and a list entry scoped to a notch, which
  could keep the `git:` entry on macos-user and a macOS host
  ([§13](#13-migrating-the-maintainers-five-forks)).
- **A lint for an old entry under another name** than the extension's or its upstream's, which
  [PPX-D34](#PPX-D34)'s lint does not see. It needs pi's package-source grammar
  ([§8.2](#82-how-pi-finds-it)). The pi pack could declare the pattern, as it does for the
  subagents render's `whenListed.matches` ([`packs/pi/pack.json:140`](../../packs/pi/pack.json#L140)).
- **Resolving conflicts, and writing the series.** As [patched forks §13](patched-forks.md#13-what-this-does-not-cover).

## 15. Risks and costs

| Risk | Mitigation |
| :--- | :--- |
| A clean pick that is wrong code | As for patched forks: the build is the only automatic check; every move is disclosed; a hold is one ref edit or one `agent_updates` key |
| Unreviewed upstream code runs inside pi's process | The same exposure today's `git:` entries carry, minus their scripts running in the user's jail ([§10](#10-trust-and-disclosure)) |
| An extension in a long-lived jail moves only at a fresh launch, where today's hourly refresh moves it in a running jail | Stated; a fresh launch is the readiness act ([PF-D5](patched-forks.md#PF-D5)) |
| An extension that writes into its own directory fails on the read-only mount | Said by pi as the extension's error; the fix is the extension's |
| `pi-archimedes` takes its core package from the registry: the fork's root depends on `@pi-archimedes/core` `2.8.0`, which the registry has, and declares no npm `workspaces` (MEASURED). Today's series changes `packages/core` only by a test devDependency, member 11's `fast-check` (MEASURED), so nothing is lost now; a later member under `packages/core/src` would never reach runtime under npm (INFERRED) | If such a member appears, the build line must build and link the workspace package; a lint cannot see this |
| A native module built against the jail's Node runs under another at the host | The toolchain is on the receipt; pi reports the load error |
| No pi has loaded a tree that carries its own `node_modules` | The first step of [§16](#16-what-i-would-build-in-order) measures it in a nested jail |

**Costs**, MEASURED 2026-10-04 unless marked:

- **Replay:** at most 0.63 s for one series' first replay, and 2.15 s for the longest walk from a fresh
  blobless clone.
- **Trees:** 21 MB and 1,386 files for `pi-subagents` (0.3 s to install), and 14 MB and 335 files
  for `pi-dynamic-workflows` at its base with the dist-free series, the tree day one delivers;
  upstream v3.13.0 alone gives 302. Its build was first timed on upstream v3.13.0 alone: 3.8 s
  `npm ci`, 3.0 s `tsc`, 0.6 s prune. Re-timed with the series, five runs each interleaved with five
  of the base alone, on a loaded machine (a one-minute load average of 40 to 57 on 32 CPUs where
  read), the three steps took 14.8 to 24.2 s in all, against 12.2 to 21.5 s for the base alone.
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
2. **The declaration**: validation, the extension key, the lint and the footprint claim.
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
- `pi-automode`, whose base is past its upstream's newest version, runs no git on a second launch
  inside the hour and shows no held suffix.
- A conflict is said once, and the good build runs.
- A copy whose store entry is reaped while it runs is never mounted.
- `yolo host -- claude` runs no check for a pi extension.
- `pi update --extensions` leaves the tree byte-identical.
- Neither `yolo prune` nor a move changes a running jail's copy.
- An attach never reports that the pack set differs.
- The pi pack's subagents MCP file is still written.
- `forks.lock.json` holds no entry for a tree.

## Open Questions

Patched forks' [OQ-PFK1](patched-forks.md#OQ-PFK1) to [OQ-PFK4](patched-forks.md#OQ-PFK4) bind this
mode as written, and one ruling of each covers both routes ([§12](#12-dependencies)).

1. ✅ **OQ-PPX1: When a patched extension's build cannot be had, what does pi start with?**

   Patched forks keep the good build when a newer upstream fails ([PF-D8](patched-forks.md#PF-D8)),
   and depart from the second half of the caching design's
   [OQ-2](pi-git-extension-caching.md#OQ-2) ([§9](#9-failure-and-the-next-step)).

   - **A — pi always starts**, without the extension when nothing serves, and the launch says so.
   - **B — The good build serves; with none, pi's launchers stop before exec.** Notches that build
     no tree are exempt.
   - **C — Strict [OQ-2](pi-git-extension-caching.md#OQ-2).** pi stops unless the newest fit is
     admitted; each launch retries a failed build itself, with no back-off.

   Under B or C, a fresh offline machine starts no pi until a first build lands; under A the
   extension can go missing unnoticed.

   <!-- vantage: question id=OQ-PPX1 -->

   _Leaning:_ B — it keeps [OQ-2](pi-git-extension-caching.md#OQ-2)'s first half: a good build is
   complete, admitted and immutable, where the case it ruled out was a launch that found the
   refresh lock held booting on whatever was installed
   ([caching §2](pi-git-extension-caching.md#2-how-pi-handles-git-packages-today)), and nothing
   to serve is exactly that ruling's case. It departs from the second half: the good build is
   whatever an earlier launch on this machine admitted, perhaps another workspace's, and a launch
   takes another's failure and its back-off, so which upstream version a launch gets depends on
   other launches. My read is that this stays within the ruling's purpose, because every launch
   gets the series and recipe its config spells, and only the upstream version, which the config
   leaves to the follow rule, depends on the machine, as [PF-D8](patched-forks.md#PF-D8) has it for
   a patched program; and a forked pi with nothing to serve does not run either.

   **Answer:**
   > **B**, decided 2026-10-04 on the leaning under the maintainer's delegation of that day:
   > "I want to get the patched forks and patched extensions out as soon as possible. So if there's design decisions you can make, make them and build it. And we can always adjust later."
   > The good build serves; with none, the owning agent pack's launchers stop before exec and say why, and notches that never build trees are exempt. Adjustable: his to overrule once he has tested the build.

2. ✅ **OQ-PPX2: Is a patched extension's version pinned in `packs.lock.json`?**

   [`pi-extension-lifecycle.md` OQ-2](pi-extension-lifecycle.md#OQ-2) ruled that yolo resolves and pins
   pi packages through `packs.lock.json`; that pin is unbuilt. The request asks for an evergreen build,
   and [PF-D16](patched-forks.md#PF-D16) keeps a patched fork's good build off every lock that travels.

   - **A — No pin.** The good build is machine-local; a hold is a tag ref or `agent_updates`.
   - **B — A pin**, moved only by `yolo pack update`; a second machine builds that exact commit.

   <!-- vantage: question id=OQ-PPX2 -->

   _Leaning:_ A — [lifecycle OQ-2](pi-extension-lifecycle.md#OQ-2)'s purpose, putting the version
   choice and a rollback in yolo, is met by the check and the ratchet; a pin that moves at every admit
   is not a pin; and B brings back the cross-machine coupling PF-D16 removes.

   **Answer:**
   > **A**, decided 2026-10-04 on the leaning under the maintainer's delegation of that day:
   > "I want to get the patched forks and patched extensions out as soon as possible. So if there's design decisions you can make, make them and build it. And we can always adjust later."
   > No pin: the good build is machine-local, and a hold is a tag ref or `agent_updates`. Adjustable: his to overrule once he has tested the build.

3. ✅ <a id="OQ-PPX3"></a>**OQ-PPX3: When the launch's only program is one the tree gate will stop,
   may the launch stop before booting, saying why?**

   Amends [PPX-D12](#PPX-D12) and [PF §6.7](patched-forks.md#67-what-the-mode-never-does)
   ([background](#oq-ppx3-background)).

   - **A — Never refused, as built.** The host line, the boot, then the gate's stop. *Cost:* the
     image step, boot, provisioning and pi's install, spent on a jail that exits 1.
   - **B — Stop before booting** when the command is the gated program, naming the extension and
     the next step. *Cost:* yolo decides from the command, which
     [HP-DIR2](host-tool-provisioning.md#HP-DIR2) rules out at the host.
   - **C — Boot into the jail's shell instead.** *Cost:* the same reading of the command, and a
     scripted `yolo -- pi -p …` gets a shell where it expected pi to exit.

   _Leaning:_ **A** — the host line arrives before the image step, where B would stop, so a Ctrl-C
   there saves what B saves; B and C buy that saving by reading the command, which the maintainer
   ruled out at the host (*"We do not sniff the command line"*,
   [HP-DIR2](host-tool-provisioning.md#HP-DIR2)), and a stop keyed on the command catches a bare
   `pi` but not `bash -lc pi` or a wrapper.

   **Answer:**
   > **Ruled in review 2026-10-05, none of the options as written:** *"I thought we don't sniff the
   > CLI at all and make any decisions for it? it'll just fall through to a shim, no? but even
   > before we get there, if a build fails, of course that's fatal. we should offer solutions and
   > workarounds when that happens for happy path, but it's fatal"*. Narrower than B, since the
   > stop keys on the build and never on the command, and firmer, since it does not wait for a
   > launch whose only program is the gated one. When a patched extension a selected pack needs,
   > or a patched fork, has no build to deliver, a fresh launch refuses before booting, whether the
   > build failed or never ran, and whatever the command is: yolo never reads the command line
   > ([HP-DIR2](host-tool-provisioning.md#HP-DIR2), 2026-09-29). The in-jail launcher gate
   > ([PPX-D18](#PPX-D18)) stays as the backstop for an attach and a nested launch, which this
   > refusal does not reach. The refusal names the ways back, as
   > [the happy-path principle](../reference/happy-path-principle.md) asks:
   >
   > - the fix the build's own failure names;
   > - `yolo pack series check` and `yolo pack rebase <pack>/<name>`, for a series that no longer
   >   applies;
   > - `yolo capture <pack>/<name>`, to retry;
   > - dropping the list entry, or the pack, to run without it;
   > - and the opt-in bypass variable [OQ-JR1](jail-notch-readiness.md#OQ-JR1) rules the same day
   >   for a program a launch could not install, if one variable fits both. Whether it does is the
   >   builder's call.
   >
   > *Recorder's reading, for the maintainer to confirm:* "a build fails" means there is no build
   > to run. A newer build that fails while the last good build still serves
   > ([PF-D8](patched-forks.md#PF-D8), [PPX-D14](#PPX-D14)) still serves, with today's warning,
   > the held suffix, and is not fatal. Nor is a notch that never builds a tree (macos-user, the
   > macOS host, Apple Container below its read-only floor, a nested launch): no build failed
   > there, so it keeps [PPX-D18](#PPX-D18)'s exemption and its line. Recorded as
   > [PPX-D40](#PPX-D40), which supersedes [PPX-D12](#PPX-D12)'s *"the jail launch is never
   > refused"*; the fork side is [`patched-forks.md` PF-D77](patched-forks.md#PF-D77). Not built.

## Decision Ledger

Every row is reversible, and the Built column says what has been built of each.
[PPX-D1](#PPX-D1)–[PPX-D17](#PPX-D17) are implementation decisions made in drafting, and
[PPX-D20](#PPX-D20)–[PPX-D38](#PPX-D38) are implementation decisions made building it, and
[PPX-D39](#PPX-D39) one made fixing what the maintainer's first real launch found. [PPX-D18](#PPX-D18) and [PPX-D19](#PPX-D19) are the two questions that were
the maintainer's, [OQ-PPX1](#OQ-PPX1) and [OQ-PPX2](#OQ-PPX2), decided on their leanings on 2026-10-04
under his delegation, and still his to overrule. [PPX-D40](#PPX-D40) is his ruling of
[OQ-PPX3](#OQ-PPX3) in review, 2026-10-05. The two core
changes this design makes to patched forks are recorded there:
[PF-D10](patched-forks.md#PF-D10), amended, and [PF-D22](patched-forks.md#PF-D22).

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="PPX-D1"></a>PPX-D1 | *Implementation decision.* **A patched extension is a `files` contribution with `source` and `patches` in place of `from`; there is no new kind.** `from`, `fork_of`, `agent` and `agents` are refused beside `source`, and `patches` is required | 2026-10-04 | [§4](#4-the-declaration) | S5, 2026-10-04: `packdecl.Contribution.IsPatchedExtension`, `patchedExtensionProblems` ([`patchedext.go`](../../internal/packdecl/patchedext.go)) |
| <a id="PPX-D2"></a>PPX-D2 | *Implementation decision.* **The extension key is `<pack>/<last segment of into>`, unique per pack across its patched extensions and its patched programs' bins; every record, lock, selection, message and explicit act uses it** | 2026-10-04 | [§1](#1-defined-terms), [§4](#4-the-declaration) | S5, 2026-10-04: `packload.PatchedTrees`, `Fork.Key`; the uniqueness refusal `validatePatchedOwnerKeys` |
| <a id="PPX-D3"></a>PPX-D3 | *Implementation decision.* **`into` is required and may not land on PATH, for any pack; `build` is optional and the sealed jail runs either way; `produces` is optional and tree-relative** | 2026-10-04 | [§4](#4-the-declaration) | S5, 2026-10-04: `patchedExtensionProblems`, `treeProducesProblems`; the PATH refusal is every `files` destination's |
| <a id="PPX-D4"></a>PPX-D4 | *Implementation decision.* **The owning agent pack is the selected pack that declares the surface of the contributing pack's `config-list` or posture-list entry equal to `~/<into>`, the entry [§8.2](#82-how-pi-finds-it)'s lint looks for; with no such entry there is none, and pi never loads the tree.** [PPX-D36](#PPX-D36) widens "equal to" to a path inside it. Not read from a `state` prefix of `into`, which says which pack keeps a directory, not which agent loads a tree there | 2026-10-04 | [§1](#1-defined-terms) | S5, 2026-10-04: `packload.owningAgentPack`, with where the entry reaches (`Fork.ListedInJail`, `ListedAtHost`) |
| <a id="PPX-D5"></a>PPX-D5 | *Implementation decision.* **The build is a fork's build act with the seal narrowed to the contributing pack and one fixed final step that copies the checkout into a reserved directory under `~/.local`; the admit adds the `produces` paths, no stray delta and no reference to the build home to the empty-delta and link checks** | 2026-10-04 | [§7.1](#71-the-build-act) | S5, 2026-10-04: `cli.treeBuildJailArgv`, `sealPacks`, `treeAdmitProblem` ([PPX-D20](#PPX-D20), [PPX-D21](#PPX-D21)) |
| <a id="PPX-D6"></a>PPX-D6 | *Implementation decision.* **The recipe is `["tree", build, sorted produces, subdir, series digest]`; selection is by extension key and platform with an exact lookup; the receipt gains PF-D7's fields; `yolo prune`'s newest-per-selection-key rule is unchanged** | 2026-10-04 | [§7.2](#72-identity-and-selection) | S5, 2026-10-04: `packdecl.TreeRecipe`; the receipt's `fork` is the extension key ([PPX-D21](#PPX-D21)) |
| <a id="PPX-D7"></a>PPX-D7 | *Implementation decision.* **A fresh launch materializes the good build, by reflink or copy and never by hardlink, into a per-launch directory beside its pack tree, and the `files` emitter mounts it read-only; no store is mounted for it, and a move reaps every other build of the key at once, marker first.** A copy whose entry's completion marker is gone when it ends may be partial: it is removed, and the record re-read once | 2026-10-04 | [§8.1](#81-in-a-jail) | S5, 2026-10-04: `run.treeDeliveriesFor`, `cli.deliverTree`, `capture.CopyTree`, the `files` emitter's copy arm ([PPX-D22](#PPX-D22)) |
| <a id="PPX-D8"></a>PPX-D8 | *Implementation decision.* **The jail learns what it was handed from `YOLO_PATCHED_TREES`, a once-at-boot sibling of `YOLO_FORK_BUILDS`** | 2026-10-04 | [§8.1](#81-in-a-jail) | S5, 2026-10-04: `entrypoint.PatchedTreesEnv` ([PPX-D23](#PPX-D23)) |
| <a id="PPX-D9"></a>PPX-D9 | *Implementation decision, applying [PF-D19](patched-forks.md#PF-D19).* **`agent_updates` off for the contributing pack or the owning agent pack holds a patched extension; with a fork of the owning agent's program selected, the fork pack counts too** | 2026-10-04 | [§2](#2-the-verdict-and-three-more-principles) PE8 | S5, 2026-10-04: `packload.Fork.HoldPacks`, read by `run.PatchedForkHold` |
| <a id="PPX-D10"></a>PPX-D10 | *Implementation decision.* **pi learns of a built tree only through a `~/<into>` list entry its author writes; one lint warns when no entry equals it.** [PPX-D36](#PPX-D36) counts a path inside it too. No lint looks for the old entry left beside it, which would need pi's package-source grammar in core. Amended 2026-10-05 by [PPX-D34](#PPX-D34), whose lint looks for one by its final name | 2026-10-04 | [§8.2](#82-how-pi-finds-it) | S5, 2026-10-04: `packload.LintPatchedTrees`, at `yolo pack lint` and in the launch's block ([PPX-D27](#PPX-D27)) |
| <a id="PPX-D11"></a>PPX-D11 | *Implementation decision.* **The Linux host renders a patched extension as a host-private versioned copy and an owned link swapped by rename, after the check and the advance and before the launch gate's comparison; the previous version is kept until the next move.** The check and the advance run at `yolo host apply`, and at `yolo host -- <bin>` only when `<bin>` is a program of the owning agent pack or of a fork of it; at any other bin the gate only reads which build the link names | 2026-10-04 | [§8.3](#83-at-the-host) | S5, 2026-10-04: `cli.renderHostTrees`, `advanceHostTrees` ([PPX-D25](#PPX-D25)) |
| <a id="PPX-D12"></a>PPX-D12 | *Implementation decision, under [OQ-PPX1](#OQ-PPX1).* **The owning agent pack's launchers, the base's and any fork's, act on a patched extension's reason as [OQ-PPX1](#OQ-PPX1) rules, and only at a notch the list entry naming the tree reaches; the jail launch is never refused** *Superseded in part 2026-10-05 by [PPX-D40](#PPX-D40) ([OQ-PPX3](#OQ-PPX3)):* at a notch that builds trees, a fresh launch now refuses before booting when a tree its owner's entry reaches there has no build; the launchers' half stands | 2026-10-04 · superseded in part 2026-10-05 | [§9](#9-failure-and-the-next-step) | S5, 2026-10-04: the jail's `stop` ([PPX-D24](#PPX-D24)) and the host's ([PPX-D26](#PPX-D26)) |
| <a id="PPX-D13"></a>PPX-D13 | *Implementation decision.* **Checks of different extension keys may run concurrently, except two that name one upstream repository, which serialize on its mirror lock; their lines print in declaration order** | 2026-10-04 | [§6.1](#61-where-the-check-runs) | S5, 2026-10-04: through the shared check (the mirror's lock); the launch walks the trees in declaration order |
| <a id="PPX-D14"></a>PPX-D14 | *Implementation decision, applying [PF-D8](patched-forks.md#PF-D8).* **A newer upstream that does not replay or build leaves the good build serving, unless [OQ-PPX1](#OQ-PPX1) is ruled C** | 2026-10-04 | [§9](#9-failure-and-the-next-step) | S5, 2026-10-04: the shared advance (`cli.advancePatchedFork`) |
| <a id="PPX-D15"></a>PPX-D15 | *Implementation decision.* **The footprint marks a patched extension for review, naming its source, ref, follow rule, series, build and landing; no flag hides its launch, move or held lines** | 2026-10-04 | [§10](#10-trust-and-disclosure) | S5, 2026-10-04: `packload.patchedTreeClaimDetail`, review-marked, and its disclosure sentence, printed at every launch ([PPX-D29](#PPX-D29)) |
| <a id="PPX-D16"></a>PPX-D16 | *Implementation decision.* **A series member that adds a path the upstream ignores at the base is said once, naming `build`** | 2026-10-04 | [§7.2](#72-identity-and-selection) | — (left for a later step: it needs the upstream's `.gitignore` at the base, read in the replay's scratch repository) |
| <a id="PPX-D17"></a>PPX-D17 | *Implementation decision.* **The check record is per extension key, as [PF-D9](patched-forks.md#PF-D9)'s is per fork key, never per declaration** | 2026-10-04 | [§14](#14-alternatives-and-what-this-does-not-cover) | S5, 2026-10-04: the check record under the extension key (`packsrc.CheckRecord`) |
| <a id="PPX-D18"></a>PPX-D18 | *Decided under delegation, on [OQ-PPX1](#OQ-PPX1)'s leaning.* **The good build serves; with none, the owning agent pack's launchers stop before exec and say why; notches that never build trees are exempt** *Amended 2026-10-05 by [PPX-D40](#PPX-D40):* with none at a notch that builds trees, a fresh launch refuses before booting, so the launchers' stop is the backstop for an attach and a nested launch; the exemption stands | 2026-10-04 · amended 2026-10-05 | [OQ-PPX1](#OQ-PPX1) | S5, 2026-10-04: in a jail ([PPX-D24](#PPX-D24)) and at the host ([PPX-D26](#PPX-D26)) |
| <a id="PPX-D19"></a>PPX-D19 | *Decided under delegation, on [OQ-PPX2](#OQ-PPX2)'s leaning.* **A patched extension writes no pin to `packs.lock.json`; its good build is machine-local** | 2026-10-04 | [OQ-PPX2](#OQ-PPX2) | S5, 2026-10-04: nothing writes one; `packs.lock.json` is untouched |
| <a id="PPX-D20"></a>PPX-D20 | *Implementation decision, building [§16](#16-what-i-would-build-in-order) step 3 under [PPX-D5](#PPX-D5), reversible.* **The reserved directory is `~/.local/share/yolo-tree/<name>`. The tree's build jail writes its toolchain record first — the image's identity and the version of the image's own `node` and `npm` — then runs the build line, if any, in a subshell with `/bin:/usr/bin` first on PATH, so `node` and `npm` are the image's and a `cd` in the line cannot move the copy's source — on lines of its own inside the subshell, so a trailing `# comment`, which a fork's build line may carry, cannot reach the copy — and ends in `cp -a` of the checkout into the reserved directory.** A tree's build jail runs as a fork's does, as a child under the interrupt scope while a good build serves (`yolo internal fork-build-jail --tree=<name>`, whose build line may be empty) | 2026-10-04 | [§7.1](#71-the-build-act) | S5, 2026-10-04: `cli.treeBuildJailArgv`, `forkBuildChildArgv` |
| <a id="PPX-D21"></a>PPX-D21 | *Implementation decision, under [PPX-D6](#PPX-D6), reversible.* **A tree's `build` receipt records the extension's name as its `bin` and the extension key as its `fork`, so selection and `yolo prune` file it under (name, platform, extension key) with no new receipt field: a record naming no bin is dropped by every store scan, and would be reaped by the next prune.** The admit counts any reference the content scan records under the tree, any file it says embeds the home, and a scan that did not run, as a reference to the build's home | 2026-10-04 | [§7.2](#72-identity-and-selection) | S5, 2026-10-04: `cli.buildForkUnderLock`, `treeAdmitProblem` |
| <a id="PPX-D22"></a>PPX-D22 | *Implementation decision, under [PPX-D7](#PPX-D7), reversible.* **The per-launch copy is `<tree>.patched/<key, "/" written "--">-<8 hex digits of the key's sha256>`, beside the pack tree, made by `capture.CopyTree` (reflink, else copy, never a hardlink; the manifest's modes; every directory keeping its owner's write bit, so the copy can be removed) and removed with its tree. The record's one re-read is the advance run again, which finds the good build a move left or rebuilds the one that went. A move reaps every other build of a tree whatever a delivery record names, since jails hold copies** | 2026-10-04 | [§8.1](#81-in-a-jail) | S5, 2026-10-04: `run.PatchedCopySlug`, `cli.copyTreeForLaunch`, `cli.advance.handedKeys` |
| <a id="PPX-D23"></a>PPX-D23 | *Implementation decision, under [PPX-D8](#PPX-D8), reversible.* **`YOLO_PATCHED_TREES` carries, per extension key, `into`, the handed entry and its label or the reason there is none, the owning agent pack, and `stop`. The delivery record beside the pack tree gains a `trees` map, additive, which an attach reads** | 2026-10-04 | [§8.1](#81-in-a-jail) | S5, 2026-10-04: `entrypoint.TreeDelivery`, `run.HandedTree` |
| <a id="PPX-D24"></a>PPX-D24 | *Implementation decision, under [PPX-D18](#PPX-D18) and [PPX-D12](#PPX-D12), reversible.* **A jail's `stop` is set when nothing serves, the owner's list entry reaches a jail (a `config-list`, or an autonomous posture list), and the notch builds trees. A nested launch inside a jail and Apple Container below its read-only floor build none, so they start the agent with a line, as macos-user does. The gate is baked into each of the owner's launchers (npm, native and a fork's source launcher) as `Install.Gate`, immediately before the exec and after the install and the refresh.** The launch-flags wrapper, which carries a program the image provides, carries no gate: no owning agent's program is baked today. *Amended 2026-10-05 by [PPX-D40](#PPX-D40):* a fresh launch where this `stop` would be set refuses before booting instead, so the baked gate is the backstop for an attach; a nested launch and Apple Container below its floor still start the agent with a line | 2026-10-04 · amended 2026-10-05 | [§9](#9-failure-and-the-next-step) | S5, 2026-10-04: `run.Options.patchedTreesWire`, `entrypoint.treeGateFor`, `treeGateShell` |
| <a id="PPX-D25"></a>PPX-D25 | *Implementation decision, under [PPX-D11](#PPX-D11), reversible.* **The host's versioned copies are `~/.local/share/yolo-jail/host-trees/<slug>/<entry key>`, the directory 0700 and never mounted by a jail; a copy is made into a temporary directory beside it, checked against the entry's completion marker and renamed in. The render keeps the version the link names and the one it named before the swap, and removes older ones; an apply removes a dropped extension's copies once its link is retired and no recorded link names them. The advance runs at `yolo host apply --assert` and `yolo apply --at host --assert`, never a dry run, and at `yolo host -- <bin>` only under `host_apply_on_launch` with `host_management` not `none`, and only when the owning agent pack declares `<bin>`.** A macOS host's render refuses each patched extension with the line naming `YOLO_RUNTIME=podman yolo -- <the owner's first program>`, and `yolo host -- <bin>` of an owning agent there says once per tree its entry reaches the host with that the agent starts without it, naming `YOLO_RUNTIME=podman yolo -- <bin>` | 2026-10-04 | [§8.3](#83-at-the-host) | S5, 2026-10-04: `paths.HostTreesDir`, `cli.renderHostTree`, `advanceHostTrees`, `sweepDroppedHostTrees`, `noteHostTreeLines` |
| <a id="PPX-D26"></a>PPX-D26 | *Implementation decision, under [PPX-D18](#PPX-D18), reversible.* **At the Linux host, `yolo host -- <bin>` of an owning agent stops before exec when a tree its list entry reaches the host with (a `config-list`, or a guarded posture list) names no directory at `~/<into>`, whatever `host_apply_on_launch` says, with the reason the check record gives; under `host_management: none`, which writes no link, it stops nothing. It reads the link rather than the record, since what the agent loads at the host is what the link names; and it prints a line naming the build the link names for each tree the program loads, and, when the machine's good build has moved past it, that `yolo host apply --assert` renders the good one; under `none` it prints none** | 2026-10-04 | [§9](#9-failure-and-the-next-step) | S5, 2026-10-04: `cli.hostTreeGate`, `noteHostTreeLines`, `hostTreeLine` |
| <a id="PPX-D27"></a>PPX-D27 | *Implementation decision, under [PPX-D10](#PPX-D10), reversible.* **The lint is a warning at `yolo pack lint`, never a failure, and a line in every launch's "Patched extensions this launch" block, an attach's included. `yolo pack lint` counts a patched fork's or a patched extension's series directory as content the pack ships** | 2026-10-04 | [§8.2](#82-how-pi-finds-it) | S5, 2026-10-04: `cli.packLint`, `run.Options.notePatchedTrees` |
| <a id="PPX-D28"></a>PPX-D28 | *Implementation decision, under [PF-D12](patched-forks.md#PF-D12) and [PF-D22](patched-forks.md#PF-D22), reversible.* **The explicit acts take the extension key: `yolo capture <pack>/<name>` (a name with a slash routes there only where the selection declares a patched extension, so it is otherwise refused as no program's), and `yolo pack update`, `install` and `status`. The shared implementation's lines name a patched extension "extension `<key>`" (`packload.Fork.Label`) where a fork's say "fork `<key>`", and "the host" where a launch's say "this jail"** | 2026-10-04 | [§6.1](#61-where-the-check-runs) | S5, 2026-10-04: `cli.captureTree`, `pinForks`, `forkStatusLines` |
| <a id="PPX-D29"></a>PPX-D29 | *Implementation decision, under [PPX-D15](#PPX-D15), reversible.* **A patched extension's review-marked claim is disclosed at every launch with the banner's block, routed per claim as a patched fork's `program` claim is, and `files` is marked in the kind registry as able to produce a review-worthy claim.** Not the jail-code block a wrapped plugin's components take, which counts per pack, since the claim's sentence names what moves the tree and what runs it | 2026-10-04 | [§10](#10-trust-and-disclosure) | S5 review, 2026-10-04: `packload.Claim.IsPatchedExtension`, `run.disclosureClassOfClaim` |
| <a id="PPX-D30"></a>PPX-D30 | *Implementation decision, under [PPX-D5](#PPX-D5), reversible.* **A tree's sealed build jail is told which extension it builds, `YOLO_TREE_BUILD=<name>`, and its boot names no orphaned overlay or list**: the seal selects the contributing pack alone, so the pack's own entry naming `~/<into>` has no owner there by construction. Additive across the host↔jail contract: an older entrypoint names the orphan, as before | 2026-10-04 | [§7.1](#71-the-build-act) | S5 review, 2026-10-04: `entrypoint.TreeBuildEnv`, `run.Options.SealedTree` |
| <a id="PPX-D31"></a>PPX-D31 | *Implementation decision, under [PPX-D11](#PPX-D11), reversible.* **`yolo host apply --revert` removes every link the `files` ownership record names into the host's versioned copies, forgets it, and removes every versioned copy no recorded link names, whatever the selection carries; its dry run names each link.** A plain `files` tree's output is left, as the revert always left it | 2026-10-04 | [§8.3](#83-at-the-host) | S5 review, 2026-10-04: `cli.revertHostTreeLinks` |
| <a id="PPX-D32"></a>PPX-D32 | *Implementation decision, under [PF-D13](patched-forks.md#PF-D13) and [PPX-D28](#PPX-D28), reversible.* **`yolo pack rebase` takes a patched extension's owner key `<pack>/<name>` as it takes a patched fork's: it clones the extension's upstream, stops at the conflict and exports into the contributing pack's own `patches` directory, a local pack's in place and a fetched pack's through a clone of its repository. Its lines name the subject "extension `<key>`" and its pack "pack `<name>`", and with no key, or a wrong one, it lists the selected patched forks and patched extensions apart.** Found integrating the parallel builds: step 3 built the verb over `packload.Forks`, which never lists an extension, while every conflict line an extension's check prints names the verb with its key, so following the line was refused | 2026-10-04 | [§9](#9-failure-and-the-next-step) | S6, 2026-10-04: `cli.packRebase`, `pickRebaseFork`; pinned by `TestPackRebaseRebasesAPatchedExtensionsSeries`, which runs the command `yolo pack update` prints, and `TestPackRebaseNamesThePatchedExtensions` |
| <a id="PPX-D33"></a>PPX-D33 | *Implementation decision, under [PPX-D25](#PPX-D25) and [PPX-D28](#PPX-D28), reversible.* **The host apply `yolo pack update` runs builds no patched extension: the render links the good build already on this machine, and an extension with none says `yolo host apply --assert` builds it ([`patched-forks.md` PF-D56](patched-forks.md#PF-D56), which rules it for patched forks too)** | 2026-10-05 | [§8.3](#83-at-the-host) | S6, 2026-10-05: `cli.hostApplyDeferring`; pinned by `TestPackUpdatesHostApplyBuildsNoPatchedExtension` |
| <a id="PPX-D34"></a>PPX-D34 | *Implementation decision, amending [PPX-D10](#PPX-D10), reversible.* **`yolo pack lint` warns when one list loads a patched extension twice: the list holds an entry that loads the tree by [PPX-D36](#PPX-D36)'s rule, `~/<into>` or a path inside it, and also holds a remote entry of the same final name, and the warning names the entry to drop. A remote entry is a string, or an object's `source`, that opens with `git:` or `npm:` or names a URL. Its final name is the last segment of its path, after an npm scope, without a `.git`, and with the path cut at its first `@`, which opens an `@<ref>` or `@<version>` as pi splits it, so a ref that holds a slash, `@feat/x`, leaves the package's own name. It matches the extension's own name (the last segment of `into`) or its upstream's (the source's subdirectory, else its repository). One list is one surface and path, across every list body of the pack whose notches meet: a `config-list` meets either posture's list, and the two postures' lists never meet. This is a warning, never a failure, and it is said at lint only, not at a launch: the agent still starts, and the launch's block keeps one line per extension.** PPX-D10 left this case to the guide, because telling every old entry apart needs pi's whole package-source grammar. Equality of final names is the narrower question the pi pack's subagents render already asks (`whenListed.matches`). The maintainer asked for it on 2026-10-05 | 2026-10-05 | [§8.2](#82-how-pi-finds-it) | 2026-10-05: `packload.LintDuplicateLoads` ([`duplicateloads.go`](../../internal/packload/duplicateloads.go)), called by `cli.packLint`; pinned by the `TestTheDuplicateLoadLint…` tests and `TestPackLintWarnsOfAnExtensionLoadedTwice`; integration, 2026-10-05: the ref cut and [PPX-D36](#PPX-D36)'s rule, pinned by `TestTheDuplicateLoadLintNamesARemoteEntryWhoseRefHasASlash` and `TestTheDuplicateLoadLintReadsTheTreeAsAnOwnerDoes` |
| <a id="PPX-D35"></a>PPX-D35 | *Implementation decision, under [PPX-D12](#PPX-D12), reversible.* **A patched extension is built and mounted only at a notch its owning agent pack's list entry reaches. A jail launch hands a tree listed only in the guarded posture to no tree arm, so no jail builds it, mounts it or is told of it, nothing stops there, and the launch's block names it with where it goes instead. The host's advance and render, at `yolo host apply` and `yolo host -- <bin>`, skip a tree listed only in the autonomous posture, and the render retires a link to it that an earlier render left, with its versioned copies, as a revert does ([PPX-D31](#PPX-D31)).** A tree with no owning agent pack names no notch, so it is still delivered at every notch and the lint says no agent loads it: the mechanism delivers any home-relative tree, and a reader that keeps no list may load one. The explicit acts (`yolo capture <pack>/<name>`, `yolo pack update`) are unchanged. Asked for in the patch-series requests' item 6, for `pi-automode`, which every jail built and mounted for a pi that never loads it | 2026-10-05 | [§9](#9-failure-and-the-next-step) | S7, 2026-10-05: `packload.Fork.DeliveredInJail`, `DeliveredAtHost`, `run.jailDeliveredTrees` (at `notePatchedTrees`' return), `cli.advanceHostTrees`, `retireHostTreeLink`; pinned by `TestAGuardedOnlyTreeIsNeitherBuiltNorMountedInAJail`, `TestHostApplyInstallsAGuardedOnlyTree`, `TestHostApplyBuildsAndLinksNoTreeListedForJailsAlone` and `TestHostApplyRetiresTheLinkOfATreeThatNoLongerReachesTheHost` |
| <a id="PPX-D36"></a>PPX-D36 | *Implementation decision, amending [PPX-D4](#PPX-D4) and [PPX-D10](#PPX-D10), reversible.* **A list entry loads a tree when, cleaned as a path, it is `~/<into>` or lies inside it. `~/<into>/` and `~/<into>/packages/session-name` count, for the owning agent pack, where its entry reaches, the launchers' stop and the lint; `~/<into>-other` and `~/<into>/../x` do not.** Core still reads none of pi's grammar beyond the path. Asked for in the patch-series requests' item 7, so a monorepo's tree such as `pi-archimedes` can load two of its packages without a series member that edits its root manifest | 2026-10-05 | [§8.2](#82-how-pi-finds-it) | S7, 2026-10-05: `packload.loadsTree`, read by `listAdds`; pinned by `TestAListEntryUnderTheTreeLoadsIt` and `TestATreeListedForJailsIsDeliveredAndAnEntryUnderItLoadsIt` |
| <a id="PPX-D37"></a>PPX-D37 | *Implementation decision, under [`patched-forks.md` PF-D63](patched-forks.md#PF-D63), reversible; found integrating the patch-series requests' parallel builds.* **A patched extension's series is read by the same reader as a patched fork's, and its errors name the extension's own remedies: "the extension's `patches`", "a patched extension applies at least one", and for an empty series the export alone, never "declare a plain fork instead", since a `files` contribution has no unpatched form to declare in its place.** The launch's line showed the fork's remedy for an extension from the first build; `yolo pack lint` reading every series made it the step lint names | 2026-10-05 | [§9](#9-failure-and-the-next-step) | Integration, 2026-10-05: `packsrc.ReadTreeSeries`, read by `packload.Fork.ReadSeries` for a tree; pinned by `TestPackLintFailsASeriesALaunchCannotRead` |
| <a id="PPX-D38"></a>PPX-D38 | *Implementation decision, under [PPX-D35](#PPX-D35), reversible; found in review integrating the patch-series requests' parallel builds.* **On a host that builds no tree for itself, a macOS host, a tree whose list entry is in a guarded posture list reaches no notch that has it, and each line that names it says so with the step that works: move the entry to a `config-list` to load it in a jail, or drop it. A jail launch's block warns so in place of naming `yolo host apply --assert`, and the host render's refusal and `yolo host -- <bin>`'s line name a jail only when the entry reaches one.** Before, the jail's line sent the user to `yolo host apply --assert`, which on a Mac refuses the tree and sends the user to a jail, which PPX-D35 builds nothing in | 2026-10-05 | [§8.3](#83-at-the-host), [§9](#9-failure-and-the-next-step) | Integration, 2026-10-05: `packload.NotDeliveredAnywhereNote` and `GuardedOnlyStep`, `run.hostBuildsOwnTrees` read by `patchedTreeLine`, `cli.noHostTreeStep` read by `renderHostTree` and `noteHostTreeLines`; pinned by `TestAGuardedOnlyTreeOnAMacOSHostNamesAStepThatWorks`, `TestAMacosUserLaunchIsSilentOnAGuardedOnlyTree` and `TestAMacOSHostNamesAStepThatWorksForAGuardedOnlyTree` |
| <a id="PPX-D39"></a>PPX-D39 | *Implementation decision, under [PPX-D5](#PPX-D5) and [FP-D9](forked-programs-as-packs.md#FP-D9), reversible.* **The seal does not trip over its own narrowing. A build jail whose pack selection the seal narrowed runs none of the launch gates that ask whether another selected pack provides what one pack names: an `agents` selector's agent, a `supersedes` claim's capability, a via profile's route and a required capability; nor does it print the report of content addressed to no destination. Each protects an agent the jail runs, a build jail runs none, and the user's own launches still run every one over the whole selection. The one such refusal no skip can answer, a fork whose base is not selected, which the jail's own loader repeats, is answered by the seal carrying the base of every fork the contributing pack declares, and in turn every base those bases fork. A build jail that stops before its build line runs is relayed with the last lines it printed, up to three from the stream it printed last on, which are its own account of why: of the jail's own two streams, what its runtime and its boot printed, when either printed, since the launch's lines after them are its keeper's report of the end, and of the launch's two otherwise, where a launch that refused before it started a container said why. A jail whose boot was done is relayed with nothing, since a boot that succeeds prints lines too and the session that stopped after it printed why on the terminal, so the step points at the output above. The next step is to fix what they name, then `yolo capture <key>`, and no line names the runtime unless the jail's own do.** Found at the maintainer's first real launch: the `matt` pack's briefing names `agents: ["pi"]`, the seal narrowed each of its five extensions' builds to `matt`, and every build jail refused before its build line, while the line said `yolo capture` would build it "once the runtime starts jails again". Corrected in review the same day: a build run in yolo's own process kept only the launch's lines, the jail's own going to the terminal, so a runtime's refusal was relayed as `keeper: started, pid N / keeper: done`; and the seal carried the contributing pack's own bases alone, so a base that itself forked a configured pack's program was refused for lacking it | 2026-10-05 | [§7.1](#71-the-build-act) | 2026-10-05: `run.Options.selectionNarrowed` at the gates of `stagePacksInto` and at `refuseUnmetCapabilities`; `packload.Fork.PackBases`, read by `cli.sealPacks`; `cli.forkBuildNotStarted`, kept by `jailTail` over the jail writers `run.Options.JailStdout` and `JailStderr` and forgotten at `run.Options.OnJailReady`, which a child build jail hands back on its fds 3 to 5; pinned by `TestABuildJailsNarrowingIsNotRefusedForWhatItDropped`, `TestAForkBuildJailsNarrowingIsNotRefusedForWhatItDropped`, `TestABuildJailsNarrowingIsNotRefusedForAViaRoute`, `TestAForkBuildsNarrowedSelectionIsNotRefusedForACapability`, `TestATreesSealCarriesTheConfiguredBaseOfItsPacksFork`, `TestATreeBuildJailThatRefusedIsRelayedWithItsRefusal`, `TestATreeBuildJailsSeveralLineRefusalIsRelayedFromItsStream`, `TestCaptureOfATreeWhoseJailRefusedRelaysTheRefusal`, `TestABuildJailThatNeverRanRecordsNothing`, `TestTheJailsOwnLinesGoToItsJailWriters`, `TestCaptureOfATreeTheRuntimeRefusedRelaysTheRuntimesError`, `TestATreeBuildJailTheRuntimeRefusedIsRelayedWithTheRuntimesError`, `TestCaptureOfAForkTheRuntimeRefusedRelaysTheRuntimesError`, `TestTheLaunchSaysWhenTheJailsBootIsDone`, `TestABuildJailThatStoppedAfterItsBootRelaysNothing`, `TestTheChildBuildJailsOwnStreamsCrossApart`, `TestTheChildBuildJailRelaysItsJailToTheDescriptorsItWasHanded`, `TestPackBasesFollowAConfiguredBasesOwnForks`, `TestATreesSealCarriesItsForkBasesOwnBases` and `TestAForkBuildJailSealedToEveryBaseDownTheChainIsAccepted` |
| <a id="PPX-D40"></a>PPX-D40 | **Ruled in review ([OQ-PPX3](#OQ-PPX3)), none of the options as written; supersedes [PPX-D12](#PPX-D12)'s "the jail launch is never refused".** *"if a build fails, of course that's fatal. we should offer solutions and workarounds when that happens for happy path, but it's fatal"*. **A fresh jail launch refuses before booting when a patched extension a selected pack needs has no build to deliver: its owning agent pack's list entry reaches this notch ([PPX-D35](#PPX-D35)), the notch builds trees, and no good build of it is on this machine, because its build failed or never ran. It applies whatever the command is, since yolo never reads the command line ([HP-DIR2](host-tool-provisioning.md#HP-DIR2)), and it is decided where the trees are, before the image step.** The refusal names the extension, the cause and the ways back: the fix the build's own failure names; `yolo pack series check` and `yolo pack rebase <pack>/<name>` for a series that no longer applies; `yolo capture <pack>/<name>` to retry; dropping the list entry, or the pack, to run without it; and the opt-in bypass variable [OQ-JR1](jail-notch-readiness.md#OQ-JR1) rules for a program a launch could not install, if one variable fits both, which is the builder's call. The launchers' stop ([PPX-D18](#PPX-D18), [PPX-D24](#PPX-D24)) stays as the backstop for an attach and a nested launch. *Recorder's reading, for the maintainer to confirm:* a newer build that fails while the good build serves ([PPX-D14](#PPX-D14)) serves with today's held suffix and refuses nothing, and a notch that never builds a tree keeps [PPX-D18](#PPX-D18)'s exemption and its line. The fork side is [`patched-forks.md` PF-D77](patched-forks.md#PF-D77) | 2026-10-05 | [OQ-PPX3](#OQ-PPX3); [§9](#9-failure-and-the-next-step) | pending |
