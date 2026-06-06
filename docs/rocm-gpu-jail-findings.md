# ROCm GPU in the YOLO jail — findings (gfx1151 / Radeon 8060S)

**Date:** 2026-06-05
**Context:** Follow-on to the recording-pipeline repair. GPU passthrough was added
to the jail (`gpu.enabled` in `yolo-jail.jsonc`) to enable GPU-accelerated rembg.
This documents how far GPU compute gets in the jail and the one blocker that stops
it.

> **⚠️ PARTIAL FIX + HARD DEPENDENCY ON A HOST SETTING (updated 2026-06-05):**
> The first fix attempt (`--ulimit memlock=-1:-1`, unconditional) was **wrong for
> rootless podman** and has been corrected. A rootless container **cannot raise
> RLIMIT_MEMLOCK above the host's hard cap** — crun rejects `setrlimit` with
> `EPERM` and the container fails to *start*. (Empirically reproduced in a nested
> jail: `podman run --ulimit memlock=-1:-1` → `crun: setrlimit RLIMIT_MEMLOCK:
> Operation not permitted` whenever the host hard cap is finite.)
>
> `run_cmd.py` now reads the host hard cap and requests `memlock=<hard>:<hard>`
> (or `-1` only when the host cap is already unlimited), and `yolo check` warns
> when the host cap is below ~16 MB. **But raising the cap itself is a host step
> the jail cannot perform.** So the real unblock is:
>
> 1. **On the GPU host, raise the memlock hard cap**, e.g. one of:
>    - `/etc/security/limits.conf`: `<user> hard memlock unlimited` (re-login), or
>    - podman: `~/.config/containers/containers.conf` → `[containers]
>      default_ulimits = ["memlock=-1:-1"]`, or
>    - if yolo runs under systemd: a drop-in with `LimitMEMLOCK=infinity`.
> 2. **Update the GPU host's yolo install** to this branch. (The "8192 after
>    restart" result means the host was running an *old* yolo with no memlock flag
>    at all — the new code was never deployed there.)
> 3. Restart the jail; confirm `ulimit -Hl` inside is now high/unlimited.
> 4. Re-run `hip_smoke` → expect `RESULT: PASS`.
>
> Full procedure: `docs/rocm-memlock-handoff.md`. §7.2 of
> `docs/rocm-passthrough-design.md` has the design rationale. The onnxruntime EP
> work (gfx1151 code objects / migraphx asserts-LLVM) remains the next item after
> compute works.

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

## Recommended next steps (host-side, in the `yolo` tool)

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
