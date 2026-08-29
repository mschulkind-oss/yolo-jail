-- agy: the MCP surface's dynamic layer. Passthrough mcp_servers, stripping
-- servers that declare provides = "web_search" because agy has native Google search.
yolo.derive("agy", "mcp", function(ctx)
  local servers = {}
  for name, cfg in pairs(ctx.mcp_servers or {}) do
    if type(cfg) == "table" and cfg.provides == "web_search" then
      -- suppress redundant search MCP
    else
      servers[name] = cfg
    end
  end
  return { mcpServers = servers }
end)

