-- codex: near-passthrough of the canonical MCP entry {command, args, env} into
-- codex's mcp_servers block. Was computed[] {from: mcp_servers, to: mcp_servers,
-- omitEmpty: true, project: copy command / copy args / default args=[] / copy env
-- omitEmpty}.
--
-- omitEmpty on the whole decl: when there are no MCP servers, the mcp_servers key
-- is dropped ENTIRELY (not emitted as {}), so an empty config leaves codex's TOML
-- without an mcp_servers table.
yolo.derive("codex", "config", function(ctx)
  if next(ctx.mcp_servers) == nil then
    return {} -- omitEmpty: no servers → no mcp_servers key at all
  end
  local out = {}
  for name, s in pairs(ctx.mcp_servers) do
    local e = {}
    e.command = s.command
    -- copy args, default to [] when absent (empty_array → JSON/TOML []).
    e.args = s.args or ctx.empty_array
    -- copy env, omitEmpty: only when non-empty.
    if s.env ~= nil and next(s.env) ~= nil then e.env = s.env end
    out[name] = e
  end
  return { mcp_servers = out }
end)
