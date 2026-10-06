# Writing Your Own Pack

Once you have tuned an agent the way you like it, with house rules, skills and settings, you can
write that setup down as a **pack**: a folder you own, keep in git, and share. Every jail you launch
then gets it, and yolo can apply the same setup to your own machine too.

This guide has two parts:

1. **[Move your setup into a pack](#part-1--move-your-setup-into-a-pack)**: scaffold one, add your
   rules, skills and settings, check it and select it.
2. **[Manage your host](#part-2--manage-your-host)**: write the same configuration into your real
   home, for agents you run outside any jail.

Before you start: a pack is what you select under `packs`, and [Packs and Skills](packs-and-skills.md)
explains how packs are chosen, trusted and updated. `yolo pack ls` shows what you have selected
today, and `yolo describe` summarizes the whole resolved environment.

---

## Part 1 — Move your setup into a pack

### Step 1: scaffold a pack

```console
$ yolo pack init ~/code/my-agent-pack
  create briefing/my-agent-pack.md
  create skills/example/SKILL.md
  create README.md
```

That gives you:

```text
my-agent-pack/
├── briefing/
│   └── my-agent-pack.md   # house rules, appended to every agent's instructions
├── skills/
│   └── example/SKILL.md   # a skill; its YAML frontmatter needs a name and a description
└── README.md
```

Put your house rules in `briefing/`. Every `*.md` file directly inside it is delivered, in filename
order, so you can split them across files. Add skills as `skills/<name>/SKILL.md`. This layout needs
no manifest at all.

> [!NOTE]
> **A root `AGENTS.md` is not delivered.** Agents read `AGENTS.md`, `CLAUDE.md` and `GEMINI.md` as
> instructions for working on the repository they sit in, and a pack is usually a repository, so
> yolo leaves those files alone. Move your rules into `briefing/`, for example with
> `mkdir briefing && git mv AGENTS.md briefing/house-rules.md`. `yolo pack lint` names any such
> file it will not deliver.

**There is no import.** `pack init` creates an empty skeleton; it does not read your existing
`~/.claude/settings.json` or skills. You copy over what you want yolo to manage. Settings you change
inside a jail are the exception: `yolo config promote` moves them into your local pack for you (see
[Settings Across Agents](agent-settings.md#see-what-yolo-wrote-and-why)).

### Step 2: add a manifest when you need more

To carry settings, environment variables or a tool as well, add a `pack.json` with a `contributes`
list: one entry per effect, each with a `kind`. The kinds you are most likely to want:

| Kind | What it contributes |
|---|---|
| `config` | a settings file yolo composes, such as `~/.claude/settings.json` |
| `config-overlay` | keys set on a settings file another pack owns |
| `env` | fixed environment variables |
| `program` | a tool yolo installs and keeps on the jail's PATH |
| `requires` | a tool that must already be there; yolo installs nothing |
| `skills`, `briefing` | skills and house rules addressed to particular agents |
| `files` | a folder of files the pack owns, placed read-only in the agent's home |
| `reads-host`, `mount` | a host file or folder, read-only in the jail |
| `loophole` | a connection to a capability on your host ([Writing a Loophole](writing-loopholes.md)) |

`yolo pack --help` lists every kind, and `yolo config-ref` documents each field.

Here is a pack that sets one Claude Code setting and one environment variable:

```jsonc
{
  "name": "my-agent-pack",
  "contributes": [
    { "kind": "config-overlay", "surface": "claude/settings",
      "config": { "managed": { "autoUpdaterStatus": "disabled" } } },
    { "kind": "env", "vars": { "MY_FLAG": "on" } }
  ]
}
```

A key under `managed` is one yolo sets every time it writes the file, and every other key in the
file is left as it was. If the agent's own pack manages the same key, the agent pack's value wins;
`pack lint` says so as "owner still wins".

Adding a manifest never switches off your `briefing/` folder. To send one extra file to one agent
only, name it and its audience:

```jsonc
{ "kind": "briefing", "from": "prose/pi.md", "agents": ["pi"] }
```

#### Give pi your extensions, themes and prompts

Put them in one folder, laid out the way pi lays out a package, and address that folder to pi with
one entry:

```text
my-agent-pack/
└── files/pi/
    ├── extensions/   # each .ts or .js file is one extension
    ├── themes/       # each .json file is one theme
    └── prompts/      # each .md file is one prompt template
```

```jsonc
{ "kind": "files", "agents": ["pi"], "from": "files/pi" }
```

yolo places the folder at `~/.pi/agent/yolo-packs/<your pack>`, read-only in a jail, and adds it
to the `packages` list in pi's `settings.json`, beside the packages you installed yourself, in a
jail and at `yolo host apply`. Adding a file to the folder needs no manifest edit. Drop the pack
from `packs` and the next launch, or the next `yolo host apply --assert`, takes the folder out of
the list.

The folder needs no `package.json`. If you add one, for npm dependencies say, either leave out its
`pi` key or list every folder in it, because a `pi` key tells pi to load only what it lists.
`yolo pack lint` warns about a folder holding none of `extensions/`, `themes/`, `skills/`,
`prompts/` or a `package.json`. Skills are better shipped in the pack's `skills/` folder, which
reaches every agent, and `yolo pack lint` says so when the folder holds a `skills/` of its own.

### Step 3: check it

```console
$ yolo pack lint ~/code/my-agent-pack
✓ pack ok — 4 file(s) stage
delivers:
  briefing       briefing/my-agent-pack.md → every agent (implicit broadcast)
  skills         skills/ → every agent (implicit broadcast)
declares 2 claim(s):
  config-overlay claude/settings  contributes keys (owner still wins)
  env            MY_FLAG  =on
```

`pack lint` checks the folder and the manifest and reports every problem, not just the first. A
problem it reports would also stop a launch, so fix it here. It then prints the pack's
**footprint**: everything the pack claims, and every file it delivers.
`yolo pack footprint ~/code/my-agent-pack` prints the same claims plus any conflict with your other
packs.

### Step 4: select it

Add it to your user config, beside your agent:

```jsonc
// ~/.config/yolo-jail/config.jsonc
{
  "packs": [
    "claude",
    "~/code/my-agent-pack"
  ]
}
```

A local folder is read in place, so there is nothing to fetch. Run `yolo check`.

### Step 5: launch, and confirm it took effect

```bash
yolo -- claude
```

Your house rules are in Claude's `CLAUDE.md`, your skills in its skills folder, and your settings in
its `settings.json`. `yolo config render claude --explain` shows which pack set each key.

### Share it

Push the folder to a git repository and select it by address, pinned to a tag:

```jsonc
"packs": ["claude", "git+ssh://git@github.com/me/dotpacks//agent?ref=v1"]
```

The next launch fetches it on the host and records the commit; `yolo pack install` does it ahead of
time. Anyone who selects a pack trusts it with everything its footprint lists, so pin tags for
packs that carry code. [Packs and Skills](packs-and-skills.md#check-a-pack-before-you-trust-it)
covers what a ref means and how to review a pack.

---

## Part 2 — Manage your host

The same packs can configure the agents you run on your own machine, outside any jail. Nothing is
confined there, so yolo treats it as configuration management: it writes the settings files, skills
and house rules your packs declare into your real home, keeps your agents' permission prompts on,
and warns before changing a value you set yourself.

Your own config reaches those files too, as it reaches a jail's. The MCP servers under
`mcp_servers`, the language servers under `lsp_servers`, the model providers your packs and
`providers` declare, and the model your `profile` key selects are written into each agent's settings on
your machine. Pi, for example, gets the same `openai-codex` model list a jail's pi has. Three
things stay behind, and the report names each one: an MCP preset, a server whose command is a path
that exists only inside a jail (such as `/workspace/...`), and a profile that needs a jail's
service, such as claude's `codex` profile.

**First, let yolo write there.** Your real home is yours until you say otherwise:
`host_management` is `"none"` by default, and `yolo host apply` refuses. Set it in your user config,
`~/.config/yolo-jail/config.jsonc`:

```jsonc
"host_management": "own"
```

### Step 1: preview

`yolo host apply` is a dry run unless you add `--assert`. It prints what would change and writes
nothing:

```console
$ yolo host apply
host apply — dry run into /home/me; nothing is written
  claude/settings      would render  /home/me/.claude/settings.json
    ⚠ would overwrite your existing value for: permissions.defaultMode
  ⚠ 3 skills in your agent skill dirs are yours, not yolo's, and would move into your local pack: house-rules, review, triage
An --assert would complete.
```

What to look for:

- **Values it would overwrite.** Each key a pack manages that differs from what you have is named.
- **Skills and house rules you already have.** The first `--assert` moves them into your local
  pack, `~/.config/yolo-jail/local/`, after asking, and from then on yolo writes every agent's
  skills folder from your packs. Nothing is deleted; anything that cannot be moved is archived.
  Afterwards, add a skill to `~/.config/yolo-jail/local/skills/`, not to an agent's own folder,
  or the next apply will offer to move it again.
- **What applies at launch only, and what does not apply at the host.** Some of what your packs
  declare reaches an agent only when yolo starts it, such as pack environment variables and the
  credential helper a loophole like `aws-auth` opens: `yolo host -- <agent>` delivers them, and
  the apply writes no file for them. Other kinds, such as `state` and `mount`, only mean
  something in a jail, and so do settings in your user config such as `mounts`, `network`,
  `resources` and `packages`, and a loophole you wrote yourself with a `command`. The report
  names both groups on one line.

`--verbose` lists every file it checked. The dry run names the keys it would overwrite but does not
print the full content, so before your first `--assert` read what your packs manage:
`yolo config render claude --at host` prints the exact file `--assert` would write.

### Step 2: apply

```console
$ yolo host apply --assert
```

yolo writes each file from your packs. The first time it writes one, it copies the file as it was
into `~/.local/share/yolo-jail/archive/config/` and keeps the keys already in it as your own edits,
laid back over every render, so a key you set stays set unless a pack manages it. Run it again
whenever you change a pack; it is safe to repeat. To keep a key you set by hand in every jail too,
`yolo config promote` declares it in your local pack.

Two things to know:

- **Server and provider lists are replaced, not merged.** yolo writes an agent's MCP server list,
  Copilot's LSP list and pi's provider list whole from your config, as it does in a jail. So a
  server you added through the agent itself, such as with `claude mcp add`, is dropped. Each one
  is named, and yolo asks before dropping anything the first time it writes a list. To keep one,
  add it to your user config under `mcp_servers`, `lsp_servers` or `providers`. That reaches every
  agent. To give it to one agent only, add a `config-overlay` in your local pack.
- **Your selected model is written once, not forced.** When your `profile` key picks a profile, yolo
  writes that profile's provider and model into the agent's settings. If you then pick another
  model in the agent, such as with `/model`, your pick stays. When you remove the selection, yolo
  clears only the values it wrote.
- **Variables are not expanded.** A `${TAVILY_API_KEY}` in pack content is written literally, and
  the apply warns about it, because writing a secret into a file yolo does not own would defeat
  `env_sources`. Use `yolo host -- <agent>` to hand an agent its keys (Step 4).

**Taking yolo back out.** Set `host_management` back to `"none"` (or delete it), then run
`yolo host apply --revert`. It removes the values yolo wrote, using the record it keeps of what it
wrote, and never touches a value you set yourself, including one you changed after yolo wrote it
and one you added inside a setting yolo also writes; the dry run names each value it keeps and
why. It is a dry run until you add `--assert`. It removes what yolo wrote; to see the file as it
was before yolo first wrote it, look in `~/.local/share/yolo-jail/archive/config/`.

**Choosing how much yolo owns.** `host_management` in your user config decides it:

| Value | What yolo does in your real home |
|---|---|
| `"none"` (the default) | Writes nothing; `yolo host apply` refuses |
| `"own"` | Writes each file whole from your packs, and keeps your own edits by recording them and laying them back over each render |

`"assert"`, which set only the keys your packs declare, is retired. A config that still says it is
refused, with a message naming the two values. A home it wrote into stays exactly as it was: set
`"own"` to have yolo take the files over (it copies each one first), or keep `"none"` and run
`yolo host apply --revert` to take yolo's keys out.

### Step 3: make sure the host has the tools your packs need

In a jail, tools come from the image and the packs. On your own machine they are whatever you have
installed, so `yolo check-deps` checks for every tool your packs declare and prints the install
command for your package manager:

```console
$ yolo check-deps
✓ psql             /opt/homebrew/bin/psql
✗ redis            MISSING → brew install redis
```

It also writes a manifest for your package manager, such as a `Brewfile`, and never installs
anything itself. `yolo host apply --assert` stops at a missing tool and offers to run its install
command; answering no writes nothing. For a tool with its own install script, that command
downloads the script, checks that it is a script and not a web page or a program, and only then
runs it, with no terminal, so the installer cannot stop to ask you anything.

### Step 4: run an agent with its keys and profile

A settings file cannot carry a secret, so keys and profiles reach a host agent through its
environment:

```bash
yolo host -- claude              # run claude with its composed environment
yolo host -p zai -- claude       # the same, on the zai profile, this launch only
eval "$(yolo host env)"          # the same environment, in your current shell
```

`yolo host --with-credentials zai -- <command>` hands any command one provider's key, and nothing
else, for that run.

### Step 5 (optional): seal it

`yolo apply --sealed` refuses if anything you did not declare shaped the environment, such as a
`yolo-jail.local.jsonc` override or edits recorded inside a jail that you have not promoted. Once it
passes, `yolo describe --hash` is a fingerprint of the whole environment you can pin in CI or
compare with a colleague's.

---

## Not built yet

- **Guest confinement**, a separate user account between the jail and your own machine. See
  [Where Agents Run](confinement.md#guest-in-development).
- **Importing existing settings into a pack.** Skills and house rules are moved for you on the first
  `yolo host apply --assert`; settings files are copied by hand.
- **Installing a `macos-user` sandbox's programs ahead of time.** A jail on a container runtime
  installs every agent and tool its packs declare when it starts; on `macos-user` each one still
  installs the first time you run it.

## Quick reference

| You want to… | Command |
|---|---|
| Start a pack | `yolo pack init <dir>` |
| Check a pack | `yolo pack lint <dir>`, `yolo pack footprint <dir>` |
| See what is selected and delivered | `yolo pack ls`, `yolo pack explain <name>` |
| Fetch or update git packs | `yolo pack install`, `yolo pack update` |
| See the resolved environment | `yolo describe` (`--json`, `--hash`) |
| Preview the host render | `yolo host apply`, `yolo config render <agent> --at host` |
| Write it into your real home | `yolo host apply --assert` |
| Take yolo back out | `yolo host apply --revert`, then `--assert` |
| Check the host has the tools | `yolo check-deps` |
| Run a host agent with its keys | `yolo host -- <agent>` |
| Inside a jail: is a restart needed? | `yolo config drift` |
