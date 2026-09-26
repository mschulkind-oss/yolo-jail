---
title: "Sharing pi git extensions across jails: immutable per-commit trees, never one shared checkout"
date: 2026-09-25
status: accepted
tags: [pi, extensions, git, caching, machine-tier, storage, isolation]
summary: "pi's git extensions cost every new jail a clone and a dependency build. The first build shared one mutable checkout per repository across jails, which let one jail's pin or update change the files another jail was running; the maintainer's rulings of 2026-09-25 withdraw it. The redesign shares content, never state: a machine store of bare mirrors and one immutable tree per resolved commit, each jail pointing at the commit its own config resolves to, pi loading each tree as a local package so it never clones or updates one itself. A launch waits for the tree it needs and never boots on another launch's leftovers. One question is open: whether the npm store gets the same treatment now."
vantage:
  status-chip: true
---

# Sharing pi git extensions across jails: immutable per-commit trees, never one shared checkout

**Status:** DECIDED, 2026-09-26 — **redesigned after review on 2026-09-25**, and [OQ-5](#OQ-5) ruled on 2026-09-26 (npm gets the same design, git first). The redesign is not
built. What `c402dd43` built from the first draft is half withdrawn: its `.pi-shared-git` shared
checkout is REVERTED, with the boot step that removes the link it left BUILT
([§3.10](#310-migration-from-what-c402dd43-shipped)), and its `due_on_change` refresh trigger
stays ([§3.12](#312-the-refresh-trigger-that-stays)). Until the redesign is built, each workspace
clones its own git extensions again, as before `c402dd43`. **MEASURED:**
pi 0.87.1's package manager, read (not run) at `dist/core/package-manager.js` in the jail's install.
**UNMEASURED:** nothing has run against a real pi git extension; every cost figure is an estimate.

> **In short.** Jails may share what is identical, never what one of them can change. So the
> machine keeps one immutable tree per resolved commit, each jail points at the commit its own
> config asks for, and pi loads that tree as a local package it never updates.

**Why it matters.** Without sharing, every new jail pays a full clone and `npm install` per git
extension (an estimated 30–60 s for a few; unmeasured). The first build removed that cost by
sharing one working copy per repository, and in doing so let jail A's `git reset --hard` or
different pin change the files jail B's running pi was using.

**The shape.** A machine store of bare **mirrors** and per-commit **trees**; per-workspace
**pointers**, one per extension, each a link to a tree; pi's settings naming the pointer as a
local package; and the pi launcher resolving each pointer before pi starts.

**Cost.** A second store beside the npm store, a new post-fold hook in the pack language
([§3.2](#32-pointing-pi-at-a-tree)), and a launch that now waits, visibly, for a clone or build
it needs. With the first build reverted and the redesign unbuilt, each workspace clones its own
git extensions again, as it did before `c402dd43`.

**Start at [§3](#3-the-design--share-content-never-state)**, the store and how a jail reaches it.

**Needs your ruling:** none. [OQ-5](#OQ-5) is ruled: the npm store gets the same immutable-tree design, built after git in the same build.

**Reads with:**
- [`pack-pi-resources.md`](pack-pi-resources.md): pack-shipped trees registered as local pi
  packages. Both designs make pi load yolo-managed content as **local** packages, which pi never
  installs or updates; the two must leave each other's `packages` entries alone.
- [`pi-extension-lifecycle.md`](pi-extension-lifecycle.md): the npm store and the pre-launch
  refresh this builds on.
- [`image-retention.md`](../reference/image-retention.md): the reaper rules
  [§3.8](#38-garbage-collection) follows.
- [`pi-git-extension-caching-plan.md`](pi-git-extension-caching-plan.md): the implementation
  sketch. It is incomplete, and nobody builds from it.

---

## Defined terms

All four are coined here.

- **Mirror**: a bare git repository per extension repository, in the machine store. It is only
  ever fetched into, never checked out.
- **Tree**: one extension at one resolved commit, checked out with its dependencies installed,
  complete and then never modified. Its key is the commit plus the dependency recipe
  ([§3.4](#34-building-a-tree)).
- **Pointer**: a per-workspace symbolic link naming the tree one extension resolved to in that
  workspace. pi's settings name the pointer, never a tree.
- **Recipe**: the exact dependency-install command a tree was built with, and the Node it ran
  under.

## 1. Principles

- **P1. Share content, never state.** Two jails that need identical bytes share one copy. Nothing
  one jail does may change what another jail reads ([OQ-4](#OQ-4)).
- **P2. A launch gets what its own config calls for.** Its result does not depend on what other
  launches did or are doing. If what it needs is being produced, it waits and then uses it; it
  never boots on whatever happened to be installed ([OQ-2](#OQ-2)). This replaces the first
  draft's "launch outranks updating".
- **P3. No winner.** Two jails asking for different versions each get their own ([OQ-3](#OQ-3)).
- **P4. Never clone what the machine already has.** A repository on the machine is fetched, not
  cloned again.
- **P5. No vendor code modification.** pi is not forked or patched. Everything works through pi's
  public contracts: its settings file and its local-package loading.

## 2. How pi handles git packages today

From pi 0.87.1's `dist/core/package-manager.js`:

| Situation | What pi does | Where |
| :--- | :--- | :--- |
| A `git:<host>/<path>[@<ref>]` entry in `packages`, user scope | Installs to `~/.pi/agent/git/<host>/<path>`: `git clone`, `git checkout <ref>` when pinned, then the dependency step | `installGit`, `getGitInstallPath` |
| The dependency step | If the checkout has a `package.json`: `<npmCommand> install` when the settings set `npmCommand`, otherwise `npm install --omit=dev` | `getGitDependencyInstallArgs` |
| A failed install | `rmSync(targetDir, { recursive: true, force: true })` | `installGit`'s catch |
| `pi update` / `pi update --extensions` | Every `git:` entry, pinned or not: `git fetch`, then `git reset --hard <commit>`, `git clean -fdx` (which deletes `node_modules`), and the dependency step again when the commit moved | `updateConfiguredSources`, `updateGit` |
| A **local** entry (a path) in `packages` | Loaded in place from the path; **never installed, never updated, never fetched**. A path that does not exist is skipped | `resolveLocalExtensionSource`; `updateConfiguredSources` takes npm and git only |
| A local directory | Loaded as a pi package: its `package.json` `pi` key, else its conventional `extensions/` `skills/` `prompts/` `themes/` folders, else the directory as one extension | `collectPackageResources` |
| Duplicate entries | One per identity: npm name, git host and path, or a local path's resolved absolute path | `getPackageIdentity` |
| A project-scope entry (`.pi/settings.json` in the repository) | Installs under the workspace's own `.pi/git`, after project trust | `getBaseDirForScope` |

Two facts carry the design. **A local package is never touched by pi's updater**, so a tree pi
loads as a local package cannot be mutated by pi. And **pi's own updater rewrites a git checkout
in place**, so any checkout pi manages cannot be shared.

### 2.1 What the first build did, and why it is withdrawn

`c402dd43` declared `.pi-shared-git`, a machine-scoped directory, and a `shared_directory` hook
that made every workspace's `~/.pi/agent/git` a link to it. Every jail's pi then cloned into, and
updated, one working copy per repository. That fails all three rulings:

- **No winner ([OQ-3](#OQ-3)).** Workspace A's `@v1` and workspace B's `@v2` are one directory; the
  last `pi update` decides which commit both run.
- **No leakage ([OQ-4](#OQ-4)).** A's update runs `git reset --hard` and `git clean -fdx` in the
  directory B's pi is running from, and B's extension has no `node_modules` for the whole install.
- **A launch's own result ([OQ-2](#OQ-2)).** The first draft let a launch that found the refresh
  lock held boot on whatever was installed.

The interim, until this redesign is built, is the pre-`c402dd43` behavior: a per-workspace
`~/.pi/agent/git`, cloned per workspace. It is slow, and it honors every ruling.

## 3. The design — share content, never state

### 3.1 The store

A machine-scoped state directory of the pi pack, `.pi-git-store`, mounted read-write at
`/home/agent/.pi-git-store` in every jail that selects pi:

```
.pi-git-store/
├── mirrors/<repo-slug>.git        bare mirror per repository, fetch only
├── trees/<commit>-<recipe>/       one extension at one commit, dependencies installed
│   └── .yolo-tree-complete        written LAST; a tree without it does not exist
├── tmp/                           builds in progress, renamed into trees/ when complete
├── locks/                         mirror-<slug>.lock, tree-<key>.lock
└── stamps/<repo-slug>/<ref>       time of the mirror's last successful fetch of that ref
```

It reuses `internal/packsrc`'s machinery, which already keeps a bare mirror per repository and a
checkout per commit for git packs:
- the mirror layout and slug;
- the fetch flags: fsck on transfer, terminal prompts disabled, and git's inherited state
  stripped;
- the per-mirror lock;
- the fetch stamp and its one-hour branch interval;
- the reset of a moved tag after a launch-time fetch.

What differs: the dependency step, the recipe in the key, the build-then-rename, and the
read-only finish. A tree is made read-only once complete (`chmod -R a-w`). That protects against
an accidental write, not a hostile one. The store is writable by every pi jail, which is the same
trust the npm store already extends; [§4](#4-invariants-and-done-conditions) states the limit.

### 3.2 Pointing pi at a tree

pi must load a tree without ever installing or updating it, so pi must see a **local** package.
The jail's composed `~/.pi/agent/settings.json` therefore never contains a `git:` entry for a
yolo-managed extension. Each is rewritten to a pointer path:

```
git:github.com/mschulkind/pi-archimedes@main
  →  ~/.pi/agent/yolo-git/github.com/mschulkind/pi-archimedes/@main
```

The ref is encoded into the pointer's last path segment (`@main`, `@v1.2.0`, `@<sha>`, `@HEAD` when
the entry names none; a `/` in a ref is escaped). So the pointer path is stable across commits, and
the settings file's content moves only when the extension list moves.

**Who rewrites.** The pi pack, through a new post-fold hook: `yolo.finalize("pi", "settings", fn)`
in `packs/pi/derive.lua`. Core calls it on the fully folded document, after every layer and every
`config-list` entry, and writes what it returns. It exists because a derive cannot do this: a
derive writes the `computed` layer, which never sees the host layer or the user's captured edits,
and replacing an array there would erase the user's list ([`pack-pi-resources.md` §2.3](pack-pi-resources.md#23-how-pis-packages-list-is-composed)
found the same wall for appending). Core never parses pi's `git:` grammar. The pack does, in
its own code, and emits pointer paths, which are yolo's grammar.

**Scope.** Only the user-scope surface is rewritten. Project-scope `git:` entries in a repository's
`.pi/settings.json` install under that workspace's own `.pi/git`, which no other jail sees, and pi
keeps managing them itself. Local entries, `npm:` entries and the registrations of
[`pack-pi-resources.md`](pack-pi-resources.md) pass through untouched.

**A user edit.** `pi install git:X` inside a jail writes `git:X` into the file and clones into the
workspace's own `~/.pi/agent/git` for that session. Capture records the new entry as the user's.
The next launch renders it as a pointer and resolves it like any other.

### 3.3 Resolving at launch

pi's pack declares one more pre-launch step, beside its existing refresh. The pi launcher runs it
before every pi exec in the jail; nothing runs at boot. It invokes the in-jail `yolo` binary to
read the pointers the rendered settings name and to resolve each one:

1. **Fetch when due.** A branch or `HEAD` ref is due when its stamp is older than one hour, or when
   the ref does not resolve in the mirror. A tag or a full commit is due only when it does not
   resolve. A mirror that does not exist yet is cloned.
2. **Resolve the ref to a commit** in the mirror. This is a local lookup that runs on every launch,
   whether or not a fetch was due. So two launches at the same moment resolve the same commit from
   the same mirror, and neither depends on which of them fetched.
3. **Ensure the tree** for that commit and this jail's recipe exists, building it if not
   ([§3.4](#34-building-a-tree)).
4. **Repoint** the pointer at the tree: a new link beside the old one, renamed over it, so the
   pointer is never missing or half-written.

Then pi execs. The per-machine stamp throttles only the network. A launch always re-resolves
against the mirror as it is, including a fetch another jail made a minute ago. That is the
result the launch would have produced by fetching itself.

### 3.4 Building a tree

A tree is built exactly as pi 0.87.1 would install the same commit, so an extension behaves the
same as when pi installs it:
1. A checkout of the commit from the mirror, in a fresh directory under `tmp/`.
2. The dependency step, only if the checkout has a `package.json`. It is the settings'
   `npmCommand` plus `install` when `npmCommand` is set, else `npm install --omit=dev`. The pi pack
   declares where `npmCommand` lives and the default argv, so core runs a declared command rather
   than knowing pi's settings.
3. A completeness check: the checkout exists, and the dependency step exited 0.
4. `chmod -R a-w`, then `.yolo-tree-complete`, then a rename to `trees/<commit>-<recipe>/`.

**The recipe key** is a hash of the dependency argv as configured, the output of `node --version`
run under that argv's environment, and the OS and architecture. So a jail running `mise exec
node@24` and a jail on node 22 never share native modules, and two jails with the same recipe
always do.

A build that fails deletes its `tmp/` directory and leaves no tree. A rename that finds the tree
already present, because another launch finished the same key first, discards its own copy and
uses the existing one: the two are identical by key.

### 3.5 Locks, waits and bounds

| Lock | Held for | Bound on the holder | A second launch that finds it held |
| :--- | :--- | :--- | :--- |
| `mirror-<slug>` | clone or fetch | 60 s, the launch-time fetch timeout | waits while the holder's heartbeat is fresh, at most the holder's bound plus one heartbeat, then re-resolves from what the holder left |
| `tree-<key>` | one tree's build | 600 s | waits the same way, then uses the tree the holder completed |

Locks are directories with a heartbeat every 60 s, the pre-launch refresh lock's shape. A lock with
no heartbeat for 600 s is stale and may be taken over. A waiter announces the wait on stderr, naming
the extension and what it is waiting for, because a launch parked in silence reads as a hang.

**When the holder fails**, the waiter does not inherit the failure. It retries the step once itself.
So a launch's result is its own even when another launch's build broke ([OQ-2](#OQ-2)).

### 3.6 Failure paths

The rule, from [OQ-2](#OQ-2): **a launch that cannot get what its config calls for does not start pi
on something else.** It stops before exec with one message naming the extension, the step that
failed, the build log's path, and the remedies (retry, or pin or remove the extension in settings).
The jail and its shell stay up.

| Failure | What the launch does |
| :--- | :--- |
| Offline; the mirror holds the ref | Warns that the fetch failed and resolves from the mirror as it is |
| Offline; no mirror, or the ref does not resolve | Stops before exec, naming the fetch error |
| The dependency step fails (network, a broken `package.json`, a native build) | Stops before exec. No tree is created, so the next launch retries |
| Disk full during a fetch or build | As the failed step above. The `tmp/` build is removed |
| A tree deleted or damaged after completion | Not detected per launch: the marker is trusted. pi reports the extension's load error. [§4](#4-invariants-and-done-conditions) states the limit |
| A crashed holder | Its lock goes stale after 600 s; a waiter then takes over |

### 3.7 Running sessions and moving pointers

A pointer moves only on its own workspace's launch; no other jail can see it. Within one jail, a
second pi launched while a first is running may repoint an extension. The first pi keeps its old
tree: Node resolves a module's symlinks to its real path when it loads it, and anything it loads
later resolves relative to that real path. So the old tree, which is immutable, stays what the
first session reads. This is INFERRED from Node's default symlink handling
(`preserveSymlinks: false`) and pi's loader; it is not measured.

### 3.8 Garbage collection

Every launch touches `.yolo-last-used` in each tree it points at and in each mirror it resolves
from. `yolo prune --apply` is the only reaper, and nothing reaps automatically in this design:
- a tree unused for 14 days is removed;
- a mirror with no tree left and unused for 14 days is removed;
- a `tmp/` build older than one day is removed, since no build outlives its 600 s bound.

This is an age rule, not a liveness rule. [`image-retention.md`](../reference/image-retention.md)
lets each reaper choose, and this one can afford to: a reaped tree costs one rebuild, never a
wrong result. The one exposure is a pi session running for more than 14 days without a relaunch
that then loads a file lazily. That is stated here rather than engineered away.

### 3.9 Notches and backends

- **Podman and Apple Container:** the store is a bind-mounted machine directory, as the npm store is.
- **macos-user:** the store lives in the sandbox account's machine tier, as the npm store does
  (`DeriveDarwinHomeLayout`), and the launcher is the same. UNVERIFIED: no Mac has run it.
- **`yolo host`:** no rewrite. `yolo.finalize` sees the notch and leaves `git:` entries alone. pi
  runs in the user's own home, manages its own git packages, and shares nothing with any jail.

### 3.10 Migration from what `c402dd43` shipped

**The revert, BUILT 2026-09-25:**
- `packs/pi/pack.json` drops the `.pi-shared-git` state and its `shared_directory` hook.
- `internal/entrypoint/shareddirgit_test.go` goes with the hook.
- `internal/packload/packproperties_test.go`'s `TestMachineGlobalTierStaysNarrow` drops
  `.pi-shared-git` from its list, along with the comment that explains it.
- `internal/cli/run/homeskeleton_test.go` may keep `.pi-shared-git` in its "must be absent" list,
  where it stays true.
- `due_on_change` stays ([§3.12](#312-the-refresh-trigger-that-stays)).

**Existing homes, BUILT 2026-09-25.** Every workspace that booted with `c402dd43` holds
`~/.pi/agent/git` as the link `linkIntoSharedDir` wrote: the RELATIVE target
`../../.pi-shared-git`, which nothing mounts after the revert, so the link dangles. A new generic
hook, `unshare_directory` (`from`, `at`), undoes exactly that link: at boot, a symlink at `from`
whose target is the one `linkIntoSharedDir` would compute for `at` is removed and replaced by an
empty real directory, and pi re-clones into it. A real directory, a link to anything else, or an
absent path are left alone, and the link is never followed, so the store is never read or
removed. `packs/pi` declares it as `{kind:"hook", hook:"unshare_directory",
from:".pi/agent/git", at:".pi-shared-git"}` in place of the retired pair
(`entrypoint.unshareDirectory`; tests in `internal/entrypoint/unsharedir_test.go`).

**The machine directory.** `.pi-shared-git` on the host
(`~/.local/share/yolo-jail/home/.pi-shared-git`) is left in place, per the move-over-delete rule,
and nothing reads or mounts it any more. No yolo command reports or reclaims it: delete it by hand
once every jail started before the revert has exited. Its checkouts do not seed the redesign's
mirrors.

### 3.11 The npm store

`.pi-shared-npm` is one mutable npm prefix shared by every pi jail, so it has the same fault. A
`pi update` in one jail replaces package files under another jail's running session, which is
[OQ-4](#OQ-4)'s leakage. Two jails pinning different versions of one package share one
`node_modules/<name>`, which is [OQ-3](#OQ-3)'s winner. The same shape fixes it: one tree per
package at a resolved version and recipe, `npm:` entries rewritten to pointers, and yolo resolving
versions. pi's own `pi update` would then have nothing left to touch in a jail. Whether that is done
now is [OQ-5](#OQ-5).

### 3.12 The refresh trigger that stays

`c402dd43`'s `due_on_change` makes pi's pre-launch refresh due whenever the settings content
changed since the last successful refresh, keyed per content
([`pack-system.md`](../reference/pack-system.md#program)). Git extensions no longer need it: pi
never installs them. It still serves the npm store until [OQ-5](#OQ-5) is ruled and built, because
an npm package newly added within the hour would otherwise be installed by pi's own startup,
unlocked. It is independent of the store's shape, so it stays either way.

## 4. Invariants and done conditions

**Invariants.**
- **I1.** No file a jail reads is changed by another jail. Trees are immutable; only pointers move,
  and only in their own workspace.
- **I2.** Two jails naming the same commit and recipe share one tree; two naming different ones
  never do.
- **I3.** A launch either runs pi on exactly the trees its config resolves to, or stops before exec
  and names why.
- **I4.** pi never clones, fetches or updates a yolo-managed git extension.
- **Limit.** A hostile agent in one jail can still write into the shared store: read-only trees stop
  accidents, not an attacker. The npm store has the same exposure today.

**Done looks like:**
- A second workspace launching pi with the same git extensions clones nothing. `.pi-git-store/trees/`
  has one entry per extension, and `~/.pi/agent/yolo-git/…` links resolve into it.
- Workspace A pins `@v1` and workspace B pins `@v2` of one extension; each runs its own version, and
  `trees/` holds both.
- An upstream moves while a pi session runs in workspace A. Workspace B's next launch builds a new
  tree and repoints B's pointer; A's session's files are byte-identical before and after.
- Two jails launched together on a new extension: one builds, the other announces a wait and then
  uses the same tree. `trees/` has exactly one entry for it.
- A broken dependency build stops the launch with the extension's name and the build log's path,
  and pi does not start.
- `~/.pi/agent/settings.json` in a jail contains no `git:` entry for a user-scope extension, and
  `pi update --extensions` changes nothing under `.pi-git-store`.

## 5. Alternatives

| Alternative | Verdict |
| :--- | :--- |
| **One shared checkout per repository** (`.pi-shared-git`, what `c402dd43` built) | **Withdrawn** by [OQ-2](#OQ-2)–[OQ-4](#OQ-4) ([§2.1](#21-what-the-first-build-did-and-why-it-is-withdrawn)) |
| **Per-workspace clones via `git clone --reference` a shared mirror** | **Rejected.** pi hard-codes its `git clone`; there is no flag to pass. It also shares no `node_modules` |
| **A `git` shim adding `--depth 1`** | **Rejected.** It breaks commit pins and saves the download, not the dependency build |
| **Trees as git worktrees of the mirror, loaded through pi's own git path** | **Rejected.** pi's updater would `git fetch` and `reset --hard` inside a shared worktree: the same leakage |
| **A read-only tree linked at pi's own `~/.pi/agent/git/<host>/<path>`** | **Rejected.** `pi update --extensions` still runs `updateGit` on every git entry, fails on the read-only tree, and makes the pre-launch refresh fail every hour |
| **Core rewriting `git:` entries itself** | **Rejected.** Core would parse a vendor's grammar; the pack does it in `yolo.finalize` ([§3.2](#32-pointing-pi-at-a-tree)) |
| **Immutable trees, pointers, local packages** | **Chosen** |

## 6. Open question

1. ✅ <a id="OQ-5"></a>**[OQ-5](#OQ-5): does the npm store move to the same shape now?**
   [§3.11](#311-the-npm-store): `.pi-shared-npm` breaks [OQ-3](#OQ-3) and [OQ-4](#OQ-4) in the same
   way the git store did, and it is live today. The earlier ruling that shared it
   ([`pi-extension-lifecycle.md` OQ-1](pi-extension-lifecycle.md#OQ-1), *"one version instead of N that
   drift"*) predates the no-winner ruling, and the two now pull apart for any pinned version.
   - **(a)** Extend this design to `npm:` entries now: one mechanism, and pi's updater touches
     nothing in a jail. The largest build, and pi's pre-launch refresh then has nothing to do.
   - **(b)** Ship git first and give npm its own follow-up; npm stays leaky until then.
   - **(c)** Unshare the npm store for now (per-workspace prefixes). No leakage, at the cost of a
     full npm install per workspace until (a).

   _Leaning:_ **(a)**, sequenced git first then npm inside one build. The rulings apply to npm
   exactly as they do to git, and a second mechanism for the same property is the drift this repo
   keeps paying for.


   **Answer:**
   > **(a)**, ruled in review 2026-09-26: extend the immutable-tree design to npm entries now,
   > sequenced git first then npm in one build. The rulings apply to npm exactly as to git, and
   > two mechanisms for one property is drift. Unbuilt.

## 7. Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="OQ-1"></a>[**OQ-1**](#OQ-1) | The refresh lock's location: **an implementation decision**, not the maintainer's (*"an implementation decision I don't need to comment on"*). This design's locks are [§3.5](#35-locks-waits-and-bounds)'s | 2026-09-25 | [§3.5](#35-locks-waits-and-bounds) | — |
| <a id="OQ-2"></a>[**OQ-2**](#OQ-2) | **A launch's result never depends on other launches.** It waits for the update it needs and gets what its config calls for; it never boots on another launch's leftovers (*"what you get in a launch should not depend on the state of other launches"*). Overturns the non-blocking leaning and the first draft's P4 | 2026-09-25 | [§1](#1-principles) P2, [§3.5](#35-locks-waits-and-bounds), [§3.6](#36-failure-paths) | — |
| <a id="OQ-3"></a>[**OQ-3**](#OQ-3) | **No winner.** Jails pick their own versions (*"You can't have one jail's configuration impact another"*) | 2026-09-25 | [§1](#1-principles) P3, [§3.3](#33-resolving-at-launch) | — |
| <a id="OQ-4"></a>[**OQ-4**](#OQ-4) | **No leakage of effects between jails**; sharing and efficiency yes (*"something has to change about your design"*) | 2026-09-25 | [§1](#1-principles) P1, [§3.1](#31-the-store) | — |
| PG-D1 | *Implementation decision.* Trees keyed by commit plus recipe, built in `tmp/` and renamed, read-only after completion | 2026-09-25 | [§3.4](#34-building-a-tree) | — |
| PG-D2 | *Implementation decision.* pi sees each tree as a **local package** through a stable per-workspace pointer, rewritten by the pi pack's `yolo.finalize` post-fold hook, user scope only, never at the host notch | 2026-09-25 | [§3.2](#32-pointing-pi-at-a-tree) | — |
| PG-D3 | *Implementation decision.* Resolution runs in the pi launcher before every exec; the fetch is throttled per mirror (one hour for branches, never for resolving tags or commits), the resolution is not | 2026-09-25 | [§3.3](#33-resolving-at-launch) | — |
| PG-D4 | *Implementation decision.* Fetch bound 60 s, build bound 600 s, heartbeat locks with 600 s staleness; a waiter retries a failed step once itself | 2026-09-25 | [§3.5](#35-locks-waits-and-bounds) | — |
| PG-D5 | *Implementation decision, from [OQ-2](#OQ-2).* A launch that cannot get its trees stops before exec, naming the extension and the log; offline with a resolvable mirror proceeds with a warning | 2026-09-25 | [§3.6](#36-failure-paths) | — |
| PG-D6 | *Implementation decision.* Garbage collection by last use, 14 days, only through `yolo prune --apply` | 2026-09-25 | [§3.8](#38-garbage-collection) | — |
| PG-D7 | *Implementation decision.* Revert `c402dd43`'s shared hook; remove the dangling `~/.pi/agent/git` link; keep `due_on_change`; no seeding from the retired store | 2026-09-25 | [§3.10](#310-migration-from-what-c402dd43-shipped) | — |
| PG-D8 | *Implementation decision.* The dangling link is removed by a new generic hook, `unshare_directory` (`from`, `at`), which pi declares in place of its retired shared pair: it replaces exactly the link `linkIntoSharedDir` wrote (matched by its relative target, never followed) with an empty directory, and touches nothing else. A hook rather than a boot rule keyed on "no current hook claims it", because only the pack knows which link it once made | 2026-09-25 | [§3.10](#310-migration-from-what-c402dd43-shipped) | yes, 2026-09-25 |
| OQ-5 | **The npm store gets the immutable-tree design too**, built after git in the same build; two mechanisms for one property is drift | 2026-09-26 | [OQ-5](#OQ-5) | — |
