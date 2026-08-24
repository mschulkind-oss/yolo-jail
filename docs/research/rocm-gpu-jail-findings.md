# ROCm GPU in the YOLO jail — findings (gfx1151 / Radeon 8060S)

**Status:** SUPERSEDED IN PART — findings gathered 2026-06-05, resolved on hardware
2026-06-06, re-checked against the tree 2026-08-23. The diagnosis below is a
**ROCm 7.1.1-era incident record**; AMD/ROCm passthrough has since SHIPPED and
§"Recommended next steps" is fully overtaken — see the "What shipped" box below.

**Context:** Follow-on to the recording-pipeline repair. GPU passthrough was added
to the jail (`gpu.enabled` in `yolo-jail.jsonc`) to enable GPU-accelerated rembg.
This documents how far GPU compute gets in the jail and the one blocker that stops
it.

> **✅ RESOLVED — no host change needed (verified on hardware 2026-06-06):**
> The memlock blocker below was **specific to the nixpkgs-built ROCm 7.1.1
> userspace** used during this diagnosis. Re-tested on the **same GPU host** with
> **ROCm 7.2.4** images (`rocm/dev-ubuntu-24.04`, `rocm/pytorch`): `hip_smoke`
> (the gfx1151 saxpy below) returns **`RESULT: PASS` at the current 8 MB host
> cap** — and keeps passing with `--ulimit` as low as **64 KB**. No
> `CREATE_QUEUE EINVAL`, confirmed real GPU execution (a wrong-arch gfx900 binary
> segfaults; the gfx1151 build computes correctly). **Newer ROCm userspace no
> longer pins a >8 MB queue ring buffer.**
>
> What changed in yolo as a result (2026-06-06):
> - **Host memlock cap left at 8 MB** — raising it (the original recommendation)
>   is unnecessary and would add an unlimited-locked-memory DoS vector for no gain.
> - **Removed** the now-misleading `yolo check` "GPU locked-memory limit" section
>   and the `yolo run` low-cap warning (they claimed "needs ~16 MB; raise the host
>   cap", which is wrong on ROCm 7.2).
> - **Kept** the `--ulimit memlock=<host-hard>:<host-hard>` clamp — harmless
>   soft-limit lift, never bricks startup. (It lived in `run_cmd.py` at the time;
>   the Go port moved it to `gpuArgs`, `internal/cli/run/helpers.go:142-155`, which
>   emits `--ulimit memlock=-1:-1` when the host hard cap is unlimited and clamps
>   to the hard cap otherwise — verified 2026-08-23.)
>
> If a workload ever does hit `CREATE_QUEUE EINVAL` on an older ROCm build, the
> remedy (raise the host memlock cap) is documented in
> [rocm-passthrough-design.md](../design/rocm-passthrough-design.md) §7.2 — but it is not the default need.
> The onnxruntime EP work (gfx1151 code objects / migraphx asserts-LLVM) remains
> the next item.
>
> The original 7.1.1-era diagnosis is preserved below for the record.

> [!NOTE]
> **What shipped since (verified against the tree 2026-08-23).** AMD/ROCm
> passthrough is no longer a "recommended next step" — it is a first-class,
> validated config surface:
>
> - `gpu.vendor` accepts `"nvidia"` or `"amd"`
>   (`internal/config/validate.go:881-885`), and the AMD-only keys `gpu.mode`
>   (`"devices"` | `"cdi"`, `:891-897`) and `gpu.hsa_override_gfx_version`
>   (`:921-927`) are validated as AMD-only — asking for them under
>   `vendor: nvidia` is a config error, not a silent no-op. `gpu.capabilities`
>   is refused for AMD outright (`:903-905`) because ROCm has no
>   driver-capabilities concept.
> - `gpu.seccomp_unconfined` survives as a validated boolean
>   (`internal/config/validate.go:930-932`) — the knob this doc's "What I
>   changed" section recommended reverting still exists; the advice to leave it
>   `false` stands, since it was never what fixed anything.
> - The host-side probe `rocmHostAvailable` gates the whole thing on the
>   `amdgpu` module, `/dev/kfd` and a render node
>   (`internal/cli/run/hostprobes.go:59-70`), and refuses `container`/macOS.
> - `yolo check` has a real ROCm device section — `/dev/kfd`, every
>   `/dev/dri/renderD*`, group membership with the exact `usermod` fix, and the
>   `amd-ctk cdi generate` hint under `mode: cdi`
>   (`internal/cli/check/sections_devices.go:139-205`).
>
> Note also that `gpu.enabled` alone is no longer the whole story — a `vendor`
> is what selects the passthrough shape.

## TL;DR

GPU passthrough is **wired correctly and the GPU is fully visible**, but **no GPU
kernel can run** because the container's `RLIMIT_MEMLOCK` is capped at **8 MB**,
and AMD's KFD driver needs to pin a **~13 MB queue ring buffer** to create a
command queue. `AMDKFD_IOC_CREATE_QUEUE` fails with `EINVAL`, ROCr's cleanup path
then segfaults. **Fix must be made in the `yolo` tool on the host** (add
`--ulimit memlock=-1` to the `podman run` invocation when `gpu.enabled`); there is
no in-jail or in-config workaround — the 8 MB cap is a kernel-enforced hard limit.

## What works (verified in-jail)

- **Device nodes present & openable:** `/dev/kfd`, `/dev/dri/renderD128`,
  `/dev/dri/card1` all `open(O_RDWR)` successfully.
- **ROCm enumerates the GPU correctly** (`rocminfo`):
  `Agent 2: gfx1151 / Radeon 8060S Graphics, 40 CUs, Gfx 11/5/1`.
- **KFD version negotiation, memory alloc/map, events all succeed:**
  `AMDKFD_IOC_GET_VERSION`, `GET_PROCESS_APERTURES_NEW`, `ALLOC_MEMORY_OF_GPU`,
  `MAP_MEMORY_TO_GPU`, `CREATE_EVENT` — all return 0.
- **`hipMalloc` / `hipMemcpy` H2D allocate device memory fine.**
- A trivial **gfx1151-native HIP saxpy kernel compiles** (clang++ from nixpkgs
  rocm-toolchain, `--offload-arch=gfx1151`) with real code objects.

## The blocker

Running any GPU kernel segfaults. `strace` shows the true cause:

```
ioctl(3, AMDKFD_IOC_CREATE_QUEUE, ...) = -1 EINVAL (Invalid argument)   # ×9 retries
... then SIGSEGV {si_code=SEGV_MAPERR, si_addr=0x8}
```

`gdb` backtrace confirms the segfault is botched cleanup *after* the failed
create, not the real fault:

```
#0 amd::ReferenceCountedObject::release()   (libamdhip64.so.7)
#1 amd::HostQueue::terminate()
#2 hip::Stream::terminate()
#4 hip::Device::NullStream(bool)
#5 hip::hipMemcpy(...)        # first H2D copy lazily creates the queue
#6 main
```

### Root cause: RLIMIT_MEMLOCK too small

- The queue ring buffer mmap right before `CREATE_QUEUE` is **13,983,744 B
  (~13.3 MB)** rw / 15.3 MB reserved.
- `ulimit -l` (soft **and** hard) = **8192 KB (8 MB)**. Cannot be raised from
  inside (`ulimit -l unlimited` → silently stays 8192; a child trying to raise it
  gets `Operation not permitted`).
- KFD pins queue memory; pin > memlock ⇒ `CREATE_QUEUE` → `EINVAL`. This is the
  well-known container-GPU requirement; AMD's own docs prescribe
  `--ulimit memlock=-1` (or `=unlimited`) for ROCm containers.
- `CREATE_QUEUE` is the **only** failing ioctl in the entire trace.

### Ruled out

- **seccomp** — set `gpu.seccomp_unconfined: true`, confirmed `Seccomp: 0`
  in-jail after restart; kernel still crashes identically. Not the cause.
  (The flag is currently left **on** in `yolo-jail.jsonc`; it can be turned back
  **off** since it didn't help — see "Recommended config" below.)
- **Permissions / uid mapping** — all three device nodes open RDWR; topology
  readable.
- **gfx version mismatch** — kernel (7.0.7-zen, May 2026) recognises gfx1151
  natively; `HSA_OVERRIDE_GFX_VERSION` not needed and doesn't help.
- **HSA env toggles** — `HSA_ENABLE_SDMA/INTERRUPT/MWAITX/DEBUG`,
  `HSA_NO_SCRATCH_RECLAIM`, `HSA_SVM_GUARD_PAGES`, `HSA_DISABLE_CACHE`: none help.

## onnxruntime EP status (secondary, blocked behind the above)

Two AMD wheels were tested (cp312 venvs, ROCm runtime libs built from nixpkgs
rocm 7.1.1 via `nix-build`):

| Wheel | Result |
|---|---|
| `onnxruntime-rocm` 1.22.1 (AMD repo, rocm-rel-7.0) | Imports; exposes ROCM + MIGraphX EPs. **No gfx1151 code objects in the wheel** (built for gfx1030/1100/1101; predates Strix Halo) → `No compatible code objects for gfx1151` → same queue-create crash. |
| `onnxruntime-migraphx` 1.23.1 (AMD repo, rocm-rel-7.1, matches nixpkgs 7.1.1) | Imports; EP loads. migraphx JIT (no precompiled-kernel problem) but aborts on an LLVM `cl::SubCommand` assertion — nixpkgs migraphx 7.1.1 ships an asserts-enabled LLVM. `MIGRAPHX_DISABLE_MLIR=1` does not dodge it. |

Both are moot until `CREATE_QUEUE` works. **After** the memlock fix:
- Best path is likely `onnxruntime-migraphx` 1.23.1 (ABI matches the nixpkgs
  runtime) **if** the nixpkgs migraphx asserts-LLVM issue is resolved — try a
  release (non-asserts) migraphx build, or AMD's TheRock gfx1151 wheels.
- `onnxruntime-rocm` would need a gfx1151-capable build (rocm 7.1+ wheel, or
  TheRock) since the 7.0 wheel lacks gfx1151 kernels.

## What I changed

- `yolo-jail.jsonc`: added `gpu.seccomp_unconfined: true` while testing the
  seccomp hypothesis. **It did not help** — recommend reverting to `false` unless
  wanted for another reason (it removes all syscall filtering for the jail).

### ⚠ Retracted: Recommended next steps (host-side, in the `yolo` tool)

> [!WARNING]
> **All three items below are overtaken (checked 2026-08-23).** Item 1 was
> *wrong about the diagnosis* — the memlock clamp shipped, but as a harmless
> soft-limit lift, not because ROCm needs it; ROCm ≥ 7.2 passes at the 8 MB cap
> (see the resolution banner). Item 1's optional `resources.memlock` /
> `gpu.memlock` knob was deliberately **not** built; the clamp is unconditional
> in `gpuArgs` (`internal/cli/run/helpers.go:151-155`). Item 2's ground-truth
> test was run and passed. The GPU config surface that actually shipped is in
> the "What shipped since" box above. Kept verbatim because a reader who finds
> `CREATE_QUEUE EINVAL` on an old ROCm build needs the reasoning, and because
> "raise the memlock cap" is a plausible-sounding fix that this incident
> **disproved** — do not re-derive it.

1. **Add `--ulimit memlock=-1` to the `podman run` flags whenever
   `gpu.enabled` is set** (both AMD and NVIDIA benefit). This is the actual fix.
   Optionally expose it as `resources.memlock` / `gpu.memlock` for control.
2. After that lands and the jail restarts, re-run the ground-truth test:
   `recording-pipeline-jail/hip_smoke.cpp` (compile cmd in
   `recording-pipeline-jail/rocm-resume.env`). Expect `RESULT: PASS`.
3. Then wire GPU rembg: onnxruntime EP via the migraphx wheel (resolve the
   asserts-LLVM abort first), set rembg's ORT providers to include
   `MIGraphXExecutionProvider`/`ROCMExecutionProvider`.

## Reproduce the diagnosis

```bash
# (paths in recording-pipeline-jail/rocm-resume.env)
cd /tmp && cp /workspace/recording-pipeline-jail/hip_smoke.cpp .
# compile for gfx1151 (see rocm-resume.env for full clang++ invocation)
# run under strace to see CREATE_QUEUE EINVAL:
LD_LIBRARY_PATH=$LIBP strace -f -e trace=ioctl ./hip_smoke 2>&1 | grep CREATE_QUEUE
ulimit -l    # => 8192  (the cap)
```
