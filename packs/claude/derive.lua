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

-- env: the provider environment claude's own process launches with. The variable NAMES
-- are Claude Code's facts, not any provider's, so the binding lives HERE — in the agent
-- pack, where a rename of what claude reads is one edit — rather than in a manifest
-- vocabulary every provider would restate (provider-catalog-and-selection.md §3.1,
-- OQ-CS8). The producer reads the composed table; a selected provider's api_key arrives
-- hydrated for this invocation only, and never crosses in the wire table itself (D8).
-- An input that is absent drops its variable: an empty base URL is a request to the
-- wrong host, and an empty token is a credential that gets SENT.
yolo.env("claude", function(ctx)
  local p = ctx.providers[ctx.selected_provider]
  if not p then return {} end
  local out = {}
  if p.endpoints and p.endpoints.anthropic and p.endpoints.anthropic.base_url then
    out.ANTHROPIC_BASE_URL = p.endpoints.anthropic.base_url
  end
  if p.api_key then
    out.ANTHROPIC_AUTH_TOKEN = p.api_key
  end
  if p.region then
    out.AWS_REGION = p.region
  end
  local m = p.models or {}
  if m.default then
    out.ANTHROPIC_DEFAULT_OPUS_MODEL = m.default
  end
  if m.sonnet then
    out.ANTHROPIC_DEFAULT_SONNET_MODEL = m.sonnet
  end
  if m.haiku then
    out.ANTHROPIC_DEFAULT_HAIKU_MODEL = m.haiku
  end
  return out
end)
