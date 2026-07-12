# Native macOS user-account sandbox — design proposal

**Status:** proposal (Phase 0 spike not yet run)
**Audience:** anyone weighing a native macOS/arm64 yolo-jail that isolates
via a **separate macOS user account** instead of the current Linux
container (which "arch switches" into a Linux VM on Mac).
**Proposed seam:** a third `runtime` value, `"macos-user"`, alongside
`"podman"` and `"container"`.

## Context — why this exists

Today's macOS story routes agents through a **Linux container**: rootless
Podman (Podman Machine) or Apple Container. On arm64 that's an *arch
switch* — arm64 host → Linux guest — dragging along a baked NixOS image
(`src/flake.nix` `entrypointPkg` + `ociImage`), the build/load pipeline
(`src/cli/image.py`), and per-side venv/Mach-O shadow-mount hacks
(`_venv_shadow_mount_args`, `_ac_materialize_under_ws_state` in
`src/cli/run_cmd.py`) that exist *only* because host Mach-O binaries can't
run in a Linux guest and Apple Container's ~22-mount ceiling forces
copy-instead-of-mount.

The ask: a **native** backend that runs agents as arm64 macOS binaries
under a dedicated unprivileged user — no VM, no image, no arch switch.

This is a real, proven pattern. **SandVault** (`sv`, webcoyote,
Apache-2.0, `brew install sandvault`) is the direct reference: it runs
Claude/Codex/Gemini/opencode in full YOLO mode as a dedicated *hidden*
unprivileged macOS user hardened with Seatbelt — exactly what we want.
Anthropic's own `@anthropic-ai/sandbox-runtime` validates the model too:
it uses a dedicated `srt-sandbox` account on **Windows**, and only falls
back to same-user Seatbelt on macOS. So "separate account + an OS sandbox
profile keyed to that account" is the preferred design where the OS
supports it; SandVault is the one that does it on macOS today. We would,
in effect, port SandVault's design into yolo-jail's existing backend seam.

## The honest verdict, up front

A dedicated-user + Seatbelt boundary is **credible and strictly stronger
than the same-user Seatbelt wrappers people already trust** (Claude Code
`/sandbox`, Safehouse, the `claude-sandbox` family) — because it adds
UID / login-keychain / TCC-database separation on top of the profile.

But it is **materially weaker than the Linux container**, and the deltas
are structural, not cosmetic:

- **Shared kernel.** Everything runs on host XNU. A kernel LPE escapes
  the sandbox entirely; the container interposes a VM/hypervisor boundary.
  The container's guarantee is "the host is simply not reachable"; ours is
  "the host is harder to reach and reading it takes a kernel bug."
- **Deprecated enforcement.** `sandbox-exec` has been deprecated since
  10.12 (2016), prints a warning on every invocation, and its SBPL profile
  format is officially undocumented ("please don't ship a product based on
  it" — Apple DTS). Still fully functional on Sequoia/arm64 and depended on
  by Chrome, Bazel, Swift PM, Codex, and Anthropic's runtime — but a
  removal risk. If it vanished we'd still have the user-account layer
  (SandVault-minus-Seatbelt ≈ a real boundary).
- **~95% escape coverage, not 100%** — raw `socket()` fds and some Mach
  IPC classes can't be closed by a profile.
- **No resource limits.** No cgroup analog for `--cpus`/`--memory`/PID caps.
- **Credential boundary is not automatic.** `~/.ssh`, `~/.gitconfig`,
  `~/.aws`, cloud tokens are plain files, not TCC-protected, and a 0755
  home lets the sandbox user traverse in and read them. It only holds if
  we deliberately `chmod 750 ~` **and** deny those reads in the profile.
  Keychain-resident secrets are protected for free; TCC is not the
  credential boundary (it guards Documents/Downloads/Photos, not dev creds,
  and has a long bypass history).
- **Needs root per run** (`sudo -u`), versus rootless Podman needing none.

**Position it as opt-in, with the container as the security-max default.**
Prefer the container for untrusted/adversarial code, hard resource caps, a
true kernel boundary, or exfil-sensitive work. The native backend is
defensible for a *trusted-but-autonomous* coding agent where the goal is
"don't let YOLO mode wreck my host or read my creds."

## Isolation model

A dedicated **hidden service account**, created once and reused.

- **Create** (idempotent, in a `yolo` setup path):
  `sudo sysadminctl -addUser _yolojail -UID 449 -GID 449 -shell /bin/zsh
  -home /Users/_yolojail -adminUser <admin> -adminPassword -`
  (password via prompt/stdin, **never** a literal arg — it shows in `ps`),
  then `dscl . create /Users/_yolojail IsHidden 1` (off the login window)
  and `dseditgroup -o edit -d _yolojail -t user staff` (not a real login
  user). Verify home isn't root-owned (known Jamf bug); `createhomedir` if
  needed.
- **Teardown:** `sudo launchctl bootout user/449` +
  `sysadminctl -deleteUser _yolojail -keepHome`.
- **Per-run launch:**
  `sudo -u _yolojail env -i HOME=/Users/_yolojail TERM=$TERM PATH=…
  /usr/bin/sandbox-exec -f /var/yolo-jail/profile-<session>.sb -- <agent argv>`.
  `env -i` is **load-bearing**: without a clean env, `HOME` still resolves
  to the *admin* home and the agent reads the host `~/.gitconfig`/`~/.ssh`
  — the #1 documented footgun. `sudo -u` preserves the controlling TTY so
  the agent REPL works; do **not** use `launchctl asuser` for the headless
  CLI (Apple DTS: it "does not set the security context correctly").
- **Workspace share** (live, same inodes, no copy): the host user grants
  an inheriting ACL — `chmod -R +a "_yolojail allow
  read,write,…,file_inherit,directory_inherit" <repo>` (no root needed —
  the host user owns the tree). `file_inherit`+`directory_inherit` solve
  the UID mismatch both ways and sidestep the umask-022 trap of a plain
  setgid-group scheme. Budget a `yolo fix-permissions` for ACL drift
  (SandVault ships `sv build --rebuild` for exactly this).
- **Credential hiding** (two layers, both required): (a) structural — a
  different UID can't unlock the host login keychain (cryptographically
  gated) and starts with a zero-grant TCC db; (b) the crux — close the
  home-traversal hole with `chmod 750 ~` **and** a root-owned `0444`
  Seatbelt profile that denies `file-read*` under `/Users` except the
  workspace + `/Users/_yolojail`, and denies `/Library/Keychains`
  (System.keychain is world-readable 644), `/Volumes` except boot, and
  `/dev/rdisk*`/`/dev/bpf`.
- **Agent auth: copy-in, never mount-in.** `_yolojail` keeps its own
  persistent `~/.claude`; the user does the Claude OAuth `/login` once
  inside the sandbox account and it persists. Git identity is written into
  the sandbox user's own `~/.gitconfig` via the existing `configure_git`/
  `configure_jj` (`src/entrypoint/identity.py`, `YOLO_GIT_NAME`/`_EMAIL`).
  The claude-oauth-broker is reached as a same-box socket by permission
  (chmod/ACL for uid 449), not by bind mount.

## Architecture — slotting into the existing seam

The backend adds a third value to the existing runtime switch; the two
container backends are untouched.

1. **Seam.** `SUPPORTED_RUNTIMES` (`src/cli/paths.py`) gains `"macos-user"`.
   `_runtime(config)` / `_runtime_for_check(config)` (`src/cli/runtime.py`)
   already resolve `YOLO_RUNTIME` env → `config['runtime']` → auto-detect
   against that tuple, so the string flows through unchanged. (Auto-detect
   stays explicit opt-in — see open questions.)
2. **Dispatch.** In `run()` (`src/cli/run_cmd.py`), right after
   `runtime = _runtime(config)`, short-circuit:
   `if runtime == "macos-user": return run_macos_user(...)` — *before* the
   `[runtime, "run", …]` argv blocks, all `-v` mounts, the `-e` env block,
   `--userns`/`--uidmap`/`--device`, and the `_jail_image` + `yolo-entrypoint`
   tail. For cleanliness, first extract the container argv assembly into
   `build_container_argv(runtime, …)` so `run()` forks cleanly between the
   two container backends and the native one.
3. **Entrypoint reused in-process.** `src/entrypoint` is stdlib-only and
   env-driven, and already runs outside a container in a temp HOME via
   `_entrypoint_preflight` — which *proves* the generators run natively.
   `run_macos_user` imports entrypoint as a library and calls a trimmed
   sequence **as the sandbox user**: `generate_shims`,
   `generate_agent_launchers`, `generate_bashrc`/`…bootstrap`/`…mise_config`,
   `generate_mcp_wrappers`, `configure_git`/`configure_jj`, and the
   `CONFIG_WRITERS` per-agent loop (already gated on `YOLO_AGENTS`). Only
   *inputs* change: rebind `WORKSPACE` to the real repo path; point
   `HOME`/`MISE_DATA_DIR`/`NPM_CONFIG_PREFIX`/`GOPATH` at the sandbox user's
   real locations; parametrize the `/workspace` literals and the chromium
   path. **Skip** the Linux-only boot steps (`configure_timezone`,
   `generate_ld_cache`, `setup_cgroup_delegation`, the iptables/socat port
   forwarding) — all no-ops on a native mac.
4. **Loopholes collapse.** `discover_loopholes`/manifests/`run_doctor_checks`
   reused as-is; only `runtime_args_for()` is replaced. The
   `/run/yolo-services` bridging and the entire `broker_relay.py`
   indirection **disappear** — a socket-file bind mount was the only reason
   the relay existed. The sandbox user connects to the broker singleton
   socket directly (chmod/ACL for uid 449), and `getpeereid` attestation
   now returns a *real* macOS uid — a stronger identity signal than a
   mapped container uid. cgroup-delegate and journal-bridge loopholes drop
   (no macOS analog; already skipped on Apple Container).
5. **Lifecycle.** `find_running_container`/`find_existing_container`/`ps`
   get a `macos-user` branch checking a pidfile/launchd job keyed by
   `container_name_for_workspace` (kept as the session key). `run_with_proxy`
   (`src/cli/tty_proxy.py`) still wraps the launch — now around the local
   `sudo`/`sandbox-exec` child (simpler job control, no guest PID ns).

### Reused vs. new

**Reused, backend-agnostic (no / trivial change):** `src/cli/config.py`
(add the runtime enum value only), `src/entrypoint/agent_registry.py`, the
`CONFIG_WRITERS` + all `configure_*` in `agent_configs.py`,
`src/cli/agents_md.py` (briefing + skills generation), `shims.py`,
`shell.py`, `identity.py`, `src/loopholes.py` (all but `runtime_args_for`),
`_inject_agent_yolo_flags`, and `_entrypoint_preflight` as the
proof-of-concept for in-process generation.

**New native code** (e.g. `src/cli/macos_user.py`): `ensure_sandbox_user()`
(idempotent create/hide/de-staff/verify), `ensure_workspace_acl()` +
`fix-permissions`, `write_seatbelt_profile()` (root-owned 0444
deny-default `.sb`), `run_macos_user()` (orchestrate in-process generation
→ broker socket perms → `sudo -u … sandbox-exec …` under `run_with_proxy`),
the `macos-user` lifecycle branches, the `build_container_argv` extraction,
and a one-time setup command that installs a `visudo -c`-validated
NOPASSWD sudoers rule scoped to `sudo -u _yolojail` + `launchctl bootout`.

**Dropped for this backend:** `src/flake.nix` image, `src/cli/image.py`,
all `-v`/userns/device argv, venv/Mach-O shadow mounts, `/run/yolo-services`
bridging + `broker_relay.py`, iptables/socat plumbing, `entrypoint/system.py`
Linux fixups.

## macOS hard limits to know

- **No GUI apps as another user** (WindowServer refuses cross-user
  connections): Xcode.app UI won't run; CLI `xcodebuild` does.
- **No nested Seatbelt**: swift/xcodebuild need `--no-sandbox` /
  `SWIFTPM_DISABLE_SANDBOX=1` — so yolo's **nested-jail feature has no
  native equivalent** under this backend.
- **Homebrew perms** can break under a restrictive host umask
  (`chmod -R o+rX /opt/homebrew` may be needed).
- **Exfil is not solved** — with network, the agent can phone home.
  SandVault's own threat model is "don't wreck the host," not "prevent
  exfiltration." Matching yolo's proxy model, a localhost-proxy-only egress
  rule + host-side filtering proxy could be added later (TLS still not
  terminated → domain-fronting gap).

## Phased plan

- **Phase 0 — spike (throwaway, GO/NO-GO gate).** On a real Sequoia/arm64
  Mac, by hand: create/hide/teardown `_yolojail`; share a repo via an
  inheriting `chmod +a` ACL; run
  `sudo -u _yolojail env -i HOME=… sandbox-exec -f profile.sb -- claude
  --dangerously-skip-permissions` and verify the agent **can** edit the
  workspace but **cannot** read the host `~/.ssh`/`~/.gitconfig` after
  `chmod 750 ~`. Confirm `sandbox-exec` still works and note the broad
  read-allows Claude/Codex need at startup. ~2–3 days. This single spike
  de-risks user creation, the ACL share, Seatbelt viability, and the
  credential boundary at once. **Nothing lands in `src/cli/` before it.**
- **Phase 1 — minimal backend behind the switch.** Add the runtime value,
  extract `build_container_argv`, add the `run_macos_user` dispatch, wire
  in-process entrypoint generation (skip Linux boot steps, rebind paths),
  launch under `run_with_proxy`. Assume the user/ACL are set up manually.
  No credential hardening yet, no auto user creation. ~1–2 weeks.
- **Phase 2 — credential boundary + loopholes + lifecycle.**
  `ensure_sandbox_user()`/`ensure_workspace_acl()`, the setup command +
  NOPASSWD sudoers, `chmod 750 ~` enforcement, collapse the loopholes
  bridging (drop `broker_relay.py`, chmod/ACL the broker socket, verify
  `getpeereid`), copy-in auth, teardown. ~1–2 weeks.
- **Phase 3 — Seatbelt hardening + docs + CI.** Ship the root-owned 0444
  deny-default profile, optional localhost-proxy egress, a profile-debug
  helper (`log stream --predicate 'sender=="Sandbox"'`), the nested/Seatbelt
  escape hatches, the honest security delta in `AGENTS.md`/`config-ref`, and
  a nightly macOS CI job exercising create→run→teardown (extends the
  existing macos-26 nightly). ~1–2 weeks.

Container backends stay the default and untouched at every phase;
`macos-user` is opt-in via `YOLO_RUNTIME`/config until Phase 3 sign-off.

## Open questions

1. **`sandbox-exec` longevity** — pre-invest in an Endpoint Security
   fallback, or accept the risk with a documented "fall back to
   user-account-only" posture?
2. **Root policy** — require per-run `sudo` + NOPASSWD sudoers (SandVault's
   model), or a pre-installed LaunchDaemon (`UserName` key) that launchd
   starts as `_yolojail` (removes per-run root but complicates TTY attach)?
3. **Keychain as another user** — the sandbox user's login keychain may be
   locked at first use (no GUI login / SecureToken). Does
   `security create-keychain -p ''` + headless Claude/gh OAuth store
   cleanly, or hit EPERM/dialog? Needs a spike.
4. **Workspace ACL durability** — does the inheriting ACL survive
   git/editor rename-and-replace and rsync; do host IDEs/Finder choke on
   `_yolojail`-owned files; exact ACE list; shared-group+setgid as belt?
5. **Broker socket cross-user perms** — exact chmod/ACL vs. shared group on
   the singleton socket + dir; validate `getpeereid` end-to-end.
6. **Startup read-allow set** — enumerate the minimum broad reads each agent
   (claude/codex/gemini/opencode) needs so a deny-default profile boots them
   without over-widening; per-agent and version-fragile.
7. **Resource limits** — accept "no cgroup analog," or bolt on
   `taskpolicy`/`setrlimit`/a memory watchdog for runaway agents?
8. **Auto-detect precedence** — keep `macos-user` explicit opt-in
   (recommended until Phase 3) or prefer it over `container` on macOS?
9. **Homebrew perms** — auto-run `chmod -R o+rX /opt/homebrew` (invasive) or
   detect-and-warn?

## Recommendation

**GO, with conditions.** The design maps cleanly onto the existing runtime
seam, reuses the entire stdlib-only entrypoint layer in-process (already
proven by `_entrypoint_preflight`), and has proven references (SandVault +
Anthropic's Windows precedent). Conditions: (a) it ships as an **opt-in
third backend**, never a rewrite — containers stay the default; (b) the
weaker-than-container delta is documented honestly; (c) the credential
boundary is claimed only **after** Phase 2 closes the `chmod 750 ~` +
Seatbelt deny-reads hole.

**Smallest first step:** run Phase 0 on a real Sequoia/arm64 Mac. Its
result is the real GO/NO-GO gate before any code lands in `src/cli/`.

## References

- SandVault (`sv`) — the closest analog, read its script directly:
  <https://github.com/webcoyote/sandvault> ·
  <https://raw.githubusercontent.com/webcoyote/sandvault/main/sv>
- Alcoholless (`alcless`, Akihiro Suda) — separate-user + rsync copy-in
  sibling.
- Anthropic Sandbox Runtime (`@anthropic-ai/sandbox-runtime`) — powers
  Claude Code `/sandbox`; dedicated `srt-sandbox` user on Windows,
  same-user Seatbelt on macOS.
- Apple: `sysadminctl(8)`, `dscl(1)`, `dseditgroup(8)`, `createhomedir(8)`,
  `sandbox-exec(1)` (deprecated), SBPL (undocumented).
- Related in-repo: `docs/sandbox-comparison.md`, `docs/macos.md`,
  `docs/platform-comparison.md`.
