# Changelog

What changed in each release of YOLO Jail, newest first.

Releases before 0.11.0 are summarized one section per minor line, such as `0.10.x`, rather than
one per patch release.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.11.1] - 2026-10-01

Agent updates no longer hang a launch, `yolo check` gives the right Nix advice and names the fix
for each problem, and `yolo host` runs yolo's own copy of your agents.

### Added

**Your agents at the host.** `yolo host -- <agent>` runs yolo's own, current copy where it can,
whatever launcher starts it (`"host_floor": false` opts out), and `yolo host apply` writes your
MCP servers, providers and model into its config. See
[yolo's own copy of your agents](userguide/guides/confinement.md#yolos-own-copy-of-your-agents).

**`gh` without a token.** The `github` pack's `github-broker` loophole runs `gh` for the jail
through your own login, read-only and only on the project's own repositories. Turn it on per
project with `yolo loopholes enable github-broker`. See [GitHub](userguide/guides/github.md).

**Per-project loophole switches.** `yolo loopholes enable <name>` and `disable` turn a loophole on
or off for one project, without editing your config. See
[Host access and loopholes](userguide/guides/loopholes.md#the-loopholes-yolo-ships).

**More agents, more providers.** `-p bedrock` sets up codex, opencode and pi; `-p codex` runs
opencode on ChatGPT; pi, opencode and oh-omp mix providers (`-p pi=zai,openrouter`). Bedrock and
wire-bridge profiles also work on macos-user.

**Choose the models each provider offers.** A pack's `models` entry adds to a provider's model
list or narrows it, and agents that allow it offer only that list.

**Work that survives a restart.** Every jail has `$YOLO_DURABLE_DIR`, a folder yolo never deletes,
and agents keep their worktrees there. A user-config `mounts` entry with `"mode": "rw"` is writable.

**herdr shows a jailed agent as that agent.** In a [herdr](https://herdr.dev) pane,
`yolo -- <agent>` registers the agent, so herdr's sidebar shows it working, blocked or done, and a
split pane's border reads `🔒 JAIL <project>`. `YOLO_NO_HERDR=1` turns it off. Contributed by
Kurt Galiatsatos ([@kurt-hs](https://github.com/kurt-hs)).

**Your own fork of an agent.** A small pack names your fork and its build command; yolo builds the
pinned commit once for every workspace. See
[Run your own fork](userguide/guides/packs-and-skills.md#run-your-own-fork-of-a-program).

### Changed

- Each terminal's agent ends with its terminal, and a jail with its last terminal or `yolo stop`.
- `use_profiles` is now `profile`; the refusal shows the rewrite. `-p pi=codex` now beats a bare
  `-p bedrock` in either order.
- Later packs in `packs` now win: list yours after the shipped pack it overrides.
- Keys go only to agents: `yolo host -p` refuses other commands (use `--with-credentials`), and a
  jail's shell gets no AWS keys or `aws-auth` credentials.
- A pi, opencode or Codex key only in your shell stops a jail launch: put it in `env_sources`.
- A project config may not set a provider's `api_key_env_name` or `endpoints`, or remove a
  provider: use your user config.
- A Bedrock launch with no region is refused: set `providers.bedrock.region`, `AWS_REGION` in
  `env_sources`, or a region in your `~/.aws/config` profile.
- Two packs shipping one skill name stop the launch (rename one, or set `"skills_tier":
  "namespaced"`), as does a `supersedes` nothing serves.
- `install_hints` may hold only package names and one `&& <command>`.
- pi reads yolo's MCP servers itself: remove `pi-mcp-adapter` if you added it for them.
- GPT-6.1 Sol replaces GPT-6 Sol on `-p codex`; pick again if you chose it.
- On a provider that lists models, such as z.ai, Claude Code's menu and tiers now use them.
- A profile list for Claude Code, Codex or Copilot is refused; profile names may not hold commas.
- `yolo host -- codex` refuses `codex agents`: yolo turns Codex's background copy off, in jails
  too.
- Two mounts at one path fail `yolo check`; on macos-user, a mount it cannot share stops the
  launch.
- pi's subagents start on your profile's model and stay on its provider.
- `yolo pack ls`, `install`, `update` and `status` refuse an argument: run them bare, as each
  covers every configured pack.

### Fixed

- An agent's update could hang and ignore Ctrl-C; now it stops, and the agent starts on the
  version you had.
- A value you set yourself, such as `ANTHROPIC_MODEL=x claude`, beats the profile's again.
- `yolo check`'s Nix advice fits Determinate or upstream Nix and keeps the users your daemon
  trusts.
- `yolo check`, `yolo check-deps`, `yolo capture` and `yolo host apply`'s wrappers step now name
  the command that fixes each problem they report.
- Rootless Podman on stock Ubuntu 26.04 no longer fails every launch.
- A Claude login on a Mac or Apple Container no longer needs repeating after a refresh.
- On macos-user, git no longer refuses your project for "dubious ownership".
- Three ways a project could reach your machine are refused: a loophole program in a project path
  with a space, a loophole installed from a `.json` project config, and a git pack address through
  a symlink.
- A project's `yolo-jail.json` is now locked by `workspace_readonly`, and neither `yolo init` nor
  the agent's briefing creates a second config file that replaces it.
- A launch's "runs pack code on your machine" list no longer names loopholes that are switched off.
- `yolo host apply` no longer empties Claude Code's `permissions.additionalDirectories`; add your
  folders back once.
- `--network` now overrides the project's `network.mode`.
- `yolo pack update --help` ran the update, and `update` reported success for an install that left
  nothing to run; `yolo host apply` could call a failed run, or a home still holding a dropped
  pack's files, up to date.
- A Ctrl-C during a launch's build or prompt no longer leaves your tab in the jail's colors.

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
