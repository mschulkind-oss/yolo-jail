-- OMP's ModelRegistry reads ~/.oh-omp/agent/models.yml. This derives its
-- documented providers table from yolo's canonical provider facts; no provider
-- credential is copied into the file: apiKey is the provider's environment name.

local ompDialect = {
  ["anthropic"] = "anthropic-messages",
  ["openai-chat-completions"] = "openai-completions",
  ["openai-responses"] = "openai-responses",
}

-- OMP can speak every yolo dialect it maps above, so choose the first endpoint in OMP's
-- stable preference order — which is also the order packs/omp declares in its `protocols`
-- list — rather than fabricating a URL for an endpoint it cannot identify. The
-- single-protocol `base_url` shorthand is deleted (protocol-resolution.md): it named no
-- protocol, so it could not say which dialect to map, and the same field meant different
-- wires to different agents.
local function providerEndpoint(prov)
  if type(prov) ~= "table" then return nil end
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

-- in_full declares a table this derive regenerates in full — ctx.in_full, the CO13 sentinel
-- (docs/design/config-ownership-and-promotion.md): its entries track a live table, so one on
-- disk this run did not produce is yolo's own stale output. A table returned without it is
-- one the derive only asserts leaves of. The fallback is for an entrypoint older than the
-- sentinel, which treated every non-empty table as regenerated in full anyway.
-- It covers that direction ONLY: a NEWER entrypoint handed an OLDER copy of this file (packs
-- are staged from the host yolo's embed) sees no declaration and adopts every table leaf by
-- leaf, and only the launch's source-skew check (version.SourceSkew) refuses that pairing.
local function in_full(ctx, t)
  if ctx.in_full then return ctx.in_full(t) end
  return t
end

yolo.derive("oh-omp", "models", function(ctx)
  local providers = {}
  for name, prov in pairs(ctx.providers or {}) do
    local baseUrl, api = providerEndpoint(prov)
    -- VIA (docs/design/wire-bridge-gateway.md OQ-WG6/WG7): the selected provider's row points
    -- at this agent's route on the service its profile names, speaking chat-completions, the
    -- protocol the via route passes through to the provider's own `openai` endpoint. The
    -- service holds the upstream credential and ignores inbound auth (WB-D4).
    if ctx.via_url ~= nil and ctx.via_url ~= "" and name == ctx.selected_provider then
      baseUrl, api = ctx.via_url, "openai-completions"
    end
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
  return { providers = in_full(ctx, providers) }
end)
