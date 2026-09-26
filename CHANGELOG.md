# Changelog

What changed in each release of YOLO Jail, newest first.

The next release is written under [Unreleased] as changes land. Cutting it renames that heading to
the version, and `just release <version>` refuses to tag until the section reads as release notes.
The section then becomes the GitHub release body word for word.

Releases before this file existed are summarized one section per minor line — `0.9.x`, `0.8.x` —
rather than one per patch release. The per-patch detail is in the commit log.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

Agents can use Bedrock through your SSO login, every agent shows a footer naming its billing route,
and a launch refuses an agent that cannot speak its provider's protocol.

### Added

**Bedrock from your SSO login.** The `aws-auth` pack, which the claude pack brings along, turns a
host `aws sso login` into a short-lived credential for a role you name. The jail holds no key and
no `~/.aws`. Enable `loopholes.aws-auth` in your user config with a `profile` and `role_arn`, as
[the pack's README](packs/aws-auth/README.md) shows, then run `yolo -p bedrock -- claude`.

**An agent footer.** Every agent with a status-line hook shows its billing route and whether it
runs in a jail or on the host, unless you set your own `statusLine`. Claude's also shows its
thinking level.

**Agents and providers are checked against each other.** A launch whose agent cannot speak any
protocol its provider serves is refused, naming any shipped pack that translates between them.
`yolo check` predicts it. See [Providers](docs/reference/providers.md).

- `yolo host-daemon status|stop|restart|logs` manages the machine-wide daemons.
- `nix shell` and `nix build` work in a jail with no extra flags.

### Changed

- **Ctrl-C reaches the jail**, interrupting what runs there, such as a Claude reply, instead of
  ending your session.
- **Git packs are fetched at launch**, and a `?ref=<branch>` pack follows its branch within the
  hour. Pin a tag or commit for any pack that runs code on your machine.
- **`lsp_servers` installs nothing.** Install servers through `mise_tools` or `packages`. Run
  `yolo programs ls` before `programs.autoprune: true`, which deletes the old copies and all of
  `~/go/bin`.
- **A new podman workspace copies only your Claude account and onboarding state** from the
  machine-wide home, so a login kept only there asks you to sign in once.
- **Pack briefings come only from a pack's `briefing/` directory**, not the instructions file at
  its root.
- **Dropping a profile clears the provider and model yolo wrote for it** into pi, opencode or
  codex, and keeps a model you picked inside the agent.
- `writable_home_dirs` and `host_files` refuse the paths of the packs you select, including packs
  yolo does not ship, and no others. A claude-only workspace may now name `.codex` or
  `~/.codex/config.toml`.
- pi updates its extensions before it starts, at most hourly and whenever its settings change.
  `agent_updates: false` turns that off.
- An unknown flag exits 2 instead of being ignored.
- Two writers for one `host_files` destination refuse the launch; the last one used to win.

### Removed

- A provider's bare `base_url`, which agents read as different protocols. Write
  `endpoints.<protocol>.base_url`.
- The `claude_plugins` pack hook. Its refusal names the replacements.
- `yolo host wrappers enable` and `disable`. Wrappers are on by default when `host_management` is
  `"own"`, and `"host_wrappers": false` in your user config turns them off.

### Fixed

- The Claude OAuth broker could return the token a jail already held, and Claude Code stopped
  with `api_request_oauth_refresh_exhausted`.
- Claude on its `codex` profile showed `did not translate` in place of ChatGPT's own errors.
- Claude over the wire bridge reported zero input tokens. A provider that rejects the usage
  request needs `"supports_usage_in_streaming": "false"` in its `options`
  ([Streamed usage](docs/reference/wire-bridge.md#streamed-usage)).
- `yolo -p` stopped changing pi's model once your host's pi settings named one.
- The models you scoped inside pi were reset to yolo's list at every launch.
- Every pi launch printed `Failed to load theme "system"`.
- With the kilo pack and no profile naming a kilo model, pi rejected its whole `models.json`.
- Claude's LSP plugins that you enabled on the host were off in the jail unless `lsp_servers`
  named their language.
- Codex could not renew its ChatGPT sign-in through the `openai-auth` pack.
- `yolo host apply --assert` replaced the `env` block of your host `~/.claude/settings.json`,
  dropping your own variables.
- `yolo host apply` skipped every git pack, even an installed one.
- `yolo host apply` adopted `~/.claude/skills/synced/`, and the next claude.ai sync lost new and
  edited skills.
- A workspace pinning an older Node, such as 20, left pi unable to start.
- A workspace's `.vscode/mcp.json` was hidden from agents and showed as modified in git.
- `yolo -p <profile> -- <command>` refused any command that was not an agent.
- Every yolo process left a copy of the shipped packs in `/tmp`, which `yolo prune` now removes.
- Two launches of one workspace at once could fail with `directory not empty`, or start with
  part of a pack missing; the second now waits for the first. Attaching to a running jail no
  longer empties and re-copies the pack files it has mounted.

### Security

- A provider credential, and the settings a profile composes, reached every process in the jail
  whichever agent selected the profile, so pi could use Bedrock because Claude had the keys. Each
  now reaches only the agent that selected it
  ([the credential gate](docs/reference/providers.md#the-credential-gate)). A plain shell sees
  only `env_sources` values no provider claims, and `yolo host env` prints one agent's slice.
  On macos-user only the program the invocation starts gets its profile's values, so an agent
  started from the sandbox's login shell gets none. A variable you set on an agent's command
  line no longer overrides what its profile composes.
- Host-side yolo followed symbolic links in a workspace's `.yolo` state, so an agent could have
  the next launch or `yolo prune` write to a host path as you.
- A provider key could be sent to `api.anthropic.com` when the provider named no Anthropic
  address.
- A new workspace's Claude login seed carried other workspaces' `projects` and `mcpServers`.
- On Apple Container older than 1.1.0, a `host_files` directory source was bound writable.

## 0.10.x

_0.10.0, September 2026._

yolo learned to route agents to local and gateway models. The new `llamacpp` pack points agents at
a llama.cpp server on your host, `kilo` and `openrouter` are gateway provider packs, and `zai`
gained the Z.ai coding plan. A provider on your host's loopback is forwarded into the jail, and
Claude, pi, opencode and Copilot read a provider's context window, timeouts and a local API-key
fallback. MCP servers are delivered by what the chosen authentication source can do.

### Changed

- A launch whose workspace is, or contains, your home directory, `~/.config/yolo-jail` or
  `~/.local/share/yolo-jail` is refused. There is no override; launch from a project directory.
- A launch whose `required_capabilities` no selected pack satisfies is refused, and `yolo check`
  predicts it.
- `yolo config` read verbs take `--at`, with the same vocabulary as `yolo apply`.
- On `macos-user`, the launch tees its disclosures into `launch.log` and announces the pack code it
  runs on your Mac.

## 0.9.x

_0.9.0, September 2026._

Your own machine became a place to run an agent. `yolo host apply`, which replaced
`yolo apply --host`, renders your packs' config, skills and briefing into your real home, and
`yolo host -- <agent>` runs an agent there with the environment a config file cannot carry:
credentials from `env_sources`, a provider's flags, and unsets. `host_wrappers: true` puts a
generated wrapper for each agent on your PATH. Providers and profiles arrived: `-p <name>` selects a
provider such as `zai` or `cerebras`, the `wire-bridge` pack lets Claude use an OpenAI-shaped
provider, and `openai-auth` shares one OpenAI login across Codex and pi. Every host service became a
pack you opt into, and a loophole can declare its own config keys, which you set under
`loopholes.<name>.settings` and yolo checks before its daemon reads them. `yolo stop` stops a
workspace's jail, and `yolo programs ls` and `yolo programs remove` show and clear programs nothing
declares any more.

### Changed

- **Run `yolo broker restart` once after upgrading**, unless `just deploy` did it for you or the
  machine has rebooted. A broker left running from the previous version reads every jail's request
  wrong, and every Claude token refresh on that host fails while `yolo broker status` looks healthy.
- The Claude OAuth broker ships in the `claude` pack and runs only when that pack is selected. It
  no longer needs `claude` on the host. `yolo broker status` shows `socket accept:` instead of
  `ping:`, and leftover per-jail relays from the previous version are removed by
  `yolo prune --apply`.
- Host services are packs, off until you select them and enable their loophole in your user
  config: `audio` (now off by default), `cgroup-delegate` (for `yolo-cglimit`), `journal` (for
  `yolo-journalctl`) and `host-processes` (for `yolo-ps`). A workspace config that enables one
  without the pack selected refuses the launch.
- The host-processes allowlist moved to `loopholes.host-processes.settings.visible` and is read
  once at launch, so an edit needs a jail restart. The journal's whole-host view is
  `loopholes.journal.settings.full: true`, in your user config only.
- `grep -r` and `find` are no longer blocked by default. Select the `guardrails` pack to block
  them again.
- `yolo host` reads only your user config, never the workspace's. Move `env_sources`, `providers`
  and profile entries meant for host launches into `~/.config/yolo-jail/config.jsonc`. A relative
  `env_sources` path now resolves beside the file that declares it.
- A provider selected with `-p` reaches a jail that is already running, and its token no longer
  appears on the container's command line. A `-p` naming a different profile than a jail started
  by an older yolo is refused; stop that jail and launch again.
- yolo keeps agent CLIs current when you run them, at most once an hour. `agent_updates: false`
  freezes them, and `yolo pack update` refreshes them on demand. Copilot runs its own update check
  and says in-session when a new version exists; `COPILOT_AUTO_UPDATE=false` silences it.
- Launches run `mise install` and no longer `mise upgrade`, so fuzzy pins such as `node = "24"`
  stay where they are until you upgrade in the workspace.
- A changed [`yolo-jail.jsonc`](yolo-jail.jsonc) with no terminal to confirm it refuses the
  launch. Pass `--accept-config-changes` in scripts and CI.
- A host service the jail cannot reach refuses the launch. `YOLO_ALLOW_UNREACHABLE_SERVICES=1`
  overrides it.
- Launching on `/workspace` from inside a jail refuses. Nest from a throwaway directory instead.
- `yolo pack install` no longer asks you to approve a fetched pack's host access. The launch
  banner names every host file a pack reads and every command it runs on your machine.
- A pack's `bin` must be a bare program name. A loophole that declares both `settings` and a
  `jail_daemon` must list its `state_files`.
- `node_modules` is kept separate between the host and the jail by default.
- The environment briefing describes what the launch applied rather than what the config asked
  for, so a nested jail's briefing says it uses host networking.
- A pack whose program names a delivery mechanism the image does not know is skipped with a note
  instead of refusing the boot.
- On Apple Container, your host `~/.claude/settings.json` and `~/.pi/agent/settings.json` now
  apply in the jail, a `host_files` file entry holds your file instead of an empty one (delete a
  `"mode": "once"` destination once to reseed it), and shared credentials are shared across
  workspaces again. Workspaces that had separate logins converge on the first one launched.
  Read-only pack mounts are skipped on versions older than 1.1.0, which ignore `:ro`.
- On `macos-user`, agents launch with the same permission-bypass flags as on the container
  backends, `workspace_readonly: true` is enforced, and launchers stopped reaching the network on
  every invocation. The launch now warns about what that backend cannot deliver, such as skills and
  briefings.

### Removed

- `yolo run --new`. Replace a jail with `yolo stop`, then an ordinary launch.
- `yolo apply --host`. Use `yolo host apply` or `yolo apply --at host`.
- The top-level `journal` and `host_processes` keys, and the `audio-alsa` loophole. Each is
  refused by name, naming its replacement.

## 0.8.x

_0.8.0, August 2026._

Agents became packs. The `packs` key selects them by name, and nothing is active until you list one,
so an empty config gives a jail with no coding agent. `yolo pack` creates, lints and installs packs,
including git-hosted ones, and a pack can carry skills, briefings, config, MCP servers and files as
well as an agent. Google's Antigravity CLI (`agy`) arrived, and Gemini CLI left. `host_files`
brings named host files into the jail, and `yolo config ls`, `diff`, `reset`, `capture`, `drift`
and `dump` show and manage what the jail changed. Every jail carries a built-in skill suite, and
`yolo describe` and `yolo apply` describe and render the environment.

### Changed

- The `agents` key is refused. List agents in `packs` instead.
- A config generator that fails now stops the boot instead of starting an agent on partial config.
- Your git identity is composed on the host and mounted read-only in the jail.
- Homebrew and the release archives ship a prebuilt bundle, so an installed `yolo` builds the jail
  image without Go or a source checkout.

### Removed

- The Gemini CLI agent.

### Contributors

Thanks to Dong ([@liudonggalaxy](https://github.com/liudonggalaxy)) for a non-fatal optional
install and faster mise workspace trust, Georgi Popov
([@georgipopovhs](https://github.com/georgipopovhs)) for a fix to image reload detection, and Svet
([@neykov](https://github.com/neykov)) for the macOS Podman VM and network checks in `yolo check`.

## 0.7.x

_0.7.0 – 0.7.1, July 2026._

yolo stopped being a Python package and became one Go binary. Installing it changed: `brew install`
from the project's tap, a platform archive from a release, or the PyPI wheel, which now wraps the
Go binary. `just install` removes the old Python install first. `yolo --help` and `yolo ps` are
colored on a terminal, and pi reads your host `~/.pi/agent/settings.json`. On a Mac, the jail image
is built through the published Linux builder container, with no Linux machine to set up.

## 0.6.x

_0.6.0, July 2026._

Agent support became a list you choose from, and OpenAI Codex joined it. The `macos-user` backend
arrived: a dedicated macOS account confined by Seatbelt, with no VM. For Apple Container and Podman
on a Mac, a ready-made Linux builder image is published.

## 0.5.x

_0.5.0, July 2026._

- `env_sources`, an ordered list of files and values, replaced the `env` key.
- `include_if_found` and `yolo-jail.local.jsonc` layer untracked overrides over a workspace config.
- AMD GPUs pass through with ROCm, and video decoding with `gpu.vaapi`.
- `mcp_servers` entries take per-server `env` with `${VAR}` expansion, and `requires_env` loads a
  server only where its variables exist.
- LSP servers became opt-in, and are uninstalled when you remove them.
- The audio loophole bridges PipeWire and ALSA as well as PulseAudio.
- Ctrl-Z in a jail drops you back to the host shell.
- `packages` entries can select a nix output such as `.dev` or `.lib`, and their libraries are found
  by the loader.

### Contributors

Thanks to Kurt Galiatsatos ([@kurt-hs](https://github.com/kurt-hs)) for macOS Podman image builds
that need less from a Linux builder, and for keeping agents in a jail from modifying yolo's own
code.

## 0.4.x

_0.4.0 – 0.4.3, April to May 2026._

macOS became a supported host, with Podman Machine, Docker (since removed) or Apple Container.
Loopholes arrived, each a host service a jail may use: the Claude OAuth broker, which serializes
token refreshes across jails, the `host-processes` view behind `yolo-ps`, a journal bridge, and
audio for Claude Code's `/voice`. `yolo prune` reclaims disk, dry-run by default. `kvm: true` passes
`/dev/kvm` through, the jail takes your host's timezone, and the `grep` block applies only to
recursive searches.

### Contributors

Thanks to Toan Vuong ([@Toad2186](https://github.com/Toad2186)) for an Apple Container fix and the
NixOS builder documentation, and to Georgi Yanchev
([@georgi-yanchev](https://github.com/georgi-yanchev)) for Apple Container startup and `yolo check`
fixes.

## 0.3.x

_0.3.0 – 0.3.1, April 2026._

Containers got readable names, and the jail prints its version at startup. The `env` key set custom
variables in a jail. Jails starting at the same time stopped racing on shared state, Claude's login
works in a jail with a read-only home, and a jail can run inside another jail. Image tars are cached
and shared across projects. 0.3.1 fixed provisioning of the free-threaded Python build and an npm
`ENOTEMPTY` failure.

## 0.2.x

_0.2.0, April 2026._

NVIDIA GPUs pass through to the jail, and `yolo check` verifies the driver setup. `resources` sets a
jail's memory, CPU and process limits, and a host-side cgroup delegate lets a jail limit its own
jobs. Claude Code is supported, with a separate history per jail. The jail announces when it is
ready and shows provisioning output while it starts.

## 0.1.x

_0.1.0, March 2026._

The first public release: a container jail in which Copilot and Gemini can run in YOLO mode with no
access to your SSH keys, git identity or cloud credentials, built from a Nix flake and configured by
[`yolo-jail.jsonc`](yolo-jail.jsonc). Blocked tools explain themselves and suggest what to use
instead.
