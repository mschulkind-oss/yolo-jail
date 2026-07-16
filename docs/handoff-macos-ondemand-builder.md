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

1. **`yolo builder setup`** — ONE sudo prompt. Writes the whole offload wiring
   the user currently copy-pastes:
   - the `builders = ssh-ng://builder@linux-builder aarch64-linux
     /etc/nix/builder_ed25519 <maxjobs> - - - <base64hostkey>` line +
     `builders-use-substitutes = true` into the daemon's nix.conf
     (`/etc/nix/nix.custom.conf` on Determinate, else `/etc/nix/nix.conf`);
   - the ssh key install (`install-credentials.sh`) — the sudo the manual
     mentions;
   - `/etc/ssh/ssh_config.d/100-linux-builder.conf` `Host linux-builder` block
     (Hostname localhost, HostKeyAlias linux-builder, Port 31022, User builder,
     IdentityFile /etc/nix/builder_ed25519);
   - a launchd service definition for on-demand start (NOT `KeepAlive=true`);
   - daemon restart (`launchctl kickstart -k system/<label>`, label from
     `_detect_nix_daemon_label()`).
   - trusted-users: still required; setup checks it and, if missing, shows the
     exact one-liner (do NOT silently mutate — that policy decision is the
     user's, per prior session constraint). If already trusted, silent.

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

## What can be done on Linux (this dev jail) vs must wait for a Mac

**Implement + unit-test here (no VM):**
- `yolo builder` typer group (`setup`/`start`/`stop`/`status`) — arg parsing,
  dispatch, and the pure logic (which files it *would* write, which commands it
  *would* run), with subprocess/sudo mocked (mirror the existing
  `monkeypatch.setattr(_cc.subprocess, "run", …)` test pattern).
- A `builder.py` module (host-side) with: `builder_reachable()` (TCP connect to
  127.0.0.1:31022 or `ssh -o BatchMode linux-builder true`), `ensure_builder()`
  (start + poll-until-ready with timeout), `builder_status()`, launchd
  plist/label constants, and the nix.conf/ssh_config content generators (pure
  string builders — unit-testable).
- Rewire `_preflight_builder_needs`: on macOS, when a build is needed and no
  builder is reachable but setup HAS been done, call `ensure_builder()` and
  re-check instead of FAILing. Only FAIL if setup is absent or start times out.
- Update `_has_linux_builder()` to also recognize the yolo-managed builder
  (ssh-ng ssh config entry / reachable port), not just `/etc/nix/machines`.
- Reword `_LINUX_BUILDER_REMEDY_TEMPLATE`: lead with `yolo builder setup` as THE
  step; drop the "leave it running in a terminal" instruction entirely.
- Docs: rewrite `docs/macos.md` "Building the image" section around
  `yolo builder setup` + on-demand; keep Cachix as the optional optimization.

**Must be verified on a Mac (unrunnable from Linux):**
- That `yolo builder setup` writes correct files and the daemon actually
  offloads (`nix build .#ociImage` succeeds with the VM auto-started).
- Poll-until-ready timing (how long from kickstart to SSH-answers) → tune the
  timeout + the "starting builder…" progress UX.
- launchd idle-stop actually fires and reclaims RAM; resident vs on-demand knob.
- Interaction with both Determinate and official Nix daemon labels.
- Whether `create-builder` needs sudo every start or just once (key install is
  one-time; VM start should not need sudo if the service is a system daemon).

## Open questions for the Mac session

1. Does `create-builder`/`run-linux-builder` need a controlling TTY, or does it
   daemonize cleanly under launchd with stdout to a logfile? (Manual shows both
   the interactive `nix run` form and the launchd form — confirm the launchd
   form needs no login shell.)
2. Idle detection: does the builder expose an idle signal, or do we implement
   the watchdog ourselves (e.g. stop if no ssh connection for N min)? Simplest:
   yolo stamps a "last build" file and a launchd timer stops the VM if stale.
3. First-run key install sudo: fold into `yolo builder setup` so it's the same
   single prompt, not a surprise second one.

## Cross-refs

- [handoff-cachix-cache.md](handoff-cachix-cache.md) — the optional
  build-elimination optimization layered on top of this.
- [happy-path-principle.md](happy-path-principle.md) — one documented builder,
  no forked paths.
- nixpkgs `doc/packages/darwin-builder.section.md` — authoritative mechanics
  (port 31022, ssh_config block, nix.conf builders line, launchd daemon form,
  cache-served default VM image).
