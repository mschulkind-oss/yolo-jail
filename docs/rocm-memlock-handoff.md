# Handoff: ROCm GPU memlock blocker — host action + test procedure

**To:** the outside-jail / GPU-host agent (and the human who can change host config).
**From:** the in-jail yolo-jail agent.
**Date:** 2026-06-05
**Branch:** `feat/rocm-passthrough` work merged to `main` (commits incl. the memlock fix).
**Reads with:** `docs/rocm-gpu-jail-findings.md` (the diagnosis) and
`docs/rocm-passthrough-design.md` §7.2 (the design rationale).

---

## TL;DR

GPU compute in the jail is blocked by **one** thing: the container's
`RLIMIT_MEMLOCK` hard cap (8 MB) is too small for AMD's KFD to pin the ~13 MB
queue ring buffer, so `AMDKFD_IOC_CREATE_QUEUE` returns `EINVAL` and ROCr
segfaults. The GPU enumerates fine; only kernel **dispatch** fails.

The fix is **two parts, and the jail can only do one of them:**

1. **(yolo code — DONE on this branch)** `yolo run` now requests the highest
   memlock the host allows (`--ulimit memlock=<host-hard>:<host-hard>`, or `-1`
   if the host is already unlimited), and `yolo check` warns when the host cap
   is too low. This is committed and tested.
2. **(host config — YOU must do this)** Raise the **host's** memlock hard cap.
   A rootless container **cannot** exceed it — this is the real unblock.

> **Why the earlier "added `--ulimit memlock=-1`, restarted, still 8192" failed
> two ways:** (a) the very first code attempt requested unlimited
> *unconditionally*, which `crun` rejects with `EPERM` on a finite-cap rootless
> host — the container wouldn't even start (now fixed to clamp to the host cap);
> and (b) far more likely in your case, **the GPU host was running an old yolo**
> that had no memlock flag at all. `8192` unchanged after restart = the new code
> was never deployed there. Update yolo on the GPU host first (step 0).

---

## Why a rootless container can't just set `memlock=-1`

Empirically reproduced in a nested jail (host hard cap 8 MB):

```
podman run --ulimit memlock=-1:-1       → crun: setrlimit RLIMIT_MEMLOCK: Operation not permitted (EPERM)
podman run --ulimit memlock=16MB:16MB   → same EPERM   (any value > host hard cap)
podman run --ulimit memlock=8MB:8MB     → OK           (== host hard cap)
```

`setrlimit` raising the **hard** limit needs privilege the rootless user
namespace doesn't have. So the jail can at most request the host's existing hard
cap. Raising that cap is a host-side action.

---

## Procedure (run on the GPU host)

### Step 0 — Deploy this yolo to the GPU host
The memlock logic lives in `src/cli/run_cmd.py` + `src/cli/check_cmd.py` on this
branch. Make sure the GPU host's `yolo` is this version (not whatever predates
the ROCm work). Quick check on the host:
```sh
yolo --version          # should be the dev build with the ROCm commits
```

### Step 1 — Check the current host cap
```sh
ulimit -Hl              # hard cap, in KB. 8192 = 8 MB (too low). 'unlimited' = good.
```
`yolo check` (with `gpu.enabled` in the workspace config) now also reports this
under a **"GPU locked-memory limit"** section — green if ≥16 MB or unlimited,
warn otherwise.

### Step 2 — Raise the host memlock hard cap (pick ONE that fits the host)

**a) PAM / limits.conf** (most Linux hosts, login sessions):
```sh
echo "$USER hard memlock unlimited" | sudo tee /etc/security/limits.d/90-rocm-memlock.conf
echo "$USER soft memlock unlimited" | sudo tee -a /etc/security/limits.d/90-rocm-memlock.conf
# log out and back in (limits are applied at login), then: ulimit -Hl  → unlimited
```

**b) podman containers.conf** (applies to all podman containers; no re-login):
```sh
mkdir -p ~/.config/containers
cat >> ~/.config/containers/containers.conf <<'EOF'
[containers]
default_ulimits = ["memlock=-1:-1"]
EOF
```
Note: this only works if the host hard cap already permits it — on some hosts
you still need (a). Verify with the smoke test below.

**c) systemd** (if yolo/podman runs under a systemd user service or scope):
```sh
sudo mkdir -p /etc/systemd/system/user@.service.d
printf '[Service]\nLimitMEMLOCK=infinity\n' | sudo tee /etc/systemd/system/user@.service.d/memlock.conf
sudo systemctl daemon-reload
# log out/in or reboot the user session
```

### Step 3 — Restart the jail and verify the limit propagated
```sh
# recreate the jail (from the host), then inside it:
ulimit -Hl              # expect: unlimited (or at least ≥ 16384)
```
If it's still `8192`, the host change didn't take — recheck step 2 (re-login?
right user? right runtime path?).

### Step 4 — Ground-truth GPU test
```sh
# inside the restarted jail (paths in recording-pipeline-jail/rocm-resume.env):
cd /tmp && cp /workspace/recording-pipeline-jail/hip_smoke.cpp .
# compile for gfx1151 + run (full clang++ invocation in rocm-resume.env):
LD_LIBRARY_PATH="$LIBP" strace -f -e trace=ioctl ./hip_smoke 2>&1 | grep CREATE_QUEUE
./hip_smoke; echo "exit=$?"
```
- **Success:** `CREATE_QUEUE` returns `0` (not `EINVAL`), `hip_smoke` prints
  `RESULT: PASS`, exit 0. GPU compute path is open.
- **Still EINVAL:** confirm `ulimit -l` (soft, not just hard) is now high inside
  the jail. If the hard cap is high but soft is still 8192, raise the soft limit
  too (`ulimit -l unlimited` should now succeed since the hard cap allows it; or
  add the `soft memlock` line in step 2a). yolo sets soft=hard via `--ulimit`,
  so a fresh jail should already have soft raised.

### Step 5 — (after compute works) onnxruntime / GPU rembg
Separate, downstream of the above — tracked in
`docs/rocm-gpu-jail-findings.md`: the rocm-7.0 onnxruntime wheel lacks gfx1151
code objects; the migraphx-7.1 wheel ABI-matches but hits a nixpkgs
asserts-LLVM abort. Try a release (non-asserts) migraphx build or AMD's TheRock
gfx1151 wheels.

---

## What changed in yolo (for reviewers)

- `src/cli/run_cmd.py`: when `gpu_enabled`, read host hard memlock via
  `resource.getrlimit` and emit `--ulimit memlock=<hard>:<hard>` (or `-1:-1` if
  host is unlimited); warn when the host cap is < ~16 MB. **Not** an
  unconditional `-1` (that bricks rootless startup with finite caps).
- `src/cli/check_cmd.py`: new **"GPU locked-memory limit"** doctor section —
  ok when host cap ≥16 MB / unlimited, warn (with the limits.conf / systemd /
  containers.conf remedies) otherwise; skipped inside a jail.
- Tests: `TestRunRocm::test_rocm_memlock_clamped_to_finite_host_cap` and
  `::test_rocm_memlock_unlimited_when_host_allows`.
- Docs: design §7.2, `USER_GUIDE.md` (runtime + troubleshooting), config-ref,
  findings doc, this handoff.

## Open question for the host agent
Is raising the memlock hard cap acceptable on the target host's security policy?
Unlimited locked memory lets a process pin arbitrary RAM (a local DoS vector).
For a single-user GPU workstation it's the standard ROCm setup; on a shared host
prefer a bounded cap (e.g. `hard memlock 1048576` = 1 GB) that's still ≥ the GPU
queue need. Report back what the host ended up with so we can note it.
