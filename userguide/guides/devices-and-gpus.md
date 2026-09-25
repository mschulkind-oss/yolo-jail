# Devices and GPUs

## Device Passthrough

**Platform support:** Device passthrough (USB, serial, cgroup rules) is a **Linux-only** feature. It relies on the host kernel exposing `/dev/bus/usb/`, `/dev/tty*`, and `--device-cgroup-rule` — none of which exist on macOS where containers run inside a VM. On macOS, device entries in `yolo-jail.jsonc` are parsed, logged as skipped with a warning, and do not prevent the jail from starting.

On Linux, pass host devices (USB, serial, etc.) into the jail:

```jsonc
{
  "devices": [
    {"usb": "0bda:2838", "description": "RTL-SDR"},
    "/dev/ttyUSB0",
    {"cgroup_rule": "c 189:* rwm"}
  ]
}
```

**Formats:**
- **USB by vendor:product ID** (preferred — stable across reboots): `{"usb": "0bda:2838"}`
- **Raw device path** (changes on replug): `"/dev/bus/usb/001/004"`
- **Cgroup rule** (broad access): `{"cgroup_rule": "c 189:* rwm"}`

Missing devices produce a warning but don't prevent the jail from starting. Device changes are subject to [config safety](../reference/configuration.md#config-safety) approval.

---

## GPU Passthrough (NVIDIA)

**Platform support:** GPU passthrough is **Linux-only**. Apple Silicon Macs use Metal, not CUDA/OpenCL, and Apple's Virtualization.framework doesn't expose the GPU to the guest Linux kernel. If `"gpu": {"enabled": true}` appears in `yolo-jail.jsonc` on macOS, it is parsed, logged as skipped with a warning, and does not prevent the jail from starting. For GPU workflows on macOS, run on a Linux box (local or EC2 `g5`/`p3` instance) instead.

On Linux, train deep learning models inside the jail using NVIDIA GPUs. Requires the NVIDIA Container Toolkit on the host.

### Host Setup

1. **Verify your GPU driver:**
   ```bash
   nvidia-smi
   ```

2. **Install the NVIDIA Container Toolkit:**
   ```bash
   curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey \
     | sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
   curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list \
     | sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' \
     | sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list
   sudo apt-get update && sudo apt-get install -y nvidia-container-toolkit
   ```

3. **Configure the container runtime:**
   ```bash
   # Podman (CDI)
   sudo nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml
   ```

4. **Validate:**
   ```bash
   yolo check   # GPU section will show nvidia-smi, nvidia-ctk, CDI spec status
   ```

### Jail Configuration

```jsonc
// yolo-jail.jsonc
{
  "gpu": {
    "enabled": true,
    "devices": "all",
    "capabilities": "compute,utility"
  }
}
```

| Key | Default | Description |
|-----|---------|-------------|
| `enabled` | `false` | Enable GPU passthrough |
| `devices` | `"all"` | `"all"`, or specific GPUs: `"0"`, `"0,1"`, `"GPU-<uuid>"` |
| `capabilities` | `"compute,utility"` | NVIDIA driver capabilities to expose |

### Installing PyTorch

Once inside the GPU-enabled jail:

```bash
pip install torch torchvision
python -c "import torch; print(torch.cuda.is_available(), torch.cuda.get_device_name(0))"
```

### Runtime Details

| Runtime | Mechanism | Notes |
|---------|-----------|-------|
| **Podman** | `--device nvidia.com/gpu=all` (CDI) | Requires CDI spec at `/etc/cdi/nvidia.yaml` |

- **Podman:** the NVIDIA branch passes identity uid/gid maps (`0:0:1` plus `1:1:65536`) and `--runtime runc`, because crun has CDI bugs. It also omits `/dev/fuse`, which the ordinary branch passes.
- **Nested podman-in-podman with GPU is not supported, and is not currently *prevented*:** the nesting branch is chosen first, so a nested GPU launch gets the nesting flags while the CDI device flags are still emitted. Don't rely on it.
- **Shared memory:** `/dev/shm` is a tmpfs sized 2 GB (`--tmpfs /dev/shm:size=2g`, not `--shm-size`) for PyTorch multi-process data loading. On Apple Container it is a tmpfs with no size argument, so the guest default applies.
- **CUDA forward compatibility:** CUDA in the container can be newer than the host driver, but not the reverse.

### AWS EC2

Use an AWS Deep Learning AMI (DLAMI) — drivers and toolkit come pre-installed.

| Instance | GPU | VRAM | Use Case | $/hr (approx) |
|----------|-----|------|----------|----------------|
| g4dn.xlarge | 1× T4 | 16 GB | Inference, light training | ~$0.53 |
| g5.xlarge | 1× A10G | 24 GB | Training + inference | ~$1.01 |
| p3.2xlarge | 1× V100 | 16 GB | Training | ~$3.06 |

### Troubleshooting GPU

- **`conmon bytes "": readObjectStart` error (Podman):** Caused by `crun` OCI runtime's CDI handling bug ([podman#27483](https://github.com/containers/podman/issues/27483)). The jail automatically uses `--runtime runc` when GPU is enabled to work around this. If you see this, update your `yolo-jail` installation.
- **`nvidia-smi` not found inside jail:** The NVIDIA Container Toolkit injects driver libs at container start. Check the toolkit is installed and configured on the host.
- **CUDA out of memory:** Reduce batch size, or limit which GPUs are exposed with `"devices": "0"`.

---

## AMD GPU Passthrough (ROCm)

**Platform support:** Like NVIDIA, AMD/ROCm passthrough is **Linux-only**. Apple Silicon Macs have no ROCm path, and Apple's Virtualization.framework doesn't expose the GPU to the guest. If `"gpu": {"enabled": true, "vendor": "amd"}` appears in `yolo-jail.jsonc` on macOS, it is parsed, logged as skipped with a warning, and does not prevent the jail from starting.

On Linux, run ROCm compute workloads inside the jail using AMD GPUs. Unlike NVIDIA, the default path needs **no host container toolkit** — just the `amdgpu` kernel driver and the right group membership. ROCm passthrough works on both AMD Instinct and consumer Radeon hardware.

> **Note:** ROCm userspace (HIP, rocm-smi, math libs) is **not** injected from the host. Unlike the NVIDIA toolkit, AMD's device-node and CDI paths inject **only kernel device nodes** (`/dev/kfd`, `/dev/dri/renderD*`). Your container image must ship its own ROCm userspace — use a `rocm/*` base image (see the PyTorch example below).

### Host Setup

> Verified end-to-end on real AMD hardware (Radeon 8060S / gfx1151, ROCm 7.2, rootless podman + crun) — both the default device-node mode and CDI mode run ROCm PyTorch inside the jail. Package names and exact group names still vary by distribution, so adapt the commands below to yours.

1. **Install the `amdgpu` kernel driver.** On Ubuntu, AMD ships `amdgpu-dkms` via the ROCm `amdgpu-install` tooling (other distros package it directly, e.g. Arch's `linux*-headers` + mainline `amdgpu`):
   ```bash
   sudo amdgpu-install --usecase=dkms
   ```
   Confirm the module is loaded:
   ```bash
   ls /sys/module/amdgpu        # present when the driver is loaded
   ls /dev/kfd /dev/dri/renderD*  # compute + render device nodes
   ```

2. **Ensure your host user can open the device nodes.** `/dev/kfd` and `/dev/dri/renderD*` are commonly owned by the `render` group (`/dev/dri/card*` by `video`), in which case your host user must be a member:
   ```bash
   sudo usermod -aG render,video "$USER"
   # log out and back in (or `newgrp render`) for the new groups to take effect
   ```
   Some distributions instead ship these nodes world-readable/writable (mode `0666`), in which case no group membership is needed — `yolo check` reports whether the nodes are openable by the current user either way. `--group-add keep-groups` (which yolo always adds) preserves whatever host groups you already hold into the rootless container.

3. **(Optional) AMD Container Toolkit for CDI mode.** The default `mode: "devices"` needs no toolkit. Only if you want CDI-style per-GPU selection (`mode: "cdi"`), install the AMD Container Toolkit and generate a spec:
   ```bash
   sudo amd-ctk cdi generate --output=/etc/cdi/amd.json
   amd-ctk cdi list   # should list amd.com/gpu=all, amd.com/gpu=0, ...
   ```
   The generated spec injects **only device nodes** (no env vars, hooks, or host-library mounts — verified), and CDI mode runs ROCm correctly under the default crun runtime (no `runc` workaround needed). On a single-GPU host CDI offers no advantage over the default device-node mode.

4. **Validate:**
   ```bash
   yolo check   # GPU section shows amdgpu module, rocminfo, device nodes, group membership
   ```

### Jail Configuration

```jsonc
// yolo-jail.jsonc
{
  "gpu": {
    "enabled": true,
    "vendor": "amd",
    "devices": "all"
  }
}
```

| Key | Default | Description |
|-----|---------|-------------|
| `vendor` | `"nvidia"` | Set to `"amd"` for ROCm passthrough. Absent ⇒ NVIDIA (backward-compatible). |
| `enabled` | `false` | Enable GPU passthrough |
| `devices` | `"all"` | `"all"`, or specific GPUs: `"0"`, `"0,1"` |
| `mode` | `"devices"` | AMD only. `"devices"` = raw device nodes (no host toolkit); `"cdi"` = `amd.com/gpu` via the AMD Container Toolkit |
| `hsa_override_gfx_version` | _(unset)_ | AMD only, optional. Override the gfx target for unsupported/consumer GPUs (e.g. `"11.0.0"`) |
| `seccomp_unconfined` | `false` | AMD only, optional. Opt-in `--security-opt seccomp=unconfined` (only needed for some HPC/numactl workloads; widens the syscall surface) |

> `capabilities` is **NVIDIA-only** — ROCm has no driver-capabilities concept, so it is rejected for `vendor: "amd"`.

### Installing PyTorch (ROCm)

ROCm userspace ships in the image, so start from a `rocm/*` base image that already includes a ROCm-built PyTorch (for example a `rocm/pytorch` image), or install the ROCm wheels from AMD's index inside such an image (pick the `rocmX.Y` index matching the image's ROCm version):

```bash
# inside a rocm/* based jail image
pip install torch --index-url https://download.pytorch.org/whl/rocm6.2
python -c "import torch; print(torch.cuda.is_available(), torch.cuda.get_device_name(0))"
# verified output on a Radeon 8060S (gfx1151): True Radeon 8060S Graphics
```

ROCm exposes the AMD GPU through PyTorch's `torch.cuda` API, so `torch.cuda.is_available()` returning `True` means ROCm is working. A generic (non-ROCm) image will get working device nodes but **no working HIP/rocm-smi**.

### Runtime Details

| Runtime | Mechanism | Notes |
|---------|-----------|-------|
| **Podman** | `--device /dev/kfd` + `--device /dev/dri/renderD*` (or `--device /dev/dri` for `all`) + `--group-add keep-groups` | Default `mode: "devices"`; no host toolkit required |
| **Podman (CDI)** | `--device amd.com/gpu=all` + `--group-add keep-groups` | `mode: "cdi"`; requires `/etc/cdi/amd.json` from `amd-ctk` |

- **Runtime:** AMD stays on the default **crun** runtime. `--group-add keep-groups` is crun-only — it preserves the host `render`/`video` GID so `/dev/kfd` is openable rootless. AMD does **not** use NVIDIA's `--runtime runc` workaround.
- **`/dev/kfd` is shared:** the Kernel Fusion Driver node is a single interface shared by all GPUs and is always passed in. Per-GPU restriction comes from which `/dev/dri/renderD*` nodes you select via `devices`.
- **Locked-memory limit:** whenever GPU passthrough is active, yolo lifts the container's locked-memory *soft* limit to the host's hard cap (`--ulimit memlock=<host-hard>:<host-hard>`, or `-1` if the host is already unlimited). A rootless container can't raise the *hard* cap above the host's, so this gives GPU runtimes the most they can pin. Current ROCm (verified on gfx1151 / ROCm 7.2) runs GPU compute fine at the common 8 MB rootless cap — no host change is needed. (Older ROCm builds pinned a larger queue ring buffer; see the troubleshooting note below if you ever hit `AMDKFD_IOC_CREATE_QUEUE EINVAL`.)
- **In-container GPU selection:** for an explicit `devices` selection (e.g. `"0"` or `"0,1"`), yolo sets `ROCR_VISIBLE_DEVICES` and `HIP_VISIBLE_DEVICES` to that value. For the default `devices: "all"` it leaves them **unset** — unlike NVIDIA's `NVIDIA_VISIBLE_DEVICES`, the ROCr/HSA selector does **not** accept the literal `"all"` (it matches no device and hides every GPU), and ROCm's own default is "all GPUs visible". These env vars are **not a security boundary** — real isolation comes from which render nodes are passed in.

### Troubleshooting AMD GPU

- **`Unable to open /dev/kfd read-write: Permission denied`:** The container process lacks the owning group. Make sure your host user is in the `render` group (`sudo usermod -aG render "$USER"`, then re-login) — `--group-add keep-groups` only preserves groups the host user already holds.
- **GPU detected but ROCm errors out on a consumer Radeon:** Consumer/unsupported GPUs often need `HSA_OVERRIDE_GFX_VERSION` to be recognized as a supported gfx target (e.g. `"11.0.0"` for gfx1100, `"10.3.0"` for gfx1030, `"9.0.0"` for gfx900). Set it via `hsa_override_gfx_version` in the config. This is best-effort, same-architecture-family only, and unsupported by AMD.
- **`rocminfo`/`rocm-smi` not found inside jail:** ROCm userspace is **not** injected from the host — it must ship inside the image. Use a `rocm/*` base image instead of a generic one.
- **GPU enumerates and `hipMalloc` works, but any kernel launch segfaults (trace shows `AMDKFD_IOC_CREATE_QUEUE … EINVAL`):** an older ROCm userspace needs to pin a larger (~13 MB) queue ring buffer than the rootless default `RLIMIT_MEMLOCK` (often 8 MB) allows. Current ROCm (7.2+) does **not** hit this — first try a newer `rocm/*` image. If you're pinned to an older ROCm build, raise the **host's** memlock hard cap (a rootless jail can't exceed it): `limits.conf` `<user> hard memlock unlimited`, systemd `LimitMEMLOCK=infinity`, or podman `containers.conf` `default_ulimits = ["memlock=-1:-1"]`, then restart the jail.
- **`torch.cuda.is_available()` is `False` / `rocminfo` shows only the CPU agent, but the device nodes are present:** if you set `ROCR_VISIBLE_DEVICES=all` (or `HIP_VISIBLE_DEVICES=all`) yourself, ROCm sees zero GPUs — the selector does not accept `"all"`. Leave it unset for all GPUs, or use explicit indices (`0`, `0,1`). yolo handles this for you (it omits the env vars when `devices: "all"`), so this only bites if you override them manually inside the container.

---
