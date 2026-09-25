---
title: "Implementation Sketch: Pi Extension Lifecycle"
date: 2026-09-17
status: draft
tags: [pi, extensions, plan, sketch]
summary: "Companion implementation sketch for Pi extension lifecycle management across YOLO jails."
vantage:
  status-chip: true
---

# Implementation Sketch: Pi Extension Lifecycle

**Status:** SKETCH, 2026-09-17, and **superseded by the build**. §1–§3 below shipped, none of them exactly as sketched. §1–§2 shipped 2026-09-21 as the `shared_directory` hook. §3 shipped 2026-09-25 as a pack-declared `refresh`, not a `pi`-named branch in the template. The design's as-built notes ([§3.1](pi-extension-lifecycle.md#31-storage-tier-decoupling-packages-from-session-state), [§3.2](pi-extension-lifecycle.md#32-execution-tier-pre-launch-auto-refresh), [§3.3](pi-extension-lifecycle.md#33-concurrency-tier-cross-jail-mutual-exclusion)) are the record. This note makes no claim about §4, the macOS parity check.

> [!NOTE]
> This is a companion sketch to [`pi-extension-lifecycle.md`](pi-extension-lifecycle.md).
> The design document wins on all behavioral decisions and architecture.

---

## 1. Pack Manifest Additions (`packs/pi/pack.json`)

Blocked on [`OQ-1`](pi-extension-lifecycle.md#OQ-1).

To implement machine-scoped storage for extensions:
1. Declare machine-scoped state directory:
   ```json
   {
     "at": ".pi-shared-npm",
     "because": "npm extension storage shared across workspaces",
     "kind": "state",
     "scope": "machine"
   }
   ```
2. Declare symlink hook:
   ```json
   {
     "at": ".pi-shared-npm",
     "from": ".pi/agent/npm",
     "hook": "shared_extension_storage",
     "kind": "hook"
   }
   ```

## 2. Hook Implementation (`internal/entrypoint/packhooks.go`)

Blocked on [`OQ-1`](pi-extension-lifecycle.md#OQ-1).

* Add `HookSharedExtensionStorage = "shared_extension_storage"` to `RunPackHooks`.
* Adapt `linkThroughShared` (`internal/entrypoint/sharedlink.go`):
  * Check if `~/.pi/agent/npm` is a real directory and `~/.pi-shared-npm` is empty.
  * If so, move existing directory contents into shared storage to migrate existing single-workspace installations.
  * Replace `~/.pi/agent/npm` with a symlink pointing to `../../.pi-shared-npm`.

## 3. Launcher Template Extension (`internal/entrypoint/shims.go`)

✅ **Built 2026-09-25, in a different shape.** The sketch below keys the refresh on binary `pi` inside the shared npm template, which would teach core what an agent is. As built, the pack declares `"refresh": {"argv": ["update", "--extensions"], "lock": ".pi-shared-npm/.yolo-update.lock"}` on its `program`, and `internal/entrypoint/prelaunchrefresh.go` renders any declared refresh into BOTH launcher templates. It also differs from the sketch in a three-way lock result, an owner token, a heartbeat that keeps a held lock from going stale, the resolved-Node prefix, and stdin from `/dev/null`. What the lock does not cover, Pi's own unlocked install of a package the refresh did not reach, is the design's open [OQ-4](pi-extension-lifecycle.md#OQ-4). The sketch is kept as written.

In `npmAgentLauncher` / `npmLauncherTemplate` for binary `pi`:
* Add shell helper function `_refresh_pi_extensions`:
  ```bash
  _refresh_pi_extensions() {
      [ "$UPDATES_ENABLED" = "1" ] || return 0
      local stamp="$STAMP_DIR/pi-extensions.stamp"
      local lock_dir="$HOME/.pi-shared-npm/.yolo-update.lock"
      if [ -f "$stamp" ] && [ "$(( $(date +%s) - $(_stamp_mtime "$stamp") ))" -le 3600 ]; then
          return 0
      fi
      if ! _take_extension_lock "$lock_dir"; then
          echo "  pi: another extension update is in progress — running installed extensions." >&2
          return 0
      fi
      echo "  Checking for Pi extension updates..." >&2
      YOLO_BYPASS_SHIMS=1 timeout 60 "$REAL_BIN" update --extensions >&2 || true
      touch "$stamp"
      rmdir "$lock_dir" 2>/dev/null || true
  }
  ```
* Invoke `_refresh_pi_extensions` right before `exec "$REAL_BIN"`.

## 4. Darwin / macOS User Parity (`internal/entrypoint/darwinhomelayout.go`)

* Verify that `.pi-shared-npm` in the sandbox account home (`/Users/_yolojail`) is mirrored into the workspace sidecar (`<workspace>/.yolo/home/.pi-shared-npm` -> `/Users/_yolojail/.pi-shared-npm`) so the relative symlink from `<sidecar>/pi/agent/npm` resolves without dangling.
