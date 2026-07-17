# RUNBOOK — macos-user backend end-to-end (you-drive, agent-advises)

**Who runs this:** YOU, on the Mac. The agent interprets output; **you run each
`yolo` command** (they self-escalate — each privileged step prompts for your
password). **Run them as your normal admin user, NOT under `sudo`.**
**Privilege:** the commands need admin (they create a hidden macOS user, write
root-owned `/var/yolo-jail`, install a Seatbelt profile) — but they escalate
per-op themselves. All bounded + reversible.

> ⚠️ **Do NOT prefix `yolo` with `sudo`.** These commands self-escalate; running
> the whole command as root makes `getpass.getuser()` return `root`, so the
> shared-workspace group ACL would be granted to `root` instead of you and
> host↔sandbox file sharing would silently break. The CLI now **refuses to run
> under sudo** (`_refuse_if_root`, added 2026-07-17 in response to this runbook's
> review) with a clear message — so this is enforced, not just advised. Run
> everything as plain `yolo`.

**What it proves:** the native no-VM backend actually runs an agent as the
hidden `_yolojail` user under Seatbelt, with `packages:` materialized via native
aarch64-darwin nix — the acceptance bar. Everything up to the real launch is
already unit-tested on Linux; this is the real-hardware confirmation.

---

## 0. ROLLBACK FIRST (know the exit before the entrance)
Everything this creates is removed by ONE command. If anything looks wrong at
any step, stop and run:
```
yolo macos-teardown               # deletes the _yolojail user + group + home
```
Blast radius, for your peace of mind (from the code):
- creates a **hidden** service user `_yolojail` (uid ≥600, `IsHidden 1`, stripped
  from `staff`) — never appears on the login screen.
- writes root-owned `/var/yolo-jail` (staged entrypoint + per-session Seatbelt
  profile) and `/etc/…` nothing on the container path; the macos-user path does
  NOT touch `/etc/nix` or your sudo policy (no NOPASSWD rule is ever installed).
- `macos-teardown` reverses the user/group/home; `macos-unshare <dir>` strips the
  ACL from a shared workspace. Nothing of yours outside the workspace is touched.

## 1. Preflight — inspect readiness WITHOUT changing anything (no sudo)
```
yolo check                         # look for the "macOS-user backend" section
```
Report what it says for: OS, sandbox-exec, sandbox user (expected: not
provisioned yet), python3, **nix present**, **flake.lock present**. This is the
readiness probe — a green-ish result here means preconditions are in place, not
that a run will succeed.

## 2. Dry-run the plan — still NO sudo, nothing executes
Put `runtime: "macos-user"` in a scratch workspace's `yolo-jail.jsonc` (or
`YOLO_RUNTIME=macos-user`), then:
```
cd /Users/Shared/yolo/some-test-project     # NEUTRAL ground (not under ~) — required
YOLO_RUNTIME=macos-user yolo --dry-run
```
This prints the FULL run plan — the Seatbelt profile, the bootstrap script, the
launch argv, the darwin `packages:` it WOULD materialize — and runs its
invariant checks, executing nothing. **Report the plan + any invariant
violations.** A clean dry-run is the gate before touching sudo.

> If the workspace is under your home dir, the plan will (correctly) refuse —
> the backend only shares neutral ground like `/Users/Shared/yolo`. Move it.

## 3. One-time setup — the privileged step (plain `yolo`, NOT `sudo`)
```
yolo macos-setup                    # do NOT prefix with sudo — it self-escalates
```
Expect prompts for your admin password (per privileged op). It: picks a free uid/gid, creates the
hidden `_yolojail` user, sets a random password (piped, never in argv),
provisions the shared root with an inheriting ACL, and prints readiness checks
(python3, sandbox-exec, nix). **Report the full output + the final verdict
(green ✓ or the ⚠ list).**

## 4. First real run under Seatbelt
Start WITHOUT packages first (isolate the sandbox launch from the nix build):
```
YOLO_RUNTIME=macos-user yolo -- bash -lc 'whoami; pwd; echo HELLO-FROM-SANDBOX'
```
Expect: `whoami` → `_yolojail`, `pwd` → your workspace, the echo prints. This
proves the sudo→env-i→sandbox-exec launch works. **Report output + any
sandbox-exec error.**

## 5. The acceptance bar — packages materialized natively
Add a package to the workspace config: `"packages": ["jq"]`, then:
```
YOLO_RUNTIME=macos-user yolo -- bash -lc 'which jq && jq --version'
```
Expect: `jq` resolves from a `/nix/store/...` path (the native aarch64-darwin
buildEnv), NOT `/usr/bin`. **Report the path `which jq` prints** — a
`/nix/store/*/bin/jq` is the acceptance bar met on real hardware. First run may
build/download the darwin closure (slow once); note if it did.

## 6. Real agent (optional, once 4–5 pass)
```
YOLO_RUNTIME=macos-user yolo -- claude
```
Confirm the agent starts, sees the workspace, and — the #1 footgun check —
does NOT see your host `~/.gitconfig`/`~/.ssh` (scrubbed HOME). Report.

## 7. Cleanup
```
yolo macos-teardown                # removes the _yolojail user + home
# if you ACL'd a workspace and want it plain again:
yolo macos-unshare /Users/Shared/yolo/some-test-project
```

---

## What to report per step (any failure is a precise bug for me)
- §1/§2: the check output + dry-run plan + any invariant violations.
- §3: setup output + verdict.
- §4: does it launch as `_yolojail`? any sandbox-exec error verbatim.
- §5: **the `which jq` path** (the acceptance-bar signal) + whether it built.
- §6: agent starts? host creds invisible?

## Likely rough edges I'd expect (so they're not surprises)
- **sudo prompt through a proxied TTY:** the launch runs under yolo's tty-proxy;
  if a sudo prompt appears mid-run and can't be answered, that's a known risk —
  report where it hung.
- **`/nix` not found / daemon not trusted:** the darwin build needs nix + a
  trusted user; if §5 fails there, paste the error — it's the same
  trusted-users wiring as the container path.
- **darwin build of a package with no aarch64-darwin build:** should warn-and-skip
  (not crash). If a package vanishes silently, report which.
