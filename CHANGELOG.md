# Changelog

What changed in each release of YOLO Jail, newest first.

Releases before 0.11.0 are summarized one section per minor line, such as `0.10.x`, rather than
one per patch release.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

**Patch series for programs and pi extensions.** A pack can name an upstream and your changes as a
patch series, and yolo builds them onto each new release they fit. When a release conflicts, the
last good build keeps running and `yolo pack rebase` sets up the fix, and `yolo pack series check`
says whether a series still applies; both work in a jail. `yolo pack lint --online` checks a series
before you use it, and patch series need git 2.40 or newer on the host. A launch now leaves out, and
names, any part of a pack written for a newer yolo instead of failing, and `yolo features` lists
what a yolo can read. See [Follow an upstream with a patch series](userguide/guides/patch-series.md).

**Claude Code plugins and mods.** A new guide covers the ways to bring a Claude Code plugin or mod
into a jail, and what each launch shows about the code it runs. See
[Claude Code plugins and mods](userguide/guides/claude-plugins-and-mods.md).

### Fixed

- When `yolo host` has no container runtime to capture an agent with, it now says to install one.
- A Ctrl-C in one of a jail's terminals now says the jail stays up for the others.
- A Ctrl-C as a terminal's session starts no longer leaves that session's command running in the
  jail.
- A host service that goes down just as a jail starts, or as another terminal joins it, is now
  reported to that terminal.
- `yolo stop` now stops an Apple Container jail, and a jail launched with a different
  `YOLO_RUNTIME`, where it used to say no jail was running, and on Apple Container yolo's messages
  now name it instead of `container stop`.
- A launch retries a Podman that cannot be started for a moment, such as mid-upgrade, instead of
  refusing, and one that gives up waiting for Podman now always shows Podman's last error.
- On Apple Container, a launch that cannot tell whether its jail is running now says to run
  `container ls`, not `container ps`.
- Ending a jail no longer waits on, and leaves behind, a stopped container Podman could not remove
  itself, as can happen after a terminal closes while its session starts.
- On Apple Container, claude, codex and agy are now recorded once per machine, where every launch
  ran their installers again and recorded nothing.
- Joining a running jail no longer runs agent installers to record them first; only a launch that
  starts a jail does.
- When yolo cannot record an agent's install, the next launches no longer try again each time: the
  launch that failed says when it will retry, and `yolo capture <agent>` retries at once.
- A launch whose temporary directory is full or unusable now says so and how to fix it, instead of
  pointing at Podman.
- After a `/login` in a jail, Claude no longer keeps the previous login's permissions or expiry
  date.
- On Apple Container, a terminal whose jail is stopped while it runs now says the jail stopped and
  why, instead of saying nothing or that the jail stays up.
- The launch, `yolo pack footprint` and `yolo host apply` now name all the code a wrapped Claude
  plugin runs, its workflows and highlighting grammars included, wherever Claude Code or Copilot
  loads it from by default, not only where its manifest says, and the launch says when its hooks
  include a mod's module.
- A Claude plugin at the root of a pack with no `skills/` folder, the usual shape of a Claude Code
  mod, now reaches a jail and `yolo host apply`.
- A plugin whose manifest names it with a path, such as `../x`, is refused instead of being written
  outside the skills folder.
- When a flat skills folder leaves out a wrapped Claude plugin's workflows, themes, highlighting
  grammars or the agent it sets for your sessions, it now says so by name, as it does for the rest.
- In a jail, `gh` now runs `--jq` and `--template`, takes an encoded branch name such as
  `MS%2Fmain`, and answers `gh auth status` without your machine's paths or the token's scopes; a
  write says at once that nothing ran and to run it on your machine.
- A jail's `gh` can no longer reach past the project through `--org` or `--user` on a secret or
  variable write, a `..` in an argument, or a `:owner` or `:repo` placeholder.
- A macos-user launch of Codex or Pi no longer refuses when the shared OpenAI credential service
  restarts just as it connects.
- `yolo host-daemon stop` or `restart` on a host-wide service that was not running, such as
  `aws-auth`, no longer stops the Claude OAuth broker in its place.
- On a Mac, a launch and `yolo check` wait up to a minute for a busy Podman machine instead of
  calling it not started after 10 seconds.
- A launch, `yolo check` or `yolo prune` stopped by a signal sent to yolo alone, such as `kill` or
  a supervisor's, no longer leaves the nix it was running behind, a macOS sandbox's package build
  included.
- A launch, or `yolo check`, no longer hangs for good, printing nothing, when nix prints one very
  long line while it builds the jail's image, yolo's own binaries, or a macOS sandbox's packages.
- A fork's build no longer refuses over something only another of your packs provides, such as the
  agent a pack's prose is addressed to or a capability your config requires, and a build that stops
  before it starts now quotes why.
- A fork's build no longer gets a copy of your config, your MCP servers' settings or your
  `agents_md_extra` text, and neither its launch nor its briefing lists credentials, host files or
  host connections the build does not get.
- A launch with `aws-auth` enabled no longer warns that its `.mount-sentinel` is missing.
- A jail's wait for its in-jail services shows only when it is slow, and a failure names the
  service's log.
- A profile that reaches nothing for an agent now says where you selected it and the setting that
  stops it.
- A missing `mounts` source now names the file and line that declare it, and the fix.
- Host service logs now record each request's real exit code.
- npm in a jail no longer prints update or funding notices.
- On Apple Container, a config with `network.forward_host_ports` now stops the launch before
  anything starts, naming the key and how to go on, instead of failing inside Apple Container with
  an error about a socket.

## [0.11.1] - 2026-10-02

`yolo host` runs yolo's own copy of your agents, a jail now ends with its last terminal, and agent
updates no longer hang a launch.

### Added

**Your agents at the host.** `yolo host -- <agent>` runs yolo's own, current copy where it can
(`"host_floor": false` opts out), and `yolo host apply` writes your MCP servers, providers and
model into its config. See
[yolo's own copy of your agents](userguide/guides/confinement.md#yolos-own-copy-of-your-agents).

**`gh` without a token.** The `github` pack runs `gh` for the jail through your login, read-only
and only on the project's repositories. Turn it on per project with
`yolo loopholes enable github-broker`; `enable` and `disable` now switch any loophole for one
project. See [GitHub](userguide/guides/github.md).

**More agents, more providers.** `-p bedrock` sets up codex, opencode and pi, `-p codex` runs
opencode on ChatGPT, and pi, opencode and oh-omp mix providers (`-p pi=zai,openrouter`). On
macos-user, `-p bedrock` and profiles that need the wire bridge, such as `-p cerebras -- claude`,
now work.

**Choose each provider's models.** A pack's `models` entry adds to a provider's model list or
narrows it, and agents that allow it, Claude Code included, offer only that list. See
[a company's model list](userguide/guides/providers-and-models.md#model-menus-and-a-companys-model-list).

**Agent scratch space that survives a restart.** Every jail has `$YOLO_DURABLE_DIR`, a folder for an
agent's own files that yolo never deletes. It isn't part of your project, and you never need to
look in it.

**Read-write mounts.** A `mounts` entry in your user config can be read-write:
`{"host": "~/scratch", "mode": "rw"}`.

**herdr shows a jailed agent.** [herdr](https://herdr.dev)'s sidebar shows a `yolo -- <agent>`
working, blocked or done, and a split pane's border reads `🔒 JAIL <project>`;
`YOLO_NO_HERDR=1` turns it off. Contributed by Kurt Galiatsatos
([@kurt-hs](https://github.com/kurt-hs)).

**Your own fork of an agent.** A small pack names your fork and its build command. Its first launch
pins it, and yolo builds that commit once for every workspace. See
[Run your own fork](userguide/guides/packs-and-skills.md#run-your-own-fork-of-a-program).

### Changed

- Each terminal's agent ends with its terminal, and a jail with its last one or `yolo stop`.
- `use_profiles` is now `profile` (the refusal shows the rewrite), and a profile name may not hold
  a comma.
- Later packs in `packs` now win: list yours after the shipped pack it overrides.
- Credentials go only to agents: `yolo host -p` and `yolo host env -p` refuse a command that isn't
  an agent (use `--with-credentials`), and a jail's shell gets no AWS credentials.
- A pi, opencode or Codex key only in your shell stops a jail launch: put it in `env_sources`.
- A project config may not set a provider's `api_key_env_name` or `endpoints`, or remove a
  provider: use your user config.
- A Bedrock launch with no region is refused: set `providers.bedrock.region` or your AWS
  profile's region.
- Two packs shipping one skill name stop the launch: rename one, or set
  `"skills_tier": "namespaced"` in one pack's `pack.json`. A `supersedes` nothing serves stops it
  too: fix or delete the claim, or remove that pack.
- A namespaced pack's skills are `/<pack>:<skill>` in a jail too, as at your host: call them by
  that name.
- A pack's `install_hints` may hold only package names before its first ` && `: move anything
  else after it.
- pi reads yolo's MCP servers itself: remove `pi-mcp-adapter` if you added it for them.
- pi on `-p codex` runs only yolo's models there, `pi --model` included: add one to
  `providers.openai-codex.models`, or set `"enforce_models": false` on your profile.
- pi's subagents run only your profile's provider's models: add a provider to pi's set
  (`-p pi=zai,openrouter`) to give them its models.
- GPT-6.1 Sol replaces GPT-6 Sol on `-p codex`: pick again if you chose it.
- `yolo host` refuses `codex agents`, and yolo turns Codex's background copy off.
- Two mounts at one path fail `yolo check`: give one another `at`. On macos-user, a mount from
  inside a home stops the launch: copy the folder under `/Users/Shared/yolo`, or remove the mount.
- `yolo pack ls`, `install`, `update` and `status` take no argument: run them bare.

### Fixed

- A launch no longer hangs on an agent's update, or on a timed-out helper's leftover process.
- A value you set, such as `ANTHROPIC_MODEL=x claude`, beats the profile's again, and pi's
  subagents start on your profile's model.
- `yolo check`'s Nix advice fits Determinate and upstream Nix, and `check`, `check-deps`,
  `capture` and `host apply`'s wrappers step name the fix for each problem.
- Rootless Podman on stock Ubuntu 26.04 works again, a Claude login on a Mac no longer needs
  repeating after it refreshes, and git on macos-user no longer refuses your project for "dubious
  ownership".
- A project can't reach your machine through a loophole path with a space or a `.json` project
  config, and `workspace_readonly` locks `yolo-jail.json`.
- A git pack can't copy your machine's files into the jail through a symlink in its address.
- The "runs pack code on your machine" list skips switched-off loopholes.
- `yolo host apply` no longer empties Claude Code's `permissions.additionalDirectories`: add your
  folders back once.
- `--network` overrides the project's `network.mode`.
- `yolo pack update --help` no longer runs the update, and neither it nor `yolo host apply`
  reports a failed run as a success.
- A Ctrl-C while a podman or Apple Container launch builds or waits at a prompt no longer leaves
  your tab in the jail's colors.

### Contributors

Thanks to Kurt Galiatsatos ([@kurt-hs](https://github.com/kurt-hs)) for herdr support (above), and
for fixes to tests that failed on a Mac with Homebrew or in a full parallel run.

## [0.11.0] - 2026-09-28

Claude can use Bedrock through your AWS SSO login, `yolo update` keeps yolo current, and a
provider's key now reaches only the agent that selected it.

### Added

**`yolo update` keeps yolo current.** It updates a Homebrew or from-source install and tells any
other install what to run. A daily check prints a line when a release is waiting;
`"update_check": false` in your user config stops it. See
[Self-update](docs/reference/self-update.md).

**Bedrock from your SSO login.** The `aws-auth` pack turns a host `aws sso login` into a
short-lived credential for a role you name, so the jail holds no AWS key. Set it up from
[its README](packs/aws-auth/README.md), then run `yolo -p bedrock -- claude`.

**Repository skills reach every agent.** Skills a repository commits for one agent, such as
`.claude/skills/`, now reach every agent in the jail. See
[the workspace layer](docs/reference/agent-briefings.md#the-workspace-layer).

**An agent footer.** Each agent's status line shows who is billed and whether it runs in a jail
or on your host.

**Lower disk priority.** On Linux, `"resources": {"io": "low"}` or `"idle"` lowers a podman
jail's disk priority where the disk's scheduler honors it, so builds stop stalling your desktop.
See [resources per setup](userguide/reference/settings-per-setup.md#resources-devices-and-networking).

**A user guide.** Read it at [docs.yolo-jail.mschulkind.dev](https://docs.yolo-jail.mschulkind.dev).

### Changed

- **A provider's key reaches only the agent that selected it**, and a variable set on an agent's
  command line no longer overrides its profile. Give a script a key with
  `yolo host --with-credentials <provider> -- <command>`. See
  [the credential gate](docs/reference/providers.md#the-credential-gate).
- **Host wrappers sync your host config without asking**, and are on whenever `host_management`
  is `"own"`. Opt out in your user config with `"host_apply_on_launch": false` or
  `"host_wrappers": false`.
- **Pack briefings come only from `briefing/`.** Move a pack's root briefing file to
  `briefing/<pack>.md`.
- **`lsp_servers` installs nothing.** Install language servers through `mise_tools` or `packages`.
- **Git packs are fetched at launch**, so a branch pin follows its branch. Pin a tag or commit.
- **A running jail keeps the packs it started with**, and refuses a profile it cannot serve unless
  you confirm a restart. `yolo stop`, then launch, to pick up a change.
- **The `codex` profiles offer GPT-6, not GPT-5.** Add a model under
  `providers.openai-codex.models` to keep it.
- **Refused, each naming what to do instead:** a provider's bare `base_url` (write
  `endpoints.<protocol>.base_url`), the `claude_plugins` hook, `yolo host wrappers enable|disable`,
  `yolo host apply --shell-init`, and two `host_files` entries for one destination. An unknown flag
  exits 2 instead of being ignored.
- A launch whose agent cannot speak its provider's protocol is refused, and `yolo check` predicts
  it.
- `writable_home_dirs` and `host_files` refuse every selected pack's directories, shipped or not,
  and no others.
- Ctrl-C interrupts what runs in the jail instead of ending your session.
- On macos-user, an agent can no longer read other processes' command lines or the system
  keychains, and may use `ioctl` only on terminals.

### Fixed

- **Security:** any process on your machine could use yolo's ChatGPT sign-in, Claude login
  refresh or wire bridge when a jail shared your host's network, on macos-user, or under
  `yolo host`. Each now requires a credential yolo gives your agents.
- **Security:** an agent could make the next launch or `yolo prune` write outside the jail through
  a symbolic link, or on macos-user rewrite the skills and briefing yolo delivers.
- **Security:** a provider key could be sent to `api.anthropic.com`, a new workspace's Claude login
  carried other workspaces' projects and MCP servers, and Apple Container older than 1.1.0 mounted
  a `host_files` directory writable.
- On macos-user, every launch with the pi, omp, agy or opencode pack deleted that agent's settings,
  sign-in and sessions.
- `nix-collect-garbage` could delete the tools of a Linux jail running for over a week.
- On a Mac with Podman, a folder your Podman machine does not share made a jail fail with a bare
  `statfs` error. yolo now names the folder.
- Claude Code could stop with `api_request_oauth_refresh_exhausted`.
- `yolo host apply --assert` dropped your own `env` variables from `~/.claude/settings.json`, and
  took over `~/.claude/skills/synced/`, so Claude Code's next sync lost your edits.

### Contributors

Thanks to Kurt Galiatsatos ([@kurt-hs](https://github.com/kurt-hs)) for `yolo update`.

## 0.10.x

_0.10.0, September 2026._

Agents can use local and gateway models. The `llamacpp` pack points them at a llama.cpp server on
your host, `kilo` and `openrouter` add gateway providers, and `zai` gained the Z.ai coding plan. A
provider on your host's loopback is reachable from the jail.

### Changed

- A launch from your home directory, `~/.config/yolo-jail` or `~/.local/share/yolo-jail`, or from
  a directory containing one, is refused. Launch from a project directory.
- A launch whose `required_capabilities` no selected pack satisfies is refused, and `yolo check`
  predicts it.

## 0.9.x

_0.9.0, September 2026._

`yolo host` runs an agent on your own machine, and `yolo host apply` writes your packs' config,
skills and briefing there. Providers and profiles arrived, picked with `-p`.

### Changed

- Run `yolo broker restart` once after upgrading, or Claude's token refreshes fail.
- Host services, audio included, are packs you select and enable. Move the top-level `journal` and
  `host_processes` keys under `loopholes.journal` and `loopholes.host-processes`.
- yolo updates agent CLIs as you run them, at most hourly; `agent_updates: false` freezes them.
- `grep -r` and `find` are no longer blocked; the `guardrails` pack blocks them.
- `yolo host` reads only your user config. Move host-launch `env_sources` and `providers` there.
- The jail keeps its own `node_modules`; install packages there too.
- Without a terminal, a changed workspace config refuses the launch; pass
  `--accept-config-changes` in CI.
- `yolo run --new` and `yolo apply --host` are refused; use `yolo stop` and `yolo host apply`.

## 0.8.x

_0.8.0, August 2026._

Agents became packs. The `packs` key selects them by name, and an empty config gives a jail with no
coding agent. `yolo pack` creates, lints and installs packs, including git-hosted ones. Google's
Antigravity CLI (`agy`) arrived, and `host_files` brings named host files into the jail.

### Changed

- The `agents` key is refused. List agents in `packs` instead.
- Your git identity comes from your host and is read-only in the jail. Set it on the host.
- The Gemini CLI agent was removed.

### Contributors

Thanks to Dong ([@liudonggalaxy](https://github.com/liudonggalaxy)) for faster mise workspace
trust and a non-fatal optional install, Georgi Popov
([@georgipopovhs](https://github.com/georgipopovhs)) for an image reload fix, and Svet
([@neykov](https://github.com/neykov)) for macOS Podman checks in `yolo check`.

## 0.7.x

_0.7.0 – 0.7.1, July 2026._

yolo stopped being a Python package and became one Go binary. Install it with `brew install` from
the project's tap, a platform archive from a release, or the PyPI wheel, which now wraps the Go
binary. On a Mac, the jail image builds through a published Linux builder container, with no Linux
machine to set up.

## 0.6.x

_0.6.0, July 2026._

OpenAI Codex joined the agents you can choose from. The `macos-user` backend arrived: a dedicated
macOS account confined by Seatbelt, with no VM.

## 0.5.x

_0.5.0, July 2026._

AMD GPUs pass through with ROCm, and video decoding with `gpu.vaapi`. `yolo-jail.local.jsonc`
layers untracked overrides over a workspace config, MCP servers take their own `env`, and the audio
loophole bridges PipeWire and ALSA as well as PulseAudio. Ctrl-Z in a jail drops you back to the
host shell.

### Changed

- `env_sources`, an ordered list of files and values, replaced the `env` key. Move your variables
  there.
- Docker is no longer a runtime. Use podman or Apple Container.
- Language servers are opt-in. List the ones you want in `lsp_servers`.

### Contributors

Thanks to Kurt Galiatsatos ([@kurt-hs](https://github.com/kurt-hs)) for macOS Podman image builds
that need less from a Linux builder, and for keeping agents in a jail from modifying yolo's own
code.

## 0.4.x

_0.4.0 – 0.4.3, April to May 2026._

macOS became a supported host, with Podman Machine, Apple Container or Docker, which 0.5 removed.
Loopholes arrived, each a host service a jail may use, such as the Claude OAuth broker, which
serializes token refreshes across jails. `yolo prune` reclaims disk, dry-run by default, and
`kvm: true` passes `/dev/kvm` through.

### Contributors

Thanks to Toan Vuong ([@Toad2186](https://github.com/Toad2186)) for an Apple Container fix and the
NixOS builder documentation, and to Georgi Yanchev
([@georgi-yanchev](https://github.com/georgi-yanchev)) for Apple Container startup and `yolo check`
fixes.

## 0.3.x

_0.3.0 – 0.3.1, April 2026._

Containers got readable names, and a jail can run inside another jail. Jails starting at once
stopped racing on shared state, and Claude's login works in a jail with a read-only home.

## 0.2.x

_0.2.0, April 2026._

NVIDIA GPUs pass through to the jail, and `yolo check` verifies the driver setup. `resources` sets
a jail's memory, CPU and process limits. Claude Code is supported, with a separate history per jail.

## 0.1.x

_0.1.0, March 2026._

The first public release: a container jail in which Copilot and Gemini run in YOLO mode with no
access to your SSH keys, git identity or cloud credentials, configured by
[`yolo-jail.jsonc`](yolo-jail.jsonc). Blocked tools explain themselves and suggest what to use
instead.
