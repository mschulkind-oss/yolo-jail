# YOLO Jail

[![CI](https://github.com/mschulkind-oss/yolo-jail/actions/workflows/ci.yml/badge.svg)](https://github.com/mschulkind-oss/yolo-jail/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

**Describe your agentic development environment once — agents, config, skills, tools and credentials — and run it anywhere from a sealed jail to your own shell.**

yolo composes everything an AI coding agent works with: which agents are installed (Claude Code, Copilot, opencode, pi, Codex, Antigravity), their settings and house rules, skills, MCP and LSP servers, packages, and the credentials and host services they may reach. You describe it once, declaratively and per workspace, and yolo renders that description wherever the agent runs. Agents and their add-ons are selected with the `packs` config key — see [Agents](#agents). Runs on **Linux and macOS** (Apple Silicon and Intel).

## Why?

Setting up an AI coding agent well means installing it, configuring it, writing its house rules, wiring its MCP and LSP servers, giving it skills and tools, and handing it the logins it needs — then doing it all again for the next agent, the next project and the next machine. yolo turns that into one declaration you can version, share as a pack, and apply anywhere.

How much the agent is confined is one setting of that declaration:

- **A container jail** (the default, with Podman or Apple Container). Agents can run in `--yolo` mode — shell commands without confirmation — because the jail has:
  - ❌ No access to `~/.ssh/`, `~/.gitconfig`, or cloud credentials
  - ✅ Separate auth (`gh auth login`, `codex login`, etc. inside the jail)
  - ✅ Your codebase mounted read-write at `/workspace`
  - ✅ Persistent tool state across restarts
- **A dedicated macOS user** (`macos-user`), a hidden service account confined by Apple Seatbelt, with no VM — see [userguide/guides/macos.md](userguide/guides/macos.md).
- **No confinement, on your own machine**: `yolo host apply` renders the same config, skills and briefing into your real home, and `yolo host -- <agent>` runs an agent there with its composed environment.

## Features

- **Declarative:** Agents, config, skills, house rules, MCP/LSP and packages come from packs and one config file, rendered the same way wherever the agent runs
- **Configurable:** Per-project config via [`yolo-jail.jsonc`](yolo-jail.jsonc), user defaults via `~/.config/yolo-jail/config.jsonc`
- **Agent-Ready:** MCP presets (Chrome DevTools, Sequential Thinking) you enable by name, and the LSP servers you declare, wired into the agents that support them
- **Confinement you choose:** A container jail by default, with no access to host credentials; a sandboxed macOS account; or your own machine
- **Optimized:** Pre-installed with modern, fast tools (`rg`, `fd`, `bat`, `eza`, `jq`, `delta`, `fzf`)
- **Restricted, when you ask:** Tools you block return clear errors with suggestions — the `guardrails` pack points recursive `grep` at `rg` — and nothing is blocked by default
- **Reproducible:** Defined entirely via Nix Flakes
- **Container Reuse:** Same workspace reuses the same container via `exec`
- **Runtime Flexible:** Works with podman (Linux/macOS), Apple Container (macOS native), or a sandboxed macOS user account with no VM
- **Cross-Platform:** Full support for Linux and macOS (Apple Silicon and Intel)

## Prerequisites

Core requirements (both platforms):

- **[Nix](https://nixos.org/download/)** (with flakes enabled)
- A container runtime — one of:
  - **[Podman](https://podman.io/)** (preferred on Linux; Podman Machine on macOS)
  - **[Apple Container](https://github.com/apple/container)** (native macOS, `brew install container`)

Additionally, to [install from source](#from-source):

- **[Go](https://go.dev/dl/)** (see [`go.mod`](go.mod) for the required version)
- **[just](https://github.com/casey/just)**

Platform specifics (in priority order):

- **Linux / x86_64** — any modern distribution with Podman. No extra setup. The primary target.
- **macOS / Apple Silicon** — via a native **arm64** Linux container (Apple Container or Podman Machine); no emulation. See [macOS guide](userguide/guides/macos.md).
- **Linux / arm64 (aarch64-linux)** — supported and CI-tested (image built + integration-tested natively on `ubuntu-24.04-arm`); same nix image as x86_64, no arch switch.
- **macOS / Intel** — also supported (x86_64 Linux container).

No builder is needed on macOS — the standard image builds entirely from the NixOS binary cache. If you add a package that isn't cached, the from-source Linux build is offloaded automatically to a tiny throwaway container on whichever container runtime is already up (Podman or Apple Container); no VM, no `sudo`, no setup.

## Install

Every channel below ships the same single `yolo` binary. Pick whichever fits — but read the note under each: a launch needs more than the binary.

**Every launch also needs a *flake bundle*** — the copy of yolo's build inputs ([`flake.nix`](flake.nix), its lockfile, the prebuilt in-jail binaries) that yolo builds the jail from. Homebrew and the from-source install put one beside the binary for you; `go install` and pipx/uvx ship the binary alone, so they need a checkout named by `YOLO_REPO_ROOT`. yolo never consults your working directory to find it. Full table: [Getting Started](userguide/getting-started.md#an-install-that-includes-the-build-files).

### Homebrew (easiest, macOS and Linux)

```bash
brew tap mschulkind-oss/tap
brew install mschulkind-oss/tap/yolo-jail
```

Works on macOS and Linuxbrew. Single command, auto-upgrades with `brew upgrade`. No source checkout, no `just` required.

### Go

```bash
go install github.com/mschulkind-oss/yolo-jail/cmd/yolo@latest
```

Builds straight from the module. Needs Go on the host; puts `yolo` in `$GOBIN` (or `$(go env GOPATH)/bin`).

The module holds no flake bundle, so this channel gets the binary and nothing else: the first `yolo` refuses with "Cannot find yolo-jail repo root" until you clone the repo and export `YOLO_REPO_ROOT=/path/to/checkout`. If you are going to have a checkout anyway, [from source](#from-source) is the channel that wants one.

### pipx / uvx

```bash
pipx install yolo-jail
# or, to run without installing:
uvx yolo-jail
```

The PyPI distribution is per-platform wheels wrapping the same prebuilt Go binary — there is no Python code and no Python runtime dependency beyond the installer itself. It exists so the pre-Go audience keeps a working upgrade path.

A wheel carries the binary alone, so like `go install` this channel needs `YOLO_REPO_ROOT` pointed at a checkout before the first launch will do anything.

### From source

For hacking on yolo-jail itself, or running an unreleased working tree. Identical on Linux and macOS:

```bash
git clone https://github.com/mschulkind-oss/yolo-jail.git
cd yolo-jail
just setup             # pinned toolchain (mise) + Go module deps
just deploy            # builds + installs the yolo CLI
```

To upgrade later: `cd yolo-jail && git pull && just deploy`

#### Upgrading from the Python version

yolo-jail used to ship as a Python package installed with `uv tool install`. `just deploy` retires that install for you — it uninstalls the `yolo-jail` uv tool and clears the console scripts it left in `$GOBIN` (`yolo`, `yolo-ps`, `yolo-host-processes`, `yolo-claude-oauth-broker-host`), which otherwise make `go install` fail with `build output "…/yolo" already exists and is not an object file`.

Nothing is deleted that cannot be positively identified as part of that old install. If something unrecognized is sitting at `$GOBIN/yolo`, the migration stops and asks you to look at it rather than guessing. `uv` itself is no longer a prerequisite.

### Optional — User-level defaults

```bash
yolo init-user-config
# Edit: ~/.config/yolo-jail/config.jsonc
```

**Platform-specific runtime setup** (one-time, needed whichever channel you installed from):

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

On macOS, image builds use the NixOS binary cache by default — no builder to set up. If you add packages that aren't in the cache (or build offline), the from-source Linux build is offloaded automatically to a throwaway container on the container runtime you already have running. See [macOS guide](userguide/guides/macos.md).

For development, see [Contributing](#contributing).

## Quick Start

Works identically on Linux and macOS:

```bash
# Navigate to any repository
cd ~/code/my-project

# Start an interactive shell in the jail
yolo

# Or run an agent directly (each needs its pack in your user config's "packs"; see Agents)
yolo -- claude           # Claude Code in YOLO mode
yolo -- copilot          # Copilot with --yolo auto-injected
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

On macOS, `yolo doctor` additionally checks the VM backend (Podman Machine or Apple Container `system status`) — confirming the runtime is up, so that an uncached build can offload to a throwaway container on it.

### First Run

On first run, YOLO Jail will:
1. Build the Linux container image via `nix build` (takes a few minutes — both Linux and macOS download from the NixOS binary cache; on macOS, any non-cached package is built by offloading to an ephemeral container on the running runtime — no VM, no `sudo`, no first-boot)
2. Load the image into your container runtime
3. Install MCP servers, LSP servers, and utilities
4. Start your command

Subsequent runs are fast — tools are cached in persistent storage on both platforms.

### Auth Setup (One-Time)

Inside the jail, authenticate with your tools:

```bash
gh auth login          # GitHub CLI
# Claude Code authenticates via /login on first run
# codex login / opencode auth login / pi's /login work the same way
```

Each coding agent authenticates itself inside the jail — see the per-agent
auth column in [Agents](#agents). Agents that take a provider API key
(opencode, pi, codex) can instead read it from [`env_sources`](#configuration).

These logins persist across jail restarts. Each workspace keeps its own, under `<workspace>/.yolo/home/`, unless a pack shares one across every workspace on the machine: the claude pack does that for Claude's OAuth login, so one `/login` serves every jail. Its `claude-oauth-broker` loophole refreshes that shared token on the host when a jail first needs it, so jails never race the refresh flow. [The jail home](docs/reference/jail-home.md#sharing-semantics) has the full layout.

## Configuration

Create a per-project config in [`yolo-jail.jsonc`](yolo-jail.jsonc):

```jsonc
{
  "runtime": "podman",              // or "container" (Apple Container)
  "packages": ["strace", "htop"],   // extra nix packages
  "mounts": ["/path/to/ref-repo"],  // extra read-only mounts
  "network": {
    "mode": "bridge",               // or "host" for host networking
    "ports": ["8000:8000"]          // publish ports in bridge mode
  },
  "security": {
    "blocked_tools": ["curl", "wget"]
  }
  // "cache_relocations" exists too, but NOT here — user scope only, see below
}
```

Workspace config merges over user defaults (`~/.config/yolo-jail/config.jsonc`), and a sibling `yolo-jail.local.jsonc` — meant to be gitignored for per-machine overrides — auto-merges over the workspace config. Lists merge and dedupe, scalars override.

**Two keys opt out of that merge, and both for the same reason: a workspace config
lives inside the jail's writable mount, so an agent could otherwise grant itself
something.** `packs` and `cache_relocations` are read straight from
`~/.config/yolo-jail/config.jsonc` and nowhere else; `yolo check` errors if either
appears in [`yolo-jail.jsonc`](yolo-jail.jsonc).

```jsonc
// ~/.config/yolo-jail/config.jsonc — never yolo-jail.jsonc
{
  // Everything a jail has beyond a bare shell. A bare NAME selects a pack that
  // ships with yolo; an address brings one from elsewhere. Nothing is on by
  // default, so with no entries here a jail has no coding agent.
  "packs": [
    "claude",                                    // a shipped agent pack
    "file:///home/me/code/my-skills-pack",       // a local pack of your own
    "git+ssh://git@github.com/org/repo//packs/team?ref=main"
  ]
}
```

A pack delivers a coding agent (its CLI, config files, skills and briefing), or
your own shared skills and house rules, or both. A pack may read a host file it
declares, which is how `claude` and `pi` compose your own `settings.json` into the
jail, and where a pack came from does not change that: a pack from a git address
is honored the same as one yolo ships. Selecting it in your user config is the
consent, and every launch lists what each pack read. The launch also fetches a git
pack itself: a pinned tag or commit stays put until you run `yolo pack install` or `yolo pack update`, and a
branch is re-fetched at most hourly. Run `yolo pack --help` for authoring, and
`yolo pack footprint <dir>` on a local clone to see what a pack reads and runs before
you add it.

**On `cache_relocations` specifically:** it moves a subdir of the jail cache onto other storage, bind-mounted **read-write** — which is the read-write host mount an agent must not be able to grant itself. Podman only.

```jsonc
// ~/.config/yolo-jail/config.jsonc — never yolo-jail.jsonc
{
  "cache_relocations": {
    // cache subdir name → absolute host path (the parent must already exist)
    "huggingface": "/data/relocated/yolo-jail/cache/huggingface"
  }
}
```

Moving an existing cache needs a stop-copy-configure-restart dance — see [Storage & Persistence](userguide/guides/storage.md#relocating-a-cache-subdir-to-other-storage) in the user guide.

Run `yolo check` after **every** edit to [`yolo-jail.jsonc`](yolo-jail.jsonc) to validate the merged config, dry-run the generated jail agent configs, and preflight the image build before restarting into the jail. Inside a running jail, `yolo check --no-build` is the fast way to validate config changes mid-session before asking for a restart.

Run `yolo config-ref` for the full configuration reference.

## Agents

> [!IMPORTANT]
> **The `agents` config key has been REMOVED. An agent arrives as a `packs`
> entry.** A config still carrying `agents` is rejected with an error.
>
> Name the pack you want, in your USER config
> (`~/.config/yolo-jail/config.jsonc` — a workspace config cannot name one):
>
> ```jsonc
> { "packs": ["claude"] }   // also: copilot, codex, opencode, pi, agy
> ```
>
> **Nothing is on by default**, so a jail with no `packs` really has no coding
> agent, and says so at launch and in `yolo check`.

Which coding agents you get follows from the packs you configure, and nothing in
the core knows what an agent is — the agents below are pack files
(`packs/*/pack.json`), not Go code.

- **No rebuild:** agents install lazily on first use, so changing `packs` never
  rebuilds the image — just restart the jail.

Each agent is launched with its autonomous/YOLO mode auto-enabled (the jail
container is the security boundary), and authenticates itself **inside the
jail** — host credentials never cross the boundary.

| Agent | pack name | Run | Install | Auth (inside the jail) |
|---|---|---|---|---|
| **Claude Code** | `claude` | `yolo -- claude` | native installer | `/login` on first run |
| **GitHub Copilot** | `copilot` | `yolo -- copilot` | npm `@github/copilot` | `/login` (GitHub OAuth) |
| **opencode** | `opencode` | `yolo -- opencode` | npm `opencode-ai` | `opencode auth login`, or a provider key (e.g. `ANTHROPIC_API_KEY`/`OPENAI_API_KEY`) |
| **pi** ([pi.dev](https://pi.dev)) | `pi` | `yolo -- pi` | npm `@earendil-works/pi-coding-agent` | `pi` `/login`, or a provider key |
| **OpenAI Codex** | `codex` | `yolo -- codex` | native installer | `codex login` (ChatGPT), or `OPENAI_API_KEY` |
| **Antigravity** | `agy` | `yolo -- agy` | native installer | Google sign-in on first run |

Provider API keys are easiest to supply via [`env_sources`](#configuration)
(a gitignored dotenv file) so they reach the agent inside the jail without
living in your committed config. MCP servers you configure (`mcp_presets` /
`mcp_servers`) are wired into every selected agent that supports MCP —
claude, copilot, opencode, codex, and agy (pi has no native MCP).

## Isolation backends

The `runtime` config picks how the agent is isolated:

- **`podman`** (Linux, default) / **`container`** (macOS, Apple Container) —
  the agent runs in a Linux container. Strongest boundary (kernel/VM
  isolation, resource caps). On macOS this means a lightweight Linux VM —
  **native arm64 on Apple Silicon (no emulation)**; see [macOS guide](userguide/guides/macos.md).
- **`macos-user`** (macOS only, and only when you name it) — the agent runs as a
  dedicated hidden macOS user under Apple Seatbelt: no VM and no Linux image, so
  it starts fastest, but the boundary is weaker than a container's. See the
  [macOS guide](userguide/guides/macos.md).

With no confinement at all, `yolo host` renders the same description onto your
own machine instead; see [Why?](#why).

## Security

- **Strict Isolation**: No access to host `~/.ssh/`, `~/.gitconfig`, or cloud credentials
- **Separate Auth**: Run `gh auth login`, `codex login`, etc. inside the jail once
- **User Mapping**: Files created in the jail are owned by your host user (matching UID/GID)
- **Blocked Tools**: Configurable list of tools that return clear error messages
- **Config Safety**: Changes to [`yolo-jail.jsonc`](yolo-jail.jsonc) require human confirmation at next startup — agents cannot silently modify the jail environment. See [docs/reference/config-safety.md](docs/reference/config-safety.md).
- **Read-Only Mounts**: Extra mounts are read-only by default

## Troubleshooting

Run `yolo doctor` to diagnose common setup issues:

```bash
yolo doctor
```

This checks your container runtime, Nix installation, configuration files, image status, and running containers.

Run `yolo check` after **every** config edit, especially when handing work from an outside agent into the jail or when an in-jail agent edits [`yolo-jail.jsonc`](yolo-jail.jsonc) mid-session and needs to verify the restart will succeed.

## Contributing

yolo-jail is a Go module, and [AGENTS.md](AGENTS.md) is the guide to developing it: the
architecture, the build and test traps, and the file that enforces each rule. From a clone:

```bash
just setup           # the toolchain mise.toml pins (Go, just, staticcheck, uv), and the Go module deps
just install-hooks   # a pre-commit hook that runs the same gate CI runs
just check-ci        # that gate: go vet and staticcheck for linux and darwin, gofmt, the changelog
                     # and user-guide checks, and the short test suite
```

The gate also needs `python3` on your `PATH`, for the user-guide checks. Commit messages follow
[Conventional Commits](https://www.conventionalcommits.org/). Changes a user can notice are
described under `[Unreleased]` in [CHANGELOG.md](CHANGELOG.md), which becomes the release notes.

The organization's [code of conduct](https://github.com/mschulkind-oss/.github/blob/main/CODE_OF_CONDUCT.md)
and [security policy](https://github.com/mschulkind-oss/.github/blob/main/SECURITY.md) apply here. Its
[contributing guide](https://github.com/mschulkind-oss/.github/blob/main/CONTRIBUTING.md) describes the
pull-request process; its toolchain and code-quality sections are written for the organization's Python
projects and do not apply to this one.

## Documentation

- [User Guide](https://docs.yolo-jail.mschulkind.dev) — Published setup, configuration, and troubleshooting ([source](userguide/README.md))
- [macOS Setup](userguide/guides/macos.md) — macOS-specific installation and setup guide
- [Platform Comparison](docs/research/platform-comparison.md) — Feature matrix: Linux vs macOS
- [Config Safety](docs/reference/config-safety.md) — How config change approval works
- [Storage & Config](docs/reference/storage-and-config.md) — Storage hierarchy and mount layout
- [Happy-path principle](docs/reference/happy-path-principle.md) — fill the matrix, support one tool per capability

## License

[Apache License 2.0](LICENSE)
