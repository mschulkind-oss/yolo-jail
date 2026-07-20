# Design: AMD ROCm GPU Passthrough for yolo-jail

**Status:** Draft / implementation-ready
**Date:** 2026-06-05
**Author:** systems engineering (in-jail)
**Scope:** Add AMD ROCm GPU passthrough to yolo-jail, mirroring the existing NVIDIA/CUDA path, with a clean in-jail-now / host-agent-later split.

---

## 1. Goal & summary

yolo-jail already passes NVIDIA GPUs into its sandbox via podman + CDI (`gpu.enabled: true` → `--device nvidia.com/gpu=all`). This design adds **AMD ROCm passthrough** by extending the same `gpu` config block with a `vendor: "nvidia" | "amd"` discriminator (default `nvidia`, fully backward-compatible). The AMD path **defaults to raw device-node passthrough** — `--device /dev/kfd` + `--device /dev/dri/renderD*` + `--group-add keep-groups` — modeled on the existing `kvm: true` precedent rather than NVIDIA's CDI machinery, because the device-node path needs **no host toolkit**, works on consumer Radeon hardware, and is the more battle-tested path (verified claims #8, #11, #19, #22). An optional `mode: "cdi"` reuses the NVIDIA-style injection against `amd.com/gpu=all` when the host has the (young, Instinct-focused) AMD Container Toolkit. Crucially, the AMD branch **must NOT** inherit NVIDIA's `--runtime runc` workaround: rootless `keep-groups` is crun-only, so AMD stays on podman's default crun. Every code change (schema, validation, argv construction, host probe, diagnostics, docs, unit tests with mocked probes) is doable **inside this jail now**; only toolkit install, CDI generation, and real-hardware verification require the later host-side agent.

---

## 2. How NVIDIA passthrough works today

File-anchored from the codebase map. All citations are to `/workspace/src/cli/...`.

- **Config schema** (`config.py:94`): `KNOWN_GPU_KEYS = {"enabled","devices","capabilities"}`; `"gpu"` is registered in `KNOWN_TOP_LEVEL_CONFIG_KEYS` (`config.py:65`). No typed dataclass — config is consumed as a raw dict via `config.get("gpu", {}).get(...)`.
- **Validation** (`config.py:863-900`): `gpu` must be a dict; `_report_unknown_keys(gpu, KNOWN_GPU_KEYS, ...)` rejects stray keys; `enabled` must be bool; `devices` must be a str (type-only, no format check); `capabilities` must be a str whose comma tokens are all in the NVIDIA `valid_caps` allowlist `{compute,utility,graphics,video,display,compat32}` (`config.py:886-893`). No defaults applied here.
- **Host probe** (`loopholes_runtime.py:375-414`): `_gpu_host_available(runtime) -> (bool, Optional[str])`. Order: macOS/`container` → `(False, "runtime does not support NVIDIA passthrough")`; non-podman → `(False, "unsupported runtime: ...")`; `shutil.which("nvidia-smi")` missing → fail; `nvidia-smi -L` rc≠0 → `(False, "nvidia-smi reported no GPUs")`; no CDI spec at `/etc/cdi/nvidia.yaml` or `/var/run/cdi/nvidia.yaml` → fail; else `(True, None)`. Exported from `__init__.py:182`, imported into `run_cmd.py:59`. Sole production caller: `run_cmd.py:1283`.
- **Runtime/userns branch** (`run_cmd.py:1292-1352`): three-way on podman — `in_container` shares parent userns; `elif gpu_enabled` (`1303-1330`) forces `--security-opt label=disable`, identity `--uidmap/--gidmap 0:0:1 + 1:1:65536`, `--runtime runc`, `--cap-add SYS_ADMIN` (runc dodges the crun+CDI+custom-userns bug, podman#27483; identity uidmap avoids slow keep-id shifting); the normal `else` (`1331-1352`) keeps `--device /dev/fuse` and `--cap-add MKNOD`, which the GPU branch drops.
- **Device injection** (`run_cmd.py:1620-1645`): gated on `gpu_enabled`; `devices=="all"` → `--device nvidia.com/gpu=all`, else per-index `--device nvidia.com/gpu={idx}`; then `-e NVIDIA_VISIBLE_DEVICES={devices}` and `-e NVIDIA_DRIVER_CAPABILITIES={capabilities}`. Warn-and-continue when requested-but-unavailable (`run_cmd.py:1615-1619`).
- **Diagnostics** (`check_cmd.py:902-1011`): "GPU (NVIDIA)" block — nvidia-smi detection, nvidia-ctk presence, podman-only runc + CDI-spec + driver-staleness checks. The KVM block (`check_cmd.py:1019-1084`) is the model for device-node + group-membership diagnostics (uses `os.access`, `grp.getgrgid`, `os.getgroups()`, in-jail skip via `YOLO_VERSION`, and confirms podman `--group-add keep-groups`).
- **Docs**: `config_ref_cmd.py:282-301` (reference) + `:389-393` (EXAMPLE CONFIG); `docs/guides/USER_GUIDE.md:602-689`; `docs/research/platform-comparison.md`; `docs/guides/macos.md:356-362`; `docs/research/sandbox-comparison.md:414-416`; `yolo-jail.jsonc:99-105`. README has no GPU mention.

---

## 3. How ROCm differs

Each point is anchored to a **confirmed** verified claim (`#N` = index in `verified_claims`). Refuted myths are flagged explicitly.

### 3.1 Device-node model vs CDI

- **Two device classes, not a vendor CDI namespace by default.** ROCm needs `/dev/kfd` (the Kernel Fusion Driver — the single compute interface *shared by all GPUs*, **always required**) plus one or more `/dev/dri/renderD<N>` render nodes (N from 128, one per GPU on non-partitioned systems: GPU 0 = renderD128, GPU 1 = renderD129). Confirmed #10, #19, #22, #24. The card nodes `/dev/dri/card*` are **NOT required** for headless compute (claim dm4, confirmed #12).
- **AMD *does* have a CDI toolkit, but it's optional.** The AMD Container Toolkit (`amd-ctk` + `amd-container-runtime`, ROCm/container-toolkit, Apache-2.0) generates a CDI spec at **`/etc/cdi/amd.json`** (JSON, *not* a YAML named like nvidia.yaml), in the same `/etc/cdi` + `/var/run/cdi` search dirs. Device names are `amd.com/gpu=all` / `amd.com/gpu=0` / `amd.com/gpu=1`, exactly mirroring `nvidia.com/gpu=`. Confirmed #5, #6, #14, #21, #26, #29. **`amd-ctk cdi list` maps `amd.com/gpu=0` → /dev/dri/renderD128** (confirmed #14).
- **NOT required: a host toolkit.** AMD documents a "Without the AMD Container Toolkit" path that "requires no additional software beyond Docker" — just the device flags. Confirmed #8. The toolkit is young (v1.0.0 June 2025 → v1.3.0 May 2026), Instinct/Ubuntu/RHEL-scoped, with no consumer-Radeon support, so it is unrealistic to require it on a developer workstation.

### 3.2 Render/video groups & rootless GID handling

- **Group membership is the make-or-break detail.** `/dev/kfd` and `/dev/dri/renderD*` are `crw-rw---- root:render`; `/dev/dri/card*` is `root:video`. The container process must hold the owning GID or `open()` fails with `Unable to open /dev/kfd read-write: Permission denied`. Confirmed #23 (NVIDIA nodes are world-rw 0666 and need no group — a clean contrast).
- **Rootless podman requires `--group-add keep-groups`.** A bare `--group-add render` fails rootless: userns GID-offsetting maps the in-container GID to a high host GID (e.g. 100039), not the real `render` GID. `keep-groups` tells crun to skip `setgroups()` so the host supplementary groups survive (shows as `nobody` inside, but kernel checks use the real host GID and succeed). Confirmed #2; this is the same flag the existing `kvm: true` path already uses (`run_cmd.py:1668`).
- **`keep-groups` is crun-only and exclusive.** It cannot be combined with any other `--group-add`, and does not work under runc (confirmed #2, well-sourced half of #16). **This is why the AMD branch must stay on crun and must NOT take NVIDIA's `--runtime runc` branch.**
- **Group-name ambiguity is real.** Sources disagree (`video` vs `render` vs both). Modern `/dev/kfd`/renderD nodes are `render`-owned; `video` is only needed for card nodes. For least privilege, compute needs `render` only (refined claim #17, uncertain — see Open Questions).

### 3.3 Seccomp

- **NOT required: `--security-opt seccomp=unconfined`.** AMD marks it explicitly **optional** — it "enables memory mapping" and is only recommended for HPC/numactl workloads, not basic ROCm compute. Confirmed #11, #27. Disabling seccomp removes all syscall filtering and widens escape surface, so it must **never be a default**; expose it as an opt-in knob only.

### 3.4 Image-baked ROCm userspace vs NVIDIA library injection

- **NOT injected: ROCm userspace libraries.** Unlike NVIDIA's toolkit (which bind-mounts host driver libs like `libnvidia-ml.so.<ver>` into arbitrary images), AMD's Docker/podman/CDI paths inject **only kernel device nodes** — ROCm userspace (HIP, rocm-smi, math libs) ships *inside* the image (`rocm/*` base images). Confirmed #0, #3; refined by **refuted #1** (the "no library injection" property is specific to AMD's Docker/podman/CDI/manual flows — Apptainer's `--rocm` flag *does* bind host ROCm libs, so do not state it as universal across all ROCm runtimes).
- **Consequence:** a generic base image gets working device nodes but **no working HIP/rocm-smi** unless the image ships ROCm. The doc and (optionally) a warning should steer AMD-GPU users toward a `rocm/*` image.
- **No driver-vs-spec staleness coupling.** Because no userspace libs are injected, there is no NVIDIA-style "CDI spec stale vs driver" check. Confirmed #3, and reinforced by **refuted #28**: the AMD CDI generator (`internal/cdi/cdi.go`, spec v0.6.0) populates **only `ContainerEdits.DeviceNodes`** — **NOT environment variables**, NOT OCI hooks, NOT host-library mounts. *Do not claim AMD CDI injects env vars.* (Env vars like `AMD_VISIBLE_DEVICES` belong to the separate Docker-only `amd-container-runtime`, not CDI.)

### 3.5 Env vars

- **NOT used: `NVIDIA_VISIBLE_DEVICES` / `NVIDIA_DRIVER_CAPABILITIES`.** There is no `NVIDIA_DRIVER_CAPABILITIES` analog for AMD — omit capabilities entirely.
- In-container GPU selection uses `ROCR_VISIBLE_DEVICES` (ROCr/HSA — AMD's *recommended* selector on Linux) and/or `HIP_VISIBLE_DEVICES` (HIP layer); both accept comma indices or `GPU-<uuid>`. `AMD_VISIBLE_DEVICES` is the Docker-only runtime-shim knob — not relevant to a podman tool. **Caveat (confirmed claim c10 in research):** these env vars are explicitly **not a security boundary** — an app can reset them; real isolation comes only from which renderD nodes you `--device` in.

### 3.6 HSA_OVERRIDE_GFX_VERSION

- Consumer/unsupported GPUs frequently need `HSA_OVERRIDE_GFX_VERSION` (e.g. `11.0.0`→gfx1100, `10.3.0`→gfx1030, `9.0.0`→gfx900) to make ROCm treat the GPU as a supported gfx target. It is a **pure userspace env var** (no host change) — safe to set from inside the jail. It is best-effort, **same-architecture-family only** (cross-arch maps like RDNA3→RDNA2 are unreliable), and unsupported by AMD. (Research angle `version-compat-userspace`, claims c13/c14 — not adversarially re-verified, so treat the exact mappings as best-effort.)

### 3.7 runc vs crun

- NVIDIA forces `--runtime runc` (CDI+crun+custom-userns bug). **AMD must stay on crun** because rootless `keep-groups` is crun-only. The NVIDIA runc/uidmap/SYS_ADMIN/label=disable block must be gated `vendor == "nvidia"`. (Well-sourced half of #16; the *specific* podman#27483 reference is **uncertain** — see Open Questions — but the keep-groups⇒crun fact is solid and is the load-bearing constraint here.)

### 3.8 Security surface (brief)

- `/dev/kfd` is a single global ioctl-rich node that **cannot be scoped per-GPU** (confirmed #24); it carries recurring local-privilege-escalation kernel CVEs (e.g. CVE-2026-43206 OOB write in `kfd_event_page_set`, confirmed #7; CVE-2026-46197, CVE-2025-21940, real per #4 — but **NOT required: the claim that the AMD vendor bulletin says "container platform... has a higher risk profile"; that quote is from third-party commentary, not AMD** — refuted #4). Per-GPU restriction is only via choosing specific renderD nodes.
- Least-privilege defaults follow from this: pass only the needed renderD node(s) + `/dev/kfd`; keep seccomp on; do **not** add `SYS_ADMIN`/`SYS_PTRACE`/`--ipc=host` for AMD.

---

## 4. Proposed design

### 4.1 Decision: reuse the `gpu` block with a `vendor` discriminator

**Recommended: extend the existing `gpu` block with `vendor` and `mode`, NOT a separate `rocm` block.**

Justification:
- One `enabled`/`devices` code path; one host-probe call site; one warn-and-continue path. The codebase map's config-schema analysis recommends this explicitly as "strictly simpler" than a parallel `rocm` block (which would need a new `KNOWN_ROCM_KEYS`, a `rocm` top-level key, a parallel validate block, and mutual-exclusion logic).
- Backward compatible: `vendor` defaults to `nvidia` when absent, so every existing config keeps working untouched.
- The device-injection and runtime-selection logic *do* diverge by vendor, but that is a clean branch inside the consumer, not a reason to fork the schema.

(One research angle argued for a separate block on the grounds that the two layers differ; we reject that because the differences live in `run_cmd.py`'s injection/runtime branches, not in the config surface, and a discriminator keeps the validation/probe wiring symmetric.)

### 4.2 Config schema

```jsonc
"gpu": {
  "enabled": true,
  "vendor": "amd",          // "nvidia" (default) | "amd"
  "devices": "all",         // "all" | "0" | "0,1" | "GPU-<uuid>"
  "mode": "devices",        // AMD only: "devices" (default, no toolkit) | "cdi"
  "capabilities": "...",    // NVIDIA only; rejected/ignored for vendor=amd
  "hsa_override_gfx_version": "11.0.0",  // AMD only, optional
  "seccomp_unconfined": false            // AMD only, optional opt-in (default false)
}
```

Defaults & validation rules:
- `vendor`: optional str ∈ `{"nvidia","amd"}`; **default applied at the consumer** (`run_cmd.py` / `check_cmd.py`), not in the validator, matching the existing no-defaults-in-validator pattern. Absent ⇒ `nvidia`.
- `devices`: unchanged type-only check (already accepts `all`/`0`/`0,1`). Only the error-text examples need vendor-neutralizing.
- `mode`: AMD only; str ∈ `{"devices","cdi"}`; default `devices`. Error if set for `vendor=nvidia` (or simply ignore — see Open Questions).
- `capabilities`: keep the NVIDIA allowlist for `vendor=nvidia`; for `vendor=amd` **emit an error if set** (AMD has no `NVIDIA_DRIVER_CAPABILITIES` analog — silently accepting it would mislead users; per docs-risk note).
- `hsa_override_gfx_version`: AMD only; optional str. Format is best-effort; validate as a non-empty str only.
- `seccomp_unconfined`: AMD only; optional bool; default `false`.

### 4.3 Device-injection strategy

**Primary: raw device nodes (`mode: "devices"`, the default).** For AMD, emit:
- `--device /dev/kfd` (always, guarded by `Path("/dev/kfd").exists()`).
- For `devices == "all"`: `--device /dev/dri` (whole dir grants all GPUs) — or, for tighter default, all discovered `/dev/dri/renderD*` nodes. Recommend `--device /dev/dri` for the `all` case to match AMD docs (confirmed #10).
- For per-index `devices == "0,1"`: `--device /dev/dri/renderD{128+idx}` for each index, always alongside `/dev/kfd`.
- `--group-add keep-groups` (podman only) — mandatory for rootless device access (confirmed #2).
- `-e ROCR_VISIBLE_DEVICES={devices}` and `-e HIP_VISIBLE_DEVICES={devices}` (skip when `devices=="all"` is fine, or pass `all`).
- If `hsa_override_gfx_version` set: `-e HSA_OVERRIDE_GFX_VERSION={value}`.
- If `seccomp_unconfined: true`: `--security-opt seccomp=unconfined` (opt-in only).

**Fallback/alternative: AMD CDI (`mode: "cdi"`).** Reuse the NVIDIA injection shape parameterized on vendor prefix: `--device amd.com/gpu=all` or `--device amd.com/gpu={idx}`. **Still emit `--group-add keep-groups`** — CDI does *not* remove the group requirement for rootless (confirmed: research claim `amd-cdi-still-needs-groups`). Do **not** emit env vars for the CDI case (CDI injects nodes only; no env — refuted #28).

**Why devices-first:** the device-node path needs no host toolkit (confirmed #8), works on consumer Radeon (toolkit is Instinct-only, claim C9), and is far more widely deployed. CDI is the nicer per-GPU-selection option when the host already has `amd-ctk` and `/etc/cdi/amd.json`, so we offer it as opt-in via `mode: "cdi"`.

### 4.4 Userns/runtime branch — does AMD need the runc workaround?

**No.** The `run_cmd.py:1303-1330` runc/identity-uidmap/SYS_ADMIN/label=disable block exists *only* to dodge the crun+CDI+custom-userns bug. Raw device-node passthrough has no CDI involvement, and rootless `keep-groups` is **crun-only** — forcing runc would break AMD group access entirely. Therefore:
- Keep `elif gpu_enabled:` (the runc branch) **NVIDIA-only.** Concretely, gate it on `gpu_enabled and gpu_vendor == "nvidia"` (or introduce `nvidia_cdi_enabled`).
- AMD GPU runs fall through to the **normal host `else` branch** (`1331-1352`), which keeps `/dev/fuse` and `MKNOD` and the default crun runtime — exactly what AMD device-node passthrough wants.
- For AMD `mode: "cdi"`: this is the one case where the crun+CDI bug *could* bite. But since `keep-groups` (still required for rootless) is crun-only and incompatible with runc, **we cannot use NVIDIA's runc workaround for AMD CDI either.** Recommend AMD CDI also stays on the normal branch and relies on crun; if real-hardware testing surfaces a crun+CDI failure for AMD, that becomes a host-agent finding (see Open Questions), not an in-jail assumption.

---

## 5. Per-file change plan

| File | Change |
|------|--------|
| `src/cli/config.py:94` | Extend `KNOWN_GPU_KEYS` → `{"enabled","devices","capabilities","vendor","mode","hsa_override_gfx_version","seccomp_unconfined"}`. |
| `src/cli/config.py:863-900` | Add `vendor`/`mode`/`hsa_override_gfx_version`/`seccomp_unconfined` validation; make capability validation NVIDIA-only; vendor-neutralize the `devices` error text (drop the NVIDIA-only `GPU-<uuid>` framing or make it conditional). See stub §5.1. |
| `src/cli/loopholes_runtime.py:~415` | Add `_rocm_host_available(runtime)` after `_gpu_host_available`. Update module docstring (`:13`). See stub §5.2. |
| `src/cli/loopholes_runtime.py:13` | Docstring enumerates `_gpu_host_available`; add the ROCm twin. |
| `src/cli/__init__.py:182` | Re-export `_rocm_host_available` alongside `_gpu_host_available` (tests import via `cli.*`). |
| `src/cli/run_cmd.py:59` | Import `_rocm_host_available`. |
| `src/cli/run_cmd.py:1280-1286` | Read `gpu_vendor = config.get("gpu",{}).get("vendor","nvidia")`; for `amd` call `_rocm_host_available(runtime)` instead of `_gpu_host_available`; set `gpu_enabled`/`gpu_unavailable_reason` the same way. |
| `src/cli/run_cmd.py:1303` | Gate the runc/uidmap branch on `gpu_enabled and gpu_vendor == "nvidia"` so AMD falls through to the normal host branch. |
| `src/cli/run_cmd.py:1620-1645` | After the NVIDIA block, add an `elif gpu_vendor == "amd"` AMD injection block (device nodes / optional CDI / keep-groups / ROCm env). See stub §5.3. |
| `src/cli/check_cmd.py:902` | Branch the "GPU (NVIDIA)" block on `vendor`; add a parallel "GPU (AMD/ROCm)" path using the same `ok/warn/fail` helpers and the KVM block's device-node + group-membership idiom. See §5.4. |
| `src/cli/config_ref_cmd.py:282-301, 389-393` | Retitle to "GPU passthrough (NVIDIA / AMD ROCm)"; document `vendor`/`mode`/AMD prereqs; add an AMD EXAMPLE CONFIG variant. Mark AMD host commands as needs-verification. |
| `docs/guides/USER_GUIDE.md:602-689` | Add an AMD/ROCm subsection: host setup (`amdgpu-dkms`, render/video groups, optional `amd-ctk cdi generate`), config table, ROCm PyTorch install example, runtime-details row (`/dev/kfd` + renderD* + keep-groups), troubleshooting (kfd permissions, render GID, gfx override, version window). No invented AWS instance types. |
| `docs/research/platform-comparison.md:28,63,160-164,192-193,261,267-269` | Add an "AMD GPU (ROCm)" matrix row (Linux-only); vendor-neutralize the diagram/macOS warning text; note vendor branching in the detection table. |
| `docs/guides/macos.md:356-362` | Broaden the GPU-unavailable note to cover AMD/ROCm. |
| `docs/research/sandbox-comparison.md:414-416` | Broaden "No NVIDIA GPU passthrough on macOS" → "No NVIDIA or AMD GPU passthrough". |
| `yolo-jail.jsonc:99-105` | Broaden the commented `gpu` stanza; add a `vendor: "amd"` example; note `capabilities` is NVIDIA-only. |
| `README.md` | Optional only — add a Features bullet if GPU is advertised top-level; otherwise no change. |
| `tests/test_cli_unit.py:82,783,3763` | Import `_rocm_host_available`; add `TestRocmHostAvailable` (mirror `TestGpuHostAvailable`); add `gpu`-schema validation tests (vendor/mode/capabilities-for-amd) + backfill missing NVIDIA validation. See §5.5. |
| `tests/test_cli_commands.py:2278` | Add `TestRunRocm` (mirror `TestRunKvm`): positive argv asserts for `/dev/kfd`, renderD nodes, `--group-add keep-groups`, ROCm env; negative companion; assert `mock_popen.called` first; assert no `nvidia.com/gpu`/`NVIDIA_*` leak. |
| `tests/test_macos_paths.py:375,676` | Add AMD cases to `TestMacosGpuSkip` (no `/dev/kfd`/`/dev/dri`/`amd.com/gpu`) and a ROCm `check` diagnostics test. |

### 5.1 Config validation stub (`config.py:863-900`)

```python
gpu = config.get("gpu")
if gpu is not None:
    if not isinstance(gpu, dict):
        errors.append("config.gpu: expected an object")
    else:
        _report_unknown_keys(gpu, KNOWN_GPU_KEYS, "config.gpu", errors)
        enabled = gpu.get("enabled")
        if enabled is not None and not isinstance(enabled, bool):
            errors.append("config.gpu.enabled: expected a boolean")

        vendor = gpu.get("vendor")
        if vendor is not None and vendor not in ("nvidia", "amd"):
            errors.append("config.gpu.vendor: expected 'nvidia' or 'amd'")
        is_amd = vendor == "amd"

        devices_val = gpu.get("devices")
        if devices_val is not None and not isinstance(devices_val, str):
            errors.append(
                "config.gpu.devices: expected a string ('all', '0', or '0,1')"
            )

        mode = gpu.get("mode")
        if mode is not None:
            if not is_amd:
                errors.append("config.gpu.mode: only valid when vendor='amd'")
            elif mode not in ("devices", "cdi"):
                errors.append("config.gpu.mode: expected 'devices' or 'cdi'")

        capabilities = gpu.get("capabilities")
        if capabilities is not None:
            if is_amd:
                errors.append(
                    "config.gpu.capabilities: not supported for vendor='amd' "
                    "(ROCm has no driver-capabilities concept)"
                )
            elif not isinstance(capabilities, str):
                errors.append(
                    "config.gpu.capabilities: expected a string (e.g. 'compute,utility')"
                )
            else:
                valid_caps = {"compute", "utility", "graphics",
                              "video", "display", "compat32"}
                for cap in capabilities.split(","):
                    cap = cap.strip()
                    if cap and cap not in valid_caps:
                        errors.append(
                            f"config.gpu.capabilities: unknown capability '{cap}'. "
                            f"Valid: {', '.join(sorted(valid_caps))}"
                        )

        gfx = gpu.get("hsa_override_gfx_version")
        if gfx is not None:
            if not is_amd:
                errors.append(
                    "config.gpu.hsa_override_gfx_version: only valid when vendor='amd'"
                )
            elif not isinstance(gfx, str):
                errors.append(
                    "config.gpu.hsa_override_gfx_version: expected a string (e.g. '11.0.0')"
                )

        seccomp = gpu.get("seccomp_unconfined")
        if seccomp is not None and not isinstance(seccomp, bool):
            errors.append("config.gpu.seccomp_unconfined: expected a boolean")
```

### 5.2 `_rocm_host_available()` stub (`loopholes_runtime.py:~415`)

Layered probe per the host-detection research (multiple signals, not one binary). Uses the same deps already imported: `shutil`, `subprocess`, `Path`, `Optional`, `IS_MACOS`.

```python
def _rocm_host_available(runtime: str) -> tuple[bool, Optional[str]]:
    """Probe whether AMD ROCm GPU passthrough will actually work on this host.

    Returns ``(True, None)`` when the host exposes the AMD kernel
    device nodes ROCm needs, or ``(False, reason)`` with a one-line
    warning phrase.  ROCm passthrough is podman + Linux only; other
    runtimes return a skip reason (callers warn/skip earlier).

    Default (device-node) mode needs no host toolkit — just the
    amdgpu kernel driver and the /dev/kfd + /dev/dri render nodes.
    """
    if IS_MACOS or runtime == "container":
        return False, "runtime does not support ROCm/AMD passthrough"
    if runtime != "podman":
        return False, f"unsupported runtime: {runtime}"

    # amdgpu kernel module loaded? (cheap, no subprocess)
    if not Path("/sys/module/amdgpu").exists():
        return False, "amdgpu kernel module not loaded"

    # Mandatory compute interface, shared by all GPUs.
    if not Path("/dev/kfd").exists():
        return False, "no /dev/kfd on host"

    # At least one DRI render node.
    if not any(Path("/dev/dri").glob("renderD*")):
        return False, "no /dev/dri render node on host"

    # Functional enumeration via rocminfo, when present, catches the
    # blacklisted/unsupported-GPU false-negative.  rocminfo's banner
    # and agent list are the AMD analog of `nvidia-smi -L`.  rocminfo
    # ignores argv, so no flags.  Absence of rocminfo is NOT fatal:
    # the device nodes above are the real precondition and ROCm
    # userspace lives in the image, not on the host.
    rocminfo = shutil.which("rocminfo")
    if rocminfo:
        try:
            probe = subprocess.run([rocminfo], capture_output=True, timeout=5)
        except (OSError, subprocess.SubprocessError) as e:
            return False, f"rocminfo failed to run ({e})"
        if probe.returncode != 0:
            return False, "rocminfo reported no GPUs"

    return True, None
```

Notes baked into the stub from verified claims: `/sys/module/amdgpu` presence (confirmed #9), `/dev/kfd` always required (confirmed #10/#22/#24), renderD glob (confirmed #12), rocminfo as enumeration (confirmed #13/#18), rocminfo ignores argv so **no `--support`** (refuted #20). We do **not** require `/etc/cdi/amd.json` in the default path — CDI is optional (confirmed #8); a `mode: "cdi"` variant would additionally require `/etc/cdi/amd.json` or `/var/run/cdi/amd.json`.

### 5.3 run_cmd injection stub (`run_cmd.py`, after the NVIDIA block ~1645)

```python
elif gpu_enabled and gpu_vendor == "amd":
    gpu_config = config.get("gpu", {})
    gpu_devices = gpu_config.get("devices", "all")
    gpu_mode = gpu_config.get("mode", "devices")

    if gpu_mode == "cdi":
        # AMD CDI: amd.com/gpu=all | amd.com/gpu=N  (spec at /etc/cdi/amd.json)
        if gpu_devices == "all":
            run_cmd.extend(["--device", "amd.com/gpu=all"])
        else:
            for idx in gpu_devices.split(","):
                run_cmd.extend(["--device", f"amd.com/gpu={idx.strip()}"])
    else:
        # Default: raw device nodes (no host toolkit needed).
        # /dev/kfd is the shared compute interface and is ALWAYS required.
        if Path("/dev/kfd").exists():
            run_cmd.extend(["--device", "/dev/kfd"])
        if gpu_devices == "all":
            run_cmd.extend(["--device", "/dev/dri"])
        else:
            for idx in gpu_devices.split(","):
                node = Path(f"/dev/dri/renderD{128 + int(idx.strip())}")
                if node.exists():
                    run_cmd.extend(["--device", str(node)])

    # Rootless podman drops supplementary groups; keep-groups (crun-only)
    # preserves the host render/video GID so /dev/kfd is openable.  This
    # is REQUIRED for both modes and is why AMD stays on crun (not runc).
    if runtime == "podman":
        run_cmd.extend(["--group-add", "keep-groups"])

    # ROCm in-container selectors (NOT a security boundary).  No
    # NVIDIA_DRIVER_CAPABILITIES analog exists — omit it.
    run_cmd.extend([
        "-e", f"ROCR_VISIBLE_DEVICES={gpu_devices}",
        "-e", f"HIP_VISIBLE_DEVICES={gpu_devices}",
    ])
    gfx = gpu_config.get("hsa_override_gfx_version")
    if gfx:
        run_cmd.extend(["-e", f"HSA_OVERRIDE_GFX_VERSION={gfx}"])
    if gpu_config.get("seccomp_unconfined") is True:
        run_cmd.extend(["--security-opt", "seccomp=unconfined"])

    console.print(
        f"[dim]ROCm passthrough (mode={gpu_mode}): devices={gpu_devices}[/dim]"
    )
```

(The existing `if gpu_enabled:` at 1620 becomes `if gpu_enabled and gpu_vendor == "nvidia":`, with the AMD `elif` above. `gpu_vendor` is read once at the gating site ~1280.)

### 5.4 check_cmd diagnostics (`check_cmd.py:902`)

Branch the header on vendor. For AMD emit a "GPU (AMD/ROCm)" block that, with the same `ok/warn/fail` helpers:
- macOS → `warn("ROCm passthrough is not supported on macOS")` and skip.
- `shutil.which("rocminfo")` → run it, `ok` per detected GPU agent; else `fail("rocminfo not found", "Install ROCm: https://rocm.docs.amd.com/projects/install-on-linux/")`. (`rocm-smi`/`amd-smi` as secondary signals.)
- `amdgpu` module: `Path("/sys/module/amdgpu").exists()` (skip inside jail via `YOLO_VERSION`, like KVM).
- Device-node + group membership: reuse the KVM idiom (`os.access`, `grp.getgrgid`, `os.getgroups()`) on `/dev/kfd` and each `/dev/dri/renderD*` — typically `render` group; `ok`/`warn`(newgrp)/`fail`(usermod -aG render).
- podman → `ok("Podman will preserve render/video group via --group-add keep-groups")`.
- `mode: "cdi"` only: search `/etc/cdi/amd.json` + `/var/run/cdi/amd.json`; if absent `fail("No AMD CDI spec", "Generate with: sudo amd-ctk cdi generate --output=/etc/cdi/amd.json")`. There is **no first-party nvidia-ctk analog beyond amd-ctk** — do not invent one; the staleness check can use `amd-ctk cdi validate` (note: no auto-refresh service exists, recommend manual regen). Mark these host commands needs-verification.
- Trailing `console.print()` blank line; counters flow through the nonlocal helpers.

### 5.5 Probe unit-test stub (`tests/test_cli_unit.py`, mirror `TestGpuHostAvailable`)

```python
class TestRocmHostAvailable:
    def _mock_probe(self, monkeypatch, *, is_macos, rocminfo, rocminfo_rc,
                    kfd, renderd, amdgpu_mod):
        monkeypatch.setattr("cli.loopholes_runtime.IS_MACOS", is_macos)
        monkeypatch.setattr(
            "cli.loopholes_runtime.shutil.which",
            lambda cmd: "/usr/bin/rocminfo" if (cmd == "rocminfo" and rocminfo) else None,
        )
        def fake_run(*a, **k):
            m = MagicMock(); m.returncode = rocminfo_rc; m.stdout = b""; return m
        monkeypatch.setattr("cli.subprocess.run", fake_run)  # verify import path!
        real_exists = Path.exists
        real_glob = Path.glob
        def fake_exists(self):
            s = str(self)
            if s == "/sys/module/amdgpu": return amdgpu_mod
            if s == "/dev/kfd": return kfd
            return real_exists(self)
        def fake_glob(self, pat):
            if str(self) == "/dev/dri" and pat == "renderD*":
                return iter([Path("/dev/dri/renderD128")]) if renderd else iter([])
            return real_glob(self, pat)
        monkeypatch.setattr(Path, "exists", fake_exists)
        monkeypatch.setattr(Path, "glob", fake_glob)
    # cases: macOS→'does not support'; 'container'→'does not support';
    # unknown runtime→'unsupported runtime'; no amdgpu module; no /dev/kfd;
    # no renderD; rocminfo rc!=0→'reported no GPUs'; all present→(True,None).
```

**Test pitfalls (from the tests-area map):** the fake `Path.exists`/`glob` allowlist must cover *every* ROCm path the probe touches, because on a real AMD CI host `/dev/kfd` etc. genuinely exist (host-dependent green/red otherwise). Patch `shutil.which` AND `Path.exists`/`glob` AND `subprocess.run` together. Confirm the actual `subprocess` import path before choosing the patch target (`cli.subprocess.run` vs `cli.loopholes_runtime.subprocess.run`) — the existing NVIDIA tests patch `cli.subprocess.run`, so verify before mirroring. For the positive argv test, assert `mock_popen.called` first to avoid a vacuous pass, and assert on the string `keep-groups`/`render`, never a hard-coded numeric GID.

---

## 6. In-jail-NOW vs host-agent-LATER split

**This is the critical operational constraint.** The implementing engineer is *inside* the jail: no AMD GPU, cannot restart the jail, no host root — but `/workspace` edits are immediately visible to the host. A separate host-side agent with real AMD hardware comes later.

### Bucket A — doable entirely inside this jail now

| Task | Why it's in-jail-safe |
|------|------------------------|
| `KNOWN_GPU_KEYS` + validation (`config.py`) | Pure Python logic; unit-testable with dict inputs. |
| `_rocm_host_available()` (`loopholes_runtime.py`) | Pure function over `shutil.which`/`Path.exists`/`subprocess.run`; fully mockable. Its real return on this host will be `(False, "amdgpu kernel module not loaded")`, which is correct. |
| Export wiring (`__init__.py`, `run_cmd.py` import) | Mechanical. |
| run_cmd argv construction (vendor gating, AMD injection block, runc-branch gating) | Builds an argv list; never executes podman. Verifiable by reading `mock_popen.call_args`. |
| `check_cmd` AMD diagnostics block | Renders text via `ok/warn/fail`; on this host shows "amdgpu not loaded"/"rocminfo not found", which is the correct in-jail output. |
| All docs (`config_ref_cmd.py`, USER_GUIDE, platform-comparison, macos, sandbox-comparison, yolo-jail.jsonc, README) | Text edits; mark every unverified host command needs-verification. |
| Unit tests: `TestRocmHostAvailable`, `gpu`-schema validation, `TestRunRocm` argv asserts, macOS-skip AMD case, requested-but-unavailable warning | Tests mock the host probe (`IS_MACOS`, `which`, `subprocess.run`, `Path.exists`/`glob`) and assert on argv/strings — no real GPU needed. Backfill the currently-missing positive NVIDIA argv/validation tests in the same pass. |
| Nested-jail smoke test (`yolo -- bash`) for argv assembly | Confirms the new branch doesn't crash container startup. With `gpu.vendor=amd` enabled but no AMD hardware, the probe returns `(False, ...)` so it exercises the warn-and-continue path end-to-end without a GPU. |

### Bucket B — requires host-side agent / real AMD hardware

| Task | Why it cannot be done in-jail |
|------|-------------------------------|
| Install AMD Container Toolkit (`apt install amd-container-toolkit` from repo.radeon.com) | Needs host root + package manager; jail has no sudo and cannot install host packages. |
| `sudo amd-ctk cdi generate --output=/etc/cdi/amd.json`; verify `amd-ctk cdi list` shows `amd.com/gpu=all` | Needs real AMD GPUs to scan + root to write `/etc/cdi`; no GPU in jail. |
| Verify `rocminfo` / `rocm-smi` / `amd-smi list` enumerate GPUs | Requires the `amdgpu` driver loaded against physical hardware — absent in jail. |
| End-to-end PyTorch-ROCm smoke test (`torch.cuda.is_available()` on a `rocm/*` image) | Requires a working GPU + ROCm userspace image actually executing on hardware. |
| Confirm rootless crun + `keep-groups` actually opens `/dev/kfd` on a real consumer Radeon | The permission behavior (userns GID offset, crun skipping setgroups) only manifests against a real group-owned device node. |
| Determine whether `--security-opt seccomp=unconfined` / `label=disable` / `setsebool container_use_devices` are needed on the target host | SELinux/seccomp behavior is host-policy-dependent and only observable on real hardware. |
| Confirm whether AMD `mode: "cdi"` hits any crun+CDI bug (and whether it can stay on crun) | Needs CDI injection against real hardware under crun. |
| Inspect a generated `/etc/cdi/amd.json` to confirm it injects only device nodes (no host libs) | Code review of the generator already strongly indicates nodes-only (refuted #28), but a hardware-generated spec is the authoritative confirmation. |
| Verify exact render/video group requirement and renderD↔GPU mapping on the target card | Group ambiguity (#17 uncertain) and partition-mode renderD numbering can only be resolved on hardware. |

**Handover note for Bucket B:** all in-jail code fails *soft* — a config with `gpu.vendor=amd, enabled=true` on a non-AMD host warns ("ROCm requested but ... — starting without GPU passthrough") and starts without GPU flags. So Bucket A can be merged safely before any Bucket B verification.

---

## 7. Open Questions

1. **podman#27483 / NVIDIA-runc premise (uncertain, #16).** The keep-groups⇒crun-only fact is solid, but the *specific* issue reference for NVIDIA's runc workaround could not be verified (the closest real issue is crun#1908, NVIDIA-CDI + keep_id, no runc workaround documented). The design's conclusion ("AMD must stay on crun; gate the runc branch NVIDIA-only") holds regardless. *Resolve:* a host agent (or anyone with podman) should confirm the exact podman/crun issue number behind yolo's existing NVIDIA runc pin, and confirm whether AMD `mode: "cdi"` under crun hits any analogous CDI-injection failure.
2. **render vs video group (uncertain, #17).** Compute is `render`-owned on modern distros; some setups/apps want `video` too; card nodes are `video`. We default to `keep-groups` (covers both), but a least-privilege explicit-`--group-add <render-gid>` mode is desirable. *Resolve:* host agent confirms on the target card which group `/dev/kfd` + renderD* are owned by, and whether `keep-groups` vs explicit GID is more reliable there.
3. **`mode` for `vendor=nvidia`: error or ignore?** Spec says error; an argument for silently ignoring exists. *Resolve:* pick one for consistency with how other vendor-specific keys are treated.
4. **HSA_OVERRIDE_GFX_VERSION mappings (not adversarially verified).** The value→gfx table (`11.0.0`→gfx1100, etc.) comes from research not re-verified by fact-check. *Resolve:* document as best-effort/same-arch-only; host agent confirms the value needed for the actual card.
5. **ROCm host/userspace version window.** AMD guarantees ±1 year (≥6.4.0) driver↔userspace compat (confirmed #25). Should `check` warn when the host driver and image ROCm are outside the window? *Resolve:* host agent decides whether a staleness warning is worth the complexity (no auto-refresh service exists for AMD CDI either, claim C11).
6. **Default image steering.** Should yolo warn (or refuse) when `gpu.vendor=amd` is enabled with a non-`rocm/*` base image, since a generic image gets device nodes but no working HIP (confirmed #0/#3)? *Resolve:* product decision — warn vs hard error vs silent.

### 7.1 Resolved on hardware (2026-06-05)

Verified on an **AMD Radeon 8060S (gfx1151, Strix Halo APU; PCI `1002:1586`)**, host ROCm 7.2.3, image ROCm 7.2.4, rootless podman 5.8.2 + crun 1.27.1, Arch Linux (no SELinux). Tested with `rocm/dev-ubuntu-24.04` and `rocm/pytorch` images, replaying the exact argv yolo emits (including its full userns flag set: identity uidmap, `SYS_ADMIN`, `MKNOD`, `label=disable`, `/dev/fuse`).

**Two real bugs were found and fixed during verification** (neither was observable in-jail):

- **`ROCR_VISIBLE_DEVICES=all` hides the GPU.** The original injection set `ROCR_VISIBLE_DEVICES={devices}` / `HIP_VISIBLE_DEVICES={devices}` unconditionally. Unlike NVIDIA's `NVIDIA_VISIBLE_DEVICES`, the ROCr/HSA selector does **not** accept the literal `"all"` — it matches no device, so `torch.cuda.is_available()` returns `False` and `rocminfo` shows only the CPU agent. Since `devices` defaults to `"all"`, **the default AMD config shipped a GPU-less container.** Fix (`run_cmd.py`): omit both env vars when `devices == "all"` (ROCm's own "all visible" default); emit them only for explicit indices/UUIDs. (The design §4.3 had hedged "skip when `devices=="all"` is fine, or pass `all`" — the "skip" branch is the correct one.)
- **`yolo check` mislabeled non-GPU HSA agents as GPUs.** `rocminfo` enumerates every HSA agent — the CPU, and on this APU an NPU/DSP (`RyzenAI-npu5`) — and the check reported each `Marketing Name:` as "GPU detected". Fix (`check_cmd.py`): only report agents whose `Device Type:` is `GPU`.

Resolutions to the questions above:

1. **runc premise / AMD CDI under crun — RESOLVED.** AMD `mode: "cdi"` (`--device amd.com/gpu=all` + `--group-add keep-groups`) runs ROCm correctly under the **default crun** runtime — no crun+CDI injection failure, so AMD needs no `runc` workaround for either mode. The NVIDIA runc pin's exact issue number remains unconfirmed but is moot for AMD. The "AMD stays on crun, runc branch gated `vendor=="nvidia"`" decision is confirmed correct on hardware.
2. **render vs video group — PARTIALLY RESOLVED.** On this host `/dev/kfd` + `/dev/dri/renderD128` are `crw-rw-rw-` (mode `0666`, world-writable) and the user was in **neither** `render` nor `video` — device `open()` succeeded regardless, and `--group-add keep-groups` worked. So the group mechanism is correct but could not be stress-tested against the `0660 root:render` case here. `keep-groups` (covers both groups, no hard-coded GID) remains the right default; an explicit least-privilege `--group-add <gid>` mode is still optional future work. The CDI spec generator emitted gids `987` (render) for kfd/renderD and `983` (video) for the card node — confirming render owns the compute nodes.
3. **`mode` for `vendor=nvidia`: error — UNCHANGED.** Kept as an error (consistency call; no hardware bearing).
4. **HSA_OVERRIDE_GFX_VERSION — RESOLVED for this card.** gfx1151 is natively supported by ROCm 7.2.4, so **no override was needed**. The knob remains best-effort/same-arch-only for genuinely unsupported consumer GPUs; the value table is still not adversarially verified.
5. **Version window — NO CHANGE RECOMMENDED.** Host 7.2.3 vs image 7.2.4 (within AMD's ±1yr window) worked fine. A staleness warning in `check` is not worth the complexity; left unimplemented.
6. **Default image steering — NO CHANGE.** Confirmed a generic image gets device nodes but no HIP. Left as a documentation steer (USER_GUIDE points users at `rocm/*` images); no warn/refuse added.

**Confirmed properties (no change needed):**
- The make-or-break path works: rootless crun + `keep-groups` + raw device nodes opens `/dev/kfd` and runs real compute (HIP vector-add kernel + `torch.mm`), including under yolo's full userns/cap flag set.
- The generated `/etc/cdi/amd.json` (CDI spec v0.6.0) injects **only `deviceNodes`** — no env vars, hooks, or host-library mounts (top-level `containerEdits` is `{}`). Confirms refuted #28.
- `amd-ctk cdi list` maps `amd.com/gpu=0` → `/dev/dri/renderD128` (confirms #14).
- Basic ROCm compute works with the **default seccomp profile enabled** — `seccomp=unconfined` is not required (confirms #11/#27). No SELinux on this host, so `container_use_devices` was not exercised.

### 7.2 Locked-memory limit blocks queue creation in-jail — **RESOLVED by ROCm 7.2 userspace (2026-06-06)**

> **UPDATE (2026-06-06, verified on hardware):** The memlock blocker described below was specific to
> the **nixpkgs-built ROCm 7.1.1** userspace the in-jail agent used. Re-tested on the same GPU host
> with **ROCm 7.2.4** images (`rocm/dev-ubuntu-24.04`, `rocm/pytorch`): the `hip_smoke` gfx1151 saxpy
> kernel returns **`RESULT: PASS`** at the **current 8 MB** host cap — and continues to pass with the
> `--ulimit` clamped as low as **64 KB**. No `AMDKFD_IOC_CREATE_QUEUE EINVAL`, no failing kfd ioctls;
> confirmed real GPU execution (a gfx900-built binary segfaults on the gfx1151 hardware while the
> gfx1151 build passes with correct numerics). So **newer ROCm userspace no longer pins a >8 MB queue
> ring buffer**, and *raising the host memlock cap is not required* on this host.
>
> Consequences (landed 2026-06-06):
> - **No host change made.** Leaving the host hard cap at 8 MB avoids an unnecessary
>   unlimited-locked-memory DoS vector for zero functional benefit.
> - **The misleading warning was removed.** `yolo run`'s low-cap warning and `yolo check`'s
>   "GPU locked-memory limit" section claimed "GPU queue creation needs ~16 MB; raise the host cap" —
>   factually wrong on ROCm 7.2 and it nudged users toward weakening host security. Both were deleted.
> - **The `--ulimit memlock=<host-hard>:<host-hard>` clamp was kept** in `run_cmd.py` — it harmlessly
>   lifts the container's *soft* limit to the host ceiling (the most a rootless container can get) and
>   never bricks startup. Tests retained, warning assertions dropped.
> - **Side-note correction:** on this host (crun 1.27.1) `--ulimit memlock=-1:-1` (unlimited) does
>   **not** brick rootless startup — crun accepts `-1`. It rejects a *finite* value above the cap
>   (16 MB → EPERM). So the stale installed yolo (`596ad4a`, unconditional `-1`) would have started
>   fine here; the clamp fix remains the safer cross-host choice.
>
> The original 7.1.1-era diagnosis is preserved below for the record.

---

#### Original diagnosis (ROCm 7.1.1 nixpkgs userspace, 2026-06-05)

A follow-up test running ROCm **inside a persistent yolo jail** (same GPU; jail kernel `7.0.7-zen`, nixpkgs ROCm 7.1.1 userspace) surfaced a blocker that the §7.1 host argv-replay did **not** hit: **GPU kernel dispatch fails at command-queue creation.** `strace`/`gdb` traced it to ground truth:

```
ioctl(AMDKFD_IOC_CREATE_QUEUE, ...) = -1 EINVAL   ← only failing call
→ ROCr cleanup path null-derefs → SIGSEGV
```

**Root cause:** KFD must pin (mlock) a **~13.3 MB queue ring buffer** to create a command queue, but the container's `RLIMIT_MEMLOCK` is capped at **8 MB**. The cap is kernel-hard — `ulimit -l unlimited` silently fails inside the jail — so pin > memlock ⇒ `CREATE_QUEUE` EINVAL. Everything *up to* dispatch works (device open, `rocminfo`, KFD version negotiation, `hipMalloc`, H2D `hipMemcpy`); only queue creation fails. This is the well-known container-GPU requirement; AMD's docs prescribe `--ulimit memlock=-1`.

**Why §7.1 didn't see it:** §7.1 verified by replaying yolo's argv as a fresh host `podman run`, which inherited the host shell's higher locked-memory limit. The *persistent* jail process inherits the restrictive 8 MB cap instead. Both observations are real; they differ only in the inherited `RLIMIT_MEMLOCK`.

**The non-obvious constraint (empirically established):** `--ulimit memlock=-1` (unlimited) is what AMD's *rootful* Docker docs prescribe, but a **rootless** podman container **cannot raise `RLIMIT_MEMLOCK` above the host process's hard cap** — `crun` calls `setrlimit` and gets `EPERM`, and the container **fails to start**. Verified in a nested jail on this host (hard cap 8 MB):

```
podman run --ulimit memlock=-1:-1      → crun: setrlimit RLIMIT_MEMLOCK: Operation not permitted
podman run --ulimit memlock=16MB:16MB  → same EPERM (any value > host hard cap)
podman run --ulimit memlock=8MB:8MB    → OK (== host hard cap)
```

So an unconditional `memlock=-1` is not a safe default — it would **brick `yolo run`** for every GPU user on a rootless host whose hard cap is finite (the common default).

**Fix (landed, `run_cmd.py`) — adaptive, not unconditional:** when `gpu_enabled` (either vendor), read the host hard cap via `resource.getrlimit(RLIMIT_MEMLOCK)` (yolo runs on the host, so it sees the real ceiling) and emit `--ulimit memlock=<hard>:<hard>` — or `memlock=-1:-1` only when the host cap is already unlimited. When the host cap is below ~16 MB, `yolo run` prints a warning (the container will start, but GPU queue creation may still fail), and `yolo check` reports the same as a `warn`. Covered by `TestRunRocm` (`test_rocm_memlock_clamped_to_finite_host_cap`, `test_rocm_memlock_unlimited_when_host_allows`). Seccomp was **ruled out** as the cause (`seccomp=unconfined` → `Seccomp: 0`, still crashed identically).

**Still requires host action — the jail cannot fix this itself.** Because a rootless jail can't exceed the host cap, the *actual* unblock is to **raise the host's memlock hard limit** (`limits.conf` `hard memlock unlimited`, systemd `LimitMEMLOCK=infinity`, or podman `containers.conf` `default_ulimits = ["memlock=-1:-1"]`) and update the GPU host's yolo to this branch. (The step-by-step lived in a
since-archived handoff, `rocm-memlock-handoff.md`, resolved 2026-06-06 and
verified on the GPU host — see git history if you need the original notes.) The
reported "8192 after restart" almost certainly means the GPU host was still
running an **old yolo** with no memlock flag at all — the new code was never
deployed there.

**Resolved (see the 2026-06-06 update at the top of this section):** the end-to-end `hip_smoke` run reaches `CREATE_QUEUE` success and `RESULT: PASS` at the 8 MB cap with ROCm 7.2.4 userspace — the memlock fix is moot for current ROCm and no host change is needed. The onnxruntime execution-provider path (gfx1151 code objects / migraphx asserts-LLVM) is a separate downstream item tracked in `docs/research/rocm-gpu-jail-findings.md`.

---

## 8. Sources

Most authoritative URLs (deduped) backing the confirmed claims:

**AMD ROCm official docs**
- https://rocm.docs.amd.com/projects/install-on-linux/en/latest/how-to/docker.html (minimal flags, /dev/kfd + /dev/dri, seccomp optional)
- https://rocm.docs.amd.com/projects/install-on-linux/en/latest/install/prerequisites.html (render/video group setup)
- https://rocm.docs.amd.com/en/latest/compatibility/compatibility-matrix.html (±1yr driver/userspace window)
- https://rocm.docs.amd.com/en/latest/conceptual/gpu-isolation.html (ROCR/HIP_VISIBLE_DEVICES not a security boundary)
- https://rocm.docs.amd.com/projects/rocminfo/en/latest/how-to/use-rocminfo.html (HSA agent enumeration)
- https://rocm.docs.amd.com/projects/amdsmi/en/latest/how-to/amdsmi-cli-tool.html (amd-smi list)

**AMD Container Toolkit (CDI)**
- https://github.com/ROCm/container-toolkit (repo, Apache-2.0; amd-ctk + amd-container-runtime)
- https://github.com/ROCm/container-toolkit/blob/main/README.md
- https://raw.githubusercontent.com/ROCm/container-toolkit/main/internal/cdi/cdi.go (generator: DeviceNodes only, no env/hooks/mounts)
- https://instinct.docs.amd.com/projects/container-toolkit/en/latest/container-runtime/cdi-guide.html (/etc/cdi/amd.json)
- https://instinct.docs.amd.com/projects/container-toolkit/en/latest/container-runtime/running-workloads.html (amd.com/gpu=<entry>, podman keep-groups note)
- https://instinct.docs.amd.com/projects/container-toolkit/en/latest/container-runtime/release-notes.html (v1.0.0 June 2025 → v1.3.0)

**Podman / rootless groups**
- https://docs.podman.io/en/latest/markdown/podman-run.1.html (keep-groups: crun-only, exclusive)
- https://www.redhat.com/en/blog/files-devices-podman (setgroups / keep_original_groups mechanism)
- https://www.redhat.com/en/blog/supplemental-groups-podman-containers
- https://docs.redhat.com/en/documentation/red_hat_ai_inference_server/3.2/html/getting_started/inference-rhaiis-with-podman-amd-rocm_getting-started (verbatim rootless --device /dev/kfd --device /dev/dri --group-add keep-groups)

**Security**
- https://www.wiz.io/blog/nvidia-ai-vulnerability-deep-dive-cve-2024-0132 + https://github.com/NVIDIA/libnvidia-container/security/advisories/GHSA-q2v4-jw5g-9xxj (CVE-2024-0132 hook/bind-mount path; CDI not impacted)
- https://nvd.nist.gov/vuln/detail/CVE-2026-43206 (kfd_event_page_set OOB write; restrict /dev/kfd)
- https://nvd.nist.gov/vuln/detail/CVE-2026-46197 + https://nvd.nist.gov/vuln/detail/CVE-2025-21940 (KFD ioctl LPE surface)

**Driver detection / device model**
- https://github.com/ROCm/rocm_smi_lib/blob/master/python_smi_tools/rocm_smi.py (/sys/module/amdgpu/initstate "live")
- https://raw.githubusercontent.com/ROCm/rocminfo/master/rocminfo.cc (banner strings; main() ignores argv → no --support flag)
