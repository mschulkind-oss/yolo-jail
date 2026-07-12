# Handoff — bring up the native macOS-user backend on real hardware

**For:** an agent (or human) with a **macOS arm64** machine and admin/sudo.
**Goal:** finish and verify the `runtime: "macos-user"` backend, which
isolates a coding agent in a dedicated macOS user + Seatbelt instead of a
Linux container. The design + honest security delta are in
[macos-native-user-sandbox-design.md](macos-native-user-sandbox-design.md);
this doc is the practical bring-up.

## What is already built and tested (on Linux CI)

Everything that can be written and unit-tested without a Mac is **done and
green** (`src/cli/macos_user.py`, `tests/test_macos_user.py`, 40 tests).
Nothing here is a stub — the artifacts are complete; only *executing* them
needs macOS.

- **Runtime seam.** `runtime: "macos-user"` (or `YOLO_RUNTIME=macos-user`)
  is a valid, validated runtime (`paths.py` `NATIVE_RUNTIMES`/`ALL_RUNTIMES`,
  `config.py`, `runtime.py`). It's **explicit opt-in — never auto-detected**,
  and errors clearly when selected off-macOS.
- **`run()` dispatch.** `run()` short-circuits to `run_macos_user(...)`
  before any container machinery; the podman/container paths are untouched.
- **Pure, tested builders** (assert SandVault parity):
  - `create_user_commands` / `delete_user_commands` — hidden account via
    `dscl`/`dseditgroup` (IsHidden, stripped from `staff`, shared group);
    the password is **never** in argv.
  - `workspace_acl_aces` / `workspace_acl_apply_script` — SandVault's
    dir/file-split inheriting ACL (files never gain execute).
  - `seatbelt_profile` — `(allow default)` with the load-bearing denies:
    all writes (re-allow workspace + sandbox home + scratch),
    `/Library/Keychains`, other users' homes under `/Users`, raw
    disk/`bpf`, `/Volumes` except boot.
  - `launch_argv` — `sudo -u … /usr/bin/env -i … sandbox-exec -f … -- <agent>`;
    the `HOME`/`USER`/`SHELL` identity trio is not caller-overridable.
  - `entrypoint_bootstrap_script` — reuses the stdlib-only entrypoint
    generators natively (shims, agent launchers, per-agent `CONFIG_WRITERS`),
    skipping the Linux-only boot steps, and installs the `yolo-log` helper.
  - `broker_socket_grant_commands` / `macos_log_wrapper_script` — loophole
    handling + the macOS unified-logging (`log`) analog of `yolo-journalctl`.
- **Orchestrator + commands.** `run_macos_user` is fully wired (install the
  root-owned 0444 profile via `sudo tee`, apply the ACL, run the bootstrap
  as the sandbox user, launch under the TTY proxy) but **guarded to macOS**
  (fails closed elsewhere). `yolo macos-setup` / `yolo macos-teardown`
  provision/remove the account. `yolo check` has a macos-user readiness
  block.

## What still needs a real Mac (your job)

These couldn't be executed on Linux CI — run and validate them, then fix
anything that differs from real macOS behavior:

1. **Account provisioning.** `yolo macos-setup`. Verify: the account is
   hidden (not on the login window), stripped from `staff`, has a home
   owned by it (watch for the Jamf root-owned-home bug — `chown -R` if so),
   and `dscl . -read /Users/_yolojail` looks right. Confirm the password is
   never visible in `ps`.
2. **`sandbox-exec` viability on your macOS version.** It's deprecated and
   prints a stderr warning — confirm it still *works* on Sequoia/Tahoe
   arm64. Load the generated profile: write `seatbelt_profile(ws)` to a
   file and run `sandbox-exec -f profile.sb -- /bin/echo ok`. If a strict
   deny breaks agent startup, note which broad reads each agent needs
   (`log stream --predicate 'sender=="Sandbox"'` while it fails) and widen
   the profile minimally.
3. **The credential boundary.** As `_yolojail`, after `chmod 750 ~` on the
   host home, verify the agent **can** edit the workspace but **cannot**
   read the host `~/.ssh`/`~/.gitconfig`/login keychain. This is the
   SandVault-parity guarantee — it must hold before advertising it.
4. **Workspace ACL round-trip.** Apply the ACL, then confirm host-side
   edits and sandbox-side edits stay visible both ways across git
   rename-and-replace and an editor save. Add `yolo macos-fix-permissions`
   if drift appears (the design doc flags this).
5. **The entrypoint bootstrap as the sandbox user.** `run_macos_user` runs
   `sudo -u _yolojail python3 <bootstrap>`; confirm it populates
   `~_yolojail/.yolo-shims` + real `~/.claude` / `~/.codex` / … and that an
   agent launches. The bootstrap rebinds `entrypoint.HOME`/`WORKSPACE` — if
   any `/workspace` or `/mise` literal leaks through, parametrize it
   (see the design doc's "parametrize the /workspace literals" note).
6. **sudo policy.** Per-run `sudo -u` needs a NOPASSWD sudoers rule scoped
   to `sudo -u _yolojail` + `launchctl bootout` (SandVault's model), or the
   session prompts for a password. Decide + install a `visudo -c`-validated
   rule (open question #2 in the design doc). This is the "don't make me run
   the autoapprover" piece — wire it so a run is non-interactive.
7. **Broker loophole.** If using Claude via the OAuth broker, run
   `broker_socket_grant_commands(<singleton socket>)` and confirm uid 449
   connects and `getpeereid` attests the real uid.

## How to run it

```bash
# one-time, needs admin (creates the hidden _yolojail account):
yolo macos-setup

# in a project, opt into the native backend:
echo '{ "runtime": "macos-user", "agents": ["claude"] }' > yolo-jail.jsonc
yolo check                 # readiness probes for the backend
yolo -- claude             # launches natively as _yolojail + Seatbelt

# optional macOS log access inside the sandbox:
#   add  "macos_log": "user"  to yolo-jail.jsonc  → `yolo-log show ...`

# remove the account when done:
yolo macos-teardown
```

## Guardrails / scope

- **Match SandVault's security level — do not relax it.** The profile
  denies and the ACL split are the standard. Any *loosening* (e.g. widening
  reads beyond what an agent's startup needs, dropping a deny) needs a
  design doc + approval, per the project owner. Widening the startup
  read-allow set minimally to make an agent boot is expected (step 2); a
  structural relaxation is not.
- **Container backends are the default and untouched.** `macos-user` stays
  opt-in.
- **Attribution.** The design is adapted from
  [SandVault](https://github.com/webcoyote/sandvault) (Apache-2.0);
  reimplemented, credited in `README.md` + `NOTICE`. Keep that intact.

## Where things live

| Piece | File |
|---|---|
| Builders + orchestrator + setup/teardown | `src/cli/macos_user.py` |
| Runtime seam | `src/cli/paths.py`, `src/cli/runtime.py`, `src/cli/config.py` |
| `run()` dispatch | `src/cli/run_cmd.py` (search `macos-user`) |
| check readiness block | `src/cli/check_cmd.py` (`_check_macos_user_backend`) |
| Tests (Linux CI) | `tests/test_macos_user.py` |
| Design + security delta | `docs/macos-native-user-sandbox-design.md` |
