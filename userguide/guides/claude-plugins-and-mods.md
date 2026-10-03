# Claude Code Plugins and Mods

A Claude Code [**plugin**](https://code.claude.com/docs/en/plugins/overview) is a folder of extras
for Claude Code, such as skills, commands, hooks and tool servers, with a manifest at
`.claude-plugin/plugin.json`. A [**mod**](https://code.claude.com/docs/en/plugins/mods/overview) is a
plugin whose JavaScript or TypeScript code runs inside Claude Code and changes how it looks and
behaves: a pane beside the transcript, a count beside the spinner, a rule for tool calls. Claude Code
calls that code file the mod's **hooks module**.

This page covers the ways to bring a plugin or a mod into a jail today, what yolo tells you about it
at launch, and what is planned.

## Four ways to bring one in

| Way | Reaches | Good for |
|---|---|---|
| [Wrap it as a pack](#wrap-it-as-a-pack) | Every jail, and your own machine with `yolo host apply` | A plugin you want everywhere, at a version you choose |
| [Keep it in the project](#keep-it-in-the-project) | Every jail of that project, and everyone who clones it | A mod for one repository |
| [Enable it on your machine](#use-the-plugins-you-enable-on-your-machine) | Every jail, each project downloading its own copy | A marketplace plugin you already use |
| [Install it in a jail, then share it](#install-it-in-a-jail-then-share-it) | That project, then every jail | Trying a plugin before you keep it |

### Wrap it as a pack

A [pack](packs-and-skills.md) can carry a plugin. yolo calls a plugin carried this way a **wrapped
plugin**. `yolo pack init --from-plugin` builds the pack for you from the plugin's folder:

```console
$ yolo pack init --from-plugin ~/src/first-mod ~/code/first-mod-pack
  create pack.json
  create skills/first-mod/ (3 file(s) from the plugin)
  create README.md

Wrapped plugin first-mod (from /home/you/src/first-mod)
  carries:
    hooks          runs code at agent lifecycle events   ⚠ RUNS CODE  (hooks/hooks.json)
```

Then select it in your user config, `~/.config/yolo-jail/config.jsonc`, and run `yolo check`:

```jsonc
{
  "packs": ["claude", "~/code/first-mod-pack"]
}
```

Every jail then gets the whole plugin, manifest, hooks and all, read-only in `~/.claude/skills/`,
and Claude Code loads it from there with no further step. It keeps its own name, so its skills run
as `/first-mod:<skill>`. `yolo host apply --assert` writes the same plugin into your own
`~/.claude/skills/`; `yolo host apply` alone shows what it would change
([Your own machine](confinement.md#your-own-machine)).

- **It is a copy.** The pack does not follow the plugin's own updates. To refresh it, delete the
  pack's `skills/first-mod/` folder and run the same `yolo pack init --from-plugin` again.
- **To share it,** keep the pack in a git repository and list it as a git pack. It is then pinned
  and updated like any other pack; see [Packs from elsewhere](packs-and-skills.md#packs-from-elsewhere).
- **Listing the plugin's own repository under `packs` does not work yet.** With no `pack.json`, its
  hooks and mod code are left out. Wrap it instead.

### Keep it in the project

A plugin in the project's own `.claude/skills/<name>/` folder loads in every jail of that project.
Claude Code loads a project's plugins only in a folder you have trusted, and yolo marks the project
as trusted inside the jail, so it loads with no prompt.

The plugin is part of the repository: the agent can edit it, it survives restarts, and everyone who
clones the repository gets it. On your own machine, Claude Code loads it once you trust the folder.
Claude Code also watches the folder, so saving a change reloads the mod in a running session.

### Use the plugins you enable on your machine

The `claude` pack starts each jail's Claude Code settings from your own `~/.claude/settings.json`
([Settings Across Agents](agent-settings.md#where-a-setting-comes-from)). When you add a marketplace
and install a plugin on your machine, with `claude plugin marketplace add` and
`claude plugin install`, Claude Code records both in that file, so the next jail you start has the
plugin enabled too.

The plugin's files do not come along. Inside the jail, Claude Code fetches the marketplace and the
plugin in the background after a session starts, and each project keeps its own copy. If the plugin
is missing from the first session in a project, run `/reload-plugins` or start Claude Code again.

Two cases do not work this way:

- **A marketplace in a private repository.** Your host's git credentials are not in the jail, so
  Claude Code there cannot fetch it unless the jail has a login of its own; see
  [Logins](authentication.md#pushing-to-git-from-the-jail).
- **A marketplace you added from a folder on your machine.** It names a path the jail does not have.

### Install it in a jail, then share it

`/plugin install <plugin>@<marketplace>` works in a jail as it does anywhere. The plugin lands in that
project's `~/.claude`. At the default user scope, the setting that enables it lands in the jail's
`~/.claude/settings.json`, which yolo keeps for that project when the jail stops.

To give every jail the same setting, run this on your machine, from the project's folder, after the
jail has stopped:

```bash
yolo config promote claude/settings                      # what would move; writes nothing
yolo config promote claude/settings --accept-promotion   # move it into your local pack
```

`promote` writes the setting into your **local pack**, `~/.config/yolo-jail/local/`, which reaches
every jail and, with `yolo host apply`, your own machine. Each other project then downloads its own
copy of the plugin, as in the section above.

## Develop a mod in a jail

`~/.claude/skills/` is read-only in a jail, so develop the mod in the project instead, and start
Claude Code with its folder:

```bash
yolo -- claude --plugin-dir /workspace/mods/first-mod
```

Claude Code watches a folder loaded this way and reloads the mod each time you save. Run
`claude plugin validate /workspace/mods/first-mod` to list the events the mod handles and what it
asks Claude Code to do.

A mod that Claude Code writes for you goes into that session's own folder,
`~/.claude/dev-mods/<session>/`, and loads only in that session; Claude Code deletes the folder
later. To keep one, wrap it as a pack from your machine, where the jail's `~/.claude` is the
project's `.yolo/home/claude/` folder (`.yolo/home/.claude/` on Apple Container):

```bash
yolo pack init --from-plugin <project>/.yolo/home/claude/dev-mods/<session>/<mod> ~/code/my-mod-pack
```

## What yolo shows you

**Before you select a pack,** `yolo pack init --from-plugin` lists what the plugin carries, and
`yolo pack footprint <folder>` lists every wrapped plugin in a pack and whether it runs code.

**Each time a jail starts,** each pack whose wrapped plugins run code gets one line, and no setting
hides it:

```text
This launch delivers pack code that runs inside the jail (`yolo pack footprint` names each plugin):
  first-mod-pack: 1 wrapped plugin runs code in the jail — hooks (1)
```

The line names each kind of code the pack's plugins carry, and the number beside a kind is how many
of those plugins carry it, not how many hooks or servers there are. The kinds are hooks, a mod's
hooks module among them, tool (MCP) servers, language servers, background monitors, programs in the
plugin's `bin/` folder and a status line for subagents. Each counts whether the plugin's manifest
names it or it sits where Claude Code looks for it by default. A plugin of skills, commands,
subagents or output styles runs no code and gets no line.

**What the launch does not list:** a plugin kept in the project, and a plugin Claude Code installs
itself, from your settings or a `/plugin install`. Check those in Claude Code: `/plugin` shows what
loaded, and in a terminal session a dim line there names the mods, such as `1 mod active · first-mod`.

## Good to know

- **Mods need Claude Code 2.1.287 or later.** A jail installs the latest Claude Code and checks for
  a newer one at most once an hour, unless you froze agent updates; then run `yolo pack update` in
  the jail ([Keep agents and packs up to date](packs-and-skills.md#keep-agents-and-packs-up-to-date)).
  `claude --version` shows which you have.
- **A mod's saved data is kept per project.** Claude Code describes a mod's store as shared by every
  session on the machine. In a jail it lives in that project's `~/.claude/plugins/`, so it differs
  from project to project and from your own machine. So do the plugins Claude Code installs in a
  jail and the mods it writes there.
- **Some profiles turn off automatic plugin updates.** On the `codex` profile, and on profiles that
  send Claude Code to another provider's service, such as `zai` and `openrouter`, yolo turns off
  Claude Code's background traffic, and Claude Code's automatic plugin updates stop with it. A
  wrapped plugin is not affected: it moves with its pack.

## When your organization blocks plugins

An organization can limit which plugins Claude Code loads, through managed settings that Claude Code
receives when you sign in with a Claude for Teams or Enterprise account, or that an administrator
installs on the machine. One rule there stops every plugin yolo delivers: an allowlist of marketplace
sources, `strictKnownMarketplaces`, with no `{"source": "skills-dir"}` entry. Claude Code then skips
every plugin kept in a skills folder, which is how yolo delivers wrapped plugins and the plugin it
writes for your [language servers](mcp-and-lsp.md). A plugin kept in the project stops too, while
plain skills, folders with a `SKILL.md` and no plugin manifest, keep loading. An empty allowlist also
blocks every marketplace, the official one included
([Claude Code's plugin controls](https://code.claude.com/docs/en/plugins/org#keep-skills-directory-plugins-loading)).

yolo does not check for this yet. If yolo's plugins do not load when you sign in with an
organization account, ask your organization's Claude Code administrator to add
`{"source": "skills-dir"}` to the allowlist. For mods, `claude plugin test`, run in a folder that
holds no mod, says whether a setting or your organization turns mods off altogether. It does not
check this allowlist, so it can say mods can load while the allowlist still stops yolo's plugins.

## Planned

- The launch line naming a mod's hooks module separately from other hooks.
- A jail receiving a plugin that sits at the top of its pack, outside `skills/`, as
  `yolo host apply` already does.
- A launch notice when an organization's policy will stop yolo's plugins from loading.

Under discussion, in the
[open questions](https://github.com/mschulkind-oss/yolo-jail/blob/main/docs/research/claude-code-mods-management.md#4-open-questions):

- listing a plugin's own repository under `packs`, with no wrapper;
- letting every jail use the plugins installed on your machine, read-only;
- letting a pack enable a marketplace plugin and leave the install to Claude Code.
