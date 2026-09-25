# Features

yolo sets up everything an AI coding agent works with, from one declaration: the agent itself, its settings and house rules, its skills, its MCP and LSP servers, the packages it needs, and the credentials and host services it may reach. The same declaration runs at whatever confinement you choose, from a container jail to your own machine. The [per-setup matrix](reference/settings-per-setup.md) says where each feature works.

## Packs and Agents

A **pack** is an add-on selected in your user config; it can install an agent, provide a login service, or configure tools. Nothing is active by default. Add a pack for the agent you want, then start it. [Getting Started](getting-started.md) explains the first launch; [Migrating to Packs](guides/migrating-to-packs.md) explains how to write your own setup down as a pack.

## Agent Integrations

Model Context Protocol (MCP) servers extend an agent with tools; Language Server Protocol (LSP) servers supply language-aware editor features. Both are configured explicitly. [MCP and LSP](guides/mcp-and-lsp.md) covers the available presets and server declarations.

## Packages and Tools

Add system packages to the image, or use mise for language runtimes. Tool blockers are opt-in rather than assumed. [Packages and Tools](guides/packages-and-tools.md) shows both paths.

## Host Services

A **loophole** is a deliberate, mediated way for an agent to use a capability that stays on the host. Some packs provide login brokers and other services; you can also declare one of your own. See [Host Services](guides/host-services.md) for a configuration example and [Loopholes](guides/loopholes.md) for the broader system.

## Confinement

How much the agent is confined is one setting of the declaration:

- **A container jail** is the default. The agent gets a live, writable copy of your project, and your host's SSH keys, git credentials, and cloud tokens are not mounted into it. On Linux the jail uses Podman; on macOS, Apple Container or Podman Machine.
- **A dedicated macOS user** (`macos-user`) runs the agent natively under Apple Seatbelt, with no VM. [macOS](guides/macos.md) covers the trade-offs.
- **Your own machine**, with no confinement: `yolo host apply` renders the same config, skills and briefing into your real home, and `yolo host -- <agent>` runs an agent there. [Migrating to Packs](guides/migrating-to-packs.md#part-2--manage-your-host) walks through it.

## Network and Devices

Publish a server running in the jail to your host, or forward a host port into the jail. Linux can also pass USB devices and GPUs through. [Networking](guides/networking.md) and [Devices and GPUs](guides/devices-and-gpus.md) cover the settings and platform limits.

## Storage

Installed tools and selected credentials survive a restart, while some state is specific to one workspace. Caches can be relocated to another disk. [Storage](guides/storage.md) explains what persists and what yolo reclaims.
