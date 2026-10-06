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

-- OMP'S OWN PROVIDERS (docs/design/pi-codex-provider-shadowing.md OQ-3, ruled 2026-10-05: yolo
-- writes no model entry over any provider an agent has built in, and the agent uses its own
-- list). packs/omp/pack.json's `built_in_providers` names them, and core hands the answer here as
-- ctx.built_in_providers, keyed by yolo provider name: `{ id = <omp's own id> }` where omp reaches
-- the provider through its own client, `false` where omp has the name built in for another plan
-- and none for this one. ompOwn returns that value, or nil for a provider omp does not implement.
-- openai-codex answers as omp's own whatever the table says, the original rule (OQ-1, OQ-2), so
-- an entrypoint older than the table still keeps omp's subscription client unshadowed.
local function ompOwn(ctx, name)
  if type(name) ~= "string" or name == "" then return nil end
  local own = nil
  if type(ctx.built_in_providers) == "table" then
    own = ctx.built_in_providers[name]
  end
  if own == nil and name == "openai-codex" then
    return { id = "openai-codex" }
  end
  if own == false or type(own) == "table" then
    return own
  end
  return nil
end

yolo.derive("oh-omp", "models", function(ctx)
  local providers = {}
  for name, prov in pairs(ctx.providers or {}) do
    -- A PROVIDER OMP HAS BUILT IN IS NEVER CATALOGUED HERE (ompOwn; OQ-3), a via row included:
    -- OMP applies a models.yml row's baseUrl to its built-in provider of the same name, so a row
    -- redirects omp's own client and replaces its model list. openai-codex, its ChatGPT
    -- subscription client, was the first case (OQ-1, OQ-2); zai, cerebras, openrouter and kilo
    -- are omp's own too, and omp reads the same key names for them that yolo's providers deliver.
    local native = (ompOwn(ctx, name) ~= nil)
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

-- settings (~/.oh-omp/agent/config.yml, omp's own settings file, which omp edits too, so the
-- surface is stateful): UNDER AN `only`, the selected provider's narrowed list as omp's
-- `enabledModels` scope (docs/design/model-lists-and-pickers.md §14.1, MM-D8). omp resolves the
-- scope once at startup and its selector then shows only the scope, with no "all" view. A
-- scope naming no model omp knows fails open to its full list, which yolo cannot see at render
-- time.
--
-- THE DEFAULT ENTRY LEADS, and that is the whole start rule: with a scope and no `--model`,
-- omp starts a fresh session on the model its own `modelRoles.default` remembers when that is
-- in the scope, else on the scope's first entry (0.15.3's startup, read in the shipped binary).
-- So a valid saved choice is never steered and an off-list one never starts, which is what
-- OQ-ML2's ruling asks of yolo, with nothing written to `modelRoles`: the selection namespace
-- lifts top-level keys only, and `modelRoles.default` is a key of a record (MM-D12).
--
-- Under the SELECTION, not the computed layer, as pi's enabledModels is: written on
-- activation, a user's edit kept, yolo's own list cleared on deselect (OQ-PSW2). Nothing when
-- no profile is active at omp's CLI name, when the selected provider's list is not narrowed
-- (what a list that only adds does to omp's catalog is OQ-MM1's), or when omp cannot reach it.
-- The scope does not refuse: `--model` still resolves any model omp knows, and `modelRoles`
-- may name one outside it.

-- ompNarrowedRun is one provider's run of the scope under an `only`: its narrowed list, the
-- default entry first, as `<provider>/<id>`; nil when the provider's list is not narrowed or omp
-- cannot reach it, which is "no scope of its own". via is whether this provider's row rides the
-- profile's via route, which only the selected provider's does.
local function ompNarrowedRun(ctx, name, profile, via)
  local prov = ctx.providers and ctx.providers[name] or nil
  if type(prov) ~= "table" or prov.models_only ~= true or type(prov.models) ~= "table" then
    return nil
  end
  -- A provider omp has built in (ompOwn, OQ-3) is reached through omp's own client and named by
  -- omp's own id; one omp has none of its own for (`false`) is reached by nothing. The list an
  -- `only` narrowed is an override a pack or the user declared, which docs/design/pi-codex-
  -- provider-shadowing.md OQ-4 holds open, so it stays omp's scope.
  local own = ompOwn(ctx, name)
  if own == false then return nil end
  local reachable = type(own) == "table" or via or (providerEndpoint(prov) ~= nil)
  if not reachable then return nil end
  local key = type(own) == "table" and own.id or name
  -- The list's order (`order`, then id), the default entry first: the profile's `model`, as an
  -- alias or an id, else the provider's `default` alias, else the first entry.
  local opts = type(prov.model_options) == "table" and prov.model_options or {}
  local rows, byId = {}, {}
  for alias, id in pairs(prov.models) do
    if type(id) == "string" and id ~= "" then
      local r = byId[id]
      if not r then
        r = { id = id }
        byId[id] = r
        table.insert(rows, r)
      end
      local f = opts[alias]
      if r.order == nil and type(f) == "table" then r.order = tonumber(f.order) end
    end
  end
  table.sort(rows, function(a, b)
    if a.order and b.order and a.order ~= b.order then return a.order < b.order end
    if a.order and not b.order then return true end
    if b.order and not a.order then return false end
    return a.id < b.id
  end)
  if #rows == 0 then return nil end
  local default = nil
  local m = type(profile) == "table" and profile.model or nil
  if type(m) == "string" and m ~= "" then
    local id = prov.models[m] or m
    if byId[id] then default = id end
  end
  if not default and type(prov.models.default) == "string" and byId[prov.models.default] then
    default = prov.models.default
  end
  default = default or rows[1].id
  local run = { key .. "/" .. default }
  for _, r in ipairs(rows) do
    if r.id ~= default then table.insert(run, key .. "/" .. r.id) end
  end
  return run
end

-- THE ACTIVE SET (docs/design/active-provider-sets.md §4.4, AP-D18). oh-omp is SET-CAPABLE
-- (packs/omp/pack.json's `provider_sets`): `-p oh-omp=zai,openrouter` makes every listed provider
-- live in one session, and ctx.active_set carries the entries in order, the first being
-- ctx.selected_provider and ctx.profile as always. The catalog above already writes a row for
-- every provider omp reaches, selected or not, and each entry's key reaches omp through the
-- credential gate, so the set is the scope's to state, and only when a scope is written at all.
--
-- A scope is all of omp's picker, so once one is written each entry must be in it: an entry
-- whose list an `only` narrowed contributes that run, its default first, and every other entry
-- contributes `<provider>/*`, every model omp has for it, which is what "no scope of its own"
-- shows it today (omp matches enabledModels as globs over `<provider>/<id>`, read in 0.15.3's
-- resolveModelScope). Runs follow set order, so the primary's leads and omp's start rule above
-- lands on the primary when nothing saved is in the scope. With no entry narrowed nothing is
-- written, exactly as for one profile (AP-P1), and a set of one takes the single path below
-- unchanged. Only the primary rides the via route (AP-D9).
yolo.derive("oh-omp", "settings", function(ctx)
  local name = ctx.selected_provider
  if name == nil or name == "" then return {} end
  local via = (ctx.via_url ~= nil and ctx.via_url ~= "")
  local set = ctx.active_set
  if type(set) ~= "table" or #set < 2 then
    local run = ompNarrowedRun(ctx, name, ctx.profile, via)
    if run == nil then return {} end
    return { selection = { enabledModels = run } }
  end
  local scope, seen, narrowed = {}, {}, false
  local function add(v)
    if not seen[v] then
      seen[v] = true
      table.insert(scope, v)
    end
  end
  for i, e in ipairs(set) do
    if type(e) == "table" and type(e.provider) == "string" and e.provider ~= "" then
      local run = ompNarrowedRun(ctx, e.provider, e.profile, i == 1 and via)
      if run then
        narrowed = true
        for _, v in ipairs(run) do add(v) end
      else
        -- omp's own id for a provider it has built in (ompOwn, OQ-3), the name otherwise.
        local own = ompOwn(ctx, e.provider)
        add(((type(own) == "table") and own.id or e.provider) .. "/*")
      end
    end
  end
  if not narrowed then return {} end
  return { selection = { enabledModels = scope } }
end)
