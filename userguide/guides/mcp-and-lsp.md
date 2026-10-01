# MCP and LSP

Two kinds of server extend what an agent can do, and yolo writes both into the settings of every
selected agent that supports them:

- An **MCP server** gives an agent extra tools, such as driving a browser, through the
  [Model Context Protocol](https://modelcontextprotocol.io/introduction).
- A **language server** gives an agent code intelligence, such as go-to-definition and diagnostics,
  through the [Language Server Protocol](https://microsoft.github.io/language-server-protocol/) (LSP).

Both are configured explicitly; none is on by default. They go in either config file, usually the
project's `yolo-jail.jsonc`, and a change reaches the agents at the jail's next fresh start.

## Which agents receive them

| Agent | MCP servers | Language servers |
|---|---|---|
| Claude Code | Yes | Yes, through a plugin yolo generates |
| Copilot | Yes | Yes |
| Codex | Yes | No: Codex has no language-server support |
| opencode | Yes | Not yet |
| pi | Yes. With the `pi-subagents` extension installed, pi's subagents get them too | Not yet |
| agy | Yes | No: agy has no language-server support |

pi has started MCP servers itself since version 0.99.0, so it needs no MCP extension. yolo writes
your servers into pi's own MCP file, `~/.pi/agent/mcp.json`, beside any you add with
`pi mcp add`, and what you change with pi's `/mcp` command stays. If you installed the
`pi-mcp-adapter` extension for yolo's servers before, remove it: with both, pi starts every server
twice. A subagent that lists `mcp:` tools needs `pi-subagents` 0.74.0 or later to run them without
the adapter, so update `pi-subagents` first.

## MCP Presets

yolo ships two MCP servers you can turn on by name:

### Available Presets

| Preset | Description |
|--------|-------------|
| `chrome-devtools` | Lets the agent drive a headless Chromium browser, which is built into the jail |
| `sequential-thinking` | Gives the agent a step-by-step reasoning tool |

### Enable Presets

```jsonc
{
  "mcp_presets": ["chrome-devtools", "sequential-thinking"]
}
```

yolo installs a preset's server when the jail starts, and keeps it current
along with the agent. On `macos-user`, presets are not delivered yet.

### Custom MCP Servers

Add your own MCP servers alongside or instead of presets. The `command` must exist inside the jail:

```jsonc
{
  "mcp_servers": {
    "my-server": {
      "command": "/workspace/scripts/my-mcp.py",
      "args": ["--port", "3333"]
    }
  }
}
```

### Disable a Preset

Set a preset server to `null` in `mcp_servers` to disable it even when listed in `mcp_presets`:

```jsonc
{
  "mcp_presets": ["chrome-devtools", "sequential-thinking"],
  "mcp_servers": {
    "sequential-thinking": null
  }
}
```

---

## LSP Servers

yolo hands the language servers you declare to the agents that read them. It never installs a
language server itself: its program must already be on the jail's `PATH`, for example from
`mise_tools` or `packages`.

### Adding Servers

```jsonc
{
  "lsp_servers": {
    "rust": {
      "command": "rust-analyzer",
      "args": [],
      "fileExtensions": {".rs": "rust"}
    }
  }
}
```

An agent starts a server when it opens a file with a matching extension.

---
