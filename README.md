# YOLO Jail

[![CI](https://github.com/mschulkind-oss/yolo-jail/actions/workflows/ci.yml/badge.svg)](https://github.com/mschulkind-oss/yolo-jail/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

**Describe your agentic development environment once — agents, config, skills, tools and credentials — and run it anywhere from a sealed jail to your own shell.**

yolo composes everything an AI coding agent works with: which agents are installed (Claude Code, Codex, Copilot, opencode, pi, Antigravity), their settings and house rules, skills, MCP and language servers, packages, and the credentials and host services they may reach. You describe it once, declaratively and per workspace, and yolo renders that description wherever the agent runs. Runs on **Linux and macOS** (Apple silicon, and Intel for now).

## Why?

Setting up an AI coding agent well means installing it, configuring it, writing its house rules,
wiring its MCP and language servers, giving it skills and tools, and handing it the logins it
needs, then doing it all again for the next agent, the next project and the next machine. yolo turns
that into one declaration you can version, share as a **pack**, and apply anywhere.

How much the agent is confined is one setting of that declaration:

- **A jail**, the default: an isolated Linux container, with Podman on Linux or Apple Container or
  Podman on a Mac. Your project is mounted read-write at `/workspace`, and your SSH keys, git
  credentials and cloud tokens are not there, so agents run without permission prompts.
- **Your own machine**: `yolo host apply` writes the same settings, skills and house rules into your
  real home, and `yolo host -- <agent>` runs an agent there with its keys and profile.
- **`macos-user`**, in development: the agent runs as a hidden macOS user inside Apple's sandbox,
  with no VM.

The [user guide](https://docs.yolo-jail.mschulkind.dev) ([source](userguide/README.md)) explains
how it works, and its [feature index](userguide/features.md) lists everything yolo can set up.

## Prerequisites

Three things on the host, whichever way you install:

- **[Nix](userguide/getting-started.md#step-1-install-nix)**, a reproducible package builder, which
  builds the jail image. On a Mac, the Nix daemon must also trust your user, so that it can download
  yolo's prebuilt image pieces and build anything uncached in a temporary container.
- **A container runtime**: [Apple Container](https://github.com/apple/container) on a Mac with Apple
  silicon and macOS 26 or later (the Mac default), or [Podman](https://podman.io/) — rootless on
  Linux, or with its Podman Machine VM on any Mac.
- **yolo** itself, below.

Platforms:

- **Linux, x86_64 and arm64** — rootless Podman. Both are CI-tested, from one Nix image definition.
- **macOS, Apple silicon** — Apple Container (recommended) or Podman Machine, running a native
  **arm64** Linux container with no emulation. The `macos-user` sandbox, which needs no container
  runtime, is coming. See the [macOS guide](userguide/guides/macos.md).
- **macOS, Intel** — Podman Machine only, and ending: Apple Container, Podman 6, Homebrew's
  prebuilt packages and the NixOS Nix installer have dropped Intel Macs, and the Nix packages
  yolo uses there stop receiving fixes at the end of 2026. Install yolo there from a
  [release archive](https://github.com/mschulkind-oss/yolo-jail/releases) rather than Homebrew.

## Install

```bash
brew install mschulkind-oss/tap/yolo-jail
```

[Homebrew](https://brew.sh/) works on macOS and Linux and installs yolo only, so install Nix and a
runtime first. **[Getting Started](userguide/getting-started.md) has the copy-paste steps** for each
Mac and Linux setup: the Nix installer and the one setting it needs, each runtime and how to check it,
the other ways to install yolo, and the first launch.

yolo builds each jail from its *flake bundle* — [`flake.nix`](flake.nix), its lockfile and the
prebuilt in-jail binaries — which it finds beside its own binary, never in your working directory.
Homebrew, a [release archive](https://github.com/mschulkind-oss/yolo-jail/releases) and the
from-source install ship one. `go install` and `pipx install yolo-jail` (or
`uvx --from yolo-jail yolo`) ship the binary alone, so they need a checkout named by
`YOLO_REPO_ROOT`: [details](userguide/getting-started.md#other-ways-to-install).

### From source

For hacking on yolo-jail itself, or running an unreleased working tree. Identical on Linux and macOS.
You need `git`, Go and [just](https://github.com/casey/just), or [mise](https://mise.jdx.dev) to
install the pinned Go and just ([without them installed](userguide/getting-started.md#from-source)):

```bash
git clone https://github.com/mschulkind-oss/yolo-jail.git
cd yolo-jail
just setup             # pinned toolchain (mise) + Go module deps
just deploy            # builds + installs the yolo CLI
```

To upgrade later, run `yolo update`; [Upgrade](userguide/getting-started.md#upgrade) covers each way of installing.

#### Upgrading from the Python version

yolo-jail used to ship as a Python package installed with `uv tool install`. `just deploy` retires that install for you — it uninstalls the `yolo-jail` uv tool and clears the console scripts it left in `$GOBIN` (`yolo`, `yolo-ps`, `yolo-host-processes`, `yolo-claude-oauth-broker-host`), which otherwise make `go install` fail with `build output "…/yolo" already exists and is not an object file`.

Nothing is deleted that cannot be positively identified as part of that old install. If something unrecognized is sitting at `$GOBIN/yolo`, the migration stops and asks you to look at it rather than guessing. `uv` itself is no longer a prerequisite.

For development, see [Contributing](#contributing).

## Quick Start

```bash
yolo init-user-config    # creates ~/.config/yolo-jail/config.jsonc; add "packs": ["claude"] to it
cd ~/code/my-project     # any repository
yolo check               # checks Nix, the runtime and your config; the first run builds the jail
yolo -- claude           # Claude Code in the jail, with its permission prompts off
yolo                     # or a shell in the jail
```

The shipped agent packs include `claude`, `codex`, `copilot`, `opencode`, `pi` and `agy`; list as
many as you like, and each installs the first time you type its name in the jail. With no packs, a
jail is a shell with no coding agent. Run `yolo check` after every config edit, and `yolo stop`,
then `yolo` again, for a running jail to pick the edit up.

Next steps, in the user guide:

- [Getting Started](userguide/getting-started.md): the first launch, logging in, and what to do next
- [Packs and Skills](userguide/guides/packs-and-skills.md): agents, shared skills and house rules
- [Configuration](userguide/reference/configuration.md): the config files, and which keys go where
- [macOS](userguide/guides/macos.md): choosing between Apple Container, Podman and `macos-user`
- [Troubleshooting](userguide/guides/troubleshooting.md): `yolo check` names the fix for most problems

## Contributing

yolo-jail is a Go module, and [AGENTS.md](AGENTS.md) is the guide to developing it: the
architecture, the build and test traps, and the file that enforces each rule. From a clone:

```bash
just setup           # the toolchain mise.toml pins, and the Go module deps
just check-ci        # the gate CI runs, before you land: go vet and staticcheck for linux and darwin,
                     # gofmt, the changelog and user-guide checks, and the short test suite
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

- [User Guide](https://docs.yolo-jail.mschulkind.dev): installing, configuring and troubleshooting
  yolo ([source](userguide/README.md))
- [Settings per setup](userguide/reference/settings-per-setup.md): what each setting does on each
  runtime
- `yolo config-ref`: every config key; `yolo --help`: every command
- [AGENTS.md](AGENTS.md): the guide to developing yolo itself

## License

[Apache License 2.0](LICENSE)
