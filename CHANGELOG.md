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
Changing those settings takes effect at your next launch, which restarts the shared service and
says so, and `yolo check` tells you when the running service still has the old ones. The
credential is served only to the agents of the launch that asked, even in a jail that shares
your host's network. Where the service does not run (macos-user, `yolo host`, or a jail that has
not enabled it) the launch leaves the Bedrock settings out and says so.

**An agent footer.** Every agent with a status-line hook shows its billing route and whether it
runs in a jail or on the host, unless you set your own `statusLine`. Claude's also shows its
thinking level.

**Agents and providers are checked against each other.** A launch whose agent cannot speak any
protocol its provider serves is refused, naming any shipped pack that translates between them.
`yolo check` predicts it. On your own machine, `yolo host` refuses a profile its agent could
reach only through the jail's wire bridge, which runs only in a container jail (podman or Apple
Container, not macos-user), and names the jail launch that works. A macos-user launch refuses it
the same way, where it used to start the agent against an address nothing served. It no longer starts the agent
pointed at an address nothing serves, and it no longer tells you to add `wire-bridge` to
`packs`, which changes nothing there. A profile whose provider the launch does not have, such as
one a `null` in your `providers` removes, is refused naming why when yolo would configure the
agent from that provider, in a jail and on the host; the agent used to start without any of its
settings. See
[Providers](docs/reference/providers.md).

**Host-only entries from a pack.** A pack's `autonomy` postures can carry `lists`: entries added
to another pack's config array only at that posture. A `guarded` list reaches your host through
`yolo host apply` and no jail, so a pack can give host pi a permission-gate extension that would
only cost tokens and prompts in a jail. See [posture lists](docs/reference/pack-system.md#autonomy).

**A repository's skills reach every agent in the jail.** Skills a repo commits for one agent,
such as `.claude/skills/`, `.agents/skills/` or `.github/skills/`, now also reach the agents
that do not read that directory, in containers and on macos-user. An agent that reads the
directory itself gets no second copy, and no other skill of the same name from elsewhere in the
repo. A repo skill never replaces yolo's own skills or a pack's in what yolo gives an agent, and
the launch names every one it held back; when an agent reads the repo's copy of such a skill by
itself, the launch says that too. It also names any symlink that points outside the repository,
and never reads through one. A repository cannot stop a launch this way: a path too deep for
every agent to be given, or a file that cannot be copied, is named and skipped. Nothing is
written into the repository. See
[the workspace layer](docs/reference/agent-briefings.md#the-workspace-layer).

**A jail's builds can yield the disk.** Set `"resources": {"io": "low"}`, or `"idle"`, and every
process in a podman jail on Linux runs at a lower disk priority, so a build stops stalling your
desktop when both want the disk. Disks whose scheduler is bfq honor both values and mq-deadline
honors `"idle"`; kyber and none, the usual NVMe default, ignore it. The launch says so when the
disk under your workspace is one of those, and `yolo check` grades that disk and names the host
change. The priority is advisory rather than a limit, and it does not reach buffered writes or
a `nix build`, which runs in the host's nix daemon. Apple Container and podman on macOS cannot
apply it, and the launch says that too. See
[resources per setup](userguide/reference/settings-per-setup.md#resources-devices-and-networking).

- `yolo host-daemon status|stop|restart|logs` manages the machine-wide daemons.
- `nix shell` and `nix build` work in a jail with no extra flags.

### Changed

- **Ctrl-C reaches the jail**, interrupting what runs there, such as a Claude reply, instead of
  ending your session.
- **Re-entering a jail an older yolo started asks before going on** when that jail cannot take
  the profile you selected. At a terminal, `Restart jail now? [Y/n]` says how many sessions a
  restart ends. Elsewhere, yolo stops and tells you to run `yolo stop` first (`container stop`
  on Apple Container). It used to warn and start the session without the profile.
  `YOLO_ALLOW_ATTACH_SKEW=1` re-enters anyway.
- **A running jail keeps the packs it started with.** Re-entering it after you change `packs`, or
  after upgrading yolo, no longer swaps its packs underneath it, which could stop a jail an older
  yolo started from booting. yolo says which packs differ and that `yolo stop`, then a new
  launch, picks them up. A profile only a newly added pack provides, or a jail whose packs this
  yolo cannot read, such as one an earlier release started with the claude pack, gets the same
  restart question.
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
- **pi and Claude offer the same models on their `codex` profiles**: the GPT-6 models, each with
  a 1M-context option, and codex starts on the same default. pi has no separate "scoped" list
  for them any more, and its sub-agents may use only those models. The older GPT-5 models are
  no longer offered; add one under `providers.openai-codex.models` in your config and it
  appears in every agent ([the `openai-codex` model list](docs/reference/providers.md#the-openai-codex-model-list)).
  If pi does not know a model you add, it warns you once and offers it without thinking levels
  or image input. A list you scoped inside pi is kept. In a pi launched without `-p codex` that has no model
  chosen yet, signing in to ChatGPT no longer picks one for you, because pi's own pick is a GPT-5
  model: choose one with `/model`.
- `writable_home_dirs` and `host_files` refuse the paths of the packs you select, including packs
  yolo does not ship, and no others. A claude-only workspace may now name `.codex` or
  `~/.codex/config.toml`.
- pi updates its extensions before it starts, at most hourly and whenever its settings change.
  `agent_updates: false` turns that off.
- An unknown flag exits 2 instead of being ignored.
- `yolo check` reports one finding, not two or three, when no container runtime answers, whether
  none is installed or one is installed but not started, and one finding for a Claude OAuth
  broker whose certificates were never generated. It no longer says no runtime is on `PATH` when
  a stopped one is. A running jail whose host-services directory is gone is one failure naming
  that directory, not one per loophole with different advice each.
- `yolo host -p` takes the jail's spelling too: `yolo host -p claude=zai -- claude` is
  `yolo host -p zai -- claude`. A pair naming a command other than the one being run is refused.
- Two writers for one `host_files` destination refuse the launch; the last one used to win.

### Removed

- A provider's bare `base_url`, which agents read as different protocols. Write
  `endpoints.<protocol>.base_url`.
- The `claude_plugins` pack hook. Its refusal names the replacements.
- `yolo host wrappers enable` and `disable`. Wrappers are on by default when `host_management` is
  `"own"`, and `"host_wrappers": false` in your user config turns them off.
- `yolo host apply --shell-init`. It now refuses and prints the PATH line for you to add to your
  shell rc yourself. A line it appended earlier stays in your rc, under the comment
  `# yolo-jail host launch wrappers`.

### Fixed

- Yolo's Codex sign-in service and its Claude login refresh answered any process that could
  reach them. In a jail using your host's network (`network.mode: "host"`), and for codex
  started with `yolo host`, that was every process on your machine, which could read your
  ChatGPT tokens or get a fresh Claude login. The Codex sign-in service now answers only a
  codex yolo started. The Claude login refresh now answers only a caller that already holds this
  machine's Claude login, so a process that cannot read your credentials gets nothing.
- On macos-user, and in the environment `yolo host env` prints, codex was pointed at a ChatGPT
  sign-in refresh address nothing served. It is now set only where yolo serves it, and the
  launch names what it left out.
- `yolo --profile= -- claude` ran `--profile=` as a command inside the jail, `yolo -p -- claude`
  selected a profile named `run`, and `yolo host --profile= -- claude` quietly selected none. A
  `-p`, `--profile`, `--at`, `--network` or `--with-credentials` with no value is now refused
  with the same message in a jail and on the host, and `yolo host -p=zai -- claude` works as it
  does in a jail. A mistyped flag before `yolo host`'s `--` is named the way a jail launch names
  it.
- `yolo --at host -- <command>` ran the command on your machine, but `yolo run --at host --
  <command>` was refused as a jail launch, and `yolo host --at host -- <command>` was refused
  too. Every spelling now runs it at the host, wherever `--at` sits, and the last `--at` you type
  wins. A bare `yolo --at host` prints `yolo host`'s usage instead of a refusal. A flag that only
  means something to a jail launch, such as `--timing`, is refused by `yolo host` by name instead
  of as an unexpected argument, and host flags typed with no `--` and no command
  (`yolo host -p zai`, `yolo --at host --profile=`) are read as flags rather than as an unknown
  verb.
- `yolo host -- <command>` stopped a launch whose provider key was missing without saying it was
  refusing the launch. It now prints the refusal a jail launch prints, word for word.
- Quitting a podman jail could hold your terminal for half a minute after a long session, while
  podman deleted everything the jail had left in `/tmp` and its container dirs. You get the
  prompt back as soon as the jail exits, and the files are deleted in the background.
  `yolo stores` lists any a crash left behind, and the next launch or `yolo prune --apply`
  removes them.
- A launch that had to load its image again, after `podman image prune` or a yolo upgrade,
  showed nothing while the image was copied. It now shows how many layers and gigabytes it
  has copied and for how long. The other slow launch steps show their progress too: the
  image build, the first-time copier build, loading the image on Apple Container and podman
  on macOS, a wait behind another launch, a pack update, the macOS native build, and a cold
  first-boot `mise install`. Steps that finish within two seconds print nothing new.
- Every jail start spent two seconds rebuilding the font cache.
- `yolo prune --nix-gc` refused to run while any jail you had started normally was running.
- On Linux, a jail running for more than a week could have its tools deleted by
  `nix-collect-garbage`, because `yolo prune` removed the GC root of the image it was running on.
  `yolo prune` now keeps that root for as long as a container uses the image.
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
- When pi on your host could not reach yolo's ChatGPT sign-in because it was started directly or
  from an editor rather than through `yolo host`, its error named a setting only jails have. It
  now says to start pi with `yolo host -- pi`.
- Host pi printed `models.json error` at every start once `yolo host apply --assert` had created
  its `~/.pi/agent/models.json`, which it wrote empty. The next `yolo host apply --assert`
  repairs a file an earlier one left that way, and `yolo host apply --revert --assert` keeps the
  empty `providers` pi needs, naming it, rather than emptying the file again.
- Claude's LSP plugins that you enabled on the host were off in the jail unless `lsp_servers`
  named their language.
- Codex could not renew its ChatGPT sign-in through the `openai-auth` pack.
- Claude on its `codex` profile never asked you to sign in to ChatGPT, so on a machine not yet
  signed in its first request failed. It now signs you in before Claude starts, as codex and pi
  do.
- `yolo host apply --assert` replaced the `env` block of your host `~/.claude/settings.json`,
  dropping your own variables.
- A settings file `yolo host apply --assert` had written came back into your jails as your own
  settings, on every backend, so a value a pack later dropped stayed in them. Jails now leave
  that file out entirely, your own settings in it included, so a setting you add to it by hand
  afterwards stays on the host. `yolo host apply --revert --assert` takes yolo back out of the
  file, and jails read it again.
- `yolo host apply` skipped every git pack, even an installed one.
- `yolo host apply` listed a config file it was about to create as unchanged, and `--assert`
  counted it among the files already in sync. That includes a file behind a link whose target
  did not exist yet, as a dotfiles checkout can leave.
- `yolo config-ref` said no file `yolo host apply` writes holds provider settings. Some do, such
  as pi's `models.json` and codex's `config.toml`, and host apply writes them without your
  providers. It now says so and names each one.
- When `yolo host apply` would drop an MCP server you had added to an agent's own config, or an
  LSP server you had added to Copilot's, it told you to declare it under `mcp_servers`, which
  reaches jails and none of the files that command writes, so the server was dropped anyway. It
  now tells you to add a `config-overlay` to your local pack for each config that would lose an
  entry, naming that config and the key it keeps them under, which keeps it.
- A jail start or `yolo host apply --assert` kept some of the MCP servers you had added to an
  agent's own config, or LSP servers to Copilot's, while saying it dropped them all. Every one it
  names is now dropped.
- With `host_management: own`, `yolo host apply` warned that an MCP server you had added by hand
  to codex's or opencode's config would be dropped, when the apply kept it. The note at the top
  of `~/.codex/config.toml` also said the file was composed at jail start; it now names
  `yolo host apply`.
- `yolo host apply --assert` wrote the rest of a pack whose `pack.json` has problems, the ones
  `yolo pack lint` and every launch refuse, into your home. It now writes nothing and names each
  problem, and the other host commands leave such a pack out and say so. `yolo config promote`
  no longer writes into such a pack and reports success. Host commands also skip a `pack.json`
  that the pack's `only` or `exclude` filters out, as jails always did.
- `yolo host apply` delivered the skills, briefings and files a pack's `only` or `exclude` leaves
  out, which no jail received. The `only` and `exclude` of a pack yolo ships were ignored
  everywhere; they now apply in jails and on your host, and `yolo pack explain` shows what they
  keep for a shipped or fetched pack as well as a local one.
- `yolo host apply` and `yolo check-deps` offered to install a program on a machine its vendor
  publishes no build for, and counted it missing. They now say there is no build for this
  machine and offer nothing, as a jail does.
- `yolo host apply` adopted `~/.claude/skills/synced/`, and the next claude.ai sync lost new and
  edited skills.
- A workspace pinning an older Node, such as 20, left pi unable to start.
- A workspace's `.vscode/mcp.json` was hidden from agents and showed as modified in git.
- `yolo -p <profile> -- <command>` refused any command that was not an agent.
- Every yolo process left a copy of the shipped packs in `/tmp`, which `yolo prune` now removes.
- Two launches of one workspace at once could fail with `directory not empty`, or start with
  part of a pack missing; the second now waits for the first. Attaching to a running jail no
  longer empties and re-copies the pack files it has mounted.
- On `macos-user`, closing one of two sessions in the same project cut the other off from
  Claude's sign-in renewal and every other host service it was using.
- On `macos-user`, every launch with the pi, omp, agy or opencode pack deleted the whole folder
  holding that agent's skills (`~/.pi/agent`, `~/.oh-omp/agent`, `~/.gemini/config`,
  `~/.config/opencode`) and put back only the skills and briefing. In 0.9.0 that cost pi's
  `settings.json` and `models.json`, and in 0.10.0 its `mcp.json` too; in both, omp's
  `models.yml`, opencode's `opencode.json`, pi's sign-in (`auth.json`) and sessions, and anything
  else the agent kept in those folders. A launch now replaces only the skills and the briefing.
  What was deleted can be recovered only from your own backup of `<workspace>/.yolo/home`.
- `yolo init` wrote a workspace config saying grep and find are blocked by default, and both it and
  `yolo init-user-config` wrote a config offering no `macos-user` runtime.
- The links on yolo-jail's PyPI page led nowhere. They now open that release's files on GitHub.
- `yolo host apply` stopped rendering a whole pack when one of its files was a link into a folder
  that no longer exists, such as a dotfiles link left after the dotfiles moved, and printed only
  `no such file or directory` between its report lines. It now names the link and its target,
  says to remove the link or recreate the folder, and applies everything else.
- With `host_management: own`, `yolo host apply` said on every run that a pack's `config-overlay`
  had overwritten one of your values, when your own edit was the value it kept, and that nothing
  could keep your value. It now says your edit is kept over that pack's overlay and how to take
  the pack's value instead. When an overlay does replace your value, it names the pack and says
  to remove the key from that pack's overlay.
- A wrapped launch such as `yolo host -- claude` refused to start when another agent's config
  could not be written, such as pi's. It now names that failure with its fix and launches, and
  refuses only when the program being launched is the one whose config failed.
- `yolo host apply` printed a long paragraph about `~/.claude/skills/synced` on every run and
  pointed at `claude plugin list`, which does not show that folder. It now says in one line that
  it holds the skills Claude Code syncs from your claude.ai account and that yolo leaves it alone,
  and only when the folder is new or has changed.
- `yolo host apply` listed every file it left unchanged, spent three lines on settings that never
  apply on your own machine, and ended an `--assert` by explaining what `--assert` means. It now
  lists only what changed, what failed and what it replaced; puts the settings that do not apply
  on one line; and ends on its verdict. `--verbose` still lists everything.

### Security

- A provider credential, and the settings a profile composes, reached every process in the jail
  whichever agent selected the profile, so pi could use Bedrock because Claude had the keys. Each
  now reaches only the agent that selected it
  ([the credential gate](docs/reference/providers.md#the-credential-gate)). A plain shell sees
  only `env_sources` values no provider claims, and `yolo host env` prints one agent's slice.
  On your own machine, `yolo host -p <profile> -- <command>` hands any command, not only an
  agent, the credentials that profile claims from `env_sources` for one run, and each withheld
  credential's line names a command that delivers it. For a command that needs several
  providers' keys at once, such as a usage bar, `yolo host --with-credentials zai,cerebras --
  <command>` (or `all`) hands it just those keys for one run, without switching any profile, and
  names what it handed over; `eval "$(yolo host env --with-credentials all)"` puts them in your
  shell. A jail launch refuses the flag. An SSO-backed Bedrock profile's credential endpoint
  still reaches only an agent.
  On macos-user only the program the invocation starts gets its profile's values, so an agent
  started from the sandbox's login shell gets none. A variable you set on an agent's command
  line no longer overrides what its profile composes.
- Host-side yolo followed symbolic links in a workspace's `.yolo` state, so an agent could have
  the next launch or `yolo prune` write to a host path as you.
- On `macos-user`, a symbolic link an agent left in its home could make the next launch delete
  and rewrite the skills and briefing in another folder the sandbox account can write, such as
  another project's. A launch now writes them only inside the sandbox home and the project's own
  `.yolo` folder, even while another session's agent is running.
- A provider key could be sent to `api.anthropic.com` when the provider named no Anthropic
  address.
- A new workspace's Claude login seed carried other workspaces' `projects` and `mcpServers`.
- On Apple Container older than 1.1.0, a `host_files` directory source was bound writable.
- On `macos-user`, an agent could edit, rename or delete the skills and briefing yolo delivers,
  because they are copied into the sandbox home instead of mounted read-only. The sandbox profile
  now denies those writes, as the read-only mount does on every other backend, and leaves the
  agent's own state beside them writable. This has not yet been verified on a Mac. A symbolic link
  an earlier session left in the workspace can no longer send the copies somewhere the profile does
  not cover: the launch replaces the link, or refuses and names it.
- On `macos-user`, a launch that refused to set up the per-workspace home over an older sandbox
  account still deleted that account's agent directory, transcripts included, right after telling
  you to move them out first.
- The wire bridge now authenticates its callers. It accepted any request on its loopback ports,
  and a jail on `network.mode: host`, or on `macos-user`, shares those ports with every process
  on your machine. Such a process could spend your provider keys or ChatGPT subscription through
  the bridge. A process that took a port first received what each agent sent there: Claude's
  saved login on the Codex profile, and the provider's key for Claude and Copilot on a bridged
  provider. Each launch now gives its agents a fresh secret for the bridge, and the bridge
  refuses any request without it and passes it on to no provider. An agent sends only that
  secret to the bridge
  ([caller authentication](docs/reference/wire-bridge.md#caller-authentication)).

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
