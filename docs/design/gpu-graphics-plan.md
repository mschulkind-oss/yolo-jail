---
title: "Build the selected graphics-driver tree"
status: accepted
stage: BUILT
next: "Both source lanes are built; a real Linux GPU host owes the acceptance its External limit names"
depends-on:
  - gpu-graphics.md
---

# Plan: build the selected graphics-driver tree

**Status:** Completed against `e02bb526bc5b4d59fa5ba7293163cdee44f3ff78`, 2026-10-28. The [design](gpu-graphics.md) wins on behavior. This plan records the settled shared contract and tree map; integration owns the combined candidate and hardware-limited acceptance. Both lanes are built (commit `6dda57495` and its follow-ups). MEASURED: unit, fixture and integration tests. UNMEASURED: a real GPU host, per the External limit below.

## Map and ownership

| Owner | Path | Change |
| :--- | :--- | :--- |
| A — metadata and boot | `flake.nix` | Emit resolved graphics-driver manifest beside baked image metadata and selected package profiles; validate actual outputs. |
| A | `internal/entrypoint/graphics.go` (new), `graphics_test.go` (new) | Parse selected manifest, preserve existing destination trees, create only absent `lib`/`share` links, write runtime provenance, reject invalid selected records without partial creation. |
| A | `internal/entrypoint/bootsteps.go`, `bootsteps_test.go` | Add container-only step after `generate_store_packages`, before loader-cache/application bootstrap; declare macos-user exclusion and pin order. Container entrypoint includes Apple Container's Linux guest. |
| A | `internal/cli/run/assemble.go`, GPU-contract test (new) | Pass explicit `YOLO_GPU_ENABLED=1|0` from requested config, independent of host device availability; disable inherited enablement. |
| B — diagnostics | `internal/cli/check/check.go`, `internal/cli/check/sections_devices.go` if needed; graphics diagnostics/test files (new) | Connect the enabled-Linux check to resolved package/version and guest exposure; separate in-jail provenance from host readiness. |

**Wire contract:** baked metadata is `/etc/yolo-jail/graphics-drivers.json`; each store package profile carries `share/yolo-jail/graphics-drivers.json`. Each record has resolved `name`, `version`, `arch: "64"|"32"`, and actual validated `lib`/`share` output paths, which can have different store roots. Preserve package order; first selected record per architecture wins. Store delivery reads only its selected profile metadata, never stale baked metadata. Runtime provenance records per-architecture `preserved: true|false`; it names selected outputs but does not prove a preserved tree points to them. Boot selects no driver when disabled or when no record exists. B's JSON decoder does not depend on a new A API.

A and B own only their listed files and may work concurrently. Commit each lane before the integration custodian combines them. The custodian owns `integration/gpugraphics_test.go`, any completion-census artifact, the user-guide pinning note after the source contract is live, and the single combined runtime/platform gate. No lane edits the other's caller or tests.

## Reuse and traps

- `bootSteps` is shared by container and macos-user; `notDarwin` must state why this `/run` step is container-only. The graphics action is also marked `notSessionPass`: jail-own boot creates the tree and an attach preserves the running jail's provenance. `bootsteps_test.go` pins both boot orders. Apple Container uses the container entrypoint and gets the tree from requested config even though it cannot pass through GPU devices; this is not hardware evidence.
- `generate_store_packages` materializes `YOLO_STORE_PROFILES`; `generate_ld_cache` follows it. Provision graphics links between those boot actions and loader-cache/application bootstrap, not on the host or at image build time.
- `assemble.go` already distinguishes requested GPU config from host-confirmed `gpuEnabled`; the new boot signal intentionally follows the requested setting, not the latter.
- A preexisting tree is wholly user-supplied even if incomplete or dangling. Check its root without following links; do not repair, roll back, or replace any child.
- A current source evaluation identifies `mesa` `out` plus `pkgsi686Linux.mesa` `out`; re-evaluate after nixpkgs changes. Do not call 26.2.5 stable. NVIDIA's `linuxPackages.nvidia_x11` attribute is known, but its actual graphics `lib`/`share` layout is not: A must not emit or promise an NVIDIA record until it validates those outputs. See the [research limits](../research/gpu-graphics.md#fast-moving--verify-before-building).
- Existing pin discovery is in [`packages-and-tools.md`](../../userguide/guides/packages-and-tools.md#nix-packages-image-level); do not add a key. The integration custodian adds the graphics-specific user-guide pointer only after the source contract is live.

## Tests and gates

A's boot tests cover resolved 64-bit paths, distinct lib/share roots, optional 32-bit metadata, first-record selection, preexisting/incomplete/dangling trees, disabled/no-driver no-op, invalid metadata/output fatality, and no partial tree. Launch call-site regressions verify explicit true/false env values when host GPU probing declines, including the Apple Container runtime where no device is passed through.

B's actual `Check` call-site tests cover resolved name/version, present and absent paths, no driver, malformed/unreadable metadata, optional 32-bit absence, existing tree that differs from selected outputs, and in-jail versus host facts. Removing the call site must fail the tests.

Each owner runs `go test ./internal/entrypoint ./internal/cli/run` (A), or `go test ./internal/cli/check` (B), plus its relevant Nix evaluation/build check; run `just format` before each conventional commit. After both commits, the custodian runs the targeted integration case and the combined `just check-ci`/reached integration gate once. No source lane claims those combined results.

**External limit:** this jail has no `/dev/dri`. Unit fixtures and a nested jail cannot prove real GPU access or the reported card-node ioctl. A Linux GPU host must verify the generated tree; Mesa 26.2.5 must be tested with it present before attributing the earlier failure to that version. R4 remains separate and unproven; capture ioctl/fd evidence before considering any remedy.
