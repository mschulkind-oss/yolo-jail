-- pi: render ~/.pi/agent/models.json from declared providers.

-- The provider's URL for the protocol THIS agent speaks — `openai`, per the resolution
-- table in zai-plumbing.md §5 (claude is the one `anthropic` consumer, and it has no
-- derive). The single-protocol `base_url` shorthand wins; otherwise the openai endpoint.
-- Total over non-tables so the call site stays a one-line gate. Returns nil when the
-- provider names no URL an openai-speaking agent can use, which is what keeps that gate
-- honest: an endpoints-only provider still reaches the catalog (the pre-endpoints gate
-- on prov.base_url silently dropped it), while a provider whose only endpoint speaks a
-- protocol pi cannot would emit an entry with no URL — a provider it cannot reach.
local function providerEndpoint(prov)
  if type(prov) ~= "table" then return nil end
  if prov.base_url then
    return prov.base_url, prov.wire_api
  end
  local ep = prov.endpoints and prov.endpoints.openai or nil
  if type(ep) == "table" and ep.base_url then
    return ep.base_url, ep.wire_api
  end
  return nil
end

yolo.derive("pi", "models", function(ctx)
  if not ctx.providers or next(ctx.providers) == nil then
    return {}
  end
  local providers = {}
  for name, prov in pairs(ctx.providers) do
    local baseUrl, wireApi = providerEndpoint(prov)
    if baseUrl then
      local modelList = {}
      if type(prov.models) == "table" then
        for alias, modelId in pairs(prov.models) do
          table.insert(modelList, { id = modelId, name = alias })
        end
      end
      providers[name] = {
        baseUrl = baseUrl,
        -- An endpoint's own wire_api is the per-protocol fact; the provider-level one
        -- only speaks for the shorthand.
        api = wireApi or prov.wire_api or "openai-completions",
        apiKeyEnv = prov.api_key_env_name,
        models = modelList,
      }
    end
  end
  if next(providers) == nil then
    return {}
  end
  return { providers = providers }
end)
