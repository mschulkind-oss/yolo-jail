---
title: "Plan sketch: select one shared jail tool store"
status: draft
stage: SKETCH
next: "Resolve the linked design questions, then promote this sketch against the implementation tree"
depends-on:
  - ../design/shared-tool-store-relocation.md#OQ-STR1
  - ../design/shared-tool-store-relocation.md#OQ-STR2
  - ../design/shared-tool-store-relocation.md#OQ-STR3
  - ../design/shared-tool-store-relocation.md#OQ-STR4
  - ../design/shared-tool-store-relocation.md#OQ-STR5
  - ../design/shared-tool-store-relocation.md#OQ-STR6
tags: [plan, storage, mise]
summary: "Source map and future verification gates for user-configured placement of the shared jail mise/Rust/Cargo store."
---

# Plan sketch: select one shared jail tool store

**Status:** 2026-10-06 — incomplete and unstable while the design questions are open. Written against `d5bc7a418`.
**Design:** [`../design/shared-tool-store-relocation.md`](../design/shared-tool-store-relocation.md).
**Precedence:** design wins on behavior; the tree wins on source facts; this sketch is advice and the first thing to be wrong.

## Source map

| Component | Future seam |
| :--- | :--- |
| `internal/paths/paths.go` | Keep default `GlobalMise` derivation authoritative; add no second path derivation. |
| `internal/config/` | Direct user-scope read, validation, no workspace/assembled-config authority, and nested-jail short circuit. Follow the direct-read pattern in `config/relocations.go`; do not inherit its cache-specific path rules without a ruling. |
| `internal/storage/ensure.go` | Provision only the selected jail store after selection; preserve `EnsureGlobalStorage`'s other paths and keep `HostMiseDir` legacy handling separate. |
| `internal/cli/run/run.go`, `storagehelpers.go`, `assemble.go`, `assemble_parts.go` | Resolve one host source, retain nested `/mise`, sealed private stores and backend-specific existing store sources. |
| `internal/cli/check/check.go` | Validate and report the selected store rather than always reporting `GlobalMise`. |
| `internal/cli/run/housekeeping.go`, `miseuserecording.go` | Record use and run the launch offer against the same selected store. Verify exact consumer names against the tree before promotion. |
| `internal/prune/prunecmd.go`, `miseversions.go`, `prune.go`, `report.go` | Inject the selected path into version pruning and apply the ruled external-root behavior for global dedup. Preserve `miseuse` liveness, age and fail-closed semantics. Keep generic physical-size totals honest about an inactive default left on disk. |
| `internal/prune/cachepurge.go` | Preserve `GlobalCache()/mise` age-purge behavior; it is not the `/mise` tool-data store. |
| `internal/cli/stores/inventory.go` | Add an explicit selected-store row outside the state-child scan when configured externally; label a remaining default child as inactive legacy data and avoid counting active bytes twice. |
| `docs/reference/storage-and-config.md`, `userguide/guides/storage.md`, `userguide/reference/settings-per-setup.md`, config-ref source | Document the setting, default, scope, supported backend cells, failure behavior and explicit migration instructions after decisions settle. |

## Reuse and traps

- `config.LoadCacheRelocations` and `validateCacheRelocations` show how a host-path key is read directly from user scope and kept inert in-jail; the key's cache semantics do not automatically fit a whole tool store.
- `jailMiseStoreDir` is the container launch split: nested `/mise` versus host `GlobalMise`; sealed builds override it later with their private store.
- `prune.FindUnusedMiseVersions` and `miseuse.ReadAll` are the current shared-store maintenance contract. Use records live under the root they describe, so the chosen root must be passed consistently to both producer and reader.
- `EnsureGlobalStorage` currently creates `GlobalMise` before configuration is loaded. A configured target cannot be safe while that early creation remains the only initialization path.
- `WalkGlobalDedupable` enumerates the default `mise` child and refuses symlinked roots; `HardlinkDuplicateFiles` can estimate same-content files across devices before a link fails. Do not add the new root until [OQ-STR6](../design/shared-tool-store-relocation.md#OQ-STR6) is ruled and the dry-run estimate remains truthful.
- `DiskUsageReport` physically sizes all children under `GlobalStorage`, including an old default that is no longer selected. Keep that distinct from active-store version pruning; do not hide or reclassify those bytes as the external store.
- `PurgeCacheByAge`'s `mise` subdir is under the separate `GlobalCache`, not `GlobalMise`. The new selector must not change cache purge routing.
- `storage.MigrateStorageLayout` scans `HostMiseDir`, not the jail store. Do not point it at the configured jail path or turn layout healing into an implicit migration.

## Build gates after rulings

1. Unit-test the user-scope resolver first: default, explicit path, workspace-only attempt, leading `~` expansion and absolute-path enforcement, colon refusal, non-string/empty malformed values, resolved credential/yolo-boundary and workspace-overlap refusals, non-directory, inaccessible/unavailable filesystem, and nested invocation with a copied host config. Keep the unset `GlobalMise()` default's existing shaped exception distinct from whether an explicit path within yolo state is allowed; rule the latter before implementation.
2. Unit-test filesystem availability with HOME on filesystem H, an absent-drive mountpoint resolving to R, and an expected filesystem identity D. Prove that comparing only with the default (`R != H`) falsely accepts the target, while the ruled expected-identity check rejects it. Test a matching mounted identity separately; a real removable-drive check belongs only on an instrumented host that can actually mount/unmount it.
3. Unit-test initialization and host launch source together: only the selected target is initialized/mounted; no configured-path failure writes to `GlobalMise`; path spaces survive argv; ordinary supported host invocations select the ruled path; nested remains `/mise`; sealed and non-target backends preserve their existing source.
4. Add consumer regressions for `yolo check`, `yolo stores`, `.yolo-use` record writing/reading, launch housekeeping, version-prune dry-run/apply selection, and the ruled dedup behavior. Seed the old default with sentinels and prove no selected-store operation attributes its versions as active, prunes it or deduplicates it. Assert that `yolo stores` labels it inactive while generic physical-size accounting may still include its bytes.
5. Add a Linux rootless-Podman integration test using the isolated HOME/user-config helpers in `integration/harness_test.go` and `packs_test.go`. A fresh jail must read a target seed, write to the target at `/mise`, keep the default unchanged, and make `yolo stores`/`prune` refer to the same store. Use a CPU-only fixture under the selected store; do not rely on a network service or interactive agent.
6. Add explicit tests for every ordinary host backend/mode refusal or supported alternative the ruling retains, while preserving the nested `/mise` and sealed/private-store exceptions. Linux or nested success is not evidence for Podman Machine, Apple Container, macos-user, rootful Podman, or a real removable drive.
7. Update the reference/config/user-guide surfaces in the source map, run targeted unit tests and the new integration test, then run the documentation checks. The landing owner runs whole-tree gates.

- Do not read or modify the host's own mise installation; do not use `HostMiseDir` as the jail store.
- Do not merge, copy, adopt, version-prune or deduplicate the former default when an alternate path is active. Generic physical-size accounting may still report its bytes as inactive storage.
- Do not let workspace or assembled config choose a writable host source.
- Do not present this sketch as an implementation hand-off until all linked design questions are ruled and every call site is rechecked against the tree.
