# YOLO Jail User Guide

**Agentic development environments, your way.** Choose your coding agents, give them the right
skills and tools, and decide what they can access. yolo brings the whole setup together across your
repositories, on Linux and macOS.

You describe the setup once: which agents, their settings and house rules, their skills and tools,
and what on your machine they may reach. yolo builds that environment for each repository and runs
the agent in it. By default that is a **jail**, an isolated container that keeps your SSH keys and
cloud logins out of reach, so the agent can work without asking permission for every command.

## How it works

Declare the tools, settings and connections an agent needs. yolo assembles the environment for your
repository and makes every crossing out of the jail a deliberate choice.

**Tools.** List the packages a project needs, and yolo builds them into the jail with
[Nix](https://nixos.org), a reproducible package builder that caches identical builds so jails
share them. For versioned development tools, such as a particular Node or Python, use
[mise](https://mise.jdx.dev), a tool-version manager. Neither changes the tools installed on your
own machine.
[Packages and tools →](guides/packages-and-tools.md)

**Settings.** Share skills, house rules, tool servers and model choices across every agent you use,
through **packs**, reusable bundles of agent setup, and your config. yolo writes each selected
agent's own settings files from them. Not every setting translates to every agent, and yolo says
which do. [How settings are applied →](guides/agent-settings.md)

**Models.** Point an agent at a different model service, such as z.ai, OpenRouter, AWS Bedrock or a
model you run yourself, by choosing a **profile** when you launch. When an agent and a provider
speak different request formats, the **wire bridge**, a small translator that runs beside the
agent, connects them: inside the jail, or on your own machine for the one `yolo host` command that
needs it. It does not open any access to your host.
[Providers and models →](guides/providers-and-models.md)

**Logins.** You log in to each tool inside the jail, once, and the login is kept. For Claude and for
a ChatGPT subscription, a service on your host keeps one shared login fresh for every jail, so
several jails do not log each other out. [Logins →](guides/authentication.md)

**Host access.** A **loophole** is a named connection from a jail to one specific capability on
your host, such as a login service or a system-log reader. Each is listed in its pack's footprint,
and most stay off until you turn them on. [Host access and loopholes →](guides/loopholes.md)

## Start here

Install Nix, a container runtime and yolo: **[Getting Started](getting-started.md)** has the
copy-paste steps for Mac and Linux. Then choose an agent, check the setup, and launch:

```bash
yolo init-user-config      # creates ~/.config/yolo-jail/config.jsonc; add "packs": ["claude"] to it
cd ~/code/my-project       # any repository
yolo init                  # optional: a yolo-jail.jsonc for this project's own settings
yolo check                 # checks Nix, the runtime and your config, and builds the jail
yolo -- claude             # start Claude Code in the jail
```

With no packs selected, the jail has a shell and yolo's built-in tools, but no coding agent.
[How packs work →](guides/packs-and-skills.md)

## What you choose

**Where agents work.** Use an isolated jail for autonomous work, the default. Manage the same agent
configuration on your own machine with `yolo host`. A third option, a separate user account on your
machine, is in development. [Where agents run →](guides/confinement.md)

**What they carry.** Bring agents, skills, house rules and tools together in packs. Use the ones
yolo ships, write your own, share them with your team, or narrow one for a single project.
[Packs and skills →](guides/packs-and-skills.md)

**How they connect.** Pick providers and how each agent logs in, and give a jailed agent only the
host capabilities you turn on. [Logins →](guides/authentication.md) ·
[Host access →](guides/loopholes.md)

## Reference

The **[feature index](features.md)** links every feature to the page that covers it.

| Page | What it covers |
|---|---|
| [Getting Started](getting-started.md) | Installing Nix, a runtime and yolo on macOS and Linux; the first launch |
| [macOS](guides/macos.md) | Apple Container, Podman and the `macos-user` sandbox, and what each can do |
| [Where Agents Run](guides/confinement.md) | The jail, your own machine, and guest confinement |
| [Packs and Skills](guides/packs-and-skills.md) | Choosing packs, checking them, sharing skills, keeping agents updated |
| [Claude Code Plugins and Mods](guides/claude-plugins-and-mods.md) | Bringing a Claude Code plugin or mod into a jail, and what the launch shows about it |
| [Settings Across Agents](guides/agent-settings.md) | How yolo writes each agent's settings, and how to inspect them |
| [Providers and Models](guides/providers-and-models.md) | Providers, profiles, API keys and the wire bridge |
| [Logins](guides/authentication.md) | Logging in, shared logins, and pushing to git from a jail |
| [Packages and Tools](guides/packages-and-tools.md) | Nix packages, mise tools, and blocking tools |
| [MCP and LSP](guides/mcp-and-lsp.md) | Tool servers and language servers |
| [Networking](guides/networking.md) | Ports, and reaching services on your host |
| [Host Access and Loopholes](guides/loopholes.md) | Every way the jail reaches your machine |
| [GitHub Without a Token](guides/github.md) | `gh` in the jail with your host's login, this project's repositories only |
| [Devices and GPUs](guides/devices-and-gpus.md) | USB, serial, NVIDIA and AMD passthrough on Linux |
| [Storage](guides/storage.md) | What persists, what yolo reclaims, and moving caches |
| [Writing Your Own Pack](guides/migrating-to-packs.md) | Your setup as a pack, and applying it to your own machine |
| [Your Own Host Service](guides/host-services.md) | Run a program on the host for the jail |
| [Writing a Loophole](guides/writing-loopholes.md) | The loophole manifest, for pack authors |
| [Troubleshooting](guides/troubleshooting.md) | Common failures and their fixes |
| [Configuration](reference/configuration.md) | Config files, which keys go where, and approving changes |
| [Settings per setup](reference/settings-per-setup.md) | What each setting does on each runtime |
| [CLI Reference](reference/cli-reference.md) | Commands and flags |

The complete list of config keys is `yolo config-ref`, and every command has its own `--help`.
The source is on [GitHub](https://github.com/mschulkind-oss/yolo-jail).
