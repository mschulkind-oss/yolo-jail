# Packs and Skills

A **pack** is a reusable bundle of agent setup: a coding agent and its settings, skills, house
rules, tools, model providers, or a connection to your host. Everything a jail has beyond a bare
shell arrives as a pack, and nothing is selected for you. This page covers choosing packs, checking
what a pack does before you trust it, sharing skills across agents, and keeping agents up to date.

To write a pack of your own, see [Writing your own pack](migrating-to-packs.md).

## Choose your packs

Packs go in your **user config**, `~/.config/yolo-jail/config.toml`, under `packs`. Select one with
a command, on your machine:

```bash
yolo pack add claude
yolo pack add codex
yolo pack remove codex        # and drop one again
```

That leaves `packs = ["claude", "guardrails"]` in the file (in a legacy `config.jsonc`,
`"packs": [...]`). `yolo pack ls` lists the packs you selected and what each delivers.

A project's own config cannot select packs. The project folder is writable from inside the
jail, so an agent could otherwise hand itself new skills, programs or host access. Run `yolo config check`
after you edit the list, and the next launch picks it up; an agent pack needs no image rebuild.

With no packs, a jail is a shell with yolo's built-in tools and no coding agent, and the launch says
so.

### The packs yolo ships

Select these by bare name.

**Agents.** Each installs the agent the first time you type its name inside the jail, and starts it
with its permission prompts off, since the jail is the safety boundary.

| Pack | Agent | Start it with |
|---|---|---|
| `claude` | [Claude Code](https://docs.anthropic.com/en/docs/claude-code) | `yolo -- claude` |
| `codex` | [OpenAI Codex CLI](https://github.com/openai/codex) | `yolo -- codex` |
| `copilot` | [GitHub Copilot CLI](https://github.com/github/copilot-cli) | `yolo -- copilot` |
| `opencode` | [opencode](https://opencode.ai) | `yolo -- opencode` |
| `pi` | [pi](https://pi.dev) | `yolo -- pi` |
| `agy` | Google Antigravity | `yolo -- agy` |
| `omp` | oh-omp, run as `oh-omp` | `yolo -- oh-omp` (not on Apple silicon or ARM Linux; its vendor publishes no build for them) |

**Model providers.** Each adds a provider and a profile of the same name, so you can point an agent
at it with `yolo -p <name> -- <agent>`: `zai`, `openrouter`, `kilo`, `cerebras`, `llamacpp`,
`bedrock`. The subscription packs are named for their agent, and their profiles for the agent too:
`claude-auth` (profile `claude`), `codex-auth` (`codex`) and `copilot-auth` (`copilot`). Each agent
brings its own, and you can also select one without its agent, to run pi, opencode or oh-omp on a
subscription they have built in: `"packs": ["pi", "copilot-auth"]`, then `yolo -p pi=copilot -- pi`.
[Providers and Models](providers-and-models.md) explains them.

**Host connections.** Each ships a **loophole**, a named connection from the jail to one capability
on your host: `aws-auth`, `github`, `serial`, `journal`, `host-processes`, `audio` and
`cgroup-delegate`. Most also need turning on by name. [Host Access and Loopholes](loopholes.md) lists
what each does, and [GitHub](github.md) covers the `github` pack, which runs the jail's `gh` with
your host's login.

**Habits.** `guardrails` makes recursive `grep` and `find` refuse and point at `rg` and `fd`, and
makes `mv` and `rm` refuse to touch the workspace's `.yolo`.
[Packages and Tools](packages-and-tools.md#blocked-tools) covers blocking tools.

Some packs bring others with them: `claude` brings `claude-auth` and `wire-bridge`, `codex` brings
`codex-auth`, `copilot` brings `copilot-auth`, `bedrock` brings `aws-auth`, and `cerebras` and `kilo` bring `wire-bridge` when
`claude` or `copilot` is selected. The launch prints the full set it resolved, so what you see there
can be longer than what you typed. A pack brought this way cannot be left out, but `copilot-auth`
only declares a provider and a profile: it runs nothing until you select that profile.

### Packs from elsewhere

A pack can also be a folder on your machine or a git repository:

```jsonc
{
  "packs": [
    "claude",
    "~/code/my-pack",
    "git+ssh://git@github.com/org/repo//packs/team?ref=v1.2.0"
  ]
}
```

A local folder is read in place. A git pack is fetched on the host the first time a launch needs it
(the jail has no git credentials of its own), and its commit is recorded in a lockfile. What moves it
later depends on the `ref`:

- **A tag or a commit** stays exactly where it is until you run `yolo pack install` or
  `yolo pack update`.
- **A branch** is fetched again at most once an hour, at your next launch.

The launch says so whenever a pack arrives or moves, for example
`Updated pack team: main 1a2b3c4d → 5d6e7f80`.

### Run your own fork of a program

If you maintain a fork of an agent or tool that a pack installs, a small pack of your own can make
every jail run your fork instead. The pack names the program it forks, where your fork's code is,
and one command that builds it:

```json
{
  "name": "pi-mine",
  "contributes": [
    { "kind": "program", "bin": "pi", "via": "source", "fork_of": "pi",
      "source": "git+https://github.com/you/pi-fork?ref=main",
      "build": "npm ci && npm run build && npm install -g \"$(npm pack --silent)\"",
      "produces": [".npm-global/bin/pi", ".npm-global/lib/node_modules/pi-fork"] }
  ]
}
```

Select your pack (`"packs": ["~/code/pi-mine"]`). The pack it forks joins by itself when yolo ships
it, and must be in `packs` too when it does not. The original pack keeps everything else it provides,
such as settings, skills and launch flags. Your pack only replaces the program.

`produces` lists what the build leaves in the jail's home. One entry must be the program itself
under `.local/bin`, `.npm-global/bin` or `go/bin`. List the folder it installs into as well, so a
new build replaces the old one whole instead of merging into it. The build must install a copy:
`npm install -g .` installs a link back to the build's own folder, which is gone once the build
ends, so yolo refuses that build and says why.

- There is nothing to install first. The first launch pins the fork to the commit its `ref` names
  then, and says so:

  ```text
  pinned fork pi-mine/pi at 1a2b3c4d (git+https://github.com/you/pi-fork?ref=main); `--update` or `yolo pack update` moves it
  ```

  Later ordinary launches keep that commit even after your branch moves on. A host `--update` fresh
  launch or `yolo pack update` re-resolves the fork pin; an in-jail `--update` that needs a mutable
  source pin names the host command instead.
  pin. `yolo pack status` shows the pin and whether it is built. The pin is kept in
  `~/.config/yolo-jail/forks.lock.json`: on another machine with the same config and that file, the
  first launch fetches the same commit, so both machines run the same build.
- That launch builds the commit once, on this machine, in a jail of its own that gets none of
  your credentials, host files or services, and none of your `mise_tools`. It has the image's
  tools, your `packages` and any Node version the forked program's pack asks for; a build that
  needs another tool fetches it in its `build` command. Every later launch, in any workspace,
  reuses the build.
  Each launch prints the commit your fork is built at.
- If the pin cannot be made (your fork's repository is unreachable, or the `ref` names nothing), the
  jail starts without that program, and the launch says why and what to do. `yolo pack install` also
  pins it, at a terminal where git can ask for an ssh host key or passphrase.
- If the build fails, the jail starts without that program, and running it says why. yolo never
  falls back to the original program under your fork's name.
- `yolo capture <program>` rebuilds it on demand, for example after an image update.
- `yolo host -- <program>` runs the same build outside a jail, on Linux. yolo keeps its own copy for
  host agents, moved out of the jail's home, and pins and builds it first if nothing has yet. A
  build that can only run in a jail, such as one that compiles the jail's home path into a binary,
  stays in jails: `yolo host` stops and says so, rather than running a copy from your PATH. To use
  your own copy at the host, leave the pack out with `"host_floor": {"<pack>": false}` in your user
  config.

This works on podman and on Apple Container 1.1.0 or later. On `macos-user`, and on older Apple
Container, the fork's program is not delivered yet, and the launch says so. On a Mac, `yolo host`
stops and says why; `"host_floor": {"<pack>": false}` lets it run your own copy instead.

If your fork is a few changes on top of someone else's project, yolo can keep them on its newest
release for you instead: see [Follow an Upstream with a Patch Series](patch-series.md).

## Select only skills from a pack

On the host, narrow a user pack without writing a wrapper pack or installing it per project:

```bash
yolo pack add local/corpus --skills-dir skills/core-tools --only skill-a --only skill-b --dry-run
yolo pack add local/corpus --skills-dir skills/core-tools --only skill-a --only skill-b
yolo config check
yolo pack explain local/corpus
```

This writes a table in **user TOML**, so every workspace gets the same selection. The named
folder is `~/.config/yolo-jail/packs/corpus`; for a Git repository, use
`yolo pack add --git <url> --ref <ref>` with the same options. A project cannot select packs.

With `--skills-dir`, yolo treats that directory as raw data and never interprets its original
manifest or plugin metadata. A direct regular `SKILL.md` makes the root one candidate;
otherwise only immediate marker-bearing children qualify. Shared or deeper directories are
not recursively registered. `--only` and `--exclude` are repeatable candidate-name globs,
not the old JSONC file filters.

For a real pack's declared skills, omit `--skills-dir` and use `--only`/`--exclude` instead.
Typed mode keeps selected ordinary folders, including a folder with no marker, and selected
manifest sourced aliases with their own ref, license and audience. A markerless folder is
delivered but not counted as a discoverable skill; yolo never invents its marker.
`--subdir` relocates the pack root; **without skill selectors it still imports the whole pack**.

A scoped import activates no programs, needs, hooks, briefing, native plugins, MCP services or
host capabilities. Supporting bytes remain inert. A payload-backed script can read repository
siblings through some linked paths, but skill deployment does not install workspace dependencies
or guarantee arbitrary relative marker paths.

Explain prints every matching import tuple, its origin/index/roots, and what available content
proves. It writes/fetches nothing. Missing content is “selection not yet verified; resolved at
next launch”, with no guessed names or counts. Scoped Check and scoped add dry-run use the same
pure inspection; ordinary whole entries in Check keep their existing readiness staging.
Optional local license/support details can remain “determined at publication”.

A stale selector refuses rather than silently widening. After a branch advances, the new pin
may be recorded even though selection fails; the old running tree stays unchanged. Use
`yolo config set` to edit the selector or choose a known-good full ref, check the config, then
restart. A selection-only change also needs a restart, not a refetch.

```bash
yolo config why 'packs[1].skills.only[0]'
yolo config set 'packs[1].skills.only[0]' '"skill-c"' --user
yolo pack remove local/corpus --skills-dir skills/core-tools
```

Removal drops at most one authored tuple. Ambiguous handles name indices and roots to choose;
fragment origins and neighboring comments are preserved. Legacy filtered files are retained by
migration: old path globs and modern name selectors are not automatically equivalent.

This is distinct from a pack manifest's Git-sourced `[[skills]]` rows and from workspace
`skill_imports`; it adds no broker grant or workspace pack authority. The
[implemented reference](https://github.com/mschulkind-oss/yolo-jail/blob/main/docs/reference/toml-scoped-pack-imports/README.md)
explains the shared carriers, recorded replay and verification limits.

## Check a pack before you trust it

A pack can read files from your home, install programs, and ship a loophole that runs a program on
your machine. yolo does not ask you to approve any of it: writing the pack into your own user config
is the approval. So read what a pack claims first:

```bash
yolo pack footprint ~/code/my-pack    # every claim a pack makes, before you select it
yolo pack ls                          # the packs you selected, and what each delivers
yolo pack explain claude              # which files a pack delivers, and which filters dropped
```

`footprint` takes a shipped pack's name or a folder, so to review a git pack, clone it and run
`footprint` on the clone. Every launch also prints what each pack reads from your machine, and prints
anything that runs on your machine just before it starts.

> [!IMPORTANT]
> **Following a branch means trusting every future commit on it.** A `?ref=main` picks up whatever
> the author pushes, within the hour, with no further question. Pin a tag or a commit for any pack
> that carries code, and especially for one that ships a loophole.

## Skills and house rules

A **skill** is a folder of instructions, and sometimes scripts, that an agent can find and use when
a task calls for it; each has a `SKILL.md` file at its top. A pack's `skills/` folder reaches every
agent you selected, each in the place that agent looks for skills, so you write a skill once instead
of copying it into every agent's home.

A pack's **briefing** is its house rules: every Markdown file directly inside its `briefing/` folder
is appended to each agent's instructions file, such as `CLAUDE.md` or `AGENTS.md`. A pack's own
root `AGENTS.md` or `CLAUDE.md` is never delivered, because agents read those as instructions for
working on the pack's repository itself.

When two sources ship a skill with the same name, the later one in this list wins:

1. skills kept in the project itself, such as `.claude/skills`
2. yolo's built-in skills
3. your selected packs, in the order you list them
4. your **local pack**, `~/.config/yolo-jail/local/`

The local pack is the one place for your own skills and house rules. yolo selects it automatically
whenever that folder exists, and it reaches every jail and, with `yolo host apply`, your own machine.

### Take only part of a pack

A pack entry can narrow what it delivers with `only` and `exclude`. Each pattern matches a folder, so
naming a skill's folder takes the whole skill:

```jsonc
{
  "packs": [
    {
      "source": "~/packs/team-skills",
      "only": ["skills/rust-*"],
      "exclude": ["skills/rust-internal"]
    }
  ]
}
```

A pack can also address a skill or a briefing file to named agents only; `yolo pack ls` shows who
receives what.

## Keep agents and packs up to date

**Agents keep themselves current.** When you first type an agent's name after a jail starts, its
launcher checks for a newer release, installs it if there is one, and then starts the agent. It
checks once per jail start, so relaunching the jail is how you pick up a release that came out
since. A jail that never starts an agent never downloads one. To freeze agents at what is installed, set this in your
user config:

```jsonc
{
  "agent_updates": false                          // every agent
  // or: "agent_updates": { "*": true, "claude": false }   by pack name
}
```

**A failed update stops the agent, it does not quietly run the old one.** When yolo finds a newer
release and cannot install it, the agent does not start: yolo says what failed and how to retry,
and gives the command that runs the version you already have, such as
`YOLO_USE_INSTALLED=claude claude` inside a jail or `YOLO_USE_INSTALLED=claude yolo -- claude` from
your shell. Offline is not a failure: when yolo cannot reach the release server at all, it starts
the installed version and says it could not check.

This controls yolo's updater only. An agent with its own updater runs it on its own schedule:
Claude Code, Copilot and Antigravity (`agy`) all do, so `agent_updates: false` does not freeze
them, and yolo cannot tell you which version of any of them ran. To freeze Copilot too, add
`{"COPILOT_AUTO_UPDATE": "false"}` to `env_sources`; for Claude Code, add
`{"DISABLE_AUTOUPDATER": "1"}`.

**Pi's extensions update the first time you start pi in a jail**, before it opens, and again after each jail relaunch. To have pi
start at once instead, set `"agent_updates": { "pi": "next-launch" }`: the update then runs in the
background while you work, and your next launch of pi uses it. yolo names the update's log as pi
starts, and tries a failed update again in front of pi at the next launch, stopping if it fails again. New extensions in
your pi settings still install before pi opens. On Podman, the same setting also controls extensions
a pack declares for yolo to build: with `"next-launch"`, a fresh launch starts with its last good
build or fallback and tries any due update in the background. A later launch can use the new build
if that update finishes first; the setting does not guarantee the next launch gets a newer one.

If the refresh did not reach a hand-added package, two pi sessions started together may both
install it, as on any machine pi runs on.

**Packs move only when you say so**, apart from a branch ref as described above:

```bash
yolo pack install    # fetch every configured git pack now
yolo pack update     # re-fetch every git pack, and update agents installed from npm
yolo pack status     # the commits you are pinned to, and whether config and lockfile agree
```

Run `yolo pack update` inside a jail to update its agents on demand, even with `agent_updates` off.

**Project tools never update behind your back.** `packages` and `mise_tools` stay at the versions
you declared; see [Packages and Tools](packages-and-tools.md).

## Next steps

- [Settings Across Agents](agent-settings.md): how yolo turns your packs and config into each
  agent's own files.
- [Writing your own pack](migrating-to-packs.md): move your skills, house rules and settings into a
  pack you own.
- [Claude Code Plugins and Mods](claude-plugins-and-mods.md): bring a Claude Code plugin or mod into a
  jail, as a pack or through Claude Code's own installs.
- [Providers and Models](providers-and-models.md): point an agent at a different model service.
