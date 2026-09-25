---
title: "Sharing Pi git extensions across workspaces: from cold clones to machine-scoped checkouts"
date: 2026-09-25
status: draft
tags: [pi, extensions, git, caching, machine-tier, storage]
summary: "Architecture for machine-scoped Pi git extension storage, eliminating redundant full clones and multi-workspace cold startup delays via shared directory hooks and git fetch reconciliation."
vantage:
  status-chip: true
---

# Sharing Pi git extensions across workspaces: from cold clones to machine-scoped checkouts

**Status:** DESIGN, 2026-09-25. Nothing built. Evidence verified at `74d830f2`.

> **In short.** Pi's git extensions are currently trapped in the workspace-scoped state
> directory, forcing every new jail to perform redundant, uncached network clones and cold
> `npm install` cycles on startup. Lifting `~/.pi/agent/git` into a machine-scoped
> `.pi-shared-git` store paired with a `shared_directory` hook mirrors the `.pi-shared-npm`
> architecture, turning repetitive 30–60 second startup clones into sub-second `git fetch`
> delta checks shared across all workspaces.

**Why it matters.** A user with multiple git extensions incurs 30–60+ seconds of blocking
network clones and dependency builds every time a new workspace jail launches. These
operations burn disk space, trigger launcher timeouts (`UPDATE_TIMEOUT=60`), and bypass
cross-jail synchronization.

**The shape.** A machine-scoped state contribution (`.pi-shared-git`) in `packs/pi/pack.json`
paired with a `shared_directory` hook mapping `~/.pi/agent/git`, relying on Pi's native
`updateGit` fetch reconciliation and YOLO's pre-launch mutual exclusion lock.

**Cost.** Workspaces sharing the machine-scoped git store share one active checkout per git
repository in user scope; workspaces requiring isolated branch pins must use project-scoped
configuration (`.pi/settings.json`), which remains workspace-private.

**Start at [§3](#3-the-proposed-architecture)** — the storage split and update flow. The rest falls out of it.

**Needs your ruling:** [OQ-1](#OQ-1), [OQ-2](#OQ-2), [OQ-3](#OQ-3).

**Reads with:** [`pi-extension-lifecycle.md`](pi-extension-lifecycle.md) (the npm store and prelaunch refresh foundation),
[`pi-git-extension-caching-plan.md`](pi-git-extension-caching-plan.md) (the companion implementation sketch — incomplete while questions are open).

---

## 1. Principles and Verdict

* **P1. Single source of truth for global tools.** Extensions configured in user scope
  (`~/.pi/agent/settings.json`) are global tools like node runtimes or CLI utilities. They
  belong in machine-scoped storage shared by all workspaces on the host, not copied into N
  isolated directories.
* **P2. Never clone what already exists.** A repository already present on the machine must
  never be downloaded from scratch over the network. Reconciling an existing checkout with
  upstream requires only a `git fetch` delta, not a fresh clone.
* **P3. Zero vendor code modification.** YOLO does not fork or patch `@earendil-works/pi-coding-agent`.
  All optimizations must work through Pi's public directory contracts and existing CLI flags
  (`pi update --extensions`).
* **P4. Launch outranks updating.** No background update failure, network timeout, or lock
  contention may prevent the interactive coding agent session from starting.

**Verdict:** Add `.pi-shared-git` as a machine-scoped state directory (`scope: "machine"`)
in `packs/pi/pack.json` with a `shared_directory` hook pointing from `.pi/agent/git`. This
reuses YOLO's proven directory-sharing and migration subsystem (`internal/entrypoint/sharedlink.go`),
instantly eliminating duplicate git clones across workspaces without writing custom git wrappers.

---

## 2. Current State and Problem Analysis

### 2.1 How Pi manages git extensions today

Pi supports installing extensions from git repositories via `git:<url>[@<ref>]`
(e.g., `git:github.com/mschulkind/pi-archimedes`).

In Pi's package manager (`dist/core/package-manager.js`):
1. **Target Directory**: For user-scoped packages, `getGitInstallPath()` resolves target paths
   under `this.agentDir/git` (`~/.pi/agent/git/<host>/<user>/<repo>`).
2. **Installation (`installGit`)**: If `!existsSync(targetDir)`, Pi executes:
   ```bash
   git clone <source.repo> <targetDir>
   ```
   If a ref was specified, it checks out the ref (`git checkout <ref>`). If `package.json`
   exists in the repository, it executes `npm install` (`getGitDependencyInstallArgs()`) inside
   `targetDir`.
3. **Update (`updateGit`)**: If `existsSync(targetDir)` is true:
   - For pinned refs: Pi runs `git fetch origin <ref>` and checks if `HEAD` equals `FETCH_HEAD`.
   - For unpinned refs: Pi resolves `@{upstream}` or `origin/HEAD` and runs:
     ```bash
     git fetch --prune --no-tags origin +refs/heads/<branch>:refs/remotes/origin/<branch>
     ```
   - If local `HEAD` matches remote `HEAD`, it verifies `node_modules` exists and exits immediately.
   - If local `HEAD` differs from remote `HEAD`, Pi writes a marker file
     (`.<name>.pi-update-incomplete`), executes `git reset --hard <commit>`, cleans untracked files
     via `git clean -fdx` (wiping `node_modules`), and executes a full `npm install`.

### 2.2 The root cause of startup cloning

In `packs/pi/pack.json`, Pi's state configuration currently declares:

```json
{
  "at": ".pi",
  "kind": "state",
  "scope": "workspace"
},
{
  "at": ".pi-shared-npm",
  "because": "the extension package store, shared so every workspace on the machine runs one version of an extension instead of N that drift",
  "kind": "state",
  "scope": "machine"
},
{
  "at": ".pi-shared-npm",
  "from": ".pi/agent/npm",
  "hook": "shared_directory",
  "kind": "hook"
}
```

Notice the critical asymmetry:
* `~/.pi/agent/npm` is redirected to `.pi-shared-npm` at `scope: "machine"`.
* `~/.pi/agent/git` has **no hook** and inherits `~/.pi`'s `scope: "workspace"`.

This creates four distinct failure modes on jail startup:

| Scenario | What happens | Result |
| :--- | :--- | :--- |
| **New workspace jail** | `~/.pi/agent/git` is empty. | `pi update --extensions` runs `installGit()` for every git extension, triggering N full network clones + N cold `npm install` builds. |
| **Launcher timeout** | 4 git clones + builds exceed `UPDATE_TIMEOUT=60`. | Launcher kills updater; `installGit` error handler removes `targetDir` (`rmSync(targetDir)`). Next boot re-clones from scratch. |
| **Throttled refresh** | Machine stamp `< 3600s` skips pre-launch refresh. | Pi's startup `resolve()` detects missing `targetDir` and runs `installGit()` inside interactive startup with zero locking. |
| **Disk duplication** | Every workspace has its own clone. | N workspaces multiply repository history and `node_modules` on host disk ($N \times \approx 80\text{ MB}$). |

The author of [`pi-extension-lifecycle.md`](pi-extension-lifecycle.md) ([§3.2](pi-extension-lifecycle.md#32-execution-tier-pre-launch-auto-refresh) warning) noted this gap during earlier implementation
but left `~/.pi/agent/git` unshared.

---

## 3. The Proposed Architecture

The design lifts Pi's user-scoped git extension storage into the machine-scoped storage tier
alongside `.pi-shared-npm`.

```
Host Store: ~/.local/share/yolo-jail/home/
├── .pi-shared-npm/               (Machine-scoped npm modules)
│   └── .yolo-update.lock         (Shared mutual exclusion lock)
└── .pi-shared-git/               (Machine-scoped git checkouts)
    └── github.com/
        ├── mschulkind/pi-archimedes/ (.git + node_modules)
        ├── mschulkind/pi-subagents/
        └── Jawfish/pi-background-tasks/

Jail Container: /home/agent/
└── .pi/agent/                    (Workspace-scoped state)
    ├── npm -> ../../.pi-shared-npm
    ├── git -> ../../.pi-shared-git
    ├── sessions/                 (Private to workspace)
    └── settings.json
```

### 3.1 Storage Tier: `.pi-shared-git`

We add a machine-scoped state declaration and symlink hook to `packs/pi/pack.json`:

```json
{
  "at": ".pi-shared-git",
  "because": "machine-wide store for Pi git extensions, avoiding redundant clones and drift across workspaces",
  "kind": "state",
  "scope": "machine"
},
{
  "at": ".pi-shared-git",
  "from": ".pi/agent/git",
  "hook": "shared_directory",
  "kind": "hook"
}
```

At container initialization, YOLO's entrypoint executes `linkThroughShared` (`sharedlink.go`):
1. **Empty store initialization**: If `.pi-shared-git` is empty and an existing workspace contains
   `~/.pi/agent/git`, the existing tree is copied into `.pi-shared-git` via `copyTreeIntoShared()`.
2. **Shared store wins**: Once `.pi-shared-git` is populated, any workspace jail mounting it
   replaces its local `~/.pi/agent/git` directory with a symlink pointing to `/home/agent/.pi-shared-git`.
3. **Backend portability**:
   - **Podman & Apple Container**: The host machine directory (`paths.GlobalHome() + "/.pi-shared-git"`)
     is bind-mounted into the jail at `/home/agent/.pi-shared-git`.
   - **macos-user**: Lives directly in the sandbox user home and is mirrored to sidecars via
     `DeriveDarwinHomeLayout` (`darwinhomelayout.go`).

### 3.2 Execution Tier: Delta Updates via `git fetch`

With `~/.pi/agent/git` mapped to `.pi-shared-git`, the lifecycle behavior in Pi's
`DefaultPackageManager` changes completely:

1. **First install on machine**:
   - `!existsSync(targetDir)` is true only once per machine.
   - Pi runs `git clone` into `.pi-shared-git` and builds dependencies via `npm install`.
2. **Subsequent launches across ANY workspace**:
   - `existsSync(targetDir)` is immediately true.
   - During launcher pre-launch refresh (`pi update --extensions`):
     - Pi calls `updateGit(source, scope)`.
     - Pi detects existing checkout and runs `getLocalGitUpdateTarget()`.
     - Executes `git fetch origin` for the configured branch or ref.
     - Compares commit hashes: if no remote updates exist, Pi exits in **~200 ms**.
     - If updates exist, Pi downloads only the delta packfile, resets `HEAD`, and refreshes
       dependencies.
3. **Subsequent workspace launches under throttled stamp (< 1 hour)**:
   - Launcher skips pre-launch refresh.
   - Pi's interactive `resolve()` sees `existsSync(installedPath) === true`.
   - Pi loads extensions directly from disk in **< 10 ms**. Zero git commands run. Zero network
     requests.

### 3.3 Concurrency Tier: Cross-Jail Mutual Exclusion

Because all workspaces now write to the same `.pi-shared-git` directory, concurrent jail launches
must not race during `git fetch`, `git reset`, or `npm install`.

1. **Reusing the pre-launch refresh lock**:
   The launcher's pre-launch refresh (`prelaunchrefresh.go`) already executes `pi update --extensions`
   under an atomic non-blocking directory lock:
   `REFRESH_LOCK="$HOME/$REFRESH_LOCK_REL"` (currently `.pi-shared-npm/.yolo-update.lock`).
   Because `pi update --extensions` reconciles both npm and git packages in a single command,
   this single lock arbitrates all background package updates across all jails.
2. **Non-blocking launch semantics**:
   If Jail 2 launches while Jail 1 holds the refresh lock:
   - Jail 2 logs an advisory message to `stderr`:
     ```text
     pi: another refresh holds ~/.pi-shared-npm/.yolo-update.lock — running what is installed.
     ```
   - Jail 2 skips the refresh and executes Pi immediately using the existing checkouts in
     `.pi-shared-git`.
3. **Incomplete update protection**:
   When Pi updates a git checkout with new commits, it writes `.<name>.pi-update-incomplete` before
   `git clean -fdx` and deletes it after `npm install` finishes.
   If an update is interrupted (power loss, SIGKILL), Pi automatically detects the marker on the
   next run and repairs dependencies (`repairMissingGitDependencies()`).

---

## 4. Invariants and Failure Modes

### 4.1 Invariants

* **I1. Single active checkout per git URL in user scope.** All workspaces sharing user configuration
  share the single repository checkout in `.pi-shared-git`.
* **I2. Workspace isolation for project packages.** Project-scoped packages (`scope: "project"`,
  configured in `.pi/settings.json`) install strictly to `<workspace>/.pi/git/` and are never shared
  or linked to `.pi-shared-git`.
* **I3. Non-blocking launch priority.** Updating extensions must never block jail startup. A held lock
  or network failure must result in running the installed version.
* **I4. Migration without data loss.** Existing checkouts in legacy workspace homes must be safely
  copied to `.pi-shared-git` on first boot before symlinking.

### 4.2 Failure Mode Matrix

| Failure Mode | Detection | System Behavior | User Impact |
| :--- | :--- | :--- | :--- |
| **Offline startup** | `git fetch` times out / fails network resolution | Pi catches network error; exits update step; launcher logs warning; Pi boots | Pi boots immediately using existing checkouts. No retry for 1 hour. |
| **Contended launch during git update** | Refresh lock held by another jail | Launcher skips pre-launch refresh; execs Pi immediately | Second jail boots in <1s. If target checkout is mid-update, Pi reports load diagnostic and continues. |
| **Corrupted git checkout in shared store** | `.git` index lock or broken object | `git fetch` returns non-zero; Pi update catches error | Other extensions load normally; error reported to stderr. |
| **Interrupted git update (timeout / crash)** | `.<name>.pi-update-incomplete` marker present | On next run, Pi detects marker and runs `cleanAndInstallGitDependencies` | Self-healing on subsequent launch. |
| **Conflicting branch refs in user scope** | Workspaces specify different `@ref` for same repo | Last workspace to run update resets shared working tree to its configured ref | Handled via [OQ-3](#OQ-3); project-scoped packages are the clean boundary. |

---

## 5. Alternatives Considered

### Alternative A: Bare git mirrors with workspace-local checkouts via `--reference`

* **Shape**: Maintain bare repositories in a machine-scoped cache (`~/.cache/git-mirrors/<repo>.git`).
  Each workspace keeps its own checkout in `<workspace>/.pi/agent/git/` cloned with
  `git clone --reference ~/.cache/git-mirrors/...`.
* **Verdict**: **Rejected**.
  1. *Requires vendor patching*: Pi's `package-manager.js` hardcodes `git clone <repo> <dir>`.
     Pi provides no hook or flag to pass `--reference`.
  2. *Duplicate disk & CPU overhead*: Every workspace would still contain a separate `node_modules`
     directory and would still run `npm install` on startup. 4 extensions across 5 workspaces
     would still result in 20 separate `node_modules` installations.
  3. *Cross-workspace drift*: Updating an extension in Workspace A leaves Workspace B stale.

### Alternative B: Git wrapper shim injecting shallow clone flags (`--depth 1`)

* **Shape**: Deploy a `git` shim in the jail that intercepts `git clone` calls from Pi and injects
  `--depth 1 --single-branch`.
* **Verdict**: **Rejected**.
  1. *Fragile argument parsing*: Shimming core utilities like `git` inside the jail risks breaking
     other developer workflows.
  2. *Solves the wrong problem*: Shallow cloning speeds up the network download from 10s to 3s,
     but does not solve the 30s `npm install` cycle, disk duplication, or cross-workspace drift.
  3. *Breaks commit-pinned extensions*: Shallow clones break checkouts pinned to historical commit
     SHAs not at branch heads.

### Alternative C: YOLO-managed package resolution (`internal/packsrc`)

* **Shape**: YOLO resolves git extension versions on the host, records commit SHAs in
  `packs.lock.json`, and stages read-only trees into the jail.
* **Verdict**: **Deferred to future milestone**.
  While this matches [Alternative D](pi-extension-lifecycle.md#alternative-d-resolve-and-pin-through-yolos-existing-pack-source-store)
  in [`pi-extension-lifecycle.md`](pi-extension-lifecycle.md), building a custom git resolver and lockfile system for Pi
  is a Heavy track project. Lifting `~/.pi/agent/git` into machine storage delivers immediate
  95%+ startup latency reduction using existing, proven mechanisms (`shared_directory`).

### Alternative D: Machine-scoped `.pi-shared-git` via `shared_directory` hook

* **Shape**: Declare `.pi-shared-git` as `scope: "machine"` in `packs/pi/pack.json` with a
  `shared_directory` hook pointing from `.pi/agent/git`.
* **Verdict**: **Chosen**. Reuses existing entrypoint infrastructure, requires zero changes to
  Pi vendor code, eliminates duplicate disk usage, and turns cold clones into fast `git fetch`
  updates.

---

## 6. Open Questions

1. 💬 **OQ-1: Lock directory location for combined npm and git refresh.**
   Should the pre-launch refresh lock remain at `.pi-shared-npm/.yolo-update.lock` or move to
   a neutral store path?

   <!-- vantage: oq id=OQ-1 leaning="Keep .pi-shared-npm/.yolo-update.lock — pi update --extensions reconciles both npm and git in one process, so the existing lock path serializes both with zero code changes." -->

   _Leaning:_ Keep `.pi-shared-npm/.yolo-update.lock`. The `pi update --extensions` command updates
   both npm and git packages in a single execution. The launcher already locks on
   `.pi-shared-npm/.yolo-update.lock` before spawning the process. Renaming or splitting the lock
   would require changing `packdecl.Refresh` and `prelaunchrefresh.go` for zero functional gain.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-2: Handling the contended launch window during git dependency rebuilds.**
   When Jail 1 acquires the refresh lock and fetches a new commit, Pi runs `git reset --hard`,
   `git clean -fdx`, and `npm install` in `.pi-shared-git`. If Jail 2 launches during this
   multi-second window, should it wait or proceed?

   <!-- vantage: oq id=OQ-2 leaning="Proceed immediately (non-blocking) — launch priority outranks updates. The update window is brief, and Pi logs an advisory load diagnostic without crashing." -->

   _Leaning:_ Proceed immediately (non-blocking launch). Invariant 4.1.3 dictates that jail startup
   never blocks on extension updates. The rebuild window opens only when upstream repositories
   have new commits (infrequent), and lasts only 2–4 seconds. If a second jail starts in that
   exact window, Pi reports an extension load error for that session, while the agent session itself
   boots cleanly.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-3: Handling multi-workspace ref conflicts in user scope.**
   What is the defined behavior if Workspace A configures `git:github.com/foo/bar@v1` and
   Workspace B configures `git:github.com/foo/bar@v2` in global settings?

   <!-- vantage: oq id=OQ-3 leaning="Last-writer-wins in .pi-shared-git for user scope; workspaces requiring conflicting versions must use project scope (.pi/settings.json)." -->

   _Leaning:_ Last-writer-wins in `.pi-shared-git` for user scope. Extensions declared in
   `~/.pi/agent/settings.json` are global user tools. This matches the maintainer's ruling on
   [`pi-extension-lifecycle.md`](pi-extension-lifecycle.md) [`OQ-1`](pi-extension-lifecycle.md#OQ-1) for npm packages: user-level extensions are shared capabilities
   where drift is avoided. Workspaces that genuinely require different branches or pins must declare
   them in project scope (`.pi/settings.json`), which installs to `<workspace>/.pi/git` without
   touching the shared machine store.

   **Answer:**
   > _(empty — fill in when decided)_

---

## 7. Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| **OQ-1** | Retain `.pi-shared-npm/.yolo-update.lock` as the single refresh lock | — | [§6](#6-open-questions) | — |
| **OQ-2** | Non-blocking launch during git dependency rebuilds | — | [§6](#6-open-questions) | — |
| **OQ-3** | Last-writer-wins for user scope; project scope for isolated pins | — | [§6](#6-open-questions) | — |
