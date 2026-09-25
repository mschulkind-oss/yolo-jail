# Companion Implementation Sketch: Pi Git Extension Caching

**Status:** SKETCH, 2026-09-25 — incomplete, and unstable while questions are open.

> **Precedence Note:** This document is an implementation sketch holding codebase mapping,
> sequencing notes, and test plans. On all questions of behavior and architecture,
> [`pi-git-extension-caching.md`](pi-git-extension-caching.md) is normative.

## 1. File Changes Map

| File | Change | Rationale |
| :--- | :--- | :--- |
| `packs/pi/pack.json` | Add `.pi-shared-git` state (`scope: "machine"`) and `shared_directory` hook from `.pi/agent/git` | Directs Pi's user-scoped git extension checkouts to the machine-scoped storage tier. |
| `internal/entrypoint/shareddirhook_test.go` | Add integration test covering `.pi-shared-git` hook | Verifies symlinking, migration of legacy workspace checkouts, and idempotency. |
| `internal/entrypoint/prelaunchrefresh_test.go` | Verify lock interaction with git refresh | Ensures pre-launch refresh lock protects both npm and git updates across concurrent launches. |

## 2. Dependency Notes and Invariants

* Blocked on [OQ-1](pi-git-extension-caching.md#OQ-1): Confirms that `.pi-shared-npm/.yolo-update.lock` remains the refresh lock without needing changes to `packdecl.Refresh` or `prelaunchrefresh.go`.
* Blocked on [OQ-2](pi-git-extension-caching.md#OQ-2): Confirms non-blocking launch priority during active git rebuilds.
* Blocked on [OQ-3](pi-git-extension-caching.md#OQ-3): Confirms user-scope vs project-scope semantics for git extensions.

## 3. Migration Mechanics

1. **Empty Shared Store**:
   - The first jail to launch with `.pi-shared-git` configured runs `copyTreeIntoShared` (`internal/entrypoint/sharedlink.go`).
   - If the workspace has existing checkouts in `~/.pi/agent/git/`, they are copied to `.pi-shared-git` under the `.yolo-copy-incomplete` marker.
   - Once complete, `~/.pi/agent/git` is replaced with a symlink to `/home/agent/.pi-shared-git`.
2. **Populated Shared Store**:
   - Subsequent workspaces discover `.pi-shared-git` already populated.
   - Any local workspace duplicate is safely discarded, and `~/.pi/agent/git` is symlinked to `/home/agent/.pi-shared-git`.

## 4. Verification Plan

1. **Unit Tests**:
   - Run `go test -short ./internal/entrypoint/...` to ensure `sharedlink.go` and `prelaunchrefresh.go` pass with the new hook.
2. **Nested Jail Verification**:
   - Launch a nested jail with Pi configured:
     ```bash
     YOLO_REPO_ROOT=/workspace /workspace/dist-go/linux-$(go env GOARCH)/yolo -- bash
     ```
   - Verify `~/.pi/agent/git` is a symlink pointing to `/home/agent/.pi-shared-git`.
   - Install a git extension (`pi install git:github.com/mschulkind/pi-archimedes`).
   - Verify files are written to host machine storage at `~/.local/share/yolo-jail/home/.pi-shared-git/`.
   - Start a second nested jail in a different throwaway workspace:
     Verify `pi-archimedes` is immediately present without running `git clone`.
