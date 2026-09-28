-- pi: render ~/.pi/agent/models.json from declared providers, write the selection
-- pair into ~/.pi/agent/settings.json when a profile is active at pi's CLI name, and
-- project canonical mcp_servers into ~/.pi/agent/mcp-adapter.json.

-- The DIALECT MAP (docs/reference/providers.md §3.4 / OQ-PT1): yolo's canonical wire_api
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

-- The provider's URL for the protocol THIS agent speaks — `openai` first, then native
-- `openai-responses`, which are also packs/pi's declared protocol preference. ONE spelling:
-- the single-protocol `base_url` shorthand is deleted (protocol-resolution.md), because the
-- same bare field meant `openai` here and `anthropic` in claude's derive — one line of user
-- config, two contradictory readings, decided by whichever agent happened to consume it. A
-- user config carrying it is a validation refusal naming `endpoints.openai.base_url`.
-- Total over non-tables so the call site stays a one-line gate. Returns nil when the
-- provider names no URL an openai-speaking agent can use, which is what keeps that gate
-- honest: a provider whose only endpoint speaks a protocol pi cannot would otherwise emit
-- an entry with no URL — a provider it cannot reach.
local function providerEndpoint(prov)
  if type(prov) ~= "table" then return nil end
  local endpoints = prov.endpoints
  if type(endpoints) ~= "table" then return nil end
  for _, protocol in ipairs({"openai", "openai-responses"}) do
    local ep = endpoints[protocol]
    if type(ep) == "table" and ep.base_url then
      return ep.base_url, ep.wire_api
    end
  end
  return nil
end

-- piReachable is THE gate both halves below ask — can pi reach this provider at all — so
-- the catalog and the selection cannot grow two answers to the same question. It returns
-- the URL and the `api` value pi would use, or nil when the provider names no URL pi can
-- speak to.
--
-- "A protocol pi can speak" is the whole piDialect map, not one row of it, and that is the
-- sense in which this gate is wider than a chat-completions one: pi registers
-- openai-completions, openai-responses AND anthropic-messages, so a provider declaring
-- `wire_api = "anthropic"` reaches pi and is a catalog row — the shorthand form arrives
-- here through providerEndpoint with its wire_api intact, and translates to
-- anthropic-messages like any other protocol. What does NOT widen with it is the ENDPOINT
-- KEY: providerEndpoint resolves Pi's declared `openai` and `openai-responses` keys in
-- that preference order. An endpoints-only provider with neither names no URL for the
-- protocol Pi resolves to, so it is no row, and a
-- provider that loses its catalog row loses its selection key with it. Writing
-- defaultProvider for such a provider would name an id pi has no entry for — the
-- half-selection a shared gate exists to make unrepresentable.
--
-- Total over non-tables, like providerEndpoint: a selected name that is absent from the
-- composed table (a profile whose provider the table does not hold — which
-- creates no requirement of its own) reads as nil here, and nil selects nothing.
local function piReachable(prov)
  if type(prov) ~= "table" then return nil end
  local baseUrl, wireApi = providerEndpoint(prov)
  -- An endpoint's own wire_api is the per-protocol fact; the provider-level one only
  -- speaks for the shorthand.
  local api = piAPI(wireApi or prov.wire_api)
  if baseUrl and api then
    return baseUrl, api
  end
  return nil
end

-- THE COMPAT FACTS. What a server does and does not support is a SERVICE fact, so the
-- PROVIDER states it and this derive translates it into pi's own spelling (OQ-CS4: the
-- provider declares the knob, the consumer decides what it means). Nothing below detects
-- anything. There is no llama.cpp branch, no loopback-URL test and no provider-name check,
-- and both of those were live proposals: "every local URL gets these flags" assumes every
-- local server is llama.cpp-shaped and silently DOWNGRADES one that is not (bedrock-mantle
-- is a working local provider that would lose capability — docs/research/
-- local-model-endpoints.md), while a name check hardcodes a vendor into an agent's derive.
-- A provider that declares none of these options gets no `compat` key at all, which is
-- exactly the file it got before this map existed.
--
-- LEFT is the canonical option name a provider declares; RIGHT is the field pi reads, and
-- the spelling is the ENTIRE risk here: pi's compat schemas set no additionalProperties,
-- so a misspelled field is accepted, read by nothing, and changes no request — a feature
-- that silently does nothing, with no error anywhere. Every field name below is
-- transcribed character by character from pi's own schema, the openai-completions member
-- of ProviderCompatSchema (pi-coding-agent/dist/core/model-config.js,
-- OpenAICompletionsCompatSchema), and the VALUES packs/llamacpp declares are the block
-- pi's own built-in llama.cpp provider generates (dist/extensions/llama/provider.js,
-- toPiModel). Verified against the installed pi 0.85.1, 2026-09-18, which agrees flag for
-- flag with the 2026-09-02 re-verification in docs/research/local-model-endpoints.md —
-- including its correction that `supportsUsageInStreaming` is TRUE, the one flag on which
-- that doc's older example JSON is stale. That file is on the doc's own fast-moving list:
-- re-read it at whatever version ships rather than trusting this comment.
--
-- ONE BLOCK PER PROVIDER covers every model this derive emits for it: pi merges a
-- provider-level compat into each model as it composes them
-- (dist/core/provider-composer.js, mergeCompat(providerConfig.compat, definition.compat)).
local piCompatFields = {
  { option = "supports_store", field = "supportsStore", boolean = true },
  { option = "supports_developer_role", field = "supportsDeveloperRole", boolean = true },
  { option = "supports_reasoning_effort", field = "supportsReasoningEffort", boolean = true },
  { option = "supports_usage_in_streaming", field = "supportsUsageInStreaming", boolean = true },
  { option = "supports_strict_mode", field = "supportsStrictMode", boolean = true },
  { option = "max_tokens_field", field = "maxTokensField" },
}

-- providerOption reads one declared option off the provider, falling back to the active
-- profile's value for the provider that profile selects — the same two-step the context
-- window and max-tokens reads below take, and deliberately the same one: a provider's
-- option surface should not resolve by two rules in one file.
local function providerOption(prov, ctx, provName, name)
  if type(prov) == "table" and type(prov.options) == "table" and prov.options[name] ~= nil then
    return prov.options[name]
  end
  if ctx.selected_provider == provName and type(ctx.profile) == "table" then
    return ctx.profile[name]
  end
  return nil
end

-- An option value is always a STRING — core refuses anything but a string or a null in an
-- options map — so a boolean service fact arrives as "true"/"false", the JSON spellings,
-- and only those. Anything else reads as UNDECLARED rather than as false: pi's default for
-- an absent flag is the permissive one, so turning a typo into `false` would silently
-- switch a capability OFF, which is worse than emitting nothing. It is the disposition
-- tonumber already gives an unparseable context_window.
local function piCompatBoolean(v)
  if v == "true" then return true end
  if v == "false" then return false end
  return nil
end

-- piCompatBlock returns the provider's compat table, or nil when it declares none of the
-- facts. The nil is the additive half of the ruling: a provider that says nothing renders
-- byte-for-byte the models.json it rendered before.
local function piCompatBlock(prov, ctx, provName)
  local compat = nil
  for _, row in ipairs(piCompatFields) do
    local raw = providerOption(prov, ctx, provName, row.option)
    local value = nil
    if row.boolean then
      value = piCompatBoolean(raw)
    elseif type(raw) == "string" and raw ~= "" then
      value = raw
    end
    if value ~= nil then
      compat = compat or {}
      compat[row.field] = value
    end
  end
  return compat
end

-- THE MODEL-CAPABILITY FACTS (vision, reasoning, cost). `piCompatFields` is a
-- PROVIDER-level block pi merges into every model; these three are MODEL-level fields, so a
-- value declared for an alias is attached to that alias's emitted model. The source is the
-- provider's `model_options.<alias>` map — the object-form `models.<alias>` entry, lowered to
-- the flat string option vocabulary by packload.liftModelFacts — with a per-alias value
-- winning over the provider's `options`, which is the FALLBACK: a fact common to every model
-- it serves is declared once, and only the model that differs overrides it. A profile, which
-- names one alias, is the wrong scope for a per-model fact; the provider fallback still
-- reaches it through providerOption for the active provider.
--
-- The names are yolo-flat because a provider's `options` is a flat name→default map whose
-- values core never validates (docs/reference/providers.md §"Profiles and options") and an
-- option VALUE is a STRING. That second half is NOT in that section — it is
-- `packdecl.OptionDefault`, which decodes a string or a null and refuses everything else,
-- and `yolo config-ref`'s `profiles` entry states it for the user side. So pi's nested cost
-- object is spelled as four scalar options and modalities as a comma list:
--
--   reasoning          "true" | "false"
--   input              comma-separated, e.g. "text,image"
--   cost_input         dollars per million input tokens
--   cost_output        dollars per million output tokens
--   cost_cache_read    dollars per million cache-read tokens
--   cost_cache_write   dollars per million cache-write tokens
--
-- ADDITIVE, not defaulting. pi's own defaults for an undeclared fact are reasoning=false,
-- input={"text"} and cost={0,0,0,0} (provider-composer.js, modelFromJson) — so an alias that
-- declares none of these renders byte-for-byte the models.json row it rendered before this
-- map existed, which is the same additive rule the compat block follows. Widening a typo
-- into a capability OFF is the failure mode piCompatBoolean already refuses for the compat
-- flags; it is reused here rather than re-derived.
--
-- A cost is emitted only when at least one rate is declared, and the undeclared ones are
-- filled with 0: pi's ModelCostSchema requires all four keys, and pi discards the WHOLE FILE
-- on a schema failure (`models.json is invalid` — the same all-or-nothing that makes the
-- empty `models` key omitted below), so a partial object is worse than a defaulted one.
local function piModelFacts(mopts, prov, ctx, provName)
  local function fact(name)
    if type(mopts) == "table" and mopts[name] ~= nil then
      return mopts[name]
    end
    return providerOption(prov, ctx, provName, name)
  end
  local facts = nil
  local reasoning = piCompatBoolean(fact("reasoning"))
  if reasoning ~= nil then
    facts = facts or {}
    facts.reasoning = reasoning
  end
  local input = fact("input")
  if type(input) == "string" and input ~= "" then
    local mods, seen = {}, {}
    for token in string.gmatch(input, "[^,%s]+") do
      if (token == "text" or token == "image") and not seen[token] then
        seen[token] = true
        table.insert(mods, token)
      end
    end
    if #mods > 0 then
      facts = facts or {}
      facts.input = mods
    end
  end
  local cost = nil
  local costFields = {
    { option = "cost_input", field = "input" },
    { option = "cost_output", field = "output" },
    { option = "cost_cache_read", field = "cacheRead" },
    { option = "cost_cache_write", field = "cacheWrite" },
  }
  for _, row in ipairs(costFields) do
    local rate = tonumber(fact(row.option))
    if rate ~= nil then
      cost = cost or { input = 0, output = 0, cacheRead = 0, cacheWrite = 0 }
      cost[row.field] = rate
    end
  end
  if cost then
    facts = facts or {}
    facts.cost = cost
  end
  return facts
end

local function isLocalEndpoint(url)
  if type(url) ~= "string" then return false end
  return string.find(url, "://localhost") or
         string.find(url, "://127%.0%.0%.1") or
         string.find(url, "://host%.containers%.internal") or
         string.find(url, "://169%.254%.1%.2") or
         string.find(url, "://0%.0%.0%.0")
end

local function isKiloEndpoint(url)
  if type(url) ~= "string" then return false end
  return string.find(url, "api%.kilo%.ai") ~= nil
end

local function normalizeKiloModel(modelId)
  if type(modelId) ~= "string" or modelId == "" then return modelId end
  if string.find(modelId, "^deepseek%-") and not string.find(modelId, "/") then
    return "deepseek/" .. modelId
  end
  return modelId
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

-- THE openai-codex MODEL LIST. codexModelList expands the one declaration of it — the
-- `models` and `model_options` packs/openai-auth/pack.json ships on the openai-codex provider,
-- with the user's `providers.openai-codex` merged over it — into the ordered list every
-- consumer presents (docs/design/model-lists-and-pickers.md ML-D1). `p` is
-- ctx.providers["openai-codex"]; anything without a `models` table expands to {}.
--
-- Each declared id is WIRE-TRUE. Its facts come from model_options under the alias spelled
-- as the id: `order` (the map is unordered all the way here, so this is the only order there
-- is; unordered ids go last, by id), `name`, `description`, `context_window`, and
-- `long_context_window`, which means "this model also has a 1M variant". That variant is
-- emitted right after its base as `<id>[1m]`, a CLIENT spelling Claude Code, packs/pi's
-- extension and the wire bridge each strip before the request leaves.
--
-- ⚠ DUPLICATED VERBATIM in packs/claude/derive.lua, packs/pi/derive.lua and
-- packs/codex/derive.lua, because a derive cannot load another file (the sandbox has no
-- require and no io). internal/entrypoint/codex_model_list_test.go fails when the copies
-- differ, and when any consumer stops reading the declaration.
local function codexModelList(p)
  if type(p) ~= "table" or type(p.models) ~= "table" then return {} end
  local opts = type(p.model_options) == "table" and p.model_options or {}
  local aliases = {}
  for alias in pairs(p.models) do
    if type(alias) == "string" then table.insert(aliases, alias) end
  end
  table.sort(aliases)
  local function text(v)
    if type(v) == "string" and v ~= "" then return v end
    return nil
  end
  -- ONE ROW PER ID, and the alias spelled as the id carries its facts. Another alias naming
  -- the same id (a `default` or `fast` the user added) fills only a fact that row still
  -- lacks, in sorted alias order, so adding one moves nothing. Both of those names sort
  -- before every declared id, and a first-alias-wins walk handed the id the new alias's
  -- facts, which are none (docs/design/model-lists-and-pickers.md ML-D6).
  local rows, byId = {}, {}
  local function absorb(id, alias)
    local r = byId[id]
    if not r then
      r = { id = id }
      byId[id] = r
      table.insert(rows, r)
    end
    local f = type(opts[alias]) == "table" and opts[alias] or {}
    if r.order == nil then r.order = tonumber(f.order) end
    if r.name == nil then r.name = text(f.name) end
    if r.description == nil then r.description = text(f.description) end
    if r.context_window == nil then r.context_window = tonumber(f.context_window) end
    if r.long_context_window == nil then r.long_context_window = tonumber(f.long_context_window) end
  end
  for _, alias in ipairs(aliases) do
    if alias ~= "" and p.models[alias] == alias then absorb(alias, alias) end
  end
  for _, alias in ipairs(aliases) do
    local id = p.models[alias]
    if type(id) == "string" and id ~= "" and id ~= alias then absorb(id, alias) end
  end
  table.sort(rows, function(a, b)
    if a.order and b.order and a.order ~= b.order then return a.order < b.order end
    if a.order and not b.order then return true end
    if b.order and not a.order then return false end
    return a.id < b.id
  end)
  local list = {}
  for _, r in ipairs(rows) do
    table.insert(list, {
      id = r.id,
      name = r.name,
      description = r.description,
      context_window = r.context_window,
    })
    if r.long_context_window then
      table.insert(list, {
        id = r.id .. "[1m]",
        base = r.id,
        name = r.name and (r.name .. " (1M context)"),
        description = r.description and (r.description .. " · 1M context"),
        context_window = r.long_context_window,
      })
    end
  end
  return list
end

-- codexDefault is the model a codex-profile launch starts on, by ONE rule in every consumer:
-- the profile's `model` option unless it is absent or "default", else the first declared id,
-- which is always a base id. nil when the list is empty and the profile names nothing.
local function codexDefault(list, profile)
  local m = type(profile) == "table" and profile.model or nil
  if type(m) == "string" and m ~= "" and m ~= "default" then return m end
  return list[1] and list[1].id
end

-- models: the catalog. Returning {} writes no row and leaves the surface's declared default,
-- `{"providers": {}}` (packs/pi/pack.json; docs/design/host-computed-layer.md HC-D1), because
-- pi 0.87.1's ModelsConfigSchema requires `providers` and reports a file without it as a
-- "models.json error" at every start. A pi-only jail reaches this with a non-empty table:
-- openai-codex is its only provider and is never catalogued, below.
yolo.derive("pi", "models", function(ctx)
  if not ctx.providers or next(ctx.providers) == nil then
    return {}
  end
  local providers = {}
  for name, prov in pairs(ctx.providers) do
    -- openai-codex is pi's BUILT-IN subscription provider, so it is never catalogued here
    -- (docs/design/pi-codex-provider-shadowing.md OQ-1, OQ-2): packs/openai-auth declares a
    -- Responses address on it, and a row written from that shadows pi's own client, which
    -- then authenticates with the ambient OPENAI_API_KEY and gets 401s. Excluded by name, as
    -- packs/codex/derive.lua does; the settings derive still selects it by name.
    local native = (name == "openai-codex")
    local baseUrl, api = nil, nil
    if not native then
      baseUrl, api = piReachable(prov)
    end
    -- VIA (docs/design/wire-bridge-gateway.md OQ-WG6/WG7): when this agent's active profile
    -- routes through a service, the SELECTED provider's row points at the per-agent route the
    -- service serves, and speaks chat-completions there, the protocol the via route passes
    -- through to the provider's own `openai` endpoint. Every other row is untouched: via is
    -- one profile's choice, and only the selected provider rides it.
    local viaRow = (not native and ctx.via_url ~= nil and ctx.via_url ~= "" and
      name == ctx.selected_provider)
    if viaRow then
      baseUrl, api = ctx.via_url, "openai-completions"
    end
    if baseUrl then
      local isKilo = (name == "kilo" or isKiloEndpoint(baseUrl))
      local modelList = {}
      local cw = nil
      local maxTokens = nil
      if type(prov.options) == "table" then
        cw = tonumber(prov.options.context_window or prov.options.max_context_tokens)
        maxTokens = tonumber(prov.options.max_tokens or prov.options.max_output_tokens)
      end
      if not cw and ctx.selected_provider == name and type(ctx.profile) == "table" then
        cw = tonumber(ctx.profile.context_window or ctx.profile.max_context_tokens)
      end
      if not maxTokens and ctx.selected_provider == name and type(ctx.profile) == "table" then
        maxTokens = tonumber(ctx.profile.max_tokens or ctx.profile.max_output_tokens)
      end
      if type(prov.models) == "table" then
        local aliases = {}
        for alias in pairs(prov.models) do
          table.insert(aliases, alias)
        end
        table.sort(aliases)
        for _, alias in ipairs(aliases) do
          local rawModelId = prov.models[alias]
          local modelId = isKilo and normalizeKiloModel(rawModelId) or rawModelId
          local m = { id = modelId, name = alias }
          local mopts = type(prov.model_options) == "table" and prov.model_options[alias] or nil
          -- `name` is the display name; the alias is the default, a declared one overrides.
          if type(mopts) == "table" and type(mopts.name) == "string" and mopts.name ~= "" then
            m.name = mopts.name
          end
          local modelCw = cw
          local modelMaxTokens = maxTokens
          if type(mopts) == "table" then
            modelCw = tonumber(mopts.context_window or mopts.max_context_tokens) or modelCw
            modelMaxTokens = tonumber(mopts.max_tokens or mopts.max_output_tokens) or modelMaxTokens
          end
          if not modelCw and isKilo and (modelId == "deepseek/deepseek-v4.1-flash" or string.find(modelId, "^deepseek/")) then
            modelCw = 1048576
          end
          if modelCw then
            m.contextWindow = modelCw
          end
          if modelMaxTokens then
            m.maxTokens = modelMaxTokens
          end
          local facts = piModelFacts(mopts, prov, ctx, name)
          if facts then
            for k, v in pairs(facts) do m[k] = v end
          end
          table.insert(modelList, m)
        end
      end
      if #modelList == 0 and isKilo and ctx.selected_provider == name and type(ctx.profile) == "table" and ctx.profile.model then
        local modelId = normalizeKiloModel(ctx.profile.model)
        local modelCw = cw
        if not modelCw and (modelId == "deepseek/deepseek-v4.1-flash" or string.find(modelId, "^deepseek/")) then
          modelCw = 1048576
        end
        local m = { id = modelId, name = ctx.profile.model }
        if modelCw then
          m.contextWindow = modelCw
        end
        if maxTokens then
          m.maxTokens = maxTokens
        end
        table.insert(modelList, m)
      end
      local entry = {
        baseUrl = baseUrl,
        api = api,
      }
      -- THE KEY IS OMITTED WHEN THERE ARE NO MODELS, and that is a hard requirement rather
      -- than tidiness: pi's `models` is an optional ARRAY (ProviderConfigSchema), an empty
      -- Lua table marshals back as a JSON OBJECT (luahook/marshal.go — `{}` is ambiguous in
      -- Lua and the config model resolves it to the object), and a schema failure does not
      -- drop the offending ROW, it discards the WHOLE FILE and returns an empty provider
      -- map. So one address-only provider deleted every other provider's catalog row, which
      -- surfaced as `models: must be array` plus "No models match pattern" for a model the
      -- same file named. Every provider that declares an address and no model list is in
      -- this class: kilo whenever no profile names one, and a user's own `endpoints.openai`
      -- (openai-codex was too, and is now never catalogued at all — see the loop's head).
      -- Omitting it is also the right STATEMENT — pi merges a models.json row into its
      -- built-in catalog for that provider (core/provider-composer.js, applyModelsJson),
      -- so "no models of my own" leaves pi's own list intact and still applies the address.
      -- packs/omp/derive.lua guards the same way for the same reason.
      if #modelList > 0 then
        entry.models = modelList
      end
      -- The provider's own compat facts, translated (piCompatFields). Emitted for ANY
      -- provider that declares them and for no provider that does not — this is the one
      -- place the block is attached, and it is attached by declaration alone.
      local compat = piCompatBlock(prov, ctx, name)
      if compat then
        entry.compat = compat
      end
      -- D11: pi has no `apiKeyEnv` field — ProviderConfigSchema is name, baseUrl, apiKey,
      -- api, oauth, headers, compat, authHeader, models, modelOverrides, and nothing in the
      -- package reads one, so the name we used to write here was dead configuration that
      -- read as the thing delivering the credential. pi's env indirection is the config-value
      -- syntax ON apiKey (`${VAR}`; docs/custom-provider.md — the maintainer's own hand-written
      -- models.json uses it), and pi expands it at read time, so yolo writes the reference
      -- verbatim and the consumer resolves it. For an unkeyed local provider, pi filters out
      -- models without a credential unless a dummy apiKey is provided (docs/research/local-model-endpoints.md).
      --
      -- A VIA ROW SENDS THE VIA SERVICE'S CALLER TOKEN, never the provider's key: the service
      -- adds that upstream itself, and demands this launch's token of every caller
      -- (docs/reference/wire-bridge.md WB-D18). ctx.via_api_key_env_name names the variable;
      -- an entrypoint older than the field hands nil, and the row then keeps the provider's
      -- reference as it did before.
      local viaKey = viaRow and ctx.via_api_key_env_name or nil
      if viaKey ~= nil and viaKey ~= "" then
        entry.apiKey = "${" .. viaKey .. "}"
      elseif prov.api_key_env_name then
        entry.apiKey = "${" .. prov.api_key_env_name .. "}"
      elseif prov.api_key then
        entry.apiKey = prov.api_key
      elseif type(prov.options) == "table" and prov.options.api_key then
        entry.apiKey = prov.options.api_key
      elseif isLocalEndpoint(baseUrl) then
        entry.apiKey = "local"
      end
      providers[name] = entry
    end
  end
  if next(providers) == nil then
    return {}
  end
  return { providers = in_full(ctx, providers) }
end)

-- The selection — defaultProvider and defaultModel, pi's OWN selection keys, verified from
-- the published package the launcher installs (pi 0.84.4, npm-extracted, the CLI never
-- run): dist/core/settings-manager.d.ts:71-72 declares the pair, the ids match EXACTLY
-- (`===`) against the provider's model list, and pi's own interactive writer persists
-- exactly this pair (core/settings-manager.js:460-475) — so the pair this writes is
-- byte-for-byte the shape pi itself would write, never a yolo spelling of it
-- (docs/reference/providers.md §3 pi row). Two verification notes that shaped the
-- surface rather than this function: a project-scope twin (.pi/settings.json in the
-- working directory) deep-merges over the global file, so the GLOBAL file is the right
-- surface for a jail-wide default; and pi resolves a saved default only when the
-- provider's credential is configured, which is D11's `apiKey: "${VAR}"` fix, already
-- landed in the catalog half above.
--
-- The pair travels under the RESERVED `selection` key of the computed layer, exactly as
-- codex's does (packs/codex/derive.lua), and for the same reason. A plain computed key is
-- re-asserted by every boot — right for models.json, which is yolo's own output, and
-- exactly wrong for a model the user can change interactively mid-session — so a key yolo
-- re-asserted would silently revert their choice on the next launch
-- (docs/reference/providers.md §5.1, the hazard OQ-CS2 names). The stateful render
-- takes the namespace, decides per key — write on activation, never on absence, and a
-- user's interactive edit stands until a NEW selection value differs from the last one
-- yolo wrote — and lifts the winners onto the surface root, so settings.json shows
-- defaultProvider/defaultModel at top level where pi reads them. The namespace is an
-- implementation detail of the layer, never of the file.
--
-- OQ-CS2 is the GUARD, not a default: when no profile is active at pi's CLI name, nothing
-- selection-shaped is written — not a default, not a clear; the no-profile case is the
-- agent's own (pi's own persisted interactive choice stands). And when the selected
-- provider is not pi-reachable, the SAME gate that keeps it out of the catalog above keeps
-- it out of the selection: no keys at all, never a defaultProvider naming a provider whose
-- catalog row the same gate dropped.
--
-- defaultModel is the id under the alias the active profile's `model` option names
-- (OQ-CS4: what an option means is the derive's business, and for pi that meaning is a
-- key of the provider's own models table), falling back to the provider's declared
-- `default` alias when the profile carries no option (OQ-CS3: the fallback is the
-- derive's business, and `default` stays an ordinary open-vocabulary alias). It is
-- omitted when the provider declares no models or names no such alias, leaving pi to
-- resolve its own model within the named provider — model ids must match the provider's
-- list exactly, so guessing one would be a selection pi refuses at resolution time.
yolo.derive("pi", "settings", function(ctx)
  if ctx.selected_provider == nil or ctx.selected_provider == "" then
    return {}
  end
  local p = ctx.providers and ctx.providers[ctx.selected_provider] or nil
  -- openai-codex is Pi's built-in subscription provider, so the catalog above writes NO
  -- row for it (docs/design/pi-codex-provider-shadowing.md OQ-1). Its address, wire and
  -- model definitions are the ones packs/pi/extensions/yolo-openai-auth.js registers: the
  -- broker-backed OAuth login, the openai-codex-responses client at chatgpt.com/backend-api,
  -- and the model list — the ONE declared list (codexModelList above), which that extension
  -- reads from the pi/codex-models surface below. This branch speaks only for the SELECTION
  -- within that list — the default pair and the pi-subagents policy. The shipped codex
  -- profile selects the declared default; a user profile may state another exact model id
  -- from that list as `model`.
  --
  -- NO enabledModels, and that is the ruling, not an omission
  -- (docs/design/model-lists-and-pickers.md ML-D2; the maintainer, 2026-09-27: "stop
  -- filling in scoped so there's only all"). pi's "all" view for openai-codex IS the
  -- declared list, because the extension registers exactly it, so a scope yolo wrote could
  -- only restate that list a second time — which is how the two lists drifted. pi shows its
  -- Scope toggle only when a scope exists, and with none a fresh session starts on the
  -- defaultProvider/defaultModel pair below (findInitialModel's saved-default step). A scope
  -- an earlier launch wrote is cleared by the selection's own deselect rule (OQ-PSW2); one
  -- the user or pi's save-as-default edited is kept.
  if ctx.selected_provider == "openai-codex" then
    local list = codexModelList(p)
    local model = codexDefault(list, ctx.profile)
    local allow = {}
    for _, e in ipairs(list) do
      table.insert(allow, "openai-codex/" .. e.id)
    end
    -- Pi-subagents has its own default, independent of Pi's chat selection.
    -- Computed output is intentional here: selection can only lift scalar keys,
    -- while this structured policy must reject legacy explicit workflow models.
    --
    -- The allow list is the declared ids, EXACTLY (docs/design/model-lists-and-pickers.md
    -- ML-D5), not a family glob written down a third time. pi-subagents 0.35.1's
    -- globToRegExp escapes `[` and `]` before it turns `*` into a wildcard
    -- (src/runs/shared/model-scope.ts), so `openai-codex/<id>[1m]` matches literally. An
    -- EMPTY list writes no modelScope at all: an enforced, strict, empty allow would refuse
    -- every child model.
    local subagents = { defaultProvider = "openai-codex" }
    if model then
      subagents.defaultModel = "openai-codex/" .. model
    end
    if #allow > 0 then
      subagents.modelScope = { enforce = true, strict = true, allow = allow }
    end
    return {
      subagents = subagents,
      selection = { defaultProvider = "openai-codex", defaultModel = model },
    }
  end
  if not piReachable(p) then
    return {}
  end
  local isKilo = (ctx.selected_provider == "kilo" or (type(p) == "table" and type(p.endpoints) == "table" and type(p.endpoints.openai) == "table" and isKiloEndpoint(p.endpoints.openai.base_url or "")))
  local alias = (ctx.profile and ctx.profile.model) or (type(p) == "table" and type(p.options) == "table" and p.options.model) or "default"
  local sel = { defaultProvider = ctx.selected_provider }
  if type(p) == "table" and type(p.models) == "table" then
    if p.models[alias] then
      sel.defaultModel = p.models[alias]
    elseif p.models["default"] then
      sel.defaultModel = p.models["default"]
    else
      for _, modelId in pairs(p.models) do
        if modelId == alias then
          sel.defaultModel = modelId
          break
        end
      end
    end
  end
  if not sel.defaultModel and isKilo and (type(p.models) ~= "table" or next(p.models) == nil) and alias ~= "default" then
    sel.defaultModel = alias
  end
  if isKilo and sel.defaultModel then
    sel.defaultModel = normalizeKiloModel(sel.defaultModel)
  end
  local enabled = {}
  if type(p) == "table" and type(p.models) == "table" and next(p.models) ~= nil then
    local seen = {}
    local modelIds = {}
    for _, modelId in pairs(p.models) do
      if isKilo then
        modelId = normalizeKiloModel(modelId)
      end
      if not seen[modelId] then
        table.insert(modelIds, modelId)
        seen[modelId] = true
      end
    end
    table.sort(modelIds)
    -- THE DEFAULT LEADS, and the first slot is not presentation: pi starts a fresh
    -- session on the FIRST enabledModel whenever the saved defaultProvider/defaultModel
    -- pair fails to resolve or sits outside the scope (buildSessionOptions in pi 0.87.1's
    -- dist/main.js, "otherwise first scoped model"), so a purely sorted list put the
    -- alphabetically first id there — glm-4.6, the OLDEST model zai serves, not its declared
    -- default. Rotate the selection's model to the front; the rest keep their sorted
    -- order, so the list stays deterministic. A nil defaultModel (no alias resolved)
    -- leaves the sorted order untouched, and an id that is somehow absent from the
    -- list rotates nothing rather than inventing an entry.
    if sel.defaultModel then
      for i, modelId in ipairs(modelIds) do
        if modelId == sel.defaultModel then
          table.remove(modelIds, i)
          table.insert(modelIds, 1, modelId)
          break
        end
      end
    end
    for _, modelId in ipairs(modelIds) do
      table.insert(enabled, ctx.selected_provider .. "/" .. modelId)
    end
  elseif sel.defaultModel then
    table.insert(enabled, ctx.selected_provider .. "/" .. sel.defaultModel)
  else
    table.insert(enabled, ctx.selected_provider .. "/*")
  end
  -- enabledModels travels under the selection, not the computed layer. pi's own /model
  -- scoping writes the same key, and a computed key is re-asserted every boot, which would
  -- revert the user's scoped list on the next launch. Under the selection it takes the
  -- pair's rules: written on activation, a user edit kept, yolo's own list cleared on
  -- deselect (OQ-PSW2). An array is a leaf there, replaced whole.
  sel.enabledModels = enabled
  return { selection = sel }
end)

-- codex-models (~/.pi/agent/yolo-openai-codex-models.json): the openai-codex model list
-- packs/pi/extensions/yolo-openai-auth.js registers, as data. The extension cannot read the
-- declaration itself — it is JavaScript pi loads, not a derive — and this surface is the
-- one channel that reaches it (docs/design/model-lists-and-pickers.md ML-D3): an env var
-- composes nothing without a profile while the extension registers the provider always, a
-- models.json row for openai-codex is the shadow OQ-1/OQ-2 of
-- docs/design/pi-codex-provider-shadowing.md forbid, and parsing YOLO_PROVIDERS in the
-- extension would be a second copy of codexModelList.
--
-- In a jail it renders WHETHER OR NOT a profile is active, because the extension registers
-- the provider on every launch. `yolo host apply` runs no derive for content, so at the host
-- this surface is `{}` under `assert` and refused under `own`, and pi keeps its own
-- openai-codex catalog there (docs/design/model-lists-and-pickers.md ML-D8, OQ-ML3). Each entry carries only what yolo declares — id, the [1m]
-- variant's base, the display name and the context window; the extension takes every
-- pi-dialect fact (cost tiers, thinking levels, compat) from pi's own catalog, looked up
-- by `base` or `id`. An empty list renders `{}`, and the extension then registers no
-- models of its own, which leaves pi's built-in openai-codex catalog in place.
yolo.derive("pi", "codex-models", function(ctx)
  local list = codexModelList(ctx.providers and ctx.providers["openai-codex"])
  if #list == 0 then return {} end
  local models = {}
  for _, e in ipairs(list) do
    table.insert(models, { id = e.id, base = e.base, name = e.name, contextWindow = e.context_window })
  end
  return { models = models }
end)

-- mcp: passthrough — canonical mcp_servers lands verbatim under mcpServers
-- in ~/.pi/agent/mcp-adapter.json, where pi-mcp-adapter / pi-mcp-extension consumes it.
yolo.derive("pi", "mcp", function(ctx)
  return { mcpServers = in_full(ctx, ctx.mcp_servers) }
end)

-- subagents-mcp (~/.config/mcp/mcp.json): the same servers again, for pi-subagents, which
-- resolves an agent's `mcp:` direct tools from ~/.config/mcp/mcp.json, agentDir/mcp.json and
-- the project's .mcp.json and .pi/mcp.json, and never from mcp-adapter.json (pi-subagents
-- 0.35.1, src/runs/shared/mcp-direct-tool-allowlist.ts, getConfigPaths). The surface renders
-- only while pi-subagents is in pi's packages (its `whenListed`), and never at the host (its
-- `notAtHost`); docs/design/agent-directory-map.md AM-R2 records why this path.
--
-- NOT declared in full, unlike the mcp surface above, and on purpose: the file is a
-- cross-tool location a user may already keep (pi-mcp-adapter's own setup writes it), so the
-- stateful render's first boot must adopt the servers already there as the user's rather than
-- claim the whole table as yolo's previous output (CO13). A server yolo stops configuring
-- still leaves, because the capture baseline knows it was yolo's.
yolo.derive("pi", "subagents-mcp", function(ctx)
  return { mcpServers = ctx.mcp_servers }
end)
