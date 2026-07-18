# Handoff: AMD ROCm Passthrough — finishing on real hardware

**To:** the host-side / outside-jail agent (or human) with access to an AMD GPU box.
**From:** the in-jail agent that implemented "Bucket A".
**Date:** 2026-06-05
**Branch:** `feat/rocm-passthrough`
**Design doc:** [`docs/design/rocm-passthrough-design.md`](../design/rocm-passthrough-design.md) — read it first; this handoff assumes it.

> **✅ STATUS: Bucket B VERIFIED ON HARDWARE (2026-06-05).** All steps below were run on a real
> AMD Radeon 8060S (gfx1151, Strix Halo APU), host ROCm 7.2.3 / image ROCm 7.2.4, rootless podman +
> crun, no SELinux. Both the device-node and CDI paths run ROCm PyTorch end-to-end. **Two real bugs
> were found and fixed** (`ROCR_VISIBLE_DEVICES=all` hid the GPU → default config shipped GPU-less;
> `yolo check` mislabeled CPU/NPU agents as GPUs). The `needs-verification` markers have been removed
> from `src/`. See design doc **§7.1 "Resolved on hardware"** for the full findings and Open-Question
> resolutions. The notes below are retained as the original handoff for context.

---

## TL;DR

All in-jail-safe code for AMD ROCm GPU passthrough is **written, tested, and committed** on
`feat/rocm-passthrough`. It mirrors the existing NVIDIA/CUDA path by extending the `gpu`
config block with a `vendor: "nvidia" | "amd"` discriminator (default `nvidia`, fully
backward-compatible). The AMD default is **raw device-node passthrough** (`/dev/kfd` +
`/dev/dri/renderD*` + `--group-add keep-groups`), with an optional `mode: "cdi"`.

Everything **fails soft**: on a host with no AMD GPU, `yolo run` with `gpu.vendor=amd` prints
a one-line warning and starts *without* GPU flags. So this branch is safe to merge before any
hardware verification — but it has **not been run against a real AMD GPU**. That is your job.

**What you cannot assume is verified:** anything requiring real `/dev/kfd`, the amdgpu driver,
ROCm userspace, or root on a GPU host. See [Bucket B](#bucket-b--what-you-must-verify-finish).

---

## What was done in-jail (Bucket A — DONE & VERIFIED)

14 files, +1051/−39. Verified in-jail by: `ruff check` clean on all changed files;
`589 passed` on the three targeted test files; `192 passed` across all
gpu/rocm/validate/kvm tests; `import cli` + probe smoke-checked. NVIDIA path untouched.

| File | Change | Design ref |
|------|--------|-----------|
| `src/cli/config.py` | `KNOWN_GPU_KEYS` += `vendor,mode,hsa_override_gfx_version,seccomp_unconfined`; validation: vendor∈{nvidia,amd}, mode AMD-only ∈{devices,cdi}, capabilities rejected for AMD, gfx AMD-only str, seccomp bool; vendor-neutralized `devices` error text | §4.2, §5.1 |
| `src/cli/loopholes_runtime.py` | `_rocm_host_available(runtime)` — layered probe: macOS/container/non-podman skip → `/sys/module/amdgpu` → `/dev/kfd` → `/dev/dri/renderD*` → optional `rocminfo` rc (no argv flags); rocminfo absence non-fatal | §5.2 |
| `src/cli/__init__.py` | re-export `_rocm_host_available` | §5 |
| `src/cli/run_cmd.py` | import probe; read `gpu_vendor`; dispatch probe by vendor; **gate NVIDIA runc/uidmap/SYS_ADMIN branch to `vendor=="nvidia"`** so AMD stays on crun; AMD injection block (kfd + dri/renderD or `amd.com/gpu=`; `--group-add keep-groups`; `ROCR_VISIBLE_DEVICES`+`HIP_VISIBLE_DEVICES`; optional `HSA_OVERRIDE_GFX_VERSION`; optional `seccomp=unconfined`) | §4.3, §4.4, §5.3 |
| `src/cli/check_cmd.py` | new "GPU (AMD/ROCm)" `yolo check` block (rocminfo enumeration, amdgpu module, /dev/kfd + renderD* group membership via the KVM idiom, keep-groups note, CDI-spec check when `mode:cdi`); skips host-state checks inside a jail | §5.4 |
| `src/cli/config_ref_cmd.py` | retitled GPU reference to NVIDIA + AMD; documents new keys; AMD example config | §5 |
| `docs/guides/USER_GUIDE.md`, `docs/research/platform-comparison.md`, `docs/guides/macos.md`, `docs/research/sandbox-comparison.md` | AMD/ROCm sections + matrix rows; macOS = neither CUDA nor ROCm | §5 |
| `yolo-jail.jsonc` | commented `vendor:"amd"` example; note capabilities is NVIDIA-only | §5 |
| `tests/test_cli_unit.py` | `TestRocmHostAvailable` (9 cases) + gpu-schema validation tests | §5.5 |
| `tests/test_cli_commands.py` | `TestRunRocm` (positive argv asserts + negative warn-and-skip) | §5.5 |
| `tests/test_macos_paths.py` | AMD macOS-skip + ROCm `check` warning | §5 |

> **needs-verification markers:** every AMD *host* command in the code/docs (rocminfo, `amd-ctk`,
> usermod, modprobe) is annotated as confirmed-against-docs-only. Grep for `needs-verification`.

---

## How to pick up this branch

```sh
git fetch && git checkout feat/rocm-passthrough
# Repo uses uv + mise. Sanity:
uv run ruff check src/cli/
uv run --group dev python -m pytest tests/test_cli_unit.py tests/test_cli_commands.py tests/test_macos_paths.py -q
```

Both should be green with no AMD hardware present (the tests mock the host probe).

---

## Bucket B — what you must verify / finish

You need an **AMD GPU host** (Instinct MI-series, or a consumer Radeon — RDNA cards work via
the device-node path) running Linux with the `amdgpu` kernel driver, podman (rootless), and
root for host setup. Work top-to-bottom; each step gates the next.

### B1. Host prerequisites
```sh
# amdgpu kernel driver (DKMS) must be loaded:
lsmod | grep amdgpu                  # expect amdgpu listed
ls -l /sys/module/amdgpu             # expect dir present
ls -l /dev/kfd /dev/dri/renderD*     # expect crw-rw---- root:render
# Your host user must be in the owning group(s):
id                                   # expect 'render' (and maybe 'video')
# If missing: sudo usermod -aG render,video $USER  && log out/in
```
Install ROCm userspace **on the host only if you want `rocminfo`/`rocm-smi` for `yolo check`**
(not required for the container path — ROCm userspace lives in the image):
`rocminfo` should list your GPU agent(s).

### B2. Verify `yolo check` (device-node mode)
With a workspace `yolo-jail.jsonc` containing:
```jsonc
{ "gpu": { "enabled": true, "vendor": "amd" } }
```
Run **on the host** (not in a jail — host-state checks self-skip inside a jail):
```sh
yolo check
```
Expect the new **GPU (AMD/ROCm)** block: rocminfo detection (or a fail w/ install URL),
`amdgpu kernel module loaded`, `Device node: /dev/kfd` + readable/writable, each
`/dev/dri/renderD*`, and `Podman will preserve render/video group via --group-add keep-groups`.
**Confirm the group-membership messages are correct for your card** (this resolves Open Question #2).

### B3. End-to-end run (device-node mode) — the real test
Use a base image that ships ROCm userspace (generic images get device nodes but no working HIP):
```sh
# point the jail image / or run a quick container with the same flags yolo emits:
yolo run -- bash
# inside the jail:
rocminfo | grep -i 'Marketing Name'          # GPU visible?
python -c "import torch; print(torch.cuda.is_available(), torch.cuda.get_device_name(0))"
```
Use a `rocm/pytorch` image (PyTorch-ROCm exposes the GPU via the `torch.cuda` API).
- If `Permission denied` opening `/dev/kfd`: the `--group-add keep-groups` path isn't working
  rootless — investigate crun version + `/etc/subgid`. **This is the make-or-break detail.**
- If the GPU isn't recognized / `hipErrorNoBinaryForGpu`: try setting
  `gpu.hsa_override_gfx_version` (e.g. `"11.0.0"` for gfx1100, `"10.3.0"` for gfx1030) — resolves
  consumer-GPU support. Confirm the right value for your card (Open Question #4).

### B4. Optional: AMD Container Toolkit / CDI mode
Only if you want `mode: "cdi"` (`amd.com/gpu=all`) instead of raw device nodes:
```sh
# Install the AMD Container Toolkit (repo.radeon.com; needs host root):
sudo amd-ctk cdi generate --output=/etc/cdi/amd.json
amd-ctk cdi list                     # expect amd.com/gpu=all, amd.com/gpu=0 -> renderD128
```
Then set `"mode": "cdi"` in the config and re-run B2/B3.
**Verify (Open Question, design §3.4 / refuted #28):** inspect `/etc/cdi/amd.json` and confirm it
injects **only device nodes** (no env vars, no host-lib mounts). If it does inject more, the docs
need a correction.
**Also verify** whether AMD CDI under crun hits any injection bug (NVIDIA needed runc for this).
If it does, that's a real finding — but note AMD *can't* use the runc workaround (keep-groups is
crun-only), so a crun fix or a different approach would be needed. Document whatever you find.

### B5. SELinux / seccomp (host-policy-dependent)
On SELinux hosts you may need `sudo setsebool -P container_use_devices 1`. The `seccomp_unconfined`
config knob exists (default off) but AMD docs mark seccomp **optional** for basic compute — only
turn it on if a workload needs it. Confirm what your host actually requires and note it in the docs.

---

## Open Questions to resolve on hardware (from design §7)

1. **podman#27483 / NVIDIA-runc premise (uncertain).** The keep-groups⇒crun-only fact is solid and
   load-bearing; the *specific* issue number behind yolo's existing NVIDIA runc pin couldn't be
   confirmed in-jail (closest real issue: crun#1908). Confirm the exact issue and whether AMD
   `mode:cdi` under crun hits anything analogous (see B4).
2. **render vs video group.** We default to `keep-groups` (covers both). Confirm on your card which
   group owns `/dev/kfd` + renderD*, and whether a least-privilege explicit `--group-add <gid>`
   would be preferable. (B2)
3. **`mode` for `vendor=nvidia`: error vs ignore.** Currently we *error*. Pure consistency call —
   confirm that matches how you want vendor-specific keys treated.
4. **HSA_OVERRIDE_GFX_VERSION value table** (best-effort, not adversarially verified). Confirm the
   right value for the actual card; same-architecture-family only. (B3)
5. **ROCm host/userspace version window.** AMD guarantees ±1yr (≥6.4.0). Decide whether `yolo check`
   should warn when host driver and image ROCm drift outside the window.
6. **Default image steering.** Should yolo warn/refuse when `gpu.vendor=amd` + a non-`rocm/*` base
   image (device nodes present but no working HIP)? Product decision: warn vs hard error vs silent.

---

## When hardware-verified

- Remove the `needs-verification` markers in code/docs (grep for them) once each command is confirmed.
- Update `docs/design/rocm-passthrough-design.md` §7 with the resolved answers, and
  `docs/research/platform-comparison.md` with confirmed support status.
- Consider opening the PR (this branch can't `git push` from inside a jail — no GitHub creds here).
- If you add any per-GPU least-privilege group mode (Open Question #2), add a `TestRunRocm` case for it.

## Pointers
- Raw research + 30 adversarially-verified claims: `scratch/rocm-harvest/` (gitignored — in-jail only;
  copy out if you want them). The design doc inlines the conclusions.
- The NVIDIA path is the working reference for everything: `run_cmd.py` (~1280 gating, ~1620 injection),
  `check_cmd.py` (~900), `loopholes_runtime.py:375` `_gpu_host_available`, `config.py` (~94, ~878).
