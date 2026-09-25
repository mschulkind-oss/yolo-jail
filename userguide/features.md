# Features

YOLO Jail gives an agent a live, writable copy of your project inside an isolated environment. Your host's SSH keys, git credentials, and cloud tokens are not mounted into it. On Linux the jail uses Podman; on macOS you can choose Apple Container, Podman Machine, or a native sandbox. The [per-setup matrix](reference/settings-per-setup.md) says where each feature works.

## Packs and Agents

A **pack** is an add-on selected in your user config; it can install an agent, provide a login service, or configure tools. Nothing is active by default. Add a pack for the agent you want, then start it inside the jail. [Getting Started](getting-started.md) explains the first launch; [Migrating to Packs](guides/migrating-to-packs.md) explains older configurations.

## Packages and Tools

Add system packages to the image, or use mise for language runtimes. Tool blockers are opt-in rather than assumed. [Packages and Tools](guides/packages-and-tools.md) shows both paths.

## Network and Devices

Publish a server running in the jail to your host, or forward a host port into the jail. Linux can also pass USB devices and GPUs through. [Networking](guides/networking.md) and [Devices and GPUs](guides/devices-and-gpus.md) cover the settings and platform limits.

## Agent Integrations

Model Context Protocol (MCP) servers extend an agent with tools; Language Server Protocol (LSP) servers supply language-aware editor features. Both are configured explicitly. [MCP and LSP](guides/mcp-and-lsp.md) covers the available presets and server declarations.

## Host Services

A **loophole** is a deliberate, mediated way for the jail to use a capability that stays on the host. Some packs provide login brokers and other services; you can also declare one of your own. See [Host Services](guides/host-services.md) for a configuration example and [Loopholes](guides/loopholes.md) for the broader system.

## Storage

Installed tools and selected credentials survive a restart, while some state is specific to one workspace. Caches can be relocated to another disk. [Storage](guides/storage.md) explains what persists and what yolo reclaims.
