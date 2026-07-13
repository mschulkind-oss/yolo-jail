# Handoff — bring up the native macOS-user backend on real hardware

**For:** an agent (or human) with a **macOS arm64** machine and admin/sudo.
**Goal:** finish and verify the `runtime: "macos-user"` backend, which
isolates a coding agent in a dedicated macOS user + Seatbelt instead of a
Linux container. The design + honest security delta are in
[macos-native-user-sandbox-design.md](macos-native-user-sandbox-design.md);
this doc is the practical bring-up.

> **Status: EXPERIMENTAL — not yet verified end-to-end on real macOS.**
> Everything that can be authored and tested on Linux is done and green, and
> an adversarial review closed the four blockers that would have stopped a
> first run (see below). But no step has executed on a Mac yet. A green
> `yolo check` proves *readiness* (preconditions in place), **not**
> *runnability*. The honest pre-launch gate is `yolo run --dry-run`; the
> definitive test is a real run here.

## What is already built and tested (on Linux CI)

Everything that can be written and unit-tested without a Mac is **done and
green** (`src/cli/macos_user.py`, `tests/test_macos_user.py`). Nothing here
is a stub — the artifacts are complete; only *executing* them needs macOS.

- **Runtime seam.** `runtime: "macos-user"` (or `YOLO_RUNTIME=macos-user`)
  is a valid, validated runtime (`paths.py` `NATIVE_RUNTIMES`/`ALL_RUNTIMES`,
  `config.py`, `runtime.py`). It's **explicit opt-in — never auto-detected**,
  and errors clearly when selected off-macOS.
- **`run()` dispatch.** `run()` short-circuits to `run_macos_user(...)`
  before any container machinery; the podman/container paths are untouched.
- **Pure, tested builders** (assert SandVault parity): `create_user_commands`
  / `delete_user_commands`, `workspace_acl_aces` / `workspace_acl_apply_script`
  (dir/file split + ancestor traversal), `seatbelt_profile`, `launch_argv`,
  `entrypoint_bootstrap_script`, `broker_socket_grant_commands`,
  `macos_log_wrapper_script`, plus the hardening builders below.
- **Run plan + invariants.** `build_run_plan` assembles the whole session as
  data; `plan_invariants` statically checks it. `yolo run --dry-run` prints
  the plan (profile, ACL, staged commands, bootstrap, launch argv, sudoers)
  and its invariant results on **any OS** and executes nothing.
- **Orchestrator + commands.** `run_macos_user` is fully wired but **guarded
  to macOS** (fails closed elsewhere). `yolo macos-setup` /
  `yolo macos-teardown` provision/remove the account **and** the sudoers
  rule. `yolo check` has an honest, experimental-labelled readiness block.

## Blockers found by adversarial review — and how they're already addressed

A review of the run path against real macOS behavior found four CONFIRMED
blockers. All four are **fixed in code** (pure builders + a fail-closed
preflight), so your job is to *confirm on hardware*, not to discover them:

- **B1 — passwordless sudo.** Without it, every run prompts on `/dev/tty`,
  and the launch (in a fresh proxied pty) prompts *again* and hangs.
  `yolo macos-setup` now installs a `visudo`-validated
  `/etc/sudoers.d/yolo-jail` (0440 root:wheel, **dot-free name**, self-checked
  with `sudo -n -l`) scoped to the exact absolute command paths the run uses.
  The run path preflights with `sudo -n` and fails closed with an actionable
  message instead of hanging.
- **B2 — `/usr/bin/python3` is the xcode-select stub** when the Command Line
  Tools are absent (it errors / triggers a GUI install, never runs Python).
  `resolve_python()` prefers a Homebrew/Nix python3 and only falls back to
  the system path; the run path verifies it actually executes *as the sandbox
  user* via `sudo -n`.
- **B3 — the bootstrap imported `entrypoint` from the host checkout**, which
  the credential-hiding `chmod 750 ~` makes untraversable to the sandbox uid.
  It now **stages** the stdlib-only `entrypoint` package into the root-owned
  `/var/yolo-jail` and imports from there. `JAIL_HOME` is set **before**
  `import entrypoint` (the HOME-derived path constants freeze at import), and
  the git identity is baked into the bootstrap env so `configure_git/jj`
  write the right identity under the scrubbed env.
- **B4 — a workspace nested under the host home was unreachable** at both
  layers. The ACL now grants a **traversal-only** ACE
  (`search,readattr,readsecurity`, no read-data) on each ancestor, and the
  Seatbelt profile allows `file-read-metadata` on each ancestor component —
  path resolution succeeds while sibling files stay unreadable.

Also hardened: the run **aborts on bootstrap failure** (no more launching a
shim-less agent), and `--login/--set-home` was dropped from the *bootstrap*
sudo (staged import + explicit cwd sidestep the login-zsh/`zprofile`
fragility). The *launch* argv keeps `--login` for now — see caveats.

## Bring-up sequence (do this on the Mac, in order)

**Stepping stone — break the chicken-and-egg via `macos/host/arm` first.**
You may be reading this because the human is trying to *start* a jail to run
the handoff, but macos-user isn't runnable yet. Don't bootstrap the agent
*inside* macos-user. Instead: run the agent **on the macOS host directly**
(or under the existing container backend if Apple Container/podman is set
up), do the bring-up below from there, and only switch a workspace to
`runtime: "macos-user"` once step 7 passes. The dry-run (step 5) needs no
Mac and no account, so it can be inspected from anywhere first.

1. **Admin shell.** `dseditgroup -o checkmember -m $(id -un) admin` → `yes`.
   A Standard user cannot `sudo` at all and `macos-setup` will fail.
2. **A real python3.** `brew install python` (→ `/opt/homebrew/bin/python3`)
   or `xcode-select --install`. Do **not** rely on the bare `/usr/bin/python3`
   stub. Step 5's dry-run shows which interpreter resolved.
3. **`yolo macos-setup`.** Enter your admin password once at the `/dev/tty`
   prompt. Confirm it (a) creates `_yolojail` (hidden, off `staff`, home
   owned by it — watch the Jamf root-owned-home bug, `chown -R` if so),
   (b) writes `/etc/sudoers.d/yolo-jail` (dot-free, 0440 root:wheel) validated
   by `visudo -cf`, and (c) self-verifies with `sudo -n -l`. Double-check:
   `sudo -n -u _yolojail /usr/bin/true` exits 0 with **no** prompt.
4. **Interpreter runs as the sandbox user.**
   `sudo -n -u _yolojail <resolved-interp> -c 'import sys; print(sys.version)'`
   exits 0 (proves B2 closed and NOPASSWD covers it).
5. **`yolo run --dry-run`** in a target workspace. Inspect the printed
   profile, ACL script, interpreter, staged commands, bootstrap, launch argv,
   and sudoers text; confirm **all plan invariants pass** and every sudo path
   matches the sudoers rule.
6. **Pick a workspace outside TCC-protected dirs** — not `~/Documents`,
   `~/Desktop`, `~/Downloads`, or iCloud. A hidden service account has no Aqua
   session and can't be TCC-prompted. (If it must live there, grant the
   parent terminal app Full Disk Access first.) `/Users/<you>/code/...` and
   `/Users/Shared/...` are fine.
7. **The real run, with the sandbox log open.** In a second terminal:
   `log stream --predicate 'sender=="Sandbox"'`. Then `yolo run` (or
   `yolo -- claude`). Confirm **no password prompt** at the proxied launch
   (B1), and watch for Seatbelt denials on the workspace's **ancestor
   components** (validates the B4 Seatbelt fix on your OS).
8. **Prove the bootstrap ran.** `~_yolojail/.yolo-shims` and the per-agent
   configs exist, and `sudo -u _yolojail cat ~_yolojail/.gitconfig` shows the
   injected identity (proves B3 + the git-identity fix).
9. **Prove the credential boundary.** As the sandboxed agent, attempts to read
   the host `~/.ssh/id_*`, `~/.gitconfig`, `~/.aws/credentials`, and
   `/Library/Keychains` must **all fail**; the agent **can** read/write inside
   the workspace and `cd` into it (validates the ancestor-search ACEs). This
   is the SandVault-parity guarantee — it must hold before advertising it.
10. **The nested-under-`chmod 750`-home case** (if your workspace is under the
    host home) is the real test of the design's core tension — success here
    (agent reaches the workspace via the ancestor ACEs + `file-read-metadata`)
    is the sign that conflict is resolved.

## Hardware caveats — confirm, don't assume (from the review)

- **Seatbelt per-component enforcement (B4 half).** Whether
  `file-read-metadata` on each ancestor is sufficient for path resolution into
  a read-denied ancestry on your OS. Watch `log stream --predicate
  'sender=="Sandbox"'` during agent start. This is the one fix whose *form* is
  speculative (the *need* is certain) — if metadata isn't enough, a
  per-ancestor `(allow file-read* (literal ...))` on just the ancestor dirs
  (not `subpath`) is the fallback.
- **`sandbox-exec` viability.** Deprecated, prints a stderr warning — confirm
  it still *works* on Sequoia/Tahoe arm64. If a strict deny breaks startup,
  note which broad reads the agent needs from the Sandbox log and widen the
  profile **minimally** (see guardrails).
- **TCC for the hidden service account** on protected dirs / external
  `/Volumes` — version-dependent and tied to whether the parent terminal
  holds Full Disk Access. Mitigation is workspace placement (step 6), not a
  code relaxation. A warn-if-workspace-under-TCC-dir check is a reasonable
  add.
- **`sudo --login` on the *launch* argv.** Kept for now (`env -i` already
  scrubs and the inner `zsh -c` does the workspace `cd`). If `_yolojail`'s
  login zsh / a broken `/etc/zprofile` interferes, drop `--login/--set-home`
  from `launch_argv` too (the bootstrap already dropped them).
- **sudoers arg-less command match.** The rule pins command *paths* but omits
  args (paths vary per session). Some hardened environments will want tighter
  specs; validate the effective policy with `sudo -n -l` regardless.

## How to run it

```bash
# one-time, needs admin (creates _yolojail + installs the sudoers rule):
yolo macos-setup

# inspect the full plan — works on ANY OS, no account needed:
yolo run --dry-run

# in a project, opt into the native backend:
echo '{ "runtime": "macos-user", "agents": ["claude"] }' > yolo-jail.jsonc
yolo check                 # honest readiness probes (experimental-labelled)
yolo -- claude             # launches natively as _yolojail + Seatbelt

# optional macOS log access inside the sandbox:
#   add  "macos_log": "user"  to yolo-jail.jsonc  → `yolo-log show ...`

# remove the account + sudoers rule when done:
yolo macos-teardown
```

## Guardrails / scope

- **Match SandVault's security level — do not relax it.** The profile denies
  and the ACL split are the standard. Any *loosening* (widening reads beyond
  what an agent's startup needs, dropping a deny) needs a design doc +
  approval, per the project owner. Widening the startup read-allow set
  minimally to make an agent boot is expected; a structural relaxation is not.
- **Container backends are the default and untouched.** `macos-user` stays
  opt-in.
- **Attribution.** The design is adapted from
  [SandVault](https://github.com/webcoyote/sandvault) (Apache-2.0);
  reimplemented, credited in `README.md` + `NOTICE`. Keep that intact.

## Where things live

| Piece | File |
|---|---|
| Builders + run plan + orchestrator + setup/teardown | `src/cli/macos_user.py` |
| Runtime seam | `src/cli/paths.py`, `src/cli/runtime.py`, `src/cli/config.py` |
| `run()` dispatch + `--dry-run` | `src/cli/run_cmd.py` (search `macos-user`) |
| check readiness block | `src/cli/check_cmd.py` (`_check_macos_user_backend`) |
| Tests (Linux CI) | `tests/test_macos_user.py` |
| Design + security delta | `docs/macos-native-user-sandbox-design.md` |
