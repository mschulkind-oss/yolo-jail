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
  the plan (profile, ACL, staged commands, bootstrap, launch argv) and its
  invariant results on **any OS** and executes nothing.
- **Orchestrator + commands.** `run_macos_user` is fully wired but **guarded
  to macOS** (fails closed elsewhere). `yolo macos-setup` /
  `yolo macos-teardown` provision/remove the account. `yolo check` has an
  honest, experimental-labelled readiness block.

## Blockers found by adversarial review — and how they're already addressed

A review of the run path against real macOS behavior found four CONFIRMED
blockers. All four are **fixed in code** (pure builders + a fail-closed
preflight), so your job is to *confirm on hardware*, not to discover them:

- **B1 — per-run `sudo`.** The privileged steps (`sudo -u _yolojail …`, the
  root-owned profile install) prompt for the admin password. That's expected
  and fine: the launch runs under `run_with_proxy`, which forwards stdin, so
  the prompt is answerable inline (this is SandVault's posture too). We
  deliberately do **NOT** install a NOPASSWD sudoers rule — changing the
  host's sudo policy is the user's call, not ours. `run_macos_user` prints a
  heads-up that sudo may prompt. A user who wants non-interactive runs can add
  their own sudoers rule; yolo neither requires nor endorses that.
- **B2 — `/usr/bin/python3` is the xcode-select stub** when the Command Line
  Tools are absent (it errors / triggers a GUI install, never runs Python).
  `resolve_python()` prefers a Homebrew/Nix python3 and only falls back to
  the system path; a `None` result fails the dry-run invariants.
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
3. **`yolo macos-setup`.** Enter your admin password when sudo prompts.
   Confirm it creates `_yolojail` (hidden, off `staff`, home owned by it —
   watch the Jamf root-owned-home bug, `chown -R` if so):
   `dscl . -read /Users/_yolojail` looks right, and the password is never
   visible in `ps`.
4. **Interpreter runs as the sandbox user.**
   `sudo -u _yolojail <resolved-interp> -c 'import sys; print(sys.version)'`
   exits 0 (proves B2 closed — a real interpreter, not the stub).
5. **`yolo run --dry-run`** in a target workspace. Inspect the printed
   profile, ACL script, interpreter, staged commands, bootstrap, and launch
   argv; confirm **all plan invariants pass**.
6. **Pick a workspace outside TCC-protected dirs** — not `~/Documents`,
   `~/Desktop`, `~/Downloads`, or iCloud. A hidden service account has no Aqua
   session and can't be TCC-prompted. (If it must live there, grant the
   parent terminal app Full Disk Access first.) `/Users/<you>/code/...` and
   `/Users/Shared/...` are fine.
7. **The real run, with the sandbox log open.** In a second terminal:
   `log stream --predicate 'sender=="Sandbox"'`. Then `yolo run` (or
   `yolo -- claude`). Answer the sudo password prompt inline (it's forwarded
   through the TTY proxy), and watch for Seatbelt denials on the workspace's
   **ancestor components** (validates the B4 Seatbelt fix on your OS).
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
- **Per-run sudo prompt.** Expected, not a bug — we don't change the host's
  sudo policy. If the prompt is disruptive in your workflow, add your own
  NOPASSWD sudoers rule (your call); yolo won't do it for you.

## How to run it

```bash
# one-time, needs admin (creates the hidden _yolojail account):
yolo macos-setup

# inspect the full plan — works on ANY OS, no account needed:
yolo run --dry-run

# in a project, opt into the native backend:
echo '{ "runtime": "macos-user", "agents": ["claude"] }' > yolo-jail.jsonc
yolo check                 # honest readiness probes (experimental-labelled)
yolo -- claude             # launches natively as _yolojail + Seatbelt

# optional macOS log access inside the sandbox:
#   add  "macos_log": "user"  to yolo-jail.jsonc  → `yolo-log show ...`

# remove the account when done:
yolo macos-teardown
```

## First real-hardware run — findings to fix (2026-07-14)

The backend was launched on a real Mac for the first time (`yolo -- claude`
in `/Users/Shared/sv-matt/repos/forms`, a large repo with a populated
`.venv`). It did **not** crash on the design blockers B1–B4 — the failures
below are **new, UX/performance issues in the setup path**, not security-model
regressions. Fix these before the backend is pleasant (or trustworthy-looking)
to use.

**Symptom the user saw:** after the "Setting up the sandbox …" line, a
multi-minute silent pause that looked exactly like a hang (they `^C`'d out of
it twice on earlier attempts), then an ACL error, then a **second**
`Password:` prompt at the very end of the run.

**Root cause — the workspace ACL walk (`workspace_acl_apply_script`,
`macos_user.py:320`, run at `macos_user.py:1104`):**

- It's a `find`-based walk over the **entire workspace**, forking `chmod -h +a`
  **once per file and twice per directory**. On a repo with a large `.venv`
  (thousands of `site-packages` files) that's tens of thousands of serial
  process spawns → the multi-minute pause.
- The walk emits **no per-item output** and the single heads-up line lumps it
  in with two other steps, so a slow ACL pass is indistinguishable from a
  freeze. This is why it reads as a hang.
- It ACLs files it arguably shouldn't (`.venv`, and it hit
  `chmod: Failed to set ACL on … flask-3.1.3.dist-info/licenses: Operation not
  permitted` — a file whose ACL couldn't be set, surfaced as the
  "workspace ACL grant reported an error" warning at `macos_user.py:1105`).
- **The re-prompt is a consequence of the slowness:** the ACL walk (step 3,
  runs as the *user*, no sudo) outlasts sudo's ~5-min credential timestamp, so
  the step-4 bootstrap sudo (`macos_user.py:1113`) has to prompt for the
  password **again** mid-run. So the slow silent step both looks like a hang
  *and* forces a second password entry.

**Fixes to make (recommended order — first two are low-risk, high-impact):**

1. **Batch all sudo work up front**, before the slow ACL walk, so one password
   entry covers the whole run and the ACL pass can't push the bootstrap past
   the sudo timestamp. Today the order is: profile install (sudo) → ACL walk
   (no sudo, slow) → bootstrap (sudo). Reordering so both sudo phases precede
   the ACL walk removes the double prompt.
2. **Show progress on the ACL walk** — a spinner + running file count (or at
   minimum a "this can take a minute on large repos" note). Silence here is the
   single biggest "is it hung?" trap.
3. **Scope the ACL tree.** Don't blanket-ACL `.venv`/`node_modules`/`.git`
   internals — most are throwaway the sandbox rebuilds, and some (like the
   flask `licenses` dir above) can't even take the ACE. Options: make the walk
   `.gitignore`-aware, or skip known-heavy dirs. **Caveat:** think about which
   of these the sandbox still needs *read* access to before excluding them —
   don't break the agent's ability to import from a shared venv if that's the
   intent. This one needs a moment of design thought, not just a filter.
4. **Speed up the walk itself** even where it must run: batching `chmod` calls
   (feed `find` output to fewer invocations) or applying inheriting ACEs at the
   directory level and letting inheritance do the rest instead of touching
   every existing file, would cut the fork count dramatically.

None of these touch the security posture — they're about not looking broken and
not making the user type their password twice. Keep the SandVault-parity ACL
*semantics* (dir/file split, workspace-only) intact while changing *how/when*
they're applied.

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
