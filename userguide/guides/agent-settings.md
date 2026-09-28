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

Your config itself comes in two files: your **user config**, `~/.config/yolo-jail/config.jsonc`,
for personal defaults, and the project's `yolo-jail.jsonc` for what one project needs.
[Configuration](../reference/configuration.md) says which keys go where.

## What translates to which agent

| Setting | Claude Code | Codex | Copilot | opencode | pi | agy |
|---|---|---|---|---|---|---|
| Skills and house rules from packs | Yes | Yes | Yes | Yes | Yes | Yes |
| MCP servers (`mcp_presets`, `mcp_servers`) | Yes | Yes | Yes | Yes | Yes, through an MCP adapter extension you install in pi | Yes |
| Language servers (`lsp_servers`) | Yes | No: Codex has no LSP support | Yes | Not yet | Not yet | No: agy has no LSP support |
| Model provider and profile (`-p`) | Yes | Yes | Yes | Yes | Yes | No |
| Permission prompts off in a jail | Yes | Yes | Yes | Yes | Yes | Yes |

An **MCP server** gives an agent extra tools through the
[Model Context Protocol](https://modelcontextprotocol.io/introduction); a **language server** gives
it code intelligence such as go-to-definition. [MCP and LSP](mcp-and-lsp.md) covers both.
[Providers and Models](providers-and-models.md) covers profiles.

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
