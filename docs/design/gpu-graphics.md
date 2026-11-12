---
title: "A GPU container needs the driver tree its libraries expect"
status: accepted
stage: BUILT
next: "A real Linux GPU host verifies the generated tree and tests Mesa 26.2.5 with it present; then graduate"
---

# A GPU container needs the driver tree its libraries expect

**Status:** 2026-10-28. Accepted against source base `e02bb526`; built 2026-10-27 (commit `6dda57495`, the boot step and its manifest). MEASURED: unit, fixture and integration tests. UNMEASURED: a real GPU host, which must still verify the tree and test Mesa 26.2.5 with it present.

> **In short.** When GPU support is enabled, a Linux container boot should expose the selected graphics driver's actual library and data outputs at the conventional runtime paths. Device passthrough and driver-tree exposure are separate requirements.

**Why it matters.** A GPU can be visible while EGL/GBM cannot find libraries at the paths compiled into the driver.

**The shape.** Image/profile metadata records resolved driver outputs; container boot creates a missing conventional tree, and `yolo check` reports what was selected and exposed.

**Cost.** One small Nix-generated manifest, boot-time symlinks under `/run`, and checks that distinguish guest state from host readiness.

**Start at:** [Provisioning contract](#provisioning-contract).

**Needs your ruling:** None.

**Reads with:** [`gpu-graphics.md`](../research/gpu-graphics.md) (evidence and limits), [Build invariants](#build-invariants) (what the retired build plan pinned), [`ROCm passthrough`](../reference/rocm-passthrough.md) (device/memlock contracts), [`image delivery`](../reference/image-staging-vs-baking.md) (baked versus selected profile outputs).

## Provisioning contract

The Nix image/profile producer records selected graphics-driver entries in `graphics-drivers.json`. Each entry carries the resolved package name and version, architecture (`64` or `32`), and the actual `lib` and `share` output paths. The producer validates both directories. Entries follow selected package order; the first selected entry for each architecture is the one exposed. The baked image installs the manifest at `/etc/yolo-jail/graphics-drivers.json`; store-delivered packages carry the same manifest beside that package profile. This is runtime metadata, not a new user config key. The current locked nixpkgs has Mesa `out` candidates for 64-bit and 32-bit, but generation must validate the required directories; the nested NVIDIA package's graphics-library layout is unverified and is not claimed as supported until its outputs are validated ([research](../research/gpu-graphics.md#fast-moving--verify-before-building)).

At container boot, after store-profile setup and before loader-cache/application bootstrap. This is the jail's own boot step, marked `notSessionPass`; an attach never reruns it and therefore preserves the running jail's driver provenance.

1. Run only when the launch explicitly hands off `gpu.enabled: true`. Device availability is not the gate: the tree can be prepared even when a requested device is unavailable.
2. Read the selected profile's manifest when store delivery is active; otherwise read baked-image metadata. Never combine a stale baked manifest with a selected store profile.
3. For each supplied architecture, if `/run/opengl-driver` or `/run/opengl-driver-32` is absent, create `lib` and `share` symlinks to that entry's resolved outputs. The 32-bit tree exists only when a selected 32-bit entry supplies it; it is never inferred or required when absent.
4. If the entire destination tree already exists, preserve it byte-for-byte, including incomplete or dangling entries. Do not fill, repair, or replace any child. Record separately that the driver's selected outputs and the exposed tree may differ.
5. Validate every selected architecture's two outputs before changing either destination. Build/publish the missing trees as one generation: on any operational failure, remove only temporary or newly created trees from this attempt, leave every preexisting tree untouched, and fail boot with an actionable next step. Write selected package identity and exposure provenance under `/run/yolo/graphics-drivers.json` only after all selected trees succeed, with `preserved: true` when a preexisting tree was left untouched and `false` when links were generated. It identifies selected outputs but does not prove that a preserved tree points to them.

Disabled GPU configuration and absence of a selected driver entry are no-ops: create no directory and write no selected-driver provenance. A malformed manifest or selected output that cannot be validated is a fatal generation error with an actionable next step; do not guess paths. Prevalidate all selected records and publish the required trees as one attempt, so an error never leaves a partial new tree set. A destination that was already present is never rolled back or mutated.

## Diagnostics and boundaries

For an enabled GPU on Linux, `yolo check` reports selected resolved package name/version and the exposed `lib`/`share` paths, checking their presence. It warns with actionable selection guidance (for example, selecting Mesa where appropriate, and using the existing package-pinning mechanism when a stable version is needed) when no driver is selected or expected paths are absent. It must distinguish selected-driver metadata from a preserved preexisting tree and must not claim the selected driver is what that tree exposes. A 32-bit path is checked only when metadata supplies one. Malformed or unreadable metadata is reported, not silently guessed.

A supported package pin remains the existing [`packages` mechanism](../../userguide/guides/packages-and-tools.md#nix-packages-image-level); add graphics-specific discovery to the user guide only after the implementation contract is live. Do not add config keys. An in-jail check reads runtime provenance and guest paths. If provenance is absent, identify the selected package from the active baked or selected-profile manifest without claiming any path was exposed. A host check may report host GPU readiness, but must not describe host `/run` as the guest tree; direct the user to check inside the running jail for resolved package version and exposure. Store-profile diagnostics use the selected profile, never the baked manifest as a fallback when store delivery is active.

This provisioner runs inside the Linux container-entrypoint boot path only, including Apple Container's Linux guest when the requested GPU flag is enabled. It does not create or alter paths on the host, macos-user, or native macOS. Apple Container has no GPU-device passthrough: exposing a driver tree there does not imply hardware access. The provisioner does not change device selection, privileges, seccomp, memlock, or GPU security policy. R4's reported card-node ioctl `EACCES` remains a separate, unproven issue; no privilege remedy is part of this design.

## Build invariants

The build was handed off through a companion plan, now retired; git history keeps its lane map and gate commands. These are the rules it pinned beyond the sections above.

- **The boot signal follows the request.** The launch passes `YOLO_GPU_ENABLED=1` or `YOLO_GPU_ENABLED=0` from the requested `gpu.enabled`, never from host-confirmed device availability, and an explicit `0` overrides any inherited value. `internal/cli/run/assemble.go` keeps the two apart on purpose; the launch call-site tests pin both values when host GPU probing declines, including on Apple Container, where no device is passed through.
- **The boot step's place is pinned.** The step is `configure_graphics_drivers` in `internal/entrypoint/bootsteps.go`, after `generate_store_packages` (which materializes the store profiles) and before `generate_ld_cache` and application bootstrap. It is marked `notDarwin`, because macos-user shares the step list and runs no container GPU exposure, and `notSessionPass`, as above. `bootsteps_test.go` pins both boot orders. Nothing is provisioned on the host or at image build time.
- **The manifest's two homes.** The baked image carries `/etc/yolo-jail/graphics-drivers.json`; each store package profile carries `share/yolo-jail/graphics-drivers.json`. An entry's `lib` and `share` outputs may have different store roots. The `yolo check` side decodes this JSON itself and depends on no entrypoint API.
- **A preexisting tree is checked at its root without following links.** It is wholly user-supplied, even when incomplete or dangling.
- **Versions are re-evaluated, not remembered.** The Mesa `out` and `pkgsi686Linux.mesa` `out` candidates are a source evaluation of the locked nixpkgs and must be re-evaluated after a nixpkgs bump. Mesa 26.2.5 is not to be called stable. No NVIDIA record is emitted or promised until its graphics `lib` and `share` outputs are validated.
- **What the tests cover.** The boot tests (`internal/entrypoint/graphics_test.go`, `graphics_behavior_test.go`) cover resolved 64-bit paths, distinct `lib` and `share` roots, optional 32-bit metadata, first-record selection, preexisting, incomplete and dangling trees, the disabled and no-driver no-ops, fatal invalid metadata or outputs, and no partial tree. The `yolo check` tests (`internal/cli/check/graphics_test.go`) go through the real `Check` call site and cover resolved name and version, present and absent paths, no driver, malformed or unreadable metadata, an absent optional 32-bit tree, a preexisting tree that differs from the selected outputs, and in-jail versus host facts; removing the call site must fail them. `integration/gpugraphics_test.go` is the combined case.
- **R4 waits on evidence.** Capture ioctl and file-descriptor evidence for the reported card-node `EACCES` before considering any remedy.

The graphics-specific user-guide pointer is live in [Devices and GPUs](../../userguide/guides/devices-and-gpus.md#mesa-graphics-libraries).

## Acceptance

A built result exposes explicit selected outputs only when enabled, leaves disabled/no-driver boots without a tree, handles optional 32-bit outputs only when supplied, preserves every preexisting tree unchanged, and reports absent or mismatched exposure honestly. A real GPU host must still verify the tree and test Mesa 26.2.5 with it present; fixture or nested-jail evidence cannot prove the reported DRM ioctl behavior.
