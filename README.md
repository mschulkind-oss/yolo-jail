# YOLO Jail

[![CI](https://github.com/mschulkind/yolo-jail/actions/workflows/ci.yml/badge.svg)](https://github.com/mschulkind/yolo-jail/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

A secure, isolated container environment for AI coding agents (Claude Code, Copilot, Gemini CLI, opencode, pi, Codex) to safely modify codebases without compromising host security or identity. Pick which agents to install per project with the [`agents` config](#agents). Runs on **Linux and macOS** (Apple Silicon and Intel) with Podman or Apple Container.

## Why?

AI coding agents like Claude Code, GitHub Copilot, and Google Gemini CLI have a `--yolo` mode that lets them run shell commands without confirmation. This is powerful but dangerous — agents can access your SSH keys, cloud credentials, git identity, and anything else on your machine.

**YOLO Jail** lets you run agents in YOLO mode safely by isolating them in a container with:
- ❌ No access to `~/.ssh/`, `~/.gitconfig`, or cloud credentials
- ✅ Separate auth (`gh auth login`, `gemini login`, etc. inside the jail)
- ✅ Your codebase mounted read-write at `/workspace`
- ✅ Persistent tool state across restarts
- ✅ Pre-configured MCP servers, LSP servers, and modern CLI tools

## Features

- **Isolated:** Runs in a podman or Apple Container container with no access to host credentials
- **Optimized:** Pre-installed with modern, fast tools (`rg`, `fd`, `bat`, `eza`, `jq`, `delta`, `fzf`)
- **Restricted:** Blocked tools return clear errors with suggestions (e.g., `rg` instead of `grep`)
- **Reproducible:** Defined entirely via Nix Flakes
- **Agent-Ready:** MCP presets (Chrome DevTools, Sequential Thinking) and LSP servers (Pyright, TypeScript) — enable by name
- **Configurable:** Per-project config via `yolo-jail.jsonc`, user defaults via `~/.config/yolo-jail/config.jsonc`
- **Container Reuse:** Same workspace reuses the same container via `exec`
- **Runtime Flexible:** Works with podman (Linux/macOS) and Apple Container (macOS native)
- **Cross-Platform:** Full support for Linux and macOS (Apple Silicon and Intel)

## Prerequisites

Core requirements (both platforms):

- **[uv](https://docs.astral.sh/uv/)** — Python package manager
- **[Nix](https://nixos.org/download/)** (with flakes enabled)
- A container runtime — one of:
  - **[Podman](https://podman.io/)** (preferred on Linux; Podman Machine on macOS)
  - **[Apple Container](https://github.com/apple/container)** (native macOS, `brew install container`)

Platform specifics:

- **Linux** — any modern distribution with Podman. No extra setup.
- **macOS** — Apple Silicon or Intel. You need a container runtime (Apple Container or Podman Machine). A remote Nix Linux builder is **optional** — the standard image builds entirely from the NixOS binary cache. See [docs/macos.md](docs/macos.md).

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
# Linux — Podman
sudo pacman -S podman                   # or apt/dnf/pacman for your distro

# macOS — Apple Container (native, recommended)
brew install container skopeo
container system start

# macOS — Podman Machine
brew install podman
podman machine init --cpus 4 --memory 8192 --disk-size 50
podman machine start
```

On macOS, image builds use the NixOS binary cache by default — no remote Linux builder required. See [docs/macos.md](docs/macos.md) if you need to add packages that aren't in the cache (or want to build offline).

For development, see [CONTRIBUTING.md](https://github.com/mschulkind-oss/.github/blob/main/CONTRIBUTING.md).

## Quick Start

Works identically on Linux and macOS:

```bash
# Navigate to any repository
cd ~/code/my-project

# Start an interactive shell in the jail
yolo

# Or run a command directly (only agents in your `agents` config are installed)
yolo -- claude           # Claude Code in YOLO mode
yolo -- copilot          # Copilot with --yolo auto-injected
yolo -- gemini           # Gemini with --yolo auto-injected
yolo -- opencode         # opencode.ai agent (auto-approve)
yolo -- pi               # pi.dev coding agent (auto-approve)
yolo -- codex            # OpenAI Codex CLI (auto-approve, sandbox off)

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

On macOS, `yolo doctor` additionally checks the VM backend (Podman Machine or Apple Container `system status`) and (if configured) the Nix remote Linux builder.

### First Run

On first run, YOLO Jail will:
1. Build the Linux container image via `nix build` (takes a few minutes — both Linux and macOS download from the NixOS binary cache; macOS only needs a remote Linux builder if you've added non-cached packages)
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
# codex login / opencode auth login / pi's /login work the same way
```

Each coding agent authenticates itself inside the jail — see the per-agent
auth column in [Agents](#agents). Agents that take a provider API key
(opencode, pi, codex) can instead read it from [`env_sources`](#configuration).

These tokens are stored in `~/.local/share/yolo-jail/home/` (same path on Linux and macOS) and persist across jail restarts. On both platforms, a host-side systemd timer (installed by `just deploy`) periodically refreshes the shared Claude OAuth token so jails never race the refresh flow.

## Configuration

Create a per-project config in `yolo-jail.jsonc`:

```jsonc
{
  "runtime": "podman",              // or "container" (Apple Container)
  "agents": ["claude", "codex"],    // which coding agents to install (see below)
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

Workspace config merges over user defaults (`~/.config/yolo-jail/config.jsonc`), and a sibling `yolo-jail.local.jsonc` — meant to be gitignored for per-machine overrides — auto-merges over the workspace config. Lists merge and dedupe, scalars override.

Run `yolo check` after **every** edit to `yolo-jail.jsonc` to validate the merged config, dry-run the generated jail agent configs, and preflight the image build before restarting into the jail. Inside a running jail, `yolo check --no-build` is the fast way to validate config changes mid-session before asking for a restart.

Run `yolo config-ref` for the full configuration reference.

## Agents

YOLO Jail is a **library of coding agents** — you choose which to install per
project with the `agents` field. Only the selected agents are installed and
configured, so a jail stays lean and boots faster. The default is Claude Code.

```jsonc
// yolo-jail.jsonc — install just the agents this project uses
{ "agents": ["claude", "codex"] }
```

- **Default:** `["claude"]` when `agents` is omitted.
- **Merge:** unlike other list fields, `agents` **replaces** (does not union)
  across the user→workspace hierarchy, so a workspace can *narrow* your
  user-level default (e.g. user `["claude","gemini"]`, but a claude-only
  workspace `["claude"]`).
- **No rebuild:** agents install lazily on first use, so changing the list
  never rebuilds the image — just restart the jail.

Each agent is launched with its autonomous/YOLO mode auto-enabled (the jail
container is the security boundary), and authenticates itself **inside the
jail** — host credentials never cross the boundary.

| Agent | `agents` value | Run | Install | Auth (inside the jail) |
|---|---|---|---|---|
| **Claude Code** | `claude` | `yolo -- claude` | native installer | `/login` on first run |
| **GitHub Copilot** | `copilot` | `yolo -- copilot` | npm `@github/copilot` | `/login` (GitHub OAuth) |
| **Gemini CLI** | `gemini` | `yolo -- gemini` | npm `@google/gemini-cli` | `gemini login`, or `GEMINI_API_KEY` |
| **opencode** | `opencode` | `yolo -- opencode` | npm `opencode-ai` | `opencode auth login`, or a provider key (e.g. `ANTHROPIC_API_KEY`/`OPENAI_API_KEY`) |
| **pi** ([pi.dev](https://pi.dev)) | `pi` | `yolo -- pi` | npm `@earendil-works/pi-coding-agent` | `pi` `/login`, or a provider key |
| **OpenAI Codex** | `codex` | `yolo -- codex` | npm `@openai/codex` | `codex login` (ChatGPT), or `OPENAI_API_KEY` |

Provider API keys are easiest to supply via [`env_sources`](#configuration)
(a gitignored dotenv file) so they reach the agent inside the jail without
living in your committed config. MCP servers you configure (`mcp_presets` /
`mcp_servers`) are wired into every selected agent that supports MCP —
claude, copilot, gemini, opencode, and codex (pi has no native MCP).

## Isolation backends

The `runtime` config picks how the agent is isolated:

- **`podman`** (Linux, default) / **`container`** (macOS, Apple Container) —
  the agent runs in a Linux container. Strongest boundary (kernel/VM
  isolation, resource caps). On macOS this means a lightweight Linux VM.
- **`macos-user`** (macOS only, **explicit opt-in**) — the agent runs
  *natively* (arm64, no VM, no arch switch) in a dedicated hidden macOS user
  account hardened with an Apple Seatbelt profile. Faster startup and native
  toolchains, but a **weaker boundary** than the container (shared kernel, no
  resource caps). Use it for a trusted-but-autonomous agent where the goal is
  "don't let YOLO mode wreck my host or read my creds"; prefer the container
  for adversarial or exfil-sensitive work. It matches the security model of
  [SandVault](https://github.com/webcoyote/sandvault) — see
  [attribution](#attribution) and the
  [design doc](docs/macos-native-user-sandbox-design.md).

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

## Contributing

See [CONTRIBUTING.md](https://github.com/mschulkind-oss/.github/blob/main/CONTRIBUTING.md) for development setup and guidelines.

## Documentation

- [User Guide](docs/USER_GUIDE.md) — Detailed setup, configuration, and troubleshooting
- [macOS Setup](docs/macos.md) — macOS-specific installation and setup guide
- [Platform Comparison](docs/platform-comparison.md) — Feature matrix: Linux vs macOS
- [Config Safety](docs/config-safety.md) — How config change approval works
- [Storage & Config](docs/storage-and-config.md) — Storage hierarchy and mount layout
- [macOS-user mode](docs/macos-user-mode.md) — running agents natively on macOS (no container/VM): usage
- [macOS-user security model](docs/macos-user-security-model.md) — the complete mental model: what the sandbox does and doesn't protect
- [macOS native-user sandbox design](docs/macos-native-user-sandbox-design.md) — the `macos-user` backend design rationale
- [Happy-path principle](docs/happy-path-principle.md) — fill the matrix, support one tool per capability

## Attribution

The native macOS backend (`runtime: "macos-user"`) adapts the isolation
design of **[SandVault](https://github.com/webcoyote/sandvault)** by Patrick
Wyatt, used under the Apache License 2.0 (the same license as this project).
We reimplemented rather than copied its mechanics — the dedicated hidden
macOS user, the `(allow default)` Seatbelt profile shape with its
load-bearing denies (all writes, `/Library/Keychains`, other users' homes,
raw disk + bpf, `/Volumes`), the dir/file-split inheriting workspace ACL, and
the env-scrubbed `sudo -u` + `sandbox-exec` launch — so yolo-jail's macOS
backend matches SandVault's security model. Thanks to SandVault for a clear,
well-documented reference. See [NOTICE](NOTICE).

## License

[Apache License 2.0](LICENSE)
