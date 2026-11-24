# Settings Across Agents

Every coding agent keeps its settings in its own files, in its own format: Claude Code in
`~/.claude/settings.json`, Codex in `~/.codex/config.toml`, and so on. yolo writes those files for
you. It combines your config and your selected packs into one description, then writes each
selected agent's own files from it every time a jail starts. You declare a setting once, and every
agent that supports it gets it.

Not every setting translates to every agent. When an agent has no equivalent, yolo leaves it out
for that agent rather than inventing one.

## Where a setting comes from

For each file an agent reads, yolo stacks these sources, later ones winning:

1. **The agent pack's defaults**, such as the settings that turn permission prompts off in a jail.
2. **Your own copy of the file**, for the agents whose pack reads it. The `claude` pack, for
   example, starts from your own `~/.claude/settings.json`, read-only.
3. **Your config and your other packs**: MCP servers, language servers, providers, and keys a pack
   sets on another agent's file.
4. **Edits made inside the jail.** When you or the agent change a setting in a running jail, such
   as with Claude's `/config`, yolo records the change when the jail stops and lays it back over
   the next render, so it is not lost when the file is written again.

Your config itself comes in two files: your **user config**, `~/.config/yolo-jail/config.toml`,
for personal defaults, and the project's `yolo-jail.toml` for what one project needs (the legacy
`.jsonc` spellings are still read).
[Configuration](../reference/configuration.md) says which keys go where.

## What translates to which agent

| Setting | Claude Code | Codex | Copilot | opencode | pi | agy |
|---|---|---|---|---|---|---|
| Skills and house rules from packs | Yes | Yes | Yes | Yes | Yes | Yes |
| MCP servers (`mcp_servers`, and packs such as `chrome-devtools`) | Yes | Yes | Yes | Yes | Yes | Yes |
| Language servers (`lsp_servers`) | Yes | No: Codex has no LSP support | Yes | Not yet | Not yet | No: agy has no LSP support |
| Model provider and profile (`-p`) | Yes | Yes | Yes | Yes | Yes | No |
| Permission prompts off in a jail | Yes | Yes | Yes | Yes | Yes | Yes |

An **MCP server** gives an agent extra tools through the
[Model Context Protocol](https://modelcontextprotocol.io/introduction); a **language server** gives
it code intelligence such as go-to-definition. [MCP and LSP](mcp-and-lsp.md) covers both.
[Providers and Models](providers-and-models.md) covers profiles.

## Keep, ignore or replace a repository's own instruction files

A repository can carry instructions for coding agents: `AGENTS.md`, `CLAUDE.md`, `CLAUDE.local.md`
and files like them. An agent reads whatever the checked-out repository says, so an agent in your
jail follows rules you did not write. `repo_instructions` lets you decide what happens to them.

```toml
# ~/.config/yolo-jail/config.toml, or the project's yolo-jail.toml
[repo_instructions]
mode = "ignore"
```

| Mode | What happens |
|---|---|
| `keep` | The default. Agents read the repository's files, as they do today. |
| `ignore` | Each agent that has a setting for it stops reading them. |
| `replace` | The same, and the text of a file you name joins the agent's briefing in their place. |

To replace, name the file with `replace_with`. In your own config that is an absolute path or one
starting with `~/`. In a project's `yolo-jail.toml` it is a path inside the project, and a link or a
path that leaves the project is refused. You can set it from the command line:

```console
$ yolo config set repo_instructions.mode replace
$ yolo config set repo_instructions.replace_with ~/rules/mine.md
```

Your own config and the project file can both set it, and the later layer wins as for any other
setting. `yolo config why repo_instructions.mode` says which layer set it.

### Which agents can do it

yolo uses each agent's own setting. It never empties, hides or replaces a file in your project, so
`git status` in the jail shows the project as it is.

| Agent | `ignore` and `replace` |
|---|---|
| Claude Code | Yes. yolo adds exclusions for the project's files to `claudeMdExcludes`, next to any you wrote. |
| Codex | Yes. yolo sets `project_doc_max_bytes` to 0. |
| pi | Yes, except `.pi/SYSTEM.md` and `.pi/APPEND_SYSTEM.md`, which pi still reads and the launch names. |
| Copilot, agy, muse, opencode, oh-omp | No. They have no setting that stops them reading these files. |

The agent's own files in your home, where yolo puts its briefing, are never excluded, so the agent
still reads what yolo tells it about the jail.

### When an agent cannot do it

The launch still goes ahead. For each agent you selected, it prints one line saying what happened,
and the line cannot be turned off:

```text
repo instructions: claude: ignoring 3 files (AGENTS.md, CLAUDE.md, +1 more) through claudeMdExcludes
repo instructions: agy: NOT ignored, it still reads GEMINI.md, AGENTS.md; agents that can ignore them: claude, codex, pi; to stop this notice: yolo config set repo_instructions.mode keep
```

If an agent that cannot ignore the files is one you rely on, either pick one of the agents above,
or set the mode back to `keep`, which silences the notice.

### On your own machine

`yolo host -- <agent>` reads your **user** config only. For Claude Code and Codex it hands the agent
the same setting as a command-line flag for that one run, and for pi its flag, and prints the same
line. Your user config's `replace` text reaches the agent through the briefing the launch itself
writes into your real home (the same automatic update that keeps the rest of your host setup
current), so you do not run `yolo host apply` for it; `yolo host apply` writes the same text. Neither
writes anything about one project into your real home, which every project shares. If the launch says
a dropped pack's leftovers are still waiting, that does not hold the update back: answer it with
`yolo host apply --assert` when you like.

## Codex's background copy

Since version 0.157, Codex starts a second copy of itself in the background, and the `codex` you
type talks to that copy. The copy does not update when Codex does, so it can show an older model
list, and it installs updates to itself on its own schedule.

yolo turns off Codex's background copy in the sessions it launches: in every jail, `macos-user`
included, and in `yolo host -- codex`. Codex there works the way it did before 0.157, with the
model list of the Codex yolo installed.

`codex agents` needs the background copy and starts it itself. Under `yolo host -- codex`, Codex
refuses it. In a jail it still works, and it starts the background copy, which every later Codex
session in that jail then uses until the jail stops. On `macos-user` it keeps running after the
session ends, until you restart the Mac. `codex queue` only reaches a background copy that is
already running.

A Codex you run yourself is untouched: yolo never changes this in your own `~/.codex`, even when
`yolo host apply` manages your other Codex settings.

Two things can stay behind:

- On `macos-user`, a background copy that a session started, before you upgraded or with
  `codex agents`, keeps running until you restart the Mac, and Codex in that project keeps using
  it until then.
- A project where Codex ran in a jail keeps the background copy's files, about 400 MiB for each
  Codex version it saw, in `.yolo/home/codex/packages/app-server-daemon` (`.yolo/home/.codex/…` on
  Apple Container). Only `codex agents` still starts them, and it starts the copy kept there,
  which can be an older Codex than yours. Delete that folder to get the space back; a later
  `codex agents` copies the Codex you have now. For `yolo host -- codex`, yolo removes its own
  copy for you.

## See what yolo wrote, and why

```bash
yolo config ls                        # every file yolo composes, and which sources feed it
yolo config render claude             # what a launch would write for Claude, without writing
yolo config render claude --explain   # which source set each key
yolo describe                         # the whole resolved environment, in one summary
```

To manage an edit made inside a jail:

```bash
yolo config diff claude/settings      # the edits recorded for that file
yolo config reset claude/settings     # throw them away; the next launch writes the file fresh
yolo config promote claude            # on the host: move them into your local pack, for every jail
```

`promote` writes the recorded edits into your **local pack**, `~/.config/yolo-jail/local/`, so the
setting becomes part of your declared setup and reaches every jail, and your own machine if you use
`yolo host apply`. See [Packs and Skills](packs-and-skills.md#skills-and-house-rules) for the local
pack.

## The same settings on your own machine

yolo can also write these files into your real home, for agents you run outside any jail:
`yolo host apply` shows what would change, and `yolo host apply --assert` writes it. On your own
machine the agents keep their permission prompts on. [Writing your own pack](migrating-to-packs.md#part-2--manage-your-host)
walks through it.
