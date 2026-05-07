# YOLO Jail

[![CI](https://github.com/mschulkind/yolo-jail/actions/workflows/ci.yml/badge.svg)](https://github.com/mschulkind/yolo-jail/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

A secure, isolated container environment for AI agents (Claude Code, Copilot, Gemini CLI) to safely modify codebases without compromising host security or identity. Runs on **Linux and macOS** (Apple Silicon and Intel) with Docker, Podman, or Apple Container.

## Why?

AI coding agents like Claude Code, GitHub Copilot, and Google Gemini CLI have a `--yolo` mode that lets them run shell commands without confirmation. This is powerful but dangerous — agents can access your SSH keys, cloud credentials, git identity, and anything else on your machine.

**YOLO Jail** lets you run agents in YOLO mode safely by isolating them in a container with:
- ❌ No access to `~/.ssh/`, `~/.gitconfig`, or cloud credentials
- ✅ Separate auth (`gh auth login`, `gemini login`, etc. inside the jail)
- ✅ Your codebase mounted read-write at `/workspace`
- ✅ Persistent tool state across restarts
- ✅ Pre-configured MCP servers, LSP servers, and modern CLI tools

## Features

- **Isolated:** Runs in a Docker/Podman container with no access to host credentials
- **Optimized:** Pre-installed with modern, fast tools (`rg`, `fd`, `bat`, `eza`, `jq`, `delta`, `fzf`)
- **Restricted:** Blocked tools return clear errors with suggestions (e.g., `rg` instead of `grep`)
- **Reproducible:** Defined entirely via Nix Flakes
- **Agent-Ready:** MCP presets (Chrome DevTools, Sequential Thinking) and LSP servers (Pyright, TypeScript) — enable by name
- **Configurable:** Per-project config via `yolo-jail.jsonc`, user defaults via `~/.config/yolo-jail/config.jsonc`
- **Container Reuse:** Same workspace reuses the same container via `exec`
- **Runtime Flexible:** Works with both Docker and Podman (prefers Podman)
- **Cross-Platform:** Full support for Linux and macOS (Apple Silicon and Intel)

## Prerequisites

Core requirements (both platforms):

- **[uv](https://docs.astral.sh/uv/)** — Python package manager
- **[Nix](https://nixos.org/download/)** (with flakes enabled)
- A container runtime — one of:
  - **[Podman](https://podman.io/)** (preferred on Linux; Podman Machine on macOS)
  - **[Docker](https://docs.docker.com/)** (Docker Engine on Linux; Docker Desktop or [Colima](https://github.com/abiosoft/colima) on macOS)
  - **[Apple Container](https://github.com/apple/container)** (native macOS, `brew install container`)

Platform specifics:

- **Linux** — any modern distribution with Docker or Podman. No extra setup.
- **macOS** — Apple Silicon or Intel. You'll need a container runtime + a Nix remote Linux builder. See [docs/macos.md](docs/macos.md).

## Installation

Two ways to install, pick whichever fits:

### Option A — Homebrew (easiest, both macOS and Linux)

```bash
brew tap mschulkind-oss/tap
brew install mschulkind-oss/tap/yolo-jail
```

Works on macOS and Linuxbrew. Single command, auto-upgrades with `brew upgrade`. No source checkout, no `just` required. Does **not** install the host-side Claude OAuth token refresher — if you run many jails in parallel against one Claude account, see [Install from source](#option-b--install-from-source) instead, or follow [scripts/README.md](scripts/README.md) to install the refresher manually.

### Option B — Install from source

Required if you want the Claude OAuth token refresher systemd timer auto-installed, or if you want to hack on yolo-jail itself. Identical on Linux and macOS:

```bash
git clone https://github.com/mschulkind-oss/yolo-jail.git
cd yolo-jail
just deploy            # builds + installs the yolo CLI + host-side token refresher
```

To upgrade later: `cd yolo-jail && git pull && just deploy`

### Optional — User-level defaults

```bash
yolo init-user-config
# Edit: ~/.config/yolo-jail/config.jsonc
```

**Platform-specific runtime setup** (one-time, needed for both install options):

```bash
# Linux — Podman (recommended)
sudo pacman -S podman                   # or apt/dnf/pacman for your distro

# Linux — Docker
sudo apt-get install docker.io          # or your distro equivalent
sudo usermod -aG docker $USER

# macOS — Apple Container (native, recommended)
brew install container skopeo
container system start

# macOS — Docker via Colima (headless/CI Macs)
brew install colima docker
colima start --cpu 4 --memory 8 --mount-type virtiofs \
  --mount "$HOME:w" --mount /private/tmp:w --mount /private/var/folders:w

# macOS — Podman Machine
brew install podman
podman machine init --cpus 4 --memory 8192 --disk-size 50
podman machine start
```

On macOS you'll also need a Nix remote Linux builder for image builds — see [docs/macos.md](docs/macos.md) for step-by-step setup.

## Quick Start

Works identically on Linux and macOS:

```bash
# Navigate to any repository
cd ~/code/my-project

# Start an interactive shell in the jail
yolo

# Or run a command directly
yolo -- claude           # Claude Code in YOLO mode
yolo -- copilot          # Copilot with --yolo auto-injected
yolo -- gemini           # Gemini with --yolo auto-injected

# Force a new container
yolo --new -- bash

# ALWAYS run this after every yolo-jail.jsonc edit, before restarting
yolo check

# Check your setup
yolo doctor

# List running jails
yolo ps

# Show full configuration reference
yolo config-ref
```

On macOS, `yolo doctor` additionally checks the VM backend (Podman Machine, Colima, or Apple Container `system status`) and the Nix remote Linux builder.

### First Run

On first run, YOLO Jail will:
1. Build the Linux container image via `nix build` (takes a few minutes — Linux downloads from the binary cache; macOS builds via the remote Linux builder)
2. Load the image into your container runtime
3. Install MCP servers, LSP servers, and utilities
4. Start your command

Subsequent runs are fast — tools are cached in persistent storage on both platforms.

### Auth Setup (One-Time)

Inside the jail, authenticate with your tools:

```bash
gh auth login          # GitHub CLI
gemini login           # Google Gemini CLI
# Claude Code authenticates via /login on first run
```

These tokens are stored in `~/.local/share/yolo-jail/home/` (same path on Linux and macOS) and persist across jail restarts. On both platforms, a host-side systemd timer (installed by `just deploy`) periodically refreshes the shared Claude OAuth token so jails never race the refresh flow.

## Configuration

Create a per-project config in `yolo-jail.jsonc`:

```jsonc
{
  "runtime": "podman",              // or "docker" or "container" (Apple Container)
  "packages": ["strace", "htop"],   // extra nix packages
  "mounts": ["/path/to/ref-repo"],  // extra read-only mounts
  "network": {
    "mode": "bridge",               // or "host" for host networking
    "ports": ["8000:8000"]          // publish ports in bridge mode
  },
  "security": {
    "blocked_tools": ["curl", "wget"]
  }
}
```

Workspace config merges over user defaults (`~/.config/yolo-jail/config.jsonc`). Lists merge and dedupe, scalars override.

Run `yolo check` after **every** edit to `yolo-jail.jsonc` to validate the merged config, dry-run the generated jail agent configs, and preflight the image build before restarting into the jail. Inside a running jail, `yolo check --no-build` is the fast way to validate config changes mid-session before asking for a restart.

Run `yolo config-ref` for the full configuration reference.

## Security

- **Strict Isolation**: No access to host `~/.ssh/`, `~/.gitconfig`, or cloud credentials
- **Separate Auth**: Run `gh auth login`, `gemini login`, etc. inside the jail once
- **User Mapping**: Files created in the jail are owned by your host user (matching UID/GID)
- **Blocked Tools**: Configurable list of tools that return clear error messages
- **Config Safety**: Changes to `yolo-jail.jsonc` require human confirmation at next startup — agents cannot silently modify the jail environment. See [docs/config-safety.md](docs/config-safety.md).
- **Read-Only Mounts**: Extra mounts are read-only by default

## Troubleshooting

Run `yolo doctor` to diagnose common setup issues:

```bash
yolo doctor
```

This checks your container runtime, Nix installation, configuration files, image status, and running containers.

Run `yolo check` after **every** config edit, especially when handing work from an outside agent into the jail or when an in-jail agent edits `yolo-jail.jsonc` mid-session and needs to verify the restart will succeed.

## Documentation

- [User Guide](docs/USER_GUIDE.md) — Detailed setup, configuration, and troubleshooting
- [macOS Setup](docs/macos.md) — macOS-specific installation and setup guide
- [Platform Comparison](docs/platform-comparison.md) — Feature matrix: Linux vs macOS
- [Config Safety](docs/config-safety.md) — How config change approval works
- [Storage & Config](docs/storage-and-config.md) — Storage hierarchy and mount layout

## License

[Apache License 2.0](LICENSE)
