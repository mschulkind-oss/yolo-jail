-- codex: project MCP servers and model_providers into codex's TOML format.

-- The provider's URL for the protocol codex speaks — `openai`, per the resolution table
-- in zai-plumbing.md §5. The single-protocol `base_url` shorthand wins; otherwise the
-- openai endpoint. Total over non-tables so the call site stays a one-line gate. Returns
-- nil when the provider names no URL an openai-speaking agent can use, which is what
-- keeps that gate honest: an endpoints-only provider still reaches the catalog (the
-- pre-endpoints gate on prov.base_url silently dropped it), while a provider whose only
-- endpoint speaks anthropic would emit an entry with no URL.
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
      local baseUrl, wireApi = providerEndpoint(prov)
      if baseUrl then
        local entry = {
          base_url = baseUrl,
          -- An endpoint's own wire_api is the per-protocol fact; the provider-level one
          -- only speaks for the shorthand.
          wire_api = wireApi or prov.wire_api or "responses",
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
