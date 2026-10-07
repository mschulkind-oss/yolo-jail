---
title: "One configured home for jail tools — without moving the host's mise"
status: in-review
stage: DESIGN
next: "Rule the store-setting, path-identity, backend, cutover and dedup questions before implementation"
tags: [design, storage, mise, relocation]
summary: "Design for selecting one trusted host directory as the shared Linux jail mise/Rust/Cargo store, while preserving /mise and leaving host mise untouched."
---

# One configured home for jail tools — without moving the host's mise

**Status:** 2026-10-06. Nothing built. Source checked against `d5bc7a418`.

> **In short.** The jail tool store is one shared writable tree, not the host's mise data. Let trusted user configuration select that tree on the supported host, keep its guest name `/mise`, and make launch, inventory and maintenance agree on exactly one selected root.

**Why it matters.** The current Linux store is tied to the filesystem holding yolo's state directory; moving it safely by hand requires stopping every writer and coordinating all store readers.

**The shape.** A user-scope store resolver feeds one host-side path to initialization, mounts, inventory and reclaim; in-jail and backend-specific stores stay separate.

**Cost.** An external path is outside the machine-state tree, so inventory and dedup can no longer discover it by scanning that tree. Selection does not move existing data.

**Start at [§3](#3-one-selected-store-everywhere)** — the invariant all consumers must share.

**Needs your ruling:** [OQ-STR1](#OQ-STR1), [OQ-STR2](#OQ-STR2), [OQ-STR3](#OQ-STR3), [OQ-STR4](#OQ-STR4), [OQ-STR5](#OQ-STR5), [OQ-STR6](#OQ-STR6).

**Reads with:** [`../plans/shared-tool-store-relocation.md`](../plans/shared-tool-store-relocation.md) (source map and future test sketch), [`../reference/storage-and-config.md`](../reference/storage-and-config.md) (scope and current path contracts), [`../plans/cache-relocation.md`](../plans/cache-relocation.md) (the distinct cache relocation and its held abstraction question).

---

## 1. The request and its boundary

The proposed setting is for **the jail's shared mise data tree**, including mise installs and the Rust state addressed by `RUSTUP_HOME=/mise/rustup` and `CARGO_HOME=/mise/cargo`. It is not the host's `~/.local/share/mise`, the user's host `mise` executable or config, the shared download cache, Podman storage, or the full `~/.local/share/yolo-jail` tree. The host's mise data path is separately named by `storage.HostMiseDir`; it is used by the legacy-layout check, not as the jail store.

This request has five settled constraints:

- The setting is trusted **user scope**. A workspace, workspace-local, per-workspace, pack, or in-jail config cannot grant a jail an arbitrary host path.
- No setting preserves the current default. A valid explicit value chooses one store; there is no automatic merge, import, copy, adoption, or fallback to the old default.
- The guest mount and environment remain `/mise`, `MISE_DATA_DIR=/mise`, `RUSTUP_HOME=/mise/rustup`, and `CARGO_HOME=/mise/cargo`.
- A nested jail keeps using its outer jail's `/mise`; copied host configuration must not redirect it to a host path.
- A configured store that is absent or unsafe cannot silently turn into the old default, a temporary store, or another filesystem path.

The initial target is the **Linux rootless-Podman** setup. Other backend cells need an explicit support/refusal contract; they cannot silently accept a setting they do not use.

## 2. What the tree does today

| Concern | Current source behavior |
| :--- | :--- |
| Default location | `paths.GlobalMise()` is `<GlobalStorage>/mise`, where `GlobalStorage` is `~/.local/share/yolo-jail`. `storage.EnsureGlobalStorage` creates it before config loads. [`paths.go`](../../internal/paths/paths.go), [`ensure.go`](../../internal/storage/ensure.go) |
| Linux container source | The host launch sets `assembleInput.miseStore` from `jailMiseStoreDir`; outside a jail that is `paths.GlobalMise()`. Podman binds that source at `/mise`. [`run.go`](../../internal/cli/run/run.go), [`storagehelpers.go`](../../internal/cli/run/storagehelpers.go), [`assemble_parts.go`](../../internal/cli/run/assemble_parts.go) |
| Nested launch | `jailMiseStoreDir(true)` returns `/mise`; no host path is derived for the nested source. [`storagehelpers.go`](../../internal/cli/run/storagehelpers.go) |
| Other backends | Podman on macOS uses a machine-wide named volume; Apple Container uses a per-workspace tool disk; macos-user puts its machine-wide mise data in the sandbox account home. These are separate contracts, not aliases for the Linux host directory. [`assemble_parts.go`](../../internal/cli/run/assemble_parts.go), [`macosuser.go`](../../internal/macosuser/macosuser.go), [`macos-user provisioning rulings`](../reference/macos-user-provisioning.md), [`macos-backend-performance.md`](../research/macos-backend-performance.md) |
| Guest environment and provisioning | Container launches set `MISE_DATA_DIR=/mise`, `RUSTUP_HOME=/mise/rustup`, and `CARGO_HOME=/mise/cargo`; macos-user explicitly sets its own `MISE_DATA_DIR`. [`assemble.go`](../../internal/cli/run/assemble.go), [`macosuser.go`](../../internal/macosuser/macosuser.go), [`provision.go`](../../internal/provision/provision.go) |
| Use records and cleanup | Each jail writes `.yolo-use` records in the shared store. Host `PruneUnusedMiseVersions`, launch housekeeping, and `miseuse` join records to protect versions used by any workspace; the age/liveness and fail-closed rules are in [`miseversions.go`](../../internal/prune/miseversions.go) and [`miseuse.go`](../../internal/miseuse/miseuse.go). |
| Inventory and preflight | `yolo check` lists `paths.GlobalMise()`. `yolo stores` discovers state children under `GlobalStorage` and gives the `mise` row its version reclaimer. An external path would be invisible to that scan. [`check.go`](../../internal/cli/check/check.go), [`inventory.go`](../../internal/cli/stores/inventory.go) |
| Other maintenance | `WalkGlobalDedupable` scans only `globalStorage/{cache,mise,home}` and refuses a symlink at each root. `DiskUsageReport` also walks every child of `GlobalStorage`, so an old default can remain in physical storage totals even when it is not the active store. `MigrateStorageLayout`'s dangling-link heal scans `HostMiseDir`, not `GlobalMise`; it is not a jail-store import or migration. [`prune.go`](../../internal/prune/prune.go), [`report.go`](../../internal/prune/report.go), [`ensure.go`](../../internal/storage/ensure.go) |
| Similar cache name | `PurgeCacheByAge`'s `mise` entry is `GlobalCache()/mise` (download cache), not the shared tool tree at `GlobalMise()`. It remains governed by cache policy and `cache_relocations`, never by the new tool-store selector. [`cachepurge.go`](../../internal/prune/cachepurge.go) |

There is no general import/adopt path for the shared jail mise store. Existing `yolo prune` version removal is the only version-level reclaimer described by the store row; it is not equivalent to `mise prune`, which can miss versions used in other workspaces.

## 3. One selected store, everywhere

The design contract is **one resolved host store path per ordinary, supported host invocation**. One resolver reads the trusted setting and supplies its answer; consumers do not each reconstruct a path or independently decide whether the default applies. The nested-jail and sealed-build exceptions below remain ahead of that host selection.

- With no setting, preserve each backend's current store selection; on the current Linux host path, this remains `paths.GlobalMise()`.
- On an ordinary host invocation in the supported Linux rootless-Podman cell, a valid setting selects one directory. Initialization, the `/mise` source, `yolo check`, `yolo stores`, version-use recording, launch housekeeping, `yolo prune`, and any global hardlink-dedup pass that includes the tool store all receive that same path.
- A nested invocation does not resolve or validate the host target. Its effective store is `/mise` from the containing jail, even when copied host configuration names another location.
- A sealed build keeps its private `/mise` store; the machine-wide setting does not make that store shared.
- `PurgeCacheByAge`'s `mise` name is in the separate `GlobalCache` tree, not the shared data tree. Preserve its existing cache-age behavior and do not route it through the tool-store setting.
- A selected external store is inventoried as a distinct location, not hidden because it lies outside `GlobalStorage`. Its size must not be reported as bytes reclaimed from the default state filesystem. This follows the separate-filesystem accounting used for relocated caches in [`cache-relocation.md`](../plans/cache-relocation.md).
- Version pruning, use recording and hardlink dedup operate on the **selected** store only. The old default is not a second active store, a fallback, a version-prune target, or an implicit cleanup target. Legacy bytes there remain untouched unless the user explicitly selects it again. Generic physical-size accounting may still show those bytes under machine storage; the inventory must label them as inactive legacy data, not as the selected tool store.

`EnsureGlobalStorage` currently creates the default `mise` child before configuration is available. The implementation must arrange selection before provisioning a tool-store root, while leaving the rest of machine-storage initialization and its migration ordering intact. In particular, `yolo check` and launch cannot create or switch to the default mise directory before discovering a configured target.

## 4. Config and cutover boundary

The config source must be read from the host user's fixed config path, not from the merged map. [`storage-and-config.md`](../reference/storage-and-config.md#the-config-scopes) and [`cache-relocation.md`](../plans/cache-relocation.md#threat-model-why-user-scope-is-the-whole-design) establish why: the workspace and assembled config are jail-writable, while install-shaped host paths require the trusted user-scope file. Validation should name a workspace-scoped attempt and the supported file where the owner may place the setting. A nested invocation validates shape only if needed and never stats or mounts the host target.

Configuration selection is not migration. When the path changes, existing running jails retain the store source with which they started; new launches receive the newly selected store. Copying or selecting an empty directory does not import installed tools, Rust state, or use records from the old location. Any manual transfer must stop writers, copy and verify the full tree (including hard links, ownership and `.yolo-use`), switch the setting, verify a fresh jail, and remove the old copy only as a separate human act. Updating yolo alone does none of those things.

Whether yolo enforces a no-active-jail cutover, as opposed to relying on the explicit stop/copy/switch procedure, remains open in [OQ-STR5](#OQ-STR5). This matters because different running jails could otherwise keep writing the former tree while new launches use the selected one.

## 5. Path and safety semantics

The path is trusted user input but is also a writable host directory mounted into a jail. The selector must reject malformed values with a message that names the setting and next step; no validation failure can fall back to the default. Space-containing paths must remain valid argv values. The host's own mise store must remain disjoint from the selected jail store.

| Path/source condition | Existing authority and proposed adaptation |
| :--- | :--- |
| Configuration source | Read the fixed user-scope config directly; do not grant authority through workspace or assembled config. `config.LoadCacheRelocations` is the direct-read precedent, and its workspace-attempt validation is defense in depth, not the trust boundary. [`relocations.go`](../../internal/config/relocations.go) |
| Expansion and shape | Follow `config.expandUser`'s established leading `~` / `~/...` expansion, then require an absolute path and clean it. `~user` is not expanded and therefore fails the absolute-path check. Empty/non-string values must be refused, not treated as unset. [`envsources.go`](../../internal/config/envsources.go), [`relocations.go`](../../internal/config/relocations.go) |
| Mount syntax | Reject `:` in a Linux Podman source; it is parsed as a mount separator/options rather than a literal path character. [`relocations.go`](../../internal/config/relocations.go) |
| Credential/yolo boundaries | For a user-selected read-write source, carry forward the resolved-path refusal set in `config.rwMountRefusal` / `paths.WritableSourceScopeBreach`: refuse a source that is or contains `$HOME`, either yolo boundary (`~/.config/yolo-jail`, `~/.local/share/yolo-jail`), or lies inside either yolo directory; refuse either-direction overlap with the workspace. This is the context-mount authority, not a new list of secret subdirectories: it deliberately does not name-block `~/.ssh` or similar paths. [`context-mounts.md` §2.3](context-mounts.md#23-refusal-set), [`mounts.go`](../../internal/config/mounts.go), [`workspacescope.go`](../../internal/paths/workspacescope.go) |
| Existing yolo-managed default | `GlobalMise()` is inside `GlobalStorage()`, so applying the generic yolo-state containment refusal to the existing implicit default would break the current source. Preserve the unchanged, unset-setting default as its existing shaped exception; do not infer that arbitrary configured paths under state are allowed. Whether an explicit value equal to `GlobalMise()` may use the exception, or must be refused, is an unresolved adaptation under [OQ-STR1](#OQ-STR1). [`paths.go`](../../internal/paths/paths.go) |

The unsafe mountpoint case is not limited to the root filesystem. If HOME/default storage is on H, the underlying directory at an absent removable-drive mountpoint may be on R, with the intended volume D; comparing `target` to `GlobalMise()` sees R ≠ H and accepts an offline target. A missing nested mount under any other filesystem has the same failure. A differing device is only evidence of availability when a separate topology rule guarantees the comparison is sound; an expected mounted-filesystem identity is the recommended alternative, with its representation and exact check target still unruled. See [OQ-STR2](#OQ-STR2).

`yolo check`, launch, inventory and maintenance must distinguish a real selected store from an unavailable, non-directory, inaccessible, or unsafe target; no failure may provision elsewhere. No consumer may follow an agent-created link inside the store to delete, inventory, or hardlink an arbitrary host tree. Existing per-file pruning walks beneath an `os.Root`, and global dedup refuses a symlinked root; preserve those protections when the store root moves. Whether a user-configured root symlink is resolved to a canonical path or rejected is [OQ-STR3](#OQ-STR3), not licensed by the cache relocation precedent.

## 6. Open questions

1. 💬 **OQ-STR1: Is the new setting one dedicated store path, or part of the cache-relocation abstraction?**

   The requested application names one whole shared store. `cache_relocations` is a per-cache-subdirectory map, and its own abstraction/host-reflection questions remain open in [`cache-relocation.md`](../plans/cache-relocation.md#OQ-CR1). The store already has independent init, environment, records, inventory and pruning consumers.

   Rule whether an explicit value equal to `GlobalMise()` gets this exception or is refused; do not generalize the exception to other user-selected paths inside yolo state.

   - **A — A dedicated scalar user-scope key** for the jail mise/Rust/Cargo store.
   - **B — Extend the cache-relocation map** to cover the mise store as a special target.

   <!-- vantage: question id=OQ-STR1 leaning="A — a dedicated scalar for one whole store keeps its lifecycle distinct from cache subdirectories; reuse the collection abstraction only if the owner wants one general storage policy. Keep the implicit GlobalMise default separate and rule whether explicitly selecting that state path is allowed." -->

   _Leaning:_ A. Preserve the requested application-config path while keeping whole-store lifecycle separate from the cache-specific map.

   **Answer:**

   > _(empty — awaiting owner ruling)_

2. 💬 **OQ-STR2: How must the resolver prove an external drive is present rather than writing to its mountpoint on another local filesystem?**

   An absent drive may leave its mountpoint directory on another filesystem. A comparison with `GlobalMise()` is only sound under a separate topology guarantee; the separate-HOME counterexample is in [§5](#5-path-and-safety-semantics).

   - **A — Compare target and default filesystem identities**, but only with that topology guarantee.
   - **B — Require a matching expected mounted-filesystem identity**; its representation and exact check target remain unruled.
   - **C — Require an existing directory only**, without distinguishing an unmounted volume from its underlying directory.

   <!-- vantage: question id=OQ-STR2 leaning="B — checking the expected mounted filesystem identity addresses separate-/home and root-filesystem counterexamples; the identity representation and config shape still need an owner ruling." -->

   _Leaning:_ B. A device-difference check against `GlobalMise()` is only a conditional topology heuristic, not proof the requested volume is mounted. Prefer an expected filesystem identity, while leaving its representation, identity source and exact target/parent rule open.

   **Answer:**

   > _(empty — awaiting owner ruling)_

3. 💬 **OQ-STR3: Does the setting accept symlinks in the configured store root?**

   `WalkGlobalDedupable` refuses a symlinked root; initialization and ordinary stat calls follow paths. Having those consumers disagree would make a successful mount unsafe for maintenance.

   - **A — Resolve to a canonical real directory once, pass that resolved path to every consumer, and refuse unresolved links.**
   - **B — Refuse any symlink component in the configured root.**
   - **C — Preserve the configured spelling and make each consumer follow it.**

   <!-- vantage: question id=OQ-STR3 leaning="A — resolve once and use the same concrete directory for launch and maintenance; this is compatible with a user-chosen symlink while keeping the dedup root non-symlinked. Refuse any path that cannot be resolved safely." -->

   _Leaning:_ A. One canonical host path prevents consumers from treating a link as safe for mounting but invisible to dedup.

   **Answer:**

   > _(empty — awaiting owner ruling)_

4. 💬 **OQ-STR4: Which ordinary host launch cells can honor an explicit store path?**

   This applies only to ordinary host invocations. Nested launches always use the outer `/mise`, and sealed builds keep their private store; neither exception is subject to host-setting refusal. Which other ordinary host cells are supported remains open.

   - **A — Support ordinary Linux rootless Podman host invocations only; refuse an explicit setting on other ordinary host backend/mode cells.**
   - **B — Also support ordinary Linux rootful Podman host invocations, but refuse the VM and native ordinary host cells.**
   - **C — Warn and ignore the setting wherever the ordinary host backend cannot use its host path.**

   <!-- vantage: question id=OQ-STR4 leaning="A — the initial scope is ordinary rootless Linux Podman host launches; preserve the existing nested /mise and sealed/private-store exceptions, and fail rather than imply an unsupported ordinary host backend used the configured path." -->

   _Leaning:_ A. Keep the first delivery narrow for ordinary host launches, preserving nested and sealed/private behavior rather than treating those established modes as unsupported setting errors.

   **Answer:**

   > _(empty — awaiting owner ruling)_

5. 💬 **OQ-STR5: Must yolo refuse a store-path cutover while a running jail still has the previous store mounted?**

   Running containers retain their original `/mise` source after the user config changes. A new launch can otherwise create two shared stores on one host, while the old default or path is still writable by live jails.

   - **A — Refuse the cutover/new fresh launch until jails using the prior source stop; keep attach behavior explicit too.**
   - **B — Make stopping all writers a documented operator requirement, with no runtime detection.**

   <!-- vantage: question id=OQ-STR5 leaning="A — prevent split-brain while old jails can still write the previous path; name the jails and the stop/relaunch next step. A config edit must not pretend to move a live mount." -->

   _Leaning:_ A. A refusal is safer than allowing newly launched workspaces to diverge from still-running writers, though it needs a reliable active-mount check.

   **Answer:**

   > _(empty — awaiting owner ruling)_

6. 💬 **OQ-STR6: Should `yolo prune --dedup-global` include an external selected mise store?**

   Today the pass scans the `mise` child under `GlobalStorage`; its dry-run groups same-content files without checking that the source and destination are on the same filesystem, while an applied hardlink can fail across devices. A relocated store would otherwise vanish from the pass or produce misleading savings.

   - **A — Include only same-filesystem hardlink candidates and make dry-run accounting use the same eligibility check.**
   - **B — Exclude an externally selected store from global dedup; keep version-pruning as its only yolo reclaimer.**

   <!-- vantage: question id=OQ-STR6 leaning="B — omit cross-filesystem store dedup until the pass can report only linkable candidates; the version reclaimer already covers the store's bounded maintenance." -->

   _Leaning:_ B. A false reclaim estimate is worse than omitting an optional optimization; revisit with a same-filesystem eligibility test if dedup is useful on the target filesystem.

   **Answer:**

   > _(empty — awaiting owner ruling)_

## 7. Completion evidence

The implementation is ready for review only when an ordinary host invocation in the ruled Linux rootless-Podman cell gives a fresh jail the selected host target at `/mise`; the old default has no writes from that launch; and `yolo check`, `yolo stores`, version-use recording, launch housekeeping and `yolo prune` all name and act on that same selected path. The unset default stays unchanged; nested launch must continue using the outer `/mise`, and sealed builds keep their private store. Explicit invalid/unavailable paths must refuse before provisioning or launching. Tests must cover the separate-HOME/offline-mount identity counterexample, resolved credential/yolo-boundary and workspace-overlap rules, and the still-to-be-ruled explicit yolo-state case. Unsupported ordinary-host backend behavior, filesystem identity and symlink cases must be asserted, not inferred from successful argv rendering.

The design does not license claims about native macOS, Podman Machine, Apple Container or rootful Podman based on Linux tests.
