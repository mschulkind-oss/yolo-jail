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

**Status:** DECIDED, 2026-09-20. **Every ruling is in; nothing is built.** The resolve-and-pin
half of [OQ-2](#OQ-2) rests on `internal/packsrc` + `packs.lock.json`, which SHIP; the
materializer rests on a Pi CLI flag this tree never invokes, and confirming it is slice one.

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
refresh it, and the leaning has moved: **YOLO resolves and pins** the package set through
`internal/packsrc` + `packs.lock.json` ([Alternative D](#alternative-d-resolve-and-pin-through-yolos-existing-pack-source-store)),
and the launcher only *materializes* it — Pi's own `pi update --extensions` is the fallback, not
the mechanism. [§3.2](#32-execution-tier-pre-launch-auto-refresh) still describes the pre-reframe
version.

**Cost.** Jails sharing the machine-scoped extension directory must synchronize npm writes
via a non-blocking directory lock. A failed update attempt or offline jail must gracefully
fall back to running the existing installed extension version.

**Start at [§3](#3-the-proposed-architecture)** — the storage and execution split. The rest falls out of it.

**Needs your ruling:** **None** — all three were ruled 2026-09-20. See
[§7](#7-decision-ledger). ⚠ One check is owed before the build: `pi update --extensions` is
CLAIMED by this doc and invoked nowhere in the tree.

**Reads with:** [`pi-extension-lifecycle-plan.md`](pi-extension-lifecycle-plan.md)
(the companion implementation sketch — incomplete while [`OQ-1`](#OQ-1) and [`OQ-2`](#OQ-2) are open),
[`pi-pack-extensions.md`](./pi-pack-extensions.md) (the accepted sibling that assigns this doc the
fetch/resolve axis and names `internal/packsrc` + `packs.lock.json`),
[`slots-and-contributions.md`](./slots-and-contributions.md) (the role model of the same
constellation), [`program-delivery.md`](program-delivery.md) (the launcher and `agent_updates`
foundation), and [`macos-user-home-tiers.md`](macos-user-home-tiers.md) (the machine vs workspace
storage tier design).

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

In YOLO, `packs/pi/pack.json` currently declares:

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
> [OQ-2](#OQ-2)'s current leaning move the *resolution and the pin* to YOLO and leave the launcher
> to materialize. Read [§3.2](#32-execution-tier-pre-launch-auto-refresh) as the materializer, not the resolver.

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
  (following the pattern established in [`darwinhomelayout.go`](../../internal/entrypoint/darwinhomelayout.go#L13-L100)).
* An initialization hook (`shared_extension_storage`) ensures that if an existing workspace already has
  a populated `~/.pi/agent/npm` directory while `.pi-shared-npm` is empty, the contents are migrated
  to the shared store rather than lost (following the "the shared file always wins, but initial local
  populates empty shared" invariant from [`linkThroughShared`](../../internal/entrypoint/claude.go#L59-L86)).

### 3.2 Execution tier: Pre-launch auto-refresh

Relying on human users to manually run `pi update --extensions` across N jails guarantees drift.
Instead, we adopt YOLO's proven transitive update model used for agent binaries and MCP/LSP servers
([`shims.go:1306-1315`](../../internal/entrypoint/shims.go#L1306-L1315)):

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

---

## 4. Invariants and Failure Modes

### 4.1 Invariants
1. **One Writer at a Time**: Only one process across the entire host may modify `.pi-shared-npm` at any instant.
2. **Launch Priority**: Starting the interactive agent session always takes precedence over updating extensions.
   No network failure, npm error, or lock contention may prevent Pi from launching.
3. **Receipts & Provenance**: Extension updates touch the stamp file on both success and failure to
   throttle repeated retries and prevent hammering the network.

### 4.2 Failure Mode Matrix

| Failure Mode | Detection | System Response | User Impact |
| :--- | :--- | :--- | :--- |
| **Offline / Network Down** | `npm view` times out or returns non-zero | `timeout 60` kills updater; stamp is touched; launcher logs advisory warning to stderr; Pi starts | Pi runs with existing installed extensions. No retry for 1 hour. |
| **npm Registry Outage** | `pi update --extensions` exits non-zero | Non-zero exit is caught; stamp is touched; Pi starts | Stored extensions remain active; error reported in stderr. |
| **Concurrent Jail Startup** | `mkdir .yolo-update.lock` fails (EEXIST) | Launcher logs notice to stderr and skips update | Second jail starts immediately without delay. |
| **Stale Lock (Crashed Jail)** | Lock directory age > 600 seconds | Stale directory removed; lock acquired; update proceeds | Self-healing without human intervention. |
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
* **Verdict**: **Not yet ruled — newly surfaced 2026-09-19.** Alternative C's objection
  ("don't reimplement npm") is about the PACKAGE MANAGER; this alternative keeps npm as the
  installer and moves only the RESOLVER, so the objection does not apply. The reason to
  prefer it is the precedent survey's finding that none of the plugin/package ecosystems
  ships a lockfile or a rollback, so delegating the version choice to `pi update --extensions`
  gives up the pin — the exact seam a distributor is supposed to occupy. This reframes
  [OQ-2](#OQ-2): the launcher may still call `pi update`, but only after YOLO has resolved and
  pinned, and it should be able to say what it resolved instead of asking a registry.

> **A third path, already available:** a pack can *declare* its Pi packages as a `config-overlay`
> on surface `pi/settings` with `managed.packages` (e.g. `npm:@quintinshaw/pi-dynamic-workflows`).
> That is the declaration half — it says what should be present without fetching anything — and it
> composes with D, which owns the fetch and the pin.

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

   ⚠ **The resolver half already exists; the materializer half is the unverified one.**
   `internal/packsrc` ships `addr.go`, `lock.go` and `store.go`, and `packs.lock.json` is real at
   `~/.config/yolo-jail/packs.lock.json` (`LoadLock`/`Save`, beside the user config) — so (c) is
   an extension of a shipping mechanism rather than a new one, which is most of why it is the
   right answer. **`pi update --extensions` is CLAIMED, not verified**: nothing in this tree
   invokes that flag, and AGENTS.md forbids probing an agent CLI beyond `--version`, so slice one
   of the build is confirming the flag exists and is non-interactive before anything depends on
   it. If it does not, (b) — a Go entrypoint subcommand — is the fallback materializer and the
   ruling's resolve-and-pin half is unaffected.

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

---

## 7. Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| **OQ-1** | **Machine-scoped storage.** Extensions are shared tool capabilities like global binaries; per-workspace copies waste disk and, load-bearingly, create cross-jail version drift | 2026-09-20 | [§6](#6-open-questions) | no |
| **OQ-2** | **Option (c).** YOLO resolves and PINS through `internal/packsrc` + `packs.lock.json`; the launcher only materializes, under a non-blocking lock. Pi keeps the package-manager half; the VERSION CHOICE moves to YOLO, the seam a distributor must own since no ecosystem here ships a lockfile or rollback. ⚠ The resolver ships; the materializer's `pi update --extensions` flag is unverified | 2026-09-20 | [§6](#6-open-questions), [Alternative D](#alternative-d-resolve-and-pin-through-yolos-existing-pack-source-store) | no |
| **OQ-3** | **Leave Pi's in-app notification untouched.** The pre-launch update runs before the TUI starts, so the check passes cleanly in the normal path. ⚠ Conditional on [OQ-2](#OQ-2)'s pin: a correctly pinned older extension WILL trigger the warning, and it will be right — yolo does not suppress a vendor's honest notice about a version yolo chose | 2026-09-20 | [§6](#6-open-questions) | no |
