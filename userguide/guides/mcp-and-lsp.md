# MCP and LSP

## MCP Presets

MCP (Model Context Protocol) servers extend agent capabilities. YOLO Jail includes built-in presets that can be enabled by name — **none are enabled by default**.

### Available Presets

| Preset | Description |
|--------|-------------|
| `chrome-devtools` | Headless Chromium automation via Chrome DevTools Protocol |
| `sequential-thinking` | Chain-of-thought reasoning MCP server |

### Enable Presets

```jsonc
{
  "mcp_presets": ["chrome-devtools", "sequential-thinking"]
}
```

### Custom MCP Servers

Add your own MCP servers alongside or instead of presets:

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

YOLO Jail can hand LSP (Language Server Protocol) servers to the agents that read them. There are **no defaults**, and YOLO never installs a language server: you declare the server, and its binary has to be on `PATH` already.

### Adding Servers

Add language servers via `lsp_servers` in your config. The binary must already be on `PATH` (install it with `mise_tools` or `packages`):

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

### Which agents read it

| Agent | How it receives LSP |
|-------|---------------------|
| **Copilot** | natively, via `~/.copilot/lsp-config.json` — any server you declare |
| **Claude Code** | via one plugin YOLO generates from your whole `lsp_servers` table — any server you declare |
| **Codex, agy** | the agent has no LSP support |
| **Pi, opencode** | not configured by YOLO today (both agents can take it — see [`mcp-configuration.md`](https://github.com/mschulkind-oss/yolo-jail/blob/main/docs/reference/mcp-configuration.md#lsp-claudes-route-is-a-generated-plugin)) |

Servers are spawned on-demand when an agent analyzes matching file types.

---
