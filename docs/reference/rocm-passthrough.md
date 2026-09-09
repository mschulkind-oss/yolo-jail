---
status: current
verified: 2026-09-09
verified_commit: 41dde711
covers:
  - internal/cli/run/helpers.go
  - internal/cli/run/hostprobes.go
  - internal/cli/run/assemble.go
  - internal/cli/run/syscalls_linux.go
  - internal/cli/check/sections_devices.go
  - internal/config/validate.go
tags: [gpu, amd, rocm, devices, podman, passthrough]
---

# AMD ROCm passthrough — how an AMD GPU reaches a jail

**Status:** CURRENT as of 2026-09-09, verified against `41dde711`.

yolo passes an AMD GPU into a jail by **raw device-node passthrough** — the kernel fusion driver
node plus one or more DRI render nodes, with podman told to preserve the host's supplementary
groups. It is not NVIDIA's CDI machinery, and the difference is not cosmetic: the device-node path
needs **no host toolkit**, works on consumer Radeon hardware, and is the more widely deployed path.
An opt-in CDI mode exists for hosts that have AMD's container toolkit.

The whole surface is the existing `gpu` config block, extended with a `vendor` discriminator that
defaults to `nvidia` — so every pre-existing config keeps working untouched.

| Component | Lives in |
| :--- | :--- |
| Argv construction — device nodes, groups, env, the memlock clamp | `internal/cli/run` (`gpuArgs` in `helpers.go`) |
| The host probe | `internal/cli/run` (`rocmHostAvailable`, `hasRenderNode`) |
| The host memlock ceiling | `internal/cli/run` (`syscalls_linux.go`) |
| Config shape and the vendor-exclusivity rules | `internal/config` (`knownGPUKeys`, `validate.go`) |
| Diagnostics | `internal/cli/check` (`sectionGPUAmd`, `checkDeviceNode`, `checkRocmEnumeration`) |

**Reads with:** [`../research/rocm-gpu-jail-findings.md`](../research/rocm-gpu-jail-findings.md)
(the hardware findings and the downstream execution-provider work),
[`../guides/USER_GUIDE.md`](../guides/USER_GUIDE.md) (the user-facing GPU section, including the
image steer).

For the `gpu` block key by key — every field, its vendor, its default — run `yolo config-ref`.

---

## The load-bearing constraints

Four facts shape the whole design. Break any one and AMD passthrough stops working, usually
silently.

1. **Group membership is make-or-break, and rootless podman needs `--group-add keep-groups`.**
   The compute nodes are group-owned, so the container process must hold the owning GID or `open()`
   fails with a permission error. A bare `--group-add <name>` **fails rootless**: userns
   GID-offsetting maps the in-container GID to a high host GID rather than the real one.
   `keep-groups` tells crun to skip `setgroups()`, so the host's supplementary groups survive — the
   process shows as `nobody` inside while the kernel's checks use the real host GID and succeed.

2. **`keep-groups` is crun-only and exclusive.** It cannot be combined with any other
   `--group-add`, and it does not work under runc.

3. **Therefore the AMD path must NOT inherit NVIDIA's `--runtime runc` pin.** That pin exists only
   to dodge a crun+CDI+custom-userns bug. Raw device-node passthrough involves no CDI, and forcing
   runc would break AMD group access outright. The NVIDIA runc / identity-uidmap / `SYS_ADMIN` /
   `label=disable` block is gated on the vendor; an AMD launch takes the ordinary branch, which
   keeps `/dev/fuse` and `MKNOD` and the default runtime.

4. **ROCm userspace is NOT injected.** Unlike NVIDIA's toolkit, which bind-mounts host driver
   libraries into arbitrary images, every AMD path injects **only kernel device nodes**. ROCm
   userspace — HIP, `rocm-smi`, the math libraries — ships *inside* the image.

> [!WARNING]
> **A generic base image with AMD passthrough gets working device nodes and no working HIP.** That
> is not a bug in the passthrough; it is constraint 4. Users need a ROCm base image, which the user
> guide steers them to. This is deliberately a documentation steer rather than a warning or a
> refusal — yolo cannot tell what a given image ships.

> [!WARNING]
> **`ROCR_VISIBLE_DEVICES` does not accept the literal `all`.** Unlike NVIDIA's selector, the
> ROCr/HSA one matches *no device* for that value — so a container gets the nodes and then reports
> no GPU, and the failure looks like broken passthrough rather than a bad env var. Since the device
> selector defaults to `all`, an unconditional emit shipped a GPU-less container as the **default**
> AMD config. The selectors are emitted only for explicit indices or UUIDs; omitting them is ROCm's
> own "all visible" default.

> [!WARNING]
> **The visible-device selectors are not a security boundary.** An application can reset them.
> Real isolation comes only from which render nodes are passed with `--device`.

## Why the `gpu` block rather than a separate `rocm` block

One `enabled`/`devices` code path, one host-probe call site, one warn-and-continue path. A parallel
block would need its own key census, its own top-level key, a parallel validation block and
mutual-exclusion logic — and the differences that actually exist live in argv construction and
runtime selection, not in the config surface. `vendor` defaults to `nvidia` when absent, which is
what makes the extension backward-compatible.

Vendor exclusivity is enforced in validation rather than left to the consumer, in both directions:
a key that is meaningless for the other vendor is an **error**, not a silent no-op. In particular
**`capabilities` is refused for AMD**, because there is no analog to NVIDIA's driver-capabilities
variable and silently accepting the key would mislead. Defaults are applied at the consumer, not in
the validator, matching the house pattern.

## Device injection

**`mode: "devices"` — the default, and the one that needs nothing on the host.** The launch emits
the kernel fusion driver node (guarded on its existence), then either the whole DRI directory for
the `all` case or the per-index render nodes; then `--group-add keep-groups` on podman; then the
visible-device selectors only for an explicit selection; then the gfx-version override and the
seccomp opt-in if set.

**`mode: "cdi"` — opt-in, for a host that has AMD's container toolkit.** Reuses the NVIDIA
injection shape against the AMD device namespace. Two things must not be "simplified" here:

- **CDI still emits `--group-add keep-groups`.** CDI does not remove the group requirement for
  rootless.
- **CDI emits no environment variables**, because AMD's CDI generator populates device nodes only —
  not env vars, not OCI hooks, not host-library mounts. The env-var knobs belong to a separate
  Docker-only runtime shim, not to CDI. There is consequently **no driver-versus-spec staleness
  coupling** to check, the way there is for NVIDIA.

**Verified on hardware: AMD CDI runs correctly under the default runtime.** There is no
crun+CDI failure for AMD in either mode, so constraint 3 holds for CDI too.

**A requested-but-unavailable GPU warns and continues.** A config asking for AMD passthrough on a
host without it starts the jail without GPU flags rather than refusing, which is what let the whole
implementation land before any hardware existed to verify it on.

### VA-API

`vaapi: true` (AMD only, default off, inert without `enabled`) adds a driver-search-path variable
pointing at the jail's own DRI driver directories, so a VA-API consumer inside the jail finds the
image's drivers. It rides the same render nodes the compute path already passes — there is no
second device set — and it is refused for `vendor: "nvidia"`. The launch's passthrough line names
it when it is on.

## The memlock clamp

When GPU passthrough is enabled — **either vendor** — the launch reads the host's own locked-memory
hard ceiling and emits a `--ulimit memlock` at exactly that value, or the unlimited literal when
the host is already unlimited.

The clamp is **adaptive rather than unconditional, and that is the whole point.** A rootless
container cannot raise its locked-memory limit above the host process's hard cap: the runtime's
`setrlimit` gets `EPERM` and **the container fails to start.** So the unconditional "unlimited"
value that AMD's rootful documentation prescribes would brick every GPU launch on a rootless host
with a finite cap, which is the common default. Clamping to the host ceiling lifts the container's
*soft* limit to the most a rootless container can get and can never fail.

> [!WARNING]
> **Do not add a warning telling users to raise the host's locked-memory cap for ROCm.** One
> existed and was deleted: the ~16 MB queue-ring requirement it was based on was specific to an
> older ROCm userspace, and current ROCm runs real compute at an 8 MB cap — verified down to 64 KB.
> The warning was factually wrong and it nudged users toward an unlimited-locked-memory DoS vector
> for zero functional benefit. Keep the clamp; do not restore the advice.

## Diagnostics

`yolo check` has its own AMD section, which runs only when passthrough is enabled for that vendor.
It walks HSA-agent enumeration, the kernel module, the device nodes, the runtime's group handling,
and — in CDI mode only — the presence of a CDI spec.

**Only agents whose device type is `GPU` are reported as GPUs.** HSA enumeration lists every agent,
including the CPU and, on an APU, an NPU or DSP; reporting each agent's marketing name announced
non-GPU hardware as a GPU.

The node check degrades usefully rather than just failing: an inaccessible node is diagnosed by
comparing its owning GID against the caller's groups, so the report distinguishes *"you are in the
group but this process has not picked it up"* — remedy: a fresh login — from *"you are not in the
group"* — remedy: add yourself.

> [!WARNING]
> **The AMD section's two in-jail guards are load-bearing, and its NVIDIA twin does not have
> them.** Inside a jail, the module check and the device-node checks report skipped-because-managed
> -by-host rather than failing on host facts an in-jail process cannot see. `sectionGPUNvidia` has
> no such guard and does fail on them. **This section is the reference for fixing that twin, not
> the thing that needs fixing** — do not "unify" the two by removing these guards.

## Security surface

The kernel fusion driver node is a **single global ioctl-rich node that cannot be scoped per-GPU**,
and it carries recurring local-privilege-escalation kernel CVEs. Per-GPU restriction is available
only by choosing specific render nodes.

The least-privilege defaults follow directly: pass only the needed render nodes plus that one node,
**keep seccomp on**, and add no extra capabilities for AMD. Basic ROCm compute is verified working
with the default seccomp profile, so `seccomp=unconfined` is an opt-in knob and never a default —
disabling seccomp removes all syscall filtering and widens the escape surface. AMD marks it
optional itself, recommended only for HPC and numactl workloads.

## What this does not do

- **It does not install or generate anything on the host.** No toolkit install, no CDI generation.
  `mode: "cdi"` consumes a spec someone else generated, and says so when there is none.
- **It does not inject ROCm userspace.** Constraint 4.
- **It does not check host-versus-image ROCm versions.** AMD's compatibility window is wide, a
  mismatch inside it works, and there is no auto-refresh service to make a staleness warning
  actionable.
- **It does not warn or refuse on a non-ROCm base image.** A documentation steer instead.
- **It does not work on the Apple Container backend or macOS.** No device passthrough; the probe
  says so and `yolo check` reports it.
- **It does not add a `render`-versus-`video` explicit-GID mode.** `keep-groups` covers both with
  no hard-coded GID. An explicit least-privilege `--group-add <gid>` mode remains optional future
  work.

## Why it's this way

Rulings a future change would otherwise undo. Every row below was settled on real hardware — an
RDNA-family APU, host and image ROCm one patch apart, rootless podman with crun, on a distro
without SELinux.

| Ruling | Why it holds |
| :--- | :--- |
| **AMD stays on the default runtime; the runc pin is vendor-gated** | `keep-groups` is crun-only, and it is the only thing that makes rootless device access work. Verified on hardware for **both** modes: AMD CDI hits no crun+CDI failure. The exact upstream issue number behind the NVIDIA pin is unconfirmed and is moot for AMD. |
| **`keep-groups`, not an explicit GID** | It covers both candidate groups with no hard-coded number. The CDI generator's own spec confirms which group owns the compute nodes versus the card node, so the mechanism is right; it could not be stress-tested against a strictly-grouped node on the verification host, whose nodes were world-writable. |
| **`mode` set for `vendor: "nvidia"` is an ERROR, not ignored** | A consistency call with no hardware bearing: a key that means nothing for the selected vendor is a mistake worth naming, exactly as `capabilities` is for AMD. |
| **The gfx-version override stays a best-effort, same-architecture-family knob** | It is a pure userspace variable, so it is safe to set from inside a jail, and it is unsupported by AMD. Cross-architecture mappings are unreliable, and the value table is not adversarially verified. The verification card needed no override at all. |
| **No host/image version-window warning** | A mismatch inside AMD's window works, and no auto-refresh exists to make the warning actionable. Not worth the complexity. |
| **No image steering beyond documentation** | Confirmed on hardware that a generic image gets nodes and no HIP — and yolo cannot tell what an image ships, so a warn or refuse would fire on correct configurations. |

## Current values

Verified at `41dde711`. The prose above explains what each of these is for; this table is the only
place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Vendor discriminator | `gpu.vendor` ∈ `{nvidia, amd}`, absent ⇒ `nvidia` | `internal/config/validate.go`; default applied in `internal/cli/run` |
| AMD injection mode | `gpu.mode` ∈ `{devices, cdi}`, default `devices` | `internal/config/validate.go` |
| AMD-only keys | `mode`, `hsa_override_gfx_version`, `seccomp_unconfined`, `vaapi` | `config.knownGPUKeys`; `yolo config-ref` |
| Compute device node | `/dev/kfd` | `internal/cli/run/helpers.go`, `rocmHostAvailable` |
| Render nodes | `/dev/dri/renderD*` (whole `/dev/dri` for `devices: "all"`) | `internal/cli/run/helpers.go`, `hasRenderNode` |
| Group flag | `--group-add keep-groups`, podman only, both modes | `internal/cli/run/helpers.go` |
| CDI device namespace and spec paths | `amd.com/gpu=…`; `/etc/cdi/amd.json`, `/var/run/cdi/amd.json` | `internal/cli/run/helpers.go`, `internal/cli/check/sections_devices.go` |
| Visible-device selectors | `ROCR_VISIBLE_DEVICES`, `HIP_VISIBLE_DEVICES` — emitted only for an explicit selection | `internal/cli/run/helpers.go` |
| Gfx override | `HSA_OVERRIDE_GFX_VERSION` | `internal/cli/run/helpers.go` |
| VA-API variable | `LIBVA_DRIVERS_PATH=/lib/dri:/usr/lib/dri` | `internal/cli/run/helpers.go` |
| Locked-memory clamp | `--ulimit memlock=<host hard>:<host hard>`, or `-1:-1` when the host is unlimited | `internal/cli/run/helpers.go`, `internal/cli/run/syscalls_linux.go` |
| Host probe order | module ⇒ compute node ⇒ a render node ⇒ a functional enumerator when present | `run.rocmHostAvailable` |
