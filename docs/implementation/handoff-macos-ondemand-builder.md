# Handoff — on-demand, auto-managed macOS Linux builder

**Status:** design agreed; CLI logic to be implemented + unit-tested on Linux;
VM/launchd/sudo bring-up must be verified on a Mac (can't be exercised from the
Linux dev jail).

**Decision (owner: Matt):** the local Linux builder is a **first-class,
frictionless path**, not a punishment. yolo brings the builder VM up **on
demand** when a build is actually needed, and a launchd timer **stops it after
idle**. No babysitting a terminal; no resident RAM when you're not building.
Trusted-users is accepted as a legitimate one-time requirement — we are NOT
adding a delivery path *just* to dodge it.

Cachix (see [handoff-cachix-cache.md](handoff-cachix-cache.md)) remains a
**pure optimization** layered on top: when present it removes the build (and
thus the builder) for cached image paths. But custom/uncached `packages` builds
are expected to be common (Matt uses them constantly), so the builder path must
be excellent on its own — it may be the path *most* users hit, with Cachix just
shaving the common case.

## Why this shape (the reasoning that survived research)

- `nix run nixpkgs#darwin.linux-builder` **is a Linux VM** (Apple
  Virtualization, ~3 GB RAM default). Telling users to run and babysit it is the
  exact podman-machine sin this whole macOS effort set out to kill.
- **Getting the builder needs no build.** The builder VM image is prebuilt and
  served from `cache.nixos.org` *as long as its config is left at defaults* — so
  "start a Linux builder" is a **download**, not a from-source Linux build.
  There is no chicken-and-egg. (Confirmed: nixpkgs
  `doc/packages/darwin-builder.section.md` — "Initially you should not change
  the remote builder configuration else you will not be able to use the binary
  cache.")
- **The builder is only needed at build time.** Once the image is built and
  loaded, running jails never touch it. We *already* compute the "will a build
  happen?" signal (`_nix_dry_run_will_build` / `_preflight_builder_needs`), so
  we can start the VM precisely when needed and let it go when idle. Resident is
  therefore wrong as a default — it steals 3 GB 24/7 for a thing used minutes a
  day.
- **launchd can own the lifecycle.** nix-darwin runs the builder as a
  `launchd.daemons.linux-builder` service; the `darwin.linux-builder` installer
  script (`create-builder` / `run-linux-builder`) can be launched
  non-interactively (no interactive login shell required). Idle-stop is a
  standard launchd `KeepAlive`-off + a watchdog/timeout.

## The frictionless UX (what replaces today's FAIL-and-babysit)

1. **`yolo builder setup`** — explains what will change, shows the exact root
   script, then RUNS the whole offload wiring in **ONE `sudo`** (a single
   interactive password prompt; `--yes` to skip the confirm, `--show` to print
   without running). This is an interactive per-run prompt, NOT a sudo-policy
   change — no NOPASSWD, nothing silent (the script is printed first). The
   batched script (`builder.setup_root_script`, piped to `sudo bash -s`):
   - the `builders = ssh-ng://builder@linux-builder aarch64-linux
     /etc/nix/builder_ed25519 <maxjobs> - - - -` line +
     `builders-use-substitutes = true` into the daemon's nix.conf
     (`/etc/nix/nix.custom.conf` on Determinate, else `/etc/nix/nix.conf`),
     guarded by a `grep` so re-running never duplicates it;
   - `/etc/ssh/ssh_config.d/100-linux-builder.conf` `Host linux-builder` block
     (Hostname localhost, HostKeyAlias linux-builder, Port 31022, User builder,
     IdentityFile /etc/nix/builder_ed25519);
   - a launchd service plist for on-demand start (NOT `KeepAlive=true`);
   - daemon restart (`launchctl kickstart -k system/<label>`, label from
     `_detect_nix_daemon_label()`).
   - **trusted-users: MERGED, not clobbered.** `builder.trusted_users_line`
     reads the *effective* set via `nix config show` and rewrites `root + …
     existing … + me`, skipping the write entirely if the user is already
     trusted (directly or via `@admin`/`@wheel`). This is safe to auto-run
     because it preserves what's there — the prior "don't silently mutate"
     constraint was about *hidden* changes and *sudo-policy* changes, neither
     of which applies to a visible, script-printed, password-prompted merge.
   - **VM ssh-key install** (`install-credentials.sh`) is done by nixpkgs' own
     `create-builder`/`darwin.linux-builder` installer, which prompts for sudo
     separately the first time. Setup notes this; folding it into the same
     single prompt is a Mac-verification open item (see below).

2. **Auto-ensure on build.** On any `yolo` / `yolo check` / `yolo build` where
   the dry-run says "will build" on macOS: if the builder isn't reachable,
   `yolo` starts it (launchctl kickstart the service), **polls port 31022 until
   SSH answers** (a few seconds), then proceeds. The old
   "FAIL → go run a command in another terminal" path is gone for the set-up
   case; FAIL only survives when setup was never run OR start genuinely failed.

3. **Idle auto-stop.** The launchd service self-stops after ~30 min idle (tunable
   via config, e.g. `macos.builder.idle_timeout_min`). Reclaims the ~3 GB
   overnight. Next build re-starts it (one boot per active session).

4. **`yolo builder status` / `stop`** — inspect (running? reachable? last used?)
   and force-stop.

### Config knobs (all optional, sane defaults)

- `macos.builder.lifecycle`: `"on-demand"` (default) | `"resident"` |
  `"manual"`. Resident = opt-in `KeepAlive=true` for people who build so
  constantly even a warm-session boot annoys them.
- `macos.builder.idle_timeout_min`: default 30.
- `macos.builder.memory_mb` / `disk_gb` / `cores`: default to the builder's own
  defaults (3072 MB) — **warn** in `yolo check` that changing these means the VM
  is no longer cache-served and must build once.

## What is DONE (implemented + unit-tested on Linux) vs must wait for a Mac

**Done — `src/cli/builder.py`, `src/cli/builder_cmd.py`, `tests/test_builder.py`
(25 cases), preflight rewire in `check_cmd.py`:**
- `yolo builder` typer group (`setup`/`start`/`stop`/`status`), macОS-gated.
- `builder_reachable()` (TCP :31022 probe), `ensure_builder()` (start +
  poll-until-ready with an INJECTABLE clock so tests don't sleep),
  `builder_status()`, `start_builder` (detached `nix run` + PID file) /
  `stop_builder` (SIGTERM the process group).
- Content generators (pure, tested): `nix_builders_line`, `ssh_config_block`,
  and — the batched-sudo core — `setup_root_script()` + `run_setup()` (pipes
  the script to a single `sudo bash -s`). `trusted_users_line()` MERGES via
  `nix config show`.
- `_preflight_builder_needs`: on macOS, build-needed + no static builder + setup
  done → `ensure_builder()` and PASS instead of FAIL. FAIL only if setup absent
  or the VM won't come up.
- `_LINUX_BUILDER_REMEDY_TEMPLATE` reworded: leads with `yolo builder setup`;
  the "leave it running in a terminal" instruction is gone.

### Correction after first Mac run (the `[PASS]…will handle it` → `[FAIL]` bug)

The first Mac test surfaced a real logic bug and a wrong start mechanism, both
now fixed:
- **Preflight gated on CONFIGURATION, not REACHABILITY.** `_has_linux_builder()`
  only checks a `builders` line exists — so right after `setup` wrote it,
  preflight PASSed ("a Linux builder will handle it") and returned WITHOUT
  starting the VM; the real build then offloaded to a dead `:31022` and FAILed.
  Fix: on macOS, if `builder_setup_state()["done"]`, call `ensure_builder()`
  (start + poll-until-reachable) and PASS only when the VM actually answers.
  `_has_linux_builder()` is now only a fallback for a *static* builder
  (nix-darwin / remote `/etc/nix/machines`).
- **Start mechanism was a launchd plist pointing at a placeholder store path.**
  Dropped entirely. `start_builder()` now spawns `nix run
  nixpkgs#darwin.linux-builder` **detached** (`start_new_session=True`, log to
  `GLOBAL_STORAGE/logs/linux-builder.log`, PID in
  `GLOBAL_STORAGE/linux-builder.pid`) — the broker_relay pattern. No
  nix-darwin, no pre-resolved path; works on any Mac with flakes. This is also
  what installs the ssh key on first boot, so the key is NOT part of
  `done` (else setup could never complete before a build).
- **Run path now ensures the builder too.** `yolo` (not just `yolo check`)
  goes through `auto_load_image`, which now calls `_preflight_builder_needs`
  on macOS before the real build.

### Correction after the SECOND Mac run (90s hang → "wouldn't start")

Root cause found (nixpkgs darwin-builder docs): the FIRST `nix run
nixpkgs#darwin.linux-builder` runs `sudo --reset-timestamp
/nix/store/…-install-credentials.sh` to install the VM ssh key — an
**interactive sudo that forces a password even when sudo is warm**. Our
`start_builder` spawned it detached with `stdin=DEVNULL` and no TTY, so that
sudo could never be answered → the VM never booted → 90s of silence → timeout.

Fixes:
- **Split first boot (interactive) from steady-state (detached).**
  `ensure_builder` now returns `"needs first-boot"` when the ssh key is absent
  instead of attempting a doomed headless start. `yolo builder start` handles
  that case with `first_boot_interactive()` — runs `nix run …` in the
  FOREGROUND (inherits the TTY) so the sudo prompt + `builder@…` login reach
  the user; treats Ctrl-C as success once the key is installed. After the key
  exists, all starts are detached and silent (the original design).
- **Diagnostics.** `start_builder` returns the Popen handle; `_poll_until_
  reachable` short-circuits on a dead child and surfaces `builder_log_tail()`
  (last lines of `GLOBAL_STORAGE/logs/linux-builder.log`) in the error, and
  emits a per-second `on_progress` heartbeat so there's no more silent 90s.
- The `check`/run FAIL for `"needs first-boot"` prints the exact one-time
  `nix run nixpkgs#darwin.linux-builder` command with what to expect.

**Must be verified on the NEXT Mac run:**
- `yolo builder start` first-boot: the interactive `nix run` installs the key,
  the VM comes up, and a subsequent `yolo` builds via the now-detached builder.
- Tune `BUILDER_START_TIMEOUT_S` (90s) once real spawn→reachable timing is seen
  (now visible via the heartbeat + log tail).
- The idle-stop watchdog is NOT built yet (the VM stays up until `yolo builder
  stop` / reboot). Add it: yolo stamps a "last build" time; a small timer
  (launchd `StartInterval` agent or a lightweight daemon) calls `stop_builder`
  when stale.
- `resident` lifecycle knob (`macos.builder.lifecycle`) not wired yet.

## Open questions for the Mac session

1. After the interactive first boot installs the key, does the DETACHED
   `nix run` (stdin=DEVNULL, no TTY) start cleanly on later boots — i.e. is the
   key install truly the only interactive step? The log tail will show if a
   second interactive prompt appears.
2. Idle detection mechanism (see above) — simplest reliable approach on macOS.
3. Does `builder_setup_state`/`yolo check` correctly report state once the
   ssh-ng builder is wired (so `Linux builder configured` reflects reality)?

## Cross-refs

- [handoff-cachix-cache.md](handoff-cachix-cache.md) — the optional
  build-elimination optimization layered on top of this.
- [happy-path-principle.md](../design/happy-path-principle.md) — one documented builder,
  no forked paths.
- nixpkgs `doc/packages/darwin-builder.section.md` — authoritative mechanics
  (port 31022, ssh_config block, nix.conf builders line, launchd daemon form,
  cache-served default VM image).
