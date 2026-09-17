-- agy: the MCP surface's dynamic layer. A passthrough of the servers this launch is
-- eligible for.
--
-- The `provides = "web_search"` strip that used to be here is GONE: agy's native Google
-- search is a fact about its BUILT-IN authentication source, so the pack states it once
-- (the program contribution's `capabilities`) and core drops the redundant servers before
-- ctx.mcp_servers reaches any derive. The surface still needs this producer — mode is
-- `computed` over `defaults.mcpServers = {}`, so without a dynamic layer agy would render
-- an empty table rather than the configured one.
yolo.derive("agy", "mcp", function(ctx)
  local servers = {}
  for name, cfg in pairs(ctx.mcp_servers or {}) do
    servers[name] = cfg
  end
  return { mcpServers = servers }
end)

