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
- [What works in each setup](#what-works-in-each-setup) — the canonical per-setup support reference
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

Inside the jail, authenticate with your tools once:

```bash
gh auth login          # GitHub CLI
gemini login           # Google Gemini CLI
claude                 # Runs /login on first launch
```

Tokens are stored in `~/.local/share/yolo-jail/home/` on the host (same path on Linux and macOS) and persist across jail restarts. You do **not** need to re-authenticate each time, and on podman a `/login` in any jail propagates to every other jail automatically.

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
yolo -- gemini             # Start Gemini (--yolo auto-injected)
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
| **Pi, opencode** | not configured by YOLO today (both agents can take it — see [`docs/design/claude-lsp-plugins.md`](../design/claude-lsp-plugins.md)) |

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
6. When the container exits, yolo sends `SIGTERM` to each service, waits 5 seconds, then `SIGKILL`.
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
| Auth tokens (gh, gemini, claude) | `~/.local/share/yolo-jail/home/` | All jails |
| Installed tools (npm, go) | `~/.local/share/yolo-jail/home/` | All jails |
| Mise tools & runtimes | `~/.local/share/yolo-jail/mise/` on Linux (bind-mounted at `/mise` inside the jail); podman named volume `yolo-mise-data-v2` on macOS and Apple Container, also mounted at `/mise` | All jails |
| Bash history | `<workspace>/.yolo/home/bash_history` | Per workspace |
| Claude sessions | `<workspace>/.yolo/home/claude-projects/` | Per workspace |
| Copilot sessions | `<workspace>/.yolo/home/copilot-sessions/` | Per workspace |
| Gemini history | `<workspace>/.yolo/home/gemini-history/` | Per workspace |
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

**This section is the canonical answer to "will this work for me".** yolo runs on four
setups — the reachable pairings of a backend with a host OS — and they do not support the same
things. Where a table below is silent about your case, that is a bug in this table; the
[gap tracker](../plans/setup-support-gaps.md) carries the internal version, with citations.

| Column | Backend | Host OS | What it is |
|---|---|---|---|
| `podman` / Linux | `podman` | Linux | Containers on your own kernel. **The parity reference:** everything is wired here first. |
| `podman` / macOS | `podman` | macOS | Containers inside one shared Podman Machine VM. |
| `container` / macOS | `container` | macOS | [Apple Container](https://github.com/apple/container) — one lightweight VM per container. **The macOS default.** |
| `macos-user` / macOS | `macos-user` | macOS | A real macOS process under an Apple Seatbelt sandbox. No container, no VM, and **no bind mounts of any kind** — which is where most of its differences come from. |

Three conventions run through every table:

- **No bare ticks.** A cell reads `works`, `works — <mechanism>`, `works differently`, `absent, warns`,
  `absent, silent`, `refuses`, or `n/a`. Several rows cannot be a tick without lying.
- **A cell marked silent is the one to watch.** The key validates, the launch prints nothing, and the
  behaviour is simply not there. Nothing will tell you. Those cells are bolded throughout.
- **"Takes effect" is a real column.** Most keys are passed on the container command line and are
  therefore frozen when the jail is created. Re-running `yolo` in a workspace whose jail is still
  running *re-enters* it and does not apply an edited config — see
  [I edited my config and re-ran `yolo`](#4-i-edited-my-config-and-re-ran-yolo-and-nothing-changed).

### Before anything else: can you launch at all?

Four things are checked before a jail exists, and three of them can refuse the launch outright. All four apply to every setup — even `macos-user`, which starts no container.

**Terms used below.** A **flake bundle** is the copy of yolo's build inputs (`flake.nix`, its lockfile, and the prebuilt in-jail binaries) that a packaged install ships beside the `yolo` binary; yolo needs one on every launch and **never** consults your working directory to find it. **Re-entry** (or *attach*) is a second `yolo` in a workspace whose jail is already running: it joins the running jail instead of starting one.

#### Does your install channel ship a flake bundle?

| Install channel | Ships a bundle? |
|---|---|
| Homebrew tap | `works` — staged beside the binary |
| GitHub release archive | `works` — staged beside the binary |
| From source, via `just install` | `works` — staged into yolo's own state dir |
| `go install …/cmd/yolo@latest` | `absent, refuses` — no bundle exists to find[^1] |
| `pipx install yolo-jail` / `uvx yolo-jail` | `absent, refuses` — no bundle exists to find[^1] |

#### The four pre-flight gates, per setup

| Gate | `podman` / Linux | `podman` / macOS | `container` / macOS | `macos-user` / macOS | Takes effect |
|---|---|---|---|---|---|
| Flake bundle resolved | `refuses` if none[^1] | `refuses` if none[^1] | `refuses` if none[^1] | `refuses` if none[^1] | fresh launch |
| Host `nix` on PATH | required — builds the jail image[^2] | required — builds the jail image[^2] | required — builds the jail image[^2] | required — realizes the native tool floor[^2] | fresh launch |
| Runtime auto-detected | `works` — the only candidate | `works` — tried after Apple Container[^3] | `works` — the macOS default[^3] | `n/a` — never auto-selected; name it[^3] | any entry |
| Runtime present but not started | `refuses` — `podman info` must answer | `refuses` — names `podman machine start` | `refuses` — names `container system start` | `n/a` — no daemon | any entry |
| Changed config, stdin **is** a terminal | prompts y/N | prompts y/N | prompts y/N | prompts y/N | fresh launch |
| Changed config, stdin is **not** a terminal | `refuses`[^4] | `refuses`[^4] | `refuses`[^4] | `refuses`[^4] | fresh launch |
| Re-entering a running jail | gate skipped, **silent**[^5] | gate skipped, **silent**[^5] | gate skipped, **silent**[^5] | `n/a` — every entry is a fresh launch | — |

[^1]: The failure is a host-side refusal naming the missing repo root — it happens before anything is built or started, so nothing is left behind. Two fixes: reinstall from a channel above that carries a bundle, or clone the repo and point at it with `YOLO_REPO_ROOT=/path/to/checkout` (exported in your shell profile if you always want it). Standing inside a checkout is *not* enough. Every launch prints which flake it resolved and what selected it, on a `Flake source:` line, before the build starts — read that line before believing anything about which code you are running.

[^2]: Learn your own value with `command -v nix`, and get the full report from `yolo check` (`yolo check --no-build` for the fast version), which fails with the install link when nix is missing. Without nix on a container setup the image build cannot run, and a failed build **stops the launch** and prints nix's own error — it does not quietly fall back to an older image. On `macos-user` the refusal names the same download page: the backend has no image, so nix is how `mise`, `node` and `git` get into the sandbox at all. Host-fact caveat: Determinate Nix's daemon has been seen to hang on store operations for non-root users; `yolo check` detects that timeout and names the remedy.

[^3]: The runtime is named by the `runtime` config key or the `YOLO_RUNTIME` environment variable; the legal spellings are `podman`, `container` and `macos-user`, and `docker` is rejected by name. With no key set, Linux uses `podman` and macOS tries Apple Container first, then podman — so on a Mac, **installing Apple Container silently changes which backend you get**. Learn your own value with `command -v container podman`; `container --version` matters separately, because a version yolo cannot read makes it decline read-only bind mounts. `macos-user` is the one backend that is never auto-selected: you must name it. Gotcha on that path: naming `macos-user` on Linux passes validation and refuses only later, after packs have been staged, rather than at pre-flight.

[^4]: **This is the axis that breaks CI jobs, cron entries, editor tasks and wrapper scripts.** Any launch whose *stdin* is not a terminal exits non-zero the first time after any edit to `yolo-jail.jsonc`, `yolo-jail.local.jsonc`, or a file they include; the message prints the diff, names the files (including the local override, which wins), and names the one grant — the `--accept-config-changes` flag. It is deliberately a flag and not an environment variable, so an approval cannot be inherited by a later launch. Two surprises worth knowing: the gate reads **stdin** while the jail's terminal allocation reads **stdout**, so `yolo < /dev/null` at a real terminal refuses while `yolo | tee log` prompts; and a workspace's *first* launch with any non-empty config counts as a change.

[^5]: Re-entry returns before this gate, so an edited config is neither approved nor refused — and it does not take effect either. Everything passed on the container command line (resources, mounts, network) is frozen at the fresh launch. To apply a config edit on a container setup, stop the jail and launch again.

One more thing about non-interactive launches, and it is a gap we have not built yet rather than a limitation to design around: when stdin is not a terminal, yolo installs no signal handlers, so a scripted launch that is interrupted or killed leaves residue behind — port forwarders, service endpoint files, no timing report — and says nothing. A later launch recovers the container half; the rest is not yet cleaned up.

#### The checklist

```sh
command -v nix                 # empty => install nix first (nixos.org/download)
command -v container podman    # on macOS, decides which backend you get by default
yolo check --no-build          # fast pre-flight: flake source, nix, runtime, config
yolo check                     # same, plus an actual image build
```

Then launch once **interactively** after any config edit, so the y/N prompt is available; scripted launches afterwards will not need `--accept-config-changes`.

### Configuration keys, per setup

Three blocks, grouped the way the config file is.

#### Workspace, mounts, and host files

This block is what the jail can see of your machine's filesystem, and what it may write. Read the table with two things in mind.

- **Takes effect** is when an edit to the key reaches a jail. Almost everything here is passed on the container command line, so it is frozen at the **fresh** launch: re-entering a running jail returns before the config-change check, and an edited value does nothing until you stop the jail and launch again. `macos-user` has no re-entry at all — every invocation builds a new sandbox — so on that column every row behaves as `any entry`.
- **Bolded cells are silent.** The key passes `yolo check`, the launch prints nothing, and the behaviour is simply not there.

| Row | `podman`/Linux | `podman`/macOS | `container`/macOS | `macos-user`/macOS | Takes effect |
|---|---|---|---|---|---|
| `mounts` — host dirs read-only at `/ctx` | works | works — VM must share the source[^vm] | works on 1.1.0+; older: `absent, warns`[^acro] | **absent, silent**[^mumounts] | fresh launch |
| `workspace_readonly` — lock workspace sub-paths | works — read-only overlay per path | works | works on 1.1.0+; older: **paths stay writable**, warns[^acro] | works — sandbox policy rule, not a mount | fresh launch |
| … and the `yolo-jail.jsonc` lock it also performs | works | works | works on 1.1.0+; below, lost **silently**[^acro] | **absent, silent** — config stays agent-writable[^mujsonc] | fresh launch |
| `per_side_paths` — `.venv`/`node_modules` not shared | works — private dir mounted over each path[^leftover] | works | works — unverified on Apple silicon[^achw] | `absent, warns` — host and sandbox share them[^mupsp] | fresh launch |
| `writable_home_dirs` — extra writable `$HOME` paths | works — read-write bind per path | works | works — the whole home is writable already | works — sandbox home is writable; dir not pre-created | fresh launch |
| `ephemeral_storage` — `volume` vs `tmpfs` scratch | works | works — volumes sit on the VM's disk | **always RAM-backed, silent**[^aceph] | `n/a` — the machine's real `/tmp`[^mueph] | fresh launch |
| `cache_relocations` — move a jail cache to other storage | works — a provisioning failure refuses the launch | expected to work, never measured[^vm] | `absent, warns` — not built[^cachegap] | `absent, warns` — not built[^cachegap] | fresh launch |
| `host_files` modes `readonly` / `once` / `copy` | works | works | works | works[^dac] | fresh launch |
| `host_files` mode `capture` — keep your local edits | works | works | works | works — needs a workspace ACL[^acl] | fresh launch |
| `host_files` source is a **file** | works — read-only bind | works[^vm] | works — copied in, **agent-writable**[^achostlayer] | works — root-owned copy at launch[^snap] | fresh launch |
| `host_files` source is a **directory** | works | works[^vm] | works, but `:ro` unchecked below 1.1.0, **silent**[^acdir] | `absent, warns` — not built yet[^mudir] | fresh launch |
| `host_files` destination: writable, and private per workspace | works | works | works — whole home is per-workspace | works under `~/.config`; home-root files **shared, silent**[^mutier] | fresh launch |
| `host_files` entries with a `source:` come from your user config only | works | works | works | works | n/a — a repo's config cannot name host bytes |
| `host_management`, `host_wrappers`, `host_apply_on_launch`, `promotion_target` | works | works | works | works | any entry — host-side keys[^hostside][^wrappath] |
| Host-side verbs refuse when run **inside** the jail | works | works | works | **does not refuse, silent**[^muinjail] | any entry |
| `programs: { autoprune: true }` | works[^prunetime] | works[^prunetime] | works[^prunetime] | **absent, silent** — never prunes, never reports | fresh launch (switch)[^prunetime] |
| `yolo programs ls` / `remove` from inside the jail | works | works | works | **wrong answer, exits 0**[^muprograms] | any entry |

[^acro]: Apple Container honours read-only binds from version 1.1.0 (measured on macOS 26.5, Apple silicon). Check yours: `container --version`. Below that floor yolo refuses to bind a `mounts` entry rather than binding it writable, and prints one skip line per entry. `workspace_readonly` is the exception: those paths sit inside the writable workspace and cannot be skipped, so they arrive writable behind a loud warning that names every declared entry — but never the `yolo-jail.jsonc` lock. All of these lines are printed while the launch builds the container, so re-entering a running jail never shows them.

[^vm]: `podman` on macOS runs a Linux VM, and a bind source must be a path that VM shares — `$HOME` and `/private` by default. List yours: `podman machine inspect --format '{{range .Mounts}}{{.Source}} {{end}}'`; add one with `podman machine init -v <path>`. yolo does not probe this set, so a source outside it passes the host-side existence check and then fails inside the VM — an empty directory, or `statfs …: no such file or directory` at container start. For `cache_relocations` a target under `$HOME` is expected to work and one on `/Volumes` is expected to fail the launch outright; nobody has confirmed either on hardware.

[^mumounts]: Nothing reads `mounts` on `macos-user`: no `/ctx` tree, no warning, no briefing line. Copying an arbitrary host tree in is not planned, but the refusal that would *tell* you is unbuilt work rather than a limitation of the backend.

[^mujsonc]: On the container backends, setting `workspace_readonly` at all also locks the workspace's own `yolo-jail.jsonc` against the agent — worth knowing before you protect an unrelated path. `macos-user` emits no such rule, so the jail's config file stays agent-writable there; an agent's edit shows up as a y/N diff at the next launch instead of being blocked.

[^leftover]: A "shadow" is a private directory mounted over `.venv`, `node_modules` and your declared paths so host and jail do not share build output. The mountpoint is created inside your live workspace, so an empty root-owned `.venv`/`node_modules` can be left behind after the jail exits.

[^achw]: Expected to work by construction — the same nested-bind shape Apple Container already uses for the cache — but never run on Apple silicon. Each entry also consumes one directory-sharing slot against an undocumented per-container limit.

[^mupsp]: There is no mount namespace here, so one path cannot show different contents inside and outside; the launch warns and the paths are shared. The warning names only *your* entries — the always-on `.venv` and `node_modules` are shared with no line at all. Pointing the sandbox at its own venv and module prefix would cover that default set, and is unbuilt.

[^aceph]: Apple Container always gets RAM-backed scratch for `/tmp`, `/var/tmp` and the container directories, whatever the key says, and nothing is printed. A build that writes large temp files can hit the memory ceiling this key exists to avoid. `YOLO_RUNTIME=podman` if you need disk-backed scratch.

[^mueph]: No container and no read-only root filesystem, so the sandbox writes the machine's real `/tmp` and `/var/folders` with no configuration. Only the RAM-backed (`tmpfs`) *choice* is unavailable, and asking for it is ignored without a word.

[^cachegap]: One yellow line per launch names the cache subdirectories that stayed put and points at `YOLO_RUNTIME=podman`. Neither cell is a backend limitation: Apple Container already nests a writable bind at exactly the depth a relocation needs, and on `macos-user` the policy that denies the target is one yolo generates itself. Both are unbuilt work. On the container backends the line is printed at launch assembly, so a re-entry never shows it.

[^dac]: These modes set file permission bits, not enforcement. In a container the agent is root, so `readonly`'s `0444` is a speed bump; on `macos-user` the sandbox runs as a separate account, so the bits actually bite.

[^acl]: `capture` is the one mode that writes into your workspace (it stores your local edits beside the composed file), so on `macos-user` the workspace needs the inherited `group:_yolojail allow` entry. Check with `ls -lde <workspace>`; repair with `yolo macos-fix-permissions`. Without it the launch dies as `mkdir …/.yolo/prism: permission denied`, naming neither ACLs nor the fix.

[^achostlayer]: Apple Container cannot bind a single file, so the host bytes are copied into the jail's home. That home is bound writable, so the agent can rewrite the host layer its own composed file is built from — where `podman` keeps that layer read-only. Nothing warns.

[^snap]: A root-owned copy taken at launch, not a live view. Every consumer reads its config at boot, so this is equivalent in practice. Symlinking the real file was measured and rejected: the sandbox runs as a different account, and a macOS home need not be readable by it.

[^acdir]: Directory sources are the one read-only bind that does not consult the Apple Container version. Below 1.1.0 your host directory is mounted **writable** while the config says read-only, and nothing says so.

[^mudir]: One yellow line names each undelivered destination and points at `runtime: "container"`. This is unbuilt work, not a limit of the backend: the same per-file copy that already delivers single-file sources needs to walk the tree, optionally bounded by size so the warning survives for genuinely huge trees.

[^mutier]: Composed files under `~/.config/…` land in a per-workspace directory and are fine. A destination at the home root (`~/.npmrc`, `~/.netrc`) lands in the sandbox account home that *every* workspace on the machine shares, so one workspace's launch overwrites another's — or, under `once`, finds the other's file already there and never seeds its own. Nothing warns.

[^hostside]: These four are host-CLI keys with no reader in any jail, so the backend is not the axis. Two behaviours to know: an unreadable or unparseable user config resolves `host_management` to `none` (yolo writes nothing) rather than to the default `assert`, so a malformed config looks like yolo going quiet; and `promotion_target` accepts only `local` or `pack:<name>`, silently falling back to `local` for anything else.

[^wrappath]: `host_wrappers` puts small scripts ahead of the real agent binaries on your PATH, and `host_apply_on_launch` only ever fires through one of them. The macOS half of that placement is not handled: the shell-init line is appended to your shell rc, which runs *after* macOS's own `path_helper` for interactive shells — so a terminal session is fine while a GUI- or IDE-launched agent silently gets the real binary. Unmeasured on a Mac.

[^muinjail]: Nothing marks a `macos-user` session as being inside a jail, so `yolo host apply` and `yolo config promote` do not refuse there: they act on the shared sandbox account home while every message says "your real home", and `yolo config ls`/`diff` describe the host rather than the session you are in. Unbuilt: one launch variable, plus a sweep of everything that reads it.

[^prunetime]: A time-axis oddity worth knowing: the *switch* is frozen at the fresh launch, but the pruning itself re-runs on every re-entry — so turning autoprune off does not stop a running jail from pruning until you relaunch. Read from your user config only (a repo must not delete your binaries), and off unless explicitly on.

[^muprograms]: `yolo programs ls` inside a `macos-user` session prints "No staged packs here — run `yolo programs` in the jail" and exits 0. You *are* in the jail; the session just does not carry the variable that says so. Unbuilt, and the same omission as the previous note.

#### Resources, devices, and networking

Two things to read before the table:

> [!WARNING]
> **"Unconfigured" is not "uncapped" on Apple Container.** With no `resources` block, the `container` backend still gets a memory and CPU cap — **yolo's own arithmetic**, roughly half your RAM and half your cores. The startup banner does not mention it (it prints only what you wrote); the agent's briefing does. On both podman backends, unset really does mean unlimited.

> [!IMPORTANT]
> **Every key in this table is frozen at the fresh launch.** All of them ride the container command line, so re-entering an already-running jail returns before the config-change prompt and before the command line is rebuilt. An edited value does nothing, silently — and worse, the agent's briefing *is* re-rendered from your edited file, so the agent is told a cap or a port that is not in force. `yolo config drift` is the detector; nothing points you at it. Exit the jail and relaunch.

| Key | `podman` / Linux | `podman` / macOS | `container` / macOS | `macos-user` / macOS | Takes effect |
|---|---|---|---|---|---|
| `resources.memory` | works — kernel-enforced [^cgroups] | works — enforced in the VM, clipped to it [^vm] | works — **but a cap you never wrote** [^acdefault] | absent, warns — not built yet [^mures] | fresh launch [^muentry] |
| `resources.cpus` | works — kernel-enforced [^cgroups] | works — clipped to the VM's vCPUs, silently [^vm] | works — half your cores unless set [^acdefault] | absent, warns — not built yet [^mures] | fresh launch [^muentry] |
| `resources.pids_limit` | works — always capped, default applied | works — always capped | absent, **silent** — no surface mentions it [^acpids] | absent, warns — not built yet [^mures] | fresh launch [^muentry] |
| `devices` (raw path, `usb:`, `cgroup_rule`) | works — all three forms [^lsusb] | absent, warns — refused by OS, never probed [^macdev] | absent, warns [^acdev] | absent, warns — devices already reachable [^mudev] | fresh launch [^muentry] |
| `gpu` — `vendor: nvidia` | works — CDI device + driver env [^nvidia] | absent, warns [^gpumac] | absent, warns [^gpumac] | absent, warns — GPU already reachable [^mugpu] | fresh launch [^muentry] |
| `gpu` — `vendor: amd` | works — `mode: devices`; `mode: cdi` can fail hard [^amdcdi] | absent, warns [^gpumac] | absent, warns [^gpumac] | absent, warns [^mugpu] | fresh launch [^muentry] |
| `kvm` | works — needs host group membership [^kvmhost] | absent, warns — not built yet [^kvmmac] | absent, warns [^acdev] | absent, warns — no Linux kernel to ask | fresh launch [^muentry] |
| `network.mode: "bridge"` (default) | works — own network namespace | works — namespace inside the VM [^vm] | works — own network per container | absent, **silent** — no isolation at all [^mubridge] | fresh launch |
| `network.mode: "host"` | works — and drops both port keys [^hostdrop] | **applies to the VM, not the Mac — silent** [^hostmac] | absent, warns — runs bridged; port keys still work [^achost] | works — the only mode it has | fresh launch |
| `network.ports` (HOST:JAIL) | works — passed to podman [^dnat] | works for a `0.0.0.0` listener — **measured** [^vm] [^dnat] | **accepted and inert — measured** [^acports] | absent, warns — every bound port is already open [^muports] | fresh launch |
| `network.forward_host_ports` (JAIL:HOST) | works — needs `socat` on the host [^socat] | works — TCP to the VM gateway, unmeasured [^vm] | **breaks the launch — measured** [^acfwd] | same-port entries already true; a remap is not built [^mufwd] | fresh launch |

Terms: **rootless podman** runs as your own user with no root daemon; **Podman Machine** is the Linux VM podman uses on macOS; **CDI** is the Container Device Interface, a host-side YAML/JSON file describing a GPU; a **remap** is a port entry whose two numbers differ (`5432:3306`).

The accepted-everywhere-and-read-nowhere census is now EMPTY. `required_capabilities` was its last member until 2026-09-17, when it got the fatal refusal its design always specified — a launch declaring a capability nothing satisfies is refused on every backend, with `YOLO_ALLOW_UNMET_CAPABILITIES=1` as the loud way past. `prune` left the census on 2026-09-16, when it got the validator it never had (it now refuses a non-object, an unknown sub-key and a threshold the reader would ignore) and the `config-ref` section whose absence hid the gap; `prune.warn_threshold_gb` is still its one reader (`yolo check`'s disk warning), which is why the census is one key wide.

The `--network` CLI flag does **not** override the config: a `network.mode` in `yolo-jail.jsonc` wins over `yolo --network …`, with no warning. And the validator's warning that the port keys are "ignored when `network.mode` is `'host'`" — the one network message that does fire on any entry — is **false on `container`** (which runs bridged and keeps the ports) and misleading on `macos-user` (both keys are ignored whatever the mode).

**Can the jail reach a service on your host's own `127.0.0.1`, with no port key?** On `podman` / Linux, yes — but only on the default bridge mode and only on a **rootless** podman whose network stack yolo could identify; it asks for host-loopback forwarding explicitly. A rootful podman gets no forwarding and **no warning**. On `macos-user` the answer is trivially yes: the sandboxed process is on the Mac's own loopback. On `container` the answer is **no** — measured on Apple Container 1.1.0, a container→host connection completes its handshake and then carries nothing; the launch discloses this for yolo's own services but says nothing about yours. `forward_host_ports` is **not** the way around it: that key breaks the launch here [^acfwd]. On **`podman` / macOS the answer is yes, measured** (2026-09-16, podman 6.0.2 on `applehv`): a Mac service on `127.0.0.1` answered from inside a default-bridge jail with data crossing both ways, at `host.containers.internal` — which resolves to `192.168.127.254` there, not the `169.254.1.2` of a Linux pasta host. Podman Machine's gvproxy forwards it, so yolo does not have to ask, and no `--map-host-loopback` is involved. What is still wrong there is what yolo *says*: it excludes macOS from the decision by name and tells the jail `unknown`, so the reachability witness never escalates on the one macOS column where the hop works — a total loophole outage would still be silent.

[^cgroups]: Needs cgroup v2 with delegation. On a **rootless** podman on a cgroup-v1 host, podman ignores memory/CPU/pids limits and yolo does not pre-flight it: the flag is emitted and does nothing. Check yours: `podman info --format '{{.Host.CgroupVersion}} rootless={{.Host.Security.Rootless}}'`.
[^vm]: On macOS, podman runs inside a Linux VM, so a limit is enforced against the VM and a network namespace is created inside it. Asking for more memory or more CPUs than the machine has is accepted and silently clipped — for memory the only signal is a hint printed *after* an out-of-memory kill; for CPUs there is none. Check the machine's size with `podman machine inspect`, and resize with `podman machine set --memory … --cpus …`.
[^acdefault]: When the key is unset, yolo probes host RAM and core count and emits half of each (memory floored at 4 GB, CPUs at 2). **A fractional `resources.cpus` breaks the launch here, measured 2026-09-16:** yolo's validator permits any positive number, and `container run --cpus 1.5` dies as `Error: The value '1.5' is invalid for '--cpus <cpus>'` followed by a usage block — a backend's argument parser shown to a user for a value yolo called valid. Integers are fine (`--cpus 2` runs). The same config works on podman.
[^mures]: `macos-user` runs the agent as an ordinary sandboxed process, so there is no cgroup to write; the launch prints one yellow line naming every `resources` key you set, and the agent briefing deliberately claims nothing. A runaway build can take the whole Mac down. This is **not built rather than impossible**: a sampled host-side watchdog on the session's process tree could contain and report a breach for memory and process count, and `GOMAXPROCS`/`-j`-style concurrency limits would honor the `cpus` number cooperatively. None of that exists today.
[^muentry]: `macos-user` is the one setup with no re-entry: every invocation starts a fresh sandbox, so it re-reads config every time. The frozen-at-launch rule applies to the three container setups.
[^acpids]: No process cap is passed and no banner, briefing, or warning mentions it — a fork bomb in the jail is unbounded. Also **not built rather than impossible**, and the mechanism is now measured: the container's cgroup filesystem IS writable on `container` 1.1.0 (`cgroup.controllers` reads `cpuset cpu io memory hugetlb pids`, and `+pids` into `cgroup.subtree_control` succeeds), so the jail's own boot can write the cap the CLI cannot pass.
[^lsusb]: `usb: "vendor:product"` needs `lsusb` on the host PATH or it degrades to a warning, and a replugged device changes its bus path — a stale raw path becomes a skipped line, never an error. Whether `cgroup_rule` is actually honored under cgroup v2 is unverified.
[^macdev]: The refusal is by host OS, before any check of *where* the device lives — yet yolo itself passes a VM-internal device path on the same launch, so a path that exists inside the Linux VM (a tun device, a loop device) is refused for a reason that does not apply. USB additionally needs macOS 15 or later (`sw_vers -productVersion`) plus Podman Machine support.
[^acdev]: Apple Container passes no devices at all — **verified against hardware** 2026-09-16: `container run --help` on 1.1.0 has no `--device`, no CDI flag, and no device selector of any kind.
[^mudev]: The sandboxed process reaches devices under ordinary macOS file permissions, so there is nothing to pass through. What is missing is the opposite — *restricting* devices, which the sandbox profile could express per declared path and does not today.
[^nvidia]: Needs the NVIDIA Container Toolkit, `runc` as the runtime (CDI fails under crun), and a CDI spec whose driver version matches the host driver. ⚠ The launch probe accepts only a `.yaml` spec: a host whose spec is `nvidia.json` is silently downgraded to no GPU. Check with `nvidia-smi -L` and `ls /etc/cdi /var/run/cdi`. A failed probe always warns and starts without the GPU, never refuses.
[^gpumac]: Apple silicon has no NVIDIA or ROCm hardware and neither macOS VM does PCIe passthrough, so CUDA/ROCm in a Linux guest is out. But the cell is **narrower than "impossible"**: a podman machine on the libkrun provider exposes a virtual GPU to the container, which gives Vulkan *compute* translated to Metal — a worse mechanism for the same goal ("the jail gets GPU compute"). yolo cannot express it: config accepts only the `nvidia` and `amd` vendors, and device passthrough is refused by host OS before any path is examined.
[^amdcdi]: With `mode: devices` the raw GPU device nodes are passed and the pre-flight probes for them. With `mode: cdi` the pre-flight does **not** check for an AMD CDI spec, so on a host with the driver but no spec the launch dies on a raw runtime error instead of yolo's usual warn-and-start-without-GPU. Check with `ls /etc/cdi/amd.json /var/run/cdi/amd.json`.
[^kvmhost]: Needs CPU virtualization extensions, the `kvm` module, and (rootless) your user in the `kvm` group — `yolo check` covers all three.
[^kvmmac]: The warning fires by host OS, and the device check it skips would have looked on the Mac rather than inside the Linux VM where the device would live. Reaching it needs Apple's nested virtualization (macOS 15+, M3 or later) plus Podman Machine support, and then a probe of the VM rather than the Mac.
[^mubridge]: A written `"mode": "bridge"` is not read: the sandboxed process shares the launcher's network stack and every port it binds is on the Mac's real interfaces. The agent's briefing says so; the human who wrote the key is told nothing. A warning on an explicit key is the small missing piece — the isolation itself the sandbox cannot provide.
[^hostdrop]: By design: under host networking yolo stops requesting host-loopback forwarding and drops **both** port keys, because there is nothing left to map. It also tells the jail that the namespace is shared, which makes an unreachable yolo service refuse the launch rather than warn.
[^hostmac]: The flag is applied — to the VM's network namespace. Two silent consequences: the agent is told "localhost resolves directly to the host", which is false (it is the VM's loopback); and the jail is told the launcher's namespace is shared, which **escalates** — with a loopback service enabled, the launch can be refused for a boundary that was never crossed.
[^achost]: Apple Container accepts no network selector, so the key cannot be honored as written; the warning names the key and the consequence. It was thought to withhold nothing because the port keys still worked here — **measured 2026-09-16, neither does** [^acports] [^acfwd]. The *goal* remains reachable and unbuilt, and the raw material is the one transport that does cross this boundary: a published UNIX socket, which carries data both ways in the host→container direction.
[^dnat]: A jail service bound to `127.0.0.1` (rather than `0.0.0.0`) is *meant* to stay publishable: yolo installs an address translation at boot for each published port. **On podman/macOS it does not work, and that is now measured** — with the rule installed, `route_localnet=1` and the listener confirmed, the Mac's dial to the published port connects and receives nothing, while an identical `0.0.0.0` listener answers. So **bind `0.0.0.0` in the jail**, and the two documentation surfaces that say a `127.0.0.1` listener is not publishable (including the agent briefing) are RIGHT rather than stale. The mechanism is that rootless port forwarding hands the connection over inside the network namespace, which never traverses the `PREROUTING` chain the fixup writes — so expect the same on **rootless Linux**, where it remains unmeasured. Your deciding host fact: `podman info --format '{{.Host.Security.Rootless}} {{.Host.RootlessNetworkCmd}}'`. On `container` none of this machinery is emitted at all, and published ports do not work there for any bind address [^acports].
[^socat]: `socat` must be installed **on the host**; absent, you get one warning and no forwarding. This is the one network key with an end-to-end test, and that test runs only on Linux.
[^acver]: Apple Container's version is the whole axis for this backend — `container --version`. One documentation claim about the mechanism ("no socat") is wrong: `socat` runs on both sides. The port keys were untested here until 2026-09-16; both are now measured, and both are worse than the corpus believed [^acports] [^acfwd].
[^acports]: **Measured on `container` 1.1.0 / macOS 25.5:** the mapping is recorded — `container inspect` shows `{"hostAddress": "0.0.0.0", "hostPort": …}` — and carries nothing. Dialling the Mac's `127.0.0.1:<hostPort>` connects and returns no data (the jail-side listener sees the connection arrive and reset), while dialling the container's own vmnet IP on the container port works normally. This is the same shape as the container→host outage: on this backend host↔container TCP establishes in both directions and carries data in neither. Nothing warns; `-p` is emitted as if it worked.
[^acfwd]: **Measured on `container` 1.1.0 — this one fails the launch.** yolo starts host-side `socat` *before* creating the container, and Apple Container then refuses the flag naming that socket: `Error: host socket <path> already exists and may be in use`. Even with no `socat` installed, the direction is inverted — AC's `--publish-socket host_path:container_path` creates the host socket and forwards a *host* connection inward to a container-side listener, which is the opposite of what this key needs. A published unix socket is nonetheless the one transport measured to cross this boundary at all, so it is the raw material for a fix rather than a dead end.
[^muports]: There is nothing to publish and nothing to confine: a port the sandboxed agent binds is on the Mac's real interfaces whether you list it or not. A launch prints one line per declared key saying so. A **remap** (differing numbers) is named as undeliverable — and that half is unbuilt rather than impossible: a small host-side relay would deliver it, at the cost of leaving the inner port exposed too.
[^mufwd]: A same-port entry (`5432:5432`) is already satisfied — the sandbox is on the Mac's stack. A remap (`5432:3306`) is warned and not delivered; a loopback relay in the launcher would deliver it, which is why this is a gap rather than a limit.

#### Packages, tools, and agent configuration

**Nothing is active by default.** An empty config gives you a jail with *no coding agent* — a coding agent arrives only because a pack installs one, and the launch says so when `packs` is empty. Your effective pack set is also larger than what you typed: a pack may pull in others through its own dependencies, so the launch prints the resolved set rather than your list.

**Config scope matters before anything else.** Keys marked † below are read from your **user** config only (`~/.config/yolo-jail/config.jsonc` and its includes). A workspace `yolo-jail.jsonc` cannot set them at all — spelling one there is a fatal pre-flight error that names the file to move it to. Every other key here is settable at either scope, workspace winning.

| Key / capability | `podman` / Linux | `podman` / macOS | `container` / macOS | `macos-user` / macOS | Takes effect |
|---|---|---|---|---|---|
| `packages` (nix packages on PATH) | works — baked into the image | works — needs a Linux builder[^builder] | works — needs a Linux builder[^builder] | works — native darwin build | fresh launch |
| `packages` per-entry `platforms` | works | works — but `"darwin"` means *absent*[^plat] | works — same inversion[^plat] | works — this is what it is for | fresh launch |
| `packages` entry that cannot build | refuses — nix error at launch | refuses — blames the builder[^builder] | refuses — blames the builder[^builder] | refuses — names your real target | fresh launch |
| One image per machine (`YOLO_STORE_PACKAGES=1`) | works — packages from the host store | absent, warns — not built yet[^storedel] | absent, warns — not built yet[^storedel] | n/a — **silent** if you set it | fresh launch |
| `mise_tools` (runtime pins) | works — tool store on the host | works — store inside the VM[^vm] | works — store inside the VM[^vm] | works — store per workspace | fresh launch |
| `mcp_presets` | works | works | works | absent, warns — broken entry still written[^presetmac] | fresh launch |
| `mcp_servers` | works | works | works | works — `command` must exist on your Mac | fresh launch |
| `mcp_servers.requires_env` | works | works | works | **silently** drops gated servers[^reqenv] | fresh launch |
| `lsp_servers` | works — known names only[^lsp] | works — known names only[^lsp] | works — known names only[^lsp] | works — last removal leaves it installed | fresh launch |
| `security.blocked_tools` | works — blocker shims on PATH | works | works | works — but blocks the Mac's BSD tool[^bsd] | fresh launch |
| `packs` † | works | works — workspace must be VM-shared[^vm] | works — staged copy, never re-read | works — some surfaces inert, said aloud[^packsmac] | fresh launch[^packspartial] |
| `providers` | works | works | works — lost on re-entry[^reentry] | works | any entry[^reentry] |
| `profiles` † | works | works | works — lost on re-entry[^reentry] | half-arrives, **silent**[^profmac] | any entry[^reentry] |
| `use_profiles` † / `-p <name>` | works — refuses a `-p` it cannot honor | works | **silently** ineffective on re-entry[^reentry] | selection arrives, body does not[^profmac] | any entry[^reentry] |
| `agent_updates` † | works | works | works | works | fresh launch |
| `env_sources` (dotenv files) | works | works | **silently** lost on re-entry[^reentry] | works — per-session, not editable in-jail | any entry[^reentry] |
| `nix build` usable inside the jail | works — host daemon, store read-only[^gcroot] | absent, warns — not wired yet[^nixmac] | absent, **silent** — not wired yet | absent — no `nix` on the sandbox PATH | fresh launch |
| GNU behaviour of `sed`/`find`/`grep`/`tar` | works — GNU userland baked | works | works | BSD tools — GNU flags fail[^bsd] | fresh launch |
| Build toolchain (`cc`, `make`, `strace`) | works — baked | works | works | works only with Xcode CLT[^clt] | fresh launch |
| Browser for the chrome-devtools MCP | works — chromium baked | works | unmeasured[^acbrowser] | absent — no browser wired | fresh launch |

Two things cut across the whole table. First, **`macos-user` has no re-entry**: every `yolo` invocation is a fresh sandbox, so every row above takes effect on your next command there, with no restart — the "fresh launch" answers cost you nothing on that column. Second, the container backends freeze most of this on the container command line at the **fresh** launch; re-entering a running jail returns before the config-change gate, so an edit you just made is not in the session you just joined. `yolo check` before you restart; a restart is what delivers.

**One gap to know about, not a limitation:** `providers` is *not* user-scope-contained the way `packs`, `profiles`, `use_profiles` and `agent_updates` are. A repo-committed, agent-editable workspace config can rewrite a provider's base URL or endpoints — including one your own user-scope profile selects — and nothing warns. Treat a workspace config you did not write as able to redirect the agent's API traffic. Closing this is unbuilt work, not a permanent property.

[^builder]: A `packages:` entry forces a **Linux** image build, so a Mac needs a working Linux builder; without one the launch dies inside nix and the message reads like a builder problem rather than a package problem. Check yours with `nix config show | grep -E 'builders|extra-platforms'`.

[^plat]: On a Mac running a *container* backend the jail is Linux, so `{"platforms": ["darwin"]}` means the package is **not** in your jail and `{"platforms": ["linux"]}` means it is. Nothing at launch names the entries it dropped.

[^storedel]: Delivering `packages:` from the host nix store — one image per machine instead of one per distinct package list — is a Linux-podman-only fast path today. On macOS the dial prints an "ignored" line and the launch bakes as normal, which costs a rebuild per distinct list. Not a ruled-out design; simply not built for these setups yet.

[^vm]: The podman machine has one fixed share set, chosen at `podman machine init`, and Apple Container has its own VM. Consequences: the mise tool store lives inside the VM (invisible from the Mac's filesystem, and lost to a `podman machine reset`), and a workspace or yolo state directory outside your home may fail to bind at all. See yours with `podman machine inspect --format '{{.Mounts}}'`.

[^presetmac]: On `macos-user` a yellow launch line tells you presets are not delivered — but the preset's entry is still written into every agent's MCP config, so it fails at first use on a path yolo chose not to create.

[^reqenv]: A server gated on `requires_env` is removed from every agent's config on `macos-user`, even when the variable *will* be in the agent's environment.

[^lsp]: Only server names yolo has an install recipe for put anything on disk. A name outside that set appears in the agent's config and installs nothing, on every setup. `yolo config-ref` lists the recognized names.

[^bsd]: `macos-user` runs against your Mac's own userland, so `sed -i` eats the next argument, `find -printf` and `tar --wildcards` are unknown, and `grep -P` and `ls --color` error out — scripts that pass on the container backends fail here. `security.blocked_tools` also measures the *sandbox* PATH, so a block replaces the BSD tool. Homebrew's GNU builds (`brew --prefix coreutils`) are not on the sandbox PATH under their plain names.

[^packsmac]: On `macos-user` a pack's skills and briefing are writable by the agent and are overwritten on the next launch, and pack-shipped MCP presets are not delivered. A pack-shipped loophole is **half** delivered, which is the precise version of what this note used to call "inert": its HOST daemon starts and its endpoint is delivered (with a per-file ACL grant for the sandbox account), while its `jail_daemon` — the in-jail half — runs for nothing at all, because this backend has no in-jail supervisor. So an *intercepting* loophole does nothing useful even with its daemon up: `claude-oauth-broker`'s TLS terminator is its jail half. The launch says all of it rather than failing quietly: one disclosure line per host daemon it starts, and one `Declined:` line per jail daemon it will not.

[^packspartial]: On podman, adding a pack and *re-entering* a running jail is the worst of both: the pack's config surfaces and hooks render, while its skills, briefing, files and host-file grants do not, and its loopholes never start. Half-arrived, with nothing said. Restart for a whole pack.

[^reentry]: Apple Container receives the provider/profile/`env_sources` channel as a **copied** file, made only on a fresh launch. Re-entering a running jail prints the delivery line, exits 0, and runs the previous launch's providers, profiles and dotenv values. On podman the same channel is a live file, so it does reach the next entry.

[^gcroot]: An in-jail `nix build`'s result gets no durable garbage-collection root, so a host `nix-collect-garbage` can delete a store path a running jail is executing from, with no warning in either place.

[^nixmac]: Reaching the host nix daemon from a jail on a Mac needs a store the jail can see holding *Linux* paths, and (on podman) a machine initialised with `-v /nix:/nix` — which cannot be added to an existing machine. Check with `command -v nix` and `podman machine inspect | grep -i /nix`. Do not force the store-view dial on: it replaces the view the jail's own binaries live in and the jail will not boot.

[^acbrowser]: Whether headless chromium starts and serves a debugging connection under Apple Container has not been measured. The likely failure is an MCP call that times out rather than a launch error. Report `container --version` and `sw_vers -productVersion` with any result.

[^clt]: Without Xcode Command Line Tools there is no `cc` or `make` in the `macos-user` sandbox and yolo does not say so; with them installed, the Mac's own toolchain is what you get. Check with `xcode-select -p`. Linux-only tools such as `strace` are absent either way.

### The questions users actually ask

Five questions, answered per setup. Column names are the (backend, host OS) pair: `podman`/Linux, `podman`/macOS, `container`/macOS (Apple Container), `macos-user`/macOS (native Seatbelt sandbox, no VM).

#### 1. Who owns the files the agent writes in my repo?

| Setup | Owner of new files |
|---|---|
| `podman`/Linux | `works — you`, on a rootless podman[^rootless] |
| `podman`/Linux, rootful | **`root:root`, silent** — your editor cannot save |
| `podman`/macOS | `unmeasured` — the Podman Machine VM's file share decides[^vmshare] |
| `container`/macOS | `unmeasured` — yolo emits no id mapping; Apple Container decides |
| `macos-user`/macOS | `works differently` — owned by `_yolojail`, readable/writable by you[^macuserown] |

[^rootless]: The jail runs as container-uid 0, which on a rootless podman maps to your own uid. Learn your value: `podman info --format '{{.Host.Security.Rootless}}'`. Rootful podman maps it to host uid 0 instead, and nothing in the launch says so. (Inside a nested jail there is no mapping at all — `--userns=host` is forced.)
[^vmshare]: Nothing in yolo decides this, and no test covers it. Expect either your own uid or an unfamiliar numeric owner. Separately: a workspace outside your home directory may not be shared into the VM at all, which fails as an unresolvable mount rather than as odd ownership.
[^macuserown]: There is no id mapping and there cannot be — the agent is a real macOS account. Your access comes from a shared group plus inheriting ACLs on the workspace root, so `ls -l` shows `_yolojail` and `git` may print ownership warnings. Two consequences: the workspace must sit outside every user's home directory (put it under `/Users/Shared`; a workspace under `/Users/<name>` is refused), and a file *moved* into the tree inherits nothing — the next launch refuses and names `yolo macos-fix-permissions`.

#### 2. Can the agent make a git commit?

Yes on every setup — **provided your host has a git identity**. yolo reads `user.name` and `user.email` from the host at launch and delivers them into the jail.

| Setup | Host identity present | Host identity missing or empty |
|---|---|---|
| `podman`/Linux | `works` — whole config composed, read-only | **absent, silent** — first commit dies[^noident] |
| `podman`/macOS | `works` — same, if `git` is on the launching shell's PATH | **absent, silent**, and indistinguishable from "no `git` on this Mac" |
| `container`/macOS | `works differently` — delivered as a **writable** copy[^acwritable] | **absent, silent** |
| `macos-user`/macOS | `works differently` — replayed as `git config --global`[^macuseradd] | **absent, silent**; visible only under `--dry-run`[^dryrun] |

Check yours before launching: `git config --get user.name && git config --get user.email`. An empty answer from either is the failure above. Setting only a name is the worst case — the identity file is composed and delivered, so the launch looks fully provisioned and the commit still fails.

[^noident]: With neither key set, yolo delivers no identity and prints nothing; `yolo check` has no git-identity section either. The agent hits git's own `Author identity unknown / *** Please tell me who you are.` mid-task. It can self-heal by setting the keys inside the jail (the config file is writable there), but only for that workspace, and only if it works out why. An unbuilt gap, not a design decision — the information is in hand at launch time.
[^acwritable]: Apple Container cannot nest a single-file mount inside its one home mount, so the identity file is written into the jail's home instead of mounted read-only. It is therefore agent-writable here where every other backend gets read-only: an agent can silently rewrite its own commit author for the session, and nothing detects or discloses that.
[^macuseradd]: This backend replays the two values as `git config --global` in the shared account home instead of composing a file. The replay only *sets* non-empty values, so an identity you later change is overwritten but one you *clear* is never removed — the stale email persists. Your global gitignore (`core.excludesFile`) does not cross here at all.
[^dryrun]: The line that would tell you (`git identity: (none — commits use no identity)`) is only printed by `yolo run --dry-run`, never by a real launch.

#### 3. Does the jail have outbound internet?

Yes on all four. There is no config option to take it away — `network.mode` admits only `bridge` (the default) and `host`.

| Setup | Egress | How |
|---|---|---|
| `podman`/Linux | `works` | podman's default bridge and its NAT |
| `podman`/macOS | `works` — inferred, untested | the Podman Machine VM's network helper |
| `container`/macOS | `works` — measured on macOS 26 | each container gets its own vmnet namespace |
| `container`/macOS **15** | **broken, silent at launch** | two vmnet faults; see below |
| `macos-user`/macOS | `works` — and wider | the Mac's own network stack, loopback and LAN included[^wider] |

**The macOS 15 Apple Container case.** On macOS 15 (Darwin 24.x) Apple Container's networking has two distinct faults, and a jail hits them *after* booting perfectly: `npm install` hangs, the agent's first API request times out. The launch is completely silent. Both faults are already detected — by `yolo check`, which is the only thing that runs the probe, so **run `yolo check` on macOS 15 before believing a jail is healthy**. It distinguishes them and prints the remedy for yours:

- *Subnet disagreement* — the gateway handed to containers is on no host interface, so the jail cannot even reach its own gateway (`ping` of the gateway fails). Remedy: `container system stop && container system start`, or pin `[network] subnet` in `~/.config/container/config.toml`.
- *Missing NAT* — addressing is fine and the gateway answers, but nothing forwards past it. Remedy: `sudo sysctl -w net.inet.ip.forwarding=1` plus a pf NAT rule; `yolo check` prints the rule derived from your own default route.

Learn your macOS major version with `sw_vers -productVersion`; macOS 26 fixed this, and the probe returns immediately on any other version. Wiring the same probe into the launch as a warning is an unbuilt gap — the detection exists, only the call site on the launch path is missing.

[^wider]: Nothing in the sandbox profile denies any network operation, so the agent reaches your Mac's loopback and your LAN directly — which is also why a loophole's host daemon needs no forwarding hop here, and why its endpoint is a plain file path with an ACL rather than a mount. What is bypassed rather than emulated is the *jail* side: an interception that a container gets from `--add-host` plus a trusted CA has no analogue on a native process, and the daemon that would terminate it is declined by name at launch.

#### 4. I edited my config and re-ran `yolo`, and nothing changed

This is the **time axis**, and it is the single most confusing thing in the product. Re-running `yolo` in a workspace whose jail is still running does **not** start a new jail — it *re-enters* the one you have (the banner says `Attaching to existing jail`). Everything that was passed on the container command line is frozen at the launch that created the jail. Content is not: skills, briefings and pack surfaces are recomposed and the whole boot re-runs on every entry, which is exactly why the confusion is so strong — you *watch* the boot regenerate everything and still get the old value.

| What you changed | `podman` re-entry (Linux, macOS) | `container` re-entry | `macos-user` |
|---|---|---|---|
| Skills (built-in, pack, your own) | `works` | `works` | `works differently`[^macuserre] |
| Briefings (`AGENTS.md`, `CLAUDE.md`), `agents_md_extra`, a filed handoff | `works` | **absent, silent** | `works differently`[^macuserre] |
| Adding or dropping a pack | `partly`[^packpartly] | **absent, silent**[^acpack] | `works` |
| Pack launch flags (`--yolo`, `--dangerously-skip-permissions`) | `works` | `works` | `works` |
| `-p <profile>` / a rotated key in `env_sources` | `works` — live env file | **absent, silent** — and prints as if delivered | `works differently` |
| `mcp_servers`, `lsp_servers`, `mise_tools`, `blocked_tools` | **absent, silent**[^envhalf] | **absent, silent** | `works` |
| `resources`, `network`, `ports`, `mounts`, `devices`, `gpu`, `packages`, `host_files` | **absent, silent** | **absent, silent** | `works`[^macuserkeys] |
| The config-change diff prompt (your y/N review) | **absent, silent** | **absent, silent** | `works` — every invocation shows the diff |
| The frozen config snapshot `yolo config drift` compares against | **absent, silent**[^drift] | **absent, silent** | `n/a` — never written, drift says "cannot determine" |
| The jail's own `yolo` version | `absent, warns` — one dim line naming `yolo stop` | `absent, warns` | `n/a` — re-staged every launch |

**`macos-user` has no time axis at all**: it has no attach, every invocation is a fresh sandbox rebuilt from your live config, so every key it reads takes effect on the next `yolo`. Its gaps are on a different axis — which keys it reads at all.

**To get a fresh jail:** `yolo stop`, then `yolo -- <cmd>`. ⚠ On Apple Container `yolo stop` prints `No jail running for this workspace` and exits 0 **while the jail is running** — a wrong liveness probe. Until that is fixed, get the jail's name from `yolo ps` and stop it with the `container` CLI directly. Every message that prescribes `yolo stop` as the remedy is unactionable on that backend.

None of the "frozen" rows above is a permanent limitation. The `-p` row proves a per-entry channel already works on podman — a live env file the jail re-reads on every entry — and the env-carried settings (MCP, LSP, mise tools, blocked tools) need nothing from container creation. They are unbuilt, not impossible.

[^macuserre]: Recomposed on every invocation and copied over the sandbox home, so it works — but the copy is **writable**, meaning the agent can edit its own skills and briefing and the next launch silently overwrites them. The agent is told this in its own briefing.
[^packpartly]: Two different clocks. A newly added pack's config surfaces, skills, briefing, hooks and launchers re-render on a re-entry; its *mounts* and its loophole do not. Because the jail home is read-only, a pack whose writer has no writable destination is likely to make the re-entry fail outright rather than degrade. Dropping a pack is the clean direction.
[^acpack]: Apple Container receives the pack tree as a copy rather than a live mount (deliberately — so an agent cannot rewrite the manifests the host reads next launch), and nothing re-copies on re-entry. A live jail keeps the packs it booted with: not the surfaces, not the skills, not the hooks.
[^envhalf]: These cross as environment variables on the container command line. An added MCP server does not appear in the agent's list until you stop and relaunch, even though the boot visibly regenerated the MCP config.
[^drift]: After a re-entry, in-jail config readers still see the config the jail was *launched* with, and `yolo config drift` compares against a baseline that may be several edits old — so it reports drift for edits you thought you had applied.
[^macuserkeys]: For the keys this backend reads. Resource limits are not enforced here and `/ctx` mounts bind nothing — those are capability gaps, not time-axis ones.

#### 5. Do I have to log in again in every workspace? And in a second jail at the same time?

It depends on the agent, not mostly on the setup. Agents whose pack asks for the *machine* credential tier log in once per machine; the rest log in per workspace.

| Agent | A second workspace on the same machine |
|---|---|
| `claude`, `agy` | `works` — one login per machine, every setup[^shared] |
| `codex`, `pi` | `works` — one login per machine, via a host-wide credential broker; **except Apple Container**[^acbroker] |
| `copilot`, `omp` | **fresh login in every workspace, silent** — every setup[^perws] |

Two jails at once is a different question, and the answer is about credential *refresh*:

| Setup | Concurrent refresh |
|---|---|
| `podman`/Linux | `works` — a host-wide broker serializes refreshes |
| `podman`/macOS | `unmeasured` — the broker starts; whether the jail can reach it across the VM is untested, and a broken hop warns rather than refuses |
| `container`/macOS | **not serialized, warns** — the loophole is inert here[^acrefresh] |
| `macos-user`/macOS | **not serialized, says so** — one shared account home, one credential file, and the serializer's *jail* half cannot run here[^musrefresh] |

Where refreshes are not serialized, two jails running the same agent share one credential file and a simultaneous refresh can consume the single-use refresh token and log you out of both. The launch tells you the mechanism is inert; it does not tell you that this is what being inert costs.

One more hazard worth knowing, on every setup: if the shared credential is **revoked or expired**, a fresh login inside a jail is written locally and then **discarded at your next entry**, relinking to the dead shared credential. Logging in again works until the next `yolo`. It is disclosed — on the entrypoint's stderr, which is usually discarded, and durably in `~/.yolo-shared-creds.log`, so read that file if a login keeps not sticking. A freshness rule here is unbuilt, not ruled out.

[^shared]: The credential lives in a machine-scoped directory, and a per-boot hook links the tool's credential file into it. On `macos-user` the same outcome arrives without any mount, because there is one shared account home — the agent is told to expect history that is not its own. First `macos-user` launch only: a real directory where a link belongs makes the launch refuse and name the path; removing `/Users/_yolojail` is the migration.
[^musrefresh]: The broker's host daemon really does start on `macos-user` — measured 2026-09-18, a bare `["claude"]` publishes its endpoint file — but serializing a refresh needs the jail-side terminator that intercepts the agent's call, and that process is a `jail_daemon`: nothing runs one on this backend, and the launch declines it by name. A host daemon with no client is not a serializer.
[^acbroker]: On Apple Container the broker process starts but the jail cannot reach it — container→host traffic is measured dead on this backend (the handshake completes and nothing crosses). `codex` prints `OpenAI login is required.` and the interactive login fails through the same dead hop. **The launch says so** as of 2026-09-18 — one line naming the pack, the loophole and that measured reason; before then this service was the one pack the inert report was withheld for, so nothing named Apple Container as the cause. This measurement is against Apple Container 1.1.0 and is expected to expire with an upstream release — check yours with `container --version` and re-test before assuming it still holds.
[^perws]: These packs simply never asked for the machine tier. The mechanism that would fix it is fully built and shipping for other agents; nothing warns.

### Packs, and the host services they bring

A **pack** is a bundle of contributions yolo installs into a jail — config files, skills, an agent's launcher, a briefing paragraph, sometimes a host service. Selecting one is a `packs` entry in your config. A **loophole** is the one contribution that is not content but a deliberate hole in the jail wall: a service running on your real machine that the jail is allowed to talk to.

Two words used throughout: a **fresh launch** starts a new container (or a new sandboxed session); a **re-entry** attaches to the jail you already have running. Everything on the container command line is frozen at the fresh launch.

#### What a pack can contribute, per setup

Every contribution kind is delivered on all four setups — config files and their overlays, autonomy postures, hooks, blocked-tool shims, skills trees, workspace and machine-scope state dirs, read-only host-file grants, providers, `requires` assertions, briefings, and `program` launchers[^capture] — **except these**:

| Contribution | podman/Linux | podman/macOS | container/macOS | macos-user/macOS |
|---|---|---|---|---|
| `env` — static vars | works | works | works, **silent on re-entry**[^acfreeze] | works — carried in the launch env |
| `files` — a tree in the agent's home | works | works | works — writable copy | **absent, silent**[^files] |
| `mount` — host dir read-only at `/ctx/<into>` | works | works | works — needs Apple Container 1.1.0+[^acver] | **absent** — and the banner says otherwise[^mountmu] |
| `service` — an in-jail daemon (the wire bridge) | works | works | works | **absent, silent** — breaks a shipped default[^svc] |
| `profile` — a named `-p` selection | works | works | works, **silent on re-entry**[^acfreeze] | partly — the vars land, the config surfaces do not[^profmu] |
| `loophole` — a host service | works | starts, reachability unmeasured[^machop] | one only[^theone] | one only, by a different route[^theone] |

On **podman** and **macos-user**, content contributions are re-rendered on any entry, so a host-side edit reaches a jail you re-enter. On **Apple Container** they are a snapshot of the last fresh launch: an edited pack, a changed setting, or a fresh `.yolo/handover.md` is composed, announced as delivered, and does not arrive until you restart the jail.[^acfreeze]

#### The loopholes, and the wire-bridge service

**Selecting the pack is not enough.** Five of the seven shipped loopholes are off until you enable them by name — `"loopholes": { "serial": { "enabled": true } }` — and only the two credential brokers come on with their pack. Enabling one and re-entering a running jail starts nothing and says nothing: every row here is fresh-launch-only, on every setup.

| Loophole (its pack) | Default | podman/Linux | podman/macOS | container/macOS | macos-user/macOS |
|---|---|---|---|---|---|
| `audio` (`audio`) | off | works[^rootless] | **absent, silent** | absent, warns | absent, warns — and the env still points at it[^audiomu] |
| `cgroup-delegate` (`cgroup-delegate`) | off | works — needs cgroup v2[^cgv2] | **absent, silent** | absent, warns | n/a, warns — no cgroups to delegate |
| `host-processes` (`host-processes`) | off | works[^rootless] | starts, **returns nothing**[^bsdps] | absent, warns | `ps` works natively — **unfiltered**[^posture] |
| `journal` (`journal`) | off | works[^rootless] | **absent, silent** | absent, warns | absent, warns — `yolo-log` instead[^maclog] |
| `serial` (`serial`) | off | works[^rootless] | starts on real hardware; hop unmeasured[^machop] | absent, warns | `/dev` open directly — **unfiltered**[^posture] |
| `claude-oauth-broker` (`claude`) | **on** | works[^rootless] | starts; hop unmeasured[^machop] | absent, warns | absent, warns — refreshes unserialized[^unserialized] |
| `openai-auth-broker` (`openai-auth`) | **on** | works[^rootless] | starts; hop unmeasured[^machop] | starts, **jail cannot reach it**[^acbroker] | works — different route, refuses the launch if it fails[^mubroker] |
| wire bridge (`wire-bridge`, a `service`) | joined automatically | works | works | works | **absent, silent**[^svc] |

Two corrections to a belief this table exists to kill. It is widely said that **no** loophole runs on Apple Container or macos-user; exactly one does, on both — the OpenAI credential broker. On macos-user it genuinely works, by a mechanism of its own. On Apple Container it starts, nothing warns, and the jail still cannot dial it, so an OpenAI login fails at first use.[^acbroker] And on macos-user, three loopholes look inert while the capability is *wider* than the loophole would allow: the sandbox reaches every process and every serial device on the machine, with the loophole's allowlists gating nothing.[^posture]

[^capture]: `program` launchers (a name on PATH plus a lazy installer that keeps the tool current) are delivered on all four. What the macOS backends lack is the install-capture store that pre-seeds those installs, so a first use downloads the vendor installer instead — slower, same result.
[^acfreeze]: Apple Container renders pack surfaces from a per-launch copy of the pack tree rather than a live read-only bind, and the copy is only refreshed by a fresh launch. Consequence for `env` and `profile`: `yolo -p <name> -- <agent>` against a running jail prints the selection it made, and the jail keeps the previous one. Restart the jail after any config or pack edit on this backend.
[^files]: `packs: ["pi"]` on macos-user silently omits the extension file the pack ships into the agent's home, so the OpenAI broker runs and the code that dials it never arrives. A gap not yet built, not a limit of the backend.
[^acver]: Read your own value with `container --version`. From 1.1.0 read-only binds are honored and this works; below it, or if the version cannot be read, the mount is skipped with a yellow line and `/ctx/<into>` does not exist. Closing the below-floor case is unbuilt work, not a permanent limit.
[^mountmu]: On macos-user the tree is not delivered at all, and the launch's host-access banner still discloses the read as if it were — the one place here where the launch is actively misleading. Unbuilt, and known to be buildable.
[^svc]: The wire bridge is an in-jail daemon that some providers route through; the `cerebras` pack pulls it in automatically when `claude` or `copilot` is selected. On macos-user nothing supervises it and nothing warns, so a default `["claude", "cerebras"]` composition points the agent at a local address with no listener. Also note on every setup: the daemon set is fixed at the fresh launch, so a newly selected service pack needs a restart.
[^profmu]: On macos-user the provider and profile variables reach the sandbox environment, but the resolved profile table does not reach the config-rendering half, so surfaces that depend on the selected profile render without it.
[^machop]: Whether a jail on macOS podman can reach a host service across the Podman Machine VM is **not measured anywhere**, and yolo cannot ask the VM to forward its loopback. So these services start, are disclosed as running, and a launch is *not* refused if they turn out to be unreachable — you meet it as `yolo-ps`, `yolo-journalctl` or a login failing at runtime. If you have a Mac with podman machine, the value worth reporting is `podman info --format '{{.Host.RootlessNetworkCmd}}'` plus whether a host loopback listener answers from inside the jail.
[^theone]: See the second table: on Apple Container and macos-user only the OpenAI credential broker is started; every other loophole prints one yellow inert line per launch naming the backend and the reason.
[^rootless]: On **rootless** podman with the default bridge network, reachability depends on yolo forwarding the host's loopback into the jail; when yolo asked for it and the service is still unreachable, the launch is **refused** rather than degraded. Read your own values with `podman info --format '{{.Host.Security.Rootless}}'` and `podman info --format '{{.Host.RootlessNetworkCmd}}'`.
[^cgv2]: Needs cgroup v2 on the host: `test -e /sys/fs/cgroup/cgroup.controllers && echo v2`.
[^audiomu]: The loophole is reported inert, but the `audio` pack's environment variables still cross, naming sockets macOS does not have — so audio-aware tools fail or hang instead of falling through to CoreAudio. The environment here is worse than absent.
[^bsdps]: The host daemon starts, the launch looks healthy, and every request fails: the daemon issues Linux `ps` arguments that macOS `ps` does not accept. `yolo-ps` returns empty output or a usage error, indistinguishable from an empty allowlist.
[^posture]: This is a posture inversion, not a bonus. The loopholes exist to show *nothing* until you list what may be seen; on macos-user the sandbox runs the host's own tools under a permissive profile, so the agent sees every process on the machine (including other users' command lines) and can open every serial device, and no config key narrows either.
[^maclog]: macos-user offers Apple's unified log instead, behind its own `macos_log` key (`off` / `user` / `full`) and a `yolo-log` helper. It is a convenience, not a boundary: the sandbox can run `/usr/bin/log` directly, so `off` is advisory.
[^unserialized]: Claude auth still works on macos-user — the sandbox reaches the API directly — but refreshes are not serialized. Two concurrent sessions, or one macos-user session beside a container jail, can race the single-use refresh token; the symptom is a logged-out Claude with nothing pointing at concurrency.
[^acbroker]: The daemon and its in-jail adapter both run, the reachability check warns without refusing, and the agent's briefing omits the one loophole this backend does start. Either the allowlist entry or the silence needs to go; until then, treat Codex/pi login on Apple Container as unverified.
[^mubroker]: On macos-user the broker is started by hand and the launch is **refused** if it does not come up, so a broken broker takes the backend down rather than failing quietly. Credentials are handed over by a per-file macOS access-control grant to the sandbox account, which is built and unit-tested but has never been executed end to end outside a real Mac. One live defect rides along: the agent's briefing still tells it no host services are running here.

### Permanent limits, and what to do instead

Short list, and it is short on purpose: these are the only things in the audit that **cannot** be
delivered on a setup, each because of a fact about the host rather than a decision yolo made. Everything
else that looks like a limit is a gap nobody has built yet — see the end of this section.

Two GPU/virtualization keys are absent on every macOS setup:

| Want | `podman` / Linux | `podman` / macOS | `container` / macOS | `macos-user` |
|---|---|---|---|---|
| `gpu` (`vendor: nvidia` or `amd`) | works — device passthrough | absent, warns | absent, warns | absent, warns [^gpu] |
| `kvm` (hardware virtualization in the jail) | works — `/dev/kvm` passed in | absent, warns | absent, warns | absent, warns |

- **`gpu`** — no NVIDIA driver has existed for macOS since 10.14, Apple silicon carries neither an
  NVIDIA nor an AMD discrete GPU, ROCm is Linux-only, and neither Mac VM passes a PCIe device through
  to its Linux guest, so there is no bus on
  which such a card could appear. **Instead:** run GPU work on a Linux host. On `macos-user` the key is not read at all, and the sandbox profile denies no
  GPU access — the agent is a native process, so Metal (Apple's own GPU API) is reachable the way it is
  for any program you run yourself. [^gpu]
- **`kvm`** — `/dev/kvm` is a Linux kernel interface. A Mac has none, the Mac VMs do not nest
  virtualization, and `macos-user` starts no Linux kernel at all. **Instead:** expect nested VMs to run
  emulated (slow but correct), or use `podman` / Linux.

Both keys are read when the jail is created, or on `macos-user` by nothing at all. Re-entering an
existing jail cannot change them, so "restart and it works" is never the answer here.

**`macos-user` runs Mach-O binaries, not ELF ones**, and that removes a whole family of jail plumbing
rather than breaking it. macOS has no ELF loader, so the FHS interpreter shim (`nix-ld`), the
shared-object symlink farm under `/lib`, and the boot-populated loader cache have no problem to solve —
a Mach-O binary's loader is `dyld`, always present. Nothing warns, and nothing should: **n/a**, not
missing. The same goes for anything image-shaped, because this setup builds no container image — the
store-delivered package farm and its PATH slot, and the lean image's bulk extras, simply do not arise.
Building a library to compile against still works: a `packages: ["foo.dev"]` entry yields a working
`pkg-config --cflags foo`.

**`iptables` is the one loss inside that family.** The rule that makes a published port reach a service
bound to the jail's own loopback is Linux netfilter, and there is no container network here to rewrite.
**Instead:** read the networking section — on `macos-user` a port the agent binds is already open on the
Mac's real interfaces, so the fix-up has nothing to fix and nothing to confine.

One standing decision, not an impossibility: **`macos-user` ships no GNU userland.** `sed -i` wants a
suffix argument, `find -printf` and `tar --wildcards` are unknown, `grep -P` is unsupported — the
proposition is *your Mac, confined*, so the agent gets the same tools the human has, and nothing at
launch predicts the difference. **Instead:** write portable scripts, or use a container backend for a
job that assumes GNU behavior.

Anything else you expected in this list — a resource cap on `macos-user`, a port remap there, host
directories delivered by `host_files`, store-delivered packages on a Mac, nested-jail port publishing —
is a **gap, not a limit**: a mechanism exists, nobody has built it. Those are tracked in
[the gap tracker](../plans/setup-support-gaps.md); the per-setup behaviour you get today is in the tables above this
section.

[^gpu]: Host facts worth checking before you believe any of this about your own machine. Which setup you
are on: `yolo check` prints the backend and host OS. What GPU the Mac actually has:
`system_profiler SPDisplaysDataType` — on Apple silicon this lists an Apple GPU and nothing else.
Whether you are on Apple silicon: `uname -m` (`arm64`). Routes to GPU compute that are *not* NVIDIA —
Vulkan compute translated to Metal by a `podman machine` provider that exposes a virtual GPU, or an
external Thunderbolt card fed to a Linux guest — are neither built nor ruled out; they are backlog
items, and no `gpu.vendor` value names them today.

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
- Check logs (same paths on Linux and macOS): `~/.copilot/logs/` (Copilot), `~/.cache/gemini-cli/logs/` (Gemini), `~/.claude/logs/` (Claude)
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
- Apple Container: uses native `--publish-socket` — no TCP gateway needed. ⚠ **Measured 2026-09-16: this path breaks the launch**, because the socket yolo hands it already exists and because AC's direction is host→container. See [^acfwd].
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
