-- codex: project MCP servers and model_providers into codex's TOML format.
yolo.derive("codex", "config", function(ctx)
  local res = {}

  -- 1. MCP servers
  if next(ctx.mcp_servers) ~= nil then
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
    res.mcp_servers = out
  end

  -- 2. Model providers
  if ctx.providers and next(ctx.providers) ~= nil then
    local provOut = {}
    for name, prov in pairs(ctx.providers) do
      if type(prov) == "table" and prov.base_url then
        local entry = {
          base_url = prov.base_url,
          wire_api = prov.wire_api or "responses",
        }
        if prov.api_key_env_name then
          entry.api_key_env = prov.api_key_env_name
        end
        provOut[name] = entry
      end
    end
    if next(provOut) ~= nil then
      res.model_providers = provOut
    end
  end

  return res
end)
