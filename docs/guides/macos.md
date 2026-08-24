# macOS Setup Guide

**Status:** REFERENCE — user-facing setup guide. **Backend-parity sweep 2026-08-24:**
[Limitations](#limitations) now carries a disabled-feature table for **each** of the two
macOS-native backends, so the choice between them can be made by reading rather than by
launching. Those two tables cite `file → function` rather than `file:line`, on purpose:
the previous revision's `assemble_parts.go:83` had drifted ~30 lines, and every row in a
table that outlives a refactor will drift the same way. **Spot-verified 2026-08-23:**
the three backends and their auto-detection order (`internal/cli/run/preflight.go:90-133`
— macOS tries `container` then `podman`; `macos-user` is opt-in only,
`internal/paths/paths.go:27`); the `macos-*` command family
(`internal/cli/dispatch.go:31-34`); the container-builder offload with no `yolo
builder` command (`internal/containerbuilder/`, `internal/image/builderoffload.go:23`);
and the broker-relay deletion (`internal/brokerrelay` is gone; the front is a
goroutine at `internal/cli/run/loopholesruntime.go:892`). **Three commands this
guide told you to run do not exist** — corrected inline below. **Not verified:**
any macOS-hardware behaviour (vmnet NAT, Podman Machine, Determinate daemon
hang, Apple Container bind-mount limits) — nobody ran a Mac for this audit; the
Nix/Homebrew instructions; the ASCII architecture diagrams.

YOLO Jail supports macOS (Apple Silicon and Intel) in addition to Linux, with
two flavors of backend:

- **Linux container** (`podman`, `container`) — Podman Machine or Apple
  Container transparently runs a lightweight Linux VM, so the jail experience is
  nearly identical to a native Linux host.
- **Native, no-VM** (`macos-user`) — the agent runs directly on macOS as a
  hidden service user (`_yolojail`) confined by Apple Seatbelt, with `packages:`
  materialized via native `aarch64-darwin` nix. No VM, no Linux image. Verified
  end-to-end on real Apple Silicon (macOS 26.5, 2026-07-21).

**On Apple Silicon the container path is native arm64 — there is no emulation.**
The image is built as `aarch64-linux` (the flake maps `aarch64-darwin →
aarch64-linux`) and the runtime VM is `linux/arm64`, so it's arm-on-arm — no
qemu, no Rosetta. The only time you hit emulation is pulling an **amd64-only
image** (e.g. some database images); that's a property of that image, not of the
backend.

> `macos-user` was prototyped, briefly excised, then **revived** as a composed
> product (native macos-user + Apple Container fallback) and is now verified on
> hardware. See
> [macos-no-vm-direction.md](../design/macos-no-vm-direction.md) for the standing
> decision and
> [macos-revival-and-distribution-plan.md](../plans/macos-revival-and-distribution-plan.md)
> for the current status.

## Choosing a runtime

| Runtime | What it is | Choose it for |
|---------|------------|---------------|
| **Podman** | Linux container in a Podman Machine VM | The portable default; Podman-in-Podman; **full feature parity with Linux hosts** — nothing in [Limitations](#limitations) is skipped for backend reasons |
| **Apple Container** | Linux container, one lightweight VM per container | Per-container CPU/memory limits, native socket forwarding (macOS 15+). Drops loopholes, context mounts and read-only protection — see [what it does not do](#apple-container-runtime-container--what-it-does-not-do) |
| **macos-user** | Native macOS user + Seatbelt, **no VM, no image** | Fastest startup; no container runtime to install; `packages:` via native darwin nix. Weaker isolation than a VM (Seatbelt, no cgroups) — see [Trade-offs](#macos-user-trade-offs) and [what it does not do](#macos-user-native-no-vm--what-it-does-not-do) |

The container runtimes are native arm64 on Apple Silicon. Set the runtime with
`YOLO_RUNTIME=podman`, `container`, or `macos-user` (or the `runtime` key in
`yolo-jail.jsonc`).

**If you are choosing between the two macOS-native backends**, the side-by-side
list is [Backend feature parity at a glance](#backend-feature-parity-at-a-glance).

Auto-detection priority:
- **macOS:** Apple Container → Podman (native-first). `macos-user` is
  **opt-in** — select it explicitly; it is not auto-detected.
- **Linux:** Podman

### macos-user trade-offs

`macos-user` swaps VM isolation for native speed. What you gain: no runtime to
install, no Linux image to build, instant startup, and `packages:` built
directly as `aarch64-darwin` nix. What you give up:

- **Weaker isolation** — Seatbelt (`sandbox-exec`) confinement, not a VM. No
  cgroups, so no resource limits.
- **Neutral-ground workspaces only** — the sandbox user can share a project
  under a non-home root like `/Users/Shared/yolo/<name>`, never a path inside
  your home. yolo refuses a home-dir workspace.
- **One-time setup** — `yolo macos-setup` creates the hidden `_yolojail` user
  (self-escalates; do **not** run under `sudo`). `yolo macos-teardown` reverses
  it. See [The macos-user backend](#the-macos-user-backend) below.

## Prerequisites

**Always required:**

| Tool | Install | Notes |
|------|---------|-------|
| **[Nix](https://nixos.org/download/)** | [Determinate Nix Installer](https://github.com/DeterminateSystems/nix-installer) recommended | Flakes must be enabled. Builds the jail image (container runtimes) or the native `aarch64-darwin` `packages:` (macos-user). Your user must be a **trusted** nix user — `yolo check` flags it if not. |

`yolo` is the only binary you install (`go install ./cmd/yolo`, `brew`, or a
release archive); everything else it provisions itself.

**Plus a runtime — pick ONE** (see [Choosing a runtime](#choosing-a-runtime)):

| Runtime | Install | Notes |
|---------|---------|-------|
| **[Podman](https://podman.io/)** | `brew install podman` | The portable default; requires Podman Machine (setup below) |
| **[Apple Container](https://github.com/apple/container)** | `brew install container` | Native per-container VM; macOS 15+ |
| **macos-user** | *(nothing to install)* | Native, no VM. Needs only Nix + `yolo macos-setup` (see [The macos-user backend](#the-macos-user-backend)) |

### Podman Machine Setup

Podman on macOS runs containers inside a Linux VM managed by `podman machine`.
Initialise it once:

```bash
# Create the VM (adjust resources to taste)
podman machine init --cpus 4 --memory 8192 --disk-size 50

# Start the VM
podman machine start
```

The machine persists across reboots. Use `podman machine stop` / `podman machine start`
to manage it.

### Apple Container (native macOS runtime)

[Apple Container](https://github.com/apple/container) uses Apple's
Virtualization.framework directly — each container runs in its own lightweight
VM with native resource limits (`--cpus`, `--memory`) and native Unix socket
forwarding (`--publish-socket`).

```bash
brew install container

# Start the container system daemon
container system start

# Verify it's working
container system info

# Install the recommended Linux kernel (required on first use)
container system kernel set --recommended
```

**Key advantages:**
- Native per-container CPU/memory limits (no cgroup delegation needed)
- Native Unix socket forwarding (no TCP gateway workaround)
- Smallest footprint — no separate VM daemon

**Key limitations:**
- A hard cap on directory-sharing devices (bind mounts) per container, which is why
  yolo consolidates the jail's home into a single `/home/agent` mount
- No security capabilities (`--cap-add`, `--security-opt`)
- Early-stage project — fewer features than Podman
- Several `yolo-jail.jsonc` keys do not reach this backend — see
  [Apple Container: what it does not do](#apple-container-runtime-container--what-it-does-not-do)
  for the full list, including the one that affects Claude logins

**Image conversion:** Apple Container requires OCI-format images. YOLO Jail
auto-converts from Nix's streamed image tar using (in priority order):
1. **skopeo** (recommended — no daemon needed): `brew install skopeo`
2. **podman** (needs running daemon as fallback)

### The macos-user backend

`macos-user` runs the agent **natively on macOS** — no VM, no Linux image. The
agent executes as a hidden service user (`_yolojail`) confined by Apple Seatbelt
(`sandbox-exec`); `packages:` from your config are built as native
`aarch64-darwin` nix. It needs no container runtime — just Nix (with your user
trusted) and a one-time account setup.

**One-time setup** (run as your normal admin user — it self-escalates per
privileged step; do **NOT** prefix with `sudo`):

```bash
yolo macos-setup      # creates the hidden _yolojail user + shared root ACL
```

This provisions the neutral shared root at `/Users/Shared/yolo`. Put projects
you want to run under it (`/Users/Shared/yolo/<name>`) — the sandbox user can
only share neutral ground, never a path inside your home.

**Run:**

```bash
cd /Users/Shared/yolo/my-project
YOLO_RUNTIME=macos-user yolo -- claude       # or set runtime: "macos-user" in yolo-jail.jsonc
```

`sudo` prompts once per run to enter the sandbox — that's expected; yolo does
not change your sudo policy.

**Teardown** (fully reverses setup; idempotent):

```bash
yolo macos-teardown                          # removes the _yolojail user + home
yolo macos-unshare /Users/Shared/yolo/my-project   # strip the shared ACL from a workspace
```

**Preflight without changing anything:** `yolo check` reports the macos-user
readiness (Seatbelt, sandbox user, nix trusted), and
`YOLO_RUNTIME=macos-user yolo --dry-run` prints the full plan (Seatbelt profile,
bootstrap argv, launch argv) and runs its invariant checks — both zero-sudo.

See [Choosing a runtime](#macos-user-trade-offs) for when to pick it, and the
runbook [mac-macos-user-e2e.md](../plans/runbooks/mac-macos-user-e2e.md) for the
full verification procedure.

### Building the image on macOS (cache vs. Linux builder)

The OCI image is a **Linux** image (`aarch64-linux`). Most of its content
(chromium, bash, python, node, …) is standard nixpkgs, fetched from
`cache.nixos.org` — but a few derivations are built from **this repo's own
source** (`yolo-jail-conf`, the entrypoint pkg, the image stream script) and
are therefore **never** on the public cache. macOS can't build a Linux
derivation locally, so those few must be built on Linux somehow.

Two things make that a non-event:

**Best — download the prebuilt image (no build at all).** When yolo-jail's
Cachix cache is published, macOS users download the fully-built image and
never compile anything. This is the intended happy path; see
[docs/plans/handoff-cachix-cache.md](../plans/handoff-cachix-cache.md) for its status. Once
live, `yolo check` shows "every image path is served from the binary cache".
CI pushes the **aarch64-linux** closure on every release (built natively on
an arm runner), so Apple Silicon Macs pull the exact arm image they run — no
cross-build, no builder needed.

**Otherwise — automatic offload to a container builder.** If a package must be
built from source (before the cache is published, or because you added a custom
package that isn't cached), a normal `yolo` run handles it **automatically**: it
starts a tiny nix+sshd Linux builder **container** on whichever runtime is
already up (podman or Apple Container), offloads the build to it over `ssh-ng`,
then tears it down. No VM to set up, no `sudo`, no `yolo builder` command, no
first boot, and zero idle RAM — the builder exists only for the duration of the
build. The one prerequisite is that your container runtime is running; `yolo
check` tells you exactly when a from-source build is required (naming the
offending derivation) and reminds you to start the runtime.

> **Important:** Do NOT set `extra-platforms = aarch64-linux` in your Nix
> config. This tells Nix to execute Linux binaries locally, which fails on
> macOS. You don't need it — the automatic container-builder offload handles
> any from-source Linux build for you.

**Your user must be trusted by the Nix daemon** (so it may offload builds to the
builder container). Check, set, and restart:

```bash
# Is a custom.conf include present? (Determinate adds it; official NixOS
# installer does not — on that one, edit nix.conf directly.)
grep -qF 'include /etc/nix/nix.custom.conf' /etc/nix/nix.conf \
  && echo 'trusted-users = root '"$(whoami)" | sudo tee -a /etc/nix/nix.custom.conf \
  || echo 'trusted-users = root '"$(whoami)" | sudo tee -a /etc/nix/nix.conf

# Restart the daemon (label depends on installer):
sudo launchctl kickstart -k system/systems.determinate.nix-daemon  # Determinate
# or: sudo launchctl kickstart -k system/org.nixos.nix-daemon       # official NixOS
```

> **Escape hatch (advanced):** if you already run your OWN Linux builder — a
> **nix-darwin** `linux-builder` (`nix.linux-builder.enable = true;`), or a
> Linux box registered in `/etc/nix/machines` (see the
> [Nix manual on distributed builds](https://nix.dev/manual/nix/latest/advanced-topics/distributed-builds)) —
> that keeps working untouched. Nix uses your configured builder and yolo never
> starts its own container; `yolo check` shows "Linux builder configured". This
> is your own nix configuration, orthogonal to yolo — the container-builder
> offload above is what covers everyone who hasn't set one up.

### Known Issue: Determinate Nix Daemon Hang

Some versions of `determinate-nixd` (notably v3.x) may hang on store
operations for non-root users. If `nix store info` hangs indefinitely:

```bash
# Kill the determinate daemon and start the vanilla nix-daemon
sudo pkill determinate-nixd
sudo /nix/var/nix/profiles/default/bin/nix-daemon &
```

This starts the standard Nix daemon which does not have the hang bug.

### Nested Nix builds inside the jail (advanced)

By default, YOLO Jail mounts the host's `/nix/store` and Nix daemon socket
into the container so `NIX_REMOTE=daemon` "just works" for nested Nix builds
inside the jail. On macOS, the runtime VM (Podman Machine, Apple container)
typically does **not** share `/nix` from the host, so the bind mount would
fail with a `statfs` error at startup. YOLO Jail therefore skips this mount
on macOS by default.

If your runtime VM *does* share `/nix` into the container (e.g. a custom
virtiofs mount of `/nix` in Podman Machine), opt back in:

```bash
export YOLO_NIX_HOST_DAEMON=1
yolo
```

With the variable set, YOLO Jail will bind-mount `/nix/var/nix/daemon-socket`
and `/nix/store:ro` into the jail and export `NIX_REMOTE=daemon`, exactly as
on Linux.

## Installation

Two options. Homebrew is easiest; the source install is for hacking on the CLI
or running an unreleased working tree. (`go install
github.com/mschulkind-oss/yolo-jail/cmd/yolo@latest` also works identically on
macOS.)

> [!NOTE]
> **`yolo` is the only binary you install on the host.** `just install` runs
> `go install ./cmd/yolo` and nothing else — `yolo-entrypoint`, `yolo-jaild`,
> `yolo-ps`, `yolo-cglimit` and `yolo-journalctl` are image-side only and never
> reach a macOS host.

### Option A — Homebrew (recommended for users)

```bash
brew tap mschulkind-oss/tap
brew install mschulkind-oss/tap/yolo-jail
```

The formula is generated on every tag by the release workflow and builds `yolo`
from the tagged source. No source checkout, no `just`, auto-updates via `brew
upgrade`. Works on Apple Silicon and Intel.

### Option B — Install from source

```bash
git clone https://github.com/mschulkind-oss/yolo-jail.git
cd yolo-jail
just deploy          # builds + installs the yolo CLI

# (Optional) Set user-level defaults
yolo init-user-config
```

> [!WARNING]
> **There is no `yolo build` command** (verified 2026-08-23 against the command
> registry, `internal/cli/dispatch.go:15-35`). This guide used to tell you to run
> it here; it would exit "unknown command". The image is built **automatically**
> by the first `yolo` run (`AutoLoadImage` nix-builds it and loads it into the
> runtime). To build it by hand, call nix directly:
> `nix build .#ociImage --no-link --print-out-paths`.

## Usage

Usage is identical to Linux:

```bash
cd /path/to/your/project
yolo run
```

Set the runtime explicitly if needed:

```bash
export YOLO_RUNTIME=podman   # or container
yolo run
```

## What Works on macOS

Everything that works on Linux works on macOS **except** the items listed in
[Limitations](#limitations) below. This includes:

- ✅ Full jail isolation (read-only root, no host credentials)
- ✅ Workspace mounting at `/workspace`
- ✅ Podman-in-Podman (nested containers via Podman Machine)
- ✅ MCP server presets (Chrome DevTools, Sequential Thinking, etc.)
- ✅ LSP servers (Pyright, TypeScript)
- ✅ Port forwarding and publishing (via TCP gateway on Podman, native sockets on Apple Container)
- ✅ `mise` tool management inside the jail
- ✅ Agent launchers for all six shipped agent packs — `claude`, `copilot`,
  `codex`, `opencode`, `pi`, `agy` (**not** Gemini CLI: the `gemini` agent was
  removed; `~/.gemini/antigravity-cli/` is now agy's tree)
- ✅ Container reuse across sessions
- ✅ Custom Nix packages in the image
- ✅ `yolo check` diagnostics (with macOS-aware checks)
- ✅ `yolo ps` and `yolo prune`
- ✅ Network modes (bridge, host, none) on **Podman** — on Apple Container only `bridge`
  (the default) is honored; see
  [Apple Container: what it does not do](#apple-container-runtime-container--what-it-does-not-do)
- ✅ Read-only root filesystem and tmpfs mounts
- ✅ **Native no-VM backend** (`macos-user`): agent under Seatbelt as
  `_yolojail`, `packages:` via native `aarch64-darwin` nix, host creds invisible
  — verified end-to-end on real Apple Silicon (see
  [The macos-user backend](#the-macos-user-backend))

> [!WARNING]
> **`yolo stop` and `yolo clean` do not exist** (verified 2026-08-23,
> `internal/cli/dispatch.go:15-35`). This bullet used to name them. The reclaim
> command is **`yolo prune`**; the full registry is `check`/`doctor`, `run`,
> `ps`, `loopholes`, `config`, `describe`, `apply`, `check-deps`, `pack`,
> `config-ref`, `init`, `init-user-config`, `broker`, `prune`, `macos-setup`,
> `macos-teardown`, `macos-unshare`, `macos-fix-permissions`. Note the last of
> those — `yolo macos-fix-permissions` (`dispatch.go:34`) — is a real macOS
> command this guide never mentions; it re-applies the shared-root ACL inheritance
> to a workspace.

## Limitations

Two kinds of limitation live here, and they are worth keeping apart.

**Platform limitations** apply to *every* macOS backend: no cgroups, no GPU
passthrough, no USB device passthrough, no device cgroup rules. Those are the
`###` sections further down, and yolo skips each with a warning.

**Backend limitations** are the interesting ones when you are still choosing.
**Podman is the parity reference** — setting the platform limitations aside, a
config key that works on Linux is wired up on a Podman Machine too. Each of the
two macOS-native backends drops a different set of keys, and the two tables below
are the whole list.

### Backend feature parity at a glance

| Config key | Podman | Apple Container | macos-user |
|---|---|---|---|
| `loopholes` (incl. the Claude OAuth broker) | ✅ | ❌ none start | ❌ none start |
| `mounts` (context mounts under `/ctx`) | ✅ | ❌ skipped, warns | ❌ skipped, **silent** |
| Pack `mount` grants (under `/ctx`) | ✅ | ❌ skipped, warns *(since 2026-08-24)* | ❌ skipped, **silent** |
| Pack `reads-host` grants | ✅ | ✅ copied *(since 2026-08-24)* | ❌ renders defaults, warns *(since 2026-08-24)* |
| `host_files` entries with a `source` | ✅ | ✅ copied *(since 2026-08-24)* | ❌ dropped, warns *(since 2026-08-24)* |
| Host `~/.config/nvim` → `/ctx/host-nvim-config` | ✅ | ❌ skipped, warns *(since 2026-08-24)* | ❌ not read at all |
| `cache_relocations` | ⚠️ wired, [untested on a Mac](#cache-relocation-cache_relocations) | ❌ skipped, warns | ❌ skipped, warns |
| `ephemeral_storage` | ✅ | ❌ always `tmpfs` | ❌ not read at all |
| `resources.memory` / `.cpus` | ✅ | ✅ | ❌ warns |
| `resources.pids_limit` | ✅ | ❌ not emitted | ❌ warns |
| `network.mode` | ✅ all three | ⚠️ `bridge` only | ❌ not read at all |
| `network.ports` / `forward_host_ports` | ✅ | ✅ under `bridge` | ❌ not wired |
| `workspace_readonly` | ✅ | ❌ ignored (`:ro` is a no-op) | ✅ as Seatbelt deny rules |
| `per_side_paths` | ✅ | ✅ | ❌ warns |
| Pack briefings + skills | ✅ | ✅ | ❌ not delivered |
| Pack `state`, `scope: machine` | ✅ | ✅ *(since 2026-08-24)* | ✅ |
| Pack `state`, `scope: workspace` | ✅ | ✅ | ⚠️ shared machine-wide |

Each ❌ and ⚠️ is explained in the two tables below. `macos-user` reads none of
the network or scratch-storage keys at all — it is a native process on your own
machine, so there is no network namespace to configure and no container
filesystem to make ephemeral.

### Apple Container (`runtime: container`) — what it does not do

The headline is **loopholes**: this backend starts no host service for any of
them, so the whole loophole surface is off. If you use Claude with an OAuth
login, that is the row to read first.

Everything below is announced at launch **except** the three rows marked
*silent* — those you have to know about, because nothing tells you.

| Config key / feature | On Apple Container | Where it is decided |
|---|---|---|
| `loopholes` — all of them, from packs and from your own config | **inert.** No host service starts, whatever the loophole declares. One yellow line per loophole at every launch, naming the pack, the loophole and the reason | `run/loopholesruntime.go` → `startLoopholes`; `run/loopholeinert.go` → `backendInertReason` |
| `claude-oauth-broker` in particular | **not running.** Refreshes of your Claude OAuth token are not serialized between jails — see the warning below | `run/run.go` → `runContainer` (the broker-singleton gate) |
| `mounts` | **skipped, with a warning.** Apple Container ignores `:ro`, so a context mount would arrive *writable*; yolo declines rather than hand a UID-0 jail a writable window onto your host | `run/backendcaps.go` → `roBindsUnsupported` |
| Pack `mount` grants | **skipped, with a warning, since 2026-08-24.** Same root cause as `mounts` above, and the same fix — but this one is a grant a human approved at `pack install` against the word *read-only*, so honoring it writably would make the approval untrue. A single-**file** pack `mount` is skipped too: it could not arrive at all here, and used to do so silently | `run/packhostgrants.go` → `hostMountArgs` |
| Your host `~/.config/nvim` | **skipped, with a warning, since 2026-08-24.** Podman binds it `:ro` at `/ctx/host-nvim-config` and the jail copies it into the agent's home at boot. Here the ignored `:ro` would leave a live write channel into your real editor config for the whole session. The visible symptom is nvim starting unconfigured | `run/assemble.go` → `assembleRunCmd` (nvim block) |
| `workspace_readonly` | **not enforced, with a loud warning.** Same root cause — `:ro` is ignored — so the paths stay writable inside the jail | `run/mounts.go` → `workspaceReadonlyMountArgs` |
| `cache_relocations` | **skipped, with a warning.** The cache stays on its original filesystem | `run/assemble_parts.go` → `appleContainerBaseMounts` |
| `ephemeral_storage` | **not honored, silently.** The scratch dirs (`/tmp`, `/run`, `/dev/shm`, …) are always `--tmpfs` here; the `volume` mode is podman-only | `run/assemble.go` → `assembleRunCmd` (`ScratchMountArgs` is on the podman branch only) |
| `resources.memory`, `resources.cpus` | **honored** — emitted as native `--memory` / `--cpus`. If you omit them yolo fills in a default (half your RAM, min 4 GB; half your cores, min 2) | `run/assemble_parts.go` → `resourceArgs` |
| `resources.pids_limit` | **not emitted, silently.** Apple Container has no equivalent flag | `run/assemble_parts.go` → `resourceArgs` |
| `network.mode: "bridge"` (the default) | **honored.** Apple Container gives each container its own `vmnet` namespace and yolo emits no `--net` — which is correct here | `run/assemble.go` → `assembleRunCmd` (network-mode block) |
| `network.mode: "host"` | **not honored — and asking for it is worse than leaving it unset.** Warns | see the warning below |
| `network.mode: "none"` | **not honored, silently.** No `--net` flag is emitted on this backend at all, so you get bridge networking regardless | `run/assemble.go` → `assembleRunCmd` (network-mode block) |
| `network.ports`, `network.forward_host_ports` | **honored under `bridge`** — published ports via `-p`, host-port forwarding via native `--publish-socket` (no socat, no TCP gateway). Both keys are read *only* in `bridge` mode | `run/assemble.go` → `assembleRunCmd`; `run/assemble_parts.go` → `forwardHostPortsArgs` |
| Pack `state` at `scope: machine` (e.g. `~/.claude-shared-credentials`) | **honored as of 2026-08-24.** It was never mounted before that, so cross-jail credential sharing silently degraded to per-workspace — see the warning below | `run/assemble_parts.go` → `appleContainerBaseMounts` |
| Any single-**file** read-only mount | **copied, not mounted.** Apple Container cannot bind a single file ([apple/container#1089](https://github.com/apple/container/issues/1089)), so yolo copies each one into the jail's home instead — your `yolo-user-env.sh`, pack briefings, pack `files`, `~/.config/yolo-jail/config.lua`, your global gitignore, pack `reads-host` grants and `host_files` file sources. You should not notice; the files arrive with the same contents. **The one exception is a pack `mount` whose source is a file**, which is skipped rather than copied — see the row above for why | `run/helpers.go` → `acMaterialize` |

Directory mounts nest perfectly well on this backend — the shared cache is mounted
at `/home/agent/.cache` *inside* the `/home/agent` mount on every launch. The
single-file limitation is specifically about files.

> [!WARNING]
> **`network.mode: "host"` is strictly worse than leaving it unset.** No host
> networking is set up (no `--net` flag is emitted), *and* both port keys are read
> only in `bridge` mode — so asking for host mode also drops every published port
> and every forwarded host port. Remove the key, or switch to
> `YOLO_RUNTIME=podman` if you genuinely need host networking. yolo warns about
> this at launch; it used to say nothing.

> [!WARNING]
> **Claude OAuth refreshes are not serialized on this backend.** The
> `claude-oauth-broker` loophole is what stops two jails burning the same
> single-use refresh token, and no loophole host service runs under Apple
> Container. Your Claude credentials file *is* shared across every workspace on
> the machine, so two jails refreshing at the same time are racing on one token.
> If you run several jails concurrently against one Claude login, use
> `YOLO_RUNTIME=podman`.

> [!IMPORTANT]
> **Upgrading to the 2026-08-24 fix: expect your Claude logins to converge.**
> Machine-wide pack `state` dirs were never mounted on Apple Container, so what
> should have been one shared credential was really one credential per workspace.
> Now that the dir is mounted, the first launch after upgrading **copies a
> stranded credential up** out of the workspace state dir into the machine-wide
> location — copy, never move, and only into a file that is missing, so you should
> not lose a login. What no code can preserve is the accidental independence the
> bug created: if you logged in separately in several workspaces, whichever
> workspace launches first wins the machine-wide slot and every other workspace
> uses that credential from then on. Each old copy is left in place at
> `<workspace>/.yolo/home/.claude-shared-credentials/` and simply stops being
> read; delete it when you are satisfied.

### macos-user (native, no VM) — what it does not do

`macos-user` has **no bind mounts at all**, and its run path returns from
`run/run.go` → `Run` before the code that consumes most mount and network config.
It is the fastest backend and the one that delivers the least.

| Config key / feature | On `macos-user` | Where it is decided |
|---|---|---|
| `loopholes` (all of them) | **inert** — the whole loophole surface is off. One yellow line per loophole at launch, same as Apple Container | `run/loopholeinert.go` → `backendInertReason` |
| `mounts` | **silently ignored, with no warning** | consumed in `run/assemble.go` → `assembleRunCmd` and `run/prepare.go` → `refreshJailBriefings`, both after the early return |
| `cache_relocations` | not delivered (it is a nested bind mount) — **warns**. A hand-made symlink is not a workaround either: the sandbox profile denies writes outside the workspace and sandbox home, and denies reads under `/Volumes` | `macosuser/orchestrator.go` → `buildPlan` |
| `forward_host_ports` | not wired (container-side only) | `run/assemble.go` → `assembleRunCmd`; `run/hostports.go` → `ParsePortForwards` |
| `per_side_paths` | not enforced — **warns**. Per-side shadowing needs a mount namespace; Seatbelt filters permissions and cannot fork a path | `macosuser/orchestrator.go` → `buildPlan` |
| `resources` | not enforced — **warns**. No cgroups, and no VM to size | `macosuser/orchestrator.go` → `buildPlan` |
| `workspace_readonly` | **enforced**, as Seatbelt deny rules | `macosuser/seatbelt.go` → `readonlyDenies` |
| Pack briefings and skills | **not delivered** — **warns**. They are composed host-side and delivered by mounting the staged tree, which this backend cannot do, so the agent starts with no `AGENTS.md`/`CLAUDE.md` and no skills (built-in ones included) — while the blocked-tool shims *are* generated, so a blocked command exits 127 with nothing explaining it | `run/loopholeinert.go` → `noteMacosUserContentGaps` |
| `lsp_servers` | config renders, binaries are **not installed** — **warns**. Install them yourself or add them to `packages` | `run/loopholeinert.go` → `noteMacosUserContentGaps` |
| Pack `reads-host` grants | **do not cross** — **warns** (since 2026-08-24). The bytes arrive on a `/ctx` mount and there is none, so each surface renders from its *defaults* layer instead. The agent gets a working config file that is not yours — the more dangerous of the two host-byte gaps, because nothing about the result looks wrong | `run/loopholeinert.go` → `noteMacosUserHostByteGaps` |
| `host_files` entries with a `source` | **dropped from the launch entirely** — **warns** (since 2026-08-24). No file appears at those paths. Filtering them out is deliberate: rendering them would serve the entry's defaults in place of the host file you named. Entries with `content`/`defaults` and no `source` are unaffected | `macosuser/runplan.go` → `sourceLessHostFilesWire` |
| Pack `state` at `scope: workspace` | **shared across every workspace on the machine** — **warns**. The sandbox home is a constant (`/Users/_yolojail`) with no workspace component, so one session's history and state are visible to every other workspace you launch | `run/loopholeinert.go` → `noteMachineWideWorkspaceState` |
| cgroups / resource limits | unavailable (no cgroups on macOS) | as [Cgroup Delegation](#cgroup-delegation-resource-limits) below |

The `mounts` row is the sharp one: it fails **silently**, so a config that
declares context mounts appears to work and delivers nothing. Pack `mount`
grants take the same path and are equally quiet.

The two host-byte rows were added on 2026-08-24 and were silent before it. They
are worth reading together, because they fail differently and only one of them is
honest about it: a dropped `host_files` source leaves *nothing* at the path, while
a `reads-host` grant leaves a plausible substitute. Neither is fixed — the fix is a
delivery mechanism (materialize into the sandbox home, the way Apple Container's
copies work), which is a design change rather than a launch-time patch.

`workspace_readonly` is the row that changed. It was a **silent no-op** on
`macos-user` until commit `d0961f2c` (2026-08-23) — the key validated, the
launch succeeded, and nothing was read-only, because the backend had no mount
to attach `:ro` to. It now renders natively as SBPL
`(deny file-write* (subpath …))` rules emitted *after* the writable-set allow,
so SBPL last-match-wins makes them stick. Absolute or escaping entries are
dropped rather than emitted.

> [!NOTE]
> **The two backends collapse the state tiers in opposite directions.** Apple
> Container used to make the *machine-wide* tier per-workspace (fixed
> 2026-08-24); `macos-user` makes the *per-workspace* tier machine-wide, and that
> one is not fixed — splitting the sandbox home would break the machine tier to
> repair the workspace tier, since the single home *is* that backend's
> shared-credentials mechanism. It warns instead.

### Cgroup Delegation (Resource Limits)

macOS has no cgroup filesystem. The `yolo-cglimit` helper inside the jail and
the host-side cgroup delegation daemon are unavailable. This means:

- `yolo-cglimit --cpu 50 --name job -- command` will not enforce CPU limits
- The cgroup delegate socket (`/run/yolo-services/cgroup-delegate.sock`) is not
  created because no daemon listens; the host services directory is still mounted
  so the container volume mount succeeds

**Workaround:** Use Podman Machine's built-in resource controls to limit
the VM's CPU/memory instead:

```bash
# Podman: configure at init time
podman machine init --cpus 2 --memory 4096
```

**Apple Container:** Native per-container resource limits work out of the box:

```bash
YOLO_RUNTIME=container yolo run  # uses --cpus and --memory flags natively
```

### GPU Passthrough

GPU passthrough is not available on macOS — neither NVIDIA (Podman CDI) nor
AMD ROCm (`/dev/kfd` + render nodes). Apple Silicon GPUs use Metal, and have
neither CUDA nor ROCm support.

- `"gpu": {"enabled": true}` in config is silently skipped with a warning
- `yolo check` reports GPU passthrough as unavailable on macOS

### USB Device Passthrough

Linux device paths (`/dev/bus/usb/...`) and `lsusb` are not available on
macOS. USB device passthrough configured via `"devices"` in `yolo-jail.jsonc`
is skipped with a warning.

### Device Cgroup Rules

`--device-cgroup-rule` flags are a Linux kernel feature. Any `"cgroup_rule"`
entries in the devices config are skipped on macOS.

### Cache Relocation (`cache_relocations`)

Moving a cache subdir onto other storage (see
[USER_GUIDE — Relocating a Cache Subdir](USER_GUIDE.md#relocating-a-cache-subdir-to-other-storage))
is **not implemented on Apple Container**. Not because the backend can't nest a
bind mount — it already mounts the shared cache at `/home/agent/.cache` inside
its writable `/home/agent` mount, which is the same nesting a relocation needs —
but because that backend takes a separate mount path built around a device
limit, and relocation has never been verified on real Apple Container hardware.
Rather than half-apply it, `yolo` prints one warning naming the skipped subdirs
and starts the jail with the cache on its original filesystem. Use
`YOLO_RUNTIME=podman` if you need it, and open an issue if you want it on Apple
Container.

It is **not implemented on `macos-user`** either, and warns there too. A
hand-made symlink is not a workaround on that backend: the Seatbelt profile
denies writes outside the workspace, the sandbox home, `/tmp` and
`/var/folders`, and denies reads under `/Volumes` — so the cache you were trying
to move stays on the boot volume.

On **Podman Machine** the mechanism itself should work, but the target has to be
a path the VM can see. Podman Machine shares your home directory into the VM, so
a target under `$HOME` ought to be fine while one on an unshared volume
(`/Volumes/...`) should fail at startup the same way any missing bind source
does. **Untested** — nobody has run this on a Mac; if you try it, the result is
worth reporting. Add the volume to the machine's mounts first if you need a
target outside `$HOME`.

### SO_PEERCRED Socket Authentication

The cgroup delegation daemon uses `SO_PEERCRED` on Linux to verify the
identity of socket clients. macOS has `LOCAL_PEERPID` as a partial equivalent
(PID only, no UID/GID). Since the cgroup daemon is skipped entirely on macOS,
this has no practical impact.

## Architecture

### Podman

```
┌─────────────────────────────────────────┐
│  macOS Host                              │
│  ┌───────────────┐  ┌────────────────┐  │
│  │  yolo (Go CLI) │  │ Nix (devShell) │  │
│  │                │  │ macOS packages │  │
│  └───────┬───────┘  └────────────────┘  │
│          │                               │
│  ┌───────▼──────────────────────────┐   │
│  │  Podman Machine                    │   │
│  │  (Linux VM — Apple Hypervisor)    │   │
│  │  ┌────────────────────────────┐  │   │
│  │  │  yolo-jail container        │  │   │
│  │  │  ┌──────────────────────┐  │  │   │
│  │  │  │  yolo-entrypoint     │  │  │   │
│  │  │  │  (always Linux)      │  │  │   │
│  │  │  │  AI agent runs here  │  │  │   │
│  │  │  └──────────────────────┘  │  │   │
│  │  └────────────────────────────┘  │   │
│  └──────────────────────────────────┘   │
└─────────────────────────────────────────┘
```

### Apple Container

```
┌─────────────────────────────────────────┐
│  macOS Host                              │
│  ┌───────────────┐  ┌────────────────┐  │
│  │  yolo (Go CLI) │  │ Nix (devShell) │  │
│  │                │  │ macOS packages │  │
│  └───────┬───────┘  └────────────────┘  │
│          │                               │
│  ┌───────▼──────────────────────────┐   │
│  │  Apple Virtualization.framework   │   │
│  │  (one VM per container)           │   │
│  │  ┌────────────────────────────┐  │   │
│  │  │  yolo-jail container/VM     │  │   │
│  │  │  ┌──────────────────────┐  │  │   │
│  │  │  │  yolo-entrypoint     │  │  │   │
│  │  │  │  (always Linux)      │  │  │   │
│  │  │  │  --cpus / --memory   │  │  │   │
│  │  │  │  native limits       │  │  │   │
│  │  │  └──────────────────────┘  │  │   │
│  │  └────────────────────────────┘  │   │
│  └──────────────────────────────────┘   │
└─────────────────────────────────────────┘
```

### macos-user (native, no VM)

```
┌─────────────────────────────────────────┐
│  macOS Host                              │
│  ┌───────────────┐  ┌────────────────┐  │
│  │  yolo (Go CLI) │  │ Nix (daemon)   │  │
│  │  as your user  │  │ aarch64-darwin │  │
│  └───────┬───────┘  └────────────────┘  │
│          │ stages yolo → /var/yolo-jail  │
│          │ sudo --user=_yolojail         │
│  ┌───────▼──────────────────────────┐   │
│  │  sandbox-exec (Seatbelt profile)  │   │
│  │  ┌────────────────────────────┐  │   │
│  │  │  _yolojail (hidden user)    │  │   │
│  │  │  yolo internal              │  │   │
│  │  │    darwin-bootstrap         │  │   │
│  │  │  AI agent runs here         │  │   │
│  │  │  packages: native darwin nix│  │   │
│  │  └────────────────────────────┘  │   │
│  └──────────────────────────────────┘   │
└─────────────────────────────────────────┘
```

Key insight: `yolo` runs on the macOS host and is platform-aware.
`yolo-entrypoint` runs inside the Linux container (podman/AC) and needs no macOS
changes; on the macos-user path the host `yolo` self-execs `yolo internal
darwin-bootstrap` as `_yolojail` instead, running the same config generators
natively. The Nix flake uses `pkgs` (native macOS) for all build-time
derivations (image-layer tooling, `writeShellScriptBin`, `stdenv.mkDerivation`,
etc.) and `imagePkgs` (Linux target) only for the *content* of the image
(chromium, bash, python, etc.). This means the image can be built on macOS using
the NixOS binary cache — no cross-compilation or remote Linux builder required.

## Troubleshooting

### `yolo check` reports macOS-specific issues

Run `yolo check` — it includes macOS-specific diagnostics for Nix daemon
connectivity, container-runtime (VM backend) status, whether a from-source
build is needed (and thus that the runtime must be up so it can offload to a
container builder), and the Nix store APFS volume.

### Podman Machine won't start

On headless Macs (EC2, CI), Podman Machine may fail because Apple's
Hypervisor.framework requires a GUI session. On such hosts, consider using
Apple Container instead (`YOLO_RUNTIME=container`) which uses
Virtualization.framework per-container.

On desktop Macs, try resetting the machine:

```bash
podman machine stop
podman machine rm
podman machine init --cpus 4 --memory 8192 --disk-size 50
podman machine start
```

### Nix build fails or hangs

1. Check the daemon is responsive: `nix store info` (should return within 2s)
2. If it hangs, see [Known Issue: Determinate Nix Daemon Hang](#known-issue-determinate-nix-daemon-hang)
3. If a package must be built from source, yolo offloads to a container builder
   on your active runtime — make sure the runtime is up (`podman machine start`
   or `container system start`) and re-run `yolo`.
4. If you configured your OWN remote Linux builder (escape hatch), check it:
   `nix store info --store ssh-ng://nix-builder` and `ssh nix-builder echo ok`.

### Container image not loading

If `yolo run` fails to load the image, try manually (there is no `yolo build`
subcommand — see the warning under *Install from source*):

```bash
# Build the image
nix build .#ociImage --no-link --print-out-paths

# Stream it into Podman
STORE_PATH=$(nix build .#ociImage --no-link --print-out-paths)
# If using a remote builder, stream via SSH:
ssh nix-builder "$STORE_PATH" | podman load
```

### Slow first build

The first `nix build` downloads the nixpkgs tarball and all Linux packages
from the binary cache. Subsequent builds are instant due to the Nix store
cache. Because all packages are fetched from the NixOS binary cache (no local
Linux build required), the bottleneck is download speed rather than
compilation time.

### File ownership issues

On macOS, Podman Machine handles file ownership mapping via virtiofs so
containers see your host-side files correctly. This is handled automatically
by `yolo`.

### Port forwarding not working

**Podman:** Host↔container port forwarding uses TCP via
`host.containers.internal` instead of Unix domain sockets (virtiofs doesn't
support them). This is automatic — if port forwarding fails, ensure:

1. `socat` is available inside the container (it's in the default image)
2. The host service is listening on the configured port
3. `host.containers.internal` resolves inside the container:
   `podman exec <container> ping -c1 host.containers.internal`

**Apple Container:** Uses native `--publish-socket` for direct Unix socket
forwarding. No TCP gateway or socat needed.

### Apple Container: no outbound internet (macOS 15 vmnet limitation)

Apple Container on Darwin 24.x (macOS 15) has a `vmnet` limitation that leaves
containers without outbound internet even though the bridge gateway is
reachable. First-time setup stalls: `mise` times out resolving node/go/python
version lists, `git`/`curl` can't reach `github.com` or `nodejs.org`.

**Symptom.** A jail can ping the gateway `192.168.64.1` but reaches nothing
beyond it — while the host has full internet:

```bash
# from inside a jail (yolo -- bash):
ping -c2 192.168.64.1   # OK
ping -c2 1.1.1.1        # 100% packet loss
```

**Cause.** On macOS 15 the `vmnet` framework is supposed to NAT the container
subnet out to the internet and doesn't. The address allocation itself is fine
(gateway `192.168.64.1`, containers `192.168.64.2+/24`), and the gateway
process even proxies DNS — but nothing NATs the container subnet's egress, and
host IP forwarding is off. `sudo pfctl -a 'com.apple/*' -s nat` shows an empty
NAT anchor and `sysctl net.inet.ip.forwarding` reads `0`. Apple documents the
framework limitation in [Apple Container: macOS 15
limitations](https://github.com/apple/container/blob/main/docs/technical-overview.md#macos-15-limitations);
it is fixed in macOS 26.

`yolo check` (a.k.a. `yolo doctor`) detects this: on macOS 15 with Apple
Container running it reads `net.inet.ip.forwarding` and, when it's `0`, warns
with the remediation below.

**Remediation** (host-side; supply the NAT that `vmnet` failed to). Replace
`en0` with your default-route interface — find it with
`route -n get default | grep interface`:

```bash
sudo sysctl -w net.inet.ip.forwarding=1
echo 'nat on en0 from 192.168.64.0/24 to any -> (en0)' | \
  sudo pfctl -a 'com.apple/yolo-vmnet-nat' -f -
```

This loads a NAT rule into a sub-anchor under the stock `nat-anchor
"com.apple/*"` (defined in `/etc/pf.conf`), so it composes with the existing
ruleset without editing or flushing it. Verify from a fresh jail:

```bash
yolo run -- curl -sS -o /dev/null -w '%{http_code}\n' https://github.com  # 200
```

**Caveat: not persistent.** Both the `sysctl` and the pf anchor reset on reboot
(and a `pfctl -f /etc/pf.conf` reload drops the anchor). Re-run the two
commands after a reboot, or wrap them in a `LaunchDaemon`. The durable fixes are
upgrading to macOS 26 (where `vmnet` NATs correctly) or using the `podman`
backend instead of Apple Container.

**A second, distinct variant — subnet disagreement.** macOS 15 vmnet can also
fail *earlier*, at addressing: because the network is created lazily when the
first container starts, the network helper and vmnet can pick different subnets.
Then the gateway the helper hands to containers isn't on any host `bridge*`
interface, and a jail **can't even reach `192.168.64.1`** — the container is
completely cut off, not merely internet-less. The NAT workaround above does
*not* help this case; the fix is to recreate the network coherently:

```bash
container system stop && container system start
```

If it recurs, pin the CIDR in `~/.config/container/config.toml`
(`[network]` `subnet = "192.168.64.1/24"`). `yolo check` distinguishes the two:
it compares the helper's allocated gateway (from `container system logs`)
against the host interface addresses and warns with *this* remedy when they
disagree, versus the forwarding/NAT remedy when addressing is sound.

### Apple Container: "virtual machine failed to start"

Apple's Virtualization.framework has a hard limit on directory-sharing devices
(bind mounts) per VM. YOLO Jail works around it by consolidating the workspace
state into a single `/home/agent` mount instead of individual overlays. If you
add many custom mounts, you may still hit the limit.

*(An earlier revision of this guide put that limit at "~22". That number came
from an upstream issue report and is not something this repo measures or
enforces anywhere, so it is no longer repeated as fact.)*

### Apple Container: "default kernel not configured for architecture arm64"

Apple Container needs a Linux kernel to boot its VMs. Install the recommended
one:

```bash
container system kernel set --recommended
```

### Apple Container: image load fails

Apple Container only accepts OCI-layout image tars. YOLO Jail automatically
converts via skopeo (preferred) or podman as fallback:

```bash
# Recommended: install skopeo (no daemon needed)
brew install skopeo

# Or use podman as fallback (needs running daemon)
podman machine start
```

### `/tmp` bind mount failures

macOS `/tmp` is a symlink to `/private/tmp`.

**Podman Machine:** The VM mounts `/private` from the host via virtiofs but
does not resolve the `/tmp` symlink itself. YOLO Jail automatically calls
`.resolve()` on all socket/directory paths before passing them to Podman, so
`/tmp/...` paths are transparently converted to `/private/tmp/...`.

### Podman Machine: broker socket bind-mount fails (`EOPNOTSUPP`)

Podman Machine cannot bind-mount Unix socket *files* directly — Podman returns
`Error: statfs ...: operation not supported` or `EOPNOTSUPP`. Nothing crosses
that boundary as a socket any more: the jail reaches the host-wide broker
singleton over **loopback-TLS**, through a per-jail `svcendpoint` front that
publishes a plain *file* (`/run/yolo-services/claude-oauth-broker.endpoint`)
into the already-mounted directory. A file is what virtiofs carries fine, and
the address lives inside it rather than in the mount.

**There is no broker relay any more** (deleted 2026-08-19,
`docs/design/broker-as-a-pack.md` §7). `internal/brokerrelay`, its
`/tmp/yolo-broker-relay-<hash>.{pid,lock,sock}` files and its
`~/.local/share/yolo-jail/logs/broker-relay-<hash>.log` are gone, and so is the
attach-time healing that used to restart one. The front is a **goroutine inside
the `yolo` process that launched the jail**, so it dies with that process: a jail
whose launcher is gone is **relaunched**, not attached-and-repaired. The daemon
behind it is host-wide and survives — one singleton, one front per jail. A host
upgrading across this change still has old relay processes and files in `/tmp`;
`yolo prune --apply` sweeps them, and so does a reboot.

### Podman Machine: TTY error (`crun: unlink /dev/console: Read-only file system`)

When stdout is a TTY, Podman passes `-t` to `crun`, which tries to unlink
`/dev/console` to set up a console device. With `--read-only` this fails unless
Podman's automatic read-only tmpfs support is active. YOLO Jail only sets
`--read-only-tmpfs=false` on Linux (where it's needed to avoid a conmon JSON
parsing conflict); on macOS the flag is omitted so crun can set up the console
correctly. No manual action is needed.

### `yolo check` reports "Nix daemon: user is NOT trusted"

With Determinate Nix on macOS, non-trusted users can still build the image via
binary cache substitution (no compilation needed). `yolo check` treats this as
a **warning** rather than a failure. To silence it, add your user to
`trusted-users` in `/etc/nix/nix.custom.conf` and restart the daemon:

```bash
# Add to /etc/nix/nix.custom.conf:
echo 'trusted-users = root your-username' | sudo tee -a /etc/nix/nix.custom.conf
sudo launchctl kickstart -k system/systems.determinate.nix-daemon
```

<!-- changelog -->
- [4d54df64] Reworded intro to two approaches (Linux container by default vs native macos-user), dropping the "always a container" framing
- [9f082ebf] Added a "Choosing a runtime" section that leads with why (performance + native arch) before the model details, and retitled the macos-user section around that
- [78c23f1a] Replaced "never auto-detected" with "never selected automatically or by default — including when no container runtime is installed"
- [8a7a2d41] Split Prerequisites into "always required" vs "pick ONE runtime" (Podman / Apple Container / macos-user), so the runtimes read as options not co-requirements
- [2026-08-24] Restructured Limitations into platform-wide vs per-backend, added the Apple Container disabled-feature table (loopholes, `mounts`, `cache_relocations`, `ephemeral_storage`, `pids_limit`, `network.mode`, single-file mounts) and an at-a-glance parity table beside the existing `macos-user` one; recorded what each backend DOES honor so a working backend stops reading as broken
