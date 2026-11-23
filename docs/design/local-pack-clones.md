---
title: "Every launch checks the latest, and a pack's own clone can be the copy that runs"
date: 2026-11-23
status: in-review
tags: [packs, git, refresh, patched-forks, local-clones]
summary: "Drop the hourly refresh throttle for branch-following git packs and the patched-fork upstream check: every fresh launch probes each remote with one git ls-remote, in parallel under a short deadline, and fetches only what moved. An optional `local` key on a git pack entry names a clone whose working tree runs when it is at least as new as origin."
stage: DESIGN
next: "The Mac witnesses of section 9, which need the owner's Mac; every code slice is built"
---

# Every launch checks the latest, and a pack's own clone can be the copy that runs

**Status:** 2026-11-23. Slices 1-7 are built ([§12](#12-slices-smallest-first)). Evidence read at `d2a831cd8` ([Appendix A](#appendix-a-evidence)). The macOS rows are
UNMEASURED and owed to the owner's Mac ([§9](#9-backends)).

> **In short.** A launch that already spends 10 to 20 seconds starting can afford one git round trip per remote, so
> the hourly throttle on pack and patched-fork checks is dropped: every fresh launch asks each remote whether the
> branch moved and fetches only if it did. The same check is what lets a pack's own clone, working tree
> included, stand in for the remote's copy.

**Why it matters.** A pack the owner develops reaches a jail up to an hour late, or only after a manual copy.

**The shape.** One **probe** per remote, run in parallel under one short deadline ([§4](#4-the-check)); a launch-time
**copy rule** that picks the clone's working tree or origin's tip ([§6](#6-the-local-key-and-the-copy-rule)).

**Cost.** Principle P4 of [patched forks](../reference/patched-forks.md#1-principles) ("a steady-state launch runs
no git") and the ruling behind it ([`OQ-PF1`](../reference/pack-system.md#oq-pf1)'s "at most hourly") are retired. An
offline launch pays the deadline instead of one fetch timeout an hour.

**Start at [§4](#4-the-check)** for the check, [§6](#6-the-local-key-and-the-copy-rule) for the clone.

**Needs your ruling:** nothing; every choice is in the [ledger](#11-decision-ledger).

**Reads with:** [`patched-forks.md`](../reference/patched-forks.md) (the local-checkout and local-clone-map rules this
reuses, and the throttle it retires), [`pack-system.md`](../reference/pack-system.md) (the git pack store and ref rule),
[`happy-path-principle.md`](../reference/happy-path-principle.md) (every stop names the next step).

---

## 1. Goal and non-goals

**Goal.** Restarting a jail runs the latest of every pack and patched fork the user follows, with no copy step and no
delay measured in hours. A pack the owner develops can run from the owner's own clone, uncommitted edits included, on a
machine that has one, and from the remote on a machine that does not.

**Non-goals.**

- **No offline guarantee is lost, and none is added.** A failed or timed-out check runs the copy already held and never
  refuses a launch ([§4.3](#43-deadline-failure-and-what-runs)).
- **Pins do not float.** A full commit, a tag, and a pack with no `ref` (pinned to a commit in the lockfile) never move
  except by `yolo pack update`; the ref rule is unchanged ([`OQ-PF1`](../reference/pack-system.md#oq-pf1)).
- **Not named "development mode"** and not a second pack kind: `local` is one optional key on an entry that is
  otherwise a normal git pack.
- **Out of scope, by the owner's ruling or because another document owns them:** the in-jail agent launchers'
  update interval and `prelaunchrefresh.go` (another design); and the background retry delays, which are backoff after a failure
  ([§10](#10-what-this-does-not-change)).

## 2. Vocabulary

- **Branch-following pack** *(a name for an existing case)*: a git pack whose `ref` is a branch name, so its ref rule
  fetches it when stale ([`OQ-PF1`](../reference/pack-system.md#oq-pf1)). Not a tag, a full commit, or an omitted `ref`.
- **Mirror** — yolo's own bare clone of a remote, one per repository, in the pack store. It is never the user's clone.
- **Probe** *(coined here)* — one `git ls-remote` of exactly the refs a check follows, compared with the same refs in the
  mirror. Moved means a name differs or is new. Not a fetch: it downloads no objects and writes nothing.
- **Held copy** — the commit a mirror already has for the ref (or, for a `local` pack, the clone's working tree), which
  runs when a check cannot finish.
- **Clone** — a git repository the user keeps on this machine, named by `local` or by `local_clones`. Never written
  by yolo ([PF-D102](../reference/patched-forks.md#PF-D102)).
- **Working-tree copy** *(coined here)* — the bytes of a clone's checked-out files as they are on disk now: committed,
  edited and untracked-but-not-ignored, with no `.git`.

## 3. Today, in one paragraph

A launch refreshes each branch pack through `refreshGrouped` ([`refresh.go:312`](../../internal/packsrc/refresh.go#L312)),
one mirror at a time, and `fetchStep` fetches only when the ref's success stamp is over an hour old
([`refresh.go:537`](../../internal/packsrc/refresh.go#L537)). A patched fork's upstream check is throttled by its own
stamp, `CheckDue` ([`checkrecord.go:540`](../../internal/packsrc/checkrecord.go#L540)), and "an attempt counts" so an
offline machine retries hourly ([§4.2](../reference/patched-forks.md#42-the-check-at-every-fresh-launch)). A patch branch in a local
checkout already has the opposite rule: it refreshes at every fresh launch and picks the newest copy
([`patchbranchcopy.go:179`](../../internal/packsrc/patchbranchcopy.go#L179)); the clone's committed branch is compared, never
its working tree.

## 4. The check

### 4.1 What runs at every fresh launch

The trigger is a **fresh launch**: a launch that boots a new jail, `yolo host`, `yolo host apply` and an explicit
`yolo pack update`. An attach never checks and never re-stages; a `--dry-run` and anything inside a jail never check
(both unchanged: [`packrefresh.go:46`](../../internal/cli/run/packrefresh.go#L46)).

For every branch-following pack and every patched fork's upstream, in parallel:

1. **Probe** the followed refs with `git ls-remote -- <url> <ref>` (a branch pack: its branch; a patched fork:
   its branch, plus `refs/tags/*` when it follows tags). The git command is the one `remoteDefaultBranch` already runs
   ([`refresh.go:328`](../../internal/packsrc/refresh.go#L328)).
2. **Unmoved:** nothing more. The held copy answers and no fetch, checkout or lockfile write happens.
3. **Moved:** the mirror's existing fetch (`fetchStep` in always mode, which already does fsck on receipt and puts moved
   tags back), then the existing materialize or check.

A tag re-pointed upstream is not "moved" for a patched fork, which never follows a re-pointed tag
([PF-D4](../reference/patched-forks.md#PF-D4)); only a new name or a changed branch tip is.
An explicit act (`yolo pack update`, `install`) skips the probe and fetches, as it does today.

### 4.2 Parallelism and the deadline

- **Grouped by mirror, concurrent across mirrors.** Packs on one repository share one mirror lock and one probe
  (`refreshGrouped` already groups them); different repositories run at the same time. No concurrency cap: a user
  with ten fetched packs has at most ten processes for a few hundred milliseconds.
- **One overall deadline per phase: 5 seconds** (`PackCheckDeadline`, a constant; tests inject the context). It covers the
  lock wait, the probe and the fetch of a moved branch, and is applied through the store's own context, which
  `flockPathContext` already honors ([`refresh.go:850`](../../internal/packsrc/refresh.go#L850)).
- **Two phases, so at most two deadlines.** The pack refresh runs before packs are resolved; the patched forks' upstream
  checks run later, in the fork-build slot, because forks are known only once the packs are loaded
  ([`patchedadvance.go:1322`](../../internal/cli/patchedadvance.go#L1322)). A fork's replay and build keep their own budgets
  ([§5](../reference/patched-forks.md#5-applying-the-series), [§7](../reference/patched-forks.md#7-the-build-and-the-launch)); only the probe and
  the fetch are under this deadline.
- **A pack with no held copy is exempt.** Nothing can stand in for it, so its first fetch keeps `LaunchFetchTimeout`
  (60 seconds), and a failure there is the resolution error it is today.

### 4.3 Deadline, failure, and what runs

A check that times out, cannot reach the remote, or finds the mirror locked past the deadline **runs the held copy
and never refuses the launch**. It says so once per pack, with the next step:

```text
Warning: pack tillr: could not check origin main (ls-remote timed out after 5s) — running the held 4cd0bc6b. Nothing to do until the network is back; `yolo pack update` checks on demand.
```

A killed fetch leaves the mirror valid (git updates refs atomically), and the next launch probes again.

### 4.4 Which lines a launch prints

A pack that moved prints today's `Updated pack` line. An unmoved pack prints nothing. A failed check prints the warning
above. A `local` pack always prints its provenance line ([§7](#7-lines-and-next-steps)), because its bytes may be
uncommitted work, which is a disclosure ([`OQ-RO3`](../reference/report-tiers.md#why-its-this-way)).

## 5. The patched-fork check and its records

The patched fork's upstream check follows the same rule: **every fresh launch probes, and only a moved upstream fetches.**
What changes in the existing mechanism:

| Piece | Before | After |
| :--- | :--- | :--- |
| Throttle | `CheckDue` by interval ([`checkrecord.go:540`](../../internal/packsrc/checkrecord.go#L540)) | No interval. A fresh launch always runs the probe; `Settled` (a tag or commit that never moves) still skips it |
| The stamp (`CheckedAt`) | Starts the interval; "an attempt counts" | **Last attempt only**, written before the probe for display and for a check killed mid-way. It throttles nothing |
| Retry after a failed fetch | `RetryFailedFetch` for an advance with nothing built | Removed: every launch retries ([PF-D88](../reference/patched-forks.md#PF-D88)'s concern is subsumed) |
| A patch branch in a checkout | `refreshPatchBranch` inside the throttle ([`patchcheck.go:332`](../../internal/packsrc/patchcheck.go#L332)) | The ordinary path; `RefreshPatchBranch` loses its meaning |
| Replay and build | Only on a new candidate | Unchanged. An unmoved upstream with unchanged inputs reuses the recorded verdict; the probe is the only per-launch cost |
| `git version` and a recorded conflict | Read only when a check runs ([PF-D41](../reference/patched-forks.md#PF-D41)) | Read every launch; a recorded conflict still holds for the same inputs and candidate |

**What stays.** The record's `Read` inputs (an edit to what the check reads still invalidates the answer), the rule that
inputs are written only with the answer found ([PF-D28](../reference/patched-forks.md#PF-D28)), the record lock, and the
`FreshAfter` rule by which a second launch reuses a check that began after its registration. Two launches at once
serialize on the record lock; the second probes again (one round trip) or reuses a qualifying fresh check.

**What an offline machine now pays.** DNS or route failure returns at once. A black-holed network pays the deadline at
each phase, in place of one fetch timeout an hour. The owner accepted this: *"What's the point of launching a jail
without good internet?"*

## 6. The `local` key and the copy rule

### 6.1 The key

```toml
packs = [
  { git = "https://github.com/owner/tillr", ref = "main", local = "~/src/tillr" },
]
```

`local` says only "my clone of this repository may be at this path". It tags nothing for refresh: every branch pack is
checked at every launch ([§4](#4-the-check)).

| Rule | Behavior |
| :--- | :--- |
| Scope | `packs` is read from the user config only, so `local` is user scope by construction; a workspace cannot name a host path. A `local` is not valid in a `pack_sources` entry or a `{ pack = … }` entry |
| Value | A string, `~` expanded with `paths.Home()` as `local_clones` does, absolute afterwards. Not absolute, not a string, or empty: refused, naming the entry and `yolo config set` |
| Needs | A `{ git = … }` entry with an explicit `ref`. No `ref`, a full commit, or `refs/tags/…`: refused ("`local` follows a branch: write `ref = \"main\"`") |
| A short name that is a tag upstream | Config cannot know without the network, so the launch says the clone is not used for a tag ([§7](#7-lines-and-next-steps)) and the pack stays frozen |
| `bare`, `namespace` | Unchanged; they name the pack, not its source |
| `subdir` | The pack root is `<clone>/<subdir>`; the working-tree file set is limited to it and the existing no-symlink-in-path rule applies |
| `skills` | Refused in this version: a skills import binds to a recorded commit pin ([`packresolve.go:197`](../../internal/config/packresolve.go#L197)), and a working tree has none. The refusal names dropping `skills` or using a `file://` source |

A `local` whose directory is missing is **not an error**: one config shared by two machines is the use case, so the
absent clone falls to origin and says so. This differs from `local_clones`, where a missing entry is fatal
([PF-D100](../reference/patched-forks.md#PF-D100)): that key exists only to register clones, so a missing one is always a
mistake, and `local` is a conditional. A *malformed* value is fatal at the same verbs as `local_clones`.

### 6.2 Is this clone usable for this pack?

Reuse the patch-branch gates ([PF-D101](../reference/patched-forks.md#PF-D101),
[PF-D104](../reference/patched-forks.md#PF-D104)) through `chooseClone`, refactored to take a clone list so the `local`
path supplies a one-element list. The clone answers only when all hold; otherwise **origin's copy runs and the line names
the failed condition and the fix**.

1. The path is a git clone (`git rev-parse --show-toplevel`).
2. One of its remotes names the pack's repository by identity (`git@github.com:o/r.git`, `ssh://`, `https://` are one
   repository). The address that remote fetches from (first `url`, after `insteadOf`) must too.
3. The clone holds the branch as that remote's tracking ref, `refs/remotes/<remote>/<ref>`.
4. **HEAD is on `refs/heads/<ref>`.** A working tree on another branch is not the pack's branch: the line says
   `clone is on branch X, not main — git -C <clone> switch main`.

### 6.3 The rule

Run `pickCopy` against the clone ([`patchbranchcopy.go:179`](../../internal/packsrc/patchbranchcopy.go#L179)) for the
relation between the clone's branch and origin's tip, with one addition, whether the working tree is **dirty** (any
tracked change or untracked-not-ignored file, from `git status` run with `GIT_OPTIONAL_LOCKS=0` and
`core.fsmonitor=false`, so it writes nothing and runs no hook the repository configures).

| Clone's branch vs origin's tip | Working tree | Runs | Line says |
| :--- | :--- | :--- | :--- |
| Equal | clean or dirty | working tree | `equal to origin` |
| Ahead | clean or dirty | working tree | `N unpushed` |
| Behind | clean | **origin's tip** (yolo's mirror) | `clone is N behind origin and clean` and `git -C <clone> pull` |
| Behind | dirty | working tree, warned | `behind origin by N and has uncommitted changes — git -C <clone> pull` |
| Diverged | any | working tree, warned | both counts and PF-D99's `push --force-with-lease` (if you rebased) |
| Fetch failed or timed out | any | working tree | `not verified`, the age of the last fetch, and `yolo pack update` |
| Origin deleted the branch | any | working tree, warned | `not on origin`, PF-D99's push that restores it |
| Clone absent | n/a | origin's tip | `no local clone at <path>` |

A clean clone behind origin runs origin because nothing of the owner's is lost and origin is newer; a dirty one runs the
working tree because the edits are the work in progress the owner wants to see.

**Origin's tip is always fetched from the address the pack names**, never from the clone's remote spelling, so the
origin copy is the bytes a machine with no clone gets, from one shared mirror. The clone's remote only has to name the
same repository.

### 6.4 What is staged

- **The file set** is what git would add: tracked files plus untracked files that are not ignored, from `git ls-files`,
  with `.git` never included. A tracked file deleted on disk is absent. Ignored files (`.env`, `node_modules`, build
  output) never reach a jail; a new file the owner just created does.
- **Submodules** (gitlinks) are not staged, which is what the origin copy does too (the mirror fetches
  `--recurse-submodules=no`, [`store.go:646`](../../internal/packsrc/store.go#L646)).
- **Content rules are the fetched pack's, not a `file://` pack's.** A `file://` pack follows symlinks
  ([`packresolve.go:378`](../../internal/config/packresolve.go#L378)); a `local` clone does not, so a link that leaves the
  pack refuses it, naming the file. Reason: the same pack must stage the same way whether the clone or origin supplies
  it, or a pack that works on one machine fails on the other.
- **Trust is the fetched pack's.** The pack is not marked `Local`, so `MayRunHostHalf` stays false
  ([`packload.go:185`](../../internal/packload/packload.go#L185)). A clone existing never grants a pack host execution;
  an owner who wants that uses a `file://` source.
- **Digest and launchers.** The staged tree is a copy made for this launch, and the pack-tree digest is over that copy
  with `.git` skipped ([`packtree.go:237`](../../internal/packload/packtree.go#L237)), so an unchanged tree has the same digest
  and a program built from it is not rebuilt, and an edit changes the digest and rebuilds. No new mechanism.
- **The tree is read once, at staging**, after the check. A concurrent edit between the two is not seen until the next
  launch; the line is printed after staging and carries the file count and, when any, the untracked count.

### 6.5 Where the decision lives

- **The check writes a per-pack launch record** beside the check records (`<PacksDir>/checks/`), under its own lock,
  one writer: the launch-time check (a fresh launch, `yolo host`, `host apply`, `pack update`). It holds the chosen copy,
  its commit, the relation, the freshness and the line's inputs.
- **`config.ResolvePack` reads the record** to learn the copy, so every notch and verb agrees without a map, as
  [PF-D102](../reference/patched-forks.md#PF-D102) does. `PackEntry` carries the expanded path as a field that is never
  serialized ([`packs.go:66`](../../internal/config/packs.go#L66)'s wire form excludes host paths).
- **The lockfile records a commit only when origin's copy is delivered.** A working-tree copy leaves the pin where it
  was; `pack status` reads the record instead.
- **A `local` path is a path into a clone**, so it registers that clone in the local clone map for patch branches, as any
  pack path does ([PF-D100](../reference/patched-forks.md#PF-D100)). `local_clones` is unchanged.
- A working-tree copy that cannot be staged (the clone vanished, a refused link) falls to origin's held copy with a
  warning; with none held it refuses, naming the clone and the fix.

## 7. Lines and next steps

Each ends with a step that works. One per `local` pack, every fresh launch (the first two are the owner's examples):

```text
tillr  local ~/src/tillr (working tree, equal to origin; 41 files)
tillr  origin main 4cd0bc6b (no local clone at ~/src/tillr — git clone https://github.com/owner/tillr ~/src/tillr)
tillr  local ~/src/tillr (working tree, 1 unpushed commit, 2 files uncommitted; 41 files)
tillr  origin main 4cd0bc6b (clone is 3 behind origin and clean — git -C ~/src/tillr pull)
Warning: tillr  local ~/src/tillr (working tree; 41 files) is behind origin by 3 and has uncommitted changes — git -C ~/src/tillr pull
Warning: tillr  local ~/src/tillr (working tree, not verified: fetch timed out, last fetched 3 hours ago; 41 files) — yolo pack update when online
Warning: tillr  origin main 4cd0bc6b (clone ~/src/tillr not used: its remote origin names github.com/o/other — fix the remote or the pack's git url)
```

**The file-count rule** (the [§6.4](#64-what-is-staged) reading): every working-tree line ends its parenthetical with
`; N files`, the pack's staged file set, whatever else it says; an origin line carries no count, because the clone's
files are not what runs. Uncommitted files are a separate item, `N files uncommitted`, with `(M untracked)` after it
when any are new. A path is written with `~` for the home directory, in prose and in the commands the line names.

| Cause | Next step named |
| :--- | :--- |
| `local` not absolute, a tag or commit `ref`, `skills` with `local` | The `yolo config set` that fixes the entry |
| Directory exists, not a clone | `git clone <url> <path>`, or remove `local` |
| No remote names the repository | Fix the remote, or the `git` url |
| No tracking ref for the branch | `git -C <clone> fetch <remote>` (PF-D104's wording) |
| HEAD on another branch | `git -C <clone> switch <ref>` |
| Ref is a tag upstream | Name the branch the pack follows |

## 8. The verbs

- **`yolo pack update`**: probes nothing and fetches origin for every pack (as now); for a `local` pack prints the same
  line. It never pulls into, or writes to, the clone.
- **`yolo pack status`**: reads the launch record and the check record, no git. For each branch pack: the commit, the
  copy, the relation and how long ago the last launch checked; stale across launches, and says so.
- **`yolo check` and `yolo config check`**: read-only and network-free. They report every [§6.2](#62-is-this-clone-usable-for-this-pack)
  condition computable from the clone alone (path, remote identity, tracking ref, HEAD) as a finding with its next step;
  they run no probe and write no record.
- **Attach**: unchanged. It reads the running jail's tree and never re-stages; edits after boot show on the next launch.
- **Dry run, in-jail, nested jail**: no check ([`packrefresh.go:46`](../../internal/cli/run/packrefresh.go#L46)).

## 9. Backends

All of it is host-side, before `stagePacks`, so podman, macos-user and Apple Container behave the same, and a nested
jail verifies it fully. Owed to the owner's Mac, through a design doc as the repository's rule requires: (a) a `~`
path and a `/var` to `/private/var` symlink compare equal in the identity match on darwin; (b) in the `seatbelt-yolo-account`
setup the launching account can read the owner's clone path. Neither is measured.

## 10. What this does not change

- **Workspace `skill_imports` follows the new rule** ([`resolver.go:257`](../../internal/skillimports/resolver.go#L257)):
  `refreshDue` is true for every branch import at every fresh launch, under the same 5 s deadline. Its transport is the
  git broker, so the check is the broker's own moved-or-not read when it has one and otherwise its fetch; either way a
  failure or timeout serves the usable record with its existing warning ("the last usable commit is staged"), and a launch
  never queues behind another's refresh (`TryWithLock`, unchanged). A pinned `rev` still never refreshes.
- **The background retry delays keep `BranchRefreshInterval`** ([`backgroundadvance.go:557`](../../internal/cli/backgroundadvance.go#L557),
  [`preparationforks.go:252`](../../internal/cli/preparationforks.go#L252)): they are backoff after a failure, not freshness throttles.
- **A pack's skill payload** ([`skillpayload.go:226`](../../internal/packsrc/skillpayload.go#L226)) follows the new rule too, because it shares `fetchStep`. It probes against its recorded commit, not the mirror's branch, so a fetch whose blob completion failed still retries. One consequence: a complete but invalid candidate is refetched at every fresh launch while the remote tip differs from the record, and the record keeps serving the last good payload meanwhile.
- **`local_clones` and the clone map** are unchanged except that a `local` path registers a clone.
- **Reapers and stamps.** The per-ref success stamp remains as the display of "last fetched"; no decision reads it.

## 11. Decision ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| LPC-D1 | Owner: drop the hourly throttle for packs. *"I'm thinking we skip the hour. We drop the hourly throttle entirely. There doesn't seem to be a good reason for it."* *"We want the updates when the updates come out as long as the check is not expensive."* Principle P4 and the "at most hourly" ruling are retired | 2026-11-23 | [§4](#4-the-check) | Built `f763457b3`: the probe replaces the hourly stamp |
| LPC-D2 | A fresh launch probes (`ls-remote`) every branch pack and fetches only a moved branch; an explicit act fetches without probing | 2026-11-23 | [§4.1](#41-what-runs-at-every-fresh-launch) | Probe built `f763457b3`; the patched-fork check probes tags too, so a new release tag on an unmoved branch fetches |
| LPC-D3 | Concurrent across mirrors, one 5 s deadline per phase covering lock wait, probe and fetch; a pack with no held copy keeps the 60 s first fetch | 2026-11-23 | [§4.2](#42-parallelism-and-the-deadline) | Built `0bd3cec73` |
| LPC-D4 | Timeout or failure runs the held copy, never refuses, one warning per pack naming the next step | 2026-11-23 | [§4.3](#43-deadline-failure-and-what-runs) | Built `0bd3cec73` |
| LPC-D5 | An unmoved pack prints nothing, a moved one the `Updated pack` line, a `local` pack always its provenance line | 2026-11-23 | [§4.4](#44-which-lines-a-launch-prints) | Built `ff591e8fd` (the `local` line), `f131c6083` (its verbs) |
| LPC-D6 | The patched-fork upstream check is probed at every fresh launch; the stamp becomes last-attempt only; `RetryFailedFetch` and the in-throttle patch-branch refresh fold into the ordinary path; replay and build only on a new candidate; PF-D5's throttle, PF-D41 and PF-D88's retry are retired | 2026-11-23 | [§5](#5-the-patched-fork-check-and-its-records) | Built `de4f47705` (patched fork), `752927669` (npm source) |
| LPC-D7 | `local` is a key of a `{ git = … }` entry with an explicit branch `ref`; user scope by construction; absolute after `~`; refused with `pack =`, tag or commit `ref`, and `skills` | 2026-11-23 | [§6.1](#61-the-key) | Built `b88473f02` |
| LPC-D8 | A missing or unusable clone falls to origin and never refuses; only a malformed value is fatal. This differs from `local_clones` because `local` is conditional by design | 2026-11-23 | [§6.1](#61-the-key) | Built `ff591e8fd`; the checks `f131c6083` |
| LPC-D9 | The clone must match by repository identity (PF-D101, PF-D104) through `chooseClone` over a one-element list, and HEAD must be on the pack's branch | 2026-11-23 | [§6.2](#62-is-this-clone-usable-for-this-pack) | Built `ff591e8fd` |
| LPC-D10 | The copy rule is `pickCopy`'s plus a dirty exception: a dirty clone behind origin runs its working tree and warns | 2026-11-23 | [§6.3](#63-the-rule) | Built `ff591e8fd` |
| LPC-D11 | Origin's tip is always fetched from the address the pack names | 2026-11-23 | [§6.3](#63-the-rule) | Built `ff591e8fd` |
| LPC-D12 | The working tree is `git ls-files` tracked plus untracked-not-ignored, no `.git`, no submodules, read once at staging | 2026-11-23 | [§6.4](#64-what-is-staged) | Built `371026c48` |
| LPC-D13 | Fetched-pack content and trust rules for every copy: links that leave the pack refuse, the pack is not `Local`, no host half | 2026-11-23 | [§6.4](#64-what-is-staged) | Built `371026c48` |
| LPC-D14 | A per-pack launch record is the one place the copy decision lives; `ResolvePack` reads it; the lockfile pins only origin's copy | 2026-11-23 | [§6.5](#65-where-the-decision-lives) | Built `ff591e8fd` (the record), `371026c48` (the pin) |
| LPC-D15 | A `local` path registers its clone in the local clone map | 2026-11-23 | [§6.5](#65-where-the-decision-lives) | Built `f131c6083` |
| LPC-D16 | Owner's "drop the hourly throttle entirely" reaches scoped `skill_imports` and a pack's skill payload, both checked at every fresh launch under the same probe and deadline; the background retry delays are failure backoff and stay; the in-jail launchers are another design's | 2026-11-23 | [§10](#10-what-this-does-not-change) | Built `c985aabc5` (`skill_imports`), `f763457b3` (the skill payload) |

## 12. Slices, smallest first

**Built:** slices 1 `f763457b3`, 2 `0bd3cec73`, 3 `de4f47705` with `752927669`, 3b `c985aabc5`, 4 `b88473f02`,
5 `ff591e8fd`, 6 `371026c48`, 7 `f131c6083`. The Mac witnesses of [§9](#9-backends) remain, and they need the owner's Mac.

Tests are event-driven: fake remotes are local bare repositories, git is a recording shim, deadlines are injected
contexts that a test cancels, and nothing sleeps or touches the network. Each row's tests must fail if its call site is
deleted.

| # | Slice | Production call site its tests pin |
| :--- | :--- | :--- |
| 1 | `probe` replaces `stampFresh` in `fetchStep`; unmoved runs no fetch, moved fetches once, the stamp is display only | `run.refreshConfiguredPacks` through `Store.Refresh` |
| 2 | Parallel checker: grouped by mirror, concurrent across mirrors, one injected deadline; a hung probe (shim blocked on a channel) yields the held copy and a warning, and a pack with no held copy still gets its long fetch | `refreshConfiguredPacks`, and `RefreshConfiguredPacks` from `yolo host` and `host apply` |
| 3 | Patched-fork check probes at every fresh launch; unmoved upstream runs no replay; `RetryFailedFetch` removed; offline run produces the held answer | the check slot at `patchedadvance.go:1322` |
| 3b | Workspace `skill_imports`: `refreshDue` true at every fresh launch for a branch import, a failure or timeout serves the usable record, a pinned `rev` never refreshes, no queueing behind another launch's refresh | the resolver call in `internal/skillimports/resolver.go` (line 104 onward) |
| 4 | `local` grammar and refusals; `PackEntry` field; `config check` findings | `ParsePackReference` and `checkReadablePacksFromEmbeddedNames` |
| 5 | Pure `decideLocalCopy` over relation, dirty, on-branch, verified; real temp clones for each table row, including a fetch failed by deleting the fake remote | the launch check, via the record `ResolvePack` reads |
| 6 | Working-tree staging: file set, gitlinks, ignored, untracked, deleted, escaping link refused, digest stable when unchanged | `stagePackEntry` ([`packs.go:952`](../../internal/cli/run/packs.go#L952)) |
| 7 | Launch lines, `pack status`, `pack update`, `yolo check` findings with next steps | the printing in `refreshPacks` and the verbs' renderers |

## Appendix A. Evidence

Read at `d2a831cd8`, 2026-11-23, in the source; none run.

- Throttle sites: `fetchStep`'s stamp test ([`refresh.go:537`](../../internal/packsrc/refresh.go#L537)), `CheckDue`
  ([`checkrecord.go:540`](../../internal/packsrc/checkrecord.go#L540)), `RetryFailedFetch`
  ([`patchcheck.go:124`](../../internal/packsrc/patchcheck.go#L124)). Other `BranchRefreshInterval` users: skill payload,
  `skill_imports` (brokered fetch, `fetchAndRecord`), fork pin ([`forkpin.go:256`](../../internal/packsrc/forkpin.go#L256), a commit pin that never floats), and the two background retry delays (failure backoff, kept).
- Reused machinery: [`chooseClone`](../../internal/packsrc/localclones.go#L178), [`pickCopy`](../../internal/packsrc/patchbranchcopy.go#L179),
  [`aheadBehind`](../../internal/packsrc/patchbranchcopy.go#L272), [`Line`](../../internal/packsrc/patchbranchcopy.go#L319),
  [`LocalClones`](../../internal/config/localclones.go#L63) and its `packEntryPath`.
- The entry's keys are `git`, `ref`, `pack`, `bare`, `namespace`, `subdir`, `skills`
  ([`naming.go:34`](../../internal/packsrc/naming.go#L34)); the `//subdir` spelling is only the internal address.
- A `file://` pack follows symlinks and is `Local`; a fetched one does neither ([`packresolve.go:378`](../../internal/config/packresolve.go#L378)).
- Not established: whether `newScratchOf` accepts a non-bare git directory for the clone's objects (it appends
  `<dir>/objects`, [`replay.go:546`](../../internal/packsrc/replay.go#L546)); whether `packstage.Stage` can take a file list or needs a
  scratch snapshot; the exact patterns the patched fork's probe needs for each `follow` mode; whether the git broker offers `skill_imports` a moved-or-not read cheaper than its fetch (slice 3b: it does not;
  `FetchImport` lists refs internally at [`importfetch.go:190`](../../internal/gitbroker/importfetch.go#L190) via the unexported
  `listImportRefs` ([`:303`](../../internal/gitbroker/importfetch.go#L303)) but exports no ref-only read, so the launch check is the
  fetch, under `PackCheckDeadline`). The planner checks each
  before relying on it.
