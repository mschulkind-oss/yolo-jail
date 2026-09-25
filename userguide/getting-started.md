# Getting Started

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

The container image is a Linux image, but most of its content downloads directly from the NixOS binary cache. A few derivations are built from yolo-jail's own source and aren't cached, so a from-source Linux build step is sometimes needed on macOS — **unless you download the prebuilt image from yolo-jail's cache** (the intended happy path; nothing to build at all). When a from-source build is needed, yolo offloads it automatically to a tiny throwaway container on whichever container runtime is already up (Podman or Apple Container) and tears it down afterward — no VM, no `sudo`, no setup. The only prerequisite is that the runtime is running. (Advanced escape hatch: if you already run your own remote Linux builder — nix-darwin `linux-builder` or a Linux box in `/etc/nix/machines` — Nix will use it.) See [Building the image on macOS](guides/macos.md#building-the-image-on-macos-cache-vs-linux-builder) for details.

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
3. **Install tools** — MCP servers and utilities are installed into persistent storage (`~/.local/share/yolo-jail/home/`). Language servers are not: you bring those yourself (see [LSP Servers](guides/mcp-and-lsp.md#lsp-servers)).
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
- `codex` and `pi` share one OpenAI login through a login service yolo runs on your host. This does not work on every setup; see [Do I have to log in again in every workspace?](reference/settings-per-setup.md#5-do-i-have-to-log-in-again-in-every-workspace-and-in-a-second-jail-at-the-same-time).
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

## Before your first launch

Every setup needs the same three things before a jail can start. yolo checks them before it builds or
starts anything; if one is missing, it stops and says what to fix, and leaves nothing behind.

### An install that includes the build files

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

### Nix on the host

yolo uses [Nix](https://nixos.org/download) to build the jail image. On `macos-user`, which has no image,
Nix is instead how `git`, `node` and `mise` get into the sandbox. Check with `command -v nix`;
`yolo check` reports a missing Nix along with the install link. If a build fails, the launch stops and
shows Nix's own error. It never falls back to an older image.

Known issue: Determinate Nix's daemon can hang for non-root users. `yolo check` detects the hang and
names the fix.

### A container runtime, started

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

### The checklist

```sh
command -v nix                 # empty => install nix first (nixos.org/download)
command -v container podman    # on macOS, decides which runtime you get by default
yolo check --no-build          # fast check: build files, nix, runtime, config
yolo check                     # the same, plus an actual image build
```
