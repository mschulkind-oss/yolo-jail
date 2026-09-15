-- OMP's ModelRegistry reads ~/.oh-omp/agent/models.yml. This derives its
-- documented providers table from yolo's canonical provider facts; no provider
-- credential is copied into the file: apiKey is the provider's environment name.

local ompDialect = {
  ["anthropic"] = "anthropic-messages",
  ["openai-chat-completions"] = "openai-completions",
  ["openai-responses"] = "openai-responses",
}

-- A yolo provider's single-protocol shorthand is authoritative. For the
-- multi-endpoint form, OMP can speak every yolo dialect it maps above, so choose
-- the first endpoint in OMP's stable preference order rather than fabricating a
-- URL for an endpoint it cannot identify.
local function providerEndpoint(prov)
  if type(prov) ~= "table" then return nil end
  if prov.base_url then
    return prov.base_url, ompDialect[prov.wire_api]
  end
  local endpoints = prov.endpoints
  if type(endpoints) ~= "table" then return nil end
  for _, name in ipairs({ "openai", "anthropic" }) do
    local ep = endpoints[name]
    if type(ep) == "table" and ep.base_url then
      local api = ompDialect[ep.wire_api]
      if api then return ep.base_url, api end
    end
  end
  return nil
end

yolo.derive("oh-omp", "models", function(ctx)
  local providers = {}
  for name, prov in pairs(ctx.providers or {}) do
    local baseUrl, api = providerEndpoint(prov)
    if baseUrl and api then
      local entry = { baseUrl = baseUrl, api = api, authHeader = true }
      if prov.api_key_env_name then
        -- OMP resolves apiKey as an environment name before treating it as a
        -- literal. The environment value is delivered by yolo's normal provider
        -- path, so this generated file remains secret-free.
        entry.apiKey = prov.api_key_env_name
      else
        -- The documented schema accepts a keyless provider only when it says
        -- so explicitly; omitting apiKey without this flag is invalid.
        entry.auth = "none"
      end
      local models = {}
      for alias, id in pairs(prov.models or {}) do
        table.insert(models, { id = id, name = alias })
      end
      if #models > 0 then entry.models = models end
      providers[name] = entry
    end
  end
  if next(providers) == nil then return {} end
  return { providers = providers }
end)
