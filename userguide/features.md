# Features

Choose where agents run, package the setup they carry, connect only what matters, and keep the
result observable and repeatable. Each entry links to the page that covers it.

**Current status:**

- **Jail**: runs in an isolated container. Supported, and the default.
- **Your own machine** (`yolo host`): configuration and launch are supported.
- **Guest**, a separate user account on your machine with no container: in development on a Mac,
  where it is the `macos-user` sandbox below. Not yet available on Linux.
- **`macos-user`**, the Mac sandbox with no container: works, but is still in development and not
  yet the recommended Mac setup.

## Where agents work

**One setup across your repositories.** Keep personal defaults, such as your agents, in your user
config, and give each repository the tools and limits it needs in its own `yolo-jail.jsonc`. The
agent works on the same files you do, at `/workspace`, with no copy to sync.
[Configuration →](reference/configuration.md)

**Choose the boundary.** Run agents in an isolated jail, configure them on your own machine, or try
the Mac sandbox. [Where agents run →](guides/confinement.md)

**Linux and Mac.** Podman on Linux; Apple Container or Podman on a Mac, running native ARM Linux on
Apple silicon with no emulation. [Getting Started →](getting-started.md) ·
[macOS →](guides/macos.md) · [What works on each setup →](reference/settings-per-setup.md)

## Shape the setup

**Bring the agents you want.** Claude Code, Codex, Copilot, opencode, pi, Antigravity and oh-omp,
each through a pack. Nothing is selected for you, and each runs without permission prompts inside
the jail, except oh-omp, which has no such setting. [Choose an agent pack →](guides/packs-and-skills.md#choose-your-packs)

**Make your setup shareable.** A pack bundles skills, house rules, settings and tools. Use the ones
yolo ships, write your own, or share one from a git repository.
[Write your own pack →](guides/migrating-to-packs.md)

**Share skills without copy-paste.** One skill reaches every agent you selected, or only the ones
you name, and a project can take only part of a pack.
[Skills and house rules →](guides/packs-and-skills.md#skills-and-house-rules)

**Bring your Claude Code plugins and mods.** Wrap a plugin as a pack, keep a mod in the repository,
or install one with Claude Code and share it with every jail. Each launch names the packs whose
plugins run code. [Claude Code plugins and mods →](guides/claude-plugins-and-mods.md)

**Settings that follow you across agents.** MCP servers, language servers and model choices are
written into each agent's own settings files, where that agent supports them.
[Settings across agents →](guides/agent-settings.md) · [MCP and LSP →](guides/mcp-and-lsp.md)

**Stay in control of agent updates.** Agents keep themselves current when you start them, or stay
frozen if you say so, for all of them or one at a time.
[Agent updates →](guides/packs-and-skills.md#keep-agents-and-packs-up-to-date)

**Keep your patches on the newest release.** Give yolo an upstream and your changes as a patch
series, for a program or a pi extension. It builds them onto each new release they fit and keeps
the last good build when they stop fitting.
[Follow an upstream with a patch series →](guides/patch-series.md)

## Connect what matters

**Choose how agents log in.** Log in inside the jail once, hand an agent an API key on purpose, or
let a host service keep one shared Claude or ChatGPT login fresh for every jail.
[Logins →](guides/authentication.md)

**Change providers without changing your workflow.** Pick a named profile at launch to point an
agent at another model service; the wire bridge handles supported format mismatches, in a jail and
for a `yolo host` command alike.
[Providers and models →](guides/providers-and-models.md)

**Grant only the host access you need.** Your SSH keys, git credentials and cloud tokens stay out.
Add read-only folders, host ports, devices or a loophole, one line at a time, and read a pack's
footprint before you select it. [Host access and loopholes →](guides/loopholes.md)

**Use GitHub without a token in the jail.** The `github` pack runs the jail's `gh` commands with
your own host login, only against the project's own repositories, and records every call. It is
read-only for now. [GitHub →](guides/github.md)

**Reach services and ports.** Open a jail's dev server from your browser, or reach a database on
your host from the jail. [Networking →](guides/networking.md)

**Hardware on Linux.** Pass USB and serial devices, NVIDIA or AMD GPUs, and `/dev/kvm` into the
jail. [Devices and GPUs →](guides/devices-and-gpus.md)

## Keep it dependable

**Give every project the right tools.** Declare packages and tool versions per project; yolo builds
a reusable environment instead of relying on whatever is installed.
[Packages and tools →](guides/packages-and-tools.md)

**Approve every change to the jail.** When a project's config changes, the next launch shows you
the difference and waits for your yes, so an agent cannot quietly grant itself more.
[Approving config changes →](reference/configuration.md#approving-config-changes)

**See what changed and what is running.** Validate your setup, inspect the resolved configuration,
list running jails, and reclaim disk with explicit, dry-run-first commands.
[CLI reference →](reference/cli-reference.md) · [Storage →](guides/storage.md)

**When something breaks.** `yolo check` names the fix for most problems.
[Troubleshooting →](guides/troubleshooting.md)
