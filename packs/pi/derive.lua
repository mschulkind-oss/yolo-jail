-- pi: render ~/.pi/agent/models.json from declared providers.
yolo.derive("pi", "models", function(ctx)
  if not ctx.providers or next(ctx.providers) == nil then
    return {}
  end
  local providers = {}
  for name, prov in pairs(ctx.providers) do
    if type(prov) == "table" and prov.base_url then
      local modelList = {}
      if type(prov.models) == "table" then
        for alias, modelId in pairs(prov.models) do
          table.insert(modelList, { id = modelId, name = alias })
        end
      end
      providers[name] = {
        baseUrl = prov.base_url,
        api = prov.wire_api or "openai-completions",
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
