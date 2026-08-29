-- opencode: project the canonical MCP and provider entries into opencode's dialect.
yolo.derive("opencode", "config", function(ctx)
  local res = {}

  -- 1. MCP servers
  if next(ctx.mcp_servers) ~= nil then
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
    res.mcp = out
  end

  -- 2. Providers
  if ctx.providers and next(ctx.providers) ~= nil then
    local provOut = {}
    for name, prov in pairs(ctx.providers) do
      if type(prov) == "table" and prov.base_url then
        local models = {}
        if type(prov.models) == "table" then
          for alias, modelId in pairs(prov.models) do
            models[modelId] = { name = alias }
          end
        end
        local entry = {
          npm = "@ai-sdk/openai-compatible",
          baseURL = prov.base_url,
          models = models,
        }
        if prov.api_key_env then
          entry.apiKey = "{env:" .. prov.api_key_env .. "}"
        end
        provOut[name] = entry
      end
    end
    if next(provOut) ~= nil then
      res.provider = provOut
    end
  end

  return res
end)
