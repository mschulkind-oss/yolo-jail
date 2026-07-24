# macOS Setup Guide

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
| **Podman** | Linux container in a Podman Machine VM | The portable default; Podman-in-Podman; parity with Linux hosts |
| **Apple Container** | Linux container, one lightweight VM per container | Per-container CPU/memory limits, native socket forwarding (macOS 15+) |
| **macos-user** | Native macOS user + Seatbelt, **no VM, no image** | Fastest startup; no container runtime to install; `packages:` via native darwin nix. Weaker isolation than a VM (Seatbelt, no cgroups) — see [Trade-offs](#macos-user-trade-offs) |

The container runtimes are native arm64 on Apple Silicon. Set the runtime with
`YOLO_RUNTIME=podman`, `container`, or `macos-user` (or the `runtime` key in
`yolo-jail.jsonc`).

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
- Maximum ~22 bind mounts per container (Virtualization.framework limit)
- No `--net=host` or network mode control
- No security capabilities (`--cap-add`, `--security-opt`)
- Early-stage project — fewer features than Podman

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
are therefore **never** on the public cache. Building those on macOS needs a
Linux builder.

Two ways to avoid that:

**Best — download the prebuilt image (no builder at all).** When yolo-jail's
Cachix cache is published, macOS users download the fully-built image and
never compile anything. This is the intended happy path; see
[docs/plans/handoff-cachix-cache.md](../plans/handoff-cachix-cache.md) for its status. Once
live, `yolo check` shows "every image path is served from the binary cache".
CI pushes the **aarch64-linux** closure on every release (built natively on
an arm runner), so Apple Silicon Macs pull the exact arm image they run — no
cross-build, no Linux builder for cached packages.

**Fallback — a local Linux builder.** Needed only until the cache is
published, or if you add a custom package that isn't cached. `yolo check`
tells you exactly when: it's quiet on the fully-cached path and only escalates
(naming the offending derivation) when a from-source build is actually
required.

> **Important:** Do NOT set `extra-platforms = aarch64-linux` in your Nix
> config. This tells Nix to execute Linux binaries locally, which fails on
> macOS. Set up a Linux builder VM (below) instead.

**The fallback builder: nix-darwin `linux-builder`**

The purpose-built Nix Linux builder: a persistent, launchd-managed Linux VM
(Apple Virtualization), the standard tool for this. If you use **nix-darwin**,
it's one line:

```nix
# in your nix-darwin configuration, then `darwin-rebuild switch`:
nix.linux-builder.enable = true;
nix.settings.trusted-users = [ "@admin" ];   # so your user may offload builds
```

**Standalone (no nix-darwin)** — run the same builder VM on demand:

```bash
nix run nixpkgs#darwin.linux-builder   # leave running in a terminal/tmux pane
```

Either way it auto-registers an `aarch64-linux` builder; `nix build .#ociImage`
then offloads the from-source derivations to it and `yolo check` shows
"Linux builder configured". Ensure your user is trusted by the daemon (see
the trusted-users note below).

**Your user must be trusted by the Nix daemon** (so it may offload builds).
Check, set, and restart:

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

With `nix.linux-builder.enable = true`, nix-darwin registers the
`aarch64-linux` builder for you. Running `nix run nixpkgs#darwin.linux-builder`
standalone leaves the VM in the foreground (`Ctrl+C` to stop; if a tmux
prefix eats it, press it twice). `yolo check` then shows "Linux builder
configured" and image builds offload automatically.

> **Escape hatch (advanced):** if you already own a Linux box, you can point
> Nix at it as a remote builder in `/etc/nix/machines` instead — see the
> [Nix manual on distributed builds](https://nix.dev/manual/nix/latest/advanced-topics/distributed-builds).
> This isn't a first-class path (it requires a machine you must already have);
> the cache + `nix-darwin linux-builder` cover everyone else.

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

# Build the OCI image (downloads Linux packages directly from the
# NixOS binary cache; no remote builder needed for the default install)
yolo build

# (Optional) Set user-level defaults
yolo init-user-config
```

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
- ✅ Agent launchers (Claude Code, Copilot, Gemini CLI)
- ✅ Container reuse across sessions
- ✅ Custom Nix packages in the image
- ✅ `yolo check` diagnostics (with macOS-aware checks)
- ✅ `yolo ps`, `yolo stop`, `yolo clean` commands
- ✅ Network modes (bridge, host, none)
- ✅ Read-only root filesystem and tmpfs mounts
- ✅ **Native no-VM backend** (`macos-user`): agent under Seatbelt as
  `_yolojail`, `packages:` via native `aarch64-darwin` nix, host creds invisible
  — verified end-to-end on real Apple Silicon (see
  [The macos-user backend](#the-macos-user-backend))

## Limitations

These features are **Linux-only** and are gracefully skipped on macOS with
a warning message:

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
connectivity, Linux builder configuration, VM backend status, and the Nix
store APFS volume.

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
3. If you configured a remote Linux builder, check it: `nix store info --store ssh-ng://nix-builder`
4. Verify SSH works: `ssh nix-builder echo ok`

### Container image not loading

If `yolo build` or `yolo run` fails to load the image, try manually:

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

Apple's Virtualization.framework has a hard limit of ~22 directory sharing
devices (bind mounts). YOLO Jail works around this by consolidating the
workspace state into a single `/home/agent` mount instead of individual
overlays. If you add many custom mounts, you may hit this limit.

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
`Error: statfs ...: operation not supported` or `EOPNOTSUPP`. YOLO Jail works
around this by running a per-jail broker relay: a supervised standalone host
process (`internal/brokerrelay`, used on macOS *and* Linux) that listens on a
relay socket created *inside* the already-mounted `/run/yolo-services/`
directory — visible to Podman through the virtiofs directory mount — and dials
the broker singleton per connection. The relay is **not** a thread inside
`yolo run`: it deliberately outlives the process that spawned it (the container
does too), with its own PID file at `/tmp/yolo-broker-relay-<hash>.pid` and log
at `~/.local/share/yolo-jail/logs/broker-relay-<hash>.log`. Any `yolo`
invocation that targets the jail (run or attach) heals a dead relay. No manual
action is needed.

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
