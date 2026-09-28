# Packs and Skills

A **pack** is a reusable bundle of agent setup: a coding agent and its settings, skills, house
rules, tools, model providers, or a connection to your host. Everything a jail has beyond a bare
shell arrives as a pack, and nothing is selected for you. This page covers choosing packs, checking
what a pack does before you trust it, sharing skills across agents, and keeping agents up to date.

To write a pack of your own, see [Writing your own pack](migrating-to-packs.md).

## Choose your packs

Packs go in your **user config**, `~/.config/yolo-jail/config.jsonc`, under `packs`:

```jsonc
{
  "packs": ["claude", "codex", "guardrails"]
}
```

A project's `yolo-jail.jsonc` cannot select packs. The project folder is writable from inside the
jail, so an agent could otherwise hand itself new skills, programs or host access. Run `yolo check`
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
at it with `yolo -p <name> -- <agent>`: `zai`, `openrouter`, `kilo`, `cerebras` and `llamacpp`.
[Providers and Models](providers-and-models.md) explains them.

**Host connections.** Each ships a **loophole**, a named connection from the jail to one capability
on your host: `aws-auth`, `serial`, `journal`, `host-processes`, `audio` and `cgroup-delegate`. Most
also need turning on by name. [Host Access and Loopholes](loopholes.md) lists what each does.

**Habits.** `guardrails` makes recursive `grep` and `find` refuse and point at `rg` and `fd`.
[Packages and Tools](packages-and-tools.md#blocked-tools) covers blocking tools.

Some packs bring others with them. The `claude`, `codex` and `pi` packs bring `openai-auth`, the
shared ChatGPT login service, and `claude` also brings `aws-auth` and `wire-bridge`. The launch
prints the full set it resolved, so what you see there can be longer than what you typed.

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

## Check a pack before you trust it

A pack can read files from your home, install programs, and ship a loophole that runs a program on
your machine. yolo does not ask you to approve any of it: writing the pack into your own user config
is the approval. So read what a pack claims first:

```bash
yolo pack footprint ~/code/my-pack    # every claim a pack makes, before you select it
yolo pack ls                          # the packs you selected, and what each delivers
yolo pack explain claude              # which files a pack delivers, and which filters dropped
```

Every launch also prints what each pack reads from your machine, and prints anything that runs on
your machine just before it starts.

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

**Agents keep themselves current.** When you type an agent's name, its launcher checks for a newer
release at most once an hour, installs it if there is one, and then starts the agent. A jail that
never starts an agent never downloads one. To freeze agents at what is installed, set this in your
user config:

```jsonc
{
  "agent_updates": false                          // every agent
  // or: "agent_updates": { "*": true, "claude": false }   by pack name
}
```

This controls yolo's updater only. An agent with its own built-in updater, such as Copilot, still
follows its own setting.

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
- [Providers and Models](providers-and-models.md): point an agent at a different model service.
