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

-- THE DISPLAY NAME OF A CATALOG ROW (docs/design/model-lists-and-pickers.md MM-D7): the
-- entry's own `name` fact, or nil so the agent shows its own catalog's name for the id, or the
-- id. NEVER the yolo alias the id sits under: `default` and `fast` are yolo's pointers, and a
-- row named after one showed a model as "default". The fact is read from the alias spelled as
-- the id first, then from any other alias naming the id, in sorted alias order, the precedence
-- codexModelList's rows follow (ML-D6).
--
-- ⚠ DUPLICATED VERBATIM in packs/opencode/derive.lua, packs/pi/derive.lua and
-- packs/omp/derive.lua, because a derive cannot load another file.
-- internal/entrypoint/modeldisplayname_test.go fails when the copies differ.
local function modelDisplayName(prov, id)
  if type(prov) ~= "table" or type(prov.models) ~= "table" then return nil end
  local opts = type(prov.model_options) == "table" and prov.model_options or {}
  local function named(alias)
    local f = opts[alias]
    if type(f) == "table" and type(f.name) == "string" and f.name ~= "" then return f.name end
    return nil
  end
  if prov.models[id] == id and named(id) then return named(id) end
  local aliases = {}
  for alias, target in pairs(prov.models) do
    if type(alias) == "string" and target == id then table.insert(aliases, alias) end
  end
  table.sort(aliases)
  for _, alias in ipairs(aliases) do
    if named(alias) then return named(alias) end
  end
  return nil
end

yolo.derive("oh-omp", "models", function(ctx)
  local providers = {}
  for name, prov in pairs(ctx.providers or {}) do
    -- openai-codex is one of OMP's BUILT-IN providers (its ChatGPT subscription client), so it
    -- is never catalogued here (docs/design/pi-codex-provider-shadowing.md OQ-1, OQ-2): OMP
    -- applies a models.yml row's baseUrl to the built-in provider of the same name, so a row
    -- redirects the subscription client. Excluded by name, as packs/codex and packs/pi do —
    -- including a via row, since a via profile routes a provider yolo catalogues.
    local native = (name == "openai-codex")
    local baseUrl, api = nil, nil
    if not native then
      baseUrl, api = providerEndpoint(prov)
    end
    -- VIA (docs/design/wire-bridge-gateway.md OQ-WG6/WG7): the selected provider's row points
    -- at this agent's route on the service its profile names, speaking chat-completions, the
    -- protocol the via route passes through to the provider's own `openai` endpoint. The
    -- service holds the upstream credential, and demands this launch's caller token of every
    -- caller (docs/reference/wire-bridge.md WB-D18), so the row's key is that token's variable
    -- (ctx.via_api_key_env_name), never the provider's. An entrypoint older than the field
    -- hands nil, and the row keeps the provider's name as it did before.
    local viaKey = nil
    if not native and ctx.via_url ~= nil and ctx.via_url ~= "" and name == ctx.selected_provider then
      baseUrl, api = ctx.via_url, "openai-completions"
      if ctx.via_api_key_env_name ~= nil and ctx.via_api_key_env_name ~= "" then
        viaKey = ctx.via_api_key_env_name
      end
    end
    if baseUrl and api then
      local entry = { baseUrl = baseUrl, api = api, authHeader = true }
      if viaKey then
        entry.apiKey = viaKey
      elseif prov.api_key_env_name then
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
      -- Each row's name is the entry's own, or none, so omp shows the id (MM-D7). Never the
      -- alias: cerebras's and llamacpp's one model showed as "default". One row per id, since
      -- a second alias for an id is a pointer, not a second model.
      local aliases, seen = {}, {}
      for alias in pairs(prov.models or {}) do table.insert(aliases, alias) end
      table.sort(aliases)
      for _, alias in ipairs(aliases) do
        local id = prov.models[alias]
        if type(id) == "string" and id ~= "" and not seen[id] then
          seen[id] = true
          table.insert(models, { id = id, name = modelDisplayName(prov, id) })
        end
      end
      if #models > 0 then entry.models = models end
      providers[name] = entry
    end
  end
  if next(providers) == nil then return {} end
  return { providers = in_full(ctx, providers) }
end)
