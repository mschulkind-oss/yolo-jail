# YOLO Jail User Guide

**Describe your agentic development environment once — agents, config, skills, tools and credentials — and run it anywhere from a sealed jail to your own shell.**

yolo composes everything an AI coding agent works with: which agents are installed, their settings and house rules, skills, MCP and LSP servers, packages, and the credentials and host services they may reach. You describe it once, declaratively and per workspace, and yolo renders that description wherever the agent runs. How much the agent is confined is one setting of it: a container jail (the default, which keeps your host credentials out), a dedicated macOS user, or no confinement at all on your own machine (`yolo host`).

YOLO Jail supports Linux and macOS. The setup differences matter: [Settings per setup](reference/settings-per-setup.md) shows which features work with Podman, Apple Container, and the native macOS sandbox.

## Quick Start

```bash
brew tap mschulkind-oss/tap
brew install mschulkind-oss/tap/yolo-jail
cd ~/code/my-project
yolo
```

Choose which agent to install by listing its pack in your user config. [Getting Started](getting-started.md) covers runtimes, the first launch, and authentication.

## What's in This Guide

### Start here

| Page | What it covers |
|---|---|
| [Getting Started](getting-started.md) | Installation, first launch, authentication |
| [Features](features.md) | A tour of what yolo sets up for an agent, and how confined it runs |
| [Settings per setup](reference/settings-per-setup.md) | What works on each host and runtime |

### Guides

| Page | What it covers |
|---|---|
| [macOS](guides/macos.md) | Apple Container, Podman Machine, and the native sandbox |
| [Packages and Tools](guides/packages-and-tools.md) | System packages, mise, and opt-in tool blockers |
| [Networking](guides/networking.md) | Ports and access to host services |
| [MCP and LSP](guides/mcp-and-lsp.md) | Model Context Protocol presets and language servers |
| [Devices and GPUs](guides/devices-and-gpus.md) | Linux device, NVIDIA, and AMD passthrough |
| [Storage](guides/storage.md) | Persistent data, caches, and container reuse |
| [Host Services](guides/host-services.md) | Run a host-side service the jail can reach |
| [Loopholes](guides/loopholes.md) | How host capabilities cross the jail boundary |
| [Migrating to Packs](guides/migrating-to-packs.md) | Move your setup into packs, and apply the same environment to your own machine |
| [Troubleshooting](guides/troubleshooting.md) | Common failures and checks |

### Reference

| Page | What it covers |
|---|---|
| [CLI Reference](reference/cli-reference.md) | Common commands and flags |
| [Configuration](reference/configuration.md) | Config files, examples, and safety |
| [Settings per setup](reference/settings-per-setup.md) | Per-setting support matrix |

### Links

- [Source repository](https://github.com/mschulkind-oss/yolo-jail)
- [Full config reference](https://github.com/mschulkind-oss/yolo-jail/blob/main/internal/cli/config_ref.txt) — or run `yolo config-ref`

**Verification:** The source guide was spot-checked on 2026-08-23 for CLI dispatch and Claude broker behavior. Platform-specific claims carry their own verification notes; see [macOS](guides/macos.md) and [Settings per setup](reference/settings-per-setup.md).
