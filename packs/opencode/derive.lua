-- opencode: project the canonical MCP and provider entries into opencode's dialect.

-- The provider's URL for the protocol opencode speaks — `openai`, per the resolution
-- table in zai-plumbing.md §5. The single-protocol `base_url` shorthand wins; otherwise
-- the openai endpoint. Total over non-tables so the call site stays a one-line gate.
-- Returns nil when the provider names no URL an openai-speaking agent can use, which is
-- what keeps the gate below honest: an endpoints-only provider still reaches the catalog
-- (the pre-endpoints gate on prov.base_url silently dropped it), while a provider whose
-- only endpoint speaks anthropic would emit an entry with no URL. opencode consumes no
-- wire_api, so only the URL comes back.
local function providerEndpoint(prov)
  if type(prov) ~= "table" then return nil end
  if prov.base_url then
    return prov.base_url
  end
  local ep = prov.endpoints and prov.endpoints.openai or nil
  if type(ep) == "table" and ep.base_url then
    return ep.base_url
  end
  return nil
end

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
      local baseUrl = providerEndpoint(prov)
      if baseUrl then
        local models = {}
        if type(prov.models) == "table" then
          for alias, modelId in pairs(prov.models) do
            models[modelId] = { name = alias }
          end
        end
        local entry = {
          npm = "@ai-sdk/openai-compatible",
          baseURL = baseUrl,
          models = models,
        }
        if prov.api_key_env_name then
          entry.apiKey = "{env:" .. prov.api_key_env_name .. "}"
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
