-- opencode: project the canonical MCP entry into opencode's dialect. Was
-- computed[] {from: mcp_servers, to: mcp, omitEmpty: true, project: fold
-- command+args→command / copy env→environment omitEmpty / inject type=local /
-- inject enabled=true}.
yolo.derive("opencode", "config", function(ctx)
  if next(ctx.mcp_servers) == nil then
    return {} -- omitEmpty: no servers → no mcp key at all
  end
  local out = {}
  for name, s in pairs(ctx.mcp_servers) do
    -- fold command + args into one array (command first, then each arg).
    local cmd = {}
    if s.command ~= nil then cmd[#cmd + 1] = s.command end
    for _, a in ipairs(s.args or {}) do cmd[#cmd + 1] = a end
    local e = { type = "local", enabled = true, command = cmd }
    -- rename env → environment, omitEmpty: only when non-empty.
    if s.env ~= nil and next(s.env) ~= nil then e.environment = s.env end
    out[name] = e
  end
  return { mcp = out }
end)
