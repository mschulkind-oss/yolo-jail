# Changelog

What changed in each release of YOLO Jail, newest first.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

**Every jail now has a place for work that survives a restart: `$YOLO_DURABLE_DIR`.** Each launch
makes a directory inside the workspace's own `.yolo` folder and tells every process in the jail
where it is, on podman, Apple Container and macos-user alike, and every agent's briefing now opens
its storage section with it: worktrees, clones, drafts and measurements go there, while `/tmp` is
emptied when the jail exits and the directories in the home belong to the agents and tools
themselves. It is ignored by git with the rest of `.yolo`, and yolo never deletes anything in it.
Instead, each launch prints how many worktrees it holds, their total size and how long the oldest
has been idle, and a second line when worktrees in the workspace or in the jail's `/tmp` are
registered at directories that no longer exist. `yolo check` lists each worktree with its size,
idle time, branch, the commits that exist nowhere else and its changed files, and `yolo stores`
shows the directory of each workspace whose jail is running. pi's
subagent worktrees now go there instead of `/tmp`. If the directory cannot be made, for example
because `.yolo` is a symbolic link, the launch says why and goes on without it. Agents run with
`yolo host` are told that the machine's `/tmp` may not survive a reboot. See
[where work survives a restart](docs/design/durable-scratch-space.md).

**`yolo host -p codex -- claude` now runs Claude Code on your ChatGPT subscription, as it does in
a jail.** yolo starts the wire bridge for that one command, lets only that `claude` use it, and
stops it when `claude` exits. The same goes for other profiles that need the bridge, such as
`-p cerebras -- claude`, and for a `use_profiles` selection through `yolo host -- claude` and the
host wrappers. The `macos-user` backend now runs the bridge the same way for each launch that
needs it, so those profiles work there too instead of being refused. Because the bridge runs only
for the command that starts it, `yolo host env` refuses such a profile and names the command that
works, and `yolo host apply` writes no bridge address into your files and says so; a `claude` you
start some other way runs on its own login. See
[the wire bridge](userguide/guides/providers-and-models.md#the-wire-bridge).

**On the `macos-user` backend, AWS Bedrock through `aws-auth` and the ChatGPT-subscription
refresh service now work.** The small helpers these services need beside the agent, which used
to run only inside a container, now run inside the macOS sandbox, confined by the same sandbox
profile as the agent and as the sandbox's own user. So `yolo -p bedrock -- claude` gets its
narrowed AWS credentials there, and `codex` keeps its ChatGPT login through a long session
instead of losing it at the first token refresh. Each helper listens on a port the launch picks,
so two sandboxes running at once do not collide, and the launch says which helpers it started and
names, with the reason, the ones it cannot run there, such as the one that shares one Claude login
between sandboxes. It says the helpers started only once they confirm it; if they fail to start,
the launch stops and shows where their log is and its last lines. Homebrew and the release
archives ship the helpers for both Apple Silicon and Intel Macs; nothing new lands on your own
`PATH`. See [macOS](userguide/guides/macos.md).

pi's subagents can now use your MCP servers. When the pi-subagents extension is in pi's
packages, a jail also writes the MCP servers you configure to `~/.config/mcp/mcp.json`, the file
pi-subagents reads when an agent lists `mcp:` tools; before, subagents could not see them. A file
you already keep there is merged into rather than replaced, so its servers and settings stay, and
so do the servers you add to it later. Without the extension, nothing is written there, and
`yolo host apply` never writes it. See [MCP configuration](docs/reference/mcp-configuration.md).

**`yolo host apply` now writes your MCP servers, LSP servers, providers and selected model into
your host agents' config, as jails do.** The servers under `mcp_servers` and `lsp_servers`, the
providers your packs and `providers` declare, and the model `use_profiles` selects reach each
agent's files on your own machine, and so does the automatic apply a wrapped launch runs. Host pi
now offers the same `openai-codex` models a jail's pi does. As in a jail, yolo writes an agent's
server and provider lists whole, so an entry you added through the agent itself is dropped unless
your config declares it; each is named, and yolo asks before dropping one the first time it writes
a list. The selected model is written when your selection changes, and a model you pick later in
the agent stays. MCP presets and servers whose command is a path that exists only inside a jail
are not written to your machine, and the report names each one. Under `host_management: own`,
the files yolo writes whole (pi's, Copilot's and Antigravity's MCP files, Copilot's LSP file, and
pi's and oh-omp's model files) are now written too, keeping what they already hold, where they
used to be refused. See [what a host apply writes](docs/design/host-computed-layer.md#14-what-was-built).

**A pack can set another pack's settings for one confinement only.** An `autonomy` posture's
`config` can now name a config file another pack owns, and its keys apply only where that
posture does: under `guarded`, a setting only your host gets through `yolo host apply`, and
under `autonomous`, one only jails get. A personal or company pack can give host pi a setting
without editing pi's pack. The owning pack still wins a key you both set, `yolo pack footprint`
names the keys beside the posture, and a setting the posture stops selecting leaves your home as
any other pack's key does. Before, such a patch did nothing and yolo said it was folded nowhere.
See [the `autonomy` kind](docs/reference/pack-system.md#autonomy).

**`frontier` is a model alias name yolo recognizes**, beside `default`, `fast` and `balanced`:
the provider's most capable model. Name it in a provider's `models` so that a pack reading it,
or an adapter mapping yolo's tiers onto an extension's own, finds your top model. Like the other
three it is a convention, never a requirement: when a pack asks for one your provider does not
name, the launch prints a warning naming the provider and the alias, and starts anyway.

**`host_files` entries with inline content now reach your host config too.** An entry in your
user config that gives its file's `content`, `defaults` or `managed` keys, rather than a
`source`, is written into your own home by `yolo host apply`, under your `host_management`
setting, as it is written into every jail. Keys you added to that file yourself stay. Remove the
entry and the next apply takes back only the keys it wrote, naming each one, and
`yolo host apply --revert` takes them back too. An entry with a `source` copies a file of yours
into a jail, so at the host it does nothing, and the report says so, as it does for
`mise_tools`.

**`yolo claude-auth` manages this machine's Claude login, as `yolo openai-auth` does for OpenAI.**
`yolo claude-auth logout` signs the whole machine out: every workspace and every jail lose the
login until someone runs `/login` in a jail again. `yolo claude-auth status` shows the machine's
login, and `yolo claude-auth inspect <file>` describes one credentials file; both show field
names, expiry times and short fingerprints, never a token. `yolo claude-auth refresh` renews the
login now. A way for jails to share the login without intercepting its renewal is built but off
until it has been measured; see
[Claude login without interception](docs/design/claude-login-without-interception.md).

### Changed

- GPT-6.1 Sol is now the default model for every agent on the ChatGPT subscription (`-p codex`) and the first entry in its model menus. GPT-6 Sol stays in the menu, listed last. A choice you made yourself is kept.

**A jail that shares your host's network now says so, at launch and in its briefing.** With
`network.mode: "host"`, in a jail started from inside another jail, and in every macos-user
jail, services listening on your machine's loopback are reachable from inside the jail. The
launch prints one line saying so and that the agent still runs without permission prompts,
because what confines it is the filesystem boundary, and the agent's briefing states the same
fact. Nothing about the jail's permissions changes.

**pi's subagents now start on your profile's default model and stay on your provider, for
every provider.** Before, only the `codex` profile set this. On any other profile a child agent
could name a model of any provider, and a pi-subagents default left over from elsewhere, such as
your host's settings, could start it on another provider's model. Now every profile sets the
pi-subagents extension's default model to the model pi itself starts on, and allows a child
only the models you configured for that provider, or any of the provider's models when you
configured none. A child can still ask for another model of the same provider. When your
provider lists models but has no `default` alias and your profile names no model, the launch
says so, and children start on their parent's model.

**The AWS Bedrock credential service in a jail now runs only for a profile that uses it, and
only that agent can use it.** With the `aws-auth` loophole on, the in-jail service starts only
when some agent's profile is `bedrock`, and a launch that leaves it off says so. It answers only
the agents on `bedrock`: another agent, or a plain shell, is refused. Attaching with
`-p <agent>=bedrock` to a jail that was started without it asks you to restart the jail rather
than hand the agent an address nothing answers. See
[the `aws-auth` pack](packs/aws-auth/README.md).

**Two packs shipping a skill of the same name now stop a jail launch with a message, as they do
at the host.** Before, a jail came up with whichever pack came last and said nothing, so one
pack's skill was silently missing; your own local pack's copy of a shared pack's skill is now
the same conflict. The message names the agent's skills directory, both packs and where each
copy lives, and the two ways out: rename one, or give one pack `"skills_tier": "namespaced"`.
It appears before the jail starts, and when you enter a running one too. **A namespaced pack's
skills keep their `pack:` prefix in jails too**, invoked as `/<pack>:<skill>` as they are on
your host, where a jail used to drop the prefix. A pack that wraps an agent plugin reaches a
jail as it reaches your host: whole when the pack is namespaced, and otherwise its skills only,
with the launch naming each part that cannot come along. A tree an agent pack reserves, such as
the claude.ai skills Claude Code syncs, is never copied into a jail's skills directory, and the
launch says when it held one back. See [the collision rule](docs/reference/pack-system.md#skills-collision).

**Packs now take precedence in the order your config lists them, in a jail and at the host
alike.** Where two packs set the same setting, the later one wins, and "later" now means one
thing everywhere: the order of your `packs` list, then the packs another pack pulls in through
its `needs`, then your personal pack in `~/.config/yolo-jail/local`, always last. Before, a jail
put the packs yolo ships ahead of all others, so your own pack beat a shipped one wherever you
listed it, while `yolo host apply` followed your list and a jail's startup read the packs in
alphabetical order. **If your pack overrides a shipped one, list it after that pack**, as in
`"packs": ["claude", "~/dotfiles/packs/mine"]`; listed before it, the shipped pack's values now
win. The same order decides a claim only one pack may hold, such as an adapter for one
conversion, a provider name, a profile name or a service name: when two packs declare it, the
later one's is used, where yolo used to keep the first. So an adapter your personal pack declares
now beats one a pack pulled in through `needs` declares for the same conversion. `yolo
config-ref` describes the order under `packs`.

**`yolo host -p` gives keys only to agent CLIs, as `-p` does in a jail.** `yolo host -p zai --
pi` still runs pi on zai, but `yolo host -p zai -- curl` no longer hands curl zai's key: it is
refused, and the refusal names `yolo host --with-credentials zai -- curl`, which is how to hand a
provider's key to any other command. `eval "$(yolo host env --with-credentials zai)"` does the
same for your shell, and the lines that tell you a key was withheld now suggest these spellings.
See [the credential gate](docs/reference/providers.md#the-credential-gate).

**A project's config can no longer change which variable a provider's key comes from.** A
provider's `api_key_env_name` in a project's workspace config is now refused, as its address
already was, and so is anything else there that decides where a
provider's key goes: any change to its `endpoints`, and removing a provider with `null`. Each
refusal names the field; move it to your user config. A project can still set a provider's
`models`, `options`, `region` and `capabilities`. See
[settings per setup](userguide/reference/settings-per-setup.md).

### Fixed

- Rootless Podman with no storage.conf of your own, as on stock Ubuntu 26.04, no longer fails
  every launch while delivering the image with `mkdir /run/containers: permission denied`. The
  image now goes into the store Podman itself reports, whatever your storage.conf files say, and
  the launch prints that store on its `Image store:` line. `yolo check` prints it too, and warns
  when Podman does not report one. You no longer need a `~/.config/containers/storage.conf` to
  work around this.
- An agent's briefing no longer calls its home "persistent across sessions". In a podman jail
  most of the home is read-only, so agents that believed it wrote their worktrees to `/tmp`,
  which is deleted once the jail exits. The briefing now lists the jail's storage classes: what
  is gone at every restart, what lasts for this workspace, what every workspace on the machine
  shares, and the workspace itself, with what each survives and what yolo cleans up. It tells
  the agent to keep nothing it needs after a restart under `/tmp`. Apple Container jails get the
  same section.
- A value you set on the command line beats a profile's value again. `ANTHROPIC_MODEL=x claude`,
  or an `export` in the jail's shell, had been overridden by what the selected profile sets for
  that agent; your value now wins, and the profile's still replaces one yolo set itself.
- On `macos-user`, an agent you start from the sandbox's shell, after a bare `yolo`, gets its
  profile's settings and keys. It used to start with none of them and reach its default
  provider.
- On `macos-user`, git inside the sandbox works on your project. The agent runs as the
  sandbox's own user, which does not own your project folder, so git refused it with
  "detected dubious ownership" and the agent's first git command failed. The sandbox's git now
  trusts that one folder, and no other.
- On `macos-user`, a project inside your home folder no longer sends you in a circle. The launch
  said to run `yolo macos-fix-permissions`, which refuses every folder in a home, so running it
  changed nothing. The launch now says first that the project has to move out of your home, and
  prints the commands that move it under `/Users/Shared/yolo` and share it there.
- `yolo check` reported a working Nix as "found but not working: probe failed" when
  `nix --version` took longer than five seconds, as a first run inside a jail on a busy Mac can.
  It now waits as long as its other Nix checks, and says whether Nix timed out, could not be
  run or exited with an error.
- `yolo check` no longer fails when the Claude login broker has stopped and left its PID file
  behind, as it does whenever its state directory is removed. It now warns that the broker is not
  running, like it does when none was ever started, because the next launch that selects `claude`
  starts a fresh one.
- A jail no longer deletes pi's `mcp.json` every time it starts. 0.11.0 removed it to clean up
  the copy older versions of yolo wrote there, and so also deleted the one you or a pi
  extension keep, such as the MCP servers a subagent's `mcp:` tools are looked up in. yolo's
  own MCP servers stay in `mcp-adapter.json`. yolo still removes the copy an older version of
  yolo wrote, but only while it holds exactly the MCP servers yolo writes today, and leaves any
  other `mcp.json` as it is.
- Starting pi on the `codex` profile while another pi session was refreshing its OpenAI login
  could lose the refreshed login: yolo broke pi's lock on its credential file after 10 seconds,
  and the two wrote at once. yolo now follows pi's own rule, treating the lock as abandoned only
  after 30 seconds without a refresh, and waits for pi to finish. If pi still holds the lock after
  about half a minute, yolo says that a running pi holds it and starts pi with its credential file
  as it is.
- Starting pi on the `codex` profile while another pi session was refreshing its OpenAI login no
  longer sends you to a browser login. yolo could not update pi's credential file while pi held
  it, and treated that as a missing login: at a terminal it started a login you did not need, and
  without one it said a login was required. yolo now says that a running pi holds the file, leaves
  it as it is, and starts pi, since that session is keeping the login current.
- Old Codex versions now get cleaned up the way the other agents' are. After Codex installs or
  updates, yolo keeps the two newest versions and the one in use, and removes the rest. Before,
  every old Codex version stayed on disk, so each update left a few hundred megabytes behind.
- A repository whose skills link one directory many times can no longer make a launch copy far
  more than the repository holds. One launch now copies at most 32 MiB and 4096 files and
  directories of a repository's skills. A skill that would go past that is left out and named at
  launch, and the skills copied before it still reach your agents.
- A pack of your own that a dotfile manager (rcm, stow, chezmoi) deploys as symlinks into your
  dotfiles now works in a jail, as it already did with `yolo host apply`. A jail refused to start
  over a link pointing out of the pack; it now follows the links and gets the files they point to,
  whether or not the pack's entry filters it with `only` or `exclude`. A pack fetched from git
  that holds such a link is still refused, in a jail and at the host.
- When your pi's catalog lacks some of the ChatGPT subscription models yolo lists, the warning pi
  shows now tells you why and what to do. Along with the missing models and what they lose, it
  names the pi you are running, for example `Your pi (0.85.1) predates these models`, and says
  that `pi update` fixes it. If yolo cannot read pi's version, the warning says your pi may
  predate them. Before, it named only the missing models.

## [0.11.0] - 2026-09-28

Claude can use Bedrock through your AWS SSO login, and a repository's skills now reach every
agent in the jail. Each agent shows a footer naming where it runs and who is billed. A
provider's key now reaches only the agent that selected it. Read the first items under Changed
before you upgrade from 0.10.0: some commands and config shapes are now refused, and a few
defaults changed.

### Added

**`yolo update` keeps yolo current.** `yolo update --check` tells you whether a newer yolo is
out, and `yolo update` installs it the way you installed yolo. A Homebrew install runs
`brew upgrade yolo-jail` and keeps the previous version, so jails already running keep working
until you restart them. A from-source install pulls the clone yolo was built from and runs
`just deploy` there. A release archive gets the download link, and a `go install`, pipx or uvx
install is told to update the binary and the clone `YOLO_REPO_ROOT` names together. yolo also
checks GitHub for a new release once a day, tells you the first time before it checks, and prints
one line when a release is waiting; a from-source install is checked only when you run
`yolo update`, and `yolo check` shows the last answer. When a Homebrew or from-source update is
waiting and you start a jail at a terminal, yolo offers to install it and start your command
again. `"update_check": false` in your user config turns the daily check, the line and the offer
off. See [Self-update](docs/reference/self-update.md). Contributed by Kurt Galiatsatos.

**Bedrock from your SSO login.** The new `aws-auth` pack, which the claude pack brings along,
turns a host `aws sso login` into a short-lived credential for an AWS role you name. The jail
holds no key and no `~/.aws`. Enable `loopholes.aws-auth` in your user config with a `profile`
and a `role_arn`, as [the pack's README](packs/aws-auth/README.md) shows, then run
`yolo -p bedrock -- claude`. Changing those settings takes effect at your next launch, which
restarts the shared service and says so, and `yolo check` tells you when the running service
still has the old ones. The credential is served only to the agents of the launch that asked,
even in a jail that shares your host's network. Where the service does not run (macos-user,
`yolo host`, or a jail that has not enabled it), the launch leaves the Bedrock settings out and
says so.

**A repository's skills reach every agent in the jail.** Skills a repository commits for one
agent, such as `.claude/skills/`, `.agents/skills/` or `.github/skills/`, now also reach the
agents that do not read that directory, in containers and on macos-user. An agent that reads the
directory itself gets no second copy. A repository skill never replaces yolo's own skills or a
pack's, and the launch names each one it held back. It also names any symbolic link that points
outside the repository and never reads through one. A path too deep to deliver, or a file that
cannot be copied, is named and skipped rather than stopping the launch, and nothing is written
into the repository. See [the workspace layer](docs/reference/agent-briefings.md#the-workspace-layer).

**An agent footer.** Every agent that has a status line shows who is billed for it (your
subscription, or the provider its profile selected) and whether it runs in a jail or on your
host, unless you set your own `statusLine`. Claude's footer also shows its thinking level.

**Agents and providers are checked against each other.** A launch whose agent cannot speak any
protocol its provider serves is refused, and the refusal names any shipped pack that translates
between them. `yolo check` predicts the refusal. On your own machine, `yolo host` refuses a
profile its agent could reach only through the jail's wire bridge (the in-jail service that
translates between Claude's protocol and OpenAI-style providers), and names the jail launch that
works. A macos-user launch refuses the same profile, where it used to start the agent pointed at
an address nothing served. A profile whose provider the launch does not have, such as one a
`null` in your `providers` removes, is refused with the reason, where the agent used to start
without any of its settings. See [Providers](docs/reference/providers.md).

**Commands for yolo's host services.** `yolo host-daemon status` lists every machine-wide service
yolo runs and whether each is healthy, and `yolo host-daemon stop|restart|logs <name>` manages
one. `yolo broker` still works and means the Claude login service. `yolo openai-auth status`,
`import` and `logout` manage the one ChatGPT login every workspace and jail on the machine
shares. It was hidden under `yolo internal openai-auth`, which still works and tells you the new
name.

**A jail's builds can yield the disk.** Set `"resources": {"io": "low"}`, or `"idle"`, and every
process in a podman jail on Linux runs at a lower disk priority, so a build stops stalling your
desktop when both want the disk. Disks whose I/O scheduler is bfq honor both values and
mq-deadline honors `"idle"`. kyber and none, the usual NVMe default, ignore it: the launch says so
when the disk under your workspace is one of those, and `yolo check` names the host change that
fixes it. The priority does not reach buffered writes or a `nix build`, which runs in the host's
Nix daemon. Apple Container and podman on macOS cannot apply it, and the launch says that too. See
[resources per setup](userguide/reference/settings-per-setup.md#resources-devices-and-networking).

**Slow launch steps show their progress.** Loading the image after `podman image prune` or an
upgrade shows a live line with how many layers and gigabytes it has copied, as it did before
0.10.0. The image build, the first build of yolo's image copier, loading the image on Apple
Container and podman on macOS, a wait behind another launch, a pack update, the macOS native build
and a first-boot `mise install` show one too. A step that finishes within two seconds prints
nothing new.

**Nix on macos-user.** The sandbox finds your host's `nix` on its PATH, talking to the host's Nix
daemon, with `nix-command` and flakes enabled. A single-user Nix install has no daemon to talk to,
so the sandbox gets no `nix` and the launch says why. In a container jail, `nix shell` and
`nix build` now work with no extra flags.

**Route an agent through the wire bridge.** A profile can name `"via": "wire-bridge"` to send pi,
omp, opencode or codex traffic through the bridge, which adds the provider's key itself, so
the agent holds only a per-launch secret for the bridge. Selecting such a profile brings the
bridge pack into the jail, and the launch refuses a route the bridge cannot serve. See
[routing a profile through the bridge](docs/reference/providers.md#routing-a-profile-through-the-bridge-via).

**Pack authors can add to another pack's config.** A `config-list` contribution adds entries to
an array in a config file another pack owns, without copying the file. An `autonomy` posture can
carry the same `lists`, added only at that posture: a `guarded` list reaches your host through
`yolo host apply` and no jail, so a pack can give host pi a permission-prompt extension that would
only cost tokens in a jail. See
[adding entries to an array](docs/reference/pack-system.md#adding-entries-to-an-array-config-list)
and [posture lists](docs/reference/pack-system.md#autonomy).

**A user guide.** Installing, configuring and troubleshooting yolo is written up as a guide,
published at [docs.yolo-jail.mschulkind.dev](https://docs.yolo-jail.mschulkind.dev).

- yolo honors `NO_COLOR`, and a jail launched with it set shows no color either.
- `yolo config reset` on your host discards the config edits an agent made inside a jail that is
  no longer running. While that jail runs, it refuses and names the command to run inside it.
- `yolo check` warns when a program your packs install has no host wrapper, or when another
  program of the same name comes earlier on your PATH, and names the fix.

### Changed

The first items here need your attention when you upgrade from 0.10.0.

- **A provider's key reaches only the agent that selected it.** An `env_sources` value that a
  provider claims as its key, and the settings a profile composes, used to reach every process in
  the jail, so pi could use Bedrock because Claude's profile had the keys. Now only the agent whose
  profile selected that provider gets them, and a plain shell sees only the `env_sources` values no
  provider claims. opencode's menu offers only the provider its profile selected. A variable you
  set on an agent's command line no longer overrides what its profile composes. If a script needs
  a provider's key, run it on your host with
  `yolo host --with-credentials <provider>,<provider> -- <command>` (or `all`), which hands that
  one run just those keys without switching any profile;
  `eval "$(yolo host env --with-credentials all)"` puts them in your shell, and
  `yolo host -p <profile> -- <command>` hands any command one profile's keys. A jail launch
  refuses `--with-credentials`. See [the credential gate](docs/reference/providers.md#the-credential-gate).
- **A launch through a host wrapper syncs your host config without asking.** When you run an
  agent through the wrapper yolo put on your PATH and your packs have changed, yolo now updates the
  agent's config files, prints `yolo host: synchronized host configuration`, and starts the agent.
  In 0.10.0 it showed the change and waited, and only when `host_apply_on_launch` was on. That key
  now defaults to on whenever wrappers are on, and wrappers are on whenever `host_management` is
  `"own"`. yolo still asks before a first apply that would overwrite settings it does not manage.
  Set `"host_apply_on_launch": false` in your user config to turn the check off, or
  `"host_wrappers": false` to go without wrappers.
- **Pack briefings come only from a pack's `briefing/` directory.** An instructions file at a
  pack's root, such as `<pack>/AGENTS.md` or `<pack>/CLAUDE.md`, is no longer delivered to any
  agent. Move that prose into `briefing/<pack>.md`. `yolo host apply` moves your local pack's
  `local/AGENTS.md` there for you, and `yolo pack lint` names any pack that still has one. Pack prose is also no longer
  labeled with the name of the pack it came from.
- **Refused config and commands.** Each refusal names what replaces it.
  - A provider's bare `base_url`, which agents read as different protocols. Write
    `endpoints.<protocol>.base_url`.
  - The `claude_plugins` pack hook.
  - `yolo host wrappers enable` and `disable`. `host_management: "own"` turns wrappers on, and
    `"host_wrappers": false` in your user config turns them off.
  - `yolo host apply --shell-init`. It prints the PATH line for you to add to your shell rc
    yourself. A line it appended earlier stays in your rc, under the comment
    `# yolo-jail host launch wrappers`.
  - An unknown flag, on every command. It exits 2 instead of being ignored.
  - Two writers for one `host_files` destination. The last one used to win.
- **`lsp_servers` installs nothing.** It only configures the agents, so install each language
  server through `mise_tools` or `packages`. Servers an earlier yolo installed stay where they are:
  `yolo programs ls` lists them and `yolo programs remove` clears them. Run `yolo programs ls`
  before you set `programs.autoprune: true`, which deletes those copies and all of `~/go/bin`.
- **Git packs are fetched at launch**, and a pack pinned to `?ref=<branch>` follows its branch
  within the hour. Pin a tag or a commit for any pack that runs code on your machine.
- **A running jail keeps the packs it started with.** Re-entering it after you change `packs`, or
  after upgrading yolo, no longer swaps its packs underneath it, which could stop a jail an older
  yolo started from booting. yolo says which packs differ, and `yolo stop` then a new launch picks
  them up. When the running jail cannot serve the profile you selected, such as a jail 0.10.0
  started, yolo asks at a terminal whether to restart it, saying how many sessions a restart ends.
  Elsewhere it stops and tells you to run `yolo stop` first (`container stop` on Apple Container).
  It used to warn and start the session without the profile. `YOLO_ALLOW_ATTACH_SKEW=1` re-enters
  anyway.
- **The `codex` profiles offer the GPT-6 models**, each with a 1M-context option, in Claude, pi and
  codex alike, and codex starts on the same default. The GPT-5 models are no longer offered: add
  one under `providers.openai-codex.models` in your config and it appears in every agent
  ([the `openai-codex` model list](docs/reference/providers.md#the-openai-codex-model-list)). If pi
  does not know a model you add, it warns once and offers it without thinking levels or image
  input. pi no longer keeps a separate scoped list for these models, and its sub-agents may use
  only them. A list you scoped inside pi is kept. In a pi launched without `-p codex` and with no
  model chosen yet, signing in to ChatGPT no longer picks a GPT-5 model for you: choose one with
  `/model`.
- **A new podman workspace copies only your Claude account and onboarding state** from the
  machine-wide home, so a login kept only there asks you to sign in once.
- **Ctrl-C reaches the jail**, interrupting what runs there, such as a Claude reply, instead of
  ending your session.
- Dropping a profile clears the provider and model yolo wrote for it into pi, opencode or codex,
  and keeps a model you picked inside the agent.
- pi updates its extensions before it starts, at most hourly and whenever its settings change.
  `agent_updates: false` turns that off.
- pi's MCP servers are written to `~/.pi/agent/mcp-adapter.json`, and the `mcp.json` yolo wrote
  before is removed.
- On podman on macOS and on Apple Container, a changed image is sent to the runtime without the
  layers it already holds, so a one-package change no longer re-sends the whole image. If that
  load fails, yolo retries once with the full image and says so.
- On macos-user, an agent can no longer read the command line or environment of processes outside
  its sandbox, `ioctl` calls are allowed only on terminals, and the system keychains are no longer
  readable.
- `writable_home_dirs` and `host_files` refuse the directories of the packs you select, including
  packs yolo does not ship, and no others. A claude-only workspace may now name `.codex` or
  `~/.codex/config.toml`.
- `yolo host -p` takes the jail's spelling too: `yolo host -p claude=zai -- claude` is
  `yolo host -p zai -- claude`. A pair naming a command other than the one being run is refused.
- `yolo check` reports one finding, not two or three, when no container runtime answers, whether
  none is installed or one is installed but stopped, and one when the Claude login service's
  certificates were never generated. A running jail whose host-services directory is gone is one
  failure naming that directory, not one per loophole.

### Fixed

- **Security:** yolo's ChatGPT sign-in service and its Claude login refresh answered any process
  that could reach them. In a jail using your host's network (`network.mode: "host"`), and for
  codex started with `yolo host`, that was every process on your machine, which could read your
  ChatGPT tokens or get a fresh Claude login. The sign-in service now answers only a codex yolo
  started, and the login refresh only a caller that already holds this machine's Claude login.
- **Security:** the wire bridge accepted any request on its loopback ports, and a jail on
  `network.mode: "host"`, or on macos-user, shares those ports with every process on your machine.
  Such a process could spend your provider keys or ChatGPT subscription through the bridge, and one
  that took a port first received what each agent sent there, including Claude's saved login on
  the `codex` profile. Each launch now gives its agents a fresh secret, and the bridge refuses any
  request without it and passes it on to no provider
  ([caller authentication](docs/reference/wire-bridge.md#caller-authentication)).
- **Security:** host-side yolo followed symbolic links in a workspace's `.yolo` state, so an agent
  could make the next launch or `yolo prune` write to a host path as you.
- **Security:** on macos-user, an agent could edit, rename or delete the skills and briefing yolo
  delivers, which are copied into the sandbox home rather than mounted read-only, and a symbolic
  link it left in its home could make the next launch rewrite them in another folder the sandbox
  account can write, such as another project's. The sandbox now denies those writes, and a launch
  writes them only inside the sandbox home and the project's own `.yolo` folder.
- **Security:** a provider key could be sent to `api.anthropic.com` when the provider named no
  Anthropic address.
- **Security:** a new workspace's Claude login copy carried other workspaces' `projects` and
  `mcpServers`.
- **Security:** on Apple Container older than 1.1.0, a `host_files` directory source was bound
  writable. It is now skipped, with the same message other read-only binds print there.
- On a Mac with Podman, a jail that needed a folder the Podman Machine does not share failed to
  start with podman's `statfs …: no such file or directory` and nothing from yolo. That included
  every Homebrew install on a machine created without Homebrew's `Cellar` folder shared. yolo now
  reads the machine's shared folders and stops first, naming the folder and the `podman machine
  init` command that adds it, and `yolo check` reports it too. A machine created as
  [Getting Started](userguide/getting-started.md#macos-podman) shows is unaffected.
- On macos-user, every launch with the pi, omp, agy or opencode pack deleted the whole folder
  holding that agent's skills (`~/.pi/agent`, `~/.oh-omp/agent`, `~/.gemini/config`,
  `~/.config/opencode`) and put back only the skills and briefing. That cost pi's settings, models,
  MCP config, sign-in and sessions, omp's `models.yml`, opencode's `opencode.json`, and anything
  else the agent kept there. A launch now replaces only the skills and the briefing. What was
  deleted can be recovered only from your own backup of `<workspace>/.yolo/home`.
- On macos-user, a launch that refused to set up the per-workspace home over an older sandbox
  account still deleted that account's agent directory, transcripts included, right after telling
  you to move them out first.
- On Linux, a jail running for more than a week could have its tools deleted by
  `nix-collect-garbage`, because `yolo prune` removed the GC root of the image it ran on. The root
  is now kept for as long as a container uses the image.
- Quitting a podman jail after a long session could hold your terminal while podman deleted what
  the jail had left in `/tmp`. You get the prompt back as soon as the jail exits and the files are
  deleted in the background; `yolo stores` lists any a crash left behind, and the next launch or
  `yolo prune --apply` removes them.
- Every jail start paused while it forced a rebuild of the font cache.
- Two jails on your host's network, or two nested jails, running at once contended for the same
  ports for the wire bridge and the ChatGPT and AWS sign-in services, and the second launch was
  refused. Such a jail now runs each of them on a port picked for that launch.
- Two launches of one workspace at once could fail with `directory not empty`, or start with part
  of a pack missing. The second now waits for the first.
- On macos-user, closing one of two sessions in the same project cut the other off from Claude's
  sign-in renewal and every other host service it used.
- The wire bridge crashed and restarted in a loop when it had nothing to serve, such as when a
  provider's key was unset.
- The Claude login service could return the token a jail already held, and Claude Code stopped
  with `api_request_oauth_refresh_exhausted`.
- Claude on its `codex` profile showed `did not translate` in place of ChatGPT's own errors, and
  on a machine not yet signed in to ChatGPT its first request failed. It now signs you in before
  Claude starts, as codex and pi do.
- Claude over the wire bridge reported zero input tokens. A provider that rejects the usage
  request needs `"supports_usage_in_streaming": "false"` in its `options`
  ([streamed usage](docs/reference/wire-bridge.md#streamed-usage)).
- Codex could not renew its ChatGPT sign-in through the `openai-auth` pack.
- On macos-user, and in the environment `yolo host env` prints, codex was pointed at a ChatGPT
  sign-in address nothing served. It is now set only where yolo serves it.
- On macos-user, a profile chosen with `-p` reached the agent's environment but not the settings
  yolo writes from it, so codex's `config.toml` named no provider or model.
- On macos-user, the files a pack puts in the agent's home, such as pi's extensions, were never
  delivered, and a launch never reported installed programs whose version differs from what yolo
  recorded.
- `yolo -p` stopped changing pi's model once your host's pi settings named one, and the models you
  scoped inside pi were reset to yolo's list at every launch.
- Every pi launch printed `Failed to load theme "system"`.
- With the kilo pack and no profile naming a kilo model, pi rejected its whole `models.json`.
- A workspace pinning an older Node, such as 20, left pi unable to start.
- The zai pack's GLM-5.3-Flash model now accepts images.
- Claude's LSP plugins that you enabled on the host were off in the jail unless `lsp_servers` named
  their language.
- A workspace's `.vscode/mcp.json` was hidden from agents and showed as modified in git.
- `yolo -p <profile> -- <command>` refused any command that was not an agent.
- A `-p`, `--profile`, `--at`, `--network` or `--with-credentials` with no value is now refused.
  `yolo --profile= -- claude` used to run `--profile=` as a command, `yolo -p -- claude` selected a
  profile named `run`, and `yolo host --profile= -- claude` quietly selected none.
  `yolo host -p=zai -- claude` now works as it does in a jail.
- `yolo run --at host -- <command>` and `yolo host --at host -- <command>` were refused. Every
  spelling now runs the command on your host, wherever `--at` sits, and the last `--at` wins. A bare
  `yolo --at host` prints `yolo host`'s usage.
- `yolo host -- <command>` stopped a launch whose provider key was missing without saying it was
  refusing. It now prints the refusal a jail launch prints.
- A wrapped launch such as `yolo host -- claude` refused to start when another agent's config could
  not be written. It now names that failure and launches, and refuses only when the program being
  launched is the one whose config failed.
- A launch flag a pack declares for its `guarded` posture never reached any launch. `yolo host --`
  now adds it and says so, naming the pack.
- An agent on your own machine was never told it was there, and `yolo host apply` dropped
  `agents_md_extra` from its briefing. Every briefing file your packs name now opens with a short
  section saying the agent runs on your real machine with permission prompts on, followed by your
  `agents_md_extra` and your packs' prose.
- `yolo config render --at host`, and a bare `yolo config render` outside any workspace, showed the
  file a jail gets, including permission-bypass settings `yolo host apply` never writes. It now
  prints exactly what `yolo host apply --assert` would write. The preview never fetches, so a git
  pack not yet fetched is named as missing from it.
- `yolo host apply --assert` replaced the `env` block of your host `~/.claude/settings.json`,
  dropping your own variables.
- A settings file `yolo host apply --assert` had written came back into your jails as your own
  settings, so a value a pack later dropped stayed in them. Jails now leave that file out, and
  `yolo host apply --revert --assert` takes yolo back out of it.
- Host pi printed `models.json error` at every start once `yolo host apply --assert` had created
  that file empty. The next apply repairs it.
- `yolo host apply` skipped every git pack, even an installed one.
- `yolo host apply` adopted `~/.claude/skills/synced/`, the skills Claude Code syncs from your
  claude.ai account, and the next sync lost new and edited skills. It now leaves that folder alone
  and says so in one line when the folder is new or has changed.
- `yolo host apply` wrote the rest of a pack whose `pack.json` has problems into your home. It now
  writes nothing for that pack and names each problem, and `yolo config promote` no longer writes
  into such a pack.
- A pack's `only` and `exclude` filters were ignored for packs yolo ships, and `yolo host apply`
  delivered what they leave out for every pack. They now apply in jails and on your host, and
  `yolo pack explain` shows what they keep.
- `yolo host apply` stopped rendering a whole pack when one of its files was a link to a folder that
  no longer exists. It now names the link and its target and applies everything else.
- `yolo host apply` listed a config file it was about to create as unchanged.
- When `yolo host apply` would drop an MCP server you had added to an agent's own config, or an
  LSP server you had added to Copilot's, it told you to declare it under `mcp_servers`, which
  does not reach the files that command writes. It now names the `config-overlay` to add to your
  local pack, which does. It also stopped keeping some of the servers it said it dropped, and, with
  `host_management: "own"`, stopped warning about servers in codex's or opencode's config that it
  keeps.
- With `host_management: "own"`, `yolo host apply` said on every run that a pack's
  `config-overlay` had overwritten one of your values when your edit was the one kept. It now says
  which value won and how to take the other.
- `yolo host apply` listed every file it left unchanged. It now lists only what changed, what
  failed and what it replaced, and ends on its verdict; `--verbose` still lists everything.
- `yolo host apply` and `yolo check-deps` offered to install a program on a machine its vendor
  publishes no build for. They now say there is no build for this machine.
- `yolo config-ref` said no file `yolo host apply` writes holds provider settings. It now names the
  ones that do, such as pi's `models.json` and codex's `config.toml`.
- `yolo prune --nix-gc` refused to run while any jail you had started normally was running.
- Every yolo process left a copy of the shipped packs in `/tmp`, which `yolo prune` now removes.
- A terminal attached to a jail that died under it could be left sending a burst of symbols for
  every key you pressed, until you ran `reset`.
- A nested jail put the outer jail's briefing in front of its own.
- `yolo init` wrote a workspace config saying grep and find are blocked by default, and both it and
  `yolo init-user-config` wrote a config offering no `macos-user` runtime.
- On a Mac with the official Nix installer, `yolo check` reported `Nix daemon: connection failed`
  and stopped, although Nix worked.
- On a Mac with Apple Container, `yolo check` warned `No OCI conversion tool for Apple Container`
  and suggested installing skopeo, which yolo never uses.
- `yolo macos-setup --help` named a `backend` config key that does not exist. It now names
  `runtime`.
- The links on yolo-jail's PyPI page led nowhere. They now open that release's files on GitHub.

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
- `include_if_found` and `<workspace>/yolo-jail.local.jsonc` layer untracked overrides over a workspace config.
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
