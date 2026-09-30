-- opencode: project the canonical MCP and provider entries into opencode's dialect.

-- The provider's URL for the protocol opencode speaks — `openai`, which is also what
-- packs/opencode declares in its `protocols` list. ONE spelling: the single-protocol
-- `base_url` shorthand is deleted (protocol-resolution.md), because the same bare field
-- meant `openai` here and `anthropic` in claude's derive. Total over non-tables so the call
-- site stays a one-line gate. Returns nil when the provider names no URL an openai-speaking
-- agent can use, which is what keeps the gate below honest: a provider whose only endpoint
-- speaks anthropic would otherwise emit an entry with no URL. opencode consumes no
-- wire_api, so only the URL comes back.
local function providerEndpoint(prov)
  if type(prov) ~= "table" then return nil end
  local ep = prov.endpoints and prov.endpoints.openai or nil
  if type(ep) == "table" and ep.base_url then
    return ep.base_url
  end
  return nil
end

local function isLocalEndpoint(url)
  if type(url) ~= "string" then return false end
  return string.find(url, "://localhost") or
         string.find(url, "://127%.0%.0%.1") or
         string.find(url, "://host%.containers%.internal") or
         string.find(url, "://169%.254%.1%.2") or
         string.find(url, "://0%.0%.0%.0")
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

-- THE MODELS OF A MULTI-MAKER PROVIDER THIS AGENT CAN CALL. callableModels expands a provider's
-- `models` and `model_options` (for Bedrock, the declaration packs/bedrock/pack.json ships,
-- with the user's `providers.<name>` merged over it) into the ordered list of the entries this
-- agent's own client can call (docs/design/bedrock-plumbing.md OQ-BR9). Each entry declares its
-- maker as the `vendor` fact, and the maker is never parsed out of the id. `makers` is the set
-- of vendors this agent's client serves, nil meaning every one, and an entry that declares no
-- vendor (a user's string-form alias) is offered to every agent.
--
-- ONE ROW PER ID, as codexModelList builds it (docs/design/model-lists-and-pickers.md ML-D6):
-- the alias spelled as the id supplies its facts first, and every other alias naming the same
-- id fills only a fact still missing, in sorted alias order. A row's `facts` are those merged
-- model_options strings. The rows are ordered by the `order` fact (declared before undeclared),
-- then by id, so the first row is the provider's declared default among what this agent can
-- call, which is the fallback OQ-BR9's ruling names: "the first model that agent can call".
--
-- ⚠ DUPLICATED VERBATIM in packs/claude/derive.lua, packs/codex/derive.lua,
-- packs/opencode/derive.lua and packs/pi/derive.lua, because a derive cannot load another file
-- (the sandbox has no require and no io). internal/entrypoint/bedrock_model_list_test.go fails
-- when the copies differ.
local function callableModels(p, makers)
  if type(p) ~= "table" or type(p.models) ~= "table" then return {} end
  local opts = type(p.model_options) == "table" and p.model_options or {}
  local aliases = {}
  for alias in pairs(p.models) do
    if type(alias) == "string" then table.insert(aliases, alias) end
  end
  table.sort(aliases)
  local rows, byId = {}, {}
  local function absorb(id, alias)
    local r = byId[id]
    if not r then
      r = { id = id, facts = {} }
      byId[id] = r
      table.insert(rows, r)
    end
    local f = type(opts[alias]) == "table" and opts[alias] or {}
    for k, v in pairs(f) do
      if r.facts[k] == nil and type(v) == "string" and v ~= "" then r.facts[k] = v end
    end
  end
  for _, alias in ipairs(aliases) do
    if alias ~= "" and p.models[alias] == alias then absorb(alias, alias) end
  end
  for _, alias in ipairs(aliases) do
    local id = p.models[alias]
    if type(id) == "string" and id ~= "" and id ~= alias then absorb(id, alias) end
  end
  for _, r in ipairs(rows) do
    r.order = tonumber(r.facts.order)
    r.vendor = r.facts.vendor
  end
  table.sort(rows, function(a, b)
    if a.order and b.order and a.order ~= b.order then return a.order < b.order end
    if a.order and not b.order then return true end
    if b.order and not a.order then return false end
    return a.id < b.id
  end)
  local list = {}
  for _, r in ipairs(rows) do
    if r.vendor == nil or makers == nil or makers[r.vendor] then table.insert(list, r) end
  end
  return list
end

-- callableModel is the model this agent starts on among `list` (callableModels' answer for it):
-- the profile's `model` when it names, as an alias or as an id, an entry this agent can call,
-- or names an id the provider does not list at all (the user's own literal, passed through);
-- else the provider's `default` alias when this agent can call it; else, only when `pick` is
-- set, the first entry this agent can call. nil when none of those applies. A profile `model`
-- naming a listed entry this agent CANNOT call is skipped, never sent: that id is one this
-- agent's client would refuse.
--
-- `pick` is off for an agent whose own default on the service is already one of its models
-- (claude's Bedrock client picks an Anthropic model itself), because yolo picks a model only to
-- make a session valid (docs/design/model-lists-and-pickers.md OQ-ML2, ruled 2026-09-29).
local function callableModel(p, list, profile, pick)
  local models = (type(p) == "table" and type(p.models) == "table") and p.models or {}
  local callable, listed = {}, {}
  for _, e in ipairs(list) do callable[e.id] = true end
  for alias, id in pairs(models) do
    if type(id) == "string" then listed[id] = true end
    if type(alias) == "string" then listed[alias] = true end
  end
  local m = type(profile) == "table" and profile.model or nil
  if type(m) == "string" and m ~= "" and m ~= "default" then
    local id = models[m]
    if type(id) ~= "string" then id = m end
    if callable[id] then return id end
    if not listed[id] then return id end
  end
  local d = models["default"]
  if type(d) == "string" and callable[d] then return d end
  if pick and list[1] then return list[1].id end
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
    res.mcp = in_full(ctx, out)
  end

  -- 2. Providers
  if ctx.providers and next(ctx.providers) ~= nil then
    local provOut = {}
    for name, prov in pairs(ctx.providers) do
      local baseUrl = providerEndpoint(prov)
      -- VIA (docs/design/wire-bridge-gateway.md OQ-WG6/WG7): the selected provider's entry
      -- points at this agent's route on the service its profile names. opencode already speaks
      -- chat-completions to every provider here (@ai-sdk/openai-compatible), the protocol the
      -- via route passes through to the provider's own `openai` endpoint.
      local viaRow = (ctx.via_url ~= nil and ctx.via_url ~= "" and name == ctx.selected_provider)
      if viaRow then
        baseUrl = ctx.via_url
      end
      if baseUrl then
        local models = {}
        local cw = nil
        local maxTokens = nil
        if type(prov.options) == "table" then
          cw = tonumber(prov.options.context_window or prov.options.max_context_tokens)
          maxTokens = tonumber(prov.options.max_tokens or prov.options.max_output_tokens)
        end
        if type(prov.models) == "table" then
          for alias, modelId in pairs(prov.models) do
            local m = { name = alias }
            if cw or maxTokens then
              local limit = {}
              if cw then limit.context = cw end
              if maxTokens then limit.output = maxTokens end
              m.limit = limit
            end
            models[modelId] = m
          end
        end
        -- D10: opencode's Info schema declares baseURL/apiKey inside `options` only
        -- (packages/core/src/v1/config/provider.ts), the loader merges only
        -- `provider.options` into what the SDK sees, and resolveSDK reads
        -- `{ ...provider.options }` — a top-level spelling lists in /models and never
        -- reaches the SDK ("undefined/chat/completions cannot be parsed as a URL", zero
        -- requests). npm and models stay top-level: those ARE the two top-level fields
        -- upstream reads. `{env:VAR}` stays valid under options — substitution applies to
        -- the whole config text at load, before the schema ever sees it.
        local entry = {
          npm = "@ai-sdk/openai-compatible",
          models = models,
        }
        entry.options = { baseURL = baseUrl }
        -- A VIA ROW SENDS THE VIA SERVICE'S CALLER TOKEN, never the provider's key: the
        -- service adds that upstream itself, and demands this launch's token of every caller
        -- (docs/reference/wire-bridge.md WB-D18). An entrypoint older than
        -- ctx.via_api_key_env_name hands nil, and the row keeps the provider's reference.
        local viaKey = viaRow and ctx.via_api_key_env_name or nil
        if viaKey ~= nil and viaKey ~= "" then
          entry.options.apiKey = "{env:" .. viaKey .. "}"
        elseif prov.api_key_env_name then
          entry.options.apiKey = "{env:" .. prov.api_key_env_name .. "}"
        elseif prov.api_key then
          entry.options.apiKey = prov.api_key
        elseif type(prov.options) == "table" and prov.options.api_key then
          entry.options.apiKey = prov.options.api_key
        elseif isLocalEndpoint(baseUrl) then
          entry.options.apiKey = "local"
        end
        provOut[name] = entry
      end
    end
    if next(provOut) ~= nil then
      res.provider = in_full(ctx, provOut)
    end
  end

  -- 3. The selection — `model`, opencode's OWN selection key, source-verified from the
  -- installed release (upstream v1.18.18, the tag the shipped binary reports):
  -- packages/core/src/v1/config/config.ts:74-76 declares it "Model to use in the format
  -- of provider/model", split on the FIRST slash at model.ts:33-39, and an unknown prefix
  -- is a ModelNotFoundError with no silent fallback (docs/reference/providers.md §3
  -- opencode row). The provider half is the catalog key above, the model half is a bare
  -- model id, and one slash joins them.
  --
  -- It travels under the RESERVED `selection` key of the computed layer, exactly as codex's
  -- and pi's do, and for the same reason: a plain computed key is re-asserted by every
  -- boot, and opencode lets a user change the model interactively mid-session, so a key
  -- yolo re-asserted would silently revert that choice on the next launch
  -- (docs/reference/providers.md §5.1, the hazard OQ-CS2 names). The stateful render
  -- takes the namespace, decides per key, and lifts the winner onto the surface root —
  -- opencode.json shows `model` at top level, where opencode reads it. The namespace is an
  -- implementation detail of the layer, never of the file.
  --
  -- The gate is the catalog's own: opencode consumes no wire_api, so "names a URL
  -- opencode can dial" (providerEndpoint) is the whole of reachability, and there is no
  -- second predicate to lift out the way codex and pi need one. That is load-bearing
  -- rather than tidy — an unknown provider prefix is a hard ModelNotFoundError in
  -- opencode, so a selection whose provider the catalog dropped would be a config that
  -- fails at first request, not a preference opencode quietly ignores.
  --
  -- OQ-CS2 is the GUARD, not a default: no active profile at opencode's CLI name, or a
  -- selected provider the gate drops, writes nothing — not a default, not a clear. The
  -- no-profile case is opencode's own, and what opencode owns is a persisted interactive
  -- choice (~/.local/state/opencode/model.json) that `model` unset falls back to.
  --
  -- The model half is the derive's business (OQ-CS3), and the fallback ladder is
  -- shortest-claim-first: the alias the active profile's `model` option names (OQ-CS4 —
  -- for opencode that is a key of the provider's models table, the same keying the
  -- catalog half above already builds); else the provider's declared `default` alias;
  -- else the ONE model it declares, where "which model" has only one possible answer;
  -- else nothing — and with `model` being a single key, nothing means NO selection at
  -- all, never a half one naming a provider with no model under it. The one-model
  -- answer belongs to the DEFAULT ask only: a profile that named an alias the provider
  -- does not declare asked a question the table cannot answer, and "some other model
  -- that happened to be alone" is not the answer — that is the same honest degradation
  -- as declaring nothing at all.
  if ctx.selected_provider ~= nil and ctx.selected_provider ~= "" then
    local p = ctx.providers and ctx.providers[ctx.selected_provider] or nil
    if providerEndpoint(p) then
      local alias = (ctx.profile and ctx.profile.model) or "default"
      local modelID = nil
      if type(p) == "table" and type(p.models) == "table" then
        if p.models[alias] then
          modelID = p.models[alias]
        elseif alias == "default" then
          local count
          -- The VALUES are the model ids (the keys are the aliases); see the catalog
          -- half above, which builds opencode's models table keyed the same way.
          for _, id in pairs(p.models) do
            modelID, count = id, (count or 0) + 1
          end
          if count ~= 1 then
            modelID = nil
          end
        end
      end
      if modelID then
        local sel = { model = ctx.selected_provider .. "/" .. modelID }
        local smallAlias = (ctx.profile and ctx.profile.small_model)
        local smallID = nil
        if smallAlias and type(p.models) == "table" then
          smallID = p.models[smallAlias] or smallAlias
        elseif type(p.models) == "table" then
          smallID = p.models.haiku or p.models.fast or p.models.small or modelID
        else
          smallID = modelID
        end
        sel.small_model = ctx.selected_provider .. "/" .. smallID
        -- THE MENU FOLLOWS THE SELECTION (docs/design/provider-credential-scope.md OQ-CN4,
        -- "both, named separately"): the credential gate withholds every other provider's
        -- key from opencode, and opencode registers a catalog row without an auth check, so
        -- without this its menu would still offer providers it can no longer call.
        -- enabled_providers is opencode's own HARD key — "When set, ONLY these providers
        -- will be enabled" — so this is the ergonomic half of the ruling, never a model list:
        -- it names the provider the profile selected and nothing else. It rides the
        -- selection beside `model` for two reasons: a deselect clears it with the model
        -- (OQ-PSW2), and it is written only when a model is, since narrowing the providers
        -- while opencode starts on its own persisted choice would disable the provider that
        -- choice names.
        sel.enabled_providers = { ctx.selected_provider }
        res.selection = sel
      end
    end
  end

  return res
end)
