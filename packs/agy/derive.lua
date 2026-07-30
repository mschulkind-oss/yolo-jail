-- agy: the MCP surface's dynamic layer. Passthrough — the canonical
-- mcp_servers table lands verbatim under mcpServers. (Was: computed[]
-- {from: mcp_servers, to: mcpServers}.)
yolo.derive("agy", "mcp", function(ctx)
  return { mcpServers = ctx.mcp_servers }
end)
