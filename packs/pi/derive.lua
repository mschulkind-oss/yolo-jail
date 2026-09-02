-- pi: render ~/.pi/agent/models.json from declared providers.

-- The DIALECT MAP (provider-table-fidelity.md §3.4 / OQ-PT1): yolo's canonical wire_api
-- → the value pi reads from providers.<id>.api. Every row is a measured fact about pi,
-- carried here because a dialect map with no provenance is the same unverified assertion
-- in a new location. PROVENANCE: pi's api vocabulary is a RUNTIME registry, not the
-- schema — `BUILTIN_APIS` (pi-ai/dist/compat.js:108-119) lists ten ids, of which the ones
-- below are the three yolo's canonical names translate to; verified from pi 0.84.4
-- (npm-extracted, the CLI itself never run), 2026-09-02. The schema's `api` is a free
-- string (pi-coding-agent/dist/core/model-config.js:173), so a value outside this map
-- would LOAD cleanly and die at first request with "No API provider registered for api".
local piDialect = {
  ["anthropic"] = "anthropic-messages",
  ["openai-chat-completions"] = "openai-completions",
  ["openai-responses"] = "openai-responses",
}

-- piAPI maps one canonical protocol to pi's `api` value, or nil when pi has no spelling
-- for it (the caller then emits no entry — a half-configured provider would fail at first
-- request from a jail that booted green). One deliberate default sits in front of the map:
-- NOTHING declared → "openai-completions". That is THIS DERIVE'S choice, not pi's — pi has
-- NO default, and an absent api is a composition error that deletes the provider from the
-- model list (pi-coding-agent/dist/core/provider-composer.js:48-52), so leaving the field
-- out is not an option the way it is for an agent with a default of its own. The openai
-- route is the one the packs ship for openai-speaking agents (zai-plumbing.md §5), so it
-- is what an undeclared endpoint means here.
local function piAPI(canonical)
  if canonical == nil then
    return "openai-completions"
  end
  return piDialect[canonical]
end

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
      -- An endpoint's own wire_api is the per-protocol fact; the provider-level one
      -- only speaks for the shorthand. A canonical value with no row in piDialect drops
      -- the whole entry: pi would list the provider and fail on every request.
      local api = piAPI(wireApi or prov.wire_api)
      if api then
        local entry = {
          baseUrl = baseUrl,
          api = api,
          models = modelList,
        }
        -- D11: pi has no `apiKeyEnv` field — ProviderConfigSchema is name, baseUrl, apiKey,
        -- api, oauth, headers, compat, authHeader, models, modelOverrides, and nothing in the
        -- package reads one, so the name we used to write here was dead configuration that
        -- read as the thing delivering the credential. pi's env indirection is the config-value
        -- syntax ON apiKey (`${VAR}`; docs/custom-provider.md — the maintainer's own hand-written
        -- models.json uses it), and pi expands it at read time, so yolo writes the reference
        -- verbatim and the consumer resolves it. Written only when the provider names a var, so
        -- a key-less provider stays key-less rather than claiming an empty one.
        if prov.api_key_env_name then
          entry.apiKey = "${" .. prov.api_key_env_name .. "}"
        end
        providers[name] = entry
      end
    end
  end
  if next(providers) == nil then
    return {}
  end
  return { providers = providers }
end)
