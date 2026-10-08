---
title: "VM-local volumes — implementation sketch"
date: 2026-10-08
status: draft
stage: SKETCH
next: "Wait for OQ-VL1 to OQ-VL4, then complete this against the tree with the implementation-plan skill"
depends-on: [vm-local-volumes.md#OQ-VL1, vm-local-volumes.md#OQ-VL2, vm-local-volumes.md#OQ-VL3, vm-local-volumes.md#OQ-VL4]
tags: [plan, macos, apple-container, podman-machine, per-side, volumes]
---

# VM-local volumes — implementation sketch

**Status:** 2026-10-08 — incomplete, and unstable while questions are open. Not a hand-off: do not
build from it. [vm-local-volumes.md](vm-local-volumes.md) is the design and wins on behavior.

Parked notes, read at `337086f64`:

- **The tool disk's code is the template, and it is specific to `/mise`.** `internal/prune/misevolumes.go`
  hard-codes the `.mise` suffix, its parse regex and the shared-volume special case, and its listing
  drops every volume name it does not parse, so side disks would be invisible to prune and
  `yolo stores` until that listing learns a second suffix. Reusable in shape: the label scheme, the
  tri-state listing, never-forced removal, allocated-bytes sizing, and inspect-then-create
  (`toolDiskExists`, `toolDiskFailure` in `internal/cli/run/actooldisk.go`, which hard-code
  `container volume …`). The tool disk's workspace-gone test is weaker than the design's
  VL-D5 (a plain missing path), so it is not reused as is.
- **The mount** replaces `venvShadowMountArgs`'s bind (`internal/cli/run/mounts.go`) per path on the
  two VM backends; that function today has no backend branch and no seal gate.
- **Podman Machine** has no labelled-volume code yet; `podmanBaseMounts` mounts one machine-wide
  `/mise` volume. Side disks there need `podman volume create --label` and a podman listing for prune.
- **`yolo stores`** rows live in `internal/cli/stores/tooldisks.go`.
- **Check before relying on it:** that `container run` accepts a named volume whose destination is
  inside a virtiofs mount (the design's P3), and how many volumes one VM takes.
- **`~/.cache`** is blocked on [OQ-VL2](vm-local-volumes.md#OQ-VL2); if ruled A, the cache bind is
  `cacheSource()` in `internal/cli/run/assemble_parts.go`.
- **Also touches:** the PodmanMac and AppleContainer cells in `internal/setupcensus/configkeys.go`,
  the `per_side_paths` text in `internal/cli/config_ref.txt` (it names `venv-shadows`), the
  persistence map's comment, the `--dry-run` argv, and the integration harness's
  `forceRemoveContainer`, which must remove side disks by workspace label.
- **Capture jails:** skip side disks when `captureJail()` is true (`seal.go`), not only under the seal.
