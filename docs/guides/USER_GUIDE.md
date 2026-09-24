# YOLO Jail User Guide

**Status:** USER REFERENCE — **spot-checked 2026-08-23.** What was verified this pass: every
`yolo <verb>` this guide tells you to run exists in the dispatch registry (`internal/cli/dispatch.go`),
and the Claude-logout entry now names the broker's real home (a contribution of the `claude` pack,
not a bundled loophole keyed on `claude` being on your PATH). **Not re-verified:** the macOS sections
(no Mac here — see [`macos.md`](macos.md), which carries its own dated status) and the measured
numbers in the storage sections.

This guide covers everything you need to get started with YOLO Jail and make the most of its features. For quick-start instructions, see the [README](../../README.md).

**YOLO Jail runs on Linux and macOS as first-class platforms.** Every section below shows instructions for both where they differ. Linux runs Podman; macOS runs Apple Container (the default), Podman Machine, or `macos-user` — a native Seatbelt sandbox with no container and no VM. Those four pairings do **not** support the same things: [What works in each setup](#what-works-in-each-setup) is the canonical, cell-by-cell answer, and it is where to look before believing any other section applies to you. For macOS-specific setup, see [docs/guides/macos.md](macos.md).

---

## Table of Contents

- [Installation](#installation)
- [First Run](#first-run)
- [Authentication](#authentication)
- [CLI Commands](#cli-commands)
- [Configuration](#configuration)
- [Network & Ports](#network--ports)
- [MCP Presets](#mcp-presets)
- [LSP Servers](#lsp-servers)
- [Package Management](#package-management)
- [Blocked Tools](#blocked-tools)
- [Device Passthrough](#device-passthrough)
- [GPU Passthrough (NVIDIA)](#gpu-passthrough-nvidia)
- [Loopholes (spawned host services)](#loopholes-spawned-host-services)
- [Storage & Persistence](#storage--persistence)
- [Container Reuse](#container-reuse)
- [Config Safety](#config-safety)
- [What works in each setup](#what-works-in-each-setup) — what each way of running yolo supports
- [Troubleshooting](#troubleshooting)

---

## Installation

### Prerequisites (both platforms)

| Tool | Purpose | Install |
|------|---------|---------|
| [uv](https://docs.astral.sh/uv/) | Python package manager | `curl -LsSf https://astral.sh/uv/install.sh \| sh` |
| [Nix](https://nixos.org/download/) | Image builder (with flakes) | [Determinate Nix Installer](https://github.com/DeterminateSystems/nix-installer) recommended |
| [just](https://github.com/casey/just) | Task runner for `just deploy` | `cargo install just`, `brew install just`, or your package manager |
| Container runtime | Podman or Apple Container | See platform-specific setup below |

**Supported platforms:** Linux (x86_64, aarch64) and macOS (Apple Silicon and Intel). On macOS, containers run in a lightweight Linux VM managed by Podman Machine or Apple Container.

### Container Runtime Setup

Pick the runtime that fits your platform. YOLO Jail auto-detects whichever is available; the env var `YOLO_RUNTIME` (or the `runtime` key in config) forces a specific one.

#### Linux

```bash
# Podman (rootless by default, no daemon)
sudo apt-get install podman          # Debian/Ubuntu
sudo dnf install podman              # Fedora/RHEL
sudo pacman -S podman                # Arch
```

Auto-detect priority on Linux: **podman**.

#### macOS

You have two runtime choices. Pick one based on your needs:

**Option A — Apple Container (native, recommended for desktop Macs on macOS 15+):**

```bash
brew install container skopeo
container system start
```

Native per-container CPU/memory limits, native Unix socket port forwarding, smallest footprint (no separate VM daemon). Has a ~22 bind mount limit — YOLO Jail works around this by consolidating workspace state into one mount. The image is delivered as a temporary OCI archive (yolo builds the `skopeo` that writes it; nothing on your `PATH` is used).

**Option B — Podman Machine:**

```bash
brew install podman
podman machine init --cpus 4 --memory 8192 --disk-size 50
podman machine start
```

Good if you already use Podman on Linux and want the same CLI on macOS. Requires a GUI session on some versions.

Auto-detect priority on macOS: **container → podman**.

#### Building the image on macOS (no builder to set up)

The container image is a Linux image, but most of its content downloads directly from the NixOS binary cache. A few derivations are built from yolo-jail's own source and aren't cached, so a from-source Linux build step is sometimes needed on macOS — **unless you download the prebuilt image from yolo-jail's cache** (the intended happy path; nothing to build at all). When a from-source build is needed, yolo offloads it automatically to a tiny throwaway container on whichever container runtime is already up (Podman or Apple Container) and tears it down afterward — no VM, no `sudo`, no setup. The only prerequisite is that the runtime is running. (Advanced escape hatch: if you already run your own remote Linux builder — nix-darwin `linux-builder` or a Linux box in `/etc/nix/machines` — Nix will use it.) See [docs/guides/macos.md § Building the image on macOS](macos.md#building-the-image-on-macos-cache-vs-linux-builder) for details.

### Install YOLO Jail

Two install paths, pick whichever fits:

#### Option A — Homebrew (easiest, both macOS and Linux)

```bash
brew tap mschulkind-oss/tap
brew install mschulkind-oss/tap/yolo-jail
```

| Pros | Cons |
|---|---|
| Single command | No claude-oauth-broker state init |
| Auto-upgrades via `brew upgrade` | No source checkout available for hacking |
| No `just`, no source, no build tools | |
| Works on macOS and Linuxbrew identically | |

This is the recommended path for users who just want to run yolo-jail. The Homebrew formula is published to [mschulkind-oss/homebrew-tap](https://github.com/mschulkind-oss/homebrew-tap) automatically on every release by the `update-homebrew` job in `.github/workflows/release.yml`.

Two more channels ship the same binary and are documented in the README: `go install github.com/mschulkind-oss/yolo-jail/cmd/yolo@latest`, and `pipx install yolo-jail` / `uvx yolo-jail` (per-platform wheels wrapping the Go binary).

**Note:** the Homebrew install skips the Claude OAuth broker state init. If you run many jails in parallel against one Claude account, install from source (Option B) so `just deploy` can prime the broker's CA + leaf certs.

#### Option B — Install from source

Required if you want the Claude OAuth broker primed via `just deploy`, or if you're hacking on yolo-jail. Identical on Linux and macOS:

```bash
git clone https://github.com/mschulkind-oss/yolo-jail.git
cd yolo-jail
just deploy      # builds + installs yolo CLI + primes claude-oauth-broker state
```

`just deploy` is idempotent and safe to re-run.

To upgrade later:

```bash
cd yolo-jail && git pull && just deploy
```

### Set Up User Defaults (Optional)

```bash
yolo init-user-config
# Edit: ~/.config/yolo-jail/config.jsonc
```

Same path and merge semantics on Linux and macOS. User-level defaults apply to all projects and are merged under workspace config.

---

## First Run

Navigate to any repository and run:

```bash
cd ~/code/my-project
yolo
```

On first run, YOLO Jail will:

1. **Build the Linux container image** via `nix build`:
   - **Linux:** Nix downloads prebuilt packages from the binary cache (~2–5 minutes).
   - **macOS:** Nix downloads the same Linux packages from the binary cache (~2–5 minutes the first time, instant on subsequent runs thanks to caching). If you've added a non-cached package, the from-source build is offloaded automatically to an ephemeral container on the running runtime — no builder to configure (the runtime just needs to be up).
2. **Deliver the image** into your container runtime with `skopeo copy`, which asks the runtime for each layer before sending it — so a yolo upgrade moves the ~26 MB that actually changed instead of the whole 3.4 GB image:
   - Podman: copied straight into podman's own storage; no tarball is written anywhere.
   - Apple Container: copied to a temporary OCI archive, `container image load`ed, and the archive removed.
   The copier is a `skopeo` build the flake produces itself (`nix build .#imageCopier`) — it carries a Nix-store source transport that a `brew install skopeo` does not have, so nothing on your `PATH` is used or needed. The first launch after a nixpkgs bump builds it (~2 minutes); after that it is a store lookup.
3. **Install tools** — MCP servers, LSP servers, and utilities are installed into persistent storage (`~/.local/share/yolo-jail/home/`).
4. **Start your command** — by default, an interactive shell.

Subsequent runs skip steps 1–3 (everything is cached) and start in seconds on both platforms.

---

## Authentication

Inside the jail, log in to your tools:

```bash
gh auth login          # GitHub CLI
claude                 # Claude Code: runs /login on first launch
agy                    # Google Antigravity: sign in when it asks
```

The jail does not reuse the logins on your host; you log in inside the jail. Every login is kept on the host and survives jail restarts, so you do **not** log in again each time you start a jail. How far one login reaches depends on the tool, not on whether you use podman:

- `claude` and `agy` keep one login for the whole machine: log in once, and the jails in all your other projects use it too.
- `codex` and `pi` share one OpenAI login through a login service yolo runs on your host. This does not work on every setup; see [Do I have to log in again in every workspace?](#5-do-i-have-to-log-in-again-in-every-workspace-and-in-a-second-jail-at-the-same-time).
- `gh`, `copilot`, `opencode` and `omp` keep a separate login for each project, so you log in once per project.

### Claude OAuth broker (refresh serialization)

Anthropic uses single-use refresh tokens — when multiple jails share the same `.credentials.json` and two of them try to refresh in the same window, one loses the race and gets logged out. YOLO Jail ships the **claude-oauth-broker** loophole: a host-side daemon that serializes refreshes behind a flock. Jails route their refresh requests through it instead of calling Anthropic directly.

The broker refreshes **both on demand and proactively**:

- **On demand** — when a jail asks for a refresh, a token with headroom is returned from cache; otherwise the broker refreshes upstream once and hands the result back.
- **Proactively** — the host daemon also runs a background refresher **by default**: it wakes every 60 s and refreshes when the shared token is within 5 minutes of expiry, retrying every 5 s (up to 12 times) while upstream is transiently unreachable. Pass `--no-background-refresh` to turn it off.

The background loop is not an optimization. Claude Code has no proactive refresh of its own for Pro/Max tokens — it refreshes reactively, after a 401 — so a jail that idles past expiry, or a laptop that suspends through it, would otherwise wake up to a logout. Refreshing ahead of expiry on the host is what prevents that.

`just deploy` primes the broker's CA + leaf certs into `~/.local/share/yolo-jail/state/claude-oauth-broker/`. **Selecting the `claude` pack is what activates the broker** — it is a contribution of that pack, and the old "is `claude` on the jail's PATH?" probe was deliberately deleted, because a lazily-installed CLI is not on PATH until first use. `yolo doctor` includes a broker self-check covering cert state and credentials parseability.

> **Security note:** Auth tokens are stored separately from your host credentials. The jail never accesses your host `~/.ssh/`, `~/.gitconfig`, or cloud credentials. The broker reads and refreshes exactly one file — the machine-shared `~/.local/share/yolo-jail/home/.claude-shared-credentials/.credentials.json`, which every jail on this machine symlinks to. **It never writes your host `~/.claude/.credentials.json`.** So a `/login` inside a jail does not keep host Claude Code logged in, and a broker refresh cannot disturb a host session.

---

## CLI Commands

### The startup banner

Every `yolo` subcommand opens by writing one line to **stderr** before it does anything:

```console
$ yolo check
yolo-jail 0.8.0+881.ga6f61864 | linux/x86_64 | host
...
```

Version, platform, and which side you are on — `host`, or `in-jail` when the command was
typed inside a jail. Paste it with any bug report; `yolo check 2>&1 | pbcopy` captures it,
and so does any other redirect, because the line is not gated on a terminal.

Two things it deliberately does **not** do. It never touches stdout, so the
machine-readable commands (`config dump`, `describe --json`, `config drift`, `config-ref`)
are byte-for-byte what they always were. And it is absent from `yolo --version`, `yolo
--help`, and the hidden `yolo internal …` family, none of which is a command a bug report
quotes.

To turn it off — for a wrapper whose stderr is a contract, or a harness that diffs
stderr — set `YOLO_NO_BANNER` to any non-empty value:

```bash
YOLO_NO_BANNER=1 yolo config drift
```

Inside a jail, the version shown is the **host** launcher's, because that is what the jail
was built by; this is exactly why the line names the side.

### `yolo` — Start a Jail

```bash
yolo                       # Interactive shell
yolo -- claude             # Start Claude Code in YOLO mode
yolo -- copilot            # Start Copilot (--yolo auto-injected)
yolo -- agy                # Start Google Antigravity (--dangerously-skip-permissions auto-injected)
yolo -- bash -c "make"     # Run a specific command
```

**Options:**
- (removed) `--new` — use `yolo stop` followed by an ordinary launch instead
- `--network bridge|host` — Override network mode for this run
- `--timing` — Show detailed startup performance timing

### `yolo init` — Initialize a Project

```bash
cd ~/code/my-project
yolo init
```

Creates a `yolo-jail.jsonc` config with documented defaults and adds `.yolo/` to `.gitignore`.

### `yolo check` — Validate Everything

```bash
yolo check              # Full check including nix build
yolo check --no-build   # Quick check (skip nix build)
```

**Run this after every edit to `yolo-jail.jsonc`.** It validates:
- Container runtime availability
- Nix installation and flakes support
- Config file syntax and schema
- Entrypoint dry-run (shims, MCP, LSP generation)
- Nix image build (unless `--no-build`)

Inside a running jail, use `yolo check --no-build` for a fast preflight before asking for a restart.

### `yolo doctor` — Alias for Check

```bash
yolo doctor             # Same as yolo check
```

### `yolo ps` — List Running Jails

```bash
yolo ps
```

Shows container names, status, uptime, and workspace mappings.

### `yolo config-ref` — Full Configuration Reference

```bash
yolo config-ref
```

Prints the complete reference for all `yolo-jail.jsonc` fields with types, defaults, and examples.

### `yolo init-user-config` — Create User Defaults

```bash
yolo init-user-config
```

Creates `~/.config/yolo-jail/config.jsonc` with the same template as `yolo init`.

### `yolo capture` — Record a Vendor Installer, Once Per Machine

```bash
yolo capture claude
```

Some packs install a program by running a vendor's installer script — a URL whose contents run as
a shell script. There is nothing to pin there, because the installer *run* is the resolution, and
`~/.local` is a per-workspace directory, so every workspace downloads its own copy (claude's
versions directory is 1.2 GB).

`yolo capture <bin>` runs that installer ONCE, in a throwaway jail with an empty home, and stores
what it left behind as a content-addressed entry under
`~/.local/share/yolo-jail/captures/entries/<key>/`, with a file manifest and a receipt beside it.
Later jails put that entry in place instead of downloading anything.

`<bin>` must be a program one of your selected packs installs with `via: "installer"`; an
npm-declared program already names a registry version and needs no capture. Captures are
machine-local and are never shared between machines.

#### It usually happens by itself

**You do not normally have to run this command.** A launch checks, before it starts your jail,
whether the machine has ever recorded each `via: "installer"` program your selected packs install.
If one is missing, that launch captures it first and says so:

```
auto-capture  1 program never recorded on this machine: claude
  Each is installed once now, in a jail of its own, so this and every other workspace
  materialize it instead of downloading it. This launch pays one installer download
  per program. Set YOLO_NO_AUTO_CAPTURE=1 to skip.
```

That first launch is slower by roughly one installer download (~205 MiB for claude). Every launch
after it, in this workspace or any other on the machine, is not.

- **It never fails your launch.** A capture that cannot run — no network, a stale installer URL, a
  full disk — warns once, names the program, and gets out of the way; that program then installs
  the ordinary way, one download per workspace, and the next launch retries the capture.
- **`YOLO_NO_AUTO_CAPTURE=1`** turns it off for a launch (any non-empty value works). Use it on a
  metered connection or when you want the launch to start now. `yolo capture <bin>` still works.
- **It is container backends only.** On `macos-user` nothing yet materializes a capture, so a
  launch there captures nothing; run `yolo capture` explicitly if you want the record.
- **Old captures are reclaimed by `yolo prune`.** The store keeps the newest recording of each
  program per platform; anything an install would no longer choose is superseded, and `yolo prune`
  reports it (dry run) or `yolo prune --apply` removes it. Each superseded entry's manifest is kept
  — kilobytes — so a record of what that version contained survives its bytes.

### `yolo programs` — What Is Installed, and What Nothing Asks For Any More

**Run this one INSIDE a jail.** The programs it is about live in that jail's per-workspace home,
and the declarations it compares them against come from its staged pack tree; on the host there is
neither, and the command says so instead of guessing.

```bash
yolo programs ls                     # the report: orphans + record drift
yolo programs remove                 # what removing them would unlink — a DRY RUN
yolo programs remove --apply         # actually remove them
yolo programs remove pyright --apply # ...or just one, by name
```

Dropping a pack removes its launcher and its staged files. **It has never removed the program it
installed** — so a jail is the union of every pack it has ever selected, and an npm package or a
`~/.local/bin` binary can outlive the config line that asked for it by months. `yolo programs ls`
names those *orphans* with their sizes (measured 448.6 MB in this repo's own jail), plus anything
the install receipts and the LSP sentinel now disagree with the disk about. Every boot COUNTS the
same orphans, in one `boot catalog:` line, and writes the list itself to
`<workspace>/.yolo/boot.log`.

Removal is deliberately awkward, in three ways:

- **`remove` is a dry run** unless you pass `--apply`. It prints every path — the package
  directory, the `bin/` symlinks pointing into it, the `@scope` directory it would empty — so what
  you read is exactly what would go.
- **Only an orphan can be removed.** Naming a program a pack, MCP preset, or LSP recipe still
  declares is an error, not a no-op: drop the declaration first.
- **`~/.local/bin` is also where you may have put things.** yolo cannot tell a tool you installed
  by hand from a dropped pack's leftovers — both are "installed and undeclared". Read the dry run.

To make each launch do it for you, put this in your **user** config
(`~/.config/yolo-jail/config.jsonc` — a workspace config cannot set it):

```jsonc
{
  "programs": { "autoprune": true }
}
```

It is **off by default**, it runs with nobody present, and it is not undoable. `yolo programs ls`
first.

---

## Configuration

YOLO Jail is configured via JSONC (JSON with comments) files:

| File | Scope | Purpose |
|------|-------|---------|
| `yolo-jail.jsonc` | Workspace | Per-project settings |
| `yolo-jail.local.jsonc` | Workspace (untracked) | Per-machine overrides, auto-merged over `yolo-jail.jsonc` when present — gitignore it (a global gitignore entry works well) |
| `~/.config/yolo-jail/config.jsonc` | User | Global defaults for all projects |

**Merge rules:** Workspace config merges over user defaults, and `yolo-jail.local.jsonc` merges over the workspace config. Lists are merged and deduplicated; scalars and objects in later layers override earlier values.

### Minimal Example

```jsonc
{
  "runtime": "podman",
  "packages": ["postgresql", "redis"],
  "mcp_presets": ["chrome-devtools"]
}
```

### Full Example

```jsonc
{
  // Packs. Nothing is active by default, so without this key the jail
  // starts with no coding agent in it and the launch says so.
  "packs": ["claude"],

  // Runtime: "podman", "container" (Apple Container), or "macos-user"
  // (a sandboxed native macOS process — no container, and never auto-selected)
  "runtime": "podman",

  // Extra nix packages baked into the image
  "packages": ["postgresql", "htop", "strace"],

  // Network configuration
  "network": {
    "mode": "bridge",
    "ports": ["8000:8000", "3000:3000"],
    "forward_host_ports": [5432, 6379]
  },

  // Security settings
  "security": {
    "blocked_tools": [
      {"name": "grep", "message": "Use rg", "suggestion": "rg <pattern>"},
      {"name": "find", "message": "Use fd"},
      "curl"
    ]
  },

  // Extra read-only mounts
  "mounts": ["~/code/shared-lib"],

  // MCP presets (opt-in)
  "mcp_presets": ["chrome-devtools", "sequential-thinking"],

  // Custom MCP servers
  "mcp_servers": {
    "my-custom": {
      "command": "/workspace/scripts/my-mcp-server.py",
      "args": []
    }
  },

  // Extra tools via mise
  "mise_tools": {"neovim": "stable", "typst": "latest"},

  // Additional LSP servers
  "lsp_servers": {
    "rust": {
      "command": "rust-analyzer",
      "args": [],
      "fileExtensions": {".rs": "rust"}
    }
  }
}
```

Run `yolo config-ref` for the complete field reference.

### Gateway providers and curated models

OpenRouter and Kilo are opt-in packs. They declare one endpoint and credential
variable each, but deliberately ship no model list: gateway catalogs change too
quickly for yolo to choose models for you. Put a finite alias-to-model-id map in
your **user** config, then have profiles select the aliases you want as defaults:

```jsonc
{
  "packs": ["openrouter", "kilo"],
  "providers": {
    "openrouter": {
      "models": {
        "coding": "~anthropic/claude-sonnet-latest",
        "reasoning": "~openai/gpt-latest"
      }
    },
    "kilo": {
      "models": { "economy": "kilo-auto/efficient" }
    }
  },
  "profiles": {
    "router-coding": { "provider": "openrouter", "model": "coding" },
    "kilo-economy": { "provider": "kilo", "model": "economy" }
  },
  "use_profiles": {
    "claude": "router-coding",
    "pi": "kilo-economy"
  }
}
```

Put `OPENROUTER_API_KEY` and `KILO_API_KEY` in an existing `env_sources` file or
the launching environment; never put a key value in the JSONC file. OpenRouter
works directly with Claude, Codex, Pi, OpenCode, and Copilot. Kilo works with
Claude and Copilot through yolo's local wire bridge, and directly with Pi and
OpenCode; it is not offered to Codex because Kilo documents Chat Completions,
not the Responses API Codex requires.

---

## Network & Ports

### Bridge Mode (Default)

The jail runs in an isolated network. Use `ports` to publish container services to the host:

```jsonc
{
  "network": {
    "mode": "bridge",
    "ports": ["8000:8000"]
  }
}
```

A service running on port 8000 inside the jail is accessible at `localhost:8000` on the host.

### Host Mode

Share the host's network stack directly:

```jsonc
{
  "network": {
    "mode": "host"
  }
}
```

All ports work as if running on the host. No port mapping needed.

### Host Port Forwarding

Make host services appear on `localhost` inside the jail — useful for databases, APIs, or other services already running on your machine:

```jsonc
{
  "network": {
    "forward_host_ports": [5432, 6379, "8080:9090"]
  }
}
```

- **Integer** (`5432`): Same port on both sides — host `127.0.0.1:5432` appears as jail `127.0.0.1:5432`
- **String** (`"8080:9090"`): Port remapping — host `127.0.0.1:9090` appears as jail `127.0.0.1:8080`

This uses socat via Unix sockets (requires `socat` on the host). Only works in bridge mode.

---

## MCP Presets

MCP (Model Context Protocol) servers extend agent capabilities. YOLO Jail includes built-in presets that can be enabled by name — **none are enabled by default**.

### Available Presets

| Preset | Description |
|--------|-------------|
| `chrome-devtools` | Headless Chromium automation via Chrome DevTools Protocol |
| `sequential-thinking` | Chain-of-thought reasoning MCP server |

### Enable Presets

```jsonc
{
  "mcp_presets": ["chrome-devtools", "sequential-thinking"]
}
```

### Custom MCP Servers

Add your own MCP servers alongside or instead of presets:

```jsonc
{
  "mcp_servers": {
    "my-server": {
      "command": "/workspace/scripts/my-mcp.py",
      "args": ["--port", "3333"]
    }
  }
}
```

### Disable a Preset

Set a preset server to `null` in `mcp_servers` to disable it even when listed in `mcp_presets`:

```jsonc
{
  "mcp_presets": ["chrome-devtools", "sequential-thinking"],
  "mcp_servers": {
    "sequential-thinking": null
  }
}
```

---

## LSP Servers

YOLO Jail can hand LSP (Language Server Protocol) servers to the agents that read them. There are **no defaults** — nothing is installed or enabled until you declare it.

### Adding Servers

Add language servers via `lsp_servers` in your config. The binary must already be on `PATH` (install it with `mise_tools` or `packages`):

```jsonc
{
  "lsp_servers": {
    "rust": {
      "command": "rust-analyzer",
      "args": [],
      "fileExtensions": {".rs": "rust"}
    }
  }
}
```

### Which agents read it

| Agent | How it receives LSP |
|-------|---------------------|
| **Copilot** | natively, via `~/.copilot/lsp-config.json` — any server you declare |
| **Claude Code** | via plugins; YOLO enables the official `pyright` / `typescript` / `gopls` plugin when you declare the matching server name |
| **Codex, agy** | the agent has no LSP support |
| **Pi, opencode** | not configured by YOLO today (both agents can take it — see [`mcp-configuration.md`](../reference/mcp-configuration.md#lsp-claudes-route-is-a-generated-plugin)) |

Servers are spawned on-demand when an agent analyzes matching file types.

---

## Package Management

### Nix Packages (Image-Level)

Add system packages via the `packages` config array. These are baked into the container image:

```jsonc
{
  "packages": ["postgresql", "strace", "htop"]
}
```

Package names must match [nixpkgs attributes](https://search.nixos.org/packages). The image only rebuilds when this list changes.

**Non-default outputs (`.dev` for headers + `pkg-config`):** Nixpkgs splits many libraries into outputs — the default output ships only the runtime `.so`, while `.dev` carries headers and `.pc` files. For cgo / FFI builds, request the `.dev` output with a dotted shorthand or an explicit `outputs` array:

```jsonc
{
  "packages": [
    "gtk4.dev",                                      // dotted shorthand
    {"name": "gtk4", "outputs": ["out", "dev"]}      // explicit form
  ]
}
```

When a `.dev` output is selected, the image also pulls in the `.dev` outputs of every transitively propagated build input — so `pkg-config --cflags gtk4` resolves `pango → harfbuzz → fontconfig → …` without listing each by hand. `PKG_CONFIG_PATH` is preset so the `.pc` files are found out of the box. The package's runtime libraries (and those of its propagated closure) are linked into `/lib` as well, so binaries built against a `.dev` request also run — a bare `"gtk4.dev"` covers both compile and runtime.

Common outputs: `out` (default), `dev` (headers + pkg-config), `bin`, `lib`, `man`, `doc`.

**Pinned versions:** Pin to a specific nixpkgs commit for reproducibility. `outputs` works alongside pinning:

```jsonc
{
  "packages": [
    "postgresql",
    {"name": "freetype", "nixpkgs": "e6f23dc0..."},
    {"name": "gtk4", "nixpkgs": "e6f23dc0...", "outputs": ["out", "dev"]}
  ]
}
```

Find nixpkgs commits for specific versions at [lazamar.co.uk/nix-versions](https://lazamar.co.uk/nix-versions/).

### Mise Tools (Runtime-Level)

Add tools to your workspace's `mise.toml` for workspace-specific runtimes:

```toml
# mise.toml
[tools]
typst = "latest"
rust = "1.80"
```

On jail startup, `mise install` fetches declared tools. They persist across restarts in the jail-land mise store mounted at `/mise` — shared by every jail, fully independent of the host's own mise installation.

To inject tools into all jails globally, use `mise_tools` in your config:

```jsonc
{
  "mise_tools": {"neovim": "stable", "typst": "latest"}
}
```

---

## Blocked Tools

**Nothing is blocked by default.** yolo's default blocked list is empty. Blocking is opt-in, two ways:

- **The `guardrails` pack** — add `"packs": ["guardrails"]` and `grep` (with recursive flags) and `find` refuse, pointing at `rg` and `fd`.
- **Your own `security.blocked_tools`** — any tool you name, with your own message.

An entry of yours naming the same tool as a pack's **replaces** the pack's wholesale. A block is only
generated when the replacement binary is actually on the agent's PATH, so a block can never leave the
jail with neither the tool nor its alternative.

What `guardrails` blocks, and what it suggests instead:

| Blocked | Suggestion |
|---------|-----------|
| `grep` (recursive flags) | Use `rg` (ripgrep) |
| `find` | Use `fd` |

### Customize Blocked Tools

```jsonc
{
  "security": {
    "blocked_tools": [
      {"name": "grep", "message": "Use rg", "suggestion": "rg <pattern>"},
      {"name": "curl", "message": "Network access blocked"}
    ]
  }
}
```

### Bypass

Set `YOLO_BYPASS_SHIMS=1` in scripts that need blocked tools:

```bash
YOLO_BYPASS_SHIMS=1 grep -r "pattern" .
```

---

## Device Passthrough

**Platform support:** Device passthrough (USB, serial, cgroup rules) is a **Linux-only** feature. It relies on the host kernel exposing `/dev/bus/usb/`, `/dev/tty*`, and `--device-cgroup-rule` — none of which exist on macOS where containers run inside a VM. On macOS, device entries in `yolo-jail.jsonc` are parsed, logged as skipped with a warning, and do not prevent the jail from starting.

On Linux, pass host devices (USB, serial, etc.) into the jail:

```jsonc
{
  "devices": [
    {"usb": "0bda:2838", "description": "RTL-SDR"},
    "/dev/ttyUSB0",
    {"cgroup_rule": "c 189:* rwm"}
  ]
}
```

**Formats:**
- **USB by vendor:product ID** (preferred — stable across reboots): `{"usb": "0bda:2838"}`
- **Raw device path** (changes on replug): `"/dev/bus/usb/001/004"`
- **Cgroup rule** (broad access): `{"cgroup_rule": "c 189:* rwm"}`

Missing devices produce a warning but don't prevent the jail from starting. Device changes are subject to [config safety](#config-safety) approval.

---

## GPU Passthrough (NVIDIA)

**Platform support:** GPU passthrough is **Linux-only**. Apple Silicon Macs use Metal, not CUDA/OpenCL, and Apple's Virtualization.framework doesn't expose the GPU to the guest Linux kernel. If `"gpu": {"enabled": true}` appears in `yolo-jail.jsonc` on macOS, it is parsed, logged as skipped with a warning, and does not prevent the jail from starting. For GPU workflows on macOS, run on a Linux box (local or EC2 `g5`/`p3` instance) instead.

On Linux, train deep learning models inside the jail using NVIDIA GPUs. Requires the NVIDIA Container Toolkit on the host.

### Host Setup

1. **Verify your GPU driver:**
   ```bash
   nvidia-smi
   ```

2. **Install the NVIDIA Container Toolkit:**
   ```bash
   curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey \
     | sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
   curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list \
     | sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' \
     | sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list
   sudo apt-get update && sudo apt-get install -y nvidia-container-toolkit
   ```

3. **Configure the container runtime:**
   ```bash
   # Podman (CDI)
   sudo nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml
   ```

4. **Validate:**
   ```bash
   yolo check   # GPU section will show nvidia-smi, nvidia-ctk, CDI spec status
   ```

### Jail Configuration

```jsonc
// yolo-jail.jsonc
{
  "gpu": {
    "enabled": true,
    "devices": "all",
    "capabilities": "compute,utility"
  }
}
```

| Key | Default | Description |
|-----|---------|-------------|
| `enabled` | `false` | Enable GPU passthrough |
| `devices` | `"all"` | `"all"`, or specific GPUs: `"0"`, `"0,1"`, `"GPU-<uuid>"` |
| `capabilities` | `"compute,utility"` | NVIDIA driver capabilities to expose |

### Installing PyTorch

Once inside the GPU-enabled jail:

```bash
pip install torch torchvision
python -c "import torch; print(torch.cuda.is_available(), torch.cuda.get_device_name(0))"
```

### Runtime Details

| Runtime | Mechanism | Notes |
|---------|-----------|-------|
| **Podman** | `--device nvidia.com/gpu=all` (CDI) | Requires CDI spec at `/etc/cdi/nvidia.yaml` |

- **Podman:** the NVIDIA branch passes identity uid/gid maps (`0:0:1` plus `1:1:65536`) and `--runtime runc`, because crun has CDI bugs. It also omits `/dev/fuse`, which the ordinary branch passes.
- **Nested podman-in-podman with GPU is not supported, and is not currently *prevented*:** the nesting branch is chosen first, so a nested GPU launch gets the nesting flags while the CDI device flags are still emitted. Don't rely on it.
- **Shared memory:** `/dev/shm` is a tmpfs sized 2 GB (`--tmpfs /dev/shm:size=2g`, not `--shm-size`) for PyTorch multi-process data loading. On Apple Container it is a tmpfs with no size argument, so the guest default applies.
- **CUDA forward compatibility:** CUDA in the container can be newer than the host driver, but not the reverse.

### AWS EC2

Use an AWS Deep Learning AMI (DLAMI) — drivers and toolkit come pre-installed.

| Instance | GPU | VRAM | Use Case | $/hr (approx) |
|----------|-----|------|----------|----------------|
| g4dn.xlarge | 1× T4 | 16 GB | Inference, light training | ~$0.53 |
| g5.xlarge | 1× A10G | 24 GB | Training + inference | ~$1.01 |
| p3.2xlarge | 1× V100 | 16 GB | Training | ~$3.06 |

### Troubleshooting GPU

- **`conmon bytes "": readObjectStart` error (Podman):** Caused by `crun` OCI runtime's CDI handling bug ([podman#27483](https://github.com/containers/podman/issues/27483)). The jail automatically uses `--runtime runc` when GPU is enabled to work around this. If you see this, update your `yolo-jail` installation.
- **`nvidia-smi` not found inside jail:** The NVIDIA Container Toolkit injects driver libs at container start. Check the toolkit is installed and configured on the host.
- **CUDA out of memory:** Reduce batch size, or limit which GPUs are exposed with `"devices": "0"`.

---

## AMD GPU Passthrough (ROCm)

**Platform support:** Like NVIDIA, AMD/ROCm passthrough is **Linux-only**. Apple Silicon Macs have no ROCm path, and Apple's Virtualization.framework doesn't expose the GPU to the guest. If `"gpu": {"enabled": true, "vendor": "amd"}` appears in `yolo-jail.jsonc` on macOS, it is parsed, logged as skipped with a warning, and does not prevent the jail from starting.

On Linux, run ROCm compute workloads inside the jail using AMD GPUs. Unlike NVIDIA, the default path needs **no host container toolkit** — just the `amdgpu` kernel driver and the right group membership. ROCm passthrough works on both AMD Instinct and consumer Radeon hardware.

> **Note:** ROCm userspace (HIP, rocm-smi, math libs) is **not** injected from the host. Unlike the NVIDIA toolkit, AMD's device-node and CDI paths inject **only kernel device nodes** (`/dev/kfd`, `/dev/dri/renderD*`). Your container image must ship its own ROCm userspace — use a `rocm/*` base image (see the PyTorch example below).

### Host Setup

> Verified end-to-end on real AMD hardware (Radeon 8060S / gfx1151, ROCm 7.2, rootless podman + crun) — both the default device-node mode and CDI mode run ROCm PyTorch inside the jail. Package names and exact group names still vary by distribution, so adapt the commands below to yours.

1. **Install the `amdgpu` kernel driver.** On Ubuntu, AMD ships `amdgpu-dkms` via the ROCm `amdgpu-install` tooling (other distros package it directly, e.g. Arch's `linux*-headers` + mainline `amdgpu`):
   ```bash
   sudo amdgpu-install --usecase=dkms
   ```
   Confirm the module is loaded:
   ```bash
   ls /sys/module/amdgpu        # present when the driver is loaded
   ls /dev/kfd /dev/dri/renderD*  # compute + render device nodes
   ```

2. **Ensure your host user can open the device nodes.** `/dev/kfd` and `/dev/dri/renderD*` are commonly owned by the `render` group (`/dev/dri/card*` by `video`), in which case your host user must be a member:
   ```bash
   sudo usermod -aG render,video "$USER"
   # log out and back in (or `newgrp render`) for the new groups to take effect
   ```
   Some distributions instead ship these nodes world-readable/writable (mode `0666`), in which case no group membership is needed — `yolo check` reports whether the nodes are openable by the current user either way. `--group-add keep-groups` (which yolo always adds) preserves whatever host groups you already hold into the rootless container.

3. **(Optional) AMD Container Toolkit for CDI mode.** The default `mode: "devices"` needs no toolkit. Only if you want CDI-style per-GPU selection (`mode: "cdi"`), install the AMD Container Toolkit and generate a spec:
   ```bash
   sudo amd-ctk cdi generate --output=/etc/cdi/amd.json
   amd-ctk cdi list   # should list amd.com/gpu=all, amd.com/gpu=0, ...
   ```
   The generated spec injects **only device nodes** (no env vars, hooks, or host-library mounts — verified), and CDI mode runs ROCm correctly under the default crun runtime (no `runc` workaround needed). On a single-GPU host CDI offers no advantage over the default device-node mode.

4. **Validate:**
   ```bash
   yolo check   # GPU section shows amdgpu module, rocminfo, device nodes, group membership
   ```

### Jail Configuration

```jsonc
// yolo-jail.jsonc
{
  "gpu": {
    "enabled": true,
    "vendor": "amd",
    "devices": "all"
  }
}
```

| Key | Default | Description |
|-----|---------|-------------|
| `vendor` | `"nvidia"` | Set to `"amd"` for ROCm passthrough. Absent ⇒ NVIDIA (backward-compatible). |
| `enabled` | `false` | Enable GPU passthrough |
| `devices` | `"all"` | `"all"`, or specific GPUs: `"0"`, `"0,1"` |
| `mode` | `"devices"` | AMD only. `"devices"` = raw device nodes (no host toolkit); `"cdi"` = `amd.com/gpu` via the AMD Container Toolkit |
| `hsa_override_gfx_version` | _(unset)_ | AMD only, optional. Override the gfx target for unsupported/consumer GPUs (e.g. `"11.0.0"`) |
| `seccomp_unconfined` | `false` | AMD only, optional. Opt-in `--security-opt seccomp=unconfined` (only needed for some HPC/numactl workloads; widens the syscall surface) |

> `capabilities` is **NVIDIA-only** — ROCm has no driver-capabilities concept, so it is rejected for `vendor: "amd"`.

### Installing PyTorch (ROCm)

ROCm userspace ships in the image, so start from a `rocm/*` base image that already includes a ROCm-built PyTorch (for example a `rocm/pytorch` image), or install the ROCm wheels from AMD's index inside such an image (pick the `rocmX.Y` index matching the image's ROCm version):

```bash
# inside a rocm/* based jail image
pip install torch --index-url https://download.pytorch.org/whl/rocm6.2
python -c "import torch; print(torch.cuda.is_available(), torch.cuda.get_device_name(0))"
# verified output on a Radeon 8060S (gfx1151): True Radeon 8060S Graphics
```

ROCm exposes the AMD GPU through PyTorch's `torch.cuda` API, so `torch.cuda.is_available()` returning `True` means ROCm is working. A generic (non-ROCm) image will get working device nodes but **no working HIP/rocm-smi**.

### Runtime Details

| Runtime | Mechanism | Notes |
|---------|-----------|-------|
| **Podman** | `--device /dev/kfd` + `--device /dev/dri/renderD*` (or `--device /dev/dri` for `all`) + `--group-add keep-groups` | Default `mode: "devices"`; no host toolkit required |
| **Podman (CDI)** | `--device amd.com/gpu=all` + `--group-add keep-groups` | `mode: "cdi"`; requires `/etc/cdi/amd.json` from `amd-ctk` |

- **Runtime:** AMD stays on the default **crun** runtime. `--group-add keep-groups` is crun-only — it preserves the host `render`/`video` GID so `/dev/kfd` is openable rootless. AMD does **not** use NVIDIA's `--runtime runc` workaround.
- **`/dev/kfd` is shared:** the Kernel Fusion Driver node is a single interface shared by all GPUs and is always passed in. Per-GPU restriction comes from which `/dev/dri/renderD*` nodes you select via `devices`.
- **Locked-memory limit:** whenever GPU passthrough is active, yolo lifts the container's locked-memory *soft* limit to the host's hard cap (`--ulimit memlock=<host-hard>:<host-hard>`, or `-1` if the host is already unlimited). A rootless container can't raise the *hard* cap above the host's, so this gives GPU runtimes the most they can pin. Current ROCm (verified on gfx1151 / ROCm 7.2) runs GPU compute fine at the common 8 MB rootless cap — no host change is needed. (Older ROCm builds pinned a larger queue ring buffer; see the troubleshooting note below if you ever hit `AMDKFD_IOC_CREATE_QUEUE EINVAL`.)
- **In-container GPU selection:** for an explicit `devices` selection (e.g. `"0"` or `"0,1"`), yolo sets `ROCR_VISIBLE_DEVICES` and `HIP_VISIBLE_DEVICES` to that value. For the default `devices: "all"` it leaves them **unset** — unlike NVIDIA's `NVIDIA_VISIBLE_DEVICES`, the ROCr/HSA selector does **not** accept the literal `"all"` (it matches no device and hides every GPU), and ROCm's own default is "all GPUs visible". These env vars are **not a security boundary** — real isolation comes from which render nodes are passed in.

### Troubleshooting AMD GPU

- **`Unable to open /dev/kfd read-write: Permission denied`:** The container process lacks the owning group. Make sure your host user is in the `render` group (`sudo usermod -aG render "$USER"`, then re-login) — `--group-add keep-groups` only preserves groups the host user already holds.
- **GPU detected but ROCm errors out on a consumer Radeon:** Consumer/unsupported GPUs often need `HSA_OVERRIDE_GFX_VERSION` to be recognized as a supported gfx target (e.g. `"11.0.0"` for gfx1100, `"10.3.0"` for gfx1030, `"9.0.0"` for gfx900). Set it via `hsa_override_gfx_version` in the config. This is best-effort, same-architecture-family only, and unsupported by AMD.
- **`rocminfo`/`rocm-smi` not found inside jail:** ROCm userspace is **not** injected from the host — it must ship inside the image. Use a `rocm/*` base image instead of a generic one.
- **GPU enumerates and `hipMalloc` works, but any kernel launch segfaults (trace shows `AMDKFD_IOC_CREATE_QUEUE … EINVAL`):** an older ROCm userspace needs to pin a larger (~13 MB) queue ring buffer than the rootless default `RLIMIT_MEMLOCK` (often 8 MB) allows. Current ROCm (7.2+) does **not** hit this — first try a newer `rocm/*` image. If you're pinned to an older ROCm build, raise the **host's** memlock hard cap (a rootless jail can't exceed it): `limits.conf` `<user> hard memlock unlimited`, systemd `LimitMEMLOCK=infinity`, or podman `containers.conf` `default_ulimits = ["memlock=-1:-1"]`, then restart the jail.
- **`torch.cuda.is_available()` is `False` / `rocminfo` shows only the CPU agent, but the device nodes are present:** if you set `ROCR_VISIBLE_DEVICES=all` (or `HIP_VISIBLE_DEVICES=all`) yourself, ROCm sees zero GPUs — the selector does not accept `"all"`. Leave it unset for all GPUs, or use explicit indices (`0`, `0,1`). yolo handles this for you (it omits the env vars when `devices: "all"`), so this only bites if you override them manually inside the container.

---

## Loopholes (spawned host services)

**A way to split the jail boundary cleanly.** A *spawned* loophole is a process that runs on the host (outside the jail) and publishes an address the jail can reach through a per-jail directory bind-mounted at `/run/yolo-services/`. The agent inside the jail can talk to the loophole without ever holding its secrets, credentials, or privileges. See [docs/guides/loopholes.md](loopholes.md) for the broader loophole system (including intercepting loopholes like the Claude OAuth broker).

> **Two shapes, one transport.** A loophole shipped as a `manifest.jsonc` uses the framework's `loopback-tls` transport: it publishes `/run/yolo-services/<name>.endpoint`, a `0600` file naming a `127.0.0.1` listener plus the certificate to pin and this jail's bearer token. A service declared in the `loopholes:` config block below **gets the same thing** — it used to get a plain Unix socket at `/run/yolo-services/<name>.sock`, on the reasoning that nothing yolo shipped let a daemon it did not write publish an endpoint file. yolo's *front* removed that objection: the daemon still binds an ordinary AF_UNIX socket, now at a host-only path, and yolo runs an authenticated front over it and publishes the endpoint file itself. So the **daemon** side of this section is unchanged from the socket era, and the **client** side is not: from inside the jail you dial a TCP address with a pinned certificate and a bearer token, never a socket file.

The privileged-operations pattern this generalizes is the cgroup delegate — a host-side listener performs cgroup operations on behalf of the container so the jail itself doesn't need `CAP_SYS_ADMIN` or rw cgroup mounts. The delegate is not itself one of the services below (it runs in-process in `yolo` and is the one service still on a bind-mounted socket, for the reason under [Security model](#security-model)), but the `loopholes` config block lets you define your own host-side trust in the same shape.

### When to use it

- **Auth / credential brokers.** A service holds API keys, OAuth tokens, or signed JWTs and answers scoped requests from the agent. The jail never sees the raw credentials.
- **Access control proxies.** A service fronts an internal API and enforces "agent X may only call endpoint Y with payload Z" rules outside the jail.
- **Audit / logging sinks.** A service receives structured events from the agent and writes them to a host-side log the jail can't tamper with.
- **Resource brokers.** Anything where you want a small piece of host-side trust without pulling the entire dependency into the jail.

### Configuration

```jsonc
{
  "loopholes": {
    "auth-broker": {
      // Command to launch on the host when the jail starts.
      // "{endpoint}" is substituted with the HOST-ONLY socket path the
      // service should bind — under /tmp, deliberately outside the
      // directory the jail sees, so the jail reaches the daemon only
      // through yolo's front.  ("{socket}" is an accepted alias.)
      "command": ["~/code/auth-broker/serve.py", "--socket", "{endpoint}"],

      // Optional environment variables for the host daemon (NOT the jail).
      "env": {
        "KEYS_FILE": "~/secrets/broker-keys.json",
        "LOG_LEVEL": "info"
      },

      // Optional override of where the ENDPOINT FILE appears inside the
      // jail.  Must start with /run/yolo-services/ — that's the only
      // directory that gets bind-mounted in, and the prefix is validated.
      // Default: /run/yolo-services/<name>.endpoint
      // ("jail_socket" is the older spelling of this key, still accepted.)
      "jail_endpoint": "/run/yolo-services/auth-broker.endpoint"
    }
  }
}
```

The service name (`auth-broker` above) must match `^[a-zA-Z][a-zA-Z0-9_-]{0,63}$`. **No loophole name is reserved any more.** `cgroup-delegate` stopped being reserved on 2026-08-18, and `claude-oauth-broker` — the last reserved name — stopped when it became a contribution of the `claude` pack rather than a built-in. Both are ordinary pack-shipped loopholes now, enabled the same way as any other: `"loopholes": {"cgroup-delegate": {"enabled": true}}` (see below).

### Lifecycle

For each service, on `yolo run`:

1. Per-jail directory `/tmp/yolo-host-services-<8hex>/` is created on the host, mode `0700`, and bind-mounted into the jail at `/run/yolo-services/`. (It is **not** under your workspace — an earlier revision of this page said `<workspace>/.yolo/host-services/`, which was never true and is actively misleading now that a manifest loophole's endpoint file in there is a credential.)
2. yolo substitutes `{endpoint}` in the service's command with the path the daemon should bind, e.g. `/tmp/yolo-front-a1b2c3d4-auth-broker.sock`. (`{socket}` is an accepted alias.) That path is **not** in the directory from step 1: leaving it there would keep a raw socket reachable from inside the jail, and would let the jail unlink the daemon's own socket.
3. yolo launches the command as a child process. The service is expected to bind the socket at the substituted path.
4. yolo waits up to 5 seconds for the service to become reachable — for a daemon that binds a socket, that the socket accepts a *connect*, never that the file exists, which a leftover from a dead predecessor would satisfy instantly; for a daemon that publishes its own endpoint file, that the file *parses*, since a truncated file would otherwise read as healthy forever. If the service exits early or doesn't publish in time, yolo logs the failure and continues without that service.
5. yolo starts its own authenticated front over the daemon's socket, which publishes `/run/yolo-services/auth-broker.endpoint`. This happens only *after* step 4 succeeds: a front that bound earlier would make the endpoint look healthy while nothing was behind it, and every authenticated connection would be dropped at the dial. The container starts, and the agent inside reads that file.
6. When the container exits, yolo sends `SIGTERM` to each service it spawned, waits 5 seconds, then `SIGKILL`s its process group. **A *host-scoped* daemon is exempt, and the asymmetry is the point:** a loophole manifest may declare `"scope": "host"` in its `host_daemon` block, meaning one daemon per machine serving every jail on it. yolo *ensures* such a daemon rather than spawning it, and gives each jail its own front over the one socket — so a jail ending closes **only its own front** and never signals the daemon, which other jails are still using. It keeps running after your last jail exits, so nothing about your jail's teardown is how you inspect or cycle one: `yolo loopholes status` runs each loophole's own host-side self-check, and the loophole's own tooling replaces the daemon (for the Claude broker, `yolo broker restart`). Which shipped loopholes declare it changes, so derive the set rather than trusting a list: `rg -n '"scope": "host"' packs/*/loopholes/*/manifest.jsonc`.
7. The per-jail directory is removed — which is also how a manifest loophole's bearer token is retired, since the token lives only in the file inside it.

Service stdout and stderr are captured to `~/.local/share/yolo-jail/logs/host-service-<name>.log` for debugging.

### Discovering the service from inside the jail

For each service, yolo injects an env var so the agent doesn't need to hard-code the path:

```
YOLO_SERVICE_AUTH_BROKER_ENDPOINT=/run/yolo-services/auth-broker.endpoint
```

The variable name is `YOLO_SERVICE_<UPPERCASED-NAME>_ENDPOINT`, with non-alphanumeric characters replaced by underscores, and it is the same variable for both shapes. Its value is always a **path to the endpoint file** and never an address — the address lives inside the file, so it can change without relaunching the jail, whose environment is frozen at container start.

The `_SOCKET` spelling still exists, for exactly one service: the cgroup delegate, whose value really is a socket path (see [Security model](#security-model) for why that one cannot be fronted). It is a retiring spelling, not a second mechanism. The two suffixes are deliberately distinct rather than one being reused: the value's meaning differs, and a client that dials a regular file as though it were a socket reports something obscure, where a client that finds its variable absent reports "not wired up in this jail" and exits cleanly.

### Minimal example service

A trivial Python broker that hands out a single secret. The service runs on the host, holds the secret, and never reveals it to the jail — the jail just gets the resolved value for the key it asks about.

```python
# ~/code/auth-broker/serve.py
import json, os, socket, sys

KEYS = json.load(open(os.environ["KEYS_FILE"]))

sock_path = sys.argv[sys.argv.index("--socket") + 1]
srv = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
srv.bind(sock_path)
srv.listen(8)

while True:
    conn, _ = srv.accept()
    try:
        line = b""
        while not line.endswith(b"\n"):
            chunk = conn.recv(4096)
            if not chunk:
                break
            line += chunk
        req = json.loads(line)
        # Toy access control: the agent can only ask for keys in an allowlist.
        key = req.get("key")
        if key in {"OPENAI_API_KEY", "STRIPE_SECRET"}:
            conn.sendall(json.dumps({"value": KEYS[key]}).encode() + b"\n")
        else:
            conn.sendall(json.dumps({"error": "key not allowed"}).encode() + b"\n")
    finally:
        conn.close()
```

Hook it up in your workspace config:

```jsonc
{
  "loopholes": {
    "auth-broker": {
      "command": ["python3", "~/code/auth-broker/serve.py", "--socket", "{endpoint}"],
      "env": {"KEYS_FILE": "~/secrets/keys.json"}
    }
  }
}
```

The daemon above needs no change from what it would have been on the retired socket transport — it binds a socket and speaks its own newline-JSON protocol, and yolo's front splices bytes without translating them. **The client does.** This page used to show a `nc -U "$YOLO_SERVICE_AUTH_BROKER_SOCKET"` one-liner here, and there is no shell equivalent of what replaced it: a client reads `$YOLO_SERVICE_AUTH_BROKER_ENDPOINT`, reads *that file*, TLS-dials the address in it with the certificate in it as its only trust root, writes the token in it as a length-prefixed frame, and only then speaks the daemon's protocol. That needs a TLS library. `cmd/yolo-ps` is the reference implementation and the steps are enumerated in [`loophole-protocol.md`](../reference/loophole-protocol.md) §["Writing a client from scratch"](../reference/loophole-protocol.md#writing-a-client-from-scratch).

The secret never enters the jail filesystem, env vars, or any bind mount.

### Security model

**The boundary is "whatever runs as your user", on either shape.** That matches the host — anything running as you can already read your credentials or act as you — and a jail extends it unchanged. What differs is how it is enforced:

- **A fronted service** (config-declared): the per-jail directory is one half of the isolation — other jails cannot see it, because it is a separate mount — and the per-jail bearer token in its `0600` endpoint file is the other. The daemon's own socket is host-only, so nothing inside the jail can reach it at all; what the jail can reach is the front, which authenticates.
- **A manifest loophole** (`loopback-tls`): identical, minus the front — the daemon publishes the endpoint file itself. Either way the token is the enforcement, because a loopback TCP port has no "can connect implies authorized" property to inherit. `0600` on a path and a pre-shared token on a port say the same thing.
- **`SO_PEERCRED` does not survive a front, and that is why one service refused to move.** On a bind-mounted socket, a Linux service can read the connecting peer's host PID off the kernel; put a front in between and it reads *yolo's* PID instead, because yolo is the process that dialled. The cgroup delegate needs the real one (it writes the caller into a cgroup), so it is the one service still on a bind-mounted socket and the one still named by a `_SOCKET` variable. Even there the credential cannot separate the jail from a same-user host process: rootless podman maps the container's UID 0 to your uid, so both arrive carrying the same one. Treat it as attribution, not as a boundary.
- What the service does with secrets, scopes, audit logging, and rate limiting is entirely up to the service. yolo just wires the plumbing.
- The cgroup delegate is this pattern applied to a privileged operation — proof that host-side trust in front of a jail is enough to carry one safely — though it is a listener inside `yolo` rather than a spawned child, so it is not literally one of the services configured here.

### Validation

`yolo check` verifies that each configured service's command exists and is executable. Catches typos before the next jail start.

### Apple Container caveat

Every host service but one is skipped on the `container` runtime. The allowance is by name — `openai-auth-broker`, the OpenAI credential broker — and it is the reason the services directory is mounted there at all; every other service, yours included, prints one yellow inert line per launch and does not start. Use `podman` if you need this feature on macOS.

The reason **used to be** the transport: Apple Container doesn't bind-mount Unix sockets through virtiofs. The `loopback-tls` transport removes that obstacle — it is a TCP connection, not a socket file — and the mount question is answered too, since an endpoint file crosses in an ordinary directory bind that this backend handles. What holds the skip in place now is a **measurement**: on `container` 1.1.0 a container→host connection completes its handshake and then carries nothing (by two alternating mechanisms), no bind address helps, and `host.containers.internal` does not resolve. A loopback-TLS loophole needs exactly that dial, so it could not be reached from the jail even if it were started. This is **deferred on a measured blocker an upstream release can expire**, not a design decision — `integration/applecontainer_test.go`'s host-loopback witness is what to re-run before believing it still holds.

---

## Storage & Persistence

### Seeing what is on disk — `yolo stores`

```bash
yolo stores            # the inventory: every store, its size, who reclaims it, and what nothing does
yolo stores --no-record  # ...without appending a dated sample
```

It **deletes, moves and mutates nothing** — the one exception is a bounded sample ledger (one dated
line per store per run, last 30 kept) so that a second run can report a growth RATE rather than just
a size. `--no-record` opts out of that.

The column worth reading is the last one. `yolo` means yolo has a reclaimer and a trigger for that
store; `human` means it does not and you decide; `not yolo's` means it is somebody else's bytes.
Untagged container images are the standing `human` row: once an image loses its repository name,
yolo has no evidence it was ever yolo's, so it never removes one — `podman image prune` is yours to
run if you want them gone.

Reclaiming is `yolo prune` (dry-run by default, `--apply` to act). Most classes are also reclaimed
automatically after a jail starts, at most once a day each; `YOLO_NO_AUTO_IMAGE_REAP=1` turns that
off. **That automatic work never prints to your terminal** — by the time it runs, the terminal
belongs to whatever is running in the jail, and a line there would land on top of it. It is
recorded in `<workspace>/.yolo/housekeeping.log` instead, beside `boot.log`:

```bash
tail ~/code/myproject/.yolo/housekeeping.log
```

The one class yolo will not reclaim without asking is the shared build-tool cache, because
re-fetching it is unbounded — you will be offered it on a TTY launch when there is at least a
gigabyte of it older than 30 days, and answering "never" stops the asking for good.

All paths below use the same layout on Linux and macOS (`~/.local/share/yolo-jail/` resolves to `/home/$USER/.local/share/yolo-jail/` on Linux and `/Users/$USER/.local/share/yolo-jail/` on macOS). On macOS with Podman Machine, make sure `$HOME` is in the VM's shared folders list.

### Timezone

The host's timezone is passed into the jail via the `TZ` env var, so `date`, log timestamps, cron expressions, and file mtimes inside the jail report the same wall-clock time as the host. Detection order:

1. `$TZ` on the host (if you've explicitly set one, it wins)
2. `/etc/timezone` plain-text zone name (Debian, Ubuntu, Arch)
3. `/etc/localtime` symlink target suffix (Fedora, macOS — `/var/db/timezone/zoneinfo/<zone>`)

If none of these resolve, the jail falls back to UTC. Override per-jail by exporting `TZ` in the shell you use to launch `yolo`, or by setting it in `env` inside `yolo-jail.jsonc`.

### What Persists Across Restarts

| Data | Location (Host) | Shared? |
|------|-----------------|---------|
| Claude and Antigravity (`agy`) logins | `~/.local/share/yolo-jail/home/.claude-shared-credentials/`, `.gemini-shared-credentials/` | All jails on the machine |
| `gh`, `copilot`, `opencode` and `omp` logins | `<workspace>/.yolo/home/` | Per workspace |
| Installed tools (npm, go) | `~/.local/share/yolo-jail/home/` | All jails |
| Mise tools & runtimes | `~/.local/share/yolo-jail/mise/` on Linux (bind-mounted at `/mise` inside the jail); podman named volume `yolo-mise-data-v2` on macOS and Apple Container, also mounted at `/mise` | All jails |
| Bash history | `<workspace>/.yolo/home/bash_history` | Per workspace |
| Claude sessions | `<workspace>/.yolo/home/claude/projects/` | Per workspace |
| Copilot sessions | `<workspace>/.yolo/home/copilot/session-state/` | Per workspace |
| SSH keys | `<workspace>/.yolo/home/ssh/` | Per workspace |

**Mise storage is jail-land only:** the host's `~/.local/share/mise/` is never mounted — jails and the host maintain fully independent mise installations, so neither side can break the other's tool installs and host↔jail mise version skew doesn't matter. Every jail sees the same store at the same path, `/mise`; only the backing differs per platform (a yolo-owned host directory on Linux, the `yolo-mise-data-v2` named volume on macOS and Apple Container). In-jail behavior is identical everywhere, and the store persists across jail restarts.

### What Gets Regenerated

On every jail start, the entrypoint regenerates:
- `.bashrc` — prompt, aliases, PATH, mise integration
- Shim scripts — blocked tool interceptors
- MCP config — `mcp-config.json` / `settings.json`
- LSP config — `lsp-config.json`
- Bootstrap script — tool installation (idempotent)

### Relocating a Cache Subdir to Other Storage

Every jail shares one cache directory, `~/.local/share/yolo-jail/cache`, bind-mounted read-write at `~/.cache` inside the container. One exception, and it needs no configuration: if this machine has a **content-addressed** cache yolo recognises — `~/.cache/pants/lmdb_store` today — a jail mounts *your own* copy of it, writable, in place of keeping a second one, so up to ~27 GB stops existing twice. The launch says so on stderr when it happens, `yolo stores` lists it in its own section as bytes yolo will never reclaim, and the jail's now-unused private copy is left for the ordinary 30-day cache purge. It is skipped entirely on macOS, on Apple Container, and whenever the host store is missing, unwritable, or empty while the jail's copy is warm — in every one of those cases the jail simply keeps its own copy, exactly as before. It sits on whatever filesystem `$HOME` is on, and some of its subdirs get very large. `cache_relocations` lets one subdir come from a different disk instead:

```jsonc
// ~/.config/yolo-jail/config.jsonc — user scope ONLY, never yolo-jail.jsonc
{
  "cache_relocations": {
    // cache subdir name → absolute host path
    "huggingface": "/data/relocated/yolo-jail/cache/huggingface"
  }
}
```

That mounts the target read-write at `/home/agent/.cache/huggingface`, nested inside the usual cache mount. Inside the jail it is an ordinary writable directory — `HF_HOME` and every other tool's cache path stay exactly as they are.

Two constraints worth knowing before you plan a move:

- **User scope only.** The key is read straight from `~/.config/yolo-jail/config.jsonc` and never from the merged config. `/workspace` is bind-mounted read-write into the jail, so a workspace config is agent-editable — it must not be able to hand out read-write host mounts. `yolo check` errors if the key shows up in `yolo-jail.jsonc`.
- **Podman only.** Apple Container (`runtime: "container"`) warns and skips the relocation. The `macos-user` backend has no container and no bind mounts, and since 2026-08-24 it warns too. **There is no workaround there** — an earlier version of this line suggested a plain host symlink, which does not work: that backend's sandbox profile denies writes outside your workspace, its own home, `/tmp` and `/var/folders`, and denies reads under `/Volumes`, so a symlink into other storage resolves to a path the agent cannot use.

The target's **parent** must already exist; only the last path component is created for you. That asymmetry is deliberate — auto-creating the whole path turns a typo like `/data/relcoated/…` into a silently-wrong empty directory back on the root filesystem, which is the exact failure the feature exists to prevent.

#### When it's worth it

Relocate caches that are **large, cold, and write-once/read-sequential** — nothing about them wants to be on NVMe:

| Subdir | Relocate? | Why |
|--------|-----------|-----|
| `huggingface` | **Yes** | Model repos, tens of GiB each, downloaded once and then read sequentially |
| `ms-playwright` | **Yes** | Browser bundles, ~400 MiB apiece, written on install and never again |
| `uv`, `pip`, `go-build` | **No** | Touched on every build and latency-sensitive — moving them to a slow disk makes every build slower |

The concrete case that motivated the feature: a 948 GiB root filesystem at **100% full**, with 241 GiB of it the yolo cache — and 185 GiB of *that* a single `cache/huggingface` holding about fifteen diffusers model repos (FLUX.1-schnell 32G, FLUX.1-Kontext-dev 32G, SDXL-base 20G, …). An 11 TB HDD on the same machine sat at 53%. One subdir, one line of config. `yolo prune` prints a `cache/ top 5` panel if you want to find your own equivalent.

#### Migrating an existing cache

Setting the key on a fresh machine needs nothing else. Moving a cache that already has bytes in it is a manual copy for now — do it in this order:

1. **Stop every jail first.** `yolo ps` lists what's running; exit each session (or `podman stop <name>`). This is not just about a torn copy from a download racing your `rsync`. Podman bind mounts are `rprivate`: a container's mounts are fixed when it starts, so a jail that was already up keeps the *old* host directory mounted at `~/.cache/huggingface` no matter what you change afterwards. Step 3 only copies, so nothing looks wrong yet — but once step 7 deletes that old directory, the agent inside a still-running jail sees its cache vanish mid-session, even though the bytes are safely at the new target. It reads exactly like cache corruption, and the only fix is the restart you skipped.

2. **Create the target's parent** (yolo creates the final component itself):

   ```bash
   mkdir -p /data/relocated/yolo-jail/cache
   ```

3. **Copy the bytes.**

   ```bash
   rsync -aH --info=progress2 \
     ~/.local/share/yolo-jail/cache/huggingface/ \
     /data/relocated/yolo-jail/cache/huggingface/
   ```

   `-a` is the load-bearing flag. `huggingface_hub` stores each file once under `blobs/<etag>` and points at it from `snapshots/<rev>/<file>` with a *relative* symlink; `-a` implies `-l`, so the links are copied as links and their targets stay valid at the new location. `-H` is cheap insurance for subdirs that use hard links (an HF cache uses none). What you must **not** add is `-L`/`--copy-links` — nor reach for `cp -aL`, `tar -h`, or a file manager that dereferences — since that writes a full copy behind every snapshot link, roughly doubling the transfer and more when several revisions of a repo are cached. That refills the disk you are trying to empty.

4. **Verify** before you delete anything. A second `rsync` in dry-run mode should have nothing left to do:

   ```bash
   du -sh ~/.local/share/yolo-jail/cache/huggingface /data/relocated/yolo-jail/cache/huggingface
   rsync -aHn --itemize-changes \
     ~/.local/share/yolo-jail/cache/huggingface/ \
     /data/relocated/yolo-jail/cache/huggingface/
   ```

5. **Set the config** in `~/.config/yolo-jail/config.jsonc` as shown above, then `yolo check` — it validates the key's scope, the subdir names, and that each target's parent exists.

6. **Restart your jails** and confirm from inside one that `~/.cache/huggingface` still has the models and is writable.

7. **Reclaim the space.** Only now delete the original: `rm -rf ~/.local/share/yolo-jail/cache/huggingface`. yolo recreates it as an empty stub mountpoint on the next start.

#### Symlinking a cache subdir does not work

It is tempting to skip all of the above and just `ln -s /data/… ~/.local/share/yolo-jail/cache/huggingface`. It does not work, and it fails confusingly.

The whole cache directory is bind-mounted into the container as one unit, and podman resolves the **source path** of that mount — not the symlinks inside it. The container therefore gets a symlink pointing at `/data/…`, a path that does not exist in the container's mount namespace. Every in-jail download then fails on a dangling path, while the same symlink resolves perfectly when you `ls` it on the host.

The one exception is `cache/images`, which holds jail image tarballs. Those are only ever read host-side, before any container exists, so symlinking that subdir is safe. Nothing else in the cache is. (Nothing WRITES a tarball there any more — the image is copied layer by layer since layer-aware delivery landed — so what is left is a backlog `yolo prune` reclaims.)

---

## Container Reuse

By default, `yolo` reuses an existing container for the same workspace:

```bash
yolo             # Creates container yolo-<hash>
yolo             # Reuses yolo-<hash> via exec
yolo stop       # Stops this workspace's jail (the next launch is fresh)
```

Containers are named deterministically based on the workspace path. Use `yolo ps` to see running containers.

---

## Config Safety

When `yolo-jail.jsonc` changes between jail startups, the CLI shows a normalized diff and asks for confirmation:

```
Config has changed since last confirmed session.
Diff:
  + "packages": ["postgresql"]

Accept this config? [y/N]:
```

This prevents agents from silently adding packages, mounts, or devices. The human must approve every change.

**The record of what you approved lives on the host**, at
`~/.local/share/yolo-jail/approvals/<container-name>.json`. It is deliberately *not* in the
workspace: `/workspace` is bind-mounted read-write, so anything that can edit `yolo-jail.jsonc`
could also have rewritten a baseline kept in there, and the next launch would have had nothing to
show you. A workspace copied or moved to a new path loses its baseline and re-prompts once.

**A launch with no terminal (CI, `yolo … < /dev/null`, a script) is REFUSED when the config
changed** — it is not auto-accepted. The refusal prints the diff and tells you the flag:

```console
$ yolo --accept-config-changes -- ./ci-task.sh
```

That approves the change for **that launch only** and records it exactly as answering `y` does. It
is a flag rather than an environment variable on purpose: `YOLO_ALLOW_*` variables suppress a
*diagnosis*, this one grants an *approval*, and an approval must not be inherited by every child
process or linger in a shell for the rest of a session.

### Workflow for Config Changes

**From outside the jail (handoff to agent):**
1. Edit `yolo-jail.jsonc`
2. Run `yolo check` to validate
3. Fix any errors
4. Run `yolo` to start the jail (will see diff and prompt for approval)

**From inside the jail (agent edits mid-session):**
1. Agent edits `yolo-jail.jsonc`
2. Agent runs `yolo check --no-build` for fast validation
3. Agent fixes any reported problems
4. Agent asks human to restart: _"I've updated the config. Please restart the jail."_
5. Human exits and runs `yolo` again (sees diff, approves)

An agent can confirm at any point whether a restart is actually needed with
`yolo config drift`: it compares the workspace config on disk against the one the
running jail was started with, and exits `0` if they match, `3` if they differ
(printing the diff), or `4` if it cannot tell (no baseline). Because the config the
jail is *running under* is fixed until a restart, this is how an agent knows its
edit has not yet taken effect. To see the full effective config the jail is running
under — the merged, canonicalized form — use `yolo config dump`.

See [../reference/config-safety.md](../reference/config-safety.md) for the full workflow.

---

## What works in each setup

yolo can run a jail four ways, depending on your computer and what you have installed. They do not all
support the same things; find your column below.

| Column | `runtime` value | Host OS | What it is |
|---|---|---|---|
| `podman` / Linux | `podman` | Linux | Containers on your own Linux machine. Everything in this guide works here. |
| `podman` / macOS | `podman` | macOS | Containers inside a Linux virtual machine that Podman runs on your Mac (the Podman Machine). |
| `container` / macOS | `container` | macOS | [Apple Container](https://github.com/apple/container): each jail runs in its own small Linux virtual machine. **The Mac default.** |
| `macos-user` / macOS | `macos-user` | macOS | An ordinary Mac program running inside Apple's built-in sandbox. No container and no virtual machine, so your project stays at its real path and host folders cannot be added. |

**Words used below.** A **pack** is an add-on you list under `packs` in your user config; most install
an agent, such as `claude`. Some packs bring a **host service**: a small program yolo runs on your real
machine that the jail may talk to, such as the login services that let several jails share one login.
yolo calls a host service a **loophole**, because it is a deliberate hole in the jail's wall. A
**restart** means `yolo stop`, then `yolo`. Running `yolo` while the project's jail is already running
**joins** that jail instead, and a joined jail keeps the settings it started with.

### What you can do on each setup

The short answers, in the order you are likely to need them. **Yes** means it works. **Should work**
means it is expected to work, but nobody has tried it on that setup yet. **No** means it does not, and
the cell says whether a fix is coming: *planned* means the maintainers have scheduled it; *not planned
yet* means they have not. The subsections below, and
[Settings per setup](../reference/settings-per-setup.md), have the detail.

| You want to… | `podman` / Linux | `podman` / macOS | `container` / macOS | `macos-user` / macOS |
|---|---|---|---|---|
| **Start a jail** | Yes. The first launch builds the jail image and takes a few minutes; later launches take seconds. | Yes, once the Podman Machine VM is running and your project is in a folder it shares. The first launch is slow.[^cap-first-mac] | Yes, and it is the Mac default. `yolo stop` cannot see these jails; use `container stop` (not planned yet).[^cap-ac-stop] | Yes, after a one-time `yolo macos-setup`. Projects must be outside every home folder; `/Users/Shared/yolo` is set up for you.[^cap-mu-start] |
| **Open a second session** | Yes. `yolo` in the same project joins the running jail; another project gets its own jail. | Yes, the same. | Yes, the same, but jails for two different projects can log each other out of `claude`.[^refresh-ac] | Yes, but each `yolo` is its own sandbox, and two `claude` sessions at once can log each other out. |
| **Install an agent** | Yes. Add its pack to your user config; it installs the first time you type its name and keeps itself up to date. | Yes. `omp` is not available on Apple silicon.[^cap-omp] | Yes, except `omp`, which is not available on Apple silicon, the only kind of Mac Apple Container runs on.[^cap-omp] | Yes for `claude` and `codex`. `agy`, `copilot`, `opencode`, `pi` and (on Apple silicon only) `omp` should work. |
| **Run an agent without permission prompts** | Yes, automatically, for every agent except `omp`.[^cap-yolo-mode] | Yes, the same. | Yes, the same. | Yes, the same. |
| **Log in to an agent** | Yes. Log in once inside a jail; `claude`, `agy`, `codex` and `pi` then work in every project, while `copilot`, `opencode` and `omp` ask once per project.[^cap-logins] | Yes for `claude`, `agy`, `copilot`, `opencode` and `omp`. The shared `codex` and `pi` login should work.[^lh-mac] | Yes for `claude`, `agy`, `copilot` and `opencode`. **No for `codex` and `pi` subscription logins**; not planned yet.[^cap-ac-openai] | Yes, except that `codex`'s shared login has not been tried on a real Mac and can be lost in a long session (fix planned), and `pi`'s OpenAI subscription login does not work.[^login-mu] |
| **Use API keys and other providers** | Yes. Put keys in a dotenv file listed under `env_sources`; `yolo -p <profile>` picks the provider and model. A changed key reaches your next `yolo`. | Yes, the same. | Yes, but restart the jail after changing a key or `-p`: joining a running jail keeps the old ones. | Yes, read fresh at every launch.[^cap-mu-keys] |
| **Use your host's SSH keys, git credentials or `gh` login** | No, by design. Your git name and email do arrive, so commits work. To push, give the jail its own key or token. | No, the same. | No, the same. | No, the same. |
| **Work on your project** | Yes. It is at `/workspace`, live and read-write, and on rootless podman new files are yours. | Yes, the same, if the project is in a folder the VM shares. | Yes, the same. | Yes, in place at its real path; nothing is mounted. |
| **See other host folders and files** | Yes: folders read-only with `mounts`, single files with `host_files`. | Yes, if they are in a folder the VM shares. | Yes. `mounts` and `host_files` folders need Apple Container 1.1.0 or later. | Single files only, with `host_files`. No `mounts` and no folders; not planned yet. |
| **Add tools with `packages`** | Yes. Nix builds them into the jail image the next time the jail starts. With a nix daemon on the host, `YOLO_STORE_PACKAGES=1` skips the image rebuild. | Yes, but slower: each different list builds a whole Linux image.[^cap-pkg-mac] | Yes, but slower, the same as `podman` / macOS. | Yes, as native Mac builds. A package with no Mac build stops the launch.[^cap-pkg-mu] |
| **Add language runtimes with mise** | Yes, with `mise_tools` or the project's `mise.toml`. One tool store serves every project. | Yes, the same. | Yes, the same. | Yes, the same. |
| **Install things yourself** (`npm -g`, `uv tool`, `go install`) | Yes, and they are kept per project. The rest of the home is read-only unless you list a folder.[^cap-selfinstall] | Yes, the same. | Yes, kept per project. The whole home is writable. | Yes, kept per project. They are Mac programs, not Linux ones. |
| **Use `nix` inside the jail** | Yes, through your host's nix daemon.[^cap-nix] | No. It is possible, but not planned for now.[^cap-nix-mac] | No. It is possible, but not planned for now.[^cap-nix-mac] | Yes: the sandbox uses your Mac's own `nix`, through its nix daemon.[^cap-nix-mu] |
| **Use yolo's host services** (shared logins, AWS Bedrock credentials, a USB serial port…) | Yes. The login services run by themselves; you turn the others on. | Should work, apart from the Linux-only ones.[^lh-mac] | No: the jail cannot connect back to the Mac.[^lh-ac] | Mostly no. yolo starts them, but what each one needs inside the sandbox is missing, so only the OpenAI login service is usable, and only partly.[^login-mu] Bedrock (`aws-auth`) is not planned yet. |
| **Reach a service running on your host** | Yes. List the port in `network.forward_host_ports` (for example `[5432]`) and it appears on the jail's `localhost`; this needs `socat` on the host. Or connect to `host.containers.internal`.[^lh-rootful] | Yes, at `host.containers.internal`. | No; not planned yet. Do not set `forward_host_ports` here: it stops the launch. | Yes. The sandbox is on the Mac's own network, so `localhost` is the Mac and `forward_host_ports` is not needed. A remapped port (`"8080:9090"`) is not supported. |
| **Reach the internet** | Yes. | Yes. | Yes. On macOS 15, run `yolo check` first.[^cap-mac15] | Yes, and your local network too. |
| **Open a jail's dev server from your host** | Yes. Add the port to `network.ports` (for example `"ports": ["3000:3000"]`) and bind the server to `0.0.0.0`, then open `localhost:3000` on your host. | Yes, the same. | Not through `network.ports`, which carries no data here (not planned yet). Connect to the container's own address and port instead; `container ls` shows the address. | Yes. A port the agent opens is already open on the Mac. |
| **Use a GPU** | Yes, with `gpu` (NVIDIA or AMD). | No. | No. | No setting, but Metal should work as it does for any Mac program. |
| **Use a USB or serial device** | Yes, with `devices`, or a serial port through the `serial` host service. | Not with `devices`. A serial port through the `serial` host service should work.[^lh-mac] | No. | No. |
| **Run containers inside the jail** | Yes. podman is built in. | Should work (same image). | No. | No. |
| **Cap the jail's memory and CPU** | Yes, with `resources`. | Yes, within the VM's size. | Yes. With no setting the jail gets about half your RAM and cores. | No: nothing is enforced, and the launch says so. Not planned yet. |

[^cap-first-mac]: yolo builds a Linux image and copies it into the VM. On the Intel Mac that yolo's CI uses, the copy alone takes 15 to 22 minutes. If Apple Container is also installed, yolo picks it instead; set `YOLO_RUNTIME=podman` to keep podman.

[^cap-ac-stop]: On Apple Container, `yolo stop` prints `No jail running for this workspace` while the jail is running, and a jail left behind by a closed window is not cleaned up. Find the name with `container ls` and stop it with `container stop <name>`.

[^cap-mu-start]: A project inside a home folder is refused. `yolo macos-setup` prepares `/Users/Shared/yolo`, so a project created under it, such as `/Users/Shared/yolo/<name>`, needs nothing more. Launch with `YOLO_RUNTIME=macos-user yolo`, and expect `sudo` to ask for your password. There is no image to build, but the first launch builds the sandbox's tools with nix, which can take many minutes. See [the macos-user backend](macos.md#the-macos-user-backend).

[^cap-omp]: A jail on an Apple silicon Mac runs ARM Linux, and the `omp` vendor publishes no ARM Linux build, so the launch says `omp` is unavailable. The same is true on an ARM Linux machine. On `macos-user` it is the other way round: `omp` has a build for Apple silicon Macs but not for Intel ones. That depends on the vendor, not on yolo.

[^cap-yolo-mode]: yolo adds each agent's own no-prompts flag or setting, for example `--dangerously-skip-permissions` for `claude` and `--yolo` for `copilot`, and the launch prints the command it ran. `omp` has no such setting. Inside a jail this is always on; there is no switch to turn it off.

[^cap-logins]: Your host's own agent logins are not reused, and every login survives a restart. `claude` and `agy` keep one login for the whole machine. `codex` and `pi` share one OpenAI login through a login service yolo runs on your host. `copilot`, `opencode` and `omp` keep one per project; sharing them is not planned yet. Details: [Do I have to log in again in every workspace?](#5-do-i-have-to-log-in-again-in-every-workspace-and-in-a-second-jail-at-the-same-time)

[^cap-ac-openai]: The jail cannot reach yolo's OpenAI login service on Apple Container, so `codex` and `pi` print `OpenAI login is required.` and the browser login fails too. The launch says so. This was measured on Apple Container 1.1.0; a later Apple release may change it, so check `container --version`.

[^login-mu]: yolo's OpenAI login service must come up for a `macos-user` launch to go ahead; if it does not, the launch stops. `codex` then logs in, but refreshing its token needs a helper that does not run on `macos-user` yet, so a long session can lose its login; relaunch to get it back. A fix is planned. None of this has been tried on a real Mac yet. `pi`'s OpenAI subscription login does not work on `macos-user` because the file that connects `pi` to the login service is not delivered there; that is not planned yet.

[^cap-mu-keys]: The keys are kept in a file that only the sandbox can read, and yolo deletes it when the session ends. If you end a session by closing its window or with `kill`, that file can be left behind in yolo's state folder. Two more gaps. Agent config files that depend on the `-p` profile are written as if no profile were chosen (not planned yet). And `claude` with `cerebras` or `kilo` does not work here, because the in-jail helper they need does not run on `macos-user` (planned).

[^cap-pkg-mac]: The jail is Linux, so every package is built for Linux. When the nix binary cache does not have one, yolo starts a temporary Linux builder container for you; that needs the runtime running and your user trusted by the nix daemon (see [Building the image on macOS](#building-the-image-on-macos-no-builder-to-set-up)).

[^cap-pkg-mu]: Packages are built on top of a built-in set of common tools (`git`, `node`, `python`, `go`, `mise`, `ripgrep`, `jq`, `uv`, `gh`, `neovim` and more), which does not include GNU `sed`, `grep`, `find` or `tar`. Mark a Linux-only package `{"name": "strace", "platforms": ["linux"]}` to skip it here.

[^cap-selfinstall]: `npm -g`, `go install`, `uv tool` and `pip --user` land in home folders that are kept with the project. `cargo install` goes to a store that every project shares, and needs Rust from mise first. An installer that writes its own home folder (`~/.bun`, say) fails with `Read-only file system` until you add the folder to `writable_home_dirs`. The built-in `python3` has no `pip`: use `uv`, or install Python with mise.

[^cap-nix]: `nix shell`, `nix build` and `nix eval` work with no extra flags: the jail image turns on `nix-command` and `flakes` in `/etc/nix/nix.conf`, and your own `~/.config/nix/nix.conf` in the jail layers on top. It needs a multi-user nix on the host, the kind that runs a nix daemon; with a single-user nix the jail gets no `nix` at all. The store is read-only and builds go through your host's daemon. `nix-shell -p` does not work, because the jail has no nixpkgs channel, and a garbage collection on the host can delete what you built.

[^cap-nix-mac]: A jail on a Mac is Linux, and your Mac's nix store holds Mac programs, so the jail cannot simply use it. Giving these jails a nix of their own is possible, but not planned for now. Use one of these instead: add the tool to `packages` and restart the jail, install a language runtime with mise, or switch to the `macos-user` backend, where `nix` works inside the sandbox. On `podman` only, there is an expert opt-in: a Podman Machine created with `/nix` shared, whose store holds the jail's Linux builds, can set `YOLO_NIX_HOST_DAEMON=1` plus `YOLO_NIX_HOST_STORE_LINUX=1`. Set the second one wrongly and the jail will not boot. See [Nested Nix builds inside the jail](macos.md#nested-nix-builds-inside-the-jail-advanced).

[^cap-nix-mu]: The sandbox gets the same `nix` that yolo used to build its tools, and every build goes through your Mac's nix daemon, so `nix build` and `nix eval` need no extra flags. It needs a multi-user nix, the kind that runs a daemon. Each launch prints one line saying whether `nix` is available in the sandbox and, if it is not, why. If you set `NIX_CONFIG` yourself, yolo keeps it and adds `extra-experimental-features = nix-command flakes` on a line of its own. If your `NIX_CONFIG` already sets `experimental-features` or `extra-experimental-features`, yolo leaves it exactly as you wrote it. A `NIX_REMOTE` you set replaces yolo's.

[^cap-mac15]: Apple Container on macOS 15 has two network faults that show up only after the jail boots: `npm install` hangs, and the agent's first request times out. `yolo check` detects both and prints the fix. See [Does the jail have outbound internet?](#3-does-the-jail-have-outbound-internet)

A few pointers for the rows above:

- **Choose a backend on a Mac:** set `YOLO_RUNTIME` (or the `runtime` key) to `podman`, `container` or
  `macos-user`. See [A container runtime, started](#a-container-runtime-started).
- **Install an agent:** put `"packs": ["claude"]` in `~/.config/yolo-jail/config.jsonc`, then type
  `claude` in the jail. A project's `yolo-jail.jsonc` cannot set `packs`. See
  [Packs, and the host services they bring](#packs-and-the-host-services-they-bring).
- **Use an API key or another provider:** list a dotenv file in `env_sources`, add the provider's pack,
  and pick it with `yolo -p <profile> -- <agent>`. See
  [Gateway providers and curated models](#gateway-providers-and-curated-models).
- **Push from the jail:** create a key inside with `ssh-keygen` and add it to the repository as a deploy
  key, or put a `GH_TOKEN` in an `env_sources` file.
- **Share more of your machine:** `"mounts": ["~/notes"]` for a read-only folder, and `host_files` in
  your user config for single files. See
  [Settings per setup](../reference/settings-per-setup.md#workspace-mounts-and-host-files).
- **Add a tool:** add it to `packages` in `yolo-jail.jsonc`, run `yolo check`, then restart the jail. See
  [Nix Packages](#nix-packages-image-level).
- **Turn on a host service:** add its pack and `"loopholes": {"<name>": {"enabled": true}}`. See
  [The loopholes](#the-loopholes-host-services-a-jail-can-use).
- **Use a GPU:** `"gpu": {"enabled": true, "vendor": "nvidia"}`. See
  [GPU Passthrough](#gpu-passthrough-nvidia).
- **Pick up a config edit:** `yolo stop`, then `yolo` again. See
  [After you edit your config](#after-you-edit-your-config).

### Before your first launch

Every setup needs the same three things before a jail can start. yolo checks them before it builds or
starts anything; if one is missing, it stops and says what to fix, and leaves nothing behind.

#### An install that includes the build files

yolo builds each jail from a Nix recipe (`flake.nix`, the "flake") and its lockfile, installed next to
the `yolo` binary together with the programs that run inside the jail. It looks for them next to its own
binary, never in your current directory, and every launch prints which copy it used on a `Flake source:`
line.

| How you installed yolo | Works on its own? |
|---|---|
| Homebrew | Yes |
| GitHub release archive | Yes |
| From source, with `just install` | Yes |
| `go install …/cmd/yolo@latest` | **No** — installs the binary only |
| `pipx install yolo-jail` / `uvx yolo-jail` | **No** — installs the binary only |

With `go install` or pipx/uvx, the first launch stops with "Cannot find yolo-jail repo root". Either
switch to a channel marked Yes, or clone the repo and point yolo at the clone with
`export YOLO_REPO_ROOT=/path/to/yolo-jail` (in your shell profile, to keep it). Running `yolo` from
inside the clone is not enough on its own.

#### Nix on the host

yolo uses [Nix](https://nixos.org/download) to build the jail image. On `macos-user`, which has no image,
Nix is instead how `git`, `node` and `mise` get into the sandbox. Check with `command -v nix`;
`yolo check` reports a missing Nix along with the install link. If a build fails, the launch stops and
shows Nix's own error. It never falls back to an older image.

Known issue: Determinate Nix's daemon can hang for non-root users. `yolo check` detects the hang and
names the fix.

#### A container runtime, started

| Setup | When yolo uses it | If it is not running |
|---|---|---|
| `podman` / Linux | Always, on Linux | yolo stops until `podman info` answers |
| `podman` / macOS | When Apple Container is not installed | yolo stops and names `podman machine start` |
| `container` / macOS | Whenever Apple Container is installed | yolo stops and names `container system start` |
| `macos-user` / macOS | Only when you choose it | Nothing to start |

To choose yourself, set the `runtime` config key or the `YOLO_RUNTIME` environment variable to `podman`,
`container` or `macos-user`. On a Mac with neither set, **installing Apple Container changes which
runtime you get**; `command -v container podman` shows what is installed. Choosing `macos-user` on Linux
fails, but partway through the launch rather than up front.

#### The checklist

```sh
command -v nix                 # empty => install nix first (nixos.org/download)
command -v container podman    # on macOS, decides which runtime you get by default
yolo check --no-build          # fast check: build files, nix, runtime, config
yolo check                     # the same, plus an actual image build
```

### After you edit your config

When your config has changed since the last launch — `yolo-jail.jsonc`, `yolo-jail.local.jsonc`, or a
file either one includes — the next launch shows the diff and asks y/N before using it. A workspace's
first launch with a non-empty config counts as a change.

**Scripts, CI, cron jobs and editor tasks cannot answer that question.** With no terminal to ask on,
the launch stops instead, printing the diff and the files involved. Pass `--accept-config-changes` to
approve it for that one launch; it is a flag rather than an environment variable, so an approval never
carries over to a later launch. Launching once in a terminal after each edit avoids the problem. yolo
asks only when its input is a terminal, so `yolo | tee log` still asks and `yolo < /dev/null` stops.

**A running jail does not pick up your edits.** Running `yolo` in a workspace whose jail is already
running joins that jail, and joining does not ask, and does not apply your edit. Resources, mounts and
network settings are fixed when a jail starts, so stop the jail and launch again. `macos-user` has no
running jail to join — every launch starts a fresh sandbox and reads the config again.

### Configuration keys, per setup

What every config key does on each setup (mounts, `host_files`, resources, devices, networking,
`packages`, MCP and LSP servers, profiles and the rest) is in
[Settings per setup](../reference/settings-per-setup.md).

### Common questions

#### 1. Who owns the files the agent writes in my repo?

| Setup | Who owns new files |
|---|---|
| `podman` / Linux, rootless (the usual setup) | You.[^own-rootless] |
| `podman` / Linux, rootful | **`root`**, and nothing warns you. Your editor cannot save them. |
| `podman` / macOS | You.[^vmshare] |
| `container` / macOS | Not yet tested. yolo sets no owner mapping, so Apple Container decides. |
| `macos-user` / macOS | The sandbox account `_yolojail`, but you can read and write them.[^macuserown] |

[^own-rootless]: The agent runs as root inside the container, which on a rootless podman is your own user on the host. Check yours: `podman info --format '{{.Host.Security.Rootless}}'`. A rootful podman makes it the host's root instead, and the launch does not say so.
[^vmshare]: Measured on a Mac: a file the agent writes is owned by your own user. The project must be in a folder the Podman Machine VM shares (your home folder by default); a project outside it fails at launch as a mount that cannot be found.
[^macuserown]: The agent is a real macOS account, so there is no owner mapping. Your access comes from a shared group plus access-control entries inherited from the project folder, so `ls -l` shows `_yolojail` and `git` may print ownership warnings. Two consequences: the project must sit outside every user's home folder (`/Users/Shared/yolo` is set up for you; a project under `/Users/<name>` is refused), and a file *moved* into the folder inherits nothing, so the next launch stops and names `yolo macos-fix-permissions`.

#### 2. Can the agent make a git commit?

Yes, on every setup, if your host has a git identity. yolo copies your host's `user.name` and
`user.email` into the jail at launch. If either is empty on your host, nothing warns you and the agent's
first commit fails with `Please tell me who you are`; on `podman` / macOS the same happens if `git` is not
on the PATH of the shell you launch from.

Check yours before launching: `git config --get user.name && git config --get user.email`. On Apple
Container and `macos-user` the agent can change the copied identity for the session.

#### 3. Does the jail have outbound internet?

Yes, on every setup, and it cannot be turned off. On `macos-user` the agent also reaches your local
network and your Mac's own services.

**Apple Container on macOS 15:** a jail can start fine and then hang on `npm install` or on the agent's
first request. Run `yolo check` once; it finds the problem and prints the command that fixes it. macOS 26
does not have this problem (`sw_vers -productVersion` shows your version).

#### 4. I edited my config and re-ran `yolo`, and nothing changed

Running `yolo` while the jail is already running joins it, and a joined jail keeps the settings it
started with. **After any config edit, run `yolo stop`, then `yolo`.** On podman, a new login key or
`-p` choice, and new skills and briefings, reach you without a restart; on Apple Container nothing does.
On `macos-user` every `yolo` starts fresh, so there is nothing to restart. The full per-setting list is in
[Settings per setup](../reference/settings-per-setup.md#what-a-running-jail-picks-up-when-you-run-yolo-again).

On Apple Container, `yolo stop` cannot see the jail yet: it prints `No jail running for this workspace`
while the jail is running. Find the jail's name with `container ls` and stop it with
`container stop <name>`, including whenever yolo suggests `yolo stop` there.

#### 5. Do I have to log in again in every workspace? And in a second jail at the same time?

It depends on the agent more than on the setup. Agents whose pack keeps its login machine-wide log in once per machine; the rest log in once per workspace.

| Agent | A second workspace on the same machine |
|---|---|
| `claude`, `agy` | Yes: one login per machine, on every setup.[^shared] |
| `codex`, `pi` | Yes: one login per machine, through yolo's OpenAI login service. Not on Apple Container[^login-ac], only partly on `macos-user`[^login-mu], and not yet tried end to end on `podman` / macOS. |
| `copilot`, `opencode`, `omp` | No: a fresh login in every workspace, on every setup.[^perws] |

Two jails at once is a different question, and the answer is about refreshing a login that is about to
expire:

| Setup | Two jails refreshing the same login |
|---|---|
| `podman` / Linux | Yes. A service on your host takes the refreshes one at a time. |
| `podman` / macOS | Should work. |
| `container` / macOS | No. The jail cannot reach that service, and the launch says so.[^refresh-ac] |
| `macos-user` / macOS | No; not planned yet.[^musrefresh] |

Without this, two jails running the same agent can log each other out; log in again when that happens.

One more hazard, on every setup: if the shared credential is **revoked or expired**, a fresh login inside a jail can be **thrown away at your next entry**, and the dead shared credential put back. Logging in again then works only until the next `yolo`. yolo records each time this happens in `~/.yolo-shared-creds.log`; read it if a login keeps not sticking.

[^shared]: The credential lives in a folder shared by every workspace, and each boot links the agent's credential file to it. On `macos-user` the credential is also shared by the whole machine, while history and config are kept per project. Every `macos-user` project uses the same sandbox account, though, so avoid running two of them at the same time. On the first `macos-user` launch only, a real folder where a link belongs makes the launch stop and name the path; removing `/Users/_yolojail` fixes it, but it also removes every login the `macos-user` sandbox keeps.
[^login-ac]: On Apple Container the jail cannot reach yolo's OpenAI login service, because traffic from a container to the Mac does not get through. `codex` prints `OpenAI login is required.` and the browser login fails the same way. The launch says so. This was measured on Apple Container 1.1.0, and a later Apple release may change it; check `container --version`. Not planned yet on yolo's side.
[^perws]: yolo can keep a login machine-wide, but the `copilot`, `opencode` and `omp` packs do not ask for it. Not planned yet.
[^refresh-ac]: Apple Container carries no traffic from a container to the Mac (measured on Apple Container 1.1.0), so the Claude login service cannot be used. Claude still logs in and works; only the coordination between jails is missing.
[^musrefresh]: On `macos-user`, the part of the Claude login service that has to run inside the jail cannot run there, so two sandboxes can still log each other out of Claude. Claude still logs in and works.

### Packs, and the host services they bring

What each kind of pack contribution (`env`, `files`, `mount`, `profile`, services and the rest) does on each setup is in [Settings per setup](../reference/settings-per-setup.md#what-a-pack-can-contribute-per-setup).

#### The loopholes: host services a jail can use

**Selecting the pack is not always enough.** Most loopholes stay off until you turn them on by name, next
to their pack — `"packs": ["serial"]` plus `"loopholes": {"serial": {"enabled": true}}`. The two login
services are the exception: they come on with their pack. A loophole you turn on starts the next time
the jail starts, not when you join a running jail. `yolo loopholes list` shows the ones your config
selects.

| Loophole (its pack) | What it does | On by default | `podman` / Linux | `podman` / macOS | `container` / macOS | `macos-user` / macOS |
|---|---|---|---|---|---|---|
| `claude-oauth-broker` (`claude`) | Lets several jails share one Claude login without logging each other out | Yes | Yes[^lh-rootful] | Should work[^lh-mac] | No[^lh-ac] | No; not planned yet[^musrefresh] |
| `openai-auth-broker` (`openai-auth`, brought in by `claude`, `codex` and `pi`) | One OpenAI subscription login for `codex` and `pi` in every jail | Yes | Yes | Should work[^lh-mac] | No[^lh-ac] | Partly[^login-mu] |
| `aws-auth` (`aws-auth`) | Bedrock with credentials from your host's `aws sso login`, narrowed to a role before they reach the jail | No | Yes[^lh-aws] | Should work[^lh-mac] | No[^lh-ac] | No; not planned yet |
| `serial` (`serial`) | A USB serial device on the host, through an allowlist (`yolo-serial`) | No | Yes | Should work[^lh-mac] | No[^lh-ac] | No: `yolo-serial` is not installed there. Not planned yet. |
| `journal` (`journal`) | The host's systemd journal (`yolo-journalctl`) | No | Yes | No: needs a Linux host[^lh-linux] | No: needs a Linux host[^lh-linux] | No; `macos_log` instead[^maclog] |
| `host-processes` (`host-processes`) | A filtered list of host processes (`yolo-ps`); nothing shows until you list names | No | Yes | No: needs a Linux host[^lh-linux] | No: needs a Linux host[^lh-linux] | No[^muprocs] |
| `audio` (`audio`) | The host's microphone and speakers (PipeWire or PulseAudio) | No | Yes | No: needs a Linux host[^lh-linux][^audiomac] | No: needs a Linux host[^lh-linux][^audiomac] | No[^audiomac] |
| `cgroup-delegate` (`cgroup-delegate`) | Lets the jail cap its own sub-jobs (`yolo-cglimit`) | No | Yes, with cgroup v2[^cgv2] | No: needs a Linux host[^lh-linux] | No: needs a Linux host[^lh-linux] | No |
| Provider helper (`wire-bridge`) | Lets `claude` or `copilot` use the Cerebras and Kilo providers | Comes in automatically | Yes | Yes | Yes | No; planned |

[^lh-rootful]: On rootless podman (the usual setup) yolo asks podman to forward your host's loopback into the jail, and if a service is still unreachable the launch stops with an error rather than continuing without it. On a rootful podman the jail cannot reach yolo's host services at all, and the launch warns; switch to rootless podman, or use `"network": {"mode": "host"}` at the cost of the jail's network isolation. Check yours: `podman info --format '{{.Host.Security.Rootless}}'`.
[^lh-mac]: The jail reaches your Mac's services through the Podman Machine VM at `host.containers.internal`, and that connection is tested nightly. The services themselves have not yet been run end to end on a Mac. If a service cannot be reached, the launch warns but still starts.
[^lh-ac]: Apple Container carries no traffic from a container back to the Mac (measured on Apple Container 1.1.0), so no host service can be used, and the launch lists each one it had to skip. This can change only with an Apple Container release.
[^lh-aws]: Turn it on in your user config with the SSO profile and the role to narrow to, then use the `bedrock` profile: `yolo -p bedrock -- claude`. See [the `aws-auth` pack](../../packs/aws-auth/README.md). Not yet tested against a real AWS SSO login.
[^lh-linux]: These need Linux on the host. On a Mac, turning one on does nothing, and the launch says so in one line naming the loophole. The `audio` pack still sets `PULSE_SERVER`, though (see [^audiomac]).
[^cgv2]: Needs cgroup v2 on the host: `test -e /sys/fs/cgroup/cgroup.controllers && echo v2`.
[^maclog]: `macos-user` offers Apple's unified log instead, behind its own `macos_log` key (`off` / `user` / `full`) and a `yolo-log` helper. It is a convenience, not a boundary: the sandbox can run `/usr/bin/log` directly, so `off` is advisory.
[^muprocs]: The `macos-user` sandbox can already see which processes are running on your Mac, though not their command lines.
[^audiomac]: On any Mac the `audio` pack still sets `PULSE_SERVER`, naming a socket that does not exist, so audio tools may fail or hang instead of using the Mac's own audio. Leave the pack out on a Mac.

### Things that will not work on a Mac

- **NVIDIA or AMD GPUs, and `/dev/kvm`.** Macs have no GPU or virtualization device a Linux jail can use;
  the launch warns if you set `gpu` or `kvm`. Run that work on a Linux machine. On `macos-user`, Metal
  should work as it does for any Mac program.
- **Linux programs on `macos-user`.** It runs Mac programs and Mac tools: `sed -i` needs a suffix,
  `find -printf`, `tar --wildcards` and `grep -P` are missing, and the launch does not warn. Write
  portable scripts, or use a container setup.

---

## Troubleshooting

Start with `yolo check` — it validates your entire setup on both platforms: runtime (podman/container), nix, config, image, running containers, GPU (Linux only), macOS VM backend (macOS only), and the Claude OAuth broker loophole.

```bash
yolo check                    # full check including nix build
yolo check --no-build         # fast — skip nix build
```

### Common Issues (both platforms)

**"Cannot find yolo-jail repo root"** — The CLI needs the source for nix image builds. A packaged install (Homebrew, release archive) ships a flake bundle beside the binary and resolves it automatically, and `just install` stages one for a from-source install. **The working directory is never consulted**, so standing in a checkout is not enough — name it with `YOLO_REPO_ROOT`:

```sh
YOLO_REPO_ROOT=~/code/yolo-jail yolo
```

Every launch prints the flake it resolved and what selected it, before the image build starts:

```
Flake source: /Users/you/.local/share/yolo-jail/flake-bundle (flake bundle staged by `just install`)
```

Set `YOLO_REPO_ROOT` in your shell profile if you always want a live checkout — building from a tree newer than the installed `yolo` is then refused with a message naming `just install`, rather than failing deep inside the boot.

> The old `repo_path` config key was retired (2026-07-23) — if it is still in your `~/.config/yolo-jail/config.jsonc`, `yolo` ignores it and warns; remove it.

**Image build fails**

- Check nix is installed with flakes: `nix --version`
- Ensure flakes are enabled in `~/.config/nix/nix.conf`:
  ```
  experimental-features = nix-command flakes
  ```
- On macOS: no builder is required for the default binary-cache build path, and an uncached build offloads automatically to a container on the running runtime — nothing to verify. (Only if you configured your *own* remote Linux builder as an escape hatch, verify it — `nix store info --store ssh-ng://nix-builder` should respond within a few seconds.)
- Run `yolo check` for detailed diagnostics

**Container won't start**

- Linux: check `podman --version`
- macOS: check that your runtime's VM/daemon is up:
  - Podman Machine: `podman machine list`
  - Apple Container: `container system status`
- Try replacing the jail: `yolo stop`, then launch again
- Check for leftover containers: `yolo ps`

**MCP server not working**

- Verify the preset is enabled in `mcp_presets`
- Check logs (same paths on Linux and macOS): `~/.copilot/logs/` (Copilot), `~/.claude/logs/` (Claude)
- Inside jail, view logs: `tail -100 ~/.copilot/logs/$(ls -1t ~/.copilot/logs | head -1)`

**LSP not responding**

- LSP servers are spawned on-demand, not as background services
- Ensure the language server binary is installed (`mise ls`)
- TypeScript LSP requires `tsconfig.json` or `jsconfig.json` in the workspace root

**Tools missing after restart**

- `eval "$(mise hook-env -s bash)"` to refresh PATH
- Or restart the jail: `yolo stop`, then launch again

**Permission errors on files**


- Linux + Podman: Rootless UID mapping handles ownership automatically
- macOS (any runtime): File ownership is mediated by the VM's virtiofs layer; files inside `/workspace` appear as the jail user and on the host appear as you
- If persistent, check `ls -la ~/.local/share/yolo-jail/home/`

**Claude keeps logging out across jails**

- Full triage walkthrough: [docs/research/claude-token-logouts.md](../research/claude-token-logouts.md). It maps each `yolo doctor` symptom to a fix.
- Background: Anthropic rotates refresh tokens single-use, so multiple jails refreshing simultaneously race each other. The `claude-oauth-broker` loophole serializes refreshes behind an `flock` on the host so jails can't race — eliminating the class entirely. **It is a contribution of the official `claude` pack** (since 2026-08-19) and on by default there, so **selecting `"packs": ["claude"]` is what installs it** — it is no longer keyed on `claude` merely being on your PATH, and there is no bundled channel any more.
- Run `yolo check` and look at the Loopholes section for broker health. Common recoveries: `yolo internal daemon claude-oauth-broker --init-ca` if certs are missing, then restart your jail.

### Linux-Specific Issues

**NVIDIA GPU not visible in jail**

- Check `nvidia-smi` on the host works
- Verify NVIDIA Container Toolkit: `nvidia-ctk --version`
- For Podman, ensure the CDI spec exists: `/etc/cdi/nvidia.yaml`
- See [GPU Passthrough](#gpu-passthrough-nvidia) for the full setup

**Podman rootless permission denied on `/dev/dri` or devices**

- Some device passthrough paths need `--cap-add` which Podman rootless may restrict
- Run Podman rootful (`sudo podman`) for these workloads

### macOS-Specific Issues

**Podman Machine won't start on headless Mac (EC2, CI)**

- Apple's Hypervisor.framework may require a GUI session

- Set `export YOLO_RUNTIME=container` for Apple Container (or drop `YOLO_RUNTIME` — auto-detect will pick it up)

**Nix build hangs or times out**

- Check `nix store info` responds within 2 seconds
- If it hangs, kill determinate-nixd and use the vanilla daemon:
  ```bash
  sudo pkill determinate-nixd
  sudo /nix/var/nix/profiles/default/bin/nix-daemon &
  ```
- If you configured your own remote Linux builder as an escape hatch, verify it: `nix store info --store ssh-ng://nix-builder` (see [docs/guides/macos.md](macos.md)). The default uncached-build path needs no manual builder — it offloads to a container on the running runtime.

**Port forwarding not working**

- Podman on macOS: YOLO Jail uses a TCP gateway (`host.containers.internal`) instead of Unix sockets because virtiofs rejects sockets. This is automatic.
- Apple Container: uses native `--publish-socket` — no TCP gateway needed. ⚠ **Measured 2026-09-16: this path breaks the launch**, because the socket yolo hands it already exists and because AC's direction is host→container. See the `network.forward_host_ports` row in [Settings per setup](../reference/settings-per-setup.md#resources-devices-and-networking).
- Ensure `socat` is in the container (it's in the default image)

**Apple Container: "virtual machine failed to start"**

- VZ.framework caps how many bind mounts a guest can take. YOLO Jail consolidates the workspace state into a single `/home/agent` mount to stay well under it, but many custom `mounts` entries can still reach it. The cap is real and yolo does not know its value: the issue that reported this named a specific number, nothing in the codebase measures or asserts one, so the number is deliberately not repeated here.
- Try `YOLO_RUNTIME=podman` to sidestep the limit.

**Apple Container: image load fails**

- Apple Container requires OCI-format images. YOLO Jail converts via `skopeo` first (no daemon needed), or `podman` as fallback.
- If you don't have `skopeo` installed, install it: `brew install skopeo`.

**`/tmp` bind mounts fail**

- macOS `/tmp` → `/private/tmp` is a symlink. The CLI resolves this automatically when it builds a mount path. (This line named a `cli.py` until 2026-09-16; nothing in yolo is Python any more.)

See [What works in each setup](#what-works-in-each-setup) for the per-setup support reference, and [docs/guides/macos.md](macos.md) for the full macOS-specific setup and the per-backend explanations behind those cells.
