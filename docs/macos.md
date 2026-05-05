# macOS Setup Guide

YOLO Jail supports macOS (Apple Silicon and Intel) in addition to Linux.
On macOS the container image is still a **Linux container** — Docker Desktop,
Colima, Podman Machine, or Apple Container transparently runs a lightweight
Linux VM, so the jail experience is nearly identical to a native Linux host.

## Runtimes

macOS supports three container runtimes:

| Runtime | Backend | Best For |
|---------|---------|----------|
| **Podman** | Podman Machine (Apple HV) | Desktop Macs, Podman-in-Podman |
| **Docker** | Docker Desktop or Colima | Headless/CI Macs, broadest compat |
| **Apple Container** | Virtualization.framework | Native macOS, per-container resource limits |

Set the runtime with `YOLO_RUNTIME=podman`, `docker`, or `container`.

Auto-detection priority:
- **macOS:** Apple Container → Podman → Docker (native-first)
- **Linux:** Podman → Docker

## Prerequisites

| Tool | Install | Notes |
|------|---------|-------|
| **[uv](https://docs.astral.sh/uv/)** | `curl -LsSf https://astral.sh/uv/install.sh \| sh` | Python package manager |
| **[Nix](https://nixos.org/download/)** | [Determinate Nix Installer](https://github.com/DeterminateSystems/nix-installer) recommended | Flakes must be enabled |
| **[Podman](https://podman.io/)** | `brew install podman` | Preferred runtime (requires Podman Machine) |
| **[Docker](https://docs.docker.com/desktop/install/mac-install/)** | Docker Desktop or [Colima](https://github.com/abiosoft/colima) | Alternative runtime |
| **[Apple Container](https://github.com/apple/container)** | `brew install container` | Native macOS runtime (macOS 15+) |

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

### Docker via Colima (alternative)

[Colima](https://github.com/abiosoft/colima) provides Docker on macOS without
Docker Desktop. This is especially useful on headless/CI Macs (e.g. EC2 Mac
instances) where Podman Machine's Apple Hypervisor may not work:

```bash
brew install colima docker

# Start with writable mounts for /tmp and /var/folders
colima start --cpu 4 --memory 8 --disk 30 \
  --mount-type virtiofs \
  --mount /Users/$USER:w \
  --mount /private/var/folders:w \
  --mount /private/tmp:w
```

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
```

**Key advantages:**
- Native per-container CPU/memory limits (no cgroup delegation needed)
- Native Unix socket forwarding (no TCP gateway workaround)
- Smallest footprint — no separate VM daemon (Colima/Podman Machine)

**Key limitations:**
- Maximum ~22 bind mounts per container (Virtualization.framework limit)
- No `--net=host` or network mode control
- No security capabilities (`--cap-add`, `--security-opt`)
- Early-stage project — fewer features than Docker/Podman

**Image conversion:** Apple Container requires OCI-format images. YOLO Jail
auto-converts from Nix's Docker V2 format using (in priority order):
1. **skopeo** (recommended — no daemon needed): `brew install skopeo`
2. **podman** or **docker** (needs running daemon as fallback)

### Nix Linux Builder (optional — binary cache substitution used by default)

The Docker image contains Linux binaries (`aarch64-linux` or `x86_64-linux`).
YOLO Jail's flake is structured so that all build-time Nix machinery
(`dockerTools`, `writeShellScriptBin`, etc.) runs natively on your macOS host,
while the image *content* (chromium, bash, python, etc.) is fetched directly
from the NixOS binary cache. **No remote Linux builder is required for a
normal install.**

A Linux builder is only needed if you:
- Modify the flake to add packages that are not in the binary cache, **or**
- Build with `--no-substitute` (disables cache)

> **Important:** Do NOT set `extra-platforms = aarch64-linux` in your Nix
> config. This tells Nix to execute Linux binaries locally, which fails on
> macOS. Instead, use a remote builder if you need one.

**Option A — Colima VM as Nix builder (for cache misses)**

Install Nix inside the Colima VM and configure it as a remote builder:

```bash
# Install Nix inside Colima
colima ssh -- sh -c 'curl --proto "=https" --tlsv1.2 -sSf -L \
  https://install.determinate.systems/nix | sh -s -- install --no-confirm'

# Get the SSH port
COLIMA_PORT=$(colima ssh-config | awk '/Port/ {print $2}')

# Configure SSH alias for "nix-builder" (for both root and your user)
cat >> ~/.ssh/config <<EOF

Host nix-builder
  HostName 127.0.0.1
  Port $COLIMA_PORT
  User $USER
  IdentityFile ~/.colima/_lima/_config/user
  StrictHostKeyChecking no
  UserKnownHostsFile /dev/null
EOF
sudo cp ~/.ssh/config /var/root/.ssh/config

# Register the builder with Nix
echo "ssh-ng://nix-builder aarch64-linux $HOME/.colima/_lima/_config/user 4 1 benchmark,big-parallel,kvm" \
  | sudo tee /etc/nix/machines

# Enable substitutes from the builder
echo 'builders-use-substitutes = true' | sudo tee -a /etc/nix/nix.custom.conf

# Restart Nix daemon
sudo launchctl kickstart -k system/systems.determinate.nix-daemon
```

> **Note:** The Colima SSH port changes on VM restart. After `colima start`,
> update `~/.ssh/config` and `/var/root/.ssh/config` with the new port.

**Option B — NixOS linux-builder (built-in)**

```bash
# Using the built-in NixOS linux-builder (starts a QEMU VM)
nix run nixpkgs#darwin.linux-builder
```

**Option C — Remote Linux host**

Configure a remote builder in `/etc/nix/machines`. See the
[Nix manual on distributed builds](https://nix.dev/manual/nix/latest/advanced-topics/distributed-builds).

### Known Issue: Determinate Nix Daemon Hang

Some versions of `determinate-nixd` (notably v3.x) may hang on store
operations for non-root users. If `nix store info` hangs indefinitely:

```bash
# Kill the determinate daemon and start the vanilla nix-daemon
sudo pkill determinate-nixd
sudo /nix/var/nix/profiles/default/bin/nix-daemon &
```

This starts the standard Nix daemon which does not have the hang bug.

## Installation

Two options. Homebrew is easiest; source install is required if you want the
Claude OAuth token refresher auto-installed or if you're hacking on the CLI.

### Option A — Homebrew (recommended for users)

```bash
brew tap mschulkind-oss/tap
brew install mschulkind-oss/tap/yolo-jail
```

The formula is auto-generated from the PyPI release on every tag. No source
checkout, no `just`, auto-updates via `brew upgrade`. Works on Apple Silicon
and Intel. Does not set up the token refresher — see
[scripts/README.md](../scripts/README.md) for manual launchd setup if you
need it.

### Option B — Install from source

```bash
git clone https://github.com/mschulkind-oss/yolo-jail.git
cd yolo-jail
just deploy          # builds, installs the yolo CLI, sets up refresher if applicable

# Build the Docker image (downloads Linux packages from cache via the
# remote Linux builder you configured above)
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
export YOLO_RUNTIME=podman   # or docker, or container
yolo run
```

## What Works on macOS

Everything that works on Linux works on macOS **except** the items listed in
[Limitations](#limitations) below. This includes:

- ✅ Full jail isolation (read-only root, no host credentials)
- ✅ Workspace mounting at `/workspace`
- ✅ Podman-in-Podman (nested containers via Podman Machine)
- ✅ Docker-in-Docker (via Docker Desktop / Colima)
- ✅ MCP server presets (Chrome DevTools, Sequential Thinking, etc.)
- ✅ LSP servers (Pyright, TypeScript)
- ✅ Port forwarding and publishing (via TCP gateway on Docker/Podman, native sockets on Apple Container)
- ✅ `mise` tool management inside the jail
- ✅ Agent launchers (Claude Code, Copilot, Gemini CLI)
- ✅ Container reuse across sessions
- ✅ Custom Nix packages in the image
- ✅ `yolo check` diagnostics (with macOS-aware checks)
- ✅ `yolo ps`, `yolo stop`, `yolo clean` commands
- ✅ Network modes (bridge, host, none)
- ✅ Read-only root filesystem and tmpfs mounts

## Limitations

These features are **Linux-only** and are gracefully skipped on macOS with
a warning message:

### Cgroup Delegation (Resource Limits)

macOS has no cgroup filesystem. The `yolo-cglimit` helper inside the jail and
the host-side cgroup delegation daemon are unavailable. This means:

- `yolo-cglimit --cpu 50 --name job -- command` will not enforce CPU limits
- The cgroup delegate socket (`/tmp/yolo-cgd/cgroup.sock`) is created as an
  empty directory so the container volume mount succeeds, but no daemon listens

**Workaround:** Use Docker Desktop's or Podman Machine's built-in resource
controls to limit the VM's CPU/memory instead:

```bash
# Podman: configure at init time
podman machine init --cpus 2 --memory 4096

# Docker Desktop: Settings → Resources → Advanced
```

**Apple Container:** Native per-container resource limits work out of the box:

```bash
YOLO_RUNTIME=container yolo run  # uses --cpus and --memory flags natively
```

### GPU Passthrough

NVIDIA GPU passthrough (Docker `--gpus` / Podman CDI) is not available on
macOS. Apple Silicon GPUs use Metal, not CUDA/OpenCL.

- `"gpu": {"enabled": true}` in config is silently skipped with a warning
- `yolo check` reports GPU passthrough as unavailable on macOS

### USB Device Passthrough

Linux device paths (`/dev/bus/usb/...`) and `lsusb` are not available on
macOS. USB device passthrough configured via `"devices"` in `yolo-jail.jsonc`
is skipped with a warning.

### Device Cgroup Rules

`--device-cgroup-rule` flags are a Linux kernel feature. Any `"cgroup_rule"`
entries in the devices config are skipped on macOS.

### SO_PEERCRED Socket Authentication

The cgroup delegation daemon uses `SO_PEERCRED` on Linux to verify the
identity of socket clients. macOS has `LOCAL_PEERPID` as a partial equivalent
(PID only, no UID/GID). Since the cgroup daemon is skipped entirely on macOS,
this has no practical impact.

## Architecture

### Docker / Podman

```
┌─────────────────────────────────────────┐
│  macOS Host                              │
│  ┌───────────────┐  ┌────────────────┐  │
│  │  yolo (cli.py) │  │ Nix (devShell) │  │
│  │  Python 3.13   │  │ macOS packages │  │
│  └───────┬───────┘  └────────────────┘  │
│          │                               │
│  ┌───────▼──────────────────────────┐   │
│  │  Podman Machine / Docker Desktop  │   │
│  │  (Linux VM — Apple Hypervisor)    │   │
│  │  ┌────────────────────────────┐  │   │
│  │  │  yolo-jail container        │  │   │
│  │  │  ┌──────────────────────┐  │  │   │
│  │  │  │  entrypoint.py       │  │  │   │
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
│  │  yolo (cli.py) │  │ Nix (devShell) │  │
│  │  Python 3.13   │  │ macOS packages │  │
│  └───────┬───────┘  └────────────────┘  │
│          │                               │
│  ┌───────▼──────────────────────────┐   │
│  │  Apple Virtualization.framework   │   │
│  │  (one VM per container)           │   │
│  │  ┌────────────────────────────┐  │   │
│  │  │  yolo-jail container/VM     │  │   │
│  │  │  ┌──────────────────────┐  │  │   │
│  │  │  │  entrypoint.py       │  │  │   │
│  │  │  │  (always Linux)      │  │  │   │
│  │  │  │  --cpus / --memory   │  │  │   │
│  │  │  │  native limits       │  │  │   │
│  │  │  └──────────────────────┘  │  │   │
│  │  └────────────────────────────┘  │   │
│  └──────────────────────────────────┘   │
└─────────────────────────────────────────┘
```

Key insight: `cli.py` runs on the macOS host and is platform-aware.
`entrypoint.py` runs inside the Linux container and needs no macOS changes.
The Nix flake uses `pkgs` (native macOS) for all build-time derivations
(`dockerTools`, `writeShellScriptBin`, `stdenv.mkDerivation`, etc.) and
`imagePkgs` (Linux target) only for the *content* of the image (chromium, bash,
python, etc.). This means the image can be built on macOS using the NixOS
binary cache — no cross-compilation or remote Linux builder required.

## Troubleshooting

### `yolo check` reports macOS-specific issues

Run `yolo check` — it includes macOS-specific diagnostics for Nix daemon
connectivity, Linux builder configuration, VM backend status, and the Nix
store APFS volume.

### Podman Machine won't start

On headless Macs (EC2, CI), Podman Machine may fail because Apple's
Hypervisor.framework requires a GUI session. Use Colima + Docker instead:

```bash
brew install colima docker
colima start --cpu 4 --memory 8 --disk 30 --mount-type virtiofs
export YOLO_RUNTIME=docker
```

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
3. Check the remote builder: `nix store info --store ssh-ng://nix-builder`
4. Verify SSH works: `ssh nix-builder echo ok`

### Container image not loading

If `yolo build` or `yolo run` fails to load the image, try manually:

```bash
# Build the image
nix build .#dockerImage --no-link --print-out-paths

# Stream it into Docker/Podman
STORE_PATH=$(nix build .#dockerImage --no-link --print-out-paths)
# If using a remote builder, stream via SSH:
ssh nix-builder "$STORE_PATH" | docker load
```

### Slow first build

The first `nix build` downloads the nixpkgs tarball and all Linux packages
from the binary cache. Subsequent builds are instant due to the Nix store
cache. Because all packages are fetched from the NixOS binary cache (no local
Linux build required), the bottleneck is download speed rather than
compilation time.

### File ownership issues

Docker Desktop and Podman Machine use different volume-mount implementations.
On macOS with Docker via Colima, containers run as root (UID 0) because the
VM handles file ownership mapping via virtiofs. This is handled automatically
by `cli.py`.

### Port forwarding not working

**Docker/Podman:** Host↔container port forwarding uses TCP via
`host.docker.internal` instead of Unix domain sockets (virtiofs doesn't
support them). This is automatic — if port forwarding fails, ensure:

1. `socat` is available inside the container (it's in the default image)
2. The host service is listening on the configured port
3. `host.docker.internal` resolves inside the container:
   `docker exec <container> ping -c1 host.docker.internal`

**Apple Container:** Uses native `--publish-socket` for direct Unix socket
forwarding. No TCP gateway or socat needed.

### Apple Container: "virtual machine failed to start"

Apple's Virtualization.framework has a hard limit of ~22 directory sharing
devices (bind mounts). YOLO Jail works around this by consolidating the
workspace state into a single `/home/agent` mount instead of individual
overlays. If you add many custom mounts, you may hit this limit.

### Apple Container: image load fails

Apple Container only accepts OCI-layout image tars. YOLO Jail automatically
converts via skopeo (preferred) or docker/podman as fallback:

```bash
# Recommended: install skopeo (no daemon needed)
brew install skopeo

# Or use docker/podman as fallback (needs running daemon)
colima start
```

### `/tmp` bind mount failures

macOS `/tmp` is a symlink to `/private/tmp`.

**Colima/Docker:** Ensure Colima is started with `--mount /private/tmp:w`.

**Podman Machine:** The VM mounts `/private` from the host via virtiofs but
does not resolve the `/tmp` symlink itself. YOLO Jail automatically calls
`.resolve()` on all socket/directory paths before passing them to Podman, so
`/tmp/...` paths are transparently converted to `/private/tmp/...`.

### Podman Machine: broker socket bind-mount fails (`EOPNOTSUPP`)

Podman Machine cannot bind-mount Unix socket *files* directly — Podman returns
`Error: statfs ...: operation not supported` or `EOPNOTSUPP`. YOLO Jail works
around this by running a lightweight in-process relay thread: the broker socket
is exposed via a relay socket created *inside* the already-mounted
`/run/yolo-services/` directory, which Podman can see through the virtiofs
directory mount. No manual action is needed.

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
