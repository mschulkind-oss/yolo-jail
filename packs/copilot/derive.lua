-- copilot has two dynamic surfaces.

-- mcp: passthrough — canonical mcp_servers lands verbatim under mcpServers.
yolo.derive("copilot", "mcp", function(ctx)
  return { mcpServers = ctx.mcp_servers }
end)

-- lsp: project each lsp_servers entry into copilot's dialect. Was computed[]
-- project ops: copy command (omitEmpty), copy args, default args=[], copy
-- fileExtensions, default fileExtensions={}.
yolo.derive("copilot", "lsp", function(ctx)
  local out = {}
  for name, s in pairs(ctx.lsp_servers) do
    local e = {}
    -- copy command, omitEmpty: skip an empty/absent command.
    if s.command ~= nil and s.command ~= "" then e.command = s.command end
    -- copy args, then default to [] when absent — ctx.empty_array so an absent
    -- args renders as JSON [], not {} (Lua can't tell {} array from {} object).
    e.args = s.args or ctx.empty_array
    -- copy fileExtensions, then default to {} (object) when absent.
    e.fileExtensions = s.fileExtensions or {}
    out[name] = e
  end
  return { lspServers = out }
end)
