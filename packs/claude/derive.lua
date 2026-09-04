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
-- vocabulary every provider would restate (docs/reference/providers.md §3.1,
-- OQ-CS8). The producer reads the composed table; a selected provider's api_key arrives
-- hydrated for this invocation only, and never crosses in the wire table itself (D8).
-- An input that is absent drops its variable: an empty base URL is a request to the
-- wrong host, and an empty token is a credential that gets SENT.
yolo.env("claude", function(ctx)
  local p = ctx.providers[ctx.selected_provider]
  if not p then return {} end
  local out = {}
  local routed = false -- claude is pointed at a non-first-party Anthropic-wire host
  if p.endpoints and p.endpoints.anthropic and p.endpoints.anthropic.base_url then
    out.ANTHROPIC_BASE_URL = p.endpoints.anthropic.base_url
    routed = true
  end
  if p.api_key then
    out.ANTHROPIC_AUTH_TOKEN = p.api_key
  end
  if p.region then
    out.AWS_REGION = p.region
  end
  -- Provider FACTS reach the derive as profile options (OQ-CS4: the provider declares
  -- the knobs, this derive decides what each one means for claude), so the values stay
  -- the provider's while the variable names stay claude's:
  --   context_window (tokens) -> the auto-compact threshold, so a 1M-context model
  --     does not compact at claude's default window (verified against claude 2.1.259:
  --     CLAUDE_CODE_AUTO_COMPACT_WINDOW is read and "takes precedence");
  --   api_timeout_ms -> claude's per-request ceiling, for providers whose reasoning
  --     turns run long.
  if ctx.profile then
    if ctx.profile.context_window then
      out.CLAUDE_CODE_AUTO_COMPACT_WINDOW = ctx.profile.context_window
    end
    if ctx.profile.api_timeout_ms then
      out.API_TIMEOUT_MS = ctx.profile.api_timeout_ms
    end
  end
  -- The model ids the provider declares are WIRE-TRUE — every agent's catalog sends
  -- them verbatim, and z.ai's routes reject claude-only spellings (measured
  -- 2026-09-04: "glm-5.3[1m]" is a 400 on both routes; pi and opencode have no
  -- [1m] handling to strip one). "[1m]" is CLAUDE CODE's syntax: it strips the
  -- suffix client-side and sends the context-1m beta in its place, which is how a
  -- 1M-context model gets its full window. So this derive alone re-spells the ids
  -- claude uses, from the provider's own context_window fact: a provider declaring
  -- a 1,000,000-token window gets the beta requested; anything smaller gets the
  -- bare id.
  local cw = tonumber((ctx.profile and ctx.profile.context_window) or "")
  local suffix = (cw and cw >= 1000000) and "[1m]" or ""
  if routed then
    -- Z.AI's recommended Claude Code config disables claude's nonessential traffic
    -- (telemetry, update checks) on a routed launch: that traffic targets
    -- api.anthropic.com, which either fails or leaks through a third-party gateway.
    -- First-party and Bedrock launches keep it (Bedrock's gated env owns that mode).
    out.CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC = "1"
  end
  local m = p.models or {}
  -- OPUS takes the alias the active profile's `model` option names (OQ-CS4: what an
  -- option means is the derive's business), falling back to the provider's declared
  -- `default` when the profile carries none (OQ-CS3). SONNET and HAIKU keep their own
  -- aliases: they are Claude's routing names inside the same provider, not a selection
  -- surface, and no profile option speaks for them. Declaring them matters even though
  -- z.ai translates claude's own tier names server-side (measured 2026-09-04:
  -- claude-sonnet-* serves as glm-5.3-flash — the FAST model), because the aliases pin
  -- each tier to the model the provider actually intends for it.
  local alias = (ctx.profile and ctx.profile.model) or "default"
  if m[alias] then
    out.ANTHROPIC_DEFAULT_OPUS_MODEL = m[alias] .. suffix
  end
  if m.sonnet then
    out.ANTHROPIC_DEFAULT_SONNET_MODEL = m.sonnet .. suffix
  end
  if m.haiku then
    out.ANTHROPIC_DEFAULT_HAIKU_MODEL = m.haiku .. suffix
  end
  return out
end)
