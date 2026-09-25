---
title: "Pi extensions want machine-scoped storage and pre-launch refreshes across jails"
date: 2026-09-17
status: accepted
tags: [pi, extensions, updates, packages, machine-tier, launchers]
summary: "Architecture for managing Pi package and extension lifecycles across multiple YOLO jails: machine-scoped extension storage, rate-limited pre-launch updates, and cross-jail concurrency control."
vantage:
  status-chip: true
---

# Pi extensions want machine-scoped storage and pre-launch refreshes across jails

**Status:** DECIDED, 2026-09-20. **All three tiers are built.** The STORAGE tier
([§3.1](#31-storage-tier-decoupling-packages-from-session-state), [OQ-1](#OQ-1)) shipped
2026-09-21. The REFRESH and CONCURRENCY tiers ([§3.2](#32-execution-tier-pre-launch-auto-refresh),
[§3.3](#33-concurrency-tier-cross-jail-mutual-exclusion)) were built 2026-09-25 as a `refresh`
object that `packs/pi` declares on its `program` contribution. Both launcher templates render it:
`pi update --extensions`, run before the exec under a non-blocking lock in the shared store.
**Only the materializer half of [OQ-2](#OQ-2)'s ruling is built:** nothing in YOLO resolves or
records a Pi package's version, so `pi update --extensions` resolves as well as installs, and
Pi and the registry still choose the version. Unit-tested only so far, and the nested-jail
observation is still owed. Two nested jails launched from one outer jail share one store
(`paths.GlobalHome()` lives under the launching user's home), so the cross-jail case can be
observed that way too; until it is, a two-home unit test pins it. **One new question came out of
the build, [OQ-4](#OQ-4):** the lock serializes only the launcher's refresh. Pi's own startup
installs any configured package that is missing from the store, pinned or not, with no lock,
whenever the refresh did not reach it first. That happens when the refresh is throttled, when
the launch is contended, and for exact pins, which `pi update --extensions` skips.

> **In short.** Pi extensions belong in YOLO's machine-scoped storage tier rather than
> isolated per-workspace homes: decoupling extension storage from workspace session state
> allows a single rate-limited pre-launch update to keep all jails synchronized without
> redundant downloads or nagging interactive update warnings.

**Why it matters.** In the status quo, `packs/pi` scopes `~/.pi` entirely to the workspace,
so every jail manages an isolated `~/.pi/agent/npm` directory. Updating an extension in one
jail leaves every other jail stale, burning network bandwidth, duplicating disk space, and
triggering Pi's interactive TUI update warning box on startup in every other workspace.

**The shape.** Decouple `~/.pi/agent/npm` from the workspace-scoped `~/.pi` state directory
via a machine-scoped storage contribution (`scope: "machine"`) and symlink hook. Two ways to
refresh it, and [OQ-2](#OQ-2) ruled between them on 2026-09-20: **YOLO resolves and pins** the package set through
`internal/packsrc` + `packs.lock.json` ([Alternative D](#alternative-d-resolve-and-pin-through-yolos-existing-pack-source-store)),
and the launcher only *materializes* it, via `pi update --extensions`. **As built (2026-09-25),
only the materializer exists.** Nothing resolves or pins a Pi package, so `pi update --extensions`
also does the resolving, and the version choice stays with Pi and the registry
([OQ-2](#OQ-2)). [§3.2](#32-execution-tier-pre-launch-auto-refresh) still describes the
pre-reframe version, and that version is what was built.

**Cost.** Jails sharing the machine-scoped extension directory must synchronize npm writes
via a non-blocking directory lock. A failed update attempt or offline jail must gracefully
fall back to running the existing installed extension version.

**Start at [§3](#3-the-proposed-architecture)** — the storage and execution split. The rest falls out of it.

**Needs your ruling:** **[OQ-4](#OQ-4)**, filed 2026-09-25 by the build: who installs a package
the refresh did not reach (throttled, contended, or an exact pin), and under what lock. The
first three were ruled 2026-09-20; see
[§7](#7-decision-ledger). ✅ **The one check owed before the build is DONE (2026-09-22)**:
`pi update --extensions` is REAL — parsed by the installed pi 0.87.0's argv handler, accepted only
under `update`, and non-interactive. Measured statically from the bundle, so the no-agent-probing rule
holds. See [`OQ-2`](#OQ-2).

**Reads with:** [`pi-extension-lifecycle-plan.md`](pi-extension-lifecycle-plan.md)
(the companion implementation sketch — its blocking questions were ruled 2026-09-20, so what it owes
now is completion against the tree rather than a decision),
[`pi-pack-extensions.md`](./pi-pack-extensions.md) (the accepted sibling that assigns this doc the
fetch/resolve axis and names `internal/packsrc` + `packs.lock.json`),
[`slots-and-contributions.md`](./slots-and-contributions.md) (the role model of the same
constellation), [`program-delivery.md`](program-delivery.md) (the launcher and `agent_updates`
foundation), and [`../reference/macos-user-home-tiers.md`](../reference/macos-user-home-tiers.md)
(the machine vs workspace storage tiers, as built).

---

## 1. Context and status quo

Pi (`@earendil-works/pi-coding-agent`) provides an extensible coding assistant with support for
modular extensions, tools, skills, and themes declared as packages in `settings.json`:

```json
{
  "packages": [
    "npm:pi-lens",
    "npm:pi-git-tools"
  ]
}
```

### 1.1 How Pi installs and updates packages

When Pi runs:
1. **Installation root**: For user-scoped packages (`scope: "user"`, the default for global settings),
   Pi installs dependencies into `~/.pi/agent/npm` (`DefaultPackageManager.getNpmInstallRoot()`). It initializes
   a private `package.json` (`{ "name": "pi-extensions", "private": true }`) and runs `npm install`
   using the configured `npmCommand`.
2. **Headless updates**: Running `pi update --extensions` invokes `DefaultPackageManager.update()`.
   The command runs non-interactively (it uses saved project trust without prompting), inspects each
   unpinned package in `settings.json`, queries `npm view <spec> version --json`, and batch-installs
   newer releases.
3. **Startup version check**: On interactive startup, Pi's `InteractiveMode` launches
   `checkForPackageUpdates()` asynchronously in the background. It iterates through all configured
   packages, queries the npm registry, and if newer versions exist, injects an eye-catching warning
   box directly into the chat viewport:
   a box reading *"Package Updates Available — run `pi update --extensions`"*, naming each
   stale package.
   This check runs unconditionally unless `PI_OFFLINE=1` is exported in the environment. Pi
   provides no setting in `settings.json` to disable or suppress this notification.

### 1.2 The multi-jail friction

Before the storage tier shipped, `packs/pi/pack.json` declared only this for Pi's state (it
still declares it, beside the machine-scoped store [§3.1](#31-storage-tier-decoupling-packages-from-session-state) adds):

```json
{
  "at": ".pi",
  "kind": "state",
  "scope": "workspace"
}
```

Because `.pi` is workspace-scoped, each workspace jail receives an isolated directory at
`<workspace>/.yolo/home/pi` mounted to `/home/agent/.pi`:

* **Fragmented state**: If a user runs `pi update --extensions` in workspace `repo-alpha`,
  `repo-alpha` updates its local `~/.pi/agent/npm`. Workspace `repo-beta` remains on the old
  version.
* **Redundant downloads**: If five workspaces run `pi`, all five independently download and store
  identical copies of the packages and their transitive node dependencies.
* **Notification nag**: Every other workspace continues to display the startup warning banner
  prompting the user to run `pi update --extensions`.

The storage tier (shipped 2026-09-21) ends the first two for `~/.pi/agent/npm`: every jail on the
machine now reads one store. Ending the nag is the refresh tier's job. That tier was built
2026-09-25 and is still unobserved in a real jail ([§3.2](#32-execution-tier-pre-launch-auto-refresh)).

---

## 2. Goals and Non-Goals

### Goals
* **Single source of truth**: Installing or updating a global Pi extension in any jail makes the
  updated extension immediately available to all jails on the machine.
* **Zero startup friction**: Eliminate the "Package Updates Available" nag banner during normal
  operation by keeping extensions evergreen automatically.
* **Policy alignment**: Honor YOLO's established `agent_updates` policy (`internal/config/agentupdates.go`)
  and rate-limiting conventions (`UPDATE_INTERVAL = 3600`).
* **Concurrency safety**: Prevent race conditions and file corruption when multiple jails launch
  simultaneously and attempt to refresh extensions.

### Non-Goals
* **Project-local extension sharing**: Packages explicitly installed with `--local` into
  `<workspace>/.pi/npm` remain strictly scoped to that workspace and will not be shared across
  the machine.
* **Vendor code modification**: We do not patch `@earendil-works/pi-coding-agent` or maintain a fork.
  The solution must operate cleanly through Pi's public CLI, configuration surfaces, and directory contracts.

---

## 3. The proposed architecture

> ⚠ **[§3.2](#32-execution-tier-pre-launch-auto-refresh) is pre-reframe.** It describes refresh as a launcher-run `pi update --extensions`.
> [Alternative D](#alternative-d-resolve-and-pin-through-yolos-existing-pack-source-store) and
> [OQ-2](#OQ-2)'s ruling move the *resolution and the pin* to YOLO and leave the launcher
> to materialize. Read [§3.2](#32-execution-tier-pre-launch-auto-refresh) as the materializer, not the resolver.
> The resolver is not built, so as built (2026-09-25) the materializer resolves too.

The architecture consists of three coordinated tiers: storage decoupling, pre-launch auto-refresh,
and cross-jail mutual exclusion. One machine-scoped `~/.pi-shared-npm` holds the `package.json` and
`node_modules`; each jail's workspace-scoped `~/.pi/agent/npm` is a **symlink** to it (a bind
mount from the host), while `~/.pi/agent/sessions` stays per-workspace.

### 3.1 Storage tier: Decoupling packages from session state

Pi's state directory `~/.pi` contains two fundamentally different kinds of data:
1. **Workspace-specific state**: Chat session transcripts (`~/.pi/agent/sessions/`), history, and
   local authentication tokens. This *must* remain private to the workspace (`scope: "workspace"`).
2. **Tool and extension binaries**: Node modules installed in `~/.pi/agent/npm/`. These are plugins
   shared across all coding tasks, equivalent to installed CLI binaries or global mise tools.

We introduce a machine-scoped state directory contribution in `packs/pi/pack.json`:
* Directory: `.pi-shared-npm`
* Scope: `scope: "machine"` (backed by `paths.GlobalHome()`, which resolves to
  `~/.local/share/yolo-jail/home/.pi-shared-npm` on the host).

At container initialization, YOLO links `~/.pi/agent/npm` to `~/.pi-shared-npm`:
* On Podman and Container runtimes, `~/.pi-shared-npm` is mounted read-write into the jail from
  `paths.GlobalHome()`.
* On the `macos-user` backend, `~/.pi-shared-npm` lives directly in the sandbox user home, and
  is mirrored into the workspace sidecar so relative symlinks resolve cleanly
  (following the pattern established in [`darwinhomelayout.go`](../../internal/entrypoint/darwinhomelayout.go):
  `DeriveDarwinHomeLayout`, whose `Mirrors` are the machine tier as the sidecar sees it).
* An initialization hook ensures that if an existing workspace already has
  a populated `~/.pi/agent/npm` directory while `.pi-shared-npm` is empty, the contents are migrated
  to the shared store rather than lost (following the "the shared side always wins, but initial local
  populates empty shared" invariant from [`linkThroughShared`](../../internal/entrypoint/sharedlink.go)).

> [!NOTE]
> **As built (2026-09-21), the hook is named `shared_directory`, not `shared_extension_storage`.**
> Two corrections, both from the recon that preceded the build. The name: an "extension storage"
> hook is the `claude_plugins` shape one step removed — a name only one pack could ever want for
> a mechanism any pack can use — and the ruling that retired `claude_plugins` forbids it
> ([`pi-pack-extensions.md` OQ-2](./pi-pack-extensions.md#10-decision-ledger), 2026-09-19). What it does is the
> DIRECTORY twin of `shared_credentials`, so that is what it is called. The reuse: `linkThroughShared`
> could not be adapted as written — it is file-shaped end to end, and its emptiness test INVERTS for
> a directory — so the decision table is parameterized by a payload shape and the file and directory
> cases share one copy of the ORDER, which is the part a data-loss bug once got wrong. The migration
> also runs IN THE JAIL rather than host-side, because the two paths are separate bind mounts and
> `rename(2)` across a mount point is `EXDEV` even on one device (measured).

### 3.2 Execution tier: Pre-launch auto-refresh

Relying on human users to manually run `pi update --extensions` across N jails guarantees drift.
Instead, we adopt YOLO's proven transitive update model used for agent binaries and MCP/LSP servers
(the agent launcher templates in [`shims.go`](../../internal/entrypoint/shims.go), whose
transitive MCP/LSP step runs `yolo internal refresh-servers` before the agent's `exec`):

1. **The Trigger**: The refresh executes inside `/home/agent/.yolo/bin/launch/pi` strictly **before**
   `exec "$REAL_BIN"`.
2. **The Throttle**: The launcher checks an update stamp file:
   `~/.cache/yolo-agent-stamps/pi-extensions.stamp`.
   * Interval: `UPDATE_INTERVAL = 3600` (1 hour).
   * If `now - mtime < 3600`, the check is skipped.
3. **The Policy Gate**: If `agent_updates` in the user configuration has frozen pack `pi`
   (`"agent_updates": { "pi": false }`), the update check is skipped unconditionally.
4. **Execution**: If due, the launcher executes:
   ```bash
   YOLO_BYPASS_SHIMS=1 timeout 60 pi update --extensions >&2 || true
   touch ~/.cache/yolo-agent-stamps/pi-extensions.stamp
   ```
5. **Outcome on Interactive TUI**: Because the latest extension release is pulled before the agent
   boots, Pi's startup `checkForPackageUpdates()` will compare the installed version against npm
   registry, discover they match, and **never render the notification warning box**.

> [!NOTE]
> **As built (2026-09-25).** Steps 1–4 hold, with the changes below. The launcher's tests are in
> [`prelaunchrefresh_test.go`](../../internal/entrypoint/prelaunchrefresh_test.go) and the
> manifest field's in [`refresh_test.go`](../../internal/packdecl/refresh_test.go). The
> `macos-user` bound below is the one item with no test.
>
> - **The pack declares the refresh; core does not know it is Pi.** The plan sketch put a
>   `_refresh_pi_extensions` branch for binary `pi` into the npm launcher template. Both templates
>   are shared by every program, so that branch would teach core what an agent is. Instead a
>   `program` contribution may carry a `refresh` object (the name, and the term *pre-launch
>   refresh*, are coined for this build). The object holds the vendor's argv and the lock
>   directory. `packs/pi` declares
>   `"refresh": {"argv": ["update", "--extensions"], "lock": ".pi-shared-npm/.yolo-update.lock"}`,
>   and [`prelaunchrefresh.go`](../../internal/entrypoint/prelaunchrefresh.go) renders whatever a
>   pack declares into both launcher templates. `packdecl.Refresh` documents the field. `packdecl`
>   refuses it on any kind but `program`, and refuses an empty argv, an empty word, and a lock
>   that is missing, absolute, escaping, unclean, or has no directory above it.
> - **The stamp is `~/.cache/yolo-agent-stamps/refresh/pi.stamp`**, not `pi-extensions.stamp`.
>   The `refresh/` subdirectory keeps a bin name from colliding with it, as `servers/` does for the
>   MCP/LSP stamps. It is machine-global because `~/.cache` is, so one refresh an hour covers the
>   machine-scoped npm store every jail shares. The refresh's stamp is separate from the program's
>   own update stamp, so a fresh `pi.stamp` does not suppress it.
>
>   ⚠ **The stamp also throttles what the refresh reaches outside that store.**
>   `pi update --extensions` reads its package list from `~/.pi/agent/settings.json` and a
>   project's `.pi/settings.json`. It also updates the git packages under `~/.pi/agent/git`. All
>   of those live in the workspace-scoped `~/.pi`. So a refresh in one workspace suppresses every
>   other workspace's refresh for the next hour. Within that hour, a workspace with its own git
>   packages or a different package list can still show Pi's update box, and it can reach a
>   package no refresh installed (see [OQ-4](#OQ-4)). The *cannot take the lock* branch stamps
>   machine-wide too, so one jail whose store mount failed suppresses the refresh in the others
>   for an hour. Keying the stamp on the workspace would trade that for one refresh per workspace
>   per hour. That choice is not ruled.
> - **It runs under the resolved Node.** `pi` declares a `node_floor`, so the refresh runs through
>   the same interpreter prefix the exec uses. Otherwise the workspace's `mise` pin would choose the
>   Node that `pi update` runs under.
> - **Two redirections were added.** stdin comes from `/dev/null`, so the refresh can never read
>   the user's terminal. stdout goes to stderr, so a piped launch (`pi -p … | consumer`) receives
>   only Pi's own output.
> - **Failure is reported and the launch goes ahead.** Exit status 124 (`timeout(1)`'s expiry) is
>   reported as a timeout, and any other non-zero status by its number. Both are stamped, and in
>   every case Pi still launches. The bound is the launcher's `_bounded`, which means
>   `timeout UPDATE_TIMEOUT` (60 s) where `timeout(1)` exists. ⚠ A stock macOS has no `timeout(1)`,
>   so on `macos-user` the refresh runs **unbounded**. This is the same trade the program's own
>   update already makes there. The lock's heartbeat ([§3.3](#33-concurrency-tier-cross-jail-mutual-exclusion))
>   stops an unbounded refresh from letting a second one into the store, but it does not bound the
>   launch that is waiting on it.
> - **The refresh does not run in two places.** `yolo pack update` (`YOLO_PACK_UPDATE=1`) exits
>   before reaching it, and so does the launcher's re-entry path.
>
> **What Pi does offline**, read statically from pi 0.87.1's `dist/core/package-manager.js` and not
> measured by running it. [OQ-2](#OQ-2) left this unknown. When `npm view` fails,
> `shouldUpdateNpmSource` returns `true` ("Preserve existing update behavior when version lookup
> fails"). An offline refresh therefore goes on to `npm install`, whose failure rejects and reaches
> `handlePackageCommand`'s `catch`, which sets `process.exitCode = 1`. So an offline refresh most
> likely exits 1, after each `npm view` has failed or reached Pi's own `NETWORK_TIMEOUT_MS` (10 s;
> four checks run at a time). The launcher's 60 s bound covers that on the container backends. With `PI_OFFLINE=1`
> exported, `updateConfiguredSources` returns at once.

### 3.3 Concurrency tier: Cross-jail mutual exclusion

Because multiple jails share the machine-scoped `.pi-shared-npm` directory, two jails launched
simultaneously could attempt to run `npm install` in the same directory concurrently, causing npm cache
lock contention or corrupted `node_modules`.

We enforce mutual exclusion using YOLO's standard non-blocking directory lock algorithm:
* **Lock Path**: `~/.pi-shared-npm/.yolo-update.lock`
* **Acquisition (`_take_lock`)**: Atomic `mkdir "$LOCK_PATH"`.
* **Non-blocking fallback**: If the lock is held by another process:
  * Check lock age. If older than `STALE_LOCK = 600` (10 minutes), clear stale lock and retry.
  * If still held, print a non-fatal notice to stderr:
    ```text
    pi: another extension update is in progress — running installed extensions.
    ```
  * Immediately proceed to launch Pi using the existing installed packages. A jail *never* blocks
    indefinitely waiting on an extension update lock.

> [!NOTE]
> **As built (2026-09-25).** The lock is the mkdir lock described above. The pack declares its
> path, since the lock's parent must be the store the refresh writes. It is taken by a new
> helper, `_take_refresh_lock`, rather than the templates' install-prefix `_take_lock`, because
> it must tell a missing store from a held lock and must carry an owner token (both below).
> [`prelaunchrefresh.go`](../../internal/entrypoint/prelaunchrefresh.go) has the full argument.
>
> - **mkdir, not flock.** A flock arbitrates only within one kernel. Apple Container runs each
>   jail in its own VM over a shared host directory, and there only the host filesystem's own
>   `mkdir(2)` is atomic. There is also no `flock(1)` in the image or on a stock macOS.
> - **A missing lock and a held lock are told apart.** The helper gives three answers: *taken*,
>   *held*, and *cannot be taken at all* (the store is missing or read-only). `_take_lock`'s two
>   answers would print "another update is in progress" to a user whose mount never happened. The
>   store is never created by the launcher. This follows `internal/cli/run/flock.go`'s lesson: its
>   error path fails open, so a WRITE must be gated on the lock really being held. Here the launch
>   fails open and the refresh fails closed.
> - **An owner token.** The lock holds a `.yolo-lock-owner` token (`$$` plus `$RANDOM` plus the
>   time, since pids repeat across jails' namespaces). Releasing the lock checks the token, so a
>   holder whose lock was broken as stale and taken over does not delete the new holder's lock when
>   it finishes. A stale break removes only that token file and an empty directory. It never runs
>   `rm -r` on a path a manifest named.
> - **A heartbeat keeps a held lock young.** The stale break reads nothing but the lock's age.
>   On the container backends `timeout 60` keeps every refresh far shorter than `STALE_LOCK`. On
>   `macos-user` the refresh is unbounded ([§3.2](#32-execution-tier-pre-launch-auto-refresh)),
>   and without a heartbeat, a live refresh stalled past 600 s would have its lock broken and a
>   second refresh would start in the same store. So while it refreshes, the launcher touches the
>   lock every 60 s (`REFRESH_HEARTBEAT`), with `touch -c` so that a beat never creates the path.
>   The beat stops when the refresh returns, when the launcher that took the lock is gone (a
>   killed launcher's lock still ages), or when the lock stops carrying that launcher's token.
> - **The lock's name marks it as yolo's bookkeeping.** The lock lives inside the store, and the
>   `shared_directory` hook's migration judges the store by its contents. A store no extension was
>   ever installed into holds only the lock while the first refresh runs, and after an interrupted
>   one. Counted as content, that lock made the store "populated", and the hook then discarded a
>   workspace's real `~/.pi/agent/npm` in favor of a store that held nothing. Now `packdecl`
>   refuses a `lock` whose name does not start with `.yolo-` (`packdecl.StoreBookkeepingPrefix`),
>   and the hook's emptiness test skips entries with that prefix. ⚠ The migration's copy-in and
>   the refresh still take different locks, so a legacy workspace's first boot can copy its tree
>   into a store that another jail's refresh has just locked and is about to write.
> - **The notice was reworded.** In a container jail it reads *"pi: another refresh holds
>   /home/agent/.pi-shared-npm/.yolo-update.lock — running what is installed."* The contended
>   launch writes no stamp, because the holder writes one when it finishes.
>
> ⚠ **One race remains.** Breaking a stale lock is check-then-act. Two launchers can judge the
> same lock stale in the same instant, and then both can end up holding it. That requires a holder
> whose launcher died (with the heartbeat, only then does a lock age past `STALE_LOCK`) *and* two
> launches within a few syscalls of each other.
>
> ⚠ **The lock covers only the launcher's refresh, not every writer of the store.** Pi's own
> resource loader calls `packageManager.resolve()` at startup. That call installs any configured
> npm package, pinned or not, that is missing from the store or does not match its configured
> range, and it takes no lock. The refresh reaches such a package first only when it runs first.
> It does not run first when it is throttled (the stamp is machine-global, while the package list
> is per-workspace). It does not run first on a contended launch, which execs Pi at once, so a
> package the holder is still installing is installed a second time, concurrently, by the second
> jail's Pi. And it never reaches an exact pin, which `pi update --extensions` skips. So §4.1's
> invariant 1 is not met for any package the refresh has not already installed. That question is
> [OQ-4](#OQ-4).

---

## 4. Invariants and Failure Modes

### 4.1 Invariants
1. **One Writer at a Time**: Only one process across the entire host may modify `.pi-shared-npm` at any instant.
   ⚠ *Not met as built (2026-09-25)* for a package the refresh has not installed. Pi's own startup
   installs it with no lock ([§3.3](#33-concurrency-tier-cross-jail-mutual-exclusion), [OQ-4](#OQ-4)).
2. **Launch Priority**: Starting the interactive agent session always takes precedence over updating extensions.
   No network failure, npm error, or lock contention may prevent Pi from launching.
3. **Receipts & Provenance**: Extension updates touch the stamp file on both success and failure to
   throttle repeated retries and prevent hammering the network.

### 4.2 Failure Mode Matrix

| Failure Mode | Detection | System Response | User Impact |
| :--- | :--- | :--- | :--- |
| **Offline / Network Down** | `npm view` times out or returns non-zero | `timeout 60` kills updater; stamp is touched; launcher logs advisory warning to stderr; Pi starts | Pi runs with existing installed extensions. No retry for 1 hour. |
| **npm Registry Outage** | `pi update --extensions` exits non-zero | Non-zero exit is caught; stamp is touched; Pi starts | Stored extensions remain active; error reported in stderr. |
| **Concurrent Jail Startup** | `mkdir .yolo-update.lock` fails (EEXIST) | Launcher logs notice to stderr and skips update | Second jail starts immediately without delay. ⚠ As built, a configured package still missing from the store is then installed by the second jail's Pi itself, with no lock ([OQ-4](#OQ-4)). |
| **Stale Lock (Crashed Jail)** | Lock directory age > 600 seconds | Stale directory removed; lock acquired; update proceeds | Self-healing without human intervention. As built, a live holder heartbeats its lock, so only a lock whose launcher died ages past 600 s. |
| **Shared store missing or read-only** (added 2026-09-25, as built) | `mkdir` of the lock fails and no lock directory exists | Refresh skipped and reported as *cannot take the refresh lock*, never as contention; the store is not created; stamp touched | Pi runs with what is installed. The notice repeats hourly, not on every launch. |
| **Unparseable Package Config** | Malformed package spec in `settings.json` | Pi's package manager logs error and skips bad entry | Valid packages continue to load; error visible in stderr. |

---

## 5. Alternatives Considered

### Alternative A: Keep workspace-scoped storage and update via pre-launch hook
* **Shape**: Keep `~/.pi` entirely workspace-scoped. Run `pi update --extensions` in each jail's launcher.
* **Verdict**: **Rejected**. Every workspace must download identical npm packages (~15–50 MB each).
  Updating packages in one workspace leaves all other workspaces with stale extensions and nagging warnings
  until each individual workspace is separately launched and updated.

### Alternative B: Suppress Pi's startup check via `PI_OFFLINE=1`
* **Shape**: Inject `PI_OFFLINE=1` into the environment when starting Pi.
* **Verdict**: **Rejected**. While `PI_OFFLINE=1` suppresses `checkForPackageUpdates()`, it also
  disables model provider calls and network fetch capabilities across other parts of the agent runtime.

### Alternative C: Implement a full standalone Go extension refresher (`yolo internal refresh-pi-extensions`)
* **Shape**: Reimplement npm version resolution and package installation in Go inside `yolo internal`.
* **Verdict**: **Rejected as unnecessary duplication**. Unlike MCP/LSP servers where YOLO already manages
  the installation manifests, Pi already ships a complete, robust package manager CLI (`pi update --extensions`)
  with support for git repositories, semver ranges, and package manifests. Shelling out to Pi's own CLI
  maintains fidelity with upstream behavior.

### Alternative D: Resolve and pin through YOLO's existing pack source store

* **Shape**: Treat a Pi package as a source YOLO RESOLVES, using Pi/npm only to MATERIALIZE
  its dependency tree. `internal/packsrc` already does the control half — a mandatory `ref`,
  a host-side fetch into `mirrors/<repo>` + `trees/<sha>`, a commit-pinned `packs.lock.json`,
  and a strictly offline launch. The launcher then only REPORTS, which is the posture
  `program via npm` already takes (`yolo pack update` is the one act that resolves).
* **Verdict**: **Chosen — [OQ-2](#OQ-2) ruled option (c), which is this alternative, on
  2026-09-20.** Surfaced 2026-09-19. Alternative C's objection
  ("don't reimplement npm") is about the PACKAGE MANAGER; this alternative keeps npm as the
  installer and moves only the RESOLVER, so the objection does not apply. The reason to
  prefer it is the precedent survey's finding that none of the plugin/package ecosystems
  ships a lockfile or a rollback, so delegating the version choice to `pi update --extensions`
  gives up the pin — the exact seam a distributor is supposed to occupy. This reframes
  [OQ-2](#OQ-2): the launcher may still call `pi update`, but only after YOLO has resolved and
  pinned, and it should be able to say what it resolved instead of asking a registry.

> **A third path, already available:** a pack can *declare* its Pi packages with a `config-list`
> contribution on surface `pi/settings` at path `/packages`, which appends its entries to Pi's
> `packages` array without replacing the entries other packs or the user put there (built
> 2026-09-24 — [`pack-system.md`](../reference/pack-system.md#adding-entries-to-an-array-config-list),
> designed in [`additive-config-lists.md`](./additive-config-lists.md)). This note used to name a
> `config-overlay` with `managed.packages`; that still works, but a merge patch replaces the array
> whole, so each overlay copies — and drifts from — every package another pack selected, which is
> the case the list kind exists for. Either is the declaration half — it says what should be
> present without fetching anything — and composes with D, which owns the fetch and the pin.

---

## 6. Open Questions

1. ✅ **OQ-1: Storage tier for Pi extensions.**
   Should `~/.pi/agent/npm` live in machine-scoped storage (`paths.GlobalHome()`, mounted across
   all workspaces) or remain workspace-scoped with independent downloads?

   <!-- vantage: oq id=OQ-1 leaning="Machine-scoped storage — extensions are shared tool capabilities like global binaries, and duplicating node_modules across N workspaces wastes disk and creates cross-jail version drift." -->

   **Answer (2026-09-20):**
   > **Machine-scoped storage.** Extensions are shared tool capabilities, like global binaries.
   > Duplicating `node_modules` across N workspaces wastes disk **and creates cross-jail version
   > drift** — the second is the load-bearing half, because disk is cheap and a jail silently
   > running a different extension version from its neighbour is not.

2. ✅ **OQ-2: Update execution mechanism.**
   Three candidates: (a) run `pi update --extensions` inside the generated launcher
   (`/home/agent/.yolo/bin/launch/pi`); (b) a dedicated Go entrypoint subcommand
   (`yolo internal refresh-pi-extensions`) like `refresh-servers`; or (c) **YOLO resolves and
   pins the package set through `internal/packsrc` + `packs.lock.json`, and the launcher only
   materializes it** ([Alternative D](#alternative-d-resolve-and-pin-through-yolos-existing-pack-source-store)).

   <!-- vantage: oq id=OQ-2 leaning="Option (c): YOLO resolves and PINS through internal/packsrc + packs.lock.json, and the launcher only materializes via `pi update --extensions` under a non-blocking lock. Pi's CLI keeps the package-manager half (no npm reimplemented), but the VERSION CHOICE moves to YOLO — the seam a distributor must own, since no ecosystem here ships a lockfile or rollback." -->

   **Answer (2026-09-20): option (c).**
   > YOLO resolves and **pins** through `internal/packsrc` + `packs.lock.json`, and the launcher
   > only materializes, via `pi update --extensions` under a non-blocking lock. Pi's CLI keeps the
   > package-manager half — no npm reimplemented — but the **version choice moves to YOLO**: the
   > seam a distributor must own, since no ecosystem here ships a lockfile or a rollback.

   **Only the materializer half is built (2026-09-25,
   [§3.2](#32-execution-tier-pre-launch-auto-refresh)); the resolve-and-pin half is not.** The
   mechanism (c) would extend does ship. `internal/packsrc` has `addr.go`, `lock.go` and
   `store.go`, and `packs.lock.json` is real at `~/.config/yolo-jail/packs.lock.json`
   (`LoadLock`/`Save`, beside the user config). So (c) is an extension of a shipping mechanism
   rather than a new one, which is most of why it is the right answer. But that mechanism
   resolves and pins PACKS only. An address is a `file://` directory or a `git+` repository
   (`packsrc.KindFile`, `KindGit`), and a `LockEntry` records a pack's name, source and commit.
   Nothing resolves, pins or records a Pi package's version. So as built, `pi update --extensions`
   resolves as well as materializes, and the version choice is still Pi's and the registry's:
   the in-range maximum at refresh time. ⚠ It also skips EXACT-version pins; see [OQ-4](#OQ-4).

   ✅ **`pi update --extensions` is VERIFIED, 2026-09-22 — slice one's check is done and the flag
   is real.** Measured STATICALLY against the installed `@earendil-works/pi-coding-agent@0.87.0`,
   by reading its bundle rather than running it, so `AGENTS.md`'s rule that nothing probes an agent
   CLI beyond `--version` is intact:

   - The argv parser handles `--extensions` explicitly and accepts it **only** under the `update`
     command — `if (arg === "--extensions") { command === "update" ? extensionsFlag = true :
     invalidOption = invalidOption ?? arg; continue }`. Its siblings on the same command are
     `--self`, `--models` and `--all`. So the flag is not merely mentioned in help text; it is
     parsed, and it is rejected as an invalid option anywhere else.
   - It is the spelling Pi itself prescribes: two separate user-facing strings tell the user to run
     `<app> update --extensions`, one when extensions were skipped and one from the
     package-update notification.
   - **Non-interactive**: the update path holds no interactive primitive — no `createInterface`,
     `inquirer`, `prompts(`, `await confirm` or `question(` anywhere in it.

   So (b), a Go entrypoint subcommand, is **not needed as the materializer**, and the fallback
   stays what it was: a fallback. ⚠ What this does NOT establish is the flag's exit code or its
   behaviour offline — a static read cannot see either, and the implementer should not assume
   success is the only outcome. (The build treats every non-zero status as a failure to report
   and launch past. A further static reading of pi 0.87.1, not a measurement, suggests an offline
   refresh exits 1; see the as-built note in [§3.2](#32-execution-tier-pre-launch-auto-refresh).)

3. ✅ **OQ-3: Handling Pi's in-app update notification check.**
   Pi's interactive TUI unconditionally runs `checkForPackageUpdates()` on startup if not offline,
   warning the user if npm has a newer version. When pre-launch update succeeds or is throttled within
   1 hour, npm will be up-to-date in the happy path. But when an update check fails or is throttled,
   should YOLO attempt to suppress Pi's warning box (e.g. by setting pinned versions in generated
   settings) or leave it untouched?

   <!-- vantage: oq id=OQ-3 leaning="Leave Pi's in-app notification untouched — in the normal path, the pre-launch update ensures extensions are up-to-date before the TUI starts, so the in-app check passes cleanly without warning." -->

   **Answer (2026-09-20):**
   > **Leave it untouched.** In the normal path the pre-launch update runs before the TUI starts,
   > so the in-app check sees the installed package matching `@latest` and passes cleanly.
   > Suppressing it artificially buys little and costs a pinned-version mechanism nobody asked for.

   ⚠ **This ruling is conditional on [OQ-2](#OQ-2)'s pin, and the two now pull against each other.** Under
   (c) the version YOLO pins is whatever `packs.lock.json` records, which is *deliberately* not
   always `@latest` — that is what a pin is for. So a jail running a correctly pinned older
   extension will see Pi's warning box, and it will be **right** rather than spurious. Left
   untouched anyway: a user told their pinned version is behind is being told the truth, and the
   alternative is yolo suppressing a vendor's honest notice about software yolo chose the version
   of. If that proves noisy in practice it is a new question, not this one.

   ⚠ **Correction (2026-09-25): an EXACT pin does not trigger the box.** This was read
   statically from pi 0.87.1's `dist/core/package-manager.js` and not measured by running it. In
   `checkForAvailableUpdates`, `if (parsed.type === "local" || parsed.pinned) return undefined`
   skips every pinned package, and `pinned` is `isExactNpmVersion(version)`. A RANGE is not
   `pinned`. `npmHasAvailableUpdate` compares against `maxSatisfying(versions, range)`, and
   `pi update --extensions` installs that same in-range maximum, so after a refresh a range sees
   nothing newer either. The pull between the two rulings described above therefore does not
   happen. The ruling stands, and nothing needs to be suppressed. The ruling was made against
   0.87.0, and this reading is of 0.87.1.

4. 💬 <a id="OQ-4"></a>**[OQ-4](#OQ-4): who installs a package the refresh did not reach, and under what lock?**
   Filed 2026-09-25 by the build of [§3.2](#32-execution-tier-pre-launch-auto-refresh), and widened
   the same day in review from exact pins to every package the refresh did not reach. It is
   **open**.

   The lock of [§3.3](#33-concurrency-tier-cross-jail-mutual-exclusion) serializes only the
   launcher's refresh. Pi installs packages at one more point, its own startup. The resource loader
   calls `packageManager.resolve()`, which installs any configured npm package that
   `!existsSync(installedPath)` or `!installedNpmMatchesConfiguredVersion(…)`, pinned or not.
   **That install takes no lock:** `package-manager.js` contains no lock primitive (pi 0.87.1,
   `dist/core/package-manager.js` and `dist/core/resource-loader.js`, read statically). The
   refresh reaches a package first only when it runs first, and three cases leave it behind:

   - **Throttled.** The refresh's stamp is machine-global, but the package list is per-workspace:
     `~/.pi/agent/settings.json` lives in the workspace-scoped `~/.pi`. A package a workspace
     declares within the hour after any refresh on the machine is installed by that workspace's
     Pi, unlocked.
   - **Contended.** A launch that finds the lock held skips the refresh and execs Pi at once
     ([§3.3](#33-concurrency-tier-cross-jail-mutual-exclusion)). If the holder is still
     installing a package, the second Pi's `resolve()` finds it missing and runs its own
     `npm install` into the same store. That makes two writers.
   - **Exact pins.** `pi update --extensions` skips them. `updateConfiguredSources` adds an npm
     package to the candidates only `if (!parsed.pinned)`, with the comment *"Pinned npm versions
     are fixed"*. A pinned package that is missing, or whose pin moved, is only ever installed by
     `resolve()`.

   So two jails that start Pi while a configured package is missing from the store can both write
   it, which is the writer §3.3 exists to serialize: §4.1's invariant 1 is not met for any package
   the refresh has not already installed. And the case that ruling (c) is *about*, a version YOLO
   chose, is one the refresh never reaches.

   The options:

   - **(a) Accept and document.** The window opens whenever a configured package, pinned or not,
     is missing from the store or outside its configured range: the first Pi launch on a machine
     with packages configured, a newly declared package, or a moved pin. It closes once one install
     of that package lands. Nothing is built.
   - **(b) Find a Pi verb that installs configured packages without persisting.** pi 0.87.1 has
     none by static reading. `update` skips exact pins. `install <source>` calls
     `installAndPersist`, which writes the source into `settings.json`, a file yolo renders.
   - **(c) Have the launcher pre-install exact pins itself, under the lock.** That means
     `npm install --prefix <store> <spec>` into the layout Pi's `getNpmInstallRoot` uses. It works
     against Pi's install layout from outside, which is [Alternative C](#alternative-c-implement-a-full-standalone-go-extension-refresher-yolo-internal-refresh-pi-extensions)'s
     objection in a smaller form. It covers the exact-pin case only, not the throttled or
     contended cases.
   - **(d) Have a contended launch wait, bounded, for the holder before it execs Pi.** This closes
     the contended case. It costs §3.3's rule that a jail never waits on the lock, and it does
     nothing for the throttled case or for exact pins.

   <!-- vantage: oq id=OQ-4 leaning="(a) Accept and document: the unlocked install happens only while a configured package is missing from the store or outside its range, and it closes once one install lands. No shipped pack declares a Pi package today." -->

   _Leaning (the builder's, restated after review; not a ruling):_ (a) for now. The window is
   bounded per package: it opens while that package is missing from the store or outside its
   range, and closes once one install lands. No shipped pack declares a Pi package today, so
   yolo opens the window only for packages a user configures. Revisit this when a shipped pack
   first declares one through `config-list` on `pi/settings`.

   **Answer:**

---

## 7. Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| **OQ-1** | **Machine-scoped storage.** Extensions are shared tool capabilities like global binaries; per-workspace copies waste disk and, load-bearingly, create cross-jail version drift | 2026-09-20 | [§6](#6-open-questions) | **yes**, 2026-09-21 — `packs/pi` declares `.pi-shared-npm` at `scope: "machine"` plus a `shared_directory` hook (NOT `shared_extension_storage`; see [§3.1](#31-storage-tier-decoupling-packages-from-session-state)) **MEASURED in a nested jail 2026-09-21**: `~/.pi/agent/npm -> ../../.pi-shared-npm`, resolving and writable, and a write inside the jail landed at the LAUNCHER's `~/.local/share/yolo-jail/home/.pi-shared-npm/` — so the store is genuinely machine-scoped across the boundary, which is the cross-jail drift the ruling is about. |
| **OQ-2** | **Option (c).** YOLO resolves and PINS through `internal/packsrc` + `packs.lock.json`; the launcher only materializes, under a non-blocking lock. Pi keeps the package-manager half; the VERSION CHOICE moves to YOLO, the seam a distributor must own since no ecosystem here ships a lockfile or rollback. ✅ The pack resolver that (c) would extend ships (it resolves packs, not Pi packages), and the materializer's `pi update --extensions` flag is **VERIFIED** — parsed by pi 0.87.0 only under `update`, and non-interactive (read statically from the bundle, 2026-09-22). ⚠ Still unknown, because a static read cannot see them: its exit code and its behaviour offline | 2026-09-20 | [§6](#6-open-questions), [Alternative D](#alternative-d-resolve-and-pin-through-yolos-existing-pack-source-store) | **partly, 2026-09-25: the materializer only, unit-tested, not yet observed in a jail.** `packs/pi` declares `"refresh": {"argv": ["update", "--extensions"], "lock": ".pi-shared-npm/.yolo-update.lock"}` on its `program`, and [`prelaunchrefresh.go`](../../internal/entrypoint/prelaunchrefresh.go) renders it into both launcher templates: stamp-throttled, `agent_updates`-gated, bounded, stdin from `/dev/null`, stdout to stderr, and run under the heartbeated lock of [§3.3](#33-concurrency-tier-cross-jail-mutual-exclusion). [`prelaunchrefresh_test.go`](../../internal/entrypoint/prelaunchrefresh_test.go) pins it, including a two-home contention cell and a call-site cell that goes through the shipped manifest. ⚠ **The resolve-and-pin half is not built.** Nothing in YOLO resolves or records a Pi package version, so the version choice is still Pi's and the registry's. ⚠ The lock covers the refresh only: Pi's own startup installs a package the refresh did not reach, including every exact pin, with no lock ([OQ-4](#OQ-4)) |
| **OQ-3** | **Leave Pi's in-app notification untouched.** The pre-launch update runs before the TUI starts, so the check passes cleanly in the normal path. ⚠ The caveat that a correctly pinned older extension WILL trigger the warning was **corrected 2026-09-25**: by static reading of pi 0.87.1, `checkForAvailableUpdates` skips exact pins and compares a range only within that range, so the two rulings do not pull against each other | 2026-09-20 | [§6](#6-open-questions) | **n/a** — nothing to build. The refresh adds no suppression, which is this ruling |
| **OQ-4** | — **open.** Who installs a package the refresh did not reach, and under what lock. Pi's own startup `resolve()` installs any configured package missing from the shared store, pinned or not, with no lock. It gets there first when the refresh is throttled (machine-global stamp, per-workspace package list), when the launch is contended, and for every exact pin, which `pi update --extensions` skips. The builder leans toward (a), accept and document | — | [§6](#6-open-questions) | — |
