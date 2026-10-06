# Devices and GPUs

Give the agent hardware: USB and serial devices, NVIDIA or AMD GPUs, and `/dev/kvm`.

> [!NOTE]
> **This page is about Linux, on Podman.** On a Mac, a container jail runs inside a Linux VM
> that has no access to the Mac's USB devices or GPU; the settings are skipped with a warning and
> the jail still starts. On `macos-user` the agent is an ordinary Mac program, so Mac GPU programs
> using Metal should work as they do outside yolo, and a serial device should work by naming its
> node (not yet tried on a Mac): list `/dev/cu.usbserial-…` (`ls /dev/cu.*` shows the ones plugged
> in) in `devices`, and the sandbox may configure it, which it otherwise refuses. The launch says
> which nodes it allowed. Raw disks stay refused there, and USB and cgroup entries are not read.
> For a USB serial device in a Podman jail on a Mac, the `serial` loophole should work; see
> [Host Access and Loopholes](loopholes.md).

## Device Passthrough

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

A missing device produces a warning but does not stop the jail. Adding a device is a config change you approve at the next launch; see [Approving config changes](../reference/configuration.md#approving-config-changes).

---

## GPU Passthrough (NVIDIA)

Run CUDA workloads, such as training models, inside the jail on an NVIDIA GPU. It needs the NVIDIA
Container Toolkit on the host. For GPU work from a Mac, use a Linux machine, local or in the cloud.

### Host Setup

1. **Verify your GPU driver:**
   ```bash
   nvidia-smi
   ```

2. **Install the NVIDIA Container Toolkit.** On Ubuntu or Debian, as below; for other distributions,
   follow [NVIDIA's install guide](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html):
   ```bash
   curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey \
     | sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
   curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list \
     | sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' \
     | sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list
   sudo apt-get update && sudo apt-get install -y nvidia-container-toolkit
   ```

3. **Describe the GPU for Podman** with a CDI spec, a file that tells container runtimes how to
   attach it. Regenerate it after a driver update:
   ```bash
   sudo nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml
   ```
   yolo looks for a `.yaml` spec; a `nvidia.json` one is not found and the jail starts without the
   GPU.

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

### Good to know

- **Shared memory:** `/dev/shm` is 2 GB, enough for PyTorch's multi-process data loading.
- **CUDA versions:** CUDA inside the jail can be newer than the host driver, but not older.
- **Nested jails with a GPU** are not supported.

### Cloud GPUs

On AWS, an Amazon Deep Learning AMI comes with the NVIDIA driver and Container Toolkit installed;
then install Podman and yolo as on any Linux machine.

### Troubleshooting GPU

- **`conmon bytes "": readObjectStart` error:** a bug in Podman's default runtime, `crun`, with GPUs ([podman#27483](https://github.com/containers/podman/issues/27483)). yolo switches to `runc` whenever a GPU is enabled, so update yolo if you see it; `runc` must be installed.
- **`nvidia-smi` not found inside jail:** The NVIDIA Container Toolkit injects driver libs at container start. Check the toolkit is installed and configured on the host.
- **CUDA out of memory:** Reduce batch size, or limit which GPUs are exposed with `"devices": "0"`.

---

## AMD GPU Passthrough (ROCm)

Run ROCm compute workloads inside the jail on an AMD GPU. Unlike NVIDIA, the default path needs **no host container toolkit** — just the `amdgpu` kernel driver and the right group membership. ROCm passthrough works on both AMD Instinct and consumer Radeon hardware.

> [!IMPORTANT]
> **The ROCm libraries are not passed in from the host.** Unlike NVIDIA's toolkit, the AMD path
> passes only the GPU's device files (`/dev/kfd`, `/dev/dri/renderD*`), and yolo's jail image
> includes no ROCm libraries. Install them inside the jail: the ROCm build of PyTorch from pip
> brings its own (see below), and nixpkgs packages ROCm under `rocmPackages` for `packages`.

### Host Setup

Tested on real AMD hardware (Radeon 8060S, ROCm 7.2, rootless Podman), in both the default mode and
CDI mode. Package and group names vary by distribution, so adapt the commands to yours.

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
   With `mode: "cdi"` and no spec at `/etc/cdi/amd.json` or `/var/run/cdi/amd.json`, the launch warns, names this command, and starts without the GPU. The generated spec injects **only device nodes** (no env vars, hooks, or host-library mounts — verified), and CDI mode runs ROCm correctly under the default crun runtime (no `runc` workaround needed). On a single-GPU host CDI offers no advantage over the default device-node mode.

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

Install the ROCm build of PyTorch inside the jail, for example with `uv` in a virtual environment.
Pick the `rocmX.Y` index that matches a ROCm release your GPU supports:

```bash
uv venv && source .venv/bin/activate
uv pip install torch --index-url https://download.pytorch.org/whl/rocm6.2
python -c "import torch; print(torch.cuda.is_available(), torch.cuda.get_device_name(0))"
# verified output on a Radeon 8060S (gfx1151): True Radeon 8060S Graphics
```

ROCm exposes the AMD GPU through PyTorch's `torch.cuda` API, so `torch.cuda.is_available()`
returning `True` means ROCm is working.

### Good to know

- **No host toolkit in the default mode.** `mode: "devices"` passes the GPU's device files straight
  in and keeps your host groups, so `/dev/kfd` opens in a rootless jail. `mode: "cdi"` uses the spec
  `amd-ctk` generated instead.
- **Choosing GPUs.** `/dev/kfd` is shared by every GPU and always passed in; `devices` chooses which
  render devices come with it. For an explicit choice such as `"0,1"`, yolo sets
  `ROCR_VISIBLE_DEVICES` and `HIP_VISIBLE_DEVICES` to it. For `"all"` it leaves them unset, because
  ROCm reads the literal `all` as no GPU at all.
- **Locked memory.** yolo raises the jail's locked-memory limit as far as the host allows, which
  current ROCm needs no more than.

### Troubleshooting AMD GPU

- **`Unable to open /dev/kfd read-write: Permission denied`:** The container process lacks the owning group. Make sure your host user is in the `render` group (`sudo usermod -aG render "$USER"`, then re-login) — `--group-add keep-groups` only preserves groups the host user already holds.
- **GPU detected but ROCm errors out on a consumer Radeon:** Consumer/unsupported GPUs often need `HSA_OVERRIDE_GFX_VERSION` to be recognized as a supported gfx target (e.g. `"11.0.0"` for gfx1100, `"10.3.0"` for gfx1030, `"9.0.0"` for gfx900). Set it via `hsa_override_gfx_version` in the config. This is best-effort, same-architecture-family only, and unsupported by AMD.
- **`rocminfo`/`rocm-smi` not found inside the jail:** the ROCm tools are not passed in from the host. Add them to `packages` from nixpkgs' `rocmPackages`.
- **GPU enumerates and `hipMalloc` works, but any kernel launch segfaults (trace shows `AMDKFD_IOC_CREATE_QUEUE … EINVAL`):** an older ROCm needs more locked memory than a rootless container usually allows. ROCm 7.2 and later do **not** hit this, so first try a newer ROCm. If you're pinned to an older ROCm build, raise the **host's** memlock hard cap (a rootless jail can't exceed it): `limits.conf` `<user> hard memlock unlimited`, systemd `LimitMEMLOCK=infinity`, or podman `containers.conf` `default_ulimits = ["memlock=-1:-1"]`, then restart the jail.
- **`torch.cuda.is_available()` is `False` / `rocminfo` shows only the CPU agent, but the device nodes are present:** if you set `ROCR_VISIBLE_DEVICES=all` (or `HIP_VISIBLE_DEVICES=all`) yourself, ROCm sees zero GPUs — the selector does not accept `"all"`. Leave it unset for all GPUs, or use explicit indices (`0`, `0,1`). yolo handles this for you (it omits the env vars when `devices: "all"`), so this only bites if you override them manually inside the container.

---

## KVM

Set `"kvm": true` to pass `/dev/kvm` into the jail, for hardware-accelerated virtual machines
inside it: QEMU, Firecracker, the Android emulator or kernel development. It needs virtualization
turned on in your computer's firmware, the `kvm` kernel module loaded, and your user in the `kvm`
group; `yolo check` verifies all three when the key is on. Leave it off unless you need it, because
it widens what the jail can reach in the host kernel.
