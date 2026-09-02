-- config (~/.claude.json, RMW): the mcpServers managed table.
-- In subscription mode (default), Claude has native web search, so servers providing
-- "web_search" are omitted. In Bedrock mode, search MCPs pass through.
yolo.derive("claude", "config", function(ctx)
  local claudeProfile = (ctx.use_profiles and ctx.use_profiles.claude) or "default"
  local isBedrock = (claudeProfile == "bedrock")
  local servers = {}
  for name, cfg in pairs(ctx.mcp_servers or {}) do
    if type(cfg) == "table" and cfg.provides == "web_search" and not isBedrock then
      -- native search is available in 1st-party subscription mode; suppress MCP
    else
      servers[name] = cfg
    end
  end
  return { mcpServers = servers }
end)

-- settings (~/.claude/settings.json): two derivations.
--  1. tombstone mcpServers — MCP belongs in .claude.json, so strip any host
--     settings.json copy. (Was computed[] {to: mcpServers, tombstone: true}.)
--  2. enabledPlugins — enable the LSP plugin for each language whose LSP is
--     configured; tombstone the others so a stale enable is removed. (Was
--     computed[] flags whenPresent x3.)
--  3. env.ENABLE_LSP_TOOL — "1" when ANY LSP is configured, else tombstone.
--     (Was computed[] flags whenAny.)
yolo.derive("claude", "settings", function(ctx)
  local plugin = {
    python     = "pyright-lsp@claude-plugins-official",
    typescript = "typescript-lsp@claude-plugins-official",
    go         = "gopls-lsp@claude-plugins-official",
  }
  local enabled = {}
  for lang, id in pairs(plugin) do
    enabled[id] = ctx.lsp_servers[lang] and true or ctx.tombstone
  end
  return {
    mcpServers = ctx.tombstone,
    enabledPlugins = enabled,
    env = { ENABLE_LSP_TOOL = next(ctx.lsp_servers) and "1" or ctx.tombstone },
  }
end)
