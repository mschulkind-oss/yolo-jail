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

**Status:** SKETCH, 2026-09-17 — incomplete, and unstable while questions are open.

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

Blocked on [`OQ-2`](pi-extension-lifecycle.md#OQ-2).

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
